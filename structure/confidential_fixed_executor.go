package structure

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sync"

	"solana_golang/utils"
	"solana_golang/zk"
)

const domainConfidentialSpendV1 = "solana_golang.structure.confidential.spend.v1"

// ConfidentialInstructionExecutor 描述隐私固定指令接口 + 未来 VM 只需实现同一组方法。
type ConfidentialInstructionExecutor interface {
	ExecuteTransparentToTransparent(ledger *ConfidentialLedger, instruction ConfidentialTransparentTransferInstruction) error
	ExecuteTransparentToPrivate(ledger *ConfidentialLedger, instruction ConfidentialDepositInstruction) error
	ExecutePrivateToPrivate(ledger *ConfidentialLedger, instruction ConfidentialPrivateTransferInstruction) error
	ExecutePrivateToTransparent(ledger *ConfidentialLedger, instruction ConfidentialWithdrawInstruction) error
}

// FixedConfidentialInstructionExecutor 执行硬编码隐私指令 + 当前阶段不依赖 VM。
type FixedConfidentialInstructionExecutor struct{}

// ConfidentialLedger 描述最小隐私账本 + 只保存公开余额、隐私资金池、密文 note 和 nullifier。
type ConfidentialLedger struct {
	mutex               sync.Mutex
	TransparentBalances map[PublicKey]uint64
	PrivacyPoolLamports uint64
	Notes               []ConfidentialNote
	SpentNullifiers     map[Hash]struct{}
}

// ConfidentialNote 描述隐私 note 状态 + 不存储明文金额只保存承诺和 ElGamal 密文。
type ConfidentialNote struct {
	Commitment       []byte
	SpendAuthority   PublicKey
	AmountPublicKey  []byte
	AmountCiphertext zk.ElGamalCiphertext
	AmountProof      zk.AmountCiphertextProof
	Spent            bool
	SpendNullifier   Hash
	AuditRecords     []ConfidentialAuditRecord
}

// ConfidentialOutputNote 描述新建隐私 note + 携带范围证明和审计授权供固定指令校验。
type ConfidentialOutputNote struct {
	Commitment       []byte
	SpendAuthority   PublicKey
	AmountPublicKey  []byte
	AmountCiphertext zk.ElGamalCiphertext
	AmountProof      zk.AmountCiphertextProof
	RangeProof       zk.RangeProof
	AuditRecords     []ConfidentialAuditRecord
}

// ConfidentialAuditRecord 描述授权审计记录 + 公开元数据配合 ElGamal 密文支持门限审计。
type ConfidentialAuditRecord struct {
	Auditor          PublicKey
	Scope            PrivacyAuditScope
	ExpiresAtSlot    uint64
	TransactionType  PrivacyInstructionType
	Commitment       []byte
	Nullifier        Hash
	OutputCommitment []byte
	AuditPublicKey   []byte
	AmountCiphertext zk.ElGamalCiphertext
	AmountProof      zk.AmountCiphertextProof
}

// ConfidentialAuditPayload 描述审计解密结果 + 审计方用门限私钥解出金额后得到完整事实。
type ConfidentialAuditPayload struct {
	TransactionType  PrivacyInstructionType
	Commitment       []byte
	Nullifier        Hash
	OutputCommitment []byte
	Amount           uint64
}

// ConfidentialTransparentTransferInstruction 描述透明转透明 + 与系统转账语义一致。
type ConfidentialTransparentTransferInstruction struct {
	Source      PublicKey
	Destination PublicKey
	Amount      uint64
}

// ConfidentialDepositInstruction 描述透明转隐私 + 用公开金额绑定证明防止铸错 note。
type ConfidentialDepositInstruction struct {
	Source      PublicKey
	Amount      uint64
	Output      ConfidentialOutputNote
	AmountProof zk.BalanceProof
}

// ConfidentialPrivateTransferInstruction 描述隐私转隐私 + 用守恒证明校验输入输出金额一致。
type ConfidentialPrivateTransferInstruction struct {
	SourceCommitment []byte
	Nullifier        Hash
	Output           ConfidentialOutputNote
	BalanceProof     zk.BalanceProof
	SpendProof       []byte
}

