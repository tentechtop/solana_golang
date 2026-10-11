package posnode

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/tentechtop/ordexus-core/consensus"
	"github.com/tentechtop/ordexus-core/database"
	"github.com/tentechtop/ordexus-core/p2p"
	"github.com/tentechtop/ordexus-core/structure"
)

func TestEpochTransitionProofRequiresStrictPreviousEpochQuorum(t *testing.T) {
	node, keys, previousSnapshot, targetSnapshot := newEpochTransitionTestFixture(t, nil)
	proof := signedEpochTransitionProof(t, node, keys[:3], previousSnapshot, targetSnapshot)
	signedStake, requiredStake, err := node.verifyEpochTransitionProof(previousSnapshot, proof, true)
	if err != nil {
		t.Fatalf("verifyEpochTransitionProof(three signers) error = %v", err)
	}
	if signedStake < requiredStake {
		t.Fatalf("signed stake = %d, required = %d", signedStake, requiredStake)
	}

	proof = signedEpochTransitionProof(t, node, keys[:2], previousSnapshot, targetSnapshot)
	if _, _, err := node.verifyEpochTransitionProof(previousSnapshot, proof, true); err == nil {
		t.Fatal("verifyEpochTransitionProof(two signers) error = nil, want strict quorum rejection")
	}
}

func TestEpochTransitionProofRejectsTamperedValidatorSet(t *testing.T) {
	node, keys, previousSnapshot, targetSnapshot := newEpochTransitionTestFixture(t, nil)
	proof := signedEpochTransitionProof(t, node, keys[:3], previousSnapshot, targetSnapshot)
	proof.Snapshot.Validators[0].StakeLamports++
	if _, _, err := node.verifyEpochTransitionProof(previousSnapshot, proof, true); err == nil {
		t.Fatal("verifyEpochTransitionProof(tampered snapshot) error = nil")
	}
}

func TestAuthenticatedEpochChainRejectsMissingAndTamperedProof(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{Path: t.TempDir(), Engine: database.EnginePebble, WAL: true})
	if err != nil {
		t.Fatalf("NewDatabase() error = %v", err)
	}
	defer db.Close()
	node, keys, previousSnapshot, targetSnapshot := newEpochTransitionTestFixture(t, db)
	if _, err := node.authenticatedEpochSnapshot(1); err == nil {
		t.Fatal("authenticatedEpochSnapshot() error = nil without transition proof")
	}
	proof := signedEpochTransitionProof(t, node, keys[:3], previousSnapshot, targetSnapshot)
	if err := node.persistEpochTransitionProof(proof); err != nil {
		t.Fatalf("persistEpochTransitionProof() error = %v", err)
	}
	authenticated, err := node.authenticatedEpochSnapshot(1)
	if err != nil {
		t.Fatalf("authenticatedEpochSnapshot() error = %v", err)
	}
	if authenticated.SnapshotStateRoot != targetSnapshot.SnapshotStateRoot {
		t.Fatalf("authenticated root = %s, want %s", authenticated.SnapshotStateRoot.String(), targetSnapshot.SnapshotStateRoot.String())
	}

	stored, found, err := node.loadEpochTransitionProof(1)
	if err != nil || !found {
		t.Fatalf("loadEpochTransitionProof() = found %v error %v", found, err)
	}
	stored.Certificate.Signatures[0].Signature = structure.Signature{}.String()
	data, err := jsonMarshalPersistedEpochTransition(stored)
	if err != nil {
		t.Fatalf("marshal tampered proof: %v", err)
	}
	if err := db.Put(database.TableCheckpoint, epochTransitionStateKey(1), data); err != nil {
		t.Fatalf("Put(tampered proof) error = %v", err)
	}
	if _, err := node.authenticatedEpochSnapshot(1); err == nil {
		t.Fatal("authenticatedEpochSnapshot() error = nil for tampered signature")
	}
}

func TestValidatorReadinessDoesNotRequireArchiveCapability(t *testing.T) {
	node, _, previousSnapshot, _ := newEpochTransitionTestFixture(t, nil)
	node.config.ResolvedNodeCapabilities = 0
	if authenticated, err := node.epochSnapshotAuthenticated(previousSnapshot); err != nil || !authenticated {
		t.Fatalf("non-archive validator epoch authentication = %v, error %v", authenticated, err)
	}
}

