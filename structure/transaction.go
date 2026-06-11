package structure

import (
	"fmt"

	"solana_golang/utils"
)

const (
	MaxSolanaTransactionSize      = 1232
	MaxSignaturesPerTransaction   = 12
	MaxAccountsPerTransaction     = 64
	MaxInstructionsPerTransaction = 64
	MaxInstructionAccounts        = MaxAccountsPerTransaction
	MaxInstructionDataSize        = MaxSolanaTransactionSize
	DefaultTransactionTTLMillis   = 400

	solanaMessageVersionPrefixMask = 0x80
)

type TransactionStatus int16

const (
	TransactionStatusPending TransactionStatus = iota
	TransactionStatusConfirmed
	TransactionStatusFailed
	TransactionStatusExpired
)

// AccountMeta 描述交易账户权限 + 支持签名校验和运行时写锁判定。
type AccountMeta struct {
	PublicKey  PublicKey
	IsSigner   bool
	IsWritable bool
}

// MessageHeader 描述交易消息头 + 用计数压缩账户签名和只读权限。
type MessageHeader struct {
	NumRequiredSignatures       uint8
	NumReadonlySignedAccounts   uint8
	NumReadonlyUnsignedAccounts uint8
}

// CompiledInstruction 描述已编译指令 + 使用 u8 账户索引匹配链上格式。
type CompiledInstruction struct {
	ProgramIDIndex uint8
	AccountIndexes []uint8
	Data           []byte
}

// MessageAddressTableLookup 描述 v0 地址表引用 + 支持大账户集合交易。
type MessageAddressTableLookup struct {
	AccountKey      PublicKey
	WritableIndexes []uint8
	ReadonlyIndexes []uint8
}

// SolanaMessage 描述可签名交易消息 + 作为交易签名和网络传输的核心结构。
type SolanaMessage struct {
	Header              MessageHeader
	AccountKeys         []PublicKey
	RecentBlockhash     Blockhash
	Instructions        []CompiledInstruction
	AddressTableLookups []MessageAddressTableLookup
	Version             uint8
	UsesAddressTable    bool
}

// Transaction 描述链上交易 + 覆盖交易池、打包、签名和执行所需字段。
type Transaction struct {
	Signatures      []Signature
	Accounts        []AccountMeta
	Instructions    []CompiledInstruction
	RecentBlockhash Blockhash
	Message         SolanaMessage
	Fee             uint64
	Size            uint64
	SubmitTime      int64
	Status          TransactionStatus
}

// Validate 校验交易结构 + 在交易池和执行层前拦截非法输入。
func (transaction Transaction) Validate() error {
	if len(transaction.Signatures) == 0 {
		return ErrEmptyTransactionSignatures
	}
	if len(transaction.Signatures) > MaxSignaturesPerTransaction {
		return fmt.Errorf("%w: signatures count %d exceeds %d", ErrInvalidTransactionEncoding, len(transaction.Signatures), MaxSignaturesPerTransaction)
	}
	message, err := transaction.SolanaMessage()
	if err != nil {
		return err
	}
	if err := message.Validate(); err != nil {
		return fmt.Errorf("structure: validate transaction message: %w", err)
	}
	return transaction.validateSignatureCount(message.Header.NumRequiredSignatures)
}

// TxID 返回交易 ID + 使用第一笔签名保持公开交易格式语义。
func (transaction Transaction) TxID() (Signature, error) {
	if len(transaction.Signatures) == 0 {
		return Signature{}, ErrEmptyTransactionSignatures
	}
	return transaction.Signatures[0], nil
}

// TxIDHex 返回交易 ID 十六进制 + 保留调试和本地索引可读性。
func (transaction Transaction) TxIDHex() (string, error) {
	transactionID, err := transaction.TxID()
	if err != nil {
		return "", err
	}
	return transactionID.Hex(), nil
}

// TxIDString 返回交易 ID 文本 + 使用 base58 签名便于外部接口展示。
func (transaction Transaction) TxIDString() (string, error) {
	transactionID, err := transaction.TxID()
	if err != nil {
		return "", err
	}
	return transactionID.String(), nil
}

// Hash 计算交易摘要 + 区块 Merkle 输入使用完整 wire bytes 避免混淆交易 ID 和摘要。
func (transaction Transaction) Hash() (Hash, error) {
	encoded, err := transaction.MarshalBinary()
	if err != nil {
		return Hash{}, err
	}
	return NewHash(utils.SHA256(encoded))
}

// Sender 返回交易发送方 + 使用第一个可写签名账户作为扣费账户。
func (transaction Transaction) Sender() (PublicKey, error) {
	for _, account := range transaction.Accounts {
		if account.IsSigner && account.IsWritable {
			return account.PublicKey, nil
		}
	}
	if !transaction.Message.IsZero() && len(transaction.Message.AccountKeys) > 0 {
		return transaction.Message.AccountKeys[0], nil
	}
	return PublicKey{}, ErrMissingWritableSigner
}

