package posnode

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tentechtop/ordexus-core/blockchain"
	"github.com/tentechtop/ordexus-core/consensus"
	"github.com/tentechtop/ordexus-core/p2p"
	"github.com/tentechtop/ordexus-core/programs/stake"
	vmprogram "github.com/tentechtop/ordexus-core/programs/vm"
	"github.com/tentechtop/ordexus-core/rpc"
	runtimepkg "github.com/tentechtop/ordexus-core/runtime"
	"github.com/tentechtop/ordexus-core/structure"
	"github.com/tentechtop/ordexus-core/utils"
)

const (
	rpcTransactionBroadcastTimeout    = 2 * time.Second
	minHealthHeadFreshnessWindow      = 5 * time.Second
	maxHealthHeadFreshnessWindow      = 30 * time.Second
	healthHeadFreshnessSlotMultiplier = 5
)

type blockLeaderMetadata struct {
	address        string
	source         string
	commissionBps  *uint16
	rewardLamports *uint64
	stakeLamports  *uint64
	voteCredits    *uint64
}

func (node *posNode) GetBalance(ctx context.Context, address string) (rpc.BalanceResult, error) {
	_ = ctx
	publicKey, err := decodePublicKey(address, "balance address")
	if err != nil {
		return rpc.BalanceResult{}, fmt.Errorf("posnode: decode balance address: %w", err)
	}
	account, found, err := node.ledger.Account(publicKey)
	if err != nil {
		return rpc.BalanceResult{}, err
	}
	if !found {
		return rpc.BalanceResult{Value: 0}, nil
	}
	return rpc.BalanceResult{Value: account.Lamports}, nil
}

func (node *posNode) GetLatestBlockhash(ctx context.Context) (rpc.LatestBlockhashResult, error) {
	_ = ctx
	head := node.ledger.Head()
	if err := node.ensureHeadBlockhashAvailable(head); err != nil {
		return rpc.LatestBlockhashResult{}, err
	}
	if ^uint64(0)-head.Slot < structure.MaxRecentBlockhashAgeSlots {
		return rpc.LatestBlockhashResult{}, fmt.Errorf("posnode: latest blockhash slot overflows last valid slot")
	}
	if ^uint64(0)-head.Height < structure.MaxRecentBlockhashAgeSlots {
		return rpc.LatestBlockhashResult{}, fmt.Errorf("posnode: latest blockhash height overflows last valid block height")
	}
	return rpc.LatestBlockhashResult{
		Blockhash:            head.BlockHash.String(),
		Slot:                 head.Slot,
		Height:               head.Height,
		LastValidSlot:        head.Slot + structure.MaxRecentBlockhashAgeSlots,
		LastValidBlockHeight: head.Height + structure.MaxRecentBlockhashAgeSlots,
	}, nil
}

func (node *posNode) GetAccountType(ctx context.Context, address string) (rpc.AccountTypeResult, error) {
	_ = ctx
	publicKey, err := decodePublicKey(address, "account type address")
	if err != nil {
		return rpc.AccountTypeResult{}, fmt.Errorf("posnode: decode account type address: %w", err)
	}
	account, found, err := node.ledger.Account(publicKey)
	if err != nil {
		return rpc.AccountTypeResult{}, err
	}
	if !found {
		return rpc.AccountTypeResult{Address: publicKey.String(), Exists: false, Type: "unknown"}, nil
	}
	return rpc.AccountTypeResult{
		Address: publicKey.String(),
		Exists:  true,
		Owner:   account.Owner.String(),
		Type:    accountTypeName(account.Owner),
	}, nil
}

// GetShieldedPool 返回全局屏蔽池公开状态 + 节点永不接收钱包 seed、私钥或查看密钥。
func (node *posNode) GetShieldedPool(ctx context.Context) (rpc.ShieldedPoolResult, error) {
	_ = ctx
	poolAddress := structure.DefaultShieldedPoolAddress
	account, found, err := node.ledger.Account(poolAddress)
	if err != nil {
		return rpc.ShieldedPoolResult{}, fmt.Errorf("posnode: load shielded pool: %w", err)
	}
	if !found {
		return rpc.ShieldedPoolResult{}, fmt.Errorf("posnode: shielded pool account is missing")
	}
	if account.Owner != structure.DefaultBuiltinProgramIDs.Shielded || account.Executable {
		return rpc.ShieldedPoolResult{}, fmt.Errorf("posnode: shielded pool account metadata is invalid")
	}
	poolState, err := structure.UnmarshalShieldedPoolStateBinary(account.Data)
	if err != nil {
		return rpc.ShieldedPoolResult{}, fmt.Errorf("posnode: decode shielded pool: %w", err)
	}
	canonicalStateData, err := poolState.MarshalBinary()
	if err != nil {
		return rpc.ShieldedPoolResult{}, fmt.Errorf("posnode: encode shielded pool: %w", err)
	}
	if !bytes.Equal(canonicalStateData, account.Data) {
		return rpc.ShieldedPoolResult{}, fmt.Errorf("posnode: shielded pool state encoding is not canonical")
	}

	recentAnchors := make([]string, len(poolState.RecentAnchors))
	for index, anchor := range poolState.RecentAnchors {
		recentAnchors[index] = anchor.String()
	}
	return rpc.ShieldedPoolResult{
		Address:              poolAddress.String(),
		StateEncodingVersion: poolState.StateEncodingVersion,
		ProtocolVersion:      poolState.ProtocolVersion,
		CircuitID:            poolState.CircuitID,
		VerifyingKeyHash:     poolState.VerifyingKeyHash.String(),
		TreeDepth:            poolState.TreeDepth,
		LeafCount:            poolState.LeafCount,
		CurrentRoot:          poolState.CurrentRoot.String(),
		RecentAnchors:        recentAnchors,
		PoolLamports:         poolState.PoolLamports,
		PhysicalLamports:     account.Lamports,
		DataBase64:           base64.StdEncoding.EncodeToString(canonicalStateData),
	}, nil
}

// GetAccountInfo 返回公开账户的开发视图 + 隐私程序账户仅返回哈希和长度。
func (node *posNode) GetAccountInfo(ctx context.Context, address string) (rpc.AccountInfoResult, error) {
	_ = ctx
	publicKey, err := decodePublicKey(address, "account info address")
	if err != nil {
		return rpc.AccountInfoResult{}, fmt.Errorf("posnode: decode account info address: %w", err)
	}
	account, found, err := node.ledger.Account(publicKey)
	if err != nil {
		return rpc.AccountInfoResult{}, err
	}
	if !found {
		return rpc.AccountInfoResult{Address: publicKey.String(), Exists: false}, nil
	}
	return accountInfoResult(publicKey, account), nil
}

func (node *posNode) SendTransaction(ctx context.Context, encodedTransaction string) (string, error) {
	transactionBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedTransaction))
	if err != nil {
		return "", fmt.Errorf("posnode: decode transaction: %w", err)
	}
	transaction, err := structure.UnmarshalTransactionBinary(transactionBytes)
	if err != nil {
		return "", fmt.Errorf("posnode: unmarshal transaction: %w", err)
	}
	return node.submitTransaction(ctx, transaction, "send_transaction")
}

// SimulateTransaction 在当前已确认状态上执行签名交易 + 返回日志、返回值和写集但不提交账本。
func (node *posNode) SimulateTransaction(ctx context.Context, encodedTransaction string) (rpc.TransactionSimulationResult, error) {
	transactionBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedTransaction))
	if err != nil {
		return rpc.TransactionSimulationResult{}, fmt.Errorf("posnode: decode simulated transaction: %w", err)
	}
	transaction, err := structure.UnmarshalTransactionBinary(transactionBytes)
	if err != nil {
		return rpc.TransactionSimulationResult{}, fmt.Errorf("posnode: unmarshal simulated transaction: %w", err)
	}
	head := node.ledger.Head()
	state := node.ledger.State()
	node.mutex.Lock()
	blockhashQueue := node.blockhashQueue.Clone()
	node.mutex.Unlock()
	result, err := node.executePreflightTransaction(ctx, transaction, head, state, blockhashQueue, map[string]struct{}{})
	if err != nil {
		return rpc.TransactionSimulationResult{}, err
	}
	return transactionSimulationResult(result.Execution), nil
}

func (node *posNode) GetBlock(ctx context.Context, slot uint64) (rpc.BlockResult, error) {
	_ = ctx
	proposal, blockHash, found, err := node.blockBySlot(slot)
	if err != nil {
		return rpc.BlockResult{}, err
	}
	if !found {
		proposal, blockHash, found, err = node.ledger.BlockByHeight(slot)
		if err != nil {
			return rpc.BlockResult{}, err
		}
	}
	if !found {
		return rpc.BlockResult{Slot: slot}, nil
	}
	return node.blockResultFromProposal(proposal, blockHash)
}

func (node *posNode) blockBySlot(slot uint64) (consensus.BlockProposal, structure.Hash, bool, error) {
	// 功能目的：按真实 slot 查主链区块；实现原因：账本 height 索引可能存在空洞，二分会误判为未找到。
	if slot == 0 {
		return consensus.BlockProposal{}, structure.Hash{}, false, nil
	}
	head := node.ledger.Head()
	if slot > head.Slot {
		return consensus.BlockProposal{}, structure.Hash{}, false, nil
	}
	for height := head.Height; height >= 1; height-- {
		proposal, blockHash, found, err := node.ledger.BlockByHeight(height)
		if err != nil {
			return consensus.BlockProposal{}, structure.Hash{}, false, err
		}
		if !found {
			if height == 1 {
				break
			}
			continue
		}
		if proposal.Header.Slot == slot {
			return proposal, blockHash, true, nil
		}
		if proposal.Header.Slot < slot {
			break
		}
		if height == 1 {
			break
		}
	}
	return consensus.BlockProposal{}, structure.Hash{}, false, nil
}

