package shielded

import (
	"bytes"
	"fmt"
	"math"
	"math/big"

	"github.com/consensys/gnark/backend/groth16"

	"github.com/tentechtop/ordexus-core/runtime"
	walletshielded "github.com/tentechtop/ordexus-core/shielded"
	"github.com/tentechtop/ordexus-core/structure"
	"github.com/tentechtop/ordexus-core/zk"
)

// Program 验证统一屏蔽 bundle + 维护全局承诺树和独立 nullifier 标记账户。
type Program struct {
	programID        structure.PublicKey
	poolAddress      structure.PublicKey
	verifyingKey     groth16.VerifyingKey
	verifyingKeyHash structure.Hash
}

type instructionAccounts struct {
	poolAddress       structure.PublicKey
	transparentSource *structure.PublicKey
	transparentTarget *structure.PublicKey
	nullifierMarkers  []structure.PublicKey
	ordered           []structure.PublicKey
}

// NewProgram 创建屏蔽池固定程序 + 验证密钥摘要会与池账户状态逐笔比对。
func NewProgram(programID structure.PublicKey, poolAddress structure.PublicKey, verifyingKey groth16.VerifyingKey) (Program, error) {
	if programID.IsZero() || poolAddress.IsZero() || verifyingKey == nil {
		return Program{}, fmt.Errorf("shielded program: program, pool and verifying key are required")
	}
	verifyingKeyDigest, err := zk.ShieldedVerifyingKeyHash(verifyingKey)
	if err != nil {
		return Program{}, err
	}
	verifyingKeyHash, err := structure.NewHash(verifyingKeyDigest[:])
	if err != nil {
		return Program{}, err
	}
	return Program{
		programID:        programID,
		poolAddress:      poolAddress,
		verifyingKey:     verifyingKey,
		verifyingKeyHash: verifyingKeyHash,
	}, nil
}

// NewDefaultProgram 使用协议内置 V2 参数创建共识程序。
func NewDefaultProgram() (Program, error) {
	parameters, err := zk.DefaultShieldedCircuitParameters()
	if err != nil {
		return Program{}, err
	}
	return NewProgram(structure.DefaultBuiltinProgramIDs.Shielded, structure.DefaultShieldedPoolAddress, parameters.VerifyingKey)
}

// ProgramID 返回屏蔽固定程序地址。
func (program Program) ProgramID() structure.PublicKey {
	return program.programID
}

// VerifyingKeyHash 返回当前共识验证密钥摘要。
func (program Program) VerifyingKeyHash() structure.Hash {
	return program.verifyingKeyHash
}

