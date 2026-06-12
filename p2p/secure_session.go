package p2p

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"solana_golang/codec/borsh"
	"solana_golang/utils"
)

const (
	// SecureSessionProtocolVersion 定义安全会话版本 + 便于后续握手格式升级时显式拒绝未知版本。
	SecureSessionProtocolVersion uint16 = 1

	secureSessionHandshakeMaxSize = 1024
	secureSessionPayloadMaxSize   = DefaultMaxMessageSize
	secureSessionIDSize           = sha256.Size
	secureSessionNonceSize        = 32
	secureSessionNoncePrefixSize  = 4
	secureSessionPayloadOverhead  = 2 + 4 + secureSessionIDSize + 8 + 4 + utils.AESGCMTagSize
	secureSessionDefaultTTL       = 30 * time.Minute
	secureSessionHandshakeMaxSkew = 5 * time.Minute
)

// SecureSessionRole 表示安全会话角色 + 用于派生方向密钥并避免两端角色混乱。
type SecureSessionRole uint8

const (
	SecureSessionRoleUnknown SecureSessionRole = 0
	// SecureSessionRoleInitiator 表示主动拨号方 + 使用 i2r 作为发送方向密钥。
	SecureSessionRoleInitiator SecureSessionRole = 1
	// SecureSessionRoleResponder 表示被动接收方 + 使用 r2i 作为发送方向密钥。
	SecureSessionRoleResponder SecureSessionRole = 2
)

// SecureSessionIdentity 保存本节点握手身份 + 使用长期 Ed25519 密钥绑定 peer id 和临时密钥。
type SecureSessionIdentity struct {
	PeerID             string
	PublicKey          []byte
	PrivateKey         []byte
	NetworkID          string
	SoftwareVersion    string
	MinProtocolVersion uint16
	MaxProtocolVersion uint16
}

// Validate 校验本地安全身份 + 防止错误密钥或伪造 peer id 进入握手流程。
func (identity SecureSessionIdentity) Validate() error {
	if err := validatePeerID(identity.PeerID); err != nil {
		return fmt.Errorf("%w: invalid local peer id: %w", ErrSecureSession, err)
	}
	if len(identity.PublicKey) != utils.Ed25519KeySize {
		return fmt.Errorf("%w: public key requires %d bytes", ErrSecureSession, utils.Ed25519KeySize)
	}
	if len(identity.PrivateKey) != utils.Ed25519KeySize {
		return fmt.Errorf("%w: private key requires %d bytes", ErrSecureSession, utils.Ed25519KeySize)
	}

	derivedPublicKey, err := utils.DeriveEd25519PublicKeyFromPrivateKey(identity.PrivateKey)
	if err != nil {
		return fmt.Errorf("%w: derive local public key: %w", ErrSecureSession, err)
	}
	if !bytes.Equal(derivedPublicKey, identity.PublicKey) {
		return fmt.Errorf("%w: local public key and private key mismatch", ErrSecureSession)
	}
	if utils.Base58Encode(identity.PublicKey) != identity.PeerID {
		return fmt.Errorf("%w: local peer id does not match public key", ErrSecureSession)
	}
	if err := validateSecureSessionNetworkID(identity.NetworkID); err != nil {
		return err
	}
	if err := validateSecureSessionSoftwareVersion(identity.SoftwareVersion); err != nil {
		return err
	}
	if _, err := negotiateSecureProtocolVersion(identity.minProtocolVersion(), identity.maxProtocolVersion(), MessageProtocolVersion, MessageProtocolVersion); err != nil {
		return err
	}
	return nil
}

// Clone 复制安全身份 + 防止调用方修改内部密钥切片。
func (identity SecureSessionIdentity) Clone() SecureSessionIdentity {
	return SecureSessionIdentity{
		PeerID:             identity.PeerID,
		PublicKey:          cloneBytes(identity.PublicKey),
		PrivateKey:         cloneBytes(identity.PrivateKey),
		NetworkID:          identity.NetworkID,
		SoftwareVersion:    identity.SoftwareVersion,
		MinProtocolVersion: identity.minProtocolVersion(),
		MaxProtocolVersion: identity.maxProtocolVersion(),
	}
}

func (identity SecureSessionIdentity) minProtocolVersion() uint16 {
	if identity.MinProtocolVersion == 0 {
		return MessageProtocolVersion
	}
	return identity.MinProtocolVersion
}

func (identity SecureSessionIdentity) maxProtocolVersion() uint16 {
	if identity.MaxProtocolVersion == 0 {
		return MessageProtocolVersion
	}
	return identity.MaxProtocolVersion
}

// SecureSessionHandshake 保存安全会话握手消息 + 用 Borsh 固定布局承载身份、临时公钥和签名。
type SecureSessionHandshake struct {
	Version             uint16
	Role                SecureSessionRole
	PeerID              string
	IdentityPublicKey   []byte
	EphemeralPublicKey  []byte
	Nonce               []byte
	CreatedAtUnixMilli  int64
	NetworkID           string
	SoftwareVersion     string
	MinProtocolVersion  uint16
	MaxProtocolVersion  uint16
	Signature           []byte
	ResumptionTicketID  []byte
	SupportsZeroRTTRead bool
}

// Clone 复制握手消息 + 防止测试或上层修改签名材料。
func (handshake SecureSessionHandshake) Clone() SecureSessionHandshake {
	handshake.IdentityPublicKey = cloneBytes(handshake.IdentityPublicKey)
	handshake.EphemeralPublicKey = cloneBytes(handshake.EphemeralPublicKey)
	handshake.Nonce = cloneBytes(handshake.Nonce)
	handshake.Signature = cloneBytes(handshake.Signature)
	handshake.ResumptionTicketID = cloneBytes(handshake.ResumptionTicketID)
	return handshake
}

// MarshalBinary 序列化握手消息 + 网络握手统一使用 Borsh 确定性布局。
func (handshake SecureSessionHandshake) MarshalBinary() ([]byte, error) {
	if err := handshake.validate(true); err != nil {
		return nil, err
	}
	return handshake.marshalBinary(true)
}

// Sign 签名握手消息 + 将节点长期身份绑定到临时 X25519 公钥。
func (handshake *SecureSessionHandshake) Sign(privateKey []byte) error {
	if err := handshake.validate(false); err != nil {
		return err
	}
	signingBytes, err := handshake.marshalBinary(false)
	if err != nil {
		return err
	}
	signature, err := utils.Ed25519Sign(privateKey, signingBytes)
	if err != nil {
		return fmt.Errorf("%w: sign handshake: %w", ErrSecureSession, err)
	}
	handshake.Signature = signature
	return nil
}

