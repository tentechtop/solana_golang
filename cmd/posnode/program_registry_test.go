package main

import (
	"testing"

	"solana_golang/runtime"
	"solana_golang/structure"
)

func TestRegisterProgramsRegistersBuiltinHandlers(t *testing.T) {
	executor := runtime.NewFixedExecutorWithRegistry(runtime.NewProgramHandlerRegistry())
	if err := registerPrograms(&executor); err != nil {
		t.Fatalf("registerPrograms() error = %v", err)
	}

	expectedPrograms := map[string]structure.PublicKey{
		"system":  structure.DefaultBuiltinProgramIDs.System,
		"token":   structure.DefaultBuiltinProgramIDs.Token,
		"stake":   structure.DefaultBuiltinProgramIDs.Stake,
		"privacy": structure.DefaultBuiltinProgramIDs.Privacy,
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

func TestRegisterProgramsSupportsPrivacyVMSyscallMode(t *testing.T) {
	executor := runtime.NewFixedExecutorWithRegistry(runtime.NewProgramHandlerRegistry())
	if err := registerProgramsWithPrivacyMode(&executor, runtime.PrivacyExecutionModeVMSyscall); err != nil {
		t.Fatalf("registerProgramsWithPrivacyMode() error = %v", err)
	}

	spec, exists := executor.Programs.Spec(structure.DefaultBuiltinProgramIDs.Privacy)
	if !exists {
		t.Fatal("privacy program should be registered")
	}
	if spec.Name != "privacy" {
		t.Fatalf("privacy spec name = %q, want privacy", spec.Name)
	}
}

func TestRegisterProgramsRejectsInvalidPrivacyMode(t *testing.T) {
	executor := runtime.NewFixedExecutorWithRegistry(runtime.NewProgramHandlerRegistry())
	if err := registerProgramsWithPrivacyMode(&executor, runtime.PrivacyExecutionMode("bad-mode")); err == nil {
		t.Fatal("registerProgramsWithPrivacyMode() error = nil, want invalid mode rejection")
	}
}
