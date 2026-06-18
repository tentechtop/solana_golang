package assembler

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"solana_golang/vm"
)

type sourceLine struct {
	Number int
	Text   string
}

type dataSymbol struct {
	Offset uint64
	Data   []byte
}

type instructionPlan struct {
	Line     sourceLine
	Opcode   string
	Operands []string
	Index    uint64
}

// Assemble 编译 VM 汇编源码 + 输出可直接写入程序账户的字节码。
func Assemble(source string) ([]byte, error) {
	code, readOnlyData, err := AssembleCode(source)
	if err != nil {
		return nil, err
	}
	return vm.EncodeRegisterBytecode(code, readOnlyData)
}

// AssembleWithManifest 编译受治理的 VM 字节码 + 将合约能力声明写入部署产物。
func AssembleWithManifest(source string, manifest vm.ProgramManifest) ([]byte, error) {
	code, readOnlyData, err := AssembleCode(source)
	if err != nil {
		return nil, err
	}
	usedSyscalls, err := UsedSyscalls(source)
	if err != nil {
		return nil, err
	}
	if err := validateManifestSyscalls(manifest, usedSyscalls); err != nil {
		return nil, err
	}
	return vm.EncodeGovernedRegisterBytecode(code, readOnlyData, manifest)
}

// UsedSyscalls 提取源码使用的 syscall + 编译前校验 manifest 能力边界。
func UsedSyscalls(source string) ([]vm.SyscallID, error) {
	lines := normalizeLines(source)
	plans, _, _, err := planInstructions(lines)
	if err != nil {
		return nil, err
	}
	syscallSet := make(map[vm.SyscallID]struct{})
	for _, plan := range plans {
		if plan.Opcode != "syscall" || len(plan.Operands) == 0 {
			continue
		}
		syscallID, err := syscallIDByName(plan.Operands[0])
		if err != nil {
			return nil, lineError(plan.Line, "%s", err.Error())
		}
		syscallSet[syscallID] = struct{}{}
	}
	syscalls := make([]vm.SyscallID, 0, len(syscallSet))
	for syscallID := range syscallSet {
		syscalls = append(syscalls, syscallID)
	}
	sort.Slice(syscalls, func(leftIndex int, rightIndex int) bool {
		return syscalls[leftIndex] < syscalls[rightIndex]
	})
	return syscalls, nil
}

// AssembleCode 编译 VM 汇编源码 + 分离代码和只读数据便于 manifest 打包。
func AssembleCode(source string) ([]byte, []byte, error) {
	lines := normalizeLines(source)
	plans, labels, readOnlyData, err := planInstructions(lines)
	if err != nil {
		return nil, nil, err
	}
	code, err := encodePlans(plans, labels, readOnlyData)
	if err != nil {
		return nil, nil, err
	}
	return code, flattenData(readOnlyData), nil
}

func validateManifestSyscalls(manifest vm.ProgramManifest, usedSyscalls []vm.SyscallID) error {
	declared := make(map[vm.SyscallID]struct{}, len(manifest.RequiredSyscalls))
	for _, syscallID := range manifest.RequiredSyscalls {
		declared[syscallID] = struct{}{}
	}
	for _, syscallID := range usedSyscalls {
		if _, exists := declared[syscallID]; exists {
			continue
		}
		return fmt.Errorf("manifest missing required syscall %d", syscallID)
	}
	return nil
}

func normalizeLines(source string) []sourceLine {
	rawLines := strings.Split(source, "\n")
	lines := make([]sourceLine, 0, len(rawLines))
	for index, rawLine := range rawLines {
		text := stripComment(strings.TrimSpace(rawLine))
		if text == "" {
			continue
		}
		lines = append(lines, sourceLine{Number: index + 1, Text: text})
	}
	return lines
}

func planInstructions(lines []sourceLine) ([]instructionPlan, map[string]uint64, map[string]dataSymbol, error) {
	plans := make([]instructionPlan, 0, len(lines))
	labels := make(map[string]uint64)
	readOnlyData := make(map[string]dataSymbol)
	nextInstruction := uint64(0)
	nextDataOffset := uint64(0)
	for _, line := range lines {
		if strings.HasSuffix(line.Text, ":") {
			name := strings.TrimSuffix(line.Text, ":")
			if err := putLabel(labels, name, nextInstruction, line.Number); err != nil {
				return nil, nil, nil, err
			}
			continue
		}
		if strings.HasPrefix(line.Text, "data ") {
			offset, err := putData(readOnlyData, line.Text, nextDataOffset, line.Number)
			if err != nil {
				return nil, nil, nil, err
			}
			nextDataOffset = offset
			continue
		}
		plan, instructionCount, err := parseInstructionPlan(line, nextInstruction)
		if err != nil {
			return nil, nil, nil, err
		}
		plans = append(plans, plan)
		nextInstruction += instructionCount
	}
	return plans, labels, readOnlyData, nil
}

