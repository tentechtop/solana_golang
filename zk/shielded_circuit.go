package zk

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"

	"github.com/consensys/gnark-crypto/ecc"
	bn254fr "github.com/consensys/gnark-crypto/ecc/bn254/fr"
	bn254mimc "github.com/consensys/gnark-crypto/ecc/bn254/fr/mimc"
	"github.com/consensys/gnark/backend/groth16"
	groth16bn254 "github.com/consensys/gnark/backend/groth16/bn254"
	"github.com/consensys/gnark/constraint"
	"github.com/consensys/gnark/frontend"
	"github.com/consensys/gnark/frontend/cs/r1cs"
	"github.com/consensys/gnark/std/hash/mimc"
)

const (
	ShieldedActionCount  = 2
	ShieldedMerkleDepth  = 32
	ShieldedValueBitSize = 64
	// ShieldedGroth16ProofBytes 是当前无 commitment gadget 的 BN254 Groth16 规范压缩证明长度。
	ShieldedGroth16ProofBytes = 164
	ShieldedCircuitID         = uint32(0x53483201)

	shieldedOwnerDomainTag        = 1
	shieldedNoteDomainTag         = 2
	shieldedNullifierDomainTag    = 3
	shieldedMerkleDomainTag       = 4
	shieldedContextDomainTag      = 5
	shieldedNullifierKeyDomainTag = 6
)

const shieldedContextDigestDomain = "solana_golang.shielded.context.v2"

var (
	ErrInvalidShieldedWitness    = errors.New("zk: invalid shielded witness")
	ErrInvalidShieldedProof      = errors.New("zk: invalid shielded proof")
	ErrInvalidShieldedParameters = errors.New("zk: invalid shielded parameters")
)

// ShieldedCircuit 固化两个 action 的屏蔽转账关系。
// ValueBalanceDirection 为 0 时表示屏蔽输入大于输出，为 1 时表示屏蔽输出大于输入。
type ShieldedCircuit struct {
	Anchor                frontend.Variable                      `gnark:",public"`
	Nullifiers            [ShieldedActionCount]frontend.Variable `gnark:",public"`
	OutputCommitments     [ShieldedActionCount]frontend.Variable `gnark:",public"`
	ValueBalanceDirection frontend.Variable                      `gnark:",public"`
	ValueBalanceMagnitude frontend.Variable                      `gnark:",public"`
	ContextDigest         frontend.Variable                      `gnark:",public"`
	SpendEnabled          [ShieldedActionCount]frontend.Variable `gnark:",public"`
	OutputEnabled         [ShieldedActionCount]frontend.Variable `gnark:",public"`

	SpendSecrets      [ShieldedActionCount]frontend.Variable
	InputValues       [ShieldedActionCount]frontend.Variable
	InputRandomness   [ShieldedActionCount]frontend.Variable
	InputDiversifiers [ShieldedActionCount]frontend.Variable
	MerklePaths       [ShieldedActionCount][ShieldedMerkleDepth]frontend.Variable
	MerklePositions   [ShieldedActionCount]frontend.Variable

	OutputValues     [ShieldedActionCount]frontend.Variable
	OutputRandomness [ShieldedActionCount]frontend.Variable
	OutputOwnerTags  [ShieldedActionCount]frontend.Variable
}

