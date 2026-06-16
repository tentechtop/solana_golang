package consensus

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
	"time"

	"solana_golang/structure"
)

func TestSlotClockTickAndSkip(t *testing.T) {
	startedAt := time.Unix(1710000000, 0)
	clock, err := NewSlotClock(startedAt, 7, 400*time.Millisecond, 250*time.Millisecond)
	if err != nil {
		t.Fatalf("NewSlotClock() error = %v", err)
	}

	openTick := clock.Tick(startedAt.Add(850 * time.Millisecond))
	if openTick.Slot != 9 {
		t.Fatalf("open slot = %d, want 9", openTick.Slot)
	}
	if openTick.ShouldSkip {
		t.Fatal("open tick ShouldSkip = true, want false")
	}

	skipTick := clock.Tick(startedAt.Add(1060 * time.Millisecond))
	if skipTick.Slot != 9 {
		t.Fatalf("skip slot = %d, want 9", skipTick.Slot)
	}
	if !skipTick.ShouldSkip {
		t.Fatal("skip tick ShouldSkip = false, want true")
	}
}

func TestSlotClockTickBeforeStartIsNotStarted(t *testing.T) {
	startedAt := time.Unix(1710000000, 0)
	clock, err := NewSlotClock(startedAt, 1, time.Second, 700*time.Millisecond)
	if err != nil {
		t.Fatalf("NewSlotClock() error = %v", err)
	}

	beforeTick := clock.Tick(startedAt.Add(-time.Millisecond))
	if beforeTick.Started {
		t.Fatal("before start tick Started = true, want false")
	}
	if beforeTick.Slot != 1 {
		t.Fatalf("before start slot = %d, want 1", beforeTick.Slot)
	}

	startTick := clock.Tick(startedAt)
	if !startTick.Started {
		t.Fatal("start tick Started = false, want true")
	}
}

func TestVoteCollectorBuildsQuorumCertificate(t *testing.T) {
	collector := newTestVoteCollector(t)
	blockHash := testHash(1)

	certificate, confirmed, err := collector.AddVote(testVote("alice", 34, blockHash))
	if err != nil {
		t.Fatalf("AddVote(alice) error = %v", err)
	}
	if confirmed {
		t.Fatal("AddVote(alice) confirmed = true, want false")
	}
	if certificate.ConfirmedStake != 0 {
		t.Fatalf("certificate stake = %d, want 0", certificate.ConfirmedStake)
	}

	certificate, confirmed, err = collector.AddVote(testVote("bob", 33, blockHash))
	if err != nil {
		t.Fatalf("AddVote(bob) error = %v", err)
	}
	if !confirmed {
		t.Fatal("AddVote(bob) confirmed = false, want true")
	}
	if certificate.ThresholdStake != 67 {
		t.Fatalf("threshold = %d, want 67", certificate.ThresholdStake)
	}
	if certificate.ConfirmedStake != 67 {
		t.Fatalf("confirmed stake = %d, want 67", certificate.ConfirmedStake)
	}
	if fmt.Sprint(certificate.Voters) != "[alice bob]" {
		t.Fatalf("voters = %v, want [alice bob]", certificate.Voters)
	}
}

func TestVoteCollectorBuildsSkipCertificate(t *testing.T) {
	collector := newTestVoteCollector(t)

	if _, _, err := collector.AddVote(testSkipVote("alice", 34)); err != nil {
		t.Fatalf("AddVote(skip alice) error = %v", err)
	}
	certificate, confirmed, err := collector.AddVote(testSkipVote("bob", 33))
	if err != nil {
		t.Fatalf("AddVote(skip bob) error = %v", err)
	}
	if !confirmed {
		t.Fatal("skip confirmed = false, want true")
	}
	if certificate.Type != VoteTypeSkip {
		t.Fatalf("certificate type = %d, want skip", certificate.Type)
	}
	if !certificate.BlockHash.IsZero() {
		t.Fatal("skip certificate block hash is not zero")
	}
}

