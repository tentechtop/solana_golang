package vmprogram

import (
	"fmt"

	"solana_golang/codec/borsh"
	"solana_golang/runtime"
	"solana_golang/structure"
	svm "solana_golang/vm"
)

const (
	assetExecuteSyscallCost  = uint64(10_000)
	assetStateVersion        = uint16(1)
	maxAssetInstructionBytes = 768
	MaxAssetStateBytes       = 1024
	maxAssetStateBytes       = MaxAssetStateBytes
	maxAssetNameBytes        = 64
	maxAssetSymbolBytes      = 16
	maxAssetURIBytes         = 256
)

type AssetInstructionType uint32

const (
	AssetInstructionInitializeFungible AssetInstructionType = iota
	AssetInstructionInitializeNFT
	AssetInstructionInitializeAccount
	AssetInstructionMintTo
	AssetInstructionTransfer
	AssetInstructionBurn
	AssetInstructionApprove
	AssetInstructionTransferFrom
)

type AssetKind uint8

const (
	AssetKindFungible AssetKind = iota + 1
	AssetKindNFT
)

type AssetAccountKind uint8

const (
	AssetAccountKindMint AssetAccountKind = iota + 1
	AssetAccountKindBalance
	AssetAccountKindAllowance
)

// AssetInstruction 描述 VM 资产合约 ABI + ERC20-like 和 NFT 共用同一二进制入口。
type AssetInstruction struct {
	Type     AssetInstructionType
	Amount   uint64
	Decimals uint8
	Name     string
	Symbol   string
	URI      string
}

// AssetMintState 保存合约 mint 状态 + 由部署后的 VM 程序账户独占写入。
type AssetMintState struct {
	Version   uint16
	Kind      AssetKind
	Decimals  uint8
	Authority structure.PublicKey
	Supply    uint64
	MaxSupply uint64
	Name      string
	Symbol    string
	URI       string
}

// AssetBalanceState 保存用户资产余额 + owner 字段用于 VM 合约内授权检查。
type AssetBalanceState struct {
	Version uint16
	Mint    structure.PublicKey
	Owner   structure.PublicKey
	Amount  uint64
}

// AssetAllowanceState 保存 ERC20-like 授权额度 + transfer_from 按该账户扣减。
type AssetAllowanceState struct {
	Version  uint16
	Source   structure.PublicKey
	Mint     structure.PublicKey
	Owner    structure.PublicKey
	Delegate structure.PublicKey
	Amount   uint64
}

// NewAssetInitializeFungibleInstruction 创建 ERC20-like 初始化指令 + 元数据写入合约状态账户。
func NewAssetInitializeFungibleInstruction(decimals uint8, name string, symbol string) (AssetInstruction, error) {
	instruction := AssetInstruction{Type: AssetInstructionInitializeFungible, Decimals: decimals, Name: name, Symbol: symbol}
	return instruction, instruction.Validate()
}

// NewAssetInitializeNFTInstruction 创建 NFT 初始化指令 + NFT 固定 decimals=0 且 max supply=1。
func NewAssetInitializeNFTInstruction(name string, symbol string, uri string) (AssetInstruction, error) {
	instruction := AssetInstruction{Type: AssetInstructionInitializeNFT, Name: name, Symbol: symbol, URI: uri}
	return instruction, instruction.Validate()
}

// NewAssetInitializeAccountInstruction 创建余额账户初始化指令 + owner 必须签名。
func NewAssetInitializeAccountInstruction() AssetInstruction {
	return AssetInstruction{Type: AssetInstructionInitializeAccount}
}

// NewAssetMintToInstruction 创建铸币指令 + mint authority 必须签名。
func NewAssetMintToInstruction(amount uint64) (AssetInstruction, error) {
	instruction := AssetInstruction{Type: AssetInstructionMintTo, Amount: amount}
	return instruction, instruction.Validate()
}

// NewAssetTransferInstruction 创建转账指令 + source owner 必须签名。
func NewAssetTransferInstruction(amount uint64) (AssetInstruction, error) {
	instruction := AssetInstruction{Type: AssetInstructionTransfer, Amount: amount}
	return instruction, instruction.Validate()
}

// NewAssetBurnInstruction 创建销毁指令 + source owner 必须签名并扣减 supply。
func NewAssetBurnInstruction(amount uint64) (AssetInstruction, error) {
	instruction := AssetInstruction{Type: AssetInstructionBurn, Amount: amount}
	return instruction, instruction.Validate()
}

// NewAssetApproveInstruction 创建授权指令 + delegate 后续可 transfer_from。
func NewAssetApproveInstruction(amount uint64) (AssetInstruction, error) {
	instruction := AssetInstruction{Type: AssetInstructionApprove, Amount: amount}
	return instruction, instruction.Validate()
}

// NewAssetTransferFromInstruction 创建授权转账指令 + delegate 必须签名。
func NewAssetTransferFromInstruction(amount uint64) (AssetInstruction, error) {
	instruction := AssetInstruction{Type: AssetInstructionTransferFrom, Amount: amount}
	return instruction, instruction.Validate()
}

// Validate 校验资产指令 + 防止畸形元数据和零金额进入合约执行。
func (instruction AssetInstruction) Validate() error {
	if len(instruction.Name) > maxAssetNameBytes {
		return fmt.Errorf("asset: name too long")
	}
	if len(instruction.Symbol) > maxAssetSymbolBytes {
		return fmt.Errorf("asset: symbol too long")
	}
	if len(instruction.URI) > maxAssetURIBytes {
		return fmt.Errorf("asset: uri too long")
	}
	switch instruction.Type {
	case AssetInstructionInitializeFungible:
		if instruction.Symbol == "" {
			return fmt.Errorf("asset: symbol is required")
		}
		return nil
	case AssetInstructionInitializeNFT:
		if instruction.Name == "" || instruction.Symbol == "" || instruction.URI == "" {
			return fmt.Errorf("asset: nft metadata is required")
		}
		return nil
	case AssetInstructionInitializeAccount:
		return nil
	case AssetInstructionMintTo, AssetInstructionTransfer, AssetInstructionBurn, AssetInstructionApprove, AssetInstructionTransferFrom:
		if instruction.Amount == 0 {
			return fmt.Errorf("asset: amount is zero")
		}
		return nil
	default:
		return fmt.Errorf("asset: invalid instruction type %d", instruction.Type)
	}
}

