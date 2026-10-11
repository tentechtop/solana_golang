package consensus

import (
	"fmt"
	"testing"

	"github.com/tentechtop/ordexus-core/structure"
)

type networkFaultSignedVote struct {
	vote      Vote
	signature []byte
}

func TestEightValidatorQuorumRecoversAfterTwoNetworkFaults(t *testing.T) {
	snapshot := newEightValidatorNetworkSnapshot(t)
	allValidators := snapshot.Validators
	if len(allValidators) != 8 {
		t.Fatalf("validator count = %d, want 8", len(allValidators))
	}

	type committedHead struct {
		slot   uint64
		height uint64
		hash   structure.Hash
	}
	head := committedHead{hash: mustHashFromText(t, "eight-validator-genesis")}

	commitWithQuorum := func(slot uint64, blockHash structure.Hash) {
		t.Helper()
		prepareVotes := signNetworkFaultVotes(t, allValidators, VoteTypePrepare, slot, head.height+1, blockHash)
		prepareCertificate, prepared := collectNetworkFaultVotes(t, snapshot, prepareVotes)
		if !prepared {
			t.Fatalf("slot %d PrepareQC was not formed after network recovery", slot)
		}
		verifyNetworkFaultCertificate(t, snapshot, prepareCertificate)
		votes := signNetworkFaultVotes(t, allValidators, VoteTypeConfirm, slot, head.height+1, blockHash)
		certificate, formed := collectNetworkFaultVotes(t, snapshot, votes)
		if !formed {
			t.Fatalf("slot %d confirm QC was not formed after network recovery", slot)
		}
		verifyNetworkFaultCertificate(t, snapshot, certificate)
		head = committedHead{slot: slot, height: head.height + 1, hash: blockHash}
	}

	commitWithQuorum(1, mustHashFromText(t, "eight-validator-block-1"))

	runFaultRecoveryCycle := func(name string, timeoutSlot uint64, recoverySlot uint64, partitions [][]ValidatorState) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			beforeFault := head
			allTimeoutVotes := signNetworkFaultVotes(
				t,
				allValidators,
				VoteTypeSkip,
				timeoutSlot,
				head.height+1,
				head.hash,
			)
			votesByValidator := make(map[string]networkFaultSignedVote, len(allTimeoutVotes))
			for _, signedVote := range allTimeoutVotes {
				votesByValidator[signedVote.vote.VoterID] = signedVote
			}

			for partitionIndex, validators := range partitions {
				partitionVotes := make([]networkFaultSignedVote, 0, len(validators))
				for _, validator := range validators {
					partitionVotes = append(partitionVotes, votesByValidator[string(validator.ValidatorID)])
				}
				if _, formed := collectNetworkFaultVotes(t, snapshot, partitionVotes); formed {
					t.Fatalf("partition %d formed TimeoutQC below Byzantine-safe quorum", partitionIndex+1)
				}
			}
			if head != beforeFault {
				t.Fatalf("head changed during fault: %+v != %+v", head, beforeFault)
			}

			timeoutQC, formed := collectNetworkFaultVotes(t, snapshot, allTimeoutVotes)
			if !formed {
				t.Fatal("TimeoutQC was not formed after partitions exchanged votes")
			}
			verifyNetworkFaultCertificate(t, snapshot, timeoutQC)
			if timeoutQC.Type != VoteTypeSkip || timeoutQC.Slot != timeoutSlot || timeoutQC.BlockHash != head.hash {
				t.Fatalf("TimeoutQC = %+v, want slot %d bound to current head", timeoutQC, timeoutSlot)
			}

			recoveryHash := mustHashFromText(t, fmt.Sprintf("eight-validator-recovery-%d", recoverySlot))
			prepareVotes := signNetworkFaultVotes(
				t,
				allValidators,
				VoteTypePrepare,
				recoverySlot,
				head.height+1,
				recoveryHash,
			)
			prepareQC, prepared := collectNetworkFaultVotes(t, snapshot, prepareVotes)
			if !prepared {
				t.Fatal("PrepareQC was not formed after network recovery")
			}
			verifyNetworkFaultCertificate(t, snapshot, prepareQC)
			recoveryVotes := signNetworkFaultVotes(
				t,
				allValidators,
				VoteTypeConfirm,
				recoverySlot,
				head.height+1,
				recoveryHash,
			)
			recoveryQC, recovered := collectNetworkFaultVotes(t, snapshot, recoveryVotes)
			if !recovered {
				t.Fatal("ConfirmQC was not formed after network recovery")
			}
			verifyNetworkFaultCertificate(t, snapshot, recoveryQC)
			head = committedHead{slot: recoverySlot, height: head.height + 1, hash: recoveryHash}
		})
	}

	runFaultRecoveryCycle("four-four partition", 2, 3, [][]ValidatorState{
		allValidators[:4],
		allValidators[4:],
	})
	runFaultRecoveryCycle("five-three partition", 4, 5, [][]ValidatorState{
		allValidators[:5],
		allValidators[5:],
	})

	if head.slot != 5 || head.height != 3 || head.hash != mustHashFromText(t, "eight-validator-recovery-5") {
		t.Fatalf("final head = %+v, want slot 5 height 3 after two recoveries", head)
	}
}

