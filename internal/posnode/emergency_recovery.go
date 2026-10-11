package posnode

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/tentechtop/ordexus-core/blockchain"
	"github.com/tentechtop/ordexus-core/consensus"
	"github.com/tentechtop/ordexus-core/database"
	"github.com/tentechtop/ordexus-core/p2p"
	"github.com/tentechtop/ordexus-core/structure"
)

var (
	emergencyRecoveryCertificateKey = []byte("posnode:consensus:emergency-recovery:v2")
	multiValidatorActivationKey     = []byte("posnode:consensus:multi-validator-activation:v1")
)

// SubmitEmergencyRecovery 接受最后安全委员会严格超过 2/3 权益签名的长老恢复证书。
func (node *posNode) SubmitEmergencyRecovery(ctx context.Context, encodedCertificate string) (string, error) {
	if !node.config.EmergencyRecovery.Enabled {
		return "", fmt.Errorf("posnode: emergency recovery is disabled")
	}
	node.recoveryApplyMutex.Lock()
	defer node.recoveryApplyMutex.Unlock()

	certificateBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedCertificate))
	if err != nil {
		return "", fmt.Errorf("posnode: decode emergency recovery certificate: %w", err)
	}
	var certificate consensus.EmergencyRecoveryCertificate
	if err := json.Unmarshal(certificateBytes, &certificate); err != nil {
		return "", fmt.Errorf("posnode: parse emergency recovery certificate: %w", err)
	}
	wireBytes, err := marshalEmergencyRecoveryCertificateBinary(certificate)
	if err != nil {
		return "", fmt.Errorf("posnode: encode emergency recovery certificate: %w", err)
	}
	elder, err := node.validateEmergencyRecoveryCertificate(certificate)
	if err != nil {
		return "", err
	}
	if err := node.activateEmergencyRecovery(certificate.Sequence, elder); err != nil {
		return "", err
	}
	if err := node.persistConsensusCertificate(emergencyRecoveryCertificateKey, certificateBytes); err != nil {
		return "", err
	}
	node.broadcastConsensusCertificate(ctx, p2p.ProtocolPoSEmergencyRecoveryV1, wireBytes)
	node.logger.Warn("posnode emergency elder activated",
		"sequence", certificate.Sequence,
		"validator_id", string(elder.ValidatorID),
		"stake", elder.StakeLamports,
		"finalized_height", certificate.FinalizedHeight,
	)
	return string(elder.ValidatorID), nil
}

// SubmitMultiValidatorActivation 通过安全委员会证书确认临时分支，并恢复多验证者最终性。
func (node *posNode) SubmitMultiValidatorActivation(ctx context.Context, encodedCertificate string) (string, error) {
	if !node.config.EmergencyRecovery.Enabled {
		return "", fmt.Errorf("posnode: emergency recovery is disabled")
	}
	node.recoveryApplyMutex.Lock()
	defer node.recoveryApplyMutex.Unlock()

	certificateBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedCertificate))
	if err != nil {
		return "", fmt.Errorf("posnode: decode multi-validator activation certificate: %w", err)
	}
	var certificate consensus.MultiValidatorActivationCertificate
	if err := json.Unmarshal(certificateBytes, &certificate); err != nil {
		return "", fmt.Errorf("posnode: parse multi-validator activation certificate: %w", err)
	}
	wireBytes, err := marshalMultiValidatorActivationCertificateBinary(certificate)
	if err != nil {
		return "", fmt.Errorf("posnode: encode multi-validator activation certificate: %w", err)
	}
	targetSet, targetHash, err := node.validateMultiValidatorActivationCertificate(certificate)
	if err != nil {
		return "", err
	}
	if _, err := node.ledger.AdvanceFinalizedCheckpoint(certificate.TargetHeight, targetHash); err != nil {
		return "", fmt.Errorf("posnode: ratify activation checkpoint: %w", err)
	}
	if err := node.persistActivationProof(certificate, certificateBytes); err != nil {
		return "", err
	}
	if err := node.activateMultiValidatorMode(certificate, targetSet); err != nil {
		return "", err
	}
	node.broadcastConsensusCertificate(ctx, p2p.ProtocolPoSMultiValidatorActivationV1, wireBytes)
	node.logger.Warn("posnode multi-validator consensus activated",
		"sequence", certificate.Sequence,
		"target_height", certificate.TargetHeight,
		"target_hash", certificate.TargetHash,
		"validators", len(targetSet.Validators()),
	)
	return certificate.TargetHash, nil
}

