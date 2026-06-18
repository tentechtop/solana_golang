package runtime_test

import (
	"bytes"
	"strings"
	"testing"

	bpfloader "solana_golang/programs/bpfloader"
	systemprogram "solana_golang/programs/system"
	svm "solana_golang/vm"
)

func TestBPFLoaderDeploymentPolicyRejectsMissingManifest(t *testing.T) {
	payerKey, payerPrivateKey := newSimulationSigner(t)
	programKey, programPrivateKey := newSimulationSigner(t)
	blockhash := newTestHash(14)
	programData := mustVMProgramData(t, svm.BuildProgramCode())
	policy := bpfloader.DeploymentPolicy{RequireManifest: true}

	result := simulateBPFLoaderDeployment(t, payerKey, payerPrivateKey, programKey, programPrivateKey, programData, mustMinimumBalance(t, len(programData)), policy, blockhash, 214)
	if result.Status != TransactionStatusFailed || result.Error == nil || !strings.Contains(result.Error.Error(), "manifest is required") {
		t.Fatalf("deployment result = status %d error %v, want manifest rejection", result.Status, result.Error)
	}
}

func TestBPFLoaderDeploymentPolicyAllowsWhitelistedDeployerWithoutDeposit(t *testing.T) {
	payerKey, payerPrivateKey := newSimulationSigner(t)
	programKey, programPrivateKey := newSimulationSigner(t)
	blockhash := newTestHash(15)
	programData := mustGovernedNoopProgramData(t, PublicKey{})
	policy := bpfloader.DeploymentPolicy{
		AllowedDeployers: []PublicKey{payerKey},
		RequireManifest:  true,
	}

	result := simulateBPFLoaderDeployment(t, payerKey, payerPrivateKey, programKey, programPrivateKey, programData, mustMinimumBalance(t, len(programData)), policy, blockhash, 215)
	if result.Status != TransactionStatusConfirmed {
		t.Fatalf("deployment status = %d error %v, want confirmed", result.Status, result.Error)
	}
}

func TestBPFLoaderDeploymentPolicyAllowsDepositForUnlistedDeployer(t *testing.T) {
	payerKey, payerPrivateKey := newSimulationSigner(t)
	programKey, programPrivateKey := newSimulationSigner(t)
	blockhash := newTestHash(16)
	programData := mustGovernedNoopProgramData(t, PublicKey{})
	depositLamports := uint64(12345)
	policy := bpfloader.DeploymentPolicy{
		AllowedDeployers:             []PublicKey{newTestPublicKey(17)},
		MinDeploymentDepositLamports: depositLamports,
		RequireManifest:              true,
	}

	result := simulateBPFLoaderDeployment(t, payerKey, payerPrivateKey, programKey, programPrivateKey, programData, mustMinimumBalance(t, len(programData))+depositLamports, policy, blockhash, 216)
	if result.Status != TransactionStatusConfirmed {
		t.Fatalf("deployment status = %d error %v, want confirmed", result.Status, result.Error)
	}
}

func TestBPFLoaderDeploymentPolicyRejectsUnlistedDeployerWithoutDeposit(t *testing.T) {
	payerKey, payerPrivateKey := newSimulationSigner(t)
	programKey, programPrivateKey := newSimulationSigner(t)
	blockhash := newTestHash(18)
	programData := mustGovernedNoopProgramData(t, PublicKey{})
	policy := bpfloader.DeploymentPolicy{
		AllowedDeployers:             []PublicKey{newTestPublicKey(19)},
		MinDeploymentDepositLamports: 100,
		RequireManifest:              true,
	}

	result := simulateBPFLoaderDeployment(t, payerKey, payerPrivateKey, programKey, programPrivateKey, programData, mustMinimumBalance(t, len(programData)), policy, blockhash, 217)
	if result.Status != TransactionStatusFailed || result.Error == nil || !strings.Contains(result.Error.Error(), "deposit is insufficient") {
		t.Fatalf("deployment result = status %d error %v, want deposit rejection", result.Status, result.Error)
	}
}

func TestBPFLoaderUpgradeRequiresGovernanceAuthority(t *testing.T) {
	payerKey, payerPrivateKey := newSimulationSigner(t)
	authorityKey, authorityPrivateKey := newSimulationSigner(t)
	programKey := newTestPublicKey(20)
	blockhash := newTestHash(21)
	currentProgramData := mustGovernedNoopProgramData(t, authorityKey)
	nextProgramData := mustGovernedNoopProgramDataWithLimit(t, authorityKey, svm.DefaultComputeUnitLimit-1)
	policy := bpfloader.DeploymentPolicy{RequireManifest: true, AllowUpgradeableContracts: true}
	programLamports := mustMinimumBalance(t, len(currentProgramData))

	failedResult := simulateBPFLoaderUpgrade(t, payerKey, payerPrivateKey, PublicKey{}, nil, programKey, currentProgramData, nextProgramData, programLamports, policy, blockhash, 218)
	if failedResult.Status != TransactionStatusFailed || failedResult.Error == nil || !strings.Contains(failedResult.Error.Error(), "upgrade authority must sign") {
		t.Fatalf("upgrade result = status %d error %v, want authority rejection", failedResult.Status, failedResult.Error)
	}

	confirmedResult := simulateBPFLoaderUpgrade(t, payerKey, payerPrivateKey, authorityKey, authorityPrivateKey, programKey, currentProgramData, nextProgramData, programLamports, policy, blockhash, 219)
	if confirmedResult.Status != TransactionStatusConfirmed {
		t.Fatalf("upgrade status = %d error %v, want confirmed", confirmedResult.Status, confirmedResult.Error)
	}
	writtenProgram := findWrittenAccount(t, confirmedResult.WrittenAccounts, programKey)
	if !bytes.Equal(writtenProgram.Data, nextProgramData) {
		t.Fatal("upgraded program data mismatch")
	}
}

