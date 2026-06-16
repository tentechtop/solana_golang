package blockchain

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"solana_golang/consensus"
	"solana_golang/database"
	"solana_golang/programs/stake"
	"solana_golang/structure"
	"solana_golang/utils"
)

var (
	chainHeadKey   = []byte("pos/head")
	qcKeyPrefix    = []byte("qc/")
	stateKeyPrefix = []byte("state/")
)

const DefaultFinalityDepth = uint64(2)

// Ledger 保存链账本状态 + 统一提交区块、账户和 QC，避免业务散落在 cmd。
type Ledger struct {
	mutex                 sync.RWMutex
	db                    database.Database
	logger                *slog.Logger
	head                  Head
	state                 consensus.ChainState
	committedTransactions map[string]structure.Hash
	finalityDepth         uint64
	closed                bool
}

// NewLedgerFromGenesis 创建账本 + 空数据库和内存模式都从同一 genesis 状态启动。
func NewLedgerFromGenesis(db database.Database, genesis GenesisConfig) (*Ledger, error) {
	return NewLedgerFromGenesisWithConfig(db, genesis, LedgerConfig{})
}

// NewLedgerFromGenesisWithConfig 创建账本 + 按配置写入 finality 参数和创世状态。
func NewLedgerFromGenesisWithConfig(db database.Database, genesis GenesisConfig, config LedgerConfig) (*Ledger, error) {
	config = normalizeLedgerConfig(config)
	state, head, err := BuildGenesisState(genesis)
	if err != nil {
		return nil, err
	}
	ledger := &Ledger{
		db:                    db,
		head:                  head,
		state:                 state,
		committedTransactions: make(map[string]structure.Hash),
		finalityDepth:         config.FinalityDepth,
	}
	if db != nil {
		if err := ledger.persistGenesisLocked(state, head); err != nil {
			return nil, err
		}
	}
	return ledger, nil
}

func (ledger *Ledger) SetLogger(logger *slog.Logger) {
	ledger.mutex.Lock()
	defer ledger.mutex.Unlock()
	ledger.logger = utils.EnsureLogger(logger)
}

// HasCommittedTransaction 查询主链交易索引 + mempool 和 RPC 用它阻止已上链交易重放。
func (ledger *Ledger) HasCommittedTransaction(transactionID string) (bool, error) {
	if transactionID == "" {
		return false, nil
	}
	ledger.mutex.RLock()
	defer ledger.mutex.RUnlock()
	if ledger.closed {
		return false, ErrLedgerClosed
	}
	_, exists := ledger.committedTransactions[transactionID]
	return exists, nil
}

// LoadOrCreateLedger 加载已有账本或创建 genesis + 节点重启后不能回到创世状态。
func LoadOrCreateLedger(db database.Database, genesis GenesisConfig) (*Ledger, error) {
	return LoadOrCreateLedgerWithConfig(db, genesis, LedgerConfig{})
}

// LoadOrCreateLedgerWithConfig 加载账本并校验状态根 + 防止损坏账户表绕过链头状态。
func LoadOrCreateLedgerWithConfig(db database.Database, genesis GenesisConfig, config LedgerConfig) (*Ledger, error) {
	config = normalizeLedgerConfig(config)
	if db == nil {
		return NewLedgerFromGenesisWithConfig(nil, genesis, config)
	}
	headBytes, err := db.Get(database.TableChain, chainHeadKey)
	if err != nil {
		return NewLedgerFromGenesisWithConfig(db, genesis, config)
	}
	if len(headBytes) == 0 {
		return NewLedgerFromGenesisWithConfig(db, genesis, config)
	}
	head, err := unmarshalHead(headBytes)
	if err != nil {
		return nil, err
	}
	state, err := loadState(db)
	if err != nil {
		return nil, err
	}
	if !config.DisableStateRecovery {
		recoveredState, _, err := recoverLoadedStateFromSnapshot(db, head, state)
		if err != nil {
			return nil, err
		}
		state = recoveredState
	} else if err := validateStateRootMatchesHead(state, head); err != nil {
		return nil, err
	}
	transactionIndex, err := loadCommittedTransactionIndex(db, head)
	if err != nil {
		return nil, err
	}
	return &Ledger{
		db:                    db,
		head:                  head,
		state:                 state,
		committedTransactions: transactionIndex,
		finalityDepth:         config.FinalityDepth,
	}, nil
}

// BuildGenesisState 构建创世状态 + 写死 treasury 并初始化启动验证者 stake account。
func BuildGenesisState(genesis GenesisConfig) (consensus.ChainState, Head, error) {
	if genesis.ChainID == "" {
		return consensus.ChainState{}, Head{}, fmt.Errorf("%w: chain id is empty", ErrInvalidGenesis)
	}
	if genesis.InitialSupplyLamports == 0 {
		genesis.InitialSupplyLamports = consensus.DefaultGenesisSupplyLamports
	}

	treasuryAddress := genesis.TreasuryAddress
	if treasuryAddress.IsZero() {
		treasuryKeyPair, err := consensus.HardcodedGenesisTreasuryKeyPair()
		if err != nil {
			return consensus.ChainState{}, Head{}, err
		}
		treasuryAddress = treasuryKeyPair.PublicKey
	}
	accounts := make([]structure.AddressedAccount, 0, len(genesis.FundedAccounts)+len(genesis.InitialValidators)+3)
	treasuryLamports := genesis.InitialSupplyLamports
	for _, fundedAccount := range genesis.FundedAccounts {
		if fundedAccount.Lamports == 0 {
			return consensus.ChainState{}, Head{}, fmt.Errorf("%w: funded account has zero lamports", ErrInvalidGenesis)
		}
		if treasuryLamports < fundedAccount.Lamports {
			return consensus.ChainState{}, Head{}, fmt.Errorf("%w: funded accounts exceed supply", ErrInvalidGenesis)
		}
		treasuryLamports -= fundedAccount.Lamports
		accounts = append(accounts, mustAccount(fundedAccount.Address, fundedAccount.Lamports, structure.DefaultBuiltinProgramIDs.System, false, nil))
	}
	for _, validator := range genesis.InitialValidators {
		validatorLamports := validator.StakeLamports + 10_000_000
		if treasuryLamports < validatorLamports {
			return consensus.ChainState{}, Head{}, fmt.Errorf("%w: validator stake exceeds supply", ErrInvalidGenesis)
		}
		treasuryLamports -= validatorLamports
		account, err := buildGenesisValidatorAccount(validator)
		if err != nil {
			return consensus.ChainState{}, Head{}, err
		}
		accounts = append(accounts, account)
	}
	accounts = append(accounts, mustAccount(treasuryAddress, treasuryLamports, structure.DefaultBuiltinProgramIDs.System, false, nil))
	accounts = append(accounts, mustAccount(structure.DefaultBuiltinProgramIDs.System, 10_000_000, structure.DefaultBuiltinProgramIDs.NativeLoader, true, nil))
	accounts = append(accounts, mustAccount(structure.DefaultBuiltinProgramIDs.Stake, 10_000_000, structure.DefaultBuiltinProgramIDs.NativeLoader, true, nil))
	state := consensus.ChainState{Accounts: mergeGenesisAccounts(accounts)}
	stateRoot, err := state.RootHash()
	if err != nil {
		return consensus.ChainState{}, Head{}, fmt.Errorf("blockchain: hash genesis state: %w", err)
	}
	genesisHash, err := structure.NewHash(stateRoot[:])
	if err != nil {
		return consensus.ChainState{}, Head{}, err
	}
	head := Head{
		ChainID:       genesis.ChainID,
		Height:        0,
		Slot:          0,
		BlockHash:     genesisHash,
		QCHash:        genesisHash,
		StateRoot:     stateRoot,
		EpochID:       0,
		FinalizedHash: genesisHash,
		UpdatedAtMs:   time.Now().UnixMilli(),
	}
	return state, head, nil
}

