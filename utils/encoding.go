package utils

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
)

const (
	base58Alphabet   = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	maxShortVecValue = 0xffff
)

var base58Indexes = func() [256]int {
	var indexes [256]int
	for i := range indexes {
		indexes[i] = -1
	}
	for i := 0; i < len(base58Alphabet); i++ {
		indexes[base58Alphabet[i]] = i
	}
	return indexes
}()

// Base58Encode 编码 Base58 字符串 + 兼容 Bitcoin/Solana 字母表。
func Base58Encode(value []byte) string {
	if len(value) == 0 {
		return ""
	}

	x := new(big.Int).SetBytes(value)
	base := big.NewInt(58)
	zero := big.NewInt(0)
	mod := new(big.Int)

	encoded := make([]byte, 0, len(value)*2)
	for x.Cmp(zero) > 0 {
		x.DivMod(x, base, mod)
		encoded = append(encoded, base58Alphabet[mod.Int64()])
	}
	for _, b := range value {
		if b != 0 {
			break
		}
		encoded = append(encoded, base58Alphabet[0])
	}
	reverseBytesInPlace(encoded)
	return string(encoded)
}

// Base58Decode 解码 Base58 字符串 + 校验 Bitcoin/Solana 字母表合法性。
func Base58Decode(value string) ([]byte, error) {
	if value == "" {
		return []byte{}, nil
	}

	result := big.NewInt(0)
	base := big.NewInt(58)
	for i := 0; i < len(value); i++ {
		c := value[i]
		digit := -1
		if int(c) < len(base58Indexes) {
			digit = base58Indexes[c]
		}
		if digit < 0 {
			return nil, fmt.Errorf("utils: invalid base58 character %q at position %d", c, i)
		}
		result.Mul(result, base)
		result.Add(result, big.NewInt(int64(digit)))
	}

	decoded := result.Bytes()
	leadingZeros := 0
	for leadingZeros < len(value) && value[leadingZeros] == base58Alphabet[0] {
		leadingZeros++
	}
	if leadingZeros == 0 {
		return decoded, nil
	}
	out := make([]byte, leadingZeros+len(decoded))
	copy(out[leadingZeros:], decoded)
	return out, nil
}

// Base64Encode 编码标准 Base64 + 使用 RFC 4648 带填充格式。
func Base64Encode(value []byte) string {
	return base64.StdEncoding.EncodeToString(value)
}

// Base64Decode 解码标准 Base64 + 校验 RFC 4648 带填充格式。
func Base64Decode(value string) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("utils: decode base64: %w", err)
	}
	return decoded, nil
}

// Base64RawEncode 编码 Raw Base64 + 使用 RFC 4648 无填充格式。
func Base64RawEncode(value []byte) string {
	return base64.RawStdEncoding.EncodeToString(value)
}

// Base64RawDecode 解码 Raw Base64 + 校验 RFC 4648 无填充格式。
func Base64RawDecode(value string) ([]byte, error) {
	decoded, err := base64.RawStdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("utils: decode raw base64: %w", err)
	}
	return decoded, nil
}

// BytesToHex 转换小写十六进制字符串 + 默认不添加 0x 前缀。
func BytesToHex(value []byte) string {
	return hex.EncodeToString(value)
}

// BytesToHexWithPrefix 转换带前缀十六进制字符串 + 兼容 0x 表示法。
func BytesToHexWithPrefix(value []byte) string {
	return "0x" + BytesToHex(value)
}

// HexToBytes 解码十六进制字符串 + 兼容可选 0x/0X 前缀。
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

// IsHexString 判断十六进制是否合法 + 复用统一解码校验逻辑。
func IsHexString(value string) bool {
	_, err := HexToBytes(value)
	return err == nil
}

// EncodeShortVecLength 编码 Solana short_vec 长度 + 使用 compact-u16 格式。
func EncodeShortVecLength(length int) ([]byte, error) {
	if length < 0 {
		return nil, fmt.Errorf("utils: shortvec length cannot be negative")
	}
	if length > maxShortVecValue {
		return nil, fmt.Errorf("utils: shortvec length %d exceeds %d", length, maxShortVecValue)
	}

	value := uint16(length)
	encoded := make([]byte, 0, 3)
	for {
		elem := byte(value & 0x7f)
		value >>= 7
		if value == 0 {
			encoded = append(encoded, elem)
			return encoded, nil
		}
		encoded = append(encoded, elem|0x80)
	}
}

// DecodeShortVecLength 解码 Solana short_vec 长度 + 校验 compact-u16 边界。
func DecodeShortVecLength(value []byte) (length int, bytesRead int, err error) {
	var result uint32
	for i, b := range value {
		if i >= 3 {
			return 0, 0, fmt.Errorf("utils: shortvec length exceeds compact-u16 size")
		}
		result |= uint32(b&0x7f) << (7 * uint(i))
		if b&0x80 == 0 {
			if result > maxShortVecValue {
				return 0, 0, fmt.Errorf("utils: shortvec length %d exceeds %d", result, maxShortVecValue)
			}
			return int(result), i + 1, nil
		}
	}
	return 0, 0, fmt.Errorf("utils: shortvec data ended before terminator")
}

// MustEncodeShortVecLength 编码 short_vec 长度 + 仅用于测试快速失败。
func MustEncodeShortVecLength(length int) []byte {
	encoded, err := EncodeShortVecLength(length)
	if err != nil {
		panic(err)
	}
	return encoded
}
