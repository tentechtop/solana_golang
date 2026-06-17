package structure

import (
	"fmt"

	"solana_golang/codec/borsh"
	"solana_golang/utils"
	"solana_golang/zk"
)

const (
	PrivacyStateVersion            = uint16(1)
	MaxPrivacyNotesPerState        = 1024
	MaxPrivacyInstructionBytes     = 4096
	MaxPrivacyEncryptedNoteBytes   = 512
	MaxPrivacyAuditRecordsPerNote  = 8
	MaxPrivacyAuditCiphertextBytes = 512
	MaxPrivacyProofBytes           = zk.DefaultMaxProofBytes
	privacySpendProofDomainV1      = "solana_golang.privacy.spend.v1"
)

type PrivacyInstructionType uint32

const (
	PrivacyInstructionDeposit PrivacyInstructionType = iota
	PrivacyInstructionWithdraw
	PrivacyInstructionTransfer
	PrivacyInstructionAuthorizeAudit
)

type PrivacyAuditScope uint8

const (
	PrivacyAuditScopeOwner PrivacyAuditScope = iota + 1
	PrivacyAuditScopeBusiness
	PrivacyAuditScopeRegulatory
)

// PrivacyInstruction 描述隐私固定指令 + 预留 VM 版本和证明字段便于未来替换验证器。
type PrivacyInstruction struct {
	Type           PrivacyInstructionType
	VMVersion      uint16
	Proof          []byte
	Deposit        *PrivacyDepositParams
	Withdraw       *PrivacyWithdrawParams
	Transfer       *PrivacyTransferParams
	AuthorizeAudit *PrivacyAuthorizeAuditParams
}

// PrivacyDepositParams 描述透明转隐私参数 + 用 commitment 和密文 note 表达隐私收款。
type PrivacyDepositParams struct {
	Amount         uint64
	Commitment     Hash
	SpendAuthority PublicKey
	EncryptedNote  []byte
	AuditRecords   []PrivacyAuditRecord
}

// PrivacyWithdrawParams 描述隐私转透明参数 + 可选找零 note 支持部分花费。
type PrivacyWithdrawParams struct {
	Amount               uint64
	SourceCommitment     Hash
	Nullifier            Hash
	AuditRecords         []PrivacyAuditRecord
	ChangeAmount         uint64
	ChangeCommitment     Hash
	ChangeSpendAuthority PublicKey
	ChangeEncryptedNote  []byte
	ChangeAuditRecords   []PrivacyAuditRecord
}

// PrivacyTransferParams 描述隐私转隐私参数 + 支持输出 note 和找零 note 原子创建。
type PrivacyTransferParams struct {
	Amount               uint64
	SourceCommitment     Hash
	Nullifier            Hash
	OutputCommitment     Hash
	OutputSpendAuthority PublicKey
	OutputEncryptedNote  []byte
	OutputAuditRecords   []PrivacyAuditRecord
	ChangeAmount         uint64
	ChangeCommitment     Hash
	ChangeSpendAuthority PublicKey
	ChangeEncryptedNote  []byte
	ChangeAuditRecords   []PrivacyAuditRecord
}

// PrivacyAuthorizeAuditParams 描述审计授权参数 + 后续补充授权不需要改动隐私余额。
type PrivacyAuthorizeAuditParams struct {
	Commitment      Hash
	Auditor         PublicKey
	Scope           PrivacyAuditScope
	ExpiresAtSlot   uint64
	AuditCiphertext []byte
}

// PrivacyAuditRecord 描述授权审计记录 + 链上只保存授权范围和审计密文。
type PrivacyAuditRecord struct {
	Auditor         PublicKey
	Scope           PrivacyAuditScope
	ExpiresAtSlot   uint64
	AuditCiphertext []byte
}

// PrivacyNoteRecord 描述隐私状态记录 + 当前模拟器保存固定指令执行需要的最小账本事实。
type PrivacyNoteRecord struct {
	Commitment     Hash
	SpendAuthority PublicKey
	Amount         uint64
	Spent          bool
	SpentSlot      uint64
	SpendNullifier Hash
	VMVersion      uint16
	EncryptedNote  []byte
	AuditRecords   []PrivacyAuditRecord
}

// PrivacyState 描述隐私程序状态 + 用 note 集合和 nullifier 集合支撑固定指令闭环。
type PrivacyState struct {
	Version         uint16
	Notes           []PrivacyNoteRecord
	SpentNullifiers []Hash
}

type privacyAuditRecordKey struct {
	Auditor        PublicKey
	Scope          PrivacyAuditScope
	CiphertextHash Hash
}

// NewPrivacyDepositInstruction 创建透明转隐私指令 + 统一执行参数校验。
func NewPrivacyDepositInstruction(vmVersion uint16, proof []byte, params PrivacyDepositParams) (PrivacyInstruction, error) {
	clonedParams := params
	clonedParams.EncryptedNote = utils.CloneBytes(params.EncryptedNote)
	clonedParams.AuditRecords = clonePrivacyAuditRecords(params.AuditRecords)
	instruction := PrivacyInstruction{Type: PrivacyInstructionDeposit, VMVersion: vmVersion, Proof: utils.CloneBytes(proof), Deposit: &clonedParams}
	return instruction, instruction.Validate()
}

// NewPrivacyWithdrawInstruction 创建隐私转透明指令 + 统一执行参数校验。
func NewPrivacyWithdrawInstruction(vmVersion uint16, proof []byte, params PrivacyWithdrawParams) (PrivacyInstruction, error) {
	clonedParams := params
	clonedParams.AuditRecords = clonePrivacyAuditRecords(params.AuditRecords)
	clonedParams.ChangeEncryptedNote = utils.CloneBytes(params.ChangeEncryptedNote)
	clonedParams.ChangeAuditRecords = clonePrivacyAuditRecords(params.ChangeAuditRecords)
	instruction := PrivacyInstruction{Type: PrivacyInstructionWithdraw, VMVersion: vmVersion, Proof: utils.CloneBytes(proof), Withdraw: &clonedParams}
	return instruction, instruction.Validate()
}

