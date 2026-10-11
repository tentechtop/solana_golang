package runtime

import (
	"strings"
	"testing"

	"github.com/tentechtop/ordexus-core/structure"
)

func TestDefaultProgramExecutionPolicyIncludesShieldedFixedProgram(t *testing.T) {
	policy, err := NewDefaultProgramExecutionPolicy(structure.DefaultBuiltinProgramIDs)
	if err != nil {
		t.Fatalf("NewDefaultProgramExecutionPolicy() error = %v", err)
	}
	if err := policy.Validate(); err != nil {
		t.Fatalf("policy Validate() error = %v", err)
	}
	fingerprint := policy.Fingerprint()
	shieldedEntry := "shielded:" + structure.DefaultBuiltinProgramIDs.Shielded.String()
	if !strings.Contains(fingerprint, "fixed_builtin=") || !strings.Contains(fingerprint, shieldedEntry) {
		t.Fatalf("fingerprint = %q, want shielded fixed builtin", fingerprint)
	}
	if strings.Contains(fingerprint, "vm_bridge="+shieldedEntry) {
		t.Fatalf("fingerprint = %q, shielded program should not be a VM bridge", fingerprint)
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
	programID := structure.DefaultBuiltinProgramIDs.Shielded.String()
	policy := ProgramExecutionPolicy{
		FixedBuiltinPrograms: []ProgramExecutionPolicyEntry{
			{Name: "shielded", ProgramID: programID},
		},
		VirtualMachineProgramOwners: []ProgramExecutionPolicyEntry{
			{Name: "bpf_loader", ProgramID: structure.DefaultBuiltinProgramIDs.BPFLoader.String()},
		},
		VirtualMachineBridgePrograms: []ProgramExecutionPolicyEntry{
			{Name: "shielded", ProgramID: programID},
		},
	}
	if err := policy.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want fixed/vm bridge overlap rejection")
	}
}
