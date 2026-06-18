package runtime_test

import (
	"bytes"
	"testing"

	bpfloader "solana_golang/programs/bpfloader"
	vmprogram "solana_golang/programs/vm"
)

func TestVirtualMachineERC20LikeContractCanBeDeployedFromBinaryAndExecuted(t *testing.T) {
	payerKey, payerPrivateKey := newSimulationSigner(t)
	programKey, programPrivateKey := newSimulationSigner(t)
	recipientKey, recipientPrivateKey := newSimulationSigner(t)
	delegateKey, delegatePrivateKey := newSimulationSigner(t)
	mintKey, mintPrivateKey := newSimulationSigner(t)
	sourceKey, sourcePrivateKey := newSimulationSigner(t)
	destinationKey, destinationPrivateKey := newSimulationSigner(t)
	allowanceKey, allowancePrivateKey := newSimulationSigner(t)
	blockhash := newTestHash(211)

	accounts := []AddressedAccount{
		newSimulationAccount(t, payerKey, 100_000_000_000, DefaultBuiltinProgramIDs.System, false),
		newSimulationAccount(t, recipientKey, 0, DefaultBuiltinProgramIDs.System, false),
		newSimulationAccount(t, delegateKey, 0, DefaultBuiltinProgramIDs.System, false),
	}
	programData := mustERC20LikeContractProgramData(t)
	accounts = deployVMContractProgram(t, accounts, payerKey, payerPrivateKey, programKey, programPrivateKey, programData, blockhash, 211)
	accounts = createVMContractStateAccount(t, accounts, payerKey, payerPrivateKey, mintKey, mintPrivateKey, programKey, blockhash, 211)
	accounts = createVMContractStateAccount(t, accounts, payerKey, payerPrivateKey, sourceKey, sourcePrivateKey, programKey, blockhash, 211)
	accounts = createVMContractStateAccount(t, accounts, payerKey, payerPrivateKey, destinationKey, destinationPrivateKey, programKey, blockhash, 211)
	accounts = createVMContractStateAccount(t, accounts, payerKey, payerPrivateKey, allowanceKey, allowancePrivateKey, programKey, blockhash, 211)

	accounts = executeVMAssetInstruction(t, accounts, programKey, []AccountMeta{
		{PublicKey: payerKey, IsSigner: true, IsWritable: true},
		{PublicKey: mintKey, IsSigner: false, IsWritable: true},
		{PublicKey: programKey, IsSigner: false, IsWritable: false},
	}, []PublicKey{payerKey, mintKey}, mustAssetInitializeFungibleInstruction(t, 6, "VM Dollar", "VMD"), blockhash, 211, map[PublicKey][]byte{
		payerKey: payerPrivateKey,
	})
	accounts = executeVMAssetInstruction(t, accounts, programKey, []AccountMeta{
		{PublicKey: payerKey, IsSigner: true, IsWritable: true},
		{PublicKey: sourceKey, IsSigner: false, IsWritable: true},
		{PublicKey: mintKey, IsSigner: false, IsWritable: false},
		{PublicKey: programKey, IsSigner: false, IsWritable: false},
	}, []PublicKey{payerKey, mintKey, sourceKey}, vmprogram.NewAssetInitializeAccountInstruction(), blockhash, 211, map[PublicKey][]byte{
		payerKey: payerPrivateKey,
	})
	accounts = executeVMAssetInstruction(t, accounts, programKey, []AccountMeta{
		{PublicKey: payerKey, IsSigner: true, IsWritable: true},
		{PublicKey: recipientKey, IsSigner: true, IsWritable: false},
		{PublicKey: destinationKey, IsSigner: false, IsWritable: true},
		{PublicKey: mintKey, IsSigner: false, IsWritable: false},
		{PublicKey: programKey, IsSigner: false, IsWritable: false},
	}, []PublicKey{recipientKey, mintKey, destinationKey}, vmprogram.NewAssetInitializeAccountInstruction(), blockhash, 211, map[PublicKey][]byte{
		payerKey:     payerPrivateKey,
		recipientKey: recipientPrivateKey,
	})
	accounts = executeVMAssetInstruction(t, accounts, programKey, []AccountMeta{
		{PublicKey: payerKey, IsSigner: true, IsWritable: true},
		{PublicKey: mintKey, IsSigner: false, IsWritable: true},
		{PublicKey: sourceKey, IsSigner: false, IsWritable: true},
		{PublicKey: programKey, IsSigner: false, IsWritable: false},
	}, []PublicKey{payerKey, mintKey, sourceKey}, mustAssetMintToInstruction(t, 1_000), blockhash, 211, map[PublicKey][]byte{
		payerKey: payerPrivateKey,
	})
	accounts = executeVMAssetInstruction(t, accounts, programKey, []AccountMeta{
		{PublicKey: payerKey, IsSigner: true, IsWritable: true},
		{PublicKey: sourceKey, IsSigner: false, IsWritable: true},
		{PublicKey: destinationKey, IsSigner: false, IsWritable: true},
		{PublicKey: mintKey, IsSigner: false, IsWritable: false},
		{PublicKey: programKey, IsSigner: false, IsWritable: false},
	}, []PublicKey{payerKey, mintKey, sourceKey, destinationKey}, mustAssetTransferInstruction(t, 125), blockhash, 211, map[PublicKey][]byte{
		payerKey: payerPrivateKey,
	})
	accounts = executeVMAssetInstruction(t, accounts, programKey, []AccountMeta{
		{PublicKey: payerKey, IsSigner: true, IsWritable: true},
		{PublicKey: allowanceKey, IsSigner: false, IsWritable: true},
		{PublicKey: mintKey, IsSigner: false, IsWritable: false},
		{PublicKey: sourceKey, IsSigner: false, IsWritable: false},
		{PublicKey: delegateKey, IsSigner: false, IsWritable: false},
		{PublicKey: programKey, IsSigner: false, IsWritable: false},
	}, []PublicKey{payerKey, mintKey, sourceKey, delegateKey, allowanceKey}, mustAssetApproveInstruction(t, 250), blockhash, 211, map[PublicKey][]byte{
		payerKey: payerPrivateKey,
	})
	accounts = executeVMAssetInstruction(t, accounts, programKey, []AccountMeta{
		{PublicKey: payerKey, IsSigner: true, IsWritable: true},
		{PublicKey: delegateKey, IsSigner: true, IsWritable: false},
		{PublicKey: sourceKey, IsSigner: false, IsWritable: true},
		{PublicKey: destinationKey, IsSigner: false, IsWritable: true},
		{PublicKey: allowanceKey, IsSigner: false, IsWritable: true},
		{PublicKey: mintKey, IsSigner: false, IsWritable: false},
		{PublicKey: programKey, IsSigner: false, IsWritable: false},
	}, []PublicKey{delegateKey, mintKey, sourceKey, destinationKey, allowanceKey}, mustAssetTransferFromInstruction(t, 200), blockhash, 211, map[PublicKey][]byte{
		payerKey:    payerPrivateKey,
		delegateKey: delegatePrivateKey,
	})
	accounts = executeVMAssetInstruction(t, accounts, programKey, []AccountMeta{
		{PublicKey: payerKey, IsSigner: true, IsWritable: true},
		{PublicKey: mintKey, IsSigner: false, IsWritable: true},
		{PublicKey: sourceKey, IsSigner: false, IsWritable: true},
		{PublicKey: programKey, IsSigner: false, IsWritable: false},
	}, []PublicKey{payerKey, mintKey, sourceKey}, mustAssetBurnInstruction(t, 100), blockhash, 211, map[PublicKey][]byte{
		payerKey: payerPrivateKey,
	})

	mintState := mustAssetMintState(t, accounts, mintKey)
	sourceState := mustAssetBalanceState(t, accounts, sourceKey)
	destinationState := mustAssetBalanceState(t, accounts, destinationKey)
	allowanceState := mustAssetAllowanceState(t, accounts, allowanceKey)
	if mintState.Kind != vmprogram.AssetKindFungible || mintState.Decimals != 6 || mintState.Supply != 900 {
		t.Fatalf("mint state = %+v, want fungible decimals=6 supply=900", mintState)
	}
	if sourceState.Amount != 575 || destinationState.Amount != 325 {
		t.Fatalf("balances source=%d destination=%d, want 575/325", sourceState.Amount, destinationState.Amount)
	}
	if allowanceState.Amount != 50 || allowanceState.Delegate != delegateKey {
		t.Fatalf("allowance = %+v, want amount=50 delegate", allowanceState)
	}
}

