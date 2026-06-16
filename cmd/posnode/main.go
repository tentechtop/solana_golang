package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"solana_golang/blockchain"
	"solana_golang/consensus"
	"solana_golang/database"
	"solana_golang/p2p"
	"solana_golang/programs/stake"
	"solana_golang/programs/system"
	"solana_golang/rpc"
	"solana_golang/runtime"
	"solana_golang/structure"
	"solana_golang/utils"
)

const (
	protocolNetworkID      = "pos-localnet"
	defaultConsensusQuorum = 2
)

type posNode struct {
	mutex            sync.Mutex
	config           nodeConfig
	logger           *slog.Logger
	host             *p2p.Host
	rpcServer        *rpc.Server
	db               database.Database
	ledger           *blockchain.Ledger
	executor         runtime.FixedExecutor
	peerKeyPair      rawKeyPair
	stakerKeyPair    structure.SolanaKeyPair
	validatorKeyPair structure.SolanaKeyPair
	consensusKeyPair structure.SolanaKeyPair
	blockhashQueue   structure.BlockhashQueue
	mempool          []structure.Transaction
	seenTransactions map[string]struct{}
	orphanProposals  map[structure.Hash][]consensus.BlockProposal
	epochSnapshot    consensus.EpochSnapshot
	leaderSchedule   consensus.LeaderSchedule
	voteCollector    *consensus.VoteCollector
	metrics          nodeMetrics
	lastProducedSlot uint64
	lastVotedSlot    uint64
	registeredSelf   bool
	knownPeerIDs     []string
}

type rawKeyPair struct {
	publicKey  []byte
	privateKey []byte
	peerID     string
}

func main() {
	configPath := flag.String("config", "", "posnode config json path")
	printPeerSeed := flag.String("print-peer-id", "", "print peer id for seed and exit")
	flag.Parse()
	if *printPeerSeed != "" {
		keyPair := mustRawKeyPair(*printPeerSeed)
		fmt.Println(keyPair.peerID)
		return
	}
	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "posnode: -config is required")
		os.Exit(1)
	}
	if err := run(*configPath); err != nil {
		slog.Error("posnode exited", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(configPath string) error {
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
	return nil
}

func newPosNode(config nodeConfig, logger *slog.Logger) (*posNode, error) {
	executor, err := runtime.NewFixedExecutor(
		system.NewProgram(structure.DefaultBuiltinProgramIDs.System),
		stake.NewProgram(structure.DefaultBuiltinProgramIDs.Stake),
	)
	if err != nil {
		return nil, fmt.Errorf("posnode: create executor: %w", err)
	}
	executor.Logger = logger
	node := &posNode{
		config:           config,
		logger:           logger,
		executor:         executor,
		peerKeyPair:      mustRawKeyPair(config.PeerSeed),
		stakerKeyPair:    mustStructureKeyPair(config.StakerSeed),
		validatorKeyPair: mustStructureKeyPair(config.ValidatorSeed),
		consensusKeyPair: mustStructureKeyPair(config.ConsensusSeed),
		seenTransactions: make(map[string]struct{}),
		orphanProposals:  make(map[structure.Hash][]consensus.BlockProposal),
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
		slog.String("peer_id", node.peerKeyPair.peerID),
		slog.String("staker", node.stakerKeyPair.PublicKey.String()),
		slog.String("validator", node.validatorKeyPair.PublicKey.String()),
		slog.String("consensus", node.consensusKeyPair.PublicKey.String()),
		slog.Uint64("genesis_supply", node.config.Genesis.InitialSupplyLamports),
		slog.Int64("genesis_start_unix_millis", node.config.GenesisStartMs),
		slog.String("data_path", node.config.DataPath),
	)
	if node.config.AutoRegister {
		go node.autoRegisterLoop(ctx)
	}
	if err := node.startRPC(); err != nil {
		return err
	}
	go node.blockSyncLoop(ctx)
	go node.slotLoop(ctx)
	return nil
}

func (node *posNode) startRPC() error {
	if !node.config.RPCEnabled {
		return nil
	}
	address := fmt.Sprintf("%s:%d", node.config.RPCListenIP, node.config.RPCPort)
	server := rpc.NewServer(rpc.ServerConfig{Address: address, Logger: node.logger}, rpc.NewDefaultRouter(node))
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
	ledger, err := blockchain.LoadOrCreateLedger(db, node.blockchainGenesisConfig())
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
		Slot:          head.Slot + 1,
		FeeCalculator: structure.DefaultFeeCalculator(),
		TimestampUnix: time.Now().Unix(),
	}); err != nil {
		return fmt.Errorf("posnode: init blockhash queue: %w", err)
	}
	if err := node.loadMempool(); err != nil {
		return err
	}
	node.logger.Info("posnode ledger ready",
		slog.Uint64("height", head.Height),
		slog.Uint64("slot", head.Slot),
		slog.String("block_hash", head.BlockHash.String()),
		slog.String("state_root", head.StateRoot.String()),
		slog.Int("mempool", len(node.mempool)),
	)
	return nil
}

