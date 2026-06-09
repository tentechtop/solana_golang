package structure

import (
	"fmt"

	"solana_golang/utils"
)

const (
	MaxAccountsPerTransaction     = 256
	MaxInstructionsPerTransaction = 1024
	MaxInstructionAccounts        = 256
	MaxInstructionDataSize        = 64 * 1024
	DefaultTransactionTTLMillis   = 400
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

// MessageHeader 描述 Solana 消息头 + 用计数压缩账户签名和只读权限。
type MessageHeader struct {
	NumRequiredSignatures       uint8
	NumReadonlySignedAccounts   uint8
	NumReadonlyUnsignedAccounts uint8
}

// CompiledInstruction 描述 Solana 已编译指令 + 使用 u8 账户索引匹配链上格式。
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

// SolanaMessage 描述 Solana 可签名消息 + 作为交易签名和网络传输的核心结构。
type SolanaMessage struct {
	Header              MessageHeader
	AccountKeys         []PublicKey
	RecentBlockhash     Blockhash
	Instructions        []CompiledInstruction
	AddressTableLookups []MessageAddressTableLookup
	Version             uint8
	UsesAddressTable    bool
}

// PohRecord 描述交易关联 PoH 记录 + 为排序、过期和回放保护提供依据。
type PohRecord struct {
	Slot      uint64
	Hash      Hash
	Sequence  uint64
	Timestamp int64
}

// Transaction 描述链上交易 + 覆盖交易池、打包、签名和执行所需字段。
type Transaction struct {
	Signatures      []Signature
	Accounts        []AccountMeta
	Instructions    []CompiledInstruction
	RecentBlockhash Blockhash
	Message         SolanaMessage
	PohRecord       *PohRecord
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
	message, err := transaction.SolanaMessage()
	if err != nil {
		return err
	}
	if err := message.Validate(); err != nil {
		return fmt.Errorf("structure: validate solana message: %w", err)
	}
	return transaction.validateSignatureCount(message.Header.NumRequiredSignatures)
}

// TxID 计算交易 ID + 与原 Java 结构保持首签名 SHA-256 语义一致。
func (transaction Transaction) TxID() (Hash, error) {
	if len(transaction.Signatures) == 0 {
		return Hash{}, ErrEmptyTransactionSignatures
	}
	return NewHash(utils.SHA256(transaction.Signatures[0][:]))
}

// TxIDHex 返回交易 ID 十六进制 + 便于日志、索引和接口输出。
func (transaction Transaction) TxIDHex() (string, error) {
	transactionID, err := transaction.TxID()
	if err != nil {
		return "", err
	}
	return transactionID.Hex(), nil
}

// Hash 计算交易哈希 + 保持与区块 Merkle Root 输入统一。
func (transaction Transaction) Hash() (Hash, error) {
	return transaction.TxID()
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

// MarshalBinary 序列化 Solana 交易 + 只包含签名和 message 避免业务元数据污染链上格式。
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
	return encoded, nil
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
		PohRecord:       clonePohRecord(transaction.PohRecord),
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

// SolanaMessage 返回 Solana 消息 + 优先使用显式 Message 否则从账户元数据构建。
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

// Validate 校验 Solana 消息 + 防止账户索引、地址表和权限头越界。
func (message SolanaMessage) Validate() error {
	if message.UsesAddressTable && message.Version != 0 {
		return fmt.Errorf("%w: only solana v0 message is supported", ErrInvalidMessageVersion)
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

// MarshalBinary 序列化 Solana 消息 + 使用 short_vec 编码兼容官方交易格式。
func (message SolanaMessage) MarshalBinary() ([]byte, error) {
	if err := message.Validate(); err != nil {
		return nil, err
	}

	encoded := make([]byte, 0, estimateMessageSize(message))
	if message.UsesAddressTable {
		encoded = append(encoded, 0x80|message.Version)
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

// Clone 深拷贝 Solana 消息 + 避免交易结构跨协程共享切片。
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

func (message SolanaMessage) totalAccountCount() int {
	accountCount := len(message.AccountKeys)
	for _, lookup := range message.AddressTableLookups {
		accountCount += len(lookup.WritableIndexes) + len(lookup.ReadonlyIndexes)
	}
	return accountCount
}

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

func validateAccountOrder(accounts []AccountMeta) error {
	lastGroup := 0
	for accountIndex, account := range accounts {
		group := accountPermissionGroup(account)
		if group < lastGroup {
			return fmt.Errorf("%w: account %d order violates solana header layout", ErrInvalidAccountMeta, accountIndex)
		}
		lastGroup = group
	}
	return nil
}

func (transaction Transaction) validateInstructions() error {
	return validateCompiledInstructions(transaction.Instructions, len(transaction.Accounts))
}

func (transaction Transaction) validateSignatureCount(requiredSignatures uint8) error {
	if len(transaction.Signatures) != int(requiredSignatures) {
		return fmt.Errorf("structure: signatures count %d does not match required %d", len(transaction.Signatures), requiredSignatures)
	}
	return nil
}

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

func validateAddressTableLookups(lookups []MessageAddressTableLookup) error {
	for lookupIndex, lookup := range lookups {
		if lookup.AccountKey.IsZero() {
			return fmt.Errorf("%w: address table %d account key is empty", ErrInvalidAddressTableLookup, lookupIndex)
		}
	}
	return nil
}

func appendSignatures(encoded *[]byte, signatures []Signature) error {
	if err := appendShortVecLength(encoded, len(signatures)); err != nil {
		return err
	}
	for _, signature := range signatures {
		*encoded = append(*encoded, signature[:]...)
	}
	return nil
}

func appendPublicKeys(encoded *[]byte, publicKeys []PublicKey) error {
	if err := appendShortVecLength(encoded, len(publicKeys)); err != nil {
		return err
	}
	for _, publicKey := range publicKeys {
		*encoded = append(*encoded, publicKey[:]...)
	}
	return nil
}

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

func appendUint8Indexes(encoded *[]byte, indexes []uint8) error {
	if err := appendShortVecLength(encoded, len(indexes)); err != nil {
		return err
	}
	*encoded = append(*encoded, indexes...)
	return nil
}

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

func appendShortVecLength(encoded *[]byte, length int) error {
	lengthBytes, err := utils.EncodeShortVecLength(length)
	if err != nil {
		return err
	}
	*encoded = append(*encoded, lengthBytes...)
	return nil
}

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

func estimateAddressTableLookupSize(lookups []MessageAddressTableLookup) int {
	size := 3
	for _, lookup := range lookups {
		size += PublicKeySize + 3 + len(lookup.WritableIndexes) + 3 + len(lookup.ReadonlyIndexes)
	}
	return size
}

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

func accountPublicKeys(accounts []AccountMeta) []PublicKey {
	publicKeys := make([]PublicKey, len(accounts))
	for index, account := range accounts {
		publicKeys[index] = account.PublicKey
	}
	return publicKeys
}

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

func clonePohRecord(value *PohRecord) *PohRecord {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneUint8Slice(value []uint8) []uint8 {
	if value == nil {
		return nil
	}
	cloned := make([]uint8, len(value))
	copy(cloned, value)
	return cloned
}