// Verify 验证握手签名 + 防止临时公钥被中间人替换。
func (handshake SecureSessionHandshake) Verify() error {
	if err := handshake.validate(true); err != nil {
		return err
	}
	signingBytes, err := handshake.marshalBinary(false)
	if err != nil {
		return err
	}
	if !utils.Ed25519Verify(handshake.IdentityPublicKey, signingBytes, handshake.Signature) {
		return fmt.Errorf("%w: invalid handshake signature", ErrSecureSession)
	}
	if err := validateSecureSessionHandshakeTime(handshake.CreatedAtUnixMilli); err != nil {
		return err
	}
	return nil
}

// UnmarshalSecureSessionHandshakeBinary 反序列化握手消息 + 解码后立即执行身份和签名边界校验。
func UnmarshalSecureSessionHandshakeBinary(data []byte) (SecureSessionHandshake, error) {
	if len(data) == 0 || len(data) > secureSessionHandshakeMaxSize {
		return SecureSessionHandshake{}, fmt.Errorf("%w: invalid handshake size", ErrSecureSession)
	}

	reader := borsh.NewBorrowedReader(data, secureSessionHandshakeMaxSize)
	version, err := reader.ReadUint16()
	if err != nil {
		return SecureSessionHandshake{}, fmt.Errorf("%w: read handshake version: %w", ErrSecureSession, err)
	}
	role, err := reader.ReadUint8()
	if err != nil {
		return SecureSessionHandshake{}, fmt.Errorf("%w: read handshake role: %w", ErrSecureSession, err)
	}
	peerID, err := reader.ReadString()
	if err != nil {
		return SecureSessionHandshake{}, fmt.Errorf("%w: read handshake peer id: %w", ErrSecureSession, err)
	}
	identityPublicKey, err := reader.ReadFixedBytes(utils.Ed25519KeySize)
	if err != nil {
		return SecureSessionHandshake{}, fmt.Errorf("%w: read identity public key: %w", ErrSecureSession, err)
	}
	ephemeralPublicKey, err := reader.ReadFixedBytes(utils.Curve25519KeySize)
	if err != nil {
		return SecureSessionHandshake{}, fmt.Errorf("%w: read ephemeral public key: %w", ErrSecureSession, err)
	}
	nonce, err := reader.ReadFixedBytes(secureSessionNonceSize)
	if err != nil {
		return SecureSessionHandshake{}, fmt.Errorf("%w: read nonce: %w", ErrSecureSession, err)
	}
	createdAtUnixMilli, err := reader.ReadInt64()
	if err != nil {
		return SecureSessionHandshake{}, fmt.Errorf("%w: read created time: %w", ErrSecureSession, err)
	}
	networkID, err := reader.ReadString()
	if err != nil {
		return SecureSessionHandshake{}, fmt.Errorf("%w: read network id: %w", ErrSecureSession, err)
	}
	softwareVersion, err := reader.ReadString()
	if err != nil {
		return SecureSessionHandshake{}, fmt.Errorf("%w: read software version: %w", ErrSecureSession, err)
	}
	minProtocolVersion, err := reader.ReadUint16()
	if err != nil {
		return SecureSessionHandshake{}, fmt.Errorf("%w: read min protocol version: %w", ErrSecureSession, err)
	}
	maxProtocolVersion, err := reader.ReadUint16()
	if err != nil {
		return SecureSessionHandshake{}, fmt.Errorf("%w: read max protocol version: %w", ErrSecureSession, err)
	}
	resumptionTicketID, err := reader.ReadBytes()
	if err != nil {
		return SecureSessionHandshake{}, fmt.Errorf("%w: read resumption ticket id: %w", ErrSecureSession, err)
	}
	supportsZeroRTTRead, err := reader.ReadBool()
	if err != nil {
		return SecureSessionHandshake{}, fmt.Errorf("%w: read zero rtt flag: %w", ErrSecureSession, err)
	}
	signature, err := reader.ReadFixedBytes(utils.Ed25519SignatureSize)
	if err != nil {
		return SecureSessionHandshake{}, fmt.Errorf("%w: read signature: %w", ErrSecureSession, err)
	}
	if err := reader.EnsureEOF(); err != nil {
		return SecureSessionHandshake{}, fmt.Errorf("%w: trailing handshake bytes: %w", ErrSecureSession, err)
	}

	handshake := SecureSessionHandshake{
		Version:             version,
		Role:                SecureSessionRole(role),
		PeerID:              peerID,
		IdentityPublicKey:   identityPublicKey,
		EphemeralPublicKey:  ephemeralPublicKey,
		Nonce:               nonce,
		CreatedAtUnixMilli:  createdAtUnixMilli,
		NetworkID:           networkID,
		SoftwareVersion:     softwareVersion,
		MinProtocolVersion:  minProtocolVersion,
		MaxProtocolVersion:  maxProtocolVersion,
		Signature:           signature,
		ResumptionTicketID:  resumptionTicketID,
		SupportsZeroRTTRead: supportsZeroRTTRead,
	}
	if err := handshake.Verify(); err != nil {
		return SecureSessionHandshake{}, err
	}
	return handshake, nil
}

// SecureSessionState 保存单次握手临时状态 + 避免临时私钥泄露到握手消息中。
type SecureSessionState struct {
	identity            SecureSessionIdentity
	role                SecureSessionRole
	ephemeralPrivateKey []byte
	handshake           SecureSessionHandshake
}

// NewSecureSessionState 创建安全会话临时状态 + 每次连接使用新的 X25519 密钥提供前向安全。
func NewSecureSessionState(identity SecureSessionIdentity, role SecureSessionRole) (*SecureSessionState, error) {
	if err := identity.Validate(); err != nil {
		return nil, err
	}
	if !isValidSecureSessionRole(role) {
		return nil, fmt.Errorf("%w: invalid secure session role", ErrSecureSession)
	}

	keyPair, err := utils.GenerateCurve25519KeyPair()
	if err != nil {
		return nil, fmt.Errorf("%w: generate ephemeral key: %w", ErrSecureSession, err)
	}
	nonce, err := utils.RandomBytes(secureSessionNonceSize)
	if err != nil {
		return nil, fmt.Errorf("%w: generate handshake nonce: %w", ErrSecureSession, err)
	}

	handshake := SecureSessionHandshake{
		Version:            SecureSessionProtocolVersion,
		Role:               role,
		PeerID:             identity.PeerID,
		IdentityPublicKey:  cloneBytes(identity.PublicKey),
		EphemeralPublicKey: cloneBytes(keyPair.PublicKey),
		Nonce:              nonce,
		CreatedAtUnixMilli: time.Now().UnixMilli(),
		NetworkID:          identity.NetworkID,
		SoftwareVersion:    identity.SoftwareVersion,
		MinProtocolVersion: identity.minProtocolVersion(),
		MaxProtocolVersion: identity.maxProtocolVersion(),
	}
	if err := handshake.Sign(identity.PrivateKey); err != nil {
		return nil, err
	}

	return &SecureSessionState{
		identity:            identity.Clone(),
		role:                role,
		ephemeralPrivateKey: cloneBytes(keyPair.PrivateKey),
		handshake:           handshake,
	}, nil
}

