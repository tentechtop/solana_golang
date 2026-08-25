package shielded

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestDeriveKeySetDeterministicAndIndependent(t *testing.T) {
	seed := bytes.Repeat([]byte{0x42}, SeedSize)
	diversifier := testDiversifier(0x11)
	spendingKey, viewKey, address, err := DeriveKeySet(seed, diversifier)
	if err != nil {
		t.Fatalf("DeriveKeySet() error = %v", err)
	}

	secondSpendingKey, secondViewKey, secondAddress, err := DeriveKeySet(seed, diversifier)
	if err != nil {
		t.Fatalf("DeriveKeySet() second error = %v", err)
	}
	if !spendingKey.Equal(secondSpendingKey) || !viewKey.Equal(secondViewKey) || address != secondAddress {
		t.Fatal("相同 seed 和 diversifier 未得到确定性密钥集合")
	}
	if err := validateCanonicalField(spendingKey.Bytes()); err != nil {
		t.Fatalf("spending key is not canonical field: %v", err)
	}
	if address.Version != ShieldedAddressVersion || address.Diversifier != diversifier || address.OwnerTag != viewKey.OwnerTag() {
		t.Fatal("地址未包含派生版本、diversifier 或 owner tag")
	}

	differentViewKey, err := spendingKey.DeriveViewKey(testDiversifier(0x12))
	if err != nil {
		t.Fatalf("DeriveViewKey() error = %v", err)
	}
	differentAddress, err := differentViewKey.Address()
	if err != nil {
		t.Fatalf("Address() error = %v", err)
	}
	if viewKey.Equal(differentViewKey) || address == differentAddress {
		t.Fatal("不同 diversifier 未隔离查看密钥和地址")
	}
	if viewKey.OwnsAddress(differentAddress) {
		t.Fatal("查看密钥接受了其他 diversifier 的地址")
	}

	differentSeed := bytes.Repeat([]byte{0x43}, SeedSize)
	differentSpendingKey, _, _, err := DeriveKeySet(differentSeed, diversifier)
	if err != nil {
		t.Fatalf("DeriveKeySet() with different seed error = %v", err)
	}
	if spendingKey.Equal(differentSpendingKey) {
		t.Fatal("不同 seed 派生出相同 SpendingKey")
	}

}

func TestKeyEncodingRoundTripAndTamperRejection(t *testing.T) {
	spendingKey, viewKey, _, err := DeriveKeySet(bytes.Repeat([]byte{0x21}, SeedSize), testDiversifier(0x31))
	if err != nil {
		t.Fatalf("DeriveKeySet() error = %v", err)
	}

	parsedSpendingKey, err := ParseSpendingKey(spendingKey.Bytes())
	if err != nil {
		t.Fatalf("ParseSpendingKey() error = %v", err)
	}
	if !parsedSpendingKey.Equal(spendingKey) {
		t.Fatal("SpendingKey 编码往返不一致")
	}
	viewKeyBytes, err := viewKey.MarshalBinary()
	if err != nil {
		t.Fatalf("ViewKey.MarshalBinary() error = %v", err)
	}
	parsedViewKey, err := ParseViewKey(viewKeyBytes)
	if err != nil {
		t.Fatalf("ParseViewKey() error = %v", err)
	}
	if !parsedViewKey.Equal(viewKey) {
		t.Fatal("ViewKey 编码往返不一致")
	}

	tamperedViewKey := cloneBytes(viewKeyBytes)
	tamperedViewKey[20] ^= 0x01
	if _, err := ParseViewKey(tamperedViewKey); err == nil {
		t.Fatal("ParseViewKey() 接受被篡改的编码")
	}
	if _, err := ParseViewKey(viewKeyBytes[:len(viewKeyBytes)-1]); err == nil {
		t.Fatal("ParseViewKey() 接受错误长度")
	}
	if _, err := ParseSpendingKey(make([]byte, SpendingKeySize)); err == nil {
		t.Fatal("ParseSpendingKey() 接受零密钥")
	}
	if _, err := ParseSpendingKey(bytes.Repeat([]byte{0xff}, SpendingKeySize)); err == nil {
		t.Fatal("ParseSpendingKey() 接受非规范域元素")
	}
	if _, err := DeriveSpendingKey(make([]byte, SeedSize-1)); err == nil {
		t.Fatal("DeriveSpendingKey() 接受错误 seed 长度")
	}
	if _, err := NewDiversifier(make([]byte, DiversifierSize-1)); err == nil {
		t.Fatal("NewDiversifier() 接受错误长度")
	}
}

