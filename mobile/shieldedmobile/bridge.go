// Package shieldedmobile exposes the shielded V2 wallet primitives through a
// gomobile-compatible string API. Secrets are accepted only by the local
// process and are never sent to a node RPC.
package shieldedmobile

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	walletshielded "github.com/tentechtop/ordexus-core/shielded"
	"github.com/tentechtop/ordexus-core/structure"
)

const (
	bridgeVersion             = "shielded-v2-mobile-1"
	shieldedSeedDomain        = "nulla.wallet.shielded_seed.v2"
	shieldedDiversifierDomain = "nulla.wallet.shielded_diversifier.v2"
)

type walletMaterialResponse struct {
	Address string `json:"address"`
	Version string `json:"version"`
}

type buildDepositRequest struct {
	AmountLamports   string `json:"amount_lamports"`
	PoolDataBase64   string `json:"pool_data_base64"`
	RecentBlockhash  string `json:"recent_blockhash"`
	RecipientAddress string `json:"recipient_address,omitempty"`
	SigningSeed      string `json:"signing_seed"`
}

type createdNoteResponse struct {
	Address          string `json:"address"`
	CommitmentBase64 string `json:"commitment_base64"`
	MemoBase64       string `json:"memo_base64"`
	RandomnessBase64 string `json:"randomness_base64"`
	ValueLamports    string `json:"value_lamports"`
}

type buildTransactionResponse struct {
	CreatedNotes      []createdNoteResponse `json:"created_notes"`
	FeeLamports       string                `json:"fee_lamports"`
	TransactionBase64 string                `json:"transaction_base64"`
	TransactionID     string                `json:"transaction_id"`
}

type spendableNoteRequest struct {
	Address          string `json:"address"`
	CommitmentBase64 string `json:"commitment_base64"`
	MemoBase64       string `json:"memo_base64"`
	Position         string `json:"position"`
	RandomnessBase64 string `json:"randomness_base64"`
	ValueLamports    string `json:"value_lamports"`
}

type privateSpendRequest struct {
	AmountLamports        string                 `json:"amount_lamports"`
	CommitmentsBase64     []string               `json:"commitments_base64"`
	MemoBase64            string                 `json:"memo_base64,omitempty"`
	Notes                 []spendableNoteRequest `json:"notes"`
	PoolDataBase64        string                 `json:"pool_data_base64"`
	RecentBlockhash       string                 `json:"recent_blockhash"`
	RecipientAddress      string                 `json:"recipient_address"`
	SigningSeed           string                 `json:"signing_seed"`
	SpentNullifiersBase64 []string               `json:"spent_nullifiers_base64"`
}

type scanTransactionRequest struct {
	SigningSeed       string `json:"signing_seed"`
	StartPosition     string `json:"start_position"`
	TransactionBase64 string `json:"transaction_base64"`
}

type scannedNoteResponse struct {
	ActionIndex      int    `json:"action_index"`
	Address          string `json:"address"`
	CommitmentBase64 string `json:"commitment_base64"`
	MemoBase64       string `json:"memo_base64"`
	NullifierBase64  string `json:"nullifier_base64"`
	Position         string `json:"position"`
	RandomnessBase64 string `json:"randomness_base64"`
	ValueLamports    string `json:"value_lamports"`
}

type scanTransactionResponse struct {
	CommitmentsBase64 []string              `json:"commitments_base64"`
	NextPosition      string                `json:"next_position"`
	NullifiersBase64  []string              `json:"nullifiers_base64"`
	OwnedNotes        []scannedNoteResponse `json:"owned_notes"`
}

type spendCandidate struct {
	input     walletshielded.SpendInput
	nullifier walletshielded.Nullifier
	position  uint64
	value     uint64
}

// Version identifies the native bridge/protocol combination.
func Version() string {
	return bridgeVersion
}

// DeriveWallet returns the deterministic zs2_ address for a Ordexus signing
// seed. The shielded seed is domain-separated from the transparent Ed25519
// key even though both are recoverable from the same mnemonic.
func DeriveWallet(signingSeed string) (string, error) {
	_, _, address, err := deriveWalletMaterial(signingSeed)
	if err != nil {
		return "", err
	}
	return marshalJSON(walletMaterialResponse{Address: address.String(), Version: bridgeVersion})
}

