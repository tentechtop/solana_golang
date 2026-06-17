package token

import (
	"fmt"

	"solana_golang/codec/borsh"
	"solana_golang/runtime"
	"solana_golang/structure"
)

const (
	MaxTokenStateBytes = 256
	TokenStateVersion  = uint8(1)
)

type InstructionType uint32

const (
	InstructionInitializeMint InstructionType = iota
	InstructionInitializeAccount
	InstructionMintTo
	InstructionTransfer
)

type AccountKind uint8

const (
	AccountKindMint AccountKind = iota + 1
	AccountKindTokenAccount
)

type Program struct {
	programID structure.PublicKey
}

type Instruction struct {
	Type     InstructionType
	Decimals uint8
	Amount   uint64
}

type MintState struct {
	Version       uint8
	Decimals      uint8
	MintAuthority structure.PublicKey
	Supply        uint64
}

type AccountState struct {
	Version uint8
	Mint    structure.PublicKey
	Owner   structure.PublicKey
	Amount  uint64
}

func NewProgram(programID structure.PublicKey) Program {
	return Program{programID: programID}
}

func (program Program) ProgramID() structure.PublicKey {
	return program.programID
}

func (program Program) Execute(context runtime.InstructionContext) error {
	instruction, err := UnmarshalInstructionBinary(context.Instruction.Data)
	if err != nil {
		return err
	}
	switch instruction.Type {
	case InstructionInitializeMint:
		return program.executeInitializeMint(instruction, context)
	case InstructionInitializeAccount:
		return program.executeInitializeAccount(context)
	case InstructionMintTo:
		return program.executeMintTo(instruction, context)
	case InstructionTransfer:
		return program.executeTransfer(instruction, context)
	default:
		return fmt.Errorf("token: unsupported instruction type %d", instruction.Type)
	}
}

func NewInitializeMintInstruction(decimals uint8) Instruction {
	return Instruction{Type: InstructionInitializeMint, Decimals: decimals}
}

func NewInitializeAccountInstruction() Instruction {
	return Instruction{Type: InstructionInitializeAccount}
}

func NewMintToInstruction(amount uint64) (Instruction, error) {
	instruction := Instruction{Type: InstructionMintTo, Amount: amount}
	return instruction, instruction.Validate()
}

func NewTransferInstruction(amount uint64) (Instruction, error) {
	instruction := Instruction{Type: InstructionTransfer, Amount: amount}
	return instruction, instruction.Validate()
}

func (instruction Instruction) Validate() error {
	switch instruction.Type {
	case InstructionInitializeMint, InstructionInitializeAccount:
		return nil
	case InstructionMintTo, InstructionTransfer:
		if instruction.Amount == 0 {
			return fmt.Errorf("token: amount is zero")
		}
		return nil
	default:
		return fmt.Errorf("token: invalid instruction type %d", instruction.Type)
	}
}

func (instruction Instruction) MarshalBinary() ([]byte, error) {
	if err := instruction.Validate(); err != nil {
		return nil, err
	}
	writer := borsh.NewWriter(MaxTokenStateBytes)
	writer.WriteUint32(uint32(instruction.Type))
	writer.WriteUint8(instruction.Decimals)
	writer.WriteUint64(instruction.Amount)
	return writer.Bytes(), nil
}

func UnmarshalInstructionBinary(data []byte) (Instruction, error) {
	reader := borsh.NewReader(data, MaxTokenStateBytes)
	instructionType, err := reader.ReadUint32()
	if err != nil {
		return Instruction{}, fmt.Errorf("token: decode instruction type: %w", err)
	}
	decimals, err := reader.ReadUint8()
	if err != nil {
		return Instruction{}, fmt.Errorf("token: decode decimals: %w", err)
	}
	amount, err := reader.ReadUint64()
	if err != nil {
		return Instruction{}, fmt.Errorf("token: decode amount: %w", err)
	}
	if err := reader.EnsureEOF(); err != nil {
		return Instruction{}, fmt.Errorf("token: decode instruction eof: %w", err)
	}
	instruction := Instruction{Type: InstructionType(instructionType), Decimals: decimals, Amount: amount}
	return instruction, instruction.Validate()
}

