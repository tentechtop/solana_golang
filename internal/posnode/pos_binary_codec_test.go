package posnode

import (
	"bytes"
	"testing"
	"time"

	"github.com/tentechtop/ordexus-core/consensus"
	"github.com/tentechtop/ordexus-core/p2p"
	"github.com/tentechtop/ordexus-core/structure"
)

func TestPOSBinaryTransactionEnvelopeRoundTrip(t *testing.T) {
	transaction := newMempoolTransfer(t, "binary-codec-source", "binary-codec-destination", 100)
	message, err := encodeTransactionRouteMessage(transaction, transactionRouteEnvelope{
		OriginPeerID: "origin-peer",
		HopCount:     1,
		MaxHops:      4,
	})
	if err != nil {
		t.Fatalf("encodeTransactionRouteMessage() error = %v", err)
	}
	if len(message.Payload) == 0 || message.Payload[0] == '{' {
		t.Fatalf("payload is not binary: %q", message.Payload)
	}
	decoded, route, err := decodeTransactionRouteMessage(message)
	if err != nil {
		t.Fatalf("decodeTransactionRouteMessage() error = %v", err)
	}
	expectedBytes, err := transaction.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary(expected) error = %v", err)
	}
	actualBytes, err := decoded.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary(actual) error = %v", err)
	}
	if !bytes.Equal(actualBytes, expectedBytes) {
		t.Fatal("decoded transaction bytes mismatch")
	}
	if route.OriginPeerID != "origin-peer" || route.HopCount != 1 || route.MaxHops != 4 {
		t.Fatalf("decoded route = %+v", route)
	}
}

func TestPOSBinarySyncResponseRoundTrip(t *testing.T) {
	node := newConsensusStatusTestNode(t)
	head := node.ledger.Head()
	stateRoot, err := node.ledger.State().RootHash()
	if err != nil {
		t.Fatalf("RootHash() error = %v", err)
	}
	proposal := consensus.BlockProposal{
		Header: consensus.BlockHeader{
			ChainID:        node.config.ChainID,
			Slot:           head.Slot + 1,
			Height:         head.Height + 1,
			ParentHash:     head.BlockHash,
			PreviousQCHash: head.QCHash,
			LeaderID:       consensus.NewValidatorID(node.consensusKeyPair.PublicKey),
			EpochID:        head.EpochID,
			StateRoot:      stateRoot,
			AccountRoot:    stateRoot,
		},
	}
	response := blockResponseEnvelope{
		Found:    true,
		Hash:     head.BlockHash.String(),
		Proposal: proposal,
	}
	payload, err := marshalBlockResponseBinary(p2p.ProtocolPoSBlockByHeightV1, response)
	if err != nil {
		t.Fatalf("marshalBlockResponseBinary() error = %v", err)
	}
	if len(payload) == 0 || payload[0] == '{' {
		t.Fatalf("payload is not binary: %q", payload)
	}
	decoded, err := unmarshalBlockResponseBinary(p2p.ProtocolPoSBlockByHeightV1, payload)
	if err != nil {
		t.Fatalf("unmarshalBlockResponseBinary() error = %v", err)
	}
	if !decoded.Found || decoded.Hash != response.Hash {
		t.Fatalf("decoded block response = %+v", decoded)
	}
	expectedHash, err := response.Proposal.Hash()
	if err != nil {
		t.Fatalf("expected proposal hash: %v", err)
	}
	actualHash, err := decoded.Proposal.Hash()
	if err != nil {
		t.Fatalf("actual proposal hash: %v", err)
	}
	if actualHash != expectedHash {
		t.Fatalf("proposal hash = %s, want %s", actualHash.String(), expectedHash.String())
	}
}