func TestVirtualMachineNFTContractCanBeDeployedFromBinaryAndExecuted(t *testing.T) {
	payerKey, payerPrivateKey := newSimulationSigner(t)
	programKey, programPrivateKey := newSimulationSigner(t)
	recipientKey, recipientPrivateKey := newSimulationSigner(t)
	mintKey, mintPrivateKey := newSimulationSigner(t)
	sourceKey, sourcePrivateKey := newSimulationSigner(t)
	destinationKey, destinationPrivateKey := newSimulationSigner(t)
	blockhash := newTestHash(212)

	accounts := []AddressedAccount{
		newSimulationAccount(t, payerKey, 100_000_000_000, DefaultBuiltinProgramIDs.System, false),
		newSimulationAccount(t, recipientKey, 0, DefaultBuiltinProgramIDs.System, false),
	}
	programData := mustNFTContractProgramData(t)
	accounts = deployVMContractProgram(t, accounts, payerKey, payerPrivateKey, programKey, programPrivateKey, programData, blockhash, 212)
	accounts = createVMContractStateAccount(t, accounts, payerKey, payerPrivateKey, mintKey, mintPrivateKey, programKey, blockhash, 212)
	accounts = createVMContractStateAccount(t, accounts, payerKey, payerPrivateKey, sourceKey, sourcePrivateKey, programKey, blockhash, 212)
	accounts = createVMContractStateAccount(t, accounts, payerKey, payerPrivateKey, destinationKey, destinationPrivateKey, programKey, blockhash, 212)

	accounts = executeVMAssetInstruction(t, accounts, programKey, []AccountMeta{
		{PublicKey: payerKey, IsSigner: true, IsWritable: true},
		{PublicKey: mintKey, IsSigner: false, IsWritable: true},
		{PublicKey: programKey, IsSigner: false, IsWritable: false},
	}, []PublicKey{payerKey, mintKey}, mustAssetInitializeNFTInstruction(t, "VM Art", "VART", "ipfs://vm-art-1"), blockhash, 212, map[PublicKey][]byte{
		payerKey: payerPrivateKey,
	})
	accounts = executeVMAssetInstruction(t, accounts, programKey, []AccountMeta{
		{PublicKey: payerKey, IsSigner: true, IsWritable: true},
		{PublicKey: sourceKey, IsSigner: false, IsWritable: true},
		{PublicKey: mintKey, IsSigner: false, IsWritable: false},
		{PublicKey: programKey, IsSigner: false, IsWritable: false},
	}, []PublicKey{payerKey, mintKey, sourceKey}, vmprogram.NewAssetInitializeAccountInstruction(), blockhash, 212, map[PublicKey][]byte{
		payerKey: payerPrivateKey,
	})
	accounts = executeVMAssetInstruction(t, accounts, programKey, []AccountMeta{
		{PublicKey: payerKey, IsSigner: true, IsWritable: true},
		{PublicKey: recipientKey, IsSigner: true, IsWritable: false},
		{PublicKey: destinationKey, IsSigner: false, IsWritable: true},
		{PublicKey: mintKey, IsSigner: false, IsWritable: false},
		{PublicKey: programKey, IsSigner: false, IsWritable: false},
	}, []PublicKey{recipientKey, mintKey, destinationKey}, vmprogram.NewAssetInitializeAccountInstruction(), blockhash, 212, map[PublicKey][]byte{
		payerKey:     payerPrivateKey,
		recipientKey: recipientPrivateKey,
	})
	accounts = executeVMAssetInstruction(t, accounts, programKey, []AccountMeta{
		{PublicKey: payerKey, IsSigner: true, IsWritable: true},
		{PublicKey: mintKey, IsSigner: false, IsWritable: true},
		{PublicKey: sourceKey, IsSigner: false, IsWritable: true},
		{PublicKey: programKey, IsSigner: false, IsWritable: false},
	}, []PublicKey{payerKey, mintKey, sourceKey}, mustAssetMintToInstruction(t, 1), blockhash, 212, map[PublicKey][]byte{
		payerKey: payerPrivateKey,
	})
	accounts = executeFailedVMAssetInstruction(t, accounts, programKey, []AccountMeta{
		{PublicKey: payerKey, IsSigner: true, IsWritable: true},
		{PublicKey: mintKey, IsSigner: false, IsWritable: true},
		{PublicKey: sourceKey, IsSigner: false, IsWritable: true},
		{PublicKey: programKey, IsSigner: false, IsWritable: false},
	}, []PublicKey{payerKey, mintKey, sourceKey}, mustAssetMintToInstruction(t, 1), blockhash, 212, map[PublicKey][]byte{
		payerKey: payerPrivateKey,
	})
	accounts = executeVMAssetInstruction(t, accounts, programKey, []AccountMeta{
		{PublicKey: payerKey, IsSigner: true, IsWritable: true},
		{PublicKey: sourceKey, IsSigner: false, IsWritable: true},
		{PublicKey: destinationKey, IsSigner: false, IsWritable: true},
		{PublicKey: mintKey, IsSigner: false, IsWritable: false},
		{PublicKey: programKey, IsSigner: false, IsWritable: false},
	}, []PublicKey{payerKey, mintKey, sourceKey, destinationKey}, mustAssetTransferInstruction(t, 1), blockhash, 212, map[PublicKey][]byte{
		payerKey: payerPrivateKey,
	})

	mintState := mustAssetMintState(t, accounts, mintKey)
	sourceState := mustAssetBalanceState(t, accounts, sourceKey)
	destinationState := mustAssetBalanceState(t, accounts, destinationKey)
	if mintState.Kind != vmprogram.AssetKindNFT || mintState.Supply != 1 || mintState.MaxSupply != 1 {
		t.Fatalf("nft mint state = %+v, want supply=max_supply=1", mintState)
	}
	if mintState.URI != "ipfs://vm-art-1" {
		t.Fatalf("nft uri = %q", mintState.URI)
	}
	if sourceState.Amount != 0 || destinationState.Amount != 1 {
		t.Fatalf("nft balances source=%d destination=%d, want 0/1", sourceState.Amount, destinationState.Amount)
	}
}

