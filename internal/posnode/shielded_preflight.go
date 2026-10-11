package posnode

import (
	"context"
	"fmt"
	"time"

	"github.com/tentechtop/ordexus-core/structure"
	"github.com/tentechtop/ordexus-core/utils"
)

const (
	shieldedMempoolPreflightTimeout  = 5 * time.Second
	shieldedPreflightFailureCacheTTL = 10 * time.Second
	maxShieldedPreflightFailureCache = 4096
	shieldedAuthorizationIDDomain    = "solana_golang.posnode.shielded_authorization.v1"
)

type shieldedPreflightRejection struct {
	cause error
}

func (rejection shieldedPreflightRejection) Error() string {
	return rejection.cause.Error()
}

func (rejection shieldedPreflightRejection) Unwrap() error {
	return rejection.cause
}

type shieldedMempoolResources struct {
	AuthorizationID  structure.Hash
	Nullifiers       []structure.Hash
	TransparentDebit map[structure.PublicKey]uint64
}

// preflightShieldedMempoolTransaction 验证屏蔽交易后再允许入池 + 单笔状态预执行先做便宜检查并只验证一次证明。
func (node *posNode) preflightShieldedMempoolTransaction(ctx context.Context, transaction structure.Transaction) error {
	containsShieldedInstruction, err := transactionContainsShieldedInstruction(transaction)
	if err != nil {
		return err
	}
	if !containsShieldedInstruction {
		return nil
	}
	if node.ledger == nil || node.executor.Programs.IsEmpty() {
		return fmt.Errorf("posnode: shielded preflight is unavailable")
	}
	// 屏蔽证明验证串行入场，避免 P2P/RPC 并发请求同时占满配对计算资源。
	if !node.shieldedPreflightMutex.TryLock() {
		return fmt.Errorf("posnode: shielded preflight is busy")
	}
	defer node.shieldedPreflightMutex.Unlock()

	if ctx == nil {
		ctx = context.Background()
	}
	preflightContext, cancel := context.WithTimeout(ctx, shieldedMempoolPreflightTimeout)
	defer cancel()
	if err := preflightContext.Err(); err != nil {
		return fmt.Errorf("posnode: shielded preflight context: %w", err)
	}

	head := node.ledger.Head()
	if err := node.ensureHeadBlockhashAvailable(head); err != nil {
		return err
	}
	state := node.ledger.State()
	node.mutex.Lock()
	blockhashQueue := node.blockhashQueue.Clone()
	node.mutex.Unlock()
	result, err := node.executePreflightTransaction(
		preflightContext,
		transaction,
		head,
		state,
		blockhashQueue,
		make(map[string]struct{}, 1),
	)
	if err != nil {
		return err
	}
	if result.Execution.Status != structure.TransactionStatusConfirmed {
		return shieldedPreflightRejection{cause: fmt.Errorf(
			"posnode: shielded preflight transaction failed: %s",
			transactionExecutionErrorMessage(result.Execution),
		)}
	}
	return nil
}

func transactionContainsShieldedInstruction(transaction structure.Transaction) (bool, error) {
	message, err := transaction.OrdexusMessage()
	if err != nil {
		return false, err
	}
	if message.UsesAddressTable || len(message.AddressTableLookups) > 0 {
		// runtime 尚未解析地址表；准入层必须安全拒绝，避免用 loaded program id 绕过固定程序预检。
		return false, fmt.Errorf("posnode: address table transactions are not supported")
	}
	for _, compiledInstruction := range message.Instructions {
		if int(compiledInstruction.ProgramIDIndex) >= len(message.AccountKeys) {
			continue
		}
		if message.AccountKeys[compiledInstruction.ProgramIDIndex] == structure.DefaultBuiltinProgramIDs.Shielded {
			return true, nil
		}
	}
	return false, nil
}

// reserveMempoolShieldedNullifiersLocked 预留待确认 nullifier + 同一 note 的不同证明或上下文不能重复占用交易池。
func (node *posNode) reserveMempoolShieldedNullifiersLocked(transactionID string, transaction structure.Transaction) error {
	nullifiers, err := shieldedNullifiersFromTransaction(transaction)
	if err != nil {
		return err
	}
	if len(nullifiers) == 0 {
		return nil
	}
	if node.mempoolShieldedNullifiers == nil {
		node.mempoolShieldedNullifiers = make(map[structure.Hash]string)
	}
	if err := node.checkShieldedNullifierReservationsLocked(transactionID, nullifiers); err != nil {
		return err
	}
	for _, nullifier := range nullifiers {
		node.mempoolShieldedNullifiers[nullifier] = transactionID
	}
	return nil
}