// Execute 验证证明和资金守恒后原子更新池状态。
func (program Program) Execute(context runtime.InstructionContext) error {
	if context.ComputeBudget.MaxComputeUnits < structure.ShieldedGroth16VerificationComputeUnits {
		return fmt.Errorf(
			"%w: shielded Groth16 verification requires %d compute units, remaining %d",
			structure.ErrInvalidFee,
			structure.ShieldedGroth16VerificationComputeUnits,
			context.ComputeBudget.MaxComputeUnits,
		)
	}
	instruction, err := structure.UnmarshalShieldedTransferInstructionBinary(context.Instruction.Data)
	if err != nil {
		return err
	}
	if instruction.Bundle.ConsensusBranchID != structure.ShieldedConsensusBranchID {
		return fmt.Errorf("%w: unsupported consensus branch %08x", structure.ErrInvalidShieldedBundle, instruction.Bundle.ConsensusBranchID)
	}
	if err := validateEncryptedOutputs(instruction.Bundle.Actions); err != nil {
		return err
	}
	accounts, err := program.resolveInstructionAccounts(context, instruction)
	if err != nil {
		return err
	}
	poolState, _, reserveLamports, err := program.loadPool(context, accounts.poolAddress)
	if err != nil {
		return err
	}
	if err := validateAnchor(poolState, instruction.Bundle.Anchor); err != nil {
		return err
	}
	if err := program.validateNullifierMarkers(context, instruction, accounts.nullifierMarkers); err != nil {
		return err
	}
	expectedValueBalance, err := expectedValueBalance(instruction, context)
	if err != nil {
		return err
	}
	if instruction.Bundle.ValueBalance != expectedValueBalance {
		return fmt.Errorf("%w: value balance %d, want %d", structure.ErrInvalidShieldedBundle, instruction.Bundle.ValueBalance, expectedValueBalance)
	}
	contextBytes, err := structure.BuildShieldedProofContextBytes(
		instruction,
		program.programID,
		program.poolAddress,
		context.Message,
		context.InstructionIndex,
		context.FeeSource,
		context.TransactionFee,
		accounts.ordered,
	)
	if err != nil {
		return err
	}
	publicAssignment, err := publicCircuitAssignment(instruction.Bundle, zk.ComputeShieldedContextDigest(contextBytes))
	if err != nil {
		return err
	}
	preparedAccounts, err := program.prepareStateTransition(
		context,
		instruction,
		accounts,
		poolState,
		reserveLamports,
	)
	if err != nil {
		return err
	}
	if context.ComputeUnitsUsed != nil {
		*context.ComputeUnitsUsed += structure.ShieldedGroth16VerificationComputeUnits
	}
	if err := zk.VerifyShieldedCircuit(program.verifyingKey, publicAssignment, instruction.Bundle.Proof); err != nil {
		return fmt.Errorf("%w: verify Groth16 proof: %v", structure.ErrInvalidShieldedBundle, err)
	}
	for address, account := range preparedAccounts {
		context.Accounts[address] = account
	}
	return nil
}

// prepareStateTransition 在验证明前用独立快照完成所有便宜状态检查 + 证明成功后不再留有可失败的状态分支。
func (program Program) prepareStateTransition(
	context runtime.InstructionContext,
	instruction structure.ShieldedTransferInstruction,
	accounts instructionAccounts,
	poolState structure.ShieldedPoolState,
	reserveLamports uint64,
) (map[structure.PublicKey]structure.Account, error) {
	preparedAccounts := cloneInstructionAccounts(context.Accounts)
	nextPoolLamports, err := applyValueBalance(poolState.PoolLamports, instruction.Bundle.ValueBalance)
	if err != nil {
		return nil, err
	}
	if accounts.transparentSource != nil {
		if err := runtime.TransferLamports(*accounts.transparentSource, accounts.poolAddress, instruction.TransparentInputAmount, preparedAccounts, context.RentConfig); err != nil {
			return nil, err
		}
	}
	if accounts.transparentTarget != nil {
		if err := runtime.TransferLamports(accounts.poolAddress, *accounts.transparentTarget, instruction.TransparentOutputAmount, preparedAccounts, context.RentConfig); err != nil {
			return nil, err
		}
	}
	poolState.PoolLamports = nextPoolLamports
	if err := appendOutputCommitments(&poolState, instruction.Bundle.Actions); err != nil {
		return nil, err
	}
	preparedContext := context
	preparedContext.Accounts = preparedAccounts
	if err := program.storeNullifierMarkers(preparedContext, instruction, accounts.nullifierMarkers); err != nil {
		return nil, err
	}
	encodedState, err := poolState.MarshalBinary()
	if err != nil {
		return nil, err
	}
	poolAccount := preparedAccounts[accounts.poolAddress].Clone()
	poolAccount.Owner = program.programID
	poolAccount.Data = encodedState
	deferredFee := uint64(0)
	if context.FeeSource == structure.FeePaymentSourceShieldedPool {
		deferredFee = context.TransactionFee
	}
	expectedPhysicalLamports, err := checkedAddUint64(reserveLamports, nextPoolLamports)
	if err != nil {
		return nil, err
	}
	expectedPhysicalLamports, err = checkedAddUint64(expectedPhysicalLamports, deferredFee)
	if err != nil {
		return nil, err
	}
	if poolAccount.Lamports != expectedPhysicalLamports {
		return nil, fmt.Errorf("%w: pool physical balance %d, want %d", structure.ErrInvalidShieldedPoolState, poolAccount.Lamports, expectedPhysicalLamports)
	}
	preparedAccounts[accounts.poolAddress] = poolAccount
	return preparedAccounts, nil
}