func (node *posNode) blockResultFromProposal(proposal consensus.BlockProposal, blockHash structure.Hash) (rpc.BlockResult, error) {
	transactions := make([]any, 0, len(proposal.Transactions))
	for _, transaction := range proposal.Transactions {
		transactionID, err := transaction.TxIDString()
		if err != nil {
			return rpc.BlockResult{}, err
		}
		transactions = append(transactions, transactionID)
	}
	leader := node.blockLeaderMetadata(proposal, blockHash)
	result := rpc.BlockResult{
		Slot:                 proposal.Header.Slot,
		Height:               proposal.Header.Height,
		EpochID:              proposal.Header.EpochID,
		Blockhash:            blockHash.String(),
		ParentSlot:           node.blockParentSlotForProposal(proposal),
		BlockTimeUnixMilli:   proposal.Header.TimestampUnixMilli,
		StateRoot:            proposal.Header.StateRoot.String(),
		EpochSnapshotRoot:    proposal.Header.EpochSnapshotRoot.String(),
		TxRoot:               proposal.Header.TxRoot.String(),
		ReceiptRoot:          proposal.Header.ReceiptRoot.String(),
		RewardQCRoot:         proposal.Header.RewardQCRoot.String(),
		EvidenceRoot:         proposal.Header.EvidenceRoot.String(),
		RewardRoot:           proposal.Header.RewardRoot.String(),
		RewardQCCount:        len(proposal.RewardQCs),
		EvidenceCount:        len(proposal.Evidence),
		RewardCount:          len(proposal.Rewards),
		LeaderAddress:        leader.address,
		LeaderAddressSource:  leader.source,
		LeaderCommissionBps:  leader.commissionBps,
		LeaderStakeLamports:  leader.stakeLamports,
		LeaderVoteCredits:    leader.voteCredits,
		LeaderRewardLamports: leader.rewardLamports,
		Transactions:         transactions,
	}
	return result, nil
}

func (node *posNode) blockParentSlotForProposal(proposal consensus.BlockProposal) uint64 {
	if proposal.Header.Height == 0 || proposal.Header.ParentHash.IsZero() {
		return 0
	}
	parentSlot, err := node.parentSlotForProposal(proposal.Header.ParentHash)
	if err == nil {
		return parentSlot
	}
	if proposal.Header.Slot == 0 {
		return 0
	}
	return proposal.Header.Slot - 1
}

func (node *posNode) blockLeaderMetadata(proposal consensus.BlockProposal, blockHash structure.Hash) blockLeaderMetadata {
	metadata := proposalRewardLeaderMetadata(proposal)
	leader, found := node.leaderStateForProposal(proposal, blockHash)
	if !found {
		return metadata
	}
	commissionBps := leader.CommissionBps
	metadata.address = leader.AccountAddress.String()
	metadata.source = "block"
	metadata.commissionBps = &commissionBps
	if leader.StakeLamports > 0 {
		stakeLamports := leader.StakeLamports
		metadata.stakeLamports = &stakeLamports
	}
	if voteCredits, exists := node.validatorVoteCredits(leader.AccountAddress); exists {
		metadata.voteCredits = &voteCredits
	}
	return metadata
}

func (node *posNode) leaderStateForProposal(proposal consensus.BlockProposal, blockHash structure.Hash) (consensus.ValidatorState, bool) {
	if proposal.Header.LeaderID == "" {
		return consensus.ValidatorState{}, false
	}
	if leader, found := node.leaderStateFromEpochContext(proposal); found {
		return leader, true
	}
	if leader, found := node.leaderStateFromBlockState(blockHash, proposal.Header.LeaderID, proposal.Header.EpochID); found {
		return leader, true
	}
	if leader, found := node.leaderStateFromBlockState(proposal.Header.ParentHash, proposal.Header.LeaderID, proposal.Header.EpochID); found {
		return leader, true
	}
	return node.leaderStateFromCurrentState(proposal.Header.LeaderID, proposal.Header.EpochID)
}

func (node *posNode) leaderStateFromEpochContext(proposal consensus.BlockProposal) (consensus.ValidatorState, bool) {
	if node.config.EpochSlots == 0 {
		return consensus.ValidatorState{}, false
	}
	node.mutex.Lock()
	defer node.mutex.Unlock()
	epochContextValue, err := node.epochContextForSlotLocked(proposal.Header.Slot)
	if err != nil {
		if node.logger != nil {
			node.logger.Info("posnode block leader context unavailable",
				slog.Uint64("slot", proposal.Header.Slot),
				slog.Uint64("height", proposal.Header.Height),
				slog.String("error", err.Error()),
			)
		}
		return consensus.ValidatorState{}, false
	}
	leader, exists := epochContextValue.Snapshot.ValidatorByID(proposal.Header.LeaderID)
	if !exists {
		return consensus.ValidatorState{}, false
	}
	return leader, true
}

func (node *posNode) leaderStateFromBlockState(blockHash structure.Hash, leaderID consensus.ValidatorID, epochID uint64) (consensus.ValidatorState, bool) {
	if blockHash.IsZero() || node.ledger == nil {
		return consensus.ValidatorState{}, false
	}
	state, err := node.ledger.StateAtBlockHash(blockHash)
	if err != nil {
		if node.logger != nil {
			node.logger.Debug("posnode block leader state unavailable",
				slog.String("block_hash", blockHash.String()),
				slog.String("leader_id", string(leaderID)),
				slog.String("error", err.Error()),
			)
		}
		return consensus.ValidatorState{}, false
	}
	return validatorStateByIDFromChainState(state, leaderID, epochID)
}

func (node *posNode) leaderStateFromCurrentState(leaderID consensus.ValidatorID, epochID uint64) (consensus.ValidatorState, bool) {
	if node.ledger == nil {
		return consensus.ValidatorState{}, false
	}
	return validatorStateByIDFromChainState(node.ledger.State(), leaderID, epochID)
}

func validatorStateByIDFromChainState(state consensus.ChainState, leaderID consensus.ValidatorID, epochID uint64) (consensus.ValidatorState, bool) {
	for _, account := range state.Accounts {
		if account.Account.Owner != structure.DefaultBuiltinProgramIDs.Stake || len(account.Account.Data) == 0 {
			continue
		}
		stakeState, err := stake.UnmarshalValidatorStateBinary(account.Account.Data)
		if err != nil {
			continue
		}
		if consensus.NewValidatorID(stakeState.ConsensusPublicKey) != leaderID {
			continue
		}
		return validatorStateFromStakeAccount(account.Address, stakeState, leaderID, epochID), true
	}
	return consensus.ValidatorState{}, false
}

func validatorStateFromStakeAccount(
	accountAddress structure.PublicKey,
	stakeState stake.ValidatorState,
	validatorID consensus.ValidatorID,
	epochID uint64,
) consensus.ValidatorState {
	effectiveStake, err := stake.EffectiveStakeAtEpoch(stakeState, epochID)
	if err != nil {
		effectiveStake = 0
	}
	status := validatorStatusFromStakeState(stakeState, epochID)
	if status != consensus.ValidatorStatusActive {
		effectiveStake = 0
	}
	return consensus.ValidatorState{
		ValidatorID:         validatorID,
		AccountAddress:      accountAddress,
		ConsensusPublicKey:  stakeState.ConsensusPublicKey,
		BLSPublicKey:        append([]byte(nil), stakeState.BLSPublicKey...),
		P2PPeerID:           stakeState.P2PPeerID,
		StakeLamports:       effectiveStake,
		Status:              status,
		CommissionBps:       stakeState.CommissionBps,
		LastVotedSlot:       stakeState.LastVoteSlot,
		MissedProposalCount: stakeState.MissedProposalCount,
		MissedVoteCount:     stakeState.MissedVoteCount,
		JailUntilEpoch:      stakeState.JailUntilEpoch,
	}
}

func validatorStatusFromStakeState(stakeState stake.ValidatorState, epochID uint64) consensus.ValidatorStatus {
	if stakeState.Status == stake.ValidatorStatusJailed {
		return consensus.ValidatorStatusJailed
	}
	if stakeState.Status == stake.ValidatorStatusExiting && stakeState.DeactivationEpoch <= epochID {
		return consensus.ValidatorStatusExiting
	}
	return consensus.ValidatorStatusActive
}

func (node *posNode) validatorVoteCredits(address structure.PublicKey) (uint64, bool) {
	account, found, err := node.ledger.Account(address)
	if err != nil || !found {
		return 0, false
	}
	stakeState, err := stake.UnmarshalValidatorStateBinary(account.Data)
	if err != nil {
		return 0, false
	}
	return stakeState.VoteCredits, true
}

func proposalRewardLeaderMetadata(proposal consensus.BlockProposal) blockLeaderMetadata {
	metadata := blockLeaderMetadata{}
	for _, reward := range proposal.Rewards {
		if reward.Type != consensus.RewardTypeLeaderFee || reward.AccountAddress.IsZero() {
			continue
		}
		rewardLamports := reward.Lamports
		metadata.address = reward.AccountAddress.String()
		metadata.source = "block"
		metadata.rewardLamports = &rewardLamports
		return metadata
	}
	return metadata
}

