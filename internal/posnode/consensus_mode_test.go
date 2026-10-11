package posnode

import (
	"testing"
	"time"

	"github.com/tentechtop/ordexus-core/consensus"
	"github.com/tentechtop/ordexus-core/database"
)

func TestConsensusModeBootstrapsThenUsesMultiValidatorAndSafeDegradation(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{Path: t.TempDir(), Engine: database.EnginePebble, WAL: true})
	if err != nil {
		t.Fatalf("NewDatabase() error = %v", err)
	}
	defer db.Close()
	node := newConsensusStatusTestNodeWithDatabase(t, db)
	node.config.Genesis.InitialValidators = []genesisValidatorConfig{{ConsensusPublicKey: node.consensusKeyPair.PublicKey.String()}}
	rawSet, err := node.ledger.ValidatorSetFromFinalizedStateAtEpoch(0)
	if err != nil {
		t.Fatalf("ValidatorSetFromFinalizedStateAtEpoch() error = %v", err)
	}
	anchor := node.ledger.Head().FinalizedHash

	node.config.ConsensusPolicy.MultiValidatorThreshold = 4
	bootstrapSet, err := node.effectiveValidatorSetLocked(rawSet, 0, anchor)
	if err != nil {
		t.Fatalf("effectiveValidatorSetLocked(bootstrap) error = %v", err)
	}
	if node.consensusMode != consensus.NetworkModeBootstrapElder || len(bootstrapSet.Validators()) != 1 || node.ledger.FinalitySuspended() {
		t.Fatalf("bootstrap mode=%q validators=%d suspended=%t", node.consensusMode, len(bootstrapSet.Validators()), node.ledger.FinalitySuspended())
	}
	founderValidatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	if bootstrapSet.Validators()[0].ValidatorID != founderValidatorID {
		t.Fatalf("bootstrap elder = %s, want founder %s", bootstrapSet.Validators()[0].ValidatorID, founderValidatorID)
	}

	node.config.ConsensusPolicy.MultiValidatorThreshold = 3
	pendingSet, err := node.effectiveValidatorSetLocked(rawSet, 0, anchor)
	if err != nil {
		t.Fatalf("effectiveValidatorSetLocked(pending activation) error = %v", err)
	}
	if node.consensusMode != consensus.NetworkModeBootstrapElder || len(pendingSet.Validators()) != 1 || node.multiValidatorActivationEpoch == 0 {
		t.Fatalf("pending mode=%q validators=%d activation_epoch=%d", node.consensusMode, len(pendingSet.Validators()), node.multiValidatorActivationEpoch)
	}
	futureSet, err := node.effectiveValidatorSetLocked(rawSet, node.multiValidatorActivationEpoch, anchor)
	if err != nil {
		t.Fatalf("effectiveValidatorSetLocked(future message) error = %v", err)
	}
	if node.consensusMode != consensus.NetworkModeBootstrapElder || len(futureSet.Validators()) != 1 || node.multiValidatorActivated {
		t.Fatalf("future message activated mode=%q validators=%d activated=%t", node.consensusMode, len(futureSet.Validators()), node.multiValidatorActivated)
	}
	node.multiValidatorActivated = true
	multiSet, err := node.effectiveValidatorSetLocked(rawSet, node.multiValidatorActivationEpoch, anchor)
	if err != nil {
		t.Fatalf("effectiveValidatorSetLocked(multi) error = %v", err)
	}
	if node.consensusMode != consensus.NetworkModeMultiValidator || len(multiSet.Validators()) != 3 || !node.multiValidatorActivated {
		t.Fatalf("multi mode=%q validators=%d activated=%t", node.consensusMode, len(multiSet.Validators()), node.multiValidatorActivated)
	}
	safetyBefore := node.safetyCheckpoint

	snapshot, err := consensus.NewEpochSnapshot(0, 1, 8, anchor, multiSet)
	if err != nil {
		t.Fatalf("NewEpochSnapshot() error = %v", err)
	}
	gate := buildLivenessGateFromSnapshot(
		snapshot,
		consensus.NewValidatorID(node.consensusKeyPair.PublicKey),
		nil,
		time.Now(),
		time.Second,
		true,
	)
	if gate.ProductionEnabled || node.consensusMode != consensus.NetworkModeMultiValidator {
		t.Fatalf("partition gate producing=%t changed mode=%q", gate.ProductionEnabled, node.consensusMode)
	}

	reducedValidators := make([]consensus.ValidatorState, 0, 2)
	for _, validator := range rawSet.Validators() {
		if validator.ValidatorID == founderValidatorID || len(reducedValidators) == 0 {
			reducedValidators = append(reducedValidators, validator)
		}
	}
	if len(reducedValidators) > 2 {
		reducedValidators = reducedValidators[:2]
	}
	reducedSet, err := consensus.NewValidatorSet(reducedValidators)
	if err != nil {
		t.Fatalf("NewValidatorSet(reduced) error = %v", err)
	}
	recoverySet, err := node.effectiveValidatorSetLocked(reducedSet, 1, anchor)
	if err != nil {
		t.Fatalf("effectiveValidatorSetLocked(reduced) error = %v", err)
	}
	if node.consensusMode != consensus.NetworkModeRecoveryElder || len(recoverySet.Validators()) != 1 || !node.ledger.FinalitySuspended() {
		t.Fatalf("recovery mode=%q validators=%d suspended=%t", node.consensusMode, len(recoverySet.Validators()), node.ledger.FinalitySuspended())
	}
	if recoverySet.Validators()[0].ValidatorID != founderValidatorID {
		t.Fatalf("recovery elder = %s, want founder %s", recoverySet.Validators()[0].ValidatorID, founderValidatorID)
	}
	if node.safetyCheckpoint != safetyBefore {
		t.Fatalf("safety checkpoint changed during degradation: %+v != %+v", node.safetyCheckpoint, safetyBefore)
	}
}