// MarshalBinary 序列化资产指令 + 部署后的合约以该 ABI 读取调用参数。
func (instruction AssetInstruction) MarshalBinary() ([]byte, error) {
	if err := instruction.Validate(); err != nil {
		return nil, err
	}
	writer := borsh.NewWriter(maxAssetInstructionBytes)
	writer.WriteUint32(uint32(instruction.Type))
	writer.WriteUint64(instruction.Amount)
	writer.WriteUint8(instruction.Decimals)
	if err := writer.WriteString(instruction.Name); err != nil {
		return nil, fmt.Errorf("asset: encode name: %w", err)
	}
	if err := writer.WriteString(instruction.Symbol); err != nil {
		return nil, fmt.Errorf("asset: encode symbol: %w", err)
	}
	if err := writer.WriteString(instruction.URI); err != nil {
		return nil, fmt.Errorf("asset: encode uri: %w", err)
	}
	return writer.Bytes(), nil
}

// UnmarshalAssetInstructionBinary 反序列化资产指令 + 解码后继续校验业务边界。
func UnmarshalAssetInstructionBinary(data []byte) (AssetInstruction, error) {
	reader := borsh.NewReader(data, maxAssetInstructionBytes)
	instructionType, err := reader.ReadUint32()
	if err != nil {
		return AssetInstruction{}, fmt.Errorf("asset: decode instruction type: %w", err)
	}
	amount, err := reader.ReadUint64()
	if err != nil {
		return AssetInstruction{}, fmt.Errorf("asset: decode amount: %w", err)
	}
	decimals, err := reader.ReadUint8()
	if err != nil {
		return AssetInstruction{}, fmt.Errorf("asset: decode decimals: %w", err)
	}
	name, err := reader.ReadString()
	if err != nil {
		return AssetInstruction{}, fmt.Errorf("asset: decode name: %w", err)
	}
	symbol, err := reader.ReadString()
	if err != nil {
		return AssetInstruction{}, fmt.Errorf("asset: decode symbol: %w", err)
	}
	uri, err := reader.ReadString()
	if err != nil {
		return AssetInstruction{}, fmt.Errorf("asset: decode uri: %w", err)
	}
	if err := reader.EnsureEOF(); err != nil {
		return AssetInstruction{}, fmt.Errorf("asset: decode eof: %w", err)
	}
	instruction := AssetInstruction{
		Type:     AssetInstructionType(instructionType),
		Amount:   amount,
		Decimals: decimals,
		Name:     name,
		Symbol:   symbol,
		URI:      uri,
	}
	return instruction, instruction.Validate()
}

// MarshalBinary 序列化 mint 状态 + 固定字段顺序保证跨节点确定性。
func (state AssetMintState) MarshalBinary() ([]byte, error) {
	if err := state.Validate(); err != nil {
		return nil, err
	}
	writer := borsh.NewWriter(maxAssetStateBytes)
	writer.WriteUint8(uint8(AssetAccountKindMint))
	writer.WriteUint16(state.Version)
	writer.WriteUint8(uint8(state.Kind))
	writer.WriteUint8(state.Decimals)
	writer.WriteFixedBytes(state.Authority[:])
	writer.WriteUint64(state.Supply)
	writer.WriteUint64(state.MaxSupply)
	if err := writer.WriteString(state.Name); err != nil {
		return nil, fmt.Errorf("asset: encode mint name: %w", err)
	}
	if err := writer.WriteString(state.Symbol); err != nil {
		return nil, fmt.Errorf("asset: encode mint symbol: %w", err)
	}
	if err := writer.WriteString(state.URI); err != nil {
		return nil, fmt.Errorf("asset: encode mint uri: %w", err)
	}
	return writer.Bytes(), nil
}

// Validate 校验 mint 状态 + 防止 NFT 供给和元数据约束被破坏。
func (state AssetMintState) Validate() error {
	if state.Version != assetStateVersion {
		return fmt.Errorf("asset: unsupported mint version %d", state.Version)
	}
	if state.Authority.IsZero() {
		return fmt.Errorf("asset: authority is empty")
	}
	if len(state.Name) > maxAssetNameBytes || len(state.Symbol) > maxAssetSymbolBytes || len(state.URI) > maxAssetURIBytes {
		return fmt.Errorf("asset: metadata too long")
	}
	switch state.Kind {
	case AssetKindFungible:
		return nil
	case AssetKindNFT:
		if state.Decimals != 0 {
			return fmt.Errorf("asset: nft decimals must be zero")
		}
		if state.MaxSupply != 1 || state.Supply > 1 {
			return fmt.Errorf("asset: invalid nft supply")
		}
		if state.Name == "" || state.Symbol == "" || state.URI == "" {
			return fmt.Errorf("asset: nft metadata is incomplete")
		}
		return nil
	default:
		return fmt.Errorf("asset: invalid kind %d", state.Kind)
	}
}

