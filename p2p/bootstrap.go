package p2p

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"solana_golang/utils"
)

const (
	defaultBootstrapTimeout       = 5 * time.Second
	defaultBootstrapQueryLimit    = 20
	defaultBootstrapRefreshTarget = 8
)

// BootstrapConfig 保存启动发现配置 + 控制引导节点、查询规模和出站连接目标。
type BootstrapConfig struct {
	Bootnodes            []utils.MultiAddress
	MinOutboundPeers     int
	QueryLimit           int
	RefreshTargetCount   int
	DialTimeout          time.Duration
	StartConnectionLoops bool
}

// BootstrapSummary 保存启动发现结果 + 便于日志和测试判断发现质量。
type BootstrapSummary struct {
	BootnodeCount        int
	ConnectedBootnodes   int
	DiscoveredPeers      int
	ConnectedPeers       int
	FindNodeQueryCount   int
	FindNodeFailureCount int
}

// Bootstrap 执行 P2P 启动发现 + 连接引导节点、查询 KAD 邻居并补足出站连接。
func (host *Host) Bootstrap(ctx context.Context, config BootstrapConfig) (BootstrapSummary, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	normalized := normalizeBootstrapConfig(config)
	summary := BootstrapSummary{BootnodeCount: len(normalized.Bootnodes)}
	if len(normalized.Bootnodes) == 0 {
		return summary, nil
	}

	var bootstrapErrors []error
	for _, address := range normalized.Bootnodes {
		result, err := host.bootstrapFromAddress(ctx, address, normalized)
		summary.merge(result)
		if err != nil {
			bootstrapErrors = append(bootstrapErrors, err)
		}
	}
	connectedPeers, err := host.connectDiscoveredPeers(ctx, normalized)
	summary.ConnectedPeers += connectedPeers
	if err != nil {
		bootstrapErrors = append(bootstrapErrors, err)
	}
	return summary, errors.Join(bootstrapErrors...)
}

func (host *Host) bootstrapFromAddress(ctx context.Context, address utils.MultiAddress, config BootstrapConfig) (BootstrapSummary, error) {
	summary := BootstrapSummary{}
	if address.PeerID == host.peerID {
		return summary, nil
	}
	if err := host.addBootstrapPeer(address); err != nil {
		return summary, err
	}

	dialContext, cancel := context.WithTimeout(ctx, config.DialTimeout)
	connection, err := host.DialAddress(dialContext, address)
	cancel()
	if err != nil {
		return summary, fmt.Errorf("p2p: bootstrap dial %s: %w", address.String(), err)
	}
	summary.ConnectedBootnodes++

	targets := host.bootstrapTargetPeerIDs(config.RefreshTargetCount)
	for _, targetPeerID := range targets {
		querySummary, err := host.queryFindNode(ctx, connection, targetPeerID, config.QueryLimit, config.DialTimeout)
		summary.merge(querySummary)
		if err != nil {
			host.logger.Warn("p2p bootstrap find-node failed",
				slog.String("bootnode", address.PeerID),
				slog.String("target_peer_id", targetPeerID),
				slog.Any("error", err),
			)
			continue
		}
	}
	if config.StartConnectionLoops {
		go host.HandleConnection(ctx, connection)
	}
	return summary, nil
}

func (host *Host) addBootstrapPeer(address utils.MultiAddress) error {
	peer, err := NewPeer(address.PeerID, []utils.MultiAddress{address})
	if err != nil {
		return err
	}
	peer.Capabilities |= PeerCapabilityDHT
	peer.Role = PeerRoleBootnode
	return host.AddPeer(peer)
}

