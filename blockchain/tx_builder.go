package blockchain

import (
	"fmt"
	"time"

	"solana_golang/programs/stake"
	"solana_golang/structure"
)

// NewTreasuryTransferTransaction 构造创世资金转账 + 资金分发必须走真实 system transfer。
func NewTreasuryTransferTransaction(treasury structure.SolanaKeyPair, destination structure.PublicKey, amount uint64, recentBlockhash structure.Hash) (structure.Transaction, error) {
	return NewTransferTransaction(treasury, destination, amount, recentBlockhash)
}

// NewTransferTransaction 构造普通转账交易 + 所有账户资金流转必须通过 system program 校验签名和余额。
func NewTransferTransaction(source structure.SolanaKeyPair, destination structure.PublicKey, amount uint64, recentBlockhash structure.Hash) (structure.Transaction, error) {
	instruction, err := structure.NewTransferInstruction(structure.TransferParams{Lamports: amount})
	if err != nil {
		return structure.Transaction{}, err
	}
	data, err := instruction.MarshalBinary()
	if err != nil {
		return structure.Transaction{}, fmt.Errorf("blockchain: marshal transfer: %w", err)
	}
	transaction := structure.Transaction{
		Accounts: []structure.AccountMeta{
			{PublicKey: source.PublicKey, IsSigner: true, IsWritable: true},
			{PublicKey: destination, IsSigner: false, IsWritable: true},
			{PublicKey: structure.DefaultBuiltinProgramIDs.System, IsSigner: false, IsWritable: false},
		},
		Instructions: []structure.CompiledInstruction{{
			ProgramIDIndex: 2,
			AccountIndexes: []uint8{0, 1},
			Data:           data,
		}},
		RecentBlockhash: recentBlockhash,
		SubmitTime:      time.Now().UnixMilli(),
	}
	return transaction.Sign(map[structure.PublicKey][]byte{source.PublicKey: source.PrivateKey})
}

// NewRegisterValidatorTransaction 构造验证者注册交易 + 新节点必须链上注册并质押后才可选 leader。
func NewRegisterValidatorTransaction(staker structure.SolanaKeyPair, validatorAccount structure.PublicKey, consensusPublicKey structure.PublicKey, p2pPeerID string, amount uint64, recentBlockhash structure.Hash) (structure.Transaction, error) {
	instruction, err := stake.NewRegisterValidatorInstruction(consensusPublicKey, p2pPeerID, 0, amount)
	if err != nil {
		return structure.Transaction{}, err
	}
	data, err := instruction.MarshalBinary()
	if err != nil {
		return structure.Transaction{}, fmt.Errorf("blockchain: marshal register validator: %w", err)
	}
	transaction := structure.Transaction{
		Accounts: []structure.AccountMeta{
			{PublicKey: staker.PublicKey, IsSigner: true, IsWritable: true},
			{PublicKey: validatorAccount, IsSigner: false, IsWritable: true},
			{PublicKey: structure.DefaultBuiltinProgramIDs.Stake, IsSigner: false, IsWritable: false},
		},
		Instructions: []structure.CompiledInstruction{{
			ProgramIDIndex: 2,
			AccountIndexes: []uint8{0, 1},
			Data:           data,
		}},
		RecentBlockhash: recentBlockhash,
		SubmitTime:      time.Now().UnixMilli(),
	}
	return transaction.Sign(map[structure.PublicKey][]byte{staker.PublicKey: staker.PrivateKey})
}

// NewStakeTransaction 构造追加质押交易 + pending stake 通过 stake program 延迟到后续 epoch 生效。
func NewStakeTransaction(staker structure.SolanaKeyPair, validatorAccount structure.PublicKey, amount uint64, recentBlockhash structure.Hash) (structure.Transaction, error) {
	instruction, err := stake.NewStakeInstruction(amount)
	if err != nil {
		return structure.Transaction{}, err
	}
	return newSignedStakeTransaction(staker, validatorAccount, instruction, recentBlockhash)
}