func (node *posNode) GetTransaction(ctx context.Context, signature string) (rpc.TransactionDetailResult, error) {
	_ = ctx
	signature = strings.TrimSpace(signature)
	if signature == "" {
		return rpc.TransactionDetailResult{}, fmt.Errorf("posnode: transaction signature is empty")
	}

	mempoolResult, found, err := node.lookupMempoolTransaction(signature)
	if err != nil {
		return rpc.TransactionDetailResult{}, err
	}
	if found {
		return mempoolResult, nil
	}

	proposal, blockHash, found, err := node.ledger.TransactionByID(signature)
	if err != nil {
		return rpc.TransactionDetailResult{}, fmt.Errorf("posnode: lookup committed transaction: %w", err)
	}
	if !found {
		rejectedResult, rejected := node.lookupRejectedTransaction(signature)
		if rejected {
			return rejectedResult, nil
		}
		return rpc.TransactionDetailResult{
			Signature: signature,
			Found:     false,
			Location:  "unknown",
			Status:    "not_found",
		}, nil
	}

	for _, transaction := range proposal.Transactions {
		transactionID, err := transaction.TxIDString()
		if err != nil {
			return rpc.TransactionDetailResult{}, fmt.Errorf("posnode: decode committed transaction id: %w", err)
		}
		if transactionID != signature {
			continue
		}
		return node.committedTransactionResult(signature, transaction, proposal, blockHash), nil
	}
	return rpc.TransactionDetailResult{}, fmt.Errorf("posnode: committed transaction index mismatch for %s", signature)
}

func (node *posNode) GetAddressTransactions(ctx context.Context, address string, cursor string, limit int) (rpc.AccountTransactionHistoryResult, error) {
	_ = ctx
	publicKey, err := decodePublicKey(address, "history address")
	if err != nil {
		return rpc.AccountTransactionHistoryResult{}, fmt.Errorf("posnode: decode history address: %w", err)
	}
	page, err := node.ledger.AddressHistory(publicKey, strings.TrimSpace(cursor), limit)
	if err != nil {
		return rpc.AccountTransactionHistoryResult{}, err
	}
	return accountTransactionHistoryResult(publicKey, page, node.ledger.Head().FinalizedHeight), nil
}

func (node *posNode) GetContractPrograms(ctx context.Context, limit int) (rpc.ContractProgramListResult, error) {
	_ = ctx
	records, err := node.ledger.ContractPrograms(limit)
	if err != nil {
		return rpc.ContractProgramListResult{}, fmt.Errorf("posnode: list contract programs: %w", err)
	}
	return contractProgramListResult(records), nil
}

func (node *posNode) GetAssetState(ctx context.Context, program string, owner string) (rpc.AssetStateResult, error) {
	_ = ctx
	programKey, err := decodePublicKey(program, "asset program")
	if err != nil {
		return rpc.AssetStateResult{}, fmt.Errorf("posnode: decode asset program: %w", err)
	}
	ownerKey, err := decodePublicKey(owner, "asset owner")
	if err != nil {
		return rpc.AssetStateResult{}, fmt.Errorf("posnode: decode asset owner: %w", err)
	}
	assetAccounts, err := blockchain.DeriveFungibleAssetBootstrapAccounts(programKey, ownerKey)
	if err != nil {
		return rpc.AssetStateResult{}, err
	}
	mintAccount, mintFound, err := node.ledger.Account(assetAccounts.Mint.PublicKey)
	if err != nil {
		return rpc.AssetStateResult{}, fmt.Errorf("posnode: load asset mint account: %w", err)
	}
	balanceAccount, balanceFound, err := node.ledger.Account(assetAccounts.Balance.PublicKey)
	if err != nil {
		return rpc.AssetStateResult{}, fmt.Errorf("posnode: load asset balance account: %w", err)
	}
	return rpc.AssetStateResult{
		Program: programKey.String(),
		Owner:   ownerKey.String(),
		Mint:    assetMintStateResult(assetAccounts.Mint.PublicKey, programKey, mintAccount, mintFound),
		Balance: assetBalanceStateResult(assetAccounts.Balance.PublicKey, programKey, balanceAccount, balanceFound),
	}, nil
}

func (node *posNode) TreasuryTransfer(ctx context.Context, destination string, lamports uint64) (string, error) {
	destinationKey, err := decodePublicKey(destination, "destination")
	if err != nil {
		return "", fmt.Errorf("posnode: decode destination: %w", err)
	}
	treasury, keySource, err := node.treasuryKeyPair()
	if err != nil {
		return "", err
	}
	transaction, err := blockchain.NewTreasuryTransferTransaction(treasury, destinationKey, lamports, node.ledger.Head().BlockHash)
	if err != nil {
		return "", err
	}
	return node.submitTransaction(ctx, transaction, "treasury_transfer",
		slog.String("destination", destinationKey.String()),
		slog.Uint64("lamports", lamports),
		slog.String("treasury_key_source", keySource),
	)
}

func (node *posNode) Transfer(ctx context.Context, sourceSeed string, destination string, lamports uint64) (string, error) {
	source, err := keyPairFromSeed(sourceSeed)
	if err != nil {
		return "", err
	}
	destinationKey, err := decodePublicKey(destination, "destination")
	if err != nil {
		return "", fmt.Errorf("posnode: decode destination: %w", err)
	}
	transaction, err := blockchain.NewTransferTransaction(source, destinationKey, lamports, node.ledger.Head().BlockHash)
	if err != nil {
		return "", err
	}
	return node.submitTransaction(ctx, transaction, "transfer",
		slog.String("source", source.PublicKey.String()),
		slog.String("destination", destinationKey.String()),
		slog.Uint64("lamports", lamports),
	)
}

func (node *posNode) GetLocalValidatorIdentity(ctx context.Context) (rpc.LocalValidatorIdentityResult, error) {
	_ = ctx
	head := node.ledger.Head()
	recommendedStakeLamports := node.config.StakeLamports
	if recommendedStakeLamports == 0 {
		recommendedStakeLamports = stake.MinimumStakeLamports
	}
	result := rpc.LocalValidatorIdentityResult{
		NodeName:                 node.config.NodeName,
		StakerAddress:            node.stakerAddress.String(),
		ValidatorAddress:         node.validatorKeyPair.PublicKey.String(),
		ConsensusPublicKey:       node.consensusKeyPair.PublicKey.String(),
		BLSPublicKey:             utils.Base58Encode(node.blsKeyPair.PublicKey),
		P2PPeerID:                node.peerKeyPair.peerID,
		RecommendedStakeLamports: recommendedStakeLamports,
		Status:                   "not_registered",
		CurrentEpoch:             head.EpochID,
	}
	account, found, err := node.ledger.Account(node.validatorKeyPair.PublicKey)
	if err != nil {
		return rpc.LocalValidatorIdentityResult{}, fmt.Errorf("posnode: read local validator account: %w", err)
	}
	if !found || account.Owner != structure.DefaultBuiltinProgramIDs.Stake || len(account.Data) == 0 {
		return result, nil
	}
	state, err := stake.UnmarshalValidatorStateBinary(account.Data)
	if err != nil {
		return rpc.LocalValidatorIdentityResult{}, fmt.Errorf("posnode: decode local validator stake state: %w", err)
	}
	effectiveStake, err := stake.EffectiveStakeAtEpoch(state, head.EpochID)
	if err != nil {
		return rpc.LocalValidatorIdentityResult{}, fmt.Errorf("posnode: calculate local validator effective stake: %w", err)
	}
	result.Registered = true
	result.StakerAddress = state.StakerAccount.String()
	result.Status = stakeStatusText(state.Status)
	result.ActiveStakeLamports = state.ActiveStake
	result.PendingStakeLamports = state.PendingStake
	result.UnlockingStakeLamports = state.UnlockingStake
	result.EffectiveStakeLamports = effectiveStake
	result.ActivationEpoch = state.ActivationEpoch
	result.DeactivationEpoch = state.DeactivationEpoch
	result.CommissionBps = state.CommissionBps
	result.VoteCredits = state.VoteCredits
	result.RewardLamports = state.RewardLamports
	result.SelfRewardLamports = state.SelfRewardLamports
	result.CommissionRewardLamports = state.CommissionRewardLamports
	result.LastRewardedSlot = state.LastRewardedSlot
	result.LastRewardEpoch = state.LastRewardEpoch
	result.LastSlashedSlot = state.LastSlashedSlot
	return result, nil
}