// Handshake 返回本地握手消息 + 调用方只得到副本避免破坏签名材料。
func (state *SecureSessionState) Handshake() SecureSessionHandshake {
	if state == nil {
		return SecureSessionHandshake{}
	}
	return state.handshake.Clone()
}

// Finalize 派生安全会话 + 双方使用同一 transcript 派生方向密钥和会话 ID。
func (state *SecureSessionState) Finalize(remote SecureSessionHandshake, expectedRemotePeerID string) (*SecureSession, error) {
	if state == nil {
		return nil, fmt.Errorf("%w: nil secure session state", ErrSecureSession)
	}
	if err := remote.Verify(); err != nil {
		return nil, err
	}
	if expectedRemotePeerID != "" && remote.PeerID != expectedRemotePeerID {
		return nil, fmt.Errorf("%w: remote peer id mismatch", ErrSecureSession)
	}
	if state.handshake.Role == remote.Role {
		return nil, fmt.Errorf("%w: duplicate secure session role", ErrSecureSession)
	}
	if state.handshake.NetworkID != remote.NetworkID {
		return nil, fmt.Errorf("%w: network id mismatch", ErrSecureSession)
	}
	protocolVersion, err := negotiateSecureProtocolVersion(
		state.handshake.MinProtocolVersion,
		state.handshake.MaxProtocolVersion,
		remote.MinProtocolVersion,
		remote.MaxProtocolVersion,
	)
	if err != nil {
		return nil, err
	}

	sharedSecret, err := utils.GenerateSharedSecret(state.ephemeralPrivateKey, remote.EphemeralPublicKey)
	if err != nil {
		return nil, fmt.Errorf("%w: generate shared secret: %w", ErrSecureSession, err)
	}
	material, err := deriveSecureSessionMaterial(state.handshake, remote, sharedSecret)
	if err != nil {
		return nil, err
	}
	return material.newSession(state.handshake.Role, state.identity.PeerID, remote.PeerID, remote.SoftwareVersion, protocolVersion)
}

// SecurePayload 保存加密后的业务载荷 + 将序号随密文发送以便接收端拒绝重放。
type SecurePayload struct {
	Version    uint16
	SessionID  []byte
	Sequence   uint64
	Ciphertext []byte
}

// MarshalBinary 序列化安全载荷 + 继续沿用 Borsh 作为 P2P 消息内部格式。
func (payload SecurePayload) MarshalBinary(maxMessageSize int) ([]byte, error) {
	if err := payload.Validate(maxMessageSize); err != nil {
		return nil, err
	}
	writer := borsh.NewWriter(maxPayloadSize(maxMessageSize))
	writer.WriteUint16(payload.Version)
	if err := writer.WriteBytes(payload.SessionID); err != nil {
		return nil, fmt.Errorf("%w: marshal session id: %w", ErrSecureSession, err)
	}
	writer.WriteUint64(payload.Sequence)
	if err := writer.WriteBytes(payload.Ciphertext); err != nil {
		return nil, fmt.Errorf("%w: marshal ciphertext: %w", ErrSecureSession, err)
	}
	return writer.BytesView(), nil
}

// Validate 校验安全载荷 + 防止错误会话、空密文和超大密文进入解密路径。
func (payload SecurePayload) Validate(maxMessageSize int) error {
	if payload.Version != SecureSessionProtocolVersion {
		return fmt.Errorf("%w: unsupported secure payload version", ErrSecureSession)
	}
	if len(payload.SessionID) != secureSessionIDSize {
		return fmt.Errorf("%w: invalid session id size", ErrSecureSession)
	}
	if len(payload.Ciphertext) < utils.AESGCMTagSize {
		return fmt.Errorf("%w: ciphertext too short", ErrSecureSession)
	}
	if len(payload.Ciphertext) > maxPayloadSize(maxMessageSize) {
		return fmt.Errorf("%w: ciphertext too large", ErrSecureSession)
	}
	return nil
}

// UnmarshalSecurePayloadBinary 反序列化安全载荷 + 解码后执行大小和版本校验。
func UnmarshalSecurePayloadBinary(data []byte, maxMessageSize int) (SecurePayload, error) {
	if len(data) == 0 || len(data) > maxPayloadSize(maxMessageSize) {
		return SecurePayload{}, fmt.Errorf("%w: invalid secure payload size", ErrSecureSession)
	}

	reader := borsh.NewBorrowedReader(data, maxPayloadSize(maxMessageSize))
	version, err := reader.ReadUint16()
	if err != nil {
		return SecurePayload{}, fmt.Errorf("%w: read secure payload version: %w", ErrSecureSession, err)
	}
	sessionID, err := reader.ReadBytes()
	if err != nil {
		return SecurePayload{}, fmt.Errorf("%w: read session id: %w", ErrSecureSession, err)
	}
	sequence, err := reader.ReadUint64()
	if err != nil {
		return SecurePayload{}, fmt.Errorf("%w: read sequence: %w", ErrSecureSession, err)
	}
	ciphertext, err := reader.ReadBytes()
	if err != nil {
		return SecurePayload{}, fmt.Errorf("%w: read ciphertext: %w", ErrSecureSession, err)
	}
	if err := reader.EnsureEOF(); err != nil {
		return SecurePayload{}, fmt.Errorf("%w: trailing secure payload bytes: %w", ErrSecureSession, err)
	}

	payload := SecurePayload{
		Version:    version,
		SessionID:  sessionID,
		Sequence:   sequence,
		Ciphertext: ciphertext,
	}
	if err := payload.Validate(maxMessageSize); err != nil {
		return SecurePayload{}, err
	}
	return payload, nil
}

// SecureSession 保存已认证会话密钥 + 使用方向密钥、单调序号和互斥锁保证并发安全。
type SecureSession struct {
	localPeerID        string
	remotePeerID       string
	role               SecureSessionRole
	networkID          string
	remoteSoftware     string
	protocolVersion    uint16
	sessionID          []byte
	sendKey            []byte
	receiveKey         []byte
	sendAEAD           cipher.AEAD
	receiveAEAD        cipher.AEAD
	sendNoncePrefix    [secureSessionNoncePrefixSize]byte
	receiveNoncePrefix [secureSessionNoncePrefixSize]byte
	expiresAtUnixMilli int64
	maxMessageSize     int
	sendSequence       uint64
	receiveSequence    uint64
	sendMutex          sync.Mutex
	receiveMutex       sync.Mutex
}

// LocalPeerID 返回本地节点 ID + 供连接管理器记录认证结果。
func (session *SecureSession) LocalPeerID() string {
	if session == nil {
		return ""
	}
	return session.localPeerID
}

