package consensus

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/tentechtop/ordexus-core/codec/borsh"
	"github.com/tentechtop/ordexus-core/structure"
)

const (
	ConsensusCodecVersion uint16 = 1

	maxConsensusTextLength      = 128
	maxCertificateVoters        = 2048
	maxConsensusContainerLength = 64 * 1024
)

// VoteType 描述投票语义 + 区分正常区块确认和超时 skip 确认。
type VoteType uint8

const (
	VoteTypeUnknown VoteType = 0
	VoteTypeConfirm VoteType = 1
	VoteTypeSkip    VoteType = 2
	VoteTypePrepare VoteType = 3
)

// Vote 描述验证者投票 + 通过 Borsh 在网络和存储之间复用同一套二进制格式。
type Vote struct {
	Type               VoteType
	Slot               uint64
	BlockHeight        uint64
	BlockHash          structure.Hash
	VoterID            string
	Stake              uint64
	CreatedAtUnixMilli int64
}

// Validate 校验投票输入 + 拒绝非法网络载荷进入共识路径。
func (vote Vote) Validate() error {
	if vote.Type != VoteTypeConfirm && vote.Type != VoteTypeSkip && vote.Type != VoteTypePrepare {
		return fmt.Errorf("%w: unknown vote type", ErrInvalidVote)
	}
	if strings.TrimSpace(vote.VoterID) == "" || len(vote.VoterID) > maxConsensusTextLength {
		return fmt.Errorf("%w: invalid voter id", ErrInvalidVote)
	}
	if vote.Stake == 0 {
		return fmt.Errorf("%w: stake must be positive", ErrInvalidVote)
	}
	if vote.CreatedAtUnixMilli <= 0 {
		return fmt.Errorf("%w: created_at must be positive", ErrInvalidVote)
	}
	if (vote.Type == VoteTypePrepare || vote.Type == VoteTypeConfirm) && vote.BlockHash.IsZero() {
		return fmt.Errorf("%w: block vote requires block hash", ErrInvalidVote)
	}
	if (vote.Type == VoteTypePrepare || vote.Type == VoteTypeConfirm) && vote.BlockHeight == 0 {
		return fmt.Errorf("%w: block vote requires block height", ErrInvalidVote)
	}
	if vote.Type == VoteTypeSkip && vote.BlockHash.IsZero() {
		return fmt.Errorf("%w: skip vote requires parent block hash", ErrInvalidVote)
	}
	if vote.Type == VoteTypeSkip && vote.BlockHeight == 0 {
		return fmt.Errorf("%w: skip vote requires target block height", ErrInvalidVote)
	}
	return nil
}

// MarshalBinary 序列化投票 + Borsh 固定字段顺序保证签名和网络传输确定性。
func (vote Vote) MarshalBinary() ([]byte, error) {
	if err := vote.Validate(); err != nil {
		return nil, err
	}

	writer := borsh.NewWriter(maxConsensusContainerLength)
	writer.WriteUint16(ConsensusCodecVersion)
	writer.WriteUint8(uint8(vote.Type))
	writer.WriteUint64(vote.Slot)
	writer.WriteUint64(vote.BlockHeight)
	writer.WriteFixedBytes(vote.BlockHash[:])
	if err := writer.WriteString(vote.VoterID); err != nil {
		return nil, fmt.Errorf("consensus: marshal voter id: %w", err)
	}
	writer.WriteUint64(vote.Stake)
	writer.WriteInt64(vote.CreatedAtUnixMilli)
	return writer.Bytes(), nil
}

