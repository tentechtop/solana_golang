package stake

import (
	stdcontext "context"
	"fmt"
	"log/slog"

	"solana_golang/codec/borsh"
	"solana_golang/runtime"
	"solana_golang/structure"
	"solana_golang/utils"
)

const (
	MaxPeerIDLength      = 128
	MaxBLSPublicKeyBytes = 128
	MaxStakeStateBytes   = 32768
	MaxDelegations       = 256
	MinimumStakeLamports = uint64(10_000_000)
)

type InstructionType uint32

const (
	InstructionRegisterValidator InstructionType = iota
	InstructionStake
	InstructionUnstake
	InstructionWithdrawUnstaked
	InstructionExitValidator
	InstructionSlashValidator
	InstructionJailValidator
	InstructionDelegate
	InstructionUndelegate
	InstructionWithdrawDelegation
	InstructionUpdateCommission
)

type ValidatorStatus uint8

const (
	ValidatorStatusActive ValidatorStatus = iota + 1
	ValidatorStatusExiting
	ValidatorStatusJailed
)

// Program 执行质押固定指令 + 通过账户数据生成下个 epoch 的验证者快照。
type Program struct {
	programID structure.PublicKey
}

// Instruction 描述质押指令 + 使用固定二进制格式避免 JSON 注入和字段歧义。
type Instruction struct {
	Type               InstructionType
	ConsensusPublicKey structure.PublicKey
	BLSPublicKey       []byte
	P2PPeerID          string
	CommissionBps      uint16
	Amount             uint64
	UnlockEpoch        uint64
}

// ValidatorState 描述质押账户数据 + 由共识组合层转换为 consensus.ValidatorState。
type ValidatorState struct {
	ConsensusPublicKey       structure.PublicKey
	BLSPublicKey             []byte
	StakerAccount            structure.PublicKey
	P2PPeerID                string
	CommissionBps            uint16
	ActiveStake              uint64
	PendingStake             uint64
	UnlockingStake           uint64
	UnlockEpoch              uint64
	Status                   ValidatorStatus
	VoteCredits              uint64
	LastVoteSlot             uint64
	LastRewardedSlot         uint64
	LastRewardEpoch          uint64
	RewardLamports           uint64
	SelfRewardLamports       uint64
	CommissionRewardLamports uint64
	MissedVoteCount          uint64
	MissedProposalCount      uint64
	JailUntilEpoch           uint64
	ActivationEpoch          uint64
	DeactivationEpoch        uint64
	LastEffectiveStake       uint64
	LastSlashedSlot          uint64
	Delegations              []DelegationState
}

// DelegationState 描述普通用户委托 + 与验证者自质押分离以便按份额自动分配收益。
type DelegationState struct {
	DelegatorAccount  structure.PublicKey
	ActiveStake       uint64
	PendingStake      uint64
	UnlockingStake    uint64
	UnlockEpoch       uint64
	ActivationEpoch   uint64
	DeactivationEpoch uint64
	RewardLamports    uint64
}

// NewProgram 创建质押程序 + 由组合层显式注册到 runtime。
func NewProgram(programID structure.PublicKey) Program {
	return Program{programID: programID}
}

// ProgramID 返回质押程序 ID + runtime 使用该值分发指令。
func (program Program) ProgramID() structure.PublicKey {
	return program.programID
}

// Execute 执行质押指令 + 所有状态变更只写入本次账户快照。
func (program Program) Execute(context runtime.InstructionContext) error {
	instruction, err := UnmarshalInstructionBinary(context.Instruction.Data)
	if err != nil {
		return err
	}
	switch instruction.Type {
	case InstructionRegisterValidator:
		return executeRegisterValidator(instruction, context)
	case InstructionStake:
		return executeStake(instruction, context)
	case InstructionUnstake:
		return executeUnstake(instruction, context)
	case InstructionWithdrawUnstaked:
		return executeWithdrawUnstaked(instruction, context)
	case InstructionExitValidator:
		return executeExitValidator(context)
	case InstructionSlashValidator:
		return executeSlashValidator(instruction, context)
	case InstructionJailValidator:
		return executeJailValidator(instruction, context)
	case InstructionDelegate:
		return executeDelegate(instruction, context)
	case InstructionUndelegate:
		return executeUndelegate(instruction, context)
	case InstructionWithdrawDelegation:
		return executeWithdrawDelegation(instruction, context)
	case InstructionUpdateCommission:
		return executeUpdateCommission(instruction, context)
	default:
		return fmt.Errorf("stake: unsupported instruction type %d", instruction.Type)
	}
}

// NewRegisterValidatorInstruction 创建注册指令 + 强制初始质押满足最低要求。
func NewRegisterValidatorInstruction(consensusPublicKey structure.PublicKey, p2pPeerID string, commissionBps uint16, amount uint64) (Instruction, error) {
	return NewRegisterValidatorInstructionWithBLS(consensusPublicKey, nil, p2pPeerID, commissionBps, amount)
}

// NewRegisterValidatorInstructionWithBLS 创建带 BLS 公钥的注册指令 + QC 聚合验证需要从验证者集合读取公钥。
func NewRegisterValidatorInstructionWithBLS(consensusPublicKey structure.PublicKey, blsPublicKey []byte, p2pPeerID string, commissionBps uint16, amount uint64) (Instruction, error) {
	instruction := Instruction{
		Type:               InstructionRegisterValidator,
		ConsensusPublicKey: consensusPublicKey,
		BLSPublicKey:       cloneBytes(blsPublicKey),
		P2PPeerID:          p2pPeerID,
		CommissionBps:      commissionBps,
		Amount:             amount,
	}
	return instruction, instruction.Validate()
}

// NewStakeInstruction 创建追加质押指令 + stake 延迟到下个 epoch 生效。
func NewStakeInstruction(amount uint64) (Instruction, error) {
	instruction := Instruction{Type: InstructionStake, Amount: amount}
	return instruction, instruction.Validate()
}

// NewUnstakeInstruction 创建解除质押指令 + 资金进入 unlocking 等待提现。
func NewUnstakeInstruction(amount uint64, unlockEpoch uint64) (Instruction, error) {
	instruction := Instruction{Type: InstructionUnstake, Amount: amount, UnlockEpoch: unlockEpoch}
	return instruction, instruction.Validate()
}

// NewWithdrawUnstakedInstruction 创建提现指令 + 只允许已到期 unlocking stake。
func NewWithdrawUnstakedInstruction(currentEpoch uint64) (Instruction, error) {
	instruction := Instruction{Type: InstructionWithdrawUnstaked, UnlockEpoch: currentEpoch}
	return instruction, instruction.Validate()
}

// NewExitValidatorInstruction 创建退出指令 + 退出后不进入下个 active set。
func NewExitValidatorInstruction() Instruction {
	return Instruction{Type: InstructionExitValidator}
}

// Validate 校验质押指令 + 在程序执行前拦截非法金额和元数据。
// NewSlashValidatorInstruction 创建罚没指令 + 作恶证据裁决后需要把惩罚写入 stake account。
func NewSlashValidatorInstruction(amount uint64) (Instruction, error) {
	instruction := Instruction{Type: InstructionSlashValidator, Amount: amount}
	return instruction, instruction.Validate()
}

// NewJailValidatorInstruction 创建 jail 指令 + jailed validator 在指定 epoch 前不能参与 leader 选举。
func NewJailValidatorInstruction(jailUntilEpoch uint64) (Instruction, error) {
	instruction := Instruction{Type: InstructionJailValidator, UnlockEpoch: jailUntilEpoch}
	return instruction, instruction.Validate()
}

