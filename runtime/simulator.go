package runtime

import (
	"encoding/binary"
	"fmt"
	"log/slog"

	"github.com/tentechtop/ordexus-core/structure"
)

const (
	SimulationLogInstructionSuccess       = "instruction success"
	SimulationLogTransactionSuccess       = "transaction success"
	stakeInstructionRegisterValidatorType = uint32(0)
)

// TransactionSimulationInput 描述交易模拟输入 + runtime 使用内存账户快照替代数据库读写。
type TransactionSimulationInput struct {
	Transaction     structure.Transaction
	Accounts        []structure.AddressedAccount
	BlockhashQueue  structure.BlockhashQueue
	CurrentSlot     uint64
	CurrentEpoch    uint64
	FeeCalculator   structure.FeeCalculator
	ComputeBudget   structure.ComputeBudgetLimits
	TransactionFee  uint64
	FeeSource       structure.FeePaymentSource
	RentConfig      structure.RentConfig
	BuiltinPrograms structure.BuiltinProgramIDs
	ProgramRegistry ProgramRegistry
	Programs        []Program
	FallbackProgram Program
	ProcessedTxIDs  map[string]struct{}
	Logger          *slog.Logger
}

// TransactionSimulator 执行交易模拟 + 只返回写集不直接提交数据库。
type TransactionSimulator struct{}

// Simulate 执行交易模拟 + 校验签名、blockhash、费用和程序状态转换。
func (simulator TransactionSimulator) Simulate(input TransactionSimulationInput) (structure.TransactionExecutionResult, error) {
	normalizedInput, err := normalizeSimulationInput(input)
	if err != nil {
		result := structure.TransactionExecutionResult{
			Status: structure.TransactionStatusFailed,
			Error:  transactionFailure(structure.TransactionErrorCodeSanitizeFailure, err.Error()),
		}
		return result, result.Validate()
	}
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
	computeUnitsConsumed := uint64(0)
	for instructionIndex, instruction := range loadedTransaction.Message.Instructions {
		normalizedInput.TransactionFee = loadedTransaction.FeeDetails.TotalFee
		normalizedInput.FeeSource = loadedTransaction.FeeSource
		instructionInput := normalizedInput
		instructionInput.ComputeBudget.MaxComputeUnits = remainingComputeUnits(
			normalizedInput.ComputeBudget.MaxComputeUnits,
			computeUnitsConsumed,
		)
		instructionComputeUnits, executionTrace, err := executeSimulatedInstruction(instructionIndex, instruction, loadedTransaction.Message, accountStates, instructionInput, programRegistry)
		result.LogMessages = append(result.LogMessages, executionTrace.LogMessages...)
		if executionTrace.ReturnData != nil {
			result.ReturnData = executionTrace.ReturnData.Clone()
		}
		if len(executionTrace.InnerInstructions) > 0 {
			result.InnerInstructions = append(result.InnerInstructions, structure.InnerInstruction{
				Index:        uint16(instructionIndex),
				Instructions: cloneCompiledInstructions(executionTrace.InnerInstructions),
			})
		}
		var computeBudgetExceeded bool
		computeUnitsConsumed, computeBudgetExceeded = accumulateComputeUnits(
			computeUnitsConsumed,
			instructionComputeUnits,
			normalizedInput.ComputeBudget.MaxComputeUnits,
		)
		if computeBudgetExceeded {
			// Roll back the entire transaction, including earlier successful instructions.
			// Loaded accounts already include the transparent fee debit.
			accountStates = cloneLoadedAccountsToMap(loadedTransaction.Accounts)
			result.Status = structure.TransactionStatusFailed
			result.Error = instructionFailure(
				uint16(instructionIndex),
				structure.InstructionErrorCodeComputationalBudgetExceeded,
				fmt.Sprintf("computational budget exceeded: consumed %d, limit %d", computeUnitsConsumed, normalizedInput.ComputeBudget.MaxComputeUnits),
			)
			result.PostBalances = balancesFromMessage(loadedTransaction.Message.AccountKeys, accountStates)
			result.WrittenAccounts = writtenAccountsFromMessage(loadedTransaction.Message.AccountKeys, accountStates)
			result.ComputeUnitsConsumed = computeUnitsConsumed
			result.CostUnits = computeUnitsConsumed
			return result, result.Validate()
		}
		if err != nil {
			result.Status = structure.TransactionStatusFailed
			result.Error = instructionFailure(uint16(instructionIndex), structure.InstructionErrorCodeGeneric, err.Error())
			accountStates = cloneLoadedAccountsToMap(loadedTransaction.Accounts)
			result.PostBalances = balancesFromMessage(loadedTransaction.Message.AccountKeys, accountStates)
			result.WrittenAccounts = writtenAccountsFromMessage(loadedTransaction.Message.AccountKeys, accountStates)
			result.ComputeUnitsConsumed = computeUnitsConsumed
			result.CostUnits = computeUnitsConsumed
			return result, result.Validate()
		}
		result.LogMessages = append(result.LogMessages, SimulationLogInstructionSuccess)
	}
	if loadedTransaction.FeeSource == structure.FeePaymentSourceShieldedPool {
		if err := debitSimulationFee(loadedTransaction.FeePayer, loadedTransaction.FeeDetails.TotalFee, accountStates, normalizedInput.RentConfig); err != nil {
			accountStates = cloneLoadedAccountsToMap(loadedTransaction.Accounts)
			result.Status = structure.TransactionStatusFailed
			result.Error = transactionFailure(structure.TransactionErrorCodeInsufficientFundsForFee, err.Error())
			result.PostBalances = balancesFromMessage(loadedTransaction.Message.AccountKeys, accountStates)
			result.WrittenAccounts = writtenAccountsFromMessage(loadedTransaction.Message.AccountKeys, accountStates)
			result.ComputeUnitsConsumed = computeUnitsConsumed
			result.CostUnits = computeUnitsConsumed
			return result, result.Validate()
		}
	}

	result.Status = structure.TransactionStatusConfirmed
	result.PostBalances = balancesFromMessage(loadedTransaction.Message.AccountKeys, accountStates)
	result.WrittenAccounts = writtenAccountsFromMessage(loadedTransaction.Message.AccountKeys, accountStates)
	result.LogMessages = append(result.LogMessages, SimulationLogTransactionSuccess)
	result.ComputeUnitsConsumed = computeUnitsConsumed
	result.CostUnits = computeUnitsConsumed
	return result, result.Validate()
}

