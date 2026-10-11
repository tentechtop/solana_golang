package consensus

import (
	"math/big"
	"testing"

	"github.com/tentechtop/ordexus-core/programs/stake"
	"github.com/tentechtop/ordexus-core/structure"
)

func TestEpochRewardWeightExceedsUint64WithoutChangingRatio(t *testing.T) {
	state, snapshot, validators := newRewardTestState(t, []uint64{250_000_000_000, 1}, []uint16{0, 0})
	credits := []uint64{256, 257}
	index := make(map[structure.PublicKey]int)
	for i, account := range state.Accounts {
		index[account.Address] = i
	}
	expected := new(big.Int)
	for i, validator := range validators {
		stakeState := mustStakeState(t, state, validator.AccountAddress)
		stakeState.VoteCredits = credits[i]
		state = replaceStakeState(t, state, validator.AccountAddress, stakeState)
		expected.Add(expected, new(big.Int).Mul(new(big.Int).SetUint64(stakeState.ActiveStake), new(big.Int).SetUint64(credits[i])))
	}
	input := BlockRewardInput{Slot: 65, Height: 65, EpochID: 1, EpochSnapshot: snapshot, Leader: validators[0], Config: DefaultRewardConfig()}
	total, err := totalEpochRewardWeight(state, index, input)
	if err != nil || total.Cmp(expected) != 0 || total.IsUint64() {
		t.Fatalf("large reward weight = %v, expected %v, err=%v", total, expected, err)
	}
	const pool = uint64(10_000_000_000)
	var paid uint64
	for i, validator := range validators {
		stakeState := mustStakeState(t, state, validator.AccountAddress)
		reward, err := calculateVoteReward(stakeState, credits[i], input, epochRewardContext{PoolLamports: pool, TotalWeight: total})
		if err != nil {
			t.Fatal(err)
		}
		want := new(big.Int).Mul(new(big.Int).SetUint64(pool), new(big.Int).SetUint64(stakeState.ActiveStake))
		want.Mul(want, new(big.Int).SetUint64(credits[i])).Div(want, expected)
		if reward != want.Uint64() {
			t.Fatalf("reward = %d, expected %s", reward, want)
		}
		paid += reward
	}
	if paid > pool || total.Cmp(expected) != 0 {
		t.Fatal("reward calculation overpaid the pool or mutated total weight")
	}
	if _, _, err := ApplyBlockRewards(state, input); err != nil {
		t.Fatalf("large-stake epoch settlement failed: %v", err)
	}
}

func TestLargeStakeSlashAndCommissionUseExactBasisPointRatio(t *testing.T) {
	const amount = uint64(2_499_999_874_860_690_439)
	slashed, err := calculateSlashLamports(stake.ValidatorState{ActiveStake: amount}, 5000)
	if err != nil || slashed != amount/2 {
		t.Fatalf("large slash = %d, expected %d, err=%v", slashed, amount/2, err)
	}
	const max = ^uint64(0)
	if commission := prorateLamports(max, 10000, 10000); commission != max {
		t.Fatalf("full commission = %d, expected %d", commission, max)
	}
}
