package consensus

import (
	"fmt"

	"solana_golang/structure"
	"solana_golang/utils"
)

const (
	// HardcodedGenesisTreasurySeed 固定创世资金账户 + 私链冷启动需要稳定资金来源。
	HardcodedGenesisTreasurySeed = "solana-golang-hardcoded-genesis-treasury-v1"
	// DefaultGenesisSupplyLamports 固定创世总供应 + 本地 PoS 网络使用 10 亿代币按 9 位精度计数。
	DefaultGenesisSupplyLamports = uint64(1_000_000_000_000_000_000)
)

// HardcodedGenesisTreasuryKeyPair 返回固定创世资金密钥 + 多节点必须推导出同一账户地址。
func HardcodedGenesisTreasuryKeyPair() (structure.SolanaKeyPair, error) {
	keyPair, err := structure.KeyPairFromSeed(utils.SHA256([]byte(HardcodedGenesisTreasurySeed)))
	if err != nil {
		return structure.SolanaKeyPair{}, fmt.Errorf("consensus: build genesis treasury key: %w", err)
	}
	return keyPair, nil
}

// HardcodedGenesisTreasuryPublicKey 返回固定创世资金地址 + 配置和 RPC 可直接展示该账户。
func HardcodedGenesisTreasuryPublicKey() (structure.PublicKey, error) {
	keyPair, err := HardcodedGenesisTreasuryKeyPair()
	if err != nil {
		return structure.PublicKey{}, err
	}
	return keyPair.PublicKey, nil
}
