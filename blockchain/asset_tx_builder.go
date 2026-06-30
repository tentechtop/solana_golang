package blockchain

import (
	"fmt"
	"strings"
	"time"

	vmprogram "solana_golang/programs/vm"
	"solana_golang/structure"
	"solana_golang/utils"
)

const (
	assetMintDerivationDomain    = "nulla.asset.mint.v1"
	assetBalanceDerivationDomain = "nulla.asset.balance.v1"
)

// FungibleAssetBootstrapParams 描述资产启动参数 + 一笔交易完成 mint、余额账户初始化和初始发行。
type FungibleAssetBootstrapParams struct {
	Payer           structure.SolanaKeyPair
	Program         structure.PublicKey
	Name            string
	Symbol          string
	Decimals        uint8
	InitialSupply   uint64
	RecentBlockhash structure.Hash
}

// FungibleAssetBootstrapAccounts 描述规范资产状态账户 + CLI、RPC 和 APP 必须使用同一地址约定。
type FungibleAssetBootstrapAccounts struct {
	Mint    structure.SolanaKeyPair
	Balance structure.SolanaKeyPair
}

// DeriveFungibleAssetMintKeyPair 派生规范 mint 账户 + 当前链没有 PDA 因此用稳定 seed 让客户端可复算。
func DeriveFungibleAssetMintKeyPair(program structure.PublicKey) (structure.SolanaKeyPair, error) {
	return deriveAssetStateKeyPair(assetMintDerivationDomain, program.String())
}

// DeriveFungibleAssetBalanceKeyPair 派生规范余额账户 + 每个合约和 owner 对应唯一资产余额状态。
func DeriveFungibleAssetBalanceKeyPair(program structure.PublicKey, owner structure.PublicKey) (structure.SolanaKeyPair, error) {
	return deriveAssetStateKeyPair(assetBalanceDerivationDomain, program.String(), owner.String())
}

// DeriveFungibleAssetBootstrapAccounts 派生启动所需账户 + 避免 CLI 和 APP 生成不同资产状态地址。
func DeriveFungibleAssetBootstrapAccounts(program structure.PublicKey, owner structure.PublicKey) (FungibleAssetBootstrapAccounts, error) {
	mint, err := DeriveFungibleAssetMintKeyPair(program)
	if err != nil {
		return FungibleAssetBootstrapAccounts{}, err
	}
	balance, err := DeriveFungibleAssetBalanceKeyPair(program, owner)
	if err != nil {
		return FungibleAssetBootstrapAccounts{}, err
	}
	return FungibleAssetBootstrapAccounts{Mint: mint, Balance: balance}, nil
}

// NewBootstrapFungibleAssetTransaction 构造 ERC20-like 启动交易 + 部署后必须初始化状态才能被钱包当资产使用。
func NewBootstrapFungibleAssetTransaction(params FungibleAssetBootstrapParams) (structure.Transaction, error) {
	if params.Program.IsZero() {
		return structure.Transaction{}, fmt.Errorf("blockchain: asset program is empty")
	}
	if params.InitialSupply == 0 {
		return structure.Transaction{}, fmt.Errorf("blockchain: initial supply is zero")
	}
	if params.RecentBlockhash == (structure.Hash{}) {
		return structure.Transaction{}, structure.ErrEmptyRecentBlockhash
	}
	assetInstruction, err := vmprogram.NewAssetInitializeFungibleInstruction(params.Decimals, params.Name, params.Symbol)
	if err != nil {
		return structure.Transaction{}, fmt.Errorf("blockchain: build initialize fungible instruction: %w", err)
	}
	mintInstruction, err := vmprogram.NewAssetMintToInstruction(params.InitialSupply)
	if err != nil {
		return structure.Transaction{}, fmt.Errorf("blockchain: build mint_to instruction: %w", err)
	}
	accounts, err := DeriveFungibleAssetBootstrapAccounts(params.Program, params.Payer.PublicKey)
	if err != nil {
		return structure.Transaction{}, err
	}
	return newBootstrapFungibleAssetTransaction(params, accounts, assetInstruction, mintInstruction)
}