func TestEightValidatorStrictQuorumBoundaryForEveryOnlineCount(t *testing.T) {
	snapshot := newEightValidatorNetworkSnapshot(t)
	validators := snapshot.Validators
	blockHash := mustHashFromText(t, "eight-validator-quorum-boundary")
	for onlineCount := 0; onlineCount <= len(validators); onlineCount++ {
		onlineCount := onlineCount
		t.Run(fmt.Sprintf("online-%d", onlineCount), func(t *testing.T) {
			for _, voteType := range []VoteType{VoteTypePrepare, VoteTypeConfirm, VoteTypeSkip} {
				votes := signNetworkFaultVotes(t, validators[:onlineCount], voteType, 7, 4, blockHash)
				certificate, formed := collectNetworkFaultVotes(t, snapshot, votes)
				wantFormed := onlineCount >= 6
				if formed != wantFormed {
					t.Fatalf("vote type %d formed=%v with %d/8 online, want %v", voteType, formed, onlineCount, wantFormed)
				}
				if formed {
					verifyNetworkFaultCertificate(t, snapshot, certificate)
				}
			}
		})
	}
}

func TestQuorumUsesReachableStakeInsteadOfValidatorCount(t *testing.T) {
	baseSnapshot := newEightValidatorNetworkSnapshot(t)
	stakes := []uint64{40, 15, 15, 10, 8, 5, 4, 3}
	validators := append([]ValidatorState(nil), baseSnapshot.Validators...)
	for index := range validators {
		validators[index].StakeLamports = stakes[index]
	}
	validatorSet, err := NewValidatorSet(validators)
	if err != nil {
		t.Fatalf("NewValidatorSet() error = %v", err)
	}
	snapshot := mustEpochSnapshot(t, 0, 1, 16, mustHashFromText(t, "weighted-network-epoch"), validatorSet)
	blockHash := mustHashFromText(t, "weighted-network-block")

	highStakeVotes := signNetworkFaultVotes(t, validators[:3], VoteTypeConfirm, 9, 5, blockHash)
	if certificate, formed := collectNetworkFaultVotes(t, snapshot, highStakeVotes); !formed {
		t.Fatal("70% stake held by three validators did not form quorum")
	} else if certificate.ConfirmedStake != 70 || certificate.ThresholdStake != 67 {
		t.Fatalf("high-stake certificate = %d/%d, want 70/67", certificate.ConfirmedStake, certificate.ThresholdStake)
	}

	lowStakeVotes := signNetworkFaultVotes(t, validators[3:], VoteTypeConfirm, 9, 5, blockHash)
	if _, formed := collectNetworkFaultVotes(t, snapshot, lowStakeVotes); formed {
		t.Fatal("five validators with only 30% stake formed quorum")
	}
}

