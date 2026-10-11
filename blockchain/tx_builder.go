package blockchain

import (
	"fmt"
	"time"

	bpfloader "github.com/tentechtop/ordexus-core/programs/bpfloader"
	"github.com/tentechtop/ordexus-core/programs/stake"
	"github.com/tentechtop/ordexus-core/structure"
	ovm "github.com/tentechtop/ordexus-core/vm"
)

// NewTreasuryTransferTransaction 构造创世资金转账 + 资金分发必须走真实 system transfer。
func NewTreasuryTransferTransaction(treasury structure.OrdexusKeyPair, destination structure.PublicKey, amount uint64, recentBlockhash structure.Hash) (structure.Transaction, error) {
	return NewTransferTransaction(treasury, destination, amount, recentBlockhash)
}

// NewTransferTransaction 构造普通转账交易 + 所有账户资金流转必须通过 system program 校验签名和余额。
func NewTransferTransaction(source structure.OrdexusKeyPair, destination structure.PublicKey, amount uint64, recentBlockhash structure.Hash) (structure.Transaction, error) {
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

// NewDeployContractTransaction 构造 VM 合约部署交易 + 同一交易内创建程序账户并写入可执行字节码。
func NewDeployContractTransaction(payer structure.OrdexusKeyPair, program structure.OrdexusKeyPair, programData []byte, depositLamports uint64, recentBlockhash structure.Hash) (structure.Transaction, error) {
	return NewDeployContractTransactionWithSigners(payer, program, programData, depositLamports, nil, recentBlockhash)
}

// NewDeployContractTransactionWithSigners 构造带 Manifest authority 签名的单交易部署。
func NewDeployContractTransactionWithSigners(
	payer structure.OrdexusKeyPair,
	program structure.OrdexusKeyPair,
	programData []byte,
	depositLamports uint64,
	additionalSigners []structure.OrdexusKeyPair,
	recentBlockhash structure.Hash,
) (structure.Transaction, error) {
	if len(programData) == 0 {
		return structure.Transaction{}, fmt.Errorf("blockchain: program data is empty")
	}
	signerAccounts, privateKeys, err := deploymentSignerConfiguration(payer, program, programData, additionalSigners)
	if err != nil {
		return structure.Transaction{}, err
	}
	minimumBalance, err := structure.MinimumBalanceForRentExemption(len(programData))
	if err != nil {
		return structure.Transaction{}, fmt.Errorf("blockchain: calculate program rent: %w", err)
	}
	programLamports, err := safeAddLamports(minimumBalance, depositLamports)
	if err != nil {
		return structure.Transaction{}, fmt.Errorf("blockchain: calculate program lamports: %w", err)
	}
	createInstruction, err := structure.NewCreateAccountInstruction(structure.CreateAccountParams{
		Lamports: programLamports,
		Space:    uint64(len(programData)),
		Owner:    structure.DefaultBuiltinProgramIDs.BPFLoader,
	})
	if err != nil {
		return structure.Transaction{}, fmt.Errorf("blockchain: build program account: %w", err)
	}
	createData, err := createInstruction.MarshalBinary()
	if err != nil {
		return structure.Transaction{}, fmt.Errorf("blockchain: marshal program account: %w", err)
	}
	deployInstruction, err := bpfloader.NewDeployInstruction(programData)
	if err != nil {
		return structure.Transaction{}, fmt.Errorf("blockchain: build deploy instruction: %w", err)
	}
	deployData, err := deployInstruction.MarshalBinary()
	if err != nil {
		return structure.Transaction{}, fmt.Errorf("blockchain: marshal deploy instruction: %w", err)
	}
	systemProgramIndex := uint8(len(signerAccounts))
	loaderProgramIndex := systemProgramIndex + 1
	transaction := structure.Transaction{
		Accounts: append(signerAccounts,
			structure.AccountMeta{PublicKey: structure.DefaultBuiltinProgramIDs.System},
			structure.AccountMeta{PublicKey: structure.DefaultBuiltinProgramIDs.BPFLoader},
		),
		Instructions: []structure.CompiledInstruction{
			{
				ProgramIDIndex: systemProgramIndex,
				AccountIndexes: []uint8{0, 1},
				Data:           createData,
			},
			{
				ProgramIDIndex: loaderProgramIndex,
				AccountIndexes: []uint8{1},
				Data:           deployData,
			},
		},
		RecentBlockhash: recentBlockhash,
		SubmitTime:      time.Now().UnixMilli(),
	}
	return transaction.Sign(privateKeys)
}

// NewDeployContractTransactions 构造兼容大字节码的部署批次；程序只在全部分片写入后才被冻结为可执行账户。
func NewDeployContractTransactions(
	payer structure.OrdexusKeyPair,
	program structure.OrdexusKeyPair,
	programData []byte,
	depositLamports uint64,
	recentBlockhash structure.Hash,
) ([]structure.Transaction, error) {
	return NewDeployContractTransactionsWithSigners(payer, program, programData, depositLamports, nil, recentBlockhash)
}

// NewDeployContractTransactionsWithSigners 构造带 Manifest authority 签名的大字节码部署批次。
func NewDeployContractTransactionsWithSigners(
	payer structure.OrdexusKeyPair,
	program structure.OrdexusKeyPair,
	programData []byte,
	depositLamports uint64,
	additionalSigners []structure.OrdexusKeyPair,
	recentBlockhash structure.Hash,
) ([]structure.Transaction, error) {
	if len(programData) <= bpfloader.MaxLoaderTransactionDataBytes {
		transaction, err := NewDeployContractTransactionWithSigners(payer, program, programData, depositLamports, additionalSigners, recentBlockhash)
		if err != nil {
			return nil, err
		}
		return []structure.Transaction{transaction}, nil
	}
	if len(programData) > ovm.MaxProgramDataSize {
		return nil, fmt.Errorf("blockchain: program data length %d exceeds %d", len(programData), ovm.MaxProgramDataSize)
	}
	finalizeSignerAccounts, finalizePrivateKeys, err := deploymentSignerConfiguration(payer, program, programData, additionalSigners)
	if err != nil {
		return nil, err
	}
	minimumBalance, err := structure.MinimumBalanceForRentExemption(len(programData))
	if err != nil {
		return nil, fmt.Errorf("blockchain: calculate program rent: %w", err)
	}
	programLamports, err := safeAddLamports(minimumBalance, depositLamports)
	if err != nil {
		return nil, fmt.Errorf("blockchain: calculate program lamports: %w", err)
	}
	createInstruction, err := structure.NewCreateAccountInstruction(structure.CreateAccountParams{
		Lamports: programLamports,
		Space:    uint64(len(programData)),
		Owner:    structure.DefaultBuiltinProgramIDs.BPFLoader,
	})
	if err != nil {
		return nil, fmt.Errorf("blockchain: build program account: %w", err)
	}
	createData, err := createInstruction.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("blockchain: marshal program account: %w", err)
	}
	createTransaction := structure.Transaction{
		Accounts: []structure.AccountMeta{
			{PublicKey: payer.PublicKey, IsSigner: true, IsWritable: true},
			{PublicKey: program.PublicKey, IsSigner: true, IsWritable: true},
			{PublicKey: structure.DefaultBuiltinProgramIDs.System},
		},
		Instructions: []structure.CompiledInstruction{{
			ProgramIDIndex: 2,
			AccountIndexes: []uint8{0, 1},
			Data:           createData,
		}},
		RecentBlockhash: recentBlockhash,
		SubmitTime:      time.Now().UnixMilli(),
	}
	privateKeys := map[structure.PublicKey][]byte{
		payer.PublicKey:   payer.PrivateKey,
		program.PublicKey: program.PrivateKey,
	}
	signedCreate, err := createTransaction.Sign(privateKeys)
	if err != nil {
		return nil, err
	}
	transactions := []structure.Transaction{signedCreate}
	for offset := 0; offset < len(programData); {
		end := offset + bpfloader.MaxLoaderTransactionDataBytes
		if end > len(programData) {
			end = len(programData)
		}
		writeInstruction, err := bpfloader.NewWriteInstruction(uint32(offset), programData[offset:end])
		if err != nil {
			return nil, fmt.Errorf("blockchain: build program write at %d: %w", offset, err)
		}
		writeData, err := writeInstruction.MarshalBinary()
		if err != nil {
			return nil, fmt.Errorf("blockchain: marshal program write at %d: %w", offset, err)
		}
		writeTransaction := structure.Transaction{
			Accounts: []structure.AccountMeta{
				{PublicKey: payer.PublicKey, IsSigner: true, IsWritable: true},
				{PublicKey: program.PublicKey, IsSigner: true, IsWritable: true},
				{PublicKey: structure.DefaultBuiltinProgramIDs.BPFLoader},
			},
			Instructions: []structure.CompiledInstruction{{
				ProgramIDIndex: 2,
				AccountIndexes: []uint8{1},
				Data:           writeData,
			}},
			RecentBlockhash: recentBlockhash,
			SubmitTime:      time.Now().UnixMilli(),
		}
		signedWrite, err := writeTransaction.Sign(privateKeys)
		if err != nil {
			return nil, err
		}
		transactions = append(transactions, signedWrite)
		offset = end
	}
	finalizeData, err := bpfloader.NewFinalizeInstruction().MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("blockchain: marshal program finalize: %w", err)
	}
	finalizeTransaction := structure.Transaction{
		Accounts: append(finalizeSignerAccounts, structure.AccountMeta{PublicKey: structure.DefaultBuiltinProgramIDs.BPFLoader}),
		Instructions: []structure.CompiledInstruction{{
			ProgramIDIndex: uint8(len(finalizeSignerAccounts)),
			AccountIndexes: []uint8{1},
			Data:           finalizeData,
		}},
		RecentBlockhash: recentBlockhash,
		SubmitTime:      time.Now().UnixMilli(),
	}
	signedFinalize, err := finalizeTransaction.Sign(finalizePrivateKeys)
	if err != nil {
		return nil, err
	}
	return append(transactions, signedFinalize), nil
}