func (host *Host) queryFindNode(
	ctx context.Context,
	connection Connection,
	targetPeerID string,
	limit int,
	timeout time.Duration,
) (BootstrapSummary, error) {
	summary := BootstrapSummary{FindNodeQueryCount: 1}
	requestPayload, err := NewKADFindNodeRequest(targetPeerID, limit)
	if err != nil {
		summary.FindNodeFailureCount = 1
		return summary, err
	}
	payload, err := requestPayload.MarshalBinary()
	if err != nil {
		summary.FindNodeFailureCount = 1
		return summary, err
	}
	request, err := NewRequestMessage(host.peerID, ProtocolFindNodeRequestV1, payload)
	if err != nil {
		summary.FindNodeFailureCount = 1
		return summary, err
	}
	request.ToPeerID = connection.RemotePeerID()

	queryContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := host.writeConnectionMessage(queryContext, connection, connection.RemotePeerID(), request); err != nil {
		summary.FindNodeFailureCount = 1
		return summary, err
	}
	response, err := connection.ReadMessage(queryContext)
	if err != nil {
		summary.FindNodeFailureCount = 1
		host.recordConnectionError(connection, err)
		return summary, err
	}
	host.markConnectionRead(connection, response.FromPeerID)
	peers, err := decodeFindNodeResponse(request, response, targetPeerID)
	if err != nil {
		summary.FindNodeFailureCount = 1
		return summary, err
	}
	for _, peer := range peers {
		if peer.ID == host.peerID {
			continue
		}
		if err := host.AddPeer(peer); err != nil {
			summary.FindNodeFailureCount = 1
			return summary, err
		}
		summary.DiscoveredPeers++
	}
	return summary, nil
}

func decodeFindNodeResponse(request Message, response Message, targetPeerID string) ([]Peer, error) {
	if response.Type != ProtocolFindNodeResponseV1 {
		return nil, fmt.Errorf("%w: invalid find-node response type", ErrInvalidMessage)
	}
	if !response.IsResponse() || response.RequestID != request.ID {
		return nil, fmt.Errorf("%w: find-node response request mismatch", ErrInvalidMessage)
	}
	payload, err := UnmarshalKADFindNodeResponseBinary(response.Payload)
	if err != nil {
		return nil, err
	}
	if payload.TargetPeerID != targetPeerID {
		return nil, fmt.Errorf("%w: find-node target mismatch", ErrInvalidMessage)
	}
	peers := make([]Peer, 0, len(payload.Peers))
	for _, hint := range payload.Peers {
		peer, err := hint.ToPeer()
		if err != nil {
			return nil, err
		}
		peers = append(peers, peer)
	}
	return peers, nil
}

func (host *Host) connectDiscoveredPeers(ctx context.Context, config BootstrapConfig) (int, error) {
	if config.MinOutboundPeers <= 0 {
		return 0, nil
	}
	connected := host.ConnectionCount()
	if connected >= config.MinOutboundPeers {
		return 0, nil
	}
	candidates, err := host.ClosestPeers(host.peerID, config.MinOutboundPeers*2)
	if err != nil {
		return 0, err
	}
	newConnections := 0
	var dialErrors []error
	for _, peer := range candidates {
		if connected+newConnections >= config.MinOutboundPeers {
			break
		}
		if peer.ID == host.peerID || host.hasConnection(peer.ID) || !peerDialable(peer, host.maxPeerFailures) {
			continue
		}
		dialContext, cancel := context.WithTimeout(ctx, config.DialTimeout)
		_, err := host.DialPeer(dialContext, peer.ID)
		cancel()
		if err != nil {
			dialErrors = append(dialErrors, fmt.Errorf("%s: %w", peer.ID, err))
			continue
		}
		newConnections++
	}
	return newConnections, errors.Join(dialErrors...)
}

func (host *Host) bootstrapTargetPeerIDs(refreshTargetCount int) []string {
	targets := []string{host.peerID}
	if host.routingTable != nil {
		targets = append(targets, host.routingTable.RefreshTargetPeerIDs(refreshTargetCount)...)
	}
	return uniquePeerIDs(targets)
}

func normalizeBootstrapConfig(config BootstrapConfig) BootstrapConfig {
	if config.MinOutboundPeers < 0 {
		config.MinOutboundPeers = 0
	}
	if config.QueryLimit <= 0 || config.QueryLimit > maxKADPeerHints {
		config.QueryLimit = defaultBootstrapQueryLimit
	}
	if config.RefreshTargetCount <= 0 {
		config.RefreshTargetCount = defaultBootstrapRefreshTarget
	}
	if config.DialTimeout <= 0 {
		config.DialTimeout = defaultBootstrapTimeout
	}
	return config
}

func (summary *BootstrapSummary) merge(next BootstrapSummary) {
	summary.BootnodeCount += next.BootnodeCount
	summary.ConnectedBootnodes += next.ConnectedBootnodes
	summary.DiscoveredPeers += next.DiscoveredPeers
	summary.ConnectedPeers += next.ConnectedPeers
	summary.FindNodeQueryCount += next.FindNodeQueryCount
	summary.FindNodeFailureCount += next.FindNodeFailureCount
}

func uniquePeerIDs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}
