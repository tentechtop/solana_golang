package structure

import "fmt"

const (
	loaderInstructionWrite       = uint32(0)
	loaderInstructionDeploy      = uint32(2)
	loaderInstructionUpgrade     = uint32(3)
	loaderInstructionHeaderBytes = 8
)

// EstimateTransactionComputeBudget 估算交易预算 + 在扣费前用确定性规则统一转账、固定程序和合约费用。
func EstimateTransactionComputeBudget(transaction Transaction, programIDs BuiltinProgramIDs) (ComputeBudgetLimits, error) {
	computeUnits, storageWriteBytes, err := EstimateTransactionResourceDemand(transaction, programIDs)
	if err != nil {
		return ComputeBudgetLimits{}, err
	}
	if computeUnits > MaxComputeUnitsPerTransaction {
		return ComputeBudgetLimits{}, fmt.Errorf("%w: estimated compute units %d exceeds transaction maximum %d", ErrInvalidFee, computeUnits, MaxComputeUnitsPerTransaction)
	}
	limits := DefaultComputeBudgetLimits()
	limits.MaxComputeUnits = computeUnits
	limits.StorageWriteBytesLimit = storageWriteBytes
	return limits, nil
}

// EstimateTransactionResourceDemand 返回未截断的交易资源需求 + 准入层据此拒绝超过执行上限的交易，避免按封顶费用执行超量工作。
func EstimateTransactionResourceDemand(transaction Transaction, programIDs BuiltinProgramIDs) (uint64, uint64, error) {
	if programIDs == (BuiltinProgramIDs{}) {
		programIDs = DefaultBuiltinProgramIDs
	}
	message, err := transaction.OrdexusMessage()
	if err != nil {
		return 0, 0, err
	}
	if err := message.Validate(); err != nil {
		return 0, 0, fmt.Errorf("structure: estimate resource demand: %w", err)
	}
	computeUnits := uint64(0)
	storageWriteBytes := uint64(0)
	for instructionIndex, instruction := range message.Instructions {
		if int(instruction.ProgramIDIndex) >= len(message.AccountKeys) {
			return 0, 0, fmt.Errorf("structure: estimate instruction %d: loaded program addresses are not resolved", instructionIndex)
		}
		programID := message.AccountKeys[instruction.ProgramIDIndex]
		instructionUnits, instructionStorage, err := estimateInstructionBudget(programID, instruction, programIDs)
		if err != nil {
			return 0, 0, fmt.Errorf("structure: estimate instruction %d budget: %w", instructionIndex, err)
		}
		var addErr error
		computeUnits, addErr = safeAddUint64(computeUnits, instructionUnits)
		if addErr != nil {
			return 0, 0, fmt.Errorf("structure: compute demand overflow: %w", addErr)
		}
		storageWriteBytes, addErr = safeAddUint64(storageWriteBytes, instructionStorage)
		if addErr != nil {
			return 0, 0, fmt.Errorf("structure: storage demand overflow: %w", addErr)
		}
	}
	if computeUnits == 0 {
		computeUnits = DefaultBuiltinInstructionCU
	}
	return computeUnits, storageWriteBytes, nil
}

func estimateInstructionBudget(
	programID PublicKey,
	instruction CompiledInstruction,
	programIDs BuiltinProgramIDs,
) (uint64, uint64, error) {
	if programID == programIDs.System && isLikelyTransferInstruction(instruction) {
		return DefaultTransferComputeUnits, 0, nil
	}
	if programID == programIDs.BPFLoader {
		return estimateLoaderInstructionBudget(instruction)
	}
	if programID == programIDs.Shielded {
		shieldedInstruction, err := UnmarshalShieldedTransferInstructionBinary(instruction.Data)
		if err != nil {
			return 0, 0, err
		}
		storageWriteBytes, err := ShieldedStorageWriteChargeBytes(shieldedInstruction.Bundle.Actions)
		if err != nil {
			return 0, 0, err
		}
		return ShieldedGroth16VerificationComputeUnits, storageWriteBytes, nil
	}
	if programIDs.IsBuiltinProgram(programID) {
		return DefaultBuiltinInstructionCU, 0, nil
	}
	return DefaultContractCallComputeUnits, 0, nil
}

func estimateLoaderInstructionBudget(instruction CompiledInstruction) (uint64, uint64, error) {
	instructionType, dataLength, ok := decodeLoaderInstructionBudgetData(instruction.Data)
	if !ok {
		return DefaultContractDeployBaseCU, uint64(len(instruction.Data)), nil
	}
	switch instructionType {
	case loaderInstructionWrite, loaderInstructionDeploy, loaderInstructionUpgrade:
		byteUnits, err := safeMulUint64(dataLength, DefaultContractDeployByteCU)
		if err != nil {
			return 0, 0, err
		}
		units, err := safeAddUint64(DefaultContractDeployBaseCU, byteUnits)
		if err != nil {
			return 0, 0, err
		}
		return units, dataLength, nil
	default:
		return DefaultBuiltinInstructionCU, 0, nil
	}
}

func decodeLoaderInstructionBudgetData(data []byte) (uint32, uint64, bool) {
	if len(data) < loaderInstructionHeaderBytes {
		return 0, 0, false
	}
	instructionType := uint32(data[0]) |
		uint32(data[1])<<8 |
		uint32(data[2])<<16 |
		uint32(data[3])<<24
	payloadLength := uint64(len(data) - loaderInstructionHeaderBytes)
	return instructionType, payloadLength, true
}

func isLikelyTransferInstruction(instruction CompiledInstruction) bool {
	return len(instruction.AccountIndexes) >= 2
}
