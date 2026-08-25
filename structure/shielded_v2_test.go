package structure

import (
	"bytes"
	"encoding/binary"
	"errors"
	"reflect"
	"testing"
)

func TestShieldedV2BorshRoundTrip(t *testing.T) {
	action := newValidShieldedAction()
	actionBytes, err := action.MarshalBinary()
	if err != nil {
		t.Fatalf("ShieldedAction.MarshalBinary() error = %v", err)
	}
	decodedAction, err := UnmarshalShieldedActionBinary(actionBytes)
	if err != nil {
		t.Fatalf("UnmarshalShieldedActionBinary() error = %v", err)
	}
	if !reflect.DeepEqual(decodedAction, action) {
		t.Fatalf("shielded action round trip mismatch\n got: %#v\nwant: %#v", decodedAction, action)
	}

	bundle := newValidShieldedBundle()
	bundleBytes, err := bundle.MarshalBinary()
	if err != nil {
		t.Fatalf("ShieldedBundle.MarshalBinary() error = %v", err)
	}
	decodedBundle, err := UnmarshalShieldedBundleBinary(bundleBytes)
	if err != nil {
		t.Fatalf("UnmarshalShieldedBundleBinary() error = %v", err)
	}
	if !reflect.DeepEqual(decodedBundle, bundle) {
		t.Fatalf("shielded bundle round trip mismatch\n got: %#v\nwant: %#v", decodedBundle, bundle)
	}

	poolState := newValidShieldedPoolState()
	poolStateBytes, err := poolState.MarshalBinary()
	if err != nil {
		t.Fatalf("ShieldedPoolState.MarshalBinary() error = %v", err)
	}
	decodedPoolState, err := UnmarshalShieldedPoolStateBinary(poolStateBytes)
	if err != nil {
		t.Fatalf("UnmarshalShieldedPoolStateBinary() error = %v", err)
	}
	if !reflect.DeepEqual(decodedPoolState, poolState) {
		t.Fatalf("shielded pool state round trip mismatch\n got: %#v\nwant: %#v", decodedPoolState, poolState)
	}

	instruction := newValidShieldedTransferInstruction()
	instructionBytes, err := instruction.MarshalBinary()
	if err != nil {
		t.Fatalf("ShieldedTransferInstruction.MarshalBinary() error = %v", err)
	}
	decodedInstruction, err := UnmarshalShieldedTransferInstructionBinary(instructionBytes)
	if err != nil {
		t.Fatalf("UnmarshalShieldedTransferInstructionBinary() error = %v", err)
	}
	if !reflect.DeepEqual(decodedInstruction, instruction) {
		t.Fatalf("shielded instruction round trip mismatch\n got: %#v\nwant: %#v", decodedInstruction, instruction)
	}
}

func TestShieldedV2DecodersRejectTrailingBytes(t *testing.T) {
	actionBytes, err := newValidShieldedAction().MarshalBinary()
	if err != nil {
		t.Fatalf("ShieldedAction.MarshalBinary() error = %v", err)
	}
	bundleBytes, err := newValidShieldedBundle().MarshalBinary()
	if err != nil {
		t.Fatalf("ShieldedBundle.MarshalBinary() error = %v", err)
	}
	poolStateBytes, err := newValidShieldedPoolState().MarshalBinary()
	if err != nil {
		t.Fatalf("ShieldedPoolState.MarshalBinary() error = %v", err)
	}
	instructionBytes, err := newValidShieldedTransferInstruction().MarshalBinary()
	if err != nil {
		t.Fatalf("ShieldedTransferInstruction.MarshalBinary() error = %v", err)
	}

	tests := []struct {
		name    string
		encoded []byte
		decode  func([]byte) error
	}{
		{
			name:    "action",
			encoded: actionBytes,
			decode: func(encoded []byte) error {
				_, decodeErr := UnmarshalShieldedActionBinary(encoded)
				return decodeErr
			},
		},
		{
			name:    "bundle",
			encoded: bundleBytes,
			decode: func(encoded []byte) error {
				_, decodeErr := UnmarshalShieldedBundleBinary(encoded)
				return decodeErr
			},
		},
		{
			name:    "pool_state",
			encoded: poolStateBytes,
			decode: func(encoded []byte) error {
				_, decodeErr := UnmarshalShieldedPoolStateBinary(encoded)
				return decodeErr
			},
		},
		{
			name:    "instruction",
			encoded: instructionBytes,
			decode: func(encoded []byte) error {
				_, decodeErr := UnmarshalShieldedTransferInstructionBinary(encoded)
				return decodeErr
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			malformed := append(bytes.Clone(test.encoded), 0xff)
			if err := test.decode(malformed); err == nil {
				t.Fatal("decoder accepted trailing byte")
			}
		})
	}
}

