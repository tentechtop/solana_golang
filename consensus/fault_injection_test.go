package consensus

import "testing"

func TestFaultInjectionLeaderOfflineFormsSkipQC(t *testing.T) {
	collector := newTestVoteCollector(t)

	if _, _, err := collector.AddVote(testSkipVote("alice", 34)); err != nil {
		t.Fatalf("AddVote(alice skip) error = %v", err)
	}
	certificate, formed, err := collector.AddVote(testSkipVote("bob", 33))
	if err != nil {
		t.Fatalf("AddVote(bob skip) error = %v", err)
	}
	if !formed {
		t.Fatal("skip QC not formed after threshold stake")
	}
	if certificate.Type != VoteTypeSkip {
		t.Fatalf("QC type = %d, want skip", certificate.Type)
	}
	if certificate.BlockHeight != 0 || !certificate.BlockHash.IsZero() {
		t.Fatalf("skip QC block reference = height %d hash %s, want empty", certificate.BlockHeight, certificate.BlockHash.String())
	}
	if certificate.ConfirmedStake != 67 || certificate.ThresholdStake != 67 {
		t.Fatalf("QC stake = confirmed %d threshold %d, want 67/67", certificate.ConfirmedStake, certificate.ThresholdStake)
	}
}
