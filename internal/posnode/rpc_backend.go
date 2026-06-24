package posnode

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"solana_golang/blockchain"
	"solana_golang/consensus"
	"solana_golang/p2p"
	"solana_golang/programs/stake"
	"solana_golang/rpc"
	runtimepkg "solana_golang/runtime"
	"solana_golang/structure"
	"solana_golang/utils"
)

const (
	protocolAddressTransparent     byte = 0x00
	protocolAddressPrivacy         byte = 0x01
	protocolAddressSize                 = structure.PublicKeySize + 1
	rpcTransactionBroadcastTimeout      = 2 * time.Second
)

type privacyChangeArtifacts struct {
	amount        uint64
	commitment    structure.Hash
	encryptedNote []byte
	auditRecords  []structure.PrivacyAuditRecord
}

func (node *posNode) GetBalance(ctx context.Context, address string) (rpc.BalanceResult, error) {
	_ = ctx
	publicKey, _, err := decodeProtocolPublicKey(address, "balance address")
	if err != nil {
		return rpc.BalanceResult{}, fmt.Errorf("posnode: decode balance address: %w", err)
	}
	account, found, err := node.ledger.Account(publicKey)
	if err != nil {
		return rpc.BalanceResult{}, err
	}
	if !found {
		return rpc.BalanceResult{Value: 0}, nil
	}
	return rpc.BalanceResult{Value: account.Lamports}, nil
}

func (node *posNode) GetLatestBlockhash(ctx context.Context) (rpc.LatestBlockhashResult, error) {
	_ = ctx
	head := node.ledger.Head()
	if err := node.ensureHeadBlockhashAvailable(head); err != nil {
		return rpc.LatestBlockhashResult{}, err
	}
	if ^uint64(0)-head.Slot < structure.MaxRecentBlockhashAgeSlots {
		return rpc.LatestBlockhashResult{}, fmt.Errorf("posnode: latest blockhash slot overflows last valid slot")
	}
	if ^uint64(0)-head.Height < structure.MaxRecentBlockhashAgeSlots {
		return rpc.LatestBlockhashResult{}, fmt.Errorf("posnode: latest blockhash height overflows last valid block height")
	}
	return rpc.LatestBlockhashResult{
		Blockhash:            head.BlockHash.String(),
		Slot:                 head.Slot,
		Height:               head.Height,
		LastValidSlot:        head.Slot + structure.MaxRecentBlockhashAgeSlots,
		LastValidBlockHeight: head.Height + structure.MaxRecentBlockhashAgeSlots,
	}, nil
}

func (node *posNode) GetAccountType(ctx context.Context, address string) (rpc.AccountTypeResult, error) {
	_ = ctx
	publicKey, _, err := decodeProtocolPublicKey(address, "account type address")
	if err != nil {
		return rpc.AccountTypeResult{}, fmt.Errorf("posnode: decode account type address: %w", err)
	}
	account, found, err := node.ledger.Account(publicKey)
	if err != nil {
		return rpc.AccountTypeResult{}, err
	}
	if !found {
		return rpc.AccountTypeResult{Address: publicKey.String(), Exists: false, Type: "unknown"}, nil
	}
	return rpc.AccountTypeResult{
		Address: publicKey.String(),
		Exists:  true,
		Owner:   account.Owner.String(),
		Type:    accountTypeName(account.Owner),
	}, nil
}

func (node *posNode) SendTransaction(ctx context.Context, encodedTransaction string) (string, error) {
	transactionBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encodedTransaction))
	if err != nil {
		return "", fmt.Errorf("posnode: decode transaction: %w", err)
	}
	transaction, err := structure.UnmarshalTransactionBinary(transactionBytes)
	if err != nil {
		return "", fmt.Errorf("posnode: unmarshal transaction: %w", err)
	}
	return node.submitTransaction(ctx, transaction, "send_transaction")
}

func (node *posNode) GetBlock(ctx context.Context, slot uint64) (rpc.BlockResult, error) {
	_ = ctx
	proposal, blockHash, found, err := node.ledger.BlockByHeight(slot)
	if err != nil {
		return rpc.BlockResult{}, err
	}
	if !found {
		return rpc.BlockResult{Slot: slot}, nil
	}
	transactions := make([]any, 0, len(proposal.Transactions))
	for _, transaction := range proposal.Transactions {
		transactionID, err := transaction.TxIDString()
		if err != nil {
			return rpc.BlockResult{}, err
		}
		transactions = append(transactions, transactionID)
	}
	return rpc.BlockResult{
		Slot:         proposal.Header.Height,
		Blockhash:    blockHash.String(),
		ParentSlot:   proposal.Header.Height - 1,
		Transactions: transactions,
	}, nil
}

func (node *posNode) GetTransaction(ctx context.Context, signature string) (rpc.TransactionDetailResult, error) {
	_ = ctx
	signature = strings.TrimSpace(signature)
	if signature == "" {
		return rpc.TransactionDetailResult{}, fmt.Errorf("posnode: transaction signature is empty")
	}

	mempoolResult, found, err := node.lookupMempoolTransaction(signature)
	if err != nil {
		return rpc.TransactionDetailResult{}, err
	}
	if found {
		return mempoolResult, nil
	}

	proposal, blockHash, found, err := node.ledger.TransactionByID(signature)
	if err != nil {
		return rpc.TransactionDetailResult{}, fmt.Errorf("posnode: lookup committed transaction: %w", err)
	}
	if !found {
		rejectedResult, rejected := node.lookupRejectedTransaction(signature)
		if rejected {
			return rejectedResult, nil
		}
		return rpc.TransactionDetailResult{
			Signature: signature,
			Found:     false,
			Location:  "unknown",
			Status:    "not_found",
		}, nil
	}

	for _, transaction := range proposal.Transactions {
		transactionID, err := transaction.TxIDString()
		if err != nil {
			return rpc.TransactionDetailResult{}, fmt.Errorf("posnode: decode committed transaction id: %w", err)
		}
		if transactionID != signature {
			continue
		}
		return node.committedTransactionResult(signature, transaction, proposal, blockHash), nil
	}
	return rpc.TransactionDetailResult{}, fmt.Errorf("posnode: committed transaction index mismatch for %s", signature)
}

func (node *posNode) GetAddressTransactions(ctx context.Context, address string, cursor string, limit int) (rpc.AccountTransactionHistoryResult, error) {
	_ = ctx
	publicKey, addressType, err := decodeProtocolPublicKey(address, "history address")
	if err != nil {
		return rpc.AccountTransactionHistoryResult{}, fmt.Errorf("posnode: decode history address: %w", err)
	}
	if addressType == protocolAddressPrivacy {
		return rpc.AccountTransactionHistoryResult{}, fmt.Errorf("posnode: public address history does not support privacy addresses")
	}
	account, found, err := node.ledger.Account(publicKey)
	if err != nil {
		return rpc.AccountTransactionHistoryResult{}, err
	}
	if found && account.Owner == structure.DefaultBuiltinProgramIDs.Privacy {
		return rpc.AccountTransactionHistoryResult{}, fmt.Errorf("posnode: public address history does not support privacy state accounts")
	}
	page, err := node.ledger.AddressHistory(publicKey, strings.TrimSpace(cursor), limit)
	if err != nil {
		return rpc.AccountTransactionHistoryResult{}, err
	}
	return accountTransactionHistoryResult(publicKey, page, node.ledger.Head().FinalizedHeight), nil
}

func (node *posNode) GetContractPrograms(ctx context.Context, limit int) (rpc.ContractProgramListResult, error) {
	_ = ctx
	records, err := node.ledger.ContractPrograms(limit)
	if err != nil {
		return rpc.ContractProgramListResult{}, fmt.Errorf("posnode: list contract programs: %w", err)
	}
	return contractProgramListResult(records), nil
}

func (node *posNode) TreasuryTransfer(ctx context.Context, destination string, lamports uint64) (string, error) {
	destinationKey, _, err := decodeProtocolPublicKey(destination, "destination")
	if err != nil {
		return "", fmt.Errorf("posnode: decode destination: %w", err)
	}
	treasury, keySource, err := node.treasuryKeyPair()
	if err != nil {
		return "", err
	}
	transaction, err := blockchain.NewTreasuryTransferTransaction(treasury, destinationKey, lamports, node.ledger.Head().BlockHash)
	if err != nil {
		return "", err
	}
	return node.submitTransaction(ctx, transaction, "treasury_transfer",
		slog.String("destination", destinationKey.String()),
		slog.Uint64("lamports", lamports),
		slog.String("treasury_key_source", keySource),
	)
}

func (node *posNode) Transfer(ctx context.Context, sourceSeed string, destination string, lamports uint64) (string, error) {
	source, err := keyPairFromSeed(sourceSeed)
	if err != nil {
		return "", err
	}
	destinationKey, _, err := decodeProtocolPublicKey(destination, "destination")
	if err != nil {
		return "", fmt.Errorf("posnode: decode destination: %w", err)
	}
	transaction, err := blockchain.NewTransferTransaction(source, destinationKey, lamports, node.ledger.Head().BlockHash)
	if err != nil {
		return "", err
	}
	return node.submitTransaction(ctx, transaction, "transfer",
		slog.String("source", source.PublicKey.String()),
		slog.String("destination", destinationKey.String()),
		slog.Uint64("lamports", lamports),
	)
}

func (node *posNode) GetPrivacyState(ctx context.Context, stateAddress string) (rpc.PrivacyStateResult, error) {
	_ = ctx
	stateKey, _, err := decodeProtocolPublicKey(stateAddress, "privacy state")
	if err != nil {
		return rpc.PrivacyStateResult{}, fmt.Errorf("posnode: decode privacy state: %w", err)
	}
	state, err := node.loadPrivacyState(stateKey)
	if err != nil {
		return rpc.PrivacyStateResult{}, err
	}
	return privacyStateResult(stateKey, state), nil
}