// RemotePeerID 返回远端节点 ID + 供 Host 使用认证后的身份写入连接池。
func (session *SecureSession) RemotePeerID() string {
	if session == nil {
		return ""
	}
	return session.remotePeerID
}

// Role 返回安全会话角色 + 供连接池在双向同时拨号时做确定性仲裁。
func (session *SecureSession) Role() SecureSessionRole {
	if session == nil {
		return SecureSessionRoleUnknown
	}
	return session.role
}

// SessionID 返回会话 ID 副本 + 防止外部修改重放校验依据。
func (session *SecureSession) SessionID() []byte {
	if session == nil {
		return nil
	}
	return cloneBytes(session.sessionID)
}

// NetworkID 返回协商后的网络 ID + 用于连接池和日志定位跨网络误连。
func (session *SecureSession) NetworkID() string {
	if session == nil {
		return ""
	}
	return session.networkID
}

// RemoteSoftwareVersion 返回远端软件版本 + 仅用于观测和兼容策略判断。
func (session *SecureSession) RemoteSoftwareVersion() string {
	if session == nil {
		return ""
	}
	return session.remoteSoftware
}

// ProtocolVersion 返回协商后的 P2P 协议版本 + 用于后续消息格式升级。
func (session *SecureSession) ProtocolVersion() uint16 {
	if session == nil {
		return 0
	}
	return session.protocolVersion
}

// IsExpired 判断会话是否过期 + 避免长期复用同一组方向密钥。
func (session *SecureSession) IsExpired(now time.Time) bool {
	if session == nil {
		return true
	}
	return now.UnixMilli() >= session.expiresAtUnixMilli
}

func (session *SecureSession) messageSizeLimit() int {
	if session == nil {
		return DefaultMaxMessageSize
	}
	return normalizeMaxMessageSize(session.maxMessageSize)
}

// Seal 加密业务载荷 + 使用发送方向密钥和单调序号生成唯一 GCM nonce。
func (session *SecureSession) Seal(plaintext []byte, associatedData []byte) (SecurePayload, error) {
	if err := session.validate(); err != nil {
		return SecurePayload{}, err
	}
	if session.IsExpired(time.Now()) {
		return SecurePayload{}, fmt.Errorf("%w: secure session expired", ErrSecureSession)
	}
	maxMessageSize := session.messageSizeLimit()
	if len(plaintext) > secureSessionPlaintextMaxSize(maxMessageSize) {
		return SecurePayload{}, fmt.Errorf("%w: plaintext too large", ErrSecureSession)
	}

	session.sendMutex.Lock()
	defer session.sendMutex.Unlock()
	if session.sendSequence == math.MaxUint64 {
		return SecurePayload{}, fmt.Errorf("%w: send sequence exhausted", ErrSecureSession)
	}

	sequence := session.sendSequence
	nonce := secureSessionNonce(session.sendNoncePrefix, sequence)
	ciphertext := session.sendAEAD.Seal(make([]byte, 0, len(plaintext)+utils.AESGCMTagSize), nonce[:], plaintext, associatedData)
	session.sendSequence++

	return SecurePayload{
		Version:    SecureSessionProtocolVersion,
		SessionID:  cloneBytes(session.sessionID),
		Sequence:   sequence,
		Ciphertext: ciphertext,
	}, nil
}

// Open 解密业务载荷 + 按接收序号精确递增拒绝重放和乱序密文。
func (session *SecureSession) Open(payload SecurePayload, associatedData []byte) ([]byte, error) {
	if err := session.validate(); err != nil {
		return nil, err
	}
	if session.IsExpired(time.Now()) {
		return nil, fmt.Errorf("%w: secure session expired", ErrSecureSession)
	}
	if err := payload.Validate(session.messageSizeLimit()); err != nil {
		return nil, err
	}
	if !utils.SecureEqual(payload.SessionID, session.sessionID) {
		return nil, fmt.Errorf("%w: session id mismatch", ErrSecureSession)
	}

	session.receiveMutex.Lock()
	defer session.receiveMutex.Unlock()
	if payload.Sequence != session.receiveSequence {
		return nil, fmt.Errorf("%w: unexpected sequence", ErrSecureSession)
	}

	nonce := secureSessionNonce(session.receiveNoncePrefix, payload.Sequence)
	plaintext, err := session.receiveAEAD.Open(nil, nonce[:], payload.Ciphertext, associatedData)
	if err != nil {
		return nil, fmt.Errorf("%w: decrypt payload: %w", ErrSecureSession, err)
	}
	session.receiveSequence++
	return plaintext, nil
}

// ResumptionTicket 生成恢复票据 + 只保存恢复材料而不持久化当前明文方向密钥。
func (session *SecureSession) ResumptionTicket() (SecureSessionResumptionTicket, error) {
	if err := session.validate(); err != nil {
		return SecureSessionResumptionTicket{}, err
	}
	secretInput := utils.ConcatBytes([]byte("p2p secure session resumption"), session.sessionID, session.sendKey, session.receiveKey)
	secret := utils.SHA256(secretInput)
	return SecureSessionResumptionTicket{
		Version:            SecureSessionProtocolVersion,
		RemotePeerID:       session.remotePeerID,
		NetworkID:          session.networkID,
		ProtocolVersion:    session.protocolVersion,
		SessionID:          cloneBytes(session.sessionID),
		ResumptionSecret:   secret,
		CreatedAtUnixMilli: time.Now().UnixMilli(),
		ExpiresAtUnixMilli: session.expiresAtUnixMilli,
	}, nil
}

func (session *SecureSession) validate() error {
	if session == nil {
		return fmt.Errorf("%w: nil secure session", ErrSecureSession)
	}
	if err := validatePeerID(session.localPeerID); err != nil {
		return fmt.Errorf("%w: invalid local peer id: %w", ErrSecureSession, err)
	}
	if err := validatePeerID(session.remotePeerID); err != nil {
		return fmt.Errorf("%w: invalid remote peer id: %w", ErrSecureSession, err)
	}
	if !isValidSecureSessionRole(session.role) {
		return fmt.Errorf("%w: invalid session role", ErrSecureSession)
	}
	if len(session.sessionID) != secureSessionIDSize {
		return fmt.Errorf("%w: invalid session id", ErrSecureSession)
	}
	if len(session.sendKey) != utils.AES256KeySize || len(session.receiveKey) != utils.AES256KeySize {
		return fmt.Errorf("%w: invalid aes key size", ErrSecureSession)
	}
	if session.sendAEAD == nil || session.receiveAEAD == nil {
		return fmt.Errorf("%w: missing aes gcm", ErrSecureSession)
	}
	if err := validateSecureSessionNetworkID(session.networkID); err != nil {
		return err
	}
	if session.protocolVersion == 0 {
		return fmt.Errorf("%w: invalid negotiated protocol version", ErrSecureSession)
	}
	if session.expiresAtUnixMilli <= 0 {
		return fmt.Errorf("%w: invalid expiration", ErrSecureSession)
	}
	return nil
}

