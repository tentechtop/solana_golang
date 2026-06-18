package privacy

import (
	"testing"

	"solana_golang/runtime"
	"solana_golang/structure"
	"solana_golang/utils"
)

func TestProgramExecutesConfidentialDepositWithMerkleAndLiability(t *testing.T) {
	programID := newTestPublicKey(210)
	sourceAddress := newTestPublicKey(211)
	stateAddress := newTestPublicKey(212)
	spendKeyPair := mustConfidentialSpendKeyPair(t)
	spendAuthority := mustConfidentialSpendAuthority(t, spendKeyPair.PublicKey)
	regulatorKeyPair := mustConfidentialRegulatorKeyPair(t)
	deposit := newConfidentialOutputFixture(t, 700, spendAuthority, regulatorKeyPair.PublicKey)
	commitmentHash := mustHashBytes(t, deposit.Output.Commitment)
	instruction, err := structure.NewPrivacyDepositInstruction(1, nil, structure.PrivacyDepositParams{
		Amount:         700,
		Commitment:     commitmentHash,
		SpendAuthority: spendAuthority,
		EncryptedNote:  []byte("confidential-note"),
		Confidential:   confidentialStructureOutput(deposit.Output),
		AmountProof:    mustCommitmentAmountProof(t, deposit.Output.Commitment, 700, deposit.Opening.Blinding),
	})
	instruction = mustStructurePrivacyInstruction(t, instruction, err)

	context := newProgramTestContext(t, programID, []structure.PublicKey{sourceAddress, stateAddress}, instruction)
	context.Accounts[sourceAddress] = mustProgramTestAccount(t, 2_000_000_000, nil, structure.DefaultBuiltinProgramIDs.System)
	context.Accounts[stateAddress] = mustProgramTestAccount(t, 2_000_000_000, nil, programID)
	if err := NewProgram(programID).Execute(context); err != nil {
		t.Fatalf("Execute(deposit) error = %v", err)
	}

	state := mustProgramPrivacyState(t, context.Accounts[stateAddress].Data)
	if len(state.Notes) != 1 || state.Notes[0].Confidential == nil {
		t.Fatalf("notes = %+v, want one confidential note", state.Notes)
	}
	if state.PrivacyPoolLamports != 700 || state.UnspentNoteLiability != 700 {
		t.Fatalf("liability pool=%d notes=%d, want 700", state.PrivacyPoolLamports, state.UnspentNoteLiability)
	}
	merkleRoot, err := structure.ComputePrivacyMerkleRoot(state.Notes)
	if err != nil {
		t.Fatalf("ComputePrivacyMerkleRoot() error = %v", err)
	}
	if state.MerkleRoot != merkleRoot {
		t.Fatal("privacy merkle root mismatch")
	}
}

func TestProgramRejectsConfidentialWithdrawWithWrongBalanceProof(t *testing.T) {
	programID := newTestPublicKey(220)
	stateAddress := newTestPublicKey(222)
	destinationAddress := newTestPublicKey(223)
	spendKeyPair := mustConfidentialSpendKeyPair(t)
	spendAuthority := mustConfidentialSpendAuthority(t, spendKeyPair.PublicKey)
	regulatorKeyPair := mustConfidentialRegulatorKeyPair(t)
	deposit := newConfidentialOutputFixture(t, 700, spendAuthority, regulatorKeyPair.PublicKey)
	commitmentHash := mustHashBytes(t, deposit.Output.Commitment)
	state := structure.PrivacyState{
		Version: structure.PrivacyStateStorageVersion,
		Notes: []structure.PrivacyNoteRecord{
			{
				Commitment:     commitmentHash,
				SpendAuthority: spendAuthority,
				VMVersion:      1,
				EncryptedNote:  []byte("confidential-note"),
				Confidential:   confidentialStructureOutput(deposit.Output),
			},
		},
		PrivacyPoolLamports:  700,
		UnspentNoteLiability: 700,
	}
	encodedState := mustMarshalProgramPrivacyState(t, state)
	nullifier := newTestHash(224)
	params := structure.PrivacyWithdrawParams{
		Amount:             701,
		SourceCommitment:   commitmentHash,
		SourceConfidential: deposit.Output.Commitment,
		Nullifier:          nullifier,
		BalanceProof:       mustBalanceProof(t, [][]byte{deposit.Output.Commitment}, nil, 700, deposit.Opening.Blinding),
	}
	proofMessage, err := structure.BuildPrivacyWithdrawProofMessage(1, stateAddress, destinationAddress, params, 50)
	if err != nil {
		t.Fatalf("BuildPrivacyWithdrawProofMessage() error = %v", err)
	}
	instruction, err := structure.NewPrivacyWithdrawInstruction(1, mustConfidentialSpendProof(t, spendKeyPair.PrivateScalar, proofMessage), params)
	instruction = mustStructurePrivacyInstruction(t, instruction, err)
	context := newProgramTestContext(t, programID, []structure.PublicKey{stateAddress, destinationAddress}, instruction)
	context.Accounts[stateAddress] = mustProgramTestAccount(t, 2_000_000_000, encodedState, programID)
	context.Accounts[destinationAddress] = mustProgramTestAccount(t, 2_000_000_000, nil, structure.DefaultBuiltinProgramIDs.System)

	if err := NewProgram(programID).Execute(context); err == nil {
		t.Fatal("Execute(withdraw) accepted wrong balance proof")
	}
	stateAfter := mustProgramPrivacyState(t, context.Accounts[stateAddress].Data)
	if stateAfter.Notes[0].Spent || len(stateAfter.SpentNullifiers) != 0 {
		t.Fatalf("state mutated after rejected withdraw: %+v", stateAfter)
	}
}