// Define 约束 note 所有权、成员关系、nullifier、输出承诺、数值范围和资金守恒。
func (circuit *ShieldedCircuit) Define(api frontend.API) error {
	hasher, err := mimc.NewMiMC(api)
	if err != nil {
		return fmt.Errorf("create shielded MiMC: %w", err)
	}

	api.AssertIsBoolean(circuit.ValueBalanceDirection)
	api.ToBinary(circuit.ValueBalanceMagnitude, ShieldedValueBitSize)
	api.AssertIsEqual(
		api.Mul(circuit.ValueBalanceDirection, api.IsZero(circuit.ValueBalanceMagnitude)),
		0,
	)

	inputTotal := frontend.Variable(0)
	outputTotal := frontend.Variable(0)

	for actionIndex := 0; actionIndex < ShieldedActionCount; actionIndex++ {
		spendEnabled := circuit.SpendEnabled[actionIndex]
		outputEnabled := circuit.OutputEnabled[actionIndex]
		api.AssertIsBoolean(spendEnabled)
		api.AssertIsBoolean(outputEnabled)

		api.ToBinary(circuit.InputValues[actionIndex], ShieldedValueBitSize)
		api.ToBinary(circuit.OutputValues[actionIndex], ShieldedValueBitSize)

		inputTotal = api.Add(inputTotal, api.Mul(spendEnabled, circuit.InputValues[actionIndex]))
		outputTotal = api.Add(outputTotal, api.Mul(outputEnabled, circuit.OutputValues[actionIndex]))

		ownerTag := shieldedCircuitHash(
			hasher,
			shieldedOwnerDomainTag,
			circuit.InputDiversifiers[actionIndex],
			circuit.SpendSecrets[actionIndex],
		)
		inputCommitment := shieldedCircuitHash(
			hasher,
			shieldedNoteDomainTag,
			ownerTag,
			circuit.InputValues[actionIndex],
			circuit.InputRandomness[actionIndex],
		)

		positionBits := api.ToBinary(circuit.MerklePositions[actionIndex], ShieldedMerkleDepth)
		computedRoot := inputCommitment
		for treeLevel := 0; treeLevel < ShieldedMerkleDepth; treeLevel++ {
			sibling := circuit.MerklePaths[actionIndex][treeLevel]
			left := api.Select(positionBits[treeLevel], sibling, computedRoot)
			right := api.Select(positionBits[treeLevel], computedRoot, sibling)
			computedRoot = shieldedCircuitHash(hasher, shieldedMerkleDomainTag, left, right)
		}
		api.AssertIsEqual(api.Mul(spendEnabled, api.Sub(computedRoot, circuit.Anchor)), 0)

		nullifierKey := shieldedCircuitHash(
			hasher,
			shieldedNullifierKeyDomainTag,
			circuit.SpendSecrets[actionIndex],
		)
		api.AssertIsEqual(api.Mul(spendEnabled, api.IsZero(nullifierKey)), 0)
		computedNullifier := shieldedCircuitHash(
			hasher,
			shieldedNullifierDomainTag,
			inputCommitment,
			circuit.MerklePositions[actionIndex],
			nullifierKey,
		)
		api.AssertIsEqual(
			api.Mul(spendEnabled, api.Sub(computedNullifier, circuit.Nullifiers[actionIndex])),
			0,
		)
		api.AssertIsEqual(api.Mul(api.Sub(1, spendEnabled), circuit.Nullifiers[actionIndex]), 0)

		// 上下文不参与 nullifier 派生，保证同一 note 的双花标识稳定。
		contextAuthorization := shieldedCircuitHash(
			hasher,
			shieldedContextDomainTag,
			circuit.ContextDigest,
			ownerTag,
			circuit.SpendSecrets[actionIndex],
		)
		api.AssertIsEqual(api.Mul(spendEnabled, api.IsZero(contextAuthorization)), 0)
		api.AssertIsEqual(api.Mul(spendEnabled, api.IsZero(circuit.SpendSecrets[actionIndex])), 0)
		api.AssertIsEqual(api.Mul(spendEnabled, api.IsZero(circuit.InputValues[actionIndex])), 0)

		computedOutputCommitment := shieldedCircuitHash(
			hasher,
			shieldedNoteDomainTag,
			circuit.OutputOwnerTags[actionIndex],
			circuit.OutputValues[actionIndex],
			circuit.OutputRandomness[actionIndex],
		)
		api.AssertIsEqual(
			api.Mul(outputEnabled, api.Sub(computedOutputCommitment, circuit.OutputCommitments[actionIndex])),
			0,
		)
		api.AssertIsEqual(api.Mul(api.Sub(1, outputEnabled), circuit.OutputCommitments[actionIndex]), 0)
		api.AssertIsEqual(api.Mul(outputEnabled, api.IsZero(circuit.OutputOwnerTags[actionIndex])), 0)
		api.AssertIsEqual(api.Mul(outputEnabled, api.IsZero(circuit.OutputValues[actionIndex])), 0)
	}

	// 两个启用的 spend 不得消费同一 nullifier。
	bothSpendsEnabled := api.Mul(circuit.SpendEnabled[0], circuit.SpendEnabled[1])
	duplicateNullifier := api.IsZero(api.Sub(circuit.Nullifiers[0], circuit.Nullifiers[1]))
	api.AssertIsEqual(api.Mul(bothSpendsEnabled, duplicateNullifier), 0)
	api.AssertIsDifferent(
		api.Add(
			circuit.SpendEnabled[0],
			circuit.SpendEnabled[1],
			circuit.OutputEnabled[0],
			circuit.OutputEnabled[1],
		),
		0,
	)

	signedValueBalance := api.Mul(
		api.Sub(1, api.Mul(2, circuit.ValueBalanceDirection)),
		circuit.ValueBalanceMagnitude,
	)
	api.AssertIsEqual(api.Sub(inputTotal, outputTotal), signedValueBalance)
	return nil
}

