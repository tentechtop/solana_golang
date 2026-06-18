package vmprogram

import (
	"bytes"
	"fmt"

	"solana_golang/runtime"
	"solana_golang/structure"
	"solana_golang/utils"
	svm "solana_golang/vm"
)

// Program 执行可执行程序账户 + 作为未来 Solana SBF 后端接入点。
type Program struct {
	LoaderProgram structure.PublicKey
	Runtime       svm.Runtime
}

// NewProgram 创建 VM 兜底程序 + 未注册固定程序时由 runtime 调用。
func NewProgram(loaderProgram structure.PublicKey, runtimeValue svm.Runtime) Program {
	return Program{LoaderProgram: loaderProgram, Runtime: runtimeValue}
}

// ProgramID 返回零值 + VM 适配器只作为 fallback 使用。
func (program Program) ProgramID() structure.PublicKey {
	return structure.PublicKey{}
}

// Execute 执行 VM 程序 + 保持交易格式为 Solana CompiledInstruction。
func (program Program) Execute(context runtime.InstructionContext) error {
	programID, programAccount, err := loadProgramAccount(program, context)
	if err != nil {
		return err
	}
	return executeProgramAccount(program.Runtime, program.loaderProgram(context.BuiltinPrograms), programID, programAccount, context)
}

func executeProgramAccount(
	runtimeValue svm.Runtime,
	loaderProgram structure.PublicKey,
	programID structure.PublicKey,
	programAccount svm.ProgramAccount,
	context runtime.InstructionContext,
) error {
	vmAccounts, err := buildInstructionAccounts(context)
	if err != nil {
		return err
	}
	if runtimeValue.LoaderID == (svm.Address{}) {
		runtimeValue = svm.NewRuntime(vmAddressFromPublicKey(loaderProgram))
	}
	if err := attachPrivacySyscall(&runtimeValue, context); err != nil {
		return err
	}
	result, err := runtimeValue.Execute(svm.Invocation{
		ProgramID:       vmAddressFromPublicKey(programID),
		ProgramAccount:  programAccount,
		Accounts:        vmAccounts,
		InstructionData: utils.CloneBytes(context.Instruction.Data),
		CurrentSlot:     context.CurrentSlot,
		ComputeLimit:    context.ComputeBudget.MaxComputeUnits,
		Sysvars: svm.Sysvars{
			Clock: svm.ClockSysvar{Slot: context.CurrentSlot},
			Rent: svm.RentSysvar{
				LamportsPerByteYear:        context.RentConfig.LamportsPerByteYear,
				ExemptionThresholdYears:    context.RentConfig.ExemptionThresholdYears,
				AccountStorageOverheadSize: context.RentConfig.AccountStorageOverheadSize,
			},
		},
	})
	if err != nil {
		return fmt.Errorf("vm program: execute %s: %w", programID.String(), err)
	}
	return writeResultAccounts(result.Accounts, context)
}

func loadProgramAccount(program Program, context runtime.InstructionContext) (structure.PublicKey, svm.ProgramAccount, error) {
	if int(context.Instruction.ProgramIDIndex) >= len(context.Message.AccountKeys) {
		return structure.PublicKey{}, svm.ProgramAccount{}, fmt.Errorf("%w: vm program index out of range", structure.ErrInvalidInstruction)
	}
	programID := context.Message.AccountKeys[context.Instruction.ProgramIDIndex]
	account, exists := context.Accounts[programID]
	if !exists {
		return structure.PublicKey{}, svm.ProgramAccount{}, fmt.Errorf("%w: vm program account not found", structure.ErrInvalidLoadedTransaction)
	}
	return programID, svm.ProgramAccount{
		Address:    vmAddressFromPublicKey(programID),
		Owner:      vmAddressFromPublicKey(account.Owner),
		Executable: account.Executable,
		Data:       utils.CloneBytes(account.Data),
	}, nil
}

func buildInstructionAccounts(context runtime.InstructionContext) ([]svm.Account, error) {
	accounts := make([]svm.Account, len(context.Instruction.AccountIndexes))
	for index, messageAccountIndex := range context.Instruction.AccountIndexes {
		account, err := buildInstructionAccount(context, int(messageAccountIndex))
		if err != nil {
			return nil, fmt.Errorf("vm program: account %d: %w", index, err)
		}
		accounts[index] = account
	}
	return accounts, nil
}

func buildInstructionAccount(context runtime.InstructionContext, messageAccountIndex int) (svm.Account, error) {
	if messageAccountIndex < 0 || messageAccountIndex >= len(context.Message.AccountKeys) {
		return svm.Account{}, fmt.Errorf("%w: vm account index %d out of range", structure.ErrInvalidInstruction, messageAccountIndex)
	}
	address := context.Message.AccountKeys[messageAccountIndex]
	account, exists := context.Accounts[address]
	if !exists {
		return svm.Account{}, fmt.Errorf("%w: vm account %s not found", structure.ErrInvalidLoadedTransaction, address.String())
	}
	return svm.Account{
		Address:    vmAddressFromPublicKey(address),
		Lamports:   account.Lamports,
		Data:       utils.CloneBytes(account.Data),
		Owner:      vmAddressFromPublicKey(account.Owner),
		Executable: account.Executable,
		RentEpoch:  account.RentEpoch,
		IsSigner:   runtime.IsSignerAddress(address, context.Message),
		IsWritable: runtime.IsWritableMessageAccount(messageAccountIndex, context.Message),
	}, nil
}

func writeResultAccounts(vmAccounts []svm.Account, context runtime.InstructionContext) error {
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

func vmAddressFromPublicKey(publicKey structure.PublicKey) svm.Address {
	var address svm.Address
	copy(address[:], publicKey[:])
	return address
}

func publicKeyFromVMAddress(address svm.Address) structure.PublicKey {
	var publicKey structure.PublicKey
	copy(publicKey[:], address[:])
	return publicKey
}