func (node *posNode) RegisterValidator(ctx context.Context, stakerSeed string, validatorSeed string, consensusSeed string, peerID string, stakeLamports uint64) (string, error) {
	staker, err := keyPairFromSeed(stakerSeed)
	if err != nil {
		return "", err
	}
	validatorAccount, err := keyPairFromSeed(validatorSeed)
	if err != nil {
		return "", err
	}
	consensusKey, err := keyPairFromSeed(consensusSeed)
	if err != nil {
		return "", err
	}
	blsKeyPair, err := consensus.BLSKeyPairFromSeed(utils.SHA256([]byte(strings.TrimSpace(consensusSeed))))
	if err != nil {
		return "", err
	}
	transaction, err := blockchain.NewRegisterValidatorTransactionWithBLS(staker, validatorAccount.PublicKey, consensusKey.PublicKey, blsKeyPair.PublicKey, strings.TrimSpace(peerID), stakeLamports, node.ledger.Head().BlockHash)
	if err != nil {
		return "", err
	}
	return node.submitTransaction(ctx, transaction, "register_validator",
		slog.String("staker", staker.PublicKey.String()),
		slog.String("validator_account", validatorAccount.PublicKey.String()),
		slog.String("consensus_public_key", consensusKey.PublicKey.String()),
		slog.String("peer_id", strings.TrimSpace(peerID)),
		slog.Uint64("stake_lamports", stakeLamports),
	)
}

func (node *posNode) RegisterValidatorIdentity(ctx context.Context, stakerSeed string, validatorAddress string, consensusPublicKey string, blsPublicKey string, peerID string, stakeLamports uint64) (string, error) {
	staker, err := keyPairFromSeed(stakerSeed)
	if err != nil {
		return "", err
	}
	validatorKey, err := decodePublicKey(validatorAddress, "validator address")
	if err != nil {
		return "", fmt.Errorf("posnode: decode validator address: %w", err)
	}
	consensusKey, err := decodePublicKey(consensusPublicKey, "consensus public key")
	if err != nil {
		return "", fmt.Errorf("posnode: decode consensus public key: %w", err)
	}
	blsKeyBytes, err := utils.Base58Decode(strings.TrimSpace(blsPublicKey))
	if err != nil {
		return "", fmt.Errorf("posnode: decode bls public key: %w", err)
	}
	if err := consensus.ValidateBLSPublicKey(blsKeyBytes); err != nil {
		return "", err
	}
	transaction, err := blockchain.NewRegisterValidatorTransactionWithBLS(staker, validatorKey, consensusKey, blsKeyBytes, strings.TrimSpace(peerID), stakeLamports, node.ledger.Head().BlockHash)
	if err != nil {
		return "", err
	}
	return node.submitTransaction(ctx, transaction, "register_validator_identity",
		slog.String("staker", staker.PublicKey.String()),
		slog.String("validator_account", validatorKey.String()),
		slog.String("consensus_public_key", consensusKey.String()),
		slog.String("peer_id", strings.TrimSpace(peerID)),
		slog.Int("bls_public_key_bytes", len(blsKeyBytes)),
		slog.Uint64("stake_lamports", stakeLamports),
	)
}

func (node *posNode) Stake(ctx context.Context, stakerSeed string, validatorAddress string, lamports uint64) (string, error) {
	staker, err := keyPairFromSeed(stakerSeed)
	if err != nil {
		return "", err
	}
	validatorKey, err := decodePublicKey(validatorAddress, "validator address")
	if err != nil {
		return "", fmt.Errorf("posnode: decode validator address: %w", err)
	}
	state, err := node.loadValidatorStakeState(validatorKey)
	if err != nil {
		return "", err
	}
	if state.StakerAccount != staker.PublicKey {
		return "", fmt.Errorf(
			"posnode: only the validator staker can add stake: current_wallet=%s required_staker=%s validator=%s; 当前实现是验证者自质押追加，普通用户委托质押尚未实现",
			staker.PublicKey.String(),
			state.StakerAccount.String(),
			validatorKey.String(),
		)
	}
	transaction, err := blockchain.NewStakeTransaction(staker, validatorKey, lamports, node.ledger.Head().BlockHash)
	if err != nil {
		return "", err
	}
	return node.submitTransaction(ctx, transaction, "stake",
		slog.String("staker", staker.PublicKey.String()),
		slog.String("validator_account", validatorKey.String()),
		slog.Uint64("lamports", lamports),
	)
}

func (node *posNode) Unstake(ctx context.Context, stakerSeed string, validatorAddress string, lamports uint64, unlockEpoch uint64) (string, error) {
	staker, err := keyPairFromSeed(stakerSeed)
	if err != nil {
		return "", err
	}
	validatorKey, err := decodePublicKey(validatorAddress, "validator address")
	if err != nil {
		return "", fmt.Errorf("posnode: decode validator address: %w", err)
	}
	state, err := node.loadValidatorStakeState(validatorKey)
	if err != nil {
		return "", err
	}
	if state.StakerAccount != staker.PublicKey {
		return "", fmt.Errorf(
			"posnode: only the validator staker can unstake: current_wallet=%s required_staker=%s validator=%s",
			staker.PublicKey.String(),
			state.StakerAccount.String(),
			validatorKey.String(),
		)
	}
	transaction, err := blockchain.NewUnstakeTransaction(staker, validatorKey, lamports, unlockEpoch, node.ledger.Head().BlockHash)
	if err != nil {
		return "", err
	}
	return node.submitTransaction(ctx, transaction, "unstake",
		slog.String("staker", staker.PublicKey.String()),
		slog.String("validator_account", validatorKey.String()),
		slog.Uint64("lamports", lamports),
		slog.Uint64("unlock_epoch", unlockEpoch),
	)
}

func (node *posNode) SlashValidator(ctx context.Context, stakerSeed string, validatorAddress string, lamports uint64) (string, error) {
	staker, err := keyPairFromSeed(stakerSeed)
	if err != nil {
		return "", err
	}
	validatorKey, err := decodePublicKey(validatorAddress, "validator address")
	if err != nil {
		return "", fmt.Errorf("posnode: decode validator address: %w", err)
	}
	transaction, err := blockchain.NewSlashValidatorTransaction(staker, validatorKey, lamports, node.ledger.Head().BlockHash)
	if err != nil {
		return "", err
	}
	return node.submitTransaction(ctx, transaction, "slash_validator",
		slog.String("staker", staker.PublicKey.String()),
		slog.String("validator_account", validatorKey.String()),
		slog.Uint64("lamports", lamports),
	)
}

func (node *posNode) JailValidator(ctx context.Context, stakerSeed string, validatorAddress string, jailUntilEpoch uint64) (string, error) {
	staker, err := keyPairFromSeed(stakerSeed)
	if err != nil {
		return "", err
	}
	validatorKey, err := decodePublicKey(validatorAddress, "validator address")
	if err != nil {
		return "", fmt.Errorf("posnode: decode validator address: %w", err)
	}
	transaction, err := blockchain.NewJailValidatorTransaction(staker, validatorKey, jailUntilEpoch, node.ledger.Head().BlockHash)
	if err != nil {
		return "", err
	}
	return node.submitTransaction(ctx, transaction, "jail_validator",
		slog.String("staker", staker.PublicKey.String()),
		slog.String("validator_account", validatorKey.String()),
		slog.Uint64("jail_until_epoch", jailUntilEpoch),
	)
}

func (node *posNode) GetValidatorSet(ctx context.Context) (rpc.ValidatorSetResult, error) {
	_ = ctx
	head := node.ledger.Head()
	state := node.ledger.State()
	validators := make([]rpc.ValidatorInfo, 0)
	for _, account := range state.Accounts {
		if account.Account.Owner != structure.DefaultBuiltinProgramIDs.Stake || len(account.Account.Data) == 0 {
			continue
		}
		stakeState, err := stake.UnmarshalValidatorStateBinary(account.Account.Data)
		if err != nil {
			continue
		}
		effectiveStake, err := stake.EffectiveStakeAtEpoch(stakeState, head.EpochID)
		if err != nil {
			return rpc.ValidatorSetResult{}, err
		}
		validatorInfo, err := validatorInfoFromStakeState(account.Address, stakeState, effectiveStake)
		if err != nil {
			return rpc.ValidatorSetResult{}, err
		}
		validators = append(validators, validatorInfo)
	}
	sort.Slice(validators, func(leftIndex int, rightIndex int) bool {
		return validators[leftIndex].ValidatorID < validators[rightIndex].ValidatorID
	})
	return rpc.ValidatorSetResult{Validators: validators}, nil
}

func validatorInfoFromStakeState(
	accountAddress structure.PublicKey,
	stakeState stake.ValidatorState,
	effectiveStake uint64,
) (rpc.ValidatorInfo, error) {
	bondedStakeLamports, err := bondedStakeLamportsFromStakeState(stakeState)
	if err != nil {
		return rpc.ValidatorInfo{}, err
	}
	selfStakeLamports, err := stake.SelfActiveStake(stakeState)
	if err != nil {
		return rpc.ValidatorInfo{}, err
	}
	selfPendingStakeLamports, err := stake.SelfPendingStake(stakeState)
	if err != nil {
		return rpc.ValidatorInfo{}, err
	}
	selfUnlockingStakeLamports, err := stake.SelfUnlockingStake(stakeState)
	if err != nil {
		return rpc.ValidatorInfo{}, err
	}
	delegatedLamports, err := stake.TotalDelegatedStake(stakeState)
	if err != nil {
		return rpc.ValidatorInfo{}, err
	}
	delegations, err := delegationInfos(stakeState.Delegations)
	if err != nil {
		return rpc.ValidatorInfo{}, err
	}
	return rpc.ValidatorInfo{
		ValidatorID:                string(consensus.NewValidatorID(stakeState.ConsensusPublicKey)),
		AccountAddress:             accountAddress.String(),
		StakerAddress:              stakeState.StakerAccount.String(),
		ConsensusPublicKey:         stakeState.ConsensusPublicKey.String(),
		P2PPeerID:                  stakeState.P2PPeerID,
		StakeLamports:              bondedStakeLamports,
		BondedStakeLamports:        bondedStakeLamports,
		EffectiveStakeLamports:     effectiveStake,
		SelfStakeLamports:          selfStakeLamports,
		SelfPendingStakeLamports:   selfPendingStakeLamports,
		SelfUnlockingStakeLamports: selfUnlockingStakeLamports,
		SelfRewardLamports:         stakeState.SelfRewardLamports,
		CommissionRewardLamports:   stakeState.CommissionRewardLamports,
		DelegatedLamports:          delegatedLamports,
		DelegatorCount:             len(stakeState.Delegations),
		Status:                     stakeValidatorStatusText(stakeState.Status),
		CommissionBps:              stakeState.CommissionBps,
		VoteCredits:                stakeState.VoteCredits,
		RewardLamports:             stakeState.RewardLamports,
		LastRewardedSlot:           stakeState.LastRewardedSlot,
		LastRewardEpoch:            stakeState.LastRewardEpoch,
		JailUntilEpoch:             stakeState.JailUntilEpoch,
		ActivationEpoch:            stakeState.ActivationEpoch,
		DeactivationEpoch:          stakeState.DeactivationEpoch,
		LastEffectiveStakeLamports: stakeState.LastEffectiveStake,
		LastSlashedSlot:            stakeState.LastSlashedSlot,
		Delegations:                delegations,
	}, nil
}

