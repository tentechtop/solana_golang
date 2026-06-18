package vmprogram

import (
	"fmt"

	privacyprogram "solana_golang/programs/privacy"
	"solana_golang/runtime"
	"solana_golang/structure"
	svm "solana_golang/vm"
)

const privacyExecuteSyscallCost = uint64(5_000)

// attachPrivacySyscall 注入隐私业务 syscall + 让 VM 字节码复用固定 privacy program。
func attachPrivacySyscall(runtimeValue *svm.Runtime, context runtime.InstructionContext) error {
	if runtimeValue == nil {
		return fmt.Errorf("vm program: runtime is nil")
	}
	registry := runtimeValue.Syscalls
	if registry.IsZero() {
		registry = svm.DefaultSyscallRegistry()
	}
	extendedRegistry, err := registry.With(svm.Syscall{
		ID:      svm.SyscallPrivacyExecute,
		Name:    "privacy_execute",
		Cost:    privacyExecuteSyscallCost,
		Handler: privacyExecuteSyscall(context),
	})
	if err != nil {
		return fmt.Errorf("vm program: attach privacy syscall: %w", err)
	}
	runtimeValue.Syscalls = extendedRegistry
	return nil
}

// privacyExecuteSyscall 执行固定隐私程序 + 成功后才把业务账户同步回 VM 和外层上下文。
func privacyExecuteSyscall(outerContext runtime.InstructionContext) svm.SyscallFunc {
	return func(vmContext *svm.Context, input []byte) ([]byte, error) {
		if vmContext == nil || vmContext.Accounts == nil {
			return nil, fmt.Errorf("privacy syscall: vm context is nil")
		}
		if len(input) != 0 {
			return nil, fmt.Errorf("privacy syscall: input must be empty")
		}

		privacyProgramID := privacyProgramIDForContext(outerContext)
		workingAccounts := cloneStructureAccounts(outerContext.Accounts)
		nestedContext, err := buildPrivacyInstructionContext(outerContext, workingAccounts, privacyProgramID)
		if err != nil {
			return nil, err
		}
		if err := privacyprogram.NewProgram(privacyProgramID).Execute(nestedContext); err != nil {
			return nil, err
		}
		if err := commitPrivacySyscallAccounts(vmContext, outerContext, workingAccounts); err != nil {
			return nil, err
		}
		return nil, nil
	}
}

func buildPrivacyInstructionContext(
	outerContext runtime.InstructionContext,
	workingAccounts map[structure.PublicKey]structure.Account,
	privacyProgramID structure.PublicKey,
) (runtime.InstructionContext, error) {
	message, programIDIndex, err := privacyNestedMessage(outerContext.Message, privacyProgramID)
	if err != nil {
		return runtime.InstructionContext{}, err
	}
	instruction := outerContext.Instruction.Clone()
	instruction.ProgramIDIndex = programIDIndex
	return runtime.InstructionContext{
		InstructionIndex: outerContext.InstructionIndex,
		Instruction:      instruction,
		Message:          message,
		Accounts:         workingAccounts,
		CurrentSlot:      outerContext.CurrentSlot,
		CurrentEpoch:     outerContext.CurrentEpoch,
		RentConfig:       outerContext.RentConfig,
		ComputeBudget:    outerContext.ComputeBudget,
		BuiltinPrograms:  privacyBuiltinPrograms(outerContext.BuiltinPrograms, privacyProgramID),
		Logger:           outerContext.Logger,
	}, nil
}

func privacyNestedMessage(message structure.ResolvedMessage, privacyProgramID structure.PublicKey) (structure.ResolvedMessage, uint8, error) {
	if privacyProgramID.IsZero() {
		return structure.ResolvedMessage{}, 0, fmt.Errorf("%w: privacy program id is empty", structure.ErrInvalidInstruction)
	}
	nextMessage := message.Clone()
	if len(nextMessage.AccountKeys) == 0 {
		return structure.ResolvedMessage{}, 0, fmt.Errorf("%w: empty account keys", structure.ErrInvalidInstruction)
	}
	if len(nextMessage.StaticAccountKeys) == 0 {
		nextMessage.StaticAccountKeys = clonePublicKeys(nextMessage.AccountKeys)
		nextMessage.LoadedAddresses = structure.LoadedAddresses{}
	}
	for accountIndex, accountKey := range nextMessage.AccountKeys {
		if accountKey == privacyProgramID {
			return nextMessage, uint8(accountIndex), nil
		}
	}
	if len(nextMessage.AccountKeys) >= structure.MaxAccountsPerTransaction {
		return structure.ResolvedMessage{}, 0, fmt.Errorf("%w: account key count exceeds %d", structure.ErrInvalidInstruction, structure.MaxAccountsPerTransaction)
	}
	nextMessage.AccountKeys = append(nextMessage.AccountKeys, privacyProgramID)
	nextMessage.LoadedAddresses.Readonly = append(nextMessage.LoadedAddresses.Readonly, privacyProgramID)
	return nextMessage, uint8(len(nextMessage.AccountKeys) - 1), nil
}

func commitPrivacySyscallAccounts(
	vmContext *svm.Context,
	outerContext runtime.InstructionContext,
	workingAccounts map[structure.PublicKey]structure.Account,
) error {
	vmAccounts := make([]svm.Account, len(outerContext.Instruction.AccountIndexes))
	for accountIndex, messageAccountIndex := range outerContext.Instruction.AccountIndexes {
		account, err := buildInstructionAccount(
			runtime.InstructionContext{Message: outerContext.Message, Accounts: workingAccounts},
			int(messageAccountIndex),
		)
		if err != nil {
			return fmt.Errorf("privacy syscall: build vm account %d: %w", accountIndex, err)
		}
		vmAccounts[accountIndex] = account
	}
	for accountIndex, vmAccount := range vmAccounts {
		if err := vmContext.Accounts.SetAccount(accountIndex, vmAccount); err != nil {
			return fmt.Errorf("privacy syscall: sync vm account %d: %w", accountIndex, err)
		}
	}
	for _, messageAccountIndex := range outerContext.Instruction.AccountIndexes {
		address := outerContext.Message.AccountKeys[messageAccountIndex]
		outerContext.Accounts[address] = workingAccounts[address].Clone()
	}
	return nil
}

func privacyProgramIDForContext(context runtime.InstructionContext) structure.PublicKey {
	if !context.BuiltinPrograms.Privacy.IsZero() {
		return context.BuiltinPrograms.Privacy
	}
	return structure.DefaultBuiltinProgramIDs.Privacy
}

func privacyBuiltinPrograms(programIDs structure.BuiltinProgramIDs, privacyProgramID structure.PublicKey) structure.BuiltinProgramIDs {
	if programIDs == (structure.BuiltinProgramIDs{}) {
		programIDs = structure.DefaultBuiltinProgramIDs
	}
	if programIDs.Privacy.IsZero() {
		programIDs.Privacy = privacyProgramID
	}
	return programIDs
}

func cloneStructureAccounts(accounts map[structure.PublicKey]structure.Account) map[structure.PublicKey]structure.Account {
	clonedAccounts := make(map[structure.PublicKey]structure.Account, len(accounts))
	for address, account := range accounts {
		clonedAccounts[address] = account.Clone()
	}
	return clonedAccounts
}

func clonePublicKeys(publicKeys []structure.PublicKey) []structure.PublicKey {
	if publicKeys == nil {
		return nil
	}
	clonedKeys := make([]structure.PublicKey, len(publicKeys))
	copy(clonedKeys, publicKeys)
	return clonedKeys
}
