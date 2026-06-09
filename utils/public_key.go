package utils

import "fmt"

const (
	// PublicKeySize 定义 Solana 公钥长度 + 用于统一输入校验。
	PublicKeySize = 32
	// SignatureSize 定义 Solana 交易签名长度 + 用于统一输入校验。
	SignatureSize = 64
)

// PublicKey 表示固定长度 Solana 公钥 + 避免动态切片长度错误。
type PublicKey [PublicKeySize]byte

// Hash 表示固定长度 Solana 哈希 + 与公钥长度保持一致。
type Hash [PublicKeySize]byte

// Blockhash 表示最近区块哈希 + 复用 Hash 类型保持兼容。
type Blockhash = Hash

// Signature 表示固定长度签名 + 兼容 Ed25519 和 Solana 交易签名。
type Signature [SignatureSize]byte

// NewPublicKey 创建公钥对象 + 校验输入必须为 32 字节。
func NewPublicKey(value []byte) (PublicKey, error) {
	var key PublicKey
	if err := requireLength(value, PublicKeySize, "public key"); err != nil {
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
	decoded, err := Base58Decode(value)
	if err != nil {
		return PublicKey{}, err
	}
	return NewPublicKey(decoded)
}

// PublicKeyFromHex 解码十六进制公钥 + 便于调试和配置导入。
func PublicKeyFromHex(value string) (PublicKey, error) {
	decoded, err := HexToBytes(value)
	if err != nil {
		return PublicKey{}, err
	}
	return NewPublicKey(decoded)
}

// Bytes 返回公钥字节拷贝 + 防止外部修改内部数组。
func (p PublicKey) Bytes() []byte {
	return CloneBytes(p[:])
}

// String 返回 Base58 公钥 + 作为默认可读表示。
func (p PublicKey) String() string {
	return Base58Encode(p[:])
}

// Hex 返回小写十六进制公钥 + 便于日志和调试。
func (p PublicKey) Hex() string {
	return BytesToHex(p[:])
}

// Equal 比较公钥是否相等 + 使用安全字节比较。
func (p PublicKey) Equal(other PublicKey) bool {
	return SecureEqual(p[:], other[:])
}

// IsZero 判断是否为空公钥 + 用于默认值检查。
func (p PublicKey) IsZero() bool {
	return p == PublicKey{}
}

// NewHash 创建哈希对象 + 校验输入必须为 32 字节。
func NewHash(value []byte) (Hash, error) {
	var hash Hash
	if err := requireLength(value, PublicKeySize, "hash"); err != nil {
		return hash, err
	}
	copy(hash[:], value)
	return hash, nil
}

// HashFromBase58 解码 Base58 哈希 + 兼容 Solana 哈希表示。
func HashFromBase58(value string) (Hash, error) {
	decoded, err := Base58Decode(value)
	if err != nil {
		return Hash{}, err
	}
	return NewHash(decoded)
}

// Bytes 返回哈希字节拷贝 + 防止外部修改内部数组。
func (h Hash) Bytes() []byte {
	return CloneBytes(h[:])
}

// String 返回 Base58 哈希 + 作为默认可读表示。
func (h Hash) String() string {
	return Base58Encode(h[:])
}

// Hex 返回小写十六进制哈希 + 便于日志和调试。
func (h Hash) Hex() string {
	return BytesToHex(h[:])
}

// NewSignature 创建签名对象 + 校验输入必须为 64 字节。
func NewSignature(value []byte) (Signature, error) {
	var signature Signature
	if err := requireLength(value, SignatureSize, "signature"); err != nil {
		return signature, err
	}
	copy(signature[:], value)
	return signature, nil
}

// SignatureFromBytes 从字节创建签名 + 复用统一长度校验。
func SignatureFromBytes(value []byte) (Signature, error) {
	return NewSignature(value)
}

// SignatureFromBase58 解码 Base58 签名 + 兼容 Solana 签名表示。
func SignatureFromBase58(value string) (Signature, error) {
	decoded, err := Base58Decode(value)
	if err != nil {
		return Signature{}, err
	}
	return NewSignature(decoded)
}

// SignatureFromHex 解码十六进制签名 + 便于调试和配置导入。
func SignatureFromHex(value string) (Signature, error) {
	decoded, err := HexToBytes(value)
	if err != nil {
		return Signature{}, err
	}
	return NewSignature(decoded)
}

// Bytes 返回签名字节拷贝 + 防止外部修改内部数组。
func (s Signature) Bytes() []byte {
	return CloneBytes(s[:])
}

// String 返回 Base58 签名 + 作为默认可读表示。
func (s Signature) String() string {
	return Base58Encode(s[:])
}

// Hex 返回小写十六进制签名 + 便于日志和调试。
func (s Signature) Hex() string {
	return BytesToHex(s[:])
}

// Equal 比较签名是否相等 + 使用安全字节比较。
func (s Signature) Equal(other Signature) bool {
	return SecureEqual(s[:], other[:])
}

// MustPublicKeyFromBase58 解码 Base58 公钥 + 仅用于包级常量和测试快速失败。
func MustPublicKeyFromBase58(value string) PublicKey {
	key, err := PublicKeyFromBase58(value)
	if err != nil {
		panic(fmt.Sprintf("utils: invalid public key %q: %v", value, err))
	}
	return key
}
