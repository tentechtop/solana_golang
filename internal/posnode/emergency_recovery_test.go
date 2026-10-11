package posnode

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/tentechtop/ordexus-core/consensus"
	"github.com/tentechtop/ordexus-core/database"
	"github.com/tentechtop/ordexus-core/structure"
)

func TestRecoveryStakeSignaturesRequireStrictTwoThirds(t *testing.T) {
	keys, set := recoveryCommittee(t, []uint64{40, 35, 25})
	message := []byte("recovery-certificate")
	if err := verifyRecoveryStakeSignatures(message, signRecoveryMessage(t, message, keys[:2]), set); err != nil {
		t.Fatalf("verifyRecoveryStakeSignatures(75/100) error = %v", err)
	}
	if err := verifyRecoveryStakeSignatures(message, signRecoveryMessage(t, message, []structure.OrdexusKeyPair{keys[0], keys[2]}), set); err == nil {
		t.Fatal("verifyRecoveryStakeSignatures(65/100) error = nil")
	}
	duplicate := signRecoveryMessage(t, message, []structure.OrdexusKeyPair{keys[0], keys[0]})
	if err := verifyRecoveryStakeSignatures(message, duplicate, set); err == nil {
		t.Fatal("duplicate recovery signature error = nil")
	}
}

func TestEmergencyRecoveryCertificateSignBytesCanonicalizeExclusions(t *testing.T) {
	certificate := consensus.EmergencyRecoveryCertificate{
		Version:                consensus.EmergencyRecoveryCertificateVersion,
		ChainID:                "recovery-test",
		Sequence:               1,
		FinalizedHeight:        7,
		FinalizedHash:          recoveryTestHash(t, "certificate-finalized").String(),
		SafetyEpochID:          2,
		SafetyValidatorSetRoot: recoveryTestHash(t, "certificate-validator-root").String(),
		ElderValidatorID:       "elder-a",
		ExcludedValidatorIDs:   []string{"validator-b", "validator-a"},
		ExpiresAtUnixMilli:     time.Now().Add(time.Hour).UnixMilli(),
	}
	first, err := certificate.SignBytes()
	if err != nil {
		t.Fatalf("SignBytes() error = %v", err)
	}
	certificate.ExcludedValidatorIDs = []string{"validator-a", "validator-b"}
	certificate.Signatures = []consensus.EmergencyRecoverySignature{{ValidatorID: "ignored", Signature: "ignored"}}
	second, err := certificate.SignBytes()
	if err != nil {
		t.Fatalf("SignBytes(reordered) error = %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("sign bytes changed after exclusion or signature order changed")
	}
}

func TestRecoveryElderIsProvisionalUntilCommitteeActivatesMultiValidatorMode(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{Path: t.TempDir(), Engine: database.EnginePebble, WAL: true})
	if err != nil {
		t.Fatalf("NewDatabase() error = %v", err)
	}
	defer db.Close()
	node := newConsensusStatusTestNodeWithDatabase(t, db)
	node.config.EmergencyRecovery.Enabled = true
	node.config.ConsensusPolicy.MultiValidatorThreshold = 3
	node.voteCollectors = make(map[uint64]*consensus.VoteCollector)
	node.epochSnapshots = make(map[uint64]consensus.EpochSnapshot)
	node.leaderSchedules = make(map[uint64]consensus.LeaderSchedule)

	safetySet, err := node.ledger.ValidatorSetFromFinalizedStateAtEpoch(0)
	if err != nil {
		t.Fatalf("ValidatorSetFromFinalizedStateAtEpoch() error = %v", err)
	}
	safetyRoot, err := consensus.ValidatorSetRoot(safetySet)
	if err != nil {
		t.Fatalf("ValidatorSetRoot() error = %v", err)
	}
	head := node.ledger.Head()
	node.consensusMode = consensus.NetworkModeMultiValidator
	node.multiValidatorActivated = true
	node.safetyCheckpoint = consensusSafetyCheckpoint{
		FinalizedHeight:  head.FinalizedHeight,
		FinalizedHash:    head.FinalizedHash.String(),
		EpochID:          0,
		ValidatorSetRoot: safetyRoot.String(),
	}
	elder, err := consensus.SelectElder(safetySet, head.FinalizedHash, nil)
	if err != nil {
		t.Fatalf("SelectElder() error = %v", err)
	}
	committeeKeys := []structure.OrdexusKeyPair{
		mustStructureKeyPair("consensus-status-consensus-a"),
		mustStructureKeyPair("consensus-status-consensus-b"),
		mustStructureKeyPair("consensus-status-consensus-c"),
	}
	recoveryCertificate := consensus.EmergencyRecoveryCertificate{
		Version:                consensus.EmergencyRecoveryCertificateVersion,
		ChainID:                node.config.ChainID,
		Sequence:               1,
		FinalizedHeight:        head.FinalizedHeight,
		FinalizedHash:          head.FinalizedHash.String(),
		SafetyEpochID:          0,
		SafetyValidatorSetRoot: safetyRoot.String(),
		ElderValidatorID:       string(elder.ValidatorID),
		ExpiresAtUnixMilli:     time.Now().Add(time.Minute).UnixMilli(),
	}
	recoverySignBytes, err := recoveryCertificate.SignBytes()
	if err != nil {
		t.Fatalf("recovery SignBytes() error = %v", err)
	}
	recoveryCertificate.Signatures = signRecoveryMessage(t, recoverySignBytes, committeeKeys)
	encodedRecovery := encodeRecoveryCertificate(t, recoveryCertificate)
	selectedID, err := node.SubmitEmergencyRecovery(context.Background(), encodedRecovery)
	if err != nil {
		t.Fatalf("SubmitEmergencyRecovery() error = %v", err)
	}
	if selectedID != string(elder.ValidatorID) || node.consensusMode != consensus.NetworkModeRecoveryElder {
		t.Fatalf("recovery mode=%q elder=%q", node.consensusMode, selectedID)
	}
	if !node.ledger.FinalitySuspended() || len(node.epochSnapshot.Validators) != 1 {
		t.Fatalf("recovery finality_suspended=%t validators=%d", node.ledger.FinalitySuspended(), len(node.epochSnapshot.Validators))
	}

	targetSet, err := node.ledger.ValidatorSetFromStateAtEpoch(0)
	if err != nil {
		t.Fatalf("ValidatorSetFromStateAtEpoch() error = %v", err)
	}
	targetRoot, err := consensus.ValidatorSetRoot(targetSet)
	if err != nil {
		t.Fatalf("target ValidatorSetRoot() error = %v", err)
	}
	activationCertificate := consensus.MultiValidatorActivationCertificate{
		Version:                consensus.MultiValidatorActivationCertificateVersion,
		ChainID:                node.config.ChainID,
		Sequence:               2,
		SafetyFinalizedHeight:  head.FinalizedHeight,
		SafetyFinalizedHash:    head.FinalizedHash.String(),
		SafetyEpochID:          0,
		SafetyValidatorSetRoot: safetyRoot.String(),
		TargetHeight:           head.Height,
		TargetHash:             head.BlockHash.String(),
		TargetEpochID:          0,
		TargetValidatorSetRoot: targetRoot.String(),
		ExpiresAtUnixMilli:     time.Now().Add(time.Minute).UnixMilli(),
	}
	activationSignBytes, err := activationCertificate.SignBytes()
	if err != nil {
		t.Fatalf("activation SignBytes() error = %v", err)
	}
	activationCertificate.Signatures = signRecoveryMessage(t, activationSignBytes, committeeKeys)
	encodedActivation := encodeRecoveryCertificate(t, activationCertificate)
	if _, err := node.SubmitMultiValidatorActivation(context.Background(), encodedActivation); err != nil {
		t.Fatalf("SubmitMultiValidatorActivation() error = %v", err)
	}
	if node.consensusMode != consensus.NetworkModeMultiValidator || node.ledger.FinalitySuspended() {
		t.Fatalf("activation mode=%q finality_suspended=%t", node.consensusMode, node.ledger.FinalitySuspended())
	}
	if len(node.epochSnapshot.Validators) != 3 {
		t.Fatalf("activation validators=%d, want 3", len(node.epochSnapshot.Validators))
	}
}

