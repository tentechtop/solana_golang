package consensus

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"time"

	"solana_golang/runtime"
	"solana_golang/structure"
	"solana_golang/utils"
)

const (
	MaxProposalTransactions = 1000
)

// BlockHeader 描述 PoS 区块头 + 补齐 QC、epoch、leader 签名所需字段。
type BlockHeader struct {
	ChainID            string
	Slot               uint64
	Height             uint64
	ParentHash         structure.Hash
	PreviousQCHash     structure.Hash
	LeaderID           ValidatorID
	EpochID            uint64
	TxRoot             structure.Hash
	ReceiptRoot        structure.Hash
	RewardRoot         structure.Hash
	StateRoot          structure.Hash
	AccountRoot        structure.Hash
	TimestampUnixMilli int64
}

// BlockProposal 描述区块提案 + 由 leader 对 header 签名后广播。
type BlockProposal struct {
	Header          BlockHeader
	Transactions    []structure.Transaction
	RewardQCs       []QuorumCertificate
	Evidence        []SlashingEvidence
	Rewards         []BlockReward
	LeaderSignature structure.Signature
}

// ChainState 保存本地账户状态 + 单进程测试中代替数据库快照。
type ChainState struct {
	Accounts []structure.AddressedAccount
}

// BlockProducer 生产区块 + 只依赖 runtime 执行接口和本地账户快照。
type BlockProducer struct {
	ChainID  string
	Executor runtime.TransactionExecutor
}

// ProposalVerifier 验证区块 + 验签、leader、交易执行和状态根必须全部通过。
type ProposalVerifier struct {
	ChainID  string
	Executor runtime.TransactionExecutor
}

// ProduceBlock 生产区块 + 执行成功交易进入区块并更新状态根。
func (producer BlockProducer) ProduceBlock(
	contextValue context.Context,
	request ProduceBlockRequest,
) (BlockProposal, ChainState, error) {
	if err := request.validate(); err != nil {
		return BlockProposal{}, ChainState{}, err
	}
	if producer.Executor == nil {
		return BlockProposal{}, ChainState{}, fmt.Errorf("consensus: nil transaction executor")
	}

	nextState := request.ParentState.clone()
	includedTransactions := make([]structure.Transaction, 0, len(request.Transactions))
	receipts := make([]structure.Hash, 0, len(request.Transactions))
	feeDetails := make([]structure.FeeDetails, 0, len(request.Transactions))
	processedTransactionIDs := make(map[string]struct{}, len(request.Transactions))
	for transactionIndex, transaction := range request.Transactions {
		if len(includedTransactions) >= MaxProposalTransactions {
			break
		}
		result, err := producer.executeTransaction(contextValue, request, transaction, nextState, processedTransactionIDs)
		if err != nil {
			return BlockProposal{}, ChainState{}, fmt.Errorf("consensus: execute transaction %d: %w", transactionIndex, err)
		}
		if result.Execution.Status != structure.TransactionStatusConfirmed {
			continue
		}
		transactionID, err := transaction.TxIDString()
		if err != nil {
			return BlockProposal{}, ChainState{}, fmt.Errorf("consensus: transaction id %d: %w", transactionIndex, err)
		}
		processedTransactionIDs[transactionID] = struct{}{}
		nextState = nextState.applyWrites(result.Execution.WrittenAccounts)
		includedTransactions = append(includedTransactions, markTransactionConfirmed(transaction, result.Execution.FeeDetails.TotalFee))
		feeDetails = append(feeDetails, result.Execution.FeeDetails)
		receiptHash, err := hashReceipt(result.Execution)
		if err != nil {
			return BlockProposal{}, ChainState{}, fmt.Errorf("consensus: hash receipt %d: %w", transactionIndex, err)
		}
		receipts = append(receipts, receiptHash)
	}
	leaderID, err := request.Schedule.LeaderForSlot(request.Slot)
	if err != nil {
		return BlockProposal{}, ChainState{}, err
	}
	leader, exists := request.EpochSnapshot.ValidatorByID(leaderID)
	if !exists {
		return BlockProposal{}, ChainState{}, fmt.Errorf("consensus: block leader not in epoch snapshot")
	}
	nextState, rewards, err := ApplyBlockRewards(nextState, BlockRewardInput{
		Slot:          request.Slot,
		ParentSlot:    request.ParentSlot,
		Height:        request.Height,
		EpochID:       request.EpochSnapshot.EpochID,
		EpochSnapshot: request.EpochSnapshot,
		Schedule:      request.Schedule,
		Leader:        leader,
		FeeDetails:    feeDetails,
		RewardQCs:     request.RewardQCs,
		Evidence:      request.Evidence,
		Config:        request.RewardConfig,
	})
	if err != nil {
		return BlockProposal{}, ChainState{}, err
	}

	header, err := buildProposalHeader(producer.ChainID, request, includedTransactions, receipts, rewards, nextState)
	if err != nil {
		return BlockProposal{}, ChainState{}, err
	}
	signData, err := header.SignBytes()
	if err != nil {
		return BlockProposal{}, ChainState{}, err
	}
	signature, err := request.LeaderKeyPair.Sign(signData)
	if err != nil {
		return BlockProposal{}, ChainState{}, fmt.Errorf("consensus: sign proposal: %w", err)
	}
	return BlockProposal{
		Header:          header,
		Transactions:    includedTransactions,
		RewardQCs:       append([]QuorumCertificate(nil), request.RewardQCs...),
		Evidence:        append([]SlashingEvidence(nil), request.Evidence...),
		Rewards:         append([]BlockReward(nil), rewards...),
		LeaderSignature: signature,
	}, nextState, nil
}

