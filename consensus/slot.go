package consensus

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"solana_golang/codec/borsh"
	"solana_golang/structure"
)

const (
	ConsensusCodecVersion uint16 = 1
	DefaultSlotDuration          = 400 * time.Millisecond
	DefaultSkipTimeout           = 250 * time.Millisecond

	maxConsensusTextLength      = 128
	maxCertificateVoters        = 2048
	maxConsensusContainerLength = 64 * 1024
)

// SlotClock 提供本地 slot 时钟 + 使用 time.Time 单调分量避免墙上时间回拨影响 slot 推进。
type SlotClock struct {
	startedAt    time.Time
	initialSlot  uint64
	slotDuration time.Duration
	skipTimeout  time.Duration
}

// SlotTick 描述当前 slot 观测结果 + 让调用方决定是否广播 skip 投票。
type SlotTick struct {
	Slot          uint64
	SlotStartedAt time.Time
	SlotDeadline  time.Time
	Elapsed       time.Duration
	ShouldSkip    bool
}

// NewSlotClock 创建 slot 时钟 + 固定 slot 时间和 skip 超时必须显式校验。
func NewSlotClock(startedAt time.Time, initialSlot uint64, slotDuration time.Duration, skipTimeout time.Duration) (*SlotClock, error) {
	if startedAt.IsZero() {
		return nil, fmt.Errorf("%w: started_at is zero", ErrInvalidClockConfig)
	}
	if slotDuration <= 0 {
		return nil, fmt.Errorf("%w: slot duration must be positive", ErrInvalidClockConfig)
	}
	if skipTimeout <= 0 || skipTimeout > slotDuration {
		return nil, fmt.Errorf("%w: skip timeout must be within slot duration", ErrInvalidClockConfig)
	}

	return &SlotClock{
		startedAt:    startedAt,
		initialSlot:  initialSlot,
		slotDuration: slotDuration,
		skipTimeout:  skipTimeout,
	}, nil
}

// Tick 计算当前 slot + startedAt 和 now 来自 time.Now 时 Go 会优先使用单调时钟差值。
func (clock *SlotClock) Tick(now time.Time) SlotTick {
	elapsed := now.Sub(clock.startedAt)
	if elapsed < 0 {
		elapsed = 0
	}

	slotOffset := uint64(elapsed / clock.slotDuration)
	slot := clock.initialSlot + slotOffset
	if slot < clock.initialSlot {
		slot = ^uint64(0)
	}

	slotStartedAt := clock.SlotStart(slot)
	slotDeadline := slotStartedAt.Add(clock.skipTimeout)
	return SlotTick{
		Slot:          slot,
		SlotStartedAt: slotStartedAt,
		SlotDeadline:  slotDeadline,
		Elapsed:       elapsed,
		ShouldSkip:    !now.Before(slotDeadline),
	}
}

// SlotStart 计算 slot 起始时间 + 统一由初始时间和固定 slot 时长推导。
func (clock *SlotClock) SlotStart(slot uint64) time.Time {
	if slot <= clock.initialSlot {
		return clock.startedAt
	}

	slotOffset := slot - clock.initialSlot
	maxOffset := uint64(time.Duration(1<<63-1) / clock.slotDuration)
	if slotOffset > maxOffset {
		return clock.startedAt.Add(time.Duration(1<<63 - 1))
	}
	return clock.startedAt.Add(time.Duration(slotOffset) * clock.slotDuration)
}

// SlotDeadline 计算 slot 超时时间 + skip 判断需要稳定的本地截止点。
func (clock *SlotClock) SlotDeadline(slot uint64) time.Time {
	return clock.SlotStart(slot).Add(clock.skipTimeout)
}

// VoteType 描述投票语义 + 区分正常区块确认和超时 skip 确认。
type VoteType uint8

const (
	VoteTypeUnknown VoteType = 0
	VoteTypeConfirm VoteType = 1
	VoteTypeSkip    VoteType = 2
)

// Vote 描述验证者投票 + 通过 Borsh 在网络和存储之间复用同一套二进制格式。
type Vote struct {
	Type               VoteType
	Slot               uint64
	BlockHash          structure.Hash
	VoterID            string
	Stake              uint64
	CreatedAtUnixMilli int64
}

// Validate 校验投票输入 + 拒绝非法网络载荷进入共识路径。
func (vote Vote) Validate() error {
	if vote.Type != VoteTypeConfirm && vote.Type != VoteTypeSkip {
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
	if vote.Type == VoteTypeConfirm && vote.BlockHash.IsZero() {
		return fmt.Errorf("%w: confirm vote requires block hash", ErrInvalidVote)
	}
	if vote.Type == VoteTypeSkip && !vote.BlockHash.IsZero() {
		return fmt.Errorf("%w: skip vote requires empty block hash", ErrInvalidVote)
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
		BlockHash:          blockHash,
		VoterID:            voterID,
		Stake:              stake,
		CreatedAtUnixMilli: createdAtUnixMilli,
	}
	return vote, vote.Validate()
}

// Quorum 描述确认阈值 + 使用整数分数避免浮点误差。
type Quorum struct {
	Numerator   uint64
	Denominator uint64
}

// RequiredStake 计算需要的 stake + 向上取整保证阈值不会被低估。
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
	return (totalStake*quorum.Numerator + quorum.Denominator - 1) / quorum.Denominator, nil
}

