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
	// PeerRolePublicRPC 表示公网入口节点 + 用于钱包查询和交易转发。
	PeerRolePublicRPC PeerRole = "public_rpc"
	// PeerRoleValidator 表示验证节点 + 用于共识消息优先连接。
	PeerRoleValidator PeerRole = "validator"
	// PeerRoleBootnode 表示引导节点 + 用于冷启动节点发现。
	PeerRoleBootnode PeerRole = "bootnode"
	// PeerRoleArchive 表示归档节点 + 用于历史数据查询和低优先级中继。
	PeerRoleArchive PeerRole = "archive"
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
	AdvertisedAddresses       []utils.MultiAddress
	VerifiedAddresses         []utils.MultiAddress
	Addresses                 []utils.MultiAddress
	Status                    PeerStatus
	Role                      PeerRole
	Capabilities              PeerCapability
	ProtocolVersion           string
	SoftwareVersion           string
	PreferredProtocols        []utils.MultiAddressProtocol
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
	SignedRecord              []byte
	Metadata                  map[string]string
}

// PeerSnapshot 保存节点快照 + 供监控、调试和 DHT 状态导出。
type PeerSnapshot struct {
	ID                        string
	AdvertisedAddresses       []utils.MultiAddress
	VerifiedAddresses         []utils.MultiAddress
	Addresses                 []utils.MultiAddress
	Status                    PeerStatus
	Role                      PeerRole
	Capabilities              PeerCapability
	ProtocolVersion           string
	SoftwareVersion           string
	PreferredProtocols        []utils.MultiAddressProtocol
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
	HasSignedRecord           bool
}

// NewPeer 创建节点信息 + 统一校验节点 ID 和地址归属。
func NewPeer(peerID string, addresses []utils.MultiAddress) (Peer, error) {
	now := time.Now().UnixMilli()
	peer := Peer{
		ID:                  peerID,
		AdvertisedAddresses: cloneAddresses(addresses),
		Addresses:           cloneAddresses(addresses),
		Status:              PeerStatusUnknown,
		Role:                PeerRoleUnknown,
		FirstSeenUnixMilli:  now,
		LastSeenUnixMilli:   now,
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
	if err := validatePeerAddressOwnership(peer.ID, peer.advertisedAddressList()); err != nil {
		return err
	}
	if err := validatePeerAddressOwnership(peer.ID, peer.VerifiedAddresses); err != nil {
		return err
	}
	if err := validatePeerPreferredProtocols(peer.PreferredProtocols); err != nil {
		return err
	}
	return nil
}

// Clone 复制节点信息 + 防止调用方修改 Host 内部状态。
func (peer Peer) Clone() Peer {
	peer.AdvertisedAddresses = cloneAddresses(peer.advertisedAddressList())
	peer.VerifiedAddresses = cloneAddresses(peer.VerifiedAddresses)
	peer.Addresses = cloneAddresses(peer.AdvertisedAddresses)
	peer.PreferredProtocols = cloneProtocols(peer.PreferredProtocols)
	peer.SignedRecord = utils.CloneBytes(peer.SignedRecord)
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
	for _, address := range next.advertisedAddressList() {
		peer.AddAdvertisedAddress(address)
	}
	for _, address := range next.VerifiedAddresses {
		peer.AddVerifiedAddress(address)
	}
	peer.mergeNodeFields(next)
	return nil
}

// AddAddress 添加兼容节点地址 + 保持旧调用方写入声明地址语义。
func (peer *Peer) AddAddress(address utils.MultiAddress) {
	peer.AddAdvertisedAddress(address)
}

// AddAdvertisedAddress 添加声明地址 + 对方签名或发现层声明后进入可拨候选。
func (peer *Peer) AddAdvertisedAddress(address utils.MultiAddress) {
	if address.PeerID != peer.ID {
		return
	}
	peer.AdvertisedAddresses = appendUniqueAddress(peer.advertisedAddressList(), address)
	peer.Addresses = cloneAddresses(peer.AdvertisedAddresses)
}

// AddVerifiedAddress 添加验证地址 + 仅在本机拨通且 PeerID 匹配后提升优先级。
func (peer *Peer) AddVerifiedAddress(address utils.MultiAddress) {
	if address.PeerID != peer.ID {
		return
	}
	peer.VerifiedAddresses = appendUniqueAddress(peer.VerifiedAddresses, address)
}

// MarkConnected 标记已连接 + 刷新活跃时间并清理历史拨号失败。
func (peer *Peer) MarkConnected() {
	now := time.Now().UnixMilli()
	peer.Status = PeerStatusConnected
	peer.LastSeenUnixMilli = now
	peer.LastConnectedUnixMilli = now
	peer.FailureCount = 0
	peer.LastError = ""
	peer.LastErrorUnixMilli = 0
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
		AdvertisedAddresses:       cloneAddresses(peer.advertisedAddressList()),
		VerifiedAddresses:         cloneAddresses(peer.VerifiedAddresses),
		Addresses:                 cloneAddresses(peer.advertisedAddressList()),
		Status:                    peer.Status,
		Role:                      peer.Role,
		Capabilities:              peer.Capabilities,
		ProtocolVersion:           peer.ProtocolVersion,
		SoftwareVersion:           peer.SoftwareVersion,
		PreferredProtocols:        cloneProtocols(peer.PreferredProtocols),
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
		HasSignedRecord:           len(peer.SignedRecord) > 0,
	}
}

