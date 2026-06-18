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
	if programIDs == (BuiltinProgramIDs{}) {
		programIDs = DefaultBuiltinProgramIDs
	}
	message, err := transaction.SolanaMessage()
	if err != nil {
		return ComputeBudgetLimits{}, err
	}
	if err := message.Validate(); err != nil {
		return ComputeBudgetLimits{}, fmt.Errorf("structure: estimate compute budget: %w", err)
	}
	limits := DefaultComputeBudgetLimits()
	computeUnits := uint64(0)
	storageWriteBytes := uint64(0)
	for instructionIndex, instruction := range message.Instructions {
		programID := message.AccountKeys[instruction.ProgramIDIndex]
		instructionUnits, instructionStorage, err := estimateInstructionBudget(programID, instruction, programIDs)
		if err != nil {
			return ComputeBudgetLimits{}, fmt.Errorf("structure: estimate instruction %d budget: %w", instructionIndex, err)
		}
		var addErr error
		computeUnits, addErr = safeAddUint64(computeUnits, instructionUnits)
		if addErr != nil {
			return ComputeBudgetLimits{}, fmt.Errorf("structure: compute budget overflow: %w", addErr)
		}
		storageWriteBytes, addErr = safeAddUint64(storageWriteBytes, instructionStorage)
		if addErr != nil {
			return ComputeBudgetLimits{}, fmt.Errorf("structure: storage budget overflow: %w", addErr)
		}
	}
	if computeUnits == 0 {
		computeUnits = DefaultBuiltinInstructionCU
	}
	if computeUnits > MaxComputeUnitsPerTransaction {
		computeUnits = MaxComputeUnitsPerTransaction
	}
	limits.MaxComputeUnits = computeUnits
	limits.StorageWriteBytesLimit = storageWriteBytes
	return limits, nil
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
	if programID == programIDs.Privacy {
		return DefaultContractCallComputeUnits, 0, nil
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