// ConfidentialWithdrawInstruction 描述隐私转透明 + 用守恒证明校验隐私输入等于公开提现金额。
type ConfidentialWithdrawInstruction struct {
	SourceCommitment []byte
	Nullifier        Hash
	Destination      PublicKey
	Amount           uint64
	BalanceProof     zk.BalanceProof
	SpendProof       []byte
	AuditRecords     []ConfidentialAuditRecord
}

// NewConfidentialLedger 创建最小隐私账本 + 克隆初始透明余额避免外部修改。
func NewConfidentialLedger(balances map[PublicKey]uint64) *ConfidentialLedger {
	clonedBalances := make(map[PublicKey]uint64, len(balances))
	for address, balance := range balances {
		clonedBalances[address] = balance
	}
	return &ConfidentialLedger{
		TransparentBalances: clonedBalances,
		SpentNullifiers:     make(map[Hash]struct{}),
	}
}

// TransparentBalance 查询透明余额 + 加锁读取避免并发读写竞态。
func (ledger *ConfidentialLedger) TransparentBalance(address PublicKey) (uint64, error) {
	unlock, err := lockConfidentialLedger(ledger)
	if err != nil {
		return 0, err
	}
	defer unlock()
	return ledger.TransparentBalances[address], nil
}

// PrivacyPoolBalance 查询隐私资金池余额 + 用于测试和审计核对资金守恒。
func (ledger *ConfidentialLedger) PrivacyPoolBalance() (uint64, error) {
	unlock, err := lockConfidentialLedger(ledger)
	if err != nil {
		return 0, err
	}
	defer unlock()
	return ledger.PrivacyPoolLamports, nil
}

// NotesSnapshot 返回 note 快照 + 拷贝切片防止测试或调用方绕过执行器改状态。
func (ledger *ConfidentialLedger) NotesSnapshot() ([]ConfidentialNote, error) {
	unlock, err := lockConfidentialLedger(ledger)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return cloneConfidentialNotes(ledger.Notes), nil
}

// ExecuteTransparentToTransparent 执行透明转透明 + 只改变透明账户余额。
func (executor FixedConfidentialInstructionExecutor) ExecuteTransparentToTransparent(ledger *ConfidentialLedger, instruction ConfidentialTransparentTransferInstruction) error {
	if err := instruction.validate(); err != nil {
		return err
	}
	unlock, err := lockConfidentialLedger(ledger)
	if err != nil {
		return err
	}
	defer unlock()
	return moveTransparentLamports(ledger, instruction.Source, instruction.Destination, instruction.Amount)
}

// ExecuteTransparentToPrivate 执行透明转隐私 + 验证范围、金额绑定和审计授权后创建 note。
func (executor FixedConfidentialInstructionExecutor) ExecuteTransparentToPrivate(ledger *ConfidentialLedger, instruction ConfidentialDepositInstruction) error {
	if err := instruction.validate(); err != nil {
		return err
	}
	unlock, err := lockConfidentialLedger(ledger)
	if err != nil {
		return err
	}
	defer unlock()
	if hasConfidentialCommitment(ledger.Notes, instruction.Output.Commitment) {
		return fmt.Errorf("%w: duplicate confidential commitment", ErrInvalidPrivacyInstruction)
	}
	if err := debitTransparentToPool(ledger, instruction.Source, instruction.Amount); err != nil {
		return err
	}
	ledger.Notes = append(ledger.Notes, confidentialNoteFromOutput(instruction.Output))
	return nil
}

