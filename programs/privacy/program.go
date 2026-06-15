package privacy

import (
	"fmt"

	"solana_golang/runtime"
	"solana_golang/structure"
	"solana_golang/utils"
	"solana_golang/zk"
)

// Program 执行隐私固定指令 + 将隐私状态转换移出 structure。
type Program struct {
	programID structure.PublicKey
}

// NewProgram 创建隐私程序 + 由组合层传入链配置中的隐私程序 ID。
func NewProgram(programID structure.PublicKey) Program {
	return Program{programID: programID}
}

// ProgramID 返回隐私程序 ID + 供 runtime 注册表分发。
func (program Program) ProgramID() structure.PublicKey {
	return program.programID
}

// Execute 执行隐私指令 + 按类型路由到固定状态转换。
func (program Program) Execute(context runtime.InstructionContext) error {
	instruction, err := structure.UnmarshalPrivacyInstructionBinary(context.Instruction.Data)
	if err != nil {
		return err
	}
	switch instruction.Type {
	case structure.PrivacyInstructionDeposit:
		return executeDeposit(program.programID, instruction, context)
	case structure.PrivacyInstructionWithdraw:
		return executeWithdraw(program.programID, instruction, context)
	case structure.PrivacyInstructionTransfer:
		return executeTransfer(program.programID, instruction, context)
	case structure.PrivacyInstructionAuthorizeAudit:
		return executeAuthorizeAudit(program.programID, instruction, context)
	default:
		return fmt.Errorf("%w: unsupported privacy type %d", structure.ErrInvalidPrivacyInstruction, instruction.Type)
	}
}

func executeDeposit(programID structure.PublicKey, instruction structure.PrivacyInstruction, context runtime.InstructionContext) error {
	if len(context.Instruction.AccountIndexes) < 2 {
		return fmt.Errorf("%w: deposit requires source and privacy state", structure.ErrInvalidPrivacyInstruction)
	}
	sourceAddress := context.Message.AccountKeys[context.Instruction.AccountIndexes[0]]
	stateAddress := context.Message.AccountKeys[context.Instruction.AccountIndexes[1]]
	if !runtime.IsSignerAddress(sourceAddress, context.Message) {
		return fmt.Errorf("%w: privacy deposit source must sign", structure.ErrMissingRequiredSignature)
	}
	state, stateAccount, err := loadStateAccount(programID, stateAddress, context.Accounts)
	if err != nil {
		return err
	}
	if findNote(state, instruction.Deposit.Commitment) >= 0 {
		return fmt.Errorf("%w: duplicate privacy commitment", structure.ErrInvalidPrivacyInstruction)
	}
	if err := validateAuditRecordsForSlot(instruction.Deposit.AuditRecords, context.CurrentSlot); err != nil {
		return err
	}
	if err := runtime.TransferLamports(sourceAddress, stateAddress, instruction.Deposit.Amount, context.Accounts, context.RentConfig); err != nil {
		return err
	}

	stateAccount = context.Accounts[stateAddress].Clone()
	state.Notes = append(state.Notes, structure.PrivacyNoteRecord{
		Commitment:     instruction.Deposit.Commitment,
		SpendAuthority: instruction.Deposit.SpendAuthority,
		Amount:         instruction.Deposit.Amount,
		VMVersion:      instruction.VMVersion,
		EncryptedNote:  utils.CloneBytes(instruction.Deposit.EncryptedNote),
		AuditRecords:   cloneAuditRecords(instruction.Deposit.AuditRecords),
	})
	return storeStateAccount(stateAddress, stateAccount, state, context)
}

func executeWithdraw(programID structure.PublicKey, instruction structure.PrivacyInstruction, context runtime.InstructionContext) error {
	if len(context.Instruction.AccountIndexes) < 2 {
		return fmt.Errorf("%w: withdraw requires privacy state and destination", structure.ErrInvalidPrivacyInstruction)
	}
	stateAddress := context.Message.AccountKeys[context.Instruction.AccountIndexes[0]]
	destinationAddress := context.Message.AccountKeys[context.Instruction.AccountIndexes[1]]
	state, stateAccount, err := loadStateAccount(programID, stateAddress, context.Accounts)
	if err != nil {
		return err
	}
	if err := validateAuditRecordsForSlot(instruction.Withdraw.AuditRecords, context.CurrentSlot); err != nil {
		return err
	}
	proofMessage, err := structure.BuildPrivacyWithdrawProofMessage(instruction.VMVersion, stateAddress, destinationAddress, *instruction.Withdraw, context.CurrentSlot)
	if err != nil {
		return err
	}
	if err := consumeNote(&state, instruction.Withdraw.SourceCommitment, instruction.Withdraw.Nullifier, instruction.Withdraw.Amount, context, instruction.Proof, proofMessage); err != nil {
		return err
	}
	if err := appendAuditRecords(&state, instruction.Withdraw.SourceCommitment, instruction.Withdraw.AuditRecords); err != nil {
		return err
	}
	if err := storeStateAccount(stateAddress, stateAccount, state, context); err != nil {
		return err
	}
	return runtime.TransferLamports(stateAddress, destinationAddress, instruction.Withdraw.Amount, context.Accounts, context.RentConfig)
}

