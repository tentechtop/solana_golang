package structure

import (
	"errors"
	"testing"
)

func TestBuiltinProgramIDsAllowSystemProgramPublicKey(t *testing.T) {
	if err := DefaultBuiltinProgramIDs.Validate(); err != nil {
		t.Fatalf("BuiltinProgramIDs.Validate() error = %v", err)
	}
	if DefaultBuiltinProgramIDs.System != (PublicKey{}) {
		t.Fatal("system program id should use the all-zero public key")
	}

	accountMeta, err := NewAccountMeta(DefaultBuiltinProgramIDs.System, false, false)
	if err != nil {
		t.Fatalf("NewAccountMeta(system program) error = %v", err)
	}
	if accountMeta.PublicKey != DefaultBuiltinProgramIDs.System {
		t.Fatal("NewAccountMeta(system program) changed public key")
	}
}

func TestInstructionCompileUsesAccountIndexMap(t *testing.T) {
	payerKey := newTestPublicKey(1)
	recipientKey := newTestPublicKey(2)
	programKey := DefaultBuiltinProgramIDs.System
	accounts := []AccountMeta{
		{PublicKey: payerKey, IsSigner: true, IsWritable: true},
		{PublicKey: recipientKey, IsSigner: false, IsWritable: true},
		{PublicKey: programKey, IsSigner: false, IsWritable: false},
	}
	indexMap, err := AccountIndexMap(accounts)
	if err != nil {
		t.Fatalf("AccountIndexMap() error = %v", err)
	}

	instruction, err := NewInstruction(programKey, accounts[:2], []byte{1, 2})
	if err != nil {
		t.Fatalf("NewInstruction() error = %v", err)
	}
	compiled, err := instruction.Compile(indexMap)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	if compiled.ProgramIDIndex != 2 {
		t.Fatalf("ProgramIDIndex = %d, want 2", compiled.ProgramIDIndex)
	}
	if len(compiled.AccountIndexes) != 2 || compiled.AccountIndexes[0] != 0 || compiled.AccountIndexes[1] != 1 {
		t.Fatalf("AccountIndexes = %v, want [0 1]", compiled.AccountIndexes)
	}
}

func TestSystemInstructionRoundTripAndRejectsLargeSpace(t *testing.T) {
	instruction, err := NewCreateAccountInstruction(CreateAccountParams{
		Lamports: 100,
		Space:    8,
		Owner:    newTestPublicKey(3),
	})
	if err != nil {
		t.Fatalf("NewCreateAccountInstruction() error = %v", err)
	}

	encoded, err := instruction.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	decoded, err := UnmarshalSystemInstructionBinary(encoded)
	if err != nil {
		t.Fatalf("UnmarshalSystemInstructionBinary() error = %v", err)
	}
	if decoded.Type != SystemInstructionCreateAccount {
		t.Fatalf("Type = %d, want create account", decoded.Type)
	}
	if decoded.CreateAccount == nil || decoded.CreateAccount.Space != 8 {
		t.Fatalf("CreateAccount params = %+v, want space 8", decoded.CreateAccount)
	}

	_, err = NewAllocateInstruction(AllocateParams{Space: uint64(MaxAccountDataSize) + 1})
	if !errors.Is(err, ErrAccountDataTooLarge) {
		t.Fatalf("NewAllocateInstruction(large) error = %v, want ErrAccountDataTooLarge", err)
	}
}

func TestFeeCalculatorSplitsBaseAndPriorityFees(t *testing.T) {
	limits := DefaultComputeBudgetLimits()
	limits.ComputeUnitPriceMicroLamports = 1

	feeDetails, err := DefaultFeeCalculator().Calculate(2, limits)
	if err != nil {
		t.Fatalf("Calculate() error = %v", err)
	}

	if feeDetails.BaseFee != 10000 {
		t.Fatalf("BaseFee = %d, want 10000", feeDetails.BaseFee)
	}
	if feeDetails.PrioritizationFee != 2 {
		t.Fatalf("PrioritizationFee = %d, want 2", feeDetails.PrioritizationFee)
	}
	if feeDetails.TotalFee != 10002 {
		t.Fatalf("TotalFee = %d, want 10002", feeDetails.TotalFee)
	}
	if feeDetails.BurnedFee != 0 || feeDetails.ValidatorFee != 10002 {
		t.Fatalf("fee split burned=%d validator=%d, want 0 and 10002", feeDetails.BurnedFee, feeDetails.ValidatorFee)
	}
}

