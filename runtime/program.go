package runtime

import (
	"fmt"

	"solana_golang/structure"
)

// ProgramID 标识可执行程序 + 复用账户模型公钥避免重复定义地址类型。
type ProgramID = structure.PublicKey

// InstructionContext 描述单指令执行上下文 + runtime 调度程序时只传执行所需状态。
type InstructionContext struct {
	InstructionIndex int
	Instruction      structure.CompiledInstruction
	Message          structure.ResolvedMessage
	Accounts         map[structure.PublicKey]structure.Account
	CurrentSlot      uint64
	RentConfig       structure.RentConfig
	ComputeBudget    structure.ComputeBudgetLimits
	BuiltinPrograms  structure.BuiltinProgramIDs
}

// Program 定义固定程序接口 + runtime 只依赖接口防止 import 具体 programs 形成循环引用。
type Program interface {
	ProgramID() ProgramID
	Execute(context InstructionContext) error
}

// ProgramRegistry 保存固定程序映射 + 由 node 组合层显式注册避免 init 副作用。
type ProgramRegistry struct {
	programs map[ProgramID]Program
	fallback Program
}

// NewProgramRegistry 创建程序注册表 + 集中拒绝 nil 和重复程序防止分发歧义。
func NewProgramRegistry(programs ...Program) (ProgramRegistry, error) {
	registry := ProgramRegistry{programs: make(map[ProgramID]Program, len(programs))}
	for _, program := range programs {
		if err := registry.RegisterProgram(program); err != nil {
			return ProgramRegistry{}, err
		}
	}
	return registry, nil
}

// RegisterProgram 注册固定程序 + 组合层显式注入可防止 runtime 反向依赖 programs。
func (registry *ProgramRegistry) RegisterProgram(program Program) error {
	if program == nil {
		return fmt.Errorf("runtime: nil program")
	}
	if registry.programs == nil {
		registry.programs = make(map[ProgramID]Program)
	}
	programID := program.ProgramID()
	if _, exists := registry.programs[programID]; exists {
		return fmt.Errorf("runtime: duplicate program %s", programID.String())
	}
	registry.programs[programID] = program
	return nil
}

// SetFallbackProgram 设置兜底程序 + 用于未来 VM 按可执行账户处理未知 program id。
func (registry *ProgramRegistry) SetFallbackProgram(program Program) error {
	if program == nil {
		registry.fallback = nil
		return nil
	}
	if program.ProgramID() != (ProgramID{}) {
		return fmt.Errorf("runtime: fallback program id must be zero")
	}
	registry.fallback = program
	return nil
}

// Execute 执行已注册程序 + 未注册时交给 VM 兜底或返回清晰错误。
func (registry ProgramRegistry) Execute(context InstructionContext) error {
	if int(context.Instruction.ProgramIDIndex) >= len(context.Message.AccountKeys) {
		return fmt.Errorf("%w: program id index out of range", structure.ErrInvalidInstruction)
	}
	programID := context.Message.AccountKeys[context.Instruction.ProgramIDIndex]
	program, exists := registry.programs[programID]
	if exists {
		return program.Execute(context)
	}
	if registry.fallback != nil {
		return registry.fallback.Execute(context)
	}
	return fmt.Errorf("runtime: unsupported program %s", programID.String())
}

// IsEmpty 判断注册表是否为空 + 执行器据此决定是否使用 structure 现有默认策略。
func (registry ProgramRegistry) IsEmpty() bool {
	return len(registry.programs) == 0 && registry.fallback == nil
}
