package posnode

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	vmprogram "github.com/tentechtop/ordexus-core/programs/vm"
	"github.com/tentechtop/ordexus-core/rpc"
	"github.com/tentechtop/ordexus-core/structure"
)

func stablecoinAccountMap(accounts []structure.AddressedAccount) map[structure.PublicKey]structure.Account {
	result := make(map[structure.PublicKey]structure.Account, len(accounts))
	for _, a := range accounts {
		result[a.Address] = a.Account
	}
	return result
}

// Fiat settlement must never use a speculative fork. Missing historical snapshots
// fail closed instead of silently falling back to the current head.
func (node *posNode) stablecoinFinalizedAccounts() (map[structure.PublicKey]structure.Account, error) {
	head := node.ledger.Head()
	if head.FinalizedHash.IsZero() {
		return nil, fmt.Errorf("stablecoin: finalized state is not available")
	}
	state, err := node.ledger.StateAtBlockHash(head.FinalizedHash)
	if err != nil {
		return nil, err
	}
	return stablecoinAccountMap(state.Accounts), nil
}

func stablecoinView(accounts map[structure.PublicKey]structure.Account, mint, owner structure.PublicKey) (rpc.StablecoinStateResult, error) {
	a, ok := accounts[mint]
	if !ok || a.Executable {
		return rpc.StablecoinStateResult{}, fmt.Errorf("stablecoin: mint not found")
	}
	program, ok := accounts[a.Owner]
	if !ok || !program.Executable || program.Owner != structure.DefaultBuiltinProgramIDs.BPFLoader || !vmprogram.IsStablecoinProgram(program.Data) {
		return rpc.StablecoinStateResult{}, fmt.Errorf("stablecoin: unrecognized program")
	}
	s, e := vmprogram.UnmarshalStablecoinMint(a.Data)
	if e != nil {
		return rpc.StablecoinStateResult{}, e
	}
	if mint != vmprogram.StablecoinMintAddress(a.Owner, s.Creator, s.Symbol) {
		return rpc.StablecoinStateResult{}, fmt.Errorf("stablecoin: mint binding mismatch")
	}
	key := vmprogram.StablecoinBalanceAddress(a.Owner, mint, owner)
	v := rpc.StablecoinStateResult{Address: mint.String(), Program: a.Owner.String(), Creator: s.Creator.String(), Authority: s.Authority.String(), Name: s.Name, Symbol: s.Symbol, Currency: s.Currency, Decimals: s.Decimals, Supply: strconv.FormatUint(s.Supply, 10), DeclaredReserve: strconv.FormatUint(s.Reserve, 10), PendingRedemption: strconv.FormatUint(s.PendingRedemption, 10), Limit: strconv.FormatUint(s.Limit, 10), Paused: s.Paused, BalanceAddress: key.String(), Balance: "0"}
	if !s.PendingAuthority.IsZero() {
		v.PendingAuthority = s.PendingAuthority.String()
	}
	if b, ok := accounts[key]; ok {
		if b.Owner == structure.DefaultBuiltinProgramIDs.System && !b.Executable && len(b.Data) == 0 {
			return v, nil
		}
		balance, e := vmprogram.UnmarshalStablecoinBalance(b.Data)
		if e != nil || b.Owner != a.Owner || b.Executable || balance.Mint != mint || balance.Owner != owner {
			return v, fmt.Errorf("stablecoin: invalid balance account")
		}
		v.AccountExists = true
		v.Balance = strconv.FormatUint(balance.Amount, 10)
	}
	return v, nil
}

func (node *posNode) GetStablecoinState(ctx context.Context, mintText, ownerText string) (rpc.StablecoinStateResult, error) {
	if e := ctx.Err(); e != nil {
		return rpc.StablecoinStateResult{}, e
	}
	mint, e := decodePublicKey(mintText, "stablecoin mint")
	if e != nil {
		return rpc.StablecoinStateResult{}, e
	}
	owner, e := decodePublicKey(ownerText, "stablecoin owner")
	if e != nil {
		return rpc.StablecoinStateResult{}, e
	}
	accounts, e := node.stablecoinFinalizedAccounts()
	if e != nil {
		return rpc.StablecoinStateResult{}, e
	}
	return stablecoinView(accounts, mint, owner)
}

func (node *posNode) GetStablecoins(ctx context.Context, ownerText, cursor string, limit int) (rpc.StablecoinListResult, error) {
	result := rpc.StablecoinListResult{Assets: []rpc.StablecoinStateResult{}}
	owner, e := decodePublicKey(ownerText, "stablecoin owner")
	if e != nil {
		return result, e
	}
	if limit < 1 || limit > 100 {
		return result, fmt.Errorf("stablecoin: limit must be 1..100")
	}
	accounts, e := node.stablecoinFinalizedAccounts()
	if e != nil {
		return result, e
	}
	keys := []structure.PublicKey{}
	for key, a := range accounts {
		if e := ctx.Err(); e != nil {
			return result, e
		}
		if len(a.Data) > 0 && a.Data[0] == vmprogram.StableMintKind && key.String() > cursor {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	for _, key := range keys {
		v, e := stablecoinView(accounts, key, owner)
		if e != nil {
			continue
		}
		if len(result.Assets) == limit {
			result.NextCursor = result.Assets[len(result.Assets)-1].Address
			break
		}
		result.Assets = append(result.Assets, v)
	}
	return result, nil
}

func (node *posNode) GetStablecoinOrders(ctx context.Context, mintText, ownerText, cursor string, limit int) (rpc.StablecoinOrderListResult, error) {
	result := rpc.StablecoinOrderListResult{Orders: []rpc.StablecoinOrderResult{}}
	mint, e := decodePublicKey(mintText, "stablecoin mint")
	if e != nil {
		return result, e
	}
	owner, e := decodePublicKey(ownerText, "stablecoin owner")
	if e != nil {
		return result, e
	}
	if limit < 1 || limit > 100 {
		return result, fmt.Errorf("stablecoin: limit must be 1..100")
	}
	accounts, e := node.stablecoinFinalizedAccounts()
	if e != nil {
		return result, e
	}
	view, e := stablecoinView(accounts, mint, owner)
	if e != nil {
		return result, e
	}
	program := accounts[mint].Owner
	for key, a := range accounts {
		if e := ctx.Err(); e != nil {
			return result, e
		}
		if a.Owner != program || a.Executable || len(a.Data) == 0 || a.Data[0] != vmprogram.StableOrderKind || key.String() <= cursor {
			continue
		}
		o, e := vmprogram.UnmarshalStablecoinOrder(a.Data)
		if e != nil || o.Mint != mint || key != vmprogram.StablecoinOrderAddress(program, mint, o.Kind, o.Owner, o.Reference) {
			continue
		}
		if view.Authority != owner.String() && o.Owner != owner {
			continue
		}
		kind := "redemption"
		if o.Kind == vmprogram.StableOrderIssue {
			kind = "issuance"
		}
		status := map[uint8]string{1: "pending", 2: "processing", 3: "settled", 4: "cancelled"}[o.Status]
		result.Orders = append(result.Orders, rpc.StablecoinOrderResult{Address: key.String(), Mint: mint.String(), Owner: o.Owner.String(), Kind: kind, Status: status, Amount: strconv.FormatUint(o.Amount, 10), Reference: o.Reference, Receipt: o.Receipt})
	}
	sort.Slice(result.Orders, func(i, j int) bool { return result.Orders[i].Address < result.Orders[j].Address })
	if len(result.Orders) > limit {
		result.Orders = result.Orders[:limit]
		result.NextCursor = result.Orders[limit-1].Address
	}
	return result, nil
}
