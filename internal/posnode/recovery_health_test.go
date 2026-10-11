package posnode

import (
	"context"
	"testing"
)

func TestRecoveryHealthDoesNotAdvertiseFinalizableUserTransactions(t *testing.T) {
	node := newConsensusStatusTestNode(t)
	node.ledger.SetFinalitySuspended(true)
	health, err := node.GetHealth(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if health.OK || health.TransactionSubmissionEnabled || health.TransactionSubmissionReason != "finality_suspended" {
		t.Fatalf("recovery reported a usable transaction endpoint: %+v", health)
	}
}
