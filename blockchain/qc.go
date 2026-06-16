package blockchain

import (
	"encoding/binary"
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

// HashCanonicalQC 计算稳定 QC 身份 + 同一区块的不同投票聚合版本必须得到同一个链头引用。
func HashCanonicalQC(qc consensus.QuorumCertificate) (structure.Hash, error) {
	if err := qc.Validate(); err != nil {
		return structure.Hash{}, err
	}
	data := make([]byte, 0, 58)
	data = append(data, []byte("pos-qc-canonical-v1")...)
	data = append(data, byte(qc.Type))
	data = binary.LittleEndian.AppendUint64(data, qc.Slot)
	data = binary.LittleEndian.AppendUint64(data, qc.BlockHeight)
	data = append(data, qc.BlockHash[:]...)
	return structure.NewHash(utils.SHA256(data))
}