// NewDelegateInstruction 创建委托质押指令 + 普通用户 stake 延迟到后续 epoch 生效。
func NewDelegateInstruction(amount uint64) (Instruction, error) {
	instruction := Instruction{Type: InstructionDelegate, Amount: amount}
	return instruction, instruction.Validate()
}

// NewUndelegateInstruction 创建取消委托指令 + active 委托进入 unlocking 后等待提现。
func NewUndelegateInstruction(amount uint64, unlockEpoch uint64) (Instruction, error) {
	instruction := Instruction{Type: InstructionUndelegate, Amount: amount, UnlockEpoch: unlockEpoch}
	return instruction, instruction.Validate()
}

// NewWithdrawDelegationInstruction 创建委托提现指令 + 到期 unlocking 资金回到委托人账户。
func NewWithdrawDelegationInstruction(currentEpoch uint64) (Instruction, error) {
	instruction := Instruction{Type: InstructionWithdrawDelegation, UnlockEpoch: currentEpoch}
	return instruction, instruction.Validate()
}

// NewUpdateCommissionInstruction 创建佣金调整指令 + 允许验证者钱包链上修改委托奖励分成。
func NewUpdateCommissionInstruction(commissionBps uint16) (Instruction, error) {
	instruction := Instruction{Type: InstructionUpdateCommission, CommissionBps: commissionBps}
	return instruction, instruction.Validate()
}

func (instruction Instruction) Validate() error {
	if instruction.P2PPeerID != "" && len(instruction.P2PPeerID) > MaxPeerIDLength {
		return fmt.Errorf("stake: p2p peer id too long")
	}
	if len(instruction.BLSPublicKey) > MaxBLSPublicKeyBytes {
		return fmt.Errorf("stake: bls public key too long")
	}
	if instruction.CommissionBps > 10000 {
		return fmt.Errorf("stake: commission exceeds 10000 bps")
	}
	switch instruction.Type {
	case InstructionRegisterValidator:
		return validateRegisterInstruction(instruction)
	case InstructionStake, InstructionUnstake, InstructionDelegate, InstructionUndelegate:
		return validateStakeAmount(instruction.Amount)
	case InstructionSlashValidator:
		if instruction.Amount == 0 {
			return fmt.Errorf("stake: slash amount is zero")
		}
		return nil
	case InstructionWithdrawUnstaked, InstructionExitValidator, InstructionJailValidator, InstructionWithdrawDelegation, InstructionUpdateCommission:
		return nil
	default:
		return fmt.Errorf("stake: invalid instruction type %d", instruction.Type)
	}
}

// MarshalBinary 序列化质押指令 + 固定字段顺序供交易签名和执行复算。
func (instruction Instruction) MarshalBinary() ([]byte, error) {
	if err := instruction.Validate(); err != nil {
		return nil, err
	}
	writer := borsh.NewWriter(MaxStakeStateBytes)
	writer.WriteUint32(uint32(instruction.Type))
	writer.WriteFixedBytes(instruction.ConsensusPublicKey[:])
	if err := writer.WriteString(instruction.P2PPeerID); err != nil {
		return nil, fmt.Errorf("stake: encode p2p peer id: %w", err)
	}
	writer.WriteUint16(instruction.CommissionBps)
	writer.WriteUint64(instruction.Amount)
	writer.WriteUint64(instruction.UnlockEpoch)
	if err := writer.WriteBytes(instruction.BLSPublicKey); err != nil {
		return nil, fmt.Errorf("stake: encode bls public key: %w", err)
	}
	return writer.Bytes(), nil
}

// UnmarshalInstructionBinary 反序列化质押指令 + 解码后继续做边界检查。
func UnmarshalInstructionBinary(data []byte) (Instruction, error) {
	reader := borsh.NewReader(data, MaxStakeStateBytes)
	instructionType, err := reader.ReadUint32()
	if err != nil {
		return Instruction{}, fmt.Errorf("stake: decode instruction type: %w", err)
	}
	consensusPublicKey, err := readPublicKey(reader, "consensus public key")
	if err != nil {
		return Instruction{}, err
	}
	p2pPeerID, err := reader.ReadString()
	if err != nil {
		return Instruction{}, fmt.Errorf("stake: decode p2p peer id: %w", err)
	}
	commissionBps, err := reader.ReadUint16()
	if err != nil {
		return Instruction{}, fmt.Errorf("stake: decode commission: %w", err)
	}
	amount, err := reader.ReadUint64()
	if err != nil {
		return Instruction{}, fmt.Errorf("stake: decode amount: %w", err)
	}
	unlockEpoch, err := reader.ReadUint64()
	if err != nil {
		return Instruction{}, fmt.Errorf("stake: decode unlock epoch: %w", err)
	}
	instruction := Instruction{
		Type:               InstructionType(instructionType),
		ConsensusPublicKey: consensusPublicKey,
		P2PPeerID:          p2pPeerID,
		CommissionBps:      commissionBps,
		Amount:             amount,
		UnlockEpoch:        unlockEpoch,
	}
	if reader.Remaining() == 0 {
		return instruction, instruction.Validate()
	}
	if instruction.BLSPublicKey, err = reader.ReadBytes(); err != nil {
		return Instruction{}, fmt.Errorf("stake: decode bls public key: %w", err)
	}
	if err := reader.EnsureEOF(); err != nil {
		return Instruction{}, fmt.Errorf("stake: decode instruction eof: %w", err)
	}
	return instruction, instruction.Validate()
}

// MarshalBinary 序列化验证者状态 + 作为 stake 账户数据写入账本。
func (state ValidatorState) MarshalBinary() ([]byte, error) {
	if err := state.Validate(); err != nil {
		return nil, err
	}
	writer := borsh.NewWriter(MaxStakeStateBytes)
	writer.WriteFixedBytes(state.ConsensusPublicKey[:])
	writer.WriteFixedBytes(state.StakerAccount[:])
	if err := writer.WriteString(state.P2PPeerID); err != nil {
		return nil, fmt.Errorf("stake: encode validator p2p peer id: %w", err)
	}
	writer.WriteUint16(state.CommissionBps)
	writer.WriteUint64(state.ActiveStake)
	writer.WriteUint64(state.PendingStake)
	writer.WriteUint64(state.UnlockingStake)
	writer.WriteUint64(state.UnlockEpoch)
	writer.WriteUint8(uint8(state.Status))
	writer.WriteUint64(state.VoteCredits)
	writer.WriteUint64(state.LastVoteSlot)
	writer.WriteUint64(state.LastRewardedSlot)
	writer.WriteUint64(state.LastRewardEpoch)
	writer.WriteUint64(state.RewardLamports)
	writer.WriteUint64(state.MissedVoteCount)
	writer.WriteUint64(state.MissedProposalCount)
	writer.WriteUint64(state.JailUntilEpoch)
	writer.WriteUint64(state.ActivationEpoch)
	writer.WriteUint64(state.DeactivationEpoch)
	writer.WriteUint64(state.LastEffectiveStake)
	if err := writer.WriteBytes(state.BLSPublicKey); err != nil {
		return nil, fmt.Errorf("stake: encode validator bls public key: %w", err)
	}
	writer.WriteUint64(state.LastSlashedSlot)
	writer.WriteUint32(uint32(len(state.Delegations)))
	for _, delegation := range state.Delegations {
		writer.WriteFixedBytes(delegation.DelegatorAccount[:])
		writer.WriteUint64(delegation.ActiveStake)
		writer.WriteUint64(delegation.PendingStake)
		writer.WriteUint64(delegation.UnlockingStake)
		writer.WriteUint64(delegation.UnlockEpoch)
		writer.WriteUint64(delegation.ActivationEpoch)
		writer.WriteUint64(delegation.DeactivationEpoch)
		writer.WriteUint64(delegation.RewardLamports)
	}
	writer.WriteUint64(state.SelfRewardLamports)
	writer.WriteUint64(state.CommissionRewardLamports)
	return writer.Bytes(), nil
}

