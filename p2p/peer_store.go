package p2p

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"solana_golang/codec/borsh"
	"solana_golang/utils"
)

const (
	// PeerStoreRecordVersion 定义 PeerStore 记录版本 + 便于后续存储格式平滑升级。
	PeerStoreRecordVersion   uint16 = 4
	peerStoreRecordVersionV3 uint16 = 3

	defaultPeerStoreLoadLimit = 1024
	maxPeerStoreAddresses     = 16
	maxPeerStoreProtocols     = 8
	maxPeerStoreMetadata      = 64
)

// PeerStore 定义节点持久化接口 + 让 P2P 核心隔离具体数据库实现。
type PeerStore interface {
	LoadPeers(ctx context.Context, limit int) ([]Peer, error)
	SavePeer(ctx context.Context, peer Peer) error
	DeletePeer(ctx context.Context, peerID string) error
}

// MemoryPeerStore 保存内存节点记录 + 供测试和无数据库嵌入场景使用。
type MemoryPeerStore struct {
	mutex sync.RWMutex
	peers map[string]Peer
}

// NewMemoryPeerStore 创建内存 PeerStore + 避免测试依赖磁盘数据库。
func NewMemoryPeerStore() *MemoryPeerStore {
	return &MemoryPeerStore{peers: make(map[string]Peer)}
}

// LoadPeers 读取内存节点记录 + 按 peer id 稳定排序保证测试可重复。
func (store *MemoryPeerStore) LoadPeers(ctx context.Context, limit int) ([]Peer, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limit = normalizePeerStoreLimit(limit)

	store.mutex.RLock()
	defer store.mutex.RUnlock()
	peerIDs := make([]string, 0, len(store.peers))
	for peerID := range store.peers {
		peerIDs = append(peerIDs, peerID)
	}
	sort.Strings(peerIDs)

	peers := make([]Peer, 0, minInt(limit, len(peerIDs)))
	for _, peerID := range peerIDs {
		if len(peers) >= limit {
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		peers = append(peers, store.peers[peerID].Clone())
	}
	return peers, nil
}

// SavePeer 写入内存节点记录 + 复制入参避免调用方后续修改污染存储。
func (store *MemoryPeerStore) SavePeer(ctx context.Context, peer Peer) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := peer.Validate(); err != nil {
		return err
	}

	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.peers[peer.ID] = peer.Clone()
	return nil
}

// DeletePeer 删除内存节点记录 + 用于屏蔽或清理不可用节点。
func (store *MemoryPeerStore) DeletePeer(ctx context.Context, peerID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validatePeerID(peerID); err != nil {
		return err
	}

	store.mutex.Lock()
	defer store.mutex.Unlock()
	delete(store.peers, peerID)
	return nil
}

// MarshalBinary 序列化 Peer + PeerStore 持久化统一使用 Borsh 布局。
func (peer Peer) MarshalBinary() ([]byte, error) {
	peer = normalizePeerForStorage(peer)
	if err := peer.Validate(); err != nil {
		return nil, err
	}

	writer := borsh.NewWriter(DefaultMaxMessageSize)
	writer.WriteUint16(PeerStoreRecordVersion)
	if err := writer.WriteString(peer.ID); err != nil {
		return nil, fmt.Errorf("p2p: marshal peer id: %w", err)
	}
	if err := writePeerAddressSlice(writer, peer.advertisedAddressList()); err != nil {
		return nil, err
	}
	if err := writer.WriteString(string(peer.Status)); err != nil {
		return nil, fmt.Errorf("p2p: marshal peer status: %w", err)
	}
	if err := writer.WriteString(string(peer.Role)); err != nil {
		return nil, fmt.Errorf("p2p: marshal peer role: %w", err)
	}
	writer.WriteUint64(uint64(peer.Capabilities))
	if err := writer.WriteString(peer.ProtocolVersion); err != nil {
		return nil, fmt.Errorf("p2p: marshal peer protocol version: %w", err)
	}
	if err := writer.WriteString(peer.SoftwareVersion); err != nil {
		return nil, fmt.Errorf("p2p: marshal peer software version: %w", err)
	}
	if err := writePeerProtocolSlice(writer, peer.PreferredProtocols); err != nil {
		return nil, err
	}
	if err := writePeerAddressSlice(writer, peer.VerifiedAddresses); err != nil {
		return nil, err
	}
	writer.WriteUint64(peer.LatestSlot)
	writer.WriteUint64(peer.BlockHeight)
	if err := writer.WriteString(peer.BestBlockHash); err != nil {
		return nil, fmt.Errorf("p2p: marshal peer best block hash: %w", err)
	}
	writer.WriteBool(peer.Validator)
	writer.WriteUint64(peer.StakeLamports)
	writer.WriteInt64(int64(peer.Score))
	writer.WriteInt64(peer.FirstSeenUnixMilli)
	writer.WriteInt64(peer.LastSeenUnixMilli)
	writer.WriteInt64(peer.LastConnectedUnixMilli)
	writer.WriteInt64(peer.LastDisconnectedUnixMilli)
	writer.WriteInt64(peer.LastErrorUnixMilli)
	if err := writer.WriteString(peer.LastError); err != nil {
		return nil, fmt.Errorf("p2p: marshal peer last error: %w", err)
	}
	writer.WriteUint32(peer.FailureCount)
	writer.WriteUint64(peer.SentBytes)
	writer.WriteUint64(peer.ReceivedBytes)
	writer.WriteInt64(peer.LastRoundTripTimeMilli)
	if err := writePeerMetadata(writer, peer.Metadata); err != nil {
		return nil, err
	}
	if err := writePeerSignedRecord(writer, peer.SignedRecord); err != nil {
		return nil, err
	}
	return writer.BytesView(), nil
}

