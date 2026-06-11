package p2p

import (
	"fmt"
	"time"

	"solana_golang/codec/borsh"
	"solana_golang/utils"
)

const (
	// KADProtocolVersion 定义 KAD payload 版本 + 便于节点发现协议独立演进。
	KADProtocolVersion uint16 = 1

	maxKADPeerHints        = 64
	maxKADAddressesPerPeer = 8
)

// KADFindNodeRequest 保存 find-node 请求 + 使用目标 peer id 查询邻近节点。
type KADFindNodeRequest struct {
	Version            uint16
	TargetPeerID       string
	Limit              uint16
	CreatedAtUnixMilli int64
}

// KADFindNodeResponse 保存 find-node 响应 + 返回已校验地址归属的节点提示。
type KADFindNodeResponse struct {
	Version            uint16
	TargetPeerID       string
	Peers              []KADPeerHint
	CreatedAtUnixMilli int64
}

// KADPeerHint 保存可连接节点提示 + 作为 DHT 查询响应的最小节点视图。
type KADPeerHint struct {
	PeerID               string
	Addresses            []string
	SignedRecord         []byte
	Role                 PeerRole
	Capabilities         PeerCapability
	ProtocolVersion      string
	SoftwareVersion      string
	LatestSlot           uint64
	BlockHeight          uint64
	BestBlockHash        string
	Validator            bool
	StakeLamports        uint64
	LastSeenUnixMilli    int64
	LastConnectUnixMilli int64
}

// NewKADFindNodeRequest 创建 find-node 请求 + 统一填充版本和创建时间。
func NewKADFindNodeRequest(targetPeerID string, limit int) (KADFindNodeRequest, error) {
	request := KADFindNodeRequest{
		Version:            KADProtocolVersion,
		TargetPeerID:       targetPeerID,
		Limit:              uint16(normalizeKADRequestLimit(limit)),
		CreatedAtUnixMilli: time.Now().UnixMilli(),
	}
	return request, request.Validate()
}

// Validate 校验 find-node 请求 + 防止无效 target 和无界 limit 进入 DHT 查询。
func (request KADFindNodeRequest) Validate() error {
	if request.Version != KADProtocolVersion {
		return fmt.Errorf("%w: unsupported kad request version", ErrInvalidMessage)
	}
	if err := validatePeerID(request.TargetPeerID); err != nil {
		return fmt.Errorf("%w: invalid kad target: %w", ErrInvalidMessage, err)
	}
	if request.Limit == 0 || request.Limit > maxKADPeerHints {
		return fmt.Errorf("%w: invalid kad request limit", ErrInvalidMessage)
	}
	if request.CreatedAtUnixMilli <= 0 {
		return fmt.Errorf("%w: invalid kad request time", ErrInvalidMessage)
	}
	return nil
}

// MarshalBinary 序列化 find-node 请求 + P2P 节点发现统一使用 Borsh。
func (request KADFindNodeRequest) MarshalBinary() ([]byte, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	writer := borsh.NewWriter(DefaultMaxMessageSize)
	writer.WriteUint16(request.Version)
	if err := writer.WriteString(request.TargetPeerID); err != nil {
		return nil, fmt.Errorf("p2p: marshal kad target: %w", err)
	}
	writer.WriteUint16(request.Limit)
	writer.WriteInt64(request.CreatedAtUnixMilli)
	return writer.BytesView(), nil
}

