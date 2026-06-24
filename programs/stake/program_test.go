package stake

import (
	"testing"

	"solana_golang/structure"
)

func TestSlashAndJailInstructionsRoundTrip(t *testing.T) {
	slashInstruction, err := NewSlashValidatorInstruction(123)
	if err != nil {
		t.Fatalf("NewSlashValidatorInstruction() error = %v", err)
	}
	slashBytes, err := slashInstruction.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary(slash) error = %v", err)
	}
	decodedSlash, err := UnmarshalInstructionBinary(slashBytes)
	if err != nil {
		t.Fatalf("UnmarshalInstructionBinary(slash) error = %v", err)
	}
	if decodedSlash.Type != InstructionSlashValidator || decodedSlash.Amount != 123 {
		t.Fatalf("decoded slash = %+v", decodedSlash)
	}

	jailInstruction, err := NewJailValidatorInstruction(9)
	if err != nil {
		t.Fatalf("NewJailValidatorInstruction() error = %v", err)
	}
	jailBytes, err := jailInstruction.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary(jail) error = %v", err)
	}
	decodedJail, err := UnmarshalInstructionBinary(jailBytes)
	if err != nil {
		t.Fatalf("UnmarshalInstructionBinary(jail) error = %v", err)
	}
	if decodedJail.Type != InstructionJailValidator || decodedJail.UnlockEpoch != 9 {
		t.Fatalf("decoded jail = %+v", decodedJail)
	}
}

func TestSlashInstructionRejectsZeroAmount(t *testing.T) {
	if _, err := NewSlashValidatorInstruction(0); err == nil {
		t.Fatal("NewSlashValidatorInstruction(0) error = nil, want error")
	}
}

func TestDelegationInstructionsRoundTrip(t *testing.T) {
	delegateInstruction, err := NewDelegateInstruction(MinimumStakeLamports)
	if err != nil {
		t.Fatalf("NewDelegateInstruction() error = %v", err)
	}
	delegateBytes, err := delegateInstruction.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary(delegate) error = %v", err)
	}
	decodedDelegate, err := UnmarshalInstructionBinary(delegateBytes)
	if err != nil {
		t.Fatalf("UnmarshalInstructionBinary(delegate) error = %v", err)
	}
	if decodedDelegate.Type != InstructionDelegate || decodedDelegate.Amount != MinimumStakeLamports {
		t.Fatalf("decoded delegate = %+v", decodedDelegate)
	}

	undelegateInstruction, err := NewUndelegateInstruction(MinimumStakeLamports, 9)
	if err != nil {
		t.Fatalf("NewUndelegateInstruction() error = %v", err)
	}
	undelegateBytes, err := undelegateInstruction.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary(undelegate) error = %v", err)
	}
	decodedUndelegate, err := UnmarshalInstructionBinary(undelegateBytes)
	if err != nil {
		t.Fatalf("UnmarshalInstructionBinary(undelegate) error = %v", err)
	}
	if decodedUndelegate.Type != InstructionUndelegate || decodedUndelegate.UnlockEpoch != 9 {
		t.Fatalf("decoded undelegate = %+v", decodedUndelegate)
	}
}

func TestUpdateCommissionInstructionRoundTrip(t *testing.T) {
	instruction, err := NewUpdateCommissionInstruction(250)
	if err != nil {
		t.Fatalf("NewUpdateCommissionInstruction() error = %v", err)
	}
	encoded, err := instruction.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary(update commission) error = %v", err)
	}
	decoded, err := UnmarshalInstructionBinary(encoded)
	if err != nil {
		t.Fatalf("UnmarshalInstructionBinary(update commission) error = %v", err)
	}
	if decoded.Type != InstructionUpdateCommission || decoded.CommissionBps != 250 {
		t.Fatalf("decoded update commission = %+v", decoded)
	}
	if _, err := NewUpdateCommissionInstruction(10001); err == nil {
		t.Fatal("NewUpdateCommissionInstruction(10001) error = nil, want range rejection")
	}
}

func TestDelegationStateRoundTripAndMature(t *testing.T) {
	state := testValidatorState(t)
	state.ActiveStake = MinimumStakeLamports
	state.PendingStake = MinimumStakeLamports
	state.ActivationEpoch = 3
	state.RewardLamports = 33
	state.SelfRewardLamports = 10
	state.CommissionRewardLamports = 3
	state.Delegations = []DelegationState{{
		DelegatorAccount: testPublicKey(t, 9),
		PendingStake:     MinimumStakeLamports,
		ActivationEpoch:  3,
		RewardLamports:   20,
	}}

	encoded, err := state.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	decoded, err := UnmarshalValidatorStateBinary(encoded)
	if err != nil {
		t.Fatalf("UnmarshalValidatorStateBinary() error = %v", err)
	}
	if len(decoded.Delegations) != 1 {
		t.Fatalf("delegation count = %d, want 1", len(decoded.Delegations))
	}
	if decoded.SelfRewardLamports != 10 || decoded.CommissionRewardLamports != 3 || decoded.Delegations[0].RewardLamports != 20 {
		t.Fatalf("reward breakdown = %+v, want self 10 commission 3 delegation 20", decoded)
	}
	if err := MatureStakeForEpoch(&decoded, 3); err != nil {
		t.Fatalf("MatureStakeForEpoch() error = %v", err)
	}
	if decoded.Delegations[0].ActiveStake != MinimumStakeLamports {
		t.Fatalf("delegation active = %d, want %d", decoded.Delegations[0].ActiveStake, MinimumStakeLamports)
	}
	selfActiveStake, err := SelfActiveStake(decoded)
	if err != nil {
		t.Fatalf("SelfActiveStake() error = %v", err)
	}
	if selfActiveStake != MinimumStakeLamports {
		t.Fatalf("self active = %d, want %d", selfActiveStake, MinimumStakeLamports)
	}
}

