package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
)

// HandlerFunc 处理单个 RPC 方法 + 返回业务结果或标准 JSON-RPC 错误。
type HandlerFunc func(ctx context.Context, params json.RawMessage) (any, *Error)

// Router 保存方法路由表 + 使用读写锁保证运行期注册和查询安全。
type Router struct {
	mu       sync.RWMutex
	handlers map[string]HandlerFunc
}

func NewRouter() *Router {
	return &Router{handlers: make(map[string]HandlerFunc)}
}
func NewDefaultRouter(backend LedgerBackend) *Router {
	router := NewRouter()
	RegisterDefaultHandlers(router, backend)
	return router
}
func (r *Router) Register(method string, handler HandlerFunc) error {
	method = strings.TrimSpace(method)
	if method == "" {
		return errors.New("rpc: method cannot be empty")
	}
	if handler == nil {
		return errors.New("rpc: handler cannot be nil")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[method] = handler
	return nil
}
func (r *Router) Handle(ctx context.Context, request Request) Response {
	if request.JSONRPC != jsonRPCVersion || strings.TrimSpace(request.Method) == "" {
		return errorResponse(request.ID, ErrInvalidRequest)
	}

	handler := r.lookup(request.Method)
	if handler == nil {
		return errorResponse(request.ID, methodNotFoundError(request.Method))
	}

	result, rpcError := handler(ctx, request.Params)
	if rpcError != nil {
		return errorResponse(request.ID, rpcError)
	}
	return successResponse(request.ID, result)
}
func (r *Router) lookup(method string) HandlerFunc {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.handlers[method]
}
