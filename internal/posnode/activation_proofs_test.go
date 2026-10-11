package posnode

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/tentechtop/ordexus-core/blockchain"
	"github.com/tentechtop/ordexus-core/consensus"
	"github.com/tentechtop/ordexus-core/database"
	"github.com/tentechtop/ordexus-core/structure"
)

func TestActivationProofSurvivesEpochReconciliationAndRestart(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{Path: t.TempDir(), Engine: database.EnginePebble, WAL: true})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node := newConsensusStatusTestNodeWithDatabase(t, db)
	node.config.EmergencyRecovery.Enabled = true
	node.config.ChainIdentityHash = testHashFromText(t, "activation-proof-chain-identity").String()
	node.config.GenesisHash = node.ledger.Head().FinalizedHash.String()
	node.config.ConsensusPolicy.MultiValidatorThreshold = 3
	node.config.Genesis.InitialSupplyLamports = 1_000_000_000_000
	keys := make([]structure.OrdexusKeyPair, 3)
	for i, suffix := range []string{"a", "b", "c"} {
		keys[i] = mustStructureKeyPair("consensus-status-consensus-" + suffix)
		node.config.Genesis.InitialValidators = append(node.config.Genesis.InitialValidators, genesisValidatorConfig{
			StakerSeed: "consensus-status-staker-" + suffix, ValidatorSeed: "consensus-status-validator-" + suffix,
			ConsensusSeed:      "consensus-status-consensus-" + suffix,
			BLSPublicKeyBase64: "", PeerID: node.epochSnapshot.Validators[i].P2PPeerID, StakeLamports: 100000000,
		})
	}
	genesisSet, err := node.ledger.ValidatorSetFromFinalizedStateAtEpoch(0)
	if err != nil {
		t.Fatal(err)
	}
	genesis, err := consensus.NewEpochSnapshot(0, 1, 16, node.epochSeed(0), genesisSet)
	if err != nil {
		t.Fatal(err)
	}
	// Use the same BLS key material when rebuilding the configured trust root.
	for i := range node.config.Genesis.InitialValidators {
		for _, validator := range genesis.Validators {
			if validator.ConsensusPublicKey == keys[i].PublicKey {
				node.config.Genesis.InitialValidators[i].BLSPublicKeyBase64 = base64.StdEncoding.EncodeToString(validator.BLSPublicKey)
				node.config.Genesis.InitialValidators[i].PeerID = validator.P2PPeerID
				node.config.Genesis.InitialValidators[i].StakeLamports = validator.StakeLamports
			}
		}
	}
	node.epochSnapshot = genesis
	elderSet, err := consensus.SingleValidatorSet(genesis.Validators[0])
	if err != nil {
		t.Fatal(err)
	}
	oldSnapshot, err := consensus.NewEpochSnapshot(1, 17, 16, node.epochSeed(1), elderSet)
	if err != nil {
		t.Fatal(err)
	}
	if err := node.persistEpochTransitionProof(signedEpochTransitionProof(t, node, keys, genesis, oldSnapshot)); err != nil {
		t.Fatal(err)
	}
	head := node.ledger.Head()
	state := node.ledger.State()
	stateRoot, _ := state.RootHash()
	proposal := consensus.BlockProposal{Header: consensus.BlockHeader{ChainID: node.config.ChainID, Slot: 17, Height: head.Height + 1,
		EpochID: 1, ParentHash: head.BlockHash, StateRoot: stateRoot, AccountRoot: stateRoot, TimestampUnixMilli: time.Now().UnixMilli()}}
	if _, err := node.ledger.CommitBlock(blockchain.CommitBlockRequest{Proposal: proposal, NextState: state}); err != nil {
		t.Fatal(err)
	}
	head = node.ledger.Head()
	root, _ := consensus.ValidatorSetRoot(genesisSet)
	node.epochSnapshot = oldSnapshot
	node.consensusMode = consensus.NetworkModeRecoveryElder
	node.multiValidatorActivated = true
	node.safetyCheckpoint = consensusSafetyCheckpoint{FinalizedHeight: 0, FinalizedHash: head.FinalizedHash.String(), EpochID: 0, ValidatorSetRoot: root.String()}
	targetSet, err := node.ledger.ValidatorSetFromStateAtEpoch(1)
	if err != nil {
		t.Fatal(err)
	}
	targetRoot, _ := consensus.ValidatorSetRoot(targetSet)
	certificate := consensus.MultiValidatorActivationCertificate{Version: 1, ChainID: node.config.ChainID, Sequence: 1,
		SafetyFinalizedHeight: 0, SafetyFinalizedHash: head.FinalizedHash.String(), SafetyEpochID: 0, SafetyValidatorSetRoot: root.String(),
		TargetHeight: head.Height, TargetHash: head.BlockHash.String(), TargetEpochID: 1, TargetValidatorSetRoot: targetRoot.String(), ExpiresAtUnixMilli: time.Now().Add(time.Minute).UnixMilli()}
	message, _ := certificate.SignBytes()
	certificate.Signatures = signRecoveryMessage(t, message, keys)
	if _, err := node.SubmitMultiValidatorActivation(context.Background(), encodeRecoveryCertificate(t, certificate)); err != nil {
		t.Fatal(err)
	}
	if _, err := node.ensureEpochTransition(context.Background(), 18); err != nil {
		t.Fatal(err)
	}
	if node.consensusMode != consensus.NetworkModeMultiValidator || node.ledger.FinalitySuspended() {
		t.Fatal("activation reverted to recovery")
	}
	activated := node.epochSnapshot
	historical, err := node.authenticatedEpochSnapshotForSlot(17)
	if err != nil || historical.SnapshotStateRoot != oldSnapshot.SnapshotStateRoot {
		t.Fatalf("lost historical committee: %v", err)
	}
	current, err := node.authenticatedEpochSnapshotForSlot(18)
	if err != nil || current.SnapshotStateRoot != activated.SnapshotStateRoot {
		t.Fatalf("activation is not authenticated: %v", err)
	}
	// The next ordinary epoch proof must extend the recovered committee, not the elder.
	next, _ := consensus.NewEpochSnapshot(2, 33, 16, node.epochSeed(2), targetSet)
	if err := node.persistEpochTransitionProof(signedEpochTransitionProof(t, node, keys, activated, next)); err != nil {
		t.Fatal(err)
	}
	if _, err := node.authenticatedEpochSnapshot(2); err != nil {
		t.Fatalf("next epoch did not extend activation: %v", err)
	}
	if err := node.syncEpochProofChainFromPeer(context.Background(), "unused-peer", 2); err != nil {
		t.Fatalf("proof sync lost the activation between epochs: %v", err)
	}
	node.epochSnapshot = oldSnapshot
	if err := node.installAuthenticatedStartupEpoch(18); err != nil {
		t.Fatal(err)
	}
	if node.consensusMode != consensus.NetworkModeMultiValidator || len(node.epochSnapshot.Validators) != 3 {
		t.Fatal("restart lost activation")
	}
	// Expiry applies at admission. The accepted proof must remain usable later.
	certificate.ExpiresAtUnixMilli = time.Now().Add(-time.Hour).UnixMilli()
	message, _ = certificate.SignBytes()
	certificate.Signatures = signRecoveryMessage(t, message, keys)
	data, _ := json.Marshal(certificate)
	if err := node.persistActivationProof(certificate, data); err != nil {
		t.Fatal(err)
	}
	if _, err := node.authenticatedEpochSnapshot(1); err != nil {
		t.Fatalf("accepted historical proof expired: %v", err)
	}
	certificate.Signatures = certificate.Signatures[:1]
	data, _ = json.Marshal(certificate)
	if err := node.persistActivationProof(certificate, data); err != nil {
		t.Fatal(err)
	}
	if _, err := node.authenticatedEpochSnapshot(1); err == nil {
		t.Fatal("authenticated activation without a safety quorum")
	}
}