// CommitBlock 提交已验证区块 + 区块、账户、索引、QC 和链头必须一起更新。
func (ledger *Ledger) CommitBlock(request CommitBlockRequest) (committedHead Head, err error) {
	startedAt := time.Now()
	var blockHash structure.Hash
	ledger.mutex.Lock()
	logger := ledger.loggerLocked()
	defer func() {
		logCommitBlock(logger, request, blockHash, committedHead, startedAt, err)
	}()
	defer ledger.mutex.Unlock()
	if ledger.closed {
		return Head{}, ErrLedgerClosed
	}
	if err := request.validate(ledger.head); err != nil {
		return Head{}, err
	}
	transactionIDs, err := transactionIDsForProposal(request.Proposal)
	if err != nil {
		return Head{}, err
	}
	if err := ledger.rejectCommittedTransactionIDsLocked(transactionIDs); err != nil {
		return Head{}, err
	}
	blockHash, err = request.Proposal.Hash()
	if err != nil {
		return Head{}, fmt.Errorf("blockchain: hash proposal: %w", err)
	}
	stateRoot, err := request.NextState.RootHash()
	if err != nil {
		return Head{}, fmt.Errorf("blockchain: hash next state: %w", err)
	}
	if stateRoot != request.Proposal.Header.StateRoot {
		return Head{}, fmt.Errorf("%w: state root mismatch", ErrInvalidCommit)
	}

	finalizedHeight := ledger.nextFinalizedHeight(request.Proposal.Header.Height)
	finalizedHash, err := ledger.finalizedHashForCommittedHeight(finalizedHeight)
	if err != nil {
		return Head{}, err
	}
	nextHead := Head{
		ChainID:         request.Proposal.Header.ChainID,
		Height:          request.Proposal.Header.Height,
		Slot:            request.Proposal.Header.Slot,
		BlockHash:       blockHash,
		QCHash:          ledger.head.QCHash,
		StateRoot:       stateRoot,
		EpochID:         request.Proposal.Header.EpochID,
		FinalizedHeight: finalizedHeight,
		FinalizedHash:   finalizedHash,
		UpdatedAtMs:     time.Now().UnixMilli(),
	}
	if request.QC != nil {
		qcHash, err := HashQC(*request.QC)
		if err != nil {
			return Head{}, err
		}
		nextHead.QCHash = qcHash
	}
	if ledger.db != nil {
		if err := ledger.persistCommitLocked(request, blockHash, nextHead, transactionIDs); err != nil {
			return Head{}, err
		}
	}
	ledger.addCommittedTransactionIDsLocked(transactionIDs, blockHash)
	ledger.state = request.NextState
	ledger.head = nextHead
	return nextHead, nil
}

// SaveBlockCandidate 保存候选分叉块 + 不改变主链头，供后续裁决重组使用。
func (ledger *Ledger) SaveBlockCandidate(request CommitBlockRequest) (blockHash structure.Hash, err error) {
	startedAt := time.Now()
	ledger.mutex.Lock()
	logger := ledger.loggerLocked()
	defer func() {
		logBlockCandidate(logger, request, blockHash, startedAt, err)
	}()
	defer ledger.mutex.Unlock()
	if ledger.closed {
		return structure.Hash{}, ErrLedgerClosed
	}
	blockHash, err = request.Proposal.Hash()
	if err != nil {
		return structure.Hash{}, fmt.Errorf("blockchain: hash candidate block: %w", err)
	}
	stateRoot, err := request.NextState.RootHash()
	if err != nil {
		return structure.Hash{}, fmt.Errorf("blockchain: hash candidate state: %w", err)
	}
	if stateRoot != request.Proposal.Header.StateRoot {
		return structure.Hash{}, fmt.Errorf("%w: candidate state root mismatch", ErrInvalidCommit)
	}
	if _, err := transactionIDsForProposal(request.Proposal); err != nil {
		return structure.Hash{}, err
	}
	if ledger.db != nil {
		ops, err := ledger.collectBlockStorageOps(request.Proposal, blockHash, request.NextState)
		if err != nil {
			return structure.Hash{}, err
		}
		if err := ledger.db.DataTransaction(ops); err != nil {
			return structure.Hash{}, fmt.Errorf("blockchain: save candidate: %w", err)
		}
	}
	return blockHash, nil
}

// ReorganizeTo 执行链重组 + 读链视图使用同一个快照，写入一次事务原子提交。
func (ledger *Ledger) ReorganizeTo(newTipHash structure.Hash) (decision ForkDecision, err error) {
	startedAt := time.Now()
	finalizedProtection := false
	oldHead := Head{}
	loggedHead := Head{}
	ledger.mutex.Lock()
	logger := ledger.loggerLocked()
	oldHead = ledger.head
	loggedHead = ledger.head
	defer func() {
		logReorg(logger, newTipHash, oldHead, loggedHead, decision, finalizedProtection, startedAt, err)
	}()
	defer ledger.mutex.Unlock()
	if ledger.closed {
		return ForkDecision{}, ErrLedgerClosed
	}
	if ledger.db == nil {
		return ForkDecision{}, fmt.Errorf("%w: reorg requires persistent database", ErrInvalidCommit)
	}

	readTx, err := ledger.db.BeginReadTransaction()
	if err != nil {
		return ForkDecision{}, fmt.Errorf("blockchain: begin reorg snapshot: %w", err)
	}
	defer readTx.Close()

	newTip, err := loadProposalByHash(readTx, newTipHash)
	if err != nil {
		return ForkDecision{}, err
	}
	if !isBetterTip(newTip.Header, newTipHash, ledger.head) {
		return ForkDecision{Accepted: false, Reason: "candidate is not better than current head"}, nil
	}
	commonAncestor, oldBlocks, newBlocks, err := ledger.collectReorgPlan(readTx, newTip, newTipHash)
	if err != nil {
		return ForkDecision{}, err
	}
	if commonAncestor.Height < ledger.head.FinalizedHeight {
		finalizedProtection = true
		return ForkDecision{}, fmt.Errorf("%w: common ancestor is below finalized height", ErrInvalidCommit)
	}
	nextState, err := loadStateSnapshot(readTx, newTipHash)
	if err != nil {
		return ForkDecision{}, err
	}
	stateRoot, err := nextState.RootHash()
	if err != nil {
		return ForkDecision{}, err
	}
	if stateRoot != newTip.Header.StateRoot {
		return ForkDecision{}, fmt.Errorf("%w: new tip state snapshot mismatch", ErrInvalidCommit)
	}

	finalizedHeight := ledger.nextFinalizedHeight(newTip.Header.Height)
	finalizedHash, err := finalizedHashFromReorgPlan(readTx, finalizedHeight, commonAncestor, newBlocks, ledger.head.FinalizedHash)
	if err != nil {
		return ForkDecision{}, err
	}
	nextHead := Head{
		ChainID:         newTip.Header.ChainID,
		Height:          newTip.Header.Height,
		Slot:            newTip.Header.Slot,
		BlockHash:       newTipHash,
		QCHash:          ledger.head.QCHash,
		StateRoot:       stateRoot,
		EpochID:         newTip.Header.EpochID,
		FinalizedHeight: finalizedHeight,
		FinalizedHash:   finalizedHash,
		UpdatedAtMs:     time.Now().UnixMilli(),
	}
	ops, transactionDelta, err := collectReorgOps(readTx, nextHead, oldBlocks, newBlocks, nextState)
	if err != nil {
		return ForkDecision{}, err
	}
	if err := ledger.db.DataTransaction(ops); err != nil {
		return ForkDecision{}, fmt.Errorf("blockchain: commit reorg transaction: %w", err)
	}
	ledger.applyTransactionIndexDeltaLocked(transactionDelta)
	ledger.head = nextHead
	ledger.state = nextState
	loggedHead = nextHead
	decision = ForkDecision{
		Accepted:       true,
		Reorganized:    len(oldBlocks) > 0,
		CommonAncestor: commonAncestor,
		OldBlocks:      oldBlocks,
		NewBlocks:      newBlocks,
		Reason:         "candidate selected by fork choice",
	}
	return decision, nil
}

// SaveQC 保存最高 QC + 下一次出块必须引用最新确认凭证。
func (ledger *Ledger) SaveQC(qc consensus.QuorumCertificate) (savedHead Head, err error) {
	startedAt := time.Now()
	var qcHash structure.Hash
	ledger.mutex.Lock()
	logger := ledger.loggerLocked()
	savedHead = ledger.head
	defer func() {
		logQCSave(logger, qc, qcHash, savedHead, startedAt, err)
	}()
	defer ledger.mutex.Unlock()
	if ledger.closed {
		return Head{}, ErrLedgerClosed
	}
	if err := qc.Validate(); err != nil {
		return Head{}, err
	}
	qcHash, err = HashQC(qc)
	if err != nil {
		return Head{}, err
	}
	nextHead := ledger.head
	nextHead.QCHash = qcHash
	nextHead.UpdatedAtMs = time.Now().UnixMilli()
	if ledger.db != nil {
		qcBytes, err := qc.MarshalBinary()
		if err != nil {
			return Head{}, fmt.Errorf("blockchain: marshal qc: %w", err)
		}
		headBytes, err := marshalHead(nextHead)
		if err != nil {
			return Head{}, err
		}
		operations := []database.DBOperation{
			database.NewUpdateOperation(database.TableChain, prefixedHashKey(qcKeyPrefix, qcHash), qcBytes),
			database.NewUpdateOperation(database.TableChain, chainHeadKey, headBytes),
		}
		if err := ledger.db.DataTransaction(operations); err != nil {
			return Head{}, err
		}
	}
	ledger.head = nextHead
	return nextHead, nil
}

