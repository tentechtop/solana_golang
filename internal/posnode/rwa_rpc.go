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

func (node *posNode) rwaFinalizedAccounts() (map[structure.PublicKey]structure.Account, error) {
	return node.stablecoinFinalizedAccounts()
}

func rwaView(accounts map[structure.PublicKey]structure.Account, mint, owner structure.PublicKey) (rpc.RWAStateResult, error) {
	account, ok := accounts[mint]
	if !ok || account.Executable {
		return rpc.RWAStateResult{}, fmt.Errorf("rwa: mint not found")
	}
	program, ok := accounts[account.Owner]
	if !ok || !program.Executable || program.Owner != structure.DefaultBuiltinProgramIDs.BPFLoader || !vmprogram.IsRWAProgram(program.Data) {
		return rpc.RWAStateResult{}, fmt.Errorf("rwa: unrecognized program")
	}
	state, err := vmprogram.UnmarshalRWAMint(account.Data)
	if err != nil {
		return rpc.RWAStateResult{}, err
	}
	if mint != vmprogram.RWAMintAddress(account.Owner, state.Creator, state.Symbol) {
		return rpc.RWAStateResult{}, fmt.Errorf("rwa: mint binding mismatch")
	}
	assetType := "share"
	if state.AssetType == vmprogram.RWAAssetNFT {
		assetType = "nft"
	}
	holdingKey := vmprogram.RWAHoldingAddress(account.Owner, mint, owner)
	result := rpc.RWAStateResult{
		Address: mint.String(), Program: account.Owner.String(), AssetType: assetType,
		Creator: state.Creator.String(), Authority: state.Authority.String(), Name: state.Name,
		Symbol: state.Symbol, Decimals: state.Decimals, Supply: strconv.FormatUint(state.Supply, 10),
		MaxSupply: strconv.FormatUint(state.MaxSupply, 10), Paused: state.Paused,
		DocumentHash: state.DocumentHash, ProposalEndSlot: state.ProposalEndSlot,
		DividendEndSlot: state.DividendEndSlot, HoldingAddress: holdingKey.String(), Balance: "0",
	}
	if !state.PendingAuthority.IsZero() {
		result.PendingAuthority = state.PendingAuthority.String()
	}
	if !state.ActiveProposal.IsZero() {
		result.ActiveProposal = state.ActiveProposal.String()
	}
	if !state.ActiveDividend.IsZero() {
		result.ActiveDividend = state.ActiveDividend.String()
	}
	if holdingAccount, exists := accounts[holdingKey]; exists {
		if holdingAccount.Owner == structure.DefaultBuiltinProgramIDs.System && !holdingAccount.Executable && len(holdingAccount.Data) == 0 {
			return result, nil
		}
		holding, err := vmprogram.UnmarshalRWAHolding(holdingAccount.Data)
		if err != nil || holdingAccount.Owner != account.Owner || holdingAccount.Executable || holding.Mint != mint || holding.Owner != owner {
			return result, fmt.Errorf("rwa: invalid holding account")
		}
		result.HoldingExists = true
		result.Balance = strconv.FormatUint(holding.Amount, 10)
		result.Approved = holding.Approved
		result.Frozen = holding.Frozen
		result.IdentityHash = holding.IdentityHash
	}
	return result, nil
}

func (node *posNode) GetRWAState(ctx context.Context, mintText, ownerText string) (rpc.RWAStateResult, error) {
	if err := ctx.Err(); err != nil {
		return rpc.RWAStateResult{}, err
	}
	mint, err := decodePublicKey(mintText, "RWA mint")
	if err != nil {
		return rpc.RWAStateResult{}, err
	}
	owner, err := decodePublicKey(ownerText, "RWA owner")
	if err != nil {
		return rpc.RWAStateResult{}, err
	}
	accounts, err := node.rwaFinalizedAccounts()
	if err != nil {
		return rpc.RWAStateResult{}, err
	}
	return rwaView(accounts, mint, owner)
}

