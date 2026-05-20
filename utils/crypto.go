package utils

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
)

// RandomBytes returns n cryptographically secure random bytes.
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

// SecureEqual compares two byte slices in constant time when their lengths match.
func SecureEqual(a []byte, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare(a, b) == 1
}

// Base64Encode encodes value using standard RFC 4648 base64 with padding.
func Base64Encode(value []byte) string {
	return base64.StdEncoding.EncodeToString(value)
}

// Base64Decode decodes standard RFC 4648 base64 with padding.
func Base64Decode(value string) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("utils: decode base64: %w", err)
	}
	return decoded, nil
}

// Base64RawEncode encodes value using standard RFC 4648 base64 without padding.
func Base64RawEncode(value []byte) string {
	return base64.RawStdEncoding.EncodeToString(value)
}

// Base64RawDecode decodes standard RFC 4648 base64 without padding.
func Base64RawDecode(value string) ([]byte, error) {
	decoded, err := base64.RawStdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("utils: decode raw base64: %w", err)
	}
	return decoded, nil
}