func TestShieldedAddressEncodingAndTamperRejection(t *testing.T) {
	_, viewKey, address, err := DeriveKeySet(bytes.Repeat([]byte{0x51}, SeedSize), testDiversifier(0x61))
	if err != nil {
		t.Fatalf("DeriveKeySet() error = %v", err)
	}
	encoded, err := address.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	if len(encoded) != ShieldedAddressSize {
		t.Fatalf("address size = %d, want %d", len(encoded), ShieldedAddressSize)
	}
	if encoded[0] != ShieldedAddressVersion {
		t.Fatalf("address version = %d, want %d", encoded[0], ShieldedAddressVersion)
	}
	if !bytes.Equal(encoded[1:1+DiversifierSize], address.Diversifier[:]) {
		t.Fatal("地址编码未包含 diversifier")
	}
	ownerTagOffset := 1 + DiversifierSize
	if !bytes.Equal(encoded[ownerTagOffset:ownerTagOffset+OwnerTagSize], address.OwnerTag[:]) {
		t.Fatal("地址编码未包含 owner tag")
	}
	transmissionKeyOffset := ownerTagOffset + OwnerTagSize
	if !bytes.Equal(encoded[transmissionKeyOffset:transmissionKeyOffset+TransmissionKeySize], address.TransmissionKey[:]) {
		t.Fatal("地址编码未包含 X25519 transmission key")
	}

	parsed, err := ParseShieldedAddress(encoded)
	if err != nil {
		t.Fatalf("ParseShieldedAddress() error = %v", err)
	}
	if parsed != address || !viewKey.OwnsAddress(parsed) {
		t.Fatal("屏蔽地址二进制往返或归属验证失败")
	}
	textAddress := address.String()
	parsedText, err := ParseShieldedAddressString(textAddress)
	if err != nil {
		t.Fatalf("ParseShieldedAddressString() error = %v", err)
	}
	if parsedText != address {
		t.Fatal("屏蔽地址文本往返不一致")
	}

	tampered := cloneBytes(encoded)
	tampered[ownerTagOffset] ^= 0x01
	if _, err := ParseShieldedAddress(tampered); err == nil {
		t.Fatal("ParseShieldedAddress() 接受错误校验和")
	}
	tampered = cloneBytes(encoded)
	tampered[0]++
	if _, err := ParseShieldedAddress(tampered); err == nil {
		t.Fatal("ParseShieldedAddress() 接受未知版本")
	}
	zeroTransmissionAddress := address
	zeroTransmissionAddress.TransmissionKey = TransmissionKey{}
	zeroTransmissionAddress.Checksum = computeAddressChecksum(zeroTransmissionAddress.payload())
	if err := zeroTransmissionAddress.Validate(); err == nil {
		t.Fatal("ShieldedAddress.Validate() 接受零 transmission key")
	}
	nonCanonicalOwnerAddress := address
	for index := range nonCanonicalOwnerAddress.OwnerTag {
		nonCanonicalOwnerAddress.OwnerTag[index] = 0xff
	}
	nonCanonicalOwnerAddress.Checksum = computeAddressChecksum(nonCanonicalOwnerAddress.payload())
	if err := nonCanonicalOwnerAddress.Validate(); err == nil {
		t.Fatal("ShieldedAddress.Validate() 接受非规范 owner tag")
	}
	if _, err := ParseShieldedAddressString("transparent_" + textAddress); err == nil {
		t.Fatal("ParseShieldedAddressString() 接受错误前缀")
	}
	rawText := base64.RawURLEncoding.EncodeToString(encoded)
	if _, err := ParseShieldedAddressString(ShieldedAddressPrefix + rawText + "="); err == nil {
		t.Fatal("ParseShieldedAddressString() 接受非规范 base64")
	}
}

func testDiversifier(start byte) Diversifier {
	var diversifier Diversifier
	for index := range diversifier {
		diversifier[index] = start + byte(index)
	}
	return diversifier
}
