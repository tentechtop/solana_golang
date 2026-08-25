package zk

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"

	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark/backend/groth16"
	"github.com/consensys/gnark/constraint"
)

const (
	shieldedParametersEncodingVersion = uint16(1)
	maxShieldedParameterSectionBytes  = uint64(1 << 30)
)

var shieldedParametersMagic = [8]byte{'S', 'H', 'L', 'D', 'P', 'A', 'R', '1'}

// ShieldedCircuitParameters 保存固定电路的 R1CS、证明密钥和验证密钥。
// 生产环境必须通过多方仪式生成参数，Setup 仅用于开发和测试。
type ShieldedCircuitParameters struct {
	ConstraintSystem constraint.ConstraintSystem
	ProvingKey       groth16.ProvingKey
	VerifyingKey     groth16.VerifyingKey
}

// SetupShieldedCircuit 为已编译电路执行 Groth16 setup。
func SetupShieldedCircuit(compiled constraint.ConstraintSystem) (*ShieldedCircuitParameters, error) {
	if compiled == nil {
		return nil, fmt.Errorf("%w: constraint system is nil", ErrInvalidShieldedParameters)
	}
	if compiled.Field().Cmp(ecc.BN254.ScalarField()) != 0 {
		return nil, fmt.Errorf("%w: constraint system is not BN254", ErrInvalidShieldedParameters)
	}
	provingKey, verifyingKey, err := groth16.Setup(compiled)
	if err != nil {
		return nil, fmt.Errorf("setup shielded circuit: %w", err)
	}
	return &ShieldedCircuitParameters{
		ConstraintSystem: compiled,
		ProvingKey:       provingKey,
		VerifyingKey:     verifyingKey,
	}, nil
}

// GenerateShieldedCircuitParameters 编译电路并执行开发用 setup。
func GenerateShieldedCircuitParameters() (*ShieldedCircuitParameters, error) {
	compiled, err := CompileShieldedCircuit()
	if err != nil {
		return nil, err
	}
	return SetupShieldedCircuit(compiled)
}

// Prove 使用当前参数生成证明。
func (parameters *ShieldedCircuitParameters) Prove(assignment *ShieldedCircuit) ([]byte, error) {
	return ProveShieldedCircuit(parameters, assignment)
}

// Verify 使用当前验证密钥验证证明。
func (parameters *ShieldedCircuitParameters) Verify(publicAssignment *ShieldedCircuit, proof []byte) error {
	if parameters == nil {
		return fmt.Errorf("%w: parameters are nil", ErrInvalidShieldedParameters)
	}
	return VerifyShieldedCircuit(parameters.VerifyingKey, publicAssignment, proof)
}

// VerifyingKeyHash 返回规范验证密钥序列化结果的 SHA-256。
func (parameters *ShieldedCircuitParameters) VerifyingKeyHash() ([sha256.Size]byte, error) {
	var emptyHash [sha256.Size]byte
	if parameters == nil || parameters.VerifyingKey == nil {
		return emptyHash, fmt.Errorf("%w: verifying key is nil", ErrInvalidShieldedParameters)
	}
	return ShieldedVerifyingKeyHash(parameters.VerifyingKey)
}

// ShieldedVerifyingKeyHash 返回单独验证密钥的规范 SHA-256 摘要。
func ShieldedVerifyingKeyHash(verifyingKey groth16.VerifyingKey) ([sha256.Size]byte, error) {
	var emptyHash [sha256.Size]byte
	if verifyingKey == nil {
		return emptyHash, fmt.Errorf("%w: verifying key is nil", ErrInvalidShieldedParameters)
	}
	encodedKey, err := writeShieldedParameterSection(verifyingKey)
	if err != nil {
		return emptyHash, fmt.Errorf("serialize shielded verifying key: %w", err)
	}
	return sha256.Sum256(encodedKey), nil
}

