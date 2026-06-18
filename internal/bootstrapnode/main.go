package bootstrapnode

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"syscall"
	"time"

	"solana_golang/p2p"
	"solana_golang/utils"
)

const (
	defaultDialIntervalMillis = int64(2000)
	defaultMaxPeers           = 256
	defaultMaxConnections     = 256
	defaultSoftwareVersion    = "bootstrapnode/0.1.0"
	maxPeerKeystoreBytes      = 8192
)

type bootstrapConfig struct {
	Environment        string       `json:"environment"`
	Production         bool         `json:"production"`
	NodeName           string       `json:"node_name"`
	ListenIP           string       `json:"listen_ip"`
	AdvertisedIP       string       `json:"advertised_ip"`
	ListenPort         int          `json:"listen_port"`
	PeerSeed           string       `json:"peer_seed"`
	PeerKeyPath        string       `json:"peer_key_path,omitempty"`
	AllowInsecureP2P   *bool        `json:"allow_insecure_p2p,omitempty"`
	NetworkID          string       `json:"network_id,omitempty"`
	SoftwareVersion    string       `json:"software_version,omitempty"`
	MaxPeers           int          `json:"max_peers,omitempty"`
	MaxConnections     int          `json:"max_connections,omitempty"`
	DialIntervalMillis int64        `json:"dial_interval_millis,omitempty"`
	StaticPeers        []peerConfig `json:"static_peers,omitempty"`
}

type peerConfig struct {
	PeerID       string   `json:"peer_id"`
	IP           string   `json:"ip"`
	Port         int      `json:"port"`
	Role         string   `json:"role,omitempty"`
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
}

func PeerIDFromSeed(seedText string) (string, error) {
	keyPair, err := rawKeyPairFromSeed(seedText)
	if err != nil {
		return "", fmt.Errorf("bootstrapnode: derive peer id: %w", err)
	}
	return keyPair.peerID, nil
}

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
		return fmt.Errorf("bootstrapnode: load peer key: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	host, err := newHost(config, keyPair, logger)
	if err != nil {
		return err
	}
	defer host.Close()

	knownPeerIDs, err := addStaticPeers(host, config.StaticPeers)
	if err != nil {
		return err
	}
	listenAddress, err := utils.BuildMultiAddress(utils.MultiAddressIP4, config.ListenIP, utils.ProtocolTCP, config.ListenPort, keyPair.peerID)
	if err != nil {
		return fmt.Errorf("bootstrapnode: build listen address: %w", err)
	}
	go func() {
		if err := host.Listen(ctx, listenAddress, host.HandleConnection); err != nil {
			logger.Error("bootstrapnode p2p listen failed", slog.Any("error", err))
		}
	}()
	go connectPeersLoop(ctx, host, knownPeerIDs, config.dialInterval(), logger)

	logger.Info("bootstrapnode started",
		slog.String("node", config.NodeName),
		slog.String("peer_id", keyPair.peerID),
		slog.String("listen_address", listenAddress.String()),
		slog.String("advertised_ip", config.AdvertisedIP),
		slog.Int("static_peers", len(knownPeerIDs)),
		slog.Bool("secure_session", !config.allowInsecure()),
	)

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	<-signals
	logger.Info("bootstrapnode shutdown requested")
	return nil
}

func loadConfig(path string) (bootstrapConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return bootstrapConfig{}, fmt.Errorf("bootstrapnode: read config: %w", err)
	}
	config := bootstrapConfig{}
	if err := json.Unmarshal(data, &config); err != nil {
		return bootstrapConfig{}, fmt.Errorf("bootstrapnode: decode config: %w", err)
	}
	return normalizeConfig(config)
}