func TestShieldedV2DecodersRejectContainerBounds(t *testing.T) {
	actionBytes, err := newValidShieldedAction().MarshalBinary()
	if err != nil {
		t.Fatalf("ShieldedAction.MarshalBinary() error = %v", err)
	}
	oversizedEphemeralKey := bytes.Clone(actionBytes)
	const shieldedActionEphemeralKeyLengthOffset = 2 + HashSize + HashSize
	binary.LittleEndian.PutUint32(
		oversizedEphemeralKey[shieldedActionEphemeralKeyLengthOffset:],
		uint32(ShieldedEphemeralKeySize+1),
	)
	if _, err := UnmarshalShieldedActionBinary(oversizedEphemeralKey); !errors.Is(err, ErrInvalidShieldedAction) {
		t.Fatalf("UnmarshalShieldedActionBinary(oversized ephemeral key) error = %v, want ErrInvalidShieldedAction", err)
	}

	bundleBytes, err := newValidShieldedBundle().MarshalBinary()
	if err != nil {
		t.Fatalf("ShieldedBundle.MarshalBinary() error = %v", err)
	}
	oversizedActions := bytes.Clone(bundleBytes)
	const shieldedBundleActionCountOffset = 2 + 4 + HashSize
	binary.LittleEndian.PutUint32(
		oversizedActions[shieldedBundleActionCountOffset:],
		uint32(MaxShieldedActionsPerBundle+1),
	)
	if _, err := UnmarshalShieldedBundleBinary(oversizedActions); !errors.Is(err, ErrInvalidShieldedBundle) {
		t.Fatalf("UnmarshalShieldedBundleBinary(oversized actions) error = %v, want ErrInvalidShieldedBundle", err)
	}

	poolStateBytes, err := newValidShieldedPoolState().MarshalBinary()
	if err != nil {
		t.Fatalf("ShieldedPoolState.MarshalBinary() error = %v", err)
	}
	oversizedFrontier := bytes.Clone(poolStateBytes)
	const shieldedPoolFrontierCountOffset = 2 + 2 + 4 + HashSize + 1 + 8
	binary.LittleEndian.PutUint32(
		oversizedFrontier[shieldedPoolFrontierCountOffset:],
		uint32(MaxShieldedTreeDepth+1),
	)
	if _, err := UnmarshalShieldedPoolStateBinary(oversizedFrontier); !errors.Is(err, ErrInvalidShieldedPoolState) {
		t.Fatalf("UnmarshalShieldedPoolStateBinary(oversized frontier) error = %v, want ErrInvalidShieldedPoolState", err)
	}

	instructionBytes, err := newValidShieldedTransferInstruction().MarshalBinary()
	if err != nil {
		t.Fatalf("ShieldedTransferInstruction.MarshalBinary() error = %v", err)
	}
	oversizedBundle := bytes.Clone(instructionBytes)
	const shieldedInstructionBundleLengthOffset = 2 + 8 + 8
	binary.LittleEndian.PutUint32(
		oversizedBundle[shieldedInstructionBundleLengthOffset:],
		uint32(MaxShieldedInstructionBytes+1),
	)
	if _, err := UnmarshalShieldedTransferInstructionBinary(oversizedBundle); err == nil {
		t.Fatal("UnmarshalShieldedTransferInstructionBinary() accepted oversized bundle")
	}
}

