package shielded

import (
	"crypto/rand"
	"fmt"
	"io"
	"math"
	"math/big"

	"github.com/tentechtop/ordexus-core/structure"
	"github.com/tentechtop/ordexus-core/zk"
)

// SpendInput 是钱包本地持有的可花费 note + 节点只会看到其 nullifier 和零知识证明。
type SpendInput struct {
	SpendingKey SpendingKey
	Address     ShieldedAddress
	Note        Note
	Witness     MerkleWitness
}

// NoteOutput 描述一个新的屏蔽收款输出。
type NoteOutput struct {
	Address ShieldedAddress
	Value   uint64
	Memo    []byte
}

// TransparentInput 描述 t→z 的透明输入 + 该账户同时支付透明手续费。
type TransparentInput struct {
	Account structure.OrdexusKeyPair
	Amount  uint64
}

// TransparentOutput 描述 z→t 的透明收款。
type TransparentOutput struct {
	Address structure.PublicKey
	Amount  uint64
}

// TransferRequest 描述钱包本地构造统一屏蔽交易所需的全部公开和私有输入。
type TransferRequest struct {
	PoolState           structure.ShieldedPoolState
	Inputs              []SpendInput
	Outputs             []NoteOutput
	TransparentInput    *TransparentInput
	TransparentOutput   *TransparentOutput
	TransparentFeePayer *structure.OrdexusKeyPair
	RecentBlockhash     structure.Blockhash
	FeeCalculator       structure.FeeCalculator
	Parameters          *zk.ShieldedCircuitParameters
	Randomness          io.Reader
}

// CreatedNote 返回本次交易创建的本地 note 信息 + 发送方应安全保存找零输出。
type CreatedNote struct {
	Address       ShieldedAddress
	Note          Note
	Commitment    Commitment
	EncryptedNote EncryptedNote
}

// TransferResult 返回可直接提交的交易和钱包本地输出。
type TransferResult struct {
	Transaction  structure.Transaction
	CreatedNotes []CreatedNote
	FeeLamports  uint64
}

// ScannedNote 是查看密钥从链上输出恢复的 note。
type ScannedNote struct {
	Address    ShieldedAddress
	Note       Note
	Commitment Commitment
	Nullifier  Nullifier
	Position   uint64
}

