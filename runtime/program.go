package runtime

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"

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
	CurrentEpoch     uint64
	RentConfig       structure.RentConfig
	ComputeBudget    structure.ComputeBudgetLimits
	BuiltinPrograms  structure.BuiltinProgramIDs
	SignerOverrides  map[structure.PublicKey]struct{}
	Logger           *slog.Logger
}

// Program 定义固定程序接口 + runtime 只依赖接口防止 import 具体 programs 形成循环引用。
type Program interface {
	ProgramID() ProgramID
	Execute(context InstructionContext) error
}

// ProgramHandler 处理单条程序指令 + 让程序接入方式与 p2p 协议处理器保持一致。
type ProgramHandler func(context InstructionContext) error

// ProgramSpec 保存程序元数据 + 让注册表同时支持按 ID 和名称查询。
type ProgramSpec struct {
	ID   ProgramID
	Name string
}

// Validate 校验程序定义 + 防止空名称进入执行路由。
func (spec ProgramSpec) Validate() error {
	if strings.TrimSpace(spec.Name) == "" {
		return fmt.Errorf("runtime: invalid program: empty name")
	}
	return nil
}

// NormalizedName 返回规范程序名 + 保持注册和查询使用同一格式。
func (spec ProgramSpec) NormalizedName() string {
	return NormalizeProgramName(spec.Name)
}

// NormalizeProgramName 规范程序名 + 只裁剪空白以保留 base58 程序地址大小写。
func NormalizeProgramName(name string) string {
	return strings.TrimSpace(name)
}

type registeredProgram struct {
	spec    ProgramSpec
	handler ProgramHandler
}

type programRegistryState struct {
	mutex    sync.RWMutex
	programs map[ProgramID]registeredProgram
	nameToID map[string]ProgramID
	fallback ProgramHandler
}

// ProgramRegistry 保存程序处理器映射 + 由 node 组合层显式注册避免 init 副作用。
type ProgramRegistry struct {
	state *programRegistryState
}

// NewProgramRegistry 创建兼容注册表 + 继续支持旧 Program 接口的显式注入。
func NewProgramRegistry(programs ...Program) (ProgramRegistry, error) {
	registry := NewProgramHandlerRegistry()
	for _, program := range programs {
		if err := registry.RegisterProgram(program); err != nil {
			return ProgramRegistry{}, err
		}
	}
	return registry, nil
}

// NewProgramHandlerRegistry 创建处理器注册表 + 默认不注册任何程序避免隐式业务逻辑。
func NewProgramHandlerRegistry() ProgramRegistry {
	return ProgramRegistry{state: newProgramRegistryState(0)}
}

// RegisterHandler 注册程序处理器 + 同时维护程序 ID 和规范名称索引。
func (registry *ProgramRegistry) RegisterHandler(spec ProgramSpec, handler ProgramHandler) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	if handler == nil {
		return fmt.Errorf("runtime: nil program handler")
	}

	normalizedName := spec.NormalizedName()
	if normalizedName == "" {
		return fmt.Errorf("runtime: invalid program: empty normalized name")
	}
	spec.Name = normalizedName

	state := registry.ensureState()
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if _, exists := state.programs[spec.ID]; exists {
		return fmt.Errorf("runtime: duplicate program %s", spec.ID.String())
	}
	if existedID, exists := state.nameToID[normalizedName]; exists && existedID != spec.ID {
		return fmt.Errorf("runtime: duplicate program name %s", normalizedName)
	}
	state.programs[spec.ID] = registeredProgram{spec: spec, handler: handler}
	state.nameToID[normalizedName] = spec.ID
	return nil
}

// RegisterProgram 注册旧接口程序 + 保持既有 programs 包无感迁移到处理器模型。
func (registry *ProgramRegistry) RegisterProgram(program Program) error {
	if program == nil {
		return fmt.Errorf("runtime: nil program")
	}
	programID := program.ProgramID()
	return registry.RegisterHandler(ProgramSpec{ID: programID, Name: programID.String()}, program.Execute)
}

// SetFallbackHandler 设置兜底处理器 + 用于未来 VM 按可执行账户处理未知 program id。
func (registry *ProgramRegistry) SetFallbackHandler(handler ProgramHandler) error {
	state := registry.ensureState()
	state.mutex.Lock()
	defer state.mutex.Unlock()
	state.fallback = handler
	return nil
}

// SetFallbackProgram 设置旧接口兜底程序 + 保持 VM 兜底程序使用零 ProgramID 的既有约定。
func (registry *ProgramRegistry) SetFallbackProgram(program Program) error {
	if program == nil {
		return registry.SetFallbackHandler(nil)
	}
	if program.ProgramID() != (ProgramID{}) {
		return fmt.Errorf("runtime: fallback program id must be zero")
	}
	return registry.SetFallbackHandler(program.Execute)
}

