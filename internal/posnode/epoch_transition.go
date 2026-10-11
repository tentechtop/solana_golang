package posnode

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/tentechtop/ordexus-core/blockchain"
	"github.com/tentechtop/ordexus-core/consensus"
	"github.com/tentechtop/ordexus-core/database"
	"github.com/tentechtop/ordexus-core/p2p"
	"github.com/tentechtop/ordexus-core/structure"
)

const (
	persistedEpochTransitionVersion    uint16 = 1
	defaultEpochProofBatchSize                = 64
	maxEpochProofBatchSize                    = 256
	defaultEpochProofBatchPayloadBytes        = 3 * 1024 * 1024
	maxEpochProofBatchPayloadBytes            = p2p.DefaultMaxMessageSize - 64*1024
	epochProofBatchResponseFixedSize          = 10 + 8 + 8 + 1 + 4 + 4
	epochTransitionCollectorCount             = 3
)

type epochTransitionCandidate struct {
	Transition consensus.EpochTransition
	Snapshot   consensus.EpochSnapshot
	Signatures map[string]consensus.EpochTransitionSignature
}

type epochTransitionProof struct {
	Certificate consensus.EpochTransitionCertificate `json:"certificate"`
	Snapshot    consensus.EpochSnapshot              `json:"snapshot"`
}

type epochProofRequest struct {
	FromEpochID uint64 `json:"from_epoch_id"`
	ToEpochID   uint64 `json:"to_epoch_id"`
}

type epochProofResponse struct {
	Found bool                 `json:"found"`
	Proof epochTransitionProof `json:"proof"`
	Error string               `json:"error,omitempty"`
}

type epochProofBatchRequest struct {
	FromEpochID    uint64
	TargetEpochID  uint64
	MaxProofs      uint32
	MaxPayloadSize uint32
}

type epochProofBatchResponse struct {
	FromEpochID   uint64
	TargetEpochID uint64
	Complete      bool
	Proofs        []epochTransitionProof
	Error         string
}

type persistedEpochTransition struct {
	Version uint16               `json:"version"`
	Proof   epochTransitionProof `json:"proof"`
}

type persistedLocalEpochTransitionVote struct {
	Version    uint16                             `json:"version"`
	Transition consensus.EpochTransition          `json:"transition"`
	Signature  consensus.EpochTransitionSignature `json:"signature"`
}

func epochTransitionStateKey(toEpochID uint64) []byte {
	return []byte(fmt.Sprintf("posnode:consensus:epoch-transition:v1:%020d", toEpochID))
}

func localEpochTransitionVoteStateKey(toEpochID uint64) []byte {
	return []byte(fmt.Sprintf("posnode:consensus:epoch-transition-vote:v1:%020d", toEpochID))
}

// installAuthenticatedStartupEpoch 只从已验证的证书链恢复当前 epoch。
// 节点恰好在边界重启且下一 epoch 证书尚未形成时，必须保留上一 epoch，继续参与证书收集。
func (node *posNode) installAuthenticatedStartupEpoch(nextSlot uint64) error {
	snapshot, err := node.authenticatedStartupEpochSnapshot(nextSlot)
	if err != nil {
		return err
	}
	if err := node.replaceEpochSnapshot(snapshot); err != nil {
		return err
	}
	node.mutex.Lock()
	defer node.mutex.Unlock()
	return node.installEpochSnapshotLocked(snapshot)
}

func (node *posNode) authenticatedStartupEpochSnapshot(nextSlot uint64) (consensus.EpochSnapshot, error) {
	targetEpochID, _ := node.epochForSlot(nextSlot)
	snapshot, err := node.authenticatedEpochSnapshot(targetEpochID)
	if err == nil {
		return snapshot, nil
	}
	if targetEpochID == 0 {
		return consensus.EpochSnapshot{}, err
	}

	// 仅“目标 epoch 的证书不存在”允许回退。证书存在但损坏时必须拒绝启动，不能掩盖磁盘损坏或攻击。
	_, found, loadErr := node.loadEpochTransitionProof(targetEpochID)
	if loadErr != nil {
		return consensus.EpochSnapshot{}, loadErr
	}
	if found {
		return consensus.EpochSnapshot{}, err
	}
	previousSnapshot, previousErr := node.authenticatedEpochSnapshot(targetEpochID - 1)
	if previousErr != nil {
		return consensus.EpochSnapshot{}, previousErr
	}
	return previousSnapshot, nil
}

