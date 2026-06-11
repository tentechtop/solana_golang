package p2p

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"solana_golang/utils"
)

func (host *Host) Listen(ctx context.Context, address utils.MultiAddress, handler ConnectionHandler) error {
	transport, err := host.transport(address.Protocol)
	if err != nil {
		return err
	}
	host.logger.Info("p2p host listen",
		slog.String("address", address.String()),
		slog.String("protocol", string(address.Protocol)),
	)
	host.addAdvertisedAddress(address)
	return transport.Listen(ctx, address, host.secureConnectionHandler(handler))
}

// DialAddress 拨号指定地址 + 成功后将连接放入连接池。
func (host *Host) DialAddress(ctx context.Context, address utils.MultiAddress) (Connection, error) {
	if err := host.checkPeerDialAllowed(address.PeerID); err != nil {
		return nil, peerProtectionDialError(address.PeerID, err)
	}

	transport, err := host.transport(address.Protocol)
	if err != nil {
		return nil, err
	}

	dialContext, cancel := host.withDialTimeout(ctx)
	defer cancel()

	connection, err := transport.Dial(dialContext, address)
	if err != nil {
		host.recordPeerError(address.PeerID, err)
		return nil, err
	}
	securedConnection, err := host.secureOutboundConnection(dialContext, connection)
	if err != nil {
		_ = connection.Close()
		host.recordPeerError(address.PeerID, err)
		return nil, err
	}
	connection = securedConnection
	connection = host.wrapConnectionWriter(connection)
	if err := host.storeConnection(address.PeerID, connection); err != nil {
		_ = connection.Close()
		host.recordPeerError(address.PeerID, err)
		return nil, err
	}
	host.recordPeerProtectionSuccess(address.PeerID)
	host.logger.Info("p2p host connected",
		slog.String("peer_id", address.PeerID),
		slog.String("protocol", string(address.Protocol)),
	)
	return connection, nil
}

// DialPeer 拨号节点 + 按协议优先级支持 QUIC 到 TCP 的降级。
func (host *Host) DialPeer(ctx context.Context, peerID string) (Connection, error) {
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
		dialErrors = append(dialErrors, err)
	}
	if len(dialErrors) == 0 {
		return nil, fmt.Errorf("p2p: dial peer %s: no usable address", peerID)
	}
	host.recordPeerDialFailure(peerID)
	return nil, fmt.Errorf("p2p: dial peer %s: %w", peerID, errors.Join(dialErrors...))
}

// dialCandidateAddresses 生成拨号候选地址 + 按协议优先级支持 QUIC 到 TCP 降级。
func (host *Host) dialCandidateAddresses(peer Peer) []utils.MultiAddress {
	if !peerDialable(peer, host.maxPeerFailures) {
		return nil
	}
	if err := host.checkPeerDialAllowed(peer.ID); err != nil {
		return nil
	}
	addresses := make([]utils.MultiAddress, 0, len(host.preferredProtocols))
	for _, protocol := range host.preferredProtocols {
		address, ok := peer.firstAddressByProtocol(protocol)
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
