package vmprogram

import (
	"fmt"
	"math/bits"

	"solana_golang/codec/borsh"
	stakeprogram "solana_golang/programs/stake"
	"solana_golang/runtime"
	"solana_golang/structure"
	"solana_golang/utils"
	svm "solana_golang/vm"
)

const (
	stakePoolExecuteSyscallCost  = uint64(8_000)
	stakePoolStateVersion        = uint16(1)
	stakePoolReceiptVersion      = uint16(1)
	maxStakePoolInstructionBytes = 512
	maxStakePoolStateBytes       = 256
	stakePoolRewardScale         = uint64(1_000_000_000)
)

// StakePoolInstructionType 定义池化质押指令 + VM 只封装池账户业务不承载 DPoS 共识核心。
type StakePoolInstructionType uint32

const (
	StakePoolInstructionInitialize StakePoolInstructionType = iota
	StakePoolInstructionDeposit
	StakePoolInstructionRegisterValidator
	StakePoolInstructionDelegate
	StakePoolInstructionDistributeRewards
	StakePoolInstructionClaimRewards
	StakePoolInstructionRequestUnstake
	StakePoolInstructionCompleteUnstake
	StakePoolInstructionWithdrawUnstaked
)

// StakePoolInstruction 描述 VM 池化质押指令 + 使用固定二进制格式保证确定性执行。
type StakePoolInstruction struct {
	Type               StakePoolInstructionType
	Amount             uint64
	UnlockEpoch        uint64
	ValidatorAccount   structure.PublicKey
	ConsensusPublicKey structure.PublicKey
	P2PPeerID          string
	CommissionBps      uint16
	BLSPublicKey       []byte
}

// StakePoolState 保存池级状态 + 份额和收益会计留在 VM 合约边界内。
type StakePoolState struct {
	Version                   uint16
	Authority                 structure.PublicKey
	ValidatorAccount          structure.PublicKey
	TotalShares               uint64
	TotalDepositedLamports    uint64
	DelegatedLamports         uint64
	RewardLamports            uint64
	AccumulatedRewardPerShare uint64
	PendingUnstakeLamports    uint64
	WithdrawableLamports      uint64
	UnlockEpoch               uint64
}

// StakePoolReceiptState 保存用户份额状态 + 避免池账户扫描所有委托人。
type StakePoolReceiptState struct {
	Version                 uint16
	PoolAccount             structure.PublicKey
	Owner                   structure.PublicKey
	Shares                  uint64
	RewardDebt              uint64
	ClaimedRewards          uint64
	PendingWithdrawLamports uint64
}

type stakePoolAccounts struct {
	userAddress      structure.PublicKey
	poolAddress      structure.PublicKey
	receiptAddress   structure.PublicKey
	validatorAddress structure.PublicKey
}

// NewStakePoolInitializeInstruction 创建初始化指令 + 绑定池账户和固定验证者账户。
func NewStakePoolInitializeInstruction(validatorAccount structure.PublicKey) (StakePoolInstruction, error) {
	instruction := StakePoolInstruction{Type: StakePoolInstructionInitialize, ValidatorAccount: validatorAccount}
	return instruction, instruction.Validate()
}

// NewStakePoolDepositInstruction 创建存入指令 + 存入后按本金 1:1 铸造池份额。
func NewStakePoolDepositInstruction(amount uint64) (StakePoolInstruction, error) {
	instruction := StakePoolInstruction{Type: StakePoolInstructionDeposit, Amount: amount}
	return instruction, instruction.Validate()
}

// NewStakePoolRegisterValidatorInstruction 创建验证者注册指令 + 通过固定 Stake Program 写入验证者状态。
func NewStakePoolRegisterValidatorInstruction(
	consensusPublicKey structure.PublicKey,
	p2pPeerID string,
	commissionBps uint16,
	amount uint64,
	blsPublicKey []byte,
) (StakePoolInstruction, error) {
	instruction := StakePoolInstruction{
		Type:               StakePoolInstructionRegisterValidator,
		Amount:             amount,
		ConsensusPublicKey: consensusPublicKey,
		P2PPeerID:          p2pPeerID,
		CommissionBps:      commissionBps,
		BLSPublicKey:       utils.CloneBytes(blsPublicKey),
	}
	return instruction, instruction.Validate()
}

// NewStakePoolDelegateInstruction 创建追加质押指令 + 池账户作为 staker 调用固定 Stake Program。
func NewStakePoolDelegateInstruction(amount uint64) (StakePoolInstruction, error) {
	instruction := StakePoolInstruction{Type: StakePoolInstructionDelegate, Amount: amount}
	return instruction, instruction.Validate()
}

// NewStakePoolDistributeRewardsInstruction 创建收益入账指令 + 只记录已经进入池账户的可分配收益。
func NewStakePoolDistributeRewardsInstruction(amount uint64) (StakePoolInstruction, error) {
	instruction := StakePoolInstruction{Type: StakePoolInstructionDistributeRewards, Amount: amount}
	return instruction, instruction.Validate()
}

// NewStakePoolClaimRewardsInstruction 创建领取收益指令 + 用户按份额领取已分配收益。
func NewStakePoolClaimRewardsInstruction() StakePoolInstruction {
	return StakePoolInstruction{Type: StakePoolInstructionClaimRewards}
}

// NewStakePoolRequestUnstakeInstruction 创建请求解质押指令 + 池账户调用固定 Stake Program 进入 unlocking。
func NewStakePoolRequestUnstakeInstruction(amount uint64, unlockEpoch uint64) (StakePoolInstruction, error) {
	instruction := StakePoolInstruction{Type: StakePoolInstructionRequestUnstake, Amount: amount, UnlockEpoch: unlockEpoch}
	return instruction, instruction.Validate()
}

// NewStakePoolCompleteUnstakeInstruction 创建完成解质押指令 + 到期后把 unlocking 资金提回池账户。
func NewStakePoolCompleteUnstakeInstruction(currentEpoch uint64) (StakePoolInstruction, error) {
	instruction := StakePoolInstruction{Type: StakePoolInstructionCompleteUnstake, UnlockEpoch: currentEpoch}
	return instruction, instruction.Validate()
}

// NewStakePoolWithdrawUnstakedInstruction 创建提现解质押资金指令 + 用户领取已到池账户的本金。
func NewStakePoolWithdrawUnstakedInstruction() StakePoolInstruction {
	return StakePoolInstruction{Type: StakePoolInstructionWithdrawUnstaked}
}