func TestAuthenticatedStartupEpochStaysOnPreviousEpochUntilBoundaryProofExists(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{Path: t.TempDir(), Engine: database.EnginePebble, WAL: true})
	if err != nil {
		t.Fatalf("NewDatabase() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	node, keys, previousSnapshot, targetSnapshot := newEpochTransitionTestFixture(t, db)

	snapshot, err := node.authenticatedStartupEpochSnapshot(targetSnapshot.StartSlot)
	if err != nil {
		t.Fatalf("authenticatedStartupEpochSnapshot(without proof) error = %v", err)
	}
	if snapshot.EpochID != previousSnapshot.EpochID || snapshot.SnapshotStateRoot != previousSnapshot.SnapshotStateRoot {
		t.Fatalf("startup epoch = %d root %s, want previous epoch %d root %s", snapshot.EpochID, snapshot.SnapshotStateRoot, previousSnapshot.EpochID, previousSnapshot.SnapshotStateRoot)
	}

	proof := signedEpochTransitionProof(t, node, keys[:3], previousSnapshot, targetSnapshot)
	if err := node.persistEpochTransitionProof(proof); err != nil {
		t.Fatalf("persistEpochTransitionProof() error = %v", err)
	}
	snapshot, err = node.authenticatedStartupEpochSnapshot(targetSnapshot.StartSlot)
	if err != nil {
		t.Fatalf("authenticatedStartupEpochSnapshot(with proof) error = %v", err)
	}
	if snapshot.EpochID != targetSnapshot.EpochID || snapshot.SnapshotStateRoot != targetSnapshot.SnapshotStateRoot {
		t.Fatalf("startup epoch = %d root %s, want certified epoch %d root %s", snapshot.EpochID, snapshot.SnapshotStateRoot, targetSnapshot.EpochID, targetSnapshot.SnapshotStateRoot)
	}
}

func TestAuthenticatedStartupEpochRejectsPresentTamperedBoundaryProof(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{Path: t.TempDir(), Engine: database.EnginePebble, WAL: true})
	if err != nil {
		t.Fatalf("NewDatabase() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	node, keys, previousSnapshot, targetSnapshot := newEpochTransitionTestFixture(t, db)
	proof := signedEpochTransitionProof(t, node, keys[:3], previousSnapshot, targetSnapshot)
	proof.Certificate.Signatures[0].Signature = structure.Signature{}.String()
	data, err := jsonMarshalPersistedEpochTransition(proof)
	if err != nil {
		t.Fatalf("jsonMarshalPersistedEpochTransition() error = %v", err)
	}
	if err := db.Put(database.TableCheckpoint, epochTransitionStateKey(targetSnapshot.EpochID), data); err != nil {
		t.Fatalf("Put(tampered proof) error = %v", err)
	}
	if _, err := node.authenticatedStartupEpochSnapshot(targetSnapshot.StartSlot); err == nil {
		t.Fatal("authenticatedStartupEpochSnapshot(tampered proof) error = nil")
	}
}

func TestEpochProofBatchRequestPaginatesAndReportsMissingProof(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{Path: t.TempDir(), Engine: database.EnginePebble, WAL: true})
	if err != nil {
		t.Fatalf("NewDatabase() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	node, keys, previousSnapshot, _ := newEpochTransitionTestFixture(t, db)
	node.peerKeyPair, err = rawKeyPairFromSeedText("epoch-proof-batch-server")
	if err != nil {
		t.Fatalf("rawKeyPairFromSeedText(server) error = %v", err)
	}
	requester, err := rawKeyPairFromSeedText("epoch-proof-batch-requester")
	if err != nil {
		t.Fatalf("rawKeyPairFromSeedText(requester) error = %v", err)
	}
	proofs := epochTransitionProofChain(t, node, keys[:3], previousSnapshot, 3)
	for _, proof := range proofs {
		if err := node.persistEpochTransitionProof(proof); err != nil {
			t.Fatalf("persistEpochTransitionProof(%d) error = %v", proof.Snapshot.EpochID, err)
		}
	}

	response := requestEpochProofBatchFromHandler(t, node, requester.peerID, epochProofBatchRequest{
		FromEpochID:    0,
		TargetEpochID:  3,
		MaxProofs:      2,
		MaxPayloadSize: defaultEpochProofBatchPayloadBytes,
	})
	if response.Complete || len(response.Proofs) != 2 || response.Proofs[1].Snapshot.EpochID != 2 {
		t.Fatalf("first page = %+v", response)
	}
	firstProofSize, err := epochTransitionProofBinarySize(proofs[0])
	if err != nil {
		t.Fatalf("epochTransitionProofBinarySize() error = %v", err)
	}
	response = requestEpochProofBatchFromHandler(t, node, requester.peerID, epochProofBatchRequest{
		FromEpochID:    0,
		TargetEpochID:  3,
		MaxProofs:      3,
		MaxPayloadSize: uint32(epochProofBatchResponseFixedSize + firstProofSize),
	})
	if response.Complete || len(response.Proofs) != 1 || response.Proofs[0].Snapshot.EpochID != 1 {
		t.Fatalf("byte-limited page = %+v", response)
	}
	response = requestEpochProofBatchFromHandler(t, node, requester.peerID, epochProofBatchRequest{
		FromEpochID:    2,
		TargetEpochID:  3,
		MaxProofs:      2,
		MaxPayloadSize: defaultEpochProofBatchPayloadBytes,
	})
	if !response.Complete || len(response.Proofs) != 1 || response.Proofs[0].Snapshot.EpochID != 3 {
		t.Fatalf("second page = %+v", response)
	}
	response = requestEpochProofBatchFromHandler(t, node, requester.peerID, epochProofBatchRequest{
		FromEpochID:    3,
		TargetEpochID:  4,
		MaxProofs:      2,
		MaxPayloadSize: defaultEpochProofBatchPayloadBytes,
	})
	if response.Error == "" || len(response.Proofs) != 0 || response.Complete {
		t.Fatalf("missing proof response = %+v", response)
	}
}

func TestPersistEpochTransitionProofBatchIsAtomicAndStoresSnapshots(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{Path: t.TempDir(), Engine: database.EnginePebble, WAL: true})
	if err != nil {
		t.Fatalf("NewDatabase() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	node, keys, previousSnapshot, _ := newEpochTransitionTestFixture(t, db)
	proofs := epochTransitionProofChain(t, node, keys[:3], previousSnapshot, 3)
	if err := node.persistEpochTransitionProofBatch([]epochTransitionProof{proofs[0], proofs[0]}); err == nil {
		t.Fatal("persistEpochTransitionProofBatch(duplicate) error = nil, want error")
	}
	if _, found, err := node.loadEpochTransitionProof(1); err != nil || found {
		t.Fatalf("proof persisted after rejected batch: found=%v error=%v", found, err)
	}
	if err := node.persistEpochTransitionProofBatch(proofs); err != nil {
		t.Fatalf("persistEpochTransitionProofBatch() error = %v", err)
	}
	for _, proof := range proofs {
		epochID := proof.Snapshot.EpochID
		if _, found, err := node.loadEpochTransitionProof(epochID); err != nil || !found {
			t.Fatalf("loadEpochTransitionProof(%d) found=%v error=%v", epochID, found, err)
		}
		loaded, found, err := node.loadEpochSnapshot(epochID, proof.Snapshot.StartSlot, node.config.EpochSlots, node.epochSeed(epochID))
		if err != nil || !found || loaded.SnapshotStateRoot != proof.Snapshot.SnapshotStateRoot {
			t.Fatalf("loadEpochSnapshot(%d) found=%v error=%v", epochID, found, err)
		}
	}
}

func newEpochTransitionTestFixture(
	t testing.TB,
	db database.Database,
) (*posNode, []structure.OrdexusKeyPair, consensus.EpochSnapshot, consensus.EpochSnapshot) {
	t.Helper()
	keys := make([]structure.OrdexusKeyPair, 4)
	validators := make([]consensus.ValidatorState, 4)
	for index := range keys {
		keys[index] = mustStructureKeyPair(string(rune('a'+index)) + "-epoch-transition-consensus")
		accountKey := mustStructureKeyPair(string(rune('a'+index)) + "-epoch-transition-account")
		validators[index] = consensus.ValidatorState{
			ValidatorID:        consensus.NewValidatorID(keys[index].PublicKey),
			AccountAddress:     accountKey.PublicKey,
			ConsensusPublicKey: keys[index].PublicKey,
			P2PPeerID:          "epoch-transition-peer-" + string(rune('a'+index)),
			StakeLamports:      10,
			Status:             consensus.ValidatorStatusActive,
		}
	}
	validatorSet, err := consensus.NewValidatorSet(validators)
	if err != nil {
		t.Fatalf("NewValidatorSet() error = %v", err)
	}
	node := &posNode{
		config: nodeConfig{
			ChainID:           "epoch-transition-chain",
			ChainIdentityHash: testHashFromText(t, "epoch-transition-identity").String(),
			GenesisHash:       testHashFromText(t, "epoch-transition-genesis").String(),
			EpochSlots:        8,
		},
		logger:                    slog.New(slog.NewTextHandler(io.Discard, nil)),
		db:                        db,
		consensusKeyPair:          keys[0],
		epochTransitionCandidates: make(map[uint64]*epochTransitionCandidate),
		epochSnapshots:            make(map[uint64]consensus.EpochSnapshot),
		leaderSchedules:           make(map[uint64]consensus.LeaderSchedule),
	}
	previousSnapshot, err := consensus.NewEpochSnapshot(0, 1, node.config.EpochSlots, node.epochSeed(0), validatorSet)
	if err != nil {
		t.Fatalf("NewEpochSnapshot(previous) error = %v", err)
	}
	targetSnapshot, err := consensus.NewEpochSnapshot(1, 9, node.config.EpochSlots, node.epochSeed(1), validatorSet)
	if err != nil {
		t.Fatalf("NewEpochSnapshot(target) error = %v", err)
	}
	node.epochSnapshot = previousSnapshot
	return node, keys, previousSnapshot, targetSnapshot
}

func signedEpochTransitionProof(
	t testing.TB,
	node *posNode,
	keys []structure.OrdexusKeyPair,
	previousSnapshot consensus.EpochSnapshot,
	targetSnapshot consensus.EpochSnapshot,
) epochTransitionProof {
	t.Helper()
	transition := consensus.EpochTransition{
		ChainID:           node.config.ChainID,
		ChainIdentityHash: node.config.ChainIdentityHash,
		GenesisHash:       node.config.GenesisHash,
		FromEpochID:       previousSnapshot.EpochID,
		ToEpochID:         targetSnapshot.EpochID,
		SourceBlockHeight: 7,
		SourceBlockHash:   testHashFromText(t, "epoch-transition-source").String(),
		FromSnapshotRoot:  previousSnapshot.SnapshotStateRoot.String(),
		ToSnapshotRoot:    targetSnapshot.SnapshotStateRoot.String(),
	}
	signBytes, err := transition.SignBytes()
	if err != nil {
		t.Fatalf("SignBytes() error = %v", err)
	}
	signatures := make([]consensus.EpochTransitionSignature, 0, len(keys))
	for _, key := range keys {
		signature, err := key.Sign(signBytes)
		if err != nil {
			t.Fatalf("Sign() error = %v", err)
		}
		signatures = append(signatures, consensus.EpochTransitionSignature{
			ValidatorID: string(consensus.NewValidatorID(key.PublicKey)),
			PublicKey:   key.PublicKey.String(),
			Signature:   signature.String(),
		})
	}
	certificate, err := (consensus.EpochTransitionCertificate{Transition: transition, Signatures: signatures}).Canonical()
	if err != nil {
		t.Fatalf("Canonical() error = %v", err)
	}
	return epochTransitionProof{Certificate: certificate, Snapshot: targetSnapshot}
}

func epochTransitionProofChain(
	t testing.TB,
	node *posNode,
	keys []structure.OrdexusKeyPair,
	previousSnapshot consensus.EpochSnapshot,
	count int,
) []epochTransitionProof {
	t.Helper()
	proofs := make([]epochTransitionProof, 0, count)
	for index := 0; index < count; index++ {
		validatorSet, err := consensus.NewValidatorSet(previousSnapshot.Validators)
		if err != nil {
			t.Fatalf("NewValidatorSet(epoch %d) error = %v", previousSnapshot.EpochID+1, err)
		}
		targetEpochID := previousSnapshot.EpochID + 1
		targetSnapshot, err := consensus.NewEpochSnapshot(
			targetEpochID,
			previousSnapshot.EndSlot+1,
			node.config.EpochSlots,
			node.epochSeed(targetEpochID),
			validatorSet,
		)
		if err != nil {
			t.Fatalf("NewEpochSnapshot(epoch %d) error = %v", targetEpochID, err)
		}
		proof := signedEpochTransitionProof(t, node, keys, previousSnapshot, targetSnapshot)
		proofs = append(proofs, proof)
		previousSnapshot = targetSnapshot
	}
	return proofs
}

func requestEpochProofBatchFromHandler(
	t testing.TB,
	node *posNode,
	requesterPeerID string,
	request epochProofBatchRequest,
) epochProofBatchResponse {
	t.Helper()
	payload, err := marshalEpochProofBatchRequestBinary(request)
	if err != nil {
		t.Fatalf("marshalEpochProofBatchRequestBinary() error = %v", err)
	}
	message, err := p2p.NewRequestMessage(requesterPeerID, p2p.ProtocolPoSEpochProofBatchV1, payload)
	if err != nil {
		t.Fatalf("NewRequestMessage() error = %v", err)
	}
	responseMessage, err := node.handleEpochProofBatchRequest(context.Background(), message)
	if err != nil {
		t.Fatalf("handleEpochProofBatchRequest() error = %v", err)
	}
	response, err := unmarshalEpochProofBatchResponseBinary(responseMessage.Payload)
	if err != nil {
		t.Fatalf("unmarshalEpochProofBatchResponseBinary() error = %v", err)
	}
	return response
}

func jsonMarshalPersistedEpochTransition(proof epochTransitionProof) ([]byte, error) {
	return json.Marshal(persistedEpochTransition{Version: persistedEpochTransitionVersion, Proof: proof})
}
