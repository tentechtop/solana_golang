package shielded_test

import (
	"bytes"
	"testing"

	shieldedprogram "solana_golang/programs/shielded"
	"solana_golang/runtime"
	walletshielded "solana_golang/shielded"
	"solana_golang/structure"
)

// TestShieldedTransferModesEndToEnd 连续执行 t→z、z→z、z→t，确保钱包证明与链上状态转换使用同一协议。
func TestShieldedTransferModesEndToEnd(t *testing.T) {
	program, err := shieldedprogram.NewDefaultProgram()
	if err != nil {
		t.Fatalf("NewDefaultProgram() error = %v", err)
	}
	poolState, err := shieldedprogram.DefaultPoolState()
	if err != nil {
		t.Fatalf("DefaultPoolState() error = %v", err)
	}
	walletTree, err := walletshielded.NewCircuitMerkleTree()
	if err != nil {
		t.Fatalf("NewCircuitMerkleTree() error = %v", err)
	}

	senderSpendingKey, senderViewKey, senderAddress := deriveShieldedKeySet(t, 0x11, 0x21)
	_, recipientViewKey, recipientAddress := deriveShieldedKeySet(t, 0x31, 0x41)
	transparentSource := transparentKeyPair(t, 0x51)
	transparentDestination := transparentKeyPair(t, 0x61).PublicKey
	accounts := initialShieldedAccounts(t, poolState, transparentSource)

	const depositedLamports = uint64(10_000_000)
	depositBlockhash := testHash(0x71)
	depositResult, err := walletshielded.BuildTransfer(walletshielded.TransferRequest{
		PoolState: poolState,
		Outputs: []walletshielded.NoteOutput{{
			Address: senderAddress,
			Value:   depositedLamports,
			Memo:    []byte("t-to-z"),
		}},
		TransparentInput: &walletshielded.TransparentInput{
			Account: transparentSource,
			Amount:  depositedLamports,
		},
		RecentBlockhash: depositBlockhash,
		FeeCalculator:   structure.DefaultFeeCalculator(),
		Randomness:      deterministicRandomness(0x81),
	})
	if err != nil {
		t.Fatalf("BuildTransfer(t→z) error = %v", err)
	}
	if len(depositResult.Transaction.Signatures) != 1 {
		t.Fatalf("t→z signature count = %d, want 1", len(depositResult.Transaction.Signatures))
	}
	insufficientDepositAccounts := cloneAddressedAccounts(accounts)
	for accountIndex := range insufficientDepositAccounts {
		if insufficientDepositAccounts[accountIndex].Address != transparentSource.PublicKey {
			continue
		}
		sourceRent, rentErr := structure.DefaultRentConfig.MinimumBalance(len(insufficientDepositAccounts[accountIndex].Account.Data))
		if rentErr != nil {
			t.Fatalf("source MinimumBalance() error = %v", rentErr)
		}
		insufficientDepositAccounts[accountIndex].Account.Lamports = sourceRent + depositResult.FeeLamports + depositedLamports - 1
	}
	insufficientDepositResult := simulateShieldedTransfer(t, program, insufficientDepositAccounts, depositResult, depositBlockhash, 1)
	assertFailedShieldedTransferDidNotChangePool(t, insufficientDepositResult, insufficientDepositAccounts)
	if insufficientDepositResult.ComputeUnitsConsumed != structure.DefaultBuiltinInstructionCU {
		t.Fatalf("insufficient deposit consumed %d compute units, want cheap pre-proof rejection %d", insufficientDepositResult.ComputeUnitsConsumed, structure.DefaultBuiltinInstructionCU)
	}
	accounts, poolState = executeShieldedTransfer(t, program, accounts, depositResult, depositBlockhash, 1)
	if poolState.PoolLamports != depositedLamports {
		t.Fatalf("t→z pool lamports = %d, want %d", poolState.PoolLamports, depositedLamports)
	}
	depositPosition, err := walletTree.Append(depositResult.CreatedNotes[0].Commitment)
	if err != nil {
		t.Fatalf("Append(t→z commitment) error = %v", err)
	}
	assertPoolRootMatchesWallet(t, poolState, walletTree)
	depositInstruction := decodeShieldedInstruction(t, depositResult.Transaction)
	assertScannedNote(t, senderViewKey, depositInstruction.Bundle.Actions[0], depositPosition, depositedLamports)

	depositWitness, err := walletTree.Witness(depositPosition)
	if err != nil {
		t.Fatalf("Witness(t→z note) error = %v", err)
	}
	sponsoredFeePayer := transparentKeyPair(t, 0x64)
	sponsoredTransfer, err := walletshielded.BuildTransfer(walletshielded.TransferRequest{
		PoolState: poolState,
		Inputs: []walletshielded.SpendInput{{
			SpendingKey: senderSpendingKey,
			Address:     senderAddress,
			Note:        depositResult.CreatedNotes[0].Note,
			Witness:     depositWitness,
		}},
		Outputs:             []walletshielded.NoteOutput{{Address: senderAddress, Value: depositedLamports}},
		TransparentFeePayer: &sponsoredFeePayer,
		RecentBlockhash:     testHash(0x74),
		FeeCalculator:       structure.DefaultFeeCalculator(),
		Randomness:          deterministicRandomness(0x84),
	})
	if err != nil {
		t.Fatalf("BuildTransfer(sponsored z→z) error = %v", err)
	}
	if err := program.VerifyTransactionAuthorization(sponsoredTransfer.Transaction, sponsoredTransfer.FeeLamports); err != nil {
		t.Fatalf("VerifyTransactionAuthorization(sponsored z→z) error = %v", err)
	}
	attackerFeePayer := transparentKeyPair(t, 0x65)
	mutatedSponsoredTransaction := sponsoredTransfer.Transaction.Clone()
	mutatedSponsoredTransaction.Signatures = nil
	mutatedSponsoredTransaction.Accounts[0].PublicKey = attackerFeePayer.PublicKey
	mutatedSponsoredTransaction, err = mutatedSponsoredTransaction.Sign(map[structure.PublicKey][]byte{
		attackerFeePayer.PublicKey: attackerFeePayer.PrivateKey,
	})
	if err != nil {
		t.Fatalf("Sign(mutated sponsored z→z) error = %v", err)
	}
	if err := program.VerifyTransactionAuthorization(mutatedSponsoredTransaction, sponsoredTransfer.FeeLamports); err == nil {
		t.Fatal("sponsored proof accepted a substituted transparent fee payer")
	}

	transferBlockhash := testHash(0x72)
	transferFee := shieldedFee(t, 0, 1)
	const recipientLamports = uint64(4_000_000)
	changeLamports := depositedLamports - recipientLamports - transferFee
	shieldedTransferResult, err := walletshielded.BuildTransfer(walletshielded.TransferRequest{
		PoolState: poolState,
		Inputs: []walletshielded.SpendInput{{
			SpendingKey: senderSpendingKey,
			Address:     senderAddress,
			Note:        depositResult.CreatedNotes[0].Note,
			Witness:     depositWitness,
		}},
		Outputs: []walletshielded.NoteOutput{
			{Address: recipientAddress, Value: recipientLamports, Memo: []byte("z-to-z")},
			{Address: senderAddress, Value: changeLamports, Memo: []byte("change")},
		},
		RecentBlockhash: transferBlockhash,
		FeeCalculator:   structure.DefaultFeeCalculator(),
		Randomness:      deterministicRandomness(0x82),
	})
	if err != nil {
		t.Fatalf("BuildTransfer(z→z) error = %v", err)
	}
	if len(shieldedTransferResult.Transaction.Signatures) != 0 {
		t.Fatalf("z→z signature count = %d, want 0", len(shieldedTransferResult.Transaction.Signatures))
	}
	if shieldedTransferResult.FeeLamports != transferFee {
		t.Fatalf("z→z fee = %d, want %d", shieldedTransferResult.FeeLamports, transferFee)
	}
	accountsBeforeShieldedTransfer := cloneAddressedAccounts(accounts)
	invalidProofTransfer := shieldedTransferResult
	invalidProofTransfer.Transaction = shieldedTransferResult.Transaction.Clone()
	invalidProofInstruction := decodeShieldedInstruction(t, invalidProofTransfer.Transaction)
	invalidProofInstruction.Bundle.Proof[0] ^= 0x01
	invalidProofInstructionData, err := invalidProofInstruction.MarshalBinary()
	if err != nil {
		t.Fatalf("invalid proof instruction MarshalBinary() error = %v", err)
	}
	invalidProofTransfer.Transaction.Instructions[0].Data = invalidProofInstructionData
	invalidProofResult := simulateShieldedTransfer(t, program, accountsBeforeShieldedTransfer, invalidProofTransfer, transferBlockhash, 2)
	assertFailedShieldedTransferDidNotChangePool(t, invalidProofResult, accountsBeforeShieldedTransfer)

	accounts, poolState = executeShieldedTransfer(t, program, accounts, shieldedTransferResult, transferBlockhash, 2)
	if poolState.PoolLamports != depositedLamports-transferFee {
		t.Fatalf("z→z pool lamports = %d, want %d", poolState.PoolLamports, depositedLamports-transferFee)
	}
	recipientPosition, err := walletTree.Append(shieldedTransferResult.CreatedNotes[0].Commitment)
	if err != nil {
		t.Fatalf("Append(z→z recipient commitment) error = %v", err)
	}
	changePosition, err := walletTree.Append(shieldedTransferResult.CreatedNotes[1].Commitment)
	if err != nil {
		t.Fatalf("Append(z→z change commitment) error = %v", err)
	}
	assertPoolRootMatchesWallet(t, poolState, walletTree)
	transferInstruction := decodeShieldedInstruction(t, shieldedTransferResult.Transaction)
	assertScannedNote(t, recipientViewKey, transferInstruction.Bundle.Actions[0], recipientPosition, recipientLamports)
	assertScannedNote(t, senderViewKey, transferInstruction.Bundle.Actions[1], changePosition, changeLamports)
	assertNullifierMarkerStored(t, accounts, transferInstruction.Bundle.Actions[0])
	replayResult := simulateShieldedTransfer(t, program, accounts, shieldedTransferResult, transferBlockhash, 2)
	assertFailedShieldedTransferDidNotChangePool(t, replayResult, accounts)

	changeWitness, err := walletTree.Witness(changePosition)
	if err != nil {
		t.Fatalf("Witness(z→z change) error = %v", err)
	}
	withdrawBlockhash := testHash(0x73)
	withdrawFee := shieldedFee(t, 0, 1)
	withdrawLamports := changeLamports - withdrawFee
	withdrawResult, err := walletshielded.BuildTransfer(walletshielded.TransferRequest{
		PoolState: poolState,
		Inputs: []walletshielded.SpendInput{{
			SpendingKey: senderSpendingKey,
			Address:     senderAddress,
			Note:        shieldedTransferResult.CreatedNotes[1].Note,
			Witness:     changeWitness,
		}},
		TransparentOutput: &walletshielded.TransparentOutput{
			Address: transparentDestination,
			Amount:  withdrawLamports,
		},
		RecentBlockhash: withdrawBlockhash,
		FeeCalculator:   structure.DefaultFeeCalculator(),
		Randomness:      deterministicRandomness(0x83),
	})
	if err != nil {
		t.Fatalf("BuildTransfer(z→t) error = %v", err)
	}
	if len(withdrawResult.Transaction.Signatures) != 0 {
		t.Fatalf("z→t signature count = %d, want 0", len(withdrawResult.Transaction.Signatures))
	}
	accounts, poolState = executeShieldedTransfer(t, program, accounts, withdrawResult, withdrawBlockhash, 3)
	if poolState.PoolLamports != recipientLamports {
		t.Fatalf("z→t pool lamports = %d, want remaining recipient note %d", poolState.PoolLamports, recipientLamports)
	}
	if destinationAccount := findAccount(t, accounts, transparentDestination); destinationAccount.Lamports != withdrawLamports {
		t.Fatalf("z→t destination lamports = %d, want %d", destinationAccount.Lamports, withdrawLamports)
	}
	assertPoolRootMatchesWallet(t, poolState, walletTree)
	withdrawInstruction := decodeShieldedInstruction(t, withdrawResult.Transaction)
	assertNullifierMarkerStored(t, accounts, withdrawInstruction.Bundle.Actions[0])
}

