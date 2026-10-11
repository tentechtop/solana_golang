package posnode

import (
	"context"
	"fmt"
	vmprogram "github.com/tentechtop/ordexus-core/programs/vm"
	"github.com/tentechtop/ordexus-core/structure"
	"strconv"
)

func rewardView(accounts map[structure.PublicKey]structure.Account, program, owner structure.PublicKey) (map[string]any, error) {
	p, ok := accounts[program]
	if !ok || !p.Executable || p.Owner != structure.DefaultBuiltinProgramIDs.BPFLoader || !vmprogram.IsRewardProgram(p.Data) {
		return nil, fmt.Errorf("reward: unrecognized program")
	}
	configKey, mintKey := vmprogram.RewardConfigAddress(program), vmprogram.RewardMintAddress(program)
	ca, ma := accounts[configKey], accounts[mintKey]
	if ca.Owner != program || ma.Owner != program || ca.Executable || ma.Executable {
		return nil, fmt.Errorf("reward: invalid account ownership")
	}
	s, err := vmprogram.UnmarshalRewardConfig(ca.Data)
	if err != nil {
		return nil, err
	}
	m, err := vmprogram.UnmarshalAssetMintStateBinary(ma.Data)
	if err != nil {
		return nil, err
	}
	if s.Mint != mintKey || m.Authority != configKey || m.MaxSupply != s.Cap || m.Decimals != 6 || m.Kind != vmprogram.AssetKindFungible {
		return nil, fmt.Errorf("reward: invalid mint binding")
	}
	balanceKey, receiptKey := vmprogram.RewardBalanceAddress(program, owner), vmprogram.RewardReceiptAddress(program, owner)
	v := map[string]any{"commitment": "finalized", "program": program.String(), "config_address": configKey.String(), "mint_address": mintKey.String(), "issuer": s.Issuer.String(), "oracle": s.Oracle.String(), "cap": strconv.FormatUint(s.Cap, 10), "total_claimed": strconv.FormatUint(s.TotalClaimed, 10), "signin_reward": strconv.FormatUint(s.SignInReward, 10), "video_reward": strconv.FormatUint(s.VideoReward, 10), "name": m.Name, "symbol": m.Symbol, "decimals": m.Decimals, "supply": strconv.FormatUint(m.Supply, 10), "balance": "0", "balance_address": balanceKey.String(), "receipt_address": receiptKey.String(), "balance_exists": false, "receipt_exists": false, "claim_day": "0", "claim_flags": uint8(0)}
	if a, ok := accounts[balanceKey]; ok && !(a.Owner == structure.DefaultBuiltinProgramIDs.System && !a.Executable && len(a.Data) == 0) {
		b, err := vmprogram.UnmarshalAssetBalanceStateBinary(a.Data)
		if err != nil || a.Owner != program || a.Executable || b.Mint != mintKey || b.Owner != owner {
			return nil, fmt.Errorf("reward: invalid balance account")
		}
		v["balance"], v["balance_exists"] = strconv.FormatUint(b.Amount, 10), true
	}
	if a, ok := accounts[receiptKey]; ok && !(a.Owner == structure.DefaultBuiltinProgramIDs.System && !a.Executable && len(a.Data) == 0) {
		r, err := vmprogram.UnmarshalRewardReceipt(a.Data)
		if err != nil || a.Owner != program || a.Executable || r.Config != configKey || r.Owner != owner {
			return nil, fmt.Errorf("reward: invalid receipt account")
		}
		v["claim_day"], v["claim_flags"], v["receipt_exists"] = strconv.FormatUint(r.Day, 10), r.Flags, true
	}
	return v, nil
}
func (node *posNode) GetRewardState(ctx context.Context, programText, ownerText string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	program, err := decodePublicKey(programText, "reward program")
	if err != nil {
		return nil, err
	}
	owner, err := decodePublicKey(ownerText, "reward owner")
	if err != nil {
		return nil, err
	}
	accounts, err := node.stablecoinFinalizedAccounts()
	if err != nil {
		return nil, err
	}
	return rewardView(accounts, program, owner)
}
