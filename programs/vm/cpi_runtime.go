package vmprogram

import (
	"fmt"

	"github.com/tentechtop/ordexus-core/runtime"
	"github.com/tentechtop/ordexus-core/structure"
	"github.com/tentechtop/ordexus-core/utils"
	ovm "github.com/tentechtop/ordexus-core/vm"
)

const maxContractInvocationDepth = uint8(4)

type contractCPIRuntime struct {
	outerContext  runtime.InstructionContext
	invokeProgram runtime.ProgramHandler
}

func newContractCPIRuntime(context runtime.InstructionContext, invokeProgram runtime.ProgramHandler) ovm.CPIRuntime {
	return &contractCPIRuntime{outerContext: context, invokeProgram: invokeProgram}
}

func (adapter *contractCPIRuntime) Invoke(vmContext *ovm.Context, instruction ovm.CPIInstruction) error {
	if adapter == nil || adapter.invokeProgram == nil || vmContext == nil || vmContext.Accounts == nil {
		return fmt.Errorf("vm program: cpi runtime is unavailable")
	}
	if adapter.outerContext.InvocationDepth >= maxContractInvocationDepth {
		return fmt.Errorf("vm program: cpi depth exceeds %d", maxContractInvocationDepth)
	}

	vmAccounts := vmContext.Accounts.Snapshot()
	programID := publicKeyFromVMAddress(instruction.ProgramID)
	programIDIndex, err := messageAccountIndex(adapter.outerContext.Message, programID)
	if err != nil {
		return fmt.Errorf("vm program: cpi program: %w", err)
	}
	if _, err := vmAccountIndex(vmAccounts, instruction.ProgramID); err != nil {
		return fmt.Errorf("vm program: cpi program must be included in outer instruction accounts: %w", err)
	}

	accountIndexes, accountAddresses, err := mapCPIAccountIndexes(adapter.outerContext.Message, vmAccounts, instruction.AccountIndexes)
	if err != nil {
		return err
	}
	signerOverrides, err := cpiSignerOverrides(adapter.outerContext.SignerOverrides, instruction.SignerPDAs, accountAddresses)
	if err != nil {
		return err
	}

	workingAccounts := cloneRuntimeAccounts(adapter.outerContext.Accounts)
	for _, account := range vmAccounts {
		workingAccounts[publicKeyFromVMAddress(account.Address)] = structure.Account{
			Lamports:   account.Lamports,
			Data:       utils.CloneBytes(account.Data),
			Owner:      publicKeyFromVMAddress(account.Owner),
			Executable: account.Executable,
			RentEpoch:  account.RentEpoch,
		}
	}

	nestedInstruction := structure.CompiledInstruction{
		ProgramIDIndex: uint8(programIDIndex),
		AccountIndexes: accountIndexes,
		Data:           utils.CloneBytes(instruction.InstructionData),
	}
	if adapter.outerContext.ExecutionTrace != nil {
		adapter.outerContext.ExecutionTrace.InnerInstructions = append(
			adapter.outerContext.ExecutionTrace.InnerInstructions,
			nestedInstruction.Clone(),
		)
	}

	nestedContext := adapter.outerContext
	nestedContext.InvocationDepth++
	nestedContext.Instruction = nestedInstruction
	nestedContext.Accounts = workingAccounts
	nestedContext.SignerOverrides = signerOverrides
	computeUnitsBefore := uint64(0)
	if nestedContext.ComputeUnitsUsed != nil {
		computeUnitsBefore = *nestedContext.ComputeUnitsUsed
	}
	if err := adapter.invokeProgram(nestedContext); err != nil {
		return fmt.Errorf("vm program: cpi invoke %s: %w", programID.String(), err)
	}
	if nestedContext.ComputeUnitsUsed != nil && *nestedContext.ComputeUnitsUsed == computeUnitsBefore {
		*nestedContext.ComputeUnitsUsed += structure.DefaultBuiltinInstructionCU
	}
	if adapter.outerContext.ComputeUnitsUsed != nil &&
		adapter.outerContext.ComputeBudget.MaxComputeUnits > 0 &&
		*adapter.outerContext.ComputeUnitsUsed > adapter.outerContext.ComputeBudget.MaxComputeUnits {
		return fmt.Errorf("vm program: cpi compute units exceed transaction limit")
	}

	if err := syncCPIAccounts(vmContext.Accounts, vmAccounts, workingAccounts); err != nil {
		return err
	}
	copyCPIReturnData(vmContext, adapter.outerContext.ExecutionTrace)
	return nil
}