// VerifyProposal 验证提案 + 复算执行结果后返回提案落地后的状态。
func (verifier ProposalVerifier) VerifyProposal(
	contextValue context.Context,
	request VerifyProposalRequest,
) (ChainState, error) {
	if err := request.validate(); err != nil {
		return ChainState{}, err
	}
	if verifier.Executor == nil {
		return ChainState{}, fmt.Errorf("consensus: nil transaction executor")
	}
	if request.Proposal.Header.ChainID != verifier.ChainID {
		return ChainState{}, fmt.Errorf("consensus: invalid chain id")
	}
	if err := request.Proposal.VerifyLeaderSignature(request.Leader.ConsensusPublicKey); err != nil {
		return ChainState{}, err
	}
	leaderID, err := request.Schedule.LeaderForSlot(request.Proposal.Header.Slot)
	if err != nil {
		return ChainState{}, err
	}
	if leaderID != request.Proposal.Header.LeaderID || leaderID != request.Leader.ValidatorID {
		return ChainState{}, fmt.Errorf("consensus: proposal leader mismatch")
	}

	nextState := request.ParentState.clone()
	receipts := make([]structure.Hash, 0, len(request.Proposal.Transactions))
	feeDetails := make([]structure.FeeDetails, 0, len(request.Proposal.Transactions))
	processedTransactionIDs := make(map[string]struct{}, len(request.Proposal.Transactions))
	for transactionIndex, transaction := range request.Proposal.Transactions {
		result, err := verifier.executeVerifyTransaction(contextValue, request, transaction, nextState, processedTransactionIDs)
		if err != nil {
			return ChainState{}, fmt.Errorf("consensus: verify transaction %d: %w", transactionIndex, err)
		}
		if result.Execution.Status != structure.TransactionStatusConfirmed {
			return ChainState{}, fmt.Errorf("consensus: proposal contains failed transaction %d", transactionIndex)
		}
		transactionID, err := transaction.TxIDString()
		if err != nil {
			return ChainState{}, fmt.Errorf("consensus: transaction id %d: %w", transactionIndex, err)
		}
		processedTransactionIDs[transactionID] = struct{}{}
		nextState = nextState.applyWrites(result.Execution.WrittenAccounts)
		feeDetails = append(feeDetails, result.Execution.FeeDetails)
		receiptHash, err := hashReceipt(result.Execution)
		if err != nil {
			return ChainState{}, fmt.Errorf("consensus: hash receipt %d: %w", transactionIndex, err)
		}
		receipts = append(receipts, receiptHash)
	}
	nextState, rewards, err := ApplyBlockRewards(nextState, BlockRewardInput{
		Slot:          request.Proposal.Header.Slot,
		ParentSlot:    request.ParentSlot,
		Height:        request.Proposal.Header.Height,
		EpochID:       request.EpochSnapshot.EpochID,
		EpochSnapshot: request.EpochSnapshot,
		Schedule:      request.Schedule,
		Leader:        request.Leader,
		FeeDetails:    feeDetails,
		RewardQCs:     request.Proposal.RewardQCs,
		Evidence:      request.Proposal.Evidence,
		Config:        request.RewardConfig,
	})
	if err != nil {
		return ChainState{}, err
	}
	if err := verifyProposalRoots(request.Proposal, receipts, rewards, nextState); err != nil {
		return ChainState{}, err
	}
	return nextState, nil
}

