package posnode

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"solana_golang/blockchain"
	"solana_golang/consensus"
	"solana_golang/database"
	"solana_golang/p2p"
	bpfloader "solana_golang/programs/bpfloader"
	"solana_golang/programs/privacy"
	"solana_golang/programs/stake"
	"solana_golang/programs/system"
	tokenprogram "solana_golang/programs/token"
	vmprogram "solana_golang/programs/vm"
	"solana_golang/rpc"
	"solana_golang/runtime"
	"solana_golang/structure"
	"solana_golang/utils"
	svm "solana_golang/vm"
)

const (
	protocolNetworkID      = "pos-localnet"
	posNodeSoftwareVersion = "posnode/1.0.0"
	defaultConsensusQuorum = 2
)

type posNode struct {
	mutex             sync.Mutex
	config            nodeConfig
	logger            *slog.Logger
	startedAt         time.Time
	host              *p2p.Host
	rpcServer         *rpc.Server
	db                database.Database
	ledger            *blockchain.Ledger
	executor          runtime.FixedExecutor
	peerKeyPair       rawKeyPair
	stakerKeyPair     structure.SolanaKeyPair
	validatorKeyPair  structure.SolanaKeyPair
	consensusKeyPair  structure.SolanaKeyPair
	blsKeyPair        consensus.BLSKeyPair
	blockhashQueue    structure.BlockhashQueue
	mempool           []structure.Transaction
	seenTransactions  map[string]struct{}
	seenProposals     map[string]struct{}
	seenQCs           map[string]uint64
	pendingEvidence   []consensus.SlashingEvidence
	seenEvidence      map[string]struct{}
	proposalChoices   map[string]consensus.BlockProposal
	signedVoteChoices map[string]consensus.SignedVote
	orphanProposals   map[structure.Hash][]consensus.BlockProposal
	epochSnapshot     consensus.EpochSnapshot
	leaderSchedule    consensus.LeaderSchedule
	voteCollector     *consensus.VoteCollector
	metrics           nodeMetrics
	lastProducedSlot  uint64
	lastVotedSlot     uint64
	registeredSelf    bool
	knownPeerIDs      []string
	workerGroup       sync.WaitGroup
}

type rawKeyPair struct {
	publicKey  []byte
	privateKey []byte
	peerID     string
}

type localValidatorKeyPairs struct {
	staker    structure.SolanaKeyPair
	validator structure.SolanaKeyPair
	consensus structure.SolanaKeyPair
	bls       consensus.BLSKeyPair
}

func PeerIDFromSeed(seedText string) (string, error) {
	keyPair, err := rawKeyPairFromSeedText(seedText)
	if err != nil {
		return "", fmt.Errorf("posnode: derive peer id: %w", err)
	}
	return keyPair.peerID, nil
}

func Run(configPath string) error {
	config, err := loadNodeConfig(configPath)
	if err != nil {
		return err
	}
	logger, err := utils.LoggerFromEnv()
	if err != nil {
		return err
	}
	slog.SetDefault(logger)
	node, err := newPosNode(config, logger)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := node.start(ctx); err != nil {
		return err
	}
	defer func() {
		if node.rpcServer != nil {
			shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = node.rpcServer.Shutdown(shutdownContext)
			shutdownCancel()
		}
		if node.host != nil {
			_ = node.host.Close()
		}
		if node.db != nil {
			_ = node.db.Close()
		}
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	<-signals
	logger.Info("posnode shutdown requested")
	cancel()
	node.waitForWorkers(3 * time.Second)
	return nil
}

func newPosNode(config nodeConfig, logger *slog.Logger) (*posNode, error) {
	executor, err := newRuntimeExecutorWithConfig(config, logger)
	if err != nil {
		return nil, fmt.Errorf("posnode: create executor: %w", err)
	}
	peerKeyPair, err := loadRawKeyPair(config.PeerSeed, config.PeerKeyPath, config, "peer")
	if err != nil {
		return nil, err
	}
	localKeys, err := loadLocalValidatorKeyPairs(config)
	if err != nil {
		return nil, err
	}
	node := &posNode{
		config:            config,
		logger:            logger,
		startedAt:         time.Now(),
		executor:          executor,
		peerKeyPair:       peerKeyPair,
		stakerKeyPair:     localKeys.staker,
		validatorKeyPair:  localKeys.validator,
		consensusKeyPair:  localKeys.consensus,
		blsKeyPair:        localKeys.bls,
		seenTransactions:  make(map[string]struct{}),
		seenProposals:     make(map[string]struct{}),
		seenQCs:           make(map[string]uint64),
		seenEvidence:      make(map[string]struct{}),
		proposalChoices:   make(map[string]consensus.BlockProposal),
		signedVoteChoices: make(map[string]consensus.SignedVote),
		orphanProposals:   make(map[structure.Hash][]consensus.BlockProposal),
	}
	if err := node.openLedger(); err != nil {
		return nil, err
	}
	head := node.ledger.Head()
	epochID, startSlot := node.epochForSlot(head.Slot + 1)
	if err := node.rebuildEpoch(epochID, startSlot, node.epochSeed(epochID)); err != nil {
		return nil, err
	}
	return node, nil
}

func (node *posNode) start(ctx context.Context) error {
	if err := node.startP2P(ctx); err != nil {
		return err
	}
	node.logger.Info("posnode started",
		slog.String("node", node.config.NodeName),
		slog.String("config_path", node.config.ConfigPath),
		slog.String("chain_id", node.config.ChainID),
		slog.String("chain_identity_hash", node.config.ChainIdentityHash),
		slog.String("genesis_hash", node.config.GenesisHash),
		slog.String("peer_id", node.peerKeyPair.peerID),
		slog.String("peer_key_source", keyMaterialSource(node.config.PeerKeyPath, node.config.PeerSeed, true)),
		slog.String("peer_key_location", keyMaterialLocation(node.config, node.config.PeerKeyPath, node.config.PeerSeed, true)),
		slog.String("node_role", string(node.config.ResolvedNodeRole)),
		slog.Uint64("node_capabilities", uint64(node.config.ResolvedNodeCapabilities)),
		slog.Bool("validator_enabled", node.config.validatorEnabled()),
		slog.Bool("consensus_enabled", node.config.consensusEnabled()),
		slog.Bool("transaction_forward_enabled", node.config.transactionForwardEnabled()),
		slog.String("staker", node.stakerKeyPair.PublicKey.String()),
		slog.String("staker_key_source", keyMaterialSource(node.config.StakerKeyPath, node.config.StakerSeed, node.config.validatorEnabled())),
		slog.String("staker_key_location", keyMaterialLocation(node.config, node.config.StakerKeyPath, node.config.StakerSeed, node.config.validatorEnabled())),
		slog.String("validator", node.validatorKeyPair.PublicKey.String()),
		slog.String("validator_key_source", keyMaterialSource(node.config.ValidatorKeyPath, node.config.ValidatorSeed, node.config.validatorEnabled())),
		slog.String("validator_key_location", keyMaterialLocation(node.config, node.config.ValidatorKeyPath, node.config.ValidatorSeed, node.config.validatorEnabled())),
		slog.String("consensus", node.consensusKeyPair.PublicKey.String()),
		slog.String("consensus_key_source", keyMaterialSource(node.config.ConsensusKeyPath, node.config.ConsensusSeed, node.config.validatorEnabled())),
		slog.String("consensus_key_location", keyMaterialLocation(node.config, node.config.ConsensusKeyPath, node.config.ConsensusSeed, node.config.validatorEnabled())),
		slog.String("bls_public_key", utils.Base58Encode(node.blsKeyPair.PublicKey)),
		slog.String("bls_key_source", keyMaterialSource(node.config.BLSKeyPath, node.config.ConsensusSeed, node.config.validatorEnabled())),
		slog.String("bls_key_location", keyMaterialLocation(node.config, node.config.BLSKeyPath, node.config.ConsensusSeed, node.config.validatorEnabled())),
		slog.String("treasury_key_source", keyMaterialSource(node.config.TreasuryKeyPath, "", !node.config.publicRPCMode())),
		slog.String("treasury_key_location", keyMaterialLocation(node.config, node.config.TreasuryKeyPath, "", !node.config.publicRPCMode())),
		slog.Uint64("genesis_supply", node.config.Genesis.InitialSupplyLamports),
		slog.Int64("genesis_start_unix_millis", node.config.GenesisStartMs),
		slog.Uint64("finality_depth", node.config.FinalityDepth),
		slog.Bool("p2p_insecure_allowed", node.config.allowInsecureP2P()),
		slog.String("privacy_execution_mode", string(node.config.PrivacyExecutionMode)),
		slog.String("program_execution_policy", runtime.NormalizeProgramExecutionPolicy(node.executor.ProgramExecutionPolicy).Fingerprint()),
		slog.Bool("state_recovery_enabled", !node.config.DisableStateRecovery),
		slog.String("data_root_path", node.config.DataRootPath),
		slog.String("data_path", node.config.DataPath),
	)
	if node.config.AutoRegister && node.config.validatorEnabled() {
		go node.autoRegisterLoop(ctx)
	}
	if err := node.startRPC(); err != nil {
		return err
	}
	node.startWorker(func() {
		node.blockSyncLoop(ctx)
	})
	if node.config.consensusEnabled() {
		node.startWorker(func() {
			node.slotLoop(ctx)
		})
	}
	return nil
}

// keyMaterialSource 标识密钥来源 + 日志只暴露来源类型避免泄露 seed 或私钥。
func keyMaterialSource(keyPath string, seedText string, enabled bool) string {
	if !enabled {
		return "disabled"
	}
	if strings.TrimSpace(keyPath) != "" {
		return "keystore_file"
	}
	if strings.TrimSpace(seedText) != "" {
		return "config_seed"
	}
	return "missing"
}

// keyMaterialLocation 标识密钥位置 + 用户排查登录材料时只需要位置不需要日志明文。
func keyMaterialLocation(config nodeConfig, keyPath string, seedText string, enabled bool) string {
	if !enabled {
		return ""
	}
	if strings.TrimSpace(keyPath) != "" {
		return strings.TrimSpace(keyPath)
	}
	if strings.TrimSpace(seedText) != "" {
		return config.ConfigPath
	}
	return ""
}

// loadLocalValidatorKeyPairs 加载本地验证者密钥 + 公网 RPC 节点不应持有共识私钥。
func loadLocalValidatorKeyPairs(config nodeConfig) (localValidatorKeyPairs, error) {
	if !config.validatorEnabled() {
		return localValidatorKeyPairs{}, nil
	}
	stakerKeyPair, err := loadStructureKeyPair(config.StakerSeed, config.StakerKeyPath, config, "staker")
	if err != nil {
		return localValidatorKeyPairs{}, err
	}
	validatorKeyPair, err := loadStructureKeyPair(config.ValidatorSeed, config.ValidatorKeyPath, config, "validator")
	if err != nil {
		return localValidatorKeyPairs{}, err
	}
	consensusKeyPair, err := loadStructureKeyPair(config.ConsensusSeed, config.ConsensusKeyPath, config, "consensus")
	if err != nil {
		return localValidatorKeyPairs{}, err
	}
	blsKeyPair, err := loadBLSKeyPair(config.ConsensusSeed, config.BLSKeyPath, config)
	if err != nil {
		return localValidatorKeyPairs{}, err
	}
	return localValidatorKeyPairs{
		staker:    stakerKeyPair,
		validator: validatorKeyPair,
		consensus: consensusKeyPair,
		bls:       blsKeyPair,
	}, nil
}

// startWorker 跟踪后台任务 + 关闭时需要等待 goroutine 退出后再关闭数据库。
func (node *posNode) startWorker(run func()) {
	node.workerGroup.Add(1)
	go func() {
		defer node.workerGroup.Done()
		run()
	}()
}

// waitForWorkers 等待后台任务退出 + 防止进程退出时 goroutine 继续访问已关闭存储。
func (node *posNode) waitForWorkers(timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		node.workerGroup.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		node.logger.Warn("posnode worker shutdown timed out", slog.Duration("timeout", timeout))
	}
}