func normalizeConfig(config bootstrapConfig) (bootstrapConfig, error) {
	config.NodeName = strings.TrimSpace(config.NodeName)
	config.ListenIP = strings.TrimSpace(config.ListenIP)
	config.AdvertisedIP = strings.TrimSpace(config.AdvertisedIP)
	config.PeerSeed = strings.TrimSpace(config.PeerSeed)
	config.PeerKeyPath = strings.TrimSpace(config.PeerKeyPath)
	config.NetworkID = strings.TrimSpace(config.NetworkID)
	config.SoftwareVersion = strings.TrimSpace(config.SoftwareVersion)
	if config.NodeName == "" {
		return bootstrapConfig{}, fmt.Errorf("bootstrapnode: node name is empty")
	}
	if config.ListenIP == "" {
		return bootstrapConfig{}, fmt.Errorf("bootstrapnode: listen ip is empty")
	}
	if config.ListenPort < 1 || config.ListenPort > 65535 {
		return bootstrapConfig{}, fmt.Errorf("bootstrapnode: invalid listen port")
	}
	if config.PeerSeed == "" && config.PeerKeyPath == "" {
		return bootstrapConfig{}, fmt.Errorf("bootstrapnode: peer key material is empty")
	}
	if isProductionBootstrapConfig(config) && config.PeerKeyPath == "" {
		return bootstrapConfig{}, fmt.Errorf("bootstrapnode: production peer key path is required")
	}
	if config.MaxPeers == 0 {
		config.MaxPeers = defaultMaxPeers
	}
	if config.MaxPeers < 1 {
		return bootstrapConfig{}, fmt.Errorf("bootstrapnode: max peers must be positive")
	}
	if config.MaxConnections == 0 {
		config.MaxConnections = defaultMaxConnections
	}
	if config.MaxConnections < 1 {
		return bootstrapConfig{}, fmt.Errorf("bootstrapnode: max connections must be positive")
	}
	if config.DialIntervalMillis == 0 {
		config.DialIntervalMillis = defaultDialIntervalMillis
	}
	if config.DialIntervalMillis < 200 {
		return bootstrapConfig{}, fmt.Errorf("bootstrapnode: dial interval must be >= 200ms")
	}
	if config.SoftwareVersion == "" {
		config.SoftwareVersion = defaultSoftwareVersion
	}
	if !config.allowInsecure() && config.NetworkID == "" {
		return bootstrapConfig{}, fmt.Errorf("bootstrapnode: secure network id is required")
	}
	return config, nil
}

func newHost(config bootstrapConfig, keyPair rawKeyPair, logger *slog.Logger) (*p2p.Host, error) {
	hostConfig := p2p.HostConfig{
		PeerID:             keyPair.peerID,
		AllowInsecure:      config.allowInsecure(),
		Production:         config.Production,
		Environment:        config.Environment,
		PreferredProtocols: []utils.MultiAddressProtocol{utils.ProtocolTCP},
		MaxPeers:           config.MaxPeers,
		MaxConnections:     config.MaxConnections,
		Logger:             logger,
	}
	if config.AdvertisedIP != "" {
		address, err := utils.BuildMultiAddress(utils.MultiAddressIP4, config.AdvertisedIP, utils.ProtocolTCP, config.ListenPort, keyPair.peerID)
		if err != nil {
			return nil, fmt.Errorf("bootstrapnode: build advertised address: %w", err)
		}
		hostConfig.AdvertisedAddresses = []utils.MultiAddress{address}
	}
	if !config.allowInsecure() {
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
		return nil, fmt.Errorf("bootstrapnode: create host: %w", err)
	}
	return host, nil
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
			return nil, fmt.Errorf("bootstrapnode: add static peer %s: %w", peer.ID, err)
		}
		peerIDs = append(peerIDs, peer.ID)
	}
	return uniqueStrings(peerIDs), nil
}

func newStaticPeer(config peerConfig) (p2p.Peer, error) {
	peerID := strings.TrimSpace(config.PeerID)
	ipAddress := strings.TrimSpace(config.IP)
	if peerID == "" || ipAddress == "" {
		return p2p.Peer{}, fmt.Errorf("bootstrapnode: static peer requires peer_id and ip")
	}
	address, err := utils.BuildMultiAddress(utils.MultiAddressIP4, ipAddress, utils.ProtocolTCP, config.Port, peerID)
	if err != nil {
		return p2p.Peer{}, fmt.Errorf("bootstrapnode: build static peer address: %w", err)
	}
	peer, err := p2p.NewPeer(peerID, []utils.MultiAddress{address})
	if err != nil {
		return p2p.Peer{}, fmt.Errorf("bootstrapnode: create static peer: %w", err)
	}
	peer.Role = parsePeerRole(config.Role)
	peer.Capabilities = parsePeerCapabilities(config.Capabilities, peer.Role)
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
			logger.Debug("bootstrapnode peer dial failed", slog.String("peer_id", peerID), slog.Any("error", err))
			continue
		}
		logger.Info("bootstrapnode peer connected", slog.String("peer_id", peerID))
	}
}

func parsePeerRole(value string) p2p.PeerRole {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "bootnode":
		return p2p.PeerRoleBootnode
	case "full":
		return p2p.PeerRoleFull
	case "validator":
		return p2p.PeerRoleValidator
	default:
		return p2p.PeerRoleUnknown
	}
}

func parsePeerCapabilities(values []string, role p2p.PeerRole) p2p.PeerCapability {
	var capabilities p2p.PeerCapability
	for _, value := range values {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "relay":
			capabilities |= p2p.PeerCapabilityRelay
		case "archive":
			capabilities |= p2p.PeerCapabilityArchive
		case "validator":
			capabilities |= p2p.PeerCapabilityValidator
		case "state_sync":
			capabilities |= p2p.PeerCapabilityStateSync
		case "dht":
			capabilities |= p2p.PeerCapabilityDHT
		}
	}
	if role == p2p.PeerRoleValidator {
		capabilities |= p2p.PeerCapabilityValidator | p2p.PeerCapabilityRelay
	}
	if role == p2p.PeerRoleBootnode {
		capabilities |= p2p.PeerCapabilityDHT | p2p.PeerCapabilityRelay
	}
	return capabilities
}

