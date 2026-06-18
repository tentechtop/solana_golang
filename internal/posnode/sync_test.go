package posnode

import (
	"testing"

	"solana_golang/blockchain"
	"solana_golang/consensus"
	"solana_golang/programs/stake"
)

func TestPeerNeedsBlockSyncWhenPeerAhead(t *testing.T) {
	localHead := blockchain.Head{
		Height:          10,
		BlockHash:       testHashFromText(t, "local-head"),
		FinalizedHeight: 8,
	}
	status := statusResponseEnvelope{
		HeadHeight: 11,
		HeadHash:   testHashFromText(t, "peer-head").String(),
	}
	if !peerNeedsBlockSync(localHead, status) {
		t.Fatal("peerNeedsBlockSync() = false, want true")
	}
}

func TestPeerNeedsBlockSyncWhenSameHeightForkDiffers(t *testing.T) {
	localHead := blockchain.Head{
		Height:          10,
		BlockHash:       testHashFromText(t, "local-head"),
		FinalizedHeight: 8,
	}
	status := statusResponseEnvelope{
		HeadHeight: 10,
		HeadHash:   testHashFromText(t, "peer-head").String(),
	}
	if !peerNeedsBlockSync(localHead, status) {
		t.Fatal("peerNeedsBlockSync() = false, want true")
	}
}

func TestPeerNeedsBlockSyncSkipsMatchingHead(t *testing.T) {
	localHash := testHashFromText(t, "local-head")
	localHead := blockchain.Head{
		Height:          10,
		BlockHash:       localHash,
		FinalizedHeight: 8,
	}
	status := statusResponseEnvelope{
		HeadHeight: 10,
		HeadHash:   localHash.String(),
	}
	if peerNeedsBlockSync(localHead, status) {
		t.Fatal("peerNeedsBlockSync() = true, want false")
	}
}

func TestPeerNeedsBlockSyncWhenPeerFinalizedAhead(t *testing.T) {
	localHead := blockchain.Head{
		Height:          12,
		BlockHash:       testHashFromText(t, "local-head"),
		FinalizedHeight: 8,
	}
	status := statusResponseEnvelope{
		HeadHeight:      11,
		HeadHash:        testHashFromText(t, "peer-head").String(),
		FinalizedHeight: 9,
	}
	if !peerNeedsBlockSync(localHead, status) {
		t.Fatal("peerNeedsBlockSync() = false, want true when peer finalized height is ahead")
	}
}

func TestCalculateSyncStartHeightFromAncestorUsesNextHeight(t *testing.T) {
	startHeight := calculateSyncStartHeightFromAncestor(21)
	if startHeight != 22 {
		t.Fatalf("calculateSyncStartHeightFromAncestor() = %d, want 22", startHeight)
	}
}

func TestCalculateSyncStartHeightFromAncestorStartsFromFirstBlockAfterGenesis(t *testing.T) {
	startHeight := calculateSyncStartHeightFromAncestor(0)
	if startHeight != 1 {
		t.Fatalf("calculateSyncStartHeightFromAncestor() = %d, want 1", startHeight)
	}
}

func TestRequiresConnectedValidatorPeerForProduction(t *testing.T) {
	tests := []struct {
		name           string
		validatorCount int
		want           bool
	}{
		{name: "empty", validatorCount: 0, want: false},
		{name: "single", validatorCount: 1, want: false},
		{name: "two", validatorCount: 2, want: true},
		{name: "three", validatorCount: 3, want: true},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			node := &posNode{
				epochSnapshot: consensus.EpochSnapshot{
					Validators: make([]consensus.ValidatorState, testCase.validatorCount),
				},
			}
			if got := node.requiresConnectedValidatorPeerForProduction(); got != testCase.want {
				t.Fatalf("requiresConnectedValidatorPeerForProduction() = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestLocalValidatorEffectiveStakeRejectsCurrentEpochJail(t *testing.T) {
	node := newConsensusStatusTestNode(t)
	stakeValue, active, err := node.localValidatorEffectiveStake(0)
	if err != nil {
		t.Fatalf("localValidatorEffectiveStake() error = %v", err)
	}
	if !active || stakeValue == 0 {
		t.Fatalf("active=%v stake=%d, want active stake", active, stakeValue)
	}

	jailLocalValidatorForTest(t, node, 1)
	stakeValue, active, err = node.localValidatorEffectiveStake(0)
	if err != nil {
		t.Fatalf("localValidatorEffectiveStake(jailed) error = %v", err)
	}
	if active || stakeValue != 0 {
		t.Fatalf("jailed active=%v stake=%d, want inactive", active, stakeValue)
	}

	stakeValue, active, err = node.localValidatorEffectiveStake(1)
	if err != nil {
		t.Fatalf("localValidatorEffectiveStake(expired jail) error = %v", err)
	}
	if !active || stakeValue == 0 {
		t.Fatalf("expired jail active=%v stake=%d, want active stake", active, stakeValue)
	}
}

func jailLocalValidatorForTest(t *testing.T, node *posNode, jailUntilEpoch uint64) {
	t.Helper()
	validatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	state := node.ledger.State()
	for index := range state.Accounts {
		stakeState, err := stake.UnmarshalValidatorStateBinary(state.Accounts[index].Account.Data)
		if err != nil {
			continue
		}
		if consensus.NewValidatorID(stakeState.ConsensusPublicKey) != validatorID {
			continue
		}
		stakeState.Status = stake.ValidatorStatusJailed
		stakeState.JailUntilEpoch = jailUntilEpoch
		stakeState.UnlockEpoch = jailUntilEpoch
		data, err := stakeState.MarshalBinary()
		if err != nil {
			t.Fatalf("MarshalBinary() error = %v", err)
		}
		state.Accounts[index].Account.Data = data
		commitConsensusStatusState(t, node.ledger, state)
		return
	}
	t.Fatalf("local validator %s not found", validatorID)
}

func TestValidatePeerStatusChainIdentityAcceptsMatchingPeer(t *testing.T) {
	config, err := normalizeNodeConfig(minimalNodeConfigForValidation())
	if err != nil {
		t.Fatalf("normalizeNodeConfig() error = %v", err)
	}
	status := statusResponseEnvelope{
		ChainID:           config.ChainID,
		ChainIdentityHash: config.ChainIdentityHash,
		GenesisHash:       config.GenesisHash,
	}
	if err := validatePeerStatusChainIdentity(config, "peer-a", status); err != nil {
		t.Fatalf("validatePeerStatusChainIdentity() error = %v", err)
	}
}

func TestValidatePeerStatusChainIdentityRejectsMismatch(t *testing.T) {
	config, err := normalizeNodeConfig(minimalNodeConfigForValidation())
	if err != nil {
		t.Fatalf("normalizeNodeConfig() error = %v", err)
	}
	status := statusResponseEnvelope{
		ChainID:           config.ChainID,
		ChainIdentityHash: testHashFromText(t, "other-chain").String(),
		GenesisHash:       config.GenesisHash,
	}
	if err := validatePeerStatusChainIdentity(config, "peer-b", status); err == nil {
		t.Fatal("validatePeerStatusChainIdentity() error = nil, want chain identity mismatch")
	}
}