// RequiredSignatureCount 统计必需签名数 + 以账户权限作为唯一来源避免字段漂移。
func (transaction Transaction) RequiredSignatureCount() int {
	message, err := transaction.SolanaMessage()
	if err != nil {
		return 0
	}
	return int(message.Header.NumRequiredSignatures)
}

// SignerAccounts 返回签名账户快照 + 避免调用方直接修改交易账户列表。
func (transaction Transaction) SignerAccounts() []AccountMeta {
	signers := make([]AccountMeta, 0, transaction.RequiredSignatureCount())
	for _, account := range transaction.Accounts {
		if account.IsSigner {
			signers = append(signers, account)
		}
	}
	return signers
}

// IsExpired 判断交易是否过期 + 使用提交时间和 TTL 控制交易池生命周期。
func (transaction Transaction) IsExpired(currentTimeMillis int64) bool {
	return transaction.IsExpiredWithTTL(currentTimeMillis, DefaultTransactionTTLMillis)
}

// IsExpiredWithTTL 判断交易是否过期 + 允许调用方按网络配置传入 TTL。
func (transaction Transaction) IsExpiredWithTTL(currentTimeMillis int64, ttlMillis int64) bool {
	if transaction.SubmitTime <= 0 || ttlMillis <= 0 {
		return true
	}
	return currentTimeMillis-transaction.SubmitTime > ttlMillis
}

// BuildSignData 构造签名数据 + 只序列化账户、区块哈希和指令避免签名自引用。
func (transaction Transaction) BuildSignData() ([]byte, error) {
	message, err := transaction.SolanaMessage()
	if err != nil {
		return nil, err
	}
	return message.MarshalBinary()
}

// MarshalBinary 序列化链上交易 + 只包含签名和 message 避免业务元数据污染链上格式。
func (transaction Transaction) MarshalBinary() ([]byte, error) {
	if err := transaction.Validate(); err != nil {
		return nil, err
	}

	encoded := make([]byte, 0, transaction.EstimatedSize())
	if err := appendSignatures(&encoded, transaction.Signatures); err != nil {
		return nil, fmt.Errorf("structure: encode signatures: %w", err)
	}
	signData, err := transaction.BuildSignData()
	if err != nil {
		return nil, fmt.Errorf("structure: build sign data: %w", err)
	}
	encoded = append(encoded, signData...)
	if len(encoded) > MaxSolanaTransactionSize {
		return nil, fmt.Errorf("%w: got %d, max %d", ErrTransactionTooLarge, len(encoded), MaxSolanaTransactionSize)
	}
	return encoded, nil
}

// UnmarshalTransactionBinary 反序列化链上交易 + 支持 legacy 和 v0 版本化 wire format。
func UnmarshalTransactionBinary(data []byte) (Transaction, error) {
	if len(data) == 0 {
		return Transaction{}, fmt.Errorf("%w: empty transaction", ErrInvalidTransactionEncoding)
	}
	if len(data) > MaxSolanaTransactionSize {
		return Transaction{}, fmt.Errorf("%w: got %d, max %d", ErrTransactionTooLarge, len(data), MaxSolanaTransactionSize)
	}

	offset := 0
	signatures, err := readSignatures(data, &offset)
	if err != nil {
		return Transaction{}, fmt.Errorf("structure: decode signatures: %w", err)
	}
	message, err := readSolanaMessage(data, &offset)
	if err != nil {
		return Transaction{}, fmt.Errorf("structure: decode message: %w", err)
	}
	if offset != len(data) {
		return Transaction{}, fmt.Errorf("%w: %d trailing bytes", ErrInvalidTransactionEncoding, len(data)-offset)
	}

	transaction := Transaction{
		Signatures:      signatures,
		Accounts:        message.StaticAccountMetas(),
		Instructions:    cloneInstructions(message.Instructions),
		RecentBlockhash: message.RecentBlockhash,
		Message:         message,
		Size:            uint64(len(data)),
	}
	return transaction, transaction.Validate()
}

// EstimatedSize 估算交易大小 + 预分配容量减少热路径内存分配。
func (transaction Transaction) EstimatedSize() int {
	message, err := transaction.SolanaMessage()
	if err != nil {
		return 3 + len(transaction.Signatures)*SignatureSize
	}
	return 3 + len(transaction.Signatures)*SignatureSize + estimateMessageSize(message)
}

// CalculatedSize 计算真实序列化大小 + 为交易池限流和区块打包提供依据。
func (transaction Transaction) CalculatedSize() (uint64, error) {
	encoded, err := transaction.MarshalBinary()
	if err != nil {
		return 0, err
	}
	return uint64(len(encoded)), nil
}