// UnmarshalVoteBinary 反序列化投票 + 解码后继续执行业务字段校验。
func UnmarshalVoteBinary(data []byte) (Vote, error) {
	reader := borsh.NewReader(data, maxConsensusContainerLength)
	version, err := reader.ReadUint16()
	if err != nil {
		return Vote{}, fmt.Errorf("consensus: unmarshal vote version: %w", err)
	}
	if version != ConsensusCodecVersion {
		return Vote{}, fmt.Errorf("%w: unsupported vote version", ErrInvalidVote)
	}

	voteType, err := reader.ReadUint8()
	if err != nil {
		return Vote{}, fmt.Errorf("consensus: unmarshal vote type: %w", err)
	}
	slot, err := reader.ReadUint64()
	if err != nil {
		return Vote{}, fmt.Errorf("consensus: unmarshal vote slot: %w", err)
	}
	blockHeight, err := reader.ReadUint64()
	if err != nil {
		return Vote{}, fmt.Errorf("consensus: unmarshal vote block height: %w", err)
	}
	blockHashBytes, err := reader.ReadFixedBytes(structure.HashSize)
	if err != nil {
		return Vote{}, fmt.Errorf("consensus: unmarshal vote block hash: %w", err)
	}
	voterID, err := reader.ReadString()
	if err != nil {
		return Vote{}, fmt.Errorf("consensus: unmarshal voter id: %w", err)
	}
	stake, err := reader.ReadUint64()
	if err != nil {
		return Vote{}, fmt.Errorf("consensus: unmarshal vote stake: %w", err)
	}
	createdAtUnixMilli, err := reader.ReadInt64()
	if err != nil {
		return Vote{}, fmt.Errorf("consensus: unmarshal vote created_at: %w", err)
	}
	if err := reader.EnsureEOF(); err != nil {
		return Vote{}, fmt.Errorf("consensus: unmarshal vote eof: %w", err)
	}

	blockHash, err := structure.NewHash(blockHashBytes)
	if err != nil {
		return Vote{}, fmt.Errorf("consensus: build vote block hash: %w", err)
	}
	vote := Vote{
		Type:               VoteType(voteType),
		Slot:               slot,
		BlockHeight:        blockHeight,
		BlockHash:          blockHash,
		VoterID:            voterID,
		Stake:              stake,
		CreatedAtUnixMilli: createdAtUnixMilli,
	}
	return vote, vote.Validate()
}

// Quorum 描述确认阈值 + Strict 表示权益必须严格大于该分数。
type Quorum struct {
	Numerator   uint64
	Denominator uint64
	Strict      bool
}

// RequiredStake 计算需要的 stake + 普通阈值向上取整，严格阈值取 floor 后加一。
func (quorum Quorum) RequiredStake(totalStake uint64) (uint64, error) {
	if quorum.Numerator == 0 || quorum.Denominator == 0 {
		return 0, fmt.Errorf("%w: zero value", ErrInvalidQuorum)
	}
	if quorum.Numerator > quorum.Denominator {
		return 0, fmt.Errorf("%w: numerator exceeds denominator", ErrInvalidQuorum)
	}
	if totalStake == 0 {
		return 0, fmt.Errorf("%w: total stake is zero", ErrInvalidQuorum)
	}

	maxUint64 := ^uint64(0)
	if totalStake > (maxUint64-quorum.Denominator+1)/quorum.Numerator {
		return 0, fmt.Errorf("%w: stake overflow", ErrInvalidQuorum)
	}
	if quorum.Strict {
		return totalStake*quorum.Numerator/quorum.Denominator + 1, nil
	}
	return (totalStake*quorum.Numerator + quorum.Denominator - 1) / quorum.Denominator, nil
}

// QuorumCertificate 描述投票确认凭证 + 达到阈值后可通过 P2P 广播给其他节点。
type QuorumCertificate struct {
	Type               VoteType
	Slot               uint64
	BlockHeight        uint64
	BlockHash          structure.Hash
	ThresholdStake     uint64
	ConfirmedStake     uint64
	Voters             []string
	CreatedAtUnixMilli int64
	SignatureScheme    string `json:"signature_scheme,omitempty"`
	AggregateSignature []byte `json:"aggregate_signature,omitempty"`
	VoterBitmap        []byte `json:"voter_bitmap,omitempty"`
}

