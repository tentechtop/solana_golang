package zk

import (
	"bytes"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"math/big"
)

const (
	SchnorrProofVersion      = uint16(1)
	SchnorrScalarSize        = 32
	SchnorrCompressedKeySize = 33
	SchnorrProofMaxBytes     = 256
	domainSchnorrChallengeV1 = "solana_golang.zk.schnorr_p256.challenge.v1"
	domainSchnorrProofV1     = "solana_golang.zk.schnorr_p256.proof.v1"
)

// SchnorrKeyPair 描述 P-256 Schnorr 密钥对 + 用于真实可运行的轻量 NIZK 授权。
type SchnorrKeyPair struct {
	PrivateScalar []byte
	PublicKey     []byte
}

// SchnorrProof 描述 Schnorr 非交互证明 + 证明知道 PublicKey 对应私钥。
type SchnorrProof struct {
	Version         uint16
	Curve           CurveID
	PublicKey       []byte
	NonceCommitment []byte
	Response        []byte
}

// GenerateSchnorrKeyPair 生成 Schnorr 密钥对 + 使用标准库密码学随机源。
func GenerateSchnorrKeyPair() (SchnorrKeyPair, error) {
	curve := elliptic.P256()
	privateScalar, err := randomScalar(rand.Reader, curve.Params().N)
	if err != nil {
		return SchnorrKeyPair{}, err
	}
	publicX, publicY := curve.ScalarBaseMult(padScalar(privateScalar))
	return SchnorrKeyPair{
		PrivateScalar: padScalar(privateScalar),
		PublicKey:     elliptic.MarshalCompressed(curve, publicX, publicY),
	}, nil
}

// SchnorrPublicKeyDigest 计算证明公钥摘要 + 可作为 32 字节 SpendAuthority 存储。
func SchnorrPublicKeyDigest(publicKey []byte) (Digest, error) {
	if err := validateCompressedP256Point(publicKey, "schnorr public key"); err != nil {
		return Digest{}, err
	}
	return HashBytes(publicKey), nil
}

// NewSchnorrProof 生成 Schnorr NIZK 证明 + 使用 Fiat-Shamir 将证明绑定到消息。
func NewSchnorrProof(privateScalar []byte, message []byte) (SchnorrProof, error) {
	curve := elliptic.P256()
	privateValue, err := normalizePrivateScalar(privateScalar, curve.Params().N)
	if err != nil {
		return SchnorrProof{}, err
	}
	publicX, publicY := curve.ScalarBaseMult(padScalar(privateValue))
	publicKey := elliptic.MarshalCompressed(curve, publicX, publicY)

	nonceScalar, err := randomScalar(rand.Reader, curve.Params().N)
	if err != nil {
		return SchnorrProof{}, err
	}
	nonceX, nonceY := curve.ScalarBaseMult(padScalar(nonceScalar))
	nonceCommitment := elliptic.MarshalCompressed(curve, nonceX, nonceY)
	challenge := schnorrChallenge(publicKey, nonceCommitment, message, curve.Params().N)

	response := new(big.Int).Mul(challenge, privateValue)
	response.Add(response, nonceScalar)
	response.Mod(response, curve.Params().N)

	proof := SchnorrProof{
		Version:         SchnorrProofVersion,
		Curve:           CurveP256,
		PublicKey:       publicKey,
		NonceCommitment: nonceCommitment,
		Response:        padScalar(response),
	}
	return proof, proof.Validate()
}

// NewSchnorrProofBytes 生成序列化证明 + 便于交易直接携带 proof 字节。
func NewSchnorrProofBytes(privateScalar []byte, message []byte) ([]byte, error) {
	proof, err := NewSchnorrProof(privateScalar, message)
	if err != nil {
		return nil, err
	}
	return proof.MarshalBinary()
}

// VerifySchnorrProofBytes 验证序列化证明 + 同时绑定期望的 32 字节公钥摘要。
func VerifySchnorrProofBytes(proofBytes []byte, message []byte, expectedPublicKeyDigest Digest) error {
	if len(proofBytes) == 0 {
		return fmt.Errorf("%w: schnorr proof is empty", ErrInvalidProof)
	}
	if expectedPublicKeyDigest.IsZero() {
		return fmt.Errorf("%w: schnorr public key digest is empty", ErrInvalidPublicInput)
	}
	proof, err := UnmarshalSchnorrProofBinary(proofBytes)
	if err != nil {
		return err
	}
	actualDigest, err := SchnorrPublicKeyDigest(proof.PublicKey)
	if err != nil {
		return err
	}
	if actualDigest != expectedPublicKeyDigest {
		return fmt.Errorf("%w: schnorr public key digest mismatch", ErrVerificationFailed)
	}
	return proof.Verify(message)
}

