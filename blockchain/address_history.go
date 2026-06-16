package blockchain

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"

	"solana_golang/consensus"
	"solana_golang/database"
	"solana_golang/programs/stake"
	"solana_golang/structure"
)

const (
	addressHistoryDefaultLimit  = 20
	addressHistoryMaxLimit      = 100
	addressHistoryTxIndexBytes  = 2
	addressHistoryEffectBytes   = 1
	addressHistoryCursorPrefix  = "addr-history:"
	addressHistoryScopePublic   = "transparent_balance_changes_only"
)

type AddressHistoryDirection string

const (
	AddressHistoryDirectionIncoming AddressHistoryDirection = "incoming"
	AddressHistoryDirectionOutgoing AddressHistoryDirection = "outgoing"
)

type AddressHistoryKind string

const (
	AddressHistoryKindTransfer          AddressHistoryKind = "transfer"
	AddressHistoryKindPrivacyDeposit    AddressHistoryKind = "privacy_deposit"
	AddressHistoryKindPrivacyWithdraw   AddressHistoryKind = "privacy_withdraw"
	AddressHistoryKindValidatorRegister AddressHistoryKind = "validator_register"
	AddressHistoryKindStakeDeposit      AddressHistoryKind = "stake_deposit"
	AddressHistoryKindStakeWithdraw     AddressHistoryKind = "stake_withdraw"
	AddressHistoryKindSlash             AddressHistoryKind = "slash"
)

// AddressHistoryRecord 描述透明余额变化历史 + 只输出公开账户层可见的金额变化。
type AddressHistoryRecord struct {
	TransactionID       string
	Direction           AddressHistoryDirection
	Kind                AddressHistoryKind
	Counterparty        string
	AmountLamports      uint64
	BlockHeight         uint64
	Slot                uint64
	BlockHash           string
	SubmitTimeUnixMilli int64
}

// AddressHistoryPage 描述地址历史分页结果 + 游标保持为后端内部不透明字节串。
type AddressHistoryPage struct {
	Scope      string
	Records    []AddressHistoryRecord
	NextCursor string
	HasMore    bool
}

// PrivacyBalanceSummary 聚合隐私 note 余额 + 把可花费余额和托管余额分开展示。
type PrivacyBalanceSummary struct {
	AvailableLamports uint64
	EscrowLamports    uint64
	SpendableNoteCount int
	SpentNoteCount     int
	OwnedNoteCount     int
	StateNoteCount     int
}

type addressHistoryEffect struct {
	Address        structure.PublicKey
	Direction      AddressHistoryDirection
	Kind           AddressHistoryKind
	Counterparty   string
	AmountLamports uint64
}

type addressHistoryIndexedEntry struct {
	Key   []byte
	Value []byte
}

type addressHistoryStoredRecord struct {
	TransactionID       string                 `json:"transaction_id"`
	Direction           AddressHistoryDirection `json:"direction"`
	Kind                AddressHistoryKind      `json:"kind"`
	Counterparty        string                 `json:"counterparty,omitempty"`
	AmountLamports      uint64                 `json:"amount_lamports"`
	BlockHeight         uint64                 `json:"block_height"`
	Slot                uint64                 `json:"slot"`
	BlockHash           string                 `json:"block_hash"`
	SubmitTimeUnixMilli int64                  `json:"submit_time_unix_milli"`
}

