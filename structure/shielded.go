package structure

import (
	"errors"
	"fmt"

	"github.com/tentechtop/ordexus-core/codec/borsh"
	"github.com/tentechtop/ordexus-core/utils"
)

const (
	// ShieldedProtocolVersion 定义当前屏蔽协议版本 + 未知版本必须通过共识升级显式启用。
	ShieldedProtocolVersion uint16 = 2
	// ShieldedPoolStateEncodingVersion 定义屏蔽池账户编码版本 + 与密码学协议版本独立演进。
	ShieldedPoolStateEncodingVersion uint16 = 1

	ShieldedEphemeralKeySize = 32

	// MaxShieldedActionsPerBundle 与当前固定电路的 action 槽位数一致 + prover/program 负责将单 action 补齐为两个槽位。
	MaxShieldedActionsPerBundle = 2
	// MaxShieldedEncryptedNoteBytes 与 V2 最大 512 字节 memo 的规范加密信封长度一致。
	MaxShieldedEncryptedNoteBytes = 632
	// ShieldedGroth16ProofBytes 与 V2 BN254 Groth16 规范压缩编码长度一致。
	ShieldedGroth16ProofBytes = 164
	MaxShieldedProofBytes     = ShieldedGroth16ProofBytes
	MaxShieldedActionBytes    = 4 * 1024
	MaxShieldedBundleBytes    = 256 * 1024
	MaxShieldedTreeDepth      = 64
	MaxShieldedRecentAnchors  = 256
	MaxShieldedPoolStateBytes = 64 * 1024

	// ShieldedPoolStateWriteChargeBytes 覆盖池账户、前沿和锚点窗口的单次共识写入成本。
	ShieldedPoolStateWriteChargeBytes = uint64(10 * 1024)
	// ShieldedNullifierStateWriteChargeBytes 是每个永久 nullifier 的计费等效字节，包含账户、索引、快照和状态证明放大。
	ShieldedNullifierStateWriteChargeBytes = uint64(64 * 1024)
)

var (
	ErrInvalidShieldedAction    = errors.New("structure: invalid shielded action")
	ErrInvalidShieldedBundle    = errors.New("structure: invalid shielded bundle")
	ErrInvalidShieldedPoolState = errors.New("structure: invalid shielded pool state")
)

// ShieldedStorageWriteChargeBytes 计算 bundle 的确定性持久状态计费量。
func ShieldedStorageWriteChargeBytes(actions []ShieldedAction) (uint64, error) {
	if len(actions) == 0 || len(actions) > MaxShieldedActionsPerBundle {
		return 0, fmt.Errorf("%w: invalid action count %d", ErrInvalidShieldedBundle, len(actions))
	}
	storageWriteBytes := ShieldedPoolStateWriteChargeBytes
	for actionIndex, action := range actions {
		if err := action.Validate(); err != nil {
			return 0, fmt.Errorf("%w: action %d: %w", ErrInvalidShieldedBundle, actionIndex, err)
		}
		if !action.EnabledSpend {
			continue
		}
		if storageWriteBytes > ^uint64(0)-ShieldedNullifierStateWriteChargeBytes {
			return 0, fmt.Errorf("%w: storage write charge overflow", ErrInvalidShieldedBundle)
		}
		storageWriteBytes += ShieldedNullifierStateWriteChargeBytes
	}
	return storageWriteBytes, nil
}

// ShieldedAction 表示一个可含虚拟输入或虚拟输出的屏蔽动作 + 显式开关避免用零字段猜测动作语义。
type ShieldedAction struct {
	EnabledSpend   bool
	EnabledOutput  bool
	Nullifier      Hash
	NoteCommitment Hash
	EphemeralKey   []byte
	EncryptedNote  []byte
}

// ShieldedBundle 表示交易中的统一屏蔽授权数据 + 同时覆盖入池、出池和池内转账。
type ShieldedBundle struct {
	ProtocolVersion   uint16
	ConsensusBranchID uint32
	Anchor            Hash
	Actions           []ShieldedAction
	ValueBalance      int64
	Proof             []byte
}

// ShieldedPoolState 表示全局屏蔽池共识状态 + 仅保存承诺树、锚点和池内资产总额。
type ShieldedPoolState struct {
	StateEncodingVersion uint16
	ProtocolVersion      uint16
	CircuitID            uint32
	VerifyingKeyHash     Hash
	TreeDepth            uint8
	LeafCount            uint64
	Frontier             []Hash
	CurrentRoot          Hash
	RecentAnchors        []Hash
	PoolLamports         uint64
}

