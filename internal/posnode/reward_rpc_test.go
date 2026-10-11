package posnode

import (
	vmprogram "github.com/tentechtop/ordexus-core/programs/vm"
	"github.com/tentechtop/ordexus-core/structure"
	"testing"
)

func TestRewardViewValidatesBindingsAndReportsActualClaimState(t *testing.T) {
	program, owner := vmprogram.StablecoinAddress("rpc-rewards", "program"), vmprogram.StablecoinAddress("rpc-rewards", "owner")
	config, mint := vmprogram.RewardConfigAddress(program), vmprogram.RewardMintAddress(program)
	balance, receipt := vmprogram.RewardBalanceAddress(program, owner), vmprogram.RewardReceiptAddress(program, owner)
	code, _ := vmprogram.RewardProgramData()
	c, _ := (vmprogram.RewardConfig{Issuer: owner, Oracle: owner, Mint: mint, Cap: 100, SignInReward: 1, VideoReward: 2, TotalClaimed: 3}).MarshalBinary()
	m, _ := (vmprogram.AssetMintState{Version: 1, Kind: vmprogram.AssetKindFungible, Decimals: 6, Authority: config, Supply: 3, MaxSupply: 100, Name: "PI", Symbol: "PI"}).MarshalBinary()
	b, _ := (vmprogram.AssetBalanceState{Version: 1, Mint: mint, Owner: owner, Amount: 3}).MarshalBinary()
	r, _ := (vmprogram.RewardReceipt{Config: config, Owner: owner, Day: 20000, Flags: 3}).MarshalBinary()
	accounts := map[structure.PublicKey]structure.Account{program: {Owner: structure.DefaultBuiltinProgramIDs.BPFLoader, Executable: true, Data: code}, config: {Owner: program, Data: c}, mint: {Owner: program, Data: m}, balance: {Owner: program, Data: b}, receipt: {Owner: program, Data: r}}
	view, err := rewardView(accounts, program, owner)
	if err != nil {
		t.Fatal(err)
	}
	if view["balance"] != "3" || view["claim_day"] != "20000" || view["claim_flags"] != uint8(3) || view["commitment"] != "finalized" {
		t.Fatal(view)
	}
	a := accounts[receipt]
	a.Owner = owner
	accounts[receipt] = a
	if _, err := rewardView(accounts, program, owner); err == nil {
		t.Fatal("foreign receipt accepted")
	}
	// An unsolicited ORDU transfer must not prevent a new wallet from claiming.
	accounts[balance] = structure.Account{Owner: structure.DefaultBuiltinProgramIDs.System, Lamports: 1}
	accounts[receipt] = structure.Account{Owner: structure.DefaultBuiltinProgramIDs.System, Lamports: 1}
	view, err = rewardView(accounts, program, owner)
	if err != nil || view["balance_exists"] != false || view["receipt_exists"] != false {
		t.Fatalf("prefunded accounts should remain uninitialized: %v %v", view, err)
	}
}