// Validate 校验证书字段 + 防止伪造或不完整 QC 被上层接受。
func (certificate QuorumCertificate) Validate() error {
	if certificate.Type != VoteTypeConfirm && certificate.Type != VoteTypeSkip && certificate.Type != VoteTypePrepare {
		return fmt.Errorf("%w: unknown certificate type", ErrInvalidCertificate)
	}
	if (certificate.Type == VoteTypePrepare || certificate.Type == VoteTypeConfirm) && certificate.BlockHash.IsZero() {
		return fmt.Errorf("%w: block certificate requires block hash", ErrInvalidCertificate)
	}
	if (certificate.Type == VoteTypePrepare || certificate.Type == VoteTypeConfirm) && certificate.BlockHeight == 0 {
		return fmt.Errorf("%w: block certificate requires block height", ErrInvalidCertificate)
	}
	if certificate.Type == VoteTypeSkip && certificate.BlockHash.IsZero() {
		return fmt.Errorf("%w: skip certificate requires parent block hash", ErrInvalidCertificate)
	}
	if certificate.Type == VoteTypeSkip && certificate.BlockHeight == 0 {
		return fmt.Errorf("%w: skip certificate requires target block height", ErrInvalidCertificate)
	}
	if certificate.ThresholdStake == 0 || certificate.ConfirmedStake < certificate.ThresholdStake {
		return fmt.Errorf("%w: insufficient stake", ErrInvalidCertificate)
	}
	if len(certificate.Voters) == 0 || len(certificate.Voters) > maxCertificateVoters {
		return fmt.Errorf("%w: invalid voters", ErrInvalidCertificate)
	}
	if certificate.CreatedAtUnixMilli <= 0 {
		return fmt.Errorf("%w: created_at must be positive", ErrInvalidCertificate)
	}
	if err := validateAggregateFields(certificate); err != nil {
		return err
	}
	return validateVoterList(certificate.Voters)
}

// MarshalBinary 序列化 QC + Borsh 确保跨节点校验的字节序稳定。
func (certificate QuorumCertificate) MarshalBinary() ([]byte, error) {
	if err := certificate.Validate(); err != nil {
		return nil, err
	}

	writer := borsh.NewWriter(maxConsensusContainerLength)
	writer.WriteUint16(ConsensusCodecVersion)
	writer.WriteUint8(uint8(certificate.Type))
	writer.WriteUint64(certificate.Slot)
	writer.WriteUint64(certificate.BlockHeight)
	writer.WriteFixedBytes(certificate.BlockHash[:])
	writer.WriteUint64(certificate.ThresholdStake)
	writer.WriteUint64(certificate.ConfirmedStake)
	writer.WriteInt64(certificate.CreatedAtUnixMilli)
	writer.WriteUint32(uint32(len(certificate.Voters)))
	for _, voterID := range certificate.Voters {
		if err := writer.WriteString(voterID); err != nil {
			return nil, fmt.Errorf("consensus: marshal qc voter id: %w", err)
		}
	}
	if err := writer.WriteString(certificate.SignatureScheme); err != nil {
		return nil, fmt.Errorf("consensus: marshal qc signature scheme: %w", err)
	}
	if err := writer.WriteBytes(certificate.AggregateSignature); err != nil {
		return nil, fmt.Errorf("consensus: marshal qc aggregate signature: %w", err)
	}
	if err := writer.WriteBytes(certificate.VoterBitmap); err != nil {
		return nil, fmt.Errorf("consensus: marshal qc voter bitmap: %w", err)
	}
	return writer.Bytes(), nil
}

