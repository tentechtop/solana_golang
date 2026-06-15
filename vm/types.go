package vm

import "solana_golang/utils"

const (
	AddressSize                   = 32
	DefaultComputeUnitLimit       = 200_000
	DefaultMaxDataIncreasePerCall = 10 * 1024
	MaxProgramDataSize            = 1024 * 1024
	MaxInstructionDataSize        = 1232
	MaxReturnDataSize             = 1024
	MaxLogMessageSize             = 1024
	MaxSyscallInputSize           = 4096
	MaxPDASeedCount               = 16
	MaxPDASeedLength              = 32
)

// Address 表示 VM 内部地址 + 使用固定 32 字节对齐 Solana 公钥。
type Address [AddressSize]byte

// Account 表示 VM 可见账户 + 保留 Solana AccountInfo 执行所需字段。
type Account struct {
	Address    Address
	Lamports   uint64
	Data       []byte
	Owner      Address
	Executable bool
	RentEpoch  uint64
	IsSigner   bool
	IsWritable bool
}

// ProgramAccount 表示可执行程序账户 + 字节码来自账户 Data 而不是交易。
type ProgramAccount struct {
	Address    Address
	Owner      Address
	Executable bool
	Data       []byte
}

// Invocation 描述一次程序调用 + 由 Solana instruction 映射而来。
type Invocation struct {
	ProgramID       Address
	ProgramAccount  ProgramAccount
	Accounts        []Account
	InstructionData []byte
	CurrentSlot     uint64
	ComputeLimit    uint64
	Sysvars         Sysvars
}

// Result 描述 VM 执行结果 + 成功后由上层运行时写回账户。
type Result struct {
	Accounts          []Account
	UnitsConsumed     uint64
	Logs              []string
	ReturnData        *ReturnData
	LastSyscallOutput []byte
}

// ClockSysvar 描述时钟系统变量 + 合约通过 syscall 读取确定性时间。
type ClockSysvar struct {
	Slot          uint64
	UnixTimestamp int64
}

// RentSysvar 描述租金系统变量 + 合约用它判断账户存储成本。
type RentSysvar struct {
	LamportsPerByteYear        uint64
	ExemptionThresholdYears    uint64
	AccountStorageOverheadSize uint64
}

// Sysvars 聚合 VM 系统变量 + 避免合约直接访问全局状态。
type Sysvars struct {
	Clock ClockSysvar
	Rent  RentSysvar
}

// ReturnData 描述程序返回数据 + 对齐 Solana return data 模型。
type ReturnData struct {
	ProgramID Address
	Data      []byte
}

// CPIInstruction 描述跨程序调用 + VM 只暴露抽象调用请求。
type CPIInstruction struct {
	ProgramID       Address
	AccountIndexes  []uint8
	InstructionData []byte
	SignerPDAs      []Address
}

// CPIRuntime 执行跨程序调用 + 上层运行时负责重新分发到目标程序。
type CPIRuntime interface {
	Invoke(context *Context, instruction CPIInstruction) error
}

// Clone 深拷贝账户 + 防止 VM 执行污染调用方快照。
func (account Account) Clone() Account {
	return Account{
		Address:    account.Address,
		Lamports:   account.Lamports,
		Data:       utils.CloneBytes(account.Data),
		Owner:      account.Owner,
		Executable: account.Executable,
		RentEpoch:  account.RentEpoch,
		IsSigner:   account.IsSigner,
		IsWritable: account.IsWritable,
	}
}

// Clone 深拷贝返回数据 + 防止调用方修改 VM 内部切片。
func (returnData ReturnData) Clone() ReturnData {
	return ReturnData{
		ProgramID: returnData.ProgramID,
		Data:      utils.CloneBytes(returnData.Data),
	}
}

// Clone 深拷贝程序账户 + 防止 loader 共享外部字节码切片。
func (account ProgramAccount) Clone() ProgramAccount {
	return ProgramAccount{
		Address:    account.Address,
		Owner:      account.Owner,
		Executable: account.Executable,
		Data:       utils.CloneBytes(account.Data),
	}
}

func cloneAccounts(accounts []Account) []Account {
	if accounts == nil {
		return nil
	}
	cloned := make([]Account, len(accounts))
	for index, account := range accounts {
		cloned[index] = account.Clone()
	}
	return cloned
}