// RewardQCs 读取已达到 finalized 高度的 QC + leader 出块时用它们确定性生成投票奖励。
func (ledger *Ledger) RewardQCs(maxBlockHeight uint64, limit int) ([]consensus.QuorumCertificate, error) {
	ledger.mutex.RLock()
	defer ledger.mutex.RUnlock()
	if ledger.closed {
		return nil, ErrLedgerClosed
	}
	if ledger.db == nil || maxBlockHeight == 0 || limit <= 0 {
		return nil, nil
	}
	readTx, err := ledger.db.BeginReadTransaction()
	if err != nil {
		return nil, fmt.Errorf("blockchain: begin reward qc snapshot: %w", err)
	}
	defer readTx.Close()
	values, err := readTx.PrefixQuery(database.TableChain, qcKeyPrefix)
	if err != nil {
		return nil, fmt.Errorf("blockchain: read reward qcs: %w", err)
	}
	bestQCsBySlot := make(map[uint64]consensus.QuorumCertificate)
	mainChainHashesByHeight := make(map[uint64]structure.Hash)
	missingMainChainHeights := make(map[uint64]struct{})
	for _, value := range values {
		qc, err := consensus.UnmarshalCertificateBinary(value.Value)
		if err != nil {
			return nil, err
		}
		if !isRewardCandidateQC(qc, maxBlockHeight) {
			continue
		}
		mainChainHash, exists, err := readMainChainHashForRewardQC(readTx, mainChainHashesByHeight, missingMainChainHeights, qc.BlockHeight)
		if err != nil {
			return nil, err
		}
		if !exists || mainChainHash != qc.BlockHash {
			continue
		}
		currentQC, exists := bestQCsBySlot[qc.Slot]
		if !exists || isBetterRewardQC(qc, currentQC) {
			bestQCsBySlot[qc.Slot] = qc
		}
	}
	qcs := make([]consensus.QuorumCertificate, 0, len(bestQCsBySlot))
	for _, qc := range bestQCsBySlot {
		qcs = append(qcs, qc)
	}
	sort.Slice(qcs, func(leftIndex int, rightIndex int) bool {
		left := qcs[leftIndex]
		right := qcs[rightIndex]
		if left.BlockHeight != right.BlockHeight {
			return left.BlockHeight < right.BlockHeight
		}
		if left.Slot != right.Slot {
			return left.Slot < right.Slot
		}
		return left.BlockHash.String() < right.BlockHash.String()
	})
	if len(qcs) > limit {
		qcs = qcs[:limit]
	}
	return qcs, nil
}

// isRewardCandidateQC 过滤奖励候选 QC + 只允许已 finalized 范围内的确认票参与奖励。
func isRewardCandidateQC(qc consensus.QuorumCertificate, maxBlockHeight uint64) bool {
	return qc.Type == consensus.VoteTypeConfirm && qc.BlockHeight > 0 && qc.BlockHeight <= maxBlockHeight
}

// readMainChainHashForRewardQC 读取主链高度索引 + 奖励只能基于当前主链区块防止侧链污染。
func readMainChainHashForRewardQC(
	readTx database.ReadTransaction,
	cachedHashes map[uint64]structure.Hash,
	missingHeights map[uint64]struct{},
	height uint64,
) (structure.Hash, bool, error) {
	if hash, exists := cachedHashes[height]; exists {
		return hash, true, nil
	}
	if _, missing := missingHeights[height]; missing {
		return structure.Hash{}, false, nil
	}
	hashBytes, err := readTx.Get(database.TableHeightToHash, uint64Key(height))
	if err != nil {
		return structure.Hash{}, false, fmt.Errorf("blockchain: read reward main chain hash: %w", err)
	}
	if len(hashBytes) == 0 {
		missingHeights[height] = struct{}{}
		return structure.Hash{}, false, nil
	}
	hash, err := structure.NewHash(hashBytes)
	if err != nil {
		return structure.Hash{}, false, fmt.Errorf("blockchain: decode reward main chain hash: %w", err)
	}
	cachedHashes[height] = hash
	return hash, true, nil
}

// isBetterRewardQC 选择同 slot 最佳 QC + 多阶段聚合时只用最高确认权重生成奖励。
func isBetterRewardQC(candidate consensus.QuorumCertificate, current consensus.QuorumCertificate) bool {
	if candidate.ConfirmedStake != current.ConfirmedStake {
		return candidate.ConfirmedStake > current.ConfirmedStake
	}
	if len(candidate.Voters) != len(current.Voters) {
		return len(candidate.Voters) > len(current.Voters)
	}
	if candidate.CreatedAtUnixMilli != current.CreatedAtUnixMilli {
		return candidate.CreatedAtUnixMilli > current.CreatedAtUnixMilli
	}
	if candidate.BlockHeight != current.BlockHeight {
		return candidate.BlockHeight > current.BlockHeight
	}
	return candidate.BlockHash.String() < current.BlockHash.String()
}

// State 返回账户状态快照 + 调用方不能修改账本内部切片。
func (ledger *Ledger) State() consensus.ChainState {
	ledger.mutex.RLock()
	defer ledger.mutex.RUnlock()
	return cloneState(ledger.state)
}

// Head 返回主链头 + 出块和状态同步使用统一入口。
func (ledger *Ledger) Head() Head {
	ledger.mutex.RLock()
	defer ledger.mutex.RUnlock()
	return ledger.head
}

// FinalityDepth 返回不可回滚深度 + 供 RPC 和监控确认所有节点使用一致规则。
func (ledger *Ledger) FinalityDepth() uint64 {
	ledger.mutex.RLock()
	defer ledger.mutex.RUnlock()
	if ledger.finalityDepth == 0 {
		return DefaultFinalityDepth
	}
	return ledger.finalityDepth
}

// StateAtBlockHash 读取指定区块状态快照 + 分叉验证必须基于父块快照而不是当前 head。
func (ledger *Ledger) StateAtBlockHash(blockHash structure.Hash) (consensus.ChainState, error) {
	ledger.mutex.RLock()
	defer ledger.mutex.RUnlock()
	if blockHash == ledger.head.BlockHash {
		return cloneState(ledger.state), nil
	}
	if ledger.db == nil {
		return consensus.ChainState{}, fmt.Errorf("%w: historical state requires persistent database", ErrInvalidCommit)
	}
	readTx, err := ledger.db.BeginReadTransaction()
	if err != nil {
		return consensus.ChainState{}, fmt.Errorf("blockchain: begin historical state snapshot: %w", err)
	}
	defer readTx.Close()
	state, err := loadStateSnapshot(readTx, blockHash)
	if err != nil {
		return consensus.ChainState{}, err
	}
	if len(state.Accounts) == 0 {
		return consensus.ChainState{}, fmt.Errorf("%w: historical state not found", ErrInvalidCommit)
	}
	return state, nil
}

// ValidatorSetFromState 从 stake account 生成验证者集合 + leader 选举只依赖链上状态。
func (ledger *Ledger) ValidatorSetFromState() (consensus.ValidatorSet, error) {
	return ledger.ValidatorSetFromStateAtEpoch(ledger.Head().EpochID)
}

// ValidatorSetFromStateAtEpoch 从 stake account 生成目标 epoch 集合 + stake 激活和 jail 过滤必须按同一 epoch 计算。
func (ledger *Ledger) ValidatorSetFromStateAtEpoch(epochID uint64) (consensus.ValidatorSet, error) {
	ledger.mutex.RLock()
	defer ledger.mutex.RUnlock()
	return ValidatorSetFromStateAtEpoch(ledger.state, epochID)
}

// ValidatorSetFromState 从状态扫描 active validator + 新节点必须注册质押后才能进入集合。
func ValidatorSetFromState(state consensus.ChainState) (consensus.ValidatorSet, error) {
	return ValidatorSetFromStateAtEpoch(state, 0)
}

// ValidatorSetFromStateAtEpoch 从状态扫描有效验证者 + 按目标 epoch 计算 effective stake。
func ValidatorSetFromStateAtEpoch(state consensus.ChainState, epochID uint64) (consensus.ValidatorSet, error) {
	validators := make([]consensus.ValidatorState, 0)
	for _, account := range state.Accounts {
		if account.Account.Owner != structure.DefaultBuiltinProgramIDs.Stake || len(account.Account.Data) == 0 {
			continue
		}
		stakeState, err := stake.UnmarshalValidatorStateBinary(account.Account.Data)
		if err != nil {
			continue
		}
		effectiveStake, err := stake.EffectiveStakeAtEpoch(stakeState, epochID)
		if err != nil || effectiveStake == 0 {
			continue
		}
		status := consensus.ValidatorStatusActive
		if stakeState.Status == stake.ValidatorStatusExiting && stakeState.DeactivationEpoch <= epochID {
			status = consensus.ValidatorStatusExiting
		}
		if status != consensus.ValidatorStatusActive {
			continue
		}
		validators = append(validators, consensus.ValidatorState{
			AccountAddress:      account.Address,
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
		})
	}
	return consensus.NewValidatorSet(validators)
}

// BlockByHash 按区块哈希读取区块 + 历史同步必须从持久化快照读取避免脏读。
func (ledger *Ledger) BlockByHash(blockHash structure.Hash) (consensus.BlockProposal, bool, error) {
	ledger.mutex.RLock()
	defer ledger.mutex.RUnlock()
	if ledger.closed {
		return consensus.BlockProposal{}, false, ErrLedgerClosed
	}
	if ledger.db == nil {
		return consensus.BlockProposal{}, false, nil
	}
	readTx, err := ledger.db.BeginReadTransaction()
	if err != nil {
		return consensus.BlockProposal{}, false, fmt.Errorf("blockchain: begin block read snapshot: %w", err)
	}
	defer readTx.Close()
	data, err := readTx.Get(database.TableBlock, blockHash[:])
	if err != nil {
		return consensus.BlockProposal{}, false, fmt.Errorf("blockchain: read block by hash: %w", err)
	}
	if len(data) == 0 {
		return consensus.BlockProposal{}, false, nil
	}
	proposal, err := unmarshalProposal(data)
	if err != nil {
		return consensus.BlockProposal{}, false, err
	}
	return proposal, true, nil
}

