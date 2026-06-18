package p2p

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"time"

	"solana_golang/codec/borsh"
	"solana_golang/utils"
)

const (
	// QUICHolePunchVersion 定义 QUIC 打洞协议版本 + 支持后续协调流程升级。
	QUICHolePunchVersion uint16 = 1

	quicHolePunchKindRequest   uint8 = 1
	quicHolePunchKindIntroduce uint8 = 2
	quicHolePunchKindAck       uint8 = 3

	defaultHolePunchDelay        = 300 * time.Millisecond
	defaultHolePunchTimeout      = 3 * time.Second
	defaultHolePunchRetryGap     = 150 * time.Millisecond
	maxHolePunchPayloadSize      = 4096
	maxHolePunchObservedAddrSize = 128
)

// QUICHolePunchMessage 保存 QUIC 打洞协调消息 + 由公网中继分发双方 NAT 映射地址。
type QUICHolePunchMessage struct {
	Version            uint16
	Kind               uint8
	SessionID          string
	SourcePeerID       string
	TargetPeerID       string
	RelayPeerID        string
	ObservedAddress    string
	StartAtUnixMilli   int64
	ExpiresAtUnixMilli int64
	CreatedAtUnixMilli int64
}

// PunchQUICPeer 请求中继协助 QUIC 打洞 + 成功后返回直连连接。
func (host *Host) PunchQUICPeer(ctx context.Context, relayPeerID string, targetPeerID string) (Connection, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateOutboundPeerID(relayPeerID); err != nil {
		return nil, err
	}
	if err := validateOutboundPeerID(targetPeerID); err != nil {
		return nil, err
	}
	if relayPeerID == targetPeerID || targetPeerID == host.peerID {
		return nil, fmt.Errorf("%w: invalid hole punch peer", ErrInvalidMessage)
	}
	if connection, ok := host.Connection(targetPeerID); ok && connection.Protocol() == utils.ProtocolQUIC {
		return connection, nil
	}
	sessionID, err := newMessageID()
	if err != nil {
		return nil, err
	}
	request := QUICHolePunchMessage{
		Version:            QUICHolePunchVersion,
		Kind:               quicHolePunchKindRequest,
		SessionID:          sessionID,
		SourcePeerID:       host.peerID,
		TargetPeerID:       targetPeerID,
		RelayPeerID:        relayPeerID,
		CreatedAtUnixMilli: time.Now().UnixMilli(),
		ExpiresAtUnixMilli: time.Now().Add(defaultHolePunchTimeout).UnixMilli(),
	}
	payload, err := request.MarshalBinary()
	if err != nil {
		return nil, err
	}
	message, err := NewMessageWithMaxSize(ProtocolQUICHolePunchV1, payload, host.maxMessageSize)
	if err != nil {
		return nil, err
	}
	if err := host.Send(ctx, relayPeerID, message); err != nil {
		return nil, fmt.Errorf("p2p: request quic hole punch via relay %s: %w", relayPeerID, err)
	}
	return host.waitHolePunchConnection(ctx, targetPeerID, defaultHolePunchTimeout)
}

func (host *Host) handleQUICHolePunchMessage(ctx context.Context, message Message) error {
	payload, err := UnmarshalQUICHolePunchMessageBinary(message.Payload)
	if err != nil {
		return err
	}
	switch payload.Kind {
	case quicHolePunchKindRequest:
		return host.handleQUICHolePunchRequest(ctx, message.FromPeerID, payload)
	case quicHolePunchKindIntroduce:
		go host.handleQUICHolePunchIntroduce(host.lifecycleContext, payload)
		return nil
	case quicHolePunchKindAck:
		host.logger.Debug("p2p quic hole punch ack",
			slog.String("session_id", payload.SessionID),
			slog.String("source_peer_id", payload.SourcePeerID),
			slog.String("target_peer_id", payload.TargetPeerID),
		)
		return nil
	default:
		return fmt.Errorf("%w: invalid hole punch kind", ErrInvalidMessage)
	}
}