func bondedStakeLamportsFromStakeState(stakeState stake.ValidatorState) (uint64, error) {
	// 功能目的：计算用户可见质押本金；实现原因：jailed 只清零共识权重不能隐藏本金。
	totalStakeLamports := stakeState.ActiveStake
	if ^uint64(0)-totalStakeLamports < stakeState.PendingStake {
		return 0, fmt.Errorf("posnode: bonded stake overflow")
	}
	totalStakeLamports += stakeState.PendingStake
	if ^uint64(0)-totalStakeLamports < stakeState.UnlockingStake {
		return 0, fmt.Errorf("posnode: bonded stake overflow")
	}
	return totalStakeLamports + stakeState.UnlockingStake, nil
}

func delegationInfos(delegations []stake.DelegationState) ([]rpc.DelegationInfo, error) {
	if len(delegations) == 0 {
		return nil, nil
	}
	result := make([]rpc.DelegationInfo, 0, len(delegations))
	for _, delegation := range delegations {
		totalStakeLamports, err := totalDelegationStakeLamports(delegation)
		if err != nil {
			return nil, err
		}
		result = append(result, rpc.DelegationInfo{
			DelegatorAddress:       delegation.DelegatorAccount.String(),
			ActiveStakeLamports:    delegation.ActiveStake,
			PendingStakeLamports:   delegation.PendingStake,
			UnlockingStakeLamports: delegation.UnlockingStake,
			TotalStakeLamports:     totalStakeLamports,
			RewardLamports:         delegation.RewardLamports,
			ActivationEpoch:        delegation.ActivationEpoch,
			DeactivationEpoch:      delegation.DeactivationEpoch,
			UnlockEpoch:            delegation.UnlockEpoch,
		})
	}
	return result, nil
}

func totalDelegationStakeLamports(delegation stake.DelegationState) (uint64, error) {
	// 功能目的：计算委托可见本金；实现原因：前端需要同时展示 active、pending 和 unlocking。
	totalStakeLamports := delegation.ActiveStake
	if ^uint64(0)-totalStakeLamports < delegation.PendingStake {
		return 0, fmt.Errorf("posnode: delegation stake overflow")
	}
	totalStakeLamports += delegation.PendingStake
	if ^uint64(0)-totalStakeLamports < delegation.UnlockingStake {
		return 0, fmt.Errorf("posnode: delegation stake overflow")
	}
	return totalStakeLamports + delegation.UnlockingStake, nil
}

func (node *posNode) GetNodeStatus(ctx context.Context) (any, error) {
	_ = ctx
	return node.statusSnapshot(), nil
}

// GetPeerNetwork 返回 peer 拓扑视图 + 让钱包区分链上验证者、已解析地址和当前连接。
func (node *posNode) GetPeerNetwork(ctx context.Context) (rpc.PeerNetworkResult, error) {
	_ = ctx
	result := rpc.PeerNetworkResult{LocalPeerID: node.peerKeyPair.peerID}
	if node.host == nil {
		return result, nil
	}

	peerSnapshots := node.host.PeerSnapshots()
	connectionStates := make(map[string]p2p.ConnectionState, len(peerSnapshots))
	for _, peerSnapshot := range peerSnapshots {
		connectionState, ok := node.host.ConnectionState(peerSnapshot.ID)
		if !ok {
			continue
		}
		connectionStates[peerSnapshot.ID] = connectionState
	}
	return buildPeerNetworkResult(node.peerKeyPair.peerID, peerSnapshots, connectionStates), nil
}

func (node *posNode) GetHealth(ctx context.Context) (rpc.HealthResult, error) {
	_ = ctx
	if node.ledger == nil {
		ready := false
		if node.bootstrapCoordinator != nil {
			status, err := node.bootstrapCoordinator.GetBootstrapStatus(ctx)
			if err != nil {
				return rpc.HealthResult{}, err
			}
			ready = status.Ready && len(node.connectedBootstrapValidatorPeerIDs()) > 0
		}
		return rpc.HealthResult{OK: ready}, nil
	}
	now := time.Now()
	head := node.ledger.Head()
	headFreshnessWindow := node.healthHeadFreshnessWindow()
	headAgeMillis, chainProgressing := healthHeadProgress(now, head, headFreshnessWindow)
	node.mutex.Lock()
	mempoolSize := len(node.mempool)
	node.mutex.Unlock()
	livenessGate := node.refreshLivenessGate(now)
	transactionSubmissionEnabled, transactionSubmissionReason := transactionSubmissionHealth(livenessGate, chainProgressing)
	healthOK := livenessGateHealthOK(livenessGate)
	if livenessGate.ProductionEnabled && !chainProgressing {
		healthOK = false
	}
	if node.ledger.FinalitySuspended() {
		healthOK = false
		transactionSubmissionEnabled = false
		transactionSubmissionReason = "finality_suspended"
	}
	return rpc.HealthResult{
		OK:                                healthOK,
		HeadHeight:                        head.Height,
		HeadSlot:                          head.Slot,
		FinalizedHeight:                   head.FinalizedHeight,
		HeadUpdatedUnixMilli:              head.UpdatedAtMs,
		HeadAgeMillis:                     headAgeMillis,
		HeadStaleThresholdMillis:          headFreshnessWindow.Milliseconds(),
		ChainProgressing:                  chainProgressing,
		TransactionSubmissionEnabled:      transactionSubmissionEnabled,
		TransactionSubmissionReason:       transactionSubmissionReason,
		MempoolSize:                       mempoolSize,
		LivenessState:                     livenessGate.State,
		LivenessMode:                      livenessGate.Mode,
		LivenessReason:                    livenessGate.Reason,
		LivenessQuorumReady:               livenessGate.QuorumReady,
		LivenessProductionEnabled:         livenessGate.ProductionEnabled,
		ReachableStakeLamports:            livenessGate.ReachableStakeLamports,
		RequiredStakeLamports:             livenessGate.RequiredStakeLamports,
		TotalActiveStakeLamports:          livenessGate.TotalActiveStakeLamports,
		RecentReachabilityWindowMillis:    livenessGate.RecentReachabilityWindowMillis,
		LastReachableStakeUpdateUnixMilli: livenessGate.LastReachableStakeUpdateUnixMilli,
	}, nil
}

// healthHeadFreshnessWindow 计算链头新鲜度窗口 + 允许少量 slot 抖动但快速暴露停止出块。
func (node *posNode) healthHeadFreshnessWindow() time.Duration {
	window := node.config.slotDuration() * healthHeadFreshnessSlotMultiplier
	if window < minHealthHeadFreshnessWindow {
		return minHealthHeadFreshnessWindow
	}
	if window > maxHealthHeadFreshnessWindow {
		return maxHealthHeadFreshnessWindow
	}
	return window
}

// healthHeadProgress 计算链头推进状态 + APP 需要拒绝已经停止出块的提交入口。
func healthHeadProgress(now time.Time, head blockchain.Head, freshnessWindow time.Duration) (int64, bool) {
	if head.UpdatedAtMs <= 0 {
		return 0, false
	}
	headAgeMillis := now.UnixMilli() - head.UpdatedAtMs
	if headAgeMillis < 0 {
		headAgeMillis = 0
	}
	return headAgeMillis, time.Duration(headAgeMillis)*time.Millisecond <= freshnessWindow
}

// transactionSubmissionHealth 汇总提交门禁 + 把共识不可用和链头陈旧拆成可读原因。
func transactionSubmissionHealth(livenessGate livenessGateJSON, chainProgressing bool) (bool, string) {
	if !livenessGate.UserTransactionPackagingEnabled {
		if livenessGate.Reason != "" {
			return false, livenessGate.Reason
		}
		return false, "transaction_packaging_disabled"
	}
	if !chainProgressing {
		return false, "chain_head_not_progressing"
	}
	return true, "ready"
}

