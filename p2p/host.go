package p2p

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"solana_golang/utils"
)

const (
	defaultDialTimeout       = 5 * time.Second
	defaultRequestTimeout    = 10 * time.Second
	defaultHeartbeatInterval = 15 * time.Second
	defaultConnectionIdle    = 45 * time.Second
	defaultMaxPeerFailures   = 3
	defaultMaxPeers          = 64
	defaultMaxConnectionsIP  = 16
)

// HostConfig 保存 Host 配置 + 支持注入节点身份、协议优先级和日志。
type HostConfig struct {
	PeerID               string
	SecureIdentity       SecureSessionIdentity
	EnableSecureSession  bool
	AllowInsecure        bool
	Production           bool
	Environment          string
	PreferredProtocols   []utils.MultiAddressProtocol
	DialTimeout          time.Duration
	RequestTimeout       time.Duration
	HandshakeTimeout     time.Duration
	PeerRecordTTL        time.Duration
	HeartbeatInterval    time.Duration
	ConnectionIdle       time.Duration
	MaxPeerFailures      uint32
	MaxPeers             int
	MaxConnections       int
	MaxPendingInbound    int
	MaxConnectionsPerIP  int
	MaxMessageSize       int
	QUICStreamPoolSize   int
	PeerProtection       PeerProtectionConfig
	ProtocolScheduler    ProtocolSchedulerConfig
	AsyncWrite           AsyncWriteConfig
	BroadcastConcurrency int
	Logger               *slog.Logger
	Registry             *ProtocolRegistry
	RoutingTable         *KADRoutingTable
	PeerStore            PeerStore
	PersistedPeerLimit   int
	AdvertisedAddresses  []utils.MultiAddress
}

// Host 管理 P2P 节点运行态 + 统一处理传输、节点表和连接池。
type Host struct {
	mutex                sync.RWMutex
	peerID               string
	secureSession        bool
	secureIdentity       SecureSessionIdentity
	preferredProtocols   []utils.MultiAddressProtocol
	dialTimeout          time.Duration
	requestTimeout       time.Duration
	handshakeTimeout     time.Duration
	peerRecordTTL        time.Duration
	heartbeatInterval    time.Duration
	connectionIdle       time.Duration
	maxPeerFailures      uint32
	maxPeers             int
	maxConnections       int
	maxConnectionsPerIP  int
	maxMessageSize       int
	quicStreamPoolSize   int
	writeQueueSize       int
	writeTimeout         time.Duration
	broadcastConcurrency int
	logger               *slog.Logger
	lifecycleContext     context.Context
	lifecycleCancel      context.CancelFunc
	inboundSlots         chan struct{}
	peerProtection       *peerProtection
	protocolDispatcher   *protocolDispatcher
	advertisedAddresses  []utils.MultiAddress
	transports           map[utils.MultiAddressProtocol]Transport
	peers                map[string]Peer
	connections          map[string]Connection
	activeDials          map[string]*peerDialCall
	connectionPeerIDs    map[string]string
	connectionStates     map[string]ConnectionState
	resumptionTickets    map[string]SecureSessionResumptionTicket
	registry             *ProtocolRegistry
	requests             *requestManager
	routingTable         *KADRoutingTable
	peerStore            PeerStore
	persistedPeerLimit   int
	metrics              p2pMetrics
	closed               bool
}

// ConnectionState 保存连接运行态 + 供心跳、监控和故障清理使用。
type ConnectionState struct {
	PeerID                 string
	ConnectionID           string
	Protocol               utils.MultiAddressProtocol
	LocalAddress           string
	ObservedRemoteAddress  string
	RemoteAddress          string
	Encrypted              bool
	NetworkID              string
	RemoteSoftwareVersion  string
	NegotiatedProtocol     uint16
	ConnectedAtUnixMilli   int64
	LastReadUnixMilli      int64
	LastWriteUnixMilli     int64
	LastHeartbeatUnixMilli int64
	FailureCount           uint32
}

