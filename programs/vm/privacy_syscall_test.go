package vmprogram

import (
	"testing"

	"solana_golang/runtime"
	"solana_golang/structure"
	svm "solana_golang/vm"
)

func TestProgramDispatchesPrivacyDepositThroughSyscall(t *testing.T) {
	sourceKey := testPublicKey(11)
	stateKey := testPublicKey(12)
	vmProgramKey := testPublicKey(13)
	amount := uint64(700)
	commitment := testHash(14)
	context := testPrivacyVMContext(t, sourceKey, stateKey, vmProgramKey, 1, testPrivacyDepositData(t, sourceKey, amount, commitment))

	if err := NewProgram(structure.DefaultBuiltinProgramIDs.BPFLoader, svm.Runtime{}).Execute(context); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got := context.Accounts[sourceKey].Lamports; got != 2_000_000_000-amount {
		t.Fatalf("source lamports = %d, want %d", got, 2_000_000_000-amount)
	}
	if got := context.Accounts[stateKey].Lamports; got != 2_000_000_000+amount {
		t.Fatalf("state lamports = %d, want %d", got, 2_000_000_000+amount)
	}
	state, err := structure.UnmarshalPrivacyStateBinary(context.Accounts[stateKey].Data)
	if err != nil {
		t.Fatalf("UnmarshalPrivacyStateBinary() error = %v", err)
	}
	if len(state.Notes) != 1 || state.Notes[0].Commitment != commitment || state.Notes[0].Amount != amount {
		t.Fatalf("privacy notes = %+v, want one deposited note", state.Notes)
	}
}

func TestProgramRollsBackPrivacySyscallFailure(t *testing.T) {
	sourceKey := testPublicKey(21)
	stateKey := testPublicKey(22)
	vmProgramKey := testPublicKey(23)
	amount := uint64(500)
	context := testPrivacyVMContext(t, sourceKey, stateKey, vmProgramKey, 0, testPrivacyDepositData(t, sourceKey, amount, testHash(24)))

	if err := NewProgram(structure.DefaultBuiltinProgramIDs.BPFLoader, svm.Runtime{}).Execute(context); err == nil {
		t.Fatal("Execute() error = nil, want missing signer failure")
	}
	if got := context.Accounts[sourceKey].Lamports; got != 2_000_000_000 {
		t.Fatalf("source lamports = %d, want unchanged", got)
	}
	if got := context.Accounts[stateKey].Lamports; got != 2_000_000_000 {
		t.Fatalf("state lamports = %d, want unchanged", got)
	}
	if len(context.Accounts[stateKey].Data) != 0 {
		t.Fatalf("state data length = %d, want rollback to empty", len(context.Accounts[stateKey].Data))
	}
}

func TestProgramDispatchesPrivacyWithdrawThroughSyscall(t *testing.T) {
	authorityKey := testPublicKey(31)
	stateKey := testPublicKey(32)
	destinationKey := testPublicKey(33)
	vmProgramKey := testPublicKey(34)
	amount := uint64(600)
	commitment := testHash(35)
	nullifier := testHash(36)
	accountKeys := []structure.PublicKey{authorityKey, stateKey, destinationKey, vmProgramKey}
	context := testPrivacyVMContextWithAccounts(t, accountKeys, 1, 1, 3, []uint8{1, 2}, testPrivacyWithdrawData(t, amount, commitment, nullifier))
	context.Accounts[authorityKey] = testAccount(t, 2_000_000_000, nil, structure.DefaultBuiltinProgramIDs.System, false)
	context.Accounts[stateKey] = testAccount(t, 2_000_000_000, testPrivacyStateData(t, authorityKey, commitment, amount), structure.DefaultBuiltinProgramIDs.Privacy, false)
	context.Accounts[destinationKey] = testAccount(t, 2_000_000_000, nil, structure.DefaultBuiltinProgramIDs.System, false)
	context.Accounts[vmProgramKey] = testAccount(t, 2_000_000_000, testPrivacyVMProgramData(t), structure.DefaultBuiltinProgramIDs.BPFLoader, true)

	if err := NewProgram(structure.DefaultBuiltinProgramIDs.BPFLoader, svm.Runtime{}).Execute(context); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got := context.Accounts[stateKey].Lamports; got != 2_000_000_000-amount {
		t.Fatalf("state lamports = %d, want %d", got, 2_000_000_000-amount)
	}
	if got := context.Accounts[destinationKey].Lamports; got != 2_000_000_000+amount {
		t.Fatalf("destination lamports = %d, want %d", got, 2_000_000_000+amount)
	}
	state, err := structure.UnmarshalPrivacyStateBinary(context.Accounts[stateKey].Data)
	if err != nil {
		t.Fatalf("UnmarshalPrivacyStateBinary() error = %v", err)
	}
	if len(state.SpentNullifiers) != 1 || state.SpentNullifiers[0] != nullifier || !state.Notes[0].Spent {
		t.Fatalf("privacy state = %+v, want spent note and nullifier", state)
	}
}

