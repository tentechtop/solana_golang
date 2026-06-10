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