// Validate 校验屏蔽动作 + 虚拟侧必须完全为空，启用侧必须携带非零密码学字段。
func (action ShieldedAction) Validate() error {
	if !action.EnabledSpend && !action.EnabledOutput {
		return fmt.Errorf("%w: spend and output are both disabled", ErrInvalidShieldedAction)
	}
	if action.EnabledSpend {
		if action.Nullifier.IsZero() {
			return fmt.Errorf("%w: nullifier is zero", ErrInvalidShieldedAction)
		}
	} else if !action.Nullifier.IsZero() {
		return fmt.Errorf("%w: disabled spend contains spend fields", ErrInvalidShieldedAction)
	}

	if action.EnabledOutput {
		if action.NoteCommitment.IsZero() {
			return fmt.Errorf("%w: note commitment is zero", ErrInvalidShieldedAction)
		}
		if err := validateShieldedFixedBytes(action.EphemeralKey, ShieldedEphemeralKeySize, "ephemeral key", ErrInvalidShieldedAction); err != nil {
			return err
		}
		if err := validateShieldedVariableBytes(action.EncryptedNote, MaxShieldedEncryptedNoteBytes, "encrypted note", ErrInvalidShieldedAction); err != nil {
			return err
		}
	} else if !action.NoteCommitment.IsZero() || len(action.EphemeralKey) != 0 || len(action.EncryptedNote) != 0 {
		return fmt.Errorf("%w: disabled output contains output fields", ErrInvalidShieldedAction)
	}
	return nil
}

// Clone 深拷贝屏蔽动作 + 防止证明构造和交易缓存共享可变切片。
func (action ShieldedAction) Clone() ShieldedAction {
	return ShieldedAction{
		EnabledSpend:   action.EnabledSpend,
		EnabledOutput:  action.EnabledOutput,
		Nullifier:      action.Nullifier,
		NoteCommitment: action.NoteCommitment,
		EphemeralKey:   utils.CloneBytes(action.EphemeralKey),
		EncryptedNote:  utils.CloneBytes(action.EncryptedNote),
	}
}

// MarshalBinary 序列化屏蔽动作 + 使用固定字段顺序产生确定性 Borsh 字节。
func (action ShieldedAction) MarshalBinary() ([]byte, error) {
	if err := action.Validate(); err != nil {
		return nil, err
	}
	writer := borsh.NewWriter(MaxShieldedActionBytes)
	if err := writeShieldedAction(writer, action); err != nil {
		return nil, err
	}
	return finishShieldedEncoding(writer, MaxShieldedActionBytes, "shielded action", ErrInvalidShieldedAction)
}

// UnmarshalShieldedActionBinary 反序列化屏蔽动作 + 拒绝未知尾随字节和非规范虚拟字段。
func UnmarshalShieldedActionBinary(data []byte) (ShieldedAction, error) {
	if err := validateShieldedInputSize(data, MaxShieldedActionBytes, "shielded action", ErrInvalidShieldedAction); err != nil {
		return ShieldedAction{}, err
	}
	reader := borsh.NewReader(data, MaxShieldedActionBytes)
	action, err := readShieldedAction(reader)
	if err != nil {
		return ShieldedAction{}, err
	}
	if err := reader.EnsureEOF(); err != nil {
		return ShieldedAction{}, fmt.Errorf("structure: decode shielded action eof: %w", err)
	}
	return action, action.Validate()
}

// UnmarshalBinary 将 Borsh 字节写入屏蔽动作 + 解码失败时保持接收者不变。
func (action *ShieldedAction) UnmarshalBinary(data []byte) error {
	if action == nil {
		return fmt.Errorf("%w: receiver is nil", ErrInvalidShieldedAction)
	}
	decoded, err := UnmarshalShieldedActionBinary(data)
	if err != nil {
		return err
	}
	*action = decoded
	return nil
}

