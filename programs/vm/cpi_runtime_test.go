package vmprogram

import (
	"testing"

	"github.com/tentechtop/ordexus-core/runtime"
	"github.com/tentechtop/ordexus-core/structure"
	ovm "github.com/tentechtop/ordexus-core/vm"
)

func TestContractCPIRuntimeMapsAccountsAndPdaSigner(t *testing.T) {
	payer := structure.PublicKey{1}
	state := structure.PublicKey{2}
	targetProgram := structure.PublicKey{3}
	outerProgram := structure.PublicKey{4}
	message := structure.ResolvedMessage{
		Header: structure.MessageHeader{
			NumRequiredSignatures:       1,
			NumReadonlyUnsignedAccounts: 1,
		},
		StaticAccountKeys: []structure.PublicKey{payer, state, targetProgram},
		AccountKeys:       []structure.PublicKey{payer, state, targetProgram},
	}
	accounts := map[structure.PublicKey]structure.Account{
		payer:         {Lamports: 100, Owner: structure.DefaultBuiltinProgramIDs.System},
		state:         {Data: []byte{0}, Owner: outerProgram},
		targetProgram: {Owner: structure.DefaultBuiltinProgramIDs.BPFLoader, Executable: true},
	}
	computeUnits := uint64(0)
	trace := runtime.ProgramExecutionTrace{}
	invoked := false
	adapter := newContractCPIRuntime(runtime.InstructionContext{
		Message:          message,
		Accounts:         accounts,
		ComputeUnitsUsed: &computeUnits,
		ExecutionTrace:   &trace,
	}, func(context runtime.InstructionContext) error {
		invoked = true
		if !runtime.IsSignerContextAddress(state, context) {
			t.Fatal("PDA signer override was not propagated")
		}
		account := context.Accounts[state]
		account.Data[0] = 9
		context.Accounts[state] = account
		return nil
	})
	accountSet, err := ovm.NewAccountSet(vmAddressFromPublicKey(outerProgram), []ovm.Account{
		{Address: vmAddressFromPublicKey(state), Data: []byte{0}, Owner: vmAddressFromPublicKey(outerProgram), IsWritable: true},
		{Address: vmAddressFromPublicKey(targetProgram), Owner: vmAddressFromPublicKey(structure.DefaultBuiltinProgramIDs.BPFLoader), Executable: true},
	}, ovm.DefaultMaxDataIncreasePerCall)
	if err != nil {
		t.Fatalf("NewAccountSet() error = %v", err)
	}
	vmContext := &ovm.Context{Accounts: accountSet}
	if err := adapter.Invoke(vmContext, ovm.CPIInstruction{
		ProgramID:       vmAddressFromPublicKey(targetProgram),
		AccountIndexes:  []uint8{0},
		InstructionData: []byte("inner"),
		SignerPDAs:      []ovm.Address{vmAddressFromPublicKey(state)},
	}); err != nil {
		t.Fatalf("Invoke() error = %v", err)
	}
	if !invoked {
		t.Fatal("nested program was not invoked")
	}
	if got := accountSet.Snapshot()[0].Data[0]; got != 9 {
		t.Fatalf("state data = %d, want 9", got)
	}
	if computeUnits != structure.DefaultBuiltinInstructionCU {
		t.Fatalf("compute units = %d, want %d", computeUnits, structure.DefaultBuiltinInstructionCU)
	}
	if len(trace.InnerInstructions) != 1 || trace.InnerInstructions[0].ProgramIDIndex != 2 {
		t.Fatalf("inner instructions = %+v", trace.InnerInstructions)
	}
}
