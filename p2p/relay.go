package p2p

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"solana_golang/codec/borsh"
	"solana_golang/utils"
)

const (
	// RelayMessageVersion 定义中继消息版本 + 支持后续中继协议平滑升级。
	RelayMessageVersion uint16 = 1

	defaultRelayMaxTTL      uint8 = 3
	defaultRelayPayloadSize       = 1024 * 1024
	defaultRelaySeenTTL           = 2 * time.Minute
	relayClockSkew                = 2 * time.Minute
	relayMessageDomain            = "p2p-relay-message-v1"
)

// RelayConfig 保存中继配置 + 让验证者、引导节点和归档节点按角色限制转发范围。
type RelayConfig struct {
	Disabled         bool
	MaxTTL           uint8
	MaxPayloadSize   int
	RequireSignature bool
	SeenTTL          time.Duration
	AllowedProtocols []ProtocolID
}

// RelayMessage 保存中继载荷 + 通过目标节点、TTL、签名和原始消息实现受控转发。
type RelayMessage struct {
	Version            uint16
	ID                 string
	SourcePeerID       string
	TargetPeerID       string
	PreviousHopPeerID  string
	TTL                uint8
	CreatedAtUnixMilli int64
	ProtocolID         ProtocolID
	Payload            []byte
	Signature          []byte
}

type relayService struct {
	mutex            sync.RWMutex
	config           RelayConfig
	routes           map[string]string
	seen             map[string]int64
	allowedProtocols map[ProtocolID]struct{}
}

func newRelayService(config RelayConfig) *relayService {
	normalized := normalizeRelayConfig(config)
	service := &relayService{
		config:           normalized,
		routes:           make(map[string]string),
		seen:             make(map[string]int64),
		allowedProtocols: make(map[ProtocolID]struct{}, len(normalized.AllowedProtocols)),
	}
	for _, protocolID := range normalized.AllowedProtocols {
		service.allowedProtocols[protocolID] = struct{}{}
	}
	return service
}

// RegisterRelayRoute 注册目标节点下一跳 + 内网节点反向连接公网节点后用于建立可达路径。
func (host *Host) RegisterRelayRoute(targetPeerID string, nextHopPeerID string) error {
	if err := validateOutboundPeerID(targetPeerID); err != nil {
		return err
	}
	if err := validateOutboundPeerID(nextHopPeerID); err != nil {
		return err
	}
	if targetPeerID == host.peerID || nextHopPeerID == host.peerID {
		return fmt.Errorf("%w: invalid relay self route", ErrInvalidMessage)
	}
	host.relay.registerRoute(targetPeerID, nextHopPeerID)
	host.logger.Info("p2p relay route registered",
		slog.String("target_peer_id", targetPeerID),
		slog.String("next_hop_peer_id", nextHopPeerID),
	)
	return nil
}

// SendRelayMessage 发送中继消息 + 用户连公网节点时按直连或路由转发到内网目标。
func (host *Host) SendRelayMessage(ctx context.Context, targetPeerID string, message Message) error {
	if err := validateOutboundPeerID(targetPeerID); err != nil {
		return err
	}
	if host.relay.disabled() {
		return fmt.Errorf("%w: relay disabled", ErrUnsupportedProtocol)
	}
	relayPayload, err := host.newRelayMessage(targetPeerID, message, "")
	if err != nil {
		return err
	}
	nextHopPeerID, err := host.relayOutboundNextHop(targetPeerID)
	if err != nil {
		return err
	}
	return host.sendRelayEnvelope(ctx, nextHopPeerID, relayPayload)
}

func (host *Host) handleRelayMessage(ctx context.Context, message Message) error {
	if host.relay.disabled() {
		return fmt.Errorf("%w: relay disabled", ErrUnsupportedProtocol)
	}
	relayPayload, err := UnmarshalRelayMessageBinary(message.Payload, host.relay.config.MaxPayloadSize)
	if err != nil {
		return err
	}
	if message.FromPeerID != "" {
		relayPayload.PreviousHopPeerID = message.FromPeerID
		host.relay.registerRoute(relayPayload.SourcePeerID, message.FromPeerID)
	}
	if err := host.relay.accept(relayPayload); err != nil {
		return err
	}
	if relayPayload.TargetPeerID == host.peerID {
		return host.consumeRelayMessage(ctx, relayPayload)
	}
	return host.forwardRelayMessage(ctx, relayPayload)
}