// SecureSessionResumptionTicket 保存 0RTT 恢复材料 + 后续只允许幂等消息在确认前使用。
type SecureSessionResumptionTicket struct {
	Version            uint16
	RemotePeerID       string
	NetworkID          string
	ProtocolVersion    uint16
	SessionID          []byte
	ResumptionSecret   []byte
	CreatedAtUnixMilli int64
	ExpiresAtUnixMilli int64
}

// Clone 复制恢复票据 + 防止调用方修改 Host 内部缓存的恢复材料。
func (ticket SecureSessionResumptionTicket) Clone() SecureSessionResumptionTicket {
	ticket.SessionID = cloneBytes(ticket.SessionID)
	ticket.ResumptionSecret = cloneBytes(ticket.ResumptionSecret)
	return ticket
}

// Validate 校验恢复票据 + 防止过期或错误长度的材料进入 0RTT 流程。
func (ticket SecureSessionResumptionTicket) Validate() error {
	if ticket.Version != SecureSessionProtocolVersion {
		return fmt.Errorf("%w: unsupported resumption ticket version", ErrSecureSession)
	}
	if err := validatePeerID(ticket.RemotePeerID); err != nil {
		return fmt.Errorf("%w: invalid ticket peer id: %w", ErrSecureSession, err)
	}
	if err := validateSecureSessionNetworkID(ticket.NetworkID); err != nil {
		return err
	}
	if ticket.ProtocolVersion == 0 {
		return fmt.Errorf("%w: invalid ticket protocol version", ErrSecureSession)
	}
	if len(ticket.SessionID) != secureSessionIDSize {
		return fmt.Errorf("%w: invalid ticket session id", ErrSecureSession)
	}
	if len(ticket.ResumptionSecret) != secureSessionIDSize {
		return fmt.Errorf("%w: invalid ticket secret", ErrSecureSession)
	}
	if ticket.CreatedAtUnixMilli <= 0 || ticket.ExpiresAtUnixMilli <= ticket.CreatedAtUnixMilli {
		return fmt.Errorf("%w: invalid ticket time", ErrSecureSession)
	}
	return nil
}

// IsExpired 判断恢复票据是否过期 + 过期材料不得用于 0RTT。
func (ticket SecureSessionResumptionTicket) IsExpired(now time.Time) bool {
	return now.UnixMilli() >= ticket.ExpiresAtUnixMilli
}

// SecureConnection 封装认证后的连接 + 保持原 Connection 接口并自动加解密业务 payload。
type SecureConnection struct {
	connection Connection
	session    *SecureSession
}

// SecureDialConnection 主动建立安全连接 + 先完成握手再返回加密连接包装器。
func SecureDialConnection(ctx context.Context, connection Connection, identity SecureSessionIdentity) (*SecureConnection, error) {
	return SecureDialConnectionWithMaxMessageSize(ctx, connection, identity, DefaultMaxMessageSize)
}

// SecureDialConnectionWithMaxMessageSize 主动建立安全连接 + 让大包上限随 Host 配置传入会话。
func SecureDialConnectionWithMaxMessageSize(ctx context.Context, connection Connection, identity SecureSessionIdentity, maxMessageSize int) (*SecureConnection, error) {
	state, err := NewSecureSessionState(identity, SecureSessionRoleInitiator)
	if err != nil {
		return nil, err
	}
	request, err := newSecureSessionRequest(identity.PeerID, connection.RemotePeerID(), state.Handshake())
	if err != nil {
		return nil, err
	}
	if err := connection.WriteMessage(ctx, request); err != nil {
		return nil, fmt.Errorf("%w: write secure session request: %w", ErrSecureSession, err)
	}

	response, err := connection.ReadMessage(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: read secure session response: %w", ErrSecureSession, err)
	}
	remoteHandshake, err := parseSecureSessionResponse(request, response)
	if err != nil {
		return nil, err
	}
	session, err := state.Finalize(remoteHandshake, connection.RemotePeerID())
	if err != nil {
		return nil, err
	}
	return NewSecureConnectionWithMaxMessageSize(connection, session, maxMessageSize)
}

// SecureAcceptConnection 接收入站安全连接 + 读取握手请求后返回加密连接包装器。
func SecureAcceptConnection(ctx context.Context, connection Connection, identity SecureSessionIdentity) (*SecureConnection, error) {
	return SecureAcceptConnectionWithMaxMessageSize(ctx, connection, identity, DefaultMaxMessageSize)
}

// SecureAcceptConnectionWithMaxMessageSize 接收入站安全连接 + 保持安全载荷上限与传输层一致。
func SecureAcceptConnectionWithMaxMessageSize(ctx context.Context, connection Connection, identity SecureSessionIdentity, maxMessageSize int) (*SecureConnection, error) {
	request, err := connection.ReadMessage(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: read secure session request: %w", ErrSecureSession, err)
	}
	remoteHandshake, err := parseSecureSessionRequest(request, identity.PeerID)
	if err != nil {
		return nil, err
	}

	state, err := NewSecureSessionState(identity, SecureSessionRoleResponder)
	if err != nil {
		return nil, err
	}
	session, err := state.Finalize(remoteHandshake, remoteHandshake.PeerID)
	if err != nil {
		return nil, err
	}
	response, err := newSecureSessionResponse(identity.PeerID, remoteHandshake.PeerID, request.ID, state.Handshake())
	if err != nil {
		return nil, err
	}
	if err := connection.WriteMessage(ctx, response); err != nil {
		return nil, fmt.Errorf("%w: write secure session response: %w", ErrSecureSession, err)
	}
	return NewSecureConnectionWithMaxMessageSize(connection, session, maxMessageSize)
}

// NewSecureConnection 创建安全连接包装器 + 显式接收已完成握手的会话对象。
func NewSecureConnection(connection Connection, session *SecureSession) (*SecureConnection, error) {
	return NewSecureConnectionWithMaxMessageSize(connection, session, DefaultMaxMessageSize)
}

// NewSecureConnectionWithMaxMessageSize 创建安全连接包装器 + 固定会话级消息上限避免读写边界不一致。
func NewSecureConnectionWithMaxMessageSize(connection Connection, session *SecureSession, maxMessageSize int) (*SecureConnection, error) {
	if connection == nil {
		return nil, fmt.Errorf("%w: nil raw connection", ErrSecureSession)
	}
	if err := session.validate(); err != nil {
		return nil, err
	}
	session.maxMessageSize = normalizeMaxMessageSize(maxMessageSize)
	return &SecureConnection{connection: connection, session: session}, nil
}

func (connection *SecureConnection) ID() string {
	return connection.connection.ID()
}

func (connection *SecureConnection) Protocol() utils.MultiAddressProtocol {
	return connection.connection.Protocol()
}

func (connection *SecureConnection) RemotePeerID() string {
	return connection.session.RemotePeerID()
}

