package posnode

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"solana_golang/blockchain"
	"solana_golang/consensus"
	"solana_golang/database"
	"solana_golang/programs/stake"
	"solana_golang/rpc"
	"solana_golang/structure"
)

func TestConsensusStatusExposesLayerWeightAndStakeBuckets(t *testing.T) {
	node := newConsensusStatusTestNode(t)
	status := node.statusSnapshot()
	if !status.Consensus.Available {
		t.Fatalf("consensus status unavailable: %+v", status.Consensus)
	}
	local := status.Consensus.LocalValidator
	if local.ValidatorID == "" {
		t.Fatalf("local validator missing: %+v", status.Consensus)
	}
	if local.TurbineLayer < 0 {
		t.Fatalf("local turbine layer = %d, want >= 0", local.TurbineLayer)
	}
	if local.EffectiveStakeLamports != stake.MinimumStakeLamports {
		t.Fatalf("effective stake = %d, want %d", local.EffectiveStakeLamports, stake.MinimumStakeLamports)
	}
	if local.ActiveStakeLamports != stake.MinimumStakeLamports {
		t.Fatalf("active stake = %d, want %d", local.ActiveStakeLamports, stake.MinimumStakeLamports)
	}
	if local.PendingStakeLamports != stake.MinimumStakeLamports {
		t.Fatalf("pending stake = %d, want %d", local.PendingStakeLamports, stake.MinimumStakeLamports)
	}
	if local.WeightBps == 0 {
		t.Fatalf("weight bps = 0, want positive")
	}
	if len(status.Consensus.Validators) != 3 {
		t.Fatalf("validator count = %d, want 3", len(status.Consensus.Validators))
	}
}

func TestGetConsensusStatusRPC(t *testing.T) {
	node := newConsensusStatusTestNode(t)
	server := rpc.NewServer(rpc.ServerConfig{Logger: node.logger}, rpc.NewDefaultRouter(node))
	response := postPosNodeJSONRPC(t, server, 1, rpc.MethodGetConsensusStatus, []any{})
	status := decodePosNodeRPCResult[consensusStatusJSON](t, response)
	if !status.Available {
		t.Fatalf("consensus status unavailable: %+v", status)
	}
	if status.LocalValidator.PendingStakeLamports != stake.MinimumStakeLamports {
		t.Fatalf("pending stake = %d, want %d", status.LocalValidator.PendingStakeLamports, stake.MinimumStakeLamports)
	}
}

func TestGetValidatorSetUsesCurrentHeadEpoch(t *testing.T) {
	node := newConsensusStatusTestNode(t)
	jailLocalValidatorForTest(t, node, 1)
	state := node.ledger.State()
	matureLocalValidatorForTest(t, node, &state, 2)
	commitConsensusStatusStateAtEpoch(t, node.ledger, state, 2)

	result, err := node.GetValidatorSet(context.Background())
	if err != nil {
		t.Fatalf("GetValidatorSet() error = %v", err)
	}
	localValidatorID := string(consensus.NewValidatorID(node.consensusKeyPair.PublicKey))
	for _, validator := range result.Validators {
		if validator.ValidatorID != localValidatorID {
			continue
		}
		if validator.StakeLamports == 0 || validator.Status != "active" {
			t.Fatalf("validator = %+v, want active stake", validator)
		}
		return
	}
	t.Fatalf("local validator %s missing from current epoch validator set", localValidatorID)
}