func (node *posNode) startRPC() error {
	if !node.config.RPCEnabled {
		return nil
	}
	address := fmt.Sprintf("%s:%d", node.config.RPCListenIP, node.config.RPCPort)
	router := rpc.NewDefaultRouter(node)
	if node.config.publicRPCMode() {
		router = rpc.NewPublicRouter(node)
	}
	server := rpc.NewServer(rpc.ServerConfig{Address: address, Logger: node.logger}, router)
	node.rpcServer = server
	go func() {
		if err := server.ListenAndServe(); err != nil {
			node.logger.Error("posnode rpc server failed", slog.Any("error", err))
		}
	}()
	return nil
}

func (node *posNode) openLedger() error {
	db, err := database.NewDatabase(database.DatabaseConfig{
		Path:   node.config.DataPath,
		Engine: database.EnginePebble,
		WAL:    true,
	})
	if err != nil {
		return fmt.Errorf("posnode: open blockchain database: %w", err)
	}
	node.db = db
	genesisConfig, err := node.blockchainGenesisConfig()
	if err != nil {
		_ = db.Close()
		return err
	}
	ledger, err := blockchain.LoadOrCreateLedgerWithConfig(db, genesisConfig, blockchain.LedgerConfig{
		FinalityDepth:        node.config.FinalityDepth,
		DisableStateRecovery: node.config.DisableStateRecovery,
	})
	if err != nil {
		_ = db.Close()
		return fmt.Errorf("posnode: load blockchain ledger: %w", err)
	}
	node.ledger = ledger
	node.ledger.SetLogger(node.logger)
	head := ledger.Head()
	node.blockhashQueue = structure.NewBlockhashQueue(150)
	if err := node.blockhashQueue.Add(structure.RecentBlockhashEntry{
		Blockhash:     head.BlockHash,
		Slot:          head.Slot,
		FeeCalculator: structure.DefaultFeeCalculator(),
		TimestampUnix: time.Now().Unix(),
	}); err != nil {
		return fmt.Errorf("posnode: init blockhash queue: %w", err)
	}
	if err := node.loadMempool(); err != nil {
		return err
	}
	node.logger.Info("posnode ledger ready",
		slog.String("chain_id", node.config.ChainID),
		slog.String("chain_identity_hash", node.config.ChainIdentityHash),
		slog.String("genesis_hash", node.config.GenesisHash),
		slog.Uint64("height", head.Height),
		slog.Uint64("slot", head.Slot),
		slog.String("block_hash", head.BlockHash.String()),
		slog.String("state_root", head.StateRoot.String()),
		slog.Int("mempool", len(node.mempool)),
	)
	return nil
}

func (node *posNode) blockchainGenesisConfig() (blockchain.GenesisConfig, error) {
	return buildBlockchainGenesisConfig(node.config)
}

func genesisPublicKeyFromAddressOrSeed(addressText string, seedText string, fieldName string) (structure.PublicKey, error) {
	addressText = strings.TrimSpace(addressText)
	if addressText != "" {
		publicKey, err := structure.PublicKeyFromBase58(addressText)
		if err != nil {
			return structure.PublicKey{}, fmt.Errorf("posnode: decode genesis %s address: %w", fieldName, err)
		}
		return publicKey, nil
	}
	seedText = strings.TrimSpace(seedText)
	if seedText == "" {
		return structure.PublicKey{}, fmt.Errorf("posnode: genesis %s key is empty", fieldName)
	}
	keyPair, err := keyPairFromSeed(seedText)
	if err != nil {
		return structure.PublicKey{}, fmt.Errorf("posnode: derive genesis %s key: %w", fieldName, err)
	}
	return keyPair.PublicKey, nil
}

func genesisBLSPublicKey(encodedPublicKey string, consensusSeed string) ([]byte, error) {
	encodedPublicKey = strings.TrimSpace(encodedPublicKey)
	if encodedPublicKey != "" {
		publicKey, err := utils.Base64Decode(encodedPublicKey)
		if err != nil {
			return nil, fmt.Errorf("posnode: decode genesis bls public key: %w", err)
		}
		if err := consensus.ValidateBLSPublicKey(publicKey); err != nil {
			return nil, err
		}
		return publicKey, nil
	}
	consensusSeed = strings.TrimSpace(consensusSeed)
	if consensusSeed == "" {
		return nil, nil
	}
	keyPair, err := consensus.BLSKeyPairFromSeed(utils.SHA256([]byte(consensusSeed)))
	if err != nil {
		return nil, fmt.Errorf("posnode: derive genesis bls public key: %w", err)
	}
	return keyPair.PublicKey, nil
}

func (node *posNode) startP2P(ctx context.Context) error {
	listenAddress, err := utils.BuildMultiAddress(utils.MultiAddressIP4, node.config.ListenIP, utils.ProtocolTCP, node.config.ListenPort, node.peerKeyPair.peerID)
	if err != nil {
		return fmt.Errorf("posnode: build listen address: %w", err)
	}
	advertisedAddresses, err := node.advertisedP2PAddresses()
	if err != nil {
		return err
	}
	allowInsecureP2P := node.config.allowInsecureP2P()
	hostConfig := p2p.HostConfig{
		PeerID:        node.peerKeyPair.peerID,
		Role:          node.config.ResolvedNodeRole,
		Capabilities:  node.config.ResolvedNodeCapabilities,
		AllowInsecure: allowInsecureP2P,
		Production:    node.config.Production,
		Environment:   node.config.Environment,
		PreferredProtocols: []utils.MultiAddressProtocol{
			utils.ProtocolTCP,
		},
		MaxPeers:       128,
		MaxConnections: 128,
		Logger:         node.logger,
	}
	hostConfig.AdvertisedAddresses = advertisedAddresses
	if !allowInsecureP2P {
		hostConfig.EnableSecureSession = true
		hostConfig.SecureIdentity = node.secureSessionIdentity()
	}
	host, err := p2p.NewHost(hostConfig)
	if err != nil {
		return fmt.Errorf("posnode: create host: %w", err)
	}
	node.host = host
	if allowInsecureP2P {
		node.logger.Warn("posnode p2p insecure mode enabled", slog.String("node", node.config.NodeName))
	}
	if err := node.registerProtocols(); err != nil {
		_ = host.Close()
		return err
	}
	for _, peerConfig := range node.config.BootstrapPeers {
		if peerConfig.PeerID == node.peerKeyPair.peerID {
			continue
		}
		address, err := utils.BuildMultiAddress(utils.MultiAddressIP4, peerConfig.IP, utils.ProtocolTCP, peerConfig.Port, peerConfig.PeerID)
		if err != nil {
			_ = host.Close()
			return fmt.Errorf("posnode: build peer address: %w", err)
		}
		peer, err := p2p.NewPeer(peerConfig.PeerID, []utils.MultiAddress{address})
		if err != nil {
			_ = host.Close()
			return fmt.Errorf("posnode: create peer: %w", err)
		}
		peer.Role = peerConfig.ResolvedRole
		peer.Capabilities = peerConfig.ResolvedCapabilities
		peer.Validator = peer.Capabilities&p2p.PeerCapabilityValidator != 0
		if err := host.AddPeer(peer); err != nil {
			_ = host.Close()
			return fmt.Errorf("posnode: add peer: %w", err)
		}
		node.knownPeerIDs = append(node.knownPeerIDs, peerConfig.PeerID)
	}
	go func() {
		if err := host.Listen(ctx, listenAddress, host.HandleConnection); err != nil {
			node.logger.Error("posnode p2p listen failed", slog.Any("error", err))
		}
	}()
	go node.connectPeersLoop(ctx)
	go node.bootstrapDiscoveryLoop(ctx)
	return nil
}

