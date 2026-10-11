package vm

import (
	"crypto/sha256"
	"testing"
)

func TestBytecodeProgramsEqualAcceptsLegacyHeader(t *testing.T) {
	code, err := BuildRegisterProgramCode(
		mustRegisterInstructionForCompatibilityTest(t, RegOpMovImm, 0, 7),
	)
	if err != nil {
		t.Fatal(err)
	}
	current, err := EncodeGovernedRegisterBytecode(code, []byte("readonly"), ProgramManifest{
		ComputeUnitLimit: DefaultComputeUnitLimit,
		RequiredSyscalls: []SyscallID{SyscallLog},
	})
	if err != nil {
		t.Fatal(err)
	}
	legacy := append([]byte(nil), current...)
	copy(legacy[:4], legacyBytecodeHeader)
	checksumOffset := len(legacy) - registerChecksumSize
	checksum := sha256.Sum256(legacy[:checksumOffset])
	copy(legacy[checksumOffset:], checksum[:])

	if !BytecodeProgramsEqual(current, legacy) {
		t.Fatal("legacy and current envelopes should describe the same program")
	}
	legacy[checksumOffset] ^= 1
	if BytecodeProgramsEqual(current, legacy) {
		t.Fatal("corrupt legacy checksum was accepted")
	}
}

func mustRegisterInstructionForCompatibilityTest(t *testing.T, opcode byte, destination byte, immediate uint64) []byte {
	t.Helper()
	instruction, err := BuildRegisterInstruction(opcode, destination, 0, 0, immediate)
	if err != nil {
		t.Fatal(err)
	}
	return instruction
}
