package zk

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	ProtocolVersion       = uint16(1)
	DefaultMaxProofBytes  = 2048
	MaxPublicInputBytes   = 4096
	MaxPublicInputCount   = 64
	HashSize              = 32
	domainProofEnvelopeV1 = "solana_golang.zk.proof.v1"
	domainPublicInputsV1  = "solana_golang.zk.public_inputs.v1"
)

var (
	ErrInvalidProof            = errors.New("zk: invalid proof")
	ErrInvalidPublicInput      = errors.New("zk: invalid public input")
	ErrInvalidVerifier         = errors.New("zk: invalid verifier")
	ErrVerifierUnavailable     = errors.New("zk: verifier unavailable")
	ErrVerificationFailed      = errors.New("zk: verification failed")
	ErrUnsupportedProof        = errors.New("zk: unsupported proof system")
	ErrUnsupportedCircuit      = errors.New("zk: unsupported circuit")
	ErrUnsupportedCurve        = errors.New("zk: unsupported curve")
	ErrUnsupportedVersion      = errors.New("zk: unsupported version")
	ErrPublicInputHashMismatch = errors.New("zk: public input hash mismatch")
)

type ProofSystem uint16

const (
	ProofSystemGroth16BN254 ProofSystem = iota + 1
	ProofSystemPlonkKZG
	ProofSystemStarkFRI
	ProofSystemSchnorrP256
)

type CurveID uint16

const (
	CurveBN254 CurveID = iota + 1
	CurveBLS12381
	CurveP256
)

type CircuitID uint16

const (
	CircuitPrivacyDeposit CircuitID = iota + 1
	CircuitPrivacyWithdraw
	CircuitPrivacyTransfer
	CircuitPrivacyAudit
)

// Digest 表示固定长度摘要 + 用数组避免切片长度不确定。
type Digest [HashSize]byte

// ProofTarget 描述推荐证明目标 + 将性能选择和安全边界显式固化。
type ProofTarget struct {
	System                  ProofSystem
	Curve                   CurveID
	Version                 uint16
	MaxProofBytes           int
	MaxPublicInputBytes     int
	TrustedSetupRequired    bool
	PerCircuitSetupRequired bool
}

// PublicInputSet 描述公开输入集合 + 用长度前缀哈希避免拼接歧义。
type PublicInputSet struct {
	Values [][]byte
}

// ProofEnvelope 描述证明信封 + 将 proof 与电路、曲线、验证密钥和输入哈希绑定。
type ProofEnvelope struct {
	Version          uint16
	System           ProofSystem
	Curve            CurveID
	Circuit          CircuitID
	VerifyingKeyHash Digest
	PublicInputHash  Digest
	Proof            []byte
}

// ProofEnvelopeParams 描述创建证明信封参数 + 避免构造函数参数顺序误用。
type ProofEnvelopeParams struct {
	Version          uint16
	System           ProofSystem
	Curve            CurveID
	Circuit          CircuitID
	VerifyingKeyHash Digest
	PublicInputs     PublicInputSet
	Proof            []byte
}

// VerificationRequest 描述验证请求 + 同时携带信封和原始公开输入做一致性检查。
type VerificationRequest struct {
	Envelope     ProofEnvelope
	PublicInputs PublicInputSet
	Context      []byte
}

// VerificationResult 描述验证结果 + 统一链上执行器和测试断言读取。
type VerificationResult struct {
	Valid   bool
	System  ProofSystem
	Curve   CurveID
	Circuit CircuitID
	Message string
}

// Verifier 验证零知识证明 + 未来 Groth16/Plonk/STARK 实现都适配该接口。
type Verifier interface {
	System() ProofSystem
	Verify(request VerificationRequest) (VerificationResult, error)
}

// Prover 生成零知识证明 + 钱包或链下证明服务按该接口接入。
type Prover interface {
	System() ProofSystem
	Prove(request ProvingRequest) (ProofEnvelope, error)
}

// ProvingRequest 描述证明请求 + 只放抽象字节避免绑定具体电路库。
type ProvingRequest struct {
	Circuit          CircuitID
	Curve            CurveID
	VerifyingKeyHash Digest
	PublicInputs     PublicInputSet
	Witness          []byte
	ProvingKey       []byte
}

