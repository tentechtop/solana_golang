package posnode

import (
	"bytes"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/tentechtop/ordexus-core/blockchain"
	"github.com/tentechtop/ordexus-core/consensus"
	walletshielded "github.com/tentechtop/ordexus-core/shielded"
	"github.com/tentechtop/ordexus-core/structure"
)

func TestAddTransactionAcceptsValidSignedShieldedDeposit(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	executor, err := newRuntimeExecutor(logger)
	if err != nil {
		t.Fatalf("newRuntimeExecutor() error = %v", err)
	}
	transparentSource := mustStructureKeyPair("signed-shielded-valid-source")
	ledger, err := blockchain.NewLedgerFromGenesis(nil, blockchain.GenesisConfig{
		ChainID: "signed-shielded-valid-test",
		FundedAccounts: []blockchain.GenesisAccount{{
			Address:  transparentSource.PublicKey,
			Lamports: 10_000_000,
		}},
	})
	if err != nil {
		t.Fatalf("NewLedgerFromGenesis() error = %v", err)
	}
	poolAccount, found, err := ledger.Account(structure.DefaultShieldedPoolAddress)
	if err != nil || !found {
		t.Fatalf("Ledger.Account(pool) found = %t, error = %v", found, err)
	}
	poolState, err := structure.UnmarshalShieldedPoolStateBinary(poolAccount.Data)
	if err != nil {
		t.Fatalf("UnmarshalShieldedPoolStateBinary() error = %v", err)
	}
	diversifier, err := walletshielded.NewDiversifier(bytes.Repeat([]byte{0x61}, walletshielded.DiversifierSize))
	if err != nil {
		t.Fatalf("NewDiversifier() error = %v", err)
	}
	_, _, shieldedAddress, err := walletshielded.DeriveKeySet(bytes.Repeat([]byte{0x62}, walletshielded.SeedSize), diversifier)
	if err != nil {
		t.Fatalf("DeriveKeySet() error = %v", err)
	}

	node := newMempoolTestNode(10, 60_000)
	node.logger = logger
	node.executor = executor
	node.ledger = ledger
	head := ledger.Head()
	if err := node.blockhashQueue.Add(structure.RecentBlockhashEntry{
		Blockhash:     head.BlockHash,
		Slot:          head.Slot,
		FeeCalculator: structure.DefaultFeeCalculator(),
	}); err != nil {
		t.Fatalf("BlockhashQueue.Add() error = %v", err)
	}
	transfer, err := walletshielded.BuildTransfer(walletshielded.TransferRequest{
		PoolState: poolState,
		Outputs: []walletshielded.NoteOutput{{
			Address: shieldedAddress,
			Value:   1_000_000,
		}},
		TransparentInput: &walletshielded.TransparentInput{
			Account: transparentSource,
			Amount:  1_000_000,
		},
		RecentBlockhash: head.BlockHash,
		FeeCalculator:   structure.DefaultFeeCalculator(),
		Randomness:      bytes.NewReader(bytes.Repeat([]byte{0x63}, 4*1024)),
	})
	if err != nil {
		t.Fatalf("BuildTransfer(t→z) error = %v", err)
	}
	if len(transfer.Transaction.Signatures) != 1 {
		t.Fatalf("signed shielded signature count = %d, want 1", len(transfer.Transaction.Signatures))
	}

	if err := node.addTransaction(transfer.Transaction); err != nil {
		t.Fatalf("addTransaction(t→z) error = %v", err)
	}
	if len(node.mempool) != 1 {
		t.Fatalf("mempool size = %d, want 1", len(node.mempool))
	}
}