// NewPrivacyTransferInstruction 创建隐私转隐私指令 + 统一执行参数校验。
func NewPrivacyTransferInstruction(vmVersion uint16, proof []byte, params PrivacyTransferParams) (PrivacyInstruction, error) {
	clonedParams := params
	clonedParams.OutputEncryptedNote = utils.CloneBytes(params.OutputEncryptedNote)
	clonedParams.OutputAuditRecords = clonePrivacyAuditRecords(params.OutputAuditRecords)
	clonedParams.ChangeEncryptedNote = utils.CloneBytes(params.ChangeEncryptedNote)
	clonedParams.ChangeAuditRecords = clonePrivacyAuditRecords(params.ChangeAuditRecords)
	instruction := PrivacyInstruction{Type: PrivacyInstructionTransfer, VMVersion: vmVersion, Proof: utils.CloneBytes(proof), Transfer: &clonedParams}
	return instruction, instruction.Validate()
}

// NewPrivacyAuthorizeAuditInstruction 创建审计授权指令 + 允许用户后续授权监管或审计方。
func NewPrivacyAuthorizeAuditInstruction(vmVersion uint16, proof []byte, params PrivacyAuthorizeAuditParams) (PrivacyInstruction, error) {
	clonedParams := params
	clonedParams.AuditCiphertext = utils.CloneBytes(params.AuditCiphertext)
	instruction := PrivacyInstruction{Type: PrivacyInstructionAuthorizeAudit, VMVersion: vmVersion, Proof: utils.CloneBytes(proof), AuthorizeAudit: &clonedParams}
	return instruction, instruction.Validate()
}

// BuildPrivacyWithdrawProofMessage 构造隐私提现证明消息 + 绑定全部花费语义防止 proof 重放。
func BuildPrivacyWithdrawProofMessage(vmVersion uint16, stateAddress PublicKey, destinationAddress PublicKey, params PrivacyWithdrawParams, currentSlot uint64) ([]byte, error) {
	if err := validatePrivacyProofMessageHeader(vmVersion, stateAddress, currentSlot); err != nil {
		return nil, err
	}
	if destinationAddress.IsZero() {
		return nil, fmt.Errorf("%w: withdraw destination is zero", ErrInvalidPrivacyInstruction)
	}
	if err := validatePrivacyWithdrawParams(&params); err != nil {
		return nil, err
	}
	auditRecordsHash, err := hashPrivacyAuditRecords(params.AuditRecords)
	if err != nil {
		return nil, err
	}
	changeEncryptedNoteHash, changeAuditRecordsHash, err := privacyChangeOutputHashes(
		params.ChangeAmount,
		params.ChangeEncryptedNote,
		params.ChangeAuditRecords,
	)
	if err != nil {
		return nil, err
	}

	writer := newPrivacyProofMessageWriter(vmVersion, PrivacyInstructionWithdraw, currentSlot, stateAddress)
	writer.WriteFixedBytes(destinationAddress[:])
	writer.WriteUint64(params.Amount)
	writer.WriteFixedBytes(params.SourceCommitment[:])
	writer.WriteFixedBytes(params.Nullifier[:])
	writer.WriteFixedBytes(auditRecordsHash[:])
	writer.WriteUint64(params.ChangeAmount)
	writer.WriteFixedBytes(params.ChangeCommitment[:])
	writer.WriteFixedBytes(params.ChangeSpendAuthority[:])
	writer.WriteFixedBytes(changeEncryptedNoteHash[:])
	writer.WriteFixedBytes(changeAuditRecordsHash[:])
	return writer.Bytes(), nil
}

// BuildPrivacyTransferProofMessage 构造隐私转隐私证明消息 + 默认绑定同一状态账户保持兼容调用。
func BuildPrivacyTransferProofMessage(vmVersion uint16, stateAddress PublicKey, params PrivacyTransferParams, currentSlot uint64) ([]byte, error) {
	return BuildPrivacyTransferProofMessageWithOutputState(vmVersion, stateAddress, stateAddress, params, currentSlot)
}

// BuildPrivacyTransferProofMessageWithOutputState 构造隐私转隐私证明消息 + 绑定输出状态账户防止 note 被重定向。
func BuildPrivacyTransferProofMessageWithOutputState(vmVersion uint16, stateAddress PublicKey, outputStateAddress PublicKey, params PrivacyTransferParams, currentSlot uint64) ([]byte, error) {
	if err := validatePrivacyProofMessageHeader(vmVersion, stateAddress, currentSlot); err != nil {
		return nil, err
	}
	if outputStateAddress.IsZero() {
		return nil, fmt.Errorf("%w: output privacy state address is zero", ErrInvalidPrivacyInstruction)
	}
	if err := validatePrivacyTransferParams(&params); err != nil {
		return nil, err
	}
	encryptedNoteHash, err := NewHash(utils.SHA256(params.OutputEncryptedNote))
	if err != nil {
		return nil, err
	}
	auditRecordsHash, err := hashPrivacyAuditRecords(params.OutputAuditRecords)
	if err != nil {
		return nil, err
	}
	changeEncryptedNoteHash, changeAuditRecordsHash, err := privacyChangeOutputHashes(
		params.ChangeAmount,
		params.ChangeEncryptedNote,
		params.ChangeAuditRecords,
	)
	if err != nil {
		return nil, err
	}

	writer := newPrivacyProofMessageWriter(vmVersion, PrivacyInstructionTransfer, currentSlot, stateAddress)
	writer.WriteFixedBytes(outputStateAddress[:])
	writer.WriteUint64(params.Amount)
	writer.WriteFixedBytes(params.SourceCommitment[:])
	writer.WriteFixedBytes(params.Nullifier[:])
	writer.WriteFixedBytes(params.OutputCommitment[:])
	writer.WriteFixedBytes(params.OutputSpendAuthority[:])
	writer.WriteFixedBytes(encryptedNoteHash[:])
	writer.WriteFixedBytes(auditRecordsHash[:])
	writer.WriteUint64(params.ChangeAmount)
	writer.WriteFixedBytes(params.ChangeCommitment[:])
	writer.WriteFixedBytes(params.ChangeSpendAuthority[:])
	writer.WriteFixedBytes(changeEncryptedNoteHash[:])
	writer.WriteFixedBytes(changeAuditRecordsHash[:])
	return writer.Bytes(), nil
}

