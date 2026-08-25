package shielded

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"io"

	"golang.org/x/crypto/hkdf"
)

const (
	// SeedSize 是创建屏蔽主密钥时要求的种子长度。
	SeedSize = 32
	// SpendingKeySize 是屏蔽主密钥的序列化长度。
	SpendingKeySize = 32
	// DiversifierSize 是单个屏蔽地址使用的 diversifier 长度。
	DiversifierSize = 11
	// OwnerTagSize 是地址所有者标签的长度。
	OwnerTagSize = 32
	// TransmissionKeySize 是 X25519 transmission key 的长度。
	TransmissionKeySize = 32
	// ViewKeyVersion 是查看密钥持久化编码的版本。
	ViewKeyVersion uint8 = 1
	// ViewKeyChecksumSize 是查看密钥持久化编码的校验和长度。
	ViewKeyChecksumSize = 4
	// ViewKeySize 是查看密钥持久化编码的固定长度。
	ViewKeySize = 1 + DiversifierSize + TransmissionKeySize + FieldElementSize + OwnerTagSize + ViewKeyChecksumSize
)

const (
	spendingKeyDomain     = "solana_golang.shielded.spending_key.v2"
	viewKeySaltDomain     = "solana_golang.shielded.view_key_salt.v2"
	transmissionKeyDomain = "solana_golang.shielded.transmission_key.v2"
	viewKeyChecksumDomain = "solana_golang.shielded.view_key_checksum.v2"
)

// Diversifier 允许同一屏蔽主密钥派生相互独立的收款地址。
type Diversifier [DiversifierSize]byte

// OwnerTag 是地址中用于确认派生关系的公开标签。
type OwnerTag [OwnerTagSize]byte

// SpendingKey 是独立于透明 Ed25519 身份的屏蔽主密钥。
//
// 该类型只保存 HKDF 派生后的主密钥，不直接保存调用方提供的 seed。
type SpendingKey struct {
	masterKey [SpendingKeySize]byte
}

// ViewKey 包含解密票据和计算 nullifier 所需的查看能力，但不包含花费授权材料。
type ViewKey struct {
	diversifier            Diversifier
	transmissionPrivateKey [TransmissionKeySize]byte
	nullifierKey           [32]byte
	ownerTag               OwnerTag
}

// NewDiversifier 从固定长度字节创建 diversifier。
func NewDiversifier(value []byte) (Diversifier, error) {
	var diversifier Diversifier
	if len(value) != DiversifierSize {
		return diversifier, fmt.Errorf("shielded: diversifier requires %d bytes, got %d", DiversifierSize, len(value))
	}
	copy(diversifier[:], value)
	return diversifier, nil
}

// DeriveSpendingKey 使用 HKDF-SHA256 从 32 字节 seed 派生屏蔽主密钥。
func DeriveSpendingKey(seed []byte) (SpendingKey, error) {
	if len(seed) != SeedSize {
		return SpendingKey{}, fmt.Errorf("shielded: seed requires %d bytes, got %d", SeedSize, len(seed))
	}

	derived, err := deriveCanonicalFieldHKDF(seed, nil, spendingKeyDomain)
	if err != nil {
		return SpendingKey{}, fmt.Errorf("shielded: derive spending key: %w", err)
	}
	var spendingKey SpendingKey
	copy(spendingKey.masterKey[:], derived[:])
	return spendingKey, nil
}

// ParseSpendingKey 恢复已安全持久化的屏蔽主密钥。
func ParseSpendingKey(encoded []byte) (SpendingKey, error) {
	if len(encoded) != SpendingKeySize {
		return SpendingKey{}, fmt.Errorf("shielded: spending key requires %d bytes, got %d", SpendingKeySize, len(encoded))
	}
	var spendingKey SpendingKey
	copy(spendingKey.masterKey[:], encoded)
	if isAllZero(spendingKey.masterKey[:]) {
		return SpendingKey{}, fmt.Errorf("shielded: spending key cannot be zero")
	}
	if err := validateCanonicalField(spendingKey.masterKey[:]); err != nil {
		return SpendingKey{}, fmt.Errorf("shielded: invalid spending key: %w", err)
	}
	return spendingKey, nil
}

// Bytes 返回屏蔽主密钥副本，调用方必须按敏感数据保护返回值。
func (spendingKey SpendingKey) Bytes() []byte {
	encoded := make([]byte, SpendingKeySize)
	copy(encoded, spendingKey.masterKey[:])
	return encoded
}