// Validate 校验池化质押指令 + 在执行前拒绝非法金额和元数据。
func (instruction StakePoolInstruction) Validate() error {
	if len(instruction.P2PPeerID) > stakeprogram.MaxPeerIDLength {
		return fmt.Errorf("stake pool: p2p peer id too long")
	}
	if len(instruction.BLSPublicKey) > stakeprogram.MaxBLSPublicKeyBytes {
		return fmt.Errorf("stake pool: bls public key too long")
	}
	if instruction.CommissionBps > 10000 {
		return fmt.Errorf("stake pool: commission exceeds 10000 bps")
	}
	switch instruction.Type {
	case StakePoolInstructionInitialize:
		if instruction.ValidatorAccount.IsZero() {
			return fmt.Errorf("stake pool: validator account is empty")
		}
		return nil
	case StakePoolInstructionDeposit, StakePoolInstructionDelegate, StakePoolInstructionDistributeRewards:
		if instruction.Amount == 0 {
			return fmt.Errorf("stake pool: amount is zero")
		}
		return nil
	case StakePoolInstructionRegisterValidator:
		if instruction.Amount < stakeprogram.MinimumStakeLamports {
			return fmt.Errorf("stake pool: amount below minimum stake")
		}
		if instruction.ConsensusPublicKey.IsZero() {
			return fmt.Errorf("stake pool: consensus public key is empty")
		}
		if instruction.P2PPeerID == "" {
			return fmt.Errorf("stake pool: p2p peer id is empty")
		}
		return nil
	case StakePoolInstructionClaimRewards:
		return nil
	case StakePoolInstructionRequestUnstake:
		if instruction.Amount < stakeprogram.MinimumStakeLamports {
			return fmt.Errorf("stake pool: unstake amount below minimum stake")
		}
		if instruction.UnlockEpoch == 0 {
			return fmt.Errorf("stake pool: unlock epoch is zero")
		}
		return nil
	case StakePoolInstructionCompleteUnstake:
		if instruction.UnlockEpoch == 0 {
			return fmt.Errorf("stake pool: current epoch is zero")
		}
		return nil
	case StakePoolInstructionWithdrawUnstaked:
		return nil
	default:
		return fmt.Errorf("stake pool: unsupported instruction type %d", instruction.Type)
	}
}

// MarshalBinary 序列化池化指令 + 供交易和 VM 程序账户复算同一字节。
func (instruction StakePoolInstruction) MarshalBinary() ([]byte, error) {
	if err := instruction.Validate(); err != nil {
		return nil, err
	}
	writer := borsh.NewWriter(maxStakePoolInstructionBytes)
	writer.WriteUint32(uint32(instruction.Type))
	writer.WriteUint64(instruction.Amount)
	writer.WriteUint64(instruction.UnlockEpoch)
	writer.WriteFixedBytes(instruction.ValidatorAccount[:])
	writer.WriteFixedBytes(instruction.ConsensusPublicKey[:])
	if err := writer.WriteString(instruction.P2PPeerID); err != nil {
		return nil, fmt.Errorf("stake pool: encode p2p peer id: %w", err)
	}
	writer.WriteUint16(instruction.CommissionBps)
	if err := writer.WriteBytes(instruction.BLSPublicKey); err != nil {
		return nil, fmt.Errorf("stake pool: encode bls public key: %w", err)
	}
	return writer.Bytes(), nil
}

// UnmarshalStakePoolInstructionBinary 反序列化池化指令 + 解码后继续执行边界校验。
func UnmarshalStakePoolInstructionBinary(data []byte) (StakePoolInstruction, error) {
	reader := borsh.NewReader(data, maxStakePoolInstructionBytes)
	instructionType, err := reader.ReadUint32()
	if err != nil {
		return StakePoolInstruction{}, fmt.Errorf("stake pool: decode instruction type: %w", err)
	}
	amount, err := reader.ReadUint64()
	if err != nil {
		return StakePoolInstruction{}, fmt.Errorf("stake pool: decode amount: %w", err)
	}
	unlockEpoch, err := reader.ReadUint64()
	if err != nil {
		return StakePoolInstruction{}, fmt.Errorf("stake pool: decode unlock epoch: %w", err)
	}
	validatorAccount, err := readStakePoolPublicKey(reader, "validator account")
	if err != nil {
		return StakePoolInstruction{}, err
	}
	consensusPublicKey, err := readStakePoolPublicKey(reader, "consensus public key")
	if err != nil {
		return StakePoolInstruction{}, err
	}
	p2pPeerID, err := reader.ReadString()
	if err != nil {
		return StakePoolInstruction{}, fmt.Errorf("stake pool: decode p2p peer id: %w", err)
	}
	commissionBps, err := reader.ReadUint16()
	if err != nil {
		return StakePoolInstruction{}, fmt.Errorf("stake pool: decode commission: %w", err)
	}
	blsPublicKey, err := reader.ReadBytes()
	if err != nil {
		return StakePoolInstruction{}, fmt.Errorf("stake pool: decode bls public key: %w", err)
	}
	if err := reader.EnsureEOF(); err != nil {
		return StakePoolInstruction{}, fmt.Errorf("stake pool: decode instruction eof: %w", err)
	}
	instruction := StakePoolInstruction{
		Type:               StakePoolInstructionType(instructionType),
		Amount:             amount,
		UnlockEpoch:        unlockEpoch,
		ValidatorAccount:   validatorAccount,
		ConsensusPublicKey: consensusPublicKey,
		P2PPeerID:          p2pPeerID,
		CommissionBps:      commissionBps,
		BLSPublicKey:       blsPublicKey,
	}
	return instruction, instruction.Validate()
}

// MarshalBinary 序列化池状态 + 固定字段顺序避免跨版本歧义。
func (state StakePoolState) MarshalBinary() ([]byte, error) {
	if err := state.Validate(); err != nil {
		return nil, err
	}
	writer := borsh.NewWriter(maxStakePoolStateBytes)
	writer.WriteUint16(state.Version)
	writer.WriteFixedBytes(state.Authority[:])
	writer.WriteFixedBytes(state.ValidatorAccount[:])
	writer.WriteUint64(state.TotalShares)
	writer.WriteUint64(state.TotalDepositedLamports)
	writer.WriteUint64(state.DelegatedLamports)
	writer.WriteUint64(state.RewardLamports)
	writer.WriteUint64(state.AccumulatedRewardPerShare)
	writer.WriteUint64(state.PendingUnstakeLamports)
	writer.WriteUint64(state.WithdrawableLamports)
	writer.WriteUint64(state.UnlockEpoch)
	return writer.Bytes(), nil
}

// Validate 校验池状态 + 防止份额和收益会计写入不一致数据。
func (state StakePoolState) Validate() error {
	if state.Version != stakePoolStateVersion {
		return fmt.Errorf("stake pool: unsupported state version %d", state.Version)
	}
	if state.Authority.IsZero() {
		return fmt.Errorf("stake pool: authority is empty")
	}
	if state.ValidatorAccount.IsZero() {
		return fmt.Errorf("stake pool: validator account is empty")
	}
	if state.PendingUnstakeLamports > state.DelegatedLamports {
		return fmt.Errorf("stake pool: pending unstake exceeds delegated lamports")
	}
	if state.PendingUnstakeLamports > 0 && state.UnlockEpoch == 0 {
		return fmt.Errorf("stake pool: pending unstake requires unlock epoch")
	}
	if state.PendingUnstakeLamports == 0 && state.UnlockEpoch != 0 {
		return fmt.Errorf("stake pool: unlock epoch without pending unstake")
	}
	if state.TotalShares == 0 && state.TotalDepositedLamports != 0 {
		return fmt.Errorf("stake pool: empty pool has active principal")
	}
	return nil
}