// BestAddress 选择最合适地址 + 按协议优先级支持 QUIC 到 TCP 的降级。
func (peer Peer) BestAddress(protocolOrder []utils.MultiAddressProtocol) (utils.MultiAddress, bool) {
	if !peer.hasDialableAddress() {
		return utils.MultiAddress{}, false
	}
	for _, protocol := range peer.dialProtocolOrder(protocolOrder) {
		if address, ok := peer.firstDialAddressByProtocol(protocol); ok {
			return address, true
		}
	}
	return utils.MultiAddress{}, false
}
func (peer Peer) firstDialAddressByProtocol(protocol utils.MultiAddressProtocol) (utils.MultiAddress, bool) {
	if address, ok := firstAddressByProtocol(peer.VerifiedAddresses, protocol); ok {
		return address, true
	}
	return firstAddressByProtocol(peer.advertisedAddressList(), protocol)
}
func firstAddressByProtocol(addresses []utils.MultiAddress, protocol utils.MultiAddressProtocol) (utils.MultiAddress, bool) {
	for _, address := range addresses {
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
	if len(next.PreferredProtocols) > 0 {
		peer.PreferredProtocols = cloneProtocols(next.PreferredProtocols)
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
	if next.FirstSeenUnixMilli > 0 && (peer.FirstSeenUnixMilli == 0 || next.FirstSeenUnixMilli < peer.FirstSeenUnixMilli) {
		peer.FirstSeenUnixMilli = next.FirstSeenUnixMilli
	}
	if next.LastConnectedUnixMilli > peer.LastConnectedUnixMilli {
		peer.LastConnectedUnixMilli = next.LastConnectedUnixMilli
	}
	if next.LastDisconnectedUnixMilli > peer.LastDisconnectedUnixMilli {
		peer.LastDisconnectedUnixMilli = next.LastDisconnectedUnixMilli
	}
	if next.LastErrorUnixMilli > peer.LastErrorUnixMilli {
		peer.LastErrorUnixMilli = next.LastErrorUnixMilli
		peer.LastError = next.LastError
	}
	if next.FailureCount > peer.FailureCount {
		peer.FailureCount = next.FailureCount
	}
	if next.SentBytes > peer.SentBytes {
		peer.SentBytes = next.SentBytes
	}
	if next.ReceivedBytes > peer.ReceivedBytes {
		peer.ReceivedBytes = next.ReceivedBytes
	}
	if next.LastRoundTripTimeMilli > 0 {
		peer.LastRoundTripTimeMilli = next.LastRoundTripTimeMilli
	}
	if len(next.SignedRecord) > 0 {
		peer.SignedRecord = utils.CloneBytes(next.SignedRecord)
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
func (peer Peer) advertisedAddressList() []utils.MultiAddress {
	if len(peer.AdvertisedAddresses) > 0 {
		return peer.AdvertisedAddresses
	}
	return peer.Addresses
}
func (peer Peer) hasDialableAddress() bool {
	return len(peer.VerifiedAddresses) > 0 || len(peer.advertisedAddressList()) > 0
}
func validatePeerAddressOwnership(peerID string, addresses []utils.MultiAddress) error {
	for _, address := range addresses {
		if address.PeerID != peerID {
			return fmt.Errorf("p2p: peer address id mismatch %q", address.PeerID)
		}
	}
	return nil
}
func appendUniqueAddress(addresses []utils.MultiAddress, address utils.MultiAddress) []utils.MultiAddress {
	rawAddress := address.String()
	for _, existing := range addresses {
		if existing.String() == rawAddress {
			return cloneAddresses(addresses)
		}
	}
	next := cloneAddresses(addresses)
	return append(next, address)
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
func (peer Peer) dialProtocolOrder(localProtocolOrder []utils.MultiAddressProtocol) []utils.MultiAddressProtocol {
	localProtocols := normalizedProtocolOrder(localProtocolOrder)
	if len(peer.PreferredProtocols) == 0 {
		return localProtocols
	}
	ordered := make([]utils.MultiAddressProtocol, 0, len(localProtocols))
	for _, remoteProtocol := range peer.PreferredProtocols {
		if !containsProtocol(localProtocols, remoteProtocol) || containsProtocol(ordered, remoteProtocol) {
			continue
		}
		ordered = append(ordered, remoteProtocol)
	}
	for _, localProtocol := range localProtocols {
		if containsProtocol(ordered, localProtocol) {
			continue
		}
		ordered = append(ordered, localProtocol)
	}
	return ordered
}
func validatePeerPreferredProtocols(protocols []utils.MultiAddressProtocol) error {
	for _, protocol := range protocols {
		if _, err := utils.ParseMultiAddressProtocol(string(protocol)); err != nil {
			return fmt.Errorf("%w: invalid peer preferred protocol: %w", ErrInvalidMessage, err)
		}
	}
	return nil
}
func cloneProtocols(protocols []utils.MultiAddressProtocol) []utils.MultiAddressProtocol {
	if protocols == nil {
		return nil
	}
	cloned := make([]utils.MultiAddressProtocol, len(protocols))
	copy(cloned, protocols)
	return cloned
}
func containsProtocol(protocols []utils.MultiAddressProtocol, target utils.MultiAddressProtocol) bool {
	for _, protocol := range protocols {
		if protocol == target {
			return true
		}
	}
	return false
}

// PeerCapabilityNames 返回能力名称列表 + APP 需要稳定字段区分 RPC 入口和验证者。
func PeerCapabilityNames(capabilities PeerCapability) []string {
	names := make([]string, 0, 5)
	if capabilities&PeerCapabilityRelay != 0 {
		names = append(names, "relay")
	}
	if capabilities&PeerCapabilityArchive != 0 {
		names = append(names, "archive")
	}
	if capabilities&PeerCapabilityValidator != 0 {
		names = append(names, "validator")
	}
	if capabilities&PeerCapabilityStateSync != 0 {
		names = append(names, "state_sync")
	}
	if capabilities&PeerCapabilityDHT != 0 {
		names = append(names, "dht")
	}
	return names
}

// PeerRoleNames 返回节点角色列表 + 用角色叠加能力表达多角色节点而不破坏旧协议字段。
func PeerRoleNames(role PeerRole, capabilities PeerCapability) []string {
	return PeerRolesNames([]PeerRole{role}, capabilities)
}

// PeerRolesNames 返回节点角色列表 + 保留配置中的多角色并兼容旧 capability 字段。
func PeerRolesNames(peerRoles []PeerRole, capabilities PeerCapability) []string {
	seen := make(map[string]struct{}, 4)
	roles := make([]string, 0, 4)
	appendRole := func(value string) {
		if value == "" {
			return
		}
		if _, exists := seen[value]; exists {
			return
		}
		seen[value] = struct{}{}
		roles = append(roles, value)
	}

	for _, role := range peerRoles {
		switch role {
		case PeerRolePublicRPC:
			appendRole("public_rpc")
			appendRole("rpc")
		case PeerRoleValidator:
			appendRole("validator")
		case PeerRoleBootnode:
			appendRole("bootnode")
			appendRole("bootstrap")
		case PeerRoleArchive:
			appendRole("archive")
		case PeerRoleFull:
			appendRole("full")
		}
	}
	if capabilities&PeerCapabilityValidator != 0 {
		appendRole("validator")
	}
	if capabilities&PeerCapabilityArchive != 0 {
		appendRole("archive")
	}
	for _, role := range peerRoles {
		if capabilities&PeerCapabilityRelay == 0 || role != PeerRolePublicRPC {
			continue
		}
		appendRole("relay")
		break
	}
	return roles
}