func TestShieldedBundleRejectsDuplicateNullifier(t *testing.T) {
	firstAction := ShieldedAction{
		EnabledSpend: true,
		Nullifier:    newTestHash(41),
	}
	secondAction := ShieldedAction{
		EnabledSpend: true,
		Nullifier:    newTestHash(73),
	}
	bundle := newValidShieldedBundle()
	bundle.Actions = []ShieldedAction{firstAction, secondAction}

	encoded, err := bundle.MarshalBinary()
	if err != nil {
		t.Fatalf("ShieldedBundle.MarshalBinary() error = %v", err)
	}
	firstActionBytes, err := firstAction.MarshalBinary()
	if err != nil {
		t.Fatalf("ShieldedAction.MarshalBinary() error = %v", err)
	}
	const shieldedBundleActionsOffset = 2 + 4 + HashSize + 4
	secondNullifierOffset := shieldedBundleActionsOffset + len(firstActionBytes) + 2
	copy(encoded[secondNullifierOffset:secondNullifierOffset+HashSize], firstAction.Nullifier[:])

	if _, err := UnmarshalShieldedBundleBinary(encoded); !errors.Is(err, ErrInvalidShieldedBundle) {
		t.Fatalf("UnmarshalShieldedBundleBinary(duplicate nullifier) error = %v, want ErrInvalidShieldedBundle", err)
	}
	bundle.Actions[1].Nullifier = bundle.Actions[0].Nullifier
	if err := bundle.Validate(); !errors.Is(err, ErrInvalidShieldedBundle) {
		t.Fatalf("ShieldedBundle.Validate(duplicate nullifier) error = %v, want ErrInvalidShieldedBundle", err)
	}
}

func TestUnsignedShieldedTransactionRoundTripAndStableTxID(t *testing.T) {
	transaction := newCanonicalUnsignedShieldedTransaction(t, 0, PublicKey{})

	if err := transaction.Validate(); err != nil {
		t.Fatalf("Transaction.Validate() error = %v", err)
	}
	if !transaction.IsUnsigned() {
		t.Fatal("Transaction.IsUnsigned() = false, want true")
	}
	transactionID, err := transaction.TxID()
	if err != nil {
		t.Fatalf("Transaction.TxID() error = %v", err)
	}
	if transactionID == (Signature{}) {
		t.Fatal("Transaction.TxID() returned zero ID")
	}
	secondTransactionID, err := transaction.TxID()
	if err != nil {
		t.Fatalf("Transaction.TxID(second call) error = %v", err)
	}
	if !transactionID.Equal(secondTransactionID) {
		t.Fatal("Transaction.TxID() is not stable across calls")
	}

	encoded, err := transaction.MarshalBinary()
	if err != nil {
		t.Fatalf("Transaction.MarshalBinary() error = %v", err)
	}
	decoded, err := UnmarshalTransactionBinary(encoded)
	if err != nil {
		t.Fatalf("UnmarshalTransactionBinary() error = %v", err)
	}
	if !decoded.IsUnsigned() {
		t.Fatal("decoded Transaction.IsUnsigned() = false, want true")
	}
	decodedTransactionID, err := decoded.TxID()
	if err != nil {
		t.Fatalf("decoded Transaction.TxID() error = %v", err)
	}
	if !transactionID.Equal(decodedTransactionID) {
		t.Fatal("unsigned transaction ID changed after wire round trip")
	}
	reencoded, err := decoded.MarshalBinary()
	if err != nil {
		t.Fatalf("decoded Transaction.MarshalBinary() error = %v", err)
	}
	if !bytes.Equal(reencoded, encoded) {
		t.Fatal("unsigned shielded transaction wire bytes changed after round trip")
	}
}

