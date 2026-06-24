package posnode

import (
	"bytes"
	"testing"

	"solana_golang/consensus"
	"solana_golang/p2p"
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
}

func TestPOSBinaryRejectsOversizedBlockLocatorRequest(t *testing.T) {
	if _, err := marshalBlockLocatorRequestBinary(blockLocatorRequestEnvelope{MaxEntries: posMaxListEntries + 1}); err == nil {
		t.Fatal("marshalBlockLocatorRequestBinary(oversized) error = nil, want error")
	}
}