// BlockByHeight 按主链高度读取区块 + 同步缺口需要先读高度索引再读区块快照。
func (ledger *Ledger) BlockByHeight(height uint64) (consensus.BlockProposal, structure.Hash, bool, error) {
	ledger.mutex.RLock()
	defer ledger.mutex.RUnlock()
	if ledger.closed {
		return consensus.BlockProposal{}, structure.Hash{}, false, ErrLedgerClosed
	}
	if ledger.db == nil || height == 0 {
		return consensus.BlockProposal{}, structure.Hash{}, false, nil
	}
	readTx, err := ledger.db.BeginReadTransaction()
	if err != nil {
		return consensus.BlockProposal{}, structure.Hash{}, false, fmt.Errorf("blockchain: begin height read snapshot: %w", err)
	}
	defer readTx.Close()
	hashBytes, err := readTx.Get(database.TableHeightToHash, uint64Key(height))
	if err != nil {
		return consensus.BlockProposal{}, structure.Hash{}, false, fmt.Errorf("blockchain: read height index: %w", err)
	}
	if len(hashBytes) == 0 {
		return consensus.BlockProposal{}, structure.Hash{}, false, nil
	}
	blockHash, err := structure.NewHash(hashBytes)
	if err != nil {
		return consensus.BlockProposal{}, structure.Hash{}, false, err
	}
	proposal, err := loadProposalByHash(readTx, blockHash)
	if err != nil {
		return consensus.BlockProposal{}, structure.Hash{}, false, err
	}
	return proposal, blockHash, true, nil
}

// StateSnapshotAtBlockHash 读取指定区块状态快照 + state sync 需要先验证根再应用。
func (ledger *Ledger) StateSnapshotAtBlockHash(blockHash structure.Hash) (consensus.ChainState, bool, error) {
	state, err := ledger.StateAtBlockHash(blockHash)
	if err != nil {
		return consensus.ChainState{}, false, err
	}
	if len(state.Accounts) == 0 {
		return consensus.ChainState{}, false, nil
	}
	return state, true, nil
}

// Account 读取当前主链账户 + RPC 查询必须基于账本内存快照复制返回。
func (ledger *Ledger) Account(address structure.PublicKey) (structure.Account, bool, error) {
	ledger.mutex.RLock()
	defer ledger.mutex.RUnlock()
	if ledger.closed {
		return structure.Account{}, false, ErrLedgerClosed
	}
	for _, account := range ledger.state.Accounts {
		if account.Address == address {
			return account.Account.Clone(), true, nil
		}
	}
	return structure.Account{}, false, nil
}

// SaveExternalBlockSnapshot 保存同步来的区块快照 + 区块和状态检查点必须在同一事务写入。
func (ledger *Ledger) SaveExternalBlockSnapshot(proposal consensus.BlockProposal, state consensus.ChainState) (structure.Hash, error) {
	return ledger.SaveBlockCandidate(CommitBlockRequest{Proposal: proposal, NextState: state})
}

// ImportFinalizedSnapshot 导入可信 finalized 状态 + 新节点同步无需从创世逐块回放。
func (ledger *Ledger) ImportFinalizedSnapshot(request ImportSnapshotRequest) (Head, error) {
	startedAt := time.Now()
	ledger.mutex.Lock()
	defer ledger.mutex.Unlock()
	if ledger.closed {
		return Head{}, ErrLedgerClosed
	}
	if request.Proposal.Header.ChainID != ledger.head.ChainID {
		return Head{}, fmt.Errorf("%w: snapshot chain id mismatch", ErrInvalidCommit)
	}
	blockHash, err := request.Proposal.Hash()
	if err != nil {
		return Head{}, fmt.Errorf("blockchain: hash snapshot block: %w", err)
	}
	stateRoot, err := request.State.RootHash()
	if err != nil {
		return Head{}, fmt.Errorf("blockchain: hash snapshot state: %w", err)
	}
	if stateRoot != request.Proposal.Header.StateRoot {
		return Head{}, fmt.Errorf("%w: snapshot state root mismatch", ErrInvalidCommit)
	}
	if request.Proposal.Header.Height <= ledger.head.Height {
		return ledger.head, nil
	}
	nextHead := Head{
		ChainID:         request.Proposal.Header.ChainID,
		Height:          request.Proposal.Header.Height,
		Slot:            request.Proposal.Header.Slot,
		BlockHash:       blockHash,
		QCHash:          request.Proposal.Header.PreviousQCHash,
		StateRoot:       stateRoot,
		EpochID:         request.Proposal.Header.EpochID,
		FinalizedHeight: request.Proposal.Header.Height,
		FinalizedHash:   blockHash,
		UpdatedAtMs:     time.Now().UnixMilli(),
	}
	if nextHead.QCHash.IsZero() {
		nextHead.QCHash = blockHash
	}
	if ledger.db != nil {
		if err := ledger.persistImportedSnapshotLocked(request, blockHash, nextHead); err != nil {
			return Head{}, err
		}
	}
	ledger.state = cloneState(request.State)
	ledger.head = nextHead
	ledger.loggerLocked().Info("ledger finalized snapshot imported",
		slog.String("chain_id", nextHead.ChainID),
		slog.Uint64("slot", nextHead.Slot),
		slog.Uint64("height", nextHead.Height),
		slog.String("block_hash", nextHead.BlockHash.String()),
		slog.String("state_root", nextHead.StateRoot.String()),
		slog.Duration("duration", time.Since(startedAt)),
	)
	return nextHead, nil
}

func (request CommitBlockRequest) validate(head Head) error {
	if request.Proposal.Header.ChainID == "" {
		return fmt.Errorf("%w: chain id is empty", ErrInvalidCommit)
	}
	if request.Proposal.Header.Height != head.Height+1 {
		return fmt.Errorf("%w: height mismatch", ErrInvalidCommit)
	}
	if request.Proposal.Header.ParentHash != head.BlockHash {
		return fmt.Errorf("%w: parent hash mismatch", ErrInvalidCommit)
	}
	if request.Proposal.Header.Slot <= head.Slot {
		return fmt.Errorf("%w: slot must increase", ErrInvalidCommit)
	}
	return nil
}

func buildGenesisValidatorAccount(validator GenesisValidator) (structure.AddressedAccount, error) {
	if validator.StakeLamports < stake.MinimumStakeLamports {
		return structure.AddressedAccount{}, fmt.Errorf("%w: validator stake below minimum", ErrInvalidGenesis)
	}
	stakeState := stake.ValidatorState{
		ConsensusPublicKey: validator.ConsensusPublicKey,
		BLSPublicKey:       append([]byte(nil), validator.BLSPublicKey...),
		StakerAccount:      validator.StakerAddress,
		P2PPeerID:          validator.P2PPeerID,
		CommissionBps:      validator.CommissionBps,
		ActiveStake:        validator.StakeLamports,
		Status:             stake.ValidatorStatusActive,
		ActivationEpoch:    0,
		LastEffectiveStake: validator.StakeLamports,
	}
	data, err := stakeState.MarshalBinary()
	if err != nil {
		return structure.AddressedAccount{}, fmt.Errorf("blockchain: marshal genesis validator: %w", err)
	}
	return mustAccount(validator.ValidatorAddress, validator.StakeLamports+10_000_000, structure.DefaultBuiltinProgramIDs.Stake, false, data), nil
}

func mustAccount(address structure.PublicKey, lamports uint64, owner structure.PublicKey, executable bool, data []byte) structure.AddressedAccount {
	account, err := structure.NewAccount(lamports, data, owner, executable, 0)
	if err != nil {
		panic(err)
	}
	return structure.AddressedAccount{Address: address, Account: account}
}

func mergeGenesisAccounts(accounts []structure.AddressedAccount) []structure.AddressedAccount {
	merged := make(map[structure.PublicKey]structure.Account, len(accounts))
	for _, account := range accounts {
		current := merged[account.Address]
		if current.Lamports == 0 && len(current.Data) == 0 {
			merged[account.Address] = account.Account.Clone()
			continue
		}
		current.Lamports += account.Account.Lamports
		if len(account.Account.Data) > 0 {
			current = account.Account.Clone()
		}
		merged[account.Address] = current
	}
	result := make([]structure.AddressedAccount, 0, len(merged))
	for address, account := range merged {
		result = append(result, structure.AddressedAccount{Address: address, Account: account.Clone()})
	}
	sort.Slice(result, func(left int, right int) bool {
		return result[left].Address.String() < result[right].Address.String()
	})
	return result
}

func cloneState(state consensus.ChainState) consensus.ChainState {
	accounts := make([]structure.AddressedAccount, len(state.Accounts))
	for index, account := range state.Accounts {
		accounts[index] = account.Clone()
	}
	return consensus.ChainState{Accounts: accounts}
}

