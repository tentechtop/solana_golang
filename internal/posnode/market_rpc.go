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

func marketAssetView(accounts map[structure.PublicKey]structure.Account, program, owner structure.PublicKey) (rpc.MarketAssetResult, error) {
	v := rpc.MarketAssetResult{}
	if !vmprogram.IsMarketTokenProgram(accounts[program]) {
		return v, fmt.Errorf("market: unsupported token")
	}
	mint := vmprogram.StablecoinAddress("nulla.asset.mint.v1", program.String())
	a := accounts[mint]
	m, err := vmprogram.UnmarshalAssetMintStateBinary(a.Data)
	if err != nil || a.Owner != program || a.Executable || m.Kind != vmprogram.AssetKindFungible || m.Decimals > 9 {
		return v, fmt.Errorf("market: invalid mint")
	}
	key := vmprogram.StablecoinAddress("nulla.asset.balance.v1", program.String(), owner.String())
	v = rpc.MarketAssetResult{Program: program.String(), Mint: mint.String(), Name: m.Name, Symbol: m.Symbol, Decimals: m.Decimals, Balance: "0", BalanceAddress: key.String()}
	if owner.IsZero() {
		return v, nil
	}
	if a, ok := accounts[key]; ok && !(a.Owner == structure.DefaultBuiltinProgramIDs.System && !a.Executable && len(a.Data) == 0) {
		b, e := vmprogram.UnmarshalAssetBalanceStateBinary(a.Data)
		if e != nil || a.Owner != program || a.Executable || b.Mint != mint || b.Owner != owner {
			return v, fmt.Errorf("market: invalid wallet balance")
		}
		v.Balance = strconv.FormatUint(b.Amount, 10)
		v.BalanceExists = true
	}
	return v, nil
}

func marketOrderView(accounts map[structure.PublicKey]structure.Account, program, owner structure.PublicKey) (rpc.MarketOrderResult, error) {
	v := rpc.MarketOrderResult{}
	p := accounts[program]
	if !p.Executable || p.Owner != structure.DefaultBuiltinProgramIDs.BPFLoader || !vmprogram.IsMarketProgram(p.Data) {
		return v, fmt.Errorf("market: unrecognized order program")
	}
	key := vmprogram.MarketStateAddress(program)
	a := accounts[key]
	s, err := vmprogram.UnmarshalMarketOrder(a.Data)
	if err != nil || a.Owner != program || a.Executable {
		return v, fmt.Errorf("market: invalid order state")
	}
	asset, err := marketAssetView(accounts, s.TokenProgram, owner)
	if err != nil {
		return v, err
	}
	if asset.Mint != s.Mint.String() {
		return v, fmt.Errorf("market: mint binding mismatch")
	}
	vaultKey := vmprogram.MarketVaultAddress(program)
	va := accounts[vaultKey]
	vault, err := vmprogram.UnmarshalAssetBalanceStateBinary(va.Data)
	if err != nil || va.Owner != s.TokenProgram || va.Executable || vault.Mint != s.Mint || vault.Owner != vmprogram.MarketVaultAuthority(program) || vault.Amount < s.Locked {
		return v, fmt.Errorf("market: escrow mismatch")
	}
	status := map[uint8]string{vmprogram.MarketOpen: "open", vmprogram.MarketFilled: "filled", vmprogram.MarketCancelled: "cancelled"}[s.Status]
	buyer := ""
	if !s.Buyer.IsZero() {
		buyer = s.Buyer.String()
	}
	return rpc.MarketOrderResult{MarketAssetResult: asset, OrderProgram: program.String(), StateAddress: key.String(), VaultAddress: vaultKey.String(), Seller: s.Seller.String(), Buyer: buyer, Amount: strconv.FormatUint(s.Amount, 10), TotalLamports: strconv.FormatUint(s.TotalLamports, 10), Locked: strconv.FormatUint(s.Locked, 10), Status: status, CreatedSlot: strconv.FormatUint(s.CreatedSlot, 10), ClosedSlot: strconv.FormatUint(s.ClosedSlot, 10), Commitment: "finalized"}, nil
}

func marketOwner(text string) (structure.PublicKey, error) {
	if text == "" {
		return structure.PublicKey{}, nil
	}
	return decodePublicKey(text, "market owner")
}
func marketSortedKeys(accounts map[structure.PublicKey]structure.Account) []structure.PublicKey {
	keys := make([]structure.PublicKey, 0)
	for k, a := range accounts {
		if a.Executable {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	return keys
}
func (node *posNode) GetMarketAssets(ctx context.Context, ownerText, cursor string, limit int) (rpc.MarketAssetList, error) {
	r := rpc.MarketAssetList{Assets: []rpc.MarketAssetResult{}, Commitment: "finalized"}
	if err := ctx.Err(); err != nil {
		return r, err
	}
	owner, err := marketOwner(ownerText)
	if err != nil {
		return r, err
	}
	if limit < 1 || limit > 100 {
		return r, fmt.Errorf("invalid limit")
	}
	accounts, err := node.stablecoinFinalizedAccounts()
	if err != nil {
		return r, err
	}
	for _, k := range marketSortedKeys(accounts) {
		if k.String() <= cursor {
			continue
		}
		if err := ctx.Err(); err != nil {
			return r, err
		}
		v, e := marketAssetView(accounts, k, owner)
		if e != nil || (!owner.IsZero() && v.Balance == "0") {
			continue
		}
		if len(r.Assets) == limit {
			r.NextCursor = r.Assets[len(r.Assets)-1].Program
			break
		}
		r.Assets = append(r.Assets, v)
	}
	return r, nil
}
func (node *posNode) GetMarketOrders(ctx context.Context, ownerText, filter, cursor string, limit int) (rpc.MarketOrderList, error) {
	r := rpc.MarketOrderList{Orders: []rpc.MarketOrderResult{}, Commitment: "finalized"}
	if err := ctx.Err(); err != nil {
		return r, err
	}
	owner, err := marketOwner(ownerText)
	if err != nil {
		return r, err
	}
	if limit < 1 || limit > 100 || (filter != "open" && filter != "mine") || (filter == "mine" && owner.IsZero()) {
		return r, fmt.Errorf("invalid market filter or owner")
	}
	accounts, err := node.stablecoinFinalizedAccounts()
	if err != nil {
		return r, err
	}
	for _, k := range marketSortedKeys(accounts) {
		if k.String() <= cursor {
			continue
		}
		if err := ctx.Err(); err != nil {
			return r, err
		}
		v, e := marketOrderView(accounts, k, owner)
		if e != nil {
			continue
		}
		if filter == "open" && v.Status != "open" || filter == "mine" && v.Seller != owner.String() && v.Buyer != owner.String() {
			continue
		}
		if len(r.Orders) == limit {
			r.NextCursor = r.Orders[len(r.Orders)-1].OrderProgram
			break
		}
		r.Orders = append(r.Orders, v)
	}
	return r, nil
}
func (node *posNode) GetMarketOrder(ctx context.Context, programText, ownerText string) (rpc.MarketOrderResult, error) {
	if err := ctx.Err(); err != nil {
		return rpc.MarketOrderResult{}, err
	}
	program, err := decodePublicKey(programText, "order program")
	if err != nil {
		return rpc.MarketOrderResult{}, err
	}
	owner, err := marketOwner(ownerText)
	if err != nil {
		return rpc.MarketOrderResult{}, err
	}
	accounts, err := node.stablecoinFinalizedAccounts()
	if err != nil {
		return rpc.MarketOrderResult{}, err
	}
	return marketOrderView(accounts, program, owner)
}
