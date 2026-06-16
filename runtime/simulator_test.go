package runtime_test

import (
	"testing"

	stakeprogram "solana_golang/programs/stake"
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

	result, err := simulateWithDefaultPrograms(t, TransactionSimulationInput{
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

func TestTransactionSimulatorTransfersToMissingDestinationAccount(t *testing.T) {
	sourceKey, sourcePrivateKey := newSimulationSigner(t)
	destinationKey := newTestPublicKey(36)
	blockhash := newTestHash(37)
	transferLamportsValue := mustMinimumBalance(t, 0) + 100
	sourceLamports := mustMinimumBalance(t, 0) + LamportsPerSignature + transferLamportsValue + 100

	transferInstruction, err := NewTransferInstruction(TransferParams{Lamports: transferLamportsValue})
	systemInstruction := mustSystemInstructionBytes(t, transferInstruction, err)
	transaction := signedSimulationTransaction(t, []AccountMeta{
		{PublicKey: sourceKey, IsSigner: true, IsWritable: true},
		{PublicKey: destinationKey, IsSigner: false, IsWritable: true},
		{PublicKey: DefaultBuiltinProgramIDs.System, IsSigner: false, IsWritable: false},
	}, []PublicKey{sourceKey, destinationKey}, systemInstruction, blockhash, map[PublicKey][]byte{
		sourceKey: sourcePrivateKey,
	})

	result, err := simulateWithDefaultPrograms(t, TransactionSimulationInput{
		Transaction:    transaction,
		Accounts:       simulationAccounts(t, sourceKey, sourceLamports),
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, 2),
		CurrentSlot:    2,
	})
	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}
	if result.Status != TransactionStatusConfirmed {
		t.Fatalf("Status = %d, want confirmed: %v", result.Status, result.Error)
	}
	destinationWritten := findWrittenAccount(t, result.WrittenAccounts, destinationKey)
	if destinationWritten.Lamports != transferLamportsValue {
		t.Fatalf("destination lamports = %d, want %d", destinationWritten.Lamports, transferLamportsValue)
	}
}

func TestTransactionSimulatorRegistersValidatorWithMissingStakeAccount(t *testing.T) {
	stakerKey, stakerPrivateKey := newSimulationSigner(t)
	validatorKey := newTestPublicKey(38)
	consensusKey := newTestPublicKey(39)
	blockhash := newTestHash(40)
	stakeLamportsValue := uint64(10_000_000)
	currentEpoch := uint64(7)
	stakerLamports := mustMinimumBalance(t, 0) + LamportsPerSignature + stakeLamportsValue + 100

	stakeInstruction, err := stakeprogram.NewRegisterValidatorInstruction(consensusKey, "peer-223", 0, stakeLamportsValue)
	if err != nil {
		t.Fatalf("NewRegisterValidatorInstruction() error = %v", err)
	}
	instructionData, err := stakeInstruction.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	transaction := signedStakeSimulationTransaction(t, []AccountMeta{
		{PublicKey: stakerKey, IsSigner: true, IsWritable: true},
		{PublicKey: validatorKey, IsSigner: false, IsWritable: true},
		{PublicKey: DefaultBuiltinProgramIDs.Stake, IsSigner: false, IsWritable: false},
	}, []PublicKey{stakerKey, validatorKey}, instructionData, blockhash, map[PublicKey][]byte{
		stakerKey: stakerPrivateKey,
	})

	result, err := simulateWithDefaultPrograms(t, TransactionSimulationInput{
		Transaction:    transaction,
		Accounts:       simulationAccountsWithPrograms(t, stakerKey, stakerLamports, DefaultBuiltinProgramIDs.Stake),
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, 3),
		CurrentSlot:    3,
		CurrentEpoch:   currentEpoch,
	})
	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}
	if result.Status != TransactionStatusConfirmed {
		t.Fatalf("Status = %d, want confirmed: %v", result.Status, result.Error)
	}

	validatorWritten := findWrittenAccount(t, result.WrittenAccounts, validatorKey)
	if validatorWritten.Owner != DefaultBuiltinProgramIDs.Stake {
		t.Fatalf("validator owner = %s, want stake", validatorWritten.Owner.String())
	}
	if validatorWritten.Lamports != stakeLamportsValue {
		t.Fatalf("validator lamports = %d, want %d", validatorWritten.Lamports, stakeLamportsValue)
	}
	validatorState, err := stakeprogram.UnmarshalValidatorStateBinary(validatorWritten.Data)
	if err != nil {
		t.Fatalf("UnmarshalValidatorStateBinary() error = %v", err)
	}
	if validatorState.StakerAccount != stakerKey {
		t.Fatal("validator staker mismatch")
	}
	if validatorState.PendingStake != stakeLamportsValue {
		t.Fatalf("pending stake = %d, want %d", validatorState.PendingStake, stakeLamportsValue)
	}
	if validatorState.ActivationEpoch != currentEpoch+1 {
		t.Fatalf("activation epoch = %d, want %d", validatorState.ActivationEpoch, currentEpoch+1)
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

	result, err := simulateWithDefaultPrograms(t, TransactionSimulationInput{
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

	result, err := simulateWithDefaultPrograms(t, TransactionSimulationInput{
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

func signedStakeSimulationTransaction(t *testing.T, accounts []AccountMeta, instructionAccounts []PublicKey, instructionData []byte, blockhash Blockhash, privateKeys map[PublicKey][]byte) Transaction {
	t.Helper()

	accountIndexByKey, err := AccountIndexMap(accounts)
	if err != nil {
		t.Fatalf("AccountIndexMap() error = %v", err)
	}
	compiledInstruction, err := CompileInstruction(DefaultBuiltinProgramIDs.Stake, instructionAccounts, instructionData, accountIndexByKey)
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

func simulationAccountsWithPrograms(t *testing.T, firstAddress PublicKey, firstLamports uint64, programAddresses ...PublicKey) []AddressedAccount {
	t.Helper()

	accounts := []AddressedAccount{
		newSimulationAccount(t, firstAddress, firstLamports, DefaultBuiltinProgramIDs.System, false),
		newSimulationAccount(t, DefaultBuiltinProgramIDs.System, mustMinimumBalance(t, 0), DefaultBuiltinProgramIDs.NativeLoader, true),
	}
	for _, programAddress := range programAddresses {
		if programAddress == DefaultBuiltinProgramIDs.System {
			continue
		}
		accounts = append(accounts, newSimulationAccount(t, programAddress, mustMinimumBalance(t, 0), DefaultBuiltinProgramIDs.NativeLoader, true))
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
