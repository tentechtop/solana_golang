package structure

import (
	"fmt"

	"solana_golang/codec/borsh"
	"solana_golang/utils"
	"solana_golang/zk"
)

const (
	PrivacyStateLegacyVersion      = uint16(1)
	PrivacyStateVersion            = uint16(1)
	PrivacyStateStorageVersion     = uint16(2)
	MaxPrivacyNotesPerState        = 1024
	MaxPrivacyInstructionBytes     = 128 * 1024
	MaxPrivacyEncryptedNoteBytes   = 512
	MaxPrivacyAuditRecordsPerNote  = 8
	MaxPrivacyAuditCiphertextBytes = 512
	MaxPrivacyProofBytes           = zk.DefaultMaxProofBytes
	privacySpendProofDomainV1      = "solana_golang.privacy.spend.v1"
	privacyMerkleLeafDomainV1      = "solana_golang.privacy.merkle.leaf.v1"
	privacyMerkleNodeDomainV1      = "solana_golang.privacy.merkle.node.v1"
)

type PrivacyInstructionType uint32

const (
	PrivacyInstructionDeposit PrivacyInstructionType = iota
	PrivacyInstructionWithdraw
	PrivacyInstructionTransfer
	PrivacyInstructionAuthorizeAudit
)

type PrivacyAuditScope uint8

const (
	PrivacyAuditScopeOwner PrivacyAuditScope = iota + 1
	PrivacyAuditScopeBusiness
	PrivacyAuditScopeRegulatory
)

// PrivacyInstruction 閹诲繗鍫梾鎰潌閸ュ搫鐣鹃幐鍥︽姢 + 妫板嫮鏆€ VM 閻楀牊婀伴崪宀冪槈閺勫骸鐡у▓鍏哥┒娴滃孩婀弶銉︽禌閹广垽鐛欑拠浣告珤閵?
type PrivacyInstruction struct {
	Type           PrivacyInstructionType
	VMVersion      uint16
	Proof          []byte
	Deposit        *PrivacyDepositParams
	Withdraw       *PrivacyWithdrawParams
	Transfer       *PrivacyTransferParams
	AuthorizeAudit *PrivacyAuthorizeAuditParams
}

// PrivacyDepositParams 閹诲繗鍫柅蹇旀鏉烆剟娈ｇ粔浣稿棘閺?+ 閻?commitment 閸滃苯鐦戦弬?note 鐞涖劏鎻梾鎰潌閺€鑸殿儥閵?
type PrivacyDepositParams struct {
	Amount         uint64
	Commitment     Hash
	SpendAuthority PublicKey
	EncryptedNote  []byte
	AuditRecords   []PrivacyAuditRecord
	Confidential   *PrivacyConfidentialOutput
	AmountProof    zk.BalanceProof
}

// PrivacyWithdrawParams 閹诲繗鍫梾鎰潌鏉烆剟鈧繑妲戦崣鍌涙殶 + 閸欘垶鈧澹橀梿?note 閺€顖涘瘮闁劌鍨庨懞杈瀭閵?
type PrivacyWithdrawParams struct {
	Amount               uint64
	SourceCommitment     Hash
	Nullifier            Hash
	AuditRecords         []PrivacyAuditRecord
	ChangeAmount         uint64
	ChangeCommitment     Hash
	ChangeSpendAuthority PublicKey
	ChangeEncryptedNote  []byte
	ChangeAuditRecords   []PrivacyAuditRecord
	SourceConfidential   []byte
	ChangeConfidential   *PrivacyConfidentialOutput
	BalanceProof         zk.BalanceProof
}

// PrivacyTransferParams 閹诲繗鍫梾鎰潌鏉烆剟娈ｇ粔浣稿棘閺?+ 閺€顖涘瘮鏉堟挸鍤?note 閸滃本澹橀梿?note 閸樼喎鐡欓崚娑樼紦閵?
type PrivacyTransferParams struct {
	Amount               uint64
	SourceCommitment     Hash
	Nullifier            Hash
	OutputCommitment     Hash
	OutputSpendAuthority PublicKey
	OutputEncryptedNote  []byte
	OutputAuditRecords   []PrivacyAuditRecord
	ChangeAmount         uint64
	ChangeCommitment     Hash
	ChangeSpendAuthority PublicKey
	ChangeEncryptedNote  []byte
	ChangeAuditRecords   []PrivacyAuditRecord
	SourceConfidential   []byte
	OutputConfidential   *PrivacyConfidentialOutput
	ChangeConfidential   *PrivacyConfidentialOutput
	BalanceProof         zk.BalanceProof
}

// PrivacyAuthorizeAuditParams 閹诲繗鍫€孤ゎ吀閹哄牊娼堥崣鍌涙殶 + 閸氬海鐢荤悰銉ュ帠閹哄牊娼堟稉宥夋付鐟曚焦鏁奸崝銊╂缁変椒缍戞０婵勨偓?
type PrivacyAuthorizeAuditParams struct {
	Commitment      Hash
	Auditor         PublicKey
	Scope           PrivacyAuditScope
	ExpiresAtSlot   uint64
	AuditCiphertext []byte
}

// PrivacyAuditRecord 閹诲繗鍫幒鍫熸綀鐎孤ゎ吀鐠佹澘缍?+ 闁惧彞绗傞崣顏冪箽鐎涙ɑ宸块弶鍐瘱閸ユ潙鎷扮€孤ゎ吀鐎靛棙鏋冮妴?
type PrivacyAuditRecord struct {
	Auditor         PublicKey
	Scope           PrivacyAuditScope
	ExpiresAtSlot   uint64
	AuditCiphertext []byte
}

// PrivacyConfidentialOutput 閹诲繗鍫崝鐘茬槕 Note 鏉堟挸鍤?+ 閻?Pedersen 閹佃儻顕崪宀冪槈閺勫孩娴涙禒锝夋懠娑撳﹥妲戦弬鍥櫨妫版縿鈧?
type PrivacyConfidentialOutput struct {
	Commitment       []byte
	AmountPublicKey  []byte
	AmountCiphertext zk.ElGamalCiphertext
	AmountProof      zk.AmountCiphertextProof
	RangeProof       zk.RangeProof
}

// PrivacyNoteRecord 閹诲繗鍫梾鎰潌閻樿埖鈧浇顔囪ぐ?+ 閺€顖涘瘮閺冄勬閺?Note 閸滃瞼鏁撴禍褏楠囬崝鐘茬槕 Note 閸忓崬鐡ㄩ妴?
type PrivacyNoteRecord struct {
	Commitment     Hash
	SpendAuthority PublicKey
	Amount         uint64
	Spent          bool
	SpentSlot      uint64
	SpendNullifier Hash
	VMVersion      uint16
	EncryptedNote  []byte
	AuditRecords   []PrivacyAuditRecord
	Confidential   *PrivacyConfidentialOutput
}

// PrivacyState 閹诲繗鍫梾鎰潌缁嬪绨悩鑸碘偓?+ 閸忣剙绱戝Ч鐘虹閸婂搫鎷?Merkle 閺嶇顔€閸忋劎缍夐弮鐘绘付鐎孤ゎ吀閸楀啿褰查弽绋款嚠娓氭稓绮伴妴?
type PrivacyState struct {
	Version              uint16
	Notes                []PrivacyNoteRecord
	SpentNullifiers      []Hash
	MerkleRoot           Hash
	PrivacyPoolLamports  uint64
	UnspentNoteLiability uint64
}

type privacyAuditRecordKey struct {
	Auditor        PublicKey
	Scope          PrivacyAuditScope
	CiphertextHash Hash
}

// NewPrivacyDepositInstruction 閸掓稑缂撻柅蹇旀鏉烆剟娈ｇ粔浣瑰瘹娴?+ 缂佺喍绔撮幍褑顢戦崣鍌涙殶閺嶏繝鐛欓妴?
func NewPrivacyDepositInstruction(vmVersion uint16, proof []byte, params PrivacyDepositParams) (PrivacyInstruction, error) {
	clonedParams := params
	clonedParams.EncryptedNote = utils.CloneBytes(params.EncryptedNote)
	clonedParams.AuditRecords = clonePrivacyAuditRecords(params.AuditRecords)
	clonedParams.Confidential = clonePrivacyConfidentialOutput(params.Confidential)
	clonedParams.AmountProof = cloneBalanceProof(params.AmountProof)
	instruction := PrivacyInstruction{Type: PrivacyInstructionDeposit, VMVersion: vmVersion, Proof: utils.CloneBytes(proof), Deposit: &clonedParams}
	return instruction, instruction.Validate()
}

// NewPrivacyWithdrawInstruction 閸掓稑缂撻梾鎰潌鏉烆剟鈧繑妲戦幐鍥︽姢 + 缂佺喍绔撮幍褑顢戦崣鍌涙殶閺嶏繝鐛欓妴?
func NewPrivacyWithdrawInstruction(vmVersion uint16, proof []byte, params PrivacyWithdrawParams) (PrivacyInstruction, error) {
	clonedParams := params
	clonedParams.AuditRecords = clonePrivacyAuditRecords(params.AuditRecords)
	clonedParams.ChangeEncryptedNote = utils.CloneBytes(params.ChangeEncryptedNote)
	clonedParams.ChangeAuditRecords = clonePrivacyAuditRecords(params.ChangeAuditRecords)
	clonedParams.SourceConfidential = utils.CloneBytes(params.SourceConfidential)
	clonedParams.ChangeConfidential = clonePrivacyConfidentialOutput(params.ChangeConfidential)
	clonedParams.BalanceProof = cloneBalanceProof(params.BalanceProof)
	instruction := PrivacyInstruction{Type: PrivacyInstructionWithdraw, VMVersion: vmVersion, Proof: utils.CloneBytes(proof), Withdraw: &clonedParams}
	return instruction, instruction.Validate()
}

