package vmprogram

import (
	"bytes"
	"fmt"

	"github.com/tentechtop/ordexus-core/runtime"
	"github.com/tentechtop/ordexus-core/structure"
	"github.com/tentechtop/ordexus-core/utils"
	ovm "github.com/tentechtop/ordexus-core/vm"
)

// Program 执行可执行程序账户 + 作为未来 Ordexus SBF 后端接入点。
type Program struct {
	LoaderProgram structure.PublicKey
	Runtime       ovm.Runtime
	InvokeProgram runtime.ProgramHandler
}

// NewProgram 创建 VM 兜底程序 + 未注册固定程序时由 runtime 调用。
func NewProgram(loaderProgram structure.PublicKey, runtimeValue ovm.Runtime) Program {
	return Program{LoaderProgram: loaderProgram, Runtime: runtimeValue}
}

// NewProgramWithInvoker 创建支持 CPI 的 VM 适配器 + 嵌套调用继续走节点统一程序注册表。
func NewProgramWithInvoker(loaderProgram structure.PublicKey, runtimeValue ovm.Runtime, invokeProgram runtime.ProgramHandler) Program {
	return Program{LoaderProgram: loaderProgram, Runtime: runtimeValue, InvokeProgram: invokeProgram}
}

// ProgramID 返回零值 + VM 适配器只作为 fallback 使用。
func (program Program) ProgramID() structure.PublicKey {
	return structure.PublicKey{}
}

// Execute 执行 VM 程序 + 保持交易格式为 Ordexus CompiledInstruction。
func (program Program) Execute(context runtime.InstructionContext) error {
	programID, programAccount, err := loadProgramAccount(program, context)
	if err != nil {
		return err
	}
	return executeProgramAccount(program.Runtime, program.loaderProgram(context.BuiltinPrograms), program.InvokeProgram, programID, programAccount, context)
}

func executeProgramAccount(
	runtimeValue ovm.Runtime,
	loaderProgram structure.PublicKey,
	invokeProgram runtime.ProgramHandler,
	programID structure.PublicKey,
	programAccount ovm.ProgramAccount,
	context runtime.InstructionContext,
) error {
	vmAccounts, err := buildInstructionAccounts(context)
	if err != nil {
		return err
	}
	if runtimeValue.LoaderID == (ovm.Address{}) {
		runtimeValue = ovm.NewRuntime(vmAddressFromPublicKey(loaderProgram))
	}
	if err := attachStakePoolSyscall(&runtimeValue, context); err != nil {
		return err
	}
	if err := attachAssetSyscall(&runtimeValue, context); err != nil {
		return err
	}
	if invokeProgram != nil {
		runtimeValue.CPI = newContractCPIRuntime(context, invokeProgram)
	}
	result, err := runtimeValue.Execute(ovm.Invocation{
		ProgramID:       vmAddressFromPublicKey(programID),
		ProgramAccount:  programAccount,
		Accounts:        vmAccounts,
		InstructionData: utils.CloneBytes(context.Instruction.Data),
		CurrentSlot:     context.CurrentSlot,
		ComputeLimit:    context.ComputeBudget.MaxComputeUnits,
		Sysvars: ovm.Sysvars{
			Clock: ovm.ClockSysvar{Slot: context.CurrentSlot},
			Rent: ovm.RentSysvar{
				LamportsPerByteYear:        context.RentConfig.LamportsPerByteYear,
				ExemptionThresholdYears:    context.RentConfig.ExemptionThresholdYears,
				AccountStorageOverheadSize: context.RentConfig.AccountStorageOverheadSize,
			},
		},
	})
	if err != nil {
		return fmt.Errorf("vm program: execute %s: %w", programID.String(), err)
	}
	if context.ComputeUnitsUsed != nil {
		*context.ComputeUnitsUsed += result.UnitsConsumed
	}
	appendVMExecutionTrace(context.ExecutionTrace, result)
	return writeResultAccounts(result.Accounts, context)
}

func appendVMExecutionTrace(trace *runtime.ProgramExecutionTrace, result ovm.Result) {
	if trace == nil {
		return
	}
	trace.LogMessages = append(trace.LogMessages, result.Logs...)
	if result.ReturnData == nil {
		return
	}
	trace.ReturnData = &structure.TransactionReturnData{
		ProgramID: publicKeyFromVMAddress(result.ReturnData.ProgramID),
		Data:      utils.CloneBytes(result.ReturnData.Data),
	}
}

