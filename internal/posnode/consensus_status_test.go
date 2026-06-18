package posnode

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"solana_golang/blockchain"
	"solana_golang/consensus"
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

func newConsensusStatusTestNode(t *testing.T) *posNode {
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
	ledger, err := blockchain.NewLedgerFromGenesis(nil, blockchain.GenesisConfig{
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

func commitConsensusStatusState(t *testing.T, ledger *blockchain.Ledger, state consensus.ChainState) {
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
			EpochID:        head.EpochID,
			StateRoot:      stateRoot,
			AccountRoot:    stateRoot,
		},
	}
	if _, err := ledger.CommitBlock(blockchain.CommitBlockRequest{Proposal: proposal, NextState: state}); err != nil {
		t.Fatalf("CommitBlock() error = %v", err)
	}
}