// Hash 计算提案哈希 + 使用区块头签名字节作为唯一输入。
func (proposal BlockProposal) Hash() (structure.Hash, error) {
	signBytes, err := proposal.Header.SignBytes()
	if err != nil {
		return structure.Hash{}, err
	}
	return structure.NewHash(utils.SHA256(signBytes))
}

// VerifyLeaderSignature 校验 leader 签名 + 防止非 leader 伪造区块。
func (proposal BlockProposal) VerifyLeaderSignature(publicKey structure.PublicKey) error {
	signBytes, err := proposal.Header.SignBytes()
	if err != nil {
		return err
	}
	if !structure.VerifyMessageSignature(publicKey, signBytes, proposal.LeaderSignature) {
		return fmt.Errorf("consensus: invalid leader signature")
	}
	return nil
}

// SignBytes 构造区块头签名数据 + 固定字段顺序保证跨节点一致。
func (header BlockHeader) SignBytes() ([]byte, error) {
	if stringsTrimmedEmpty(header.ChainID) {
		return nil, fmt.Errorf("consensus: chain id is empty")
	}
	encoded := make([]byte, 0, 256)
	encoded = append(encoded, []byte(header.ChainID)...)
	encoded = appendUint64ForHash(encoded, header.Slot)
	encoded = appendUint64ForHash(encoded, header.Height)
	encoded = append(encoded, header.ParentHash[:]...)
	encoded = append(encoded, header.PreviousQCHash[:]...)
	encoded = append(encoded, []byte(header.LeaderID)...)
	encoded = appendUint64ForHash(encoded, header.EpochID)
	encoded = append(encoded, header.TxRoot[:]...)
	encoded = append(encoded, header.ReceiptRoot[:]...)
	encoded = append(encoded, header.RewardRoot[:]...)
	encoded = append(encoded, header.StateRoot[:]...)
	encoded = append(encoded, header.AccountRoot[:]...)
	encoded = appendUint64ForHash(encoded, uint64(header.TimestampUnixMilli))
	return encoded, nil
}

type ProduceBlockRequest struct {
	Slot           uint64
	ParentSlot     uint64
	Height         uint64
	EpochSnapshot  EpochSnapshot
	Schedule       LeaderSchedule
	ParentHash     structure.Hash
	PreviousQCHash structure.Hash
	ParentState    ChainState
	Transactions   []structure.Transaction
	BlockhashQueue structure.BlockhashQueue
	LeaderKeyPair  structure.SolanaKeyPair
	RewardQCs      []QuorumCertificate
	Evidence       []SlashingEvidence
	RewardConfig   RewardConfig
}

type VerifyProposalRequest struct {
	Proposal       BlockProposal
	ParentSlot     uint64
	EpochSnapshot  EpochSnapshot
	Schedule       LeaderSchedule
	ParentHash     structure.Hash
	ParentState    ChainState
	BlockhashQueue structure.BlockhashQueue
	Leader         ValidatorState
	RewardConfig   RewardConfig
}

func (request ProduceBlockRequest) validate() error {
	if request.LeaderKeyPair.PublicKey.IsZero() {
		return fmt.Errorf("consensus: leader key is empty")
	}
	if request.ParentSlot != 0 && request.ParentSlot >= request.Slot {
		return fmt.Errorf("consensus: parent slot must be lower than proposal slot")
	}
	leaderID, err := request.Schedule.LeaderForSlot(request.Slot)
	if err != nil {
		return err
	}
	leader, exists := request.EpochSnapshot.ValidatorByID(leaderID)
	if !exists || leader.ConsensusPublicKey != request.LeaderKeyPair.PublicKey {
		return fmt.Errorf("consensus: local key is not slot leader")
	}
	return nil
}

func (request VerifyProposalRequest) validate() error {
	if request.Proposal.Header.ParentHash != request.ParentHash {
		return fmt.Errorf("consensus: parent hash mismatch")
	}
	if request.ParentSlot != 0 && request.ParentSlot >= request.Proposal.Header.Slot {
		return fmt.Errorf("consensus: parent slot must be lower than proposal slot")
	}
	if request.Proposal.Header.EpochID != request.EpochSnapshot.EpochID {
		return fmt.Errorf("consensus: epoch mismatch")
	}
	if len(request.Proposal.Transactions) > MaxProposalTransactions {
		return fmt.Errorf("consensus: proposal transaction count exceeds limit")
	}
	return nil
}

