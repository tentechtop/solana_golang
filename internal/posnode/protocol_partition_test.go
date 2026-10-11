package posnode

import (
	"testing"

	"github.com/tentechtop/ordexus-core/consensus"
	"github.com/tentechtop/ordexus-core/p2p"
)

func TestVoteMessagePartitionKeyUsesOriginalValidator(t *testing.T) {
	keyPair := mustStructureKeyPair("protocol-partition-vote")
	vote := consensus.Vote{
		Type:               consensus.VoteTypePrepare,
		Slot:               7,
		BlockHeight:        7,
		BlockHash:          mustHash("partition-vote"),
		VoterID:            "validator-a",
		Stake:              10,
		CreatedAtUnixMilli: 1,
	}
	envelope, err := newSignedVoteEnvelope(vote, keyPair, consensus.BLSKeyPair{})
	if err != nil {
		t.Fatalf("newSignedVoteEnvelope() error = %v", err)
	}
	message, err := encodeSignedVoteEnvelopeMessage(envelope)
	if err != nil {
		t.Fatalf("encodeSignedVoteEnvelopeMessage() error = %v", err)
	}
	message.FromPeerID = "relay-a"
	if key := voteMessagePartitionKey(message); key != vote.VoterID {
		t.Fatalf("partition key = %q, want %q", key, vote.VoterID)
	}
	message.FromPeerID = "relay-b"
	if key := voteMessagePartitionKey(message); key != vote.VoterID {
		t.Fatalf("relayed partition key = %q, want %q", key, vote.VoterID)
	}
}

func TestProtocolPartitionKeyRejectsMalformedPayload(t *testing.T) {
	message := p2p.Message{Payload: []byte("invalid")}
	if key := transactionMessagePartitionKey(message); key != "" {
		t.Fatalf("transaction partition key = %q, want empty", key)
	}
	if key := voteMessagePartitionKey(message); key != "" {
		t.Fatalf("vote partition key = %q, want empty", key)
	}
	if key := qcMessagePartitionKey(message); key != "" {
		t.Fatalf("qc partition key = %q, want empty", key)
	}
}