// NewPrivacyTransferInstruction 閸掓稑缂撻梾鎰潌鏉烆剟娈ｇ粔浣瑰瘹娴?+ 缂佺喍绔撮幍褑顢戦崣鍌涙殶閺嶏繝鐛欓妴?
func NewPrivacyTransferInstruction(vmVersion uint16, proof []byte, params PrivacyTransferParams) (PrivacyInstruction, error) {
	clonedParams := params
	clonedParams.OutputEncryptedNote = utils.CloneBytes(params.OutputEncryptedNote)
	clonedParams.OutputAuditRecords = clonePrivacyAuditRecords(params.OutputAuditRecords)
	clonedParams.ChangeEncryptedNote = utils.CloneBytes(params.ChangeEncryptedNote)
	clonedParams.ChangeAuditRecords = clonePrivacyAuditRecords(params.ChangeAuditRecords)
	clonedParams.SourceConfidential = utils.CloneBytes(params.SourceConfidential)
	clonedParams.OutputConfidential = clonePrivacyConfidentialOutput(params.OutputConfidential)
	clonedParams.ChangeConfidential = clonePrivacyConfidentialOutput(params.ChangeConfidential)
	clonedParams.BalanceProof = cloneBalanceProof(params.BalanceProof)
	instruction := PrivacyInstruction{Type: PrivacyInstructionTransfer, VMVersion: vmVersion, Proof: utils.CloneBytes(proof), Transfer: &clonedParams}
	return instruction, instruction.Validate()
}

// NewPrivacyAuthorizeAuditInstruction 閸掓稑缂撶€孤ゎ吀閹哄牊娼堥幐鍥︽姢 + 閸忎浇顔忛悽銊﹀煕閸氬海鐢婚幒鍫熸綀閻╂垹顓搁幋鏍ь吀鐠佲剝鏌熼妴?
func NewPrivacyAuthorizeAuditInstruction(vmVersion uint16, proof []byte, params PrivacyAuthorizeAuditParams) (PrivacyInstruction, error) {
	clonedParams := params
	clonedParams.AuditCiphertext = utils.CloneBytes(params.AuditCiphertext)
	instruction := PrivacyInstruction{Type: PrivacyInstructionAuthorizeAudit, VMVersion: vmVersion, Proof: utils.CloneBytes(proof), AuthorizeAudit: &clonedParams}
	return instruction, instruction.Validate()
}

// BuildPrivacyWithdrawProofMessage 閺嬪嫰鈧娀娈ｇ粔浣瑰絹閻滄媽鐦夐弰搴㈢Х閹?+ 缂佹垵鐣鹃崗銊╁劥閼鸿精鍨傜拠顓濈疅闂冨弶顒?proof 闁插秵鏂侀妴?
func BuildPrivacyWithdrawProofMessage(vmVersion uint16, stateAddress PublicKey, destinationAddress PublicKey, params PrivacyWithdrawParams, currentSlot uint64) ([]byte, error) {
	if err := validatePrivacyProofMessageHeader(vmVersion, stateAddress, currentSlot); err != nil {
		return nil, err
	}
	if destinationAddress.IsZero() {
		return nil, fmt.Errorf("%w: withdraw destination is zero", ErrInvalidPrivacyInstruction)
	}
	if err := validatePrivacyWithdrawParams(&params); err != nil {
		return nil, err
	}
	auditRecordsHash, err := hashPrivacyAuditRecords(params.AuditRecords)
	if err != nil {
		return nil, err
	}
	changeEncryptedNoteHash, changeAuditRecordsHash, err := privacyChangeOutputHashes(
		params.ChangeAmount,
		params.ChangeEncryptedNote,
		params.ChangeAuditRecords,
	)
	if err != nil {
		return nil, err
	}

	writer := newPrivacyProofMessageWriter(vmVersion, PrivacyInstructionWithdraw, currentSlot, stateAddress)
	writer.WriteFixedBytes(destinationAddress[:])
	writer.WriteUint64(params.Amount)
	writer.WriteFixedBytes(params.SourceCommitment[:])
	writer.WriteFixedBytes(params.Nullifier[:])
	writer.WriteFixedBytes(auditRecordsHash[:])
	writer.WriteUint64(params.ChangeAmount)
	writer.WriteFixedBytes(params.ChangeCommitment[:])
	writer.WriteFixedBytes(params.ChangeSpendAuthority[:])
	writer.WriteFixedBytes(changeEncryptedNoteHash[:])
	writer.WriteFixedBytes(changeAuditRecordsHash[:])
	return writer.Bytes(), nil
}

// BuildPrivacyTransferProofMessage 閺嬪嫰鈧娀娈ｇ粔浣芥祮闂呮劗顫嗙拠浣规濞戝牊浼?+ 姒涙顓荤紒鎴濈暰閸氬奔绔撮悩鑸碘偓浣藉閹磋渹绻氶幐浣稿悑鐎圭鐨熼悽銊ｂ偓?
func BuildPrivacyTransferProofMessage(vmVersion uint16, stateAddress PublicKey, params PrivacyTransferParams, currentSlot uint64) ([]byte, error) {
	return BuildPrivacyTransferProofMessageWithOutputState(vmVersion, stateAddress, stateAddress, params, currentSlot)
}

// BuildPrivacyTransferProofMessageWithOutputState 閺嬪嫰鈧娀娈ｇ粔浣芥祮闂呮劗顫嗙拠浣规濞戝牊浼?+ 缂佹垵鐣炬潏鎾冲毉閻樿埖鈧浇澶勯幋鐑芥Щ濮?note 鐞氼偊鍣哥€规艾鎮滈妴?
func BuildPrivacyTransferProofMessageWithOutputState(vmVersion uint16, stateAddress PublicKey, outputStateAddress PublicKey, params PrivacyTransferParams, currentSlot uint64) ([]byte, error) {
	if err := validatePrivacyProofMessageHeader(vmVersion, stateAddress, currentSlot); err != nil {
		return nil, err
	}
	if outputStateAddress.IsZero() {
		return nil, fmt.Errorf("%w: output privacy state address is zero", ErrInvalidPrivacyInstruction)
	}
	if err := validatePrivacyTransferParams(&params); err != nil {
		return nil, err
	}
	encryptedNoteHash, err := NewHash(utils.SHA256(params.OutputEncryptedNote))
	if err != nil {
		return nil, err
	}
	auditRecordsHash, err := hashPrivacyAuditRecords(params.OutputAuditRecords)
	if err != nil {
		return nil, err
	}
	changeEncryptedNoteHash, changeAuditRecordsHash, err := privacyChangeOutputHashes(
		params.ChangeAmount,
		params.ChangeEncryptedNote,
		params.ChangeAuditRecords,
	)
	if err != nil {
		return nil, err
	}

	writer := newPrivacyProofMessageWriter(vmVersion, PrivacyInstructionTransfer, currentSlot, stateAddress)
	writer.WriteFixedBytes(outputStateAddress[:])
	writer.WriteUint64(params.Amount)
	writer.WriteFixedBytes(params.SourceCommitment[:])
	writer.WriteFixedBytes(params.Nullifier[:])
	writer.WriteFixedBytes(params.OutputCommitment[:])
	writer.WriteFixedBytes(params.OutputSpendAuthority[:])
	writer.WriteFixedBytes(encryptedNoteHash[:])
	writer.WriteFixedBytes(auditRecordsHash[:])
	writer.WriteUint64(params.ChangeAmount)
	writer.WriteFixedBytes(params.ChangeCommitment[:])
	writer.WriteFixedBytes(params.ChangeSpendAuthority[:])
	writer.WriteFixedBytes(changeEncryptedNoteHash[:])
	writer.WriteFixedBytes(changeAuditRecordsHash[:])
	return writer.Bytes(), nil
}

// Validate 閺嶏繝鐛欓梾鎰潌閹稿洣鎶?+ 闂冨弶顒涢柌鎴︻杺娑撴椽娴傞妴浣界槈閺勫氦绻冩径褍鎷?oneof 閸欏倹鏆熺紓鍝勩亼閵?
func (instruction PrivacyInstruction) Validate() error {
	if err := zk.ValidateProtocolVersion(instruction.VMVersion); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidPrivacyInstruction, err)
	}
	if err := zk.ValidateOptionalProofBytes(instruction.Proof, MaxPrivacyProofBytes); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidPrivacyInstruction, err)
	}
	switch instruction.Type {
	case PrivacyInstructionDeposit:
		return validatePrivacyDepositParams(instruction.Deposit)
	case PrivacyInstructionWithdraw:
		return validatePrivacyWithdrawParams(instruction.Withdraw)
	case PrivacyInstructionTransfer:
		return validatePrivacyTransferParams(instruction.Transfer)
	case PrivacyInstructionAuthorizeAudit:
		return validatePrivacyAuthorizeAuditParams(instruction.AuthorizeAudit)
	default:
		return fmt.Errorf("%w: unsupported type %d", ErrInvalidPrivacyInstruction, instruction.Type)
	}
}

