package rpcnode

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"solana_golang/internal/poswire"
	"solana_golang/p2p"
	"solana_golang/rpc"
	"solana_golang/structure"
	"solana_golang/utils"
)

const (
	defaultDialIntervalMillis = int64(2000)
	defaultMaxPeers           = 256
	defaultMaxConnections     = 256
	defaultRPCPort            = 8899
	defaultRPCMaxBodyBytes    = int64(1 << 20)
	defaultRPCMaxBatchSize    = 32
	defaultRPCRequestMillis   = int64(8000)
	defaultSoftwareVersion    = "rpcnode/0.1.0"
	maxPeerKeystoreBytes      = 8192
	transactionFanoutPeers    = 64
	transactionFanoutWorkers  = 64
	transactionFanoutTimeout  = 6 * time.Second
	stableBlockhashProbePeers = 4
	stableBlockhashMinSlots   = uint64(32)
)

type rpcNodeConfig struct {
	ConfigPath              string       `json:"-"`
	Environment             string       `json:"environment"`
	Production              bool         `json:"production"`
	NodeName                string       `json:"node_name"`
	ListenIP                string       `json:"listen_ip"`
	AdvertisedIP            string       `json:"advertised_ip"`
	ListenPort              int          `json:"listen_port"`
	Network                 string       `json:"network,omitempty"`
	PeerSeed                string       `json:"peer_seed"`
	PeerKeyPath             string       `json:"peer_key_path,omitempty"`
	AllowInsecureP2P        *bool        `json:"allow_insecure_p2p,omitempty"`
	NetworkID               string       `json:"network_id,omitempty"`
	SoftwareVersion         string       `json:"software_version,omitempty"`
	NodeRole                string       `json:"node_role,omitempty"`
	NodeRoles               []string     `json:"node_roles,omitempty"`
	MaxPeers                int          `json:"max_peers,omitempty"`
	MaxConnections          int          `json:"max_connections,omitempty"`
	DialIntervalMillis      int64        `json:"dial_interval_millis,omitempty"`
	StaticPeers             []peerConfig `json:"static_peers,omitempty"`
	RPCListenIP             string       `json:"rpc_listen_ip"`
	RPCPort                 int          `json:"rpc_port"`
	RPCRequestTimeoutMillis int64        `json:"rpc_request_timeout_millis,omitempty"`
	RPCMaxBodyBytes         int64        `json:"rpc_max_body_bytes,omitempty"`
	RPCMaxBatchSize         int          `json:"rpc_max_batch_size,omitempty"`
}

