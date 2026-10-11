package posnode

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/tentechtop/ordexus-core/blockchain"
	"github.com/tentechtop/ordexus-core/consensus"
	"github.com/tentechtop/ordexus-core/database"
)

var (
	timeoutQCStateKey                 = []byte("posnode:consensus:latest-timeout-qc:v1")
	localProposalStatePrefix          = []byte("posnode:consensus:local-proposal:v1/")
	localProposalWatermarkStateKey    = []byte("posnode:consensus:local-proposal-watermark:v1")
	localVoteEnvelopeStatePrefix      = []byte("posnode:consensus:local-vote:v1/")
	localPrepareVoteWatermarkStateKey = []byte("posnode:consensus:local-vote-watermark:prepare:v1")
	localConfirmVoteWatermarkStateKey = []byte("posnode:consensus:local-vote-watermark:confirm:v1")
	localTimeoutVoteWatermarkStateKey = []byte("posnode:consensus:local-vote-watermark:timeout:v1")
)

const (
	maxLoadedLocalProposals     = 8192
	maxLoadedLocalVoteEnvelopes = 8192
)

// loadPersistedLocalProposals 恢复本节点已经签名的提案，避免进程重启后在同一 slot 生成第二个提案。
func (node *posNode) loadPersistedLocalProposals() error {
	if node.db == nil {
		return nil
	}
	watermark, err := node.loadLocalVoteWatermark(localProposalWatermarkStateKey)
	if err != nil {
		return fmt.Errorf("posnode: load local proposal watermark: %w", err)
	}
	node.mutex.Lock()
	node.lastProducedSlot = watermark
	node.mutex.Unlock()

	values, err := node.db.PrefixQueryReverseWithLimit(
		database.TableCheckpoint,
		localProposalStatePrefix,
		maxLoadedLocalProposals,
	)
	if err != nil {
		return fmt.Errorf("posnode: load local proposal journal: %w", err)
	}
	localValidatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	for _, value := range values {
		envelope, decodeErr := unmarshalProposalEnvelopeBinary(value.Value)
		if decodeErr != nil {
			return fmt.Errorf("posnode: decode local proposal journal: %w", decodeErr)
		}
		proposal := envelope.Proposal
		if proposal.Header.ChainID != node.config.ChainID || proposal.Header.LeaderID != localValidatorID {
			return fmt.Errorf("posnode: local proposal journal leader or chain mismatch")
		}
		if verifyErr := proposal.VerifyLeaderSignature(node.consensusKeyPair.PublicKey); verifyErr != nil {
			return fmt.Errorf("posnode: verify local proposal journal: %w", verifyErr)
		}

		choiceKey := proposalChoiceKey(proposal.Header.Slot, localValidatorID)
		node.mutex.Lock()
		if existing, exists := node.proposalChoices[choiceKey]; exists {
			same, hashErr := sameProposalChoice(existing, proposal)
			if hashErr != nil || !same {
				node.mutex.Unlock()
				return fmt.Errorf("posnode: conflicting local proposals persisted for slot %d", proposal.Header.Slot)
			}
		} else {
			if node.proposalChoices == nil {
				node.proposalChoices = make(map[string]consensus.BlockProposal)
			}
			node.proposalChoices[choiceKey] = proposal
		}
		if proposal.Header.Slot > node.lastProducedSlot {
			node.lastProducedSlot = proposal.Header.Slot
		}
		node.mutex.Unlock()
	}
	return nil
}