// Clone 深拷贝交易 + 防止跨协程共享切片导致竞态修改。
func (transaction Transaction) Clone() Transaction {
	return Transaction{
		Signatures:      cloneSignatures(transaction.Signatures),
		Accounts:        cloneAccounts(transaction.Accounts),
		Instructions:    cloneInstructions(transaction.Instructions),
		RecentBlockhash: transaction.RecentBlockhash,
		Message:         transaction.Message.Clone(),
		Fee:             transaction.Fee,
		Size:            transaction.Size,
		SubmitTime:      transaction.SubmitTime,
		Status:          transaction.Status,
	}
}

// Clone 深拷贝指令 + 避免调用方共享底层切片造成数据串改。
func (instruction CompiledInstruction) Clone() CompiledInstruction {
	return CompiledInstruction{
		ProgramIDIndex: instruction.ProgramIDIndex,
		AccountIndexes: cloneUint8Slice(instruction.AccountIndexes),
		Data:           utils.CloneBytes(instruction.Data),
	}
}

// SolanaMessage 返回交易消息 + 优先使用显式 Message 否则从账户元数据构建。
func (transaction Transaction) SolanaMessage() (SolanaMessage, error) {
	if !transaction.Message.IsZero() {
		return transaction.Message.Clone(), nil
	}
	if err := transaction.validateAccounts(); err != nil {
		return SolanaMessage{}, err
	}
	if transaction.RecentBlockhash == (Blockhash{}) {
		return SolanaMessage{}, ErrEmptyRecentBlockhash
	}
	if err := transaction.validateInstructions(); err != nil {
		return SolanaMessage{}, err
	}
	return SolanaMessage{
		Header:          buildMessageHeader(transaction.Accounts),
		AccountKeys:     accountPublicKeys(transaction.Accounts),
		RecentBlockhash: transaction.RecentBlockhash,
		Instructions:    cloneInstructions(transaction.Instructions),
	}, nil
}

// Validate 校验交易消息 + 防止账户索引、地址表和权限头越界。
func (message SolanaMessage) Validate() error {
	if message.UsesAddressTable && message.Version != 0 {
		return fmt.Errorf("%w: only v0 message is supported", ErrInvalidMessageVersion)
	}
	if !message.UsesAddressTable && message.Version != 0 {
		return fmt.Errorf("%w: legacy message cannot carry a version", ErrInvalidMessageVersion)
	}
	if !message.UsesAddressTable && len(message.AddressTableLookups) > 0 {
		return fmt.Errorf("%w: legacy message cannot contain address table lookups", ErrInvalidAddressTableLookup)
	}
	if len(message.AccountKeys) == 0 {
		return ErrEmptyAccountKeys
	}
	accountCount := message.totalAccountCount()
	if accountCount > MaxAccountsPerTransaction {
		return fmt.Errorf("structure: account key count %d exceeds %d", accountCount, MaxAccountsPerTransaction)
	}
	if message.RecentBlockhash == (Blockhash{}) {
		return ErrEmptyRecentBlockhash
	}
	if err := validateMessageHeader(message.Header, len(message.AccountKeys)); err != nil {
		return err
	}
	if err := validateCompiledInstructions(message.Instructions, accountCount); err != nil {
		return err
	}
	return validateAddressTableLookups(message.AddressTableLookups)
}

// MarshalBinary 序列化交易消息 + 使用 short_vec 编码保持链上格式稳定。
func (message SolanaMessage) MarshalBinary() ([]byte, error) {
	if err := message.Validate(); err != nil {
		return nil, err
	}

	encoded := make([]byte, 0, estimateMessageSize(message))
	if message.UsesAddressTable {
		encoded = append(encoded, solanaMessageVersionPrefixMask|message.Version)
	}
	encoded = append(encoded, message.Header.NumRequiredSignatures)
	encoded = append(encoded, message.Header.NumReadonlySignedAccounts)
	encoded = append(encoded, message.Header.NumReadonlyUnsignedAccounts)
	if err := appendPublicKeys(&encoded, message.AccountKeys); err != nil {
		return nil, fmt.Errorf("structure: encode account keys: %w", err)
	}
	encoded = append(encoded, message.RecentBlockhash[:]...)
	if err := appendInstructions(&encoded, message.Instructions); err != nil {
		return nil, fmt.Errorf("structure: encode instructions: %w", err)
	}
	if message.UsesAddressTable {
		return appendAddressTableLookups(&encoded, message.AddressTableLookups)
	}
	return encoded, nil
}

// UnmarshalSolanaMessageBinary 反序列化交易消息 + 自动识别 legacy 和 v0 前缀。
func UnmarshalSolanaMessageBinary(data []byte) (SolanaMessage, error) {
	if len(data) == 0 {
		return SolanaMessage{}, fmt.Errorf("%w: empty message", ErrInvalidTransactionEncoding)
	}
	offset := 0
	message, err := readSolanaMessage(data, &offset)
	if err != nil {
		return SolanaMessage{}, err
	}
	if offset != len(data) {
		return SolanaMessage{}, fmt.Errorf("%w: %d trailing bytes", ErrInvalidTransactionEncoding, len(data)-offset)
	}
	return message, message.Validate()
}

