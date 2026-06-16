package blockchain

import (
	"fmt"
	"time"

	"solana_golang/structure"
)

const PrivacyStateRentReserveBytes = 4096

type PrivacyDepositTransactionParams struct {
	Source        structure.SolanaKeyPair
	StateAccount  structure.SolanaKeyPair
	Amount        uint64
	Commitment    structure.Hash
	EncryptedNote []byte
	AuditRecords  []structure.PrivacyAuditRecord
	CreateState   bool
}

type PrivacyWithdrawTransactionParams struct {
	Authority        structure.SolanaKeyPair
	StateAddress     structure.PublicKey
	Destination      structure.PublicKey
	Amount           uint64
	SourceCommitment structure.Hash
	Nullifier        structure.Hash
	AuditRecords     []structure.PrivacyAuditRecord
}

type PrivacyTransferTransactionParams struct {
	Authority            structure.SolanaKeyPair
	StateAddress         structure.PublicKey
	Amount               uint64
	SourceCommitment     structure.Hash
	Nullifier            structure.Hash
	OutputCommitment     structure.Hash
	OutputSpendAuthority structure.PublicKey
	OutputEncryptedNote  []byte
	OutputAuditRecords   []structure.PrivacyAuditRecord
}

type PrivacyAuthorizeAuditTransactionParams struct {
	Authority       structure.SolanaKeyPair
	StateAddress    structure.PublicKey
	Commitment      structure.Hash
	Auditor         structure.PublicKey
	Scope           structure.PrivacyAuditScope
	ExpiresAtSlot   uint64
	AuditCiphertext []byte
}

// NewPrivacyDepositTransaction 构造透明转隐私交易 + 首次使用时原子创建隐私状态账户避免半初始化。
func NewPrivacyDepositTransaction(params PrivacyDepositTransactionParams, recentBlockhash structure.Hash) (structure.Transaction, error) {
	instruction, err := structure.NewPrivacyDepositInstruction(structure.PrivacyStateVersion, nil, structure.PrivacyDepositParams{
		Amount:         params.Amount,
		Commitment:     params.Commitment,
		SpendAuthority: params.Source.PublicKey,
		EncryptedNote:  params.EncryptedNote,
		AuditRecords:   params.AuditRecords,
	})
	if err != nil {
		return structure.Transaction{}, err
	}
	instructionData, err := instruction.MarshalBinary()
	if err != nil {
		return structure.Transaction{}, fmt.Errorf("blockchain: marshal privacy deposit: %w", err)
	}
	accounts := privacyDepositAccounts(params)
	instructions, err := privacyDepositInstructions(params, accounts, instructionData)
	if err != nil {
		return structure.Transaction{}, err
	}
	signers := map[structure.PublicKey][]byte{params.Source.PublicKey: params.Source.PrivateKey}
	if params.CreateState {
		signers[params.StateAccount.PublicKey] = params.StateAccount.PrivateKey
	}
	return signPrivacyTransaction(accounts, instructions, recentBlockhash, signers)
}

// NewPrivacyWithdrawTransaction 构造隐私转透明交易 + 花费授权由持有 note 的签名账户完成。
func NewPrivacyWithdrawTransaction(params PrivacyWithdrawTransactionParams, recentBlockhash structure.Hash) (structure.Transaction, error) {
	instruction, err := structure.NewPrivacyWithdrawInstruction(structure.PrivacyStateVersion, nil, structure.PrivacyWithdrawParams{
		Amount:           params.Amount,
		SourceCommitment: params.SourceCommitment,
		Nullifier:        params.Nullifier,
		AuditRecords:     params.AuditRecords,
	})
	if err != nil {
		return structure.Transaction{}, err
	}
	data, err := instruction.MarshalBinary()
	if err != nil {
		return structure.Transaction{}, fmt.Errorf("blockchain: marshal privacy withdraw: %w", err)
	}
	accounts := []structure.AccountMeta{
		{PublicKey: params.Authority.PublicKey, IsSigner: true, IsWritable: true},
		{PublicKey: params.StateAddress, IsSigner: false, IsWritable: true},
		{PublicKey: params.Destination, IsSigner: false, IsWritable: true},
		{PublicKey: structure.DefaultBuiltinProgramIDs.Privacy, IsSigner: false, IsWritable: false},
	}
	instructions, err := compilePrivacyInstruction(accounts, []structure.PublicKey{params.StateAddress, params.Destination}, data)
	if err != nil {
		return structure.Transaction{}, err
	}
	return signPrivacyTransaction(accounts, instructions, recentBlockhash, map[structure.PublicKey][]byte{params.Authority.PublicKey: params.Authority.PrivateKey})
}