func deploymentSignerConfiguration(
	payer structure.OrdexusKeyPair,
	program structure.OrdexusKeyPair,
	programData []byte,
	additionalSigners []structure.OrdexusKeyPair,
) ([]structure.AccountMeta, map[structure.PublicKey][]byte, error) {
	accounts := []structure.AccountMeta{
		{PublicKey: payer.PublicKey, IsSigner: true, IsWritable: true},
		{PublicKey: program.PublicKey, IsSigner: true, IsWritable: true},
	}
	privateKeys := map[structure.PublicKey][]byte{
		payer.PublicKey:   payer.PrivateKey,
		program.PublicKey: program.PrivateKey,
	}
	for index, signer := range additionalSigners {
		if signer.PublicKey.IsZero() {
			return nil, nil, fmt.Errorf("blockchain: deployment signer %d is empty", index)
		}
		if _, exists := privateKeys[signer.PublicKey]; exists {
			return nil, nil, fmt.Errorf("blockchain: duplicate deployment signer %s", signer.PublicKey.String())
		}
		privateKeys[signer.PublicKey] = signer.PrivateKey
		accounts = append(accounts, structure.AccountMeta{PublicKey: signer.PublicKey, IsSigner: true})
	}
	manifest, hasManifest, err := ovm.DecodeProgramManifest(programData)
	if err != nil {
		return nil, nil, fmt.Errorf("blockchain: decode deployment manifest: %w", err)
	}
	if !hasManifest || manifest.UpgradeAuthority == (ovm.Address{}) {
		return accounts, privateKeys, nil
	}
	var authority structure.PublicKey
	copy(authority[:], manifest.UpgradeAuthority[:])
	if _, exists := privateKeys[authority]; !exists {
		return nil, nil, fmt.Errorf("blockchain: missing private key for manifest upgrade authority %s", authority.String())
	}
	return accounts, privateKeys, nil
}