// Validate 校验隐私指令 + 防止金额为零、证明过大和 oneof 参数缺失。
func (instruction PrivacyInstruction) Validate() error {
	if err := zk.ValidateProtocolVersion(instruction.VMVersion); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidPrivacyInstruction, err)
	}
	if err := zk.ValidateOptionalProofBytes(instruction.Proof, MaxPrivacyProofBytes); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidPrivacyInstruction, err)
	}
	switch instruction.Type {
	case PrivacyInstructionDeposit:
		return validatePrivacyDepositParams(instruction.Deposit)
	case PrivacyInstructionWithdraw:
		return validatePrivacyWithdrawParams(instruction.Withdraw)
	case PrivacyInstructionTransfer:
		return validatePrivacyTransferParams(instruction.Transfer)
	case PrivacyInstructionAuthorizeAudit:
		return validatePrivacyAuthorizeAuditParams(instruction.AuthorizeAudit)
	default:
		return fmt.Errorf("%w: unsupported type %d", ErrInvalidPrivacyInstruction, instruction.Type)
	}
}

// MarshalBinary 序列化隐私指令 + 固定格式便于交易签名和未来 VM 兼容。
func (instruction PrivacyInstruction) MarshalBinary() ([]byte, error) {
	if err := instruction.Validate(); err != nil {
		return nil, err
	}

	writer := borsh.NewWriter(MaxPrivacyInstructionBytes)
	writer.WriteUint32(uint32(instruction.Type))
	writer.WriteUint16(instruction.VMVersion)
	if err := writer.WriteBytes(instruction.Proof); err != nil {
		return nil, fmt.Errorf("structure: encode privacy proof: %w", err)
	}
	if err := writePrivacyInstructionBody(writer, instruction); err != nil {
		return nil, fmt.Errorf("structure: encode privacy instruction body: %w", err)
	}
	return writer.Bytes(), nil
}

// UnmarshalPrivacyInstructionBinary 反序列化隐私指令 + 拒绝尾部污染字节。
func UnmarshalPrivacyInstructionBinary(data []byte) (PrivacyInstruction, error) {
	reader := borsh.NewReader(data, MaxPrivacyInstructionBytes)
	instructionType, err := reader.ReadUint32()
	if err != nil {
		return PrivacyInstruction{}, fmt.Errorf("structure: decode privacy instruction type: %w", err)
	}
	vmVersion, err := reader.ReadUint16()
	if err != nil {
		return PrivacyInstruction{}, fmt.Errorf("structure: decode privacy vm version: %w", err)
	}
	proof, err := reader.ReadBytes()
	if err != nil {
		return PrivacyInstruction{}, fmt.Errorf("structure: decode privacy proof: %w", err)
	}

	instruction, err := readPrivacyInstructionBody(reader, PrivacyInstructionType(instructionType), vmVersion, proof)
	if err != nil {
		return PrivacyInstruction{}, err
	}
	if err := reader.EnsureEOF(); err != nil {
		return PrivacyInstruction{}, fmt.Errorf("structure: decode privacy instruction eof: %w", err)
	}
	return instruction, instruction.Validate()
}

// MarshalBinary 序列化隐私状态 + 使用 Borsh 保证状态字节确定性。
func (state PrivacyState) MarshalBinary() ([]byte, error) {
	if err := state.Validate(); err != nil {
		return nil, err
	}

	writer := borsh.NewWriter(MaxAccountDataSize)
	writer.WriteUint16(state.Version)
	writer.WriteUint32(uint32(len(state.Notes)))
	for _, note := range state.Notes {
		writer.WriteFixedBytes(note.Commitment[:])
		writer.WriteFixedBytes(note.SpendAuthority[:])
		writer.WriteUint64(note.Amount)
		writer.WriteBool(note.Spent)
		writer.WriteUint64(note.SpentSlot)
		writer.WriteFixedBytes(note.SpendNullifier[:])
		writer.WriteUint16(note.VMVersion)
		if err := writer.WriteBytes(note.EncryptedNote); err != nil {
			return nil, fmt.Errorf("structure: encode privacy note: %w", err)
		}
		if err := writePrivacyAuditRecords(writer, note.AuditRecords); err != nil {
			return nil, fmt.Errorf("structure: encode privacy audit records: %w", err)
		}
	}
	writer.WriteUint32(uint32(len(state.SpentNullifiers)))
	for _, nullifier := range state.SpentNullifiers {
		writer.WriteFixedBytes(nullifier[:])
	}
	return writer.Bytes(), nil
}

// UnmarshalPrivacyStateBinary 反序列化隐私状态 + 空账户和预分配零数据都按版本一初始化。
func UnmarshalPrivacyStateBinary(data []byte) (PrivacyState, error) {
	if len(data) == 0 || isZeroFilledPrivacyStateData(data) {
		return PrivacyState{Version: PrivacyStateVersion}, nil
	}

	reader := borsh.NewReader(data, MaxAccountDataSize)
	version, err := reader.ReadUint16()
	if err != nil {
		return PrivacyState{}, fmt.Errorf("structure: decode privacy state version: %w", err)
	}
	state := PrivacyState{Version: version}
	if state.Notes, err = readPrivacyNotes(reader); err != nil {
		return PrivacyState{}, err
	}
	if state.SpentNullifiers, err = readPrivacyNullifiers(reader); err != nil {
		return PrivacyState{}, err
	}
	if err := reader.EnsureEOF(); err != nil {
		return PrivacyState{}, fmt.Errorf("structure: decode privacy state eof: %w", err)
	}
	return state, state.Validate()
}

func isZeroFilledPrivacyStateData(data []byte) bool {
	for _, value := range data {
		if value != 0 {
			return false
		}
	}
	return true
}

// Validate 校验隐私状态 + 防止重复 commitment 和重复 nullifier。
func (state PrivacyState) Validate() error {
	if state.Version != PrivacyStateVersion {
		return fmt.Errorf("%w: unsupported state version %d", ErrInvalidPrivacyInstruction, state.Version)
	}
	if len(state.Notes) > MaxPrivacyNotesPerState {
		return fmt.Errorf("%w: note count %d exceeds %d", ErrInvalidPrivacyInstruction, len(state.Notes), MaxPrivacyNotesPerState)
	}
	return validatePrivacyStateUniqueness(state)
}

func validatePrivacyProofMessageHeader(vmVersion uint16, stateAddress PublicKey, currentSlot uint64) error {
	if err := zk.ValidateProtocolVersion(vmVersion); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidPrivacyInstruction, err)
	}
	if stateAddress.IsZero() {
		return fmt.Errorf("%w: privacy state address is zero", ErrInvalidPrivacyInstruction)
	}
	if currentSlot == 0 {
		return fmt.Errorf("%w: proof slot cannot be zero", ErrInvalidPrivacyInstruction)
	}
	return nil
}