// VerifierRegistry 保存 verifier 集合 + 初始化后只读保证并发安全。
type VerifierRegistry struct {
	verifiers map[ProofSystem]Verifier
}

// RejectVerifier 默认拒绝 verifier + 防止未接入真实 ZK 时误验通过。
type RejectVerifier struct {
	ProofSystem ProofSystem
}

// RecommendedPrivacyProofTarget 返回隐私交易推荐目标 + 当前优先链上验证速度和成熟度。
func RecommendedPrivacyProofTarget() ProofTarget {
	return ProofTarget{
		System:                  ProofSystemGroth16BN254,
		Curve:                   CurveBN254,
		Version:                 ProtocolVersion,
		MaxProofBytes:           DefaultMaxProofBytes,
		MaxPublicInputBytes:     MaxPublicInputBytes,
		TrustedSetupRequired:    true,
		PerCircuitSetupRequired: true,
	}
}

// NewDigest 创建摘要对象 + 校验输入必须为固定 32 字节。
func NewDigest(value []byte) (Digest, error) {
	var digest Digest
	if len(value) != HashSize {
		return digest, fmt.Errorf("%w: digest requires %d bytes, got %d", ErrInvalidPublicInput, HashSize, len(value))
	}
	copy(digest[:], value)
	return digest, nil
}

// HashBytes 计算单段字节摘要 + 统一公开输入和验证密钥哈希格式。
func HashBytes(value []byte) Digest {
	return sha256.Sum256(value)
}

// Bytes 返回摘要拷贝 + 防止外部修改内部数组。
func (digest Digest) Bytes() []byte {
	cloned := make([]byte, HashSize)
	copy(cloned, digest[:])
	return cloned
}

// IsZero 判断摘要是否为空 + 防止关键绑定字段缺失。
func (digest Digest) IsZero() bool {
	return digest == Digest{}
}

// NewPublicInputSet 创建公开输入集合 + 复制切片避免调用方签名后修改。
func NewPublicInputSet(values [][]byte) (PublicInputSet, error) {
	inputs := PublicInputSet{Values: cloneByteSlices(values)}
	return inputs, inputs.Validate()
}

// Validate 校验公开输入集合 + 限制数量和总字节防止资源滥用。
func (inputs PublicInputSet) Validate() error {
	if len(inputs.Values) == 0 {
		return fmt.Errorf("%w: public inputs cannot be empty", ErrInvalidPublicInput)
	}
	if len(inputs.Values) > MaxPublicInputCount {
		return fmt.Errorf("%w: input count %d exceeds %d", ErrInvalidPublicInput, len(inputs.Values), MaxPublicInputCount)
	}
	totalBytes := 0
	for inputIndex, value := range inputs.Values {
		if len(value) == 0 {
			return fmt.Errorf("%w: input %d is empty", ErrInvalidPublicInput, inputIndex)
		}
		totalBytes += len(value)
		if totalBytes > MaxPublicInputBytes {
			return fmt.Errorf("%w: total bytes %d exceeds %d", ErrInvalidPublicInput, totalBytes, MaxPublicInputBytes)
		}
	}
	return nil
}

// Hash 计算公开输入哈希 + 使用域隔离和长度前缀避免二义性。
func (inputs PublicInputSet) Hash() (Digest, error) {
	if err := inputs.Validate(); err != nil {
		return Digest{}, err
	}
	buffer := bytes.Buffer{}
	writeLengthPrefixedBytes(&buffer, []byte(domainPublicInputsV1))
	writeUint16(&buffer, uint16(len(inputs.Values)))
	for _, value := range inputs.Values {
		writeLengthPrefixedBytes(&buffer, value)
	}
	return HashBytes(buffer.Bytes()), nil
}

// Clone 深拷贝公开输入集合 + 防止 verifier 间共享底层切片。
func (inputs PublicInputSet) Clone() PublicInputSet {
	return PublicInputSet{Values: cloneByteSlices(inputs.Values)}
}

