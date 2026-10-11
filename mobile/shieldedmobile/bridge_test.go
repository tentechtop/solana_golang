package shieldedmobile

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	shieldedprogram "github.com/tentechtop/ordexus-core/programs/shielded"
	"github.com/tentechtop/ordexus-core/structure"
)

func TestDeriveWalletIsDeterministicAndDomainSeparated(t *testing.T) {
	firstJSON, err := DeriveWallet("able acid agent anchor april asset balance beacon binary bridge budget canvas")
	if err != nil {
		t.Fatalf("DeriveWallet(first) error = %v", err)
	}
	secondJSON, err := DeriveWallet("  able acid agent anchor april asset balance beacon binary bridge budget canvas  ")
	if err != nil {
		t.Fatalf("DeriveWallet(second) error = %v", err)
	}
	if firstJSON != secondJSON {
		t.Fatal("DeriveWallet() changed after surrounding whitespace normalization")
	}
	response := walletMaterialResponse{}
	if err := json.Unmarshal([]byte(firstJSON), &response); err != nil {
		t.Fatalf("decode wallet response: %v", err)
	}
	if !strings.HasPrefix(response.Address, "zs2_") || response.Version != bridgeVersion {
		t.Fatalf("wallet response = %+v", response)
	}
}

func TestBuildDepositAndScanTransactionLocally(t *testing.T) {
	const signingSeed = "able acid agent anchor april asset balance beacon binary bridge budget canvas"
	poolState, err := shieldedprogram.DefaultPoolState()
	if err != nil {
		t.Fatalf("DefaultPoolState() error = %v", err)
	}
	poolData, err := poolState.MarshalBinary()
	if err != nil {
		t.Fatalf("pool MarshalBinary() error = %v", err)
	}
	blockhash := structure.Hash{1, 2, 3, 4}
	request, err := json.Marshal(buildDepositRequest{
		AmountLamports:  "2500000",
		PoolDataBase64:  base64.StdEncoding.EncodeToString(poolData),
		RecentBlockhash: blockhash.String(),
		SigningSeed:     signingSeed,
	})
	if err != nil {
		t.Fatalf("encode deposit request: %v", err)
	}
	resultJSON, err := BuildDeposit(string(request))
	if err != nil {
		t.Fatalf("BuildDeposit() error = %v", err)
	}
	result := buildTransactionResponse{}
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		t.Fatalf("decode deposit response: %v", err)
	}
	if len(result.CreatedNotes) != 1 || result.CreatedNotes[0].ValueLamports != "2500000" || result.TransactionID == "" {
		t.Fatalf("deposit response = %+v", result)
	}
	wire, err := base64.StdEncoding.DecodeString(result.TransactionBase64)
	if err != nil {
		t.Fatalf("decode transaction: %v", err)
	}
	transaction, err := structure.UnmarshalTransactionBinary(wire)
	if err != nil {
		t.Fatalf("UnmarshalTransactionBinary() error = %v", err)
	}
	if err := transaction.Validate(); err != nil {
		t.Fatalf("transaction Validate() error = %v", err)
	}

	scanRequest, err := json.Marshal(scanTransactionRequest{
		SigningSeed:       signingSeed,
		StartPosition:     "0",
		TransactionBase64: result.TransactionBase64,
	})
	if err != nil {
		t.Fatalf("encode scan request: %v", err)
	}
	scanJSON, err := ScanTransaction(string(scanRequest))
	if err != nil {
		t.Fatalf("ScanTransaction() error = %v", err)
	}
	scan := scanTransactionResponse{}
	if err := json.Unmarshal([]byte(scanJSON), &scan); err != nil {
		t.Fatalf("decode scan response: %v", err)
	}
	if scan.NextPosition != "1" || len(scan.CommitmentsBase64) != 1 || len(scan.OwnedNotes) != 1 {
		t.Fatalf("scan response = %+v", scan)
	}
	if scan.OwnedNotes[0].ValueLamports != "2500000" || scan.OwnedNotes[0].NullifierBase64 == "" {
		t.Fatalf("owned note = %+v", scan.OwnedNotes[0])
	}
}