func (producer BlockProducer) executeTransaction(
	contextValue context.Context,
	request ProduceBlockRequest,
	transaction structure.Transaction,
	state ChainState,
	processedTransactionIDs map[string]struct{},
) (runtime.TransactionResult, error) {
	return producer.Executor.ExecuteTransaction(contextValue, runtime.TransactionRequest{
		ChainID: producer.ChainID,
		Slot:    request.Slot,
		Epoch:   request.EpochSnapshot.EpochID,
		Mode:    runtime.ExecutionModeFixedInstruction,
		Simulation: runtime.TransactionSimulationInput{
			Transaction:    transaction,
			Accounts:       state.Accounts,
			BlockhashQueue: request.BlockhashQueue,
			CurrentSlot:    request.Slot,
			CurrentEpoch:   request.EpochSnapshot.EpochID,
			ProcessedTxIDs: processedTransactionIDs,
		},
	})
}

func (verifier ProposalVerifier) executeVerifyTransaction(
	contextValue context.Context,
	request VerifyProposalRequest,
	transaction structure.Transaction,
	state ChainState,
	processedTransactionIDs map[string]struct{},
) (runtime.TransactionResult, error) {
	return verifier.Executor.ExecuteTransaction(contextValue, runtime.TransactionRequest{
		ChainID: verifier.ChainID,
		Slot:    request.Proposal.Header.Slot,
		Epoch:   request.EpochSnapshot.EpochID,
		Mode:    runtime.ExecutionModeFixedInstruction,
		Simulation: runtime.TransactionSimulationInput{
			Transaction:    transaction,
			Accounts:       state.Accounts,
			BlockhashQueue: request.BlockhashQueue,
			CurrentSlot:    request.Proposal.Header.Slot,
			CurrentEpoch:   request.EpochSnapshot.EpochID,
			ProcessedTxIDs: processedTransactionIDs,
		},
	})
}

func buildProposalHeader(
	chainID string,
	request ProduceBlockRequest,
	transactions []structure.Transaction,
	receipts []structure.Hash,
	rewards []BlockReward,
	state ChainState,
) (BlockHeader, error) {
	leaderID, err := request.Schedule.LeaderForSlot(request.Slot)
	if err != nil {
		return BlockHeader{}, err
	}
	txRoot, err := hashTransactions(transactions)
	if err != nil {
		return BlockHeader{}, err
	}
	receiptRoot, err := hashHashList(receipts)
	if err != nil {
		return BlockHeader{}, err
	}
	rewardRoot, err := HashBlockRewards(rewards)
	if err != nil {
		return BlockHeader{}, err
	}
	stateRoot, err := state.RootHash()
	if err != nil {
		return BlockHeader{}, err
	}
	return BlockHeader{
		ChainID:            chainID,
		Slot:               request.Slot,
		Height:             request.Height,
		ParentHash:         request.ParentHash,
		PreviousQCHash:     request.PreviousQCHash,
		LeaderID:           leaderID,
		EpochID:            request.EpochSnapshot.EpochID,
		TxRoot:             txRoot,
		ReceiptRoot:        receiptRoot,
		RewardRoot:         rewardRoot,
		StateRoot:          stateRoot,
		AccountRoot:        stateRoot,
		TimestampUnixMilli: time.Now().UnixMilli(),
	}, nil
}

func verifyProposalRoots(proposal BlockProposal, receipts []structure.Hash, rewards []BlockReward, state ChainState) error {
	txRoot, err := hashTransactions(proposal.Transactions)
	if err != nil {
		return err
	}
	receiptRoot, err := hashHashList(receipts)
	if err != nil {
		return err
	}
	rewardRoot, err := HashBlockRewards(rewards)
	if err != nil {
		return err
	}
	stateRoot, err := state.RootHash()
	if err != nil {
		return err
	}
	if proposal.Header.TxRoot != txRoot || proposal.Header.ReceiptRoot != receiptRoot {
		return fmt.Errorf("consensus: proposal transaction roots mismatch")
	}
	if proposal.Header.RewardRoot != rewardRoot || !EqualBlockRewards(proposal.Rewards, rewards) {
		return fmt.Errorf("consensus: proposal reward root mismatch")
	}
	if proposal.Header.StateRoot != stateRoot || proposal.Header.AccountRoot != stateRoot {
		return fmt.Errorf("consensus: proposal state root mismatch")
	}
	return nil
}

