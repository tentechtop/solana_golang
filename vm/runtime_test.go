package vm

import (
	"encoding/binary"
	"errors"
	"testing"

	"solana_golang/zk"
)

func TestRuntimeWritesInstructionDataToOwnedWritableAccount(t *testing.T) {
	programID := testAddress(1)
	loaderID := testAddress(2)
	programAccount := testProgramAccount(t, programID, loaderID, BuildProgramCode(BuildWriteInstructionDataOp(0, 0)))
	dataAccount := Account{Address: testAddress(3), Owner: programID, IsWritable: true}

	result, err := NewRuntime(loaderID).Execute(Invocation{
		ProgramID:       programID,
		ProgramAccount:  programAccount,
		Accounts:        []Account{dataAccount},
		InstructionData: []byte("hello"),
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if string(result.Accounts[0].Data) != "hello" {
		t.Fatalf("account data = %q, want hello", string(result.Accounts[0].Data))
	}
	if result.UnitsConsumed == 0 {
		t.Fatal("UnitsConsumed = 0, want positive")
	}
}

func TestRuntimeRejectsReadonlyWrite(t *testing.T) {
	programID := testAddress(4)
	loaderID := testAddress(5)
	programAccount := testProgramAccount(t, programID, loaderID, BuildProgramCode(BuildWriteInstructionDataOp(0, 0)))
	dataAccount := Account{Address: testAddress(6), Owner: programID, IsWritable: false}

	_, err := NewRuntime(loaderID).Execute(Invocation{
		ProgramID:       programID,
		ProgramAccount:  programAccount,
		Accounts:        []Account{dataAccount},
		InstructionData: []byte("hello"),
	})
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("Execute() error = %v, want ErrPermissionDenied", err)
	}
}

func TestRuntimeRejectsOwnerMismatchWrite(t *testing.T) {
	programID := testAddress(7)
	loaderID := testAddress(8)
	programAccount := testProgramAccount(t, programID, loaderID, BuildProgramCode(BuildWriteInstructionDataOp(0, 0)))
	dataAccount := Account{Address: testAddress(9), Owner: testAddress(10), IsWritable: true}

	_, err := NewRuntime(loaderID).Execute(Invocation{
		ProgramID:       programID,
		ProgramAccount:  programAccount,
		Accounts:        []Account{dataAccount},
		InstructionData: []byte("hello"),
	})
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("Execute() error = %v, want ErrPermissionDenied", err)
	}
}