func TestMempoolShieldedAuthorizationRejectsProofVariant(t *testing.T) {
	node, source, poolState, recentBlockhash, shieldedAddress := newShieldedDepositAdmissionFixture(
		t,
		"shielded-authorization-source",
		10_000_000,
	)
	first := buildShieldedDepositTransaction(t, source, poolState, recentBlockhash, shieldedAddress, 1_000_000, 0x01)
	proofVariant := buildShieldedDepositTransaction(t, source, poolState, recentBlockhash, shieldedAddress, 1_000_000, 0x01)
	firstID, err := first.TxIDString()
	if err != nil {
		t.Fatalf("first TxIDString() error = %v", err)
	}
	variantID, err := proofVariant.TxIDString()
	if err != nil {
		t.Fatalf("variant TxIDString() error = %v", err)
	}
	if firstID == variantID {
		t.Fatal("independently randomized proofs unexpectedly produced the same signed transaction id")
	}
	firstResources, err := shieldedMempoolResourcesFromTransaction(first)
	if err != nil {
		t.Fatalf("first shieldedMempoolResourcesFromTransaction() error = %v", err)
	}
	variantResources, err := shieldedMempoolResourcesFromTransaction(proofVariant)
	if err != nil {
		t.Fatalf("variant shieldedMempoolResourcesFromTransaction() error = %v", err)
	}
	if firstResources.AuthorizationID != variantResources.AuthorizationID {
		t.Fatal("proof variants did not share the proof-stripped authorization id")
	}

	if err := node.addTransaction(first); err != nil {
		t.Fatalf("addTransaction(first) error = %v", err)
	}
	if err := node.addTransaction(proofVariant); err == nil || !strings.Contains(err.Error(), "shielded authorization is already reserved") {
		t.Fatalf("addTransaction(proof variant) error = %v, want authorization reservation rejection", err)
	}
	if len(node.mempool) != 1 {
		t.Fatalf("mempool size = %d, want 1", len(node.mempool))
	}
}

func TestMempoolShieldedTransparentDebitRejectsAggregateOverspendAndReleases(t *testing.T) {
	node, source, poolState, recentBlockhash, shieldedAddress := newShieldedDepositAdmissionFixture(
		t,
		"shielded-debit-source",
		3_000_000,
	)
	first := buildShieldedDepositTransaction(t, source, poolState, recentBlockhash, shieldedAddress, 1_200_000, 0x01)
	second := buildShieldedDepositTransaction(t, source, poolState, recentBlockhash, shieldedAddress, 1_200_000, 0x02)
	firstResources, err := shieldedMempoolResourcesFromTransaction(first)
	if err != nil {
		t.Fatalf("first shieldedMempoolResourcesFromTransaction() error = %v", err)
	}
	secondResources, err := shieldedMempoolResourcesFromTransaction(second)
	if err != nil {
		t.Fatalf("second shieldedMempoolResourcesFromTransaction() error = %v", err)
	}
	if firstResources.AuthorizationID == secondResources.AuthorizationID {
		t.Fatal("different output commitments unexpectedly shared an authorization id")
	}

	if err := node.addTransaction(first); err != nil {
		t.Fatalf("addTransaction(first) error = %v", err)
	}
	if err := node.addTransaction(second); err == nil || !strings.Contains(err.Error(), "shielded transparent debit") {
		t.Fatalf("addTransaction(second) error = %v, want aggregate debit rejection", err)
	}
	if len(node.mempool) != 1 {
		t.Fatalf("mempool size after aggregate debit rejection = %d, want 1", len(node.mempool))
	}
	if reserved := node.mempoolShieldedTransparentDebits[source.PublicKey]; reserved != firstResources.TransparentDebit[source.PublicKey] {
		t.Fatalf("reserved transparent debit = %d, want %d", reserved, firstResources.TransparentDebit[source.PublicKey])
	}

	node.config.MempoolTransactionTTLMillis = 1
	node.mempool[0].SubmitTime = time.Now().Add(-time.Minute).UnixMilli()
	if removed := node.pruneMempool(time.Now().UnixMilli(), node.transactionValidationSlot()); removed != 1 {
		t.Fatalf("pruneMempool() removed = %d, want 1", removed)
	}
	if len(node.mempoolShieldedTransparentDebits) != 0 || len(node.mempoolShieldedAuthorizations) != 0 {
		t.Fatalf(
			"shielded reservations after expiry = debits %d, authorizations %d; want zero",
			len(node.mempoolShieldedTransparentDebits),
			len(node.mempoolShieldedAuthorizations),
		)
	}
	node.config.MempoolTransactionTTLMillis = 60_000
	if err := node.addTransaction(second); err != nil {
		t.Fatalf("addTransaction(second after release) error = %v", err)
	}
}

