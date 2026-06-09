package structure

import (
	"bytes"
	"testing"
)

func TestPublicKeyHashAndSignatureValues(t *testing.T) {
	keyBytes := bytes.Repeat([]byte{0x01}, PublicKeySize)
	publicKey, err := NewPublicKey(keyBytes)
	if err != nil {
		t.Fatalf("NewPublicKey() error = %v", err)
	}
	if !bytes.Equal(publicKey.Bytes(), keyBytes) {
		t.Fatal("PublicKey.Bytes() != original")
	}
	if publicKey.IsZero() {
		t.Fatal("PublicKey.IsZero() = true, want false")
	}

	hash, err := NewHash(keyBytes)
	if err != nil {
		t.Fatalf("NewHash() error = %v", err)
	}
	hashFromBase58, err := HashFromBase58(hash.String())
	if err != nil {
		t.Fatalf("HashFromBase58() error = %v", err)
	}
	if hashFromBase58 != hash {
		t.Fatal("HashFromBase58() != original")
	}

	signatureBytes := bytes.Repeat([]byte{0x02}, SignatureSize)
	signature, err := NewSignature(signatureBytes)
	if err != nil {
		t.Fatalf("NewSignature() error = %v", err)
	}
	if !signature.Equal(signature) {
		t.Fatal("Signature.Equal() = false, want true")
	}
}