func TestUnsignedShieldedTransactionIDExcludesRerandomizableProof(t *testing.T) {
	transaction := newCanonicalUnsignedShieldedTransaction(t, 0, PublicKey{})
	originalTransactionID, err := transaction.TxID()
	if err != nil {
		t.Fatalf("Transaction.TxID() error = %v", err)
	}
	message, err := transaction.SolanaMessage()
	if err != nil {
		t.Fatalf("Transaction.SolanaMessage() error = %v", err)
	}
	instruction, err := UnmarshalShieldedTransferInstructionBinary(message.Instructions[0].Data)
	if err != nil {
		t.Fatalf("UnmarshalShieldedTransferInstructionBinary() error = %v", err)
	}
	for proofIndex := range instruction.Bundle.Proof {
		instruction.Bundle.Proof[proofIndex] ^= byte(0x5a + proofIndex%31)
	}
	message.Instructions[0].Data, err = instruction.MarshalBinary()
	if err != nil {
		t.Fatalf("ShieldedTransferInstruction.MarshalBinary() error = %v", err)
	}
	proofVariant := Transaction{Message: message}
	if err := proofVariant.Validate(); err != nil {
		t.Fatalf("Transaction.Validate(proof variant) error = %v", err)
	}
	proofVariantTransactionID, err := proofVariant.TxID()
	if err != nil {
		t.Fatalf("Transaction.TxID(proof variant) error = %v", err)
	}
	if !originalTransactionID.Equal(proofVariantTransactionID) {
		t.Fatal("unsigned shielded transaction ID changed when only the Groth16 proof changed")
	}
}

func TestUnsignedShieldedTransactionIDBindsPublicAuthorization(t *testing.T) {
	baseTransaction := newCanonicalUnsignedShieldedTransaction(t, 0, PublicKey{})
	baseMessage, err := baseTransaction.SolanaMessage()
	if err != nil {
		t.Fatalf("Transaction.SolanaMessage() error = %v", err)
	}
	baseTransactionID, err := baseTransaction.TxID()
	if err != nil {
		t.Fatalf("Transaction.TxID() error = %v", err)
	}

	testCases := []struct {
		name   string
		mutate func(*SolanaMessage, *ShieldedTransferInstruction)
	}{
		{
			name: "recent blockhash",
			mutate: func(message *SolanaMessage, _ *ShieldedTransferInstruction) {
				message.RecentBlockhash = newTestHash(139)
			},
		},
		{
			name: "anchor",
			mutate: func(_ *SolanaMessage, instruction *ShieldedTransferInstruction) {
				instruction.Bundle.Anchor = newTestHash(140)
			},
		},
		{
			name: "nullifier",
			mutate: func(message *SolanaMessage, instruction *ShieldedTransferInstruction) {
				instruction.Bundle.Actions[0].Nullifier = newTestHash(142)
				message.AccountKeys[1] = DeriveShieldedNullifierAccount(DefaultBuiltinProgramIDs.Shielded, instruction.Bundle.Actions[0].Nullifier)
			},
		},
		{
			name: "note commitment",
			mutate: func(_ *SolanaMessage, instruction *ShieldedTransferInstruction) {
				instruction.Bundle.Actions[0].NoteCommitment = newTestHash(144)
			},
		},
		{
			name: "encrypted note",
			mutate: func(_ *SolanaMessage, instruction *ShieldedTransferInstruction) {
				instruction.Bundle.Actions[0].EncryptedNote[0] ^= 0x01
			},
		},
		{
			name: "value balance",
			mutate: func(_ *SolanaMessage, instruction *ShieldedTransferInstruction) {
				instruction.Bundle.ValueBalance++
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			message := baseMessage.Clone()
			instruction, decodeErr := UnmarshalShieldedTransferInstructionBinary(message.Instructions[0].Data)
			if decodeErr != nil {
				t.Fatalf("UnmarshalShieldedTransferInstructionBinary() error = %v", decodeErr)
			}
			testCase.mutate(&message, &instruction)
			message.Instructions[0].Data, decodeErr = instruction.MarshalBinary()
			if decodeErr != nil {
				t.Fatalf("ShieldedTransferInstruction.MarshalBinary() error = %v", decodeErr)
			}
			variant := Transaction{Message: message}
			if err := variant.Validate(); err != nil {
				t.Fatalf("Transaction.Validate() error = %v", err)
			}
			variantTransactionID, txIDErr := variant.TxID()
			if txIDErr != nil {
				t.Fatalf("Transaction.TxID() error = %v", txIDErr)
			}
			if baseTransactionID.Equal(variantTransactionID) {
				t.Fatal("unsigned shielded transaction ID did not bind changed public authorization")
			}
		})
	}
}