// UnmarshalStakePoolStateBinary 反序列化池状态 + 供测试和后续 RPC 查询复用。
func UnmarshalStakePoolStateBinary(data []byte) (StakePoolState, error) {
	reader := borsh.NewReader(data, maxStakePoolStateBytes)
	version, err := reader.ReadUint16()
	if err != nil {
		return StakePoolState{}, fmt.Errorf("stake pool: decode state version: %w", err)
	}
	authority, err := readStakePoolPublicKey(reader, "authority")
	if err != nil {
		return StakePoolState{}, err
	}
	validatorAccount, err := readStakePoolPublicKey(reader, "validator account")
	if err != nil {
		return StakePoolState{}, err
	}
	totalShares, err := reader.ReadUint64()
	if err != nil {
		return StakePoolState{}, fmt.Errorf("stake pool: decode total shares: %w", err)
	}
	totalDepositedLamports, err := reader.ReadUint64()
	if err != nil {
		return StakePoolState{}, fmt.Errorf("stake pool: decode total deposited: %w", err)
	}
	delegatedLamports, err := reader.ReadUint64()
	if err != nil {
		return StakePoolState{}, fmt.Errorf("stake pool: decode delegated lamports: %w", err)
	}
	rewardLamports, err := reader.ReadUint64()
	if err != nil {
		return StakePoolState{}, fmt.Errorf("stake pool: decode reward lamports: %w", err)
	}
	accumulatedRewardPerShare, err := reader.ReadUint64()
	if err != nil {
		return StakePoolState{}, fmt.Errorf("stake pool: decode accumulated reward: %w", err)
	}
	pendingUnstakeLamports, err := reader.ReadUint64()
	if err != nil {
		return StakePoolState{}, fmt.Errorf("stake pool: decode pending unstake: %w", err)
	}
	withdrawableLamports, err := reader.ReadUint64()
	if err != nil {
		return StakePoolState{}, fmt.Errorf("stake pool: decode withdrawable lamports: %w", err)
	}
	unlockEpoch, err := reader.ReadUint64()
	if err != nil {
		return StakePoolState{}, fmt.Errorf("stake pool: decode unlock epoch: %w", err)
	}
	if err := reader.EnsureEOF(); err != nil {
		return StakePoolState{}, fmt.Errorf("stake pool: decode state eof: %w", err)
	}
	state := StakePoolState{
		Version:                   version,
		Authority:                 authority,
		ValidatorAccount:          validatorAccount,
		TotalShares:               totalShares,
		TotalDepositedLamports:    totalDepositedLamports,
		DelegatedLamports:         delegatedLamports,
		RewardLamports:            rewardLamports,
		AccumulatedRewardPerShare: accumulatedRewardPerShare,
		PendingUnstakeLamports:    pendingUnstakeLamports,
		WithdrawableLamports:      withdrawableLamports,
		UnlockEpoch:               unlockEpoch,
	}
	return state, state.Validate()
}

// MarshalBinary 序列化用户份额状态 + 记录 reward debt 防止历史收益被新份额领取。
func (state StakePoolReceiptState) MarshalBinary() ([]byte, error) {
	if err := state.Validate(); err != nil {
		return nil, err
	}
	writer := borsh.NewWriter(maxStakePoolStateBytes)
	writer.WriteUint16(state.Version)
	writer.WriteFixedBytes(state.PoolAccount[:])
	writer.WriteFixedBytes(state.Owner[:])
	writer.WriteUint64(state.Shares)
	writer.WriteUint64(state.RewardDebt)
	writer.WriteUint64(state.ClaimedRewards)
	writer.WriteUint64(state.PendingWithdrawLamports)
	return writer.Bytes(), nil
}

// Validate 校验用户份额状态 + 防止跨池或跨 owner 的 receipt 混用。
func (state StakePoolReceiptState) Validate() error {
	if state.Version != stakePoolReceiptVersion {
		return fmt.Errorf("stake pool: unsupported receipt version %d", state.Version)
	}
	if state.PoolAccount.IsZero() {
		return fmt.Errorf("stake pool: receipt pool account is empty")
	}
	if state.Owner.IsZero() {
		return fmt.Errorf("stake pool: receipt owner is empty")
	}
	return nil
}

// UnmarshalStakePoolReceiptStateBinary 反序列化用户份额状态 + 解码后校验 owner 绑定。
func UnmarshalStakePoolReceiptStateBinary(data []byte) (StakePoolReceiptState, error) {
	reader := borsh.NewReader(data, maxStakePoolStateBytes)
	version, err := reader.ReadUint16()
	if err != nil {
		return StakePoolReceiptState{}, fmt.Errorf("stake pool: decode receipt version: %w", err)
	}
	poolAccount, err := readStakePoolPublicKey(reader, "receipt pool account")
	if err != nil {
		return StakePoolReceiptState{}, err
	}
	owner, err := readStakePoolPublicKey(reader, "receipt owner")
	if err != nil {
		return StakePoolReceiptState{}, err
	}
	shares, err := reader.ReadUint64()
	if err != nil {
		return StakePoolReceiptState{}, fmt.Errorf("stake pool: decode receipt shares: %w", err)
	}
	rewardDebt, err := reader.ReadUint64()
	if err != nil {
		return StakePoolReceiptState{}, fmt.Errorf("stake pool: decode receipt reward debt: %w", err)
	}
	claimedRewards, err := reader.ReadUint64()
	if err != nil {
		return StakePoolReceiptState{}, fmt.Errorf("stake pool: decode receipt claimed rewards: %w", err)
	}
	pendingWithdrawLamports, err := reader.ReadUint64()
	if err != nil {
		return StakePoolReceiptState{}, fmt.Errorf("stake pool: decode receipt pending withdraw: %w", err)
	}
	if err := reader.EnsureEOF(); err != nil {
		return StakePoolReceiptState{}, fmt.Errorf("stake pool: decode receipt eof: %w", err)
	}
	state := StakePoolReceiptState{
		Version:                 version,
		PoolAccount:             poolAccount,
		Owner:                   owner,
		Shares:                  shares,
		RewardDebt:              rewardDebt,
		ClaimedRewards:          claimedRewards,
		PendingWithdrawLamports: pendingWithdrawLamports,
	}
	return state, state.Validate()
}

// StakePoolBridgeProgramData 返回内置池化质押 VM 字节码 + 内容等价于 syscall stake_pool_execute; exit。
func StakePoolBridgeProgramData() ([]byte, error) {
	dispatchInstruction, err := svm.BuildRegisterInstruction(svm.RegOpSyscall, 0, 0, 0, uint64(svm.SyscallStakePoolExecute))
	if err != nil {
		return nil, err
	}
	code, err := svm.BuildRegisterProgramCode(dispatchInstruction)
	if err != nil {
		return nil, err
	}
	return svm.EncodeGovernedRegisterBytecode(code, nil, svm.ProgramManifest{
		ComputeUnitLimit: svm.DefaultComputeUnitLimit,
		RequiredSyscalls: []svm.SyscallID{svm.SyscallStakePoolExecute},
	})
}

// attachStakePoolSyscall 注入池化质押 syscall + VM 只通过受控入口调度固定 Stake Program。
func attachStakePoolSyscall(runtimeValue *svm.Runtime, context runtime.InstructionContext) error {
	if runtimeValue == nil {
		return fmt.Errorf("vm program: runtime is nil")
	}
	registry := runtimeValue.Syscalls
	if registry.IsZero() {
		registry = svm.DefaultSyscallRegistry()
	}
	extendedRegistry, err := registry.With(svm.Syscall{
		ID:      svm.SyscallStakePoolExecute,
		Name:    "stake_pool_execute",
		Cost:    stakePoolExecuteSyscallCost,
		Handler: stakePoolExecuteSyscall(context),
	})
	if err != nil {
		return fmt.Errorf("vm program: attach stake pool syscall: %w", err)
	}
	runtimeValue.Syscalls = extendedRegistry
	return nil
}