// UnmarshalCertificateBinary 反序列化 QC + 读取后再次校验阈值和投票人列表。
func UnmarshalCertificateBinary(data []byte) (QuorumCertificate, error) {
	reader := borsh.NewReader(data, maxConsensusContainerLength)
	version, err := reader.ReadUint16()
	if err != nil {
		return QuorumCertificate{}, fmt.Errorf("consensus: unmarshal qc version: %w", err)
	}
	if version != ConsensusCodecVersion {
		return QuorumCertificate{}, fmt.Errorf("%w: unsupported qc version", ErrInvalidCertificate)
	}

	certificate, err := readCertificateFields(reader)
	if err != nil {
		return QuorumCertificate{}, err
	}
	if reader.Remaining() == 0 {
		return certificate, certificate.Validate()
	}
	if certificate.SignatureScheme, err = reader.ReadString(); err != nil {
		return QuorumCertificate{}, fmt.Errorf("consensus: unmarshal qc signature scheme: %w", err)
	}
	if certificate.AggregateSignature, err = reader.ReadBytes(); err != nil {
		return QuorumCertificate{}, fmt.Errorf("consensus: unmarshal qc aggregate signature: %w", err)
	}
	if certificate.VoterBitmap, err = reader.ReadBytes(); err != nil {
		return QuorumCertificate{}, fmt.Errorf("consensus: unmarshal qc voter bitmap: %w", err)
	}
	if err := reader.EnsureEOF(); err != nil {
		return QuorumCertificate{}, fmt.Errorf("consensus: unmarshal qc eof: %w", err)
	}
	return certificate, certificate.Validate()
}

// VoteCollector 聚合本地投票 + 用可信 stake 表而不是网络载荷决定投票权重。
type VoteCollector struct {
	mutex                 sync.Mutex
	validatorStake        map[string]uint64
	confirmThresholdStake uint64
	timeoutThresholdStake uint64
	totalStake            uint64
	buckets               map[voteKey]*voteBucket
	voterChoices          map[voterChoiceKey]voteChoice
}

// NewVoteCollector 创建投票聚合器 + 验证者 stake 表是确认阈值的信任根。
func NewVoteCollector(validatorStake map[string]uint64, quorum Quorum) (*VoteCollector, error) {
	return NewVoteCollectorWithQuorums(validatorStake, quorum, quorum)
}

// NewVoteCollectorWithQuorums 为确认票和超时票配置独立阈值。
// 两类证书的最小交集必须严格大于最大拜占庭权益，避免恶意双签者独占交集。
func NewVoteCollectorWithQuorums(
	validatorStake map[string]uint64,
	confirmQuorum Quorum,
	timeoutQuorum Quorum,
) (*VoteCollector, error) {
	normalizedStake, totalStake, err := normalizeValidatorStake(validatorStake)
	if err != nil {
		return nil, err
	}
	confirmThresholdStake, err := confirmQuorum.RequiredStake(totalStake)
	if err != nil {
		return nil, err
	}
	timeoutThresholdStake, err := timeoutQuorum.RequiredStake(totalStake)
	if err != nil {
		return nil, err
	}
	intersectionStake := uint64(0)
	if confirmThresholdStake > totalStake-timeoutThresholdStake {
		intersectionStake = confirmThresholdStake - (totalStake - timeoutThresholdStake)
	}
	maximumByzantineStake := (totalStake - 1) / 3
	if intersectionStake <= maximumByzantineStake {
		return nil, fmt.Errorf("%w: confirm and timeout thresholds lack an honest intersection", ErrInvalidQuorum)
	}

	return &VoteCollector{
		validatorStake:        normalizedStake,
		confirmThresholdStake: confirmThresholdStake,
		timeoutThresholdStake: timeoutThresholdStake,
		totalStake:            totalStake,
		buckets:               make(map[voteKey]*voteBucket),
		voterChoices:          make(map[voterChoiceKey]voteChoice),
	}, nil
}

// AddVote 添加投票 + 同一验证者同一 slot 同一类型只能选择一个结果。
func (collector *VoteCollector) AddVote(vote Vote) (QuorumCertificate, bool, error) {
	return collector.addVote(vote, nil, nil)
}