// Validate 校验屏蔽 bundle + 约束版本、动作唯一性、证明和绑定签名。
func (bundle ShieldedBundle) Validate() error {
	if bundle.ProtocolVersion != ShieldedProtocolVersion {
		return fmt.Errorf("%w: unsupported protocol version %d", ErrInvalidShieldedBundle, bundle.ProtocolVersion)
	}
	if bundle.ConsensusBranchID == 0 {
		return fmt.Errorf("%w: consensus branch id is zero", ErrInvalidShieldedBundle)
	}
	if bundle.Anchor.IsZero() {
		return fmt.Errorf("%w: anchor is zero", ErrInvalidShieldedBundle)
	}
	if len(bundle.Actions) == 0 || len(bundle.Actions) > MaxShieldedActionsPerBundle {
		return fmt.Errorf("%w: action count %d is outside [1,%d]", ErrInvalidShieldedBundle, len(bundle.Actions), MaxShieldedActionsPerBundle)
	}

	nullifiers := make(map[Hash]struct{}, len(bundle.Actions))
	commitments := make(map[Hash]struct{}, len(bundle.Actions))
	for index, action := range bundle.Actions {
		if err := action.Validate(); err != nil {
			return fmt.Errorf("%w: action %d: %w", ErrInvalidShieldedBundle, index, err)
		}
		if action.EnabledSpend {
			if _, exists := nullifiers[action.Nullifier]; exists {
				return fmt.Errorf("%w: duplicate nullifier at action %d", ErrInvalidShieldedBundle, index)
			}
			nullifiers[action.Nullifier] = struct{}{}
		}
		if action.EnabledOutput {
			if _, exists := commitments[action.NoteCommitment]; exists {
				return fmt.Errorf("%w: duplicate note commitment at action %d", ErrInvalidShieldedBundle, index)
			}
			commitments[action.NoteCommitment] = struct{}{}
		}
	}
	if err := validateShieldedFixedBytes(bundle.Proof, ShieldedGroth16ProofBytes, "proof", ErrInvalidShieldedBundle); err != nil {
		return err
	}
	return nil
}

// Clone 深拷贝屏蔽 bundle + 保证签名摘要计算不受调用方后续修改影响。
func (bundle ShieldedBundle) Clone() ShieldedBundle {
	clonedActions := make([]ShieldedAction, len(bundle.Actions))
	for index, action := range bundle.Actions {
		clonedActions[index] = action.Clone()
	}
	return ShieldedBundle{
		ProtocolVersion:   bundle.ProtocolVersion,
		ConsensusBranchID: bundle.ConsensusBranchID,
		Anchor:            bundle.Anchor,
		Actions:           clonedActions,
		ValueBalance:      bundle.ValueBalance,
		Proof:             utils.CloneBytes(bundle.Proof),
	}
}

// MarshalBinary 序列化屏蔽 bundle + action 按 Borsh Vec<struct> 连续编码。
func (bundle ShieldedBundle) MarshalBinary() ([]byte, error) {
	if err := bundle.Validate(); err != nil {
		return nil, err
	}
	writer := borsh.NewWriter(MaxShieldedBundleBytes)
	writer.WriteUint16(bundle.ProtocolVersion)
	writer.WriteUint32(bundle.ConsensusBranchID)
	writer.WriteFixedBytes(bundle.Anchor[:])
	writer.WriteUint32(uint32(len(bundle.Actions)))
	for index, action := range bundle.Actions {
		if err := writeShieldedAction(writer, action); err != nil {
			return nil, fmt.Errorf("structure: encode shielded bundle action %d: %w", index, err)
		}
	}
	writer.WriteInt64(bundle.ValueBalance)
	if err := writer.WriteBytes(bundle.Proof); err != nil {
		return nil, fmt.Errorf("structure: encode shielded bundle proof: %w", err)
	}
	return finishShieldedEncoding(writer, MaxShieldedBundleBytes, "shielded bundle", ErrInvalidShieldedBundle)
}