func TestAddTransactionRejectsUnresolvedAddressTableProgram(t *testing.T) {
	feePayer := mustStructureKeyPair("address-table-program-fee-payer")
	message := structure.OrdexusMessage{
		Header: structure.MessageHeader{
			NumRequiredSignatures: 1,
		},
		AccountKeys:     []structure.PublicKey{feePayer.PublicKey},
		RecentBlockhash: testShieldedHash(0x31),
		Instructions: []structure.CompiledInstruction{{
			ProgramIDIndex: 1,
		}},
		AddressTableLookups: []structure.MessageAddressTableLookup{{
			AccountKey:      mustStructureKeyPair("address-table-program-account").PublicKey,
			WritableIndexes: []uint8{0},
		}},
		Version:          0,
		UsesAddressTable: true,
	}
	transaction := structure.Transaction{Message: message}
	transaction, err := transaction.Sign(map[structure.PublicKey][]byte{
		feePayer.PublicKey: feePayer.PrivateKey,
	})
	if err != nil {
		t.Fatalf("Transaction.Sign() error = %v", err)
	}
	if err := transaction.Validate(); err != nil {
		t.Fatalf("Transaction.Validate() error = %v", err)
	}

	node := newMempoolTestNode(10, 60_000)
	err = node.addTransaction(transaction)
	if err == nil || !strings.Contains(err.Error(), "address table transactions are not supported") {
		t.Fatalf("addTransaction() error = %v, want unresolved address table rejection", err)
	}
	if len(node.mempool) != 0 {
		t.Fatalf("address table transaction entered mempool: %d", len(node.mempool))
	}
}

func TestAddTransactionRejectsShieldedComputeDemandBeforeProof(t *testing.T) {
	feePayer := mustStructureKeyPair("signed-shielded-invalid-proof")
	baseTransaction := newInvalidSignedShieldedTransaction(
		t,
		testShieldedHash(0x35),
		testShieldedHash(0x36),
		testShieldedHash(0x37),
	)
	overLimitTransaction := baseTransaction.Clone()
	overLimitTransaction.Signatures = nil
	overLimitTransaction.Instructions = make([]structure.CompiledInstruction, 8)
	for index := range overLimitTransaction.Instructions {
		overLimitTransaction.Instructions[index] = baseTransaction.Instructions[0].Clone()
	}
	var err error
	overLimitTransaction, err = overLimitTransaction.Sign(map[structure.PublicKey][]byte{
		feePayer.PublicKey: feePayer.PrivateKey,
	})
	if err != nil {
		t.Fatalf("Transaction.Sign(over limit) error = %v", err)
	}

	node := newMempoolTestNode(10, 60_000)
	err = node.addTransaction(overLimitTransaction)
	if err == nil || !strings.Contains(err.Error(), "transaction compute demand") {
		t.Fatalf("addTransaction() error = %v, want raw compute demand rejection", err)
	}
	if strings.Contains(err.Error(), "shielded authorization preflight") || strings.Contains(err.Error(), "shielded preflight is unavailable") {
		t.Fatalf("over-limit transaction reached proof preflight: %v", err)
	}
	if len(node.mempool) != 0 {
		t.Fatalf("over-limit shielded transaction entered mempool: %d", len(node.mempool))
	}
}

