package p2p

import (
	"fmt"
	"time"

	"solana_golang/codec/borsh"
	"solana_golang/utils"
)

const (
	// PeerRecordVersion 定义签名节点记录版本 + 支持后续地址记录格式升级。
	PeerRecordVersion uint16 = 1

	defaultPeerRecordTTL   = 30 * time.Minute
	maxPeerRecordTTL       = 24 * time.Hour
	peerRecordMaxClockSkew = 5 * time.Minute
	maxPeerRecordAddresses = 8
	maxPeerRecordSize      = 16 * 1024
	peerRecordDomain       = "p2p-signed-peer-record-v1"
)

// SignedPeerRecord 保存节点自签名地址记录 + 让 DHT 只能转发节点本人授权的可拨地址。
type SignedPeerRecord struct {
	Version            uint16
	PeerID             string
	PublicKey          []byte
	Addresses          []string
	Role               PeerRole
	Capabilities       PeerCapability
	ProtocolVersion    string
	SoftwareVersion    string
	LatestSlot         uint64
	BlockHeight        uint64
	BestBlockHash      string
	Validator          bool
	StakeLamports      uint64
	IssuedAtUnixMilli  int64
	ExpiresAtUnixMilli int64
	Signature          []byte
}

// NewSignedPeerRecord 创建并签名节点记录 + 将可拨地址绑定到节点长期身份。
func NewSignedPeerRecord(peer Peer, identity SecureSessionIdentity, ttl time.Duration) (SignedPeerRecord, error) {
	if err := identity.Validate(); err != nil {
		return SignedPeerRecord{}, err
	}
	if peer.ID != identity.PeerID {
		return SignedPeerRecord{}, fmt.Errorf("%w: peer record identity mismatch", ErrSecureSession)
	}
	peer = normalizePeerForStorage(peer)
	if err := peer.Validate(); err != nil {
		return SignedPeerRecord{}, err
	}

	now := time.Now()
	record := SignedPeerRecord{
		Version:            PeerRecordVersion,
		PeerID:             peer.ID,
		PublicKey:          utils.CloneBytes(identity.PublicKey),
		Addresses:          peerRecordAddressStrings(peer.Addresses),
		Role:               normalizePeerRole(peer.Role),
		Capabilities:       peer.Capabilities,
		ProtocolVersion:    peer.ProtocolVersion,
		SoftwareVersion:    peer.SoftwareVersion,
		LatestSlot:         peer.LatestSlot,
		BlockHeight:        peer.BlockHeight,
		BestBlockHash:      peer.BestBlockHash,
		Validator:          peer.Validator,
		StakeLamports:      peer.StakeLamports,
		IssuedAtUnixMilli:  now.UnixMilli(),
		ExpiresAtUnixMilli: now.Add(normalizePeerRecordTTL(ttl)).UnixMilli(),
	}
	if err := record.Sign(identity.PrivateKey); err != nil {
		return SignedPeerRecord{}, err
	}
	return record, nil
}

// Sign 签名节点记录 + 只对不含签名的确定性字段布局签名。
func (record *SignedPeerRecord) Sign(privateKey []byte) error {
	if record == nil {
		return fmt.Errorf("%w: nil peer record", ErrInvalidMessage)
	}
	if err := record.validate(false); err != nil {
		return err
	}
	signingBytes, err := record.signingBytes()
	if err != nil {
		return err
	}
	signature, err := utils.Ed25519Sign(privateKey, signingBytes)
	if err != nil {
		return fmt.Errorf("%w: sign peer record: %w", ErrSecureSession, err)
	}
	record.Signature = signature
	return nil
}

// Verify 验证节点记录 + 同时检查签名、地址归属和过期时间。
func (record SignedPeerRecord) Verify() error {
	if err := record.validate(true); err != nil {
		return err
	}
	now := time.Now()
	issuedAt := time.UnixMilli(record.IssuedAtUnixMilli)
	expiresAt := time.UnixMilli(record.ExpiresAtUnixMilli)
	if now.Add(peerRecordMaxClockSkew).Before(issuedAt) {
		return fmt.Errorf("%w: peer record issued in future", ErrInvalidMessage)
	}
	if now.After(expiresAt) {
		return fmt.Errorf("%w: peer record expired", ErrInvalidMessage)
	}
	signingBytes, err := record.signingBytes()
	if err != nil {
		return err
	}
	if !utils.Ed25519Verify(record.PublicKey, signingBytes, record.Signature) {
		return fmt.Errorf("%w: invalid peer record signature", ErrSecureSession)
	}
	return nil
}