// Clone 深拷贝交易消息 + 避免交易结构跨协程共享切片。
func (message SolanaMessage) Clone() SolanaMessage {
	return SolanaMessage{
		Header:              message.Header,
		AccountKeys:         clonePublicKeys(message.AccountKeys),
		RecentBlockhash:     message.RecentBlockhash,
		Instructions:        cloneInstructions(message.Instructions),
		AddressTableLookups: cloneAddressTableLookups(message.AddressTableLookups),
		Version:             message.Version,
		UsesAddressTable:    message.UsesAddressTable,
	}
}

// IsZero 判断是否未显式设置消息 + 便于兼容账户元数据构建模式。
func (message SolanaMessage) IsZero() bool {
	return len(message.AccountKeys) == 0 && len(message.Instructions) == 0 && message.RecentBlockhash == (Blockhash{})
}

// StaticAccountMetas 还原静态账户权限 + 按 message header 计算 signer 和 writable。
func (message SolanaMessage) StaticAccountMetas() []AccountMeta {
	accounts := make([]AccountMeta, len(message.AccountKeys))
	requiredSignatures := int(message.Header.NumRequiredSignatures)
	readonlySignedStart := requiredSignatures - int(message.Header.NumReadonlySignedAccounts)
	readonlyUnsignedStart := len(message.AccountKeys) - int(message.Header.NumReadonlyUnsignedAccounts)
	for accountIndex, publicKey := range message.AccountKeys {
		isSigner := accountIndex < requiredSignatures
		isReadonlySigned := isSigner && accountIndex >= readonlySignedStart
		isReadonlyUnsigned := !isSigner && accountIndex >= readonlyUnsignedStart
		accounts[accountIndex] = AccountMeta{
			PublicKey:  publicKey,
			IsSigner:   isSigner,
			IsWritable: !isReadonlySigned && !isReadonlyUnsigned,
		}
	}
	return accounts
}

// totalAccountCount 统计消息账户总数 + 将地址表展开账户计入索引边界。
func (message SolanaMessage) totalAccountCount() int {
	accountCount := len(message.AccountKeys)
	for _, lookup := range message.AddressTableLookups {
		accountCount += len(lookup.WritableIndexes) + len(lookup.ReadonlyIndexes)
	}
	return accountCount
}

// validateAccounts 校验交易账户集合 + 保证唯一性、顺序和扣费账户可用。
func (transaction Transaction) validateAccounts() error {
	if len(transaction.Accounts) == 0 {
		return ErrEmptyAccountKeys
	}
	if len(transaction.Accounts) > MaxAccountsPerTransaction {
		return fmt.Errorf("structure: account count %d exceeds %d", len(transaction.Accounts), MaxAccountsPerTransaction)
	}
	if err := validateUniqueAccounts(transaction.Accounts); err != nil {
		return err
	}
	if err := validateAccountOrder(transaction.Accounts); err != nil {
		return err
	}
	if _, err := transaction.Sender(); err != nil {
		return err
	}
	return nil
}

// validateUniqueAccounts 校验账户唯一性 + 同时逐项执行账户元数据校验。
func validateUniqueAccounts(accounts []AccountMeta) error {
	seenAccounts := make(map[PublicKey]struct{}, len(accounts))
	for accountIndex, account := range accounts {
		if err := account.Validate(); err != nil {
			return fmt.Errorf("structure: account %d: %w", accountIndex, err)
		}
		if _, exists := seenAccounts[account.PublicKey]; exists {
			return fmt.Errorf("%w: duplicate account %d", ErrInvalidAccountMeta, accountIndex)
		}
		seenAccounts[account.PublicKey] = struct{}{}
	}
	return nil
}

// validateAccountOrder 校验账户排列顺序 + 保持与消息头计数规则一致。
func validateAccountOrder(accounts []AccountMeta) error {
	lastGroup := 0
	for accountIndex, account := range accounts {
		group := accountPermissionGroup(account)
		if group < lastGroup {
			return fmt.Errorf("%w: account %d order violates message header layout", ErrInvalidAccountMeta, accountIndex)
		}
		lastGroup = group
	}
	return nil
}

// validateInstructions 校验交易指令集合 + 使用当前账户数量约束指令索引。
func (transaction Transaction) validateInstructions() error {
	return validateCompiledInstructions(transaction.Instructions, len(transaction.Accounts))
}