// UnmarshalAssetMintStateBinary 反序列化 mint 状态 + 供合约执行和测试读取。
func UnmarshalAssetMintStateBinary(data []byte) (AssetMintState, error) {
	reader := borsh.NewReader(data, maxAssetStateBytes)
	kind, version, err := readAssetStatePrefix(reader)
	if err != nil {
		return AssetMintState{}, err
	}
	if kind != AssetAccountKindMint {
		return AssetMintState{}, fmt.Errorf("asset: account is not mint")
	}
	assetKind, err := reader.ReadUint8()
	if err != nil {
		return AssetMintState{}, fmt.Errorf("asset: decode kind: %w", err)
	}
	decimals, err := reader.ReadUint8()
	if err != nil {
		return AssetMintState{}, fmt.Errorf("asset: decode decimals: %w", err)
	}
	authority, err := readAssetPublicKey(reader, "authority")
	if err != nil {
		return AssetMintState{}, err
	}
	supply, err := reader.ReadUint64()
	if err != nil {
		return AssetMintState{}, fmt.Errorf("asset: decode supply: %w", err)
	}
	maxSupply, err := reader.ReadUint64()
	if err != nil {
		return AssetMintState{}, fmt.Errorf("asset: decode max supply: %w", err)
	}
	name, err := reader.ReadString()
	if err != nil {
		return AssetMintState{}, fmt.Errorf("asset: decode name: %w", err)
	}
	symbol, err := reader.ReadString()
	if err != nil {
		return AssetMintState{}, fmt.Errorf("asset: decode symbol: %w", err)
	}
	uri, err := reader.ReadString()
	if err != nil {
		return AssetMintState{}, fmt.Errorf("asset: decode uri: %w", err)
	}
	if err := reader.EnsureEOF(); err != nil {
		return AssetMintState{}, fmt.Errorf("asset: decode mint eof: %w", err)
	}
	state := AssetMintState{
		Version:   version,
		Kind:      AssetKind(assetKind),
		Decimals:  decimals,
		Authority: authority,
		Supply:    supply,
		MaxSupply: maxSupply,
		Name:      name,
		Symbol:    symbol,
		URI:       uri,
	}
	return state, state.Validate()
}

// MarshalBinary 序列化余额状态 + owner 作为合约授权来源。
func (state AssetBalanceState) MarshalBinary() ([]byte, error) {
	if err := state.Validate(); err != nil {
		return nil, err
	}
	writer := borsh.NewWriter(maxAssetStateBytes)
	writer.WriteUint8(uint8(AssetAccountKindBalance))
	writer.WriteUint16(state.Version)
	writer.WriteFixedBytes(state.Mint[:])
	writer.WriteFixedBytes(state.Owner[:])
	writer.WriteUint64(state.Amount)
	return writer.Bytes(), nil
}

// Validate 校验余额状态 + 防止空 mint 或空 owner 进入资产账本。
func (state AssetBalanceState) Validate() error {
	if state.Version != assetStateVersion {
		return fmt.Errorf("asset: unsupported balance version %d", state.Version)
	}
	if state.Mint.IsZero() {
		return fmt.Errorf("asset: balance mint is empty")
	}
	if state.Owner.IsZero() {
		return fmt.Errorf("asset: balance owner is empty")
	}
	return nil
}

// UnmarshalAssetBalanceStateBinary 反序列化余额状态 + 用于合约校验余额账户。
func UnmarshalAssetBalanceStateBinary(data []byte) (AssetBalanceState, error) {
	reader := borsh.NewReader(data, maxAssetStateBytes)
	kind, version, err := readAssetStatePrefix(reader)
	if err != nil {
		return AssetBalanceState{}, err
	}
	if kind != AssetAccountKindBalance {
		return AssetBalanceState{}, fmt.Errorf("asset: account is not balance")
	}
	mint, err := readAssetPublicKey(reader, "balance mint")
	if err != nil {
		return AssetBalanceState{}, err
	}
	owner, err := readAssetPublicKey(reader, "balance owner")
	if err != nil {
		return AssetBalanceState{}, err
	}
	amount, err := reader.ReadUint64()
	if err != nil {
		return AssetBalanceState{}, fmt.Errorf("asset: decode balance amount: %w", err)
	}
	if err := reader.EnsureEOF(); err != nil {
		return AssetBalanceState{}, fmt.Errorf("asset: decode balance eof: %w", err)
	}
	state := AssetBalanceState{Version: version, Mint: mint, Owner: owner, Amount: amount}
	return state, state.Validate()
}

// MarshalBinary 序列化授权状态 + transfer_from 扣减该账户额度。
func (state AssetAllowanceState) MarshalBinary() ([]byte, error) {
	if err := state.Validate(); err != nil {
		return nil, err
	}
	writer := borsh.NewWriter(maxAssetStateBytes)
	writer.WriteUint8(uint8(AssetAccountKindAllowance))
	writer.WriteUint16(state.Version)
	writer.WriteFixedBytes(state.Source[:])
	writer.WriteFixedBytes(state.Mint[:])
	writer.WriteFixedBytes(state.Owner[:])
	writer.WriteFixedBytes(state.Delegate[:])
	writer.WriteUint64(state.Amount)
	return writer.Bytes(), nil
}

// Validate 校验授权状态 + 绑定 source、owner 和 delegate 防止混用。
func (state AssetAllowanceState) Validate() error {
	if state.Version != assetStateVersion {
		return fmt.Errorf("asset: unsupported allowance version %d", state.Version)
	}
	if state.Source.IsZero() || state.Mint.IsZero() || state.Owner.IsZero() || state.Delegate.IsZero() {
		return fmt.Errorf("asset: allowance contains empty address")
	}
	return nil
}