// UnmarshalShieldedBundleBinary 反序列化屏蔽 bundle + 在分配 action 前检查数量上限。
func UnmarshalShieldedBundleBinary(data []byte) (ShieldedBundle, error) {
	if err := validateShieldedInputSize(data, MaxShieldedBundleBytes, "shielded bundle", ErrInvalidShieldedBundle); err != nil {
		return ShieldedBundle{}, err
	}
	reader := borsh.NewReader(data, MaxShieldedBundleBytes)
	protocolVersion, err := reader.ReadUint16()
	if err != nil {
		return ShieldedBundle{}, fmt.Errorf("structure: decode shielded bundle protocol version: %w", err)
	}
	consensusBranchID, err := reader.ReadUint32()
	if err != nil {
		return ShieldedBundle{}, fmt.Errorf("structure: decode shielded bundle consensus branch id: %w", err)
	}
	anchor, err := readShieldedHash(reader, "shielded bundle anchor")
	if err != nil {
		return ShieldedBundle{}, err
	}
	actionCount, err := reader.ReadUint32()
	if err != nil {
		return ShieldedBundle{}, fmt.Errorf("structure: decode shielded bundle action count: %w", err)
	}
	if actionCount == 0 || actionCount > uint32(MaxShieldedActionsPerBundle) {
		return ShieldedBundle{}, fmt.Errorf("%w: action count %d is outside [1,%d]", ErrInvalidShieldedBundle, actionCount, MaxShieldedActionsPerBundle)
	}
	actions := make([]ShieldedAction, int(actionCount))
	for index := range actions {
		actions[index], err = readShieldedAction(reader)
		if err != nil {
			return ShieldedBundle{}, fmt.Errorf("structure: decode shielded bundle action %d: %w", index, err)
		}
		if err := actions[index].Validate(); err != nil {
			return ShieldedBundle{}, fmt.Errorf("%w: action %d: %w", ErrInvalidShieldedBundle, index, err)
		}
	}
	valueBalance, err := reader.ReadInt64()
	if err != nil {
		return ShieldedBundle{}, fmt.Errorf("structure: decode shielded bundle value balance: %w", err)
	}
	proof, err := readShieldedBytes(reader, MaxShieldedProofBytes, "shielded bundle proof", ErrInvalidShieldedBundle)
	if err != nil {
		return ShieldedBundle{}, err
	}
	if err := reader.EnsureEOF(); err != nil {
		return ShieldedBundle{}, fmt.Errorf("structure: decode shielded bundle eof: %w", err)
	}
	bundle := ShieldedBundle{
		ProtocolVersion:   protocolVersion,
		ConsensusBranchID: consensusBranchID,
		Anchor:            anchor,
		Actions:           actions,
		ValueBalance:      valueBalance,
		Proof:             proof,
	}
	return bundle, bundle.Validate()
}

// UnmarshalBinary 将 Borsh 字节写入屏蔽 bundle + 解码失败时保持接收者不变。
func (bundle *ShieldedBundle) UnmarshalBinary(data []byte) error {
	if bundle == nil {
		return fmt.Errorf("%w: receiver is nil", ErrInvalidShieldedBundle)
	}
	decoded, err := UnmarshalShieldedBundleBinary(data)
	if err != nil {
		return err
	}
	*bundle = decoded
	return nil
}

// Validate 校验屏蔽池状态 + 防止版本混用、树越界和无效锚点进入共识状态。
func (state ShieldedPoolState) Validate() error {
	if state.StateEncodingVersion != ShieldedPoolStateEncodingVersion {
		return fmt.Errorf("%w: unsupported state encoding version %d", ErrInvalidShieldedPoolState, state.StateEncodingVersion)
	}
	if state.ProtocolVersion != ShieldedProtocolVersion {
		return fmt.Errorf("%w: unsupported protocol version %d", ErrInvalidShieldedPoolState, state.ProtocolVersion)
	}
	if state.CircuitID == 0 {
		return fmt.Errorf("%w: circuit id is zero", ErrInvalidShieldedPoolState)
	}
	if state.VerifyingKeyHash.IsZero() {
		return fmt.Errorf("%w: verifying key hash is zero", ErrInvalidShieldedPoolState)
	}
	if state.TreeDepth == 0 || state.TreeDepth > MaxShieldedTreeDepth {
		return fmt.Errorf("%w: tree depth %d is outside [1,%d]", ErrInvalidShieldedPoolState, state.TreeDepth, MaxShieldedTreeDepth)
	}
	if state.TreeDepth < 64 && state.LeafCount > uint64(1)<<state.TreeDepth {
		return fmt.Errorf("%w: leaf count %d exceeds tree capacity", ErrInvalidShieldedPoolState, state.LeafCount)
	}
	if len(state.Frontier) != int(state.TreeDepth) {
		return fmt.Errorf("%w: frontier length %d does not match tree depth %d", ErrInvalidShieldedPoolState, len(state.Frontier), state.TreeDepth)
	}
	capacity := uint64(0)
	if state.TreeDepth < 64 {
		capacity = uint64(1) << state.TreeDepth
	}
	for index, node := range state.Frontier {
		nodeExpected := state.LeafCount != capacity && state.LeafCount&(uint64(1)<<index) != 0
		if nodeExpected == node.IsZero() {
			return fmt.Errorf("%w: frontier node %d presence does not match leaf count", ErrInvalidShieldedPoolState, index)
		}
	}
	if state.CurrentRoot.IsZero() {
		return fmt.Errorf("%w: current root is zero", ErrInvalidShieldedPoolState)
	}
	if len(state.RecentAnchors) > MaxShieldedRecentAnchors {
		return fmt.Errorf("%w: recent anchor count %d exceeds %d", ErrInvalidShieldedPoolState, len(state.RecentAnchors), MaxShieldedRecentAnchors)
	}
	anchors := make(map[Hash]struct{}, len(state.RecentAnchors))
	for index, anchor := range state.RecentAnchors {
		if anchor.IsZero() {
			return fmt.Errorf("%w: recent anchor %d is zero", ErrInvalidShieldedPoolState, index)
		}
		if _, exists := anchors[anchor]; exists {
			return fmt.Errorf("%w: duplicate recent anchor at index %d", ErrInvalidShieldedPoolState, index)
		}
		anchors[anchor] = struct{}{}
	}
	return nil
}