// validateSignatureCount 校验签名数量 + 必须与消息头必需签名数完全一致。
func (transaction Transaction) validateSignatureCount(requiredSignatures uint8) error {
	if len(transaction.Signatures) != int(requiredSignatures) {
		return fmt.Errorf("structure: signatures count %d does not match required %d", len(transaction.Signatures), requiredSignatures)
	}
	return nil
}

// validateCompiledInstructions 校验编译后指令列表 + 限制数量并逐条检查索引。
func validateCompiledInstructions(instructions []CompiledInstruction, accountCount int) error {
	if len(instructions) == 0 {
		return ErrEmptyInstructions
	}
	if len(instructions) > MaxInstructionsPerTransaction {
		return fmt.Errorf("structure: instruction count %d exceeds %d", len(instructions), MaxInstructionsPerTransaction)
	}
	for instructionIndex, instruction := range instructions {
		if err := validateInstruction(instruction, accountCount); err != nil {
			return fmt.Errorf("structure: instruction %d: %w", instructionIndex, err)
		}
	}
	return nil
}

// validateInstruction 校验单条指令 + 防止程序索引、账户索引和数据长度越界。
func validateInstruction(instruction CompiledInstruction, accountCount int) error {
	if int(instruction.ProgramIDIndex) >= accountCount {
		return fmt.Errorf("%w: program id index %d out of range", ErrInvalidInstruction, instruction.ProgramIDIndex)
	}
	if len(instruction.AccountIndexes) > MaxInstructionAccounts {
		return fmt.Errorf("%w: account index count %d exceeds %d", ErrInvalidInstruction, len(instruction.AccountIndexes), MaxInstructionAccounts)
	}
	if len(instruction.Data) > MaxInstructionDataSize {
		return fmt.Errorf("%w: data size %d exceeds %d", ErrInvalidInstruction, len(instruction.Data), MaxInstructionDataSize)
	}
	for _, accountIndex := range instruction.AccountIndexes {
		if int(accountIndex) >= accountCount {
			return fmt.Errorf("%w: account index %d out of range", ErrInvalidInstruction, accountIndex)
		}
	}
	return nil
}

// validateMessageHeader 校验消息头计数 + 保证签名和只读账户数量不越界。
func validateMessageHeader(header MessageHeader, accountKeyCount int) error {
	if header.NumRequiredSignatures == 0 {
		return fmt.Errorf("%w: required signatures cannot be zero", ErrInvalidMessageHeader)
	}
	if int(header.NumRequiredSignatures) > accountKeyCount {
		return fmt.Errorf("%w: required signatures exceed account keys", ErrInvalidMessageHeader)
	}
	if header.NumReadonlySignedAccounts > header.NumRequiredSignatures {
		return fmt.Errorf("%w: readonly signed accounts exceed signed accounts", ErrInvalidMessageHeader)
	}

	unsignedAccounts := accountKeyCount - int(header.NumRequiredSignatures)
	if int(header.NumReadonlyUnsignedAccounts) > unsignedAccounts {
		return fmt.Errorf("%w: readonly unsigned accounts exceed unsigned accounts", ErrInvalidMessageHeader)
	}
	return nil
}

// validateAddressTableLookups 校验地址表引用 + 地址表账户公钥不能为空。
func validateAddressTableLookups(lookups []MessageAddressTableLookup) error {
	for lookupIndex, lookup := range lookups {
		if lookup.AccountKey.IsZero() {
			return fmt.Errorf("%w: address table %d account key is empty", ErrInvalidAddressTableLookup, lookupIndex)
		}
	}
	return nil
}

// appendSignatures 编码签名列表 + 使用 short_vec 长度保持链上格式稳定。
func appendSignatures(encoded *[]byte, signatures []Signature) error {
	if err := appendShortVecLength(encoded, len(signatures)); err != nil {
		return err
	}
	for _, signature := range signatures {
		*encoded = append(*encoded, signature[:]...)
	}
	return nil
}

// appendPublicKeys 编码账户公钥列表 + 使用 short_vec 长度保持链上兼容。
func appendPublicKeys(encoded *[]byte, publicKeys []PublicKey) error {
	if err := appendShortVecLength(encoded, len(publicKeys)); err != nil {
		return err
	}
	for _, publicKey := range publicKeys {
		*encoded = append(*encoded, publicKey[:]...)
	}
	return nil
}

// appendInstructions 编码指令列表 + 为每条指令补充索引上下文错误。
func appendInstructions(encoded *[]byte, instructions []CompiledInstruction) error {
	if err := appendShortVecLength(encoded, len(instructions)); err != nil {
		return err
	}
	for instructionIndex, instruction := range instructions {
		if err := appendInstruction(encoded, instruction); err != nil {
			return fmt.Errorf("structure: encode instruction %d: %w", instructionIndex, err)
		}
	}
	return nil
}

