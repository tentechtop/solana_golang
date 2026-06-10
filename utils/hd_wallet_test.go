package utils

import (
	"bytes"
	"strings"
	"testing"
)

const bip39VectorMnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"

// TestGenerateMnemonicAndSeed 验证目标行为 + 保证核心场景和边界条件稳定。
func TestGenerateMnemonicAndSeed(t *testing.T) {
	mnemonic, err := GenerateMnemonic()
	if err != nil {
		t.Fatalf("GenerateMnemonic() error = %v", err)
	}
	if len(mnemonic) != 12 {
		t.Fatalf("len(mnemonic) = %d, want 12", len(mnemonic))
	}

	seed, err := GenerateSeed(mnemonic, "")
	if err != nil {
		t.Fatalf("GenerateSeed() error = %v", err)
	}
	if len(seed) != bip39SeedLength {
		t.Fatalf("seed length = %d, want %d", len(seed), bip39SeedLength)
	}
}

// TestGenerateSeedBIP39Vector 验证目标行为 + 保证核心场景和边界条件稳定。
func TestGenerateSeedBIP39Vector(t *testing.T) {
	entropy, err := HexToBytes("00000000000000000000000000000000")
	if err != nil {
		t.Fatalf("HexToBytes(entropy) error = %v", err)
	}
	mnemonic, err := NewBIP39Mnemonic(entropy)
	if err != nil {
		t.Fatalf("NewBIP39Mnemonic() error = %v", err)
	}
	if mnemonic != bip39VectorMnemonic {
		t.Fatalf("mnemonic = %q, want %q", mnemonic, bip39VectorMnemonic)
	}

	restoredEntropy, err := EntropyFromBIP39Mnemonic(mnemonic)
	if err != nil {
		t.Fatalf("EntropyFromBIP39Mnemonic() error = %v", err)
	}
	if !bytes.Equal(restoredEntropy, entropy) {
		t.Fatalf("restored entropy = %x, want %x", restoredEntropy, entropy)
	}

	seed, err := GenerateSeedFromMnemonicString(bip39VectorMnemonic, "TREZOR")
	if err != nil {
		t.Fatalf("GenerateSeedFromMnemonicString() error = %v", err)
	}
	want := "c55257c360c07c72029aebc1b53c05ed0362ada38ead3e3e9efa3708e53495531f09a6987599d18264c1e1c92f2cf141630c7a3c4ab7c81b2f001698e7463b04"
	if got := BytesToHex(seed); got != want {
		t.Fatalf("seed = %q, want %q", got, want)
	}
}

// TestBIP39EntropyRoundTripAllSupportedSizes 验证目标行为 + 保证核心场景和边界条件稳定。
func TestBIP39EntropyRoundTripAllSupportedSizes(t *testing.T) {
	for entropyBytes := 16; entropyBytes <= 32; entropyBytes += 4 {
		entropy := make([]byte, entropyBytes)
		for i := range entropy {
			entropy[i] = byte(i*17 + entropyBytes)
		}
		mnemonic, err := NewBIP39Mnemonic(entropy)
		if err != nil {
			t.Fatalf("NewBIP39Mnemonic(%d bytes) error = %v", entropyBytes, err)
		}
		restored, err := EntropyFromBIP39Mnemonic(mnemonic)
		if err != nil {
			t.Fatalf("EntropyFromBIP39Mnemonic(%d bytes) error = %v", entropyBytes, err)
		}
		if !bytes.Equal(restored, entropy) {
			t.Fatalf("BIP39 entropy round trip %d bytes = %x, want %x", entropyBytes, restored, entropy)
		}
		if !IsBIP39MnemonicValid(mnemonic) {
			t.Fatalf("IsBIP39MnemonicValid(%q) = false, want true", mnemonic)
		}
	}
}

// TestBIP39InvalidInput 验证目标行为 + 保证核心场景和边界条件稳定。
func TestBIP39InvalidInput(t *testing.T) {
	if _, err := NewBIP39Entropy(96); err == nil {
		t.Fatal("NewBIP39Entropy(96) error = nil, want error")
	}
	if _, err := NewBIP39Mnemonic(make([]byte, 15)); err == nil {
		t.Fatal("NewBIP39Mnemonic(15 bytes) error = nil, want error")
	}
	if _, err := EntropyFromBIP39Mnemonic("abandon abandon abandon"); err == nil {
		t.Fatal("EntropyFromBIP39Mnemonic(short mnemonic) error = nil, want error")
	}
	if _, err := EntropyFromBIP39Mnemonic(strings.Replace(bip39VectorMnemonic, "about", "zoo", 1)); err == nil {
		t.Fatal("EntropyFromBIP39Mnemonic(bad checksum) error = nil, want error")
	}
	if _, err := EntropyFromBIP39Mnemonic(strings.Replace(bip39VectorMnemonic, "about", "notaword", 1)); err == nil {
		t.Fatal("EntropyFromBIP39Mnemonic(unknown word) error = nil, want error")
	}
	if _, err := NewBIP39Seed("not a valid mnemonic", ""); err == nil {
		t.Fatal("NewBIP39Seed(invalid mnemonic) error = nil, want error")
	}
}