// UnmarshalAssetAllowanceStateBinary 反序列化授权状态 + 用于授权转账校验。
func UnmarshalAssetAllowanceStateBinary(data []byte) (AssetAllowanceState, error) {
	reader := borsh.NewReader(data, maxAssetStateBytes)
	kind, version, err := readAssetStatePrefix(reader)
	if err != nil {
		return AssetAllowanceState{}, err
	}
	if kind != AssetAccountKindAllowance {
		return AssetAllowanceState{}, fmt.Errorf("asset: account is not allowance")
	}
	source, err := readAssetPublicKey(reader, "allowance source")
	if err != nil {
		return AssetAllowanceState{}, err
	}
	mint, err := readAssetPublicKey(reader, "allowance mint")
	if err != nil {
		return AssetAllowanceState{}, err
	}
	owner, err := readAssetPublicKey(reader, "allowance owner")
	if err != nil {
		return AssetAllowanceState{}, err
	}
	delegate, err := readAssetPublicKey(reader, "allowance delegate")
	if err != nil {
		return AssetAllowanceState{}, err
	}
	amount, err := reader.ReadUint64()
	if err != nil {
		return AssetAllowanceState{}, fmt.Errorf("asset: decode allowance amount: %w", err)
	}
	if err := reader.EnsureEOF(); err != nil {
		return AssetAllowanceState{}, fmt.Errorf("asset: decode allowance eof: %w", err)
	}
	state := AssetAllowanceState{Version: version, Source: source, Mint: mint, Owner: owner, Delegate: delegate, Amount: amount}
	return state, state.Validate()
}

// ERC20LikeContractProgramData 返回 ERC20-like 合约二进制 + 可由普通用户通过 BPFLoader 部署。
func ERC20LikeContractProgramData() ([]byte, error) {
	return assetContractProgramData()
}

// NFTContractProgramData 返回 NFT 合约二进制 + 资产规则由部署后的 VM 合约入口执行。
func NFTContractProgramData() ([]byte, error) {
	return assetContractProgramData()
}

func assetContractProgramData() ([]byte, error) {
	dispatchInstruction, err := svm.BuildRegisterInstruction(svm.RegOpSyscall, 0, 0, 0, uint64(svm.SyscallAssetExecute))
	if err != nil {
		return nil, err
	}
	code, err := svm.BuildRegisterProgramCode(dispatchInstruction)
	if err != nil {
		return nil, err
	}
	return svm.EncodeGovernedRegisterBytecode(code, nil, svm.ProgramManifest{
		ComputeUnitLimit: svm.DefaultComputeUnitLimit,
		RequiredSyscalls: []svm.SyscallID{svm.SyscallAssetExecute},
	})
}

func attachAssetSyscall(runtimeValue *svm.Runtime, context runtime.InstructionContext) error {
	if runtimeValue == nil {
		return fmt.Errorf("vm program: runtime is nil")
	}
	registry := runtimeValue.Syscalls
	if registry.IsZero() {
		registry = svm.DefaultSyscallRegistry()
	}
	extendedRegistry, err := registry.With(svm.Syscall{
		ID:      svm.SyscallAssetExecute,
		Name:    "asset_execute",
		Cost:    assetExecuteSyscallCost,
		Handler: assetExecuteSyscall(context),
	})
	if err != nil {
		return fmt.Errorf("vm program: attach asset syscall: %w", err)
	}
	runtimeValue.Syscalls = extendedRegistry
	return nil
}

func assetExecuteSyscall(outerContext runtime.InstructionContext) svm.SyscallFunc {
	return func(vmContext *svm.Context, input []byte) ([]byte, error) {
		if vmContext == nil || vmContext.Accounts == nil {
			return nil, fmt.Errorf("asset syscall: vm context is nil")
		}
		if len(input) != 0 {
			return nil, fmt.Errorf("asset syscall: input must be empty")
		}
		instruction, err := UnmarshalAssetInstructionBinary(outerContext.Instruction.Data)
		if err != nil {
			return nil, err
		}
		workingAccounts := cloneStructureAccounts(outerContext.Accounts)
		if err := executeAssetInstruction(instruction, outerContext, workingAccounts); err != nil {
			return nil, err
		}
		if err := commitAssetSyscallAccounts(vmContext, outerContext, workingAccounts); err != nil {
			return nil, err
		}
		return nil, nil
	}
}

func executeAssetInstruction(instruction AssetInstruction, context runtime.InstructionContext, accounts map[structure.PublicKey]structure.Account) error {
	switch instruction.Type {
	case AssetInstructionInitializeFungible:
		return executeAssetInitializeMint(instruction, AssetKindFungible, context, accounts)
	case AssetInstructionInitializeNFT:
		return executeAssetInitializeMint(instruction, AssetKindNFT, context, accounts)
	case AssetInstructionInitializeAccount:
		return executeAssetInitializeAccount(context, accounts)
	case AssetInstructionMintTo:
		return executeAssetMintTo(instruction, context, accounts)
	case AssetInstructionTransfer:
		return executeAssetTransfer(instruction, context, accounts)
	case AssetInstructionBurn:
		return executeAssetBurn(instruction, context, accounts)
	case AssetInstructionApprove:
		return executeAssetApprove(instruction, context, accounts)
	case AssetInstructionTransferFrom:
		return executeAssetTransferFrom(instruction, context, accounts)
	default:
		return fmt.Errorf("asset: unsupported instruction type %d", instruction.Type)
	}
}