// BuildTransfer 在钱包本地生成证明和交易 + 节点 RPC 不接收 seed、金额明文或花费密钥。
func BuildTransfer(request TransferRequest) (TransferResult, error) {
	if err := validateTransferRequest(request); err != nil {
		return TransferResult{}, err
	}
	parameters := request.Parameters
	if parameters == nil {
		var err error
		parameters, err = zk.DefaultShieldedCircuitParameters()
		if err != nil {
			return TransferResult{}, err
		}
	}
	verifyingKeyDigest, err := parameters.VerifyingKeyHash()
	if err != nil {
		return TransferResult{}, err
	}
	if string(verifyingKeyDigest[:]) != string(request.PoolState.VerifyingKeyHash[:]) {
		return TransferResult{}, fmt.Errorf("shielded: proving parameters do not match pool verifying key")
	}
	randomnessSource := request.Randomness
	if randomnessSource == nil {
		randomnessSource = rand.Reader
	}

	actionCount := len(request.Inputs)
	if len(request.Outputs) > actionCount {
		actionCount = len(request.Outputs)
	}
	actions := make([]structure.ShieldedAction, actionCount)
	createdNotes := make([]CreatedNote, len(request.Outputs))
	assignment := newShieldedCircuitAssignment()
	inputTotal := uint64(0)
	outputTotal := uint64(0)

	for actionIndex := 0; actionIndex < actionCount; actionIndex++ {
		if actionIndex < len(request.Inputs) {
			input := request.Inputs[actionIndex]
			inputValue, err := populateSpendWitness(assignment, actionIndex, input, request.PoolState.CurrentRoot)
			if err != nil {
				return TransferResult{}, fmt.Errorf("shielded: input %d: %w", actionIndex, err)
			}
			inputTotal, err = addTransferValue(inputTotal, inputValue)
			if err != nil {
				return TransferResult{}, err
			}
			nullifier := assignment.Nullifiers[actionIndex].(*big.Int)
			nullifierBytes, err := zk.ShieldedFieldBytes(nullifier)
			if err != nil {
				return TransferResult{}, err
			}
			copy(actions[actionIndex].Nullifier[:], nullifierBytes[:])
			actions[actionIndex].EnabledSpend = true
		}
		if actionIndex < len(request.Outputs) {
			output := request.Outputs[actionIndex]
			createdNote, err := populateOutputWitness(assignment, actionIndex, output, randomnessSource)
			if err != nil {
				return TransferResult{}, fmt.Errorf("shielded: output %d: %w", actionIndex, err)
			}
			outputTotal, err = addTransferValue(outputTotal, output.Value)
			if err != nil {
				return TransferResult{}, err
			}
			createdNotes[actionIndex] = createdNote
			encryptedNoteBytes, err := createdNote.EncryptedNote.MarshalBinary()
			if err != nil {
				return TransferResult{}, err
			}
			actions[actionIndex].EnabledOutput = true
			copy(actions[actionIndex].NoteCommitment[:], createdNote.Commitment[:])
			actions[actionIndex].EphemeralKey = append([]byte(nil), createdNote.EncryptedNote.EphemeralPublicKey[:]...)
			actions[actionIndex].EncryptedNote = encryptedNoteBytes
		}
	}

	feeSource, signer, signatureCount, err := transferFeeSource(request)
	if err != nil {
		return TransferResult{}, err
	}
	feeCalculator := request.FeeCalculator
	if feeCalculator == (structure.FeeCalculator{}) {
		feeCalculator = structure.DefaultFeeCalculator()
	}
	computeBudget := structure.DefaultComputeBudgetLimits()
	computeBudget.MaxComputeUnits = structure.ShieldedGroth16VerificationComputeUnits
	computeBudget.StorageWriteBytesLimit, err = structure.ShieldedStorageWriteChargeBytes(actions)
	if err != nil {
		return TransferResult{}, err
	}
	feeDetails, err := feeCalculator.Calculate(signatureCount, computeBudget)
	if err != nil {
		return TransferResult{}, err
	}
	transparentInputAmount := uint64(0)
	if request.TransparentInput != nil {
		transparentInputAmount = request.TransparentInput.Amount
	}
	transparentOutputAmount := uint64(0)
	if request.TransparentOutput != nil {
		transparentOutputAmount = request.TransparentOutput.Amount
	}
	shieldedFee := uint64(0)
	if feeSource == structure.FeePaymentSourceShieldedPool {
		shieldedFee = feeDetails.TotalFee
	}
	expectedValueBalance, err := calculateTransferValueBalance(transparentInputAmount, transparentOutputAmount, shieldedFee)
	if err != nil {
		return TransferResult{}, err
	}
	actualValueBalance, err := signedTransferDifference(inputTotal, outputTotal)
	if err != nil {
		return TransferResult{}, err
	}
	if actualValueBalance != expectedValueBalance {
		return TransferResult{}, fmt.Errorf("shielded: value balance %d does not match transparent sides and fee %d", actualValueBalance, expectedValueBalance)
	}

	bundle := structure.ShieldedBundle{
		ProtocolVersion:   structure.ShieldedProtocolVersion,
		ConsensusBranchID: structure.ShieldedConsensusBranchID,
		Anchor:            request.PoolState.CurrentRoot,
		Actions:           actions,
		ValueBalance:      actualValueBalance,
	}
	instruction := structure.ShieldedTransferInstruction{
		EncodingVersion:         structure.ShieldedInstructionEncodingVersion,
		TransparentInputAmount:  transparentInputAmount,
		TransparentOutputAmount: transparentOutputAmount,
		Bundle:                  bundle,
	}
	messageAccounts, orderedInstructionAccounts, err := buildTransferAccounts(request, actions, signer)
	if err != nil {
		return TransferResult{}, err
	}
	// 先用固定长度占位证明构造完整消息，证明上下文必须绑定付费者、消息头和全部账户顺序。
	instruction.Bundle.Proof = make([]byte, structure.ShieldedGroth16ProofBytes)
	instruction.Bundle.Proof[0] = 1
	placeholderInstructionData, err := instruction.MarshalBinary()
	if err != nil {
		return TransferResult{}, err
	}
	accountIndex, err := structure.AccountIndexMap(messageAccounts)
	if err != nil {
		return TransferResult{}, err
	}
	compiledInstruction, err := structure.CompileInstruction(
		structure.DefaultBuiltinProgramIDs.Shielded,
		orderedInstructionAccounts,
		placeholderInstructionData,
		accountIndex,
	)
	if err != nil {
		return TransferResult{}, err
	}
	authorizationMessage, err := (structure.Transaction{
		Accounts:        messageAccounts,
		Instructions:    []structure.CompiledInstruction{compiledInstruction},
		RecentBlockhash: request.RecentBlockhash,
	}).OrdexusMessage()
	if err != nil {
		return TransferResult{}, err
	}
	resolvedAuthorizationMessage, err := structure.NewResolvedMessage(authorizationMessage, structure.LoadedAddresses{})
	if err != nil {
		return TransferResult{}, err
	}
	contextBytes, err := structure.BuildShieldedProofContextBytes(
		instruction,
		structure.DefaultBuiltinProgramIDs.Shielded,
		structure.DefaultShieldedPoolAddress,
		resolvedAuthorizationMessage,
		0,
		feeSource,
		feeDetails.TotalFee,
		orderedInstructionAccounts,
	)
	if err != nil {
		return TransferResult{}, err
	}
	if err := populatePublicWitness(assignment, bundle, zk.ComputeShieldedContextDigest(contextBytes)); err != nil {
		return TransferResult{}, err
	}
	proof, err := parameters.Prove(assignment)
	if err != nil {
		return TransferResult{}, fmt.Errorf("shielded: generate proof: %w", err)
	}
	instruction.Bundle.Proof = proof
	instructionData, err := instruction.MarshalBinary()
	if err != nil {
		return TransferResult{}, err
	}
	compiledInstruction.Data = instructionData
	transaction := structure.Transaction{
		Accounts:        messageAccounts,
		Instructions:    []structure.CompiledInstruction{compiledInstruction},
		RecentBlockhash: request.RecentBlockhash,
		Fee:             feeDetails.TotalFee,
	}
	if signer != nil {
		transaction, err = transaction.Sign(map[structure.PublicKey][]byte{signer.PublicKey: signer.PrivateKey})
		if err != nil {
			return TransferResult{}, err
		}
	}
	if err := transaction.Validate(); err != nil {
		return TransferResult{}, err
	}
	if _, err := transaction.CalculatedSize(); err != nil {
		return TransferResult{}, fmt.Errorf("shielded: encode transfer transaction: %w", err)
	}
	return TransferResult{Transaction: transaction, CreatedNotes: createdNotes, FeeLamports: feeDetails.TotalFee}, nil
}