// NewPrivacyTransferTransaction 构造隐私转隐私交易 + 消耗旧 note 并在同一状态账户中生成新 note。
func NewPrivacyTransferTransaction(params PrivacyTransferTransactionParams, recentBlockhash structure.Hash) (structure.Transaction, error) {
	instruction, err := structure.NewPrivacyTransferInstruction(structure.PrivacyStateVersion, nil, structure.PrivacyTransferParams{
		Amount:               params.Amount,
		SourceCommitment:     params.SourceCommitment,
		Nullifier:            params.Nullifier,
		OutputCommitment:     params.OutputCommitment,
		OutputSpendAuthority: params.OutputSpendAuthority,
		OutputEncryptedNote:  params.OutputEncryptedNote,
		OutputAuditRecords:   params.OutputAuditRecords,
	})
	if err != nil {
		return structure.Transaction{}, err
	}
	data, err := instruction.MarshalBinary()
	if err != nil {
		return structure.Transaction{}, fmt.Errorf("blockchain: marshal privacy transfer: %w", err)
	}
	accounts := []structure.AccountMeta{
		{PublicKey: params.Authority.PublicKey, IsSigner: true, IsWritable: true},
		{PublicKey: params.StateAddress, IsSigner: false, IsWritable: true},
		{PublicKey: structure.DefaultBuiltinProgramIDs.Privacy, IsSigner: false, IsWritable: false},
	}
	instructions, err := compilePrivacyInstruction(accounts, []structure.PublicKey{params.StateAddress}, data)
	if err != nil {
		return structure.Transaction{}, err
	}
	return signPrivacyTransaction(accounts, instructions, recentBlockhash, map[structure.PublicKey][]byte{params.Authority.PublicKey: params.Authority.PrivateKey})
}

// NewPrivacyAuthorizeAuditTransaction 构造审计授权交易 + 只追加授权密文不改变隐私余额。
func NewPrivacyAuthorizeAuditTransaction(params PrivacyAuthorizeAuditTransactionParams, recentBlockhash structure.Hash) (structure.Transaction, error) {
	instruction, err := structure.NewPrivacyAuthorizeAuditInstruction(structure.PrivacyStateVersion, nil, structure.PrivacyAuthorizeAuditParams{
		Commitment:      params.Commitment,
		Auditor:         params.Auditor,
		Scope:           params.Scope,
		ExpiresAtSlot:   params.ExpiresAtSlot,
		AuditCiphertext: params.AuditCiphertext,
	})
	if err != nil {
		return structure.Transaction{}, err
	}
	data, err := instruction.MarshalBinary()
	if err != nil {
		return structure.Transaction{}, fmt.Errorf("blockchain: marshal privacy audit auth: %w", err)
	}
	accounts := []structure.AccountMeta{
		{PublicKey: params.Authority.PublicKey, IsSigner: true, IsWritable: true},
		{PublicKey: params.StateAddress, IsSigner: false, IsWritable: true},
		{PublicKey: structure.DefaultBuiltinProgramIDs.Privacy, IsSigner: false, IsWritable: false},
	}
	instructions, err := compilePrivacyInstruction(accounts, []structure.PublicKey{params.StateAddress}, data)
	if err != nil {
		return structure.Transaction{}, err
	}
	return signPrivacyTransaction(accounts, instructions, recentBlockhash, map[structure.PublicKey][]byte{params.Authority.PublicKey: params.Authority.PrivateKey})
}

