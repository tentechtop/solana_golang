package posnode

import (
	"context"
	"errors"
	"testing"

	"solana_golang/blockchain"
	"solana_golang/consensus"
	"solana_golang/programs/stake"
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

func TestRequiresConnectedValidatorPeerForProduction(t *testing.T) {
	tests := []struct {
		name           string
		validatorCount int
		want           bool
	}{
		{name: "empty", validatorCount: 0, want: false},
		{name: "single", validatorCount: 1, want: false},
		{name: "two", validatorCount: 2, want: true},
		{name: "three", validatorCount: 3, want: true},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			node := &posNode{
				epochSnapshot: consensus.EpochSnapshot{
					Validators: make([]consensus.ValidatorState, testCase.validatorCount),
				},
			}
			if got := node.requiresConnectedValidatorPeerForProduction(); got != testCase.want {
				t.Fatalf("requiresConnectedValidatorPeerForProduction() = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestHasMinimumActiveValidatorCount(t *testing.T) {
	tests := []struct {
		name                 string
		activeValidatorCount int
		want                 bool
	}{
		{name: "zero", activeValidatorCount: 0, want: false},
		{name: "one", activeValidatorCount: 1, want: false},
		{name: "two", activeValidatorCount: 2, want: true},
		{name: "three", activeValidatorCount: 3, want: true},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			if got := hasMinimumActiveValidatorCount(testCase.activeValidatorCount); got != testCase.want {
				t.Fatalf("hasMinimumActiveValidatorCount() = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestSyncPeerIDsSnapshotIncludesBootstrapKnownAndValidators(t *testing.T) {
	node := &posNode{
		config: nodeConfig{
			BootstrapPeers: []peerConfig{
				{PeerID: "boot-peer"},
				{PeerID: "local-peer"},
				{},
			},
		},
		peerKeyPair:  rawKeyPair{peerID: "local-peer"},
		knownPeerIDs: []string{"known-peer", "boot-peer", "local-peer"},
		epochSnapshot: consensus.EpochSnapshot{
			Validators: []consensus.ValidatorState{
				{P2PPeerID: "validator-peer"},
				{P2PPeerID: "local-peer"},
				{P2PPeerID: "known-peer"},
			},
		},
	}

	got := node.syncPeerIDsSnapshot()
	want := []string{"boot-peer", "known-peer", "validator-peer"}
	assertStringSlicesEqual(t, got, want)
}

func TestHeadQCLagAcceptable(t *testing.T) {
	tests := []struct {
		name          string
		headHeight    uint64
		qcHeight      uint64
		qcExists      bool
		maxAllowedLag uint64
		want          bool
	}{
		{name: "genesis without qc", headHeight: 0, qcHeight: 0, qcExists: false, maxAllowedLag: 2, want: true},
		{name: "first optimistic block can pipeline", headHeight: 1, qcHeight: 0, qcExists: false, maxAllowedLag: 2, want: true},
		{name: "second optimistic block can pipeline", headHeight: 2, qcHeight: 0, qcExists: false, maxAllowedLag: 2, want: true},
		{name: "third optimistic block waits for qc", headHeight: 3, qcHeight: 0, qcExists: false, maxAllowedLag: 2, want: false},
		{name: "qc at head", headHeight: 12, qcHeight: 12, qcExists: true, maxAllowedLag: 2, want: true},
		{name: "qc one block behind can pipeline", headHeight: 12, qcHeight: 11, qcExists: true, maxAllowedLag: 2, want: true},
		{name: "qc two blocks behind can pipeline", headHeight: 12, qcHeight: 10, qcExists: true, maxAllowedLag: 2, want: true},
		{name: "qc beyond finality depth pauses", headHeight: 12, qcHeight: 9, qcExists: true, maxAllowedLag: 2, want: false},
		{name: "qc ahead is accepted", headHeight: 12, qcHeight: 13, qcExists: true, maxAllowedLag: 2, want: true},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got := headQCLagAcceptable(testCase.headHeight, testCase.qcHeight, testCase.qcExists, testCase.maxAllowedLag)
			if got != testCase.want {
				t.Fatalf("headQCLagAcceptable() = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestStaleHeadQCRecoveryPeerMatches(t *testing.T) {
	headHash := testHashFromText(t, "local-head")
	finalizedHash := testHashFromText(t, "local-finalized")
	head := blockchain.Head{
		Height:          20,
		BlockHash:       headHash,
		FinalizedHeight: 18,
		FinalizedHash:   finalizedHash,
	}
	tests := []struct {
		name   string
		status statusResponseEnvelope
		want   bool
	}{
		{
			name: "matching validator peer",
			status: statusResponseEnvelope{
				ValidatorEnabled: true,
				ConsensusEnabled: true,
				HeadHeight:       20,
				HeadHash:         headHash.String(),
				FinalizedHeight:  18,
				FinalizedHash:    finalizedHash.String(),
			},
			want: true,
		},
		{
			name: "same head but peer finalized ahead skips",
			status: statusResponseEnvelope{
				ValidatorEnabled: true,
				ConsensusEnabled: true,
				HeadHeight:       20,
				HeadHash:         headHash.String(),
				FinalizedHeight:  19,
				FinalizedHash:    testHashFromText(t, "peer-finalized-ahead").String(),
			},
			want: false,
		},
		{
			name: "same height fork skips",
			status: statusResponseEnvelope{
				ValidatorEnabled: true,
				ConsensusEnabled: true,
				HeadHeight:       20,
				HeadHash:         testHashFromText(t, "peer-fork").String(),
				FinalizedHeight:  18,
				FinalizedHash:    finalizedHash.String(),
			},
			want: false,
		},
		{
			name: "non validator skips",
			status: statusResponseEnvelope{
				ValidatorEnabled: false,
				ConsensusEnabled: true,
				HeadHeight:       20,
				HeadHash:         headHash.String(),
				FinalizedHeight:  18,
				FinalizedHash:    finalizedHash.String(),
			},
			want: false,
		},
		{
			name: "finalized hash mismatch skips",
			status: statusResponseEnvelope{
				ValidatorEnabled: true,
				ConsensusEnabled: true,
				HeadHeight:       20,
				HeadHash:         headHash.String(),
				FinalizedHeight:  18,
				FinalizedHash:    testHashFromText(t, "peer-finalized-mismatch").String(),
			},
			want: false,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got := staleHeadQCRecoveryPeerMatches(head, testCase.status)
			if got != testCase.want {
				t.Fatalf("staleHeadQCRecoveryPeerMatches() = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestRecoveryVoteMatchesHead(t *testing.T) {
	headHash := testHashFromText(t, "recovery-head")
	head := blockchain.Head{Slot: 8, Height: 3, BlockHash: headHash}
	tests := []struct {
		name string
		vote consensus.Vote
		want bool
	}{
		{
			name: "confirm vote matches head",
			vote: consensus.Vote{
				Type:        consensus.VoteTypeConfirm,
				Slot:        8,
				BlockHeight: 3,
				BlockHash:   headHash,
			},
			want: true,
		},
		{
			name: "non confirm vote skips",
			vote: consensus.Vote{
				Type:        consensus.VoteTypeSkip,
				Slot:        8,
				BlockHeight: 3,
				BlockHash:   headHash,
			},
			want: false,
		},
		{
			name: "slot mismatch skips",
			vote: consensus.Vote{
				Type:        consensus.VoteTypeConfirm,
				Slot:        9,
				BlockHeight: 3,
				BlockHash:   headHash,
			},
			want: false,
		},
		{
			name: "height mismatch skips",
			vote: consensus.Vote{
				Type:        consensus.VoteTypeConfirm,
				Slot:        8,
				BlockHeight: 4,
				BlockHash:   headHash,
			},
			want: false,
		},
		{
			name: "hash mismatch skips",
			vote: consensus.Vote{
				Type:        consensus.VoteTypeConfirm,
				Slot:        8,
				BlockHeight: 3,
				BlockHash:   testHashFromText(t, "recovery-fork"),
			},
			want: false,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got := recoveryVoteMatchesHead(testCase.vote, head)
			if got != testCase.want {
				t.Fatalf("recoveryVoteMatchesHead() = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestRebuildEpochReusesVoteCollectorByEpoch(t *testing.T) {
	node := newConsensusStatusTestNode(t)
	if err := node.rebuildEpoch(0, 1, node.epochSeed(0)); err != nil {
		t.Fatalf("rebuildEpoch(epoch 0) error = %v", err)
	}
	epochZeroCollector := node.voteCollector
	if epochZeroCollector == nil {
		t.Fatal("epoch 0 vote collector is nil")
	}

	if err := node.rebuildEpoch(1, 17, node.epochSeed(1)); err != nil {
		t.Fatalf("rebuildEpoch(epoch 1) error = %v", err)
	}
	epochOneCollector := node.voteCollector
	if epochOneCollector == nil {
		t.Fatal("epoch 1 vote collector is nil")
	}
	if epochOneCollector == epochZeroCollector {
		t.Fatal("epoch 1 reused epoch 0 vote collector")
	}

	if err := node.rebuildEpoch(0, 1, node.epochSeed(0)); err != nil {
		t.Fatalf("rebuildEpoch(epoch 0 again) error = %v", err)
	}
	if node.voteCollector != epochZeroCollector {
		t.Fatal("epoch 0 vote collector was not reused")
	}
	if node.voteCollectors[1] != epochOneCollector {
		t.Fatal("epoch 1 vote collector cache entry changed")
	}
}

func TestEpochContextLookupDoesNotRollbackGlobalEpoch(t *testing.T) {
	node := newConsensusStatusTestNode(t)
	if err := node.rebuildEpoch(1, 17, node.epochSeed(1)); err != nil {
		t.Fatalf("rebuildEpoch(epoch 1) error = %v", err)
	}

	node.mutex.Lock()
	if _, err := node.epochContextForSlotLocked(1); err != nil {
		node.mutex.Unlock()
		t.Fatalf("epochContextForSlotLocked(old slot) error = %v", err)
	}
	if err := node.ensureEpochForSlotLocked(1); err != nil {
		node.mutex.Unlock()
		t.Fatalf("ensureEpochForSlotLocked(old slot) error = %v", err)
	}
	gotEpochID := node.epochSnapshot.EpochID
	node.mutex.Unlock()

	if gotEpochID != 1 {
		t.Fatalf("global epoch = %d, want 1", gotEpochID)
	}
}

func TestReadOnlyRoutingDoesNotAdvanceGlobalEpoch(t *testing.T) {
	node := newConsensusStatusTestNode(t)
	if err := node.rebuildEpoch(0, 1, node.epochSeed(0)); err != nil {
		t.Fatalf("rebuildEpoch(epoch 0) error = %v", err)
	}

	node.mutex.Lock()
	_ = node.transactionFastPathForSlotLocked(17, true)
	gotEpochID := node.epochSnapshot.EpochID
	node.mutex.Unlock()

	if gotEpochID != 0 {
		t.Fatalf("global epoch = %d, want 0", gotEpochID)
	}
}

func TestCommitPrunesFutureEpochContextCaches(t *testing.T) {
	node := newConsensusStatusTestNode(t)
	if err := node.rebuildEpoch(0, 1, node.epochSeed(0)); err != nil {
		t.Fatalf("rebuildEpoch(epoch 0) error = %v", err)
	}

	node.mutex.Lock()
	if _, err := node.epochContextForSlotLocked(17); err != nil {
		node.mutex.Unlock()
		t.Fatalf("epochContextForSlotLocked(future slot) error = %v", err)
	}
	if _, exists := node.voteCollectors[1]; !exists {
		node.mutex.Unlock()
		t.Fatal("future vote collector was not cached")
	}
	node.pruneFutureEpochContextCachesLocked(0)
	_, snapshotExists := node.epochSnapshots[1]
	_, scheduleExists := node.leaderSchedules[1]
	_, collectorExists := node.voteCollectors[1]
	node.mutex.Unlock()

	if snapshotExists || scheduleExists || collectorExists {
		t.Fatal("future epoch context cache was not pruned")
	}
}

func TestLocalVoteEnvelopeCacheRequiresSameChoice(t *testing.T) {
	keyPair := mustStructureKeyPair("local-vote-envelope-cache")
	validatorID := consensus.NewValidatorID(keyPair.PublicKey)
	blockHash := testHashFromText(t, "cached-vote-head")
	node := &posNode{consensusKeyPair: keyPair}
	envelope := voteEnvelope{
		Vote: consensus.Vote{
			Type:        consensus.VoteTypeConfirm,
			Slot:        12,
			BlockHeight: 4,
			BlockHash:   blockHash,
			VoterID:     string(validatorID),
		},
		PublicKey:    keyPair.PublicKey,
		BLSPublicKey: []byte{1, 2, 3},
		BLSSignature: []byte{4, 5, 6},
		OriginPeerID: "local-peer",
		HopCount:     0,
		MaxHops:      defaultVoteMaxHops,
	}
	if !node.rememberLocalVoteEnvelope(envelope) {
		t.Fatal("rememberLocalVoteEnvelope() = false, want true")
	}

	node.mutex.Lock()
	cachedEnvelope, exists := node.cachedLocalVoteEnvelopeLocked(12, blockHash, validatorID)
	node.mutex.Unlock()
	if !exists {
		t.Fatal("cachedLocalVoteEnvelopeLocked() exists = false, want true")
	}
	if cachedEnvelope.Vote.BlockHash != blockHash {
		t.Fatalf("cached block hash = %s, want %s", cachedEnvelope.Vote.BlockHash.String(), blockHash.String())
	}

	otherHash := testHashFromText(t, "cached-vote-other-head")
	node.mutex.Lock()
	_, exists = node.cachedLocalVoteEnvelopeLocked(12, otherHash, validatorID)
	node.mutex.Unlock()
	if exists {
		t.Fatal("cachedLocalVoteEnvelopeLocked() accepted different block hash")
	}
	node.mutex.Lock()
	anyCachedEnvelope, exists := node.cachedAnyLocalVoteEnvelopeLocked(12, validatorID)
	node.mutex.Unlock()
	if !exists {
		t.Fatal("cachedAnyLocalVoteEnvelopeLocked() exists = false, want true")
	}
	if anyCachedEnvelope.Vote.BlockHash != blockHash {
		t.Fatalf("cached any block hash = %s, want %s", anyCachedEnvelope.Vote.BlockHash.String(), blockHash.String())
	}

	conflictingEnvelope := envelope
	conflictingEnvelope.Vote.BlockHash = otherHash
	if node.rememberLocalVoteEnvelope(conflictingEnvelope) {
		t.Fatal("rememberLocalVoteEnvelope() accepted conflicting block hash")
	}

	cachedEnvelope.BLSPublicKey[0] = 9
	node.mutex.Lock()
	cachedEnvelope, exists = node.cachedLocalVoteEnvelopeLocked(12, blockHash, validatorID)
	node.mutex.Unlock()
	if !exists {
		t.Fatal("cachedLocalVoteEnvelopeLocked() missing after clone mutation")
	}
	if cachedEnvelope.BLSPublicKey[0] != 1 {
		t.Fatalf("cached BLS public key was mutated: got %d, want 1", cachedEnvelope.BLSPublicKey[0])
	}
}

func TestVoteForCommittedHeadAllowsOldSlotWithoutRollback(t *testing.T) {
	node := newConsensusStatusTestNode(t)
	if err := node.rebuildEpoch(0, 1, node.epochSeed(0)); err != nil {
		t.Fatalf("rebuildEpoch() error = %v", err)
	}
	slot := node.epochSnapshot.StartSlot
	blockHash := testHashFromText(t, "recovery-old-slot-head")
	proposal := recoveryProposalForTest(t, node, slot)
	node.lastVotedSlot = slot + 8

	if err := node.voteForCommittedHead(context.Background(), proposal, blockHash, 0); err != nil {
		t.Fatalf("voteForCommittedHead() error = %v", err)
	}

	if node.lastVotedSlot != slot+8 {
		t.Fatalf("lastVotedSlot = %d, want %d", node.lastVotedSlot, slot+8)
	}
	if got := node.metrics.votesSent.Load(); got != 1 {
		t.Fatalf("votesSent = %d, want 1", got)
	}
	validatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	node.mutex.Lock()
	_, exists := node.cachedLocalVoteEnvelopeLocked(slot, blockHash, validatorID)
	node.mutex.Unlock()
	if !exists {
		t.Fatal("old slot recovery vote was not cached")
	}
}

func TestVoteForCommittedHeadRejectsConflictingCachedChoice(t *testing.T) {
	node := newConsensusStatusTestNode(t)
	if err := node.rebuildEpoch(0, 1, node.epochSeed(0)); err != nil {
		t.Fatalf("rebuildEpoch() error = %v", err)
	}
	slot := node.epochSnapshot.StartSlot
	validatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	stakeValue, active, err := node.localValidatorEffectiveStake(node.epochSnapshot.EpochID)
	if err != nil {
		t.Fatalf("localValidatorEffectiveStake() error = %v", err)
	}
	if !active || stakeValue == 0 {
		t.Fatalf("active=%v stake=%d, want active stake", active, stakeValue)
	}
	cachedHash := testHashFromText(t, "recovery-cached-head")
	cachedEnvelope, err := node.newLocalVoteEnvelope(consensus.Vote{
		Type:               consensus.VoteTypeConfirm,
		Slot:               slot,
		BlockHeight:        node.ledger.Head().Height + 1,
		BlockHash:          cachedHash,
		VoterID:            string(validatorID),
		Stake:              stakeValue,
		CreatedAtUnixMilli: 1710000000000,
	})
	if err != nil {
		t.Fatalf("newLocalVoteEnvelope() error = %v", err)
	}
	if !node.rememberLocalVoteEnvelope(cachedEnvelope) {
		t.Fatal("rememberLocalVoteEnvelope() = false, want true")
	}

	requestedHash := testHashFromText(t, "recovery-conflicting-head")
	proposal := recoveryProposalForTest(t, node, slot)
	if err := node.voteForCommittedHead(context.Background(), proposal, requestedHash, 0); err != nil {
		t.Fatalf("voteForCommittedHead() error = %v", err)
	}

	if got := node.metrics.votesSent.Load(); got != 0 {
		t.Fatalf("votesSent = %d, want 0", got)
	}
	node.mutex.Lock()
	cachedEnvelope, exists := node.cachedLocalVoteEnvelopeLocked(slot, cachedHash, validatorID)
	_, conflictingExists := node.cachedLocalVoteEnvelopeLocked(slot, requestedHash, validatorID)
	node.mutex.Unlock()
	if !exists || cachedEnvelope.Vote.BlockHash != cachedHash {
		t.Fatal("cached local vote choice was not preserved")
	}
	if conflictingExists {
		t.Fatal("conflicting local recovery vote was cached")
	}
}

func recoveryProposalForTest(t *testing.T, node *posNode, slot uint64) consensus.BlockProposal {
	t.Helper()
	stateRoot, err := node.ledger.State().RootHash()
	if err != nil {
		t.Fatalf("RootHash() error = %v", err)
	}
	head := node.ledger.Head()
	return consensus.BlockProposal{
		Header: consensus.BlockHeader{
			ChainID:            node.config.ChainID,
			Slot:               slot,
			Height:             head.Height + 1,
			ParentHash:         head.BlockHash,
			PreviousQCHash:     head.QCHash,
			LeaderID:           consensus.NewValidatorID(node.consensusKeyPair.PublicKey),
			EpochID:            node.epochSnapshot.EpochID,
			TimestampUnixMilli: 1710000000000,
			StateRoot:          stateRoot,
			AccountRoot:        stateRoot,
		},
	}
}

func TestShouldImportFinalizedSnapshotAfterSyncStartError(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		status    statusResponseEnvelope
		localHead blockchain.Head
		want      bool
	}{
		{
			name: "boundary error with peer finalized ahead imports",
			err: finalizedBoundarySyncError{
				PeerID:          "peer-a",
				AncestorHeight:  10,
				FinalizedHeight: 12,
			},
			status: statusResponseEnvelope{
				FinalizedHeight: 20,
				FinalizedHash:   testHashFromText(t, "peer-finalized").String(),
			},
			localHead: blockchain.Head{FinalizedHeight: 12},
			want:      true,
		},
		{
			name: "wrapped boundary error imports",
			err:  errors.Join(errors.New("sync failed"), finalizedBoundarySyncError{PeerID: "peer-a", AncestorHeight: 10, FinalizedHeight: 12}),
			status: statusResponseEnvelope{
				FinalizedHeight: 20,
				FinalizedHash:   testHashFromText(t, "peer-finalized-wrapped").String(),
			},
			localHead: blockchain.Head{FinalizedHeight: 12},
			want:      true,
		},
		{
			name: "plain error skips import",
			err:  errors.New("temporary status failure"),
			status: statusResponseEnvelope{
				FinalizedHeight: 20,
				FinalizedHash:   testHashFromText(t, "peer-finalized-plain").String(),
			},
			localHead: blockchain.Head{FinalizedHeight: 12},
			want:      false,
		},
		{
			name: "peer finalized not ahead skips import",
			err: finalizedBoundarySyncError{
				PeerID:          "peer-a",
				AncestorHeight:  10,
				FinalizedHeight: 12,
			},
			status: statusResponseEnvelope{
				FinalizedHeight: 12,
				FinalizedHash:   testHashFromText(t, "peer-finalized-equal").String(),
			},
			localHead: blockchain.Head{FinalizedHeight: 12},
			want:      false,
		},
		{
			name: "missing peer finalized hash skips import",
			err: finalizedBoundarySyncError{
				PeerID:          "peer-a",
				AncestorHeight:  10,
				FinalizedHeight: 12,
			},
			status:    statusResponseEnvelope{FinalizedHeight: 20},
			localHead: blockchain.Head{FinalizedHeight: 12},
			want:      false,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got := shouldImportFinalizedSnapshotAfterSyncStartError(testCase.err, testCase.status, testCase.localHead)
			if got != testCase.want {
				t.Fatalf("shouldImportFinalizedSnapshotAfterSyncStartError() = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestShouldImportFinalizedSnapshotBeforeBlockSync(t *testing.T) {
	finalizedHash := testHashFromText(t, "peer-finalized-before-sync").String()
	tests := []struct {
		name      string
		status    statusResponseEnvelope
		localHead blockchain.Head
		want      bool
	}{
		{
			name:      "peer finalized beyond local head imports",
			status:    statusResponseEnvelope{FinalizedHeight: 21, FinalizedHash: finalizedHash},
			localHead: blockchain.Head{Height: 20, FinalizedHeight: 18},
			want:      true,
		},
		{
			name:      "peer finalized at local head uses block sync",
			status:    statusResponseEnvelope{FinalizedHeight: 20, FinalizedHash: finalizedHash},
			localHead: blockchain.Head{Height: 20, FinalizedHeight: 18},
			want:      false,
		},
		{
			name:      "missing finalized hash skips import",
			status:    statusResponseEnvelope{FinalizedHeight: 21},
			localHead: blockchain.Head{Height: 20, FinalizedHeight: 18},
			want:      false,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got := shouldImportFinalizedSnapshotBeforeBlockSync(testCase.status, testCase.localHead)
			if got != testCase.want {
				t.Fatalf("shouldImportFinalizedSnapshotBeforeBlockSync() = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestShouldImportFinalizedSnapshotAfterBlockSyncError(t *testing.T) {
	finalizedHash := testHashFromText(t, "peer-finalized-after-sync-error").String()
	tests := []struct {
		name      string
		err       error
		status    statusResponseEnvelope
		localHead blockchain.Head
		want      bool
	}{
		{
			name:      "block validation error imports peer finalized checkpoint",
			err:       errors.New("consensus: proposal leader mismatch"),
			status:    statusResponseEnvelope{FinalizedHeight: 22, FinalizedHash: finalizedHash},
			localHead: blockchain.Head{Height: 20, FinalizedHeight: 18},
			want:      true,
		},
		{
			name:      "nil error skips import",
			err:       nil,
			status:    statusResponseEnvelope{FinalizedHeight: 22, FinalizedHash: finalizedHash},
			localHead: blockchain.Head{Height: 20, FinalizedHeight: 18},
			want:      false,
		},
		{
			name:      "peer finalized not ahead skips import",
			err:       errors.New("consensus: proposal leader mismatch"),
			status:    statusResponseEnvelope{FinalizedHeight: 18, FinalizedHash: finalizedHash},
			localHead: blockchain.Head{Height: 20, FinalizedHeight: 18},
			want:      false,
		},
		{
			name:      "missing finalized hash skips import",
			err:       errors.New("consensus: proposal leader mismatch"),
			status:    statusResponseEnvelope{FinalizedHeight: 22},
			localHead: blockchain.Head{Height: 20, FinalizedHeight: 18},
			want:      false,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got := shouldImportFinalizedSnapshotAfterBlockSyncError(testCase.err, testCase.status, testCase.localHead)
			if got != testCase.want {
				t.Fatalf("shouldImportFinalizedSnapshotAfterBlockSyncError() = %v, want %v", got, testCase.want)
			}
		})
	}
}

func assertStringSlicesEqual(t *testing.T, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("slice length = %d, want %d, got=%v", len(got), len(want), got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("slice[%d] = %q, want %q, got=%v", index, got[index], want[index], got)
		}
	}
}

func TestLocalValidatorEffectiveStakeRejectsCurrentEpochJail(t *testing.T) {
	node := newConsensusStatusTestNode(t)
	stakeValue, active, err := node.localValidatorEffectiveStake(0)
	if err != nil {
		t.Fatalf("localValidatorEffectiveStake() error = %v", err)
	}
	if !active || stakeValue == 0 {
		t.Fatalf("active=%v stake=%d, want active stake", active, stakeValue)
	}

	jailLocalValidatorForTest(t, node, 1)
	stakeValue, active, err = node.localValidatorEffectiveStake(0)
	if err != nil {
		t.Fatalf("localValidatorEffectiveStake(jailed) error = %v", err)
	}
	if active || stakeValue != 0 {
		t.Fatalf("jailed active=%v stake=%d, want inactive", active, stakeValue)
	}

	stakeValue, active, err = node.localValidatorEffectiveStake(1)
	if err != nil {
		t.Fatalf("localValidatorEffectiveStake(expired jail) error = %v", err)
	}
	if !active || stakeValue == 0 {
		t.Fatalf("expired jail active=%v stake=%d, want active stake", active, stakeValue)
	}
}

func TestLocalValidatorSnapshotStakeIgnoresSpeculativeCurrentState(t *testing.T) {
	node := newConsensusStatusTestNode(t)
	snapshot := node.epochSnapshot
	snapshotStake, snapshotActive := node.localValidatorEffectiveStakeFromSnapshot(snapshot)
	if !snapshotActive || snapshotStake == 0 {
		t.Fatalf("snapshot active=%v stake=%d, want active stake", snapshotActive, snapshotStake)
	}

	jailLocalValidatorForTest(t, node, 1)
	currentStake, currentActive, err := node.localValidatorEffectiveStake(0)
	if err != nil {
		t.Fatalf("localValidatorEffectiveStake() error = %v", err)
	}
	if currentActive || currentStake != 0 {
		t.Fatalf("current active=%v stake=%d, want inactive current state", currentActive, currentStake)
	}

	nextStake, nextActive := node.localValidatorEffectiveStakeFromSnapshot(snapshot)
	if !nextActive || nextStake != snapshotStake {
		t.Fatalf("snapshot after jail active=%v stake=%d, want active stake %d", nextActive, nextStake, snapshotStake)
	}
}

func jailLocalValidatorForTest(t *testing.T, node *posNode, jailUntilEpoch uint64) {
	t.Helper()
	validatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	state := node.ledger.State()
	for index := range state.Accounts {
		stakeState, err := stake.UnmarshalValidatorStateBinary(state.Accounts[index].Account.Data)
		if err != nil {
			continue
		}
		if consensus.NewValidatorID(stakeState.ConsensusPublicKey) != validatorID {
			continue
		}
		stakeState.Status = stake.ValidatorStatusJailed
		stakeState.JailUntilEpoch = jailUntilEpoch
		stakeState.UnlockEpoch = jailUntilEpoch
		data, err := stakeState.MarshalBinary()
		if err != nil {
			t.Fatalf("MarshalBinary() error = %v", err)
		}
		state.Accounts[index].Account.Data = data
		commitConsensusStatusState(t, node.ledger, state)
		return
	}
	t.Fatalf("local validator %s not found", validatorID)
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