func (ledger *Ledger) loggerLocked() *slog.Logger {
	return utils.EnsureLogger(ledger.logger)
}

func logCommitBlock(
	logger *slog.Logger,
	request CommitBlockRequest,
	blockHash structure.Hash,
	head Head,
	startedAt time.Time,
	err error,
) {
	attrs := []slog.Attr{
		slog.String("chain_id", request.Proposal.Header.ChainID),
		slog.Uint64("slot", request.Proposal.Header.Slot),
		slog.Uint64("height", request.Proposal.Header.Height),
		slog.String("block_hash", blockHash.String()),
		slog.String("parent_hash", request.Proposal.Header.ParentHash.String()),
		slog.String("leader_id", string(request.Proposal.Header.LeaderID)),
		slog.Int("tx_count", len(request.Proposal.Transactions)),
		slog.Int("reward_count", len(request.Proposal.Rewards)),
		slog.String("qc_hash", head.QCHash.String()),
		slog.Uint64("finalized_height", head.FinalizedHeight),
		slog.String("finalized_hash", head.FinalizedHash.String()),
		slog.Int64("duration_ms", time.Since(startedAt).Milliseconds()),
	}
	if err != nil {
		attrs = append(attrs, slog.Any("error", err))
		logger.LogAttrs(context.Background(), slog.LevelError, "ledger commit block failed", attrs...)
		return
	}
	logger.LogAttrs(context.Background(), slog.LevelInfo, "ledger commit block committed", attrs...)
}

func logBlockCandidate(
	logger *slog.Logger,
	request CommitBlockRequest,
	blockHash structure.Hash,
	startedAt time.Time,
	err error,
) {
	attrs := []slog.Attr{
		slog.String("chain_id", request.Proposal.Header.ChainID),
		slog.Uint64("slot", request.Proposal.Header.Slot),
		slog.Uint64("height", request.Proposal.Header.Height),
		slog.String("block_hash", blockHash.String()),
		slog.String("parent_hash", request.Proposal.Header.ParentHash.String()),
		slog.String("leader_id", string(request.Proposal.Header.LeaderID)),
		slog.Int("tx_count", len(request.Proposal.Transactions)),
		slog.Int("reward_count", len(request.Proposal.Rewards)),
		slog.String("state_root", request.Proposal.Header.StateRoot.String()),
		slog.Int64("duration_ms", time.Since(startedAt).Milliseconds()),
	}
	if err != nil {
		attrs = append(attrs, slog.Any("error", err))
		logger.LogAttrs(context.Background(), slog.LevelError, "ledger save block candidate failed", attrs...)
		return
	}
	logger.LogAttrs(context.Background(), slog.LevelInfo, "ledger block candidate saved", attrs...)
}

func logReorg(
	logger *slog.Logger,
	newTipHash structure.Hash,
	oldHead Head,
	newHead Head,
	decision ForkDecision,
	finalizedProtection bool,
	startedAt time.Time,
	err error,
) {
	attrs := []slog.Attr{
		slog.String("new_tip_hash", newTipHash.String()),
		slog.Uint64("old_head_height", oldHead.Height),
		slog.String("old_head_hash", oldHead.BlockHash.String()),
		slog.Bool("accepted", decision.Accepted),
		slog.Bool("reorganized", decision.Reorganized),
		slog.String("reason", decision.Reason),
		slog.Bool("finalized_protection", finalizedProtection),
		slog.Uint64("common_ancestor_height", decision.CommonAncestor.Height),
		slog.String("common_ancestor_hash", decision.CommonAncestor.BlockHash.String()),
		slog.Uint64("new_head_height", newHead.Height),
		slog.String("new_head_hash", newHead.BlockHash.String()),
		slog.Uint64("finalized_height", newHead.FinalizedHeight),
		slog.String("finalized_hash", newHead.FinalizedHash.String()),
		slog.Any("old_chain_blocks", hashesToStrings(decision.OldBlocks)),
		slog.Any("new_chain_blocks", hashesToStrings(decision.NewBlocks)),
		slog.Int64("duration_ms", time.Since(startedAt).Milliseconds()),
	}
	if err != nil {
		attrs = append(attrs, slog.Any("error", err))
		logger.LogAttrs(context.Background(), slog.LevelError, "ledger reorg failed", attrs...)
		return
	}
	logger.LogAttrs(context.Background(), slog.LevelInfo, "ledger reorg decided", attrs...)
}

func logQCSave(
	logger *slog.Logger,
	qc consensus.QuorumCertificate,
	qcHash structure.Hash,
	head Head,
	startedAt time.Time,
	err error,
) {
	attrs := []slog.Attr{
		slog.Uint64("slot", qc.Slot),
		slog.Uint64("height", qc.BlockHeight),
		slog.String("block_hash", qc.BlockHash.String()),
		slog.String("qc_hash", qcHash.String()),
		slog.Uint64("confirmed_stake", qc.ConfirmedStake),
		slog.Uint64("threshold_stake", qc.ThresholdStake),
		slog.Int("voter_count", len(qc.Voters)),
		slog.Uint64("head_height", head.Height),
		slog.String("head_hash", head.BlockHash.String()),
		slog.Int64("duration_ms", time.Since(startedAt).Milliseconds()),
	}
	if err != nil {
		attrs = append(attrs, slog.Any("error", err))
		logger.LogAttrs(context.Background(), slog.LevelError, "ledger save qc failed", attrs...)
		return
	}
	logger.LogAttrs(context.Background(), slog.LevelInfo, "ledger qc saved", attrs...)
}

func hashesToStrings(hashes []structure.Hash) []string {
	values := make([]string, len(hashes))
	for index, hash := range hashes {
		values[index] = hash.String()
	}
	return values
}

func (ledger *Ledger) persistCommitLocked(request CommitBlockRequest, blockHash structure.Hash, head Head, transactionIDs []string) error {
	operations, err := ledger.collectBlockStorageOps(request.Proposal, blockHash, request.NextState)
	if err != nil {
		return err
	}
	operations = append(operations,
		database.NewUpdateOperation(database.TableHeightToHash, uint64Key(head.Height), blockHash[:]),
		database.NewUpdateOperation(database.TableHashToHeight, blockHash[:], uint64Key(head.Height)),
	)
	appendTransactionIndexOps(&operations, transactionIDs, blockHash)
	if request.QC != nil {
		qcBytes, err := request.QC.MarshalBinary()
		if err != nil {
			return fmt.Errorf("blockchain: marshal qc: %w", err)
		}
		operations = append(operations, database.NewUpdateOperation(database.TableChain, prefixedHashKey(qcKeyPrefix, head.QCHash), qcBytes))
	}
	for _, account := range request.NextState.Accounts {
		accountBytes, err := account.Account.MarshalBinary()
		if err != nil {
			return fmt.Errorf("blockchain: marshal account: %w", err)
		}
		operations = append(operations, database.NewUpdateOperation(database.TableAccount, account.Address[:], accountBytes))
	}
	headBytes, err := marshalHead(head)
	if err != nil {
		return err
	}
	operations = append(operations, database.NewUpdateOperation(database.TableChain, chainHeadKey, headBytes))
	return ledger.db.DataTransaction(operations)
}

func (ledger *Ledger) persistImportedSnapshotLocked(request ImportSnapshotRequest, blockHash structure.Hash, head Head) error {
	operations, err := ledger.collectBlockStorageOps(request.Proposal, blockHash, request.State)
	if err != nil {
		return err
	}
	operations = append(operations,
		database.NewUpdateOperation(database.TableHeightToHash, uint64Key(head.Height), blockHash[:]),
		database.NewUpdateOperation(database.TableHashToHeight, blockHash[:], uint64Key(head.Height)),
	)
	if err := appendAccountSnapshotImportOps(ledger.db, &operations, request.State); err != nil {
		return err
	}
	headBytes, err := marshalHead(head)
	if err != nil {
		return err
	}
	operations = append(operations, database.NewUpdateOperation(database.TableChain, chainHeadKey, headBytes))
	return ledger.db.DataTransaction(operations)
}

// appendAccountSnapshotImportOps 覆盖当前账户表 + 快照导入后读路径必须与 state root 完全一致。
func appendAccountSnapshotImportOps(db database.Database, operations *[]database.DBOperation, state consensus.ChainState) error {
	snapshotAddresses := make(map[structure.PublicKey]struct{}, len(state.Accounts))
	for _, account := range state.Accounts {
		snapshotAddresses[account.Address] = struct{}{}
	}
	currentAccounts, err := db.PrefixQuery(database.TableAccount, nil)
	if err != nil {
		return fmt.Errorf("blockchain: list current accounts for snapshot import: %w", err)
	}
	for _, currentAccount := range currentAccounts {
		address, err := structure.NewPublicKey(currentAccount.Key)
		if err != nil {
			return fmt.Errorf("blockchain: decode current account address: %w", err)
		}
		if _, exists := snapshotAddresses[address]; exists {
			continue
		}
		*operations = append(*operations, database.NewDeleteOperation(database.TableAccount, currentAccount.Key))
	}
	for _, account := range state.Accounts {
		accountBytes, err := account.Account.MarshalBinary()
		if err != nil {
			return fmt.Errorf("blockchain: marshal imported account: %w", err)
		}
		*operations = append(*operations, database.NewUpdateOperation(database.TableAccount, account.Address[:], accountBytes))
	}
	return nil
}

