package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestApplyRPCURLOverrideKeepsManifestWhenEmpty(t *testing.T) {
	plan := manifest{RPCURLs: []string{"http://127.0.0.1:8899/"}}
	result, err := applyRPCURLOverride(plan, " ")
	if err != nil {
		t.Fatalf("applyRPCURLOverride() error = %v", err)
	}
	if len(result.RPCURLs) != 1 || result.RPCURLs[0] != plan.RPCURLs[0] {
		t.Fatalf("RPCURLs = %#v, want %#v", result.RPCURLs, plan.RPCURLs)
	}
}

func TestApplyRPCURLOverrideRejectsUnsupportedScheme(t *testing.T) {
	if _, err := applyRPCURLOverride(manifest{}, "tcp://127.0.0.1:8899"); err == nil {
		t.Fatal("applyRPCURLOverride() error = nil, want unsupported scheme error")
	}
}

func TestApplyRPCURLOverrideUsesPublicGateway(t *testing.T) {
	result, err := applyRPCURLOverride(manifest{RPCURLs: []string{"http://127.0.0.1:8899/"}}, " http://101.35.87.31:8899/ ")
	if err != nil {
		t.Fatalf("applyRPCURLOverride() error = %v", err)
	}
	if len(result.RPCURLs) != 1 || result.RPCURLs[0] != "http://101.35.87.31:8899/" {
		t.Fatalf("RPCURLs = %#v, want public gateway only", result.RPCURLs)
	}
}

func TestBucketStressError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "deadline", err: context.DeadlineExceeded, want: "context_deadline_exceeded"},
		{name: "blockhash", err: errors.New("recent blockhash is not valid"), want: "recent_blockhash_invalid"},
		{name: "balance", err: errors.New("insufficient balance"), want: "insufficient_balance"},
		{name: "stake", err: errors.New("stake account already exists"), want: "stake_state_invalid"},
		{name: "rpc", err: errors.New("rpc sendTransaction: no connected validator peer"), want: "rpc_or_transport"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := bucketStressError(test.err); got != test.want {
				t.Fatalf("bucketStressError() = %s, want %s", got, test.want)
			}
		})
	}
}

func TestFixedTransferParamsRotatesSeedsAndAmount(t *testing.T) {
	source, destination, lamports, err := fixedTransferParams([]string{"user-1", "user-2", "user-3"}, 4)
	if err != nil {
		t.Fatalf("fixedTransferParams() error = %v", err)
	}
	if source != "user-2" || destination != "user-1" {
		t.Fatalf("source=%s destination=%s, want rotated distinct seeds", source, destination)
	}
	if lamports != 1_004 {
		t.Fatalf("lamports = %d, want 1004", lamports)
	}
}

func TestFixedTransferParamsRejectsSingleSeed(t *testing.T) {
	if _, _, _, err := fixedTransferParams([]string{"user-1"}, 0); err == nil {
		t.Fatal("fixedTransferParams() error = nil, want seed count error")
	}
}

func TestOperationStatsErrorCopyIsIndependent(t *testing.T) {
	var stats operationStats
	recordResult(&stats, errors.New("mempool is full"))
	copied := stats.copyErrors()
	if copied["mempool_rejected"] != 1 {
		t.Fatalf("mempool_rejected = %d, want 1", copied["mempool_rejected"])
	}
	samples := stats.copyErrorSamples()
	if samples["mempool_rejected"] != "mempool is full" {
		t.Fatalf("mempool_rejected sample = %q, want %q", samples["mempool_rejected"], "mempool is full")
	}
	copied["mempool_rejected"] = 99
	if latest := stats.copyErrors()["mempool_rejected"]; latest != 1 {
		t.Fatalf("stored mempool_rejected = %d, want 1", latest)
	}
	samples["mempool_rejected"] = "changed"
	if latest := stats.copyErrorSamples()["mempool_rejected"]; latest != "mempool is full" {
		t.Fatalf("stored mempool_rejected sample = %q, want original", latest)
	}
}

func TestRPCErrorDetailIncludesData(t *testing.T) {
	rpcError := &rpcError{
		Message: "internal error",
		Data:    json.RawMessage(`"send transaction: preflight failed"`),
	}
	if got := rpcError.detail(); got != `internal error data="send transaction: preflight failed"` {
		t.Fatalf("detail() = %q", got)
	}
}