// CompileShieldedCircuit 编译固定布局的 BN254 R1CS。
func CompileShieldedCircuit() (constraint.ConstraintSystem, error) {
	compiled, err := frontend.Compile(ecc.BN254.ScalarField(), r1cs.NewBuilder, &ShieldedCircuit{})
	if err != nil {
		return nil, fmt.Errorf("compile shielded circuit: %w", err)
	}
	return compiled, nil
}

// ProveShieldedCircuit 为完整 witness 生成 Groth16 证明字节。
func ProveShieldedCircuit(parameters *ShieldedCircuitParameters, assignment *ShieldedCircuit) ([]byte, error) {
	if err := parameters.validateForProving(); err != nil {
		return nil, err
	}
	if assignment == nil {
		return nil, fmt.Errorf("%w: assignment is nil", ErrInvalidShieldedWitness)
	}
	fullWitness, err := frontend.NewWitness(assignment, ecc.BN254.ScalarField())
	if err != nil {
		return nil, fmt.Errorf("%w: build witness: %v", ErrInvalidShieldedWitness, err)
	}
	proof, err := groth16.Prove(parameters.ConstraintSystem, parameters.ProvingKey, fullWitness)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidShieldedWitness, err)
	}
	proofBuffer := bytes.Buffer{}
	if _, err := proof.WriteTo(&proofBuffer); err != nil {
		return nil, fmt.Errorf("serialize shielded proof: %w", err)
	}
	return proofBuffer.Bytes(), nil
}

