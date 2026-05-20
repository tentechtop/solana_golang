package utils

import (
	"bytes"
	"testing"
)

func TestHexHelpers(t *testing.T) {
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

func TestIntegerHelpers(t *testing.T) {
	intBytes := IntToBytes(258)
	if !bytes.Equal(intBytes, []byte{0x00, 0x00, 0x01, 0x02}) {
		t.Fatalf("IntToBytes() = %v, want [0 0 1 2]", intBytes)
	}
	decodedInt, err := BytesToInt(intBytes)
	if err != nil {
		t.Fatalf("BytesToInt() error = %v", err)
	}
	if decodedInt != 258 {
		t.Fatalf("BytesToInt() = %d, want 258", decodedInt)
	}

	negative32, err := BytesToInt32(Int32ToBytes(-2))
	if err != nil {
		t.Fatalf("BytesToInt32() error = %v", err)
	}
	if negative32 != -2 {
		t.Fatalf("BytesToInt32() = %d, want -2", negative32)
	}

	unsigned16, err := BytesToUint16(Uint16ToBytes(65535))
	if err != nil {
		t.Fatalf("BytesToUint16() error = %v", err)
	}
	if unsigned16 != 65535 {
		t.Fatalf("BytesToUint16() = %d, want 65535", unsigned16)
	}

	unsigned32, err := BytesToUint32(Uint32ToBytes(4294967295))
	if err != nil {
		t.Fatalf("BytesToUint32() error = %v", err)
	}
	if unsigned32 != 4294967295 {
		t.Fatalf("BytesToUint32() = %d, want 4294967295", unsigned32)
	}

	negative64, err := BytesToInt64(Int64ToBytes(-123456789))
	if err != nil {
		t.Fatalf("BytesToInt64() error = %v", err)
	}
	if negative64 != -123456789 {
		t.Fatalf("BytesToInt64() = %d, want -123456789", negative64)
	}

	unsigned64, err := BytesToUint64(Uint64ToBytes(18446744073709551615))
	if err != nil {
		t.Fatalf("BytesToUint64() error = %v", err)
	}
	if unsigned64 != 18446744073709551615 {
		t.Fatalf("BytesToUint64() = %d, want 18446744073709551615", unsigned64)
	}

	if _, err := BytesToInt([]byte{0x01, 0x02}); err == nil {
		t.Fatal("BytesToInt(short input) error = nil, want error")
	}
}

func TestLittleEndianIntegerHelpers(t *testing.T) {
	if got := Uint16ToBytesLE(0x1234); !bytes.Equal(got, []byte{0x34, 0x12}) {
		t.Fatalf("Uint16ToBytesLE() = %x, want 3412", got)
	}
	u16, err := BytesToUint16LE([]byte{0x34, 0x12})
	if err != nil {
		t.Fatalf("BytesToUint16LE() error = %v", err)
	}
	if u16 != 0x1234 {
		t.Fatalf("BytesToUint16LE() = %#x, want 0x1234", u16)
	}

	if got := Uint32ToBytesLE(0x12345678); !bytes.Equal(got, []byte{0x78, 0x56, 0x34, 0x12}) {
		t.Fatalf("Uint32ToBytesLE() = %x, want 78563412", got)
	}
	u32, err := BytesToUint32LE([]byte{0x78, 0x56, 0x34, 0x12})
	if err != nil {
		t.Fatalf("BytesToUint32LE() error = %v", err)
	}
	if u32 != 0x12345678 {
		t.Fatalf("BytesToUint32LE() = %#x, want 0x12345678", u32)
	}

	if got := Uint64ToBytesLE(0x0102030405060708); !bytes.Equal(got, []byte{0x08, 0x07, 0x06, 0x05, 0x04, 0x03, 0x02, 0x01}) {
		t.Fatalf("Uint64ToBytesLE() = %x, want 0807060504030201", got)
	}
	u64, err := BytesToUint64LE([]byte{0x08, 0x07, 0x06, 0x05, 0x04, 0x03, 0x02, 0x01})
	if err != nil {
		t.Fatalf("BytesToUint64LE() error = %v", err)
	}
	if u64 != 0x0102030405060708 {
		t.Fatalf("BytesToUint64LE() = %#x, want 0x0102030405060708", u64)
	}

	i16, err := BytesToInt16LE(Int16ToBytesLE(-1234))
	if err != nil {
		t.Fatalf("BytesToInt16LE() error = %v", err)
	}
	if i16 != -1234 {
		t.Fatalf("BytesToInt16LE() = %d, want -1234", i16)
	}
	i32, err := BytesToInt32LE(Int32ToBytesLE(-123456))
	if err != nil {
		t.Fatalf("BytesToInt32LE() error = %v", err)
	}
	if i32 != -123456 {
		t.Fatalf("BytesToInt32LE() = %d, want -123456", i32)
	}
	i64, err := BytesToInt64LE(Int64ToBytesLE(-1234567890123))
	if err != nil {
		t.Fatalf("BytesToInt64LE() error = %v", err)
	}
	if i64 != -1234567890123 {
		t.Fatalf("BytesToInt64LE() = %d, want -1234567890123", i64)
	}

	if _, err := BytesToUint64LE([]byte{1, 2, 3}); err == nil {
		t.Fatal("BytesToUint64LE(short input) error = nil, want error")
	}
}

func TestHashHelpers(t *testing.T) {
	input := []byte("hello")
	wantSHA256 := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	wantSHA512 := "9b71d224bd62f3785d96d46ad3ea3d73319bfbc2890caadae2dff72519673ca72323c3d99ba5c11d7c7acc6e14b8c5da0c4663475c2e5c3adef46f73bcdec043"

	if got := SHA256Hex(input); got != wantSHA256 {
		t.Fatalf("SHA256Hex() = %q, want %q", got, wantSHA256)
	}
	if got := BytesToHex(Sha256(input)); got != wantSHA256 {
		t.Fatalf("Sha256() = %q, want %q", got, wantSHA256)
	}
	if got := Sha256Hex(input); got != wantSHA256 {
		t.Fatalf("Sha256Hex() = %q, want %q", got, wantSHA256)
	}

	doubleHash := DoubleSHA256(input)
	if !bytes.Equal(doubleHash, SHA256(SHA256(input))) {
		t.Fatal("DoubleSHA256() != SHA256(SHA256(input))")
	}
	if !bytes.Equal(DoubleSha256(input), doubleHash) {
		t.Fatal("DoubleSha256() != DoubleSHA256()")
	}
	if DoubleSHA256Hex(input) != BytesToHex(doubleHash) {
		t.Fatal("DoubleSHA256Hex() != hex DoubleSHA256()")
	}
	if DoubleSha256Hex(input) != DoubleSHA256Hex(input) {
		t.Fatal("DoubleSha256Hex() != DoubleSHA256Hex()")
	}
	if !bytes.Equal(Checksum4(input), doubleHash[:4]) {
		t.Fatal("Checksum4() != first 4 bytes of DoubleSHA256()")
	}
	if got := SHA512Hex(input); got != wantSHA512 {
		t.Fatalf("SHA512Hex() = %q, want %q", got, wantSHA512)
	}
	if !bytes.Equal(Sha512(input), SHA512(input)) {
		t.Fatal("Sha512() != SHA512()")
	}

	key := bytes.Repeat([]byte{0x0b}, 20)
	hmac := HMACSHA512(key, []byte("Hi There"))
	wantHMAC := "87aa7cdea5ef619d4ff0b4241a1d6cb02379f4e2ce4ec2787ad0b30545e17cdedaa833b7d6b8a702038b274eaea3f4e4be9d914eeb61f1702e696c203a126854"
	if got := BytesToHex(hmac); got != wantHMAC {
		t.Fatalf("HMACSHA512() = %q, want %q", got, wantHMAC)
	}
}

func TestByteSliceHelpers(t *testing.T) {
	original := []byte{1, 2, 3}
	cloned := CloneBytes(original)
	if !bytes.Equal(cloned, original) {
		t.Fatalf("CloneBytes() = %v, want %v", cloned, original)
	}
	cloned[0] = 9
	if original[0] != 1 {
		t.Fatal("CloneBytes() returned an aliased slice")
	}

	if got := ConcatBytes([]byte{1}, nil, []byte{2, 3}); !bytes.Equal(got, []byte{1, 2, 3}) {
		t.Fatalf("ConcatBytes() = %v, want [1 2 3]", got)
	}
	if got := ReverseBytes(original); !bytes.Equal(got, []byte{3, 2, 1}) {
		t.Fatalf("ReverseBytes() = %v, want [3 2 1]", got)
	}
	if ReverseBytes(nil) != nil {
		t.Fatal("ReverseBytes(nil) != nil")
	}
}