// Clone 深拷贝屏蔽池状态 + 防止分叉缓存共享 frontier 和 anchor 切片。
func (state ShieldedPoolState) Clone() ShieldedPoolState {
	return ShieldedPoolState{
		StateEncodingVersion: state.StateEncodingVersion,
		ProtocolVersion:      state.ProtocolVersion,
		CircuitID:            state.CircuitID,
		VerifyingKeyHash:     state.VerifyingKeyHash,
		TreeDepth:            state.TreeDepth,
		LeafCount:            state.LeafCount,
		Frontier:             cloneShieldedHashes(state.Frontier),
		CurrentRoot:          state.CurrentRoot,
		RecentAnchors:        cloneShieldedHashes(state.RecentAnchors),
		PoolLamports:         state.PoolLamports,
	}
}

// MarshalBinary 序列化屏蔽池状态 + 对哈希向量使用 Borsh 长度前缀和固定元素编码。
func (state ShieldedPoolState) MarshalBinary() ([]byte, error) {
	if err := state.Validate(); err != nil {
		return nil, err
	}
	writer := borsh.NewWriter(MaxShieldedPoolStateBytes)
	writer.WriteUint16(state.StateEncodingVersion)
	writer.WriteUint16(state.ProtocolVersion)
	writer.WriteUint32(state.CircuitID)
	writer.WriteFixedBytes(state.VerifyingKeyHash[:])
	writer.WriteUint8(state.TreeDepth)
	writer.WriteUint64(state.LeafCount)
	writeShieldedHashes(writer, state.Frontier)
	writer.WriteFixedBytes(state.CurrentRoot[:])
	writeShieldedHashes(writer, state.RecentAnchors)
	writer.WriteUint64(state.PoolLamports)
	return finishShieldedEncoding(writer, MaxShieldedPoolStateBytes, "shielded pool state", ErrInvalidShieldedPoolState)
}

