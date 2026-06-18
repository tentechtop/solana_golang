package vmprogram

import (
	"testing"

	stakeprogram "solana_golang/programs/stake"
	"solana_golang/runtime"
	"solana_golang/structure"
	svm "solana_golang/vm"
)

func TestStakePoolDistributesRewardsWithoutHistoricalDilution(t *testing.T) {
	firstUserKey := testPublicKey(81)
	secondUserKey := testPublicKey(82)
	poolKey := testPublicKey(83)
	firstReceiptKey := testPublicKey(84)
	secondReceiptKey := testPublicKey(85)
	validatorKey := testPublicKey(86)
	vmProgramKey := testPublicKey(87)
	accounts := testStakePoolAccounts(t, firstUserKey, poolKey, firstReceiptKey, validatorKey, vmProgramKey)
	accounts[secondUserKey] = testAccount(t, 2_000_000_000, nil, structure.DefaultBuiltinProgramIDs.System, false)
	accounts[secondReceiptKey] = testAccount(t, 2_000_000_000, nil, vmProgramKey, false)

	testExecuteStakePoolInstruction(t, accounts, []structure.PublicKey{firstUserKey, poolKey, firstReceiptKey, vmProgramKey}, 3, []uint8{0, 1, 2}, testStakePoolInitializeData(t, validatorKey))
	testExecuteStakePoolInstruction(t, accounts, []structure.PublicKey{firstUserKey, poolKey, firstReceiptKey, vmProgramKey}, 3, []uint8{0, 1, 2}, testStakePoolDepositData(t, 20_000_000))
	testExecuteStakePoolInstruction(t, accounts, []structure.PublicKey{firstUserKey, poolKey, firstReceiptKey, vmProgramKey}, 3, []uint8{0, 1, 2}, testStakePoolDistributeData(t, 5_000_000))
	testExecuteStakePoolInstruction(t, accounts, []structure.PublicKey{secondUserKey, poolKey, secondReceiptKey, vmProgramKey}, 3, []uint8{0, 1, 2}, testStakePoolDepositData(t, 20_000_000))

	if err := testExecuteStakePoolInstructionResult(accounts, []structure.PublicKey{secondUserKey, poolKey, secondReceiptKey, vmProgramKey}, 3, []uint8{0, 1, 2}, testStakePoolClaimData(t)); err == nil {
		t.Fatal("second user claim error = nil, want no historical rewards")
	}
	testExecuteStakePoolInstruction(t, accounts, []structure.PublicKey{firstUserKey, poolKey, firstReceiptKey, vmProgramKey}, 3, []uint8{0, 1, 2}, testStakePoolClaimData(t))

	firstReceipt, err := UnmarshalStakePoolReceiptStateBinary(accounts[firstReceiptKey].Data)
	if err != nil {
		t.Fatalf("UnmarshalStakePoolReceiptStateBinary(first) error = %v", err)
	}
	if firstReceipt.ClaimedRewards != 5_000_000 {
		t.Fatalf("first claimed rewards = %d, want 5000000", firstReceipt.ClaimedRewards)
	}
	secondReceipt, err := UnmarshalStakePoolReceiptStateBinary(accounts[secondReceiptKey].Data)
	if err != nil {
		t.Fatalf("UnmarshalStakePoolReceiptStateBinary(second) error = %v", err)
	}
	if secondReceipt.ClaimedRewards != 0 {
		t.Fatalf("second claimed rewards = %d, want 0", secondReceipt.ClaimedRewards)
	}
}

