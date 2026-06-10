package structure

import (
	"fmt"

	"solana_golang/utils"
)

const (
	PublicKeySize = 32
	HashSize      = 32
	SignatureSize = 64
)

// PublicKey 表示固定长度账户公钥 + 避免动态切片长度错误。
type PublicKey [PublicKeySize]byte

// Hash 表示固定长度哈希 + 统一链上摘要值对象。
type Hash [HashSize]byte

// Blockhash 表示区块哈希 + 复用哈希值对象保持格式一致。
type Blockhash = Hash

// TransactionHash 表示交易哈希 + 复用哈希值对象保持索引一致。
type TransactionHash = Hash

// Signature 表示固定长度签名 + 兼容 Ed25519 和交易签名。
type Signature [SignatureSize]byte

// NewPublicKey 创建公钥对象 + 校验输入必须为 32 字节。
func NewPublicKey(value []byte) (PublicKey, error) {
	var key PublicKey
	if err := requireValueLength(value, PublicKeySize, "public key"); err != nil {
		return key, err
	}
	copy(key[:], value)
	return key, nil
}

// PublicKeyFromBytes 从字节创建公钥 + 复用统一长度校验。
func PublicKeyFromBytes(value []byte) (PublicKey, error) {
	return NewPublicKey(value)
}

// PublicKeyFromBase58 解码 Base58 公钥 + 兼容 Solana 地址格式。
func PublicKeyFromBase58(value string) (PublicKey, error) {
	decoded, err := utils.Base58Decode(value)
	if err != nil {
		return PublicKey{}, err
	}
	return NewPublicKey(decoded)
}

// PublicKeyFromHex 解码十六进制公钥 + 便于调试和配置导入。
func PublicKeyFromHex(value string) (PublicKey, error) {
	decoded, err := utils.HexToBytes(value)
	if err != nil {
		return PublicKey{}, err
	}
	return NewPublicKey(decoded)
}

// Bytes 返回公钥字节拷贝 + 防止外部修改内部数组。
func (publicKey PublicKey) Bytes() []byte {
	return utils.CloneBytes(publicKey[:])
}

func (publicKey PublicKey) String() string {
	return utils.Base58Encode(publicKey[:])
}

func (publicKey PublicKey) Hex() string {
	return utils.BytesToHex(publicKey[:])
}

// Equal 比较公钥是否相等 + 使用常量时间比较降低侧信道风险。
func (publicKey PublicKey) Equal(other PublicKey) bool {
	return utils.SecureEqual(publicKey[:], other[:])
}

// IsZero 判断是否为空公钥 + 用于默认值检查。
func (publicKey PublicKey) IsZero() bool {
	return publicKey == PublicKey{}
}

// NewHash 创建哈希对象 + 校验输入必须为 32 字节。
func NewHash(value []byte) (Hash, error) {
	var hash Hash
	if err := requireValueLength(value, HashSize, "hash"); err != nil {
		return hash, err
	}
	copy(hash[:], value)
	return hash, nil
}

// HashFromBase58 解码 Base58 哈希 + 兼容区块和交易哈希表示。
func HashFromBase58(value string) (Hash, error) {
	decoded, err := utils.Base58Decode(value)
	if err != nil {
		return Hash{}, err
	}
	return NewHash(decoded)
}

// HashFromHex 解码十六进制哈希 + 便于本地调试和持久化读取。
func HashFromHex(value string) (Hash, error) {
	decoded, err := utils.HexToBytes(value)
	if err != nil {
		return Hash{}, err
	}
	return NewHash(decoded)
}

// Bytes 返回哈希字节拷贝 + 防止外部修改内部数组。
func (hash Hash) Bytes() []byte {
	return utils.CloneBytes(hash[:])
}

func (hash Hash) String() string {
	return utils.Base58Encode(hash[:])
}

func (hash Hash) Hex() string {
	return utils.BytesToHex(hash[:])
}

// IsZero 判断是否为空哈希 + 用于默认值检查。
func (hash Hash) IsZero() bool {
	return hash == Hash{}
}

// NewSignature 创建签名对象 + 校验输入必须为 64 字节。
func NewSignature(value []byte) (Signature, error) {
	var signature Signature
	if err := requireValueLength(value, SignatureSize, "signature"); err != nil {
		return signature, err
	}
	copy(signature[:], value)
	return signature, nil
}

// SignatureFromBytes 从字节创建签名 + 复用统一长度校验。
func SignatureFromBytes(value []byte) (Signature, error) {
	return NewSignature(value)
}

// SignatureFromBase58 解码 Base58 签名 + 兼容交易签名表示。
func SignatureFromBase58(value string) (Signature, error) {
	decoded, err := utils.Base58Decode(value)
	if err != nil {
		return Signature{}, err
	}
	return NewSignature(decoded)
}

// SignatureFromHex 解码十六进制签名 + 便于调试和配置导入。
func SignatureFromHex(value string) (Signature, error) {
	decoded, err := utils.HexToBytes(value)
	if err != nil {
		return Signature{}, err
	}
	return NewSignature(decoded)
}

// Bytes 返回签名字节拷贝 + 防止外部修改内部数组。
func (signature Signature) Bytes() []byte {
	return utils.CloneBytes(signature[:])
}

func (signature Signature) String() string {
	return utils.Base58Encode(signature[:])
}

func (signature Signature) Hex() string {
	return utils.BytesToHex(signature[:])
}

// Equal 比较签名是否相等 + 使用常量时间比较降低侧信道风险。
func (signature Signature) Equal(other Signature) bool {
	return utils.SecureEqual(signature[:], other[:])
}

// MustPublicKeyFromBase58 解码 Base58 公钥 + 仅用于包级常量和测试快速失败。
func MustPublicKeyFromBase58(value string) PublicKey {
	key, err := PublicKeyFromBase58(value)
	if err != nil {
		panic(fmt.Sprintf("structure: invalid public key %q: %v", value, err))
	}
	return key
}
func requireValueLength(value []byte, want int, name string) error {
	if len(value) != want {
		return fmt.Errorf("structure: %s requires %d bytes, got %d", name, want, len(value))
	}
	return nil
}