func newProgramTestContext(t *testing.T, programID structure.PublicKey, instructionAccounts []structure.PublicKey, instruction structure.PrivacyInstruction) runtime.InstructionContext {
	t.Helper()

	instructionData, err := instruction.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	accountKeys := append([]structure.PublicKey(nil), instructionAccounts...)
	accountKeys = append(accountKeys, programID)
	return runtime.InstructionContext{
		Instruction: structure.CompiledInstruction{
			ProgramIDIndex: uint8(len(accountKeys) - 1),
			AccountIndexes: sequentialProgramAccountIndexes(len(instructionAccounts)),
			Data:           instructionData,
		},
		Message: structure.ResolvedMessage{
			Header:            structure.MessageHeader{NumRequiredSignatures: 1},
			StaticAccountKeys: accountKeys,
			AccountKeys:       accountKeys,
		},
		Accounts:    make(map[structure.PublicKey]structure.Account),
		CurrentSlot: 50,
		RentConfig:  structure.DefaultRentConfig,
	}
}

func sequentialProgramAccountIndexes(count int) []uint8 {
	indexes := make([]uint8, count)
	for index := range indexes {
		indexes[index] = uint8(index)
	}
	return indexes
}

func confidentialStructureOutput(output ConfidentialOutputNote) *structure.PrivacyConfidentialOutput {
	return &structure.PrivacyConfidentialOutput{
		Commitment:       utils.CloneBytes(output.Commitment),
		AmountPublicKey:  utils.CloneBytes(output.AmountPublicKey),
		AmountCiphertext: output.AmountCiphertext,
		AmountProof:      output.AmountProof,
		RangeProof:       output.RangeProof,
	}
}

func mustHashBytes(t *testing.T, value []byte) structure.Hash {
	t.Helper()

	hash, err := structure.NewHash(utils.SHA256(value))
	if err != nil {
		t.Fatalf("NewHash() error = %v", err)
	}
	return hash
}

func mustStructurePrivacyInstruction(t *testing.T, instruction structure.PrivacyInstruction, err error) structure.PrivacyInstruction {
	t.Helper()

	if err != nil {
		t.Fatalf("NewPrivacyInstruction() error = %v", err)
	}
	return instruction
}

func mustProgramTestAccount(t *testing.T, lamports uint64, data []byte, owner structure.PublicKey) structure.Account {
	t.Helper()

	account, err := structure.NewAccount(lamports, data, owner, false, 0)
	if err != nil {
		t.Fatalf("NewAccount() error = %v", err)
	}
	return account
}

func mustMarshalProgramPrivacyState(t *testing.T, state structure.PrivacyState) []byte {
	t.Helper()

	encoded, err := state.MarshalBinary()
	if err != nil {
		t.Fatalf("PrivacyState.MarshalBinary() error = %v", err)
	}
	return encoded
}

func mustProgramPrivacyState(t *testing.T, data []byte) structure.PrivacyState {
	t.Helper()

	state, err := structure.UnmarshalPrivacyStateBinary(data)
	if err != nil {
		t.Fatalf("UnmarshalPrivacyStateBinary() error = %v", err)
	}
	return state
}
