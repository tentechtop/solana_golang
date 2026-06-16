package consensus

import (
	"fmt"
	"sort"

	"github.com/cloudflare/circl/sign/bls"

	"solana_golang/utils"
)

const (
	BLSSignatureSchemeBasic = "bls12-381-basic-g2sigg1"
	blsKeyInfo              = "solana-golang-pos-qc-bls-v1"
)

// BLSKeyPair 描述 BLS 共识密钥 + 用于高性能 QC 聚合签名。
type BLSKeyPair struct {
	PrivateKey []byte
	PublicKey  []byte
}

// BLSKeyPairFromSeed 派生 BLS 密钥 + 多节点测试通过稳定 seed 可复现。
func BLSKeyPairFromSeed(seed []byte) (BLSKeyPair, error) {
	if len(seed) < 32 {
		seed = utils.SHA256(seed)
	}
	privateKey, err := bls.KeyGen[bls.KeyG2SigG1](seed, nil, []byte(blsKeyInfo))
	if err != nil {
		return BLSKeyPair{}, fmt.Errorf("consensus: derive bls key: %w", err)
	}
	privateKeyBytes, err := privateKey.MarshalBinary()
	if err != nil {
		return BLSKeyPair{}, fmt.Errorf("consensus: marshal bls private key: %w", err)
	}
	publicKeyBytes, err := privateKey.PublicKey().MarshalBinary()
	if err != nil {
		return BLSKeyPair{}, fmt.Errorf("consensus: marshal bls public key: %w", err)
	}
	return BLSKeyPair{PrivateKey: privateKeyBytes, PublicKey: publicKeyBytes}, nil
}

// SignBLSVote 签署投票消息 + 每个验证者消息包含 voter_id 防止 BASIC 聚合模式同消息风险。
func SignBLSVote(privateKeyBytes []byte, vote Vote) ([]byte, error) {
	if err := vote.Validate(); err != nil {
		return nil, err
	}
	privateKey := &bls.PrivateKey[bls.KeyG2SigG1]{}
	if err := privateKey.UnmarshalBinary(privateKeyBytes); err != nil {
		return nil, fmt.Errorf("consensus: unmarshal bls private key: %w", err)
	}
	return bls.Sign(privateKey, BLSVoteMessage(vote)), nil
}

// VerifyBLSVote 校验单票 BLS 签名 + 聚合前先拦截伪造投票。
func VerifyBLSVote(publicKeyBytes []byte, signature []byte, vote Vote) error {
	if err := vote.Validate(); err != nil {
		return err
	}
	publicKey := &bls.PublicKey[bls.KeyG2SigG1]{}
	if err := publicKey.UnmarshalBinary(publicKeyBytes); err != nil {
		return fmt.Errorf("consensus: unmarshal bls public key: %w", err)
	}
	if !bls.Verify(publicKey, BLSVoteMessage(vote), signature) {
		return fmt.Errorf("%w: invalid bls vote signature", ErrInvalidVote)
	}
	return nil
}

// ValidateBLSPublicKey 校验 BLS 公钥字节 + 防止错误公钥进入 epoch snapshot。
func ValidateBLSPublicKey(publicKeyBytes []byte) error {
	if len(publicKeyBytes) == 0 {
		return nil
	}
	publicKey := &bls.PublicKey[bls.KeyG2SigG1]{}
	if err := publicKey.UnmarshalBinary(publicKeyBytes); err != nil {
		return fmt.Errorf("consensus: unmarshal bls public key: %w", err)
	}
	return nil
}

// BLSVoteMessage 构造 BLS 签名消息 + 固定字段顺序保证各节点聚合验证一致。
func BLSVoteMessage(vote Vote) []byte {
	message := make([]byte, 0, 128+len(vote.VoterID))
	message = append(message, []byte("pos-qc-vote-v1")...)
	message = append(message, byte(vote.Type))
	message = appendUint64ForHash(message, vote.Slot)
	message = appendUint64ForHash(message, vote.BlockHeight)
	message = append(message, vote.BlockHash[:]...)
	message = append(message, []byte(vote.VoterID)...)
	return message
}