// stakePoolExecuteSyscall 执行池化质押业务 + 成功后同步 VM 和外层账户快照。
func stakePoolExecuteSyscall(outerContext runtime.InstructionContext) svm.SyscallFunc {
	return func(vmContext *svm.Context, input []byte) ([]byte, error) {
		if vmContext == nil || vmContext.Accounts == nil {
			return nil, fmt.Errorf("stake pool syscall: vm context is nil")
		}
		if len(input) != 0 {
			return nil, fmt.Errorf("stake pool syscall: input must be empty")
		}

		instruction, err := UnmarshalStakePoolInstructionBinary(outerContext.Instruction.Data)
		if err != nil {
			return nil, err
		}
		workingAccounts := cloneStructureAccounts(outerContext.Accounts)
		if err := executeStakePoolInstruction(instruction, outerContext, workingAccounts); err != nil {
			return nil, err
		}
		if err := commitStakePoolSyscallAccounts(vmContext, outerContext, workingAccounts); err != nil {
			return nil, err
		}
		return nil, nil
	}
}

func executeStakePoolInstruction(
	instruction StakePoolInstruction,
	context runtime.InstructionContext,
	accounts map[structure.PublicKey]structure.Account,
) error {
	switch instruction.Type {
	case StakePoolInstructionInitialize:
		return executeStakePoolInitialize(instruction, context, accounts)
	case StakePoolInstructionDeposit:
		return executeStakePoolDeposit(instruction, context, accounts)
	case StakePoolInstructionRegisterValidator:
		return executeStakePoolRegisterValidator(instruction, context, accounts)
	case StakePoolInstructionDelegate:
		return executeStakePoolDelegate(instruction, context, accounts)
	case StakePoolInstructionDistributeRewards:
		return executeStakePoolDistributeRewards(instruction, context, accounts)
	case StakePoolInstructionClaimRewards:
		return executeStakePoolClaimRewards(context, accounts)
	case StakePoolInstructionRequestUnstake:
		return executeStakePoolRequestUnstake(instruction, context, accounts)
	case StakePoolInstructionCompleteUnstake:
		return executeStakePoolCompleteUnstake(instruction, context, accounts)
	case StakePoolInstructionWithdrawUnstaked:
		return executeStakePoolWithdrawUnstaked(context, accounts)
	default:
		return fmt.Errorf("stake pool: unsupported instruction type %d", instruction.Type)
	}
}

func executeStakePoolInitialize(
	instruction StakePoolInstruction,
	context runtime.InstructionContext,
	accounts map[structure.PublicKey]structure.Account,
) error {
	accountSet, err := loadStakePoolAccounts(context, accounts, false)
	if err != nil {
		return err
	}
	if err := requireTransactionSigner(accountSet.userAddress, context, "authority"); err != nil {
		return err
	}
	poolAccount := accounts[accountSet.poolAddress]
	if len(poolAccount.Data) != 0 {
		return fmt.Errorf("stake pool: pool account already initialized")
	}
	state := StakePoolState{
		Version:          stakePoolStateVersion,
		Authority:        accountSet.userAddress,
		ValidatorAccount: instruction.ValidatorAccount,
	}
	return writeStakePoolState(accountSet.poolAddress, poolAccount, state, context, accounts)
}

func executeStakePoolDeposit(
	instruction StakePoolInstruction,
	context runtime.InstructionContext,
	accounts map[structure.PublicKey]structure.Account,
) error {
	accountSet, err := loadStakePoolAccounts(context, accounts, false)
	if err != nil {
		return err
	}
	if err := requireTransactionSigner(accountSet.userAddress, context, "depositor"); err != nil {
		return err
	}
	state, err := readStakePoolState(accountSet.poolAddress, accounts)
	if err != nil {
		return err
	}
	receipt, err := readOrCreateStakePoolReceipt(accountSet, accounts)
	if err != nil {
		return err
	}
	pendingRewards, err := stakePoolPendingRewards(state, receipt)
	if err != nil {
		return err
	}
	if err := runtime.TransferLamports(accountSet.userAddress, accountSet.poolAddress, instruction.Amount, accounts, context.RentConfig); err != nil {
		return fmt.Errorf("stake pool: transfer deposit: %w", err)
	}
	if err := checkedAddAssign(&state.TotalShares, instruction.Amount, "stake pool: total shares overflow"); err != nil {
		return err
	}
	if err := checkedAddAssign(&state.TotalDepositedLamports, instruction.Amount, "stake pool: total deposited overflow"); err != nil {
		return err
	}
	if err := checkedAddAssign(&receipt.Shares, instruction.Amount, "stake pool: receipt shares overflow"); err != nil {
		return err
	}
	nextDebt, err := stakePoolRewardEntitlement(state, receipt.Shares)
	if err != nil {
		return err
	}
	if nextDebt < pendingRewards {
		return fmt.Errorf("stake pool: reward debt underflow")
	}
	receipt.RewardDebt = nextDebt - pendingRewards
	if err := writeStakePoolState(accountSet.poolAddress, accounts[accountSet.poolAddress], state, context, accounts); err != nil {
		return err
	}
	return writeStakePoolReceipt(accountSet.receiptAddress, accounts[accountSet.receiptAddress], receipt, context, accounts)
}

func executeStakePoolRegisterValidator(
	instruction StakePoolInstruction,
	context runtime.InstructionContext,
	accounts map[structure.PublicKey]structure.Account,
) error {
	accountSet, state, err := loadStakePoolAuthorityContext(context, accounts, true)
	if err != nil {
		return err
	}
	if err := requireStakePoolUnbondedPrincipal(state, instruction.Amount); err != nil {
		return err
	}
	stakeInstruction, err := stakeprogram.NewRegisterValidatorInstructionWithBLS(
		instruction.ConsensusPublicKey,
		instruction.BLSPublicKey,
		instruction.P2PPeerID,
		instruction.CommissionBps,
		instruction.Amount,
	)
	if err != nil {
		return err
	}
	if err := executeNestedStakeInstruction(stakeInstruction, accountSet, context, accounts); err != nil {
		return err
	}
	if err := checkedAddAssign(&state.DelegatedLamports, instruction.Amount, "stake pool: delegated lamports overflow"); err != nil {
		return err
	}
	return writeStakePoolState(accountSet.poolAddress, accounts[accountSet.poolAddress], state, context, accounts)
}

func executeStakePoolDelegate(
	instruction StakePoolInstruction,
	context runtime.InstructionContext,
	accounts map[structure.PublicKey]structure.Account,
) error {
	accountSet, state, err := loadStakePoolAuthorityContext(context, accounts, true)
	if err != nil {
		return err
	}
	if err := requireStakePoolUnbondedPrincipal(state, instruction.Amount); err != nil {
		return err
	}
	stakeInstruction, err := stakeprogram.NewStakeInstruction(instruction.Amount)
	if err != nil {
		return err
	}
	if err := executeNestedStakeInstruction(stakeInstruction, accountSet, context, accounts); err != nil {
		return err
	}
	if err := checkedAddAssign(&state.DelegatedLamports, instruction.Amount, "stake pool: delegated lamports overflow"); err != nil {
		return err
	}
	return writeStakePoolState(accountSet.poolAddress, accounts[accountSet.poolAddress], state, context, accounts)
}

