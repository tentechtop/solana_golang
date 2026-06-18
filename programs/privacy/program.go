package privacy

import (
	"fmt"
	"math"

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
	if instruction.Deposit.Confidential != nil {
		if err := appendConfidentialDepositNote(&state, instruction); err != nil {
			return err
		}
		return storeStateAccount(stateAddress, stateAccount, state, context)
	}
	state.Notes = append(state.Notes, structure.PrivacyNoteRecord{
		Commitment:     instruction.Deposit.Commitment,
		SpendAuthority: instruction.Deposit.SpendAuthority,
		Amount:         instruction.Deposit.Amount,
		VMVersion:      instruction.VMVersion,
		EncryptedNote:  utils.CloneBytes(instruction.Deposit.EncryptedNote),
		AuditRecords:   cloneAuditRecords(instruction.Deposit.AuditRecords),
	})
	if err := creditPrivacyLiability(&state, instruction.Deposit.Amount); err != nil {
		return err
	}
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
	if err := validateAuditRecordsForSlot(instruction.Withdraw.ChangeAuditRecords, context.CurrentSlot); err != nil {
		return err
	}
	proofMessage, err := structure.BuildPrivacyWithdrawProofMessage(instruction.VMVersion, stateAddress, destinationAddress, *instruction.Withdraw, context.CurrentSlot)
	if err != nil {
		return err
	}
	inputAmount, err := privacySpendInputAmount(instruction.Withdraw.Amount, instruction.Withdraw.ChangeAmount)
	if err != nil {
		return err
	}
	if len(instruction.Withdraw.SourceConfidential) > 0 {
		if err := executeConfidentialWithdraw(&state, instruction, context, proofMessage); err != nil {
			return err
		}
		if err := storeStateAccount(stateAddress, stateAccount, state, context); err != nil {
			return err
		}
		return runtime.TransferLamports(stateAddress, destinationAddress, instruction.Withdraw.Amount, context.Accounts, context.RentConfig)
	}
	sourceNote, err := consumeNote(&state, instruction.Withdraw.SourceCommitment, instruction.Withdraw.Nullifier, inputAmount, context, instruction.Proof, proofMessage)
	if err != nil {
		return err
	}
	if err := appendWithdrawChangeNote(&state, instruction, sourceNote); err != nil {
		return err
	}
	if err := appendAuditRecords(&state, instruction.Withdraw.SourceCommitment, instruction.Withdraw.AuditRecords); err != nil {
		return err
	}
	if err := debitPrivacyLiability(&state, instruction.Withdraw.Amount); err != nil {
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
	outputStateAddress := privacyTransferOutputStateAddress(stateAddress, context)
	sourceState, sourceStateAccount, err := loadStateAccount(programID, stateAddress, context.Accounts)
	if err != nil {
		return err
	}
	outputState := structure.PrivacyState{}
	outputStateAccount := structure.Account{}
	if outputStateAddress != stateAddress {
		outputState, outputStateAccount, err = loadStateAccount(programID, outputStateAddress, context.Accounts)
		if err != nil {
			return err
		}
	} else {
		outputState = sourceState
	}
	if err := validateTransferOutput(sourceState, outputState, instruction); err != nil {
		return err
	}
	if err := validateAuditRecordsForSlot(instruction.Transfer.OutputAuditRecords, context.CurrentSlot); err != nil {
		return err
	}
	if err := validateAuditRecordsForSlot(instruction.Transfer.ChangeAuditRecords, context.CurrentSlot); err != nil {
		return err
	}
	proofMessage, err := structure.BuildPrivacyTransferProofMessageWithOutputState(instruction.VMVersion, stateAddress, outputStateAddress, *instruction.Transfer, context.CurrentSlot)
	if err != nil {
		return err
	}
	inputAmount, err := privacySpendInputAmount(instruction.Transfer.Amount, instruction.Transfer.ChangeAmount)
	if err != nil {
		return err
	}
	if len(instruction.Transfer.SourceConfidential) > 0 {
		if err := executeConfidentialTransfer(&sourceState, &outputState, outputStateAddress, stateAddress, instruction, context, proofMessage); err != nil {
			return err
		}
		if outputStateAddress == stateAddress {
			return storeStateAccount(stateAddress, sourceStateAccount, sourceState, context)
		}
		if err := storeStateAccount(stateAddress, sourceStateAccount, sourceState, context); err != nil {
			return err
		}
		if err := storeStateAccount(outputStateAddress, outputStateAccount, outputState, context); err != nil {
			return err
		}
		return runtime.TransferLamports(stateAddress, outputStateAddress, instruction.Transfer.Amount, context.Accounts, context.RentConfig)
	}
	sourceNote, err := consumeNote(&sourceState, instruction.Transfer.SourceCommitment, instruction.Transfer.Nullifier, inputAmount, context, instruction.Proof, proofMessage)
	if err != nil {
		return err
	}
	outputNote := privacyOutputNote(instruction)
	if outputStateAddress == stateAddress {
		sourceState.Notes = append(sourceState.Notes, outputNote)
		if err := appendTransferChangeNote(&sourceState, instruction, sourceNote); err != nil {
			return err
		}
		return storeStateAccount(stateAddress, sourceStateAccount, sourceState, context)
	}
	if err := appendTransferChangeNote(&sourceState, instruction, sourceNote); err != nil {
		return err
	}
	outputState.Notes = append(outputState.Notes, outputNote)
	if err := debitPrivacyLiability(&sourceState, instruction.Transfer.Amount); err != nil {
		return err
	}
	if err := creditPrivacyLiability(&outputState, instruction.Transfer.Amount); err != nil {
		return err
	}
	if err := storeStateAccount(stateAddress, sourceStateAccount, sourceState, context); err != nil {
		return err
	}
	if err := storeStateAccount(outputStateAddress, outputStateAccount, outputState, context); err != nil {
		return err
	}
	return runtime.TransferLamports(stateAddress, outputStateAddress, instruction.Transfer.Amount, context.Accounts, context.RentConfig)
}

func privacySpendInputAmount(amount uint64, changeAmount uint64) (uint64, error) {
	if amount > math.MaxUint64-changeAmount {
		return 0, fmt.Errorf("%w: privacy spend amount overflow", structure.ErrInvalidPrivacyInstruction)
	}
	return amount + changeAmount, nil
}

func privacyOutputNote(instruction structure.PrivacyInstruction) structure.PrivacyNoteRecord {
	return structure.PrivacyNoteRecord{
		Commitment:     instruction.Transfer.OutputCommitment,
		SpendAuthority: instruction.Transfer.OutputSpendAuthority,
		Amount:         instruction.Transfer.Amount,
		VMVersion:      instruction.VMVersion,
		EncryptedNote:  utils.CloneBytes(instruction.Transfer.OutputEncryptedNote),
		AuditRecords:   cloneAuditRecords(instruction.Transfer.OutputAuditRecords),
	}
}

func appendConfidentialDepositNote(state *structure.PrivacyState, instruction structure.PrivacyInstruction) error {
	if err := validateConfidentialDeposit(instruction.Deposit); err != nil {
		return err
	}
	state.Notes = append(state.Notes, structure.PrivacyNoteRecord{
		Commitment:     instruction.Deposit.Commitment,
		SpendAuthority: instruction.Deposit.SpendAuthority,
		VMVersion:      instruction.VMVersion,
		EncryptedNote:  utils.CloneBytes(instruction.Deposit.EncryptedNote),
		AuditRecords:   cloneAuditRecords(instruction.Deposit.AuditRecords),
		Confidential:   cloneConfidentialOutput(instruction.Deposit.Confidential),
	})
	if err := creditPrivacyLiability(state, instruction.Deposit.Amount); err != nil {
		return err
	}
	return nil
}

func executeConfidentialWithdraw(state *structure.PrivacyState, instruction structure.PrivacyInstruction, context runtime.InstructionContext, proofMessage []byte) error {
	sourceNote, err := consumeStateConfidentialNote(state, instruction.Withdraw.SourceCommitment, instruction.Withdraw.SourceConfidential, instruction.Withdraw.Nullifier, context, instruction.Proof, proofMessage)
	if err != nil {
		return err
	}
	outputCommitments := make([][]byte, 0, 1)
	if instruction.Withdraw.ChangeConfidential != nil {
		if err := appendConfidentialWithdrawChangeNote(state, instruction, sourceNote); err != nil {
			return err
		}
		outputCommitments = append(outputCommitments, instruction.Withdraw.ChangeConfidential.Commitment)
	}
	if err := zk.VerifyBalanceProof([][]byte{instruction.Withdraw.SourceConfidential}, outputCommitments, instruction.Withdraw.Amount, instruction.Withdraw.BalanceProof); err != nil {
		return fmt.Errorf("%w: verify confidential withdraw balance: %w", structure.ErrInvalidPrivacyInstruction, err)
	}
	if err := debitPrivacyLiability(state, instruction.Withdraw.Amount); err != nil {
		return err
	}
	return appendAuditRecords(state, instruction.Withdraw.SourceCommitment, instruction.Withdraw.AuditRecords)
}

func executeConfidentialTransfer(sourceState *structure.PrivacyState, outputState *structure.PrivacyState, outputStateAddress structure.PublicKey, sourceStateAddress structure.PublicKey, instruction structure.PrivacyInstruction, context runtime.InstructionContext, proofMessage []byte) error {
	sourceNote, err := consumeStateConfidentialNote(sourceState, instruction.Transfer.SourceCommitment, instruction.Transfer.SourceConfidential, instruction.Transfer.Nullifier, context, instruction.Proof, proofMessage)
	if err != nil {
		return err
	}
	outputCommitments := [][]byte{instruction.Transfer.OutputConfidential.Commitment}
	outputNote := privacyConfidentialTransferOutputNote(instruction)
	if outputStateAddress == sourceStateAddress {
		sourceState.Notes = append(sourceState.Notes, outputNote)
		if instruction.Transfer.ChangeConfidential != nil {
			if err := appendConfidentialTransferChangeNote(sourceState, instruction, sourceNote); err != nil {
				return err
			}
			outputCommitments = append(outputCommitments, instruction.Transfer.ChangeConfidential.Commitment)
		}
		return verifyConfidentialTransferBalance(instruction, outputCommitments)
	}
	if instruction.Transfer.ChangeConfidential != nil {
		if err := appendConfidentialTransferChangeNote(sourceState, instruction, sourceNote); err != nil {
			return err
		}
		outputCommitments = append(outputCommitments, instruction.Transfer.ChangeConfidential.Commitment)
	}
	outputState.Notes = append(outputState.Notes, outputNote)
	if err := debitPrivacyLiability(sourceState, instruction.Transfer.Amount); err != nil {
		return err
	}
	if err := creditPrivacyLiability(outputState, instruction.Transfer.Amount); err != nil {
		return err
	}
	return verifyConfidentialTransferBalance(instruction, outputCommitments)
}

func verifyConfidentialTransferBalance(instruction structure.PrivacyInstruction, outputCommitments [][]byte) error {
	if err := zk.VerifyBalanceProof([][]byte{instruction.Transfer.SourceConfidential}, outputCommitments, 0, instruction.Transfer.BalanceProof); err != nil {
		return fmt.Errorf("%w: verify confidential transfer balance: %w", structure.ErrInvalidPrivacyInstruction, err)
	}
	return nil
}

func privacyConfidentialTransferOutputNote(instruction structure.PrivacyInstruction) structure.PrivacyNoteRecord {
	return structure.PrivacyNoteRecord{
		Commitment:     instruction.Transfer.OutputCommitment,
		SpendAuthority: instruction.Transfer.OutputSpendAuthority,
		VMVersion:      instruction.VMVersion,
		EncryptedNote:  utils.CloneBytes(instruction.Transfer.OutputEncryptedNote),
		AuditRecords:   cloneAuditRecords(instruction.Transfer.OutputAuditRecords),
		Confidential:   cloneConfidentialOutput(instruction.Transfer.OutputConfidential),
	}
}

func appendWithdrawChangeNote(state *structure.PrivacyState, instruction structure.PrivacyInstruction, sourceNote structure.PrivacyNoteRecord) error {
	if instruction.Withdraw.ChangeAmount == 0 {
		return nil
	}
	return appendChangeNote(state, privacyWithdrawChangeNote(instruction), sourceNote)
}

func appendTransferChangeNote(state *structure.PrivacyState, instruction structure.PrivacyInstruction, sourceNote structure.PrivacyNoteRecord) error {
	if instruction.Transfer.ChangeAmount == 0 {
		return nil
	}
	return appendChangeNote(state, privacyTransferChangeNote(instruction), sourceNote)
}

func appendChangeNote(state *structure.PrivacyState, changeNote structure.PrivacyNoteRecord, sourceNote structure.PrivacyNoteRecord) error {
	if changeNote.SpendAuthority != sourceNote.SpendAuthority {
		return fmt.Errorf("%w: change spend authority must match source note", structure.ErrInvalidPrivacyInstruction)
	}
	if findNote(*state, changeNote.Commitment) >= 0 {
		return fmt.Errorf("%w: duplicate change commitment", structure.ErrInvalidPrivacyInstruction)
	}
	state.Notes = append(state.Notes, changeNote)
	return nil
}

func appendConfidentialWithdrawChangeNote(state *structure.PrivacyState, instruction structure.PrivacyInstruction, sourceNote structure.PrivacyNoteRecord) error {
	changeNote := privacyConfidentialWithdrawChangeNote(instruction)
	return appendConfidentialChangeNote(state, changeNote, sourceNote)
}

func appendConfidentialTransferChangeNote(state *structure.PrivacyState, instruction structure.PrivacyInstruction, sourceNote structure.PrivacyNoteRecord) error {
	changeNote := privacyConfidentialTransferChangeNote(instruction)
	return appendConfidentialChangeNote(state, changeNote, sourceNote)
}

func appendConfidentialChangeNote(state *structure.PrivacyState, changeNote structure.PrivacyNoteRecord, sourceNote structure.PrivacyNoteRecord) error {
	if changeNote.SpendAuthority != sourceNote.SpendAuthority {
		return fmt.Errorf("%w: confidential change spend authority must match source note", structure.ErrInvalidPrivacyInstruction)
	}
	if findNote(*state, changeNote.Commitment) >= 0 {
		return fmt.Errorf("%w: duplicate confidential change commitment", structure.ErrInvalidPrivacyInstruction)
	}
	state.Notes = append(state.Notes, changeNote)
	return nil
}

func privacyWithdrawChangeNote(instruction structure.PrivacyInstruction) structure.PrivacyNoteRecord {
	return structure.PrivacyNoteRecord{
		Commitment:     instruction.Withdraw.ChangeCommitment,
		SpendAuthority: instruction.Withdraw.ChangeSpendAuthority,
		Amount:         instruction.Withdraw.ChangeAmount,
		VMVersion:      instruction.VMVersion,
		EncryptedNote:  utils.CloneBytes(instruction.Withdraw.ChangeEncryptedNote),
		AuditRecords:   cloneAuditRecords(instruction.Withdraw.ChangeAuditRecords),
	}
}

func privacyConfidentialWithdrawChangeNote(instruction structure.PrivacyInstruction) structure.PrivacyNoteRecord {
	return structure.PrivacyNoteRecord{
		Commitment:     instruction.Withdraw.ChangeCommitment,
		SpendAuthority: instruction.Withdraw.ChangeSpendAuthority,
		VMVersion:      instruction.VMVersion,
		EncryptedNote:  utils.CloneBytes(instruction.Withdraw.ChangeEncryptedNote),
		AuditRecords:   cloneAuditRecords(instruction.Withdraw.ChangeAuditRecords),
		Confidential:   cloneConfidentialOutput(instruction.Withdraw.ChangeConfidential),
	}
}

func privacyTransferChangeNote(instruction structure.PrivacyInstruction) structure.PrivacyNoteRecord {
	return structure.PrivacyNoteRecord{
		Commitment:     instruction.Transfer.ChangeCommitment,
		SpendAuthority: instruction.Transfer.ChangeSpendAuthority,
		Amount:         instruction.Transfer.ChangeAmount,
		VMVersion:      instruction.VMVersion,
		EncryptedNote:  utils.CloneBytes(instruction.Transfer.ChangeEncryptedNote),
		AuditRecords:   cloneAuditRecords(instruction.Transfer.ChangeAuditRecords),
	}
}

func privacyConfidentialTransferChangeNote(instruction structure.PrivacyInstruction) structure.PrivacyNoteRecord {
	return structure.PrivacyNoteRecord{
		Commitment:     instruction.Transfer.ChangeCommitment,
		SpendAuthority: instruction.Transfer.ChangeSpendAuthority,
		VMVersion:      instruction.VMVersion,
		EncryptedNote:  utils.CloneBytes(instruction.Transfer.ChangeEncryptedNote),
		AuditRecords:   cloneAuditRecords(instruction.Transfer.ChangeAuditRecords),
		Confidential:   cloneConfidentialOutput(instruction.Transfer.ChangeConfidential),
	}
}

func privacyTransferOutputStateAddress(sourceStateAddress structure.PublicKey, context runtime.InstructionContext) structure.PublicKey {
	if len(context.Instruction.AccountIndexes) < 2 {
		return sourceStateAddress
	}
	return context.Message.AccountKeys[context.Instruction.AccountIndexes[1]]
}

func validateTransferOutput(sourceState structure.PrivacyState, outputState structure.PrivacyState, instruction structure.PrivacyInstruction) error {
	if findNote(sourceState, instruction.Transfer.OutputCommitment) >= 0 || findNote(outputState, instruction.Transfer.OutputCommitment) >= 0 {
		return fmt.Errorf("%w: duplicate output commitment", structure.ErrInvalidPrivacyInstruction)
	}
	if instruction.Transfer.ChangeAmount > 0 && (findNote(sourceState, instruction.Transfer.ChangeCommitment) >= 0 || findNote(outputState, instruction.Transfer.ChangeCommitment) >= 0) {
		return fmt.Errorf("%w: duplicate change commitment", structure.ErrInvalidPrivacyInstruction)
	}
	return nil
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
	if err := refreshPrivacyStatePublicCommitments(&state); err != nil {
		return err
	}
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

func refreshPrivacyStatePublicCommitments(state *structure.PrivacyState) error {
	state.Version = structure.PrivacyStateStorageVersion
	merkleRoot, err := structure.ComputePrivacyMerkleRoot(state.Notes)
	if err != nil {
		return err
	}
	state.MerkleRoot = merkleRoot
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
) (structure.PrivacyNoteRecord, error) {
	if context.CurrentSlot == 0 {
		return structure.PrivacyNoteRecord{}, fmt.Errorf("%w: spend slot cannot be zero", structure.ErrInvalidPrivacyInstruction)
	}
	if hasNullifier(*state, nullifier) {
		return structure.PrivacyNoteRecord{}, fmt.Errorf("%w: nullifier already spent", structure.ErrInvalidPrivacyInstruction)
	}
	noteIndex := findNote(*state, commitment)
	if noteIndex < 0 {
		return structure.PrivacyNoteRecord{}, fmt.Errorf("%w: source commitment not found", structure.ErrInvalidPrivacyInstruction)
	}
	note := state.Notes[noteIndex]
	if note.Spent {
		return structure.PrivacyNoteRecord{}, fmt.Errorf("%w: source commitment already spent", structure.ErrInvalidPrivacyInstruction)
	}
	if note.Amount != amount {
		return structure.PrivacyNoteRecord{}, fmt.Errorf("%w: privacy amount mismatch", structure.ErrInvalidPrivacyInstruction)
	}
	if err := validateSpendAuthorization(note.SpendAuthority, context.Message, proof, proofMessage); err != nil {
		return structure.PrivacyNoteRecord{}, err
	}
	state.Notes[noteIndex].Spent = true
	state.Notes[noteIndex].SpentSlot = context.CurrentSlot
	state.Notes[noteIndex].SpendNullifier = nullifier
	state.SpentNullifiers = append(state.SpentNullifiers, nullifier)
	return note, nil
}

func consumeStateConfidentialNote(
	state *structure.PrivacyState,
	commitment structure.Hash,
	confidentialCommitment []byte,
	nullifier structure.Hash,
	context runtime.InstructionContext,
	proof []byte,
	proofMessage []byte,
) (structure.PrivacyNoteRecord, error) {
	if context.CurrentSlot == 0 {
		return structure.PrivacyNoteRecord{}, fmt.Errorf("%w: spend slot cannot be zero", structure.ErrInvalidPrivacyInstruction)
	}
	if hasNullifier(*state, nullifier) {
		return structure.PrivacyNoteRecord{}, fmt.Errorf("%w: nullifier already spent", structure.ErrInvalidPrivacyInstruction)
	}
	noteIndex := findConfidentialNote(*state, commitment, confidentialCommitment)
	if noteIndex < 0 {
		return structure.PrivacyNoteRecord{}, fmt.Errorf("%w: confidential source commitment not found", structure.ErrInvalidPrivacyInstruction)
	}
	note := state.Notes[noteIndex]
	if note.Spent {
		return structure.PrivacyNoteRecord{}, fmt.Errorf("%w: confidential source commitment already spent", structure.ErrInvalidPrivacyInstruction)
	}
	if err := validateSpendAuthorization(note.SpendAuthority, context.Message, proof, proofMessage); err != nil {
		return structure.PrivacyNoteRecord{}, err
	}
	state.Notes[noteIndex].Spent = true
	state.Notes[noteIndex].SpentSlot = context.CurrentSlot
	state.Notes[noteIndex].SpendNullifier = nullifier
	state.SpentNullifiers = append(state.SpentNullifiers, nullifier)
	return note, nil
}

func findConfidentialNote(state structure.PrivacyState, commitment structure.Hash, confidentialCommitment []byte) int {
	for noteIndex, note := range state.Notes {
		if note.Commitment != commitment || note.Confidential == nil {
			continue
		}
		if string(note.Confidential.Commitment) == string(confidentialCommitment) {
			return noteIndex
		}
	}
	return -1
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

func validateConfidentialDeposit(params *structure.PrivacyDepositParams) error {
	if params == nil || params.Confidential == nil {
		return fmt.Errorf("%w: confidential deposit output is missing", structure.ErrInvalidPrivacyInstruction)
	}
	if err := zk.VerifyCommitmentAmountProof(params.Confidential.Commitment, params.Amount, params.AmountProof); err != nil {
		return fmt.Errorf("%w: verify confidential deposit amount: %w", structure.ErrInvalidPrivacyInstruction, err)
	}
	return nil
}

func creditPrivacyLiability(state *structure.PrivacyState, amount uint64) error {
	nextPool, err := safeAddUint64(state.PrivacyPoolLamports, amount)
	if err != nil {
		return fmt.Errorf("%w: privacy pool liability overflow", structure.ErrInvalidPrivacyInstruction)
	}
	nextLiability, err := safeAddUint64(state.UnspentNoteLiability, amount)
	if err != nil {
		return fmt.Errorf("%w: unspent note liability overflow", structure.ErrInvalidPrivacyInstruction)
	}
	state.PrivacyPoolLamports = nextPool
	state.UnspentNoteLiability = nextLiability
	return nil
}

func debitPrivacyLiability(state *structure.PrivacyState, amount uint64) error {
	if state.PrivacyPoolLamports < amount || state.UnspentNoteLiability < amount {
		return fmt.Errorf("%w: privacy pool liability too low", structure.ErrInsufficientLamports)
	}
	state.PrivacyPoolLamports -= amount
	state.UnspentNoteLiability -= amount
	return nil
}

func cloneConfidentialOutput(output *structure.PrivacyConfidentialOutput) *structure.PrivacyConfidentialOutput {
	if output == nil {
		return nil
	}
	cloned := &structure.PrivacyConfidentialOutput{
		Commitment:      utils.CloneBytes(output.Commitment),
		AmountPublicKey: utils.CloneBytes(output.AmountPublicKey),
		AmountCiphertext: zk.ElGamalCiphertext{
			NonceCommitment: utils.CloneBytes(output.AmountCiphertext.NonceCommitment),
			CiphertextPoint: utils.CloneBytes(output.AmountCiphertext.CiphertextPoint),
		},
		AmountProof: zk.AmountCiphertextProof{
			Version:            output.AmountProof.Version,
			CommitmentNonce:    utils.CloneBytes(output.AmountProof.CommitmentNonce),
			RandomnessNonce:    utils.CloneBytes(output.AmountProof.RandomnessNonce),
			CiphertextNonce:    utils.CloneBytes(output.AmountProof.CiphertextNonce),
			AmountResponse:     utils.CloneBytes(output.AmountProof.AmountResponse),
			BlindingResponse:   utils.CloneBytes(output.AmountProof.BlindingResponse),
			RandomnessResponse: utils.CloneBytes(output.AmountProof.RandomnessResponse),
		},
		RangeProof: output.RangeProof,
	}
	cloned.RangeProof.Commitment = utils.CloneBytes(output.RangeProof.Commitment)
	cloned.RangeProof.BitCommitments = cloneByteSlices(output.RangeProof.BitCommitments)
	cloned.RangeProof.BitProofs = cloneBitProofs(output.RangeProof.BitProofs)
	return cloned
}

func cloneByteSlices(values [][]byte) [][]byte {
	cloned := make([][]byte, len(values))
	for index := range values {
		cloned[index] = utils.CloneBytes(values[index])
	}
	return cloned
}

func cloneBitProofs(proofs []zk.BitProof) []zk.BitProof {
	cloned := make([]zk.BitProof, len(proofs))
	for index, proof := range proofs {
		cloned[index] = zk.BitProof{
			Nonce0:     utils.CloneBytes(proof.Nonce0),
			Nonce1:     utils.CloneBytes(proof.Nonce1),
			Challenge0: utils.CloneBytes(proof.Challenge0),
			Challenge1: utils.CloneBytes(proof.Challenge1),
			Response0:  utils.CloneBytes(proof.Response0),
			Response1:  utils.CloneBytes(proof.Response1),
		}
	}
	return cloned
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