func executeAssetInitializeMint(instruction AssetInstruction, kind AssetKind, context runtime.InstructionContext, accounts map[structure.PublicKey]structure.Account) error {
	if len(context.Instruction.AccountIndexes) < 2 {
		return fmt.Errorf("asset: initialize mint requires authority and mint")
	}
	authorityAddress, err := assetAccountAddressAt(context, 0)
	if err != nil {
		return err
	}
	mintAddress, err := assetAccountAddressAt(context, 1)
	if err != nil {
		return err
	}
	if err := requireTransactionSigner(authorityAddress, context, "asset authority"); err != nil {
		return err
	}
	mintAccount, err := assetOwnedWritableAccount("mint", mintAddress, context, accounts)
	if err != nil {
		return err
	}
	if len(mintAccount.Data) != 0 {
		return fmt.Errorf("asset: mint already initialized")
	}
	state := AssetMintState{
		Version:   assetStateVersion,
		Kind:      kind,
		Decimals:  instruction.Decimals,
		Authority: authorityAddress,
		Name:      instruction.Name,
		Symbol:    instruction.Symbol,
		URI:       instruction.URI,
	}
	if kind == AssetKindNFT {
		state.Decimals = 0
		state.MaxSupply = 1
	}
	return writeAssetAccount(mintAddress, mintAccount, state.MarshalBinary, context, accounts)
}

func executeAssetInitializeAccount(context runtime.InstructionContext, accounts map[structure.PublicKey]structure.Account) error {
	if len(context.Instruction.AccountIndexes) < 3 {
		return fmt.Errorf("asset: initialize account requires owner, mint and balance")
	}
	ownerAddress, err := assetAccountAddressAt(context, 0)
	if err != nil {
		return err
	}
	mintAddress, err := assetAccountAddressAt(context, 1)
	if err != nil {
		return err
	}
	balanceAddress, err := assetAccountAddressAt(context, 2)
	if err != nil {
		return err
	}
	if err := requireTransactionSigner(ownerAddress, context, "asset owner"); err != nil {
		return err
	}
	if _, err := readAssetMintState(mintAddress, context, accounts); err != nil {
		return err
	}
	balanceAccount, err := assetOwnedWritableAccount("balance", balanceAddress, context, accounts)
	if err != nil {
		return err
	}
	if len(balanceAccount.Data) != 0 {
		return fmt.Errorf("asset: balance already initialized")
	}
	state := AssetBalanceState{Version: assetStateVersion, Mint: mintAddress, Owner: ownerAddress}
	return writeAssetAccount(balanceAddress, balanceAccount, state.MarshalBinary, context, accounts)
}

func executeAssetMintTo(instruction AssetInstruction, context runtime.InstructionContext, accounts map[structure.PublicKey]structure.Account) error {
	if len(context.Instruction.AccountIndexes) < 3 {
		return fmt.Errorf("asset: mint_to requires authority, mint and destination")
	}
	authorityAddress, mintAddress, destinationAddress, err := assetThreeAccounts(context)
	if err != nil {
		return err
	}
	if err := requireTransactionSigner(authorityAddress, context, "asset authority"); err != nil {
		return err
	}
	mintState, mintAccount, err := readWritableAssetMintState(mintAddress, context, accounts)
	if err != nil {
		return err
	}
	if mintState.Authority != authorityAddress {
		return fmt.Errorf("asset: authority mismatch")
	}
	destinationState, destinationAccount, err := readWritableAssetBalanceState(destinationAddress, context, accounts)
	if err != nil {
		return err
	}
	if destinationState.Mint != mintAddress {
		return fmt.Errorf("asset: destination mint mismatch")
	}
	if err := validateAssetMintAmount(mintState, destinationState, instruction.Amount); err != nil {
		return err
	}
	mintState.Supply += instruction.Amount
	destinationState.Amount += instruction.Amount
	if err := writeAssetAccount(mintAddress, mintAccount, mintState.MarshalBinary, context, accounts); err != nil {
		return err
	}
	return writeAssetAccount(destinationAddress, destinationAccount, destinationState.MarshalBinary, context, accounts)
}

func executeAssetTransfer(instruction AssetInstruction, context runtime.InstructionContext, accounts map[structure.PublicKey]structure.Account) error {
	if len(context.Instruction.AccountIndexes) < 4 {
		return fmt.Errorf("asset: transfer requires owner, mint, source and destination")
	}
	ownerAddress, err := assetAccountAddressAt(context, 0)
	if err != nil {
		return err
	}
	mintAddress, err := assetAccountAddressAt(context, 1)
	if err != nil {
		return err
	}
	sourceAddress, err := assetAccountAddressAt(context, 2)
	if err != nil {
		return err
	}
	destinationAddress, err := assetAccountAddressAt(context, 3)
	if err != nil {
		return err
	}
	if err := requireTransactionSigner(ownerAddress, context, "asset owner"); err != nil {
		return err
	}
	sourceState, sourceAccount, destinationState, destinationAccount, mintState, err := readTransferStates(ownerAddress, mintAddress, sourceAddress, destinationAddress, context, accounts)
	if err != nil {
		return err
	}
	if err := applyAssetTransfer(instruction.Amount, mintState, &sourceState, &destinationState); err != nil {
		return err
	}
	if err := writeAssetAccount(sourceAddress, sourceAccount, sourceState.MarshalBinary, context, accounts); err != nil {
		return err
	}
	return writeAssetAccount(destinationAddress, destinationAccount, destinationState.MarshalBinary, context, accounts)
}

func executeAssetBurn(instruction AssetInstruction, context runtime.InstructionContext, accounts map[structure.PublicKey]structure.Account) error {
	if len(context.Instruction.AccountIndexes) < 3 {
		return fmt.Errorf("asset: burn requires owner, mint and source")
	}
	ownerAddress, mintAddress, sourceAddress, err := assetThreeAccounts(context)
	if err != nil {
		return err
	}
	if err := requireTransactionSigner(ownerAddress, context, "asset owner"); err != nil {
		return err
	}
	mintState, mintAccount, err := readWritableAssetMintState(mintAddress, context, accounts)
	if err != nil {
		return err
	}
	sourceState, sourceAccount, err := readWritableAssetBalanceState(sourceAddress, context, accounts)
	if err != nil {
		return err
	}
	if sourceState.Owner != ownerAddress || sourceState.Mint != mintAddress {
		return fmt.Errorf("asset: burn source mismatch")
	}
	if mintState.Kind == AssetKindNFT && instruction.Amount != 1 {
		return fmt.Errorf("asset: nft amount must be one")
	}
	if sourceState.Amount < instruction.Amount || mintState.Supply < instruction.Amount {
		return fmt.Errorf("asset: insufficient balance")
	}
	sourceState.Amount -= instruction.Amount
	mintState.Supply -= instruction.Amount
	if err := writeAssetAccount(mintAddress, mintAccount, mintState.MarshalBinary, context, accounts); err != nil {
		return err
	}
	return writeAssetAccount(sourceAddress, sourceAccount, sourceState.MarshalBinary, context, accounts)
}

