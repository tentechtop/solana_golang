package bpfloader

import (
	"fmt"

	"solana_golang/codec/borsh"
	"solana_golang/runtime"
	"solana_golang/structure"
	svm "solana_golang/vm"
)

const MaxLoaderInstructionBytes = svm.MaxInstructionDataSize

type InstructionType uint32

const (
	InstructionWrite InstructionType = iota
	InstructionFinalize
	InstructionDeploy
	InstructionUpgrade
)

// Program 执行 VM 程序部署指令 + loader 只管理字节码账户生命周期。
type Program struct {
	programID structure.PublicKey
	policy    DeploymentPolicy
}

type DeploymentPolicy struct {
	AllowedDeployers             []structure.PublicKey
	MinDeploymentDepositLamports uint64
	RequireManifest              bool
	AllowUpgradeableContracts    bool
}

// Instruction 描述 BPFLoader 指令 + 支持分片写入和单指令部署。
type Instruction struct {
	Type   InstructionType
	Offset uint32
	Data   []byte
}

// NewProgram 创建 BPFLoader 程序 + 由组合层显式注册到 runtime。
func NewProgram(programID structure.PublicKey) Program {
	return Program{programID: programID}
}

func NewProgramWithPolicy(programID structure.PublicKey, policy DeploymentPolicy) Program {
	return Program{programID: programID, policy: policy.Normalize()}
}

// ProgramID 返回 BPFLoader 程序 ID + runtime 使用该值分发部署指令。
func (program Program) ProgramID() structure.PublicKey {
	return program.programID
}

// Execute 执行部署指令 + 保证只有程序账户签名才能写入或冻结自身代码。
func (program Program) Execute(context runtime.InstructionContext) error {
	instruction, err := UnmarshalInstructionBinary(context.Instruction.Data)
	if err != nil {
		return err
	}
	switch instruction.Type {
	case InstructionWrite:
		return program.executeWrite(instruction, context)
	case InstructionFinalize:
		return program.executeFinalize(context)
	case InstructionDeploy:
		return program.executeDeploy(instruction, context)
	case InstructionUpgrade:
		return program.executeUpgrade(instruction, context)
	default:
		return fmt.Errorf("bpfloader: unsupported instruction type %d", instruction.Type)
	}
}

// NewWriteInstruction 创建分片写入指令 + 支持超过单交易大小的程序分批部署。
func NewWriteInstruction(offset uint32, data []byte) (Instruction, error) {
	instruction := Instruction{Type: InstructionWrite, Offset: offset, Data: cloneBytes(data)}
	return instruction, instruction.Validate()
}

// NewFinalizeInstruction 创建冻结指令 + 字节码校验通过后账户变为 executable。
func NewFinalizeInstruction() Instruction {
	return Instruction{Type: InstructionFinalize}
}

// NewDeployInstruction 创建单指令部署指令 + 小合约可一次写入并冻结。
func NewDeployInstruction(data []byte) (Instruction, error) {
	instruction := Instruction{Type: InstructionDeploy, Data: cloneBytes(data)}
	return instruction, instruction.Validate()
}

func NewUpgradeInstruction(data []byte) (Instruction, error) {
	instruction := Instruction{Type: InstructionUpgrade, Data: cloneBytes(data)}
	return instruction, instruction.Validate()
}

// Validate 校验 loader 指令 + 防止空代码和越界输入进入执行层。
func (instruction Instruction) Validate() error {
	switch instruction.Type {
	case InstructionWrite:
		if len(instruction.Data) == 0 {
			return fmt.Errorf("bpfloader: write data is empty")
		}
		return validateProgramDataLength(instruction.Data)
	case InstructionFinalize:
		return nil
	case InstructionDeploy:
		if len(instruction.Data) == 0 {
			return fmt.Errorf("bpfloader: deploy data is empty")
		}
		return validateProgramDataLength(instruction.Data)
	case InstructionUpgrade:
		if len(instruction.Data) == 0 {
			return fmt.Errorf("bpfloader: upgrade data is empty")
		}
		return validateProgramDataLength(instruction.Data)
	default:
		return fmt.Errorf("bpfloader: invalid instruction type %d", instruction.Type)
	}
}