// appendInstruction 编码单条指令 + 按程序索引、账户索引、数据顺序写入。
func appendInstruction(encoded *[]byte, instruction CompiledInstruction) error {
	*encoded = append(*encoded, instruction.ProgramIDIndex)
	if err := appendUint8Indexes(encoded, instruction.AccountIndexes); err != nil {
		return fmt.Errorf("structure: encode instruction accounts: %w", err)
	}
	if err := appendShortVecLength(encoded, len(instruction.Data)); err != nil {
		return fmt.Errorf("structure: encode instruction data length: %w", err)
	}
	*encoded = append(*encoded, instruction.Data...)
	return nil
}

// appendUint8Indexes 编码 u8 索引列表 + 使用 short_vec 表达可变长度集合。
func appendUint8Indexes(encoded *[]byte, indexes []uint8) error {
	if err := appendShortVecLength(encoded, len(indexes)); err != nil {
		return err
	}
	*encoded = append(*encoded, indexes...)
	return nil
}

// appendAddressTableLookups 编码地址表引用 + 分别写入可写和只读索引集合。
func appendAddressTableLookups(encoded *[]byte, lookups []MessageAddressTableLookup) ([]byte, error) {
	if err := appendShortVecLength(encoded, len(lookups)); err != nil {
		return nil, err
	}
	for _, lookup := range lookups {
		*encoded = append(*encoded, lookup.AccountKey[:]...)
		if err := appendUint8Indexes(encoded, lookup.WritableIndexes); err != nil {
			return nil, fmt.Errorf("structure: encode writable lookup indexes: %w", err)
		}
		if err := appendUint8Indexes(encoded, lookup.ReadonlyIndexes); err != nil {
			return nil, fmt.Errorf("structure: encode readonly lookup indexes: %w", err)
		}
	}
	return *encoded, nil
}

// appendShortVecLength 追加 short_vec 长度 + 统一处理长度编码错误。
func appendShortVecLength(encoded *[]byte, length int) error {
	lengthBytes, err := utils.EncodeShortVecLength(length)
	if err != nil {
		return err
	}
	*encoded = append(*encoded, lengthBytes...)
	return nil
}

func readSignatures(data []byte, offset *int) ([]Signature, error) {
	signatureCount, err := readShortVecLength(data, offset)
	if err != nil {
		return nil, err
	}
	if signatureCount == 0 {
		return nil, ErrEmptyTransactionSignatures
	}
	if signatureCount > MaxSignaturesPerTransaction {
		return nil, fmt.Errorf("%w: signatures count %d exceeds %d", ErrInvalidTransactionEncoding, signatureCount, MaxSignaturesPerTransaction)
	}

	signatures := make([]Signature, signatureCount)
	for signatureIndex := range signatures {
		signatureBytes, err := readFixedBytes(data, offset, SignatureSize)
		if err != nil {
			return nil, fmt.Errorf("signature %d: %w", signatureIndex, err)
		}
		signature, err := NewSignature(signatureBytes)
		if err != nil {
			return nil, err
		}
		signatures[signatureIndex] = signature
	}
	return signatures, nil
}

func readSolanaMessage(data []byte, offset *int) (SolanaMessage, error) {
	if *offset >= len(data) {
		return SolanaMessage{}, fmt.Errorf("%w: missing message header", ErrInvalidTransactionEncoding)
	}

	firstByte, err := readUint8(data, offset)
	if err != nil {
		return SolanaMessage{}, err
	}
	message := SolanaMessage{}
	if firstByte&solanaMessageVersionPrefixMask != 0 {
		message.UsesAddressTable = true
		message.Version = firstByte &^ solanaMessageVersionPrefixMask
		if message.Version != 0 {
			return SolanaMessage{}, fmt.Errorf("%w: unsupported version %d", ErrInvalidMessageVersion, message.Version)
		}
		message.Header.NumRequiredSignatures, err = readUint8(data, offset)
	} else {
		message.Header.NumRequiredSignatures = firstByte
	}
	if err != nil {
		return SolanaMessage{}, err
	}
	if message.Header.NumReadonlySignedAccounts, err = readUint8(data, offset); err != nil {
		return SolanaMessage{}, err
	}
	if message.Header.NumReadonlyUnsignedAccounts, err = readUint8(data, offset); err != nil {
		return SolanaMessage{}, err
	}

	if message.AccountKeys, err = readPublicKeys(data, offset); err != nil {
		return SolanaMessage{}, fmt.Errorf("account keys: %w", err)
	}
	blockhashBytes, err := readFixedBytes(data, offset, HashSize)
	if err != nil {
		return SolanaMessage{}, fmt.Errorf("recent blockhash: %w", err)
	}
	recentBlockhash, err := NewHash(blockhashBytes)
	if err != nil {
		return SolanaMessage{}, err
	}
	message.RecentBlockhash = recentBlockhash
	if message.Instructions, err = readInstructions(data, offset); err != nil {
		return SolanaMessage{}, fmt.Errorf("instructions: %w", err)
	}
	if message.UsesAddressTable {
		if message.AddressTableLookups, err = readAddressTableLookups(data, offset); err != nil {
			return SolanaMessage{}, fmt.Errorf("address table lookups: %w", err)
		}
	}
	return message, message.Validate()
}