func (node *posNode) GetRWAAssets(ctx context.Context, ownerText, cursor string, limit int) (rpc.RWAListResult, error) {
	result := rpc.RWAListResult{Assets: []rpc.RWAStateResult{}}
	owner, err := decodePublicKey(ownerText, "RWA owner")
	if err != nil {
		return result, err
	}
	if limit < 1 || limit > 100 {
		return result, fmt.Errorf("rwa: limit must be 1..100")
	}
	accounts, err := node.rwaFinalizedAccounts()
	if err != nil {
		return result, err
	}
	keys := []structure.PublicKey{}
	for key, account := range accounts {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if len(account.Data) > 0 && account.Data[0] == vmprogram.RWAMintKind && key.String() > cursor {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	for _, key := range keys {
		view, err := rwaView(accounts, key, owner)
		if err != nil {
			continue
		}
		if len(result.Assets) == limit {
			result.NextCursor = result.Assets[len(result.Assets)-1].Address
			break
		}
		result.Assets = append(result.Assets, view)
	}
	return result, nil
}

func (node *posNode) GetRWAProposal(ctx context.Context, addressText string) (rpc.RWAProposalResult, error) {
	if err := ctx.Err(); err != nil {
		return rpc.RWAProposalResult{}, err
	}
	address, err := decodePublicKey(addressText, "RWA proposal")
	if err != nil {
		return rpc.RWAProposalResult{}, err
	}
	accounts, err := node.rwaFinalizedAccounts()
	if err != nil {
		return rpc.RWAProposalResult{}, err
	}
	account, ok := accounts[address]
	if !ok || account.Executable {
		return rpc.RWAProposalResult{}, fmt.Errorf("rwa: proposal not found")
	}
	program, ok := accounts[account.Owner]
	if !ok || !program.Executable || program.Owner != structure.DefaultBuiltinProgramIDs.BPFLoader || !vmprogram.IsRWAProgram(program.Data) {
		return rpc.RWAProposalResult{}, fmt.Errorf("rwa: unrecognized proposal program")
	}
	proposal, err := vmprogram.UnmarshalRWAProposal(account.Data)
	if err != nil || address != vmprogram.RWAProposalAddress(account.Owner, proposal.Mint, proposal.DocumentHash) {
		return rpc.RWAProposalResult{}, fmt.Errorf("rwa: proposal binding mismatch")
	}
	return rpc.RWAProposalResult{
		Address: address.String(), Mint: proposal.Mint.String(), Creator: proposal.Creator.String(),
		CreatedSlot: proposal.CreatedSlot, EndSlot: proposal.EndSlot, SnapshotSupply: strconv.FormatUint(proposal.SnapshotSupply, 10),
		Yes: strconv.FormatUint(proposal.Yes, 10), No: strconv.FormatUint(proposal.No, 10), Abstain: strconv.FormatUint(proposal.Abstain, 10),
		QuorumPercent: proposal.QuorumPercent, Finalized: proposal.Finalized, Passed: proposal.Passed,
		Title: proposal.Title, DocumentHash: proposal.DocumentHash,
	}, nil
}

func (node *posNode) GetRWADividend(ctx context.Context, addressText, ownerText string) (rpc.RWADividendResult, error) {
	if err := ctx.Err(); err != nil {
		return rpc.RWADividendResult{}, err
	}
	address, err := decodePublicKey(addressText, "RWA dividend")
	if err != nil {
		return rpc.RWADividendResult{}, err
	}
	owner, err := decodePublicKey(ownerText, "RWA owner")
	if err != nil {
		return rpc.RWADividendResult{}, err
	}
	accounts, err := node.rwaFinalizedAccounts()
	if err != nil {
		return rpc.RWADividendResult{}, err
	}
	account, ok := accounts[address]
	if !ok || account.Executable {
		return rpc.RWADividendResult{}, fmt.Errorf("rwa: dividend not found")
	}
	program, ok := accounts[account.Owner]
	if !ok || !program.Executable || program.Owner != structure.DefaultBuiltinProgramIDs.BPFLoader || !vmprogram.IsRWAProgram(program.Data) {
		return rpc.RWADividendResult{}, fmt.Errorf("rwa: unrecognized dividend program")
	}
	dividend, err := vmprogram.UnmarshalRWADividend(account.Data)
	if err != nil || address != vmprogram.RWADividendAddress(account.Owner, dividend.Mint, dividend.DocumentHash) {
		return rpc.RWADividendResult{}, fmt.Errorf("rwa: dividend binding mismatch")
	}
	claimKey := vmprogram.RWADividendClaimAddress(account.Owner, address, owner)
	result := rpc.RWADividendResult{
		Address: address.String(), Mint: dividend.Mint.String(), Creator: dividend.Creator.String(),
		CreatedSlot: dividend.CreatedSlot, EndSlot: dividend.EndSlot, SnapshotSupply: strconv.FormatUint(dividend.SnapshotSupply, 10),
		Total: strconv.FormatUint(dividend.Total, 10), Claimed: strconv.FormatUint(dividend.Claimed, 10),
		Closed: dividend.Closed, DocumentHash: dividend.DocumentHash, ClaimAddress: claimKey.String(), ClaimAmount: "0",
	}
	if claimAccount, ok := accounts[claimKey]; ok && len(claimAccount.Data) > 0 {
		claim, err := vmprogram.UnmarshalRWADividendClaim(claimAccount.Data)
		if err != nil || claimAccount.Owner != account.Owner || claimAccount.Executable || claim.Dividend != address || claim.Owner != owner {
			return result, fmt.Errorf("rwa: invalid dividend claim")
		}
		result.HasClaim = true
		result.ClaimAmount = strconv.FormatUint(claim.Amount, 10)
	}
	return result, nil
}
