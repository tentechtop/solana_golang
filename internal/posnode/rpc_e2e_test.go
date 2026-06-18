package posnode

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"solana_golang/blockchain"
	"solana_golang/consensus"
	"solana_golang/database"
	stakeprogram "solana_golang/programs/stake"
	"solana_golang/rpc"
	"solana_golang/structure"
	"solana_golang/utils"
)

type posNodeRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpc.Error      `json:"error,omitempty"`
}

func TestHTTPJSONRPCSubmitsSignedTransaction(t *testing.T) {
	node, source, destination := newRPCIntegrationTestNode(t)
	server := rpc.NewServer(rpc.ServerConfig{Logger: node.logger}, rpc.NewDefaultRouter(node))

	balanceResponse := postPosNodeJSONRPC(t, server, 1, rpc.MethodGetBalance, []any{source.PublicKey.String()})
	balance := decodePosNodeRPCResult[rpc.BalanceResult](t, balanceResponse)
	if balance.Value != 1_000_000_000 {
		t.Fatalf("balance = %d, want 1000000000", balance.Value)
	}

	transaction := newRPCTransferTransaction(t, node, source, destination.PublicKey, 10_000)
	encodedTransaction := encodeRPCTransaction(t, transaction)
	sendResponse := postPosNodeJSONRPC(t, server, 2, rpc.MethodSendTransaction, []any{encodedTransaction})
	signature := decodePosNodeRPCResult[string](t, sendResponse)
	if signature == "" {
		t.Fatal("sendTransaction signature is empty")
	}
	if len(node.mempool) != 1 {
		t.Fatalf("mempool size = %d, want 1", len(node.mempool))
	}
	expectedFee := mustEstimateTransactionFeeDetails(t, transaction)
	if node.mempool[0].Fee != expectedFee.TotalFee {
		t.Fatalf("mempool fee = %d, want %d", node.mempool[0].Fee, expectedFee.TotalFee)
	}
	if got := node.metrics.transactionsIn.Load(); got != 1 {
		t.Fatalf("transactionsIn = %d, want 1", got)
	}

	healthResponse := postPosNodeJSONRPC(t, server, 3, rpc.MethodGetHealth, []any{})
	health := decodePosNodeRPCResult[rpc.HealthResult](t, healthResponse)
	if !health.OK {
		t.Fatal("health ok = false, want true")
	}
	if health.MempoolSize != 1 {
		t.Fatalf("health mempool size = %d, want 1", health.MempoolSize)
	}

	metricsResponse := postPosNodeJSONRPC(t, server, 4, rpc.MethodGetMetrics, []any{})
	metrics := decodePosNodeRPCResult[nodeOperationalMetrics](t, metricsResponse)
	if metrics.MempoolSize != 1 {
		t.Fatalf("metrics mempool size = %d, want 1", metrics.MempoolSize)
	}
	if metrics.Counters.TransactionsIn != 1 {
		t.Fatalf("metrics transactions in = %d, want 1", metrics.Counters.TransactionsIn)
	}
}

func TestHTTPJSONRPCRejectsInvalidSignedTransaction(t *testing.T) {
	node, source, destination := newRPCIntegrationTestNode(t)
	server := rpc.NewServer(rpc.ServerConfig{Logger: node.logger}, rpc.NewDefaultRouter(node))
	transaction := newRPCTransferTransaction(t, node, source, destination.PublicKey, 10_000)
	transaction.Signatures[0][0] ^= 0xff
	encodedTransaction := encodeRPCTransaction(t, transaction)

	response := postPosNodeJSONRPC(t, server, 1, rpc.MethodSendTransaction, []any{encodedTransaction})
	if response.Error == nil {
		t.Fatalf("response error = nil, want invalid signature error")
	}
	if len(node.mempool) != 0 {
		t.Fatalf("mempool size = %d, want 0", len(node.mempool))
	}
	if got := node.metrics.transactionsDrop.Load(); got != 1 {
		t.Fatalf("transactionsDrop = %d, want 1", got)
	}
}

