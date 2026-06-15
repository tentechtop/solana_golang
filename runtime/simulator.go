package runtime

import (
	"fmt"

	"solana_golang/structure"
)

const (
	SimulationLogInstructionSuccess = "instruction success"
	SimulationLogTransactionSuccess = "transaction success"
)

// TransactionSimulationInput 描述交易模拟输入 + runtime 使用内存账户快照替代数据库读写。
type TransactionSimulationInput struct {
	Transaction     structure.Transaction
	Accounts        []structure.AddressedAccount
	BlockhashQueue  structure.BlockhashQueue
	CurrentSlot     uint64
	FeeCalculator   structure.FeeCalculator
	ComputeBudget   structure.ComputeBudgetLimits
	RentConfig      structure.RentConfig
	BuiltinPrograms structure.BuiltinProgramIDs
	Programs        []Program
	FallbackProgram Program
}

// TransactionSimulator 执行交易模拟 + 只返回写集不直接提交数据库。
type TransactionSimulator struct{}

// Simulate 执行交易模拟 + 校验签名、blockhash、费用和程序状态转换。
func (simulator TransactionSimulator) Simulate(input TransactionSimulationInput) (structure.TransactionExecutionResult, error) {
	normalizedInput := normalizeSimulationInput(input)
	loadedTransaction, result, err := prepareSimulation(normalizedInput)
	if err != nil {
		return result, err
	}
	if result.Status == structure.TransactionStatusFailed {
		return result, nil
	}

	programRegistry, err := newSimulationProgramRegistry(normalizedInput)
	if err != nil {
		result.Status = structure.TransactionStatusFailed
		result.Error = transactionFailure(structure.TransactionErrorCodeInvalidProgramForExecution, err.Error())
		result.PostBalances = balancesFromLoadedAccounts(loadedTransaction.Accounts)
		result.WrittenAccounts = writtenAccountsFromLoadedAccounts(loadedTransaction.Accounts)
		return result, result.Validate()
	}

	accountStates := cloneLoadedAccountsToMap(loadedTransaction.Accounts)
	for instructionIndex, instruction := range loadedTransaction.Message.Instructions {
		if err := executeSimulatedInstruction(instructionIndex, instruction, loadedTransaction.Message, accountStates, normalizedInput, programRegistry); err != nil {
			result.Status = structure.TransactionStatusFailed
			result.Error = instructionFailure(uint16(instructionIndex), structure.InstructionErrorCodeGeneric, err.Error())
			result.PostBalances = balancesFromMessage(loadedTransaction.Message.AccountKeys, accountStates)
			result.WrittenAccounts = writtenAccountsFromMessage(loadedTransaction.Message.AccountKeys, accountStates)
			return result, result.Validate()
		}
		result.LogMessages = append(result.LogMessages, SimulationLogInstructionSuccess)
	}

	result.Status = structure.TransactionStatusConfirmed
	result.PostBalances = balancesFromMessage(loadedTransaction.Message.AccountKeys, accountStates)
	result.WrittenAccounts = writtenAccountsFromMessage(loadedTransaction.Message.AccountKeys, accountStates)
	result.LogMessages = append(result.LogMessages, SimulationLogTransactionSuccess)
	return result, result.Validate()
}

func normalizeSimulationInput(input TransactionSimulationInput) TransactionSimulationInput {
	if input.FeeCalculator == (structure.FeeCalculator{}) {
		input.FeeCalculator = structure.DefaultFeeCalculator()
	}
	if input.ComputeBudget == (structure.ComputeBudgetLimits{}) {
		input.ComputeBudget = structure.DefaultComputeBudgetLimits()
	}
	if input.RentConfig == (structure.RentConfig{}) {
		input.RentConfig = structure.DefaultRentConfig
	}
	if input.BuiltinPrograms == (structure.BuiltinProgramIDs{}) {
		input.BuiltinPrograms = structure.DefaultBuiltinProgramIDs
	}
	return input
}

func newSimulationProgramRegistry(input TransactionSimulationInput) (ProgramRegistry, error) {
	registry, err := NewProgramRegistry(input.Programs...)
	if err != nil {
		return ProgramRegistry{}, err
	}
	if err := registry.SetFallbackProgram(input.FallbackProgram); err != nil {
		return ProgramRegistry{}, err
	}
	return registry, nil
}