func normalizeSimulationInput(input TransactionSimulationInput) (TransactionSimulationInput, error) {
	if input.FeeCalculator == (structure.FeeCalculator{}) {
		input.FeeCalculator = structure.DefaultFeeCalculator()
	}
	if input.RentConfig == (structure.RentConfig{}) {
		input.RentConfig = structure.DefaultRentConfig
	}
	if input.BuiltinPrograms == (structure.BuiltinProgramIDs{}) {
		input.BuiltinPrograms = structure.DefaultBuiltinProgramIDs
	}
	estimatedBudget, err := structure.EstimateTransactionComputeBudget(input.Transaction, input.BuiltinPrograms)
	if err != nil {
		return TransactionSimulationInput{}, fmt.Errorf("runtime: estimate transaction compute budget: %w", err)
	}
	if input.ComputeBudget == (structure.ComputeBudgetLimits{}) {
		input.ComputeBudget = estimatedBudget
	} else {
		if err := input.ComputeBudget.Validate(); err != nil {
			return TransactionSimulationInput{}, fmt.Errorf("runtime: validate transaction compute budget: %w", err)
		}
		if input.ComputeBudget.MaxComputeUnits < estimatedBudget.MaxComputeUnits {
			return TransactionSimulationInput{}, fmt.Errorf(
				"runtime: declared compute budget %d is below estimated demand %d",
				input.ComputeBudget.MaxComputeUnits,
				estimatedBudget.MaxComputeUnits,
			)
		}
	}
	return input, nil
}

// accumulateComputeUnits 累计交易计算量 + 使用饱和结果避免恶意程序上报值触发 uint64 回绕。
func accumulateComputeUnits(consumed uint64, instructionUnits uint64, limit uint64) (uint64, bool) {
	if instructionUnits > ^uint64(0)-consumed {
		return ^uint64(0), true
	}
	total := consumed + instructionUnits
	return total, total > limit
}

// remainingComputeUnits 返回当前指令可用余额 + 沙箱据此在提交账户变更前拒绝超限执行。
func remainingComputeUnits(limit uint64, consumed uint64) uint64 {
	if consumed >= limit {
		return 0
	}
	return limit - consumed
}

