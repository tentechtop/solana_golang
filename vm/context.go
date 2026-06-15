package vm

import (
	"fmt"
	"unicode/utf8"

	"solana_golang/utils"
)

// Context 保存一次 VM 调用上下文 + 将账户、计量、syscall 和运行态结果集中管理。
type Context struct {
	Invocation        Invocation
	Accounts          *AccountSet
	Meter             *ComputeMeter
	Syscalls          SyscallRegistry
	CPI               CPIRuntime
	Logs              []string
	ReturnData        *ReturnData
	LastSyscallOutput []byte
}

// Log 记录程序日志 + 限制长度和 UTF-8 避免日志污染。
func (context *Context) Log(message string) error {
	if context == nil {
		return fmt.Errorf("%w: context is nil", ErrExecutionFailed)
	}
	if !utf8.ValidString(message) {
		return fmt.Errorf("%w: log message must be utf8", ErrExecutionFailed)
	}
	if len(message) > MaxLogMessageSize {
		return fmt.Errorf("%w: log message length %d exceeds %d", ErrExecutionFailed, len(message), MaxLogMessageSize)
	}
	context.Logs = append(context.Logs, message)
	return nil
}

// SetReturnData 设置程序返回数据 + 限制大小避免交易结果膨胀。
func (context *Context) SetReturnData(data []byte) error {
	if context == nil {
		return fmt.Errorf("%w: context is nil", ErrExecutionFailed)
	}
	if len(data) > MaxReturnDataSize {
		return fmt.Errorf("%w: return data length %d exceeds %d", ErrExecutionFailed, len(data), MaxReturnDataSize)
	}
	returnData := ReturnData{
		ProgramID: context.Invocation.ProgramID,
		Data:      utils.CloneBytes(data),
	}
	context.ReturnData = &returnData
	return nil
}

// SetLastSyscallOutput 保存 syscall 输出 + 让解释器后续指令可扩展读取。
func (context *Context) SetLastSyscallOutput(output []byte) {
	if context == nil {
		return
	}
	context.LastSyscallOutput = utils.CloneBytes(output)
}

// CloneLogs 深拷贝日志 + 防止结果被调用方修改。
func (context *Context) CloneLogs() []string {
	if context == nil || context.Logs == nil {
		return nil
	}
	cloned := make([]string, len(context.Logs))
	copy(cloned, context.Logs)
	return cloned
}

// CloneReturnData 深拷贝返回数据 + 保持结果对象不可变语义。
func (context *Context) CloneReturnData() *ReturnData {
	if context == nil || context.ReturnData == nil {
		return nil
	}
	cloned := context.ReturnData.Clone()
	return &cloned
}