func (node *posNode) advertisedP2PAddresses() ([]utils.MultiAddress, error) {
	advertisedIP := strings.TrimSpace(node.config.AdvertisedIP)
	if advertisedIP == "" {
		return nil, nil
	}
	advertisedPort := node.config.AdvertisedPort
	if advertisedPort == 0 {
		advertisedPort = node.config.ListenPort
	}
	address, err := utils.BuildMultiAddress(utils.MultiAddressIP4, advertisedIP, utils.ProtocolTCP, advertisedPort, node.peerKeyPair.peerID)
	if err != nil {
		return nil, fmt.Errorf("posnode: build advertised address: %w", err)
	}
	return []utils.MultiAddress{address}, nil
}

func (node *posNode) secureSessionIdentity() p2p.SecureSessionIdentity {
	networkID := node.config.P2PNetworkID
	if strings.TrimSpace(networkID) == "" {
		networkID = node.config.ChainID
	}
	return p2p.SecureSessionIdentity{
		PeerID:          node.peerKeyPair.peerID,
		PublicKey:       utils.CloneBytes(node.peerKeyPair.publicKey),
		PrivateKey:      utils.CloneBytes(node.peerKeyPair.privateKey),
		NetworkID:       networkID,
		SoftwareVersion: posNodeSoftwareVersion,
	}
}

type programRegistration struct {
	spec    runtime.ProgramSpec
	handler runtime.ProgramHandler
}

func newRuntimeExecutor(logger *slog.Logger) (runtime.FixedExecutor, error) {
	return newRuntimeExecutorWithPrivacyMode(runtime.PrivacyExecutionModeFixed, logger)
}

func newRuntimeExecutorWithPrivacyMode(privacyExecutionMode runtime.PrivacyExecutionMode, logger *slog.Logger) (runtime.FixedExecutor, error) {
	return newRuntimeExecutorWithPolicy(privacyExecutionMode, bpfloader.DeploymentPolicy{}, logger)
}

func newRuntimeExecutorWithConfig(config nodeConfig, logger *slog.Logger) (runtime.FixedExecutor, error) {
	return newRuntimeExecutorWithPolicy(config.PrivacyExecutionMode, deploymentPolicyFromConfig(config.ContractDeploymentPolicy), logger)
}

func newRuntimeExecutorWithPolicy(privacyExecutionMode runtime.PrivacyExecutionMode, deploymentPolicy bpfloader.DeploymentPolicy, logger *slog.Logger) (runtime.FixedExecutor, error) {
	executor := runtime.NewFixedExecutorWithRegistry(runtime.NewProgramHandlerRegistry())
	executor.Logger = logger
	executor.PrivacyExecutionMode = privacyExecutionMode
	programExecutionPolicy, err := runtime.NewDefaultProgramExecutionPolicy(structure.DefaultBuiltinProgramIDs, privacyExecutionMode)
	if err != nil {
		return runtime.FixedExecutor{}, fmt.Errorf("posnode: program execution policy: %w", err)
	}
	executor.ProgramExecutionPolicy = programExecutionPolicy
	if err := registerProgramsWithPrivacyModeAndLoaderPolicy(&executor, privacyExecutionMode, deploymentPolicy); err != nil {
		return runtime.FixedExecutor{}, err
	}
	virtualMachineProgram := vmprogram.NewProgram(structure.DefaultBuiltinProgramIDs.BPFLoader, svm.Runtime{})
	if err := executor.SetFallbackProgramHandler(virtualMachineProgram.Execute); err != nil {
		return runtime.FixedExecutor{}, fmt.Errorf("posnode: register vm fallback: %w", err)
	}
	return executor, nil
}

// registerPrograms 注册链上程序处理器 + 对齐 p2p 协议注册模式降低新增程序改动面。
func registerPrograms(executor *runtime.FixedExecutor) error {
	return registerProgramsWithPrivacyMode(executor, runtime.PrivacyExecutionModeFixed)
}

func registerProgramsWithPrivacyMode(executor *runtime.FixedExecutor, privacyExecutionMode runtime.PrivacyExecutionMode) error {
	return registerProgramsWithPrivacyModeAndLoaderPolicy(executor, privacyExecutionMode, bpfloader.DeploymentPolicy{})
}

func registerProgramsWithPrivacyModeAndLoaderPolicy(executor *runtime.FixedExecutor, privacyExecutionMode runtime.PrivacyExecutionMode, deploymentPolicy bpfloader.DeploymentPolicy) error {
	privacyHandler, err := privacyProgramHandler(privacyExecutionMode)
	if err != nil {
		return err
	}
	registrations := []programRegistration{
		{
			spec:    runtime.ProgramSpec{ID: structure.DefaultBuiltinProgramIDs.System, Name: "system"},
			handler: system.NewProgram(structure.DefaultBuiltinProgramIDs.System).Execute,
		},
		{
			spec:    runtime.ProgramSpec{ID: structure.DefaultBuiltinProgramIDs.Token, Name: "token"},
			handler: tokenprogram.NewProgram(structure.DefaultBuiltinProgramIDs.Token).Execute,
		},
		{
			spec:    runtime.ProgramSpec{ID: structure.DefaultBuiltinProgramIDs.Stake, Name: "stake"},
			handler: stake.NewProgram(structure.DefaultBuiltinProgramIDs.Stake).Execute,
		},
		{
			spec:    runtime.ProgramSpec{ID: structure.DefaultBuiltinProgramIDs.Privacy, Name: "privacy"},
			handler: privacyHandler,
		},
		{
			spec:    runtime.ProgramSpec{ID: structure.DefaultBuiltinProgramIDs.BPFLoader, Name: "bpf_loader"},
			handler: bpfloader.NewProgramWithPolicy(structure.DefaultBuiltinProgramIDs.BPFLoader, deploymentPolicy).Execute,
		},
	}
	for _, registration := range registrations {
		if err := executor.RegisterProgramHandler(registration.spec, registration.handler); err != nil {
			return fmt.Errorf("posnode: register program %s: %w", registration.spec.Name, err)
		}
	}
	return nil
}

func deploymentPolicyFromConfig(config contractDeploymentPolicyConfig) bpfloader.DeploymentPolicy {
	policy := bpfloader.DeploymentPolicy{
		AllowedDeployers:             append([]structure.PublicKey(nil), config.ResolvedAllowedDeployers...),
		MinDeploymentDepositLamports: config.MinDeploymentDepositLamports,
	}
	if config.RequireManifest != nil {
		policy.RequireManifest = *config.RequireManifest
	}
	if config.AllowUpgradeableContracts != nil {
		policy.AllowUpgradeableContracts = *config.AllowUpgradeableContracts
	}
	return policy.Normalize()
}

func privacyProgramHandler(privacyExecutionMode runtime.PrivacyExecutionMode) (runtime.ProgramHandler, error) {
	normalizedMode, err := runtime.NormalizePrivacyExecutionMode(privacyExecutionMode)
	if err != nil {
		return nil, fmt.Errorf("posnode: privacy execution mode: %w", err)
	}
	switch normalizedMode {
	case runtime.PrivacyExecutionModeFixed:
		return privacy.NewProgram(structure.DefaultBuiltinProgramIDs.Privacy).Execute, nil
	case runtime.PrivacyExecutionModeVMSyscall:
		return vmprogram.NewPrivacyBridgeProgram(structure.DefaultBuiltinProgramIDs.Privacy, structure.DefaultBuiltinProgramIDs.BPFLoader, svm.Runtime{}).Execute, nil
	default:
		return nil, fmt.Errorf("posnode: unsupported privacy execution mode %s", normalizedMode)
	}
}

