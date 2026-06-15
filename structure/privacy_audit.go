package structure

import (
	"fmt"

	"solana_golang/codec/borsh"
	"solana_golang/utils"
)

const (
	PrivacyAuditPayloadVersion = uint16(1)
	PrivacyAuditKeySize        = utils.AES256KeySize
	privacyAuditPayloadMaxSize = 256
)

// PrivacyAuditPayload 描述链下审计明文 + 审计方解密后用它核对链上隐私交易事实。
type PrivacyAuditPayload struct {
	Version          uint16
	TransactionType  PrivacyInstructionType
	Commitment       Hash
	Nullifier        Hash
	OutputCommitment Hash
	Amount           uint64
	Slot             uint64
}

// NewEncryptedPrivacyAuditRecord 创建授权审计记录 + 使用 AES-GCM 让链上只保存密文。
func NewEncryptedPrivacyAuditRecord(auditor PublicKey, scope PrivacyAuditScope, expiresAtSlot uint64, auditKey []byte, payload PrivacyAuditPayload) (PrivacyAuditRecord, error) {
	if err := validatePrivacyAuditKey(auditKey); err != nil {
		return PrivacyAuditRecord{}, err
	}
	if err := payload.Validate(); err != nil {
		return PrivacyAuditRecord{}, err
	}
	if expiresAtSlot != 0 && payload.Slot >= expiresAtSlot {
		return PrivacyAuditRecord{}, fmt.Errorf("%w: audit payload slot is outside authorization", ErrInvalidPrivacyInstruction)
	}

	encodedPayload, err := payload.MarshalBinary()
	if err != nil {
		return PrivacyAuditRecord{}, err
	}
	auditCiphertext, err := utils.AESGCMEncrypt(auditKey, encodedPayload)
	if err != nil {
		return PrivacyAuditRecord{}, fmt.Errorf("structure: encrypt audit payload: %w", err)
	}

	record := PrivacyAuditRecord{
		Auditor:         auditor,
		Scope:           scope,
		ExpiresAtSlot:   expiresAtSlot,
		AuditCiphertext: auditCiphertext,
	}
	return record, validatePrivacyAuditRecord(record)
}

// AuditPrivacyNote 解密 note 上的授权审计记录 + 只返回授权范围内能成功解密的负载。
func AuditPrivacyNote(note PrivacyNoteRecord, auditor PublicKey, scope PrivacyAuditScope, currentSlot uint64, auditKey []byte) ([]PrivacyAuditPayload, error) {
	if err := validatePrivacyAuditKey(auditKey); err != nil {
		return nil, err
	}
	if auditor.IsZero() {
		return nil, fmt.Errorf("%w: audit auditor is zero", ErrInvalidPrivacyInstruction)
	}
	if !scope.IsValid() {
		return nil, fmt.Errorf("%w: invalid audit scope %d", ErrInvalidPrivacyInstruction, scope)
	}

	payloads := make([]PrivacyAuditPayload, 0, len(note.AuditRecords))
	for _, record := range note.AuditRecords {
		if record.Auditor != auditor || record.Scope != scope {
			continue
		}
		if record.ExpiresAtSlot != 0 && record.ExpiresAtSlot <= currentSlot {
			continue
		}
		payload, err := decryptPrivacyAuditPayload(auditKey, record)
		if err != nil {
			return nil, err
		}
		payloads = append(payloads, payload)
	}
	if len(payloads) == 0 {
		return nil, fmt.Errorf("%w: no active audit record", ErrInvalidPrivacyInstruction)
	}
	return payloads, nil
}

// Validate 校验审计负载 + 防止审计密文解开后语义不完整。
func (payload PrivacyAuditPayload) Validate() error {
	if payload.Version != PrivacyAuditPayloadVersion {
		return fmt.Errorf("%w: unsupported audit payload version %d", ErrInvalidPrivacyInstruction, payload.Version)
	}
	if payload.Amount == 0 {
		return fmt.Errorf("%w: audit amount cannot be zero", ErrInvalidPrivacyInstruction)
	}
	if payload.Slot == 0 {
		return fmt.Errorf("%w: audit slot cannot be zero", ErrInvalidPrivacyInstruction)
	}
	if payload.Commitment.IsZero() {
		return fmt.Errorf("%w: audit commitment is zero", ErrInvalidPrivacyInstruction)
	}
	switch payload.TransactionType {
	case PrivacyInstructionDeposit:
		return nil
	case PrivacyInstructionWithdraw:
		return validateAuditSpendPayload(payload)
	case PrivacyInstructionTransfer:
		if err := validateAuditSpendPayload(payload); err != nil {
			return err
		}
		if payload.OutputCommitment.IsZero() {
			return fmt.Errorf("%w: audit output commitment is zero", ErrInvalidPrivacyInstruction)
		}
		return nil
	default:
		return fmt.Errorf("%w: unsupported audit transaction type %d", ErrInvalidPrivacyInstruction, payload.TransactionType)
	}
}