func (host *Host) consumeRelayMessage(ctx context.Context, relayPayload RelayMessage) error {
	innerMessage, err := UnmarshalBinary(relayPayload.Payload, host.maxMessageSize)
	if err != nil {
		return fmt.Errorf("p2p: relay unmarshal inner message: %w", err)
	}
	if innerMessage.Type != relayPayload.ProtocolID {
		return fmt.Errorf("%w: relay protocol mismatch", ErrInvalidMessage)
	}
	if innerMessage.ToPeerID != "" && innerMessage.ToPeerID != host.peerID {
		return fmt.Errorf("%w: relay inner target mismatch", ErrInvalidMessage)
	}
	result, err := host.HandleMessage(ctx, innerMessage)
	if err != nil {
		return err
	}
	if !result.HasResponse {
		host.logger.Debug("p2p relay consumed",
			slog.String("source_peer_id", relayPayload.SourcePeerID),
			slog.String("message_id", relayPayload.ID),
			slog.Uint64("protocol_id", uint64(relayPayload.ProtocolID)),
		)
		return nil
	}
	return host.SendRelayMessage(ctx, relayPayload.SourcePeerID, result.Message)
}

func (host *Host) forwardRelayMessage(ctx context.Context, relayPayload RelayMessage) error {
	if relayPayload.TTL == 0 {
		return fmt.Errorf("%w: relay ttl exhausted", ErrInvalidMessage)
	}
	relayPayload.TTL--
	if relayPayload.TTL == 0 {
		return fmt.Errorf("%w: relay ttl exhausted", ErrInvalidMessage)
	}
	nextHopPeerID, err := host.relayNextHop(relayPayload.TargetPeerID)
	if err != nil {
		return err
	}
	if nextHopPeerID == relayPayload.PreviousHopPeerID {
		return fmt.Errorf("%w: relay loop detected", ErrDuplicateMessage)
	}
	host.logger.Debug("p2p relay forwarded",
		slog.String("source_peer_id", relayPayload.SourcePeerID),
		slog.String("target_peer_id", relayPayload.TargetPeerID),
		slog.String("next_hop_peer_id", nextHopPeerID),
		slog.String("message_id", relayPayload.ID),
		slog.Uint64("ttl", uint64(relayPayload.TTL)),
	)
	return host.sendRelayEnvelope(ctx, nextHopPeerID, relayPayload)
}

func (host *Host) relayNextHop(targetPeerID string) (string, error) {
	if _, ok := host.Connection(targetPeerID); ok {
		return targetPeerID, nil
	}
	if nextHopPeerID, ok := host.relay.nextHop(targetPeerID); ok {
		return nextHopPeerID, nil
	}
	return "", fmt.Errorf("%w: relay route %s", ErrPeerNotFound, targetPeerID)
}

func (host *Host) relayOutboundNextHop(targetPeerID string) (string, error) {
	if nextHopPeerID, err := host.relayNextHop(targetPeerID); err == nil {
		return nextHopPeerID, nil
	}
	relays := host.connectedRelayPeers(targetPeerID)
	if len(relays) == 0 {
		return "", fmt.Errorf("%w: relay next hop %s", ErrPeerNotFound, targetPeerID)
	}
	return relays[0], nil
}

func (host *Host) connectedRelayPeers(targetPeerID string) []string {
	host.mutex.RLock()
	defer host.mutex.RUnlock()
	relays := make([]string, 0, len(host.connections))
	for peerID, connection := range host.connections {
		if peerID == "" || peerID == host.peerID || peerID == targetPeerID || connection == nil {
			continue
		}
		peer, ok := host.peers[peerID]
		if !ok || peer.Capabilities&PeerCapabilityRelay == 0 {
			continue
		}
		relays = append(relays, peerID)
	}
	return relays
}

func (host *Host) sendRelayEnvelope(ctx context.Context, nextHopPeerID string, relayPayload RelayMessage) error {
	encodedPayload, err := relayPayload.MarshalBinary(host.relay.config.MaxPayloadSize)
	if err != nil {
		return err
	}
	envelope, err := NewMessageWithMaxSize(ProtocolRelayMessageV1, encodedPayload, host.maxMessageSize)
	if err != nil {
		return err
	}
	return host.Send(ctx, nextHopPeerID, envelope)
}