func executeTransfer(programID structure.PublicKey, instruction structure.PrivacyInstruction, context runtime.InstructionContext) error {
	if len(context.Instruction.AccountIndexes) < 1 {
		return fmt.Errorf("%w: private transfer requires privacy state", structure.ErrInvalidPrivacyInstruction)
	}
	stateAddress := context.Message.AccountKeys[context.Instruction.AccountIndexes[0]]
	state, stateAccount, err := loadStateAccount(programID, stateAddress, context.Accounts)
	if err != nil {
		return err
	}
	if findNote(state, instruction.Transfer.OutputCommitment) >= 0 {
		return fmt.Errorf("%w: duplicate output commitment", structure.ErrInvalidPrivacyInstruction)
	}
	if err := validateAuditRecordsForSlot(instruction.Transfer.OutputAuditRecords, context.CurrentSlot); err != nil {
		return err
	}
	proofMessage, err := structure.BuildPrivacyTransferProofMessage(instruction.VMVersion, stateAddress, *instruction.Transfer, context.CurrentSlot)
	if err != nil {
		return err
	}
	if err := consumeNote(&state, instruction.Transfer.SourceCommitment, instruction.Transfer.Nullifier, instruction.Transfer.Amount, context, instruction.Proof, proofMessage); err != nil {
		return err
	}
	state.Notes = append(state.Notes, structure.PrivacyNoteRecord{
		Commitment:     instruction.Transfer.OutputCommitment,
		SpendAuthority: instruction.Transfer.OutputSpendAuthority,
		Amount:         instruction.Transfer.Amount,
		VMVersion:      instruction.VMVersion,
		EncryptedNote:  utils.CloneBytes(instruction.Transfer.OutputEncryptedNote),
		AuditRecords:   cloneAuditRecords(instruction.Transfer.OutputAuditRecords),
	})
	return storeStateAccount(stateAddress, stateAccount, state, context)
}

func executeAuthorizeAudit(programID structure.PublicKey, instruction structure.PrivacyInstruction, context runtime.InstructionContext) error {
	if len(context.Instruction.AccountIndexes) < 1 {
		return fmt.Errorf("%w: authorize audit requires privacy state", structure.ErrInvalidPrivacyInstruction)
	}
	stateAddress := context.Message.AccountKeys[context.Instruction.AccountIndexes[0]]
	state, stateAccount, err := loadStateAccount(programID, stateAddress, context.Accounts)
	if err != nil {
		return err
	}
	auditRecord := structure.PrivacyAuditRecord{
		Auditor:         instruction.AuthorizeAudit.Auditor,
		Scope:           instruction.AuthorizeAudit.Scope,
		ExpiresAtSlot:   instruction.AuthorizeAudit.ExpiresAtSlot,
		AuditCiphertext: utils.CloneBytes(instruction.AuthorizeAudit.AuditCiphertext),
	}
	if err := validateAuditRecordForSlot(auditRecord, context.CurrentSlot); err != nil {
		return err
	}
	if err := authorizeAudit(&state, instruction.AuthorizeAudit.Commitment, auditRecord, context.Message); err != nil {
		return err
	}
	return storeStateAccount(stateAddress, stateAccount, state, context)
}

func loadStateAccount(programID structure.PublicKey, stateAddress structure.PublicKey, accounts map[structure.PublicKey]structure.Account) (structure.PrivacyState, structure.Account, error) {
	account, exists := accounts[stateAddress]
	if !exists {
		return structure.PrivacyState{}, structure.Account{}, fmt.Errorf("%w: privacy state account not found", structure.ErrInvalidLoadedTransaction)
	}
	if account.Owner != programID {
		return structure.PrivacyState{}, structure.Account{}, fmt.Errorf("%w: privacy state owner mismatch", structure.ErrInvalidPrivacyInstruction)
	}
	state, err := structure.UnmarshalPrivacyStateBinary(account.Data)
	if err != nil {
		return structure.PrivacyState{}, structure.Account{}, err
	}
	return state, account.Clone(), nil
}