// BuildDeposit builds and proves a t->z transaction locally. The returned
// transaction is ready for sendTransaction; the node never receives the
// mnemonic or plaintext note.
func BuildDeposit(requestJSON string) (string, error) {
	request := buildDepositRequest{}
	if err := unmarshalRequest(requestJSON, &request); err != nil {
		return "", err
	}
	amount, err := parsePositiveUint64(request.AmountLamports, "amount_lamports")
	if err != nil {
		return "", err
	}
	poolState, err := parsePoolState(request.PoolDataBase64)
	if err != nil {
		return "", err
	}
	recentBlockhash, err := structure.HashFromBase58(strings.TrimSpace(request.RecentBlockhash))
	if err != nil {
		return "", fmt.Errorf("shielded mobile: invalid recent_blockhash: %w", err)
	}
	_, _, ownAddress, err := deriveWalletMaterial(request.SigningSeed)
	if err != nil {
		return "", err
	}
	recipient := ownAddress
	if value := strings.TrimSpace(request.RecipientAddress); value != "" {
		recipient, err = walletshielded.ParseShieldedAddressString(value)
		if err != nil {
			return "", fmt.Errorf("shielded mobile: invalid recipient_address: %w", err)
		}
	}
	transparentAccount, err := deriveTransparentAccount(request.SigningSeed)
	if err != nil {
		return "", err
	}
	result, err := walletshielded.BuildTransfer(walletshielded.TransferRequest{
		PoolState: poolState,
		Outputs: []walletshielded.NoteOutput{{
			Address: recipient,
			Value:   amount,
		}},
		TransparentInput: &walletshielded.TransparentInput{
			Account: transparentAccount,
			Amount:  amount,
		},
		RecentBlockhash: recentBlockhash,
	})
	if err != nil {
		return "", fmt.Errorf("shielded mobile: build deposit: %w", err)
	}
	return encodeTransferResult(result)
}

// BuildPrivateTransfer builds and proves a z->z transaction locally from the
// caller's scanned note set and public commitment tree.
func BuildPrivateTransfer(requestJSON string) (string, error) {
	request := privateSpendRequest{}
	if err := unmarshalRequest(requestJSON, &request); err != nil {
		return "", err
	}
	amount, err := parsePositiveUint64(request.AmountLamports, "amount_lamports")
	if err != nil {
		return "", err
	}
	recipient, err := walletshielded.ParseShieldedAddressString(strings.TrimSpace(request.RecipientAddress))
	if err != nil {
		return "", fmt.Errorf("shielded mobile: invalid recipient_address: %w", err)
	}
	poolState, recentBlockhash, spendingKey, ownAddress, tree, candidates, err := preparePrivateSpend(request)
	if err != nil {
		return "", err
	}
	selected, fee, err := selectSpendCandidates(candidates, amount)
	if err != nil {
		return "", err
	}
	inputTotal := sumCandidates(selected)
	change := inputTotal - amount - fee
	memo, err := decodeOptionalBase64(request.MemoBase64, "memo_base64")
	if err != nil {
		return "", err
	}
	outputs := []walletshielded.NoteOutput{{Address: recipient, Value: amount, Memo: memo}}
	if change > 0 {
		outputs = append(outputs, walletshielded.NoteOutput{Address: ownAddress, Value: change})
	}
	inputs, err := createSpendInputs(spendingKey, tree, selected)
	if err != nil {
		return "", err
	}
	result, err := walletshielded.BuildTransfer(walletshielded.TransferRequest{
		PoolState:       poolState,
		Inputs:          inputs,
		Outputs:         outputs,
		RecentBlockhash: recentBlockhash,
	})
	if err != nil {
		return "", fmt.Errorf("shielded mobile: build private transfer: %w", err)
	}
	return encodeTransferResult(result)
}

