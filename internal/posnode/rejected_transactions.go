package posnode

import (
	"time"

	"solana_golang/consensus"
	"solana_golang/rpc"
	"solana_golang/structure"
)

const (
	rejectedTransactionStatusTTLMillis = int64(5 * 60 * 1000)
	maxRejectedTransactionStatuses     = 4096
)

type rejectedTransactionStatus struct {
	Detail              rpc.TransactionDetailResult
	RecordedAtUnixMilli int64
}

func (node *posNode) removeRejectedMempoolTransactions(rejectedTransactions []consensus.RejectedTransaction, slot uint64) {
	if len(rejectedTransactions) == 0 {
		return
	}
	rejectedByID := make(map[string]consensus.RejectedTransaction, len(rejectedTransactions))
	for _, rejectedTransaction := range rejectedTransactions {
		if rejectedTransaction.TransactionID == "" {
			continue
		}
		rejectedByID[rejectedTransaction.TransactionID] = rejectedTransaction
	}
	if len(rejectedByID) == 0 {
		return
	}

	nowMillis := time.Now().UnixMilli()
	node.mutex.Lock()
	if node.rejectedTransactions == nil {
		node.rejectedTransactions = make(map[string]rejectedTransactionStatus)
	}
	remainingTransactions := make([]structure.Transaction, 0, len(node.mempool))
	removeTransactionIDs := make([]string, 0, len(rejectedByID))
	for _, transaction := range node.mempool {
		transactionID, err := transaction.TxIDString()
		if err != nil {
			continue
		}
		rejectedTransaction, rejected := rejectedByID[transactionID]
		if !rejected {
			remainingTransactions = append(remainingTransactions, transaction)
			continue
		}
		removeTransactionIDs = append(removeTransactionIDs, transactionID)
		delete(node.seenTransactions, transactionID)
		node.rejectedTransactions[transactionID] = rejectedTransactionStatus{
			Detail:              buildRejectedTransactionDetail(rejectedTransaction, slot),
			RecordedAtUnixMilli: nowMillis,
		}
	}
	node.mempool = remainingTransactions
	node.pruneRejectedTransactionsLocked(nowMillis)
	node.mutex.Unlock()

	node.deleteMempoolTransactions(removeTransactionIDs)
	if len(removeTransactionIDs) == 0 {
		return
	}
	node.metrics.transactionsDrop.Add(uint64(len(removeTransactionIDs)))
	node.logger.Warn("posnode mempool rejected transactions removed",
		"slot", slot,
		"count", len(removeTransactionIDs),
		"tx_ids", removeTransactionIDs,
	)
}

func (node *posNode) lookupRejectedTransaction(signature string) (rpc.TransactionDetailResult, bool) {
	node.mutex.Lock()
	defer node.mutex.Unlock()
	if len(node.rejectedTransactions) == 0 {
		return rpc.TransactionDetailResult{}, false
	}
	nowMillis := time.Now().UnixMilli()
	status, found := node.rejectedTransactions[signature]
	if !found {
		node.pruneRejectedTransactionsLocked(nowMillis)
		return rpc.TransactionDetailResult{}, false
	}
	if rejectedTransactionExpired(status, nowMillis) {
		delete(node.rejectedTransactions, signature)
		return rpc.TransactionDetailResult{}, false
	}
	return status.Detail, true
}

func buildRejectedTransactionDetail(rejectedTransaction consensus.RejectedTransaction, slot uint64) rpc.TransactionDetailResult {
	detail := buildTransactionDetailResult(
		rejectedTransaction.TransactionID,
		rejectedTransaction.Transaction,
		"rejected",
		"failed",
		0,
		slot,
		structure.Hash{},
		"",
		false,
	)
	detail.Error = rejectedTransaction.Error
	return detail
}

func (node *posNode) pruneRejectedTransactionsLocked(nowMillis int64) {
	for transactionID, status := range node.rejectedTransactions {
		if rejectedTransactionExpired(status, nowMillis) {
			delete(node.rejectedTransactions, transactionID)
		}
	}
	for len(node.rejectedTransactions) > maxRejectedTransactionStatuses {
		for transactionID := range node.rejectedTransactions {
			delete(node.rejectedTransactions, transactionID)
			break
		}
	}
}

func rejectedTransactionExpired(status rejectedTransactionStatus, nowMillis int64) bool {
	if status.RecordedAtUnixMilli <= 0 {
		return true
	}
	return nowMillis-status.RecordedAtUnixMilli > rejectedTransactionStatusTTLMillis
}