func TestProgramDispatchesPrivacyTransferThroughSyscall(t *testing.T) {
	authorityKey := testPublicKey(41)
	stateKey := testPublicKey(42)
	receiverKey := testPublicKey(43)
	vmProgramKey := testPublicKey(44)
	amount := uint64(800)
	sourceCommitment := testHash(45)
	nullifier := testHash(46)
	outputCommitment := testHash(47)
	accountKeys := []structure.PublicKey{authorityKey, stateKey, vmProgramKey}
	context := testPrivacyVMContextWithAccounts(
		t,
		accountKeys,
		1,
		1,
		2,
		[]uint8{1},
		testPrivacyTransferData(t, amount, sourceCommitment, nullifier, outputCommitment, receiverKey),
	)
	context.Accounts[authorityKey] = testAccount(t, 2_000_000_000, nil, structure.DefaultBuiltinProgramIDs.System, false)
	context.Accounts[stateKey] = testAccount(t, 2_000_000_000, testPrivacyStateData(t, authorityKey, sourceCommitment, amount), structure.DefaultBuiltinProgramIDs.Privacy, false)
	context.Accounts[vmProgramKey] = testAccount(t, 2_000_000_000, testPrivacyVMProgramData(t), structure.DefaultBuiltinProgramIDs.BPFLoader, true)

	if err := NewProgram(structure.DefaultBuiltinProgramIDs.BPFLoader, svm.Runtime{}).Execute(context); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	state, err := structure.UnmarshalPrivacyStateBinary(context.Accounts[stateKey].Data)
	if err != nil {
		t.Fatalf("UnmarshalPrivacyStateBinary() error = %v", err)
	}
	if len(state.Notes) != 2 || !state.Notes[0].Spent || state.Notes[1].Commitment != outputCommitment {
		t.Fatalf("privacy notes = %+v, want spent source and output note", state.Notes)
	}
	if state.Notes[1].SpendAuthority != receiverKey || state.Notes[1].Amount != amount {
		t.Fatalf("output note = %+v, want receiver amount %d", state.Notes[1], amount)
	}
}

func testPrivacyVMContext(
	t *testing.T,
	sourceKey structure.PublicKey,
	stateKey structure.PublicKey,
	vmProgramKey structure.PublicKey,
	requiredSignatures uint8,
	instructionData []byte,
) runtime.InstructionContext {
	t.Helper()

	accountKeys := []structure.PublicKey{sourceKey, stateKey, vmProgramKey}
	context := testPrivacyVMContextWithAccounts(t, accountKeys, requiredSignatures, 1, 2, []uint8{0, 1}, instructionData)
	context.Accounts[sourceKey] = testAccount(t, 2_000_000_000, nil, structure.DefaultBuiltinProgramIDs.System, false)
	context.Accounts[stateKey] = testAccount(t, 2_000_000_000, nil, structure.DefaultBuiltinProgramIDs.Privacy, false)
	context.Accounts[vmProgramKey] = testAccount(t, 2_000_000_000, testPrivacyVMProgramData(t), structure.DefaultBuiltinProgramIDs.BPFLoader, true)
	return context
}