// UnmarshalKADFindNodeRequestBinary 反序列化 find-node 请求 + 解码后立即执行边界校验。
func UnmarshalKADFindNodeRequestBinary(data []byte) (KADFindNodeRequest, error) {
	reader := borsh.NewBorrowedReader(data, DefaultMaxMessageSize)
	version, err := reader.ReadUint16()
	if err != nil {
		return KADFindNodeRequest{}, fmt.Errorf("p2p: read kad request version: %w", err)
	}
	targetPeerID, err := reader.ReadString()
	if err != nil {
		return KADFindNodeRequest{}, fmt.Errorf("p2p: read kad request target: %w", err)
	}
	limit, err := reader.ReadUint16()
	if err != nil {
		return KADFindNodeRequest{}, fmt.Errorf("p2p: read kad request limit: %w", err)
	}
	createdAtUnixMilli, err := reader.ReadInt64()
	if err != nil {
		return KADFindNodeRequest{}, fmt.Errorf("p2p: read kad request time: %w", err)
	}
	if err := reader.EnsureEOF(); err != nil {
		return KADFindNodeRequest{}, fmt.Errorf("p2p: read kad request eof: %w", err)
	}
	request := KADFindNodeRequest{
		Version:            version,
		TargetPeerID:       targetPeerID,
		Limit:              limit,
		CreatedAtUnixMilli: createdAtUnixMilli,
	}
	return request, request.Validate()
}

// NewKADFindNodeResponse 创建 find-node 响应 + 将 Peer 快照转换为可传输提示。
func NewKADFindNodeResponse(targetPeerID string, peers []Peer) (KADFindNodeResponse, error) {
	response := KADFindNodeResponse{
		Version:            KADProtocolVersion,
		TargetPeerID:       targetPeerID,
		Peers:              PeersToKADPeerHints(peers),
		CreatedAtUnixMilli: time.Now().UnixMilli(),
	}
	return response, response.Validate()
}