// BuildWithdraw builds and proves a z->t transaction locally. Its network fee
// is paid from the selected shielded notes so an unrelated transparent fee
// payer does not create an additional linkage.
func BuildWithdraw(requestJSON string) (string, error) {
	request := privateSpendRequest{}
	if err := unmarshalRequest(requestJSON, &request); err != nil {
		return "", err
	}
	amount, err := parsePositiveUint64(request.AmountLamports, "amount_lamports")
	if err != nil {
		return "", err
	}
	destination, err := parseTransparentAddress(request.RecipientAddress)
	if err != nil {
		return "", err
	}
	poolState, recentBlockhash, spendingKey, ownAddress, tree, candidates, err := preparePrivateSpend(request)
	if err != nil {
		return "", err
	}
	selected, fee, err := selectSpendCandidates(candidates, amount)
	if err != nil {
		return "", err
	}
	change := sumCandidates(selected) - amount - fee
	outputs := make([]walletshielded.NoteOutput, 0, 1)
	if change > 0 {
		outputs = append(outputs, walletshielded.NoteOutput{Address: ownAddress, Value: change})
	}
	inputs, err := createSpendInputs(spendingKey, tree, selected)
	if err != nil {
		return "", err
	}
	result, err := walletshielded.BuildTransfer(walletshielded.TransferRequest{
		PoolState:         poolState,
		Inputs:            inputs,
		Outputs:           outputs,
		TransparentOutput: &walletshielded.TransparentOutput{Address: destination, Amount: amount},
		RecentBlockhash:   recentBlockhash,
	})
	if err != nil {
		return "", fmt.Errorf("shielded mobile: build withdraw: %w", err)
	}
	return encodeTransferResult(result)
}

// ScanTransaction extracts public commitments/nullifiers and decrypts outputs
// owned by the supplied local wallet. start_position is the global commitment
// position immediately before this transaction.
func ScanTransaction(requestJSON string) (string, error) {
	request := scanTransactionRequest{}
	if err := unmarshalRequest(requestJSON, &request); err != nil {
		return "", err
	}
	startPosition, err := strconv.ParseUint(strings.TrimSpace(request.StartPosition), 10, 64)
	if err != nil {
		return "", fmt.Errorf("shielded mobile: start_position must be a uint64")
	}
	wire, err := base64.StdEncoding.DecodeString(strings.TrimSpace(request.TransactionBase64))
	if err != nil {
		return "", fmt.Errorf("shielded mobile: decode transaction_base64: %w", err)
	}
	transaction, err := structure.UnmarshalTransactionBinary(wire)
	if err != nil {
		return "", fmt.Errorf("shielded mobile: decode transaction: %w", err)
	}
	message, err := transaction.OrdexusMessage()
	if err != nil {
		return "", fmt.Errorf("shielded mobile: resolve transaction message: %w", err)
	}
	_, viewKey, _, err := deriveWalletMaterial(request.SigningSeed)
	if err != nil {
		return "", err
	}
	response := scanTransactionResponse{
		CommitmentsBase64: []string{},
		NullifiersBase64:  []string{},
		OwnedNotes:        []scannedNoteResponse{},
	}
	position := startPosition
	for _, compiledInstruction := range message.Instructions {
		programIndex := int(compiledInstruction.ProgramIDIndex)
		if programIndex < 0 || programIndex >= len(message.AccountKeys) || message.AccountKeys[programIndex] != structure.DefaultBuiltinProgramIDs.Shielded {
			continue
		}
		instruction, err := structure.UnmarshalShieldedTransferInstructionBinary(compiledInstruction.Data)
		if err != nil {
			return "", fmt.Errorf("shielded mobile: decode shielded instruction: %w", err)
		}
		for actionIndex, action := range instruction.Bundle.Actions {
			if action.EnabledSpend {
				response.NullifiersBase64 = append(response.NullifiersBase64, base64.StdEncoding.EncodeToString(action.Nullifier[:]))
			}
			if !action.EnabledOutput {
				continue
			}
			response.CommitmentsBase64 = append(response.CommitmentsBase64, base64.StdEncoding.EncodeToString(action.NoteCommitment[:]))
			scanned, owned, err := walletshielded.ScanAction(viewKey, action, position)
			if err != nil {
				return "", fmt.Errorf("shielded mobile: scan shielded action: %w", err)
			}
			if owned {
				response.OwnedNotes = append(response.OwnedNotes, scannedNoteResponse{
					ActionIndex:      actionIndex,
					Address:          scanned.Address.String(),
					CommitmentBase64: base64.StdEncoding.EncodeToString(scanned.Commitment[:]),
					MemoBase64:       base64.StdEncoding.EncodeToString(scanned.Note.Memo),
					NullifierBase64:  base64.StdEncoding.EncodeToString(scanned.Nullifier[:]),
					Position:         strconv.FormatUint(scanned.Position, 10),
					RandomnessBase64: base64.StdEncoding.EncodeToString(scanned.Note.Randomness[:]),
					ValueLamports:    strconv.FormatUint(scanned.Note.Value, 10),
				})
			}
			position++
		}
	}
	response.NextPosition = strconv.FormatUint(position, 10)
	return marshalJSON(response)
}