// ensureEpochTransition 在 epoch 边界冻结链上验证者集合，并由上一 epoch 权益集合签署后才安装。
func (node *posNode) ensureEpochTransition(ctx context.Context, slot uint64) (bool, error) {
	targetEpochID, targetStartSlot := node.epochForSlot(slot)
	node.mutex.Lock()
	currentSnapshot := node.epochSnapshot
	node.mutex.Unlock()
	if targetEpochID < currentSnapshot.EpochID {
		return false, fmt.Errorf("posnode: consensus slot is behind installed epoch")
	}
	if targetEpochID == currentSnapshot.EpochID {
		authenticatedSnapshot, err := node.authenticatedEpochSnapshot(targetEpochID)
		if err != nil {
			return false, err
		}
		if authenticatedSnapshot.SnapshotStateRoot != currentSnapshot.SnapshotStateRoot {
			if err := node.replaceEpochSnapshot(authenticatedSnapshot); err != nil {
				return false, err
			}
			node.mutex.Lock()
			err = node.installEpochSnapshotLocked(authenticatedSnapshot)
			node.mutex.Unlock()
			if err != nil {
				return false, err
			}
		}
		return true, nil
	}
	if currentSnapshot.EpochID == ^uint64(0) || targetEpochID != currentSnapshot.EpochID+1 {
		return false, fmt.Errorf("posnode: epoch transition must advance exactly one epoch")
	}
	if authenticated, err := node.epochSnapshotAuthenticated(currentSnapshot); err != nil || !authenticated {
		if err == nil {
			err = fmt.Errorf("posnode: current epoch snapshot is not authenticated")
		}
		return false, err
	}

	if proof, found, err := node.loadEpochTransitionProof(targetEpochID); err != nil {
		return false, err
	} else if found {
		if _, _, err := node.verifyEpochTransitionProof(currentSnapshot, proof, true); err != nil {
			return false, err
		}
		if err := node.installCertifiedEpochProof(proof, slot); err != nil {
			return false, err
		}
		return true, nil
	}

	node.mutex.Lock()
	candidate, err := node.epochTransitionCandidateLocked(currentSnapshot, targetEpochID, targetStartSlot)
	if err != nil {
		node.mutex.Unlock()
		return false, err
	}
	unsignedProof := candidate.proof()
	transition := candidate.Transition
	node.mutex.Unlock()
	if len(unsignedProof.Certificate.Signatures) > 0 {
		signedStake, requiredStake, verifyErr := node.verifyEpochTransitionProof(currentSnapshot, unsignedProof, false)
		if verifyErr != nil {
			return false, verifyErr
		}
		if signedStake >= requiredStake {
			if err := node.persistEpochTransitionProof(unsignedProof); err != nil {
				return false, err
			}
			if err := node.installCertifiedEpochProof(unsignedProof, slot); err != nil {
				return false, err
			}
			return true, nil
		}
	}
	sourceReady, err := node.epochTransitionSourceQuorumReady(ctx, transition, currentSnapshot)
	if err != nil || !sourceReady {
		return false, err
	}

	node.mutex.Lock()
	candidate = node.epochTransitionCandidates[targetEpochID]
	if candidate == nil || candidate.Transition != transition {
		node.mutex.Unlock()
		return false, nil
	}
	localValidatorID := string(consensus.NewValidatorID(node.consensusKeyPair.PublicKey))
	_, localSignatureAlreadyAdded := candidate.Signatures[localValidatorID]
	localSignature, signed, err := node.localEpochTransitionSignature(candidate.Transition, currentSnapshot)
	if err != nil {
		node.mutex.Unlock()
		return false, err
	}
	if signed {
		candidate.Signatures[localSignature.ValidatorID] = localSignature
	}
	proof := candidate.proof()
	node.mutex.Unlock()

	if len(proof.Certificate.Signatures) == 0 {
		return false, nil
	}
	if signed && !localSignatureAlreadyAdded {
		if err := node.broadcastEpochTransitionProof(ctx, proof, ""); err != nil {
			node.logger.Debug("posnode epoch transition broadcast failed", slog.Any("error", err))
		}
	}
	signedStake, requiredStake, verifyErr := node.verifyEpochTransitionProof(currentSnapshot, proof, false)
	if verifyErr != nil {
		return false, verifyErr
	}
	if signedStake < requiredStake {
		return false, nil
	}
	if err := node.persistEpochTransitionProof(proof); err != nil {
		return false, err
	}
	if err := node.installCertifiedEpochProof(proof, slot); err != nil {
		return false, err
	}
	return true, nil
}

func (node *posNode) epochTransitionCandidateLocked(
	currentSnapshot consensus.EpochSnapshot,
	targetEpochID uint64,
	targetStartSlot uint64,
) (*epochTransitionCandidate, error) {
	if candidate := node.epochTransitionCandidates[targetEpochID]; candidate != nil {
		head := node.ledger.Head()
		if len(candidate.Signatures) > 0 ||
			(candidate.Transition.SourceBlockHeight == head.FinalizedHeight && candidate.Transition.SourceBlockHash == head.FinalizedHash.String()) {
			return candidate, nil
		}
		delete(node.epochTransitionCandidates, targetEpochID)
	}
	head := node.ledger.Head()
	targetSnapshot, err := node.buildEpochSnapshotLocked(targetEpochID, targetStartSlot, node.epochSeed(targetEpochID))
	if err != nil {
		return nil, err
	}
	candidate := &epochTransitionCandidate{
		Transition: consensus.EpochTransition{
			ChainID:           node.config.ChainID,
			ChainIdentityHash: node.config.ChainIdentityHash,
			GenesisHash:       node.config.GenesisHash,
			FromEpochID:       currentSnapshot.EpochID,
			ToEpochID:         targetEpochID,
			SourceBlockHeight: head.FinalizedHeight,
			SourceBlockHash:   head.FinalizedHash.String(),
			FromSnapshotRoot:  currentSnapshot.SnapshotStateRoot.String(),
			ToSnapshotRoot:    targetSnapshot.SnapshotStateRoot.String(),
		},
		Snapshot:   targetSnapshot,
		Signatures: make(map[string]consensus.EpochTransitionSignature),
	}
	if err := candidate.Transition.Validate(); err != nil {
		return nil, err
	}
	node.epochTransitionCandidates[targetEpochID] = candidate
	return candidate, nil
}

