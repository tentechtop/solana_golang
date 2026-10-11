package bpfloader

import (
	"bytes"
	"testing"
)

func TestWriteInstructionHonorsSerializedInstructionLimit(t *testing.T) {
	instruction, err := NewWriteInstruction(0, bytes.Repeat([]byte{1}, MaxLoaderInstructionDataBytes))
	if err != nil {
		t.Fatalf("NewWriteInstruction(max) error = %v", err)
	}
	encoded, err := instruction.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary(max) error = %v", err)
	}
	if len(encoded) != MaxLoaderInstructionBytes {
		t.Fatalf("encoded length = %d, want %d", len(encoded), MaxLoaderInstructionBytes)
	}
	if _, err := NewWriteInstruction(0, bytes.Repeat([]byte{1}, MaxLoaderInstructionDataBytes+1)); err == nil {
		t.Fatal("NewWriteInstruction(over max) error = nil")
	}
}