func storeStateAccount(stateAddress structure.PublicKey, account structure.Account, state structure.PrivacyState, context runtime.InstructionContext) error {
	encoded, err := state.MarshalBinary()
	if err != nil {
		return err
	}
	if err := account.SetData(encoded, context.RentConfig); err != nil {
		return err
	}
	context.Accounts[stateAddress] = account
	return nil
}

func findNote(state structure.PrivacyState, commitment structure.Hash) int {
	for noteIndex, note := range state.Notes {
		if note.Commitment == commitment {
			return noteIndex
		}
	}
	return -1
}

func hasNullifier(state structure.PrivacyState, nullifier structure.Hash) bool {
	for _, spentNullifier := range state.SpentNullifiers {
		if spentNullifier == nullifier {
			return true
		}
	}
	return false
}

func consumeNote(
	state *structure.PrivacyState,
	commitment structure.Hash,
	nullifier structure.Hash,
	amount uint64,
	context runtime.InstructionContext,
	proof []byte,
	proofMessage []byte,
) error {
	if context.CurrentSlot == 0 {
		return fmt.Errorf("%w: spend slot cannot be zero", structure.ErrInvalidPrivacyInstruction)
	}
	if hasNullifier(*state, nullifier) {
		return fmt.Errorf("%w: nullifier already spent", structure.ErrInvalidPrivacyInstruction)
	}
	noteIndex := findNote(*state, commitment)
	if noteIndex < 0 {
		return fmt.Errorf("%w: source commitment not found", structure.ErrInvalidPrivacyInstruction)
	}
	note := state.Notes[noteIndex]
	if note.Spent {
		return fmt.Errorf("%w: source commitment already spent", structure.ErrInvalidPrivacyInstruction)
	}
	if note.Amount != amount {
		return fmt.Errorf("%w: privacy amount mismatch", structure.ErrInvalidPrivacyInstruction)
	}
	if err := validateSpendAuthorization(note.SpendAuthority, context.Message, proof, proofMessage); err != nil {
		return err
	}
	state.Notes[noteIndex].Spent = true
	state.Notes[noteIndex].SpentSlot = context.CurrentSlot
	state.Notes[noteIndex].SpendNullifier = nullifier
	state.SpentNullifiers = append(state.SpentNullifiers, nullifier)
	return nil
}

func validateSpendAuthorization(spendAuthority structure.PublicKey, message structure.ResolvedMessage, proof []byte, proofMessage []byte) error {
	if runtime.IsSignerAddress(spendAuthority, message) {
		return nil
	}
	if len(proof) == 0 {
		return fmt.Errorf("%w: spend authority must sign or provide zk proof", structure.ErrMissingRequiredSignature)
	}
	if len(proofMessage) == 0 {
		return fmt.Errorf("%w: spend proof message is empty", structure.ErrInvalidPrivacyInstruction)
	}
	if err := zk.VerifySchnorrProofBytes(proof, proofMessage, zk.Digest(spendAuthority)); err != nil {
		return fmt.Errorf("%w: verify spend zk proof: %w", structure.ErrInvalidPrivacyInstruction, err)
	}
	return nil
}

func appendAuditRecords(state *structure.PrivacyState, commitment structure.Hash, records []structure.PrivacyAuditRecord) error {
	if len(records) == 0 {
		return nil
	}
	noteIndex := findNote(*state, commitment)
	if noteIndex < 0 {
		return fmt.Errorf("%w: audit commitment not found", structure.ErrInvalidPrivacyInstruction)
	}
	for _, record := range records {
		if hasAuditRecord(state.Notes[noteIndex].AuditRecords, record) {
			return fmt.Errorf("%w: duplicate audit authorization", structure.ErrInvalidPrivacyInstruction)
		}
		if len(state.Notes[noteIndex].AuditRecords) >= structure.MaxPrivacyAuditRecordsPerNote {
			return fmt.Errorf("%w: audit record count exceeds %d", structure.ErrInvalidPrivacyInstruction, structure.MaxPrivacyAuditRecordsPerNote)
		}
		state.Notes[noteIndex].AuditRecords = append(state.Notes[noteIndex].AuditRecords, cloneAuditRecord(record))
	}
	return nil
}

