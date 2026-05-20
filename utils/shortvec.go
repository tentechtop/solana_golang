package utils

import "fmt"

const maxShortVecValue = 0xffff

// EncodeShortVecLength encodes a Solana short_vec compact-u16 length.
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

// DecodeShortVecLength decodes a Solana short_vec compact-u16 length.
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

// MustEncodeShortVecLength encodes a short_vec length and panics on error. Intended for tests.
func MustEncodeShortVecLength(length int) []byte {
	encoded, err := EncodeShortVecLength(length)
	if err != nil {
		panic(err)
	}
	return encoded
}
