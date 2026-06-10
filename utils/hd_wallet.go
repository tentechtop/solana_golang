package utils

import (
	"crypto/hmac"
	"crypto/sha512"
	"fmt"
	"strconv"
	"strings"
)

const (
	bip44Purpose          = 44
	solanaCoinType        = 501
	mnemonicEntropyBits   = 128
	hardenedDeriveOffset  = uint32(0x80000000)
	bip39SeedLength       = 64
	slip10MinSeedLength   = 16
	slip10MaxSeedLength   = 64
	ed25519SeedMasterSalt = "ed25519 seed"
)

// KeyInfo 保存钱包密钥信息 + 汇总私钥、公钥、地址和路径。
type KeyInfo struct {
	PrivateKey []byte
	PublicKey  []byte
	Address    string
	Path       string
}

// HDNode 表示 SLIP-0010 扩展私钥节点 + 保存私钥、链码和路径。
type HDNode struct {
	PrivateKey []byte
	ChainCode  []byte
	Path       string
}

// GenerateMnemonic 生成 12 个英文助记词 + 使用 BIP-39 标准熵。
func GenerateMnemonic() ([]string, error) {
	mnemonic, err := GenerateMnemonicString()
	if err != nil {
		return nil, err
	}
	return strings.Fields(mnemonic), nil
}

// GenerateMnemonicString 生成助记词字符串 + 便于展示和持久化。
func GenerateMnemonicString() (string, error) {
	entropy, err := NewBIP39Entropy(mnemonicEntropyBits)
	if err != nil {
		return "", fmt.Errorf("utils: generate mnemonic entropy: %w", err)
	}
	mnemonic, err := NewBIP39Mnemonic(entropy)
	if err != nil {
		return "", fmt.Errorf("utils: generate mnemonic: %w", err)
	}
	return mnemonic, nil
}

// GenerateSeed 生成 BIP-39 种子 + 从助记词数组和密码短语派生。
func GenerateSeed(mnemonic []string, passphrase string) ([]byte, error) {
	return GenerateSeedFromMnemonicString(strings.Join(mnemonic, " "), passphrase)
}

// GenerateSeedFromMnemonicString 生成 BIP-39 种子 + 从助记词字符串派生。
func GenerateSeedFromMnemonicString(mnemonic string, passphrase string) ([]byte, error) {
	normalized := strings.Join(strings.Fields(mnemonic), " ")
	if !IsBIP39MnemonicValid(normalized) {
		return nil, fmt.Errorf("utils: invalid bip39 mnemonic")
	}
	seed, err := NewBIP39Seed(normalized, passphrase)
	if err != nil {
		return nil, err
	}
	return CloneBytes(seed), nil
}

// GenerateRootHDNode 派生根 HD 节点 + 使用 SLIP-0010 Ed25519 规则。
func GenerateRootHDNode(seed []byte) (HDNode, error) {
	if err := validateSLIP10Seed(seed); err != nil {
		return HDNode{}, err
	}
	result := hmacSHA512([]byte(ed25519SeedMasterSalt), seed)
	return HDNode{
		PrivateKey: CloneBytes(result[:Ed25519KeySize]),
		ChainCode:  CloneBytes(result[Ed25519KeySize:]),
		Path:       "m",
	}, nil
}

// GenerateRootPrivateKey 派生根私钥 + 复用 SLIP-0010 根节点逻辑。
func GenerateRootPrivateKey(seed []byte) ([]byte, error) {
	node, err := GenerateRootHDNode(seed)
	if err != nil {
		return nil, err
	}
	return node.PrivateKey, nil
}