func executeShieldedTransfer(
	t *testing.T,
	program shieldedprogram.Program,
	accounts []structure.AddressedAccount,
	transfer walletshielded.TransferResult,
	blockhash structure.Blockhash,
	slot uint64,
) ([]structure.AddressedAccount, structure.ShieldedPoolState) {
	t.Helper()
	if err := program.VerifyTransactionAuthorization(transfer.Transaction, transfer.FeeLamports); err != nil {
		t.Fatalf("VerifyTransactionAuthorization() error = %v", err)
	}
	result := simulateShieldedTransfer(t, program, accounts, transfer, blockhash, slot)
	if result.Status != structure.TransactionStatusConfirmed || result.Error != nil {
		t.Fatalf("Simulate() status = %d, error = %v", result.Status, result.Error)
	}
	if result.FeeDetails.TotalFee != transfer.FeeLamports {
		t.Fatalf("runtime fee = %d, wallet fee = %d", result.FeeDetails.TotalFee, transfer.FeeLamports)
	}
	if result.ComputeUnitsConsumed != structure.ShieldedGroth16VerificationComputeUnits {
		t.Fatalf("shielded compute units = %d, want %d", result.ComputeUnitsConsumed, structure.ShieldedGroth16VerificationComputeUnits)
	}
	accounts = mergeAccounts(accounts, result.WrittenAccounts)
	poolAccount := findAccount(t, accounts, structure.DefaultShieldedPoolAddress)
	poolState, err := structure.UnmarshalShieldedPoolStateBinary(poolAccount.Data)
	if err != nil {
		t.Fatalf("UnmarshalShieldedPoolStateBinary() error = %v", err)
	}
	return accounts, poolState
}