func (host *Host) handleQUICHolePunchRequest(ctx context.Context, senderPeerID string, payload QUICHolePunchMessage) error {
	if host.capabilities&PeerCapabilityRelay == 0 {
		return fmt.Errorf("%w: hole punch relay capability required", ErrUnsupportedProtocol)
	}
	if payload.SourcePeerID != senderPeerID {
		return fmt.Errorf("%w: hole punch source mismatch", ErrInvalidMessage)
	}
	if payload.RelayPeerID != "" && payload.RelayPeerID != host.peerID {
		return fmt.Errorf("%w: hole punch relay mismatch", ErrInvalidMessage)
	}
	sourceAddress, err := host.quicObservedAddress(payload.SourcePeerID)
	if err != nil {
		return err
	}
	targetAddress, err := host.quicObservedAddress(payload.TargetPeerID)
	if err != nil {
		return err
	}
	startAt := time.Now().Add(defaultHolePunchDelay)
	expiresAt := startAt.Add(defaultHolePunchTimeout)
	sourceIntroduce := payload.newIntroduce(host.peerID, targetAddress.String(), startAt, expiresAt)
	targetIntroduce := payload.newIntroduce(host.peerID, sourceAddress.String(), startAt, expiresAt)
	if err := host.sendQUICHolePunch(ctx, payload.SourcePeerID, sourceIntroduce); err != nil {
		return fmt.Errorf("p2p: send hole punch source introduce: %w", err)
	}
	if err := host.sendQUICHolePunch(ctx, payload.TargetPeerID, targetIntroduce); err != nil {
		return fmt.Errorf("p2p: send hole punch target introduce: %w", err)
	}
	host.logger.Info("p2p quic hole punch coordinated",
		slog.String("session_id", payload.SessionID),
		slog.String("source_peer_id", payload.SourcePeerID),
		slog.String("target_peer_id", payload.TargetPeerID),
		slog.String("source_address", sourceAddress.String()),
		slog.String("target_address", targetAddress.String()),
	)
	return nil
}

func (host *Host) handleQUICHolePunchIntroduce(ctx context.Context, payload QUICHolePunchMessage) {
	address, err := payload.IntroducedAddress()
	if err != nil {
		host.logger.Warn("p2p quic hole punch introduce rejected",
			slog.String("session_id", payload.SessionID),
			slog.Any("error", err),
		)
		return
	}
	if address.PeerID == host.peerID {
		return
	}
	expectedPeerID, err := host.expectedHolePunchPeerID(payload)
	if err != nil {
		host.logger.Warn("p2p quic hole punch introduce rejected",
			slog.String("session_id", payload.SessionID),
			slog.Any("error", err),
		)
		return
	}
	if address.PeerID != expectedPeerID {
		host.logger.Warn("p2p quic hole punch peer mismatch",
			slog.String("session_id", payload.SessionID),
			slog.String("expected_peer_id", expectedPeerID),
			slog.String("address_peer_id", address.PeerID),
		)
		return
	}
	if err := host.addHolePunchPeerAddress(address); err != nil {
		host.logger.Warn("p2p quic hole punch peer address rejected",
			slog.String("session_id", payload.SessionID),
			slog.String("peer_id", address.PeerID),
			slog.Any("error", err),
		)
		return
	}
	waitDuration := time.Until(time.UnixMilli(payload.StartAtUnixMilli))
	if waitDuration > 0 {
		timer := time.NewTimer(waitDuration)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return
		}
	}
	connection, err := host.dialHolePunchAddress(ctx, address, time.UnixMilli(payload.ExpiresAtUnixMilli))
	if err != nil {
		host.logger.Warn("p2p quic hole punch failed",
			slog.String("session_id", payload.SessionID),
			slog.String("peer_id", address.PeerID),
			slog.String("address", address.String()),
			slog.Any("error", err),
		)
		return
	}
	go host.HandleConnection(host.lifecycleContext, connection)
	host.identifyPeerAsync(connection, address.PeerID)
	host.sendHolePunchAck(ctx, payload, address.PeerID)
}

func (host *Host) expectedHolePunchPeerID(payload QUICHolePunchMessage) (string, error) {
	if host.peerID == payload.SourcePeerID {
		return payload.TargetPeerID, nil
	}
	if host.peerID == payload.TargetPeerID {
		return payload.SourcePeerID, nil
	}
	return "", fmt.Errorf("%w: local peer is not part of hole punch", ErrInvalidMessage)
}

func (host *Host) dialHolePunchAddress(ctx context.Context, address utils.MultiAddress, expiresAt time.Time) (Connection, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var dialErrors []error
	for time.Now().Before(expiresAt) {
		if connection, ok := host.Connection(address.PeerID); ok && connection.Protocol() == utils.ProtocolQUIC {
			return connection, nil
		}
		attemptContext, cancel := context.WithTimeout(ctx, defaultHolePunchRetryGap)
		connection, err := host.DialAddress(attemptContext, address)
		cancel()
		if err == nil {
			return connection, nil
		}
		dialErrors = append(dialErrors, err)
		timer := time.NewTimer(defaultHolePunchRetryGap)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		}
	}
	if connection, ok := host.Connection(address.PeerID); ok && connection.Protocol() == utils.ProtocolQUIC {
		return connection, nil
	}
	return nil, fmt.Errorf("p2p: quic hole punch dial %s: %w", address.String(), errors.Join(dialErrors...))
}