// MarshalBinary 将完整参数编码为带版本、布局和校验和的二进制信封。
func (parameters *ShieldedCircuitParameters) MarshalBinary() ([]byte, error) {
	if err := parameters.validateForProving(); err != nil {
		return nil, err
	}
	constraintSystemBytes, err := writeShieldedParameterSection(parameters.ConstraintSystem)
	if err != nil {
		return nil, fmt.Errorf("serialize shielded constraint system: %w", err)
	}
	provingKeyBytes, err := writeShieldedParameterSection(parameters.ProvingKey)
	if err != nil {
		return nil, fmt.Errorf("serialize shielded proving key: %w", err)
	}
	verifyingKeyBytes, err := writeShieldedParameterSection(parameters.VerifyingKey)
	if err != nil {
		return nil, fmt.Errorf("serialize shielded verifying key: %w", err)
	}

	buffer := bytes.Buffer{}
	buffer.Write(shieldedParametersMagic[:])
	if err := binary.Write(&buffer, binary.BigEndian, shieldedParametersEncodingVersion); err != nil {
		return nil, err
	}
	if err := binary.Write(&buffer, binary.BigEndian, uint16(ShieldedActionCount)); err != nil {
		return nil, err
	}
	if err := binary.Write(&buffer, binary.BigEndian, uint16(ShieldedMerkleDepth)); err != nil {
		return nil, err
	}
	if err := binary.Write(&buffer, binary.BigEndian, uint16(0)); err != nil {
		return nil, err
	}
	for _, section := range [][]byte{constraintSystemBytes, provingKeyBytes, verifyingKeyBytes} {
		if err := binary.Write(&buffer, binary.BigEndian, uint64(len(section))); err != nil {
			return nil, err
		}
		if _, err := buffer.Write(section); err != nil {
			return nil, err
		}
	}
	checksum := sha256.Sum256(buffer.Bytes())
	buffer.Write(checksum[:])
	return buffer.Bytes(), nil
}

// UnmarshalShieldedCircuitParameters 解码并校验完整参数。
func UnmarshalShieldedCircuitParameters(encoded []byte) (*ShieldedCircuitParameters, error) {
	parameters := &ShieldedCircuitParameters{}
	if err := parameters.UnmarshalBinary(encoded); err != nil {
		return nil, err
	}
	return parameters, nil
}

// UnmarshalBinary 解码并替换当前参数。
func (parameters *ShieldedCircuitParameters) UnmarshalBinary(encoded []byte) error {
	if parameters == nil {
		return fmt.Errorf("%w: destination is nil", ErrInvalidShieldedParameters)
	}
	minimumLength := len(shieldedParametersMagic) + 8 + 3*8 + sha256.Size
	if len(encoded) < minimumLength {
		return fmt.Errorf("%w: encoding is truncated", ErrInvalidShieldedParameters)
	}
	payloadLength := len(encoded) - sha256.Size
	expectedChecksum := sha256.Sum256(encoded[:payloadLength])
	if !bytes.Equal(expectedChecksum[:], encoded[payloadLength:]) {
		return fmt.Errorf("%w: checksum mismatch", ErrInvalidShieldedParameters)
	}

	reader := bytes.NewReader(encoded[:payloadLength])
	var magic [8]byte
	if _, err := io.ReadFull(reader, magic[:]); err != nil {
		return fmt.Errorf("%w: read magic: %v", ErrInvalidShieldedParameters, err)
	}
	if magic != shieldedParametersMagic {
		return fmt.Errorf("%w: unexpected magic", ErrInvalidShieldedParameters)
	}
	var version, actionCount, merkleDepth, reserved uint16
	for _, destination := range []*uint16{&version, &actionCount, &merkleDepth, &reserved} {
		if err := binary.Read(reader, binary.BigEndian, destination); err != nil {
			return fmt.Errorf("%w: read header: %v", ErrInvalidShieldedParameters, err)
		}
	}
	if version != shieldedParametersEncodingVersion {
		return fmt.Errorf("%w: unsupported encoding version %d", ErrInvalidShieldedParameters, version)
	}
	if actionCount != ShieldedActionCount || merkleDepth != ShieldedMerkleDepth || reserved != 0 {
		return fmt.Errorf("%w: incompatible circuit layout", ErrInvalidShieldedParameters)
	}

	sections := make([][]byte, 3)
	for sectionIndex := range sections {
		section, err := readShieldedParameterSection(reader)
		if err != nil {
			return fmt.Errorf("%w: read section %d: %v", ErrInvalidShieldedParameters, sectionIndex, err)
		}
		sections[sectionIndex] = section
	}
	if reader.Len() != 0 {
		return fmt.Errorf("%w: trailing payload bytes", ErrInvalidShieldedParameters)
	}

	constraintSystem := groth16.NewCS(ecc.BN254)
	if err := readShieldedSerializableSection(constraintSystem, sections[0]); err != nil {
		return fmt.Errorf("%w: decode constraint system: %v", ErrInvalidShieldedParameters, err)
	}
	provingKey := groth16.NewProvingKey(ecc.BN254)
	if err := readShieldedSerializableSection(provingKey, sections[1]); err != nil {
		return fmt.Errorf("%w: decode proving key: %v", ErrInvalidShieldedParameters, err)
	}
	verifyingKey := groth16.NewVerifyingKey(ecc.BN254)
	if err := readShieldedSerializableSection(verifyingKey, sections[2]); err != nil {
		return fmt.Errorf("%w: decode verifying key: %v", ErrInvalidShieldedParameters, err)
	}

	decoded := &ShieldedCircuitParameters{
		ConstraintSystem: constraintSystem,
		ProvingKey:       provingKey,
		VerifyingKey:     verifyingKey,
	}
	if err := decoded.validateForProving(); err != nil {
		return err
	}
	*parameters = *decoded
	return nil
}

