package utils

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	_ "embed"
	"fmt"
	"strings"
)

const (
	bip39MinEntropyBits = 128
	bip39MaxEntropyBits = 256
	bip39WordBitSize    = 11
	bip39WordCount      = 2048
)

//go:embed bip39_english.txt
var bip39EnglishWordList string

var (
	bip39EnglishWords []string
	bip39EnglishMap   map[string]int
)

func init() {
	bip39EnglishWords = strings.Fields(bip39EnglishWordList)
	if len(bip39EnglishWords) != bip39WordCount {
		panic(fmt.Sprintf("utils: invalid BIP39 English wordlist size %d", len(bip39EnglishWords)))
	}
	bip39EnglishMap = make(map[string]int, len(bip39EnglishWords))
	for i, word := range bip39EnglishWords {
		bip39EnglishMap[word] = i
	}
}

// NewBIP39Entropy creates cryptographically secure entropy for a BIP-39 mnemonic.
func NewBIP39Entropy(bitSize int) ([]byte, error) {
	if err := validateBIP39EntropyBitSize(bitSize); err != nil {
		return nil, err
	}
	entropy := make([]byte, bitSize/8)
	if _, err := rand.Read(entropy); err != nil {
		return nil, fmt.Errorf("utils: generate bip39 entropy: %w", err)
	}
	return entropy, nil
}

// NewBIP39Mnemonic converts entropy into an English BIP-39 mnemonic.
func NewBIP39Mnemonic(entropy []byte) (string, error) {
	entropyBitSize := len(entropy) * 8
	if err := validateBIP39EntropyBitSize(entropyBitSize); err != nil {
		return "", err
	}

	checksum := sha256.Sum256(entropy)
	checksumBitSize := entropyBitSize / 32
	totalBitSize := entropyBitSize + checksumBitSize
	words := make([]string, totalBitSize/bip39WordBitSize)

	for wordIndex := range words {
		index := 0
		for bitOffset := 0; bitOffset < bip39WordBitSize; bitOffset++ {
			bitIndex := wordIndex*bip39WordBitSize + bitOffset
			index <<= 1
			if bitIndex < entropyBitSize {
				index |= getBit(entropy, bitIndex)
			} else {
				index |= getBit(checksum[:], bitIndex-entropyBitSize)
			}
		}
		words[wordIndex] = bip39EnglishWords[index]
	}
	return strings.Join(words, " "), nil
}

// EntropyFromBIP39Mnemonic validates a mnemonic and returns its original entropy.
func EntropyFromBIP39Mnemonic(mnemonic string) ([]byte, error) {
	words := strings.Fields(mnemonic)
	if len(words)%3 != 0 || len(words) < 12 || len(words) > 24 {
		return nil, fmt.Errorf("utils: invalid bip39 mnemonic word count %d", len(words))
	}

	totalBitSize := len(words) * bip39WordBitSize
	checksumBitSize := totalBitSize % 32
	entropyBitSize := totalBitSize - checksumBitSize
	if err := validateBIP39EntropyBitSize(entropyBitSize); err != nil {
		return nil, err
	}

	entropy := make([]byte, entropyBitSize/8)
	checksumBits := make([]byte, checksumBitSize)
	for wordPosition, word := range words {
		index, ok := bip39EnglishMap[word]
		if !ok {
			return nil, fmt.Errorf("utils: bip39 word %q not found", word)
		}
		for bitOffset := 0; bitOffset < bip39WordBitSize; bitOffset++ {
			bit := (index >> (bip39WordBitSize - 1 - bitOffset)) & 1
			bitIndex := wordPosition*bip39WordBitSize + bitOffset
			if bitIndex < entropyBitSize {
				setBit(entropy, bitIndex, bit)
			} else {
				checksumBits[bitIndex-entropyBitSize] = byte(bit)
			}
		}
	}

	expectedChecksum := sha256.Sum256(entropy)
	for i, got := range checksumBits {
		if got != byte(getBit(expectedChecksum[:], i)) {
			return nil, fmt.Errorf("utils: bip39 checksum incorrect")
		}
	}
	return entropy, nil
}

// IsBIP39MnemonicValid reports whether mnemonic is a valid English BIP-39 mnemonic.
func IsBIP39MnemonicValid(mnemonic string) bool {
	_, err := EntropyFromBIP39Mnemonic(mnemonic)
	return err == nil
}

// NewBIP39Seed derives the 64-byte BIP-39 seed with PBKDF2-HMAC-SHA512.
func NewBIP39Seed(mnemonic string, passphrase string) ([]byte, error) {
	normalized := strings.Join(strings.Fields(mnemonic), " ")
	if _, err := EntropyFromBIP39Mnemonic(normalized); err != nil {
		return nil, err
	}
	seed, err := pbkdf2.Key(sha512.New, normalized, []byte("mnemonic"+passphrase), 2048, bip39SeedLength)
	if err != nil {
		return nil, fmt.Errorf("utils: derive bip39 seed: %w", err)
	}
	return seed, nil
}

func validateBIP39EntropyBitSize(bitSize int) error {
	if bitSize < bip39MinEntropyBits || bitSize > bip39MaxEntropyBits || bitSize%32 != 0 {
		return fmt.Errorf("utils: bip39 entropy length must be [%d, %d] bits and a multiple of 32, got %d", bip39MinEntropyBits, bip39MaxEntropyBits, bitSize)
	}
	return nil
}

func getBit(data []byte, bitIndex int) int {
	return int((data[bitIndex/8] >> (7 - uint(bitIndex%8))) & 1)
}

func setBit(data []byte, bitIndex int, value int) {
	if value == 0 {
		return
	}
	data[bitIndex/8] |= 1 << (7 - uint(bitIndex%8))
}
