package runtime_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"

	vmprogram "github.com/tentechtop/ordexus-core/programs/vm"
	"github.com/tentechtop/ordexus-core/structure"
)

func TestMarketSignedWalletLifecycle(t *testing.T) {
	var f struct {
		Seller, Buyer, TokenProgram, Mint string
		Orders                            []string
		Steps                             []struct {
			Label       string
			Success     bool
			Transaction string
		}
	}
	data, err := os.ReadFile("testdata/market-wallet.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	key := func(s string) PublicKey {
		k, e := structure.PublicKeyFromBase58(s)
		if e != nil {
			t.Fatal(e)
		}
		return k
	}
	accounts := []AddressedAccount{newSimulationAccount(t, key(f.Seller), 200000000000, DefaultBuiltinProgramIDs.System, false), newSimulationAccount(t, key(f.Buyer), 200000000000, DefaultBuiltinProgramIDs.System, false)}
	for _, step := range f.Steps {
		wire, _ := base64.StdEncoding.DecodeString(step.Transaction)
		tx, e := structure.UnmarshalTransactionBinary(wire)
		if e != nil {
			t.Fatal(e)
		}
		result, e := simulateWithVirtualMachine(t, TransactionSimulationInput{Transaction: tx, Accounts: accounts, BlockhashQueue: newSimulationBlockhashQueue(t, tx.RecentBlockhash, 240), CurrentSlot: 240})
		if e != nil {
			t.Fatal(e)
		}
		if (result.Status == TransactionStatusConfirmed) != step.Success {
			t.Fatalf("%s: status=%v error=%+v", step.Label, result.Status, result.Error)
		}
		next := mergeSimulationAccounts(accounts, result.WrittenAccounts)
		if !step.Success {
			for _, a := range next {
				if a.Address == tx.Accounts[0].PublicKey {
					continue
				}
				found := false
				for _, old := range accounts {
					if old.Address == a.Address {
						found = true
						if old.Account.Lamports != a.Account.Lamports || old.Account.Owner != a.Account.Owner || !bytes.Equal(old.Account.Data, a.Account.Data) {
							t.Fatalf("%s changed %s", step.Label, a.Address.String())
						}
					}
				}
				if !found && (len(a.Account.Data) > 0 || a.Account.Lamports > 0) {
					t.Fatal("failed trade created funded account")
				}
			}
		}
		accounts = next
	}
	var sum, sellerAmount, buyerAmount uint64
	for _, a := range accounts {
		if a.Account.Owner == key(f.TokenProgram) {
			b, e := vmprogram.UnmarshalAssetBalanceStateBinary(a.Account.Data)
			if e == nil {
				sum += b.Amount
				if b.Owner == key(f.Seller) {
					sellerAmount += b.Amount
				}
				if b.Owner == key(f.Buyer) {
					buyerAmount += b.Amount
				}
			}
		}
	}
	if sum != 1000000000 || sellerAmount != 800000000 || buyerAmount != 200000000 {
		t.Fatalf("supply conservation: %d seller=%d buyer=%d", sum, sellerAmount, buyerAmount)
	}
	for i, p := range f.Orders {
		for _, a := range accounts {
			if a.Address == vmprogram.MarketStateAddress(key(p)) {
				o, e := vmprogram.UnmarshalMarketOrder(a.Account.Data)
				if e != nil {
					t.Fatal(e)
				}
				want := uint8(vmprogram.MarketFilled)
				if i == 1 {
					want = vmprogram.MarketCancelled
				}
				if o.Status != want || o.Locked != 0 {
					t.Fatalf("invalid terminal order: %+v", o)
				}
			}
		}
	}
}
