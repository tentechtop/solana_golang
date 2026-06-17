package consensus

import (
	"fmt"

	"solana_golang/structure"
)

// DoubleProposalEvidence 描述同 slot 双出块证据 + 两个有效 leader 签名即可独立验证。
type DoubleProposalEvidence struct {
	FirstProposal  BlockProposal
	SecondProposal BlockProposal
}

// DoubleVoteEvidence 描述同 slot 双投票证据 + 冲突投票进入惩罚流程。
type DoubleVoteEvidence struct {
	FirstVote  Vote
	SecondVote Vote
}

type SlashingEvidenceType uint8

const (
	SlashingEvidenceTypeDoubleProposal SlashingEvidenceType = iota + 1
	SlashingEvidenceTypeDoubleVote
)

// SignedVote 保存签名投票 + slash 必须验证原始签名后才能生效。
type SignedVote struct {
	Vote      Vote
	PublicKey structure.PublicKey
	Signature structure.Signature
}

// SignedDoubleVoteEvidence 描述双投签名证据 + 防止伪造 vote 触发罚没。
type SignedDoubleVoteEvidence struct {
	FirstVote  SignedVote
	SecondVote SignedVote
}

// SlashingEvidence 描述恶意证据 + 进入区块后由所有节点重算 slash 结果。
type SlashingEvidence struct {
	Type           SlashingEvidenceType
	DoubleProposal *DoubleProposalEvidence
	DoubleVote     *SignedDoubleVoteEvidence
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

// Validate 校验签名投票 + 投票人 ID 必须由签名公钥确定。
func (vote SignedVote) Validate() error {
	if err := vote.Vote.Validate(); err != nil {
		return err
	}
	if vote.PublicKey.IsZero() {
		return fmt.Errorf("consensus: signed vote public key is empty")
	}
	validatorID := NewValidatorID(vote.PublicKey)
	if string(validatorID) != vote.Vote.VoterID {
		return fmt.Errorf("consensus: signed vote voter id mismatch")
	}
	voteBytes, err := vote.Vote.MarshalBinary()
	if err != nil {
		return err
	}
	if !structure.VerifyMessageSignature(vote.PublicKey, voteBytes, vote.Signature) {
		return fmt.Errorf("consensus: invalid vote signature")
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
	if evidence.FirstVote.Type == evidence.SecondVote.Type &&
		evidence.FirstVote.BlockHeight == evidence.SecondVote.BlockHeight &&
		evidence.FirstVote.BlockHash == evidence.SecondVote.BlockHash {
		return fmt.Errorf("consensus: votes are identical")
	}
	return nil
}

// Validate 校验签名双投证据 + 两张签名票必须构成同 slot 冲突。
func (evidence SignedDoubleVoteEvidence) Validate() error {
	if err := evidence.FirstVote.Validate(); err != nil {
		return fmt.Errorf("consensus: first signed vote: %w", err)
	}
	if err := evidence.SecondVote.Validate(); err != nil {
		return fmt.Errorf("consensus: second signed vote: %w", err)
	}
	return DoubleVoteEvidence{
		FirstVote:  evidence.FirstVote.Vote,
		SecondVote: evidence.SecondVote.Vote,
	}.Validate()
}

// Validate 校验证据并返回被罚验证者 + 区块验证时据此定位 stake account。
func (evidence SlashingEvidence) Validate(snapshot EpochSnapshot) (ValidatorState, uint64, error) {
	switch evidence.Type {
	case SlashingEvidenceTypeDoubleProposal:
		return evidence.validateDoubleProposal(snapshot)
	case SlashingEvidenceTypeDoubleVote:
		return evidence.validateDoubleVote(snapshot)
	default:
		return ValidatorState{}, 0, fmt.Errorf("consensus: unsupported slashing evidence type %d", evidence.Type)
	}
}

func (evidence SlashingEvidence) validateDoubleProposal(snapshot EpochSnapshot) (ValidatorState, uint64, error) {
	if evidence.DoubleProposal == nil {
		return ValidatorState{}, 0, fmt.Errorf("consensus: missing double proposal evidence")
	}
	leaderID := evidence.DoubleProposal.FirstProposal.Header.LeaderID
	validator, exists := snapshot.ValidatorByID(leaderID)
	if !exists {
		return ValidatorState{}, 0, fmt.Errorf("consensus: double proposal validator missing from snapshot")
	}
	if err := evidence.DoubleProposal.Validate(validator.ConsensusPublicKey); err != nil {
		return ValidatorState{}, 0, err
	}
	return validator, evidence.DoubleProposal.FirstProposal.Header.Slot, nil
}

func (evidence SlashingEvidence) validateDoubleVote(snapshot EpochSnapshot) (ValidatorState, uint64, error) {
	if evidence.DoubleVote == nil {
		return ValidatorState{}, 0, fmt.Errorf("consensus: missing double vote evidence")
	}
	if err := evidence.DoubleVote.Validate(); err != nil {
		return ValidatorState{}, 0, err
	}
	validatorID := ValidatorID(evidence.DoubleVote.FirstVote.Vote.VoterID)
	validator, exists := snapshot.ValidatorByID(validatorID)
	if !exists {
		return ValidatorState{}, 0, fmt.Errorf("consensus: double vote validator missing from snapshot")
	}
	if validator.ConsensusPublicKey != evidence.DoubleVote.FirstVote.PublicKey {
		return ValidatorState{}, 0, fmt.Errorf("consensus: double vote public key mismatch")
	}
	return validator, evidence.DoubleVote.FirstVote.Vote.Slot, nil
}