func (host *Host) waitHolePunchConnection(ctx context.Context, targetPeerID string, timeout time.Duration) (Connection, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	waitContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		if connection, ok := host.Connection(targetPeerID); ok && connection.Protocol() == utils.ProtocolQUIC {
			return connection, nil
		}
		select {
		case <-waitContext.Done():
			return nil, fmt.Errorf("p2p: quic hole punch %s: %w", targetPeerID, waitContext.Err())
		case <-ticker.C:
		}
	}
}

func (host *Host) dialPeerViaQUICHolePunch(ctx context.Context, targetPeerID string) (Connection, error) {
	relays := host.connectedHolePunchRelays(targetPeerID)
	if len(relays) == 0 {
		return nil, nil
	}
	var punchErrors []error
	for _, relayPeerID := range relays {
		connection, err := host.PunchQUICPeer(ctx, relayPeerID, targetPeerID)
		if err == nil {
			host.logger.Info("p2p quic hole punch connected",
				slog.String("relay_peer_id", relayPeerID),
				slog.String("target_peer_id", targetPeerID),
			)
			return connection, nil
		}
		punchErrors = append(punchErrors, err)
	}
	return nil, fmt.Errorf("p2p: quic hole punch peer %s: %w", targetPeerID, errors.Join(punchErrors...))
}