// UnmarshalPeerBinary 反序列化 Peer + 读取后立即校验地址归属和字段边界。
func UnmarshalPeerBinary(data []byte) (Peer, error) {
	reader := borsh.NewBorrowedReader(data, DefaultMaxMessageSize)
	version, err := reader.ReadUint16()
	if err != nil {
		return Peer{}, fmt.Errorf("p2p: read peer version: %w", err)
	}
	if version != 1 && version != 2 && version != peerStoreRecordVersionV3 && version != PeerStoreRecordVersion {
		return Peer{}, fmt.Errorf("%w: unsupported peer store version", ErrInvalidMessage)
	}
	peerID, err := reader.ReadString()
	if err != nil {
		return Peer{}, fmt.Errorf("p2p: read peer id: %w", err)
	}
	addresses, err := readPeerAddressSlice(reader)
	if err != nil {
		return Peer{}, err
	}
	status, err := reader.ReadString()
	if err != nil {
		return Peer{}, fmt.Errorf("p2p: read peer status: %w", err)
	}
	role, err := reader.ReadString()
	if err != nil {
		return Peer{}, fmt.Errorf("p2p: read peer role: %w", err)
	}
	capabilities, err := reader.ReadUint64()
	if err != nil {
		return Peer{}, fmt.Errorf("p2p: read peer capabilities: %w", err)
	}
	protocolVersion, err := reader.ReadString()
	if err != nil {
		return Peer{}, fmt.Errorf("p2p: read peer protocol version: %w", err)
	}
	softwareVersion, err := reader.ReadString()
	if err != nil {
		return Peer{}, fmt.Errorf("p2p: read peer software version: %w", err)
	}
	var preferredProtocols []utils.MultiAddressProtocol
	if version >= peerStoreRecordVersionV3 {
		preferredProtocols, err = readPeerProtocolSlice(reader)
		if err != nil {
			return Peer{}, err
		}
	}
	var verifiedAddresses []utils.MultiAddress
	if version >= PeerStoreRecordVersion {
		verifiedAddresses, err = readPeerAddressSlice(reader)
		if err != nil {
			return Peer{}, err
		}
	}
	latestSlot, err := reader.ReadUint64()
	if err != nil {
		return Peer{}, fmt.Errorf("p2p: read peer latest slot: %w", err)
	}
	blockHeight, err := reader.ReadUint64()
	if err != nil {
		return Peer{}, fmt.Errorf("p2p: read peer block height: %w", err)
	}
	bestBlockHash, err := reader.ReadString()
	if err != nil {
		return Peer{}, fmt.Errorf("p2p: read peer best block hash: %w", err)
	}
	validator, err := reader.ReadBool()
	if err != nil {
		return Peer{}, fmt.Errorf("p2p: read peer validator: %w", err)
	}
	stakeLamports, err := reader.ReadUint64()
	if err != nil {
		return Peer{}, fmt.Errorf("p2p: read peer stake: %w", err)
	}
	score, err := reader.ReadInt64()
	if err != nil {
		return Peer{}, fmt.Errorf("p2p: read peer score: %w", err)
	}
	firstSeenUnixMilli, err := reader.ReadInt64()
	if err != nil {
		return Peer{}, fmt.Errorf("p2p: read peer first seen: %w", err)
	}
	lastSeenUnixMilli, err := reader.ReadInt64()
	if err != nil {
		return Peer{}, fmt.Errorf("p2p: read peer last seen: %w", err)
	}
	lastConnectedUnixMilli, err := reader.ReadInt64()
	if err != nil {
		return Peer{}, fmt.Errorf("p2p: read peer last connected: %w", err)
	}
	lastDisconnectedUnixMilli, err := reader.ReadInt64()
	if err != nil {
		return Peer{}, fmt.Errorf("p2p: read peer last disconnected: %w", err)
	}
	lastErrorUnixMilli, err := reader.ReadInt64()
	if err != nil {
		return Peer{}, fmt.Errorf("p2p: read peer last error time: %w", err)
	}
	lastError, err := reader.ReadString()
	if err != nil {
		return Peer{}, fmt.Errorf("p2p: read peer last error: %w", err)
	}
	failureCount, err := reader.ReadUint32()
	if err != nil {
		return Peer{}, fmt.Errorf("p2p: read peer failure count: %w", err)
	}
	sentBytes, err := reader.ReadUint64()
	if err != nil {
		return Peer{}, fmt.Errorf("p2p: read peer sent bytes: %w", err)
	}
	receivedBytes, err := reader.ReadUint64()
	if err != nil {
		return Peer{}, fmt.Errorf("p2p: read peer received bytes: %w", err)
	}
	lastRoundTripTimeMilli, err := reader.ReadInt64()
	if err != nil {
		return Peer{}, fmt.Errorf("p2p: read peer round trip: %w", err)
	}
	metadata, err := readPeerMetadata(reader)
	if err != nil {
		return Peer{}, err
	}
	var signedRecord []byte
	if version >= 2 {
		signedRecord, err = readPeerSignedRecord(reader)
		if err != nil {
			return Peer{}, err
		}
	}
	if err := reader.EnsureEOF(); err != nil {
		return Peer{}, fmt.Errorf("p2p: read peer eof: %w", err)
	}

	peer := Peer{
		ID:                        peerID,
		AdvertisedAddresses:       addresses,
		VerifiedAddresses:         verifiedAddresses,
		Addresses:                 addresses,
		Status:                    PeerStatus(status),
		Role:                      PeerRole(role),
		Capabilities:              PeerCapability(capabilities),
		ProtocolVersion:           protocolVersion,
		SoftwareVersion:           softwareVersion,
		PreferredProtocols:        preferredProtocols,
		LatestSlot:                latestSlot,
		BlockHeight:               blockHeight,
		BestBlockHash:             bestBlockHash,
		Validator:                 validator,
		StakeLamports:             stakeLamports,
		Score:                     int(score),
		FirstSeenUnixMilli:        firstSeenUnixMilli,
		LastSeenUnixMilli:         lastSeenUnixMilli,
		LastConnectedUnixMilli:    lastConnectedUnixMilli,
		LastDisconnectedUnixMilli: lastDisconnectedUnixMilli,
		LastErrorUnixMilli:        lastErrorUnixMilli,
		LastError:                 lastError,
		FailureCount:              failureCount,
		SentBytes:                 sentBytes,
		ReceivedBytes:             receivedBytes,
		LastRoundTripTimeMilli:    lastRoundTripTimeMilli,
		SignedRecord:              signedRecord,
		Metadata:                  metadata,
	}
	peer = normalizePeerForStorage(peer)
	return peer, peer.Validate()
}

