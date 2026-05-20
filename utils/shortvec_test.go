package utils

import (
	"bytes"
	"testing"
)

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