// NewHost 创建 Host + 默认注册 TCP 和 QUIC 传输边界。
func NewHost(config HostConfig, transports ...Transport) (*Host, error) {
	if err := validatePeerID(config.PeerID); err != nil {
		return nil, err
	}
	routingTable, err := normalizeRoutingTable(config.RoutingTable, config.PeerID)
	if err != nil {
		return nil, err
	}
	secureSession, secureIdentity, err := normalizeSecureSessionIdentity(config)
	if err != nil {
		return nil, err
	}
	if err := validateAdvertisedAddresses(config.AdvertisedAddresses, config.PeerID); err != nil {
		return nil, err
	}
	maxPeers := normalizeMaxPeers(config.MaxPeers)
	maxConnections := normalizeMaxConnections(config.MaxConnections, maxPeers)
	maxPendingInbound := normalizeMaxPendingInbound(config.MaxPendingInbound, maxConnections)
	maxMessageSize := normalizeMaxMessageSize(config.MaxMessageSize)
	quicStreamPoolSize := normalizeQUICStreamPoolSize(config.QUICStreamPoolSize)
	hostContext, hostCancel := context.WithCancel(context.Background())

	host := &Host{
		peerID:               config.PeerID,
		secureSession:        secureSession,
		secureIdentity:       secureIdentity,
		preferredProtocols:   normalizedProtocolOrder(config.PreferredProtocols),
		dialTimeout:          normalizeDialTimeout(config.DialTimeout),
		requestTimeout:       normalizeRequestTimeout(config.RequestTimeout),
		handshakeTimeout:     normalizeHandshakeTimeout(config.HandshakeTimeout),
		peerRecordTTL:        normalizePeerRecordTTL(config.PeerRecordTTL),
		heartbeatInterval:    normalizeHeartbeatInterval(config.HeartbeatInterval),
		connectionIdle:       normalizeConnectionIdle(config.ConnectionIdle),
		maxPeerFailures:      normalizeMaxPeerFailures(config.MaxPeerFailures),
		maxPeers:             maxPeers,
		maxConnections:       maxConnections,
		maxConnectionsPerIP:  normalizeMaxConnectionsPerIP(config.MaxConnectionsPerIP, maxConnections),
		maxMessageSize:       maxMessageSize,
		quicStreamPoolSize:   quicStreamPoolSize,
		writeQueueSize:       normalizeWriteQueueSize(config.AsyncWrite.QueueSize),
		writeTimeout:         normalizeWriteTimeout(config.AsyncWrite.WriteTimeout),
		broadcastConcurrency: normalizeBroadcastConcurrency(config.BroadcastConcurrency),
		logger:               normalizeLogger(config.Logger),
		lifecycleContext:     hostContext,
		lifecycleCancel:      hostCancel,
		inboundSlots:         newInboundLimiter(maxPendingInbound),
		peerProtection:       newPeerProtection(config.PeerProtection, maxConnections),
		advertisedAddresses:  cloneAddresses(config.AdvertisedAddresses),
		transports:           make(map[utils.MultiAddressProtocol]Transport),
		peers:                make(map[string]Peer),
		connections:          make(map[string]Connection),
		activeDials:          make(map[string]*peerDialCall),
		connectionPeerIDs:    make(map[string]string),
		connectionStates:     make(map[string]ConnectionState),
		resumptionTickets:    make(map[string]SecureSessionResumptionTicket),
		registry:             normalizeRegistry(config.Registry),
		requests:             newRequestManager(),
		routingTable:         routingTable,
		peerStore:            normalizePeerStore(config.PeerStore),
		persistedPeerLimit:   normalizePeerStoreLimit(config.PersistedPeerLimit),
	}
	host.protocolDispatcher = newProtocolDispatcher(host, config.ProtocolScheduler)
	host.protocolDispatcher.start(host.lifecycleContext)

	if len(transports) == 0 {
		quicTransport, err := NewQUICTransportWithConfig(QUICTransportConfig{
			MaxPendingInbound:   maxPendingInbound,
			MaxConnectionsPerIP: host.maxConnectionsPerIP,
			MaxMessageSize:      maxMessageSize,
			StreamPoolSize:      quicStreamPoolSize,
			MessagePriority:     host.messagePriority,
			MessageConcurrency:  host.messageConcurrency,
			MessagePartitionKey: host.messagePartitionKey,
			Logger:              host.logger,
		})
		if err != nil {
			host.lifecycleCancel()
			return nil, err
		}
		transports = []Transport{
			quicTransport,
			NewTCPTransportWithConfig(TCPTransportConfig{
				MaxPendingInbound:   maxPendingInbound,
				MaxConnectionsPerIP: host.maxConnectionsPerIP,
				MaxMessageSize:      maxMessageSize,
				Logger:              host.logger,
			}),
		}
	}
	for _, transport := range transports {
		if err := host.RegisterTransport(transport); err != nil {
			return nil, err
		}
	}
	if err := host.registerDefaultProtocolHandlers(); err != nil {
		return nil, err
	}
	host.logger.Info("p2p host initialized",
		slog.String("peer_id", host.peerID),
		slog.Bool("secure_session", host.secureSession),
		slog.Int("max_peers", host.maxPeers),
		slog.Int("max_connections", host.maxConnections),
		slog.Int("max_message_size", host.maxMessageSize),
		slog.Int("quic_stream_pool_size", host.quicStreamPoolSize),
		slog.Int("control_rate_limit", host.peerProtection.config.MaxControlMessagesPerSecond),
		slog.Int("data_rate_limit", host.peerProtection.config.MaxDataMessagesPerSecond),
		slog.Int("write_queue_size", host.writeQueueSize),
		slog.Duration("write_timeout", host.writeTimeout),
		slog.Int("broadcast_concurrency", host.broadcastConcurrency),
		slog.Int("protocol_workers", host.protocolDispatcher.config.WorkerCount),
		slog.Int("protocol_partitions", host.protocolDispatcher.config.PartitionCount),
		slog.Duration("dial_timeout", host.dialTimeout),
		slog.Duration("request_timeout", host.requestTimeout),
		slog.Duration("handshake_timeout", host.handshakeTimeout),
	)
	return host, nil
}

