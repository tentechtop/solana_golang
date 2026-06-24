package posnode

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"solana_golang/consensus"
	"solana_golang/internal/poswire"
	"solana_golang/p2p"
	"solana_golang/rpc"
	"solana_golang/structure"
)

const (
	maxForwardedRPCBodyBytes   = 1 << 20
	maxForwardedRPCBatchSize   = 32
	publicForwardRequestMillis = int64(8000)
	transactionFanoutPeers     = 64
	transactionFanoutWorkers   = 64
	transactionFanoutTimeout   = 6 * time.Second
	transactionRouteProbeTime  = 1200 * time.Millisecond
	stableBlockhashProbePeers  = 4
	stableBlockhashMinSlots    = uint64(32)
)

type rpcForwardDecodedResponse struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpc.Error      `json:"error,omitempty"`
}

type upstreamBlockhashStatus struct {
	FinalizedHash   string `json:"finalized_hash"`
	FinalizedHeight uint64 `json:"finalized_height"`
	FinalizedSlot   uint64 `json:"finalized_slot"`
	HeadHash        string `json:"head_hash"`
	HeadHeight      uint64 `json:"head_height"`
	HeadSlot        uint64 `json:"head_slot"`
}

type upstreamTransactionRouteStatus struct {
	UpcomingLeaders []leaderSlotJSON    `json:"upcoming_leaders"`
	TransactionFast transactionFastJSON `json:"transaction_fast_path"`
}

func (node *posNode) ForwardPublicRPC(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, *rpc.Error, error) {
	response, err := node.forwardPublicRPC(ctx, method, params)
	if err != nil {
		return nil, nil, err
	}
	if response.Error != nil {
		return nil, response.RPCError(), nil
	}
	if len(response.Result) == 0 {
		return json.RawMessage("null"), nil, nil
	}
	return json.RawMessage(response.Result), nil, nil
}

func (node *posNode) forwardPublicRPC(ctx context.Context, method string, params json.RawMessage) (poswire.RPCForwardResponse, error) {
	if err := node.ensureBootstrapPublicForwardReady(); err != nil {
		return poswire.RPCForwardResponse{}, err
	}
	if method == rpc.MethodGetLatestBlockhash {
		return node.forwardStableLatestBlockhash(ctx, params)
	}
	if method == rpc.MethodSendTransaction {
		return node.forwardSignedTransaction(ctx, method, params)
	}
	peerID, err := node.selectForwardValidatorPeer()
	if err != nil {
		return poswire.RPCForwardResponse{}, err
	}
	return node.forwardRawRPCToPeer(ctx, peerID, method, params)
}

func (node *posNode) ensureBootstrapPublicForwardReady() error {
	if node.bootstrapCoordinator == nil {
		return fmt.Errorf("posnode: public rpc forwarding requires bootstrap coordinator")
	}
	manifest, err := node.bootstrapCoordinator.GetBootstrapManifest(context.Background())
	if err != nil {
		return err
	}
	if !manifest.Ready {
		return fmt.Errorf("posnode: bootstrap manifest is not ready")
	}
	if err := node.activateBootstrapManifest(context.Background(), manifest); err != nil {
		return err
	}
	if node.host == nil {
		return fmt.Errorf("posnode: p2p host is not ready")
	}
	return nil
}

func (node *posNode) forwardRawRPCToPeer(ctx context.Context, peerID string, method string, params json.RawMessage) (poswire.RPCForwardResponse, error) {
	payload, err := poswire.MarshalRPCForwardRequest(poswire.RPCForwardRequest{
		Method: method,
		Params: params,
	}, maxForwardedRPCBodyBytes)
	if err != nil {
		return poswire.RPCForwardResponse{}, fmt.Errorf("posnode: marshal p2p rpc request: %w", err)
	}
	request, err := p2p.NewRequestMessageWithMaxSize(node.peerKeyPair.peerID, p2p.ProtocolPoSRPCForwardV1, payload, maxForwardedRPCBodyBytes)
	if err != nil {
		return poswire.RPCForwardResponse{}, fmt.Errorf("posnode: build p2p rpc request: %w", err)
	}
	requestContext, cancel := context.WithTimeout(ctx, time.Duration(publicForwardRequestMillis)*time.Millisecond)
	defer cancel()
	response, err := node.host.Request(requestContext, peerID, request)
	if err != nil {
		return poswire.RPCForwardResponse{}, fmt.Errorf("posnode: forward rpc to validator %s: %w", peerID, err)
	}
	return poswire.UnmarshalRPCForwardResponse(response.Payload, maxForwardedRPCBodyBytes)
}

