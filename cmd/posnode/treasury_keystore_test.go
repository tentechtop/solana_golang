package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"solana_golang/structure"
	"solana_golang/utils"
)

func TestLoadTreasuryKeyPairFromSeedKeystore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "treasury.json")
	if err := os.WriteFile(path, []byte(`{"seed":"test treasury seed"}`), 0o600); err != nil {
		t.Fatalf("write keystore: %v", err)
	}
	keyPair, err := loadTreasuryKeyPairFromFile(path, false)
	if err != nil {
		t.Fatalf("loadTreasuryKeyPairFromFile() error = %v", err)
	}
	expected, err := structure.KeyPairFromSeed(utils.SHA256([]byte("test treasury seed")))
	if err != nil {
		t.Fatalf("KeyPairFromSeed() error = %v", err)
	}
	if keyPair.PublicKey != expected.PublicKey || !bytes.Equal(keyPair.PrivateKey, expected.PrivateKey) {
		t.Fatal("loaded treasury keypair does not match expected seed")
	}
}

func TestTreasuryKeyPairRequiresKeystore(t *testing.T) {
	allowHardcoded := true
	node := &posNode{config: nodeConfig{AllowHardcodedTreasury: &allowHardcoded}}
	_, _, err := node.treasuryKeyPair()
	if err == nil {
		t.Fatal("treasuryKeyPair() error = nil, want required keystore error")
	}
	if !strings.Contains(err.Error(), "treasury keystore is required") {
		t.Fatalf("treasuryKeyPair() error = %v, want required keystore error", err)
	}
}