func TestAddTransactionRejectsShieldedInvalidProofBeforeMempool(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	executor, err := newRuntimeExecutor(logger)
	if err != nil {
		t.Fatalf("newRuntimeExecutor() error = %v", err)
	}
	invalidSignedFeePayer := mustStructureKeyPair("signed-shielded-invalid-proof")
	ledger, err := blockchain.NewLedgerFromGenesis(nil, blockchain.GenesisConfig{
		ChainID: "shielded-preflight-test",
		FundedAccounts: []blockchain.GenesisAccount{{
			Address:  invalidSignedFeePayer.PublicKey,
			Lamports: 10_000_000,
		}},
	})
	if err != nil {
		t.Fatalf("NewLedgerFromGenesis() error = %v", err)
	}
	poolAccount, found, err := ledger.Account(structure.DefaultShieldedPoolAddress)
	if err != nil || !found {
		t.Fatalf("Ledger.Account(pool) found = %t, error = %v", found, err)
	}
	poolState, err := structure.UnmarshalShieldedPoolStateBinary(poolAccount.Data)
	if err != nil {
		t.Fatalf("UnmarshalShieldedPoolStateBinary() error = %v", err)
	}

	node := newMempoolTestNode(10, 60_000)
	node.logger = logger
	node.executor = executor
	node.ledger = ledger
	head := ledger.Head()
	if err := node.blockhashQueue.Add(structure.RecentBlockhashEntry{
		Blockhash:     head.BlockHash,
		Slot:          head.Slot,
		FeeCalculator: structure.DefaultFeeCalculator(),
	}); err != nil {
		t.Fatalf("BlockhashQueue.Add() error = %v", err)
	}
	transaction := newInvalidUnsignedShieldedTransaction(t, head.BlockHash, poolState.CurrentRoot, testCanonicalShieldedHash(0x41))
	validSignatures, err := transaction.HasValidSignatures()
	if err != nil || !validSignatures {
		t.Fatalf("HasValidSignatures() = %t, error = %v", validSignatures, err)
	}

	err = node.addTransaction(transaction)
	if err == nil || !strings.Contains(err.Error(), "shielded preflight transaction failed") {
		t.Fatalf("addTransaction() error = %v, want shielded preflight failure", err)
	}
	if len(node.mempool) != 0 || len(node.seenTransactions) != 0 {
		t.Fatalf("invalid proof entered mempool: transactions = %d, seen = %d", len(node.mempool), len(node.seenTransactions))
	}
	if len(node.rejectedTransactions) != 0 {
		t.Fatalf("invalid proof poisoned semantic transaction rejection cache: rejected = %d", len(node.rejectedTransactions))
	}
	if got := node.metrics.transactionsDrop.Load(); got != 1 {
		t.Fatalf("transactionsDrop = %d, want 1", got)
	}
	if err := node.addTransaction(transaction); err == nil || !strings.Contains(err.Error(), "recently failed preflight") {
		t.Fatalf("addTransaction(exact retry) error = %v, want wire failure cache rejection", err)
	}
	proofVariant := mutateShieldedProof(t, transaction)
	originalID, err := transaction.TxIDString()
	if err != nil {
		t.Fatalf("original TxIDString() error = %v", err)
	}
	variantID, err := proofVariant.TxIDString()
	if err != nil {
		t.Fatalf("proof variant TxIDString() error = %v", err)
	}
	if originalID != variantID {
		t.Fatalf("proof variant transaction id = %s, want semantic id %s", variantID, originalID)
	}
	if err := node.addTransaction(proofVariant); err == nil || strings.Contains(err.Error(), "recently failed preflight") {
		t.Fatalf("addTransaction(proof variant) error = %v, want a fresh wire preflight failure", err)
	}
	if len(node.seenTransactions) != 0 || len(node.rejectedTransactions) != 0 {
		t.Fatalf("proof variant poisoned semantic transaction caches: seen = %d, rejected = %d", len(node.seenTransactions), len(node.rejectedTransactions))
	}

	signedTransaction := newInvalidSignedShieldedTransaction(t, head.BlockHash, poolState.CurrentRoot, testCanonicalShieldedHash(0x42))
	validSignatures, err = signedTransaction.HasValidSignatures()
	if err != nil || !validSignatures {
		t.Fatalf("signed HasValidSignatures() = %t, error = %v", validSignatures, err)
	}
	err = node.addTransaction(signedTransaction)
	if err == nil || !strings.Contains(err.Error(), "verify Groth16 proof") {
		t.Fatalf("addTransaction(signed) error = %v, want shielded proof preflight failure", err)
	}
	if len(node.mempool) != 0 || len(node.seenTransactions) != 0 || len(node.rejectedTransactions) != 0 {
		t.Fatalf("signed invalid proof changed admission indexes: mempool = %d, seen = %d, rejected = %d", len(node.mempool), len(node.seenTransactions), len(node.rejectedTransactions))
	}
	if got := node.metrics.transactionsDrop.Load(); got != 4 {
		t.Fatalf("transactionsDrop after signed proof = %d, want 4", got)
	}
}