func (node *posNode) forwardStableLatestBlockhash(ctx context.Context, params json.RawMessage) (poswire.RPCForwardResponse, error) {
	peerIDs := node.connectedBootstrapValidatorPeerIDs()
	if len(peerIDs) == 0 {
		return poswire.RPCForwardResponse{}, fmt.Errorf("posnode: no connected validator peer")
	}
	startIndex := node.nextRPCForwardPeer.Add(1)
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
			return poswire.RPCForwardResponse{}, fmt.Errorf("posnode: marshal stable blockhash: %w", err)
		}
		return poswire.RPCForwardResponse{Result: resultBytes}, nil
	}
	return node.forwardRawRPCToPeer(ctx, orderedPeerIDs[0], rpc.MethodGetLatestBlockhash, params)
}

func stableLatestBlockhashFromStatus(status upstreamBlockhashStatus) (rpc.LatestBlockhashResult, bool) {
	finalizedHash := strings.TrimSpace(status.FinalizedHash)
	if finalizedHash == "" || status.FinalizedHeight == 0 || status.FinalizedSlot == 0 {
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

func (node *posNode) forwardSignedTransaction(ctx context.Context, method string, params json.RawMessage) (poswire.RPCForwardResponse, error) {
	peerIDs := node.connectedBootstrapValidatorPeerIDs()
	if len(peerIDs) == 0 {
		return poswire.RPCForwardResponse{}, fmt.Errorf("posnode: no connected validator peer")
	}
	orderedPeerIDs := node.orderedBootstrapTransactionPeerIDs(ctx, peerIDs)
	var firstRetryableError *poswire.RPCForwardError
	var lastTransportError error
	for peerIndex, peerID := range orderedPeerIDs {
		response, err := node.forwardRawRPCToPeer(ctx, peerID, method, params)
		if err != nil {
			lastTransportError = err
			node.logger.Warn("bootstrap send transaction forward failed",
				slog.String("peer_id", peerID),
				slog.Any("error", err),
			)
			continue
		}
		if response.Error == nil {
			signature := rpcForwardResultText(response.Result)
			node.logger.Info("bootstrap send transaction accepted",
				slog.String("peer_id", peerID),
				slog.String("signature", signature),
			)
			fanoutParams := append(json.RawMessage(nil), params...)
			fanoutPeerIDs := append([]string(nil), orderedPeerIDs...)
			node.startWorker(func() {
				node.fanoutSignedTransaction(context.WithoutCancel(ctx), method, fanoutParams, fanoutPeerIDs, peerIndex, signature)
			})
			return response, nil
		}
		if !isRetryableTransactionForwardError(response.Error) {
			return response, nil
		}
		if firstRetryableError == nil {
			firstRetryableError = response.Error
		}
		node.logger.Warn("bootstrap send transaction retrying validator",
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
	return poswire.RPCForwardResponse{}, fmt.Errorf("posnode: no validator accepted transaction")
}

// orderedBootstrapTransactionPeerIDs 排序公网交易入口 + 先覆盖当前和未来 leader，再做普通验证者冗余扩散。
func (node *posNode) orderedBootstrapTransactionPeerIDs(ctx context.Context, peerIDs []string) []string {
	if len(peerIDs) == 0 {
		return nil
	}
	startIndex := node.nextRPCForwardPeer.Add(1) - 1
	rotatedPeerIDs := rotatePeerIDs(peerIDs, startIndex)
	priorityPeerIDs := node.bootstrapPreferredTransactionPeerIDsFromStatus(ctx, rotatedPeerIDs)
	if len(priorityPeerIDs) == 0 {
		priorityPeerIDs = node.bootstrapPreferredTransactionPeerIDsFromConfig()
	}
	return prioritizePeerIDs(rotatedPeerIDs, priorityPeerIDs)
}

// bootstrapPreferredTransactionPeerIDsFromStatus 读取验证者路由视图 + 动态质押变化后以链上节点状态为准。
func (node *posNode) bootstrapPreferredTransactionPeerIDsFromStatus(ctx context.Context, peerIDs []string) []string {
	if node.host == nil || len(peerIDs) == 0 {
		return nil
	}
	probeContext, cancel := context.WithTimeout(ctx, transactionRouteProbeTime)
	defer cancel()
	probeLimit := minInt(stableBlockhashProbePeers, len(peerIDs))
	for index, peerID := range peerIDs {
		if index >= probeLimit {
			break
		}
		select {
		case <-probeContext.Done():
			return nil
		default:
		}
		response, err := node.forwardRawRPCToPeer(probeContext, peerID, rpc.MethodGetNodeStatus, json.RawMessage("[]"))
		if err != nil || response.Error != nil {
			continue
		}
		status := upstreamTransactionRouteStatus{}
		if err := json.Unmarshal(response.Result, &status); err != nil {
			continue
		}
		peerIDs := status.TransactionFast.PreferredPeerIDs
		if len(peerIDs) == 0 {
			peerIDs = leaderSlotPeerIDs(status.UpcomingLeaders)
		}
		if len(peerIDs) > 0 {
			return uniquePeerIDs(peerIDs)
		}
	}
	return nil
}

// bootstrapPreferredTransactionPeerIDsFromConfig 兜底计算启动期 leader + bootstrap 无账本时仍能覆盖初始 leader 窗口。
func (node *posNode) bootstrapPreferredTransactionPeerIDsFromConfig() []string {
	chainID, genesisStartMs, slotMillis, epochSlots, forwardSlots, validators := node.bootstrapRoutingConfigSnapshot()
	if chainID == "" || epochSlots == 0 || len(validators) == 0 {
		return nil
	}
	validatorSet, err := bootstrapValidatorSetFromGenesis(validators)
	if err != nil {
		node.logger.Debug("bootstrap transaction route genesis validators unavailable", slog.Any("error", err))
		return nil
	}
	startSlot := bootstrapRoutingSlot(genesisStartMs, slotMillis)
	epochID, epochStartSlot := epochForSlotWithEpochSlots(startSlot, epochSlots)
	snapshot, err := consensus.NewEpochSnapshot(epochID, epochStartSlot, epochSlots, mustHash(fmt.Sprintf("%s-epoch-%d", chainID, epochID)), validatorSet)
	if err != nil {
		node.logger.Debug("bootstrap transaction route epoch unavailable", slog.Any("error", err))
		return nil
	}
	schedule, err := consensus.NewLeaderSchedule(snapshot)
	if err != nil {
		node.logger.Debug("bootstrap transaction route schedule unavailable", slog.Any("error", err))
		return nil
	}
	if forwardSlots < 0 {
		forwardSlots = 0
	}
	peerIDs := make([]string, 0, forwardSlots+1)
	for offset := 0; offset <= forwardSlots; offset++ {
		slot := startSlot + uint64(offset)
		if slot > snapshot.EndSlot {
			break
		}
		leaderID, err := schedule.LeaderForSlot(slot)
		if err != nil {
			continue
		}
		leader, exists := snapshot.ValidatorByID(leaderID)
		if exists {
			peerIDs = append(peerIDs, leader.P2PPeerID)
		}
	}
	return uniquePeerIDs(peerIDs)
}

func (node *posNode) bootstrapRoutingConfigSnapshot() (string, int64, int, uint64, int, []genesisValidatorConfig) {
	node.mutex.Lock()
	defer node.mutex.Unlock()
	validators := append([]genesisValidatorConfig(nil), node.config.Genesis.InitialValidators...)
	return node.config.ChainID,
		node.config.GenesisStartMs,
		node.config.SlotMillis,
		node.config.EpochSlots,
		node.config.TransactionLeaderForwardSlots,
		validators
}

func bootstrapValidatorSetFromGenesis(validators []genesisValidatorConfig) (consensus.ValidatorSet, error) {
	states := make([]consensus.ValidatorState, 0, len(validators))
	for _, validator := range validators {
		validatorAddress, err := genesisPublicKeyFromAddressOrSeed(validator.ValidatorAddress, validator.ValidatorSeed, "validator account")
		if err != nil {
			return consensus.ValidatorSet{}, err
		}
		consensusPublicKey, err := genesisPublicKeyFromAddressOrSeed(validator.ConsensusPublicKey, validator.ConsensusSeed, "validator consensus")
		if err != nil {
			return consensus.ValidatorSet{}, err
		}
		blsPublicKey, err := genesisBLSPublicKey(validator.BLSPublicKeyBase64, validator.ConsensusSeed)
		if err != nil {
			return consensus.ValidatorSet{}, err
		}
		states = append(states, consensus.ValidatorState{
			AccountAddress:     validatorAddress,
			ConsensusPublicKey: consensusPublicKey,
			BLSPublicKey:       blsPublicKey,
			P2PPeerID:          validator.PeerID,
			StakeLamports:      validator.StakeLamports,
			CommissionBps:      validator.CommissionBps,
		})
	}
	return consensus.NewValidatorSet(states)
}

func bootstrapRoutingSlot(genesisStartMs int64, slotMillis int) uint64 {
	if slotMillis <= 0 {
		return 1
	}
	startedAt := time.UnixMilli(genesisStartMs)
	now := time.Now()
	if !now.After(startedAt) {
		return 1
	}
	return uint64(now.Sub(startedAt)/(time.Duration(slotMillis)*time.Millisecond)) + 1
}

func epochForSlotWithEpochSlots(slot uint64, epochSlots uint64) (uint64, uint64) {
	if slot == 0 || epochSlots == 0 {
		return 0, 1
	}
	epochID := (slot - 1) / epochSlots
	return epochID, epochID*epochSlots + 1
}

func leaderSlotPeerIDs(leaders []leaderSlotJSON) []string {
	peerIDs := make([]string, 0, len(leaders))
	for _, leader := range leaders {
		peerIDs = append(peerIDs, leader.PeerID)
	}
	return uniquePeerIDs(peerIDs)
}

func prioritizePeerIDs(peerIDs []string, priorityPeerIDs []string) []string {
	if len(peerIDs) == 0 {
		return nil
	}
	connected := make(map[string]struct{}, len(peerIDs))
	for _, peerID := range peerIDs {
		connected[peerID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(peerIDs))
	result := make([]string, 0, len(peerIDs))
	for _, peerID := range priorityPeerIDs {
		if _, exists := connected[peerID]; !exists {
			continue
		}
		if _, exists := seen[peerID]; exists {
			continue
		}
		seen[peerID] = struct{}{}
		result = append(result, peerID)
	}
	for _, peerID := range peerIDs {
		if _, exists := seen[peerID]; exists {
			continue
		}
		seen[peerID] = struct{}{}
		result = append(result, peerID)
	}
	return result
}

// handleRPCForwardRequest 处理公网入口转发请求 + P2P 只承载二进制封装，JSON-RPC 仅在本地 RPC 边界还原。
func (node *posNode) handleRPCForwardRequest(ctx context.Context, message p2p.Message) (p2p.Message, error) {
	if len(message.Payload) == 0 {
		return p2p.Message{}, fmt.Errorf("posnode: forwarded rpc payload is empty")
	}
	if len(message.Payload) > maxForwardedRPCBodyBytes {
		return p2p.Message{}, fmt.Errorf("posnode: forwarded rpc payload too large")
	}
	forwardRequest, err := poswire.UnmarshalRPCForwardRequest(message.Payload, maxForwardedRPCBodyBytes)
	if err != nil {
		return p2p.Message{}, err
	}
	body, err := json.Marshal(rpc.Request{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  forwardRequest.Method,
		Params:  forwardRequest.Params,
	})
	if err != nil {
		return p2p.Message{}, fmt.Errorf("posnode: marshal forwarded rpc request: %w", err)
	}
	responseBody, err := rpc.HandleRawRequest(ctx, rpc.NewPublicRouter(node), body, maxForwardedRPCBatchSize)
	if err != nil {
		return p2p.Message{}, fmt.Errorf("posnode: handle forwarded rpc: %w", err)
	}
	forwardResponse, err := encodeRPCForwardResponse(responseBody)
	if err != nil {
		return p2p.Message{}, err
	}
	responsePayload, err := poswire.MarshalRPCForwardResponse(forwardResponse, maxForwardedRPCBodyBytes)
	if err != nil {
		return p2p.Message{}, fmt.Errorf("posnode: marshal forwarded rpc response: %w", err)
	}
	response, err := p2p.NewResponseMessage(node.peerKeyPair.peerID, p2p.ProtocolPoSRPCForwardV1, message.ID, responsePayload)
	if err != nil {
		return p2p.Message{}, err
	}
	response.ToPeerID = message.FromPeerID
	return response, nil
}

func encodeRPCForwardResponse(responseBody []byte) (poswire.RPCForwardResponse, error) {
	if len(responseBody) == 0 || string(responseBody) == "null" {
		return poswire.RPCForwardResponse{Result: []byte("null")}, nil
	}
	decoded := rpcForwardDecodedResponse{}
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return poswire.RPCForwardResponse{}, fmt.Errorf("posnode: decode forwarded rpc response: %w", err)
	}
	if decoded.Error != nil {
		forwardError, err := poswire.RPCForwardErrorFromRPCError(decoded.Error)
		if err != nil {
			return poswire.RPCForwardResponse{}, err
		}
		return poswire.RPCForwardResponse{Error: forwardError}, nil
	}
	if len(decoded.Result) == 0 {
		return poswire.RPCForwardResponse{Result: []byte("null")}, nil
	}
	return poswire.RPCForwardResponse{Result: decoded.Result}, nil
}

func (node *posNode) fanoutSignedTransaction(ctx context.Context, method string, params json.RawMessage, peerIDs []string, acceptedIndex int, signature string) {
	targets := transactionFanoutTargets(peerIDs, acceptedIndex, transactionFanoutPeers)
	if len(targets) == 0 {
		return
	}
	if node.rpcForwardFanoutLimiter != nil {
		select {
		case node.rpcForwardFanoutLimiter <- struct{}{}:
			defer func() { <-node.rpcForwardFanoutLimiter }()
		case <-ctx.Done():
			return
		}
	}
	paramsCopy := append(json.RawMessage(nil), params...)
	contextValue, cancel := context.WithTimeout(ctx, transactionFanoutTimeout)
	defer cancel()
	successCount := node.runTransactionFanoutWorkers(contextValue, method, paramsCopy, targets)
	node.logger.Info("bootstrap transaction fanout completed",
		slog.String("signature", signature),
		slog.Int("targets", len(targets)),
		slog.Int("accepted", successCount),
	)
}

func (node *posNode) runTransactionFanoutWorkers(ctx context.Context, method string, params json.RawMessage, targets []string) int {
	workerCount := minInt(transactionFanoutWorkers, len(targets))
	jobs := make(chan string)
	successes := make(chan struct{}, len(targets))
	var waitGroup sync.WaitGroup
	for workerIndex := 0; workerIndex < workerCount; workerIndex++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for peerID := range jobs {
				response, err := node.forwardRawRPCToPeer(ctx, peerID, method, params)
				if err == nil && response.Error == nil {
					successes <- struct{}{}
				}
			}
		}()
	}
	for _, peerID := range targets {
		select {
		case <-ctx.Done():
			close(jobs)
			waitGroup.Wait()
			close(successes)
			return len(successes)
		case jobs <- peerID:
		}
	}
	close(jobs)
	waitGroup.Wait()
	close(successes)
	return len(successes)
}

func (node *posNode) selectForwardValidatorPeer() (string, error) {
	candidates := node.connectedBootstrapValidatorPeerIDs()
	if len(candidates) == 0 {
		return "", fmt.Errorf("posnode: no connected validator peer")
	}
	index := node.nextRPCForwardPeer.Add(1)
	return candidates[index%uint64(len(candidates))], nil
}

func (node *posNode) connectedBootstrapValidatorPeerIDs() []string {
	if node.host == nil {
		return nil
	}
	node.refreshKnownPeersFromHost()
	node.mutex.Lock()
	peerIDs := make([]string, 0, len(node.config.BootstrapPeers))
	for _, peer := range node.config.BootstrapPeers {
		if peer.PeerID == "" || peer.PeerID == node.peerKeyPair.peerID {
			continue
		}
		if peer.ResolvedCapabilities&p2p.PeerCapabilityValidator == 0 && peer.ResolvedRole != p2p.PeerRoleValidator {
			continue
		}
		peerIDs = append(peerIDs, peer.PeerID)
	}
	node.mutex.Unlock()
	connectedPeerIDs := make([]string, 0, len(peerIDs))
	for _, peerID := range uniquePeerIDs(peerIDs) {
		if _, connected := node.host.ConnectionState(peerID); connected {
			connectedPeerIDs = append(connectedPeerIDs, peerID)
		}
	}
	return connectedPeerIDs
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
	text := ""
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
	dataText := ""
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