func preparePrivateSpend(request privateSpendRequest) (
	structure.ShieldedPoolState,
	structure.Blockhash,
	walletshielded.SpendingKey,
	walletshielded.ShieldedAddress,
	*walletshielded.IncrementalMerkleTree,
	[]spendCandidate,
	error,
) {
	poolState, err := parsePoolState(request.PoolDataBase64)
	if err != nil {
		return structure.ShieldedPoolState{}, structure.Blockhash{}, walletshielded.SpendingKey{}, walletshielded.ShieldedAddress{}, nil, nil, err
	}
	recentBlockhash, err := structure.HashFromBase58(strings.TrimSpace(request.RecentBlockhash))
	if err != nil {
		return structure.ShieldedPoolState{}, structure.Blockhash{}, walletshielded.SpendingKey{}, walletshielded.ShieldedAddress{}, nil, nil, fmt.Errorf("shielded mobile: invalid recent_blockhash: %w", err)
	}
	spendingKey, _, ownAddress, err := deriveWalletMaterial(request.SigningSeed)
	if err != nil {
		return structure.ShieldedPoolState{}, structure.Blockhash{}, walletshielded.SpendingKey{}, walletshielded.ShieldedAddress{}, nil, nil, err
	}
	tree, commitments, err := buildCommitmentTree(request.CommitmentsBase64)
	if err != nil {
		return structure.ShieldedPoolState{}, structure.Blockhash{}, walletshielded.SpendingKey{}, walletshielded.ShieldedAddress{}, nil, nil, err
	}
	localRoot := tree.Root()
	if uint64(len(commitments)) != poolState.LeafCount || !bytes.Equal(localRoot[:], poolState.CurrentRoot[:]) {
		return structure.ShieldedPoolState{}, structure.Blockhash{}, walletshielded.SpendingKey{}, walletshielded.ShieldedAddress{}, nil, nil, fmt.Errorf("shielded mobile: local commitment tree is not synchronized with the pool")
	}
	spentNullifiers, err := parseDigestSet(request.SpentNullifiersBase64, "spent_nullifiers_base64")
	if err != nil {
		return structure.ShieldedPoolState{}, structure.Blockhash{}, walletshielded.SpendingKey{}, walletshielded.ShieldedAddress{}, nil, nil, err
	}
	candidates := make([]spendCandidate, 0, len(request.Notes))
	seenPositions := make(map[uint64]struct{}, len(request.Notes))
	for noteIndex, encodedNote := range request.Notes {
		candidate, err := parseSpendCandidate(spendingKey, tree, commitments, encodedNote)
		if err != nil {
			return structure.ShieldedPoolState{}, structure.Blockhash{}, walletshielded.SpendingKey{}, walletshielded.ShieldedAddress{}, nil, nil, fmt.Errorf("shielded mobile: note %d: %w", noteIndex, err)
		}
		if candidate.input.Address != ownAddress {
			return structure.ShieldedPoolState{}, structure.Blockhash{}, walletshielded.SpendingKey{}, walletshielded.ShieldedAddress{}, nil, nil, fmt.Errorf("shielded mobile: note %d does not belong to this wallet address", noteIndex)
		}
		if _, exists := seenPositions[candidate.position]; exists {
			return structure.ShieldedPoolState{}, structure.Blockhash{}, walletshielded.SpendingKey{}, walletshielded.ShieldedAddress{}, nil, nil, fmt.Errorf("shielded mobile: duplicate note position %d", candidate.position)
		}
		seenPositions[candidate.position] = struct{}{}
		if _, spent := spentNullifiers[candidate.nullifier]; !spent {
			candidates = append(candidates, candidate)
		}
	}
	if len(candidates) == 0 {
		return structure.ShieldedPoolState{}, structure.Blockhash{}, walletshielded.SpendingKey{}, walletshielded.ShieldedAddress{}, nil, nil, fmt.Errorf("shielded mobile: no spendable shielded notes")
	}
	return poolState, recentBlockhash, spendingKey, ownAddress, tree, candidates, nil
}