func (node *posNode) lookupMempoolTransaction(signature string) (rpc.TransactionDetailResult, bool, error) {
	node.mutex.Lock()
	defer node.mutex.Unlock()

	for _, transaction := range node.mempool {
		transactionID, err := transaction.TxIDString()
		if err != nil {
			return rpc.TransactionDetailResult{}, false, fmt.Errorf("posnode: decode mempool transaction id: %w", err)
		}
		if transactionID != signature {
			continue
		}
		return buildTransactionDetailResult(signature, transaction.Clone(), "mempool", "pending", 0, 0, structure.Hash{}, "", false), true, nil
	}
	return rpc.TransactionDetailResult{}, false, nil
}

func (node *posNode) committedTransactionResult(
	signature string,
	transaction structure.Transaction,
	proposal consensus.BlockProposal,
	blockHash structure.Hash,
) rpc.TransactionDetailResult {
	head := node.ledger.Head()
	finalized := proposal.Header.Height <= head.FinalizedHeight
	status := "confirmed"
	if finalized {
		status = "finalized"
	}
	return buildTransactionDetailResult(
		signature,
		transaction,
		"block",
		status,
		proposal.Header.Height,
		proposal.Header.Slot,
		blockHash,
		node.blockLeaderMetadata(proposal, blockHash).address,
		finalized,
	)
}

func (node *posNode) submitTransaction(ctx context.Context, transaction structure.Transaction, action string, attrs ...slog.Attr) (transactionID string, err error) {
	startedAt := time.Now()
	head := node.ledger.Head()
	defer func() {
		node.logRPCTransactionSubmit(ctx, action, transactionID, head, startedAt, err, attrs...)
	}()
	transactionID, err = transaction.TxIDString()
	if err != nil {
		return "", err
	}
	if existingTransaction, exists := node.mempoolTransactionByID(transactionID); exists {
		node.scheduleRPCTransactionBroadcast(ctx, existingTransaction, transactionID)
		return transactionID, nil
	}
	committed, err := node.transactionAlreadyCommitted(transactionID)
	if err != nil {
		return "", err
	}
	if committed {
		node.clearTransactionTracking(transactionID)
		return transactionID, nil
	}
	containsShieldedInstruction, err := transactionContainsShieldedInstruction(transaction)
	if err != nil {
		return "", err
	}
	if err := validateTransactionResourceDemand(transaction); err != nil {
		node.metrics.transactionsDrop.Add(1)
		return "", err
	}
	if containsShieldedInstruction {
		if err := node.addTransactionWithContext(ctx, transaction); err != nil {
			return "", err
		}
		node.scheduleRPCTransactionBroadcast(ctx, transaction, transactionID)
		return transactionID, nil
	}
	if err := node.ensureHeadBlockhashAvailable(head); err != nil {
		return "", err
	}
	if err := node.preflightTransaction(ctx, transaction, head); err != nil {
		node.metrics.transactionsDrop.Add(1)
		return "", err
	}
	if err := node.addTransaction(transaction); err != nil {
		return "", err
	}
	node.scheduleRPCTransactionBroadcast(ctx, transaction, transactionID)
	return transactionID, nil
}

// scheduleRPCTransactionBroadcast 后台扩散已接收交易 + RPC sendTransaction 必须在入池后快速返回签名。
func (node *posNode) scheduleRPCTransactionBroadcast(ctx context.Context, transaction structure.Transaction, transactionID string) {
	if node.host == nil || !node.config.transactionForwardEnabled() {
		return
	}
	node.startWorker(func() {
		baseContext := context.Background()
		if ctx != nil {
			baseContext = context.WithoutCancel(ctx)
		}
		broadcastContext, cancel := context.WithTimeout(baseContext, rpcTransactionBroadcastTimeout)
		defer cancel()
		node.broadcastTransaction(broadcastContext, transaction)
		node.logger.Debug("posnode rpc transaction broadcast scheduled",
			slog.String("tx_id", transactionID),
			slog.Duration("timeout", rpcTransactionBroadcastTimeout),
		)
	})
}

// preflightTransaction 预执行 RPC 交易 + 避免把必然失败的交易返回成已提交签名。
func (node *posNode) preflightTransaction(ctx context.Context, transaction structure.Transaction, head blockchain.Head) error {
	if node.executor.Programs.IsEmpty() {
		return nil
	}
	state := node.ledger.State()
	node.mutex.Lock()
	blockhashQueue := node.blockhashQueue.Clone()
	pendingTransactions := make([]structure.Transaction, len(node.mempool))
	copy(pendingTransactions, node.mempool)
	node.mutex.Unlock()

	processedTransactionIDs := make(map[string]struct{}, len(pendingTransactions)+1)
	for _, pendingTransaction := range pendingTransactions {
		result, err := node.executePreflightTransaction(ctx, pendingTransaction, head, state, blockhashQueue, processedTransactionIDs)
		if err != nil || result.Execution.Status != structure.TransactionStatusConfirmed {
			continue
		}
		pendingTransactionID, err := pendingTransaction.TxIDString()
		if err == nil {
			processedTransactionIDs[pendingTransactionID] = struct{}{}
		}
		state = applyPreflightWrites(state, result.Execution.WrittenAccounts)
	}

	result, err := node.executePreflightTransaction(ctx, transaction, head, state, blockhashQueue, processedTransactionIDs)
	if err != nil {
		return err
	}
	if result.Execution.Status == structure.TransactionStatusConfirmed {
		return nil
	}
	return fmt.Errorf("posnode: preflight transaction failed: %s", transactionExecutionErrorMessage(result.Execution))
}

func (node *posNode) executePreflightTransaction(
	ctx context.Context,
	transaction structure.Transaction,
	head blockchain.Head,
	state consensus.ChainState,
	blockhashQueue structure.BlockhashQueue,
	processedTransactionIDs map[string]struct{},
) (runtimepkg.TransactionResult, error) {
	result, err := node.executor.ExecuteTransaction(ctx, runtimepkg.TransactionRequest{
		ChainID: node.config.ChainID,
		Slot:    head.Slot,
		Epoch:   head.EpochID,
		Mode:    runtimepkg.ExecutionModeFixedInstruction,
		Simulation: runtimepkg.TransactionSimulationInput{
			Transaction:    transaction,
			Accounts:       state.Accounts,
			BlockhashQueue: blockhashQueue,
			CurrentSlot:    head.Slot,
			CurrentEpoch:   head.EpochID,
			ProcessedTxIDs: processedTransactionIDs,
			Logger:         node.logger,
		},
	})
	if err != nil {
		return runtimepkg.TransactionResult{}, fmt.Errorf("posnode: preflight transaction: %w", err)
	}
	return result, nil
}

func applyPreflightWrites(state consensus.ChainState, writes []structure.AddressedAccount) consensus.ChainState {
	accountIndexByAddress := make(map[structure.PublicKey]int, len(state.Accounts)+len(writes))
	nextAccounts := make([]structure.AddressedAccount, len(state.Accounts))
	for index, account := range state.Accounts {
		nextAccounts[index] = structure.AddressedAccount{Address: account.Address, Account: account.Account.Clone()}
		accountIndexByAddress[account.Address] = index
	}
	for _, write := range writes {
		if index, exists := accountIndexByAddress[write.Address]; exists {
			nextAccounts[index] = structure.AddressedAccount{Address: write.Address, Account: write.Account.Clone()}
			continue
		}
		accountIndexByAddress[write.Address] = len(nextAccounts)
		nextAccounts = append(nextAccounts, structure.AddressedAccount{Address: write.Address, Account: write.Account.Clone()})
	}
	return consensus.ChainState{Accounts: nextAccounts}
}

// ensureHeadBlockhashAvailable 补齐链头 blockhash 窗口 + RPC 使用链头哈希构造交易时必须可立即校验。
func (node *posNode) ensureHeadBlockhashAvailable(head blockchain.Head) error {
	if head.BlockHash == (structure.Hash{}) {
		return nil
	}
	node.mutex.Lock()
	defer node.mutex.Unlock()
	if _, exists := node.blockhashQueue.Find(head.BlockHash); exists {
		return nil
	}
	if err := node.blockhashQueue.Add(structure.RecentBlockhashEntry{
		Blockhash:     head.BlockHash,
		Slot:          head.Slot,
		FeeCalculator: structure.DefaultFeeCalculator(),
		TimestampUnix: time.Now().Unix(),
	}); err != nil {
		return fmt.Errorf("posnode: add head blockhash to queue: %w", err)
	}
	return nil
}

func transactionExecutionErrorMessage(result structure.TransactionExecutionResult) string {
	if result.Error == nil {
		return "transaction rejected"
	}
	return result.Error.Error()
}

func (node *posNode) logRPCTransactionSubmit(
	ctx context.Context,
	action string,
	transactionID string,
	head blockchain.Head,
	startedAt time.Time,
	err error,
	attrs ...slog.Attr,
) {
	logAttrs := []slog.Attr{
		slog.String("action", action),
		slog.String("tx_id", transactionID),
		slog.Uint64("head_height", head.Height),
		slog.Uint64("head_slot", head.Slot),
		slog.String("head_hash", head.BlockHash.String()),
		slog.String("qc_hash", head.QCHash.String()),
		slog.Int64("duration_ms", time.Since(startedAt).Milliseconds()),
	}
	logAttrs = append(logAttrs, attrs...)
	if err != nil {
		logAttrs = append(logAttrs, slog.Any("error", err))
		node.logger.LogAttrs(ctx, slog.LevelError, "posnode rpc transaction submit failed", logAttrs...)
		return
	}
	node.logger.LogAttrs(ctx, slog.LevelInfo, "posnode rpc transaction submitted", logAttrs...)
}

