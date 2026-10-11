package posnode

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/tentechtop/ordexus-core/blockchain"
	"github.com/tentechtop/ordexus-core/consensus"
	"github.com/tentechtop/ordexus-core/database"
	"github.com/tentechtop/ordexus-core/structure"
)

var activationHistoryPrefix = []byte("posnode:consensus:activation-proof:v1:")

func (node *posNode) persistActivationProof(certificate consensus.MultiValidatorActivationCertificate, data []byte) error {
	if node.db != nil {
		previous, err := node.db.Get(database.TableCheckpoint, multiValidatorActivationKey)
		if err != nil {
			return err
		}
		if len(previous) > 0 {
			var older consensus.MultiValidatorActivationCertificate
			if err := json.Unmarshal(previous, &older); err != nil {
				return err
			}
			if older.Sequence != certificate.Sequence {
				// Migrate the last certificate from nodes that kept only one key.
				key := []byte(fmt.Sprintf("%s%020d", activationHistoryPrefix, older.Sequence))
				if err := node.persistConsensusCertificate(key, previous); err != nil {
					return err
				}
			}
		}
	}
	key := []byte(fmt.Sprintf("%s%020d", activationHistoryPrefix, certificate.Sequence))
	if err := node.persistConsensusCertificate(key, data); err != nil {
		return err
	}
	return node.persistConsensusCertificate(multiValidatorActivationKey, data)
}

// Retain every committee change. The old single-elder epoch proof remains valid
// for slots up to the ratified checkpoint, including after a node restart.
func (node *posNode) loadActivationProofs() ([]consensus.MultiValidatorActivationCertificate, error) {
	if node.db == nil {
		return nil, nil
	}
	bySequence := make(map[uint64]consensus.MultiValidatorActivationCertificate)
	var decodeErr error
	decode := func(data []byte) {
		var certificate consensus.MultiValidatorActivationCertificate
		if err := json.Unmarshal(data, &certificate); err != nil {
			decodeErr = err
			return
		}
		if existing, ok := bySequence[certificate.Sequence]; ok {
			first, _ := json.Marshal(existing)
			second, _ := json.Marshal(certificate)
			if string(first) != string(second) {
				decodeErr = fmt.Errorf("posnode: conflicting persisted activation sequence")
			}
		}
		bySequence[certificate.Sequence] = certificate
	}
	if err := node.db.IterateByPrefix(database.TableCheckpoint, activationHistoryPrefix, func(_ []byte, data []byte) bool {
		decode(data)
		return decodeErr == nil
	}); err != nil {
		return nil, err
	}
	data, err := node.db.Get(database.TableCheckpoint, multiValidatorActivationKey)
	if err != nil {
		return nil, err
	}
	if len(data) > 0 {
		decode(data) // Read certificates persisted by older node versions, too.
	}
	if decodeErr != nil {
		return nil, fmt.Errorf("posnode: decode activation proof: %w", decodeErr)
	}
	proofs := make([]consensus.MultiValidatorActivationCertificate, 0, len(bySequence))
	for _, certificate := range bySequence {
		proofs = append(proofs, certificate)
	}
	sort.Slice(proofs, func(i, j int) bool { return proofs[i].Sequence < proofs[j].Sequence })
	return proofs, nil
}

func (node *posNode) activationAnchor(height uint64, encodedHash string) (consensus.ChainState, uint64, error) {
	if node.ledger == nil || height > node.ledger.Head().FinalizedHeight {
		return consensus.ChainState{}, 0, fmt.Errorf("posnode: activation anchor is not finalized")
	}
	hash, err := structure.HashFromBase58(encodedHash)
	if err != nil {
		return consensus.ChainState{}, 0, err
	}
	var slot uint64
	if height > 0 {
		proposal, actualHash, found, err := node.ledger.BlockByHeight(height)
		if err != nil || !found || actualHash != hash {
			return consensus.ChainState{}, 0, fmt.Errorf("posnode: activation anchor is not on the finalized chain")
		}
		slot = proposal.Header.Slot
	} else {
		// StateAtBlockHash below verifies that the genesis state exists. A configured
		// genesis commitment additionally prevents substituting another genesis.
		if node.config.GenesisHash != "" && node.config.GenesisHash != encodedHash {
			return consensus.ChainState{}, 0, fmt.Errorf("posnode: activation genesis anchor mismatch")
		}
	}
	state, err := node.ledger.StateAtBlockHash(hash)
	return state, slot, err
}

