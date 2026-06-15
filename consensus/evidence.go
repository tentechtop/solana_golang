package consensus

import (
	"fmt"

	"solana_golang/structure"
)

// DoubleProposalEvidence 描述同 slot 双出块证据 + 两个有效签名即可独立验证。
type DoubleProposalEvidence struct {
	FirstProposal  BlockProposal
	SecondProposal BlockProposal
}

// DoubleVoteEvidence 描述同 slot 双投票证据 + 冲突投票进入惩罚流程。
type DoubleVoteEvidence struct {
	FirstVote  Vote
	SecondVote Vote
}

// Validate 校验双出块证据 + 必须同一 leader 同一 slot 不同区块。
func (evidence DoubleProposalEvidence) Validate(publicKey structure.PublicKey) error {
	firstHash, err := evidence.FirstProposal.Hash()
	if err != nil {
		return err
	}
	secondHash, err := evidence.SecondProposal.Hash()
	if err != nil {
		return err
	}
	if firstHash == secondHash {
		return fmt.Errorf("consensus: proposals are identical")
	}
	if evidence.FirstProposal.Header.Slot != evidence.SecondProposal.Header.Slot {
		return fmt.Errorf("consensus: proposals are not in same slot")
	}
	if evidence.FirstProposal.Header.LeaderID != evidence.SecondProposal.Header.LeaderID {
		return fmt.Errorf("consensus: proposals are from different leaders")
	}
	if err := evidence.FirstProposal.VerifyLeaderSignature(publicKey); err != nil {
		return fmt.Errorf("consensus: first proposal signature: %w", err)
	}
	if err := evidence.SecondProposal.VerifyLeaderSignature(publicKey); err != nil {
		return fmt.Errorf("consensus: second proposal signature: %w", err)
	}
	return nil
}

// Validate 校验双投票证据 + 同一验证者同一 slot 选择不同结果即冲突。
func (evidence DoubleVoteEvidence) Validate() error {
	if err := evidence.FirstVote.Validate(); err != nil {
		return fmt.Errorf("consensus: first vote: %w", err)
	}
	if err := evidence.SecondVote.Validate(); err != nil {
		return fmt.Errorf("consensus: second vote: %w", err)
	}
	if evidence.FirstVote.VoterID != evidence.SecondVote.VoterID {
		return fmt.Errorf("consensus: votes are from different validators")
	}
	if evidence.FirstVote.Slot != evidence.SecondVote.Slot {
		return fmt.Errorf("consensus: votes are not in same slot")
	}
	if evidence.FirstVote.Type == evidence.SecondVote.Type && evidence.FirstVote.BlockHash == evidence.SecondVote.BlockHash {
		return fmt.Errorf("consensus: votes are identical")
	}
	return nil
}
