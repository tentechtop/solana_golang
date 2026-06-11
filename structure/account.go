package structure

import (
	"fmt"
	"sort"

	"solana_golang/codec/borsh"
	"solana_golang/utils"
)

const (
	MaxAccountDataSize                   = 10 * 1024 * 1024
	MaxAccountDataIncreasePerInstruction = 10 * 1024
	RentAccountStorageOverheadBytes      = uint64(128)
	RentLamportsPerByteYear              = uint64(3480)
	RentExemptionThresholdYears          = uint64(2)
)

var DefaultRentConfig = RentConfig{
	LamportsPerByteYear:        RentLamportsPerByteYear,
	ExemptionThresholdYears:    RentExemptionThresholdYears,
	AccountStorageOverheadSize: RentAccountStorageOverheadBytes,
}

// RentConfig 描述租金参数 + 支持不同网络在不改账户结构的情况下调整成本模型。
type RentConfig struct {
	LamportsPerByteYear        uint64
	ExemptionThresholdYears    uint64
	AccountStorageOverheadSize uint64
}

// Account 描述链上账户状态 + 使用固定五字段模型保证状态存储边界清晰。
type Account struct {
	Lamports   uint64
	Data       []byte
	Owner      PublicKey
	Executable bool
	RentEpoch  uint64
}

// Validate 校验租金参数 + 防止错误参数绕过账户余额约束。
func (rentConfig RentConfig) Validate() error {
	if rentConfig.LamportsPerByteYear == 0 {
		return fmt.Errorf("%w: lamports per byte year cannot be zero", ErrInvalidRentConfig)
	}
	if rentConfig.ExemptionThresholdYears == 0 {
		return fmt.Errorf("%w: exemption threshold years cannot be zero", ErrInvalidRentConfig)
	}
	return nil
}

// MinimumBalance 计算租金豁免最小余额 + 使用数据长度和账户存储开销得到确定性结果。
func (rentConfig RentConfig) MinimumBalance(dataLength int) (uint64, error) {
	if err := rentConfig.Validate(); err != nil {
		return 0, err
	}
	if dataLength < 0 {
		return 0, fmt.Errorf("%w: negative data length", ErrInvalidAccount)
	}
	if dataLength > MaxAccountDataSize {
		return 0, fmt.Errorf("%w: data length %d exceeds %d", ErrAccountDataTooLarge, dataLength, MaxAccountDataSize)
	}

	totalBytes, err := safeAddUint64(uint64(dataLength), rentConfig.AccountStorageOverheadSize)
	if err != nil {
		return 0, fmt.Errorf("structure: calculate rent bytes: %w", err)
	}
	lamportsPerYear, err := safeMulUint64(totalBytes, rentConfig.LamportsPerByteYear)
	if err != nil {
		return 0, fmt.Errorf("structure: calculate rent yearly cost: %w", err)
	}
	minimumBalance, err := safeMulUint64(lamportsPerYear, rentConfig.ExemptionThresholdYears)
	if err != nil {
		return 0, fmt.Errorf("structure: calculate rent exemption: %w", err)
	}
	return minimumBalance, nil
}

// MinimumBalanceForRentExemption 计算默认租金豁免余额 + 为 RPC 和创建账户提供统一入口。
func MinimumBalanceForRentExemption(dataLength int) (uint64, error) {
	return DefaultRentConfig.MinimumBalance(dataLength)
}

// NewAccount 创建账户状态 + 复制 data 并在写入账本前执行租金校验。
func NewAccount(lamports uint64, data []byte, owner PublicKey, executable bool, rentEpoch uint64) (Account, error) {
	return NewAccountWithRent(lamports, data, owner, executable, rentEpoch, DefaultRentConfig)
}

// NewAccountWithRent 创建账户状态 + 允许测试网或私有网络传入自定义租金参数。
func NewAccountWithRent(lamports uint64, data []byte, owner PublicKey, executable bool, rentEpoch uint64, rentConfig RentConfig) (Account, error) {
	account := Account{
		Lamports:   lamports,
		Data:       utils.CloneBytes(data),
		Owner:      owner,
		Executable: executable,
		RentEpoch:  rentEpoch,
	}
	if err := account.ValidateWithRent(rentConfig); err != nil {
		return Account{}, err
	}
	return account, nil
}

// Validate 校验账户状态 + 防止非法账户写入账本和状态树。
func (account Account) Validate() error {
	return account.ValidateWithRent(DefaultRentConfig)
}

