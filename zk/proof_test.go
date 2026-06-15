package zk

import (
	"errors"
	"testing"
)

type testVerifier struct {
	system ProofSystem
	valid  bool
}

func (verifier testVerifier) System() ProofSystem {
	return verifier.system
}

func (verifier testVerifier) Verify(request VerificationRequest) (VerificationResult, error) {
	if err := request.Validate(); err != nil {
		return failedVerification(request.Envelope, err.Error()), err
	}
	if !verifier.valid {
		return failedVerification(request.Envelope, ErrVerificationFailed.Error()), ErrVerificationFailed
	}
	return VerificationResult{
		Valid:   true,
		System:  request.Envelope.System,
		Curve:   request.Envelope.Curve,
		Circuit: request.Envelope.Circuit,
		Message: "ok",
	}, nil
}

func TestRecommendedPrivacyProofTargetUsesGroth16BN254(t *testing.T) {
	target := RecommendedPrivacyProofTarget()
	if target.System != ProofSystemGroth16BN254 {
		t.Fatalf("System = %d, want Groth16 BN254", target.System)
	}
	if target.Curve != CurveBN254 {
		t.Fatalf("Curve = %d, want BN254", target.Curve)
	}
	if !target.TrustedSetupRequired || !target.PerCircuitSetupRequired {
		t.Fatal("Groth16 target must declare trusted setup requirements")
	}
}

func TestProofEnvelopeBindsPublicInputsAndCopiesProof(t *testing.T) {
	publicInputs, err := NewPublicInputSet([][]byte{[]byte("root"), []byte("nullifier")})
	if err != nil {
		t.Fatalf("NewPublicInputSet() error = %v", err)
	}
	proof := []byte("proof-bytes")
	envelope, err := NewProofEnvelope(ProofEnvelopeParams{
		System:           ProofSystemGroth16BN254,
		Curve:            CurveBN254,
		Circuit:          CircuitPrivacyTransfer,
		VerifyingKeyHash: HashBytes([]byte("vk")),
		PublicInputs:     publicInputs,
		Proof:            proof,
	})
	if err != nil {
		t.Fatalf("NewProofEnvelope() error = %v", err)
	}
	proof[0] = 'X'
	if string(envelope.Proof) != "proof-bytes" {
		t.Fatalf("envelope proof mutated to %q", string(envelope.Proof))
	}

	firstHash, err := envelope.CanonicalHash()
	if err != nil {
		t.Fatalf("CanonicalHash() error = %v", err)
	}
	secondHash, err := envelope.Clone().CanonicalHash()
	if err != nil {
		t.Fatalf("Clone().CanonicalHash() error = %v", err)
	}
	if firstHash != secondHash {
		t.Fatal("canonical hash changed after clone")
	}
}

func TestVerificationRequestRejectsPublicInputHashMismatch(t *testing.T) {
	publicInputs, err := NewPublicInputSet([][]byte{[]byte("expected")})
	if err != nil {
		t.Fatalf("NewPublicInputSet() error = %v", err)
	}
	envelope, err := NewProofEnvelope(ProofEnvelopeParams{
		System:           ProofSystemGroth16BN254,
		Curve:            CurveBN254,
		Circuit:          CircuitPrivacyWithdraw,
		VerifyingKeyHash: HashBytes([]byte("vk")),
		PublicInputs:     publicInputs,
		Proof:            []byte("proof"),
	})
	if err != nil {
		t.Fatalf("NewProofEnvelope() error = %v", err)
	}
	wrongInputs, err := NewPublicInputSet([][]byte{[]byte("wrong")})
	if err != nil {
		t.Fatalf("NewPublicInputSet(wrong) error = %v", err)
	}

	err = (VerificationRequest{Envelope: envelope, PublicInputs: wrongInputs}).Validate()
	if !errors.Is(err, ErrPublicInputHashMismatch) {
		t.Fatalf("Validate() error = %v, want ErrPublicInputHashMismatch", err)
	}
}

func TestVerifierRegistryDispatchesVerifier(t *testing.T) {
	request := newTestVerificationRequest(t)
	registry, err := NewVerifierRegistry(testVerifier{system: ProofSystemGroth16BN254, valid: true})
	if err != nil {
		t.Fatalf("NewVerifierRegistry() error = %v", err)
	}

	result, err := registry.Verify(request)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if !result.Valid || result.System != ProofSystemGroth16BN254 || result.Circuit != CircuitPrivacyTransfer {
		t.Fatalf("result = %+v, want valid Groth16 transfer", result)
	}
}

func TestRejectVerifierFailsClosed(t *testing.T) {
	request := newTestVerificationRequest(t)
	registry, err := NewVerifierRegistry(RejectVerifier{ProofSystem: ProofSystemGroth16BN254})
	if err != nil {
		t.Fatalf("NewVerifierRegistry() error = %v", err)
	}

	result, err := registry.Verify(request)
	if !errors.Is(err, ErrVerifierUnavailable) {
		t.Fatalf("Verify() error = %v, want ErrVerifierUnavailable", err)
	}
	if result.Valid {
		t.Fatal("RejectVerifier returned valid result")
	}
}

func TestValidateOptionalProofBytesRejectsOversizedProof(t *testing.T) {
	err := ValidateOptionalProofBytes(make([]byte, 3), 2)
	if !errors.Is(err, ErrInvalidProof) {
		t.Fatalf("ValidateOptionalProofBytes() error = %v, want ErrInvalidProof", err)
	}
}

func newTestVerificationRequest(t *testing.T) VerificationRequest {
	t.Helper()

	publicInputs, err := NewPublicInputSet([][]byte{[]byte("root"), []byte("nullifier"), []byte("output")})
	if err != nil {
		t.Fatalf("NewPublicInputSet() error = %v", err)
	}
	envelope, err := NewProofEnvelope(ProofEnvelopeParams{
		System:           ProofSystemGroth16BN254,
		Curve:            CurveBN254,
		Circuit:          CircuitPrivacyTransfer,
		VerifyingKeyHash: HashBytes([]byte("vk")),
		PublicInputs:     publicInputs,
		Proof:            []byte("proof"),
	})
	if err != nil {
		t.Fatalf("NewProofEnvelope() error = %v", err)
	}
	return VerificationRequest{Envelope: envelope, PublicInputs: publicInputs}
}