func (host *Host) newRelayMessage(targetPeerID string, message Message, previousHopPeerID string) (RelayMessage, error) {
	outbound := message.Clone()
	prepared, err := host.prepareOutboundMessageFields(targetPeerID, outbound, false)
	if err != nil {
		return RelayMessage{}, err
	}
	payload, err := prepared.MarshalBinary(host.maxMessageSize)
	if err != nil {
		return RelayMessage{}, err
	}
	relayPayload := RelayMessage{
		Version:            RelayMessageVersion,
		ID:                 prepared.ID,
		SourcePeerID:       host.peerID,
		TargetPeerID:       targetPeerID,
		PreviousHopPeerID:  previousHopPeerID,
		TTL:                host.relay.config.MaxTTL,
		CreatedAtUnixMilli: time.Now().UnixMilli(),
		ProtocolID:         prepared.Type,
		Payload:            payload,
	}
	preSignConfig := host.relay.config
	preSignConfig.RequireSignature = false
	if err := relayPayload.Validate(preSignConfig); err != nil {
		return RelayMessage{}, err
	}
	if len(host.secureIdentity.PrivateKey) > 0 {
		if err := relayPayload.Sign(host.secureIdentity.PrivateKey); err != nil {
			return RelayMessage{}, err
		}
	}
	return relayPayload, relayPayload.Validate(host.relay.config)
}

// Sign 签名中继消息 + 将来源节点身份和不可变业务载荷绑定。
func (message *RelayMessage) Sign(privateKey []byte) error {
	if message == nil {
		return fmt.Errorf("%w: nil relay message", ErrInvalidMessage)
	}
	if err := message.validate(false, normalizeRelayConfig(RelayConfig{})); err != nil {
		return err
	}
	signingBytes, err := message.signingBytes()
	if err != nil {
		return err
	}
	signature, err := utils.Ed25519Sign(privateKey, signingBytes)
	if err != nil {
		return fmt.Errorf("%w: sign relay message: %w", ErrSecureSession, err)
	}
	message.Signature = signature
	return nil
}

// VerifySignature 验证中继签名 + 防止公网中继节点伪造来源和业务载荷。
func (message RelayMessage) VerifySignature() error {
	if len(message.Signature) != utils.Ed25519SignatureSize {
		return fmt.Errorf("%w: invalid relay signature size", ErrSecureSession)
	}
	publicKey, err := utils.Base58Decode(message.SourcePeerID)
	if err != nil {
		return fmt.Errorf("%w: invalid relay source public key: %w", ErrSecureSession, err)
	}
	signingBytes, err := message.signingBytes()
	if err != nil {
		return err
	}
	if !utils.Ed25519Verify(publicKey, signingBytes, message.Signature) {
		return fmt.Errorf("%w: invalid relay signature", ErrSecureSession)
	}
	return nil
}

func (message RelayMessage) MarshalBinary(maxPayloadSize int) ([]byte, error) {
	if err := message.Validate(RelayConfig{MaxPayloadSize: maxPayloadSize}); err != nil {
		return nil, err
	}
	writer := borsh.NewWriter(maxRelayPayloadSize(maxPayloadSize))
	writer.WriteUint16(message.Version)
	if err := writer.WriteString(strings.ToLower(message.ID)); err != nil {
		return nil, fmt.Errorf("p2p: marshal relay id: %w", err)
	}
	if err := writer.WriteString(message.SourcePeerID); err != nil {
		return nil, fmt.Errorf("p2p: marshal relay source: %w", err)
	}
	if err := writer.WriteString(message.TargetPeerID); err != nil {
		return nil, fmt.Errorf("p2p: marshal relay target: %w", err)
	}
	if err := writer.WriteString(message.PreviousHopPeerID); err != nil {
		return nil, fmt.Errorf("p2p: marshal relay previous hop: %w", err)
	}
	writer.WriteUint8(message.TTL)
	writer.WriteInt64(message.CreatedAtUnixMilli)
	writer.WriteUint32(uint32(message.ProtocolID))
	if err := writer.WriteBytes(message.Payload); err != nil {
		return nil, fmt.Errorf("p2p: marshal relay payload: %w", err)
	}
	if err := writer.WriteBytes(message.Signature); err != nil {
		return nil, fmt.Errorf("p2p: marshal relay signature: %w", err)
	}
	return writer.BytesView(), nil
}