// prepareDurableLocalProposal 在广播之前同步落盘本节点的唯一提案选择。
func (node *posNode) prepareDurableLocalProposal(proposal consensus.BlockProposal) (consensus.BlockProposal, error) {
	localValidatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	if proposal.Header.ChainID != node.config.ChainID || proposal.Header.LeaderID != localValidatorID {
		return consensus.BlockProposal{}, fmt.Errorf("posnode: local proposal leader or chain mismatch")
	}
	if err := proposal.VerifyLeaderSignature(node.consensusKeyPair.PublicKey); err != nil {
		return consensus.BlockProposal{}, fmt.Errorf("posnode: verify local proposal before persistence: %w", err)
	}

	node.mutex.Lock()
	defer node.mutex.Unlock()
	choiceKey := proposalChoiceKey(proposal.Header.Slot, localValidatorID)
	if existing, exists := node.proposalChoices[choiceKey]; exists {
		same, err := sameProposalChoice(existing, proposal)
		if err != nil {
			return consensus.BlockProposal{}, err
		}
		if !same {
			return consensus.BlockProposal{}, fmt.Errorf("posnode: local proposal choice conflict for slot %d", proposal.Header.Slot)
		}
		return existing, nil
	}

	stateKey := localProposalStateKey(proposal.Header.Slot)
	if node.db != nil {
		persistedData, err := node.db.Get(database.TableCheckpoint, stateKey)
		if err != nil {
			return consensus.BlockProposal{}, fmt.Errorf("posnode: read local proposal journal: %w", err)
		}
		if len(persistedData) > 0 {
			envelope, decodeErr := unmarshalProposalEnvelopeBinary(persistedData)
			if decodeErr != nil {
				return consensus.BlockProposal{}, fmt.Errorf("posnode: decode persisted local proposal: %w", decodeErr)
			}
			persisted := envelope.Proposal
			same, hashErr := sameProposalChoice(persisted, proposal)
			if hashErr != nil || !same || persisted.Header.LeaderID != localValidatorID {
				return consensus.BlockProposal{}, fmt.Errorf("posnode: persisted local proposal choice conflict for slot %d", proposal.Header.Slot)
			}
			node.proposalChoices[choiceKey] = persisted
			if persisted.Header.Slot > node.lastProducedSlot {
				node.lastProducedSlot = persisted.Header.Slot
			}
			return persisted, nil
		}
	}
	if proposal.Header.Slot <= node.lastProducedSlot {
		return consensus.BlockProposal{}, fmt.Errorf("posnode: local proposal slot %d does not exceed durable watermark", proposal.Header.Slot)
	}

	if node.db != nil {
		encoded, err := marshalProposalEnvelopeBinary(proposalEnvelope{Proposal: proposal})
		if err != nil {
			return consensus.BlockProposal{}, fmt.Errorf("posnode: encode local proposal journal: %w", err)
		}
		if err := node.db.Put(database.TableCheckpoint, stateKey, encoded); err != nil {
			return consensus.BlockProposal{}, fmt.Errorf("posnode: persist local proposal journal: %w", err)
		}
		if err := node.db.Put(database.TableCheckpoint, localProposalWatermarkStateKey, encodeLocalVoteWatermark(proposal.Header.Slot)); err != nil {
			return consensus.BlockProposal{}, fmt.Errorf("posnode: persist local proposal watermark: %w", err)
		}
		if err := node.db.Flush(); err != nil {
			return consensus.BlockProposal{}, fmt.Errorf("posnode: flush local proposal journal: %w", err)
		}
	}
	if node.proposalChoices == nil {
		node.proposalChoices = make(map[string]consensus.BlockProposal)
	}
	node.proposalChoices[choiceKey] = proposal
	node.lastProducedSlot = proposal.Header.Slot
	return proposal, nil
}

func sameProposalChoice(first consensus.BlockProposal, second consensus.BlockProposal) (bool, error) {
	firstEncoded, err := marshalProposalEnvelopeBinary(proposalEnvelope{Proposal: first})
	if err != nil {
		return false, fmt.Errorf("posnode: encode first proposal choice: %w", err)
	}
	secondEncoded, err := marshalProposalEnvelopeBinary(proposalEnvelope{Proposal: second})
	if err != nil {
		return false, fmt.Errorf("posnode: encode second proposal choice: %w", err)
	}
	return bytes.Equal(firstEncoded, secondEncoded), nil
}

