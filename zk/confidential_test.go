package zk

import (
	"bytes"
	"crypto/elliptic"
	"math/big"
	"testing"
)

func TestConfidentialAmountRangeConservationElGamalAndTSS(t *testing.T) {
	regulatorKeyPair := mustElGamalKeyPair(t)
	recoveredPrivateScalar := mustRecoverRegulatorScalar(t, regulatorKeyPair.PrivateScalar)
	inputOpening := mustAmountOpening(t, 700)
	inputCommitment := mustAmountCommitment(t, inputOpening)
	inputCiphertext := mustEncryptAmount(t, regulatorKeyPair.PublicKey, inputOpening.Amount)

	inputRangeProof := mustRangeProof(t, inputOpening)
	mustVerifyRangeProof(t, inputRangeProof)
	mustVerifyCommitmentAmount(t, inputCommitment, inputOpening)
	assertDecryptedAmount(t, recoveredPrivateScalar, inputCiphertext, 700)

	outputOpening := mustAmountOpening(t, inputOpening.Amount)
	outputCommitment := mustAmountCommitment(t, outputOpening)
	outputCiphertext := mustEncryptAmount(t, regulatorKeyPair.PublicKey, outputOpening.Amount)
	outputRangeProof := mustRangeProof(t, outputOpening)
	mustVerifyRangeProof(t, outputRangeProof)
	assertDecryptedAmount(t, recoveredPrivateScalar, outputCiphertext, 700)

	privateTransferDelta := mustScalarDelta(t, inputOpening.Blinding, outputOpening.Blinding)
	privateTransferProof, err := NewBalanceProof([][]byte{inputCommitment}, [][]byte{outputCommitment}, 0, privateTransferDelta)
	if err != nil {
		t.Fatalf("NewBalanceProof(private transfer) error = %v", err)
	}
	if err := VerifyBalanceProof([][]byte{inputCommitment}, [][]byte{outputCommitment}, 0, privateTransferProof); err != nil {
		t.Fatalf("VerifyBalanceProof(private transfer) error = %v", err)
	}

	withdrawProof, err := NewBalanceProof([][]byte{outputCommitment}, nil, outputOpening.Amount, outputOpening.Blinding)
	if err != nil {
		t.Fatalf("NewBalanceProof(withdraw) error = %v", err)
	}
	if err := VerifyBalanceProof([][]byte{outputCommitment}, nil, outputOpening.Amount, withdrawProof); err != nil {
		t.Fatalf("VerifyBalanceProof(withdraw) error = %v", err)
	}
}

func TestConfidentialProofRejectsTampering(t *testing.T) {
	opening := mustAmountOpening(t, 700)
	commitment := mustAmountCommitment(t, opening)
	rangeProof := mustRangeProof(t, opening)
	tamperedRangeProof := cloneRangeProofForTest(rangeProof)
	tamperedRangeProof.BitProofs[0].Response0[0] ^= 0x01
	if err := tamperedRangeProof.Verify(); err == nil {
		t.Fatal("tampered range proof verified")
	}

	badOutputOpening := mustAmountOpening(t, 701)
	badOutputCommitment := mustAmountCommitment(t, badOutputOpening)
	badDelta := mustScalarDelta(t, opening.Blinding, badOutputOpening.Blinding)
	if _, err := NewBalanceProof([][]byte{commitment}, [][]byte{badOutputCommitment}, 0, badDelta); err == nil {
		t.Fatal("unbalanced private transfer proof was created")
	}

	if _, err := NewCommitmentAmountProof(commitment, 701, opening.Blinding); err == nil {
		t.Fatal("wrong public amount proof was created")
	}
}

