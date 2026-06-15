package system

import (
	"fmt"

	"solana_golang/runtime"
	"solana_golang/structure"
)

// Program 执行系统固定指令 + 保持账户基础操作不进入 structure。
type Program struct {
	programID structure.PublicKey
}

// NewProgram 创建系统程序 + 由组合层传入链配置中的系统程序 ID。
func NewProgram(programID structure.PublicKey) Program {
	return Program{programID: programID}
}

// ProgramID 返回系统程序 ID + 供 runtime 注册表分发。
func (program Program) ProgramID() structure.PublicKey {
	return program.programID
}

// Execute 执行系统指令 + 只修改本次执行上下文中的账户快照。
func (program Program) Execute(context runtime.InstructionContext) error {
	systemInstruction, err := structure.UnmarshalSystemInstructionBinary(context.Instruction.Data)
	if err != nil {
		return err
	}
	return executeSystemInstruction(systemInstruction, context)
}

func executeSystemInstruction(systemInstruction structure.SystemInstruction, context runtime.InstructionContext) error {
	switch systemInstruction.Type {
	case structure.SystemInstructionTransfer:
		return executeSystemTransfer(systemInstruction, context)
	case structure.SystemInstructionCreateAccount:
		return executeSystemCreateAccount(systemInstruction, context)
	case structure.SystemInstructionAssign:
		return executeSystemAssign(systemInstruction, context)
	case structure.SystemInstructionAllocate:
		return executeSystemAllocate(systemInstruction, context)
	default:
		return fmt.Errorf("%w: unsupported type %d", structure.ErrInvalidSystemInstruction, systemInstruction.Type)
	}
}

func executeSystemTransfer(systemInstruction structure.SystemInstruction, context runtime.InstructionContext) error {
	if len(context.Instruction.AccountIndexes) < 2 {
		return fmt.Errorf("%w: transfer requires source and destination", structure.ErrInvalidSystemInstruction)
	}
	sourceAddress := context.Message.AccountKeys[context.Instruction.AccountIndexes[0]]
	destinationAddress := context.Message.AccountKeys[context.Instruction.AccountIndexes[1]]
	if !runtime.IsSignerAddress(sourceAddress, context.Message) {
		return fmt.Errorf("%w: transfer source must sign", structure.ErrMissingRequiredSignature)
	}
	return runtime.TransferLamports(sourceAddress, destinationAddress, systemInstruction.Transfer.Lamports, context.Accounts, context.RentConfig)
}

func executeSystemCreateAccount(systemInstruction structure.SystemInstruction, context runtime.InstructionContext) error {
	if len(context.Instruction.AccountIndexes) < 2 {
		return fmt.Errorf("%w: create account requires payer and new account", structure.ErrInvalidSystemInstruction)
	}
	payerAddress := context.Message.AccountKeys[context.Instruction.AccountIndexes[0]]
	newAccountAddress := context.Message.AccountKeys[context.Instruction.AccountIndexes[1]]
	if !runtime.IsSignerAddress(payerAddress, context.Message) || !runtime.IsSignerAddress(newAccountAddress, context.Message) {
		return fmt.Errorf("%w: create account requires payer and new account signatures", structure.ErrMissingRequiredSignature)
	}
	if err := runtime.TransferLamports(payerAddress, newAccountAddress, systemInstruction.CreateAccount.Lamports, context.Accounts, context.RentConfig); err != nil {
		return err
	}

	newAccount := context.Accounts[newAccountAddress].Clone()
	newAccount.Owner = systemInstruction.CreateAccount.Owner
	newAccount.Data = make([]byte, int(systemInstruction.CreateAccount.Space))
	if err := newAccount.ValidateWithRent(context.RentConfig); err != nil {
		return err
	}
	context.Accounts[newAccountAddress] = newAccount
	return nil
}

func executeSystemAssign(systemInstruction structure.SystemInstruction, context runtime.InstructionContext) error {
	if len(context.Instruction.AccountIndexes) < 1 {
		return fmt.Errorf("%w: assign requires target account", structure.ErrInvalidSystemInstruction)
	}
	targetAddress := context.Message.AccountKeys[context.Instruction.AccountIndexes[0]]
	if !runtime.IsSignerAddress(targetAddress, context.Message) {
		return fmt.Errorf("%w: assign target must sign", structure.ErrMissingRequiredSignature)
	}
	account := context.Accounts[targetAddress].Clone()
	account.Owner = systemInstruction.Assign.Owner
	context.Accounts[targetAddress] = account
	return nil
}

func executeSystemAllocate(systemInstruction structure.SystemInstruction, context runtime.InstructionContext) error {
	if len(context.Instruction.AccountIndexes) < 1 {
		return fmt.Errorf("%w: allocate requires target account", structure.ErrInvalidSystemInstruction)
	}
	targetAddress := context.Message.AccountKeys[context.Instruction.AccountIndexes[0]]
	if !runtime.IsSignerAddress(targetAddress, context.Message) {
		return fmt.Errorf("%w: allocate target must sign", structure.ErrMissingRequiredSignature)
	}
	account := context.Accounts[targetAddress].Clone()
	if err := account.ResizeData(int(systemInstruction.Allocate.Space), context.RentConfig); err != nil {
		return err
	}
	context.Accounts[targetAddress] = account
	return nil
}