func (ledger *Ledger) collectBlockStorageOps(proposal consensus.BlockProposal, blockHash structure.Hash, state consensus.ChainState) ([]database.DBOperation, error) {
	blockBytes, err := marshalProposal(proposal)
	if err != nil {
		return nil, err
	}
	operations := []database.DBOperation{
		database.NewUpdateOperation(database.TableBlock, blockHash[:], blockBytes),
		database.NewUpdateOperation(database.TableHashToHeight, blockHash[:], uint64Key(proposal.Header.Height)),
	}
	for _, account := range state.Accounts {
		accountBytes, err := account.Account.MarshalBinary()
		if err != nil {
			return nil, fmt.Errorf("blockchain: marshal state snapshot account: %w", err)
		}
		operations = append(operations, database.NewUpdateOperation(database.TableCheckpoint, stateAccountKey(blockHash, account.Address), accountBytes))
	}
	return operations, nil
}

func transactionIDsForProposal(proposal consensus.BlockProposal) ([]string, error) {
	transactionIDs := make([]string, 0, len(proposal.Transactions))
	seenTransactionIDs := make(map[string]struct{}, len(proposal.Transactions))
	for _, transaction := range proposal.Transactions {
		transactionID, err := transaction.TxIDString()
		if err != nil {
			return nil, fmt.Errorf("blockchain: calculate transaction id: %w", err)
		}
		if _, exists := seenTransactionIDs[transactionID]; exists {
			return nil, fmt.Errorf("%w: duplicate transaction %s in block", ErrInvalidCommit, transactionID)
		}
		seenTransactionIDs[transactionID] = struct{}{}
		transactionIDs = append(transactionIDs, transactionID)
	}
	return transactionIDs, nil
}

func appendTransactionIndexOps(operations *[]database.DBOperation, transactionIDs []string, blockHash structure.Hash) {
	for _, transactionID := range transactionIDs {
		*operations = append(*operations, database.NewUpdateOperation(database.TableTxToBlock, []byte(transactionID), blockHash[:]))
	}
}

func (ledger *Ledger) rejectCommittedTransactionIDsLocked(transactionIDs []string) error {
	for _, transactionID := range transactionIDs {
		if _, exists := ledger.committedTransactions[transactionID]; exists {
			return fmt.Errorf("%w: transaction %s already committed", ErrInvalidCommit, transactionID)
		}
	}
	return nil
}

func (ledger *Ledger) addCommittedTransactionIDsLocked(transactionIDs []string, blockHash structure.Hash) {
	if ledger.committedTransactions == nil {
		ledger.committedTransactions = make(map[string]structure.Hash)
	}
	for _, transactionID := range transactionIDs {
		ledger.committedTransactions[transactionID] = blockHash
	}
}

func (ledger *Ledger) applyTransactionIndexDeltaLocked(delta transactionIndexDelta) {
	if ledger.committedTransactions == nil {
		ledger.committedTransactions = make(map[string]structure.Hash)
	}
	for _, transactionID := range delta.RemoveIDs {
		delete(ledger.committedTransactions, transactionID)
	}
	for transactionID, blockHash := range delta.Add {
		ledger.committedTransactions[transactionID] = blockHash
	}
}

func (ledger *Ledger) persistGenesisLocked(state consensus.ChainState, head Head) error {
	operations := make([]database.DBOperation, 0, len(state.Accounts)*2+3)
	for _, account := range state.Accounts {
		accountBytes, err := account.Account.MarshalBinary()
		if err != nil {
			return fmt.Errorf("blockchain: marshal genesis account: %w", err)
		}
		operations = append(operations,
			database.NewUpdateOperation(database.TableAccount, account.Address[:], accountBytes),
			database.NewUpdateOperation(database.TableCheckpoint, stateAccountKey(head.BlockHash, account.Address), accountBytes),
		)
	}
	headBytes, err := marshalHead(head)
	if err != nil {
		return err
	}
	operations = append(operations,
		database.NewUpdateOperation(database.TableHeightToHash, uint64Key(head.Height), head.BlockHash[:]),
		database.NewUpdateOperation(database.TableHashToHeight, head.BlockHash[:], uint64Key(head.Height)),
		database.NewUpdateOperation(database.TableChain, chainHeadKey, headBytes),
	)
	return ledger.db.DataTransaction(operations)
}

func (ledger *Ledger) persistStateLocked(state consensus.ChainState) error {
	operations := make([]database.DBOperation, 0, len(state.Accounts))
	for _, account := range state.Accounts {
		accountBytes, err := account.Account.MarshalBinary()
		if err != nil {
			return fmt.Errorf("blockchain: marshal genesis account: %w", err)
		}
		operations = append(operations, database.NewUpdateOperation(database.TableAccount, account.Address[:], accountBytes))
	}
	return ledger.db.DataTransaction(operations)
}

func (ledger *Ledger) persistHeadLocked(head Head) error {
	headBytes, err := marshalHead(head)
	if err != nil {
		return err
	}
	return ledger.db.Put(database.TableChain, chainHeadKey, headBytes)
}

func loadState(db database.Database) (consensus.ChainState, error) {
	readTx, err := db.BeginReadTransaction()
	if err != nil {
		return consensus.ChainState{}, fmt.Errorf("blockchain: begin state read snapshot: %w", err)
	}
	defer readTx.Close()
	return loadStateFromReader(readTx)
}

func loadStateFromReader(readTx database.ReadTransaction) (consensus.ChainState, error) {
	values, err := readTx.PrefixQuery(database.TableAccount, nil)
	if err != nil {
		return consensus.ChainState{}, fmt.Errorf("blockchain: load accounts: %w", err)
	}
	accounts := make([]structure.AddressedAccount, 0, len(values))
	for _, value := range values {
		address, err := structure.NewPublicKey(value.Key)
		if err != nil {
			return consensus.ChainState{}, fmt.Errorf("blockchain: load account address: %w", err)
		}
		account, err := structure.UnmarshalAccountBinary(value.Value)
		if err != nil {
			return consensus.ChainState{}, fmt.Errorf("blockchain: load account: %w", err)
		}
		accounts = append(accounts, structure.AddressedAccount{Address: address, Account: account})
	}
	return consensus.ChainState{Accounts: accounts}, nil
}

func normalizeLedgerConfig(config LedgerConfig) LedgerConfig {
	if config.FinalityDepth == 0 {
		config.FinalityDepth = DefaultFinalityDepth
	}
	return config
}

func validateStateRootMatchesHead(state consensus.ChainState, head Head) error {
	stateRoot, err := state.RootHash()
	if err != nil {
		return fmt.Errorf("blockchain: hash loaded state: %w", err)
	}
	if stateRoot != head.StateRoot {
		return fmt.Errorf("%w: loaded state root %s does not match head %s", ErrInvalidCommit, stateRoot.String(), head.StateRoot.String())
	}
	return nil
}

func recoverLoadedStateFromSnapshot(db database.Database, head Head, state consensus.ChainState) (consensus.ChainState, bool, error) {
	if err := validateStateRootMatchesHead(state, head); err == nil {
		return state, false, nil
	}
	readTx, err := db.BeginReadTransaction()
	if err != nil {
		return consensus.ChainState{}, false, fmt.Errorf("blockchain: begin state recovery snapshot: %w", err)
	}
	defer readTx.Close()
	recoveredState, err := loadStateSnapshot(readTx, head.BlockHash)
	if err != nil {
		return consensus.ChainState{}, false, err
	}
	if len(recoveredState.Accounts) == 0 {
		return consensus.ChainState{}, false, fmt.Errorf("%w: head state snapshot not found", ErrInvalidCommit)
	}
	if err := validateStateRootMatchesHead(recoveredState, head); err != nil {
		return consensus.ChainState{}, false, err
	}
	operations := make([]database.DBOperation, 0, len(recoveredState.Accounts))
	if err := appendAccountSnapshotImportOps(db, &operations, recoveredState); err != nil {
		return consensus.ChainState{}, false, err
	}
	if err := db.DataTransaction(operations); err != nil {
		return consensus.ChainState{}, false, fmt.Errorf("blockchain: commit recovered state: %w", err)
	}
	return recoveredState, true, nil
}

func loadCommittedTransactionIndex(db database.Database, head Head) (map[string]structure.Hash, error) {
	readTx, err := db.BeginReadTransaction()
	if err != nil {
		return nil, fmt.Errorf("blockchain: begin transaction index snapshot: %w", err)
	}
	defer readTx.Close()
	values, err := readTx.RangeQuery(database.TableTxToBlock, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("blockchain: load transaction index: %w", err)
	}
	if len(values) > 0 {
		index := make(map[string]structure.Hash, len(values))
		for _, value := range values {
			blockHash, err := structure.NewHash(value.Value)
			if err != nil {
				return nil, err
			}
			index[string(value.Key)] = blockHash
		}
		return index, nil
	}
	return rebuildCommittedTransactionIndex(readTx, head)
}