// MarshalBinary 鎼村繐鍨崠鏍缁変焦瀵氭禒?+ 閸ュ搫鐣鹃弽鐓庣础娓氬じ绨禍銈嗘缁涙儳鎮曢崪灞炬弓閺?VM 閸忕厧顔愰妴?
func (instruction PrivacyInstruction) MarshalBinary() ([]byte, error) {
	if err := instruction.Validate(); err != nil {
		return nil, err
	}

	writer := borsh.NewWriter(MaxPrivacyInstructionBytes)
	writer.WriteUint32(uint32(instruction.Type))
	writer.WriteUint16(instruction.VMVersion)
	if err := writer.WriteBytes(instruction.Proof); err != nil {
		return nil, fmt.Errorf("structure: encode privacy proof: %w", err)
	}
	if err := writePrivacyInstructionBody(writer, instruction); err != nil {
		return nil, fmt.Errorf("structure: encode privacy instruction body: %w", err)
	}
	return writer.Bytes(), nil
}

// UnmarshalPrivacyInstructionBinary 閸欏秴绨崚妤€瀵查梾鎰潌閹稿洣鎶?+ 閹锋帞绮风亸楣冨劥濮光剝鐓嬬€涙濡妴?
func UnmarshalPrivacyInstructionBinary(data []byte) (PrivacyInstruction, error) {
	reader := borsh.NewReader(data, MaxPrivacyInstructionBytes)
	instructionType, err := reader.ReadUint32()
	if err != nil {
		return PrivacyInstruction{}, fmt.Errorf("structure: decode privacy instruction type: %w", err)
	}
	vmVersion, err := reader.ReadUint16()
	if err != nil {
		return PrivacyInstruction{}, fmt.Errorf("structure: decode privacy vm version: %w", err)
	}
	proof, err := reader.ReadBytes()
	if err != nil {
		return PrivacyInstruction{}, fmt.Errorf("structure: decode privacy proof: %w", err)
	}

	instruction, err := readPrivacyInstructionBody(reader, PrivacyInstructionType(instructionType), vmVersion, proof)
	if err != nil {
		return PrivacyInstruction{}, err
	}
	if err := reader.EnsureEOF(); err != nil {
		return PrivacyInstruction{}, fmt.Errorf("structure: decode privacy instruction eof: %w", err)
	}
	return instruction, instruction.Validate()
}

// MarshalBinary 鎼村繐鍨崠鏍缁変胶濮搁幀?+ 娴ｈ法鏁?Borsh 娣囨繆鐦夐悩鑸碘偓浣哥摟閼哄倻鈥樼€规碍鈧佲偓?
func (state PrivacyState) MarshalBinary() ([]byte, error) {
	if err := normalizePrivacyState(&state); err != nil {
		return nil, err
	}
	if err := state.Validate(); err != nil {
		return nil, err
	}

	writer := borsh.NewWriter(MaxAccountDataSize)
	writer.WriteUint16(PrivacyStateStorageVersion)
	writer.WriteUint32(uint32(len(state.Notes)))
	for _, note := range state.Notes {
		writer.WriteFixedBytes(note.Commitment[:])
		writer.WriteFixedBytes(note.SpendAuthority[:])
		writer.WriteUint64(note.Amount)
		writer.WriteBool(note.Spent)
		writer.WriteUint64(note.SpentSlot)
		writer.WriteFixedBytes(note.SpendNullifier[:])
		writer.WriteUint16(note.VMVersion)
		if err := writer.WriteBytes(note.EncryptedNote); err != nil {
			return nil, fmt.Errorf("structure: encode privacy note: %w", err)
		}
		if err := writePrivacyAuditRecords(writer, note.AuditRecords); err != nil {
			return nil, fmt.Errorf("structure: encode privacy audit records: %w", err)
		}
		if err := writePrivacyConfidentialOutput(writer, note.Confidential); err != nil {
			return nil, fmt.Errorf("structure: encode privacy confidential note: %w", err)
		}
	}
	writer.WriteUint32(uint32(len(state.SpentNullifiers)))
	for _, nullifier := range state.SpentNullifiers {
		writer.WriteFixedBytes(nullifier[:])
	}
	writer.WriteFixedBytes(state.MerkleRoot[:])
	writer.WriteUint64(state.PrivacyPoolLamports)
	writer.WriteUint64(state.UnspentNoteLiability)
	return writer.Bytes(), nil
}

// UnmarshalPrivacyStateBinary 閸欏秴绨崚妤€瀵查梾鎰潌閻樿埖鈧?+ 缁岄缚澶勯幋宄版嫲妫板嫬鍨庨柊宥夋祩閺佺増宓侀柈鑺ュ瘻閻楀牊婀版稉鈧崚婵嗩潗閸栨牓鈧?
func UnmarshalPrivacyStateBinary(data []byte) (PrivacyState, error) {
	if len(data) == 0 || isZeroFilledPrivacyStateData(data) {
		return PrivacyState{Version: PrivacyStateVersion}, nil
	}

	reader := borsh.NewReader(data, MaxAccountDataSize)
	version, err := reader.ReadUint16()
	if err != nil {
		return PrivacyState{}, fmt.Errorf("structure: decode privacy state version: %w", err)
	}
	state := PrivacyState{Version: version}
	if state.Notes, err = readPrivacyNotes(reader, version); err != nil {
		return PrivacyState{}, err
	}
	if state.SpentNullifiers, err = readPrivacyNullifiers(reader); err != nil {
		return PrivacyState{}, err
	}
	if reader.Remaining() > 0 {
		if state.MerkleRoot, err = readPrivacyHash(reader, "privacy merkle root"); err != nil {
			return PrivacyState{}, err
		}
		if state.PrivacyPoolLamports, err = reader.ReadUint64(); err != nil {
			return PrivacyState{}, fmt.Errorf("structure: decode privacy pool lamports: %w", err)
		}
		if state.UnspentNoteLiability, err = reader.ReadUint64(); err != nil {
			return PrivacyState{}, fmt.Errorf("structure: decode unspent note liability: %w", err)
		}
	}
	if err := reader.EnsureEOF(); err != nil {
		return PrivacyState{}, fmt.Errorf("structure: decode privacy state eof: %w", err)
	}
	if err := normalizePrivacyState(&state); err != nil {
		return PrivacyState{}, err
	}
	return state, state.Validate()
}

func isZeroFilledPrivacyStateData(data []byte) bool {
	for _, value := range data {
		if value != 0 {
			return false
		}
	}
	return true
}

// Validate 閺嶏繝鐛欓梾鎰潌閻樿埖鈧?+ 闂冨弶顒涢柌宥咁槻 commitment 閸滃矂鍣告径?nullifier閵?
func (state PrivacyState) Validate() error {
	if state.Version != PrivacyStateVersion && state.Version != PrivacyStateStorageVersion {
		return fmt.Errorf("%w: unsupported state version %d", ErrInvalidPrivacyInstruction, state.Version)
	}
	if len(state.Notes) > MaxPrivacyNotesPerState {
		return fmt.Errorf("%w: note count %d exceeds %d", ErrInvalidPrivacyInstruction, len(state.Notes), MaxPrivacyNotesPerState)
	}
	if err := validatePrivacyStateUniqueness(state); err != nil {
		return err
	}
	if err := validatePrivacyStateMerkleRoot(state); err != nil {
		return err
	}
	if state.PrivacyPoolLamports != state.UnspentNoteLiability {
		return fmt.Errorf("%w: privacy pool liability mismatch", ErrInvalidPrivacyInstruction)
	}
	return nil
}

func normalizePrivacyState(state *PrivacyState) error {
	if state == nil {
		return fmt.Errorf("%w: privacy state is nil", ErrInvalidPrivacyInstruction)
	}
	merkleRoot, err := ComputePrivacyMerkleRoot(state.Notes)
	if err != nil {
		return err
	}
	if state.Version == PrivacyStateLegacyVersion {
		state.Version = PrivacyStateStorageVersion
		state.MerkleRoot = merkleRoot
		state.UnspentNoteLiability = legacyPrivacyStateLiability(state.Notes)
		state.PrivacyPoolLamports = state.UnspentNoteLiability
		return nil
	}
	if state.MerkleRoot.IsZero() {
		state.MerkleRoot = merkleRoot
	}
	return nil
}

func validatePrivacyProofMessageHeader(vmVersion uint16, stateAddress PublicKey, currentSlot uint64) error {
	if err := zk.ValidateProtocolVersion(vmVersion); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidPrivacyInstruction, err)
	}
	if stateAddress.IsZero() {
		return fmt.Errorf("%w: privacy state address is zero", ErrInvalidPrivacyInstruction)
	}
	if currentSlot == 0 {
		return fmt.Errorf("%w: proof slot cannot be zero", ErrInvalidPrivacyInstruction)
	}
	return nil
}

func newPrivacyProofMessageWriter(vmVersion uint16, instructionType PrivacyInstructionType, currentSlot uint64, stateAddress PublicKey) *borsh.Writer {
	writer := borsh.NewWriter(MaxPrivacyInstructionBytes)
	writer.WriteFixedBytes([]byte(privacySpendProofDomainV1))
	writer.WriteUint16(vmVersion)
	writer.WriteUint32(uint32(instructionType))
	writer.WriteUint64(currentSlot)
	writer.WriteFixedBytes(stateAddress[:])
	return writer
}

func hashPrivacyAuditRecords(records []PrivacyAuditRecord) (Hash, error) {
	writer := borsh.NewWriter(MaxPrivacyInstructionBytes)
	if err := writePrivacyAuditRecords(writer, records); err != nil {
		return Hash{}, err
	}
	return NewHash(utils.SHA256(writer.Bytes()))
}

