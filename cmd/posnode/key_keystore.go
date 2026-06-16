package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"solana_golang/consensus"
	"solana_golang/structure"
	"solana_golang/utils"
)

const maxNodeKeystoreBytes = 8192

type nodeKeystoreFile struct {
	Seed             string `json:"seed,omitempty"`
	SeedBase64       string `json:"seed_base64,omitempty"`
	PrivateKeyBase64 string `json:"private_key_base64,omitempty"`
	SecretKeyBase64  string `json:"secret_key_base64,omitempty"`
	PublicKeyBase64  string `json:"public_key_base64,omitempty"`
}

func loadRawKeyPair(seedText string, keyPath string, config nodeConfig, purpose string) (rawKeyPair, error) {
	if strings.TrimSpace(keyPath) != "" {
		keyFile, err := loadNodeKeystoreFile(keyPath, isProductionNodeConfig(config))
		if err != nil {
			return rawKeyPair{}, err
		}
		return rawKeyPairFromKeystore(keyFile, purpose)
	}
	if isProductionNodeConfig(config) {
		return rawKeyPair{}, fmt.Errorf("posnode: %s key path is required in production", purpose)
	}
	return rawKeyPairFromSeedText(seedText)
}

func loadStructureKeyPair(seedText string, keyPath string, config nodeConfig, purpose string) (structure.SolanaKeyPair, error) {
	if strings.TrimSpace(keyPath) != "" {
		keyFile, err := loadNodeKeystoreFile(keyPath, isProductionNodeConfig(config))
		if err != nil {
			return structure.SolanaKeyPair{}, err
		}
		keyPair, err := solanaKeyPairFromKeystore(keyFile)
		if err != nil {
			return structure.SolanaKeyPair{}, fmt.Errorf("posnode: load %s keypair: %w", purpose, err)
		}
		return keyPair, nil
	}
	if isProductionNodeConfig(config) {
		return structure.SolanaKeyPair{}, fmt.Errorf("posnode: %s key path is required in production", purpose)
	}
	return keyPairFromSeed(seedText)
}

func loadBLSKeyPair(seedText string, keyPath string, config nodeConfig) (consensus.BLSKeyPair, error) {
	if strings.TrimSpace(keyPath) != "" {
		keyFile, err := loadNodeKeystoreFile(keyPath, isProductionNodeConfig(config))
		if err != nil {
			return consensus.BLSKeyPair{}, err
		}
		return blsKeyPairFromKeystore(keyFile)
	}
	if isProductionNodeConfig(config) {
		return consensus.BLSKeyPair{}, fmt.Errorf("posnode: bls key path is required in production")
	}
	seedText = strings.TrimSpace(seedText)
	if seedText == "" {
		return consensus.BLSKeyPair{}, fmt.Errorf("posnode: bls seed is empty")
	}
	return consensus.BLSKeyPairFromSeed(utils.SHA256([]byte(seedText)))
}

func loadNodeKeystoreFile(path string, production bool) (nodeKeystoreFile, error) {
	cleanPath := filepath.Clean(strings.TrimSpace(path))
	if cleanPath == "." || cleanPath == "" {
		return nodeKeystoreFile{}, fmt.Errorf("posnode: node keystore path is empty")
	}
	info, err := os.Stat(cleanPath)
	if err != nil {
		return nodeKeystoreFile{}, fmt.Errorf("posnode: stat node keystore: %w", err)
	}
	if info.IsDir() {
		return nodeKeystoreFile{}, fmt.Errorf("posnode: node keystore is a directory")
	}
	if info.Size() <= 0 || info.Size() > maxNodeKeystoreBytes {
		return nodeKeystoreFile{}, fmt.Errorf("posnode: invalid node keystore size")
	}
	if err := validateTreasuryKeystorePermissions(info, production); err != nil {
		return nodeKeystoreFile{}, err
	}
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return nodeKeystoreFile{}, fmt.Errorf("posnode: read node keystore: %w", err)
	}
	keyFile := nodeKeystoreFile{}
	if err := json.Unmarshal(data, &keyFile); err != nil {
		return nodeKeystoreFile{}, fmt.Errorf("posnode: decode node keystore: %w", err)
	}
	return keyFile, nil
}

func rawKeyPairFromKeystore(keyFile nodeKeystoreFile, purpose string) (rawKeyPair, error) {
	seed, expectedPublicKey, err := ed25519SeedFromKeystore(keyFile)
	if err != nil {
		return rawKeyPair{}, fmt.Errorf("posnode: load %s raw key: %w", purpose, err)
	}
	keyPair, err := rawKeyPairFromPrivateKey(seed)
	if err != nil {
		return rawKeyPair{}, err
	}
	if len(expectedPublicKey) > 0 && !bytes.Equal(keyPair.publicKey, expectedPublicKey) {
		return rawKeyPair{}, fmt.Errorf("posnode: %s public key does not match private key", purpose)
	}
	return keyPair, nil
}

func solanaKeyPairFromKeystore(keyFile nodeKeystoreFile) (structure.SolanaKeyPair, error) {
	seed, expectedPublicKey, err := ed25519SeedFromKeystore(keyFile)
	if err != nil {
		return structure.SolanaKeyPair{}, err
	}
	keyPair, err := structure.KeyPairFromSeed(seed)
	if err != nil {
		return structure.SolanaKeyPair{}, err
	}
	if len(expectedPublicKey) > 0 && !bytes.Equal(keyPair.PublicKey[:], expectedPublicKey) {
		return structure.SolanaKeyPair{}, fmt.Errorf("posnode: public key does not match private key")
	}
	return keyPair, nil
}

