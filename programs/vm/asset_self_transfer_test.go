package vmprogram

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tentechtop/ordexus-core/runtime"
	"github.com/tentechtop/ordexus-core/structure"
)

func TestAssetSelfTransferPreservesBalanceAndSupply(t *testing.T) {
	for _, kind := range []AssetKind{AssetKindFungible, AssetKindNFT} {
		for _, delegated := range []bool{false, true} {
			actor, mint, balance, allowance, program := testPublicKey(1), testPublicKey(2), testPublicKey(3), testPublicKey(4), testPublicKey(5)
			amount := uint64(7)
			maxSupply := uint64(0)
			if kind == AssetKindNFT {
				amount = 1
				maxSupply = 1
			}
			mintData, err := (AssetMintState{Version: 1, Kind: kind, Authority: actor, Supply: amount, MaxSupply: maxSupply, Name: "Self", Symbol: "SELF", URI: "https://example.com/self"}).MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}
			balanceData, _ := (AssetBalanceState{Version: 1, Mint: mint, Owner: actor, Amount: amount}).MarshalBinary()
			allowanceData, _ := (AssetAllowanceState{Version: 1, Source: balance, Mint: mint, Owner: actor, Delegate: actor, Amount: amount}).MarshalBinary()
			accounts := map[structure.PublicKey]structure.Account{
				mint:      testAccount(t, 100000000, mintData, program, false),
				balance:   testAccount(t, 100000000, balanceData, program, false),
				allowance: testAccount(t, 100000000, allowanceData, program, false),
			}
			keys := []structure.PublicKey{actor, balance, allowance, mint, program}
			indexes := []uint8{0, 3, 1, 1}
			if delegated {
				indexes = append(indexes, 2)
			}
			context := runtime.InstructionContext{
				Instruction: structure.CompiledInstruction{ProgramIDIndex: 4, AccountIndexes: indexes},
				Message:     structure.ResolvedMessage{Header: structure.MessageHeader{NumRequiredSignatures: 1, NumReadonlyUnsignedAccounts: 2}, StaticAccountKeys: keys, AccountKeys: keys},
				Accounts:    accounts, RentConfig: structure.DefaultRentConfig,
			}
			call := executeAssetTransfer
			if delegated {
				call = executeAssetTransferFrom
			}
			if err := call(AssetInstruction{Amount: amount}, context, accounts); err != nil {
				t.Fatalf("kind %d delegated %v: %v", kind, delegated, err)
			}
			if !bytes.Equal(accounts[balance].Data, balanceData) || !bytes.Equal(accounts[mint].Data, mintData) {
				t.Fatalf("self transfer changed balance or supply: kind %d delegated %v", kind, delegated)
			}
			if delegated {
				state, err := UnmarshalAssetAllowanceStateBinary(accounts[allowance].Data)
				if err != nil || state.Amount != 0 {
					t.Fatalf("delegated transfer did not consume allowance: %v", err)
				}
			}
			if err := call(AssetInstruction{Amount: amount + 1}, context, accounts); err == nil {
				t.Fatal("self transfer bypassed balance or allowance limits")
			}
			context.Message.Header.NumRequiredSignatures = 0
			if err := call(AssetInstruction{Amount: amount}, context, accounts); err == nil || !strings.Contains(err.Error(), "missing required signature") {
				t.Fatalf("self transfer bypassed signer check: %v", err)
			}
		}
	}
}

func TestAssetSelfTransferAtUint64Limit(t *testing.T) {
	state := AssetBalanceState{Amount: ^uint64(0)}
	if err := applyAssetTransfer(1, AssetMintState{Kind: AssetKindFungible}, &state, &state); err != nil || state.Amount != ^uint64(0) {
		t.Fatalf("valid self transfer overflowed or changed balance: %v", err)
	}
}