func deployVMContractProgram(t *testing.T, accounts []AddressedAccount, payerKey PublicKey, payerPrivateKey []byte, programKey PublicKey, programPrivateKey []byte, programData []byte, blockhash Blockhash, slot uint64) []AddressedAccount {
	t.Helper()

	accounts = createAccountWithSystemProgram(t, accounts, payerKey, payerPrivateKey, programKey, programPrivateKey, DefaultBuiltinProgramIDs.BPFLoader, mustMinimumBalance(t, len(programData)), uint64(len(programData)), blockhash, slot)
	deployInstruction, err := bpfloader.NewDeployInstruction(programData)
	deployData := mustBPFLoaderInstructionBytes(t, deployInstruction, err)
	transaction := signedSimulationProgramTransaction(t, DefaultBuiltinProgramIDs.BPFLoader, []AccountMeta{
		{PublicKey: payerKey, IsSigner: true, IsWritable: true},
		{PublicKey: programKey, IsSigner: true, IsWritable: true},
		{PublicKey: DefaultBuiltinProgramIDs.BPFLoader, IsSigner: false, IsWritable: false},
	}, []PublicKey{programKey}, deployData, blockhash, map[PublicKey][]byte{
		payerKey:   payerPrivateKey,
		programKey: programPrivateKey,
	})
	accounts = simulateConfirmedVMTransaction(t, accounts, transaction, blockhash, slot)
	programAccount := findWrittenAccount(t, accounts, programKey)
	if !programAccount.Executable || programAccount.Owner != DefaultBuiltinProgramIDs.BPFLoader {
		t.Fatalf("program account executable=%v owner=%s", programAccount.Executable, programAccount.Owner.String())
	}
	if !bytes.Equal(programAccount.Data, programData) {
		t.Fatal("program account data does not match deployed bytecode")
	}
	return accounts
}