// MarshalBinary 序列化签名节点记录 + 网络和存储统一使用 Borsh 布局。
func (record SignedPeerRecord) MarshalBinary() ([]byte, error) {
	if err := record.validate(true); err != nil {
		return nil, err
	}
	return record.marshalBinary(true)
}

// UnmarshalSignedPeerRecordBinary 反序列化签名节点记录 + 解码后立即验签和检查过期时间。
func UnmarshalSignedPeerRecordBinary(data []byte) (SignedPeerRecord, error) {
	if len(data) == 0 || len(data) > maxPeerRecordSize {
		return SignedPeerRecord{}, fmt.Errorf("%w: invalid peer record size", ErrInvalidMessage)
	}
	reader := borsh.NewReader(data, maxPeerRecordSize)
	version, err := reader.ReadUint16()
	if err != nil {
		return SignedPeerRecord{}, fmt.Errorf("p2p: read peer record version: %w", err)
	}
	peerID, err := reader.ReadString()
	if err != nil {
		return SignedPeerRecord{}, fmt.Errorf("p2p: read peer record id: %w", err)
	}
	publicKey, err := reader.ReadFixedBytes(utils.Ed25519KeySize)
	if err != nil {
		return SignedPeerRecord{}, fmt.Errorf("p2p: read peer record public key: %w", err)
	}
	addresses, err := readPeerRecordStringSlice(reader, maxPeerRecordAddresses)
	if err != nil {
		return SignedPeerRecord{}, err
	}
	role, err := reader.ReadString()
	if err != nil {
		return SignedPeerRecord{}, fmt.Errorf("p2p: read peer record role: %w", err)
	}
	capabilities, err := reader.ReadUint64()
	if err != nil {
		return SignedPeerRecord{}, fmt.Errorf("p2p: read peer record capabilities: %w", err)
	}
	protocolVersion, err := reader.ReadString()
	if err != nil {
		return SignedPeerRecord{}, fmt.Errorf("p2p: read peer record protocol version: %w", err)
	}
	softwareVersion, err := reader.ReadString()
	if err != nil {
		return SignedPeerRecord{}, fmt.Errorf("p2p: read peer record software version: %w", err)
	}
	latestSlot, err := reader.ReadUint64()
	if err != nil {
		return SignedPeerRecord{}, fmt.Errorf("p2p: read peer record latest slot: %w", err)
	}
	blockHeight, err := reader.ReadUint64()
	if err != nil {
		return SignedPeerRecord{}, fmt.Errorf("p2p: read peer record block height: %w", err)
	}
	bestBlockHash, err := reader.ReadString()
	if err != nil {
		return SignedPeerRecord{}, fmt.Errorf("p2p: read peer record best block hash: %w", err)
	}
	validator, err := reader.ReadBool()
	if err != nil {
		return SignedPeerRecord{}, fmt.Errorf("p2p: read peer record validator: %w", err)
	}
	stakeLamports, err := reader.ReadUint64()
	if err != nil {
		return SignedPeerRecord{}, fmt.Errorf("p2p: read peer record stake: %w", err)
	}
	issuedAtUnixMilli, err := reader.ReadInt64()
	if err != nil {
		return SignedPeerRecord{}, fmt.Errorf("p2p: read peer record issued time: %w", err)
	}
	expiresAtUnixMilli, err := reader.ReadInt64()
	if err != nil {
		return SignedPeerRecord{}, fmt.Errorf("p2p: read peer record expires time: %w", err)
	}
	signature, err := reader.ReadFixedBytes(utils.Ed25519SignatureSize)
	if err != nil {
		return SignedPeerRecord{}, fmt.Errorf("p2p: read peer record signature: %w", err)
	}
	if err := reader.EnsureEOF(); err != nil {
		return SignedPeerRecord{}, fmt.Errorf("p2p: read peer record eof: %w", err)
	}

	record := SignedPeerRecord{
		Version:            version,
		PeerID:             peerID,
		PublicKey:          publicKey,
		Addresses:          addresses,
		Role:               PeerRole(role),
		Capabilities:       PeerCapability(capabilities),
		ProtocolVersion:    protocolVersion,
		SoftwareVersion:    softwareVersion,
		LatestSlot:         latestSlot,
		BlockHeight:        blockHeight,
		BestBlockHash:      bestBlockHash,
		Validator:          validator,
		StakeLamports:      stakeLamports,
		IssuedAtUnixMilli:  issuedAtUnixMilli,
		ExpiresAtUnixMilli: expiresAtUnixMilli,
		Signature:          signature,
	}
	if err := record.Verify(); err != nil {
		return SignedPeerRecord{}, err
	}
	return record, nil
}