func TestStakePoolRegistersValidatorThroughFixedStakeProgram(t *testing.T) {
	authorityKey := testPublicKey(91)
	poolKey := testPublicKey(92)
	receiptKey := testPublicKey(93)
	validatorKey := testPublicKey(94)
	vmProgramKey := testPublicKey(95)
	consensusKey := testPublicKey(96)
	accounts := testStakePoolAccounts(t, authorityKey, poolKey, receiptKey, validatorKey, vmProgramKey)

	testExecuteStakePoolInstruction(t, accounts, []structure.PublicKey{authorityKey, poolKey, receiptKey, vmProgramKey}, 3, []uint8{0, 1, 2}, testStakePoolInitializeData(t, validatorKey))
	testExecuteStakePoolInstruction(t, accounts, []structure.PublicKey{authorityKey, poolKey, receiptKey, vmProgramKey}, 3, []uint8{0, 1, 2}, testStakePoolDepositData(t, stakeprogram.MinimumStakeLamports))
	testExecuteStakePoolInstruction(
		t,
		accounts,
		[]structure.PublicKey{authorityKey, poolKey, receiptKey, validatorKey, vmProgramKey},
		4,
		[]uint8{0, 1, 2, 3},
		testStakePoolRegisterData(t, consensusKey, stakeprogram.MinimumStakeLamports),
	)

	poolState, err := UnmarshalStakePoolStateBinary(accounts[poolKey].Data)
	if err != nil {
		t.Fatalf("UnmarshalStakePoolStateBinary() error = %v", err)
	}
	if poolState.DelegatedLamports != stakeprogram.MinimumStakeLamports {
		t.Fatalf("delegated lamports = %d, want %d", poolState.DelegatedLamports, stakeprogram.MinimumStakeLamports)
	}
	if accounts[validatorKey].Owner != structure.DefaultBuiltinProgramIDs.Stake {
		t.Fatalf("validator owner = %s, want stake program", accounts[validatorKey].Owner.String())
	}
	validatorState, err := stakeprogram.UnmarshalValidatorStateBinary(accounts[validatorKey].Data)
	if err != nil {
		t.Fatalf("UnmarshalValidatorStateBinary() error = %v", err)
	}
	if validatorState.StakerAccount != poolKey {
		t.Fatalf("validator staker = %s, want pool %s", validatorState.StakerAccount.String(), poolKey.String())
	}
	if validatorState.PendingStake != stakeprogram.MinimumStakeLamports {
		t.Fatalf("pending stake = %d, want %d", validatorState.PendingStake, stakeprogram.MinimumStakeLamports)
	}
}

func TestStakePoolUnstakesAndWithdrawsThroughFixedStakeProgram(t *testing.T) {
	authorityKey := testPublicKey(101)
	poolKey := testPublicKey(102)
	receiptKey := testPublicKey(103)
	validatorKey := testPublicKey(104)
	vmProgramKey := testPublicKey(105)
	consensusKey := testPublicKey(106)
	accounts := testStakePoolAccounts(t, authorityKey, poolKey, receiptKey, validatorKey, vmProgramKey)
	stakeAmount := 2 * stakeprogram.MinimumStakeLamports
	unstakeAmount := stakeprogram.MinimumStakeLamports

	testExecuteStakePoolInstruction(t, accounts, []structure.PublicKey{authorityKey, poolKey, receiptKey, vmProgramKey}, 3, []uint8{0, 1, 2}, testStakePoolInitializeData(t, validatorKey))
	testExecuteStakePoolInstruction(t, accounts, []structure.PublicKey{authorityKey, poolKey, receiptKey, vmProgramKey}, 3, []uint8{0, 1, 2}, testStakePoolDepositData(t, stakeAmount))
	testExecuteStakePoolInstruction(
		t,
		accounts,
		[]structure.PublicKey{authorityKey, poolKey, receiptKey, validatorKey, vmProgramKey},
		4,
		[]uint8{0, 1, 2, 3},
		testStakePoolRegisterData(t, consensusKey, stakeAmount),
	)
	testExecuteStakePoolInstructionAtEpoch(
		t,
		accounts,
		[]structure.PublicKey{authorityKey, poolKey, receiptKey, validatorKey, vmProgramKey},
		4,
		[]uint8{0, 1, 2, 3},
		testStakePoolRequestUnstakeData(t, unstakeAmount, 6),
		5,
	)
	testExecuteStakePoolInstructionAtEpoch(
		t,
		accounts,
		[]structure.PublicKey{authorityKey, poolKey, receiptKey, validatorKey, vmProgramKey},
		4,
		[]uint8{0, 1, 2, 3},
		testStakePoolCompleteUnstakeData(t, 6),
		6,
	)
	testExecuteStakePoolInstructionAtEpoch(t, accounts, []structure.PublicKey{authorityKey, poolKey, receiptKey, vmProgramKey}, 3, []uint8{0, 1, 2}, testStakePoolWithdrawUnstakedData(t), 6)

	poolState, err := UnmarshalStakePoolStateBinary(accounts[poolKey].Data)
	if err != nil {
		t.Fatalf("UnmarshalStakePoolStateBinary() error = %v", err)
	}
	if poolState.DelegatedLamports != stakeprogram.MinimumStakeLamports {
		t.Fatalf("delegated lamports = %d, want %d", poolState.DelegatedLamports, stakeprogram.MinimumStakeLamports)
	}
	if poolState.WithdrawableLamports != 0 || poolState.PendingUnstakeLamports != 0 {
		t.Fatalf("pool exit accounting = withdrawable %d pending %d, want zero", poolState.WithdrawableLamports, poolState.PendingUnstakeLamports)
	}
	receipt, err := UnmarshalStakePoolReceiptStateBinary(accounts[receiptKey].Data)
	if err != nil {
		t.Fatalf("UnmarshalStakePoolReceiptStateBinary() error = %v", err)
	}
	if receipt.Shares != stakeprogram.MinimumStakeLamports || receipt.PendingWithdrawLamports != 0 {
		t.Fatalf("receipt exit state = shares %d pending %d", receipt.Shares, receipt.PendingWithdrawLamports)
	}
}