func (node *posNode) registerProtocols() error {
	specs := []p2p.ProtocolSpec{
		{ID: p2p.ProtocolPoSTransactionV1, Name: "/pos/transaction/1.0.0", Priority: p2p.MessagePriorityHigh, Class: p2p.ProtocolClassData},
		{ID: p2p.ProtocolPoSProposalV1, Name: "/pos/proposal/1.0.0", Priority: p2p.MessagePriorityHigh, Class: p2p.ProtocolClassData},
		{ID: p2p.ProtocolPoSVoteV1, Name: "/pos/vote/1.0.0", Priority: p2p.MessagePriorityHigh, Class: p2p.ProtocolClassData},
		{ID: p2p.ProtocolPoSQCV1, Name: "/pos/qc/1.0.0", Priority: p2p.MessagePriorityHigh, Class: p2p.ProtocolClassData},
		{ID: p2p.ProtocolPoSEvidenceV1, Name: "/pos/evidence/1.0.0", Priority: p2p.MessagePriorityHigh, Class: p2p.ProtocolClassData},
	}
	handlers := []p2p.VoidProtocolHandler{
		node.handleTransactionMessage,
		node.handleProposalMessage,
		node.handleVoteMessage,
		node.handleQCMessage,
		node.handleEvidenceMessage,
	}
	for index, spec := range specs {
		if err := node.host.RegisterVoidHandler(spec, handlers[index]); err != nil {
			return fmt.Errorf("posnode: register protocol %s: %w", spec.Name, err)
		}
	}
	resultSpecs := []p2p.ProtocolSpec{
		{ID: p2p.ProtocolPoSBlockByHashV1, Name: "/pos/sync/block-by-hash/1.0.0", HasResponse: true, Priority: p2p.MessagePriorityHigh, Class: p2p.ProtocolClassData, Concurrency: p2p.ProtocolConcurrencyStateless},
		{ID: p2p.ProtocolPoSBlockByHeightV1, Name: "/pos/sync/block-by-height/1.0.0", HasResponse: true, Priority: p2p.MessagePriorityHigh, Class: p2p.ProtocolClassData, Concurrency: p2p.ProtocolConcurrencyStateless},
		{ID: p2p.ProtocolPoSStateSnapshotV1, Name: "/pos/sync/state-snapshot/1.0.0", HasResponse: true, Priority: p2p.MessagePriorityLow, Class: p2p.ProtocolClassData, Concurrency: p2p.ProtocolConcurrencyStateless},
		{ID: p2p.ProtocolPoSStatusV1, Name: "/pos/status/1.0.0", HasResponse: true, Priority: p2p.MessagePriorityHigh, Class: p2p.ProtocolClassData, Concurrency: p2p.ProtocolConcurrencyStateless},
		{ID: p2p.ProtocolPoSBlockLocatorV1, Name: "/pos/sync/block-locator/1.0.0", HasResponse: true, Priority: p2p.MessagePriorityHigh, Class: p2p.ProtocolClassData, Concurrency: p2p.ProtocolConcurrencyStateless},
		{ID: p2p.ProtocolPoSCommonAncestorV1, Name: "/pos/sync/common-ancestor/1.0.0", HasResponse: true, Priority: p2p.MessagePriorityHigh, Class: p2p.ProtocolClassData, Concurrency: p2p.ProtocolConcurrencyStateless},
		{ID: p2p.ProtocolPoSRPCForwardV1, Name: "/pos/rpc/forward/1.0.0", HasResponse: true, Priority: p2p.MessagePriorityNormal, Class: p2p.ProtocolClassControl, Concurrency: p2p.ProtocolConcurrencyStateless},
	}
	resultHandlers := []p2p.ResultProtocolHandler{
		node.handleBlockByHashRequest,
		node.handleBlockByHeightRequest,
		node.handleStateSnapshotRequest,
		node.handleStatusRequest,
		node.handleBlockLocatorRequest,
		node.handleCommonAncestorRequest,
		node.handleRPCForwardRequest,
	}
	for index, spec := range resultSpecs {
		if err := node.host.RegisterResultHandler(spec, resultHandlers[index]); err != nil {
			return fmt.Errorf("posnode: register protocol %s: %w", spec.Name, err)
		}
	}
	return nil
}

func (node *posNode) connectPeersLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, peerID := range node.connectionPeerIDsSnapshot() {
				if _, ok := node.host.Connection(peerID); ok {
					continue
				}
				if _, err := node.host.DialPeer(ctx, peerID); err != nil {
					node.logger.Debug("posnode peer dial failed", slog.String("peer_id", peerID), slog.Any("error", err))
				}
			}
		}
	}
}

func (node *posNode) bootstrapDiscoveryLoop(ctx context.Context) {
	bootnodes := node.bootstrapAddresses()
	if len(bootnodes) == 0 {
		return
	}
	timer := time.NewTimer(200 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			node.runBootstrapDiscovery(ctx, bootnodes)
			timer.Reset(15 * time.Second)
		}
	}
}

func (node *posNode) bootstrapAddresses() []utils.MultiAddress {
	addresses := make([]utils.MultiAddress, 0, len(node.config.BootstrapPeers))
	for _, peerConfig := range node.config.BootstrapPeers {
		if peerConfig.PeerID == node.peerKeyPair.peerID {
			continue
		}
		address, err := utils.BuildMultiAddress(utils.MultiAddressIP4, peerConfig.IP, utils.ProtocolTCP, peerConfig.Port, peerConfig.PeerID)
		if err != nil {
			node.logger.Warn("posnode bootstrap address skipped", slog.String("peer_id", peerConfig.PeerID), slog.Any("error", err))
			continue
		}
		addresses = append(addresses, address)
	}
	return addresses
}

func (node *posNode) runBootstrapDiscovery(ctx context.Context, bootnodes []utils.MultiAddress) {
	summary, err := node.host.Bootstrap(ctx, p2p.BootstrapConfig{
		Bootnodes:            bootnodes,
		MinOutboundPeers:     4,
		QueryLimit:           32,
		RefreshTargetCount:   8,
		DialTimeout:          3 * time.Second,
		StartConnectionLoops: true,
	})
	if err != nil {
		node.logger.Debug("posnode bootstrap discovery failed", slog.Any("error", err))
	}
	node.refreshKnownPeersFromHost()
	node.logger.Debug("posnode bootstrap discovery completed",
		slog.Int("bootnodes", summary.BootnodeCount),
		slog.Int("connected_bootnodes", summary.ConnectedBootnodes),
		slog.Int("discovered_peers", summary.DiscoveredPeers),
		slog.Int("connected_peers", summary.ConnectedPeers),
	)
}

func (node *posNode) refreshKnownPeersFromHost() {
	if node.host == nil {
		return
	}
	for _, snapshot := range node.host.PeerSnapshots() {
		if snapshot.ID == "" || snapshot.ID == node.peerKeyPair.peerID {
			continue
		}
		node.addKnownPeerID(snapshot.ID)
	}
}

func (node *posNode) autoRegisterLoop(ctx context.Context) {
	ticker := time.NewTicker(1500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if node.localValidatorStakeAccountExists() {
			node.registeredSelf = true
			node.logger.Info("posnode register validator skipped; local validator stake account exists")
			return
		}
		if !node.autoRegisterBlockhashReady() {
			continue
		}
		transaction, err := node.buildRegisterTransaction()
		if err != nil {
			node.logger.Error("posnode build register transaction failed", slog.Any("error", err))
			return
		}
		if err := node.addTransaction(transaction); err != nil {
			node.logger.Error("posnode add self register transaction failed", slog.Any("error", err))
			return
		}
		node.broadcastTransaction(ctx, transaction)
		node.registeredSelf = true
		node.logger.Info("posnode register validator transaction submitted", slog.Uint64("stake", node.config.StakeLamports))
		return
	}
}

func (node *posNode) localValidatorStakeAccountExists() bool {
	account, found, err := node.ledger.Account(node.validatorKeyPair.PublicKey)
	if err != nil {
		node.logger.Warn("posnode local validator stake account check failed", slog.Any("error", err))
		return false
	}
	return found && account.Owner == structure.DefaultBuiltinProgramIDs.Stake && len(account.Data) > 0
}

func (node *posNode) autoRegisterBlockhashReady() bool {
	headSlot := node.ledger.Head().Slot
	wallSlot := node.currentWallSlot()
	if headSlot >= wallSlot {
		return true
	}
	if wallSlot-headSlot <= structure.MaxRecentBlockhashAgeSlots {
		return true
	}
	node.logger.Debug("posnode auto register waits for ledger sync",
		slog.Uint64("head_slot", headSlot),
		slog.Uint64("wall_slot", wallSlot),
		slog.Uint64("max_recent_blockhash_age_slots", structure.MaxRecentBlockhashAgeSlots),
	)
	return false
}

// currentWallSlot 计算当前墙钟 slot + 自动注册必须避免使用过期 blockhash。
func (node *posNode) currentWallSlot() uint64 {
	startedAt := node.config.genesisStartTime()
	now := time.Now()
	if !now.After(startedAt) {
		return 1
	}
	return uint64(now.Sub(startedAt)/node.config.slotDuration()) + 1
}

func (node *posNode) slotLoop(ctx context.Context) {
	startedAt := node.config.genesisStartTime()
	clock, err := consensus.NewSlotClock(startedAt, 1, node.config.slotDuration(), node.slotSkipTimeout())
	if err != nil {
		node.logger.Error("posnode slot clock failed", slog.Any("error", err))
		return
	}
	ticker := time.NewTicker(node.slotTickInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			node.onSlotTick(ctx, clock.Tick(now))
		}
	}
}

