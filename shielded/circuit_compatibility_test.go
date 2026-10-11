package shielded

import (
	"bytes"
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/tentechtop/ordexus-core/structure"
	"github.com/tentechtop/ordexus-core/zk"
)

func TestWalletPrimitivesMatchShieldedCircuit(t *testing.T) {
	spendingKey, viewKey, address, err := DeriveKeySet(bytes.Repeat([]byte{0xc1}, SeedSize), testDiversifier(0x51))
	if err != nil {
		t.Fatalf("DeriveKeySet() error = %v", err)
	}
	note, err := NewNote(880_001, testRandomness(0x7a), []byte("电路外加密 memo"))
	if err != nil {
		t.Fatalf("NewNote() error = %v", err)
	}

	spendSecretField := new(big.Int).SetBytes(spendingKey.Bytes())
	diversifierField := new(big.Int).SetBytes(address.Diversifier[:])
	expectedOwnerTag, err := zk.DeriveShieldedOwnerTag(spendSecretField, diversifierField)
	if err != nil {
		t.Fatalf("zk.DeriveShieldedOwnerTag() error = %v", err)
	}
	if expectedOwnerTag.Cmp(new(big.Int).SetBytes(address.OwnerTag[:])) != 0 {
		t.Fatal("钱包 owner tag 与证明电路不一致")
	}

	expectedCommitment, err := zk.ComputeShieldedNoteCommitment(
		expectedOwnerTag,
		new(big.Int).SetUint64(note.Value),
		new(big.Int).SetBytes(note.Randomness[:]),
	)
	if err != nil {
		t.Fatalf("zk.ComputeShieldedNoteCommitment() error = %v", err)
	}
	commitment, err := note.Commitment(address)
	if err != nil {
		t.Fatalf("Note.Commitment() error = %v", err)
	}
	if expectedCommitment.Cmp(new(big.Int).SetBytes(commitment[:])) != 0 {
		t.Fatal("钱包 note commitment 与证明电路不一致")
	}

	expectedNullifierKey, err := zk.DeriveShieldedNullifierKey(spendSecretField)
	if err != nil {
		t.Fatalf("zk.DeriveShieldedNullifierKey() error = %v", err)
	}
	if expectedNullifierKey.Cmp(new(big.Int).SetBytes(viewKey.nullifierKey[:])) != 0 {
		t.Fatal("钱包 nullifier key 与证明电路不一致")
	}
	const position = uint64(9)
	expectedNullifier, err := zk.ComputeShieldedNullifier(
		expectedNullifierKey,
		expectedCommitment,
		new(big.Int).SetUint64(uint64(position)),
	)
	if err != nil {
		t.Fatalf("zk.ComputeShieldedNullifier() error = %v", err)
	}
	nullifier, err := ComputeNullifier(viewKey, commitment, uint64(position))
	if err != nil {
		t.Fatalf("ComputeNullifier() error = %v", err)
	}
	if expectedNullifier.Cmp(new(big.Int).SetBytes(nullifier[:])) != 0 {
		t.Fatal("钱包 nullifier 与证明电路不一致")
	}

	tree, err := NewCircuitMerkleTree()
	if err != nil {
		t.Fatalf("NewCircuitMerkleTree() error = %v", err)
	}
	for leafPosition := uint64(0); leafPosition <= position; leafPosition++ {
		leaf := digestFromUint64(uint64(2_000 + leafPosition))
		if leafPosition == position {
			leaf = commitment
		}
		if _, err := tree.Append(leaf); err != nil {
			t.Fatalf("Append(%d) error = %v", leafPosition, err)
		}
	}
	witness, err := tree.Witness(uint64(position))
	if err != nil {
		t.Fatalf("Witness() error = %v", err)
	}
	if witness.Depth != CircuitMerkleDepth {
		t.Fatalf("wallet depth %d, want %d", witness.Depth, CircuitMerkleDepth)
	}
	if CircuitMerkleDepth != zk.ShieldedMerkleDepth {
		t.Fatalf("wallet depth %d, circuit depth %d", CircuitMerkleDepth, zk.ShieldedMerkleDepth)
	}
	if structure.ShieldedGroth16ProofBytes != zk.ShieldedGroth16ProofBytes {
		t.Fatalf("wire proof bytes %d, verifier proof bytes %d", structure.ShieldedGroth16ProofBytes, zk.ShieldedGroth16ProofBytes)
	}
	path := make([]*big.Int, len(witness.Siblings))
	for siblingIndex, sibling := range witness.Siblings {
		path[siblingIndex] = new(big.Int).SetBytes(sibling[:])
	}
	expectedRoot, err := zk.ComputeShieldedMerkleRoot(expectedCommitment, path, position)
	if err != nil {
		t.Fatalf("zk.ComputeShieldedMerkleRoot() error = %v", err)
	}
	walletRoot := tree.Root()
	if expectedRoot.Cmp(new(big.Int).SetBytes(walletRoot[:])) != 0 {
		t.Fatal("钱包 Merkle root 与证明电路不一致")
	}

	goldenValues := map[string][2]string{
		"spending key": {
			hex.EncodeToString(spendingKey.Bytes()),
			"09e4f1723d862581cfcc1c53d452231dc48690895d186cb7f6045f1fbc23da18",
		},
		"address": {
			address.String(),
			"zs2_AlFSU1RVVldYWVpbDAh1uTlM7MuvcqIrac4fHHBIxMF0XmIC3_pNNy3BtJhslsmWyaCMgBAQYhE8zBdKR2C0apcTvlDxFTgTGNBtR2Jp6IM",
		},
		"owner tag": {
			hex.EncodeToString(address.OwnerTag[:]),
			"0c0875b9394ceccbaf72a22b69ce1f1c7048c4c1745e6202dffa4d372dc1b498",
		},
		"commitment": {
			commitment.String(),
			"01d8cc75ae224dea9b13077ee3cd6a5ccbb91ac6a7f364c1f5a8f1c43145cebe",
		},
		"nullifier": {
			nullifier.String(),
			"188f833f34c72d25dc11a9d2352dc03dd713517d9a92f32d97b96cc28e11313f",
		},
		"root": {
			walletRoot.String(),
			"0e68a8305cb2473727f36761a5ef4b4dfa6deb1081d32d9d88438ebb176e5257",
		},
	}
	for valueName, values := range goldenValues {
		if values[0] != values[1] {
			t.Fatalf("%s vector = %s, want %s", valueName, values[0], values[1])
		}
	}
}
