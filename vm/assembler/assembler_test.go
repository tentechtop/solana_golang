package assembler

import (
	"testing"

	"solana_golang/vm"
)

func TestAssembleBuildsExecutableLogProgram(t *testing.T) {
	source := `
data message "hello-svmasm"
syscall log message
exit
`
	bytecode, err := Assemble(source)
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	programID := testAddress(1)
	loaderID := testAddress(2)
	result, err := vm.NewRuntime(loaderID).Execute(vm.Invocation{
		ProgramID: programID,
		ProgramAccount: vm.ProgramAccount{
			Address:    programID,
			Owner:      loaderID,
			Executable: true,
			Data:       bytecode,
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(result.Logs) != 1 || result.Logs[0] != "hello-svmasm" {
		t.Fatalf("Logs = %+v, want hello-svmasm", result.Logs)
	}
}

func TestAssembleRejectsDuplicateLabel(t *testing.T) {
	source := `
loop:
loop:
exit
`
	if _, err := Assemble(source); err == nil {
		t.Fatal("Assemble() error = nil, want duplicate label error")
	}
}

func testAddress(seed byte) vm.Address {
	var address vm.Address
	for index := range address {
		address[index] = seed + byte(index)
	}
	return address
}
