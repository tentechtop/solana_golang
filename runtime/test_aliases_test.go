package runtime_test

import (
	"testing"

	bpfloader "github.com/tentechtop/ordexus-core/programs/bpfloader"
	stakeprogram "github.com/tentechtop/ordexus-core/programs/stake"
	systemprogram "github.com/tentechtop/ordexus-core/programs/system"
	tokenprogram "github.com/tentechtop/ordexus-core/programs/token"
	vmprogram "github.com/tentechtop/ordexus-core/programs/vm"
	runtimepkg "github.com/tentechtop/ordexus-core/runtime"
	"github.com/tentechtop/ordexus-core/structure"
	ovm "github.com/tentechtop/ordexus-core/vm"
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
type PublicKey = structure.PublicKey
type RecentBlockhashEntry = structure.RecentBlockhashEntry
type SystemInstruction = structure.SystemInstruction
type TransferParams = structure.TransferParams
type Transaction = structure.Transaction
type TransactionExecutionResult = structure.TransactionExecutionResult
type TokenAccountState = tokenprogram.AccountState
type TokenMintState = tokenprogram.MintState

const (
	LamportsPerSignature           = structure.LamportsPerSignature
	TransactionStatusConfirmed     = structure.TransactionStatusConfirmed
	TransactionStatusFailed        = structure.TransactionStatusFailed
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
	NewPublicKey                                = structure.NewPublicKey
	NewTransferInstruction                      = structure.NewTransferInstruction
	CompileInstruction                          = structure.CompileInstruction
	AccountIndexMap                             = structure.AccountIndexMap
	TransactionErrorCodeInsufficientFundsForFee = structure.TransactionErrorCodeInsufficientFundsForFee
)

func simulateWithDefaultPrograms(t *testing.T, input TransactionSimulationInput) (structure.TransactionExecutionResult, error) {
	t.Helper()
	input.Programs = append(input.Programs,
		systemprogram.NewProgram(DefaultBuiltinProgramIDs.System),
		tokenprogram.NewProgram(DefaultBuiltinProgramIDs.Token),
		stakeprogram.NewProgram(DefaultBuiltinProgramIDs.Stake),
		bpfloader.NewProgram(DefaultBuiltinProgramIDs.BPFLoader),
	)
	return runtimepkg.TransactionSimulator{}.Simulate(input)
}

func simulateWithVirtualMachine(t *testing.T, input TransactionSimulationInput) (structure.TransactionExecutionResult, error) {
	t.Helper()
	input.Programs = append(input.Programs,
		systemprogram.NewProgram(DefaultBuiltinProgramIDs.System),
		bpfloader.NewProgram(DefaultBuiltinProgramIDs.BPFLoader),
	)
	input.FallbackProgram = vmprogram.NewProgram(DefaultBuiltinProgramIDs.BPFLoader, ovm.Runtime{})
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

func mustTransactionFeeDetails(t *testing.T, transaction Transaction) structure.FeeDetails {
	t.Helper()
	computeBudget, err := structure.EstimateTransactionComputeBudget(transaction, DefaultBuiltinProgramIDs)
	if err != nil {
		t.Fatalf("EstimateTransactionComputeBudget() error = %v", err)
	}
	feeDetails, err := DefaultFeeCalculator().Calculate(len(transaction.Signatures), computeBudget)
	if err != nil {
		t.Fatalf("FeeCalculator.Calculate() error = %v", err)
	}
	return feeDetails
}

func signedSimulationProgramTransaction(t *testing.T, programID PublicKey, accounts []AccountMeta, instructionAccounts []PublicKey, instructionData []byte, blockhash Blockhash, privateKeys map[PublicKey][]byte) Transaction {
	t.Helper()
	accountIndexByKey, err := AccountIndexMap(accounts)
	if err != nil {
		t.Fatalf("AccountIndexMap() error = %v", err)
	}
	compiledInstruction, err := CompileInstruction(programID, instructionAccounts, instructionData, accountIndexByKey)
	if err != nil {
		t.Fatalf("CompileInstruction() error = %v", err)
	}
	transaction := Transaction{
		Accounts:        accounts,
		Instructions:    []CompiledInstruction{compiledInstruction},
		RecentBlockhash: blockhash,
	}
	signedTransaction, err := transaction.Sign(privateKeys)
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	return signedTransaction
}
