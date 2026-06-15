package structure

import "fmt"

// InstructionExecutionContext 描述指令执行上下文 + 为固定指令和未来 VM 共享同一入口。
type InstructionExecutionContext struct {
	InstructionIndex int
	Instruction      CompiledInstruction
	Message          ResolvedMessage
	Accounts         map[PublicKey]Account
	CurrentSlot      uint64
	RentConfig       RentConfig
	BuiltinPrograms  BuiltinProgramIDs
}

// InstructionStrategy 执行单个程序指令 + 用策略模式隔离固定指令和未来 VM。
type InstructionStrategy interface {
	ProgramID() PublicKey
	Execute(context InstructionExecutionContext) error
}

// InstructionStrategyRegistry 保存程序执行策略 + 使用程序 ID 做 O(1) 分发。
type InstructionStrategyRegistry struct {
	strategies map[PublicKey]InstructionStrategy
}

// NewInstructionStrategyRegistry 创建策略注册表 + 拒绝重复程序防止分发歧义。
func NewInstructionStrategyRegistry(strategies ...InstructionStrategy) (InstructionStrategyRegistry, error) {
	registry := InstructionStrategyRegistry{strategies: make(map[PublicKey]InstructionStrategy, len(strategies))}
	for _, strategy := range strategies {
		if strategy == nil {
			return InstructionStrategyRegistry{}, fmt.Errorf("%w: nil instruction strategy", ErrInvalidInstruction)
		}
		programID := strategy.ProgramID()
		if _, exists := registry.strategies[programID]; exists {
			return InstructionStrategyRegistry{}, fmt.Errorf("%w: duplicate strategy for %s", ErrInvalidInstruction, programID.String())
		}
		registry.strategies[programID] = strategy
	}
	return registry, nil
}

// DefaultInstructionStrategyRegistry 创建默认策略注册表 + 当前只执行固定系统和隐私指令。
func DefaultInstructionStrategyRegistry(programIDs BuiltinProgramIDs) (InstructionStrategyRegistry, error) {
	return NewInstructionStrategyRegistry(
		SystemInstructionStrategy{Program: programIDs.System},
		PrivacyInstructionStrategy{Program: programIDs.Privacy},
	)
}

// Execute 执行已注册策略 + 为未支持程序返回清晰错误。
func (registry InstructionStrategyRegistry) Execute(context InstructionExecutionContext) error {
	if int(context.Instruction.ProgramIDIndex) >= len(context.Message.AccountKeys) {
		return fmt.Errorf("%w: program id index out of range", ErrInvalidInstruction)
	}
	programID := context.Message.AccountKeys[context.Instruction.ProgramIDIndex]
	strategy, exists := registry.strategies[programID]
	if !exists {
		return fmt.Errorf("unsupported program %s", programID.String())
	}
	return strategy.Execute(context)
}

// SystemInstructionStrategy 执行系统固定指令 + 保持现阶段无需 VM 的转账闭环。
type SystemInstructionStrategy struct {
	Program PublicKey
}

// ProgramID 返回系统程序 ID + 供策略注册表索引。
func (strategy SystemInstructionStrategy) ProgramID() PublicKey {
	return strategy.Program
}

// Execute 执行系统指令 + 复用现有系统账户状态转换。
func (strategy SystemInstructionStrategy) Execute(context InstructionExecutionContext) error {
	systemInstruction, err := UnmarshalSystemInstructionBinary(context.Instruction.Data)
	if err != nil {
		return err
	}
	return executeSystemInstruction(systemInstruction, context.Instruction, context.Message, context.Accounts, context.RentConfig)
}
