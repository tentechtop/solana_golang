package structure

import (
	"fmt"

	"solana_golang/utils"
)

type TransactionErrorCode uint16

const (
	TransactionErrorCodeNone TransactionErrorCode = iota
	TransactionErrorCodeAccountInUse
	TransactionErrorCodeAccountLoadedTwice
	TransactionErrorCodeAccountNotFound
	TransactionErrorCodeProgramAccountNotFound
	TransactionErrorCodeInsufficientFundsForFee
	TransactionErrorCodeInvalidAccountForFee
	TransactionErrorCodeAlreadyProcessed
	TransactionErrorCodeBlockhashNotFound
	TransactionErrorCodeSignatureFailure
	TransactionErrorCodeInvalidProgramForExecution
	TransactionErrorCodeSanitizeFailure
	TransactionErrorCodeInvalidAddressLookupTable
	TransactionErrorCodeInvalidRentPayingAccount
	TransactionErrorCodeInstructionError
)

// TransactionError 描述交易处理错误 + 保留稳定错误码和可选指令错误。
type TransactionError struct {
	Code             TransactionErrorCode
	Message          string
	InstructionError *InstructionError
}

// InnerInstruction 描述内部调用指令 + 记录顶层指令触发的嵌套调用。
type InnerInstruction struct {
	Index        uint16
	Instructions []CompiledInstruction
}

// TransactionReturnData 描述指令返回数据 + 保存最后一次程序返回的字节。
type TransactionReturnData struct {
	ProgramID PublicKey
	Data      []byte
}

// TokenBalance 描述 token 余额视图 + 为交易元数据保留资产余额变化。
type TokenBalance struct {
	AccountIndex uint8
	Mint         PublicKey
	Owner        PublicKey
	ProgramID    PublicKey
	Amount       string
	Decimals     uint8
}

// ExecutionResult 描述指令执行结果 + 汇总日志、返回数据和计算资源。
type ExecutionResult struct {
	Success              bool
	Error                *InstructionError
	LogMessages          []string
	ReturnData           *TransactionReturnData
	ComputeUnitsConsumed uint64
	CostUnits            uint64
}

// TransactionExecutionResult 描述交易执行结果 + 汇总费用、余额、日志和写集。
type TransactionExecutionResult struct {
	Status               TransactionStatus
	Error                *TransactionError
	FeeDetails           FeeDetails
	PreBalances          []uint64
	PostBalances         []uint64
	PreTokenBalances     []TokenBalance
	PostTokenBalances    []TokenBalance
	InnerInstructions    []InnerInstruction
	LogMessages          []string
	ReturnData           *TransactionReturnData
	ComputeUnitsConsumed uint64
	CostUnits            uint64
	LoadedAddresses      LoadedAddresses
	WrittenAccounts      []AddressedAccount
}

// Error 返回交易错误文本 + 便于错误链和日志输出。
func (transactionError TransactionError) Error() string {
	if transactionError.Message != "" {
		return transactionError.Message
	}
	if transactionError.InstructionError != nil {
		return transactionError.InstructionError.Error()
	}
	return fmt.Sprintf("transaction error code %d", transactionError.Code)
}

// Validate 校验交易错误 + 防止成功码被误写为失败结果。
func (transactionError TransactionError) Validate() error {
	if transactionError.Code == TransactionErrorCodeNone {
		return fmt.Errorf("%w: transaction error code cannot be none", ErrInvalidExecutionResult)
	}
	if transactionError.Code == TransactionErrorCodeInstructionError {
		if transactionError.InstructionError == nil {
			return fmt.Errorf("%w: missing instruction error", ErrInvalidExecutionResult)
		}
		return transactionError.InstructionError.Validate()
	}
	return nil
}

// Clone 深拷贝交易错误 + 保持执行结果不可变。
func (transactionError TransactionError) Clone() *TransactionError {
	cloned := transactionError
	if transactionError.InstructionError != nil {
		cloned.InstructionError = transactionError.InstructionError.Clone()
	}
	return &cloned
}