// ValidateWithRent 校验账户状态 + 将结构约束和租金豁免约束放在同一入口。
func (account Account) ValidateWithRent(rentConfig RentConfig) error {
	if len(account.Data) > MaxAccountDataSize {
		return fmt.Errorf("%w: data length %d exceeds %d", ErrAccountDataTooLarge, len(account.Data), MaxAccountDataSize)
	}
	if account.Lamports == 0 {
		if len(account.Data) != 0 || account.Executable {
			return fmt.Errorf("%w: zero lamport account must be empty and non-executable", ErrRentExemption)
		}
		return nil
	}
	minimumBalance, err := rentConfig.MinimumBalance(len(account.Data))
	if err != nil {
		return err
	}
	if account.Lamports < minimumBalance {
		return fmt.Errorf("%w: lamports %d below minimum %d", ErrRentExemption, account.Lamports, minimumBalance)
	}
	return nil
}

// DataLen 返回账户数据长度 + 避免调用方依赖可变切片本身。
func (account Account) DataLen() int {
	return len(account.Data)
}

// DataBytes 返回账户数据拷贝 + 防止外部修改账户内部数据。
func (account Account) DataBytes() []byte {
	return utils.CloneBytes(account.Data)
}

// MinimumBalance 计算当前账户最小余额 + 使用当前 data 长度做租金判定。
func (account Account) MinimumBalance(rentConfig RentConfig) (uint64, error) {
	return rentConfig.MinimumBalance(len(account.Data))
}

// IsRentExempt 判断账户是否满足租金豁免 + 为执行层快速准入提供布尔结果。
func (account Account) IsRentExempt(rentConfig RentConfig) (bool, error) {
	minimumBalance, err := account.MinimumBalance(rentConfig)
	if err != nil {
		return false, err
	}
	return account.Lamports >= minimumBalance, nil
}

// CreditLamports 增加账户余额 + 使用溢出检查保护账本金额一致性。
func (account *Account) CreditLamports(lamports uint64) error {
	if account == nil {
		return fmt.Errorf("%w: account is nil", ErrInvalidAccount)
	}
	nextLamports, err := safeAddUint64(account.Lamports, lamports)
	if err != nil {
		return fmt.Errorf("structure: credit lamports: %w", err)
	}
	account.Lamports = nextLamports
	return nil
}

// DebitLamports 扣减账户余额 + 扣减后仍必须满足租金豁免约束。
func (account *Account) DebitLamports(lamports uint64, rentConfig RentConfig) error {
	if account == nil {
		return fmt.Errorf("%w: account is nil", ErrInvalidAccount)
	}
	if lamports > account.Lamports {
		return fmt.Errorf("%w: debit %d from %d", ErrInsufficientLamports, lamports, account.Lamports)
	}

	nextAccount := account.Clone()
	nextAccount.Lamports -= lamports
	if err := nextAccount.ValidateWithRent(rentConfig); err != nil {
		return fmt.Errorf("structure: debit lamports: %w", err)
	}
	account.Lamports = nextAccount.Lamports
	return nil
}

// SetData 替换账户数据 + 限制单次增长并重新执行租金校验。
func (account *Account) SetData(data []byte, rentConfig RentConfig) error {
	if account == nil {
		return fmt.Errorf("%w: account is nil", ErrInvalidAccount)
	}
	if len(data) > MaxAccountDataSize {
		return fmt.Errorf("%w: data length %d exceeds %d", ErrAccountDataTooLarge, len(data), MaxAccountDataSize)
	}
	if len(data) > len(account.Data)+MaxAccountDataIncreasePerInstruction {
		return fmt.Errorf("%w: data increase %d exceeds %d", ErrAccountDataTooLarge, len(data)-len(account.Data), MaxAccountDataIncreasePerInstruction)
	}

	nextAccount := account.Clone()
	nextAccount.Data = utils.CloneBytes(data)
	if err := nextAccount.ValidateWithRent(rentConfig); err != nil {
		return fmt.Errorf("structure: set account data: %w", err)
	}
	*account = nextAccount
	return nil
}