func executeStakePoolDistributeRewards(
	instruction StakePoolInstruction,
	context runtime.InstructionContext,
	accounts map[structure.PublicKey]structure.Account,
) error {
	accountSet, state, err := loadStakePoolAuthorityContext(context, accounts, false)
	if err != nil {
		return err
	}
	if state.TotalShares == 0 {
		return fmt.Errorf("stake pool: cannot distribute rewards without shares")
	}
	poolAccount := accounts[accountSet.poolAddress]
	if err := requirePoolRewardReserve(poolAccount, state, instruction.Amount, context.RentConfig); err != nil {
		return err
	}
	rewardPerShare, err := safeMulDivUint64(instruction.Amount, stakePoolRewardScale, state.TotalShares)
	if err != nil {
		return err
	}
	if rewardPerShare == 0 {
		return fmt.Errorf("stake pool: reward amount too small for current shares")
	}
	if err := checkedAddAssign(&state.RewardLamports, instruction.Amount, "stake pool: reward lamports overflow"); err != nil {
		return err
	}
	if err := checkedAddAssign(&state.AccumulatedRewardPerShare, rewardPerShare, "stake pool: accumulated reward overflow"); err != nil {
		return err
	}
	return writeStakePoolState(accountSet.poolAddress, poolAccount, state, context, accounts)
}

func executeStakePoolClaimRewards(
	context runtime.InstructionContext,
	accounts map[structure.PublicKey]structure.Account,
) error {
	accountSet, err := loadStakePoolAccounts(context, accounts, false)
	if err != nil {
		return err
	}
	if err := requireTransactionSigner(accountSet.userAddress, context, "reward owner"); err != nil {
		return err
	}
	state, err := readStakePoolState(accountSet.poolAddress, accounts)
	if err != nil {
		return err
	}
	receipt, err := readExistingStakePoolReceipt(accountSet, accounts)
	if err != nil {
		return err
	}
	pendingRewards, err := stakePoolPendingRewards(state, receipt)
	if err != nil {
		return err
	}
	if pendingRewards == 0 {
		return fmt.Errorf("stake pool: no rewards to claim")
	}
	if err := runtime.TransferLamports(accountSet.poolAddress, accountSet.userAddress, pendingRewards, accounts, context.RentConfig); err != nil {
		return fmt.Errorf("stake pool: transfer rewards: %w", err)
	}
	nextDebt, err := stakePoolRewardEntitlement(state, receipt.Shares)
	if err != nil {
		return err
	}
	receipt.RewardDebt = nextDebt
	if err := checkedAddAssign(&receipt.ClaimedRewards, pendingRewards, "stake pool: claimed rewards overflow"); err != nil {
		return err
	}
	return writeStakePoolReceipt(accountSet.receiptAddress, accounts[accountSet.receiptAddress], receipt, context, accounts)
}

func executeStakePoolRequestUnstake(
	instruction StakePoolInstruction,
	context runtime.InstructionContext,
	accounts map[structure.PublicKey]structure.Account,
) error {
	accountSet, err := loadStakePoolAccounts(context, accounts, true)
	if err != nil {
		return err
	}
	if err := requireTransactionSigner(accountSet.userAddress, context, "unstake owner"); err != nil {
		return err
	}
	state, err := readStakePoolState(accountSet.poolAddress, accounts)
	if err != nil {
		return err
	}
	if accountSet.validatorAddress != state.ValidatorAccount {
		return fmt.Errorf("stake pool: validator account mismatch")
	}
	if state.PendingUnstakeLamports != 0 {
		return fmt.Errorf("stake pool: another unstake batch is pending")
	}
	if instruction.UnlockEpoch <= context.CurrentEpoch {
		return fmt.Errorf("stake pool: unlock epoch must be greater than current epoch")
	}
	receipt, err := readExistingStakePoolReceipt(accountSet, accounts)
	if err != nil {
		return err
	}
	if instruction.Amount > receipt.Shares {
		return fmt.Errorf("stake pool: unstake amount exceeds receipt shares")
	}
	pendingRewards, err := stakePoolPendingRewards(state, receipt)
	if err != nil {
		return err
	}
	if pendingRewards != 0 {
		return fmt.Errorf("stake pool: claim rewards before unstake")
	}
	stakeInstruction, err := stakeprogram.NewUnstakeInstruction(instruction.Amount, instruction.UnlockEpoch)
	if err != nil {
		return err
	}
	if err := executeNestedStakeInstruction(stakeInstruction, accountSet, context, accounts); err != nil {
		return err
	}
	if err := checkedSubAssign(&state.TotalShares, instruction.Amount, "stake pool: total shares underflow"); err != nil {
		return err
	}
	if err := checkedSubAssign(&state.TotalDepositedLamports, instruction.Amount, "stake pool: total deposited underflow"); err != nil {
		return err
	}
	if err := checkedAddAssign(&state.PendingUnstakeLamports, instruction.Amount, "stake pool: pending unstake overflow"); err != nil {
		return err
	}
	state.UnlockEpoch = instruction.UnlockEpoch
	if err := checkedSubAssign(&receipt.Shares, instruction.Amount, "stake pool: receipt shares underflow"); err != nil {
		return err
	}
	if err := checkedAddAssign(&receipt.PendingWithdrawLamports, instruction.Amount, "stake pool: receipt pending withdraw overflow"); err != nil {
		return err
	}
	nextDebt, err := stakePoolRewardEntitlement(state, receipt.Shares)
	if err != nil {
		return err
	}
	receipt.RewardDebt = nextDebt
	if err := writeStakePoolState(accountSet.poolAddress, accounts[accountSet.poolAddress], state, context, accounts); err != nil {
		return err
	}
	return writeStakePoolReceipt(accountSet.receiptAddress, accounts[accountSet.receiptAddress], receipt, context, accounts)
}

func executeStakePoolCompleteUnstake(
	instruction StakePoolInstruction,
	context runtime.InstructionContext,
	accounts map[structure.PublicKey]structure.Account,
) error {
	accountSet, state, err := loadStakePoolAuthorityContext(context, accounts, true)
	if err != nil {
		return err
	}
	if state.PendingUnstakeLamports == 0 {
		return fmt.Errorf("stake pool: no pending unstake batch")
	}
	if instruction.UnlockEpoch < state.UnlockEpoch {
		return fmt.Errorf("stake pool: unstake batch is not withdrawable")
	}
	beforeLamports := accounts[accountSet.poolAddress].Lamports
	stakeInstruction, err := stakeprogram.NewWithdrawUnstakedInstruction(instruction.UnlockEpoch)
	if err != nil {
		return err
	}
	if err := executeNestedStakeInstruction(stakeInstruction, accountSet, context, accounts); err != nil {
		return err
	}
	afterLamports := accounts[accountSet.poolAddress].Lamports
	if afterLamports < beforeLamports {
		return fmt.Errorf("stake pool: pool lamports decreased during unstake completion")
	}
	receivedLamports := afterLamports - beforeLamports
	if receivedLamports != state.PendingUnstakeLamports {
		return fmt.Errorf("stake pool: received unstake %d, want %d", receivedLamports, state.PendingUnstakeLamports)
	}
	if err := checkedSubAssign(&state.DelegatedLamports, receivedLamports, "stake pool: delegated lamports underflow"); err != nil {
		return err
	}
	if err := checkedAddAssign(&state.WithdrawableLamports, receivedLamports, "stake pool: withdrawable lamports overflow"); err != nil {
		return err
	}
	state.PendingUnstakeLamports = 0
	state.UnlockEpoch = 0
	return writeStakePoolState(accountSet.poolAddress, accounts[accountSet.poolAddress], state, context, accounts)
}