func newPrivacyProofMessageWriter(vmVersion uint16, instructionType PrivacyInstructionType, currentSlot uint64, stateAddress PublicKey) *borsh.Writer {
	writer := borsh.NewWriter(MaxPrivacyInstructionBytes)
	writer.WriteFixedBytes([]byte(privacySpendProofDomainV1))
	writer.WriteUint16(vmVersion)
	writer.WriteUint32(uint32(instructionType))
	writer.WriteUint64(currentSlot)
	writer.WriteFixedBytes(stateAddress[:])
	return writer
}

func hashPrivacyAuditRecords(records []PrivacyAuditRecord) (Hash, error) {
	writer := borsh.NewWriter(MaxPrivacyInstructionBytes)
	if err := writePrivacyAuditRecords(writer, records); err != nil {
		return Hash{}, err
	}
	return NewHash(utils.SHA256(writer.Bytes()))
}

func writePrivacyInstructionBody(writer *borsh.Writer, instruction PrivacyInstruction) error {
	switch instruction.Type {
	case PrivacyInstructionDeposit:
		writer.WriteUint64(instruction.Deposit.Amount)
		writer.WriteFixedBytes(instruction.Deposit.Commitment[:])
		writer.WriteFixedBytes(instruction.Deposit.SpendAuthority[:])
		if err := writer.WriteBytes(instruction.Deposit.EncryptedNote); err != nil {
			return err
		}
		return writePrivacyAuditRecords(writer, instruction.Deposit.AuditRecords)
	case PrivacyInstructionWithdraw:
		writer.WriteUint64(instruction.Withdraw.Amount)
		writer.WriteFixedBytes(instruction.Withdraw.SourceCommitment[:])
		writer.WriteFixedBytes(instruction.Withdraw.Nullifier[:])
		if err := writePrivacyAuditRecords(writer, instruction.Withdraw.AuditRecords); err != nil {
			return err
		}
		return writePrivacyChangeOutput(writer,
			instruction.Withdraw.ChangeAmount,
			instruction.Withdraw.ChangeCommitment,
			instruction.Withdraw.ChangeSpendAuthority,
			instruction.Withdraw.ChangeEncryptedNote,
			instruction.Withdraw.ChangeAuditRecords,
		)
	case PrivacyInstructionTransfer:
		writer.WriteUint64(instruction.Transfer.Amount)
		writer.WriteFixedBytes(instruction.Transfer.SourceCommitment[:])
		writer.WriteFixedBytes(instruction.Transfer.Nullifier[:])
		writer.WriteFixedBytes(instruction.Transfer.OutputCommitment[:])
		writer.WriteFixedBytes(instruction.Transfer.OutputSpendAuthority[:])
		if err := writer.WriteBytes(instruction.Transfer.OutputEncryptedNote); err != nil {
			return err
		}
		if err := writePrivacyAuditRecords(writer, instruction.Transfer.OutputAuditRecords); err != nil {
			return err
		}
		return writePrivacyChangeOutput(writer,
			instruction.Transfer.ChangeAmount,
			instruction.Transfer.ChangeCommitment,
			instruction.Transfer.ChangeSpendAuthority,
			instruction.Transfer.ChangeEncryptedNote,
			instruction.Transfer.ChangeAuditRecords,
		)
	case PrivacyInstructionAuthorizeAudit:
		writer.WriteFixedBytes(instruction.AuthorizeAudit.Commitment[:])
		writer.WriteFixedBytes(instruction.AuthorizeAudit.Auditor[:])
		writer.WriteUint8(uint8(instruction.AuthorizeAudit.Scope))
		writer.WriteUint64(instruction.AuthorizeAudit.ExpiresAtSlot)
		return writer.WriteBytes(instruction.AuthorizeAudit.AuditCiphertext)
	}
	return nil
}

func readPrivacyInstructionBody(reader *borsh.Reader, instructionType PrivacyInstructionType, vmVersion uint16, proof []byte) (PrivacyInstruction, error) {
	switch instructionType {
	case PrivacyInstructionDeposit:
		return readPrivacyDepositInstruction(reader, vmVersion, proof)
	case PrivacyInstructionWithdraw:
		return readPrivacyWithdrawInstruction(reader, vmVersion, proof)
	case PrivacyInstructionTransfer:
		return readPrivacyTransferInstruction(reader, vmVersion, proof)
	case PrivacyInstructionAuthorizeAudit:
		return readPrivacyAuthorizeAuditInstruction(reader, vmVersion, proof)
	default:
		return PrivacyInstruction{}, fmt.Errorf("%w: unsupported type %d", ErrInvalidPrivacyInstruction, instructionType)
	}
}

func readPrivacyDepositInstruction(reader *borsh.Reader, vmVersion uint16, proof []byte) (PrivacyInstruction, error) {
	amount, err := reader.ReadUint64()
	if err != nil {
		return PrivacyInstruction{}, fmt.Errorf("structure: decode privacy deposit amount: %w", err)
	}
	commitment, err := readPrivacyHash(reader, "deposit commitment")
	if err != nil {
		return PrivacyInstruction{}, err
	}
	spendAuthority, err := readPrivacyPublicKey(reader, "deposit spend authority")
	if err != nil {
		return PrivacyInstruction{}, err
	}
	encryptedNote, err := reader.ReadBytes()
	if err != nil {
		return PrivacyInstruction{}, fmt.Errorf("structure: decode privacy deposit encrypted note: %w", err)
	}
	auditRecords, err := readPrivacyAuditRecords(reader)
	if err != nil {
		return PrivacyInstruction{}, err
	}
	return NewPrivacyDepositInstruction(vmVersion, proof, PrivacyDepositParams{Amount: amount, Commitment: commitment, SpendAuthority: spendAuthority, EncryptedNote: encryptedNote, AuditRecords: auditRecords})
}

