package utils

import "fmt"

const (
	// PublicKeySize is the size in bytes of a Solana public key.
	PublicKeySize = 32
	// SignatureSize is the size in bytes of a Solana transaction signature.
	SignatureSize = 64
)

// PublicKey is a fixed-size Solana public key.
type PublicKey [PublicKeySize]byte

// Hash is a fixed-size 32-byte Solana hash value.
type Hash [PublicKeySize]byte

// Blockhash is a fixed-size Solana recent blockhash.
type Blockhash = Hash

// Signature is a fixed-size Ed25519/Solana signature.
type Signature [SignatureSize]byte

// NewPublicKey converts a 32-byte slice into a PublicKey.
func NewPublicKey(value []byte) (PublicKey, error) {
	var key PublicKey
	if err := requireLength(value, PublicKeySize, "public key"); err != nil {
		return key, err
	}
	copy(key[:], value)
	return key, nil
}

// PublicKeyFromBytes converts a 32-byte slice into a PublicKey.
func PublicKeyFromBytes(value []byte) (PublicKey, error) {
	return NewPublicKey(value)
}

// PublicKeyFromBase58 decodes a Base58-encoded Solana public key.
func PublicKeyFromBase58(value string) (PublicKey, error) {
	decoded, err := Base58Decode(value)
	if err != nil {
		return PublicKey{}, err
	}
	return NewPublicKey(decoded)
}

// PublicKeyFromHex decodes a hex-encoded Solana public key.
func PublicKeyFromHex(value string) (PublicKey, error) {
	decoded, err := HexToBytes(value)
	if err != nil {
		return PublicKey{}, err
	}
	return NewPublicKey(decoded)
}

// Bytes returns a copy of the public key bytes.
func (p PublicKey) Bytes() []byte {
	return CloneBytes(p[:])
}

// String returns the Base58 representation of the public key.
func (p PublicKey) String() string {
	return Base58Encode(p[:])
}

// Hex returns the lower-case hex representation of the public key.
func (p PublicKey) Hex() string {
	return BytesToHex(p[:])
}

// Equal reports whether two public keys are equal.
func (p PublicKey) Equal(other PublicKey) bool {
	return SecureEqual(p[:], other[:])
}

// IsZero reports whether the public key is all zero bytes.
func (p PublicKey) IsZero() bool {
	return p == PublicKey{}
}

// NewHash converts a 32-byte slice into a Hash.
func NewHash(value []byte) (Hash, error) {
	var hash Hash
	if err := requireLength(value, PublicKeySize, "hash"); err != nil {
		return hash, err
	}
	copy(hash[:], value)
	return hash, nil
}

// HashFromBase58 decodes a Base58-encoded Solana hash.
func HashFromBase58(value string) (Hash, error) {
	decoded, err := Base58Decode(value)
	if err != nil {
		return Hash{}, err
	}
	return NewHash(decoded)
}

// Bytes returns a copy of the hash bytes.
func (h Hash) Bytes() []byte {
	return CloneBytes(h[:])
}

// String returns the Base58 representation of the hash.
func (h Hash) String() string {
	return Base58Encode(h[:])
}

// Hex returns the lower-case hex representation of the hash.
func (h Hash) Hex() string {
	return BytesToHex(h[:])
}

// NewSignature converts a 64-byte slice into a Signature.
func NewSignature(value []byte) (Signature, error) {
	var signature Signature
	if err := requireLength(value, SignatureSize, "signature"); err != nil {
		return signature, err
	}
	copy(signature[:], value)
	return signature, nil
}

// SignatureFromBytes converts a 64-byte slice into a Signature.
func SignatureFromBytes(value []byte) (Signature, error) {
	return NewSignature(value)
}

// SignatureFromBase58 decodes a Base58-encoded Solana signature.
func SignatureFromBase58(value string) (Signature, error) {
	decoded, err := Base58Decode(value)
	if err != nil {
		return Signature{}, err
	}
	return NewSignature(decoded)
}

// SignatureFromHex decodes a hex-encoded Solana signature.
func SignatureFromHex(value string) (Signature, error) {
	decoded, err := HexToBytes(value)
	if err != nil {
		return Signature{}, err
	}
	return NewSignature(decoded)
}

// Bytes returns a copy of the signature bytes.
func (s Signature) Bytes() []byte {
	return CloneBytes(s[:])
}

// String returns the Base58 representation of the signature.
func (s Signature) String() string {
	return Base58Encode(s[:])
}

// Hex returns the lower-case hex representation of the signature.
func (s Signature) Hex() string {
	return BytesToHex(s[:])
}

// Equal reports whether two signatures are equal.
func (s Signature) Equal(other Signature) bool {
	return SecureEqual(s[:], other[:])
}

// MustPublicKeyFromBase58 decodes value and panics on error. Intended for package-level constants and tests.
func MustPublicKeyFromBase58(value string) PublicKey {
	key, err := PublicKeyFromBase58(value)
	if err != nil {
		panic(fmt.Sprintf("utils: invalid public key %q: %v", value, err))
	}
	return key
}
