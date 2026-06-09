package utils

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
)

const (
	// Ed25519KeySize 定义 Solana 核心密钥长度 + 使用 Ed25519 seed 大小。
	Ed25519KeySize = ed25519.SeedSize
	// Ed25519SignatureSize 定义 Ed25519 签名长度 + 用于签名输入校验。
	Ed25519SignatureSize = ed25519.SignatureSize
)

// Ed25519KeyPair 保存 Solana 密钥对 + 使用 32 字节公钥和 32 字节私钥种子。
type Ed25519KeyPair struct {
	PublicKey  []byte
	PrivateKey []byte
}

// GenerateEd25519KeyPair 生成 Ed25519 密钥对 + 返回 32 字节 seed 兼容 Solana 派生。
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

// GenerateEd25519KeyPairBytes 生成 Ed25519 原始密钥 + 返回公钥和私钥字节切片。
func GenerateEd25519KeyPairBytes() (publicKey []byte, privateKey []byte, err error) {
	keyPair, err := GenerateEd25519KeyPair()
	if err != nil {
		return nil, nil, err
	}
	return keyPair.PublicKey, keyPair.PrivateKey, nil
}

// DeriveEd25519PublicKeyFromPrivateKey 派生公钥 + 从 32 字节私钥 seed 计算。
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

// Ed25519Sign 签名数据 + 使用 32 字节私钥 seed 生成 64 字节签名。
func Ed25519Sign(privateKey []byte, data []byte) ([]byte, error) {
	if err := requireLength(privateKey, Ed25519KeySize, "ed25519 private key"); err != nil {
		return nil, err
	}
	signature := ed25519.Sign(ed25519.NewKeyFromSeed(privateKey), data)
	return CloneBytes(signature), nil
}

// Ed25519Verify 验证签名 + 使用 32 字节公钥校验 64 字节签名。
func Ed25519Verify(publicKey []byte, data []byte, signature []byte) bool {
	if len(publicKey) != Ed25519KeySize || len(signature) != Ed25519SignatureSize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(publicKey), data, signature)
}

// ApplyEd25519Signature 提供签名别名 + 复用 Ed25519Sign 统一校验。
func ApplyEd25519Signature(privateKey []byte, data []byte) ([]byte, error) {
	return Ed25519Sign(privateKey, data)
}

// VerifyEd25519Signature 提供验签别名 + 复用 Ed25519Verify 统一校验。
func VerifyEd25519Signature(publicKey []byte, data []byte, signature []byte) bool {
	return Ed25519Verify(publicKey, data, signature)
}

// FastEd25519Sign 提供快速签名别名 + 标准库实现已直接高效。
func FastEd25519Sign(privateKey []byte, data []byte) ([]byte, error) {
	return Ed25519Sign(privateKey, data)
}

// FastEd25519Verify 提供快速验签别名 + 兼容旧接口命名。
func FastEd25519Verify(publicKey []byte, data []byte, signature []byte) bool {
	return Ed25519Verify(publicKey, data, signature)
}
