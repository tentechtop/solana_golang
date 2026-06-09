package utils

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"fmt"
)

// RandomBytes 生成安全随机字节 + 使用密码学随机源。
func RandomBytes(n int) ([]byte, error) {
	if n < 0 {
		return nil, fmt.Errorf("utils: random byte length cannot be negative")
	}
	value := make([]byte, n)
	if _, err := rand.Read(value); err != nil {
		return nil, fmt.Errorf("utils: generate random bytes: %w", err)
	}
	return value, nil
}

// SecureEqual 常量时间比较字节切片 + 降低时序侧信道风险。
func SecureEqual(a []byte, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare(a, b) == 1
}

// SHA256 计算 SHA-256 摘要 + 返回拷贝避免外部修改内部数组。
func SHA256(value []byte) []byte {
	sum := sha256.Sum256(value)
	return CloneBytes(sum[:])
}

// Sha256 提供 SHA256 别名 + 兼容不同命名习惯。
func Sha256(value []byte) []byte {
	return SHA256(value)
}

// SHA256Hex 计算 SHA-256 十六进制摘要 + 便于日志和存储。
func SHA256Hex(value []byte) string {
	return BytesToHex(SHA256(value))
}

// Sha256Hex 提供 SHA256Hex 别名 + 兼容不同命名习惯。
func Sha256Hex(value []byte) string {
	return SHA256Hex(value)
}

// DoubleSHA256 执行双 SHA-256 哈希 + 兼容区块链校验场景。
func DoubleSHA256(value []byte) []byte {
	return SHA256(SHA256(value))
}

// DoubleSha256 提供 DoubleSHA256 别名 + 兼容不同命名习惯。
func DoubleSha256(value []byte) []byte {
	return DoubleSHA256(value)
}

// DoubleSHA256Hex 计算双 SHA-256 十六进制摘要 + 便于展示和索引。
func DoubleSHA256Hex(value []byte) string {
	return BytesToHex(DoubleSHA256(value))
}

// DoubleSha256Hex 提供 DoubleSHA256Hex 别名 + 兼容不同命名习惯。
func DoubleSha256Hex(value []byte) string {
	return DoubleSHA256Hex(value)
}

// Checksum4 计算 4 字节校验和 + 采用区块链常用双 SHA-256 截断规则。
func Checksum4(value []byte) []byte {
	return CloneBytes(DoubleSHA256(value)[:4])
}

// SHA512 计算 SHA-512 摘要 + 返回拷贝避免外部修改内部数组。
func SHA512(value []byte) []byte {
	sum := sha512.Sum512(value)
	return CloneBytes(sum[:])
}

// Sha512 提供 SHA512 别名 + 兼容不同命名习惯。
func Sha512(value []byte) []byte {
	return SHA512(value)
}

// SHA512Hex 计算 SHA-512 十六进制摘要 + 便于日志和存储。
func SHA512Hex(value []byte) string {
	return BytesToHex(SHA512(value))
}

// HMACSHA512 计算 HMAC-SHA512 + 用于密钥派生和消息认证。
func HMACSHA512(key []byte, data []byte) []byte {
	mac := hmac.New(sha512.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}
