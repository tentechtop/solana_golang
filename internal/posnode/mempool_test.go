package posnode

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"solana_golang/blockchain"
	"solana_golang/structure"
)

func TestAddTransactionAcceptsSignedUserTransaction(t *testing.T) {
	node := newMempoolTestNode(10, 60_000)
	transaction := newMempoolTransfer(t, "mempool-source", "mempool-destination", 100)

	if err := node.addTransaction(transaction); err != nil {
		t.Fatalf("addTransaction() error = %v", err)
	}
	if len(node.mempool) != 1 {
		t.Fatalf("mempool size = %d, want 1", len(node.mempool))
	}
	expectedFee, err := estimateTransactionFeeDetails(transaction)
	if err != nil {
		t.Fatalf("estimateTransactionFeeDetails() error = %v", err)
	}
	if node.mempool[0].Fee != expectedFee.TotalFee {
		t.Fatalf("mempool fee = %d, want %d", node.mempool[0].Fee, expectedFee.TotalFee)
	}
	if err := node.addTransaction(transaction); err != nil {
		t.Fatalf("duplicate addTransaction() error = %v", err)
	}
	if len(node.mempool) != 1 {
		t.Fatalf("duplicate mempool size = %d, want 1", len(node.mempool))
	}
	if got := node.metrics.transactionsIn.Load(); got != 1 {
		t.Fatalf("transactionsIn = %d, want 1", got)
	}
}

func TestAddTransactionRejectsFullAndExpiredMempool(t *testing.T) {
	fullNode := newMempoolTestNode(1, 60_000)
	firstTransaction := newMempoolTransfer(t, "full-source-1", "full-destination-1", 100)
	secondTransaction := newMempoolTransfer(t, "full-source-2", "full-destination-2", 100)
	if err := fullNode.addTransaction(firstTransaction); err != nil {
		t.Fatalf("add first transaction: %v", err)
	}
	if err := fullNode.addTransaction(secondTransaction); err == nil {
		t.Fatal("addTransaction() error = nil, want mempool full")
	}
	if got := fullNode.metrics.transactionsDrop.Load(); got != 1 {
		t.Fatalf("transactionsDrop after full = %d, want 1", got)
	}

	expiredNode := newMempoolTestNode(10, 1)
	expiredTransaction := newMempoolTransfer(t, "expired-source", "expired-destination", 100)
	expiredTransaction.SubmitTime = time.Now().Add(-time.Minute).UnixMilli()
	if err := expiredNode.addTransaction(expiredTransaction); err == nil {
		t.Fatal("addTransaction() error = nil, want expired transaction")
	}
	if got := expiredNode.metrics.transactionsDrop.Load(); got != 1 {
		t.Fatalf("transactionsDrop after expired = %d, want 1", got)
	}
}

func TestAddTransactionRejectsInvalidSignature(t *testing.T) {
	node := newMempoolTestNode(10, 60_000)
	transaction := newMempoolTransfer(t, "bad-signature-source", "bad-signature-destination", 100)
	transaction.Signatures[0][0] ^= 0xff

	if err := node.addTransaction(transaction); err == nil {
		t.Fatal("addTransaction() error = nil, want invalid signature")
	}
	if len(node.mempool) != 0 {
		t.Fatalf("mempool size = %d, want 0", len(node.mempool))
	}
	if got := node.metrics.transactionsDrop.Load(); got != 1 {
		t.Fatalf("transactionsDrop = %d, want 1", got)
	}
}

func TestSelectMempoolTransactionsRemovesExpiredSeenID(t *testing.T) {
	node := newMempoolTestNode(10, 1)
	transaction := newMempoolTransfer(t, "select-source", "select-destination", 100)
	transaction.SubmitTime = time.Now().UnixMilli()
	transactionID, err := transaction.TxIDString()
	if err != nil {
		t.Fatalf("TxIDString() error = %v", err)
	}

	node.mempool = append(node.mempool, transaction)
	node.seenTransactions[transactionID] = struct{}{}
	selected, removed := node.selectMempoolTransactionsLocked(time.Now().Add(time.Minute).UnixMilli(), 1)
	if len(selected) != 0 {
		t.Fatalf("selected = %d, want 0", len(selected))
	}
	if len(removed) != 1 || removed[0] != transactionID {
		t.Fatalf("removed = %+v, want %s", removed, transactionID)
	}
	if _, exists := node.seenTransactions[transactionID]; exists {
		t.Fatal("expired transaction id still marked as seen")
	}
}