func authorizeAudit(state *structure.PrivacyState, commitment structure.Hash, record structure.PrivacyAuditRecord, message structure.ResolvedMessage) error {
	noteIndex := findNote(*state, commitment)
	if noteIndex < 0 {
		return fmt.Errorf("%w: audit commitment not found", structure.ErrInvalidPrivacyInstruction)
	}
	note := state.Notes[noteIndex]
	if note.Spent {
		return fmt.Errorf("%w: cannot authorize spent note", structure.ErrInvalidPrivacyInstruction)
	}
	if !runtime.IsSignerAddress(note.SpendAuthority, message) {
		return fmt.Errorf("%w: audit authorization authority must sign", structure.ErrMissingRequiredSignature)
	}
	if hasAuditRecord(note.AuditRecords, record) {
		return fmt.Errorf("%w: duplicate audit authorization", structure.ErrInvalidPrivacyInstruction)
	}
	if len(note.AuditRecords) >= structure.MaxPrivacyAuditRecordsPerNote {
		return fmt.Errorf("%w: audit record count exceeds %d", structure.ErrInvalidPrivacyInstruction, structure.MaxPrivacyAuditRecordsPerNote)
	}
	state.Notes[noteIndex].AuditRecords = append(state.Notes[noteIndex].AuditRecords, cloneAuditRecord(record))
	return nil
}

func validateAuditRecordsForSlot(records []structure.PrivacyAuditRecord, currentSlot uint64) error {
	if len(records) > structure.MaxPrivacyAuditRecordsPerNote {
		return fmt.Errorf("%w: audit record count %d exceeds %d", structure.ErrInvalidPrivacyInstruction, len(records), structure.MaxPrivacyAuditRecordsPerNote)
	}
	seenRecords := make(map[auditRecordKey]struct{}, len(records))
	for recordIndex, record := range records {
		if err := validateAuditRecordForSlot(record, currentSlot); err != nil {
			return fmt.Errorf("privacy: audit record %d: %w", recordIndex, err)
		}
		key := auditKey(record)
		if _, exists := seenRecords[key]; exists {
			return fmt.Errorf("%w: duplicate audit record", structure.ErrInvalidPrivacyInstruction)
		}
		seenRecords[key] = struct{}{}
	}
	return nil
}

func validateAuditRecordForSlot(record structure.PrivacyAuditRecord, currentSlot uint64) error {
	if record.Auditor.IsZero() {
		return fmt.Errorf("%w: audit auditor is zero", structure.ErrInvalidPrivacyInstruction)
	}
	if !record.Scope.IsValid() {
		return fmt.Errorf("%w: invalid audit scope %d", structure.ErrInvalidPrivacyInstruction, record.Scope)
	}
	if len(record.AuditCiphertext) == 0 {
		return fmt.Errorf("%w: audit ciphertext cannot be empty", structure.ErrInvalidPrivacyInstruction)
	}
	if len(record.AuditCiphertext) > structure.MaxPrivacyAuditCiphertextBytes {
		return fmt.Errorf("%w: audit ciphertext length %d exceeds %d", structure.ErrInvalidPrivacyInstruction, len(record.AuditCiphertext), structure.MaxPrivacyAuditCiphertextBytes)
	}
	if record.ExpiresAtSlot != 0 && record.ExpiresAtSlot <= currentSlot {
		return fmt.Errorf("%w: audit authorization already expired", structure.ErrInvalidPrivacyInstruction)
	}
	return nil
}

func hasAuditRecord(records []structure.PrivacyAuditRecord, target structure.PrivacyAuditRecord) bool {
	targetKey := auditKey(target)
	for _, record := range records {
		if auditKey(record) == targetKey {
			return true
		}
	}
	return false
}

type auditRecordKey struct {
	auditor        structure.PublicKey
	scope          structure.PrivacyAuditScope
	ciphertextHash structure.Hash
}

func auditKey(record structure.PrivacyAuditRecord) auditRecordKey {
	ciphertextHash, err := structure.NewHash(utils.SHA256(record.AuditCiphertext))
	if err != nil {
		return auditRecordKey{auditor: record.Auditor, scope: record.Scope}
	}
	return auditRecordKey{auditor: record.Auditor, scope: record.Scope, ciphertextHash: ciphertextHash}
}

func cloneAuditRecords(records []structure.PrivacyAuditRecord) []structure.PrivacyAuditRecord {
	if records == nil {
		return nil
	}
	cloned := make([]structure.PrivacyAuditRecord, len(records))
	for index, record := range records {
		cloned[index] = cloneAuditRecord(record)
	}
	return cloned
}

func cloneAuditRecord(record structure.PrivacyAuditRecord) structure.PrivacyAuditRecord {
	return structure.PrivacyAuditRecord{
		Auditor:         record.Auditor,
		Scope:           record.Scope,
		ExpiresAtSlot:   record.ExpiresAtSlot,
		AuditCiphertext: utils.CloneBytes(record.AuditCiphertext),
	}
}