// ResizeData 调整账户数据长度 + 新增区域按零值填充保证确定性。
func (account *Account) ResizeData(dataLength int, rentConfig RentConfig) error {
	if account == nil {
		return fmt.Errorf("%w: account is nil", ErrInvalidAccount)
	}
	if dataLength < 0 {
		return fmt.Errorf("%w: negative data length", ErrInvalidAccount)
	}
	if dataLength > MaxAccountDataSize {
		return fmt.Errorf("%w: data length %d exceeds %d", ErrAccountDataTooLarge, dataLength, MaxAccountDataSize)
	}
	if dataLength > len(account.Data)+MaxAccountDataIncreasePerInstruction {
		return fmt.Errorf("%w: data increase %d exceeds %d", ErrAccountDataTooLarge, dataLength-len(account.Data), MaxAccountDataIncreasePerInstruction)
	}

	resizedData := make([]byte, dataLength)
	copy(resizedData, account.Data)
	return account.SetData(resizedData, rentConfig)
}

// Clone 深拷贝账户状态 + 避免状态执行和缓存共享 data 切片。
func (account Account) Clone() Account {
	return Account{
		Lamports:   account.Lamports,
		Data:       utils.CloneBytes(account.Data),
		Owner:      account.Owner,
		Executable: account.Executable,
		RentEpoch:  account.RentEpoch,
	}
}

// MarshalBinary 序列化账户状态 + 使用五字段固定顺序保证账本字节确定。
func (account Account) MarshalBinary() ([]byte, error) {
	if err := account.Validate(); err != nil {
		return nil, err
	}

	writer := borsh.NewWriter(MaxAccountDataSize)
	writer.WriteUint64(account.Lamports)
	if err := writer.WriteBytes(account.Data); err != nil {
		return nil, fmt.Errorf("structure: encode account data: %w", err)
	}
	writer.WriteFixedBytes(account.Owner[:])
	writer.WriteBool(account.Executable)
	writer.WriteUint64(account.RentEpoch)
	return writer.Bytes(), nil
}

// UnmarshalAccountBinary 反序列化账户状态 + 解码后必须校验租金和尾部字节。
func UnmarshalAccountBinary(data []byte) (Account, error) {
	reader := borsh.NewReader(data, MaxAccountDataSize)
	lamports, err := reader.ReadUint64()
	if err != nil {
		return Account{}, fmt.Errorf("structure: decode account lamports: %w", err)
	}
	accountData, err := reader.ReadBytes()
	if err != nil {
		return Account{}, fmt.Errorf("structure: decode account data: %w", err)
	}
	ownerBytes, err := reader.ReadFixedBytes(PublicKeySize)
	if err != nil {
		return Account{}, fmt.Errorf("structure: decode account owner: %w", err)
	}
	owner, err := NewPublicKey(ownerBytes)
	if err != nil {
		return Account{}, fmt.Errorf("structure: decode account owner: %w", err)
	}
	executable, err := reader.ReadBool()
	if err != nil {
		return Account{}, fmt.Errorf("structure: decode account executable: %w", err)
	}
	rentEpoch, err := reader.ReadUint64()
	if err != nil {
		return Account{}, fmt.Errorf("structure: decode account rent epoch: %w", err)
	}
	if err := reader.EnsureEOF(); err != nil {
		return Account{}, fmt.Errorf("structure: decode account eof: %w", err)
	}

	account := Account{
		Lamports:   lamports,
		Data:       accountData,
		Owner:      owner,
		Executable: executable,
		RentEpoch:  rentEpoch,
	}
	return account, account.Validate()
}

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

// Validate 校验账户元数据 + 公钥全零也是有效地址因此这里只保留结构入口。
func (account AccountMeta) Validate() error {
	return nil
}

// IsFeePayer 判断是否可作为扣费账户 + 扣费账户必须签名且可写。
func (account AccountMeta) IsFeePayer() bool {
	return account.IsSigner && account.IsWritable
}

// PermissionGroup 返回账户权限分组 + 匹配消息头账户排序要求。
func (account AccountMeta) PermissionGroup() int {
	return accountPermissionGroup(account)
}

// CloneAccountMetas 深拷贝账户列表 + 避免调用方修改交易内部账户顺序。
func CloneAccountMetas(accounts []AccountMeta) []AccountMeta {
	return cloneAccounts(accounts)
}

// SortAccountMetasForMessage 排序账户元数据 + 生成符合消息头的账户顺序。
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

func safeAddUint64(left uint64, right uint64) (uint64, error) {
	if left > ^uint64(0)-right {
		return 0, fmt.Errorf("%w: uint64 addition overflow", ErrInvalidAccount)
	}
	return left + right, nil
}

func safeMulUint64(left uint64, right uint64) (uint64, error) {
	if left == 0 || right == 0 {
		return 0, nil
	}
	if left > ^uint64(0)/right {
		return 0, fmt.Errorf("%w: uint64 multiplication overflow", ErrInvalidAccount)
	}
	return left * right, nil
}
