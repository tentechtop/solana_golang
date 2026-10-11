package consensus

import (
	"context"
	"testing"
	"time"

	"github.com/tentechtop/ordexus-core/structure"
)

func TestValidateTimeoutQCRequiresParentBinding(t *testing.T) {
	parentHash := testHash(41)
	certificate := QuorumCertificate{
		Type:               VoteTypeSkip,
		Slot:               9,
		BlockHeight:        4,
		BlockHash:          parentHash,
		ThresholdStake:     67,
		ConfirmedStake:     67,
		Voters:             []string{"alice", "bob"},
		CreatedAtUnixMilli: time.Now().UnixMilli(),
	}
	if err := validateTimeoutQC(10, 3, 4, parentHash, &certificate); err != nil {
		t.Fatalf("validateTimeoutQC() error = %v", err)
	}
	wrongParent := structure.Hash{1}
	if err := validateTimeoutQC(10, 3, 4, wrongParent, &certificate); err == nil {
		t.Fatal("validateTimeoutQC(wrong parent) error = nil")
	}
	if err := validateTimeoutQC(11, 3, 4, parentHash, &certificate); err == nil {
		t.Fatal("validateTimeoutQC(wrong slot) error = nil")
	}
}

func TestProduceBlockCarriesTimeoutQC(t *testing.T) {
	fixture := newPoSTestFixture(t)
	epoch := mustEpochSnapshot(t, 0, 1, 32, mustHashFromText(t, "timeout-epoch"), fixture.validatorSetFromGenesis(t))
	schedule := mustLeaderSchedule(t, epoch)
	leaderID, err := schedule.LeaderForSlot(2)
	if err != nil {
		t.Fatalf("LeaderForSlot() error = %v", err)
	}
	parentHash := mustHashFromText(t, "timeout-genesis")
	timeoutQC := collectQC(t, epoch, VoteTypeSkip, 1, 1, parentHash)
	proposal, _, err := (BlockProducer{ChainID: "pos-e2e", Executor: newPoSTestExecutor(t)}).ProduceBlock(
		context.Background(),
		ProduceBlockRequest{
			Slot:           2,
			ParentSlot:     0,
			Height:         1,
			EpochSnapshot:  epoch,
			Schedule:       schedule,
			ParentHash:     parentHash,
			PreviousQCHash: parentHash,
			TimeoutQC:      &timeoutQC,
			ParentState:    fixture.state,
			BlockhashQueue: fixture.blockhashQueue,
			LeaderKeyPair:  fixture.consensusKeyPairByID(t, leaderID),
		},
	)
	if err != nil {
		t.Fatalf("ProduceBlock() error = %v", err)
	}
	if proposal.TimeoutQC == nil || proposal.TimeoutQC.Slot != timeoutQC.Slot {
		t.Fatalf("proposal timeout qc = %+v, want slot %d", proposal.TimeoutQC, timeoutQC.Slot)
	}
	expectedHash, err := HashQuorumCertificate(&timeoutQC)
	if err != nil {
		t.Fatalf("HashQuorumCertificate() error = %v", err)
	}
	if proposal.Header.TimeoutQCHash != expectedHash {
		t.Fatal("proposal timeout qc hash mismatch")
	}
}