func cloneInstructionAccounts(accounts map[structure.PublicKey]structure.Account) map[structure.PublicKey]structure.Account {
	cloned := make(map[structure.PublicKey]structure.Account, len(accounts))
	for address, account := range accounts {
		cloned[address] = account.Clone()
	}
	return cloned
}

// VerifyTransactionAuthorization 在读取链状态前验证屏蔽交易的固定成本授权数据 + 交易池可先拒绝随机证明，避免放大后续状态预执行开销。
func (program Program) VerifyTransactionAuthorization(transaction structure.Transaction, transactionFee uint64) error {
	if err := transaction.Validate(); err != nil {
		return err
	}
	message, err := transaction.OrdexusMessage()
	if err != nil {
		return err
	}
	resolvedMessage, err := structure.NewResolvedMessage(message, structure.LoadedAddresses{})
	if err != nil {
		return err
	}
	feeSource := structure.FeePaymentSourceTransparentAccount
	if resolvedMessage.Header.NumRequiredSignatures == 0 {
		feeSource = structure.FeePaymentSourceShieldedPool
	}
	shieldedInstructionCount := 0
	for instructionIndex, compiledInstruction := range resolvedMessage.Instructions {
		if int(compiledInstruction.ProgramIDIndex) >= len(resolvedMessage.AccountKeys) || resolvedMessage.AccountKeys[compiledInstruction.ProgramIDIndex] != program.programID {
			continue
		}
		shieldedInstructionCount++
		instruction, err := structure.UnmarshalShieldedTransferInstructionBinary(compiledInstruction.Data)
		if err != nil {
			return err
		}
		if instruction.Bundle.ConsensusBranchID != structure.ShieldedConsensusBranchID {
			return fmt.Errorf("%w: unsupported consensus branch %08x", structure.ErrInvalidShieldedBundle, instruction.Bundle.ConsensusBranchID)
		}
		if err := validateEncryptedOutputs(instruction.Bundle.Actions); err != nil {
			return err
		}
		preflightContext := runtime.InstructionContext{
			InstructionIndex: instructionIndex,
			Instruction:      compiledInstruction,
			Message:          resolvedMessage,
			TransactionFee:   transactionFee,
			FeeSource:        feeSource,
			BuiltinPrograms:  structure.DefaultBuiltinProgramIDs,
		}
		accounts, err := program.resolveInstructionAccounts(preflightContext, instruction)
		if err != nil {
			return err
		}
		expectedBalance, err := expectedValueBalance(instruction, preflightContext)
		if err != nil {
			return err
		}
		if instruction.Bundle.ValueBalance != expectedBalance {
			return fmt.Errorf("%w: value balance %d, want %d", structure.ErrInvalidShieldedBundle, instruction.Bundle.ValueBalance, expectedBalance)
		}
		contextBytes, err := structure.BuildShieldedProofContextBytes(
			instruction,
			program.programID,
			program.poolAddress,
			resolvedMessage,
			instructionIndex,
			preflightContext.FeeSource,
			transactionFee,
			accounts.ordered,
		)
		if err != nil {
			return err
		}
		publicAssignment, err := publicCircuitAssignment(instruction.Bundle, zk.ComputeShieldedContextDigest(contextBytes))
		if err != nil {
			return err
		}
		if err := zk.VerifyShieldedCircuit(program.verifyingKey, publicAssignment, instruction.Bundle.Proof); err != nil {
			return fmt.Errorf("%w: verify Groth16 proof: %v", structure.ErrInvalidShieldedBundle, err)
		}
	}
	if shieldedInstructionCount == 0 {
		return fmt.Errorf("%w: transaction does not contain a shielded instruction", structure.ErrInvalidInstruction)
	}
	return nil
}

