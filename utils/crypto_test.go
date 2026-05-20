package utils

import (
	"bytes"
	"testing"
)

func TestCryptoHelpers(t *testing.T) {
	random, err := RandomBytes(32)
	if err != nil {
		t.Fatalf("RandomBytes() error = %v", err)
	}
	if len(random) != 32 {
		t.Fatalf("RandomBytes() length = %d, want 32", len(random))
	}
	if _, err := RandomBytes(-1); err == nil {
		t.Fatal("RandomBytes(-1) error = nil, want error")
	}

	if !SecureEqual([]byte{1, 2, 3}, []byte{1, 2, 3}) {
		t.Fatal("SecureEqual(equal) = false, want true")
	}
	if SecureEqual([]byte{1, 2, 3}, []byte{1, 2, 4}) {
		t.Fatal("SecureEqual(different) = true, want false")
	}
	if SecureEqual([]byte{1, 2, 3}, []byte{1, 2}) {
		t.Fatal("SecureEqual(different length) = true, want false")
	}

	encoded := Base64Encode([]byte("hello"))
	if encoded != "aGVsbG8=" {
		t.Fatalf("Base64Encode() = %q, want aGVsbG8=", encoded)
	}
	decoded, err := Base64Decode(encoded)
	if err != nil {
		t.Fatalf("Base64Decode() error = %v", err)
	}
	if !bytes.Equal(decoded, []byte("hello")) {
		t.Fatalf("Base64Decode() = %q, want hello", decoded)
	}

	rawEncoded := Base64RawEncode([]byte("hello"))
	if rawEncoded != "aGVsbG8" {
		t.Fatalf("Base64RawEncode() = %q, want aGVsbG8", rawEncoded)
	}
	rawDecoded, err := Base64RawDecode(rawEncoded)
	if err != nil {
		t.Fatalf("Base64RawDecode() error = %v", err)
	}
	if !bytes.Equal(rawDecoded, []byte("hello")) {
		t.Fatalf("Base64RawDecode() = %q, want hello", rawDecoded)
	}
	if _, err := Base64Decode("not base64"); err == nil {
		t.Fatal("Base64Decode(invalid) error = nil, want error")
	}
	if _, err := Base64RawDecode("not base64"); err == nil {
		t.Fatal("Base64RawDecode(invalid) error = nil, want error")
	}
}