func testExecuteStakePoolInstruction(
	t *testing.T,
	accounts map[structure.PublicKey]structure.Account,
	accountKeys []structure.PublicKey,
	programIDIndex uint8,
	instructionAccounts []uint8,
	instructionData []byte,
) {
	t.Helper()
	if err := testExecuteStakePoolInstructionResult(accounts, accountKeys, programIDIndex, instructionAccounts, instructionData); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}

func testExecuteStakePoolInstructionAtEpoch(
	t *testing.T,
	accounts map[structure.PublicKey]structure.Account,
	accountKeys []structure.PublicKey,
	programIDIndex uint8,
	instructionAccounts []uint8,
	instructionData []byte,
	currentEpoch uint64,
) {
	t.Helper()
	if err := testExecuteStakePoolInstructionResultAtEpoch(accounts, accountKeys, programIDIndex, instructionAccounts, instructionData, currentEpoch); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}

func testExecuteStakePoolInstructionResult(
	accounts map[structure.PublicKey]structure.Account,
	accountKeys []structure.PublicKey,
	programIDIndex uint8,
	instructionAccounts []uint8,
	instructionData []byte,
) error {
	return testExecuteStakePoolInstructionResultAtEpoch(accounts, accountKeys, programIDIndex, instructionAccounts, instructionData, 3)
}

func testExecuteStakePoolInstructionResultAtEpoch(
	accounts map[structure.PublicKey]structure.Account,
	accountKeys []structure.PublicKey,
	programIDIndex uint8,
	instructionAccounts []uint8,
	instructionData []byte,
	currentEpoch uint64,
) error {
	context := runtime.InstructionContext{
		Instruction: structure.CompiledInstruction{
			ProgramIDIndex: programIDIndex,
			AccountIndexes: append([]uint8(nil), instructionAccounts...),
			Data:           instructionData,
		},
		Message: structure.ResolvedMessage{
			Header: structure.MessageHeader{
				NumRequiredSignatures:       1,
				NumReadonlyUnsignedAccounts: 1,
			},
			StaticAccountKeys: append([]structure.PublicKey(nil), accountKeys...),
			AccountKeys:       append([]structure.PublicKey(nil), accountKeys...),
		},
		Accounts:        accounts,
		CurrentSlot:     60,
		CurrentEpoch:    currentEpoch,
		RentConfig:      structure.DefaultRentConfig,
		BuiltinPrograms: structure.DefaultBuiltinProgramIDs,
	}
	return NewProgram(structure.DefaultBuiltinProgramIDs.BPFLoader, svm.Runtime{}).Execute(context)
}