// AddVoteWithBLS 添加 BLS 投票 + 达到阈值时生成 aggregate_signature 和 voter_bitmap。
func (collector *VoteCollector) AddVoteWithBLS(vote Vote, blsSignature []byte, validatorOrder []string) (QuorumCertificate, bool, error) {
	if len(blsSignature) == 0 {
		return QuorumCertificate{}, false, fmt.Errorf("%w: missing bls vote signature", ErrInvalidVote)
	}
	if len(validatorOrder) == 0 {
		return QuorumCertificate{}, false, fmt.Errorf("%w: missing validator order", ErrInvalidVote)
	}
	return collector.addVote(vote, blsSignature, validatorOrder)
}

func (collector *VoteCollector) addVote(vote Vote, blsSignature []byte, validatorOrder []string) (QuorumCertificate, bool, error) {
	if err := vote.Validate(); err != nil {
		return QuorumCertificate{}, false, err
	}

	collector.mutex.Lock()
	defer collector.mutex.Unlock()

	registeredStake, exists := collector.validatorStake[vote.VoterID]
	if !exists {
		return QuorumCertificate{}, false, fmt.Errorf("%w: %s", ErrUnknownValidator, vote.VoterID)
	}
	if registeredStake != vote.Stake {
		return QuorumCertificate{}, false, fmt.Errorf("%w: stake mismatch", ErrInvalidVote)
	}

	choiceKey := voterChoiceKey{slot: vote.Slot, phase: votePhaseForChoice(vote.Type), voterID: vote.VoterID}
	if existingChoice, exists := collector.voterChoices[choiceKey]; exists {
		if existingChoice.voteType == vote.Type &&
			existingChoice.blockHeight == vote.BlockHeight &&
			existingChoice.blockHash == vote.BlockHash {
			return QuorumCertificate{}, false, ErrDuplicateVote
		}
		return QuorumCertificate{}, false, ErrConflictingVote
	}

	key := voteKey{slot: vote.Slot, voteType: vote.Type, blockHash: vote.BlockHash}
	bucket := collector.bucket(key)
	if bucket.blockHeight != 0 && bucket.blockHeight != vote.BlockHeight {
		return QuorumCertificate{}, false, fmt.Errorf("%w: block height mismatch", ErrInvalidVote)
	}
	previousStake := bucket.stake
	bucket.stake += registeredStake
	bucket.blockHeight = vote.BlockHeight
	bucket.voters[vote.VoterID] = struct{}{}
	if len(blsSignature) > 0 {
		bucket.blsSignatures[vote.VoterID] = append([]byte(nil), blsSignature...)
	}
	if vote.CreatedAtUnixMilli > bucket.createdAtUnixMilli {
		bucket.createdAtUnixMilli = vote.CreatedAtUnixMilli
	}
	collector.voterChoices[choiceKey] = voteChoice{
		voteType:    vote.Type,
		blockHeight: vote.BlockHeight,
		blockHash:   vote.BlockHash,
	}

	thresholdStake := collector.thresholdStakeForVoteType(vote.Type)
	if bucket.stake < thresholdStake {
		return QuorumCertificate{}, false, nil
	}
	// 首次跨过法定人数时立即形成 QC 以推进共识；之后只在收齐全体权重时再形成一次
	// 完整 QC 供奖励统计使用，避免每增加一票都重新聚合并广播证书。
	if previousStake >= thresholdStake && bucket.stake < collector.totalStake {
		return QuorumCertificate{}, false, nil
	}
	certificate := collector.certificate(key, bucket, thresholdStake)
	if len(validatorOrder) == 0 || len(bucket.blsSignatures) != len(bucket.voters) {
		return certificate, true, nil
	}
	aggregateCertificate, err := AttachBLSAggregate(certificate, validatorOrder, bucket.blsSignatures)
	if err != nil {
		return QuorumCertificate{}, false, err
	}
	return aggregateCertificate, true, nil
}

// ThresholdStake 返回确认阈值 + 测试和调试可以直接观察本地配置结果。
func (collector *VoteCollector) ThresholdStake() uint64 {
	collector.mutex.Lock()
	defer collector.mutex.Unlock()
	return collector.confirmThresholdStake
}

