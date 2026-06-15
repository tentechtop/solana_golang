package runtime_test

import (
	"testing"

	privacyprogram "solana_golang/programs/privacy"
	systemprogram "solana_golang/programs/system"
	vmprogram "solana_golang/programs/vm"
	runtimepkg "solana_golang/runtime"
	"solana_golang/structure"
	svm "solana_golang/vm"
)

type TransactionSimulationInput = runtimepkg.TransactionSimulationInput
type TransactionSimulator = runtimepkg.TransactionSimulator

type Account = structure.Account
type AccountMeta = structure.AccountMeta
type AddressedAccount = structure.AddressedAccount
type Blockhash = structure.Blockhash
type BlockhashQueue = structure.BlockhashQueue
type CompiledInstruction = structure.CompiledInstruction
type CreateAccountParams = structure.CreateAccountParams
type Hash = structure.Hash
type PrivacyAuditRecord = structure.PrivacyAuditRecord
type PrivacyAuthorizeAuditParams = structure.PrivacyAuthorizeAuditParams
type PrivacyDepositParams = structure.PrivacyDepositParams
type PrivacyInstruction = structure.PrivacyInstruction
type PrivacyAuditPayload = structure.PrivacyAuditPayload
type PrivacyAuditScope = structure.PrivacyAuditScope
type PrivacyNoteRecord = structure.PrivacyNoteRecord
type PrivacyState = structure.PrivacyState
type PrivacyTransferParams = structure.PrivacyTransferParams
type PrivacyWithdrawParams = structure.PrivacyWithdrawParams
type PublicKey = structure.PublicKey
type RecentBlockhashEntry = structure.RecentBlockhashEntry
type SystemInstruction = structure.SystemInstruction
type TransferParams = structure.TransferParams
type Transaction = structure.Transaction
type TransactionExecutionResult = structure.TransactionExecutionResult

const (
	LamportsPerSignature           = structure.LamportsPerSignature
	PrivacyAuditScopeRegulatory    = structure.PrivacyAuditScopeRegulatory
	PrivacyAuditScopeBusiness      = structure.PrivacyAuditScopeBusiness
	TransactionStatusConfirmed     = structure.TransactionStatusConfirmed
	TransactionStatusFailed        = structure.TransactionStatusFailed
	PrivacyAuditPayloadVersion     = structure.PrivacyAuditPayloadVersion
	PrivacyInstructionDeposit      = structure.PrivacyInstructionDeposit
	PrivacyInstructionTransfer     = structure.PrivacyInstructionTransfer
	PrivacyInstructionWithdraw     = structure.PrivacyInstructionWithdraw
	PrivacyAuditKeySize            = structure.PrivacyAuditKeySize
	PrivacyStateVersion            = structure.PrivacyStateVersion
	TransactionErrorCodeBlockhash  = structure.TransactionErrorCodeBlockhashNotFound
	MaxRecentBlockhashAgeSlots     = structure.MaxRecentBlockhashAgeSlots
	TransactionErrorCodeFeeFailure = structure.TransactionErrorCodeInsufficientFundsForFee
)

var (
	DefaultBuiltinProgramIDs                    = structure.DefaultBuiltinProgramIDs
	DefaultFeeCalculator                        = structure.DefaultFeeCalculator
	DefaultRentConfig                           = structure.DefaultRentConfig
	NewAccount                                  = structure.NewAccount
	NewBlockhashQueue                           = structure.NewBlockhashQueue
	NewCreateAccountInstruction                 = structure.NewCreateAccountInstruction
	NewHash                                     = structure.NewHash
	NewPrivacyAuthorizeAuditInstruction         = structure.NewPrivacyAuthorizeAuditInstruction
	NewPrivacyDepositInstruction                = structure.NewPrivacyDepositInstruction
	NewPrivacyTransferInstruction               = structure.NewPrivacyTransferInstruction
	NewPrivacyWithdrawInstruction               = structure.NewPrivacyWithdrawInstruction
	NewPublicKey                                = structure.NewPublicKey
	NewTransferInstruction                      = structure.NewTransferInstruction
	UnmarshalPrivacyStateBinary                 = structure.UnmarshalPrivacyStateBinary
	BuildPrivacyTransferProofMessage            = structure.BuildPrivacyTransferProofMessage
	BuildPrivacyWithdrawProofMessage            = structure.BuildPrivacyWithdrawProofMessage
	AuditPrivacyNote                            = structure.AuditPrivacyNote
	CompileInstruction                          = structure.CompileInstruction
	AccountIndexMap                             = structure.AccountIndexMap
	NewEncryptedPrivacyAuditRecord              = structure.NewEncryptedPrivacyAuditRecord
	TransactionErrorCodeInsufficientFundsForFee = structure.TransactionErrorCodeInsufficientFundsForFee
)

func simulateWithDefaultPrograms(t *testing.T, input TransactionSimulationInput) (structure.TransactionExecutionResult, error) {
	t.Helper()
	input.Programs = append(input.Programs,
		systemprogram.NewProgram(DefaultBuiltinProgramIDs.System),
		privacyprogram.NewProgram(DefaultBuiltinProgramIDs.Privacy),
	)
	return runtimepkg.TransactionSimulator{}.Simulate(input)
}

func simulateWithVirtualMachine(t *testing.T, input TransactionSimulationInput) (structure.TransactionExecutionResult, error) {
	t.Helper()
	input.Programs = append(input.Programs,
		systemprogram.NewProgram(DefaultBuiltinProgramIDs.System),
		privacyprogram.NewProgram(DefaultBuiltinProgramIDs.Privacy),
	)
	input.FallbackProgram = vmprogram.NewProgram(DefaultBuiltinProgramIDs.BPFLoader, svm.Runtime{})
	return runtimepkg.TransactionSimulator{}.Simulate(input)
}

func newTestPublicKey(seed byte) PublicKey {
	var publicKey PublicKey
	for index := range publicKey {
		publicKey[index] = seed
	}
	return publicKey
}

func newTestHash(seed byte) Hash {
	var hash Hash
	for index := range hash {
		hash[index] = seed
	}
	return hash
}

func mustMinimumBalance(t *testing.T, dataLength int) uint64 {
	t.Helper()
	balance, err := DefaultRentConfig.MinimumBalance(dataLength)
	if err != nil {
		t.Fatalf("MinimumBalance() error = %v", err)
	}
	return balance
}