func readPrivacyWithdrawInstruction(reader *borsh.Reader, vmVersion uint16, proof []byte) (PrivacyInstruction, error) {
	amount, err := reader.ReadUint64()
	if err != nil {
		return PrivacyInstruction{}, fmt.Errorf("structure: decode privacy withdraw amount: %w", err)
	}
	sourceCommitment, err := readPrivacyHash(reader, "withdraw source commitment")
	if err != nil {
		return PrivacyInstruction{}, err
	}
	nullifier, err := readPrivacyHash(reader, "withdraw nullifier")
	if err != nil {
		return PrivacyInstruction{}, err
	}
	auditRecords, err := readPrivacyAuditRecords(reader)
	if err != nil {
		return PrivacyInstruction{}, err
	}
	changeAmount, changeCommitment, changeSpendAuthority, changeEncryptedNote, changeAuditRecords, err := readPrivacyChangeOutput(reader, "withdraw change")
	if err != nil {
		return PrivacyInstruction{}, err
	}
	return NewPrivacyWithdrawInstruction(vmVersion, proof, PrivacyWithdrawParams{
		Amount:               amount,
		SourceCommitment:     sourceCommitment,
		Nullifier:            nullifier,
		AuditRecords:         auditRecords,
		ChangeAmount:         changeAmount,
		ChangeCommitment:     changeCommitment,
		ChangeSpendAuthority: changeSpendAuthority,
		ChangeEncryptedNote:  changeEncryptedNote,
		ChangeAuditRecords:   changeAuditRecords,
	})
}

func readPrivacyTransferInstruction(reader *borsh.Reader, vmVersion uint16, proof []byte) (PrivacyInstruction, error) {
	amount, err := reader.ReadUint64()
	if err != nil {
		return PrivacyInstruction{}, fmt.Errorf("structure: decode privacy transfer amount: %w", err)
	}
	sourceCommitment, err := readPrivacyHash(reader, "transfer source commitment")
	if err != nil {
		return PrivacyInstruction{}, err
	}
	nullifier, err := readPrivacyHash(reader, "transfer nullifier")
	if err != nil {
		return PrivacyInstruction{}, err
	}
	outputCommitment, err := readPrivacyHash(reader, "transfer output commitment")
	if err != nil {
		return PrivacyInstruction{}, err
	}
	outputSpendAuthority, err := readPrivacyPublicKey(reader, "transfer output spend authority")
	if err != nil {
		return PrivacyInstruction{}, err
	}
	encryptedNote, err := reader.ReadBytes()
	if err != nil {
		return PrivacyInstruction{}, fmt.Errorf("structure: decode privacy transfer encrypted note: %w", err)
	}
	auditRecords, err := readPrivacyAuditRecords(reader)
	if err != nil {
		return PrivacyInstruction{}, err
	}
	changeAmount, changeCommitment, changeSpendAuthority, changeEncryptedNote, changeAuditRecords, err := readPrivacyChangeOutput(reader, "transfer change")
	if err != nil {
		return PrivacyInstruction{}, err
	}
	return NewPrivacyTransferInstruction(vmVersion, proof, PrivacyTransferParams{
		Amount:               amount,
		SourceCommitment:     sourceCommitment,
		Nullifier:            nullifier,
		OutputCommitment:     outputCommitment,
		OutputSpendAuthority: outputSpendAuthority,
		OutputEncryptedNote:  encryptedNote,
		OutputAuditRecords:   auditRecords,
		ChangeAmount:         changeAmount,
		ChangeCommitment:     changeCommitment,
		ChangeSpendAuthority: changeSpendAuthority,
		ChangeEncryptedNote:  changeEncryptedNote,
		ChangeAuditRecords:   changeAuditRecords,
	})
}

func readPrivacyAuthorizeAuditInstruction(reader *borsh.Reader, vmVersion uint16, proof []byte) (PrivacyInstruction, error) {
	commitment, err := readPrivacyHash(reader, "audit commitment")
	if err != nil {
		return PrivacyInstruction{}, err
	}
	auditor, err := readPrivacyPublicKey(reader, "audit auditor")
	if err != nil {
		return PrivacyInstruction{}, err
	}
	scopeValue, err := reader.ReadUint8()
	if err != nil {
		return PrivacyInstruction{}, fmt.Errorf("structure: decode audit scope: %w", err)
	}
	expiresAtSlot, err := reader.ReadUint64()
	if err != nil {
		return PrivacyInstruction{}, fmt.Errorf("structure: decode audit expires slot: %w", err)
	}
	auditCiphertext, err := reader.ReadBytes()
	if err != nil {
		return PrivacyInstruction{}, fmt.Errorf("structure: decode audit ciphertext: %w", err)
	}
	return NewPrivacyAuthorizeAuditInstruction(vmVersion, proof, PrivacyAuthorizeAuditParams{
		Commitment:      commitment,
		Auditor:         auditor,
		Scope:           PrivacyAuditScope(scopeValue),
		ExpiresAtSlot:   expiresAtSlot,
		AuditCiphertext: auditCiphertext,
	})
}

func readPrivacyNotes(reader *borsh.Reader) ([]PrivacyNoteRecord, error) {
	noteCount, err := reader.ReadUint32()
	if err != nil {
		return nil, fmt.Errorf("structure: decode privacy note count: %w", err)
	}
	if noteCount > MaxPrivacyNotesPerState {
		return nil, fmt.Errorf("%w: note count %d exceeds %d", ErrInvalidPrivacyInstruction, noteCount, MaxPrivacyNotesPerState)
	}
	notes := make([]PrivacyNoteRecord, int(noteCount))
	for noteIndex := range notes {
		note, err := readPrivacyNote(reader)
		if err != nil {
			return nil, fmt.Errorf("structure: decode privacy note %d: %w", noteIndex, err)
		}
		notes[noteIndex] = note
	}
	return notes, nil
}

func readPrivacyNote(reader *borsh.Reader) (PrivacyNoteRecord, error) {
	commitment, err := readPrivacyHash(reader, "note commitment")
	if err != nil {
		return PrivacyNoteRecord{}, err
	}
	spendAuthority, err := readPrivacyPublicKey(reader, "note spend authority")
	if err != nil {
		return PrivacyNoteRecord{}, err
	}
	amount, err := reader.ReadUint64()
	if err != nil {
		return PrivacyNoteRecord{}, fmt.Errorf("structure: decode note amount: %w", err)
	}
	spent, err := reader.ReadBool()
	if err != nil {
		return PrivacyNoteRecord{}, fmt.Errorf("structure: decode note spent: %w", err)
	}
	spentSlot, err := reader.ReadUint64()
	if err != nil {
		return PrivacyNoteRecord{}, fmt.Errorf("structure: decode note spent slot: %w", err)
	}
	spendNullifier, err := readPrivacyHash(reader, "note spend nullifier")
	if err != nil {
		return PrivacyNoteRecord{}, err
	}
	vmVersion, err := reader.ReadUint16()
	if err != nil {
		return PrivacyNoteRecord{}, fmt.Errorf("structure: decode note vm version: %w", err)
	}
	encryptedNote, err := reader.ReadBytes()
	if err != nil {
		return PrivacyNoteRecord{}, fmt.Errorf("structure: decode note encrypted data: %w", err)
	}
	auditRecords, err := readPrivacyAuditRecords(reader)
	if err != nil {
		return PrivacyNoteRecord{}, err
	}
	return PrivacyNoteRecord{
		Commitment:     commitment,
		SpendAuthority: spendAuthority,
		Amount:         amount,
		Spent:          spent,
		SpentSlot:      spentSlot,
		SpendNullifier: spendNullifier,
		VMVersion:      vmVersion,
		EncryptedNote:  encryptedNote,
		AuditRecords:   auditRecords,
	}, nil
}