// epochTransitionSourceQuorumReady 要求上一 epoch 的严格法定权益看到同一最终确认检查点后才允许本地签名。
func (node *posNode) epochTransitionSourceQuorumReady(
	ctx context.Context,
	transition consensus.EpochTransition,
	previousSnapshot consensus.EpochSnapshot,
) (bool, error) {
	head := node.ledger.Head()
	if head.FinalizedHeight != transition.SourceBlockHeight || head.FinalizedHash.String() != transition.SourceBlockHash {
		return false, nil
	}
	requiredStake, err := (consensus.Quorum{Numerator: 2, Denominator: 3, Strict: true}).RequiredStake(previousSnapshot.TotalActiveStake)
	if err != nil {
		return false, err
	}
	validatorsByPeerID := make(map[string]consensus.ValidatorState, len(previousSnapshot.Validators))
	localValidatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	var matchingStake uint64
	for _, validator := range previousSnapshot.Validators {
		if validator.ValidatorID == localValidatorID {
			matchingStake = validator.StakeLamports
		}
		if strings.TrimSpace(validator.P2PPeerID) != "" {
			validatorsByPeerID[validator.P2PPeerID] = validator
		}
	}
	if matchingStake >= requiredStake {
		return true, nil
	}
	peerIDs := node.connectedValidatorPeerIDsSnapshot(true)
	if len(peerIDs) == 0 {
		return false, nil
	}
	type statusResult struct {
		peerID string
		status statusResponseEnvelope
		err    error
	}
	workerCount := len(peerIDs)
	if workerCount > 8 {
		workerCount = 8
	}
	jobs := make(chan string)
	results := make(chan statusResult, len(peerIDs))
	for worker := 0; worker < workerCount; worker++ {
		go func() {
			for peerID := range jobs {
				statusContext, cancel := context.WithTimeout(ctx, node.peerStatusTimeout())
				status, requestErr := node.requestStatus(statusContext, peerID)
				cancel()
				results <- statusResult{peerID: peerID, status: status, err: requestErr}
			}
		}()
	}
	for _, peerID := range peerIDs {
		jobs <- peerID
	}
	close(jobs)
	for range peerIDs {
		result := <-results
		if result.err != nil ||
			result.status.FinalizedHeight != transition.SourceBlockHeight ||
			result.status.FinalizedHash != transition.SourceBlockHash {
			continue
		}
		validator, exists := validatorsByPeerID[result.peerID]
		if !exists {
			continue
		}
		if ^uint64(0)-matchingStake < validator.StakeLamports {
			return false, fmt.Errorf("posnode: epoch transition reachable stake overflow")
		}
		matchingStake += validator.StakeLamports
	}
	return matchingStake >= requiredStake, nil
}

func (candidate *epochTransitionCandidate) proof() epochTransitionProof {
	signatures := make([]consensus.EpochTransitionSignature, 0, len(candidate.Signatures))
	for _, signature := range candidate.Signatures {
		signatures = append(signatures, signature)
	}
	certificate := consensus.EpochTransitionCertificate{
		Transition: candidate.Transition,
		Signatures: signatures,
	}
	if len(signatures) > 0 {
		certificate, _ = certificate.Canonical()
	}
	return epochTransitionProof{Certificate: certificate, Snapshot: candidate.Snapshot}
}

func (node *posNode) localEpochTransitionSignature(
	transition consensus.EpochTransition,
	fromSnapshot consensus.EpochSnapshot,
) (consensus.EpochTransitionSignature, bool, error) {
	validatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	if _, active := fromSnapshot.ValidatorByID(validatorID); !active || len(node.consensusKeyPair.PrivateKey) == 0 {
		return consensus.EpochTransitionSignature{}, false, nil
	}
	if persisted, found, err := node.loadLocalEpochTransitionVote(transition.ToEpochID); err != nil {
		return consensus.EpochTransitionSignature{}, false, err
	} else if found {
		if persisted.Transition != transition {
			return consensus.EpochTransitionSignature{}, false, fmt.Errorf("posnode: refusing conflicting epoch transition signature")
		}
		return persisted.Signature, true, nil
	}
	signBytes, err := transition.SignBytes()
	if err != nil {
		return consensus.EpochTransitionSignature{}, false, err
	}
	signature, err := node.consensusKeyPair.Sign(signBytes)
	if err != nil {
		return consensus.EpochTransitionSignature{}, false, err
	}
	item := consensus.EpochTransitionSignature{
		ValidatorID: string(validatorID),
		PublicKey:   node.consensusKeyPair.PublicKey.String(),
		Signature:   signature.String(),
	}
	if err := node.persistLocalEpochTransitionVote(transition, item); err != nil {
		return consensus.EpochTransitionSignature{}, false, err
	}
	return item, true, nil
}