// TestSLIP10Ed25519Vector 验证目标行为 + 保证核心场景和边界条件稳定。
func TestSLIP10Ed25519Vector(t *testing.T) {
	seed, err := HexToBytes("000102030405060708090a0b0c0d0e0f")
	if err != nil {
		t.Fatalf("HexToBytes(seed) error = %v", err)
	}

	root, err := GenerateRootHDNode(seed)
	if err != nil {
		t.Fatalf("GenerateRootHDNode() error = %v", err)
	}
	if got, want := BytesToHex(root.PrivateKey), "2b4be7f19ee27bbf30c667b642d5f4aa69fd169872f8fc3059c08ebae2eb19e7"; got != want {
		t.Fatalf("root private key = %q, want %q", got, want)
	}
	if got, want := BytesToHex(root.ChainCode), "90046a93de5380a72b5e45010748567d5ea02bbf6522f979e05c0d8d8ca9fffb"; got != want {
		t.Fatalf("root chain code = %q, want %q", got, want)
	}

	node, err := DeriveHDNodeFromSeedAndPath(seed, "m/0'/1'")
	if err != nil {
		t.Fatalf("DeriveHDNodeFromSeedAndPath() error = %v", err)
	}
	if got, want := BytesToHex(node.PrivateKey), "b1d0bad404bf35da785a64ca1ac54b2617211d2777696fbffaf208f746ae84f2"; got != want {
		t.Fatalf("m/0'/1' private key = %q, want %q", got, want)
	}
	if got, want := BytesToHex(node.ChainCode), "a320425f77d1b5c2505a6b1b27382b37368ee640e3557c315416801243552f14"; got != want {
		t.Fatalf("m/0'/1' chain code = %q, want %q", got, want)
	}
}

// TestSolanaKeyPairDerivation 验证目标行为 + 保证核心场景和边界条件稳定。
func TestSolanaKeyPairDerivation(t *testing.T) {
	seed, err := GenerateSeedFromMnemonicString(bip39VectorMnemonic, "")
	if err != nil {
		t.Fatalf("GenerateSeedFromMnemonicString() error = %v", err)
	}

	keyInfo, err := GetSolanaKeyPairFromSeed(seed, 0, 0)
	if err != nil {
		t.Fatalf("GetSolanaKeyPairFromSeed() error = %v", err)
	}
	if keyInfo.Path != "m/44'/501'/0'/0'" {
		t.Fatalf("path = %q, want m/44'/501'/0'/0'", keyInfo.Path)
	}
	if len(keyInfo.PrivateKey) != Ed25519KeySize {
		t.Fatalf("private key length = %d, want %d", len(keyInfo.PrivateKey), Ed25519KeySize)
	}
	if len(keyInfo.PublicKey) != Ed25519KeySize {
		t.Fatalf("public key length = %d, want %d", len(keyInfo.PublicKey), Ed25519KeySize)
	}
	decodedAddress, err := Base58Decode(keyInfo.Address)
	if err != nil {
		t.Fatalf("Base58Decode(address) error = %v", err)
	}
	if !bytes.Equal(decodedAddress, keyInfo.PublicKey) {
		t.Fatal("decoded address does not equal public key")
	}

	signature, err := Ed25519Sign(keyInfo.PrivateKey, []byte("solana hd wallet"))
	if err != nil {
		t.Fatalf("Ed25519Sign() error = %v", err)
	}
	if !Ed25519Verify(keyInfo.PublicKey, []byte("solana hd wallet"), signature) {
		t.Fatal("derived key pair signature verification failed")
	}

	repeated, err := GetSolanaKeyPairFromSeedAndPath(seed, keyInfo.Path)
	if err != nil {
		t.Fatalf("GetSolanaKeyPairFromSeedAndPath() error = %v", err)
	}
	if !bytes.Equal(repeated.PrivateKey, keyInfo.PrivateKey) ||
		!bytes.Equal(repeated.PublicKey, keyInfo.PublicKey) ||
		repeated.Address != keyInfo.Address {
		t.Fatal("same seed and path derived different key info")
	}
}

