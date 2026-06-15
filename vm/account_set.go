package vm

import (
	"fmt"

	"solana_golang/utils"
)

// AccountSet 管理指令账户视图 + 所有写入都经过权限和 owner 校验。
type AccountSet struct {
	programID       Address
	accounts        []Account
	originals       []Account
	maxDataIncrease int
}

// NewAccountSet 创建账户集合 + 对输入账户做深拷贝保证失败可回滚。
func NewAccountSet(programID Address, accounts []Account, maxDataIncrease int) (*AccountSet, error) {
	if maxDataIncrease <= 0 {
		maxDataIncrease = DefaultMaxDataIncreasePerCall
	}
	clonedAccounts := cloneAccounts(accounts)
	return &AccountSet{
		programID:       programID,
		accounts:        clonedAccounts,
		originals:       cloneAccounts(clonedAccounts),
		maxDataIncrease: maxDataIncrease,
	}, nil
}

// Snapshot 返回执行后账户快照 + 调用方只在成功后写回。
func (set *AccountSet) Snapshot() []Account {
	if set == nil {
		return nil
	}
	return cloneAccounts(set.accounts)
}

// RequireSigner 校验账户签名权限 + 支持合约显式要求用户授权。
func (set *AccountSet) RequireSigner(accountIndex int) error {
	account, err := set.account(accountIndex)
	if err != nil {
		return err
	}
	if !account.IsSigner {
		return fmt.Errorf("%w: account %d must sign", ErrPermissionDenied, accountIndex)
	}
	return nil
}

// WriteInstructionData 写入指令数据 + 合约只能写自己 owner 的 writable 数据账户。
func (set *AccountSet) WriteInstructionData(accountIndex int, offset uint32, instructionData []byte) error {
	return set.WriteData(accountIndex, int(offset), instructionData)
}

// WriteData 写入账户数据 + 统一执行 writable、owner、executable 和增长限制。
func (set *AccountSet) WriteData(accountIndex int, offset int, data []byte) error {
	if offset < 0 {
		return fmt.Errorf("%w: negative data offset", ErrInvalidAccount)
	}
	account, err := set.mutableAccount(accountIndex)
	if err != nil {
		return err
	}
	if account.Owner != set.programID {
		return fmt.Errorf("%w: account %d owner mismatch", ErrPermissionDenied, accountIndex)
	}
	if account.Executable {
		return fmt.Errorf("%w: account %d is executable", ErrPermissionDenied, accountIndex)
	}
	nextLength := offset + len(data)
	if nextLength < offset {
		return fmt.Errorf("%w: data length overflow", ErrInvalidAccount)
	}
	if nextLength > len(account.Data)+set.maxDataIncrease {
		return fmt.Errorf("%w: data increase %d exceeds %d", ErrInvalidAccount, nextLength-len(account.Data), set.maxDataIncrease)
	}
	nextData := utils.CloneBytes(account.Data)
	if nextLength > len(nextData) {
		resizedData := make([]byte, nextLength)
		copy(resizedData, nextData)
		nextData = resizedData
	}
	copy(nextData[offset:], data)
	account.Data = nextData
	set.accounts[accountIndex] = account
	return nil
}

// TransferLamports 转移 lamports + 只允许程序扣减自己 owner 的 writable 账户。
func (set *AccountSet) TransferLamports(sourceIndex int, destinationIndex int, lamports uint64) error {
	source, err := set.mutableAccount(sourceIndex)
	if err != nil {
		return err
	}
	destination, err := set.mutableAccount(destinationIndex)
	if err != nil {
		return err
	}
	if source.Owner != set.programID {
		return fmt.Errorf("%w: source account %d owner mismatch", ErrPermissionDenied, sourceIndex)
	}
	if lamports > source.Lamports {
		return fmt.Errorf("%w: debit %d from %d", ErrInvalidAccount, lamports, source.Lamports)
	}
	if destination.Lamports > ^uint64(0)-lamports {
		return fmt.Errorf("%w: lamports overflow", ErrInvalidAccount)
	}
	source.Lamports -= lamports
	destination.Lamports += lamports
	set.accounts[sourceIndex] = source
	set.accounts[destinationIndex] = destination
	return nil
}

func (set *AccountSet) account(accountIndex int) (Account, error) {
	if set == nil {
		return Account{}, fmt.Errorf("%w: account set is nil", ErrInvalidAccount)
	}
	if accountIndex < 0 || accountIndex >= len(set.accounts) {
		return Account{}, fmt.Errorf("%w: account index %d out of range", ErrInvalidAccount, accountIndex)
	}
	return set.accounts[accountIndex].Clone(), nil
}

func (set *AccountSet) mutableAccount(accountIndex int) (Account, error) {
	if set == nil {
		return Account{}, fmt.Errorf("%w: account set is nil", ErrInvalidAccount)
	}
	if accountIndex < 0 || accountIndex >= len(set.accounts) {
		return Account{}, fmt.Errorf("%w: account index %d out of range", ErrInvalidAccount, accountIndex)
	}
	account := set.accounts[accountIndex].Clone()
	if !account.IsWritable {
		return Account{}, fmt.Errorf("%w: account %d is readonly", ErrPermissionDenied, accountIndex)
	}
	return account, nil
}
