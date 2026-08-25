package structure

import (
	"bytes"
	"fmt"
	"math"

	"solana_golang/codec/borsh"
	"solana_golang/utils"
)

const (
	ShieldedInstructionEncodingVersion uint16 = 1
	ShieldedConsensusBranchID          uint32 = 0x53483200
	MaxShieldedInstructionBytes               = MaxSolanaTransactionSize
	MaxShieldedTransparentAmount              = uint64(math.MaxInt64)
)

const (
	shieldedPoolAddressDomain      = "solana_golang.shielded.pool.v2"
	shieldedNullifierAccountDomain = "solana_golang.shielded.nullifier_account.v2"
)

// DefaultShieldedPoolAddress 是全链唯一屏蔽池账户 + 所有 z 地址共享同一承诺树和匿名集。
var DefaultShieldedPoolAddress = DeriveShieldedPoolAddress(DefaultBuiltinProgramIDs.Shielded)

// ShieldedTransferInstruction 统一承载 t→z、z→t 与 z→z + 透明侧金额仅用于守恒关系和实际转账。
type ShieldedTransferInstruction struct {
	EncodingVersion         uint16
	TransparentInputAmount  uint64
	TransparentOutputAmount uint64
	Bundle                  ShieldedBundle
}

// DeriveShieldedPoolAddress 从程序 ID 派生唯一全局池地址。
func DeriveShieldedPoolAddress(programID PublicKey) PublicKey {
	encoded := make([]byte, 0, len(shieldedPoolAddressDomain)+PublicKeySize)
	encoded = append(encoded, shieldedPoolAddressDomain...)
	encoded = append(encoded, programID[:]...)
	digest := utils.SHA256(encoded)
	var address PublicKey
	copy(address[:], digest)
	return address
}

// DeriveShieldedNullifierAccount 为 nullifier 派生确定性标记账户地址。
func DeriveShieldedNullifierAccount(programID PublicKey, nullifier Hash) PublicKey {
	encoded := make([]byte, 0, len(shieldedNullifierAccountDomain)+PublicKeySize+HashSize)
	encoded = append(encoded, shieldedNullifierAccountDomain...)
	encoded = append(encoded, programID[:]...)
	encoded = append(encoded, nullifier[:]...)
	digest := utils.SHA256(encoded)
	var address PublicKey
	copy(address[:], digest)
	return address
}

// ValidatePublicData 校验生成证明前已确定的公开数据 + Proof 字段可以暂时为空。
func (instruction ShieldedTransferInstruction) ValidatePublicData() error {
	if instruction.EncodingVersion != ShieldedInstructionEncodingVersion {
		return fmt.Errorf("%w: unsupported instruction encoding version %d", ErrInvalidShieldedBundle, instruction.EncodingVersion)
	}
	if instruction.TransparentInputAmount > MaxShieldedTransparentAmount {
		return fmt.Errorf("%w: transparent input exceeds %d", ErrInvalidShieldedBundle, MaxShieldedTransparentAmount)
	}
	if instruction.TransparentOutputAmount > MaxShieldedTransparentAmount {
		return fmt.Errorf("%w: transparent output exceeds %d", ErrInvalidShieldedBundle, MaxShieldedTransparentAmount)
	}
	bundle := instruction.Bundle.Clone()
	if len(bundle.Proof) == 0 {
		bundle.Proof = make([]byte, ShieldedGroth16ProofBytes)
		bundle.Proof[0] = 1
	}
	return bundle.Validate()
}

// Validate 校验完整屏蔽指令 + 共识执行前证明字段必须存在。
func (instruction ShieldedTransferInstruction) Validate() error {
	if err := instruction.ValidatePublicData(); err != nil {
		return err
	}
	return instruction.Bundle.Validate()
}

// AuthorizationBytes 返回证明绑定的规范公开字节 + 排除 Proof 避免自引用。
func (instruction ShieldedTransferInstruction) AuthorizationBytes() ([]byte, error) {
	if err := instruction.ValidatePublicData(); err != nil {
		return nil, err
	}
	writer := borsh.NewWriter(MaxShieldedInstructionBytes)
	writer.WriteUint16(instruction.EncodingVersion)
	writer.WriteUint64(instruction.TransparentInputAmount)
	writer.WriteUint64(instruction.TransparentOutputAmount)
	writer.WriteUint16(instruction.Bundle.ProtocolVersion)
	writer.WriteUint32(instruction.Bundle.ConsensusBranchID)
	writer.WriteFixedBytes(instruction.Bundle.Anchor[:])
	writer.WriteUint32(uint32(len(instruction.Bundle.Actions)))
	for actionIndex, action := range instruction.Bundle.Actions {
		if err := writeShieldedAction(writer, action); err != nil {
			return nil, fmt.Errorf("structure: encode shielded authorization action %d: %w", actionIndex, err)
		}
	}
	writer.WriteInt64(instruction.Bundle.ValueBalance)
	encoded := writer.Bytes()
	if len(encoded) > MaxShieldedInstructionBytes {
		return nil, fmt.Errorf("%w: authorization bytes exceed %d", ErrInvalidShieldedBundle, MaxShieldedInstructionBytes)
	}
	return encoded, nil
}

