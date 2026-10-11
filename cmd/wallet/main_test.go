package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/tentechtop/ordexus-core/consensus"
	"github.com/tentechtop/ordexus-core/structure"
	"github.com/tentechtop/ordexus-core/utils"
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

func TestParseContractAccountsPreservesPermissions(t *testing.T) {
	readonly, err := structure.KeyPairFromSeed(utils.SHA256([]byte("wallet-contract-readonly")))
	if err != nil {
		t.Fatalf("KeyPairFromSeed(readonly) error = %v", err)
	}
	signerWritable, err := structure.KeyPairFromSeed(utils.SHA256([]byte("wallet-contract-signer")))
	if err != nil {
		t.Fatalf("KeyPairFromSeed(signer) error = %v", err)
	}
	accounts, err := parseContractAccounts([]string{
		readonly.PublicKey.String() + ":r",
		signerWritable.PublicKey.String() + ":sw",
	})
	if err != nil {
		t.Fatalf("parseContractAccounts() error = %v", err)
	}
	if len(accounts) != 2 || accounts[0].IsSigner || accounts[0].IsWritable || !accounts[1].IsSigner || !accounts[1].IsWritable {
		t.Fatalf("accounts = %+v", accounts)
	}
}

func TestContractInstructionDataRejectsMultipleSources(t *testing.T) {
	if _, err := contractInstructionData("0102", "AQI=", ""); err == nil {
		t.Fatal("contractInstructionData() error = nil, want multiple source rejection")
	}
	data, err := contractInstructionData("0102", "", "")
	if err != nil {
		t.Fatalf("contractInstructionData(hex) error = %v", err)
	}
	if len(data) != 2 || data[0] != 1 || data[1] != 2 {
		t.Fatalf("hex data = %v, want [1 2]", data)
	}
}