func writePrivacyInstructionBody(writer *borsh.Writer, instruction PrivacyInstruction) error {
	switch instruction.Type {
	case PrivacyInstructionDeposit:
		writer.WriteUint64(instruction.Deposit.Amount)
		writer.WriteFixedBytes(instruction.Deposit.Commitment[:])
		writer.WriteFixedBytes(instruction.Deposit.SpendAuthority[:])
		if err := writer.WriteBytes(instruction.Deposit.EncryptedNote); err != nil {
			return err
		}
		if err := writePrivacyAuditRecords(writer, instruction.Deposit.AuditRecords); err != nil {
			return err
		}
		if err := writePrivacyConfidentialOutput(writer, instruction.Deposit.Confidential); err != nil {
			return err
		}
		return writePrivacyBalanceProof(writer, instruction.Deposit.AmountProof)
	case PrivacyInstructionWithdraw:
		writer.WriteUint64(instruction.Withdraw.Amount)
		writer.WriteFixedBytes(instruction.Withdraw.SourceCommitment[:])
		writer.WriteFixedBytes(instruction.Withdraw.Nullifier[:])
		if err := writePrivacyAuditRecords(writer, instruction.Withdraw.AuditRecords); err != nil {
			return err
		}
		if err := writePrivacyChangeOutput(writer,
			instruction.Withdraw.ChangeAmount,
			instruction.Withdraw.ChangeCommitment,
			instruction.Withdraw.ChangeSpendAuthority,
			instruction.Withdraw.ChangeEncryptedNote,
			instruction.Withdraw.ChangeAuditRecords,
		); err != nil {
			return err
		}
		if err := writer.WriteBytes(instruction.Withdraw.SourceConfidential); err != nil {
			return err
		}
		if err := writePrivacyConfidentialOutput(writer, instruction.Withdraw.ChangeConfidential); err != nil {
			return err
		}
		return writePrivacyBalanceProof(writer, instruction.Withdraw.BalanceProof)
	case PrivacyInstructionTransfer:
		writer.WriteUint64(instruction.Transfer.Amount)
		writer.WriteFixedBytes(instruction.Transfer.SourceCommitment[:])
		writer.WriteFixedBytes(instruction.Transfer.Nullifier[:])
		writer.WriteFixedBytes(instruction.Transfer.OutputCommitment[:])
		writer.WriteFixedBytes(instruction.Transfer.OutputSpendAuthority[:])
		if err := writer.WriteBytes(instruction.Transfer.OutputEncryptedNote); err != nil {
			return err
		}
		if err := writePrivacyAuditRecords(writer, instruction.Transfer.OutputAuditRecords); err != nil {
			return err
		}
		if err := writePrivacyChangeOutput(writer,
			instruction.Transfer.ChangeAmount,
			instruction.Transfer.ChangeCommitment,
			instruction.Transfer.ChangeSpendAuthority,
			instruction.Transfer.ChangeEncryptedNote,
			instruction.Transfer.ChangeAuditRecords,
		); err != nil {
			return err
		}
		if err := writer.WriteBytes(instruction.Transfer.SourceConfidential); err != nil {
			return err
		}
		if err := writePrivacyConfidentialOutput(writer, instruction.Transfer.OutputConfidential); err != nil {
			return err
		}
		if err := writePrivacyConfidentialOutput(writer, instruction.Transfer.ChangeConfidential); err != nil {
			return err
		}
		return writePrivacyBalanceProof(writer, instruction.Transfer.BalanceProof)
	case PrivacyInstructionAuthorizeAudit:
		writer.WriteFixedBytes(instruction.AuthorizeAudit.Commitment[:])
		writer.WriteFixedBytes(instruction.AuthorizeAudit.Auditor[:])
		writer.WriteUint8(uint8(instruction.AuthorizeAudit.Scope))
		writer.WriteUint64(instruction.AuthorizeAudit.ExpiresAtSlot)
		return writer.WriteBytes(instruction.AuthorizeAudit.AuditCiphertext)
	}
	return nil
}

func readPrivacyInstructionBody(reader *borsh.Reader, instructionType PrivacyInstructionType, vmVersion uint16, proof []byte) (PrivacyInstruction, error) {
	switch instructionType {
	case PrivacyInstructionDeposit:
		return readPrivacyDepositInstruction(reader, vmVersion, proof)
	case PrivacyInstructionWithdraw:
		return readPrivacyWithdrawInstruction(reader, vmVersion, proof)
	case PrivacyInstructionTransfer:
		return readPrivacyTransferInstruction(reader, vmVersion, proof)
	case PrivacyInstructionAuthorizeAudit:
		return readPrivacyAuthorizeAuditInstruction(reader, vmVersion, proof)
	default:
		return PrivacyInstruction{}, fmt.Errorf("%w: unsupported type %d", ErrInvalidPrivacyInstruction, instructionType)
	}
}

func readPrivacyDepositInstruction(reader *borsh.Reader, vmVersion uint16, proof []byte) (PrivacyInstruction, error) {
	amount, err := reader.ReadUint64()
	if err != nil {
		return PrivacyInstruction{}, fmt.Errorf("structure: decode privacy deposit amount: %w", err)
	}
	commitment, err := readPrivacyHash(reader, "deposit commitment")
	if err != nil {
		return PrivacyInstruction{}, err
	}
	spendAuthority, err := readPrivacyPublicKey(reader, "deposit spend authority")
	if err != nil {
		return PrivacyInstruction{}, err
	}
	encryptedNote, err := reader.ReadBytes()
	if err != nil {
		return PrivacyInstruction{}, fmt.Errorf("structure: decode privacy deposit encrypted note: %w", err)
	}
	auditRecords, err := readPrivacyAuditRecords(reader)
	if err != nil {
		return PrivacyInstruction{}, err
	}
	params := PrivacyDepositParams{Amount: amount, Commitment: commitment, SpendAuthority: spendAuthority, EncryptedNote: encryptedNote, AuditRecords: auditRecords}
	if reader.Remaining() > 0 {
		params.Confidential, err = readPrivacyConfidentialOutput(reader)
		if err != nil {
			return PrivacyInstruction{}, err
		}
		params.AmountProof, err = readPrivacyBalanceProof(reader)
		if err != nil {
			return PrivacyInstruction{}, err
		}
	}
	return NewPrivacyDepositInstruction(vmVersion, proof, params)
}

func readPrivacyWithdrawInstruction(reader *borsh.Reader, vmVersion uint16, proof []byte) (PrivacyInstruction, error) {
	amount, err := reader.ReadUint64()
	if err != nil {
		return PrivacyInstruction{}, fmt.Errorf("structure: decode privacy withdraw amount: %w", err)
	}
	sourceCommitment, err := readPrivacyHash(reader, "withdraw source commitment")
	if err != nil {
		return PrivacyInstruction{}, err
	}
	nullifier, err := readPrivacyHash(reader, "withdraw nullifier")
	if err != nil {
		return PrivacyInstruction{}, err
	}
	auditRecords, err := readPrivacyAuditRecords(reader)
	if err != nil {
		return PrivacyInstruction{}, err
	}
	changeAmount, changeCommitment, changeSpendAuthority, changeEncryptedNote, changeAuditRecords, err := readPrivacyChangeOutput(reader, "withdraw change")
	if err != nil {
		return PrivacyInstruction{}, err
	}
	sourceConfidential := []byte(nil)
	var changeConfidential *PrivacyConfidentialOutput
	balanceProof := zk.BalanceProof{}
	if reader.Remaining() > 0 {
		sourceConfidential, err = reader.ReadBytes()
		if err != nil {
			return PrivacyInstruction{}, fmt.Errorf("structure: decode withdraw source confidential: %w", err)
		}
		changeConfidential, err = readPrivacyConfidentialOutput(reader)
		if err != nil {
			return PrivacyInstruction{}, err
		}
		balanceProof, err = readPrivacyBalanceProof(reader)
		if err != nil {
			return PrivacyInstruction{}, err
		}
	}
	return NewPrivacyWithdrawInstruction(vmVersion, proof, PrivacyWithdrawParams{
		Amount:               amount,
		SourceCommitment:     sourceCommitment,
		Nullifier:            nullifier,
		AuditRecords:         auditRecords,
		ChangeAmount:         changeAmount,
		ChangeCommitment:     changeCommitment,
		ChangeSpendAuthority: changeSpendAuthority,
		ChangeEncryptedNote:  changeEncryptedNote,
		ChangeAuditRecords:   changeAuditRecords,
		SourceConfidential:   sourceConfidential,
		ChangeConfidential:   changeConfidential,
		BalanceProof:         balanceProof,
	})
}