func TestUnsignedShieldedTransactionAcceptsCanonicalWithdrawalShape(t *testing.T) {
	transparentTarget := newTestPublicKey(137)
	transaction := newCanonicalUnsignedShieldedTransaction(t, 100_000, transparentTarget)
	if err := transaction.Validate(); err != nil {
		t.Fatalf("Transaction.Validate(canonical withdrawal) error = %v", err)
	}
	message, err := transaction.SolanaMessage()
	if err != nil {
		t.Fatalf("Transaction.SolanaMessage() error = %v", err)
	}
	if len(message.AccountKeys) != 4 || message.AccountKeys[1] != transparentTarget {
		t.Fatalf("canonical withdrawal accounts = %v, want target at index 1", message.AccountKeys)
	}
}

func TestUnsignedShieldedTransactionRejectsMessageMalleability(t *testing.T) {
	baseTransaction := newCanonicalUnsignedShieldedTransaction(t, 0, PublicKey{})
	baseMessage, err := baseTransaction.SolanaMessage()
	if err != nil {
		t.Fatalf("Transaction.SolanaMessage() error = %v", err)
	}

	testCases := []struct {
		name   string
		mutate func(*SolanaMessage)
	}{
		{
			name: "address table",
			mutate: func(message *SolanaMessage) {
				message.UsesAddressTable = true
				message.AddressTableLookups = []MessageAddressTableLookup{{AccountKey: newTestPublicKey(141)}}
			},
		},
		{
			name: "unused writable account",
			mutate: func(message *SolanaMessage) {
				programIndex := int(message.Instructions[0].ProgramIDIndex)
				message.AccountKeys = append(message.AccountKeys, PublicKey{})
				copy(message.AccountKeys[programIndex+1:], message.AccountKeys[programIndex:])
				message.AccountKeys[programIndex] = newTestPublicKey(143)
				message.Instructions[0].ProgramIDIndex++
			},
		},
		{
			name: "unused readonly account",
			mutate: func(message *SolanaMessage) {
				message.AccountKeys = append(message.AccountKeys, newTestPublicKey(145))
				message.Header.NumReadonlyUnsignedAccounts++
			},
		},
		{
			name: "reordered message accounts",
			mutate: func(message *SolanaMessage) {
				message.AccountKeys[0], message.AccountKeys[1] = message.AccountKeys[1], message.AccountKeys[0]
				message.Instructions[0].AccountIndexes[0], message.Instructions[0].AccountIndexes[1] = 1, 0
			},
		},
		{
			name: "reordered instruction accounts",
			mutate: func(message *SolanaMessage) {
				message.Instructions[0].AccountIndexes[0], message.Instructions[0].AccountIndexes[1] = 1, 0
			},
		},
		{
			name: "noncanonical permissions",
			mutate: func(message *SolanaMessage) {
				message.Header.NumReadonlyUnsignedAccounts = 0
			},
		},
		{
			name: "wrong nullifier marker",
			mutate: func(message *SolanaMessage) {
				message.AccountKeys[1] = newTestPublicKey(147)
			},
		},
		{
			name: "transparent input without signer",
			mutate: func(message *SolanaMessage) {
				instruction, decodeErr := UnmarshalShieldedTransferInstructionBinary(message.Instructions[0].Data)
				if decodeErr != nil {
					t.Fatalf("UnmarshalShieldedTransferInstructionBinary() error = %v", decodeErr)
				}
				instruction.TransparentInputAmount = 1
				message.Instructions[0].Data, decodeErr = instruction.MarshalBinary()
				if decodeErr != nil {
					t.Fatalf("ShieldedTransferInstruction.MarshalBinary() error = %v", decodeErr)
				}
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			message := baseMessage.Clone()
			testCase.mutate(&message)
			transaction := Transaction{Message: message}
			if err := transaction.Validate(); err == nil {
				t.Fatal("Transaction.Validate() accepted a malleable unsigned shielded message")
			}
			if _, err := transaction.TxID(); err == nil {
				t.Fatal("Transaction.TxID() accepted a malleable unsigned shielded message")
			}
		})
	}
}

