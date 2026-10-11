package blockchain

import (
	"testing"

	"github.com/tentechtop/ordexus-core/database"
	"github.com/tentechtop/ordexus-core/structure"
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

func TestShieldedInstructionEffectsExposeOnlyTransparentSides(t *testing.T) {
	source := testKeyPair(t, "shielded-history-source").PublicKey
	destination := testKeyPair(t, "shielded-history-destination").PublicKey
	instruction := structure.ShieldedTransferInstruction{
		TransparentInputAmount:  9_000,
		TransparentOutputAmount: 4_000,
	}
	effects := shieldedInstructionEffects(instruction, []structure.PublicKey{
		structure.DefaultShieldedPoolAddress,
		source,
		destination,
	})
	if len(effects) != 2 {
		t.Fatalf("effect count = %d, want 2", len(effects))
	}
	if effects[0].Address != source || effects[0].Direction != AddressHistoryDirectionOutgoing || effects[0].Kind != AddressHistoryKindShieldedDeposit || effects[0].AmountLamports != 9_000 {
		t.Fatalf("deposit effect = %+v", effects[0])
	}
	if effects[1].Address != destination || effects[1].Direction != AddressHistoryDirectionIncoming || effects[1].Kind != AddressHistoryKindShieldedWithdraw || effects[1].AmountLamports != 4_000 {
		t.Fatalf("withdraw effect = %+v", effects[1])
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
