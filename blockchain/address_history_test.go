package blockchain

import (
	"testing"

	"solana_golang/database"
	"solana_golang/structure"
)

func TestLedgerAddressHistoryPersistsAndRebuilds(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{
		Path:   t.TempDir(),
		Engine: database.EnginePebble,
		WAL:    true,
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	ledger, err := LoadOrCreateLedger(db, testGenesis(t))
	if err != nil {
		t.Fatalf("load ledger: %v", err)
	}
	source := testKeyPair(t, "history-source")
	destination := testKeyPair(t, "history-destination")
	transaction, err := NewTransferTransaction(source, destination.PublicKey, 7_500, ledger.Head().BlockHash)
	if err != nil {
		t.Fatalf("NewTransferTransaction() error = %v", err)
	}
	proposal, nextState := testProposalFromHead(t, ledger.Head(), ledger.State(), 1, 1, "history-block")
	proposal.Transactions = []structure.Transaction{transaction}
	if _, err := ledger.CommitBlock(CommitBlockRequest{Proposal: proposal, NextState: nextState}); err != nil {
		t.Fatalf("CommitBlock() error = %v", err)
	}

	sourceHistory, err := ledger.AddressHistory(source.PublicKey, "", 1)
	if err != nil {
		t.Fatalf("AddressHistory(source) error = %v", err)
	}
	if len(sourceHistory.Records) != 1 {
		t.Fatalf("source records = %d, want 1", len(sourceHistory.Records))
	}
	assertAddressHistoryRecord(t, sourceHistory.Records[0], AddressHistoryDirectionOutgoing, AddressHistoryKindTransfer, destination.PublicKey.String(), 7_500)

	destinationHistory, err := ledger.AddressHistory(destination.PublicKey, "", 1)
	if err != nil {
		t.Fatalf("AddressHistory(destination) error = %v", err)
	}
	if len(destinationHistory.Records) != 1 {
		t.Fatalf("destination records = %d, want 1", len(destinationHistory.Records))
	}
	assertAddressHistoryRecord(t, destinationHistory.Records[0], AddressHistoryDirectionIncoming, AddressHistoryKindTransfer, source.PublicKey.String(), 7_500)

	if err := db.ClearTable(database.TableAddrToTx); err != nil {
		t.Fatalf("ClearTable(TableAddrToTx) error = %v", err)
	}
	reloaded, err := LoadOrCreateLedger(db, testGenesis(t))
	if err != nil {
		t.Fatalf("reload ledger: %v", err)
	}
	rebuiltHistory, err := reloaded.AddressHistory(source.PublicKey, "", 1)
	if err != nil {
		t.Fatalf("AddressHistory(rebuilt) error = %v", err)
	}
	if len(rebuiltHistory.Records) != 1 {
		t.Fatalf("rebuilt records = %d, want 1", len(rebuiltHistory.Records))
	}
	assertAddressHistoryRecord(t, rebuiltHistory.Records[0], AddressHistoryDirectionOutgoing, AddressHistoryKindTransfer, destination.PublicKey.String(), 7_500)
}

func TestLedgerAddressHistoryIndexesPrivacyDepositSourceOnly(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{
		Path:   t.TempDir(),
		Engine: database.EnginePebble,
		WAL:    true,
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	ledger, err := LoadOrCreateLedger(db, testGenesis(t))
	if err != nil {
		t.Fatalf("load ledger: %v", err)
	}
	source := testKeyPair(t, "privacy-history-source")
	stateAccount := testKeyPair(t, "privacy-history-state")
	transaction, err := NewPrivacyDepositTransaction(PrivacyDepositTransactionParams{
		Source:        source,
		StateAccount:  stateAccount,
		Amount:        9_999,
		Commitment:    testHash(t, "privacy-history-commitment"),
		EncryptedNote: []byte("note"),
		CreateState:   true,
	}, ledger.Head().BlockHash)
	if err != nil {
		t.Fatalf("NewPrivacyDepositTransaction() error = %v", err)
	}
	proposal, nextState := testProposalFromHead(t, ledger.Head(), ledger.State(), 1, 1, "privacy-history-block")
	proposal.Transactions = []structure.Transaction{transaction}
	if _, err := ledger.CommitBlock(CommitBlockRequest{Proposal: proposal, NextState: nextState}); err != nil {
		t.Fatalf("CommitBlock() error = %v", err)
	}

	sourceHistory, err := ledger.AddressHistory(source.PublicKey, "", 10)
	if err != nil {
		t.Fatalf("AddressHistory(source) error = %v", err)
	}
	if len(sourceHistory.Records) != 1 {
		t.Fatalf("source records = %d, want 1", len(sourceHistory.Records))
	}
	assertAddressHistoryRecord(t, sourceHistory.Records[0], AddressHistoryDirectionOutgoing, AddressHistoryKindPrivacyDeposit, stateAccount.PublicKey.String(), 9_999)

	stateHistory, err := ledger.AddressHistory(stateAccount.PublicKey, "", 10)
	if err != nil {
		t.Fatalf("AddressHistory(state) error = %v", err)
	}
	if len(stateHistory.Records) != 0 {
		t.Fatalf("privacy state records = %d, want 0", len(stateHistory.Records))
	}
}

func TestLedgerPrivacyBalanceAggregatesOwnedNotes(t *testing.T) {
	db, err := database.NewDatabase(database.DatabaseConfig{
		Path:   t.TempDir(),
		Engine: database.EnginePebble,
		WAL:    true,
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	ledger, err := LoadOrCreateLedger(db, testGenesis(t))
	if err != nil {
		t.Fatalf("load ledger: %v", err)
	}
	owner := testKeyPair(t, "privacy-balance-owner")
	other := testKeyPair(t, "privacy-balance-other")
	stateAddress := testKeyPair(t, "privacy-balance-state").PublicKey
	privacyState := structure.PrivacyState{
		Version: structure.PrivacyStateVersion,
		Notes: []structure.PrivacyNoteRecord{
			{
				Commitment:     testHash(t, "note-available"),
				SpendAuthority: owner.PublicKey,
				Amount:         7,
				VMVersion:      structure.PrivacyStateVersion,
				EncryptedNote:  []byte("available"),
			},
			{
				Commitment:     testHash(t, "note-spent"),
				SpendAuthority: owner.PublicKey,
				Amount:         4,
				Spent:          true,
				SpentSlot:      2,
				SpendNullifier: testHash(t, "note-spent-nullifier"),
				VMVersion:      structure.PrivacyStateVersion,
				EncryptedNote:  []byte("spent"),
			},
			{
				Commitment:     testHash(t, "note-other"),
				SpendAuthority: other.PublicKey,
				Amount:         9,
				VMVersion:      structure.PrivacyStateVersion,
				EncryptedNote:  []byte("other"),
			},
		},
		SpentNullifiers: []structure.Hash{testHash(t, "note-spent-nullifier")},
	}
	data, err := privacyState.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary(privacy state) error = %v", err)
	}
	rentLamports, err := structure.MinimumBalanceForRentExemption(len(data))
	if err != nil {
		t.Fatalf("MinimumBalanceForRentExemption() error = %v", err)
	}
	account, err := structure.NewAccount(rentLamports+50, data, structure.DefaultBuiltinProgramIDs.Privacy, false, 0)
	if err != nil {
		t.Fatalf("NewAccount() error = %v", err)
	}

	nextState := ledger.State()
	nextState.Accounts = append(nextState.Accounts, structure.AddressedAccount{
		Address: stateAddress,
		Account: account,
	})
	proposal, _ := testProposalFromHead(t, ledger.Head(), nextState, 1, 1, "privacy-balance-block")
	if _, err := ledger.CommitBlock(CommitBlockRequest{Proposal: proposal, NextState: nextState}); err != nil {
		t.Fatalf("CommitBlock() error = %v", err)
	}

	summary, err := ledger.PrivacyBalance(stateAddress, owner.PublicKey)
	if err != nil {
		t.Fatalf("PrivacyBalance() error = %v", err)
	}
	if summary.AvailableLamports != 7 {
		t.Fatalf("available = %d, want 7", summary.AvailableLamports)
	}
	if summary.EscrowLamports != rentLamports+50 {
		t.Fatalf("escrow = %d, want %d", summary.EscrowLamports, rentLamports+50)
	}
	if summary.SpendableNoteCount != 1 {
		t.Fatalf("spendable note count = %d, want 1", summary.SpendableNoteCount)
	}
	if summary.SpentNoteCount != 1 {
		t.Fatalf("spent note count = %d, want 1", summary.SpentNoteCount)
	}
	if summary.OwnedNoteCount != 2 {
		t.Fatalf("owned note count = %d, want 2", summary.OwnedNoteCount)
	}
	if summary.StateNoteCount != 3 {
		t.Fatalf("state note count = %d, want 3", summary.StateNoteCount)
	}
}

func assertAddressHistoryRecord(t *testing.T, record AddressHistoryRecord, direction AddressHistoryDirection, kind AddressHistoryKind, counterparty string, amount uint64) {
	t.Helper()
	if record.Direction != direction {
		t.Fatalf("direction = %s, want %s", record.Direction, direction)
	}
	if record.Kind != kind {
		t.Fatalf("kind = %s, want %s", record.Kind, kind)
	}
	if record.Counterparty != counterparty {
		t.Fatalf("counterparty = %s, want %s", record.Counterparty, counterparty)
	}
	if record.AmountLamports != amount {
		t.Fatalf("amount = %d, want %d", record.AmountLamports, amount)
	}
	if record.TransactionID == "" {
		t.Fatal("transaction id is empty")
	}
	if record.BlockHash == "" {
		t.Fatal("block hash is empty")
	}
}