// UnmarshalValidatorStateBinary 反序列化验证者状态 + 供程序和共识快照读取。
func UnmarshalValidatorStateBinary(data []byte) (ValidatorState, error) {
	reader := borsh.NewReader(data, MaxStakeStateBytes)
	consensusPublicKey, err := readPublicKey(reader, "validator consensus public key")
	if err != nil {
		return ValidatorState{}, err
	}
	stakerAccount, err := readPublicKey(reader, "validator staker account")
	if err != nil {
		return ValidatorState{}, err
	}
	p2pPeerID, err := reader.ReadString()
	if err != nil {
		return ValidatorState{}, fmt.Errorf("stake: decode validator p2p peer id: %w", err)
	}
	commissionBps, err := reader.ReadUint16()
	if err != nil {
		return ValidatorState{}, fmt.Errorf("stake: decode commission: %w", err)
	}
	activeStake, err := reader.ReadUint64()
	if err != nil {
		return ValidatorState{}, fmt.Errorf("stake: decode active stake: %w", err)
	}
	pendingStake, err := reader.ReadUint64()
	if err != nil {
		return ValidatorState{}, fmt.Errorf("stake: decode pending stake: %w", err)
	}
	unlockingStake, err := reader.ReadUint64()
	if err != nil {
		return ValidatorState{}, fmt.Errorf("stake: decode unlocking stake: %w", err)
	}
	unlockEpoch, err := reader.ReadUint64()
	if err != nil {
		return ValidatorState{}, fmt.Errorf("stake: decode unlock epoch: %w", err)
	}
	status, err := reader.ReadUint8()
	if err != nil {
		return ValidatorState{}, fmt.Errorf("stake: decode status: %w", err)
	}
	state := ValidatorState{
		ConsensusPublicKey: consensusPublicKey,
		StakerAccount:      stakerAccount,
		P2PPeerID:          p2pPeerID,
		CommissionBps:      commissionBps,
		ActiveStake:        activeStake,
		PendingStake:       pendingStake,
		UnlockingStake:     unlockingStake,
		UnlockEpoch:        unlockEpoch,
		Status:             ValidatorStatus(status),
	}
	if reader.Remaining() == 0 {
		return state, state.Validate()
	}
	if state.VoteCredits, err = reader.ReadUint64(); err != nil {
		return ValidatorState{}, fmt.Errorf("stake: decode vote credits: %w", err)
	}
	if state.LastVoteSlot, err = reader.ReadUint64(); err != nil {
		return ValidatorState{}, fmt.Errorf("stake: decode last vote slot: %w", err)
	}
	if state.LastRewardedSlot, err = reader.ReadUint64(); err != nil {
		return ValidatorState{}, fmt.Errorf("stake: decode last rewarded slot: %w", err)
	}
	if state.LastRewardEpoch, err = reader.ReadUint64(); err != nil {
		return ValidatorState{}, fmt.Errorf("stake: decode last reward epoch: %w", err)
	}
	if state.RewardLamports, err = reader.ReadUint64(); err != nil {
		return ValidatorState{}, fmt.Errorf("stake: decode reward lamports: %w", err)
	}
	if state.MissedVoteCount, err = reader.ReadUint64(); err != nil {
		return ValidatorState{}, fmt.Errorf("stake: decode missed vote count: %w", err)
	}
	if state.MissedProposalCount, err = reader.ReadUint64(); err != nil {
		return ValidatorState{}, fmt.Errorf("stake: decode missed proposal count: %w", err)
	}
	if state.JailUntilEpoch, err = reader.ReadUint64(); err != nil {
		return ValidatorState{}, fmt.Errorf("stake: decode jail until epoch: %w", err)
	}
	if reader.Remaining() == 0 {
		return state, state.Validate()
	}
	if state.ActivationEpoch, err = reader.ReadUint64(); err != nil {
		return ValidatorState{}, fmt.Errorf("stake: decode activation epoch: %w", err)
	}
	if state.DeactivationEpoch, err = reader.ReadUint64(); err != nil {
		return ValidatorState{}, fmt.Errorf("stake: decode deactivation epoch: %w", err)
	}
	if state.LastEffectiveStake, err = reader.ReadUint64(); err != nil {
		return ValidatorState{}, fmt.Errorf("stake: decode effective stake: %w", err)
	}
	if reader.Remaining() == 0 {
		return state, state.Validate()
	}
	if state.BLSPublicKey, err = reader.ReadBytes(); err != nil {
		return ValidatorState{}, fmt.Errorf("stake: decode validator bls public key: %w", err)
	}
	if reader.Remaining() == 0 {
		return state, state.Validate()
	}
	if state.LastSlashedSlot, err = reader.ReadUint64(); err != nil {
		return ValidatorState{}, fmt.Errorf("stake: decode last slashed slot: %w", err)
	}
	if reader.Remaining() == 0 {
		return state, state.Validate()
	}
	delegationCount, err := reader.ReadUint32()
	if err != nil {
		return ValidatorState{}, fmt.Errorf("stake: decode delegation count: %w", err)
	}
	if delegationCount > MaxDelegations {
		return ValidatorState{}, fmt.Errorf("stake: delegation count exceeds limit")
	}
	state.Delegations = make([]DelegationState, 0, delegationCount)
	for index := uint32(0); index < delegationCount; index++ {
		delegation, err := readDelegationState(reader)
		if err != nil {
			return ValidatorState{}, fmt.Errorf("stake: decode delegation %d: %w", index, err)
		}
		state.Delegations = append(state.Delegations, delegation)
	}
	if reader.Remaining() == 0 {
		return state, state.Validate()
	}
	if state.SelfRewardLamports, err = reader.ReadUint64(); err != nil {
		return ValidatorState{}, fmt.Errorf("stake: decode self reward lamports: %w", err)
	}
	if reader.Remaining() == 0 {
		return state, state.Validate()
	}
	if state.CommissionRewardLamports, err = reader.ReadUint64(); err != nil {
		return ValidatorState{}, fmt.Errorf("stake: decode commission reward lamports: %w", err)
	}
	if err := reader.EnsureEOF(); err != nil {
		return ValidatorState{}, fmt.Errorf("stake: decode validator eof: %w", err)
	}
	return state, state.Validate()
}

// Validate 校验验证者状态 + 防止非法状态进入 epoch snapshot。
func (state ValidatorState) Validate() error {
	return state.validate(true)
}

