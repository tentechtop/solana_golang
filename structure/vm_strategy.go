package structure

import (
	"bytes"
	"fmt"

	"solana_golang/utils"
	svm "solana_golang/vm"
)

// VirtualMachineInstructionStrategy 执行可执行程序账户 + 作为 Solana SBF 后端接入点。
type VirtualMachineInstructionStrategy struct {
	LoaderProgram PublicKey
	Runtime       svm.Runtime
}

// ProgramID 返回零值 + 该策略只作为 fallback 使用。
func (strategy VirtualMachineInstructionStrategy) ProgramID() PublicKey {
	return PublicKey{}
}

// Execute 执行 VM 程序 + 保持交易格式为 Solana CompiledInstruction。
func (strategy VirtualMachineInstructionStrategy) Execute(context InstructionExecutionContext) error {
	programID, programAccount, err := loadVMProgramAccount(strategy, context)
	if err != nil {
		return err
	}
	vmAccounts, err := buildVMInstructionAccounts(context)
	if err != nil {
		return err
	}
	runtime := strategy.Runtime
	if runtime.LoaderID == (svm.Address{}) {
		runtime = svm.NewRuntime(vmAddressFromPublicKey(strategy.loaderProgram(context.BuiltinPrograms)))
	}
	result, err := runtime.Execute(svm.Invocation{
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
		return fmt.Errorf("structure: execute vm program %s: %w", programID.String(), err)
	}
	return writeVMResultAccounts(result.Accounts, context)
}

func loadVMProgramAccount(strategy VirtualMachineInstructionStrategy, context InstructionExecutionContext) (PublicKey, svm.ProgramAccount, error) {
	if int(context.Instruction.ProgramIDIndex) >= len(context.Message.AccountKeys) {
		return PublicKey{}, svm.ProgramAccount{}, fmt.Errorf("%w: vm program index out of range", ErrInvalidInstruction)
	}
	programID := context.Message.AccountKeys[context.Instruction.ProgramIDIndex]
	account, exists := context.Accounts[programID]
	if !exists {
		return PublicKey{}, svm.ProgramAccount{}, fmt.Errorf("%w: vm program account not found", ErrInvalidLoadedTransaction)
	}
	return programID, svm.ProgramAccount{
		Address:    vmAddressFromPublicKey(programID),
		Owner:      vmAddressFromPublicKey(account.Owner),
		Executable: account.Executable,
		Data:       utils.CloneBytes(account.Data),
	}, nil
}

func buildVMInstructionAccounts(context InstructionExecutionContext) ([]svm.Account, error) {
	accounts := make([]svm.Account, len(context.Instruction.AccountIndexes))
	for index, messageAccountIndex := range context.Instruction.AccountIndexes {
		account, err := buildVMInstructionAccount(context, int(messageAccountIndex))
		if err != nil {
			return nil, fmt.Errorf("structure: vm account %d: %w", index, err)
		}
		accounts[index] = account
	}
	return accounts, nil
}

func buildVMInstructionAccount(context InstructionExecutionContext, messageAccountIndex int) (svm.Account, error) {
	if messageAccountIndex < 0 || messageAccountIndex >= len(context.Message.AccountKeys) {
		return svm.Account{}, fmt.Errorf("%w: vm account index %d out of range", ErrInvalidInstruction, messageAccountIndex)
	}
	address := context.Message.AccountKeys[messageAccountIndex]
	account, exists := context.Accounts[address]
	if !exists {
		return svm.Account{}, fmt.Errorf("%w: vm account %s not found", ErrInvalidLoadedTransaction, address.String())
	}
	return svm.Account{
		Address:    vmAddressFromPublicKey(address),
		Lamports:   account.Lamports,
		Data:       utils.CloneBytes(account.Data),
		Owner:      vmAddressFromPublicKey(account.Owner),
		Executable: account.Executable,
		RentEpoch:  account.RentEpoch,
		IsSigner:   isSignerAddress(address, context.Message),
		IsWritable: isWritableMessageAccount(messageAccountIndex, context.Message),
	}, nil
}

func writeVMResultAccounts(vmAccounts []svm.Account, context InstructionExecutionContext) error {
	for _, vmAccount := range vmAccounts {
		address := publicKeyFromVMAddress(vmAccount.Address)
		account, exists := context.Accounts[address]
		if !exists {
			return fmt.Errorf("%w: vm result account %s not found", ErrInvalidLoadedTransaction, address.String())
		}
		if vmAccount.Owner != vmAddressFromPublicKey(account.Owner) || vmAccount.Executable != account.Executable {
			return fmt.Errorf("%w: vm cannot change owner or executable flag", ErrInvalidInstruction)
		}
		if !bytes.Equal(vmAccount.Data, account.Data) || vmAccount.Lamports != account.Lamports {
			updatedAccount := account.Clone()
			updatedAccount.Lamports = vmAccount.Lamports
			updatedAccount.Data = utils.CloneBytes(vmAccount.Data)
			if err := updatedAccount.ValidateWithRent(context.RentConfig); err != nil {
				return fmt.Errorf("structure: validate vm account writeback: %w", err)
			}
			context.Accounts[address] = updatedAccount
		}
	}
	return nil
}

func (strategy VirtualMachineInstructionStrategy) loaderProgram(programIDs BuiltinProgramIDs) PublicKey {
	if !strategy.LoaderProgram.IsZero() {
		return strategy.LoaderProgram
	}
	return programIDs.BPFLoader
}

func isWritableMessageAccount(accountIndex int, message ResolvedMessage) bool {
	staticMetas := message.StaticAccountMetas()
	if accountIndex < len(staticMetas) {
		return staticMetas[accountIndex].IsWritable
	}
	loadedIndex := accountIndex - len(message.StaticAccountKeys)
	return loadedIndex >= 0 && loadedIndex < len(message.LoadedAddresses.Writable)
}

func vmAddressFromPublicKey(publicKey PublicKey) svm.Address {
	var address svm.Address
	copy(address[:], publicKey[:])
	return address
}

func publicKeyFromVMAddress(address svm.Address) PublicKey {
	var publicKey PublicKey
	copy(publicKey[:], address[:])
	return publicKey
}