// PeerID 返回本节点身份 + 供消息路由和日志标识使用。
func (host *Host) PeerID() string {
	return host.peerID
}

// SecureSessionEnabled 返回安全会话状态 + 供节点状态和监控直接读取真实 Host 配置。
func (host *Host) SecureSessionEnabled() bool {
	host.mutex.RLock()
	defer host.mutex.RUnlock()
	return host.secureSession
}

// RegisterTransport 注册传输实现 + 允许按协议替换 TCP 或 QUIC 实现。
func (host *Host) RegisterTransport(transport Transport) error {
	if transport == nil {
		return ErrNilTransport
	}

	host.mutex.Lock()
	defer host.mutex.Unlock()
	if host.closed {
		return ErrHostClosed
	}
	host.transports[transport.Protocol()] = transport
	return nil
}

// RegisterVoidHandler 注册无响应协议 + 将协议处理能力绑定到当前 Host。
func (host *Host) RegisterVoidHandler(spec ProtocolSpec, handler VoidProtocolHandler) error {
	return host.registry.RegisterVoidHandler(spec, handler)
}

// RegisterResultHandler 注册有响应协议 + 将请求响应处理能力绑定到当前 Host。
func (host *Host) RegisterResultHandler(spec ProtocolSpec, handler ResultProtocolHandler) error {
	return host.registry.RegisterResultHandler(spec, handler)
}

// HandleMessage 处理入站消息 + 按消息协议 ID 分发到注册表处理器。
func (host *Host) HandleMessage(ctx context.Context, message Message) (ProtocolHandleResult, error) {
	if err := message.Validate(host.maxMessageSize); err != nil {
		return ProtocolHandleResult{}, err
	}
	return host.registry.HandleWithMaxMessageSize(ctx, message, host.maxMessageSize)
}