// validate 校验验证者核心字段 + 派生权重重算前允许旧快照短暂落后。
func (state ValidatorState) validate(checkLastEffectiveStake bool) error {
	if state.ConsensusPublicKey.IsZero() {
		return fmt.Errorf("stake: consensus public key is empty")
	}
	if len(state.BLSPublicKey) > MaxBLSPublicKeyBytes {
		return fmt.Errorf("stake: bls public key too long")
	}
	if state.StakerAccount.IsZero() {
		return fmt.Errorf("stake: staker account is empty")
	}
	if len(state.P2PPeerID) == 0 || len(state.P2PPeerID) > MaxPeerIDLength {
		return fmt.Errorf("stake: invalid p2p peer id")
	}
	if state.CommissionBps > 10000 {
		return fmt.Errorf("stake: commission exceeds 10000 bps")
	}
	if ^uint64(0)-state.ActiveStake < state.PendingStake {
		return fmt.Errorf("stake: stake overflow")
	}
	if ^uint64(0)-state.ActiveStake-state.PendingStake < state.UnlockingStake {
		return fmt.Errorf("stake: stake overflow")
	}
	if state.ActiveStake+state.PendingStake < MinimumStakeLamports && state.Status == ValidatorStatusActive {
		return fmt.Errorf("stake: active validator stake below minimum")
	}
	totalBondedStake := state.ActiveStake + state.PendingStake
	if ^uint64(0)-totalBondedStake < state.UnlockingStake {
		return fmt.Errorf("stake: stake overflow")
	}
	totalBondedStake += state.UnlockingStake
	if checkLastEffectiveStake && state.LastEffectiveStake > totalBondedStake {
		return fmt.Errorf("stake: effective stake exceeds bonded stake")
	}
	if err := validateDelegations(state.Delegations); err != nil {
		return err
	}
	delegatedStake, err := TotalDelegatedStake(state)
	if err != nil {
		return err
	}
	if delegatedStake > totalBondedStake {
		return fmt.Errorf("stake: delegated stake exceeds total stake")
	}
	rewardBreakdown, err := RewardBreakdownLamports(state)
	if err != nil {
		return err
	}
	if rewardBreakdown > state.RewardLamports {
		return fmt.Errorf("stake: reward breakdown exceeds total reward")
	}
	if state.Status != ValidatorStatusActive && state.Status != ValidatorStatusExiting && state.Status != ValidatorStatusJailed {
		return fmt.Errorf("stake: invalid validator status %d", state.Status)
	}
	return nil
}

// EffectiveStakeAtEpoch 计算 epoch 生效权重 + 避免 pending stake 在当前 epoch 立即影响共识。
func EffectiveStakeAtEpoch(state ValidatorState, epochID uint64) (uint64, error) {
	if err := state.validate(false); err != nil {
		return 0, err
	}
	if state.Status == ValidatorStatusJailed && state.JailUntilEpoch > epochID {
		return 0, nil
	}
	if state.Status == ValidatorStatusExiting && state.DeactivationEpoch <= epochID {
		return 0, nil
	}

	effectiveStake := state.ActiveStake
	if state.UnlockingStake > 0 && state.DeactivationEpoch > epochID {
		if ^uint64(0)-effectiveStake < state.UnlockingStake {
			return 0, fmt.Errorf("stake: effective stake overflow")
		}
		effectiveStake += state.UnlockingStake
	}
	if state.PendingStake > 0 && state.ActivationEpoch <= epochID {
		if ^uint64(0)-effectiveStake < state.PendingStake {
			return 0, fmt.Errorf("stake: effective stake overflow")
		}
		effectiveStake += state.PendingStake
	}
	if effectiveStake < MinimumStakeLamports {
		return 0, nil
	}
	return effectiveStake, nil
}

// MatureStakeForEpoch 迁移到期 pending stake + 让后续 unstake/slash 看到一致的 active bucket。
func MatureStakeForEpoch(state *ValidatorState, epochID uint64) error {
	if state == nil {
		return fmt.Errorf("stake: nil validator state")
	}
	if state.PendingStake > 0 && state.ActivationEpoch <= epochID {
		if ^uint64(0)-state.ActiveStake < state.PendingStake {
			return fmt.Errorf("stake: active stake overflow")
		}
		state.ActiveStake += state.PendingStake
		state.PendingStake = 0
		state.ActivationEpoch = 0
	}
	for index := range state.Delegations {
		delegation := &state.Delegations[index]
		if delegation.PendingStake == 0 || delegation.ActivationEpoch > epochID {
			continue
		}
		if ^uint64(0)-delegation.ActiveStake < delegation.PendingStake {
			return fmt.Errorf("stake: delegation active stake overflow")
		}
		delegation.ActiveStake += delegation.PendingStake
		delegation.PendingStake = 0
		delegation.ActivationEpoch = 0
	}
	effectiveStake, err := EffectiveStakeAtEpoch(*state, epochID)
	if err != nil {
		return err
	}
	state.LastEffectiveStake = effectiveStake
	if state.Status == ValidatorStatusJailed && state.JailUntilEpoch <= epochID && effectiveStake >= MinimumStakeLamports {
		state.Status = ValidatorStatusActive
	}
	return nil
}

func executeRegisterValidator(instruction Instruction, context runtime.InstructionContext) error {
	if len(context.Instruction.AccountIndexes) < 2 {
		return fmt.Errorf("stake: register requires staker and validator accounts")
	}
	stakerAddress := context.Message.AccountKeys[context.Instruction.AccountIndexes[0]]
	validatorAddress := context.Message.AccountKeys[context.Instruction.AccountIndexes[1]]
	if !runtime.IsSignerContextAddress(stakerAddress, context) {
		return fmt.Errorf("%w: staker must sign", structure.ErrMissingRequiredSignature)
	}
	if err := runtime.TransferLamports(stakerAddress, validatorAddress, instruction.Amount, context.Accounts, context.RentConfig); err != nil {
		return err
	}

	validatorAccount := context.Accounts[validatorAddress].Clone()
	if len(validatorAccount.Data) != 0 {
		return fmt.Errorf("stake: validator account already initialized")
	}
	state := ValidatorState{
		ConsensusPublicKey: instruction.ConsensusPublicKey,
		BLSPublicKey:       cloneBytes(instruction.BLSPublicKey),
		StakerAccount:      stakerAddress,
		P2PPeerID:          instruction.P2PPeerID,
		CommissionBps:      instruction.CommissionBps,
		PendingStake:       instruction.Amount,
		Status:             ValidatorStatusActive,
		ActivationEpoch:    context.CurrentEpoch + 1,
	}
	effectiveStake, err := EffectiveStakeAtEpoch(state, context.CurrentEpoch)
	if err != nil {
		return err
	}
	state.LastEffectiveStake = effectiveStake
	return writeValidatorState("register_validator", validatorAddress, validatorAccount, state, context)
}

func executeStake(instruction Instruction, context runtime.InstructionContext) error {
	stakerAddress, validatorAddress, state, _, err := loadWritableValidator(context)
	if err != nil {
		return err
	}
	if state.Status != ValidatorStatusActive {
		return fmt.Errorf("stake: validator is not active")
	}
	if err := MatureStakeForEpoch(&state, context.CurrentEpoch); err != nil {
		return err
	}
	if err := runtime.TransferLamports(stakerAddress, validatorAddress, instruction.Amount, context.Accounts, context.RentConfig); err != nil {
		return err
	}
	if ^uint64(0)-state.PendingStake < instruction.Amount {
		return fmt.Errorf("stake: pending stake overflow")
	}
	state.PendingStake += instruction.Amount
	state.ActivationEpoch = context.CurrentEpoch + 1
	effectiveStake, err := EffectiveStakeAtEpoch(state, context.CurrentEpoch)
	if err != nil {
		return err
	}
	state.LastEffectiveStake = effectiveStake
	validatorAccount := context.Accounts[validatorAddress].Clone()
	return writeValidatorState("stake", validatorAddress, validatorAccount, state, context)
}

