package runtime

import (
	"bytes"
	"fmt"

	"solana_golang/structure"
)

// ExecuteInstructionSandbox 执行单条指令沙箱 + 失败或越权时丢弃临时状态防止污染交易。
func ExecuteInstructionSandbox(
	instructionIndex int,
	instruction structure.CompiledInstruction,
	message structure.ResolvedMessage,
	accounts map[structure.PublicKey]structure.Account,
	input TransactionSimulationInput,
	registry ProgramRegistry,
) (uint64, error) {
	sandboxAccounts := cloneAccountMap(accounts)
	computeUnitsUsed := uint64(0)
	if err := registry.Execute(InstructionContext{
		InstructionIndex: instructionIndex,
		Instruction:      instruction,
		Message:          message,
		Accounts:         sandboxAccounts,
		CurrentSlot:      input.CurrentSlot,
		CurrentEpoch:     input.CurrentEpoch,
		RentConfig:       input.RentConfig,
		ComputeBudget:    input.ComputeBudget,
		BuiltinPrograms:  input.BuiltinPrograms,
		Logger:           input.Logger,
		ComputeUnitsUsed: &computeUnitsUsed,
	}); err != nil {
		return normalizeInstructionComputeUnits(computeUnitsUsed), err
	}
	if err := ValidateInstructionWrites(instruction, message, accounts, sandboxAccounts, input.BuiltinPrograms); err != nil {
		return normalizeInstructionComputeUnits(computeUnitsUsed), err
	}
	replaceAccountMap(accounts, sandboxAccounts)
	return normalizeInstructionComputeUnits(computeUnitsUsed), nil
}

// ValidateInstructionWrites 校验指令写集 + runtime 统一拦截未声明 writable 的账户变更。
func ValidateInstructionWrites(
	instruction structure.CompiledInstruction,
	message structure.ResolvedMessage,
	before map[structure.PublicKey]structure.Account,
	after map[structure.PublicKey]structure.Account,
	programIDs structure.BuiltinProgramIDs,
) error {
	programID, err := instructionProgramID(instruction, message)
	if err != nil {
		return err
	}
	writableAccounts := writableAccountSet(message)
	instructionAccounts := instructionAccountSet(instruction, message)
	for address, afterAccount := range after {
		beforeAccount, exists := before[address]
		if !exists {
			if _, writable := writableAccounts[address]; !writable {
				return fmt.Errorf("%w: created account %s is not writable", structure.ErrInvalidInstruction, address.String())
			}
			continue
		}
		if accountsEqual(beforeAccount, afterAccount) {
			continue
		}
		if _, writable := writableAccounts[address]; !writable {
			return fmt.Errorf("%w: account %s changed without writable permission", structure.ErrInvalidInstruction, address.String())
		}
		if _, included := instructionAccounts[address]; !included {
			return fmt.Errorf("%w: account %s changed outside instruction accounts", structure.ErrInvalidInstruction, address.String())
		}
		if err := validateProgramOwnerWrite(programID, address, beforeAccount, afterAccount, programIDs); err != nil {
			return err
		}
	}
	return nil
}

func validateProgramOwnerWrite(
	programID structure.PublicKey,
	address structure.PublicKey,
	before structure.Account,
	after structure.Account,
	programIDs structure.BuiltinProgramIDs,
) error {
	if programID != programIDs.Token {
		return nil
	}
	if before.Owner != programID || after.Owner != programID {
		return fmt.Errorf("%w: token program cannot modify non-token account %s", structure.ErrInvalidInstruction, address.String())
	}
	if before.Executable != after.Executable {
		return fmt.Errorf("%w: token program cannot change executable flag", structure.ErrInvalidInstruction)
	}
	if before.Owner != after.Owner {
		return fmt.Errorf("%w: token program cannot change owner", structure.ErrInvalidInstruction)
	}
	return nil
}

func instructionProgramID(instruction structure.CompiledInstruction, message structure.ResolvedMessage) (structure.PublicKey, error) {
	if int(instruction.ProgramIDIndex) >= len(message.AccountKeys) {
		return structure.PublicKey{}, fmt.Errorf("%w: program id index out of range", structure.ErrInvalidInstruction)
	}
	return message.AccountKeys[instruction.ProgramIDIndex], nil
}

func writableAccountSet(message structure.ResolvedMessage) map[structure.PublicKey]struct{} {
	accounts := make(map[structure.PublicKey]struct{}, len(message.AccountKeys))
	for accountIndex, accountKey := range message.AccountKeys {
		if IsWritableMessageAccount(accountIndex, message) {
			accounts[accountKey] = struct{}{}
		}
	}
	return accounts
}

func instructionAccountSet(instruction structure.CompiledInstruction, message structure.ResolvedMessage) map[structure.PublicKey]struct{} {
	accounts := make(map[structure.PublicKey]struct{}, len(instruction.AccountIndexes))
	for _, accountIndex := range instruction.AccountIndexes {
		if int(accountIndex) >= len(message.AccountKeys) {
			continue
		}
		accounts[message.AccountKeys[accountIndex]] = struct{}{}
	}
	return accounts
}

func cloneAccountMap(accounts map[structure.PublicKey]structure.Account) map[structure.PublicKey]structure.Account {
	cloned := make(map[structure.PublicKey]structure.Account, len(accounts))
	for address, account := range accounts {
		cloned[address] = account.Clone()
	}
	return cloned
}

func replaceAccountMap(target map[structure.PublicKey]structure.Account, source map[structure.PublicKey]structure.Account) {
	for address := range target {
		delete(target, address)
	}
	for address, account := range source {
		target[address] = account.Clone()
	}
}

func accountsEqual(left structure.Account, right structure.Account) bool {
	return left.Lamports == right.Lamports &&
		left.Owner == right.Owner &&
		left.Executable == right.Executable &&
		left.RentEpoch == right.RentEpoch &&
		bytes.Equal(left.Data, right.Data)
}

func normalizeInstructionComputeUnits(units uint64) uint64 {
	if units != 0 {
		return units
	}
	return structure.DefaultBuiltinInstructionCU
}
