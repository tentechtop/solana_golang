package stake

import "testing"

func TestSlashAndJailInstructionsRoundTrip(t *testing.T) {
	slashInstruction, err := NewSlashValidatorInstruction(123)
	if err != nil {
		t.Fatalf("NewSlashValidatorInstruction() error = %v", err)
	}
	slashBytes, err := slashInstruction.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary(slash) error = %v", err)
	}
	decodedSlash, err := UnmarshalInstructionBinary(slashBytes)
	if err != nil {
		t.Fatalf("UnmarshalInstructionBinary(slash) error = %v", err)
	}
	if decodedSlash.Type != InstructionSlashValidator || decodedSlash.Amount != 123 {
		t.Fatalf("decoded slash = %+v", decodedSlash)
	}

	jailInstruction, err := NewJailValidatorInstruction(9)
	if err != nil {
		t.Fatalf("NewJailValidatorInstruction() error = %v", err)
	}
	jailBytes, err := jailInstruction.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary(jail) error = %v", err)
	}
	decodedJail, err := UnmarshalInstructionBinary(jailBytes)
	if err != nil {
		t.Fatalf("UnmarshalInstructionBinary(jail) error = %v", err)
	}
	if decodedJail.Type != InstructionJailValidator || decodedJail.UnlockEpoch != 9 {
		t.Fatalf("decoded jail = %+v", decodedJail)
	}
}

func TestSlashInstructionRejectsZeroAmount(t *testing.T) {
	if _, err := NewSlashValidatorInstruction(0); err == nil {
		t.Fatal("NewSlashValidatorInstruction(0) error = nil, want error")
	}
}