func (connection *SecureConnection) LocalAddress() string {
	return connection.connection.LocalAddress()
}

func (connection *SecureConnection) RemoteAddress() string {
	return connection.connection.RemoteAddress()
}

// ReadMessage 读取并解密消息 + 外层路由字段参与认证防止密文被改挂协议。
func (connection *SecureConnection) ReadMessage(ctx context.Context) (Message, error) {
	message, err := connection.connection.ReadMessage(ctx)
	if err != nil {
		return Message{}, err
	}
	associatedData, err := secureMessageAssociatedData(message)
	if err != nil {
		return Message{}, err
	}
	payload, err := UnmarshalSecurePayloadBinary(message.Payload, connection.session.messageSizeLimit())
	if err != nil {
		return Message{}, err
	}
	plaintext, err := connection.session.Open(payload, associatedData)
	if err != nil {
		return Message{}, err
	}
	message.Payload = plaintext
	if err := message.Validate(connection.session.messageSizeLimit()); err != nil {
		return Message{}, err
	}
	return message, nil
}

// WriteMessage 加密并写入消息 + 只加密业务 payload，外层路由字段用于连接内分发和认证数据。
func (connection *SecureConnection) WriteMessage(ctx context.Context, message Message) error {
	outbound := message
	maxMessageSize := connection.session.messageSizeLimit()
	if err := outbound.Validate(maxMessageSize); err != nil {
		return err
	}
	associatedData, err := secureMessageAssociatedData(outbound)
	if err != nil {
		return err
	}
	payload, err := connection.session.Seal(outbound.Payload, associatedData)
	if err != nil {
		return err
	}
	encodedPayload, err := payload.MarshalBinary(maxMessageSize)
	if err != nil {
		return err
	}
	outbound.Payload = encodedPayload
	if err := outbound.Validate(maxMessageSize); err != nil {
		return err
	}
	return connection.connection.WriteMessage(ctx, outbound)
}

// Close 关闭底层连接 + 安全包装器不持有额外网络资源。
func (connection *SecureConnection) Close() error {
	return connection.connection.Close()
}

// Session 返回安全会话指针 + 供连接管理器生成恢复票据和调试状态。
func (connection *SecureConnection) Session() *SecureSession {
	return connection.session
}

func (handshake SecureSessionHandshake) marshalBinary(includeSignature bool) ([]byte, error) {
	writer := borsh.NewWriter(secureSessionHandshakeMaxSize)
	writer.WriteUint16(handshake.Version)
	writer.WriteUint8(uint8(handshake.Role))
	if err := writer.WriteString(handshake.PeerID); err != nil {
		return nil, fmt.Errorf("%w: marshal peer id: %w", ErrSecureSession, err)
	}
	writer.WriteFixedBytes(handshake.IdentityPublicKey)
	writer.WriteFixedBytes(handshake.EphemeralPublicKey)
	writer.WriteFixedBytes(handshake.Nonce)
	writer.WriteInt64(handshake.CreatedAtUnixMilli)
	if err := writer.WriteString(handshake.NetworkID); err != nil {
		return nil, fmt.Errorf("%w: marshal network id: %w", ErrSecureSession, err)
	}
	if err := writer.WriteString(handshake.SoftwareVersion); err != nil {
		return nil, fmt.Errorf("%w: marshal software version: %w", ErrSecureSession, err)
	}
	writer.WriteUint16(handshake.MinProtocolVersion)
	writer.WriteUint16(handshake.MaxProtocolVersion)
	if err := writer.WriteBytes(handshake.ResumptionTicketID); err != nil {
		return nil, fmt.Errorf("%w: marshal ticket id: %w", ErrSecureSession, err)
	}
	writer.WriteBool(handshake.SupportsZeroRTTRead)
	if includeSignature {
		writer.WriteFixedBytes(handshake.Signature)
	}
	return writer.BytesView(), nil
}

func (handshake SecureSessionHandshake) validate(requireSignature bool) error {
	if handshake.Version != SecureSessionProtocolVersion {
		return fmt.Errorf("%w: unsupported handshake version", ErrSecureSession)
	}
	if !isValidSecureSessionRole(handshake.Role) {
		return fmt.Errorf("%w: invalid handshake role", ErrSecureSession)
	}
	if err := validatePeerID(handshake.PeerID); err != nil {
		return fmt.Errorf("%w: invalid handshake peer id: %w", ErrSecureSession, err)
	}
	if len(handshake.IdentityPublicKey) != utils.Ed25519KeySize {
		return fmt.Errorf("%w: invalid identity public key size", ErrSecureSession)
	}
	if len(handshake.EphemeralPublicKey) != utils.Curve25519KeySize || isZeroBytes(handshake.EphemeralPublicKey) {
		return fmt.Errorf("%w: invalid ephemeral public key", ErrSecureSession)
	}
	if len(handshake.Nonce) != secureSessionNonceSize || isZeroBytes(handshake.Nonce) {
		return fmt.Errorf("%w: invalid handshake nonce", ErrSecureSession)
	}
	if handshake.CreatedAtUnixMilli <= 0 {
		return fmt.Errorf("%w: invalid handshake time", ErrSecureSession)
	}
	if err := validateSecureSessionNetworkID(handshake.NetworkID); err != nil {
		return err
	}
	if err := validateSecureSessionSoftwareVersion(handshake.SoftwareVersion); err != nil {
		return err
	}
	if _, err := negotiateSecureProtocolVersion(handshake.MinProtocolVersion, handshake.MaxProtocolVersion, MessageProtocolVersion, MessageProtocolVersion); err != nil {
		return err
	}
	if len(handshake.ResumptionTicketID) > secureSessionIDSize {
		return fmt.Errorf("%w: resumption ticket id too large", ErrSecureSession)
	}
	if utils.Base58Encode(handshake.IdentityPublicKey) != handshake.PeerID {
		return fmt.Errorf("%w: peer id does not match identity public key", ErrSecureSession)
	}
	if requireSignature && len(handshake.Signature) != utils.Ed25519SignatureSize {
		return fmt.Errorf("%w: invalid handshake signature size", ErrSecureSession)
	}
	return nil
}

func validateSecureSessionHandshakeTime(createdAtUnixMilli int64) error {
	createdAt := time.UnixMilli(createdAtUnixMilli)
	if time.Since(createdAt) > secureSessionHandshakeMaxSkew {
		return fmt.Errorf("%w: stale handshake", ErrSecureSession)
	}
	if time.Until(createdAt) > secureSessionHandshakeMaxSkew {
		return fmt.Errorf("%w: future handshake", ErrSecureSession)
	}
	return nil
}

func isValidSecureSessionRole(role SecureSessionRole) bool {
	return role == SecureSessionRoleInitiator || role == SecureSessionRoleResponder
}