func validateEncryptedOutputs(actions []structure.ShieldedAction) error {
	for actionIndex, action := range actions {
		if !action.EnabledOutput {
			continue
		}
		encryptedNote, err := walletshielded.ParseEncryptedNote(action.EncryptedNote)
		if err != nil {
			return fmt.Errorf("%w: action %d encrypted note: %v", structure.ErrInvalidShieldedAction, actionIndex, err)
		}
		if !bytes.Equal(action.EphemeralKey, encryptedNote.EphemeralPublicKey[:]) {
			return fmt.Errorf("%w: action %d ephemeral key mismatch", structure.ErrInvalidShieldedAction, actionIndex)
		}
	}
	return nil
}

func (program Program) resolveInstructionAccounts(context runtime.InstructionContext, instruction structure.ShieldedTransferInstruction) (instructionAccounts, error) {
	resolved := instructionAccounts{nullifierMarkers: make([]structure.PublicKey, 0, len(instruction.Bundle.Actions))}
	for _, accountIndex := range context.Instruction.AccountIndexes {
		if int(accountIndex) >= len(context.Message.AccountKeys) {
			return instructionAccounts{}, fmt.Errorf("%w: shielded account index is out of range", structure.ErrInvalidInstruction)
		}
		resolved.ordered = append(resolved.ordered, context.Message.AccountKeys[accountIndex])
	}
	cursor := 0
	readWritable := func(field string) (structure.PublicKey, error) {
		if cursor >= len(context.Instruction.AccountIndexes) {
			return structure.PublicKey{}, fmt.Errorf("%w: missing %s account", structure.ErrInvalidInstruction, field)
		}
		accountIndex := int(context.Instruction.AccountIndexes[cursor])
		cursor++
		if !runtime.IsWritableMessageAccount(accountIndex, context.Message) {
			return structure.PublicKey{}, fmt.Errorf("%w: %s account must be writable", structure.ErrInvalidInstruction, field)
		}
		return context.Message.AccountKeys[accountIndex], nil
	}
	poolAddress, err := readWritable("pool")
	if err != nil {
		return instructionAccounts{}, err
	}
	if poolAddress != program.poolAddress {
		return instructionAccounts{}, fmt.Errorf("%w: unexpected shielded pool address", structure.ErrInvalidInstruction)
	}
	resolved.poolAddress = poolAddress
	if instruction.TransparentInputAmount > 0 {
		sourceAddress, err := readWritable("transparent source")
		if err != nil {
			return instructionAccounts{}, err
		}
		if !runtime.IsSignerAddress(sourceAddress, context.Message) {
			return instructionAccounts{}, fmt.Errorf("%w: transparent input source must sign", structure.ErrMissingRequiredSignature)
		}
		resolved.transparentSource = &sourceAddress
	}
	if instruction.TransparentOutputAmount > 0 {
		targetAddress, err := readWritable("transparent destination")
		if err != nil {
			return instructionAccounts{}, err
		}
		if targetAddress == program.poolAddress {
			return instructionAccounts{}, fmt.Errorf("%w: transparent destination cannot be the shielded pool", structure.ErrInvalidInstruction)
		}
		resolved.transparentTarget = &targetAddress
	}
	for _, action := range instruction.Bundle.Actions {
		if !action.EnabledSpend {
			continue
		}
		markerAddress, err := readWritable("nullifier marker")
		if err != nil {
			return instructionAccounts{}, err
		}
		expectedMarkerAddress := structure.DeriveShieldedNullifierAccount(program.programID, action.Nullifier)
		if markerAddress != expectedMarkerAddress {
			return instructionAccounts{}, fmt.Errorf("%w: nullifier marker address mismatch", structure.ErrInvalidInstruction)
		}
		resolved.nullifierMarkers = append(resolved.nullifierMarkers, markerAddress)
	}
	if cursor != len(context.Instruction.AccountIndexes) {
		return instructionAccounts{}, fmt.Errorf("%w: unexpected shielded instruction accounts", structure.ErrInvalidInstruction)
	}
	return resolved, nil
}

