package shielded

import (
	"errors"
	"testing"

	"solana_golang/runtime"
	"solana_golang/structure"
)

func TestExecuteRejectsInsufficientComputeBudgetBeforeVerification(t *testing.T) {
	computeUnitsUsed := uint64(0)
	computeBudget := structure.DefaultComputeBudgetLimits()
	computeBudget.MaxComputeUnits = structure.ShieldedGroth16VerificationComputeUnits - 1

	err := (Program{}).Execute(runtime.InstructionContext{
		ComputeBudget:    computeBudget,
		ComputeUnitsUsed: &computeUnitsUsed,
	})
	if !errors.Is(err, structure.ErrInvalidFee) {
		t.Fatalf("Execute() error = %v, want ErrInvalidFee", err)
	}
	if computeUnitsUsed != 0 {
		t.Fatalf("compute units = %d, want zero before Groth16 verification", computeUnitsUsed)
	}
}