// ExecutePrivateToPrivate 执行隐私转隐私 + 消耗旧 note 并创建新密文 note。
func (executor FixedConfidentialInstructionExecutor) ExecutePrivateToPrivate(ledger *ConfidentialLedger, instruction ConfidentialPrivateTransferInstruction) error {
	if err := instruction.validate(); err != nil {
		return err
	}
	unlock, err := lockConfidentialLedger(ledger)
	if err != nil {
		return err
	}
	defer unlock()
	sourceIndex, err := spendableNoteIndex(ledger, instruction.SourceCommitment, instruction.Nullifier)
	if err != nil {
		return err
	}
	if hasConfidentialCommitment(ledger.Notes, instruction.Output.Commitment) {
		return fmt.Errorf("%w: duplicate confidential commitment", ErrInvalidPrivacyInstruction)
	}
	if err := zk.VerifyBalanceProof([][]byte{instruction.SourceCommitment}, [][]byte{instruction.Output.Commitment}, 0, instruction.BalanceProof); err != nil {
		return fmt.Errorf("%w: verify private transfer balance: %w", ErrInvalidPrivacyInstruction, err)
	}
	spendMessage := BuildConfidentialPrivateTransferSpendMessage(instruction.SourceCommitment, instruction.Nullifier, instruction.Output.Commitment)
	if err := verifyConfidentialSpendProof(ledger.Notes[sourceIndex], spendMessage, instruction.SpendProof); err != nil {
		return err
	}
	consumeConfidentialNote(ledger, sourceIndex, instruction.Nullifier)
	ledger.Notes = append(ledger.Notes, confidentialNoteFromOutput(instruction.Output))
	return nil
}

// ExecutePrivateToTransparent 执行隐私转透明 + 消耗 note 并从隐私资金池释放公开金额。
func (executor FixedConfidentialInstructionExecutor) ExecutePrivateToTransparent(ledger *ConfidentialLedger, instruction ConfidentialWithdrawInstruction) error {
	if err := instruction.validate(); err != nil {
		return err
	}
	unlock, err := lockConfidentialLedger(ledger)
	if err != nil {
		return err
	}
	defer unlock()
	sourceIndex, err := spendableNoteIndex(ledger, instruction.SourceCommitment, instruction.Nullifier)
	if err != nil {
		return err
	}
	if err := zk.VerifyBalanceProof([][]byte{instruction.SourceCommitment}, nil, instruction.Amount, instruction.BalanceProof); err != nil {
		return fmt.Errorf("%w: verify private withdraw balance: %w", ErrInvalidPrivacyInstruction, err)
	}
	spendMessage := BuildConfidentialWithdrawSpendMessage(instruction.SourceCommitment, instruction.Nullifier, instruction.Destination, instruction.Amount)
	if err := verifyConfidentialSpendProof(ledger.Notes[sourceIndex], spendMessage, instruction.SpendProof); err != nil {
		return err
	}
	if err := ensureConfidentialAuditAppendCapacity(ledger, sourceIndex, len(instruction.AuditRecords)); err != nil {
		return err
	}
	if err := releasePrivatePoolLamports(ledger, instruction.Destination, instruction.Amount); err != nil {
		return err
	}
	appendConfidentialAuditRecords(ledger, sourceIndex, instruction.AuditRecords)
	consumeConfidentialNote(ledger, sourceIndex, instruction.Nullifier)
	return nil
}

// AuditConfidentialNote 解密授权审计记录 + 使用 TSS 份额恢复监管私钥后解密 ElGamal 金额。
func AuditConfidentialNote(note ConfidentialNote, auditor PublicKey, scope PrivacyAuditScope, currentSlot uint64, shares []zk.ThresholdShare, threshold int, maxAmount uint64) ([]ConfidentialAuditPayload, error) {
	if auditor.IsZero() {
		return nil, fmt.Errorf("%w: audit auditor is zero", ErrInvalidPrivacyInstruction)
	}
	if !scope.IsValid() {
		return nil, fmt.Errorf("%w: invalid audit scope %d", ErrInvalidPrivacyInstruction, scope)
	}
	privateScalar, err := zk.RecoverScalar(shares, threshold)
	if err != nil {
		return nil, fmt.Errorf("%w: recover audit threshold key: %w", ErrInvalidPrivacyInstruction, err)
	}
	return decryptConfidentialAuditRecords(note.AuditRecords, auditor, scope, currentSlot, privateScalar, maxAmount)
}

