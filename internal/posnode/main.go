package posnode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/tentechtop/ordexus-core/blockchain"
	"github.com/tentechtop/ordexus-core/consensus"
	"github.com/tentechtop/ordexus-core/database"
	"github.com/tentechtop/ordexus-core/p2p"
	bpfloader "github.com/tentechtop/ordexus-core/programs/bpfloader"
	shieldedprogram "github.com/tentechtop/ordexus-core/programs/shielded"
	"github.com/tentechtop/ordexus-core/programs/stake"
	"github.com/tentechtop/ordexus-core/programs/system"
	tokenprogram "github.com/tentechtop/ordexus-core/programs/token"
	vmprogram "github.com/tentechtop/ordexus-core/programs/vm"
	"github.com/tentechtop/ordexus-core/rpc"
	"github.com/tentechtop/ordexus-core/runtime"
	"github.com/tentechtop/ordexus-core/structure"
	"github.com/tentechtop/ordexus-core/utils"
	ovm "github.com/tentechtop/ordexus-core/vm"
)

const (
	protocolNetworkID      = "pos-localnet"
	posNodeSoftwareVersion = "posnode/1.0.0"
	defaultConsensusQuorum = 2
	defaultTimeoutQuorum   = 2
)

type posNode struct {
	mutex                            sync.Mutex
	shieldedPreflightMutex           sync.Mutex
	config                           nodeConfig
	logger                           *slog.Logger
	startedAt                        time.Time
	runtimeContext                   context.Context
	host                             *p2p.Host
	rpcServer                        *rpc.Server
	bootstrapCoordinator             *bootstrapCoordinator
	db                               database.Database
	ledger                           *blockchain.Ledger
	executor                         runtime.FixedExecutor
	peerKeyPair                      rawKeyPair
	stakerAddress                    structure.PublicKey
	stakerKeyPair                    structure.OrdexusKeyPair
	validatorKeyPair                 structure.OrdexusKeyPair
	consensusKeyPair                 structure.OrdexusKeyPair
	blsKeyPair                       consensus.BLSKeyPair
	blockhashQueue                   structure.BlockhashQueue
	mempool                          []structure.Transaction
	mempoolShieldedNullifiers        map[structure.Hash]string
	mempoolShieldedAuthorizations    map[structure.Hash]string
	mempoolShieldedTransparentDebits map[structure.PublicKey]uint64
	mempoolShieldedDebitReservations map[string]map[structure.PublicKey]uint64
	shieldedPreflightFailures        map[structure.Hash]time.Time
	seenTransactions                 map[string]struct{}
	rejectedTransactions             map[string]rejectedTransactionStatus
	seenProposals                    map[string]struct{}
	seenQCs                          map[string]uint64
	pendingEvidence                  []consensus.SlashingEvidence
	seenEvidence                     map[string]struct{}
	proposalChoices                  map[string]consensus.BlockProposal
	signedVoteChoices                map[string]consensus.SignedVote
	voteEnvelopeChoices              map[string]voteEnvelope
	orphanProposals                  map[structure.Hash][]consensus.BlockProposal
	epochSnapshot                    consensus.EpochSnapshot
	leaderSchedule                   consensus.LeaderSchedule
	voteCollector                    *consensus.VoteCollector
	voteCollectors                   map[uint64]*consensus.VoteCollector
	epochSnapshots                   map[uint64]consensus.EpochSnapshot
	leaderSchedules                  map[uint64]consensus.LeaderSchedule
	metrics                          nodeMetrics
	lastProducedSlot                 uint64
	lastVotedSlot                    uint64
	livenessGate                     livenessGateJSON
	registeredSelf                   bool
	knownPeerIDs                     []string
	bootstrapManifestApplied         bool
	validatorPairing                 *validatorPairingSession
	pairingActivationRunning         bool
	nextRPCForwardPeer               atomic.Uint64
	rpcForwardFanoutLimiter          chan struct{}
	workerGroup                      sync.WaitGroup
	doubleVoteInjected               bool
	doubleProposalInjected           bool
	localVoteSigningMutex            sync.Mutex
	acceptedQCs                      map[string]uint64
	localVoteBroadcastAt             map[string]time.Time
	prepareQCs                       map[structure.Hash]consensus.QuorumCertificate
	epochTransitionCandidates        map[uint64]*epochTransitionCandidate
	lastPreparedSlot                 uint64
	lastTimeoutVotedSlot             uint64
	latestTimeoutQC                  *consensus.QuorumCertificate
	roundSlot                        uint64
	roundParentHash                  structure.Hash
	roundStartedAt                   time.Time
	roundLockedProposalReplayAt      time.Time
	livenessQuorumReadySince         time.Time
	livenessQuorumFingerprint        string
	transactionForwardLimiter        chan struct{}
	activeRecoveryValidator          *consensus.ValidatorState
	recoverySequence                 uint64
	recoveryApplyMutex               sync.Mutex
	consensusMode                    consensus.NetworkMode
	multiValidatorActivated          bool
	multiValidatorActivationEpoch    uint64
	founderElderValidatorID          consensus.ValidatorID
	recoveryElderValidatorID         consensus.ValidatorID
	recoveryActivationSlot           uint64
	safetyCheckpoint                 consensusSafetyCheckpoint
}

type rawKeyPair struct {
	publicKey  []byte
	privateKey []byte
	peerID     string
}

type localValidatorKeyPairs struct {
	stakerAddress structure.PublicKey
	staker        structure.OrdexusKeyPair
	validator     structure.OrdexusKeyPair
	consensus     structure.OrdexusKeyPair
	bls           consensus.BLSKeyPair
}

func PeerIDFromSeed(seedText string) (string, error) {
	keyPair, err := rawKeyPairFromSeedText(seedText)
	if err != nil {
		return "", fmt.Errorf("posnode: derive peer id: %w", err)
	}
	return keyPair.peerID, nil
}

func Run(configPath string) error {
	logger, err := utils.LoggerFromEnv()
	if err != nil {
		return err
	}
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runContext(ctx, configPath, logger, false)
}

// RunContext embeds the existing node in an application. Cancellation closes
// its listeners and ledger without sending process-wide signals.
func RunContext(ctx context.Context, configPath string, logger *slog.Logger) error {
	return runContext(ctx, configPath, logger, true)
}

func runContext(parent context.Context, configPath string, logger *slog.Logger, quietPairing bool) error {
	if err := parent.Err(); err != nil {
		return err
	}
	if logger == nil {
		logger = slog.Default()
	}
	config, err := loadNodeConfig(configPath)
	if err != nil {
		return err
	}
	config.quietPairingQR = quietPairing
	config, err = prepareBootstrapJoinConfig(parent, config, logger)
	if err != nil {
		return err
	}
	node, err := newPosNode(config, logger)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	defer func() {
		cancel()
		if node.rpcServer != nil {
			shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = node.rpcServer.Shutdown(shutdownContext)
			shutdownCancel()
		}
		if node.host != nil {
			_ = node.host.Close()
		}
		node.waitForWorkers(3 * time.Second)
		if node.db != nil {
			_ = node.db.Close()
		}
	}()

	if err := node.start(ctx); err != nil {
		return err
	}
	<-ctx.Done()
	logger.Info("posnode shutdown requested")
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
		config:                           config,
		logger:                           logger,
		startedAt:                        time.Now(),
		executor:                         executor,
		peerKeyPair:                      peerKeyPair,
		stakerAddress:                    localKeys.stakerAddress,
		stakerKeyPair:                    localKeys.staker,
		validatorKeyPair:                 localKeys.validator,
		consensusKeyPair:                 localKeys.consensus,
		blsKeyPair:                       localKeys.bls,
		seenTransactions:                 make(map[string]struct{}),
		mempoolShieldedNullifiers:        make(map[structure.Hash]string),
		mempoolShieldedAuthorizations:    make(map[structure.Hash]string),
		mempoolShieldedTransparentDebits: make(map[structure.PublicKey]uint64),
		mempoolShieldedDebitReservations: make(map[string]map[structure.PublicKey]uint64),
		shieldedPreflightFailures:        make(map[structure.Hash]time.Time),
		rejectedTransactions:             make(map[string]rejectedTransactionStatus),
		seenProposals:                    make(map[string]struct{}),
		seenQCs:                          make(map[string]uint64),
		seenEvidence:                     make(map[string]struct{}),
		proposalChoices:                  make(map[string]consensus.BlockProposal),
		signedVoteChoices:                make(map[string]consensus.SignedVote),
		voteEnvelopeChoices:              make(map[string]voteEnvelope),
		orphanProposals:                  make(map[structure.Hash][]consensus.BlockProposal),
		voteCollectors:                   make(map[uint64]*consensus.VoteCollector),
		epochSnapshots:                   make(map[uint64]consensus.EpochSnapshot),
		leaderSchedules:                  make(map[uint64]consensus.LeaderSchedule),
		rpcForwardFanoutLimiter:          make(chan struct{}, transactionFanoutWorkers),
		acceptedQCs:                      make(map[string]uint64),
		localVoteBroadcastAt:             make(map[string]time.Time),
		prepareQCs:                       make(map[structure.Hash]consensus.QuorumCertificate),
		epochTransitionCandidates:        make(map[uint64]*epochTransitionCandidate),
		transactionForwardLimiter:        make(chan struct{}, transactionForwardWorkers),
	}
	if config.BootstrapCoordinator.Enabled {
		coordinator, err := newBootstrapCoordinator(config, logger)
		if err != nil {
			return nil, err
		}
		node.bootstrapCoordinator = coordinator
		if coordinator.frozen && coordinator.frozenManifest.Ready {
			if err := node.activateBootstrapManifest(context.Background(), coordinator.frozenManifest); err != nil {
				return nil, err
			}
		}
	}
	if config.bootstrapCoordinatorOnly() {
		return node, nil
	}
	if config.bootstrapPairingPending() {
		return node, nil
	}
	if err := node.openLedger(); err != nil {
		return nil, err
	}
	head := node.ledger.Head()
	if err := node.loadConsensusModeState(); err != nil {
		return nil, err
	}
	nextSlot := head.Slot
	if nextSlot != ^uint64(0) {
		nextSlot++
	}
	epochID, startSlot := node.epochForSlot(nextSlot)
	if epochID == 0 {
		// 创世 epoch 需要按链上策略初始化并持久化共识模式；它本身由创世配置提供信任根。
		if err := node.rebuildEpoch(epochID, startSlot, node.epochSeed(epochID)); err != nil {
			return nil, err
		}
	} else {
		if err := node.installAuthenticatedStartupEpoch(nextSlot); err != nil {
			return nil, err
		}
	}
	if err := node.loadPersistedLocalProposals(); err != nil {
		return nil, err
	}
	if err := node.loadPersistedLocalVoteEnvelopes(); err != nil {
		return nil, err
	}
	if err := node.loadPersistedTimeoutQC(); err != nil {
		return nil, err
	}
	return node, nil
}

func (node *posNode) start(ctx context.Context) error {
	node.runtimeContext = ctx
	if node.config.bootstrapPairingPending() {
		node.logger.Info("posnode pairing mode started",
			slog.String("node", node.config.NodeName),
			slog.String("config_path", node.config.ConfigPath),
			slog.String("peer_id", node.peerKeyPair.peerID),
			slog.String("rpc_url", validatorPairingRPCURL(node.config)),
			slog.String("bootstrap_rpc_url", node.config.BootstrapJoin.RPCURL),
		)
		if err := node.startRPC(); err != nil {
			return err
		}
		return node.startValidatorPairing()
	}
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
		slog.String("staker", node.stakerAddress.String()),
		slog.String("staker_key_source", stakerKeyMaterialSource(node.config)),
		slog.String("staker_key_location", stakerKeyMaterialLocation(node.config)),
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
	if err := node.startValidatorPairing(); err != nil {
		return err
	}
	node.replayPersistedCurrentRoundVotes(ctx)
	node.startLedgerWorkers(ctx)
	return nil
}

// startLedgerWorkers 启动链上后台任务 + 配对后热启动与普通启动共享同一套 worker 边界。
func (node *posNode) startLedgerWorkers(ctx context.Context) {
	if node.ledger == nil {
		return
	}
	node.startWorker(func() {
		node.blockSyncLoop(ctx)
	})
	node.startWorker(func() {
		node.mempoolMaintenanceLoop(ctx)
	})
	if !node.config.consensusEnabled() {
		return
	}
	node.startWorker(func() {
		node.livenessGateLoop(ctx)
	})
	node.startWorker(func() {
		node.slotLoop(ctx)
	})
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

// stakerKeyMaterialSource 标识 staker 来源 + 钱包配对后节点只保存公开地址不保存钱包私钥。
func stakerKeyMaterialSource(config nodeConfig) string {
	if !config.validatorEnabled() {
		return "disabled"
	}
	if strings.TrimSpace(config.StakerKeyPath) != "" || strings.TrimSpace(config.StakerSeed) != "" {
		return keyMaterialSource(config.StakerKeyPath, config.StakerSeed, true)
	}
	if strings.TrimSpace(config.StakerAddress) != "" {
		return "public_address"
	}
	return "missing"
}

// stakerKeyMaterialLocation 标识 staker 位置 + 只输出地址或路径，避免日志出现私钥材料。
func stakerKeyMaterialLocation(config nodeConfig) string {
	if !config.validatorEnabled() {
		return ""
	}
	if strings.TrimSpace(config.StakerKeyPath) != "" || strings.TrimSpace(config.StakerSeed) != "" {
		return keyMaterialLocation(config, config.StakerKeyPath, config.StakerSeed, true)
	}
	return strings.TrimSpace(config.StakerAddress)
}

// loadLocalValidatorKeyPairs 加载本地验证者密钥 + 公网 RPC 节点不应持有共识私钥。
func loadLocalValidatorKeyPairs(config nodeConfig) (localValidatorKeyPairs, error) {
	if !config.validatorEnabled() {
		return localValidatorKeyPairs{}, nil
	}
	stakerKeyPair, stakerAddress, err := loadLocalStakerIdentity(config)
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
		stakerAddress: stakerAddress,
		staker:        stakerKeyPair,
		validator:     validatorKeyPair,
		consensus:     consensusKeyPair,
		bls:           blsKeyPair,
	}, nil
}