func (state ChainState) clone() ChainState {
	accounts := make([]structure.AddressedAccount, len(state.Accounts))
	for index, account := range state.Accounts {
		accounts[index] = structure.AddressedAccount{Address: account.Address, Account: account.Account.Clone()}
	}
	return ChainState{Accounts: accounts}
}

func (state ChainState) applyWrites(writes []structure.AddressedAccount) ChainState {
	nextState := state.clone()
	accountIndexByAddress := make(map[structure.PublicKey]int, len(nextState.Accounts))
	for index, account := range nextState.Accounts {
		accountIndexByAddress[account.Address] = index
	}
	for _, write := range writes {
		index, exists := accountIndexByAddress[write.Address]
		if !exists && isBuiltinProgramPlaceholder(write) {
			continue
		}
		if exists {
			nextState.Accounts[index] = structure.AddressedAccount{Address: write.Address, Account: write.Account.Clone()}
			continue
		}
		accountIndexByAddress[write.Address] = len(nextState.Accounts)
		nextState.Accounts = append(nextState.Accounts, structure.AddressedAccount{Address: write.Address, Account: write.Account.Clone()})
	}
	return nextState
}

func isBuiltinProgramPlaceholder(account structure.AddressedAccount) bool {
	return structure.DefaultBuiltinProgramIDs.IsBuiltinProgram(account.Address) &&
		account.Account.Lamports == 0 &&
		len(account.Account.Data) == 0 &&
		!account.Account.Executable &&
		account.Account.Owner == structure.DefaultBuiltinProgramIDs.NativeLoader
}

// RootHash 计算账户状态根 + 地址排序保证跨节点可复算。
func (state ChainState) RootHash() (structure.Hash, error) {
	accounts := state.clone().Accounts
	sort.Slice(accounts, func(leftIndex int, rightIndex int) bool {
		return bytes.Compare(accounts[leftIndex].Address[:], accounts[rightIndex].Address[:]) < 0
	})

	encoded := make([]byte, 0, len(accounts)*80)
	for _, account := range accounts {
		accountBytes, err := account.Account.MarshalBinary()
		if err != nil {
			return structure.Hash{}, fmt.Errorf("consensus: marshal account for state root: %w", err)
		}
		encoded = append(encoded, account.Address[:]...)
		encoded = append(encoded, utils.SHA256(accountBytes)...)
	}
	return structure.NewHash(utils.SHA256(encoded))
}

func hashTransactions(transactions []structure.Transaction) (structure.Hash, error) {
	hashes := make([]structure.Hash, len(transactions))
	for index, transaction := range transactions {
		hash, err := transaction.Hash()
		if err != nil {
			return structure.Hash{}, fmt.Errorf("consensus: hash transaction %d: %w", index, err)
		}
		hashes[index] = hash
	}
	return hashHashList(hashes)
}

func hashHashList(hashes []structure.Hash) (structure.Hash, error) {
	if len(hashes) == 0 {
		return structure.NewHash(make([]byte, structure.HashSize))
	}
	encoded := make([]byte, 0, len(hashes)*structure.HashSize)
	for _, hash := range hashes {
		encoded = append(encoded, hash[:]...)
	}
	return structure.NewHash(utils.SHA256(encoded))
}

func hashReceipt(result structure.TransactionExecutionResult) (structure.Hash, error) {
	encoded := make([]byte, 0, 64)
	encoded = appendUint64ForHash(encoded, uint64(result.Status))
	encoded = appendUint64ForHash(encoded, result.FeeDetails.TotalFee)
	for _, balance := range result.PostBalances {
		encoded = appendUint64ForHash(encoded, balance)
	}
	return structure.NewHash(utils.SHA256(encoded))
}

func markTransactionConfirmed(transaction structure.Transaction, feeLamports uint64) structure.Transaction {
	nextTransaction := transaction.Clone()
	nextTransaction.Status = structure.TransactionStatusConfirmed
	nextTransaction.Fee = feeLamports
	return nextTransaction
}

func stringsTrimmedEmpty(value string) bool {
	for _, char := range value {
		if char != ' ' && char != '\t' && char != '\n' && char != '\r' {
			return false
		}
	}
	return true
}
