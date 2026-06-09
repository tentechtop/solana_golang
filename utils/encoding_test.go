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

func TestBase64Encoding(t *testing.T) {
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

func TestHexEncoding(t *testing.T) {
	value := []byte{0x00, 0x0f, 0x10, 0xff}

	if got := BytesToHex(value); got != "000f10ff" {
		t.Fatalf("BytesToHex() = %q, want %q", got, "000f10ff")
	}
	if got := BytesToHexWithPrefix(value); got != "0x000f10ff" {
		t.Fatalf("BytesToHexWithPrefix() = %q, want %q", got, "0x000f10ff")
	}

	decoded, err := HexToBytes(" 0x000F10ff ")
	if err != nil {
		t.Fatalf("HexToBytes() error = %v", err)
	}
	if !bytes.Equal(decoded, value) {
		t.Fatalf("HexToBytes() = %v, want %v", decoded, value)
	}
	if _, err := HexToBytes("0x0"); err == nil {
		t.Fatal("HexToBytes(odd length) error = nil, want error")
	}
	if IsHexString("0xzz") {
		t.Fatal("IsHexString(0xzz) = true, want false")
	}
}

func TestShortVecEncoding(t *testing.T) {
	cases := []struct {
		length int
		want   []byte
	}{
		{0, []byte{0x00}},
		{1, []byte{0x01}},
		{127, []byte{0x7f}},
		{128, []byte{0x80, 0x01}},
		{255, []byte{0xff, 0x01}},
		{300, []byte{0xac, 0x02}},
		{16383, []byte{0xff, 0x7f}},
		{16384, []byte{0x80, 0x80, 0x01}},
		{65535, []byte{0xff, 0xff, 0x03}},
	}

	for _, tc := range cases {
		encoded, err := EncodeShortVecLength(tc.length)
		if err != nil {
			t.Fatalf("EncodeShortVecLength(%d) error = %v", tc.length, err)
		}
		if !bytes.Equal(encoded, tc.want) {
			t.Fatalf("EncodeShortVecLength(%d) = %x, want %x", tc.length, encoded, tc.want)
		}
		decoded, read, err := DecodeShortVecLength(encoded)
		if err != nil {
			t.Fatalf("DecodeShortVecLength(%x) error = %v", encoded, err)
		}
		if decoded != tc.length || read != len(encoded) {
			t.Fatalf("DecodeShortVecLength(%x) = (%d, %d), want (%d, %d)", encoded, decoded, read, tc.length, len(encoded))
		}
	}
	if got := MustEncodeShortVecLength(300); !bytes.Equal(got, []byte{0xac, 0x02}) {
		t.Fatalf("MustEncodeShortVecLength(300) = %x, want ac02", got)
	}
}

func TestShortVecInvalidInput(t *testing.T) {
	if _, err := EncodeShortVecLength(-1); err == nil {
		t.Fatal("EncodeShortVecLength(-1) error = nil, want error")
	}
	if _, err := EncodeShortVecLength(65536); err == nil {
		t.Fatal("EncodeShortVecLength(65536) error = nil, want error")
	}
	if _, _, err := DecodeShortVecLength([]byte{0x80}); err == nil {
		t.Fatal("DecodeShortVecLength(incomplete) error = nil, want error")
	}
	if _, _, err := DecodeShortVecLength([]byte{0xff, 0xff, 0xff, 0x00}); err == nil {
		t.Fatal("DecodeShortVecLength(too long) error = nil, want error")
	}
}
