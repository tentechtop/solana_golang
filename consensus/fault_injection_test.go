package consensus

import "testing"

func TestFaultInjectionLeaderOfflineFormsSkipQC(t *testing.T) {
	collector, err := NewVoteCollectorWithQuorums(
		map[string]uint64{"alice": 34, "bob": 33, "carol": 33},
		Quorum{Numerator: 2, Denominator: 3, Strict: true},
		Quorum{Numerator: 2, Denominator: 3, Strict: true},
	)
	if err != nil {
		t.Fatalf("NewVoteCollectorWithQuorums() error = %v", err)
	}

	_, formed, err := collector.AddVote(testSkipVote("alice", 34))
	if err != nil {
		t.Fatalf("AddVote(alice skip) error = %v", err)
	}
	if formed {
		t.Fatal("skip QC formed below Byzantine-safe quorum")
	}
	certificate, formed, err := collector.AddVote(testSkipVote("bob", 33))
	if err != nil {
		t.Fatalf("AddVote(bob skip) error = %v", err)
	}
	if !formed {
		t.Fatal("skip QC not formed after two-thirds quorum")
	}
	if certificate.Type != VoteTypeSkip {
		t.Fatalf("QC type = %d, want skip", certificate.Type)
	}
	if certificate.BlockHeight != 11 || certificate.BlockHash != testHash(9) {
		t.Fatalf("skip QC block reference = height %d hash %s, want parent binding", certificate.BlockHeight, certificate.BlockHash.String())
	}
	if certificate.ConfirmedStake != 67 || certificate.ThresholdStake != 67 {
		t.Fatalf("QC stake = confirmed %d threshold %d, want 67/67", certificate.ConfirmedStake, certificate.ThresholdStake)
	}
}