func TestRuntimeTransfersLamportsFromProgramOwnedAccount(t *testing.T) {
	programID := testAddress(11)
	loaderID := testAddress(12)
	programAccount := testProgramAccount(t, programID, loaderID, BuildProgramCode(BuildTransferLamportsOp(0, 1, 7)))
	sourceAccount := Account{Address: testAddress(13), Owner: programID, Lamports: 10, IsWritable: true}
	destinationAccount := Account{Address: testAddress(14), Owner: testAddress(15), Lamports: 1, IsWritable: true}

	result, err := NewRuntime(loaderID).Execute(Invocation{
		ProgramID:      programID,
		ProgramAccount: programAccount,
		Accounts:       []Account{sourceAccount, destinationAccount},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Accounts[0].Lamports != 3 || result.Accounts[1].Lamports != 8 {
		t.Fatalf("lamports = %d/%d, want 3/8", result.Accounts[0].Lamports, result.Accounts[1].Lamports)
	}
}

func TestRuntimeRejectsComputeExceeded(t *testing.T) {
	programID := testAddress(16)
	loaderID := testAddress(17)
	programAccount := testProgramAccount(t, programID, loaderID, BuildProgramCode(BuildWriteInstructionDataOp(0, 0)))
	dataAccount := Account{Address: testAddress(18), Owner: programID, IsWritable: true}

	_, err := NewRuntime(loaderID).Execute(Invocation{
		ProgramID:       programID,
		ProgramAccount:  programAccount,
		Accounts:        []Account{dataAccount},
		InstructionData: []byte("hello"),
		ComputeLimit:    1,
	})
	if !errors.Is(err, ErrComputeExceeded) {
		t.Fatalf("Execute() error = %v, want ErrComputeExceeded", err)
	}
}

func TestRuntimeRejectsManifestUndeclaredSyscall(t *testing.T) {
	programID := testAddress(60)
	loaderID := testAddress(61)
	programData, err := EncodeGovernedBytecode(BuildProgramCode(BuildSyscallOp(SyscallLog, []byte("blocked"))), ProgramManifest{})
	if err != nil {
		t.Fatalf("EncodeGovernedBytecode() error = %v", err)
	}
	programAccount := ProgramAccount{Address: programID, Owner: loaderID, Executable: true, Data: programData}

	_, err = NewRuntime(loaderID).Execute(Invocation{ProgramID: programID, ProgramAccount: programAccount})
	if !errors.Is(err, ErrInvalidProgram) {
		t.Fatalf("Execute() error = %v, want ErrInvalidProgram", err)
	}
}

func TestRuntimeUsesManifestComputeLimit(t *testing.T) {
	programID := testAddress(62)
	loaderID := testAddress(63)
	programData, err := EncodeGovernedBytecode(BuildProgramCode(), ProgramManifest{ComputeUnitLimit: 1})
	if err != nil {
		t.Fatalf("EncodeGovernedBytecode() error = %v", err)
	}
	programAccount := ProgramAccount{Address: programID, Owner: loaderID, Executable: true, Data: programData}

	_, err = NewRuntime(loaderID).Execute(Invocation{
		ProgramID:      programID,
		ProgramAccount: programAccount,
		ComputeLimit:   DefaultComputeUnitLimit,
	})
	if !errors.Is(err, ErrComputeExceeded) {
		t.Fatalf("Execute() error = %v, want ErrComputeExceeded", err)
	}
}

func TestBytecodeLoaderRejectsManifestChecksumMismatch(t *testing.T) {
	programID := testAddress(64)
	loaderID := testAddress(65)
	programData, err := EncodeGovernedBytecode(BuildProgramCode(), ProgramManifest{ComputeUnitLimit: DefaultComputeUnitLimit})
	if err != nil {
		t.Fatalf("EncodeGovernedBytecode() error = %v", err)
	}
	programData[len(programData)-1] ^= 0xff
	programAccount := ProgramAccount{Address: programID, Owner: loaderID, Executable: true, Data: programData}

	_, err = (BytecodeLoader{}).Load(programAccount, loaderID)
	if !errors.Is(err, ErrInvalidProgram) {
		t.Fatalf("Load() error = %v, want ErrInvalidProgram", err)
	}
}

func TestRuntimeSyscallsLogAndReturnData(t *testing.T) {
	programID := testAddress(19)
	loaderID := testAddress(20)
	programAccount := testProgramAccount(t, programID, loaderID, BuildProgramCode(
		BuildSyscallOp(SyscallLog, []byte("hello-log")),
		BuildSyscallOp(SyscallSetReturnData, []byte("return-data")),
	))

	result, err := NewRuntime(loaderID).Execute(Invocation{ProgramID: programID, ProgramAccount: programAccount})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(result.Logs) != 1 || result.Logs[0] != "hello-log" {
		t.Fatalf("Logs = %+v, want hello-log", result.Logs)
	}
	if result.ReturnData == nil || string(result.ReturnData.Data) != "return-data" || result.ReturnData.ProgramID != programID {
		t.Fatalf("ReturnData = %+v, want program return-data", result.ReturnData)
	}
}

func TestRuntimeSyscallReadsClockSysvar(t *testing.T) {
	programID := testAddress(21)
	loaderID := testAddress(22)
	programAccount := testProgramAccount(t, programID, loaderID, BuildProgramCode(BuildSyscallOp(SyscallGetClock, nil)))

	result, err := NewRuntime(loaderID).Execute(Invocation{
		ProgramID:      programID,
		ProgramAccount: programAccount,
		Sysvars:        Sysvars{Clock: ClockSysvar{Slot: 77, UnixTimestamp: 1234}},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(result.LastSyscallOutput) != 16 {
		t.Fatalf("clock output length = %d, want 16", len(result.LastSyscallOutput))
	}
	if slot := binary.LittleEndian.Uint64(result.LastSyscallOutput[:8]); slot != 77 {
		t.Fatalf("clock slot = %d, want 77", slot)
	}
}

func TestRuntimeSyscallCreatesProgramAddress(t *testing.T) {
	programID := testAddress(23)
	loaderID := testAddress(24)
	pdaInput, err := EncodeProgramAddressInput(programID, [][]byte{[]byte("vault")})
	if err != nil {
		t.Fatalf("EncodeProgramAddressInput() error = %v", err)
	}
	programAccount := testProgramAccount(t, programID, loaderID, BuildProgramCode(BuildSyscallOp(SyscallCreateProgramAddress, pdaInput)))
	wantAddress, err := CreateProgramAddress(programID, [][]byte{[]byte("vault")})
	if err != nil {
		t.Fatalf("CreateProgramAddress() error = %v", err)
	}

	result, err := NewRuntime(loaderID).Execute(Invocation{ProgramID: programID, ProgramAccount: programAccount})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var gotAddress Address
	copy(gotAddress[:], result.LastSyscallOutput)
	if gotAddress != wantAddress {
		t.Fatalf("pda = %x, want %x", gotAddress, wantAddress)
	}
}

func TestRuntimeSyscallVerifiesSchnorrProof(t *testing.T) {
	programID := testAddress(25)
	loaderID := testAddress(26)
	keyPair, err := zk.GenerateSchnorrKeyPair()
	if err != nil {
		t.Fatalf("GenerateSchnorrKeyPair() error = %v", err)
	}
	message := []byte("gosvm privacy spend")
	proof, err := zk.NewSchnorrProofBytes(keyPair.PrivateScalar, message)
	if err != nil {
		t.Fatalf("NewSchnorrProofBytes() error = %v", err)
	}
	digest, err := zk.SchnorrPublicKeyDigest(keyPair.PublicKey)
	if err != nil {
		t.Fatalf("SchnorrPublicKeyDigest() error = %v", err)
	}
	verifyInput := EncodeSchnorrVerifyInput(digest, message, proof)
	programAccount := testProgramAccount(t, programID, loaderID, BuildProgramCode(BuildSyscallOp(SyscallVerifySchnorr, verifyInput)))

	result, err := NewRuntime(loaderID).Execute(Invocation{ProgramID: programID, ProgramAccount: programAccount})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(result.LastSyscallOutput) != 1 || result.LastSyscallOutput[0] != 1 {
		t.Fatalf("schnorr output = %v, want [1]", result.LastSyscallOutput)
	}
}

func TestRuntimeSyscallInvokesCPI(t *testing.T) {
	programID := testAddress(27)
	loaderID := testAddress(28)
	cpiInstruction := CPIInstruction{
		ProgramID:       testAddress(29),
		AccountIndexes:  []uint8{0},
		InstructionData: []byte("inner"),
	}
	cpiInput, err := EncodeCPIInstruction(cpiInstruction)
	if err != nil {
		t.Fatalf("EncodeCPIInstruction() error = %v", err)
	}
	programAccount := testProgramAccount(t, programID, loaderID, BuildProgramCode(BuildSyscallOp(SyscallInvoke, cpiInput)))
	fakeCPI := &testCPIRuntime{}
	runtime := NewRuntime(loaderID)
	runtime.CPI = fakeCPI

	_, err = runtime.Execute(Invocation{ProgramID: programID, ProgramAccount: programAccount})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !fakeCPI.called || fakeCPI.instruction.ProgramID != cpiInstruction.ProgramID || string(fakeCPI.instruction.InstructionData) != "inner" {
		t.Fatalf("fake CPI = called %v instruction %+v", fakeCPI.called, fakeCPI.instruction)
	}
}

type testCPIRuntime struct {
	called      bool
	instruction CPIInstruction
}

func (runtime *testCPIRuntime) Invoke(_ *Context, instruction CPIInstruction) error {
	runtime.called = true
	runtime.instruction = instruction
	return nil
}

func testProgramAccount(t *testing.T, programID Address, loaderID Address, code []byte) ProgramAccount {
	t.Helper()

	encoded, err := EncodeBytecode(code)
	if err != nil {
		t.Fatalf("EncodeBytecode() error = %v", err)
	}
	return ProgramAccount{Address: programID, Owner: loaderID, Executable: true, Data: encoded}
}

func testAddress(seed byte) Address {
	var address Address
	for index := range address {
		address[index] = seed + byte(index)
	}
	return address
}