func TestSelectMempoolTransactionsRetainsSelectedUntilCommit(t *testing.T) {
	node := newMempoolTestNode(10, 60_000)
	transaction := newMempoolTransfer(t, "selected-source", "selected-destination", 100)
	transactionID, err := transaction.TxIDString()
	if err != nil {
		t.Fatalf("TxIDString() error = %v", err)
	}

	node.mempool = append(node.mempool, transaction)
	node.seenTransactions[transactionID] = struct{}{}
	selected, removed := node.selectMempoolTransactionsLocked(time.Now().UnixMilli(), 1)
	if len(selected) != 1 {
		t.Fatalf("selected = %d, want 1", len(selected))
	}
	if len(removed) != 0 {
		t.Fatalf("removed = %+v, want empty", removed)
	}
	if len(node.mempool) != 1 {
		t.Fatalf("mempool size = %d, want 1", len(node.mempool))
	}
	if _, exists := node.seenTransactions[transactionID]; !exists {
		t.Fatal("selected transaction id was removed before commit")
	}

	node.removeCommittedMempoolTransactions(selected)
	if len(node.mempool) != 0 {
		t.Fatalf("mempool size after commit = %d, want 0", len(node.mempool))
	}
	if _, exists := node.seenTransactions[transactionID]; exists {
		t.Fatal("committed transaction id still marked as seen")
	}
}

func TestSelectMempoolTransactionsDropsInvalidRecentBlockhash(t *testing.T) {
	node := newMempoolTestNode(10, 60_000)
	validBlockhash := mustHash("valid-mempool-blockhash")
	if err := node.blockhashQueue.Add(structure.RecentBlockhashEntry{
		Blockhash:     validBlockhash,
		Slot:          10,
		FeeCalculator: structure.DefaultFeeCalculator(),
	}); err != nil {
		t.Fatalf("add valid blockhash: %v", err)
	}
	transaction := newMempoolTransfer(t, "stale-blockhash-source", "stale-blockhash-destination", 100)
	transactionID, err := transaction.TxIDString()
	if err != nil {
		t.Fatalf("TxIDString() error = %v", err)
	}
	node.mempool = append(node.mempool, transaction)
	node.seenTransactions[transactionID] = struct{}{}

	selected, removed := node.selectMempoolTransactionsLocked(time.Now().UnixMilli(), 10)
	if len(selected) != 0 {
		t.Fatalf("selected = %d, want 0", len(selected))
	}
	if len(removed) != 1 || removed[0] != transactionID {
		t.Fatalf("removed = %+v, want %s", removed, transactionID)
	}
	if _, exists := node.seenTransactions[transactionID]; exists {
		t.Fatal("invalid recent blockhash transaction id still marked as seen")
	}
	if got := node.metrics.transactionsDrop.Load(); got != 1 {
		t.Fatalf("transactionsDrop = %d, want 1", got)
	}
}

func TestAddTransactionRejectsInvalidRecentBlockhash(t *testing.T) {
	node := newMempoolTestNode(10, 60_000)
	validBlockhash := mustHash("rpc-valid-blockhash")
	if err := node.blockhashQueue.Add(structure.RecentBlockhashEntry{
		Blockhash:     validBlockhash,
		Slot:          7,
		FeeCalculator: structure.DefaultFeeCalculator(),
	}); err != nil {
		t.Fatalf("add valid blockhash: %v", err)
	}
	transaction := newMempoolTransfer(t, "rpc-stale-blockhash-source", "rpc-stale-blockhash-destination", 100)

	if err := node.addTransaction(transaction); err == nil {
		t.Fatal("addTransaction() error = nil, want invalid recent blockhash")
	}
	if len(node.mempool) != 0 {
		t.Fatalf("mempool size = %d, want 0", len(node.mempool))
	}
	if got := node.metrics.transactionsDrop.Load(); got != 1 {
		t.Fatalf("transactionsDrop = %d, want 1", got)
	}
}

func newMempoolTestNode(maxTransactions int, ttlMillis int64) *posNode {
	return &posNode{
		config: nodeConfig{
			MempoolMaxTransactions:      maxTransactions,
			MempoolTransactionTTLMillis: ttlMillis,
		},
		logger:           slog.New(slog.NewJSONHandler(io.Discard, nil)),
		seenTransactions: make(map[string]struct{}),
	}
}

func newMempoolTransfer(t *testing.T, sourceSeed string, destinationSeed string, amount uint64) structure.Transaction {
	t.Helper()
	source := mustStructureKeyPair(sourceSeed)
	destination := mustStructureKeyPair(destinationSeed)
	transaction, err := blockchain.NewTransferTransaction(source, destination.PublicKey, amount, mustHash(sourceSeed+"-blockhash"))
	if err != nil {
		t.Fatalf("NewTransferTransaction() error = %v", err)
	}
	return transaction
}