// BuildConfidentialPrivateTransferSpendMessage 构造隐私转隐私花费消息 + 将 proof 绑定到输出承诺和 nullifier。
func BuildConfidentialPrivateTransferSpendMessage(sourceCommitment []byte, nullifier Hash, outputCommitment []byte) []byte {
	buffer := bytes.Buffer{}
	buffer.WriteString(domainConfidentialSpendV1)
	writeConfidentialLengthPrefixedBytes(&buffer, []byte{byte(PrivacyInstructionTransfer)})
	writeConfidentialLengthPrefixedBytes(&buffer, sourceCommitment)
	writeConfidentialLengthPrefixedBytes(&buffer, nullifier[:])
	writeConfidentialLengthPrefixedBytes(&buffer, outputCommitment)
	return buffer.Bytes()
}

// BuildConfidentialWithdrawSpendMessage 构造隐私转透明花费消息 + 将 proof 绑定到收款方和公开金额。
func BuildConfidentialWithdrawSpendMessage(sourceCommitment []byte, nullifier Hash, destination PublicKey, amount uint64) []byte {
	buffer := bytes.Buffer{}
	buffer.WriteString(domainConfidentialSpendV1)
	writeConfidentialLengthPrefixedBytes(&buffer, []byte{byte(PrivacyInstructionWithdraw)})
	writeConfidentialLengthPrefixedBytes(&buffer, sourceCommitment)
	writeConfidentialLengthPrefixedBytes(&buffer, nullifier[:])
	writeConfidentialLengthPrefixedBytes(&buffer, destination[:])
	writeConfidentialUint64(&buffer, amount)
	return buffer.Bytes()
}

func (instruction ConfidentialTransparentTransferInstruction) validate() error {
	if instruction.Source.IsZero() || instruction.Destination.IsZero() {
		return fmt.Errorf("%w: transparent account is zero", ErrInvalidPrivacyInstruction)
	}
	if instruction.Amount == 0 {
		return fmt.Errorf("%w: transparent amount cannot be zero", ErrInvalidPrivacyInstruction)
	}
	return nil
}

func (instruction ConfidentialDepositInstruction) validate() error {
	if instruction.Source.IsZero() {
		return fmt.Errorf("%w: deposit source is zero", ErrInvalidPrivacyInstruction)
	}
	if instruction.Amount == 0 || instruction.Amount > zk.MaxConfidentialAmount {
		return fmt.Errorf("%w: invalid deposit amount %d", ErrInvalidPrivacyInstruction, instruction.Amount)
	}
	if err := validateConfidentialOutputNote(instruction.Output, PrivacyInstructionDeposit); err != nil {
		return err
	}
	if err := validateConfidentialAuditLinks(instruction.Output.AuditRecords, instruction.Output.Commitment, Hash{}, nil); err != nil {
		return err
	}
	return zk.VerifyCommitmentAmountProof(instruction.Output.Commitment, instruction.Amount, instruction.AmountProof)
}

func (instruction ConfidentialPrivateTransferInstruction) validate() error {
	if len(instruction.SourceCommitment) == 0 || instruction.Nullifier.IsZero() {
		return fmt.Errorf("%w: private transfer source or nullifier is empty", ErrInvalidPrivacyInstruction)
	}
	if len(instruction.SpendProof) == 0 || len(instruction.SpendProof) > zk.SchnorrProofMaxBytes {
		return fmt.Errorf("%w: invalid private transfer spend proof", ErrInvalidPrivacyInstruction)
	}
	if err := validateConfidentialOutputNote(instruction.Output, PrivacyInstructionTransfer); err != nil {
		return err
	}
	return validateConfidentialAuditLinks(instruction.Output.AuditRecords, instruction.SourceCommitment, instruction.Nullifier, instruction.Output.Commitment)
}