func (node *posNode) validateEmergencyRecoveryCertificate(certificate consensus.EmergencyRecoveryCertificate) (consensus.ValidatorState, error) {
	if certificate.ChainID != node.config.ChainID {
		return consensus.ValidatorState{}, fmt.Errorf("posnode: emergency recovery chain id mismatch")
	}
	if certificate.ExpiresAtUnixMilli <= time.Now().UnixMilli() {
		return consensus.ValidatorState{}, fmt.Errorf("posnode: emergency recovery certificate expired")
	}
	node.mutex.Lock()
	sequence := node.recoverySequence
	safety := node.safetyCheckpoint
	node.mutex.Unlock()
	if certificate.Sequence <= sequence {
		return consensus.ValidatorState{}, fmt.Errorf("posnode: emergency recovery sequence was already applied")
	}
	if certificate.FinalizedHeight != safety.FinalizedHeight ||
		strings.TrimSpace(certificate.FinalizedHash) != safety.FinalizedHash ||
		certificate.SafetyEpochID != safety.EpochID ||
		strings.TrimSpace(certificate.SafetyValidatorSetRoot) != safety.ValidatorSetRoot {
		return consensus.ValidatorState{}, fmt.Errorf("posnode: emergency recovery certificate does not bind the safety checkpoint")
	}
	signBytes, err := certificate.SignBytes()
	if err != nil {
		return consensus.ValidatorState{}, err
	}
	safetySet, err := node.safetyValidatorSet()
	if err != nil {
		return consensus.ValidatorState{}, err
	}
	if err := verifyRecoveryStakeSignatures(signBytes, certificate.Signatures, safetySet); err != nil {
		return consensus.ValidatorState{}, err
	}
	excluded := make(map[consensus.ValidatorID]struct{}, len(certificate.ExcludedValidatorIDs))
	known := make(map[consensus.ValidatorID]struct{}, len(safetySet.Validators()))
	for _, validator := range safetySet.Validators() {
		known[validator.ValidatorID] = struct{}{}
	}
	for _, value := range certificate.ExcludedValidatorIDs {
		validatorID := consensus.ValidatorID(strings.TrimSpace(value))
		if _, exists := known[validatorID]; !exists {
			return consensus.ValidatorState{}, fmt.Errorf("posnode: excluded validator is not in the safety committee")
		}
		excluded[validatorID] = struct{}{}
	}
	anchor, err := structure.HashFromBase58(safety.FinalizedHash)
	if err != nil {
		return consensus.ValidatorState{}, err
	}
	elder, err := consensus.SelectElder(safetySet, anchor, excluded)
	if err != nil {
		return consensus.ValidatorState{}, err
	}
	if string(elder.ValidatorID) != strings.TrimSpace(certificate.ElderValidatorID) {
		return consensus.ValidatorState{}, fmt.Errorf("posnode: recovery elder is not the deterministic highest-weight candidate")
	}
	return elder, nil
}

