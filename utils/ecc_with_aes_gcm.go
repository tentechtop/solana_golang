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
	// Curve25519KeySize 定义 X25519 原始密钥长度 + 用于统一输入校验。
	Curve25519KeySize = 32
	// AES256KeySize 定义 AES-256 密钥长度 + 用于统一输入校验。
	AES256KeySize = 32
	// AESGCMNonceSize 定义 GCM nonce 长度 + 使用标准 96 位随机数。
	AESGCMNonceSize = 12
	// AESGCMTagSize 定义 GCM 认证标签长度 + 使用标准 128 位认证强度。
	AESGCMTagSize = 16
	// AESGCMHKDFSaltSize 定义 HKDF salt 长度 + 保持派生输入强度。
	AESGCMHKDFSaltSize = 32
)

var defaultAESGCMHKDFSalt = mustRandomAESGCMHKDFSalt()

// Curve25519KeyPair 保存 X25519 密钥对 + 使用原始字节便于跨语言传输。
type Curve25519KeyPair struct {
	PrivateKey []byte
	PublicKey  []byte
}

// GenerateCurve25519KeyPair 生成 X25519 密钥对 + 使用标准库安全随机源。
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

// GenerateCurve25519KeyPairBytes 生成 X25519 原始密钥 + 按私钥优先顺序兼容旧接口。
func GenerateCurve25519KeyPairBytes() (privateKey []byte, publicKey []byte, err error) {
	keyPair, err := GenerateCurve25519KeyPair()
	if err != nil {
		return nil, nil, err
	}
	return keyPair.PrivateKey, keyPair.PublicKey, nil
}

// GenerateSharedSecret 生成共享密钥 + 执行 X25519 私钥和远端公钥协商。
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

// DeriveAESKey 派生 AES-256 密钥 + 使用进程级随机 salt 隔离默认派生结果。
func DeriveAESKey(sharedSecret []byte) ([]byte, error) {
	return DeriveAESKeyWithSalt(sharedSecret, defaultAESGCMHKDFSalt)
}

// DeriveAESKeyWithSalt 派生 AES-256 密钥 + 使用显式 salt 保证跨端一致。
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

// AESGCMEncrypt 加密明文 + 返回 nonce、密文和认证标签的拼接结果。
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

// AESGCMDecrypt 解密密文 + 按 nonce、密文和认证标签格式解析。
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

// DeriveAesKey 提供 DeriveAESKey 别名 + 支持短驼峰调用习惯。
func DeriveAesKey(sharedSecret []byte) ([]byte, error) {
	return DeriveAESKey(sharedSecret)
}

// DeriveAesKeyWithSalt 提供 DeriveAESKeyWithSalt 别名 + 支持短驼峰调用习惯。
func DeriveAesKeyWithSalt(sharedSecret []byte, salt []byte) ([]byte, error) {
	return DeriveAESKeyWithSalt(sharedSecret, salt)
}

// AesGcmEncrypt 提供 AESGCMEncrypt 别名 + 支持短驼峰调用习惯。
func AesGcmEncrypt(key []byte, plaintext []byte) ([]byte, error) {
	return AESGCMEncrypt(key, plaintext)
}

// AesGcmDecrypt 提供 AESGCMDecrypt 别名 + 支持短驼峰调用习惯。
func AesGcmDecrypt(key []byte, encryptedData []byte) ([]byte, error) {
	return AESGCMDecrypt(key, encryptedData)
}

// newAESGCM 执行对应逻辑 + 保持函数职责清晰可维护。
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

// hkdfSHA256 执行对应逻辑 + 保持函数职责清晰可维护。
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

// mustRandomAESGCMHKDFSalt 执行对应逻辑 + 保持函数职责清晰可维护。
func mustRandomAESGCMHKDFSalt() []byte {
	salt, err := RandomBytes(AESGCMHKDFSaltSize)
	if err != nil {
		panic(fmt.Errorf("utils: initialize aes-gcm hkdf salt: %w", err))
	}
	return salt
}
