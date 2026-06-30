package blockchain

import (
	"bytes"
	"testing"

	"solana_golang/structure"
	svm "solana_golang/vm"
)

func TestNewDeployContractTransactionCreatesSignedDeployment(t *testing.T) {
	payer := testDeployKeyPair(t, 1)
	program := testDeployKeyPair(t, 2)
	code, err := svm.BuildRegisterProgramCode()
	if err != nil {
		t.Fatalf("BuildRegisterProgramCode() error = %v", err)
	}
	bytecode, err := svm.EncodeRegisterBytecode(code, nil)
	if err != nil {
		t.Fatalf("EncodeRegisterBytecode() error = %v", err)
	}
	blockhash := testDeployHash(3)

	transaction, err := NewDeployContractTransaction(payer, program, bytecode, 123, blockhash)
	if err != nil {
		t.Fatalf("NewDeployContractTransaction() error = %v", err)
	}
	if transaction.RequiredSignatureCount() != 2 {
		t.Fatalf("RequiredSignatureCount() = %d, want 2", transaction.RequiredSignatureCount())
	}
	if len(transaction.Instructions) != 2 {
		t.Fatalf("instructions length = %d, want 2", len(transaction.Instructions))
	}
	if transaction.Instructions[0].ProgramIDIndex != 2 || transaction.Instructions[1].ProgramIDIndex != 3 {
		t.Fatalf("program indexes = %d/%d, want 2/3", transaction.Instructions[0].ProgramIDIndex, transaction.Instructions[1].ProgramIDIndex)
	}
	valid, err := transaction.HasValidSignatures()
	if err != nil {
		t.Fatalf("HasValidSignatures() error = %v", err)
	}
	if !valid {
		t.Fatal("deploy transaction signatures are invalid")
	}
	if !bytes.Equal(transaction.RecentBlockhash[:], blockhash[:]) {
		t.Fatal("recent blockhash mismatch")
	}
}

func TestNewBootstrapFungibleAssetTransactionCreatesSignedAssetInitialization(t *testing.T) {
	payer := testDeployKeyPair(t, 4)
	program := testDeployKeyPair(t, 5)
	blockhash := testDeployHash(6)

	transaction, err := NewBootstrapFungibleAssetTransaction(FungibleAssetBootstrapParams{
		Payer:           payer,
		Program:         program.PublicKey,
		Name:            "泡泡币",
		Symbol:          "POP",
		Decimals:        6,
		InitialSupply:   1_000_000_000_000_000,
		RecentBlockhash: blockhash,
	})
	if err != nil {
		t.Fatalf("NewBootstrapFungibleAssetTransaction() error = %v", err)
	}
	if transaction.RequiredSignatureCount() != 3 {
		t.Fatalf("RequiredSignatureCount() = %d, want 3", transaction.RequiredSignatureCount())
	}
	if len(transaction.Instructions) != 5 {
		t.Fatalf("instructions length = %d, want 5", len(transaction.Instructions))
	}
	if transaction.Instructions[0].ProgramIDIndex != 3 || transaction.Instructions[2].ProgramIDIndex != 4 {
		t.Fatalf("program indexes = %d/%d, want system/program", transaction.Instructions[0].ProgramIDIndex, transaction.Instructions[2].ProgramIDIndex)
	}
	valid, err := transaction.HasValidSignatures()
	if err != nil {
		t.Fatalf("HasValidSignatures() error = %v", err)
	}
	if !valid {
		t.Fatal("asset bootstrap transaction signatures are invalid")
	}
	assetAccounts, err := DeriveFungibleAssetBootstrapAccounts(program.PublicKey, payer.PublicKey)
	if err != nil {
		t.Fatalf("DeriveFungibleAssetBootstrapAccounts() error = %v", err)
	}
	if transaction.Accounts[1].PublicKey != assetAccounts.Mint.PublicKey || transaction.Accounts[2].PublicKey != assetAccounts.Balance.PublicKey {
		t.Fatal("asset state accounts do not match canonical derivation")
	}
}

func testDeployKeyPair(t *testing.T, seedByte byte) structure.SolanaKeyPair {
	t.Helper()
	seed := bytes.Repeat([]byte{seedByte}, structure.SolanaPrivateKeySeedSize)
	keyPair, err := structure.KeyPairFromSeed(seed)
	if err != nil {
		t.Fatalf("KeyPairFromSeed() error = %v", err)
	}
	return keyPair
}

func testDeployHash(seedByte byte) structure.Hash {
	var hash structure.Hash
	for index := range hash {
		hash[index] = seedByte + byte(index)
	}
	return hash
}
