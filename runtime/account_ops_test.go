package runtime

import (
	"testing"

	"solana_golang/structure"
)

func TestTransferLamportsCreatesDestinationAccount(t *testing.T) {
	sourceAddress := testPublicKey(1)
	destinationAddress := testPublicKey(2)
	sourceAccount, err := structure.NewAccount(1_000_000_000, nil, structure.DefaultBuiltinProgramIDs.System, false, 0)
	if err != nil {
		t.Fatalf("NewAccount(source) error = %v", err)
	}
	accounts := map[structure.PublicKey]structure.Account{sourceAddress: sourceAccount}
	if err := TransferLamports(sourceAddress, destinationAddress, 50_000_000, accounts, structure.DefaultRentConfig); err != nil {
		t.Fatalf("TransferLamports() error = %v", err)
	}
	if accounts[sourceAddress].Lamports != 950_000_000 {
		t.Fatalf("source lamports = %d, want 950000000", accounts[sourceAddress].Lamports)
	}
	destinationAccount, exists := accounts[destinationAddress]
	if !exists {
		t.Fatal("destination account was not created")
	}
	if destinationAccount.Lamports != 50_000_000 {
		t.Fatalf("destination lamports = %d, want 50000000", destinationAccount.Lamports)
	}
	if destinationAccount.Owner != structure.DefaultBuiltinProgramIDs.System {
		t.Fatal("destination owner is not system program")
	}
}

func testPublicKey(seed byte) structure.PublicKey {
	value := make([]byte, structure.PublicKeySize)
	for index := range value {
		value[index] = seed
	}
	key, err := structure.NewPublicKey(value)
	if err != nil {
		panic(err)
	}
	return key
}
