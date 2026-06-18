package consensus

import (
	"fmt"

	"solana_golang/structure"
)

const (
	// HardcodedGenesisTreasuryPublicKeyBase58 固定创世资金公钥 + 共识只依赖公开身份避免私钥进入代码。
	HardcodedGenesisTreasuryPublicKeyBase58 = "4vgAxQAXeKXhyrJyQ5XDXzr1wR92NaS631GEkDjdhRn9"
	// DefaultGenesisSupplyLamports 固定创世总供应 + 统一使用 1 亿枚代币和 9 位精度。
	DefaultGenesisSupplyLamports = structure.DefaultGenesisSupplyLamports
)

// HardcodedGenesisTreasuryKeyPair 拒绝返回私钥 + 共识层不允许内置可签名密钥材料。
func HardcodedGenesisTreasuryKeyPair() (structure.SolanaKeyPair, error) {
	return structure.SolanaKeyPair{}, fmt.Errorf("consensus: genesis treasury private key is not embedded; configure local keystore")
}

// HardcodedGenesisTreasuryPublicKey 返回固定创世资金地址 + 配置和 RPC 可直接展示该账户。
func HardcodedGenesisTreasuryPublicKey() (structure.PublicKey, error) {
	publicKey, err := structure.PublicKeyFromBase58(HardcodedGenesisTreasuryPublicKeyBase58)
	if err != nil {
		return structure.PublicKey{}, fmt.Errorf("consensus: decode genesis treasury public key: %w", err)
	}
	return publicKey, nil
}