func rebuildCommittedTransactionIndex(readTx database.ReadTransaction, head Head) (map[string]structure.Hash, error) {
	index := make(map[string]structure.Hash)
	for height := uint64(1); height <= head.Height; height++ {
		blockHashBytes, err := readTx.Get(database.TableHeightToHash, uint64Key(height))
		if err != nil {
			return nil, fmt.Errorf("blockchain: read height index for transaction rebuild: %w", err)
		}
		if len(blockHashBytes) == 0 {
			continue
		}
		blockHash, err := structure.NewHash(blockHashBytes)
		if err != nil {
			return nil, err
		}
		proposal, err := loadProposalByHash(readTx, blockHash)
		if err != nil {
			return nil, err
		}
		transactionIDs, err := transactionIDsForProposal(proposal)
		if err != nil {
			return nil, err
		}
		for _, transactionID := range transactionIDs {
			index[transactionID] = blockHash
		}
	}
	return index, nil
}

func readTransactionIndex(readTx database.ReadTransaction, transactionID string) (structure.Hash, bool, error) {
	blockHashBytes, err := readTx.Get(database.TableTxToBlock, []byte(transactionID))
	if err != nil {
		return structure.Hash{}, false, fmt.Errorf("blockchain: read transaction index: %w", err)
	}
	if len(blockHashBytes) == 0 {
		return structure.Hash{}, false, nil
	}
	blockHash, err := structure.NewHash(blockHashBytes)
	if err != nil {
		return structure.Hash{}, false, err
	}
	return blockHash, true, nil
}

func loadStateSnapshot(readTx database.ReadTransaction, blockHash structure.Hash) (consensus.ChainState, error) {
	prefix := stateBlockPrefix(blockHash)
	values, err := readTx.PrefixQuery(database.TableCheckpoint, prefix)
	if err != nil {
		return consensus.ChainState{}, fmt.Errorf("blockchain: load state snapshot: %w", err)
	}
	accounts := make([]structure.AddressedAccount, 0, len(values))
	for _, value := range values {
		if len(value.Key) != len(prefix)+structure.PublicKeySize {
			return consensus.ChainState{}, fmt.Errorf("blockchain: invalid state snapshot key length")
		}
		address, err := structure.NewPublicKey(value.Key[len(prefix):])
		if err != nil {
			return consensus.ChainState{}, fmt.Errorf("blockchain: decode snapshot address: %w", err)
		}
		account, err := structure.UnmarshalAccountBinary(value.Value)
		if err != nil {
			return consensus.ChainState{}, fmt.Errorf("blockchain: decode snapshot account: %w", err)
		}
		accounts = append(accounts, structure.AddressedAccount{Address: address, Account: account})
	}
	return consensus.ChainState{Accounts: accounts}, nil
}

func marshalProposal(proposal consensus.BlockProposal) ([]byte, error) {
	transactions := make([]string, len(proposal.Transactions))
	for index, transaction := range proposal.Transactions {
		transactionBytes, err := transaction.MarshalBinary()
		if err != nil {
			return nil, fmt.Errorf("blockchain: marshal transaction %d: %w", index, err)
		}
		transactions[index] = encodeBytes(transactionBytes)
	}
	payload := storedProposal{
		Header:          proposal.Header,
		Transactions:    transactions,
		RewardQCs:       append([]consensus.QuorumCertificate(nil), proposal.RewardQCs...),
		Rewards:         append([]consensus.BlockReward(nil), proposal.Rewards...),
		LeaderSignature: encodeBytes(proposal.LeaderSignature[:]),
	}
	return json.Marshal(payload)
}

func unmarshalProposal(data []byte) (consensus.BlockProposal, error) {
	payload := storedProposal{}
	if err := json.Unmarshal(data, &payload); err != nil {
		return consensus.BlockProposal{}, fmt.Errorf("blockchain: decode proposal: %w", err)
	}
	signatureBytes, err := decodeBytes(payload.LeaderSignature)
	if err != nil {
		return consensus.BlockProposal{}, fmt.Errorf("blockchain: decode leader signature: %w", err)
	}
	signature, err := structure.NewSignature(signatureBytes)
	if err != nil {
		return consensus.BlockProposal{}, err
	}
	transactions := make([]structure.Transaction, len(payload.Transactions))
	for index, encodedTransaction := range payload.Transactions {
		transactionBytes, err := decodeBytes(encodedTransaction)
		if err != nil {
			return consensus.BlockProposal{}, fmt.Errorf("blockchain: decode transaction %d: %w", index, err)
		}
		transaction, err := structure.UnmarshalTransactionBinary(transactionBytes)
		if err != nil {
			return consensus.BlockProposal{}, fmt.Errorf("blockchain: unmarshal transaction %d: %w", index, err)
		}
		transactions[index] = transaction
	}
	return consensus.BlockProposal{
		Header:          payload.Header,
		Transactions:    transactions,
		RewardQCs:       append([]consensus.QuorumCertificate(nil), payload.RewardQCs...),
		Rewards:         append([]consensus.BlockReward(nil), payload.Rewards...),
		LeaderSignature: signature,
	}, nil
}

func marshalHead(head Head) ([]byte, error) {
	return json.Marshal(storedHead{
		ChainID:         head.ChainID,
		Height:          head.Height,
		Slot:            head.Slot,
		BlockHash:       encodeBytes(head.BlockHash[:]),
		QCHash:          encodeBytes(head.QCHash[:]),
		StateRoot:       encodeBytes(head.StateRoot[:]),
		EpochID:         head.EpochID,
		FinalizedHeight: head.FinalizedHeight,
		FinalizedHash:   encodeBytes(head.FinalizedHash[:]),
		UpdatedAtMs:     head.UpdatedAtMs,
	})
}

func unmarshalHead(data []byte) (Head, error) {
	payload := storedHead{}
	if err := json.Unmarshal(data, &payload); err != nil {
		return Head{}, fmt.Errorf("blockchain: decode head: %w", err)
	}
	blockHashBytes, err := decodeBytes(payload.BlockHash)
	if err != nil {
		return Head{}, fmt.Errorf("blockchain: decode head block hash: %w", err)
	}
	qcHashBytes, err := decodeBytes(payload.QCHash)
	if err != nil {
		return Head{}, fmt.Errorf("blockchain: decode head qc hash: %w", err)
	}
	stateRootBytes, err := decodeBytes(payload.StateRoot)
	if err != nil {
		return Head{}, fmt.Errorf("blockchain: decode head state root: %w", err)
	}
	blockHash, err := structure.NewHash(blockHashBytes)
	if err != nil {
		return Head{}, err
	}
	qcHash, err := structure.NewHash(qcHashBytes)
	if err != nil {
		return Head{}, err
	}
	stateRoot, err := structure.NewHash(stateRootBytes)
	if err != nil {
		return Head{}, err
	}
	return Head{
		ChainID:         payload.ChainID,
		Height:          payload.Height,
		Slot:            payload.Slot,
		BlockHash:       blockHash,
		QCHash:          qcHash,
		StateRoot:       stateRoot,
		EpochID:         payload.EpochID,
		FinalizedHeight: payload.FinalizedHeight,
		FinalizedHash:   finalizedHashFromPayload(payload),
		UpdatedAtMs:     payload.UpdatedAtMs,
	}, nil
}

func finalizedHashFromPayload(payload storedHead) structure.Hash {
	if payload.FinalizedHash == "" {
		return structure.Hash{}
	}
	value, err := decodeBytes(payload.FinalizedHash)
	if err != nil {
		return structure.Hash{}
	}
	hash, err := structure.NewHash(value)
	if err != nil {
		return structure.Hash{}
	}
	return hash
}

func loadProposalByHash(readTx database.ReadTransaction, hash structure.Hash) (consensus.BlockProposal, error) {
	data, err := readTx.Get(database.TableBlock, hash[:])
	if err != nil {
		return consensus.BlockProposal{}, fmt.Errorf("blockchain: read block: %w", err)
	}
	if len(data) == 0 {
		return consensus.BlockProposal{}, fmt.Errorf("%w: block not found", ErrInvalidCommit)
	}
	return unmarshalProposal(data)
}