func readPrivacyNullifiers(reader *borsh.Reader) ([]Hash, error) {
	nullifierCount, err := reader.ReadUint32()
	if err != nil {
		return nil, fmt.Errorf("structure: decode nullifier count: %w", err)
	}
	if nullifierCount > MaxPrivacyNotesPerState {
		return nil, fmt.Errorf("%w: nullifier count %d exceeds %d", ErrInvalidPrivacyInstruction, nullifierCount, MaxPrivacyNotesPerState)
	}
	nullifiers := make([]Hash, int(nullifierCount))
	for nullifierIndex := range nullifiers {
		nullifier, err := readPrivacyHash(reader, "spent nullifier")
		if err != nil {
			return nil, fmt.Errorf("structure: decode nullifier %d: %w", nullifierIndex, err)
		}
		nullifiers[nullifierIndex] = nullifier
	}
	return nullifiers, nil
}

func privacyChangeOutputHashes(amount uint64, encryptedNote []byte, auditRecords []PrivacyAuditRecord) (Hash, Hash, error) {
	if amount == 0 {
		return Hash{}, Hash{}, nil
	}
	encryptedNoteHash, err := NewHash(utils.SHA256(encryptedNote))
	if err != nil {
		return Hash{}, Hash{}, err
	}
	auditRecordsHash, err := hashPrivacyAuditRecords(auditRecords)
	if err != nil {
		return Hash{}, Hash{}, err
	}
	return encryptedNoteHash, auditRecordsHash, nil
}

func writePrivacyChangeOutput(
	writer *borsh.Writer,
	amount uint64,
	commitment Hash,
	spendAuthority PublicKey,
	encryptedNote []byte,
	auditRecords []PrivacyAuditRecord,
) error {
	writer.WriteUint64(amount)
	writer.WriteFixedBytes(commitment[:])
	writer.WriteFixedBytes(spendAuthority[:])
	if err := writer.WriteBytes(encryptedNote); err != nil {
		return err
	}
	return writePrivacyAuditRecords(writer, auditRecords)
}

func readPrivacyChangeOutput(reader *borsh.Reader, field string) (uint64, Hash, PublicKey, []byte, []PrivacyAuditRecord, error) {
	if reader.Remaining() == 0 {
		return 0, Hash{}, PublicKey{}, nil, nil, nil
	}
	amount, err := reader.ReadUint64()
	if err != nil {
		return 0, Hash{}, PublicKey{}, nil, nil, fmt.Errorf("structure: decode %s amount: %w", field, err)
	}
	commitment, err := readPrivacyHash(reader, field+" commitment")
	if err != nil {
		return 0, Hash{}, PublicKey{}, nil, nil, err
	}
	spendAuthority, err := readPrivacyPublicKey(reader, field+" spend authority")
	if err != nil {
		return 0, Hash{}, PublicKey{}, nil, nil, err
	}
	encryptedNote, err := reader.ReadBytes()
	if err != nil {
		return 0, Hash{}, PublicKey{}, nil, nil, fmt.Errorf("structure: decode %s encrypted note: %w", field, err)
	}
	auditRecords, err := readPrivacyAuditRecords(reader)
	if err != nil {
		return 0, Hash{}, PublicKey{}, nil, nil, err
	}
	return amount, commitment, spendAuthority, encryptedNote, auditRecords, nil
}

func writePrivacyAuditRecords(writer *borsh.Writer, records []PrivacyAuditRecord) error {
	if len(records) > MaxPrivacyAuditRecordsPerNote {
		return fmt.Errorf("%w: audit record count %d exceeds %d", ErrInvalidPrivacyInstruction, len(records), MaxPrivacyAuditRecordsPerNote)
	}
	writer.WriteUint32(uint32(len(records)))
	for _, record := range records {
		writer.WriteFixedBytes(record.Auditor[:])
		writer.WriteUint8(uint8(record.Scope))
		writer.WriteUint64(record.ExpiresAtSlot)
		if err := writer.WriteBytes(record.AuditCiphertext); err != nil {
			return fmt.Errorf("structure: encode audit ciphertext: %w", err)
		}
	}
	return nil
}

func readPrivacyAuditRecords(reader *borsh.Reader) ([]PrivacyAuditRecord, error) {
	recordCount, err := reader.ReadUint32()
	if err != nil {
		return nil, fmt.Errorf("structure: decode audit record count: %w", err)
	}
	if recordCount > MaxPrivacyAuditRecordsPerNote {
		return nil, fmt.Errorf("%w: audit record count %d exceeds %d", ErrInvalidPrivacyInstruction, recordCount, MaxPrivacyAuditRecordsPerNote)
	}
	records := make([]PrivacyAuditRecord, int(recordCount))
	for recordIndex := range records {
		record, err := readPrivacyAuditRecord(reader)
		if err != nil {
			return nil, fmt.Errorf("structure: decode audit record %d: %w", recordIndex, err)
		}
		records[recordIndex] = record
	}
	return records, nil
}

func readPrivacyAuditRecord(reader *borsh.Reader) (PrivacyAuditRecord, error) {
	auditor, err := readPrivacyPublicKey(reader, "audit auditor")
	if err != nil {
		return PrivacyAuditRecord{}, err
	}
	scopeValue, err := reader.ReadUint8()
	if err != nil {
		return PrivacyAuditRecord{}, fmt.Errorf("structure: decode audit scope: %w", err)
	}
	expiresAtSlot, err := reader.ReadUint64()
	if err != nil {
		return PrivacyAuditRecord{}, fmt.Errorf("structure: decode audit expires slot: %w", err)
	}
	auditCiphertext, err := reader.ReadBytes()
	if err != nil {
		return PrivacyAuditRecord{}, fmt.Errorf("structure: decode audit ciphertext: %w", err)
	}
	return PrivacyAuditRecord{
		Auditor:         auditor,
		Scope:           PrivacyAuditScope(scopeValue),
		ExpiresAtSlot:   expiresAtSlot,
		AuditCiphertext: auditCiphertext,
	}, nil
}