// QuorumCertificate 描述投票确认凭证 + 达到阈值后可通过 P2P 广播给其他节点。
type QuorumCertificate struct {
	Type               VoteType
	Slot               uint64
	BlockHash          structure.Hash
	ThresholdStake     uint64
	ConfirmedStake     uint64
	Voters             []string
	CreatedAtUnixMilli int64
}

// Validate 校验证书字段 + 防止伪造或不完整 QC 被上层接受。
func (certificate QuorumCertificate) Validate() error {
	if certificate.Type != VoteTypeConfirm && certificate.Type != VoteTypeSkip {
		return fmt.Errorf("%w: unknown certificate type", ErrInvalidCertificate)
	}
	if certificate.Type == VoteTypeConfirm && certificate.BlockHash.IsZero() {
		return fmt.Errorf("%w: confirm certificate requires block hash", ErrInvalidCertificate)
	}
	if certificate.Type == VoteTypeSkip && !certificate.BlockHash.IsZero() {
		return fmt.Errorf("%w: skip certificate requires empty block hash", ErrInvalidCertificate)
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
	if err := reader.EnsureEOF(); err != nil {
		return QuorumCertificate{}, fmt.Errorf("consensus: unmarshal qc eof: %w", err)
	}
	return certificate, certificate.Validate()
}

// VoteCollector 聚合本地投票 + 用可信 stake 表而不是网络载荷决定投票权重。
type VoteCollector struct {
	mutex          sync.Mutex
	validatorStake map[string]uint64
	thresholdStake uint64
	totalStake     uint64
	buckets        map[voteKey]*voteBucket
	voterChoices   map[voterChoiceKey]structure.Hash
}

// NewVoteCollector 创建投票聚合器 + 验证者 stake 表是确认阈值的信任根。
func NewVoteCollector(validatorStake map[string]uint64, quorum Quorum) (*VoteCollector, error) {
	normalizedStake, totalStake, err := normalizeValidatorStake(validatorStake)
	if err != nil {
		return nil, err
	}
	thresholdStake, err := quorum.RequiredStake(totalStake)
	if err != nil {
		return nil, err
	}

	return &VoteCollector{
		validatorStake: normalizedStake,
		thresholdStake: thresholdStake,
		totalStake:     totalStake,
		buckets:        make(map[voteKey]*voteBucket),
		voterChoices:   make(map[voterChoiceKey]structure.Hash),
	}, nil
}

// AddVote 添加投票 + 同一验证者同一 slot 同一类型只能选择一个结果。
func (collector *VoteCollector) AddVote(vote Vote) (QuorumCertificate, bool, error) {
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

	choiceKey := voterChoiceKey{slot: vote.Slot, voteType: vote.Type, voterID: vote.VoterID}
	if existingHash, exists := collector.voterChoices[choiceKey]; exists {
		return QuorumCertificate{}, false, duplicateOrConflictError(existingHash, vote.BlockHash)
	}

	key := voteKey{slot: vote.Slot, voteType: vote.Type, blockHash: vote.BlockHash}
	bucket := collector.bucket(key)
	bucket.stake += registeredStake
	bucket.voters[vote.VoterID] = struct{}{}
	if vote.CreatedAtUnixMilli > bucket.createdAtUnixMilli {
		bucket.createdAtUnixMilli = vote.CreatedAtUnixMilli
	}
	collector.voterChoices[choiceKey] = vote.BlockHash

	if bucket.stake < collector.thresholdStake {
		return QuorumCertificate{}, false, nil
	}
	return collector.certificate(key, bucket), true, nil
}

// ThresholdStake 返回确认阈值 + 测试和调试可以直接观察本地配置结果。
func (collector *VoteCollector) ThresholdStake() uint64 {
	collector.mutex.Lock()
	defer collector.mutex.Unlock()
	return collector.thresholdStake
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
	slot     uint64
	voteType VoteType
	voterID  string
}

type voteBucket struct {
	stake              uint64
	voters             map[string]struct{}
	createdAtUnixMilli int64
}

func (collector *VoteCollector) bucket(key voteKey) *voteBucket {
	bucket, exists := collector.buckets[key]
	if exists {
		return bucket
	}
	bucket = &voteBucket{voters: make(map[string]struct{})}
	collector.buckets[key] = bucket
	return bucket
}

func (collector *VoteCollector) certificate(key voteKey, bucket *voteBucket) QuorumCertificate {
	voters := make([]string, 0, len(bucket.voters))
	for voterID := range bucket.voters {
		voters = append(voters, voterID)
	}
	sort.Strings(voters)
	return QuorumCertificate{
		Type:               key.voteType,
		Slot:               key.slot,
		BlockHash:          key.blockHash,
		ThresholdStake:     collector.thresholdStake,
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

func duplicateOrConflictError(existingHash structure.Hash, incomingHash structure.Hash) error {
	if existingHash == incomingHash {
		return ErrDuplicateVote
	}
	return ErrConflictingVote
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