func (program Program) executeInitializeMint(instruction Instruction, context runtime.InstructionContext) error {
	if len(context.Instruction.AccountIndexes) < 2 {
		return fmt.Errorf("token: initialize mint requires mint and authority")
	}
	mintAddress := accountAddressAt(context, 0)
	authorityAddress := accountAddressAt(context, 1)
	if err := requireWritable(context, mintAddress); err != nil {
		return err
	}
	if !runtime.IsSignerAddress(authorityAddress, context.Message) {
		return fmt.Errorf("%w: mint authority must sign", structure.ErrMissingRequiredSignature)
	}
	mintAccount, err := tokenOwnedAccount(context, mintAddress, program.programID)
	if err != nil {
		return err
	}
	if len(mintAccount.Data) != 0 {
		return fmt.Errorf("token: mint account is already initialized")
	}
	state := MintState{
		Version:       TokenStateVersion,
		Decimals:      instruction.Decimals,
		MintAuthority: authorityAddress,
	}
	return writeAccountData(context, mintAddress, mintAccount, state.MarshalBinary)
}

func (program Program) executeInitializeAccount(context runtime.InstructionContext) error {
	if len(context.Instruction.AccountIndexes) < 3 {
		return fmt.Errorf("token: initialize account requires account, mint and owner")
	}
	accountAddress := accountAddressAt(context, 0)
	mintAddress := accountAddressAt(context, 1)
	ownerAddress := accountAddressAt(context, 2)
	if err := requireWritable(context, accountAddress); err != nil {
		return err
	}
	if _, err := readMintState(context, mintAddress, program.programID); err != nil {
		return err
	}
	tokenAccount, err := tokenOwnedAccount(context, accountAddress, program.programID)
	if err != nil {
		return err
	}
	if len(tokenAccount.Data) != 0 {
		return fmt.Errorf("token: token account is already initialized")
	}
	state := AccountState{
		Version: TokenStateVersion,
		Mint:    mintAddress,
		Owner:   ownerAddress,
	}
	return writeAccountData(context, accountAddress, tokenAccount, state.MarshalBinary)
}

func (program Program) executeMintTo(instruction Instruction, context runtime.InstructionContext) error {
	if len(context.Instruction.AccountIndexes) < 3 {
		return fmt.Errorf("token: mint_to requires mint, destination and authority")
	}
	mintAddress := accountAddressAt(context, 0)
	destinationAddress := accountAddressAt(context, 1)
	authorityAddress := accountAddressAt(context, 2)
	if err := requireWritable(context, mintAddress); err != nil {
		return err
	}
	if err := requireWritable(context, destinationAddress); err != nil {
		return err
	}
	if !runtime.IsSignerAddress(authorityAddress, context.Message) {
		return fmt.Errorf("%w: mint authority must sign", structure.ErrMissingRequiredSignature)
	}
	mintState, err := readMintState(context, mintAddress, program.programID)
	if err != nil {
		return err
	}
	if mintState.MintAuthority != authorityAddress {
		return fmt.Errorf("token: mint authority mismatch")
	}
	destinationState, err := readAccountState(context, destinationAddress, program.programID)
	if err != nil {
		return err
	}
	if destinationState.Mint != mintAddress {
		return fmt.Errorf("token: destination mint mismatch")
	}
	if ^uint64(0)-mintState.Supply < instruction.Amount || ^uint64(0)-destinationState.Amount < instruction.Amount {
		return fmt.Errorf("token: amount overflow")
	}
	mintState.Supply += instruction.Amount
	destinationState.Amount += instruction.Amount
	if err := writeAccountData(context, mintAddress, context.Accounts[mintAddress], mintState.MarshalBinary); err != nil {
		return err
	}
	return writeAccountData(context, destinationAddress, context.Accounts[destinationAddress], destinationState.MarshalBinary)
}

