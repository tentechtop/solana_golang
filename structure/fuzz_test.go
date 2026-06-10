package structure

import (
	"bytes"
	"testing"

	"solana_golang/utils"
)

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
		message := utils.SHA256(seed)
		signature, err := keyPair.Sign(message)
		if err != nil {
			t.Fatalf("Sign() error = %v", err)
		}
		if !keyPair.Verify(message, signature) {
			t.Fatal("Verify() = false, want true")
		}
	})
}
