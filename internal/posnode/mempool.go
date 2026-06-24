package posnode

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"solana_golang/database"
	"solana_golang/structure"
)

var mempoolKeyPrefix = []byte("pos/mempool/")

const (
	minMempoolMaintenanceInterval = time.Second
	maxMempoolMaintenanceInterval = 5 * time.Second
)

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
		transaction, _, err = applyEstimatedTransactionFee(transaction)
		if err != nil {
			_ = node.db.Delete(database.TableChain, value.Key)
			continue
		}
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

// mempoolMaintenanceLoop 周期清理本地交易池 + 非 leader 节点也必须释放过期交易避免池满。
func (node *posNode) mempoolMaintenanceLoop(ctx context.Context) {
	ticker := time.NewTicker(node.mempoolMaintenanceInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			node.pruneMempool(time.Now().UnixMilli(), node.mempoolCurrentSlot())
		}
	}
}

func (node *posNode) mempoolMaintenanceInterval() time.Duration {
	interval := time.Duration(node.config.SlotMillis) * time.Millisecond
	if interval < minMempoolMaintenanceInterval {
		return minMempoolMaintenanceInterval
	}
	if interval > maxMempoolMaintenanceInterval {
		return maxMempoolMaintenanceInterval
	}
	return interval
}

func (node *posNode) mempoolCurrentSlot() uint64 {
	return node.transactionValidationSlot()
}

// transactionValidationSlot 统一交易时效校验槽位 + Solana 模式按已确认链头推进 recent blockhash 窗口。
func (node *posNode) transactionValidationSlot() uint64 {
	if node.ledger == nil {
		return 0
	}
	return node.ledger.Head().Slot
}

func (node *posNode) pruneMempool(nowMillis int64, currentSlot uint64) int {
	node.mutex.Lock()
	remaining := make([]structure.Transaction, 0, len(node.mempool))
	removeIDs := make([]string, 0)
	for _, transaction := range node.mempool {
		transactionID, err := transaction.TxIDString()
		if err != nil {
			continue
		}
		remove, reason := node.shouldPruneMempoolTransactionLocked(transactionID, transaction, nowMillis, currentSlot)
		if !remove {
			remaining = append(remaining, transaction)
			continue
		}
		removeIDs = append(removeIDs, transactionID)
		delete(node.seenTransactions, transactionID)
		if reason == "expired" || reason == "recent blockhash is not valid" {
			node.metrics.transactionsDrop.Add(1)
		}
		node.logger.Debug("posnode mempool transaction pruned",
			"tx_id", transactionID,
			"reason", reason,
			"current_slot", currentSlot,
		)
	}
	node.mempool = remaining
	node.mutex.Unlock()

	node.deleteMempoolTransactions(removeIDs)
	if len(removeIDs) > 0 {
		node.logger.Info("posnode mempool maintenance pruned",
			"removed", len(removeIDs),
			"remaining", len(remaining),
			"current_slot", currentSlot,
		)
	}
	return len(removeIDs)
}

func (node *posNode) shouldPruneMempoolTransactionLocked(transactionID string, transaction structure.Transaction, nowMillis int64, currentSlot uint64) (bool, string) {
	if transaction.IsExpiredWithTTL(nowMillis, node.config.MempoolTransactionTTLMillis) {
		return true, "expired"
	}
	if !node.transactionRecentBlockhashValidLocked(transaction, currentSlot) {
		return true, "recent blockhash is not valid"
	}
	committed, err := node.transactionAlreadyCommitted(transactionID)
	if err != nil {
		node.logger.Warn("posnode mempool committed transaction prune check failed",
			"tx_id", transactionID,
			"error", err,
		)
		return false, ""
	}
	if committed {
		return true, "committed"
	}
	return false, ""
}