// NewUnstakeTransaction 构造解除质押交易 + active stake 进入 unlocking 状态等待提取。
func NewUnstakeTransaction(staker structure.SolanaKeyPair, validatorAccount structure.PublicKey, amount uint64, unlockEpoch uint64, recentBlockhash structure.Hash) (structure.Transaction, error) {
	instruction, err := stake.NewUnstakeInstruction(amount, unlockEpoch)
	if err != nil {
		return structure.Transaction{}, err
	}
	return newSignedStakeTransaction(staker, validatorAccount, instruction, recentBlockhash)
}

// NewWithdrawUnstakedTransaction 构造提取解锁资金交易 + 只允许到期 unlocking stake 回到 staker。
func NewWithdrawUnstakedTransaction(staker structure.SolanaKeyPair, validatorAccount structure.PublicKey, currentEpoch uint64, recentBlockhash structure.Hash) (structure.Transaction, error) {
	instruction, err := stake.NewWithdrawUnstakedInstruction(currentEpoch)
	if err != nil {
		return structure.Transaction{}, err
	}
	return newSignedStakeTransaction(staker, validatorAccount, instruction, recentBlockhash)
}

// NewExitValidatorTransaction 构造退出验证者交易 + 下个 epoch 不再进入 active validator set。
func NewExitValidatorTransaction(staker structure.SolanaKeyPair, validatorAccount structure.PublicKey, recentBlockhash structure.Hash) (structure.Transaction, error) {
	instruction := stake.NewExitValidatorInstruction()
	return newSignedStakeTransaction(staker, validatorAccount, instruction, recentBlockhash)
}

// NewSlashValidatorTransaction 构造罚没交易 + slash 必须进入链上执行流程形成可审计状态变更。
func NewSlashValidatorTransaction(staker structure.SolanaKeyPair, validatorAccount structure.PublicKey, amount uint64, recentBlockhash structure.Hash) (structure.Transaction, error) {
	instruction, err := stake.NewSlashValidatorInstruction(amount)
	if err != nil {
		return structure.Transaction{}, err
	}
	return newSignedStakeTransaction(staker, validatorAccount, instruction, recentBlockhash)
}

// NewJailValidatorTransaction 构造 jail 交易 + jailed 状态写入 stake account 后由 epoch 快照排除。
func NewJailValidatorTransaction(staker structure.SolanaKeyPair, validatorAccount structure.PublicKey, jailUntilEpoch uint64, recentBlockhash structure.Hash) (structure.Transaction, error) {
	instruction, err := stake.NewJailValidatorInstruction(jailUntilEpoch)
	if err != nil {
		return structure.Transaction{}, err
	}
	return newSignedStakeTransaction(staker, validatorAccount, instruction, recentBlockhash)
}

func newSignedStakeTransaction(staker structure.SolanaKeyPair, validatorAccount structure.PublicKey, instruction stake.Instruction, recentBlockhash structure.Hash) (structure.Transaction, error) {
	data, err := instruction.MarshalBinary()
	if err != nil {
		return structure.Transaction{}, fmt.Errorf("blockchain: marshal stake instruction: %w", err)
	}
	transaction := structure.Transaction{
		Accounts: []structure.AccountMeta{
			{PublicKey: staker.PublicKey, IsSigner: true, IsWritable: true},
			{PublicKey: validatorAccount, IsSigner: false, IsWritable: true},
			{PublicKey: structure.DefaultBuiltinProgramIDs.Stake, IsSigner: false, IsWritable: false},
		},
		Instructions: []structure.CompiledInstruction{{
			ProgramIDIndex: 2,
			AccountIndexes: []uint8{0, 1},
			Data:           data,
		}},
		RecentBlockhash: recentBlockhash,
		SubmitTime:      time.Now().UnixMilli(),
	}
	return transaction.Sign(map[structure.PublicKey][]byte{staker.PublicKey: staker.PrivateKey})
}