func TestValidateRejectsRewardBreakdownOverflow(t *testing.T) {
	state := testValidatorState(t)
	state.RewardLamports = 9
	state.SelfRewardLamports = 10
	if err := state.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want reward breakdown rejection")
	}
}

func TestSelfStakeBucketsExcludeDelegationBuckets(t *testing.T) {
	state := testValidatorState(t)
	state.ActiveStake = 3 * MinimumStakeLamports
	state.PendingStake = 2 * MinimumStakeLamports
	state.UnlockingStake = 2 * MinimumStakeLamports
	state.Delegations = []DelegationState{{
		DelegatorAccount: testPublicKey(t, 9),
		ActiveStake:      MinimumStakeLamports,
		PendingStake:     MinimumStakeLamports,
		UnlockingStake:   MinimumStakeLamports,
	}}

	selfActiveStake, err := SelfActiveStake(state)
	if err != nil {
		t.Fatalf("SelfActiveStake() error = %v", err)
	}
	selfPendingStake, err := SelfPendingStake(state)
	if err != nil {
		t.Fatalf("SelfPendingStake() error = %v", err)
	}
	selfUnlockingStake, err := SelfUnlockingStake(state)
	if err != nil {
		t.Fatalf("SelfUnlockingStake() error = %v", err)
	}
	if selfActiveStake != 2*MinimumStakeLamports || selfPendingStake != MinimumStakeLamports || selfUnlockingStake != MinimumStakeLamports {
		t.Fatalf("self buckets active=%d pending=%d unlocking=%d, want 20000000/10000000/10000000", selfActiveStake, selfPendingStake, selfUnlockingStake)
	}
}

