package main

import (
	"testing"

	"solana_golang/blockchain"
)

func TestPeerNeedsBlockSyncWhenPeerAhead(t *testing.T) {
	localHead := blockchain.Head{
		Height:          10,
		BlockHash:       testHashFromText(t, "local-head"),
		FinalizedHeight: 8,
	}
	status := statusResponseEnvelope{
		HeadHeight: 11,
		HeadHash:   testHashFromText(t, "peer-head").String(),
	}
	if !peerNeedsBlockSync(localHead, status) {
		t.Fatal("peerNeedsBlockSync() = false, want true")
	}
}

func TestPeerNeedsBlockSyncWhenSameHeightForkDiffers(t *testing.T) {
	localHead := blockchain.Head{
		Height:          10,
		BlockHash:       testHashFromText(t, "local-head"),
		FinalizedHeight: 8,
	}
	status := statusResponseEnvelope{
		HeadHeight: 10,
		HeadHash:   testHashFromText(t, "peer-head").String(),
	}
	if !peerNeedsBlockSync(localHead, status) {
		t.Fatal("peerNeedsBlockSync() = false, want true")
	}
}

func TestPeerNeedsBlockSyncSkipsMatchingHead(t *testing.T) {
	localHash := testHashFromText(t, "local-head")
	localHead := blockchain.Head{
		Height:          10,
		BlockHash:       localHash,
		FinalizedHeight: 8,
	}
	status := statusResponseEnvelope{
		HeadHeight: 10,
		HeadHash:   localHash.String(),
	}
	if peerNeedsBlockSync(localHead, status) {
		t.Fatal("peerNeedsBlockSync() = true, want false")
	}
}

func TestPeerNeedsBlockSyncWhenPeerFinalizedAhead(t *testing.T) {
	localHead := blockchain.Head{
		Height:          12,
		BlockHash:       testHashFromText(t, "local-head"),
		FinalizedHeight: 8,
	}
	status := statusResponseEnvelope{
		HeadHeight:      11,
		HeadHash:        testHashFromText(t, "peer-head").String(),
		FinalizedHeight: 9,
	}
	if !peerNeedsBlockSync(localHead, status) {
		t.Fatal("peerNeedsBlockSync() = false, want true when peer finalized height is ahead")
	}
}

func TestCalculateSyncStartHeightFromAncestorUsesNextHeight(t *testing.T) {
	startHeight := calculateSyncStartHeightFromAncestor(21)
	if startHeight != 22 {
		t.Fatalf("calculateSyncStartHeightFromAncestor() = %d, want 22", startHeight)
	}
}

func TestCalculateSyncStartHeightFromAncestorStartsFromFirstBlockAfterGenesis(t *testing.T) {
	startHeight := calculateSyncStartHeightFromAncestor(0)
	if startHeight != 1 {
		t.Fatalf("calculateSyncStartHeightFromAncestor() = %d, want 1", startHeight)
	}
}

func TestValidatePeerStatusChainIdentityAcceptsMatchingPeer(t *testing.T) {
	config, err := normalizeNodeConfig(minimalNodeConfigForValidation())
	if err != nil {
		t.Fatalf("normalizeNodeConfig() error = %v", err)
	}
	status := statusResponseEnvelope{
		ChainID:           config.ChainID,
		ChainIdentityHash: config.ChainIdentityHash,
		GenesisHash:       config.GenesisHash,
	}
	if err := validatePeerStatusChainIdentity(config, "peer-a", status); err != nil {
		t.Fatalf("validatePeerStatusChainIdentity() error = %v", err)
	}
}

func TestValidatePeerStatusChainIdentityRejectsMismatch(t *testing.T) {
	config, err := normalizeNodeConfig(minimalNodeConfigForValidation())
	if err != nil {
		t.Fatalf("normalizeNodeConfig() error = %v", err)
	}
	status := statusResponseEnvelope{
		ChainID:           config.ChainID,
		ChainIdentityHash: testHashFromText(t, "other-chain").String(),
		GenesisHash:       config.GenesisHash,
	}
	if err := validatePeerStatusChainIdentity(config, "peer-b", status); err == nil {
		t.Fatal("validatePeerStatusChainIdentity() error = nil, want chain identity mismatch")
	}
}