func (node *posNode) onSlotTick(ctx context.Context, tick consensus.SlotTick) {
	if !node.config.consensusEnabled() {
		return
	}
	if !tick.Started {
		return
	}
	node.mutex.Lock()
	if tick.Slot <= node.lastProducedSlot {
		node.mutex.Unlock()
		return
	}
	if tick.Slot > node.epochSnapshot.EndSlot || tick.Slot < node.epochSnapshot.StartSlot {
		if err := node.ensureEpochForSlotLocked(tick.Slot); err != nil {
			node.logger.Error("posnode rebuild epoch failed", slog.Uint64("slot", tick.Slot), slog.Any("error", err))
			node.mutex.Unlock()
			return
		}
	}
	leaderID, err := node.leaderSchedule.LeaderForSlot(tick.Slot)
	if err != nil {
		node.mutex.Unlock()
		return
	}
	isLeader := leaderID == consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	node.mutex.Unlock()
	if isLeader && tick.ShouldSkip {
		node.logger.Warn("posnode leader slot skipped after deadline",
			slog.Uint64("slot", tick.Slot),
			slog.Time("slot_deadline", tick.SlotDeadline),
			slog.Duration("elapsed", tick.Elapsed),
		)
		return
	}
	if isLeader {
		node.produceCurrentSlot(ctx, tick.Slot)
	}
}

func (node *posNode) produceCurrentSlot(ctx context.Context, slot uint64) {
	if !node.config.consensusEnabled() {
		return
	}
	now := time.Now()
	if node.slotDeadlinePassed(slot, now) {
		node.logger.Warn("posnode produce skipped after slot deadline",
			slog.Uint64("slot", slot),
			slog.Time("slot_deadline", node.slotProductionDeadline(slot)),
			slog.Time("now", now),
		)
		return
	}
	node.mutex.Lock()
	if slot <= node.lastProducedSlot {
		node.mutex.Unlock()
		return
	}
	head := node.ledger.Head()
	if slot <= head.Slot {
		node.mutex.Unlock()
		return
	}
	node.mutex.Unlock()
	if node.hasAheadValidatorPeer(ctx, head.Height, node.slotProductionDeadline(slot)) {
		node.logger.Info("posnode production paused for block sync",
			slog.Uint64("slot", slot),
			slog.Uint64("local_height", head.Height),
			slog.String("local_hash", head.BlockHash.String()),
		)
		return
	}
	now = time.Now()
	if node.slotDeadlinePassed(slot, now) {
		node.logger.Warn("posnode produce skipped after sync delay",
			slog.Uint64("slot", slot),
			slog.Time("slot_deadline", node.slotProductionDeadline(slot)),
			slog.Time("now", now),
		)
		return
	}
	node.mutex.Lock()
	if slot <= node.lastProducedSlot {
		node.mutex.Unlock()
		return
	}
	head = node.ledger.Head()
	if slot <= head.Slot {
		node.mutex.Unlock()
		return
	}
	transactions, removeTransactionIDs := node.selectMempoolTransactionsLocked(time.Now().UnixMilli(), slot)
	rewardQCs, err := node.ledger.RewardQCs(head.FinalizedHeight, consensus.MaxRewardQCsPerBlock)
	if err != nil {
		node.logger.Warn("posnode reward qc load failed", slog.Any("error", err))
		rewardQCs = nil
	}
	evidence := node.pendingEvidenceSnapshotLocked()
	request := consensus.ProduceBlockRequest{
		Slot:           slot,
		ParentSlot:     head.Slot,
		Height:         head.Height + 1,
		EpochSnapshot:  node.epochSnapshot,
		Schedule:       node.leaderSchedule,
		ParentHash:     head.BlockHash,
		PreviousQCHash: head.QCHash,
		ParentState:    node.ledger.State(),
		Transactions:   transactions,
		BlockhashQueue: node.blockhashQueue,
		LeaderKeyPair:  node.consensusKeyPair,
		RewardQCs:      rewardQCs,
		Evidence:       evidence,
		RewardConfig:   consensus.DefaultRewardConfig(),
	}
	node.mutex.Unlock()
	node.deleteMempoolTransactions(removeTransactionIDs)

	producer := consensus.BlockProducer{ChainID: node.config.ChainID, Executor: node.executor}
	proposal, nextState, err := producer.ProduceBlock(ctx, request)
	if err != nil {
		node.logger.Error("posnode produce block failed", slog.Uint64("slot", slot), slog.Any("error", err))
		return
	}
	proposalHash, err := proposal.Hash()
	if err != nil {
		node.logger.Error("posnode hash proposal failed", slog.Any("error", err))
		return
	}
	nextHead, err := node.ledger.CommitBlock(blockchain.CommitBlockRequest{Proposal: proposal, NextState: nextState})
	if err != nil {
		node.logger.Error("posnode commit produced block failed", slog.Uint64("slot", slot), slog.Any("error", err))
		return
	}
	node.removePendingEvidence(evidence)
	node.removeCommittedMempoolTransactions(proposal.Transactions)
	node.recordCommittedBlockhash(proposal.Header.Slot, proposalHash)
	node.mutex.Lock()
	node.lastProducedSlot = slot
	node.mutex.Unlock()
	node.metrics.blocksProduced.Add(1)
	node.logger.Info("posnode block proposed",
		slog.Uint64("slot", slot),
		slog.Uint64("height", nextHead.Height),
		slog.String("leader_id", string(proposal.Header.LeaderID)),
		slog.String("parent_hash", proposal.Header.ParentHash.String()),
		slog.Int("transactions", len(proposal.Transactions)),
		slog.Any("tx_ids", transactionIDsForLog(proposal.Transactions)),
		slog.Int("reward_qc_count", len(proposal.RewardQCs)),
		slog.Int("evidence_count", len(proposal.Evidence)),
		slog.Int("reward_count", len(proposal.Rewards)),
		slog.String("block_hash", proposalHash.String()),
		slog.String("qc_hash", nextHead.QCHash.String()),
	)
	node.broadcastProposal(ctx, proposal)
	node.voteForKnownProposal(ctx, proposal.Header.Slot, proposalHash)
	node.retryOrphanChildren(ctx, proposalHash)
}

func (node *posNode) handleTransactionMessage(ctx context.Context, message p2p.Message) error {
	transaction, err := decodeTransactionMessage(message)
	if err != nil {
		return err
	}
	transactionID, err := transaction.TxIDString()
	if err != nil {
		return err
	}
	node.mutex.Lock()
	_, alreadySeen := node.seenTransactions[transactionID]
	node.mutex.Unlock()
	if alreadySeen {
		node.logger.Debug("posnode transaction duplicate ignored",
			slog.String("tx_id", transactionID),
			slog.String("from_peer", message.FromPeerID),
		)
		return nil
	}
	if err := node.addTransaction(transaction); err != nil {
		return err
	}
	node.logger.Info("posnode transaction received",
		slog.String("tx_id", transactionID),
		slog.String("from_peer", message.FromPeerID),
	)
	node.broadcastTransaction(ctx, transaction)
	return nil
}

func (node *posNode) handleProposalMessage(ctx context.Context, message p2p.Message) error {
	proposal, err := decodeProposalMessage(message)
	if err != nil {
		return err
	}
	proposalHash, err := proposal.Hash()
	if err != nil {
		return err
	}
	node.logger.Info("posnode proposal received",
		slog.Uint64("slot", proposal.Header.Slot),
		slog.Uint64("height", proposal.Header.Height),
		slog.String("block_hash", proposalHash.String()),
		slog.String("parent_hash", proposal.Header.ParentHash.String()),
		slog.String("leader_id", string(proposal.Header.LeaderID)),
		slog.String("from_peer", message.FromPeerID),
		slog.Int("tx_count", len(proposal.Transactions)),
	)
	node.observeProposalForEvidence(ctx, proposal, proposalHash)
	firstSeen := node.markProposalSeen(proposalHash)
	now := time.Now()
	if node.slotDeadlinePassed(proposal.Header.Slot, now) {
		if firstSeen {
			node.metrics.proposalsRejected.Add(1)
			node.logger.Warn("posnode proposal rejected after slot deadline",
				slog.Uint64("slot", proposal.Header.Slot),
				slog.Uint64("height", proposal.Header.Height),
				slog.String("block_hash", proposalHash.String()),
				slog.String("from_peer", message.FromPeerID),
				slog.Time("slot_deadline", node.slotProductionDeadline(proposal.Header.Slot)),
				slog.Time("now", now),
			)
		}
		return nil
	}
	if err := node.syncProposalBranch(ctx, message.FromPeerID, proposal); err != nil {
		node.logger.Warn("posnode proposal branch sync failed",
			slog.String("from_peer", message.FromPeerID),
			slog.Uint64("slot", proposal.Header.Slot),
			slog.Uint64("height", proposal.Header.Height),
			slog.String("block_hash", proposalHash.String()),
			slog.Any("error", err),
		)
	}
	if err := node.voteForProposal(ctx, proposal); err != nil {
		return err
	}
	if firstSeen {
		node.forwardProposalByTurbine(ctx, proposal, proposalHash, message.FromPeerID)
	}
	return nil
}