func (node *posNode) handleEpochTransitionMessage(ctx context.Context, message p2p.Message) error {
	incoming, err := unmarshalEpochTransitionProofBinary(message.Payload)
	if err != nil {
		return fmt.Errorf("posnode: decode epoch transition proof: %w", err)
	}
	previousSnapshot, err := node.authenticatedEpochSnapshot(incoming.Certificate.Transition.FromEpochID)
	if err != nil {
		return err
	}
	signedStake, requiredStake, err := node.verifyEpochTransitionProof(previousSnapshot, incoming, false)
	if err != nil {
		return err
	}
	if signedStake < requiredStake {
		if stored, found, loadErr := node.loadEpochTransitionProof(incoming.Certificate.Transition.ToEpochID); loadErr != nil {
			return loadErr
		} else if found {
			if _, _, verifyErr := node.verifyEpochTransitionProof(previousSnapshot, stored, true); verifyErr == nil {
				return nil
			}
		}
		if err := node.requireLocallyDerivedEpochTransition(previousSnapshot, incoming); err != nil {
			return err
		}
	}

	node.mutex.Lock()
	targetEpochID := incoming.Certificate.Transition.ToEpochID
	candidate := node.epochTransitionCandidates[targetEpochID]
	if candidate == nil {
		candidate = &epochTransitionCandidate{
			Transition: incoming.Certificate.Transition,
			Snapshot:   incoming.Snapshot,
			Signatures: make(map[string]consensus.EpochTransitionSignature),
		}
		node.epochTransitionCandidates[targetEpochID] = candidate
	}
	if candidate.Transition != incoming.Certificate.Transition || candidate.Snapshot.SnapshotStateRoot != incoming.Snapshot.SnapshotStateRoot {
		node.mutex.Unlock()
		return fmt.Errorf("posnode: conflicting epoch transition candidate")
	}
	changed := false
	for _, signature := range incoming.Certificate.Signatures {
		if _, exists := candidate.Signatures[signature.ValidatorID]; exists {
			continue
		}
		candidate.Signatures[signature.ValidatorID] = signature
		changed = true
	}
	merged := candidate.proof()
	node.mutex.Unlock()
	if !changed {
		return nil
	}

	mergedStake, mergedRequired, err := node.verifyEpochTransitionProof(previousSnapshot, merged, false)
	if err != nil {
		return err
	}
	if mergedStake >= mergedRequired {
		if err := node.persistEpochTransitionProof(merged); err != nil {
			return err
		}
		currentSlot := node.currentConsensusSlot()
		if err := node.installCertifiedEpochProof(merged, currentSlot); err != nil {
			return err
		}
		// 收到的消息本身已是完整证明时，不再由每个接收节点重复全网广播。
		if signedStake >= requiredStake {
			return nil
		}
	}
	return node.broadcastEpochTransitionProof(ctx, merged, message.FromPeerID)
}

func (node *posNode) requireLocallyDerivedEpochTransition(
	previousSnapshot consensus.EpochSnapshot,
	proof epochTransitionProof,
) error {
	node.mutex.Lock()
	defer node.mutex.Unlock()
	if node.epochSnapshot.SnapshotStateRoot != previousSnapshot.SnapshotStateRoot {
		return fmt.Errorf("posnode: partial epoch proof does not extend the current local epoch")
	}
	head := node.ledger.Head()
	transition := proof.Certificate.Transition
	if transition.SourceBlockHeight != head.FinalizedHeight || transition.SourceBlockHash != head.FinalizedHash.String() {
		return fmt.Errorf("posnode: partial epoch proof finalized checkpoint mismatch")
	}
	targetStartSlot := proof.Snapshot.StartSlot
	expected, err := node.buildEpochSnapshotLocked(transition.ToEpochID, targetStartSlot, node.epochSeed(transition.ToEpochID))
	if err != nil {
		return err
	}
	if expected.SnapshotStateRoot != proof.Snapshot.SnapshotStateRoot {
		return fmt.Errorf("posnode: partial epoch proof target is not derived from finalized state")
	}
	return nil
}

func (node *posNode) verifyEpochTransitionProof(
	previousSnapshot consensus.EpochSnapshot,
	proof epochTransitionProof,
	requireQuorum bool,
) (uint64, uint64, error) {
	certificate := proof.Certificate
	if err := certificate.ValidateStructure(); err != nil {
		return 0, 0, err
	}
	transition := certificate.Transition
	if transition.ChainID != node.config.ChainID ||
		transition.ChainIdentityHash != node.config.ChainIdentityHash ||
		transition.GenesisHash != node.config.GenesisHash {
		return 0, 0, fmt.Errorf("posnode: epoch transition chain identity mismatch")
	}
	if transition.FromEpochID != previousSnapshot.EpochID ||
		transition.FromSnapshotRoot != previousSnapshot.SnapshotStateRoot.String() {
		return 0, 0, fmt.Errorf("posnode: epoch transition does not extend authenticated snapshot")
	}
	canonicalSnapshot, err := canonicalEpochSnapshot(proof.Snapshot)
	if err != nil {
		return 0, 0, err
	}
	if node.config.EpochSlots == 0 ||
		^uint64(0)/node.config.EpochSlots == 0 ||
		transition.ToEpochID >= ^uint64(0)/node.config.EpochSlots {
		return 0, 0, fmt.Errorf("posnode: epoch transition slot range overflows")
	}
	expectedStartSlot := transition.ToEpochID*node.config.EpochSlots + 1
	expectedEndSlot := (transition.ToEpochID + 1) * node.config.EpochSlots
	if canonicalSnapshot.EpochID != transition.ToEpochID ||
		canonicalSnapshot.StartSlot != expectedStartSlot ||
		canonicalSnapshot.EndSlot != expectedEndSlot ||
		canonicalSnapshot.RandomSeed != node.epochSeed(transition.ToEpochID) ||
		transition.ToSnapshotRoot != canonicalSnapshot.SnapshotStateRoot.String() {
		return 0, 0, fmt.Errorf("posnode: epoch transition target snapshot mismatch")
	}
	signBytes, err := transition.SignBytes()
	if err != nil {
		return 0, 0, err
	}
	requiredStake, err := (consensus.Quorum{Numerator: 2, Denominator: 3, Strict: true}).RequiredStake(previousSnapshot.TotalActiveStake)
	if err != nil {
		return 0, 0, err
	}
	var signedStake uint64
	for _, item := range certificate.Signatures {
		validatorID := consensus.ValidatorID(strings.TrimSpace(item.ValidatorID))
		validator, exists := previousSnapshot.ValidatorByID(validatorID)
		if !exists {
			return 0, 0, fmt.Errorf("posnode: epoch transition signer is outside previous epoch")
		}
		publicKey, err := structure.PublicKeyFromBase58(strings.TrimSpace(item.PublicKey))
		if err != nil || publicKey != validator.ConsensusPublicKey {
			return 0, 0, fmt.Errorf("posnode: epoch transition signer public key mismatch")
		}
		signature, err := structure.SignatureFromBase58(strings.TrimSpace(item.Signature))
		if err != nil || !structure.VerifyMessageSignature(publicKey, signBytes, signature) {
			return 0, 0, fmt.Errorf("posnode: invalid epoch transition signature")
		}
		if ^uint64(0)-signedStake < validator.StakeLamports {
			return 0, 0, fmt.Errorf("posnode: epoch transition signed stake overflow")
		}
		signedStake += validator.StakeLamports
	}
	if requireQuorum && signedStake < requiredStake {
		return signedStake, requiredStake, fmt.Errorf("posnode: epoch transition signed stake %d is below required %d", signedStake, requiredStake)
	}
	return signedStake, requiredStake, nil
}