func executeAssetApprove(instruction AssetInstruction, context runtime.InstructionContext, accounts map[structure.PublicKey]structure.Account) error {
	if len(context.Instruction.AccountIndexes) < 5 {
		return fmt.Errorf("asset: approve requires owner, mint, source, delegate and allowance")
	}
	ownerAddress, err := assetAccountAddressAt(context, 0)
	if err != nil {
		return err
	}
	mintAddress, err := assetAccountAddressAt(context, 1)
	if err != nil {
		return err
	}
	sourceAddress, err := assetAccountAddressAt(context, 2)
	if err != nil {
		return err
	}
	delegateAddress, err := assetAccountAddressAt(context, 3)
	if err != nil {
		return err
	}
	allowanceAddress, err := assetAccountAddressAt(context, 4)
	if err != nil {
		return err
	}
	if err := requireTransactionSigner(ownerAddress, context, "asset owner"); err != nil {
		return err
	}
	sourceState, err := readAssetBalanceState(sourceAddress, context, accounts)
	if err != nil {
		return err
	}
	if sourceState.Owner != ownerAddress || sourceState.Mint != mintAddress {
		return fmt.Errorf("asset: source owner mismatch")
	}
	if _, err := readAssetMintState(mintAddress, context, accounts); err != nil {
		return err
	}
	allowanceAccount, err := assetOwnedWritableAccount("allowance", allowanceAddress, context, accounts)
	if err != nil {
		return err
	}
	if err := validateAssetAllowanceReuse(allowanceAccount, sourceAddress, mintAddress, ownerAddress, delegateAddress); err != nil {
		return err
	}
	state := AssetAllowanceState{
		Version:  assetStateVersion,
		Source:   sourceAddress,
		Mint:     mintAddress,
		Owner:    ownerAddress,
		Delegate: delegateAddress,
		Amount:   instruction.Amount,
	}
	return writeAssetAccount(allowanceAddress, allowanceAccount, state.MarshalBinary, context, accounts)
}

func executeAssetTransferFrom(instruction AssetInstruction, context runtime.InstructionContext, accounts map[structure.PublicKey]structure.Account) error {
	if len(context.Instruction.AccountIndexes) < 5 {
		return fmt.Errorf("asset: transfer_from requires delegate, mint, source, destination and allowance")
	}
	delegateAddress, err := assetAccountAddressAt(context, 0)
	if err != nil {
		return err
	}
	mintAddress, err := assetAccountAddressAt(context, 1)
	if err != nil {
		return err
	}
	sourceAddress, err := assetAccountAddressAt(context, 2)
	if err != nil {
		return err
	}
	destinationAddress, err := assetAccountAddressAt(context, 3)
	if err != nil {
		return err
	}
	allowanceAddress, err := assetAccountAddressAt(context, 4)
	if err != nil {
		return err
	}
	if err := requireTransactionSigner(delegateAddress, context, "asset delegate"); err != nil {
		return err
	}
	allowanceState, allowanceAccount, err := readWritableAssetAllowanceState(allowanceAddress, context, accounts)
	if err != nil {
		return err
	}
	if allowanceState.Source != sourceAddress || allowanceState.Delegate != delegateAddress {
		return fmt.Errorf("asset: allowance mismatch")
	}
	if allowanceState.Mint != mintAddress {
		return fmt.Errorf("asset: allowance mint mismatch")
	}
	sourceState, sourceAccount, destinationState, destinationAccount, mintState, err := readTransferStates(allowanceState.Owner, mintAddress, sourceAddress, destinationAddress, context, accounts)
	if err != nil {
		return err
	}
	if allowanceState.Amount < instruction.Amount {
		return fmt.Errorf("asset: insufficient allowance")
	}
	if err := applyAssetTransfer(instruction.Amount, mintState, &sourceState, &destinationState); err != nil {
		return err
	}
	allowanceState.Amount -= instruction.Amount
	if err := writeAssetAccount(sourceAddress, sourceAccount, sourceState.MarshalBinary, context, accounts); err != nil {
		return err
	}
	if err := writeAssetAccount(destinationAddress, destinationAccount, destinationState.MarshalBinary, context, accounts); err != nil {
		return err
	}
	return writeAssetAccount(allowanceAddress, allowanceAccount, allowanceState.MarshalBinary, context, accounts)
}

