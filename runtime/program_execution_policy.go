package runtime

import (
	"fmt"
	"sort"
	"strings"

	"solana_golang/structure"
)

const (
	ProgramExecutionPolicyVersion = "program_execution_policy_v1"

	ProgramExecutionClassFixedBuiltin               ProgramExecutionClass = "fixed_builtin"
	ProgramExecutionClassVirtualMachineProgram      ProgramExecutionClass = "vm_program"
	ProgramExecutionClassVirtualMachineBridge       ProgramExecutionClass = "vm_bridge"
	ProgramExecutionClassVirtualMachineProgramOwner ProgramExecutionClass = "vm_program_owner"
)

// ProgramExecutionClass 标识执行入口类别 + 链身份需要稳定区分固定程序、VM 合约和桥接程序。
type ProgramExecutionClass string

// ProgramExecutionPolicyEntry 描述一个策略目标 + 用名称和地址生成稳定链身份指纹。
type ProgramExecutionPolicyEntry struct {
	Name      string
	ProgramID string
}

// ProgramExecutionPolicy 描述程序执行边界 + 明确简单业务固定执行、复杂业务按 loader 进入 VM。
type ProgramExecutionPolicy struct {
	FixedBuiltinPrograms         []ProgramExecutionPolicyEntry
	VirtualMachineProgramOwners  []ProgramExecutionPolicyEntry
	VirtualMachineBridgePrograms []ProgramExecutionPolicyEntry
}

// NewDefaultProgramExecutionPolicy 创建默认执行策略 + 隐私模式决定 privacy 是固定程序还是 VM syscall 桥。
func NewDefaultProgramExecutionPolicy(
	programIDs structure.BuiltinProgramIDs,
	privacyExecutionMode PrivacyExecutionMode,
) (ProgramExecutionPolicy, error) {
	if programIDs == (structure.BuiltinProgramIDs{}) {
		programIDs = structure.DefaultBuiltinProgramIDs
	}
	mode, err := NormalizePrivacyExecutionMode(privacyExecutionMode)
	if err != nil {
		return ProgramExecutionPolicy{}, err
	}
	policy := ProgramExecutionPolicy{
		FixedBuiltinPrograms: []ProgramExecutionPolicyEntry{
			{Name: "bpf_loader", ProgramID: programIDs.BPFLoader.String()},
			{Name: "system", ProgramID: programIDs.System.String()},
			{Name: "token", ProgramID: programIDs.Token.String()},
			{Name: "stake", ProgramID: programIDs.Stake.String()},
		},
		VirtualMachineProgramOwners: []ProgramExecutionPolicyEntry{
			{Name: "bpf_loader", ProgramID: programIDs.BPFLoader.String()},
		},
	}
	privacyEntry := ProgramExecutionPolicyEntry{Name: "privacy", ProgramID: programIDs.Privacy.String()}
	if mode == PrivacyExecutionModeVMSyscall {
		policy.VirtualMachineBridgePrograms = append(policy.VirtualMachineBridgePrograms, privacyEntry)
		return NormalizeProgramExecutionPolicy(policy), nil
	}
	policy.FixedBuiltinPrograms = append(policy.FixedBuiltinPrograms, privacyEntry)
	return NormalizeProgramExecutionPolicy(policy), nil
}

// NormalizeProgramExecutionPolicy 规范策略顺序 + 确保日志和链身份不会受配置顺序影响。
func NormalizeProgramExecutionPolicy(policy ProgramExecutionPolicy) ProgramExecutionPolicy {
	return ProgramExecutionPolicy{
		FixedBuiltinPrograms:         normalizeProgramExecutionPolicyEntries(policy.FixedBuiltinPrograms),
		VirtualMachineProgramOwners:  normalizeProgramExecutionPolicyEntries(policy.VirtualMachineProgramOwners),
		VirtualMachineBridgePrograms: normalizeProgramExecutionPolicyEntries(policy.VirtualMachineBridgePrograms),
	}
}

// Validate 校验执行策略 + 防止固定程序和 VM 桥接程序边界重叠。
func (policy ProgramExecutionPolicy) Validate() error {
	normalizedPolicy := NormalizeProgramExecutionPolicy(policy)
	if err := validateProgramExecutionPolicyEntries(ProgramExecutionClassFixedBuiltin, normalizedPolicy.FixedBuiltinPrograms); err != nil {
		return err
	}
	if err := validateProgramExecutionPolicyEntries(ProgramExecutionClassVirtualMachineProgramOwner, normalizedPolicy.VirtualMachineProgramOwners); err != nil {
		return err
	}
	if err := validateProgramExecutionPolicyEntries(ProgramExecutionClassVirtualMachineBridge, normalizedPolicy.VirtualMachineBridgePrograms); err != nil {
		return err
	}
	if len(normalizedPolicy.VirtualMachineProgramOwners) == 0 {
		return fmt.Errorf("%w: vm program owner is empty", ErrInvalidExecutionRequest)
	}
	return validateProgramExecutionPolicyDisjoint(normalizedPolicy)
}

