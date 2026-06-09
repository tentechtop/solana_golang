package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	defaultMaxBodyBytes = int64(1 << 20)
	defaultMaxBatchSize = 32
	defaultReadTimeout  = 5 * time.Second
	defaultWriteTimeout = 10 * time.Second
	defaultIdleTimeout  = 60 * time.Second
)

// ServerConfig 定义 HTTP RPC 配置 + 限制资源使用防止请求放大。
type ServerConfig struct {
	Address      string
	MaxBodyBytes int64
	MaxBatchSize int
}

// Server 提供 HTTP JSON-RPC 服务 + 使用标准库减少外部依赖。
type Server struct {
	router       *Router
	maxBodyBytes int64
	maxBatchSize int
	httpServer   *http.Server
}

func NewServer(config ServerConfig, router *Router) *Server {
	if router == nil {
		router = NewDefaultRouter(nil)
	}
	if config.MaxBodyBytes <= 0 {
		config.MaxBodyBytes = defaultMaxBodyBytes
	}
	if config.MaxBatchSize <= 0 {
		config.MaxBatchSize = defaultMaxBatchSize
	}

	server := &Server{
		router:       router,
		maxBodyBytes: config.MaxBodyBytes,
		maxBatchSize: config.MaxBatchSize,
	}
	server.httpServer = &http.Server{
		Addr:         config.Address,
		Handler:      server,
		ReadTimeout:  defaultReadTimeout,
		WriteTimeout: defaultWriteTimeout,
		IdleTimeout:  defaultIdleTimeout,
	}
	return server
}

func (s *Server) ListenAndServe() error {
	if s.httpServer == nil {
		return errors.New("rpc: http server is nil")
	}
	if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("rpc: listen and serve: %w", err)
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.httpServer == nil {
		return nil
	}
	if err := s.httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("rpc: shutdown server: %w", err)
	}
	return nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(errorResponse(nil, ErrInvalidRequest))
		return
	}

	body, err := readRequestBody(w, r, s.maxBodyBytes)
	if err != nil {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_ = json.NewEncoder(w).Encode(errorResponse(nil, ErrRequestBodyTooLarge))
		return
	}

	response, batch := s.handleBody(r.Context(), body)
	if response == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if batch {
		_ = json.NewEncoder(w).Encode(response.([]Response))
		return
	}
	_ = json.NewEncoder(w).Encode(response.(Response))
}

func (s *Server) handleBody(ctx context.Context, body []byte) (any, bool) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return errorResponse(nil, ErrParseError), false
	}
	if trimmed[0] == '[' {
		return s.handleBatch(ctx, trimmed), true
	}
	return s.handleSingle(ctx, trimmed), false
}

func (s *Server) handleSingle(ctx context.Context, body []byte) Response {
	var request Request
	if err := decodeStrict(body, &request); err != nil {
		return errorResponse(nil, ErrParseError)
	}
	return s.router.Handle(ctx, request)
}

func (s *Server) handleBatch(ctx context.Context, body []byte) []Response {
	var requests []Request
	if err := decodeStrict(body, &requests); err != nil {
		return []Response{errorResponse(nil, ErrParseError)}
	}
	if len(requests) == 0 {
		return []Response{errorResponse(nil, ErrInvalidRequest)}
	}
	if len(requests) > s.maxBatchSize {
		return []Response{errorResponse(nil, ErrBatchSizeExceeded)}
	}

	responses := make([]Response, 0, len(requests))
	for _, request := range requests {
		responses = append(responses, s.router.Handle(ctx, request))
	}
	return responses
}

func readRequestBody(w http.ResponseWriter, r *http.Request, maxBodyBytes int64) ([]byte, error) {
	reader := http.MaxBytesReader(w, r.Body, maxBodyBytes)
	defer reader.Close()
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	return body, nil
}

func decodeStrict(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("rpc: trailing json values")
	}
	return nil
}
