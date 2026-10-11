package blockchain

import (
	"bytes"
	"testing"

	bpfloader "github.com/tentechtop/ordexus-core/programs/bpfloader"
	"github.com/tentechtop/ordexus-core/structure"
	"github.com/tentechtop/ordexus-core/utils"
	ovm "github.com/tentechtop/ordexus-core/vm"
)

func TestNewDeployContractTransactionRequiresManifestAuthoritySigner(t *testing.T) {
	payer := contractTestKeyPair(t, "authority-deploy-payer")
	program := contractTestKeyPair(t, "authority-deploy-program")
	authority := contractTestKeyPair(t, "authority-deploy-authority")
	var authorityAddress ovm.Address
	copy(authorityAddress[:], authority.PublicKey[:])
	programData, err := ovm.EncodeGovernedBytecode(ovm.BuildProgramCode(), ovm.ProgramManifest{
		ComputeUnitLimit: 200_000,
		UpgradeAuthority: authorityAddress,
	})
	if err != nil {
		t.Fatalf("EncodeGovernedBytecode() error = %v", err)
	}
	blockhash := contractTestHash(t, "authority-deploy-blockhash")
	if _, err := NewDeployContractTransaction(payer, program, programData, 0, blockhash); err == nil {
		t.Fatal("NewDeployContractTransaction() error = nil, want missing authority signer")
	}
	transaction, err := NewDeployContractTransactionWithSigners(
		payer,
		program,
		programData,
		0,
		[]structure.OrdexusKeyPair{authority},
		blockhash,
	)
	if err != nil {
		t.Fatalf("NewDeployContractTransactionWithSigners() error = %v", err)
	}
	if len(transaction.Signatures) != 3 {
		t.Fatalf("signature count = %d, want 3", len(transaction.Signatures))
	}
	if valid, err := transaction.HasValidSignatures(); err != nil || !valid {
		t.Fatalf("HasValidSignatures() = %v, %v", valid, err)
	}
}

func TestNewDeployContractTransactionsSplitsLargeBytecode(t *testing.T) {
	payer := contractTestKeyPair(t, "large-deploy-payer")
	program := contractTestKeyPair(t, "large-deploy-program")
	programData, err := ovm.EncodeBytecode(bytes.Repeat([]byte{ovm.OpReturn}, bpfloader.MaxLoaderTransactionDataBytes*2+17))
	if err != nil {
		t.Fatalf("EncodeBytecode() error = %v", err)
	}

	transactions, err := NewDeployContractTransactions(
		payer,
		program,
		programData,
		0,
		contractTestHash(t, "large-deploy-blockhash"),
	)
	if err != nil {
		t.Fatalf("NewDeployContractTransactions() error = %v", err)
	}
	if len(transactions) != 5 {
		t.Fatalf("transaction count = %d, want create, three writes, finalize", len(transactions))
	}
	reassembled := make([]byte, len(programData))
	for transactionIndex, transaction := range transactions {
		if valid, err := transaction.HasValidSignatures(); err != nil || !valid {
			t.Fatalf("transaction %d signatures = %v, %v", transactionIndex, valid, err)
		}
		encoded, err := transaction.MarshalBinary()
		if err != nil {
			t.Fatalf("transaction %d MarshalBinary() error = %v", transactionIndex, err)
		}
		if len(encoded) > structure.MaxOrdexusTransactionSize {
			t.Fatalf("transaction %d size = %d", transactionIndex, len(encoded))
		}
		if transactionIndex == 0 || transactionIndex == len(transactions)-1 {
			continue
		}
		message, err := transaction.OrdexusMessage()
		if err != nil {
			t.Fatalf("OrdexusMessage() error = %v", err)
		}
		for _, compiled := range message.Instructions {
			instruction, err := bpfloader.UnmarshalInstructionBinary(compiled.Data)
			if err != nil {
				t.Fatalf("UnmarshalInstructionBinary() error = %v", err)
			}
			copy(reassembled[int(instruction.Offset):], instruction.Data)
		}
	}
	if !bytes.Equal(reassembled, programData) {
		t.Fatal("reassembled bytecode differs from input")
	}
}