func TestPOSBinaryBlockQCResponseRoundTrip(t *testing.T) {
	certificate := consensus.QuorumCertificate{
		Type:               consensus.VoteTypeConfirm,
		Slot:               11,
		BlockHeight:        7,
		BlockHash:          mustHash("block-qc-response"),
		ThresholdStake:     67,
		ConfirmedStake:     70,
		Voters:             []string{"validator-a", "validator-b"},
		CreatedAtUnixMilli: 1710000000000,
	}
	payload, err := marshalBlockQCResponseBinary(blockQCResponseEnvelope{Found: true, QC: certificate})
	if err != nil {
		t.Fatalf("marshalBlockQCResponseBinary() error = %v", err)
	}
	decoded, err := unmarshalBlockQCResponseBinary(payload)
	if err != nil {
		t.Fatalf("unmarshalBlockQCResponseBinary() error = %v", err)
	}
	if !decoded.Found || decoded.QC.BlockHash != certificate.BlockHash || decoded.QC.ConfirmedStake != certificate.ConfirmedStake {
		t.Fatalf("decoded block qc response = %+v", decoded)
	}
}

func TestPOSBinaryStateSnapshotRoundTrip(t *testing.T) {
	node := newConsensusStatusTestNode(t)
	head := node.ledger.Head()
	encoded, err := encodeStateSnapshotResponse(head.BlockHash, node.ledger.State())
	if err != nil {
		t.Fatalf("encodeStateSnapshotResponse() error = %v", err)
	}
	encoded.ChainID = node.config.ChainID
	encoded.ChainIdentityHash = node.config.ChainIdentityHash
	encoded.GenesisHash = node.config.GenesisHash
	payload, err := marshalStateSnapshotResponseBinary(encoded)
	if err != nil {
		t.Fatalf("marshalStateSnapshotResponseBinary() error = %v", err)
	}
	decoded, err := unmarshalStateSnapshotResponseBinary(payload)
	if err != nil {
		t.Fatalf("unmarshalStateSnapshotResponseBinary() error = %v", err)
	}
	blockHash, state, err := decodeStateSnapshotResponse(decoded)
	if err != nil {
		t.Fatalf("decodeStateSnapshotResponse() error = %v", err)
	}
	if blockHash != head.BlockHash {
		t.Fatalf("block hash = %s, want %s", blockHash.String(), head.BlockHash.String())
	}
	if len(state.Accounts) != len(node.ledger.State().Accounts) {
		t.Fatalf("account count = %d, want %d", len(state.Accounts), len(node.ledger.State().Accounts))
	}
}

func TestPOSBinaryStatusResponseRoundTrip(t *testing.T) {
	node := newConsensusStatusTestNode(t)
	status := node.statusSnapshot()
	status.Metrics.BlocksProduced = 7
	status.Metrics.QCFormed = 5
	payload, err := marshalStatusResponseBinary(status)
	if err != nil {
		t.Fatalf("marshalStatusResponseBinary() error = %v", err)
	}
	if len(payload) == 0 || payload[0] == '{' {
		t.Fatalf("payload is not binary: %q", payload)
	}
	decoded, err := unmarshalStatusResponseBinary(payload)
	if err != nil {
		t.Fatalf("unmarshalStatusResponseBinary() error = %v", err)
	}
	if decoded.ChainIdentityHash != status.ChainIdentityHash {
		t.Fatalf("chain identity = %s, want %s", decoded.ChainIdentityHash, status.ChainIdentityHash)
	}
	if decoded.HeadHeight != status.HeadHeight || decoded.HeadHash != status.HeadHash {
		t.Fatalf("head = %d/%s, want %d/%s", decoded.HeadHeight, decoded.HeadHash, status.HeadHeight, status.HeadHash)
	}
	if decoded.Consensus.ValidatorCount != status.Consensus.ValidatorCount {
		t.Fatalf("validator count = %d, want %d", decoded.Consensus.ValidatorCount, status.Consensus.ValidatorCount)
	}
	if decoded.Metrics.BlocksProduced != 7 || decoded.Metrics.QCFormed != 5 {
		t.Fatalf("metrics = %+v", decoded.Metrics)
	}
}

func TestPOSBinaryRejectsLegacyJSONPayload(t *testing.T) {
	if _, err := unmarshalTransactionEnvelopeBinary([]byte(`{"transaction":"abc"}`)); err == nil {
		t.Fatal("unmarshalTransactionEnvelopeBinary(JSON) error = nil, want error")
	}
	if _, err := unmarshalBlockResponseBinary(p2p.ProtocolPoSBlockByHeightV1, []byte(`{"found":false}`)); err == nil {
		t.Fatal("unmarshalBlockResponseBinary(JSON) error = nil, want error")
	}
	if _, err := unmarshalEpochTransitionProofBinary([]byte(`{"certificate":{}}`)); err == nil {
		t.Fatal("unmarshalEpochTransitionProofBinary(JSON) error = nil, want error")
	}
	if _, err := unmarshalEmergencyRecoveryCertificateBinary([]byte(`{"version":2}`)); err == nil {
		t.Fatal("unmarshalEmergencyRecoveryCertificateBinary(JSON) error = nil, want error")
	}
}