// UnmarshalShieldedPoolStateBinary 反序列化屏蔽池状态 + 对向量计数、固定哈希和尾部字节执行严格检查。
func UnmarshalShieldedPoolStateBinary(data []byte) (ShieldedPoolState, error) {
	if err := validateShieldedInputSize(data, MaxShieldedPoolStateBytes, "shielded pool state", ErrInvalidShieldedPoolState); err != nil {
		return ShieldedPoolState{}, err
	}
	reader := borsh.NewReader(data, MaxShieldedPoolStateBytes)
	stateEncodingVersion, err := reader.ReadUint16()
	if err != nil {
		return ShieldedPoolState{}, fmt.Errorf("structure: decode shielded state encoding version: %w", err)
	}
	protocolVersion, err := reader.ReadUint16()
	if err != nil {
		return ShieldedPoolState{}, fmt.Errorf("structure: decode shielded state protocol version: %w", err)
	}
	circuitID, err := reader.ReadUint32()
	if err != nil {
		return ShieldedPoolState{}, fmt.Errorf("structure: decode shielded state circuit id: %w", err)
	}
	verifyingKeyHash, err := readShieldedHash(reader, "shielded state verifying key hash")
	if err != nil {
		return ShieldedPoolState{}, err
	}
	treeDepth, err := reader.ReadUint8()
	if err != nil {
		return ShieldedPoolState{}, fmt.Errorf("structure: decode shielded state tree depth: %w", err)
	}
	leafCount, err := reader.ReadUint64()
	if err != nil {
		return ShieldedPoolState{}, fmt.Errorf("structure: decode shielded state leaf count: %w", err)
	}
	frontier, err := readShieldedHashes(reader, MaxShieldedTreeDepth, "shielded state frontier", ErrInvalidShieldedPoolState)
	if err != nil {
		return ShieldedPoolState{}, err
	}
	currentRoot, err := readShieldedHash(reader, "shielded state current root")
	if err != nil {
		return ShieldedPoolState{}, err
	}
	recentAnchors, err := readShieldedHashes(reader, MaxShieldedRecentAnchors, "shielded state recent anchors", ErrInvalidShieldedPoolState)
	if err != nil {
		return ShieldedPoolState{}, err
	}
	poolLamports, err := reader.ReadUint64()
	if err != nil {
		return ShieldedPoolState{}, fmt.Errorf("structure: decode shielded state pool lamports: %w", err)
	}
	if err := reader.EnsureEOF(); err != nil {
		return ShieldedPoolState{}, fmt.Errorf("structure: decode shielded pool state eof: %w", err)
	}
	state := ShieldedPoolState{
		StateEncodingVersion: stateEncodingVersion,
		ProtocolVersion:      protocolVersion,
		CircuitID:            circuitID,
		VerifyingKeyHash:     verifyingKeyHash,
		TreeDepth:            treeDepth,
		LeafCount:            leafCount,
		Frontier:             frontier,
		CurrentRoot:          currentRoot,
		RecentAnchors:        recentAnchors,
		PoolLamports:         poolLamports,
	}
	return state, state.Validate()
}

// UnmarshalBinary 将 Borsh 字节写入屏蔽池状态 + 解码失败时保持接收者不变。
func (state *ShieldedPoolState) UnmarshalBinary(data []byte) error {
	if state == nil {
		return fmt.Errorf("%w: receiver is nil", ErrInvalidShieldedPoolState)
	}
	decoded, err := UnmarshalShieldedPoolStateBinary(data)
	if err != nil {
		return err
	}
	*state = decoded
	return nil
}

func writeShieldedAction(writer *borsh.Writer, action ShieldedAction) error {
	writer.WriteBool(action.EnabledSpend)
	writer.WriteBool(action.EnabledOutput)
	writer.WriteFixedBytes(action.Nullifier[:])
	writer.WriteFixedBytes(action.NoteCommitment[:])
	if err := writer.WriteBytes(action.EphemeralKey); err != nil {
		return fmt.Errorf("structure: encode shielded ephemeral key: %w", err)
	}
	if err := writer.WriteBytes(action.EncryptedNote); err != nil {
		return fmt.Errorf("structure: encode shielded encrypted note: %w", err)
	}
	return nil
}

func readShieldedAction(reader *borsh.Reader) (ShieldedAction, error) {
	enabledSpend, err := reader.ReadBool()
	if err != nil {
		return ShieldedAction{}, fmt.Errorf("structure: decode shielded enabled spend: %w", err)
	}
	enabledOutput, err := reader.ReadBool()
	if err != nil {
		return ShieldedAction{}, fmt.Errorf("structure: decode shielded enabled output: %w", err)
	}
	nullifier, err := readShieldedHash(reader, "shielded nullifier")
	if err != nil {
		return ShieldedAction{}, err
	}
	noteCommitment, err := readShieldedHash(reader, "shielded note commitment")
	if err != nil {
		return ShieldedAction{}, err
	}
	ephemeralKey, err := readShieldedBytes(reader, ShieldedEphemeralKeySize, "shielded ephemeral key", ErrInvalidShieldedAction)
	if err != nil {
		return ShieldedAction{}, err
	}
	encryptedNote, err := readShieldedBytes(reader, MaxShieldedEncryptedNoteBytes, "shielded encrypted note", ErrInvalidShieldedAction)
	if err != nil {
		return ShieldedAction{}, err
	}
	return ShieldedAction{
		EnabledSpend:   enabledSpend,
		EnabledOutput:  enabledOutput,
		Nullifier:      nullifier,
		NoteCommitment: noteCommitment,
		EphemeralKey:   normalizeShieldedEmptyBytes(ephemeralKey),
		EncryptedNote:  normalizeShieldedEmptyBytes(encryptedNote),
	}, nil
}

