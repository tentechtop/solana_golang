package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sort"
	"sync"
	"syscall"
	"time"

	"solana_golang/consensus"
	"solana_golang/p2p"
	"solana_golang/programs/stake"
	"solana_golang/programs/system"
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
	executor         runtime.FixedExecutor
	peerKeyPair      rawKeyPair
	stakerKeyPair    structure.SolanaKeyPair
	validatorKeyPair structure.SolanaKeyPair
	consensusKeyPair structure.SolanaKeyPair
	state            consensus.ChainState
	blockhashQueue   structure.BlockhashQueue
	mempool          []structure.Transaction
	seenTransactions map[string]struct{}
	epochSnapshot    consensus.EpochSnapshot
	leaderSchedule   consensus.LeaderSchedule
	voteCollector    *consensus.VoteCollector
	latestBlockHash  structure.Hash
	latestQCHash     structure.Hash
	height           uint64
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
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
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
		if node.host != nil {
			_ = node.host.Close()
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
	node := &posNode{
		config:           config,
		logger:           logger,
		executor:         executor,
		peerKeyPair:      mustRawKeyPair(config.PeerSeed),
		stakerKeyPair:    mustStructureKeyPair(config.StakerSeed),
		validatorKeyPair: mustStructureKeyPair(config.ValidatorSeed),
		consensusKeyPair: mustStructureKeyPair(config.ConsensusSeed),
		seenTransactions: make(map[string]struct{}),
	}
	if err := node.initGenesis(); err != nil {
		return nil, err
	}
	if err := node.rebuildEpoch(0, 1, mustHash("genesis-seed")); err != nil {
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
	)
	if node.config.AutoRegister {
		go node.autoRegisterLoop(ctx)
	}
	go node.slotLoop(ctx)
	return nil
}

