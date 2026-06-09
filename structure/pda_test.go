package structure

import (
	"bytes"
	"testing"

	"solana_golang/utils"
)

func TestPDAHelpers(t *testing.T) {
	programID, err := PublicKeyFromBase58("11111111111111111111111111111111")
	if err != nil {
		t.Fatalf("PublicKeyFromBase58(system program) error = %v", err)
	}

	address, bump, err := FindProgramAddress([][]byte{[]byte("vault")}, programID)
	if err != nil {
		t.Fatalf("FindProgramAddress() error = %v", err)
	}
	if IsOnEd25519Curve(address[:]) {
		t.Fatal("PDA is on Ed25519 curve, want off curve")
	}

	created, err := CreateProgramAddress([][]byte{[]byte("vault"), []byte{bump}}, programID)
	if err != nil {
		t.Fatalf("CreateProgramAddress() with bump error = %v", err)
	}
	if !address.Equal(created) {
		t.Fatal("CreateProgramAddress() with bump did not match FindProgramAddress()")
	}

	withSeed, err := CreateWithSeed(programID, "seed", programID)
	if err != nil {
		t.Fatalf("CreateWithSeed() error = %v", err)
	}
	want := utils.SHA256(utils.ConcatBytes(programID[:], []byte("seed"), programID[:]))
	if !bytes.Equal(withSeed[:], want) {
		t.Fatalf("CreateWithSeed() = %x, want %x", withSeed[:], want)
	}
}

func TestEd25519CurveCheck(t *testing.T) {
	seed := bytes.Repeat([]byte{0x01}, utils.Ed25519KeySize)
	publicKey, err := utils.DeriveEd25519PublicKeyFromPrivateKey(seed)
	if err != nil {
		t.Fatalf("DeriveEd25519PublicKeyFromPrivateKey() error = %v", err)
	}
	if !IsOnEd25519Curve(publicKey) {
		t.Fatal("valid Ed25519 public key reported off curve")
	}
	if IsOnEd25519Curve([]byte{1, 2, 3}) {
		t.Fatal("short input reported on curve")
	}
	identity := make([]byte, PublicKeySize)
	identity[0] = 1
	if !IsOnEd25519Curve(identity) {
		t.Fatal("identity point reported off curve")
	}
	negativeIdentityEncoding := utils.CloneBytes(identity)
	negativeIdentityEncoding[31] = 0x80
	if IsOnEd25519Curve(negativeIdentityEncoding) {
		t.Fatal("non-canonical negative identity encoding reported on curve")
	}
}

func TestPDAInvalidInput(t *testing.T) {
	programID, err := PublicKeyFromBase58("11111111111111111111111111111111")
	if err != nil {
		t.Fatalf("PublicKeyFromBase58(system program) error = %v", err)
	}
	if _, err := CreateProgramAddress([][]byte{bytes.Repeat([]byte{1}, maxSeedLength+1)}, programID); err == nil {
		t.Fatal("CreateProgramAddress(long seed) error = nil, want error")
	}
	tooManySeeds := make([][]byte, maxSeeds+1)
	for seedIndex := range tooManySeeds {
		tooManySeeds[seedIndex] = []byte{byte(seedIndex)}
	}
	if _, err := CreateProgramAddress(tooManySeeds, programID); err == nil {
		t.Fatal("CreateProgramAddress(too many seeds) error = nil, want error")
	}
	if _, _, err := FindProgramAddress(tooManySeeds[:maxSeeds], programID); err == nil {
		t.Fatal("FindProgramAddress(no bump room) error = nil, want error")
	}
	if _, err := CreateWithSeed(programID, string(bytes.Repeat([]byte{'a'}, maxSeedStringLength+1)), programID); err == nil {
		t.Fatal("CreateWithSeed(long seed) error = nil, want error")
	}
}
