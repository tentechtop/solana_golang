package blockchain

import (
	"fmt"
	"sort"
	"time"

	bpfloader "github.com/tentechtop/ordexus-core/programs/bpfloader"
	"github.com/tentechtop/ordexus-core/structure"
	"github.com/tentechtop/ordexus-core/utils"
)

// NewInvokeContractTransaction 构造通用合约调用交易 + 账户顺序保持调用方传入的 instruction account 语义。
// NewCreateContractAccountTransaction 创建由合约拥有的状态账户，账户租金与签名均由系统程序校验。
// NewCreateContractAccountTransaction 创建由合约拥有的状态账户。
func NewCreateContractAccountTransaction(
	payer structure.OrdexusKeyPair,
	account structure.OrdexusKeyPair,
	program structure.PublicKey,
	space uint64,
	depositLamports uint64,
	recentBlockhash structure.Hash,
) (structure.Transaction, error) {
	if payer.PublicKey.IsZero() || account.PublicKey.IsZero() {
		return structure.Transaction{}, fmt.Errorf("blockchain: contract account signer is empty")
	}
	if payer.PublicKey == account.PublicKey {
		return structure.Transaction{}, fmt.Errorf("blockchain: payer and contract account must differ")
	}
	if program.IsZero() {
		return structure.Transaction{}, fmt.Errorf("blockchain: contract program is empty")
	}
	if recentBlockhash.IsZero() {
		return structure.Transaction{}, structure.ErrEmptyRecentBlockhash
	}
	minimumBalance, err := structure.MinimumBalanceForRentExemption(int(space))
	if err != nil {
		return structure.Transaction{}, fmt.Errorf("blockchain: calculate contract account rent: %w", err)
	}
	lamports, err := safeAddLamports(minimumBalance, depositLamports)
	if err != nil {
		return structure.Transaction{}, fmt.Errorf("blockchain: calculate contract account lamports: %w", err)
	}
	createInstruction, err := structure.NewCreateAccountInstruction(structure.CreateAccountParams{
		Lamports: lamports,
		Space:    space,
		Owner:    program,
	})
	if err != nil {
		return structure.Transaction{}, fmt.Errorf("blockchain: build contract account: %w", err)
	}
	data, err := createInstruction.MarshalBinary()
	if err != nil {
		return structure.Transaction{}, fmt.Errorf("blockchain: marshal contract account: %w", err)
	}
	transaction := structure.Transaction{
		Accounts: []structure.AccountMeta{
			{PublicKey: payer.PublicKey, IsSigner: true, IsWritable: true},
			{PublicKey: account.PublicKey, IsSigner: true, IsWritable: true},
			{PublicKey: structure.DefaultBuiltinProgramIDs.System},
		},
		Instructions: []structure.CompiledInstruction{{
			ProgramIDIndex: 2,
			AccountIndexes: []uint8{0, 1},
			Data:           data,
		}},
		RecentBlockhash: recentBlockhash,
		SubmitTime:      time.Now().UnixMilli(),
	}
	return transaction.Sign(map[structure.PublicKey][]byte{
		payer.PublicKey:   payer.PrivateKey,
		account.PublicKey: account.PrivateKey,
	})
}

