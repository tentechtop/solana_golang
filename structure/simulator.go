package structure

import (
	"fmt"
)

const (
	SimulationLogInstructionSuccess = "instruction success"
	SimulationLogTransactionSuccess = "transaction success"
)

// TransactionSimulationInput 描述交易模拟输入 + 使用内存账户快照替代数据库读写。
type TransactionSimulationInput struct {
	Transaction     Transaction
	Accounts        []AddressedAccount
	BlockhashQueue  BlockhashQueue
	CurrentSlot     uint64
	FeeCalculator   FeeCalculator
	ComputeBudget   ComputeBudgetLimits
	RentConfig      RentConfig
	BuiltinPrograms BuiltinProgramIDs
	Strategies      []InstructionStrategy
}

// TransactionSimulator 执行交易模拟 + 只返回写集不直接提交数据库。
type TransactionSimulator struct{}

// Simulate 执行交易模拟 + 校验签名、blockhash、费用和系统指令状态转换。
func (simulator TransactionSimulator) Simulate(input TransactionSimulationInput) (TransactionExecutionResult, error) {
	normalizedInput := normalizeSimulationInput(input)
	loadedTransaction, result, err := prepareSimulation(normalizedInput)
	if err != nil {
		return result, err
	}
	if result.Status == TransactionStatusFailed {
		return result, nil
	}

	strategyRegistry, err := DefaultInstructionStrategyRegistry(normalizedInput.BuiltinPrograms)
	if err != nil {
		result.Status = TransactionStatusFailed
		result.Error = transactionFailure(TransactionErrorCodeInvalidProgramForExecution, err.Error())
		result.PostBalances = balancesFromLoadedAccounts(loadedTransaction.Accounts)
		result.WrittenAccounts = writtenAccountsFromLoadedAccounts(loadedTransaction.Accounts)
		return result, result.Validate()
	}
	if len(normalizedInput.Strategies) > 0 {
		strategyRegistry, err = NewInstructionStrategyRegistry(normalizedInput.Strategies...)
		if err != nil {
			result.Status = TransactionStatusFailed
			result.Error = transactionFailure(TransactionErrorCodeInvalidProgramForExecution, err.Error())
			result.PostBalances = balancesFromLoadedAccounts(loadedTransaction.Accounts)
			result.WrittenAccounts = writtenAccountsFromLoadedAccounts(loadedTransaction.Accounts)
			return result, result.Validate()
		}
	}

	accountStates := cloneLoadedAccountsToMap(loadedTransaction.Accounts)
	for instructionIndex, instruction := range loadedTransaction.Message.Instructions {
		if err := executeSimulatedInstruction(instructionIndex, instruction, loadedTransaction.Message, accountStates, normalizedInput, strategyRegistry); err != nil {
			result.Status = TransactionStatusFailed
			result.Error = instructionFailure(uint16(instructionIndex), InstructionErrorCodeGeneric, err.Error())
			result.PostBalances = balancesFromMessage(loadedTransaction.Message.AccountKeys, accountStates)
			result.WrittenAccounts = writtenAccountsFromMessage(loadedTransaction.Message.AccountKeys, accountStates)
			return result, result.Validate()
		}
		result.LogMessages = append(result.LogMessages, SimulationLogInstructionSuccess)
	}

	result.Status = TransactionStatusConfirmed
	result.PostBalances = balancesFromMessage(loadedTransaction.Message.AccountKeys, accountStates)
	result.WrittenAccounts = writtenAccountsFromMessage(loadedTransaction.Message.AccountKeys, accountStates)
	result.LogMessages = append(result.LogMessages, SimulationLogTransactionSuccess)
	return result, result.Validate()
}