func readPrivacyHash(reader *borsh.Reader, field string) (Hash, error) {
	value, err := reader.ReadFixedBytes(HashSize)
	if err != nil {
		return Hash{}, fmt.Errorf("structure: decode %s: %w", field, err)
	}
	hash, err := NewHash(value)
	if err != nil {
		return Hash{}, fmt.Errorf("structure: decode %s: %w", field, err)
	}
	return hash, nil
}

func readPrivacyPublicKey(reader *borsh.Reader, field string) (PublicKey, error) {
	value, err := reader.ReadFixedBytes(PublicKeySize)
	if err != nil {
		return PublicKey{}, fmt.Errorf("structure: decode %s: %w", field, err)
	}
	publicKey, err := NewPublicKey(value)
	if err != nil {
		return PublicKey{}, fmt.Errorf("structure: decode %s: %w", field, err)
	}
	return publicKey, nil
}

func validatePrivacyStateUniqueness(state PrivacyState) error {
	commitments := make(map[Hash]struct{}, len(state.Notes))
	for noteIndex, note := range state.Notes {
		if err := validatePrivacyNote(note); err != nil {
			return fmt.Errorf("structure: privacy note %d: %w", noteIndex, err)
		}
		if _, exists := commitments[note.Commitment]; exists {
			return fmt.Errorf("%w: duplicate commitment", ErrInvalidPrivacyInstruction)
		}
		commitments[note.Commitment] = struct{}{}
	}
	nullifiers := make(map[Hash]struct{}, len(state.SpentNullifiers))
	for _, nullifier := range state.SpentNullifiers {
		if nullifier.IsZero() {
			return fmt.Errorf("%w: zero nullifier", ErrInvalidPrivacyInstruction)
		}
		if _, exists := nullifiers[nullifier]; exists {
			return fmt.Errorf("%w: duplicate nullifier", ErrInvalidPrivacyInstruction)
		}
		nullifiers[nullifier] = struct{}{}
	}
	return nil
}

func validatePrivacyNote(note PrivacyNoteRecord) error {
	if note.Commitment.IsZero() {
		return fmt.Errorf("%w: zero commitment", ErrInvalidPrivacyInstruction)
	}
	if note.SpendAuthority.IsZero() {
		return fmt.Errorf("%w: zero spend authority", ErrInvalidPrivacyInstruction)
	}
	if note.Amount == 0 {
		return fmt.Errorf("%w: note amount cannot be zero", ErrInvalidPrivacyInstruction)
	}
	if note.Spent {
		if note.SpentSlot == 0 || note.SpendNullifier.IsZero() {
			return fmt.Errorf("%w: spent note missing slot or nullifier", ErrInvalidPrivacyInstruction)
		}
	}
	if !note.Spent && (note.SpentSlot != 0 || !note.SpendNullifier.IsZero()) {
		return fmt.Errorf("%w: unspent note has spend metadata", ErrInvalidPrivacyInstruction)
	}
	if err := validateEncryptedNote(note.EncryptedNote); err != nil {
		return err
	}
	return validatePrivacyAuditRecords(note.AuditRecords)
}

func validatePrivacyDepositParams(params *PrivacyDepositParams) error {
	if params == nil {
		return fmt.Errorf("%w: deposit params are nil", ErrInvalidPrivacyInstruction)
	}
	if params.Amount == 0 {
		return fmt.Errorf("%w: deposit amount cannot be zero", ErrInvalidPrivacyInstruction)
	}
	if params.Commitment.IsZero() {
		return fmt.Errorf("%w: deposit commitment is zero", ErrInvalidPrivacyInstruction)
	}
	if params.SpendAuthority.IsZero() {
		return fmt.Errorf("%w: deposit spend authority is zero", ErrInvalidPrivacyInstruction)
	}
	if err := validateEncryptedNote(params.EncryptedNote); err != nil {
		return err
	}
	return validatePrivacyAuditRecords(params.AuditRecords)
}

func validatePrivacyWithdrawParams(params *PrivacyWithdrawParams) error {
	if params == nil {
		return fmt.Errorf("%w: withdraw params are nil", ErrInvalidPrivacyInstruction)
	}
	if params.Amount == 0 {
		return fmt.Errorf("%w: withdraw amount cannot be zero", ErrInvalidPrivacyInstruction)
	}
	if params.SourceCommitment.IsZero() || params.Nullifier.IsZero() {
		return fmt.Errorf("%w: withdraw commitment or nullifier is zero", ErrInvalidPrivacyInstruction)
	}
	if err := validatePrivacyAuditRecords(params.AuditRecords); err != nil {
		return err
	}
	return validatePrivacyChangeOutput(
		params.ChangeAmount,
		params.ChangeCommitment,
		params.ChangeSpendAuthority,
		params.ChangeEncryptedNote,
		params.ChangeAuditRecords,
	)
}

func validatePrivacyTransferParams(params *PrivacyTransferParams) error {
	if params == nil {
		return fmt.Errorf("%w: transfer params are nil", ErrInvalidPrivacyInstruction)
	}
	if params.Amount == 0 {
		return fmt.Errorf("%w: transfer amount cannot be zero", ErrInvalidPrivacyInstruction)
	}
	if params.SourceCommitment.IsZero() || params.Nullifier.IsZero() || params.OutputCommitment.IsZero() {
		return fmt.Errorf("%w: transfer commitment or nullifier is zero", ErrInvalidPrivacyInstruction)
	}
	if params.OutputSpendAuthority.IsZero() {
		return fmt.Errorf("%w: transfer output spend authority is zero", ErrInvalidPrivacyInstruction)
	}
	if err := validateEncryptedNote(params.OutputEncryptedNote); err != nil {
		return err
	}
	if params.ChangeAmount > 0 && params.OutputCommitment == params.ChangeCommitment {
		return fmt.Errorf("%w: transfer output and change commitment must differ", ErrInvalidPrivacyInstruction)
	}
	if err := validatePrivacyAuditRecords(params.OutputAuditRecords); err != nil {
		return err
	}
	return validatePrivacyChangeOutput(
		params.ChangeAmount,
		params.ChangeCommitment,
		params.ChangeSpendAuthority,
		params.ChangeEncryptedNote,
		params.ChangeAuditRecords,
	)
}

