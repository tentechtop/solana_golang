package zk

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/consensys/gnark-crypto/ecc"
	bn254curve "github.com/consensys/gnark-crypto/ecc/bn254"
	"github.com/consensys/gnark/backend/groth16"
	groth16bn254 "github.com/consensys/gnark/backend/groth16/bn254"
)

func TestShieldedCircuitProofLifecycle(t *testing.T) {
	parameters, err := GenerateShieldedCircuitParameters()
	if err != nil {
		t.Fatalf("GenerateShieldedCircuitParameters() error = %v", err)
	}
	assignment := newValidShieldedAssignment(t)

	proof, err := parameters.Prove(&assignment)
	if err != nil {
		t.Fatalf("Prove() error = %v", err)
	}
	if len(proof) != ShieldedGroth16ProofBytes {
		t.Fatalf("proof length = %d, want %d", len(proof), ShieldedGroth16ProofBytes)
	}
	if err := parameters.Verify(&assignment, proof); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}

	t.Run("raw proof encoding", func(t *testing.T) {
		decodedProof := groth16.NewProof(ecc.BN254)
		if _, err := decodedProof.ReadFrom(bytes.NewReader(proof)); err != nil {
			t.Fatalf("ReadFrom() error = %v", err)
		}
		rawProof := bytes.Buffer{}
		if _, err := decodedProof.WriteRawTo(&rawProof); err != nil {
			t.Fatalf("WriteRawTo() error = %v", err)
		}
		if err := parameters.Verify(&assignment, rawProof.Bytes()); err == nil {
			t.Fatal("Verify() accepted a non-canonical raw proof")
		}
	})

	t.Run("unused commitment proof", func(t *testing.T) {
		decodedProof := groth16.NewProof(ecc.BN254)
		if _, err := decodedProof.ReadFrom(bytes.NewReader(proof)); err != nil {
			t.Fatalf("ReadFrom() error = %v", err)
		}
		bn254Proof, ok := decodedProof.(*groth16bn254.Proof)
		if !ok {
			t.Fatal("decoded proof is not BN254")
		}
		_, _, generator, _ := bn254curve.Generators()
		bn254Proof.CommitmentPok = generator
		malleableProof := bytes.Buffer{}
		if _, err := bn254Proof.WriteTo(&malleableProof); err != nil {
			t.Fatalf("WriteTo() error = %v", err)
		}
		if malleableProof.Len() != ShieldedGroth16ProofBytes {
			t.Fatalf("malleable proof length = %d, want %d", malleableProof.Len(), ShieldedGroth16ProofBytes)
		}
		if err := parameters.Verify(&assignment, malleableProof.Bytes()); err == nil {
			t.Fatal("Verify() accepted unused commitment proof data")
		}
	})

	t.Run("wrong input amount", func(t *testing.T) {
		invalidAssignment := assignment
		invalidAssignment.InputValues[0] = uint64(99)
		if _, err := parameters.Prove(&invalidAssignment); err == nil {
			t.Fatal("Prove() accepted an invalid input amount")
		}
	})

	t.Run("input amount exceeds 64 bits", func(t *testing.T) {
		invalidAssignment := assignment
		invalidAssignment.InputValues[0] = new(big.Int).Lsh(big.NewInt(1), ShieldedValueBitSize)
		if _, err := parameters.Prove(&invalidAssignment); err == nil {
			t.Fatal("Prove() accepted an input amount larger than 64 bits")
		}
	})

	t.Run("wrong Merkle path", func(t *testing.T) {
		invalidAssignment := assignment
		invalidAssignment.MerklePaths[0][7] = big.NewInt(987654321)
		if _, err := parameters.Prove(&invalidAssignment); err == nil {
			t.Fatal("Prove() accepted an invalid Merkle path")
		}
	})

	t.Run("wrong nullifier", func(t *testing.T) {
		invalidPublicAssignment := assignment
		invalidPublicAssignment.Nullifiers[0] = new(big.Int).Add(
			assignment.Nullifiers[0].(*big.Int),
			big.NewInt(1),
		)
		if err := parameters.Verify(&invalidPublicAssignment, proof); err == nil {
			t.Fatal("Verify() accepted a proof with the wrong nullifier")
		}
		if _, err := parameters.Prove(&invalidPublicAssignment); err == nil {
			t.Fatal("Prove() accepted the wrong nullifier")
		}
	})

	t.Run("parameter serialization", func(t *testing.T) {
		encodedParameters, err := parameters.MarshalBinary()
		if err != nil {
			t.Fatalf("MarshalBinary() error = %v", err)
		}
		decodedParameters, err := UnmarshalShieldedCircuitParameters(encodedParameters)
		if err != nil {
			t.Fatalf("UnmarshalShieldedCircuitParameters() error = %v", err)
		}
		if err := decodedParameters.Verify(&assignment, proof); err != nil {
			t.Fatalf("decoded Verify() error = %v", err)
		}

		corruptedParameters := append([]byte(nil), encodedParameters...)
		corruptedParameters[len(corruptedParameters)/2] ^= 0x01
		if _, err := UnmarshalShieldedCircuitParameters(corruptedParameters); err == nil {
			t.Fatal("UnmarshalShieldedCircuitParameters() accepted corrupted parameters")
		}
	})
}