func mapCPIAccountIndexes(message structure.ResolvedMessage, vmAccounts []ovm.Account, indexes []uint8) ([]uint8, map[structure.PublicKey]struct{}, error) {
	mapped := make([]uint8, len(indexes))
	addresses := make(map[structure.PublicKey]struct{}, len(indexes))
	for index, vmIndex := range indexes {
		if int(vmIndex) >= len(vmAccounts) {
			return nil, nil, fmt.Errorf("vm program: cpi account index %d out of range", vmIndex)
		}
		address := publicKeyFromVMAddress(vmAccounts[vmIndex].Address)
		messageIndex, err := messageAccountIndex(message, address)
		if err != nil {
			return nil, nil, fmt.Errorf("vm program: cpi account %d: %w", vmIndex, err)
		}
		mapped[index] = uint8(messageIndex)
		addresses[address] = struct{}{}
	}
	return mapped, addresses, nil
}

func cpiSignerOverrides(existing map[structure.PublicKey]struct{}, signerPDAs []ovm.Address, accounts map[structure.PublicKey]struct{}) (map[structure.PublicKey]struct{}, error) {
	overrides := make(map[structure.PublicKey]struct{}, len(existing)+len(signerPDAs))
	for address := range existing {
		overrides[address] = struct{}{}
	}
	for _, signerPDA := range signerPDAs {
		address := publicKeyFromVMAddress(signerPDA)
		if _, exists := accounts[address]; !exists {
			return nil, fmt.Errorf("vm program: cpi signer %s is not an instruction account", address.String())
		}
		overrides[address] = struct{}{}
	}
	return overrides, nil
}

func syncCPIAccounts(accountSet *ovm.AccountSet, previous []ovm.Account, working map[structure.PublicKey]structure.Account) error {
	for index, previousAccount := range previous {
		address := publicKeyFromVMAddress(previousAccount.Address)
		account, exists := working[address]
		if !exists {
			return fmt.Errorf("vm program: cpi removed account %s", address.String())
		}
		updated := ovm.Account{
			Address:    previousAccount.Address,
			Lamports:   account.Lamports,
			Data:       utils.CloneBytes(account.Data),
			Owner:      vmAddressFromPublicKey(account.Owner),
			Executable: account.Executable,
			RentEpoch:  account.RentEpoch,
			IsSigner:   previousAccount.IsSigner,
			IsWritable: previousAccount.IsWritable,
		}
		if err := accountSet.SetFixedProgramAccount(index, updated); err != nil {
			return fmt.Errorf("vm program: sync cpi account %d: %w", index, err)
		}
	}
	return nil
}

func copyCPIReturnData(context *ovm.Context, trace *runtime.ProgramExecutionTrace) {
	if trace == nil || trace.ReturnData == nil {
		return
	}
	context.ReturnData = &ovm.ReturnData{
		ProgramID: vmAddressFromPublicKey(trace.ReturnData.ProgramID),
		Data:      utils.CloneBytes(trace.ReturnData.Data),
	}
}

func messageAccountIndex(message structure.ResolvedMessage, address structure.PublicKey) (int, error) {
	for index, accountAddress := range message.AccountKeys {
		if accountAddress == address {
			return index, nil
		}
	}
	return 0, fmt.Errorf("account %s is not in transaction message", address.String())
}

func vmAccountIndex(accounts []ovm.Account, address ovm.Address) (int, error) {
	for index, account := range accounts {
		if account.Address == address {
			return index, nil
		}
	}
	return 0, fmt.Errorf("account is not visible to VM")
}

func cloneRuntimeAccounts(accounts map[structure.PublicKey]structure.Account) map[structure.PublicKey]structure.Account {
	cloned := make(map[structure.PublicKey]structure.Account, len(accounts))
	for address, account := range accounts {
		cloned[address] = account.Clone()
	}
	return cloned
}