// NewInvokeContractTransaction 构造通用合约调用交易，并保留指令账户顺序。
func NewInvokeContractTransaction(
	payer structure.OrdexusKeyPair,
	program structure.PublicKey,
	instructionAccounts []structure.AccountMeta,
	instructionData []byte,
	additionalSigners []structure.OrdexusKeyPair,
	recentBlockhash structure.Hash,
) (structure.Transaction, error) {
	if payer.PublicKey.IsZero() {
		return structure.Transaction{}, fmt.Errorf("blockchain: contract payer is empty")
	}
	if program.IsZero() {
		return structure.Transaction{}, fmt.Errorf("blockchain: contract program is empty")
	}
	if recentBlockhash.IsZero() {
		return structure.Transaction{}, structure.ErrEmptyRecentBlockhash
	}
	if len(instructionData) > structure.MaxInstructionDataSize {
		return structure.Transaction{}, fmt.Errorf("blockchain: contract instruction data exceeds %d bytes", structure.MaxInstructionDataSize)
	}

	accounts, instructionAccountAddresses, err := contractTransactionAccounts(payer.PublicKey, program, instructionAccounts)
	if err != nil {
		return structure.Transaction{}, err
	}
	accountIndexByAddress := make(map[structure.PublicKey]uint8, len(accounts))
	for index, account := range accounts {
		accountIndexByAddress[account.PublicKey] = uint8(index)
	}
	instructionIndexes := make([]uint8, len(instructionAccountAddresses))
	for index, address := range instructionAccountAddresses {
		instructionIndexes[index] = accountIndexByAddress[address]
	}

	privateKeys, err := contractSignerPrivateKeys(payer, accounts, additionalSigners)
	if err != nil {
		return structure.Transaction{}, err
	}
	transaction := structure.Transaction{
		Accounts: accounts,
		Instructions: []structure.CompiledInstruction{{
			ProgramIDIndex: accountIndexByAddress[program],
			AccountIndexes: instructionIndexes,
			Data:           utils.CloneBytes(instructionData),
		}},
		RecentBlockhash: recentBlockhash,
		SubmitTime:      time.Now().UnixMilli(),
	}
	return transaction.Sign(privateKeys)
}

// NewUpgradeContractTransaction 构造受 Manifest authority 控制的合约升级交易。
func NewUpgradeContractTransaction(
	payer structure.OrdexusKeyPair,
	authority structure.OrdexusKeyPair,
	program structure.PublicKey,
	programData []byte,
	recentBlockhash structure.Hash,
) (structure.Transaction, error) {
	if payer.PublicKey.IsZero() || authority.PublicKey.IsZero() {
		return structure.Transaction{}, fmt.Errorf("blockchain: contract upgrade signer is empty")
	}
	if program.IsZero() {
		return structure.Transaction{}, fmt.Errorf("blockchain: contract program is empty")
	}
	if recentBlockhash.IsZero() {
		return structure.Transaction{}, structure.ErrEmptyRecentBlockhash
	}
	instruction, err := bpfloader.NewUpgradeInstruction(programData)
	if err != nil {
		return structure.Transaction{}, fmt.Errorf("blockchain: build upgrade instruction: %w", err)
	}
	data, err := instruction.MarshalBinary()
	if err != nil {
		return structure.Transaction{}, fmt.Errorf("blockchain: marshal upgrade instruction: %w", err)
	}

	accounts := []structure.AccountMeta{{PublicKey: payer.PublicKey, IsSigner: true, IsWritable: true}}
	if authority.PublicKey != payer.PublicKey {
		accounts = append(accounts, structure.AccountMeta{PublicKey: authority.PublicKey, IsSigner: true, IsWritable: false})
	}
	accounts = append(accounts,
		structure.AccountMeta{PublicKey: program, IsSigner: false, IsWritable: true},
		structure.AccountMeta{PublicKey: structure.DefaultBuiltinProgramIDs.BPFLoader, IsSigner: false, IsWritable: false},
	)
	programIndex := uint8(1)
	if authority.PublicKey != payer.PublicKey {
		programIndex = 2
	}
	loaderIndex := uint8(len(accounts) - 1)
	transaction := structure.Transaction{
		Accounts: accounts,
		Instructions: []structure.CompiledInstruction{{
			ProgramIDIndex: loaderIndex,
			AccountIndexes: []uint8{programIndex},
			Data:           data,
		}},
		RecentBlockhash: recentBlockhash,
		SubmitTime:      time.Now().UnixMilli(),
	}
	privateKeys := map[structure.PublicKey][]byte{payer.PublicKey: payer.PrivateKey}
	privateKeys[authority.PublicKey] = authority.PrivateKey
	return transaction.Sign(privateKeys)
}