func TestUnsignedOrdinaryTransactionIsRejected(t *testing.T) {
	message := SolanaMessage{
		Header: MessageHeader{
			NumReadonlyUnsignedAccounts: 1,
		},
		AccountKeys: []PublicKey{
			newTestPublicKey(151),
			DefaultBuiltinProgramIDs.System,
		},
		RecentBlockhash: newTestHash(173),
		Instructions: []CompiledInstruction{
			{
				ProgramIDIndex: 1,
				AccountIndexes: []uint8{0},
				Data:           []byte{1},
			},
		},
	}
	transaction := Transaction{Message: message}
	if err := transaction.Validate(); !errors.Is(err, ErrMissingRequiredSignature) {
		t.Fatalf("Transaction.Validate(unsigned ordinary transaction) error = %v, want ErrMissingRequiredSignature", err)
	}

	messageBytes, err := message.MarshalBinary()
	if err != nil {
		t.Fatalf("SolanaMessage.MarshalBinary() error = %v", err)
	}
	forgedWireTransaction := append([]byte{0}, messageBytes...)
	if _, err := UnmarshalTransactionBinary(forgedWireTransaction); !errors.Is(err, ErrMissingRequiredSignature) {
		t.Fatalf("UnmarshalTransactionBinary(unsigned ordinary transaction) error = %v, want ErrMissingRequiredSignature", err)
	}
}

func TestShieldedFeeBudgetChargesPersistentStateGrowth(t *testing.T) {
	transaction := newCanonicalUnsignedShieldedTransaction(t, 0, PublicKey{})
	budget, err := EstimateTransactionComputeBudget(transaction, DefaultBuiltinProgramIDs)
	if err != nil {
		t.Fatalf("EstimateTransactionComputeBudget() error = %v", err)
	}
	wantStorageBytes := ShieldedPoolStateWriteChargeBytes + ShieldedNullifierStateWriteChargeBytes
	if budget.MaxComputeUnits != ShieldedGroth16VerificationComputeUnits || budget.StorageWriteBytesLimit != wantStorageBytes {
		t.Fatalf("shielded budget = compute %d, storage %d; want %d/%d", budget.MaxComputeUnits, budget.StorageWriteBytesLimit, ShieldedGroth16VerificationComputeUnits, wantStorageBytes)
	}
	fee, err := DefaultFeeCalculator().Calculate(0, budget)
	if err != nil {
		t.Fatalf("FeeCalculator.Calculate() error = %v", err)
	}
	wantStorageFee := wantStorageBytes * DefaultStorageWriteLamportsPerByte
	if fee.StorageWriteFee != wantStorageFee || fee.TotalFee <= fee.ComputeFee {
		t.Fatalf("shielded fee = %+v, want storage fee %d", fee, wantStorageFee)
	}

	outputOnlyAction := newValidShieldedAction()
	outputOnlyAction.EnabledSpend = false
	outputOnlyAction.Nullifier = Hash{}
	outputOnlyBytes, err := ShieldedStorageWriteChargeBytes([]ShieldedAction{outputOnlyAction})
	if err != nil {
		t.Fatalf("ShieldedStorageWriteChargeBytes(output only) error = %v", err)
	}
	if outputOnlyBytes != ShieldedPoolStateWriteChargeBytes {
		t.Fatalf("output-only storage charge = %d, want %d", outputOnlyBytes, ShieldedPoolStateWriteChargeBytes)
	}
}

func TestDeriveShieldedNullifierAccountIsDeterministic(t *testing.T) {
	programID := DefaultBuiltinProgramIDs.Shielded
	nullifier := newTestHash(199)

	first := DeriveShieldedNullifierAccount(programID, nullifier)
	second := DeriveShieldedNullifierAccount(programID, nullifier)
	if first.IsZero() {
		t.Fatal("DeriveShieldedNullifierAccount() returned zero address")
	}
	if !first.Equal(second) {
		t.Fatal("DeriveShieldedNullifierAccount() returned different addresses for the same input")
	}
	if first.Equal(DeriveShieldedNullifierAccount(programID, newTestHash(200))) {
		t.Fatal("DeriveShieldedNullifierAccount() did not separate nullifiers")
	}
	if first.Equal(DeriveShieldedNullifierAccount(newTestPublicKey(211), nullifier)) {
		t.Fatal("DeriveShieldedNullifierAccount() did not separate program IDs")
	}
}