func TestMempoolShieldedNullifierReservationRejectsConflictAndReleasesOnExpiry(t *testing.T) {
	node := newMempoolTestNode(10, 1)
	nullifier := testShieldedHash(0x51)
	anchor := testShieldedHash(0x52)
	first := newInvalidUnsignedShieldedTransaction(t, testShieldedHash(0x53), anchor, nullifier)
	second := newInvalidUnsignedShieldedTransaction(t, testShieldedHash(0x54), anchor, nullifier)
	first.SubmitTime = time.Now().Add(-time.Minute).UnixMilli()
	second.SubmitTime = time.Now().UnixMilli()
	firstID, err := first.TxIDString()
	if err != nil {
		t.Fatalf("first TxIDString() error = %v", err)
	}
	secondID, err := second.TxIDString()
	if err != nil {
		t.Fatalf("second TxIDString() error = %v", err)
	}
	if firstID == secondID {
		t.Fatal("transactions with different blockhashes unexpectedly share an id")
	}

	if err := node.reserveMempoolShieldedNullifiersLocked(firstID, first); err != nil {
		t.Fatalf("reserve first nullifier error = %v", err)
	}
	node.mempool = append(node.mempool, first)
	node.seenTransactions[firstID] = struct{}{}
	if err := node.checkMempoolShieldedNullifiersLocked(secondID, second); err == nil {
		t.Fatal("conflicting shielded nullifier was not rejected")
	}

	if removed := node.pruneMempool(time.Now().UnixMilli(), 0); removed != 1 {
		t.Fatalf("pruneMempool() removed = %d, want 1", removed)
	}
	if err := node.reserveMempoolShieldedNullifiersLocked(secondID, second); err != nil {
		t.Fatalf("reserve nullifier after expiry error = %v", err)
	}
}

func TestMempoolShieldedNullifierReservationReleasedByRemovalPaths(t *testing.T) {
	removalPaths := map[string]func(*posNode, structure.Transaction, string){
		"committed": func(node *posNode, transaction structure.Transaction, _ string) {
			node.removeCommittedMempoolTransactions([]structure.Transaction{transaction})
		},
		"rejected": func(node *posNode, transaction structure.Transaction, transactionID string) {
			node.removeRejectedMempoolTransactions([]consensus.RejectedTransaction{{
				TransactionID: transactionID,
				Transaction:   transaction,
				Status:        structure.TransactionStatusFailed,
				Error:         "test rejection",
			}}, 1)
		},
	}
	for name, remove := range removalPaths {
		t.Run(name, func(t *testing.T) {
			node := newMempoolTestNode(10, 60_000)
			nullifier := testShieldedHash(0x71)
			transaction := newInvalidUnsignedShieldedTransaction(t, testShieldedHash(0x72), testShieldedHash(0x73), nullifier)
			transaction.SubmitTime = time.Now().UnixMilli()
			transactionID, err := transaction.TxIDString()
			if err != nil {
				t.Fatalf("TxIDString() error = %v", err)
			}
			if err := node.reserveMempoolShieldedNullifiersLocked(transactionID, transaction); err != nil {
				t.Fatalf("reserveMempoolShieldedNullifiersLocked() error = %v", err)
			}
			node.mempool = append(node.mempool, transaction)
			node.seenTransactions[transactionID] = struct{}{}

			remove(node, transaction, transactionID)
			if len(node.mempoolShieldedNullifiers) != 0 {
				t.Fatalf("nullifier reservations after %s removal = %d, want 0", name, len(node.mempoolShieldedNullifiers))
			}
		})
	}
}