func UnmarshalRelayMessageBinary(data []byte, maxPayloadSize int) (RelayMessage, error) {
	if len(data) == 0 || len(data) > maxRelayPayloadSize(maxPayloadSize) {
		return RelayMessage{}, fmt.Errorf("%w: invalid relay payload size", ErrInvalidMessage)
	}
	reader := borsh.NewBorrowedReader(data, maxRelayPayloadSize(maxPayloadSize))
	version, err := reader.ReadUint16()
	if err != nil {
		return RelayMessage{}, fmt.Errorf("p2p: read relay version: %w", err)
	}
	messageID, err := reader.ReadString()
	if err != nil {
		return RelayMessage{}, fmt.Errorf("p2p: read relay id: %w", err)
	}
	sourcePeerID, err := reader.ReadString()
	if err != nil {
		return RelayMessage{}, fmt.Errorf("p2p: read relay source: %w", err)
	}
	targetPeerID, err := reader.ReadString()
	if err != nil {
		return RelayMessage{}, fmt.Errorf("p2p: read relay target: %w", err)
	}
	previousHopPeerID, err := reader.ReadString()
	if err != nil {
		return RelayMessage{}, fmt.Errorf("p2p: read relay previous hop: %w", err)
	}
	ttl, err := reader.ReadUint8()
	if err != nil {
		return RelayMessage{}, fmt.Errorf("p2p: read relay ttl: %w", err)
	}
	createdAtUnixMilli, err := reader.ReadInt64()
	if err != nil {
		return RelayMessage{}, fmt.Errorf("p2p: read relay time: %w", err)
	}
	protocolID, err := reader.ReadUint32()
	if err != nil {
		return RelayMessage{}, fmt.Errorf("p2p: read relay protocol: %w", err)
	}
	payload, err := reader.ReadBytes()
	if err != nil {
		return RelayMessage{}, fmt.Errorf("p2p: read relay payload: %w", err)
	}
	signature, err := reader.ReadBytes()
	if err != nil {
		return RelayMessage{}, fmt.Errorf("p2p: read relay signature: %w", err)
	}
	if err := reader.EnsureEOF(); err != nil {
		return RelayMessage{}, fmt.Errorf("p2p: read relay eof: %w", err)
	}
	message := RelayMessage{
		Version:            version,
		ID:                 strings.ToLower(messageID),
		SourcePeerID:       sourcePeerID,
		TargetPeerID:       targetPeerID,
		PreviousHopPeerID:  previousHopPeerID,
		TTL:                ttl,
		CreatedAtUnixMilli: createdAtUnixMilli,
		ProtocolID:         ProtocolID(protocolID),
		Payload:            payload,
		Signature:          signature,
	}
	return message, message.Validate(RelayConfig{MaxPayloadSize: maxPayloadSize})
}

func (message RelayMessage) Validate(config RelayConfig) error {
	return message.validate(true, normalizeRelayConfig(config))
}

func (message RelayMessage) validate(allowUnsigned bool, config RelayConfig) error {
	if message.Version != RelayMessageVersion {
		return fmt.Errorf("%w: unsupported relay version", ErrInvalidMessage)
	}
	if _, err := messageIDBytes(message.ID); err != nil {
		return err
	}
	if err := validateMessagePeerID(message.SourcePeerID, false); err != nil {
		return err
	}
	if err := validateMessagePeerID(message.TargetPeerID, false); err != nil {
		return err
	}
	if err := validateMessagePeerID(message.PreviousHopPeerID, true); err != nil {
		return err
	}
	if message.SourcePeerID == message.TargetPeerID {
		return fmt.Errorf("%w: relay source equals target", ErrInvalidMessage)
	}
	if message.TTL == 0 || message.TTL > config.MaxTTL {
		return fmt.Errorf("%w: invalid relay ttl", ErrInvalidMessage)
	}
	if len(message.Payload) == 0 || len(message.Payload) > config.MaxPayloadSize {
		return fmt.Errorf("%w: invalid relay payload size", ErrInvalidMessage)
	}
	if message.CreatedAtUnixMilli <= 0 {
		return fmt.Errorf("%w: invalid relay time", ErrInvalidMessage)
	}
	now := time.Now()
	createdAt := time.UnixMilli(message.CreatedAtUnixMilli)
	if now.Add(relayClockSkew).Before(createdAt) || now.Add(-config.SeenTTL).After(createdAt) {
		return fmt.Errorf("%w: relay time outside window", ErrInvalidMessage)
	}
	if config.RequireSignature || len(message.Signature) > 0 {
		return message.VerifySignature()
	}
	if !allowUnsigned {
		return nil
	}
	return nil
}

