package rpc

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"

	vmprogram "github.com/tentechtop/ordexus-core/programs/vm"
)

const (
	MethodGetRWACapabilities = "getRwaCapabilities"
	MethodGetRWAAssets       = "getRwaAssets"
	MethodGetRWAState        = "getRwaState"
	MethodGetRWAProposal     = "getRwaProposal"
	MethodGetRWADividend     = "getRwaDividend"
)

type RWAStateResult struct {
	Address          string `json:"address"`
	Program          string `json:"program"`
	AssetType        string `json:"asset_type"`
	Creator          string `json:"creator"`
	Authority        string `json:"authority"`
	PendingAuthority string `json:"pending_authority"`
	Name             string `json:"name"`
	Symbol           string `json:"symbol"`
	Decimals         uint8  `json:"decimals"`
	Supply           string `json:"supply"`
	MaxSupply        string `json:"max_supply"`
	Paused           bool   `json:"paused"`
	DocumentHash     string `json:"document_hash"`
	ActiveProposal   string `json:"active_proposal"`
	ProposalEndSlot  uint64 `json:"proposal_end_slot"`
	ActiveDividend   string `json:"active_dividend"`
	DividendEndSlot  uint64 `json:"dividend_end_slot"`
	HoldingAddress   string `json:"holding_address"`
	HoldingExists    bool   `json:"holding_exists"`
	Balance          string `json:"balance"`
	Approved         bool   `json:"approved"`
	Frozen           bool   `json:"frozen"`
	IdentityHash     string `json:"identity_hash"`
}

type RWAListResult struct {
	Assets     []RWAStateResult `json:"assets"`
	NextCursor string           `json:"next_cursor"`
}

type RWAProposalResult struct {
	Address        string `json:"address"`
	Mint           string `json:"mint"`
	Creator        string `json:"creator"`
	CreatedSlot    uint64 `json:"created_slot"`
	EndSlot        uint64 `json:"end_slot"`
	SnapshotSupply string `json:"snapshot_supply"`
	Yes            string `json:"yes"`
	No             string `json:"no"`
	Abstain        string `json:"abstain"`
	QuorumPercent  uint8  `json:"quorum_percent"`
	Finalized      bool   `json:"finalized"`
	Passed         bool   `json:"passed"`
	Title          string `json:"title"`
	DocumentHash   string `json:"document_hash"`
}

type RWADividendResult struct {
	Address        string `json:"address"`
	Mint           string `json:"mint"`
	Creator        string `json:"creator"`
	CreatedSlot    uint64 `json:"created_slot"`
	EndSlot        uint64 `json:"end_slot"`
	SnapshotSupply string `json:"snapshot_supply"`
	Total          string `json:"total"`
	Claimed        string `json:"claimed"`
	Closed         bool   `json:"closed"`
	DocumentHash   string `json:"document_hash"`
	ClaimAddress   string `json:"claim_address"`
	HasClaim       bool   `json:"has_claim"`
	ClaimAmount    string `json:"claim_amount"`
}

type RWABackend interface {
	GetRWAAssets(context.Context, string, string, int) (RWAListResult, error)
	GetRWAState(context.Context, string, string) (RWAStateResult, error)
	GetRWAProposal(context.Context, string) (RWAProposalResult, error)
	GetRWADividend(context.Context, string, string) (RWADividendResult, error)
}

func registerRWAHandlers(router *Router, backend LedgerBackend) {
	_ = router.Register(MethodGetRWACapabilities, func(_ context.Context, params json.RawMessage) (any, *Error) {
		values, parseError := parseParamsArray(params)
		if parseError != nil {
			return nil, parseError
		}
		if len(values) != 0 {
			return nil, invalidParamsError("getRwaCapabilities takes no parameters")
		}
		if _, ok := backend.(RWABackend); !ok {
			return nil, ErrMethodUnavailable
		}
		data, err := vmprogram.RWAProgramData()
		if err != nil {
			return nil, internalError(err.Error())
		}
		hash := sha256.Sum256(data)
		return map[string]any{
			"version": 1, "template_base64": base64.StdEncoding.EncodeToString(data),
			"template_hash": hex.EncodeToString(hash[:]), "asset_types": []string{"share", "nft"},
			"share_decimals": 0, "nft_max_supply": 1, "commitment": "finalized",
		}, nil
	})
	_ = router.Register(MethodGetRWAAssets, func(ctx context.Context, params json.RawMessage) (any, *Error) {
		backend, ok := backend.(RWABackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		values, parseError := parseParamsArray(params)
		if parseError != nil {
			return nil, parseError
		}
		if len(values) != 3 {
			return nil, invalidParamsError("getRwaAssets requires owner, cursor and limit")
		}
		var owner, cursor string
		var limit int
		if json.Unmarshal(values[0], &owner) != nil || json.Unmarshal(values[1], &cursor) != nil || json.Unmarshal(values[2], &limit) != nil || len(owner) > 96 || len(cursor) > 96 || limit < 1 || limit > 100 {
			return nil, invalidParamsError("invalid RWA list parameters")
		}
		result, err := backend.GetRWAAssets(ctx, owner, cursor, limit)
		if err != nil {
			return nil, internalError(err.Error())
		}
		return result, nil
	})
	_ = router.Register(MethodGetRWAState, func(ctx context.Context, params json.RawMessage) (any, *Error) {
		backend, ok := backend.(RWABackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		values, parseError := parseParamsArray(params)
		if parseError != nil {
			return nil, parseError
		}
		if len(values) != 2 {
			return nil, invalidParamsError("getRwaState requires mint and owner")
		}
		var mint, owner string
		if json.Unmarshal(values[0], &mint) != nil || json.Unmarshal(values[1], &owner) != nil || len(mint) > 96 || len(owner) > 96 {
			return nil, invalidParamsError("invalid RWA state parameters")
		}
		result, err := backend.GetRWAState(ctx, mint, owner)
		if err != nil {
			return nil, internalError(err.Error())
		}
		return result, nil
	})
	_ = router.Register(MethodGetRWAProposal, func(ctx context.Context, params json.RawMessage) (any, *Error) {
		backend, ok := backend.(RWABackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		values, parseError := parseParamsArray(params)
		if parseError != nil {
			return nil, parseError
		}
		if len(values) != 1 {
			return nil, invalidParamsError("getRwaProposal requires an address")
		}
		var address string
		if json.Unmarshal(values[0], &address) != nil || len(address) > 96 {
			return nil, invalidParamsError("invalid RWA proposal address")
		}
		result, err := backend.GetRWAProposal(ctx, address)
		if err != nil {
			return nil, internalError(err.Error())
		}
		return result, nil
	})
	_ = router.Register(MethodGetRWADividend, func(ctx context.Context, params json.RawMessage) (any, *Error) {
		backend, ok := backend.(RWABackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		values, parseError := parseParamsArray(params)
		if parseError != nil {
			return nil, parseError
		}
		if len(values) != 2 {
			return nil, invalidParamsError("getRwaDividend requires address and owner")
		}
		var address, owner string
		if json.Unmarshal(values[0], &address) != nil || json.Unmarshal(values[1], &owner) != nil || len(address) > 96 || len(owner) > 96 {
			return nil, invalidParamsError("invalid RWA dividend parameters")
		}
		result, err := backend.GetRWADividend(ctx, address, owner)
		if err != nil {
			return nil, internalError(err.Error())
		}
		return result, nil
	})
}