// ToPeer 转换为节点快照 + 只允许已验证记录进入 Peer 表和 DHT。
func (record SignedPeerRecord) ToPeer() (Peer, error) {
	if err := record.Verify(); err != nil {
		return Peer{}, err
	}
	addresses := make([]utils.MultiAddress, 0, len(record.Addresses))
	for _, rawAddress := range record.Addresses {
		address, err := utils.ParseMultiAddress(rawAddress)
		if err != nil {
			return Peer{}, err
		}
		addresses = append(addresses, address)
	}
	peer, err := NewPeer(record.PeerID, addresses)
	if err != nil {
		return Peer{}, err
	}
	peer.Role = normalizePeerRole(record.Role)
	peer.Capabilities = record.Capabilities
	peer.ProtocolVersion = record.ProtocolVersion
	peer.SoftwareVersion = record.SoftwareVersion
	peer.LatestSlot = record.LatestSlot
	peer.BlockHeight = record.BlockHeight
	peer.BestBlockHash = record.BestBlockHash
	peer.Validator = record.Validator
	peer.StakeLamports = record.StakeLamports
	peer.LastSeenUnixMilli = time.Now().UnixMilli()
	encoded, err := record.MarshalBinary()
	if err != nil {
		return Peer{}, err
	}
	peer.SignedRecord = encoded
	return peer, nil
}

func (record SignedPeerRecord) signingBytes() ([]byte, error) {
	writer := borsh.NewWriter(maxPeerRecordSize)
	if err := writer.WriteString(peerRecordDomain); err != nil {
		return nil, fmt.Errorf("p2p: marshal peer record domain: %w", err)
	}
	payload, err := record.marshalBinary(false)
	if err != nil {
		return nil, err
	}
	if err := writer.WriteBytes(payload); err != nil {
		return nil, fmt.Errorf("p2p: marshal peer record payload: %w", err)
	}
	return writer.Bytes(), nil
}

func (record SignedPeerRecord) marshalBinary(includeSignature bool) ([]byte, error) {
	if err := record.validate(includeSignature); err != nil {
		return nil, err
	}
	writer := borsh.NewWriter(maxPeerRecordSize)
	writer.WriteUint16(record.Version)
	if err := writer.WriteString(record.PeerID); err != nil {
		return nil, fmt.Errorf("p2p: marshal peer record id: %w", err)
	}
	writer.WriteFixedBytes(record.PublicKey)
	if err := writePeerRecordStringSlice(writer, record.Addresses); err != nil {
		return nil, err
	}
	if err := writer.WriteString(string(normalizePeerRole(record.Role))); err != nil {
		return nil, fmt.Errorf("p2p: marshal peer record role: %w", err)
	}
	writer.WriteUint64(uint64(record.Capabilities))
	if err := writer.WriteString(record.ProtocolVersion); err != nil {
		return nil, fmt.Errorf("p2p: marshal peer record protocol version: %w", err)
	}
	if err := writer.WriteString(record.SoftwareVersion); err != nil {
		return nil, fmt.Errorf("p2p: marshal peer record software version: %w", err)
	}
	writer.WriteUint64(record.LatestSlot)
	writer.WriteUint64(record.BlockHeight)
	if err := writer.WriteString(record.BestBlockHash); err != nil {
		return nil, fmt.Errorf("p2p: marshal peer record best block hash: %w", err)
	}
	writer.WriteBool(record.Validator)
	writer.WriteUint64(record.StakeLamports)
	writer.WriteInt64(record.IssuedAtUnixMilli)
	writer.WriteInt64(record.ExpiresAtUnixMilli)
	if includeSignature {
		writer.WriteFixedBytes(record.Signature)
	}
	return writer.Bytes(), nil
}