func (program Program) loadPool(context runtime.InstructionContext, poolAddress structure.PublicKey) (structure.ShieldedPoolState, structure.Account, uint64, error) {
	poolAccount, exists := context.Accounts[poolAddress]
	if !exists {
		return structure.ShieldedPoolState{}, structure.Account{}, 0, fmt.Errorf("%w: shielded pool is missing", structure.ErrInvalidLoadedTransaction)
	}
	if poolAccount.Owner != program.programID || poolAccount.Executable {
		return structure.ShieldedPoolState{}, structure.Account{}, 0, fmt.Errorf("%w: shielded pool owner mismatch", structure.ErrInvalidShieldedPoolState)
	}
	state, err := structure.UnmarshalShieldedPoolStateBinary(poolAccount.Data)
	if err != nil {
		return structure.ShieldedPoolState{}, structure.Account{}, 0, err
	}
	if state.ProtocolVersion != structure.ShieldedProtocolVersion || state.CircuitID != zk.ShieldedCircuitID || state.TreeDepth != zk.ShieldedMerkleDepth {
		return structure.ShieldedPoolState{}, structure.Account{}, 0, fmt.Errorf("%w: pool circuit configuration mismatch", structure.ErrInvalidShieldedPoolState)
	}
	if state.VerifyingKeyHash != program.verifyingKeyHash {
		return structure.ShieldedPoolState{}, structure.Account{}, 0, fmt.Errorf("%w: verifying key hash mismatch", structure.ErrInvalidShieldedPoolState)
	}
	if poolAccount.Lamports < state.PoolLamports {
		return structure.ShieldedPoolState{}, structure.Account{}, 0, fmt.Errorf("%w: pool liabilities exceed physical balance", structure.ErrInvalidShieldedPoolState)
	}
	return state, poolAccount, poolAccount.Lamports - state.PoolLamports, nil
}

func (program Program) validateNullifierMarkers(context runtime.InstructionContext, instruction structure.ShieldedTransferInstruction, markerAddresses []structure.PublicKey) error {
	markerIndex := 0
	for _, action := range instruction.Bundle.Actions {
		if !action.EnabledSpend {
			continue
		}
		markerAddress := markerAddresses[markerIndex]
		markerIndex++
		markerAccount, exists := context.Accounts[markerAddress]
		if !exists {
			return fmt.Errorf("%w: nullifier marker placeholder is missing", structure.ErrInvalidLoadedTransaction)
		}
		if markerAccount.Owner == program.programID {
			return fmt.Errorf("%w: nullifier already spent or marker occupied", structure.ErrInvalidShieldedBundle)
		}
		if markerAccount.Owner != context.BuiltinPrograms.System || len(markerAccount.Data) != 0 || markerAccount.Executable {
			return fmt.Errorf("%w: nullifier marker address is occupied", structure.ErrInvalidShieldedBundle)
		}
	}
	return nil
}

func (program Program) storeNullifierMarkers(context runtime.InstructionContext, instruction structure.ShieldedTransferInstruction, markerAddresses []structure.PublicKey) error {
	markerIndex := 0
	for _, action := range instruction.Bundle.Actions {
		if !action.EnabledSpend {
			continue
		}
		markerAddress := markerAddresses[markerIndex]
		markerIndex++
		markerAccount := context.Accounts[markerAddress].Clone()
		markerAccount.Owner = program.programID
		// nullifier 已绑定在派生地址中；只写 owner tombstone，避免凭空引入租金负债。
		markerAccount.Data = nil
		context.Accounts[markerAddress] = markerAccount
	}
	return nil
}