// ScanAction 使用查看密钥恢复属于钱包的输出并计算其未来 nullifier。
func ScanAction(viewKey ViewKey, action structure.ShieldedAction, position uint64) (ScannedNote, bool, error) {
	if !action.EnabledOutput {
		return ScannedNote{}, false, nil
	}
	encryptedNote, err := ParseEncryptedNote(action.EncryptedNote)
	if err != nil {
		return ScannedNote{}, false, err
	}
	if len(action.EphemeralKey) != TransmissionKeySize || string(action.EphemeralKey) != string(encryptedNote.EphemeralPublicKey[:]) {
		return ScannedNote{}, false, fmt.Errorf("shielded: action ephemeral key mismatch")
	}
	note, err := DecryptNote(viewKey, encryptedNote)
	if err != nil {
		return ScannedNote{}, false, nil
	}
	address, err := viewKey.Address()
	if err != nil {
		return ScannedNote{}, false, err
	}
	commitment, err := ComputeNoteCommitment(address, note)
	if err != nil {
		return ScannedNote{}, false, err
	}
	if string(commitment[:]) != string(action.NoteCommitment[:]) {
		return ScannedNote{}, false, fmt.Errorf("shielded: decrypted note commitment mismatch")
	}
	nullifier, err := ComputeNullifier(viewKey, commitment, position)
	if err != nil {
		return ScannedNote{}, false, err
	}
	return ScannedNote{Address: address, Note: note, Commitment: commitment, Nullifier: nullifier, Position: position}, true, nil
}