// TestGetSolanaKeyPairFromMnemonic 验证目标行为 + 保证核心场景和边界条件稳定。
func TestGetSolanaKeyPairFromMnemonic(t *testing.T) {
	words := strings.Fields(bip39VectorMnemonic)
	keyInfo, err := GetSolanaKeyPair(words, 0, 0)
	if err != nil {
		t.Fatalf("GetSolanaKeyPair() error = %v", err)
	}
	fromString, err := GetSolanaKeyPairFromMnemonicString(bip39VectorMnemonic, 0, 0)
	if err != nil {
		t.Fatalf("GetSolanaKeyPairFromMnemonicString() error = %v", err)
	}
	if fromString.Address != keyInfo.Address {
		t.Fatalf("GetSolanaKeyPairFromMnemonicString address = %s, want %s", fromString.Address, keyInfo.Address)
	}

	accountOne, err := GetSolanaKeyPair(words, 1, 0)
	if err != nil {
		t.Fatalf("GetSolanaKeyPair(account 1) error = %v", err)
	}
	addressOne, err := GetSolanaKeyPair(words, 0, 1)
	if err != nil {
		t.Fatalf("GetSolanaKeyPair(address 1) error = %v", err)
	}
	if keyInfo.Address == accountOne.Address {
		t.Fatal("account 0 and account 1 addresses are equal")
	}
	if keyInfo.Address == addressOne.Address {
		t.Fatal("address index 0 and 1 addresses are equal")
	}
}

// TestParseDerivationPath 验证目标行为 + 保证核心场景和边界条件稳定。
func TestParseDerivationPath(t *testing.T) {
	indexes, normalized, err := ParseDerivationPath(" M/44H/501h/0'/0/1' ")
	if err != nil {
		t.Fatalf("ParseDerivationPath() error = %v", err)
	}
	if normalized != "m/44'/501'/0'/0/1'" {
		t.Fatalf("normalized path = %q, want m/44'/501'/0'/0/1'", normalized)
	}
	want := []uint32{
		44 | hardenedDeriveOffset,
		501 | hardenedDeriveOffset,
		0 | hardenedDeriveOffset,
		0,
		1 | hardenedDeriveOffset,
	}
	for i := range want {
		if indexes[i] != want[i] {
			t.Fatalf("indexes[%d] = %d, want %d", i, indexes[i], want[i])
		}
	}
}

// TestHDWalletInvalidInput 验证目标行为 + 保证核心场景和边界条件稳定。
func TestHDWalletInvalidInput(t *testing.T) {
	if _, err := GenerateSeedFromMnemonicString("not a valid mnemonic", ""); err == nil {
		t.Fatal("GenerateSeedFromMnemonicString(invalid) error = nil, want error")
	}
	if _, err := GenerateRootHDNode([]byte{1, 2, 3}); err == nil {
		t.Fatal("GenerateRootHDNode(short seed) error = nil, want error")
	}
	if _, _, err := DeriveChildPrivateKey([]byte{1, 2, 3}, bytes.Repeat([]byte{0}, Ed25519KeySize), hardenedDeriveOffset); err == nil {
		t.Fatal("DeriveChildPrivateKey(short key) error = nil, want error")
	}
	root, err := GenerateRootHDNode(bytes.Repeat([]byte{0}, bip39SeedLength))
	if err != nil {
		t.Fatalf("GenerateRootHDNode() error = %v", err)
	}
	if _, err := DeriveChildHDNode(root, 0); err == nil {
		t.Fatal("DeriveChildHDNode(non-hardened) error = nil, want error")
	}
	if _, _, err := DerivePrivateKeyFromSeedAndPath(make([]byte, bip39SeedLength), "m//0"); err == nil {
		t.Fatal("DerivePrivateKeyFromSeedAndPath(bad path) error = nil, want error")
	}
	if _, _, err := DerivePrivateKeyFromSeedAndPath(make([]byte, bip39SeedLength), "m/44'/501'/0'/0/0"); err == nil {
		t.Fatal("DerivePrivateKeyFromSeedAndPath(non-hardened path) error = nil, want error")
	}
	if _, err := GetSolanaKeyPairFromSeed(make([]byte, bip39SeedLength), -1, 0); err == nil {
		t.Fatal("GetSolanaKeyPairFromSeed(negative account) error = nil, want error")
	}
	if _, err := GetSolanaAddress([]byte{1, 2, 3}); err == nil {
		t.Fatal("GetSolanaAddress(short key) error = nil, want error")
	}
	if _, err := GetSolanaKeyPairFromMnemonicString("not a valid mnemonic", 0, 0); err == nil {
		t.Fatal("GetSolanaKeyPairFromMnemonicString(invalid) error = nil, want error")
	}
	if _, err := GenerateRootPrivateKey(bytes.Repeat([]byte{0}, bip39SeedLength)); err != nil {
		t.Fatalf("GenerateRootPrivateKey() error = %v", err)
	}
}