func normalizeSimulationInput(input TransactionSimulationInput) TransactionSimulationInput {
	if input.FeeCalculator == (FeeCalculator{}) {
		input.FeeCalculator = DefaultFeeCalculator()
	}
	if input.ComputeBudget == (ComputeBudgetLimits{}) {
		input.ComputeBudget = DefaultComputeBudgetLimits()
	}
	if input.RentConfig == (RentConfig{}) {
		input.RentConfig = DefaultRentConfig
	}
	if input.BuiltinPrograms == (BuiltinProgramIDs{}) {
		input.BuiltinPrograms = DefaultBuiltinProgramIDs
	}
	return input
}

func prepareSimulation(input TransactionSimulationInput) (LoadedTransaction, TransactionExecutionResult, error) {
	result := TransactionExecutionResult{}
	if err := input.Transaction.Validate(); err != nil {
		result.Status = TransactionStatusFailed
		result.Error = transactionFailure(TransactionErrorCodeSanitizeFailure, err.Error())
		return LoadedTransaction{}, result, result.Validate()
	}
	if valid, err := input.Transaction.HasValidSignatures(); err != nil || !valid {
		message := "signature verification failed"
		if err != nil {
			message = err.Error()
		}
		result.Status = TransactionStatusFailed
		result.Error = transactionFailure(TransactionErrorCodeSignatureFailure, message)
		return LoadedTransaction{}, result, result.Validate()
	}

	message, err := input.Transaction.SolanaMessage()
	if err != nil {
		result.Status = TransactionStatusFailed
		result.Error = transactionFailure(TransactionErrorCodeSanitizeFailure, err.Error())
		return LoadedTransaction{}, result, result.Validate()
	}
	if !input.BlockhashQueue.IsRecent(message.RecentBlockhash, input.CurrentSlot) {
		result.Status = TransactionStatusFailed
		result.Error = transactionFailure(TransactionErrorCodeBlockhashNotFound, "recent blockhash is not valid")
		return LoadedTransaction{}, result, result.Validate()
	}
	resolvedMessage, err := NewResolvedMessage(message, LoadedAddresses{})
	if err != nil {
		result.Status = TransactionStatusFailed
		result.Error = transactionFailure(TransactionErrorCodeSanitizeFailure, err.Error())
		return LoadedTransaction{}, result, result.Validate()
	}
	feeDetails, err := input.FeeCalculator.Calculate(len(input.Transaction.Signatures), input.ComputeBudget)
	if err != nil {
		result.Status = TransactionStatusFailed
		result.Error = transactionFailure(TransactionErrorCodeInsufficientFundsForFee, err.Error())
		return LoadedTransaction{}, result, result.Validate()
	}

	accountStates, err := accountMapFromAddressedAccounts(input.Accounts)
	if err != nil {
		result.Status = TransactionStatusFailed
		result.Error = transactionFailure(TransactionErrorCodeAccountLoadedTwice, err.Error())
		return LoadedTransaction{}, result, result.Validate()
	}
	addMissingCreateAccountPlaceholders(resolvedMessage, accountStates, input.BuiltinPrograms)
	loadedAccounts, err := loadSimulationAccounts(resolvedMessage, accountStates, input.BuiltinPrograms)
	if err != nil {
		result.Status = TransactionStatusFailed
		result.Error = transactionFailure(TransactionErrorCodeAccountNotFound, err.Error())
		return LoadedTransaction{}, result, result.Validate()
	}
	loadedTransaction, err := NewLoadedTransaction(input.Transaction, resolvedMessage, loadedAccounts, feeDetails)
	if err != nil {
		result.Status = TransactionStatusFailed
		result.Error = transactionFailure(TransactionErrorCodeSanitizeFailure, err.Error())
		return LoadedTransaction{}, result, result.Validate()
	}

	result.FeeDetails = feeDetails
	result.PreBalances = balancesFromLoadedAccounts(loadedAccounts)
	result.LoadedAddresses = resolvedMessage.LoadedAddresses.Clone()
	if err := debitSimulationFee(loadedTransaction.FeePayer, feeDetails.TotalFee, accountStates, input.RentConfig); err != nil {
		result.Status = TransactionStatusFailed
		result.Error = transactionFailure(TransactionErrorCodeInsufficientFundsForFee, err.Error())
		result.PostBalances = balancesFromMessage(resolvedMessage.AccountKeys, accountStates)
		result.WrittenAccounts = writtenAccountsFromMessage(resolvedMessage.AccountKeys, accountStates)
		return LoadedTransaction{}, result, result.Validate()
	}
	loadedAccounts, err = loadSimulationAccounts(resolvedMessage, accountStates, input.BuiltinPrograms)
	if err != nil {
		result.Status = TransactionStatusFailed
		result.Error = transactionFailure(TransactionErrorCodeAccountNotFound, err.Error())
		return LoadedTransaction{}, result, result.Validate()
	}
	loadedTransaction.Accounts = loadedAccounts
	return loadedTransaction, result, nil
}