func TestAmountCiphertextProofBindsAuditCiphertextToCommitment(t *testing.T) {
	regulatorKeyPair := mustElGamalKeyPair(t)
	opening := mustAmountOpening(t, 700)
	commitment := mustAmountCommitment(t, opening)
	ciphertext, randomness := mustEncryptAmountWithRandomness(t, regulatorKeyPair.PublicKey, opening.Amount)
	proof, err := NewAmountCiphertextProof(regulatorKeyPair.PublicKey, commitment, ciphertext, opening, randomness)
	if err != nil {
		t.Fatalf("NewAmountCiphertextProof() error = %v", err)
	}
	encoded, err := proof.MarshalBinary()
	if err != nil {
		t.Fatalf("AmountCiphertextProof.MarshalBinary() error = %v", err)
	}
	decoded, err := UnmarshalAmountCiphertextProofBinary(encoded)
	if err != nil {
		t.Fatalf("UnmarshalAmountCiphertextProofBinary() error = %v", err)
	}
	if err := VerifyAmountCiphertextProof(regulatorKeyPair.PublicKey, commitment, ciphertext, decoded); err != nil {
		t.Fatalf("VerifyAmountCiphertextProof() error = %v", err)
	}

	wrongCiphertext := mustEncryptAmount(t, regulatorKeyPair.PublicKey, opening.Amount+1)
	if err := VerifyAmountCiphertextProof(regulatorKeyPair.PublicKey, commitment, wrongCiphertext, decoded); err == nil {
		t.Fatal("VerifyAmountCiphertextProof() accepted mismatched ciphertext")
	}
	if _, err := NewAmountCiphertextProof(regulatorKeyPair.PublicKey, commitment, wrongCiphertext, opening, randomness); err == nil {
		t.Fatal("NewAmountCiphertextProof() accepted ciphertext built from a different amount or randomness")
	}
}

func TestThresholdScalarRecoveryRejectsInvalidShares(t *testing.T) {
	regulatorKeyPair := mustElGamalKeyPair(t)
	shares, err := SplitScalar(regulatorKeyPair.PrivateScalar, 3, 5)
	if err != nil {
		t.Fatalf("SplitScalar() error = %v", err)
	}
	if _, err := RecoverScalar(shares[:2], 3); err == nil {
		t.Fatal("RecoverScalar() accepted insufficient shares")
	}
	duplicateShares := []ThresholdShare{shares[0], shares[0], shares[2]}
	if _, err := RecoverScalar(duplicateShares, 3); err == nil {
		t.Fatal("RecoverScalar() accepted duplicate shares")
	}
}

func mustElGamalKeyPair(t *testing.T) ElGamalKeyPair {
	t.Helper()

	keyPair, err := GenerateElGamalKeyPair()
	if err != nil {
		t.Fatalf("GenerateElGamalKeyPair() error = %v", err)
	}
	return keyPair
}

func mustRecoverRegulatorScalar(t *testing.T, privateScalar []byte) []byte {
	t.Helper()

	shares, err := SplitScalar(privateScalar, 3, 5)
	if err != nil {
		t.Fatalf("SplitScalar() error = %v", err)
	}
	recoveredPrivateScalar, err := RecoverScalar([]ThresholdShare{shares[0], shares[2], shares[4]}, 3)
	if err != nil {
		t.Fatalf("RecoverScalar() error = %v", err)
	}
	if !bytes.Equal(recoveredPrivateScalar, privateScalar) {
		t.Fatal("recovered regulator scalar does not match original")
	}
	return recoveredPrivateScalar
}

func mustAmountOpening(t *testing.T, amount uint64) AmountOpening {
	t.Helper()

	opening, err := NewAmountOpening(amount)
	if err != nil {
		t.Fatalf("NewAmountOpening() error = %v", err)
	}
	return opening
}

func mustAmountCommitment(t *testing.T, opening AmountOpening) []byte {
	t.Helper()

	commitment, err := CommitAmount(opening)
	if err != nil {
		t.Fatalf("CommitAmount() error = %v", err)
	}
	return commitment
}

func mustEncryptAmount(t *testing.T, publicKey []byte, amount uint64) ElGamalCiphertext {
	t.Helper()

	ciphertext, _ := mustEncryptAmountWithRandomness(t, publicKey, amount)
	return ciphertext
}