func newValidShieldedAssignment(t *testing.T) ShieldedCircuit {
	t.Helper()
	assignment := newZeroShieldedAssignment()

	spendSecret := big.NewInt(1234567)
	inputDiversifier := big.NewInt(7654321)
	inputValue := big.NewInt(100)
	inputRandomness := big.NewInt(11223344)
	position := uint64(37)

	inputOwnerTag, err := DeriveShieldedOwnerTag(spendSecret, inputDiversifier)
	if err != nil {
		t.Fatalf("DeriveShieldedOwnerTag() error = %v", err)
	}
	inputCommitment, err := ComputeShieldedNoteCommitment(inputOwnerTag, inputValue, inputRandomness)
	if err != nil {
		t.Fatalf("ComputeShieldedNoteCommitment(input) error = %v", err)
	}
	merklePath := make([]*big.Int, ShieldedMerkleDepth)
	for treeLevel := range merklePath {
		merklePath[treeLevel] = big.NewInt(int64(1000 + treeLevel))
		assignment.MerklePaths[0][treeLevel] = new(big.Int).Set(merklePath[treeLevel])
	}
	anchor, err := ComputeShieldedMerkleRoot(inputCommitment, merklePath, position)
	if err != nil {
		t.Fatalf("ComputeShieldedMerkleRoot() error = %v", err)
	}
	nullifierKey, err := DeriveShieldedNullifierKey(spendSecret)
	if err != nil {
		t.Fatalf("DeriveShieldedNullifierKey() error = %v", err)
	}
	nullifier, err := ComputeShieldedNullifier(nullifierKey, inputCommitment, big.NewInt(int64(position)))
	if err != nil {
		t.Fatalf("ComputeShieldedNullifier() error = %v", err)
	}

	receiverOwnerTag, err := DeriveShieldedOwnerTag(big.NewInt(246810), big.NewInt(13579))
	if err != nil {
		t.Fatalf("DeriveShieldedOwnerTag(receiver) error = %v", err)
	}
	outputValue := big.NewInt(70)
	outputRandomness := big.NewInt(55667788)
	outputCommitment, err := ComputeShieldedNoteCommitment(receiverOwnerTag, outputValue, outputRandomness)
	if err != nil {
		t.Fatalf("ComputeShieldedNoteCommitment(output) error = %v", err)
	}

	assignment.Anchor = anchor
	assignment.Nullifiers[0] = nullifier
	assignment.OutputCommitments[0] = outputCommitment
	assignment.ValueBalanceDirection = uint8(0)
	assignment.ValueBalanceMagnitude = uint64(30)
	assignment.ContextDigest = big.NewInt(99887766)
	assignment.SpendEnabled[0] = uint8(1)
	assignment.OutputEnabled[0] = uint8(1)

	assignment.SpendSecrets[0] = spendSecret
	assignment.InputValues[0] = inputValue
	assignment.InputRandomness[0] = inputRandomness
	assignment.InputDiversifiers[0] = inputDiversifier
	assignment.MerklePositions[0] = uint64(position)
	assignment.OutputValues[0] = outputValue
	assignment.OutputRandomness[0] = outputRandomness
	assignment.OutputOwnerTags[0] = receiverOwnerTag
	return assignment
}

func newZeroShieldedAssignment() ShieldedCircuit {
	assignment := ShieldedCircuit{
		Anchor:                uint64(0),
		ValueBalanceDirection: uint8(0),
		ValueBalanceMagnitude: uint64(0),
		ContextDigest:         uint64(0),
	}
	for actionIndex := 0; actionIndex < ShieldedActionCount; actionIndex++ {
		assignment.Nullifiers[actionIndex] = uint64(0)
		assignment.OutputCommitments[actionIndex] = uint64(0)
		assignment.SpendEnabled[actionIndex] = uint8(0)
		assignment.OutputEnabled[actionIndex] = uint8(0)
		assignment.SpendSecrets[actionIndex] = uint64(0)
		assignment.InputValues[actionIndex] = uint64(0)
		assignment.InputRandomness[actionIndex] = uint64(0)
		assignment.InputDiversifiers[actionIndex] = uint64(0)
		assignment.MerklePositions[actionIndex] = uint64(0)
		assignment.OutputValues[actionIndex] = uint64(0)
		assignment.OutputRandomness[actionIndex] = uint64(0)
		assignment.OutputOwnerTags[actionIndex] = uint64(0)
		for treeLevel := 0; treeLevel < ShieldedMerkleDepth; treeLevel++ {
			assignment.MerklePaths[actionIndex][treeLevel] = uint64(0)
		}
	}
	return assignment
}
