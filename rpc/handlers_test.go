package rpc

import (
	"encoding/json"
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
