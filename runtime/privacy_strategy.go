package runtime

import (
	"fmt"
	"strings"
)

// PrivacyExecutionMode 标识隐私程序执行策略 + 支持固定程序和 VM syscall 桥切换。
type PrivacyExecutionMode string

const (
	PrivacyExecutionModeFixed     PrivacyExecutionMode = "fixed"
	PrivacyExecutionModeVMSyscall PrivacyExecutionMode = "vm_syscall"
)

// NormalizePrivacyExecutionMode 规范隐私执行策略 + 配置为空时保持生产默认固定程序。
func NormalizePrivacyExecutionMode(mode PrivacyExecutionMode) (PrivacyExecutionMode, error) {
	switch strings.TrimSpace(strings.ToLower(string(mode))) {
	case "", string(PrivacyExecutionModeFixed):
		return PrivacyExecutionModeFixed, nil
	case string(PrivacyExecutionModeVMSyscall), "vm", "syscall":
		return PrivacyExecutionModeVMSyscall, nil
	default:
		return "", fmt.Errorf("%w: unsupported privacy execution mode %s", ErrInvalidExecutionRequest, mode)
	}
}
