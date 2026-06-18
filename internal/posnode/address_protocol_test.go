package posnode

import (
	"testing"

	"solana_golang/structure"
	"solana_golang/utils"
)

func TestDecodeProtocolPublicKeyUsesPayloadType(t *testing.T) {
	rawKey := make([]byte, structure.PublicKeySize)
	rawKey[31] = 7
	transparentAddress := encodeTestProtocolAddress(protocolAddressTransparent, rawKey, "t")
	privacyAddress := encodeTestProtocolAddress(protocolAddressPrivacy, rawKey, "z")

	transparentKey, transparentType, err := decodeProtocolPublicKey(transparentAddress, "transparent")
	if err != nil {
		t.Fatalf("decode transparent address error = %v", err)
	}
	if transparentType != protocolAddressTransparent {
		t.Fatalf("transparent type = %d, want %d", transparentType, protocolAddressTransparent)
	}
	if transparentKey.String() != utils.Base58Encode(rawKey) {
		t.Fatalf("transparent key = %s, want %s", transparentKey.String(), utils.Base58Encode(rawKey))
	}

	_, privacyType, err := decodeProtocolPublicKey(privacyAddress, "privacy")
	if err != nil {
		t.Fatalf("decode privacy address error = %v", err)
	}
	if privacyType != protocolAddressPrivacy {
		t.Fatalf("privacy type = %d, want %d", privacyType, protocolAddressPrivacy)
	}
}

func TestDecodeProtocolPublicKeyRejectsPrefixMismatch(t *testing.T) {
	rawKey := make([]byte, structure.PublicKeySize)
	rawKey[31] = 9
	forgedAddress := encodeTestProtocolAddress(protocolAddressPrivacy, rawKey, "t")

	if _, _, err := decodeProtocolPublicKey(forgedAddress, "forged"); err == nil {
		t.Fatal("decode forged address error = nil, want error")
	}
}

func encodeTestProtocolAddress(addressType byte, rawKey []byte, prefix string) string {
	payload := make([]byte, protocolAddressSize)
	payload[0] = addressType
	copy(payload[1:], rawKey)
	return prefix + utils.Base58Encode(payload)
}