func TestVoteCollectorRejectsDuplicateAndConflict(t *testing.T) {
	collector := newTestVoteCollector(t)
	blockHash := testHash(1)
	nextBlockHash := testHash(2)

	if _, _, err := collector.AddVote(testVote("alice", 34, blockHash)); err != nil {
		t.Fatalf("AddVote(first) error = %v", err)
	}
	if _, _, err := collector.AddVote(testVote("alice", 34, blockHash)); !errors.Is(err, ErrDuplicateVote) {
		t.Fatalf("AddVote(duplicate) error = %v, want ErrDuplicateVote", err)
	}
	if _, _, err := collector.AddVote(testVote("alice", 34, nextBlockHash)); !errors.Is(err, ErrConflictingVote) {
		t.Fatalf("AddVote(conflict) error = %v, want ErrConflictingVote", err)
	}
}

func TestVoteBorshRoundTrip(t *testing.T) {
	vote := testVote("alice", 34, testHash(7))

	encoded, err := vote.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	decoded, err := UnmarshalVoteBinary(encoded)
	if err != nil {
		t.Fatalf("UnmarshalVoteBinary() error = %v", err)
	}
	if decoded != vote {
		t.Fatalf("decoded vote = %+v, want %+v", decoded, vote)
	}
}

func TestCertificateBorshRoundTrip(t *testing.T) {
	collector := newTestVoteCollector(t)
	blockHash := testHash(9)

	if _, _, err := collector.AddVote(testVote("alice", 34, blockHash)); err != nil {
		t.Fatalf("AddVote(alice) error = %v", err)
	}
	certificate, confirmed, err := collector.AddVote(testVote("bob", 33, blockHash))
	if err != nil {
		t.Fatalf("AddVote(bob) error = %v", err)
	}
	if !confirmed {
		t.Fatal("confirmed = false, want true")
	}

	encoded, err := certificate.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	decoded, err := UnmarshalCertificateBinary(encoded)
	if err != nil {
		t.Fatalf("UnmarshalCertificateBinary() error = %v", err)
	}
	if !bytes.Equal(decoded.BlockHash[:], certificate.BlockHash[:]) {
		t.Fatal("decoded block hash mismatch")
	}
	if decoded.ConfirmedStake != certificate.ConfirmedStake {
		t.Fatalf("decoded stake = %d, want %d", decoded.ConfirmedStake, certificate.ConfirmedStake)
	}
	if fmt.Sprint(decoded.Voters) != fmt.Sprint(certificate.Voters) {
		t.Fatalf("decoded voters = %v, want %v", decoded.Voters, certificate.Voters)
	}
}

func Example_localSlotSkipAndVoteConfirm() {
	startedAt := time.Unix(1710000000, 0)
	clock, _ := NewSlotClock(startedAt, 0, 400*time.Millisecond, 250*time.Millisecond)
	tick := clock.Tick(startedAt.Add(260 * time.Millisecond))
	fmt.Println(tick.Slot, tick.ShouldSkip)

	collector, _ := NewVoteCollector(map[string]uint64{
		"alice": 34,
		"bob":   33,
		"carol": 33,
	}, Quorum{Numerator: 2, Denominator: 3})
	blockHash := testHash(1)

	_, confirmed, _ := collector.AddVote(testVote("alice", 34, blockHash))
	fmt.Println(confirmed)

	certificate, confirmed, _ := collector.AddVote(testVote("bob", 33, blockHash))
	fmt.Println(confirmed, certificate.ConfirmedStake)

	// Output:
	// 0 true
	// false
	// true 67
}

func newTestVoteCollector(t *testing.T) *VoteCollector {
	t.Helper()

	collector, err := NewVoteCollector(map[string]uint64{
		"alice": 34,
		"bob":   33,
		"carol": 33,
	}, Quorum{Numerator: 2, Denominator: 3})
	if err != nil {
		t.Fatalf("NewVoteCollector() error = %v", err)
	}
	return collector
}

func testVote(voterID string, stake uint64, blockHash structure.Hash) Vote {
	return Vote{
		Type:               VoteTypeConfirm,
		Slot:               10,
		BlockHeight:        10,
		BlockHash:          blockHash,
		VoterID:            voterID,
		Stake:              stake,
		CreatedAtUnixMilli: 1710000000000,
	}
}

func testSkipVote(voterID string, stake uint64) Vote {
	return Vote{
		Type:               VoteTypeSkip,
		Slot:               10,
		VoterID:            voterID,
		Stake:              stake,
		CreatedAtUnixMilli: 1710000000000,
	}
}

func testHash(seed byte) structure.Hash {
	var hash structure.Hash
	for index := range hash {
		hash[index] = seed + byte(index)
	}
	return hash
}