func TestGetBlockReturnsComputedLeaderAddress(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{
		Path:   t.TempDir(),
		Engine: database.EnginePebble,
		WAL:    true,
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	node := newConsensusStatusTestNodeWithDatabase(t, db)
	slot := node.ledger.Head().Slot + 3
	height := node.ledger.Head().Height + 1
	leader := commitScheduledLeaderBlock(t, node, slot, height)

	resultBySlot, err := node.GetBlock(context.Background(), slot)
	if err != nil {
		t.Fatalf("GetBlock(slot) error = %v", err)
	}
	if resultBySlot.Slot != slot {
		t.Fatalf("slot result slot = %d, want %d", resultBySlot.Slot, slot)
	}
	if resultBySlot.Height != height {
		t.Fatalf("slot result height = %d, want %d", resultBySlot.Height, height)
	}
	if resultBySlot.Transactions == nil || len(resultBySlot.Transactions) != 0 {
		t.Fatalf("transactions = %#v, want empty array", resultBySlot.Transactions)
	}
	if resultBySlot.LeaderAddress != leader.AccountAddress.String() {
		t.Fatalf("leader address = %s, want %s", resultBySlot.LeaderAddress, leader.AccountAddress.String())
	}
	if resultBySlot.LeaderAddressSource != "block" {
		t.Fatalf("leader source = %q, want block", resultBySlot.LeaderAddressSource)
	}
	if resultBySlot.LeaderStakeLamports == nil || *resultBySlot.LeaderStakeLamports != leader.StakeLamports {
		t.Fatalf("leader stake = %v, want %d", resultBySlot.LeaderStakeLamports, leader.StakeLamports)
	}
	if resultBySlot.LeaderCommissionBps == nil || *resultBySlot.LeaderCommissionBps != leader.CommissionBps {
		t.Fatalf("leader commission = %v, want %d", resultBySlot.LeaderCommissionBps, leader.CommissionBps)
	}
	if resultBySlot.LeaderVoteCredits == nil {
		t.Fatal("leader vote credits missing")
	}

	resultByHeight, err := node.GetBlock(context.Background(), height)
	if err != nil {
		t.Fatalf("GetBlock(height) error = %v", err)
	}
	if resultByHeight.Slot != slot || resultByHeight.Height != height {
		t.Fatalf("height fallback result = slot %d height %d, want slot %d height %d", resultByHeight.Slot, resultByHeight.Height, slot, height)
	}
}

func TestGetBlockReturnsLeaderFromHistoricalState(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{
		Path:   t.TempDir(),
		Engine: database.EnginePebble,
		WAL:    true,
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	node := newConsensusStatusTestNodeWithDatabase(t, db)
	slot := node.ledger.Head().Slot + 3
	height := node.ledger.Head().Height + 1
	leader := commitScheduledLeaderBlock(t, node, slot, height)
	removeEpochLeaderForTest(t, node, leader.ValidatorID)

	result, err := node.GetBlock(context.Background(), slot)
	if err != nil {
		t.Fatalf("GetBlock(slot) error = %v", err)
	}
	if result.LeaderAddress != leader.AccountAddress.String() {
		t.Fatalf("leader address = %s, want historical %s", result.LeaderAddress, leader.AccountAddress.String())
	}
	if result.LeaderCommissionBps == nil || *result.LeaderCommissionBps != leader.CommissionBps {
		t.Fatalf("leader commission = %v, want %d", result.LeaderCommissionBps, leader.CommissionBps)
	}
	if result.LeaderStakeLamports == nil || *result.LeaderStakeLamports != leader.StakeLamports {
		t.Fatalf("leader stake = %v, want %d", result.LeaderStakeLamports, leader.StakeLamports)
	}
}

func TestConsensusStatusZerosEffectiveStakeForJailedLocalValidator(t *testing.T) {
	node := newConsensusStatusTestNode(t)
	jailLocalValidatorForTest(t, node, 1)

	status := node.statusSnapshot()
	local := status.Consensus.LocalValidator
	if local.Status != "jailed" {
		t.Fatalf("local status = %q, want jailed", local.Status)
	}
	if local.EffectiveStakeLamports != 0 {
		t.Fatalf("effective stake = %d, want 0", local.EffectiveStakeLamports)
	}
	if local.WeightBps != 0 {
		t.Fatalf("weight bps = %d, want 0", local.WeightBps)
	}
}

func commitScheduledLeaderBlock(t *testing.T, node *posNode, slot uint64, height uint64) consensus.ValidatorState {
	t.Helper()
	node.mutex.Lock()
	epochContextValue, err := node.epochContextForSlotLocked(slot)
	node.mutex.Unlock()
	if err != nil {
		t.Fatalf("epochContextForSlotLocked() error = %v", err)
	}
	leaderID, err := epochContextValue.Schedule.LeaderForSlot(slot)
	if err != nil {
		t.Fatalf("LeaderForSlot() error = %v", err)
	}
	leader, exists := epochContextValue.Snapshot.ValidatorByID(leaderID)
	if !exists {
		t.Fatalf("leader %s missing from snapshot", leaderID)
	}
	state := node.ledger.State()
	stateRoot, err := state.RootHash()
	if err != nil {
		t.Fatalf("RootHash() error = %v", err)
	}
	head := node.ledger.Head()
	proposal := consensus.BlockProposal{
		Header: consensus.BlockHeader{
			ChainID:            head.ChainID,
			Slot:               slot,
			Height:             height,
			ParentHash:         head.BlockHash,
			PreviousQCHash:     head.QCHash,
			LeaderID:           leaderID,
			EpochID:            epochContextValue.EpochID,
			StateRoot:          stateRoot,
			AccountRoot:        stateRoot,
			TimestampUnixMilli: 1_700_000_000_000,
		},
	}
	if _, err := node.ledger.CommitBlock(blockchain.CommitBlockRequest{Proposal: proposal, NextState: state}); err != nil {
		t.Fatalf("CommitBlock() error = %v", err)
	}
	return leader
}

func removeEpochLeaderForTest(t *testing.T, node *posNode, leaderID consensus.ValidatorID) {
	t.Helper()
	node.mutex.Lock()
	defer node.mutex.Unlock()
	filteredValidators := make([]consensus.ValidatorState, 0, len(node.epochSnapshot.Validators))
	for _, validator := range node.epochSnapshot.Validators {
		if validator.ValidatorID == leaderID {
			continue
		}
		filteredValidators = append(filteredValidators, validator)
	}
	if len(filteredValidators) == len(node.epochSnapshot.Validators) {
		t.Fatalf("leader %s not found in epoch snapshot", leaderID)
	}
	node.epochSnapshot.Validators = filteredValidators
	node.epochSnapshots[node.epochSnapshot.EpochID] = node.epochSnapshot
}

func newConsensusStatusTestNode(t *testing.T) *posNode {
	return newConsensusStatusTestNodeWithDatabase(t, nil)
}

func newConsensusStatusTestNodeWithDatabase(t *testing.T, db database.Database) *posNode {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	validatorKeys := []structure.SolanaKeyPair{
		mustStructureKeyPair("consensus-status-validator-a"),
		mustStructureKeyPair("consensus-status-validator-b"),
		mustStructureKeyPair("consensus-status-validator-c"),
	}
	consensusKeys := []structure.SolanaKeyPair{
		mustStructureKeyPair("consensus-status-consensus-a"),
		mustStructureKeyPair("consensus-status-consensus-b"),
		mustStructureKeyPair("consensus-status-consensus-c"),
	}
	stakerKeys := []structure.SolanaKeyPair{
		mustStructureKeyPair("consensus-status-staker-a"),
		mustStructureKeyPair("consensus-status-staker-b"),
		mustStructureKeyPair("consensus-status-staker-c"),
	}
	genesisValidators := make([]blockchain.GenesisValidator, len(validatorKeys))
	for index := range validatorKeys {
		genesisValidators[index] = blockchain.GenesisValidator{
			StakerAddress:      stakerKeys[index].PublicKey,
			ValidatorAddress:   validatorKeys[index].PublicKey,
			ConsensusPublicKey: consensusKeys[index].PublicKey,
			P2PPeerID:          mustRawKeyPair("consensus-status-peer-" + string(rune('a'+index))).peerID,
			StakeLamports:      stake.MinimumStakeLamports,
		}
	}
	ledger, err := blockchain.NewLedgerFromGenesis(db, blockchain.GenesisConfig{
		ChainID:               "posnode-consensus-status",
		InitialSupplyLamports: 1_000_000_000_000,
		InitialValidators:     genesisValidators,
	})
	if err != nil {
		t.Fatalf("NewLedgerFromGenesis() error = %v", err)
	}
	ledger.SetLogger(logger)
	addPendingStakeForValidator(t, ledger, validatorKeys[0].PublicKey, stake.MinimumStakeLamports)
	validatorSet, err := ledger.ValidatorSetFromStateAtEpoch(0)
	if err != nil {
		t.Fatalf("ValidatorSetFromStateAtEpoch() error = %v", err)
	}
	snapshot, err := consensus.NewEpochSnapshot(0, 1, 16, testHashFromText(t, "consensus-status-seed"), validatorSet)
	if err != nil {
		t.Fatalf("NewEpochSnapshot() error = %v", err)
	}
	schedule, err := consensus.NewLeaderSchedule(snapshot)
	if err != nil {
		t.Fatalf("NewLeaderSchedule() error = %v", err)
	}
	return &posNode{
		config: nodeConfig{
			ChainID:                       "posnode-consensus-status",
			NodeName:                      "consensus-status-node",
			EpochSlots:                    16,
			TurbineFanout:                 2,
			SlotMillis:                    1000,
			GenesisStartMs:                time.Now().Add(time.Hour).UnixMilli(),
			TransactionLeaderForwardSlots: 2,
		},
		logger:           logger,
		db:               db,
		ledger:           ledger,
		consensusKeyPair: consensusKeys[0],
		peerKeyPair:      rawKeyPair{peerID: genesisValidators[0].P2PPeerID},
		epochSnapshot:    snapshot,
		leaderSchedule:   schedule,
		seenTransactions: make(map[string]struct{}),
	}
}

func addPendingStakeForValidator(t *testing.T, ledger *blockchain.Ledger, validatorAddress structure.PublicKey, lamports uint64) {
	t.Helper()
	state := ledger.State()
	for index := range state.Accounts {
		if state.Accounts[index].Address != validatorAddress {
			continue
		}
		stakeState, err := stake.UnmarshalValidatorStateBinary(state.Accounts[index].Account.Data)
		if err != nil {
			t.Fatalf("UnmarshalValidatorStateBinary() error = %v", err)
		}
		stakeState.PendingStake += lamports
		stakeState.ActivationEpoch = 1
		stakeState.LastEffectiveStake = stake.MinimumStakeLamports
		data, err := stakeState.MarshalBinary()
		if err != nil {
			t.Fatalf("MarshalBinary() error = %v", err)
		}
		state.Accounts[index].Account.Data = data
		state.Accounts[index].Account.Lamports += lamports
		commitConsensusStatusState(t, ledger, state)
		return
	}
	t.Fatalf("validator account %s not found", validatorAddress.String())
}

func matureLocalValidatorForTest(t *testing.T, node *posNode, state *consensus.ChainState, epochID uint64) {
	t.Helper()
	validatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	for index := range state.Accounts {
		stakeState, err := stake.UnmarshalValidatorStateBinary(state.Accounts[index].Account.Data)
		if err != nil {
			continue
		}
		if consensus.NewValidatorID(stakeState.ConsensusPublicKey) != validatorID {
			continue
		}
		if err := stake.MatureStakeForEpoch(&stakeState, epochID); err != nil {
			t.Fatalf("MatureStakeForEpoch() error = %v", err)
		}
		data, err := stakeState.MarshalBinary()
		if err != nil {
			t.Fatalf("MarshalBinary() error = %v", err)
		}
		state.Accounts[index].Account.Data = data
		return
	}
	t.Fatalf("local validator %s not found", validatorID)
}

func commitConsensusStatusState(t *testing.T, ledger *blockchain.Ledger, state consensus.ChainState) {
	t.Helper()
	commitConsensusStatusStateAtEpoch(t, ledger, state, ledger.Head().EpochID)
}

func commitConsensusStatusStateAtEpoch(
	t *testing.T,
	ledger *blockchain.Ledger,
	state consensus.ChainState,
	epochID uint64,
) {
	t.Helper()
	head := ledger.Head()
	stateRoot, err := state.RootHash()
	if err != nil {
		t.Fatalf("RootHash() error = %v", err)
	}
	proposal := consensus.BlockProposal{
		Header: consensus.BlockHeader{
			ChainID:        head.ChainID,
			Slot:           head.Slot + 1,
			Height:         head.Height + 1,
			ParentHash:     head.BlockHash,
			PreviousQCHash: head.QCHash,
			LeaderID:       consensus.NewValidatorID(mustStructureKeyPair("consensus-status-consensus-a").PublicKey),
			EpochID:        epochID,
			StateRoot:      stateRoot,
			AccountRoot:    stateRoot,
		},
	}
	if _, err := ledger.CommitBlock(blockchain.CommitBlockRequest{Proposal: proposal, NextState: state}); err != nil {
		t.Fatalf("CommitBlock() error = %v", err)
	}
}