func validatePrivacyChangeOutput(
	amount uint64,
	commitment Hash,
	spendAuthority PublicKey,
	encryptedNote []byte,
	auditRecords []PrivacyAuditRecord,
) error {
	if amount == 0 {
		if !commitment.IsZero() || !spendAuthority.IsZero() || len(encryptedNote) > 0 || len(auditRecords) > 0 {
			return fmt.Errorf("%w: zero change amount has change output data", ErrInvalidPrivacyInstruction)
		}
		return nil
	}
	if commitment.IsZero() || spendAuthority.IsZero() {
		return fmt.Errorf("%w: change commitment or spend authority is zero", ErrInvalidPrivacyInstruction)
	}
	if err := validateEncryptedNote(encryptedNote); err != nil {
		return err
	}
	return validatePrivacyAuditRecords(auditRecords)
}

func validatePrivacyAuthorizeAuditParams(params *PrivacyAuthorizeAuditParams) error {
	if params == nil {
		return fmt.Errorf("%w: authorize audit params are nil", ErrInvalidPrivacyInstruction)
	}
	if params.Commitment.IsZero() {
		return fmt.Errorf("%w: audit commitment is zero", ErrInvalidPrivacyInstruction)
	}
	return validatePrivacyAuditRecord(PrivacyAuditRecord{
		Auditor:         params.Auditor,
		Scope:           params.Scope,
		ExpiresAtSlot:   params.ExpiresAtSlot,
		AuditCiphertext: params.AuditCiphertext,
	})
}

func validateEncryptedNote(value []byte) error {
	if len(value) == 0 {
		return fmt.Errorf("%w: encrypted note cannot be empty", ErrInvalidPrivacyInstruction)
	}
	if len(value) > MaxPrivacyEncryptedNoteBytes {
		return fmt.Errorf("%w: encrypted note length %d exceeds %d", ErrInvalidPrivacyInstruction, len(value), MaxPrivacyEncryptedNoteBytes)
	}
	return nil
}

func validatePrivacyAuditRecords(records []PrivacyAuditRecord) error {
	if len(records) > MaxPrivacyAuditRecordsPerNote {
		return fmt.Errorf("%w: audit record count %d exceeds %d", ErrInvalidPrivacyInstruction, len(records), MaxPrivacyAuditRecordsPerNote)
	}
	seenRecords := make(map[privacyAuditRecordKey]struct{}, len(records))
	for recordIndex, record := range records {
		if err := validatePrivacyAuditRecord(record); err != nil {
			return fmt.Errorf("structure: audit record %d: %w", recordIndex, err)
		}
		key := privacyAuditKey(record)
		if _, exists := seenRecords[key]; exists {
			return fmt.Errorf("%w: duplicate audit record", ErrInvalidPrivacyInstruction)
		}
		seenRecords[key] = struct{}{}
	}
	return nil
}

func validatePrivacyAuditRecordsForSlot(records []PrivacyAuditRecord, currentSlot uint64) error {
	if err := validatePrivacyAuditRecords(records); err != nil {
		return err
	}
	for _, record := range records {
		if record.ExpiresAtSlot != 0 && record.ExpiresAtSlot <= currentSlot {
			return fmt.Errorf("%w: audit authorization already expired", ErrInvalidPrivacyInstruction)
		}
	}
	return nil
}

func validatePrivacyAuditRecordForSlot(record PrivacyAuditRecord, currentSlot uint64) error {
	if err := validatePrivacyAuditRecord(record); err != nil {
		return err
	}
	if record.ExpiresAtSlot != 0 && record.ExpiresAtSlot <= currentSlot {
		return fmt.Errorf("%w: audit authorization already expired", ErrInvalidPrivacyInstruction)
	}
	return nil
}

func validatePrivacyAuditRecord(record PrivacyAuditRecord) error {
	if record.Auditor.IsZero() {
		return fmt.Errorf("%w: audit auditor is zero", ErrInvalidPrivacyInstruction)
	}
	if !record.Scope.IsValid() {
		return fmt.Errorf("%w: invalid audit scope %d", ErrInvalidPrivacyInstruction, record.Scope)
	}
	if len(record.AuditCiphertext) == 0 {
		return fmt.Errorf("%w: audit ciphertext cannot be empty", ErrInvalidPrivacyInstruction)
	}
	if len(record.AuditCiphertext) > MaxPrivacyAuditCiphertextBytes {
		return fmt.Errorf("%w: audit ciphertext length %d exceeds %d", ErrInvalidPrivacyInstruction, len(record.AuditCiphertext), MaxPrivacyAuditCiphertextBytes)
	}
	return nil
}

func hasPrivacyAuditRecord(records []PrivacyAuditRecord, target PrivacyAuditRecord) bool {
	targetKey := privacyAuditKey(target)
	for _, record := range records {
		if privacyAuditKey(record) == targetKey {
			return true
		}
	}
	return false
}

func privacyAuditKey(record PrivacyAuditRecord) privacyAuditRecordKey {
	ciphertextHash, err := NewHash(utils.SHA256(record.AuditCiphertext))
	if err != nil {
		return privacyAuditRecordKey{Auditor: record.Auditor, Scope: record.Scope}
	}
	return privacyAuditRecordKey{Auditor: record.Auditor, Scope: record.Scope, CiphertextHash: ciphertextHash}
}

func clonePrivacyAuditRecords(records []PrivacyAuditRecord) []PrivacyAuditRecord {
	if records == nil {
		return nil
	}
	cloned := make([]PrivacyAuditRecord, len(records))
	for index, record := range records {
		cloned[index] = clonePrivacyAuditRecord(record)
	}
	return cloned
}

func clonePrivacyAuditRecord(record PrivacyAuditRecord) PrivacyAuditRecord {
	return PrivacyAuditRecord{
		Auditor:         record.Auditor,
		Scope:           record.Scope,
		ExpiresAtSlot:   record.ExpiresAtSlot,
		AuditCiphertext: utils.CloneBytes(record.AuditCiphertext),
	}
}

// IsValid 校验审计范围 + 防止链上出现未定义权限语义。
func (scope PrivacyAuditScope) IsValid() bool {
	return scope == PrivacyAuditScopeOwner ||
		scope == PrivacyAuditScopeBusiness ||
		scope == PrivacyAuditScopeRegulatory
}