func executeDelegate(instruction Instruction, context runtime.InstructionContext) error {
	delegatorAddress, validatorAddress, state, validatorAccount, err := loadValidatorForDelegator(context)
	if err != nil {
		return err
	}
	if state.Status != ValidatorStatusActive {
		return fmt.Errorf("stake: validator is not active")
	}
	if err := MatureStakeForEpoch(&state, context.CurrentEpoch); err != nil {
		return err
	}
	if err := runtime.TransferLamports(delegatorAddress, validatorAddress, instruction.Amount, context.Accounts, context.RentConfig); err != nil {
		return err
	}
	if ^uint64(0)-state.PendingStake < instruction.Amount {
		return fmt.Errorf("stake: pending stake overflow")
	}
	delegationIndex, exists := findDelegationIndex(state.Delegations, delegatorAddress)
	if !exists {
		if len(state.Delegations) >= MaxDelegations {
			return fmt.Errorf("stake: too many delegations")
		}
		state.Delegations = append(state.Delegations, DelegationState{DelegatorAccount: delegatorAddress})
		delegationIndex = len(state.Delegations) - 1
	}
	delegation := &state.Delegations[delegationIndex]
	if ^uint64(0)-delegation.PendingStake < instruction.Amount {
		return fmt.Errorf("stake: delegation pending stake overflow")
	}
	delegation.PendingStake += instruction.Amount
	delegation.ActivationEpoch = context.CurrentEpoch + 1
	state.PendingStake += instruction.Amount
	state.ActivationEpoch = context.CurrentEpoch + 1
	effectiveStake, err := EffectiveStakeAtEpoch(state, context.CurrentEpoch)
	if err != nil {
		return err
	}
	state.LastEffectiveStake = effectiveStake
	return writeValidatorState("delegate", validatorAddress, validatorAccount, state, context)
}

func executeUnstake(instruction Instruction, context runtime.InstructionContext) error {
	_, validatorAddress, state, validatorAccount, err := loadWritableValidator(context)
	if err != nil {
		return err
	}
	if err := MatureStakeForEpoch(&state, context.CurrentEpoch); err != nil {
		return err
	}
	selfActiveStake, err := SelfActiveStake(state)
	if err != nil {
		return err
	}
	if instruction.Amount > selfActiveStake {
		return fmt.Errorf("stake: unstake exceeds active stake")
	}
	state.ActiveStake -= instruction.Amount
	state.UnlockingStake += instruction.Amount
	state.UnlockEpoch = instruction.UnlockEpoch
	state.DeactivationEpoch = context.CurrentEpoch + 1
	effectiveStake, err := EffectiveStakeAtEpoch(state, context.CurrentEpoch)
	if err != nil {
		return err
	}
	state.LastEffectiveStake = effectiveStake
	return writeValidatorState("unstake", validatorAddress, validatorAccount, state, context)
}

func executeUndelegate(instruction Instruction, context runtime.InstructionContext) error {
	delegatorAddress, validatorAddress, state, validatorAccount, err := loadValidatorForDelegator(context)
	if err != nil {
		return err
	}
	if err := MatureStakeForEpoch(&state, context.CurrentEpoch); err != nil {
		return err
	}
	delegationIndex, exists := findDelegationIndex(state.Delegations, delegatorAddress)
	if !exists {
		return fmt.Errorf("stake: delegation not found")
	}
	delegation := &state.Delegations[delegationIndex]
	if instruction.Amount > delegation.ActiveStake {
		return fmt.Errorf("stake: undelegate exceeds active delegation")
	}
	if instruction.Amount > state.ActiveStake {
		return fmt.Errorf("stake: undelegate exceeds validator active stake")
	}
	delegation.ActiveStake -= instruction.Amount
	delegation.UnlockingStake += instruction.Amount
	delegation.UnlockEpoch = instruction.UnlockEpoch
	delegation.DeactivationEpoch = context.CurrentEpoch + 1
	state.ActiveStake -= instruction.Amount
	state.UnlockingStake += instruction.Amount
	state.DeactivationEpoch = context.CurrentEpoch + 1
	effectiveStake, err := EffectiveStakeAtEpoch(state, context.CurrentEpoch)
	if err != nil {
		return err
	}
	state.LastEffectiveStake = effectiveStake
	state.Delegations = compactDelegations(state.Delegations)
	return writeValidatorState("undelegate", validatorAddress, validatorAccount, state, context)
}

func executeWithdrawUnstaked(instruction Instruction, context runtime.InstructionContext) error {
	stakerAddress, validatorAddress, state, _, err := loadWritableValidator(context)
	if err != nil {
		return err
	}
	if err := MatureStakeForEpoch(&state, context.CurrentEpoch); err != nil {
		return err
	}
	if state.UnlockingStake == 0 || instruction.UnlockEpoch < state.UnlockEpoch {
		return fmt.Errorf("stake: unlocking stake is not withdrawable")
	}
	amount := state.UnlockingStake
	state.UnlockingStake = 0
	if err := runtime.TransferLamports(validatorAddress, stakerAddress, amount, context.Accounts, context.RentConfig); err != nil {
		return err
	}
	validatorAccount := context.Accounts[validatorAddress].Clone()
	return writeValidatorState("withdraw_unstaked", validatorAddress, validatorAccount, state, context)
}

func executeWithdrawDelegation(instruction Instruction, context runtime.InstructionContext) error {
	delegatorAddress, validatorAddress, state, validatorAccount, err := loadValidatorForDelegator(context)
	if err != nil {
		return err
	}
	if err := MatureStakeForEpoch(&state, context.CurrentEpoch); err != nil {
		return err
	}
	delegationIndex, exists := findDelegationIndex(state.Delegations, delegatorAddress)
	if !exists {
		return fmt.Errorf("stake: delegation not found")
	}
	delegation := &state.Delegations[delegationIndex]
	if delegation.UnlockingStake == 0 || instruction.UnlockEpoch < delegation.UnlockEpoch {
		return fmt.Errorf("stake: delegation is not withdrawable")
	}
	amount := delegation.UnlockingStake
	delegation.UnlockingStake = 0
	if amount > state.UnlockingStake {
		return fmt.Errorf("stake: delegation unlocking exceeds validator unlocking stake")
	}
	state.UnlockingStake -= amount
	if err := runtime.TransferLamports(validatorAddress, delegatorAddress, amount, context.Accounts, context.RentConfig); err != nil {
		return err
	}
	state.Delegations = compactDelegations(state.Delegations)
	validatorAccount = context.Accounts[validatorAddress].Clone()
	return writeValidatorState("withdraw_delegation", validatorAddress, validatorAccount, state, context)
}

func executeUpdateCommission(instruction Instruction, context runtime.InstructionContext) error {
	stakerAddress, validatorAddress, state, validatorAccount, err := loadWritableValidator(context)
	if err != nil {
		return err
	}
	if stakerAddress != state.StakerAccount {
		return fmt.Errorf("stake: only staker can update commission")
	}
	state.CommissionBps = instruction.CommissionBps
	return writeValidatorState("update_commission", validatorAddress, validatorAccount, state, context)
}

func executeExitValidator(context runtime.InstructionContext) error {
	_, validatorAddress, state, validatorAccount, err := loadWritableValidator(context)
	if err != nil {
		return err
	}
	state.Status = ValidatorStatusExiting
	state.DeactivationEpoch = context.CurrentEpoch + 1
	effectiveStake, err := EffectiveStakeAtEpoch(state, context.CurrentEpoch)
	if err != nil {
		return err
	}
	state.LastEffectiveStake = effectiveStake
	return writeValidatorState("exit_validator", validatorAddress, validatorAccount, state, context)
}

