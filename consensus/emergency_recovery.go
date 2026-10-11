package consensus

import (
	"encoding/binary"
	"fmt"
	"sort"
	"strings"

	"github.com/tentechtop/ordexus-core/structure"
)

const (
	EmergencyRecoveryCertificateVersion        uint16 = 2
	MultiValidatorActivationCertificateVersion uint16 = 1
)

// EmergencyRecoverySignature 是安全委员会验证者对模式切换证书的签名。
type EmergencyRecoverySignature struct {
	ValidatorID string `json:"validator_id"`
	PublicKey   string `json:"public_key"`
	Signature   string `json:"signature"`
}

// EmergencyRecoveryCertificate 从最后确认检查点选出临时长老。
// ExcludedValidatorIDs 由同一份 >2/3 权益证书确认，用于跳过已失联的高权重验证者。
type EmergencyRecoveryCertificate struct {
	Version                uint16                       `json:"version"`
	ChainID                string                       `json:"chain_id"`
	Sequence               uint64                       `json:"sequence"`
	FinalizedHeight        uint64                       `json:"finalized_height"`
	FinalizedHash          string                       `json:"finalized_hash"`
	SafetyEpochID          uint64                       `json:"safety_epoch_id"`
	SafetyValidatorSetRoot string                       `json:"safety_validator_set_root"`
	ElderValidatorID       string                       `json:"elder_validator_id"`
	ExcludedValidatorIDs   []string                     `json:"excluded_validator_ids,omitempty"`
	ExpiresAtUnixMilli     int64                        `json:"expires_at_unix_milli"`
	Signatures             []EmergencyRecoverySignature `json:"signatures"`
}

// MultiValidatorActivationCertificate 由冻结的安全委员会确认临时分支和新验证者集合。
type MultiValidatorActivationCertificate struct {
	Version                uint16                       `json:"version"`
	ChainID                string                       `json:"chain_id"`
	Sequence               uint64                       `json:"sequence"`
	SafetyFinalizedHeight  uint64                       `json:"safety_finalized_height"`
	SafetyFinalizedHash    string                       `json:"safety_finalized_hash"`
	SafetyEpochID          uint64                       `json:"safety_epoch_id"`
	SafetyValidatorSetRoot string                       `json:"safety_validator_set_root"`
	TargetHeight           uint64                       `json:"target_height"`
	TargetHash             string                       `json:"target_hash"`
	TargetEpochID          uint64                       `json:"target_epoch_id"`
	TargetValidatorSetRoot string                       `json:"target_validator_set_root"`
	ExpiresAtUnixMilli     int64                        `json:"expires_at_unix_milli"`
	Signatures             []EmergencyRecoverySignature `json:"signatures"`
}

// SignBytes 返回跨节点稳定的恢复签名消息；签名列表不参与签名。
func (certificate EmergencyRecoveryCertificate) SignBytes() ([]byte, error) {
	chainID := strings.TrimSpace(certificate.ChainID)
	if certificate.Version != EmergencyRecoveryCertificateVersion || chainID == "" || certificate.Sequence == 0 || strings.TrimSpace(certificate.ElderValidatorID) == "" {
		return nil, fmt.Errorf("consensus: invalid emergency recovery certificate identity")
	}
	finalizedHash, err := structure.HashFromBase58(strings.TrimSpace(certificate.FinalizedHash))
	if err != nil {
		return nil, fmt.Errorf("consensus: decode recovery finalized hash: %w", err)
	}
	validatorSetRoot, err := structure.HashFromBase58(strings.TrimSpace(certificate.SafetyValidatorSetRoot))
	if err != nil {
		return nil, fmt.Errorf("consensus: decode recovery validator set root: %w", err)
	}
	excluded, err := normalizedValidatorIDs(certificate.ExcludedValidatorIDs)
	if err != nil {
		return nil, err
	}
	encoded := make([]byte, 0, 256)
	encoded = append(encoded, []byte("pos-emergency-recovery-v2")...)
	encoded = appendCertificateIdentity(encoded, certificate.Version, chainID, certificate.Sequence)
	encoded = binary.LittleEndian.AppendUint64(encoded, certificate.FinalizedHeight)
	encoded = append(encoded, finalizedHash[:]...)
	encoded = binary.LittleEndian.AppendUint64(encoded, certificate.SafetyEpochID)
	encoded = append(encoded, validatorSetRoot[:]...)
	encoded = appendString(encoded, strings.TrimSpace(certificate.ElderValidatorID))
	encoded = binary.LittleEndian.AppendUint64(encoded, uint64(len(excluded)))
	for _, validatorID := range excluded {
		encoded = appendString(encoded, validatorID)
	}
	encoded = binary.LittleEndian.AppendUint64(encoded, uint64(certificate.ExpiresAtUnixMilli))
	return encoded, nil
}