func loadProgramAccount(program Program, context runtime.InstructionContext) (structure.PublicKey, ovm.ProgramAccount, error) {
	if int(context.Instruction.ProgramIDIndex) >= len(context.Message.AccountKeys) {
		return structure.PublicKey{}, ovm.ProgramAccount{}, fmt.Errorf("%w: vm program index out of range", structure.ErrInvalidInstruction)
	}
	programID := context.Message.AccountKeys[context.Instruction.ProgramIDIndex]
	account, exists := context.Accounts[programID]
	if !exists {
		return structure.PublicKey{}, ovm.ProgramAccount{}, fmt.Errorf("%w: vm program account not found", structure.ErrInvalidLoadedTransaction)
	}
	return programID, ovm.ProgramAccount{
		Address:    vmAddressFromPublicKey(programID),
		Owner:      vmAddressFromPublicKey(account.Owner),
		Executable: account.Executable,
		Data:       utils.CloneBytes(account.Data),
	}, nil
}

func buildInstructionAccounts(context runtime.InstructionContext) ([]ovm.Account, error) {
	accounts := make([]ovm.Account, len(context.Instruction.AccountIndexes))
	for index, messageAccountIndex := range context.Instruction.AccountIndexes {
		account, err := buildInstructionAccount(context, int(messageAccountIndex))
		if err != nil {
			return nil, fmt.Errorf("vm program: account %d: %w", index, err)
		}
		accounts[index] = account
	}
	return accounts, nil
}

func buildInstructionAccount(context runtime.InstructionContext, messageAccountIndex int) (ovm.Account, error) {
	if messageAccountIndex < 0 || messageAccountIndex >= len(context.Message.AccountKeys) {
		return ovm.Account{}, fmt.Errorf("%w: vm account index %d out of range", structure.ErrInvalidInstruction, messageAccountIndex)
	}
	address := context.Message.AccountKeys[messageAccountIndex]
	account, exists := context.Accounts[address]
	if !exists {
		return ovm.Account{}, fmt.Errorf("%w: vm account %s not found", structure.ErrInvalidLoadedTransaction, address.String())
	}
	return ovm.Account{
		Address:    vmAddressFromPublicKey(address),
		Lamports:   account.Lamports,
		Data:       utils.CloneBytes(account.Data),
		Owner:      vmAddressFromPublicKey(account.Owner),
		Executable: account.Executable,
		RentEpoch:  account.RentEpoch,
		IsSigner:   runtime.IsSignerContextAddress(address, context),
		IsWritable: runtime.IsWritableMessageAccount(messageAccountIndex, context.Message),
	}, nil
}

func writeResultAccounts(vmAccounts []ovm.Account, context runtime.InstructionContext) error {
	for _, vmAccount := range vmAccounts {
		address := publicKeyFromVMAddress(vmAccount.Address)
		account, exists := context.Accounts[address]
		if !exists {
			return fmt.Errorf("%w: vm result account %s not found", structure.ErrInvalidLoadedTransaction, address.String())
		}
		if vmAccount.Owner != vmAddressFromPublicKey(account.Owner) || vmAccount.Executable != account.Executable {
			return fmt.Errorf("%w: vm cannot change owner or executable flag", structure.ErrInvalidInstruction)
		}
		if !bytes.Equal(vmAccount.Data, account.Data) || vmAccount.Lamports != account.Lamports {
			updatedAccount := account.Clone()
			updatedAccount.Lamports = vmAccount.Lamports
			updatedAccount.Data = utils.CloneBytes(vmAccount.Data)
			if err := updatedAccount.ValidateWithRent(context.RentConfig); err != nil {
				return fmt.Errorf("vm program: validate account writeback: %w", err)
			}
			context.Accounts[address] = updatedAccount
		}
	}
	return nil
}

func (program Program) loaderProgram(programIDs structure.BuiltinProgramIDs) structure.PublicKey {
	if !program.LoaderProgram.IsZero() {
		return program.LoaderProgram
	}
	return programIDs.BPFLoader
}

func vmAddressFromPublicKey(publicKey structure.PublicKey) ovm.Address {
	var address ovm.Address
	copy(address[:], publicKey[:])
	return address
}

func publicKeyFromVMAddress(address ovm.Address) structure.PublicKey {
	var publicKey structure.PublicKey
	copy(publicKey[:], address[:])
	return publicKey
}