func (node *posNode) GetPrivacyBalance(ctx context.Context, stateAddress string, spendAuthority string) (rpc.PrivacyBalanceResult, error) {
	_ = ctx
	stateKey, _, err := decodeProtocolPublicKey(stateAddress, "privacy state")
	if err != nil {
		return rpc.PrivacyBalanceResult{}, fmt.Errorf("posnode: decode privacy state for balance: %w", err)
	}
	authorityKey, authorityType, err := decodeProtocolPublicKey(spendAuthority, "privacy spend authority")
	if err != nil {
		return rpc.PrivacyBalanceResult{}, fmt.Errorf("posnode: decode privacy spend authority: %w", err)
	}
	if authorityType == protocolAddressPrivacy {
		return rpc.PrivacyBalanceResult{}, fmt.Errorf("posnode: privacy spend authority must be a transparent public key")
	}
	summary, err := node.ledger.PrivacyBalance(stateKey, authorityKey)
	if err != nil {
		return rpc.PrivacyBalanceResult{}, err
	}
	return privacyBalanceResult(stateKey, authorityKey, summary), nil
}

func (node *posNode) PrivacyDeposit(ctx context.Context, sourceSeed string, stateSeed string, lamports uint64, auditor string, auditSecret string, expiresAtSlot uint64) (rpc.PrivacyTransactionResult, error) {
	source, err := keyPairFromSeed(sourceSeed)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	stateAccount, err := keyPairFromSeed(stateSeed)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, fmt.Errorf("posnode: build privacy state keypair: %w", err)
	}
	_, found, err := node.ledger.Account(stateAccount.PublicKey)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	commitment, err := randomPrivacyHash()
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	auditRecords, err := node.buildPrivacyAuditRecords(auditor, auditSecret, expiresAtSlot, structure.PrivacyAuditScopeRegulatory, structure.PrivacyAuditPayload{
		Version:         structure.PrivacyAuditPayloadVersion,
		TransactionType: structure.PrivacyInstructionDeposit,
		Commitment:      commitment,
		Amount:          lamports,
		Slot:            node.currentAuditSlot(),
	})
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	transaction, err := blockchain.NewPrivacyDepositTransaction(blockchain.PrivacyDepositTransactionParams{
		Source:         source,
		StateAccount:   stateAccount,
		SpendAuthority: source.PublicKey,
		Amount:         lamports,
		Commitment:     commitment,
		EncryptedNote:  privacyNoteBytes("deposit", lamports, commitment, structure.Hash{}),
		AuditRecords:   auditRecords,
		CreateState:    !found,
	}, node.ledger.Head().BlockHash)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	signature, err := node.submitTransaction(ctx, transaction, "privacy_deposit",
		slog.String("source", source.PublicKey.String()),
		slog.String("privacy_state", stateAccount.PublicKey.String()),
		slog.Uint64("lamports", lamports),
	)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	return rpc.PrivacyTransactionResult{Signature: signature, PrivacyState: stateAccount.PublicKey.String(), Commitment: commitment.String()}, nil
}

func (node *posNode) PrivacyDepositToState(ctx context.Context, sourceSeed string, stateAddress string, lamports uint64, auditor string, auditSecret string, expiresAtSlot uint64) (rpc.PrivacyTransactionResult, error) {
	source, err := keyPairFromSeed(sourceSeed)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	stateKey, _, err := decodeProtocolPublicKey(stateAddress, "privacy state")
	if err != nil {
		return rpc.PrivacyTransactionResult{}, fmt.Errorf("posnode: decode privacy state: %w", err)
	}
	if err := node.requirePrivacyStateAccount(stateKey); err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	commitment, err := randomPrivacyHash()
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	auditRecords, err := node.buildPrivacyAuditRecords(auditor, auditSecret, expiresAtSlot, structure.PrivacyAuditScopeRegulatory, structure.PrivacyAuditPayload{
		Version:         structure.PrivacyAuditPayloadVersion,
		TransactionType: structure.PrivacyInstructionDeposit,
		Commitment:      commitment,
		Amount:          lamports,
		Slot:            node.currentAuditSlot(),
	})
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	transaction, err := blockchain.NewPrivacyDepositTransaction(blockchain.PrivacyDepositTransactionParams{
		Source:         source,
		StateAccount:   structure.SolanaKeyPair{PublicKey: stateKey},
		SpendAuthority: source.PublicKey,
		Amount:         lamports,
		Commitment:     commitment,
		EncryptedNote:  privacyNoteBytes("deposit", lamports, commitment, structure.Hash{}),
		AuditRecords:   auditRecords,
		CreateState:    false,
	}, node.ledger.Head().BlockHash)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	signature, err := node.submitTransaction(ctx, transaction, "privacy_deposit_to_state",
		slog.String("source", source.PublicKey.String()),
		slog.String("privacy_state", stateKey.String()),
		slog.Uint64("lamports", lamports),
	)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	return rpc.PrivacyTransactionResult{Signature: signature, PrivacyState: stateKey.String(), Commitment: commitment.String()}, nil
}

func (node *posNode) PrivacyDepositToReceiver(ctx context.Context, sourceSeed string, stateAddress string, spendAuthority string, lamports uint64, auditor string, auditSecret string, expiresAtSlot uint64) (rpc.PrivacyTransactionResult, error) {
	source, err := keyPairFromSeed(sourceSeed)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	stateKey, spendAuthorityKey, err := node.decodePrivacyReceiver(stateAddress, spendAuthority)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	commitment, err := randomPrivacyHash()
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	auditRecords, err := node.buildPrivacyAuditRecords(auditor, auditSecret, expiresAtSlot, structure.PrivacyAuditScopeRegulatory, structure.PrivacyAuditPayload{
		Version:         structure.PrivacyAuditPayloadVersion,
		TransactionType: structure.PrivacyInstructionDeposit,
		Commitment:      commitment,
		Amount:          lamports,
		Slot:            node.currentAuditSlot(),
	})
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	transaction, err := blockchain.NewPrivacyDepositTransaction(blockchain.PrivacyDepositTransactionParams{
		Source:         source,
		StateAccount:   structure.SolanaKeyPair{PublicKey: stateKey},
		SpendAuthority: spendAuthorityKey,
		Amount:         lamports,
		Commitment:     commitment,
		EncryptedNote:  privacyNoteBytes("deposit", lamports, commitment, structure.Hash{}),
		AuditRecords:   auditRecords,
		CreateState:    false,
	}, node.ledger.Head().BlockHash)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	signature, err := node.submitTransaction(ctx, transaction, "privacy_deposit_to_receiver",
		slog.String("source", source.PublicKey.String()),
		slog.String("privacy_state", stateKey.String()),
		slog.String("spend_authority", spendAuthorityKey.String()),
		slog.Uint64("lamports", lamports),
	)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	return rpc.PrivacyTransactionResult{Signature: signature, PrivacyState: stateKey.String(), Commitment: commitment.String()}, nil
}

func (node *posNode) PrivacyWithdraw(ctx context.Context, authoritySeed string, stateAddress string, destination string, commitment string, nullifier string, lamports uint64, auditor string, auditSecret string, expiresAtSlot uint64) (rpc.PrivacyTransactionResult, error) {
	authority, stateKey, commitmentHash, nullifierHash, err := parsePrivacySpendInputs(authoritySeed, stateAddress, commitment, nullifier)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	destinationKey, _, err := decodeProtocolPublicKey(destination, "privacy withdraw destination")
	if err != nil {
		return rpc.PrivacyTransactionResult{}, fmt.Errorf("posnode: decode privacy withdraw destination: %w", err)
	}
	sourceState, err := node.loadPrivacyState(stateKey)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	sourceNote, err := privacySpendNote(sourceState, commitmentHash)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	auditSlot := node.currentAuditSlot()
	changeOutput, err := node.buildPrivacyChangeArtifacts(
		"withdraw_change",
		structure.PrivacyInstructionWithdraw,
		sourceNote.Amount,
		lamports,
		commitmentHash,
		nullifierHash,
		auditor,
		auditSecret,
		expiresAtSlot,
		auditSlot,
	)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	auditRecords, err := node.buildPrivacyAuditRecords(auditor, auditSecret, expiresAtSlot, structure.PrivacyAuditScopeRegulatory, structure.PrivacyAuditPayload{
		Version:          structure.PrivacyAuditPayloadVersion,
		TransactionType:  structure.PrivacyInstructionWithdraw,
		Commitment:       commitmentHash,
		Nullifier:        nullifierHash,
		OutputCommitment: changeOutput.commitment,
		Amount:           lamports,
		Slot:             auditSlot,
	})
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	transaction, err := blockchain.NewPrivacyWithdrawTransaction(blockchain.PrivacyWithdrawTransactionParams{
		Authority:            authority,
		StateAddress:         stateKey,
		Destination:          destinationKey,
		Amount:               lamports,
		SourceCommitment:     commitmentHash,
		Nullifier:            nullifierHash,
		AuditRecords:         auditRecords,
		ChangeAmount:         changeOutput.amount,
		ChangeCommitment:     changeOutput.commitment,
		ChangeSpendAuthority: sourceNote.SpendAuthority,
		ChangeEncryptedNote:  changeOutput.encryptedNote,
		ChangeAuditRecords:   changeOutput.auditRecords,
	}, node.ledger.Head().BlockHash)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	signature, err := node.submitTransaction(ctx, transaction, "privacy_withdraw",
		slog.String("authority", authority.PublicKey.String()),
		slog.String("privacy_state", stateKey.String()),
		slog.Uint64("lamports", lamports),
	)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	return privacySpendResult(signature, stateKey, commitmentHash, nullifierHash, structure.Hash{}, changeOutput), nil
}