func (ledger *Ledger) AddressHistory(address structure.PublicKey, cursor string, limit int) (AddressHistoryPage, error) {
	ledger.mutex.RLock()
	defer ledger.mutex.RUnlock()
	if ledger.closed {
		return AddressHistoryPage{}, ErrLedgerClosed
	}
	if ledger.db == nil {
		return AddressHistoryPage{Scope: addressHistoryScopePublic}, nil
	}

	pageSize := normalizeAddressHistoryLimit(limit)
	prefix := addressHistoryPrefix(address)
	lastKey, err := decodeAddressHistoryCursor(prefix, cursor)
	if err != nil {
		return AddressHistoryPage{}, err
	}
	page, err := ledger.db.PageKeyByPrefixReverse(database.TableAddrToTx, prefix, pageSize, lastKey)
	if err != nil {
		return AddressHistoryPage{}, fmt.Errorf("blockchain: page address history: %w", err)
	}
	if len(page.Data) == 0 {
		return AddressHistoryPage{Scope: addressHistoryScopePublic}, nil
	}
	values, err := ledger.db.BatchGet(database.TableAddrToTx, page.Data)
	if err != nil {
		return AddressHistoryPage{}, fmt.Errorf("blockchain: load address history page: %w", err)
	}

	records := make([]AddressHistoryRecord, 0, len(values))
	for index, value := range values {
		if len(value) == 0 {
			return AddressHistoryPage{}, fmt.Errorf("blockchain: address history value missing for key %s", base64.StdEncoding.EncodeToString(page.Data[index]))
		}
		record, err := unmarshalAddressHistoryStoredRecord(value)
		if err != nil {
			return AddressHistoryPage{}, err
		}
		records = append(records, AddressHistoryRecord{
			TransactionID:       record.TransactionID,
			Direction:           record.Direction,
			Kind:                record.Kind,
			Counterparty:        record.Counterparty,
			AmountLamports:      record.AmountLamports,
			BlockHeight:         record.BlockHeight,
			Slot:                record.Slot,
			BlockHash:           record.BlockHash,
			SubmitTimeUnixMilli: record.SubmitTimeUnixMilli,
		})
	}

	nextCursor := ""
	if !page.IsLastPage && len(page.LastKey) > 0 {
		nextCursor = encodeAddressHistoryCursor(page.LastKey)
	}
	return AddressHistoryPage{
		Scope:      addressHistoryScopePublic,
		Records:    records,
		NextCursor: nextCursor,
		HasMore:    !page.IsLastPage,
	}, nil
}

func (ledger *Ledger) PrivacyBalance(stateAddress structure.PublicKey, spendAuthority structure.PublicKey) (PrivacyBalanceSummary, error) {
	account, found, err := ledger.Account(stateAddress)
	if err != nil {
		return PrivacyBalanceSummary{}, err
	}
	if !found {
		return PrivacyBalanceSummary{}, nil
	}
	if account.Owner != structure.DefaultBuiltinProgramIDs.Privacy {
		return PrivacyBalanceSummary{}, fmt.Errorf("blockchain: account is not a privacy state")
	}
	state, err := structure.UnmarshalPrivacyStateBinary(account.Data)
	if err != nil {
		return PrivacyBalanceSummary{}, fmt.Errorf("blockchain: decode privacy state: %w", err)
	}
	summary := PrivacyBalanceSummary{
		EscrowLamports: account.Lamports,
		StateNoteCount: len(state.Notes),
	}
	for _, note := range state.Notes {
		if note.SpendAuthority != spendAuthority {
			continue
		}
		summary.OwnedNoteCount++
		if note.Spent {
			summary.SpentNoteCount++
			continue
		}
		if ^uint64(0)-summary.AvailableLamports < note.Amount {
			return PrivacyBalanceSummary{}, fmt.Errorf("blockchain: privacy balance overflow")
		}
		summary.AvailableLamports += note.Amount
		summary.SpendableNoteCount++
	}
	return summary, nil
}

func ensureAddressHistoryIndex(db database.Database, head Head) error {
	if db == nil || head.Height == 0 {
		return nil
	}
	isEmpty, err := db.IsEmpty(database.TableAddrToTx)
	if err != nil {
		return fmt.Errorf("blockchain: check address history index: %w", err)
	}
	if !isEmpty {
		return nil
	}

	readTx, err := db.BeginReadTransaction()
	if err != nil {
		return fmt.Errorf("blockchain: begin address history rebuild snapshot: %w", err)
	}
	operations, err := rebuildAddressHistoryIndexOps(readTx, head)
	closeErr := readTx.Close()
	if err != nil {
		if closeErr != nil {
			return fmt.Errorf("%w; close snapshot: %v", err, closeErr)
		}
		return err
	}
	if closeErr != nil {
		return fmt.Errorf("blockchain: close address history rebuild snapshot: %w", closeErr)
	}
	if len(operations) == 0 {
		return nil
	}
	if err := db.DataTransaction(operations); err != nil {
		return fmt.Errorf("blockchain: persist rebuilt address history index: %w", err)
	}
	return nil
}