func TestHTTPJSONRPCRejectsPreflightFailedTransaction(t *testing.T) {
	node, source, destination := newRPCIntegrationTestNode(t)
	server := rpc.NewServer(rpc.ServerConfig{Logger: node.logger}, rpc.NewDefaultRouter(node))
	transaction := newRPCTransferTransaction(t, node, source, destination.PublicKey, 2_000_000_000)
	encodedTransaction := encodeRPCTransaction(t, transaction)

	response := postPosNodeJSONRPC(t, server, 1, rpc.MethodSendTransaction, []any{encodedTransaction})
	if response.Error == nil {
		t.Fatal("response error = nil, want preflight failure")
	}
	errorData := fmt.Sprint(response.Error.Data)
	if !strings.Contains(errorData, "preflight transaction failed") {
		t.Fatalf("error data = %q, want preflight failure", response.Error.Data)
	}
	if len(node.mempool) != 0 {
		t.Fatalf("mempool size = %d, want 0", len(node.mempool))
	}
	if got := node.metrics.transactionsDrop.Load(); got != 1 {
		t.Fatalf("transactionsDrop = %d, want 1", got)
	}
}

func TestHTTPJSONRPCGetTransactionReturnsMempoolDetails(t *testing.T) {
	node, source, destination := newRPCIntegrationTestNode(t)
	server := rpc.NewServer(rpc.ServerConfig{Logger: node.logger}, rpc.NewDefaultRouter(node))

	transaction := newRPCTransferTransaction(t, node, source, destination.PublicKey, 10_000)
	encodedTransaction := encodeRPCTransaction(t, transaction)
	sendResponse := postPosNodeJSONRPC(t, server, 1, rpc.MethodSendTransaction, []any{encodedTransaction})
	signature := decodePosNodeRPCResult[string](t, sendResponse)

	detailResponse := postPosNodeJSONRPC(t, server, 2, rpc.MethodGetTransaction, []any{signature})
	detail := decodePosNodeRPCResult[rpc.TransactionDetailResult](t, detailResponse)
	if !detail.Found {
		t.Fatal("detail found = false, want true")
	}
	if detail.Location != "mempool" {
		t.Fatalf("detail location = %s, want mempool", detail.Location)
	}
	if detail.Status != "pending" {
		t.Fatalf("detail status = %s, want pending", detail.Status)
	}
	if detail.Sender != source.PublicKey.String() {
		t.Fatalf("detail sender = %s, want %s", detail.Sender, source.PublicKey.String())
	}
	expectedFee := mustEstimateTransactionFeeDetails(t, transaction)
	assertRPCTransactionFeeDetails(t, detail, expectedFee)
	if detail.InstructionCount != 1 {
		t.Fatalf("detail instruction count = %d, want 1", detail.InstructionCount)
	}
}