func validateTransferRequest(request TransferRequest) error {
	if err := request.PoolState.Validate(); err != nil {
		return err
	}
	if request.PoolState.ProtocolVersion != structure.ShieldedProtocolVersion || request.PoolState.CircuitID != zk.ShieldedCircuitID || request.PoolState.TreeDepth != CircuitMerkleDepth {
		return fmt.Errorf("shielded: pool state is incompatible with V2 circuit")
	}
	if request.RecentBlockhash.IsZero() {
		return structure.ErrEmptyRecentBlockhash
	}
	if len(request.Inputs) > zk.ShieldedActionCount || len(request.Outputs) > zk.ShieldedActionCount {
		return fmt.Errorf("shielded: at most %d inputs and outputs are supported", zk.ShieldedActionCount)
	}
	if len(request.Inputs) == 0 && len(request.Outputs) == 0 {
		return fmt.Errorf("shielded: transfer requires a shielded input or output")
	}
	if request.TransparentInput != nil && request.TransparentFeePayer != nil {
		return fmt.Errorf("shielded: transparent input already acts as fee payer")
	}
	if request.TransparentInput != nil && request.TransparentInput.Amount == 0 {
		return fmt.Errorf("shielded: transparent input amount must be positive")
	}
	if request.TransparentOutput != nil && request.TransparentOutput.Amount == 0 {
		return fmt.Errorf("shielded: transparent output amount must be positive")
	}
	return nil
}

func populateSpendWitness(assignment *zk.ShieldedCircuit, actionIndex int, input SpendInput, anchor structure.Hash) (uint64, error) {
	if input.Note.Value == 0 || input.Witness.Depth != CircuitMerkleDepth || input.Witness.Position >= uint64(1)<<CircuitMerkleDepth {
		return 0, fmt.Errorf("invalid spend note or Merkle witness")
	}
	viewKey, err := input.SpendingKey.DeriveViewKey(input.Address.Diversifier)
	if err != nil {
		return 0, err
	}
	if !viewKey.OwnsAddress(input.Address) {
		return 0, fmt.Errorf("spending key does not own input address")
	}
	commitment, err := ComputeNoteCommitment(input.Address, input.Note)
	if err != nil {
		return 0, err
	}
	var expectedRoot Digest
	copy(expectedRoot[:], anchor[:])
	if !input.Witness.Verify(commitment, expectedRoot) {
		return 0, fmt.Errorf("Merkle witness does not match anchor")
	}
	nullifier, err := ComputeNullifier(viewKey, commitment, input.Witness.Position)
	if err != nil {
		return 0, err
	}
	assignment.SpendEnabled[actionIndex] = 1
	assignment.Nullifiers[actionIndex] = new(big.Int).SetBytes(nullifier[:])
	assignment.SpendSecrets[actionIndex] = new(big.Int).SetBytes(input.SpendingKey.Bytes())
	assignment.InputValues[actionIndex] = input.Note.Value
	assignment.InputRandomness[actionIndex] = new(big.Int).SetBytes(input.Note.Randomness[:])
	assignment.InputDiversifiers[actionIndex] = new(big.Int).SetBytes(input.Address.Diversifier[:])
	assignment.MerklePositions[actionIndex] = input.Witness.Position
	for level, sibling := range input.Witness.Siblings {
		assignment.MerklePaths[actionIndex][level] = new(big.Int).SetBytes(sibling[:])
	}
	return input.Note.Value, nil
}

