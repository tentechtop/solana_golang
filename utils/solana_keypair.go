package utils

import (
	"fmt"
)

const (
	// SolanaPrivateKeySeedSize is the size in bytes of an Ed25519 seed.
	SolanaPrivateKeySeedSize = 32
	// SolanaSecretKeySize is the Solana CLI secret key format: 32-byte seed + 32-byte public key.
	SolanaSecretKeySize = 64
)

// SolanaKeyPair stores Solana-style key material.
type SolanaKeyPair struct {
	PublicKey  PublicKey
	PrivateKey []byte
}

// KeyPairFromSeed derives a Solana key pair from a 32-byte Ed25519 seed.
func KeyPairFromSeed(seed []byte) (SolanaKeyPair, error) {
	if err := requireLength(seed, SolanaPrivateKeySeedSize, "solana private key seed"); err != nil {
		return SolanaKeyPair{}, err
	}
	publicKeyBytes, err := DeriveEd25519PublicKeyFromPrivateKey(seed)
	if err != nil {
		return SolanaKeyPair{}, err
	}
	publicKey, err := NewPublicKey(publicKeyBytes)
	if err != nil {
		return SolanaKeyPair{}, err
	}
	return SolanaKeyPair{PublicKey: publicKey, PrivateKey: CloneBytes(seed)}, nil
}

// KeyPairFromSecretKey64 loads the Solana CLI 64-byte secret key format: seed + public key.
func KeyPairFromSecretKey64(secretKey []byte) (SolanaKeyPair, error) {
	if err := requireLength(secretKey, SolanaSecretKeySize, "solana secret key"); err != nil {
		return SolanaKeyPair{}, err
	}
	keyPair, err := KeyPairFromSeed(secretKey[:SolanaPrivateKeySeedSize])
	if err != nil {
		return SolanaKeyPair{}, err
	}
	if !SecureEqual(keyPair.PublicKey[:], secretKey[SolanaPrivateKeySeedSize:]) {
		return SolanaKeyPair{}, fmt.Errorf("utils: solana secret key public key does not match seed")
	}
	return keyPair, nil
}

// ToSecretKey64 converts a 32-byte seed into Solana's 64-byte secret key format.
func ToSecretKey64(seed []byte) ([]byte, error) {
	keyPair, err := KeyPairFromSeed(seed)
	if err != nil {
		return nil, err
	}
	return keyPair.SecretKey64(), nil
}

// SecretKey64 returns the Solana CLI 64-byte secret key format.
func (k SolanaKeyPair) SecretKey64() []byte {
	return ConcatBytes(k.PrivateKey, k.PublicKey[:])
}

// Sign signs data using the key pair's private seed.
func (k SolanaKeyPair) Sign(data []byte) (Signature, error) {
	signature, err := Ed25519Sign(k.PrivateKey, data)
	if err != nil {
		return Signature{}, err
	}
	return NewSignature(signature)
}

// Verify verifies a signature against data using the key pair's public key.
func (k SolanaKeyPair) Verify(data []byte, signature Signature) bool {
	return Ed25519Verify(k.PublicKey[:], data, signature[:])
}