func readTransferStates(ownerAddress structure.PublicKey, mintAddress structure.PublicKey, sourceAddress structure.PublicKey, destinationAddress structure.PublicKey, context runtime.InstructionContext, accounts map[structure.PublicKey]structure.Account) (AssetBalanceState, structure.Account, AssetBalanceState, structure.Account, AssetMintState, error) {
	sourceState, sourceAccount, err := readWritableAssetBalanceState(sourceAddress, context, accounts)
	if err != nil {
		return AssetBalanceState{}, structure.Account{}, AssetBalanceState{}, structure.Account{}, AssetMintState{}, err
	}
	if sourceState.Owner != ownerAddress {
		return AssetBalanceState{}, structure.Account{}, AssetBalanceState{}, structure.Account{}, AssetMintState{}, fmt.Errorf("asset: source owner mismatch")
	}
	destinationState, destinationAccount, err := readWritableAssetBalanceState(destinationAddress, context, accounts)
	if err != nil {
		return AssetBalanceState{}, structure.Account{}, AssetBalanceState{}, structure.Account{}, AssetMintState{}, err
	}
	if sourceState.Mint != mintAddress || destinationState.Mint != mintAddress {
		return AssetBalanceState{}, structure.Account{}, AssetBalanceState{}, structure.Account{}, AssetMintState{}, fmt.Errorf("asset: mint mismatch")
	}
	mintState, err := readAssetMintState(mintAddress, context, accounts)
	if err != nil {
		return AssetBalanceState{}, structure.Account{}, AssetBalanceState{}, structure.Account{}, AssetMintState{}, err
	}
	return sourceState, sourceAccount, destinationState, destinationAccount, mintState, nil
}

func applyAssetTransfer(amount uint64, mintState AssetMintState, sourceState *AssetBalanceState, destinationState *AssetBalanceState) error {
	if sourceState.Amount < amount {
		return fmt.Errorf("asset: insufficient balance")
	}
	if mintState.Kind == AssetKindNFT {
		if amount != 1 {
			return fmt.Errorf("asset: nft amount must be one")
		}
		if destinationState.Amount != 0 {
			return fmt.Errorf("asset: nft destination already owns token")
		}
	}
	if ^uint64(0)-destinationState.Amount < amount {
		return fmt.Errorf("asset: amount overflow")
	}
	sourceState.Amount -= amount
	destinationState.Amount += amount
	return nil
}

func validateAssetMintAmount(mintState AssetMintState, destinationState AssetBalanceState, amount uint64) error {
	if mintState.Kind == AssetKindNFT {
		if amount != 1 {
			return fmt.Errorf("asset: nft amount must be one")
		}
		if destinationState.Amount != 0 {
			return fmt.Errorf("asset: nft destination already owns token")
		}
	}
	if mintState.MaxSupply > 0 {
		if mintState.Supply > mintState.MaxSupply || amount > mintState.MaxSupply-mintState.Supply {
			return fmt.Errorf("asset: max supply exceeded")
		}
	}
	if ^uint64(0)-mintState.Supply < amount {
		return fmt.Errorf("asset: supply overflow")
	}
	if ^uint64(0)-destinationState.Amount < amount {
		return fmt.Errorf("asset: amount overflow")
	}
	return nil
}

func validateAssetAllowanceReuse(account structure.Account, sourceAddress structure.PublicKey, mintAddress structure.PublicKey, ownerAddress structure.PublicKey, delegateAddress structure.PublicKey) error {
	if len(account.Data) == 0 {
		return nil
	}
	state, err := UnmarshalAssetAllowanceStateBinary(account.Data)
	if err != nil {
		return err
	}
	if state.Source != sourceAddress || state.Mint != mintAddress || state.Owner != ownerAddress || state.Delegate != delegateAddress {
		return fmt.Errorf("asset: allowance account binding mismatch")
	}
	return nil
}

func readAssetMintState(address structure.PublicKey, context runtime.InstructionContext, accounts map[structure.PublicKey]structure.Account) (AssetMintState, error) {
	account, err := assetOwnedAccount("mint", address, context, accounts)
	if err != nil {
		return AssetMintState{}, err
	}
	return UnmarshalAssetMintStateBinary(account.Data)
}

func readWritableAssetMintState(address structure.PublicKey, context runtime.InstructionContext, accounts map[structure.PublicKey]structure.Account) (AssetMintState, structure.Account, error) {
	account, err := assetOwnedWritableAccount("mint", address, context, accounts)
	if err != nil {
		return AssetMintState{}, structure.Account{}, err
	}
	state, err := UnmarshalAssetMintStateBinary(account.Data)
	return state, account, err
}

func readAssetBalanceState(address structure.PublicKey, context runtime.InstructionContext, accounts map[structure.PublicKey]structure.Account) (AssetBalanceState, error) {
	account, err := assetOwnedAccount("balance", address, context, accounts)
	if err != nil {
		return AssetBalanceState{}, err
	}
	return UnmarshalAssetBalanceStateBinary(account.Data)
}

func readWritableAssetBalanceState(address structure.PublicKey, context runtime.InstructionContext, accounts map[structure.PublicKey]structure.Account) (AssetBalanceState, structure.Account, error) {
	account, err := assetOwnedWritableAccount("balance", address, context, accounts)
	if err != nil {
		return AssetBalanceState{}, structure.Account{}, err
	}
	state, err := UnmarshalAssetBalanceStateBinary(account.Data)
	return state, account, err
}

func readWritableAssetAllowanceState(address structure.PublicKey, context runtime.InstructionContext, accounts map[structure.PublicKey]structure.Account) (AssetAllowanceState, structure.Account, error) {
	account, err := assetOwnedWritableAccount("allowance", address, context, accounts)
	if err != nil {
		return AssetAllowanceState{}, structure.Account{}, err
	}
	state, err := UnmarshalAssetAllowanceStateBinary(account.Data)
	return state, account, err
}

func assetOwnedAccount(name string, address structure.PublicKey, context runtime.InstructionContext, accounts map[structure.PublicKey]structure.Account) (structure.Account, error) {
	account, exists := accounts[address]
	if !exists {
		return structure.Account{}, fmt.Errorf("%w: %s account not found", structure.ErrInvalidLoadedTransaction, name)
	}
	if account.Executable {
		return structure.Account{}, fmt.Errorf("%w: %s account cannot be executable", structure.ErrInvalidInstruction, name)
	}
	programID, err := assetVMProgramID(context)
	if err != nil {
		return structure.Account{}, err
	}
	if account.Owner != programID {
		return structure.Account{}, fmt.Errorf("%w: %s account owner must be asset contract", structure.ErrInvalidInstruction, name)
	}
	return account.Clone(), nil
}