func writeShieldedHashes(writer *borsh.Writer, hashes []Hash) {
	writer.WriteUint32(uint32(len(hashes)))
	for _, hash := range hashes {
		writer.WriteFixedBytes(hash[:])
	}
}

func readShieldedHashes(reader *borsh.Reader, maximum int, field string, baseError error) ([]Hash, error) {
	count, err := reader.ReadUint32()
	if err != nil {
		return nil, fmt.Errorf("structure: decode %s count: %w", field, err)
	}
	if count > uint32(maximum) {
		return nil, fmt.Errorf("%w: %s count %d exceeds %d", baseError, field, count, maximum)
	}
	if count == 0 {
		return nil, nil
	}
	hashes := make([]Hash, int(count))
	for index := range hashes {
		hashes[index], err = readShieldedHash(reader, fmt.Sprintf("%s %d", field, index))
		if err != nil {
			return nil, err
		}
	}
	return hashes, nil
}

func readShieldedHash(reader *borsh.Reader, field string) (Hash, error) {
	encoded, err := reader.ReadFixedBytes(HashSize)
	if err != nil {
		return Hash{}, fmt.Errorf("structure: decode %s: %w", field, err)
	}
	var hash Hash
	copy(hash[:], encoded)
	return hash, nil
}

func readShieldedBytes(reader *borsh.Reader, maximum int, field string, baseError error) ([]byte, error) {
	length, err := reader.ReadUint32()
	if err != nil {
		return nil, fmt.Errorf("structure: decode %s length: %w", field, err)
	}
	if length > uint32(maximum) {
		return nil, fmt.Errorf("%w: %s length %d exceeds %d", baseError, field, length, maximum)
	}
	value, err := reader.ReadFixedBytes(int(length))
	if err != nil {
		return nil, fmt.Errorf("structure: decode %s: %w", field, err)
	}
	return value, nil
}

func validateShieldedFixedBytes(value []byte, expected int, field string, baseError error) error {
	if len(value) != expected {
		return fmt.Errorf("%w: %s length %d, want %d", baseError, field, len(value), expected)
	}
	if shieldedBytesAreZero(value) {
		return fmt.Errorf("%w: %s is zero", baseError, field)
	}
	return nil
}

func validateShieldedVariableBytes(value []byte, maximum int, field string, baseError error) error {
	if len(value) == 0 {
		return fmt.Errorf("%w: %s is empty", baseError, field)
	}
	if len(value) > maximum {
		return fmt.Errorf("%w: %s length %d exceeds %d", baseError, field, len(value), maximum)
	}
	if shieldedBytesAreZero(value) {
		return fmt.Errorf("%w: %s is zero", baseError, field)
	}
	return nil
}

func validateShieldedInputSize(data []byte, maximum int, field string, baseError error) error {
	if len(data) == 0 {
		return fmt.Errorf("%w: %s bytes are empty", baseError, field)
	}
	if len(data) > maximum {
		return fmt.Errorf("%w: %s bytes length %d exceeds %d", baseError, field, len(data), maximum)
	}
	return nil
}

func finishShieldedEncoding(writer *borsh.Writer, maximum int, field string, baseError error) ([]byte, error) {
	encoded := writer.Bytes()
	if len(encoded) > maximum {
		return nil, fmt.Errorf("%w: %s encoded length %d exceeds %d", baseError, field, len(encoded), maximum)
	}
	return encoded, nil
}

func shieldedBytesAreZero(value []byte) bool {
	for _, current := range value {
		if current != 0 {
			return false
		}
	}
	return true
}

func normalizeShieldedEmptyBytes(value []byte) []byte {
	if len(value) == 0 {
		return nil
	}
	return value
}

func cloneShieldedHashes(hashes []Hash) []Hash {
	if hashes == nil {
		return nil
	}
	cloned := make([]Hash, len(hashes))
	copy(cloned, hashes)
	return cloned
}