func readPublicKeys(data []byte, offset *int) ([]PublicKey, error) {
	keyCount, err := readShortVecLength(data, offset)
	if err != nil {
		return nil, err
	}
	if keyCount == 0 {
		return nil, ErrEmptyAccountKeys
	}
	if keyCount > MaxAccountsPerTransaction {
		return nil, fmt.Errorf("%w: account key count %d exceeds %d", ErrInvalidTransactionEncoding, keyCount, MaxAccountsPerTransaction)
	}

	publicKeys := make([]PublicKey, keyCount)
	for keyIndex := range publicKeys {
		keyBytes, err := readFixedBytes(data, offset, PublicKeySize)
		if err != nil {
			return nil, fmt.Errorf("account key %d: %w", keyIndex, err)
		}
		publicKey, err := NewPublicKey(keyBytes)
		if err != nil {
			return nil, err
		}
		publicKeys[keyIndex] = publicKey
	}
	return publicKeys, nil
}

func readInstructions(data []byte, offset *int) ([]CompiledInstruction, error) {
	instructionCount, err := readShortVecLength(data, offset)
	if err != nil {
		return nil, err
	}
	if instructionCount == 0 {
		return nil, ErrEmptyInstructions
	}
	if instructionCount > MaxInstructionsPerTransaction {
		return nil, fmt.Errorf("%w: instruction count %d exceeds %d", ErrInvalidTransactionEncoding, instructionCount, MaxInstructionsPerTransaction)
	}

	instructions := make([]CompiledInstruction, instructionCount)
	for instructionIndex := range instructions {
		instruction, err := readInstruction(data, offset)
		if err != nil {
			return nil, fmt.Errorf("instruction %d: %w", instructionIndex, err)
		}
		instructions[instructionIndex] = instruction
	}
	return instructions, nil
}

func readInstruction(data []byte, offset *int) (CompiledInstruction, error) {
	programIDIndex, err := readUint8(data, offset)
	if err != nil {
		return CompiledInstruction{}, fmt.Errorf("program id index: %w", err)
	}
	accountIndexes, err := readUint8Indexes(data, offset)
	if err != nil {
		return CompiledInstruction{}, fmt.Errorf("account indexes: %w", err)
	}
	instructionData, err := readShortBytes(data, offset, MaxInstructionDataSize)
	if err != nil {
		return CompiledInstruction{}, fmt.Errorf("instruction data: %w", err)
	}
	return CompiledInstruction{
		ProgramIDIndex: programIDIndex,
		AccountIndexes: accountIndexes,
		Data:           instructionData,
	}, nil
}

func readAddressTableLookups(data []byte, offset *int) ([]MessageAddressTableLookup, error) {
	lookupCount, err := readShortVecLength(data, offset)
	if err != nil {
		return nil, err
	}
	lookups := make([]MessageAddressTableLookup, lookupCount)
	for lookupIndex := range lookups {
		accountKeyBytes, err := readFixedBytes(data, offset, PublicKeySize)
		if err != nil {
			return nil, fmt.Errorf("lookup %d account key: %w", lookupIndex, err)
		}
		accountKey, err := NewPublicKey(accountKeyBytes)
		if err != nil {
			return nil, err
		}
		writableIndexes, err := readUint8Indexes(data, offset)
		if err != nil {
			return nil, fmt.Errorf("lookup %d writable indexes: %w", lookupIndex, err)
		}
		readonlyIndexes, err := readUint8Indexes(data, offset)
		if err != nil {
			return nil, fmt.Errorf("lookup %d readonly indexes: %w", lookupIndex, err)
		}
		lookups[lookupIndex] = MessageAddressTableLookup{
			AccountKey:      accountKey,
			WritableIndexes: writableIndexes,
			ReadonlyIndexes: readonlyIndexes,
		}
	}
	return lookups, nil
}

func readUint8Indexes(data []byte, offset *int) ([]uint8, error) {
	indexCount, err := readShortVecLength(data, offset)
	if err != nil {
		return nil, err
	}
	if indexCount > MaxInstructionAccounts {
		return nil, fmt.Errorf("%w: index count %d exceeds %d", ErrInvalidTransactionEncoding, indexCount, MaxInstructionAccounts)
	}
	return readFixedBytes(data, offset, indexCount)
}

func readShortBytes(data []byte, offset *int, maxLength int) ([]byte, error) {
	length, err := readShortVecLength(data, offset)
	if err != nil {
		return nil, err
	}
	if length > maxLength {
		return nil, fmt.Errorf("%w: byte length %d exceeds %d", ErrInvalidTransactionEncoding, length, maxLength)
	}
	return readFixedBytes(data, offset, length)
}