func populateOutputWitness(assignment *zk.ShieldedCircuit, actionIndex int, output NoteOutput, randomnessSource io.Reader) (CreatedNote, error) {
	if output.Value == 0 {
		return CreatedNote{}, fmt.Errorf("output value must be positive")
	}
	note, err := GenerateNoteWithReader(output.Value, output.Memo, randomnessSource)
	if err != nil {
		return CreatedNote{}, err
	}
	commitment, err := ComputeNoteCommitment(output.Address, note)
	if err != nil {
		return CreatedNote{}, err
	}
	encryptedNote, err := EncryptNoteWithReader(output.Address, note, randomnessSource)
	if err != nil {
		return CreatedNote{}, err
	}
	assignment.OutputEnabled[actionIndex] = 1
	assignment.OutputCommitments[actionIndex] = new(big.Int).SetBytes(commitment[:])
	assignment.OutputValues[actionIndex] = output.Value
	assignment.OutputRandomness[actionIndex] = new(big.Int).SetBytes(note.Randomness[:])
	assignment.OutputOwnerTags[actionIndex] = new(big.Int).SetBytes(output.Address.OwnerTag[:])
	return CreatedNote{Address: output.Address, Note: note, Commitment: commitment, EncryptedNote: encryptedNote}, nil
}

func populatePublicWitness(assignment *zk.ShieldedCircuit, bundle structure.ShieldedBundle, contextDigest *big.Int) error {
	anchor, err := zk.ShieldedFieldFromBytes(bundle.Anchor[:])
	if err != nil {
		return err
	}
	assignment.Anchor = anchor
	assignment.ContextDigest = contextDigest
	if bundle.ValueBalance >= 0 {
		assignment.ValueBalanceDirection = 0
		assignment.ValueBalanceMagnitude = uint64(bundle.ValueBalance)
	} else {
		assignment.ValueBalanceDirection = 1
		assignment.ValueBalanceMagnitude = absoluteSignedValue(bundle.ValueBalance)
	}
	return nil
}

func buildTransferAccounts(request TransferRequest, actions []structure.ShieldedAction, signer *structure.OrdexusKeyPair) ([]structure.AccountMeta, []structure.PublicKey, error) {
	orderedInstructionAccounts := []structure.PublicKey{structure.DefaultShieldedPoolAddress}
	messageAccounts := make([]structure.AccountMeta, 0, 6)
	if signer != nil {
		messageAccounts = append(messageAccounts, structure.AccountMeta{PublicKey: signer.PublicKey, IsSigner: true, IsWritable: true})
	}
	messageAccounts = append(messageAccounts, structure.AccountMeta{PublicKey: structure.DefaultShieldedPoolAddress, IsWritable: true})
	if request.TransparentInput != nil {
		orderedInstructionAccounts = append(orderedInstructionAccounts, request.TransparentInput.Account.PublicKey)
		messageAccounts = append(messageAccounts, structure.AccountMeta{PublicKey: request.TransparentInput.Account.PublicKey, IsSigner: true, IsWritable: true})
	}
	if request.TransparentOutput != nil {
		orderedInstructionAccounts = append(orderedInstructionAccounts, request.TransparentOutput.Address)
		messageAccounts = append(messageAccounts, structure.AccountMeta{PublicKey: request.TransparentOutput.Address, IsWritable: true})
	}
	for _, action := range actions {
		if !action.EnabledSpend {
			continue
		}
		markerAddress := structure.DeriveShieldedNullifierAccount(structure.DefaultBuiltinProgramIDs.Shielded, action.Nullifier)
		orderedInstructionAccounts = append(orderedInstructionAccounts, markerAddress)
		messageAccounts = append(messageAccounts, structure.AccountMeta{PublicKey: markerAddress, IsWritable: true})
	}
	messageAccounts = append(messageAccounts, structure.AccountMeta{PublicKey: structure.DefaultBuiltinProgramIDs.Shielded})
	mergedAccounts, err := structure.MergeAccountMetas(messageAccounts)
	if err != nil {
		return nil, nil, err
	}
	return structure.SortAccountMetasForMessage(mergedAccounts), orderedInstructionAccounts, nil
}

