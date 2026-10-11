package structure

import (
	"fmt"

	"github.com/tentechtop/ordexus-core/utils"
)

const (
	OrdexusPrivateKeySeedSize = 32
	OrdexusSecretKeySize      = 64
)

// OrdexusKeyPair 保存密钥材料 + 包含公钥和私钥 seed。
type OrdexusKeyPair struct {
	PublicKey  PublicKey
	PrivateKey []byte
}

// KeyPairFromSeed 从 seed 派生密钥对 + 校验 32 字节 Ed25519 seed。
func KeyPairFromSeed(seed []byte) (OrdexusKeyPair, error) {
	if err := requireValueLength(seed, OrdexusPrivateKeySeedSize, "private key seed"); err != nil {
		return OrdexusKeyPair{}, err
	}
	publicKeyBytes, err := utils.DeriveEd25519PublicKeyFromPrivateKey(seed)
	if err != nil {
		return OrdexusKeyPair{}, err
	}
	publicKey, err := NewPublicKey(publicKeyBytes)
	if err != nil {
		return OrdexusKeyPair{}, err
	}
	return OrdexusKeyPair{PublicKey: publicKey, PrivateKey: utils.CloneBytes(seed)}, nil
}

// KeyPairFromSecretKey64 加载 CLI 私钥 + 校验 seed 派生公钥与附带公钥一致。
func KeyPairFromSecretKey64(secretKey []byte) (OrdexusKeyPair, error) {
	if err := requireValueLength(secretKey, OrdexusSecretKeySize, "secret key"); err != nil {
		return OrdexusKeyPair{}, err
	}
	keyPair, err := KeyPairFromSeed(secretKey[:OrdexusPrivateKeySeedSize])
	if err != nil {
		return OrdexusKeyPair{}, err
	}
	if !utils.SecureEqual(keyPair.PublicKey[:], secretKey[OrdexusPrivateKeySeedSize:]) {
		return OrdexusKeyPair{}, fmt.Errorf("structure: secret key public key does not match seed")
	}
	return keyPair, nil
}

// ToSecretKey64 转换 CLI 私钥格式 + 将 seed 和公钥拼接为 64 字节。
func ToSecretKey64(seed []byte) ([]byte, error) {
	keyPair, err := KeyPairFromSeed(seed)
	if err != nil {
		return nil, err
	}
	return keyPair.SecretKey64(), nil
}

// SecretKey64 返回 CLI 私钥格式 + 按 seed 加公钥顺序拼接。
func (keyPair OrdexusKeyPair) SecretKey64() []byte {
	return utils.ConcatBytes(keyPair.PrivateKey, keyPair.PublicKey[:])
}

// Sign 签名数据 + 使用密钥对中的私钥 seed。
func (keyPair OrdexusKeyPair) Sign(data []byte) (Signature, error) {
	signature, err := utils.Ed25519Sign(keyPair.PrivateKey, data)
	if err != nil {
		return Signature{}, err
	}
	return NewSignature(signature)
}

// Verify 验证签名 + 使用密钥对中的公钥。
func (keyPair OrdexusKeyPair) Verify(data []byte, signature Signature) bool {
	return utils.Ed25519Verify(keyPair.PublicKey[:], data, signature[:])
}