func executeStakePoolWithdrawUnstaked(
	context runtime.InstructionContext,
	accounts map[structure.PublicKey]structure.Account,
) error {
	accountSet, err := loadStakePoolAccounts(context, accounts, false)
	if err != nil {
		return err
	}
	if err := requireTransactionSigner(accountSet.userAddress, context, "unstake owner"); err != nil {
		return err
	}
	state, err := readStakePoolState(accountSet.poolAddress, accounts)
	if err != nil {
		return err
	}
	receipt, err := readExistingStakePoolReceipt(accountSet, accounts)
	if err != nil {
		return err
	}
	amount := receipt.PendingWithdrawLamports
	if amount == 0 {
		return fmt.Errorf("stake pool: no unstaked lamports to withdraw")
	}
	if amount > state.WithdrawableLamports {
		return fmt.Errorf("stake pool: withdraw amount exceeds pool withdrawable lamports")
	}
	if err := runtime.TransferLamports(accountSet.poolAddress, accountSet.userAddress, amount, accounts, context.RentConfig); err != nil {
		return fmt.Errorf("stake pool: transfer unstaked lamports: %w", err)
	}
	if err := checkedSubAssign(&state.WithdrawableLamports, amount, "stake pool: withdrawable lamports underflow"); err != nil {
		return err
	}
	receipt.PendingWithdrawLamports = 0
	if err := writeStakePoolState(accountSet.poolAddress, accounts[accountSet.poolAddress], state, context, accounts); err != nil {
		return err
	}
	return writeStakePoolReceipt(accountSet.receiptAddress, accounts[accountSet.receiptAddress], receipt, context, accounts)
}

func loadStakePoolAuthorityContext(
	context runtime.InstructionContext,
	accounts map[structure.PublicKey]structure.Account,
	validatorRequired bool,
) (stakePoolAccounts, StakePoolState, error) {
	accountSet, err := loadStakePoolAccounts(context, accounts, validatorRequired)
	if err != nil {
		return stakePoolAccounts{}, StakePoolState{}, err
	}
	state, err := readStakePoolState(accountSet.poolAddress, accounts)
	if err != nil {
		return stakePoolAccounts{}, StakePoolState{}, err
	}
	if err := requireTransactionSigner(state.Authority, context, "pool authority"); err != nil {
		return stakePoolAccounts{}, StakePoolState{}, err
	}
	if validatorRequired && accountSet.validatorAddress != state.ValidatorAccount {
		return stakePoolAccounts{}, StakePoolState{}, fmt.Errorf("stake pool: validator account mismatch")
	}
	return accountSet, state, nil
}

func loadStakePoolAccounts(
	context runtime.InstructionContext,
	accounts map[structure.PublicKey]structure.Account,
	validatorRequired bool,
) (stakePoolAccounts, error) {
	requiredAccounts := 3
	if validatorRequired {
		requiredAccounts = 4
	}
	if len(context.Instruction.AccountIndexes) < requiredAccounts {
		return stakePoolAccounts{}, fmt.Errorf("stake pool: instruction requires %d accounts", requiredAccounts)
	}
	userAddress, err := messageAccountAt(context, 0)
	if err != nil {
		return stakePoolAccounts{}, err
	}
	poolAddress, err := messageAccountAt(context, 1)
	if err != nil {
		return stakePoolAccounts{}, err
	}
	receiptAddress, err := messageAccountAt(context, 2)
	if err != nil {
		return stakePoolAccounts{}, err
	}
	if userAddress == poolAddress || userAddress == receiptAddress || poolAddress == receiptAddress {
		return stakePoolAccounts{}, fmt.Errorf("stake pool: user, pool and receipt accounts must be distinct")
	}
	if err := validateStakePoolOwnedWritableAccount("pool", poolAddress, context, accounts); err != nil {
		return stakePoolAccounts{}, err
	}
	if err := validateStakePoolOwnedWritableAccount("receipt", receiptAddress, context, accounts); err != nil {
		return stakePoolAccounts{}, err
	}
	if err := requireWritableInstructionAccount(context, 0, "user"); err != nil {
		return stakePoolAccounts{}, err
	}
	accountSet := stakePoolAccounts{userAddress: userAddress, poolAddress: poolAddress, receiptAddress: receiptAddress}
	if !validatorRequired {
		return accountSet, nil
	}
	validatorAddress, err := messageAccountAt(context, 3)
	if err != nil {
		return stakePoolAccounts{}, err
	}
	if validatorAddress == poolAddress || validatorAddress == receiptAddress || validatorAddress == userAddress {
		return stakePoolAccounts{}, fmt.Errorf("stake pool: validator account must be distinct")
	}
	if err := requireWritableInstructionAccount(context, 3, "validator"); err != nil {
		return stakePoolAccounts{}, err
	}
	if _, exists := accounts[validatorAddress]; !exists {
		return stakePoolAccounts{}, fmt.Errorf("%w: validator account not found", structure.ErrInvalidLoadedTransaction)
	}
	accountSet.validatorAddress = validatorAddress
	return accountSet, nil
}

func executeNestedStakeInstruction(
	stakeInstruction stakeprogram.Instruction,
	accountSet stakePoolAccounts,
	outerContext runtime.InstructionContext,
	accounts map[structure.PublicKey]structure.Account,
) error {
	instructionData, err := stakeInstruction.MarshalBinary()
	if err != nil {
		return err
	}
	stakeProgramID := stakeProgramIDForContext(outerContext)
	message, programIDIndex, err := stakeNestedMessage(outerContext.Message, stakeProgramID)
	if err != nil {
		return err
	}
	poolIndex, err := accountIndexInMessage(message.AccountKeys, accountSet.poolAddress)
	if err != nil {
		return err
	}
	validatorIndex, err := accountIndexInMessage(message.AccountKeys, accountSet.validatorAddress)
	if err != nil {
		return err
	}
	nestedContext := runtime.InstructionContext{
		InstructionIndex: outerContext.InstructionIndex,
		Instruction: structure.CompiledInstruction{
			ProgramIDIndex: programIDIndex,
			AccountIndexes: []uint8{poolIndex, validatorIndex},
			Data:           instructionData,
		},
		Message:         message,
		Accounts:        accounts,
		CurrentSlot:     outerContext.CurrentSlot,
		CurrentEpoch:    outerContext.CurrentEpoch,
		RentConfig:      outerContext.RentConfig,
		ComputeBudget:   outerContext.ComputeBudget,
		BuiltinPrograms: stakeBuiltinPrograms(outerContext.BuiltinPrograms, stakeProgramID),
		SignerOverrides: map[structure.PublicKey]struct{}{accountSet.poolAddress: {}},
		Logger:          outerContext.Logger,
	}
	if err := stakeprogram.NewProgram(stakeProgramID).Execute(nestedContext); err != nil {
		return fmt.Errorf("stake pool: execute fixed stake program: %w", err)
	}
	return nil
}

