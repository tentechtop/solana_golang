package structure

import "fmt"

// AddressedAccount 描述带地址的账户状态 + 数据库和执行层用地址定位账户 value。
type AddressedAccount struct {
	Address PublicKey
	Account Account
}

// LoadedAccount 描述执行期已加载账户 + 保存权限、程序标记和账户状态快照。
type LoadedAccount struct {
	Address    PublicKey
	Account    Account
	IsSigner   bool
	IsWritable bool
	IsProgram  bool
}

// LoadedAddresses 描述地址表解析结果 + 将可写和只读地址按执行顺序分组。
type LoadedAddresses struct {
	Writable []PublicKey
	Readonly []PublicKey
}

// LoadedAddressView 描述地址表查询视图 + 为 RPC 输出保留字符串格式。
type LoadedAddressView struct {
	Writable []string `json:"writable,omitempty"`
	Readonly []string `json:"readonly,omitempty"`
}

// ResolvedMessage 描述执行期消息 + 将静态账户和加载地址合并为完整账户表。
type ResolvedMessage struct {
	Header            MessageHeader
	StaticAccountKeys []PublicKey
	LoadedAddresses   LoadedAddresses
	AccountKeys       []PublicKey
	RecentBlockhash   Blockhash
	Instructions      []CompiledInstruction
}

// LoadedTransaction 描述已加载交易 + 绑定交易、解析消息、账户快照和费用明细。
type LoadedTransaction struct {
	Transaction Transaction
	Message     ResolvedMessage
	Accounts    []LoadedAccount
	FeePayer    PublicKey
	FeeDetails  FeeDetails
}

// Validate 校验带地址账户 + 保证账户状态自身满足账本约束。
func (addressedAccount AddressedAccount) Validate() error {
	if err := addressedAccount.Account.Validate(); err != nil {
		return fmt.Errorf("structure: validate addressed account: %w", err)
	}
	return nil
}

// Clone 深拷贝带地址账户 + 避免执行层修改数据库缓存快照。
func (addressedAccount AddressedAccount) Clone() AddressedAccount {
	return AddressedAccount{
		Address: addressedAccount.Address,
		Account: addressedAccount.Account.Clone(),
	}
}

// Validate 校验已加载账户 + 确保账户状态和权限快照可执行。
func (loadedAccount LoadedAccount) Validate() error {
	if err := loadedAccount.Account.Validate(); err != nil {
		return fmt.Errorf("structure: validate loaded account: %w", err)
	}
	return nil
}

// Clone 深拷贝已加载账户 + 避免执行器共享账户 data 切片。
func (loadedAccount LoadedAccount) Clone() LoadedAccount {
	return LoadedAccount{
		Address:    loadedAccount.Address,
		Account:    loadedAccount.Account.Clone(),
		IsSigner:   loadedAccount.IsSigner,
		IsWritable: loadedAccount.IsWritable,
		IsProgram:  loadedAccount.IsProgram,
	}
}

// Validate 校验加载地址 + 防止地址表结果超过执行账户上限或重复。
func (loadedAddresses LoadedAddresses) Validate() error {
	totalCount := len(loadedAddresses.Writable) + len(loadedAddresses.Readonly)
	if totalCount > MaxAccountsPerTransaction {
		return fmt.Errorf("%w: loaded address count %d exceeds %d", ErrInvalidLoadedTransaction, totalCount, MaxAccountsPerTransaction)
	}
	if hasDuplicatePublicKeys(append(clonePublicKeys(loadedAddresses.Writable), loadedAddresses.Readonly...)) {
		return fmt.Errorf("%w: duplicate loaded addresses", ErrInvalidLoadedTransaction)
	}
	return nil
}

// Clone 深拷贝加载地址 + 防止地址表解析结果被调用方修改。
func (loadedAddresses LoadedAddresses) Clone() LoadedAddresses {
	return LoadedAddresses{
		Writable: clonePublicKeys(loadedAddresses.Writable),
		Readonly: clonePublicKeys(loadedAddresses.Readonly),
	}
}

// ToView 转换加载地址查询视图 + 将公钥转为外部展示字符串。
func (loadedAddresses LoadedAddresses) ToView() LoadedAddressView {
	view := LoadedAddressView{
		Writable: make([]string, len(loadedAddresses.Writable)),
		Readonly: make([]string, len(loadedAddresses.Readonly)),
	}
	for index, address := range loadedAddresses.Writable {
		view.Writable[index] = address.String()
	}
	for index, address := range loadedAddresses.Readonly {
		view.Readonly[index] = address.String()
	}
	return view
}

// NewResolvedMessage 创建执行期消息 + 合并静态账户和地址表账户。
func NewResolvedMessage(message SolanaMessage, loadedAddresses LoadedAddresses) (ResolvedMessage, error) {
	if err := message.Validate(); err != nil {
		return ResolvedMessage{}, err
	}
	if err := loadedAddresses.Validate(); err != nil {
		return ResolvedMessage{}, err
	}
	if !message.UsesAddressTable && len(loadedAddresses.Writable)+len(loadedAddresses.Readonly) > 0 {
		return ResolvedMessage{}, fmt.Errorf("%w: legacy message cannot have loaded addresses", ErrInvalidLoadedTransaction)
	}

	accountKeys := clonePublicKeys(message.AccountKeys)
	accountKeys = append(accountKeys, loadedAddresses.Writable...)
	accountKeys = append(accountKeys, loadedAddresses.Readonly...)
	resolvedMessage := ResolvedMessage{
		Header:            message.Header,
		StaticAccountKeys: clonePublicKeys(message.AccountKeys),
		LoadedAddresses:   loadedAddresses.Clone(),
		AccountKeys:       accountKeys,
		RecentBlockhash:   message.RecentBlockhash,
		Instructions:      cloneInstructions(message.Instructions),
	}
	if err := resolvedMessage.Validate(); err != nil {
		return ResolvedMessage{}, err
	}
	return resolvedMessage, nil
}