// VerifyShieldedCircuit 使用公开 assignment 验证 Groth16 证明。
func VerifyShieldedCircuit(verifyingKey groth16.VerifyingKey, publicAssignment *ShieldedCircuit, proofBytes []byte) error {
	if verifyingKey == nil {
		return fmt.Errorf("%w: verifying key is nil", ErrInvalidShieldedParameters)
	}
	if publicAssignment == nil {
		return fmt.Errorf("%w: public assignment is nil", ErrInvalidShieldedWitness)
	}
	if len(proofBytes) != ShieldedGroth16ProofBytes {
		return fmt.Errorf(
			"%w: proof length must be %d, got %d",
			ErrInvalidShieldedProof,
			ShieldedGroth16ProofBytes,
			len(proofBytes),
		)
	}

	proof := groth16.NewProof(ecc.BN254)
	proofReader := bytes.NewReader(proofBytes)
	if _, err := proof.ReadFrom(proofReader); err != nil {
		return fmt.Errorf("%w: decode proof: %v", ErrInvalidShieldedProof, err)
	}
	if proofReader.Len() != 0 {
		return fmt.Errorf("%w: trailing proof bytes", ErrInvalidShieldedProof)
	}
	canonicalProof := bytes.Buffer{}
	if _, err := proof.WriteTo(&canonicalProof); err != nil {
		return fmt.Errorf("%w: encode canonical proof: %v", ErrInvalidShieldedProof, err)
	}
	if !bytes.Equal(canonicalProof.Bytes(), proofBytes) {
		return fmt.Errorf("%w: proof encoding is not canonical", ErrInvalidShieldedProof)
	}
	bn254Proof, ok := proof.(*groth16bn254.Proof)
	if !ok || len(bn254Proof.Commitments) != 0 || !bn254Proof.CommitmentPok.IsInfinity() {
		return fmt.Errorf("%w: unexpected proof commitment data", ErrInvalidShieldedProof)
	}
	publicWitness, err := frontend.NewWitness(publicAssignment, ecc.BN254.ScalarField(), frontend.PublicOnly())
	if err != nil {
		return fmt.Errorf("%w: build public witness: %v", ErrInvalidShieldedWitness, err)
	}
	if err := groth16.Verify(proof, verifyingKey, publicWitness); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidShieldedProof, err)
	}
	return nil
}

// DeriveShieldedOwnerTag 从花费密钥和 diversifier 派生 note 所有者标签。
func DeriveShieldedOwnerTag(spendSecret, diversifier *big.Int) (*big.Int, error) {
	return shieldedNativeHash(
		big.NewInt(shieldedOwnerDomainTag),
		diversifier,
		spendSecret,
	)
}

// ComputeShieldedNoteCommitment 计算与电路一致的 note commitment。
func ComputeShieldedNoteCommitment(ownerTag, value, randomness *big.Int) (*big.Int, error) {
	return shieldedNativeHash(
		big.NewInt(shieldedNoteDomainTag),
		ownerTag,
		value,
		randomness,
	)
}

// DeriveShieldedNullifierKey 派生允许 full viewing key 识别已花费 note 的 nullifier key。
func DeriveShieldedNullifierKey(spendSecret *big.Int) (*big.Int, error) {
	return shieldedNativeHash(
		big.NewInt(shieldedNullifierKeyDomainTag),
		spendSecret,
	)
}

// ComputeShieldedNullifier 使用 nullifier key 计算稳定且与交易上下文无关的 nullifier。
func ComputeShieldedNullifier(nullifierKey, noteCommitment, position *big.Int) (*big.Int, error) {
	return shieldedNativeHash(
		big.NewInt(shieldedNullifierDomainTag),
		noteCommitment,
		position,
		nullifierKey,
	)
}

// ComputeShieldedMerkleRoot 根据固定深度路径计算根。
func ComputeShieldedMerkleRoot(noteCommitment *big.Int, merklePath []*big.Int, position uint64) (*big.Int, error) {
	if noteCommitment == nil {
		return nil, fmt.Errorf("%w: note commitment is nil", ErrInvalidShieldedWitness)
	}
	if len(merklePath) != ShieldedMerkleDepth {
		return nil, fmt.Errorf(
			"%w: Merkle path requires %d nodes, got %d",
			ErrInvalidShieldedWitness,
			ShieldedMerkleDepth,
			len(merklePath),
		)
	}
	computedRoot := new(big.Int).Set(noteCommitment)
	for treeLevel, sibling := range merklePath {
		if sibling == nil {
			return nil, fmt.Errorf("%w: Merkle path node %d is nil", ErrInvalidShieldedWitness, treeLevel)
		}
		var err error
		if (position>>treeLevel)&1 == 0 {
			computedRoot, err = shieldedNativeHash(
				big.NewInt(shieldedMerkleDomainTag),
				computedRoot,
				sibling,
			)
		} else {
			computedRoot, err = shieldedNativeHash(
				big.NewInt(shieldedMerkleDomainTag),
				sibling,
				computedRoot,
			)
		}
		if err != nil {
			return nil, err
		}
	}
	return computedRoot, nil
}

