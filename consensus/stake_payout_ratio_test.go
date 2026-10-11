package consensus

import (
	"math/big"
	"testing"

	"github.com/tentechtop/ordexus-core/programs/stake"
	"github.com/tentechtop/ordexus-core/structure"
)

func TestEpochPayoutPreservesLargeDelegatorShareAndBalances(t *testing.T) {
	const delegated = uint64(2_499_999_874_860_690_439)
	for _, selfStake := range []uint64{0, 100_000_000} {
		for _, commission := range []uint16{0, 1000} {
			state, snapshot, validators := newRewardTestState(t, []uint64{10}, []uint16{commission})
			validator := validators[0]
			delegator := mustKeyPair(t, "large-payout-delegator")
			stakeState := mustStakeState(t, state, validator.AccountAddress)
			stakeState.ActiveStake = selfStake + delegated
			stakeState.LastEffectiveStake = stakeState.ActiveStake
			stakeState.VoteCredits = 256
			stakeState.Delegations = []stake.DelegationState{{DelegatorAccount: delegator.PublicKey, ActiveStake: delegated}}
			state = replaceStakeState(t, state, validator.AccountAddress, stakeState)
			state.Accounts = append(state.Accounts, newTestAccount(t, delegator.PublicKey, 1_000_000_000, structure.DefaultBuiltinProgramIDs.System, false, nil))
			index := accountIndexByAddress(state)
			state.Accounts[index[validator.AccountAddress]].Account.Lamports += delegated
			input := BlockRewardInput{Slot: 65, Height: 65, EpochID: 1, EpochSnapshot: snapshot, Leader: validator, Config: DefaultRewardConfig()}
			context, err := buildEpochRewardContext(state, index, input)
			if err != nil {
				t.Fatal(err)
			}
			pool := context.PoolLamports
			fee := new(big.Int).Mul(new(big.Int).SetUint64(pool), new(big.Int).SetUint64(uint64(commission)))
			fee.Div(fee, big.NewInt(10000))
			payout := pool - fee.Uint64()
			wantDelegation := new(big.Int).Mul(new(big.Int).SetUint64(payout), new(big.Int).SetUint64(delegated))
			wantDelegation.Div(wantDelegation, new(big.Int).SetUint64(selfStake+delegated))
			if selfStake == 0 {
				wantDelegation.SetUint64(payout)
			}
			beforeDelegator := state.Accounts[index[delegator.PublicKey]].Account.Lamports
			beforeStaker := state.Accounts[index[stakeState.StakerAccount]].Account.Lamports
			beforeSupply, _ := totalStateLamports(state)
			next, rewards, err := ApplyBlockRewards(state, input)
			if err != nil {
				t.Fatal(err)
			}
			settled := mustStakeState(t, next, validator.AccountAddress)
			if settled.Delegations[0].RewardLamports != wantDelegation.Uint64() {
				t.Fatalf("self=%d commission=%d: delegation reward %d, want %d", selfStake, commission, settled.Delegations[0].RewardLamports, wantDelegation.Uint64())
			}
			if got := next.Accounts[index[delegator.PublicKey]].Account.Lamports - beforeDelegator; got != wantDelegation.Uint64() {
				t.Fatalf("delegator balance credited %d, want %d", got, wantDelegation.Uint64())
			}
			if got := next.Accounts[index[stakeState.StakerAccount]].Account.Lamports - beforeStaker; got != payout-wantDelegation.Uint64() {
				t.Fatalf("self balance credited %d, want %d", got, payout-wantDelegation.Uint64())
			}
			if settled.SelfRewardLamports+settled.Delegations[0].RewardLamports+settled.CommissionRewardLamports != pool {
				t.Fatal("reward breakdown does not conserve pool")
			}
			afterSupply, err := totalStateLamports(next)
			if err != nil || afterSupply-beforeSupply != pool {
				t.Fatalf("supply increment does not equal pool: %v", err)
			}
			var paid uint64
			for _, reward := range rewards {
				paid += reward.Lamports
			}
			if paid != pool {
				t.Fatalf("reward events total %d, want %d", paid, pool)
			}
			again, _, err := ApplyBlockRewards(next, input)
			if err != nil {
				t.Fatal(err)
			}
			againSupply, _ := totalStateLamports(again)
			if againSupply != afterSupply {
				t.Fatal("epoch paid twice")
			}
			if mustStakeState(t, state, validator.AccountAddress).Delegations[0].RewardLamports != 0 {
				t.Fatal("input state mutated")
			}
		}
	}
}

func TestStakePayoutActivationRetainsHistoricalArithmetic(t *testing.T) {
	const pool = uint64(1_623_540_000_000)
	const delegated = uint64(2_499_999_874_860_690_439)
	const total = delegated + 100_000_000
	input := BlockRewardInput{EpochID: 31, Config: RewardConfig{ExactStakePayoutActivationEpoch: 32}}
	legacyProduct := pool
	legacyProduct *= delegated
	if got := stakePayoutShare(pool, delegated, total, input); got != legacyProduct/total {
		t.Fatal("historical payout changed")
	}
	input.EpochID = 32
	want := new(big.Int).Mul(new(big.Int).SetUint64(pool), new(big.Int).SetUint64(delegated))
	want.Div(want, new(big.Int).SetUint64(total))
	if got := stakePayoutShare(pool, delegated, total, input); got != want.Uint64() || got < pool-100 {
		t.Fatalf("activation payout %d, want %s", got, want)
	}
	if got := stakePayoutShare(^uint64(0), total, total, input); got != ^uint64(0) {
		t.Fatal("full share loses units at uint64 boundary")
	}
}
