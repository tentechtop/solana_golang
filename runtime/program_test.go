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