func simulateShieldedTransfer(
	t *testing.T,
	program shieldedprogram.Program,
	accounts []structure.AddressedAccount,
	transfer walletshielded.TransferResult,
	blockhash structure.Blockhash,
	slot uint64,
) structure.TransactionExecutionResult {
	t.Helper()
	queue := structure.NewBlockhashQueue(structure.MaxRecentBlockhashAgeSlots)
	if err := queue.Add(structure.RecentBlockhashEntry{
		Blockhash:     blockhash,
		Slot:          slot,
		FeeCalculator: structure.DefaultFeeCalculator(),
	}); err != nil {
		t.Fatalf("BlockhashQueue.Add() error = %v", err)
	}
	result, err := (runtime.TransactionSimulator{}).Simulate(runtime.TransactionSimulationInput{
		Transaction:    transfer.Transaction,
		Accounts:       accounts,
		BlockhashQueue: queue,
		CurrentSlot:    slot,
		FeeCalculator:  structure.DefaultFeeCalculator(),
		Programs:       []runtime.Program{program},
	})
	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}
	return result
}

func assertFailedShieldedTransferDidNotChangePool(
	t *testing.T,
	result structure.TransactionExecutionResult,
	accountsBefore []structure.AddressedAccount,
) {
	t.Helper()
	if result.Status != structure.TransactionStatusFailed || result.Error == nil {
		t.Fatalf("invalid shielded transfer status = %d, error = %v", result.Status, result.Error)
	}
	poolBefore := findAccount(t, accountsBefore, structure.DefaultShieldedPoolAddress)
	poolAfter := findAccount(t, result.WrittenAccounts, structure.DefaultShieldedPoolAddress)
	if poolBefore.Lamports != poolAfter.Lamports || !bytes.Equal(poolBefore.Data, poolAfter.Data) {
		t.Fatal("failed shielded transfer changed pool state or charged its fee")
	}
}

