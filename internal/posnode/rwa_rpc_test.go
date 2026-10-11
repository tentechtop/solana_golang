package posnode

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"testing"

	vmprogram "github.com/tentechtop/ordexus-core/programs/vm"
	"github.com/tentechtop/ordexus-core/structure"
)

func TestRWAViewPrecisionComplianceAndProgramBinding(t *testing.T) {
	owner := vmprogram.StablecoinAddress("rwa-rpc-test", "owner")
	program := vmprogram.StablecoinAddress("rwa-rpc-test", "program")
	mint := vmprogram.RWAMintAddress(program, owner, "RSHARE")
	holding := vmprogram.RWAHoldingAddress(program, mint, owner)
	code, _ := vmprogram.RWAProgramData()
	mintData, _ := (vmprogram.RWAMint{
		AssetType: vmprogram.RWAAssetShare, Creator: owner, Authority: owner,
		Name: "Regulated Share", Symbol: "RSHARE", DocumentHash: testRWAHash("documents"),
		MaxSupply: math.MaxUint64, Supply: math.MaxUint64,
	}).MarshalBinary()
	accounts := map[structure.PublicKey]structure.Account{
		program: {Executable: true, Owner: structure.DefaultBuiltinProgramIDs.BPFLoader, Data: code},
		mint:    {Owner: program, Data: mintData},
		holding: {Owner: structure.DefaultBuiltinProgramIDs.System, Lamports: 1},
	}
	view, err := rwaView(accounts, mint, owner)
	if err != nil || view.HoldingExists || view.Balance != "0" || view.Supply != "18446744073709551615" || view.AssetType != "share" {
		t.Fatalf("empty view: %+v, %v", view, err)
	}
	holdingData, _ := (vmprogram.RWAHolding{Mint: mint, Owner: owner, Amount: 9007199254740993, Approved: true, IdentityHash: testRWAHash("identity")}).MarshalBinary()
	accounts[holding] = structure.Account{Owner: program, Data: holdingData}
	view, err = rwaView(accounts, mint, owner)
	if err != nil || !view.HoldingExists || !view.Approved || view.Balance != "9007199254740993" {
		t.Fatalf("holding view: %+v, %v", view, err)
	}
	wrong := accounts[program]
	wrong.Owner = structure.DefaultBuiltinProgramIDs.System
	accounts[program] = wrong
	if _, err = rwaView(accounts, mint, owner); err == nil {
		t.Fatal("unrecognized RWA loader accepted")
	}
}

func testRWAHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
