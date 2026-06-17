package consensus

import (
	"strings"
	"testing"
)

func TestHardcodedGenesisTreasuryPublicKey(t *testing.T) {
	publicKey, err := HardcodedGenesisTreasuryPublicKey()
	if err != nil {
		t.Fatalf("HardcodedGenesisTreasuryPublicKey() error = %v", err)
	}
	if publicKey.String() != HardcodedGenesisTreasuryPublicKeyBase58 {
		t.Fatalf("public key = %s, want %s", publicKey.String(), HardcodedGenesisTreasuryPublicKeyBase58)
	}
}

func TestHardcodedGenesisTreasuryKeyPairRejected(t *testing.T) {
	_, err := HardcodedGenesisTreasuryKeyPair()
	if err == nil {
		t.Fatal("HardcodedGenesisTreasuryKeyPair() error = nil, want private key rejection")
	}
	if !strings.Contains(err.Error(), "private key is not embedded") {
		t.Fatalf("HardcodedGenesisTreasuryKeyPair() error = %v, want private key rejection", err)
	}
}