func (node *posNode) handleVoteMessage(ctx context.Context, message p2p.Message) error {
	envelope, err := decodeVoteMessage(message)
	if err != nil {
		return err
	}
	if err := node.verifyVoteEnvelope(envelope); err != nil {
		return err
	}
	node.observeSignedVoteForEvidence(ctx, envelope)
	node.mutex.Lock()
	validatorOrder := node.epochSnapshot.ValidatorOrder()
	var qc consensus.QuorumCertificate
	var formed bool
	if len(envelope.BLSSignature) > 0 {
		qc, formed, err = node.voteCollector.AddVoteWithBLS(envelope.Vote, envelope.BLSSignature, validatorOrder)
	} else {
		qc, formed, err = node.voteCollector.AddVote(envelope.Vote)
	}
	node.mutex.Unlock()
	if err != nil {
		return err
	}
	if formed {
		if _, err := node.ledger.SaveQC(qc); err != nil {
			return err
		}
		if !node.markQCSeen(qc) {
			return nil
		}
		qcHash, err := hashQC(qc)
		if err != nil {
			return err
		}
		node.metrics.qcFormed.Add(1)
		node.logger.Info("posnode qc formed",
			slog.Uint64("slot", qc.Slot),
			slog.Uint64("height", qc.BlockHeight),
			slog.String("block_hash", qc.BlockHash.String()),
			slog.String("qc_hash", qcHash.String()),
			slog.Uint64("confirmed_stake", qc.ConfirmedStake),
			slog.Uint64("threshold_stake", qc.ThresholdStake),
			slog.Int("voter_count", len(qc.Voters)),
		)
		node.broadcastQC(ctx, qc, "")
	}
	return nil
}

func (node *posNode) handleQCMessage(ctx context.Context, message p2p.Message) error {
	envelope := qcEnvelope{}
	if err := jsonUnmarshal(message.Payload, &envelope); err != nil {
		return err
	}
	if err := node.verifyQuorumCertificate(envelope.QC); err != nil {
		return err
	}
	if node.hasQCSeen(envelope.QC) {
		return nil
	}
	head, err := node.ledger.SaveQC(envelope.QC)
	if err != nil {
		return err
	}
	if !node.markQCSeen(envelope.QC) {
		return nil
	}
	qcHash := head.QCHash
	node.metrics.qcReceived.Add(1)
	node.logger.Info("posnode qc received",
		slog.Uint64("slot", envelope.QC.Slot),
		slog.Uint64("height", envelope.QC.BlockHeight),
		slog.String("block_hash", envelope.QC.BlockHash.String()),
		slog.String("qc_hash", qcHash.String()),
		slog.Uint64("confirmed_stake", envelope.QC.ConfirmedStake),
		slog.Int("voter_count", len(envelope.QC.Voters)),
	)
	node.broadcastQC(ctx, envelope.QC, message.FromPeerID)
	return nil
}

func (node *posNode) handleEvidenceMessage(ctx context.Context, message p2p.Message) error {
	evidence, err := decodeEvidenceMessage(message)
	if err != nil {
		return err
	}
	added, err := node.addPendingSlashingEvidence(evidence)
	if err != nil {
		return err
	}
	node.metrics.evidenceReceived.Add(1)
	node.logger.Warn("posnode evidence received",
		slog.String("from_peer", message.FromPeerID),
		slog.Int("payload_bytes", len(message.Payload)),
		slog.Bool("queued", added),
	)
	if added {
		node.broadcastEvidence(ctx, evidence, message.FromPeerID)
	}
	return nil
}

func (node *posNode) voteForProposal(ctx context.Context, proposal consensus.BlockProposal) error {
	now := time.Now()
	if node.slotDeadlinePassed(proposal.Header.Slot, now) {
		node.metrics.proposalsRejected.Add(1)
		node.logger.Warn("posnode proposal vote rejected after slot deadline",
			slog.Uint64("slot", proposal.Header.Slot),
			slog.Uint64("height", proposal.Header.Height),
			slog.Time("slot_deadline", node.slotProductionDeadline(proposal.Header.Slot)),
			slog.Time("now", now),
		)
		return nil
	}
	node.mutex.Lock()
	if node.config.consensusEnabled() && proposal.Header.Slot <= node.lastVotedSlot {
		node.mutex.Unlock()
		return nil
	}
	if err := node.ensureEpochForSlotLocked(proposal.Header.Slot); err != nil {
		node.mutex.Unlock()
		return err
	}
	leader, exists := node.epochSnapshot.ValidatorByID(proposal.Header.LeaderID)
	if !exists {
		node.mutex.Unlock()
		return fmt.Errorf("posnode: proposal leader not in snapshot")
	}
	epochSnapshot := node.epochSnapshot
	leaderSchedule := node.leaderSchedule
	blockhashQueue := node.blockhashQueue
	node.mutex.Unlock()

	parentState, parentReady := node.ensureParentAvailable(ctx, proposal.Header.ParentHash)
	if !parentReady {
		node.storeOrphanProposal(proposal)
		return nil
	}
	parentSlot, err := node.parentSlotForProposal(proposal.Header.ParentHash)
	if err != nil {
		return err
	}
	request := consensus.VerifyProposalRequest{
		Proposal:       proposal,
		ParentSlot:     parentSlot,
		EpochSnapshot:  epochSnapshot,
		Schedule:       leaderSchedule,
		ParentHash:     proposal.Header.ParentHash,
		ParentState:    parentState,
		BlockhashQueue: blockhashQueue,
		Leader:         leader,
		RewardConfig:   consensus.DefaultRewardConfig(),
	}

	verifier := consensus.ProposalVerifier{ChainID: node.config.ChainID, Executor: node.executor}
	nextState, err := verifier.VerifyProposal(ctx, request)
	if err != nil {
		return err
	}
	proposalHash, err := proposal.Hash()
	if err != nil {
		return err
	}
	commitRequest := blockchain.CommitBlockRequest{Proposal: proposal, NextState: nextState}
	head := node.ledger.Head()
	acceptedProposal := true
	if proposal.Header.ParentHash == head.BlockHash && proposal.Header.Height == head.Height+1 {
		if _, err := node.ledger.CommitBlock(commitRequest); err != nil {
			return err
		}
	} else {
		if _, err := node.ledger.SaveBlockCandidate(commitRequest); err != nil {
			return err
		}
		decision, err := node.ledger.ReorganizeTo(proposalHash)
		if err != nil {
			return err
		}
		node.metrics.forkDecisions.Add(1)
		if decision.Reorganized {
			node.metrics.reorgs.Add(1)
		}
		acceptedProposal = decision.Accepted
		node.logger.Info("posnode fork decision",
			slog.Bool("accepted", decision.Accepted),
			slog.Bool("reorganized", decision.Reorganized),
			slog.String("reason", decision.Reason),
			slog.String("block_hash", proposalHash.String()),
			slog.Uint64("height", proposal.Header.Height),
			slog.Uint64("slot", proposal.Header.Slot),
			slog.String("common_ancestor_hash", decision.CommonAncestor.BlockHash.String()),
			slog.Uint64("common_ancestor_height", decision.CommonAncestor.Height),
			slog.Any("old_chain_blocks", hashesToStrings(decision.OldBlocks)),
			slog.Any("new_chain_blocks", hashesToStrings(decision.NewBlocks)),
		)
	}
	if !acceptedProposal {
		return nil
	}
	node.removeCommittedMempoolTransactions(proposal.Transactions)
	node.recordCommittedBlockhash(proposal.Header.Slot, proposalHash)
	node.metrics.proposalsAccepted.Add(1)
	node.retryOrphanChildren(ctx, proposalHash)
	if !node.config.consensusEnabled() {
		return nil
	}
	node.mutex.Lock()
	if proposal.Header.Slot <= node.lastVotedSlot {
		node.mutex.Unlock()
		return nil
	}
	node.lastVotedSlot = proposal.Header.Slot
	stakeValue := uint64(0)
	validatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	for _, validator := range node.epochSnapshot.Validators {
		if validator.ValidatorID == validatorID {
			stakeValue = validator.StakeLamports
			break
		}
	}
	node.mutex.Unlock()
	if stakeValue == 0 {
		return nil
	}
	vote := consensus.Vote{
		Type:               consensus.VoteTypeConfirm,
		Slot:               proposal.Header.Slot,
		BlockHeight:        proposal.Header.Height,
		BlockHash:          proposalHash,
		VoterID:            string(validatorID),
		Stake:              stakeValue,
		CreatedAtUnixMilli: time.Now().UnixMilli(),
	}
	node.logger.Info("posnode vote sent",
		slog.Uint64("slot", vote.Slot),
		slog.Uint64("height", vote.BlockHeight),
		slog.String("block_hash", vote.BlockHash.String()),
		slog.String("validator_id", vote.VoterID),
		slog.Uint64("stake", vote.Stake),
	)
	node.metrics.votesSent.Add(1)
	node.broadcastVote(ctx, vote)
	return node.handleLocalVote(ctx, vote)
}