func (program Program) executeTransfer(instruction Instruction, context runtime.InstructionContext) error {
	if len(context.Instruction.AccountIndexes) < 3 {
		return fmt.Errorf("token: transfer requires source, destination and owner")
	}
	sourceAddress := accountAddressAt(context, 0)
	destinationAddress := accountAddressAt(context, 1)
	ownerAddress := accountAddressAt(context, 2)
	if err := requireWritable(context, sourceAddress); err != nil {
		return err
	}
	if err := requireWritable(context, destinationAddress); err != nil {
		return err
	}
	if !runtime.IsSignerAddress(ownerAddress, context.Message) {
		return fmt.Errorf("%w: token owner must sign", structure.ErrMissingRequiredSignature)
	}
	sourceState, err := readAccountState(context, sourceAddress, program.programID)
	if err != nil {
		return err
	}
	if sourceState.Owner != ownerAddress {
		return fmt.Errorf("token: source owner mismatch")
	}
	destinationState, err := readAccountState(context, destinationAddress, program.programID)
	if err != nil {
		return err
	}
	if sourceState.Mint != destinationState.Mint {
		return fmt.Errorf("token: mint mismatch")
	}
	if sourceState.Amount < instruction.Amount {
		return fmt.Errorf("token: insufficient token balance")
	}
	if ^uint64(0)-destinationState.Amount < instruction.Amount {
		return fmt.Errorf("token: amount overflow")
	}
	sourceState.Amount -= instruction.Amount
	destinationState.Amount += instruction.Amount
	if err := writeAccountData(context, sourceAddress, context.Accounts[sourceAddress], sourceState.MarshalBinary); err != nil {
		return err
	}
	return writeAccountData(context, destinationAddress, context.Accounts[destinationAddress], destinationState.MarshalBinary)
}

func (state MintState) MarshalBinary() ([]byte, error) {
	writer := borsh.NewWriter(MaxTokenStateBytes)
	writer.WriteUint8(uint8(AccountKindMint))
	writer.WriteUint8(state.Version)
	writer.WriteUint8(state.Decimals)
	writer.WriteFixedBytes(state.MintAuthority[:])
	writer.WriteUint64(state.Supply)
	return writer.Bytes(), nil
}

func UnmarshalMintStateBinary(data []byte) (MintState, error) {
	reader := borsh.NewReader(data, MaxTokenStateBytes)
	kind, version, err := readStatePrefix(reader)
	if err != nil {
		return MintState{}, err
	}
	if kind != AccountKindMint {
		return MintState{}, fmt.Errorf("token: account is not mint")
	}
	decimals, err := reader.ReadUint8()
	if err != nil {
		return MintState{}, fmt.Errorf("token: decode mint decimals: %w", err)
	}
	authority, err := readPublicKey(reader, "mint authority")
	if err != nil {
		return MintState{}, err
	}
	supply, err := reader.ReadUint64()
	if err != nil {
		return MintState{}, fmt.Errorf("token: decode mint supply: %w", err)
	}
	if err := reader.EnsureEOF(); err != nil {
		return MintState{}, fmt.Errorf("token: decode mint eof: %w", err)
	}
	return MintState{Version: version, Decimals: decimals, MintAuthority: authority, Supply: supply}, nil
}

func (state AccountState) MarshalBinary() ([]byte, error) {
	writer := borsh.NewWriter(MaxTokenStateBytes)
	writer.WriteUint8(uint8(AccountKindTokenAccount))
	writer.WriteUint8(state.Version)
	writer.WriteFixedBytes(state.Mint[:])
	writer.WriteFixedBytes(state.Owner[:])
	writer.WriteUint64(state.Amount)
	return writer.Bytes(), nil
}