func TestWaitTransactionConfirmed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		defer request.Body.Close()
		var requestPayload rpcRequest
		if err := json.NewDecoder(request.Body).Decode(&requestPayload); err != nil {
			t.Errorf("decode request: %v", err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		if requestPayload.Method != "getTransaction" {
			t.Errorf("method = %s, want getTransaction", requestPayload.Method)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"found":true,"status":"confirmed"}}`))
	}))
	defer server.Close()

	if err := waitTransactionConfirmed(server.URL, "signature", time.Second); err != nil {
		t.Fatalf("waitTransactionConfirmed() error = %v", err)
	}
}

func TestSignEmergencyRecoveryCertificateRejectsDuplicateSigner(t *testing.T) {
	signer, err := structure.KeyPairFromSeed(utils.SHA256([]byte("wallet-recovery-governance")))
	if err != nil {
		t.Fatalf("KeyPairFromSeed() error = %v", err)
	}
	parentHash, err := structure.NewHash(utils.SHA256([]byte("wallet-recovery-parent")))
	if err != nil {
		t.Fatalf("NewHash() error = %v", err)
	}
	certificate := consensus.EmergencyRecoveryCertificate{
		Version:                consensus.EmergencyRecoveryCertificateVersion,
		ChainID:                "recovery-test",
		Sequence:               1,
		FinalizedHeight:        3,
		FinalizedHash:          parentHash.String(),
		SafetyEpochID:          1,
		SafetyValidatorSetRoot: parentHash.String(),
		ElderValidatorID:       "elder-a",
		ExpiresAtUnixMilli:     time.Now().Add(time.Minute).UnixMilli(),
	}
	if err := signEmergencyRecoveryCertificate(&certificate, signer); err != nil {
		t.Fatalf("signEmergencyRecoveryCertificate() error = %v", err)
	}
	if len(certificate.Signatures) != 1 {
		t.Fatalf("signature count = %d, want 1", len(certificate.Signatures))
	}
	signBytes, err := certificate.SignBytes()
	if err != nil {
		t.Fatalf("SignBytes() error = %v", err)
	}
	signature, err := structure.SignatureFromBase58(certificate.Signatures[0].Signature)
	if err != nil || !structure.VerifyMessageSignature(signer.PublicKey, signBytes, signature) {
		t.Fatal("governance signature is invalid")
	}
	if err := signEmergencyRecoveryCertificate(&certificate, signer); err == nil {
		t.Fatal("duplicate signer error = nil")
	}
}

func TestSignMultiValidatorActivationCertificateRejectsDuplicateSigner(t *testing.T) {
	signer, err := structure.KeyPairFromSeed(utils.SHA256([]byte("wallet-activation-validator")))
	if err != nil {
		t.Fatalf("KeyPairFromSeed() error = %v", err)
	}
	checkpoint, err := structure.NewHash(utils.SHA256([]byte("wallet-activation-checkpoint")))
	if err != nil {
		t.Fatalf("NewHash() error = %v", err)
	}
	certificate := consensus.MultiValidatorActivationCertificate{
		Version:                consensus.MultiValidatorActivationCertificateVersion,
		ChainID:                "activation-test",
		Sequence:               2,
		SafetyFinalizedHeight:  3,
		SafetyFinalizedHash:    checkpoint.String(),
		SafetyEpochID:          1,
		SafetyValidatorSetRoot: checkpoint.String(),
		TargetHeight:           8,
		TargetHash:             checkpoint.String(),
		TargetEpochID:          2,
		TargetValidatorSetRoot: checkpoint.String(),
		ExpiresAtUnixMilli:     time.Now().Add(time.Minute).UnixMilli(),
	}
	if err := signMultiValidatorActivationCertificate(&certificate, signer); err != nil {
		t.Fatalf("signMultiValidatorActivationCertificate() error = %v", err)
	}
	if len(certificate.Signatures) != 1 || certificate.Signatures[0].ValidatorID != string(consensus.NewValidatorID(signer.PublicKey)) {
		t.Fatalf("activation signatures = %+v", certificate.Signatures)
	}
	if err := signMultiValidatorActivationCertificate(&certificate, signer); err == nil {
		t.Fatal("duplicate activation signer error = nil")
	}
}

func TestValidatorPairSubmitsRegistrationWhenBootstrapManifestReady(t *testing.T) {
	validatorKey, err := structure.KeyPairFromSeed(utils.SHA256([]byte("wallet-pair-validator")))
	if err != nil {
		t.Fatalf("KeyPairFromSeed(validator) error = %v", err)
	}
	consensusKey, err := structure.KeyPairFromSeed(utils.SHA256([]byte("wallet-pair-consensus")))
	if err != nil {
		t.Fatalf("KeyPairFromSeed(consensus) error = %v", err)
	}
	blsKey, err := consensus.BLSKeyPairFromSeed(utils.SHA256([]byte("wallet-pair-bls")))
	if err != nil {
		t.Fatalf("BLSKeyPairFromSeed() error = %v", err)
	}
	blockhash, err := structure.NewHash(utils.SHA256([]byte("wallet-pair-blockhash")))
	if err != nil {
		t.Fatalf("NewHash() error = %v", err)
	}

	methodCalls := make(map[string]int)
	completedSignature := ""
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		decoded := rpcRequest{}
		if err := json.NewDecoder(request.Body).Decode(&decoded); err != nil {
			t.Errorf("Decode(request) error = %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		methodCalls[decoded.Method]++
		response := map[string]any{"jsonrpc": "2.0", "id": decoded.ID}
		switch decoded.Method {
		case "getBootstrapManifest":
			response["result"] = bootstrapManifestStatusResult{Ready: true}
		case "getLatestBlockhash":
			response["result"] = latestBlockhashResult{Blockhash: blockhash.String(), Slot: 1}
		case "sendTransaction":
			response["result"] = "registration-signature"
		case "completeValidatorPairing":
			requestData, marshalErr := json.Marshal(decoded.Params[0])
			if marshalErr != nil {
				t.Errorf("Marshal(complete request) error = %v", marshalErr)
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			completeRequest := validatorPairingCompleteRequest{}
			if err := json.Unmarshal(requestData, &completeRequest); err != nil {
				t.Errorf("Unmarshal(complete request) error = %v", err)
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			completedSignature = completeRequest.Signature
			response["result"] = validatorPairingCompleteResult{State: "completed", Signature: completeRequest.Signature}
		default:
			t.Errorf("unexpected rpc method %q", decoded.Method)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(writer).Encode(response); err != nil {
			t.Errorf("Encode(response) error = %v", err)
		}
	}))
	defer server.Close()

	payload := validatorPairingPayload{
		Version:            1,
		Mode:               validatorPairingModeBootstrap,
		RPCURL:             server.URL,
		BootstrapRPCURL:    server.URL,
		ChainID:            "test-chain",
		NodeName:           "new-validator",
		NodePeerID:         "new-validator-peer",
		AdvertisedIP:       "127.0.0.1",
		AdvertisedPort:     5102,
		Network:            "tcp",
		ValidatorAddress:   validatorKey.PublicKey.String(),
		ConsensusAddress:   consensusKey.PublicKey.String(),
		BLSPublicKey:       utils.Base58Encode(blsKey.PublicKey),
		RegisteredAtUnixMS: time.Now().UnixMilli(),
		Token:              "pairing-token",
		ExpiresAtUnixMS:    time.Now().Add(time.Minute).UnixMilli(),
	}
	payloadData, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal(payload) error = %v", err)
	}
	stakerPath := filepath.Join(t.TempDir(), "staker.json")
	if err := writeEd25519Keystore(stakerPath); err != nil {
		t.Fatalf("writeEd25519Keystore() error = %v", err)
	}

	err = runValidatorPair([]string{
		"-payload", validatorPairingPayloadPrefix + utils.Base64RawEncode(payloadData),
		"-staker-key", stakerPath,
		"-lamports", "10000000",
	})
	if err != nil {
		t.Fatalf("runValidatorPair() error = %v", err)
	}
	if methodCalls["sendTransaction"] != 1 || methodCalls["completeValidatorPairing"] != 1 {
		t.Fatalf("rpc calls = %+v, want one registration and one completion", methodCalls)
	}
	if completedSignature != "registration-signature" {
		t.Fatalf("completed signature = %q, want registration-signature", completedSignature)
	}
}
