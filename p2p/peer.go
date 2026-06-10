package p2p

import (
	"fmt"
	"time"

	"solana_golang/utils"
)

const peerIDByteSize = 32

// PeerRole 表示节点角色 + 便于连接策略区分普通节点和验证节点。
type PeerRole string

const (
	// PeerRoleUnknown 表示未知角色 + 用于未完成握手的节点。
	PeerRoleUnknown PeerRole = "unknown"
	// PeerRoleFull 表示全节点 + 用于区块和状态同步。
	PeerRoleFull PeerRole = "full"
	// PeerRoleValidator 表示验证节点 + 用于共识消息优先连接。
	PeerRoleValidator PeerRole = "validator"
	// PeerRoleBootnode 表示引导节点 + 用于冷启动节点发现。
	PeerRoleBootnode PeerRole = "bootnode"
)

// PeerStatus 表示节点状态 + 供连接池和发现层判断节点可用性。
type PeerStatus string

const (
	// PeerStatusUnknown 表示未知状态 + 用于刚发现但未连接的节点。
	PeerStatusUnknown PeerStatus = "unknown"
	// PeerStatusConnected 表示已连接状态 + 用于优先复用现有连接。
	PeerStatusConnected PeerStatus = "connected"
	// PeerStatusDisconnected 表示断开状态 + 用于触发后续重连。
	PeerStatusDisconnected PeerStatus = "disconnected"
	// PeerStatusBlocked 表示已屏蔽状态 + 用于隔离异常或恶意节点。
	PeerStatusBlocked PeerStatus = "blocked"
)

// PeerCapability 表示节点能力位 + 用位图降低节点状态传播体积。
type PeerCapability uint64

const (
	// PeerCapabilityRelay 表示可转发消息 + 用于资源广播路径选择。
	PeerCapabilityRelay PeerCapability = 1 << iota
	// PeerCapabilityArchive 表示保存历史数据 + 用于历史同步节点选择。
	PeerCapabilityArchive
	// PeerCapabilityValidator 表示参与验证 + 用于共识消息优先级判断。
	PeerCapabilityValidator
	// PeerCapabilityStateSync 表示支持状态同步 + 用于快速追块。
	PeerCapabilityStateSync
	// PeerCapabilityDHT 表示支持 DHT 查询 + 用于节点发现。
	PeerCapabilityDHT
)

// Peer 保存节点信息 + 供 Host、连接池和 DHT 共享同一份节点描述。
type Peer struct {
	ID                        string
	Addresses                 []utils.MultiAddress
	Status                    PeerStatus
	Role                      PeerRole
	Capabilities              PeerCapability
	ProtocolVersion           string
	SoftwareVersion           string
	LatestSlot                uint64
	BlockHeight               uint64
	BestBlockHash             string
	Validator                 bool
	StakeLamports             uint64
	Score                     int
	FirstSeenUnixMilli        int64
	LastSeenUnixMilli         int64
	LastConnectedUnixMilli    int64
	LastDisconnectedUnixMilli int64
	LastErrorUnixMilli        int64
	LastError                 string
	FailureCount              uint32
	SentBytes                 uint64
	ReceivedBytes             uint64
	LastRoundTripTimeMilli    int64
	Metadata                  map[string]string
}

// PeerSnapshot 保存节点快照 + 供监控、调试和 DHT 状态导出。
type PeerSnapshot struct {
	ID                        string
	Addresses                 []utils.MultiAddress
	Status                    PeerStatus
	Role                      PeerRole
	Capabilities              PeerCapability
	ProtocolVersion           string
	SoftwareVersion           string
	LatestSlot                uint64
	BlockHeight               uint64
	BestBlockHash             string
	Validator                 bool
	StakeLamports             uint64
	Score                     int
	FirstSeenUnixMilli        int64
	LastSeenUnixMilli         int64
	LastConnectedUnixMilli    int64
	LastDisconnectedUnixMilli int64
	LastErrorUnixMilli        int64
	LastError                 string
	FailureCount              uint32
	SentBytes                 uint64
	ReceivedBytes             uint64
	LastRoundTripTimeMilli    int64
}