// ThresholdStakeForVoteType 返回指定阶段的证书阈值。
func (collector *VoteCollector) ThresholdStakeForVoteType(voteType VoteType) uint64 {
	collector.mutex.Lock()
	defer collector.mutex.Unlock()
	return collector.thresholdStakeForVoteType(voteType)
}

// TotalStake 返回总 stake + 用于确认阈值和日志展示。
func (collector *VoteCollector) TotalStake() uint64 {
	collector.mutex.Lock()
	defer collector.mutex.Unlock()
	return collector.totalStake
}

type voteKey struct {
	slot      uint64
	voteType  VoteType
	blockHash structure.Hash
}

type voterChoiceKey struct {
	slot    uint64
	phase   votePhase
	voterID string
}

type votePhase uint8

const (
	votePhasePrepare votePhase = iota + 1
	votePhaseDecision
)

func votePhaseForChoice(voteType VoteType) votePhase {
	if voteType == VoteTypePrepare {
		return votePhasePrepare
	}
	return votePhaseDecision
}

type voteChoice struct {
	voteType    VoteType
	blockHeight uint64
	blockHash   structure.Hash
}

type voteBucket struct {
	blockHeight        uint64
	stake              uint64
	voters             map[string]struct{}
	blsSignatures      map[string][]byte
	createdAtUnixMilli int64
}

func (collector *VoteCollector) bucket(key voteKey) *voteBucket {
	bucket, exists := collector.buckets[key]
	if exists {
		return bucket
	}
	bucket = &voteBucket{
		voters:        make(map[string]struct{}),
		blsSignatures: make(map[string][]byte),
	}
	collector.buckets[key] = bucket
	return bucket
}

func (collector *VoteCollector) thresholdStakeForVoteType(voteType VoteType) uint64 {
	if voteType == VoteTypeSkip {
		return collector.timeoutThresholdStake
	}
	return collector.confirmThresholdStake
}

func (collector *VoteCollector) certificate(key voteKey, bucket *voteBucket, thresholdStake uint64) QuorumCertificate {
	voters := make([]string, 0, len(bucket.voters))
	for voterID := range bucket.voters {
		voters = append(voters, voterID)
	}
	sort.Strings(voters)
	return QuorumCertificate{
		Type:               key.voteType,
		Slot:               key.slot,
		BlockHeight:        bucket.blockHeight,
		BlockHash:          key.blockHash,
		ThresholdStake:     thresholdStake,
		ConfirmedStake:     bucket.stake,
		Voters:             voters,
		CreatedAtUnixMilli: bucket.createdAtUnixMilli,
	}
}

func readCertificateFields(reader *borsh.Reader) (QuorumCertificate, error) {
	certificateType, err := reader.ReadUint8()
	if err != nil {
		return QuorumCertificate{}, fmt.Errorf("consensus: unmarshal qc type: %w", err)
	}
	slot, err := reader.ReadUint64()
	if err != nil {
		return QuorumCertificate{}, fmt.Errorf("consensus: unmarshal qc slot: %w", err)
	}
	blockHeight, err := reader.ReadUint64()
	if err != nil {
		return QuorumCertificate{}, fmt.Errorf("consensus: unmarshal qc block height: %w", err)
	}
	blockHashBytes, err := reader.ReadFixedBytes(structure.HashSize)
	if err != nil {
		return QuorumCertificate{}, fmt.Errorf("consensus: unmarshal qc block hash: %w", err)
	}
	thresholdStake, err := reader.ReadUint64()
	if err != nil {
		return QuorumCertificate{}, fmt.Errorf("consensus: unmarshal qc threshold: %w", err)
	}
	confirmedStake, err := reader.ReadUint64()
	if err != nil {
		return QuorumCertificate{}, fmt.Errorf("consensus: unmarshal qc stake: %w", err)
	}
	createdAtUnixMilli, err := reader.ReadInt64()
	if err != nil {
		return QuorumCertificate{}, fmt.Errorf("consensus: unmarshal qc created_at: %w", err)
	}
	voters, err := readVoters(reader)
	if err != nil {
		return QuorumCertificate{}, err
	}

	blockHash, err := structure.NewHash(blockHashBytes)
	if err != nil {
		return QuorumCertificate{}, fmt.Errorf("consensus: build qc block hash: %w", err)
	}
	return QuorumCertificate{
		Type:               VoteType(certificateType),
		Slot:               slot,
		BlockHeight:        blockHeight,
		BlockHash:          blockHash,
		ThresholdStake:     thresholdStake,
		ConfirmedStake:     confirmedStake,
		Voters:             voters,
		CreatedAtUnixMilli: createdAtUnixMilli,
	}, nil
}