func mustEncryptAmountWithRandomness(t *testing.T, publicKey []byte, amount uint64) (ElGamalCiphertext, []byte) {
	t.Helper()

	ciphertext, randomness, err := EncryptAmount(publicKey, amount)
	if err != nil {
		t.Fatalf("EncryptAmount() error = %v", err)
	}
	return ciphertext, randomness
}

func mustRangeProof(t *testing.T, opening AmountOpening) RangeProof {
	t.Helper()

	proof, err := NewRangeProof(opening, ConfidentialRangeBits)
	if err != nil {
		t.Fatalf("NewRangeProof() error = %v", err)
	}
	return proof
}

func mustVerifyRangeProof(t *testing.T, proof RangeProof) {
	t.Helper()

	encoded, err := proof.MarshalBinary()
	if err != nil {
		t.Fatalf("RangeProof.MarshalBinary() error = %v", err)
	}
	decoded, err := UnmarshalRangeProofBinary(encoded)
	if err != nil {
		t.Fatalf("UnmarshalRangeProofBinary() error = %v", err)
	}
	if err := decoded.Verify(); err != nil {
		t.Fatalf("RangeProof.Verify() error = %v", err)
	}
}

func mustVerifyCommitmentAmount(t *testing.T, commitment []byte, opening AmountOpening) {
	t.Helper()

	proof, err := NewCommitmentAmountProof(commitment, opening.Amount, opening.Blinding)
	if err != nil {
		t.Fatalf("NewCommitmentAmountProof() error = %v", err)
	}
	encoded, err := proof.MarshalBinary()
	if err != nil {
		t.Fatalf("BalanceProof.MarshalBinary() error = %v", err)
	}
	decoded, err := UnmarshalBalanceProofBinary(encoded)
	if err != nil {
		t.Fatalf("UnmarshalBalanceProofBinary() error = %v", err)
	}
	if err := VerifyCommitmentAmountProof(commitment, opening.Amount, decoded); err != nil {
		t.Fatalf("VerifyCommitmentAmountProof() error = %v", err)
	}
}

func assertDecryptedAmount(t *testing.T, privateScalar []byte, ciphertext ElGamalCiphertext, want uint64) {
	t.Helper()

	got, err := DecryptAmount(privateScalar, ciphertext, 2048)
	if err != nil {
		t.Fatalf("DecryptAmount() error = %v", err)
	}
	if got != want {
		t.Fatalf("DecryptAmount() = %d, want %d", got, want)
	}
}

func mustScalarDelta(t *testing.T, inputBlinding []byte, outputBlinding []byte) []byte {
	t.Helper()

	inputValue, err := scalarFromBytes(inputBlinding, true)
	if err != nil {
		t.Fatalf("input scalarFromBytes() error = %v", err)
	}
	outputValue, err := scalarFromBytes(outputBlinding, true)
	if err != nil {
		t.Fatalf("output scalarFromBytes() error = %v", err)
	}
	order := elliptic.P256().Params().N
	delta := new(big.Int).Sub(inputValue, outputValue)
	delta.Mod(delta, order)
	return padScalar(delta)
}

func cloneRangeProofForTest(proof RangeProof) RangeProof {
	cloned := proof
	cloned.Commitment = cloneBytes(proof.Commitment)
	cloned.BitCommitments = cloneByteSlices(proof.BitCommitments)
	cloned.BitProofs = make([]BitProof, len(proof.BitProofs))
	for index, bitProof := range proof.BitProofs {
		cloned.BitProofs[index] = BitProof{
			Nonce0:     cloneBytes(bitProof.Nonce0),
			Nonce1:     cloneBytes(bitProof.Nonce1),
			Challenge0: cloneBytes(bitProof.Challenge0),
			Challenge1: cloneBytes(bitProof.Challenge1),
			Response0:  cloneBytes(bitProof.Response0),
			Response1:  cloneBytes(bitProof.Response1),
		}
	}
	return cloned
}