func validateSecureSessionNetworkID(networkID string) error {
	normalized := strings.TrimSpace(networkID)
	if normalized == "" {
		return fmt.Errorf("%w: network id cannot be empty", ErrSecureSession)
	}
	if normalized != networkID {
		return fmt.Errorf("%w: network id has leading or trailing spaces", ErrSecureSession)
	}
	if len(networkID) > 128 {
		return fmt.Errorf("%w: network id too long", ErrSecureSession)
	}
	if containsControlCharacter(networkID) {
		return fmt.Errorf("%w: network id contains control character", ErrSecureSession)
	}
	return nil
}

func validateSecureSessionSoftwareVersion(version string) error {
	normalized := strings.TrimSpace(version)
	if normalized == "" {
		return fmt.Errorf("%w: software version cannot be empty", ErrSecureSession)
	}
	if normalized != version {
		return fmt.Errorf("%w: software version has leading or trailing spaces", ErrSecureSession)
	}
	if len(version) > 64 {
		return fmt.Errorf("%w: software version too long", ErrSecureSession)
	}
	if containsControlCharacter(version) {
		return fmt.Errorf("%w: software version contains control character", ErrSecureSession)
	}
	return nil
}

func negotiateSecureProtocolVersion(localMin uint16, localMax uint16, remoteMin uint16, remoteMax uint16) (uint16, error) {
	if localMin == 0 || localMax == 0 || remoteMin == 0 || remoteMax == 0 {
		return 0, fmt.Errorf("%w: protocol version cannot be zero", ErrSecureSession)
	}
	if localMin > localMax || remoteMin > remoteMax {
		return 0, fmt.Errorf("%w: invalid protocol version range", ErrSecureSession)
	}
	minSupported := maxUint16(localMin, remoteMin)
	maxSupported := minUint16(localMax, remoteMax)
	if minSupported > maxSupported {
		return 0, fmt.Errorf("%w: protocol version has no overlap", ErrSecureSession)
	}
	if MessageProtocolVersion < minSupported || MessageProtocolVersion > maxSupported {
		return 0, fmt.Errorf("%w: local message protocol version unsupported", ErrSecureSession)
	}
	return MessageProtocolVersion, nil
}

func containsControlCharacter(value string) bool {
	for _, item := range value {
		if item < 32 || item == 127 {
			return true
		}
	}
	return false
}

func maxUint16(left uint16, right uint16) uint16 {
	if left > right {
		return left
	}
	return right
}

func minUint16(left uint16, right uint16) uint16 {
	if left < right {
		return left
	}
	return right
}

type secureSessionMaterial struct {
	sessionID          []byte
	networkID          string
	initiatorToKey     []byte
	responderToKey     []byte
	initiatorNonce     [secureSessionNoncePrefixSize]byte
	responderNonce     [secureSessionNoncePrefixSize]byte
	expiresAtUnixMilli int64
}

func deriveSecureSessionMaterial(local SecureSessionHandshake, remote SecureSessionHandshake, sharedSecret []byte) (secureSessionMaterial, error) {
	transcript, err := secureSessionTranscript(local, remote)
	if err != nil {
		return secureSessionMaterial{}, err
	}
	initiatorKey, err := deriveSecureSessionKey(sharedSecret, transcript, "initiator-to-responder-key")
	if err != nil {
		return secureSessionMaterial{}, err
	}
	responderKey, err := deriveSecureSessionKey(sharedSecret, transcript, "responder-to-initiator-key")
	if err != nil {
		return secureSessionMaterial{}, err
	}
	return secureSessionMaterial{
		sessionID:          secureSessionHash("session-id", transcript),
		networkID:          local.NetworkID,
		initiatorToKey:     initiatorKey,
		responderToKey:     responderKey,
		initiatorNonce:     secureSessionNoncePrefix("initiator-to-responder-nonce", transcript),
		responderNonce:     secureSessionNoncePrefix("responder-to-initiator-nonce", transcript),
		expiresAtUnixMilli: time.Now().Add(secureSessionDefaultTTL).UnixMilli(),
	}, nil
}

func (material secureSessionMaterial) newSession(
	role SecureSessionRole,
	localPeerID string,
	remotePeerID string,
	remoteSoftwareVersion string,
	protocolVersion uint16,
) (*SecureSession, error) {
	session := &SecureSession{
		localPeerID:        localPeerID,
		remotePeerID:       remotePeerID,
		role:               role,
		networkID:          material.networkID,
		remoteSoftware:     remoteSoftwareVersion,
		protocolVersion:    protocolVersion,
		sessionID:          cloneBytes(material.sessionID),
		expiresAtUnixMilli: material.expiresAtUnixMilli,
	}
	if role == SecureSessionRoleInitiator {
		session.sendKey = cloneBytes(material.initiatorToKey)
		session.receiveKey = cloneBytes(material.responderToKey)
		session.sendNoncePrefix = material.initiatorNonce
		session.receiveNoncePrefix = material.responderNonce
		return initializeSecureSessionAEAD(session)
	}
	session.sendKey = cloneBytes(material.responderToKey)
	session.receiveKey = cloneBytes(material.initiatorToKey)
	session.sendNoncePrefix = material.responderNonce
	session.receiveNoncePrefix = material.initiatorNonce
	return initializeSecureSessionAEAD(session)
}

func initializeSecureSessionAEAD(session *SecureSession) (*SecureSession, error) {
	sendAEAD, err := newSecureSessionAEAD(session.sendKey)
	if err != nil {
		return nil, err
	}
	receiveAEAD, err := newSecureSessionAEAD(session.receiveKey)
	if err != nil {
		return nil, err
	}
	session.sendAEAD = sendAEAD
	session.receiveAEAD = receiveAEAD
	return session, nil
}

func secureSessionTranscript(local SecureSessionHandshake, remote SecureSessionHandshake) ([]byte, error) {
	initiator, responder, err := canonicalSecureSessionHandshakes(local, remote)
	if err != nil {
		return nil, err
	}
	initiatorBytes, err := initiator.MarshalBinary()
	if err != nil {
		return nil, err
	}
	responderBytes, err := responder.MarshalBinary()
	if err != nil {
		return nil, err
	}

	writer := borsh.NewWriter(secureSessionHandshakeMaxSize * 2)
	if err := writer.WriteString("p2p-secure-session-transcript-v1"); err != nil {
		return nil, fmt.Errorf("%w: marshal transcript domain: %w", ErrSecureSession, err)
	}
	if err := writer.WriteBytes(initiatorBytes); err != nil {
		return nil, fmt.Errorf("%w: marshal initiator transcript: %w", ErrSecureSession, err)
	}
	if err := writer.WriteBytes(responderBytes); err != nil {
		return nil, fmt.Errorf("%w: marshal responder transcript: %w", ErrSecureSession, err)
	}
	return utils.SHA256(writer.BytesView()), nil
}