func newInvalidUnsignedShieldedTransaction(
	t *testing.T,
	recentBlockhash structure.Blockhash,
	anchor structure.Hash,
	nullifier structure.Hash,
) structure.Transaction {
	t.Helper()
	computeBudget := structure.DefaultComputeBudgetLimits()
	computeBudget.MaxComputeUnits = structure.DefaultContractCallComputeUnits
	storageWriteBytes, err := structure.ShieldedStorageWriteChargeBytes([]structure.ShieldedAction{{
		EnabledSpend: true,
		Nullifier:    nullifier,
	}})
	if err != nil {
		t.Fatalf("ShieldedStorageWriteChargeBytes() error = %v", err)
	}
	computeBudget.StorageWriteBytesLimit = storageWriteBytes
	feeDetails, err := structure.DefaultFeeCalculator().Calculate(0, computeBudget)
	if err != nil {
		t.Fatalf("FeeCalculator.Calculate() error = %v", err)
	}
	invalidProof := make([]byte, structure.ShieldedGroth16ProofBytes)
	for index := range invalidProof {
		invalidProof[index] = 0xff
	}
	instruction := structure.ShieldedTransferInstruction{
		EncodingVersion: structure.ShieldedInstructionEncodingVersion,
		Bundle: structure.ShieldedBundle{
			ProtocolVersion:   structure.ShieldedProtocolVersion,
			ConsensusBranchID: structure.ShieldedConsensusBranchID,
			Anchor:            anchor,
			Actions: []structure.ShieldedAction{{
				EnabledSpend: true,
				Nullifier:    nullifier,
			}},
			ValueBalance: int64(feeDetails.TotalFee),
			Proof:        invalidProof,
		},
	}
	instructionData, err := instruction.MarshalBinary()
	if err != nil {
		t.Fatalf("ShieldedTransferInstruction.MarshalBinary() error = %v", err)
	}
	markerAddress := structure.DeriveShieldedNullifierAccount(structure.DefaultBuiltinProgramIDs.Shielded, nullifier)
	transaction := structure.Transaction{
		Accounts: []structure.AccountMeta{
			{PublicKey: structure.DefaultShieldedPoolAddress, IsWritable: true},
			{PublicKey: markerAddress, IsWritable: true},
			{PublicKey: structure.DefaultBuiltinProgramIDs.Shielded},
		},
		Instructions: []structure.CompiledInstruction{{
			ProgramIDIndex: 2,
			AccountIndexes: []uint8{0, 1},
			Data:           instructionData,
		}},
		RecentBlockhash: recentBlockhash,
		Fee:             feeDetails.TotalFee,
	}
	if err := transaction.Validate(); err != nil {
		t.Fatalf("Transaction.Validate() error = %v", err)
	}
	return transaction
}

func newInvalidSignedShieldedTransaction(
	t *testing.T,
	recentBlockhash structure.Blockhash,
	anchor structure.Hash,
	nullifier structure.Hash,
) structure.Transaction {
	t.Helper()
	feePayer := mustStructureKeyPair("signed-shielded-invalid-proof")
	invalidProof := make([]byte, structure.ShieldedGroth16ProofBytes)
	for index := range invalidProof {
		invalidProof[index] = 0xff
	}
	instruction := structure.ShieldedTransferInstruction{
		EncodingVersion:        structure.ShieldedInstructionEncodingVersion,
		TransparentInputAmount: 1_000_000,
		Bundle: structure.ShieldedBundle{
			ProtocolVersion:   structure.ShieldedProtocolVersion,
			ConsensusBranchID: structure.ShieldedConsensusBranchID,
			Anchor:            anchor,
			Actions: []structure.ShieldedAction{{
				EnabledSpend: true,
				Nullifier:    nullifier,
			}},
			ValueBalance: -1_000_000,
			Proof:        invalidProof,
		},
	}
	instructionData, err := instruction.MarshalBinary()
	if err != nil {
		t.Fatalf("signed ShieldedTransferInstruction.MarshalBinary() error = %v", err)
	}
	markerAddress := structure.DeriveShieldedNullifierAccount(structure.DefaultBuiltinProgramIDs.Shielded, nullifier)
	transaction := structure.Transaction{
		Accounts: []structure.AccountMeta{
			{PublicKey: feePayer.PublicKey, IsSigner: true, IsWritable: true},
			{PublicKey: structure.DefaultShieldedPoolAddress, IsWritable: true},
			{PublicKey: markerAddress, IsWritable: true},
			{PublicKey: structure.DefaultBuiltinProgramIDs.Shielded},
		},
		Instructions: []structure.CompiledInstruction{{
			ProgramIDIndex: 3,
			AccountIndexes: []uint8{1, 0, 2},
			Data:           instructionData,
		}},
		RecentBlockhash: recentBlockhash,
	}
	transaction, err = transaction.Sign(map[structure.PublicKey][]byte{
		feePayer.PublicKey: feePayer.PrivateKey,
	})
	if err != nil {
		t.Fatalf("Transaction.Sign() error = %v", err)
	}
	feeDetails, err := estimateTransactionFeeDetails(transaction)
	if err != nil {
		t.Fatalf("estimateTransactionFeeDetails() error = %v", err)
	}
	transaction.Fee = feeDetails.TotalFee
	return transaction
}

