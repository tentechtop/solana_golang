package vm

import (
	"bytes"
	"fmt"
)

// SetAccount 替换账户快照 + 业务 syscall 成功后同步固定程序执行结果。
func (set *AccountSet) SetAccount(accountIndex int, account Account) error {
	if set == nil {
		return fmt.Errorf("%w: account set is nil", ErrInvalidAccount)
	}
	if accountIndex < 0 || accountIndex >= len(set.accounts) {
		return fmt.Errorf("%w: account index %d out of range", ErrInvalidAccount, accountIndex)
	}
	current := set.accounts[accountIndex]
	if current.Address != account.Address {
		return fmt.Errorf("%w: account %d address mismatch", ErrInvalidAccount, accountIndex)
	}
	if current.Owner != account.Owner || current.Executable != account.Executable {
		return fmt.Errorf("%w: account %d metadata changed", ErrPermissionDenied, accountIndex)
	}
	if !current.IsWritable && (current.Lamports != account.Lamports || !bytes.Equal(current.Data, account.Data)) {
		return fmt.Errorf("%w: account %d is readonly", ErrPermissionDenied, accountIndex)
	}
	account.IsSigner = current.IsSigner
	account.IsWritable = current.IsWritable
	set.accounts[accountIndex] = account.Clone()
	return nil
}
