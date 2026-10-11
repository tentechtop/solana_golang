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

// These are signed by the actual Ordexus JavaScript builder with public TEST keys.
// The fixture exercises wire compatibility, signatures and VM atomic rollback.
func TestStablecoinWalletLifecycle(t *testing.T) {
	var fixture struct {
		Program string `json:"program"`
		Mint    string `json:"mint"`
		Issuer  string `json:"issuer"`
		Holder  string `json:"holder"`
		Other   string `json:"other"`
		Steps   []struct {
			Label       string `json:"label"`
			Success     bool   `json:"success"`
			Transaction string `json:"transaction"`
		} `json:"steps"`
	}
	data, e := os.ReadFile("testdata/stablecoin-wallet.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(data, &fixture); e != nil {
		t.Fatal(e)
	}
	key := func(text string) PublicKey {
		p, e := structure.PublicKeyFromBase58(text)
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	issuer, holder, other := key(fixture.Issuer), key(fixture.Holder), key(fixture.Other)
	accounts := []AddressedAccount{
		newSimulationAccount(t, issuer, 200_000_000_000, DefaultBuiltinProgramIDs.System, false),
		newSimulationAccount(t, holder, 200_000_000_000, DefaultBuiltinProgramIDs.System, false),
		newSimulationAccount(t, other, 200_000_000_000, DefaultBuiltinProgramIDs.System, false),
	}
	for _, step := range fixture.Steps {
		wire, e := base64.StdEncoding.DecodeString(step.Transaction)
		if e != nil {
			t.Fatal(e)
		}
		tx, e := structure.UnmarshalTransactionBinary(wire)
		if e != nil {
			t.Fatalf("%s: %v", step.Label, e)
		}
		result, e := simulateWithVirtualMachine(t, TransactionSimulationInput{Transaction: tx, Accounts: accounts, BlockhashQueue: newSimulationBlockhashQueue(t, tx.RecentBlockhash, 240), CurrentSlot: 240})
		if e != nil {
			t.Fatalf("%s: %v", step.Label, e)
		}
		if (result.Status == TransactionStatusConfirmed) != step.Success {
			t.Fatalf("%s: status %v, expected success=%v, error=%+v", step.Label, result.Status, step.Success, result.Error)
		}
		next := mergeSimulationAccounts(accounts, result.WrittenAccounts)
		if !step.Success {
			for _, a := range next {
				if a.Address == tx.Accounts[0].PublicKey {
					continue
				} // A failed transaction may charge the payer's fee.
				found := false
				for _, old := range accounts {
					if old.Address == a.Address {
						found = true
						if old.Account.Lamports != a.Account.Lamports || old.Account.Owner != a.Account.Owner || old.Account.Executable != a.Account.Executable || !bytes.Equal(old.Account.Data, a.Account.Data) {
							t.Fatalf("%s: failed transaction changed %s", step.Label, a.Address.String())
						}
						break
					}
				}
				if !found && (a.Account.Lamports != 0 || len(a.Account.Data) != 0 || a.Account.Executable || a.Account.Owner != DefaultBuiltinProgramIDs.System) {
					t.Fatalf("%s: failed transaction left a new account %s", step.Label, a.Address.String())
				}
			}
		}
		accounts = next
	}
	var mint vmprogram.StablecoinMint
	for _, a := range accounts {
		if a.Address == key(fixture.Mint) {
			mint, e = vmprogram.UnmarshalStablecoinMint(a.Account.Data)
			if e != nil {
				t.Fatal(e)
			}
		}
	}
	if mint.Supply != 75_000_000 || mint.Reserve != 80_000_000 || mint.PendingRedemption != 0 || mint.Authority != other {
		t.Fatalf("unexpected final mint: %+v", mint)
	}
	var balanceSum uint64
	for _, a := range accounts {
		if a.Account.Owner != key(fixture.Program) {
			continue
		}
		s, e := vmprogram.UnmarshalStablecoinBalance(a.Account.Data)
		if e == nil {
			balanceSum += s.Amount
		}
	}
	if balanceSum != mint.Supply {
		t.Fatalf("balances %d != supply %d", balanceSum, mint.Supply)
	}
}
