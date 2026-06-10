package p2p

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// ProtocolHandler 处理协议消息 + 返回值由协议定义决定是否必须存在。
type ProtocolHandler func(ctx context.Context, message Message) (Message, error)

// VoidProtocolHandler 处理无响应协议 + 简化广播类消息接入。
type VoidProtocolHandler func(ctx context.Context, message Message) error

// ResultProtocolHandler 处理有响应协议 + 强制调用方返回响应消息。
type ResultProtocolHandler func(ctx context.Context, message Message) (Message, error)

// ProtocolHandleResult 保存协议处理结果 + 明确区分空响应和处理失败。
type ProtocolHandleResult struct {
	Message     Message
	HasResponse bool
}

type registeredProtocol struct {
	spec    ProtocolSpec
	handler ProtocolHandler
}

// ProtocolRegistry 管理协议注册 + 使用读写锁保证并发路由安全。
type ProtocolRegistry struct {
	mutex    sync.RWMutex
	byID     map[ProtocolID]registeredProtocol
	nameToID map[string]ProtocolID
}

// NewProtocolRegistry 创建协议注册表 + 默认不注册处理器避免隐藏业务逻辑。
func NewProtocolRegistry() *ProtocolRegistry {
	return &ProtocolRegistry{
		byID:     make(map[ProtocolID]registeredProtocol),
		nameToID: make(map[string]ProtocolID),
	}
}

// RegisterVoidHandler 注册无响应处理器 + 校验协议定义必须声明无响应。
func (registry *ProtocolRegistry) RegisterVoidHandler(spec ProtocolSpec, handler VoidProtocolHandler) error {
	if handler == nil {
		return ErrNilProtocolHandler
	}
	if spec.HasResponse {
		return fmt.Errorf("%w: %s expects response", ErrProtocolResponseMismatch, spec.Name)
	}
	return registry.register(spec, func(ctx context.Context, message Message) (Message, error) {
		return Message{}, handler(ctx, message)
	})
}

// RegisterResultHandler 注册有响应处理器 + 校验协议定义必须声明有响应。
func (registry *ProtocolRegistry) RegisterResultHandler(spec ProtocolSpec, handler ResultProtocolHandler) error {
	if handler == nil {
		return ErrNilProtocolHandler
	}
	if !spec.HasResponse {
		return fmt.Errorf("%w: %s does not expect response", ErrProtocolResponseMismatch, spec.Name)
	}
	return registry.register(spec, func(ctx context.Context, message Message) (Message, error) {
		return handler(ctx, message)
	})
}

// Spec 查询协议定义 + 供上层检查协议优先级和响应语义。
func (registry *ProtocolRegistry) Spec(protocolID ProtocolID) (ProtocolSpec, bool) {
	registry.mutex.RLock()
	defer registry.mutex.RUnlock()
	registered, ok := registry.byID[protocolID]
	return registered.spec, ok
}

// SpecByName 按协议名查询协议定义 + 支持外部配置使用字符串协议名。
func (registry *ProtocolRegistry) SpecByName(name string) (ProtocolSpec, bool) {
	registry.mutex.RLock()
	defer registry.mutex.RUnlock()
	protocolID, ok := registry.nameToID[NormalizeProtocolName(name)]
	if !ok {
		return ProtocolSpec{}, false
	}
	registered, ok := registry.byID[protocolID]
	return registered.spec, ok
}

// Handle 执行协议处理器 + 按协议声明强制校验响应消息。
func (registry *ProtocolRegistry) Handle(ctx context.Context, message Message) (ProtocolHandleResult, error) {
	registered, err := registry.lookup(ProtocolID(message.Type))
	if err != nil {
		return ProtocolHandleResult{}, err
	}

	response, err := registered.handler(ctx, message)
	if err != nil {
		return ProtocolHandleResult{}, fmt.Errorf("p2p: handle protocol %s: %w", registered.spec.Name, err)
	}
	if !registered.spec.HasResponse {
		return ProtocolHandleResult{}, nil
	}
	if err := response.Validate(DefaultMaxMessageSize); err != nil {
		return ProtocolHandleResult{}, fmt.Errorf("%w: invalid response: %v", ErrProtocolResponseMismatch, err)
	}
	if !response.IsResponse() || !strings.EqualFold(response.RequestID, message.ID) {
		return ProtocolHandleResult{}, fmt.Errorf("%w: response does not match request", ErrProtocolResponseMismatch)
	}
	return ProtocolHandleResult{Message: response, HasResponse: true}, nil
}
func (registry *ProtocolRegistry) register(spec ProtocolSpec, handler ProtocolHandler) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	if handler == nil {
		return ErrNilProtocolHandler
	}

	normalizedName := spec.NormalizedName()
	if normalizedName == "" {
		return fmt.Errorf("%w: empty normalized name", ErrInvalidProtocol)
	}
	spec.Name = normalizedName

	registry.mutex.Lock()
	defer registry.mutex.Unlock()
	if _, exists := registry.byID[spec.ID]; exists {
		return fmt.Errorf("%w: duplicated protocol id %d", ErrProtocolConflict, spec.ID)
	}
	if existedID, exists := registry.nameToID[normalizedName]; exists && existedID != spec.ID {
		return fmt.Errorf("%w: duplicated protocol name %s", ErrProtocolConflict, normalizedName)
	}
	registry.byID[spec.ID] = registeredProtocol{spec: spec, handler: handler}
	registry.nameToID[normalizedName] = spec.ID
	return nil
}
func (registry *ProtocolRegistry) lookup(protocolID ProtocolID) (registeredProtocol, error) {
	registry.mutex.RLock()
	defer registry.mutex.RUnlock()
	registered, ok := registry.byID[protocolID]
	if !ok {
		return registeredProtocol{}, fmt.Errorf("%w: %d", ErrProtocolNotFound, protocolID)
	}
	if registered.handler == nil {
		return registeredProtocol{}, ErrNilProtocolHandler
	}
	return registered, nil
}
func responseFor(request Message, senderPeerID string, messageType MessageType, payload []byte) (Message, error) {
	response, err := NewResponseMessage(senderPeerID, messageType, request.ID, payload)
	if err != nil {
		return Message{}, err
	}
	response.ToPeerID = request.FromPeerID
	return response, nil
}
