package runtime_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/tentechtop/ordexus-core/blockchain"
	vmprogram "github.com/tentechtop/ordexus-core/programs/vm"
	"github.com/tentechtop/ordexus-core/structure"
)

func TestRWABootstrapTransactionExecutesAtomicallyInVirtualMachine(t *testing.T) {
	payerKey, payerPrivateKey := newSimulationSigner(t)
	programKey, programPrivateKey := newSimulationSigner(t)
	blockhash := newTestHash(231)
	accounts := []AddressedAccount{
		newSimulationAccount(t, payerKey, 100_000_000_000, DefaultBuiltinProgramIDs.System, false),
	}
	programData, err := vmprogram.RWAProgramData()
	if err != nil {
		t.Fatal(err)
	}
	accounts = deployVMContractProgram(t, accounts, payerKey, payerPrivateKey, programKey, programPrivateKey, programData, blockhash, 231)
	hash := func(value string) string {
		sum := sha256.Sum256([]byte(value))
		return hex.EncodeToString(sum[:])
	}
	transaction, err := blockchain.NewBootstrapRWAAssetTransaction(blockchain.RWABootstrapParams{
		Payer:   structure.OrdexusKeyPair{PublicKey: payerKey, PrivateKey: payerPrivateKey},
		Program: programKey, AssetType: vmprogram.RWAAssetShare, Name: "Runtime Equity", Symbol: "RTEQ",
		MaxSupply: 1000, InitialSupply: 250, DocumentHash: hash("runtime-rwa-documents"),
		IdentityHash: hash("runtime-rwa-issuer-identity"), RecentBlockhash: blockhash,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := simulateWithVirtualMachine(t, TransactionSimulationInput{
		Transaction: transaction, Accounts: accounts,
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, 231), CurrentSlot: 231,
	})
	if err != nil || result.Status != TransactionStatusConfirmed {
		t.Fatalf("bootstrap status=%v error=%+v execute=%v", result.Status, result.Error, err)
	}
	accounts = mergeSimulationAccounts(accounts, result.WrittenAccounts)
	stateAccounts, err := blockchain.DeriveRWABootstrapAccounts(programKey, payerKey, "RTEQ")
	if err != nil {
		t.Fatal(err)
	}
	var mintData, holdingData []byte
	for _, account := range accounts {
		switch account.Address {
		case stateAccounts.Mint.PublicKey:
			mintData = account.Account.Data
		case stateAccounts.Holding.PublicKey:
			holdingData = account.Account.Data
		}
	}
	mint, err := vmprogram.UnmarshalRWAMint(mintData)
	if err != nil || mint.Supply != 250 || mint.MaxSupply != 1000 || mint.AssetType != vmprogram.RWAAssetShare {
		t.Fatalf("mint=%+v error=%v", mint, err)
	}
	holding, err := vmprogram.UnmarshalRWAHolding(holdingData)
	if err != nil || holding.Amount != 250 || !holding.Approved || holding.Owner != payerKey || holding.Mint != stateAccounts.Mint.PublicKey {
		t.Fatalf("holding=%+v error=%v", holding, err)
	}
}

func TestRWAJavaScriptWalletFixture(t *testing.T) {
	var fixture struct {
		Blockhash string                         `json:"blockhash"`
		Issuer    string                         `json:"issuer"`
		Holder    string                         `json:"holder"`
		Outsider  string                         `json:"outsider"`
		Share     struct{ Program, Mint string } `json:"share"`
		NFT       struct{ Program, Mint string } `json:"nft"`
		Steps     []struct {
			Label       string `json:"label"`
			Success     bool   `json:"success"`
			Slot        uint64 `json:"slot"`
			Transaction string `json:"transaction"`
		} `json:"steps"`
	}
	data, err := os.ReadFile("testdata/rwa-wallet.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	decodeKey := func(value string) PublicKey {
		key, err := structure.PublicKeyFromBase58(value)
		if err != nil {
			t.Fatal(err)
		}
		return key
	}
	issuer, holder, outsider := decodeKey(fixture.Issuer), decodeKey(fixture.Holder), decodeKey(fixture.Outsider)
	accounts := []AddressedAccount{
		newSimulationAccount(t, issuer, 200_000_000_000, DefaultBuiltinProgramIDs.System, false),
		newSimulationAccount(t, holder, 200_000_000_000, DefaultBuiltinProgramIDs.System, false),
		newSimulationAccount(t, outsider, 200_000_000_000, DefaultBuiltinProgramIDs.System, false),
	}
	for _, step := range fixture.Steps {
		wire, err := base64.StdEncoding.DecodeString(step.Transaction)
		if err != nil {
			t.Fatal(err)
		}
		transaction, err := structure.UnmarshalTransactionBinary(wire)
		if err != nil {
			t.Fatalf("%s: %v", step.Label, err)
		}
		result, err := simulateWithVirtualMachine(t, TransactionSimulationInput{
			Transaction: transaction, Accounts: accounts,
			BlockhashQueue: newSimulationBlockhashQueue(t, transaction.RecentBlockhash, step.Slot), CurrentSlot: step.Slot,
		})
		if err != nil {
			t.Fatalf("%s: %v", step.Label, err)
		}
		if (result.Status == TransactionStatusConfirmed) != step.Success {
			t.Fatalf("%s: status=%v expected success=%v error=%+v", step.Label, result.Status, step.Success, result.Error)
		}
		next := mergeSimulationAccounts(accounts, result.WrittenAccounts)
		if !step.Success {
			for _, candidate := range next {
				if candidate.Address == transaction.Accounts[0].PublicKey {
					continue // failed transactions may charge only the payer fee
				}
				for _, previous := range accounts {
					if previous.Address == candidate.Address && (previous.Account.Lamports != candidate.Account.Lamports || previous.Account.Owner != candidate.Account.Owner || previous.Account.Executable != candidate.Account.Executable || !bytes.Equal(previous.Account.Data, candidate.Account.Data)) {
						t.Fatalf("%s changed %s after rejection", step.Label, candidate.Address)
					}
				}
			}
		}
		accounts = next
	}
	accountMap := map[PublicKey]Account{}
	for _, account := range accounts {
		accountMap[account.Address] = account.Account
	}
	shareProgram, shareMint := decodeKey(fixture.Share.Program), decodeKey(fixture.Share.Mint)
	share, err := vmprogram.UnmarshalRWAMint(accountMap[shareMint].Data)
	if err != nil || share.AssetType != vmprogram.RWAAssetShare || share.Supply != 100 {
		t.Fatalf("share mint=%+v error=%v", share, err)
	}
	issuerShare, err := vmprogram.UnmarshalRWAHolding(accountMap[vmprogram.RWAHoldingAddress(shareProgram, shareMint, issuer)].Data)
	if err != nil || issuerShare.Amount != 75 {
		t.Fatalf("issuer share=%+v error=%v", issuerShare, err)
	}
	holderShare, err := vmprogram.UnmarshalRWAHolding(accountMap[vmprogram.RWAHoldingAddress(shareProgram, shareMint, holder)].Data)
	if err != nil || holderShare.Amount != 25 || !holderShare.Approved {
		t.Fatalf("holder share=%+v error=%v", holderShare, err)
	}
	nftProgram, nftMint := decodeKey(fixture.NFT.Program), decodeKey(fixture.NFT.Mint)
	nft, err := vmprogram.UnmarshalRWAMint(accountMap[nftMint].Data)
	if err != nil || nft.AssetType != vmprogram.RWAAssetNFT || nft.Supply != 1 {
		t.Fatalf("NFT mint=%+v error=%v", nft, err)
	}
	holderNFT, err := vmprogram.UnmarshalRWAHolding(accountMap[vmprogram.RWAHoldingAddress(nftProgram, nftMint, holder)].Data)
	if err != nil || holderNFT.Amount != 1 || !holderNFT.Approved {
		t.Fatalf("holder NFT=%+v error=%v", holderNFT, err)
	}
}