// Equal 使用常量时间比较两个屏蔽主密钥。
func (spendingKey SpendingKey) Equal(other SpendingKey) bool {
	return subtle.ConstantTimeCompare(spendingKey.masterKey[:], other.masterKey[:]) == 1
}

// DeriveViewKey 使用屏蔽主密钥和 diversifier 派生独立查看密钥。
func (spendingKey SpendingKey) DeriveViewKey(diversifier Diversifier) (ViewKey, error) {
	if isAllZero(spendingKey.masterKey[:]) {
		return ViewKey{}, fmt.Errorf("shielded: spending key is not initialized")
	}

	saltInput := make([]byte, 0, len(viewKeySaltDomain)+DiversifierSize)
	saltInput = append(saltInput, viewKeySaltDomain...)
	saltInput = append(saltInput, diversifier[:]...)
	salt := sha256.Sum256(saltInput)

	transmissionPrivateKey, err := deriveHKDFSHA256(spendingKey.masterKey[:], salt[:], transmissionKeyDomain, TransmissionKeySize)
	if err != nil {
		return ViewKey{}, fmt.Errorf("shielded: derive transmission key: %w", err)
	}
	if isAllZero(transmissionPrivateKey) {
		return ViewKey{}, fmt.Errorf("shielded: derived transmission key cannot be zero")
	}
	spendSecret := Digest(spendingKey.masterKey)
	nullifierKey, err := mimcFieldHash(nullifierKeyTag, spendSecret)
	if err != nil {
		return ViewKey{}, fmt.Errorf("shielded: derive nullifier key: %w", err)
	}
	diversifierField, err := digestFromShortBytes(diversifier[:])
	if err != nil {
		return ViewKey{}, fmt.Errorf("shielded: encode diversifier: %w", err)
	}
	ownerTag, err := mimcFieldHash(ownerDomainTag, diversifierField, spendSecret)
	if err != nil {
		return ViewKey{}, fmt.Errorf("shielded: derive owner tag: %w", err)
	}
	if isAllZero(nullifierKey[:]) || isAllZero(ownerTag[:]) {
		return ViewKey{}, fmt.Errorf("shielded: derived field key cannot be zero")
	}

	viewKey := ViewKey{diversifier: diversifier}
	copy(viewKey.transmissionPrivateKey[:], transmissionPrivateKey)
	copy(viewKey.nullifierKey[:], nullifierKey[:])
	copy(viewKey.ownerTag[:], ownerTag[:])
	return viewKey, nil
}

// DeriveKeySet 一次性从 seed 和 diversifier 派生屏蔽主密钥、查看密钥与地址。
func DeriveKeySet(seed []byte, diversifier Diversifier) (SpendingKey, ViewKey, ShieldedAddress, error) {
	spendingKey, err := DeriveSpendingKey(seed)
	if err != nil {
		return SpendingKey{}, ViewKey{}, ShieldedAddress{}, err
	}
	viewKey, err := spendingKey.DeriveViewKey(diversifier)
	if err != nil {
		return SpendingKey{}, ViewKey{}, ShieldedAddress{}, err
	}
	address, err := viewKey.Address()
	if err != nil {
		return SpendingKey{}, ViewKey{}, ShieldedAddress{}, err
	}
	return spendingKey, viewKey, address, nil
}

// Diversifier 返回查看密钥绑定的 diversifier。
func (viewKey ViewKey) Diversifier() Diversifier {
	return viewKey.diversifier
}

// OwnerTag 返回查看密钥对应的公开所有者标签。
func (viewKey ViewKey) OwnerTag() OwnerTag {
	return viewKey.ownerTag
}

// MarshalBinary 返回查看密钥的带校验和固定长度编码，调用方必须保护该敏感数据。
func (viewKey ViewKey) MarshalBinary() ([]byte, error) {
	if isAllZero(viewKey.transmissionPrivateKey[:]) || isAllZero(viewKey.nullifierKey[:]) || isAllZero(viewKey.ownerTag[:]) {
		return nil, fmt.Errorf("shielded: view key is not initialized")
	}
	if err := validateCanonicalField(viewKey.nullifierKey[:]); err != nil {
		return nil, fmt.Errorf("shielded: invalid view nullifier key: %w", err)
	}
	if err := validateCanonicalField(viewKey.ownerTag[:]); err != nil {
		return nil, fmt.Errorf("shielded: invalid view owner tag: %w", err)
	}

	payload := make([]byte, 0, ViewKeySize-ViewKeyChecksumSize)
	payload = append(payload, ViewKeyVersion)
	payload = append(payload, viewKey.diversifier[:]...)
	payload = append(payload, viewKey.transmissionPrivateKey[:]...)
	payload = append(payload, viewKey.nullifierKey[:]...)
	payload = append(payload, viewKey.ownerTag[:]...)
	checksum := computeViewKeyChecksum(payload)
	return append(payload, checksum[:]...), nil
}