func mutateShieldedProof(t *testing.T, transaction structure.Transaction) structure.Transaction {
	t.Helper()
	mutated := transaction.Clone()
	instruction, err := structure.UnmarshalShieldedTransferInstructionBinary(mutated.Instructions[0].Data)
	if err != nil {
		t.Fatalf("UnmarshalShieldedTransferInstructionBinary() error = %v", err)
	}
	instruction.Bundle.Proof[0] ^= 0x01
	instructionData, err := instruction.MarshalBinary()
	if err != nil {
		t.Fatalf("mutated ShieldedTransferInstruction.MarshalBinary() error = %v", err)
	}
	mutated.Instructions[0].Data = instructionData
	return mutated
}

func testShieldedHash(value byte) structure.Hash {
	var hash structure.Hash
	for index := range hash {
		hash[index] = value
	}
	return hash
}

func testCanonicalShieldedHash(value byte) structure.Hash {
	var hash structure.Hash
	hash[len(hash)-1] = value
	return hash
}

func newShieldedDepositAdmissionFixture(
	t *testing.T,
	sourceSeed string,
	sourceLamports uint64,
) (*posNode, structure.OrdexusKeyPair, structure.ShieldedPoolState, structure.Blockhash, walletshielded.ShieldedAddress) {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	executor, err := newRuntimeExecutor(logger)
	if err != nil {
		t.Fatalf("newRuntimeExecutor() error = %v", err)
	}
	source := mustStructureKeyPair(sourceSeed)
	ledger, err := blockchain.NewLedgerFromGenesis(nil, blockchain.GenesisConfig{
		ChainID: sourceSeed,
		FundedAccounts: []blockchain.GenesisAccount{{
			Address:  source.PublicKey,
			Lamports: sourceLamports,
		}},
	})
	if err != nil {
		t.Fatalf("NewLedgerFromGenesis() error = %v", err)
	}
	poolAccount, found, err := ledger.Account(structure.DefaultShieldedPoolAddress)
	if err != nil || !found {
		t.Fatalf("Ledger.Account(pool) found = %t, error = %v", found, err)
	}
	poolState, err := structure.UnmarshalShieldedPoolStateBinary(poolAccount.Data)
	if err != nil {
		t.Fatalf("UnmarshalShieldedPoolStateBinary() error = %v", err)
	}
	diversifier, err := walletshielded.NewDiversifier(bytes.Repeat([]byte{0x76}, walletshielded.DiversifierSize))
	if err != nil {
		t.Fatalf("NewDiversifier() error = %v", err)
	}
	_, _, shieldedAddress, err := walletshielded.DeriveKeySet(bytes.Repeat([]byte{0x77}, walletshielded.SeedSize), diversifier)
	if err != nil {
		t.Fatalf("DeriveKeySet() error = %v", err)
	}
	node := newMempoolTestNode(10, 60_000)
	node.logger = logger
	node.executor = executor
	node.ledger = ledger
	recentBlockhash := ledger.Head().BlockHash
	if err := node.blockhashQueue.Add(structure.RecentBlockhashEntry{
		Blockhash:     recentBlockhash,
		Slot:          ledger.Head().Slot,
		FeeCalculator: structure.DefaultFeeCalculator(),
	}); err != nil {
		t.Fatalf("BlockhashQueue.Add() error = %v", err)
	}
	return node, source, poolState, recentBlockhash, shieldedAddress
}

func buildShieldedDepositTransaction(
	t *testing.T,
	source structure.OrdexusKeyPair,
	poolState structure.ShieldedPoolState,
	recentBlockhash structure.Blockhash,
	shieldedAddress walletshielded.ShieldedAddress,
	amount uint64,
	randomnessByte byte,
) structure.Transaction {
	t.Helper()
	transfer, err := walletshielded.BuildTransfer(walletshielded.TransferRequest{
		PoolState: poolState,
		Outputs: []walletshielded.NoteOutput{{
			Address: shieldedAddress,
			Value:   amount,
		}},
		TransparentInput: &walletshielded.TransparentInput{
			Account: source,
			Amount:  amount,
		},
		RecentBlockhash: recentBlockhash,
		FeeCalculator:   structure.DefaultFeeCalculator(),
		Randomness:      bytes.NewReader(bytes.Repeat([]byte{randomnessByte}, 4*1024)),
	})
	if err != nil {
		t.Fatalf("BuildTransfer(t→z) error = %v", err)
	}
	return transfer.Transaction
}
