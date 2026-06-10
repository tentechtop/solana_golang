package utils

import (
	"bytes"
	"crypto/ed25519"
	"testing"
)

func TestGenerateEd25519KeyPair(t *testing.T) {
	keyPair, err := GenerateEd25519KeyPair()
	if err != nil {
		t.Fatalf("GenerateEd25519KeyPair() error = %v", err)
	}
	if len(keyPair.PublicKey) != Ed25519KeySize {
		t.Fatalf("public key length = %d, want %d", len(keyPair.PublicKey), Ed25519KeySize)
	}
	if len(keyPair.PrivateKey) != Ed25519KeySize {
		t.Fatalf("private key length = %d, want %d", len(keyPair.PrivateKey), Ed25519KeySize)
	}

	publicKey, privateKey, err := GenerateEd25519KeyPairBytes()
	if err != nil {
		t.Fatalf("GenerateEd25519KeyPairBytes() error = %v", err)
	}
	if len(publicKey) != Ed25519KeySize || len(privateKey) != Ed25519KeySize {
		t.Fatalf("GenerateEd25519KeyPairBytes() lengths = (%d, %d), want (%d, %d)",
			len(publicKey), len(privateKey), Ed25519KeySize, Ed25519KeySize)
	}
}
func TestEd25519DeriveSignAndVerify(t *testing.T) {
	seed := bytes.Repeat([]byte{0x01}, Ed25519KeySize)
	data := []byte("solana transaction message")

	publicKey, err := DeriveEd25519PublicKeyFromPrivateKey(seed)
	if err != nil {
		t.Fatalf("DeriveEd25519PublicKeyFromPrivateKey() error = %v", err)
	}
	wantPublicKey := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	if !bytes.Equal(publicKey, wantPublicKey) {
		t.Fatalf("derived public key = %x, want %x", publicKey, wantPublicKey)
	}

	signature, err := Ed25519Sign(seed, data)
	if err != nil {
		t.Fatalf("Ed25519Sign() error = %v", err)
	}
	if len(signature) != Ed25519SignatureSize {
		t.Fatalf("signature length = %d, want %d", len(signature), Ed25519SignatureSize)
	}
	if !Ed25519Verify(publicKey, data, signature) {
		t.Fatal("Ed25519Verify() = false, want true")
	}
	if Ed25519Verify(publicKey, []byte("tampered"), signature) {
		t.Fatal("Ed25519Verify(tampered data) = true, want false")
	}
	if Ed25519Verify(bytes.Repeat([]byte{0x02}, Ed25519KeySize), data, signature) {
		t.Fatal("Ed25519Verify(wrong public key) = true, want false")
	}
}
func TestEd25519RFC8032Vector(t *testing.T) {
	seed, err := HexToBytes("9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60")
	if err != nil {
		t.Fatalf("HexToBytes(seed) error = %v", err)
	}
	wantPublicKey, err := HexToBytes("d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a")
	if err != nil {
		t.Fatalf("HexToBytes(public key) error = %v", err)
	}
	wantSignature, err := HexToBytes("e5564300c360ac729086e2cc806e828a84877f1eb8e5d974d873e065224901555fb8821590a33bacc61e39701cf9b46bd25bf5f0595bbe24655141438e7a100b")
	if err != nil {
		t.Fatalf("HexToBytes(signature) error = %v", err)
	}

	publicKey, err := DeriveEd25519PublicKeyFromPrivateKey(seed)
	if err != nil {
		t.Fatalf("DeriveEd25519PublicKeyFromPrivateKey() error = %v", err)
	}
	if !bytes.Equal(publicKey, wantPublicKey) {
		t.Fatalf("public key = %x, want %x", publicKey, wantPublicKey)
	}

	signature, err := Ed25519Sign(seed, nil)
	if err != nil {
		t.Fatalf("Ed25519Sign() error = %v", err)
	}
	if !bytes.Equal(signature, wantSignature) {
		t.Fatalf("signature = %x, want %x", signature, wantSignature)
	}
	if !Ed25519Verify(publicKey, nil, signature) {
		t.Fatal("Ed25519Verify() = false, want true")
	}
}
func TestEd25519Aliases(t *testing.T) {
	keyPair, err := GenerateEd25519KeyPair()
	if err != nil {
		t.Fatalf("GenerateEd25519KeyPair() error = %v", err)
	}
	data := []byte("alias test")

	signature, err := ApplyEd25519Signature(keyPair.PrivateKey, data)
	if err != nil {
		t.Fatalf("ApplyEd25519Signature() error = %v", err)
	}
	if !VerifyEd25519Signature(keyPair.PublicKey, data, signature) {
		t.Fatal("VerifyEd25519Signature() = false, want true")
	}

	fastSignature, err := FastEd25519Sign(keyPair.PrivateKey, data)
	if err != nil {
		t.Fatalf("FastEd25519Sign() error = %v", err)
	}
	if !FastEd25519Verify(keyPair.PublicKey, data, fastSignature) {
		t.Fatal("FastEd25519Verify() = false, want true")
	}
}
func TestEd25519InvalidInput(t *testing.T) {
	if _, err := DeriveEd25519PublicKeyFromPrivateKey([]byte{1, 2, 3}); err == nil {
		t.Fatal("DeriveEd25519PublicKeyFromPrivateKey(short key) error = nil, want error")
	}
	if _, err := Ed25519Sign([]byte{1, 2, 3}, []byte("data")); err == nil {
		t.Fatal("Ed25519Sign(short key) error = nil, want error")
	}
	if Ed25519Verify([]byte{1, 2, 3}, []byte("data"), bytes.Repeat([]byte{0}, Ed25519SignatureSize)) {
		t.Fatal("Ed25519Verify(short public key) = true, want false")
	}
	if Ed25519Verify(bytes.Repeat([]byte{0}, Ed25519KeySize), []byte("data"), []byte{1, 2, 3}) {
		t.Fatal("Ed25519Verify(short signature) = true, want false")
	}
}