func (host *Host) connectedHolePunchRelays(targetPeerID string) []string {
	host.mutex.RLock()
	defer host.mutex.RUnlock()
	relays := make([]string, 0, len(host.connections))
	for peerID, connection := range host.connections {
		if peerID == "" || peerID == targetPeerID || peerID == host.peerID || connection == nil {
			continue
		}
		if connection.Protocol() != utils.ProtocolQUIC {
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

func (host *Host) quicObservedAddress(peerID string) (utils.MultiAddress, error) {
	state, ok := host.ConnectionState(peerID)
	if !ok {
		return utils.MultiAddress{}, fmt.Errorf("%w: hole punch peer %s not connected to relay", ErrPeerNotFound, peerID)
	}
	if state.Protocol != utils.ProtocolQUIC {
		return utils.MultiAddress{}, fmt.Errorf("%w: hole punch requires quic connection", ErrUnsupportedProtocol)
	}
	return multiAddressFromObserved(state.observedRemoteAddress(), peerID)
}

func (host *Host) addHolePunchPeerAddress(address utils.MultiAddress) error {
	peer, ok := host.Peer(address.PeerID)
	if !ok {
		var err error
		peer, err = NewPeer(address.PeerID, []utils.MultiAddress{address})
		if err != nil {
			return err
		}
		peer.Role = PeerRoleFull
		return host.AddPeer(peer)
	}
	peer.AddAdvertisedAddress(address)
	peer.PreferredProtocols = appendQUICPreferred(peer.PreferredProtocols)
	return host.AddPeer(peer)
}

func (host *Host) sendQUICHolePunch(ctx context.Context, peerID string, payload QUICHolePunchMessage) error {
	encodedPayload, err := payload.MarshalBinary()
	if err != nil {
		return err
	}
	message, err := NewMessageWithMaxSize(ProtocolQUICHolePunchV1, encodedPayload, host.maxMessageSize)
	if err != nil {
		return err
	}
	return host.Send(ctx, peerID, message)
}

func (host *Host) sendHolePunchAck(ctx context.Context, payload QUICHolePunchMessage, peerID string) {
	if payload.RelayPeerID == "" {
		return
	}
	ack := QUICHolePunchMessage{
		Version:            QUICHolePunchVersion,
		Kind:               quicHolePunchKindAck,
		SessionID:          payload.SessionID,
		SourcePeerID:       host.peerID,
		TargetPeerID:       peerID,
		RelayPeerID:        payload.RelayPeerID,
		CreatedAtUnixMilli: time.Now().UnixMilli(),
		ExpiresAtUnixMilli: time.Now().Add(defaultHolePunchTimeout).UnixMilli(),
	}
	if err := host.sendQUICHolePunch(ctx, payload.RelayPeerID, ack); err != nil {
		host.logger.Debug("p2p quic hole punch ack failed",
			slog.String("session_id", payload.SessionID),
			slog.Any("error", err),
		)
	}
}

func (message QUICHolePunchMessage) newIntroduce(
	relayPeerID string,
	observedAddress string,
	startAt time.Time,
	expiresAt time.Time,
) QUICHolePunchMessage {
	return QUICHolePunchMessage{
		Version:            QUICHolePunchVersion,
		Kind:               quicHolePunchKindIntroduce,
		SessionID:          message.SessionID,
		SourcePeerID:       message.SourcePeerID,
		TargetPeerID:       message.TargetPeerID,
		RelayPeerID:        relayPeerID,
		ObservedAddress:    observedAddress,
		StartAtUnixMilli:   startAt.UnixMilli(),
		ExpiresAtUnixMilli: expiresAt.UnixMilli(),
		CreatedAtUnixMilli: time.Now().UnixMilli(),
	}
}

func (message QUICHolePunchMessage) IntroducedAddress() (utils.MultiAddress, error) {
	if err := message.Validate(); err != nil {
		return utils.MultiAddress{}, err
	}
	address, err := utils.ParseMultiAddress(message.ObservedAddress)
	if err != nil {
		return utils.MultiAddress{}, err
	}
	if address.Protocol != utils.ProtocolQUIC {
		return utils.MultiAddress{}, fmt.Errorf("%w: hole punch address must be quic", ErrInvalidMessage)
	}
	return address, nil
}

func (message QUICHolePunchMessage) MarshalBinary() ([]byte, error) {
	if err := message.Validate(); err != nil {
		return nil, err
	}
	writer := borsh.NewWriter(maxHolePunchPayloadSize)
	writer.WriteUint16(message.Version)
	writer.WriteUint8(message.Kind)
	if err := writer.WriteString(message.SessionID); err != nil {
		return nil, fmt.Errorf("p2p: marshal hole punch session: %w", err)
	}
	if err := writer.WriteString(message.SourcePeerID); err != nil {
		return nil, fmt.Errorf("p2p: marshal hole punch source: %w", err)
	}
	if err := writer.WriteString(message.TargetPeerID); err != nil {
		return nil, fmt.Errorf("p2p: marshal hole punch target: %w", err)
	}
	if err := writer.WriteString(message.RelayPeerID); err != nil {
		return nil, fmt.Errorf("p2p: marshal hole punch relay: %w", err)
	}
	if err := writer.WriteString(message.ObservedAddress); err != nil {
		return nil, fmt.Errorf("p2p: marshal hole punch address: %w", err)
	}
	writer.WriteInt64(message.StartAtUnixMilli)
	writer.WriteInt64(message.ExpiresAtUnixMilli)
	writer.WriteInt64(message.CreatedAtUnixMilli)
	return writer.BytesView(), nil
}

func UnmarshalQUICHolePunchMessageBinary(data []byte) (QUICHolePunchMessage, error) {
	if len(data) == 0 || len(data) > maxHolePunchPayloadSize {
		return QUICHolePunchMessage{}, fmt.Errorf("%w: invalid hole punch payload size", ErrInvalidMessage)
	}
	reader := borsh.NewBorrowedReader(data, maxHolePunchPayloadSize)
	version, err := reader.ReadUint16()
	if err != nil {
		return QUICHolePunchMessage{}, fmt.Errorf("p2p: read hole punch version: %w", err)
	}
	kind, err := reader.ReadUint8()
	if err != nil {
		return QUICHolePunchMessage{}, fmt.Errorf("p2p: read hole punch kind: %w", err)
	}
	sessionID, err := reader.ReadString()
	if err != nil {
		return QUICHolePunchMessage{}, fmt.Errorf("p2p: read hole punch session: %w", err)
	}
	sourcePeerID, err := reader.ReadString()
	if err != nil {
		return QUICHolePunchMessage{}, fmt.Errorf("p2p: read hole punch source: %w", err)
	}
	targetPeerID, err := reader.ReadString()
	if err != nil {
		return QUICHolePunchMessage{}, fmt.Errorf("p2p: read hole punch target: %w", err)
	}
	relayPeerID, err := reader.ReadString()
	if err != nil {
		return QUICHolePunchMessage{}, fmt.Errorf("p2p: read hole punch relay: %w", err)
	}
	observedAddress, err := reader.ReadString()
	if err != nil {
		return QUICHolePunchMessage{}, fmt.Errorf("p2p: read hole punch address: %w", err)
	}
	startAtUnixMilli, err := reader.ReadInt64()
	if err != nil {
		return QUICHolePunchMessage{}, fmt.Errorf("p2p: read hole punch start: %w", err)
	}
	expiresAtUnixMilli, err := reader.ReadInt64()
	if err != nil {
		return QUICHolePunchMessage{}, fmt.Errorf("p2p: read hole punch expires: %w", err)
	}
	createdAtUnixMilli, err := reader.ReadInt64()
	if err != nil {
		return QUICHolePunchMessage{}, fmt.Errorf("p2p: read hole punch created: %w", err)
	}
	if err := reader.EnsureEOF(); err != nil {
		return QUICHolePunchMessage{}, fmt.Errorf("p2p: read hole punch eof: %w", err)
	}
	message := QUICHolePunchMessage{
		Version:            version,
		Kind:               kind,
		SessionID:          sessionID,
		SourcePeerID:       sourcePeerID,
		TargetPeerID:       targetPeerID,
		RelayPeerID:        relayPeerID,
		ObservedAddress:    observedAddress,
		StartAtUnixMilli:   startAtUnixMilli,
		ExpiresAtUnixMilli: expiresAtUnixMilli,
		CreatedAtUnixMilli: createdAtUnixMilli,
	}
	return message, message.Validate()
}

func (message QUICHolePunchMessage) Validate() error {
	if message.Version != QUICHolePunchVersion {
		return fmt.Errorf("%w: unsupported hole punch version", ErrInvalidMessage)
	}
	if message.Kind < quicHolePunchKindRequest || message.Kind > quicHolePunchKindAck {
		return fmt.Errorf("%w: invalid hole punch kind", ErrInvalidMessage)
	}
	if _, err := messageIDBytes(message.SessionID); err != nil {
		return fmt.Errorf("%w: invalid hole punch session: %w", ErrInvalidMessage, err)
	}
	if err := validateMessagePeerID(message.SourcePeerID, false); err != nil {
		return err
	}
	if err := validateMessagePeerID(message.TargetPeerID, false); err != nil {
		return err
	}
	if err := validateMessagePeerID(message.RelayPeerID, true); err != nil {
		return err
	}
	if message.SourcePeerID == message.TargetPeerID {
		return fmt.Errorf("%w: hole punch source equals target", ErrInvalidMessage)
	}
	if message.Kind == quicHolePunchKindIntroduce {
		if len(message.ObservedAddress) == 0 || len(message.ObservedAddress) > maxHolePunchObservedAddrSize {
			return fmt.Errorf("%w: invalid hole punch observed address", ErrInvalidMessage)
		}
		address, err := utils.ParseMultiAddress(message.ObservedAddress)
		if err != nil {
			return fmt.Errorf("%w: invalid hole punch observed address: %w", ErrInvalidMessage, err)
		}
		if address.Protocol != utils.ProtocolQUIC {
			return fmt.Errorf("%w: hole punch observed address must be quic", ErrInvalidMessage)
		}
		if message.StartAtUnixMilli <= 0 || message.ExpiresAtUnixMilli <= message.StartAtUnixMilli {
			return fmt.Errorf("%w: invalid hole punch time range", ErrInvalidMessage)
		}
	}
	if message.CreatedAtUnixMilli <= 0 || message.ExpiresAtUnixMilli <= 0 {
		return fmt.Errorf("%w: invalid hole punch time", ErrInvalidMessage)
	}
	if time.Now().UnixMilli() > message.ExpiresAtUnixMilli {
		return fmt.Errorf("%w: expired hole punch message", ErrInvalidMessage)
	}
	return nil
}

func multiAddressFromObserved(rawAddress string, peerID string) (utils.MultiAddress, error) {
	host, portText, err := net.SplitHostPort(rawAddress)
	if err != nil {
		return utils.MultiAddress{}, fmt.Errorf("%w: invalid observed address: %w", ErrInvalidMessage, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return utils.MultiAddress{}, fmt.Errorf("%w: invalid observed port: %w", ErrInvalidMessage, err)
	}
	parsedIP := net.ParseIP(host)
	if parsedIP == nil || parsedIP.To4() == nil {
		return utils.MultiAddress{}, fmt.Errorf("%w: invalid observed ip", ErrInvalidMessage)
	}
	return utils.BuildMultiAddress(utils.MultiAddressIP4, host, utils.ProtocolQUIC, port, peerID)
}

func appendQUICPreferred(protocols []utils.MultiAddressProtocol) []utils.MultiAddressProtocol {
	if containsProtocol(protocols, utils.ProtocolQUIC) {
		return cloneProtocols(protocols)
	}
	next := make([]utils.MultiAddressProtocol, 0, len(protocols)+1)
	next = append(next, utils.ProtocolQUIC)
	next = append(next, protocols...)
	return next
}