func contractTransactionAccounts(payer structure.PublicKey, program structure.PublicKey, instructionAccounts []structure.AccountMeta) ([]structure.AccountMeta, []structure.PublicKey, error) {
	accountsByAddress := map[structure.PublicKey]structure.AccountMeta{
		payer: {PublicKey: payer, IsSigner: true, IsWritable: true},
	}
	addresses := make([]structure.PublicKey, len(instructionAccounts))
	for index, account := range instructionAccounts {
		if account.PublicKey.IsZero() {
			return nil, nil, fmt.Errorf("blockchain: contract account %d is empty", index)
		}
		if _, exists := accountsByAddress[account.PublicKey]; exists && account.PublicKey != payer {
			return nil, nil, fmt.Errorf("blockchain: duplicate contract account %s", account.PublicKey.String())
		}
		if account.PublicKey == program && (account.IsSigner || account.IsWritable) {
			return nil, nil, fmt.Errorf("blockchain: contract program account must be readonly and unsigned")
		}
		if existing, exists := accountsByAddress[account.PublicKey]; exists {
			existing.IsSigner = existing.IsSigner || account.IsSigner
			existing.IsWritable = existing.IsWritable || account.IsWritable
			accountsByAddress[account.PublicKey] = existing
		} else {
			accountsByAddress[account.PublicKey] = account
		}
		addresses[index] = account.PublicKey
	}
	if _, exists := accountsByAddress[program]; !exists {
		accountsByAddress[program] = structure.AccountMeta{PublicKey: program}
	}

	orderedAddresses := make([]structure.PublicKey, 0, len(accountsByAddress))
	orderedAddresses = append(orderedAddresses, payer)
	for _, address := range addresses {
		if address == payer || containsPublicKey(orderedAddresses, address) {
			continue
		}
		orderedAddresses = append(orderedAddresses, address)
	}
	if !containsPublicKey(orderedAddresses, program) {
		orderedAddresses = append(orderedAddresses, program)
	}
	accounts := make([]structure.AccountMeta, len(orderedAddresses))
	for index, address := range orderedAddresses {
		accounts[index] = accountsByAddress[address]
	}
	sort.SliceStable(accounts, func(left int, right int) bool {
		return contractAccountPermissionGroup(accounts[left]) < contractAccountPermissionGroup(accounts[right])
	})
	return accounts, addresses, nil
}

func contractSignerPrivateKeys(payer structure.OrdexusKeyPair, accounts []structure.AccountMeta, additionalSigners []structure.OrdexusKeyPair) (map[structure.PublicKey][]byte, error) {
	privateKeys := map[structure.PublicKey][]byte{payer.PublicKey: payer.PrivateKey}
	for index, signer := range additionalSigners {
		if signer.PublicKey.IsZero() {
			return nil, fmt.Errorf("blockchain: additional signer %d is empty", index)
		}
		if _, exists := privateKeys[signer.PublicKey]; exists {
			return nil, fmt.Errorf("blockchain: duplicate contract signer %s", signer.PublicKey.String())
		}
		privateKeys[signer.PublicKey] = signer.PrivateKey
	}
	for _, account := range accounts {
		if !account.IsSigner {
			continue
		}
		if _, exists := privateKeys[account.PublicKey]; !exists {
			return nil, fmt.Errorf("blockchain: missing private key for contract signer %s", account.PublicKey.String())
		}
	}
	return privateKeys, nil
}

func contractAccountPermissionGroup(account structure.AccountMeta) int {
	if account.IsSigner && account.IsWritable {
		return 0
	}
	if account.IsSigner {
		return 1
	}
	if account.IsWritable {
		return 2
	}
	return 3
}

func containsPublicKey(addresses []structure.PublicKey, target structure.PublicKey) bool {
	for _, address := range addresses {
		if address == target {
			return true
		}
	}
	return false
}
