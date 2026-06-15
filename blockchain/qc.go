package blockchain

import (
	"fmt"

	"solana_golang/consensus"
	"solana_golang/structure"
	"solana_golang/utils"
)

// HashQC 计算 QC 哈希 + 链头保存最高确认凭证引用。
func HashQC(qc consensus.QuorumCertificate) (structure.Hash, error) {
	data, err := qc.MarshalBinary()
	if err != nil {
		return structure.Hash{}, fmt.Errorf("blockchain: marshal qc: %w", err)
	}
	return structure.NewHash(utils.SHA256(data))
}
