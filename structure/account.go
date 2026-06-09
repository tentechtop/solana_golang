package structure

import (
	"fmt"
	"sort"
)

// NewAccountMeta 创建账户元数据 + 集中校验空公钥避免无效账户进入交易。
func NewAccountMeta(publicKey PublicKey, isSigner bool, isWritable bool) (AccountMeta, error) {
	account := AccountMeta{
		PublicKey:  publicKey,
		IsSigner:   isSigner,
		IsWritable: isWritable,
	}
	if err := account.Validate(); err != nil {
		return AccountMeta{}, err
	}
	return account, nil
}

// Validate 校验账户元数据 + 防止空账户污染消息账户列表。
func (account AccountMeta) Validate() error {
	if account.PublicKey.IsZero() {
		return fmt.Errorf("%w: public key is empty", ErrInvalidAccountMeta)
	}
	return nil
}

// IsFeePayer 判断是否可作为扣费账户 + Solana 扣费账户必须签名且可写。
func (account AccountMeta) IsFeePayer() bool {
	return account.IsSigner && account.IsWritable
}

// PermissionGroup 返回账户权限分组 + 匹配 Solana 消息头账户排序要求。
func (account AccountMeta) PermissionGroup() int {
	return accountPermissionGroup(account)
}

// CloneAccountMetas 深拷贝账户列表 + 避免调用方修改交易内部账户顺序。
func CloneAccountMetas(accounts []AccountMeta) []AccountMeta {
	return cloneAccounts(accounts)
}

// SortAccountMetasForMessage 排序账户元数据 + 生成符合 Solana header 的账户顺序。
func SortAccountMetasForMessage(accounts []AccountMeta) []AccountMeta {
	sortedAccounts := cloneAccounts(accounts)
	sort.SliceStable(sortedAccounts, func(leftIndex int, rightIndex int) bool {
		leftGroup := sortedAccounts[leftIndex].PermissionGroup()
		rightGroup := sortedAccounts[rightIndex].PermissionGroup()
		return leftGroup < rightGroup
	})
	return sortedAccounts
}

// MergeAccountMetas 合并账户权限 + 指令构建阶段复用账户时保留更高权限。
func MergeAccountMetas(accounts []AccountMeta) ([]AccountMeta, error) {
	accountIndexByKey := make(map[PublicKey]int, len(accounts))
	mergedAccounts := make([]AccountMeta, 0, len(accounts))
	for _, account := range accounts {
		if err := account.Validate(); err != nil {
			return nil, err
		}
		existingIndex, exists := accountIndexByKey[account.PublicKey]
		if !exists {
			accountIndexByKey[account.PublicKey] = len(mergedAccounts)
			mergedAccounts = append(mergedAccounts, account)
			continue
		}
		mergedAccounts[existingIndex].IsSigner = mergedAccounts[existingIndex].IsSigner || account.IsSigner
		mergedAccounts[existingIndex].IsWritable = mergedAccounts[existingIndex].IsWritable || account.IsWritable
	}
	return mergedAccounts, nil
}

// AccountIndexMap 构建账户索引表 + 为指令编译提供 O(1) 公钥定位。
func AccountIndexMap(accounts []AccountMeta) (map[PublicKey]uint8, error) {
	if len(accounts) > MaxAccountsPerTransaction {
		return nil, fmt.Errorf("structure: account count %d exceeds %d", len(accounts), MaxAccountsPerTransaction)
	}
	indexMap := make(map[PublicKey]uint8, len(accounts))
	for accountIndex, account := range accounts {
		if err := account.Validate(); err != nil {
			return nil, fmt.Errorf("structure: account %d: %w", accountIndex, err)
		}
		if _, exists := indexMap[account.PublicKey]; exists {
			return nil, fmt.Errorf("%w: duplicate account %d", ErrInvalidAccountMeta, accountIndex)
		}
		indexMap[account.PublicKey] = uint8(accountIndex)
	}
	return indexMap, nil
}
