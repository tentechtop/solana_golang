package posnode

import (
	"strings"
	"testing"

	"github.com/tentechtop/ordexus-core/runtime"
	"github.com/tentechtop/ordexus-core/structure"
)

func TestRegisterProgramsRegistersBuiltinHandlers(t *testing.T) {
	executor := runtime.NewFixedExecutorWithRegistry(runtime.NewProgramHandlerRegistry())
	if err := registerPrograms(&executor); err != nil {
		t.Fatalf("registerPrograms() error = %v", err)
	}

	expectedPrograms := map[string]structure.PublicKey{
		"system":     structure.DefaultBuiltinProgramIDs.System,
		"token":      structure.DefaultBuiltinProgramIDs.Token,
		"stake":      structure.DefaultBuiltinProgramIDs.Stake,
		"shielded":   structure.DefaultBuiltinProgramIDs.Shielded,
		"bpf_loader": structure.DefaultBuiltinProgramIDs.BPFLoader,
	}
	for expectedName, expectedID := range expectedPrograms {
		spec, exists := executor.Programs.Spec(expectedID)
		if !exists {
			t.Fatalf("Spec(%s) should exist", expectedName)
		}
		if spec.Name != expectedName {
			t.Fatalf("Spec(%s).Name = %q, want %q", expectedName, spec.Name, expectedName)
		}
		namedSpec, exists := executor.Programs.SpecByName(expectedName)
		if !exists {
			t.Fatalf("SpecByName(%s) should exist", expectedName)
		}
		if namedSpec.ID != expectedID {
			t.Fatalf("SpecByName(%s).ID = %s, want %s", expectedName, namedSpec.ID.String(), expectedID.String())
		}
	}
}

func TestNewRuntimeExecutorSetsProgramExecutionPolicy(t *testing.T) {
	executor, err := newRuntimeExecutor(nil)
	if err != nil {
		t.Fatalf("newRuntimeExecutor() error = %v", err)
	}
	fingerprint := executor.ProgramExecutionPolicy.Fingerprint()
	for _, expected := range []string{
		"vm_program_owner=bpf_loader:" + structure.DefaultBuiltinProgramIDs.BPFLoader.String(),
		"shielded:" + structure.DefaultBuiltinProgramIDs.Shielded.String(),
	} {
		if !strings.Contains(fingerprint, expected) {
			t.Fatalf("fingerprint = %q, want %s", fingerprint, expected)
		}
	}
}