func (node *posNode) PrivacyTransfer(ctx context.Context, authoritySeed string, stateAddress string, commitment string, nullifier string, recipient string, lamports uint64, auditor string, auditSecret string, expiresAtSlot uint64) (rpc.PrivacyTransactionResult, error) {
	authority, stateKey, commitmentHash, nullifierHash, err := parsePrivacySpendInputs(authoritySeed, stateAddress, commitment, nullifier)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	recipientKey, _, err := decodeProtocolPublicKey(recipient, "privacy recipient")
	if err != nil {
		return rpc.PrivacyTransactionResult{}, fmt.Errorf("posnode: decode privacy recipient: %w", err)
	}
	sourceState, err := node.loadPrivacyState(stateKey)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	sourceNote, err := privacySpendNote(sourceState, commitmentHash)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	outputCommitment, err := randomPrivacyHash()
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	auditSlot := node.currentAuditSlot()
	changeOutput, err := node.buildPrivacyChangeArtifacts(
		"transfer_change",
		structure.PrivacyInstructionTransfer,
		sourceNote.Amount,
		lamports,
		commitmentHash,
		nullifierHash,
		auditor,
		auditSecret,
		expiresAtSlot,
		auditSlot,
	)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	auditRecords, err := node.buildPrivacyAuditRecords(auditor, auditSecret, expiresAtSlot, structure.PrivacyAuditScopeRegulatory, structure.PrivacyAuditPayload{
		Version:          structure.PrivacyAuditPayloadVersion,
		TransactionType:  structure.PrivacyInstructionTransfer,
		Commitment:       commitmentHash,
		Nullifier:        nullifierHash,
		OutputCommitment: outputCommitment,
		Amount:           lamports,
		Slot:             auditSlot,
	})
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	transaction, err := blockchain.NewPrivacyTransferTransaction(blockchain.PrivacyTransferTransactionParams{
		Authority:            authority,
		StateAddress:         stateKey,
		Amount:               lamports,
		SourceCommitment:     commitmentHash,
		Nullifier:            nullifierHash,
		OutputCommitment:     outputCommitment,
		OutputSpendAuthority: recipientKey,
		OutputEncryptedNote:  privacyNoteBytes("transfer", lamports, outputCommitment, nullifierHash),
		OutputAuditRecords:   auditRecords,
		ChangeAmount:         changeOutput.amount,
		ChangeCommitment:     changeOutput.commitment,
		ChangeSpendAuthority: sourceNote.SpendAuthority,
		ChangeEncryptedNote:  changeOutput.encryptedNote,
		ChangeAuditRecords:   changeOutput.auditRecords,
	}, node.ledger.Head().BlockHash)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	signature, err := node.submitTransaction(ctx, transaction, "privacy_transfer",
		slog.String("authority", authority.PublicKey.String()),
		slog.String("privacy_state", stateKey.String()),
		slog.Uint64("lamports", lamports),
	)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	return privacySpendResult(signature, stateKey, commitmentHash, nullifierHash, outputCommitment, changeOutput), nil
}

func (node *posNode) PrivacyTransferToReceiver(ctx context.Context, authoritySeed string, sourceStateAddress string, commitment string, nullifier string, destinationStateAddress string, destinationSpendAuthority string, lamports uint64, auditor string, auditSecret string, expiresAtSlot uint64) (rpc.PrivacyTransactionResult, error) {
	authority, sourceStateKey, commitmentHash, nullifierHash, err := parsePrivacySpendInputs(authoritySeed, sourceStateAddress, commitment, nullifier)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	destinationStateKey, destinationSpendAuthorityKey, err := node.decodePrivacyReceiver(destinationStateAddress, destinationSpendAuthority)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	sourceState, err := node.loadPrivacyState(sourceStateKey)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	sourceNote, err := privacySpendNote(sourceState, commitmentHash)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	outputCommitment, err := randomPrivacyHash()
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	auditSlot := node.currentAuditSlot()
	changeOutput, err := node.buildPrivacyChangeArtifacts(
		"transfer_change",
		structure.PrivacyInstructionTransfer,
		sourceNote.Amount,
		lamports,
		commitmentHash,
		nullifierHash,
		auditor,
		auditSecret,
		expiresAtSlot,
		auditSlot,
	)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	auditRecords, err := node.buildPrivacyAuditRecords(auditor, auditSecret, expiresAtSlot, structure.PrivacyAuditScopeRegulatory, structure.PrivacyAuditPayload{
		Version:          structure.PrivacyAuditPayloadVersion,
		TransactionType:  structure.PrivacyInstructionTransfer,
		Commitment:       commitmentHash,
		Nullifier:        nullifierHash,
		OutputCommitment: outputCommitment,
		Amount:           lamports,
		Slot:             auditSlot,
	})
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	transaction, err := blockchain.NewPrivacyTransferTransaction(blockchain.PrivacyTransferTransactionParams{
		Authority:            authority,
		StateAddress:         sourceStateKey,
		OutputStateAddress:   destinationStateKey,
		Amount:               lamports,
		SourceCommitment:     commitmentHash,
		Nullifier:            nullifierHash,
		OutputCommitment:     outputCommitment,
		OutputSpendAuthority: destinationSpendAuthorityKey,
		OutputEncryptedNote:  privacyNoteBytes("transfer", lamports, outputCommitment, nullifierHash),
		OutputAuditRecords:   auditRecords,
		ChangeAmount:         changeOutput.amount,
		ChangeCommitment:     changeOutput.commitment,
		ChangeSpendAuthority: sourceNote.SpendAuthority,
		ChangeEncryptedNote:  changeOutput.encryptedNote,
		ChangeAuditRecords:   changeOutput.auditRecords,
	}, node.ledger.Head().BlockHash)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	signature, err := node.submitTransaction(ctx, transaction, "privacy_transfer_to_receiver",
		slog.String("authority", authority.PublicKey.String()),
		slog.String("source_privacy_state", sourceStateKey.String()),
		slog.String("destination_privacy_state", destinationStateKey.String()),
		slog.String("destination_spend_authority", destinationSpendAuthorityKey.String()),
		slog.Uint64("lamports", lamports),
	)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	return privacySpendResult(signature, destinationStateKey, commitmentHash, nullifierHash, outputCommitment, changeOutput), nil
}

func (node *posNode) PrivacyAuthorizeAudit(ctx context.Context, authoritySeed string, stateAddress string, commitment string, auditor string, auditSecret string, scope uint8, expiresAtSlot uint64) (rpc.PrivacyTransactionResult, error) {
	authority, stateKey, commitmentHash, _, err := parsePrivacySpendInputs(authoritySeed, stateAddress, commitment, randomPrivacyHashString())
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	state, err := node.loadPrivacyState(stateKey)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	amount, err := privacyNoteAmount(state, commitmentHash)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	auditorKey, _, err := decodeProtocolPublicKey(auditor, "auditor")
	if err != nil {
		return rpc.PrivacyTransactionResult{}, fmt.Errorf("posnode: decode auditor: %w", err)
	}
	auditKey := utils.SHA256([]byte(strings.TrimSpace(auditSecret)))
	auditRecord, err := structure.NewEncryptedPrivacyAuditRecord(auditorKey, structure.PrivacyAuditScope(scope), expiresAtSlot, auditKey, structure.PrivacyAuditPayload{
		Version:         structure.PrivacyAuditPayloadVersion,
		TransactionType: structure.PrivacyInstructionDeposit,
		Commitment:      commitmentHash,
		Amount:          amount,
		Slot:            node.currentAuditSlot(),
	})
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	transaction, err := blockchain.NewPrivacyAuthorizeAuditTransaction(blockchain.PrivacyAuthorizeAuditTransactionParams{
		Authority:       authority,
		StateAddress:    stateKey,
		Commitment:      commitmentHash,
		Auditor:         auditorKey,
		Scope:           structure.PrivacyAuditScope(scope),
		ExpiresAtSlot:   expiresAtSlot,
		AuditCiphertext: auditRecord.AuditCiphertext,
	}, node.ledger.Head().BlockHash)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	signature, err := node.submitTransaction(ctx, transaction, "privacy_authorize_audit",
		slog.String("authority", authority.PublicKey.String()),
		slog.String("privacy_state", stateKey.String()),
		slog.String("auditor", auditorKey.String()),
	)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	return rpc.PrivacyTransactionResult{Signature: signature, PrivacyState: stateKey.String(), Commitment: commitmentHash.String()}, nil
}

func (node *posNode) GetLocalValidatorIdentity(ctx context.Context) (rpc.LocalValidatorIdentityResult, error) {
	_ = ctx
	head := node.ledger.Head()
	recommendedStakeLamports := node.config.StakeLamports
	if recommendedStakeLamports == 0 {
		recommendedStakeLamports = stake.MinimumStakeLamports
	}
	result := rpc.LocalValidatorIdentityResult{
		NodeName:                 node.config.NodeName,
		StakerAddress:            node.stakerAddress.String(),
		ValidatorAddress:         node.validatorKeyPair.PublicKey.String(),
		ConsensusPublicKey:       node.consensusKeyPair.PublicKey.String(),
		BLSPublicKey:             utils.Base58Encode(node.blsKeyPair.PublicKey),
		P2PPeerID:                node.peerKeyPair.peerID,
		RecommendedStakeLamports: recommendedStakeLamports,
		Status:                   "not_registered",
		CurrentEpoch:             head.EpochID,
	}
	account, found, err := node.ledger.Account(node.validatorKeyPair.PublicKey)
	if err != nil {
		return rpc.LocalValidatorIdentityResult{}, fmt.Errorf("posnode: read local validator account: %w", err)
	}
	if !found || account.Owner != structure.DefaultBuiltinProgramIDs.Stake || len(account.Data) == 0 {
		return result, nil
	}
	state, err := stake.UnmarshalValidatorStateBinary(account.Data)
	if err != nil {
		return rpc.LocalValidatorIdentityResult{}, fmt.Errorf("posnode: decode local validator stake state: %w", err)
	}
	effectiveStake, err := stake.EffectiveStakeAtEpoch(state, head.EpochID)
	if err != nil {
		return rpc.LocalValidatorIdentityResult{}, fmt.Errorf("posnode: calculate local validator effective stake: %w", err)
	}
	result.Registered = true
	result.StakerAddress = state.StakerAccount.String()
	result.Status = stakeStatusText(state.Status)
	result.ActiveStakeLamports = state.ActiveStake
	result.PendingStakeLamports = state.PendingStake
	result.UnlockingStakeLamports = state.UnlockingStake
	result.EffectiveStakeLamports = effectiveStake
	result.ActivationEpoch = state.ActivationEpoch
	result.DeactivationEpoch = state.DeactivationEpoch
	result.CommissionBps = state.CommissionBps
	result.VoteCredits = state.VoteCredits
	result.RewardLamports = state.RewardLamports
	result.SelfRewardLamports = state.SelfRewardLamports
	result.CommissionRewardLamports = state.CommissionRewardLamports
	result.LastRewardedSlot = state.LastRewardedSlot
	result.LastRewardEpoch = state.LastRewardEpoch
	result.LastSlashedSlot = state.LastSlashedSlot
	return result, nil
}