// NewProofEnvelope 创建证明信封 + 自动绑定公开输入哈希。
func NewProofEnvelope(params ProofEnvelopeParams) (ProofEnvelope, error) {
	version := params.Version
	if version == 0 {
		version = ProtocolVersion
	}
	publicInputHash, err := params.PublicInputs.Hash()
	if err != nil {
		return ProofEnvelope{}, err
	}
	envelope := ProofEnvelope{
		Version:          version,
		System:           params.System,
		Curve:            params.Curve,
		Circuit:          params.Circuit,
		VerifyingKeyHash: params.VerifyingKeyHash,
		PublicInputHash:  publicInputHash,
		Proof:            cloneBytes(params.Proof),
	}
	return envelope, envelope.Validate()
}

// Validate 校验证明信封 + 防止错误 proof 进入交易执行层。
func (envelope ProofEnvelope) Validate() error {
	if err := ValidateProtocolVersion(envelope.Version); err != nil {
		return err
	}
	if !envelope.System.IsSupported() {
		return fmt.Errorf("%w: system %d", ErrUnsupportedProof, envelope.System)
	}
	if !envelope.Curve.IsSupported() {
		return fmt.Errorf("%w: curve %d", ErrUnsupportedCurve, envelope.Curve)
	}
	if !envelope.Circuit.IsSupported() {
		return fmt.Errorf("%w: circuit %d", ErrUnsupportedCircuit, envelope.Circuit)
	}
	if envelope.VerifyingKeyHash.IsZero() {
		return fmt.Errorf("%w: verifying key hash is empty", ErrInvalidProof)
	}
	if envelope.PublicInputHash.IsZero() {
		return fmt.Errorf("%w: public input hash is empty", ErrInvalidProof)
	}
	return ValidateOptionalProofBytes(envelope.Proof, DefaultMaxProofBytes)
}

// CanonicalHash 计算证明信封哈希 + 用于签名和缓存索引。
func (envelope ProofEnvelope) CanonicalHash() (Digest, error) {
	if err := envelope.Validate(); err != nil {
		return Digest{}, err
	}
	buffer := bytes.Buffer{}
	writeLengthPrefixedBytes(&buffer, []byte(domainProofEnvelopeV1))
	writeUint16(&buffer, envelope.Version)
	writeUint16(&buffer, uint16(envelope.System))
	writeUint16(&buffer, uint16(envelope.Curve))
	writeUint16(&buffer, uint16(envelope.Circuit))
	buffer.Write(envelope.VerifyingKeyHash[:])
	buffer.Write(envelope.PublicInputHash[:])
	writeLengthPrefixedBytes(&buffer, envelope.Proof)
	return HashBytes(buffer.Bytes()), nil
}

// Clone 深拷贝证明信封 + 防止 proof 字节被外部修改。
func (envelope ProofEnvelope) Clone() ProofEnvelope {
	return ProofEnvelope{
		Version:          envelope.Version,
		System:           envelope.System,
		Curve:            envelope.Curve,
		Circuit:          envelope.Circuit,
		VerifyingKeyHash: envelope.VerifyingKeyHash,
		PublicInputHash:  envelope.PublicInputHash,
		Proof:            cloneBytes(envelope.Proof),
	}
}

// Validate 校验验证请求 + 确保公开输入和信封哈希一致。
func (request VerificationRequest) Validate() error {
	if err := request.Envelope.Validate(); err != nil {
		return err
	}
	publicInputHash, err := request.PublicInputs.Hash()
	if err != nil {
		return err
	}
	if publicInputHash != request.Envelope.PublicInputHash {
		return ErrPublicInputHashMismatch
	}
	return nil
}

// NewVerifierRegistry 创建 verifier 注册表 + 拒绝重复或非法 verifier。
func NewVerifierRegistry(verifiers ...Verifier) (VerifierRegistry, error) {
	registry := VerifierRegistry{verifiers: make(map[ProofSystem]Verifier, len(verifiers))}
	for _, verifier := range verifiers {
		if verifier == nil {
			return VerifierRegistry{}, fmt.Errorf("%w: verifier is nil", ErrInvalidVerifier)
		}
		system := verifier.System()
		if !system.IsSupported() {
			return VerifierRegistry{}, fmt.Errorf("%w: verifier system %d", ErrUnsupportedProof, system)
		}
		if _, exists := registry.verifiers[system]; exists {
			return VerifierRegistry{}, fmt.Errorf("%w: duplicate verifier for system %d", ErrInvalidVerifier, system)
		}
		registry.verifiers[system] = verifier
	}
	return registry, nil
}

