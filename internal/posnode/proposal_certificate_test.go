package posnode

import (
	"errors"
	"testing"

	"github.com/tentechtop/ordexus-core/blockchain"
	"github.com/tentechtop/ordexus-core/consensus"
	"github.com/tentechtop/ordexus-core/database"
)

func TestProposalParentRequiresConfirmQC(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{Path: t.TempDir(), Engine: database.EnginePebble, WAL: true})
	if err != nil {
		t.Fatalf("NewDatabase() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	node := newConsensusStatusTestNodeWithDatabase(t, db)
	head := node.ledger.Head()
	proposal := consensus.BlockProposal{Header: consensus.BlockHeader{
		Height:         head.Height + 1,
		ParentHash:     head.BlockHash,
		PreviousQCHash: head.QCHash,
	}}
	if err := node.verifyProposalParentCertificate(proposal); !errors.Is(err, errProposalParentQCMissing) {
		t.Fatalf("verifyProposalParentCertificate() error = %v, want missing parent QC", err)
	}

	qc := collectConsensusStatusQC(t, node.epochSnapshot, consensus.VoteTypeConfirm, head.Slot, head.Height, head.BlockHash)
	if _, err := node.ledger.SaveQC(qc); err != nil {
		t.Fatalf("SaveQC() error = %v", err)
	}
	proposal.Header.PreviousQCHash, err = blockchain.HashCanonicalQC(qc)
	if err != nil {
		t.Fatalf("HashCanonicalQC() error = %v", err)
	}
	if err := node.verifyProposalParentCertificate(proposal); err != nil {
		t.Fatalf("verifyProposalParentCertificate(certified) error = %v", err)
	}
}

func TestFirstProposalReferencesGenesisCertificate(t *testing.T) {
	node := &posNode{}
	genesisHash := testHashFromText(t, "proposal-certificate-genesis")
	proposal := consensus.BlockProposal{Header: consensus.BlockHeader{
		Height:         1,
		ParentHash:     genesisHash,
		PreviousQCHash: genesisHash,
	}}
	if err := node.verifyProposalParentCertificate(proposal); err != nil {
		t.Fatalf("verifyProposalParentCertificate(genesis) error = %v", err)
	}
	proposal.Header.PreviousQCHash = testHashFromText(t, "wrong-genesis-certificate")
	if err := node.verifyProposalParentCertificate(proposal); err == nil {
		t.Fatal("verifyProposalParentCertificate(wrong genesis) error = nil")
	}
}

func TestSaveSyncedProposalParentCertificateRepairsMissingIndex(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{Path: t.TempDir(), Engine: database.EnginePebble, WAL: true})
	if err != nil {
		t.Fatalf("NewDatabase() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	node := newConsensusStatusTestNodeWithDatabase(t, db)
	head := node.ledger.Head()
	parentQC := collectConsensusStatusQC(t, node.epochSnapshot, consensus.VoteTypeConfirm, head.Slot, head.Height, head.BlockHash)
	parentQCHash, err := blockchain.HashCanonicalQC(parentQC)
	if err != nil {
		t.Fatalf("HashCanonicalQC() error = %v", err)
	}
	proposal := consensus.BlockProposal{Header: consensus.BlockHeader{
		Height:         head.Height + 1,
		ParentHash:     head.BlockHash,
		PreviousQCHash: parentQCHash,
	}}

	if err := node.verifyProposalParentCertificate(proposal); !errors.Is(err, errProposalParentQCMissing) {
		t.Fatalf("verifyProposalParentCertificate() error = %v, want missing parent QC", err)
	}
	if err := node.saveSyncedProposalParentCertificate(proposal, parentQC); err != nil {
		t.Fatalf("saveSyncedProposalParentCertificate() error = %v", err)
	}
	if err := node.verifyProposalParentCertificate(proposal); err != nil {
		t.Fatalf("verifyProposalParentCertificate(repaired) error = %v", err)
	}
}