func (node *posNode) RegisterValidator(ctx context.Context, stakerSeed string, validatorSeed string, consensusSeed string, peerID string, stakeLamports uint64) (string, error) {
	staker, err := keyPairFromSeed(stakerSeed)
	if err != nil {
		return "", err
	}
	validatorAccount, err := keyPairFromSeed(validatorSeed)
	if err != nil {
		return "", err
	}
	consensusKey, err := keyPairFromSeed(consensusSeed)
	if err != nil {
		return "", err
	}
	blsKeyPair, err := consensus.BLSKeyPairFromSeed(utils.SHA256([]byte(strings.TrimSpace(consensusSeed))))
	if err != nil {
		return "", err
	}
	transaction, err := blockchain.NewRegisterValidatorTransactionWithBLS(staker, validatorAccount.PublicKey, consensusKey.PublicKey, blsKeyPair.PublicKey, strings.TrimSpace(peerID), stakeLamports, node.ledger.Head().BlockHash)
	if err != nil {
		return "", err
	}
	return node.submitTransaction(ctx, transaction, "register_validator",
		slog.String("staker", staker.PublicKey.String()),
		slog.String("validator_account", validatorAccount.PublicKey.String()),
		slog.String("consensus_public_key", consensusKey.PublicKey.String()),
		slog.String("peer_id", strings.TrimSpace(peerID)),
		slog.Uint64("stake_lamports", stakeLamports),
	)
}

func (node *posNode) RegisterValidatorIdentity(ctx context.Context, stakerSeed string, validatorAddress string, consensusPublicKey string, blsPublicKey string, peerID string, stakeLamports uint64) (string, error) {
	staker, err := keyPairFromSeed(stakerSeed)
	if err != nil {
		return "", err
	}
	validatorKey, err := decodeTransparentPublicKey(validatorAddress, "validator address")
	if err != nil {
		return "", fmt.Errorf("posnode: decode validator address: %w", err)
	}
	consensusKey, err := decodeTransparentPublicKey(consensusPublicKey, "consensus public key")
	if err != nil {
		return "", fmt.Errorf("posnode: decode consensus public key: %w", err)
	}
	blsKeyBytes, err := utils.Base58Decode(strings.TrimSpace(blsPublicKey))
	if err != nil {
		return "", fmt.Errorf("posnode: decode bls public key: %w", err)
	}
	if err := consensus.ValidateBLSPublicKey(blsKeyBytes); err != nil {
		return "", err
	}
	transaction, err := blockchain.NewRegisterValidatorTransactionWithBLS(staker, validatorKey, consensusKey, blsKeyBytes, strings.TrimSpace(peerID), stakeLamports, node.ledger.Head().BlockHash)
	if err != nil {
		return "", err
	}
	return node.submitTransaction(ctx, transaction, "register_validator_identity",
		slog.String("staker", staker.PublicKey.String()),
		slog.String("validator_account", validatorKey.String()),
		slog.String("consensus_public_key", consensusKey.String()),
		slog.String("peer_id", strings.TrimSpace(peerID)),
		slog.Int("bls_public_key_bytes", len(blsKeyBytes)),
		slog.Uint64("stake_lamports", stakeLamports),
	)
}

func (node *posNode) Stake(ctx context.Context, stakerSeed string, validatorAddress string, lamports uint64) (string, error) {
	staker, err := keyPairFromSeed(stakerSeed)
	if err != nil {
		return "", err
	}
	validatorKey, _, err := decodeProtocolPublicKey(validatorAddress, "validator address")
	if err != nil {
		return "", fmt.Errorf("posnode: decode validator address: %w", err)
	}
	state, err := node.loadValidatorStakeState(validatorKey)
	if err != nil {
		return "", err
	}
	if state.StakerAccount != staker.PublicKey {
		return "", fmt.Errorf(
			"posnode: only the validator staker can add stake: current_wallet=%s required_staker=%s validator=%s; 当前实现是验证者自质押追加，普通用户委托质押尚未实现",
			staker.PublicKey.String(),
			state.StakerAccount.String(),
			validatorKey.String(),
		)
	}
	transaction, err := blockchain.NewStakeTransaction(staker, validatorKey, lamports, node.ledger.Head().BlockHash)
	if err != nil {
		return "", err
	}
	return node.submitTransaction(ctx, transaction, "stake",
		slog.String("staker", staker.PublicKey.String()),
		slog.String("validator_account", validatorKey.String()),
		slog.Uint64("lamports", lamports),
	)
}

func (node *posNode) Unstake(ctx context.Context, stakerSeed string, validatorAddress string, lamports uint64, unlockEpoch uint64) (string, error) {
	staker, err := keyPairFromSeed(stakerSeed)
	if err != nil {
		return "", err
	}
	validatorKey, _, err := decodeProtocolPublicKey(validatorAddress, "validator address")
	if err != nil {
		return "", fmt.Errorf("posnode: decode validator address: %w", err)
	}
	state, err := node.loadValidatorStakeState(validatorKey)
	if err != nil {
		return "", err
	}
	if state.StakerAccount != staker.PublicKey {
		return "", fmt.Errorf(
			"posnode: only the validator staker can unstake: current_wallet=%s required_staker=%s validator=%s",
			staker.PublicKey.String(),
			state.StakerAccount.String(),
			validatorKey.String(),
		)
	}
	transaction, err := blockchain.NewUnstakeTransaction(staker, validatorKey, lamports, unlockEpoch, node.ledger.Head().BlockHash)
	if err != nil {
		return "", err
	}
	return node.submitTransaction(ctx, transaction, "unstake",
		slog.String("staker", staker.PublicKey.String()),
		slog.String("validator_account", validatorKey.String()),
		slog.Uint64("lamports", lamports),
		slog.Uint64("unlock_epoch", unlockEpoch),
	)
}

func (node *posNode) SlashValidator(ctx context.Context, stakerSeed string, validatorAddress string, lamports uint64) (string, error) {
	staker, err := keyPairFromSeed(stakerSeed)
	if err != nil {
		return "", err
	}
	validatorKey, _, err := decodeProtocolPublicKey(validatorAddress, "validator address")
	if err != nil {
		return "", fmt.Errorf("posnode: decode validator address: %w", err)
	}
	transaction, err := blockchain.NewSlashValidatorTransaction(staker, validatorKey, lamports, node.ledger.Head().BlockHash)
	if err != nil {
		return "", err
	}
	return node.submitTransaction(ctx, transaction, "slash_validator",
		slog.String("staker", staker.PublicKey.String()),
		slog.String("validator_account", validatorKey.String()),
		slog.Uint64("lamports", lamports),
	)
}

func (node *posNode) JailValidator(ctx context.Context, stakerSeed string, validatorAddress string, jailUntilEpoch uint64) (string, error) {
	staker, err := keyPairFromSeed(stakerSeed)
	if err != nil {
		return "", err
	}
	validatorKey, _, err := decodeProtocolPublicKey(validatorAddress, "validator address")
	if err != nil {
		return "", fmt.Errorf("posnode: decode validator address: %w", err)
	}
	transaction, err := blockchain.NewJailValidatorTransaction(staker, validatorKey, jailUntilEpoch, node.ledger.Head().BlockHash)
	if err != nil {
		return "", err
	}
	return node.submitTransaction(ctx, transaction, "jail_validator",
		slog.String("staker", staker.PublicKey.String()),
		slog.String("validator_account", validatorKey.String()),
		slog.Uint64("jail_until_epoch", jailUntilEpoch),
	)
}

