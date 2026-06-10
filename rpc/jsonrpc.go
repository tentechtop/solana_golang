package rpc

import "encoding/json"

const jsonRPCVersion = "2.0"

// Request 表示 JSON-RPC 请求 + 使用 RawMessage 延迟解析参数避免重复序列化。
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response 表示 JSON-RPC 响应 + 按规范在 result 和 error 中二选一返回。
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Error 表示 JSON-RPC 错误对象 + 保持错误码稳定便于客户端处理。
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// successResponse 执行对应逻辑 + 保持函数职责清晰可维护。
func successResponse(id json.RawMessage, result any) Response {
	return Response{
		JSONRPC: jsonRPCVersion,
		ID:      normalizeID(id),
		Result:  result,
	}
}

// errorResponse 执行对应逻辑 + 保持函数职责清晰可维护。
func errorResponse(id json.RawMessage, rpcError *Error) Response {
	return Response{
		JSONRPC: jsonRPCVersion,
		ID:      normalizeID(id),
		Error:   rpcError,
	}
}

// normalizeID 执行对应逻辑 + 保持函数职责清晰可维护。
func normalizeID(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage("null")
	}
	return id
}