func writePeerAddressSlice(writer *borsh.Writer, addresses []utils.MultiAddress) error {
	if len(addresses) > maxPeerStoreAddresses {
		return fmt.Errorf("%w: too many peer addresses", ErrInvalidMessage)
	}
	writer.WriteUint32(uint32(len(addresses)))
	for _, address := range addresses {
		if err := writer.WriteString(address.String()); err != nil {
			return fmt.Errorf("p2p: marshal peer address: %w", err)
		}
	}
	return nil
}

func readPeerAddressSlice(reader *borsh.Reader) ([]utils.MultiAddress, error) {
	count, err := reader.ReadUint32()
	if err != nil {
		return nil, fmt.Errorf("p2p: read peer address count: %w", err)
	}
	if count > maxPeerStoreAddresses {
		return nil, fmt.Errorf("%w: too many peer addresses", ErrInvalidMessage)
	}
	addresses := make([]utils.MultiAddress, 0, int(count))
	for index := 0; index < int(count); index++ {
		rawAddress, err := reader.ReadString()
		if err != nil {
			return nil, fmt.Errorf("p2p: read peer address: %w", err)
		}
		address, err := utils.ParseMultiAddress(rawAddress)
		if err != nil {
			return nil, fmt.Errorf("p2p: parse peer address: %w", err)
		}
		addresses = append(addresses, address)
	}
	return addresses, nil
}

func writePeerProtocolSlice(writer *borsh.Writer, protocols []utils.MultiAddressProtocol) error {
	if len(protocols) > maxPeerStoreProtocols {
		return fmt.Errorf("%w: too many peer protocols", ErrInvalidMessage)
	}
	if err := validatePeerPreferredProtocols(protocols); err != nil {
		return err
	}
	writer.WriteUint32(uint32(len(protocols)))
	for _, protocol := range protocols {
		if err := writer.WriteString(string(protocol)); err != nil {
			return fmt.Errorf("p2p: marshal peer protocol: %w", err)
		}
	}
	return nil
}