func (node *posNode) selectMempoolTransactionsLocked(nowMillis int64, currentSlot uint64) ([]structure.Transaction, []string) {
	node.sortMempoolLocked()
	selected := make([]structure.Transaction, 0, len(node.mempool))
	remaining := make([]structure.Transaction, 0, len(node.mempool))
	removeIDs := make([]string, 0)
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
		if !node.transactionRecentBlockhashValidLocked(transaction, currentSlot) {
			removeIDs = append(removeIDs, transactionID)
			delete(node.seenTransactions, transactionID)
			node.metrics.transactionsDrop.Add(1)
			node.logger.Warn("posnode mempool transaction dropped",
				"tx_id", transactionID,
				"reason", "recent blockhash is not valid",
				"recent_blockhash", transaction.RecentBlockhash.String(),
				"current_slot", currentSlot,
			)
			continue
		}
		committed, err := node.transactionAlreadyCommitted(transactionID)
		if err != nil {
			node.logger.Warn("posnode mempool committed transaction check failed",
				"tx_id", transactionID,
				"error", err,
			)
			remaining = append(remaining, transaction)
			continue
		}
		if committed {
			removeIDs = append(removeIDs, transactionID)
			delete(node.seenTransactions, transactionID)
			continue
		}
		selected = append(selected, transaction)
		remaining = append(remaining, transaction)
	}
	node.mempool = remaining
	return selected, removeIDs
}

func (node *posNode) transactionRecentBlockhashValidLocked(transaction structure.Transaction, currentSlot uint64) bool {
	if len(node.blockhashQueue.Entries) == 0 {
		return true
	}
	return node.blockhashQueue.IsRecent(transaction.RecentBlockhash, currentSlot)
}

func (node *posNode) transactionAlreadyCommitted(transactionID string) (bool, error) {
	if node.ledger == nil || transactionID == "" {
		return false, nil
	}
	committed, err := node.ledger.HasCommittedTransaction(transactionID)
	if err != nil {
		return false, fmt.Errorf("posnode: query committed transaction index: %w", err)
	}
	return committed, nil
}

// mempoolTransactionByID 获取仍在池内的交易 + RPC 重试必须重新扩散真实待打包交易。
func (node *posNode) mempoolTransactionByID(transactionID string) (structure.Transaction, bool) {
	if transactionID == "" {
		return structure.Transaction{}, false
	}
	node.mutex.Lock()
	defer node.mutex.Unlock()
	if _, exists := node.seenTransactions[transactionID]; !exists {
		return structure.Transaction{}, false
	}
	transaction, exists := node.mempoolTransactionByIDLocked(transactionID)
	if !exists {
		delete(node.seenTransactions, transactionID)
		return structure.Transaction{}, false
	}
	return transaction, true
}

// mempoolTransactionByIDLocked 校验去重索引有效性 + seenTransactions 可能因异常路径残留。
func (node *posNode) mempoolTransactionByIDLocked(transactionID string) (structure.Transaction, bool) {
	for _, transaction := range node.mempool {
		currentTransactionID, err := transaction.TxIDString()
		if err != nil {
			continue
		}
		if currentTransactionID == transactionID {
			return transaction, true
		}
	}
	return structure.Transaction{}, false
}

// clearTransactionTracking 清理交易本地索引 + 已提交交易重复到达时不能被旧缓存误判为待处理。
func (node *posNode) clearTransactionTracking(transactionID string) {
	if transactionID == "" {
		return
	}
	node.mutex.Lock()
	defer node.mutex.Unlock()
	delete(node.seenTransactions, transactionID)
	delete(node.rejectedTransactions, transactionID)
}

func (node *posNode) removeCommittedMempoolTransactions(transactions []structure.Transaction) {
	if len(transactions) == 0 {
		return
	}
	transactionIDs := make(map[string]struct{}, len(transactions))
	for _, transaction := range transactions {
		transactionID, err := transaction.TxIDString()
		if err != nil {
			continue
		}
		transactionIDs[transactionID] = struct{}{}
	}
	if len(transactionIDs) == 0 {
		return
	}

	node.mutex.Lock()
	remaining := make([]structure.Transaction, 0, len(node.mempool))
	removeIDs := make([]string, 0, len(transactionIDs))
	for _, transaction := range node.mempool {
		transactionID, err := transaction.TxIDString()
		if err != nil {
			continue
		}
		if _, committed := transactionIDs[transactionID]; committed {
			removeIDs = append(removeIDs, transactionID)
			delete(node.seenTransactions, transactionID)
			continue
		}
		remaining = append(remaining, transaction)
	}
	node.mempool = remaining
	node.mutex.Unlock()

	node.deleteMempoolTransactions(removeIDs)
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
