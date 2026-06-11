package p2p

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

func normalizeSecureSessionIdentity(config HostConfig) (bool, SecureSessionIdentity, error) {
	enabled := config.EnableSecureSession || hasSecureSessionIdentity(config.SecureIdentity)
	if !enabled {
		if config.AllowInsecure {
			return false, SecureSessionIdentity{}, nil
		}
		return false, SecureSessionIdentity{}, fmt.Errorf("%w: secure identity required", ErrSecureSession)
	}
	if config.SecureIdentity.PeerID != config.PeerID {
		return false, SecureSessionIdentity{}, fmt.Errorf("%w: secure identity peer id mismatch", ErrSecureSession)
	}
	if err := config.SecureIdentity.Validate(); err != nil {
		return false, SecureSessionIdentity{}, err
	}
	return true, config.SecureIdentity.Clone(), nil
}

func hasSecureSessionIdentity(identity SecureSessionIdentity) bool {
	return identity.PeerID != "" ||
		len(identity.PublicKey) > 0 ||
		len(identity.PrivateKey) > 0 ||
		identity.NetworkID != "" ||
		identity.SoftwareVersion != ""
}

func (host *Host) expectedPeerRecordNetworkID() string {
	if !host.secureSession {
		return ""
	}
	return host.secureIdentity.NetworkID
}

func (host *Host) secureConnectionHandler(handler ConnectionHandler) ConnectionHandler {
	if handler == nil {
		return nil
	}
	return func(ctx context.Context, connection Connection) {
		releaseSlot, err := host.acquireInboundSlot(ctx)
		if err != nil {
			host.logger.Warn("p2p inbound connection rejected",
				slog.String("connection_id", connection.ID()),
				slog.Any("error", err),
			)
			_ = connection.Close()
			return
		}
		defer releaseSlot()

		handledConnection := connection
		if host.secureSession {
			secureConnection, ok := host.acceptSecureConnection(ctx, connection)
			if !ok {
				return
			}
			handledConnection = secureConnection
		}
		handler(ctx, handledConnection)
	}
}

func (host *Host) acceptSecureConnection(ctx context.Context, connection Connection) (*SecureConnection, bool) {
	handshakeContext, cancel := host.withHandshakeTimeout(ctx)
	secureConnection, err := SecureAcceptConnection(handshakeContext, connection, host.secureIdentity)
	cancel()
	if err != nil {
		host.metrics.secureHandshakeFailed.Add(1)
		host.logger.Warn("p2p secure session accept failed",
			slog.String("connection_id", connection.ID()),
			slog.Any("error", err),
		)
		_ = connection.Close()
		return nil, false
	}
	host.metrics.secureHandshakeOK.Add(1)
	if err := host.storeConnection(secureConnection.RemotePeerID(), secureConnection); err != nil {
		host.logger.Warn("p2p secure connection rejected",
			slog.String("connection_id", secureConnection.ID()),
			slog.String("peer_id", secureConnection.RemotePeerID()),
			slog.Any("error", err),
		)
		_ = secureConnection.Close()
		return nil, false
	}
	session := secureConnection.Session()
	host.logger.Info("p2p secure connection accepted",
		slog.String("connection_id", secureConnection.ID()),
		slog.String("peer_id", secureConnection.RemotePeerID()),
		slog.String("network_id", session.NetworkID()),
		slog.String("remote_software", session.RemoteSoftwareVersion()),
		slog.Uint64("protocol_version", uint64(session.ProtocolVersion())),
	)
	host.identifyPeerAsync(secureConnection, secureConnection.RemotePeerID())
	return secureConnection, true
}

func (host *Host) secureOutboundConnection(ctx context.Context, connection Connection) (Connection, error) {
	if !host.secureSession {
		return connection, nil
	}
	secureConnection, err := SecureDialConnection(ctx, connection, host.secureIdentity)
	if err != nil {
		host.metrics.secureHandshakeFailed.Add(1)
		return nil, err
	}
	host.metrics.secureHandshakeOK.Add(1)
	return secureConnection, nil
}

func (host *Host) storeResumptionTicketLocked(connection Connection) {
	secureConnection, ok := connection.(*SecureConnection)
	if !ok {
		return
	}
	ticket, err := secureConnection.Session().ResumptionTicket()
	if err != nil {
		return
	}
	host.resumptionTickets[ticket.RemotePeerID] = ticket
}

type secureConnectionStateSnapshot struct {
	encrypted             bool
	networkID             string
	remoteSoftwareVersion string
	protocolVersion       uint16
}

func secureConnectionState(connection Connection) secureConnectionStateSnapshot {
	secureConnection, ok := connection.(*SecureConnection)
	if !ok {
		return secureConnectionStateSnapshot{}
	}
	session := secureConnection.Session()
	return secureConnectionStateSnapshot{
		encrypted:             true,
		networkID:             session.NetworkID(),
		remoteSoftwareVersion: session.RemoteSoftwareVersion(),
		protocolVersion:       session.ProtocolVersion(),
	}
}

// isExpectedConnectionClose 判断预期关闭错误 + 避免正常停机和超时路径污染异常日志。
func isExpectedConnectionClose(err error) bool {
	return errors.Is(err, ErrConnectionClosed) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
}