func UnmarshalAccountStateBinary(data []byte) (AccountState, error) {
	reader := borsh.NewReader(data, MaxTokenStateBytes)
	kind, version, err := readStatePrefix(reader)
	if err != nil {
		return AccountState{}, err
	}
	if kind != AccountKindTokenAccount {
		return AccountState{}, fmt.Errorf("token: account is not token account")
	}
	mint, err := readPublicKey(reader, "account mint")
	if err != nil {
		return AccountState{}, err
	}
	owner, err := readPublicKey(reader, "account owner")
	if err != nil {
		return AccountState{}, err
	}
	amount, err := reader.ReadUint64()
	if err != nil {
		return AccountState{}, fmt.Errorf("token: decode account amount: %w", err)
	}
	if err := reader.EnsureEOF(); err != nil {
		return AccountState{}, fmt.Errorf("token: decode account eof: %w", err)
	}
	return AccountState{Version: version, Mint: mint, Owner: owner, Amount: amount}, nil
}

func readStatePrefix(reader *borsh.Reader) (AccountKind, uint8, error) {
	kind, err := reader.ReadUint8()
	if err != nil {
		return 0, 0, fmt.Errorf("token: decode account kind: %w", err)
	}
	version, err := reader.ReadUint8()
	if err != nil {
		return 0, 0, fmt.Errorf("token: decode account version: %w", err)
	}
	if version != TokenStateVersion {
		return 0, 0, fmt.Errorf("token: unsupported state version %d", version)
	}
	return AccountKind(kind), version, nil
}

func readMintState(context runtime.InstructionContext, address structure.PublicKey, programID structure.PublicKey) (MintState, error) {
	account, err := tokenOwnedAccount(context, address, programID)
	if err != nil {
		return MintState{}, err
	}
	return UnmarshalMintStateBinary(account.Data)
}

func readAccountState(context runtime.InstructionContext, address structure.PublicKey, programID structure.PublicKey) (AccountState, error) {
	account, err := tokenOwnedAccount(context, address, programID)
	if err != nil {
		return AccountState{}, err
	}
	return UnmarshalAccountStateBinary(account.Data)
}

func tokenOwnedAccount(context runtime.InstructionContext, address structure.PublicKey, programID structure.PublicKey) (structure.Account, error) {
	account, exists := context.Accounts[address]
	if !exists {
		return structure.Account{}, fmt.Errorf("%w: token account %s not found", structure.ErrInvalidLoadedTransaction, address.String())
	}
	if account.Owner != programID {
		return structure.Account{}, fmt.Errorf("%w: token account owner mismatch", structure.ErrInvalidInstruction)
	}
	if account.Executable {
		return structure.Account{}, fmt.Errorf("%w: token account cannot be executable", structure.ErrInvalidInstruction)
	}
	return account.Clone(), nil
}

func requireWritable(context runtime.InstructionContext, address structure.PublicKey) error {
	for _, accountIndex := range context.Instruction.AccountIndexes {
		if context.Message.AccountKeys[accountIndex] != address {
			continue
		}
		if runtime.IsWritableMessageAccount(int(accountIndex), context.Message) {
			return nil
		}
	}
	return fmt.Errorf("%w: token account must be writable", structure.ErrInvalidInstruction)
}

func accountAddressAt(context runtime.InstructionContext, instructionAccountIndex int) structure.PublicKey {
	messageAccountIndex := context.Instruction.AccountIndexes[instructionAccountIndex]
	return context.Message.AccountKeys[messageAccountIndex]
}

func writeAccountData(
	context runtime.InstructionContext,
	address structure.PublicKey,
	account structure.Account,
	marshal func() ([]byte, error),
) error {
	data, err := marshal()
	if err != nil {
		return err
	}
	account.Data = data
	if err := account.ValidateWithRent(context.RentConfig); err != nil {
		return fmt.Errorf("token: validate account write: %w", err)
	}
	context.Accounts[address] = account
	return nil
}

func readPublicKey(reader *borsh.Reader, field string) (structure.PublicKey, error) {
	value, err := reader.ReadFixedBytes(structure.PublicKeySize)
	if err != nil {
		return structure.PublicKey{}, fmt.Errorf("token: decode %s: %w", field, err)
	}
	publicKey, err := structure.NewPublicKey(value)
	if err != nil {
		return structure.PublicKey{}, fmt.Errorf("token: decode %s: %w", field, err)
	}
	return publicKey, nil
}