func (node *posNode) GetValidatorSet(ctx context.Context) (rpc.ValidatorSetResult, error) {
	_ = ctx
	head := node.ledger.Head()
	validatorSet, err := node.ledger.ValidatorSetFromStateAtEpoch(head.EpochID)
	if err != nil {
		return rpc.ValidatorSetResult{}, err
	}
	validators := validatorSet.Validators()
	result := rpc.ValidatorSetResult{Validators: make([]rpc.ValidatorInfo, len(validators))}
	for index, validator := range validators {
		stakeState, err := node.loadValidatorStakeState(validator.AccountAddress)
		if err != nil {
			return rpc.ValidatorSetResult{}, err
		}
		selfStakeLamports, err := stake.SelfActiveStake(stakeState)
		if err != nil {
			return rpc.ValidatorSetResult{}, err
		}
		selfPendingStakeLamports, err := stake.SelfPendingStake(stakeState)
		if err != nil {
			return rpc.ValidatorSetResult{}, err
		}
		selfUnlockingStakeLamports, err := stake.SelfUnlockingStake(stakeState)
		if err != nil {
			return rpc.ValidatorSetResult{}, err
		}
		delegatedLamports, err := stake.TotalDelegatedStake(stakeState)
		if err != nil {
			return rpc.ValidatorSetResult{}, err
		}
		result.Validators[index] = rpc.ValidatorInfo{
			ValidatorID:                string(validator.ValidatorID),
			AccountAddress:             validator.AccountAddress.String(),
			StakerAddress:              stakeState.StakerAccount.String(),
			ConsensusPublicKey:         validator.ConsensusPublicKey.String(),
			P2PPeerID:                  validator.P2PPeerID,
			StakeLamports:              validator.StakeLamports,
			SelfStakeLamports:          selfStakeLamports,
			SelfPendingStakeLamports:   selfPendingStakeLamports,
			SelfUnlockingStakeLamports: selfUnlockingStakeLamports,
			SelfRewardLamports:         stakeState.SelfRewardLamports,
			CommissionRewardLamports:   stakeState.CommissionRewardLamports,
			DelegatedLamports:          delegatedLamports,
			DelegatorCount:             len(stakeState.Delegations),
			Status:                     validatorStatusText(validator.Status),
			CommissionBps:              validator.CommissionBps,
			VoteCredits:                stakeState.VoteCredits,
			RewardLamports:             stakeState.RewardLamports,
			LastRewardedSlot:           stakeState.LastRewardedSlot,
			LastRewardEpoch:            stakeState.LastRewardEpoch,
			JailUntilEpoch:             stakeState.JailUntilEpoch,
			ActivationEpoch:            stakeState.ActivationEpoch,
			DeactivationEpoch:          stakeState.DeactivationEpoch,
			LastEffectiveStakeLamports: stakeState.LastEffectiveStake,
			LastSlashedSlot:            stakeState.LastSlashedSlot,
			Delegations:                delegationInfos(stakeState.Delegations),
		}
	}
	return result, nil
}

func delegationInfos(delegations []stake.DelegationState) []rpc.DelegationInfo {
	if len(delegations) == 0 {
		return nil
	}
	result := make([]rpc.DelegationInfo, 0, len(delegations))
	for _, delegation := range delegations {
		result = append(result, rpc.DelegationInfo{
			DelegatorAddress:       delegation.DelegatorAccount.String(),
			ActiveStakeLamports:    delegation.ActiveStake,
			PendingStakeLamports:   delegation.PendingStake,
			UnlockingStakeLamports: delegation.UnlockingStake,
			RewardLamports:         delegation.RewardLamports,
			ActivationEpoch:        delegation.ActivationEpoch,
			DeactivationEpoch:      delegation.DeactivationEpoch,
			UnlockEpoch:            delegation.UnlockEpoch,
		})
	}
	return result
}

func (node *posNode) GetNodeStatus(ctx context.Context) (any, error) {
	_ = ctx
	return node.statusSnapshot(), nil
}

// GetPeerNetwork 返回 peer 拓扑视图 + 让钱包区分链上验证者、已解析地址和当前连接。
func (node *posNode) GetPeerNetwork(ctx context.Context) (rpc.PeerNetworkResult, error) {
	_ = ctx
	result := rpc.PeerNetworkResult{LocalPeerID: node.peerKeyPair.peerID}
	if node.host == nil {
		return result, nil
	}

	peerSnapshots := node.host.PeerSnapshots()
	connectionStates := make(map[string]p2p.ConnectionState, len(peerSnapshots))
	for _, peerSnapshot := range peerSnapshots {
		connectionState, ok := node.host.ConnectionState(peerSnapshot.ID)
		if !ok {
			continue
		}
		connectionStates[peerSnapshot.ID] = connectionState
	}
	return buildPeerNetworkResult(node.peerKeyPair.peerID, peerSnapshots, connectionStates), nil
}

func (node *posNode) GetHealth(ctx context.Context) (rpc.HealthResult, error) {
	_ = ctx
	if node.ledger == nil {
		ready := false
		if node.bootstrapCoordinator != nil {
			status, err := node.bootstrapCoordinator.GetBootstrapStatus(ctx)
			if err != nil {
				return rpc.HealthResult{}, err
			}
			ready = status.Ready && len(node.connectedBootstrapValidatorPeerIDs()) > 0
		}
		return rpc.HealthResult{OK: ready}, nil
	}
	head := node.ledger.Head()
	node.mutex.Lock()
	mempoolSize := len(node.mempool)
	node.mutex.Unlock()
	livenessGate := node.refreshLivenessGate(time.Now())
	return rpc.HealthResult{
		OK:                                livenessGateHealthOK(livenessGate),
		HeadHeight:                        head.Height,
		HeadSlot:                          head.Slot,
		FinalizedHeight:                   head.FinalizedHeight,
		MempoolSize:                       mempoolSize,
		LivenessState:                     livenessGate.State,
		LivenessMode:                      livenessGate.Mode,
		LivenessReason:                    livenessGate.Reason,
		LivenessQuorumReady:               livenessGate.QuorumReady,
		LivenessProductionEnabled:         livenessGate.ProductionEnabled,
		ReachableStakeLamports:            livenessGate.ReachableStakeLamports,
		RequiredStakeLamports:             livenessGate.RequiredStakeLamports,
		TotalActiveStakeLamports:          livenessGate.TotalActiveStakeLamports,
		RecentReachabilityWindowMillis:    livenessGate.RecentReachabilityWindowMillis,
		LastReachableStakeUpdateUnixMilli: livenessGate.LastReachableStakeUpdateUnixMilli,
	}, nil
}

func (node *posNode) lookupMempoolTransaction(signature string) (rpc.TransactionDetailResult, bool, error) {
	node.mutex.Lock()
	defer node.mutex.Unlock()

	for _, transaction := range node.mempool {
		transactionID, err := transaction.TxIDString()
		if err != nil {
			return rpc.TransactionDetailResult{}, false, fmt.Errorf("posnode: decode mempool transaction id: %w", err)
		}
		if transactionID != signature {
			continue
		}
		return buildTransactionDetailResult(signature, transaction.Clone(), "mempool", "pending", 0, 0, structure.Hash{}, "", false), true, nil
	}
	return rpc.TransactionDetailResult{}, false, nil
}

func (node *posNode) committedTransactionResult(
	signature string,
	transaction structure.Transaction,
	proposal consensus.BlockProposal,
	blockHash structure.Hash,
) rpc.TransactionDetailResult {
	head := node.ledger.Head()
	finalized := proposal.Header.Height <= head.FinalizedHeight
	status := "confirmed"
	if finalized {
		status = "finalized"
	}
	return buildTransactionDetailResult(
		signature,
		transaction,
		"block",
		status,
		proposal.Header.Height,
		proposal.Header.Slot,
		blockHash,
		proposalLeaderAddress(proposal),
		finalized,
	)
}

func (node *posNode) submitTransaction(ctx context.Context, transaction structure.Transaction, action string, attrs ...slog.Attr) (transactionID string, err error) {
	startedAt := time.Now()
	head := node.ledger.Head()
	defer func() {
		node.logRPCTransactionSubmit(ctx, action, transactionID, head, startedAt, err, attrs...)
	}()
	transactionID, err = transaction.TxIDString()
	if err != nil {
		return "", err
	}
	if existingTransaction, exists := node.mempoolTransactionByID(transactionID); exists {
		node.scheduleRPCTransactionBroadcast(ctx, existingTransaction, transactionID)
		return transactionID, nil
	}
	committed, err := node.transactionAlreadyCommitted(transactionID)
	if err != nil {
		return "", err
	}
	if committed {
		node.clearTransactionTracking(transactionID)
		return transactionID, nil
	}
	if err := node.ensureHeadBlockhashAvailable(head); err != nil {
		return "", err
	}
	if err := node.preflightTransaction(ctx, transaction, head); err != nil {
		node.metrics.transactionsDrop.Add(1)
		return "", err
	}
	if err := node.addTransaction(transaction); err != nil {
		return "", err
	}
	node.scheduleRPCTransactionBroadcast(ctx, transaction, transactionID)
	return transactionID, nil
}

// scheduleRPCTransactionBroadcast 后台扩散已接收交易 + RPC sendTransaction 必须在入池后快速返回签名。
func (node *posNode) scheduleRPCTransactionBroadcast(ctx context.Context, transaction structure.Transaction, transactionID string) {
	if node.host == nil || !node.config.transactionForwardEnabled() {
		return
	}
	node.startWorker(func() {
		baseContext := context.Background()
		if ctx != nil {
			baseContext = context.WithoutCancel(ctx)
		}
		broadcastContext, cancel := context.WithTimeout(baseContext, rpcTransactionBroadcastTimeout)
		defer cancel()
		node.broadcastTransaction(broadcastContext, transaction)
		node.logger.Debug("posnode rpc transaction broadcast scheduled",
			slog.String("tx_id", transactionID),
			slog.Duration("timeout", rpcTransactionBroadcastTimeout),
		)
	})
}

// preflightTransaction 预执行 RPC 交易 + 避免把必然失败的交易返回成已提交签名。
func (node *posNode) preflightTransaction(ctx context.Context, transaction structure.Transaction, head blockchain.Head) error {
	if node.executor.Programs.IsEmpty() {
		return nil
	}
	state := node.ledger.State()
	node.mutex.Lock()
	blockhashQueue := node.blockhashQueue.Clone()
	pendingTransactions := make([]structure.Transaction, len(node.mempool))
	copy(pendingTransactions, node.mempool)
	node.mutex.Unlock()

	processedTransactionIDs := make(map[string]struct{}, len(pendingTransactions)+1)
	for _, pendingTransaction := range pendingTransactions {
		result, err := node.executePreflightTransaction(ctx, pendingTransaction, head, state, blockhashQueue, processedTransactionIDs)
		if err != nil || result.Execution.Status != structure.TransactionStatusConfirmed {
			continue
		}
		pendingTransactionID, err := pendingTransaction.TxIDString()
		if err == nil {
			processedTransactionIDs[pendingTransactionID] = struct{}{}
		}
		state = applyPreflightWrites(state, result.Execution.WrittenAccounts)
	}

	result, err := node.executePreflightTransaction(ctx, transaction, head, state, blockhashQueue, processedTransactionIDs)
	if err != nil {
		return err
	}
	if result.Execution.Status == structure.TransactionStatusConfirmed {
		return nil
	}
	return fmt.Errorf("posnode: preflight transaction failed: %s", transactionExecutionErrorMessage(result.Execution))
}