func encodePlans(plans []instructionPlan, labels map[string]uint64, readOnlyData map[string]dataSymbol) ([]byte, error) {
	code := make([]byte, 0, len(plans)*vm.RegisterInstructionSize)
	for _, plan := range plans {
		encoded, err := encodePlan(plan, labels, readOnlyData)
		if err != nil {
			return nil, err
		}
		code = append(code, encoded...)
	}
	return code, nil
}

func encodePlan(plan instructionPlan, labels map[string]uint64, readOnlyData map[string]dataSymbol) ([]byte, error) {
	switch plan.Opcode {
	case "exit":
		return build(plan, vm.RegOpExit, 0, 0, 0, 0)
	case "mov":
		return encodeMove(plan)
	case "add":
		return encodeBinary(plan, vm.RegOpAddReg, vm.RegOpAddImm)
	case "sub":
		return encodeBinary(plan, vm.RegOpSubReg, vm.RegOpSubImm)
	case "mul":
		return encodeRegisterOnlyBinary(plan, vm.RegOpMulReg)
	case "div":
		return encodeRegisterOnlyBinary(plan, vm.RegOpDivReg)
	case "load":
		return encodeLoad(plan)
	case "store":
		return encodeStore(plan)
	case "jmp":
		return encodeJump(plan, labels, vm.RegOpJmp)
	case "jz":
		return encodeConditionalJump(plan, labels, vm.RegOpJz)
	case "jnz":
		return encodeConditionalJump(plan, labels, vm.RegOpJnz)
	case "syscall":
		return encodeSyscall(plan, readOnlyData)
	default:
		return nil, lineError(plan.Line, "unknown opcode %s", plan.Opcode)
	}
}

func parseInstructionPlan(line sourceLine, index uint64) (instructionPlan, uint64, error) {
	fields := splitInstruction(line.Text)
	if len(fields) == 0 {
		return instructionPlan{}, 0, lineError(line, "empty instruction")
	}
	plan := instructionPlan{Line: line, Opcode: strings.ToLower(fields[0]), Operands: fields[1:], Index: index}
	if plan.Opcode == "syscall" && len(plan.Operands) == 2 {
		return plan, 3, nil
	}
	return plan, 1, nil
}

func encodeMove(plan instructionPlan) ([]byte, error) {
	if len(plan.Operands) != 2 {
		return nil, lineError(plan.Line, "mov needs 2 operands")
	}
	dst, err := parseRegister(plan.Operands[0])
	if err != nil {
		return nil, lineError(plan.Line, "%s", err.Error())
	}
	src, err := parseRegister(plan.Operands[1])
	if err == nil {
		return build(plan, vm.RegOpMovReg, dst, src, 0, 0)
	}
	imm, err := parseUint(plan.Operands[1])
	if err != nil {
		return nil, lineError(plan.Line, "invalid mov immediate %s", plan.Operands[1])
	}
	return build(plan, vm.RegOpMovImm, dst, 0, 0, imm)
}

func encodeBinary(plan instructionPlan, registerOpcode byte, immediateOpcode byte) ([]byte, error) {
	if len(plan.Operands) != 2 {
		return nil, lineError(plan.Line, "%s needs 2 operands", plan.Opcode)
	}
	dst, err := parseRegister(plan.Operands[0])
	if err != nil {
		return nil, lineError(plan.Line, "%s", err.Error())
	}
	src, err := parseRegister(plan.Operands[1])
	if err == nil {
		return build(plan, registerOpcode, dst, src, 0, 0)
	}
	imm, err := parseUint(plan.Operands[1])
	if err != nil {
		return nil, lineError(plan.Line, "invalid immediate %s", plan.Operands[1])
	}
	return build(plan, immediateOpcode, dst, 0, 0, imm)
}

func encodeRegisterOnlyBinary(plan instructionPlan, opcode byte) ([]byte, error) {
	if len(plan.Operands) != 2 {
		return nil, lineError(plan.Line, "%s needs 2 operands", plan.Opcode)
	}
	dst, err := parseRegister(plan.Operands[0])
	if err != nil {
		return nil, lineError(plan.Line, "%s", err.Error())
	}
	src, err := parseRegister(plan.Operands[1])
	if err != nil {
		return nil, lineError(plan.Line, "%s", err.Error())
	}
	return build(plan, opcode, dst, src, 0, 0)
}