func blsKeyPairFromKeystore(keyFile nodeKeystoreFile) (consensus.BLSKeyPair, error) {
	if strings.TrimSpace(keyFile.Seed) != "" {
		return consensus.BLSKeyPairFromSeed(utils.SHA256([]byte(strings.TrimSpace(keyFile.Seed))))
	}
	if strings.TrimSpace(keyFile.SeedBase64) != "" {
		seed, err := decodeKeystoreBase64(keyFile.SeedBase64, "bls seed")
		if err != nil {
			return consensus.BLSKeyPair{}, err
		}
		return consensus.BLSKeyPairFromSeed(seed)
	}
	if strings.TrimSpace(keyFile.PrivateKeyBase64) == "" {
		return consensus.BLSKeyPair{}, fmt.Errorf("posnode: bls keystore has no key material")
	}
	privateKey, err := decodeKeystoreBase64(keyFile.PrivateKeyBase64, "bls private key")
	if err != nil {
		return consensus.BLSKeyPair{}, err
	}
	keyPair, err := consensus.BLSKeyPairFromPrivateKey(privateKey)
	if err != nil {
		return consensus.BLSKeyPair{}, err
	}
	if strings.TrimSpace(keyFile.PublicKeyBase64) == "" {
		return keyPair, nil
	}
	expectedPublicKey, err := decodeKeystoreBase64(keyFile.PublicKeyBase64, "bls public key")
	if err != nil {
		return consensus.BLSKeyPair{}, err
	}
	if !bytes.Equal(keyPair.PublicKey, expectedPublicKey) {
		return consensus.BLSKeyPair{}, fmt.Errorf("posnode: bls public key does not match private key")
	}
	return keyPair, nil
}

func ed25519SeedFromKeystore(keyFile nodeKeystoreFile) ([]byte, []byte, error) {
	expectedPublicKey, err := optionalKeystorePublicKey(keyFile.PublicKeyBase64)
	if err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(keyFile.Seed) != "" {
		return utils.SHA256([]byte(strings.TrimSpace(keyFile.Seed))), expectedPublicKey, nil
	}
	if strings.TrimSpace(keyFile.SeedBase64) != "" {
		seed, err := decodeSizedKeystoreBase64(keyFile.SeedBase64, utils.Ed25519KeySize, "ed25519 seed")
		return seed, expectedPublicKey, err
	}
	if strings.TrimSpace(keyFile.PrivateKeyBase64) != "" {
		seed, err := decodeSizedKeystoreBase64(keyFile.PrivateKeyBase64, utils.Ed25519KeySize, "ed25519 private key")
		return seed, expectedPublicKey, err
	}
	if strings.TrimSpace(keyFile.SecretKeyBase64) == "" {
		return nil, nil, fmt.Errorf("posnode: keystore has no key material")
	}
	secretKey, err := decodeSizedKeystoreBase64(keyFile.SecretKeyBase64, structure.SolanaSecretKeySize, "ed25519 secret key")
	if err != nil {
		return nil, nil, err
	}
	if len(expectedPublicKey) > 0 && !bytes.Equal(secretKey[structure.SolanaPrivateKeySeedSize:], expectedPublicKey) {
		return nil, nil, fmt.Errorf("posnode: secret key public key mismatch")
	}
	return secretKey[:structure.SolanaPrivateKeySeedSize], secretKey[structure.SolanaPrivateKeySeedSize:], nil
}

func optionalKeystorePublicKey(encodedPublicKey string) ([]byte, error) {
	if strings.TrimSpace(encodedPublicKey) == "" {
		return nil, nil
	}
	return decodeSizedKeystoreBase64(encodedPublicKey, utils.Ed25519KeySize, "ed25519 public key")
}

func decodeSizedKeystoreBase64(encodedValue string, expectedSize int, fieldName string) ([]byte, error) {
	value, err := decodeKeystoreBase64(encodedValue, fieldName)
	if err != nil {
		return nil, err
	}
	if len(value) != expectedSize {
		return nil, fmt.Errorf("posnode: %s requires %d bytes", fieldName, expectedSize)
	}
	return value, nil
}

func decodeKeystoreBase64(encodedValue string, fieldName string) ([]byte, error) {
	value, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedValue))
	if err != nil {
		return nil, fmt.Errorf("posnode: decode %s: %w", fieldName, err)
	}
	return value, nil
}

func rawKeyPairFromSeedText(seedText string) (rawKeyPair, error) {
	seedText = strings.TrimSpace(seedText)
	if seedText == "" {
		return rawKeyPair{}, fmt.Errorf("posnode: peer seed is empty")
	}
	return rawKeyPairFromPrivateKey(utils.SHA256([]byte(seedText)))
}

func rawKeyPairFromPrivateKey(privateKey []byte) (rawKeyPair, error) {
	publicKey, err := utils.DeriveEd25519PublicKeyFromPrivateKey(privateKey)
	if err != nil {
		return rawKeyPair{}, fmt.Errorf("posnode: derive peer public key: %w", err)
	}
	return rawKeyPair{
		publicKey:  utils.CloneBytes(publicKey),
		privateKey: utils.CloneBytes(privateKey),
		peerID:     utils.Base58Encode(publicKey),
	}, nil
}
