package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"solana_golang/blockchain"
	"solana_golang/rpc"
	"solana_golang/structure"
)

type posNodeRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpc.Error      `json:"error,omitempty"`
}

func TestHTTPJSONRPCSubmitsSignedTransaction(t *testing.T) {
	node, source, destination := newRPCIntegrationTestNode(t)
	server := rpc.NewServer(rpc.ServerConfig{Logger: node.logger}, rpc.NewDefaultRouter(node))

	balanceResponse := postPosNodeJSONRPC(t, server, 1, rpc.MethodGetBalance, []any{source.PublicKey.String()})
	balance := decodePosNodeRPCResult[rpc.BalanceResult](t, balanceResponse)
	if balance.Value != 1_000_000_000 {
		t.Fatalf("balance = %d, want 1000000000", balance.Value)
	}

	transaction := newRPCTransferTransaction(t, node, source, destination.PublicKey, 10_000)
	encodedTransaction := encodeRPCTransaction(t, transaction)
	sendResponse := postPosNodeJSONRPC(t, server, 2, rpc.MethodSendTransaction, []any{encodedTransaction})
	signature := decodePosNodeRPCResult[string](t, sendResponse)
	if signature == "" {
		t.Fatal("sendTransaction signature is empty")
	}
	if len(node.mempool) != 1 {
		t.Fatalf("mempool size = %d, want 1", len(node.mempool))
	}
	if got := node.metrics.transactionsIn.Load(); got != 1 {
		t.Fatalf("transactionsIn = %d, want 1", got)
	}

	healthResponse := postPosNodeJSONRPC(t, server, 3, rpc.MethodGetHealth, []any{})
	health := decodePosNodeRPCResult[rpc.HealthResult](t, healthResponse)
	if !health.OK {
		t.Fatal("health ok = false, want true")
	}
	if health.MempoolSize != 1 {
		t.Fatalf("health mempool size = %d, want 1", health.MempoolSize)
	}
}

func TestHTTPJSONRPCRejectsInvalidSignedTransaction(t *testing.T) {
	node, source, destination := newRPCIntegrationTestNode(t)
	server := rpc.NewServer(rpc.ServerConfig{Logger: node.logger}, rpc.NewDefaultRouter(node))
	transaction := newRPCTransferTransaction(t, node, source, destination.PublicKey, 10_000)
	transaction.Signatures[0][0] ^= 0xff
	encodedTransaction := encodeRPCTransaction(t, transaction)

	response := postPosNodeJSONRPC(t, server, 1, rpc.MethodSendTransaction, []any{encodedTransaction})
	if response.Error == nil {
		t.Fatalf("response error = nil, want invalid signature error")
	}
	if len(node.mempool) != 0 {
		t.Fatalf("mempool size = %d, want 0", len(node.mempool))
	}
	if got := node.metrics.transactionsDrop.Load(); got != 1 {
		t.Fatalf("transactionsDrop = %d, want 1", got)
	}
}

func newRPCIntegrationTestNode(t *testing.T) (*posNode, structure.SolanaKeyPair, structure.SolanaKeyPair) {
	t.Helper()
	source := mustStructureKeyPair("rpc-source")
	destination := mustStructureKeyPair("rpc-destination")
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	ledger, err := blockchain.NewLedgerFromGenesis(nil, blockchain.GenesisConfig{
		ChainID: "posnode-rpc-e2e",
		FundedAccounts: []blockchain.GenesisAccount{
			{Address: source.PublicKey, Lamports: 1_000_000_000},
			{Address: destination.PublicKey, Lamports: 1_000_000},
		},
	})
	if err != nil {
		t.Fatalf("NewLedgerFromGenesis() error = %v", err)
	}
	ledger.SetLogger(logger)
	node := &posNode{
		config: nodeConfig{
			MempoolMaxTransactions:      10,
			MempoolTransactionTTLMillis: 60_000,
		},
		logger:           logger,
		ledger:           ledger,
		seenTransactions: make(map[string]struct{}),
	}
	return node, source, destination
}

func newRPCTransferTransaction(t *testing.T, node *posNode, source structure.SolanaKeyPair, destination structure.PublicKey, amount uint64) structure.Transaction {
	t.Helper()
	transaction, err := blockchain.NewTransferTransaction(source, destination, amount, node.ledger.Head().BlockHash)
	if err != nil {
		t.Fatalf("NewTransferTransaction() error = %v", err)
	}
	return transaction
}

func encodeRPCTransaction(t *testing.T, transaction structure.Transaction) string {
	t.Helper()
	transactionBytes, err := transaction.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	return base64.StdEncoding.EncodeToString(transactionBytes)
}

func postPosNodeJSONRPC(t *testing.T, handler http.Handler, id int, method string, params []any) posNodeRPCResponse {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", response.Code, response.Body.String())
	}

	var decoded posNodeRPCResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return decoded
}

func decodePosNodeRPCResult[T any](t *testing.T, response posNodeRPCResponse) T {
	t.Helper()
	var zero T
	if response.Error != nil {
		t.Fatalf("response error = %+v", response.Error)
	}
	if len(response.Result) == 0 {
		t.Fatal("response result is empty")
	}
	if err := json.Unmarshal(response.Result, &zero); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	return zero
}