func readStakePoolState(poolAddress structure.PublicKey, accounts map[structure.PublicKey]structure.Account) (StakePoolState, error) {
	account := accounts[poolAddress]
	if len(account.Data) == 0 {
		return StakePoolState{}, fmt.Errorf("stake pool: pool account is not initialized")
	}
	return UnmarshalStakePoolStateBinary(account.Data)
}

func readOrCreateStakePoolReceipt(accountSet stakePoolAccounts, accounts map[structure.PublicKey]structure.Account) (StakePoolReceiptState, error) {
	account := accounts[accountSet.receiptAddress]
	if len(account.Data) == 0 {
		return StakePoolReceiptState{
			Version:     stakePoolReceiptVersion,
			PoolAccount: accountSet.poolAddress,
			Owner:       accountSet.userAddress,
		}, nil
	}
	return readExistingStakePoolReceipt(accountSet, accounts)
}

func readExistingStakePoolReceipt(accountSet stakePoolAccounts, accounts map[structure.PublicKey]structure.Account) (StakePoolReceiptState, error) {
	account := accounts[accountSet.receiptAddress]
	if len(account.Data) == 0 {
		return StakePoolReceiptState{}, fmt.Errorf("stake pool: receipt account is not initialized")
	}
	receipt, err := UnmarshalStakePoolReceiptStateBinary(account.Data)
	if err != nil {
		return StakePoolReceiptState{}, err
	}
	if receipt.PoolAccount != accountSet.poolAddress {
		return StakePoolReceiptState{}, fmt.Errorf("stake pool: receipt pool mismatch")
	}
	if receipt.Owner != accountSet.userAddress {
		return StakePoolReceiptState{}, fmt.Errorf("stake pool: receipt owner mismatch")
	}
	return receipt, nil
}

func writeStakePoolState(
	address structure.PublicKey,
	account structure.Account,
	state StakePoolState,
	context runtime.InstructionContext,
	accounts map[structure.PublicKey]structure.Account,
) error {
	data, err := state.MarshalBinary()
	if err != nil {
		return err
	}
	if err := account.SetData(data, context.RentConfig); err != nil {
		return fmt.Errorf("stake pool: write pool state: %w", err)
	}
	accounts[address] = account
	return nil
}

func writeStakePoolReceipt(
	address structure.PublicKey,
	account structure.Account,
	state StakePoolReceiptState,
	context runtime.InstructionContext,
	accounts map[structure.PublicKey]structure.Account,
) error {
	data, err := state.MarshalBinary()
	if err != nil {
		return err
	}
	if err := account.SetData(data, context.RentConfig); err != nil {
		return fmt.Errorf("stake pool: write receipt state: %w", err)
	}
	accounts[address] = account
	return nil
}

func stakePoolPendingRewards(pool StakePoolState, receipt StakePoolReceiptState) (uint64, error) {
	entitlement, err := stakePoolRewardEntitlement(pool, receipt.Shares)
	if err != nil {
		return 0, err
	}
	if entitlement < receipt.RewardDebt {
		return 0, fmt.Errorf("stake pool: reward debt exceeds entitlement")
	}
	return entitlement - receipt.RewardDebt, nil
}

func stakePoolRewardEntitlement(pool StakePoolState, shares uint64) (uint64, error) {
	return safeMulDivUint64(shares, pool.AccumulatedRewardPerShare, stakePoolRewardScale)
}

func requirePoolRewardReserve(account structure.Account, state StakePoolState, amount uint64, rentConfig structure.RentConfig) error {
	minimumBalance, err := account.MinimumBalance(rentConfig)
	if err != nil {
		return err
	}
	protectedLamports, err := stakePoolProtectedLamports(state)
	if err != nil {
		return err
	}
	requiredReserve, err := safeAddUint64(minimumBalance, protectedLamports)
	if err != nil {
		return fmt.Errorf("stake pool: calculate reward reserve: %w", err)
	}
	requiredReserve, err = safeAddUint64(requiredReserve, amount)
	if err != nil {
		return fmt.Errorf("stake pool: calculate reward reserve: %w", err)
	}
	if account.Lamports < requiredReserve {
		return fmt.Errorf("%w: reward reserve %d below required %d", structure.ErrInsufficientLamports, account.Lamports, requiredReserve)
	}
	return nil
}

func requireStakePoolUnbondedPrincipal(state StakePoolState, amount uint64) error {
	unbondedPrincipal, err := stakePoolUnbondedPrincipal(state)
	if err != nil {
		return err
	}
	if amount > unbondedPrincipal {
		return fmt.Errorf("%w: stake amount %d exceeds unbonded principal %d", structure.ErrInsufficientLamports, amount, unbondedPrincipal)
	}
	return nil
}

func stakePoolUnbondedPrincipal(state StakePoolState) (uint64, error) {
	if state.PendingUnstakeLamports > state.DelegatedLamports {
		return 0, fmt.Errorf("stake pool: pending unstake exceeds delegated lamports")
	}
	activeDelegatedLamports := state.DelegatedLamports - state.PendingUnstakeLamports
	if activeDelegatedLamports > state.TotalDepositedLamports {
		return 0, fmt.Errorf("stake pool: active delegated lamports exceed deposited lamports")
	}
	return state.TotalDepositedLamports - activeDelegatedLamports, nil
}

func stakePoolProtectedLamports(state StakePoolState) (uint64, error) {
	unbondedPrincipal, err := stakePoolUnbondedPrincipal(state)
	if err != nil {
		return 0, err
	}
	return safeAddUint64(unbondedPrincipal, state.WithdrawableLamports)
}

func validateStakePoolOwnedWritableAccount(
	name string,
	address structure.PublicKey,
	context runtime.InstructionContext,
	accounts map[structure.PublicKey]structure.Account,
) error {
	account, exists := accounts[address]
	if !exists {
		return fmt.Errorf("%w: %s account not found", structure.ErrInvalidLoadedTransaction, name)
	}
	if account.Executable {
		return fmt.Errorf("%w: %s account is executable", structure.ErrInvalidInstruction, name)
	}
	vmProgramID, err := stakePoolVMProgramID(context)
	if err != nil {
		return err
	}
	if account.Owner != vmProgramID {
		return fmt.Errorf("%w: %s account owner must be vm program", structure.ErrInvalidInstruction, name)
	}
	if err := requireWritableAddress(context, address, name); err != nil {
		return err
	}
	return nil
}

func commitStakePoolSyscallAccounts(
	vmContext *svm.Context,
	outerContext runtime.InstructionContext,
	workingAccounts map[structure.PublicKey]structure.Account,
) error {
	vmAccounts := make([]svm.Account, len(outerContext.Instruction.AccountIndexes))
	for accountIndex, messageAccountIndex := range outerContext.Instruction.AccountIndexes {
		account, err := buildInstructionAccount(
			runtime.InstructionContext{Message: outerContext.Message, Accounts: workingAccounts},
			int(messageAccountIndex),
		)
		if err != nil {
			return fmt.Errorf("stake pool syscall: build vm account %d: %w", accountIndex, err)
		}
		vmAccounts[accountIndex] = account
	}
	for accountIndex, vmAccount := range vmAccounts {
		if err := vmContext.Accounts.SetFixedProgramAccount(accountIndex, vmAccount); err != nil {
			return fmt.Errorf("stake pool syscall: sync vm account %d: %w", accountIndex, err)
		}
	}
	for _, messageAccountIndex := range outerContext.Instruction.AccountIndexes {
		address := outerContext.Message.AccountKeys[messageAccountIndex]
		outerContext.Accounts[address] = workingAccounts[address].Clone()
	}
	return nil
}