func (node *posNode) voteForKnownProposal(ctx context.Context, slot uint64, blockHash structure.Hash) {
	if !node.config.consensusEnabled() {
		return
	}
	now := time.Now()
	if node.slotDeadlinePassed(slot, now) {
		node.logger.Warn("posnode leader self vote skipped after slot deadline",
			slog.Uint64("slot", slot),
			slog.String("block_hash", blockHash.String()),
			slog.Time("slot_deadline", node.slotProductionDeadline(slot)),
			slog.Time("now", now),
		)
		return
	}
	node.mutex.Lock()
	if slot <= node.lastVotedSlot {
		node.mutex.Unlock()
		return
	}
	validatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	stakeValue := uint64(0)
	for _, validator := range node.epochSnapshot.Validators {
		if validator.ValidatorID == validatorID {
			stakeValue = validator.StakeLamports
			break
		}
	}
	node.lastVotedSlot = slot
	node.mutex.Unlock()
	if stakeValue == 0 {
		return
	}
	vote := consensus.Vote{
		Type:               consensus.VoteTypeConfirm,
		Slot:               slot,
		BlockHeight:        node.ledger.Head().Height,
		BlockHash:          blockHash,
		VoterID:            string(validatorID),
		Stake:              stakeValue,
		CreatedAtUnixMilli: time.Now().UnixMilli(),
	}
	node.logger.Info("posnode leader self vote sent",
		slog.Uint64("slot", vote.Slot),
		slog.Uint64("height", vote.BlockHeight),
		slog.String("block_hash", vote.BlockHash.String()),
		slog.String("validator_id", vote.VoterID),
		slog.Uint64("stake", vote.Stake),
	)
	node.metrics.votesSent.Add(1)
	node.broadcastVote(ctx, vote)
	if err := node.handleLocalVote(ctx, vote); err != nil {
		node.logger.Warn("posnode local vote failed", slog.Any("error", err))
	}
}

func (node *posNode) handleLocalVote(ctx context.Context, vote consensus.Vote) error {
	message, err := encodeVoteMessage(vote, node.consensusKeyPair, node.blsKeyPair)
	if err != nil {
		return err
	}
	message.FromPeerID = node.peerKeyPair.peerID
	return node.handleVoteMessage(ctx, message)
}

func (node *posNode) addTransaction(transaction structure.Transaction) error {
	if err := transaction.Validate(); err != nil {
		return err
	}
	signatureValid, err := transaction.HasValidSignatures()
	if err != nil {
		node.metrics.transactionsDrop.Add(1)
		return fmt.Errorf("posnode: verify transaction signatures: %w", err)
	}
	if !signatureValid {
		node.metrics.transactionsDrop.Add(1)
		return fmt.Errorf("posnode: invalid transaction signature")
	}
	transaction, _, err = applyEstimatedTransactionFee(transaction)
	if err != nil {
		node.metrics.transactionsDrop.Add(1)
		return err
	}
	transactionID, err := transaction.TxIDString()
	if err != nil {
		return err
	}
	committed, err := node.transactionAlreadyCommitted(transactionID)
	if err != nil {
		return err
	}
	if committed {
		node.metrics.transactionsDrop.Add(1)
		return fmt.Errorf("posnode: transaction already committed")
	}
	node.mutex.Lock()
	defer node.mutex.Unlock()
	if _, exists := node.seenTransactions[transactionID]; exists {
		return nil
	}
	if len(node.mempool) >= node.config.MempoolMaxTransactions {
		node.metrics.transactionsDrop.Add(1)
		return fmt.Errorf("posnode: mempool is full")
	}
	if transaction.SubmitTime == 0 {
		transaction.SubmitTime = time.Now().UnixMilli()
	}
	if transaction.IsExpiredWithTTL(time.Now().UnixMilli(), node.config.MempoolTransactionTTLMillis) {
		node.metrics.transactionsDrop.Add(1)
		return fmt.Errorf("posnode: transaction expired")
	}
	currentSlot := uint64(0)
	if node.ledger != nil {
		currentSlot = node.ledger.Head().Slot
	}
	if !node.transactionRecentBlockhashValidLocked(transaction, currentSlot) {
		node.metrics.transactionsDrop.Add(1)
		return fmt.Errorf("posnode: recent blockhash is not valid")
	}
	if err := node.persistMempoolTransaction(transactionID, transaction); err != nil {
		return err
	}
	node.seenTransactions[transactionID] = struct{}{}
	node.mempool = append(node.mempool, transaction)
	node.sortMempoolLocked()
	node.metrics.transactionsIn.Add(1)
	node.logger.Info("posnode transaction accepted",
		slog.String("tx_id", transactionID),
		slog.Uint64("fee", transaction.Fee),
		slog.Int64("submit_time", transaction.SubmitTime),
		slog.Int("mempool_size", len(node.mempool)),
	)
	return nil
}

func (node *posNode) broadcastTransaction(ctx context.Context, transaction structure.Transaction) {
	transactionID, txIDErr := transaction.TxIDString()
	if txIDErr != nil {
		transactionID = ""
	}
	if node.host == nil {
		node.logger.Debug("posnode transaction broadcast skipped",
			slog.String("tx_id", transactionID),
			slog.String("reason", "host unavailable"),
		)
		return
	}
	if !node.config.transactionForwardEnabled() {
		node.logger.Debug("posnode transaction broadcast skipped",
			slog.String("tx_id", transactionID),
			slog.String("reason", "transaction forwarding disabled"),
		)
		return
	}
	message, err := encodeTransactionMessage(transaction)
	if err != nil {
		node.logger.Error("posnode encode transaction failed", slog.String("tx_id", transactionID), slog.Any("error", err))
		return
	}
	preferredPeerIDs, fallbackPeerIDs := node.transactionRouteTargets(ctx)
	preferredPeerIDs = uniquePeerIDs(preferredPeerIDs)
	fallbackPeerIDs = uniquePeerIDs(fallbackPeerIDs)
	var preferredError error
	if len(preferredPeerIDs) > 0 {
		preferredError = node.host.Broadcast(ctx, preferredPeerIDs, message)
	}
	var fallbackError error
	if len(fallbackPeerIDs) > 0 {
		fallbackError = node.host.Broadcast(ctx, fallbackPeerIDs, message)
	}
	if err := mergeRouteErrors(preferredError, fallbackError); err != nil {
		node.logger.Warn("posnode broadcast transaction failed",
			slog.String("tx_id", transactionID),
			slog.Int("preferred_peers", len(preferredPeerIDs)),
			slog.Int("fallback_peers", len(fallbackPeerIDs)),
			slog.Any("error", err),
		)
		return
	}
	node.logger.Debug("posnode transaction routed",
		slog.String("tx_id", transactionID),
		slog.Int("preferred_peers", len(preferredPeerIDs)),
		slog.Int("fallback_peers", len(fallbackPeerIDs)),
	)
}

func (node *posNode) broadcastProposal(ctx context.Context, proposal consensus.BlockProposal) {
	if node.host == nil {
		utils.EnsureLogger(node.logger).Debug("posnode turbine proposal broadcast skipped", slog.String("reason", "host unavailable"))
		return
	}
	proposalHash, hashErr := proposal.Hash()
	if hashErr != nil {
		proposalHash = structure.Hash{}
	}
	node.markProposalSeen(proposalHash)
	message, err := encodeProposalMessage(proposal)
	if err != nil {
		node.logger.Error("posnode encode proposal failed", slog.Any("error", err))
		return
	}
	peerIDs, position, err := node.turbineChildPeerIDs(ctx, proposal.Header.Slot, proposal.Header.LeaderID, "")
	if err != nil {
		node.logger.Warn("posnode turbine proposal route failed",
			slog.Uint64("slot", proposal.Header.Slot),
			slog.Uint64("height", proposal.Header.Height),
			slog.String("block_hash", proposalHash.String()),
			slog.Any("error", err),
		)
		return
	}
	if len(peerIDs) == 0 {
		node.logger.Debug("posnode turbine proposal has no children",
			slog.Uint64("slot", proposal.Header.Slot),
			slog.String("block_hash", proposalHash.String()),
			slog.Int("turbine_layer", position.Layer),
		)
		return
	}
	if err := node.host.Broadcast(ctx, peerIDs, message); err != nil {
		node.logger.Warn("posnode broadcast proposal failed",
			slog.Uint64("slot", proposal.Header.Slot),
			slog.Uint64("height", proposal.Header.Height),
			slog.String("block_hash", proposalHash.String()),
			slog.Int("turbine_layer", position.Layer),
			slog.Int("child_count", len(peerIDs)),
			slog.Any("error", err),
		)
		return
	}
	node.logger.Debug("posnode proposal broadcast",
		slog.Uint64("slot", proposal.Header.Slot),
		slog.Uint64("height", proposal.Header.Height),
		slog.String("block_hash", proposalHash.String()),
		slog.Int("turbine_layer", position.Layer),
		slog.Int("child_count", len(peerIDs)),
	)
}