type peerConfig struct {
	PeerID       string   `json:"peer_id"`
	IP           string   `json:"ip"`
	Port         int      `json:"port"`
	Network      string   `json:"network,omitempty"`
	Role         string   `json:"role,omitempty"`
	Roles        []string `json:"roles,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

type rawKeyPair struct {
	publicKey  []byte
	privateKey []byte
	peerID     string
}

type peerKeystoreFile struct {
	Seed             string `json:"seed,omitempty"`
	SeedBase64       string `json:"seed_base64,omitempty"`
	PrivateKeyBase64 string `json:"private_key_base64,omitempty"`
	PublicKeyBase64  string `json:"public_key_base64,omitempty"`
}

type rpcNode struct {
	config        rpcNodeConfig
	logger        *slog.Logger
	keyPair       rawKeyPair
	host          *p2p.Host
	rpcServer     *rpc.Server
	knownPeerIDs  []string
	nextPeerIndex atomic.Uint64
	fanoutLimiter chan struct{}
}

// PeerIDFromSeed 派生 PeerID + 命令行部署前需要确认公网入口身份稳定。
func PeerIDFromSeed(seedText string) (string, error) {
	keyPair, err := rawKeyPairFromSeed(seedText)
	if err != nil {
		return "", fmt.Errorf("rpcnode: derive peer id: %w", err)
	}
	return keyPair.peerID, nil
}

// Run 启动 RPC 入口节点 + 仅提供发现和转发不参与同步共识。
func Run(configPath string) error {
	config, err := loadConfig(configPath)
	if err != nil {
		return err
	}
	logger, err := utils.LoggerFromEnv()
	if err != nil {
		return err
	}
	slog.SetDefault(logger)
	keyPair, err := loadPeerKeyPair(config)
	if err != nil {
		return fmt.Errorf("rpcnode: load peer key: %w", err)
	}
	node := &rpcNode{
		config:        config,
		logger:        logger,
		keyPair:       keyPair,
		fanoutLimiter: make(chan struct{}, transactionFanoutWorkers),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverErrors := make(chan error, 2)
	if err := node.start(ctx, serverErrors); err != nil {
		return err
	}
	defer node.close()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	select {
	case signalValue := <-signals:
		logger.Info("rpcnode shutdown requested", slog.String("signal", signalValue.String()))
		return nil
	case err := <-serverErrors:
		return err
	}
}

func (node *rpcNode) start(ctx context.Context, serverErrors chan<- error) error {
	host, err := newHost(node.config, node.keyPair, node.logger)
	if err != nil {
		return err
	}
	node.host = host
	knownPeerIDs, err := addStaticPeers(host, node.config.StaticPeers)
	if err != nil {
		_ = host.Close()
		return err
	}
	node.knownPeerIDs = knownPeerIDs
	p2pProtocol := node.config.p2pProtocol()
	listenAddress, err := utils.BuildMultiAddress(utils.MultiAddressIP4, node.config.ListenIP, p2pProtocol, node.config.ListenPort, node.keyPair.peerID)
	if err != nil {
		_ = host.Close()
		return fmt.Errorf("rpcnode: build listen address: %w", err)
	}
	go func() {
		if err := host.Listen(ctx, listenAddress, host.HandleConnection); err != nil {
			serverErrors <- fmt.Errorf("rpcnode: p2p listen: %w", err)
		}
	}()
	go connectPeersLoop(ctx, host, knownPeerIDs, node.config.dialInterval(), node.logger)
	node.startRPC(serverErrors)
	node.logger.Info("rpcnode started",
		slog.String("node", node.config.NodeName),
		slog.String("config_path", node.config.ConfigPath),
		slog.String("peer_id", node.keyPair.peerID),
		slog.String("node_role", string(p2p.PeerRolePublicRPC)),
		slog.Uint64("node_capabilities", uint64(rpcNodeCapabilities())),
		slog.Any("node_capability_names", p2p.PeerCapabilityNames(rpcNodeCapabilities())),
		slog.Bool("validator_enabled", false),
		slog.Bool("consensus_enabled", false),
		slog.String("listen_address", listenAddress.String()),
		slog.String("advertised_ip", node.config.AdvertisedIP),
		slog.Int("static_peers", len(knownPeerIDs)),
		slog.Bool("secure_session", !node.config.allowInsecure()),
	)
	return nil
}

func (node *rpcNode) startRPC(serverErrors chan<- error) {
	address := fmt.Sprintf("%s:%d", node.config.RPCListenIP, node.config.RPCPort)
	server := rpc.NewServer(rpc.ServerConfig{
		Address:      address,
		MaxBodyBytes: node.config.RPCMaxBodyBytes,
		MaxBatchSize: node.config.RPCMaxBatchSize,
		Logger:       node.logger,
	}, node.newRouter())
	node.rpcServer = server
	go func() {
		if err := server.ListenAndServe(); err != nil {
			serverErrors <- err
		}
	}()
}

func (node *rpcNode) close() {
	if node.rpcServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = node.rpcServer.Shutdown(ctx)
		cancel()
	}
	if node.host != nil {
		_ = node.host.Close()
	}
}

func loadConfig(path string) (rpcNodeConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return rpcNodeConfig{}, fmt.Errorf("rpcnode: read config: %w", err)
	}
	config := rpcNodeConfig{}
	if err := json.Unmarshal(data, &config); err != nil {
		return rpcNodeConfig{}, fmt.Errorf("rpcnode: decode config: %w", err)
	}
	config.ConfigPath = filepath.Clean(strings.TrimSpace(path))
	return normalizeConfig(config)
}

func normalizeConfig(config rpcNodeConfig) (rpcNodeConfig, error) {
	config.NodeName = strings.TrimSpace(config.NodeName)
	config.ListenIP = strings.TrimSpace(config.ListenIP)
	config.AdvertisedIP = strings.TrimSpace(config.AdvertisedIP)
	config.PeerSeed = strings.TrimSpace(config.PeerSeed)
	config.PeerKeyPath = strings.TrimSpace(config.PeerKeyPath)
	config.NetworkID = strings.TrimSpace(config.NetworkID)
	config.SoftwareVersion = strings.TrimSpace(config.SoftwareVersion)
	config.NodeRole = strings.TrimSpace(config.NodeRole)
	config.RPCListenIP = strings.TrimSpace(config.RPCListenIP)
	if config.NodeName == "" {
		return rpcNodeConfig{}, fmt.Errorf("rpcnode: node name is empty")
	}
	if config.ListenIP == "" {
		return rpcNodeConfig{}, fmt.Errorf("rpcnode: listen ip is empty")
	}
	if config.ListenPort < 1 || config.ListenPort > 65535 {
		return rpcNodeConfig{}, fmt.Errorf("rpcnode: invalid listen port")
	}
	network, err := normalizeP2PNetwork(config.Network)
	if err != nil {
		return rpcNodeConfig{}, fmt.Errorf("rpcnode: invalid p2p network: %w", err)
	}
	config.Network = string(network)
	if config.PeerSeed == "" && config.PeerKeyPath == "" {
		return rpcNodeConfig{}, fmt.Errorf("rpcnode: peer key material is empty")
	}
	if isProductionConfig(config) && config.PeerKeyPath == "" {
		return rpcNodeConfig{}, fmt.Errorf("rpcnode: production peer key path is required")
	}
	if config.RPCListenIP == "" {
		config.RPCListenIP = config.ListenIP
	}
	if config.RPCPort == 0 {
		config.RPCPort = defaultRPCPort
	}
	if config.RPCPort < 1 || config.RPCPort > 65535 {
		return rpcNodeConfig{}, fmt.Errorf("rpcnode: invalid rpc port")
	}
	if config.MaxPeers == 0 {
		config.MaxPeers = defaultMaxPeers
	}
	if config.MaxPeers < 1 {
		return rpcNodeConfig{}, fmt.Errorf("rpcnode: max peers must be positive")
	}
	if config.MaxConnections == 0 {
		config.MaxConnections = defaultMaxConnections
	}
	if config.MaxConnections < 1 {
		return rpcNodeConfig{}, fmt.Errorf("rpcnode: max connections must be positive")
	}
	if config.DialIntervalMillis == 0 {
		config.DialIntervalMillis = defaultDialIntervalMillis
	}
	if config.DialIntervalMillis < 200 {
		return rpcNodeConfig{}, fmt.Errorf("rpcnode: dial interval must be >= 200ms")
	}
	if config.RPCRequestTimeoutMillis == 0 {
		config.RPCRequestTimeoutMillis = defaultRPCRequestMillis
	}
	if config.RPCRequestTimeoutMillis < 200 {
		return rpcNodeConfig{}, fmt.Errorf("rpcnode: rpc request timeout must be >= 200ms")
	}
	if config.RPCMaxBodyBytes == 0 {
		config.RPCMaxBodyBytes = defaultRPCMaxBodyBytes
	}
	if config.RPCMaxBodyBytes < 256 || config.RPCMaxBodyBytes > p2p.MaxConfigurableMessageSize {
		return rpcNodeConfig{}, fmt.Errorf("rpcnode: invalid rpc max body bytes")
	}
	if config.RPCMaxBatchSize == 0 {
		config.RPCMaxBatchSize = defaultRPCMaxBatchSize
	}
	if config.RPCMaxBatchSize < 1 || config.RPCMaxBatchSize > 128 {
		return rpcNodeConfig{}, fmt.Errorf("rpcnode: invalid rpc max batch size")
	}
	if config.SoftwareVersion == "" {
		config.SoftwareVersion = defaultSoftwareVersion
	}
	if err := validateRPCNodeRoles(config.NodeRole, config.NodeRoles); err != nil {
		return rpcNodeConfig{}, err
	}
	if !config.allowInsecure() && config.NetworkID == "" {
		return rpcNodeConfig{}, fmt.Errorf("rpcnode: secure network id is required")
	}
	return config, nil
}

func validateRPCNodeRoles(roleValue string, roleValues []string) error {
	role, _, err := parsePeerRoles(roleValue, roleValues)
	if err != nil {
		return fmt.Errorf("rpcnode: invalid node role: %w", err)
	}
	if role == p2p.PeerRoleUnknown {
		return nil
	}
	if role != p2p.PeerRolePublicRPC {
		return fmt.Errorf("rpcnode: node role must be public_rpc")
	}
	for _, roleText := range roleValues {
		roleItem, err := parsePeerRole(roleText)
		if err != nil {
			return fmt.Errorf("rpcnode: invalid node role: %w", err)
		}
		if roleItem != p2p.PeerRolePublicRPC {
			return fmt.Errorf("rpcnode: node role must be public_rpc")
		}
	}
	return nil
}

// 功能目的：统一解析 P2P 传输协议；实现原因：RPC 网关监听、广告和拨号必须保持同一协议语义。
func normalizeP2PNetwork(value string) (utils.MultiAddressProtocol, error) {
	network := strings.TrimSpace(value)
	if network == "" {
		return utils.ProtocolTCP, nil
	}
	return utils.ParseMultiAddressProtocol(network)
}

func (config rpcNodeConfig) p2pProtocol() utils.MultiAddressProtocol {
	protocol, err := normalizeP2PNetwork(config.Network)
	if err != nil {
		return utils.ProtocolTCP
	}
	return protocol
}

func preferredP2PProtocols(primary utils.MultiAddressProtocol) []utils.MultiAddressProtocol {
	if primary == utils.ProtocolTCP {
		return []utils.MultiAddressProtocol{utils.ProtocolTCP, utils.ProtocolQUIC}
	}
	return []utils.MultiAddressProtocol{utils.ProtocolQUIC, utils.ProtocolTCP}
}

func newHost(config rpcNodeConfig, keyPair rawKeyPair, logger *slog.Logger) (*p2p.Host, error) {
	hostConfig := p2p.HostConfig{
		PeerID:                       keyPair.peerID,
		Role:                         p2p.PeerRolePublicRPC,
		Capabilities:                 rpcNodeCapabilities(),
		AllowInsecure:                config.allowInsecure(),
		Production:                   config.Production,
		Environment:                  config.Environment,
		PreferredProtocols:           preferredP2PProtocols(config.p2pProtocol()),
		RequestTimeout:               time.Duration(config.RPCRequestTimeoutMillis) * time.Millisecond,
		IgnoredUnregisteredProtocols: rpcNodeIgnoredUnregisteredProtocols(),
		MaxPeers:                     config.MaxPeers,
		MaxConnections:               config.MaxConnections,
		Logger:                       logger,
	}
	if config.AdvertisedIP != "" {
		address, err := utils.BuildMultiAddress(utils.MultiAddressIP4, config.AdvertisedIP, config.p2pProtocol(), config.ListenPort, keyPair.peerID)
		if err != nil {
			return nil, fmt.Errorf("rpcnode: build advertised address: %w", err)
		}
		hostConfig.AdvertisedAddresses = []utils.MultiAddress{address}
	}
	if !config.allowInsecure() {
		hostConfig.EnableSecureSession = true
		hostConfig.SecureIdentity = p2p.SecureSessionIdentity{
			PeerID:          keyPair.peerID,
			PublicKey:       keyPair.publicKey,
			PrivateKey:      keyPair.privateKey,
			NetworkID:       config.NetworkID,
			SoftwareVersion: config.SoftwareVersion,
		}
	}
	host, err := p2p.NewHost(hostConfig)
	if err != nil {
		return nil, fmt.Errorf("rpcnode: create host: %w", err)
	}
	return host, nil
}

func rpcNodeIgnoredUnregisteredProtocols() []p2p.ProtocolID {
	return []p2p.ProtocolID{
		p2p.ProtocolPoSTransactionV1,
		p2p.ProtocolPoSProposalV1,
		p2p.ProtocolPoSVoteV1,
		p2p.ProtocolPoSQCV1,
		p2p.ProtocolPoSBlockByHashV1,
		p2p.ProtocolPoSBlockByHeightV1,
		p2p.ProtocolPoSStateSnapshotV1,
		p2p.ProtocolPoSStatusV1,
		p2p.ProtocolPoSEvidenceV1,
		p2p.ProtocolPoSBlockLocatorV1,
		p2p.ProtocolPoSCommonAncestorV1,
	}
}

func addStaticPeers(host *p2p.Host, configs []peerConfig) ([]string, error) {
	peerIDs := make([]string, 0, len(configs))
	for _, config := range configs {
		peer, err := newStaticPeer(config)
		if err != nil {
			return nil, err
		}
		if peer.ID == host.PeerID() {
			continue
		}
		if err := host.AddPeer(peer); err != nil {
			return nil, fmt.Errorf("rpcnode: add static peer %s: %w", peer.ID, err)
		}
		peerIDs = append(peerIDs, peer.ID)
	}
	return uniqueStrings(peerIDs), nil
}

func newStaticPeer(config peerConfig) (p2p.Peer, error) {
	peerID := strings.TrimSpace(config.PeerID)
	ipAddress := strings.TrimSpace(config.IP)
	if peerID == "" || ipAddress == "" {
		return p2p.Peer{}, fmt.Errorf("rpcnode: static peer requires peer_id and ip")
	}
	if config.Port < 1 || config.Port > 65535 {
		return p2p.Peer{}, fmt.Errorf("rpcnode: static peer %s invalid port", peerID)
	}
	network, err := normalizeP2PNetwork(config.Network)
	if err != nil {
		return p2p.Peer{}, fmt.Errorf("rpcnode: static peer %s network: %w", peerID, err)
	}
	address, err := utils.BuildMultiAddress(utils.MultiAddressIP4, ipAddress, network, config.Port, peerID)
	if err != nil {
		return p2p.Peer{}, fmt.Errorf("rpcnode: build static peer address: %w", err)
	}
	peer, err := p2p.NewPeer(peerID, []utils.MultiAddress{address})
	if err != nil {
		return p2p.Peer{}, fmt.Errorf("rpcnode: create static peer: %w", err)
	}
	role, roleCapabilities, err := parsePeerRoles(config.Role, config.Roles)
	if err != nil {
		return p2p.Peer{}, fmt.Errorf("rpcnode: static peer %s role: %w", peerID, err)
	}
	capabilities, err := parsePeerCapabilities(config.Capabilities, role, roleCapabilities)
	if err != nil {
		return p2p.Peer{}, fmt.Errorf("rpcnode: static peer %s capabilities: %w", peerID, err)
	}
	peer.Role = role
	peer.Capabilities = capabilities
	peer.Validator = peer.Capabilities&p2p.PeerCapabilityValidator != 0
	return peer, nil
}

func connectPeersLoop(ctx context.Context, host *p2p.Host, peerIDs []string, interval time.Duration, logger *slog.Logger) {
	if len(peerIDs) == 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			connectKnownPeers(ctx, host, peerIDs, logger)
		}
	}
}

func connectKnownPeers(ctx context.Context, host *p2p.Host, peerIDs []string, logger *slog.Logger) {
	for _, peerID := range peerIDs {
		if _, ok := host.Connection(peerID); ok {
			continue
		}
		if _, err := host.DialPeer(ctx, peerID); err != nil {
			logger.Debug("rpcnode peer dial failed", slog.String("peer_id", peerID), slog.Any("error", err))
			continue
		}
		logger.Info("rpcnode peer connected", slog.String("peer_id", peerID))
	}
}

func (config rpcNodeConfig) allowInsecure() bool {
	if config.AllowInsecureP2P == nil {
		if isProductionConfig(config) {
			return false
		}
		return true
	}
	return *config.AllowInsecureP2P
}

func (config rpcNodeConfig) dialInterval() time.Duration {
	return time.Duration(config.DialIntervalMillis) * time.Millisecond
}

func isProductionConfig(config rpcNodeConfig) bool {
	if config.Production {
		return true
	}
	environment := strings.TrimSpace(strings.ToLower(config.Environment))
	return environment == "production" || environment == "prod"
}

func rpcNodeCapabilities() p2p.PeerCapability {
	return p2p.PeerCapabilityDHT | p2p.PeerCapabilityRelay
}

func parsePeerRoles(roleValue string, roleValues []string) (p2p.PeerRole, p2p.PeerCapability, error) {
	rawRoles := make([]string, 0, len(roleValues)+1)
	if strings.TrimSpace(roleValue) != "" {
		rawRoles = append(rawRoles, roleValue)
	}
	rawRoles = append(rawRoles, roleValues...)
	if len(rawRoles) == 0 {
		return p2p.PeerRoleUnknown, 0, nil
	}
	roles := make([]p2p.PeerRole, 0, len(rawRoles))
	seen := make(map[p2p.PeerRole]struct{}, len(rawRoles))
	for _, rawRole := range rawRoles {
		role, err := parsePeerRole(rawRole)
		if err != nil {
			return p2p.PeerRoleUnknown, 0, err
		}
		if _, exists := seen[role]; exists {
			continue
		}
		seen[role] = struct{}{}
		roles = append(roles, role)
	}
	if len(roles) == 0 {
		return p2p.PeerRoleUnknown, 0, nil
	}
	capabilities := p2p.PeerCapability(0)
	for _, role := range roles {
		capabilities |= capabilitiesForRole(role)
	}
	return roles[0], capabilities, nil
}

func parsePeerRole(value string) (p2p.PeerRole, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "public_rpc", "public-rpc", "rpc", "rpc_gateway", "rpc-gateway":
		return p2p.PeerRolePublicRPC, nil
	case "bootnode", "bootstrap":
		return p2p.PeerRoleBootnode, nil
	case "full":
		return p2p.PeerRoleFull, nil
	case "validator":
		return p2p.PeerRoleValidator, nil
	case "archive":
		return p2p.PeerRoleArchive, nil
	default:
		return p2p.PeerRoleUnknown, fmt.Errorf("unsupported role %q", value)
	}
}

func parsePeerCapabilities(
	values []string,
	role p2p.PeerRole,
	roleCapabilities p2p.PeerCapability,
) (p2p.PeerCapability, error) {
	capabilities := roleCapabilities
	for _, value := range values {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "relay":
			capabilities |= p2p.PeerCapabilityRelay
		case "archive":
			capabilities |= p2p.PeerCapabilityArchive
		case "validator":
			capabilities |= p2p.PeerCapabilityValidator
		case "state_sync", "state-sync", "statesync":
			capabilities |= p2p.PeerCapabilityStateSync
		case "dht":
			capabilities |= p2p.PeerCapabilityDHT
		default:
			return 0, fmt.Errorf("unsupported capability %q", value)
		}
	}
	if role == p2p.PeerRoleValidator {
		capabilities |= p2p.PeerCapabilityValidator | p2p.PeerCapabilityRelay
	}
	if role == p2p.PeerRolePublicRPC || role == p2p.PeerRoleBootnode {
		capabilities |= p2p.PeerCapabilityDHT | p2p.PeerCapabilityRelay
	}
	return capabilities, nil
}

func capabilitiesForRole(role p2p.PeerRole) p2p.PeerCapability {
	switch role {
	case p2p.PeerRoleValidator:
		return p2p.PeerCapabilityValidator | p2p.PeerCapabilityRelay | p2p.PeerCapabilityStateSync
	case p2p.PeerRolePublicRPC:
		return p2p.PeerCapabilityDHT | p2p.PeerCapabilityRelay
	case p2p.PeerRoleBootnode:
		return p2p.PeerCapabilityDHT | p2p.PeerCapabilityRelay
	case p2p.PeerRoleArchive:
		return p2p.PeerCapabilityArchive | p2p.PeerCapabilityRelay
	case p2p.PeerRoleFull:
		return p2p.PeerCapabilityDHT | p2p.PeerCapabilityRelay
	default:
		return 0
	}
}

func loadPeerKeyPair(config rpcNodeConfig) (rawKeyPair, error) {
	if strings.TrimSpace(config.PeerKeyPath) != "" {
		keyFile, err := loadPeerKeystore(config.PeerKeyPath, isProductionConfig(config))
		if err != nil {
			return rawKeyPair{}, err
		}
		return rawKeyPairFromKeystore(keyFile)
	}
	if isProductionConfig(config) {
		return rawKeyPair{}, fmt.Errorf("rpcnode: peer key path is required in production")
	}
	return rawKeyPairFromSeed(config.PeerSeed)
}

func loadPeerKeystore(path string, production bool) (peerKeystoreFile, error) {
	cleanPath := filepath.Clean(strings.TrimSpace(path))
	if cleanPath == "." || cleanPath == "" {
		return peerKeystoreFile{}, fmt.Errorf("rpcnode: peer keystore path is empty")
	}
	info, err := os.Stat(cleanPath)
	if err != nil {
		return peerKeystoreFile{}, fmt.Errorf("rpcnode: stat peer keystore: %w", err)
	}
	if info.IsDir() {
		return peerKeystoreFile{}, fmt.Errorf("rpcnode: peer keystore is a directory")
	}
	if info.Size() <= 0 || info.Size() > maxPeerKeystoreBytes {
		return peerKeystoreFile{}, fmt.Errorf("rpcnode: invalid peer keystore size")
	}
	if production && goruntime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return peerKeystoreFile{}, fmt.Errorf("rpcnode: peer keystore must not be group/world readable")
	}
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return peerKeystoreFile{}, fmt.Errorf("rpcnode: read peer keystore: %w", err)
	}
	keyFile := peerKeystoreFile{}
	if err := json.Unmarshal(data, &keyFile); err != nil {
		return peerKeystoreFile{}, fmt.Errorf("rpcnode: decode peer keystore: %w", err)
	}
	return keyFile, nil
}

func rawKeyPairFromKeystore(keyFile peerKeystoreFile) (rawKeyPair, error) {
	privateKey, expectedPublicKey, err := privateKeyFromKeystore(keyFile)
	if err != nil {
		return rawKeyPair{}, err
	}
	keyPair, err := rawKeyPairFromPrivateKey(privateKey)
	if err != nil {
		return rawKeyPair{}, err
	}
	if len(expectedPublicKey) > 0 && !bytes.Equal(keyPair.publicKey, expectedPublicKey) {
		return rawKeyPair{}, fmt.Errorf("rpcnode: public key does not match private key")
	}
	return keyPair, nil
}

func privateKeyFromKeystore(keyFile peerKeystoreFile) ([]byte, []byte, error) {
	expectedPublicKey, err := optionalPublicKey(keyFile.PublicKeyBase64)
	if err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(keyFile.Seed) != "" {
		return utils.SHA256([]byte(strings.TrimSpace(keyFile.Seed))), expectedPublicKey, nil
	}
	if strings.TrimSpace(keyFile.SeedBase64) != "" {
		privateKey, err := decodeSizedKeystoreBase64(keyFile.SeedBase64, utils.Ed25519KeySize, "peer seed")
		return privateKey, expectedPublicKey, err
	}
	if strings.TrimSpace(keyFile.PrivateKeyBase64) != "" {
		privateKey, err := decodeSizedKeystoreBase64(keyFile.PrivateKeyBase64, utils.Ed25519KeySize, "peer private key")
		return privateKey, expectedPublicKey, err
	}
	return nil, nil, fmt.Errorf("rpcnode: peer keystore has no key material")
}

func optionalPublicKey(encodedPublicKey string) ([]byte, error) {
	if strings.TrimSpace(encodedPublicKey) == "" {
		return nil, nil
	}
	return decodeSizedKeystoreBase64(encodedPublicKey, utils.Ed25519KeySize, "peer public key")
}

func decodeSizedKeystoreBase64(encodedValue string, expectedSize int, fieldName string) ([]byte, error) {
	value, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedValue))
	if err != nil {
		return nil, fmt.Errorf("rpcnode: decode %s: %w", fieldName, err)
	}
	if len(value) != expectedSize {
		return nil, fmt.Errorf("rpcnode: %s requires %d bytes", fieldName, expectedSize)
	}
	return value, nil
}

func rawKeyPairFromSeed(seedText string) (rawKeyPair, error) {
	seedText = strings.TrimSpace(seedText)
	if seedText == "" {
		return rawKeyPair{}, fmt.Errorf("rpcnode: peer seed is empty")
	}
	return rawKeyPairFromPrivateKey(utils.SHA256([]byte(seedText)))
}

func rawKeyPairFromPrivateKey(privateKey []byte) (rawKeyPair, error) {
	publicKey, err := utils.DeriveEd25519PublicKeyFromPrivateKey(privateKey)
	if err != nil {
		return rawKeyPair{}, fmt.Errorf("rpcnode: derive peer public key: %w", err)
	}
	return rawKeyPair{
		publicKey:  utils.CloneBytes(publicKey),
		privateKey: utils.CloneBytes(privateKey),
		peerID:     utils.Base58Encode(publicKey),
	}, nil
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func (node *rpcNode) newRouter() *rpc.Router {
	router := rpc.NewRouter()
	_ = router.Register(rpc.MethodGetNodeStatus, node.nodeStatusHandler())
	_ = router.Register(rpc.MethodGetPeerNetwork, node.peerNetworkHandler())
	_ = router.Register(rpc.MethodGetConsensusStatus, node.consensusStatusHandler())
	for _, method := range forwardedMethods() {
		methodName := method
		_ = router.Register(methodName, node.forwardHandler(methodName))
	}
	return router
}

func forwardedMethods() []string {
	return []string{
		rpc.MethodGetBalance,
		rpc.MethodGetAccountType,
		rpc.MethodGetLatestBlockhash,
		rpc.MethodSendTransaction,
		rpc.MethodGetBlock,
		rpc.MethodGetTransaction,
		rpc.MethodGetAddressTransactions,
		rpc.MethodGetContractPrograms,
		rpc.MethodGetPrivacyState,
		rpc.MethodGetPrivacyBalance,
		rpc.MethodGetValidatorSet,
		rpc.MethodGetHealth,
	}
}

func (node *rpcNode) forwardHandler(method string) rpc.HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *rpc.Error) {
		result, rpcError := node.forwardMethod(ctx, method, params)
		if rpcError != nil {
			return nil, rpcError
		}
		return result, nil
	}
}

func (node *rpcNode) forwardMethod(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, *rpc.Error) {
	response, err := node.forwardRawRPC(ctx, method, params)
	if err != nil {
		return nil, unavailableRPCError(err.Error())
	}
	if response.Error != nil {
		return nil, response.RPCError()
	}
	if len(response.Result) == 0 {
		return json.RawMessage("null"), nil
	}
	return json.RawMessage(response.Result), nil
}

func (node *rpcNode) forwardRawRPC(ctx context.Context, method string, params json.RawMessage) (poswire.RPCForwardResponse, error) {
	if method == rpc.MethodGetLatestBlockhash {
		return node.forwardStableLatestBlockhash(ctx, params)
	}
	if method == rpc.MethodSendTransaction {
		return node.forwardSignedTransaction(ctx, method, params)
	}
	peerID, err := node.selectValidatorPeer()
	if err != nil {
		return poswire.RPCForwardResponse{}, err
	}
	return node.forwardRawRPCToPeer(ctx, peerID, method, params)
}

func (node *rpcNode) forwardRawRPCToPeer(ctx context.Context, peerID string, method string, params json.RawMessage) (poswire.RPCForwardResponse, error) {
	payload, err := poswire.MarshalRPCForwardRequest(poswire.RPCForwardRequest{
		Method: method,
		Params: params,
	}, int(node.config.RPCMaxBodyBytes)+1024)
	if err != nil {
		return poswire.RPCForwardResponse{}, fmt.Errorf("rpcnode: marshal p2p rpc request: %w", err)
	}
	request, err := p2p.NewRequestMessageWithMaxSize(node.keyPair.peerID, p2p.ProtocolPoSRPCForwardV1, payload, int(node.config.RPCMaxBodyBytes)+1024)
	if err != nil {
		return poswire.RPCForwardResponse{}, fmt.Errorf("rpcnode: build p2p rpc request: %w", err)
	}
	requestContext, cancel := context.WithTimeout(ctx, time.Duration(node.config.RPCRequestTimeoutMillis)*time.Millisecond)
	defer cancel()
	response, err := node.host.Request(requestContext, peerID, request)
	if err != nil {
		return poswire.RPCForwardResponse{}, fmt.Errorf("rpcnode: forward rpc to validator %s: %w", peerID, err)
	}
	return poswire.UnmarshalRPCForwardResponse(response.Payload, int(node.config.RPCMaxBodyBytes)+1024)
}

type upstreamBlockhashStatus struct {
	FinalizedHash   string `json:"finalized_hash"`
	FinalizedHeight uint64 `json:"finalized_height"`
	FinalizedSlot   uint64 `json:"finalized_slot"`
	HeadHash        string `json:"head_hash"`
	HeadHeight      uint64 `json:"head_height"`
	HeadSlot        uint64 `json:"head_slot"`
}

func (node *rpcNode) forwardStableLatestBlockhash(ctx context.Context, params json.RawMessage) (poswire.RPCForwardResponse, error) {
	peerIDs := node.connectedValidatorPeerIDs()
	if len(peerIDs) == 0 {
		return poswire.RPCForwardResponse{}, fmt.Errorf("no connected validator peer")
	}
	startIndex := node.nextPeerIndex.Add(1)
	orderedPeerIDs := rotatePeerIDs(peerIDs, startIndex)
	var bestResult rpc.LatestBlockhashResult
	var bestFound bool
	for index, peerID := range orderedPeerIDs {
		if index >= stableBlockhashProbePeers {
			break
		}
		response, err := node.forwardRawRPCToPeer(ctx, peerID, rpc.MethodGetNodeStatus, json.RawMessage("[]"))
		if err != nil || response.Error != nil {
			continue
		}
		status := upstreamBlockhashStatus{}
		if err := json.Unmarshal(response.Result, &status); err != nil {
			continue
		}
		result, ok := stableLatestBlockhashFromStatus(status)
		if !ok {
			continue
		}
		if !bestFound || result.Height > bestResult.Height {
			bestResult = result
			bestFound = true
		}
	}
	if bestFound {
		resultBytes, err := json.Marshal(bestResult)
		if err != nil {
			return poswire.RPCForwardResponse{}, fmt.Errorf("rpcnode: marshal stable blockhash: %w", err)
		}
		return poswire.RPCForwardResponse{Result: resultBytes}, nil
	}
	return node.forwardRawRPCToPeer(ctx, orderedPeerIDs[0], rpc.MethodGetLatestBlockhash, params)
}

func stableLatestBlockhashFromStatus(status upstreamBlockhashStatus) (rpc.LatestBlockhashResult, bool) {
	finalizedHash := strings.TrimSpace(status.FinalizedHash)
	if finalizedHash == "" || status.FinalizedHeight == 0 {
		return rpc.LatestBlockhashResult{}, false
	}
	if status.FinalizedSlot == 0 {
		return rpc.LatestBlockhashResult{}, false
	}
	if ^uint64(0)-status.FinalizedSlot < structure.MaxRecentBlockhashAgeSlots {
		return rpc.LatestBlockhashResult{}, false
	}
	lastValidSlot := status.FinalizedSlot + structure.MaxRecentBlockhashAgeSlots
	if status.HeadSlot > 0 {
		if status.HeadSlot < status.FinalizedSlot || status.HeadSlot > lastValidSlot {
			return rpc.LatestBlockhashResult{}, false
		}
		if lastValidSlot-status.HeadSlot < stableBlockhashMinSlots {
			return rpc.LatestBlockhashResult{}, false
		}
	}
	return rpc.LatestBlockhashResult{
		Blockhash:     finalizedHash,
		Slot:          status.FinalizedSlot,
		Height:        status.FinalizedHeight,
		LastValidSlot: lastValidSlot,
	}, true
}

func (node *rpcNode) forwardSignedTransaction(ctx context.Context, method string, params json.RawMessage) (poswire.RPCForwardResponse, error) {
	peerIDs := node.connectedValidatorPeerIDs()
	if len(peerIDs) == 0 {
		return poswire.RPCForwardResponse{}, fmt.Errorf("no connected validator peer")
	}
	startIndex := node.nextPeerIndex.Add(1) - 1
	orderedPeerIDs := rotatePeerIDs(peerIDs, startIndex)
	var firstRetryableError *poswire.RPCForwardError
	var lastTransportError error
	for peerIndex, peerID := range orderedPeerIDs {
		response, err := node.forwardRawRPCToPeer(ctx, peerID, method, params)
		if err != nil {
			lastTransportError = err
			node.logger.Warn("rpcnode send transaction forward failed",
				slog.String("peer_id", peerID),
				slog.Any("error", err),
			)
			continue
		}
		if response.Error == nil {
			signature := rpcForwardResultText(response.Result)
			node.logger.Info("rpcnode send transaction accepted",
				slog.String("peer_id", peerID),
				slog.String("signature", signature),
			)
			node.fanoutSignedTransaction(ctx, method, params, orderedPeerIDs, peerIndex, signature)
			return response, nil
		}
		if !isRetryableTransactionForwardError(response.Error) {
			return response, nil
		}
		if firstRetryableError == nil {
			firstRetryableError = response.Error
		}
		node.logger.Warn("rpcnode send transaction retrying validator",
			slog.String("peer_id", peerID),
			slog.String("reason", rpcForwardErrorText(response.Error)),
		)
	}
	if firstRetryableError != nil {
		return poswire.RPCForwardResponse{Error: firstRetryableError}, nil
	}
	if lastTransportError != nil {
		return poswire.RPCForwardResponse{}, lastTransportError
	}
	return poswire.RPCForwardResponse{}, fmt.Errorf("no validator accepted transaction")
}

func (node *rpcNode) fanoutSignedTransaction(ctx context.Context, method string, params json.RawMessage, peerIDs []string, acceptedIndex int, signature string) {
	targets := transactionFanoutTargets(peerIDs, acceptedIndex, transactionFanoutPeers)
	if len(targets) == 0 {
		return
	}
	if node.fanoutLimiter != nil {
		select {
		case node.fanoutLimiter <- struct{}{}:
			defer func() { <-node.fanoutLimiter }()
		case <-ctx.Done():
			return
		}
	}
	paramsCopy := append(json.RawMessage(nil), params...)
	contextValue, cancel := context.WithTimeout(ctx, transactionFanoutTimeout)
	defer cancel()
	successCount := node.runTransactionFanoutWorkers(contextValue, method, paramsCopy, targets)
	node.logger.Info("rpcnode transaction fanout completed",
		slog.String("signature", signature),
		slog.Int("targets", len(targets)),
		slog.Int("accepted", successCount),
	)
}

func (node *rpcNode) runTransactionFanoutWorkers(ctx context.Context, method string, params json.RawMessage, targets []string) int {
	workerCount := minInt(transactionFanoutWorkers, len(targets))
	jobs := make(chan string)
	var waitGroup sync.WaitGroup
	var successCount atomic.Int64
	for workerIndex := 0; workerIndex < workerCount; workerIndex++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for peerID := range jobs {
				response, err := node.forwardRawRPCToPeer(ctx, peerID, method, params)
				if err != nil || response.Error != nil {
					continue
				}
				successCount.Add(1)
			}
		}()
	}
	for _, peerID := range targets {
		select {
		case <-ctx.Done():
			close(jobs)
			waitGroup.Wait()
			return int(successCount.Load())
		case jobs <- peerID:
		}
	}
	close(jobs)
	waitGroup.Wait()
	return int(successCount.Load())
}

func transactionFanoutTargets(peerIDs []string, acceptedIndex int, limit int) []string {
	if len(peerIDs) <= 1 || limit <= 0 || acceptedIndex < 0 || acceptedIndex >= len(peerIDs) {
		return nil
	}
	targets := make([]string, 0, minInt(limit, len(peerIDs)-1))
	for offset := 1; offset < len(peerIDs) && len(targets) < limit; offset++ {
		targetIndex := (acceptedIndex + offset) % len(peerIDs)
		targets = append(targets, peerIDs[targetIndex])
	}
	return targets
}

func rotatePeerIDs(peerIDs []string, startIndex uint64) []string {
	if len(peerIDs) == 0 {
		return nil
	}
	result := make([]string, 0, len(peerIDs))
	offset := int(startIndex % uint64(len(peerIDs)))
	result = append(result, peerIDs[offset:]...)
	result = append(result, peerIDs[:offset]...)
	return result
}

func rpcForwardResultText(result []byte) string {
	if len(result) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(result, &text); err == nil {
		return text
	}
	return string(result)
}

func isRetryableTransactionForwardError(forwardError *poswire.RPCForwardError) bool {
	errorText := strings.ToLower(rpcForwardErrorText(forwardError))
	return strings.Contains(errorText, "recent blockhash is not valid") ||
		strings.Contains(errorText, "mempool is full")
}

func rpcForwardErrorText(forwardError *poswire.RPCForwardError) string {
	if forwardError == nil {
		return ""
	}
	if len(forwardError.Data) == 0 {
		return forwardError.Message
	}
	var dataText string
	if err := json.Unmarshal(forwardError.Data, &dataText); err == nil {
		return dataText
	}
	return string(forwardError.Data)
}

func minInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
}

func (node *rpcNode) selectValidatorPeer() (string, error) {
	candidates := node.connectedValidatorPeerIDs()
	if len(candidates) == 0 {
		return "", fmt.Errorf("no connected validator peer")
	}
	index := node.nextPeerIndex.Add(1)
	return candidates[int(index%uint64(len(candidates)))], nil
}

func (node *rpcNode) connectedValidatorPeerIDs() []string {
	if node.host == nil {
		return nil
	}
	peerIDs := make([]string, 0)
	for _, snapshot := range node.host.PeerSnapshots() {
		if !isValidatorPeer(snapshot) {
			continue
		}
		if _, connected := node.host.ConnectionState(snapshot.ID); !connected {
			continue
		}
		peerIDs = append(peerIDs, snapshot.ID)
	}
	sort.Strings(peerIDs)
	return peerIDs
}

func isValidatorPeer(snapshot p2p.PeerSnapshot) bool {
	if snapshot.Validator {
		return true
	}
	if snapshot.Role == p2p.PeerRoleValidator {
		return true
	}
	return snapshot.Capabilities&p2p.PeerCapabilityValidator != 0
}

func unavailableRPCError(message string) *rpc.Error {
	return &rpc.Error{Code: rpc.CodeMethodUnavailable, Message: "method unavailable", Data: message}
}

func internalRPCError(action string, err error) *rpc.Error {
	return &rpc.Error{Code: rpc.CodeInternalError, Message: "internal error", Data: fmt.Sprintf("%s: %v", action, err)}
}

func (node *rpcNode) nodeStatusHandler() rpc.HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *rpc.Error) {
		_ = params
		return node.nodeStatus(ctx), nil
	}
}

func (node *rpcNode) nodeStatus(ctx context.Context) map[string]any {
	status := map[string]any{
		"node_mode":             "rpcnode",
		"node_name":             node.config.NodeName,
		"peer_id":               node.keyPair.peerID,
		"node_role":             string(p2p.PeerRolePublicRPC),
		"node_roles":            p2p.PeerRoleNames(p2p.PeerRolePublicRPC, rpcNodeCapabilities()),
		"node_capabilities":     uint64(rpcNodeCapabilities()),
		"node_capability_names": p2p.PeerCapabilityNames(rpcNodeCapabilities()),
		"validator_enabled":     false,
		"consensus_enabled":     false,
		"rpc_forwarding":        true,
		"known_peer_count":      len(node.host.PeerSnapshots()),
		"connected_peer_count":  node.host.ConnectionCount(),
		"p2p_secure_session":    !node.config.allowInsecure(),
		"p2p_insecure_allowed":  node.config.allowInsecure(),
		"head_height":           uint64(0),
		"head_slot":             uint64(0),
		"finalized_height":      uint64(0),
		"finalized_slot":        uint64(0),
		"mempool_size":          0,
		"validator_count":       0,
		"transaction_fast_path": map[string]any{"fast_path_available": false},
		"consensus":             map[string]any{"available": false, "validator_count": 0},
	}
	upstream, err := node.upstreamNodeStatus(ctx)
	if err != nil {
		status["upstream_error"] = err.Error()
		return status
	}
	status["upstream_node_status"] = upstream
	copyUpstreamStatusFields(status, upstream)
	return status
}

func (node *rpcNode) upstreamNodeStatus(ctx context.Context) (map[string]any, error) {
	result, rpcError := node.forwardMethod(ctx, rpc.MethodGetNodeStatus, json.RawMessage("[]"))
	if rpcError != nil {
		return nil, fmt.Errorf("%v", rpcError.Data)
	}
	upstream := make(map[string]any)
	if err := json.Unmarshal(result, &upstream); err != nil {
		return nil, fmt.Errorf("decode upstream node status: %w", err)
	}
	return upstream, nil
}

func copyUpstreamStatusFields(status map[string]any, upstream map[string]any) {
	for _, key := range []string{
		"chain_id",
		"chain_identity_hash",
		"genesis_hash",
		"head_height",
		"head_slot",
		"finalized_height",
		"finalized_slot",
		"mempool_size",
		"validator_count",
		"transaction_fast_path",
		"consensus",
	} {
		if value, exists := upstream[key]; exists {
			status[key] = value
		}
	}
}

func (node *rpcNode) peerNetworkHandler() rpc.HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *rpc.Error) {
		_ = ctx
		_ = params
		return node.peerNetwork(), nil
	}
}

func (node *rpcNode) consensusStatusHandler() rpc.HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *rpc.Error) {
		_ = ctx
		_ = params
		return map[string]any{
			"available":         false,
			"validator_count":   0,
			"local_validator":   map[string]any{},
			"validators":        []any{},
			"rpc_forwarding":    true,
			"validator_enabled": false,
		}, nil
	}
}

func (node *rpcNode) peerNetwork() rpc.PeerNetworkResult {
	result := rpc.PeerNetworkResult{LocalPeerID: node.keyPair.peerID}
	if node.host == nil {
		return result
	}
	peerSnapshots := append([]p2p.PeerSnapshot{node.localPeerSnapshot()}, node.host.PeerSnapshots()...)
	connectionStates := make(map[string]p2p.ConnectionState, len(peerSnapshots))
	for _, peerSnapshot := range peerSnapshots {
		connectionState, ok := node.host.ConnectionState(peerSnapshot.ID)
		if ok {
			connectionStates[peerSnapshot.ID] = connectionState
		}
	}
	return buildPeerNetworkResult(node.keyPair.peerID, peerSnapshots, connectionStates)
}

func (node *rpcNode) localPeerSnapshot() p2p.PeerSnapshot {
	return p2p.PeerSnapshot{
		ID:           node.keyPair.peerID,
		Status:       p2p.PeerStatusConnected,
		Role:         p2p.PeerRolePublicRPC,
		Capabilities: rpcNodeCapabilities(),
		Validator:    false,
	}
}

func buildPeerNetworkResult(
	localPeerID string,
	peerSnapshots []p2p.PeerSnapshot,
	connectionStates map[string]p2p.ConnectionState,
) rpc.PeerNetworkResult {
	result := rpc.PeerNetworkResult{
		LocalPeerID: localPeerID,
		Peers:       make([]rpc.PeerNetworkPeerResult, 0, len(peerSnapshots)),
	}
	for _, peerSnapshot := range peerSnapshots {
		connectionState, connected := connectionStates[peerSnapshot.ID]
		result.Peers = append(result.Peers, rpc.PeerNetworkPeerResult{
			PeerID:                    peerSnapshot.ID,
			Status:                    string(peerSnapshot.Status),
			Role:                      string(peerSnapshot.Role),
			Roles:                     p2p.PeerRoleNames(peerSnapshot.Role, peerSnapshot.Capabilities),
			Capabilities:              uint64(peerSnapshot.Capabilities),
			CapabilityNames:           p2p.PeerCapabilityNames(peerSnapshot.Capabilities),
			Validator:                 isValidatorPeer(peerSnapshot),
			Connected:                 connected,
			BestAddress:               bestPeerAddress(peerSnapshot, connectionState, connected),
			AdvertisedAddresses:       stringifyMultiAddresses(peerSnapshot.AdvertisedAddresses),
			VerifiedAddresses:         stringifyMultiAddresses(peerSnapshot.VerifiedAddresses),
			PreferredProtocols:        stringifyProtocols(peerSnapshot.PreferredProtocols),
			LatestSlot:                peerSnapshot.LatestSlot,
			BlockHeight:               peerSnapshot.BlockHeight,
			FailureCount:              peerSnapshot.FailureCount,
			LastError:                 visiblePeerLastError(peerSnapshot, connected),
			LastSeenUnixMilli:         peerSnapshot.LastSeenUnixMilli,
			LastConnectedUnixMilli:    peerSnapshot.LastConnectedUnixMilli,
			LastDisconnectedUnixMilli: peerSnapshot.LastDisconnectedUnixMilli,
			Connection:                buildPeerConnectionInfo(connectionState, connected),
		})
	}
	sort.Slice(result.Peers, func(leftIndex int, rightIndex int) bool {
		leftPeer := result.Peers[leftIndex]
		rightPeer := result.Peers[rightIndex]
		if leftPeer.Connected != rightPeer.Connected {
			return leftPeer.Connected
		}
		if leftPeer.Validator != rightPeer.Validator {
			return leftPeer.Validator
		}
		return leftPeer.PeerID < rightPeer.PeerID
	})
	return result
}

func visiblePeerLastError(peerSnapshot p2p.PeerSnapshot, connected bool) string {
	if connected {
		return ""
	}
	return peerSnapshot.LastError
}

func buildPeerConnectionInfo(connectionState p2p.ConnectionState, connected bool) *rpc.PeerConnectionInfo {
	if !connected {
		return nil
	}
	return &rpc.PeerConnectionInfo{
		Protocol:               string(connectionState.Protocol),
		RemoteAddress:          connectionState.RemoteAddress,
		ObservedRemoteAddress:  connectionState.ObservedRemoteAddress,
		Encrypted:              connectionState.Encrypted,
		ConnectedAtUnixMilli:   connectionState.ConnectedAtUnixMilli,
		LastReadUnixMilli:      connectionState.LastReadUnixMilli,
		LastWriteUnixMilli:     connectionState.LastWriteUnixMilli,
		LastHeartbeatUnixMilli: connectionState.LastHeartbeatUnixMilli,
		FailureCount:           connectionState.FailureCount,
	}
}

func bestPeerAddress(peerSnapshot p2p.PeerSnapshot, connectionState p2p.ConnectionState, connected bool) string {
	if len(peerSnapshot.VerifiedAddresses) > 0 {
		return peerSnapshot.VerifiedAddresses[0].String()
	}
	if len(peerSnapshot.AdvertisedAddresses) > 0 {
		return peerSnapshot.AdvertisedAddresses[0].String()
	}
	if connected {
		return connectionState.RemoteAddress
	}
	return ""
}

func stringifyMultiAddresses(addresses []utils.MultiAddress) []string {
	if len(addresses) == 0 {
		return nil
	}
	values := make([]string, 0, len(addresses))
	for _, address := range addresses {
		values = append(values, address.String())
	}
	return values
}

func stringifyProtocols(protocols []utils.MultiAddressProtocol) []string {
	if len(protocols) == 0 {
		return nil
	}
	values := make([]string, 0, len(protocols))
	for _, protocol := range protocols {
		values = append(values, string(protocol))
	}
	return values
}