func (record SignedPeerRecord) validate(requireSignature bool) error {
	if record.Version != PeerRecordVersion {
		return fmt.Errorf("%w: unsupported peer record version", ErrInvalidMessage)
	}
	if err := validatePeerID(record.PeerID); err != nil {
		return fmt.Errorf("%w: invalid peer record id: %w", ErrInvalidMessage, err)
	}
	if len(record.PublicKey) != utils.Ed25519KeySize {
		return fmt.Errorf("%w: invalid peer record public key", ErrInvalidMessage)
	}
	if utils.Base58Encode(record.PublicKey) != record.PeerID {
		return fmt.Errorf("%w: peer record id does not match public key", ErrSecureSession)
	}
	if len(record.Addresses) == 0 || len(record.Addresses) > maxPeerRecordAddresses {
		return fmt.Errorf("%w: invalid peer record addresses", ErrInvalidMessage)
	}
	for _, rawAddress := range record.Addresses {
		address, err := utils.ParseMultiAddress(rawAddress)
		if err != nil {
			return fmt.Errorf("%w: invalid peer record address: %w", ErrInvalidMessage, err)
		}
		if address.PeerID != record.PeerID {
			return fmt.Errorf("%w: peer record address owner mismatch", ErrInvalidMessage)
		}
	}
	if record.IssuedAtUnixMilli <= 0 || record.ExpiresAtUnixMilli <= record.IssuedAtUnixMilli {
		return fmt.Errorf("%w: invalid peer record time range", ErrInvalidMessage)
	}
	if time.Duration(record.ExpiresAtUnixMilli-record.IssuedAtUnixMilli)*time.Millisecond > maxPeerRecordTTL {
		return fmt.Errorf("%w: peer record ttl too large", ErrInvalidMessage)
	}
	if requireSignature && len(record.Signature) != utils.Ed25519SignatureSize {
		return fmt.Errorf("%w: invalid peer record signature size", ErrSecureSession)
	}
	return nil
}

func peerRecordAddressStrings(addresses []utils.MultiAddress) []string {
	values := make([]string, 0, len(addresses))
	seen := make(map[string]struct{}, len(addresses))
	for _, address := range addresses {
		rawAddress := address.String()
		if _, ok := seen[rawAddress]; ok {
			continue
		}
		seen[rawAddress] = struct{}{}
		values = append(values, rawAddress)
	}
	return values
}

func writePeerRecordStringSlice(writer *borsh.Writer, values []string) error {
	if len(values) > maxPeerRecordAddresses {
		return fmt.Errorf("%w: too many peer record strings", ErrInvalidMessage)
	}
	writer.WriteUint32(uint32(len(values)))
	for _, value := range values {
		if err := writer.WriteString(value); err != nil {
			return fmt.Errorf("p2p: marshal peer record string: %w", err)
		}
	}
	return nil
}

func readPeerRecordStringSlice(reader *borsh.Reader, limit int) ([]string, error) {
	count, err := reader.ReadUint32()
	if err != nil {
		return nil, fmt.Errorf("p2p: read peer record string count: %w", err)
	}
	if int(count) > limit {
		return nil, fmt.Errorf("%w: too many peer record strings", ErrInvalidMessage)
	}
	values := make([]string, 0, int(count))
	for index := 0; index < int(count); index++ {
		value, err := reader.ReadString()
		if err != nil {
			return nil, fmt.Errorf("p2p: read peer record string: %w", err)
		}
		values = append(values, value)
	}
	return values, nil
}

func normalizePeerRecordTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return defaultPeerRecordTTL
	}
	if ttl > maxPeerRecordTTL {
		return maxPeerRecordTTL
	}
	return ttl
}

func verifyPeerSignedRecord(peer Peer) error {
	if len(peer.SignedRecord) == 0 {
		return nil
	}
	record, err := UnmarshalSignedPeerRecordBinary(peer.SignedRecord)
	if err != nil {
		return err
	}
	if record.PeerID != peer.ID {
		return fmt.Errorf("%w: signed peer record id mismatch", ErrInvalidMessage)
	}
	return nil
}

func signedPeerRecordFromPeer(peer Peer) (SignedPeerRecord, bool, error) {
	if len(peer.SignedRecord) == 0 {
		return SignedPeerRecord{}, false, nil
	}
	record, err := UnmarshalSignedPeerRecordBinary(peer.SignedRecord)
	if err != nil {
		return SignedPeerRecord{}, false, err
	}
	if record.PeerID != peer.ID {
		return SignedPeerRecord{}, false, fmt.Errorf("%w: signed peer record id mismatch", ErrInvalidMessage)
	}
	return record, true, nil
}
