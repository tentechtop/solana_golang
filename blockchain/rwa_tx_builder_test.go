package blockchain

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	vmprogram "github.com/tentechtop/ordexus-core/programs/vm"
)

func TestNewBootstrapRWAAssetTransactionBuildsShareAndNFT(t *testing.T) {
	hash := func(value string) string {
		sum := sha256.Sum256([]byte(value))
		return hex.EncodeToString(sum[:])
	}
	for _, test := range []struct {
		name          string
		assetType     uint8
		symbol        string
		maxSupply     uint64
		initialSupply uint64
	}{
		{name: "share", assetType: vmprogram.RWAAssetShare, symbol: "RSHARE", maxSupply: 1000, initialSupply: 100},
		{name: "nft", assetType: vmprogram.RWAAssetNFT, symbol: "RNFT", maxSupply: 1, initialSupply: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			payer := testDeployKeyPair(t, 41+test.assetType)
			program := testDeployKeyPair(t, 51+test.assetType)
			transaction, err := NewBootstrapRWAAssetTransaction(RWABootstrapParams{
				Payer: payer, Program: program.PublicKey, AssetType: test.assetType,
				Name: "Regulated Asset", Symbol: test.symbol, MaxSupply: test.maxSupply,
				InitialSupply: test.initialSupply, DocumentHash: hash("documents-" + test.name),
				IdentityHash: hash("identity-" + test.name), RecentBlockhash: testDeployHash(61 + test.assetType),
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(transaction.Instructions) != 5 || transaction.RequiredSignatureCount() != 3 {
				t.Fatalf("instructions/signatures = %d/%d", len(transaction.Instructions), transaction.RequiredSignatureCount())
			}
			valid, err := transaction.HasValidSignatures()
			if err != nil || !valid {
				t.Fatalf("invalid signatures: %v", err)
			}
			accounts, err := DeriveRWABootstrapAccounts(program.PublicKey, payer.PublicKey, test.symbol)
			if err != nil || transaction.Accounts[1].PublicKey != accounts.Mint.PublicKey || transaction.Accounts[2].PublicKey != accounts.Holding.PublicKey {
				t.Fatalf("canonical accounts mismatch: %v", err)
			}
		})
	}
}

func TestNewBootstrapRWAAssetTransactionAllowsZeroInitialSupply(t *testing.T) {
	payer := testDeployKeyPair(t, 71)
	program := testDeployKeyPair(t, 72)
	sum := sha256.Sum256([]byte("zero-supply"))
	hash := hex.EncodeToString(sum[:])
	transaction, err := NewBootstrapRWAAssetTransaction(RWABootstrapParams{Payer: payer, Program: program.PublicKey, AssetType: vmprogram.RWAAssetShare, Name: "Future Issue", Symbol: "FUTURE", MaxSupply: 100, DocumentHash: hash, IdentityHash: hash, RecentBlockhash: testDeployHash(73)})
	if err != nil {
		t.Fatal(err)
	}
	if len(transaction.Instructions) != 4 {
		t.Fatalf("instructions = %d, want 4", len(transaction.Instructions))
	}
}
