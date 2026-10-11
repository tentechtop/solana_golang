package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tentechtop/ordexus-core/vm"
)

func TestRunBuildsGovernedBytecodeWhenManifestExists(t *testing.T) {
	tempDir := t.TempDir()
	sourcePath := filepath.Join(tempDir, "contract.ovmasm")
	manifestPath := filepath.Join(tempDir, "contract.ovm.json")
	outputPath := filepath.Join(tempDir, "dist", "contract.ovmbin")

	writeTestFile(t, sourcePath, "syscall asset_execute\nexit\n")
	writeTestFile(t, manifestPath, `{
  "name": "contract",
  "version": "0.1.0",
  "compute_unit_limit": 123456,
  "required_syscalls": ["asset_execute"],
  "upgrade_authority": ""
}
`)

	if err := run([]string{"ovmasm", sourcePath, outputPath}); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	bytecode, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	manifest, ok, err := vm.DecodeProgramManifest(bytecode)
	if err != nil {
		t.Fatalf("DecodeProgramManifest() error = %v", err)
	}
	if !ok {
		t.Fatal("DecodeProgramManifest() ok = false, want true")
	}
	if manifest.ComputeUnitLimit != 123456 {
		t.Fatalf("ComputeUnitLimit = %d, want 123456", manifest.ComputeUnitLimit)
	}
	if len(manifest.RequiredSyscalls) != 1 || manifest.RequiredSyscalls[0] != vm.SyscallAssetExecute {
		t.Fatalf("RequiredSyscalls = %+v, want asset_execute", manifest.RequiredSyscalls)
	}
}

func TestRunNoManifestBuildsPlainRegisterBytecode(t *testing.T) {
	tempDir := t.TempDir()
	sourcePath := filepath.Join(tempDir, "contract.ovmasm")
	outputPath := filepath.Join(tempDir, "contract.ovmbin")

	writeTestFile(t, sourcePath, "syscall asset_execute\nexit\n")
	if err := run([]string{"ovmasm", "-no-manifest", sourcePath, outputPath}); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	bytecode, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	_, ok, err := vm.DecodeProgramManifest(bytecode)
	if err != nil {
		t.Fatalf("DecodeProgramManifest() error = %v", err)
	}
	if ok {
		t.Fatal("DecodeProgramManifest() ok = true, want false")
	}
}

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
