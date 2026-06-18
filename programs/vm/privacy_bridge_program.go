package vmprogram

import (
	"fmt"
	"sync"

	"solana_golang/runtime"
	"solana_golang/structure"
	"solana_golang/utils"
	svm "solana_golang/vm"
)

var (
	privacyBridgeProgramDataOnce sync.Once
	privacyBridgeProgramData     []byte
	privacyBridgeProgramDataErr  error
)

// PrivacyBridgeProgram 使用内置 VM 字节码调度隐私业务 + 保持 Privacy ProgramID 不变。
type PrivacyBridgeProgram struct {
	programID     structure.PublicKey
	loaderProgram structure.PublicKey
	runtime       svm.Runtime
}

// NewPrivacyBridgeProgram 创建隐私 VM 桥程序 + 用于策略切换到 vm_syscall 模式。
func NewPrivacyBridgeProgram(programID structure.PublicKey, loaderProgram structure.PublicKey, runtimeValue svm.Runtime) PrivacyBridgeProgram {
	return PrivacyBridgeProgram{programID: programID, loaderProgram: loaderProgram, runtime: runtimeValue}
}

// ProgramID 返回隐私程序 ID + 让 runtime 用同一交易格式分发到 VM 桥。
func (program PrivacyBridgeProgram) ProgramID() structure.PublicKey {
	if !program.programID.IsZero() {
		return program.programID
	}
	return structure.DefaultBuiltinProgramIDs.Privacy
}

// Execute 执行内置隐私 VM 字节码 + 字节码只负责 syscall 调度固定业务。
func (program PrivacyBridgeProgram) Execute(context runtime.InstructionContext) error {
	programData, err := PrivacyBridgeProgramData()
	if err != nil {
		return fmt.Errorf("vm privacy bridge: build program data: %w", err)
	}
	programID := program.programIDFor(context.BuiltinPrograms)
	loaderProgram := program.loaderProgramFor(context.BuiltinPrograms)
	programAccount := svm.ProgramAccount{
		Address:    vmAddressFromPublicKey(programID),
		Owner:      vmAddressFromPublicKey(loaderProgram),
		Executable: true,
		Data:       programData,
	}
	return executeProgramAccount(program.runtime, loaderProgram, programID, programAccount, context)
}

// PrivacyBridgeProgramData 返回内置 VM 字节码 + 内容等价于 syscall privacy_execute; exit。
func PrivacyBridgeProgramData() ([]byte, error) {
	privacyBridgeProgramDataOnce.Do(func() {
		dispatchInstruction, err := svm.BuildRegisterInstruction(svm.RegOpSyscall, 0, 0, 0, uint64(svm.SyscallPrivacyExecute))
		if err != nil {
			privacyBridgeProgramDataErr = err
			return
		}
		code, err := svm.BuildRegisterProgramCode(dispatchInstruction)
		if err != nil {
			privacyBridgeProgramDataErr = err
			return
		}
		privacyBridgeProgramData, privacyBridgeProgramDataErr = svm.EncodeRegisterBytecode(code, nil)
	})
	if privacyBridgeProgramDataErr != nil {
		return nil, privacyBridgeProgramDataErr
	}
	return utils.CloneBytes(privacyBridgeProgramData), nil
}

func (program PrivacyBridgeProgram) programIDFor(programIDs structure.BuiltinProgramIDs) structure.PublicKey {
	if !program.programID.IsZero() {
		return program.programID
	}
	if !programIDs.Privacy.IsZero() {
		return programIDs.Privacy
	}
	return structure.DefaultBuiltinProgramIDs.Privacy
}

func (program PrivacyBridgeProgram) loaderProgramFor(programIDs structure.BuiltinProgramIDs) structure.PublicKey {
	if !program.loaderProgram.IsZero() {
		return program.loaderProgram
	}
	if !programIDs.BPFLoader.IsZero() {
		return programIDs.BPFLoader
	}
	return structure.DefaultBuiltinProgramIDs.BPFLoader
}