func initialShieldedAccounts(t *testing.T, poolState structure.ShieldedPoolState, source structure.SolanaKeyPair) []structure.AddressedAccount {
	t.Helper()
	poolData, err := poolState.MarshalBinary()
	if err != nil {
		t.Fatalf("ShieldedPoolState.MarshalBinary() error = %v", err)
	}
	poolReserve, err := structure.DefaultRentConfig.MinimumBalance(shieldedprogram.PoolAccountDataCapacity)
	if err != nil {
		t.Fatalf("pool MinimumBalance() error = %v", err)
	}
	programRent, err := structure.DefaultRentConfig.MinimumBalance(0)
	if err != nil {
		t.Fatalf("program MinimumBalance() error = %v", err)
	}
	const depositedLamports = uint64(10_000_000)
	transparentFee := shieldedFee(t, 1, 0)
	sourceRent := programRent
	sourceAccount := mustAccount(t, sourceRent+depositedLamports+transparentFee+1_000, nil, structure.DefaultBuiltinProgramIDs.System, false)
	poolAccount := mustAccount(t, poolReserve, poolData, structure.DefaultBuiltinProgramIDs.Shielded, false)
	programAccount := mustAccount(t, programRent, nil, structure.DefaultBuiltinProgramIDs.NativeLoader, true)
	return []structure.AddressedAccount{
		{Address: source.PublicKey, Account: sourceAccount},
		{Address: structure.DefaultShieldedPoolAddress, Account: poolAccount},
		{Address: structure.DefaultBuiltinProgramIDs.Shielded, Account: programAccount},
	}
}

