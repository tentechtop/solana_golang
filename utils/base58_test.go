package utils

import (
	"bytes"
	"testing"
)

func TestBase58EncodeDecode(t *testing.T) {
	value := []byte("hello world")
	encoded := Base58Encode(value)
	if encoded != "StV1DL6CwTryKyV" {
		t.Fatalf("Base58Encode() = %q, want %q", encoded, "StV1DL6CwTryKyV")
	}

	decoded, err := Base58Decode(encoded)
	if err != nil {
		t.Fatalf("Base58Decode() error = %v", err)
	}
	if !bytes.Equal(decoded, value) {
		t.Fatalf("Base58Decode() = %v, want %v", decoded, value)
	}
}

func TestBase58LeadingZeros(t *testing.T) {
	value := []byte{0x00, 0x00, 0x01, 0x02}
	encoded := Base58Encode(value)
	if encoded != "115T" {
		t.Fatalf("Base58Encode() = %q, want %q", encoded, "115T")
	}

	decoded, err := Base58Decode(encoded)
	if err != nil {
		t.Fatalf("Base58Decode() error = %v", err)
	}
	if !bytes.Equal(decoded, value) {
		t.Fatalf("Base58Decode() = %v, want %v", decoded, value)
	}
}

func TestBase58EmptyAndAllZeros(t *testing.T) {
	if got := Base58Encode(nil); got != "" {
		t.Fatalf("Base58Encode(nil) = %q, want empty", got)
	}
	decoded, err := Base58Decode("")
	if err != nil {
		t.Fatalf("Base58Decode(empty) error = %v", err)
	}
	if len(decoded) != 0 {
		t.Fatalf("Base58Decode(empty) length = %d, want 0", len(decoded))
	}

	value := []byte{0, 0, 0}
	encoded := Base58Encode(value)
	if encoded != "111" {
		t.Fatalf("Base58Encode(zeros) = %q, want 111", encoded)
	}
	decoded, err = Base58Decode(encoded)
	if err != nil {
		t.Fatalf("Base58Decode(zeros) error = %v", err)
	}
	if !bytes.Equal(decoded, value) {
		t.Fatalf("Base58Decode(zeros) = %v, want %v", decoded, value)
	}
}

func TestBase58InvalidInput(t *testing.T) {
	if _, err := Base58Decode("0OIl"); err == nil {
		t.Fatal("Base58Decode(invalid) error = nil, want error")
	}
}
