package consensus

import (
	"testing"

	"github.com/tentechtop/ordexus-core/structure"
	"github.com/tentechtop/ordexus-core/utils"
)

func TestSelectElderUsesStakeThenDeterministicTieBreak(t *testing.T) {
	first := networkModeTestKey(t, "elder-first")
	second := networkModeTestKey(t, "elder-second")
	third := networkModeTestKey(t, "elder-third")
	set, err := NewValidatorSet([]ValidatorState{
		{ConsensusPublicKey: first.PublicKey, StakeLamports: 10},
		{ConsensusPublicKey: second.PublicKey, StakeLamports: 20},
		{ConsensusPublicKey: third.PublicKey, StakeLamports: 20},
	})
	if err != nil {
		t.Fatalf("NewValidatorSet() error = %v", err)
	}
	anchor := networkModeTestHash(t, "elder-anchor")
	selected, err := SelectElder(set, anchor, nil)
	if err != nil {
		t.Fatalf("SelectElder() error = %v", err)
	}
	if selected.StakeLamports != 20 {
		t.Fatalf("selected stake = %d, want 20", selected.StakeLamports)
	}
	again, err := SelectElder(set, anchor, nil)
	if err != nil || again.ValidatorID != selected.ValidatorID {
		t.Fatalf("deterministic selection = %q/%v, want %q", again.ValidatorID, err, selected.ValidatorID)
	}
	excluded := map[ValidatorID]struct{}{selected.ValidatorID: {}}
	next, err := SelectElder(set, anchor, excluded)
	if err != nil {
		t.Fatalf("SelectElder(excluded) error = %v", err)
	}
	if next.ValidatorID == selected.ValidatorID || next.StakeLamports != 20 {
		t.Fatalf("next elder = %+v", next)
	}
}

func TestValidatorSetRootCommitsStake(t *testing.T) {
	key := networkModeTestKey(t, "root-validator")
	first, err := NewValidatorSet([]ValidatorState{{ConsensusPublicKey: key.PublicKey, StakeLamports: 10}})
	if err != nil {
		t.Fatalf("NewValidatorSet(first) error = %v", err)
	}
	second, err := NewValidatorSet([]ValidatorState{{ConsensusPublicKey: key.PublicKey, StakeLamports: 11}})
	if err != nil {
		t.Fatalf("NewValidatorSet(second) error = %v", err)
	}
	firstRoot, _ := ValidatorSetRoot(first)
	secondRoot, _ := ValidatorSetRoot(second)
	if firstRoot == secondRoot {
		t.Fatal("validator set root did not commit stake")
	}
}

func networkModeTestKey(t *testing.T, label string) structure.OrdexusKeyPair {
	t.Helper()
	key, err := structure.KeyPairFromSeed(utils.SHA256([]byte(label)))
	if err != nil {
		t.Fatalf("KeyPairFromSeed() error = %v", err)
	}
	return key
}

func networkModeTestHash(t *testing.T, label string) structure.Hash {
	t.Helper()
	hash, err := structure.NewHash(utils.SHA256([]byte(label)))
	if err != nil {
		t.Fatalf("NewHash() error = %v", err)
	}
	return hash
}