func (message RelayMessage) signingBytes() ([]byte, error) {
	writer := borsh.NewWriter(maxRelayPayloadSize(defaultRelayPayloadSize))
	if err := writer.WriteString(relayMessageDomain); err != nil {
		return nil, fmt.Errorf("p2p: marshal relay domain: %w", err)
	}
	unsigned := message
	unsigned.Signature = nil
	payload, err := unsigned.marshalUnsignedBinary()
	if err != nil {
		return nil, err
	}
	if err := writer.WriteBytes(payload); err != nil {
		return nil, fmt.Errorf("p2p: marshal relay signing payload: %w", err)
	}
	return writer.BytesView(), nil
}

func (message RelayMessage) marshalUnsignedBinary() ([]byte, error) {
	writer := borsh.NewWriter(maxRelayPayloadSize(defaultRelayPayloadSize))
	writer.WriteUint16(message.Version)
	if err := writer.WriteString(strings.ToLower(message.ID)); err != nil {
		return nil, fmt.Errorf("p2p: marshal unsigned relay id: %w", err)
	}
	if err := writer.WriteString(message.SourcePeerID); err != nil {
		return nil, fmt.Errorf("p2p: marshal unsigned relay source: %w", err)
	}
	if err := writer.WriteString(message.TargetPeerID); err != nil {
		return nil, fmt.Errorf("p2p: marshal unsigned relay target: %w", err)
	}
	writer.WriteInt64(message.CreatedAtUnixMilli)
	writer.WriteUint32(uint32(message.ProtocolID))
	if err := writer.WriteBytes(message.Payload); err != nil {
		return nil, fmt.Errorf("p2p: marshal unsigned relay payload: %w", err)
	}
	return writer.BytesView(), nil
}

func (service *relayService) disabled() bool {
	if service == nil {
		return true
	}
	service.mutex.RLock()
	defer service.mutex.RUnlock()
	return service.config.Disabled
}

func (service *relayService) registerRoute(targetPeerID string, nextHopPeerID string) {
	service.mutex.Lock()
	defer service.mutex.Unlock()
	service.routes[targetPeerID] = nextHopPeerID
}

func (service *relayService) nextHop(targetPeerID string) (string, bool) {
	service.mutex.RLock()
	defer service.mutex.RUnlock()
	nextHopPeerID, ok := service.routes[targetPeerID]
	return nextHopPeerID, ok
}

func (service *relayService) accept(message RelayMessage) error {
	service.mutex.Lock()
	defer service.mutex.Unlock()
	if service.config.Disabled {
		return fmt.Errorf("%w: relay disabled", ErrUnsupportedProtocol)
	}
	if err := message.Validate(service.config); err != nil {
		return err
	}
	if _, ok := service.allowedProtocols[message.ProtocolID]; len(service.allowedProtocols) > 0 && !ok {
		return fmt.Errorf("%w: relay protocol not allowed", ErrUnsupportedProtocol)
	}
	nowUnixMilli := time.Now().UnixMilli()
	service.pruneSeenLocked(nowUnixMilli)
	if _, ok := service.seen[message.ID]; ok {
		return fmt.Errorf("%w: relay message %s", ErrDuplicateMessage, message.ID)
	}
	service.seen[message.ID] = nowUnixMilli + service.config.SeenTTL.Milliseconds()
	return nil
}

func (service *relayService) pruneSeenLocked(nowUnixMilli int64) {
	for messageID, expiresAtUnixMilli := range service.seen {
		if expiresAtUnixMilli <= nowUnixMilli {
			delete(service.seen, messageID)
		}
	}
}

func normalizeRelayConfig(config RelayConfig) RelayConfig {
	if config.MaxTTL == 0 || config.MaxTTL > defaultRelayMaxTTL {
		config.MaxTTL = defaultRelayMaxTTL
	}
	if config.MaxPayloadSize <= 0 || config.MaxPayloadSize > defaultRelayPayloadSize {
		config.MaxPayloadSize = defaultRelayPayloadSize
	}
	if config.SeenTTL <= 0 {
		config.SeenTTL = defaultRelaySeenTTL
	}
	if config.SeenTTL > time.Hour {
		config.SeenTTL = time.Hour
	}
	return config
}

func maxRelayPayloadSize(maxPayloadSize int) int {
	normalized := maxPayloadSize
	if normalized <= 0 || normalized > defaultRelayPayloadSize {
		normalized = defaultRelayPayloadSize
	}
	return normalized + 1024
}
