package consensus

import (
	"fmt"

	"github.com/tentechtop/ordexus-core/structure"
	"github.com/tentechtop/ordexus-core/utils"
)

const consensusProofDomainV1 = "solana_golang.consensus.proof.v1"

// HashRewardQuorumCertificates 将奖励凭证完整承诺到区块头，避免无状态效果的 QC 被替换或删除。
func HashRewardQuorumCertificates(certificates []QuorumCertificate) (structure.Hash, error) {
	if len(certificates) == 0 {
		return structure.Hash{}, nil
	}
	encoded := append([]byte{}, []byte(consensusProofDomainV1+"/reward-qc")...)
	encoded = appendUint64ForHash(encoded, uint64(len(certificates)))
	for index, certificate := range certificates {
		certificateBytes, err := certificate.MarshalBinary()
		if err != nil {
			return structure.Hash{}, fmt.Errorf("consensus: marshal reward qc %d: %w", index, err)
		}
		encoded = appendLengthPrefixedConsensusProof(encoded, certificateBytes)
	}
	return structure.NewHash(utils.SHA256(encoded))
}

// HashSlashingEvidenceList 将处罚证据的可验证部分承诺到区块头，区块哈希因此唯一绑定证据。
func HashSlashingEvidenceList(evidences []SlashingEvidence) (structure.Hash, error) {
	if len(evidences) == 0 {
		return structure.Hash{}, nil
	}
	encoded := append([]byte{}, []byte(consensusProofDomainV1+"/slashing")...)
	encoded = appendUint64ForHash(encoded, uint64(len(evidences)))
	for index, evidence := range evidences {
		evidenceBytes, err := marshalSlashingEvidenceForHash(evidence)
		if err != nil {
			return structure.Hash{}, fmt.Errorf("consensus: hash slashing evidence %d: %w", index, err)
		}
		encoded = appendLengthPrefixedConsensusProof(encoded, evidenceBytes)
	}
	return structure.NewHash(utils.SHA256(encoded))
}

// VerifyQuorumCertificateForSnapshot 使用凭证发生 slot 的验证者快照验签并重算质押阈值。
func VerifyQuorumCertificateForSnapshot(certificate QuorumCertificate, snapshot EpochSnapshot, quorum Quorum) error {
	if err := snapshot.Validate(); err != nil {
		return fmt.Errorf("%w: invalid epoch snapshot: %v", ErrInvalidCertificate, err)
	}
	if err := certificate.Validate(); err != nil {
		return err
	}
	if certificate.Slot < snapshot.StartSlot || certificate.Slot > snapshot.EndSlot {
		return fmt.Errorf("%w: qc slot is outside epoch snapshot", ErrInvalidCertificate)
	}
	publicKeys := snapshot.BLSPublicKeys()
	if len(snapshot.Validators) == 0 || len(publicKeys) != len(snapshot.Validators) {
		return fmt.Errorf("%w: complete bls validator set is required", ErrInvalidCertificate)
	}
	if certificate.SignatureScheme == "" {
		return fmt.Errorf("%w: aggregate qc signature is required", ErrInvalidCertificate)
	}
	return VerifyBLSAggregateWithStake(
		certificate,
		snapshot.ValidatorOrder(),
		publicKeys,
		snapshot.StakeMap(),
		quorum,
	)
}

func marshalSlashingEvidenceForHash(evidence SlashingEvidence) ([]byte, error) {
	encoded := []byte{byte(evidence.Type)}
	switch evidence.Type {
	case SlashingEvidenceTypeDoubleProposal:
		if evidence.DoubleProposal == nil {
			return nil, fmt.Errorf("missing double proposal evidence")
		}
		first, err := marshalSignedProposalForEvidenceHash(evidence.DoubleProposal.FirstProposal)
		if err != nil {
			return nil, err
		}
		second, err := marshalSignedProposalForEvidenceHash(evidence.DoubleProposal.SecondProposal)
		if err != nil {
			return nil, err
		}
		encoded = appendLengthPrefixedConsensusProof(encoded, first)
		encoded = appendLengthPrefixedConsensusProof(encoded, second)
	case SlashingEvidenceTypeDoubleVote:
		if evidence.DoubleVote == nil {
			return nil, fmt.Errorf("missing double vote evidence")
		}
		first, err := marshalSignedVoteForEvidenceHash(evidence.DoubleVote.FirstVote)
		if err != nil {
			return nil, err
		}
		second, err := marshalSignedVoteForEvidenceHash(evidence.DoubleVote.SecondVote)
		if err != nil {
			return nil, err
		}
		encoded = appendLengthPrefixedConsensusProof(encoded, first)
		encoded = appendLengthPrefixedConsensusProof(encoded, second)
	default:
		return nil, fmt.Errorf("unsupported slashing evidence type %d", evidence.Type)
	}
	return encoded, nil
}

func marshalSignedProposalForEvidenceHash(proposal BlockProposal) ([]byte, error) {
	headerBytes, err := proposal.Header.SignBytes()
	if err != nil {
		return nil, err
	}
	encoded := appendLengthPrefixedConsensusProof(nil, headerBytes)
	return append(encoded, proposal.LeaderSignature[:]...), nil
}

func marshalSignedVoteForEvidenceHash(vote SignedVote) ([]byte, error) {
	voteBytes, err := vote.Vote.MarshalBinary()
	if err != nil {
		return nil, err
	}
	encoded := appendLengthPrefixedConsensusProof(nil, voteBytes)
	encoded = append(encoded, vote.PublicKey[:]...)
	return append(encoded, vote.Signature[:]...), nil
}

func appendLengthPrefixedConsensusProof(target []byte, value []byte) []byte {
	target = appendUint64ForHash(target, uint64(len(value)))
	return append(target, value...)
}