// Verify 验证 Schnorr NIZK 证明 + 只证明私钥知识不泄露私钥。
func (proof SchnorrProof) Verify(message []byte) error {
	if err := proof.Validate(); err != nil {
		return err
	}
	curve := elliptic.P256()
	order := curve.Params().N
	publicX, publicY := elliptic.UnmarshalCompressed(curve, proof.PublicKey)
	if publicX == nil {
		return fmt.Errorf("%w: invalid schnorr public key", ErrInvalidProof)
	}
	nonceX, nonceY := elliptic.UnmarshalCompressed(curve, proof.NonceCommitment)
	if nonceX == nil {
		return fmt.Errorf("%w: invalid schnorr nonce commitment", ErrInvalidProof)
	}

	response := new(big.Int).SetBytes(proof.Response)
	if response.Sign() <= 0 || response.Cmp(order) >= 0 {
		return fmt.Errorf("%w: schnorr response out of range", ErrInvalidProof)
	}
	challenge := schnorrChallenge(proof.PublicKey, proof.NonceCommitment, message, order)

	leftX, leftY := curve.ScalarBaseMult(padScalar(response))
	challengePublicX, challengePublicY := curve.ScalarMult(publicX, publicY, padScalar(challenge))
	rightX, rightY := curve.Add(nonceX, nonceY, challengePublicX, challengePublicY)
	if rightX == nil || leftX.Cmp(rightX) != 0 || leftY.Cmp(rightY) != 0 {
		return ErrVerificationFailed
	}
	return nil
}

// Validate 校验 Schnorr 证明结构 + 防止畸形点和越界标量进入验证。
func (proof SchnorrProof) Validate() error {
	if proof.Version != SchnorrProofVersion {
		return fmt.Errorf("%w: schnorr version %d", ErrUnsupportedVersion, proof.Version)
	}
	if proof.Curve != CurveP256 {
		return fmt.Errorf("%w: schnorr curve %d", ErrUnsupportedCurve, proof.Curve)
	}
	if err := validateCompressedP256Point(proof.PublicKey, "schnorr public key"); err != nil {
		return err
	}
	if err := validateCompressedP256Point(proof.NonceCommitment, "schnorr nonce commitment"); err != nil {
		return err
	}
	if len(proof.Response) != SchnorrScalarSize {
		return fmt.Errorf("%w: schnorr response requires %d bytes, got %d", ErrInvalidProof, SchnorrScalarSize, len(proof.Response))
	}
	response := new(big.Int).SetBytes(proof.Response)
	if response.Sign() <= 0 || response.Cmp(elliptic.P256().Params().N) >= 0 {
		return fmt.Errorf("%w: schnorr response out of range", ErrInvalidProof)
	}
	return nil
}

// MarshalBinary 序列化 Schnorr 证明 + 使用固定长度字段便于交易携带。
func (proof SchnorrProof) MarshalBinary() ([]byte, error) {
	if err := proof.Validate(); err != nil {
		return nil, err
	}
	buffer := bytes.Buffer{}
	writeLengthPrefixedBytes(&buffer, []byte(domainSchnorrProofV1))
	writeUint16(&buffer, proof.Version)
	writeUint16(&buffer, uint16(proof.Curve))
	buffer.Write(proof.PublicKey)
	buffer.Write(proof.NonceCommitment)
	buffer.Write(proof.Response)
	return buffer.Bytes(), nil
}

// UnmarshalSchnorrProofBinary 反序列化 Schnorr 证明 + 拒绝尾部污染字节。
func UnmarshalSchnorrProofBinary(data []byte) (SchnorrProof, error) {
	if len(data) > SchnorrProofMaxBytes {
		return SchnorrProof{}, fmt.Errorf("%w: schnorr proof length %d exceeds %d", ErrInvalidProof, len(data), SchnorrProofMaxBytes)
	}
	reader := bytes.NewReader(data)
	domain, err := readLengthPrefixedBytes(reader, len(domainSchnorrProofV1))
	if err != nil {
		return SchnorrProof{}, err
	}
	if string(domain) != domainSchnorrProofV1 {
		return SchnorrProof{}, fmt.Errorf("%w: invalid schnorr proof domain", ErrInvalidProof)
	}
	version, err := readUint16(reader)
	if err != nil {
		return SchnorrProof{}, err
	}
	curveValue, err := readUint16(reader)
	if err != nil {
		return SchnorrProof{}, err
	}
	publicKey, err := readFixedBytes(reader, SchnorrCompressedKeySize)
	if err != nil {
		return SchnorrProof{}, err
	}
	nonceCommitment, err := readFixedBytes(reader, SchnorrCompressedKeySize)
	if err != nil {
		return SchnorrProof{}, err
	}
	response, err := readFixedBytes(reader, SchnorrScalarSize)
	if err != nil {
		return SchnorrProof{}, err
	}
	if reader.Len() != 0 {
		return SchnorrProof{}, fmt.Errorf("%w: schnorr proof has %d trailing bytes", ErrInvalidProof, reader.Len())
	}
	proof := SchnorrProof{
		Version:         version,
		Curve:           CurveID(curveValue),
		PublicKey:       publicKey,
		NonceCommitment: nonceCommitment,
		Response:        response,
	}
	return proof, proof.Validate()
}