func createVMContractStateAccount(t *testing.T, accounts []AddressedAccount, payerKey PublicKey, payerPrivateKey []byte, stateKey PublicKey, statePrivateKey []byte, owner PublicKey, blockhash Blockhash, slot uint64) []AddressedAccount {
	t.Helper()
	return createAccountWithSystemProgram(t, accounts, payerKey, payerPrivateKey, stateKey, statePrivateKey, owner, mustMinimumBalance(t, vmprogram.MaxAssetStateBytes), 0, blockhash, slot)
}

func createAccountWithSystemProgram(t *testing.T, accounts []AddressedAccount, payerKey PublicKey, payerPrivateKey []byte, newAccountKey PublicKey, newAccountPrivateKey []byte, owner PublicKey, lamports uint64, space uint64, blockhash Blockhash, slot uint64) []AddressedAccount {
	t.Helper()

	createInstruction, err := NewCreateAccountInstruction(CreateAccountParams{
		Lamports: lamports,
		Space:    space,
		Owner:    owner,
	})
	instructionData := mustSystemInstructionBytes(t, createInstruction, err)
	transaction := signedSimulationProgramTransaction(t, DefaultBuiltinProgramIDs.System, []AccountMeta{
		{PublicKey: payerKey, IsSigner: true, IsWritable: true},
		{PublicKey: newAccountKey, IsSigner: true, IsWritable: true},
		{PublicKey: DefaultBuiltinProgramIDs.System, IsSigner: false, IsWritable: false},
	}, []PublicKey{payerKey, newAccountKey}, instructionData, blockhash, map[PublicKey][]byte{
		payerKey:      payerPrivateKey,
		newAccountKey: newAccountPrivateKey,
	})
	return simulateConfirmedVMTransaction(t, accounts, transaction, blockhash, slot)
}

