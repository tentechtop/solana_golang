package main

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
	"solana_golang/rpc"
	"solana_golang/structure"
	"solana_golang/utils"
)

const (
	protocolAddressTransparent byte = 0x00
	protocolAddressPrivacy     byte = 0x01
	protocolAddressSize             = structure.PublicKeySize + 1
)

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
		Source:        source,
		StateAccount:  stateAccount,
		Amount:        lamports,
		Commitment:    commitment,
		EncryptedNote: privacyNoteBytes("deposit", lamports, commitment, structure.Hash{}),
		AuditRecords:  auditRecords,
		CreateState:   !found,
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
		Source:        source,
		StateAccount:  structure.SolanaKeyPair{PublicKey: stateKey},
		Amount:        lamports,
		Commitment:    commitment,
		EncryptedNote: privacyNoteBytes("deposit", lamports, commitment, structure.Hash{}),
		AuditRecords:  auditRecords,
		CreateState:   false,
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

func (node *posNode) PrivacyWithdraw(ctx context.Context, authoritySeed string, stateAddress string, destination string, commitment string, nullifier string, lamports uint64, auditor string, auditSecret string, expiresAtSlot uint64) (rpc.PrivacyTransactionResult, error) {
	authority, stateKey, commitmentHash, nullifierHash, err := parsePrivacySpendInputs(authoritySeed, stateAddress, commitment, nullifier)
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	destinationKey, _, err := decodeProtocolPublicKey(destination, "privacy withdraw destination")
	if err != nil {
		return rpc.PrivacyTransactionResult{}, fmt.Errorf("posnode: decode privacy withdraw destination: %w", err)
	}
	auditRecords, err := node.buildPrivacyAuditRecords(auditor, auditSecret, expiresAtSlot, structure.PrivacyAuditScopeRegulatory, structure.PrivacyAuditPayload{
		Version:         structure.PrivacyAuditPayloadVersion,
		TransactionType: structure.PrivacyInstructionWithdraw,
		Commitment:      commitmentHash,
		Nullifier:       nullifierHash,
		Amount:          lamports,
		Slot:            node.currentAuditSlot(),
	})
	if err != nil {
		return rpc.PrivacyTransactionResult{}, err
	}
	transaction, err := blockchain.NewPrivacyWithdrawTransaction(blockchain.PrivacyWithdrawTransactionParams{
		Authority:        authority,
		StateAddress:     stateKey,
		Destination:      destinationKey,
		Amount:           lamports,
		SourceCommitment: commitmentHash,
		Nullifier:        nullifierHash,
		AuditRecords:     auditRecords,
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
	return rpc.PrivacyTransactionResult{Signature: signature, PrivacyState: stateKey.String(), Commitment: commitmentHash.String(), Nullifier: nullifierHash.String()}, nil
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
	outputCommitment, err := randomPrivacyHash()
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
		Slot:             node.currentAuditSlot(),
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
	return rpc.PrivacyTransactionResult{Signature: signature, PrivacyState: stateKey.String(), Commitment: commitmentHash.String(), Nullifier: nullifierHash.String(), OutputCommitment: outputCommitment.String()}, nil
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

func (node *posNode) Stake(ctx context.Context, stakerSeed string, validatorAddress string, lamports uint64) (string, error) {
	staker, err := keyPairFromSeed(stakerSeed)
	if err != nil {
		return "", err
	}
	validatorKey, _, err := decodeProtocolPublicKey(validatorAddress, "validator address")
	if err != nil {
		return "", fmt.Errorf("posnode: decode validator address: %w", err)
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
	validatorSet, err := node.ledger.ValidatorSetFromState()
	if err != nil {
		return rpc.ValidatorSetResult{}, err
	}
	validators := validatorSet.Validators()
	result := rpc.ValidatorSetResult{Validators: make([]rpc.ValidatorInfo, len(validators))}
	for index, validator := range validators {
		result.Validators[index] = rpc.ValidatorInfo{
			ValidatorID:        string(validator.ValidatorID),
			AccountAddress:     validator.AccountAddress.String(),
			ConsensusPublicKey: validator.ConsensusPublicKey.String(),
			P2PPeerID:          validator.P2PPeerID,
			StakeLamports:      validator.StakeLamports,
			Status:             validatorStatusText(validator.Status),
			CommissionBps:      validator.CommissionBps,
		}
	}
	return result, nil
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
	head := node.ledger.Head()
	node.mutex.Lock()
	mempoolSize := len(node.mempool)
	node.mutex.Unlock()
	return rpc.HealthResult{
		OK:              true,
		HeadHeight:      head.Height,
		HeadSlot:        head.Slot,
		FinalizedHeight: head.FinalizedHeight,
		MempoolSize:     mempoolSize,
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
		return buildTransactionDetailResult(signature, transaction.Clone(), "mempool", "pending", 0, 0, structure.Hash{}, false), true, nil
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
		finalized,
	)
}

func (node *posNode) submitTransaction(ctx context.Context, transaction structure.Transaction, action string, attrs ...slog.Attr) (transactionID string, err error) {
	startedAt := time.Now()
	head := node.ledger.Head()
	defer func() {
		node.logRPCTransactionSubmit(ctx, action, transactionID, head, startedAt, err, attrs...)
	}()
	if err := node.addTransaction(transaction); err != nil {
		return "", err
	}
	transactionID, err = transaction.TxIDString()
	if err != nil {
		return "", err
	}
	node.broadcastTransaction(ctx, transaction)
	return transactionID, nil
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
			Validator:                 peerSnapshot.Validator,
			Connected:                 connected,
			BestAddress:               bestPeerAddress(peerSnapshot, connectionState, connected),
			AdvertisedAddresses:       stringifyMultiAddresses(peerSnapshot.AdvertisedAddresses),
			VerifiedAddresses:         stringifyMultiAddresses(peerSnapshot.VerifiedAddresses),
			PreferredProtocols:        stringifyProtocols(peerSnapshot.PreferredProtocols),
			LatestSlot:                peerSnapshot.LatestSlot,
			BlockHeight:               peerSnapshot.BlockHeight,
			FailureCount:              peerSnapshot.FailureCount,
			LastError:                 peerSnapshot.LastError,
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
	for _, note := range state.Notes {
		if note.Commitment == commitment && !note.Spent {
			return note.Amount, nil
		}
	}
	return 0, fmt.Errorf("posnode: unspent privacy note not found")
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

func buildTransactionDetailResult(
	signature string,
	transaction structure.Transaction,
	location string,
	status string,
	blockHeight uint64,
	slot uint64,
	blockHash structure.Hash,
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