func assertScannedNote(
	t *testing.T,
	viewKey walletshielded.ViewKey,
	action structure.ShieldedAction,
	position uint64,
	wantValue uint64,
) {
	t.Helper()
	scanned, owned, err := walletshielded.ScanAction(viewKey, action, position)
	if err != nil {
		t.Fatalf("ScanAction() error = %v", err)
	}
	if !owned {
		t.Fatal("ScanAction() did not recognize wallet-owned output")
	}
	if scanned.Note.Value != wantValue || scanned.Position != position {
		t.Fatalf("scanned note = value %d at %d, want value %d at %d", scanned.Note.Value, scanned.Position, wantValue, position)
	}
}

func assertNullifierMarkerStored(t *testing.T, accounts []structure.AddressedAccount, action structure.ShieldedAction) {
	t.Helper()
	markerAddress := structure.DeriveShieldedNullifierAccount(structure.DefaultBuiltinProgramIDs.Shielded, action.Nullifier)
	markerAccount := findAccount(t, accounts, markerAddress)
	if markerAccount.Owner != structure.DefaultBuiltinProgramIDs.Shielded {
		t.Fatalf("nullifier marker owner = %s, want shielded program", markerAccount.Owner.String())
	}
}

func assertPoolRootMatchesWallet(t *testing.T, poolState structure.ShieldedPoolState, tree *walletshielded.IncrementalMerkleTree) {
	t.Helper()
	walletRoot := tree.Root()
	if !bytes.Equal(poolState.CurrentRoot[:], walletRoot[:]) {
		t.Fatalf("pool root %x does not match wallet root %x", poolState.CurrentRoot, walletRoot)
	}
	if poolState.LeafCount != tree.LeafCount() {
		t.Fatalf("pool leaf count = %d, wallet leaf count = %d", poolState.LeafCount, tree.LeafCount())
	}
}

func decodeShieldedInstruction(t *testing.T, transaction structure.Transaction) structure.ShieldedTransferInstruction {
	t.Helper()
	if len(transaction.Instructions) != 1 {
		t.Fatalf("instruction count = %d, want 1", len(transaction.Instructions))
	}
	instruction, err := structure.UnmarshalShieldedTransferInstructionBinary(transaction.Instructions[0].Data)
	if err != nil {
		t.Fatalf("UnmarshalShieldedTransferInstructionBinary() error = %v", err)
	}
	return instruction
}