func newSimulationProgramRegistry(input TransactionSimulationInput) (ProgramRegistry, error) {
	registry := input.ProgramRegistry.Clone()
	if input.FallbackProgram != nil {
		if err := registry.SetFallbackProgram(input.FallbackProgram); err != nil {
			return ProgramRegistry{}, err
		}
	}
	for _, program := range input.Programs {
		if err := registry.RegisterProgram(program); err != nil {
			return ProgramRegistry{}, err
		}
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
	transactionID, err := input.Transaction.TxIDString()
	if err != nil {
		result.Status = structure.TransactionStatusFailed
		result.Error = transactionFailure(structure.TransactionErrorCodeSignatureFailure, err.Error())
		return structure.LoadedTransaction{}, result, result.Validate()
	}
	if _, exists := input.ProcessedTxIDs[transactionID]; exists {
		result.Status = structure.TransactionStatusFailed
		result.Error = transactionFailure(structure.TransactionErrorCodeAlreadyProcessed, "transaction already processed")
		return structure.LoadedTransaction{}, result, result.Validate()
	}

	message, err := input.Transaction.OrdexusMessage()
	if err != nil {
		result.Status = structure.TransactionStatusFailed
		result.Error = transactionFailure(structure.TransactionErrorCodeSanitizeFailure, err.Error())
		return structure.LoadedTransaction{}, result, result.Validate()
	}
	if message.UsesAddressTable || len(message.AddressTableLookups) != 0 {
		// runtime 尚未接入地址表状态解析，共识执行必须安全拒绝而不能用空加载地址继续。
		result.Status = structure.TransactionStatusFailed
		result.Error = transactionFailure(structure.TransactionErrorCodeInvalidAddressLookupTable, "address table transactions are not supported by runtime")
		return structure.LoadedTransaction{}, result, result.Validate()
	}
	if err := validateTransactionAuthorization(input.Transaction, message, input.BuiltinPrograms); err != nil {
		result.Status = structure.TransactionStatusFailed
		result.Error = transactionFailure(structure.TransactionErrorCodeSignatureFailure, err.Error())
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
	addMissingAccountPlaceholders(resolvedMessage, accountStates, input.BuiltinPrograms)
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
	if loadedTransaction.FeeSource == structure.FeePaymentSourceTransparentAccount {
		if err := debitSimulationFee(loadedTransaction.FeePayer, feeDetails.TotalFee, accountStates, input.RentConfig); err != nil {
			result.Status = structure.TransactionStatusFailed
			result.Error = transactionFailure(structure.TransactionErrorCodeInsufficientFundsForFee, err.Error())
			result.PostBalances = balancesFromMessage(resolvedMessage.AccountKeys, accountStates)
			result.WrittenAccounts = writtenAccountsFromMessage(resolvedMessage.AccountKeys, accountStates)
			return structure.LoadedTransaction{}, result, result.Validate()
		}
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

func validateTransactionAuthorization(transaction structure.Transaction, message structure.OrdexusMessage, builtinPrograms structure.BuiltinProgramIDs) error {
	if message.Header.NumRequiredSignatures > 0 {
		if message.Header.NumReadonlySignedAccounts == message.Header.NumRequiredSignatures {
			return fmt.Errorf("%w: transparent fee payer must be writable", structure.ErrMissingWritableSigner)
		}
		return nil
	}
	if len(transaction.Signatures) != 0 {
		return fmt.Errorf("%w: unsigned message cannot carry signatures", structure.ErrInvalidTransactionEncoding)
	}
	if len(message.Instructions) != 1 {
		return fmt.Errorf("%w: shielded authorization requires exactly one instruction", structure.ErrMissingRequiredSignature)
	}
	instruction := message.Instructions[0]
	if int(instruction.ProgramIDIndex) >= len(message.AccountKeys) || message.AccountKeys[instruction.ProgramIDIndex] != builtinPrograms.Shielded {
		return fmt.Errorf("%w: unsigned transaction must target the shielded program", structure.ErrMissingRequiredSignature)
	}
	if len(instruction.AccountIndexes) == 0 || instruction.AccountIndexes[0] != 0 {
		return fmt.Errorf("%w: shielded pool must be the first instruction account", structure.ErrInvalidInstruction)
	}
	metas := message.StaticAccountMetas()
	if len(metas) == 0 || !metas[0].IsWritable {
		return fmt.Errorf("%w: shielded pool must be writable", structure.ErrInvalidInstruction)
	}
	return nil
}

// addMissingAccountPlaceholders 补齐可创建账户占位 + 指令执行前必须先完成账户加载。
func addMissingAccountPlaceholders(message structure.ResolvedMessage, accounts map[structure.PublicKey]structure.Account, builtinPrograms structure.BuiltinProgramIDs) {
	addMissingBuiltinProgramPlaceholders(message, accounts, builtinPrograms)
	for _, instruction := range message.Instructions {
		if int(instruction.ProgramIDIndex) >= len(message.AccountKeys) {
			continue
		}
		programID := message.AccountKeys[instruction.ProgramIDIndex]
		if programID == builtinPrograms.System {
			addMissingSystemAccountPlaceholder(instruction, message, accounts, builtinPrograms)
			continue
		}
		if programID == builtinPrograms.Stake {
			addMissingStakeAccountPlaceholder(instruction, message, accounts, builtinPrograms)
			continue
		}
		if programID == builtinPrograms.Shielded {
			addMissingShieldedAccountPlaceholders(instruction, message, accounts, builtinPrograms)
		}
	}
}

func addMissingBuiltinProgramPlaceholders(
	message structure.ResolvedMessage,
	accounts map[structure.PublicKey]structure.Account,
	builtinPrograms structure.BuiltinProgramIDs,
) {
	for _, accountKey := range message.AccountKeys {
		if !builtinPrograms.IsBuiltinProgram(accountKey) {
			continue
		}
		if _, exists := accounts[accountKey]; exists {
			continue
		}
		accounts[accountKey] = structure.Account{Owner: builtinPrograms.NativeLoader}
	}
}

func addMissingSystemAccountPlaceholder(
	instruction structure.CompiledInstruction,
	message structure.ResolvedMessage,
	accounts map[structure.PublicKey]structure.Account,
	builtinPrograms structure.BuiltinProgramIDs,
) {
	if len(instruction.AccountIndexes) < 2 {
		return
	}
	systemInstruction, err := structure.UnmarshalSystemInstructionBinary(instruction.Data)
	if err != nil {
		return
	}
	if !systemInstructionCreatesOrFundsDestination(systemInstruction.Type) {
		return
	}
	addMissingWritableSystemAccount(message.AccountKeys[instruction.AccountIndexes[1]], accounts, builtinPrograms)
}

func systemInstructionCreatesOrFundsDestination(instructionType structure.SystemInstructionType) bool {
	return instructionType == structure.SystemInstructionCreateAccount || instructionType == structure.SystemInstructionTransfer
}

func addMissingStakeAccountPlaceholder(
	instruction structure.CompiledInstruction,
	message structure.ResolvedMessage,
	accounts map[structure.PublicKey]structure.Account,
	builtinPrograms structure.BuiltinProgramIDs,
) {
	if len(instruction.AccountIndexes) < 2 || !isStakeRegisterValidatorInstruction(instruction.Data) {
		return
	}
	addMissingWritableSystemAccount(message.AccountKeys[instruction.AccountIndexes[1]], accounts, builtinPrograms)
}

// isStakeRegisterValidatorInstruction 识别 stake 注册 wire type + 避免 runtime 反向 import stake 程序造成循环依赖。
func isStakeRegisterValidatorInstruction(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	return binary.LittleEndian.Uint32(data[:4]) == stakeInstructionRegisterValidatorType
}

func addMissingShieldedAccountPlaceholders(
	instruction structure.CompiledInstruction,
	message structure.ResolvedMessage,
	accounts map[structure.PublicKey]structure.Account,
	builtinPrograms structure.BuiltinProgramIDs,
) {
	shieldedInstruction, err := structure.UnmarshalShieldedTransferInstructionBinary(instruction.Data)
	if err != nil || len(instruction.AccountIndexes) == 0 {
		return
	}
	cursor := 1
	if shieldedInstruction.TransparentInputAmount > 0 {
		cursor++
	}
	if shieldedInstruction.TransparentOutputAmount > 0 {
		if cursor >= len(instruction.AccountIndexes) {
			return
		}
		destinationIndex := instruction.AccountIndexes[cursor]
		if int(destinationIndex) < len(message.AccountKeys) {
			addMissingWritableSystemAccount(message.AccountKeys[destinationIndex], accounts, builtinPrograms)
		}
		cursor++
	}
	for _, action := range shieldedInstruction.Bundle.Actions {
		if !action.EnabledSpend {
			continue
		}
		if cursor >= len(instruction.AccountIndexes) {
			return
		}
		markerIndex := instruction.AccountIndexes[cursor]
		if int(markerIndex) < len(message.AccountKeys) {
			addMissingWritableSystemAccount(message.AccountKeys[markerIndex], accounts, builtinPrograms)
		}
		cursor++
	}
}

func addMissingWritableSystemAccount(address structure.PublicKey, accounts map[structure.PublicKey]structure.Account, builtinPrograms structure.BuiltinProgramIDs) {
	if _, exists := accounts[address]; exists {
		return
	}
	accounts[address] = structure.Account{Owner: builtinPrograms.System}
}

func executeSimulatedInstruction(
	instructionIndex int,
	instruction structure.CompiledInstruction,
	message structure.ResolvedMessage,
	accounts map[structure.PublicKey]structure.Account,
	input TransactionSimulationInput,
	registry ProgramRegistry,
) (uint64, ProgramExecutionTrace, error) {
	return ExecuteInstructionSandbox(instructionIndex, instruction, message, accounts, input, registry)
}

func cloneCompiledInstructions(instructions []structure.CompiledInstruction) []structure.CompiledInstruction {
	cloned := make([]structure.CompiledInstruction, len(instructions))
	for index, instruction := range instructions {
		cloned[index] = instruction.Clone()
	}
	return cloned
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