func TestPOSBinaryRejectsOversizedBlockLocatorRequest(t *testing.T) {
	if _, err := marshalBlockLocatorRequestBinary(blockLocatorRequestEnvelope{MaxEntries: posMaxListEntries + 1}); err == nil {
		t.Fatal("marshalBlockLocatorRequestBinary(oversized) error = nil, want error")
	}
}

func TestPOSBinaryEpochTransitionProofRoundTrip(t *testing.T) {
	node, keys, previousSnapshot, targetSnapshot := newEpochTransitionTestFixture(t, nil)
	proof := signedEpochTransitionProof(t, node, keys[:3], previousSnapshot, targetSnapshot)
	payload, err := marshalEpochTransitionProofBinary(proof)
	if err != nil {
		t.Fatalf("marshalEpochTransitionProofBinary() error = %v", err)
	}
	if len(payload) == 0 || payload[0] == '{' {
		t.Fatalf("epoch transition payload is not binary: %q", payload)
	}
	decoded, err := unmarshalEpochTransitionProofBinary(payload)
	if err != nil {
		t.Fatalf("unmarshalEpochTransitionProofBinary() error = %v", err)
	}
	if decoded.Certificate.Transition != proof.Certificate.Transition {
		t.Fatalf("decoded transition = %+v, want %+v", decoded.Certificate.Transition, proof.Certificate.Transition)
	}
	if decoded.Snapshot.SnapshotStateRoot != targetSnapshot.SnapshotStateRoot ||
		len(decoded.Snapshot.Validators) != len(targetSnapshot.Validators) {
		t.Fatalf("decoded snapshot root=%s validators=%d", decoded.Snapshot.SnapshotStateRoot.String(), len(decoded.Snapshot.Validators))
	}
	if len(decoded.Certificate.Signatures) != 3 {
		t.Fatalf("decoded signature count = %d, want 3", len(decoded.Certificate.Signatures))
	}
}

func TestPOSBinaryEpochTransitionRejectsVersionProtocolAndTrailingBytes(t *testing.T) {
	node, keys, previousSnapshot, targetSnapshot := newEpochTransitionTestFixture(t, nil)
	proof := signedEpochTransitionProof(t, node, keys[:3], previousSnapshot, targetSnapshot)
	payload, err := marshalEpochTransitionProofBinary(proof)
	if err != nil {
		t.Fatalf("marshalEpochTransitionProofBinary() error = %v", err)
	}

	unknownVersion := append([]byte(nil), payload...)
	unknownVersion[4]++
	if _, err := unmarshalEpochTransitionProofBinary(unknownVersion); err == nil {
		t.Fatal("unmarshalEpochTransitionProofBinary(unknown version) error = nil, want error")
	}

	wrongProtocol := append([]byte(nil), payload...)
	wrongProtocol[6]++
	if _, err := unmarshalEpochTransitionProofBinary(wrongProtocol); err == nil {
		t.Fatal("unmarshalEpochTransitionProofBinary(wrong protocol) error = nil, want error")
	}

	withTrailingBytes := append(append([]byte(nil), payload...), 0)
	if _, err := unmarshalEpochTransitionProofBinary(withTrailingBytes); err == nil {
		t.Fatal("unmarshalEpochTransitionProofBinary(trailing byte) error = nil, want error")
	}
}

