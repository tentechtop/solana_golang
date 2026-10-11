package consensus

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/tentechtop/ordexus-core/structure"
)

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

func TestVoteCollectorEmitsOnlyThresholdAndFullCertificates(t *testing.T) {
	collector, err := NewVoteCollector(
		map[string]uint64{"alice": 34, "bob": 33, "carol": 20, "dave": 13},
		Quorum{Numerator: 2, Denominator: 3},
	)
	if err != nil {
		t.Fatalf("NewVoteCollector() error = %v", err)
	}
	blockHash := testHash(7)
	for _, voter := range []struct {
		id    string
		stake uint64
		want  bool
	}{
		{id: "alice", stake: 34, want: false},
		{id: "bob", stake: 33, want: true},
		{id: "carol", stake: 20, want: false},
		{id: "dave", stake: 13, want: true},
	} {
		vote := testVote(voter.id, voter.stake, blockHash)
		certificate, formed, err := collector.AddVote(vote)
		if err != nil {
			t.Fatalf("AddVote(%s) error = %v", voter.id, err)
		}
		if formed != voter.want {
			t.Fatalf("AddVote(%s) formed = %v, want %v", voter.id, formed, voter.want)
		}
		if voter.id == "dave" && certificate.ConfirmedStake != 100 {
			t.Fatalf("full certificate stake = %d, want 100", certificate.ConfirmedStake)
		}
	}
}

func TestStrictQuorumRequiresMoreThanTwoThirds(t *testing.T) {
	required, err := (Quorum{Numerator: 2, Denominator: 3, Strict: true}).RequiredStake(99)
	if err != nil {
		t.Fatalf("RequiredStake() error = %v", err)
	}
	if required != 67 {
		t.Fatalf("required stake = %d, want 67", required)
	}
}

func TestVoteCollectorRejectsQuorumsWithoutHonestIntersection(t *testing.T) {
	_, err := NewVoteCollectorWithQuorums(
		map[string]uint64{"alice": 34, "bob": 33, "carol": 33},
		Quorum{Numerator: 2, Denominator: 3, Strict: true},
		Quorum{Numerator: 1, Denominator: 3, Strict: true},
	)
	if !errors.Is(err, ErrInvalidQuorum) {
		t.Fatalf("NewVoteCollectorWithQuorums(unsafe timeout) error = %v, want ErrInvalidQuorum", err)
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
	if certificate.BlockHash != testHash(9) || certificate.BlockHeight != 11 {
		t.Fatalf("skip certificate parent = %s/%d, want bound parent", certificate.BlockHash.String(), certificate.BlockHeight)
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

func TestVoteCollectorRejectsConfirmAfterTimeoutInSameRound(t *testing.T) {
	collector := newTestVoteCollector(t)
	if _, _, err := collector.AddVote(testSkipVote("alice", 34)); err != nil {
		t.Fatalf("AddVote(timeout) error = %v", err)
	}
	confirmVote := testVote("alice", 34, testHash(1))
	if _, _, err := collector.AddVote(confirmVote); !errors.Is(err, ErrConflictingVote) {
		t.Fatalf("AddVote(confirm after timeout) error = %v, want ErrConflictingVote", err)
	}
}

func TestVoteCollectorAllowsPrepareThenTimeoutInSameRound(t *testing.T) {
	collector := newTestVoteCollector(t)
	prepareVote := testVote("alice", 34, testHash(1))
	prepareVote.Type = VoteTypePrepare
	if _, _, err := collector.AddVote(prepareVote); err != nil {
		t.Fatalf("AddVote(prepare) error = %v", err)
	}
	if _, _, err := collector.AddVote(testSkipVote("alice", 34)); err != nil {
		t.Fatalf("AddVote(timeout after prepare) error = %v", err)
	}
}

func TestVoteCollectorRejectsConflictingPrepareVote(t *testing.T) {
	collector := newTestVoteCollector(t)
	firstVote := testVote("alice", 34, testHash(1))
	firstVote.Type = VoteTypePrepare
	secondVote := testVote("alice", 34, testHash(2))
	secondVote.Type = VoteTypePrepare
	if _, _, err := collector.AddVote(firstVote); err != nil {
		t.Fatalf("AddVote(first prepare) error = %v", err)
	}
	if _, _, err := collector.AddVote(secondVote); !errors.Is(err, ErrConflictingVote) {
		t.Fatalf("AddVote(conflicting prepare) error = %v, want ErrConflictingVote", err)
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

func ExampleVoteCollector() {
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
		BlockHeight:        11,
		BlockHash:          testHash(9),
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