// DeriveChildHDNode 派生硬化子节点 + 使用 SLIP-0010 Ed25519 CKDpriv。
func DeriveChildHDNode(parent HDNode, index uint32) (HDNode, error) {
	if index&hardenedDeriveOffset == 0 {
		return HDNode{}, fmt.Errorf("utils: ed25519 SLIP-0010 requires hardened child index")
	}
	if err := requireLength(parent.PrivateKey, Ed25519KeySize, "parent ed25519 private key"); err != nil {
		return HDNode{}, err
	}
	if err := requireLength(parent.ChainCode, Ed25519KeySize, "parent chain code"); err != nil {
		return HDNode{}, err
	}

	result := hmacSHA512(parent.ChainCode, ConcatBytes([]byte{0x00}, parent.PrivateKey, Uint32ToBytes(index)))
	child := HDNode{
		PrivateKey: CloneBytes(result[:Ed25519KeySize]),
		ChainCode:  CloneBytes(result[Ed25519KeySize:]),
		Path:       appendPathIndex(parent.Path, index),
	}
	return child, nil
}

// DeriveChildPrivateKey 派生子私钥和链码 + 兼容旧接口入参形式。
func DeriveChildPrivateKey(parentPrivateKey []byte, parentChainCode []byte, index uint32) ([]byte, []byte, error) {
	child, err := DeriveChildHDNode(HDNode{PrivateKey: parentPrivateKey, ChainCode: parentChainCode, Path: "m"}, index)
	if err != nil {
		return nil, nil, err
	}
	return child.PrivateKey, child.ChainCode, nil
}

// GetSolanaAddress 生成 Solana 地址 + 地址格式为 Base58 公钥。
func GetSolanaAddress(publicKey []byte) (string, error) {
	if err := requireLength(publicKey, Ed25519KeySize, "solana public key"); err != nil {
		return "", err
	}
	return Base58Encode(publicKey), nil
}

// GetSolanaKeyPair 派生 Solana 密钥对 + 使用助记词和标准路径参数。
func GetSolanaKeyPair(mnemonic []string, accountIndex int, addressIndex int) (KeyInfo, error) {
	seed, err := GenerateSeed(mnemonic, "")
	if err != nil {
		return KeyInfo{}, err
	}
	return GetSolanaKeyPairFromSeed(seed, accountIndex, addressIndex)
}

// GetSolanaKeyPairFromMnemonicString 派生 Solana 密钥对 + 使用助记词字符串。
func GetSolanaKeyPairFromMnemonicString(mnemonic string, accountIndex int, addressIndex int) (KeyInfo, error) {
	seed, err := GenerateSeedFromMnemonicString(mnemonic, "")
	if err != nil {
		return KeyInfo{}, err
	}
	return GetSolanaKeyPairFromSeed(seed, accountIndex, addressIndex)
}

// GetSolanaKeyPairFromSeed 派生标准路径密钥对 + 路径为 m/44'/501'/accountIndex'/addressIndex'。
func GetSolanaKeyPairFromSeed(seed []byte, accountIndex int, addressIndex int) (KeyInfo, error) {
	if accountIndex < 0 {
		return KeyInfo{}, fmt.Errorf("utils: account index cannot be negative")
	}
	if addressIndex < 0 {
		return KeyInfo{}, fmt.Errorf("utils: address index cannot be negative")
	}
	path := SolanaDerivationPath(accountIndex, addressIndex)
	return GetSolanaKeyPairFromSeedAndPath(seed, path)
}

// GetSolanaKeyPairFromSeedAndPath 派生 Solana 密钥对 + 使用 seed 和 SLIP-0010 路径。
func GetSolanaKeyPairFromSeedAndPath(seed []byte, path string) (KeyInfo, error) {
	node, err := DeriveHDNodeFromSeedAndPath(seed, path)
	if err != nil {
		return KeyInfo{}, err
	}
	publicKey, err := DeriveEd25519PublicKeyFromPrivateKey(node.PrivateKey)
	if err != nil {
		return KeyInfo{}, err
	}
	address, err := GetSolanaAddress(publicKey)
	if err != nil {
		return KeyInfo{}, err
	}
	return KeyInfo{
		PrivateKey: CloneBytes(node.PrivateKey),
		PublicKey:  CloneBytes(publicKey),
		Address:    address,
		Path:       node.Path,
	}, nil
}