// MarshalBinary 序列化 loader 指令 + 固定格式供交易签名和执行复算。
func (instruction Instruction) MarshalBinary() ([]byte, error) {
	if err := instruction.Validate(); err != nil {
		return nil, err
	}
	writer := borsh.NewWriter(MaxLoaderInstructionBytes)
	writer.WriteUint32(uint32(instruction.Type))
	writer.WriteUint32(instruction.Offset)
	if err := writer.WriteBytes(instruction.Data); err != nil {
		return nil, fmt.Errorf("bpfloader: encode data: %w", err)
	}
	return writer.Bytes(), nil
}

// UnmarshalInstructionBinary 反序列化 loader 指令 + 解码后继续做边界校验。
func UnmarshalInstructionBinary(data []byte) (Instruction, error) {
	reader := borsh.NewReader(data, MaxLoaderInstructionBytes)
	instructionType, err := reader.ReadUint32()
	if err != nil {
		return Instruction{}, fmt.Errorf("bpfloader: decode instruction type: %w", err)
	}
	offset, err := reader.ReadUint32()
	if err != nil {
		return Instruction{}, fmt.Errorf("bpfloader: decode offset: %w", err)
	}
	payload, err := reader.ReadBytes()
	if err != nil {
		return Instruction{}, fmt.Errorf("bpfloader: decode data: %w", err)
	}
	if err := reader.EnsureEOF(); err != nil {
		return Instruction{}, fmt.Errorf("bpfloader: decode eof: %w", err)
	}
	instruction := Instruction{Type: InstructionType(instructionType), Offset: offset, Data: payload}
	return instruction, instruction.Validate()
}

func (program Program) executeWrite(instruction Instruction, context runtime.InstructionContext) error {
	programAddress, programAccount, err := program.loadWritableProgramAccount(context)
	if err != nil {
		return err
	}
	endOffset := uint64(instruction.Offset) + uint64(len(instruction.Data))
	if endOffset > uint64(len(programAccount.Data)) {
		return fmt.Errorf("bpfloader: write range exceeds program account data")
	}
	copy(programAccount.Data[instruction.Offset:endOffset], instruction.Data)
	context.Accounts[programAddress] = programAccount
	return nil
}

func (program Program) executeFinalize(context runtime.InstructionContext) error {
	programAddress, programAccount, err := program.loadWritableProgramAccount(context)
	if err != nil {
		return err
	}
	if err := program.validateDeployment(context, programAccount, programAccount.Data); err != nil {
		return err
	}
	programAccount.Executable = true
	if err := programAccount.ValidateWithRent(context.RentConfig); err != nil {
		return fmt.Errorf("bpfloader: validate executable account: %w", err)
	}
	context.Accounts[programAddress] = programAccount
	return nil
}

func (program Program) executeDeploy(instruction Instruction, context runtime.InstructionContext) error {
	programAddress, programAccount, err := program.loadWritableProgramAccount(context)
	if err != nil {
		return err
	}
	if err := program.validateDeployment(context, programAccount, instruction.Data); err != nil {
		return err
	}
	if err := programAccount.SetData(instruction.Data, context.RentConfig); err != nil {
		return fmt.Errorf("bpfloader: write program data: %w", err)
	}
	programAccount.Executable = true
	if err := programAccount.ValidateWithRent(context.RentConfig); err != nil {
		return fmt.Errorf("bpfloader: validate executable account: %w", err)
	}
	context.Accounts[programAddress] = programAccount
	return nil
}

