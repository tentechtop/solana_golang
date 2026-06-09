package structure

import (
	"bytes"
	"testing"
)

func TestSolanaKeyPairSecretKey64(t *testing.T) {
	seed := bytes.Repeat([]byte{0x09}, SolanaPrivateKeySeedSize)
	keyPair, err := KeyPairFromSeed(seed)
	if err != nil {
		t.Fatalf("KeyPairFromSeed() error = %v", err)
	}
	if !bytes.Equal(keyPair.PrivateKey, seed) {
		t.Fatalf("private key = %x, want %x", keyPair.PrivateKey, seed)
	}

	secretKey := keyPair.SecretKey64()
	if len(secretKey) != SolanaSecretKeySize {
		t.Fatalf("SecretKey64() length = %d, want %d", len(secretKey), SolanaSecretKeySize)
	}
	loaded, err := KeyPairFromSecretKey64(secretKey)
	if err != nil {
		t.Fatalf("KeyPairFromSecretKey64() error = %v", err)
	}
	if !loaded.PublicKey.Equal(keyPair.PublicKey) || !bytes.Equal(loaded.PrivateKey, keyPair.PrivateKey) {
		t.Fatal("loaded key pair differs from original")
	}

	signature, err := keyPair.Sign([]byte("payload"))
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	if !keyPair.Verify([]byte("payload"), signature) {
		t.Fatal("Verify() = false, want true")
	}
	if keyPair.Verify([]byte("tampered"), signature) {
		t.Fatal("Verify(tampered) = true, want false")
	}
}

func TestSolanaKeyPairInvalidInput(t *testing.T) {
	if _, err := KeyPairFromSeed([]byte{1, 2, 3}); err == nil {
		t.Fatal("KeyPairFromSeed(short) error = nil, want error")
	}
	if _, err := KeyPairFromSecretKey64([]byte{1, 2, 3}); err == nil {
		t.Fatal("KeyPairFromSecretKey64(short) error = nil, want error")
	}

	seed := bytes.Repeat([]byte{0x09}, SolanaPrivateKeySeedSize)
	secretKey, err := ToSecretKey64(seed)
	if err != nil {
		t.Fatalf("ToSecretKey64() error = %v", err)
	}
	secretKey[SolanaPrivateKeySeedSize] ^= 0xff
	if _, err := KeyPairFromSecretKey64(secretKey); err == nil {
		t.Fatal("KeyPairFromSecretKey64(mismatched public key) error = nil, want error")
	}
}