func TestNewCreateContractAccountTransactionBuildsRentExemptOwnedAccount(t *testing.T) {
	payer := contractTestKeyPair(t, "contract-account-payer")
	account := contractTestKeyPair(t, "contract-state-account")
	program := contractTestKeyPair(t, "contract-account-program").PublicKey
	blockhash := contractTestHash(t, "contract-account-blockhash")

	transaction, err := NewCreateContractAccountTransaction(payer, account, program, 64, 7, blockhash)
	if err != nil {
		t.Fatalf("NewCreateContractAccountTransaction() error = %v", err)
	}
	if valid, err := transaction.HasValidSignatures(); err != nil || !valid {
		t.Fatalf("HasValidSignatures() = %v, %v", valid, err)
	}
	message, err := transaction.OrdexusMessage()
	if err != nil {
		t.Fatalf("OrdexusMessage() error = %v", err)
	}
	if len(message.Instructions) != 1 || message.AccountKeys[message.Instructions[0].ProgramIDIndex] != structure.DefaultBuiltinProgramIDs.System {
		t.Fatalf("create account instruction = %+v", message.Instructions)
	}
	decoded, err := structure.UnmarshalSystemInstructionBinary(message.Instructions[0].Data)
	if err != nil {
		t.Fatalf("UnmarshalSystemInstructionBinary() error = %v", err)
	}
	minimumBalance, err := structure.MinimumBalanceForRentExemption(64)
	if err != nil {
		t.Fatalf("MinimumBalanceForRentExemption() error = %v", err)
	}
	if decoded.CreateAccount == nil || decoded.CreateAccount.Owner != program || decoded.CreateAccount.Space != 64 || decoded.CreateAccount.Lamports != minimumBalance+7 {
		t.Fatalf("create account params = %+v", decoded.CreateAccount)
	}
}

func TestNewInvokeContractTransactionPreservesInstructionAccountOrder(t *testing.T) {
	payer := contractTestKeyPair(t, "contract-payer")
	signer := contractTestKeyPair(t, "contract-signer")
	readonly := contractTestKeyPair(t, "contract-readonly").PublicKey
	writable := contractTestKeyPair(t, "contract-writable").PublicKey
	program := contractTestKeyPair(t, "contract-program").PublicKey
	blockhash := contractTestHash(t, "contract-blockhash")

	transaction, err := NewInvokeContractTransaction(
		payer,
		program,
		[]structure.AccountMeta{
			{PublicKey: readonly},
			{PublicKey: signer.PublicKey, IsSigner: true},
			{PublicKey: writable, IsWritable: true},
			{PublicKey: payer.PublicKey, IsSigner: true, IsWritable: true},
		},
		[]byte("call-data"),
		[]structure.OrdexusKeyPair{signer},
		blockhash,
	)
	if err != nil {
		t.Fatalf("NewInvokeContractTransaction() error = %v", err)
	}
	if valid, err := transaction.HasValidSignatures(); err != nil || !valid {
		t.Fatalf("HasValidSignatures() = %v, %v", valid, err)
	}
	message, err := transaction.OrdexusMessage()
	if err != nil {
		t.Fatalf("OrdexusMessage() error = %v", err)
	}
	instruction := message.Instructions[0]
	want := []structure.PublicKey{readonly, signer.PublicKey, writable, payer.PublicKey}
	for index, accountIndex := range instruction.AccountIndexes {
		if got := message.AccountKeys[accountIndex]; got != want[index] {
			t.Fatalf("instruction account %d = %s, want %s", index, got.String(), want[index].String())
		}
	}
	if message.AccountKeys[instruction.ProgramIDIndex] != program {
		t.Fatalf("program = %s, want %s", message.AccountKeys[instruction.ProgramIDIndex].String(), program.String())
	}
}

func TestNewInvokeContractTransactionRejectsMissingSignerKey(t *testing.T) {
	payer := contractTestKeyPair(t, "missing-signer-payer")
	signer := contractTestKeyPair(t, "missing-signer")
	program := contractTestKeyPair(t, "missing-signer-program").PublicKey
	_, err := NewInvokeContractTransaction(
		payer,
		program,
		[]structure.AccountMeta{{PublicKey: signer.PublicKey, IsSigner: true}},
		nil,
		nil,
		contractTestHash(t, "missing-signer-blockhash"),
	)
	if err == nil {
		t.Fatal("NewInvokeContractTransaction() error = nil, want missing signer")
	}
}

func contractTestKeyPair(t *testing.T, seed string) structure.OrdexusKeyPair {
	t.Helper()
	keyPair, err := structure.KeyPairFromSeed(utils.SHA256([]byte(seed)))
	if err != nil {
		t.Fatalf("KeyPairFromSeed() error = %v", err)
	}
	return keyPair
}

func contractTestHash(t *testing.T, seed string) structure.Hash {
	t.Helper()
	hash, err := structure.NewHash(utils.SHA256([]byte(seed)))
	if err != nil {
		t.Fatalf("NewHash() error = %v", err)
	}
	return hash
}