// AttachBLSAggregate 聚合 QC 签名 + voter bitmap 按完整验证者有序列表编码。
func AttachBLSAggregate(certificate QuorumCertificate, validatorOrder []string, signaturesByValidator map[string][]byte) (QuorumCertificate, error) {
	if err := certificate.Validate(); err != nil {
		return QuorumCertificate{}, err
	}
	if len(validatorOrder) == 0 {
		return QuorumCertificate{}, fmt.Errorf("%w: empty validator order", ErrInvalidCertificate)
	}
	orderedVoters := append([]string(nil), certificate.Voters...)
	sort.Strings(orderedVoters)
	signatures := make([]bls.Signature, 0, len(orderedVoters))
	bitmap := make([]byte, (len(validatorOrder)+7)/8)
	indexByValidator := make(map[string]int, len(validatorOrder))
	for index, validatorID := range validatorOrder {
		if validatorID == "" {
			return QuorumCertificate{}, fmt.Errorf("%w: empty validator id in bitmap order", ErrInvalidCertificate)
		}
		if _, exists := indexByValidator[validatorID]; exists {
			return QuorumCertificate{}, fmt.Errorf("%w: duplicate validator id in bitmap order", ErrInvalidCertificate)
		}
		indexByValidator[validatorID] = index
	}
	for _, voterID := range orderedVoters {
		index, exists := indexByValidator[voterID]
		if !exists {
			return QuorumCertificate{}, fmt.Errorf("%w: voter not in validator order", ErrInvalidCertificate)
		}
		signature := signaturesByValidator[voterID]
		if len(signature) == 0 {
			return QuorumCertificate{}, fmt.Errorf("%w: missing bls signature", ErrInvalidCertificate)
		}
		signatures = append(signatures, cloneBytes(signature))
		bitmap[index/8] |= byte(1 << uint(index%8))
	}
	aggregateSignature, err := bls.Aggregate(bls.KeyG2SigG1{}, signatures)
	if err != nil {
		return QuorumCertificate{}, fmt.Errorf("consensus: aggregate bls signatures: %w", err)
	}
	certificate.SignatureScheme = BLSSignatureSchemeBasic
	certificate.AggregateSignature = cloneBytes(aggregateSignature)
	certificate.VoterBitmap = bitmap
	return certificate, certificate.Validate()
}

// VerifyBLSAggregate 校验聚合 QC + 用 bitmap 还原公钥集合和 voter_id 消息。
func VerifyBLSAggregate(certificate QuorumCertificate, validatorOrder []string, publicKeysByValidator map[string][]byte) error {
	if err := certificate.Validate(); err != nil {
		return err
	}
	voters, err := votersFromBitmap(validatorOrder, certificate.VoterBitmap)
	if err != nil {
		return err
	}
	if !sameStringSet(voters, certificate.Voters) {
		return fmt.Errorf("%w: voter bitmap mismatch", ErrInvalidCertificate)
	}
	publicKeys := make([]*bls.PublicKey[bls.KeyG2SigG1], 0, len(voters))
	messages := make([][]byte, 0, len(voters))
	for _, voterID := range voters {
		publicKeyBytes := publicKeysByValidator[voterID]
		if len(publicKeyBytes) == 0 {
			return fmt.Errorf("%w: missing bls public key", ErrInvalidCertificate)
		}
		publicKey := &bls.PublicKey[bls.KeyG2SigG1]{}
		if err := publicKey.UnmarshalBinary(publicKeyBytes); err != nil {
			return fmt.Errorf("consensus: unmarshal bls public key: %w", err)
		}
		publicKeys = append(publicKeys, publicKey)
		messages = append(messages, BLSVoteMessage(Vote{
			Type:               certificate.Type,
			Slot:               certificate.Slot,
			BlockHeight:        certificate.BlockHeight,
			BlockHash:          certificate.BlockHash,
			VoterID:            voterID,
			Stake:              1,
			CreatedAtUnixMilli: certificate.CreatedAtUnixMilli,
		}))
	}
	if !bls.VerifyAggregate(publicKeys, messages, certificate.AggregateSignature) {
		return fmt.Errorf("%w: invalid bls aggregate signature", ErrInvalidCertificate)
	}
	return nil
}