func executeSlashValidator(instruction Instruction, context runtime.InstructionContext) error {
	_, validatorAddress, state, validatorAccount, err := loadWritableValidator(context)
	if err != nil {
		return err
	}
	if err := MatureStakeForEpoch(&state, context.CurrentEpoch); err != nil {
		return err
	}
	totalStake, err := validatorTotalStake(state)
	if err != nil {
		return err
	}
	if instruction.Amount > totalStake {
		return fmt.Errorf("stake: slash exceeds total stake")
	}
	if err := validatorAccount.DebitLamports(instruction.Amount, context.RentConfig); err != nil {
		return fmt.Errorf("stake: burn slashed lamports: %w", err)
	}
	state, err = ApplySlash(state, instruction.Amount)
	if err != nil {
		return err
	}
	if state.ActiveStake+state.PendingStake < MinimumStakeLamports {
		state.Status = ValidatorStatusJailed
		state.JailUntilEpoch = state.UnlockEpoch
	}
	effectiveStake, err := EffectiveStakeAtEpoch(state, context.CurrentEpoch)
	if err != nil {
		return err
	}
	state.LastEffectiveStake = effectiveStake
	return writeValidatorState("slash_validator", validatorAddress, validatorAccount, state, context)
}

func executeJailValidator(instruction Instruction, context runtime.InstructionContext) error {
	_, validatorAddress, state, validatorAccount, err := loadWritableValidator(context)
	if err != nil {
		return err
	}
	state.Status = ValidatorStatusJailed
	state.UnlockEpoch = instruction.UnlockEpoch
	state.JailUntilEpoch = instruction.UnlockEpoch
	effectiveStake, err := EffectiveStakeAtEpoch(state, context.CurrentEpoch)
	if err != nil {
		return err
	}
	state.LastEffectiveStake = effectiveStake
	return writeValidatorState("jail_validator", validatorAddress, validatorAccount, state, context)
}

func subtractStakeBucket(value uint64, remaining uint64) (uint64, uint64) {
	if remaining == 0 {
		return value, 0
	}
	if value >= remaining {
		return value - remaining, 0
	}
	return 0, remaining - value
}

func validatorTotalStake(state ValidatorState) (uint64, error) {
	total := state.ActiveStake
	if ^uint64(0)-total < state.PendingStake {
		return 0, fmt.Errorf("stake: total stake overflow")
	}
	total += state.PendingStake
	if ^uint64(0)-total < state.UnlockingStake {
		return 0, fmt.Errorf("stake: total stake overflow")
	}
	return total + state.UnlockingStake, nil
}

func loadWritableValidator(context runtime.InstructionContext) (structure.PublicKey, structure.PublicKey, ValidatorState, structure.Account, error) {
	if len(context.Instruction.AccountIndexes) < 2 {
		return structure.PublicKey{}, structure.PublicKey{}, ValidatorState{}, structure.Account{}, fmt.Errorf("stake: instruction requires staker and validator accounts")
	}
	stakerAddress := context.Message.AccountKeys[context.Instruction.AccountIndexes[0]]
	validatorAddress := context.Message.AccountKeys[context.Instruction.AccountIndexes[1]]
	if !runtime.IsSignerContextAddress(stakerAddress, context) {
		return structure.PublicKey{}, structure.PublicKey{}, ValidatorState{}, structure.Account{}, fmt.Errorf("%w: staker must sign", structure.ErrMissingRequiredSignature)
	}
	validatorAccount, exists := context.Accounts[validatorAddress]
	if !exists {
		return structure.PublicKey{}, structure.PublicKey{}, ValidatorState{}, structure.Account{}, fmt.Errorf("stake: validator account not found")
	}
	state, err := UnmarshalValidatorStateBinary(validatorAccount.Data)
	if err != nil {
		return structure.PublicKey{}, structure.PublicKey{}, ValidatorState{}, structure.Account{}, err
	}
	if state.StakerAccount != stakerAddress {
		return structure.PublicKey{}, structure.PublicKey{}, ValidatorState{}, structure.Account{}, fmt.Errorf("stake: staker mismatch")
	}
	return stakerAddress, validatorAddress, state, validatorAccount.Clone(), nil
}

func loadValidatorForDelegator(context runtime.InstructionContext) (structure.PublicKey, structure.PublicKey, ValidatorState, structure.Account, error) {
	if len(context.Instruction.AccountIndexes) < 2 {
		return structure.PublicKey{}, structure.PublicKey{}, ValidatorState{}, structure.Account{}, fmt.Errorf("stake: delegate requires delegator and validator accounts")
	}
	delegatorAddress := context.Message.AccountKeys[context.Instruction.AccountIndexes[0]]
	validatorAddress := context.Message.AccountKeys[context.Instruction.AccountIndexes[1]]
	if !runtime.IsSignerContextAddress(delegatorAddress, context) {
		return structure.PublicKey{}, structure.PublicKey{}, ValidatorState{}, structure.Account{}, fmt.Errorf("%w: delegator must sign", structure.ErrMissingRequiredSignature)
	}
	validatorAccount, exists := context.Accounts[validatorAddress]
	if !exists {
		return structure.PublicKey{}, structure.PublicKey{}, ValidatorState{}, structure.Account{}, fmt.Errorf("stake: validator account not found")
	}
	state, err := UnmarshalValidatorStateBinary(validatorAccount.Data)
	if err != nil {
		return structure.PublicKey{}, structure.PublicKey{}, ValidatorState{}, structure.Account{}, err
	}
	return delegatorAddress, validatorAddress, state, validatorAccount.Clone(), nil
}

func writeValidatorState(action string, address structure.PublicKey, account structure.Account, state ValidatorState, context runtime.InstructionContext) error {
	data, err := state.MarshalBinary()
	if err != nil {
		return err
	}
	account.Owner = context.BuiltinPrograms.Stake
	if err := account.SetData(data, context.RentConfig); err != nil {
		return err
	}
	context.Accounts[address] = account
	logValidatorStateWrite(action, address, state, context)
	return nil
}

func logValidatorStateWrite(action string, address structure.PublicKey, state ValidatorState, context runtime.InstructionContext) {
	logger := utils.EnsureLogger(context.Logger)
	logger.LogAttrs(stdcontext.Background(), slog.LevelInfo, "stake validator state written",
		slog.String("action", action),
		slog.Uint64("slot", context.CurrentSlot),
		slog.Int("instruction_index", context.InstructionIndex),
		slog.String("validator_account", address.String()),
		slog.String("validator_id", ValidatorIDFromConsensusKey(state.ConsensusPublicKey)),
		slog.String("consensus_public_key", state.ConsensusPublicKey.String()),
		slog.Int("bls_public_key_bytes", len(state.BLSPublicKey)),
		slog.String("staker", state.StakerAccount.String()),
		slog.String("p2p_peer_id", state.P2PPeerID),
		slog.Int("status", int(state.Status)),
		slog.Uint64("active_stake", state.ActiveStake),
		slog.Uint64("pending_stake", state.PendingStake),
		slog.Uint64("unlocking_stake", state.UnlockingStake),
		slog.Uint64("unlock_epoch", state.UnlockEpoch),
		slog.Uint64("jail_until_epoch", state.JailUntilEpoch),
		slog.Uint64("activation_epoch", state.ActivationEpoch),
		slog.Uint64("deactivation_epoch", state.DeactivationEpoch),
		slog.Uint64("effective_stake", state.LastEffectiveStake),
		slog.Uint64("vote_credits", state.VoteCredits),
		slog.Uint64("self_reward_lamports", state.SelfRewardLamports),
		slog.Uint64("commission_reward_lamports", state.CommissionRewardLamports),
		slog.Uint64("missed_vote_count", state.MissedVoteCount),
		slog.Uint64("missed_proposal_count", state.MissedProposalCount),
		slog.Uint64("last_slashed_slot", state.LastSlashedSlot),
		slog.Int("commission_bps", int(state.CommissionBps)),
	)
}

