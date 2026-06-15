package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"solana_golang/database"
	"solana_golang/structure"
)

var mempoolKeyPrefix = []byte("pos/mempool/")

type storedMempoolTransaction struct {
	Transaction string `json:"transaction"`
	SubmitTime  int64  `json:"submit_time"`
	Fee         uint64 `json:"fee"`
}

func (node *posNode) loadMempool() error {
	if node.db == nil {
		return nil
	}
	values, err := node.db.PrefixQuery(database.TableChain, mempoolKeyPrefix)
	if err != nil {
		return fmt.Errorf("posnode: load mempool: %w", err)
	}
	nowMillis := time.Now().UnixMilli()
	for _, value := range values {
		stored := storedMempoolTransaction{}
		if err := json.Unmarshal(value.Value, &stored); err != nil {
			_ = node.db.Delete(database.TableChain, value.Key)
			continue
		}
		transactionBytes, err := base64.StdEncoding.DecodeString(stored.Transaction)
		if err != nil {
			_ = node.db.Delete(database.TableChain, value.Key)
			continue
		}
		transaction, err := structure.UnmarshalTransactionBinary(transactionBytes)
		if err != nil {
			continue
		}
		transaction.SubmitTime = stored.SubmitTime
		transaction.Fee = stored.Fee
		if transaction.IsExpiredWithTTL(nowMillis, node.config.MempoolTransactionTTLMillis) {
			_ = node.db.Delete(database.TableChain, value.Key)
			continue
		}
		transactionID, err := transaction.TxIDString()
		if err != nil {
			continue
		}
		if _, exists := node.seenTransactions[transactionID]; exists {
			continue
		}
		node.seenTransactions[transactionID] = struct{}{}
		node.mempool = append(node.mempool, transaction)
	}
	node.sortMempoolLocked()
	return nil
}

func (node *posNode) persistMempoolTransaction(transactionID string, transaction structure.Transaction) error {
	if node.db == nil {
		return nil
	}
	transactionBytes, err := transaction.MarshalBinary()
	if err != nil {
		return fmt.Errorf("posnode: marshal mempool transaction: %w", err)
	}
	payload, err := json.Marshal(storedMempoolTransaction{
		Transaction: base64.StdEncoding.EncodeToString(transactionBytes),
		SubmitTime:  transaction.SubmitTime,
		Fee:         transaction.Fee,
	})
	if err != nil {
		return fmt.Errorf("posnode: marshal mempool envelope: %w", err)
	}
	return node.db.Put(database.TableChain, mempoolKey(transactionID), payload)
}

func (node *posNode) deleteMempoolTransactions(transactionIDs []string) {
	if node.db == nil || len(transactionIDs) == 0 {
		return
	}
	operations := make([]database.DBOperation, 0, len(transactionIDs))
	for _, transactionID := range transactionIDs {
		operations = append(operations, database.NewDeleteOperation(database.TableChain, mempoolKey(transactionID)))
	}
	if err := node.db.DataTransaction(operations); err != nil {
		node.logger.Warn("posnode delete mempool transactions failed", "error", err)
	}
}

func (node *posNode) selectMempoolTransactionsLocked(nowMillis int64) ([]structure.Transaction, []string) {
	node.sortMempoolLocked()
	selected := make([]structure.Transaction, 0, len(node.mempool))
	remaining := make([]structure.Transaction, 0, len(node.mempool))
	removeIDs := make([]string, 0)
	writableLocks := make(map[structure.PublicKey]struct{})
	for _, transaction := range node.mempool {
		transactionID, err := transaction.TxIDString()
		if err != nil {
			continue
		}
		if transaction.IsExpiredWithTTL(nowMillis, node.config.MempoolTransactionTTLMillis) {
			removeIDs = append(removeIDs, transactionID)
			delete(node.seenTransactions, transactionID)
			continue
		}
		if transactionConflictsWithLocks(transaction, writableLocks) {
			remaining = append(remaining, transaction)
			continue
		}
		lockWritableAccounts(transaction, writableLocks)
		selected = append(selected, transaction)
		removeIDs = append(removeIDs, transactionID)
		delete(node.seenTransactions, transactionID)
	}
	node.mempool = remaining
	return selected, removeIDs
}

func (node *posNode) sortMempoolLocked() {
	sort.SliceStable(node.mempool, func(left int, right int) bool {
		if node.mempool[left].Fee == node.mempool[right].Fee {
			return node.mempool[left].SubmitTime < node.mempool[right].SubmitTime
		}
		return node.mempool[left].Fee > node.mempool[right].Fee
	})
}

func mempoolKey(transactionID string) []byte {
	key := make([]byte, 0, len(mempoolKeyPrefix)+len(transactionID))
	key = append(key, mempoolKeyPrefix...)
	key = append(key, []byte(transactionID)...)
	return key
}

func transactionConflictsWithLocks(transaction structure.Transaction, locks map[structure.PublicKey]struct{}) bool {
	for _, account := range transaction.Accounts {
		if !account.IsWritable {
			continue
		}
		if _, exists := locks[account.PublicKey]; exists {
			return true
		}
	}
	return false
}

func lockWritableAccounts(transaction structure.Transaction, locks map[structure.PublicKey]struct{}) {
	for _, account := range transaction.Accounts {
		if account.IsWritable {
			locks[account.PublicKey] = struct{}{}
		}
	}
}
