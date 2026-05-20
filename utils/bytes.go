package utils

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
)

// BytesToHex returns a lower-case hex string without a 0x prefix.
func BytesToHex(value []byte) string {
	return hex.EncodeToString(value)
}

// BytesToHexWithPrefix returns a lower-case hex string with a 0x prefix.
func BytesToHexWithPrefix(value []byte) string {
	return "0x" + BytesToHex(value)
}

// HexToBytes decodes a hex string. A leading 0x/0X prefix is accepted.
func HexToBytes(value string) ([]byte, error) {
	normalized := NormalizeHex(value)
	if normalized == "" {
		return []byte{}, nil
	}
	decoded, err := hex.DecodeString(normalized)
	if err != nil {
		return nil, fmt.Errorf("utils: decode hex: %w", err)
	}
	return decoded, nil
}

// NormalizeHex trims whitespace and removes an optional 0x/0X prefix.
func NormalizeHex(value string) string {
	normalized := strings.TrimSpace(value)
	if len(normalized) >= 2 && normalized[0] == '0' && (normalized[1] == 'x' || normalized[1] == 'X') {
		return normalized[2:]
	}
	return normalized
}

// IsHexString reports whether value can be decoded by HexToBytes.
func IsHexString(value string) bool {
	_, err := HexToBytes(value)
	return err == nil
}

// IntToBytes encodes a Java-style 32-bit int using big-endian byte order.
func IntToBytes(value int) []byte {
	return Int32ToBytes(int32(value))
}

// BytesToInt decodes a 4-byte big-endian signed int.
func BytesToInt(value []byte) (int, error) {
	decoded, err := BytesToInt32(value)
	if err != nil {
		return 0, err
	}
	return int(decoded), nil
}

// Int32ToBytes encodes a signed 32-bit integer using big-endian byte order.
func Int32ToBytes(value int32) []byte {
	encoded := make([]byte, 4)
	binary.BigEndian.PutUint32(encoded, uint32(value))
	return encoded
}

// BytesToInt32 decodes a 4-byte big-endian signed integer.
func BytesToInt32(value []byte) (int32, error) {
	if err := requireLength(value, 4, "int32"); err != nil {
		return 0, err
	}
	return int32(binary.BigEndian.Uint32(value)), nil
}

// Int64ToBytes encodes a signed 64-bit integer using big-endian byte order.
func Int64ToBytes(value int64) []byte {
	encoded := make([]byte, 8)
	binary.BigEndian.PutUint64(encoded, uint64(value))
	return encoded
}

// BytesToInt64 decodes an 8-byte big-endian signed integer.
func BytesToInt64(value []byte) (int64, error) {
	if err := requireLength(value, 8, "int64"); err != nil {
		return 0, err
	}
	return int64(binary.BigEndian.Uint64(value)), nil
}

// Uint16ToBytes encodes an unsigned 16-bit integer using big-endian byte order.
func Uint16ToBytes(value uint16) []byte {
	encoded := make([]byte, 2)
	binary.BigEndian.PutUint16(encoded, value)
	return encoded
}

// BytesToUint16 decodes a 2-byte big-endian unsigned integer.
func BytesToUint16(value []byte) (uint16, error) {
	if err := requireLength(value, 2, "uint16"); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(value), nil
}

// Uint32ToBytes encodes an unsigned 32-bit integer using big-endian byte order.
func Uint32ToBytes(value uint32) []byte {
	encoded := make([]byte, 4)
	binary.BigEndian.PutUint32(encoded, value)
	return encoded
}

// BytesToUint32 decodes a 4-byte big-endian unsigned integer.
func BytesToUint32(value []byte) (uint32, error) {
	if err := requireLength(value, 4, "uint32"); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(value), nil
}

// Uint64ToBytes encodes an unsigned 64-bit integer using big-endian byte order.
func Uint64ToBytes(value uint64) []byte {
	encoded := make([]byte, 8)
	binary.BigEndian.PutUint64(encoded, value)
	return encoded
}

// BytesToUint64 decodes an 8-byte big-endian unsigned integer.
func BytesToUint64(value []byte) (uint64, error) {
	if err := requireLength(value, 8, "uint64"); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(value), nil
}

// Uint16ToBytesLE encodes an unsigned 16-bit integer using little-endian byte order.
func Uint16ToBytesLE(value uint16) []byte {
	encoded := make([]byte, 2)
	binary.LittleEndian.PutUint16(encoded, value)
	return encoded
}

// BytesToUint16LE decodes a 2-byte little-endian unsigned integer.
func BytesToUint16LE(value []byte) (uint16, error) {
	if err := requireLength(value, 2, "uint16 little-endian"); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(value), nil
}

// Uint32ToBytesLE encodes an unsigned 32-bit integer using little-endian byte order.
func Uint32ToBytesLE(value uint32) []byte {
	encoded := make([]byte, 4)
	binary.LittleEndian.PutUint32(encoded, value)
	return encoded
}