func (node *posNode) blockchainGenesisConfig() blockchain.GenesisConfig {
	genesis := blockchain.GenesisConfig{
		ChainID:               node.config.ChainID,
		InitialSupplyLamports: node.config.Genesis.InitialSupplyLamports,
		FundedAccounts:        make([]blockchain.GenesisAccount, 0, len(node.config.Genesis.FundedAccounts)),
		InitialValidators:     make([]blockchain.GenesisValidator, 0, len(node.config.Genesis.InitialValidators)),
	}
	for _, account := range node.config.Genesis.FundedAccounts {
		keyPair := mustStructureKeyPair(account.Seed)
		genesis.FundedAccounts = append(genesis.FundedAccounts, blockchain.GenesisAccount{
			Address:  keyPair.PublicKey,
			Lamports: account.Lamports,
		})
	}
	for _, validator := range node.config.Genesis.InitialValidators {
		staker := mustStructureKeyPair(validator.StakerSeed)
		validatorAccount := mustStructureKeyPair(validator.ValidatorSeed)
		consensusKey := mustStructureKeyPair(validator.ConsensusSeed)
		genesis.InitialValidators = append(genesis.InitialValidators, blockchain.GenesisValidator{
			StakerAddress:      staker.PublicKey,
			ValidatorAddress:   validatorAccount.PublicKey,
			ConsensusPublicKey: consensusKey.PublicKey,
			P2PPeerID:          validator.PeerID,
			StakeLamports:      validator.StakeLamports,
		})
	}
	return genesis
}

