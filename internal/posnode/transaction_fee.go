package posnode

import (
	"fmt"

	"solana_golang/structure"
)

// estimateTransactionFeeDetails 估算交易手续费明细 + 入池排序和 RPC 展示必须基于节点可信费用。
func estimateTransactionFeeDetails(transaction structure.Transaction) (structure.FeeDetails, error) {
	limits, err := structure.EstimateTransactionComputeBudget(transaction, structure.DefaultBuiltinProgramIDs)
	if err != nil {
		return structure.FeeDetails{}, fmt.Errorf("posnode: estimate transaction budget: %w", err)
	}
	feeDetails, err := structure.DefaultFeeCalculator().Calculate(len(transaction.Signatures), limits)
	if err != nil {
		return structure.FeeDetails{}, fmt.Errorf("posnode: estimate transaction fee: %w", err)
	}
	return feeDetails, nil
}

// applyEstimatedTransactionFee 回填交易手续费字段 + Fee 不参与签名序列化，节点侧必须统一重算。
func applyEstimatedTransactionFee(transaction structure.Transaction) (structure.Transaction, structure.FeeDetails, error) {
	feeDetails, err := estimateTransactionFeeDetails(transaction)
	if err != nil {
		return structure.Transaction{}, structure.FeeDetails{}, err
	}
	normalizedTransaction := transaction.Clone()
	normalizedTransaction.Fee = feeDetails.TotalFee
	return normalizedTransaction, feeDetails, nil
}