func (node *posNode) startP2P(ctx context.Context) error {
	listenAddress, err := utils.BuildMultiAddress(utils.MultiAddressIP4, node.config.ListenIP, utils.ProtocolTCP, node.config.ListenPort, node.peerKeyPair.peerID)
	if err != nil {
		return fmt.Errorf("posnode: build listen address: %w", err)
	}
	host, err := p2p.NewHost(p2p.HostConfig{
		PeerID:        node.peerKeyPair.peerID,
		AllowInsecure: true,
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
	}
	handlers := []p2p.VoidProtocolHandler{
		node.handleTransactionMessage,
		node.handleProposalMessage,
		node.handleVoteMessage,
		node.handleQCMessage,
	}
	for index, spec := range specs {
		if err := node.host.RegisterVoidHandler(spec, handlers[index]); err != nil {
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
	startedAt := time.Now()
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
	if tick.Slot > node.epochSnapshot.EndSlot {
		seed := node.latestBlockHash
		if seed.IsZero() {
			seed = mustHash("empty-epoch-seed")
		}
		if err := node.rebuildEpochLocked(node.epochSnapshot.EpochID+1, tick.Slot, seed); err != nil {
			node.logger.Error("posnode rebuild epoch failed", slog.Uint64("slot", tick.Slot), slog.Any("error", err))
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
	transactions := append([]structure.Transaction(nil), node.mempool...)
	node.mempool = nil
	request := consensus.ProduceBlockRequest{
		Slot:           slot,
		Height:         node.height + 1,
		EpochSnapshot:  node.epochSnapshot,
		Schedule:       node.leaderSchedule,
		ParentHash:     node.latestBlockHash,
		PreviousQCHash: node.latestQCHash,
		ParentState:    node.state,
		Transactions:   transactions,
		BlockhashQueue: node.blockhashQueue,
		LeaderKeyPair:  node.consensusKeyPair,
	}
	node.mutex.Unlock()

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
	node.mutex.Lock()
	node.state = nextState
	node.latestBlockHash = proposalHash
	node.height = proposal.Header.Height
	node.lastProducedSlot = slot
	node.mutex.Unlock()
	node.logger.Info("posnode block proposed",
		slog.Uint64("slot", slot),
		slog.Uint64("height", proposal.Header.Height),
		slog.Int("transactions", len(proposal.Transactions)),
		slog.String("hash", proposalHash.String()),
	)
	node.broadcastProposal(ctx, proposal)
	node.voteForKnownProposal(ctx, proposal.Header.Slot, proposalHash)
}

func (node *posNode) handleTransactionMessage(ctx context.Context, message p2p.Message) error {
	transaction, err := decodeTransactionMessage(message)
	if err != nil {
		return err
	}
	return node.addTransaction(transaction)
}

func (node *posNode) handleProposalMessage(ctx context.Context, message p2p.Message) error {
	proposal, err := decodeProposalMessage(message)
	if err != nil {
		return err
	}
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
		node.logger.Info("posnode qc formed", slog.Uint64("slot", qc.Slot), slog.Uint64("stake", qc.ConfirmedStake))
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
	qcHash, err := hashQC(envelope.QC)
	if err != nil {
		return err
	}
	node.mutex.Lock()
	node.latestQCHash = qcHash
	node.mutex.Unlock()
	node.logger.Info("posnode qc received", slog.Uint64("slot", envelope.QC.Slot), slog.String("qc_hash", qcHash.String()))
	return nil
}

func (node *posNode) voteForProposal(ctx context.Context, proposal consensus.BlockProposal) error {
	node.mutex.Lock()
	if proposal.Header.Slot <= node.lastVotedSlot {
		node.mutex.Unlock()
		return nil
	}
	leader, exists := node.epochSnapshot.ValidatorByID(proposal.Header.LeaderID)
	if !exists {
		node.mutex.Unlock()
		return fmt.Errorf("posnode: proposal leader not in snapshot")
	}
	request := consensus.VerifyProposalRequest{
		Proposal:       proposal,
		EpochSnapshot:  node.epochSnapshot,
		Schedule:       node.leaderSchedule,
		ParentHash:     node.latestBlockHash,
		ParentState:    node.state,
		BlockhashQueue: node.blockhashQueue,
		Leader:         leader,
	}
	node.mutex.Unlock()

	verifier := consensus.ProposalVerifier{ChainID: node.config.ChainID, Executor: node.executor}
	nextState, err := verifier.VerifyProposal(ctx, request)
	if err != nil {
		return err
	}
	proposalHash, err := proposal.Hash()
	if err != nil {
		return err
	}
	node.mutex.Lock()
	node.state = nextState
	node.latestBlockHash = proposalHash
	node.height = proposal.Header.Height
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
		BlockHash:          proposalHash,
		VoterID:            string(validatorID),
		Stake:              stakeValue,
		CreatedAtUnixMilli: time.Now().UnixMilli(),
	}
	node.logger.Info("posnode vote sent", slog.Uint64("slot", vote.Slot), slog.String("voter", vote.VoterID))
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
		BlockHash:          blockHash,
		VoterID:            string(validatorID),
		Stake:              stakeValue,
		CreatedAtUnixMilli: time.Now().UnixMilli(),
	}
	node.logger.Info("posnode leader self vote sent", slog.Uint64("slot", vote.Slot), slog.String("voter", vote.VoterID))
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
	transactionID, err := transaction.TxIDString()
	if err != nil {
		return err
	}
	node.mutex.Lock()
	defer node.mutex.Unlock()
	if _, exists := node.seenTransactions[transactionID]; exists {
		return nil
	}
	node.seenTransactions[transactionID] = struct{}{}
	node.mempool = append(node.mempool, transaction)
	node.logger.Info("posnode transaction accepted", slog.String("tx", transactionID), slog.Int("mempool", len(node.mempool)))
	return nil
}

func (node *posNode) broadcastTransaction(ctx context.Context, transaction structure.Transaction) {
	message, err := encodeTransactionMessage(transaction)
	if err != nil {
		node.logger.Error("posnode encode transaction failed", slog.Any("error", err))
		return
	}
	if err := node.host.Broadcast(ctx, node.knownPeerIDs, message); err != nil {
		node.logger.Warn("posnode broadcast transaction failed", slog.Any("error", err))
	}
}

func (node *posNode) broadcastProposal(ctx context.Context, proposal consensus.BlockProposal) {
	message, err := encodeProposalMessage(proposal)
	if err != nil {
		node.logger.Error("posnode encode proposal failed", slog.Any("error", err))
		return
	}
	if err := node.host.Broadcast(ctx, node.knownPeerIDs, message); err != nil {
		node.logger.Warn("posnode broadcast proposal failed", slog.Any("error", err))
	}
}

func (node *posNode) broadcastVote(ctx context.Context, vote consensus.Vote) {
	message, err := encodeVoteMessage(vote, node.consensusKeyPair)
	if err != nil {
		node.logger.Error("posnode encode vote failed", slog.Any("error", err))
		return
	}
	if err := node.host.Broadcast(ctx, node.knownPeerIDs, message); err != nil {
		node.logger.Warn("posnode broadcast vote failed", slog.Any("error", err))
	}
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
	message, err := encodeQCMessage(qc)
	if err != nil {
		node.logger.Error("posnode encode qc failed", slog.Any("error", err))
		return
	}
	if err := node.host.Broadcast(ctx, node.knownPeerIDs, message); err != nil {
		node.logger.Warn("posnode broadcast qc failed", slog.Any("error", err))
	}
}

func (node *posNode) rebuildEpoch(epochID uint64, startSlot uint64, seed structure.Hash) error {
	node.mutex.Lock()
	defer node.mutex.Unlock()
	return node.rebuildEpochLocked(epochID, startSlot, seed)
}

func (node *posNode) rebuildEpochLocked(epochID uint64, startSlot uint64, seed structure.Hash) error {
	validatorSet, err := node.validatorSetFromStateLocked()
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

func (node *posNode) validatorSetFromStateLocked() (consensus.ValidatorSet, error) {
	validators := make([]consensus.ValidatorState, 0)
	for _, account := range node.state.Accounts {
		if account.Account.Owner != structure.DefaultBuiltinProgramIDs.Stake || len(account.Account.Data) == 0 {
			continue
		}
		stakeState, err := stake.UnmarshalValidatorStateBinary(account.Account.Data)
		if err != nil {
			continue
		}
		if stakeState.Status != stake.ValidatorStatusActive {
			continue
		}
		validators = append(validators, consensus.ValidatorState{
			AccountAddress:     account.Address,
			ConsensusPublicKey: stakeState.ConsensusPublicKey,
			P2PPeerID:          stakeState.P2PPeerID,
			StakeLamports:      stakeState.ActiveStake + stakeState.PendingStake,
			Status:             consensus.ValidatorStatusActive,
			CommissionBps:      stakeState.CommissionBps,
		})
	}
	return consensus.NewValidatorSet(validators)
}

func (node *posNode) initGenesis() error {
	accounts := make([]structure.AddressedAccount, 0, len(node.config.Genesis.FundedAccounts)+len(node.config.Genesis.InitialValidators)+2)
	for _, funded := range node.config.Genesis.FundedAccounts {
		keyPair := mustStructureKeyPair(funded.Seed)
		accounts = append(accounts, newAccount(keyPair.PublicKey, funded.Lamports, structure.DefaultBuiltinProgramIDs.System, false, nil))
	}
	for _, validatorConfig := range node.config.Genesis.InitialValidators {
		staker := mustStructureKeyPair(validatorConfig.StakerSeed)
		validator := mustStructureKeyPair(validatorConfig.ValidatorSeed)
		consensusKey := mustStructureKeyPair(validatorConfig.ConsensusSeed)
		stakeState := stake.ValidatorState{
			ConsensusPublicKey: consensusKey.PublicKey,
			StakerAccount:      staker.PublicKey,
			P2PPeerID:          validatorConfig.PeerID,
			CommissionBps:      0,
			ActiveStake:        validatorConfig.StakeLamports,
			Status:             stake.ValidatorStatusActive,
		}
		data, err := stakeState.MarshalBinary()
		if err != nil {
			return fmt.Errorf("posnode: marshal genesis validator: %w", err)
		}
		accounts = append(accounts, newAccount(validator.PublicKey, validatorConfig.StakeLamports+10_000_000, structure.DefaultBuiltinProgramIDs.Stake, false, data))
	}
	accounts = append(accounts, newAccount(structure.DefaultBuiltinProgramIDs.System, 10_000_000, structure.DefaultBuiltinProgramIDs.NativeLoader, true, nil))
	accounts = append(accounts, newAccount(structure.DefaultBuiltinProgramIDs.Stake, 10_000_000, structure.DefaultBuiltinProgramIDs.NativeLoader, true, nil))
	sort.Slice(accounts, func(leftIndex int, rightIndex int) bool {
		return accounts[leftIndex].Address.String() < accounts[rightIndex].Address.String()
	})
	node.state = consensus.ChainState{Accounts: accounts}
	node.latestBlockHash = mustHash("genesis")
	node.latestQCHash = mustHash("genesis-qc")
	node.blockhashQueue = structure.NewBlockhashQueue(150)
	return node.blockhashQueue.Add(structure.RecentBlockhashEntry{
		Blockhash:     node.latestBlockHash,
		Slot:          1,
		FeeCalculator: structure.DefaultFeeCalculator(),
		TimestampUnix: time.Now().Unix(),
	})
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
		RecentBlockhash: node.latestBlockHash,
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
