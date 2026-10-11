package vmprogram

import (
	"bytes"
	"math"
	"testing"

	"github.com/tentechtop/ordexus-core/runtime"
	"github.com/tentechtop/ordexus-core/structure"
	ovm "github.com/tentechtop/ordexus-core/vm"
	"github.com/tentechtop/ordexus-core/vm/assembler"
)

func TestStablecoinMintInvariantAndCodec(t *testing.T) {
	key := StablecoinAddress("test", "creator")
	valid := StablecoinMint{Creator: key, Authority: key, Name: "Dollar", Symbol: "NUSD", Currency: "USD", Decimals: 6, Supply: math.MaxUint64 - 1, PendingRedemption: 1, Reserve: math.MaxUint64, Limit: math.MaxUint64}
	data, err := valid.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := UnmarshalStablecoinMint(data)
	if err != nil || decoded != valid {
		t.Fatalf("roundtrip: %+v %v", decoded, err)
	}
	if _, err = UnmarshalAssetMintStateBinary(data); err == nil {
		t.Fatal("legacy codec accepted stablecoin state")
	}
	if _, err = UnmarshalStablecoinMint(append(data, 0)); err == nil {
		t.Fatal("trailing bytes accepted")
	}
	for _, change := range []func(*StablecoinMint){func(s *StablecoinMint) { s.Supply++ }, func(s *StablecoinMint) { s.Reserve-- }, func(s *StablecoinMint) { s.Limit-- }, func(s *StablecoinMint) { s.Decimals = 10 }} {
		bad := valid
		change(&bad)
		if bad.Validate() == nil {
			t.Fatal("invalid invariant accepted")
		}
	}
}

func TestStablecoinTemplateMatchesAssembly(t *testing.T) {
	// Keep chain tests runnable without the independently maintained editor checkout.
	source := "mov r9, 0x53544231\nsyscall asset_execute\nexit\n"
	built, err := assembler.AssembleWithManifest(source, ovm.ProgramManifest{ComputeUnitLimit: ovm.DefaultComputeUnitLimit, RequiredSyscalls: []ovm.SyscallID{ovm.SyscallAssetExecute}})
	if err != nil {
		t.Fatal(err)
	}
	expected, err := StablecoinProgramData()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(built, expected) {
		t.Fatal("studio template differs from pinned wallet program")
	}
	changed := append([]byte(nil), built...)
	changed[len(changed)-1] ^= 1
	if IsStablecoinProgram(changed) {
		t.Fatal("changed template accepted")
	}
}

func TestStablecoinRejectsReadOnlyMintUnsignedActorAndLegacyInstruction(t *testing.T) {
	actor := StablecoinAddress("test", "actor")
	program := StablecoinAddress("test", "program")
	mint := StablecoinMintAddress(program, actor, "NUSD")
	code, _ := StablecoinProgramData()
	for _, mode := range []string{"valid", "readonly", "unsigned", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			keys := []structure.PublicKey{actor, mint, program}
			accounts := map[structure.PublicKey]structure.Account{actor: {Owner: structure.DefaultBuiltinProgramIDs.System, Lamports: 100000000}, mint: {Owner: program, Lamports: 100000000}, program: {Owner: structure.DefaultBuiltinProgramIDs.BPFLoader, Lamports: 100000000, Executable: true, Data: code}}
			instruction := AssetInstruction{Type: StableInitialize, Amount: 1000, Decimals: 6, Name: "Dollar", Symbol: "NUSD", URI: "USD"}
			if mode == "legacy" {
				instruction, _ = NewAssetInitializeFungibleInstruction(6, "Dollar", "NUSD")
			}
			wire, err := instruction.MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}
			header := structure.MessageHeader{NumRequiredSignatures: 1, NumReadonlyUnsignedAccounts: 1}
			if mode == "readonly" {
				header.NumReadonlyUnsignedAccounts = 2
			}
			if mode == "unsigned" {
				header.NumRequiredSignatures = 0
			}
			ctx := runtime.InstructionContext{Instruction: structure.CompiledInstruction{ProgramIDIndex: 2, AccountIndexes: []uint8{0, 1}, Data: wire}, Message: structure.ResolvedMessage{Header: header, StaticAccountKeys: keys, AccountKeys: keys}, Accounts: accounts, RentConfig: structure.DefaultRentConfig, BuiltinPrograms: structure.DefaultBuiltinProgramIDs}
			err = NewProgram(structure.DefaultBuiltinProgramIDs.BPFLoader, ovm.Runtime{}).Execute(ctx)
			if (err == nil) != (mode == "valid") {
				t.Fatalf("%s: %v", mode, err)
			}
			if mode != "valid" && len(accounts[mint].Data) != 0 {
				t.Fatal("failed initialization wrote mint")
			}
		})
	}
}
