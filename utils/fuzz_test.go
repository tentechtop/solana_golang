package utils

import (
	"bytes"
	"testing"
)

func FuzzBase58RoundTrip(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0, 0, 1, 2})
	f.Add([]byte("hello world"))
	f.Fuzz(func(t *testing.T, value []byte) {
		encoded := Base58Encode(value)
		decoded, err := Base58Decode(encoded)
		if err != nil {
			t.Fatalf("Base58Decode(Base58Encode(%x)) error = %v", value, err)
		}
		if !bytes.Equal(decoded, value) {
			t.Fatalf("Base58 round trip = %x, want %x", decoded, value)
		}
	})
}

func FuzzHexRoundTrip(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0, 15, 16, 255})
	f.Fuzz(func(t *testing.T, value []byte) {
		decoded, err := HexToBytes(BytesToHex(value))
		if err != nil {
			t.Fatalf("HexToBytes(BytesToHex(%x)) error = %v", value, err)
		}
		if !bytes.Equal(decoded, value) {
			t.Fatalf("hex round trip = %x, want %x", decoded, value)
		}
	})
}

func FuzzShortVecRoundTrip(f *testing.F) {
	f.Add(0)
	f.Add(127)
	f.Add(128)
	f.Add(65535)
	f.Fuzz(func(t *testing.T, length int) {
		if length < 0 || length > maxShortVecValue {
			t.Skip()
		}
		encoded, err := EncodeShortVecLength(length)
		if err != nil {
			t.Fatalf("EncodeShortVecLength(%d) error = %v", length, err)
		}
		decoded, read, err := DecodeShortVecLength(encoded)
		if err != nil {
			t.Fatalf("DecodeShortVecLength(%x) error = %v", encoded, err)
		}
		if decoded != length || read != len(encoded) {
			t.Fatalf("shortvec round trip = (%d, %d), want (%d, %d)", decoded, read, length, len(encoded))
		}
	})
}

func FuzzBIP39EntropyRoundTrip(f *testing.F) {
	f.Add(bytes.Repeat([]byte{0}, 16))
	f.Add(bytes.Repeat([]byte{1}, 20))
	f.Add(bytes.Repeat([]byte{2}, 32))
	f.Fuzz(func(t *testing.T, entropy []byte) {
		if len(entropy) < 16 || len(entropy) > 32 || len(entropy)%4 != 0 {
			t.Skip()
		}
		mnemonic, err := NewBIP39Mnemonic(entropy)
		if err != nil {
			t.Fatalf("NewBIP39Mnemonic(%x) error = %v", entropy, err)
		}
		restored, err := EntropyFromBIP39Mnemonic(mnemonic)
		if err != nil {
			t.Fatalf("EntropyFromBIP39Mnemonic(%q) error = %v", mnemonic, err)
		}
		if !bytes.Equal(restored, entropy) {
			t.Fatalf("BIP39 entropy round trip = %x, want %x", restored, entropy)
		}
	})
}

func FuzzSolanaKeyPairSeedSignVerify(f *testing.F) {
	f.Add(bytes.Repeat([]byte{1}, SolanaPrivateKeySeedSize))
	f.Add(bytes.Repeat([]byte{255}, SolanaPrivateKeySeedSize))
	f.Fuzz(func(t *testing.T, seed []byte) {
		if len(seed) != SolanaPrivateKeySeedSize {
			t.Skip()
		}
		keyPair, err := KeyPairFromSeed(seed)
		if err != nil {
			t.Fatalf("KeyPairFromSeed() error = %v", err)
		}
		message := SHA256(seed)
		signature, err := keyPair.Sign(message)
		if err != nil {
			t.Fatalf("Sign() error = %v", err)
		}
		if !keyPair.Verify(message, signature) {
			t.Fatal("Verify() = false, want true")
		}
	})
}