func localProposalStateKey(slot uint64) []byte {
	key := make([]byte, len(localProposalStatePrefix)+8)
	copy(key, localProposalStatePrefix)
	binary.BigEndian.PutUint64(key[len(localProposalStatePrefix):], slot)
	return key
}

// replayPersistedCurrentRoundProposal 重新验证并直连广播当前轮原提案，供重启或断网恢复后的节点继续聚合 QC。
func (node *posNode) replayPersistedCurrentRoundProposal(ctx context.Context) {
	if !node.config.consensusEnabled() || node.ledger == nil {
		return
	}
	head := node.ledger.Head()
	currentSlot := node.currentConsensusSlot()
	localValidatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	node.mutex.Lock()
	proposal, exists := node.proposalChoices[proposalChoiceKey(currentSlot, localValidatorID)]
	node.mutex.Unlock()
	if !exists || proposal.Header.Height != head.Height+1 || proposal.Header.ParentHash != head.BlockHash {
		return
	}
	if err := node.voteForProposal(ctx, proposal); err != nil {
		node.logger.Warn("posnode replay persisted local proposal failed",
			"slot", proposal.Header.Slot,
			"height", proposal.Header.Height,
			"error", err,
		)
		return
	}
	node.broadcastRecoveredProposal(ctx, proposal)
}

// broadcastRecoveredProposal 对当前已连接验证者直接重放提案，恢复路径不依赖旧 Turbine 转发节点仍保存去重状态。
func (node *posNode) broadcastRecoveredProposal(ctx context.Context, proposal consensus.BlockProposal) {
	if node.host == nil {
		return
	}
	message, err := encodeProposalMessage(proposal)
	if err != nil {
		node.logger.Warn("posnode encode recovered proposal failed", "slot", proposal.Header.Slot, "error", err)
		return
	}
	peerIDs := node.connectedValidatorPeerIDsSnapshot(true)
	if len(peerIDs) == 0 {
		return
	}
	if err := node.host.Broadcast(ctx, peerIDs, message); err != nil {
		node.logger.Warn("posnode broadcast recovered proposal failed",
			"slot", proposal.Header.Slot,
			"height", proposal.Header.Height,
			"peer_count", len(peerIDs),
			"error", err,
		)
		return
	}
	node.logger.Info("posnode recovered proposal replayed",
		"slot", proposal.Header.Slot,
		"height", proposal.Header.Height,
		"peer_count", len(peerIDs),
	)
}

