package main

import "testing"

func TestValidateRPCURL(t *testing.T) {
	result, err := validateRPCURL(" http://101.35.87.31:8899/ ")
	if err != nil {
		t.Fatalf("validateRPCURL() error = %v", err)
	}
	if result != "http://101.35.87.31:8899/" {
		t.Fatalf("validateRPCURL() = %q", result)
	}
}

func TestValidateRPCURLRejectsInvalidScheme(t *testing.T) {
	if _, err := validateRPCURL("tcp://101.35.87.31:8899"); err == nil {
		t.Fatal("validateRPCURL() error = nil, want invalid scheme")
	}
}

func TestSelectProbeSeeds(t *testing.T) {
	source, destination, err := selectProbeSeeds([]string{" user-18v-01 ", "user-18v-02", "user-18v-03"}, 2, 1)
	if err != nil {
		t.Fatalf("selectProbeSeeds() error = %v", err)
	}
	if source != "user-18v-03" || destination != "user-18v-02" {
		t.Fatalf("selectProbeSeeds() = %q, %q", source, destination)
	}
}

func TestSelectProbeSeedsRejectsSameSeed(t *testing.T) {
	if _, _, err := selectProbeSeeds([]string{"user-18v-01", "user-18v-01"}, 0, 1); err == nil {
		t.Fatal("selectProbeSeeds() error = nil, want same seed error")
	}
}

func TestSelectProbeSeedsRejectsOutOfRangeIndex(t *testing.T) {
	if _, _, err := selectProbeSeeds([]string{"user-18v-01", "user-18v-02"}, 2, 1); err == nil {
		t.Fatal("selectProbeSeeds() error = nil, want out of range error")
	}
}

func TestValidateOperation(t *testing.T) {
	operation, err := validateOperation(" Delegate ")
	if err != nil {
		t.Fatalf("validateOperation() error = %v", err)
	}
	if operation != "delegate" {
		t.Fatalf("validateOperation() = %q, want delegate", operation)
	}
}

func TestValidateOperationRejectsUnknown(t *testing.T) {
	if _, err := validateOperation("stake"); err == nil {
		t.Fatal("validateOperation() error = nil, want unknown operation error")
	}
}

func TestSelectValidatorAddress(t *testing.T) {
	address, err := selectValidatorAddress(manifest{
		Validators: []validatorManifest{
			{ValidatorAddress: "BKiepHxmUBRBBPSR7Biuz1hcZhTY7cp7zNH3qufhj7y6"},
			{ValidatorAddress: "CZrai3tWo6oztD9fXzBfggJpX9PknPHWfeuNKtEeocCY"},
		},
	}, 1)
	if err != nil {
		t.Fatalf("selectValidatorAddress() error = %v", err)
	}
	if address.String() != "CZrai3tWo6oztD9fXzBfggJpX9PknPHWfeuNKtEeocCY" {
		t.Fatalf("selectValidatorAddress() = %s", address.String())
	}
}

func TestSelectValidatorAddressRejectsOutOfRangeIndex(t *testing.T) {
	if _, err := selectValidatorAddress(manifest{}, 0); err == nil {
		t.Fatal("selectValidatorAddress() error = nil, want out of range error")
	}
}
