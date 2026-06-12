package p2p

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	"solana_golang/utils"
)

func (host *Host) Listen(ctx context.Context, address utils.MultiAddress, handler ConnectionHandler) error {
	if err := validateHostListenAddress(address, host.peerID); err != nil {
		return err
	}
	transport, err := host.transport(address.Protocol)
	if err != nil {
		return err
	}
	host.logger.Info("p2p host listen",
		slog.String("address", address.String()),
		slog.String("protocol", string(address.Protocol)),
	)
	if isDialableAdvertisedAddress(address) {
		host.addAdvertisedAddress(address)
	}
	return transport.Listen(ctx, address, host.secureConnectionHandler(handler))
}

func validateHostListenAddress(address utils.MultiAddress, peerID string) error {
	if address.PeerID != peerID {
		return fmt.Errorf("%w: listen address owner mismatch", ErrInvalidMessage)
	}
	if _, err := utils.ParseMultiAddress(address.String()); err != nil {
		return fmt.Errorf("%w: invalid listen address: %w", ErrInvalidMessage, err)
	}
	return nil
}

// DialAddress 拨号指定地址 + 成功后将连接放入连接池。
func (host *Host) DialAddress(ctx context.Context, address utils.MultiAddress) (Connection, error) {
	if err := validateHostDialAddress(address); err != nil {
		return nil, err
	}
	if err := host.checkPeerDialAllowed(address.PeerID); err != nil {
		return nil, peerProtectionDialError(address.PeerID, err)
	}

	transport, err := host.transport(address.Protocol)
	if err != nil {
		return nil, WithErrorInfo(err, ErrorInfo{
			Operation: "dial_transport_lookup",
			PeerID:    address.PeerID,
			Protocol:  address.Protocol,
		})
	}

	dialContext, cancel := host.withDialTimeout(ctx)
	defer cancel()

	connection, err := transport.Dial(dialContext, address)
	if err != nil {
		dialError := WithErrorInfo(err, ErrorInfo{
			Operation: "dial_transport",
			PeerID:    address.PeerID,
			Protocol:  address.Protocol,
		})
		host.recordPeerError(address.PeerID, dialError)
		return nil, dialError
	}
	securedConnection, err := host.secureOutboundConnection(dialContext, connection)
	if err != nil {
		_ = connection.Close()
		secureError := WithErrorInfo(err, ErrorInfo{
			Operation: "dial_secure_handshake",
			PeerID:    address.PeerID,
			Protocol:  address.Protocol,
		})
		host.recordPeerError(address.PeerID, secureError)
		return nil, secureError
	}
	connection = securedConnection
	connection = host.wrapConnectionWriter(connection)
	if err := host.storeConnection(address.PeerID, connection); err != nil {
		_ = connection.Close()
		storeError := WithErrorInfo(err, ErrorInfo{
			Operation: "dial_store_connection",
			PeerID:    address.PeerID,
			Protocol:  address.Protocol,
		})
		if errors.Is(err, ErrDuplicateConnection) {
			host.recordPeerProtectionSuccess(address.PeerID)
			return nil, storeError
		}
		host.recordPeerError(address.PeerID, storeError)
		return nil, storeError
	}
	host.markPeerAddressVerified(address.PeerID, address)
	host.recordPeerProtectionSuccess(address.PeerID)
	host.logger.Info("p2p host connected",
		slog.String("peer_id", address.PeerID),
		slog.String("protocol", string(address.Protocol)),
	)
	return connection, nil
}

func validateHostDialAddress(address utils.MultiAddress) error {
	if err := validatePeerID(address.PeerID); err != nil {
		return fmt.Errorf("%w: invalid dial peer id: %w", ErrInvalidMessage, err)
	}
	if _, err := utils.ParseMultiAddress(address.String()); err != nil {
		return fmt.Errorf("%w: invalid dial address: %w", ErrInvalidMessage, err)
	}
	if net.ParseIP(address.IPAddress) == nil {
		return fmt.Errorf("%w: invalid dial ip address", ErrInvalidMessage)
	}
	if address.Port < 1 || address.Port > 65535 {
		return fmt.Errorf("%w: invalid dial port", ErrInvalidMessage)
	}
	return nil
}