// Validate 校验内部指令列表 + 防止嵌套指令为空或索引越界。
func (innerInstruction InnerInstruction) Validate(accountCount int) error {
	if len(innerInstruction.Instructions) == 0 {
		return fmt.Errorf("%w: inner instructions cannot be empty", ErrInvalidExecutionResult)
	}
	if len(innerInstruction.Instructions) > MaxInstructionsPerTransaction {
		return fmt.Errorf("%w: inner instruction count %d exceeds %d", ErrInvalidExecutionResult, len(innerInstruction.Instructions), MaxInstructionsPerTransaction)
	}
	for instructionIndex, instruction := range innerInstruction.Instructions {
		if err := validateInstruction(instruction, accountCount); err != nil {
			return fmt.Errorf("structure: inner instruction %d: %w", instructionIndex, err)
		}
	}
	return nil
}

// Clone 深拷贝内部指令 + 避免元数据共享底层数据。
func (innerInstruction InnerInstruction) Clone() InnerInstruction {
	return InnerInstruction{
		Index:        innerInstruction.Index,
		Instructions: cloneInstructions(innerInstruction.Instructions),
	}
}

// Validate 校验返回数据 + 限制返回字节避免元数据膨胀。
func (returnData TransactionReturnData) Validate() error {
	if len(returnData.Data) > MaxInstructionDataSize {
		return fmt.Errorf("%w: return data length %d exceeds %d", ErrInvalidExecutionResult, len(returnData.Data), MaxInstructionDataSize)
	}
	return nil
}

// Clone 深拷贝返回数据 + 防止调用方修改执行元数据。
func (returnData TransactionReturnData) Clone() *TransactionReturnData {
	cloned := TransactionReturnData{
		ProgramID: returnData.ProgramID,
		Data:      utils.CloneBytes(returnData.Data),
	}
	return &cloned
}

// Validate 校验 token 余额视图 + 确保余额文本存在。
func (tokenBalance TokenBalance) Validate() error {
	if tokenBalance.Amount == "" {
		return fmt.Errorf("%w: token amount is empty", ErrInvalidExecutionResult)
	}
	return nil
}

// Validate 校验指令执行结果 + 成功和错误状态必须一致。
func (result ExecutionResult) Validate() error {
	if result.Success && result.Error != nil {
		return fmt.Errorf("%w: success result cannot carry instruction error", ErrInvalidExecutionResult)
	}
	if !result.Success && result.Error == nil {
		return fmt.Errorf("%w: failed result must carry instruction error", ErrInvalidExecutionResult)
	}
	if result.Error != nil {
		return result.Error.Validate()
	}
	if result.ReturnData != nil {
		return result.ReturnData.Validate()
	}
	return nil
}

// Clone 深拷贝指令执行结果 + 避免日志和返回数据被外部修改。
func (result ExecutionResult) Clone() ExecutionResult {
	return ExecutionResult{
		Success:              result.Success,
		Error:                cloneInstructionError(result.Error),
		LogMessages:          cloneStringSlice(result.LogMessages),
		ReturnData:           cloneReturnData(result.ReturnData),
		ComputeUnitsConsumed: result.ComputeUnitsConsumed,
		CostUnits:            result.CostUnits,
	}
}

// Validate 校验交易执行结果 + 保证状态、错误、余额和写集一致。
func (result TransactionExecutionResult) Validate() error {
	if result.Status == TransactionStatusFailed && result.Error == nil {
		return fmt.Errorf("%w: failed transaction must carry error", ErrInvalidExecutionResult)
	}
	if result.Status != TransactionStatusFailed && result.Error != nil {
		return fmt.Errorf("%w: non-failed transaction cannot carry error", ErrInvalidExecutionResult)
	}
	if len(result.PreBalances) != len(result.PostBalances) {
		return fmt.Errorf("%w: pre and post balance lengths differ", ErrInvalidExecutionResult)
	}
	if result.Error != nil {
		if err := result.Error.Validate(); err != nil {
			return err
		}
	}
	if result.ReturnData != nil {
		if err := result.ReturnData.Validate(); err != nil {
			return err
		}
	}
	if err := validateTokenBalances(result.PreTokenBalances); err != nil {
		return err
	}
	if err := validateTokenBalances(result.PostTokenBalances); err != nil {
		return err
	}
	return validateWrittenAccounts(result.WrittenAccounts)
}