func buildCommitmentTree(encodedCommitments []string) (*walletshielded.IncrementalMerkleTree, []walletshielded.Commitment, error) {
	tree, err := walletshielded.NewCircuitMerkleTree()
	if err != nil {
		return nil, nil, err
	}
	commitments := make([]walletshielded.Commitment, len(encodedCommitments))
	for index, encoded := range encodedCommitments {
		digest, err := parseDigestBase64(encoded, fmt.Sprintf("commitments_base64[%d]", index))
		if err != nil {
			return nil, nil, err
		}
		commitments[index] = digest
		if _, err := tree.Append(digest); err != nil {
			return nil, nil, fmt.Errorf("shielded mobile: append commitment %d: %w", index, err)
		}
	}
	return tree, commitments, nil
}

func parseSpendCandidate(
	spendingKey walletshielded.SpendingKey,
	tree *walletshielded.IncrementalMerkleTree,
	commitments []walletshielded.Commitment,
	encoded spendableNoteRequest,
) (spendCandidate, error) {
	position, err := strconv.ParseUint(strings.TrimSpace(encoded.Position), 10, 64)
	if err != nil || position >= uint64(len(commitments)) {
		return spendCandidate{}, fmt.Errorf("position is outside the local commitment tree")
	}
	value, err := parsePositiveUint64(encoded.ValueLamports, "value_lamports")
	if err != nil {
		return spendCandidate{}, err
	}
	address, err := walletshielded.ParseShieldedAddressString(strings.TrimSpace(encoded.Address))
	if err != nil {
		return spendCandidate{}, fmt.Errorf("invalid address: %w", err)
	}
	randomnessBytes, err := decodeFixedBase64(encoded.RandomnessBase64, walletshielded.NoteRandomnessSize, "randomness_base64")
	if err != nil {
		return spendCandidate{}, err
	}
	var randomness walletshielded.NoteRandomness
	copy(randomness[:], randomnessBytes)
	memo, err := decodeOptionalBase64(encoded.MemoBase64, "memo_base64")
	if err != nil {
		return spendCandidate{}, err
	}
	note, err := walletshielded.NewNote(value, randomness, memo)
	if err != nil {
		return spendCandidate{}, err
	}
	commitment, err := walletshielded.ComputeNoteCommitment(address, note)
	if err != nil {
		return spendCandidate{}, err
	}
	encodedCommitment, err := parseDigestBase64(encoded.CommitmentBase64, "commitment_base64")
	if err != nil {
		return spendCandidate{}, err
	}
	if commitment != encodedCommitment || commitment != commitments[position] {
		return spendCandidate{}, fmt.Errorf("note commitment does not match its tree position")
	}
	nullifier, err := spendingKey.Nullifier(address.Diversifier, commitment, position)
	if err != nil {
		return spendCandidate{}, err
	}
	return spendCandidate{
		input: walletshielded.SpendInput{
			Address: address,
			Note:    note,
		},
		nullifier: nullifier,
		position:  position,
		value:     value,
	}, nil
}

func selectSpendCandidates(candidates []spendCandidate, amount uint64) ([]spendCandidate, uint64, error) {
	sort.Slice(candidates, func(left, right int) bool {
		if candidates[left].value == candidates[right].value {
			return candidates[left].position < candidates[right].position
		}
		return candidates[left].value < candidates[right].value
	})
	feeOne, err := shieldedPoolFee(1)
	if err != nil {
		return nil, 0, err
	}
	if required, ok := checkedAdd(amount, feeOne); ok {
		for _, candidate := range candidates {
			if candidate.value >= required {
				return []spendCandidate{candidate}, feeOne, nil
			}
		}
	}
	feeTwo, err := shieldedPoolFee(2)
	if err != nil {
		return nil, 0, err
	}
	required, ok := checkedAdd(amount, feeTwo)
	if !ok {
		return nil, 0, fmt.Errorf("shielded mobile: amount and fee overflow")
	}
	bestLeft, bestRight := -1, -1
	bestTotal := ^uint64(0)
	for left := 0; left < len(candidates); left++ {
		for right := left + 1; right < len(candidates); right++ {
			total, ok := checkedAdd(candidates[left].value, candidates[right].value)
			if ok && total >= required && total < bestTotal {
				bestLeft, bestRight, bestTotal = left, right, total
			}
		}
	}
	if bestLeft >= 0 {
		return []spendCandidate{candidates[bestLeft], candidates[bestRight]}, feeTwo, nil
	}
	return nil, 0, fmt.Errorf("shielded mobile: available notes cannot cover amount plus fee with at most two inputs")
}