// SignBytes 返回跨节点稳定的多验证者激活签名消息。
func (certificate MultiValidatorActivationCertificate) SignBytes() ([]byte, error) {
	chainID := strings.TrimSpace(certificate.ChainID)
	if certificate.Version != MultiValidatorActivationCertificateVersion || chainID == "" || certificate.Sequence == 0 {
		return nil, fmt.Errorf("consensus: invalid multi-validator activation certificate identity")
	}
	safetyHash, err := structure.HashFromBase58(strings.TrimSpace(certificate.SafetyFinalizedHash))
	if err != nil {
		return nil, fmt.Errorf("consensus: decode activation safety hash: %w", err)
	}
	safetyRoot, err := structure.HashFromBase58(strings.TrimSpace(certificate.SafetyValidatorSetRoot))
	if err != nil {
		return nil, fmt.Errorf("consensus: decode activation safety validator root: %w", err)
	}
	targetHash, err := structure.HashFromBase58(strings.TrimSpace(certificate.TargetHash))
	if err != nil {
		return nil, fmt.Errorf("consensus: decode activation target hash: %w", err)
	}
	targetRoot, err := structure.HashFromBase58(strings.TrimSpace(certificate.TargetValidatorSetRoot))
	if err != nil {
		return nil, fmt.Errorf("consensus: decode activation target validator root: %w", err)
	}
	encoded := make([]byte, 0, 256)
	encoded = append(encoded, []byte("pos-multi-validator-activation-v1")...)
	encoded = appendCertificateIdentity(encoded, certificate.Version, chainID, certificate.Sequence)
	encoded = binary.LittleEndian.AppendUint64(encoded, certificate.SafetyFinalizedHeight)
	encoded = append(encoded, safetyHash[:]...)
	encoded = binary.LittleEndian.AppendUint64(encoded, certificate.SafetyEpochID)
	encoded = append(encoded, safetyRoot[:]...)
	encoded = binary.LittleEndian.AppendUint64(encoded, certificate.TargetHeight)
	encoded = append(encoded, targetHash[:]...)
	encoded = binary.LittleEndian.AppendUint64(encoded, certificate.TargetEpochID)
	encoded = append(encoded, targetRoot[:]...)
	encoded = binary.LittleEndian.AppendUint64(encoded, uint64(certificate.ExpiresAtUnixMilli))
	return encoded, nil
}

func normalizedValidatorIDs(values []string) ([]string, error) {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		validatorID := strings.TrimSpace(value)
		if validatorID == "" {
			return nil, fmt.Errorf("consensus: empty excluded validator id")
		}
		if _, exists := seen[validatorID]; exists {
			return nil, fmt.Errorf("consensus: duplicate excluded validator id")
		}
		seen[validatorID] = struct{}{}
		result = append(result, validatorID)
	}
	sort.Strings(result)
	return result, nil
}

func appendCertificateIdentity(encoded []byte, version uint16, chainID string, sequence uint64) []byte {
	encoded = binary.LittleEndian.AppendUint16(encoded, version)
	encoded = appendString(encoded, chainID)
	return binary.LittleEndian.AppendUint64(encoded, sequence)
}

func appendString(encoded []byte, value string) []byte {
	encoded = binary.LittleEndian.AppendUint64(encoded, uint64(len(value)))
	return append(encoded, value...)
}