// NewRegisterValidatorTransaction 构造验证者注册交易 + 新节点必须链上注册并质押后才可选 leader。
func NewRegisterValidatorTransaction(staker structure.OrdexusKeyPair, validatorAccount structure.PublicKey, consensusPublicKey structure.PublicKey, p2pPeerID string, amount uint64, recentBlockhash structure.Hash) (structure.Transaction, error) {
	return NewRegisterValidatorTransactionWithBLS(staker, validatorAccount, consensusPublicKey, nil, p2pPeerID, amount, recentBlockhash)
}

// NewRegisterValidatorTransactionWithBLS 构造带 BLS 公钥的注册交易 + 高性能 QC 需要链上绑定聚合验签公钥。
func NewRegisterValidatorTransactionWithBLS(staker structure.OrdexusKeyPair, validatorAccount structure.PublicKey, consensusPublicKey structure.PublicKey, blsPublicKey []byte, p2pPeerID string, amount uint64, recentBlockhash structure.Hash) (structure.Transaction, error) {
	instruction, err := stake.NewRegisterValidatorInstructionWithBLS(consensusPublicKey, blsPublicKey, p2pPeerID, 0, amount)
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

func safeAddLamports(left uint64, right uint64) (uint64, error) {
	if ^uint64(0)-left < right {
		return 0, fmt.Errorf("lamports overflow")
	}
	return left + right, nil
}

// NewStakeTransaction 构造追加质押交易 + pending stake 通过 stake program 延迟到后续 epoch 生效。
func NewStakeTransaction(staker structure.OrdexusKeyPair, validatorAccount structure.PublicKey, amount uint64, recentBlockhash structure.Hash) (structure.Transaction, error) {
	instruction, err := stake.NewStakeInstruction(amount)
	if err != nil {
		return structure.Transaction{}, err
	}
	return newSignedStakeTransaction(staker, validatorAccount, instruction, recentBlockhash)
}

// NewDelegateStakeTransaction 构造 DPoS 委托交易 + 普通用户本地签名后通过 sendTransaction 广播。
func NewDelegateStakeTransaction(delegator structure.OrdexusKeyPair, validatorAccount structure.PublicKey, amount uint64, recentBlockhash structure.Hash) (structure.Transaction, error) {
	instruction, err := stake.NewDelegateInstruction(amount)
	if err != nil {
		return structure.Transaction{}, err
	}
	return newSignedStakeTransaction(delegator, validatorAccount, instruction, recentBlockhash)
}

// NewUndelegateStakeTransaction 构造 DPoS 取消委托交易 + active 委托进入 unlocking 状态。
func NewUndelegateStakeTransaction(delegator structure.OrdexusKeyPair, validatorAccount structure.PublicKey, amount uint64, unlockEpoch uint64, recentBlockhash structure.Hash) (structure.Transaction, error) {
	instruction, err := stake.NewUndelegateInstruction(amount, unlockEpoch)
	if err != nil {
		return structure.Transaction{}, err
	}
	return newSignedStakeTransaction(delegator, validatorAccount, instruction, recentBlockhash)
}

// NewWithdrawDelegationTransaction 构造 DPoS 委托提现交易 + 到期资金回到委托人账户。
func NewWithdrawDelegationTransaction(delegator structure.OrdexusKeyPair, validatorAccount structure.PublicKey, currentEpoch uint64, recentBlockhash structure.Hash) (structure.Transaction, error) {
	instruction, err := stake.NewWithdrawDelegationInstruction(currentEpoch)
	if err != nil {
		return structure.Transaction{}, err
	}
	return newSignedStakeTransaction(delegator, validatorAccount, instruction, recentBlockhash)
}

// NewUnstakeTransaction 构造解除质押交易 + active stake 进入 unlocking 状态等待提取。
func NewUnstakeTransaction(staker structure.OrdexusKeyPair, validatorAccount structure.PublicKey, amount uint64, unlockEpoch uint64, recentBlockhash structure.Hash) (structure.Transaction, error) {
	instruction, err := stake.NewUnstakeInstruction(amount, unlockEpoch)
	if err != nil {
		return structure.Transaction{}, err
	}
	return newSignedStakeTransaction(staker, validatorAccount, instruction, recentBlockhash)
}

// NewWithdrawUnstakedTransaction 构造提取解锁资金交易 + 只允许到期 unlocking stake 回到 staker。
func NewWithdrawUnstakedTransaction(staker structure.OrdexusKeyPair, validatorAccount structure.PublicKey, currentEpoch uint64, recentBlockhash structure.Hash) (structure.Transaction, error) {
	instruction, err := stake.NewWithdrawUnstakedInstruction(currentEpoch)
	if err != nil {
		return structure.Transaction{}, err
	}
	return newSignedStakeTransaction(staker, validatorAccount, instruction, recentBlockhash)
}

// NewExitValidatorTransaction 构造退出验证者交易 + 下个 epoch 不再进入 active validator set。
func NewExitValidatorTransaction(staker structure.OrdexusKeyPair, validatorAccount structure.PublicKey, recentBlockhash structure.Hash) (structure.Transaction, error) {
	instruction := stake.NewExitValidatorInstruction()
	return newSignedStakeTransaction(staker, validatorAccount, instruction, recentBlockhash)
}

// NewSlashValidatorTransaction 构造罚没交易 + slash 必须进入链上执行流程形成可审计状态变更。
func NewSlashValidatorTransaction(staker structure.OrdexusKeyPair, validatorAccount structure.PublicKey, amount uint64, recentBlockhash structure.Hash) (structure.Transaction, error) {
	instruction, err := stake.NewSlashValidatorInstruction(amount)
	if err != nil {
		return structure.Transaction{}, err
	}
	return newSignedStakeTransaction(staker, validatorAccount, instruction, recentBlockhash)
}

// NewJailValidatorTransaction 构造 jail 交易 + jailed 状态写入 stake account 后由 epoch 快照排除。
func NewJailValidatorTransaction(staker structure.OrdexusKeyPair, validatorAccount structure.PublicKey, jailUntilEpoch uint64, recentBlockhash structure.Hash) (structure.Transaction, error) {
	instruction, err := stake.NewJailValidatorInstruction(jailUntilEpoch)
	if err != nil {
		return structure.Transaction{}, err
	}
	return newSignedStakeTransaction(staker, validatorAccount, instruction, recentBlockhash)
}

func newSignedStakeTransaction(staker structure.OrdexusKeyPair, validatorAccount structure.PublicKey, instruction stake.Instruction, recentBlockhash structure.Hash) (structure.Transaction, error) {
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
