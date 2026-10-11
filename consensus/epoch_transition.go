package consensus

import (
	"fmt"
	"sort"
	"strings"

	"github.com/tentechtop/ordexus-core/structure"
)

const epochTransitionDomainV1 = "solana_golang.consensus.epoch-transition.v1"

// EpochTransition 将下一 epoch 验证者快照绑定到上一 epoch 已认证链头。
// 验证者只对自己从该链头状态计算出的目标快照签名，避免节点自报身份进入共识集合。
type EpochTransition struct {
	ChainID           string `json:"chain_id"`
	ChainIdentityHash string `json:"chain_identity_hash"`
	GenesisHash       string `json:"genesis_hash"`
	FromEpochID       uint64 `json:"from_epoch_id"`
	ToEpochID         uint64 `json:"to_epoch_id"`
	SourceBlockHeight uint64 `json:"source_block_height"`
	SourceBlockHash   string `json:"source_block_hash"`
	FromSnapshotRoot  string `json:"from_snapshot_root"`
	ToSnapshotRoot    string `json:"to_snapshot_root"`
}

// EpochTransitionSignature 是上一 epoch 单个验证者对同一过渡声明的签名。
type EpochTransitionSignature struct {
	ValidatorID string `json:"validator_id"`
	PublicKey   string `json:"public_key"`
	Signature   string `json:"signature"`
}

// EpochTransitionCertificate 保存严格超过 2/3 上一 epoch 权益的签名，可独立验证下一验证者集合。
type EpochTransitionCertificate struct {
	Transition EpochTransition            `json:"transition"`
	Signatures []EpochTransitionSignature `json:"signatures"`
}

// Validate 校验过渡的确定性边界，不接受跳 epoch 或空承诺。
func (transition EpochTransition) Validate() error {
	if strings.TrimSpace(transition.ChainID) == "" || len(transition.ChainID) > maxConsensusTextLength {
		return fmt.Errorf("consensus: invalid epoch transition chain id")
	}
	if _, err := structure.HashFromBase58(strings.TrimSpace(transition.ChainIdentityHash)); err != nil {
		return fmt.Errorf("consensus: invalid epoch transition chain identity hash: %w", err)
	}
	if _, err := structure.HashFromBase58(strings.TrimSpace(transition.GenesisHash)); err != nil {
		return fmt.Errorf("consensus: invalid epoch transition genesis hash: %w", err)
	}
	if transition.FromEpochID == ^uint64(0) || transition.ToEpochID != transition.FromEpochID+1 {
		return fmt.Errorf("consensus: epoch transition must advance exactly one epoch")
	}
	if _, err := structure.HashFromBase58(strings.TrimSpace(transition.SourceBlockHash)); err != nil {
		return fmt.Errorf("consensus: invalid epoch transition source hash: %w", err)
	}
	if _, err := structure.HashFromBase58(strings.TrimSpace(transition.FromSnapshotRoot)); err != nil {
		return fmt.Errorf("consensus: invalid epoch transition source snapshot root: %w", err)
	}
	if _, err := structure.HashFromBase58(strings.TrimSpace(transition.ToSnapshotRoot)); err != nil {
		return fmt.Errorf("consensus: invalid epoch transition target snapshot root: %w", err)
	}
	return nil
}

// SignBytes 返回跨节点一致的签名消息，签名同时承诺链、源链头和前后验证者集合。
func (transition EpochTransition) SignBytes() ([]byte, error) {
	if err := transition.Validate(); err != nil {
		return nil, err
	}
	sourceHash, _ := structure.HashFromBase58(strings.TrimSpace(transition.SourceBlockHash))
	chainIdentityHash, _ := structure.HashFromBase58(strings.TrimSpace(transition.ChainIdentityHash))
	genesisHash, _ := structure.HashFromBase58(strings.TrimSpace(transition.GenesisHash))
	fromRoot, _ := structure.HashFromBase58(strings.TrimSpace(transition.FromSnapshotRoot))
	toRoot, _ := structure.HashFromBase58(strings.TrimSpace(transition.ToSnapshotRoot))
	encoded := append([]byte{}, []byte(epochTransitionDomainV1)...)
	encoded = appendUint64ForHash(encoded, uint64(len(transition.ChainID)))
	encoded = append(encoded, transition.ChainID...)
	encoded = append(encoded, chainIdentityHash[:]...)
	encoded = append(encoded, genesisHash[:]...)
	encoded = appendUint64ForHash(encoded, transition.FromEpochID)
	encoded = appendUint64ForHash(encoded, transition.ToEpochID)
	encoded = appendUint64ForHash(encoded, transition.SourceBlockHeight)
	encoded = append(encoded, sourceHash[:]...)
	encoded = append(encoded, fromRoot[:]...)
	encoded = append(encoded, toRoot[:]...)
	return encoded, nil
}

// ValidateStructure 校验证书容器；权益和签名真伪必须结合上一 epoch 快照验证。
func (certificate EpochTransitionCertificate) ValidateStructure() error {
	if err := certificate.Transition.Validate(); err != nil {
		return err
	}
	if len(certificate.Signatures) == 0 || len(certificate.Signatures) > MaxValidatorsPerSet {
		return fmt.Errorf("consensus: invalid epoch transition signature count")
	}
	seen := make(map[string]struct{}, len(certificate.Signatures))
	for index, item := range certificate.Signatures {
		validatorID := strings.TrimSpace(item.ValidatorID)
		if validatorID == "" || len(validatorID) > maxConsensusTextLength {
			return fmt.Errorf("consensus: invalid epoch transition validator id at index %d", index)
		}
		if _, exists := seen[validatorID]; exists {
			return fmt.Errorf("consensus: duplicate epoch transition validator signature")
		}
		if strings.TrimSpace(item.PublicKey) == "" || strings.TrimSpace(item.Signature) == "" {
			return fmt.Errorf("consensus: incomplete epoch transition signature at index %d", index)
		}
		seen[validatorID] = struct{}{}
	}
	return nil
}

// Canonical 返回按 validator_id 排序的证书，保证持久化和传播结果稳定。
func (certificate EpochTransitionCertificate) Canonical() (EpochTransitionCertificate, error) {
	if err := certificate.ValidateStructure(); err != nil {
		return EpochTransitionCertificate{}, err
	}
	canonical := EpochTransitionCertificate{
		Transition: certificate.Transition,
		Signatures: append([]EpochTransitionSignature(nil), certificate.Signatures...),
	}
	sort.Slice(canonical.Signatures, func(left int, right int) bool {
		return canonical.Signatures[left].ValidatorID < canonical.Signatures[right].ValidatorID
	})
	return canonical, nil
}