// BuildShieldedProofContextBytes 编码证明必须绑定的完整规范交易上下文。
// 全部屏蔽证明字节会先被替换为公开授权数据，避免证明自引用和多指令循环依赖。
func BuildShieldedProofContextBytes(
	instruction ShieldedTransferInstruction,
	programID PublicKey,
	poolAddress PublicKey,
	message ResolvedMessage,
	instructionIndex int,
	feeSource FeePaymentSource,
	transactionFee uint64,
	instructionAccounts []PublicKey,
) ([]byte, error) {
	authorizationBytes, err := instruction.AuthorizationBytes()
	if err != nil {
		return nil, err
	}
	if programID.IsZero() || poolAddress.IsZero() || message.RecentBlockhash.IsZero() {
		return nil, fmt.Errorf("%w: proof context contains an empty identifier", ErrInvalidShieldedBundle)
	}
	if feeSource != FeePaymentSourceTransparentAccount && feeSource != FeePaymentSourceShieldedPool {
		return nil, fmt.Errorf("%w: invalid fee source %d", ErrInvalidShieldedBundle, feeSource)
	}
	if len(instructionAccounts) == 0 || len(instructionAccounts) > MaxInstructionAccounts {
		return nil, fmt.Errorf("%w: invalid proof context account count %d", ErrInvalidShieldedBundle, len(instructionAccounts))
	}
	authorizationMessageBytes, err := buildShieldedProofAuthorizationMessage(message, programID, instructionIndex, authorizationBytes)
	if err != nil {
		return nil, err
	}
	writer := borsh.NewWriter(MaxShieldedInstructionBytes)
	if err := writer.WriteBytes(authorizationMessageBytes); err != nil {
		return nil, fmt.Errorf("structure: encode shielded authorization message: %w", err)
	}
	if err := writer.WriteBytes(authorizationBytes); err != nil {
		return nil, fmt.Errorf("structure: encode shielded authorization context: %w", err)
	}
	writer.WriteUint32(uint32(instructionIndex))
	writer.WriteFixedBytes(programID[:])
	writer.WriteFixedBytes(poolAddress[:])
	writer.WriteFixedBytes(message.RecentBlockhash[:])
	writer.WriteUint8(uint8(feeSource))
	writer.WriteUint64(transactionFee)
	writer.WriteUint32(uint32(len(instructionAccounts)))
	for _, address := range instructionAccounts {
		writer.WriteFixedBytes(address[:])
	}
	return writer.Bytes(), nil
}