// Clone 深拷贝交易执行结果 + 保证区块元数据写入后不可被调用方修改。
func (result TransactionExecutionResult) Clone() TransactionExecutionResult {
	return TransactionExecutionResult{
		Status:               result.Status,
		Error:                cloneTransactionError(result.Error),
		FeeDetails:           result.FeeDetails,
		PreBalances:          cloneUint64Slice(result.PreBalances),
		PostBalances:         cloneUint64Slice(result.PostBalances),
		PreTokenBalances:     cloneTokenBalances(result.PreTokenBalances),
		PostTokenBalances:    cloneTokenBalances(result.PostTokenBalances),
		InnerInstructions:    cloneInnerInstructions(result.InnerInstructions),
		LogMessages:          cloneStringSlice(result.LogMessages),
		ReturnData:           cloneReturnData(result.ReturnData),
		ComputeUnitsConsumed: result.ComputeUnitsConsumed,
		CostUnits:            result.CostUnits,
		LoadedAddresses:      result.LoadedAddresses.Clone(),
		WrittenAccounts:      cloneAddressedAccounts(result.WrittenAccounts),
	}
}

// ToStatusMeta 转换区块交易元数据 + 保持执行结果和区块查询视图字段一致。
func (result TransactionExecutionResult) ToStatusMeta() TransactionStatusMeta {
	errorMessage := ""
	if result.Error != nil {
		errorMessage = result.Error.Error()
	}
	return TransactionStatusMeta{
		Status:               result.Status,
		Err:                  errorMessage,
		Fee:                  result.FeeDetails.TotalFee,
		ComputeUnitsConsumed: result.ComputeUnitsConsumed,
		CostUnits:            result.CostUnits,
		PreBalances:          cloneUint64Slice(result.PreBalances),
		PostBalances:         cloneUint64Slice(result.PostBalances),
		PreTokenBalances:     cloneTokenBalances(result.PreTokenBalances),
		PostTokenBalances:    cloneTokenBalances(result.PostTokenBalances),
		InnerInstructions:    cloneInnerInstructions(result.InnerInstructions),
		LogMessages:          cloneStringSlice(result.LogMessages),
		LoadedAddresses:      result.LoadedAddresses.ToView(),
		ReturnData:           cloneReturnData(result.ReturnData),
	}
}

func validateTokenBalances(tokenBalances []TokenBalance) error {
	for balanceIndex, tokenBalance := range tokenBalances {
		if err := tokenBalance.Validate(); err != nil {
			return fmt.Errorf("structure: token balance %d: %w", balanceIndex, err)
		}
	}
	return nil
}

func validateWrittenAccounts(accounts []AddressedAccount) error {
	for accountIndex, account := range accounts {
		if err := account.Validate(); err != nil {
			return fmt.Errorf("structure: written account %d: %w", accountIndex, err)
		}
	}
	return nil
}

func cloneInstructionError(value *InstructionError) *InstructionError {
	if value == nil {
		return nil
	}
	return value.Clone()
}

func cloneTransactionError(value *TransactionError) *TransactionError {
	if value == nil {
		return nil
	}
	return value.Clone()
}

func cloneReturnData(value *TransactionReturnData) *TransactionReturnData {
	if value == nil {
		return nil
	}
	return value.Clone()
}

func cloneStringSlice(value []string) []string {
	if value == nil {
		return nil
	}
	cloned := make([]string, len(value))
	copy(cloned, value)
	return cloned
}

func cloneUint64Slice(value []uint64) []uint64 {
	if value == nil {
		return nil
	}
	cloned := make([]uint64, len(value))
	copy(cloned, value)
	return cloned
}

func cloneInnerInstructions(value []InnerInstruction) []InnerInstruction {
	if value == nil {
		return nil
	}
	cloned := make([]InnerInstruction, len(value))
	for index, innerInstruction := range value {
		cloned[index] = innerInstruction.Clone()
	}
	return cloned
}

func cloneTokenBalances(value []TokenBalance) []TokenBalance {
	if value == nil {
		return nil
	}
	cloned := make([]TokenBalance, len(value))
	copy(cloned, value)
	return cloned
}

func cloneAddressedAccounts(value []AddressedAccount) []AddressedAccount {
	if value == nil {
		return nil
	}
	cloned := make([]AddressedAccount, len(value))
	for index, account := range value {
		cloned[index] = account.Clone()
	}
	return cloned
}
