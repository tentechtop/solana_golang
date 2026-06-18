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

func TestAssemblePrivacyExecuteSyscall(t *testing.T) {
	source := `
syscall privacy_execute
exit
`
	if _, err := Assemble(source); err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
}

func TestAssembleWithManifestBuildsGovernedRegisterBytecode(t *testing.T) {
	source := `
syscall asset_execute
exit
`
	bytecode, err := AssembleWithManifest(source, vm.ProgramManifest{
		ComputeUnitLimit: 100_000,
		RequiredSyscalls: []vm.SyscallID{
			vm.SyscallAssetExecute,
		},
	})
	if err != nil {
		t.Fatalf("AssembleWithManifest() error = %v", err)
	}
	manifest, ok, err := vm.DecodeProgramManifest(bytecode)
	if err != nil {
		t.Fatalf("DecodeProgramManifest() error = %v", err)
	}
	if !ok {
		t.Fatal("DecodeProgramManifest() ok = false, want true")
	}
	if manifest.ComputeUnitLimit != 100_000 {
		t.Fatalf("ComputeUnitLimit = %d, want 100000", manifest.ComputeUnitLimit)
	}
	if len(manifest.RequiredSyscalls) != 1 || manifest.RequiredSyscalls[0] != vm.SyscallAssetExecute {
		t.Fatalf("RequiredSyscalls = %+v, want asset_execute", manifest.RequiredSyscalls)
	}
}

func TestAssembleWithManifestRejectsMissingSyscallDeclaration(t *testing.T) {
	source := `
syscall asset_execute
exit
`
	_, err := AssembleWithManifest(source, vm.ProgramManifest{
		ComputeUnitLimit: 100_000,
		RequiredSyscalls: []vm.SyscallID{
			vm.SyscallLog,
		},
	})
	if err == nil {
		t.Fatal("AssembleWithManifest() error = nil, want missing syscall error")
	}
}

func testAddress(seed byte) vm.Address {
	var address vm.Address
	for index := range address {
		address[index] = seed + byte(index)
	}
	return address
}