func readPrivacyTransferInstruction(reader *borsh.Reader, vmVersion uint16, proof []byte) (PrivacyInstruction, error) {
	amount, err := reader.ReadUint64()
	if err != nil {
		return PrivacyInstruction{}, fmt.Errorf("structure: decode privacy transfer amount: %w", err)
	}
	sourceCommitment, err := readPrivacyHash(reader, "transfer source commitment")
	if err != nil {
		return PrivacyInstruction{}, err
	}
	nullifier, err := readPrivacyHash(reader, "transfer nullifier")
	if err != nil {
		return PrivacyInstruction{}, err
	}
	outputCommitment, err := readPrivacyHash(reader, "transfer output commitment")
	if err != nil {
		return PrivacyInstruction{}, err
	}
	outputSpendAuthority, err := readPrivacyPublicKey(reader, "transfer output spend authority")
	if err != nil {
		return PrivacyInstruction{}, err
	}
	encryptedNote, err := reader.ReadBytes()
	if err != nil {
		return PrivacyInstruction{}, fmt.Errorf("structure: decode privacy transfer encrypted note: %w", err)
	}
	auditRecords, err := readPrivacyAuditRecords(reader)
	if err != nil {
		return PrivacyInstruction{}, err
	}
	changeAmount, changeCommitment, changeSpendAuthority, changeEncryptedNote, changeAuditRecords, err := readPrivacyChangeOutput(reader, "transfer change")
	if err != nil {
		return PrivacyInstruction{}, err
	}
	sourceConfidential := []byte(nil)
	var outputConfidential *PrivacyConfidentialOutput
	var changeConfidential *PrivacyConfidentialOutput
	balanceProof := zk.BalanceProof{}
	if reader.Remaining() > 0 {
		sourceConfidential, err = reader.ReadBytes()
		if err != nil {
			return PrivacyInstruction{}, fmt.Errorf("structure: decode transfer source confidential: %w", err)
		}
		outputConfidential, err = readPrivacyConfidentialOutput(reader)
		if err != nil {
			return PrivacyInstruction{}, err
		}
		changeConfidential, err = readPrivacyConfidentialOutput(reader)
		if err != nil {
			return PrivacyInstruction{}, err
		}
		balanceProof, err = readPrivacyBalanceProof(reader)
		if err != nil {
			return PrivacyInstruction{}, err
		}
	}
	return NewPrivacyTransferInstruction(vmVersion, proof, PrivacyTransferParams{
		Amount:               amount,
		SourceCommitment:     sourceCommitment,
		Nullifier:            nullifier,
		OutputCommitment:     outputCommitment,
		OutputSpendAuthority: outputSpendAuthority,
		OutputEncryptedNote:  encryptedNote,
		OutputAuditRecords:   auditRecords,
		ChangeAmount:         changeAmount,
		ChangeCommitment:     changeCommitment,
		ChangeSpendAuthority: changeSpendAuthority,
		ChangeEncryptedNote:  changeEncryptedNote,
		ChangeAuditRecords:   changeAuditRecords,
		SourceConfidential:   sourceConfidential,
		OutputConfidential:   outputConfidential,
		ChangeConfidential:   changeConfidential,
		BalanceProof:         balanceProof,
	})
}

func readPrivacyAuthorizeAuditInstruction(reader *borsh.Reader, vmVersion uint16, proof []byte) (PrivacyInstruction, error) {
	commitment, err := readPrivacyHash(reader, "audit commitment")
	if err != nil {
		return PrivacyInstruction{}, err
	}
	auditor, err := readPrivacyPublicKey(reader, "audit auditor")
	if err != nil {
		return PrivacyInstruction{}, err
	}
	scopeValue, err := reader.ReadUint8()
	if err != nil {
		return PrivacyInstruction{}, fmt.Errorf("structure: decode audit scope: %w", err)
	}
	expiresAtSlot, err := reader.ReadUint64()
	if err != nil {
		return PrivacyInstruction{}, fmt.Errorf("structure: decode audit expires slot: %w", err)
	}
	auditCiphertext, err := reader.ReadBytes()
	if err != nil {
		return PrivacyInstruction{}, fmt.Errorf("structure: decode audit ciphertext: %w", err)
	}
	return NewPrivacyAuthorizeAuditInstruction(vmVersion, proof, PrivacyAuthorizeAuditParams{
		Commitment:      commitment,
		Auditor:         auditor,
		Scope:           PrivacyAuditScope(scopeValue),
		ExpiresAtSlot:   expiresAtSlot,
		AuditCiphertext: auditCiphertext,
	})
}

func readPrivacyNotes(reader *borsh.Reader, version uint16) ([]PrivacyNoteRecord, error) {
	noteCount, err := reader.ReadUint32()
	if err != nil {
		return nil, fmt.Errorf("structure: decode privacy note count: %w", err)
	}
	if noteCount > MaxPrivacyNotesPerState {
		return nil, fmt.Errorf("%w: note count %d exceeds %d", ErrInvalidPrivacyInstruction, noteCount, MaxPrivacyNotesPerState)
	}
	notes := make([]PrivacyNoteRecord, int(noteCount))
	for noteIndex := range notes {
		note, err := readPrivacyNote(reader, version)
		if err != nil {
			return nil, fmt.Errorf("structure: decode privacy note %d: %w", noteIndex, err)
		}
		notes[noteIndex] = note
	}
	return notes, nil
}

func readPrivacyNote(reader *borsh.Reader, version uint16) (PrivacyNoteRecord, error) {
	commitment, err := readPrivacyHash(reader, "note commitment")
	if err != nil {
		return PrivacyNoteRecord{}, err
	}
	spendAuthority, err := readPrivacyPublicKey(reader, "note spend authority")
	if err != nil {
		return PrivacyNoteRecord{}, err
	}
	amount, err := reader.ReadUint64()
	if err != nil {
		return PrivacyNoteRecord{}, fmt.Errorf("structure: decode note amount: %w", err)
	}
	spent, err := reader.ReadBool()
	if err != nil {
		return PrivacyNoteRecord{}, fmt.Errorf("structure: decode note spent: %w", err)
	}
	spentSlot, err := reader.ReadUint64()
	if err != nil {
		return PrivacyNoteRecord{}, fmt.Errorf("structure: decode note spent slot: %w", err)
	}
	spendNullifier, err := readPrivacyHash(reader, "note spend nullifier")
	if err != nil {
		return PrivacyNoteRecord{}, err
	}
	vmVersion, err := reader.ReadUint16()
	if err != nil {
		return PrivacyNoteRecord{}, fmt.Errorf("structure: decode note vm version: %w", err)
	}
	encryptedNote, err := reader.ReadBytes()
	if err != nil {
		return PrivacyNoteRecord{}, fmt.Errorf("structure: decode note encrypted data: %w", err)
	}
	auditRecords, err := readPrivacyAuditRecords(reader)
	if err != nil {
		return PrivacyNoteRecord{}, err
	}
	var confidential *PrivacyConfidentialOutput
	if version >= PrivacyStateStorageVersion {
		confidential, err = readPrivacyConfidentialOutput(reader)
		if err != nil {
			return PrivacyNoteRecord{}, err
		}
	}
	return PrivacyNoteRecord{
		Commitment:     commitment,
		SpendAuthority: spendAuthority,
		Amount:         amount,
		Spent:          spent,
		SpentSlot:      spentSlot,
		SpendNullifier: spendNullifier,
		VMVersion:      vmVersion,
		EncryptedNote:  encryptedNote,
		AuditRecords:   auditRecords,
		Confidential:   confidential,
	}, nil
}

func readPrivacyNullifiers(reader *borsh.Reader) ([]Hash, error) {
	nullifierCount, err := reader.ReadUint32()
	if err != nil {
		return nil, fmt.Errorf("structure: decode nullifier count: %w", err)
	}
	if nullifierCount > MaxPrivacyNotesPerState {
		return nil, fmt.Errorf("%w: nullifier count %d exceeds %d", ErrInvalidPrivacyInstruction, nullifierCount, MaxPrivacyNotesPerState)
	}
	nullifiers := make([]Hash, int(nullifierCount))
	for nullifierIndex := range nullifiers {
		nullifier, err := readPrivacyHash(reader, "spent nullifier")
		if err != nil {
			return nil, fmt.Errorf("structure: decode nullifier %d: %w", nullifierIndex, err)
		}
		nullifiers[nullifierIndex] = nullifier
	}
	return nullifiers, nil
}

func privacyChangeOutputHashes(amount uint64, encryptedNote []byte, auditRecords []PrivacyAuditRecord) (Hash, Hash, error) {
	if amount == 0 {
		return Hash{}, Hash{}, nil
	}
	encryptedNoteHash, err := NewHash(utils.SHA256(encryptedNote))
	if err != nil {
		return Hash{}, Hash{}, err
	}
	auditRecordsHash, err := hashPrivacyAuditRecords(auditRecords)
	if err != nil {
		return Hash{}, Hash{}, err
	}
	return encryptedNoteHash, auditRecordsHash, nil
}

func writePrivacyChangeOutput(
	writer *borsh.Writer,
	amount uint64,
	commitment Hash,
	spendAuthority PublicKey,
	encryptedNote []byte,
	auditRecords []PrivacyAuditRecord,
) error {
	writer.WriteUint64(amount)
	writer.WriteFixedBytes(commitment[:])
	writer.WriteFixedBytes(spendAuthority[:])
	if err := writer.WriteBytes(encryptedNote); err != nil {
		return err
	}
	return writePrivacyAuditRecords(writer, auditRecords)
}

func readPrivacyChangeOutput(reader *borsh.Reader, field string) (uint64, Hash, PublicKey, []byte, []PrivacyAuditRecord, error) {
	if reader.Remaining() == 0 {
		return 0, Hash{}, PublicKey{}, nil, nil, nil
	}
	amount, err := reader.ReadUint64()
	if err != nil {
		return 0, Hash{}, PublicKey{}, nil, nil, fmt.Errorf("structure: decode %s amount: %w", field, err)
	}
	commitment, err := readPrivacyHash(reader, field+" commitment")
	if err != nil {
		return 0, Hash{}, PublicKey{}, nil, nil, err
	}
	spendAuthority, err := readPrivacyPublicKey(reader, field+" spend authority")
	if err != nil {
		return 0, Hash{}, PublicKey{}, nil, nil, err
	}
	encryptedNote, err := reader.ReadBytes()
	if err != nil {
		return 0, Hash{}, PublicKey{}, nil, nil, fmt.Errorf("structure: decode %s encrypted note: %w", field, err)
	}
	auditRecords, err := readPrivacyAuditRecords(reader)
	if err != nil {
		return 0, Hash{}, PublicKey{}, nil, nil, err
	}
	return amount, commitment, spendAuthority, encryptedNote, auditRecords, nil
}

