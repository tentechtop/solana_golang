package consensus

import (
	"fmt"

	"solana_golang/structure"
)

type blockResourceUsage struct {
	ComputeUnits uint64
	CostUnits    uint64
}

// selectBlockTransactionPrefix 按交易原序选择确定性前缀 + producer 只执行预算内候选，verifier 用同一规则确认提案完整落在上限内。
func selectBlockTransactionPrefix(
	transactions []structure.Transaction,
	maximumTransactionCount int,
) ([]structure.Transaction, blockResourceUsage, bool, error) {
	if maximumTransactionCount < 0 {
		return nil, blockResourceUsage{}, false, fmt.Errorf("consensus: invalid block transaction count limit %d", maximumTransactionCount)
	}
	selectedCount := len(transactions)
	if selectedCount > maximumTransactionCount {
		selectedCount = maximumTransactionCount
	}
	selected := make([]structure.Transaction, 0, selectedCount)
	usage := blockResourceUsage{}
	for transactionIndex, transaction := range transactions {
		if len(selected) >= maximumTransactionCount {
			return selected, usage, true, nil
		}
		demand, err := estimateBlockTransactionResourceDemand(transactionIndex, transaction)
		if err != nil {
			return nil, blockResourceUsage{}, false, err
		}
		nextUsage, exceedsLimit := addBlockResourceUsage(usage, demand)
		if exceedsLimit {
			return selected, usage, true, nil
		}
		selected = append(selected, transaction)
		usage = nextUsage
	}
	return selected, usage, false, nil
}

func validateBlockTransactionResources(transactions []structure.Transaction) error {
	selected, usage, truncated, err := selectBlockTransactionPrefix(transactions, MaxProposalTransactions)
	if err != nil {
		return err
	}
	if truncated || len(selected) != len(transactions) {
		return fmt.Errorf(
			"consensus: proposal resource demand exceeds block limit: accepted %d of %d transactions at %d compute and %d cost units",
			len(selected),
			len(transactions),
			usage.ComputeUnits,
			usage.CostUnits,
		)
	}
	return nil
}

func estimateBlockTransactionResourceDemand(transactionIndex int, transaction structure.Transaction) (blockResourceUsage, error) {
	computeUnits, _, err := structure.EstimateTransactionResourceDemand(transaction, structure.DefaultBuiltinProgramIDs)
	if err != nil {
		return blockResourceUsage{}, fmt.Errorf("consensus: estimate transaction %d resource demand: %w", transactionIndex, err)
	}
	if computeUnits > structure.MaxComputeUnitsPerTransaction {
		return blockResourceUsage{}, fmt.Errorf(
			"consensus: transaction %d compute demand %d exceeds transaction limit %d",
			transactionIndex,
			computeUnits,
			structure.MaxComputeUnitsPerTransaction,
		)
	}
	// 当前执行器的 CostUnits 与 ComputeUnits 使用同一确定性计量；独立成本模型上线前两者按相同需求准入。
	return blockResourceUsage{ComputeUnits: computeUnits, CostUnits: computeUnits}, nil
}

func addBlockResourceUsage(current blockResourceUsage, additional blockResourceUsage) (blockResourceUsage, bool) {
	if current.ComputeUnits > MaxBlockComputeUnits || current.CostUnits > MaxBlockCostUnits ||
		additional.ComputeUnits > MaxBlockComputeUnits-current.ComputeUnits ||
		additional.CostUnits > MaxBlockCostUnits-current.CostUnits {
		return current, true
	}
	return blockResourceUsage{
		ComputeUnits: current.ComputeUnits + additional.ComputeUnits,
		CostUnits:    current.CostUnits + additional.CostUnits,
	}, false
}

func addActualBlockResourceUsage(
	current blockResourceUsage,
	execution structure.TransactionExecutionResult,
	transactionIndex int,
) (blockResourceUsage, error) {
	additional := blockResourceUsage{
		ComputeUnits: execution.ComputeUnitsConsumed,
		CostUnits:    execution.CostUnits,
	}
	nextUsage, exceedsLimit := addBlockResourceUsage(current, additional)
	if exceedsLimit {
		return blockResourceUsage{}, fmt.Errorf(
			"consensus: transaction %d actual block resources exceed limit: consumed %d compute and %d cost units before transaction, additional %d/%d",
			transactionIndex,
			current.ComputeUnits,
			current.CostUnits,
			additional.ComputeUnits,
			additional.CostUnits,
		)
	}
	return nextUsage, nil
}