func (node *posNode) executePreflightTransaction(
	ctx context.Context,
	transaction structure.Transaction,
	head blockchain.Head,
	state consensus.ChainState,
	blockhashQueue structure.BlockhashQueue,
	processedTransactionIDs map[string]struct{},
) (runtimepkg.TransactionResult, error) {
	result, err := node.executor.ExecuteTransaction(ctx, runtimepkg.TransactionRequest{
		ChainID: node.config.ChainID,
		Slot:    head.Slot,
		Epoch:   head.EpochID,
		Mode:    runtimepkg.ExecutionModeFixedInstruction,
		Simulation: runtimepkg.TransactionSimulationInput{
			Transaction:    transaction,
			Accounts:       state.Accounts,
			BlockhashQueue: blockhashQueue,
			CurrentSlot:    head.Slot,
			CurrentEpoch:   head.EpochID,
			ProcessedTxIDs: processedTransactionIDs,
			Logger:         node.logger,
		},
	})
	if err != nil {
		return runtimepkg.TransactionResult{}, fmt.Errorf("posnode: preflight transaction: %w", err)
	}
	return result, nil
}

func applyPreflightWrites(state consensus.ChainState, writes []structure.AddressedAccount) consensus.ChainState {
	accountIndexByAddress := make(map[structure.PublicKey]int, len(state.Accounts)+len(writes))
	nextAccounts := make([]structure.AddressedAccount, len(state.Accounts))
	for index, account := range state.Accounts {
		nextAccounts[index] = structure.AddressedAccount{Address: account.Address, Account: account.Account.Clone()}
		accountIndexByAddress[account.Address] = index
	}
	for _, write := range writes {
		if index, exists := accountIndexByAddress[write.Address]; exists {
			nextAccounts[index] = structure.AddressedAccount{Address: write.Address, Account: write.Account.Clone()}
			continue
		}
		accountIndexByAddress[write.Address] = len(nextAccounts)
		nextAccounts = append(nextAccounts, structure.AddressedAccount{Address: write.Address, Account: write.Account.Clone()})
	}
	return consensus.ChainState{Accounts: nextAccounts}
}

// ensureHeadBlockhashAvailable 补齐链头 blockhash 窗口 + RPC 使用链头哈希构造交易时必须可立即校验。
func (node *posNode) ensureHeadBlockhashAvailable(head blockchain.Head) error {
	if head.BlockHash == (structure.Hash{}) {
		return nil
	}
	node.mutex.Lock()
	defer node.mutex.Unlock()
	if _, exists := node.blockhashQueue.Find(head.BlockHash); exists {
		return nil
	}
	if err := node.blockhashQueue.Add(structure.RecentBlockhashEntry{
		Blockhash:     head.BlockHash,
		Slot:          head.Slot,
		FeeCalculator: structure.DefaultFeeCalculator(),
		TimestampUnix: time.Now().Unix(),
	}); err != nil {
		return fmt.Errorf("posnode: add head blockhash to queue: %w", err)
	}
	return nil
}

func transactionExecutionErrorMessage(result structure.TransactionExecutionResult) string {
	if result.Error == nil {
		return "transaction rejected"
	}
	return result.Error.Error()
}

func (node *posNode) logRPCTransactionSubmit(
	ctx context.Context,
	action string,
	transactionID string,
	head blockchain.Head,
	startedAt time.Time,
	err error,
	attrs ...slog.Attr,
) {
	logAttrs := []slog.Attr{
		slog.String("action", action),
		slog.String("tx_id", transactionID),
		slog.Uint64("head_height", head.Height),
		slog.Uint64("head_slot", head.Slot),
		slog.String("head_hash", head.BlockHash.String()),
		slog.String("qc_hash", head.QCHash.String()),
		slog.Int64("duration_ms", time.Since(startedAt).Milliseconds()),
	}
	logAttrs = append(logAttrs, attrs...)
	if err != nil {
		logAttrs = append(logAttrs, slog.Any("error", err))
		node.logger.LogAttrs(ctx, slog.LevelError, "posnode rpc transaction submit failed", logAttrs...)
		return
	}
	node.logger.LogAttrs(ctx, slog.LevelInfo, "posnode rpc transaction submitted", logAttrs...)
}

func (node *posNode) loadPrivacyState(stateKey structure.PublicKey) (structure.PrivacyState, error) {
	account, found, err := node.ledger.Account(stateKey)
	if err != nil {
		return structure.PrivacyState{}, err
	}
	if !found {
		return structure.PrivacyState{Version: structure.PrivacyStateVersion}, nil
	}
	if account.Owner != structure.DefaultBuiltinProgramIDs.Privacy {
		return structure.PrivacyState{}, fmt.Errorf("posnode: privacy state owner mismatch")
	}
	return structure.UnmarshalPrivacyStateBinary(account.Data)
}

func (node *posNode) requirePrivacyStateAccount(stateKey structure.PublicKey) error {
	account, found, err := node.ledger.Account(stateKey)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("posnode: privacy state account not found")
	}
	if account.Owner != structure.DefaultBuiltinProgramIDs.Privacy {
		return fmt.Errorf("posnode: account is not privacy state")
	}
	return nil
}

func (node *posNode) loadValidatorStakeState(validatorKey structure.PublicKey) (stake.ValidatorState, error) {
	account, found, err := node.ledger.Account(validatorKey)
	if err != nil {
		return stake.ValidatorState{}, err
	}
	if !found {
		return stake.ValidatorState{}, fmt.Errorf("posnode: validator account is not registered")
	}
	if account.Owner != structure.DefaultBuiltinProgramIDs.Stake || len(account.Data) == 0 {
		return stake.ValidatorState{}, fmt.Errorf("posnode: account is not a validator stake account")
	}
	state, err := stake.UnmarshalValidatorStateBinary(account.Data)
	if err != nil {
		return stake.ValidatorState{}, fmt.Errorf("posnode: decode validator stake state: %w", err)
	}
	return state, nil
}

func (node *posNode) decodePrivacyReceiver(stateAddress string, spendAuthority string) (structure.PublicKey, structure.PublicKey, error) {
	stateKey, _, err := decodeProtocolPublicKey(stateAddress, "privacy receiver state")
	if err != nil {
		return structure.PublicKey{}, structure.PublicKey{}, fmt.Errorf("posnode: decode receiver state: %w", err)
	}
	if err := node.requirePrivacyStateAccount(stateKey); err != nil {
		return structure.PublicKey{}, structure.PublicKey{}, err
	}
	spendAuthorityKey, addressType, err := decodeProtocolPublicKey(spendAuthority, "privacy receiver spend authority")
	if err != nil {
		return structure.PublicKey{}, structure.PublicKey{}, fmt.Errorf("posnode: decode receiver spend authority: %w", err)
	}
	if addressType == protocolAddressPrivacy {
		return structure.PublicKey{}, structure.PublicKey{}, fmt.Errorf("posnode: receiver spend authority must be a transparent public key")
	}
	return stateKey, spendAuthorityKey, nil
}

func accountTypeName(owner structure.PublicKey) string {
	switch owner {
	case structure.DefaultBuiltinProgramIDs.Privacy:
		return "privacy_state"
	case structure.DefaultBuiltinProgramIDs.System:
		return "transparent"
	case structure.DefaultBuiltinProgramIDs.Stake:
		return "stake"
	default:
		return "program"
	}
}

func (node *posNode) buildPrivacyAuditRecords(auditor string, auditSecret string, expiresAtSlot uint64, scope structure.PrivacyAuditScope, payload structure.PrivacyAuditPayload) ([]structure.PrivacyAuditRecord, error) {
	auditor = strings.TrimSpace(auditor)
	auditSecret = strings.TrimSpace(auditSecret)
	if auditor == "" && auditSecret == "" {
		return nil, nil
	}
	auditorKey, _, err := decodeProtocolPublicKey(auditor, "auditor")
	if err != nil {
		return nil, fmt.Errorf("posnode: decode auditor: %w", err)
	}
	auditKey := utils.SHA256([]byte(auditSecret))
	record, err := structure.NewEncryptedPrivacyAuditRecord(auditorKey, scope, expiresAtSlot, auditKey, payload)
	if err != nil {
		return nil, err
	}
	return []structure.PrivacyAuditRecord{record}, nil
}

