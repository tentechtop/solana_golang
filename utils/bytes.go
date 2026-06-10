package utils

import (
	"encoding/binary"
	"fmt"
)

// IntToBytes 编码 int + 使用大端序保持网络序一致。
func IntToBytes(value int) []byte {
	return Int32ToBytes(int32(value))
}

// BytesToInt 解码 4 字节有符号整数 + 使用大端序保持网络序一致。
func BytesToInt(value []byte) (int, error) {
	decoded, err := BytesToInt32(value)
	if err != nil {
		return 0, err
	}
	return int(decoded), nil
}

// Int32ToBytes 编码 int32 + 使用大端序保持字典序一致。
func Int32ToBytes(value int32) []byte {
	encoded := make([]byte, 4)
	binary.BigEndian.PutUint32(encoded, uint32(value))
	return encoded
}

// BytesToInt32 解码 int32 + 校验长度防止越界读取。
func BytesToInt32(value []byte) (int32, error) {
	if err := requireLength(value, 4, "int32"); err != nil {
		return 0, err
	}
	return int32(binary.BigEndian.Uint32(value)), nil
}

// Int64ToBytes 编码 int64 + 使用大端序保持排序语义。
func Int64ToBytes(value int64) []byte {
	encoded := make([]byte, 8)
	binary.BigEndian.PutUint64(encoded, uint64(value))
	return encoded
}

// BytesToInt64 解码 int64 + 校验长度防止输入异常。
func BytesToInt64(value []byte) (int64, error) {
	if err := requireLength(value, 8, "int64"); err != nil {
		return 0, err
	}
	return int64(binary.BigEndian.Uint64(value)), nil
}

// Uint16ToBytes 编码 uint16 + 使用大端序保持网络序一致。
func Uint16ToBytes(value uint16) []byte {
	encoded := make([]byte, 2)
	binary.BigEndian.PutUint16(encoded, value)
	return encoded
}

// BytesToUint16 解码 uint16 + 校验长度防止越界读取。
func BytesToUint16(value []byte) (uint16, error) {
	if err := requireLength(value, 2, "uint16"); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(value), nil
}

// Uint32ToBytes 编码 uint32 + 使用大端序保持网络序一致。
func Uint32ToBytes(value uint32) []byte {
	encoded := make([]byte, 4)
	binary.BigEndian.PutUint32(encoded, value)
	return encoded
}

// BytesToUint32 解码 uint32 + 校验长度防止输入异常。
func BytesToUint32(value []byte) (uint32, error) {
	if err := requireLength(value, 4, "uint32"); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(value), nil
}

// Uint64ToBytes 编码 uint64 + 使用大端序保持排序语义。
func Uint64ToBytes(value uint64) []byte {
	encoded := make([]byte, 8)
	binary.BigEndian.PutUint64(encoded, value)
	return encoded
}

// BytesToUint64 解码 uint64 + 校验长度防止输入异常。
func BytesToUint64(value []byte) (uint64, error) {
	if err := requireLength(value, 8, "uint64"); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(value), nil
}

// Uint16ToBytesLE 编码 uint16 + 使用小端序兼容 Solana 数据格式。
func Uint16ToBytesLE(value uint16) []byte {
	encoded := make([]byte, 2)
	binary.LittleEndian.PutUint16(encoded, value)
	return encoded
}

// BytesToUint16LE 解码 uint16 + 使用小端序兼容链上数据。
func BytesToUint16LE(value []byte) (uint16, error) {
	if err := requireLength(value, 2, "uint16 little-endian"); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(value), nil
}

// Uint32ToBytesLE 编码 uint32 + 使用小端序兼容 Solana 数据格式。
func Uint32ToBytesLE(value uint32) []byte {
	encoded := make([]byte, 4)
	binary.LittleEndian.PutUint32(encoded, value)
	return encoded
}

// BytesToUint32LE 解码 uint32 + 使用小端序兼容链上数据。
func BytesToUint32LE(value []byte) (uint32, error) {
	if err := requireLength(value, 4, "uint32 little-endian"); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(value), nil
}

// Uint64ToBytesLE 编码 uint64 + 使用小端序兼容 Solana 数据格式。
func Uint64ToBytesLE(value uint64) []byte {
	encoded := make([]byte, 8)
	binary.LittleEndian.PutUint64(encoded, value)
	return encoded
}

// BytesToUint64LE 解码 uint64 + 使用小端序兼容链上数据。
func BytesToUint64LE(value []byte) (uint64, error) {
	if err := requireLength(value, 8, "uint64 little-endian"); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(value), nil
}

// Int16ToBytesLE 编码 int16 + 复用小端 uint16 编码逻辑。
func Int16ToBytesLE(value int16) []byte {
	return Uint16ToBytesLE(uint16(value))
}

// BytesToInt16LE 解码 int16 + 复用小端 uint16 校验逻辑。
func BytesToInt16LE(value []byte) (int16, error) {
	decoded, err := BytesToUint16LE(value)
	if err != nil {
		return 0, err
	}
	return int16(decoded), nil
}

// Int32ToBytesLE 编码 int32 + 复用小端 uint32 编码逻辑。
func Int32ToBytesLE(value int32) []byte {
	return Uint32ToBytesLE(uint32(value))
}

// BytesToInt32LE 解码 int32 + 复用小端 uint32 校验逻辑。
func BytesToInt32LE(value []byte) (int32, error) {
	decoded, err := BytesToUint32LE(value)
	if err != nil {
		return 0, err
	}
	return int32(decoded), nil
}

// Int64ToBytesLE 编码 int64 + 复用小端 uint64 编码逻辑。
func Int64ToBytesLE(value int64) []byte {
	return Uint64ToBytesLE(uint64(value))
}

// BytesToInt64LE 解码 int64 + 复用小端 uint64 校验逻辑。
func BytesToInt64LE(value []byte) (int64, error) {
	decoded, err := BytesToUint64LE(value)
	if err != nil {
		return 0, err
	}
	return int64(decoded), nil
}

// CloneBytes 复制字节切片 + 防止调用方共享底层数组。
func CloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}

// ConcatBytes 拼接字节切片 + 预分配容量降低内存分配。
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

// ReverseBytes 反转字节切片 + 返回拷贝避免修改入参。
func ReverseBytes(value []byte) []byte {
	reversed := CloneBytes(value)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	return reversed
}
func reverseBytesInPlace(value []byte) {
	for left, right := 0, len(value)-1; left < right; left, right = left+1, right-1 {
		value[left], value[right] = value[right], value[left]
	}
}
func requireLength(value []byte, want int, name string) error {
	if len(value) != want {
		return fmt.Errorf("utils: %s requires %d bytes, got %d", name, want, len(value))
	}
	return nil
}