func TestInstallEpochSnapshotReconcilesRecoveryModeBeforeSingleValidatorFinality(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{Path: t.TempDir(), Engine: database.EnginePebble, WAL: true})
	if err != nil {
		t.Fatalf("NewDatabase() error = %v", err)
	}
	defer db.Close()

	node := newConsensusStatusTestNodeWithDatabase(t, db)
	node.config.ConsensusPolicy.MultiValidatorThreshold = 3
	node.consensusMode = consensus.NetworkModeMultiValidator
	node.multiValidatorActivated = true
	node.ledger.SetFinalitySuspended(false)

	rawSet, err := node.ledger.ValidatorSetFromFinalizedStateAtEpoch(1)
	if err != nil {
		t.Fatalf("ValidatorSetFromFinalizedStateAtEpoch() error = %v", err)
	}
	validators := rawSet.Validators()
	singleSet, err := consensus.NewValidatorSet(validators[:1])
	if err != nil {
		t.Fatalf("NewValidatorSet(single) error = %v", err)
	}
	snapshot, err := consensus.NewEpochSnapshot(1, 17, 16, testHashFromText(t, "authenticated-recovery-snapshot"), singleSet)
	if err != nil {
		t.Fatalf("NewEpochSnapshot() error = %v", err)
	}

	if err := node.installEpochSnapshotLocked(snapshot); err != nil {
		t.Fatalf("installEpochSnapshotLocked() error = %v", err)
	}
	if node.consensusMode != consensus.NetworkModeRecoveryElder {
		t.Fatalf("consensus mode = %q, want recovery_elder", node.consensusMode)
	}
	if !node.ledger.FinalitySuspended() {
		t.Fatal("single-validator authenticated snapshot did not suspend finality")
	}
	if node.recoveryElderValidatorID != validators[0].ValidatorID {
		t.Fatalf("recovery elder = %q, want %q", node.recoveryElderValidatorID, validators[0].ValidatorID)
	}

	node.consensusMode = consensus.NetworkModeMultiValidator
	node.ledger.SetFinalitySuspended(false)
	if err := node.loadConsensusModeState(); err != nil {
		t.Fatalf("loadConsensusModeState() error = %v", err)
	}
	if node.consensusMode != consensus.NetworkModeRecoveryElder || !node.ledger.FinalitySuspended() {
		t.Fatalf("persisted mode=%q suspended=%t, want recovery_elder and suspended", node.consensusMode, node.ledger.FinalitySuspended())
	}
}