func rebuildAddressHistoryIndexOps(readTx database.ReadTransaction, head Head) ([]database.DBOperation, error) {
	operations := make([]database.DBOperation, 0)
	for height := uint64(1); height <= head.Height; height++ {
		blockHashBytes, err := readTx.Get(database.TableHeightToHash, uint64Key(height))
		if err != nil {
			return nil, fmt.Errorf("blockchain: read height index for address history rebuild: %w", err)
		}
		if len(blockHashBytes) == 0 {
			continue
		}
		blockHash, err := structure.NewHash(blockHashBytes)
		if err != nil {
			return nil, err
		}
		proposal, err := loadProposalByHash(readTx, blockHash)
		if err != nil {
			return nil, err
		}
		if err := appendAddressHistoryIndexOps(&operations, proposal, blockHash); err != nil {
			return nil, err
		}
	}
	return operations, nil
}

func appendAddressHistoryIndexOps(operations *[]database.DBOperation, proposal consensus.BlockProposal, blockHash structure.Hash) error {
	entries, err := buildAddressHistoryEntries(proposal, blockHash)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		*operations = append(*operations, database.NewUpdateOperation(database.TableAddrToTx, entry.Key, entry.Value))
	}
	return nil
}

func appendAddressHistoryDeleteOps(operations *[]database.DBOperation, proposal consensus.BlockProposal, blockHash structure.Hash) error {
	entries, err := buildAddressHistoryEntries(proposal, blockHash)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		*operations = append(*operations, database.NewDeleteOperation(database.TableAddrToTx, entry.Key))
	}
	return nil
}

func buildAddressHistoryEntries(proposal consensus.BlockProposal, blockHash structure.Hash) ([]addressHistoryIndexedEntry, error) {
	if len(proposal.Transactions) > int(^uint16(0)) {
		return nil, fmt.Errorf("blockchain: transaction count %d exceeds address history index limit", len(proposal.Transactions))
	}
	entries := make([]addressHistoryIndexedEntry, 0, len(proposal.Transactions)*2)
	for transactionIndex, transaction := range proposal.Transactions {
		transactionID, err := transaction.TxIDString()
		if err != nil {
			return nil, fmt.Errorf("blockchain: calculate address history transaction id: %w", err)
		}
		effects, err := extractAddressHistoryEffects(transaction)
		if err != nil {
			return nil, err
		}
		for effectIndex, effect := range effects {
			if effectIndex > int(^uint8(0)) {
				return nil, fmt.Errorf("blockchain: address history effect count exceeds limit for %s", transactionID)
			}
			recordBytes, err := marshalAddressHistoryStoredRecord(addressHistoryStoredRecord{
				TransactionID:       transactionID,
				Direction:           effect.Direction,
				Kind:                effect.Kind,
				Counterparty:        effect.Counterparty,
				AmountLamports:      effect.AmountLamports,
				BlockHeight:         proposal.Header.Height,
				Slot:                proposal.Header.Slot,
				BlockHash:           blockHash.String(),
				SubmitTimeUnixMilli: transaction.SubmitTime,
			})
			if err != nil {
				return nil, err
			}
			entries = append(entries, addressHistoryIndexedEntry{
				Key: addressHistoryKey(effect.Address, proposal.Header.Height, uint16(transactionIndex), uint8(effectIndex), transactionID),
				Value: recordBytes,
			})
		}
	}
	return entries, nil
}

