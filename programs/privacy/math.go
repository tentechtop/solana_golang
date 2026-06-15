package privacy

import "fmt"

func safeAddUint64(left uint64, right uint64) (uint64, error) {
	if ^uint64(0)-left < right {
		return 0, fmt.Errorf("%w: uint64 overflow", ErrInsufficientLamports)
	}
	return left + right, nil
}