func (instruction ConfidentialWithdrawInstruction) validate() error {
	if len(instruction.SourceCommitment) == 0 || instruction.Nullifier.IsZero() || instruction.Destination.IsZero() {
		return fmt.Errorf("%w: withdraw source, nullifier or destination is empty", ErrInvalidPrivacyInstruction)
	}
	if instruction.Amount == 0 || instruction.Amount > zk.MaxConfidentialAmount {
		return fmt.Errorf("%w: invalid withdraw amount %d", ErrInvalidPrivacyInstruction, instruction.Amount)
	}
	if len(instruction.SpendProof) == 0 || len(instruction.SpendProof) > zk.SchnorrProofMaxBytes {
		return fmt.Errorf("%w: invalid withdraw spend proof", ErrInvalidPrivacyInstruction)
	}
	if err := validateConfidentialAuditRecords(instruction.AuditRecords, PrivacyInstructionWithdraw); err != nil {
		return err
	}
	return validateConfidentialAuditLinks(instruction.AuditRecords, instruction.SourceCommitment, instruction.Nullifier, nil)
}

func validateConfidentialOutputNote(output ConfidentialOutputNote, transactionType PrivacyInstructionType) error {
	if len(output.Commitment) == 0 || output.SpendAuthority.IsZero() {
		return fmt.Errorf("%w: confidential output is incomplete", ErrInvalidPrivacyInstruction)
	}
	if err := zk.VerifyAmountCiphertextProof(output.AmountPublicKey, output.Commitment, output.AmountCiphertext, output.AmountProof); err != nil {
		return fmt.Errorf("%w: output amount ciphertext proof: %w", ErrInvalidPrivacyInstruction, err)
	}
	if err := output.RangeProof.Verify(); err != nil {
		return fmt.Errorf("%w: output range proof: %w", ErrInvalidPrivacyInstruction, err)
	}
	if !bytes.Equal(output.RangeProof.Commitment, output.Commitment) {
		return fmt.Errorf("%w: output range commitment mismatch", ErrInvalidPrivacyInstruction)
	}
	return validateConfidentialAuditRecords(output.AuditRecords, transactionType)
}

func validateConfidentialAuditRecords(records []ConfidentialAuditRecord, transactionType PrivacyInstructionType) error {
	if len(records) == 0 {
		return fmt.Errorf("%w: privacy transaction requires audit record", ErrInvalidPrivacyInstruction)
	}
	if len(records) > MaxPrivacyAuditRecordsPerNote {
		return fmt.Errorf("%w: audit record count %d exceeds %d", ErrInvalidPrivacyInstruction, len(records), MaxPrivacyAuditRecordsPerNote)
	}
	for index, record := range records {
		if err := validateConfidentialAuditRecord(record, transactionType); err != nil {
			return fmt.Errorf("structure: confidential audit record %d: %w", index, err)
		}
	}
	return nil
}

func validateConfidentialAuditRecord(record ConfidentialAuditRecord, transactionType PrivacyInstructionType) error {
	if record.TransactionType != transactionType {
		return fmt.Errorf("%w: audit transaction type mismatch", ErrInvalidPrivacyInstruction)
	}
	if record.Auditor.IsZero() || !record.Scope.IsValid() || len(record.Commitment) == 0 {
		return fmt.Errorf("%w: audit authorization is incomplete", ErrInvalidPrivacyInstruction)
	}
	if err := zk.VerifyAmountCiphertextProof(record.AuditPublicKey, record.Commitment, record.AmountCiphertext, record.AmountProof); err != nil {
		return fmt.Errorf("%w: audit amount ciphertext proof: %w", ErrInvalidPrivacyInstruction, err)
	}
	if transactionType == PrivacyInstructionDeposit {
		return nil
	}
	if record.Nullifier.IsZero() {
		return fmt.Errorf("%w: audit nullifier is zero", ErrInvalidPrivacyInstruction)
	}
	if transactionType == PrivacyInstructionTransfer && len(record.OutputCommitment) == 0 {
		return fmt.Errorf("%w: audit output commitment is empty", ErrInvalidPrivacyInstruction)
	}
	return nil
}