func TestResolvedMessageAndLoadedTransactionValidate(t *testing.T) {
	transaction := newTestVersionedTransaction()
	loadedAddresses := LoadedAddresses{Writable: []PublicKey{newTestPublicKey(2)}}
	resolvedMessage, err := NewResolvedMessage(transaction.Message, loadedAddresses)
	if err != nil {
		t.Fatalf("NewResolvedMessage() error = %v", err)
	}
	if len(resolvedMessage.AccountKeys) != 2 {
		t.Fatalf("AccountKeys length = %d, want 2", len(resolvedMessage.AccountKeys))
	}

	feeDetails, err := DefaultFeeCalculator().Calculate(len(transaction.Signatures), DefaultComputeBudgetLimits())
	if err != nil {
		t.Fatalf("Calculate() error = %v", err)
	}
	loadedAccounts := []LoadedAccount{
		{
			Address:    resolvedMessage.AccountKeys[0],
			Account:    newRentExemptTestAccount(t, []byte{1}),
			IsSigner:   true,
			IsWritable: true,
		},
		{
			Address:    resolvedMessage.AccountKeys[1],
			Account:    newRentExemptTestAccount(t, []byte{2}),
			IsWritable: true,
		},
	}

	loadedTransaction, err := NewLoadedTransaction(transaction, resolvedMessage, loadedAccounts, feeDetails)
	if err != nil {
		t.Fatalf("NewLoadedTransaction() error = %v", err)
	}
	if loadedTransaction.FeePayer != resolvedMessage.AccountKeys[0] {
		t.Fatal("NewLoadedTransaction() did not set fee payer to first account")
	}

	_, err = NewResolvedMessage(transaction.Message, LoadedAddresses{Writable: []PublicKey{transaction.Message.AccountKeys[0]}})
	if !errors.Is(err, ErrInvalidLoadedTransaction) {
		t.Fatalf("NewResolvedMessage(duplicate) error = %v, want ErrInvalidLoadedTransaction", err)
	}
}

func TestTransactionExecutionResultValidationAndMeta(t *testing.T) {
	transactionError := &TransactionError{
		Code: TransactionErrorCodeInstructionError,
		InstructionError: &InstructionError{
			InstructionIndex: 0,
			Code:             InstructionErrorCodeInvalidInstructionData,
			Message:          "bad instruction data",
		},
	}
	result := TransactionExecutionResult{
		Status:       TransactionStatusFailed,
		Error:        transactionError,
		FeeDetails:   FeeDetails{TotalFee: 5000},
		PreBalances:  []uint64{10},
		PostBalances: []uint64{5},
		LogMessages:  []string{"program failed"},
		ReturnData: &TransactionReturnData{
			ProgramID: newTestPublicKey(7),
			Data:      []byte{1},
		},
		LoadedAddresses: LoadedAddresses{Readonly: []PublicKey{newTestPublicKey(8)}},
		WrittenAccounts: []AddressedAccount{
			{
				Address: newTestPublicKey(9),
				Account: newRentExemptTestAccount(t, []byte{3}),
			},
		},
	}

	if err := result.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	meta := result.ToStatusMeta()
	if meta.Err == "" {
		t.Fatal("ToStatusMeta() did not copy error")
	}
	if meta.Fee != 5000 {
		t.Fatalf("meta.Fee = %d, want 5000", meta.Fee)
	}
	if len(meta.LoadedAddresses.Readonly) != 1 {
		t.Fatalf("LoadedAddresses readonly length = %d, want 1", len(meta.LoadedAddresses.Readonly))
	}
}

func TestSysvarModelsValidate(t *testing.T) {
	rentSysvar := DefaultRentSysvar()
	minimumBalance, err := rentSysvar.MinimumBalance(0)
	if err != nil {
		t.Fatalf("MinimumBalance() error = %v", err)
	}
	if minimumBalance != RentAccountStorageOverheadBytes*RentLamportsPerByteYear*RentExemptionThresholdYears {
		t.Fatalf("MinimumBalance() = %d, want default zero-data rent", minimumBalance)
	}

	instruction, err := NewInstruction(DefaultBuiltinProgramIDs.System, nil, []byte{1})
	if err != nil {
		t.Fatalf("NewInstruction() error = %v", err)
	}
	instructionsSysvar := InstructionsSysvar{Instructions: []Instruction{instruction}, CurrentIndex: 0}
	currentInstruction, err := instructionsSysvar.CurrentInstruction()
	if err != nil {
		t.Fatalf("CurrentInstruction() error = %v", err)
	}
	if currentInstruction.ProgramID != DefaultBuiltinProgramIDs.System {
		t.Fatal("CurrentInstruction() returned wrong instruction")
	}
}

func TestBlockhashQueueAndStatusCache(t *testing.T) {
	queue := NewBlockhashQueue(MaxRecentBlockhashAgeSlots)
	oldEntry := RecentBlockhashEntry{
		Blockhash:     newTestHash(10),
		Slot:          10,
		FeeCalculator: DefaultFeeCalculator(),
	}
	if err := queue.Add(oldEntry); err != nil {
		t.Fatalf("Add(old) error = %v", err)
	}
	newEntry := RecentBlockhashEntry{
		Blockhash:     newTestHash(11),
		Slot:          161,
		FeeCalculator: DefaultFeeCalculator(),
	}
	if err := queue.Add(newEntry); err != nil {
		t.Fatalf("Add(new) error = %v", err)
	}

	if queue.IsRecent(oldEntry.Blockhash, 161) {
		t.Fatal("old blockhash should not be recent")
	}
	if !queue.IsRecent(newEntry.Blockhash, 161) {
		t.Fatal("new blockhash should be recent")
	}

	statusEntry := StatusCacheEntry{
		TransactionID: newTestSignature(12),
		MessageHash:   newTestHash(13),
		Slot:          161,
		Status:        TransactionStatusConfirmed,
	}
	if err := statusEntry.Validate(); err != nil {
		t.Fatalf("StatusCacheEntry.Validate() error = %v", err)
	}
	statusEntry.Status = TransactionStatusPending
	if err := statusEntry.Validate(); !errors.Is(err, ErrInvalidStatusCache) {
		t.Fatalf("StatusCacheEntry.Validate(pending) error = %v, want ErrInvalidStatusCache", err)
	}
}