func (node *posNode) startP2P(ctx context.Context) error {
	listenAddress, err := utils.BuildMultiAddress(utils.MultiAddressIP4, node.config.ListenIP, utils.ProtocolTCP, node.config.ListenPort, node.peerKeyPair.peerID)
	if err != nil {
		return fmt.Errorf("posnode: build listen address: %w", err)
	}
	host, err := p2p.NewHost(p2p.HostConfig{
		PeerID:        node.peerKeyPair.peerID,
		AllowInsecure: node.config.allowInsecureP2P(),
		Production:    node.config.Production,
		Environment:   node.config.Environment,
		PreferredProtocols: []utils.MultiAddressProtocol{
			utils.ProtocolTCP,
		},
		MaxPeers:       128,
		MaxConnections: 128,
		Logger:         node.logger,
	})
	if err != nil {
		return fmt.Errorf("posnode: create host: %w", err)
	}
	node.host = host
	if node.config.allowInsecureP2P() {
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
		peer.Role = p2p.PeerRoleValidator
		peer.Capabilities = p2p.PeerCapabilityValidator | p2p.PeerCapabilityRelay
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
	return nil
}

func (node *posNode) registerProtocols() error {
	specs := []p2p.ProtocolSpec{
		{ID: p2p.ProtocolPoSTransactionV1, Name: "/pos/transaction/1.0.0", Priority: p2p.MessagePriorityNormal, Class: p2p.ProtocolClassData},
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
		{ID: p2p.ProtocolPoSBlockByHashV1, Name: "/pos/sync/block-by-hash/1.0.0", HasResponse: true, Priority: p2p.MessagePriorityLow, Class: p2p.ProtocolClassData, Concurrency: p2p.ProtocolConcurrencyStateless},
		{ID: p2p.ProtocolPoSBlockByHeightV1, Name: "/pos/sync/block-by-height/1.0.0", HasResponse: true, Priority: p2p.MessagePriorityLow, Class: p2p.ProtocolClassData, Concurrency: p2p.ProtocolConcurrencyStateless},
		{ID: p2p.ProtocolPoSStateSnapshotV1, Name: "/pos/sync/state-snapshot/1.0.0", HasResponse: true, Priority: p2p.MessagePriorityLow, Class: p2p.ProtocolClassData, Concurrency: p2p.ProtocolConcurrencyStateless},
		{ID: p2p.ProtocolPoSStatusV1, Name: "/pos/status/1.0.0", HasResponse: true, Priority: p2p.MessagePriorityLow, Class: p2p.ProtocolClassData, Concurrency: p2p.ProtocolConcurrencyStateless},
	}
	resultHandlers := []p2p.ResultProtocolHandler{
		node.handleBlockByHashRequest,
		node.handleBlockByHeightRequest,
		node.handleStateSnapshotRequest,
		node.handleStatusRequest,
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
			for _, peerID := range node.knownPeerIDs {
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

func (node *posNode) autoRegisterLoop(ctx context.Context) {
	timer := time.NewTimer(1500 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
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
	}
}

func (node *posNode) slotLoop(ctx context.Context) {
	startedAt := node.config.genesisStartTime()
	clock, err := consensus.NewSlotClock(startedAt, 1, node.config.slotDuration(), node.config.slotDuration()*7/10)
	if err != nil {
		node.logger.Error("posnode slot clock failed", slog.Any("error", err))
		return
	}
	ticker := time.NewTicker(node.config.slotDuration() / 2)
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
	if isLeader {
		node.produceCurrentSlot(ctx, tick.Slot)
	}
}

func (node *posNode) produceCurrentSlot(ctx context.Context, slot uint64) {
	node.mutex.Lock()
	if slot <= node.lastProducedSlot {
		node.mutex.Unlock()
		return
	}
	transactions, removeTransactionIDs := node.selectMempoolTransactionsLocked(time.Now().UnixMilli())
	head := node.ledger.Head()
	rewardQCs, err := node.ledger.RewardQCs(head.FinalizedHeight, consensus.MaxRewardQCsPerBlock)
	if err != nil {
		node.logger.Warn("posnode reward qc load failed", slog.Any("error", err))
		rewardQCs = nil
	}
	request := consensus.ProduceBlockRequest{
		Slot:           slot,
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
	return node.voteForProposal(ctx, proposal)
}

func (node *posNode) handleVoteMessage(ctx context.Context, message p2p.Message) error {
	envelope, err := decodeVoteMessage(message)
	if err != nil {
		return err
	}
	if err := node.verifyVoteEnvelope(envelope); err != nil {
		return err
	}
	node.mutex.Lock()
	qc, formed, err := node.voteCollector.AddVote(envelope.Vote)
	node.mutex.Unlock()
	if err != nil {
		return err
	}
	if formed {
		if _, err := node.ledger.SaveQC(qc); err != nil {
			return err
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
		node.broadcastQC(ctx, qc)
	}
	return nil
}

func (node *posNode) handleQCMessage(ctx context.Context, message p2p.Message) error {
	envelope := qcEnvelope{}
	if err := jsonUnmarshal(message.Payload, &envelope); err != nil {
		return err
	}
	if err := envelope.QC.Validate(); err != nil {
		return err
	}
	head, err := node.ledger.SaveQC(envelope.QC)
	if err != nil {
		return err
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
	return nil
}

func (node *posNode) handleEvidenceMessage(ctx context.Context, message p2p.Message) error {
	_ = ctx
	if len(message.Payload) == 0 {
		return fmt.Errorf("posnode: empty evidence payload")
	}
	node.metrics.evidenceReceived.Add(1)
	node.logger.Warn("posnode evidence received",
		slog.String("from_peer", message.FromPeerID),
		slog.Int("payload_bytes", len(message.Payload)),
	)
	return nil
}

func (node *posNode) voteForProposal(ctx context.Context, proposal consensus.BlockProposal) error {
	node.mutex.Lock()
	if proposal.Header.Slot <= node.lastVotedSlot {
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
	request := consensus.VerifyProposalRequest{
		Proposal:       proposal,
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
	node.metrics.proposalsAccepted.Add(1)
	node.retryOrphanChildren(ctx, proposalHash)
	node.mutex.Lock()
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
	message, err := encodeVoteMessage(vote, node.consensusKeyPair)
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
	transactionID, err := transaction.TxIDString()
	if err != nil {
		return err
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
	proposalHash, hashErr := proposal.Hash()
	if hashErr != nil {
		proposalHash = structure.Hash{}
	}
	message, err := encodeProposalMessage(proposal)
	if err != nil {
		node.logger.Error("posnode encode proposal failed", slog.Any("error", err))
		return
	}
	if err := node.host.Broadcast(ctx, node.knownPeerIDs, message); err != nil {
		node.logger.Warn("posnode broadcast proposal failed",
			slog.Uint64("slot", proposal.Header.Slot),
			slog.Uint64("height", proposal.Header.Height),
			slog.String("block_hash", proposalHash.String()),
			slog.Any("error", err),
		)
		return
	}
	node.logger.Debug("posnode proposal broadcast",
		slog.Uint64("slot", proposal.Header.Slot),
		slog.Uint64("height", proposal.Header.Height),
		slog.String("block_hash", proposalHash.String()),
		slog.Int("peer_count", len(node.knownPeerIDs)),
	)
}

func (node *posNode) broadcastVote(ctx context.Context, vote consensus.Vote) {
	message, err := encodeVoteMessage(vote, node.consensusKeyPair)
	if err != nil {
		node.logger.Error("posnode encode vote failed", slog.Any("error", err))
		return
	}
	if err := node.host.Broadcast(ctx, node.knownPeerIDs, message); err != nil {
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
		slog.Int("peer_count", len(node.knownPeerIDs)),
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
	return nil
}

func (node *posNode) broadcastQC(ctx context.Context, qc consensus.QuorumCertificate) {
	qcHash, hashErr := hashQC(qc)
	if hashErr != nil {
		qcHash = structure.Hash{}
	}
	message, err := encodeQCMessage(qc)
	if err != nil {
		node.logger.Error("posnode encode qc failed", slog.Any("error", err))
		return
	}
	if err := node.host.Broadcast(ctx, node.knownPeerIDs, message); err != nil {
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
		slog.Int("peer_count", len(node.knownPeerIDs)),
	)
}

func (node *posNode) rebuildEpoch(epochID uint64, startSlot uint64, seed structure.Hash) error {
	node.mutex.Lock()
	defer node.mutex.Unlock()
	return node.rebuildEpochLocked(epochID, startSlot, seed)
}

func (node *posNode) rebuildEpochLocked(epochID uint64, startSlot uint64, seed structure.Hash) error {
	validatorSet, err := node.ledger.ValidatorSetFromState()
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
	instruction, err := stake.NewRegisterValidatorInstruction(node.consensusKeyPair.PublicKey, node.peerKeyPair.peerID, 0, node.config.StakeLamports)
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

func mustRawKeyPair(seedText string) rawKeyPair {
	privateKey := utils.SHA256([]byte(seedText))
	publicKey, err := utils.DeriveEd25519PublicKeyFromPrivateKey(privateKey)
	if err != nil {
		panic(err)
	}
	return rawKeyPair{publicKey: publicKey, privateKey: privateKey, peerID: utils.Base58Encode(publicKey)}
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
