package system

import (
	"bytes"
	"github.com/tentechtop/ordexus-core/runtime"
	"github.com/tentechtop/ordexus-core/structure"
	"testing"
)

// Stablecoin allocation keys are public; their signatures must never authorize
// resetting existing program state or withdrawing the account's rent.
func TestSystemCannotOverwriteReassignResizeOrDrainProgramState(t *testing.T) {
	payer := structure.PublicKey{1}
	target := structure.PublicKey{2}
	owner := structure.PublicKey{3}
	system := structure.DefaultBuiltinProgramIDs.System
	create, _ := structure.NewCreateAccountInstruction(structure.CreateAccountParams{Lamports: 10000000, Owner: owner})
	assign, _ := structure.NewAssignInstruction(structure.AssignParams{Owner: system})
	allocate, _ := structure.NewAllocateInstruction(structure.AllocateParams{Space: 0})
	transfer, _ := structure.NewTransferInstruction(structure.TransferParams{Lamports: 1})
	for _, tc := range []struct {
		name        string
		instruction structure.SystemInstruction
		indexes     []uint8
	}{{"recreate", create, []uint8{0, 1}}, {"assign", assign, []uint8{1}}, {"allocate", allocate, []uint8{1}}, {"drain", transfer, []uint8{1, 0}}} {
		t.Run(tc.name, func(t *testing.T) {
			original := structure.Account{Owner: owner, Lamports: 100000000, Data: []byte{18, 1, 0, 42}}
			accounts := map[structure.PublicKey]structure.Account{payer: {Owner: system, Lamports: 100000000}, target: original.Clone()}
			data, err := tc.instruction.MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}
			keys := []structure.PublicKey{payer, target, system}
			ctx := runtime.InstructionContext{Accounts: accounts, Instruction: structure.CompiledInstruction{ProgramIDIndex: 2, AccountIndexes: tc.indexes, Data: data}, Message: structure.ResolvedMessage{Header: structure.MessageHeader{NumRequiredSignatures: 2, NumReadonlyUnsignedAccounts: 1}, StaticAccountKeys: keys, AccountKeys: keys}, RentConfig: structure.DefaultRentConfig, BuiltinPrograms: structure.DefaultBuiltinProgramIDs}
			if err = NewProgram(system).Execute(ctx); err == nil {
				t.Fatal("program state mutation accepted despite valid allocation-key signature")
			}
			got := accounts[target]
			if got.Owner != original.Owner || got.Lamports != original.Lamports || !bytes.Equal(got.Data, original.Data) || accounts[payer].Lamports != 100000000 {
				t.Fatal("rejected operation mutated accounts")
			}
		})
	}
}
