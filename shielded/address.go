package shielded

import (
	"crypto/ecdh"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"
)

const (
	// ShieldedAddressVersion 是当前屏蔽地址的协议版本。
	ShieldedAddressVersion uint8 = 2
	// AddressChecksumSize 是屏蔽地址校验和长度。
	AddressChecksumSize = 4
	// ShieldedAddressSize 是屏蔽地址的固定二进制长度。
	ShieldedAddressSize = 1 + DiversifierSize + OwnerTagSize + TransmissionKeySize + AddressChecksumSize
	// ShieldedAddressPrefix 用于区分屏蔽地址和透明账户地址。
	ShieldedAddressPrefix = "zs2_"
)

const addressChecksumDomain = "solana_golang.shielded.address_checksum.v2"

// TransmissionKey 是地址携带的 X25519 收款公钥。
type TransmissionKey [TransmissionKeySize]byte

// AddressChecksum 用于发现地址复制或传输过程中的意外损坏。
type AddressChecksum [AddressChecksumSize]byte

// ShieldedAddress 是包含版本、diversifier、owner tag、X25519 公钥和校验和的收款地址。
type ShieldedAddress struct {
	Version         uint8
	Diversifier     Diversifier
	OwnerTag        OwnerTag
	TransmissionKey TransmissionKey
	Checksum        AddressChecksum
}

// Address 从查看密钥生成 X25519 收款地址。
func (viewKey ViewKey) Address() (ShieldedAddress, error) {
	if isAllZero(viewKey.transmissionPrivateKey[:]) || isAllZero(viewKey.nullifierKey[:]) {
		return ShieldedAddress{}, fmt.Errorf("shielded: view key is not initialized")
	}
	privateKey, err := ecdh.X25519().NewPrivateKey(viewKey.transmissionPrivateKey[:])
	if err != nil {
		return ShieldedAddress{}, fmt.Errorf("shielded: parse transmission private key: %w", err)
	}

	address := ShieldedAddress{
		Version:     ShieldedAddressVersion,
		Diversifier: viewKey.diversifier,
		OwnerTag:    viewKey.ownerTag,
	}
	copy(address.TransmissionKey[:], privateKey.PublicKey().Bytes())
	address.Checksum = computeAddressChecksum(address.payload())
	return address, nil
}

// Validate 验证地址版本、公钥和校验和。
func (address ShieldedAddress) Validate() error {
	if address.Version != ShieldedAddressVersion {
		return fmt.Errorf("shielded: unsupported address version %d", address.Version)
	}
	if isAllZero(address.OwnerTag[:]) {
		return fmt.Errorf("shielded: owner tag cannot be zero")
	}
	if err := validateCanonicalField(address.OwnerTag[:]); err != nil {
		return fmt.Errorf("shielded: invalid owner tag: %w", err)
	}
	if err := validateTransmissionKey(address.TransmissionKey); err != nil {
		return err
	}
	expectedChecksum := computeAddressChecksum(address.payload())
	if subtle.ConstantTimeCompare(address.Checksum[:], expectedChecksum[:]) != 1 {
		return fmt.Errorf("shielded: address checksum mismatch")
	}
	return nil
}

// MarshalBinary 返回地址的固定长度规范编码。
func (address ShieldedAddress) MarshalBinary() ([]byte, error) {
	if err := address.Validate(); err != nil {
		return nil, err
	}
	encoded := make([]byte, 0, ShieldedAddressSize)
	encoded = append(encoded, address.payload()...)
	encoded = append(encoded, address.Checksum[:]...)
	return encoded, nil
}

// ParseShieldedAddress 解析并验证固定长度屏蔽地址。
func ParseShieldedAddress(encoded []byte) (ShieldedAddress, error) {
	if len(encoded) != ShieldedAddressSize {
		return ShieldedAddress{}, fmt.Errorf("shielded: address requires %d bytes, got %d", ShieldedAddressSize, len(encoded))
	}

	address := ShieldedAddress{Version: encoded[0]}
	offset := 1
	copy(address.Diversifier[:], encoded[offset:offset+DiversifierSize])
	offset += DiversifierSize
	copy(address.OwnerTag[:], encoded[offset:offset+OwnerTagSize])
	offset += OwnerTagSize
	copy(address.TransmissionKey[:], encoded[offset:offset+TransmissionKeySize])
	offset += TransmissionKeySize
	copy(address.Checksum[:], encoded[offset:offset+AddressChecksumSize])
	if err := address.Validate(); err != nil {
		return ShieldedAddress{}, err
	}
	return address, nil
}

// String 返回带独立前缀的 URL 安全地址文本。
func (address ShieldedAddress) String() string {
	encoded, err := address.MarshalBinary()
	if err != nil {
		return ""
	}
	return ShieldedAddressPrefix + base64.RawURLEncoding.EncodeToString(encoded)
}

// ParseShieldedAddressString 解析并验证文本屏蔽地址。
func ParseShieldedAddressString(value string) (ShieldedAddress, error) {
	if !strings.HasPrefix(value, ShieldedAddressPrefix) {
		return ShieldedAddress{}, fmt.Errorf("shielded: address requires prefix %q", ShieldedAddressPrefix)
	}
	encoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, ShieldedAddressPrefix))
	if err != nil {
		return ShieldedAddress{}, fmt.Errorf("shielded: decode address: %w", err)
	}
	address, err := ParseShieldedAddress(encoded)
	if err != nil {
		return ShieldedAddress{}, err
	}
	if address.String() != value {
		return ShieldedAddress{}, fmt.Errorf("shielded: address text is not canonical")
	}
	return address, nil
}

// OwnsAddress 判断查看密钥是否完整匹配指定地址。
func (viewKey ViewKey) OwnsAddress(address ShieldedAddress) bool {
	if err := address.Validate(); err != nil {
		return false
	}
	expected, err := viewKey.Address()
	if err != nil {
		return false
	}
	expectedBytes, err := expected.MarshalBinary()
	if err != nil {
		return false
	}
	actualBytes, err := address.MarshalBinary()
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(expectedBytes, actualBytes) == 1
}

func (address ShieldedAddress) payload() []byte {
	payload := make([]byte, 0, ShieldedAddressSize-AddressChecksumSize)
	payload = append(payload, address.Version)
	payload = append(payload, address.Diversifier[:]...)
	payload = append(payload, address.OwnerTag[:]...)
	payload = append(payload, address.TransmissionKey[:]...)
	return payload
}

func computeAddressChecksum(payload []byte) AddressChecksum {
	hasher := sha256.New()
	hasher.Write([]byte(addressChecksumDomain))
	hasher.Write(payload)
	digest := hasher.Sum(nil)
	var checksum AddressChecksum
	copy(checksum[:], digest[:AddressChecksumSize])
	return checksum
}

func validateTransmissionKey(transmissionKey TransmissionKey) error {
	if isAllZero(transmissionKey[:]) {
		return fmt.Errorf("shielded: transmission key cannot be zero")
	}
	publicKey, err := ecdh.X25519().NewPublicKey(transmissionKey[:])
	if err != nil {
		return fmt.Errorf("shielded: parse transmission key: %w", err)
	}
	probeMaterial := [TransmissionKeySize]byte{1}
	privateKey, err := ecdh.X25519().NewPrivateKey(probeMaterial[:])
	if err != nil {
		return fmt.Errorf("shielded: create transmission validation key: %w", err)
	}
	if _, err := privateKey.ECDH(publicKey); err != nil {
		return fmt.Errorf("shielded: invalid transmission key: %w", err)
	}
	return nil
}
