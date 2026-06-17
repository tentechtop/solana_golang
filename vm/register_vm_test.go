package vm

import (
	"errors"
	"testing"
)

func TestRegisterRuntimeLogsReadOnlyData(t *testing.T) {
	programID := testAddress(41)
	loaderID := testAddress(42)
	message := []byte("register-log")
	code := mustRegisterCode(t,
		mustRegisterInstruction(t, RegOpMovImm, 1, 0, 0, RegisterMemoryBaseReadOnly),
		mustRegisterInstruction(t, RegOpMovImm, 2, 0, 0, uint64(len(message))),
		mustRegisterInstruction(t, RegOpSyscall, 0, 0, 0, uint64(SyscallLog)),
	)
	programAccount := testRegisterProgramAccount(t, programID, loaderID, code, message)

	result, err := NewRuntime(loaderID).Execute(Invocation{ProgramID: programID, ProgramAccount: programAccount})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(result.Logs) != 1 || result.Logs[0] != "register-log" {
		t.Fatalf("Logs = %+v, want register-log", result.Logs)
	}
}

func TestRegisterRuntimeWritesAccountDataThroughSyscall(t *testing.T) {
	programID := testAddress(43)
	loaderID := testAddress(44)
	input := EncodeAccountDataWriteInput(0, 0, []byte("vm-data"))
	code := mustRegisterCode(t,
		mustRegisterInstruction(t, RegOpMovImm, 1, 0, 0, RegisterMemoryBaseReadOnly),
		mustRegisterInstruction(t, RegOpMovImm, 2, 0, 0, uint64(len(input))),
		mustRegisterInstruction(t, RegOpSyscall, 0, 0, 0, uint64(SyscallSetAccountData)),
	)
	programAccount := testRegisterProgramAccount(t, programID, loaderID, code, input)
	dataAccount := Account{Address: testAddress(45), Owner: programID, IsWritable: true}

	result, err := NewRuntime(loaderID).Execute(Invocation{
		ProgramID:      programID,
		ProgramAccount: programAccount,
		Accounts:       []Account{dataAccount},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if string(result.Accounts[0].Data) != "vm-data" {
		t.Fatalf("account data = %q, want vm-data", string(result.Accounts[0].Data))
	}
}

func TestRegisterRuntimeRejectsUnknownSyscallInVerifier(t *testing.T) {
	programID := testAddress(46)
	loaderID := testAddress(47)
	code := mustRegisterCode(t, mustRegisterInstruction(t, RegOpSyscall, 0, 0, 0, 99999))
	programAccount := testRegisterProgramAccount(t, programID, loaderID, code, nil)

	_, err := NewRuntime(loaderID).Execute(Invocation{ProgramID: programID, ProgramAccount: programAccount})
	if !errors.Is(err, ErrInvalidProgram) {
		t.Fatalf("Execute() error = %v, want ErrInvalidProgram", err)
	}
}

func mustRegisterInstruction(t *testing.T, opcode byte, dst byte, src byte, offset int32, immediate uint64) []byte {
	t.Helper()

	instruction, err := BuildRegisterInstruction(opcode, dst, src, offset, immediate)
	if err != nil {
		t.Fatalf("BuildRegisterInstruction() error = %v", err)
	}
	return instruction
}

func mustRegisterCode(t *testing.T, instructions ...[]byte) []byte {
	t.Helper()

	code, err := BuildRegisterProgramCode(instructions...)
	if err != nil {
		t.Fatalf("BuildRegisterProgramCode() error = %v", err)
	}
	return code
}

func testRegisterProgramAccount(t *testing.T, programID Address, loaderID Address, code []byte, readOnlyData []byte) ProgramAccount {
	t.Helper()

	encoded, err := EncodeRegisterBytecode(code, readOnlyData)
	if err != nil {
		t.Fatalf("EncodeRegisterBytecode() error = %v", err)
	}
	return ProgramAccount{Address: programID, Owner: loaderID, Executable: true, Data: encoded}
}