func (node *posNode) loadValidatorStakeState(validatorKey structure.PublicKey) (stake.ValidatorState, error) {
	account, found, err := node.ledger.Account(validatorKey)
	if err != nil {
		return stake.ValidatorState{}, err
	}
	if !found {
		return stake.ValidatorState{}, fmt.Errorf("posnode: validator account is not registered")
	}
	if account.Owner != structure.DefaultBuiltinProgramIDs.Stake || len(account.Data) == 0 {
		return stake.ValidatorState{}, fmt.Errorf("posnode: account is not a validator stake account")
	}
	state, err := stake.UnmarshalValidatorStateBinary(account.Data)
	if err != nil {
		return stake.ValidatorState{}, fmt.Errorf("posnode: decode validator stake state: %w", err)
	}
	return state, nil
}

func accountTypeName(owner structure.PublicKey) string {
	switch owner {
	case structure.DefaultBuiltinProgramIDs.System:
		return "transparent"
	case structure.DefaultBuiltinProgramIDs.Stake:
		return "stake"
	case structure.DefaultBuiltinProgramIDs.Shielded:
		return "shielded_pool"
	default:
		return "program"
	}
}

func accountInfoResult(address structure.PublicKey, account structure.Account) rpc.AccountInfoResult {
	result := rpc.AccountInfoResult{
		Address:    address.String(),
		Exists:     true,
		Lamports:   strconv.FormatUint(account.Lamports, 10),
		Owner:      account.Owner.String(),
		Executable: account.Executable,
		RentEpoch:  account.RentEpoch,
		DataLength: len(account.Data),
		DataHash:   utils.SHA256Hex(account.Data),
	}
	if account.Owner == structure.DefaultBuiltinProgramIDs.Shielded {
		result.DataRedacted = true
		return result
	}
	result.DataBase64 = base64.StdEncoding.EncodeToString(account.Data)
	return result
}

func transactionSimulationResult(execution structure.TransactionExecutionResult) rpc.TransactionSimulationResult {
	result := rpc.TransactionSimulationResult{
		Status:               transactionSimulationStatus(execution.Status),
		FeeLamports:          strconv.FormatUint(execution.FeeDetails.TotalFee, 10),
		PreBalances:          uint64Strings(execution.PreBalances),
		PostBalances:         uint64Strings(execution.PostBalances),
		LogMessages:          append([]string(nil), execution.LogMessages...),
		InnerInstructions:    transactionSimulationInnerInstructions(execution.InnerInstructions),
		ComputeUnitsConsumed: execution.ComputeUnitsConsumed,
		CostUnits:            execution.CostUnits,
		WrittenAccounts:      make([]rpc.AccountInfoResult, 0, len(execution.WrittenAccounts)),
	}
	if result.PreBalances == nil {
		result.PreBalances = []string{}
	}
	if result.PostBalances == nil {
		result.PostBalances = []string{}
	}
	if result.LogMessages == nil {
		result.LogMessages = []string{}
	}
	if result.InnerInstructions == nil {
		result.InnerInstructions = []rpc.TransactionSimulationInnerInstructionResult{}
	}
	if execution.Error != nil {
		result.Error = transactionSimulationErrorResult(execution.Error)
	}
	if execution.ReturnData != nil {
		result.ReturnData = &rpc.TransactionSimulationReturnDataResult{
			Program:    execution.ReturnData.ProgramID.String(),
			DataBase64: base64.StdEncoding.EncodeToString(execution.ReturnData.Data),
		}
	}
	for _, writtenAccount := range execution.WrittenAccounts {
		result.WrittenAccounts = append(result.WrittenAccounts, accountInfoResult(writtenAccount.Address, writtenAccount.Account))
	}
	return result
}

func transactionSimulationInnerInstructions(values []structure.InnerInstruction) []rpc.TransactionSimulationInnerInstructionResult {
	if values == nil {
		return nil
	}
	result := make([]rpc.TransactionSimulationInnerInstructionResult, len(values))
	for outerIndex, inner := range values {
		result[outerIndex] = rpc.TransactionSimulationInnerInstructionResult{
			Index:        inner.Index,
			Instructions: make([]rpc.TransactionSimulationCompiledInstructionResult, len(inner.Instructions)),
		}
		for instructionIndex, instruction := range inner.Instructions {
			result[outerIndex].Instructions[instructionIndex] = rpc.TransactionSimulationCompiledInstructionResult{
				ProgramIDIndex: instruction.ProgramIDIndex,
				AccountIndexes: append([]uint8(nil), instruction.AccountIndexes...),
				DataBase64:     base64.StdEncoding.EncodeToString(instruction.Data),
			}
		}
	}
	return result
}

func transactionSimulationErrorResult(transactionError *structure.TransactionError) *rpc.TransactionSimulationErrorResult {
	result := &rpc.TransactionSimulationErrorResult{
		Code:    uint16(transactionError.Code),
		Message: transactionError.Error(),
	}
	if transactionError.InstructionError == nil {
		return result
	}
	instructionIndex := transactionError.InstructionError.InstructionIndex
	instructionCode := uint16(transactionError.InstructionError.Code)
	result.InstructionIndex = &instructionIndex
	result.InstructionCode = &instructionCode
	return result
}

func transactionSimulationStatus(status structure.TransactionStatus) string {
	switch status {
	case structure.TransactionStatusPending:
		return "pending"
	case structure.TransactionStatusConfirmed:
		return "confirmed"
	case structure.TransactionStatusFailed:
		return "failed"
	case structure.TransactionStatusExpired:
		return "expired"
	default:
		return "unknown"
	}
}

func uint64Strings(values []uint64) []string {
	if values == nil {
		return nil
	}
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = strconv.FormatUint(value, 10)
	}
	return result
}

func accountTransactionHistoryResult(address structure.PublicKey, page blockchain.AddressHistoryPage, finalizedHeight uint64) rpc.AccountTransactionHistoryResult {
	records := make([]rpc.AccountTransactionRecordResult, len(page.Records))
	for index, record := range page.Records {
		status := "confirmed"
		finalized := record.BlockHeight > 0 && record.BlockHeight <= finalizedHeight
		if finalized {
			status = "finalized"
		}
		records[index] = rpc.AccountTransactionRecordResult{
			Signature:           record.TransactionID,
			Direction:           string(record.Direction),
			Kind:                string(record.Kind),
			Counterparty:        record.Counterparty,
			AmountLamports:      fmt.Sprintf("%d", record.AmountLamports),
			BlockHeight:         record.BlockHeight,
			Slot:                record.Slot,
			Blockhash:           record.BlockHash,
			SubmitTimeUnixMilli: record.SubmitTimeUnixMilli,
			Finalized:           finalized,
			Status:              status,
			Location:            "block",
		}
	}
	return rpc.AccountTransactionHistoryResult{
		Address:    address.String(),
		Scope:      page.Scope,
		Records:    records,
		NextCursor: page.NextCursor,
		HasMore:    page.HasMore,
	}
}

func contractProgramListResult(records []blockchain.ContractProgramRecord) rpc.ContractProgramListResult {
	programs := make([]rpc.ContractProgramResult, len(records))
	for index, record := range records {
		programs[index] = rpc.ContractProgramResult{
			Address:    record.Address.String(),
			Owner:      record.Owner.String(),
			Executable: true,
			Lamports:   fmt.Sprintf("%d", record.Lamports),
			DataLength: record.DataLength,
			CodeHash:   record.CodeHash,
			RentEpoch:  record.RentEpoch,
		}
	}
	return rpc.ContractProgramListResult{
		Scope:    "executable_bpfloader_programs",
		Programs: programs,
	}
}

func assetMintStateResult(address structure.PublicKey, expectedOwner structure.PublicKey, account structure.Account, found bool) rpc.AssetMintStateResult {
	result := rpc.AssetMintStateResult{Address: address.String(), Exists: found}
	if !found {
		return result
	}
	result.Owner = account.Owner.String()
	if account.Owner != expectedOwner {
		result.Error = "asset mint owner mismatch"
		return result
	}
	state, err := vmprogram.UnmarshalAssetMintStateBinary(account.Data)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Kind = assetKindName(state.Kind)
	result.Decimals = state.Decimals
	result.Authority = state.Authority.String()
	result.Supply = strconv.FormatUint(state.Supply, 10)
	result.MaxSupply = strconv.FormatUint(state.MaxSupply, 10)
	result.Name = state.Name
	result.Symbol = state.Symbol
	result.URI = state.URI
	return result
}

func assetBalanceStateResult(address structure.PublicKey, expectedOwner structure.PublicKey, account structure.Account, found bool) rpc.AssetBalanceStateResult {
	result := rpc.AssetBalanceStateResult{Address: address.String(), Exists: found}
	if !found {
		return result
	}
	result.Owner = account.Owner.String()
	if account.Owner != expectedOwner {
		result.Error = "asset balance owner mismatch"
		return result
	}
	state, err := vmprogram.UnmarshalAssetBalanceStateBinary(account.Data)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.Mint = state.Mint.String()
	result.Amount = strconv.FormatUint(state.Amount, 10)
	return result
}

func assetKindName(kind vmprogram.AssetKind) string {
	switch kind {
	case vmprogram.AssetKindFungible:
		return "fungible"
	case vmprogram.AssetKindNFT:
		return "nft"
	default:
		return "unknown"
	}
}