// Spec 查询程序定义 + 供上层检查程序是否已注册。
func (registry ProgramRegistry) Spec(programID ProgramID) (ProgramSpec, bool) {
	state := registry.state
	if state == nil {
		return ProgramSpec{}, false
	}
	state.mutex.RLock()
	defer state.mutex.RUnlock()
	registered, ok := state.programs[programID]
	return registered.spec, ok
}

// SpecByName 按名称查询程序定义 + 支持外部配置使用稳定程序名。
func (registry ProgramRegistry) SpecByName(name string) (ProgramSpec, bool) {
	state := registry.state
	if state == nil {
		return ProgramSpec{}, false
	}
	state.mutex.RLock()
	defer state.mutex.RUnlock()
	programID, ok := state.nameToID[NormalizeProgramName(name)]
	if !ok {
		return ProgramSpec{}, false
	}
	registered, ok := state.programs[programID]
	return registered.spec, ok
}

// Clone 复制程序注册表 + 防止执行器把可变 map 暴露给交易输入。
func (registry ProgramRegistry) Clone() ProgramRegistry {
	state := registry.state
	if state == nil {
		return ProgramRegistry{}
	}
	state.mutex.RLock()
	defer state.mutex.RUnlock()

	clonedState := newProgramRegistryState(len(state.programs))
	clonedState.fallback = state.fallback
	for programID, registered := range state.programs {
		clonedState.programs[programID] = registered
	}
	for name, programID := range state.nameToID {
		clonedState.nameToID[name] = programID
	}
	return ProgramRegistry{state: clonedState}
}

// Execute 执行已注册程序 + 未注册时交给 VM 兜底或返回清晰错误。
func (registry ProgramRegistry) Execute(context InstructionContext) error {
	if int(context.Instruction.ProgramIDIndex) >= len(context.Message.AccountKeys) {
		return fmt.Errorf("%w: program id index out of range", structure.ErrInvalidInstruction)
	}
	programID := context.Message.AccountKeys[context.Instruction.ProgramIDIndex]
	program, fallback := registry.lookup(programID)
	if program.handler != nil {
		return program.handler(context)
	}
	if fallback != nil {
		if err := validateVirtualMachineFallbackProgram(programID, context); err != nil {
			return err
		}
		return fallback(context)
	}
	return fmt.Errorf("runtime: unsupported program %s", programID.String())
}

// IsEmpty 判断注册表是否为空 + 执行器据此决定是否注入默认程序注册表。
func (registry ProgramRegistry) IsEmpty() bool {
	state := registry.state
	if state == nil {
		return true
	}
	state.mutex.RLock()
	defer state.mutex.RUnlock()
	return len(state.programs) == 0 && state.fallback == nil
}

func (registry ProgramRegistry) lookup(programID ProgramID) (registeredProgram, ProgramHandler) {
	state := registry.state
	if state == nil {
		return registeredProgram{}, nil
	}
	state.mutex.RLock()
	defer state.mutex.RUnlock()
	program, exists := state.programs[programID]
	if exists {
		return program, nil
	}
	return registeredProgram{}, state.fallback
}

// validateVirtualMachineFallbackProgram 校验 VM 兜底入口 + 防止普通未知账户绕过固定程序边界。
func validateVirtualMachineFallbackProgram(programID ProgramID, context InstructionContext) error {
	account, exists := context.Accounts[programID]
	if !exists {
		return fmt.Errorf("%w: vm program account %s not found", structure.ErrInvalidLoadedTransaction, programID.String())
	}
	if !account.Executable {
		return fmt.Errorf("%w: vm program account %s is not executable", structure.ErrInvalidInstruction, programID.String())
	}
	loaderProgramID := context.BuiltinPrograms.BPFLoader
	if loaderProgramID.IsZero() {
		loaderProgramID = structure.DefaultBuiltinProgramIDs.BPFLoader
	}
	if account.Owner != loaderProgramID {
		return fmt.Errorf("%w: vm program account %s owner is not bpf loader", structure.ErrInvalidInstruction, programID.String())
	}
	return nil
}

func (registry *ProgramRegistry) ensureState() *programRegistryState {
	if registry.state == nil {
		registry.state = newProgramRegistryState(0)
	}
	return registry.state
}

func newProgramRegistryState(capacity int) *programRegistryState {
	return &programRegistryState{
		programs: make(map[ProgramID]registeredProgram, capacity),
		nameToID: make(map[string]ProgramID, capacity),
	}
}