func recoveryCommittee(t *testing.T, stakes []uint64) ([]structure.OrdexusKeyPair, consensus.ValidatorSet) {
	t.Helper()
	keys := make([]structure.OrdexusKeyPair, len(stakes))
	validators := make([]consensus.ValidatorState, len(stakes))
	for index, stake := range stakes {
		keys[index] = recoveryTestKeyPair(t, "committee-"+string(rune('a'+index)))
		validators[index] = consensus.ValidatorState{
			ConsensusPublicKey: keys[index].PublicKey,
			StakeLamports:      stake,
			Status:             consensus.ValidatorStatusActive,
		}
	}
	set, err := consensus.NewValidatorSet(validators)
	if err != nil {
		t.Fatalf("NewValidatorSet() error = %v", err)
	}
	return keys, set
}

func signRecoveryMessage(t *testing.T, message []byte, keys []structure.OrdexusKeyPair) []consensus.EmergencyRecoverySignature {
	t.Helper()
	result := make([]consensus.EmergencyRecoverySignature, 0, len(keys))
	for _, key := range keys {
		signature, err := structure.SignMessage(key.PrivateKey, message)
		if err != nil {
			t.Fatalf("SignMessage() error = %v", err)
		}
		result = append(result, consensus.EmergencyRecoverySignature{
			ValidatorID: string(consensus.NewValidatorID(key.PublicKey)),
			PublicKey:   key.PublicKey.String(),
			Signature:   signature.String(),
		})
	}
	return result
}

func encodeRecoveryCertificate(t *testing.T, certificate any) string {
	t.Helper()
	data, err := json.Marshal(certificate)
	if err != nil {
		t.Fatalf("Marshal(certificate) error = %v", err)
	}
	return base64.StdEncoding.EncodeToString(data)
}

func recoveryTestKeyPair(t *testing.T, label string) structure.OrdexusKeyPair {
	t.Helper()
	return mustStructureKeyPair(label)
}

func recoveryTestHash(t *testing.T, label string) structure.Hash {
	t.Helper()
	return testHashFromText(t, label)
}
