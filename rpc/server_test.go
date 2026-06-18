package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type testLedgerBackend struct{}

func (b testLedgerBackend) GetBalance(context.Context, string) (BalanceResult, error) {
	return BalanceResult{Value: 100}, nil
}
func (b testLedgerBackend) GetLatestBlockhash(context.Context) (LatestBlockhashResult, error) {
	return LatestBlockhashResult{Blockhash: "test-blockhash", Slot: 10, Height: 9, LastValidSlot: 160}, nil
}
func (b testLedgerBackend) SendTransaction(context.Context, string) (string, error) {
	return "test-signature", nil
}
func (b testLedgerBackend) GetBlock(context.Context, uint64) (BlockResult, error) {
	return BlockResult{Slot: 10, Blockhash: "test-blockhash"}, nil
}

type testPublicBackend struct {
	testLedgerBackend
}

func (b testPublicBackend) GetNodeStatus(context.Context) (any, error) {
	return map[string]any{"head_height": float64(9)}, nil
}
func (b testPublicBackend) GetHealth(context.Context) (HealthResult, error) {
	return HealthResult{OK: true, HeadHeight: 9, HeadSlot: 10}, nil
}

func TestServerGetBalance(t *testing.T) {
	server := NewServer(ServerConfig{}, NewDefaultRouter(testLedgerBackend{}))
	response := postJSONRPC(t, server, `{"jsonrpc":"2.0","id":1,"method":"getBalance","params":["address"]}`)

	var decoded Response
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.Error != nil {
		t.Fatalf("response error = %+v", decoded.Error)
	}
	result, ok := decoded.Result.(map[string]any)
	if !ok || result["value"].(float64) != 100 {
		t.Fatalf("result = %#v, want value 100", decoded.Result)
	}
}
func TestServerGetLatestBlockhash(t *testing.T) {
	server := NewServer(ServerConfig{}, NewDefaultRouter(testLedgerBackend{}))
	response := postJSONRPC(t, server, `{"jsonrpc":"2.0","id":1,"method":"getLatestBlockhash","params":[]}`)

	var decoded Response
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.Error != nil {
		t.Fatalf("response error = %+v", decoded.Error)
	}
	result, ok := decoded.Result.(map[string]any)
	if !ok || result["blockhash"].(string) != "test-blockhash" {
		t.Fatalf("result = %#v, want latest blockhash", decoded.Result)
	}
}
func TestServerMethodNotFound(t *testing.T) {
	server := NewServer(ServerConfig{}, NewDefaultRouter(testLedgerBackend{}))
	response := postJSONRPC(t, server, `{"jsonrpc":"2.0","id":1,"method":"unknown","params":[]}`)

	var decoded Response
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.Error == nil || decoded.Error.Code != CodeMethodNotFound {
		t.Fatalf("error = %+v, want method not found", decoded.Error)
	}
}

func TestPublicRouterAllowsSignedTransactionSubmission(t *testing.T) {
	server := NewServer(ServerConfig{}, NewPublicRouter(testLedgerBackend{}))
	response := postJSONRPC(t, server, `{"jsonrpc":"2.0","id":1,"method":"sendTransaction","params":["tx"]}`)

	var decoded Response
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.Error != nil {
		t.Fatalf("response error = %+v", decoded.Error)
	}
	if decoded.Result.(string) != "test-signature" {
		t.Fatalf("result = %#v, want test signature", decoded.Result)
	}
}

func TestPublicRouterKeepsReadOnlyNodeStatus(t *testing.T) {
	server := NewServer(ServerConfig{}, NewPublicRouter(testPublicBackend{}))
	response := postJSONRPC(t, server, `{"jsonrpc":"2.0","id":1,"method":"getNodeStatus","params":[]}`)

	var decoded Response
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.Error != nil {
		t.Fatalf("response error = %+v", decoded.Error)
	}
	result := decoded.Result.(map[string]any)
	if result["head_height"].(float64) != 9 {
		t.Fatalf("result = %#v, want node status", decoded.Result)
	}
}