func writePrivacyConfidentialOutput(writer *borsh.Writer, output *PrivacyConfidentialOutput) error {
	writer.WriteBool(output != nil)
	if output == nil {
		return nil
	}
	if err := writer.WriteBytes(output.Commitment); err != nil {
		return fmt.Errorf("structure: encode confidential commitment: %w", err)
	}
	if err := writer.WriteBytes(output.AmountPublicKey); err != nil {
		return fmt.Errorf("structure: encode confidential amount public key: %w", err)
	}
	if err := writer.WriteBytes(output.AmountCiphertext.NonceCommitment); err != nil {
		return fmt.Errorf("structure: encode confidential amount nonce: %w", err)
	}
	if err := writer.WriteBytes(output.AmountCiphertext.CiphertextPoint); err != nil {
		return fmt.Errorf("structure: encode confidential amount ciphertext: %w", err)
	}
	if err := writePrivacyAmountCiphertextProof(writer, output.AmountProof); err != nil {
		return err
	}
	rangeProofBytes, err := output.RangeProof.MarshalBinary()
	if err != nil {
		return fmt.Errorf("structure: encode confidential range proof: %w", err)
	}
	return writer.WriteBytes(rangeProofBytes)
}

func readPrivacyConfidentialOutput(reader *borsh.Reader) (*PrivacyConfidentialOutput, error) {
	exists, err := reader.ReadBool()
	if err != nil {
		return nil, fmt.Errorf("structure: decode confidential output flag: %w", err)
	}
	if !exists {
		return nil, nil
	}
	output := PrivacyConfidentialOutput{}
	if output.Commitment, err = reader.ReadBytes(); err != nil {
		return nil, fmt.Errorf("structure: decode confidential commitment: %w", err)
	}
	if output.AmountPublicKey, err = reader.ReadBytes(); err != nil {
		return nil, fmt.Errorf("structure: decode confidential amount public key: %w", err)
	}
	if output.AmountCiphertext.NonceCommitment, err = reader.ReadBytes(); err != nil {
		return nil, fmt.Errorf("structure: decode confidential amount nonce: %w", err)
	}
	if output.AmountCiphertext.CiphertextPoint, err = reader.ReadBytes(); err != nil {
		return nil, fmt.Errorf("structure: decode confidential amount ciphertext: %w", err)
	}
	if output.AmountProof, err = readPrivacyAmountCiphertextProof(reader); err != nil {
		return nil, err
	}
	rangeProofBytes, err := reader.ReadBytes()
	if err != nil {
		return nil, fmt.Errorf("structure: decode confidential range proof: %w", err)
	}
	output.RangeProof, err = zk.UnmarshalRangeProofBinary(rangeProofBytes)
	if err != nil {
		return nil, fmt.Errorf("structure: decode confidential range proof: %w", err)
	}
	return &output, nil
}

func writePrivacyBalanceProof(writer *borsh.Writer, proof zk.BalanceProof) error {
	if isZeroBalanceProof(proof) {
		return writer.WriteBytes(nil)
	}
	proofBytes, err := proof.MarshalBinary()
	if err != nil {
		return fmt.Errorf("structure: encode balance proof: %w", err)
	}
	return writer.WriteBytes(proofBytes)
}

func readPrivacyBalanceProof(reader *borsh.Reader) (zk.BalanceProof, error) {
	proofBytes, err := reader.ReadBytes()
	if err != nil {
		return zk.BalanceProof{}, fmt.Errorf("structure: decode balance proof: %w", err)
	}
	if len(proofBytes) == 0 {
		return zk.BalanceProof{}, nil
	}
	proof, err := zk.UnmarshalBalanceProofBinary(proofBytes)
	if err != nil {
		return zk.BalanceProof{}, fmt.Errorf("structure: decode balance proof: %w", err)
	}
	return proof, nil
}

func writePrivacyAmountCiphertextProof(writer *borsh.Writer, proof zk.AmountCiphertextProof) error {
	proofBytes, err := proof.MarshalBinary()
	if err != nil {
		return fmt.Errorf("structure: encode amount ciphertext proof: %w", err)
	}
	return writer.WriteBytes(proofBytes)
}

func readPrivacyAmountCiphertextProof(reader *borsh.Reader) (zk.AmountCiphertextProof, error) {
	proofBytes, err := reader.ReadBytes()
	if err != nil {
		return zk.AmountCiphertextProof{}, fmt.Errorf("structure: decode amount ciphertext proof: %w", err)
	}
	proof, err := zk.UnmarshalAmountCiphertextProofBinary(proofBytes)
	if err != nil {
		return zk.AmountCiphertextProof{}, fmt.Errorf("structure: decode amount ciphertext proof: %w", err)
	}
	return proof, nil
}

func writePrivacyAuditRecords(writer *borsh.Writer, records []PrivacyAuditRecord) error {
	if len(records) > MaxPrivacyAuditRecordsPerNote {
		return fmt.Errorf("%w: audit record count %d exceeds %d", ErrInvalidPrivacyInstruction, len(records), MaxPrivacyAuditRecordsPerNote)
	}
	writer.WriteUint32(uint32(len(records)))
	for _, record := range records {
		writer.WriteFixedBytes(record.Auditor[:])
		writer.WriteUint8(uint8(record.Scope))
		writer.WriteUint64(record.ExpiresAtSlot)
		if err := writer.WriteBytes(record.AuditCiphertext); err != nil {
			return fmt.Errorf("structure: encode audit ciphertext: %w", err)
		}
	}
	return nil
}

func readPrivacyAuditRecords(reader *borsh.Reader) ([]PrivacyAuditRecord, error) {
	recordCount, err := reader.ReadUint32()
	if err != nil {
		return nil, fmt.Errorf("structure: decode audit record count: %w", err)
	}
	if recordCount > MaxPrivacyAuditRecordsPerNote {
		return nil, fmt.Errorf("%w: audit record count %d exceeds %d", ErrInvalidPrivacyInstruction, recordCount, MaxPrivacyAuditRecordsPerNote)
	}
	records := make([]PrivacyAuditRecord, int(recordCount))
	for recordIndex := range records {
		record, err := readPrivacyAuditRecord(reader)
		if err != nil {
			return nil, fmt.Errorf("structure: decode audit record %d: %w", recordIndex, err)
		}
		records[recordIndex] = record
	}
	return records, nil
}

func readPrivacyAuditRecord(reader *borsh.Reader) (PrivacyAuditRecord, error) {
	auditor, err := readPrivacyPublicKey(reader, "audit auditor")
	if err != nil {
		return PrivacyAuditRecord{}, err
	}
	scopeValue, err := reader.ReadUint8()
	if err != nil {
		return PrivacyAuditRecord{}, fmt.Errorf("structure: decode audit scope: %w", err)
	}
	expiresAtSlot, err := reader.ReadUint64()
	if err != nil {
		return PrivacyAuditRecord{}, fmt.Errorf("structure: decode audit expires slot: %w", err)
	}
	auditCiphertext, err := reader.ReadBytes()
	if err != nil {
		return PrivacyAuditRecord{}, fmt.Errorf("structure: decode audit ciphertext: %w", err)
	}
	return PrivacyAuditRecord{
		Auditor:         auditor,
		Scope:           PrivacyAuditScope(scopeValue),
		ExpiresAtSlot:   expiresAtSlot,
		AuditCiphertext: auditCiphertext,
	}, nil
}

func readPrivacyHash(reader *borsh.Reader, field string) (Hash, error) {
	value, err := reader.ReadFixedBytes(HashSize)
	if err != nil {
		return Hash{}, fmt.Errorf("structure: decode %s: %w", field, err)
	}
	hash, err := NewHash(value)
	if err != nil {
		return Hash{}, fmt.Errorf("structure: decode %s: %w", field, err)
	}
	return hash, nil
}

func readPrivacyPublicKey(reader *borsh.Reader, field string) (PublicKey, error) {
	value, err := reader.ReadFixedBytes(PublicKeySize)
	if err != nil {
		return PublicKey{}, fmt.Errorf("structure: decode %s: %w", field, err)
	}
	publicKey, err := NewPublicKey(value)
	if err != nil {
		return PublicKey{}, fmt.Errorf("structure: decode %s: %w", field, err)
	}
	return publicKey, nil
}

func validatePrivacyStateUniqueness(state PrivacyState) error {
	commitments := make(map[Hash]struct{}, len(state.Notes))
	for noteIndex, note := range state.Notes {
		if err := validatePrivacyNote(note); err != nil {
			return fmt.Errorf("structure: privacy note %d: %w", noteIndex, err)
		}
		if _, exists := commitments[note.Commitment]; exists {
			return fmt.Errorf("%w: duplicate commitment", ErrInvalidPrivacyInstruction)
		}
		commitments[note.Commitment] = struct{}{}
	}
	nullifiers := make(map[Hash]struct{}, len(state.SpentNullifiers))
	for _, nullifier := range state.SpentNullifiers {
		if nullifier.IsZero() {
			return fmt.Errorf("%w: zero nullifier", ErrInvalidPrivacyInstruction)
		}
		if _, exists := nullifiers[nullifier]; exists {
			return fmt.Errorf("%w: duplicate nullifier", ErrInvalidPrivacyInstruction)
		}
		nullifiers[nullifier] = struct{}{}
	}
	return nil
}

func validatePrivacyStateMerkleRoot(state PrivacyState) error {
	merkleRoot, err := ComputePrivacyMerkleRoot(state.Notes)
	if err != nil {
		return err
	}
	if state.MerkleRoot != merkleRoot {
		return fmt.Errorf("%w: privacy merkle root mismatch", ErrInvalidPrivacyInstruction)
	}
	return nil
}