// loadLocalStakerIdentity 加载质押控制者身份 + 验证者运行只需 staker 公钥，质押签名留在钱包。
func loadLocalStakerIdentity(config nodeConfig) (structure.OrdexusKeyPair, structure.PublicKey, error) {
	if strings.TrimSpace(config.StakerKeyPath) != "" || strings.TrimSpace(config.StakerSeed) != "" {
		keyPair, err := loadStructureKeyPair(config.StakerSeed, config.StakerKeyPath, config, "staker")
		if err != nil {
			return structure.OrdexusKeyPair{}, structure.PublicKey{}, err
		}
		return keyPair, keyPair.PublicKey, nil
	}
	addressText := strings.TrimSpace(config.StakerAddress)
	if addressText == "" {
		return structure.OrdexusKeyPair{}, structure.PublicKey{}, fmt.Errorf("posnode: staker address is required")
	}
	address, err := structure.PublicKeyFromBase58(addressText)
	if err != nil {
		return structure.OrdexusKeyPair{}, structure.PublicKey{}, fmt.Errorf("posnode: decode staker address: %w", err)
	}
	return structure.OrdexusKeyPair{}, address, nil
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
	if node.config.BootstrapCoordinator.Enabled {
		router = rpc.NewBootstrapRouter(node)
	}
	if !node.config.publicRPCMode() && !node.config.BootstrapCoordinator.Enabled && node.config.EmergencyRecovery.Enabled {
		if err := router.Register("submitEmergencyRecovery", func(ctx context.Context, params json.RawMessage) (any, *rpc.Error) {
			var values []string
			if err := json.Unmarshal(params, &values); err != nil || len(values) != 1 || strings.TrimSpace(values[0]) == "" {
				return nil, rpc.ErrInvalidParams
			}
			transactionID, err := node.SubmitEmergencyRecovery(ctx, values[0])
			if err != nil {
				return nil, &rpc.Error{Code: rpc.CodeInternalError, Message: "emergency recovery rejected", Data: err.Error()}
			}
			return transactionID, nil
		}); err != nil {
			return fmt.Errorf("posnode: register emergency recovery rpc: %w", err)
		}
		if err := router.Register("submitMultiValidatorActivation", func(ctx context.Context, params json.RawMessage) (any, *rpc.Error) {
			var values []string
			if err := json.Unmarshal(params, &values); err != nil || len(values) != 1 || strings.TrimSpace(values[0]) == "" {
				return nil, rpc.ErrInvalidParams
			}
			checkpointHash, err := node.SubmitMultiValidatorActivation(ctx, values[0])
			if err != nil {
				return nil, &rpc.Error{Code: rpc.CodeInternalError, Message: "multi-validator activation rejected", Data: err.Error()}
			}
			return checkpointHash, nil
		}); err != nil {
			return fmt.Errorf("posnode: register multi-validator activation rpc: %w", err)
		}
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
	if err := node.ensureStakePayoutSchedule(); err != nil {
		_ = db.Close()
		return err
	}
	head := ledger.Head()
	node.blockhashQueue, err = restoreRecentBlockhashQueue(ledger)
	if err != nil {
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
	p2pProtocol := node.config.p2pProtocol()
	listenAddress, err := utils.BuildMultiAddress(utils.MultiAddressIP4, node.config.ListenIP, p2pProtocol, node.config.ListenPort, node.peerKeyPair.peerID)
	if err != nil {
		return fmt.Errorf("posnode: build listen address: %w", err)
	}
	advertisedAddresses, err := node.advertisedP2PAddresses()
	if err != nil {
		return err
	}
	allowInsecureP2P := node.config.allowInsecureP2P()
	hostConfig := p2p.HostConfig{
		PeerID:             node.peerKeyPair.peerID,
		Role:               node.config.advertisedP2PRole(),
		Capabilities:       node.config.ResolvedNodeCapabilities,
		AllowInsecure:      allowInsecureP2P,
		Production:         node.config.Production,
		Environment:        node.config.Environment,
		PreferredProtocols: preferredP2PProtocols(p2pProtocol),
		MaxPeers:           128,
		MaxConnections:     128,
		BlockedPeerIDs:     append([]string(nil), node.config.FaultInjection.BlockedPeerIDs...),
		Logger:             node.logger,
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
		address, err := utils.BuildMultiAddress(utils.MultiAddressIP4, peerConfig.IP, peerConfig.p2pProtocol(), peerConfig.Port, peerConfig.PeerID)
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
	address, err := utils.BuildMultiAddress(utils.MultiAddressIP4, advertisedIP, node.config.p2pProtocol(), advertisedPort, node.peerKeyPair.peerID)
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
	return newRuntimeExecutorWithPolicy(bpfloader.DeploymentPolicy{}, logger)
}

func newRuntimeExecutorWithConfig(config nodeConfig, logger *slog.Logger) (runtime.FixedExecutor, error) {
	return newRuntimeExecutorWithPolicy(deploymentPolicyFromConfig(config.ContractDeploymentPolicy), logger)
}

func newRuntimeExecutorWithPolicy(deploymentPolicy bpfloader.DeploymentPolicy, logger *slog.Logger) (runtime.FixedExecutor, error) {
	executor := runtime.NewFixedExecutorWithRegistry(runtime.NewProgramHandlerRegistry())
	executor.Logger = logger
	programExecutionPolicy, err := runtime.NewDefaultProgramExecutionPolicy(structure.DefaultBuiltinProgramIDs)
	if err != nil {
		return runtime.FixedExecutor{}, fmt.Errorf("posnode: program execution policy: %w", err)
	}
	executor.ProgramExecutionPolicy = programExecutionPolicy
	if err := registerProgramsWithLoaderPolicy(&executor, deploymentPolicy); err != nil {
		return runtime.FixedExecutor{}, err
	}
	virtualMachineProgram := vmprogram.NewProgramWithInvoker(
		structure.DefaultBuiltinProgramIDs.BPFLoader,
		ovm.Runtime{},
		func(context runtime.InstructionContext) error {
			return executor.Programs.Execute(context)
		},
	)
	if err := executor.SetFallbackProgramHandler(virtualMachineProgram.Execute); err != nil {
		return runtime.FixedExecutor{}, fmt.Errorf("posnode: register vm fallback: %w", err)
	}
	return executor, nil
}

// registerPrograms 注册链上程序处理器 + 对齐 p2p 协议注册模式降低新增程序改动面。
func registerPrograms(executor *runtime.FixedExecutor) error {
	return registerProgramsWithLoaderPolicy(executor, bpfloader.DeploymentPolicy{})
}

func registerProgramsWithLoaderPolicy(executor *runtime.FixedExecutor, deploymentPolicy bpfloader.DeploymentPolicy) error {
	shieldedProgram, err := shieldedprogram.NewDefaultProgram()
	if err != nil {
		return fmt.Errorf("posnode: create shielded program: %w", err)
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
			spec:    runtime.ProgramSpec{ID: structure.DefaultBuiltinProgramIDs.Shielded, Name: "shielded"},
			handler: shieldedProgram.Execute,
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

func (node *posNode) registerProtocols() error {
	if node.ledger == nil {
		spec := p2p.ProtocolSpec{ID: p2p.ProtocolPoSStatusV1, Name: "/pos/status/1.0.0", HasResponse: true, Priority: p2p.MessagePriorityHigh, Class: p2p.ProtocolClassControl, Concurrency: p2p.ProtocolConcurrencyStateless}
		if err := node.host.RegisterResultHandler(spec, node.handleStatusRequest); err != nil {
			return fmt.Errorf("posnode: register protocol %s: %w", spec.Name, err)
		}
		return nil
	}
	specs := []p2p.ProtocolSpec{
		{ID: p2p.ProtocolPoSTransactionV1, Name: "/pos/transaction/1.0.0", Priority: p2p.MessagePriorityNormal, Class: p2p.ProtocolClassData, Concurrency: p2p.ProtocolConcurrencyStateKey, PartitionKey: transactionMessagePartitionKey},
		{ID: p2p.ProtocolPoSProposalV1, Name: "/pos/proposal/1.0.0", Priority: p2p.MessagePriorityHigh, Class: p2p.ProtocolClassData},
		{ID: p2p.ProtocolPoSVoteV1, Name: "/pos/vote/1.0.0", Priority: p2p.MessagePriorityHigh, Class: p2p.ProtocolClassData, Concurrency: p2p.ProtocolConcurrencyStateKey, PartitionKey: voteMessagePartitionKey},
		{ID: p2p.ProtocolPoSQCV1, Name: "/pos/qc/1.0.0", Priority: p2p.MessagePriorityHigh, Class: p2p.ProtocolClassData, Concurrency: p2p.ProtocolConcurrencyStateKey, PartitionKey: qcMessagePartitionKey},
		{ID: p2p.ProtocolPoSEvidenceV1, Name: "/pos/evidence/1.0.0", Priority: p2p.MessagePriorityHigh, Class: p2p.ProtocolClassData},
		{ID: p2p.ProtocolPoSEmergencyRecoveryV1, Name: "/pos/recovery/elder/1.0.0", Priority: p2p.MessagePriorityHigh, Class: p2p.ProtocolClassControl},
		{ID: p2p.ProtocolPoSMultiValidatorActivationV1, Name: "/pos/recovery/activate/1.0.0", Priority: p2p.MessagePriorityHigh, Class: p2p.ProtocolClassControl},
		{ID: p2p.ProtocolPoSEpochTransitionV1, Name: "/pos/epoch-transition/1.0.0", Priority: p2p.MessagePriorityHigh, Class: p2p.ProtocolClassControl},
	}
	handlers := []p2p.VoidProtocolHandler{
		node.handleTransactionMessage,
		node.handleProposalMessage,
		node.handleVoteMessage,
		node.handleQCMessage,
		node.handleEvidenceMessage,
		node.handleEmergencyRecoveryMessage,
		node.handleMultiValidatorActivationMessage,
		node.handleEpochTransitionMessage,
	}
	for index, spec := range specs {
		if err := node.host.RegisterVoidHandler(spec, handlers[index]); err != nil {
			return fmt.Errorf("posnode: register protocol %s: %w", spec.Name, err)
		}
	}
	resultSpecs := []p2p.ProtocolSpec{
		{ID: p2p.ProtocolPoSBlockByHashV1, Name: "/pos/sync/block-by-hash/1.0.0", HasResponse: true, Priority: p2p.MessagePriorityHigh, Class: p2p.ProtocolClassData, Concurrency: p2p.ProtocolConcurrencyStateless},
		{ID: p2p.ProtocolPoSBlockByHeightV1, Name: "/pos/sync/block-by-height/1.0.0", HasResponse: true, Priority: p2p.MessagePriorityHigh, Class: p2p.ProtocolClassData, Concurrency: p2p.ProtocolConcurrencyStateless},
		{ID: p2p.ProtocolPoSBlockQCV1, Name: "/pos/sync/block-qc/1.0.0", HasResponse: true, Priority: p2p.MessagePriorityHigh, Class: p2p.ProtocolClassData, Concurrency: p2p.ProtocolConcurrencyStateless},
		{ID: p2p.ProtocolPoSStateSnapshotV1, Name: "/pos/sync/state-snapshot/1.0.0", HasResponse: true, Priority: p2p.MessagePriorityLow, Class: p2p.ProtocolClassData, Concurrency: p2p.ProtocolConcurrencyStateless},
		{ID: p2p.ProtocolPoSStatusV1, Name: "/pos/status/1.0.0", HasResponse: true, Priority: p2p.MessagePriorityHigh, Class: p2p.ProtocolClassControl, Concurrency: p2p.ProtocolConcurrencyStateless},
		{ID: p2p.ProtocolPoSBlockLocatorV1, Name: "/pos/sync/block-locator/1.0.0", HasResponse: true, Priority: p2p.MessagePriorityHigh, Class: p2p.ProtocolClassData, Concurrency: p2p.ProtocolConcurrencyStateless},
		{ID: p2p.ProtocolPoSCommonAncestorV1, Name: "/pos/sync/common-ancestor/1.0.0", HasResponse: true, Priority: p2p.MessagePriorityHigh, Class: p2p.ProtocolClassData, Concurrency: p2p.ProtocolConcurrencyStateless},
		{ID: p2p.ProtocolPoSRPCForwardV1, Name: "/pos/rpc/forward/1.0.0", HasResponse: true, Priority: p2p.MessagePriorityNormal, Class: p2p.ProtocolClassControl, Concurrency: p2p.ProtocolConcurrencyStateless},
		{ID: p2p.ProtocolPoSEpochProofV1, Name: "/pos/sync/epoch-proof/1.0.0", HasResponse: true, Priority: p2p.MessagePriorityHigh, Class: p2p.ProtocolClassControl, Concurrency: p2p.ProtocolConcurrencyStateless},
		{ID: p2p.ProtocolPoSEpochProofBatchV1, Name: "/pos/sync/epoch-proof-batch/1.0.0", HasResponse: true, Priority: p2p.MessagePriorityHigh, Class: p2p.ProtocolClassControl, Concurrency: p2p.ProtocolConcurrencyStateless},
	}
	resultHandlers := []p2p.ResultProtocolHandler{
		node.handleBlockByHashRequest,
		node.handleBlockByHeightRequest,
		node.handleBlockQCRequest,
		node.handleStateSnapshotRequest,
		node.handleStatusRequest,
		node.handleBlockLocatorRequest,
		node.handleCommonAncestorRequest,
		node.handleRPCForwardRequest,
		node.handleEpochProofRequest,
		node.handleEpochProofBatchRequest,
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
		address, err := utils.BuildMultiAddress(utils.MultiAddressIP4, peerConfig.IP, peerConfig.p2pProtocol(), peerConfig.Port, peerConfig.PeerID)
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
		if !node.autoRegisterBlockhashReady(ctx) {
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

func (node *posNode) autoRegisterBlockhashReady(ctx context.Context) bool {
	if node.ledger == nil {
		return false
	}
	head := node.ledger.Head()
	// 加入节点必须先同步到至少一个 finalized 区块，再用当前链头构造注册交易；
	// 直接使用本地创世哈希会在长老节点上因 recent blockhash 过期而被拒绝。
	if head.Height == 0 || head.FinalizedHeight == 0 || head.BlockHash.IsZero() {
		return false
	}
	if len(node.config.BootstrapPeers) == 0 {
		return true
	}
	if node.host == nil {
		return false
	}
	for _, peer := range node.config.BootstrapPeers {
		if peer.PeerID == "" || peer.PeerID == node.peerKeyPair.peerID {
			continue
		}
		if _, connected := node.host.ConnectionState(peer.PeerID); !connected {
			continue
		}
		statusContext, cancel := context.WithTimeout(ctx, node.peerStatusTimeout())
		status, err := node.requestStatus(statusContext, peer.PeerID)
		cancel()
		if err != nil {
			continue
		}
		if status.HeadSlot <= head.Slot+structure.MaxRecentBlockhashAgeSlots {
			return true
		}
	}
	return false
}

// currentConsensusSlot 以已确认链头和最新超时证书推进轮次，不依赖创世墙钟。
func (node *posNode) currentConsensusSlot() uint64 {
	head := node.ledger.Head()
	node.mutex.Lock()
	defer node.mutex.Unlock()
	return node.currentConsensusSlotLocked(head)
}

func (node *posNode) currentConsensusSlotLocked(head blockchain.Head) uint64 {
	if head.Slot == ^uint64(0) {
		return head.Slot
	}
	if node.latestTimeoutQC != nil &&
		node.latestTimeoutQC.BlockHash == head.BlockHash &&
		head.Height != ^uint64(0) &&
		node.latestTimeoutQC.BlockHeight == head.Height+1 &&
		node.latestTimeoutQC.Slot >= head.Slot {
		if node.latestTimeoutQC.Slot == ^uint64(0) {
			return node.latestTimeoutQC.Slot
		}
		return node.latestTimeoutQC.Slot + 1
	}
	return head.Slot + 1
}

func (node *posNode) slotLoop(ctx context.Context) {
	ticker := time.NewTicker(node.slotTickInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			node.onRoundTick(ctx, now)
		}
	}
}

func (node *posNode) onRoundTick(ctx context.Context, now time.Time) {
	if !node.config.consensusEnabled() {
		return
	}
	head := node.ledger.Head()
	node.mutex.Lock()
	slot := node.currentConsensusSlotLocked(head)
	node.mutex.Unlock()
	epochReady, err := node.ensureEpochTransition(ctx, slot)
	if err != nil || !epochReady {
		node.mutex.Lock()
		node.roundStartedAt = time.Time{}
		node.mutex.Unlock()
		if err != nil {
			node.logger.Warn("posnode production paused for unauthenticated epoch",
				slog.Uint64("slot", slot),
				slog.Any("error", err),
			)
		}
		return
	}
	livenessGate := node.refreshLivenessGate(now)
	if !livenessGateAllowsProduction(livenessGate) {
		node.mutex.Lock()
		// 法定人数恢复后必须重新获得完整 leader 窗口，不能沿用网络尚未就绪时的超时计时。
		node.roundStartedAt = time.Time{}
		node.mutex.Unlock()
		return
	}
	head = node.ledger.Head()
	node.mutex.Lock()
	slot = node.currentConsensusSlotLocked(head)
	if slot > node.epochSnapshot.EndSlot || slot < node.epochSnapshot.StartSlot {
		installedEpochID := node.epochSnapshot.EpochID
		installedEndSlot := node.epochSnapshot.EndSlot
		// 链头可能在本轮完成 epoch 检查后并发推进到下一 epoch；本轮安全延后，由下一次 tick 安装证明。
		node.roundStartedAt = time.Time{}
		node.mutex.Unlock()
		node.logger.Debug("posnode round deferred for concurrent epoch transition",
			slog.Uint64("slot", slot),
			slog.Uint64("installed_epoch_id", installedEpochID),
			slog.Uint64("installed_epoch_end_slot", installedEndSlot),
		)
		return
	}
	roundStarted := node.roundSlot != slot || node.roundParentHash != head.BlockHash || node.roundStartedAt.IsZero()
	if roundStarted {
		node.roundSlot = slot
		node.roundParentHash = head.BlockHash
		node.roundStartedAt = now
		node.roundLockedProposalReplayAt = time.Time{}
	}
	leaderID, err := node.leaderSchedule.LeaderForSlot(slot)
	if err != nil {
		node.mutex.Unlock()
		return
	}
	isLeader := leaderID == consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	proposalReady := roundProposalReadyAt(
		now,
		node.roundStartedAt,
		node.config.slotDuration(),
		len(node.epochSnapshot.Validators),
	)
	timedOut := !now.Before(node.roundStartedAt.Add(node.roundLeaderTimeout(head.Slot, slot)))
	node.mutex.Unlock()
	if roundStarted {
		// 故障恢复后重放本轮已落盘的提案和投票，确保重启节点沿用原选择并重新参与 QC 聚合。
		node.replayPersistedCurrentRoundProposal(ctx)
		node.replayPersistedCurrentRoundVotes(ctx)
	}
	if isLeader && proposalReady {
		node.produceCurrentSlot(ctx, slot)
	}
	if timedOut {
		if !node.replayConfirmedProposalAfterTimeout(ctx, slot, head, now) {
			node.voteForTimeout(ctx, slot, head)
		}
	}
}

func (node *posNode) produceCurrentSlot(ctx context.Context, slot uint64) {
	if !node.config.consensusEnabled() {
		return
	}
	if !node.applyFaultInjectedProposalDelay(ctx, slot) {
		return
	}
	node.mutex.Lock()
	currentHead := node.ledger.Head()
	if slot != node.currentConsensusSlotLocked(currentHead) {
		node.mutex.Unlock()
		node.logger.Info("posnode delayed proposal cancelled after round advanced",
			slog.Uint64("slot", slot),
			slog.Uint64("current_slot", node.currentConsensusSlot()),
		)
		return
	}
	if slot <= node.lastProducedSlot {
		node.mutex.Unlock()
		return
	}
	head := node.ledger.Head()
	if slot <= head.Slot || head.Height == ^uint64(0) {
		node.mutex.Unlock()
		return
	}
	epochID := node.epochSnapshot.EpochID
	epochSnapshot := node.epochSnapshot
	node.mutex.Unlock()
	if stakeValue, active := node.localValidatorEffectiveStakeFromSnapshot(epochSnapshot); !active || stakeValue == 0 {
		node.logger.Warn("posnode production paused for inactive local validator",
			slog.Uint64("slot", slot),
			slog.Uint64("epoch_id", epochID),
		)
		return
	}
	if err := node.ensureLocalValidatorCurrentState(epochSnapshot); err != nil {
		node.logger.Warn("posnode production paused for incomplete validator state",
			slog.Uint64("slot", slot),
			slog.Uint64("epoch_id", epochID),
			slog.Any("error", err),
		)
		return
	}
	livenessGate := node.refreshLivenessGate(time.Now())
	if !livenessGateAllowsProduction(livenessGate) {
		node.logger.Warn("posnode production paused for liveness quorum",
			slog.Uint64("slot", slot),
			slog.Uint64("epoch_id", epochID),
			slog.String("state", livenessGate.State),
			slog.String("mode", livenessGate.Mode),
			slog.String("reason", livenessGate.Reason),
			slog.Uint64("reachable_stake", livenessGate.ReachableStakeLamports),
			slog.Uint64("required_stake", livenessGate.RequiredStakeLamports),
			slog.Uint64("total_stake", livenessGate.TotalActiveStakeLamports),
			slog.Int("reachable_validators", livenessGate.ReachableValidatorCount),
			slog.Int("validator_count", livenessGate.ValidatorCount),
		)
		return
	}
	if node.shouldPauseProductionForSync(ctx, head) {
		node.logger.Info("posnode production paused for block sync",
			slog.Uint64("slot", slot),
			slog.Uint64("local_height", head.Height),
			slog.String("local_hash", head.BlockHash.String()),
		)
		return
	}
	if fresh, qcHeight, err := node.productionHeadQCFresh(head); err != nil {
		node.logger.Warn("posnode production head qc check failed",
			slog.Uint64("slot", slot),
			slog.Any("error", err),
		)
		return
	} else if !fresh {
		node.logger.Warn("posnode production paused for stale head qc",
			slog.Uint64("slot", slot),
			slog.Uint64("head_height", head.Height),
			slog.Uint64("qc_height", qcHeight),
		)
		node.tryRecoverStaleHeadQC(ctx, head, qcHeight)
		return
	}
	node.mutex.Lock()
	if slot <= node.lastProducedSlot {
		node.mutex.Unlock()
		return
	}
	head = node.ledger.Head()
	if slot <= head.Slot || head.Height == ^uint64(0) {
		node.mutex.Unlock()
		return
	}
	epochID = node.epochSnapshot.EpochID
	epochSnapshot = node.epochSnapshot
	if stakeValue, active := node.localValidatorEffectiveStakeFromSnapshot(epochSnapshot); !active || stakeValue == 0 {
		node.mutex.Unlock()
		node.logger.Warn("posnode production cancelled for inactive local validator",
			slog.Uint64("slot", slot),
			slog.Uint64("epoch_id", epochID),
		)
		return
	}
	if fresh, qcHeight, err := node.productionHeadQCFreshLocked(head); err != nil {
		node.mutex.Unlock()
		node.logger.Warn("posnode production head qc recheck failed",
			slog.Uint64("slot", slot),
			slog.Any("error", err),
		)
		return
	} else if !fresh {
		node.mutex.Unlock()
		node.logger.Warn("posnode production cancelled for stale head qc",
			slog.Uint64("slot", slot),
			slog.Uint64("head_height", head.Height),
			slog.Uint64("qc_height", qcHeight),
		)
		return
	}
	livenessGate = node.livenessGate
	if !livenessGateAllowsProduction(livenessGate) {
		node.mutex.Unlock()
		node.logger.Warn("posnode production cancelled for liveness quorum",
			slog.Uint64("slot", slot),
			slog.Uint64("epoch_id", epochID),
			slog.String("state", livenessGate.State),
			slog.String("mode", livenessGate.Mode),
			slog.String("reason", livenessGate.Reason),
			slog.Uint64("reachable_stake", livenessGate.ReachableStakeLamports),
			slog.Uint64("required_stake", livenessGate.RequiredStakeLamports),
			slog.Uint64("total_stake", livenessGate.TotalActiveStakeLamports),
			slog.Int("reachable_validators", livenessGate.ReachableValidatorCount),
			slog.Int("validator_count", livenessGate.ValidatorCount),
		)
		return
	}
	var timeoutQC *consensus.QuorumCertificate
	if head.Slot != ^uint64(0) && slot > head.Slot+1 {
		if node.latestTimeoutQC == nil || slot == 0 || node.latestTimeoutQC.Slot != slot-1 ||
			node.latestTimeoutQC.BlockHeight != head.Height+1 || node.latestTimeoutQC.BlockHash != head.BlockHash {
			node.mutex.Unlock()
			node.logger.Warn("posnode production cancelled without matching timeout qc", slog.Uint64("slot", slot))
			return
		}
		copyValue := *node.latestTimeoutQC
		timeoutQC = &copyValue
	}
	transactions, removeTransactionIDs := node.selectMempoolTransactionsLocked(time.Now().UnixMilli(), slot)
	rewardQCs, err := node.ledger.RewardQCs(head.FinalizedHeight, consensus.MaxRewardQCsPerBlock)
	if err != nil {
		node.logger.Warn("posnode reward qc load failed", slog.Any("error", err))
		rewardQCs = nil
	}
	parentState := node.ledger.State()
	evidence := node.pendingEvidenceSnapshotForSlotLocked(slot, parentState)
	proofSnapshots, err := node.consensusProofSnapshotsLocked(rewardQCs, evidence, timeoutQC, epochSnapshot)
	if err != nil {
		node.mutex.Unlock()
		node.logger.Error("posnode consensus proof snapshot load failed", slog.Uint64("slot", slot), slog.Any("error", err))
		return
	}
	request := consensus.ProduceBlockRequest{
		Slot:           slot,
		ParentSlot:     head.Slot,
		Height:         head.Height + 1,
		EpochSnapshot:  epochSnapshot,
		Schedule:       node.leaderSchedule,
		ParentHash:     head.BlockHash,
		PreviousQCHash: head.QCHash,
		TimeoutQC:      timeoutQC,
		ParentState:    parentState,
		Transactions:   transactions,
		BlockhashQueue: node.blockhashQueue,
		LeaderKeyPair:  node.consensusKeyPair,
		RewardQCs:      rewardQCs,
		Evidence:       evidence,
		ProofSnapshots: proofSnapshots,
		RewardConfig:   node.config.rewardConfig(),
	}
	node.mutex.Unlock()
	node.deleteMempoolTransactions(removeTransactionIDs)

	producer := consensus.BlockProducer{ChainID: node.config.ChainID, Executor: node.executor}
	productionResult, err := producer.ProduceBlockWithReport(ctx, request)
	if err != nil {
		node.logger.Error("posnode produce block failed", slog.Uint64("slot", slot), slog.Any("error", err))
		return
	}
	proposal := productionResult.Proposal
	nextState := productionResult.State
	proposalHash, err := proposal.Hash()
	if err != nil {
		node.logger.Error("posnode hash proposal failed", slog.Any("error", err))
		return
	}
	proposal, err = node.prepareDurableLocalProposal(proposal)
	if err != nil {
		node.logger.Error("posnode persist produced proposal failed", slog.Uint64("slot", slot), slog.Any("error", err))
		return
	}
	proposalHash, err = proposal.Hash()
	if err != nil {
		node.logger.Error("posnode hash persisted proposal failed", slog.Any("error", err))
		return
	}
	if _, err := node.ledger.SaveBlockCandidate(blockchain.CommitBlockRequest{Proposal: proposal, NextState: nextState}); err != nil {
		node.logger.Error("posnode save produced candidate failed", slog.Uint64("slot", slot), slog.Any("error", err))
		return
	}
	if err := node.confirmBlockIfPrepared(ctx, proposalHash); err != nil {
		node.logger.Warn("posnode produced candidate prepare qc check failed", slog.Uint64("slot", slot), slog.Any("error", err))
	}
	node.removeRejectedMempoolTransactions(productionResult.RejectedTransactions, request.Slot)
	if err := node.promoteBlockIfCertified(ctx, proposalHash); err != nil {
		node.logger.Warn("posnode produced candidate promotion check failed",
			slog.Uint64("slot", proposal.Header.Slot),
			slog.Uint64("height", proposal.Header.Height),
			slog.String("block_hash", proposalHash.String()),
			slog.Any("error", err),
		)
	}
	node.metrics.blocksProduced.Add(1)
	node.logger.Info("posnode block proposed",
		slog.Uint64("slot", slot),
		slog.Uint64("height", proposal.Header.Height),
		slog.String("leader_id", string(proposal.Header.LeaderID)),
		slog.String("parent_hash", proposal.Header.ParentHash.String()),
		slog.Int("transactions", len(proposal.Transactions)),
		slog.Any("tx_ids", transactionIDsForLog(proposal.Transactions)),
		slog.Int("reward_qc_count", len(proposal.RewardQCs)),
		slog.Int("evidence_count", len(proposal.Evidence)),
		slog.Int("reward_count", len(proposal.Rewards)),
		slog.String("block_hash", proposalHash.String()),
		slog.String("previous_qc_hash", proposal.Header.PreviousQCHash.String()),
	)
	node.broadcastProposal(ctx, proposal)
	node.injectDoubleProposalFault(ctx, proposal)
	node.voteForKnownProposal(ctx, proposal.Header.Slot, proposal.Header.Height, proposalHash)
}

func (node *posNode) productionHeadQCFresh(head blockchain.Head) (bool, uint64, error) {
	return node.productionHeadQCFreshWithPeerState(head)
}

func (node *posNode) productionHeadQCFreshLocked(head blockchain.Head) (bool, uint64, error) {
	return node.productionHeadQCFreshWithPeerState(head)
}

func (node *posNode) productionHeadQCFreshWithPeerState(head blockchain.Head) (bool, uint64, error) {
	qc, exists, err := node.ledger.HeadQC()
	if err != nil {
		return false, 0, fmt.Errorf("posnode: load head qc: %w", err)
	}
	qcHeight := uint64(0)
	if exists {
		qcHeight = qc.BlockHeight
	}
	finalityDepth := node.ledger.FinalityDepth()
	return headQCLagAcceptable(head.Height, qcHeight, exists, finalityDepth), qcHeight, nil
}

func headQCLagAcceptable(headHeight uint64, qcHeight uint64, qcExists bool, maxAllowedLag uint64) bool {
	_ = maxAllowedLag
	if headHeight == 0 {
		return true
	}
	return qcExists && qcHeight == headHeight
}

func (node *posNode) tryRecoverStaleHeadQC(ctx context.Context, head blockchain.Head, qcHeight uint64) {
	if !node.config.consensusEnabled() || head.Height == 0 {
		return
	}
	if !node.staleHeadQCRecoveryPeerAgrees(ctx, head) {
		return
	}
	proposal, blockHash, found, err := node.ledger.BlockByHeight(head.Height)
	if err != nil {
		node.logger.Warn("posnode stale head qc recovery block load failed",
			slog.Uint64("head_height", head.Height),
			slog.Any("error", err),
		)
		return
	}
	if !found || blockHash != head.BlockHash {
		node.logger.Warn("posnode stale head qc recovery skipped for missing head block",
			slog.Uint64("head_height", head.Height),
			slog.String("head_hash", head.BlockHash.String()),
			slog.Bool("found", found),
			slog.String("block_hash", blockHash.String()),
		)
		return
	}
	if proposal.Header.Slot != head.Slot || proposal.Header.Height != head.Height {
		node.logger.Warn("posnode stale head qc recovery skipped for head mismatch",
			slog.Uint64("head_slot", head.Slot),
			slog.Uint64("proposal_slot", proposal.Header.Slot),
			slog.Uint64("head_height", head.Height),
			slog.Uint64("proposal_height", proposal.Header.Height),
		)
		return
	}
	if err := node.voteForCommittedHead(ctx, proposal, blockHash, qcHeight); err != nil {
		node.logger.Warn("posnode stale head qc recovery vote failed",
			slog.Uint64("slot", proposal.Header.Slot),
			slog.Uint64("height", proposal.Header.Height),
			slog.String("block_hash", blockHash.String()),
			slog.Any("error", err),
		)
	}
}

func (node *posNode) tryRecoverStaleHeadQCAfterPeerVote(ctx context.Context, vote consensus.Vote) {
	if !node.config.consensusEnabled() || node.ledger == nil {
		return
	}
	localValidatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	if vote.VoterID == string(localValidatorID) {
		return
	}
	head := node.ledger.Head()
	if !recoveryVoteMatchesHead(vote, head) {
		return
	}
	fresh, qcHeight, err := node.productionHeadQCFresh(head)
	if err != nil {
		node.logger.Warn("posnode stale head qc peer vote recovery check failed",
			slog.Uint64("slot", vote.Slot),
			slog.Uint64("height", vote.BlockHeight),
			slog.Any("error", err),
		)
		return
	}
	if fresh {
		return
	}
	proposal, blockHash, found, err := node.ledger.BlockByHeight(head.Height)
	if err != nil {
		node.logger.Warn("posnode stale head qc peer vote recovery block load failed",
			slog.Uint64("head_height", head.Height),
			slog.Any("error", err),
		)
		return
	}
	if !found || blockHash != head.BlockHash {
		node.logger.Warn("posnode stale head qc peer vote recovery skipped for missing head block",
			slog.Uint64("head_height", head.Height),
			slog.String("head_hash", head.BlockHash.String()),
			slog.Bool("found", found),
			slog.String("block_hash", blockHash.String()),
		)
		return
	}
	node.logger.Info("posnode stale head qc recovery triggered by peer vote",
		slog.Uint64("slot", vote.Slot),
		slog.Uint64("height", vote.BlockHeight),
		slog.String("block_hash", vote.BlockHash.String()),
		slog.String("peer_validator_id", vote.VoterID),
		slog.Uint64("qc_height", qcHeight),
	)
	if err := node.voteForCommittedHead(ctx, proposal, blockHash, qcHeight); err != nil {
		node.logger.Warn("posnode stale head qc peer vote recovery failed",
			slog.Uint64("slot", vote.Slot),
			slog.Uint64("height", vote.BlockHeight),
			slog.String("block_hash", vote.BlockHash.String()),
			slog.Any("error", err),
		)
	}
}

func recoveryVoteMatchesHead(vote consensus.Vote, head blockchain.Head) bool {
	if vote.Type != consensus.VoteTypeConfirm {
		return false
	}
	if vote.Slot != head.Slot || vote.BlockHeight != head.Height {
		return false
	}
	return vote.BlockHash == head.BlockHash
}

func (node *posNode) staleHeadQCRecoveryPeerAgrees(ctx context.Context, head blockchain.Head) bool {
	peerIDs := node.productionSyncGatePeerIDs(head.Height)
	if len(peerIDs) == 0 {
		return false
	}
	for _, peerID := range peerIDs {
		statusContext, cancel := context.WithTimeout(ctx, minPeerStatusTimeout)
		status, err := node.requestStatus(statusContext, peerID)
		cancel()
		if err != nil {
			node.logger.Debug("posnode stale head qc recovery status failed",
				slog.String("peer_id", peerID),
				slog.Uint64("head_height", head.Height),
				slog.Any("error", err),
			)
			continue
		}
		if staleHeadQCRecoveryPeerMatches(head, status) {
			return true
		}
		node.logger.Warn("posnode stale head qc recovery peer mismatch",
			slog.String("peer_id", peerID),
			slog.Uint64("local_head_height", head.Height),
			slog.String("local_head_hash", head.BlockHash.String()),
			slog.Uint64("peer_head_height", status.HeadHeight),
			slog.String("peer_head_hash", status.HeadHash),
			slog.Uint64("local_finalized_height", head.FinalizedHeight),
			slog.Uint64("peer_finalized_height", status.FinalizedHeight),
		)
	}
	return false
}

func staleHeadQCRecoveryPeerMatches(head blockchain.Head, status statusResponseEnvelope) bool {
	if !status.ConsensusEnabled || !status.ValidatorEnabled {
		return false
	}
	if status.HeadHeight != head.Height || status.HeadHash != head.BlockHash.String() {
		return false
	}
	if status.FinalizedHeight > head.FinalizedHeight {
		return false
	}
	if status.FinalizedHeight == head.FinalizedHeight && status.FinalizedHash != "" {
		return status.FinalizedHash == head.FinalizedHash.String()
	}
	return true
}

func (node *posNode) handleTransactionMessage(ctx context.Context, message p2p.Message) error {
	transaction, route, err := decodeTransactionRouteMessage(message)
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
	if err := node.addTransactionWithContext(ctx, transaction); err != nil {
		return err
	}
	node.logger.Info("posnode transaction received",
		slog.String("tx_id", transactionID),
		slog.String("from_peer", message.FromPeerID),
	)
	node.scheduleTransactionForward(transaction, route, message.FromPeerID)
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
	if err := node.ensurePeerProposalEpochProof(ctx, message.FromPeerID, proposal); err != nil {
		return fmt.Errorf("posnode: authenticate proposal epoch: %w", err)
	}
	node.observeProposalForEvidence(ctx, proposal, proposalHash)
	firstSeen := node.markProposalSeen(proposalHash)
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
	epochContextValue, err := node.epochContextForSlotLocked(envelope.Vote.Slot)
	if err != nil {
		node.mutex.Unlock()
		return err
	}
	validatorOrder := epochContextValue.Snapshot.ValidatorOrder()
	voteCollector := epochContextValue.VoteCollector
	var qc consensus.QuorumCertificate
	var formed bool
	if len(envelope.BLSSignature) > 0 {
		qc, formed, err = voteCollector.AddVoteWithBLS(envelope.Vote, envelope.BLSSignature, validatorOrder)
	} else {
		qc, formed, err = voteCollector.AddVote(envelope.Vote)
	}
	node.mutex.Unlock()
	if err != nil {
		if errors.Is(err, consensus.ErrDuplicateVote) {
			return nil
		}
		return err
	}
	if formed {
		switch qc.Type {
		case consensus.VoteTypePrepare:
			if err := node.acceptPrepareQC(ctx, qc); err != nil {
				return err
			}
		case consensus.VoteTypeConfirm:
			if _, err := node.ledger.SaveQC(qc); err != nil {
				return err
			}
			if err := node.promoteCertifiedBlock(ctx, qc); err != nil {
				return err
			}
		case consensus.VoteTypeSkip:
			if err := node.acceptTimeoutQC(qc); err != nil {
				return err
			}
		default:
			return fmt.Errorf("posnode: unsupported formed qc type %d", qc.Type)
		}
		qcHash, err := hashQC(qc)
		if err != nil {
			return err
		}
		node.markQCAccepted(qcHash, qc.Slot)
		if !node.markQCSeen(qc) {
			return nil
		}
		node.metrics.qcFormed.Add(1)
		node.logger.Info("posnode qc formed",
			slog.Uint64("vote_type", uint64(qc.Type)),
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
	if !formed && message.FromPeerID != node.peerKeyPair.peerID {
		node.tryRecoverStaleHeadQCAfterPeerVote(ctx, envelope.Vote)
	}
	if message.FromPeerID != node.peerKeyPair.peerID {
		node.forwardVote(ctx, envelope, message.FromPeerID)
	}
	return nil
}

func (node *posNode) handleQCMessage(ctx context.Context, message p2p.Message) error {
	envelope, err := unmarshalQCEnvelopeBinary(message.Payload)
	if err != nil {
		return err
	}
	qcHash, err := hashQC(envelope.QC)
	if err != nil {
		return err
	}
	receivedQCHash := qcHash
	if node.hasQCAccepted(receivedQCHash) {
		return nil
	}
	qcEpochID, _ := node.epochForSlot(envelope.QC.Slot)
	if qcEpochID > 0 {
		if err := node.syncEpochProofChainFromPeer(ctx, message.FromPeerID, qcEpochID); err != nil {
			return fmt.Errorf("posnode: sync qc epoch proof: %w", err)
		}
	}
	if err := node.verifyQuorumCertificate(envelope.QC); err != nil {
		return err
	}
	if node.hasQCSeen(envelope.QC) {
		return nil
	}
	switch envelope.QC.Type {
	case consensus.VoteTypePrepare:
		if err := node.acceptPrepareQC(ctx, envelope.QC); err != nil {
			return err
		}
	case consensus.VoteTypeConfirm:
		head, err := node.ledger.SaveQC(envelope.QC)
		if err != nil {
			return err
		}
		qcHash = head.QCHash
		if err := node.promoteCertifiedBlock(ctx, envelope.QC); err != nil {
			return err
		}
	case consensus.VoteTypeSkip:
		if err := node.acceptTimeoutQC(envelope.QC); err != nil {
			return err
		}
	default:
		return fmt.Errorf("posnode: unsupported received qc type %d", envelope.QC.Type)
	}
	node.markQCAccepted(receivedQCHash, envelope.QC.Slot)
	if !node.markQCSeen(envelope.QC) {
		return nil
	}
	node.metrics.qcReceived.Add(1)
	node.logger.Info("posnode qc received",
		slog.Uint64("vote_type", uint64(envelope.QC.Type)),
		slog.Uint64("slot", envelope.QC.Slot),
		slog.Uint64("height", envelope.QC.BlockHeight),
		slog.String("block_hash", envelope.QC.BlockHash.String()),
		slog.String("qc_hash", qcHash.String()),
		slog.Uint64("confirmed_stake", envelope.QC.ConfirmedStake),
		slog.Int("voter_count", len(envelope.QC.Voters)),
	)
	node.forwardQC(ctx, envelope, message.FromPeerID)
	return nil
}

// acceptPrepareQC 接受提案准备证书。准备证书只触发 Confirm 投票，不写入账本、不提交区块。
func (node *posNode) acceptPrepareQC(ctx context.Context, certificate consensus.QuorumCertificate) error {
	if certificate.Type != consensus.VoteTypePrepare {
		return fmt.Errorf("posnode: certificate is not a prepare qc")
	}
	if err := node.verifyQuorumCertificate(certificate); err != nil {
		return fmt.Errorf("posnode: verify prepare qc: %w", err)
	}
	node.mutex.Lock()
	if node.prepareQCs == nil {
		node.prepareQCs = make(map[structure.Hash]consensus.QuorumCertificate)
	}
	existing, exists := node.prepareQCs[certificate.BlockHash]
	if !exists || certificate.ConfirmedStake > existing.ConfirmedStake {
		node.prepareQCs[certificate.BlockHash] = certificate
	}
	if !exists {
		head := node.ledger.Head()
		if node.roundSlot == certificate.Slot &&
			node.roundParentHash == head.BlockHash &&
			certificate.BlockHeight == head.Height+1 {
			// PrepareQC 开启独立的 Confirm 阶段计时，避免 QC 恰好在提案阶段截止时到达，
			// 将诚实验证者永久切分为 ConfirmVote 与 TimeoutVote 两组。
			node.roundStartedAt = time.Now()
			node.roundLockedProposalReplayAt = time.Time{}
		}
	}
	node.mutex.Unlock()
	return node.voteForPreparedBlock(ctx, certificate)
}

func (node *posNode) confirmBlockIfPrepared(ctx context.Context, blockHash structure.Hash) error {
	node.mutex.Lock()
	certificate, exists := node.prepareQCs[blockHash]
	node.mutex.Unlock()
	if !exists {
		return nil
	}
	return node.voteForPreparedBlock(ctx, certificate)
}

// voteForPreparedBlock 只在验证过 PrepareQC 且本地已有完整提案时签署决定阶段投票。
func (node *posNode) voteForPreparedBlock(ctx context.Context, certificate consensus.QuorumCertificate) error {
	if !node.config.consensusEnabled() || node.ledger == nil {
		return nil
	}
	proposal, found, err := node.ledger.BlockByHash(certificate.BlockHash)
	if err != nil || !found {
		return err
	}
	if proposal.Header.Slot != certificate.Slot || proposal.Header.Height != certificate.BlockHeight {
		return fmt.Errorf("posnode: prepare qc proposal mismatch")
	}
	head := node.ledger.Head()
	if proposal.Header.ParentHash != head.BlockHash || proposal.Header.Height != head.Height+1 || proposal.Header.Slot <= head.Slot {
		return nil
	}

	validatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	node.mutex.Lock()
	epochContextValue, err := node.epochContextForSlotLocked(proposal.Header.Slot)
	if err != nil {
		node.mutex.Unlock()
		return err
	}
	cachedEnvelope, hasCachedEnvelope := node.cachedLocalVoteEnvelopeLocked(proposal.Header.Slot, certificate.BlockHash, validatorID)
	decisionEnvelope, hasDecision := node.cachedDecisionLocalVoteEnvelopeLocked(proposal.Header.Slot, validatorID)
	node.mutex.Unlock()
	if hasDecision {
		if hasCachedEnvelope {
			return node.sendPreparedLocalVoteEnvelope(ctx, cachedEnvelope, false)
		}
		node.logger.Debug("posnode confirm vote skipped for existing decision",
			slog.Uint64("slot", proposal.Header.Slot),
			slog.Uint64("vote_type", uint64(decisionEnvelope.Vote.Type)),
			slog.String("validator_id", string(validatorID)),
		)
		return nil
	}
	stakeValue, active := node.localValidatorEffectiveStakeFromSnapshot(epochContextValue.Snapshot)
	if !active || stakeValue == 0 {
		return nil
	}
	vote := consensus.Vote{
		Type:               consensus.VoteTypeConfirm,
		Slot:               proposal.Header.Slot,
		BlockHeight:        proposal.Header.Height,
		BlockHash:          certificate.BlockHash,
		VoterID:            string(validatorID),
		Stake:              stakeValue,
		CreatedAtUnixMilli: time.Now().UnixMilli(),
	}
	node.logger.Info("posnode confirm vote sent",
		slog.Uint64("slot", vote.Slot),
		slog.Uint64("height", vote.BlockHeight),
		slog.String("block_hash", vote.BlockHash.String()),
		slog.String("validator_id", vote.VoterID),
		slog.Uint64("stake", vote.Stake),
	)
	if err := node.sendLocalVote(ctx, vote, false); err != nil {
		// PrepareQC 与本地超时计时器可能并发到达；若超时决定已先持久化，
		// 这是正常的轮次竞争，不应把发送 QC 的诚实 peer 记为协议失败。
		node.mutex.Lock()
		_, decisionExists := node.cachedDecisionLocalVoteEnvelopeLocked(proposal.Header.Slot, validatorID)
		node.mutex.Unlock()
		if decisionExists {
			return nil
		}
		return err
	}
	node.metrics.votesSent.Add(1)
	return nil
}

// acceptTimeoutQC 接受绑定当前链头的超时证书，并将下一轮推进到证书 slot+1。
func (node *posNode) acceptTimeoutQC(certificate consensus.QuorumCertificate) error {
	if certificate.Type != consensus.VoteTypeSkip {
		return fmt.Errorf("posnode: certificate is not a timeout qc")
	}
	if err := node.verifyQuorumCertificate(certificate); err != nil {
		return fmt.Errorf("posnode: verify timeout qc: %w", err)
	}
	head := node.ledger.Head()
	if head.Height == ^uint64(0) || head.Slot == ^uint64(0) ||
		certificate.BlockHash != head.BlockHash || certificate.BlockHeight != head.Height+1 || certificate.Slot <= head.Slot {
		// 合法的旧轮次证书会在节点追块或网络恢复时重复到达。它不能推进当前链头，
		// 但也不属于发送方的协议违规，否则诚实节点会因正常网络延迟被 P2P 保护误罚。
		node.logger.Debug("posnode stale timeout qc ignored",
			slog.Uint64("certificate_slot", certificate.Slot),
			slog.Uint64("certificate_height", certificate.BlockHeight),
			slog.String("certificate_block_hash", certificate.BlockHash.String()),
			slog.Uint64("head_slot", head.Slot),
			slog.Uint64("head_height", head.Height),
			slog.String("head_block_hash", head.BlockHash.String()),
		)
		return nil
	}
	node.mutex.Lock()
	if node.latestTimeoutQC != nil && node.latestTimeoutQC.BlockHash == certificate.BlockHash && node.latestTimeoutQC.Slot >= certificate.Slot {
		node.mutex.Unlock()
		return nil
	}
	node.mutex.Unlock()
	if err := node.persistTimeoutQC(certificate); err != nil {
		return err
	}
	if node.ledger.Head().BlockHash != certificate.BlockHash {
		return nil
	}
	node.mutex.Lock()
	defer node.mutex.Unlock()
	copyValue := certificate
	node.latestTimeoutQC = &copyValue
	node.prunePrepareQCsThroughSlotLocked(certificate.Slot)
	node.roundStartedAt = time.Time{}
	return nil
}

func (node *posNode) prunePrepareQCsThroughSlotLocked(slot uint64) {
	for blockHash, certificate := range node.prepareQCs {
		if certificate.Slot <= slot {
			delete(node.prepareQCs, blockHash)
		}
	}
}

func (node *posNode) promoteBlockIfCertified(ctx context.Context, blockHash structure.Hash) error {
	qc, exists, err := node.ledger.BlockQC(blockHash)
	if err != nil || !exists {
		return err
	}
	return node.promoteCertifiedBlock(ctx, qc)
}

func (node *posNode) promoteCertifiedBlock(ctx context.Context, qc consensus.QuorumCertificate) error {
	if qc.Type != consensus.VoteTypeConfirm || qc.BlockHash.IsZero() {
		return fmt.Errorf("posnode: block promotion requires confirm qc")
	}
	if err := node.verifyQuorumCertificate(qc); err != nil {
		return fmt.Errorf("posnode: verify block qc: %w", err)
	}
	head := node.ledger.Head()
	if head.BlockHash != qc.BlockHash {
		proposal, found, err := node.ledger.BlockByHash(qc.BlockHash)
		if err != nil {
			return err
		}
		if !found {
			node.logger.Debug("posnode certified block waits for proposal",
				slog.Uint64("slot", qc.Slot),
				slog.Uint64("height", qc.BlockHeight),
				slog.String("block_hash", qc.BlockHash.String()),
			)
			return nil
		}
		if proposal.Header.Height != qc.BlockHeight {
			return fmt.Errorf("posnode: certified block height mismatch")
		}
		decision, err := node.ledger.ReorganizeTo(qc.BlockHash)
		if err != nil {
			return err
		}
		node.metrics.forkDecisions.Add(1)
		if decision.Reorganized {
			node.metrics.reorgs.Add(1)
		}
		node.logger.Info("posnode certified fork decision",
			slog.Bool("accepted", decision.Accepted),
			slog.Bool("reorganized", decision.Reorganized),
			slog.String("reason", decision.Reason),
			slog.Uint64("slot", qc.Slot),
			slog.Uint64("height", qc.BlockHeight),
			slog.String("block_hash", qc.BlockHash.String()),
			slog.String("common_ancestor_hash", decision.CommonAncestor.BlockHash.String()),
			slog.Uint64("common_ancestor_height", decision.CommonAncestor.Height),
			slog.Any("old_chain_blocks", hashesToStrings(decision.OldBlocks)),
			slog.Any("new_chain_blocks", hashesToStrings(decision.NewBlocks)),
		)
		if !decision.Accepted && node.ledger.Head().BlockHash != qc.BlockHash {
			return nil
		}
	}
	proposal, found, err := node.ledger.BlockByHash(qc.BlockHash)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	node.applyCertifiedProposalEffects(ctx, proposal, qc.BlockHash)
	return nil
}

func (node *posNode) applyCertifiedProposalEffects(ctx context.Context, proposal consensus.BlockProposal, blockHash structure.Hash) {
	node.pruneFinalizedPendingEvidence()
	node.pruneFinalizedLocalVoteJournal()
	node.pruneFinalizedLocalProposalJournal()
	node.removeCommittedMempoolTransactions(proposal.Transactions)
	node.recordCommittedBlockhash(proposal.Header.Slot, blockHash)
	node.pruneMempool(time.Now().UnixMilli(), node.mempoolCurrentSlot())
	node.mutex.Lock()
	node.prunePrepareQCsThroughSlotLocked(proposal.Header.Slot)
	node.pruneFutureEpochContextCachesLocked(proposal.Header.EpochID)
	node.mutex.Unlock()
	node.metrics.proposalsAccepted.Add(1)
	node.retryOrphanChildren(ctx, blockHash)
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
	if handled, err := node.replayCachedLocalVoteForProposal(ctx, proposal); handled {
		return err
	}
	node.mutex.Lock()
	if node.config.consensusEnabled() && proposal.Header.Slot <= node.localProposalVoteWatermarkLocked() {
		node.mutex.Unlock()
		return nil
	}
	epochContextValue, err := node.epochContextForSlotLocked(proposal.Header.Slot)
	if err != nil {
		node.mutex.Unlock()
		return err
	}
	leader, exists := epochContextValue.Snapshot.ValidatorByID(proposal.Header.LeaderID)
	if !exists {
		node.mutex.Unlock()
		return fmt.Errorf("posnode: proposal leader not in snapshot")
	}
	epochSnapshot := epochContextValue.Snapshot
	leaderSchedule := epochContextValue.Schedule
	blockhashQueue := node.blockhashQueue
	proofSnapshots, err := node.consensusProofSnapshotsLocked(proposal.RewardQCs, proposal.Evidence, proposal.TimeoutQC, epochSnapshot)
	if err != nil {
		node.mutex.Unlock()
		return err
	}
	node.mutex.Unlock()
	if proposal.TimeoutQC != nil {
		if err := node.verifyQuorumCertificate(*proposal.TimeoutQC); err != nil {
			return fmt.Errorf("posnode: verify proposal timeout qc: %w", err)
		}
	}

	parentState, parentReady := node.ensureParentAvailable(ctx, proposal.Header.ParentHash)
	if !parentReady {
		node.storeOrphanProposal(proposal)
		return nil
	}
	if err := node.verifyProposalParentCertificate(proposal); err != nil {
		if errors.Is(err, errProposalParentQCMissing) {
			node.storeOrphanProposal(proposal)
			return nil
		}
		return err
	}
	parentSlot, err := node.parentSlotForProposal(proposal.Header.ParentHash)
	if err != nil {
		return err
	}
	if node.config.consensusEnabled() && proposal.Header.Slot > parentSlot+1 && proposal.TimeoutQC == nil {
		return fmt.Errorf("posnode: proposal skipped slots without timeout qc")
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
		ProofSnapshots: proofSnapshots,
		RewardConfig:   node.config.rewardConfig(),
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
	if _, err := node.ledger.SaveBlockCandidate(commitRequest); err != nil {
		return err
	}
	if err := node.confirmBlockIfPrepared(ctx, proposalHash); err != nil {
		return err
	}
	if proposal.TimeoutQC != nil {
		if err := node.acceptTimeoutQC(*proposal.TimeoutQC); err != nil {
			return err
		}
	}
	if err := node.promoteBlockIfCertified(ctx, proposalHash); err != nil {
		node.logger.Warn("posnode candidate promotion check failed",
			slog.Uint64("slot", proposal.Header.Slot),
			slog.Uint64("height", proposal.Header.Height),
			slog.String("block_hash", proposalHash.String()),
			slog.Any("error", err),
		)
	}
	node.retryOrphanChildren(ctx, proposalHash)
	if !node.config.consensusEnabled() {
		return nil
	}
	validatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	stakeValue, active := node.localValidatorEffectiveStakeFromSnapshot(epochSnapshot)
	if !active || stakeValue == 0 {
		node.logger.Warn("posnode vote skipped for inactive local validator",
			slog.Uint64("slot", proposal.Header.Slot),
			slog.Uint64("height", proposal.Header.Height),
			slog.String("validator_id", string(validatorID)),
		)
		return nil
	}
	node.mutex.Lock()
	if proposal.Header.Slot <= node.localProposalVoteWatermarkLocked() {
		node.mutex.Unlock()
		return nil
	}
	node.mutex.Unlock()
	vote := consensus.Vote{
		Type:               consensus.VoteTypePrepare,
		Slot:               proposal.Header.Slot,
		BlockHeight:        proposal.Header.Height,
		BlockHash:          proposalHash,
		VoterID:            string(validatorID),
		Stake:              stakeValue,
		CreatedAtUnixMilli: time.Now().UnixMilli(),
	}
	node.logger.Info("posnode prepare vote sent",
		slog.Uint64("slot", vote.Slot),
		slog.Uint64("height", vote.BlockHeight),
		slog.String("block_hash", vote.BlockHash.String()),
		slog.String("validator_id", vote.VoterID),
		slog.Uint64("stake", vote.Stake),
	)
	if err := node.sendLocalVote(ctx, vote, false); err != nil {
		return err
	}
	node.metrics.votesSent.Add(1)
	return nil
}

// replayCachedLocalVoteForProposal 收到同一提案时重放原签名，帮助重启 leader 重新聚合而不产生第二次签名。
func (node *posNode) replayCachedLocalVoteForProposal(ctx context.Context, proposal consensus.BlockProposal) (bool, error) {
	if !node.config.consensusEnabled() {
		return false, nil
	}
	proposalHash, err := proposal.Hash()
	if err != nil {
		return true, err
	}
	validatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	node.mutex.Lock()
	envelope, exactChoice := node.cachedLocalPrepareVoteEnvelopeLocked(proposal.Header.Slot, proposalHash, validatorID)
	_, hasPrepareChoice := node.cachedLocalPrepareVoteEnvelopeLocked(proposal.Header.Slot, structure.Hash{}, validatorID)
	_, hasDecisionChoice := node.cachedDecisionLocalVoteEnvelopeLocked(proposal.Header.Slot, validatorID)
	node.mutex.Unlock()
	if exactChoice {
		node.logger.Info("posnode cached proposal vote replayed",
			slog.Uint64("slot", envelope.Vote.Slot),
			slog.Uint64("height", envelope.Vote.BlockHeight),
			slog.String("block_hash", envelope.Vote.BlockHash.String()),
			slog.String("validator_id", envelope.Vote.VoterID),
		)
		return true, node.sendPreparedLocalVoteEnvelope(ctx, envelope, false)
	}
	return hasPrepareChoice || hasDecisionChoice, nil
}

func (node *posNode) voteForKnownProposal(ctx context.Context, slot uint64, blockHeight uint64, blockHash structure.Hash) {
	if !node.config.consensusEnabled() {
		return
	}
	node.mutex.Lock()
	if slot <= node.localProposalVoteWatermarkLocked() {
		node.mutex.Unlock()
		return
	}
	validatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	epochContextValue, err := node.epochContextForSlotLocked(slot)
	if err != nil {
		node.mutex.Unlock()
		node.logger.Warn("posnode leader self vote epoch lookup failed",
			slog.Uint64("slot", slot),
			slog.Any("error", err),
		)
		return
	}
	epochSnapshot := epochContextValue.Snapshot
	node.mutex.Unlock()
	stakeValue, active := node.localValidatorEffectiveStakeFromSnapshot(epochSnapshot)
	if !active || stakeValue == 0 {
		node.logger.Warn("posnode leader self vote skipped for inactive local validator",
			slog.Uint64("slot", slot),
			slog.String("validator_id", string(validatorID)),
		)
		return
	}
	node.mutex.Lock()
	if slot <= node.localProposalVoteWatermarkLocked() {
		node.mutex.Unlock()
		return
	}
	node.mutex.Unlock()
	vote := consensus.Vote{
		Type:               consensus.VoteTypePrepare,
		Slot:               slot,
		BlockHeight:        blockHeight,
		BlockHash:          blockHash,
		VoterID:            string(validatorID),
		Stake:              stakeValue,
		CreatedAtUnixMilli: time.Now().UnixMilli(),
	}
	node.logger.Info("posnode leader self prepare vote sent",
		slog.Uint64("slot", vote.Slot),
		slog.Uint64("height", vote.BlockHeight),
		slog.String("block_hash", vote.BlockHash.String()),
		slog.String("validator_id", vote.VoterID),
		slog.Uint64("stake", vote.Stake),
	)
	if err := node.sendLocalVote(ctx, vote, false); err != nil {
		node.logger.Warn("posnode local vote failed", slog.Any("error", err))
		return
	}
	node.metrics.votesSent.Add(1)
}

// voteForTimeout 在当前 leader 轮次超时后投票；父块绑定防止不同分叉聚合为同一证书。
func (node *posNode) voteForTimeout(ctx context.Context, slot uint64, observedHead blockchain.Head) {
	if !node.config.consensusEnabled() || observedHead.Height == ^uint64(0) {
		return
	}
	node.mutex.Lock()
	currentHead := node.ledger.Head()
	if currentHead.BlockHash != observedHead.BlockHash || currentHead.Height != observedHead.Height ||
		slot != node.currentConsensusSlotLocked(currentHead) || slot <= node.localSignedVoteWatermarkLocked() {
		node.mutex.Unlock()
		return
	}
	epochContextValue, err := node.epochContextForSlotLocked(slot)
	if err != nil {
		node.mutex.Unlock()
		node.logger.Warn("posnode timeout vote epoch lookup failed", slog.Uint64("slot", slot), slog.Any("error", err))
		return
	}
	validatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	stakeValue, active := node.localValidatorEffectiveStakeFromSnapshot(epochContextValue.Snapshot)
	if !active || stakeValue == 0 {
		node.mutex.Unlock()
		return
	}
	node.mutex.Unlock()

	vote := consensus.Vote{
		Type:               consensus.VoteTypeSkip,
		Slot:               slot,
		BlockHeight:        observedHead.Height + 1,
		BlockHash:          observedHead.BlockHash,
		VoterID:            string(validatorID),
		Stake:              stakeValue,
		CreatedAtUnixMilli: time.Now().UnixMilli(),
	}
	node.logger.Info("posnode timeout vote sent",
		slog.Uint64("slot", slot),
		slog.Uint64("height", vote.BlockHeight),
		slog.String("parent_hash", vote.BlockHash.String()),
		slog.String("validator_id", vote.VoterID),
		slog.Uint64("stake", vote.Stake),
	)
	if err := node.sendLocalVote(ctx, vote, false); err != nil {
		node.logger.Warn("posnode timeout vote failed", slog.Uint64("slot", slot), slog.Any("error", err))
		return
	}
	node.metrics.votesSent.Add(1)
}

func (node *posNode) voteForCommittedHead(
	ctx context.Context,
	proposal consensus.BlockProposal,
	blockHash structure.Hash,
	qcHeight uint64,
) error {
	validatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	node.mutex.Lock()
	epochContextValue, err := node.epochContextForSlotLocked(proposal.Header.Slot)
	if err != nil {
		node.mutex.Unlock()
		return err
	}
	epochSnapshot := epochContextValue.Snapshot
	cachedEnvelope, hasCachedEnvelope := node.cachedLocalVoteEnvelopeLocked(proposal.Header.Slot, blockHash, validatorID)
	anyCachedEnvelope, hasAnyCachedEnvelope := node.cachedAnyLocalVoteEnvelopeLocked(proposal.Header.Slot, validatorID)
	if hasAnyCachedEnvelope && !hasCachedEnvelope {
		node.mutex.Unlock()
		node.logger.Warn("posnode stale head qc recovery vote skipped for conflicting local choice",
			slog.Uint64("slot", proposal.Header.Slot),
			slog.Uint64("height", proposal.Header.Height),
			slog.String("block_hash", blockHash.String()),
			slog.String("cached_block_hash", anyCachedEnvelope.Vote.BlockHash.String()),
			slog.String("validator_id", string(validatorID)),
			slog.Uint64("qc_height", qcHeight),
		)
		return nil
	}
	node.mutex.Unlock()

	stakeValue, active := node.localValidatorEffectiveStakeFromSnapshot(epochSnapshot)
	if !active || stakeValue == 0 {
		node.logger.Warn("posnode stale head qc recovery vote skipped for inactive local validator",
			slog.Uint64("slot", proposal.Header.Slot),
			slog.Uint64("height", proposal.Header.Height),
			slog.String("validator_id", string(validatorID)),
		)
		return nil
	}
	if hasCachedEnvelope {
		node.replayLocalVoteEnvelope(ctx, cachedEnvelope, qcHeight)
		return nil
	}

	vote := consensus.Vote{
		Type:               consensus.VoteTypeConfirm,
		Slot:               proposal.Header.Slot,
		BlockHeight:        proposal.Header.Height,
		BlockHash:          blockHash,
		VoterID:            string(validatorID),
		Stake:              stakeValue,
		CreatedAtUnixMilli: time.Now().UnixMilli(),
	}

	node.logger.Info("posnode stale head qc recovery vote sent",
		slog.Uint64("slot", vote.Slot),
		slog.Uint64("height", vote.BlockHeight),
		slog.String("block_hash", vote.BlockHash.String()),
		slog.String("validator_id", vote.VoterID),
		slog.Uint64("stake", vote.Stake),
		slog.Uint64("qc_height", qcHeight),
	)
	if err := node.sendLocalVote(ctx, vote, true); err != nil {
		return err
	}
	node.metrics.votesSent.Add(1)
	return nil
}

func (node *posNode) sendLocalVote(ctx context.Context, vote consensus.Vote, directRecovery bool) error {
	authenticatedSnapshot, err := node.authenticatedEpochSnapshotForSlot(vote.Slot)
	if err != nil {
		return err
	}
	if err := node.ensureLocalValidatorCurrentState(authenticatedSnapshot); err != nil {
		return err
	}
	node.localVoteSigningMutex.Lock()
	preparedEnvelope, exists, err := node.preparedLocalVoteEnvelope(vote)
	if err != nil {
		node.localVoteSigningMutex.Unlock()
		return err
	}
	if exists {
		node.localVoteSigningMutex.Unlock()
		return node.sendPreparedLocalVoteEnvelope(ctx, preparedEnvelope, directRecovery)
	}
	envelope, err := node.newLocalVoteEnvelope(vote)
	if err != nil {
		node.localVoteSigningMutex.Unlock()
		return err
	}
	envelope, err = node.prepareDurableLocalVoteEnvelope(envelope)
	if err != nil {
		node.localVoteSigningMutex.Unlock()
		return err
	}
	node.localVoteSigningMutex.Unlock()
	return node.sendPreparedLocalVoteEnvelope(ctx, envelope, directRecovery)
}

func (node *posNode) sendPreparedLocalVoteEnvelope(ctx context.Context, envelope voteEnvelope, directRecovery bool) error {
	retryAfter := node.localVoteBroadcastRetryInterval(envelope.Vote.Slot)
	broadcastRegular, broadcastRecovery := node.claimLocalVoteBroadcastRoutes(
		envelope,
		directRecovery,
		time.Now(),
		retryAfter,
	)
	if broadcastRegular {
		node.broadcastVoteEnvelope(ctx, envelope)
	}
	if broadcastRecovery {
		node.broadcastStaleHeadRecoveryVoteEnvelope(ctx, envelope)
	}
	if err := node.handleLocalVoteEnvelope(ctx, envelope); err != nil {
		return err
	}
	node.injectDoubleVoteFault(ctx, envelope.Vote)
	return nil
}

func (node *posNode) localVoteBroadcastRetryInterval(slot uint64) time.Duration {
	parentSlot := uint64(0)
	if node.ledger != nil {
		parentSlot = node.ledger.Head().Slot
	} else if slot > 0 {
		parentSlot = slot - 1
	}
	retryAfter := node.roundLeaderTimeout(parentSlot, slot)
	if retryAfter <= 0 {
		return time.Second
	}
	return retryAfter
}

// claimLocalVoteBroadcastRoutes 限制同一份本地签名投票在一个 Leader 超时窗口内重复扩散。
// 普通路由和落后节点恢复路由分别计时，确保后触发的恢复广播不会被普通广播吞掉。
func (node *posNode) claimLocalVoteBroadcastRoutes(
	envelope voteEnvelope,
	directRecovery bool,
	now time.Time,
	retryAfter time.Duration,
) (bool, bool) {
	validatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	if envelope.Vote.VoterID != string(validatorID) || envelope.PublicKey != node.consensusKeyPair.PublicKey {
		return true, directRecovery
	}
	if retryAfter <= 0 {
		retryAfter = time.Second
	}
	choiceKey := localVoteEnvelopeChoiceKey(envelope.Vote.Slot, validatorID, envelope.Vote.Type)
	node.mutex.Lock()
	defer node.mutex.Unlock()
	if node.localVoteBroadcastAt == nil {
		node.localVoteBroadcastAt = make(map[string]time.Time)
	}
	claim := func(route string) bool {
		key := localVoteBroadcastRouteKey(choiceKey, route)
		lastBroadcastAt, exists := node.localVoteBroadcastAt[key]
		if exists && now.Before(lastBroadcastAt.Add(retryAfter)) {
			return false
		}
		node.localVoteBroadcastAt[key] = now
		return true
	}
	broadcastRegular := claim("regular")
	broadcastRecovery := false
	if directRecovery {
		broadcastRecovery = claim("recovery")
	}
	return broadcastRegular, broadcastRecovery
}

func localVoteBroadcastRouteKey(choiceKey string, route string) string {
	return choiceKey + "/" + route
}

func (node *posNode) newLocalVoteEnvelope(vote consensus.Vote) (voteEnvelope, error) {
	envelope, err := newSignedVoteEnvelope(vote, node.consensusKeyPair, node.blsKeyPair)
	if err != nil {
		return voteEnvelope{}, err
	}
	envelope.OriginPeerID = node.peerKeyPair.peerID
	envelope.HopCount = 0
	envelope.MaxHops = defaultVoteMaxHops
	return envelope, nil
}

func (node *posNode) handleLocalVoteEnvelope(ctx context.Context, envelope voteEnvelope) error {
	message, err := encodeSignedVoteEnvelopeMessage(envelope)
	if err != nil {
		return err
	}
	message.FromPeerID = node.peerKeyPair.peerID
	return node.handleVoteMessage(ctx, message)
}

func (node *posNode) replayLocalVoteEnvelope(ctx context.Context, envelope voteEnvelope, qcHeight uint64) {
	node.logger.Info("posnode stale head qc recovery vote replayed",
		slog.Uint64("slot", envelope.Vote.Slot),
		slog.Uint64("height", envelope.Vote.BlockHeight),
		slog.String("block_hash", envelope.Vote.BlockHash.String()),
		slog.String("validator_id", envelope.Vote.VoterID),
		slog.Uint64("stake", envelope.Vote.Stake),
		slog.Uint64("qc_height", qcHeight),
	)
	node.broadcastVoteEnvelope(ctx, envelope)
	node.broadcastStaleHeadRecoveryVoteEnvelope(ctx, envelope)
	if err := node.handleLocalVoteEnvelope(ctx, envelope); err != nil && !errors.Is(err, consensus.ErrDuplicateVote) {
		node.logger.Warn("posnode replay local vote failed",
			slog.Uint64("slot", envelope.Vote.Slot),
			slog.Uint64("height", envelope.Vote.BlockHeight),
			slog.String("block_hash", envelope.Vote.BlockHash.String()),
			slog.String("validator_id", envelope.Vote.VoterID),
			slog.Any("error", err),
		)
	}
}

// rememberLocalVoteEnvelope 缓存完整本地投票信封 + 恢复重播必须复用原始签名避免重复签名。
func (node *posNode) rememberLocalVoteEnvelope(envelope voteEnvelope) bool {
	node.mutex.Lock()
	defer node.mutex.Unlock()
	return node.rememberLocalVoteEnvelopeLocked(envelope)
}

func (node *posNode) rememberLocalVoteEnvelopeLocked(envelope voteEnvelope) bool {
	validatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	if envelope.Vote.VoterID != string(validatorID) {
		return false
	}
	key := localVoteEnvelopeChoiceKey(envelope.Vote.Slot, validatorID, envelope.Vote.Type)
	if node.voteEnvelopeChoices == nil {
		node.voteEnvelopeChoices = make(map[string]voteEnvelope)
	}
	existingEnvelope, exists := node.voteEnvelopeChoices[key]
	if exists {
		return sameVoteChoice(existingEnvelope.Vote, envelope.Vote)
	}
	node.voteEnvelopeChoices[key] = cloneVoteEnvelope(envelope)
	return true
}

func (node *posNode) cachedLocalVoteEnvelopeLocked(slot uint64, blockHash structure.Hash, validatorID consensus.ValidatorID) (voteEnvelope, bool) {
	envelope, exists := node.cachedDecisionLocalVoteEnvelopeLocked(slot, validatorID)
	if !exists {
		return voteEnvelope{}, false
	}
	if envelope.Vote.Type != consensus.VoteTypeConfirm {
		return voteEnvelope{}, false
	}
	if envelope.Vote.BlockHash != blockHash {
		return voteEnvelope{}, false
	}
	return envelope, true
}

func (node *posNode) cachedLocalPrepareVoteEnvelopeLocked(slot uint64, blockHash structure.Hash, validatorID consensus.ValidatorID) (voteEnvelope, bool) {
	envelope, exists := node.cachedLocalVoteEnvelopeForPhaseLocked(slot, validatorID, consensus.VoteTypePrepare)
	if !exists || envelope.Vote.Type != consensus.VoteTypePrepare {
		return voteEnvelope{}, false
	}
	if !blockHash.IsZero() && envelope.Vote.BlockHash != blockHash {
		return voteEnvelope{}, false
	}
	return envelope, true
}

func (node *posNode) cachedDecisionLocalVoteEnvelopeLocked(slot uint64, validatorID consensus.ValidatorID) (voteEnvelope, bool) {
	return node.cachedLocalVoteEnvelopeForPhaseLocked(slot, validatorID, consensus.VoteTypeConfirm)
}

// cachedAnyLocalVoteEnvelopeLocked 保留原有调用语义，仅返回 Confirm/Skip 决定阶段的投票。
func (node *posNode) cachedAnyLocalVoteEnvelopeLocked(slot uint64, validatorID consensus.ValidatorID) (voteEnvelope, bool) {
	return node.cachedDecisionLocalVoteEnvelopeLocked(slot, validatorID)
}

func (node *posNode) cachedLocalVoteEnvelopeForPhaseLocked(slot uint64, validatorID consensus.ValidatorID, voteType consensus.VoteType) (voteEnvelope, bool) {
	if node.voteEnvelopeChoices == nil {
		return voteEnvelope{}, false
	}
	envelope, exists := node.voteEnvelopeChoices[localVoteEnvelopeChoiceKey(slot, validatorID, voteType)]
	if !exists {
		return voteEnvelope{}, false
	}
	if envelope.Vote.Slot != slot || envelope.Vote.VoterID != string(validatorID) {
		return voteEnvelope{}, false
	}
	return cloneVoteEnvelope(envelope), true
}

func localVoteEnvelopeChoiceKey(slot uint64, validatorID consensus.ValidatorID, voteType consensus.VoteType) string {
	phase := "decision"
	if voteType == consensus.VoteTypePrepare {
		phase = "prepare"
	}
	return fmt.Sprintf("vote-envelope/%d/%s/%s", slot, phase, validatorID)
}

func cloneVoteEnvelope(envelope voteEnvelope) voteEnvelope {
	clonedEnvelope := envelope
	clonedEnvelope.BLSPublicKey = append([]byte(nil), envelope.BLSPublicKey...)
	clonedEnvelope.BLSSignature = append([]byte(nil), envelope.BLSSignature...)
	return clonedEnvelope
}

func (node *posNode) localValidatorEffectiveStake(epochID uint64) (uint64, bool, error) {
	if node.ledger == nil {
		return 0, false, nil
	}
	validatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	if !node.validatorKeyPair.PublicKey.IsZero() {
		account, found, err := node.ledger.Account(node.validatorKeyPair.PublicKey)
		if err != nil {
			return 0, false, fmt.Errorf("posnode: load local validator account: %w", err)
		}
		if found {
			if account.Owner != structure.DefaultBuiltinProgramIDs.Stake || len(account.Data) == 0 {
				return 0, false, nil
			}
			stakeState, err := stake.UnmarshalValidatorStateBinary(account.Data)
			if err != nil {
				return 0, false, fmt.Errorf("posnode: decode local validator stake: %w", err)
			}
			return validatorEffectiveStakeForEpoch(stakeState, validatorID, epochID)
		}
	}
	state := node.ledger.State()
	for _, account := range state.Accounts {
		if account.Account.Owner != structure.DefaultBuiltinProgramIDs.Stake || len(account.Account.Data) == 0 {
			continue
		}
		stakeState, err := stake.UnmarshalValidatorStateBinary(account.Account.Data)
		if err != nil {
			continue
		}
		if consensus.NewValidatorID(stakeState.ConsensusPublicKey) != validatorID {
			continue
		}
		return validatorEffectiveStakeForEpoch(stakeState, validatorID, epochID)
	}
	return 0, false, nil
}

// localValidatorEffectiveStakeFromSnapshot 读取本地 epoch 权重 + 共识签名必须绑定已确定的 epoch 快照而不是 speculative head。
func (node *posNode) localValidatorEffectiveStakeFromSnapshot(snapshot consensus.EpochSnapshot) (uint64, bool) {
	validatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	validator, exists := snapshot.ValidatorByID(validatorID)
	if !exists || validator.StakeLamports == 0 {
		return 0, false
	}
	return validator.StakeLamports, true
}

func validatorEffectiveStakeForEpoch(stakeState stake.ValidatorState, validatorID consensus.ValidatorID, epochID uint64) (uint64, bool, error) {
	if consensus.NewValidatorID(stakeState.ConsensusPublicKey) != validatorID {
		return 0, false, nil
	}
	if stakeState.Status == stake.ValidatorStatusJailed {
		return 0, false, nil
	}
	effectiveStake, err := stake.EffectiveStakeAtEpoch(stakeState, epochID)
	if err != nil {
		return 0, false, fmt.Errorf("posnode: calculate local validator effective stake: %w", err)
	}
	if effectiveStake == 0 {
		return 0, false, nil
	}
	if stakeState.Status == stake.ValidatorStatusExiting && stakeState.DeactivationEpoch <= epochID {
		return 0, false, nil
	}
	return effectiveStake, true, nil
}

func (node *posNode) addTransaction(transaction structure.Transaction) error {
	return node.addTransactionWithContext(context.Background(), transaction)
}

func (node *posNode) addTransactionWithContext(ctx context.Context, transaction structure.Transaction) error {
	if err := transaction.Validate(); err != nil {
		return err
	}
	containsShieldedInstruction, err := transactionContainsShieldedInstruction(transaction)
	if err != nil {
		node.metrics.transactionsDrop.Add(1)
		return err
	}
	if err := validateTransactionResourceDemand(transaction); err != nil {
		node.metrics.transactionsDrop.Add(1)
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
	shieldedWireHash := structure.Hash{}
	if containsShieldedInstruction {
		shieldedWireHash, err = transaction.Hash()
		if err != nil {
			node.metrics.transactionsDrop.Add(1)
			return fmt.Errorf("posnode: hash shielded transaction wire bytes: %w", err)
		}
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
		node.clearTransactionTracking(transactionID)
		node.logger.Debug("posnode transaction already committed",
			slog.String("tx_id", transactionID),
		)
		return nil
	}
	if transaction.SubmitTime == 0 {
		transaction.SubmitTime = time.Now().UnixMilli()
	}
	if containsShieldedInstruction {
		node.mutex.Lock()
		if node.shieldedPreflightFailureCachedLocked(shieldedWireHash, time.Now()) {
			node.mutex.Unlock()
			node.metrics.transactionsDrop.Add(1)
			return fmt.Errorf("posnode: identical shielded transaction recently failed preflight")
		}
		if _, exists := node.seenTransactions[transactionID]; exists {
			if _, inMempool := node.mempoolTransactionByIDLocked(transactionID); inMempool {
				node.mutex.Unlock()
				return nil
			}
			delete(node.seenTransactions, transactionID)
		}
		if len(node.mempool) >= node.config.MempoolMaxTransactions {
			node.mutex.Unlock()
			node.metrics.transactionsDrop.Add(1)
			return fmt.Errorf("posnode: mempool is full")
		}
		if transaction.IsExpiredWithTTL(time.Now().UnixMilli(), node.config.MempoolTransactionTTLMillis) {
			node.mutex.Unlock()
			node.metrics.transactionsDrop.Add(1)
			return fmt.Errorf("posnode: transaction expired")
		}
		currentSlot := node.transactionValidationSlot()
		if !node.transactionRecentBlockhashValidLocked(transaction, currentSlot) {
			node.mutex.Unlock()
			node.metrics.transactionsDrop.Add(1)
			return fmt.Errorf("posnode: recent blockhash is not valid")
		}
		if err := node.checkMempoolShieldedResourcesLocked(transactionID, transaction); err != nil {
			node.mutex.Unlock()
			node.metrics.transactionsDrop.Add(1)
			return err
		}
		node.mutex.Unlock()

		if err := node.preflightShieldedMempoolTransaction(ctx, transaction); err != nil {
			var rejection shieldedPreflightRejection
			if errors.As(err, &rejection) {
				node.mutex.Lock()
				node.recordShieldedPreflightFailureLocked(shieldedWireHash, time.Now())
				node.mutex.Unlock()
			}
			node.metrics.transactionsDrop.Add(1)
			return err
		}
	}
	node.mutex.Lock()
	defer node.mutex.Unlock()
	if _, exists := node.seenTransactions[transactionID]; exists {
		if _, inMempool := node.mempoolTransactionByIDLocked(transactionID); inMempool {
			return nil
		}
		delete(node.seenTransactions, transactionID)
	}
	if len(node.mempool) >= node.config.MempoolMaxTransactions {
		node.metrics.transactionsDrop.Add(1)
		return fmt.Errorf("posnode: mempool is full")
	}
	if transaction.IsExpiredWithTTL(time.Now().UnixMilli(), node.config.MempoolTransactionTTLMillis) {
		node.metrics.transactionsDrop.Add(1)
		return fmt.Errorf("posnode: transaction expired")
	}
	currentSlot := node.transactionValidationSlot()
	if !node.transactionRecentBlockhashValidLocked(transaction, currentSlot) {
		node.metrics.transactionsDrop.Add(1)
		return fmt.Errorf("posnode: recent blockhash is not valid")
	}
	if containsShieldedInstruction {
		if node.shieldedPreflightFailureCachedLocked(shieldedWireHash, time.Now()) {
			node.metrics.transactionsDrop.Add(1)
			return fmt.Errorf("posnode: identical shielded transaction recently failed preflight")
		}
		if err := node.reserveMempoolShieldedResourcesLocked(transactionID, transaction); err != nil {
			node.metrics.transactionsDrop.Add(1)
			return err
		}
	}
	if err := node.persistMempoolTransaction(transactionID, transaction); err != nil {
		node.releaseMempoolShieldedResourcesLocked(transactionID)
		return err
	}
	if containsShieldedInstruction {
		delete(node.shieldedPreflightFailures, shieldedWireHash)
	}
	delete(node.rejectedTransactions, transactionID)
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
	node.broadcastTransactionWithRoute(ctx, transaction, transactionRouteEnvelope{
		OriginPeerID: node.peerKeyPair.peerID,
		HopCount:     0,
		MaxHops:      defaultTransactionMaxHops,
	}, "")
}

func (node *posNode) forwardTransaction(ctx context.Context, transaction structure.Transaction, route transactionRouteEnvelope, fromPeerID string) {
	nextRoute, ok := route.nextHop(node.peerKeyPair.peerID)
	if !ok {
		transactionID, _ := transaction.TxIDString()
		node.logger.Debug("posnode transaction forward skipped",
			slog.String("tx_id", transactionID),
			slog.String("reason", "max hops reached"),
			slog.Uint64("hop_count", uint64(route.HopCount)),
			slog.Uint64("max_hops", uint64(route.normalized(node.peerKeyPair.peerID).MaxHops)),
		)
		return
	}
	node.broadcastTransactionWithRoute(ctx, transaction, nextRoute, fromPeerID)
}

// scheduleTransactionForward 将已入池交易的后续扩散移出协议处理线程。
// 分区期间不可达目标的拨号不能占住 P2P 调度 worker；入池持久化仍在调用方返回前完成。
func (node *posNode) scheduleTransactionForward(transaction structure.Transaction, route transactionRouteEnvelope, fromPeerID string) {
	if node.host == nil || !node.config.transactionForwardEnabled() {
		return
	}
	if node.transactionForwardLimiter != nil {
		select {
		case node.transactionForwardLimiter <- struct{}{}:
		case <-node.runtimeDone():
			return
		default:
			node.logger.Warn("posnode transaction forward saturated",
				slog.String("from_peer", fromPeerID),
				slog.Int("limit", cap(node.transactionForwardLimiter)),
			)
			return
		}
	}
	node.startWorker(func() {
		if node.transactionForwardLimiter != nil {
			defer func() { <-node.transactionForwardLimiter }()
		}
		baseContext := node.runtimeContext
		if baseContext == nil {
			baseContext = context.Background()
		}
		forwardContext, cancel := context.WithTimeout(baseContext, transactionForwardTimeout)
		defer cancel()
		node.forwardTransaction(forwardContext, transaction, route, fromPeerID)
	})
}

func (node *posNode) runtimeDone() <-chan struct{} {
	if node.runtimeContext == nil {
		return nil
	}
	return node.runtimeContext.Done()
}

func (node *posNode) broadcastTransactionWithRoute(ctx context.Context, transaction structure.Transaction, route transactionRouteEnvelope, fromPeerID string) {
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
	route = route.normalized(node.peerKeyPair.peerID)
	message, err := encodeTransactionRouteMessage(transaction, route)
	if err != nil {
		node.logger.Error("posnode encode transaction failed", slog.String("tx_id", transactionID), slog.Any("error", err))
		return
	}
	preferredPeerIDs := make([]string, 0)
	fallbackPeerIDs := make([]string, 0)
	if route.HopCount == 0 {
		preferredPeerIDs, fallbackPeerIDs = node.transactionRouteTargets(ctx)
	} else {
		fallbackPeerIDs = node.transactionForwardTargets(ctx, fromPeerID)
	}
	preferredPeerIDs = uniquePeerIDs(preferredPeerIDs)
	fallbackPeerIDs = uniquePeerIDs(fallbackPeerIDs)
	preferredPeerIDs = removePeerIDs(preferredPeerIDs, node.peerKeyPair.peerID, fromPeerID, route.OriginPeerID)
	fallbackPeerIDs = removePeerIDs(fallbackPeerIDs, node.peerKeyPair.peerID, fromPeerID, route.OriginPeerID)
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
		slog.Uint64("hop_count", uint64(route.HopCount)),
		slog.Uint64("max_hops", uint64(route.MaxHops)),
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
	turbinePeerIDs, position, err := node.turbineChildPeerIDs(ctx, proposal.Header.Slot, proposal.Header.LeaderID, "")
	if err != nil {
		node.logger.Warn("posnode turbine proposal route failed",
			slog.Uint64("slot", proposal.Header.Slot),
			slog.Uint64("height", proposal.Header.Height),
			slog.String("block_hash", proposalHash.String()),
			slog.Any("error", err),
		)
	}
	// Proposal 是本轮唯一可投票对象。恢复期间 Turbine 的固定首层节点可能尚未完成协议注册，
	// 只投递给树子节点会让已达到 quorum 的网络在超时前无人收到提案，最终分裂为 confirm/timeout 两组。
	// Leader 同时直发当前已连接验证者；后续转发仍沿用 Turbine，兼顾首轮可靠性和迟到节点传播。
	peerIDs := uniquePeerIDs(append(turbinePeerIDs, node.connectedValidatorPeerIDsSnapshot(true)...))
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
		slog.Int("child_count", len(turbinePeerIDs)),
		slog.Int("peer_count", len(peerIDs)),
	)
}

func (node *posNode) broadcastVoteEnvelope(ctx context.Context, envelope voteEnvelope) {
	if node.host == nil {
		return
	}
	envelope = cloneVoteEnvelope(envelope)
	if envelope.OriginPeerID == "" {
		envelope.OriginPeerID = node.peerKeyPair.peerID
	}
	if envelope.MaxHops == 0 {
		envelope.MaxHops = defaultVoteMaxHops
	}
	message, err := encodeSignedVoteEnvelopeMessage(envelope)
	if err != nil {
		node.logger.Error("posnode encode vote failed", slog.Any("error", err))
		return
	}
	peerIDs := node.voteRouteTargets(ctx, envelope.Vote, "", envelope.OriginPeerID)
	if len(peerIDs) == 0 {
		return
	}
	if err := node.host.Broadcast(ctx, peerIDs, message); err != nil {
		node.logger.Warn("posnode broadcast vote failed",
			slog.Uint64("slot", envelope.Vote.Slot),
			slog.Uint64("height", envelope.Vote.BlockHeight),
			slog.String("block_hash", envelope.Vote.BlockHash.String()),
			slog.String("validator_id", envelope.Vote.VoterID),
			slog.Any("error", err),
		)
		return
	}
	node.logger.Debug("posnode vote broadcast",
		slog.Uint64("slot", envelope.Vote.Slot),
		slog.Uint64("height", envelope.Vote.BlockHeight),
		slog.String("block_hash", envelope.Vote.BlockHash.String()),
		slog.String("validator_id", envelope.Vote.VoterID),
		slog.Int("peer_count", len(peerIDs)),
	)
}

func (node *posNode) broadcastStaleHeadRecoveryVoteEnvelope(ctx context.Context, envelope voteEnvelope) {
	if node.host == nil {
		return
	}
	envelope = cloneVoteEnvelope(envelope)
	envelope.OriginPeerID = node.peerKeyPair.peerID
	envelope.HopCount = 1
	envelope.MaxHops = 1
	message, err := encodeSignedVoteEnvelopeMessage(envelope)
	if err != nil {
		node.logger.Error("posnode encode recovery vote failed", slog.Any("error", err))
		return
	}
	peerIDs := recoveryVoteTargetPeerIDs(
		node.connectedValidatorPeerIDsSnapshot(true),
		node.futureLeaderCollectorPeerIDs(envelope.Vote.Slot),
		node.peerKeyPair.peerID,
	)
	if len(peerIDs) == 0 {
		return
	}
	if err := node.host.Broadcast(ctx, peerIDs, message); err != nil {
		node.logger.Warn("posnode broadcast recovery vote failed",
			slog.Uint64("slot", envelope.Vote.Slot),
			slog.Uint64("height", envelope.Vote.BlockHeight),
			slog.String("block_hash", envelope.Vote.BlockHash.String()),
			slog.String("validator_id", envelope.Vote.VoterID),
			slog.Int("peer_count", len(peerIDs)),
			slog.Any("error", err),
		)
		return
	}
	node.logger.Info("posnode stale head qc recovery vote broadcast",
		slog.Uint64("slot", envelope.Vote.Slot),
		slog.Uint64("height", envelope.Vote.BlockHeight),
		slog.String("block_hash", envelope.Vote.BlockHash.String()),
		slog.String("validator_id", envelope.Vote.VoterID),
		slog.Int("peer_count", len(peerIDs)),
	)
}

// recoveryVoteTargetPeerIDs 优先向当前实际连通的全部验证者补发原签名投票。
// 静态未来 Leader 只在连接视图尚未建立时兜底，避免法定人数分区因收集器位于另一侧而永久停滞。
func recoveryVoteTargetPeerIDs(connectedPeerIDs []string, scheduledCollectorPeerIDs []string, excludedPeerIDs ...string) []string {
	targets := connectedPeerIDs
	if len(targets) == 0 {
		targets = scheduledCollectorPeerIDs
	}
	return removePeerIDs(uniquePeerIDs(targets), excludedPeerIDs...)
}

func (node *posNode) forwardVote(ctx context.Context, envelope voteEnvelope, fromPeerID string) {
	route, ok := voteRouteEnvelope{
		OriginPeerID: envelope.OriginPeerID,
		HopCount:     envelope.HopCount,
		MaxHops:      envelope.MaxHops,
	}.nextHop(node.peerKeyPair.peerID)
	if !ok {
		node.logger.Debug("posnode vote forward skipped",
			slog.Uint64("slot", envelope.Vote.Slot),
			slog.String("block_hash", envelope.Vote.BlockHash.String()),
			slog.String("validator_id", envelope.Vote.VoterID),
			slog.String("reason", "max hops reached"),
		)
		return
	}
	envelope.OriginPeerID = route.OriginPeerID
	envelope.HopCount = route.HopCount
	envelope.MaxHops = route.MaxHops
	message, err := encodeSignedVoteEnvelopeMessage(envelope)
	if err != nil {
		node.logger.Error("posnode encode forwarded vote failed", slog.Any("error", err))
		return
	}
	peerIDs := node.voteRouteTargets(ctx, envelope.Vote, fromPeerID, route.OriginPeerID)
	if len(peerIDs) == 0 {
		return
	}
	if err := node.host.Broadcast(ctx, peerIDs, message); err != nil {
		node.logger.Warn("posnode forward vote failed",
			slog.Uint64("slot", envelope.Vote.Slot),
			slog.String("block_hash", envelope.Vote.BlockHash.String()),
			slog.String("validator_id", envelope.Vote.VoterID),
			slog.Int("peer_count", len(peerIDs)),
			slog.Any("error", err),
		)
		return
	}
	node.logger.Debug("posnode vote forwarded",
		slog.Uint64("slot", envelope.Vote.Slot),
		slog.String("block_hash", envelope.Vote.BlockHash.String()),
		slog.String("validator_id", envelope.Vote.VoterID),
		slog.Int("peer_count", len(peerIDs)),
		slog.Uint64("hop_count", uint64(route.HopCount)),
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
	epochContextValue, err := node.epochContextForSlotLocked(envelope.Vote.Slot)
	if err != nil {
		return err
	}
	validatorID := consensus.ValidatorID(envelope.Vote.VoterID)
	validator, exists := epochContextValue.Snapshot.ValidatorByID(validatorID)
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
	node.mutex.Lock()
	epochContextValue, err := node.epochContextForSlotLocked(qc.Slot)
	if err != nil {
		node.mutex.Unlock()
		return err
	}
	snapshot := epochContextValue.Snapshot
	node.mutex.Unlock()
	quorum := consensus.Quorum{Numerator: defaultConsensusQuorum, Denominator: 3, Strict: true}
	if qc.Type == consensus.VoteTypeSkip {
		quorum = consensus.Quorum{Numerator: defaultTimeoutQuorum, Denominator: 3, Strict: true}
	}
	return consensus.VerifyQuorumCertificateForSnapshot(
		qc,
		snapshot,
		quorum,
	)
}

func (node *posNode) broadcastQC(ctx context.Context, qc consensus.QuorumCertificate, excludedPeerID string) {
	if node.host == nil {
		return
	}
	qcHash, hashErr := hashQC(qc)
	if hashErr != nil {
		qcHash = structure.Hash{}
	}
	rootValidatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	envelope := qcEnvelope{
		QC:              qc,
		RootValidatorID: string(rootValidatorID),
		OriginPeerID:    node.peerKeyPair.peerID,
		// QC 已由形成节点直接投递给全部已连接验证者；该次投递就是唯一传播跳，
		// 接收方不得再次全员转发，否则节点数增加后会产生 O(n^2) 消息风暴。
		HopCount: defaultQCMaxHops,
		MaxHops:  defaultQCMaxHops,
	}
	message, err := encodeQCEnvelopeMessage(envelope)
	if err != nil {
		node.logger.Error("posnode encode qc failed", slog.Any("error", err))
		return
	}
	// QC 是阶段转换的必要证据，直接发送给所有已连接验证者，避免树形路由在中间节点掉线时将证书分割在少数子树。
	targets := removePeerIDs(
		node.connectedValidatorPeerIDsSnapshot(true),
		node.peerKeyPair.peerID,
		excludedPeerID,
	)
	if len(targets) == 0 {
		return
	}
	if err := node.host.Broadcast(ctx, targets, message); err != nil {
		node.logger.Warn("posnode broadcast qc failed",
			slog.Uint64("slot", qc.Slot),
			slog.Uint64("height", qc.BlockHeight),
			slog.String("block_hash", qc.BlockHash.String()),
			slog.String("qc_hash", qcHash.String()),
			slog.String("root_validator_id", string(rootValidatorID)),
			slog.Any("error", err),
		)
		return
	}
	node.logger.Debug("posnode qc broadcast",
		slog.Uint64("slot", qc.Slot),
		slog.Uint64("height", qc.BlockHeight),
		slog.String("block_hash", qc.BlockHash.String()),
		slog.String("qc_hash", qcHash.String()),
		slog.String("root_validator_id", string(rootValidatorID)),
		slog.Int("peer_count", len(targets)),
	)
}

func (node *posNode) forwardQC(ctx context.Context, envelope qcEnvelope, fromPeerID string) {
	if node.host == nil {
		return
	}
	if envelope.MaxHops == 0 {
		envelope.MaxHops = defaultQCMaxHops
	}
	if envelope.OriginPeerID == "" {
		envelope.OriginPeerID = fromPeerID
	}
	if envelope.HopCount >= envelope.MaxHops {
		node.logger.Debug("posnode qc forward skipped",
			slog.Uint64("slot", envelope.QC.Slot),
			slog.String("block_hash", envelope.QC.BlockHash.String()),
			slog.String("reason", "max hops reached"),
		)
		return
	}
	rootValidatorID, ok := node.qcRootValidatorID(envelope, fromPeerID)
	if !ok {
		node.logger.Warn("posnode qc forward skipped",
			slog.Uint64("slot", envelope.QC.Slot),
			slog.String("block_hash", envelope.QC.BlockHash.String()),
			slog.String("reason", "root validator unavailable"),
		)
		return
	}
	envelope.RootValidatorID = string(rootValidatorID)
	envelope.HopCount++
	message, err := encodeQCEnvelopeMessage(envelope)
	if err != nil {
		node.logger.Error("posnode encode forwarded qc failed", slog.Any("error", err))
		return
	}
	qcHash, hashErr := hashQC(envelope.QC)
	if hashErr != nil {
		qcHash = structure.Hash{}
	}
	targets := removePeerIDs(
		node.connectedValidatorPeerIDsSnapshot(true),
		node.peerKeyPair.peerID,
		fromPeerID,
		envelope.OriginPeerID,
	)
	if len(targets) == 0 {
		return
	}
	if err := node.host.Broadcast(ctx, targets, message); err != nil {
		node.logger.Warn("posnode forward qc failed",
			slog.Uint64("slot", envelope.QC.Slot),
			slog.String("block_hash", envelope.QC.BlockHash.String()),
			slog.String("qc_hash", qcHash.String()),
			slog.String("root_validator_id", string(rootValidatorID)),
			slog.Int("peer_count", len(targets)),
			slog.Any("error", err),
		)
		return
	}
	node.logger.Debug("posnode qc forwarded",
		slog.Uint64("slot", envelope.QC.Slot),
		slog.String("block_hash", envelope.QC.BlockHash.String()),
		slog.String("qc_hash", qcHash.String()),
		slog.String("root_validator_id", string(rootValidatorID)),
		slog.Int("peer_count", len(targets)),
		slog.Uint64("hop_count", uint64(envelope.HopCount)),
	)
}

func (node *posNode) qcRootValidatorID(envelope qcEnvelope, fromPeerID string) (consensus.ValidatorID, bool) {
	if envelope.RootValidatorID != "" {
		return consensus.ValidatorID(envelope.RootValidatorID), true
	}
	if validatorID, exists := node.validatorIDByPeerID(fromPeerID); exists {
		return validatorID, true
	}
	_, leaderID, found := node.leaderPeerForSlot(envelope.QC.Slot)
	return leaderID, found
}

func (node *posNode) rebuildEpoch(epochID uint64, startSlot uint64, seed structure.Hash) error {
	node.mutex.Lock()
	defer node.mutex.Unlock()
	return node.rebuildEpochLocked(epochID, startSlot, seed)
}

func (node *posNode) rebuildEpochLocked(epochID uint64, startSlot uint64, seed structure.Hash) error {
	snapshot, persisted, err := node.loadEpochSnapshot(epochID, startSlot, node.config.EpochSlots, seed)
	if err != nil {
		return err
	}
	if !persisted {
		snapshot, err = node.buildEpochSnapshotLocked(epochID, startSlot, seed)
		if err != nil {
			return err
		}
		if err := node.persistEpochSnapshot(snapshot); err != nil {
			return err
		}
	}
	return node.installEpochSnapshotLocked(snapshot)
}

// replaceEpochLocked 仅在法定签名证书已经验证后重建当前 epoch，用于显式共识模式交接。
func (node *posNode) replaceEpochLocked(epochID uint64, startSlot uint64, seed structure.Hash) error {
	snapshot, err := node.buildEpochSnapshotLocked(epochID, startSlot, seed)
	if err != nil {
		return err
	}
	if err := node.replaceEpochSnapshot(snapshot); err != nil {
		return err
	}
	return node.installEpochSnapshotLocked(snapshot)
}

func (node *posNode) buildEpochSnapshotLocked(epochID uint64, startSlot uint64, seed structure.Hash) (consensus.EpochSnapshot, error) {
	validatorSet, err := node.ledger.ValidatorSetFromFinalizedStateAtEpoch(epochID)
	if err != nil {
		return consensus.EpochSnapshot{}, err
	}
	validatorSet, err = node.effectiveValidatorSetLocked(validatorSet, epochID, seed)
	if err != nil {
		return consensus.EpochSnapshot{}, err
	}
	return consensus.NewEpochSnapshot(epochID, startSlot, node.config.EpochSlots, seed, validatorSet)
}

func (node *posNode) installEpochSnapshotLocked(snapshot consensus.EpochSnapshot) error {
	if err := node.reconcileConsensusModeForInstalledSnapshotLocked(snapshot); err != nil {
		return err
	}
	schedule, err := consensus.NewLeaderSchedule(snapshot)
	if err != nil {
		return err
	}
	collector, err := node.voteCollectorForEpochLocked(snapshot.EpochID, snapshot)
	if err != nil {
		return err
	}
	node.epochSnapshot = snapshot
	node.leaderSchedule = schedule
	node.voteCollector = collector
	if node.epochSnapshots == nil {
		node.epochSnapshots = make(map[uint64]consensus.EpochSnapshot)
	}
	if node.leaderSchedules == nil {
		node.leaderSchedules = make(map[uint64]consensus.LeaderSchedule)
	}
	node.epochSnapshots[snapshot.EpochID] = snapshot
	node.leaderSchedules[snapshot.EpochID] = schedule
	node.logger.Info("posnode epoch ready",
		slog.Uint64("epoch", snapshot.EpochID),
		slog.Uint64("start_slot", snapshot.StartSlot),
		slog.Uint64("end_slot", snapshot.EndSlot),
		slog.Int("validators", len(snapshot.Validators)),
		slog.Uint64("total_stake", snapshot.TotalActiveStake),
	)
	return nil
}

// voteCollectorForEpochLocked 复用 epoch 投票收集器 + 避免跨 epoch 路由切换丢失已收到的恢复投票。
func (node *posNode) voteCollectorForEpochLocked(epochID uint64, snapshot consensus.EpochSnapshot) (*consensus.VoteCollector, error) {
	if node.voteCollectors == nil {
		node.voteCollectors = make(map[uint64]*consensus.VoteCollector)
	}
	if collector, exists := node.voteCollectors[epochID]; exists {
		return collector, nil
	}
	collector, err := consensus.NewVoteCollectorWithQuorums(
		snapshot.StakeMap(),
		consensus.Quorum{Numerator: defaultConsensusQuorum, Denominator: 3, Strict: true},
		consensus.Quorum{Numerator: defaultTimeoutQuorum, Denominator: 3, Strict: true},
	)
	if err != nil {
		return nil, err
	}
	node.voteCollectors[epochID] = collector
	return collector, nil
}

func (node *posNode) buildRegisterTransaction() (structure.Transaction, error) {
	if len(node.stakerKeyPair.PrivateKey) == 0 {
		return structure.Transaction{}, fmt.Errorf("posnode: auto register requires local staker private key")
	}
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

func mustStructureKeyPair(seedText string) structure.OrdexusKeyPair {
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