// loadPersistedTimeoutQC 恢复尚未被新区块消费的超时证书。
func (node *posNode) loadPersistedTimeoutQC() error {
	if node.db == nil || node.ledger == nil {
		return nil
	}
	data, err := node.db.Get(database.TableCheckpoint, timeoutQCStateKey)
	if err != nil {
		return fmt.Errorf("posnode: load timeout qc: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	certificate, err := consensus.UnmarshalCertificateBinary(data)
	if err != nil {
		return fmt.Errorf("posnode: decode timeout qc: %w", err)
	}
	if certificate.Type != consensus.VoteTypeSkip {
		return fmt.Errorf("posnode: persisted timeout qc has invalid type")
	}
	head := node.ledger.Head()
	if head.Height == ^uint64(0) || head.Slot == ^uint64(0) ||
		certificate.BlockHash != head.BlockHash || certificate.BlockHeight != head.Height+1 || certificate.Slot <= head.Slot {
		return nil
	}
	if err := node.verifyQuorumCertificate(certificate); err != nil {
		return fmt.Errorf("posnode: verify persisted timeout qc: %w", err)
	}
	node.mutex.Lock()
	copyValue := certificate
	node.latestTimeoutQC = &copyValue
	node.mutex.Unlock()
	return nil
}

func (node *posNode) persistTimeoutQC(certificate consensus.QuorumCertificate) error {
	if node.db == nil {
		return nil
	}
	data, err := certificate.MarshalBinary()
	if err != nil {
		return fmt.Errorf("posnode: encode timeout qc: %w", err)
	}
	if err := node.db.Put(database.TableCheckpoint, timeoutQCStateKey, data); err != nil {
		return fmt.Errorf("posnode: persist timeout qc: %w", err)
	}
	return nil
}

// loadPersistedLocalVoteEnvelopes 恢复本地已签投票 + 重启后不得忘记选择并为同一轮冲突结果再次签名。
func (node *posNode) loadPersistedLocalVoteEnvelopes() error {
	if node.db == nil {
		return nil
	}
	confirmWatermark, err := node.loadLocalVoteWatermark(localConfirmVoteWatermarkStateKey)
	if err != nil {
		return fmt.Errorf("posnode: load confirm vote watermark: %w", err)
	}
	timeoutWatermark, err := node.loadLocalVoteWatermark(localTimeoutVoteWatermarkStateKey)
	if err != nil {
		return fmt.Errorf("posnode: load timeout vote watermark: %w", err)
	}
	prepareWatermark, err := node.loadLocalVoteWatermark(localPrepareVoteWatermarkStateKey)
	if err != nil {
		return fmt.Errorf("posnode: load prepare vote watermark: %w", err)
	}
	node.mutex.Lock()
	node.lastPreparedSlot = prepareWatermark
	node.lastVotedSlot = confirmWatermark
	node.lastTimeoutVotedSlot = timeoutWatermark
	node.mutex.Unlock()

	values, err := node.db.PrefixQueryReverseWithLimit(
		database.TableCheckpoint,
		localVoteEnvelopeStatePrefix,
		maxLoadedLocalVoteEnvelopes,
	)
	if err != nil {
		return fmt.Errorf("posnode: load local vote journal: %w", err)
	}
	localValidatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	for _, value := range values {
		envelope, err := unmarshalVoteEnvelopeBinary(value.Value)
		if err != nil {
			return fmt.Errorf("posnode: decode local vote journal: %w", err)
		}
		if envelope.Vote.VoterID != string(localValidatorID) || envelope.PublicKey != node.consensusKeyPair.PublicKey {
			return fmt.Errorf("posnode: local vote journal validator mismatch")
		}
		if err := node.verifyVoteEnvelope(envelope); err != nil {
			return fmt.Errorf("posnode: verify local vote journal: %w", err)
		}

		node.mutex.Lock()
		if !node.rememberLocalVoteEnvelopeLocked(envelope) {
			node.mutex.Unlock()
			return fmt.Errorf("posnode: conflicting local votes persisted for slot %d", envelope.Vote.Slot)
		}
		node.advanceLocalVoteWatermarkLocked(envelope.Vote)
		node.mutex.Unlock()
	}
	return nil
}

// prepareDurableLocalVoteEnvelope 先写本地安全日志再返回可广播信封 + 写盘失败时禁止发送投票。
func (node *posNode) prepareDurableLocalVoteEnvelope(envelope voteEnvelope) (voteEnvelope, error) {
	if err := node.verifyVoteEnvelope(envelope); err != nil {
		return voteEnvelope{}, fmt.Errorf("posnode: verify local vote before persistence: %w", err)
	}
	localValidatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	if envelope.Vote.VoterID != string(localValidatorID) || envelope.PublicKey != node.consensusKeyPair.PublicKey {
		return voteEnvelope{}, fmt.Errorf("posnode: local vote validator mismatch")
	}

	node.mutex.Lock()
	defer node.mutex.Unlock()
	key := localVoteEnvelopeChoiceKey(envelope.Vote.Slot, localValidatorID, envelope.Vote.Type)
	if existingEnvelope, exists := node.voteEnvelopeChoices[key]; exists {
		if !sameVoteChoice(existingEnvelope.Vote, envelope.Vote) {
			return voteEnvelope{}, fmt.Errorf("posnode: local vote choice conflict for slot %d", envelope.Vote.Slot)
		}
		return cloneVoteEnvelope(existingEnvelope), nil
	}

	stateKey := localVoteEnvelopeStateKey(envelope.Vote)
	if node.db != nil {
		persistedData, err := node.db.Get(database.TableCheckpoint, stateKey)
		if err != nil {
			return voteEnvelope{}, fmt.Errorf("posnode: read local vote journal: %w", err)
		}
		if len(persistedData) > 0 {
			persistedEnvelope, err := unmarshalVoteEnvelopeBinary(persistedData)
			if err != nil {
				return voteEnvelope{}, fmt.Errorf("posnode: decode persisted local vote: %w", err)
			}
			if !sameVoteChoice(persistedEnvelope.Vote, envelope.Vote) ||
				persistedEnvelope.Vote.VoterID != string(localValidatorID) ||
				persistedEnvelope.PublicKey != node.consensusKeyPair.PublicKey {
				return voteEnvelope{}, fmt.Errorf("posnode: persisted local vote choice conflict for slot %d", envelope.Vote.Slot)
			}
			node.voteEnvelopeChoices[key] = cloneVoteEnvelope(persistedEnvelope)
			node.advanceLocalVoteWatermarkLocked(persistedEnvelope.Vote)
			return cloneVoteEnvelope(persistedEnvelope), nil
		}
	}

	if envelope.Vote.Type == consensus.VoteTypePrepare && envelope.Vote.Slot <= node.localSignedVoteWatermarkLocked() {
		return voteEnvelope{}, fmt.Errorf("posnode: local prepare vote slot %d follows a durable decision", envelope.Vote.Slot)
	}
	if envelope.Vote.Slot <= node.localVoteWatermarkLocked(envelope.Vote.Type) {
		return voteEnvelope{}, fmt.Errorf("posnode: local vote slot %d does not exceed durable watermark", envelope.Vote.Slot)
	}

	if node.db != nil {
		encodedEnvelope, err := marshalVoteEnvelopeBinary(envelope)
		if err != nil {
			return voteEnvelope{}, fmt.Errorf("posnode: encode local vote journal: %w", err)
		}
		if err := node.db.Put(database.TableCheckpoint, stateKey, encodedEnvelope); err != nil {
			return voteEnvelope{}, fmt.Errorf("posnode: persist local vote journal: %w", err)
		}
		watermarkKey, err := localVoteWatermarkStateKey(envelope.Vote.Type)
		if err != nil {
			return voteEnvelope{}, err
		}
		if err := node.db.Put(database.TableCheckpoint, watermarkKey, encodeLocalVoteWatermark(envelope.Vote.Slot)); err != nil {
			return voteEnvelope{}, fmt.Errorf("posnode: persist local vote watermark: %w", err)
		}
		if err := node.db.Flush(); err != nil {
			return voteEnvelope{}, fmt.Errorf("posnode: flush local vote journal: %w", err)
		}
	}

	if node.voteEnvelopeChoices == nil {
		node.voteEnvelopeChoices = make(map[string]voteEnvelope)
	}
	node.voteEnvelopeChoices[key] = cloneVoteEnvelope(envelope)
	node.advanceLocalVoteWatermarkLocked(envelope.Vote)
	return cloneVoteEnvelope(envelope), nil
}

func (node *posNode) loadLocalVoteWatermark(stateKey []byte) (uint64, error) {
	data, err := node.db.Get(database.TableCheckpoint, stateKey)
	if err != nil {
		return 0, err
	}
	if len(data) == 0 {
		return 0, nil
	}
	if len(data) != 8 {
		return 0, fmt.Errorf("invalid encoded length %d", len(data))
	}
	return binary.BigEndian.Uint64(data), nil
}

func encodeLocalVoteWatermark(slot uint64) []byte {
	encoded := make([]byte, 8)
	binary.BigEndian.PutUint64(encoded, slot)
	return encoded
}

func localVoteWatermarkStateKey(voteType consensus.VoteType) ([]byte, error) {
	switch voteType {
	case consensus.VoteTypePrepare:
		return localPrepareVoteWatermarkStateKey, nil
	case consensus.VoteTypeConfirm:
		return localConfirmVoteWatermarkStateKey, nil
	case consensus.VoteTypeSkip:
		return localTimeoutVoteWatermarkStateKey, nil
	default:
		return nil, fmt.Errorf("posnode: unsupported local vote type %d", voteType)
	}
}

func (node *posNode) localVoteWatermarkLocked(voteType consensus.VoteType) uint64 {
	switch voteType {
	case consensus.VoteTypePrepare:
		return node.lastPreparedSlot
	case consensus.VoteTypeConfirm, consensus.VoteTypeSkip:
		return node.localSignedVoteWatermarkLocked()
	default:
		return ^uint64(0)
	}
}

func (node *posNode) localSignedVoteWatermarkLocked() uint64 {
	if node.lastTimeoutVotedSlot > node.lastVotedSlot {
		return node.lastTimeoutVotedSlot
	}
	return node.lastVotedSlot
}

func (node *posNode) localProposalVoteWatermarkLocked() uint64 {
	watermark := node.localSignedVoteWatermarkLocked()
	if node.lastPreparedSlot > watermark {
		return node.lastPreparedSlot
	}
	return watermark
}

func (node *posNode) preparedLocalVoteEnvelope(vote consensus.Vote) (voteEnvelope, bool, error) {
	validatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	if vote.VoterID != string(validatorID) {
		return voteEnvelope{}, false, fmt.Errorf("posnode: local vote validator mismatch")
	}
	node.mutex.Lock()
	defer node.mutex.Unlock()
	key := localVoteEnvelopeChoiceKey(vote.Slot, validatorID, vote.Type)
	if envelope, exists := node.voteEnvelopeChoices[key]; exists {
		if !sameVoteChoice(envelope.Vote, vote) {
			return voteEnvelope{}, false, fmt.Errorf("posnode: local vote choice conflict for slot %d", vote.Slot)
		}
		return cloneVoteEnvelope(envelope), true, nil
	}
	if vote.Type == consensus.VoteTypePrepare && vote.Slot <= node.localSignedVoteWatermarkLocked() {
		return voteEnvelope{}, false, fmt.Errorf("posnode: local prepare vote slot %d follows a durable decision", vote.Slot)
	}
	if vote.Slot <= node.localVoteWatermarkLocked(vote.Type) {
		return voteEnvelope{}, false, fmt.Errorf("posnode: local vote slot %d does not exceed durable watermark", vote.Slot)
	}
	return voteEnvelope{}, false, nil
}

func (node *posNode) advanceLocalVoteWatermarkLocked(vote consensus.Vote) {
	switch vote.Type {
	case consensus.VoteTypePrepare:
		if vote.Slot > node.lastPreparedSlot {
			node.lastPreparedSlot = vote.Slot
		}
	case consensus.VoteTypeConfirm:
		if vote.Slot > node.lastVotedSlot {
			node.lastVotedSlot = vote.Slot
		}
	case consensus.VoteTypeSkip:
		if vote.Slot > node.lastTimeoutVotedSlot {
			node.lastTimeoutVotedSlot = vote.Slot
		}
	}
}

func localVoteEnvelopeStateKey(vote consensus.Vote) []byte {
	if vote.Type == consensus.VoteTypePrepare {
		key := make([]byte, len(localVoteEnvelopeStatePrefix)+9)
		copy(key, localVoteEnvelopeStatePrefix)
		key[len(localVoteEnvelopeStatePrefix)] = byte(consensus.VoteTypePrepare)
		binary.BigEndian.PutUint64(key[len(localVoteEnvelopeStatePrefix)+1:], vote.Slot)
		return key
	}
	key := make([]byte, len(localVoteEnvelopeStatePrefix)+8)
	copy(key, localVoteEnvelopeStatePrefix)
	binary.BigEndian.PutUint64(key[len(localVoteEnvelopeStatePrefix):], vote.Slot)
	return key
}

// replayPersistedCurrentRoundVotes 重播当前轮本地安全日志 + 所有节点重启后可重新聚合未完成的 QC。
func (node *posNode) replayPersistedCurrentRoundVotes(ctx context.Context) {
	if !node.config.consensusEnabled() || node.ledger == nil {
		return
	}
	currentSlot := node.currentConsensusSlot()
	validatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	envelopes := make([]voteEnvelope, 0, 2)
	node.mutex.Lock()
	for _, voteType := range []consensus.VoteType{consensus.VoteTypePrepare, consensus.VoteTypeConfirm} {
		key := localVoteEnvelopeChoiceKey(currentSlot, validatorID, voteType)
		if envelope, exists := node.voteEnvelopeChoices[key]; exists {
			envelopes = append(envelopes, cloneVoteEnvelope(envelope))
		}
	}
	node.mutex.Unlock()

	for _, envelope := range envelopes {
		node.broadcastVoteEnvelope(ctx, envelope)
		if err := node.handleLocalVoteEnvelope(ctx, envelope); err != nil && err != consensus.ErrDuplicateVote {
			node.logger.Warn("posnode replay persisted local vote failed",
				"slot", envelope.Vote.Slot,
				"vote_type", envelope.Vote.Type,
				"error", err,
			)
		}
	}
}

// replayConfirmedProposalAfterTimeout 重播本轮已确认的提案和原始签名。
// 验证者一旦对提案签名就不能再为同一轮签 TimeoutVote；重播让未收到提案的验证者补票，
// 同时避免为了活性破坏单轮唯一选择约束。
func (node *posNode) replayConfirmedProposalAfterTimeout(
	ctx context.Context,
	slot uint64,
	observedHead blockchain.Head,
	now time.Time,
) bool {
	if !node.config.consensusEnabled() || node.ledger == nil {
		return false
	}
	validatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	node.mutex.Lock()
	currentHead := node.ledger.Head()
	if currentHead.BlockHash != observedHead.BlockHash || currentHead.Height != observedHead.Height ||
		slot != node.currentConsensusSlotLocked(currentHead) {
		node.mutex.Unlock()
		return false
	}
	envelope, exists := node.cachedDecisionLocalVoteEnvelopeLocked(slot, validatorID)
	if !exists || envelope.Vote.Type != consensus.VoteTypeConfirm {
		node.mutex.Unlock()
		return false
	}
	retryAfter := node.roundLeaderTimeout(currentHead.Slot, slot)
	if retryAfter > 0 && !node.roundLockedProposalReplayAt.IsZero() &&
		now.Before(node.roundLockedProposalReplayAt.Add(retryAfter)) {
		node.mutex.Unlock()
		return true
	}
	node.roundLockedProposalReplayAt = now
	node.mutex.Unlock()

	proposal, found, err := node.ledger.BlockByHash(envelope.Vote.BlockHash)
	if err != nil || !found {
		node.logger.Warn("posnode locked proposal replay unavailable",
			"slot", slot,
			"height", envelope.Vote.BlockHeight,
			"block_hash", envelope.Vote.BlockHash.String(),
			"found", found,
			"error", err,
		)
		return true
	}
	if proposal.Header.Slot != slot || proposal.Header.Height != observedHead.Height+1 ||
		proposal.Header.ParentHash != observedHead.BlockHash {
		node.logger.Warn("posnode locked proposal replay rejected for round mismatch",
			"slot", slot,
			"proposal_slot", proposal.Header.Slot,
			"proposal_height", proposal.Header.Height,
			"parent_hash", proposal.Header.ParentHash.String(),
		)
		return true
	}

	node.logger.Info("posnode locked proposal replayed after timeout",
		"slot", slot,
		"height", proposal.Header.Height,
		"block_hash", envelope.Vote.BlockHash.String(),
		"validator_id", string(validatorID),
	)
	if proposal.Header.LeaderID == validatorID {
		node.broadcastRecoveredProposal(ctx, proposal)
	}
	node.broadcastVoteEnvelope(ctx, envelope)
	node.broadcastStaleHeadRecoveryVoteEnvelope(ctx, envelope)
	if err := node.handleLocalVoteEnvelope(ctx, envelope); err != nil && err != consensus.ErrDuplicateVote {
		node.logger.Warn("posnode locked proposal local vote replay failed",
			"slot", slot,
			"block_hash", envelope.Vote.BlockHash.String(),
			"error", err,
		)
	}
	return true
}

// pruneFinalizedLocalVoteJournal 清理已最终确认轮次的本地投票 + 保留未最终确认窗口用于安全恢复。
func (node *posNode) pruneFinalizedLocalVoteJournal() {
	if node.ledger == nil {
		return
	}
	head := node.ledger.Head()
	if head.FinalizedHeight == 0 {
		return
	}
	finalizedProposal, _, found, err := node.ledger.BlockByHeight(head.FinalizedHeight)
	if err != nil || !found {
		return
	}
	finalizedSlot := finalizedProposal.Header.Slot
	keysToDelete := make([][]byte, 0)
	node.mutex.Lock()
	for choiceKey, envelope := range node.voteEnvelopeChoices {
		if envelope.Vote.Slot > finalizedSlot {
			continue
		}
		delete(node.voteEnvelopeChoices, choiceKey)
		delete(node.localVoteBroadcastAt, localVoteBroadcastRouteKey(choiceKey, "regular"))
		delete(node.localVoteBroadcastAt, localVoteBroadcastRouteKey(choiceKey, "recovery"))
		keysToDelete = append(keysToDelete, localVoteEnvelopeStateKey(envelope.Vote))
	}
	node.mutex.Unlock()
	if node.db == nil || len(keysToDelete) == 0 {
		return
	}
	if err := node.db.BatchDelete(database.TableCheckpoint, keysToDelete); err != nil {
		node.logger.Warn("posnode prune finalized local vote journal failed",
			"finalized_height", head.FinalizedHeight,
			"finalized_slot", finalizedSlot,
			"error", err,
		)
	}
}

// pruneFinalizedLocalProposalJournal 清理已经最终确认的本地提案正文，但保留单调 watermark 防止旧 slot 再提案。
func (node *posNode) pruneFinalizedLocalProposalJournal() {
	if node.ledger == nil {
		return
	}
	head := node.ledger.Head()
	if head.FinalizedHeight == 0 {
		return
	}
	finalizedProposal, _, found, err := node.ledger.BlockByHeight(head.FinalizedHeight)
	if err != nil || !found {
		return
	}
	finalizedSlot := finalizedProposal.Header.Slot
	localValidatorID := consensus.NewValidatorID(node.consensusKeyPair.PublicKey)
	keysToDelete := make([][]byte, 0)
	node.mutex.Lock()
	for choiceKey, proposal := range node.proposalChoices {
		if proposal.Header.LeaderID != localValidatorID || proposal.Header.Slot > finalizedSlot {
			continue
		}
		delete(node.proposalChoices, choiceKey)
		keysToDelete = append(keysToDelete, localProposalStateKey(proposal.Header.Slot))
	}
	node.mutex.Unlock()
	if node.db == nil || len(keysToDelete) == 0 {
		return
	}
	if err := node.db.BatchDelete(database.TableCheckpoint, keysToDelete); err != nil {
		node.logger.Warn("posnode prune finalized local proposal journal failed",
			"finalized_height", head.FinalizedHeight,
			"finalized_slot", finalizedSlot,
			"error", err,
		)
	}
}