func TestPublicRouterDoesNotExposeManagementMethods(t *testing.T) {
	server := NewServer(ServerConfig{}, NewPublicRouter(testLedgerBackend{}))
	methods := []string{
		MethodTreasuryTransfer,
		MethodTransfer,
		MethodRegisterValidator,
		MethodStake,
		MethodUnstake,
		MethodSlashValidator,
		MethodJailValidator,
		MethodGetLocalValidatorIdentity,
		MethodGetPeerNetwork,
		MethodGetConsensusStatus,
		MethodGetMetrics,
	}
	for _, method := range methods {
		body := `{"jsonrpc":"2.0","id":1,"method":"` + method + `","params":[]}`
		response := postJSONRPC(t, server, body)
		var decoded Response
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("decode response for %s: %v", method, err)
		}
		if decoded.Error == nil || decoded.Error.Code != CodeMethodNotFound {
			t.Fatalf("%s error = %+v, want method not found", method, decoded.Error)
		}
	}
}
func TestServerInvalidParams(t *testing.T) {
	server := NewServer(ServerConfig{}, NewDefaultRouter(testLedgerBackend{}))
	response := postJSONRPC(t, server, `{"jsonrpc":"2.0","id":1,"method":"getBlock","params":["bad"]}`)

	var decoded Response
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.Error == nil || decoded.Error.Code != CodeInvalidParams {
		t.Fatalf("error = %+v, want invalid params", decoded.Error)
	}
}
func TestServerBatch(t *testing.T) {
	server := NewServer(ServerConfig{}, NewDefaultRouter(testLedgerBackend{}))
	response := postJSONRPC(t, server, `[
		{"jsonrpc":"2.0","id":1,"method":"getBalance","params":["address"]},
		{"jsonrpc":"2.0","id":2,"method":"sendTransaction","params":["tx"]}
	]`)

	var decoded []Response
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode batch response: %v", err)
	}
	if len(decoded) != 2 {
		t.Fatalf("batch response length = %d, want 2", len(decoded))
	}
	if decoded[0].Error != nil || decoded[1].Error != nil {
		t.Fatalf("batch errors = %+v %+v", decoded[0].Error, decoded[1].Error)
	}
}
func TestServerMethodUnavailableWithoutBackend(t *testing.T) {
	server := NewServer(ServerConfig{}, NewDefaultRouter(nil))
	response := postJSONRPC(t, server, `{"jsonrpc":"2.0","id":1,"method":"getBalance","params":["address"]}`)

	var decoded Response
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.Error == nil || decoded.Error.Code != CodeMethodUnavailable {
		t.Fatalf("error = %+v, want method unavailable", decoded.Error)
	}
}
func TestServerRejectsTrailingJSON(t *testing.T) {
	server := NewServer(ServerConfig{}, NewDefaultRouter(testLedgerBackend{}))
	response := postJSONRPC(t, server, `{"jsonrpc":"2.0","id":1,"method":"getBalance","params":["address"]}{}`)

	var decoded Response
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.Error == nil || decoded.Error.Code != CodeParseError {
		t.Fatalf("error = %+v, want parse error", decoded.Error)
	}
}
func TestServerWritesStructuredRequestLog(t *testing.T) {
	var logOutput bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logOutput, nil))
	server := NewServer(ServerConfig{Logger: logger}, NewDefaultRouter(testLedgerBackend{}))

	_ = postJSONRPC(t, server, `{"jsonrpc":"2.0","id":1,"method":"getBalance","params":["address"]}`)

	logLine := logOutput.String()
	if !strings.Contains(logLine, `"msg":"rpc http request completed"`) {
		t.Fatalf("log line = %q, want rpc request message", logLine)
	}
	if !strings.Contains(logLine, `"status":200`) {
		t.Fatalf("log line = %q, want status field", logLine)
	}
	if !strings.Contains(logLine, `"method":"POST"`) {
		t.Fatalf("log line = %q, want method field", logLine)
	}
}

func TestServerRejectsNonRootPath(t *testing.T) {
	server := NewServer(ServerConfig{}, NewDefaultRouter(testLedgerBackend{}))
	request := httptest.NewRequest(http.MethodPost, "/health", bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"getBalance","params":["address"]}`))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %s", response.Code, response.Body.String())
	}
	var decoded Response
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.Error == nil || decoded.Error.Code != CodeMethodNotFound {
		t.Fatalf("error = %+v, want method not found", decoded.Error)
	}
}

func postJSONRPC(t *testing.T, handler http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", response.Code, response.Body.String())
	}
	return response
}
