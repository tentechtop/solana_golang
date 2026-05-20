package utils

import (
	"fmt"
	"math/big"
)

const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

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

// Base58Encode encodes value using the Bitcoin/Solana Base58 alphabet.
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

// Base58Decode decodes value using the Bitcoin/Solana Base58 alphabet.
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

func reverseBytesInPlace(value []byte) {
	for left, right := 0, len(value)-1; left < right; left, right = left+1, right-1 {
		value[left], value[right] = value[right], value[left]
	}
}