func addMissingCreateAccountPlaceholders(message ResolvedMessage, accounts map[PublicKey]Account, builtinPrograms BuiltinProgramIDs) {
	for _, instruction := range message.Instructions {
		if int(instruction.ProgramIDIndex) >= len(message.AccountKeys) {
			continue
		}
		programID := message.AccountKeys[instruction.ProgramIDIndex]
		if programID != builtinPrograms.System || len(instruction.AccountIndexes) < 2 {
			continue
		}
		systemInstruction, err := UnmarshalSystemInstructionBinary(instruction.Data)
		if err != nil || systemInstruction.Type != SystemInstructionCreateAccount {
			continue
		}
		newAccountAddress := message.AccountKeys[instruction.AccountIndexes[1]]
		if _, exists := accounts[newAccountAddress]; exists {
			continue
		}
		accounts[newAccountAddress] = Account{Owner: builtinPrograms.System}
	}
}

func executeSimulatedInstruction(instructionIndex int, instruction CompiledInstruction, message ResolvedMessage, accounts map[PublicKey]Account, input TransactionSimulationInput, registry InstructionStrategyRegistry) error {
	return registry.Execute(InstructionExecutionContext{
		InstructionIndex: instructionIndex,
		Instruction:      instruction,
		Message:          message,
		Accounts:         accounts,
		CurrentSlot:      input.CurrentSlot,
		RentConfig:       input.RentConfig,
		BuiltinPrograms:  input.BuiltinPrograms,
	})
}

func executeSystemInstruction(systemInstruction SystemInstruction, compiledInstruction CompiledInstruction, message ResolvedMessage, accounts map[PublicKey]Account, rentConfig RentConfig) error {
	switch systemInstruction.Type {
	case SystemInstructionTransfer:
		return executeSystemTransfer(systemInstruction, compiledInstruction, message, accounts, rentConfig)
	case SystemInstructionCreateAccount:
		return executeSystemCreateAccount(systemInstruction, compiledInstruction, message, accounts, rentConfig)
	case SystemInstructionAssign:
		return executeSystemAssign(systemInstruction, compiledInstruction, message, accounts)
	case SystemInstructionAllocate:
		return executeSystemAllocate(systemInstruction, compiledInstruction, message, accounts, rentConfig)
	default:
		return fmt.Errorf("%w: unsupported type %d", ErrInvalidSystemInstruction, systemInstruction.Type)
	}
}

func executeSystemTransfer(systemInstruction SystemInstruction, compiledInstruction CompiledInstruction, message ResolvedMessage, accounts map[PublicKey]Account, rentConfig RentConfig) error {
	if len(compiledInstruction.AccountIndexes) < 2 {
		return fmt.Errorf("%w: transfer requires source and destination", ErrInvalidSystemInstruction)
	}
	sourceAddress := message.AccountKeys[compiledInstruction.AccountIndexes[0]]
	destinationAddress := message.AccountKeys[compiledInstruction.AccountIndexes[1]]
	if !isSignerAddress(sourceAddress, message) {
		return fmt.Errorf("%w: transfer source must sign", ErrMissingRequiredSignature)
	}
	return transferLamports(sourceAddress, destinationAddress, systemInstruction.Transfer.Lamports, accounts, rentConfig)
}