func privacyDepositAccounts(params PrivacyDepositTransactionParams) []structure.AccountMeta {
	if !params.CreateState {
		return []structure.AccountMeta{
			{PublicKey: params.Source.PublicKey, IsSigner: true, IsWritable: true},
			{PublicKey: params.StateAccount.PublicKey, IsSigner: false, IsWritable: true},
			{PublicKey: structure.DefaultBuiltinProgramIDs.Privacy, IsSigner: false, IsWritable: false},
		}
	}
	return []structure.AccountMeta{
		{PublicKey: params.Source.PublicKey, IsSigner: true, IsWritable: true},
		{PublicKey: params.StateAccount.PublicKey, IsSigner: true, IsWritable: true},
		{PublicKey: structure.DefaultBuiltinProgramIDs.System, IsSigner: false, IsWritable: false},
		{PublicKey: structure.DefaultBuiltinProgramIDs.Privacy, IsSigner: false, IsWritable: false},
	}
}

func privacyDepositInstructions(params PrivacyDepositTransactionParams, accounts []structure.AccountMeta, privacyData []byte) ([]structure.CompiledInstruction, error) {
	privacyInstruction, err := compileInstruction(accounts, structure.DefaultBuiltinProgramIDs.Privacy, []structure.PublicKey{params.Source.PublicKey, params.StateAccount.PublicKey}, privacyData)
	if err != nil {
		return nil, err
	}
	if !params.CreateState {
		return []structure.CompiledInstruction{privacyInstruction}, nil
	}
	createInstruction, err := createPrivacyStateInstruction(accounts)
	if err != nil {
		return nil, err
	}
	return []structure.CompiledInstruction{createInstruction, privacyInstruction}, nil
}

func createPrivacyStateInstruction(accounts []structure.AccountMeta) (structure.CompiledInstruction, error) {
	rentReserve, err := structure.MinimumBalanceForRentExemption(PrivacyStateRentReserveBytes)
	if err != nil {
		return structure.CompiledInstruction{}, fmt.Errorf("blockchain: calculate privacy rent: %w", err)
	}
	instruction, err := structure.NewCreateAccountInstruction(structure.CreateAccountParams{
		Lamports: rentReserve,
		Space:    PrivacyStateRentReserveBytes,
		Owner:    structure.DefaultBuiltinProgramIDs.Privacy,
	})
	if err != nil {
		return structure.CompiledInstruction{}, err
	}
	data, err := instruction.MarshalBinary()
	if err != nil {
		return structure.CompiledInstruction{}, fmt.Errorf("blockchain: marshal privacy state create: %w", err)
	}
	return compileInstruction(accounts, structure.DefaultBuiltinProgramIDs.System, []structure.PublicKey{accounts[0].PublicKey, accounts[1].PublicKey}, data)
}

func compilePrivacyInstruction(accounts []structure.AccountMeta, instructionAccounts []structure.PublicKey, data []byte) ([]structure.CompiledInstruction, error) {
	instruction, err := compileInstruction(accounts, structure.DefaultBuiltinProgramIDs.Privacy, instructionAccounts, data)
	if err != nil {
		return nil, err
	}
	return []structure.CompiledInstruction{instruction}, nil
}

func compileInstruction(accounts []structure.AccountMeta, programID structure.PublicKey, instructionAccounts []structure.PublicKey, data []byte) (structure.CompiledInstruction, error) {
	accountIndexByKey, err := structure.AccountIndexMap(accounts)
	if err != nil {
		return structure.CompiledInstruction{}, err
	}
	return structure.CompileInstruction(programID, instructionAccounts, data, accountIndexByKey)
}

func signPrivacyTransaction(accounts []structure.AccountMeta, instructions []structure.CompiledInstruction, recentBlockhash structure.Hash, signers map[structure.PublicKey][]byte) (structure.Transaction, error) {
	transaction := structure.Transaction{
		Accounts:        accounts,
		Instructions:    instructions,
		RecentBlockhash: recentBlockhash,
		SubmitTime:      time.Now().UnixMilli(),
	}
	return transaction.Sign(signers)
}