func executeVMAssetInstruction(t *testing.T, accounts []AddressedAccount, programKey PublicKey, accountMetas []AccountMeta, instructionAccounts []PublicKey, instruction vmprogram.AssetInstruction, blockhash Blockhash, slot uint64, privateKeys map[PublicKey][]byte) []AddressedAccount {
	t.Helper()

	instructionData := mustAssetInstructionBytes(t, instruction)
	transaction := signedSimulationProgramTransaction(t, programKey, accountMetas, instructionAccounts, instructionData, blockhash, privateKeys)
	return simulateConfirmedVMTransaction(t, accounts, transaction, blockhash, slot)
}

func executeFailedVMAssetInstruction(t *testing.T, accounts []AddressedAccount, programKey PublicKey, accountMetas []AccountMeta, instructionAccounts []PublicKey, instruction vmprogram.AssetInstruction, blockhash Blockhash, slot uint64, privateKeys map[PublicKey][]byte) []AddressedAccount {
	t.Helper()

	instructionData := mustAssetInstructionBytes(t, instruction)
	transaction := signedSimulationProgramTransaction(t, programKey, accountMetas, instructionAccounts, instructionData, blockhash, privateKeys)
	result, err := simulateWithVirtualMachine(t, TransactionSimulationInput{
		Transaction:    transaction,
		Accounts:       accounts,
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, slot),
		CurrentSlot:    slot,
	})
	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}
	if result.Status != TransactionStatusFailed {
		t.Fatalf("Status = %d, want failed", result.Status)
	}
	return mergeSimulationAccounts(accounts, result.WrittenAccounts)
}

func simulateConfirmedVMTransaction(t *testing.T, accounts []AddressedAccount, transaction Transaction, blockhash Blockhash, slot uint64) []AddressedAccount {
	t.Helper()

	result, err := simulateWithVirtualMachine(t, TransactionSimulationInput{
		Transaction:    transaction,
		Accounts:       accounts,
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, slot),
		CurrentSlot:    slot,
	})
	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}
	if result.Status != TransactionStatusConfirmed {
		t.Fatalf("Status = %d, error = %v", result.Status, result.Error)
	}
	return mergeSimulationAccounts(accounts, result.WrittenAccounts)
}

func mergeSimulationAccounts(accounts []AddressedAccount, writtenAccounts []AddressedAccount) []AddressedAccount {
	merged := make([]AddressedAccount, len(accounts))
	copy(merged, accounts)
	accountIndexByAddress := make(map[PublicKey]int, len(merged))
	for accountIndex, account := range merged {
		accountIndexByAddress[account.Address] = accountIndex
	}
	for _, account := range writtenAccounts {
		if accountIndex, exists := accountIndexByAddress[account.Address]; exists {
			merged[accountIndex] = account
			continue
		}
		accountIndexByAddress[account.Address] = len(merged)
		merged = append(merged, account)
	}
	return merged
}

func mustERC20LikeContractProgramData(t *testing.T) []byte {
	t.Helper()
	data, err := vmprogram.ERC20LikeContractProgramData()
	if err != nil {
		t.Fatalf("ERC20LikeContractProgramData() error = %v", err)
	}
	return data
}