// Validate 校验执行期消息 + 确认账户表、权限头和指令索引一致。
func (message ResolvedMessage) Validate() error {
	if len(message.StaticAccountKeys) == 0 {
		return ErrEmptyAccountKeys
	}
	if len(message.AccountKeys) == 0 {
		return ErrEmptyAccountKeys
	}
	if len(message.AccountKeys) > MaxAccountsPerTransaction {
		return fmt.Errorf("%w: account key count %d exceeds %d", ErrInvalidLoadedTransaction, len(message.AccountKeys), MaxAccountsPerTransaction)
	}
	if hasDuplicatePublicKeys(message.AccountKeys) {
		return fmt.Errorf("%w: duplicate account keys", ErrInvalidLoadedTransaction)
	}
	if message.RecentBlockhash == (Blockhash{}) {
		return ErrEmptyRecentBlockhash
	}
	if err := validateMessageHeader(message.Header, len(message.StaticAccountKeys)); err != nil {
		return err
	}
	if err := validateCompiledInstructions(message.Instructions, len(message.AccountKeys)); err != nil {
		return err
	}
	return message.LoadedAddresses.Validate()
}

// StaticAccountMetas 还原静态账户权限 + 给加载账户阶段生成权限快照。
func (message ResolvedMessage) StaticAccountMetas() []AccountMeta {
	return SolanaMessage{
		Header:          message.Header,
		AccountKeys:     clonePublicKeys(message.StaticAccountKeys),
		RecentBlockhash: message.RecentBlockhash,
		Instructions:    cloneInstructions(message.Instructions),
	}.StaticAccountMetas()
}

// Clone 深拷贝执行期消息 + 防止执行器修改共享账户索引表。
func (message ResolvedMessage) Clone() ResolvedMessage {
	return ResolvedMessage{
		Header:            message.Header,
		StaticAccountKeys: clonePublicKeys(message.StaticAccountKeys),
		LoadedAddresses:   message.LoadedAddresses.Clone(),
		AccountKeys:       clonePublicKeys(message.AccountKeys),
		RecentBlockhash:   message.RecentBlockhash,
		Instructions:      cloneInstructions(message.Instructions),
	}
}

// NewLoadedTransaction 创建已加载交易 + 组合交易、解析消息和账户状态。
func NewLoadedTransaction(transaction Transaction, resolvedMessage ResolvedMessage, accounts []LoadedAccount, feeDetails FeeDetails) (LoadedTransaction, error) {
	loadedTransaction := LoadedTransaction{
		Transaction: transaction.Clone(),
		Message:     resolvedMessage.Clone(),
		Accounts:    cloneLoadedAccounts(accounts),
		FeeDetails:  feeDetails,
	}
	if len(resolvedMessage.AccountKeys) > 0 {
		loadedTransaction.FeePayer = resolvedMessage.AccountKeys[0]
	}
	if err := loadedTransaction.Validate(); err != nil {
		return LoadedTransaction{}, err
	}
	return loadedTransaction, nil
}

// Validate 校验已加载交易 + 保证交易、账户快照和解析消息一致。
func (transaction LoadedTransaction) Validate() error {
	if err := transaction.Transaction.Validate(); err != nil {
		return fmt.Errorf("structure: validate loaded transaction source: %w", err)
	}
	if err := transaction.Message.Validate(); err != nil {
		return fmt.Errorf("structure: validate resolved message: %w", err)
	}
	if len(transaction.Accounts) != len(transaction.Message.AccountKeys) {
		return fmt.Errorf("%w: loaded account count %d does not match message keys %d", ErrInvalidLoadedTransaction, len(transaction.Accounts), len(transaction.Message.AccountKeys))
	}
	for accountIndex, account := range transaction.Accounts {
		if account.Address != transaction.Message.AccountKeys[accountIndex] {
			return fmt.Errorf("%w: account %d address mismatch", ErrInvalidLoadedTransaction, accountIndex)
		}
		if err := account.Validate(); err != nil {
			return fmt.Errorf("structure: loaded account %d: %w", accountIndex, err)
		}
	}
	if transaction.FeePayer != transaction.Message.AccountKeys[0] {
		return fmt.Errorf("%w: fee payer does not match first account", ErrInvalidLoadedTransaction)
	}
	return nil
}

// Clone 深拷贝已加载交易 + 防止并发执行时共享账户快照。
func (transaction LoadedTransaction) Clone() LoadedTransaction {
	return LoadedTransaction{
		Transaction: transaction.Transaction.Clone(),
		Message:     transaction.Message.Clone(),
		Accounts:    cloneLoadedAccounts(transaction.Accounts),
		FeePayer:    transaction.FeePayer,
		FeeDetails:  transaction.FeeDetails,
	}
}

func cloneLoadedAccounts(accounts []LoadedAccount) []LoadedAccount {
	if accounts == nil {
		return nil
	}
	cloned := make([]LoadedAccount, len(accounts))
	for index, account := range accounts {
		cloned[index] = account.Clone()
	}
	return cloned
}

func hasDuplicatePublicKeys(publicKeys []PublicKey) bool {
	seen := make(map[PublicKey]struct{}, len(publicKeys))
	for _, publicKey := range publicKeys {
		if _, exists := seen[publicKey]; exists {
			return true
		}
		seen[publicKey] = struct{}{}
	}
	return false
}