func validateConfidentialAuditLinks(records []ConfidentialAuditRecord, commitment []byte, nullifier Hash, outputCommitment []byte) error {
	for index, record := range records {
		if !bytes.Equal(record.Commitment, commitment) {
			return fmt.Errorf("%w: audit record %d commitment mismatch", ErrInvalidPrivacyInstruction, index)
		}
		if nullifier != (Hash{}) && record.Nullifier != nullifier {
			return fmt.Errorf("%w: audit record %d nullifier mismatch", ErrInvalidPrivacyInstruction, index)
		}
		if outputCommitment != nil && !bytes.Equal(record.OutputCommitment, outputCommitment) {
			return fmt.Errorf("%w: audit record %d output commitment mismatch", ErrInvalidPrivacyInstruction, index)
		}
	}
	return nil
}

func lockConfidentialLedger(ledger *ConfidentialLedger) (func(), error) {
	if ledger == nil {
		return nil, fmt.Errorf("%w: confidential ledger is nil", ErrInvalidPrivacyInstruction)
	}
	ledger.mutex.Lock()
	ensureConfidentialLedgerMaps(ledger)
	return ledger.mutex.Unlock, nil
}

func ensureConfidentialLedgerMaps(ledger *ConfidentialLedger) {
	if ledger.TransparentBalances == nil {
		ledger.TransparentBalances = make(map[PublicKey]uint64)
	}
	if ledger.SpentNullifiers == nil {
		ledger.SpentNullifiers = make(map[Hash]struct{})
	}
}

func moveTransparentLamports(ledger *ConfidentialLedger, source PublicKey, destination PublicKey, amount uint64) error {
	if ledger.TransparentBalances[source] < amount {
		return fmt.Errorf("%w: transparent balance too low", ErrInsufficientLamports)
	}
	nextDestinationBalance, err := safeAddUint64(ledger.TransparentBalances[destination], amount)
	if err != nil {
		return fmt.Errorf("structure: credit transparent destination: %w", err)
	}
	ledger.TransparentBalances[source] -= amount
	ledger.TransparentBalances[destination] = nextDestinationBalance
	return nil
}

func debitTransparentToPool(ledger *ConfidentialLedger, source PublicKey, amount uint64) error {
	if ledger.TransparentBalances[source] < amount {
		return fmt.Errorf("%w: transparent balance too low", ErrInsufficientLamports)
	}
	nextPoolBalance, err := safeAddUint64(ledger.PrivacyPoolLamports, amount)
	if err != nil {
		return fmt.Errorf("structure: credit privacy pool: %w", err)
	}
	ledger.TransparentBalances[source] -= amount
	ledger.PrivacyPoolLamports = nextPoolBalance
	return nil
}

func releasePrivatePoolLamports(ledger *ConfidentialLedger, destination PublicKey, amount uint64) error {
	if ledger.PrivacyPoolLamports < amount {
		return fmt.Errorf("%w: privacy pool balance too low", ErrInsufficientLamports)
	}
	nextDestinationBalance, err := safeAddUint64(ledger.TransparentBalances[destination], amount)
	if err != nil {
		return fmt.Errorf("structure: credit withdraw destination: %w", err)
	}
	ledger.PrivacyPoolLamports -= amount
	ledger.TransparentBalances[destination] = nextDestinationBalance
	return nil
}

func spendableNoteIndex(ledger *ConfidentialLedger, commitment []byte, nullifier Hash) (int, error) {
	if _, exists := ledger.SpentNullifiers[nullifier]; exists {
		return -1, fmt.Errorf("%w: nullifier already spent", ErrInvalidPrivacyInstruction)
	}
	for index, note := range ledger.Notes {
		if !bytes.Equal(note.Commitment, commitment) {
			continue
		}
		if note.Spent {
			return -1, fmt.Errorf("%w: confidential note already spent", ErrInvalidPrivacyInstruction)
		}
		return index, nil
	}
	return -1, fmt.Errorf("%w: confidential note not found", ErrInvalidPrivacyInstruction)
}

