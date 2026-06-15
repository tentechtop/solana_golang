package vm

import (
	"bytes"
	"crypto/sha256"
	"fmt"
)

const pdaDomain = "GoSVMProgramDerivedAddress"

// CreateProgramAddress 派生程序地址 + 用域隔离和 seed 限制保证确定性。
func CreateProgramAddress(programID Address, seeds [][]byte) (Address, error) {
	var address Address
	if programID == (Address{}) {
		return address, fmt.Errorf("%w: pda program id is empty", ErrInvalidAccount)
	}
	if len(seeds) == 0 || len(seeds) > MaxPDASeedCount {
		return address, fmt.Errorf("%w: pda seed count %d exceeds bounds", ErrInvalidAccount, len(seeds))
	}
	buffer := bytes.Buffer{}
	buffer.WriteString(pdaDomain)
	buffer.Write(programID[:])
	for seedIndex, seed := range seeds {
		if len(seed) == 0 || len(seed) > MaxPDASeedLength {
			return address, fmt.Errorf("%w: pda seed %d length %d exceeds bounds", ErrInvalidAccount, seedIndex, len(seed))
		}
		buffer.WriteByte(byte(len(seed)))
		buffer.Write(seed)
	}
	sum := sha256.Sum256(buffer.Bytes())
	copy(address[:], sum[:])
	if address == (Address{}) {
		return address, fmt.Errorf("%w: pda derived zero address", ErrInvalidAccount)
	}
	return address, nil
}