func validateRegisterInstruction(instruction Instruction) error {
	if instruction.ConsensusPublicKey.IsZero() {
		return fmt.Errorf("stake: consensus public key is empty")
	}
	if len(instruction.P2PPeerID) == 0 {
		return fmt.Errorf("stake: p2p peer id is empty")
	}
	return validateStakeAmount(instruction.Amount)
}

func validateStakeAmount(amount uint64) error {
	if amount < MinimumStakeLamports {
		return fmt.Errorf("stake: amount below minimum stake")
	}
	return nil
}

func readPublicKey(reader *borsh.Reader, field string) (structure.PublicKey, error) {
	value, err := reader.ReadFixedBytes(structure.PublicKeySize)
	if err != nil {
		return structure.PublicKey{}, fmt.Errorf("stake: decode %s: %w", field, err)
	}
	publicKey, err := structure.NewPublicKey(value)
	if err != nil {
		return structure.PublicKey{}, fmt.Errorf("stake: decode %s: %w", field, err)
	}
	return publicKey, nil
}

func readDelegationState(reader *borsh.Reader) (DelegationState, error) {
	delegatorAccount, err := readPublicKey(reader, "delegator account")
	if err != nil {
		return DelegationState{}, err
	}
	activeStake, err := reader.ReadUint64()
	if err != nil {
		return DelegationState{}, fmt.Errorf("stake: decode delegation active stake: %w", err)
	}
	pendingStake, err := reader.ReadUint64()
	if err != nil {
		return DelegationState{}, fmt.Errorf("stake: decode delegation pending stake: %w", err)
	}
	unlockingStake, err := reader.ReadUint64()
	if err != nil {
		return DelegationState{}, fmt.Errorf("stake: decode delegation unlocking stake: %w", err)
	}
	unlockEpoch, err := reader.ReadUint64()
	if err != nil {
		return DelegationState{}, fmt.Errorf("stake: decode delegation unlock epoch: %w", err)
	}
	activationEpoch, err := reader.ReadUint64()
	if err != nil {
		return DelegationState{}, fmt.Errorf("stake: decode delegation activation epoch: %w", err)
	}
	deactivationEpoch, err := reader.ReadUint64()
	if err != nil {
		return DelegationState{}, fmt.Errorf("stake: decode delegation deactivation epoch: %w", err)
	}
	rewardLamports, err := reader.ReadUint64()
	if err != nil {
		return DelegationState{}, fmt.Errorf("stake: decode delegation reward lamports: %w", err)
	}
	return DelegationState{
		DelegatorAccount:  delegatorAccount,
		ActiveStake:       activeStake,
		PendingStake:      pendingStake,
		UnlockingStake:    unlockingStake,
		UnlockEpoch:       unlockEpoch,
		ActivationEpoch:   activationEpoch,
		DeactivationEpoch: deactivationEpoch,
		RewardLamports:    rewardLamports,
	}, nil
}

func validateDelegations(delegations []DelegationState) error {
	if len(delegations) > MaxDelegations {
		return fmt.Errorf("stake: too many delegations")
	}
	seen := make(map[structure.PublicKey]struct{}, len(delegations))
	for _, delegation := range delegations {
		if delegation.DelegatorAccount.IsZero() {
			return fmt.Errorf("stake: delegation account is empty")
		}
		if _, exists := seen[delegation.DelegatorAccount]; exists {
			return fmt.Errorf("stake: duplicate delegation")
		}
		if _, err := delegation.TotalStake(); err != nil {
			return err
		}
		seen[delegation.DelegatorAccount] = struct{}{}
	}
	return nil
}

func findDelegationIndex(delegations []DelegationState, delegator structure.PublicKey) (int, bool) {
	for index, delegation := range delegations {
		if delegation.DelegatorAccount == delegator {
			return index, true
		}
	}
	return 0, false
}

func compactDelegations(delegations []DelegationState) []DelegationState {
	compacted := delegations[:0]
	for _, delegation := range delegations {
		total, err := delegation.TotalStake()
		if err != nil || total == 0 {
			continue
		}
		compacted = append(compacted, delegation)
	}
	return compacted
}

// SelfActiveStake 返回验证者自有 active stake + 防止验证者提走普通用户委托资金。
func SelfActiveStake(state ValidatorState) (uint64, error) {
	return selfStakeBucket(state.ActiveStake, state.Delegations, func(delegation DelegationState) uint64 {
		return delegation.ActiveStake
	}, "active")
}

// SelfPendingStake 返回验证者自有 pending stake + 避免把委托人的待生效资金展示为验证者自质押。
func SelfPendingStake(state ValidatorState) (uint64, error) {
	return selfStakeBucket(state.PendingStake, state.Delegations, func(delegation DelegationState) uint64 {
		return delegation.PendingStake
	}, "pending")
}

// SelfUnlockingStake 返回验证者自有 unlocking stake + 避免把委托人的解锁中资金展示为验证者自质押。
func SelfUnlockingStake(state ValidatorState) (uint64, error) {
	return selfStakeBucket(state.UnlockingStake, state.Delegations, func(delegation DelegationState) uint64 {
		return delegation.UnlockingStake
	}, "unlocking")
}

func selfStakeBucket(
	total uint64,
	delegations []DelegationState,
	value func(DelegationState) uint64,
	name string,
) (uint64, error) {
	delegatedValue := uint64(0)
	for _, delegation := range delegations {
		delegationValue := value(delegation)
		if ^uint64(0)-delegatedValue < delegationValue {
			return 0, fmt.Errorf("stake: delegated %s overflow", name)
		}
		delegatedValue += delegationValue
	}
	if delegatedValue > total {
		return 0, fmt.Errorf("stake: delegated %s exceeds validator %s stake", name, name)
	}
	return total - delegatedValue, nil
}

// TotalDelegatedStake 返回总委托质押 + 奖励分配和校验共用同一口径。
func TotalDelegatedStake(state ValidatorState) (uint64, error) {
	total := uint64(0)
	for _, delegation := range state.Delegations {
		delegationTotal, err := delegation.TotalStake()
		if err != nil {
			return 0, err
		}
		if ^uint64(0)-total < delegationTotal {
			return 0, fmt.Errorf("stake: delegated stake overflow")
		}
		total += delegationTotal
	}
	return total, nil
}

// TotalDelegationRewards 汇总委托收益 + 让 RPC 和状态校验使用同一套溢出规则。
func TotalDelegationRewards(delegations []DelegationState) (uint64, error) {
	total := uint64(0)
	for _, delegation := range delegations {
		if ^uint64(0)-total < delegation.RewardLamports {
			return 0, fmt.Errorf("stake: delegation reward overflow")
		}
		total += delegation.RewardLamports
	}
	return total, nil
}