// Validate 校验 find-node 响应 + 防止畸形节点提示污染路由表。
func (response KADFindNodeResponse) Validate() error {
	if response.Version != KADProtocolVersion {
		return fmt.Errorf("%w: unsupported kad response version", ErrInvalidMessage)
	}
	if err := validatePeerID(response.TargetPeerID); err != nil {
		return fmt.Errorf("%w: invalid kad response target: %w", ErrInvalidMessage, err)
	}
	if len(response.Peers) > maxKADPeerHints {
		return fmt.Errorf("%w: too many kad peer hints", ErrInvalidMessage)
	}
	if response.CreatedAtUnixMilli <= 0 {
		return fmt.Errorf("%w: invalid kad response time", ErrInvalidMessage)
	}
	for _, peer := range response.Peers {
		if err := peer.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// MarshalBinary 序列化 find-node 响应 + 使用显式数组长度限制内存占用。
func (response KADFindNodeResponse) MarshalBinary() ([]byte, error) {
	if err := response.Validate(); err != nil {
		return nil, err
	}
	writer := borsh.NewWriter(DefaultMaxMessageSize)
	writer.WriteUint16(response.Version)
	if err := writer.WriteString(response.TargetPeerID); err != nil {
		return nil, fmt.Errorf("p2p: marshal kad response target: %w", err)
	}
	writer.WriteUint32(uint32(len(response.Peers)))
	for _, peer := range response.Peers {
		if err := peer.marshalTo(writer); err != nil {
			return nil, err
		}
	}
	writer.WriteInt64(response.CreatedAtUnixMilli)
	return writer.BytesView(), nil
}

// UnmarshalKADFindNodeResponseBinary 反序列化 find-node 响应 + 解码后校验每个节点提示。
func UnmarshalKADFindNodeResponseBinary(data []byte) (KADFindNodeResponse, error) {
	reader := borsh.NewBorrowedReader(data, DefaultMaxMessageSize)
	version, err := reader.ReadUint16()
	if err != nil {
		return KADFindNodeResponse{}, fmt.Errorf("p2p: read kad response version: %w", err)
	}
	targetPeerID, err := reader.ReadString()
	if err != nil {
		return KADFindNodeResponse{}, fmt.Errorf("p2p: read kad response target: %w", err)
	}
	peerCount, err := reader.ReadUint32()
	if err != nil {
		return KADFindNodeResponse{}, fmt.Errorf("p2p: read kad peer count: %w", err)
	}
	if peerCount > maxKADPeerHints {
		return KADFindNodeResponse{}, fmt.Errorf("%w: too many kad peer hints", ErrInvalidMessage)
	}
	peers := make([]KADPeerHint, 0, int(peerCount))
	for index := 0; index < int(peerCount); index++ {
		peer, err := unmarshalKADPeerHint(reader)
		if err != nil {
			return KADFindNodeResponse{}, err
		}
		peers = append(peers, peer)
	}
	createdAtUnixMilli, err := reader.ReadInt64()
	if err != nil {
		return KADFindNodeResponse{}, fmt.Errorf("p2p: read kad response time: %w", err)
	}
	if err := reader.EnsureEOF(); err != nil {
		return KADFindNodeResponse{}, fmt.Errorf("p2p: read kad response eof: %w", err)
	}
	response := KADFindNodeResponse{
		Version:            version,
		TargetPeerID:       targetPeerID,
		Peers:              peers,
		CreatedAtUnixMilli: createdAtUnixMilli,
	}
	return response, response.Validate()
}

// PeersToKADPeerHints 转换节点快照 + 避免 DHT 响应暴露 Host 内部可变结构。
func PeersToKADPeerHints(peers []Peer) []KADPeerHint {
	hints := make([]KADPeerHint, 0, minInt(len(peers), maxKADPeerHints))
	for _, peer := range peers {
		if len(hints) >= maxKADPeerHints {
			break
		}
		if !peerShareableInDHT(peer) {
			continue
		}
		hint, err := NewKADPeerHint(peer)
		if err != nil {
			continue
		}
		hints = append(hints, hint)
	}
	return hints
}

// NewKADPeerHint 创建节点提示 + 将地址转换为字符串便于 Borsh 编码。
func NewKADPeerHint(peer Peer) (KADPeerHint, error) {
	if !peerShareableInDHT(peer) {
		return KADPeerHint{}, fmt.Errorf("%w: peer is not shareable in dht", ErrInvalidMessage)
	}
	record, ok, err := signedPeerRecordFromPeer(peer, "")
	if err != nil {
		return KADPeerHint{}, err
	}
	if !ok {
		return KADPeerHint{}, fmt.Errorf("%w: missing signed peer record", ErrInvalidMessage)
	}
	encoded, err := record.MarshalBinary()
	if err != nil {
		return KADPeerHint{}, err
	}
	hint := KADPeerHint{
		PeerID:               record.PeerID,
		Addresses:            append([]string(nil), record.Addresses...),
		SignedRecord:         encoded,
		Role:                 record.Role,
		Capabilities:         record.Capabilities,
		ProtocolVersion:      record.ProtocolVersion,
		SoftwareVersion:      record.SoftwareVersion,
		LatestSlot:           record.LatestSlot,
		BlockHeight:          record.BlockHeight,
		BestBlockHash:        record.BestBlockHash,
		Validator:            record.Validator,
		StakeLamports:        record.StakeLamports,
		LastSeenUnixMilli:    peer.LastSeenUnixMilli,
		LastConnectUnixMilli: peer.LastConnectedUnixMilli,
	}
	if err := hint.Validate(); err != nil {
		return KADPeerHint{}, err
	}
	return hint, nil
}

// Validate 校验节点提示 + 确保地址中的 peer id 与提示身份一致。
func (hint KADPeerHint) Validate() error {
	if err := validatePeerID(hint.PeerID); err != nil {
		return fmt.Errorf("%w: invalid kad peer hint id: %w", ErrInvalidMessage, err)
	}
	if len(hint.Addresses) == 0 || len(hint.Addresses) > maxKADAddressesPerPeer {
		return fmt.Errorf("%w: invalid kad peer hint addresses", ErrInvalidMessage)
	}
	record, err := UnmarshalSignedPeerRecordBinary(hint.SignedRecord)
	if err != nil {
		return err
	}
	if record.PeerID != hint.PeerID {
		return fmt.Errorf("%w: kad signed record id mismatch", ErrInvalidMessage)
	}
	for _, rawAddress := range hint.Addresses {
		address, err := utils.ParseMultiAddress(rawAddress)
		if err != nil {
			return fmt.Errorf("%w: invalid kad peer address: %w", ErrInvalidMessage, err)
		}
		if address.PeerID != hint.PeerID {
			return fmt.Errorf("%w: kad peer address id mismatch", ErrInvalidMessage)
		}
	}
	if len(record.Addresses) != len(hint.Addresses) {
		return fmt.Errorf("%w: kad signed record address mismatch", ErrInvalidMessage)
	}
	for index, rawAddress := range hint.Addresses {
		if record.Addresses[index] != rawAddress {
			return fmt.Errorf("%w: kad signed record address mismatch", ErrInvalidMessage)
		}
	}
	return nil
}

// ToPeer 转换为 Peer + 进入 Host 前再次复用 Peer 校验逻辑。
func (hint KADPeerHint) ToPeer() (Peer, error) {
	if err := hint.Validate(); err != nil {
		return Peer{}, err
	}
	record, err := UnmarshalSignedPeerRecordBinary(hint.SignedRecord)
	if err != nil {
		return Peer{}, err
	}
	peer, err := record.ToPeer()
	if err != nil {
		return Peer{}, err
	}
	peer.LastSeenUnixMilli = hint.LastSeenUnixMilli
	peer.LastConnectedUnixMilli = hint.LastConnectUnixMilli
	return peer, nil
}

func (hint KADPeerHint) marshalTo(writer *borsh.Writer) error {
	if err := hint.Validate(); err != nil {
		return err
	}
	if err := writer.WriteString(hint.PeerID); err != nil {
		return fmt.Errorf("p2p: marshal kad peer id: %w", err)
	}
	if err := writeKADStringSlice(writer, hint.Addresses); err != nil {
		return err
	}
	if err := writer.WriteBytes(hint.SignedRecord); err != nil {
		return fmt.Errorf("p2p: marshal kad signed record: %w", err)
	}
	if err := writer.WriteString(string(normalizePeerRole(hint.Role))); err != nil {
		return fmt.Errorf("p2p: marshal kad peer role: %w", err)
	}
	writer.WriteUint64(uint64(hint.Capabilities))
	if err := writer.WriteString(hint.ProtocolVersion); err != nil {
		return fmt.Errorf("p2p: marshal kad protocol version: %w", err)
	}
	if err := writer.WriteString(hint.SoftwareVersion); err != nil {
		return fmt.Errorf("p2p: marshal kad software version: %w", err)
	}
	writer.WriteUint64(hint.LatestSlot)
	writer.WriteUint64(hint.BlockHeight)
	if err := writer.WriteString(hint.BestBlockHash); err != nil {
		return fmt.Errorf("p2p: marshal kad best block hash: %w", err)
	}
	writer.WriteBool(hint.Validator)
	writer.WriteUint64(hint.StakeLamports)
	writer.WriteInt64(hint.LastSeenUnixMilli)
	writer.WriteInt64(hint.LastConnectUnixMilli)
	return nil
}

func unmarshalKADPeerHint(reader *borsh.Reader) (KADPeerHint, error) {
	peerID, err := reader.ReadString()
	if err != nil {
		return KADPeerHint{}, fmt.Errorf("p2p: read kad peer id: %w", err)
	}
	addresses, err := readKADStringSlice(reader, maxKADAddressesPerPeer)
	if err != nil {
		return KADPeerHint{}, err
	}
	signedRecord, err := reader.ReadBytes()
	if err != nil {
		return KADPeerHint{}, fmt.Errorf("p2p: read kad signed record: %w", err)
	}
	if len(signedRecord) > maxPeerRecordSize {
		return KADPeerHint{}, fmt.Errorf("%w: kad signed record too large", ErrInvalidMessage)
	}
	role, err := reader.ReadString()
	if err != nil {
		return KADPeerHint{}, fmt.Errorf("p2p: read kad role: %w", err)
	}
	capabilities, err := reader.ReadUint64()
	if err != nil {
		return KADPeerHint{}, fmt.Errorf("p2p: read kad capabilities: %w", err)
	}
	protocolVersion, err := reader.ReadString()
	if err != nil {
		return KADPeerHint{}, fmt.Errorf("p2p: read kad protocol version: %w", err)
	}
	softwareVersion, err := reader.ReadString()
	if err != nil {
		return KADPeerHint{}, fmt.Errorf("p2p: read kad software version: %w", err)
	}
	latestSlot, err := reader.ReadUint64()
	if err != nil {
		return KADPeerHint{}, fmt.Errorf("p2p: read kad latest slot: %w", err)
	}
	blockHeight, err := reader.ReadUint64()
	if err != nil {
		return KADPeerHint{}, fmt.Errorf("p2p: read kad block height: %w", err)
	}
	bestBlockHash, err := reader.ReadString()
	if err != nil {
		return KADPeerHint{}, fmt.Errorf("p2p: read kad best block hash: %w", err)
	}
	validator, err := reader.ReadBool()
	if err != nil {
		return KADPeerHint{}, fmt.Errorf("p2p: read kad validator: %w", err)
	}
	stakeLamports, err := reader.ReadUint64()
	if err != nil {
		return KADPeerHint{}, fmt.Errorf("p2p: read kad stake: %w", err)
	}
	lastSeenUnixMilli, err := reader.ReadInt64()
	if err != nil {
		return KADPeerHint{}, fmt.Errorf("p2p: read kad last seen: %w", err)
	}
	lastConnectUnixMilli, err := reader.ReadInt64()
	if err != nil {
		return KADPeerHint{}, fmt.Errorf("p2p: read kad last connect: %w", err)
	}
	hint := KADPeerHint{
		PeerID:               peerID,
		Addresses:            addresses,
		SignedRecord:         signedRecord,
		Role:                 PeerRole(role),
		Capabilities:         PeerCapability(capabilities),
		ProtocolVersion:      protocolVersion,
		SoftwareVersion:      softwareVersion,
		LatestSlot:           latestSlot,
		BlockHeight:          blockHeight,
		BestBlockHash:        bestBlockHash,
		Validator:            validator,
		StakeLamports:        stakeLamports,
		LastSeenUnixMilli:    lastSeenUnixMilli,
		LastConnectUnixMilli: lastConnectUnixMilli,
	}
	return hint, hint.Validate()
}

func writeKADStringSlice(writer *borsh.Writer, values []string) error {
	if len(values) > maxKADAddressesPerPeer {
		return fmt.Errorf("%w: too many kad strings", ErrInvalidMessage)
	}
	writer.WriteUint32(uint32(len(values)))
	for _, value := range values {
		if err := writer.WriteString(value); err != nil {
			return fmt.Errorf("p2p: marshal kad string: %w", err)
		}
	}
	return nil
}

func readKADStringSlice(reader *borsh.Reader, limit int) ([]string, error) {
	count, err := reader.ReadUint32()
	if err != nil {
		return nil, fmt.Errorf("p2p: read kad string count: %w", err)
	}
	if int(count) > limit {
		return nil, fmt.Errorf("%w: too many kad strings", ErrInvalidMessage)
	}
	values := make([]string, 0, int(count))
	for index := 0; index < int(count); index++ {
		value, err := reader.ReadString()
		if err != nil {
			return nil, fmt.Errorf("p2p: read kad string: %w", err)
		}
		values = append(values, value)
	}
	return values, nil
}

func normalizeKADRequestLimit(limit int) int {
	if limit <= 0 || limit > defaultKADFindNodeSize {
		return defaultKADFindNodeSize
	}
	return limit
}

func normalizePeerRole(role PeerRole) PeerRole {
	if role == "" {
		return PeerRoleUnknown
	}
	return role
}