func shieldedPoolFee(spendCount int) (uint64, error) {
	limits := structure.DefaultComputeBudgetLimits()
	limits.MaxComputeUnits = structure.ShieldedGroth16VerificationComputeUnits
	limits.StorageWriteBytesLimit = structure.ShieldedPoolStateWriteChargeBytes + uint64(spendCount)*structure.ShieldedNullifierStateWriteChargeBytes
	details, err := structure.DefaultFeeCalculator().Calculate(0, limits)
	if err != nil {
		return 0, fmt.Errorf("shielded mobile: calculate shielded fee: %w", err)
	}
	return details.TotalFee, nil
}

func createSpendInputs(spendingKey walletshielded.SpendingKey, tree *walletshielded.IncrementalMerkleTree, candidates []spendCandidate) ([]walletshielded.SpendInput, error) {
	inputs := make([]walletshielded.SpendInput, len(candidates))
	for index, candidate := range candidates {
		witness, err := tree.Witness(candidate.position)
		if err != nil {
			return nil, fmt.Errorf("shielded mobile: create witness for position %d: %w", candidate.position, err)
		}
		inputs[index] = candidate.input
		inputs[index].SpendingKey = spendingKey
		inputs[index].Witness = witness
	}
	return inputs, nil
}

func parseDigestSet(values []string, field string) (map[walletshielded.Digest]struct{}, error) {
	result := make(map[walletshielded.Digest]struct{}, len(values))
	for index, value := range values {
		digest, err := parseDigestBase64(value, fmt.Sprintf("%s[%d]", field, index))
		if err != nil {
			return nil, err
		}
		result[digest] = struct{}{}
	}
	return result, nil
}

func parseDigestBase64(value string, field string) (walletshielded.Digest, error) {
	decoded, err := decodeFixedBase64(value, walletshielded.FieldElementSize, field)
	if err != nil {
		return walletshielded.Digest{}, err
	}
	digest, err := walletshielded.NewDigest(decoded)
	if err != nil {
		return walletshielded.Digest{}, fmt.Errorf("shielded mobile: invalid %s: %w", field, err)
	}
	return digest, nil
}

func parseTransparentAddress(value string) (structure.PublicKey, error) {
	normalized := strings.TrimSpace(value)
	if strings.HasPrefix(normalized, "T") {
		normalized = strings.TrimPrefix(normalized, "T")
	}
	publicKey, err := structure.PublicKeyFromBase58(normalized)
	if err != nil {
		return structure.PublicKey{}, fmt.Errorf("shielded mobile: invalid transparent recipient_address: %w", err)
	}
	return publicKey, nil
}

func decodeFixedBase64(value string, expectedLength int, field string) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(decoded) != expectedLength {
		return nil, fmt.Errorf("shielded mobile: %s must encode %d bytes", field, expectedLength)
	}
	return decoded, nil
}

func decodeOptionalBase64(value string, field string) ([]byte, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return nil, fmt.Errorf("shielded mobile: invalid %s: %w", field, err)
	}
	return decoded, nil
}

func checkedAdd(left, right uint64) (uint64, bool) {
	if left > ^uint64(0)-right {
		return 0, false
	}
	return left + right, true
}

func sumCandidates(candidates []spendCandidate) uint64 {
	total := uint64(0)
	for _, candidate := range candidates {
		total += candidate.value
	}
	return total
}