func (node *posNode) validateMultiValidatorActivationCertificate(certificate consensus.MultiValidatorActivationCertificate) (consensus.ValidatorSet, structure.Hash, error) {
	if certificate.ChainID != node.config.ChainID {
		return consensus.ValidatorSet{}, structure.Hash{}, fmt.Errorf("posnode: activation chain id mismatch")
	}
	if certificate.ExpiresAtUnixMilli <= time.Now().UnixMilli() {
		return consensus.ValidatorSet{}, structure.Hash{}, fmt.Errorf("posnode: activation certificate expired")
	}
	node.mutex.Lock()
	mode := node.consensusMode
	sequence := node.recoverySequence
	safety := node.safetyCheckpoint
	node.mutex.Unlock()
	if mode != consensus.NetworkModeRecoveryElder {
		return consensus.ValidatorSet{}, structure.Hash{}, fmt.Errorf("posnode: activation requires recovery elder mode")
	}
	if certificate.Sequence <= sequence {
		return consensus.ValidatorSet{}, structure.Hash{}, fmt.Errorf("posnode: activation sequence was already applied")
	}
	if certificate.SafetyFinalizedHeight != safety.FinalizedHeight ||
		strings.TrimSpace(certificate.SafetyFinalizedHash) != safety.FinalizedHash ||
		certificate.SafetyEpochID != safety.EpochID ||
		strings.TrimSpace(certificate.SafetyValidatorSetRoot) != safety.ValidatorSetRoot {
		return consensus.ValidatorSet{}, structure.Hash{}, fmt.Errorf("posnode: activation certificate does not bind the safety checkpoint")
	}
	head := node.ledger.Head()
	targetHash, err := structure.HashFromBase58(strings.TrimSpace(certificate.TargetHash))
	if err != nil {
		return consensus.ValidatorSet{}, structure.Hash{}, fmt.Errorf("posnode: decode activation target hash: %w", err)
	}
	if certificate.TargetHeight != head.Height || targetHash != head.BlockHash {
		return consensus.ValidatorSet{}, structure.Hash{}, fmt.Errorf("posnode: activation certificate does not bind the current provisional head")
	}
	node.mutex.Lock()
	expectedEpochID, _ := node.epochForSlot(node.currentConsensusSlotLocked(head))
	node.mutex.Unlock()
	if certificate.TargetEpochID != expectedEpochID {
		return consensus.ValidatorSet{}, structure.Hash{}, fmt.Errorf("posnode: activation target epoch does not match next consensus slot")
	}
	state, err := node.ledger.StateAtBlockHash(targetHash)
	if err != nil {
		return consensus.ValidatorSet{}, structure.Hash{}, err
	}
	targetSet, err := blockchain.ValidatorSetFromStateAtEpoch(state, certificate.TargetEpochID)
	if err != nil {
		return consensus.ValidatorSet{}, structure.Hash{}, err
	}
	if len(targetSet.Validators()) < node.config.ConsensusPolicy.MultiValidatorThreshold {
		return consensus.ValidatorSet{}, structure.Hash{}, fmt.Errorf("posnode: activation target has insufficient validators")
	}
	targetRoot, err := consensus.ValidatorSetRoot(targetSet)
	if err != nil {
		return consensus.ValidatorSet{}, structure.Hash{}, err
	}
	if targetRoot.String() != strings.TrimSpace(certificate.TargetValidatorSetRoot) {
		return consensus.ValidatorSet{}, structure.Hash{}, fmt.Errorf("posnode: activation target validator set root mismatch")
	}
	signBytes, err := certificate.SignBytes()
	if err != nil {
		return consensus.ValidatorSet{}, structure.Hash{}, err
	}
	safetySet, err := node.safetyValidatorSet()
	if err != nil {
		return consensus.ValidatorSet{}, structure.Hash{}, err
	}
	if err := verifyRecoveryStakeSignatures(signBytes, certificate.Signatures, safetySet); err != nil {
		return consensus.ValidatorSet{}, structure.Hash{}, err
	}
	return targetSet, targetHash, nil
}

