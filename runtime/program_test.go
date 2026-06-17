package runtime

import (
	"context"
	"errors"
	"testing"

	"solana_golang/structure"
)

type fakeProgram struct {
	id       structure.PublicKey
	execute  func() error
	executed bool
}

func (program *fakeProgram) ProgramID() ProgramID {
	return program.id
}

func (program *fakeProgram) Execute(context InstructionContext) error {
	program.executed = true
	if program.execute == nil {
		return nil
	}
	return program.execute()
}

func TestProgramRegistryRejectsInvalidPrograms(t *testing.T) {
	if _, err := NewProgramRegistry(nil); err == nil {
		t.Fatal("NewProgramRegistry(nil) should fail")
	}

	duplicatedID := testProgramID(7)
	_, err := NewProgramRegistry(&fakeProgram{id: duplicatedID}, &fakeProgram{id: duplicatedID})
	if err == nil {
		t.Fatal("NewProgramRegistry(duplicate) should fail")
	}

	registry := NewProgramHandlerRegistry()
	if err := registry.RegisterHandler(ProgramSpec{}, func(context InstructionContext) error { return nil }); err == nil {
		t.Fatal("RegisterHandler(empty spec) should fail")
	}
	if err := registry.RegisterHandler(ProgramSpec{ID: testProgramID(8), Name: "program"}, nil); err == nil {
		t.Fatal("RegisterHandler(nil handler) should fail")
	}
}

func TestProgramRegistryExecutesProgram(t *testing.T) {
	expectedError := errors.New("program failed")
	program := &fakeProgram{
		id:      testProgramID(9),
		execute: func() error { return expectedError },
	}
	registry, err := NewProgramRegistry(program)
	if err != nil {
		t.Fatalf("NewProgramRegistry() error = %v", err)
	}

	err = registry.Execute(InstructionContext{
		Instruction: structure.CompiledInstruction{ProgramIDIndex: 0},
		Message:     structure.ResolvedMessage{AccountKeys: []structure.PublicKey{program.id}},
	})
	if !errors.Is(err, expectedError) {
		t.Fatalf("Execute() error = %v, want %v", err, expectedError)
	}
	if !program.executed {
		t.Fatal("program was not executed")
	}
}

func TestProgramRegistryExecutesHandler(t *testing.T) {
	expectedError := errors.New("handler failed")
	programID := testProgramID(11)
	registry := NewProgramHandlerRegistry()
	err := registry.RegisterHandler(ProgramSpec{ID: programID, Name: "Test/Program"}, func(context InstructionContext) error {
		return expectedError
	})
	if err != nil {
		t.Fatalf("RegisterHandler() error = %v", err)
	}

	spec, exists := registry.Spec(programID)
	if !exists {
		t.Fatal("Spec() should find program")
	}
	if spec.Name != "Test/Program" {
		t.Fatalf("Spec().Name = %q, want trimmed name", spec.Name)
	}
	if _, exists := registry.SpecByName(" Test/Program "); !exists {
		t.Fatal("SpecByName() should use normalized name")
	}

	err = registry.Execute(InstructionContext{
		Instruction: structure.CompiledInstruction{ProgramIDIndex: 0},
		Message:     structure.ResolvedMessage{AccountKeys: []structure.PublicKey{programID}},
	})
	if !errors.Is(err, expectedError) {
		t.Fatalf("Execute() error = %v, want %v", err, expectedError)
	}
}

func TestProgramRegistryRejectsDuplicateHandlerName(t *testing.T) {
	registry := NewProgramHandlerRegistry()
	handler := func(context InstructionContext) error { return nil }
	if err := registry.RegisterHandler(ProgramSpec{ID: testProgramID(12), Name: "system"}, handler); err != nil {
		t.Fatalf("RegisterHandler(first) error = %v", err)
	}
	err := registry.RegisterHandler(ProgramSpec{ID: testProgramID(13), Name: " system "}, handler)
	if err == nil {
		t.Fatal("RegisterHandler(duplicate name) should fail")
	}
}

func TestFixedExecutorRejectsCanceledContext(t *testing.T) {
	contextValue, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := FixedExecutor{}.ExecuteTransaction(contextValue, TransactionRequest{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ExecuteTransaction() error = %v, want context canceled", err)
	}
}

func testProgramID(seed byte) structure.PublicKey {
	var publicKey structure.PublicKey
	publicKey[0] = seed
	return publicKey
}