// ComputeShieldedMerkleNode 计算与电路一致的内部 Merkle 节点。
func ComputeShieldedMerkleNode(left, right *big.Int) (*big.Int, error) {
	return shieldedNativeHash(big.NewInt(shieldedMerkleDomainTag), left, right)
}

// ComputeShieldedEmptyRoot 计算零叶子填充的固定深度空树根。
func ComputeShieldedEmptyRoot(depth int) (*big.Int, error) {
	if depth < 1 || depth > ShieldedMerkleDepth {
		return nil, fmt.Errorf("%w: empty tree depth %d is outside [1,%d]", ErrInvalidShieldedWitness, depth, ShieldedMerkleDepth)
	}
	current := big.NewInt(0)
	for level := 0; level < depth; level++ {
		var err error
		current, err = ComputeShieldedMerkleNode(current, current)
		if err != nil {
			return nil, err
		}
	}
	return current, nil
}

// ShieldedFieldFromBytes 解码 canonical 32 字节 BN254 标量域元素。
func ShieldedFieldFromBytes(encoded []byte) (*big.Int, error) {
	if len(encoded) != 32 {
		return nil, fmt.Errorf("%w: field element requires 32 bytes, got %d", ErrInvalidShieldedWitness, len(encoded))
	}
	value := new(big.Int).SetBytes(encoded)
	if value.Cmp(ecc.BN254.ScalarField()) >= 0 {
		return nil, fmt.Errorf("%w: field element is not canonical", ErrInvalidShieldedWitness)
	}
	return value, nil
}

// ShieldedFieldBytes 编码 canonical 32 字节 BN254 标量域元素。
func ShieldedFieldBytes(value *big.Int) ([32]byte, error) {
	var encoded [32]byte
	if value == nil || value.Sign() < 0 || value.Cmp(ecc.BN254.ScalarField()) >= 0 {
		return encoded, fmt.Errorf("%w: field element is not canonical", ErrInvalidShieldedWitness)
	}
	value.FillBytes(encoded[:])
	return encoded, nil
}

// ComputeShieldedContextDigest 将交易公开上下文映射到 BN254 标量域。
func ComputeShieldedContextDigest(parts ...[]byte) *big.Int {
	hasher := sha256.New()
	hasher.Write([]byte(shieldedContextDigestDomain))
	var lengthBytes [8]byte
	for _, part := range parts {
		binary.BigEndian.PutUint64(lengthBytes[:], uint64(len(part)))
		hasher.Write(lengthBytes[:])
		hasher.Write(part)
	}
	digest := new(big.Int).SetBytes(hasher.Sum(nil))
	digest.Mod(digest, ecc.BN254.ScalarField())
	if digest.Sign() == 0 {
		digest.SetUint64(1)
	}
	return digest
}

func shieldedCircuitHash(hasher mimc.MiMC, values ...frontend.Variable) frontend.Variable {
	hasher.Reset()
	hasher.Write(values...)
	return hasher.Sum()
}

func shieldedNativeHash(values ...*big.Int) (*big.Int, error) {
	hasher := bn254mimc.NewMiMC()
	for valueIndex, value := range values {
		if value == nil {
			return nil, fmt.Errorf("%w: hash input %d is nil", ErrInvalidShieldedWitness, valueIndex)
		}
		if value.Sign() < 0 || value.Cmp(ecc.BN254.ScalarField()) >= 0 {
			return nil, fmt.Errorf(
				"%w: hash input %d is not a canonical BN254 field element",
				ErrInvalidShieldedWitness,
				valueIndex,
			)
		}
		var fieldElement bn254fr.Element
		fieldElement.SetBigInt(value)
		encodedElement := fieldElement.Bytes()
		if _, err := hasher.Write(encodedElement[:]); err != nil {
			return nil, fmt.Errorf("hash shielded input %d: %w", valueIndex, err)
		}
	}
	return new(big.Int).SetBytes(hasher.Sum(nil)), nil
}
