package vm

import (
	"fmt"
)

// Runtime 执行 Solana 风格程序调用 + 将 loader、executor、meter 组合为稳定入口。
type Runtime struct {
	Loader          Loader
	Executor        Executor
	Syscalls        SyscallRegistry
	CPI             CPIRuntime
	LoaderID        Address
	ComputeLimit    uint64
	MaxDataIncrease int
}

// NewRuntime 创建默认运行时 + 当前使用最小字节码后端。
func NewRuntime(loaderID Address) Runtime {
	return Runtime{
		Loader:          BytecodeLoader{},
		Executor:        BytecodeExecutor{},
		Syscalls:        DefaultSyscallRegistry(),
		LoaderID:        loaderID,
		ComputeLimit:    DefaultComputeUnitLimit,
		MaxDataIncrease: DefaultMaxDataIncreasePerCall,
	}
}

// Execute 执行一次程序调用 + 失败时不返回可写账户变更。
func (runtime Runtime) Execute(invocation Invocation) (Result, error) {
	normalizedRuntime := runtime.normalize()
	if invocation.ProgramID == (Address{}) {
		return Result{}, fmt.Errorf("%w: program id is empty", ErrInvalidProgram)
	}
	if len(invocation.InstructionData) > MaxInstructionDataSize {
		return Result{}, fmt.Errorf("%w: instruction data length %d exceeds %d", ErrExecutionFailed, len(invocation.InstructionData), MaxInstructionDataSize)
	}
	program, err := normalizedRuntime.Loader.Load(invocation.ProgramAccount.Clone(), normalizedRuntime.LoaderID)
	if err != nil {
		return Result{}, err
	}
	accountSet, err := NewAccountSet(invocation.ProgramID, invocation.Accounts, normalizedRuntime.MaxDataIncrease)
	if err != nil {
		return Result{}, err
	}
	meter := NewComputeMeter(firstNonZero(invocation.ComputeLimit, normalizedRuntime.ComputeLimit))
	invocation.Sysvars = normalizeSysvars(invocation.Sysvars, invocation.CurrentSlot)
	context := &Context{
		Invocation: invocation,
		Accounts:   accountSet,
		Meter:      &meter,
		Syscalls:   normalizedRuntime.Syscalls,
		CPI:        normalizedRuntime.CPI,
	}
	if err := normalizedRuntime.Executor.Execute(context, program); err != nil {
		return Result{}, err
	}
	return Result{
		Accounts:          accountSet.Snapshot(),
		UnitsConsumed:     meter.Consumed(),
		Logs:              context.CloneLogs(),
		ReturnData:        context.CloneReturnData(),
		LastSyscallOutput: context.LastSyscallOutput,
	}, nil
}

func (runtime Runtime) normalize() Runtime {
	if runtime.Loader == nil {
		runtime.Loader = BytecodeLoader{}
	}
	if runtime.Executor == nil {
		runtime.Executor = BytecodeExecutor{}
	}
	if runtime.Syscalls.IsZero() {
		runtime.Syscalls = DefaultSyscallRegistry()
	}
	if runtime.ComputeLimit == 0 {
		runtime.ComputeLimit = DefaultComputeUnitLimit
	}
	if runtime.MaxDataIncrease <= 0 {
		runtime.MaxDataIncrease = DefaultMaxDataIncreasePerCall
	}
	return runtime
}

func normalizeSysvars(sysvars Sysvars, currentSlot uint64) Sysvars {
	if sysvars.Clock.Slot == 0 {
		sysvars.Clock.Slot = currentSlot
	}
	return sysvars
}

func firstNonZero(values ...uint64) uint64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}