func buildShieldedProofAuthorizationMessage(
	message ResolvedMessage,
	programID PublicKey,
	instructionIndex int,
	expectedInstructionAuthorization []byte,
) ([]byte, error) {
	if err := message.Validate(); err != nil {
		return nil, fmt.Errorf("%w: invalid proof context message: %v", ErrInvalidShieldedBundle, err)
	}
	if len(message.LoadedAddresses.Writable) != 0 || len(message.LoadedAddresses.Readonly) != 0 || len(message.AccountKeys) != len(message.StaticAccountKeys) {
		return nil, fmt.Errorf("%w: address table messages are not supported by shielded proof context", ErrInvalidShieldedBundle)
	}
	for accountIndex := range message.AccountKeys {
		if message.AccountKeys[accountIndex] != message.StaticAccountKeys[accountIndex] {
			return nil, fmt.Errorf("%w: resolved and static proof context accounts differ", ErrInvalidShieldedBundle)
		}
	}
	if instructionIndex < 0 || instructionIndex >= len(message.Instructions) {
		return nil, fmt.Errorf("%w: shielded instruction index %d is out of range", ErrInvalidShieldedBundle, instructionIndex)
	}

	authorizationInstructions := cloneInstructions(message.Instructions)
	for currentInstructionIndex := range authorizationInstructions {
		compiledInstruction := &authorizationInstructions[currentInstructionIndex]
		if int(compiledInstruction.ProgramIDIndex) >= len(message.AccountKeys) {
			return nil, fmt.Errorf("%w: shielded program index is out of range", ErrInvalidShieldedBundle)
		}
		if message.AccountKeys[compiledInstruction.ProgramIDIndex] != programID {
			continue
		}
		shieldedInstruction, err := UnmarshalShieldedTransferInstructionBinary(compiledInstruction.Data)
		if err != nil {
			return nil, fmt.Errorf("%w: decode shielded instruction %d: %v", ErrInvalidShieldedBundle, currentInstructionIndex, err)
		}
		instructionAuthorization, err := shieldedInstruction.AuthorizationBytes()
		if err != nil {
			return nil, err
		}
		if currentInstructionIndex == instructionIndex && !bytes.Equal(instructionAuthorization, expectedInstructionAuthorization) {
			return nil, fmt.Errorf("%w: shielded instruction authorization does not match message", ErrInvalidShieldedBundle)
		}
		compiledInstruction.Data = instructionAuthorization
	}
	if int(message.Instructions[instructionIndex].ProgramIDIndex) >= len(message.AccountKeys) || message.AccountKeys[message.Instructions[instructionIndex].ProgramIDIndex] != programID {
		return nil, fmt.Errorf("%w: proof context instruction does not target shielded program", ErrInvalidShieldedBundle)
	}

	authorizationMessage := SolanaMessage{
		Header:          message.Header,
		AccountKeys:     clonePublicKeys(message.StaticAccountKeys),
		RecentBlockhash: message.RecentBlockhash,
		Instructions:    authorizationInstructions,
	}
	encoded, err := authorizationMessage.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("structure: encode shielded proof authorization message: %w", err)
	}
	return encoded, nil
}

// MarshalBinary 序列化完整屏蔽指令。
func (instruction ShieldedTransferInstruction) MarshalBinary() ([]byte, error) {
	if err := instruction.Validate(); err != nil {
		return nil, err
	}
	bundleBytes, err := instruction.Bundle.MarshalBinary()
	if err != nil {
		return nil, err
	}
	writer := borsh.NewWriter(MaxShieldedInstructionBytes)
	writer.WriteUint16(instruction.EncodingVersion)
	writer.WriteUint64(instruction.TransparentInputAmount)
	writer.WriteUint64(instruction.TransparentOutputAmount)
	if err := writer.WriteBytes(bundleBytes); err != nil {
		return nil, fmt.Errorf("structure: encode shielded instruction bundle: %w", err)
	}
	encoded := writer.Bytes()
	if len(encoded) > MaxShieldedInstructionBytes {
		return nil, fmt.Errorf("%w: instruction bytes exceed %d", ErrInvalidShieldedBundle, MaxShieldedInstructionBytes)
	}
	return encoded, nil
}

// UnmarshalShieldedTransferInstructionBinary 反序列化完整屏蔽指令并拒绝尾随字节。
func UnmarshalShieldedTransferInstructionBinary(data []byte) (ShieldedTransferInstruction, error) {
	if len(data) == 0 || len(data) > MaxShieldedInstructionBytes {
		return ShieldedTransferInstruction{}, fmt.Errorf("%w: invalid instruction byte length %d", ErrInvalidShieldedBundle, len(data))
	}
	reader := borsh.NewReader(data, MaxShieldedInstructionBytes)
	encodingVersion, err := reader.ReadUint16()
	if err != nil {
		return ShieldedTransferInstruction{}, fmt.Errorf("structure: decode shielded instruction version: %w", err)
	}
	transparentInputAmount, err := reader.ReadUint64()
	if err != nil {
		return ShieldedTransferInstruction{}, fmt.Errorf("structure: decode transparent input: %w", err)
	}
	transparentOutputAmount, err := reader.ReadUint64()
	if err != nil {
		return ShieldedTransferInstruction{}, fmt.Errorf("structure: decode transparent output: %w", err)
	}
	bundleBytes, err := reader.ReadBytes()
	if err != nil {
		return ShieldedTransferInstruction{}, fmt.Errorf("structure: decode shielded instruction bundle: %w", err)
	}
	if err := reader.EnsureEOF(); err != nil {
		return ShieldedTransferInstruction{}, fmt.Errorf("structure: decode shielded instruction eof: %w", err)
	}
	bundle, err := UnmarshalShieldedBundleBinary(bundleBytes)
	if err != nil {
		return ShieldedTransferInstruction{}, err
	}
	instruction := ShieldedTransferInstruction{
		EncodingVersion:         encodingVersion,
		TransparentInputAmount:  transparentInputAmount,
		TransparentOutputAmount: transparentOutputAmount,
		Bundle:                  bundle,
	}
	return instruction, instruction.Validate()
}