func verifyRecoveryStakeSignatures(signBytes []byte, signatures []consensus.EmergencyRecoverySignature, validatorSet consensus.ValidatorSet) error {
	validators := make(map[consensus.ValidatorID]consensus.ValidatorState, len(validatorSet.Validators()))
	var totalStake uint64
	for _, validator := range validatorSet.Validators() {
		validators[validator.ValidatorID] = validator
		if ^uint64(0)-totalStake < validator.StakeLamports {
			return fmt.Errorf("posnode: safety validator stake overflow")
		}
		totalStake += validator.StakeLamports
	}
	requiredStake, err := (consensus.Quorum{Numerator: 2, Denominator: 3, Strict: true}).RequiredStake(totalStake)
	if err != nil {
		return err
	}
	verified := make(map[consensus.ValidatorID]struct{}, len(signatures))
	var signedStake uint64
	for _, item := range signatures {
		validatorID := consensus.ValidatorID(strings.TrimSpace(item.ValidatorID))
		validator, exists := validators[validatorID]
		if !exists {
			return fmt.Errorf("posnode: recovery signature uses validator outside the safety committee")
		}
		if _, duplicate := verified[validatorID]; duplicate {
			return fmt.Errorf("posnode: duplicate recovery validator signature")
		}
		publicKey, err := structure.PublicKeyFromBase58(strings.TrimSpace(item.PublicKey))
		if err != nil || publicKey != validator.ConsensusPublicKey {
			return fmt.Errorf("posnode: recovery signature public key mismatch")
		}
		signature, err := structure.SignatureFromBase58(strings.TrimSpace(item.Signature))
		if err != nil || !structure.VerifyMessageSignature(publicKey, signBytes, signature) {
			return fmt.Errorf("posnode: invalid recovery validator signature")
		}
		if ^uint64(0)-signedStake < validator.StakeLamports {
			return fmt.Errorf("posnode: signed recovery stake overflow")
		}
		signedStake += validator.StakeLamports
		verified[validatorID] = struct{}{}
	}
	if signedStake < requiredStake {
		return fmt.Errorf("posnode: recovery signed stake %d is below required %d", signedStake, requiredStake)
	}
	return nil
}

func (node *posNode) activateEmergencyRecovery(sequence uint64, elder consensus.ValidatorState) error {
	node.mutex.Lock()
	previousMode := node.consensusMode
	previousValidator := node.activeRecoveryValidator
	previousValidatorID := node.recoveryElderValidatorID
	previousSequence := node.recoverySequence
	previousActivationSlot := node.recoveryActivationSlot
	previousVoteCollectors := node.voteCollectors
	previousEpochSnapshots := node.epochSnapshots
	previousLeaderSchedules := node.leaderSchedules
	previousEpochSnapshot := node.epochSnapshot
	previousLeaderSchedule := node.leaderSchedule
	previousVoteCollector := node.voteCollector

	head := node.ledger.Head()
	node.consensusMode = consensus.NetworkModeRecoveryElder
	node.activeRecoveryValidator = &elder
	node.recoveryElderValidatorID = elder.ValidatorID
	node.recoverySequence = sequence
	node.recoveryActivationSlot = head.Slot + 1
	node.voteCollectors = make(map[uint64]*consensus.VoteCollector)
	node.epochSnapshots = make(map[uint64]consensus.EpochSnapshot)
	node.leaderSchedules = make(map[uint64]consensus.LeaderSchedule)
	node.ledger.SetFinalitySuspended(true)
	epochID, startSlot := node.epochForSlot(node.currentConsensusSlotLocked(head))
	err := node.replaceEpochLocked(epochID, startSlot, node.epochSeed(epochID))
	if err == nil {
		err = node.persistConsensusModeStateLocked()
	}
	if err != nil {
		node.consensusMode = previousMode
		node.activeRecoveryValidator = previousValidator
		node.recoveryElderValidatorID = previousValidatorID
		node.recoverySequence = previousSequence
		node.recoveryActivationSlot = previousActivationSlot
		node.voteCollectors = previousVoteCollectors
		node.epochSnapshots = previousEpochSnapshots
		node.leaderSchedules = previousLeaderSchedules
		node.epochSnapshot = previousEpochSnapshot
		node.leaderSchedule = previousLeaderSchedule
		node.voteCollector = previousVoteCollector
		node.ledger.SetFinalitySuspended(previousMode == consensus.NetworkModeRecoveryElder)
	}
	node.mutex.Unlock()
	if err != nil {
		return fmt.Errorf("posnode: activate emergency recovery: %w", err)
	}
	return nil
}