func consumeConfidentialNote(ledger *ConfidentialLedger, noteIndex int, nullifier Hash) {
	ledger.Notes[noteIndex].Spent = true
	ledger.Notes[noteIndex].SpendNullifier = nullifier
	ledger.SpentNullifiers[nullifier] = struct{}{}
}

func verifyConfidentialSpendProof(note ConfidentialNote, message []byte, proof []byte) error {
	var expectedDigest zk.Digest
	copy(expectedDigest[:], note.SpendAuthority[:])
	if err := zk.VerifySchnorrProofBytes(proof, message, expectedDigest); err != nil {
		return fmt.Errorf("%w: verify confidential spend proof: %w", ErrInvalidPrivacyInstruction, err)
	}
	return nil
}

func hasConfidentialCommitment(notes []ConfidentialNote, commitment []byte) bool {
	for _, note := range notes {
		if bytes.Equal(note.Commitment, commitment) {
			return true
		}
	}
	return false
}

func writeConfidentialLengthPrefixedBytes(buffer *bytes.Buffer, value []byte) {
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], uint32(len(value)))
	buffer.Write(encoded[:])
	buffer.Write(value)
}

func writeConfidentialUint64(buffer *bytes.Buffer, value uint64) {
	var encoded [8]byte
	binary.LittleEndian.PutUint64(encoded[:], value)
	buffer.Write(encoded[:])
}

func ensureConfidentialAuditAppendCapacity(ledger *ConfidentialLedger, noteIndex int, recordCount int) error {
	nextCount := len(ledger.Notes[noteIndex].AuditRecords) + recordCount
	if nextCount > MaxPrivacyAuditRecordsPerNote {
		return fmt.Errorf("%w: audit record count exceeds %d", ErrInvalidPrivacyInstruction, MaxPrivacyAuditRecordsPerNote)
	}
	return nil
}

func appendConfidentialAuditRecords(ledger *ConfidentialLedger, noteIndex int, records []ConfidentialAuditRecord) {
	clonedRecords := cloneConfidentialAuditRecords(records)
	ledger.Notes[noteIndex].AuditRecords = append(ledger.Notes[noteIndex].AuditRecords, clonedRecords...)
}

func confidentialNoteFromOutput(output ConfidentialOutputNote) ConfidentialNote {
	return ConfidentialNote{
		Commitment:       utils.CloneBytes(output.Commitment),
		SpendAuthority:   output.SpendAuthority,
		AmountPublicKey:  utils.CloneBytes(output.AmountPublicKey),
		AmountCiphertext: cloneElGamalCiphertext(output.AmountCiphertext),
		AmountProof:      cloneAmountCiphertextProof(output.AmountProof),
		AuditRecords:     cloneConfidentialAuditRecords(output.AuditRecords),
	}
}

func decryptConfidentialAuditRecords(records []ConfidentialAuditRecord, auditor PublicKey, scope PrivacyAuditScope, currentSlot uint64, privateScalar []byte, maxAmount uint64) ([]ConfidentialAuditPayload, error) {
	payloads := make([]ConfidentialAuditPayload, 0, len(records))
	for _, record := range records {
		if !recordMatchesAudit(record, auditor, scope, currentSlot) {
			continue
		}
		if err := zk.VerifyAmountCiphertextProof(record.AuditPublicKey, record.Commitment, record.AmountCiphertext, record.AmountProof); err != nil {
			return nil, fmt.Errorf("%w: verify confidential audit amount proof: %w", ErrInvalidPrivacyInstruction, err)
		}
		amount, err := zk.DecryptAmount(privateScalar, record.AmountCiphertext, maxAmount)
		if err != nil {
			return nil, fmt.Errorf("%w: decrypt confidential audit amount: %w", ErrInvalidPrivacyInstruction, err)
		}
		payloads = append(payloads, confidentialAuditPayload(record, amount))
	}
	if len(payloads) == 0 {
		return nil, fmt.Errorf("%w: no active confidential audit record", ErrInvalidPrivacyInstruction)
	}
	return payloads, nil
}