// Verify 分发证明验证 + 未注册系统默认安全拒绝。
func (registry VerifierRegistry) Verify(request VerificationRequest) (VerificationResult, error) {
	if err := request.Validate(); err != nil {
		return failedVerification(request.Envelope, err.Error()), err
	}
	verifier, exists := registry.verifiers[request.Envelope.System]
	if !exists {
		return failedVerification(request.Envelope, ErrVerifierUnavailable.Error()), ErrVerifierUnavailable
	}
	return verifier.Verify(request)
}

// System 返回拒绝 verifier 的证明系统 + 供注册表索引。
func (verifier RejectVerifier) System() ProofSystem {
	return verifier.ProofSystem
}

// Verify 拒绝所有证明 + 未接入真实密码学库前保持安全失败。
func (verifier RejectVerifier) Verify(request VerificationRequest) (VerificationResult, error) {
	if err := request.Validate(); err != nil {
		return failedVerification(request.Envelope, err.Error()), err
	}
	return failedVerification(request.Envelope, ErrVerifierUnavailable.Error()), ErrVerifierUnavailable
}

// ValidateOptionalProofBytes 校验证明字节 + 允许固定指令阶段空 proof 但限制上限。
func ValidateOptionalProofBytes(proof []byte, maxBytes int) error {
	if maxBytes <= 0 {
		return fmt.Errorf("%w: max proof bytes must be positive", ErrInvalidProof)
	}
	if len(proof) > maxBytes {
		return fmt.Errorf("%w: proof length %d exceeds %d", ErrInvalidProof, len(proof), maxBytes)
	}
	return nil
}

// ValidateProtocolVersion 校验协议版本 + 防止不同版本证明格式混用。
func ValidateProtocolVersion(version uint16) error {
	if version != ProtocolVersion {
		return fmt.Errorf("%w: version %d", ErrUnsupportedVersion, version)
	}
	return nil
}

// IsSupported 判断证明系统是否支持 + 固定枚举避免未知系统进入执行层。
func (system ProofSystem) IsSupported() bool {
	return system == ProofSystemGroth16BN254 ||
		system == ProofSystemPlonkKZG ||
		system == ProofSystemStarkFRI ||
		system == ProofSystemSchnorrP256
}

// IsSupported 判断曲线是否支持 + 固定枚举避免未知曲线进入执行层。
func (curve CurveID) IsSupported() bool {
	return curve == CurveBN254 || curve == CurveBLS12381 || curve == CurveP256
}

// IsSupported 判断电路是否支持 + 固定枚举避免未知电路进入执行层。
func (circuit CircuitID) IsSupported() bool {
	return circuit == CircuitPrivacyDeposit ||
		circuit == CircuitPrivacyWithdraw ||
		circuit == CircuitPrivacyTransfer ||
		circuit == CircuitPrivacyAudit
}

func failedVerification(envelope ProofEnvelope, message string) VerificationResult {
	return VerificationResult{
		Valid:   false,
		System:  envelope.System,
		Curve:   envelope.Curve,
		Circuit: envelope.Circuit,
		Message: message,
	}
}

func writeUint16(buffer *bytes.Buffer, value uint16) {
	var encoded [2]byte
	binary.LittleEndian.PutUint16(encoded[:], value)
	buffer.Write(encoded[:])
}

func writeLengthPrefixedBytes(buffer *bytes.Buffer, value []byte) {
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], uint32(len(value)))
	buffer.Write(encoded[:])
	buffer.Write(value)
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}

func cloneByteSlices(values [][]byte) [][]byte {
	if values == nil {
		return nil
	}
	cloned := make([][]byte, len(values))
	for index, value := range values {
		cloned[index] = cloneBytes(value)
	}
	return cloned
}
