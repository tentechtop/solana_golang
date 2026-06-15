package structure

import (
	"testing"

	"solana_golang/utils"
)

func TestTransactionSimulatorTransfersLamports(t *testing.T) {
	sourceKey, sourcePrivateKey := newSimulationSigner(t)
	destinationKey := newTestPublicKey(31)
	blockhash := newTestHash(32)
	transferLamportsValue := uint64(1000)
	sourceMinimum := mustMinimumBalance(t, 0)
	destinationMinimum := mustMinimumBalance(t, 0)
	sourceLamports := sourceMinimum + LamportsPerSignature + transferLamportsValue + 100
	destinationLamports := destinationMinimum

	transferInstruction, err := NewTransferInstruction(TransferParams{Lamports: transferLamportsValue})
	systemInstruction := mustSystemInstructionBytes(t, transferInstruction, err)
	transaction := signedSimulationTransaction(t, []AccountMeta{
		{PublicKey: sourceKey, IsSigner: true, IsWritable: true},
		{PublicKey: destinationKey, IsSigner: false, IsWritable: true},
		{PublicKey: DefaultBuiltinProgramIDs.System, IsSigner: false, IsWritable: false},
	}, []PublicKey{sourceKey, destinationKey}, systemInstruction, blockhash, map[PublicKey][]byte{
		sourceKey: sourcePrivateKey,
	})

	result, err := TransactionSimulator{}.Simulate(TransactionSimulationInput{
		Transaction:    transaction,
		Accounts:       simulationAccounts(t, sourceKey, sourceLamports, destinationKey, destinationLamports),
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, 1),
		CurrentSlot:    1,
	})
	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}
	if result.Status != TransactionStatusConfirmed {
		t.Fatalf("Status = %d, want confirmed: %v", result.Status, result.Error)
	}
	if result.Error != nil {
		t.Fatalf("Error = %v, want nil", result.Error)
	}

	sourceWritten := findWrittenAccount(t, result.WrittenAccounts, sourceKey)
	destinationWritten := findWrittenAccount(t, result.WrittenAccounts, destinationKey)
	if sourceWritten.Lamports != sourceLamports-LamportsPerSignature-transferLamportsValue {
		t.Fatalf("source lamports = %d, want %d", sourceWritten.Lamports, sourceLamports-LamportsPerSignature-transferLamportsValue)
	}
	if destinationWritten.Lamports != destinationLamports+transferLamportsValue {
		t.Fatalf("destination lamports = %d, want %d", destinationWritten.Lamports, destinationLamports+transferLamportsValue)
	}
	if result.FeeDetails.TotalFee != LamportsPerSignature {
		t.Fatalf("TotalFee = %d, want %d", result.FeeDetails.TotalFee, LamportsPerSignature)
	}
}

func TestTransactionSimulatorCreatesAccount(t *testing.T) {
	payerKey, payerPrivateKey := newSimulationSigner(t)
	newAccountKey, newAccountPrivateKey := newSimulationSigner(t)
	ownerKey := newTestPublicKey(41)
	blockhash := newTestHash(42)
	space := uint64(8)
	newAccountLamports := mustMinimumBalance(t, int(space))
	payerMinimum := mustMinimumBalance(t, 0)
	createAccountFee := LamportsPerSignature * 2
	payerLamports := payerMinimum + createAccountFee + newAccountLamports + 100

	createAccountInstruction, err := NewCreateAccountInstruction(CreateAccountParams{
		Lamports: newAccountLamports,
		Space:    space,
		Owner:    ownerKey,
	})
	systemInstruction := mustSystemInstructionBytes(t, createAccountInstruction, err)
	transaction := signedSimulationTransaction(t, []AccountMeta{
		{PublicKey: payerKey, IsSigner: true, IsWritable: true},
		{PublicKey: newAccountKey, IsSigner: true, IsWritable: true},
		{PublicKey: DefaultBuiltinProgramIDs.System, IsSigner: false, IsWritable: false},
	}, []PublicKey{payerKey, newAccountKey}, systemInstruction, blockhash, map[PublicKey][]byte{
		payerKey:      payerPrivateKey,
		newAccountKey: newAccountPrivateKey,
	})

	result, err := TransactionSimulator{}.Simulate(TransactionSimulationInput{
		Transaction:    transaction,
		Accounts:       simulationAccounts(t, payerKey, payerLamports),
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, 10),
		CurrentSlot:    10,
	})
	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}
	if result.Status != TransactionStatusConfirmed {
		t.Fatalf("Status = %d, want confirmed: %v", result.Status, result.Error)
	}

	payerWritten := findWrittenAccount(t, result.WrittenAccounts, payerKey)
	newAccountWritten := findWrittenAccount(t, result.WrittenAccounts, newAccountKey)
	if payerWritten.Lamports != payerLamports-createAccountFee-newAccountLamports {
		t.Fatalf("payer lamports = %d, want %d", payerWritten.Lamports, payerLamports-createAccountFee-newAccountLamports)
	}
	if newAccountWritten.Lamports != newAccountLamports {
		t.Fatalf("new account lamports = %d, want %d", newAccountWritten.Lamports, newAccountLamports)
	}
	if newAccountWritten.Owner != ownerKey {
		t.Fatal("new account owner mismatch")
	}
	if newAccountWritten.DataLen() != int(space) {
		t.Fatalf("new account data len = %d, want %d", newAccountWritten.DataLen(), space)
	}
}

