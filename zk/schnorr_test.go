package zk

import (
	"errors"
	"testing"
)

func TestSchnorrProofBytesVerify(t *testing.T) {
	keyPair, err := GenerateSchnorrKeyPair()
	if err != nil {
		t.Fatalf("GenerateSchnorrKeyPair() error = %v", err)
	}
	message := []byte("privacy spend message")
	proofBytes, err := NewSchnorrProofBytes(keyPair.PrivateScalar, message)
	if err != nil {
		t.Fatalf("NewSchnorrProofBytes() error = %v", err)
	}
	publicKeyDigest, err := SchnorrPublicKeyDigest(keyPair.PublicKey)
	if err != nil {
		t.Fatalf("SchnorrPublicKeyDigest() error = %v", err)
	}

	if err := VerifySchnorrProofBytes(proofBytes, message, publicKeyDigest); err != nil {
		t.Fatalf("VerifySchnorrProofBytes() error = %v", err)
	}
}

func TestSchnorrProofRejectsWrongMessage(t *testing.T) {
	keyPair, err := GenerateSchnorrKeyPair()
	if err != nil {
		t.Fatalf("GenerateSchnorrKeyPair() error = %v", err)
	}
	proofBytes, err := NewSchnorrProofBytes(keyPair.PrivateScalar, []byte("expected message"))
	if err != nil {
		t.Fatalf("NewSchnorrProofBytes() error = %v", err)
	}
	publicKeyDigest, err := SchnorrPublicKeyDigest(keyPair.PublicKey)
	if err != nil {
		t.Fatalf("SchnorrPublicKeyDigest() error = %v", err)
	}

	err = VerifySchnorrProofBytes(proofBytes, []byte("wrong message"), publicKeyDigest)
	if !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("VerifySchnorrProofBytes() error = %v, want ErrVerificationFailed", err)
	}
}

func TestSchnorrProofRejectsPublicKeyDigestMismatch(t *testing.T) {
	keyPair, err := GenerateSchnorrKeyPair()
	if err != nil {
		t.Fatalf("GenerateSchnorrKeyPair() error = %v", err)
	}
	otherKeyPair, err := GenerateSchnorrKeyPair()
	if err != nil {
		t.Fatalf("GenerateSchnorrKeyPair(other) error = %v", err)
	}
	message := []byte("privacy spend message")
	proofBytes, err := NewSchnorrProofBytes(keyPair.PrivateScalar, message)
	if err != nil {
		t.Fatalf("NewSchnorrProofBytes() error = %v", err)
	}
	otherDigest, err := SchnorrPublicKeyDigest(otherKeyPair.PublicKey)
	if err != nil {
		t.Fatalf("SchnorrPublicKeyDigest(other) error = %v", err)
	}

	err = VerifySchnorrProofBytes(proofBytes, message, otherDigest)
	if !errors.Is(err, ErrVerificationFailed) {
		t.Fatalf("VerifySchnorrProofBytes() error = %v, want ErrVerificationFailed", err)
	}
}

func TestSchnorrProofMarshalRejectsTrailingBytes(t *testing.T) {
	keyPair, err := GenerateSchnorrKeyPair()
	if err != nil {
		t.Fatalf("GenerateSchnorrKeyPair() error = %v", err)
	}
	proofBytes, err := NewSchnorrProofBytes(keyPair.PrivateScalar, []byte("message"))
	if err != nil {
		t.Fatalf("NewSchnorrProofBytes() error = %v", err)
	}
	proofBytes = append(proofBytes, 1)

	_, err = UnmarshalSchnorrProofBinary(proofBytes)
	if !errors.Is(err, ErrInvalidProof) {
		t.Fatalf("UnmarshalSchnorrProofBinary() error = %v, want ErrInvalidProof", err)
	}
}