func (program Program) executeUpgrade(instruction Instruction, context runtime.InstructionContext) error {
	programAddress, programAccount, err := program.loadWritableExecutableProgramAccount(context)
	if err != nil {
		return err
	}
	currentManifest, ok, err := svm.DecodeProgramManifest(programAccount.Data)
	if err != nil {
		return fmt.Errorf("bpfloader: decode current manifest: %w", err)
	}
	if !ok {
		return fmt.Errorf("%w: executable program is immutable without manifest", structure.ErrInvalidInstruction)
	}
	if currentManifest.UpgradeAuthority == (svm.Address{}) {
		return fmt.Errorf("%w: executable program has no upgrade authority", structure.ErrInvalidInstruction)
	}
	if !program.isVMAddressSigner(currentManifest.UpgradeAuthority, context) {
		return fmt.Errorf("%w: upgrade authority must sign", structure.ErrMissingRequiredSignature)
	}
	if err := validateSVMProgramData(instruction.Data, program.programID); err != nil {
		return err
	}
	nextManifest, ok, err := svm.DecodeProgramManifest(instruction.Data)
	if err != nil {
		return fmt.Errorf("bpfloader: decode next manifest: %w", err)
	}
	if !ok {
		return fmt.Errorf("%w: upgraded program must keep manifest", structure.ErrInvalidInstruction)
	}
	if nextManifest.UpgradeAuthority != (svm.Address{}) && nextManifest.UpgradeAuthority != currentManifest.UpgradeAuthority {
		return fmt.Errorf("%w: upgrade cannot rotate authority", structure.ErrInvalidInstruction)
	}
	if err := programAccount.SetData(instruction.Data, context.RentConfig); err != nil {
		return fmt.Errorf("bpfloader: write upgraded program data: %w", err)
	}
	programAccount.Executable = true
	if err := programAccount.ValidateWithRent(context.RentConfig); err != nil {
		return fmt.Errorf("bpfloader: validate upgraded executable account: %w", err)
	}
	context.Accounts[programAddress] = programAccount
	return nil
}

func (program Program) loadWritableProgramAccount(context runtime.InstructionContext) (structure.PublicKey, structure.Account, error) {
	if len(context.Instruction.AccountIndexes) < 1 {
		return structure.PublicKey{}, structure.Account{}, fmt.Errorf("bpfloader: instruction requires program account")
	}
	programAddress := context.Message.AccountKeys[context.Instruction.AccountIndexes[0]]
	if !runtime.IsSignerAddress(programAddress, context.Message) {
		return structure.PublicKey{}, structure.Account{}, fmt.Errorf("%w: program account must sign", structure.ErrMissingRequiredSignature)
	}
	if !runtime.IsWritableMessageAccount(int(context.Instruction.AccountIndexes[0]), context.Message) {
		return structure.PublicKey{}, structure.Account{}, fmt.Errorf("%w: program account must be writable", structure.ErrInvalidInstruction)
	}
	account, exists := context.Accounts[programAddress]
	if !exists {
		return structure.PublicKey{}, structure.Account{}, fmt.Errorf("%w: program account not found", structure.ErrInvalidLoadedTransaction)
	}
	if account.Owner != program.programID {
		return structure.PublicKey{}, structure.Account{}, fmt.Errorf("%w: program account owner must be bpf loader", structure.ErrInvalidInstruction)
	}
	if account.Executable {
		return structure.PublicKey{}, structure.Account{}, fmt.Errorf("%w: program account already executable", structure.ErrInvalidInstruction)
	}
	return programAddress, account.Clone(), nil
}

func (program Program) loadWritableExecutableProgramAccount(context runtime.InstructionContext) (structure.PublicKey, structure.Account, error) {
	if len(context.Instruction.AccountIndexes) < 1 {
		return structure.PublicKey{}, structure.Account{}, fmt.Errorf("bpfloader: instruction requires program account")
	}
	programAddress := context.Message.AccountKeys[context.Instruction.AccountIndexes[0]]
	if !runtime.IsWritableMessageAccount(int(context.Instruction.AccountIndexes[0]), context.Message) {
		return structure.PublicKey{}, structure.Account{}, fmt.Errorf("%w: program account must be writable", structure.ErrInvalidInstruction)
	}
	account, exists := context.Accounts[programAddress]
	if !exists {
		return structure.PublicKey{}, structure.Account{}, fmt.Errorf("%w: program account not found", structure.ErrInvalidLoadedTransaction)
	}
	if account.Owner != program.programID {
		return structure.PublicKey{}, structure.Account{}, fmt.Errorf("%w: program account owner must be bpf loader", structure.ErrInvalidInstruction)
	}
	if !account.Executable {
		return structure.PublicKey{}, structure.Account{}, fmt.Errorf("%w: program account is not executable", structure.ErrInvalidInstruction)
	}
	return programAddress, account.Clone(), nil
}