func readShortVecLength(data []byte, offset *int) (int, error) {
	length, bytesRead, err := utils.DecodeShortVecLength(data[*offset:])
	if err != nil {
		return 0, err
	}
	*offset += bytesRead
	return length, nil
}

func readUint8(data []byte, offset *int) (uint8, error) {
	if *offset >= len(data) {
		return 0, fmt.Errorf("%w: unexpected end", ErrInvalidTransactionEncoding)
	}
	value := data[*offset]
	*offset = *offset + 1
	return value, nil
}

func readFixedBytes(data []byte, offset *int, length int) ([]byte, error) {
	if length < 0 {
		return nil, fmt.Errorf("%w: negative length", ErrInvalidTransactionEncoding)
	}
	if len(data)-*offset < length {
		return nil, fmt.Errorf("%w: need %d bytes, remaining %d", ErrInvalidTransactionEncoding, length, len(data)-*offset)
	}
	value := utils.CloneBytes(data[*offset : *offset+length])
	*offset += length
	return value, nil
}

// estimateMessageSize 估算消息编码大小 + 为序列化预分配容量降低分配次数。
func estimateMessageSize(message SolanaMessage) int {
	size := 3 + 3 + len(message.AccountKeys)*PublicKeySize + PublicKeySize
	for _, instruction := range message.Instructions {
		size += 1 + 3 + len(instruction.AccountIndexes) + 3 + len(instruction.Data)
	}
	if message.UsesAddressTable {
		size += estimateAddressTableLookupSize(message.AddressTableLookups)
	}
	return size
}

// estimateAddressTableLookupSize 估算地址表编码大小 + 计入公钥和两组索引长度。
func estimateAddressTableLookupSize(lookups []MessageAddressTableLookup) int {
	size := 3
	for _, lookup := range lookups {
		size += PublicKeySize + 3 + len(lookup.WritableIndexes) + 3 + len(lookup.ReadonlyIndexes)
	}
	return size
}

// buildMessageHeader 构建消息头 + 从账户权限统计签名和只读账户数量。
func buildMessageHeader(accounts []AccountMeta) MessageHeader {
	header := MessageHeader{}
	for _, account := range accounts {
		if account.IsSigner {
			header.NumRequiredSignatures++
		}
		if account.IsSigner && !account.IsWritable {
			header.NumReadonlySignedAccounts++
		}
		if !account.IsSigner && !account.IsWritable {
			header.NumReadonlyUnsignedAccounts++
		}
	}
	return header
}

// accountPublicKeys 提取账户公钥顺序 + 保持与账户元数据排列一致。
func accountPublicKeys(accounts []AccountMeta) []PublicKey {
	publicKeys := make([]PublicKey, len(accounts))
	for index, account := range accounts {
		publicKeys[index] = account.PublicKey
	}
	return publicKeys
}

// accountPermissionGroup 计算账户排序分组 + 匹配签名和写权限布局。
func accountPermissionGroup(account AccountMeta) int {
	if account.IsSigner && account.IsWritable {
		return 0
	}
	if account.IsSigner {
		return 1
	}
	if account.IsWritable {
		return 2
	}
	return 3
}
func cloneSignatures(value []Signature) []Signature {
	if value == nil {
		return nil
	}
	cloned := make([]Signature, len(value))
	copy(cloned, value)
	return cloned
}
func cloneAccounts(value []AccountMeta) []AccountMeta {
	if value == nil {
		return nil
	}
	cloned := make([]AccountMeta, len(value))
	copy(cloned, value)
	return cloned
}
func cloneInstructions(value []CompiledInstruction) []CompiledInstruction {
	if value == nil {
		return nil
	}
	cloned := make([]CompiledInstruction, len(value))
	for index, instruction := range value {
		cloned[index] = instruction.Clone()
	}
	return cloned
}
func clonePublicKeys(value []PublicKey) []PublicKey {
	if value == nil {
		return nil
	}
	cloned := make([]PublicKey, len(value))
	copy(cloned, value)
	return cloned
}
func cloneAddressTableLookups(value []MessageAddressTableLookup) []MessageAddressTableLookup {
	if value == nil {
		return nil
	}
	cloned := make([]MessageAddressTableLookup, len(value))
	for index, lookup := range value {
		cloned[index] = MessageAddressTableLookup{
			AccountKey:      lookup.AccountKey,
			WritableIndexes: cloneUint8Slice(lookup.WritableIndexes),
			ReadonlyIndexes: cloneUint8Slice(lookup.ReadonlyIndexes),
		}
	}
	return cloned
}
func cloneUint8Slice(value []uint8) []uint8 {
	if value == nil {
		return nil
	}
	cloned := make([]uint8, len(value))
	copy(cloned, value)
	return cloned
}