func newEightValidatorNetworkSnapshot(t *testing.T) EpochSnapshot {
	t.Helper()
	validators := make([]ValidatorState, 0, 8)
	for index := 0; index < 8; index++ {
		accountKeyPair := mustKeyPair(t, fmt.Sprintf("network-fault-account-%d", index))
		consensusKeyPair := mustKeyPair(t, fmt.Sprintf("network-fault-consensus-%d", index))
		blsKeyPair := testBLSKeyPair(t, consensusKeyPair.PublicKey)
		validators = append(validators, ValidatorState{
			AccountAddress:     accountKeyPair.PublicKey,
			ConsensusPublicKey: consensusKeyPair.PublicKey,
			BLSPublicKey:       append([]byte(nil), blsKeyPair.PublicKey...),
			P2PPeerID:          fmt.Sprintf("network-fault-peer-%d", index),
			StakeLamports:      1,
			Status:             ValidatorStatusActive,
		})
	}
	validatorSet, err := NewValidatorSet(validators)
	if err != nil {
		t.Fatalf("NewValidatorSet() error = %v", err)
	}
	return mustEpochSnapshot(t, 0, 1, 16, mustHashFromText(t, "eight-validator-network-epoch"), validatorSet)
}

func signNetworkFaultVotes(
	t *testing.T,
	validators []ValidatorState,
	voteType VoteType,
	slot uint64,
	blockHeight uint64,
	blockHash structure.Hash,
) []networkFaultSignedVote {
	t.Helper()
	signedVotes := make([]networkFaultSignedVote, 0, len(validators))
	createdAtUnixMilli := int64(1_710_000_000_000 + slot)
	for _, validator := range validators {
		vote := Vote{
			Type:               voteType,
			Slot:               slot,
			BlockHeight:        blockHeight,
			BlockHash:          blockHash,
			VoterID:            string(validator.ValidatorID),
			Stake:              validator.StakeLamports,
			CreatedAtUnixMilli: createdAtUnixMilli,
		}
		blsKeyPair := testBLSKeyPair(t, validator.ConsensusPublicKey)
		signature, err := SignBLSVote(blsKeyPair.PrivateKey, vote)
		if err != nil {
			t.Fatalf("SignBLSVote() error = %v", err)
		}
		signedVotes = append(signedVotes, networkFaultSignedVote{vote: vote, signature: signature})
	}
	return signedVotes
}

func collectNetworkFaultVotes(
	t *testing.T,
	snapshot EpochSnapshot,
	signedVotes []networkFaultSignedVote,
) (QuorumCertificate, bool) {
	t.Helper()
	collector, err := NewVoteCollectorWithQuorums(
		snapshot.StakeMap(),
		Quorum{Numerator: 2, Denominator: 3, Strict: true},
		Quorum{Numerator: 2, Denominator: 3, Strict: true},
	)
	if err != nil {
		t.Fatalf("NewVoteCollector() error = %v", err)
	}
	for _, signedVote := range signedVotes {
		certificate, formed, err := collector.AddVoteWithBLS(
			signedVote.vote,
			signedVote.signature,
			snapshot.ValidatorOrder(),
		)
		if err != nil {
			t.Fatalf("AddVoteWithBLS() error = %v", err)
		}
		if formed {
			return certificate, true
		}
	}
	return QuorumCertificate{}, false
}

func verifyNetworkFaultCertificate(t *testing.T, snapshot EpochSnapshot, certificate QuorumCertificate) {
	t.Helper()
	wantThreshold := uint64(6)
	quorum := Quorum{Numerator: 2, Denominator: 3, Strict: true}
	if certificate.ThresholdStake != wantThreshold || certificate.ConfirmedStake < certificate.ThresholdStake {
		t.Fatalf("certificate stake = %d/%d, want threshold %d/8", certificate.ConfirmedStake, certificate.ThresholdStake, wantThreshold)
	}
	if err := VerifyQuorumCertificateForSnapshot(
		certificate,
		snapshot,
		quorum,
	); err != nil {
		t.Fatalf("VerifyQuorumCertificateForSnapshot() error = %v", err)
	}
}
