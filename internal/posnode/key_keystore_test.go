package posnode

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"solana_golang/consensus"
	"solana_golang/structure"
	"solana_golang/utils"
)

func TestLoadRawKeyPairFromKeystore(t *testing.T) {
	seed := utils.SHA256([]byte("peer-keystore-test"))
	publicKey, err := utils.DeriveEd25519PublicKeyFromPrivateKey(seed)
	if err != nil {
		t.Fatalf("DeriveEd25519PublicKeyFromPrivateKey() error = %v", err)
	}
	path := writeNodeKeystore(t, `{"private_key_base64":"`+base64.StdEncoding.EncodeToString(seed)+`","public_key_base64":"`+base64.StdEncoding.EncodeToString(publicKey)+`"}`)

	keyPair, err := loadRawKeyPair("", path, nodeConfig{}, "peer")
	if err != nil {
		t.Fatalf("loadRawKeyPair() error = %v", err)
	}
	if keyPair.peerID != utils.Base58Encode(publicKey) || !bytes.Equal(keyPair.privateKey, seed) {
		t.Fatal("raw keypair does not match keystore")
	}
}

func TestLoadStructureKeyPairFromKeystore(t *testing.T) {
	expected, err := structure.KeyPairFromSeed(utils.SHA256([]byte("staker-keystore-test")))
	if err != nil {
		t.Fatalf("KeyPairFromSeed() error = %v", err)
	}
	path := writeNodeKeystore(t, `{"secret_key_base64":"`+base64.StdEncoding.EncodeToString(expected.SecretKey64())+`"}`)

	keyPair, err := loadStructureKeyPair("", path, nodeConfig{}, "staker")
	if err != nil {
		t.Fatalf("loadStructureKeyPair() error = %v", err)
	}
	if keyPair.PublicKey != expected.PublicKey || !bytes.Equal(keyPair.PrivateKey, expected.PrivateKey) {
		t.Fatal("solana keypair does not match keystore")
	}
}

func TestLoadBLSKeyPairFromPrivateKeystore(t *testing.T) {
	expected, err := consensus.BLSKeyPairFromSeed(utils.SHA256([]byte("bls-keystore-test")))
	if err != nil {
		t.Fatalf("BLSKeyPairFromSeed() error = %v", err)
	}
	path := writeNodeKeystore(t, `{"private_key_base64":"`+base64.StdEncoding.EncodeToString(expected.PrivateKey)+`"}`)

	keyPair, err := loadBLSKeyPair("", path, nodeConfig{})
	if err != nil {
		t.Fatalf("loadBLSKeyPair() error = %v", err)
	}
	if !bytes.Equal(keyPair.PublicKey, expected.PublicKey) || !bytes.Equal(keyPair.PrivateKey, expected.PrivateKey) {
		t.Fatal("bls keypair does not match keystore")
	}
}

func writeNodeKeystore(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "node-key.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write node keystore: %v", err)
	}
	return path
}