func testStakePoolAccounts(
	t *testing.T,
	userKey structure.PublicKey,
	poolKey structure.PublicKey,
	receiptKey structure.PublicKey,
	validatorKey structure.PublicKey,
	vmProgramKey structure.PublicKey,
) map[structure.PublicKey]structure.Account {
	t.Helper()
	return map[structure.PublicKey]structure.Account{
		userKey:      testAccount(t, 2_000_000_000, nil, structure.DefaultBuiltinProgramIDs.System, false),
		poolKey:      testAccount(t, 2_000_000_000, nil, vmProgramKey, false),
		receiptKey:   testAccount(t, 2_000_000_000, nil, vmProgramKey, false),
		validatorKey: testAccount(t, 2_000_000_000, nil, structure.DefaultBuiltinProgramIDs.System, false),
		vmProgramKey: testAccount(t, 2_000_000_000, testStakePoolVMProgramData(t), structure.DefaultBuiltinProgramIDs.BPFLoader, true),
	}
}

func testStakePoolInitializeData(t *testing.T, validatorKey structure.PublicKey) []byte {
	t.Helper()
	instruction, err := NewStakePoolInitializeInstruction(validatorKey)
	if err != nil {
		t.Fatalf("NewStakePoolInitializeInstruction() error = %v", err)
	}
	return testMarshalStakePoolInstruction(t, instruction)
}

func testStakePoolDepositData(t *testing.T, amount uint64) []byte {
	t.Helper()
	instruction, err := NewStakePoolDepositInstruction(amount)
	if err != nil {
		t.Fatalf("NewStakePoolDepositInstruction() error = %v", err)
	}
	return testMarshalStakePoolInstruction(t, instruction)
}

func testStakePoolDistributeData(t *testing.T, amount uint64) []byte {
	t.Helper()
	instruction, err := NewStakePoolDistributeRewardsInstruction(amount)
	if err != nil {
		t.Fatalf("NewStakePoolDistributeRewardsInstruction() error = %v", err)
	}
	return testMarshalStakePoolInstruction(t, instruction)
}

func testStakePoolRegisterData(t *testing.T, consensusKey structure.PublicKey, amount uint64) []byte {
	t.Helper()
	instruction, err := NewStakePoolRegisterValidatorInstruction(consensusKey, "vm-pool-validator", 0, amount, nil)
	if err != nil {
		t.Fatalf("NewStakePoolRegisterValidatorInstruction() error = %v", err)
	}
	return testMarshalStakePoolInstruction(t, instruction)
}

func testStakePoolClaimData(t *testing.T) []byte {
	t.Helper()
	return testMarshalStakePoolInstruction(t, NewStakePoolClaimRewardsInstruction())
}

func testStakePoolRequestUnstakeData(t *testing.T, amount uint64, unlockEpoch uint64) []byte {
	t.Helper()
	instruction, err := NewStakePoolRequestUnstakeInstruction(amount, unlockEpoch)
	if err != nil {
		t.Fatalf("NewStakePoolRequestUnstakeInstruction() error = %v", err)
	}
	return testMarshalStakePoolInstruction(t, instruction)
}

func testStakePoolCompleteUnstakeData(t *testing.T, currentEpoch uint64) []byte {
	t.Helper()
	instruction, err := NewStakePoolCompleteUnstakeInstruction(currentEpoch)
	if err != nil {
		t.Fatalf("NewStakePoolCompleteUnstakeInstruction() error = %v", err)
	}
	return testMarshalStakePoolInstruction(t, instruction)
}

func testStakePoolWithdrawUnstakedData(t *testing.T) []byte {
	t.Helper()
	return testMarshalStakePoolInstruction(t, NewStakePoolWithdrawUnstakedInstruction())
}

func testMarshalStakePoolInstruction(t *testing.T, instruction StakePoolInstruction) []byte {
	t.Helper()
	encoded, err := instruction.MarshalBinary()
	if err != nil {
		t.Fatalf("StakePoolInstruction.MarshalBinary() error = %v", err)
	}
	return encoded
}

func testStakePoolVMProgramData(t *testing.T) []byte {
	t.Helper()
	encoded, err := StakePoolBridgeProgramData()
	if err != nil {
		t.Fatalf("StakePoolBridgeProgramData() error = %v", err)
	}
	return encoded
}