// BytesToUint32LE decodes a 4-byte little-endian unsigned integer.
func BytesToUint32LE(value []byte) (uint32, error) {
	if err := requireLength(value, 4, "uint32 little-endian"); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(value), nil
}

// Uint64ToBytesLE encodes an unsigned 64-bit integer using little-endian byte order.
func Uint64ToBytesLE(value uint64) []byte {
	encoded := make([]byte, 8)
	binary.LittleEndian.PutUint64(encoded, value)
	return encoded
}

// BytesToUint64LE decodes an 8-byte little-endian unsigned integer.
func BytesToUint64LE(value []byte) (uint64, error) {
	if err := requireLength(value, 8, "uint64 little-endian"); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(value), nil
}

// Int16ToBytesLE encodes a signed 16-bit integer using little-endian byte order.
func Int16ToBytesLE(value int16) []byte {
	return Uint16ToBytesLE(uint16(value))
}

// BytesToInt16LE decodes a 2-byte little-endian signed integer.
func BytesToInt16LE(value []byte) (int16, error) {
	decoded, err := BytesToUint16LE(value)
	if err != nil {
		return 0, err
	}
	return int16(decoded), nil
}

// Int32ToBytesLE encodes a signed 32-bit integer using little-endian byte order.
func Int32ToBytesLE(value int32) []byte {
	return Uint32ToBytesLE(uint32(value))
}

// BytesToInt32LE decodes a 4-byte little-endian signed integer.
func BytesToInt32LE(value []byte) (int32, error) {
	decoded, err := BytesToUint32LE(value)
	if err != nil {
		return 0, err
	}
	return int32(decoded), nil
}

// Int64ToBytesLE encodes a signed 64-bit integer using little-endian byte order.
func Int64ToBytesLE(value int64) []byte {
	return Uint64ToBytesLE(uint64(value))
}

// BytesToInt64LE decodes an 8-byte little-endian signed integer.
func BytesToInt64LE(value []byte) (int64, error) {
	decoded, err := BytesToUint64LE(value)
	if err != nil {
		return 0, err
	}
	return int64(decoded), nil
}

// SHA256 returns the SHA-256 digest of value.
func SHA256(value []byte) []byte {
	sum := sha256.Sum256(value)
	return CloneBytes(sum[:])
}

// Sha256 is an alias for SHA256.
func Sha256(value []byte) []byte {
	return SHA256(value)
}

// SHA256Hex returns the SHA-256 digest encoded as a lower-case hex string.
func SHA256Hex(value []byte) string {
	return BytesToHex(SHA256(value))
}

// Sha256Hex is an alias for SHA256Hex.
func Sha256Hex(value []byte) string {
	return SHA256Hex(value)
}

// DoubleSHA256 hashes value twice with SHA-256.
func DoubleSHA256(value []byte) []byte {
	return SHA256(SHA256(value))
}

// DoubleSha256 is an alias for DoubleSHA256.
func DoubleSha256(value []byte) []byte {
	return DoubleSHA256(value)
}

// DoubleSHA256Hex returns the double SHA-256 digest as a lower-case hex string.
func DoubleSHA256Hex(value []byte) string {
	return BytesToHex(DoubleSHA256(value))
}

// DoubleSha256Hex is an alias for DoubleSHA256Hex.
func DoubleSha256Hex(value []byte) string {
	return DoubleSHA256Hex(value)
}

// Checksum4 returns the first 4 bytes of DoubleSHA256(value), a common block-chain checksum.
func Checksum4(value []byte) []byte {
	return CloneBytes(DoubleSHA256(value)[:4])
}

// SHA512 returns the SHA-512 digest of value.
func SHA512(value []byte) []byte {
	sum := sha512.Sum512(value)
	return CloneBytes(sum[:])
}

// Sha512 is an alias for SHA512.
func Sha512(value []byte) []byte {
	return SHA512(value)
}

// SHA512Hex returns the SHA-512 digest encoded as a lower-case hex string.
func SHA512Hex(value []byte) string {
	return BytesToHex(SHA512(value))
}

// HMACSHA512 returns HMAC-SHA512(key, data).
func HMACSHA512(key []byte, data []byte) []byte {
	mac := hmac.New(sha512.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

// CloneBytes returns a copy of value.
func CloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}

// ConcatBytes concatenates all byte slices into a new byte slice.
func ConcatBytes(parts ...[]byte) []byte {
	total := 0
	for _, part := range parts {
		total += len(part)
	}
	joined := make([]byte, total)
	offset := 0
	for _, part := range parts {
		offset += copy(joined[offset:], part)
	}
	return joined
}

// ReverseBytes returns a reversed copy of value.
func ReverseBytes(value []byte) []byte {
	reversed := CloneBytes(value)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	return reversed
}

func requireLength(value []byte, want int, name string) error {
	if len(value) != want {
		return fmt.Errorf("utils: %s requires %d bytes, got %d", name, want, len(value))
	}
	return nil
}
