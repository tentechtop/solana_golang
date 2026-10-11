package blockchain

import (
	"testing"
	"time"

	"github.com/tentechtop/ordexus-core/consensus"
	"github.com/tentechtop/ordexus-core/structure"
	"github.com/tentechtop/ordexus-core/utils"
)

func TestStoredProposalRoundTripPreservesTimeoutQC(t *testing.T) {
	parentHash, err := structure.NewHash(utils.SHA256([]byte("stored-timeout-parent")))
	if err != nil {
		t.Fatalf("NewHash() error = %v", err)
	}
	proposal := consensus.BlockProposal{
		Header: consensus.BlockHeader{ChainID: "timeout-store", Slot: 2, Height: 1, ParentHash: parentHash},
		TimeoutQC: &consensus.QuorumCertificate{
			Type:               consensus.VoteTypeSkip,
			Slot:               1,
			BlockHeight:        1,
			BlockHash:          parentHash,
			ThresholdStake:     67,
			ConfirmedStake:     67,
			Voters:             []string{"alice", "bob"},
			CreatedAtUnixMilli: time.Now().UnixMilli(),
		},
	}
	data, err := marshalProposal(proposal)
	if err != nil {
		t.Fatalf("marshalProposal() error = %v", err)
	}
	decoded, err := unmarshalProposal(data)
	if err != nil {
		t.Fatalf("unmarshalProposal() error = %v", err)
	}
	if decoded.TimeoutQC == nil || decoded.TimeoutQC.Slot != 1 || decoded.TimeoutQC.BlockHash != parentHash {
		t.Fatalf("decoded timeout qc = %+v", decoded.TimeoutQC)
	}
}