func TestApplySlashKeepsDelegationBucketsConsistent(t *testing.T) {
	state := testValidatorState(t)
	state.ActiveStake = 3 * MinimumStakeLamports
	state.LastEffectiveStake = state.ActiveStake
	state.Delegations = []DelegationState{{
		DelegatorAccount: testPublicKey(t, 9),
		ActiveStake:      MinimumStakeLamports,
	}}

	slashed, err := ApplySlash(state, MinimumStakeLamports)
	if err != nil {
		t.Fatalf("ApplySlash() error = %v", err)
	}
	if len(slashed.Delegations) != 0 {
		t.Fatalf("delegation count = %d, want 0", len(slashed.Delegations))
	}
	selfActiveStake, err := SelfActiveStake(slashed)
	if err != nil {
		t.Fatalf("SelfActiveStake() error = %v", err)
	}
	if selfActiveStake != 2*MinimumStakeLamports {
		t.Fatalf("self active = %d, want %d", selfActiveStake, 2*MinimumStakeLamports)
	}
	if err := slashed.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestApplySlashPreservesDelegationBucketLimits(t *testing.T) {
	state := testValidatorState(t)
	state.ActiveStake = 50 * MinimumStakeLamports
	state.UnlockingStake = 20 * MinimumStakeLamports
	state.LastEffectiveStake = state.ActiveStake
	state.Delegations = []DelegationState{{
		DelegatorAccount: testPublicKey(t, 9),
		ActiveStake:      40 * MinimumStakeLamports,
		UnlockingStake:   10 * MinimumStakeLamports,
	}}

	slashed, err := ApplySlash(state, 30*MinimumStakeLamports)
	if err != nil {
		t.Fatalf("ApplySlash() error = %v", err)
	}
	if slashed.ActiveStake != 20*MinimumStakeLamports {
		t.Fatalf("active stake = %d, want %d", slashed.ActiveStake, 20*MinimumStakeLamports)
	}
	if slashed.Delegations[0].ActiveStake != 10*MinimumStakeLamports {
		t.Fatalf("delegation active = %d, want %d", slashed.Delegations[0].ActiveStake, 10*MinimumStakeLamports)
	}
	if _, err := SelfActiveStake(slashed); err != nil {
		t.Fatalf("SelfActiveStake() error = %v", err)
	}
}

func TestNormalizeDelegationBucketsRepairsExcessDelegatedActive(t *testing.T) {
	state := testValidatorState(t)
	state.ActiveStake = 12 * MinimumStakeLamports
	state.UnlockingStake = 20 * MinimumStakeLamports
	state.LastEffectiveStake = state.ActiveStake
	state.Delegations = []DelegationState{{
		DelegatorAccount: testPublicKey(t, 9),
		ActiveStake:      30 * MinimumStakeLamports,
		UnlockingStake:   2 * MinimumStakeLamports,
	}}

	normalized, changed, err := NormalizeDelegationBuckets(state)
	if err != nil {
		t.Fatalf("NormalizeDelegationBuckets() error = %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want repair")
	}
	if normalized.Delegations[0].ActiveStake != normalized.ActiveStake {
		t.Fatalf("delegation active = %d, want %d", normalized.Delegations[0].ActiveStake, normalized.ActiveStake)
	}
	if _, err := SelfActiveStake(normalized); err != nil {
		t.Fatalf("SelfActiveStake() error = %v", err)
	}
}

func TestEffectiveStakeActivatesAtEpochBoundary(t *testing.T) {
	state := testValidatorState(t)
	state.ActiveStake = 0
	state.PendingStake = MinimumStakeLamports
	state.ActivationEpoch = 3

	before, err := EffectiveStakeAtEpoch(state, 2)
	if err != nil {
		t.Fatalf("EffectiveStakeAtEpoch(before) error = %v", err)
	}
	if before != 0 {
		t.Fatalf("before activation stake = %d, want 0", before)
	}

	after, err := EffectiveStakeAtEpoch(state, 3)
	if err != nil {
		t.Fatalf("EffectiveStakeAtEpoch(after) error = %v", err)
	}
	if after != MinimumStakeLamports {
		t.Fatalf("after activation stake = %d, want %d", after, MinimumStakeLamports)
	}
}

func TestMatureStakeMovesPendingToActive(t *testing.T) {
	state := testValidatorState(t)
	state.ActiveStake = MinimumStakeLamports
	state.PendingStake = MinimumStakeLamports
	state.ActivationEpoch = 5

	if err := MatureStakeForEpoch(&state, 5); err != nil {
		t.Fatalf("MatureStakeForEpoch() error = %v", err)
	}
	if state.PendingStake != 0 {
		t.Fatalf("pending stake = %d, want 0", state.PendingStake)
	}
	if state.ActiveStake != 2*MinimumStakeLamports {
		t.Fatalf("active stake = %d, want %d", state.ActiveStake, 2*MinimumStakeLamports)
	}
	if state.LastEffectiveStake != 2*MinimumStakeLamports {
		t.Fatalf("effective stake = %d, want %d", state.LastEffectiveStake, 2*MinimumStakeLamports)
	}
}

func TestEffectiveStakeKeepsUnlockingUntilDeactivationEpoch(t *testing.T) {
	state := testValidatorState(t)
	state.ActiveStake = MinimumStakeLamports
	state.UnlockingStake = MinimumStakeLamports
	state.DeactivationEpoch = 4

	currentEpochStake, err := EffectiveStakeAtEpoch(state, 3)
	if err != nil {
		t.Fatalf("EffectiveStakeAtEpoch(current) error = %v", err)
	}
	if currentEpochStake != 2*MinimumStakeLamports {
		t.Fatalf("current epoch stake = %d, want %d", currentEpochStake, 2*MinimumStakeLamports)
	}

	nextEpochStake, err := EffectiveStakeAtEpoch(state, 4)
	if err != nil {
		t.Fatalf("EffectiveStakeAtEpoch(next) error = %v", err)
	}
	if nextEpochStake != MinimumStakeLamports {
		t.Fatalf("next epoch stake = %d, want %d", nextEpochStake, MinimumStakeLamports)
	}
}

func TestEffectiveStakeRecomputesAfterStakeReduction(t *testing.T) {
	state := testValidatorState(t)
	state.ActiveStake = 2*MinimumStakeLamports - 1
	state.LastEffectiveStake = 2 * MinimumStakeLamports

	effectiveStake, err := EffectiveStakeAtEpoch(state, 1)
	if err != nil {
		t.Fatalf("EffectiveStakeAtEpoch() error = %v", err)
	}
	wantEffectiveStake := 2*MinimumStakeLamports - 1
	if effectiveStake != wantEffectiveStake {
		t.Fatalf("effective stake = %d, want %d", effectiveStake, wantEffectiveStake)
	}

	state.LastEffectiveStake = effectiveStake
	if err := state.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func testValidatorState(t *testing.T) ValidatorState {
	t.Helper()
	return ValidatorState{
		ConsensusPublicKey: testPublicKey(t, 1),
		StakerAccount:      testPublicKey(t, 2),
		P2PPeerID:          "peer-test",
		ActiveStake:        MinimumStakeLamports,
		Status:             ValidatorStatusActive,
	}
}

func testPublicKey(t *testing.T, seed byte) structure.PublicKey {
	t.Helper()
	value := make([]byte, structure.PublicKeySize)
	for index := range value {
		value[index] = seed + byte(index)
	}
	publicKey, err := structure.NewPublicKey(value)
	if err != nil {
		t.Fatalf("NewPublicKey() error = %v", err)
	}
	return publicKey
}
