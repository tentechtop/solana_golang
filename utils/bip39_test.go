package utils

import (
	"bytes"
	"strings"
	"testing"
)

const (
	bip39OfficialEntropyHex = "00000000000000000000000000000000"
	bip39OfficialMnemonic   = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
	bip39OfficialSeedHex    = "c55257c360c07c72029aebc1b53c05ed0362ada38ead3e3e9efa3708e53495531f09a6987599d18264c1e1c92f2cf141630c7a3c4ab7c81b2f001698e7463b04"
)

func TestBIP39OfficialVector(t *testing.T) {
	entropy, err := HexToBytes(bip39OfficialEntropyHex)
	if err != nil {
		t.Fatalf("HexToBytes() error = %v", err)
	}

	mnemonic, err := NewBIP39Mnemonic(entropy)
	if err != nil {
		t.Fatalf("NewBIP39Mnemonic() error = %v", err)
	}
	if mnemonic != bip39OfficialMnemonic {
		t.Fatalf("mnemonic = %q, want %q", mnemonic, bip39OfficialMnemonic)
	}

	restoredEntropy, err := EntropyFromBIP39Mnemonic(mnemonic)
	if err != nil {
		t.Fatalf("EntropyFromBIP39Mnemonic() error = %v", err)
	}
	if !bytes.Equal(restoredEntropy, entropy) {
		t.Fatalf("restored entropy = %x, want %x", restoredEntropy, entropy)
	}

	seed, err := NewBIP39Seed(mnemonic, "TREZOR")
	if err != nil {
		t.Fatalf("NewBIP39Seed() error = %v", err)
	}
	if got := BytesToHex(seed); got != bip39OfficialSeedHex {
		t.Fatalf("seed = %q, want %q", got, bip39OfficialSeedHex)
	}
}
func TestBIP39EntropyRoundTripSupportedSizes(t *testing.T) {
	for bitSize := 128; bitSize <= 256; bitSize += 32 {
		entropy := deterministicBIP39Entropy(bitSize)
		mnemonic, err := NewBIP39Mnemonic(entropy)
		if err != nil {
			t.Fatalf("NewBIP39Mnemonic(%d bits) error = %v", bitSize, err)
		}

		restoredEntropy, err := EntropyFromBIP39Mnemonic(mnemonic)
		if err != nil {
			t.Fatalf("EntropyFromBIP39Mnemonic(%d bits) error = %v", bitSize, err)
		}
		if !bytes.Equal(restoredEntropy, entropy) {
			t.Fatalf("restored entropy %d bits = %x, want %x", bitSize, restoredEntropy, entropy)
		}
		if !IsBIP39MnemonicValid(mnemonic) {
			t.Fatalf("IsBIP39MnemonicValid(%d bits) = false, want true", bitSize)
		}
	}
}
func TestNewBIP39EntropySizeValidation(t *testing.T) {
	validSizes := []int{128, 160, 192, 224, 256}
	for _, bitSize := range validSizes {
		entropy, err := NewBIP39Entropy(bitSize)
		if err != nil {
			t.Fatalf("NewBIP39Entropy(%d) error = %v", bitSize, err)
		}
		if len(entropy) != bitSize/8 {
			t.Fatalf("NewBIP39Entropy(%d) length = %d, want %d", bitSize, len(entropy), bitSize/8)
		}
	}

	invalidSizes := []int{0, 96, 129, 288}
	for _, bitSize := range invalidSizes {
		if _, err := NewBIP39Entropy(bitSize); err == nil {
			t.Fatalf("NewBIP39Entropy(%d) error = nil, want error", bitSize)
		}
	}
}
func TestNewBIP39MnemonicRejectsInvalidEntropy(t *testing.T) {
	invalidEntropyList := [][]byte{
		nil,
		make([]byte, 15),
		make([]byte, 33),
	}

	for _, entropy := range invalidEntropyList {
		if _, err := NewBIP39Mnemonic(entropy); err == nil {
			t.Fatalf("NewBIP39Mnemonic(%d bytes) error = nil, want error", len(entropy))
		}
	}
}
func TestEntropyFromBIP39MnemonicRejectsInvalidInput(t *testing.T) {
	invalidMnemonicList := []string{
		strings.Repeat("abandon ", 11),
		strings.Replace(bip39OfficialMnemonic, "about", "notaword", 1),
		strings.Replace(bip39OfficialMnemonic, "about", "abandon", 1),
	}

	for _, mnemonic := range invalidMnemonicList {
		if _, err := EntropyFromBIP39Mnemonic(mnemonic); err == nil {
			t.Fatalf("EntropyFromBIP39Mnemonic(%q) error = nil, want error", mnemonic)
		}
		if IsBIP39MnemonicValid(mnemonic) {
			t.Fatalf("IsBIP39MnemonicValid(%q) = true, want false", mnemonic)
		}
	}
}
func TestNewBIP39SeedNormalizesWhitespace(t *testing.T) {
	mnemonicWithExtraSpaces := "  abandon   abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about  "
	wantSeed, err := NewBIP39Seed(bip39OfficialMnemonic, "TREZOR")
	if err != nil {
		t.Fatalf("NewBIP39Seed(valid) error = %v", err)
	}

	gotSeed, err := NewBIP39Seed(mnemonicWithExtraSpaces, "TREZOR")
	if err != nil {
		t.Fatalf("NewBIP39Seed(extra spaces) error = %v", err)
	}
	if !bytes.Equal(gotSeed, wantSeed) {
		t.Fatalf("seed with extra spaces = %x, want %x", gotSeed, wantSeed)
	}
}
func deterministicBIP39Entropy(bitSize int) []byte {
	entropy := make([]byte, bitSize/8)
	for index := range entropy {
		entropy[index] = byte(index*17 + bitSize/8)
	}
	return entropy
}
