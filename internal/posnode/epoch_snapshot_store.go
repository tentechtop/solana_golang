package posnode

import (
	"encoding/json"
	"fmt"

	"github.com/tentechtop/ordexus-core/consensus"
	"github.com/tentechtop/ordexus-core/database"
	"github.com/tentechtop/ordexus-core/structure"
)

const persistedEpochSnapshotVersion uint16 = 2

type persistedEpochSnapshot struct {
	Version  uint16                  `json:"version"`
	Snapshot consensus.EpochSnapshot `json:"snapshot"`
}

func epochSnapshotStateKey(epochID uint64) []byte {
	return []byte(fmt.Sprintf("posnode:consensus:epoch-snapshot:v1:%020d", epochID))
}

// persistEpochSnapshot 保存共识使用过的 epoch 验证者快照，节点重启后仍按证据发生时的权重和公钥验签。
func (node *posNode) persistEpochSnapshot(snapshot consensus.EpochSnapshot) error {
	return node.persistEpochSnapshotWithReplacement(snapshot, false)
}

// replaceEpochSnapshot 仅供已经验证法定签名证书的模式切换替换同一 epoch 的有效验证者快照。
func (node *posNode) replaceEpochSnapshot(snapshot consensus.EpochSnapshot) error {
	return node.persistEpochSnapshotWithReplacement(snapshot, true)
}

func (node *posNode) persistEpochSnapshotWithReplacement(snapshot consensus.EpochSnapshot, allowReplacement bool) error {
	if node.db == nil {
		return nil
	}
	operation, required, err := node.epochSnapshotWriteOperation(snapshot, allowReplacement)
	if err != nil {
		return err
	}
	if !required {
		return nil
	}
	if err := node.db.Put(operation.Table, operation.Key, operation.Value); err != nil {
		return fmt.Errorf("posnode: persist epoch snapshot: %w", err)
	}
	if err := node.db.Flush(); err != nil {
		return fmt.Errorf("posnode: flush epoch snapshot: %w", err)
	}
	return nil
}

func (node *posNode) epochSnapshotWriteOperation(
	snapshot consensus.EpochSnapshot,
	allowReplacement bool,
) (database.DBOperation, bool, error) {
	canonical, err := canonicalEpochSnapshot(snapshot)
	if err != nil {
		return database.DBOperation{}, false, fmt.Errorf("posnode: validate epoch snapshot before persist: %w", err)
	}
	stateKey := epochSnapshotStateKey(snapshot.EpochID)
	existingData, err := node.db.Get(database.TableCheckpoint, stateKey)
	if err != nil {
		return database.DBOperation{}, false, fmt.Errorf("posnode: read existing epoch snapshot: %w", err)
	}
	if len(existingData) > 0 {
		existing, err := decodePersistedEpochSnapshot(existingData)
		if err != nil {
			return database.DBOperation{}, false, fmt.Errorf("posnode: decode existing epoch snapshot: %w", err)
		}
		if existing.EpochID != canonical.EpochID || existing.StartSlot != canonical.StartSlot ||
			existing.EndSlot != canonical.EndSlot || existing.RandomSeed != canonical.RandomSeed ||
			existing.SnapshotStateRoot != canonical.SnapshotStateRoot {
			if !allowReplacement {
				return database.DBOperation{}, false, fmt.Errorf("posnode: refusing to overwrite immutable epoch %d snapshot", snapshot.EpochID)
			}
		} else {
			return database.DBOperation{}, false, nil
		}
	}
	data, err := json.Marshal(persistedEpochSnapshot{
		Version:  persistedEpochSnapshotVersion,
		Snapshot: canonical,
	})
	if err != nil {
		return database.DBOperation{}, false, fmt.Errorf("posnode: encode epoch snapshot: %w", err)
	}
	return database.NewUpdateOperation(database.TableCheckpoint, stateKey, data), true, nil
}

func (node *posNode) loadEpochSnapshot(
	epochID uint64,
	startSlot uint64,
	epochSlots uint64,
	seed structure.Hash,
) (consensus.EpochSnapshot, bool, error) {
	if node.db == nil {
		return consensus.EpochSnapshot{}, false, nil
	}
	data, err := node.db.Get(database.TableCheckpoint, epochSnapshotStateKey(epochID))
	if err != nil {
		return consensus.EpochSnapshot{}, false, fmt.Errorf("posnode: load epoch snapshot: %w", err)
	}
	if len(data) == 0 {
		return consensus.EpochSnapshot{}, false, nil
	}
	if epochSlots == 0 || startSlot > ^uint64(0)-(epochSlots-1) {
		return consensus.EpochSnapshot{}, false, fmt.Errorf("posnode: invalid epoch slot range")
	}
	canonical, err := decodePersistedEpochSnapshot(data)
	if err != nil {
		return consensus.EpochSnapshot{}, false, fmt.Errorf("posnode: decode epoch snapshot: %w", err)
	}
	if canonical.EpochID != epochID || canonical.StartSlot != startSlot ||
		canonical.EndSlot != startSlot+epochSlots-1 || canonical.RandomSeed != seed {
		return consensus.EpochSnapshot{}, false, fmt.Errorf("posnode: persisted epoch snapshot consensus parameters mismatch")
	}
	return canonical, true, nil
}

func decodePersistedEpochSnapshot(data []byte) (consensus.EpochSnapshot, error) {
	var persisted persistedEpochSnapshot
	if err := json.Unmarshal(data, &persisted); err != nil {
		return consensus.EpochSnapshot{}, err
	}
	if persisted.Version != persistedEpochSnapshotVersion {
		return consensus.EpochSnapshot{}, fmt.Errorf("unsupported epoch snapshot version %d", persisted.Version)
	}
	canonical, err := canonicalEpochSnapshot(persisted.Snapshot)
	if err != nil {
		return consensus.EpochSnapshot{}, fmt.Errorf("validate persisted epoch snapshot: %w", err)
	}
	return canonical, nil
}

func canonicalEpochSnapshot(snapshot consensus.EpochSnapshot) (consensus.EpochSnapshot, error) {
	if snapshot.EndSlot < snapshot.StartSlot {
		return consensus.EpochSnapshot{}, fmt.Errorf("invalid epoch slot range")
	}
	if snapshot.EndSlot-snapshot.StartSlot == ^uint64(0) {
		return consensus.EpochSnapshot{}, fmt.Errorf("epoch slot range overflows")
	}
	for index, validator := range snapshot.Validators {
		if validator.ValidatorID != consensus.NewValidatorID(validator.ConsensusPublicKey) {
			return consensus.EpochSnapshot{}, fmt.Errorf("validator %d id does not match consensus public key", index)
		}
	}
	validatorSet, err := consensus.NewValidatorSet(snapshot.Validators)
	if err != nil {
		return consensus.EpochSnapshot{}, err
	}
	epochSlots := snapshot.EndSlot - snapshot.StartSlot + 1
	canonical, err := consensus.NewEpochSnapshot(
		snapshot.EpochID,
		snapshot.StartSlot,
		epochSlots,
		snapshot.RandomSeed,
		validatorSet,
	)
	if err != nil {
		return consensus.EpochSnapshot{}, err
	}
	if canonical.SnapshotStateRoot != snapshot.SnapshotStateRoot ||
		canonical.TotalActiveStake != snapshot.TotalActiveStake {
		return consensus.EpochSnapshot{}, fmt.Errorf("epoch snapshot root or total stake mismatch")
	}
	return canonical, nil
}
