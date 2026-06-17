package runtime

import (
	"fmt"

	"solana_golang/structure"
)

// AccountLockSet 管理交易账户锁 + 写锁互斥且读锁可共享。
type AccountLockSet struct {
	readLocks  map[structure.PublicKey]int
	writeLocks map[structure.PublicKey]struct{}
}

// NewAccountLockSet 创建账户锁集合 + 统一给 mempool 和执行调度复用。
func NewAccountLockSet() *AccountLockSet {
	return &AccountLockSet{
		readLocks:  make(map[structure.PublicKey]int),
		writeLocks: make(map[structure.PublicKey]struct{}),
	}
}

// TryLockTransaction 尝试锁定交易账户 + 有写冲突时返回 false 让调用方串行或延后。
func (locks *AccountLockSet) TryLockTransaction(transaction structure.Transaction) (bool, error) {
	if locks == nil {
		return false, fmt.Errorf("runtime: nil account lock set")
	}
	readAccounts, writeAccounts, err := TransactionLockAccounts(transaction)
	if err != nil {
		return false, err
	}
	for account := range writeAccounts {
		if _, exists := locks.writeLocks[account]; exists {
			return false, nil
		}
		if locks.readLocks[account] > 0 {
			return false, nil
		}
	}
	for account := range readAccounts {
		if _, exists := locks.writeLocks[account]; exists {
			return false, nil
		}
	}
	for account := range writeAccounts {
		locks.writeLocks[account] = struct{}{}
	}
	for account := range readAccounts {
		locks.readLocks[account]++
	}
	return true, nil
}

// UnlockTransaction 释放交易账户锁 + 供未来并行执行 worker 完成后归还锁。
func (locks *AccountLockSet) UnlockTransaction(transaction structure.Transaction) error {
	if locks == nil {
		return fmt.Errorf("runtime: nil account lock set")
	}
	readAccounts, writeAccounts, err := TransactionLockAccounts(transaction)
	if err != nil {
		return err
	}
	for account := range writeAccounts {
		delete(locks.writeLocks, account)
	}
	for account := range readAccounts {
		count := locks.readLocks[account]
		if count <= 1 {
			delete(locks.readLocks, account)
			continue
		}
		locks.readLocks[account] = count - 1
	}
	return nil
}

// TransactionLockAccounts 拆分交易读写账户 + 账户 meta 是锁粒度的唯一来源。
func TransactionLockAccounts(transaction structure.Transaction) (map[structure.PublicKey]struct{}, map[structure.PublicKey]struct{}, error) {
	message, err := transaction.SolanaMessage()
	if err != nil {
		return nil, nil, err
	}
	resolvedMessage, err := structure.NewResolvedMessage(message, structure.LoadedAddresses{})
	if err != nil {
		return nil, nil, err
	}
	readAccounts := make(map[structure.PublicKey]struct{}, len(resolvedMessage.AccountKeys))
	writeAccounts := make(map[structure.PublicKey]struct{}, len(resolvedMessage.AccountKeys))
	for accountIndex, accountKey := range resolvedMessage.AccountKeys {
		if IsWritableMessageAccount(accountIndex, resolvedMessage) {
			writeAccounts[accountKey] = struct{}{}
			continue
		}
		readAccounts[accountKey] = struct{}{}
	}
	return readAccounts, writeAccounts, nil
}