// DerivePrivateKeyFromSeedAndPath 派生 32 字节私钥 + 支持 m/44'/501'/0'/0' 路径。
func DerivePrivateKeyFromSeedAndPath(seed []byte, path string) ([]byte, string, error) {
	node, err := DeriveHDNodeFromSeedAndPath(seed, path)
	if err != nil {
		return nil, "", err
	}
	return node.PrivateKey, node.Path, nil
}

// DeriveHDNodeFromSeedAndPath 派生 HD 节点 + 按路径逐级执行硬化派生。
func DeriveHDNodeFromSeedAndPath(seed []byte, path string) (HDNode, error) {
	indexes, normalizedPath, err := ParseDerivationPath(path)
	if err != nil {
		return HDNode{}, err
	}
	node, err := GenerateRootHDNode(seed)
	if err != nil {
		return HDNode{}, err
	}
	node.Path = "m"
	for _, index := range indexes {
		node, err = DeriveChildHDNode(node, index)
		if err != nil {
			return HDNode{}, err
		}
	}
	node.Path = normalizedPath
	return node, nil
}

// ParseDerivationPath 解析派生路径 + 支持撇号、h、H 标识硬化索引。
func ParseDerivationPath(path string) ([]uint32, string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil, "", fmt.Errorf("utils: derivation path is empty")
	}
	if trimmed == "m" || trimmed == "M" {
		return []uint32{}, "m", nil
	}
	if len(trimmed) < 3 || (trimmed[0] != 'm' && trimmed[0] != 'M') || trimmed[1] != '/' {
		return nil, "", fmt.Errorf("utils: derivation path must start with m/")
	}

	parts := strings.Split(trimmed[2:], "/")
	indexes := make([]uint32, 0, len(parts))
	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			return nil, "", fmt.Errorf("utils: derivation path contains an empty index")
		}
		hardened := strings.HasSuffix(part, "'") || strings.HasSuffix(part, "h") || strings.HasSuffix(part, "H")
		indexText := part
		if hardened {
			indexText = strings.TrimRight(part, "'hH")
		}
		if indexText == "" {
			return nil, "", fmt.Errorf("utils: derivation path contains an empty hardened index")
		}
		parsed, err := strconv.ParseUint(indexText, 10, 31)
		if err != nil {
			return nil, "", fmt.Errorf("utils: invalid derivation path index %q: %w", part, err)
		}
		index := uint32(parsed)
		normalizedPart := strconv.FormatUint(parsed, 10)
		if hardened {
			index |= hardenedDeriveOffset
			normalizedPart += "'"
		}
		indexes = append(indexes, index)
		normalized = append(normalized, normalizedPart)
	}
	return indexes, "m/" + strings.Join(normalized, "/"), nil
}

// SolanaDerivationPath 构造 Solana 标准路径 + 格式为 m/44'/501'/accountIndex'/addressIndex'。
func SolanaDerivationPath(accountIndex int, addressIndex int) string {
	return fmt.Sprintf("m/%d'/%d'/%d'/%d'", bip44Purpose, solanaCoinType, accountIndex, addressIndex)
}
func hmacSHA512(key []byte, data []byte) []byte {
	mac := hmac.New(sha512.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}
func validateSLIP10Seed(seed []byte) error {
	if len(seed) < slip10MinSeedLength || len(seed) > slip10MaxSeedLength {
		return fmt.Errorf("utils: slip-0010 seed requires %d-%d bytes, got %d", slip10MinSeedLength, slip10MaxSeedLength, len(seed))
	}
	return nil
}
func appendPathIndex(parentPath string, index uint32) string {
	if parentPath == "" {
		parentPath = "m"
	}
	value := index &^ hardenedDeriveOffset
	suffix := strconv.FormatUint(uint64(value), 10)
	if index&hardenedDeriveOffset != 0 {
		suffix += "'"
	}
	return parentPath + "/" + suffix
}
