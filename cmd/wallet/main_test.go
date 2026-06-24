package main

import (
	"encoding/json"
	"testing"
	"time"

	"solana_golang/utils"
)

func TestLoadValidatorPairingPayload(t *testing.T) {
	payload := validatorPairingPayload{
		Version:           1,
		RPCURL:            "http://192.168.1.10:9110/",
		ChainID:           "test-chain",
		ChainIdentityHash: "chain-hash",
		GenesisHash:       "genesis-hash",
		NodePeerID:        "peer-id",
		ValidatorAddress:  "validator",
		ConsensusAddress:  "consensus",
		BLSPublicKey:      "bls",
		Token:             "token",
		ExpiresAtUnixMS:   time.Now().Add(time.Minute).UnixMilli(),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	decoded, err := loadValidatorPairingPayload(validatorPairingPayloadPrefix+utils.Base64RawEncode(data), "")
	if err != nil {
		t.Fatalf("loadValidatorPairingPayload() error = %v", err)
	}
	if decoded.RPCURL != payload.RPCURL || decoded.Token != payload.Token {
		t.Fatalf("decoded payload = %+v, want %+v", decoded, payload)
	}
}

func TestLoadValidatorPairingPayloadRejectsBadPrefix(t *testing.T) {
	if _, err := loadValidatorPairingPayload("http://192.168.1.10", ""); err == nil {
		t.Fatal("loadValidatorPairingPayload() error = nil, want bad prefix rejection")
	}
}

func TestValidateWalletRPCURLRejectsUnsafeURL(t *testing.T) {
	if err := validateWalletRPCURL("http://user:pass@127.0.0.1:8899/"); err == nil {
		t.Fatal("validateWalletRPCURL(userinfo) error = nil, want rejection")
	}
	if err := validateWalletRPCURL("file:///tmp/rpc"); err == nil {
		t.Fatal("validateWalletRPCURL(file) error = nil, want rejection")
	}
}

func TestTransactionSubmitResultAcceptsStringAndObject(t *testing.T) {
	var stringResult transactionSubmitResult
	if err := json.Unmarshal([]byte(`"sig-string"`), &stringResult); err != nil {
		t.Fatalf("Unmarshal(string) error = %v", err)
	}
	if stringResult.Signature != "sig-string" {
		t.Fatalf("string signature = %q, want sig-string", stringResult.Signature)
	}
	var objectResult transactionSubmitResult
	if err := json.Unmarshal([]byte(`{"signature":"sig-object"}`), &objectResult); err != nil {
		t.Fatalf("Unmarshal(object) error = %v", err)
	}
	if objectResult.Signature != "sig-object" {
		t.Fatalf("object signature = %q, want sig-object", objectResult.Signature)
	}
}