func canonicalSecureSessionHandshakes(local SecureSessionHandshake, remote SecureSessionHandshake) (SecureSessionHandshake, SecureSessionHandshake, error) {
	if local.Role == remote.Role {
		return SecureSessionHandshake{}, SecureSessionHandshake{}, fmt.Errorf("%w: duplicate role in transcript", ErrSecureSession)
	}
	if local.Role == SecureSessionRoleInitiator {
		return local, remote, nil
	}
	if remote.Role == SecureSessionRoleInitiator {
		return remote, local, nil
	}
	return SecureSessionHandshake{}, SecureSessionHandshake{}, fmt.Errorf("%w: missing initiator", ErrSecureSession)
}

func deriveSecureSessionKey(sharedSecret []byte, transcript []byte, label string) ([]byte, error) {
	salt := secureSessionHash(label, transcript)
	key, err := utils.DeriveAESKeyWithSalt(sharedSecret, salt)
	if err != nil {
		return nil, fmt.Errorf("%w: derive %s: %w", ErrSecureSession, label, err)
	}
	return key, nil
}

func secureSessionHash(label string, transcript []byte) []byte {
	hash := sha256.New()
	hash.Write([]byte("p2p-secure-session-v1"))
	hash.Write([]byte(label))
	hash.Write(transcript)
	return hash.Sum(nil)
}

func secureSessionNoncePrefix(label string, transcript []byte) [secureSessionNoncePrefixSize]byte {
	hash := secureSessionHash(label, transcript)
	var prefix [secureSessionNoncePrefixSize]byte
	copy(prefix[:], hash[:secureSessionNoncePrefixSize])
	return prefix
}

func secureSessionNonce(prefix [secureSessionNoncePrefixSize]byte, sequence uint64) [utils.AESGCMNonceSize]byte {
	var nonce [utils.AESGCMNonceSize]byte
	copy(nonce[:secureSessionNoncePrefixSize], prefix[:])
	binary.BigEndian.PutUint64(nonce[secureSessionNoncePrefixSize:], sequence)
	return nonce
}

func secureSessionPlaintextMaxSize(maxMessageSize int) int {
	maxSize := maxPayloadSize(maxMessageSize) - secureSessionPayloadOverhead
	if maxSize < 0 {
		return 0
	}
	return maxSize
}

func newSecureSessionAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != utils.AES256KeySize {
		return nil, fmt.Errorf("%w: invalid aes key size", ErrSecureSession)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: create aes cipher: %w", ErrSecureSession, err)
	}
	aead, err := cipher.NewGCMWithNonceSize(block, utils.AESGCMNonceSize)
	if err != nil {
		return nil, fmt.Errorf("%w: create aes gcm: %w", ErrSecureSession, err)
	}
	return aead, nil
}

func newSecureSessionRequest(localPeerID string, remotePeerID string, handshake SecureSessionHandshake) (Message, error) {
	payload, err := handshake.MarshalBinary()
	if err != nil {
		return Message{}, err
	}
	request, err := NewRequestMessage(localPeerID, ProtocolSecureSessionV1, payload)
	if err != nil {
		return Message{}, err
	}
	request.ToPeerID = remotePeerID
	return request, request.Validate(DefaultMaxMessageSize)
}

func newSecureSessionResponse(localPeerID string, remotePeerID string, requestID string, handshake SecureSessionHandshake) (Message, error) {
	payload, err := handshake.MarshalBinary()
	if err != nil {
		return Message{}, err
	}
	response, err := NewResponseMessage(localPeerID, ProtocolSecureSessionV1, requestID, payload)
	if err != nil {
		return Message{}, err
	}
	response.ToPeerID = remotePeerID
	return response, response.Validate(DefaultMaxMessageSize)
}

func parseSecureSessionRequest(message Message, localPeerID string) (SecureSessionHandshake, error) {
	if message.Type != ProtocolSecureSessionV1 || !message.IsRequest() {
		return SecureSessionHandshake{}, fmt.Errorf("%w: invalid secure session request", ErrSecureSession)
	}
	if message.ToPeerID != "" && message.ToPeerID != localPeerID {
		return SecureSessionHandshake{}, fmt.Errorf("%w: secure session request target mismatch", ErrSecureSession)
	}
	handshake, err := UnmarshalSecureSessionHandshakeBinary(message.Payload)
	if err != nil {
		return SecureSessionHandshake{}, err
	}
	if handshake.Role != SecureSessionRoleInitiator {
		return SecureSessionHandshake{}, fmt.Errorf("%w: request must use initiator role", ErrSecureSession)
	}
	if message.FromPeerID != "" && message.FromPeerID != handshake.PeerID {
		return SecureSessionHandshake{}, fmt.Errorf("%w: request peer id mismatch", ErrSecureSession)
	}
	return handshake, nil
}

func parseSecureSessionResponse(request Message, response Message) (SecureSessionHandshake, error) {
	if response.Type != ProtocolSecureSessionV1 || !response.IsResponse() {
		return SecureSessionHandshake{}, fmt.Errorf("%w: invalid secure session response", ErrSecureSession)
	}
	if response.RequestID != request.ID {
		return SecureSessionHandshake{}, fmt.Errorf("%w: secure session response request id mismatch", ErrSecureSession)
	}
	handshake, err := UnmarshalSecureSessionHandshakeBinary(response.Payload)
	if err != nil {
		return SecureSessionHandshake{}, err
	}
	if handshake.Role != SecureSessionRoleResponder {
		return SecureSessionHandshake{}, fmt.Errorf("%w: response must use responder role", ErrSecureSession)
	}
	if response.FromPeerID != "" && response.FromPeerID != handshake.PeerID {
		return SecureSessionHandshake{}, fmt.Errorf("%w: response peer id mismatch", ErrSecureSession)
	}
	return handshake, nil
}

func secureMessageAssociatedData(message Message) ([]byte, error) {
	writer := borsh.NewWriter(DefaultMaxMessageSize)
	writer.WriteUint16(message.effectiveVersion())
	if err := writer.WriteString(strings.ToLower(message.ID)); err != nil {
		return nil, fmt.Errorf("%w: associated message id: %w", ErrSecureSession, err)
	}
	writer.WriteUint32(uint32(message.Type))
	if err := writer.WriteString(message.FromPeerID); err != nil {
		return nil, fmt.Errorf("%w: associated from peer id: %w", ErrSecureSession, err)
	}
	if err := writer.WriteString(message.ToPeerID); err != nil {
		return nil, fmt.Errorf("%w: associated to peer id: %w", ErrSecureSession, err)
	}
	if err := writer.WriteString(strings.ToLower(message.RequestID)); err != nil {
		return nil, fmt.Errorf("%w: associated request id: %w", ErrSecureSession, err)
	}
	writer.WriteUint8(uint8(message.Flag))
	writer.WriteInt64(message.CreatedAtUnixMilli)
	return writer.BytesView(), nil
}