func deriveWalletMaterial(signingSeed string) (walletshielded.SpendingKey, walletshielded.ViewKey, walletshielded.ShieldedAddress, error) {
	normalized := strings.TrimSpace(signingSeed)
	if normalized == "" {
		return walletshielded.SpendingKey{}, walletshielded.ViewKey{}, walletshielded.ShieldedAddress{}, fmt.Errorf("shielded mobile: signing seed is empty")
	}
	shieldedSeed := deriveDomainHash(shieldedSeedDomain, normalized)
	diversifierHash := deriveDomainHash(shieldedDiversifierDomain, normalized)
	diversifier, err := walletshielded.NewDiversifier(diversifierHash[:walletshielded.DiversifierSize])
	if err != nil {
		return walletshielded.SpendingKey{}, walletshielded.ViewKey{}, walletshielded.ShieldedAddress{}, err
	}
	spendingKey, viewKey, address, err := walletshielded.DeriveKeySet(shieldedSeed[:], diversifier)
	if err != nil {
		return walletshielded.SpendingKey{}, walletshielded.ViewKey{}, walletshielded.ShieldedAddress{}, fmt.Errorf("shielded mobile: derive wallet: %w", err)
	}
	return spendingKey, viewKey, address, nil
}

func deriveTransparentAccount(signingSeed string) (structure.OrdexusKeyPair, error) {
	normalized := strings.TrimSpace(signingSeed)
	if normalized == "" {
		return structure.OrdexusKeyPair{}, fmt.Errorf("shielded mobile: signing seed is empty")
	}
	seed := sha256.Sum256([]byte(normalized))
	keyPair, err := structure.KeyPairFromSeed(seed[:])
	if err != nil {
		return structure.OrdexusKeyPair{}, fmt.Errorf("shielded mobile: derive transparent account: %w", err)
	}
	return keyPair, nil
}

func deriveDomainHash(domain string, value string) [sha256.Size]byte {
	hasher := sha256.New()
	hasher.Write([]byte(domain))
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	hasher.Write(size[:])
	hasher.Write([]byte(value))
	var result [sha256.Size]byte
	copy(result[:], hasher.Sum(nil))
	return result
}

func parsePoolState(encoded string) (structure.ShieldedPoolState, error) {
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return structure.ShieldedPoolState{}, fmt.Errorf("shielded mobile: decode pool_data_base64: %w", err)
	}
	poolState, err := structure.UnmarshalShieldedPoolStateBinary(data)
	if err != nil {
		return structure.ShieldedPoolState{}, fmt.Errorf("shielded mobile: decode pool state: %w", err)
	}
	return poolState, nil
}

func parsePositiveUint64(value string, field string) (uint64, error) {
	parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	if err != nil || parsed == 0 {
		return 0, fmt.Errorf("shielded mobile: %s must be a positive uint64", field)
	}
	return parsed, nil
}

func encodeTransferResult(result walletshielded.TransferResult) (string, error) {
	wire, err := result.Transaction.MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("shielded mobile: encode transaction: %w", err)
	}
	transactionID, err := result.Transaction.TxIDString()
	if err != nil {
		return "", fmt.Errorf("shielded mobile: encode transaction id: %w", err)
	}
	createdNotes := make([]createdNoteResponse, len(result.CreatedNotes))
	for index, created := range result.CreatedNotes {
		createdNotes[index] = createdNoteResponse{
			Address:          created.Address.String(),
			CommitmentBase64: base64.StdEncoding.EncodeToString(created.Commitment[:]),
			MemoBase64:       base64.StdEncoding.EncodeToString(created.Note.Memo),
			RandomnessBase64: base64.StdEncoding.EncodeToString(created.Note.Randomness[:]),
			ValueLamports:    strconv.FormatUint(created.Note.Value, 10),
		}
	}
	return marshalJSON(buildTransactionResponse{
		CreatedNotes:      createdNotes,
		FeeLamports:       strconv.FormatUint(result.FeeLamports, 10),
		TransactionBase64: base64.StdEncoding.EncodeToString(wire),
		TransactionID:     transactionID,
	})
}

func unmarshalRequest(requestJSON string, destination any) error {
	decoder := json.NewDecoder(strings.NewReader(requestJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("shielded mobile: invalid request JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("shielded mobile: request JSON contains trailing data")
		}
		return fmt.Errorf("shielded mobile: invalid trailing JSON: %w", err)
	}
	return nil
}

func marshalJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("shielded mobile: encode response: %w", err)
	}
	return string(encoded), nil
}
