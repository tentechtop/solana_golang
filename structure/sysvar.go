package structure

import "fmt"

const (
	DefaultRentBurnPercent = uint8(50)
)

// ClockSysvar 描述时钟上下文 + 为程序读取 slot、epoch 和 unix 时间提供状态。
type ClockSysvar struct {
	Slot                uint64
	EpochStartTimestamp int64
	Epoch               uint64
	LeaderScheduleEpoch uint64
	UnixTimestamp       int64
}

// RentSysvar 描述租金上下文 + 将租金配置暴露给执行程序。
type RentSysvar struct {
	LamportsPerByteYear        uint64
	ExemptionThresholdYears    uint64
	AccountStorageOverheadSize uint64
	BurnPercent                uint8
}

// InstructionsSysvar 描述当前交易指令上下文 + 支持程序检查同交易内指令。
type InstructionsSysvar struct {
	Instructions []Instruction
	CurrentIndex uint16
}

// DefaultRentSysvar 返回默认租金 sysvar + 复用账户租金配置。
func DefaultRentSysvar() RentSysvar {
	return RentSysvar{
		LamportsPerByteYear:        DefaultRentConfig.LamportsPerByteYear,
		ExemptionThresholdYears:    DefaultRentConfig.ExemptionThresholdYears,
		AccountStorageOverheadSize: DefaultRentConfig.AccountStorageOverheadSize,
		BurnPercent:                DefaultRentBurnPercent,
	}
}

// RentConfig 转换租金配置 + 给账户校验和执行器复用。
func (sysvar RentSysvar) RentConfig() RentConfig {
	return RentConfig{
		LamportsPerByteYear:        sysvar.LamportsPerByteYear,
		ExemptionThresholdYears:    sysvar.ExemptionThresholdYears,
		AccountStorageOverheadSize: sysvar.AccountStorageOverheadSize,
	}
}

// Validate 校验租金 sysvar + 防止无效租金参数进入程序执行。
func (sysvar RentSysvar) Validate() error {
	if sysvar.BurnPercent > 100 {
		return fmt.Errorf("%w: rent burn percent %d exceeds 100", ErrInvalidSysvar, sysvar.BurnPercent)
	}
	if err := sysvar.RentConfig().Validate(); err != nil {
		return fmt.Errorf("structure: validate rent sysvar: %w", err)
	}
	return nil
}

// MinimumBalance 计算账户最小余额 + 使用 sysvar 中的租金参数。
func (sysvar RentSysvar) MinimumBalance(dataLength int) (uint64, error) {
	if err := sysvar.Validate(); err != nil {
		return 0, err
	}
	return sysvar.RentConfig().MinimumBalance(dataLength)
}

// Validate 校验指令 sysvar + 保证当前指令索引可读。
func (sysvar InstructionsSysvar) Validate() error {
	if len(sysvar.Instructions) == 0 {
		return fmt.Errorf("%w: instructions cannot be empty", ErrInvalidSysvar)
	}
	if int(sysvar.CurrentIndex) >= len(sysvar.Instructions) {
		return fmt.Errorf("%w: current index %d out of range", ErrInvalidSysvar, sysvar.CurrentIndex)
	}
	for instructionIndex, instruction := range sysvar.Instructions {
		if err := instruction.Validate(); err != nil {
			return fmt.Errorf("structure: sysvar instruction %d: %w", instructionIndex, err)
		}
	}
	return nil
}

// CurrentInstruction 返回当前指令 + 为程序读取调用上下文提供稳定入口。
func (sysvar InstructionsSysvar) CurrentInstruction() (Instruction, error) {
	if err := sysvar.Validate(); err != nil {
		return Instruction{}, err
	}
	return sysvar.Instructions[sysvar.CurrentIndex].Clone(), nil
}

// Clone 深拷贝指令 sysvar + 避免程序读取时修改交易指令。
func (sysvar InstructionsSysvar) Clone() InstructionsSysvar {
	instructions := make([]Instruction, len(sysvar.Instructions))
	for index, instruction := range sysvar.Instructions {
		instructions[index] = instruction.Clone()
	}
	return InstructionsSysvar{
		Instructions: instructions,
		CurrentIndex: sysvar.CurrentIndex,
	}
}