// NewPeer 创建节点信息 + 统一校验节点 ID 和地址归属。
func NewPeer(peerID string, addresses []utils.MultiAddress) (Peer, error) {
	now := time.Now().UnixMilli()
	peer := Peer{
		ID:                 peerID,
		Addresses:          cloneAddresses(addresses),
		Status:             PeerStatusUnknown,
		Role:               PeerRoleUnknown,
		FirstSeenUnixMilli: now,
		LastSeenUnixMilli:  now,
	}
	if err := peer.Validate(); err != nil {
		return Peer{}, err
	}
	return peer, nil
}

// Validate 校验节点信息 + 防止错误地址写入连接池和 DHT。
func (peer Peer) Validate() error {
	if err := validatePeerID(peer.ID); err != nil {
		return err
	}
	if peer.Role == "" {
		return fmt.Errorf("p2p: peer role cannot be empty")
	}
	for _, address := range peer.Addresses {
		if address.PeerID != peer.ID {
			return fmt.Errorf("p2p: peer address id mismatch %q", address.PeerID)
		}
	}
	return nil
}

// Clone 复制节点信息 + 防止调用方修改 Host 内部状态。
func (peer Peer) Clone() Peer {
	peer.Addresses = cloneAddresses(peer.Addresses)
	peer.Metadata = cloneStringMap(peer.Metadata)
	return peer
}

// Merge 合并节点信息 + 保留已有统计并吸收新的地址和状态字段。
func (peer *Peer) Merge(next Peer) error {
	if peer.ID != next.ID {
		return fmt.Errorf("p2p: cannot merge different peer %q", next.ID)
	}
	if err := next.Validate(); err != nil {
		return err
	}
	for _, address := range next.Addresses {
		peer.AddAddress(address)
	}
	peer.mergeNodeFields(next)
	return nil
}

// AddAddress 添加节点地址 + 去重后保持原有协议优先级。
func (peer *Peer) AddAddress(address utils.MultiAddress) {
	if address.PeerID != peer.ID {
		return
	}
	rawAddress := address.String()
	for _, existing := range peer.Addresses {
		if existing.String() == rawAddress {
			return
		}
	}
	peer.Addresses = append(peer.Addresses, address)
}

// MarkConnected 标记已连接 + 刷新活跃时间并清理连续失败计数。
func (peer *Peer) MarkConnected() {
	now := time.Now().UnixMilli()
	peer.Status = PeerStatusConnected
	peer.LastSeenUnixMilli = now
	peer.LastConnectedUnixMilli = now
	peer.FailureCount = 0
}

// MarkDisconnected 标记已断开 + 保留最后断开时间用于退避策略。
func (peer *Peer) MarkDisconnected() {
	peer.Status = PeerStatusDisconnected
	peer.LastDisconnectedUnixMilli = time.Now().UnixMilli()
}

// RecordError 记录节点错误 + 供黑名单和节点评分策略使用。
func (peer *Peer) RecordError(err error) {
	if err == nil {
		return
	}
	peer.LastError = err.Error()
	peer.LastErrorUnixMilli = time.Now().UnixMilli()
	peer.FailureCount++
}

// Snapshot 返回节点快照 + 避免外部读取时修改内部地址切片。
func (peer Peer) Snapshot() PeerSnapshot {
	return PeerSnapshot{
		ID:                        peer.ID,
		Addresses:                 cloneAddresses(peer.Addresses),
		Status:                    peer.Status,
		Role:                      peer.Role,
		Capabilities:              peer.Capabilities,
		ProtocolVersion:           peer.ProtocolVersion,
		SoftwareVersion:           peer.SoftwareVersion,
		LatestSlot:                peer.LatestSlot,
		BlockHeight:               peer.BlockHeight,
		BestBlockHash:             peer.BestBlockHash,
		Validator:                 peer.Validator,
		StakeLamports:             peer.StakeLamports,
		Score:                     peer.Score,
		FirstSeenUnixMilli:        peer.FirstSeenUnixMilli,
		LastSeenUnixMilli:         peer.LastSeenUnixMilli,
		LastConnectedUnixMilli:    peer.LastConnectedUnixMilli,
		LastDisconnectedUnixMilli: peer.LastDisconnectedUnixMilli,
		LastErrorUnixMilli:        peer.LastErrorUnixMilli,
		LastError:                 peer.LastError,
		FailureCount:              peer.FailureCount,
		SentBytes:                 peer.SentBytes,
		ReceivedBytes:             peer.ReceivedBytes,
		LastRoundTripTimeMilli:    peer.LastRoundTripTimeMilli,
	}
}