func extractAddressHistoryEffects(transaction structure.Transaction) ([]addressHistoryEffect, error) {
	if len(transaction.Instructions) == 0 {
		return nil, nil
	}
	effects := make([]addressHistoryEffect, 0, len(transaction.Instructions)*2)
	for _, instruction := range transaction.Instructions {
		programID, accountKeys, err := resolveInstructionAccounts(transaction, instruction)
		if err != nil {
			return nil, err
		}
		switch programID {
		case structure.DefaultBuiltinProgramIDs.System:
			systemInstruction, err := structure.UnmarshalSystemInstructionBinary(instruction.Data)
			if err != nil {
				return nil, fmt.Errorf("blockchain: decode system instruction for address history: %w", err)
			}
			effects = append(effects, systemInstructionEffects(systemInstruction, accountKeys)...)
		case structure.DefaultBuiltinProgramIDs.Privacy:
			privacyInstruction, err := structure.UnmarshalPrivacyInstructionBinary(instruction.Data)
			if err != nil {
				return nil, fmt.Errorf("blockchain: decode privacy instruction for address history: %w", err)
			}
			effects = append(effects, privacyInstructionEffects(privacyInstruction, accountKeys)...)
		case structure.DefaultBuiltinProgramIDs.Stake:
			stakeInstruction, err := stake.UnmarshalInstructionBinary(instruction.Data)
			if err != nil {
				return nil, fmt.Errorf("blockchain: decode stake instruction for address history: %w", err)
			}
			effects = append(effects, stakeInstructionEffects(stakeInstruction, accountKeys)...)
		}
	}
	return effects, nil
}

func resolveInstructionAccounts(transaction structure.Transaction, instruction structure.CompiledInstruction) (structure.PublicKey, []structure.PublicKey, error) {
	if int(instruction.ProgramIDIndex) >= len(transaction.Accounts) {
		return structure.PublicKey{}, nil, fmt.Errorf("blockchain: instruction program index %d out of range", instruction.ProgramIDIndex)
	}
	accountKeys := make([]structure.PublicKey, 0, len(instruction.AccountIndexes))
	for _, accountIndex := range instruction.AccountIndexes {
		if int(accountIndex) >= len(transaction.Accounts) {
			return structure.PublicKey{}, nil, fmt.Errorf("blockchain: instruction account index %d out of range", accountIndex)
		}
		accountKeys = append(accountKeys, transaction.Accounts[accountIndex].PublicKey)
	}
	return transaction.Accounts[instruction.ProgramIDIndex].PublicKey, accountKeys, nil
}

func systemInstructionEffects(instruction structure.SystemInstruction, accountKeys []structure.PublicKey) []addressHistoryEffect {
	if instruction.Type != structure.SystemInstructionTransfer || len(accountKeys) < 2 {
		return nil
	}
	source := accountKeys[0]
	destination := accountKeys[1]
	return []addressHistoryEffect{
		{
			Address:        source,
			Direction:      AddressHistoryDirectionOutgoing,
			Kind:           AddressHistoryKindTransfer,
			Counterparty:   destination.String(),
			AmountLamports: instruction.Transfer.Lamports,
		},
		{
			Address:        destination,
			Direction:      AddressHistoryDirectionIncoming,
			Kind:           AddressHistoryKindTransfer,
			Counterparty:   source.String(),
			AmountLamports: instruction.Transfer.Lamports,
		},
	}
}

func privacyInstructionEffects(instruction structure.PrivacyInstruction, accountKeys []structure.PublicKey) []addressHistoryEffect {
	switch instruction.Type {
	case structure.PrivacyInstructionDeposit:
		if instruction.Deposit == nil || len(accountKeys) < 2 {
			return nil
		}
		return []addressHistoryEffect{{
			Address:        accountKeys[0],
			Direction:      AddressHistoryDirectionOutgoing,
			Kind:           AddressHistoryKindPrivacyDeposit,
			Counterparty:   accountKeys[1].String(),
			AmountLamports: instruction.Deposit.Amount,
		}}
	case structure.PrivacyInstructionWithdraw:
		if instruction.Withdraw == nil || len(accountKeys) < 2 {
			return nil
		}
		return []addressHistoryEffect{{
			Address:        accountKeys[1],
			Direction:      AddressHistoryDirectionIncoming,
			Kind:           AddressHistoryKindPrivacyWithdraw,
			Counterparty:   accountKeys[0].String(),
			AmountLamports: instruction.Withdraw.Amount,
		}}
	default:
		return nil
	}
}