func recordMatchesAudit(record ConfidentialAuditRecord, auditor PublicKey, scope PrivacyAuditScope, currentSlot uint64) bool {
	if record.Auditor != auditor || record.Scope != scope {
		return false
	}
	return record.ExpiresAtSlot == 0 || record.ExpiresAtSlot > currentSlot
}

func confidentialAuditPayload(record ConfidentialAuditRecord, amount uint64) ConfidentialAuditPayload {
	return ConfidentialAuditPayload{
		TransactionType:  record.TransactionType,
		Commitment:       utils.CloneBytes(record.Commitment),
		Nullifier:        record.Nullifier,
		OutputCommitment: utils.CloneBytes(record.OutputCommitment),
		Amount:           amount,
	}
}

func cloneConfidentialNotes(notes []ConfidentialNote) []ConfidentialNote {
	cloned := make([]ConfidentialNote, len(notes))
	for index, note := range notes {
		cloned[index] = note.Clone()
	}
	return cloned
}

func cloneConfidentialAuditRecords(records []ConfidentialAuditRecord) []ConfidentialAuditRecord {
	cloned := make([]ConfidentialAuditRecord, len(records))
	for index, record := range records {
		cloned[index] = cloneConfidentialAuditRecord(record)
	}
	return cloned
}

func cloneConfidentialAuditRecord(record ConfidentialAuditRecord) ConfidentialAuditRecord {
	return ConfidentialAuditRecord{
		Auditor:          record.Auditor,
		Scope:            record.Scope,
		ExpiresAtSlot:    record.ExpiresAtSlot,
		TransactionType:  record.TransactionType,
		Commitment:       utils.CloneBytes(record.Commitment),
		Nullifier:        record.Nullifier,
		OutputCommitment: utils.CloneBytes(record.OutputCommitment),
		AuditPublicKey:   utils.CloneBytes(record.AuditPublicKey),
		AmountCiphertext: cloneElGamalCiphertext(record.AmountCiphertext),
		AmountProof:      cloneAmountCiphertextProof(record.AmountProof),
	}
}

func cloneElGamalCiphertext(ciphertext zk.ElGamalCiphertext) zk.ElGamalCiphertext {
	return zk.ElGamalCiphertext{
		NonceCommitment: utils.CloneBytes(ciphertext.NonceCommitment),
		CiphertextPoint: utils.CloneBytes(ciphertext.CiphertextPoint),
	}
}

func cloneAmountCiphertextProof(proof zk.AmountCiphertextProof) zk.AmountCiphertextProof {
	return zk.AmountCiphertextProof{
		Version:            proof.Version,
		CommitmentNonce:    utils.CloneBytes(proof.CommitmentNonce),
		RandomnessNonce:    utils.CloneBytes(proof.RandomnessNonce),
		CiphertextNonce:    utils.CloneBytes(proof.CiphertextNonce),
		AmountResponse:     utils.CloneBytes(proof.AmountResponse),
		BlindingResponse:   utils.CloneBytes(proof.BlindingResponse),
		RandomnessResponse: utils.CloneBytes(proof.RandomnessResponse),
	}
}

// Clone 深拷贝隐私 note + 防止外部修改密文和审计记录。
func (note ConfidentialNote) Clone() ConfidentialNote {
	return ConfidentialNote{
		Commitment:       utils.CloneBytes(note.Commitment),
		SpendAuthority:   note.SpendAuthority,
		AmountPublicKey:  utils.CloneBytes(note.AmountPublicKey),
		AmountCiphertext: cloneElGamalCiphertext(note.AmountCiphertext),
		AmountProof:      cloneAmountCiphertextProof(note.AmountProof),
		Spent:            note.Spent,
		SpendNullifier:   note.SpendNullifier,
		AuditRecords:     cloneConfidentialAuditRecords(note.AuditRecords),
	}
}