func transferFeeSource(request TransferRequest) (structure.FeePaymentSource, *structure.OrdexusKeyPair, int, error) {
	if request.TransparentInput != nil {
		if len(request.TransparentInput.Account.PrivateKey) == 0 {
			return 0, nil, 0, fmt.Errorf("shielded: transparent input private key is missing")
		}
		return structure.FeePaymentSourceTransparentAccount, &request.TransparentInput.Account, 1, nil
	}
	if request.TransparentFeePayer != nil {
		if len(request.TransparentFeePayer.PrivateKey) == 0 {
			return 0, nil, 0, fmt.Errorf("shielded: transparent fee payer private key is missing")
		}
		return structure.FeePaymentSourceTransparentAccount, request.TransparentFeePayer, 1, nil
	}
	return structure.FeePaymentSourceShieldedPool, nil, 0, nil
}

func addTransferValue(left, right uint64) (uint64, error) {
	if left > math.MaxUint64-right {
		return 0, fmt.Errorf("shielded: value sum overflow")
	}
	return left + right, nil
}

func signedTransferDifference(inputTotal, outputTotal uint64) (int64, error) {
	if inputTotal >= outputTotal {
		difference := inputTotal - outputTotal
		if difference > math.MaxInt64 {
			return 0, fmt.Errorf("shielded: positive value balance exceeds int64")
		}
		return int64(difference), nil
	}
	difference := outputTotal - inputTotal
	if difference > math.MaxInt64 {
		return 0, fmt.Errorf("shielded: negative value balance exceeds int64")
	}
	return -int64(difference), nil
}

func calculateTransferValueBalance(transparentInput, transparentOutput, shieldedFee uint64) (int64, error) {
	positiveSide, err := addTransferValue(transparentOutput, shieldedFee)
	if err != nil {
		return 0, err
	}
	return signedTransferDifference(positiveSide, transparentInput)
}

func absoluteSignedValue(value int64) uint64 {
	if value >= 0 {
		return uint64(value)
	}
	return uint64(-(value + 1)) + 1
}

func newShieldedCircuitAssignment() *zk.ShieldedCircuit {
	assignment := &zk.ShieldedCircuit{
		Anchor:                0,
		ValueBalanceDirection: 0,
		ValueBalanceMagnitude: 0,
		ContextDigest:         0,
	}
	for actionIndex := 0; actionIndex < zk.ShieldedActionCount; actionIndex++ {
		assignment.Nullifiers[actionIndex] = 0
		assignment.OutputCommitments[actionIndex] = 0
		assignment.SpendEnabled[actionIndex] = 0
		assignment.OutputEnabled[actionIndex] = 0
		assignment.SpendSecrets[actionIndex] = 0
		assignment.InputValues[actionIndex] = 0
		assignment.InputRandomness[actionIndex] = 0
		assignment.InputDiversifiers[actionIndex] = 0
		assignment.MerklePositions[actionIndex] = 0
		assignment.OutputValues[actionIndex] = 0
		assignment.OutputRandomness[actionIndex] = 0
		assignment.OutputOwnerTags[actionIndex] = 0
		for treeLevel := 0; treeLevel < zk.ShieldedMerkleDepth; treeLevel++ {
			assignment.MerklePaths[actionIndex][treeLevel] = 0
		}
	}
	return assignment
}