func TestPOSBinaryEpochProofRequestAndResponseRoundTrip(t *testing.T) {
	requestPayload, err := marshalEpochProofRequestBinary(epochProofRequest{FromEpochID: 7, ToEpochID: 8})
	if err != nil {
		t.Fatalf("marshalEpochProofRequestBinary() error = %v", err)
	}
	request, err := unmarshalEpochProofRequestBinary(requestPayload)
	if err != nil {
		t.Fatalf("unmarshalEpochProofRequestBinary() error = %v", err)
	}
	if request.FromEpochID != 7 || request.ToEpochID != 8 {
		t.Fatalf("decoded epoch proof request = %+v", request)
	}

	node, keys, previousSnapshot, targetSnapshot := newEpochTransitionTestFixture(t, nil)
	proof := signedEpochTransitionProof(t, node, keys[:3], previousSnapshot, targetSnapshot)
	responsePayload, err := marshalEpochProofResponseBinary(epochProofResponse{Found: true, Proof: proof})
	if err != nil {
		t.Fatalf("marshalEpochProofResponseBinary() error = %v", err)
	}
	response, err := unmarshalEpochProofResponseBinary(responsePayload)
	if err != nil {
		t.Fatalf("unmarshalEpochProofResponseBinary() error = %v", err)
	}
	if !response.Found || response.Proof.Snapshot.SnapshotStateRoot != targetSnapshot.SnapshotStateRoot {
		t.Fatalf("decoded epoch proof response = %+v", response)
	}
}

func TestPOSBinaryEpochProofBatchRoundTripAndRejectsGap(t *testing.T) {
	node, keys, previousSnapshot, _ := newEpochTransitionTestFixture(t, nil)
	proofs := epochTransitionProofChain(t, node, keys[:3], previousSnapshot, 3)
	requestPayload, err := marshalEpochProofBatchRequestBinary(epochProofBatchRequest{
		FromEpochID:    0,
		TargetEpochID:  3,
		MaxProofs:      2,
		MaxPayloadSize: defaultEpochProofBatchPayloadBytes,
	})
	if err != nil {
		t.Fatalf("marshalEpochProofBatchRequestBinary() error = %v", err)
	}
	request, err := unmarshalEpochProofBatchRequestBinary(requestPayload)
	if err != nil {
		t.Fatalf("unmarshalEpochProofBatchRequestBinary() error = %v", err)
	}
	if request.FromEpochID != 0 || request.TargetEpochID != 3 || request.MaxProofs != 2 {
		t.Fatalf("decoded epoch proof batch request = %+v", request)
	}

	responsePayload, err := marshalEpochProofBatchResponseBinary(epochProofBatchResponse{
		FromEpochID:   0,
		TargetEpochID: 3,
		Complete:      true,
		Proofs:        proofs,
	})
	if err != nil {
		t.Fatalf("marshalEpochProofBatchResponseBinary() error = %v", err)
	}
	if len(responsePayload) == 0 || responsePayload[0] == '{' {
		t.Fatalf("epoch proof batch payload is not binary: %q", responsePayload)
	}
	response, err := unmarshalEpochProofBatchResponseBinary(responsePayload)
	if err != nil {
		t.Fatalf("unmarshalEpochProofBatchResponseBinary() error = %v", err)
	}
	if !response.Complete || len(response.Proofs) != 3 || response.Proofs[2].Snapshot.EpochID != 3 {
		t.Fatalf("decoded epoch proof batch response = %+v", response)
	}

	if _, err := marshalEpochProofBatchResponseBinary(epochProofBatchResponse{
		FromEpochID:   0,
		TargetEpochID: 3,
		Complete:      true,
		Proofs:        []epochTransitionProof{proofs[0], proofs[2]},
	}); err == nil {
		t.Fatal("marshalEpochProofBatchResponseBinary(gap) error = nil, want error")
	}
	legacyJSON := []byte(`{"from_epoch_id":0,"target_epoch_id":3}`)
	if _, err := unmarshalEpochProofBatchRequestBinary(legacyJSON); err == nil {
		t.Fatal("unmarshalEpochProofBatchRequestBinary(JSON) error = nil, want binary rejection")
	}
}

