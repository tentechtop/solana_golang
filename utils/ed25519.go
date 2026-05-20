package utils

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
)

const (
	// Ed25519KeySize is the core public/private seed size used by Solana.
	Ed25519KeySize = ed25519.SeedSize
	// Ed25519SignatureSize is the Ed25519 signature size.
	Ed25519SignatureSize = ed25519.SignatureSize
)

// Ed25519KeyPair stores Solana-style 32-byte public key and 32-byte private seed.
type Ed25519KeyPair struct {
	PublicKey  []byte
	PrivateKey []byte
}

// GenerateEd25519KeyPair creates an Ed25519 key pair.
//
// The returned PrivateKey is the 32-byte seed, matching the Java reference's
// "core private key" and Solana key derivation helpers.
func GenerateEd25519KeyPair() (Ed25519KeyPair, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Ed25519KeyPair{}, fmt.Errorf("utils: generate ed25519 key pair: %w", err)
	}
	return Ed25519KeyPair{
		PublicKey:  CloneBytes(publicKey),
		PrivateKey: CloneBytes(privateKey.Seed()),
	}, nil
}

// GenerateEd25519KeyPairBytes creates an Ed25519 key pair and returns raw byte slices.
func GenerateEd25519KeyPairBytes() (publicKey []byte, privateKey []byte, err error) {
	keyPair, err := GenerateEd25519KeyPair()
	if err != nil {
		return nil, nil, err
	}
	return keyPair.PublicKey, keyPair.PrivateKey, nil
}

// DeriveEd25519PublicKeyFromPrivateKey derives the 32-byte public key from a 32-byte private seed.
func DeriveEd25519PublicKeyFromPrivateKey(privateKey []byte) ([]byte, error) {
	if err := requireLength(privateKey, Ed25519KeySize, "ed25519 private key"); err != nil {
		return nil, err
	}
	fullPrivateKey := ed25519.NewKeyFromSeed(privateKey)
	publicKey, ok := fullPrivateKey.Public().(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("utils: derive ed25519 public key")
	}
	return CloneBytes(publicKey), nil
}

// Ed25519Sign signs data with a 32-byte private seed and returns a 64-byte signature.
func Ed25519Sign(privateKey []byte, data []byte) ([]byte, error) {
	if err := requireLength(privateKey, Ed25519KeySize, "ed25519 private key"); err != nil {
		return nil, err
	}
	signature := ed25519.Sign(ed25519.NewKeyFromSeed(privateKey), data)
	return CloneBytes(signature), nil
}

// Ed25519Verify verifies a 64-byte signature with a 32-byte public key.
func Ed25519Verify(publicKey []byte, data []byte, signature []byte) bool {
	if len(publicKey) != Ed25519KeySize || len(signature) != Ed25519SignatureSize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(publicKey), data, signature)
}

// ApplyEd25519Signature is an alias matching the Java reference's applySignature behavior.
func ApplyEd25519Signature(privateKey []byte, data []byte) ([]byte, error) {
	return Ed25519Sign(privateKey, data)
}

// VerifyEd25519Signature is an alias matching the Java reference's verifySignature behavior.
func VerifyEd25519Signature(publicKey []byte, data []byte, signature []byte) bool {
	return Ed25519Verify(publicKey, data, signature)
}

// FastEd25519Sign is an alias for Ed25519Sign. Go's standard library implementation is already direct.
func FastEd25519Sign(privateKey []byte, data []byte) ([]byte, error) {
	return Ed25519Sign(privateKey, data)
}

// FastEd25519Verify is an alias for Ed25519Verify.
func FastEd25519Verify(publicKey []byte, data []byte, signature []byte) bool {
	return Ed25519Verify(publicKey, data, signature)
}