func (parameters *ShieldedCircuitParameters) validateForProving() error {
	if parameters == nil {
		return fmt.Errorf("%w: parameters are nil", ErrInvalidShieldedParameters)
	}
	if parameters.ConstraintSystem == nil {
		return fmt.Errorf("%w: constraint system is nil", ErrInvalidShieldedParameters)
	}
	if parameters.ProvingKey == nil {
		return fmt.Errorf("%w: proving key is nil", ErrInvalidShieldedParameters)
	}
	if parameters.VerifyingKey == nil {
		return fmt.Errorf("%w: verifying key is nil", ErrInvalidShieldedParameters)
	}
	if parameters.ConstraintSystem.Field().Cmp(ecc.BN254.ScalarField()) != 0 ||
		parameters.ProvingKey.CurveID() != ecc.BN254 ||
		parameters.VerifyingKey.CurveID() != ecc.BN254 {
		return fmt.Errorf("%w: parameters are not BN254", ErrInvalidShieldedParameters)
	}
	return nil
}

func writeShieldedParameterSection(value io.WriterTo) ([]byte, error) {
	buffer := bytes.Buffer{}
	if _, err := value.WriteTo(&buffer); err != nil {
		return nil, err
	}
	if uint64(buffer.Len()) > maxShieldedParameterSectionBytes {
		return nil, fmt.Errorf("parameter section exceeds %d bytes", maxShieldedParameterSectionBytes)
	}
	return buffer.Bytes(), nil
}

func readShieldedParameterSection(reader *bytes.Reader) ([]byte, error) {
	var sectionLength uint64
	if err := binary.Read(reader, binary.BigEndian, &sectionLength); err != nil {
		return nil, err
	}
	if sectionLength == 0 || sectionLength > maxShieldedParameterSectionBytes {
		return nil, fmt.Errorf("invalid section length %d", sectionLength)
	}
	if sectionLength > uint64(reader.Len()) {
		return nil, io.ErrUnexpectedEOF
	}
	section := make([]byte, int(sectionLength))
	if _, err := io.ReadFull(reader, section); err != nil {
		return nil, err
	}
	return section, nil
}

func readShieldedSerializableSection(value io.ReaderFrom, encoded []byte) error {
	reader := bytes.NewReader(encoded)
	if _, err := value.ReadFrom(reader); err != nil {
		return err
	}
	if reader.Len() != 0 {
		return fmt.Errorf("trailing section bytes")
	}
	return nil
}