func encodeLoad(plan instructionPlan) ([]byte, error) {
	if len(plan.Operands) != 2 {
		return nil, lineError(plan.Line, "load needs 2 operands")
	}
	dst, err := parseRegister(plan.Operands[0])
	if err != nil {
		return nil, lineError(plan.Line, "%s", err.Error())
	}
	base, offset, err := parseMemoryOperand(plan.Operands[1])
	if err != nil {
		return nil, lineError(plan.Line, "%s", err.Error())
	}
	return build(plan, vm.RegOpLoad, dst, base, offset, 0)
}

func encodeStore(plan instructionPlan) ([]byte, error) {
	if len(plan.Operands) != 2 {
		return nil, lineError(plan.Line, "store needs 2 operands")
	}
	base, offset, err := parseMemoryOperand(plan.Operands[0])
	if err != nil {
		return nil, lineError(plan.Line, "%s", err.Error())
	}
	src, err := parseRegister(plan.Operands[1])
	if err != nil {
		return nil, lineError(plan.Line, "%s", err.Error())
	}
	return build(plan, vm.RegOpStore, base, src, offset, 0)
}

func encodeJump(plan instructionPlan, labels map[string]uint64, opcode byte) ([]byte, error) {
	if len(plan.Operands) != 1 {
		return nil, lineError(plan.Line, "jump needs label")
	}
	target, err := resolveLabel(labels, plan.Operands[0])
	if err != nil {
		return nil, lineError(plan.Line, "%s", err.Error())
	}
	return build(plan, opcode, 0, 0, 0, target)
}

func encodeConditionalJump(plan instructionPlan, labels map[string]uint64, opcode byte) ([]byte, error) {
	if len(plan.Operands) != 2 {
		return nil, lineError(plan.Line, "%s needs register and label", plan.Opcode)
	}
	register, err := parseRegister(plan.Operands[0])
	if err != nil {
		return nil, lineError(plan.Line, "%s", err.Error())
	}
	target, err := resolveLabel(labels, plan.Operands[1])
	if err != nil {
		return nil, lineError(plan.Line, "%s", err.Error())
	}
	return build(plan, opcode, register, 0, 0, target)
}

func encodeSyscall(plan instructionPlan, readOnlyData map[string]dataSymbol) ([]byte, error) {
	if len(plan.Operands) != 1 && len(plan.Operands) != 2 {
		return nil, lineError(plan.Line, "syscall needs name and optional data")
	}
	syscallID, err := syscallIDByName(plan.Operands[0])
	if err != nil {
		return nil, lineError(plan.Line, "%s", err.Error())
	}
	if len(plan.Operands) == 1 {
		return build(plan, vm.RegOpSyscall, 0, 0, 0, uint64(syscallID))
	}
	symbol, exists := readOnlyData[plan.Operands[1]]
	if !exists {
		return nil, lineError(plan.Line, "unknown data symbol %s", plan.Operands[1])
	}
	code := make([]byte, 0, vm.RegisterInstructionSize*3)
	address := vm.RegisterMemoryBaseReadOnly + symbol.Offset
	code = appendBuilt(code, plan, vm.RegOpMovImm, 1, 0, 0, address)
	code = appendBuilt(code, plan, vm.RegOpMovImm, 2, 0, 0, uint64(len(symbol.Data)))
	code = appendBuilt(code, plan, vm.RegOpSyscall, 0, 0, 0, uint64(syscallID))
	return code, nil
}

func build(plan instructionPlan, opcode byte, dst byte, src byte, offset int32, immediate uint64) ([]byte, error) {
	encoded, err := vm.BuildRegisterInstruction(opcode, dst, src, offset, immediate)
	if err != nil {
		return nil, lineError(plan.Line, "%s", err.Error())
	}
	return encoded, nil
}

func appendBuilt(code []byte, plan instructionPlan, opcode byte, dst byte, src byte, offset int32, immediate uint64) []byte {
	encoded, err := build(plan, opcode, dst, src, offset, immediate)
	if err != nil {
		return code
	}
	return append(code, encoded...)
}

func putLabel(labels map[string]uint64, name string, value uint64, lineNumber int) error {
	if !isIdentifier(name) {
		return fmt.Errorf("line %d: invalid label %s", lineNumber, name)
	}
	if _, exists := labels[name]; exists {
		return fmt.Errorf("line %d: duplicate label %s", lineNumber, name)
	}
	labels[name] = value
	return nil
}