// RewardBreakdownLamports 汇总独立收益字段 + 防止自质押、佣金、委托收益超过总收益。
func RewardBreakdownLamports(state ValidatorState) (uint64, error) {
	total := state.SelfRewardLamports
	if ^uint64(0)-total < state.CommissionRewardLamports {
		return 0, fmt.Errorf("stake: reward breakdown overflow")
	}
	total += state.CommissionRewardLamports
	delegationRewards, err := TotalDelegationRewards(state.Delegations)
	if err != nil {
		return 0, err
	}
	if ^uint64(0)-total < delegationRewards {
		return 0, fmt.Errorf("stake: reward breakdown overflow")
	}
	return total + delegationRewards, nil
}

// TotalStake 返回单个委托总额 + 包含 active、pending 和 unlocking。
func (delegation DelegationState) TotalStake() (uint64, error) {
	total := delegation.ActiveStake
	if ^uint64(0)-total < delegation.PendingStake {
		return 0, fmt.Errorf("stake: delegation stake overflow")
	}
	total += delegation.PendingStake
	if ^uint64(0)-total < delegation.UnlockingStake {
		return 0, fmt.Errorf("stake: delegation stake overflow")
	}
	return total + delegation.UnlockingStake, nil
}

type stakeSlashBucket uint8

const (
	stakeSlashBucketPending stakeSlashBucket = iota + 1
	stakeSlashBucketActive
	stakeSlashBucketUnlocking
)

// ApplySlash 扣减验证者和委托质押 + 总 bucket 与委托明细必须同步变化以保持可验证状态。
func ApplySlash(state ValidatorState, amount uint64) (ValidatorState, error) {
	totalStake, err := validatorTotalStake(state)
	if err != nil {
		return ValidatorState{}, err
	}
	if amount > totalStake {
		return ValidatorState{}, fmt.Errorf("stake: slash exceeds total stake")
	}
	remainingSlash := amount
	remainingSlash = slashStakeBucket(&state, stakeSlashBucketPending, remainingSlash)
	remainingSlash = slashStakeBucket(&state, stakeSlashBucketActive, remainingSlash)
	remainingSlash = slashStakeBucket(&state, stakeSlashBucketUnlocking, remainingSlash)
	if remainingSlash != 0 {
		return ValidatorState{}, fmt.Errorf("stake: slash remainder is non-zero")
	}
	state.Delegations = compactDelegations(state.Delegations)
	state, _, err = NormalizeDelegationBuckets(state)
	if err != nil {
		return ValidatorState{}, err
	}
	nextTotalStake, err := validatorTotalStake(state)
	if err != nil {
		return ValidatorState{}, err
	}
	if state.LastEffectiveStake > nextTotalStake {
		state.LastEffectiveStake = nextTotalStake
	}
	return state, nil
}

// NormalizeDelegationBuckets 修复委托 bucket 上限 + 历史状态迁移和罚没后都必须满足派生自质押不为负。
func NormalizeDelegationBuckets(state ValidatorState) (ValidatorState, bool, error) {
	if err := validateDelegations(state.Delegations); err != nil {
		return ValidatorState{}, false, err
	}
	changed := false
	if trimExcessDelegationBucket(&state, stakeSlashBucketPending) {
		changed = true
	}
	if trimExcessDelegationBucket(&state, stakeSlashBucketActive) {
		changed = true
	}
	if trimExcessDelegationBucket(&state, stakeSlashBucketUnlocking) {
		changed = true
	}
	if changed {
		state.Delegations = compactDelegations(state.Delegations)
	}
	totalStake, err := validatorTotalStake(state)
	if err != nil {
		return ValidatorState{}, false, err
	}
	if state.LastEffectiveStake > totalStake {
		state.LastEffectiveStake = totalStake
		changed = true
	}
	return state, changed, nil
}

func slashStakeBucket(state *ValidatorState, bucket stakeSlashBucket, remainingSlash uint64) uint64 {
	if remainingSlash == 0 {
		return 0
	}
	for index := range state.Delegations {
		validatorAvailable := validatorBucketValue(*state, bucket)
		delegationValue := delegationBucketValue(state.Delegations[index], bucket)
		if validatorAvailable == 0 || delegationValue == 0 {
			continue
		}
		slashLamports := minUint64Stake(remainingSlash, minUint64Stake(delegationValue, validatorAvailable))
		setDelegationBucketValue(&state.Delegations[index], bucket, delegationValue-slashLamports)
		subtractValidatorBucketValue(state, bucket, slashLamports)
		remainingSlash -= slashLamports
		if remainingSlash == 0 {
			return 0
		}
	}
	validatorAvailable := validatorBucketValue(*state, bucket)
	slashLamports := minUint64Stake(remainingSlash, validatorAvailable)
	subtractValidatorBucketValue(state, bucket, slashLamports)
	return remainingSlash - slashLamports
}

func trimExcessDelegationBucket(state *ValidatorState, bucket stakeSlashBucket) bool {
	delegatedValue := delegatedBucketValue(state.Delegations, bucket)
	validatorValue := validatorBucketValue(*state, bucket)
	if delegatedValue <= validatorValue {
		return false
	}
	excessValue := delegatedValue - validatorValue
	for index := range state.Delegations {
		if excessValue == 0 {
			return true
		}
		currentValue := delegationBucketValue(state.Delegations[index], bucket)
		trimmedValue := minUint64Stake(currentValue, excessValue)
		setDelegationBucketValue(&state.Delegations[index], bucket, currentValue-trimmedValue)
		excessValue -= trimmedValue
	}
	return true
}

func delegatedBucketValue(delegations []DelegationState, bucket stakeSlashBucket) uint64 {
	total := uint64(0)
	for _, delegation := range delegations {
		value := delegationBucketValue(delegation, bucket)
		if ^uint64(0)-total < value {
			return ^uint64(0)
		}
		total += value
	}
	return total
}

func validatorBucketValue(state ValidatorState, bucket stakeSlashBucket) uint64 {
	switch bucket {
	case stakeSlashBucketPending:
		return state.PendingStake
	case stakeSlashBucketActive:
		return state.ActiveStake
	case stakeSlashBucketUnlocking:
		return state.UnlockingStake
	default:
		return 0
	}
}

func delegationBucketValue(delegation DelegationState, bucket stakeSlashBucket) uint64 {
	switch bucket {
	case stakeSlashBucketPending:
		return delegation.PendingStake
	case stakeSlashBucketActive:
		return delegation.ActiveStake
	case stakeSlashBucketUnlocking:
		return delegation.UnlockingStake
	default:
		return 0
	}
}

func subtractValidatorBucketValue(state *ValidatorState, bucket stakeSlashBucket, lamports uint64) {
	switch bucket {
	case stakeSlashBucketPending:
		state.PendingStake -= lamports
	case stakeSlashBucketActive:
		state.ActiveStake -= lamports
	case stakeSlashBucketUnlocking:
		state.UnlockingStake -= lamports
	}
}

func setDelegationBucketValue(delegation *DelegationState, bucket stakeSlashBucket, lamports uint64) {
	switch bucket {
	case stakeSlashBucketPending:
		delegation.PendingStake = lamports
	case stakeSlashBucketActive:
		delegation.ActiveStake = lamports
	case stakeSlashBucketUnlocking:
		delegation.UnlockingStake = lamports
	}
}

func minUint64Stake(left uint64, right uint64) uint64 {
	if left < right {
		return left
	}
	return right
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}

// ValidatorIDFromConsensusKey 计算验证者 ID + 供 cmd 和测试不 import consensus 时使用。
func ValidatorIDFromConsensusKey(publicKey structure.PublicKey) string {
	hash := utils.SHA256(publicKey[:])
	return utils.BytesToHex(hash[:16])
}