func mustNFTContractProgramData(t *testing.T) []byte {
	t.Helper()
	data, err := vmprogram.NFTContractProgramData()
	if err != nil {
		t.Fatalf("NFTContractProgramData() error = %v", err)
	}
	return data
}

func mustAssetInstruction(t *testing.T, instruction vmprogram.AssetInstruction, err error) vmprogram.AssetInstruction {
	t.Helper()
	if err != nil {
		t.Fatalf("build asset instruction error = %v", err)
	}
	return instruction
}

func mustAssetInitializeFungibleInstruction(t *testing.T, decimals uint8, name string, symbol string) vmprogram.AssetInstruction {
	t.Helper()
	instruction, err := vmprogram.NewAssetInitializeFungibleInstruction(decimals, name, symbol)
	return mustAssetInstruction(t, instruction, err)
}

func mustAssetInitializeNFTInstruction(t *testing.T, name string, symbol string, uri string) vmprogram.AssetInstruction {
	t.Helper()
	instruction, err := vmprogram.NewAssetInitializeNFTInstruction(name, symbol, uri)
	return mustAssetInstruction(t, instruction, err)
}

func mustAssetMintToInstruction(t *testing.T, amount uint64) vmprogram.AssetInstruction {
	t.Helper()
	instruction, err := vmprogram.NewAssetMintToInstruction(amount)
	return mustAssetInstruction(t, instruction, err)
}

func mustAssetTransferInstruction(t *testing.T, amount uint64) vmprogram.AssetInstruction {
	t.Helper()
	instruction, err := vmprogram.NewAssetTransferInstruction(amount)
	return mustAssetInstruction(t, instruction, err)
}

func mustAssetBurnInstruction(t *testing.T, amount uint64) vmprogram.AssetInstruction {
	t.Helper()
	instruction, err := vmprogram.NewAssetBurnInstruction(amount)
	return mustAssetInstruction(t, instruction, err)
}

func mustAssetApproveInstruction(t *testing.T, amount uint64) vmprogram.AssetInstruction {
	t.Helper()
	instruction, err := vmprogram.NewAssetApproveInstruction(amount)
	return mustAssetInstruction(t, instruction, err)
}

func mustAssetTransferFromInstruction(t *testing.T, amount uint64) vmprogram.AssetInstruction {
	t.Helper()
	instruction, err := vmprogram.NewAssetTransferFromInstruction(amount)
	return mustAssetInstruction(t, instruction, err)
}

func mustAssetInstructionBytes(t *testing.T, instruction vmprogram.AssetInstruction) []byte {
	t.Helper()
	data, err := instruction.MarshalBinary()
	if err != nil {
		t.Fatalf("AssetInstruction.MarshalBinary() error = %v", err)
	}
	return data
}

func mustBPFLoaderInstructionBytes(t *testing.T, instruction bpfloader.Instruction, err error) []byte {
	t.Helper()
	if err != nil {
		t.Fatalf("build bpfloader instruction error = %v", err)
	}
	data, err := instruction.MarshalBinary()
	if err != nil {
		t.Fatalf("BPFLoaderInstruction.MarshalBinary() error = %v", err)
	}
	return data
}

func mustAssetMintState(t *testing.T, accounts []AddressedAccount, address PublicKey) vmprogram.AssetMintState {
	t.Helper()
	state, err := vmprogram.UnmarshalAssetMintStateBinary(findWrittenAccount(t, accounts, address).Data)
	if err != nil {
		t.Fatalf("UnmarshalAssetMintStateBinary() error = %v", err)
	}
	return state
}

func mustAssetBalanceState(t *testing.T, accounts []AddressedAccount, address PublicKey) vmprogram.AssetBalanceState {
	t.Helper()
	state, err := vmprogram.UnmarshalAssetBalanceStateBinary(findWrittenAccount(t, accounts, address).Data)
	if err != nil {
		t.Fatalf("UnmarshalAssetBalanceStateBinary() error = %v", err)
	}
	return state
}

func mustAssetAllowanceState(t *testing.T, accounts []AddressedAccount, address PublicKey) vmprogram.AssetAllowanceState {
	t.Helper()
	state, err := vmprogram.UnmarshalAssetAllowanceStateBinary(findWrittenAccount(t, accounts, address).Data)
	if err != nil {
		t.Fatalf("UnmarshalAssetAllowanceStateBinary() error = %v", err)
	}
	return state
}