func prepareSimulation(input TransactionSimulationInput) (structure.LoadedTransaction, structure.TransactionExecutionResult, error) {
	result := structure.TransactionExecutionResult{}
	if err := input.Transaction.Validate(); err != nil {
		result.Status = structure.TransactionStatusFailed
		result.Error = transactionFailure(structure.TransactionErrorCodeSanitizeFailure, err.Error())
		return structure.LoadedTransaction{}, result, result.Validate()
	}
	if valid, err := input.Transaction.HasValidSignatures(); err != nil || !valid {
		message := "signature verification failed"
		if err != nil {
			message = err.Error()
		}
		result.Status = structure.TransactionStatusFailed
		result.Error = transactionFailure(structure.TransactionErrorCodeSignatureFailure, message)
		return structure.LoadedTransaction{}, result, result.Validate()
	}

	message, err := input.Transaction.SolanaMessage()
	if err != nil {
		result.Status = structure.TransactionStatusFailed
		result.Error = transactionFailure(structure.TransactionErrorCodeSanitizeFailure, err.Error())
		return structure.LoadedTransaction{}, result, result.Validate()
	}
	if !input.BlockhashQueue.IsRecent(message.RecentBlockhash, input.CurrentSlot) {
		result.Status = structure.TransactionStatusFailed
		result.Error = transactionFailure(structure.TransactionErrorCodeBlockhashNotFound, "recent blockhash is not valid")
		return structure.LoadedTransaction{}, result, result.Validate()
	}
	resolvedMessage, err := structure.NewResolvedMessage(message, structure.LoadedAddresses{})
	if err != nil {
		result.Status = structure.TransactionStatusFailed
		result.Error = transactionFailure(structure.TransactionErrorCodeSanitizeFailure, err.Error())
		return structure.LoadedTransaction{}, result, result.Validate()
	}
	feeDetails, err := input.FeeCalculator.Calculate(len(input.Transaction.Signatures), input.ComputeBudget)
	if err != nil {
		result.Status = structure.TransactionStatusFailed
		result.Error = transactionFailure(structure.TransactionErrorCodeInsufficientFundsForFee, err.Error())
		return structure.LoadedTransaction{}, result, result.Validate()
	}

	accountStates, err := accountMapFromAddressedAccounts(input.Accounts)
	if err != nil {
		result.Status = structure.TransactionStatusFailed
		result.Error = transactionFailure(structure.TransactionErrorCodeAccountLoadedTwice, err.Error())
		return structure.LoadedTransaction{}, result, result.Validate()
	}
	addMissingCreateAccountPlaceholders(resolvedMessage, accountStates, input.BuiltinPrograms)
	loadedAccounts, err := loadSimulationAccounts(resolvedMessage, accountStates, input.BuiltinPrograms)
	if err != nil {
		result.Status = structure.TransactionStatusFailed
		result.Error = transactionFailure(structure.TransactionErrorCodeAccountNotFound, err.Error())
		return structure.LoadedTransaction{}, result, result.Validate()
	}
	loadedTransaction, err := structure.NewLoadedTransaction(input.Transaction, resolvedMessage, loadedAccounts, feeDetails)
	if err != nil {
		result.Status = structure.TransactionStatusFailed
		result.Error = transactionFailure(structure.TransactionErrorCodeSanitizeFailure, err.Error())
		return structure.LoadedTransaction{}, result, result.Validate()
	}

	result.FeeDetails = feeDetails
	result.PreBalances = balancesFromLoadedAccounts(loadedAccounts)
	result.LoadedAddresses = resolvedMessage.LoadedAddresses.Clone()
	if err := debitSimulationFee(loadedTransaction.FeePayer, feeDetails.TotalFee, accountStates, input.RentConfig); err != nil {
		result.Status = structure.TransactionStatusFailed
		result.Error = transactionFailure(structure.TransactionErrorCodeInsufficientFundsForFee, err.Error())
		result.PostBalances = balancesFromMessage(resolvedMessage.AccountKeys, accountStates)
		result.WrittenAccounts = writtenAccountsFromMessage(resolvedMessage.AccountKeys, accountStates)
		return structure.LoadedTransaction{}, result, result.Validate()
	}
	loadedAccounts, err = loadSimulationAccounts(resolvedMessage, accountStates, input.BuiltinPrograms)
	if err != nil {
		result.Status = structure.TransactionStatusFailed
		result.Error = transactionFailure(structure.TransactionErrorCodeAccountNotFound, err.Error())
		return structure.LoadedTransaction{}, result, result.Validate()
	}
	loadedTransaction.Accounts = loadedAccounts
	return loadedTransaction, result, nil
}

func addMissingCreateAccountPlaceholders(message structure.ResolvedMessage, accounts map[structure.PublicKey]structure.Account, builtinPrograms structure.BuiltinProgramIDs) {
	for _, instruction := range message.Instructions {
		if int(instruction.ProgramIDIndex) >= len(message.AccountKeys) {
			continue
		}
		programID := message.AccountKeys[instruction.ProgramIDIndex]
		if programID != builtinPrograms.System || len(instruction.AccountIndexes) < 2 {
			continue
		}
		systemInstruction, err := structure.UnmarshalSystemInstructionBinary(instruction.Data)
		if err != nil || systemInstruction.Type != structure.SystemInstructionCreateAccount {
			continue
		}
		newAccountAddress := message.AccountKeys[instruction.AccountIndexes[1]]
		if _, exists := accounts[newAccountAddress]; exists {
			continue
		}
		accounts[newAccountAddress] = structure.Account{Owner: builtinPrograms.System}
	}
}