func (node *posNode) broadcastVote(ctx context.Context, vote consensus.Vote) {
	message, err := encodeVoteMessage(vote, node.consensusKeyPair, node.blsKeyPair)
	if err != nil {
		node.logger.Error("posnode encode vote failed", slog.Any("error", err))
		return
	}
	peerIDs := node.validatorPeerIDsSnapshot(true)
	if err := node.host.Broadcast(ctx, peerIDs, message); err != nil {
		node.logger.Warn("posnode broadcast vote failed",
			slog.Uint64("slot", vote.Slot),
			slog.Uint64("height", vote.BlockHeight),
			slog.String("block_hash", vote.BlockHash.String()),
			slog.String("validator_id", vote.VoterID),
			slog.Any("error", err),
		)
		return
	}
	node.logger.Debug("posnode vote broadcast",
		slog.Uint64("slot", vote.Slot),
		slog.Uint64("height", vote.BlockHeight),
		slog.String("block_hash", vote.BlockHash.String()),
		slog.String("validator_id", vote.VoterID),
		slog.Int("peer_count", len(peerIDs)),
	)
}

func (node *posNode) verifyVoteEnvelope(envelope voteEnvelope) error {
	voteBytes, err := envelope.Vote.MarshalBinary()
	if err != nil {
		return err
	}
	if !structure.VerifyMessageSignature(envelope.PublicKey, voteBytes, envelope.Signature) {
		return fmt.Errorf("posnode: invalid vote signature")
	}
	node.mutex.Lock()
	defer node.mutex.Unlock()
	validatorID := consensus.ValidatorID(envelope.Vote.VoterID)
	validator, exists := node.epochSnapshot.ValidatorByID(validatorID)
	if !exists {
		return fmt.Errorf("posnode: vote validator not in snapshot")
	}
	if validator.ConsensusPublicKey != envelope.PublicKey {
		return fmt.Errorf("posnode: vote public key mismatch")
	}
	if len(validator.BLSPublicKey) == 0 {
		return nil
	}
	if len(envelope.BLSPublicKey) == 0 || len(envelope.BLSSignature) == 0 {
		return fmt.Errorf("posnode: missing bls vote proof")
	}
	if !bytes.Equal(validator.BLSPublicKey, envelope.BLSPublicKey) {
		return fmt.Errorf("posnode: bls public key mismatch")
	}
	if err := consensus.VerifyBLSVote(envelope.BLSPublicKey, envelope.BLSSignature, envelope.Vote); err != nil {
		return err
	}
	return nil
}

func (node *posNode) verifyQuorumCertificate(qc consensus.QuorumCertificate) error {
	if err := qc.Validate(); err != nil {
		return err
	}
	node.mutex.Lock()
	if err := node.ensureEpochForSlotLocked(qc.Slot); err != nil {
		node.mutex.Unlock()
		return err
	}
	validatorOrder := node.epochSnapshot.ValidatorOrder()
	publicKeysByValidator := node.epochSnapshot.BLSPublicKeys()
	stakeByValidator := node.epochSnapshot.StakeMap()
	blsComplete := len(node.epochSnapshot.Validators) > 0 && len(publicKeysByValidator) == len(node.epochSnapshot.Validators)
	node.mutex.Unlock()
	if qc.SignatureScheme == "" {
		if blsComplete {
			return fmt.Errorf("posnode: aggregate qc required for bls validator set")
		}
		return nil
	}
	return consensus.VerifyBLSAggregateWithStake(
		qc,
		validatorOrder,
		publicKeysByValidator,
		stakeByValidator,
		consensus.Quorum{Numerator: defaultConsensusQuorum, Denominator: 3},
	)
}

func (node *posNode) broadcastQC(ctx context.Context, qc consensus.QuorumCertificate, excludedPeerID string) {
	qcHash, hashErr := hashQC(qc)
	if hashErr != nil {
		qcHash = structure.Hash{}
	}
	message, err := encodeQCMessage(qc)
	if err != nil {
		node.logger.Error("posnode encode qc failed", slog.Any("error", err))
		return
	}
	peerIDs := node.validatorPeerIDsSnapshot(true)
	targets := make([]string, 0, len(peerIDs))
	for _, peerID := range peerIDs {
		if peerID == "" || peerID == excludedPeerID {
			continue
		}
		targets = append(targets, peerID)
	}
	if len(targets) == 0 {
		return
	}
	if err := node.host.Broadcast(ctx, targets, message); err != nil {
		node.logger.Warn("posnode broadcast qc failed",
			slog.Uint64("slot", qc.Slot),
			slog.Uint64("height", qc.BlockHeight),
			slog.String("block_hash", qc.BlockHash.String()),
			slog.String("qc_hash", qcHash.String()),
			slog.Any("error", err),
		)
		return
	}
	node.logger.Debug("posnode qc broadcast",
		slog.Uint64("slot", qc.Slot),
		slog.Uint64("height", qc.BlockHeight),
		slog.String("block_hash", qc.BlockHash.String()),
		slog.String("qc_hash", qcHash.String()),
		slog.Int("peer_count", len(targets)),
	)
}

func (node *posNode) rebuildEpoch(epochID uint64, startSlot uint64, seed structure.Hash) error {
	node.mutex.Lock()
	defer node.mutex.Unlock()
	return node.rebuildEpochLocked(epochID, startSlot, seed)
}

func (node *posNode) rebuildEpochLocked(epochID uint64, startSlot uint64, seed structure.Hash) error {
	validatorSet, err := node.ledger.ValidatorSetFromStateAtEpoch(epochID)
	if err != nil {
		return err
	}
	snapshot, err := consensus.NewEpochSnapshot(epochID, startSlot, node.config.EpochSlots, seed, validatorSet)
	if err != nil {
		return err
	}
	schedule, err := consensus.NewLeaderSchedule(snapshot)
	if err != nil {
		return err
	}
	collector, err := consensus.NewVoteCollector(snapshot.StakeMap(), consensus.Quorum{Numerator: defaultConsensusQuorum, Denominator: 3})
	if err != nil {
		return err
	}
	node.epochSnapshot = snapshot
	node.leaderSchedule = schedule
	node.voteCollector = collector
	node.logger.Info("posnode epoch ready",
		slog.Uint64("epoch", epochID),
		slog.Uint64("start_slot", startSlot),
		slog.Uint64("end_slot", snapshot.EndSlot),
		slog.Int("validators", len(snapshot.Validators)),
		slog.Uint64("total_stake", snapshot.TotalActiveStake),
	)
	return nil
}

func (node *posNode) buildRegisterTransaction() (structure.Transaction, error) {
	instruction, err := stake.NewRegisterValidatorInstructionWithBLS(node.consensusKeyPair.PublicKey, node.blsKeyPair.PublicKey, node.peerKeyPair.peerID, 0, node.config.StakeLamports)
	if err != nil {
		return structure.Transaction{}, err
	}
	data, err := instruction.MarshalBinary()
	if err != nil {
		return structure.Transaction{}, err
	}
	transaction := structure.Transaction{
		Accounts: []structure.AccountMeta{
			{PublicKey: node.stakerKeyPair.PublicKey, IsSigner: true, IsWritable: true},
			{PublicKey: node.validatorKeyPair.PublicKey, IsSigner: false, IsWritable: true},
			{PublicKey: structure.DefaultBuiltinProgramIDs.Stake, IsSigner: false, IsWritable: false},
		},
		Instructions: []structure.CompiledInstruction{{
			ProgramIDIndex: 2,
			AccountIndexes: []uint8{0, 1},
			Data:           data,
		}},
		RecentBlockhash: node.ledger.Head().BlockHash,
		SubmitTime:      time.Now().UnixMilli(),
	}
	return transaction.Sign(map[structure.PublicKey][]byte{node.stakerKeyPair.PublicKey: node.stakerKeyPair.PrivateKey})
}

func newAccount(address structure.PublicKey, lamports uint64, owner structure.PublicKey, executable bool, data []byte) structure.AddressedAccount {
	account, err := structure.NewAccount(lamports, data, owner, executable, 0)
	if err != nil {
		panic(err)
	}
	return structure.AddressedAccount{Address: address, Account: account}
}

func mustStructureKeyPair(seedText string) structure.SolanaKeyPair {
	keyPair, err := structure.KeyPairFromSeed(utils.SHA256([]byte(seedText)))
	if err != nil {
		panic(err)
	}
	return keyPair
}

func mustBLSKeyPair(seedText string) consensus.BLSKeyPair {
	keyPair, err := consensus.BLSKeyPairFromSeed(utils.SHA256([]byte(seedText)))
	if err != nil {
		panic(err)
	}
	return keyPair
}

func mustRawKeyPair(seedText string) rawKeyPair {
	keyPair, err := rawKeyPairFromSeedText(seedText)
	if err != nil {
		panic(err)
	}
	return keyPair
}

func mustHash(text string) structure.Hash {
	hash, err := structure.NewHash(utils.SHA256([]byte(text)))
	if err != nil {
		panic(err)
	}
	return hash
}

func transactionIDsForLog(transactions []structure.Transaction) []string {
	ids := make([]string, 0, len(transactions))
	for _, transaction := range transactions {
		transactionID, err := transaction.TxIDString()
		if err != nil {
			continue
		}
		ids = append(ids, transactionID)
	}
	return ids
}

func hashesToStrings(hashes []structure.Hash) []string {
	values := make([]string, len(hashes))
	for index, hash := range hashes {
		values[index] = hash.String()
	}
	return values
}