func TestPOSBinaryRecoveryCertificatesRoundTrip(t *testing.T) {
	key := mustStructureKeyPair("binary-recovery-signer")
	recovery := consensus.EmergencyRecoveryCertificate{
		Version:                consensus.EmergencyRecoveryCertificateVersion,
		ChainID:                "binary-recovery-chain",
		Sequence:               3,
		FinalizedHeight:        17,
		FinalizedHash:          testHashFromText(t, "binary-recovery-finalized").String(),
		SafetyEpochID:          2,
		SafetyValidatorSetRoot: testHashFromText(t, "binary-recovery-safety-root").String(),
		ElderValidatorID:       string(consensus.NewValidatorID(key.PublicKey)),
		ExcludedValidatorIDs:   []string{"validator-b", "validator-a"},
		ExpiresAtUnixMilli:     time.Now().Add(time.Hour).UnixMilli(),
	}
	recovery.Signatures = []consensus.EmergencyRecoverySignature{signBinaryRecoveryCertificate(t, key, recovery)}
	recoveryPayload, err := marshalEmergencyRecoveryCertificateBinary(recovery)
	if err != nil {
		t.Fatalf("marshalEmergencyRecoveryCertificateBinary() error = %v", err)
	}
	decodedRecovery, err := unmarshalEmergencyRecoveryCertificateBinary(recoveryPayload)
	if err != nil {
		t.Fatalf("unmarshalEmergencyRecoveryCertificateBinary() error = %v", err)
	}
	if decodedRecovery.Sequence != recovery.Sequence || decodedRecovery.FinalizedHash != recovery.FinalizedHash || len(decodedRecovery.Signatures) != 1 {
		t.Fatalf("decoded recovery certificate = %+v", decodedRecovery)
	}

	activation := consensus.MultiValidatorActivationCertificate{
		Version:                consensus.MultiValidatorActivationCertificateVersion,
		ChainID:                recovery.ChainID,
		Sequence:               4,
		SafetyFinalizedHeight:  recovery.FinalizedHeight,
		SafetyFinalizedHash:    recovery.FinalizedHash,
		SafetyEpochID:          recovery.SafetyEpochID,
		SafetyValidatorSetRoot: recovery.SafetyValidatorSetRoot,
		TargetHeight:           21,
		TargetHash:             testHashFromText(t, "binary-activation-target").String(),
		TargetEpochID:          3,
		TargetValidatorSetRoot: testHashFromText(t, "binary-activation-target-root").String(),
		ExpiresAtUnixMilli:     recovery.ExpiresAtUnixMilli,
	}
	activation.Signatures = []consensus.EmergencyRecoverySignature{signBinaryActivationCertificate(t, key, activation)}
	activationPayload, err := marshalMultiValidatorActivationCertificateBinary(activation)
	if err != nil {
		t.Fatalf("marshalMultiValidatorActivationCertificateBinary() error = %v", err)
	}
	decodedActivation, err := unmarshalMultiValidatorActivationCertificateBinary(activationPayload)
	if err != nil {
		t.Fatalf("unmarshalMultiValidatorActivationCertificateBinary() error = %v", err)
	}
	if decodedActivation.Sequence != activation.Sequence || decodedActivation.TargetHash != activation.TargetHash || len(decodedActivation.Signatures) != 1 {
		t.Fatalf("decoded activation certificate = %+v", decodedActivation)
	}
}

func signBinaryRecoveryCertificate(t *testing.T, key structure.OrdexusKeyPair, certificate consensus.EmergencyRecoveryCertificate) consensus.EmergencyRecoverySignature {
	t.Helper()
	message, err := certificate.SignBytes()
	if err != nil {
		t.Fatalf("recovery SignBytes() error = %v", err)
	}
	return signBinaryCertificateMessage(t, key, message)
}

func signBinaryActivationCertificate(t *testing.T, key structure.OrdexusKeyPair, certificate consensus.MultiValidatorActivationCertificate) consensus.EmergencyRecoverySignature {
	t.Helper()
	message, err := certificate.SignBytes()
	if err != nil {
		t.Fatalf("activation SignBytes() error = %v", err)
	}
	return signBinaryCertificateMessage(t, key, message)
}

func signBinaryCertificateMessage(t *testing.T, key structure.OrdexusKeyPair, message []byte) consensus.EmergencyRecoverySignature {
	t.Helper()
	signature, err := key.Sign(message)
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	return consensus.EmergencyRecoverySignature{
		ValidatorID: string(consensus.NewValidatorID(key.PublicKey)),
		PublicKey:   key.PublicKey.String(),
		Signature:   signature.String(),
	}
}