func (node *posNode) broadcastEpochTransitionProof(ctx context.Context, proof epochTransitionProof, excludedPeerID string) error {
	if node.host == nil {
		return nil
	}
	payload, err := marshalEpochTransitionProofBinary(proof)
	if err != nil {
		return err
	}
	message, err := p2p.NewMessage(p2p.ProtocolPoSEpochTransitionV1, payload)
	if err != nil {
		return err
	}
	previousSnapshot, err := node.authenticatedEpochSnapshot(proof.Certificate.Transition.FromEpochID)
	if err != nil {
		return err
	}
	signedStake, requiredStake, err := node.verifyEpochTransitionProof(previousSnapshot, proof, false)
	if err != nil {
		return err
	}
	targets := node.connectedValidatorPeerIDsSnapshot(true)
	if signedStake < requiredStake {
		collectorPeerIDs := rotateLimitedPeerIDs(
			validatorPeerIDsFromSnapshot(previousSnapshot),
			proof.Certificate.Transition.ToEpochID,
			epochTransitionCollectorCount,
		)
		targets = collectorPeerIDs
	}
	targets = removePeerIDs(targets, node.peerKeyPair.peerID, excludedPeerID)
	return node.host.Broadcast(ctx, targets, message)
}

func (node *posNode) handleEpochProofRequest(ctx context.Context, message p2p.Message) (p2p.Message, error) {
	_ = ctx
	request, err := unmarshalEpochProofRequestBinary(message.Payload)
	if err != nil {
		return p2p.Message{}, fmt.Errorf("posnode: decode epoch proof request: %w", err)
	}
	response := epochProofResponse{}
	if request.FromEpochID == ^uint64(0) || request.ToEpochID != request.FromEpochID+1 {
		response.Error = "epoch proof request must advance exactly one epoch"
	} else {
		proof, found, err := node.loadEpochTransitionProof(request.ToEpochID)
		if err != nil {
			response.Error = err.Error()
		} else {
			response.Found = found
			response.Proof = proof
		}
	}
	payload, err := marshalEpochProofResponseBinary(response)
	if err != nil {
		return p2p.Message{}, err
	}
	result, err := p2p.NewResponseMessage(node.peerKeyPair.peerID, p2p.ProtocolPoSEpochProofV1, message.ID, payload)
	if err != nil {
		return p2p.Message{}, err
	}
	result.ToPeerID = message.FromPeerID
	return result, nil
}

func (node *posNode) handleEpochProofBatchRequest(ctx context.Context, message p2p.Message) (p2p.Message, error) {
	_ = ctx
	request, err := unmarshalEpochProofBatchRequestBinary(message.Payload)
	if err != nil {
		return p2p.Message{}, fmt.Errorf("posnode: decode epoch proof batch request: %w", err)
	}
	response := epochProofBatchResponse{
		FromEpochID:   request.FromEpochID,
		TargetEpochID: request.TargetEpochID,
	}
	payloadSize := epochProofBatchResponseFixedSize
	nextEpochID := request.FromEpochID + 1
	for uint32(len(response.Proofs)) < request.MaxProofs {
		proof, found, loadErr := node.loadEpochTransitionProof(nextEpochID)
		if loadErr != nil {
			if len(response.Proofs) == 0 {
				response.Error = loadErr.Error()
			}
			break
		}
		if !found {
			if len(response.Proofs) == 0 {
				response.Error = fmt.Sprintf("missing epoch transition proof %d", nextEpochID)
			}
			break
		}
		proofSize, sizeErr := epochTransitionProofBinarySize(proof)
		if sizeErr != nil {
			if len(response.Proofs) == 0 {
				response.Error = sizeErr.Error()
			}
			break
		}
		if uint64(payloadSize)+uint64(proofSize) > uint64(request.MaxPayloadSize) {
			if len(response.Proofs) == 0 {
				response.Error = "epoch transition proof exceeds requested payload limit"
			}
			break
		}
		response.Proofs = append(response.Proofs, proof)
		payloadSize += proofSize
		if nextEpochID == request.TargetEpochID {
			response.Complete = true
			break
		}
		nextEpochID++
	}
	payload, err := marshalEpochProofBatchResponseBinary(response)
	if err != nil {
		return p2p.Message{}, err
	}
	result, err := p2p.NewResponseMessage(node.peerKeyPair.peerID, p2p.ProtocolPoSEpochProofBatchV1, message.ID, payload)
	if err != nil {
		return p2p.Message{}, err
	}
	result.ToPeerID = message.FromPeerID
	return result, nil
}

