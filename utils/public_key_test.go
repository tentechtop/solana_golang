package utils

import (
	"bytes"
	"testing"
)

func TestPublicKeyHashAndSignatureTypes(t *testing.T) {
	keyBytes := bytes.Repeat([]byte{0x01}, PublicKeySize)
	key, err := NewPublicKey(keyBytes)
	if err != nil {
		t.Fatalf("NewPublicKey() error = %v", err)
	}
	fromBytes, err := PublicKeyFromBytes(keyBytes)
	if err != nil {
		t.Fatalf("PublicKeyFromBytes() error = %v", err)
	}
	if fromBytes != key {
		t.Fatal("PublicKeyFromBytes() != NewPublicKey()")
	}
	if !bytes.Equal(key.Bytes(), keyBytes) {
		t.Fatalf("PublicKey.Bytes() = %x, want %x", key.Bytes(), keyBytes)
	}
	if key.String() != Base58Encode(keyBytes) {
		t.Fatalf("PublicKey.String() = %q, want %q", key.String(), Base58Encode(keyBytes))
	}
	if key.Hex() != BytesToHex(keyBytes) {
		t.Fatalf("PublicKey.Hex() = %q, want %q", key.Hex(), BytesToHex(keyBytes))
	}

	fromBase58, err := PublicKeyFromBase58(key.String())
	if err != nil {
		t.Fatalf("PublicKeyFromBase58() error = %v", err)
	}
	if !key.Equal(fromBase58) {
		t.Fatal("PublicKeyFromBase58() != original")
	}

	fromHex, err := PublicKeyFromHex(key.Hex())
	if err != nil {
		t.Fatalf("PublicKeyFromHex() error = %v", err)
	}
	if !key.Equal(fromHex) {
		t.Fatal("PublicKeyFromHex() != original")
	}

	hash, err := NewHash(keyBytes)
	if err != nil {
		t.Fatalf("NewHash() error = %v", err)
	}
	if hash.String() != key.String() {
		t.Fatal("Hash.String() does not match Base58 key bytes")
	}
	if hash.Hex() != BytesToHex(keyBytes) {
		t.Fatalf("Hash.Hex() = %q, want %q", hash.Hex(), BytesToHex(keyBytes))
	}
	hashFromBase58, err := HashFromBase58(hash.String())
	if err != nil {
		t.Fatalf("HashFromBase58() error = %v", err)
	}
	if !bytes.Equal(hashFromBase58.Bytes(), hash.Bytes()) {
		t.Fatal("HashFromBase58() != original")
	}

	signatureBytes := bytes.Repeat([]byte{0x02}, SignatureSize)
	signature, err := NewSignature(signatureBytes)
	if err != nil {
		t.Fatalf("NewSignature() error = %v", err)
	}
	signatureFromBytes, err := SignatureFromBytes(signatureBytes)
	if err != nil {
		t.Fatalf("SignatureFromBytes() error = %v", err)
	}
	if signatureFromBytes != signature {
		t.Fatal("SignatureFromBytes() != NewSignature()")
	}
	if !bytes.Equal(signature.Bytes(), signatureBytes) {
		t.Fatal("Signature.Bytes() != original bytes")
	}
	if signature.String() != Base58Encode(signatureBytes) {
		t.Fatalf("Signature.String() = %q, want %q", signature.String(), Base58Encode(signatureBytes))
	}
	signatureFromBase58, err := SignatureFromBase58(signature.String())
	if err != nil {
		t.Fatalf("SignatureFromBase58() error = %v", err)
	}
	if !signature.Equal(signatureFromBase58) {
		t.Fatal("SignatureFromBase58() != original")
	}
	signatureFromHex, err := SignatureFromHex(signature.Hex())
	if err != nil {
		t.Fatalf("SignatureFromHex() error = %v", err)
	}
	if !signature.Equal(signatureFromHex) {
		t.Fatal("SignatureFromHex() != original")
	}
	if MustPublicKeyFromBase58(key.String()) != key {
		t.Fatal("MustPublicKeyFromBase58() != original")
	}

	if _, err := NewPublicKey([]byte{1, 2, 3}); err == nil {
		t.Fatal("NewPublicKey(short) error = nil, want error")
	}
	if _, err := NewSignature([]byte{1, 2, 3}); err == nil {
		t.Fatal("NewSignature(short) error = nil, want error")
	}
	if !(PublicKey{}).IsZero() {
		t.Fatal("zero public key IsZero() = false, want true")
	}
}