func buildPeerNetworkResult(
	localPeerID string,
	peerSnapshots []p2p.PeerSnapshot,
	connectionStates map[string]p2p.ConnectionState,
) rpc.PeerNetworkResult {
	result := rpc.PeerNetworkResult{
		LocalPeerID: localPeerID,
		Peers:       make([]rpc.PeerNetworkPeerResult, 0, len(peerSnapshots)),
	}
	for _, peerSnapshot := range peerSnapshots {
		connectionState, connected := connectionStates[peerSnapshot.ID]
		result.Peers = append(result.Peers, rpc.PeerNetworkPeerResult{
			PeerID:                    peerSnapshot.ID,
			Status:                    string(peerSnapshot.Status),
			Role:                      string(peerSnapshot.Role),
			Roles:                     p2p.PeerRoleNames(peerSnapshot.Role, peerSnapshot.Capabilities),
			Capabilities:              uint64(peerSnapshot.Capabilities),
			CapabilityNames:           p2p.PeerCapabilityNames(peerSnapshot.Capabilities),
			Validator:                 peerSnapshot.Validator,
			Connected:                 connected,
			BestAddress:               bestPeerAddress(peerSnapshot, connectionState, connected),
			AdvertisedAddresses:       stringifyMultiAddresses(peerSnapshot.AdvertisedAddresses),
			VerifiedAddresses:         stringifyMultiAddresses(peerSnapshot.VerifiedAddresses),
			PreferredProtocols:        stringifyProtocols(peerSnapshot.PreferredProtocols),
			LatestSlot:                peerSnapshot.LatestSlot,
			BlockHeight:               peerSnapshot.BlockHeight,
			FailureCount:              peerSnapshot.FailureCount,
			LastError:                 visiblePeerLastError(peerSnapshot, connected),
			LastSeenUnixMilli:         peerSnapshot.LastSeenUnixMilli,
			LastConnectedUnixMilli:    peerSnapshot.LastConnectedUnixMilli,
			LastDisconnectedUnixMilli: peerSnapshot.LastDisconnectedUnixMilli,
			Connection:                buildPeerConnectionInfo(connectionState, connected),
		})
	}
	sort.Slice(result.Peers, func(leftIndex int, rightIndex int) bool {
		leftPeer := result.Peers[leftIndex]
		rightPeer := result.Peers[rightIndex]
		if leftPeer.Connected != rightPeer.Connected {
			return leftPeer.Connected
		}
		if leftPeer.Validator != rightPeer.Validator {
			return leftPeer.Validator
		}
		if leftPeer.LastConnectedUnixMilli != rightPeer.LastConnectedUnixMilli {
			return leftPeer.LastConnectedUnixMilli > rightPeer.LastConnectedUnixMilli
		}
		return leftPeer.PeerID < rightPeer.PeerID
	})
	return result
}

func visiblePeerLastError(peerSnapshot p2p.PeerSnapshot, connected bool) string {
	if connected {
		return ""
	}
	return peerSnapshot.LastError
}

func buildPeerConnectionInfo(connectionState p2p.ConnectionState, connected bool) *rpc.PeerConnectionInfo {
	if !connected {
		return nil
	}
	return &rpc.PeerConnectionInfo{
		Protocol:               string(connectionState.Protocol),
		RemoteAddress:          connectionState.RemoteAddress,
		ObservedRemoteAddress:  connectionState.ObservedRemoteAddress,
		Encrypted:              connectionState.Encrypted,
		ConnectedAtUnixMilli:   connectionState.ConnectedAtUnixMilli,
		LastReadUnixMilli:      connectionState.LastReadUnixMilli,
		LastWriteUnixMilli:     connectionState.LastWriteUnixMilli,
		LastHeartbeatUnixMilli: connectionState.LastHeartbeatUnixMilli,
		FailureCount:           connectionState.FailureCount,
	}
}

func bestPeerAddress(peerSnapshot p2p.PeerSnapshot, connectionState p2p.ConnectionState, connected bool) string {
	if len(peerSnapshot.VerifiedAddresses) > 0 {
		return peerSnapshot.VerifiedAddresses[0].String()
	}
	if len(peerSnapshot.AdvertisedAddresses) > 0 {
		return peerSnapshot.AdvertisedAddresses[0].String()
	}
	if connected {
		return connectionState.RemoteAddress
	}
	return ""
}

func stringifyMultiAddresses(addresses []utils.MultiAddress) []string {
	if len(addresses) == 0 {
		return nil
	}
	values := make([]string, 0, len(addresses))
	for _, address := range addresses {
		values = append(values, address.String())
	}
	return values
}

func stringifyProtocols(protocols []utils.MultiAddressProtocol) []string {
	if len(protocols) == 0 {
		return nil
	}
	values := make([]string, 0, len(protocols))
	for _, protocol := range protocols {
		values = append(values, string(protocol))
	}
	return values
}

func decodePublicKey(address string, field string) (structure.PublicKey, error) {
	trimmedAddress := strings.TrimSpace(address)
	if trimmedAddress == "" {
		return structure.PublicKey{}, fmt.Errorf("%s is empty", field)
	}
	publicKey, err := structure.PublicKeyFromBase58(trimmedAddress)
	if err != nil {
		return structure.PublicKey{}, fmt.Errorf("%s is invalid: %w", field, err)
	}
	return publicKey, nil
}

func keyPairFromSeed(seedText string) (structure.OrdexusKeyPair, error) {
	seedText = strings.TrimSpace(seedText)
	if seedText == "" {
		return structure.OrdexusKeyPair{}, fmt.Errorf("posnode: seed is empty")
	}
	keyPair, err := structure.KeyPairFromSeed(utils.SHA256([]byte(seedText)))
	if err != nil {
		return structure.OrdexusKeyPair{}, fmt.Errorf("posnode: build keypair: %w", err)
	}
	return keyPair, nil
}

func validatorStatusText(status consensus.ValidatorStatus) string {
	switch status {
	case consensus.ValidatorStatusActive:
		return "active"
	case consensus.ValidatorStatusJailed:
		return "jailed"
	case consensus.ValidatorStatusExiting:
		return "exiting"
	default:
		return "inactive"
	}
}

func stakeStatusText(status stake.ValidatorStatus) string {
	switch status {
	case stake.ValidatorStatusActive:
		return "active"
	case stake.ValidatorStatusJailed:
		return "jailed"
	case stake.ValidatorStatusExiting:
		return "exiting"
	default:
		return "inactive"
	}
}

func buildTransactionDetailResult(
	signature string,
	transaction structure.Transaction,
	location string,
	status string,
	blockHeight uint64,
	slot uint64,
	blockHash structure.Hash,
	leaderAddress string,
	finalized bool,
) rpc.TransactionDetailResult {
	result := rpc.TransactionDetailResult{
		Signature:           signature,
		Found:               true,
		Location:            location,
		Status:              status,
		FeeLamports:         transaction.Fee,
		SubmitTimeUnixMilli: transaction.SubmitTime,
		InstructionCount:    len(transaction.Instructions),
		BlockHeight:         blockHeight,
		Slot:                slot,
		Finalized:           finalized,
	}
	feeDetails, err := estimateTransactionFeeDetails(transaction)
	if err == nil {
		result.FeeLamports = feeDetails.TotalFee
		result.BaseFeeLamports = feeDetails.BaseFee
		result.PrioritizationFeeLamports = feeDetails.PrioritizationFee
		result.BurnedFeeLamports = feeDetails.BurnedFee
		result.LeaderFeeLamports = feeDetails.ValidatorFee
	}
	if leaderAddress != "" {
		result.LeaderAddress = leaderAddress
	}

	sender, err := transaction.Sender()
	if err == nil {
		result.Sender = sender.String()
	}
	if !transaction.RecentBlockhash.IsZero() {
		result.RecentBlockhash = transaction.RecentBlockhash.String()
	}
	if !blockHash.IsZero() {
		result.Blockhash = blockHash.String()
	}
	result.AccountAddresses = transactionAccountAddresses(transaction.Accounts)
	result.WritableAddresses = transactionWritableAddresses(transaction.Accounts)
	if transactionBytes, err := transaction.MarshalBinary(); err == nil {
		result.TransactionBase64 = base64.StdEncoding.EncodeToString(transactionBytes)
	}
	return result
}

func proposalLeaderAddress(proposal consensus.BlockProposal) string {
	for _, reward := range proposal.Rewards {
		if reward.Type != consensus.RewardTypeLeaderFee {
			continue
		}
		if reward.AccountAddress.IsZero() {
			continue
		}
		return reward.AccountAddress.String()
	}
	return ""
}

func transactionAccountAddresses(accounts []structure.AccountMeta) []string {
	if len(accounts) == 0 {
		return nil
	}
	addresses := make([]string, 0, len(accounts))
	for _, account := range accounts {
		addresses = append(addresses, account.PublicKey.String())
	}
	return addresses
}

func transactionWritableAddresses(accounts []structure.AccountMeta) []string {
	writableAddresses := make([]string, 0, len(accounts))
	for _, account := range accounts {
		if !account.IsWritable {
			continue
		}
		writableAddresses = append(writableAddresses, account.PublicKey.String())
	}
	if len(writableAddresses) == 0 {
		return nil
	}
	return writableAddresses
}