// Fingerprint 返回策略指纹 + 链身份和日志用它快速确认执行边界。
func (policy ProgramExecutionPolicy) Fingerprint() string {
	normalizedPolicy := NormalizeProgramExecutionPolicy(policy)
	parts := []string{
		ProgramExecutionPolicyVersion,
		programExecutionPolicyPart(ProgramExecutionClassFixedBuiltin, normalizedPolicy.FixedBuiltinPrograms),
		programExecutionPolicyPart(ProgramExecutionClassVirtualMachineProgramOwner, normalizedPolicy.VirtualMachineProgramOwners),
		programExecutionPolicyPart(ProgramExecutionClassVirtualMachineBridge, normalizedPolicy.VirtualMachineBridgePrograms),
	}
	return strings.Join(parts, "|")
}

// IsZero 判断策略是否未配置 + 执行器用默认策略补齐观测字段。
func (policy ProgramExecutionPolicy) IsZero() bool {
	return len(policy.FixedBuiltinPrograms) == 0 &&
		len(policy.VirtualMachineProgramOwners) == 0 &&
		len(policy.VirtualMachineBridgePrograms) == 0
}

func defaultFixedProgramExecutionPolicy() ProgramExecutionPolicy {
	policy, err := NewDefaultProgramExecutionPolicy(structure.DefaultBuiltinProgramIDs, PrivacyExecutionModeFixed)
	if err != nil {
		panic(fmt.Sprintf("runtime: default program execution policy is invalid: %v", err))
	}
	return policy
}

func normalizeProgramExecutionPolicyEntries(entries []ProgramExecutionPolicyEntry) []ProgramExecutionPolicyEntry {
	normalizedEntries := make([]ProgramExecutionPolicyEntry, 0, len(entries))
	seenEntries := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		normalizedEntry := ProgramExecutionPolicyEntry{
			Name:      strings.TrimSpace(entry.Name),
			ProgramID: strings.TrimSpace(entry.ProgramID),
		}
		key := normalizedEntry.Name + "\x00" + normalizedEntry.ProgramID
		if _, exists := seenEntries[key]; exists {
			continue
		}
		seenEntries[key] = struct{}{}
		normalizedEntries = append(normalizedEntries, normalizedEntry)
	}
	sort.Slice(normalizedEntries, func(leftIndex int, rightIndex int) bool {
		leftEntry := normalizedEntries[leftIndex]
		rightEntry := normalizedEntries[rightIndex]
		if leftEntry.Name != rightEntry.Name {
			return leftEntry.Name < rightEntry.Name
		}
		return leftEntry.ProgramID < rightEntry.ProgramID
	})
	return normalizedEntries
}

func validateProgramExecutionPolicyEntries(class ProgramExecutionClass, entries []ProgramExecutionPolicyEntry) error {
	for index, entry := range entries {
		if entry.Name == "" {
			return fmt.Errorf("%w: %s entry %d name is empty", ErrInvalidExecutionRequest, class, index)
		}
		if entry.ProgramID == "" {
			return fmt.Errorf("%w: %s entry %d program id is empty", ErrInvalidExecutionRequest, class, index)
		}
		if _, err := structure.PublicKeyFromBase58(entry.ProgramID); err != nil {
			return fmt.Errorf("%w: %s entry %d program id: %w", ErrInvalidExecutionRequest, class, index, err)
		}
	}
	return nil
}

func validateProgramExecutionPolicyDisjoint(policy ProgramExecutionPolicy) error {
	fixedProgramIDs := programExecutionPolicyIDSet(policy.FixedBuiltinPrograms)
	for _, bridgeProgram := range policy.VirtualMachineBridgePrograms {
		if _, exists := fixedProgramIDs[bridgeProgram.ProgramID]; exists {
			return fmt.Errorf("%w: program %s cannot be fixed and vm bridge", ErrInvalidExecutionRequest, bridgeProgram.ProgramID)
		}
	}
	return nil
}

func programExecutionPolicyIDSet(entries []ProgramExecutionPolicyEntry) map[string]struct{} {
	set := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		set[entry.ProgramID] = struct{}{}
	}
	return set
}

func programExecutionPolicyPart(class ProgramExecutionClass, entries []ProgramExecutionPolicyEntry) string {
	values := make([]string, len(entries))
	for index, entry := range entries {
		values[index] = entry.Name + ":" + entry.ProgramID
	}
	return string(class) + "=" + strings.Join(values, ",")
}
