package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"solana_golang/consensus"
	"solana_golang/p2p"
	"solana_golang/structure"
)

func TestFaultInjectionMissingParentStoresOrphanProposal(t *testing.T) {
	node := newConsensusStatusTestNode(t)
	node.orphanProposals = make(map[structure.Hash][]consensus.BlockProposal)
	node.knownPeerIDs = nil

	slot := node.epochSnapshot.StartSlot
	leaderID, err := node.leaderSchedule.LeaderForSlot(slot)
	if err != nil {
		t.Fatalf("LeaderForSlot() error = %v", err)
	}
	stateRoot, err := node.ledger.State().RootHash()
	if err != nil {
		t.Fatalf("state root: %v", err)
	}
	proposal := consensus.BlockProposal{
		Header: consensus.BlockHeader{
			ChainID:            node.config.ChainID,
			Slot:               slot,
			Height:             node.ledger.Head().Height + 1,
			ParentHash:         testHashFromText(t, "missing-parent"),
			PreviousQCHash:     node.ledger.Head().QCHash,
			LeaderID:           leaderID,
			EpochID:            node.epochSnapshot.EpochID,
			StateRoot:          stateRoot,
			AccountRoot:        stateRoot,
			TimestampUnixMilli: time.Now().UnixMilli(),
		},
	}
	proposalHash, err := proposal.Hash()
	if err != nil {
		t.Fatalf("proposal hash: %v", err)
	}

	if err := node.applySyncedProposal(context.Background(), proposal, proposalHash); err != nil {
		t.Fatalf("applySyncedProposal() error = %v", err)
	}
	node.mutex.Lock()
	orphanCount := node.orphanProposalCountLocked()
	node.mutex.Unlock()
	if orphanCount != 1 {
		t.Fatalf("orphan count = %d, want 1", orphanCount)
	}
	if got := node.metrics.orphanStored.Load(); got != 1 {
		t.Fatalf("orphanStored = %d, want 1", got)
	}
	if got := node.metrics.syncFailures.Load(); got != 1 {
		t.Fatalf("syncFailures = %d, want 1", got)
	}
}

func TestFaultInjectionOrphanCapacityDropsOverflow(t *testing.T) {
	node := newConsensusStatusTestNode(t)
	parentHash := testHashFromText(t, "orphan-cap-parent")
	node.orphanProposals = map[structure.Hash][]consensus.BlockProposal{
		parentHash: make([]consensus.BlockProposal, maxOrphanProposals),
	}

	node.storeOrphanProposal(consensus.BlockProposal{
		Header: consensus.BlockHeader{
			Slot:       node.epochSnapshot.StartSlot,
			Height:     node.ledger.Head().Height + 1,
			ParentHash: parentHash,
		},
	})

	node.mutex.Lock()
	orphanCount := node.orphanProposalCountLocked()
	node.mutex.Unlock()
	if orphanCount != maxOrphanProposals {
		t.Fatalf("orphan count = %d, want cap %d", orphanCount, maxOrphanProposals)
	}
	if got := node.metrics.transactionsDrop.Load(); got != 1 {
		t.Fatalf("transactionsDrop = %d, want 1", got)
	}
	if got := node.metrics.orphanStored.Load(); got != 0 {
		t.Fatalf("orphanStored = %d, want 0", got)
	}
}

func TestFaultInjectionMissingBlockHeightReturnsNotFound(t *testing.T) {
	node := newConsensusStatusTestNode(t)
	requestPayload, err := json.Marshal(blockHeightRequestEnvelope{Height: node.ledger.Head().Height + 99})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	request, err := p2p.NewRequestMessage(mustRawKeyPair("fault-sync-requester").peerID, p2p.ProtocolPoSBlockByHeightV1, requestPayload)
	if err != nil {
		t.Fatalf("NewRequestMessage() error = %v", err)
	}

	response, err := node.handleBlockByHeightRequest(context.Background(), request)
	if err != nil {
		t.Fatalf("handleBlockByHeightRequest() error = %v", err)
	}
	if response.Type != p2p.ProtocolPoSBlockByHeightV1 || response.RequestID != request.ID {
		t.Fatalf("response routing = type %d request %s, want type %d request %s", response.Type, response.RequestID, p2p.ProtocolPoSBlockByHeightV1, request.ID)
	}
	envelope := blockResponseEnvelope{}
	if err := json.Unmarshal(response.Payload, &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Found || envelope.Hash != "" || envelope.Error != "" {
		t.Fatalf("response envelope = %+v, want not found without error", envelope)
	}
}