// DialPeer 拨号节点 + 按协议优先级支持 QUIC 到 TCP 的降级。
func (host *Host) DialPeer(ctx context.Context, peerID string) (Connection, error) {
	if connection, ok := host.Connection(peerID); ok {
		if err := host.checkPeerDialAllowedOrConnected(peerID); err != nil {
			return nil, peerProtectionDialError(peerID, err)
		}
		return connection, nil
	}
	if err := host.checkPeerDialAllowed(peerID); err != nil {
		return nil, peerProtectionDialError(peerID, err)
	}

	peer, ok := host.Peer(peerID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrPeerNotFound, peerID)
	}

	addresses := host.dialCandidateAddresses(peer)
	var dialErrors []error
	for index, address := range addresses {
		attemptContext, cancel := host.withDialAttemptTimeout(ctx, len(addresses)-index)
		connection, err := host.DialAddress(attemptContext, address)
		cancel()
		if err == nil {
			go host.HandleConnection(host.lifecycleContext, connection)
			host.identifyPeerAsync(connection, peerID)
			return connection, nil
		}
		if errors.Is(err, ErrDuplicateConnection) {
			existingConnection, ok := host.Connection(peerID)
			if ok {
				host.recordPeerProtectionSuccess(peerID)
				host.recordPeerConnectionSuccess(peerID)
				return existingConnection, nil
			}
		}
		dialErrors = append(dialErrors, err)
	}
	if existingConnection, ok := host.Connection(peerID); ok {
		host.recordPeerProtectionSuccess(peerID)
		host.recordPeerConnectionSuccess(peerID)
		return existingConnection, nil
	}
	if len(dialErrors) == 0 {
		return nil, fmt.Errorf("p2p: dial peer %s: no usable address", peerID)
	}
	if shouldRecordPeerDialFailure(dialErrors) {
		host.recordPeerDialFailure(peerID)
	}
	return nil, fmt.Errorf("p2p: dial peer %s: %w", peerID, errors.Join(dialErrors...))
}

func shouldRecordPeerDialFailure(dialErrors []error) bool {
	for _, err := range dialErrors {
		if err == nil {
			continue
		}
		if errors.Is(err, ErrDuplicateConnection) || errors.Is(err, ErrConnectionClosed) || errors.Is(err, context.Canceled) {
			continue
		}
		return true
	}
	return false
}

// dialCandidateAddresses 生成拨号候选地址 + 按协议优先级支持 QUIC 到 TCP 降级。
func (host *Host) dialCandidateAddresses(peer Peer) []utils.MultiAddress {
	if !peerDialable(peer, host.maxPeerFailures) {
		return nil
	}
	if err := host.checkPeerDialAllowed(peer.ID); err != nil {
		return nil
	}
	protocols := peer.dialProtocolOrder(host.preferredProtocols)
	addresses := make([]utils.MultiAddress, 0, len(protocols))
	for _, protocol := range protocols {
		address, ok := peer.firstDialAddressByProtocol(protocol)
		if ok {
			addresses = append(addresses, address)
		}
	}
	return addresses
}

// transport 获取指定协议传输 + 持读锁同时检查 Host 是否已关闭。
func (host *Host) transport(protocol utils.MultiAddressProtocol) (Transport, error) {
	host.mutex.RLock()
	defer host.mutex.RUnlock()
	if host.closed {
		return nil, ErrHostClosed
	}
	transport, ok := host.transports[protocol]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedProtocol, protocol)
	}
	return transport, nil
}

// withDialTimeout 构造拨号上下文 + 调用方未设置截止时间时使用 Host 默认超时。
func (host *Host) withDialTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, host.dialTimeout)
}

// withDialAttemptTimeout 构造单次拨号上下文 + 保留后续协议降级所需时间。
func (host *Host) withDialAttemptTimeout(ctx context.Context, remainingAttempts int) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if remainingAttempts <= 1 {
		return host.withDialTimeout(ctx)
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return context.WithTimeout(ctx, host.dialTimeout/time.Duration(remainingAttempts))
	}
	remainingTime := time.Until(deadline)
	if remainingTime <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, remainingTime/time.Duration(remainingAttempts))
}