func legacyPrivacyStateLiability(notes []PrivacyNoteRecord) uint64 {
	total := uint64(0)
	for _, note := range notes {
		if note.Spent {
			continue
		}
		if ^uint64(0)-total < note.Amount {
			return ^uint64(0)
		}
		total += note.Amount
	}
	return total
}

func validatePrivacyNote(note PrivacyNoteRecord) error {
	if note.Commitment.IsZero() {
		return fmt.Errorf("%w: zero commitment", ErrInvalidPrivacyInstruction)
	}
	if note.SpendAuthority.IsZero() {
		return fmt.Errorf("%w: zero spend authority", ErrInvalidPrivacyInstruction)
	}
	if note.Amount == 0 && note.Confidential == nil {
		return fmt.Errorf("%w: note amount cannot be zero", ErrInvalidPrivacyInstruction)
	}
	if note.Confidential != nil {
		if err := validatePrivacyConfidentialOutput(note.Confidential); err != nil {
			return err
		}
	}
	if note.Spent {
		if note.SpentSlot == 0 || note.SpendNullifier.IsZero() {
			return fmt.Errorf("%w: spent note missing slot or nullifier", ErrInvalidPrivacyInstruction)
		}
	}
	if !note.Spent && (note.SpentSlot != 0 || !note.SpendNullifier.IsZero()) {
		return fmt.Errorf("%w: unspent note has spend metadata", ErrInvalidPrivacyInstruction)
	}
	if err := validateEncryptedNote(note.EncryptedNote); err != nil {
		return err
	}
	return validatePrivacyAuditRecords(note.AuditRecords)
}

func validatePrivacyDepositParams(params *PrivacyDepositParams) error {
	if params == nil {
		return fmt.Errorf("%w: deposit params are nil", ErrInvalidPrivacyInstruction)
	}
	if params.Amount == 0 {
		return fmt.Errorf("%w: deposit amount cannot be zero", ErrInvalidPrivacyInstruction)
	}
	if params.Commitment.IsZero() {
		return fmt.Errorf("%w: deposit commitment is zero", ErrInvalidPrivacyInstruction)
	}
	if params.SpendAuthority.IsZero() {
		return fmt.Errorf("%w: deposit spend authority is zero", ErrInvalidPrivacyInstruction)
	}
	if err := validateEncryptedNote(params.EncryptedNote); err != nil {
		return err
	}
	if err := validatePrivacyAuditRecords(params.AuditRecords); err != nil {
		return err
	}
	if params.Confidential == nil {
		return nil
	}
	if err := validatePrivacyConfidentialOutput(params.Confidential); err != nil {
		return err
	}
	return validatePrivacyBalanceProof(params.AmountProof, "deposit amount proof")
}

func validatePrivacyWithdrawParams(params *PrivacyWithdrawParams) error {
	if params == nil {
		return fmt.Errorf("%w: withdraw params are nil", ErrInvalidPrivacyInstruction)
	}
	if params.Amount == 0 {
		return fmt.Errorf("%w: withdraw amount cannot be zero", ErrInvalidPrivacyInstruction)
	}
	if params.SourceCommitment.IsZero() || params.Nullifier.IsZero() {
		return fmt.Errorf("%w: withdraw commitment or nullifier is zero", ErrInvalidPrivacyInstruction)
	}
	if err := validatePrivacyAuditRecords(params.AuditRecords); err != nil {
		return err
	}
	if err := validatePrivacyChangeOutput(
		params.ChangeAmount,
		params.ChangeCommitment,
		params.ChangeSpendAuthority,
		params.ChangeEncryptedNote,
		params.ChangeAuditRecords,
	); err != nil {
		return err
	}
	if len(params.SourceConfidential) == 0 && params.ChangeConfidential == nil && isZeroBalanceProof(params.BalanceProof) {
		return nil
	}
	if len(params.SourceConfidential) == 0 {
		return fmt.Errorf("%w: withdraw confidential source is empty", ErrInvalidPrivacyInstruction)
	}
	if params.ChangeAmount == 0 && params.ChangeConfidential != nil {
		return fmt.Errorf("%w: zero withdraw change has confidential output", ErrInvalidPrivacyInstruction)
	}
	if params.ChangeAmount > 0 {
		if params.ChangeConfidential == nil {
			return fmt.Errorf("%w: withdraw change confidential output is missing", ErrInvalidPrivacyInstruction)
		}
		if err := validatePrivacyConfidentialOutput(params.ChangeConfidential); err != nil {
			return err
		}
	}
	return validatePrivacyBalanceProof(params.BalanceProof, "withdraw balance proof")
}

func validatePrivacyTransferParams(params *PrivacyTransferParams) error {
	if params == nil {
		return fmt.Errorf("%w: transfer params are nil", ErrInvalidPrivacyInstruction)
	}
	if params.Amount == 0 {
		return fmt.Errorf("%w: transfer amount cannot be zero", ErrInvalidPrivacyInstruction)
	}
	if params.SourceCommitment.IsZero() || params.Nullifier.IsZero() || params.OutputCommitment.IsZero() {
		return fmt.Errorf("%w: transfer commitment or nullifier is zero", ErrInvalidPrivacyInstruction)
	}
	if params.OutputSpendAuthority.IsZero() {
		return fmt.Errorf("%w: transfer output spend authority is zero", ErrInvalidPrivacyInstruction)
	}
	if err := validateEncryptedNote(params.OutputEncryptedNote); err != nil {
		return err
	}
	if params.ChangeAmount > 0 && params.OutputCommitment == params.ChangeCommitment {
		return fmt.Errorf("%w: transfer output and change commitment must differ", ErrInvalidPrivacyInstruction)
	}
	if err := validatePrivacyAuditRecords(params.OutputAuditRecords); err != nil {
		return err
	}
	if err := validatePrivacyChangeOutput(
		params.ChangeAmount,
		params.ChangeCommitment,
		params.ChangeSpendAuthority,
		params.ChangeEncryptedNote,
		params.ChangeAuditRecords,
	); err != nil {
		return err
	}
	if len(params.SourceConfidential) == 0 && params.OutputConfidential == nil && params.ChangeConfidential == nil && isZeroBalanceProof(params.BalanceProof) {
		return nil
	}
	if len(params.SourceConfidential) == 0 || params.OutputConfidential == nil {
		return fmt.Errorf("%w: transfer confidential source or output is missing", ErrInvalidPrivacyInstruction)
	}
	if err := validatePrivacyConfidentialOutput(params.OutputConfidential); err != nil {
		return err
	}
	if params.ChangeAmount == 0 && params.ChangeConfidential != nil {
		return fmt.Errorf("%w: zero transfer change has confidential output", ErrInvalidPrivacyInstruction)
	}
	if params.ChangeAmount > 0 {
		if params.ChangeConfidential == nil {
			return fmt.Errorf("%w: transfer change confidential output is missing", ErrInvalidPrivacyInstruction)
		}
		if err := validatePrivacyConfidentialOutput(params.ChangeConfidential); err != nil {
			return err
		}
	}
	return validatePrivacyBalanceProof(params.BalanceProof, "transfer balance proof")
}

func validatePrivacyConfidentialOutput(output *PrivacyConfidentialOutput) error {
	if output == nil {
		return fmt.Errorf("%w: confidential output is nil", ErrInvalidPrivacyInstruction)
	}
	if err := zk.VerifyAmountCiphertextProof(output.AmountPublicKey, output.Commitment, output.AmountCiphertext, output.AmountProof); err != nil {
		return fmt.Errorf("%w: confidential amount proof: %w", ErrInvalidPrivacyInstruction, err)
	}
	if err := output.RangeProof.Verify(); err != nil {
		return fmt.Errorf("%w: confidential range proof: %w", ErrInvalidPrivacyInstruction, err)
	}
	if string(output.RangeProof.Commitment) != string(output.Commitment) {
		return fmt.Errorf("%w: confidential range commitment mismatch", ErrInvalidPrivacyInstruction)
	}
	return nil
}

func validatePrivacyBalanceProof(proof zk.BalanceProof, field string) error {
	if isZeroBalanceProof(proof) {
		return fmt.Errorf("%w: %s is missing", ErrInvalidPrivacyInstruction, field)
	}
	if _, err := proof.MarshalBinary(); err != nil {
		return fmt.Errorf("%w: %s: %w", ErrInvalidPrivacyInstruction, field, err)
	}
	return nil
}

func isZeroBalanceProof(proof zk.BalanceProof) bool {
	return proof.Version == 0 && len(proof.NonceCommitment) == 0 && len(proof.Response) == 0
}

func validatePrivacyChangeOutput(
	amount uint64,
	commitment Hash,
	spendAuthority PublicKey,
	encryptedNote []byte,
	auditRecords []PrivacyAuditRecord,
) error {
	if amount == 0 {
		if !commitment.IsZero() || !spendAuthority.IsZero() || len(encryptedNote) > 0 || len(auditRecords) > 0 {
			return fmt.Errorf("%w: zero change amount has change output data", ErrInvalidPrivacyInstruction)
		}
		return nil
	}
	if commitment.IsZero() || spendAuthority.IsZero() {
		return fmt.Errorf("%w: change commitment or spend authority is zero", ErrInvalidPrivacyInstruction)
	}
	if err := validateEncryptedNote(encryptedNote); err != nil {
		return err
	}
	return validatePrivacyAuditRecords(auditRecords)
}

func validatePrivacyAuthorizeAuditParams(params *PrivacyAuthorizeAuditParams) error {
	if params == nil {
		return fmt.Errorf("%w: authorize audit params are nil", ErrInvalidPrivacyInstruction)
	}
	if params.Commitment.IsZero() {
		return fmt.Errorf("%w: audit commitment is zero", ErrInvalidPrivacyInstruction)
	}
	return validatePrivacyAuditRecord(PrivacyAuditRecord{
		Auditor:         params.Auditor,
		Scope:           params.Scope,
		ExpiresAtSlot:   params.ExpiresAtSlot,
		AuditCiphertext: params.AuditCiphertext,
	})
}

