package posnode

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"

	"github.com/tentechtop/ordexus-core/structure"
	"github.com/tentechtop/ordexus-core/utils"
)

const maxTreasuryKeystoreBytes = 8192

type treasuryKeystoreFile struct {
	Seed             string `json:"seed,omitempty"`
	PrivateKeyBase64 string `json:"private_key_base64,omitempty"`
	SecretKeyBase64  string `json:"secret_key_base64,omitempty"`
}

func (node *posNode) treasuryKeyPair() (structure.OrdexusKeyPair, string, error) {
	keyPath := strings.TrimSpace(node.config.TreasuryKeyPath)
	if keyPath != "" {
		keyPair, err := loadTreasuryKeyPairFromFile(keyPath, isProductionNodeConfig(node.config))
		if err != nil {
			return structure.OrdexusKeyPair{}, "", err
		}
		if err := node.validateTreasuryKeyPair(keyPair); err != nil {
			return structure.OrdexusKeyPair{}, "", err
		}
		return keyPair, "keystore", nil
	}
	return structure.OrdexusKeyPair{}, "", fmt.Errorf("posnode: treasury keystore is required; consensus only embeds the public key")
}

func (node *posNode) validateTreasuryKeyPair(keyPair structure.OrdexusKeyPair) error {
	expectedAddress := strings.TrimSpace(node.config.Genesis.TreasuryAddress)
	if expectedAddress == "" {
		return nil
	}
	expectedPublicKey, err := structure.PublicKeyFromBase58(expectedAddress)
	if err != nil {
		return fmt.Errorf("posnode: decode configured treasury address: %w", err)
	}
	if keyPair.PublicKey != expectedPublicKey {
		return fmt.Errorf("posnode: treasury keystore public key does not match genesis treasury address")
	}
	return nil
}

func loadTreasuryKeyPairFromFile(path string, production bool) (structure.OrdexusKeyPair, error) {
	cleanPath := filepath.Clean(strings.TrimSpace(path))
	if cleanPath == "." || cleanPath == "" {
		return structure.OrdexusKeyPair{}, fmt.Errorf("posnode: treasury keystore path is empty")
	}
	info, err := os.Stat(cleanPath)
	if err != nil {
		return structure.OrdexusKeyPair{}, fmt.Errorf("posnode: stat treasury keystore: %w", err)
	}
	if info.IsDir() {
		return structure.OrdexusKeyPair{}, fmt.Errorf("posnode: treasury keystore is a directory")
	}
	if info.Size() <= 0 || info.Size() > maxTreasuryKeystoreBytes {
		return structure.OrdexusKeyPair{}, fmt.Errorf("posnode: invalid treasury keystore size")
	}
	if err := validateTreasuryKeystorePermissions(info, production); err != nil {
		return structure.OrdexusKeyPair{}, err
	}
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return structure.OrdexusKeyPair{}, fmt.Errorf("posnode: read treasury keystore: %w", err)
	}
	keyFile := treasuryKeystoreFile{}
	if err := json.Unmarshal(data, &keyFile); err != nil {
		return structure.OrdexusKeyPair{}, fmt.Errorf("posnode: decode treasury keystore: %w", err)
	}
	return treasuryKeyPairFromKeystore(keyFile)
}

func validateTreasuryKeystorePermissions(info os.FileInfo, production bool) error {
	if !production || goruntime.GOOS == "windows" {
		return nil
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("posnode: treasury keystore must not be group/world readable")
	}
	return nil
}

func treasuryKeyPairFromKeystore(keyFile treasuryKeystoreFile) (structure.OrdexusKeyPair, error) {
	if strings.TrimSpace(keyFile.Seed) != "" {
		keyPair, err := structure.KeyPairFromSeed(utils.SHA256([]byte(strings.TrimSpace(keyFile.Seed))))
		if err != nil {
			return structure.OrdexusKeyPair{}, fmt.Errorf("posnode: derive treasury seed: %w", err)
		}
		return keyPair, nil
	}
	if strings.TrimSpace(keyFile.PrivateKeyBase64) != "" {
		return treasuryKeyPairFromBase64Seed(keyFile.PrivateKeyBase64)
	}
	if strings.TrimSpace(keyFile.SecretKeyBase64) != "" {
		return treasuryKeyPairFromBase64Secret(keyFile.SecretKeyBase64)
	}
	return structure.OrdexusKeyPair{}, fmt.Errorf("posnode: treasury keystore has no key material")
}

func treasuryKeyPairFromBase64Seed(encodedSeed string) (structure.OrdexusKeyPair, error) {
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedSeed))
	if err != nil {
		return structure.OrdexusKeyPair{}, fmt.Errorf("posnode: decode treasury private key seed: %w", err)
	}
	keyPair, err := structure.KeyPairFromSeed(seed)
	if err != nil {
		return structure.OrdexusKeyPair{}, fmt.Errorf("posnode: build treasury keypair: %w", err)
	}
	return keyPair, nil
}

func treasuryKeyPairFromBase64Secret(encodedSecret string) (structure.OrdexusKeyPair, error) {
	secretKey, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedSecret))
	if err != nil {
		return structure.OrdexusKeyPair{}, fmt.Errorf("posnode: decode treasury secret key: %w", err)
	}
	keyPair, err := structure.KeyPairFromSecretKey64(secretKey)
	if err != nil {
		return structure.OrdexusKeyPair{}, fmt.Errorf("posnode: build treasury secret keypair: %w", err)
	}
	return keyPair, nil
}