// checkMempoolShieldedResourcesLocked 在证明验证前检查公开授权、nullifier 与透明借记冲突。
func (node *posNode) checkMempoolShieldedResourcesLocked(transactionID string, transaction structure.Transaction) error {
	resources, err := shieldedMempoolResourcesFromTransaction(transaction)
	if err != nil {
		return err
	}
	return node.checkShieldedResourceReservationsLocked(transactionID, resources)
}

// reserveMempoolShieldedResourcesLocked 原子预留屏蔽交易的全部待确认资源。
func (node *posNode) reserveMempoolShieldedResourcesLocked(transactionID string, transaction structure.Transaction) error {
	if _, alreadyReserved := node.mempoolShieldedDebitReservations[transactionID]; alreadyReserved {
		return nil
	}
	resources, err := shieldedMempoolResourcesFromTransaction(transaction)
	if err != nil {
		return err
	}
	if err := node.checkShieldedResourceReservationsLocked(transactionID, resources); err != nil {
		return err
	}
	if node.mempoolShieldedNullifiers == nil {
		node.mempoolShieldedNullifiers = make(map[structure.Hash]string)
	}
	if node.mempoolShieldedAuthorizations == nil {
		node.mempoolShieldedAuthorizations = make(map[structure.Hash]string)
	}
	if node.mempoolShieldedTransparentDebits == nil {
		node.mempoolShieldedTransparentDebits = make(map[structure.PublicKey]uint64)
	}
	if node.mempoolShieldedDebitReservations == nil {
		node.mempoolShieldedDebitReservations = make(map[string]map[structure.PublicKey]uint64)
	}
	for _, nullifier := range resources.Nullifiers {
		node.mempoolShieldedNullifiers[nullifier] = transactionID
	}
	node.mempoolShieldedAuthorizations[resources.AuthorizationID] = transactionID
	if len(resources.TransparentDebit) > 0 {
		reservedDebit := make(map[structure.PublicKey]uint64, len(resources.TransparentDebit))
		for address, amount := range resources.TransparentDebit {
			node.mempoolShieldedTransparentDebits[address] += amount
			reservedDebit[address] = amount
		}
		node.mempoolShieldedDebitReservations[transactionID] = reservedDebit
	}
	return nil
}

func (node *posNode) checkShieldedResourceReservationsLocked(transactionID string, resources shieldedMempoolResources) error {
	if reservedTransactionID, exists := node.mempoolShieldedAuthorizations[resources.AuthorizationID]; exists && reservedTransactionID != transactionID {
		return fmt.Errorf("posnode: shielded authorization is already reserved by transaction %s", reservedTransactionID)
	}
	if err := node.checkShieldedNullifierReservationsLocked(transactionID, resources.Nullifiers); err != nil {
		return err
	}
	if err := node.checkCommittedShieldedNullifiersLocked(resources.Nullifiers); err != nil {
		return err
	}
	return node.checkShieldedTransparentDebitReservationsLocked(transactionID, resources.TransparentDebit)
}

// checkCommittedShieldedNullifiersLocked 从最新账本状态复查 marker + 防止预执行后刚确认的双花进入交易池。
func (node *posNode) checkCommittedShieldedNullifiersLocked(nullifiers []structure.Hash) error {
	if len(nullifiers) == 0 {
		return nil
	}
	if node.ledger == nil {
		return fmt.Errorf("posnode: shielded nullifier state is unavailable")
	}
	for _, nullifier := range nullifiers {
		markerAddress := structure.DeriveShieldedNullifierAccount(structure.DefaultBuiltinProgramIDs.Shielded, nullifier)
		account, exists, err := node.ledger.Account(markerAddress)
		if err != nil {
			return fmt.Errorf("posnode: read shielded nullifier marker %s: %w", markerAddress.String(), err)
		}
		if !exists {
			continue
		}
		if account.Owner == structure.DefaultBuiltinProgramIDs.Shielded {
			return fmt.Errorf("posnode: shielded nullifier is already committed")
		}
		if account.Owner != structure.DefaultBuiltinProgramIDs.System || len(account.Data) != 0 || account.Executable {
			return fmt.Errorf("posnode: shielded nullifier marker address %s is occupied", markerAddress.String())
		}
	}
	return nil
}