func schnorrChallenge(publicKey []byte, nonceCommitment []byte, message []byte, order *big.Int) *big.Int {
	buffer := bytes.Buffer{}
	writeLengthPrefixedBytes(&buffer, []byte(domainSchnorrChallengeV1))
	writeLengthPrefixedBytes(&buffer, publicKey)
	writeLengthPrefixedBytes(&buffer, nonceCommitment)
	writeLengthPrefixedBytes(&buffer, message)
	sum := sha256.Sum256(buffer.Bytes())
	challenge := new(big.Int).SetBytes(sum[:])
	challenge.Mod(challenge, order)
	return challenge
}

func randomScalar(reader io.Reader, order *big.Int) (*big.Int, error) {
	for {
		value, err := rand.Int(reader, order)
		if err != nil {
			return nil, fmt.Errorf("zk: generate schnorr scalar: %w", err)
		}
		if value.Sign() > 0 {
			return value, nil
		}
	}
}

func normalizePrivateScalar(privateScalar []byte, order *big.Int) (*big.Int, error) {
	if len(privateScalar) != SchnorrScalarSize {
		return nil, fmt.Errorf("%w: private scalar requires %d bytes, got %d", ErrInvalidProof, SchnorrScalarSize, len(privateScalar))
	}
	value := new(big.Int).SetBytes(privateScalar)
	if value.Sign() <= 0 || value.Cmp(order) >= 0 {
		return nil, fmt.Errorf("%w: private scalar out of range", ErrInvalidProof)
	}
	return value, nil
}

func validateCompressedP256Point(value []byte, field string) error {
	if len(value) != SchnorrCompressedKeySize {
		return fmt.Errorf("%w: %s requires %d bytes, got %d", ErrInvalidProof, field, SchnorrCompressedKeySize, len(value))
	}
	x, _ := elliptic.UnmarshalCompressed(elliptic.P256(), value)
	if x == nil {
		return fmt.Errorf("%w: invalid %s", ErrInvalidProof, field)
	}
	return nil
}

func padScalar(value *big.Int) []byte {
	encoded := value.Bytes()
	if len(encoded) >= SchnorrScalarSize {
		return encoded[len(encoded)-SchnorrScalarSize:]
	}
	padded := make([]byte, SchnorrScalarSize)
	copy(padded[SchnorrScalarSize-len(encoded):], encoded)
	return padded
}

func readUint16(reader *bytes.Reader) (uint16, error) {
	var encoded [2]byte
	if _, err := io.ReadFull(reader, encoded[:]); err != nil {
		return 0, fmt.Errorf("%w: read u16: %w", ErrInvalidProof, err)
	}
	return binary.LittleEndian.Uint16(encoded[:]), nil
}

func readLengthPrefixedBytes(reader *bytes.Reader, maxLength int) ([]byte, error) {
	var lengthBytes [4]byte
	if _, err := io.ReadFull(reader, lengthBytes[:]); err != nil {
		return nil, fmt.Errorf("%w: read bytes length: %w", ErrInvalidProof, err)
	}
	length := binary.LittleEndian.Uint32(lengthBytes[:])
	if length > uint32(maxLength) {
		return nil, fmt.Errorf("%w: bytes length %d exceeds %d", ErrInvalidProof, length, maxLength)
	}
	return readFixedBytes(reader, int(length))
}

func readFixedBytes(reader *bytes.Reader, length int) ([]byte, error) {
	if length < 0 || length > reader.Len() {
		return nil, fmt.Errorf("%w: need %d bytes, remaining %d", ErrInvalidProof, length, reader.Len())
	}
	value := make([]byte, length)
	if _, err := io.ReadFull(reader, value); err != nil {
		return nil, fmt.Errorf("%w: read bytes: %w", ErrInvalidProof, err)
	}
	return value, nil
}
