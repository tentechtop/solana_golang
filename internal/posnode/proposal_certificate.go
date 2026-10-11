package posnode

import (
	"errors"
	"fmt"

	"github.com/tentechtop/ordexus-core/blockchain"
	"github.com/tentechtop/ordexus-core/consensus"
)

var errProposalParentQCMissing = errors.New("posnode: proposal parent has no confirm qc")

// verifyProposalParentCertificate 验证提案引用的父块 QC + 创世后的链头必须逐块由法定人数证书连接。
func (node *posNode) verifyProposalParentCertificate(proposal consensus.BlockProposal) error {
	if proposal.Header.Height == 0 {
		return fmt.Errorf("posnode: proposal height must be positive")
	}
	if proposal.Header.Height == 1 {
		if proposal.Header.PreviousQCHash != proposal.Header.ParentHash {
			return fmt.Errorf("posnode: first proposal must reference genesis certificate")
		}
		return nil
	}

	parentQC, exists, err := node.ledger.BlockQC(proposal.Header.ParentHash)
	if err != nil {
		return fmt.Errorf("posnode: load proposal parent qc: %w", err)
	}
	if !exists {
		head := node.ledger.Head()
		if head.BlockHash == proposal.Header.ParentHash {
			parentQC, exists, err = node.ledger.HeadQC()
			if err != nil {
				return fmt.Errorf("posnode: load proposal head qc: %w", err)
			}
			// 纯内存账本只用于测试和嵌入场景，不保存可回读的 BlockQC 索引。
			if !exists && node.db == nil && head.Height+1 == proposal.Header.Height &&
				head.QCHash == proposal.Header.PreviousQCHash {
				return nil
			}
		}
	}
	if !exists {
		return errProposalParentQCMissing
	}
	if parentQC.Type != consensus.VoteTypeConfirm ||
		parentQC.BlockHash != proposal.Header.ParentHash ||
		parentQC.BlockHeight == ^uint64(0) ||
		parentQC.BlockHeight+1 != proposal.Header.Height {
		return fmt.Errorf("posnode: proposal parent qc binding mismatch")
	}
	if err := node.verifyQuorumCertificate(parentQC); err != nil {
		return fmt.Errorf("posnode: verify proposal parent qc: %w", err)
	}
	parentQCHash, err := blockchain.HashCanonicalQC(parentQC)
	if err != nil {
		return fmt.Errorf("posnode: hash proposal parent qc: %w", err)
	}
	if proposal.Header.PreviousQCHash != parentQCHash {
		return fmt.Errorf("posnode: proposal previous qc hash mismatch")
	}
	return nil
}