func (node *posNode) checkShieldedTransparentDebitReservationsLocked(transactionID string, debits map[structure.PublicKey]uint64) error {
	if len(debits) == 0 {
		return nil
	}
	if node.ledger == nil {
		return fmt.Errorf("posnode: shielded transparent debit state is unavailable")
	}
	for address, debit := range debits {
		account, exists, err := node.ledger.Account(address)
		if err != nil {
			return fmt.Errorf("posnode: read shielded transparent debit account %s: %w", address.String(), err)
		}
		if !exists {
			return fmt.Errorf("posnode: shielded transparent debit account %s is missing", address.String())
		}
		minimumBalance, err := account.MinimumBalance(structure.DefaultRentConfig)
		if err != nil {
			return fmt.Errorf("posnode: shielded transparent debit account %s rent: %w", address.String(), err)
		}
		if account.Lamports < minimumBalance {
			return fmt.Errorf("posnode: shielded transparent debit account %s is not rent exempt", address.String())
		}
		available := account.Lamports - minimumBalance
		alreadyReserved := node.mempoolShieldedTransparentDebits[address]
		if ownDebit := node.mempoolShieldedDebitReservations[transactionID][address]; ownDebit <= alreadyReserved {
			alreadyReserved -= ownDebit
		}
		if alreadyReserved > available || debit > available-alreadyReserved {
			return fmt.Errorf(
				"posnode: shielded transparent debit for account %s exceeds available balance: reserved %d, requested %d, available %d",
				address.String(),
				alreadyReserved,
				debit,
				available,
			)
		}
	}
	return nil
}

// checkMempoolShieldedNullifiersLocked 在昂贵证明验证前执行只读冲突检查。
func (node *posNode) checkMempoolShieldedNullifiersLocked(transactionID string, transaction structure.Transaction) error {
	nullifiers, err := shieldedNullifiersFromTransaction(transaction)
	if err != nil {
		return err
	}
	return node.checkShieldedNullifierReservationsLocked(transactionID, nullifiers)
}

func (node *posNode) checkShieldedNullifierReservationsLocked(transactionID string, nullifiers []structure.Hash) error {
	for _, nullifier := range nullifiers {
		if reservedTransactionID, exists := node.mempoolShieldedNullifiers[nullifier]; exists && reservedTransactionID != transactionID {
			return fmt.Errorf("posnode: shielded nullifier is already reserved by transaction %s", reservedTransactionID)
		}
	}
	return nil
}

// releaseMempoolShieldedNullifiersLocked 释放离池交易的全部 nullifier + 扫描索引可兼容损坏交易清理路径。
func (node *posNode) releaseMempoolShieldedNullifiersLocked(transactionID string) {
	if transactionID == "" {
		return
	}
	for nullifier, reservedTransactionID := range node.mempoolShieldedNullifiers {
		if reservedTransactionID == transactionID {
			delete(node.mempoolShieldedNullifiers, nullifier)
		}
	}
}

// releaseMempoolShieldedResourcesLocked 释放离池交易的全部屏蔽资源预留。
func (node *posNode) releaseMempoolShieldedResourcesLocked(transactionID string) {
	if transactionID == "" {
		return
	}
	node.releaseMempoolShieldedNullifiersLocked(transactionID)
	for authorizationID, reservedTransactionID := range node.mempoolShieldedAuthorizations {
		if reservedTransactionID == transactionID {
			delete(node.mempoolShieldedAuthorizations, authorizationID)
		}
	}
	for address, amount := range node.mempoolShieldedDebitReservations[transactionID] {
		reserved := node.mempoolShieldedTransparentDebits[address]
		if amount >= reserved {
			delete(node.mempoolShieldedTransparentDebits, address)
			continue
		}
		node.mempoolShieldedTransparentDebits[address] = reserved - amount
	}
	delete(node.mempoolShieldedDebitReservations, transactionID)
}

func shieldedNullifiersFromTransaction(transaction structure.Transaction) ([]structure.Hash, error) {
	resources, err := shieldedMempoolResourcesFromTransaction(transaction)
	if err != nil {
		return nil, err
	}
	return resources.Nullifiers, nil
}

