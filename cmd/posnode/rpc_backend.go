package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"solana_golang/blockchain"
	"solana_golang/consensus"
	"solana_golang/rpc"
	"solana_golang/structure"
	"solana_golang/utils"
)

func (node *posNode) GetBalance(ctx context.Context, address string) (rpc.BalanceResult, error) {
	_ = ctx
	publicKey, err := structure.PublicKeyFromBase58(strings.TrimSpace(address))
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

func (node *posNode) TreasuryTransfer(ctx context.Context, destination string, lamports uint64) (string, error) {
	destinationKey, err := structure.PublicKeyFromBase58(strings.TrimSpace(destination))
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
	destinationKey, err := structure.PublicKeyFromBase58(strings.TrimSpace(destination))
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
	validatorKey, err := structure.PublicKeyFromBase58(strings.TrimSpace(validatorAddress))
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
	validatorKey, err := structure.PublicKeyFromBase58(strings.TrimSpace(validatorAddress))
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
	validatorKey, err := structure.PublicKeyFromBase58(strings.TrimSpace(validatorAddress))
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
	validatorKey, err := structure.PublicKeyFromBase58(strings.TrimSpace(validatorAddress))
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