func testPrivacyVMContextWithAccounts(
	t *testing.T,
	accountKeys []structure.PublicKey,
	requiredSignatures uint8,
	readonlyUnsignedAccounts uint8,
	programIDIndex uint8,
	instructionAccounts []uint8,
	instructionData []byte,
) runtime.InstructionContext {
	t.Helper()

	return runtime.InstructionContext{
		Instruction: structure.CompiledInstruction{
			ProgramIDIndex: programIDIndex,
			AccountIndexes: append([]uint8(nil), instructionAccounts...),
			Data:           instructionData,
		},
		Message: structure.ResolvedMessage{
			Header: structure.MessageHeader{
				NumRequiredSignatures:       requiredSignatures,
				NumReadonlyUnsignedAccounts: readonlyUnsignedAccounts,
			},
			StaticAccountKeys: append([]structure.PublicKey(nil), accountKeys...),
			AccountKeys:       append([]structure.PublicKey(nil), accountKeys...),
		},
		Accounts:        make(map[structure.PublicKey]structure.Account, len(accountKeys)),
		CurrentSlot:     50,
		RentConfig:      structure.DefaultRentConfig,
		BuiltinPrograms: structure.DefaultBuiltinProgramIDs,
	}
}

func testPrivacyDepositData(t *testing.T, sourceKey structure.PublicKey, amount uint64, commitment structure.Hash) []byte {
	t.Helper()

	instruction, err := structure.NewPrivacyDepositInstruction(1, nil, structure.PrivacyDepositParams{
		Amount:         amount,
		Commitment:     commitment,
		SpendAuthority: sourceKey,
		EncryptedNote:  []byte("vm-note"),
	})
	if err != nil {
		t.Fatalf("NewPrivacyDepositInstruction() error = %v", err)
	}
	encoded, err := instruction.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	return encoded
}

func testPrivacyWithdrawData(t *testing.T, amount uint64, commitment structure.Hash, nullifier structure.Hash) []byte {
	t.Helper()

	instruction, err := structure.NewPrivacyWithdrawInstruction(1, nil, structure.PrivacyWithdrawParams{
		Amount:           amount,
		SourceCommitment: commitment,
		Nullifier:        nullifier,
	})
	if err != nil {
		t.Fatalf("NewPrivacyWithdrawInstruction() error = %v", err)
	}
	encoded, err := instruction.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	return encoded
}

func testPrivacyTransferData(
	t *testing.T,
	amount uint64,
	sourceCommitment structure.Hash,
	nullifier structure.Hash,
	outputCommitment structure.Hash,
	receiverKey structure.PublicKey,
) []byte {
	t.Helper()

	instruction, err := structure.NewPrivacyTransferInstruction(1, nil, structure.PrivacyTransferParams{
		Amount:               amount,
		SourceCommitment:     sourceCommitment,
		Nullifier:            nullifier,
		OutputCommitment:     outputCommitment,
		OutputSpendAuthority: receiverKey,
		OutputEncryptedNote:  []byte("vm-transfer-note"),
	})
	if err != nil {
		t.Fatalf("NewPrivacyTransferInstruction() error = %v", err)
	}
	encoded, err := instruction.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	return encoded
}

func testPrivacyStateData(t *testing.T, spendAuthority structure.PublicKey, commitment structure.Hash, amount uint64) []byte {
	t.Helper()

	state := structure.PrivacyState{
		Version: structure.PrivacyStateStorageVersion,
		Notes: []structure.PrivacyNoteRecord{
			{
				Commitment:     commitment,
				SpendAuthority: spendAuthority,
				Amount:         amount,
				VMVersion:      1,
				EncryptedNote:  []byte("vm-source-note"),
			},
		},
		PrivacyPoolLamports:  amount,
		UnspentNoteLiability: amount,
	}
	encoded, err := state.MarshalBinary()
	if err != nil {
		t.Fatalf("PrivacyState.MarshalBinary() error = %v", err)
	}
	return encoded
}

func testPrivacyVMProgramData(t *testing.T) []byte {
	t.Helper()

	encoded, err := PrivacyBridgeProgramData()
	if err != nil {
		t.Fatalf("PrivacyBridgeProgramData() error = %v", err)
	}
	return encoded
}

func testAccount(t *testing.T, lamports uint64, data []byte, owner structure.PublicKey, executable bool) structure.Account {
	t.Helper()

	account, err := structure.NewAccount(lamports, data, owner, executable, 0)
	if err != nil {
		t.Fatalf("NewAccount() error = %v", err)
	}
	return account
}

func testPublicKey(seed byte) structure.PublicKey {
	var publicKey structure.PublicKey
	for index := range publicKey {
		publicKey[index] = seed + byte(index)
	}
	return publicKey
}

func testHash(seed byte) structure.Hash {
	var hash structure.Hash
	for index := range hash {
		hash[index] = seed + byte(index)
	}
	return hash
}