func executeSystemCreateAccount(systemInstruction SystemInstruction, compiledInstruction CompiledInstruction, message ResolvedMessage, accounts map[PublicKey]Account, rentConfig RentConfig) error {
	if len(compiledInstruction.AccountIndexes) < 2 {
		return fmt.Errorf("%w: create account requires payer and new account", ErrInvalidSystemInstruction)
	}
	payerAddress := message.AccountKeys[compiledInstruction.AccountIndexes[0]]
	newAccountAddress := message.AccountKeys[compiledInstruction.AccountIndexes[1]]
	if !isSignerAddress(payerAddress, message) || !isSignerAddress(newAccountAddress, message) {
		return fmt.Errorf("%w: create account requires payer and new account signatures", ErrMissingRequiredSignature)
	}
	if err := transferLamports(payerAddress, newAccountAddress, systemInstruction.CreateAccount.Lamports, accounts, rentConfig); err != nil {
		return err
	}

	newAccount := accounts[newAccountAddress].Clone()
	newAccount.Owner = systemInstruction.CreateAccount.Owner
	newAccount.Data = make([]byte, int(systemInstruction.CreateAccount.Space))
	if err := newAccount.ValidateWithRent(rentConfig); err != nil {
		return err
	}
	accounts[newAccountAddress] = newAccount
	return nil
}

func executeSystemAssign(systemInstruction SystemInstruction, compiledInstruction CompiledInstruction, message ResolvedMessage, accounts map[PublicKey]Account) error {
	if len(compiledInstruction.AccountIndexes) < 1 {
		return fmt.Errorf("%w: assign requires target account", ErrInvalidSystemInstruction)
	}
	targetAddress := message.AccountKeys[compiledInstruction.AccountIndexes[0]]
	if !isSignerAddress(targetAddress, message) {
		return fmt.Errorf("%w: assign target must sign", ErrMissingRequiredSignature)
	}
	account := accounts[targetAddress].Clone()
	account.Owner = systemInstruction.Assign.Owner
	accounts[targetAddress] = account
	return nil
}

func executeSystemAllocate(systemInstruction SystemInstruction, compiledInstruction CompiledInstruction, message ResolvedMessage, accounts map[PublicKey]Account, rentConfig RentConfig) error {
	if len(compiledInstruction.AccountIndexes) < 1 {
		return fmt.Errorf("%w: allocate requires target account", ErrInvalidSystemInstruction)
	}
	targetAddress := message.AccountKeys[compiledInstruction.AccountIndexes[0]]
	if !isSignerAddress(targetAddress, message) {
		return fmt.Errorf("%w: allocate target must sign", ErrMissingRequiredSignature)
	}
	account := accounts[targetAddress].Clone()
	if err := account.ResizeData(int(systemInstruction.Allocate.Space), rentConfig); err != nil {
		return err
	}
	accounts[targetAddress] = account
	return nil
}

func transferLamports(sourceAddress PublicKey, destinationAddress PublicKey, lamports uint64, accounts map[PublicKey]Account, rentConfig RentConfig) error {
	sourceAccount, exists := accounts[sourceAddress]
	if !exists {
		return fmt.Errorf("%w: source account not found", ErrInvalidLoadedTransaction)
	}
	destinationAccount, exists := accounts[destinationAddress]
	if !exists {
		return fmt.Errorf("%w: destination account not found", ErrInvalidLoadedTransaction)
	}
	if err := sourceAccount.DebitLamports(lamports, rentConfig); err != nil {
		return err
	}
	if err := destinationAccount.CreditLamports(lamports); err != nil {
		return err
	}
	if err := destinationAccount.ValidateWithRent(rentConfig); err != nil {
		return err
	}
	accounts[sourceAddress] = sourceAccount
	accounts[destinationAddress] = destinationAccount
	return nil
}