func shieldedFee(t *testing.T, signatureCount int, spendCount int) uint64 {
	t.Helper()
	budget := structure.DefaultComputeBudgetLimits()
	budget.MaxComputeUnits = structure.ShieldedGroth16VerificationComputeUnits
	budget.StorageWriteBytesLimit = structure.ShieldedPoolStateWriteChargeBytes + uint64(spendCount)*structure.ShieldedNullifierStateWriteChargeBytes
	details, err := structure.DefaultFeeCalculator().Calculate(signatureCount, budget)
	if err != nil {
		t.Fatalf("FeeCalculator.Calculate() error = %v", err)
	}
	return details.TotalFee
}

func deriveShieldedKeySet(t *testing.T, seedByte byte, diversifierByte byte) (walletshielded.SpendingKey, walletshielded.ViewKey, walletshielded.ShieldedAddress) {
	t.Helper()
	diversifier, err := walletshielded.NewDiversifier(bytes.Repeat([]byte{diversifierByte}, walletshielded.DiversifierSize))
	if err != nil {
		t.Fatalf("NewDiversifier() error = %v", err)
	}
	spendingKey, viewKey, address, err := walletshielded.DeriveKeySet(bytes.Repeat([]byte{seedByte}, walletshielded.SeedSize), diversifier)
	if err != nil {
		t.Fatalf("DeriveKeySet() error = %v", err)
	}
	return spendingKey, viewKey, address
}

func transparentKeyPair(t *testing.T, seedByte byte) structure.SolanaKeyPair {
	t.Helper()
	keyPair, err := structure.KeyPairFromSeed(bytes.Repeat([]byte{seedByte}, structure.SolanaPrivateKeySeedSize))
	if err != nil {
		t.Fatalf("KeyPairFromSeed() error = %v", err)
	}
	return keyPair
}

func deterministicRandomness(value byte) *bytes.Reader {
	return bytes.NewReader(bytes.Repeat([]byte{value}, 4*1024))
}

func testHash(value byte) structure.Hash {
	var hash structure.Hash
	for index := range hash {
		hash[index] = value
	}
	return hash
}

func mustAccount(t *testing.T, lamports uint64, data []byte, owner structure.PublicKey, executable bool) structure.Account {
	t.Helper()
	account, err := structure.NewAccount(lamports, data, owner, executable, 0)
	if err != nil {
		t.Fatalf("NewAccount() error = %v", err)
	}
	return account
}

func mergeAccounts(current []structure.AddressedAccount, written []structure.AddressedAccount) []structure.AddressedAccount {
	merged := make([]structure.AddressedAccount, len(current))
	copy(merged, current)
	indexByAddress := make(map[structure.PublicKey]int, len(merged))
	for accountIndex, addressedAccount := range merged {
		indexByAddress[addressedAccount.Address] = accountIndex
	}
	for _, addressedAccount := range written {
		if accountIndex, exists := indexByAddress[addressedAccount.Address]; exists {
			merged[accountIndex] = addressedAccount
			continue
		}
		indexByAddress[addressedAccount.Address] = len(merged)
		merged = append(merged, addressedAccount)
	}
	return merged
}

func cloneAddressedAccounts(accounts []structure.AddressedAccount) []structure.AddressedAccount {
	cloned := make([]structure.AddressedAccount, len(accounts))
	for accountIndex, account := range accounts {
		cloned[accountIndex] = account.Clone()
	}
	return cloned
}

func findAccount(t *testing.T, accounts []structure.AddressedAccount, address structure.PublicKey) structure.Account {
	t.Helper()
	for _, addressedAccount := range accounts {
		if addressedAccount.Address == address {
			return addressedAccount.Account
		}
	}
	t.Fatalf("account %s not found", address.String())
	return structure.Account{}
}