func newValidShieldedAction() ShieldedAction {
	return ShieldedAction{
		EnabledSpend:   true,
		EnabledOutput:  true,
		Nullifier:      newTestHash(11),
		NoteCommitment: newTestHash(23),
		EphemeralKey:   newShieldedTestBytes(ShieldedEphemeralKeySize, 37),
		EncryptedNote:  newShieldedTestBytes(96, 59),
	}
}

func newValidShieldedBundle() ShieldedBundle {
	return ShieldedBundle{
		ProtocolVersion:   ShieldedProtocolVersion,
		ConsensusBranchID: ShieldedConsensusBranchID,
		Anchor:            newTestHash(71),
		Actions:           []ShieldedAction{newValidShieldedAction()},
		ValueBalance:      -12345,
		Proof:             newShieldedTestBytes(ShieldedGroth16ProofBytes, 83),
	}
}

func newValidShieldedPoolState() ShieldedPoolState {
	currentRoot := newTestHash(97)
	return ShieldedPoolState{
		StateEncodingVersion: ShieldedPoolStateEncodingVersion,
		ProtocolVersion:      ShieldedProtocolVersion,
		CircuitID:            0x53483201,
		VerifyingKeyHash:     newTestHash(101),
		TreeDepth:            3,
		LeafCount:            1,
		Frontier: []Hash{
			newTestHash(103),
			{},
			{},
		},
		CurrentRoot:   currentRoot,
		RecentAnchors: []Hash{currentRoot, newTestHash(107)},
		PoolLamports:  5_000_000,
	}
}

func newValidShieldedTransferInstruction() ShieldedTransferInstruction {
	return ShieldedTransferInstruction{
		EncodingVersion:         ShieldedInstructionEncodingVersion,
		TransparentInputAmount:  500_000,
		TransparentOutputAmount: 100_000,
		Bundle:                  newValidShieldedBundle(),
	}
}

func newCanonicalUnsignedShieldedTransaction(t *testing.T, transparentOutputAmount uint64, transparentTarget PublicKey) Transaction {
	t.Helper()
	instruction := newValidShieldedTransferInstruction()
	instruction.TransparentInputAmount = 0
	instruction.TransparentOutputAmount = transparentOutputAmount
	instructionData, err := instruction.MarshalBinary()
	if err != nil {
		t.Fatalf("ShieldedTransferInstruction.MarshalBinary() error = %v", err)
	}

	markerAddress := DeriveShieldedNullifierAccount(DefaultBuiltinProgramIDs.Shielded, instruction.Bundle.Actions[0].Nullifier)
	accounts := []AccountMeta{{PublicKey: DefaultShieldedPoolAddress, IsWritable: true}}
	instructionAccounts := []PublicKey{DefaultShieldedPoolAddress}
	if transparentOutputAmount > 0 {
		accounts = append(accounts, AccountMeta{PublicKey: transparentTarget, IsWritable: true})
		instructionAccounts = append(instructionAccounts, transparentTarget)
	}
	accounts = append(accounts,
		AccountMeta{PublicKey: markerAddress, IsWritable: true},
		AccountMeta{PublicKey: DefaultBuiltinProgramIDs.Shielded},
	)
	instructionAccounts = append(instructionAccounts, markerAddress)
	accountIndexes, err := AccountIndexMap(accounts)
	if err != nil {
		t.Fatalf("AccountIndexMap() error = %v", err)
	}
	compiledInstruction, err := CompileInstruction(
		DefaultBuiltinProgramIDs.Shielded,
		instructionAccounts,
		instructionData,
		accountIndexes,
	)
	if err != nil {
		t.Fatalf("CompileInstruction() error = %v", err)
	}
	return Transaction{
		Accounts:        accounts,
		Instructions:    []CompiledInstruction{compiledInstruction},
		RecentBlockhash: newTestHash(111),
	}
}

func newShieldedTestBytes(length int, seed byte) []byte {
	value := make([]byte, length)
	for index := range value {
		value[index] = seed + byte(index%127)
	}
	return value
}
