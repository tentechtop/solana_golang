package blockchain

import (
	"fmt"
	"time"

	vmprogram "github.com/tentechtop/ordexus-core/programs/vm"
	"github.com/tentechtop/ordexus-core/structure"
)

// RWABootstrapParams creates the canonical mint and the issuer's compliant
// holding in one atomic transaction. DocumentHash and IdentityHash are lower-
// case SHA-256 hex commitments; personal/legal documents stay off chain.
type RWABootstrapParams struct {
	Payer           structure.OrdexusKeyPair
	Program         structure.PublicKey
	AssetType       uint8
	Name            string
	Symbol          string
	MaxSupply       uint64
	InitialSupply   uint64
	DocumentHash    string
	IdentityHash    string
	RecentBlockhash structure.Hash
}

type RWABootstrapAccounts struct {
	Mint    structure.OrdexusKeyPair
	Holding structure.OrdexusKeyPair
}

func DeriveRWAMintKeyPair(program, creator structure.PublicKey, symbol string) (structure.OrdexusKeyPair, error) {
	return deriveAssetStateKeyPair("nulla.rwa.mint.v1", program.String(), creator.String(), symbol)
}

func DeriveRWAHoldingKeyPair(program, mint, owner structure.PublicKey) (structure.OrdexusKeyPair, error) {
	return deriveAssetStateKeyPair("nulla.rwa.holding.v1", program.String(), mint.String(), owner.String())
}

func DeriveRWABootstrapAccounts(program, creator structure.PublicKey, symbol string) (RWABootstrapAccounts, error) {
	mint, err := DeriveRWAMintKeyPair(program, creator, symbol)
	if err != nil {
		return RWABootstrapAccounts{}, err
	}
	holding, err := DeriveRWAHoldingKeyPair(program, mint.PublicKey, creator)
	if err != nil {
		return RWABootstrapAccounts{}, err
	}
	return RWABootstrapAccounts{Mint: mint, Holding: holding}, nil
}

func NewBootstrapRWAAssetTransaction(params RWABootstrapParams) (structure.Transaction, error) {
	if params.Payer.PublicKey.IsZero() || params.Program.IsZero() {
		return structure.Transaction{}, fmt.Errorf("blockchain: RWA payer or program is empty")
	}
	if params.RecentBlockhash.IsZero() {
		return structure.Transaction{}, structure.ErrEmptyRecentBlockhash
	}
	if params.AssetType != vmprogram.RWAAssetShare && params.AssetType != vmprogram.RWAAssetNFT {
		return structure.Transaction{}, fmt.Errorf("blockchain: invalid RWA asset type")
	}
	if params.InitialSupply > params.MaxSupply || (params.AssetType == vmprogram.RWAAssetNFT && (params.MaxSupply != 1 || params.InitialSupply > 1)) {
		return structure.Transaction{}, fmt.Errorf("blockchain: invalid RWA supply")
	}
	descriptor := "share:" + params.DocumentHash
	if params.AssetType == vmprogram.RWAAssetNFT {
		descriptor = "nft:" + params.DocumentHash
	}
	initialize := vmprogram.AssetInstruction{Type: vmprogram.RWAInitialize, Amount: params.MaxSupply, Name: params.Name, Symbol: params.Symbol, URI: descriptor}
	setInvestor := vmprogram.AssetInstruction{Type: vmprogram.RWASetInvestor, Amount: 1, URI: params.IdentityHash}
	issue := vmprogram.AssetInstruction{Type: vmprogram.RWAIssue, Amount: params.InitialSupply}
	initializeData, err := initialize.MarshalBinary()
	if err != nil {
		return structure.Transaction{}, fmt.Errorf("blockchain: build RWA initialize: %w", err)
	}
	setInvestorData, err := setInvestor.MarshalBinary()
	if err != nil {
		return structure.Transaction{}, fmt.Errorf("blockchain: build RWA investor registration: %w", err)
	}
	var issueData []byte
	if params.InitialSupply > 0 {
		issueData, err = issue.MarshalBinary()
		if err != nil {
			return structure.Transaction{}, fmt.Errorf("blockchain: build RWA issuance: %w", err)
		}
	}
	accounts, err := DeriveRWABootstrapAccounts(params.Program, params.Payer.PublicKey, params.Symbol)
	if err != nil {
		return structure.Transaction{}, err
	}
	if accounts.Mint.PublicKey != vmprogram.RWAMintAddress(params.Program, params.Payer.PublicKey, params.Symbol) || accounts.Holding.PublicKey != vmprogram.RWAHoldingAddress(params.Program, accounts.Mint.PublicKey, params.Payer.PublicKey) {
		return structure.Transaction{}, fmt.Errorf("blockchain: RWA canonical derivation mismatch")
	}
	createMintData, err := newAssetStateCreateInstructionData(params.Program)
	if err != nil {
		return structure.Transaction{}, fmt.Errorf("blockchain: build RWA mint account: %w", err)
	}
	createHoldingData, err := newAssetStateCreateInstructionData(params.Program)
	if err != nil {
		return structure.Transaction{}, fmt.Errorf("blockchain: build RWA holding account: %w", err)
	}
	transaction := structure.Transaction{
		Accounts: []structure.AccountMeta{
			{PublicKey: params.Payer.PublicKey, IsSigner: true, IsWritable: true},
			{PublicKey: accounts.Mint.PublicKey, IsSigner: true, IsWritable: true},
			{PublicKey: accounts.Holding.PublicKey, IsSigner: true, IsWritable: true},
			{PublicKey: structure.DefaultBuiltinProgramIDs.System},
			{PublicKey: params.Program},
		},
		Instructions: []structure.CompiledInstruction{
			{ProgramIDIndex: 3, AccountIndexes: []uint8{0, 1}, Data: createMintData},
			{ProgramIDIndex: 3, AccountIndexes: []uint8{0, 2}, Data: createHoldingData},
			{ProgramIDIndex: 4, AccountIndexes: []uint8{0, 1}, Data: initializeData},
			{ProgramIDIndex: 4, AccountIndexes: []uint8{0, 1, 0, 2}, Data: setInvestorData},
		},
		RecentBlockhash: params.RecentBlockhash,
		SubmitTime:      time.Now().UnixMilli(),
	}
	if params.InitialSupply > 0 {
		transaction.Instructions = append(transaction.Instructions, structure.CompiledInstruction{ProgramIDIndex: 4, AccountIndexes: []uint8{0, 1, 2}, Data: issueData})
	}
	return transaction.Sign(map[structure.PublicKey][]byte{
		params.Payer.PublicKey:     params.Payer.PrivateKey,
		accounts.Mint.PublicKey:    accounts.Mint.PrivateKey,
		accounts.Holding.PublicKey: accounts.Holding.PrivateKey,
	})
}
