package stake

import (
	"fmt"

	"solana_golang/codec/borsh"
	"solana_golang/runtime"
	"solana_golang/structure"
	"solana_golang/utils"
)

const (
	MaxPeerIDLength      = 128
	MaxStakeStateBytes   = 512
	MinimumStakeLamports = uint64(10_000_000)
)

type InstructionType uint32

const (
	InstructionRegisterValidator InstructionType = iota
	InstructionStake
	InstructionUnstake
	InstructionWithdrawUnstaked
	InstructionExitValidator
)

type ValidatorStatus uint8

const (
	ValidatorStatusActive ValidatorStatus = iota + 1
	ValidatorStatusExiting
)

// Program 执行质押固定指令 + 通过账户数据生成下个 epoch 的验证者快照。
type Program struct {
	programID structure.PublicKey
}

// Instruction 描述质押指令 + 使用固定二进制格式避免 JSON 注入和字段歧义。
type Instruction struct {
	Type               InstructionType
	ConsensusPublicKey structure.PublicKey
	P2PPeerID          string
	CommissionBps      uint16
	Amount             uint64
	UnlockEpoch        uint64
}

// ValidatorState 描述质押账户数据 + 由共识组合层转换为 consensus.ValidatorState。
type ValidatorState struct {
	ConsensusPublicKey structure.PublicKey
	StakerAccount      structure.PublicKey
	P2PPeerID          string
	CommissionBps      uint16
	ActiveStake        uint64
	PendingStake       uint64
	UnlockingStake     uint64
	UnlockEpoch        uint64
	Status             ValidatorStatus
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
	default:
		return fmt.Errorf("stake: unsupported instruction type %d", instruction.Type)
	}
}