func readPeerProtocolSlice(reader *borsh.Reader) ([]utils.MultiAddressProtocol, error) {
	count, err := reader.ReadUint32()
	if err != nil {
		return nil, fmt.Errorf("p2p: read peer protocol count: %w", err)
	}
	if count > maxPeerStoreProtocols {
		return nil, fmt.Errorf("%w: too many peer protocols", ErrInvalidMessage)
	}
	protocols := make([]utils.MultiAddressProtocol, 0, int(count))
	for index := 0; index < int(count); index++ {
		value, err := reader.ReadString()
		if err != nil {
			return nil, fmt.Errorf("p2p: read peer protocol: %w", err)
		}
		protocol, err := utils.ParseMultiAddressProtocol(value)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid peer protocol: %w", ErrInvalidMessage, err)
		}
		protocols = append(protocols, protocol)
	}
	return protocols, nil
}

func writePeerMetadata(writer *borsh.Writer, metadata map[string]string) error {
	if len(metadata) > maxPeerStoreMetadata {
		return fmt.Errorf("%w: too many peer metadata entries", ErrInvalidMessage)
	}
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	writer.WriteUint32(uint32(len(keys)))
	for _, key := range keys {
		if err := writer.WriteString(key); err != nil {
			return fmt.Errorf("p2p: marshal peer metadata key: %w", err)
		}
		if err := writer.WriteString(metadata[key]); err != nil {
			return fmt.Errorf("p2p: marshal peer metadata value: %w", err)
		}
	}
	return nil
}

func readPeerMetadata(reader *borsh.Reader) (map[string]string, error) {
	count, err := reader.ReadUint32()
	if err != nil {
		return nil, fmt.Errorf("p2p: read peer metadata count: %w", err)
	}
	if count > maxPeerStoreMetadata {
		return nil, fmt.Errorf("%w: too many peer metadata entries", ErrInvalidMessage)
	}
	metadata := make(map[string]string, int(count))
	for index := 0; index < int(count); index++ {
		key, err := reader.ReadString()
		if err != nil {
			return nil, fmt.Errorf("p2p: read peer metadata key: %w", err)
		}
		value, err := reader.ReadString()
		if err != nil {
			return nil, fmt.Errorf("p2p: read peer metadata value: %w", err)
		}
		metadata[key] = value
	}
	if len(metadata) == 0 {
		return nil, nil
	}
	return metadata, nil
}

func writePeerSignedRecord(writer *borsh.Writer, signedRecord []byte) error {
	if len(signedRecord) > maxPeerRecordSize {
		return fmt.Errorf("%w: signed peer record too large", ErrInvalidMessage)
	}
	if err := writer.WriteBytes(signedRecord); err != nil {
		return fmt.Errorf("p2p: marshal peer signed record: %w", err)
	}
	return nil
}

func readPeerSignedRecord(reader *borsh.Reader) ([]byte, error) {
	signedRecord, err := reader.ReadBytes()
	if err != nil {
		return nil, fmt.Errorf("p2p: read peer signed record: %w", err)
	}
	if len(signedRecord) > maxPeerRecordSize {
		return nil, fmt.Errorf("%w: signed peer record too large", ErrInvalidMessage)
	}
	return signedRecord, nil
}

func normalizePeerForStorage(peer Peer) Peer {
	advertisedAddresses := peer.advertisedAddressList()
	peer.AdvertisedAddresses = cloneAddresses(advertisedAddresses)
	peer.Addresses = cloneAddresses(advertisedAddresses)
	peer.VerifiedAddresses = cloneAddresses(peer.VerifiedAddresses)
	if peer.Status == "" {
		peer.Status = PeerStatusUnknown
	}
	if peer.Role == "" {
		peer.Role = PeerRoleUnknown
	}
	return peer
}

func normalizePeerStoreLimit(limit int) int {
	if limit <= 0 {
		return defaultPeerStoreLoadLimit
	}
	return limit
}

func normalizePeerStore(store PeerStore) PeerStore {
	if store == nil {
		return nil
	}
	return store
}

func minInt(first int, second int) int {
	if first < second {
		return first
	}
	return second
}

func peerShareableInDHT(peer Peer) bool {
	return peer.Status != PeerStatusBlocked && len(peer.advertisedAddressList()) > 0
}

func peerDialable(peer Peer, maxPeerFailures uint32) bool {
	if peer.Status == PeerStatusBlocked || !peer.hasDialableAddress() {
		return false
	}
	if maxPeerFailures > 0 && peer.FailureCount >= maxPeerFailures {
		return false
	}
	return true
}