func (program Program) validateDeployment(context runtime.InstructionContext, programAccount structure.Account, data []byte) error {
	if err := validateSVMProgramData(data, program.programID); err != nil {
		return err
	}
	policy := program.policy.Normalize()
	if policy.RequireManifest {
		if _, ok, err := svm.DecodeProgramManifest(data); err != nil {
			return fmt.Errorf("bpfloader: decode deployment manifest: %w", err)
		} else if !ok {
			return fmt.Errorf("%w: manifest is required", structure.ErrInvalidInstruction)
		}
	}
	manifest, hasManifest, err := svm.DecodeProgramManifest(data)
	if err != nil {
		return fmt.Errorf("bpfloader: decode deployment manifest: %w", err)
	}
	if hasManifest && manifest.UpgradeAuthority != (svm.Address{}) {
		if !policy.AllowUpgradeableContracts {
			return fmt.Errorf("%w: upgradeable contracts are disabled", structure.ErrInvalidInstruction)
		}
		if !program.isVMAddressSigner(manifest.UpgradeAuthority, context) {
			return fmt.Errorf("%w: upgrade authority must sign", structure.ErrMissingRequiredSignature)
		}
	}
	if program.isDeploymentAllowedByPolicy(context, programAccount, len(data)) {
		return nil
	}
	return fmt.Errorf("%w: deployer is not whitelisted and deposit is insufficient", structure.ErrInvalidInstruction)
}

func (program Program) isDeploymentAllowedByPolicy(context runtime.InstructionContext, programAccount structure.Account, dataLength int) bool {
	policy := program.policy.Normalize()
	if len(policy.AllowedDeployers) == 0 && policy.MinDeploymentDepositLamports == 0 {
		return true
	}
	if policy.isAllowedDeployer(feePayerAddress(context)) {
		return true
	}
	if policy.MinDeploymentDepositLamports == 0 {
		return false
	}
	minimumBalance, err := context.RentConfig.MinimumBalance(dataLength)
	if err != nil {
		return false
	}
	if programAccount.Lamports < minimumBalance {
		return false
	}
	return programAccount.Lamports-minimumBalance >= policy.MinDeploymentDepositLamports
}

func (program Program) isVMAddressSigner(address svm.Address, context runtime.InstructionContext) bool {
	publicKey := publicKeyFromVMAddress(address)
	return runtime.IsSignerAddress(publicKey, context.Message)
}

func (policy DeploymentPolicy) Normalize() DeploymentPolicy {
	if policy.AllowedDeployers == nil {
		return policy
	}
	normalized := policy
	normalized.AllowedDeployers = make([]structure.PublicKey, 0, len(policy.AllowedDeployers))
	seen := make(map[structure.PublicKey]struct{}, len(policy.AllowedDeployers))
	for _, deployer := range policy.AllowedDeployers {
		if deployer.IsZero() {
			continue
		}
		if _, exists := seen[deployer]; exists {
			continue
		}
		seen[deployer] = struct{}{}
		normalized.AllowedDeployers = append(normalized.AllowedDeployers, deployer)
	}
	return normalized
}

func (policy DeploymentPolicy) isAllowedDeployer(address structure.PublicKey) bool {
	if address.IsZero() {
		return false
	}
	for _, deployer := range policy.AllowedDeployers {
		if deployer == address {
			return true
		}
	}
	return false
}

func feePayerAddress(context runtime.InstructionContext) structure.PublicKey {
	if len(context.Message.AccountKeys) == 0 {
		return structure.PublicKey{}
	}
	return context.Message.AccountKeys[0]
}

func validateSVMProgramData(data []byte, loaderProgramID structure.PublicKey) error {
	if err := validateProgramDataLength(data); err != nil {
		return err
	}
	programAccount := svm.ProgramAccount{
		Owner:      vmAddressFromPublicKey(loaderProgramID),
		Executable: true,
		Data:       cloneBytes(data),
	}
	if _, err := (svm.BytecodeLoader{}).Load(programAccount, vmAddressFromPublicKey(loaderProgramID)); err != nil {
		return fmt.Errorf("bpfloader: validate svm bytecode: %w", err)
	}
	return nil
}

func validateProgramDataLength(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("bpfloader: program data is empty")
	}
	if len(data) > svm.MaxProgramDataSize {
		return fmt.Errorf("bpfloader: program data length %d exceeds %d", len(data), svm.MaxProgramDataSize)
	}
	return nil
}

func vmAddressFromPublicKey(publicKey structure.PublicKey) svm.Address {
	var address svm.Address
	copy(address[:], publicKey[:])
	return address
}

func publicKeyFromVMAddress(address svm.Address) structure.PublicKey {
	var publicKey structure.PublicKey
	copy(publicKey[:], address[:])
	return publicKey
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}