func publicCircuitAssignment(bundle structure.ShieldedBundle, contextDigest *big.Int) (*zk.ShieldedCircuit, error) {
	assignment := &zk.ShieldedCircuit{Anchor: new(big.Int).SetBytes(bundle.Anchor[:]), ContextDigest: contextDigest}
	for actionIndex := 0; actionIndex < zk.ShieldedActionCount; actionIndex++ {
		// gnark 即使只构造 public witness，也要求禁用槽位携带显式零值。
		assignment.SpendEnabled[actionIndex] = 0
		assignment.OutputEnabled[actionIndex] = 0
		assignment.Nullifiers[actionIndex] = 0
		assignment.OutputCommitments[actionIndex] = 0
	}
	if bundle.ValueBalance >= 0 {
		assignment.ValueBalanceDirection = 0
		assignment.ValueBalanceMagnitude = uint64(bundle.ValueBalance)
	} else {
		assignment.ValueBalanceDirection = 1
		assignment.ValueBalanceMagnitude = absoluteInt64(bundle.ValueBalance)
	}
	for actionIndex, action := range bundle.Actions {
		if action.EnabledSpend {
			nullifier, err := zk.ShieldedFieldFromBytes(action.Nullifier[:])
			if err != nil {
				return nil, fmt.Errorf("%w: action %d nullifier: %v", structure.ErrInvalidShieldedBundle, actionIndex, err)
			}
			assignment.SpendEnabled[actionIndex] = 1
			assignment.Nullifiers[actionIndex] = nullifier
		}
		if action.EnabledOutput {
			commitment, err := zk.ShieldedFieldFromBytes(action.NoteCommitment[:])
			if err != nil {
				return nil, fmt.Errorf("%w: action %d commitment: %v", structure.ErrInvalidShieldedBundle, actionIndex, err)
			}
			assignment.OutputEnabled[actionIndex] = 1
			assignment.OutputCommitments[actionIndex] = commitment
		}
	}
	return assignment, nil
}

func expectedValueBalance(instruction structure.ShieldedTransferInstruction, context runtime.InstructionContext) (int64, error) {
	shieldedFee := uint64(0)
	if context.FeeSource == structure.FeePaymentSourceShieldedPool {
		if context.Message.Header.NumRequiredSignatures != 0 || instruction.TransparentInputAmount != 0 {
			return 0, fmt.Errorf("%w: shielded fee source cannot have transparent signers or inputs", structure.ErrInvalidShieldedBundle)
		}
		shieldedFee = context.TransactionFee
	} else if context.FeeSource == structure.FeePaymentSourceTransparentAccount {
		if context.Message.Header.NumRequiredSignatures == 0 {
			return 0, fmt.Errorf("%w: transparent fee source requires a signer", structure.ErrMissingRequiredSignature)
		}
	} else {
		return 0, fmt.Errorf("%w: unsupported fee source", structure.ErrInvalidShieldedBundle)
	}
	positiveSide, err := checkedAddUint64(instruction.TransparentOutputAmount, shieldedFee)
	if err != nil || positiveSide > math.MaxInt64 {
		return 0, fmt.Errorf("%w: transparent output and fee overflow", structure.ErrInvalidShieldedBundle)
	}
	return int64(positiveSide) - int64(instruction.TransparentInputAmount), nil
}

func applyValueBalance(poolLamports uint64, valueBalance int64) (uint64, error) {
	if valueBalance >= 0 {
		amount := uint64(valueBalance)
		if poolLamports < amount {
			return 0, fmt.Errorf("%w: shielded pool balance is insufficient", structure.ErrInvalidShieldedBundle)
		}
		return poolLamports - amount, nil
	}
	return checkedAddUint64(poolLamports, absoluteInt64(valueBalance))
}

func absoluteInt64(value int64) uint64 {
	if value >= 0 {
		return uint64(value)
	}
	return uint64(-(value + 1)) + 1
}

func checkedAddUint64(left, right uint64) (uint64, error) {
	if left > math.MaxUint64-right {
		return 0, fmt.Errorf("%w: lamport overflow", structure.ErrInvalidShieldedPoolState)
	}
	return left + right, nil
}