func debitSimulationFee(feePayer PublicKey, totalFee uint64, accounts map[PublicKey]Account, rentConfig RentConfig) error {
	account, exists := accounts[feePayer]
	if !exists {
		return fmt.Errorf("%w: fee payer not found", ErrInvalidLoadedTransaction)
	}
	if err := account.DebitLamports(totalFee, rentConfig); err != nil {
		return err
	}
	accounts[feePayer] = account
	return nil
}

func loadSimulationAccounts(message ResolvedMessage, accountStates map[PublicKey]Account, builtinPrograms BuiltinProgramIDs) ([]LoadedAccount, error) {
	loadedAccounts := make([]LoadedAccount, len(message.AccountKeys))
	staticMetas := message.StaticAccountMetas()
	for accountIndex, accountKey := range message.AccountKeys {
		account, exists := accountStates[accountKey]
		if !exists {
			return nil, fmt.Errorf("%w: account %d is missing", ErrInvalidLoadedTransaction, accountIndex)
		}
		loadedAccounts[accountIndex] = LoadedAccount{
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

func accountMapFromAddressedAccounts(accounts []AddressedAccount) (map[PublicKey]Account, error) {
	accountMap := make(map[PublicKey]Account, len(accounts))
	for accountIndex, addressedAccount := range accounts {
		if _, exists := accountMap[addressedAccount.Address]; exists {
			return nil, fmt.Errorf("%w: duplicate account %d", ErrInvalidLoadedTransaction, accountIndex)
		}
		if err := addressedAccount.Validate(); err != nil {
			return nil, fmt.Errorf("structure: simulation account %d: %w", accountIndex, err)
		}
		accountMap[addressedAccount.Address] = addressedAccount.Account.Clone()
	}
	return accountMap, nil
}

func cloneLoadedAccountsToMap(accounts []LoadedAccount) map[PublicKey]Account {
	accountMap := make(map[PublicKey]Account, len(accounts))
	for _, account := range accounts {
		accountMap[account.Address] = account.Account.Clone()
	}
	return accountMap
}

func balancesFromLoadedAccounts(accounts []LoadedAccount) []uint64 {
	balances := make([]uint64, len(accounts))
	for index, account := range accounts {
		balances[index] = account.Account.Lamports
	}
	return balances
}

func balancesFromMessage(accountKeys []PublicKey, accounts map[PublicKey]Account) []uint64 {
	balances := make([]uint64, len(accountKeys))
	for index, accountKey := range accountKeys {
		balances[index] = accounts[accountKey].Lamports
	}
	return balances
}

func writtenAccountsFromMessage(accountKeys []PublicKey, accounts map[PublicKey]Account) []AddressedAccount {
	writtenAccounts := make([]AddressedAccount, len(accountKeys))
	for index, accountKey := range accountKeys {
		writtenAccounts[index] = AddressedAccount{
			Address: accountKey,
			Account: accounts[accountKey].Clone(),
		}
	}
	return writtenAccounts
}

func writtenAccountsFromLoadedAccounts(accounts []LoadedAccount) []AddressedAccount {
	writtenAccounts := make([]AddressedAccount, len(accounts))
	for index, account := range accounts {
		writtenAccounts[index] = AddressedAccount{
			Address: account.Address,
			Account: account.Account.Clone(),
		}
	}
	return writtenAccounts
}

func isSignerAddress(address PublicKey, message ResolvedMessage) bool {
	requiredSignatures := int(message.Header.NumRequiredSignatures)
	for accountIndex := 0; accountIndex < requiredSignatures && accountIndex < len(message.StaticAccountKeys); accountIndex++ {
		if message.StaticAccountKeys[accountIndex] == address {
			return true
		}
	}
	return false
}

func transactionFailure(code TransactionErrorCode, message string) *TransactionError {
	return &TransactionError{Code: code, Message: message}
}

func instructionFailure(instructionIndex uint16, code InstructionErrorCode, message string) *TransactionError {
	return &TransactionError{
		Code: TransactionErrorCodeInstructionError,
		InstructionError: &InstructionError{
			InstructionIndex: instructionIndex,
			Code:             code,
			Message:          message,
		},
	}
}