func readVoters(reader *borsh.Reader) ([]string, error) {
	voterCount, err := reader.ReadUint32()
	if err != nil {
		return nil, fmt.Errorf("consensus: unmarshal qc voter count: %w", err)
	}
	if voterCount == 0 || voterCount > maxCertificateVoters {
		return nil, fmt.Errorf("%w: invalid voter count", ErrInvalidCertificate)
	}

	voters := make([]string, 0, voterCount)
	for index := uint32(0); index < voterCount; index++ {
		voterID, err := reader.ReadString()
		if err != nil {
			return nil, fmt.Errorf("consensus: unmarshal qc voter %d: %w", index, err)
		}
		voters = append(voters, voterID)
	}
	return voters, nil
}

func normalizeValidatorStake(validatorStake map[string]uint64) (map[string]uint64, uint64, error) {
	if len(validatorStake) == 0 {
		return nil, 0, fmt.Errorf("%w: empty validator set", ErrInvalidVote)
	}

	normalized := make(map[string]uint64, len(validatorStake))
	var totalStake uint64
	for validatorID, stake := range validatorStake {
		if strings.TrimSpace(validatorID) == "" || len(validatorID) > maxConsensusTextLength {
			return nil, 0, fmt.Errorf("%w: invalid validator id", ErrInvalidVote)
		}
		if stake == 0 {
			return nil, 0, fmt.Errorf("%w: zero validator stake", ErrInvalidVote)
		}
		if ^uint64(0)-totalStake < stake {
			return nil, 0, fmt.Errorf("%w: total stake overflow", ErrInvalidVote)
		}
		normalized[validatorID] = stake
		totalStake += stake
	}
	return normalized, totalStake, nil
}

func validateVoterList(voters []string) error {
	seen := make(map[string]struct{}, len(voters))
	previousVoterID := ""
	for index, voterID := range voters {
		if strings.TrimSpace(voterID) == "" || len(voterID) > maxConsensusTextLength {
			return fmt.Errorf("%w: invalid voter id", ErrInvalidCertificate)
		}
		if index > 0 && voterID <= previousVoterID {
			return fmt.Errorf("%w: voters must be sorted and unique", ErrInvalidCertificate)
		}
		if _, exists := seen[voterID]; exists {
			return fmt.Errorf("%w: duplicate voter id", ErrInvalidCertificate)
		}
		seen[voterID] = struct{}{}
		previousVoterID = voterID
	}
	return nil
}

func validateAggregateFields(certificate QuorumCertificate) error {
	hasAggregate := certificate.SignatureScheme != "" || len(certificate.AggregateSignature) > 0 || len(certificate.VoterBitmap) > 0
	if !hasAggregate {
		return nil
	}
	if certificate.SignatureScheme != BLSSignatureSchemeBasic {
		return fmt.Errorf("%w: unsupported aggregate signature scheme", ErrInvalidCertificate)
	}
	if len(certificate.AggregateSignature) == 0 {
		return fmt.Errorf("%w: missing aggregate signature", ErrInvalidCertificate)
	}
	if len(certificate.VoterBitmap) == 0 {
		return fmt.Errorf("%w: missing voter bitmap", ErrInvalidCertificate)
	}
	return nil
}