func stakeNestedMessage(message structure.ResolvedMessage, stakeProgramID structure.PublicKey) (structure.ResolvedMessage, uint8, error) {
	if stakeProgramID.IsZero() {
		return structure.ResolvedMessage{}, 0, fmt.Errorf("%w: stake program id is empty", structure.ErrInvalidInstruction)
	}
	nextMessage := message.Clone()
	if len(nextMessage.AccountKeys) == 0 {
		return structure.ResolvedMessage{}, 0, fmt.Errorf("%w: empty account keys", structure.ErrInvalidInstruction)
	}
	if len(nextMessage.StaticAccountKeys) == 0 {
		nextMessage.StaticAccountKeys = clonePublicKeys(nextMessage.AccountKeys)
		nextMessage.LoadedAddresses = structure.LoadedAddresses{}
	}
	for accountIndex, accountKey := range nextMessage.AccountKeys {
		if accountKey == stakeProgramID {
			return nextMessage, uint8(accountIndex), nil
		}
	}
	if len(nextMessage.AccountKeys) >= structure.MaxAccountsPerTransaction {
		return structure.ResolvedMessage{}, 0, fmt.Errorf("%w: account key count exceeds %d", structure.ErrInvalidInstruction, structure.MaxAccountsPerTransaction)
	}
	nextMessage.AccountKeys = append(nextMessage.AccountKeys, stakeProgramID)
	nextMessage.LoadedAddresses.Readonly = append(nextMessage.LoadedAddresses.Readonly, stakeProgramID)
	return nextMessage, uint8(len(nextMessage.AccountKeys) - 1), nil
}

func messageAccountAt(context runtime.InstructionContext, instructionAccountIndex int) (structure.PublicKey, error) {
	if instructionAccountIndex < 0 || instructionAccountIndex >= len(context.Instruction.AccountIndexes) {
		return structure.PublicKey{}, fmt.Errorf("%w: account index %d out of range", structure.ErrInvalidInstruction, instructionAccountIndex)
	}
	messageAccountIndex := int(context.Instruction.AccountIndexes[instructionAccountIndex])
	if messageAccountIndex < 0 || messageAccountIndex >= len(context.Message.AccountKeys) {
		return structure.PublicKey{}, fmt.Errorf("%w: message account index %d out of range", structure.ErrInvalidInstruction, messageAccountIndex)
	}
	return context.Message.AccountKeys[messageAccountIndex], nil
}

func accountIndexInMessage(accountKeys []structure.PublicKey, address structure.PublicKey) (uint8, error) {
	for accountIndex, accountKey := range accountKeys {
		if accountKey == address {
			return uint8(accountIndex), nil
		}
	}
	return 0, fmt.Errorf("%w: account %s not found in message", structure.ErrInvalidInstruction, address.String())
}

func requireWritableInstructionAccount(context runtime.InstructionContext, instructionAccountIndex int, name string) error {
	if instructionAccountIndex < 0 || instructionAccountIndex >= len(context.Instruction.AccountIndexes) {
		return fmt.Errorf("%w: %s account index out of range", structure.ErrInvalidInstruction, name)
	}
	return requireWritableMessageAccount(context, int(context.Instruction.AccountIndexes[instructionAccountIndex]), name)
}

func requireWritableAddress(context runtime.InstructionContext, address structure.PublicKey, name string) error {
	messageAccountIndex, err := accountIndexInMessage(context.Message.AccountKeys, address)
	if err != nil {
		return err
	}
	return requireWritableMessageAccount(context, int(messageAccountIndex), name)
}

func requireWritableMessageAccount(context runtime.InstructionContext, messageAccountIndex int, name string) error {
	if !runtime.IsWritableMessageAccount(messageAccountIndex, context.Message) {
		return fmt.Errorf("%w: %s account must be writable", structure.ErrInvalidInstruction, name)
	}
	return nil
}

func requireTransactionSigner(address structure.PublicKey, context runtime.InstructionContext, name string) error {
	if runtime.IsSignerAddress(address, context.Message) {
		return nil
	}
	return fmt.Errorf("%w: %s must sign", structure.ErrMissingRequiredSignature, name)
}

func stakePoolVMProgramID(context runtime.InstructionContext) (structure.PublicKey, error) {
	if int(context.Instruction.ProgramIDIndex) >= len(context.Message.AccountKeys) {
		return structure.PublicKey{}, fmt.Errorf("%w: vm program id index out of range", structure.ErrInvalidInstruction)
	}
	return context.Message.AccountKeys[context.Instruction.ProgramIDIndex], nil
}

func stakeProgramIDForContext(context runtime.InstructionContext) structure.PublicKey {
	if !context.BuiltinPrograms.Stake.IsZero() {
		return context.BuiltinPrograms.Stake
	}
	return structure.DefaultBuiltinProgramIDs.Stake
}

func stakeBuiltinPrograms(programIDs structure.BuiltinProgramIDs, stakeProgramID structure.PublicKey) structure.BuiltinProgramIDs {
	if programIDs == (structure.BuiltinProgramIDs{}) {
		programIDs = structure.DefaultBuiltinProgramIDs
	}
	if programIDs.Stake.IsZero() {
		programIDs.Stake = stakeProgramID
	}
	return programIDs
}

func readStakePoolPublicKey(reader *borsh.Reader, field string) (structure.PublicKey, error) {
	value, err := reader.ReadFixedBytes(structure.PublicKeySize)
	if err != nil {
		return structure.PublicKey{}, fmt.Errorf("stake pool: decode %s: %w", field, err)
	}
	publicKey, err := structure.NewPublicKey(value)
	if err != nil {
		return structure.PublicKey{}, fmt.Errorf("stake pool: decode %s: %w", field, err)
	}
	return publicKey, nil
}

func checkedAddAssign(target *uint64, value uint64, message string) error {
	nextValue, err := safeAddUint64(*target, value)
	if err != nil {
		return fmt.Errorf("%s: %w", message, err)
	}
	*target = nextValue
	return nil
}

func checkedSubAssign(target *uint64, value uint64, message string) error {
	if value > *target {
		return fmt.Errorf("%s", message)
	}
	*target -= value
	return nil
}

func safeAddUint64(left uint64, right uint64) (uint64, error) {
	if left > ^uint64(0)-right {
		return 0, fmt.Errorf("uint64 addition overflow")
	}
	return left + right, nil
}

func safeMulDivUint64(left uint64, right uint64, divisor uint64) (uint64, error) {
	if divisor == 0 {
		return 0, fmt.Errorf("stake pool: division by zero")
	}
	high, low := bits.Mul64(left, right)
	if high >= divisor {
		return 0, fmt.Errorf("stake pool: multiplication division overflow")
	}
	quotient, _ := bits.Div64(high, low, divisor)
	return quotient, nil
}