func simulateBPFLoaderDeployment(
	t *testing.T,
	payerKey PublicKey,
	payerPrivateKey []byte,
	programKey PublicKey,
	programPrivateKey []byte,
	programData []byte,
	programLamports uint64,
	policy bpfloader.DeploymentPolicy,
	blockhash Blockhash,
	slot uint64,
) TransactionExecutionResult {
	t.Helper()

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
	result, err := simulateWithBPFLoaderPolicy(t, TransactionSimulationInput{
		Transaction: transaction,
		Accounts: []AddressedAccount{
			newSimulationAccount(t, payerKey, mustMinimumBalance(t, 0)+LamportsPerSignature*2+100, DefaultBuiltinProgramIDs.System, false),
			newSimulationDataAccount(t, programKey, programLamports, DefaultBuiltinProgramIDs.BPFLoader, false, make([]byte, len(programData))),
		},
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, slot),
		CurrentSlot:    slot,
	}, policy)
	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}
	return result
}

func simulateBPFLoaderUpgrade(
	t *testing.T,
	payerKey PublicKey,
	payerPrivateKey []byte,
	authorityKey PublicKey,
	authorityPrivateKey []byte,
	programKey PublicKey,
	currentProgramData []byte,
	nextProgramData []byte,
	programLamports uint64,
	policy bpfloader.DeploymentPolicy,
	blockhash Blockhash,
	slot uint64,
) TransactionExecutionResult {
	t.Helper()

	upgradeInstruction, err := bpfloader.NewUpgradeInstruction(nextProgramData)
	upgradeData := mustBPFLoaderInstructionBytes(t, upgradeInstruction, err)
	accountMetas := []AccountMeta{
		{PublicKey: payerKey, IsSigner: true, IsWritable: true},
	}
	privateKeys := map[PublicKey][]byte{payerKey: payerPrivateKey}
	if !authorityKey.IsZero() {
		accountMetas = append(accountMetas, AccountMeta{PublicKey: authorityKey, IsSigner: true, IsWritable: false})
		privateKeys[authorityKey] = authorityPrivateKey
	}
	accountMetas = append(accountMetas,
		AccountMeta{PublicKey: programKey, IsSigner: false, IsWritable: true},
		AccountMeta{PublicKey: DefaultBuiltinProgramIDs.BPFLoader, IsSigner: false, IsWritable: false},
	)
	transaction := signedSimulationProgramTransaction(t, DefaultBuiltinProgramIDs.BPFLoader, accountMetas, []PublicKey{programKey}, upgradeData, blockhash, privateKeys)
	accounts := []AddressedAccount{
		newSimulationAccount(t, payerKey, mustMinimumBalance(t, 0)+LamportsPerSignature*2+100, DefaultBuiltinProgramIDs.System, false),
		newSimulationDataAccount(t, programKey, programLamports, DefaultBuiltinProgramIDs.BPFLoader, true, currentProgramData),
	}
	if !authorityKey.IsZero() {
		accounts = append(accounts, newSimulationAccount(t, authorityKey, mustMinimumBalance(t, 0), DefaultBuiltinProgramIDs.System, false))
	}
	result, err := simulateWithBPFLoaderPolicy(t, TransactionSimulationInput{
		Transaction:    transaction,
		Accounts:       accounts,
		BlockhashQueue: newSimulationBlockhashQueue(t, blockhash, slot),
		CurrentSlot:    slot,
	}, policy)
	if err != nil {
		t.Fatalf("Simulate() error = %v", err)
	}
	return result
}

func simulateWithBPFLoaderPolicy(t *testing.T, input TransactionSimulationInput, policy bpfloader.DeploymentPolicy) (TransactionExecutionResult, error) {
	t.Helper()
	input.Programs = append(input.Programs,
		systemprogram.NewProgram(DefaultBuiltinProgramIDs.System),
		bpfloader.NewProgramWithPolicy(DefaultBuiltinProgramIDs.BPFLoader, policy),
	)
	return TransactionSimulator{}.Simulate(input)
}

func mustGovernedNoopProgramData(t *testing.T, upgradeAuthority PublicKey) []byte {
	t.Helper()
	return mustGovernedNoopProgramDataWithLimit(t, upgradeAuthority, svm.DefaultComputeUnitLimit)
}

func mustGovernedNoopProgramDataWithLimit(t *testing.T, upgradeAuthority PublicKey, computeLimit uint64) []byte {
	t.Helper()
	var authority svm.Address
	copy(authority[:], upgradeAuthority[:])
	programData, err := svm.EncodeGovernedBytecode(svm.BuildProgramCode(), svm.ProgramManifest{
		ComputeUnitLimit: computeLimit,
		UpgradeAuthority: authority,
	})
	if err != nil {
		t.Fatalf("EncodeGovernedBytecode() error = %v", err)
	}
	return programData
}