// VerifyBLSAggregateWithStake 校验聚合 QC 权重 + 防止低 stake 签名伪造成高 stake QC。
func VerifyBLSAggregateWithStake(
	certificate QuorumCertificate,
	validatorOrder []string,
	publicKeysByValidator map[string][]byte,
	stakeByValidator map[string]uint64,
	quorum Quorum,
) error {
	if err := VerifyBLSAggregate(certificate, validatorOrder, publicKeysByValidator); err != nil {
		return err
	}
	orderedStake, totalStake, err := stakeByValidatorOrder(validatorOrder, stakeByValidator)
	if err != nil {
		return err
	}
	requiredStake, err := quorum.RequiredStake(totalStake)
	if err != nil {
		return err
	}
	if certificate.ThresholdStake != requiredStake {
		return fmt.Errorf("%w: threshold stake mismatch", ErrInvalidCertificate)
	}
	voters, err := votersFromBitmap(validatorOrder, certificate.VoterBitmap)
	if err != nil {
		return err
	}
	confirmedStake := uint64(0)
	for _, voterID := range voters {
		stake, exists := orderedStake[voterID]
		if !exists {
			return fmt.Errorf("%w: voter stake missing", ErrInvalidCertificate)
		}
		if ^uint64(0)-confirmedStake < stake {
			return fmt.Errorf("%w: confirmed stake overflow", ErrInvalidCertificate)
		}
		confirmedStake += stake
	}
	if confirmedStake != certificate.ConfirmedStake {
		return fmt.Errorf("%w: confirmed stake mismatch", ErrInvalidCertificate)
	}
	if confirmedStake < requiredStake {
		return fmt.Errorf("%w: insufficient confirmed stake", ErrInvalidCertificate)
	}
	return nil
}

func votersFromBitmap(validatorOrder []string, bitmap []byte) ([]string, error) {
	if len(validatorOrder) == 0 || len(bitmap) == 0 {
		return nil, fmt.Errorf("%w: empty voter bitmap", ErrInvalidCertificate)
	}
	if len(bitmap) != (len(validatorOrder)+7)/8 {
		return nil, fmt.Errorf("%w: voter bitmap length mismatch", ErrInvalidCertificate)
	}
	voters := make([]string, 0)
	for index, validatorID := range validatorOrder {
		if bitmap[index/8]&byte(1<<uint(index%8)) == 0 {
			continue
		}
		voters = append(voters, validatorID)
	}
	if len(voters) == 0 {
		return nil, fmt.Errorf("%w: empty voter bitmap", ErrInvalidCertificate)
	}
	sort.Strings(voters)
	return voters, nil
}

func stakeByValidatorOrder(validatorOrder []string, stakeByValidator map[string]uint64) (map[string]uint64, uint64, error) {
	if len(validatorOrder) == 0 {
		return nil, 0, fmt.Errorf("%w: empty validator order", ErrInvalidCertificate)
	}
	orderedStake := make(map[string]uint64, len(validatorOrder))
	totalStake := uint64(0)
	for _, validatorID := range validatorOrder {
		if validatorID == "" {
			return nil, 0, fmt.Errorf("%w: empty validator id", ErrInvalidCertificate)
		}
		if _, exists := orderedStake[validatorID]; exists {
			return nil, 0, fmt.Errorf("%w: duplicate validator id", ErrInvalidCertificate)
		}
		stake := stakeByValidator[validatorID]
		if stake == 0 {
			return nil, 0, fmt.Errorf("%w: validator stake missing", ErrInvalidCertificate)
		}
		if ^uint64(0)-totalStake < stake {
			return nil, 0, fmt.Errorf("%w: total stake overflow", ErrInvalidCertificate)
		}
		orderedStake[validatorID] = stake
		totalStake += stake
	}
	return orderedStake, totalStake, nil
}

func sameStringSet(left []string, right []string) bool {
	leftCopy := append([]string(nil), left...)
	rightCopy := append([]string(nil), right...)
	sort.Strings(leftCopy)
	sort.Strings(rightCopy)
	if len(leftCopy) != len(rightCopy) {
		return false
	}
	for index := range leftCopy {
		if leftCopy[index] != rightCopy[index] {
			return false
		}
	}
	return true
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}