func (node *posNode) requestEpochTransitionProof(
	ctx context.Context,
	peerID string,
	fromEpochID uint64,
) (epochTransitionProof, bool, error) {
	if fromEpochID == ^uint64(0) {
		return epochTransitionProof{}, false, fmt.Errorf("posnode: epoch proof source epoch overflows")
	}
	payload, err := marshalEpochProofRequestBinary(epochProofRequest{FromEpochID: fromEpochID, ToEpochID: fromEpochID + 1})
	if err != nil {
		return epochTransitionProof{}, false, err
	}
	request, err := p2p.NewRequestMessage(node.peerKeyPair.peerID, p2p.ProtocolPoSEpochProofV1, payload)
	if err != nil {
		return epochTransitionProof{}, false, err
	}
	responseMessage, err := node.host.Request(ctx, peerID, request)
	if err != nil {
		return epochTransitionProof{}, false, err
	}
	response, err := unmarshalEpochProofResponseBinary(responseMessage.Payload)
	if err != nil {
		return epochTransitionProof{}, false, err
	}
	if response.Error != "" {
		return epochTransitionProof{}, false, fmt.Errorf("posnode: peer epoch proof response: %s", response.Error)
	}
	return response.Proof, response.Found, nil
}

func (node *posNode) requestEpochTransitionProofBatch(
	ctx context.Context,
	peerID string,
	fromEpochID uint64,
	targetEpochID uint64,
) (epochProofBatchResponse, error) {
	payload, err := marshalEpochProofBatchRequestBinary(epochProofBatchRequest{
		FromEpochID:    fromEpochID,
		TargetEpochID:  targetEpochID,
		MaxProofs:      defaultEpochProofBatchSize,
		MaxPayloadSize: defaultEpochProofBatchPayloadBytes,
	})
	if err != nil {
		return epochProofBatchResponse{}, err
	}
	request, err := p2p.NewRequestMessage(node.peerKeyPair.peerID, p2p.ProtocolPoSEpochProofBatchV1, payload)
	if err != nil {
		return epochProofBatchResponse{}, err
	}
	responseMessage, err := node.host.Request(ctx, peerID, request)
	if err != nil {
		return epochProofBatchResponse{}, err
	}
	response, err := unmarshalEpochProofBatchResponseBinary(responseMessage.Payload)
	if err != nil {
		return epochProofBatchResponse{}, err
	}
	if response.FromEpochID != fromEpochID || response.TargetEpochID != targetEpochID {
		return epochProofBatchResponse{}, fmt.Errorf("posnode: peer epoch proof batch range mismatch")
	}
	if response.Error != "" {
		return epochProofBatchResponse{}, fmt.Errorf("posnode: peer epoch proof batch response: %s", response.Error)
	}
	return response, nil
}

// syncEpochProofChainFromPeer 从创世可信集合逐代验证到目标 epoch，不信任远端自报的节点角色。
func (node *posNode) syncEpochProofChainFromPeer(ctx context.Context, peerID string, targetEpochID uint64) error {
	if node.config.EpochSlots == 0 ||
		^uint64(0)/node.config.EpochSlots == 0 ||
		targetEpochID >= ^uint64(0)/node.config.EpochSlots {
		return fmt.Errorf("posnode: target epoch slot range overflows")
	}
	previousSnapshot, err := node.trustedGenesisEpochSnapshot()
	if err != nil {
		return err
	}
	activations, err := node.loadActivationProofs()
	if err != nil {
		return err
	}
	authenticated := make(map[uint64]consensus.EpochSnapshot)
	previousSnapshot, err = node.applyAuthenticatedActivations(previousSnapshot, activations, authenticated, nil)
	if err != nil {
		return err
	}
	fetchedProofCount := 0
	batchRequestCount := 0
	fallbackRequestCount := 0
	nextEpochID := uint64(1)
	for nextEpochID <= targetEpochID {
		proof, found, loadErr := node.loadEpochTransitionProof(nextEpochID)
		if loadErr != nil {
			return loadErr
		}
		if !found {
			break
		}
		if _, _, verifyErr := node.verifyEpochTransitionProof(previousSnapshot, proof, true); verifyErr != nil {
			break
		}
		previousSnapshot, err = node.applyAuthenticatedActivations(proof.Snapshot, activations, authenticated, nil)
		if err != nil {
			return err
		}
		nextEpochID++
	}

	batchSupported := true
	for nextEpochID <= targetEpochID {
		var proofs []epochTransitionProof
		if batchSupported {
			response, batchErr := node.requestEpochTransitionProofBatch(ctx, peerID, nextEpochID-1, targetEpochID)
			batchRequestCount++
			if batchErr == nil {
				proofs = response.Proofs
			} else {
				batchSupported = false
			}
		}
		if len(proofs) == 0 {
			proof, found, requestErr := node.requestEpochTransitionProof(ctx, peerID, nextEpochID-1)
			fallbackRequestCount++
			if requestErr != nil {
				return requestErr
			}
			if !found {
				return fmt.Errorf("posnode: peer %s missing epoch transition proof %d", peerID, nextEpochID)
			}
			proofs = []epochTransitionProof{proof}
		}

		verifiedSnapshot := previousSnapshot
		for index, proof := range proofs {
			expectedEpochID := nextEpochID + uint64(index)
			if proof.Certificate.Transition.ToEpochID != expectedEpochID {
				return fmt.Errorf("posnode: peer epoch proof batch expected epoch %d, got %d", expectedEpochID, proof.Certificate.Transition.ToEpochID)
			}
			if _, _, verifyErr := node.verifyEpochTransitionProof(verifiedSnapshot, proof, true); verifyErr != nil {
				return fmt.Errorf("posnode: verify peer epoch transition proof %d: %w", expectedEpochID, verifyErr)
			}
			verifiedSnapshot, err = node.applyAuthenticatedActivations(proof.Snapshot, activations, authenticated, nil)
			if err != nil {
				return err
			}
		}
		if err := node.persistEpochTransitionProofBatch(proofs); err != nil {
			return err
		}
		node.mutex.Lock()
		for _, proof := range proofs {
			epochID := proof.Certificate.Transition.ToEpochID
			node.epochSnapshots[epochID] = proof.Snapshot
			delete(node.leaderSchedules, epochID)
		}
		node.mutex.Unlock()
		previousSnapshot = verifiedSnapshot
		nextEpochID += uint64(len(proofs))
		fetchedProofCount += len(proofs)
	}
	if fetchedProofCount > 0 {
		node.logger.Info("posnode epoch proof chain synced",
			slog.String("peer_id", peerID),
			slog.Uint64("target_epoch", targetEpochID),
			slog.Int("proof_count", fetchedProofCount),
			slog.Int("batch_requests", batchRequestCount),
			slog.Int("fallback_requests", fallbackRequestCount),
		)
	}
	return nil
}