func (node *posNode) buildPrivacyChangeArtifacts(
	kind string,
	transactionType structure.PrivacyInstructionType,
	inputAmount uint64,
	outputAmount uint64,
	sourceCommitment structure.Hash,
	nullifier structure.Hash,
	auditor string,
	auditSecret string,
	expiresAtSlot uint64,
	auditSlot uint64,
) (privacyChangeArtifacts, error) {
	if outputAmount > inputAmount {
		return privacyChangeArtifacts{}, fmt.Errorf("posnode: privacy amount exceeds source note amount: amount=%d source=%d", outputAmount, inputAmount)
	}
	changeAmount := inputAmount - outputAmount
	if changeAmount == 0 {
		return privacyChangeArtifacts{}, nil
	}
	changeCommitment, err := randomPrivacyHash()
	if err != nil {
		return privacyChangeArtifacts{}, err
	}
	auditRecords, err := node.buildPrivacyAuditRecords(auditor, auditSecret, expiresAtSlot, structure.PrivacyAuditScopeRegulatory, structure.PrivacyAuditPayload{
		Version:          structure.PrivacyAuditPayloadVersion,
		TransactionType:  transactionType,
		Commitment:       sourceCommitment,
		Nullifier:        nullifier,
		OutputCommitment: changeCommitment,
		Amount:           changeAmount,
		Slot:             auditSlot,
	})
	if err != nil {
		return privacyChangeArtifacts{}, err
	}
	return privacyChangeArtifacts{
		amount:        changeAmount,
		commitment:    changeCommitment,
		encryptedNote: privacyNoteBytes(kind, changeAmount, changeCommitment, nullifier),
		auditRecords:  auditRecords,
	}, nil
}

func (node *posNode) currentAuditSlot() uint64 {
	head := node.ledger.Head()
	if head.Slot != 0 {
		return head.Slot
	}
	if head.Height != 0 {
		return head.Height
	}
	return 1
}

func privacyStateResult(address structure.PublicKey, state structure.PrivacyState) rpc.PrivacyStateResult {
	notes := make([]rpc.PrivacyNoteResult, len(state.Notes))
	for index, note := range state.Notes {
		notes[index] = privacyNoteResult(note)
	}
	nullifiers := make([]string, len(state.SpentNullifiers))
	for index, nullifier := range state.SpentNullifiers {
		nullifiers[index] = nullifier.String()
	}
	return rpc.PrivacyStateResult{
		Address:         address.String(),
		Version:         state.Version,
		Notes:           notes,
		SpentNullifiers: nullifiers,
	}
}

func accountTransactionHistoryResult(address structure.PublicKey, page blockchain.AddressHistoryPage, finalizedHeight uint64) rpc.AccountTransactionHistoryResult {
	records := make([]rpc.AccountTransactionRecordResult, len(page.Records))
	for index, record := range page.Records {
		status := "confirmed"
		finalized := record.BlockHeight > 0 && record.BlockHeight <= finalizedHeight
		if finalized {
			status = "finalized"
		}
		records[index] = rpc.AccountTransactionRecordResult{
			Signature:           record.TransactionID,
			Direction:           string(record.Direction),
			Kind:                string(record.Kind),
			Counterparty:        record.Counterparty,
			AmountLamports:      fmt.Sprintf("%d", record.AmountLamports),
			BlockHeight:         record.BlockHeight,
			Slot:                record.Slot,
			Blockhash:           record.BlockHash,
			SubmitTimeUnixMilli: record.SubmitTimeUnixMilli,
			Finalized:           finalized,
			Status:              status,
			Location:            "block",
		}
	}
	return rpc.AccountTransactionHistoryResult{
		Address:    address.String(),
		Scope:      page.Scope,
		Records:    records,
		NextCursor: page.NextCursor,
		HasMore:    page.HasMore,
	}
}

func contractProgramListResult(records []blockchain.ContractProgramRecord) rpc.ContractProgramListResult {
	programs := make([]rpc.ContractProgramResult, len(records))
	for index, record := range records {
		programs[index] = rpc.ContractProgramResult{
			Address:    record.Address.String(),
			Owner:      record.Owner.String(),
			Executable: true,
			Lamports:   fmt.Sprintf("%d", record.Lamports),
			DataLength: record.DataLength,
			CodeHash:   record.CodeHash,
			RentEpoch:  record.RentEpoch,
		}
	}
	return rpc.ContractProgramListResult{
		Scope:    "executable_bpfloader_programs",
		Programs: programs,
	}
}

func privacyBalanceResult(stateAddress structure.PublicKey, spendAuthority structure.PublicKey, summary blockchain.PrivacyBalanceSummary) rpc.PrivacyBalanceResult {
	return rpc.PrivacyBalanceResult{
		StateAddress:       stateAddress.String(),
		SpendAuthority:     spendAuthority.String(),
		AvailableLamports:  fmt.Sprintf("%d", summary.AvailableLamports),
		EscrowLamports:     fmt.Sprintf("%d", summary.EscrowLamports),
		SpendableNoteCount: summary.SpendableNoteCount,
		SpentNoteCount:     summary.SpentNoteCount,
		OwnedNoteCount:     summary.OwnedNoteCount,
		StateNoteCount:     summary.StateNoteCount,
	}
}

func privacyNoteResult(note structure.PrivacyNoteRecord) rpc.PrivacyNoteResult {
	records := make([]rpc.PrivacyAuditRecordResult, len(note.AuditRecords))
	for index, record := range note.AuditRecords {
		records[index] = rpc.PrivacyAuditRecordResult{
			Auditor:       record.Auditor.String(),
			Scope:         uint8(record.Scope),
			ExpiresAtSlot: record.ExpiresAtSlot,
			Ciphertext:    utils.Base64Encode(record.AuditCiphertext),
		}
	}
	return rpc.PrivacyNoteResult{
		Commitment:     note.Commitment.String(),
		SpendAuthority: note.SpendAuthority.String(),
		Amount:         note.Amount,
		Spent:          note.Spent,
		SpentSlot:      note.SpentSlot,
		SpendNullifier: privacyNullifierString(note),
		AuditRecords:   records,
	}
}

func privacyNullifierString(note structure.PrivacyNoteRecord) string {
	if note.SpendNullifier.IsZero() {
		return ""
	}
	return note.SpendNullifier.String()
}

func buildPeerNetworkResult(
	localPeerID string,
	peerSnapshots []p2p.PeerSnapshot,
	connectionStates map[string]p2p.ConnectionState,
) rpc.PeerNetworkResult {
	result := rpc.PeerNetworkResult{
		LocalPeerID: localPeerID,
		Peers:       make([]rpc.PeerNetworkPeerResult, 0, len(peerSnapshots)),
	}
	for _, peerSnapshot := range peerSnapshots {
		connectionState, connected := connectionStates[peerSnapshot.ID]
		result.Peers = append(result.Peers, rpc.PeerNetworkPeerResult{
			PeerID:                    peerSnapshot.ID,
			Status:                    string(peerSnapshot.Status),
			Role:                      string(peerSnapshot.Role),
			Roles:                     p2p.PeerRoleNames(peerSnapshot.Role, peerSnapshot.Capabilities),
			Capabilities:              uint64(peerSnapshot.Capabilities),
			CapabilityNames:           p2p.PeerCapabilityNames(peerSnapshot.Capabilities),
			Validator:                 peerSnapshot.Validator,
			Connected:                 connected,
			BestAddress:               bestPeerAddress(peerSnapshot, connectionState, connected),
			AdvertisedAddresses:       stringifyMultiAddresses(peerSnapshot.AdvertisedAddresses),
			VerifiedAddresses:         stringifyMultiAddresses(peerSnapshot.VerifiedAddresses),
			PreferredProtocols:        stringifyProtocols(peerSnapshot.PreferredProtocols),
			LatestSlot:                peerSnapshot.LatestSlot,
			BlockHeight:               peerSnapshot.BlockHeight,
			FailureCount:              peerSnapshot.FailureCount,
			LastError:                 visiblePeerLastError(peerSnapshot, connected),
			LastSeenUnixMilli:         peerSnapshot.LastSeenUnixMilli,
			LastConnectedUnixMilli:    peerSnapshot.LastConnectedUnixMilli,
			LastDisconnectedUnixMilli: peerSnapshot.LastDisconnectedUnixMilli,
			Connection:                buildPeerConnectionInfo(connectionState, connected),
		})
	}
	sort.Slice(result.Peers, func(leftIndex int, rightIndex int) bool {
		leftPeer := result.Peers[leftIndex]
		rightPeer := result.Peers[rightIndex]
		if leftPeer.Connected != rightPeer.Connected {
			return leftPeer.Connected
		}
		if leftPeer.Validator != rightPeer.Validator {
			return leftPeer.Validator
		}
		if leftPeer.LastConnectedUnixMilli != rightPeer.LastConnectedUnixMilli {
			return leftPeer.LastConnectedUnixMilli > rightPeer.LastConnectedUnixMilli
		}
		return leftPeer.PeerID < rightPeer.PeerID
	})
	return result
}

func visiblePeerLastError(peerSnapshot p2p.PeerSnapshot, connected bool) string {
	if connected {
		return ""
	}
	return peerSnapshot.LastError
}

func buildPeerConnectionInfo(connectionState p2p.ConnectionState, connected bool) *rpc.PeerConnectionInfo {
	if !connected {
		return nil
	}
	return &rpc.PeerConnectionInfo{
		Protocol:               string(connectionState.Protocol),
		RemoteAddress:          connectionState.RemoteAddress,
		ObservedRemoteAddress:  connectionState.ObservedRemoteAddress,
		Encrypted:              connectionState.Encrypted,
		ConnectedAtUnixMilli:   connectionState.ConnectedAtUnixMilli,
		LastReadUnixMilli:      connectionState.LastReadUnixMilli,
		LastWriteUnixMilli:     connectionState.LastWriteUnixMilli,
		LastHeartbeatUnixMilli: connectionState.LastHeartbeatUnixMilli,
		FailureCount:           connectionState.FailureCount,
	}
}

func bestPeerAddress(peerSnapshot p2p.PeerSnapshot, connectionState p2p.ConnectionState, connected bool) string {
	if len(peerSnapshot.VerifiedAddresses) > 0 {
		return peerSnapshot.VerifiedAddresses[0].String()
	}
	if len(peerSnapshot.AdvertisedAddresses) > 0 {
		return peerSnapshot.AdvertisedAddresses[0].String()
	}
	if connected {
		return connectionState.RemoteAddress
	}
	return ""
}