func TestHTTPJSONRPCGetTransactionReturnsCommittedBlockDetails(t *testing.T) {
	source := mustStructureKeyPair("rpc-committed-source")
	destination := mustStructureKeyPair("rpc-committed-destination")
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	db, err := database.NewDatabase(database.DatabaseConfig{
		Path:   t.TempDir(),
		Engine: database.EnginePebble,
		WAL:    true,
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	ledger, err := blockchain.LoadOrCreateLedger(db, blockchain.GenesisConfig{
		ChainID: "posnode-rpc-transaction",
		FundedAccounts: []blockchain.GenesisAccount{
			{Address: source.PublicKey, Lamports: 1_000_000_000},
			{Address: destination.PublicKey, Lamports: 1_000_000},
		},
	})
	if err != nil {
		t.Fatalf("LoadOrCreateLedger() error = %v", err)
	}
	ledger.SetLogger(logger)
	node := &posNode{
		config: nodeConfig{
			MempoolMaxTransactions:      10,
			MempoolTransactionTTLMillis: 60_000,
		},
		logger:           logger,
		db:               db,
		ledger:           ledger,
		seenTransactions: make(map[string]struct{}),
	}
	server := rpc.NewServer(rpc.ServerConfig{Logger: node.logger}, rpc.NewDefaultRouter(node))

	transaction := newRPCTransferTransaction(t, node, source, destination.PublicKey, 10_000)
	transactionID, err := transaction.TxIDString()
	if err != nil {
		t.Fatalf("TxIDString() error = %v", err)
	}
	proposal, nextState := blockchainTestProposalFromHead(t, ledger.Head(), ledger.State(), 1, 1, "rpc-committed")
	proposal.Transactions = []structure.Transaction{transaction}
	committedHead, err := ledger.CommitBlock(blockchain.CommitBlockRequest{Proposal: proposal, NextState: nextState})
	if err != nil {
		t.Fatalf("CommitBlock() error = %v", err)
	}

	detailResponse := postPosNodeJSONRPC(t, server, 1, rpc.MethodGetTransaction, []any{transactionID})
	detail := decodePosNodeRPCResult[rpc.TransactionDetailResult](t, detailResponse)
	if !detail.Found {
		t.Fatal("detail found = false, want true")
	}
	if detail.Location != "block" {
		t.Fatalf("detail location = %s, want block", detail.Location)
	}
	if detail.Status != "confirmed" {
		t.Fatalf("detail status = %s, want confirmed", detail.Status)
	}
	if detail.BlockHeight != committedHead.Height {
		t.Fatalf("detail block height = %d, want %d", detail.BlockHeight, committedHead.Height)
	}
	if detail.Slot != committedHead.Slot {
		t.Fatalf("detail slot = %d, want %d", detail.Slot, committedHead.Slot)
	}
	if detail.Blockhash != committedHead.BlockHash.String() {
		t.Fatalf("detail block hash = %s, want %s", detail.Blockhash, committedHead.BlockHash.String())
	}
	expectedFee := mustEstimateTransactionFeeDetails(t, transaction)
	assertRPCTransactionFeeDetails(t, detail, expectedFee)
}

func TestHTTPJSONRPCGetAddressTransactionsReturnsCommittedHistory(t *testing.T) {
	source := mustStructureKeyPair("rpc-history-source")
	destination := mustStructureKeyPair("rpc-history-destination")
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	db, err := database.NewDatabase(database.DatabaseConfig{
		Path:   t.TempDir(),
		Engine: database.EnginePebble,
		WAL:    true,
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	ledger, err := blockchain.LoadOrCreateLedger(db, blockchain.GenesisConfig{
		ChainID: "posnode-rpc-history",
		FundedAccounts: []blockchain.GenesisAccount{
			{Address: source.PublicKey, Lamports: 1_000_000_000},
			{Address: destination.PublicKey, Lamports: 1_000_000},
		},
	})
	if err != nil {
		t.Fatalf("LoadOrCreateLedger() error = %v", err)
	}
	ledger.SetLogger(logger)
	node := &posNode{
		config: nodeConfig{
			MempoolMaxTransactions:      10,
			MempoolTransactionTTLMillis: 60_000,
		},
		logger:           logger,
		db:               db,
		ledger:           ledger,
		seenTransactions: make(map[string]struct{}),
	}
	server := rpc.NewServer(rpc.ServerConfig{Logger: node.logger}, rpc.NewDefaultRouter(node))

	transaction := newRPCTransferTransaction(t, node, source, destination.PublicKey, 25_000)
	transactionID, err := transaction.TxIDString()
	if err != nil {
		t.Fatalf("TxIDString() error = %v", err)
	}
	proposal, nextState := blockchainTestProposalFromHead(t, ledger.Head(), ledger.State(), 1, 1, "rpc-history-block")
	proposal.Transactions = []structure.Transaction{transaction}
	committedHead, err := ledger.CommitBlock(blockchain.CommitBlockRequest{Proposal: proposal, NextState: nextState})
	if err != nil {
		t.Fatalf("CommitBlock() error = %v", err)
	}

	address := encodeTestProtocolAddress(protocolAddressTransparent, source.PublicKey[:], "t")
	response := postPosNodeJSONRPC(t, server, 1, rpc.MethodGetAddressTransactions, []any{address, 10})
	history := decodePosNodeRPCResult[rpc.AccountTransactionHistoryResult](t, response)
	if history.Scope != "transparent_balance_changes_only" {
		t.Fatalf("scope = %s, want transparent_balance_changes_only", history.Scope)
	}
	if len(history.Records) != 1 {
		t.Fatalf("history records = %d, want 1", len(history.Records))
	}

	record := history.Records[0]
	if record.Signature != transactionID {
		t.Fatalf("record signature = %s, want %s", record.Signature, transactionID)
	}
	if record.Direction != "outgoing" {
		t.Fatalf("record direction = %s, want outgoing", record.Direction)
	}
	if record.Kind != "transfer" {
		t.Fatalf("record kind = %s, want transfer", record.Kind)
	}
	if record.Counterparty != destination.PublicKey.String() {
		t.Fatalf("record counterparty = %s, want %s", record.Counterparty, destination.PublicKey.String())
	}
	if record.AmountLamports != "25000" {
		t.Fatalf("record amount = %s, want 25000", record.AmountLamports)
	}
	if record.BlockHeight != committedHead.Height {
		t.Fatalf("record block height = %d, want %d", record.BlockHeight, committedHead.Height)
	}
	if record.Blockhash != committedHead.BlockHash.String() {
		t.Fatalf("record block hash = %s, want %s", record.Blockhash, committedHead.BlockHash.String())
	}
}

func TestHTTPJSONRPCGetPrivacyBalanceReturnsAggregatedNotes(t *testing.T) {
	owner := mustStructureKeyPair("rpc-privacy-owner")
	other := mustStructureKeyPair("rpc-privacy-other")
	stateAccount := mustStructureKeyPair("rpc-privacy-state")
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	ledger, err := blockchain.NewLedgerFromGenesis(nil, blockchain.GenesisConfig{
		ChainID: "posnode-rpc-privacy-balance",
		FundedAccounts: []blockchain.GenesisAccount{
			{Address: owner.PublicKey, Lamports: 1_000_000},
		},
	})
	if err != nil {
		t.Fatalf("NewLedgerFromGenesis() error = %v", err)
	}
	ledger.SetLogger(logger)
	node := &posNode{
		config: nodeConfig{
			MempoolMaxTransactions:      10,
			MempoolTransactionTTLMillis: 60_000,
		},
		logger:           logger,
		ledger:           ledger,
		seenTransactions: make(map[string]struct{}),
	}
	server := rpc.NewServer(rpc.ServerConfig{Logger: node.logger}, rpc.NewDefaultRouter(node))

	privacyState := buildPrivacyStateForTest(t, []structure.PrivacyNoteRecord{
		{
			Commitment:     blockchainTestHash(t, "rpc-privacy-note-available"),
			SpendAuthority: owner.PublicKey,
			Amount:         7,
			VMVersion:      structure.PrivacyStateVersion,
			EncryptedNote:  []byte("available"),
		},
		{
			Commitment:     blockchainTestHash(t, "rpc-privacy-note-spent"),
			SpendAuthority: owner.PublicKey,
			Amount:         4,
			Spent:          true,
			SpentSlot:      2,
			SpendNullifier: blockchainTestHash(t, "rpc-privacy-note-nullifier"),
			VMVersion:      structure.PrivacyStateVersion,
			EncryptedNote:  []byte("spent"),
		},
		{
			Commitment:     blockchainTestHash(t, "rpc-privacy-note-other"),
			SpendAuthority: other.PublicKey,
			Amount:         9,
			VMVersion:      structure.PrivacyStateVersion,
			EncryptedNote:  []byte("other"),
		},
	}, []structure.Hash{blockchainTestHash(t, "rpc-privacy-note-nullifier")})
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
		Address: stateAccount.PublicKey,
		Account: account,
	})
	proposal, _ := blockchainTestProposalFromHead(t, ledger.Head(), nextState, 1, 1, "rpc-privacy-balance-block")
	if _, err := ledger.CommitBlock(blockchain.CommitBlockRequest{Proposal: proposal, NextState: nextState}); err != nil {
		t.Fatalf("CommitBlock() error = %v", err)
	}

	stateAddress := encodeTestProtocolAddress(protocolAddressPrivacy, stateAccount.PublicKey[:], "z")
	ownerAddress := encodeTestProtocolAddress(protocolAddressTransparent, owner.PublicKey[:], "t")
	response := postPosNodeJSONRPC(t, server, 1, rpc.MethodGetPrivacyBalance, []any{stateAddress, ownerAddress})
	summary := decodePosNodeRPCResult[rpc.PrivacyBalanceResult](t, response)
	if summary.AvailableLamports != "7" {
		t.Fatalf("available = %s, want 7", summary.AvailableLamports)
	}
	if summary.EscrowLamports != fmt.Sprintf("%d", rentLamports+50) {
		t.Fatalf("escrow = %s, want %d", summary.EscrowLamports, rentLamports+50)
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

func TestHTTPJSONRPCPrivacyDepositToReceiverUsesReceiverAuthority(t *testing.T) {
	node, _, _ := newRPCIntegrationTestNode(t)
	server := rpc.NewServer(rpc.ServerConfig{Logger: node.logger}, rpc.NewDefaultRouter(node))
	receiverState := mustStructureKeyPair("rpc-privacy-receiver-state")
	receiverAuthority := mustStructureKeyPair("rpc-privacy-receiver-authority")
	addPrivacyStateAccountToLedger(t, node.ledger, receiverState.PublicKey)

	stateAddress := encodeTestProtocolAddress(protocolAddressPrivacy, receiverState.PublicKey[:], "z")
	authorityAddress := encodeTestProtocolAddress(protocolAddressTransparent, receiverAuthority.PublicKey[:], "t")
	response := postPosNodeJSONRPC(t, server, 1, rpc.MethodPrivacyDepositToReceiver, []any{
		"rpc-source",
		stateAddress,
		authorityAddress,
		uint64(50_000),
		"",
		"",
		uint64(0),
	})
	result := decodePosNodeRPCResult[rpc.PrivacyTransactionResult](t, response)
	if result.Signature == "" || result.PrivacyState != receiverState.PublicKey.String() {
		t.Fatalf("result = %+v, want receiver state and signature", result)
	}
	if len(node.mempool) != 1 {
		t.Fatalf("mempool size = %d, want 1", len(node.mempool))
	}
	privacyInstruction, err := structure.UnmarshalPrivacyInstructionBinary(node.mempool[0].Instructions[0].Data)
	if err != nil {
		t.Fatalf("UnmarshalPrivacyInstructionBinary() error = %v", err)
	}
	if privacyInstruction.Deposit.SpendAuthority != receiverAuthority.PublicKey {
		t.Fatalf("spend authority = %s, want %s", privacyInstruction.Deposit.SpendAuthority.String(), receiverAuthority.PublicKey.String())
	}
}

func TestHTTPJSONRPCPrivacyTransferToReceiverBuildsChangeNote(t *testing.T) {
	node, source, _ := newRPCIntegrationTestNode(t)
	server := rpc.NewServer(rpc.ServerConfig{Logger: node.logger}, rpc.NewDefaultRouter(node))
	sourceState := mustStructureKeyPair("rpc-privacy-source-state")
	receiverState := mustStructureKeyPair("rpc-privacy-change-receiver-state")
	receiverAuthority := mustStructureKeyPair("rpc-privacy-change-receiver-authority")
	sourceCommitment := blockchainTestHash(t, "rpc-privacy-change-source-note")
	nullifier := blockchainTestHash(t, "rpc-privacy-change-nullifier")
	addPrivacyStateWithNotesToLedger(t, node.ledger, sourceState.PublicKey, []structure.PrivacyNoteRecord{
		{
			Commitment:     sourceCommitment,
			SpendAuthority: source.PublicKey,
			Amount:         1000,
			VMVersion:      structure.PrivacyStateVersion,
			EncryptedNote:  []byte("source-note"),
		},
	}, 1000)
	addPrivacyStateAccountToLedger(t, node.ledger, receiverState.PublicKey)

	response := postPosNodeJSONRPC(t, server, 1, rpc.MethodPrivacyTransferToReceiver, []any{
		"rpc-source",
		encodeTestProtocolAddress(protocolAddressPrivacy, sourceState.PublicKey[:], "z"),
		sourceCommitment.String(),
		nullifier.String(),
		encodeTestProtocolAddress(protocolAddressPrivacy, receiverState.PublicKey[:], "z"),
		encodeTestProtocolAddress(protocolAddressTransparent, receiverAuthority.PublicKey[:], "t"),
		uint64(250),
		"",
		"",
		uint64(0),
	})
	result := decodePosNodeRPCResult[rpc.PrivacyTransactionResult](t, response)
	if result.Signature == "" || result.ChangeCommitment == "" || result.ChangeLamports != "750" {
		t.Fatalf("result = %+v, want signature and 750 lamports change", result)
	}
	if len(node.mempool) != 1 {
		t.Fatalf("mempool size = %d, want 1", len(node.mempool))
	}
	privacyInstruction, err := structure.UnmarshalPrivacyInstructionBinary(node.mempool[0].Instructions[0].Data)
	if err != nil {
		t.Fatalf("UnmarshalPrivacyInstructionBinary() error = %v", err)
	}
	if privacyInstruction.Transfer.Amount != 250 || privacyInstruction.Transfer.ChangeAmount != 750 {
		t.Fatalf("transfer params = %+v, want output 250 and change 750", privacyInstruction.Transfer)
	}
	if privacyInstruction.Transfer.ChangeSpendAuthority != source.PublicKey {
		t.Fatalf("change spend authority = %s, want %s", privacyInstruction.Transfer.ChangeSpendAuthority.String(), source.PublicKey.String())
	}
}

func TestHTTPJSONRPCPrivacyWithdrawBuildsChangeNote(t *testing.T) {
	node, source, destination := newRPCIntegrationTestNode(t)
	server := rpc.NewServer(rpc.ServerConfig{Logger: node.logger}, rpc.NewDefaultRouter(node))
	sourceState := mustStructureKeyPair("rpc-privacy-withdraw-source-state")
	sourceCommitment := blockchainTestHash(t, "rpc-privacy-withdraw-source-note")
	nullifier := blockchainTestHash(t, "rpc-privacy-withdraw-nullifier")
	addPrivacyStateWithNotesToLedger(t, node.ledger, sourceState.PublicKey, []structure.PrivacyNoteRecord{
		{
			Commitment:     sourceCommitment,
			SpendAuthority: source.PublicKey,
			Amount:         1000,
			VMVersion:      structure.PrivacyStateVersion,
			EncryptedNote:  []byte("withdraw-source-note"),
		},
	}, 1000)

	response := postPosNodeJSONRPC(t, server, 1, rpc.MethodPrivacyWithdraw, []any{
		"rpc-source",
		encodeTestProtocolAddress(protocolAddressPrivacy, sourceState.PublicKey[:], "z"),
		sourceCommitment.String(),
		nullifier.String(),
		encodeTestProtocolAddress(protocolAddressTransparent, destination.PublicKey[:], "t"),
		uint64(400),
		"",
		"",
		uint64(0),
	})
	result := decodePosNodeRPCResult[rpc.PrivacyTransactionResult](t, response)
	if result.Signature == "" || result.ChangeCommitment == "" || result.ChangeLamports != "600" {
		t.Fatalf("result = %+v, want signature and 600 lamports change", result)
	}
	if len(node.mempool) != 1 {
		t.Fatalf("mempool size = %d, want 1", len(node.mempool))
	}
	privacyInstruction, err := structure.UnmarshalPrivacyInstructionBinary(node.mempool[0].Instructions[0].Data)
	if err != nil {
		t.Fatalf("UnmarshalPrivacyInstructionBinary() error = %v", err)
	}
	if privacyInstruction.Withdraw.Amount != 400 || privacyInstruction.Withdraw.ChangeAmount != 600 {
		t.Fatalf("withdraw params = %+v, want output 400 and change 600", privacyInstruction.Withdraw)
	}
	if privacyInstruction.Withdraw.ChangeSpendAuthority != source.PublicKey {
		t.Fatalf("change spend authority = %s, want %s", privacyInstruction.Withdraw.ChangeSpendAuthority.String(), source.PublicKey.String())
	}
}

func TestHTTPJSONRPCStakeRequiresSignedTransaction(t *testing.T) {
	node, _, _ := newRPCIntegrationTestNode(t)
	server := rpc.NewServer(rpc.ServerConfig{Logger: node.logger}, rpc.NewDefaultRouter(node))
	validator := mustStructureKeyPair("rpc-stake-mismatch-validator")
	requiredStaker := mustStructureKeyPair("rpc-stake-mismatch-required-staker")
	consensusKey := mustStructureKeyPair("rpc-stake-mismatch-consensus")
	addValidatorStakeAccountToLedger(t, node.ledger, validator.PublicKey, stakeprogram.ValidatorState{
		ConsensusPublicKey: consensusKey.PublicKey,
		StakerAccount:      requiredStaker.PublicKey,
		P2PPeerID:          "rpc-stake-mismatch-peer",
		Status:             stakeprogram.ValidatorStatusActive,
		ActiveStake:        stakeprogram.MinimumStakeLamports,
		ActivationEpoch:    1,
	})

	response := postPosNodeJSONRPC(t, server, 1, rpc.MethodStake, []any{
		"rpc-source",
		validator.PublicKey.String(),
		uint64(stakeprogram.MinimumStakeLamports),
	})
	if response.Error == nil {
		t.Fatal("response error = nil, want signed transaction requirement")
	}
	errorText := fmt.Sprint(response.Error.Data)
	if !strings.Contains(errorText, "requires wallet-local signing") {
		t.Fatalf("error data = %q, want signed transaction requirement", errorText)
	}
	if strings.Contains(errorText, "preflight transaction failed") {
		t.Fatalf("error data = %q, should fail before preflight", errorText)
	}
	if strings.Contains(errorText, requiredStaker.PublicKey.String()) {
		t.Fatalf("error data = %q, should not expose stake account details before signed transaction", errorText)
	}
}

func newRPCIntegrationTestNode(t *testing.T) (*posNode, structure.SolanaKeyPair, structure.SolanaKeyPair) {
	t.Helper()
	source := mustStructureKeyPair("rpc-source")
	destination := mustStructureKeyPair("rpc-destination")
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	executor, err := newRuntimeExecutor(logger)
	if err != nil {
		t.Fatalf("newRuntimeExecutor() error = %v", err)
	}
	ledger, err := blockchain.NewLedgerFromGenesis(nil, blockchain.GenesisConfig{
		ChainID: "posnode-rpc-e2e",
		FundedAccounts: []blockchain.GenesisAccount{
			{Address: source.PublicKey, Lamports: 1_000_000_000},
			{Address: destination.PublicKey, Lamports: 1_000_000},
		},
	})
	if err != nil {
		t.Fatalf("NewLedgerFromGenesis() error = %v", err)
	}
	ledger.SetLogger(logger)
	node := &posNode{
		config: nodeConfig{
			MempoolMaxTransactions:      10,
			MempoolTransactionTTLMillis: 60_000,
		},
		logger:           logger,
		ledger:           ledger,
		executor:         executor,
		blockhashQueue:   structure.NewBlockhashQueue(150),
		seenTransactions: make(map[string]struct{}),
	}
	if err := node.ensureHeadBlockhashAvailable(ledger.Head()); err != nil {
		t.Fatalf("ensureHeadBlockhashAvailable() error = %v", err)
	}
	return node, source, destination
}

func addPrivacyStateAccountToLedger(t *testing.T, ledger *blockchain.Ledger, stateAddress structure.PublicKey) {
	t.Helper()
	addPrivacyStateWithNotesToLedger(t, ledger, stateAddress, nil, 0)
}

func addPrivacyStateWithNotesToLedger(t *testing.T, ledger *blockchain.Ledger, stateAddress structure.PublicKey, notes []structure.PrivacyNoteRecord, escrowLamports uint64) {
	t.Helper()
	privacyState := buildPrivacyStateForTest(t, notes, nil)
	data, err := privacyState.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary(privacy state) error = %v", err)
	}
	rentLamports, err := structure.MinimumBalanceForRentExemption(blockchain.PrivacyStateRentReserveBytes)
	if err != nil {
		t.Fatalf("MinimumBalanceForRentExemption() error = %v", err)
	}
	account, err := structure.NewAccount(rentLamports+escrowLamports, data, structure.DefaultBuiltinProgramIDs.Privacy, false, 0)
	if err != nil {
		t.Fatalf("NewAccount() error = %v", err)
	}
	nextState := ledger.State()
	nextState.Accounts = append(nextState.Accounts, structure.AddressedAccount{Address: stateAddress, Account: account})
	proposal, _ := blockchainTestProposalFromHead(t, ledger.Head(), nextState, ledger.Head().Slot+1, ledger.Head().Height+1, "rpc-privacy-state-account")
	if _, err := ledger.CommitBlock(blockchain.CommitBlockRequest{Proposal: proposal, NextState: nextState}); err != nil {
		t.Fatalf("CommitBlock() error = %v", err)
	}
}

// buildPrivacyStateForTest 构造一致隐私状态 + 保证测试数据满足 Merkle 和负债校验。
func buildPrivacyStateForTest(t *testing.T, notes []structure.PrivacyNoteRecord, spentNullifiers []structure.Hash) structure.PrivacyState {
	t.Helper()
	unspentNoteLiability := uint64(0)
	for _, note := range notes {
		if note.Spent {
			continue
		}
		if ^uint64(0)-unspentNoteLiability < note.Amount {
			t.Fatalf("privacy test note liability overflow")
		}
		unspentNoteLiability += note.Amount
	}
	merkleRoot, err := structure.ComputePrivacyMerkleRoot(notes)
	if err != nil {
		t.Fatalf("ComputePrivacyMerkleRoot() error = %v", err)
	}
	return structure.PrivacyState{
		Version:              structure.PrivacyStateVersion,
		Notes:                notes,
		SpentNullifiers:      spentNullifiers,
		MerkleRoot:           merkleRoot,
		PrivacyPoolLamports:  unspentNoteLiability,
		UnspentNoteLiability: unspentNoteLiability,
	}
}

func addValidatorStakeAccountToLedger(t *testing.T, ledger *blockchain.Ledger, validatorAddress structure.PublicKey, validatorState stakeprogram.ValidatorState) {
	t.Helper()
	data, err := validatorState.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary(validator state) error = %v", err)
	}
	account, err := structure.NewAccount(stakeprogram.MinimumStakeLamports, data, structure.DefaultBuiltinProgramIDs.Stake, false, 0)
	if err != nil {
		t.Fatalf("NewAccount(validator stake) error = %v", err)
	}
	nextState := ledger.State()
	nextState.Accounts = append(nextState.Accounts, structure.AddressedAccount{Address: validatorAddress, Account: account})
	proposal, _ := blockchainTestProposalFromHead(t, ledger.Head(), nextState, ledger.Head().Slot+1, ledger.Head().Height+1, "rpc-validator-stake-account")
	if _, err := ledger.CommitBlock(blockchain.CommitBlockRequest{Proposal: proposal, NextState: nextState}); err != nil {
		t.Fatalf("CommitBlock() error = %v", err)
	}
}

func newRPCTransferTransaction(t *testing.T, node *posNode, source structure.SolanaKeyPair, destination structure.PublicKey, amount uint64) structure.Transaction {
	t.Helper()
	transaction, err := blockchain.NewTransferTransaction(source, destination, amount, node.ledger.Head().BlockHash)
	if err != nil {
		t.Fatalf("NewTransferTransaction() error = %v", err)
	}
	return transaction
}

func mustEstimateTransactionFeeDetails(t *testing.T, transaction structure.Transaction) structure.FeeDetails {
	t.Helper()
	feeDetails, err := estimateTransactionFeeDetails(transaction)
	if err != nil {
		t.Fatalf("estimateTransactionFeeDetails() error = %v", err)
	}
	return feeDetails
}

func assertRPCTransactionFeeDetails(t *testing.T, detail rpc.TransactionDetailResult, expected structure.FeeDetails) {
	t.Helper()
	if detail.FeeLamports != expected.TotalFee {
		t.Fatalf("detail fee = %d, want %d", detail.FeeLamports, expected.TotalFee)
	}
	if detail.BaseFeeLamports != expected.BaseFee {
		t.Fatalf("detail base fee = %d, want %d", detail.BaseFeeLamports, expected.BaseFee)
	}
	if detail.PrioritizationFeeLamports != expected.PrioritizationFee {
		t.Fatalf("detail priority fee = %d, want %d", detail.PrioritizationFeeLamports, expected.PrioritizationFee)
	}
	if detail.LeaderFeeLamports != expected.ValidatorFee {
		t.Fatalf("detail leader fee = %d, want %d", detail.LeaderFeeLamports, expected.ValidatorFee)
	}
	if detail.BurnedFeeLamports != expected.BurnedFee {
		t.Fatalf("detail burned fee = %d, want %d", detail.BurnedFeeLamports, expected.BurnedFee)
	}
}

func blockchainTestProposalFromHead(t *testing.T, head blockchain.Head, state consensus.ChainState, slot uint64, height uint64, seed string) (consensus.BlockProposal, consensus.ChainState) {
	t.Helper()
	stateRoot, err := state.RootHash()
	if err != nil {
		t.Fatalf("state root: %v", err)
	}
	return consensus.BlockProposal{
		Header: consensus.BlockHeader{
			ChainID:            head.ChainID,
			Slot:               slot,
			Height:             height,
			ParentHash:         head.BlockHash,
			PreviousQCHash:     head.QCHash,
			LeaderID:           consensus.NewValidatorID(mustStructureKeyPair(seed).PublicKey),
			EpochID:            head.EpochID,
			StateRoot:          stateRoot,
			AccountRoot:        stateRoot,
			TimestampUnixMilli: 1,
		},
	}, state
}

func blockchainTestHash(t *testing.T, seed string) structure.Hash {
	t.Helper()
	hash, err := structure.NewHash(utils.SHA256([]byte(seed)))
	if err != nil {
		t.Fatalf("NewHash(%q) error = %v", seed, err)
	}
	return hash
}

func encodeRPCTransaction(t *testing.T, transaction structure.Transaction) string {
	t.Helper()
	transactionBytes, err := transaction.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	return base64.StdEncoding.EncodeToString(transactionBytes)
}

func postPosNodeJSONRPC(t *testing.T, handler http.Handler, id int, method string, params []any) posNodeRPCResponse {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", response.Code, response.Body.String())
	}

	var decoded posNodeRPCResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return decoded
}

func decodePosNodeRPCResult[T any](t *testing.T, response posNodeRPCResponse) T {
	t.Helper()
	var zero T
	if response.Error != nil {
		t.Fatalf("response error = %+v", response.Error)
	}
	if len(response.Result) == 0 {
		t.Fatal("response result is empty")
	}
	if err := json.Unmarshal(response.Result, &zero); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	return zero
}