func shieldedMempoolResourcesFromTransaction(transaction structure.Transaction) (shieldedMempoolResources, error) {
	message, err := transaction.OrdexusMessage()
	if err != nil {
		return shieldedMempoolResources{}, err
	}
	authorizationMessage := message.Clone()
	transparentDebits := make(map[structure.PublicKey]uint64)
	nullifiers := make([]structure.Hash, 0, structure.MaxShieldedActionsPerBundle)
	seenNullifiers := make(map[structure.Hash]struct{}, structure.MaxShieldedActionsPerBundle)
	shieldedInstructionCount := 0
	for instructionIndex, compiledInstruction := range message.Instructions {
		if int(compiledInstruction.ProgramIDIndex) >= len(message.AccountKeys) {
			continue
		}
		if message.AccountKeys[compiledInstruction.ProgramIDIndex] != structure.DefaultBuiltinProgramIDs.Shielded {
			continue
		}
		shieldedInstructionCount++
		instruction, err := structure.UnmarshalShieldedTransferInstructionBinary(compiledInstruction.Data)
		if err != nil {
			return shieldedMempoolResources{}, fmt.Errorf("posnode: decode shielded instruction %d for mempool reservation: %w", instructionIndex, err)
		}
		authorizationBytes, err := instruction.AuthorizationBytes()
		if err != nil {
			return shieldedMempoolResources{}, fmt.Errorf("posnode: encode shielded instruction %d authorization: %w", instructionIndex, err)
		}
		authorizationMessage.Instructions[instructionIndex].Data = authorizationBytes
		if instruction.TransparentInputAmount > 0 {
			if len(compiledInstruction.AccountIndexes) < 2 {
				return shieldedMempoolResources{}, fmt.Errorf("posnode: shielded instruction %d is missing transparent source", instructionIndex)
			}
			sourceIndex := int(compiledInstruction.AccountIndexes[1])
			if sourceIndex >= len(message.AccountKeys) {
				return shieldedMempoolResources{}, fmt.Errorf("posnode: shielded instruction %d transparent source index is out of range", instructionIndex)
			}
			if err := addShieldedTransparentDebit(transparentDebits, message.AccountKeys[sourceIndex], instruction.TransparentInputAmount); err != nil {
				return shieldedMempoolResources{}, err
			}
		}
		for _, action := range instruction.Bundle.Actions {
			if !action.EnabledSpend {
				continue
			}
			if _, exists := seenNullifiers[action.Nullifier]; exists {
				return shieldedMempoolResources{}, fmt.Errorf("posnode: duplicate shielded nullifier across transaction instructions")
			}
			seenNullifiers[action.Nullifier] = struct{}{}
			nullifiers = append(nullifiers, action.Nullifier)
		}
	}
	if shieldedInstructionCount == 0 {
		return shieldedMempoolResources{}, fmt.Errorf("posnode: transaction does not contain a shielded instruction")
	}
	if message.Header.NumRequiredSignatures > 0 {
		if len(message.AccountKeys) == 0 {
			return shieldedMempoolResources{}, fmt.Errorf("posnode: shielded transaction fee payer is missing")
		}
		if err := addShieldedTransparentDebit(transparentDebits, message.AccountKeys[0], transaction.Fee); err != nil {
			return shieldedMempoolResources{}, err
		}
	}
	authorizationBytes, err := authorizationMessage.MarshalBinary()
	if err != nil {
		return shieldedMempoolResources{}, fmt.Errorf("posnode: encode shielded authorization message: %w", err)
	}
	authorizationPayload := make([]byte, 0, len(shieldedAuthorizationIDDomain)+8+len(authorizationBytes))
	authorizationPayload = append(authorizationPayload, shieldedAuthorizationIDDomain...)
	for shift := 0; shift < 8; shift++ {
		authorizationPayload = append(authorizationPayload, byte(transaction.Fee>>(uint(shift)*8)))
	}
	authorizationPayload = append(authorizationPayload, authorizationBytes...)
	authorizationID, err := structure.NewHash(utils.SHA256(authorizationPayload))
	if err != nil {
		return shieldedMempoolResources{}, err
	}
	return shieldedMempoolResources{
		AuthorizationID:  authorizationID,
		Nullifiers:       nullifiers,
		TransparentDebit: transparentDebits,
	}, nil
}

func addShieldedTransparentDebit(debits map[structure.PublicKey]uint64, address structure.PublicKey, amount uint64) error {
	if amount == 0 {
		return nil
	}
	current := debits[address]
	if amount > ^uint64(0)-current {
		return fmt.Errorf("posnode: shielded transparent debit overflow for account %s", address.String())
	}
	debits[address] = current + amount
	return nil
}

func (node *posNode) shieldedPreflightFailureCachedLocked(wireHash structure.Hash, now time.Time) bool {
	node.pruneShieldedPreflightFailuresLocked(now)
	expiresAt, exists := node.shieldedPreflightFailures[wireHash]
	return exists && now.Before(expiresAt)
}

func (node *posNode) recordShieldedPreflightFailureLocked(wireHash structure.Hash, now time.Time) {
	if node.shieldedPreflightFailures == nil {
		node.shieldedPreflightFailures = make(map[structure.Hash]time.Time)
	}
	node.pruneShieldedPreflightFailuresLocked(now)
	for len(node.shieldedPreflightFailures) >= maxShieldedPreflightFailureCache {
		for cachedHash := range node.shieldedPreflightFailures {
			delete(node.shieldedPreflightFailures, cachedHash)
			break
		}
	}
	node.shieldedPreflightFailures[wireHash] = now.Add(shieldedPreflightFailureCacheTTL)
}

func (node *posNode) pruneShieldedPreflightFailuresLocked(now time.Time) {
	for wireHash, expiresAt := range node.shieldedPreflightFailures {
		if !now.Before(expiresAt) {
			delete(node.shieldedPreflightFailures, wireHash)
		}
	}
}