func (ledger *Ledger) collectReorgPlan(readTx database.ReadTransaction, newTip consensus.BlockProposal, newTipHash structure.Hash) (Head, []structure.Hash, []structure.Hash, error) {
	newAncestors := make(map[structure.Hash]consensus.BlockProposal)
	cursor := newTip
	cursorHash := newTipHash
	for {
		newAncestors[cursorHash] = cursor
		if cursorHash == ledger.head.BlockHash || cursor.Header.Height == 0 {
			break
		}
		parent, err := loadProposalByHash(readTx, cursor.Header.ParentHash)
		if err != nil {
			if cursor.Header.ParentHash == ledger.head.FinalizedHash || cursor.Header.Height == 1 {
				break
			}
			return Head{}, nil, nil, err
		}
		cursor = parent
		cursorHash = cursor.Header.ParentHash
		if parentHash, err := parent.Hash(); err == nil {
			cursorHash = parentHash
		}
	}

	oldBlocks := make([]structure.Hash, 0)
	oldCursorHash := ledger.head.BlockHash
	for {
		if oldCursorHash == ledger.head.FinalizedHash && ledger.head.FinalizedHeight == 0 {
			newBlocks, err := collectNewBlocks(readTx, newTip, newTipHash, oldCursorHash)
			if err != nil {
				return Head{}, nil, nil, err
			}
			return Head{
				ChainID:   ledger.head.ChainID,
				Height:    0,
				Slot:      0,
				BlockHash: oldCursorHash,
				StateRoot: ledger.head.FinalizedHash,
				EpochID:   0,
			}, oldBlocks, newBlocks, nil
		}
		if ancestor, exists := newAncestors[oldCursorHash]; exists {
			newBlocks, err := collectNewBlocks(readTx, newTip, newTipHash, oldCursorHash)
			if err != nil {
				return Head{}, nil, nil, err
			}
			return Head{
				ChainID:   ancestor.Header.ChainID,
				Height:    ancestor.Header.Height,
				Slot:      ancestor.Header.Slot,
				BlockHash: oldCursorHash,
				StateRoot: ancestor.Header.StateRoot,
				EpochID:   ancestor.Header.EpochID,
			}, oldBlocks, newBlocks, nil
		}
		oldBlock, err := loadProposalByHash(readTx, oldCursorHash)
		if err != nil {
			return Head{}, nil, nil, err
		}
		oldBlocks = append(oldBlocks, oldCursorHash)
		oldCursorHash = oldBlock.Header.ParentHash
	}
}

func collectNewBlocks(readTx database.ReadTransaction, newTip consensus.BlockProposal, newTipHash structure.Hash, ancestorHash structure.Hash) ([]structure.Hash, error) {
	reversed := make([]structure.Hash, 0)
	cursor := newTip
	cursorHash := newTipHash
	for cursorHash != ancestorHash {
		reversed = append(reversed, cursorHash)
		if cursor.Header.ParentHash == ancestorHash {
			break
		}
		parent, err := loadProposalByHash(readTx, cursor.Header.ParentHash)
		if err != nil {
			return nil, err
		}
		cursor = parent
		cursorHash = cursor.Header.ParentHash
		if parentHash, err := parent.Hash(); err == nil {
			cursorHash = parentHash
		}
	}
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	return reversed, nil
}

type transactionIndexDelta struct {
	RemoveIDs []string
	Add       map[string]structure.Hash
}

func collectReorgOps(readTx database.ReadTransaction, head Head, oldBlocks []structure.Hash, newBlocks []structure.Hash, state consensus.ChainState) ([]database.DBOperation, transactionIndexDelta, error) {
	operations := make([]database.DBOperation, 0, len(oldBlocks)+len(newBlocks)+len(state.Accounts)+1)
	delta := transactionIndexDelta{Add: make(map[string]structure.Hash)}
	oldBlockSet := make(map[structure.Hash]struct{}, len(oldBlocks))
	for _, oldHash := range oldBlocks {
		oldBlockSet[oldHash] = struct{}{}
		oldBlock, err := loadProposalByHash(readTx, oldHash)
		if err != nil {
			return nil, delta, err
		}
		oldTransactionIDs, err := transactionIDsForProposal(oldBlock)
		if err != nil {
			return nil, delta, err
		}
		for _, transactionID := range oldTransactionIDs {
			delta.RemoveIDs = append(delta.RemoveIDs, transactionID)
			operations = append(operations, database.NewDeleteOperation(database.TableTxToBlock, []byte(transactionID)))
		}
		operations = append(operations, database.NewDeleteOperation(database.TableHeightToHash, uint64Key(oldBlock.Header.Height)))
	}
	seenNewTransactionIDs := make(map[string]struct{})
	for _, newHash := range newBlocks {
		newBlock, err := loadProposalByHash(readTx, newHash)
		if err != nil {
			return nil, delta, err
		}
		newTransactionIDs, err := transactionIDsForProposal(newBlock)
		if err != nil {
			return nil, delta, err
		}
		for _, transactionID := range newTransactionIDs {
			if _, exists := seenNewTransactionIDs[transactionID]; exists {
				return nil, delta, fmt.Errorf("%w: duplicate transaction %s in new fork", ErrInvalidCommit, transactionID)
			}
			seenNewTransactionIDs[transactionID] = struct{}{}
			indexedBlockHash, exists, err := readTransactionIndex(readTx, transactionID)
			if err != nil {
				return nil, delta, err
			}
			if exists {
				if _, rolledBack := oldBlockSet[indexedBlockHash]; !rolledBack {
					return nil, delta, fmt.Errorf("%w: transaction %s already committed", ErrInvalidCommit, transactionID)
				}
			}
			delta.Add[transactionID] = newHash
			operations = append(operations, database.NewUpdateOperation(database.TableTxToBlock, []byte(transactionID), newHash[:]))
		}
		heightBytes := uint64Key(newBlock.Header.Height)
		operations = append(operations, database.NewUpdateOperation(database.TableHeightToHash, heightBytes, newHash[:]))
	}
	for _, account := range state.Accounts {
		accountBytes, err := account.Account.MarshalBinary()
		if err != nil {
			return nil, delta, fmt.Errorf("blockchain: marshal reorg account: %w", err)
		}
		operations = append(operations, database.NewUpdateOperation(database.TableAccount, account.Address[:], accountBytes))
	}
	headBytes, err := marshalHead(head)
	if err != nil {
		return nil, delta, err
	}
	operations = append(operations, database.NewUpdateOperation(database.TableChain, chainHeadKey, headBytes))
	return operations, delta, nil
}

func isBetterTip(header consensus.BlockHeader, hash structure.Hash, head Head) bool {
	if header.Height > head.Height {
		return true
	}
	if header.Height < head.Height {
		return false
	}
	return hash.String() < head.BlockHash.String()
}

func (ledger *Ledger) finalizedHashForCommittedHeight(finalizedHeight uint64) (structure.Hash, error) {
	if finalizedHeight == 0 {
		return ledger.head.FinalizedHash, nil
	}
	if finalizedHeight == ledger.head.Height {
		return ledger.head.BlockHash, nil
	}
	if ledger.db == nil {
		return ledger.head.FinalizedHash, nil
	}
	hashBytes, err := ledger.db.Get(database.TableHeightToHash, uint64Key(finalizedHeight))
	if err != nil {
		return structure.Hash{}, fmt.Errorf("blockchain: read finalized hash: %w", err)
	}
	if len(hashBytes) == 0 {
		return structure.Hash{}, fmt.Errorf("%w: finalized block not found", ErrInvalidCommit)
	}
	hash, err := structure.NewHash(hashBytes)
	if err != nil {
		return structure.Hash{}, err
	}
	return hash, nil
}

func finalizedHashFromReorgPlan(
	readTx database.ReadTransaction,
	finalizedHeight uint64,
	commonAncestor Head,
	newBlocks []structure.Hash,
	previousFinalizedHash structure.Hash,
) (structure.Hash, error) {
	if finalizedHeight == 0 {
		return previousFinalizedHash, nil
	}
	if finalizedHeight == commonAncestor.Height {
		return commonAncestor.BlockHash, nil
	}
	for _, newHash := range newBlocks {
		block, err := loadProposalByHash(readTx, newHash)
		if err != nil {
			return structure.Hash{}, err
		}
		if block.Header.Height == finalizedHeight {
			return newHash, nil
		}
	}
	hashBytes, err := readTx.Get(database.TableHeightToHash, uint64Key(finalizedHeight))
	if err != nil {
		return structure.Hash{}, fmt.Errorf("blockchain: read reorg finalized hash: %w", err)
	}
	if len(hashBytes) == 0 {
		return structure.Hash{}, fmt.Errorf("%w: reorg finalized block not found", ErrInvalidCommit)
	}
	hash, err := structure.NewHash(hashBytes)
	if err != nil {
		return structure.Hash{}, err
	}
	return hash, nil
}

func (ledger *Ledger) nextFinalizedHeight(nextHeight uint64) uint64 {
	finalityDepth := ledger.finalityDepth
	if finalityDepth == 0 {
		finalityDepth = DefaultFinalityDepth
	}
	if nextHeight <= finalityDepth {
		return ledger.head.FinalizedHeight
	}
	nextFinalizedHeight := nextHeight - finalityDepth
	if nextFinalizedHeight < ledger.head.FinalizedHeight {
		return ledger.head.FinalizedHeight
	}
	return nextFinalizedHeight
}

func prefixedHashKey(prefix []byte, hash structure.Hash) []byte {
	key := make([]byte, 0, len(prefix)+structure.HashSize)
	key = append(key, prefix...)
	key = append(key, hash[:]...)
	return key
}

func stateBlockPrefix(hash structure.Hash) []byte {
	return prefixedHashKey(stateKeyPrefix, hash)
}

func stateAccountKey(hash structure.Hash, address structure.PublicKey) []byte {
	key := stateBlockPrefix(hash)
	key = append(key, address[:]...)
	return key
}

func uint64Key(value uint64) []byte {
	key := make([]byte, 8)
	for index := 0; index < 8; index++ {
		key[7-index] = byte(value >> (uint(index) * 8))
	}
	return key
}