func (node *posNode) epochSnapshotAuthenticated(snapshot consensus.EpochSnapshot) (bool, error) {
	authenticated, err := node.authenticatedEpochSnapshot(snapshot.EpochID)
	if err != nil {
		return false, err
	}
	return authenticated.SnapshotStateRoot == snapshot.SnapshotStateRoot, nil
}

// ensureLocalValidatorCurrentState 将签名权和完整当前状态绑定；归档历史不参与该资格判断。
func (node *posNode) ensureLocalValidatorCurrentState(snapshot consensus.EpochSnapshot) error {
	authenticated, err := node.epochSnapshotAuthenticated(snapshot)
	if err != nil {
		return err
	}
	if !authenticated {
		return fmt.Errorf("posnode: local epoch snapshot is not authenticated")
	}
	validatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	validator, exists := snapshot.ValidatorByID(validatorID)
	if !exists || validator.ConsensusPublicKey != node.consensusKeyPair.PublicKey || validator.StakeLamports == 0 {
		return fmt.Errorf("posnode: local validator is not active in authenticated epoch")
	}
	if node.ledger == nil {
		return fmt.Errorf("posnode: local validator ledger is unavailable")
	}
	head := node.ledger.Head()
	if _, err := node.ledger.StateAtBlockHash(head.BlockHash); err != nil {
		return fmt.Errorf("posnode: local validator current state is unavailable: %w", err)
	}
	return nil
}

func (node *posNode) authenticatedEpochSnapshot(epochID uint64) (consensus.EpochSnapshot, error) {
	return node.authenticatedSnapshotThroughSlot(epochID, nil)
}

func (node *posNode) authenticatedEpochSnapshotForSlot(slot uint64) (consensus.EpochSnapshot, error) {
	epochID, _ := node.epochForSlot(slot)
	snapshot, err := node.authenticatedSnapshotThroughSlot(epochID, &slot)
	if err != nil {
		return consensus.EpochSnapshot{}, err
	}
	if slot < snapshot.StartSlot || slot > snapshot.EndSlot {
		return consensus.EpochSnapshot{}, fmt.Errorf("posnode: vote slot is outside authenticated epoch")
	}
	return snapshot, nil
}

func (node *posNode) trustedGenesisEpochSnapshot() (consensus.EpochSnapshot, error) {
	// 未启动的单元测试节点没有完整配置；生产节点 runtimeContext 非空，始终从创世配置重建信任根。
	if node.runtimeContext == nil && node.epochSnapshot.EpochID == 0 && len(node.epochSnapshot.Validators) > 0 {
		return canonicalEpochSnapshot(node.epochSnapshot)
	}
	genesisConfig, err := node.blockchainGenesisConfig()
	if err != nil {
		return consensus.EpochSnapshot{}, err
	}
	state, _, err := blockchain.BuildGenesisState(genesisConfig)
	if err != nil {
		return consensus.EpochSnapshot{}, err
	}
	validatorSet, err := blockchain.ValidatorSetFromStateAtEpoch(state, 0)
	if err != nil {
		return consensus.EpochSnapshot{}, err
	}
	return consensus.NewEpochSnapshot(0, 1, node.config.EpochSlots, node.epochSeed(0), validatorSet)
}

func (node *posNode) installCertifiedEpochProof(proof epochTransitionProof, currentSlot uint64) error {
	if err := node.replaceEpochSnapshot(proof.Snapshot); err != nil {
		return err
	}
	node.mutex.Lock()
	defer node.mutex.Unlock()
	node.epochSnapshots[proof.Snapshot.EpochID] = proof.Snapshot
	delete(node.leaderSchedules, proof.Snapshot.EpochID)
	epochID, _ := node.epochForSlot(currentSlot)
	if epochID != proof.Snapshot.EpochID {
		return nil
	}
	if node.epochSnapshot.EpochID == proof.Snapshot.EpochID &&
		node.epochSnapshot.SnapshotStateRoot == proof.Snapshot.SnapshotStateRoot {
		return nil
	}
	return node.installEpochSnapshotLocked(proof.Snapshot)
}