func (node *posNode) authenticateActivationProof(certificate consensus.MultiValidatorActivationCertificate, authenticated map[uint64]consensus.EpochSnapshot) (consensus.EpochSnapshot, uint64, error) {
	if certificate.ChainID != node.config.ChainID || certificate.SafetyEpochID > certificate.TargetEpochID || certificate.SafetyFinalizedHeight > certificate.TargetHeight {
		return consensus.EpochSnapshot{}, 0, fmt.Errorf("posnode: activation proof identity or checkpoint order mismatch")
	}
	signBytes, err := certificate.SignBytes()
	if err != nil {
		return consensus.EpochSnapshot{}, 0, err
	}
	safetyState, _, err := node.activationAnchor(certificate.SafetyFinalizedHeight, certificate.SafetyFinalizedHash)
	if err != nil {
		return consensus.EpochSnapshot{}, 0, err
	}
	safetySet, setErr := blockchain.ValidatorSetFromStateAtEpoch(safetyState, certificate.SafetyEpochID)
	root, rootErr := consensus.ValidatorSetRoot(safetySet)
	if setErr != nil || rootErr != nil || root.String() != certificate.SafetyValidatorSetRoot {
		snapshot, exists := authenticated[certificate.SafetyEpochID]
		if !exists {
			return consensus.EpochSnapshot{}, 0, fmt.Errorf("posnode: activation safety epoch is not authenticated")
		}
		safetySet, err = consensus.NewValidatorSet(snapshot.Validators)
		if err != nil {
			return consensus.EpochSnapshot{}, 0, err
		}
		root, err = consensus.ValidatorSetRoot(safetySet)
		if err != nil || root.String() != certificate.SafetyValidatorSetRoot {
			return consensus.EpochSnapshot{}, 0, fmt.Errorf("posnode: activation safety committee root mismatch")
		}
	}
	if err := verifyRecoveryStakeSignatures(signBytes, certificate.Signatures, safetySet); err != nil {
		return consensus.EpochSnapshot{}, 0, err
	}
	targetState, targetSlot, err := node.activationAnchor(certificate.TargetHeight, certificate.TargetHash)
	if err != nil || targetSlot == ^uint64(0) {
		return consensus.EpochSnapshot{}, 0, fmt.Errorf("posnode: invalid activation target anchor: %v", err)
	}
	targetEpochID, startSlot := node.epochForSlot(targetSlot + 1)
	if targetEpochID != certificate.TargetEpochID {
		return consensus.EpochSnapshot{}, 0, fmt.Errorf("posnode: activation proof target epoch mismatch")
	}
	targetSet, err := blockchain.ValidatorSetFromStateAtEpoch(targetState, targetEpochID)
	if err != nil {
		return consensus.EpochSnapshot{}, 0, err
	}
	threshold := node.config.ConsensusPolicy.MultiValidatorThreshold
	if threshold == 0 {
		threshold = consensus.DefaultMultiValidatorThreshold
	}
	root, err = consensus.ValidatorSetRoot(targetSet)
	if err != nil || root.String() != certificate.TargetValidatorSetRoot || len(targetSet.Validators()) < threshold {
		return consensus.EpochSnapshot{}, 0, fmt.Errorf("posnode: activation target committee commitment mismatch")
	}
	snapshot, err := consensus.NewEpochSnapshot(targetEpochID, startSlot, node.config.EpochSlots, node.epochSeed(targetEpochID), targetSet)
	return snapshot, targetSlot, err
}

func (node *posNode) authenticatedSnapshotThroughSlot(epochID uint64, slot *uint64) (consensus.EpochSnapshot, error) {
	previous, err := node.trustedGenesisEpochSnapshot()
	if err != nil {
		return consensus.EpochSnapshot{}, err
	}
	activations, err := node.loadActivationProofs()
	if err != nil {
		return consensus.EpochSnapshot{}, err
	}
	authenticated := make(map[uint64]consensus.EpochSnapshot)
	for epoch := uint64(0); ; epoch++ {
		if epoch > 0 {
			proof, found, err := node.loadEpochTransitionProof(epoch)
			if err != nil {
				return consensus.EpochSnapshot{}, err
			}
			if !found {
				return consensus.EpochSnapshot{}, fmt.Errorf("posnode: missing authenticated epoch transition proof %d", epoch)
			}
			if _, _, err := node.verifyEpochTransitionProof(previous, proof, true); err != nil {
				return consensus.EpochSnapshot{}, err
			}
			previous = proof.Snapshot
		}
		var bound *uint64
		if epoch == epochID {
			bound = slot
		}
		previous, err = node.applyAuthenticatedActivations(previous, activations, authenticated, bound)
		if err != nil {
			return consensus.EpochSnapshot{}, err
		}
		if epoch == epochID {
			return previous, nil
		}
	}
}

func (node *posNode) applyAuthenticatedActivations(snapshot consensus.EpochSnapshot, activations []consensus.MultiValidatorActivationCertificate, authenticated map[uint64]consensus.EpochSnapshot, slot *uint64) (consensus.EpochSnapshot, error) {
	authenticated[snapshot.EpochID] = snapshot
	for _, certificate := range activations {
		if certificate.TargetEpochID != snapshot.EpochID {
			continue
		}
		activated, targetSlot, err := node.authenticateActivationProof(certificate, authenticated)
		if err != nil {
			return consensus.EpochSnapshot{}, err
		}
		if slot != nil && *slot <= targetSlot {
			continue
		}
		snapshot = activated
		authenticated[snapshot.EpochID] = activated
	}
	return snapshot, nil
}