func executeSimulatedInstruction(
	instructionIndex int,
	instruction structure.CompiledInstruction,
	message structure.ResolvedMessage,
	accounts map[structure.PublicKey]structure.Account,
	input TransactionSimulationInput,
	registry ProgramRegistry,
) error {
	return registry.Execute(InstructionContext{
		InstructionIndex: instructionIndex,
		Instruction:      instruction,
		Message:          message,
		Accounts:         accounts,
		CurrentSlot:      input.CurrentSlot,
		RentConfig:       input.RentConfig,
		ComputeBudget:    input.ComputeBudget,
		BuiltinPrograms:  input.BuiltinPrograms,
	})
}

func debitSimulationFee(feePayer structure.PublicKey, totalFee uint64, accounts map[structure.PublicKey]structure.Account, rentConfig structure.RentConfig) error {
	account, exists := accounts[feePayer]
	if !exists {
		return fmt.Errorf("%w: fee payer not found", structure.ErrInvalidLoadedTransaction)
	}
	if err := account.DebitLamports(totalFee, rentConfig); err != nil {
		return err
	}
	accounts[feePayer] = account
	return nil
}

func loadSimulationAccounts(
	message structure.ResolvedMessage,
	accountStates map[structure.PublicKey]structure.Account,
	builtinPrograms structure.BuiltinProgramIDs,
) ([]structure.LoadedAccount, error) {
	loadedAccounts := make([]structure.LoadedAccount, len(message.AccountKeys))
	staticMetas := message.StaticAccountMetas()
	for accountIndex, accountKey := range message.AccountKeys {
		account, exists := accountStates[accountKey]
		if !exists {
			return nil, fmt.Errorf("%w: account %d is missing", structure.ErrInvalidLoadedTransaction, accountIndex)
		}
		loadedAccounts[accountIndex] = structure.LoadedAccount{
			Address:    accountKey,
			Account:    account.Clone(),
			IsProgram:  builtinPrograms.IsBuiltinProgram(accountKey) || account.Executable,
			IsSigner:   accountIndex < len(staticMetas) && staticMetas[accountIndex].IsSigner,
			IsWritable: accountIndex < len(staticMetas) && staticMetas[accountIndex].IsWritable,
		}
		if accountIndex >= len(staticMetas) {
			loadedAccounts[accountIndex].IsWritable = accountIndex < len(message.StaticAccountKeys)+len(message.LoadedAddresses.Writable)
		}
	}
	return loadedAccounts, nil
}

func accountMapFromAddressedAccounts(accounts []structure.AddressedAccount) (map[structure.PublicKey]structure.Account, error) {
	accountMap := make(map[structure.PublicKey]structure.Account, len(accounts))
	for accountIndex, addressedAccount := range accounts {
		if _, exists := accountMap[addressedAccount.Address]; exists {
			return nil, fmt.Errorf("%w: duplicate account %d", structure.ErrInvalidLoadedTransaction, accountIndex)
		}
		if err := addressedAccount.Validate(); err != nil {
			return nil, fmt.Errorf("runtime: simulation account %d: %w", accountIndex, err)
		}
		accountMap[addressedAccount.Address] = addressedAccount.Account.Clone()
	}
	return accountMap, nil
}

func cloneLoadedAccountsToMap(accounts []structure.LoadedAccount) map[structure.PublicKey]structure.Account {
	accountMap := make(map[structure.PublicKey]structure.Account, len(accounts))
	for _, account := range accounts {
		accountMap[account.Address] = account.Account.Clone()
	}
	return accountMap
}

func balancesFromLoadedAccounts(accounts []structure.LoadedAccount) []uint64 {
	balances := make([]uint64, len(accounts))
	for index, account := range accounts {
		balances[index] = account.Account.Lamports
	}
	return balances
}

func balancesFromMessage(accountKeys []structure.PublicKey, accounts map[structure.PublicKey]structure.Account) []uint64 {
	balances := make([]uint64, len(accountKeys))
	for index, accountKey := range accountKeys {
		balances[index] = accounts[accountKey].Lamports
	}
	return balances
}

func writtenAccountsFromMessage(accountKeys []structure.PublicKey, accounts map[structure.PublicKey]structure.Account) []structure.AddressedAccount {
	writtenAccounts := make([]structure.AddressedAccount, len(accountKeys))
	for index, accountKey := range accountKeys {
		writtenAccounts[index] = structure.AddressedAccount{
			Address: accountKey,
			Account: accounts[accountKey].Clone(),
		}
	}
	return writtenAccounts
}

func writtenAccountsFromLoadedAccounts(accounts []structure.LoadedAccount) []structure.AddressedAccount {
	writtenAccounts := make([]structure.AddressedAccount, len(accounts))
	for index, account := range accounts {
		writtenAccounts[index] = structure.AddressedAccount{
			Address: account.Address,
			Account: account.Account.Clone(),
		}
	}
	return writtenAccounts
}

func transactionFailure(code structure.TransactionErrorCode, message string) *structure.TransactionError {
	return &structure.TransactionError{Code: code, Message: message}
}

func instructionFailure(instructionIndex uint16, code structure.InstructionErrorCode, message string) *structure.TransactionError {
	return &structure.TransactionError{
		Code: structure.TransactionErrorCodeInstructionError,
		InstructionError: &structure.InstructionError{
			InstructionIndex: instructionIndex,
			Code:             code,
			Message:          message,
		},
	}
}