func TestFourValidatorEvidenceDrivenRoundFlow(t *testing.T) {
	fixture := newPoSTestFixture(t)
	epoch := mustEpochSnapshot(t, 0, 1, 32, mustHashFromText(t, "four-validator-timeout-epoch"), fixture.validatorSetFromGenesis(t))
	schedule := mustLeaderSchedule(t, epoch)
	producer := BlockProducer{ChainID: "pos-e2e", Executor: newPoSTestExecutor(t)}
	genesisHash := mustHashFromText(t, "four-validator-genesis")
	firstLeaderID, err := schedule.LeaderForSlot(1)
	if err != nil {
		t.Fatalf("LeaderForSlot(1) error = %v", err)
	}
	firstProposal, firstState, err := producer.ProduceBlock(context.Background(), ProduceBlockRequest{
		Slot:           1,
		ParentSlot:     0,
		Height:         1,
		EpochSnapshot:  epoch,
		Schedule:       schedule,
		ParentHash:     genesisHash,
		PreviousQCHash: genesisHash,
		ParentState:    fixture.state,
		Transactions:   fixture.registerTransactions(t),
		BlockhashQueue: fixture.blockhashQueue,
		LeaderKeyPair:  fixture.consensusKeyPairByID(t, firstLeaderID),
	})
	if err != nil {
		t.Fatalf("ProduceBlock(first) error = %v", err)
	}
	firstHash, err := firstProposal.Hash()
	if err != nil {
		t.Fatalf("first proposal hash error = %v", err)
	}
	confirmQC := collectQC(t, epoch, VoteTypeConfirm, 1, 1, firstHash)
	if err := VerifyBLSAggregateWithStake(
		confirmQC,
		epoch.ValidatorOrder(),
		epoch.BLSPublicKeys(),
		epoch.StakeMap(),
		Quorum{Numerator: 2, Denominator: 3, Strict: true},
	); err != nil {
		t.Fatalf("VerifyBLSAggregateWithStake(confirm) error = %v", err)
	}

	timeoutQC := collectQC(t, epoch, VoteTypeSkip, 2, 2, firstHash)
	if err := VerifyBLSAggregateWithStake(
		timeoutQC,
		epoch.ValidatorOrder(),
		epoch.BLSPublicKeys(),
		epoch.StakeMap(),
		Quorum{Numerator: 2, Denominator: 3, Strict: true},
	); err != nil {
		t.Fatalf("VerifyBLSAggregateWithStake(timeout) error = %v", err)
	}

	nextLeaderID, err := schedule.LeaderForSlot(3)
	if err != nil {
		t.Fatalf("LeaderForSlot(3) error = %v", err)
	}
	if _, _, err := producer.ProduceBlock(context.Background(), ProduceBlockRequest{
		Slot:           3,
		ParentSlot:     1,
		Height:         2,
		EpochSnapshot:  epoch,
		Schedule:       schedule,
		ParentHash:     firstHash,
		PreviousQCHash: mustHashFromText(t, "four-validator-first-qc"),
		ParentState:    firstState,
		BlockhashQueue: fixture.blockhashQueue,
		LeaderKeyPair:  fixture.consensusKeyPairByID(t, nextLeaderID),
	}); err == nil {
		t.Fatal("ProduceBlock(without timeout qc) error = nil")
	}
	tamperedTimeoutQC := timeoutQC
	tamperedTimeoutQC.AggregateSignature = append([]byte(nil), timeoutQC.AggregateSignature...)
	tamperedTimeoutQC.AggregateSignature[0] ^= 0xff
	if _, _, err := producer.ProduceBlock(context.Background(), ProduceBlockRequest{
		Slot:           3,
		ParentSlot:     1,
		Height:         2,
		EpochSnapshot:  epoch,
		Schedule:       schedule,
		ParentHash:     firstHash,
		PreviousQCHash: mustHashFromText(t, "four-validator-first-qc"),
		TimeoutQC:      &tamperedTimeoutQC,
		ParentState:    firstState,
		BlockhashQueue: fixture.blockhashQueue,
		LeaderKeyPair:  fixture.consensusKeyPairByID(t, nextLeaderID),
	}); err == nil {
		t.Fatal("ProduceBlock(with tampered timeout qc) error = nil")
	}

	nextProposal, _, err := producer.ProduceBlock(context.Background(), ProduceBlockRequest{
		Slot:           3,
		ParentSlot:     1,
		Height:         2,
		EpochSnapshot:  epoch,
		Schedule:       schedule,
		ParentHash:     firstHash,
		PreviousQCHash: mustHashFromText(t, "four-validator-first-qc"),
		TimeoutQC:      &timeoutQC,
		ParentState:    firstState,
		BlockhashQueue: fixture.blockhashQueue,
		LeaderKeyPair:  fixture.consensusKeyPairByID(t, nextLeaderID),
	})
	if err != nil {
		t.Fatalf("ProduceBlock(after TimeoutQC) error = %v", err)
	}
	if nextProposal.TimeoutQC == nil || nextProposal.TimeoutQC.Slot != 2 || nextProposal.Header.Slot != 3 {
		t.Fatalf("next proposal timeout evidence = %+v slot=%d", nextProposal.TimeoutQC, nextProposal.Header.Slot)
	}
	if _, err := (ProposalVerifier{ChainID: "pos-e2e", Executor: newPoSTestExecutor(t)}).VerifyProposal(
		context.Background(),
		VerifyProposalRequest{
			Proposal:       nextProposal,
			ParentSlot:     1,
			EpochSnapshot:  epoch,
			Schedule:       schedule,
			ParentHash:     firstHash,
			ParentState:    firstState,
			BlockhashQueue: fixture.blockhashQueue,
			Leader:         fixture.validatorByID(t, epoch, nextLeaderID),
		},
	); err != nil {
		t.Fatalf("VerifyProposal(after TimeoutQC) error = %v", err)
	}
	nextHash, err := nextProposal.Hash()
	if err != nil {
		t.Fatalf("next proposal hash error = %v", err)
	}
	nextConfirmQC := collectQC(t, epoch, VoteTypeConfirm, 3, 2, nextHash)
	if err := VerifyBLSAggregateWithStake(
		nextConfirmQC,
		epoch.ValidatorOrder(),
		epoch.BLSPublicKeys(),
		epoch.StakeMap(),
		Quorum{Numerator: 2, Denominator: 3, Strict: true},
	); err != nil {
		t.Fatalf("VerifyBLSAggregateWithStake(next confirm) error = %v", err)
	}
}
