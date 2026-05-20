package utils

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
)

const (
	// Curve25519KeySize is the byte length of raw X25519 public and private keys.
	Curve25519KeySize = 32
	// AES256KeySize is the AES-256 key length in bytes.
	AES256KeySize = 32
	// AESGCMNonceSize is the Java reference's 12-byte GCM IV length.
	AESGCMNonceSize = 12
	// AESGCMTagSize is the Java reference's 128-bit GCM tag length in bytes.
	AESGCMTagSize = 16
	// AESGCMHKDFSaltSize is the salt length used by the Java reference.
	AESGCMHKDFSaltSize = 32
)

var defaultAESGCMHKDFSalt = mustRandomAESGCMHKDFSalt()

// Curve25519KeyPair stores a raw X25519 private/public key pair.
type Curve25519KeyPair struct {
	PrivateKey []byte
	PublicKey  []byte
}

// GenerateCurve25519KeyPair creates a raw X25519 key pair.
func GenerateCurve25519KeyPair() (Curve25519KeyPair, error) {
	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return Curve25519KeyPair{}, fmt.Errorf("utils: generate curve25519 key pair: %w", err)
	}
	return Curve25519KeyPair{
		PrivateKey: CloneBytes(privateKey.Bytes()),
		PublicKey:  CloneBytes(privateKey.PublicKey().Bytes()),
	}, nil
}

// GenerateCurve25519KeyPairBytes creates a raw X25519 key pair and returns private key first.
func GenerateCurve25519KeyPairBytes() (privateKey []byte, publicKey []byte, err error) {
	keyPair, err := GenerateCurve25519KeyPair()
	if err != nil {
		return nil, nil, err
	}
	return keyPair.PrivateKey, keyPair.PublicKey, nil
}

// GenerateSharedSecret performs X25519 key agreement with a local private key and remote public key.
func GenerateSharedSecret(privateKey []byte, publicKey []byte) ([]byte, error) {
	if err := requireLength(privateKey, Curve25519KeySize, "curve25519 private key"); err != nil {
		return nil, err
	}
	if err := requireLength(publicKey, Curve25519KeySize, "curve25519 public key"); err != nil {
		return nil, err
	}

	localPrivateKey, err := ecdh.X25519().NewPrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("utils: parse curve25519 private key: %w", err)
	}
	remotePublicKey, err := ecdh.X25519().NewPublicKey(publicKey)
	if err != nil {
		return nil, fmt.Errorf("utils: parse curve25519 public key: %w", err)
	}

	sharedSecret, err := localPrivateKey.ECDH(remotePublicKey)
	if err != nil {
		return nil, fmt.Errorf("utils: generate shared secret: %w", err)
	}
	return CloneBytes(sharedSecret), nil
}

// DeriveAESKey derives a 32-byte AES-256 key with HKDF-SHA256.
//
// This mirrors the Java utility's package-level random salt behavior: the salt is
// generated once per process. Use DeriveAESKeyWithSalt when the other side runs
// in another process or language and must derive the same key.
func DeriveAESKey(sharedSecret []byte) ([]byte, error) {
	return DeriveAESKeyWithSalt(sharedSecret, defaultAESGCMHKDFSalt)
}

// DeriveAESKeyWithSalt derives a 32-byte AES-256 key with HKDF-SHA256 and an explicit salt.
func DeriveAESKeyWithSalt(sharedSecret []byte, salt []byte) ([]byte, error) {
	if err := requireLength(sharedSecret, Curve25519KeySize, "curve25519 shared secret"); err != nil {
		return nil, err
	}
	if len(salt) == 0 {
		return nil, fmt.Errorf("utils: hkdf salt cannot be empty")
	}
	key, err := hkdfSHA256(sharedSecret, salt, []byte("AES-GCM-256"), AES256KeySize)
	if err != nil {
		return nil, err
	}
	return key, nil
}

// AESGCMEncrypt encrypts plaintext with AES-GCM and returns nonce + ciphertext + tag.
func AESGCMEncrypt(key []byte, plaintext []byte) ([]byte, error) {
	gcm, err := newAESGCM(key)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, AESGCMNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("utils: generate aes-gcm nonce: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)
	return ConcatBytes(nonce, ciphertext), nil
}

// AESGCMDecrypt decrypts data in nonce + ciphertext + tag format.
func AESGCMDecrypt(key []byte, encryptedData []byte) ([]byte, error) {
	if len(encryptedData) < AESGCMNonceSize+AESGCMTagSize {
		return nil, fmt.Errorf("utils: aes-gcm encrypted data requires at least %d bytes, got %d", AESGCMNonceSize+AESGCMTagSize, len(encryptedData))
	}

	gcm, err := newAESGCM(key)
	if err != nil {
		return nil, err
	}

	nonce := encryptedData[:AESGCMNonceSize]
	ciphertext := encryptedData[AESGCMNonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("utils: decrypt aes-gcm: %w", err)
	}
	return plaintext, nil
}

// DeriveAesKey is an alias matching the Java reference's method name casing.
func DeriveAesKey(sharedSecret []byte) ([]byte, error) {
	return DeriveAESKey(sharedSecret)
}

// DeriveAesKeyWithSalt is an alias matching the Java reference's method name casing.
func DeriveAesKeyWithSalt(sharedSecret []byte, salt []byte) ([]byte, error) {
	return DeriveAESKeyWithSalt(sharedSecret, salt)
}

// AesGcmEncrypt is an alias matching the Java reference's method name casing.
func AesGcmEncrypt(key []byte, plaintext []byte) ([]byte, error) {
	return AESGCMEncrypt(key, plaintext)
}

// AesGcmDecrypt is an alias matching the Java reference's method name casing.
func AesGcmDecrypt(key []byte, encryptedData []byte) ([]byte, error) {
	return AESGCMDecrypt(key, encryptedData)
}

func newAESGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("utils: create aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCMWithNonceSize(block, AESGCMNonceSize)
	if err != nil {
		return nil, fmt.Errorf("utils: create aes-gcm: %w", err)
	}
	return gcm, nil
}

func hkdfSHA256(inputKeyingMaterial []byte, salt []byte, info []byte, length int) ([]byte, error) {
	if length < 0 {
		return nil, fmt.Errorf("utils: hkdf length cannot be negative")
	}
	if length > 255*sha256.Size {
		return nil, fmt.Errorf("utils: hkdf length %d exceeds maximum %d", length, 255*sha256.Size)
	}
	if salt == nil {
		salt = make([]byte, sha256.Size)
	}

	extract := hmac.New(sha256.New, salt)
	extract.Write(inputKeyingMaterial)
	pseudoRandomKey := extract.Sum(nil)

	output := make([]byte, 0, length)
	previous := []byte(nil)
	for counter := byte(1); len(output) < length; counter++ {
		expand := hmac.New(sha256.New, pseudoRandomKey)
		expand.Write(previous)
		expand.Write(info)
		expand.Write([]byte{counter})
		previous = expand.Sum(nil)
		output = append(output, previous...)
	}
	return CloneBytes(output[:length]), nil
}

func mustRandomAESGCMHKDFSalt() []byte {
	salt, err := RandomBytes(AESGCMHKDFSaltSize)
	if err != nil {
		panic(fmt.Errorf("utils: initialize aes-gcm hkdf salt: %w", err))
	}
	return salt
}
