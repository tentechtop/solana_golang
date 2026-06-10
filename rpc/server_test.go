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

func (b testLedgerBackend) SendTransaction(context.Context, string) (string, error) {
	return "test-signature", nil
}

func (b testLedgerBackend) GetBlock(context.Context, uint64) (BlockResult, error) {
	return BlockResult{Slot: 10, Blockhash: "test-blockhash"}, nil
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