func TestTransactionSimulatorRejectsInsufficientFeeReserve(t *testing.T) {
	sourceKey, sourcePrivateKey := newSimulationSigner(t)
	destinationKey := newTestPublicKey(51)
	blockhash := newTestHash(52)
	sourceMinimum := mustMinimumBalance(t, 0)
	destinationMinimum := mustMinimumBalance(t, 0)
	sourceLamports := sourceMinimum + LamportsPerSignature - 1

	transferInstruction, err := NewTransferInstruction(TransferParams{Lamports: 1})
	systemInstruction := mustSystemInstructionBytes(t, transferInstruction, err)
	transaction := signedSimulationTransaction(t, []AccountMeta{
		{PublicKey: sourceKey, IsSigner: true, IsWritable: true},
		{PublicKey: destinationKey, IsSigner: false, IsWritable: true},
		{PublicKey: DefaultBuiltinProgramIDs.System, IsSigner: false, IsWritable: false},
	}, []PublicKey{sourceKey, destinationKey}, systemInstruction, blockhash, map[PublicKey][]byte{
		sourceKey: sourcePrivateKey,
	})

	result, err := TransactionSimulator{}.Simulate(TransactionSimulationInput{
		Transaction:    transaction,
		Accounts:       simulationAccounts(t, sourceKey, sourceLamports, destinationKey, destinationMinimum),
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, 20),
		CurrentSlot:    20,
	})
	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}
	if result.Status != TransactionStatusFailed {
		t.Fatalf("Status = %d, want failed", result.Status)
	}
	if result.Error == nil || result.Error.Code != TransactionErrorCodeInsufficientFundsForFee {
		t.Fatalf("Error = %+v, want insufficient funds for fee", result.Error)
	}
	sourceWritten := findWrittenAccount(t, result.WrittenAccounts, sourceKey)
	if sourceWritten.Lamports != sourceLamports {
		t.Fatalf("source lamports = %d, want unchanged %d", sourceWritten.Lamports, sourceLamports)
	}
}

func newSimulationSigner(t *testing.T) (PublicKey, []byte) {
	t.Helper()

	publicKeyBytes, privateKey, err := utils.GenerateEd25519KeyPairBytes()
	if err != nil {
		t.Fatalf("GenerateEd25519KeyPairBytes() error = %v", err)
	}
	publicKey, err := NewPublicKey(publicKeyBytes)
	if err != nil {
		t.Fatalf("NewPublicKey() error = %v", err)
	}
	return publicKey, privateKey
}

func signedSimulationTransaction(t *testing.T, accounts []AccountMeta, instructionAccounts []PublicKey, instructionData []byte, blockhash Blockhash, privateKeys map[PublicKey][]byte) Transaction {
	t.Helper()

	accountIndexByKey, err := AccountIndexMap(accounts)
	if err != nil {
		t.Fatalf("AccountIndexMap() error = %v", err)
	}
	compiledInstruction, err := CompileInstruction(DefaultBuiltinProgramIDs.System, instructionAccounts, instructionData, accountIndexByKey)
	if err != nil {
		t.Fatalf("CompileInstruction() error = %v", err)
	}
	transaction := Transaction{
		Accounts:        accounts,
		Instructions:    []CompiledInstruction{compiledInstruction},
		RecentBlockhash: blockhash,
	}
	signedTransaction, err := transaction.Sign(privateKeys)
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	return signedTransaction
}

func mustSystemInstructionBytes(t *testing.T, instruction SystemInstruction, err error) []byte {
	t.Helper()

	if err != nil {
		t.Fatalf("build system instruction error = %v", err)
	}
	encoded, err := instruction.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	return encoded
}

func simulationAccounts(t *testing.T, firstAddress PublicKey, firstLamports uint64, rest ...interface{}) []AddressedAccount {
	t.Helper()

	accounts := []AddressedAccount{
		newSimulationAccount(t, firstAddress, firstLamports, DefaultBuiltinProgramIDs.System, false),
		newSimulationAccount(t, DefaultBuiltinProgramIDs.System, mustMinimumBalance(t, 0), DefaultBuiltinProgramIDs.NativeLoader, true),
	}
	for index := 0; index < len(rest); index += 2 {
		address, ok := rest[index].(PublicKey)
		if !ok {
			t.Fatalf("rest[%d] is %T, want PublicKey", index, rest[index])
		}
		lamports, ok := rest[index+1].(uint64)
		if !ok {
			t.Fatalf("rest[%d] is %T, want uint64", index+1, rest[index+1])
		}
		accounts = append(accounts, newSimulationAccount(t, address, lamports, DefaultBuiltinProgramIDs.System, false))
	}
	return accounts
}

func newSimulationAccount(t *testing.T, address PublicKey, lamports uint64, owner PublicKey, executable bool) AddressedAccount {
	t.Helper()

	account, err := NewAccount(lamports, nil, owner, executable, 0)
	if err != nil {
		t.Fatalf("NewAccount() error = %v", err)
	}
	return AddressedAccount{Address: address, Account: account}
}

func newSimulationBlockhashQueue(t *testing.T, blockhash Blockhash, slot uint64) BlockhashQueue {
	t.Helper()

	queue := NewBlockhashQueue(MaxRecentBlockhashAgeSlots)
	if err := queue.Add(RecentBlockhashEntry{
		Blockhash:     blockhash,
		Slot:          slot,
		FeeCalculator: DefaultFeeCalculator(),
	}); err != nil {
		t.Fatalf("BlockhashQueue.Add() error = %v", err)
	}
	return queue
}

func findWrittenAccount(t *testing.T, writtenAccounts []AddressedAccount, address PublicKey) Account {
	t.Helper()

	for _, account := range writtenAccounts {
		if account.Address == address {
			return account.Account
		}
	}
	t.Fatalf("written account %s not found", address.String())
	return Account{}
}