func (node *posNode) activateMultiValidatorMode(certificate consensus.MultiValidatorActivationCertificate, targetSet consensus.ValidatorSet) error {
	node.mutex.Lock()
	defer node.mutex.Unlock()
	node.consensusMode = consensus.NetworkModeMultiValidator
	node.multiValidatorActivated = true
	node.recoverySequence = certificate.Sequence
	node.activeRecoveryValidator = nil
	node.recoveryElderValidatorID = ""
	node.recoveryActivationSlot = 0
	node.safetyCheckpoint = consensusSafetyCheckpoint{
		FinalizedHeight:  certificate.TargetHeight,
		FinalizedHash:    certificate.TargetHash,
		EpochID:          certificate.TargetEpochID,
		ValidatorSetRoot: certificate.TargetValidatorSetRoot,
	}
	node.voteCollectors = make(map[uint64]*consensus.VoteCollector)
	node.epochSnapshots = make(map[uint64]consensus.EpochSnapshot)
	node.leaderSchedules = make(map[uint64]consensus.LeaderSchedule)
	node.ledger.SetFinalitySuspended(false)
	node.latestTimeoutQC = nil
	head := node.ledger.Head()
	epochID, startSlot := node.epochForSlot(node.currentConsensusSlotLocked(head))
	if len(targetSet.Validators()) < node.config.ConsensusPolicy.MultiValidatorThreshold {
		return fmt.Errorf("posnode: activation target no longer satisfies validator threshold")
	}
	snapshot, err := consensus.NewEpochSnapshot(epochID, startSlot, node.config.EpochSlots, node.epochSeed(epochID), targetSet)
	if err != nil {
		return err
	}
	if err := node.replaceEpochSnapshot(snapshot); err != nil {
		return err
	}
	if err := node.installEpochSnapshotLocked(snapshot); err != nil {
		return err
	}
	return node.persistConsensusModeStateLocked()
}

func (node *posNode) persistConsensusCertificate(key []byte, data []byte) error {
	if node.db == nil {
		return nil
	}
	if err := node.db.Put(database.TableCheckpoint, key, data); err != nil {
		return fmt.Errorf("posnode: persist consensus certificate: %w", err)
	}
	return nil
}

func (node *posNode) handleEmergencyRecoveryMessage(ctx context.Context, message p2p.Message) error {
	certificate, err := unmarshalEmergencyRecoveryCertificateBinary(message.Payload)
	if err != nil {
		return fmt.Errorf("posnode: decode propagated recovery certificate: %w", err)
	}
	node.mutex.Lock()
	alreadyApplied := certificate.Sequence <= node.recoverySequence
	node.mutex.Unlock()
	if alreadyApplied {
		return nil
	}
	certificateBytes, err := json.Marshal(certificate)
	if err != nil {
		return fmt.Errorf("posnode: marshal propagated recovery certificate: %w", err)
	}
	_, err = node.SubmitEmergencyRecovery(ctx, base64.StdEncoding.EncodeToString(certificateBytes))
	return err
}

func (node *posNode) handleMultiValidatorActivationMessage(ctx context.Context, message p2p.Message) error {
	certificate, err := unmarshalMultiValidatorActivationCertificateBinary(message.Payload)
	if err != nil {
		return fmt.Errorf("posnode: decode propagated activation certificate: %w", err)
	}
	node.mutex.Lock()
	alreadyApplied := certificate.Sequence <= node.recoverySequence
	node.mutex.Unlock()
	if alreadyApplied {
		return nil
	}
	certificateBytes, err := json.Marshal(certificate)
	if err != nil {
		return fmt.Errorf("posnode: marshal propagated activation certificate: %w", err)
	}
	_, err = node.SubmitMultiValidatorActivation(ctx, base64.StdEncoding.EncodeToString(certificateBytes))
	return err
}

func (node *posNode) broadcastConsensusCertificate(ctx context.Context, protocolID p2p.ProtocolID, data []byte) {
	if node.host == nil {
		return
	}
	message, err := p2p.NewMessage(protocolID, data)
	if err != nil {
		node.logger.Warn("posnode encode consensus certificate failed", "protocol", protocolID, "error", err)
		return
	}
	peerIDs := node.syncPeerIDsSnapshot()
	if len(peerIDs) == 0 {
		return
	}
	if err := node.host.Broadcast(ctx, peerIDs, message); err != nil {
		node.logger.Warn("posnode broadcast consensus certificate failed", "protocol", protocolID, "error", err)
	}
}