func validateEncryptedNote(value []byte) error {
	if len(value) == 0 {
		return fmt.Errorf("%w: encrypted note cannot be empty", ErrInvalidPrivacyInstruction)
	}
	if len(value) > MaxPrivacyEncryptedNoteBytes {
		return fmt.Errorf("%w: encrypted note length %d exceeds %d", ErrInvalidPrivacyInstruction, len(value), MaxPrivacyEncryptedNoteBytes)
	}
	return nil
}

func validatePrivacyAuditRecords(records []PrivacyAuditRecord) error {
	if len(records) > MaxPrivacyAuditRecordsPerNote {
		return fmt.Errorf("%w: audit record count %d exceeds %d", ErrInvalidPrivacyInstruction, len(records), MaxPrivacyAuditRecordsPerNote)
	}
	seenRecords := make(map[privacyAuditRecordKey]struct{}, len(records))
	for recordIndex, record := range records {
		if err := validatePrivacyAuditRecord(record); err != nil {
			return fmt.Errorf("structure: audit record %d: %w", recordIndex, err)
		}
		key := privacyAuditKey(record)
		if _, exists := seenRecords[key]; exists {
			return fmt.Errorf("%w: duplicate audit record", ErrInvalidPrivacyInstruction)
		}
		seenRecords[key] = struct{}{}
	}
	return nil
}

func validatePrivacyAuditRecordsForSlot(records []PrivacyAuditRecord, currentSlot uint64) error {
	if err := validatePrivacyAuditRecords(records); err != nil {
		return err
	}
	for _, record := range records {
		if record.ExpiresAtSlot != 0 && record.ExpiresAtSlot <= currentSlot {
			return fmt.Errorf("%w: audit authorization already expired", ErrInvalidPrivacyInstruction)
		}
	}
	return nil
}

func validatePrivacyAuditRecordForSlot(record PrivacyAuditRecord, currentSlot uint64) error {
	if err := validatePrivacyAuditRecord(record); err != nil {
		return err
	}
	if record.ExpiresAtSlot != 0 && record.ExpiresAtSlot <= currentSlot {
		return fmt.Errorf("%w: audit authorization already expired", ErrInvalidPrivacyInstruction)
	}
	return nil
}

func validatePrivacyAuditRecord(record PrivacyAuditRecord) error {
	if record.Auditor.IsZero() {
		return fmt.Errorf("%w: audit auditor is zero", ErrInvalidPrivacyInstruction)
	}
	if !record.Scope.IsValid() {
		return fmt.Errorf("%w: invalid audit scope %d", ErrInvalidPrivacyInstruction, record.Scope)
	}
	if len(record.AuditCiphertext) == 0 {
		return fmt.Errorf("%w: audit ciphertext cannot be empty", ErrInvalidPrivacyInstruction)
	}
	if len(record.AuditCiphertext) > MaxPrivacyAuditCiphertextBytes {
		return fmt.Errorf("%w: audit ciphertext length %d exceeds %d", ErrInvalidPrivacyInstruction, len(record.AuditCiphertext), MaxPrivacyAuditCiphertextBytes)
	}
	return nil
}

func hasPrivacyAuditRecord(records []PrivacyAuditRecord, target PrivacyAuditRecord) bool {
	targetKey := privacyAuditKey(target)
	for _, record := range records {
		if privacyAuditKey(record) == targetKey {
			return true
		}
	}
	return false
}

func privacyAuditKey(record PrivacyAuditRecord) privacyAuditRecordKey {
	ciphertextHash, err := NewHash(utils.SHA256(record.AuditCiphertext))
	if err != nil {
		return privacyAuditRecordKey{Auditor: record.Auditor, Scope: record.Scope}
	}
	return privacyAuditRecordKey{Auditor: record.Auditor, Scope: record.Scope, CiphertextHash: ciphertextHash}
}

func clonePrivacyAuditRecords(records []PrivacyAuditRecord) []PrivacyAuditRecord {
	if records == nil {
		return nil
	}
	cloned := make([]PrivacyAuditRecord, len(records))
	for index, record := range records {
		cloned[index] = clonePrivacyAuditRecord(record)
	}
	return cloned
}

func clonePrivacyAuditRecord(record PrivacyAuditRecord) PrivacyAuditRecord {
	return PrivacyAuditRecord{
		Auditor:         record.Auditor,
		Scope:           record.Scope,
		ExpiresAtSlot:   record.ExpiresAtSlot,
		AuditCiphertext: utils.CloneBytes(record.AuditCiphertext),
	}
}

func clonePrivacyConfidentialOutput(output *PrivacyConfidentialOutput) *PrivacyConfidentialOutput {
	if output == nil {
		return nil
	}
	cloned := *output
	cloned.Commitment = utils.CloneBytes(output.Commitment)
	cloned.AmountPublicKey = utils.CloneBytes(output.AmountPublicKey)
	cloned.AmountCiphertext = cloneElGamalCiphertext(output.AmountCiphertext)
	cloned.AmountProof = cloneAmountCiphertextProof(output.AmountProof)
	cloned.RangeProof = cloneRangeProof(output.RangeProof)
	return &cloned
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

func cloneRangeProof(proof zk.RangeProof) zk.RangeProof {
	cloned := zk.RangeProof{
		Version:    proof.Version,
		Bits:       proof.Bits,
		Commitment: utils.CloneBytes(proof.Commitment),
		BitProofs:  make([]zk.BitProof, len(proof.BitProofs)),
	}
	cloned.BitCommitments = make([][]byte, len(proof.BitCommitments))
	for index := range proof.BitCommitments {
		cloned.BitCommitments[index] = utils.CloneBytes(proof.BitCommitments[index])
	}
	for index, bitProof := range proof.BitProofs {
		cloned.BitProofs[index] = zk.BitProof{
			Nonce0:     utils.CloneBytes(bitProof.Nonce0),
			Nonce1:     utils.CloneBytes(bitProof.Nonce1),
			Challenge0: utils.CloneBytes(bitProof.Challenge0),
			Challenge1: utils.CloneBytes(bitProof.Challenge1),
			Response0:  utils.CloneBytes(bitProof.Response0),
			Response1:  utils.CloneBytes(bitProof.Response1),
		}
	}
	return cloned
}

func cloneBalanceProof(proof zk.BalanceProof) zk.BalanceProof {
	return zk.BalanceProof{
		Version:         proof.Version,
		NonceCommitment: utils.CloneBytes(proof.NonceCommitment),
		Response:        utils.CloneBytes(proof.Response),
	}
}

// ComputePrivacyMerkleRoot 鐠侊紕鐣婚梾鎰潌 Note 閺?+ 閸忋劏濡悙鍦暏绾喖鐣鹃幀褎鐗撮弽锟犵崣閹佃儻顕崣顏囨嫹閸旂姳绗夌弧鈩冩暭閵?
func ComputePrivacyMerkleRoot(notes []PrivacyNoteRecord) (Hash, error) {
	if len(notes) == 0 {
		return Hash{}, nil
	}
	leaves := make([]Hash, len(notes))
	for noteIndex, note := range notes {
		leaf, err := privacyMerkleLeaf(note)
		if err != nil {
			return Hash{}, fmt.Errorf("structure: privacy merkle leaf %d: %w", noteIndex, err)
		}
		leaves[noteIndex] = leaf
	}
	return privacyMerkleRoot(leaves)
}

func privacyMerkleLeaf(note PrivacyNoteRecord) (Hash, error) {
	writer := borsh.NewWriter(MaxPrivacyInstructionBytes)
	writer.WriteFixedBytes([]byte(privacyMerkleLeafDomainV1))
	if note.Confidential != nil {
		if err := writer.WriteBytes(note.Confidential.Commitment); err != nil {
			return Hash{}, err
		}
	} else {
		writer.WriteFixedBytes(note.Commitment[:])
	}
	writer.WriteFixedBytes(note.SpendAuthority[:])
	return NewHash(utils.SHA256(writer.Bytes()))
}

func privacyMerkleRoot(leaves []Hash) (Hash, error) {
	currentLevel := append([]Hash(nil), leaves...)
	for len(currentLevel) > 1 {
		nextLevel := make([]Hash, 0, (len(currentLevel)+1)/2)
		for index := 0; index < len(currentLevel); index += 2 {
			rightIndex := index + 1
			if rightIndex >= len(currentLevel) {
				rightIndex = index
			}
			parent, err := privacyMerkleParent(currentLevel[index], currentLevel[rightIndex])
			if err != nil {
				return Hash{}, err
			}
			nextLevel = append(nextLevel, parent)
		}
		currentLevel = nextLevel
	}
	return currentLevel[0], nil
}

func privacyMerkleParent(left Hash, right Hash) (Hash, error) {
	writer := borsh.NewWriter(MaxPrivacyInstructionBytes)
	writer.WriteFixedBytes([]byte(privacyMerkleNodeDomainV1))
	writer.WriteFixedBytes(left[:])
	writer.WriteFixedBytes(right[:])
	return NewHash(utils.SHA256(writer.Bytes()))
}

// IsValid 校验审计范围 + 防止链上出现未定义权限语义。
func (scope PrivacyAuditScope) IsValid() bool {
	return scope == PrivacyAuditScopeOwner ||
		scope == PrivacyAuditScopeBusiness ||
		scope == PrivacyAuditScopeRegulatory
}
