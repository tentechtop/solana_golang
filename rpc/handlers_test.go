package rpc

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestParseBootstrapRegisterValidatorParamsAllowsDiscoveryChainID(t *testing.T) {
	request := validBootstrapRegistrationRequest()
	request.ChainID = ""
	params, err := json.Marshal([]BootstrapValidatorRegistrationRequest{request})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	parsed, rpcError := parseBootstrapRegisterValidatorParams(params)
	if rpcError != nil {
		t.Fatalf("parseBootstrapRegisterValidatorParams() error = %v", rpcError)
	}
	if parsed.ChainID != "" {
		t.Fatalf("ChainID = %q, want empty discovery chain id", parsed.ChainID)
	}
}

func TestParseBootstrapRegisterValidatorParamsRejectsMissingIdentity(t *testing.T) {
	request := validBootstrapRegistrationRequest()
	request.PeerID = ""
	params, err := json.Marshal([]BootstrapValidatorRegistrationRequest{request})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	if _, rpcError := parseBootstrapRegisterValidatorParams(params); rpcError == nil {
		t.Fatal("parseBootstrapRegisterValidatorParams() error = nil, want missing identity rejection")
	}
}

func TestValidatorPairingErrorUsesInvalidParamsForPairingValidation(t *testing.T) {
	rpcError := validatorPairingError(errors.New("posnode: bootstrap staker signature invalid"))

	if rpcError.Code != CodeInvalidParams {
		t.Fatalf("Code = %d, want %d", rpcError.Code, CodeInvalidParams)
	}
	if rpcError.Message != ErrInvalidParams.Message {
		t.Fatalf("Message = %q, want %q", rpcError.Message, ErrInvalidParams.Message)
	}
	if rpcError.Data == nil {
		t.Fatal("Data = nil, want diagnostic detail")
	}
}

func TestValidatorPairingErrorKeepsConfigFailuresInternal(t *testing.T) {
	rpcError := validatorPairingError(errors.New("posnode: write paired validator config: access denied"))

	if rpcError.Code != CodeInternalError {
		t.Fatalf("Code = %d, want %d", rpcError.Code, CodeInternalError)
	}
	if rpcError.Message != ErrInternalError.Message {
		t.Fatalf("Message = %q, want %q", rpcError.Message, ErrInternalError.Message)
	}
}

func validBootstrapRegistrationRequest() BootstrapValidatorRegistrationRequest {
	return BootstrapValidatorRegistrationRequest{
		ChainID:               "test-chain",
		NodeName:              "validator-01",
		PeerID:                "peer-id",
		AdvertisedIP:          "127.0.0.1",
		AdvertisedPort:        5101,
		Network:               "tcp",
		StakerAddress:         "staker",
		ValidatorAddress:      "validator",
		ConsensusPublicKey:    "consensus",
		BLSPublicKeyBase64:    "bls",
		StakeLamports:         1,
		RegisteredAtUnixMilli: 1,
		StakerSignature:       "staker-signature",
		Signature:             "signature",
	}
}
