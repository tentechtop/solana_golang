package posnode

import (
	"math"
	vmprogram "github.com/tentechtop/ordexus-core/programs/vm"
	"github.com/tentechtop/ordexus-core/structure"
	"testing"
)

func TestStablecoinViewPrecisionDustAndProgramBinding(t *testing.T) {
	owner := vmprogram.StablecoinAddress("test", "owner")
	program := vmprogram.StablecoinAddress("test", "program")
	mint := vmprogram.StablecoinMintAddress(program, owner, "NUSD")
	balance := vmprogram.StablecoinBalanceAddress(program, mint, owner)
	code, _ := vmprogram.StablecoinProgramData()
	data, _ := (vmprogram.StablecoinMint{Creator: owner, Authority: owner, Name: "Dollar", Symbol: "NUSD", Currency: "USD", Limit: math.MaxUint64, Reserve: math.MaxUint64, Supply: math.MaxUint64}).MarshalBinary()
	accounts := map[structure.PublicKey]structure.Account{program: {Executable: true, Owner: structure.DefaultBuiltinProgramIDs.BPFLoader, Data: code}, mint: {Owner: program, Data: data}, balance: {Owner: structure.DefaultBuiltinProgramIDs.System, Lamports: 1}}
	view, err := stablecoinView(accounts, mint, owner)
	if err != nil {
		t.Fatal(err)
	}
	if view.AccountExists || view.Balance != "0" || view.Supply != "18446744073709551615" {
		t.Fatalf("bad view %+v", view)
	}
	b, _ := (vmprogram.StablecoinBalance{Mint: mint, Owner: owner, Amount: 9007199254740993}).MarshalBinary()
	accounts[balance] = structure.Account{Owner: program, Data: b}
	view, err = stablecoinView(accounts, mint, owner)
	if err != nil || view.Balance != "9007199254740993" || !view.AccountExists {
		t.Fatalf("precision %+v %v", view, err)
	}
	wrong := accounts[program]
	wrong.Owner = structure.DefaultBuiltinProgramIDs.System
	accounts[program] = wrong
	if _, err = stablecoinView(accounts, mint, owner); err == nil {
		t.Fatal("unrecognized loader accepted")
	}
}