func newBootstrapFungibleAssetTransaction(
	params FungibleAssetBootstrapParams,
	assetAccounts FungibleAssetBootstrapAccounts,
	initializeInstruction vmprogram.AssetInstruction,
	mintInstruction vmprogram.AssetInstruction,
) (structure.Transaction, error) {
	createMintData, err := newAssetStateCreateInstructionData(params.Program)
	if err != nil {
		return structure.Transaction{}, fmt.Errorf("blockchain: build mint account: %w", err)
	}
	createBalanceData, err := newAssetStateCreateInstructionData(params.Program)
	if err != nil {
		return structure.Transaction{}, fmt.Errorf("blockchain: build balance account: %w", err)
	}
	initializeData, err := initializeInstruction.MarshalBinary()
	if err != nil {
		return structure.Transaction{}, fmt.Errorf("blockchain: marshal initialize fungible: %w", err)
	}
	initializeAccountData, err := vmprogram.NewAssetInitializeAccountInstruction().MarshalBinary()
	if err != nil {
		return structure.Transaction{}, fmt.Errorf("blockchain: marshal initialize account: %w", err)
	}
	mintData, err := mintInstruction.MarshalBinary()
	if err != nil {
		return structure.Transaction{}, fmt.Errorf("blockchain: marshal mint_to: %w", err)
	}

	transaction := structure.Transaction{
		Accounts: []structure.AccountMeta{
			{PublicKey: params.Payer.PublicKey, IsSigner: true, IsWritable: true},
			{PublicKey: assetAccounts.Mint.PublicKey, IsSigner: true, IsWritable: true},
			{PublicKey: assetAccounts.Balance.PublicKey, IsSigner: true, IsWritable: true},
			{PublicKey: structure.DefaultBuiltinProgramIDs.System, IsSigner: false, IsWritable: false},
			{PublicKey: params.Program, IsSigner: false, IsWritable: false},
		},
		Instructions: []structure.CompiledInstruction{
			{ProgramIDIndex: 3, AccountIndexes: []uint8{0, 1}, Data: createMintData},
			{ProgramIDIndex: 3, AccountIndexes: []uint8{0, 2}, Data: createBalanceData},
			{ProgramIDIndex: 4, AccountIndexes: []uint8{0, 1}, Data: initializeData},
			{ProgramIDIndex: 4, AccountIndexes: []uint8{0, 1, 2}, Data: initializeAccountData},
			{ProgramIDIndex: 4, AccountIndexes: []uint8{0, 1, 2}, Data: mintData},
		},
		RecentBlockhash: params.RecentBlockhash,
		SubmitTime:      time.Now().UnixMilli(),
	}
	return transaction.Sign(map[structure.PublicKey][]byte{
		params.Payer.PublicKey:          params.Payer.PrivateKey,
		assetAccounts.Mint.PublicKey:    assetAccounts.Mint.PrivateKey,
		assetAccounts.Balance.PublicKey: assetAccounts.Balance.PrivateKey,
	})
}

func newAssetStateCreateInstructionData(program structure.PublicKey) ([]byte, error) {
	lamports, err := structure.MinimumBalanceForRentExemption(vmprogram.MaxAssetStateBytes)
	if err != nil {
		return nil, fmt.Errorf("blockchain: calculate asset state rent: %w", err)
	}
	instruction, err := structure.NewCreateAccountInstruction(structure.CreateAccountParams{
		Lamports: lamports,
		Space:    0,
		Owner:    program,
	})
	if err != nil {
		return nil, err
	}
	return instruction.MarshalBinary()
}

func deriveAssetStateKeyPair(domain string, parts ...string) (structure.SolanaKeyPair, error) {
	seedText := domain + "|" + strings.Join(parts, "|")
	keyPair, err := structure.KeyPairFromSeed(utils.SHA256([]byte(seedText)))
	if err != nil {
		return structure.SolanaKeyPair{}, fmt.Errorf("blockchain: derive asset state key: %w", err)
	}
	return keyPair, nil
}