// ParseViewKey 解析并验证查看密钥持久化编码。
func ParseViewKey(encoded []byte) (ViewKey, error) {
	if len(encoded) != ViewKeySize {
		return ViewKey{}, fmt.Errorf("shielded: view key requires %d bytes, got %d", ViewKeySize, len(encoded))
	}
	if encoded[0] != ViewKeyVersion {
		return ViewKey{}, fmt.Errorf("shielded: unsupported view key version %d", encoded[0])
	}
	payload := encoded[:ViewKeySize-ViewKeyChecksumSize]
	expectedChecksum := computeViewKeyChecksum(payload)
	actualChecksum := encoded[ViewKeySize-ViewKeyChecksumSize:]
	if subtle.ConstantTimeCompare(expectedChecksum[:], actualChecksum) != 1 {
		return ViewKey{}, fmt.Errorf("shielded: view key checksum mismatch")
	}

	viewKey := ViewKey{}
	offset := 1
	copy(viewKey.diversifier[:], encoded[offset:offset+DiversifierSize])
	offset += DiversifierSize
	copy(viewKey.transmissionPrivateKey[:], encoded[offset:offset+TransmissionKeySize])
	offset += TransmissionKeySize
	copy(viewKey.nullifierKey[:], encoded[offset:offset+FieldElementSize])
	offset += FieldElementSize
	copy(viewKey.ownerTag[:], encoded[offset:offset+OwnerTagSize])
	if isAllZero(viewKey.transmissionPrivateKey[:]) || isAllZero(viewKey.nullifierKey[:]) || isAllZero(viewKey.ownerTag[:]) {
		return ViewKey{}, fmt.Errorf("shielded: view key contains zero key material")
	}
	if err := validateCanonicalField(viewKey.nullifierKey[:]); err != nil {
		return ViewKey{}, fmt.Errorf("shielded: invalid view nullifier key: %w", err)
	}
	if err := validateCanonicalField(viewKey.ownerTag[:]); err != nil {
		return ViewKey{}, fmt.Errorf("shielded: invalid view owner tag: %w", err)
	}
	if _, err := viewKey.Address(); err != nil {
		return ViewKey{}, err
	}
	return viewKey, nil
}

// Equal 使用常量时间比较查看密钥的完整持久化内容。
func (viewKey ViewKey) Equal(other ViewKey) bool {
	left, err := viewKey.MarshalBinary()
	if err != nil {
		return false
	}
	right, err := other.MarshalBinary()
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(left, right) == 1
}

func deriveHKDFSHA256(inputKeyMaterial []byte, salt []byte, domain string, outputLength int) ([]byte, error) {
	if outputLength <= 0 {
		return nil, fmt.Errorf("output length must be positive")
	}
	reader := hkdf.New(sha256.New, inputKeyMaterial, salt, []byte(domain))
	output := make([]byte, outputLength)
	if _, err := io.ReadFull(reader, output); err != nil {
		return nil, err
	}
	return output, nil
}

func deriveCanonicalFieldHKDF(inputKeyMaterial []byte, salt []byte, domain string) (Digest, error) {
	reader := hkdf.New(sha256.New, inputKeyMaterial, salt, []byte(domain))
	for attempt := 0; attempt < 256; attempt++ {
		var candidate Digest
		if _, err := io.ReadFull(reader, candidate[:]); err != nil {
			return Digest{}, err
		}
		candidate[0] &= 0x3f
		if isAllZero(candidate[:]) {
			continue
		}
		if validateCanonicalField(candidate[:]) == nil {
			return candidate, nil
		}
	}
	return Digest{}, fmt.Errorf("cannot derive non-zero canonical BN254 field element")
}

func computeViewKeyChecksum(payload []byte) [ViewKeyChecksumSize]byte {
	hasher := sha256.New()
	hasher.Write([]byte(viewKeyChecksumDomain))
	hasher.Write(payload)
	digest := hasher.Sum(nil)
	var checksum [ViewKeyChecksumSize]byte
	copy(checksum[:], digest[:ViewKeyChecksumSize])
	return checksum
}

func isAllZero(value []byte) bool {
	var combined byte
	for _, item := range value {
		combined |= item
	}
	return combined == 0
}