func (node *posNode) persistEpochTransitionProof(proof epochTransitionProof) error {
	if node.db == nil {
		return nil
	}
	data, err := json.Marshal(persistedEpochTransition{Version: persistedEpochTransitionVersion, Proof: proof})
	if err != nil {
		return err
	}
	key := epochTransitionStateKey(proof.Certificate.Transition.ToEpochID)
	existing, err := node.db.Get(database.TableCheckpoint, key)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		stored, err := decodePersistedEpochTransition(existing)
		if err != nil {
			return err
		}
		if stored.Certificate.Transition != proof.Certificate.Transition || stored.Snapshot.SnapshotStateRoot != proof.Snapshot.SnapshotStateRoot {
			return fmt.Errorf("posnode: refusing to overwrite certified epoch transition")
		}
		return nil
	}
	if err := node.db.Put(database.TableCheckpoint, key, data); err != nil {
		return err
	}
	return node.db.Flush()
}

func (node *posNode) persistEpochTransitionProofBatch(proofs []epochTransitionProof) error {
	if len(proofs) == 0 {
		return fmt.Errorf("posnode: epoch transition proof batch is empty")
	}
	if len(proofs) > maxEpochProofBatchSize {
		return fmt.Errorf("posnode: epoch transition proof batch exceeds limit")
	}
	if node.db == nil {
		return nil
	}
	operations := make([]database.DBOperation, 0, len(proofs)*2)
	seenEpochIDs := make(map[uint64]struct{}, len(proofs))
	for _, proof := range proofs {
		toEpochID := proof.Certificate.Transition.ToEpochID
		if _, exists := seenEpochIDs[toEpochID]; exists {
			return fmt.Errorf("posnode: duplicate epoch transition proof %d", toEpochID)
		}
		seenEpochIDs[toEpochID] = struct{}{}
		data, err := json.Marshal(persistedEpochTransition{Version: persistedEpochTransitionVersion, Proof: proof})
		if err != nil {
			return err
		}
		key := epochTransitionStateKey(toEpochID)
		existing, err := node.db.Get(database.TableCheckpoint, key)
		if err != nil {
			return err
		}
		if len(existing) > 0 {
			stored, err := decodePersistedEpochTransition(existing)
			if err != nil {
				return err
			}
			if stored.Certificate.Transition != proof.Certificate.Transition || stored.Snapshot.SnapshotStateRoot != proof.Snapshot.SnapshotStateRoot {
				return fmt.Errorf("posnode: refusing to overwrite certified epoch transition")
			}
		}
		operations = append(operations, database.NewUpdateOperation(database.TableCheckpoint, key, data))

		snapshotOperation, required, err := node.epochSnapshotWriteOperation(proof.Snapshot, true)
		if err != nil {
			return err
		}
		if required {
			operations = append(operations, snapshotOperation)
		}
	}
	if err := node.db.DataTransaction(operations); err != nil {
		return fmt.Errorf("posnode: persist epoch transition proof batch: %w", err)
	}
	return node.db.Flush()
}

func (node *posNode) loadEpochTransitionProof(toEpochID uint64) (epochTransitionProof, bool, error) {
	if node.db == nil {
		return epochTransitionProof{}, false, nil
	}
	data, err := node.db.Get(database.TableCheckpoint, epochTransitionStateKey(toEpochID))
	if err != nil {
		return epochTransitionProof{}, false, err
	}
	if len(data) == 0 {
		return epochTransitionProof{}, false, nil
	}
	proof, err := decodePersistedEpochTransition(data)
	if err != nil {
		return epochTransitionProof{}, false, err
	}
	return proof, true, nil
}

func decodePersistedEpochTransition(data []byte) (epochTransitionProof, error) {
	var persisted persistedEpochTransition
	if err := json.Unmarshal(data, &persisted); err != nil {
		return epochTransitionProof{}, err
	}
	if persisted.Version != persistedEpochTransitionVersion {
		return epochTransitionProof{}, fmt.Errorf("posnode: unsupported epoch transition version %d", persisted.Version)
	}
	return persisted.Proof, nil
}

func (node *posNode) persistLocalEpochTransitionVote(
	transition consensus.EpochTransition,
	signature consensus.EpochTransitionSignature,
) error {
	if node.db == nil {
		return nil
	}
	data, err := json.Marshal(persistedLocalEpochTransitionVote{
		Version:    persistedEpochTransitionVersion,
		Transition: transition,
		Signature:  signature,
	})
	if err != nil {
		return err
	}
	key := localEpochTransitionVoteStateKey(transition.ToEpochID)
	existing, err := node.db.Get(database.TableCheckpoint, key)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		var stored persistedLocalEpochTransitionVote
		if err := json.Unmarshal(existing, &stored); err != nil {
			return err
		}
		if stored.Transition != transition || stored.Signature != signature {
			return fmt.Errorf("posnode: refusing to overwrite local epoch transition vote")
		}
		return nil
	}
	if err := node.db.Put(database.TableCheckpoint, key, data); err != nil {
		return err
	}
	return node.db.Flush()
}

func (node *posNode) loadLocalEpochTransitionVote(toEpochID uint64) (persistedLocalEpochTransitionVote, bool, error) {
	if node.db == nil {
		return persistedLocalEpochTransitionVote{}, false, nil
	}
	data, err := node.db.Get(database.TableCheckpoint, localEpochTransitionVoteStateKey(toEpochID))
	if err != nil {
		return persistedLocalEpochTransitionVote{}, false, err
	}
	if len(data) == 0 {
		return persistedLocalEpochTransitionVote{}, false, nil
	}
	var stored persistedLocalEpochTransitionVote
	if err := json.Unmarshal(data, &stored); err != nil {
		return persistedLocalEpochTransitionVote{}, false, err
	}
	if stored.Version != persistedEpochTransitionVersion {
		return persistedLocalEpochTransitionVote{}, false, fmt.Errorf("posnode: unsupported local epoch transition vote version")
	}
	return stored, true, nil
}