// MarshalBinary 序列化审计负载 + 用固定字段顺序保证审计密文跨端一致。
func (payload PrivacyAuditPayload) MarshalBinary() ([]byte, error) {
	if err := payload.Validate(); err != nil {
		return nil, err
	}
	writer := borsh.NewWriter(privacyAuditPayloadMaxSize)
	writer.WriteUint16(payload.Version)
	writer.WriteUint32(uint32(payload.TransactionType))
	writer.WriteFixedBytes(payload.Commitment[:])
	writer.WriteFixedBytes(payload.Nullifier[:])
	writer.WriteFixedBytes(payload.OutputCommitment[:])
	writer.WriteUint64(payload.Amount)
	writer.WriteUint64(payload.Slot)
	return writer.Bytes(), nil
}

// UnmarshalPrivacyAuditPayloadBinary 反序列化审计负载 + 解密后必须校验尾部字节。
func UnmarshalPrivacyAuditPayloadBinary(data []byte) (PrivacyAuditPayload, error) {
	reader := borsh.NewReader(data, privacyAuditPayloadMaxSize)
	version, err := reader.ReadUint16()
	if err != nil {
		return PrivacyAuditPayload{}, fmt.Errorf("structure: decode audit payload version: %w", err)
	}
	transactionType, err := reader.ReadUint32()
	if err != nil {
		return PrivacyAuditPayload{}, fmt.Errorf("structure: decode audit transaction type: %w", err)
	}
	commitment, err := readPrivacyHash(reader, "audit payload commitment")
	if err != nil {
		return PrivacyAuditPayload{}, err
	}
	nullifier, err := readPrivacyHash(reader, "audit payload nullifier")
	if err != nil {
		return PrivacyAuditPayload{}, err
	}
	outputCommitment, err := readPrivacyHash(reader, "audit payload output commitment")
	if err != nil {
		return PrivacyAuditPayload{}, err
	}
	amount, err := reader.ReadUint64()
	if err != nil {
		return PrivacyAuditPayload{}, fmt.Errorf("structure: decode audit amount: %w", err)
	}
	slot, err := reader.ReadUint64()
	if err != nil {
		return PrivacyAuditPayload{}, fmt.Errorf("structure: decode audit slot: %w", err)
	}
	if err := reader.EnsureEOF(); err != nil {
		return PrivacyAuditPayload{}, fmt.Errorf("structure: decode audit payload eof: %w", err)
	}

	payload := PrivacyAuditPayload{
		Version:          version,
		TransactionType:  PrivacyInstructionType(transactionType),
		Commitment:       commitment,
		Nullifier:        nullifier,
		OutputCommitment: outputCommitment,
		Amount:           amount,
		Slot:             slot,
	}
	return payload, payload.Validate()
}

func decryptPrivacyAuditPayload(auditKey []byte, record PrivacyAuditRecord) (PrivacyAuditPayload, error) {
	if err := validatePrivacyAuditRecord(record); err != nil {
		return PrivacyAuditPayload{}, err
	}
	plaintext, err := utils.AESGCMDecrypt(auditKey, record.AuditCiphertext)
	if err != nil {
		return PrivacyAuditPayload{}, fmt.Errorf("structure: decrypt audit payload: %w", err)
	}
	return UnmarshalPrivacyAuditPayloadBinary(plaintext)
}

func validateAuditSpendPayload(payload PrivacyAuditPayload) error {
	if payload.Nullifier.IsZero() {
		return fmt.Errorf("%w: audit nullifier is zero", ErrInvalidPrivacyInstruction)
	}
	return nil
}

func validatePrivacyAuditKey(auditKey []byte) error {
	if len(auditKey) != PrivacyAuditKeySize {
		return fmt.Errorf("%w: audit key requires %d bytes, got %d", ErrInvalidPrivacyInstruction, PrivacyAuditKeySize, len(auditKey))
	}
	return nil
}