func assetOwnedWritableAccount(name string, address structure.PublicKey, context runtime.InstructionContext, accounts map[structure.PublicKey]structure.Account) (structure.Account, error) {
	account, err := assetOwnedAccount(name, address, context, accounts)
	if err != nil {
		return structure.Account{}, err
	}
	if err := requireAssetWritableAddress(context, address, name); err != nil {
		return structure.Account{}, err
	}
	return account, nil
}

func writeAssetAccount(address structure.PublicKey, account structure.Account, marshal func() ([]byte, error), context runtime.InstructionContext, accounts map[structure.PublicKey]structure.Account) error {
	data, err := marshal()
	if err != nil {
		return err
	}
	if err := account.SetData(data, context.RentConfig); err != nil {
		return fmt.Errorf("asset: write account data: %w", err)
	}
	accounts[address] = account
	return nil
}

func commitAssetSyscallAccounts(vmContext *svm.Context, outerContext runtime.InstructionContext, workingAccounts map[structure.PublicKey]structure.Account) error {
	vmAccounts := make([]svm.Account, len(outerContext.Instruction.AccountIndexes))
	for accountIndex, messageAccountIndex := range outerContext.Instruction.AccountIndexes {
		account, err := buildInstructionAccount(runtime.InstructionContext{Message: outerContext.Message, Accounts: workingAccounts}, int(messageAccountIndex))
		if err != nil {
			return fmt.Errorf("asset syscall: build vm account %d: %w", accountIndex, err)
		}
		vmAccounts[accountIndex] = account
	}
	for accountIndex, vmAccount := range vmAccounts {
		if err := vmContext.Accounts.SetAccount(accountIndex, vmAccount); err != nil {
			return fmt.Errorf("asset syscall: sync vm account %d: %w", accountIndex, err)
		}
	}
	for _, messageAccountIndex := range outerContext.Instruction.AccountIndexes {
		address := outerContext.Message.AccountKeys[messageAccountIndex]
		outerContext.Accounts[address] = workingAccounts[address].Clone()
	}
	return nil
}

func assetThreeAccounts(context runtime.InstructionContext) (structure.PublicKey, structure.PublicKey, structure.PublicKey, error) {
	first, err := assetAccountAddressAt(context, 0)
	if err != nil {
		return structure.PublicKey{}, structure.PublicKey{}, structure.PublicKey{}, err
	}
	second, err := assetAccountAddressAt(context, 1)
	if err != nil {
		return structure.PublicKey{}, structure.PublicKey{}, structure.PublicKey{}, err
	}
	third, err := assetAccountAddressAt(context, 2)
	if err != nil {
		return structure.PublicKey{}, structure.PublicKey{}, structure.PublicKey{}, err
	}
	return first, second, third, nil
}

func assetAccountAddressAt(context runtime.InstructionContext, instructionAccountIndex int) (structure.PublicKey, error) {
	if instructionAccountIndex < 0 || instructionAccountIndex >= len(context.Instruction.AccountIndexes) {
		return structure.PublicKey{}, fmt.Errorf("%w: asset account index out of range", structure.ErrInvalidInstruction)
	}
	messageAccountIndex := int(context.Instruction.AccountIndexes[instructionAccountIndex])
	if messageAccountIndex < 0 || messageAccountIndex >= len(context.Message.AccountKeys) {
		return structure.PublicKey{}, fmt.Errorf("%w: asset message account index out of range", structure.ErrInvalidInstruction)
	}
	return context.Message.AccountKeys[messageAccountIndex], nil
}

func requireAssetWritableAddress(context runtime.InstructionContext, address structure.PublicKey, name string) error {
	for _, accountIndex := range context.Instruction.AccountIndexes {
		if context.Message.AccountKeys[accountIndex] != address {
			continue
		}
		if runtime.IsWritableMessageAccount(int(accountIndex), context.Message) {
			return nil
		}
	}
	return fmt.Errorf("%w: %s account must be writable", structure.ErrInvalidInstruction, name)
}

func assetVMProgramID(context runtime.InstructionContext) (structure.PublicKey, error) {
	if int(context.Instruction.ProgramIDIndex) >= len(context.Message.AccountKeys) {
		return structure.PublicKey{}, fmt.Errorf("%w: asset program id index out of range", structure.ErrInvalidInstruction)
	}
	return context.Message.AccountKeys[context.Instruction.ProgramIDIndex], nil
}

func readAssetStatePrefix(reader *borsh.Reader) (AssetAccountKind, uint16, error) {
	accountKind, err := reader.ReadUint8()
	if err != nil {
		return 0, 0, fmt.Errorf("asset: decode account kind: %w", err)
	}
	version, err := reader.ReadUint16()
	if err != nil {
		return 0, 0, fmt.Errorf("asset: decode version: %w", err)
	}
	if version != assetStateVersion {
		return 0, 0, fmt.Errorf("asset: unsupported state version %d", version)
	}
	return AssetAccountKind(accountKind), version, nil
}

func readAssetPublicKey(reader *borsh.Reader, field string) (structure.PublicKey, error) {
	value, err := reader.ReadFixedBytes(structure.PublicKeySize)
	if err != nil {
		return structure.PublicKey{}, fmt.Errorf("asset: decode %s: %w", field, err)
	}
	publicKey, err := structure.NewPublicKey(value)
	if err != nil {
		return structure.PublicKey{}, fmt.Errorf("asset: decode %s: %w", field, err)
	}
	return publicKey, nil
}