// BestAddress 选择最合适地址 + 按协议优先级支持 QUIC 到 TCP 的降级。
func (peer Peer) BestAddress(protocolOrder []utils.MultiAddressProtocol) (utils.MultiAddress, bool) {
	if len(peer.Addresses) == 0 {
		return utils.MultiAddress{}, false
	}
	for _, protocol := range normalizedProtocolOrder(protocolOrder) {
		if address, ok := peer.firstAddressByProtocol(protocol); ok {
			return address, true
		}
	}
	return peer.Addresses[0], true
}
func (peer Peer) firstAddressByProtocol(protocol utils.MultiAddressProtocol) (utils.MultiAddress, bool) {
	for _, address := range peer.Addresses {
		if address.Protocol == protocol {
			return address, true
		}
	}
	return utils.MultiAddress{}, false
}
func (peer *Peer) mergeNodeFields(next Peer) {
	peer.Status = choosePeerStatus(peer.Status, next.Status)
	peer.Role = choosePeerRole(peer.Role, next.Role)
	peer.Capabilities |= next.Capabilities
	if next.ProtocolVersion != "" {
		peer.ProtocolVersion = next.ProtocolVersion
	}
	if next.SoftwareVersion != "" {
		peer.SoftwareVersion = next.SoftwareVersion
	}
	if next.LatestSlot > peer.LatestSlot {
		peer.LatestSlot = next.LatestSlot
	}
	if next.BlockHeight > peer.BlockHeight {
		peer.BlockHeight = next.BlockHeight
	}
	if next.BestBlockHash != "" {
		peer.BestBlockHash = next.BestBlockHash
	}
	if next.Validator {
		peer.Validator = true
	}
	if next.StakeLamports > 0 {
		peer.StakeLamports = next.StakeLamports
	}
	if next.Score != 0 {
		peer.Score = next.Score
	}
	if next.LastSeenUnixMilli > peer.LastSeenUnixMilli {
		peer.LastSeenUnixMilli = next.LastSeenUnixMilli
	}
	peer.Metadata = mergeStringMap(peer.Metadata, next.Metadata)
}
func choosePeerStatus(current PeerStatus, next PeerStatus) PeerStatus {
	if next == "" || next == PeerStatusUnknown {
		return current
	}
	return next
}
func choosePeerRole(current PeerRole, next PeerRole) PeerRole {
	if next == "" || next == PeerRoleUnknown {
		return current
	}
	return next
}
func validatePeerID(peerID string) error {
	decoded, err := utils.Base58Decode(peerID)
	if err != nil {
		return fmt.Errorf("p2p: invalid peer id: %w", err)
	}
	if len(decoded) != peerIDByteSize {
		return fmt.Errorf("p2p: peer id requires %d decoded bytes, got %d", peerIDByteSize, len(decoded))
	}
	return nil
}
func cloneAddresses(addresses []utils.MultiAddress) []utils.MultiAddress {
	if addresses == nil {
		return nil
	}
	cloned := make([]utils.MultiAddress, len(addresses))
	copy(cloned, addresses)
	return cloned
}
func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
func mergeStringMap(current map[string]string, next map[string]string) map[string]string {
	if len(next) == 0 {
		return cloneStringMap(current)
	}
	merged := cloneStringMap(current)
	if merged == nil {
		merged = make(map[string]string, len(next))
	}
	for key, value := range next {
		merged[key] = value
	}
	return merged
}
func normalizedProtocolOrder(protocolOrder []utils.MultiAddressProtocol) []utils.MultiAddressProtocol {
	if len(protocolOrder) == 0 {
		return []utils.MultiAddressProtocol{utils.ProtocolQUIC, utils.ProtocolTCP}
	}
	return append([]utils.MultiAddressProtocol(nil), protocolOrder...)
}