func putData(readOnlyData map[string]dataSymbol, text string, offset uint64, lineNumber int) (uint64, error) {
	parts := strings.SplitN(strings.TrimSpace(strings.TrimPrefix(text, "data ")), " ", 2)
	if len(parts) != 2 || !isIdentifier(parts[0]) {
		return 0, fmt.Errorf("line %d: invalid data declaration", lineNumber)
	}
	value, err := strconv.Unquote(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, fmt.Errorf("line %d: invalid data string: %w", lineNumber, err)
	}
	if _, exists := readOnlyData[parts[0]]; exists {
		return 0, fmt.Errorf("line %d: duplicate data symbol %s", lineNumber, parts[0])
	}
	data := []byte(value)
	readOnlyData[parts[0]] = dataSymbol{Offset: offset, Data: data}
	return offset + uint64(len(data)), nil
}

func flattenData(readOnlyData map[string]dataSymbol) []byte {
	totalLength := uint64(0)
	for _, symbol := range readOnlyData {
		if symbol.Offset+uint64(len(symbol.Data)) > totalLength {
			totalLength = symbol.Offset + uint64(len(symbol.Data))
		}
	}
	data := make([]byte, int(totalLength))
	for _, symbol := range readOnlyData {
		copy(data[symbol.Offset:], symbol.Data)
	}
	return data
}

func splitInstruction(text string) []string {
	replacer := strings.NewReplacer(",", " ", "\t", " ")
	return strings.Fields(replacer.Replace(text))
}

func stripComment(text string) string {
	if index := strings.Index(text, "#"); index >= 0 {
		return strings.TrimSpace(text[:index])
	}
	return text
}

func parseRegister(value string) (byte, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if !strings.HasPrefix(normalized, "r") {
		return 0, fmt.Errorf("invalid register %s", value)
	}
	number, err := strconv.ParseUint(strings.TrimPrefix(normalized, "r"), 10, 8)
	if err != nil || number >= vm.RegisterCount {
		return 0, fmt.Errorf("register %s out of range", value)
	}
	return byte(number), nil
}

func parseUint(value string) (uint64, error) {
	return strconv.ParseUint(strings.TrimSpace(value), 0, 64)
}

func parseMemoryOperand(value string) (byte, int32, error) {
	text := strings.TrimSpace(value)
	if !strings.HasPrefix(text, "[") || !strings.HasSuffix(text, "]") {
		return 0, 0, fmt.Errorf("invalid memory operand %s", value)
	}
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(text, "["), "]"))
	if strings.Contains(inner, "+") {
		return parseMemoryWithOffset(inner, "+")
	}
	if strings.Contains(inner, "-") {
		return parseMemoryWithOffset(inner, "-")
	}
	register, err := parseRegister(inner)
	return register, 0, err
}

func parseMemoryWithOffset(inner string, separator string) (byte, int32, error) {
	parts := strings.SplitN(inner, separator, 2)
	register, err := parseRegister(parts[0])
	if err != nil {
		return 0, 0, err
	}
	offset, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 0, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid memory offset %s", parts[1])
	}
	if separator == "-" {
		offset = -offset
	}
	return register, int32(offset), nil
}

func resolveLabel(labels map[string]uint64, name string) (uint64, error) {
	target, exists := labels[name]
	if !exists {
		return 0, fmt.Errorf("unknown label %s", name)
	}
	return target, nil
}

func syscallIDByName(name string) (vm.SyscallID, error) {
	switch strings.ToLower(name) {
	case "log", "sol_log":
		return vm.SyscallLog, nil
	case "sha256", "sol_sha256":
		return vm.SyscallSHA256, nil
	case "set_return_data", "sol_set_return_data":
		return vm.SyscallSetReturnData, nil
	case "get_clock", "sol_get_clock":
		return vm.SyscallGetClock, nil
	case "get_account_data", "sol_get_account_data":
		return vm.SyscallGetAccountData, nil
	case "set_account_data", "sol_set_account_data":
		return vm.SyscallSetAccountData, nil
	case "privacy_execute", "sol_privacy_execute":
		return vm.SyscallPrivacyExecute, nil
	case "stake_pool_execute", "sol_stake_pool_execute":
		return vm.SyscallStakePoolExecute, nil
	case "asset_execute", "sol_asset_execute":
		return vm.SyscallAssetExecute, nil
	default:
		return 0, fmt.Errorf("unknown syscall %s", name)
	}
}

func isIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for index, char := range value {
		isLetter := char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char == '_'
		isDigit := char >= '0' && char <= '9'
		if index == 0 && !isLetter {
			return false
		}
		if !isLetter && !isDigit {
			return false
		}
	}
	return true
}

func lineError(line sourceLine, format string, args ...any) error {
	return fmt.Errorf("line %d: %s", line.Number, fmt.Sprintf(format, args...))
}