func (config bootstrapConfig) allowInsecure() bool {
	if config.AllowInsecureP2P == nil {
		if isProductionBootstrapConfig(config) {
			return false
		}
		return true
	}
	return *config.AllowInsecureP2P
}

func isProductionBootstrapConfig(config bootstrapConfig) bool {
	if config.Production {
		return true
	}
	environment := strings.TrimSpace(strings.ToLower(config.Environment))
	return environment == "production" || environment == "prod"
}

func (config bootstrapConfig) dialInterval() time.Duration {
	return time.Duration(config.DialIntervalMillis) * time.Millisecond
}

func loadPeerKeyPair(config bootstrapConfig) (rawKeyPair, error) {
	if strings.TrimSpace(config.PeerKeyPath) != "" {
		keyFile, err := loadPeerKeystore(config.PeerKeyPath, isProductionBootstrapConfig(config))
		if err != nil {
			return rawKeyPair{}, err
		}
		return rawKeyPairFromKeystore(keyFile)
	}
	if isProductionBootstrapConfig(config) {
		return rawKeyPair{}, fmt.Errorf("bootstrapnode: peer key path is required in production")
	}
	return rawKeyPairFromSeed(config.PeerSeed)
}

func loadPeerKeystore(path string, production bool) (peerKeystoreFile, error) {
	cleanPath := filepath.Clean(strings.TrimSpace(path))
	if cleanPath == "." || cleanPath == "" {
		return peerKeystoreFile{}, fmt.Errorf("bootstrapnode: peer keystore path is empty")
	}
	info, err := os.Stat(cleanPath)
	if err != nil {
		return peerKeystoreFile{}, fmt.Errorf("bootstrapnode: stat peer keystore: %w", err)
	}
	if info.IsDir() {
		return peerKeystoreFile{}, fmt.Errorf("bootstrapnode: peer keystore is a directory")
	}
	if info.Size() <= 0 || info.Size() > maxPeerKeystoreBytes {
		return peerKeystoreFile{}, fmt.Errorf("bootstrapnode: invalid peer keystore size")
	}
	if production && goruntime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return peerKeystoreFile{}, fmt.Errorf("bootstrapnode: peer keystore must not be group/world readable")
	}
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return peerKeystoreFile{}, fmt.Errorf("bootstrapnode: read peer keystore: %w", err)
	}
	keyFile := peerKeystoreFile{}
	if err := json.Unmarshal(data, &keyFile); err != nil {
		return peerKeystoreFile{}, fmt.Errorf("bootstrapnode: decode peer keystore: %w", err)
	}
	return keyFile, nil
}

func rawKeyPairFromKeystore(keyFile peerKeystoreFile) (rawKeyPair, error) {
	privateKey, err := privateKeyFromKeystore(keyFile)
	if err != nil {
		return rawKeyPair{}, err
	}
	return rawKeyPairFromPrivateKey(privateKey)
}

func privateKeyFromKeystore(keyFile peerKeystoreFile) ([]byte, error) {
	if strings.TrimSpace(keyFile.Seed) != "" {
		return utils.SHA256([]byte(strings.TrimSpace(keyFile.Seed))), nil
	}
	if strings.TrimSpace(keyFile.SeedBase64) != "" {
		return decodeSizedKeystoreBase64(keyFile.SeedBase64, utils.Ed25519KeySize, "peer seed")
	}
	if strings.TrimSpace(keyFile.PrivateKeyBase64) != "" {
		return decodeSizedKeystoreBase64(keyFile.PrivateKeyBase64, utils.Ed25519KeySize, "peer private key")
	}
	return nil, fmt.Errorf("bootstrapnode: peer keystore has no key material")
}

func decodeSizedKeystoreBase64(encodedValue string, expectedSize int, fieldName string) ([]byte, error) {
	value, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedValue))
	if err != nil {
		return nil, fmt.Errorf("bootstrapnode: decode %s: %w", fieldName, err)
	}
	if len(value) != expectedSize {
		return nil, fmt.Errorf("bootstrapnode: %s requires %d bytes", fieldName, expectedSize)
	}
	return value, nil
}

func rawKeyPairFromSeed(seedText string) (rawKeyPair, error) {
	return rawKeyPairFromPrivateKey(utils.SHA256([]byte(strings.TrimSpace(seedText))))
}

func rawKeyPairFromPrivateKey(privateKey []byte) (rawKeyPair, error) {
	publicKey, err := utils.DeriveEd25519PublicKeyFromPrivateKey(privateKey)
	if err != nil {
		return rawKeyPair{}, err
	}
	return rawKeyPair{publicKey: utils.CloneBytes(publicKey), privateKey: utils.CloneBytes(privateKey), peerID: utils.Base58Encode(publicKey)}, nil
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
