package runtime

import (
	"strings"
	"testing"

	"solana_golang/structure"
)

func TestDefaultProgramExecutionPolicySeparatesPrivacyModes(t *testing.T) {
	fixedPolicy, err := NewDefaultProgramExecutionPolicy(structure.DefaultBuiltinProgramIDs, PrivacyExecutionModeFixed)
	if err != nil {
		t.Fatalf("NewDefaultProgramExecutionPolicy(fixed) error = %v", err)
	}
	if err := fixedPolicy.Validate(); err != nil {
		t.Fatalf("fixed policy Validate() error = %v", err)
	}
	fixedFingerprint := fixedPolicy.Fingerprint()
	privacyEntry := "privacy:" + structure.DefaultBuiltinProgramIDs.Privacy.String()
	if !strings.Contains(fixedFingerprint, "fixed_builtin=") || !strings.Contains(fixedFingerprint, privacyEntry) {
		t.Fatalf("fixed fingerprint = %q, want privacy fixed builtin", fixedFingerprint)
	}
	if strings.Contains(fixedFingerprint, "vm_bridge="+privacyEntry) {
		t.Fatalf("fixed fingerprint = %q, privacy should not be vm bridge", fixedFingerprint)
	}

	virtualMachinePolicy, err := NewDefaultProgramExecutionPolicy(structure.DefaultBuiltinProgramIDs, PrivacyExecutionModeVMSyscall)
	if err != nil {
		t.Fatalf("NewDefaultProgramExecutionPolicy(vm_syscall) error = %v", err)
	}
	if err := virtualMachinePolicy.Validate(); err != nil {
		t.Fatalf("vm policy Validate() error = %v", err)
	}
	virtualMachineFingerprint := virtualMachinePolicy.Fingerprint()
	if !strings.Contains(virtualMachineFingerprint, "vm_bridge="+privacyEntry) {
		t.Fatalf("vm fingerprint = %q, want privacy vm bridge", virtualMachineFingerprint)
	}
	if fixedFingerprint == virtualMachineFingerprint {
		t.Fatalf("fingerprints both %q, want mode-specific policies", fixedFingerprint)
	}
}

func TestProgramExecutionPolicyFingerprintIsStable(t *testing.T) {
	programIDs := structure.DefaultBuiltinProgramIDs
	policy := ProgramExecutionPolicy{
		FixedBuiltinPrograms: []ProgramExecutionPolicyEntry{
			{Name: "token", ProgramID: programIDs.Token.String()},
			{Name: "system", ProgramID: programIDs.System.String()},
			{Name: "token", ProgramID: programIDs.Token.String()},
		},
		VirtualMachineProgramOwners: []ProgramExecutionPolicyEntry{
			{Name: "bpf_loader", ProgramID: programIDs.BPFLoader.String()},
		},
	}
	fingerprint := policy.Fingerprint()
	expectedPrefix := ProgramExecutionPolicyVersion + "|fixed_builtin=system:" + programIDs.System.String() + ",token:" + programIDs.Token.String()
	if !strings.HasPrefix(fingerprint, expectedPrefix) {
		t.Fatalf("fingerprint = %q, want prefix %q", fingerprint, expectedPrefix)
	}
}

func TestProgramExecutionPolicyRejectsFixedBridgeOverlap(t *testing.T) {
	programID := structure.DefaultBuiltinProgramIDs.Privacy.String()
	policy := ProgramExecutionPolicy{
		FixedBuiltinPrograms: []ProgramExecutionPolicyEntry{
			{Name: "privacy", ProgramID: programID},
		},
		VirtualMachineProgramOwners: []ProgramExecutionPolicyEntry{
			{Name: "bpf_loader", ProgramID: structure.DefaultBuiltinProgramIDs.BPFLoader.String()},
		},
		VirtualMachineBridgePrograms: []ProgramExecutionPolicyEntry{
			{Name: "privacy", ProgramID: programID},
		},
	}
	if err := policy.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want fixed/vm bridge overlap rejection")
	}
}