func stringifyMultiAddresses(addresses []utils.MultiAddress) []string {
	if len(addresses) == 0 {
		return nil
	}
	values := make([]string, 0, len(addresses))
	for _, address := range addresses {
		values = append(values, address.String())
	}
	return values
}

func stringifyProtocols(protocols []utils.MultiAddressProtocol) []string {
	if len(protocols) == 0 {
		return nil
	}
	values := make([]string, 0, len(protocols))
	for _, protocol := range protocols {
		values = append(values, string(protocol))
	}
	return values
}

func privacyNoteAmount(state structure.PrivacyState, commitment structure.Hash) (uint64, error) {
	note, err := privacySpendNote(state, commitment)
	if err != nil {
		return 0, err
	}
	return note.Amount, nil
}

func privacySpendNote(state structure.PrivacyState, commitment structure.Hash) (structure.PrivacyNoteRecord, error) {
	for _, note := range state.Notes {
		if note.Commitment == commitment && !note.Spent {
			return note, nil
		}
	}
	return structure.PrivacyNoteRecord{}, fmt.Errorf("posnode: unspent privacy note not found")
}

func privacySpendResult(
	signature string,
	privacyState structure.PublicKey,
	commitment structure.Hash,
	nullifier structure.Hash,
	outputCommitment structure.Hash,
	changeOutput privacyChangeArtifacts,
) rpc.PrivacyTransactionResult {
	result := rpc.PrivacyTransactionResult{
		Signature:    signature,
		PrivacyState: privacyState.String(),
		Commitment:   commitment.String(),
		Nullifier:    nullifier.String(),
	}
	if !outputCommitment.IsZero() {
		result.OutputCommitment = outputCommitment.String()
	}
	if changeOutput.amount > 0 {
		result.ChangeCommitment = changeOutput.commitment.String()
		result.ChangeLamports = fmt.Sprintf("%d", changeOutput.amount)
	}
	return result
}

func parsePrivacySpendInputs(authoritySeed string, stateAddress string, commitment string, nullifier string) (structure.SolanaKeyPair, structure.PublicKey, structure.Hash, structure.Hash, error) {
	authority, err := keyPairFromSeed(authoritySeed)
	if err != nil {
		return structure.SolanaKeyPair{}, structure.PublicKey{}, structure.Hash{}, structure.Hash{}, err
	}
	stateKey, _, err := decodeProtocolPublicKey(stateAddress, "privacy state")
	if err != nil {
		return structure.SolanaKeyPair{}, structure.PublicKey{}, structure.Hash{}, structure.Hash{}, fmt.Errorf("posnode: decode privacy state: %w", err)
	}
	commitmentHash, err := structure.HashFromBase58(strings.TrimSpace(commitment))
	if err != nil {
		return structure.SolanaKeyPair{}, structure.PublicKey{}, structure.Hash{}, structure.Hash{}, fmt.Errorf("posnode: decode privacy commitment: %w", err)
	}
	nullifierHash, err := structure.HashFromBase58(strings.TrimSpace(nullifier))
	if err != nil {
		return structure.SolanaKeyPair{}, structure.PublicKey{}, structure.Hash{}, structure.Hash{}, fmt.Errorf("posnode: decode privacy nullifier: %w", err)
	}
	return authority, stateKey, commitmentHash, nullifierHash, nil
}

func decodeProtocolPublicKey(address string, field string) (structure.PublicKey, byte, error) {
	trimmedAddress := strings.TrimSpace(address)
	if trimmedAddress == "" {
		return structure.PublicKey{}, 0, fmt.Errorf("%s is empty", field)
	}
	prefix, encodedBody, hasPrefix := protocolAddressPrefix(trimmedAddress)
	decodedBody, err := utils.Base58Decode(encodedBody)
	if err != nil {
		return structure.PublicKey{}, 0, err
	}
	if len(decodedBody) == structure.PublicKeySize && !hasPrefix {
		key, err := structure.NewPublicKey(decodedBody)
		return key, 0, err
	}
	if len(decodedBody) != protocolAddressSize {
		return structure.PublicKey{}, 0, fmt.Errorf("%s payload length = %d, want %d or %d", field, len(decodedBody), structure.PublicKeySize, protocolAddressSize)
	}
	addressType := decodedBody[0]
	if addressType != protocolAddressTransparent && addressType != protocolAddressPrivacy {
		return structure.PublicKey{}, 0, fmt.Errorf("%s address type byte %d is unsupported", field, addressType)
	}
	if hasPrefix && prefix != addressType {
		return structure.PublicKey{}, 0, fmt.Errorf("%s prefix does not match payload type", field)
	}
	key, err := structure.NewPublicKey(decodedBody[1:])
	return key, addressType, err
}

func decodeTransparentPublicKey(address string, field string) (structure.PublicKey, error) {
	key, addressType, err := decodeProtocolPublicKey(address, field)
	if err != nil {
		return structure.PublicKey{}, err
	}
	if addressType == protocolAddressPrivacy {
		return structure.PublicKey{}, fmt.Errorf("%s must be a transparent public key", field)
	}
	return key, nil
}

func protocolAddressPrefix(address string) (byte, string, bool) {
	if strings.HasPrefix(address, "t") {
		return protocolAddressTransparent, address[1:], true
	}
	if strings.HasPrefix(address, "z") {
		return protocolAddressPrivacy, address[1:], true
	}
	return 0, address, false
}

func randomPrivacyHashString() string {
	hash, err := randomPrivacyHash()
	if err != nil {
		return structure.Hash{}.String()
	}
	return hash.String()
}

func randomPrivacyHash() (structure.Hash, error) {
	value := make([]byte, structure.HashSize)
	if _, err := rand.Read(value); err != nil {
		return structure.Hash{}, fmt.Errorf("posnode: generate privacy hash: %w", err)
	}
	return structure.NewHash(value)
}

func privacyNoteBytes(kind string, amount uint64, commitment structure.Hash, nullifier structure.Hash) []byte {
	note := fmt.Sprintf("kind=%s;amount=%d;commitment=%s;nullifier=%s", kind, amount, commitment.String(), nullifier.String())
	return []byte(note)
}

func keyPairFromSeed(seedText string) (structure.SolanaKeyPair, error) {
	seedText = strings.TrimSpace(seedText)
	if seedText == "" {
		return structure.SolanaKeyPair{}, fmt.Errorf("posnode: seed is empty")
	}
	keyPair, err := structure.KeyPairFromSeed(utils.SHA256([]byte(seedText)))
	if err != nil {
		return structure.SolanaKeyPair{}, fmt.Errorf("posnode: build keypair: %w", err)
	}
	return keyPair, nil
}

func validatorStatusText(status consensus.ValidatorStatus) string {
	switch status {
	case consensus.ValidatorStatusActive:
		return "active"
	case consensus.ValidatorStatusJailed:
		return "jailed"
	case consensus.ValidatorStatusExiting:
		return "exiting"
	default:
		return "inactive"
	}
}

func stakeStatusText(status stake.ValidatorStatus) string {
	switch status {
	case stake.ValidatorStatusActive:
		return "active"
	case stake.ValidatorStatusJailed:
		return "jailed"
	case stake.ValidatorStatusExiting:
		return "exiting"
	default:
		return "inactive"
	}
}

func buildTransactionDetailResult(
	signature string,
	transaction structure.Transaction,
	location string,
	status string,
	blockHeight uint64,
	slot uint64,
	blockHash structure.Hash,
	leaderAddress string,
	finalized bool,
) rpc.TransactionDetailResult {
	result := rpc.TransactionDetailResult{
		Signature:           signature,
		Found:               true,
		Location:            location,
		Status:              status,
		FeeLamports:         transaction.Fee,
		SubmitTimeUnixMilli: transaction.SubmitTime,
		InstructionCount:    len(transaction.Instructions),
		BlockHeight:         blockHeight,
		Slot:                slot,
		Finalized:           finalized,
	}
	feeDetails, err := estimateTransactionFeeDetails(transaction)
	if err == nil {
		result.FeeLamports = feeDetails.TotalFee
		result.BaseFeeLamports = feeDetails.BaseFee
		result.PrioritizationFeeLamports = feeDetails.PrioritizationFee
		result.BurnedFeeLamports = feeDetails.BurnedFee
		result.LeaderFeeLamports = feeDetails.ValidatorFee
	}
	if leaderAddress != "" {
		result.LeaderAddress = leaderAddress
	}

	sender, err := transaction.Sender()
	if err == nil {
		result.Sender = sender.String()
	}
	if !transaction.RecentBlockhash.IsZero() {
		result.RecentBlockhash = transaction.RecentBlockhash.String()
	}
	if !blockHash.IsZero() {
		result.Blockhash = blockHash.String()
	}
	result.AccountAddresses = transactionAccountAddresses(transaction.Accounts)
	result.WritableAddresses = transactionWritableAddresses(transaction.Accounts)
	return result
}

func proposalLeaderAddress(proposal consensus.BlockProposal) string {
	for _, reward := range proposal.Rewards {
		if reward.Type != consensus.RewardTypeLeaderFee {
			continue
		}
		if reward.AccountAddress.IsZero() {
			continue
		}
		return reward.AccountAddress.String()
	}
	return ""
}

func transactionAccountAddresses(accounts []structure.AccountMeta) []string {
	if len(accounts) == 0 {
		return nil
	}
	addresses := make([]string, 0, len(accounts))
	for _, account := range accounts {
		addresses = append(addresses, account.PublicKey.String())
	}
	return addresses
}

func transactionWritableAddresses(accounts []structure.AccountMeta) []string {
	writableAddresses := make([]string, 0, len(accounts))
	for _, account := range accounts {
		if !account.IsWritable {
			continue
		}
		writableAddresses = append(writableAddresses, account.PublicKey.String())
	}
	if len(writableAddresses) == 0 {
		return nil
	}
	return writableAddresses
}