// NewRegisterValidatorInstruction 创建注册指令 + 强制初始质押满足最低要求。
func NewRegisterValidatorInstruction(consensusPublicKey structure.PublicKey, p2pPeerID string, commissionBps uint16, amount uint64) (Instruction, error) {
	instruction := Instruction{
		Type:               InstructionRegisterValidator,
		ConsensusPublicKey: consensusPublicKey,
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
func (instruction Instruction) Validate() error {
	if instruction.P2PPeerID != "" && len(instruction.P2PPeerID) > MaxPeerIDLength {
		return fmt.Errorf("stake: p2p peer id too long")
	}
	if instruction.CommissionBps > 10000 {
		return fmt.Errorf("stake: commission exceeds 10000 bps")
	}
	switch instruction.Type {
	case InstructionRegisterValidator:
		return validateRegisterInstruction(instruction)
	case InstructionStake, InstructionUnstake:
		return validateStakeAmount(instruction.Amount)
	case InstructionWithdrawUnstaked, InstructionExitValidator:
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
	if err := reader.EnsureEOF(); err != nil {
		return Instruction{}, fmt.Errorf("stake: decode instruction eof: %w", err)
	}
	instruction := Instruction{
		Type:               InstructionType(instructionType),
		ConsensusPublicKey: consensusPublicKey,
		P2PPeerID:          p2pPeerID,
		CommissionBps:      commissionBps,
		Amount:             amount,
		UnlockEpoch:        unlockEpoch,
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
	if err := reader.EnsureEOF(); err != nil {
		return ValidatorState{}, fmt.Errorf("stake: decode validator eof: %w", err)
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
	return state, state.Validate()
}

// Validate 校验验证者状态 + 防止非法状态进入 epoch snapshot。
func (state ValidatorState) Validate() error {
	if state.ConsensusPublicKey.IsZero() {
		return fmt.Errorf("stake: consensus public key is empty")
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
	if state.ActiveStake+state.PendingStake < MinimumStakeLamports && state.Status == ValidatorStatusActive {
		return fmt.Errorf("stake: active validator stake below minimum")
	}
	if state.Status != ValidatorStatusActive && state.Status != ValidatorStatusExiting {
		return fmt.Errorf("stake: invalid validator status %d", state.Status)
	}
	return nil
}

func executeRegisterValidator(instruction Instruction, context runtime.InstructionContext) error {
	if len(context.Instruction.AccountIndexes) < 2 {
		return fmt.Errorf("stake: register requires staker and validator accounts")
	}
	stakerAddress := context.Message.AccountKeys[context.Instruction.AccountIndexes[0]]
	validatorAddress := context.Message.AccountKeys[context.Instruction.AccountIndexes[1]]
	if !runtime.IsSignerAddress(stakerAddress, context.Message) {
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
		StakerAccount:      stakerAddress,
		P2PPeerID:          instruction.P2PPeerID,
		CommissionBps:      instruction.CommissionBps,
		ActiveStake:        instruction.Amount,
		Status:             ValidatorStatusActive,
	}
	return writeValidatorState(validatorAddress, validatorAccount, state, context)
}

func executeStake(instruction Instruction, context runtime.InstructionContext) error {
	stakerAddress, validatorAddress, state, _, err := loadWritableValidator(context)
	if err != nil {
		return err
	}
	if state.Status != ValidatorStatusActive {
		return fmt.Errorf("stake: validator is not active")
	}
	if err := runtime.TransferLamports(stakerAddress, validatorAddress, instruction.Amount, context.Accounts, context.RentConfig); err != nil {
		return err
	}
	if ^uint64(0)-state.PendingStake < instruction.Amount {
		return fmt.Errorf("stake: pending stake overflow")
	}
	state.PendingStake += instruction.Amount
	validatorAccount := context.Accounts[validatorAddress].Clone()
	return writeValidatorState(validatorAddress, validatorAccount, state, context)
}

func executeUnstake(instruction Instruction, context runtime.InstructionContext) error {
	_, validatorAddress, state, validatorAccount, err := loadWritableValidator(context)
	if err != nil {
		return err
	}
	if instruction.Amount > state.ActiveStake {
		return fmt.Errorf("stake: unstake exceeds active stake")
	}
	state.ActiveStake -= instruction.Amount
	state.UnlockingStake += instruction.Amount
	state.UnlockEpoch = instruction.UnlockEpoch
	return writeValidatorState(validatorAddress, validatorAccount, state, context)
}

func executeWithdrawUnstaked(instruction Instruction, context runtime.InstructionContext) error {
	stakerAddress, validatorAddress, state, _, err := loadWritableValidator(context)
	if err != nil {
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
	return writeValidatorState(validatorAddress, validatorAccount, state, context)
}

func executeExitValidator(context runtime.InstructionContext) error {
	_, validatorAddress, state, validatorAccount, err := loadWritableValidator(context)
	if err != nil {
		return err
	}
	state.Status = ValidatorStatusExiting
	return writeValidatorState(validatorAddress, validatorAccount, state, context)
}

func loadWritableValidator(context runtime.InstructionContext) (structure.PublicKey, structure.PublicKey, ValidatorState, structure.Account, error) {
	if len(context.Instruction.AccountIndexes) < 2 {
		return structure.PublicKey{}, structure.PublicKey{}, ValidatorState{}, structure.Account{}, fmt.Errorf("stake: instruction requires staker and validator accounts")
	}
	stakerAddress := context.Message.AccountKeys[context.Instruction.AccountIndexes[0]]
	validatorAddress := context.Message.AccountKeys[context.Instruction.AccountIndexes[1]]
	if !runtime.IsSignerAddress(stakerAddress, context.Message) {
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

func writeValidatorState(address structure.PublicKey, account structure.Account, state ValidatorState, context runtime.InstructionContext) error {
	data, err := state.MarshalBinary()
	if err != nil {
		return err
	}
	account.Owner = context.BuiltinPrograms.Stake
	if err := account.SetData(data, context.RentConfig); err != nil {
		return err
	}
	context.Accounts[address] = account
	return nil
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

// ValidatorIDFromConsensusKey 计算验证者 ID + 供 cmd 和测试不 import consensus 时使用。
func ValidatorIDFromConsensusKey(publicKey structure.PublicKey) string {
	hash := utils.SHA256(publicKey[:])
	return utils.BytesToHex(hash[:16])
}