func stakeInstructionEffects(instruction stake.Instruction, accountKeys []structure.PublicKey) []addressHistoryEffect {
	if len(accountKeys) < 2 {
		return nil
	}
	stakerAddress := accountKeys[0]
	validatorAddress := accountKeys[1]
	switch instruction.Type {
	case stake.InstructionRegisterValidator:
		return twoSidedAddressHistoryEffects(stakerAddress, validatorAddress, AddressHistoryKindValidatorRegister, instruction.Amount)
	case stake.InstructionStake:
		return twoSidedAddressHistoryEffects(stakerAddress, validatorAddress, AddressHistoryKindStakeDeposit, instruction.Amount)
	case stake.InstructionSlashValidator:
		return []addressHistoryEffect{{
			Address:        validatorAddress,
			Direction:      AddressHistoryDirectionOutgoing,
			Kind:           AddressHistoryKindSlash,
			Counterparty:   "burn",
			AmountLamports: instruction.Amount,
		}}
	default:
		return nil
	}
}

func twoSidedAddressHistoryEffects(source structure.PublicKey, destination structure.PublicKey, kind AddressHistoryKind, amount uint64) []addressHistoryEffect {
	return []addressHistoryEffect{
		{
			Address:        source,
			Direction:      AddressHistoryDirectionOutgoing,
			Kind:           kind,
			Counterparty:   destination.String(),
			AmountLamports: amount,
		},
		{
			Address:        destination,
			Direction:      AddressHistoryDirectionIncoming,
			Kind:           kind,
			Counterparty:   source.String(),
			AmountLamports: amount,
		},
	}
}

func normalizeAddressHistoryLimit(limit int) int {
	if limit <= 0 {
		return addressHistoryDefaultLimit
	}
	if limit > addressHistoryMaxLimit {
		return addressHistoryMaxLimit
	}
	return limit
}

func addressHistoryPrefix(address structure.PublicKey) []byte {
	prefix := make([]byte, structure.PublicKeySize)
	copy(prefix, address[:])
	return prefix
}

func addressHistoryKey(address structure.PublicKey, blockHeight uint64, transactionIndex uint16, effectIndex uint8, transactionID string) []byte {
	key := make([]byte, 0, structure.PublicKeySize+8+addressHistoryTxIndexBytes+addressHistoryEffectBytes+len(transactionID))
	key = append(key, address[:]...)
	key = binary.BigEndian.AppendUint64(key, blockHeight)
	key = binary.BigEndian.AppendUint16(key, transactionIndex)
	key = append(key, effectIndex)
	key = append(key, []byte(transactionID)...)
	return key
}

func marshalAddressHistoryStoredRecord(record addressHistoryStoredRecord) ([]byte, error) {
	data, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("blockchain: marshal address history record: %w", err)
	}
	return data, nil
}

func unmarshalAddressHistoryStoredRecord(data []byte) (addressHistoryStoredRecord, error) {
	var record addressHistoryStoredRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return addressHistoryStoredRecord{}, fmt.Errorf("blockchain: decode address history record: %w", err)
	}
	return record, nil
}

func encodeAddressHistoryCursor(lastKey []byte) string {
	return addressHistoryCursorPrefix + base64.StdEncoding.EncodeToString(lastKey)
}

func decodeAddressHistoryCursor(prefix []byte, cursor string) ([]byte, error) {
	if cursor == "" {
		return nil, nil
	}
	if len(cursor) <= len(addressHistoryCursorPrefix) || cursor[:len(addressHistoryCursorPrefix)] != addressHistoryCursorPrefix {
		return nil, fmt.Errorf("blockchain: invalid address history cursor")
	}
	key, err := base64.StdEncoding.DecodeString(cursor[len(addressHistoryCursorPrefix):])
	if err != nil {
		return nil, fmt.Errorf("blockchain: decode address history cursor: %w", err)
	}
	if len(key) < len(prefix) || string(key[:len(prefix)]) != string(prefix) {
		return nil, fmt.Errorf("blockchain: address history cursor does not match address")
	}
	return key, nil
}
