package structure

import (
	"fmt"
	"math/big"
)

const (
	TokenLamports                   = uint64(1_000_000_000)
	DefaultGenesisTokenSupply       = uint64(100_000_000)
	DefaultGenesisSupplyLamports    = DefaultGenesisTokenSupply * TokenLamports
	DefaultInitialInflationBps      = uint16(500)
	DefaultTerminalInflationBps     = uint16(150)
	DefaultInflationDecayBpsPerYear = uint16(1500)
	DefaultSlotsPerYear             = uint64(78_840_000)
	DefaultSlotsPerEpoch            = uint64(432_000)
	basisPointsDenominator          = uint64(10_000)
)

// MonetaryPolicy 描述货币发行规则 + 所有节点用同一参数计算供应和通胀奖励。
type MonetaryPolicy struct {
	GenesisSupplyLamports    uint64
	InitialInflationBps      uint16
	TerminalInflationBps     uint16
	InflationDecayBpsPerYear uint16
	SlotsPerYear             uint64
	SlotsPerEpoch            uint64
}

// DefaultMonetaryPolicy 返回默认货币政策 + 用明确发行曲线替代散落常量。
func DefaultMonetaryPolicy() MonetaryPolicy {
	return MonetaryPolicy{
		GenesisSupplyLamports:    DefaultGenesisSupplyLamports,
		InitialInflationBps:      DefaultInitialInflationBps,
		TerminalInflationBps:     DefaultTerminalInflationBps,
		InflationDecayBpsPerYear: DefaultInflationDecayBpsPerYear,
		SlotsPerYear:             DefaultSlotsPerYear,
		SlotsPerEpoch:            DefaultSlotsPerEpoch,
	}
}

// Normalize 补齐货币政策默认值 + 配置只需覆盖业务关注字段。
func (policy MonetaryPolicy) Normalize() MonetaryPolicy {
	if policy == (MonetaryPolicy{}) {
		return DefaultMonetaryPolicy()
	}
	defaultPolicy := DefaultMonetaryPolicy()
	if policy.GenesisSupplyLamports == 0 {
		policy.GenesisSupplyLamports = defaultPolicy.GenesisSupplyLamports
	}
	if policy.InitialInflationBps == 0 {
		policy.InitialInflationBps = defaultPolicy.InitialInflationBps
	}
	if policy.TerminalInflationBps == 0 {
		policy.TerminalInflationBps = defaultPolicy.TerminalInflationBps
	}
	if policy.InflationDecayBpsPerYear == 0 {
		policy.InflationDecayBpsPerYear = defaultPolicy.InflationDecayBpsPerYear
	}
	if policy.SlotsPerYear == 0 {
		policy.SlotsPerYear = defaultPolicy.SlotsPerYear
	}
	if policy.SlotsPerEpoch == 0 {
		policy.SlotsPerEpoch = defaultPolicy.SlotsPerEpoch
	}
	return policy
}

// Validate 校验货币政策 + 防止非法通胀参数进入共识计算。
func (policy MonetaryPolicy) Validate() error {
	policy = policy.Normalize()
	if policy.GenesisSupplyLamports == 0 {
		return fmt.Errorf("%w: genesis supply cannot be zero", ErrInvalidFee)
	}
	if uint64(policy.InitialInflationBps) > basisPointsDenominator {
		return fmt.Errorf("%w: initial inflation bps exceeds 10000", ErrInvalidFee)
	}
	if policy.TerminalInflationBps > policy.InitialInflationBps {
		return fmt.Errorf("%w: terminal inflation exceeds initial inflation", ErrInvalidFee)
	}
	if uint64(policy.InflationDecayBpsPerYear) > basisPointsDenominator {
		return fmt.Errorf("%w: inflation decay bps exceeds 10000", ErrInvalidFee)
	}
	if policy.SlotsPerYear == 0 || policy.SlotsPerEpoch == 0 {
		return fmt.Errorf("%w: slots per year and epoch must be positive", ErrInvalidFee)
	}
	return nil
}

// InflationBpsForEpoch 计算指定 epoch 年化通胀率 + 使用衰减曲线并受最低通胀保护。
func (policy MonetaryPolicy) InflationBpsForEpoch(epochID uint64) (uint16, error) {
	policy = policy.Normalize()
	if err := policy.Validate(); err != nil {
		return 0, err
	}
	elapsedSlots, err := safeMulUint64(epochID, policy.SlotsPerEpoch)
	if err != nil {
		return 0, fmt.Errorf("structure: calculate inflation year: %w", err)
	}
	yearIndex := elapsedSlots / policy.SlotsPerYear
	inflation := uint64(policy.InitialInflationBps)
	for year := uint64(0); year < yearIndex && inflation > uint64(policy.TerminalInflationBps); year++ {
		inflation = inflation * uint64(basisPointsDenominator-uint64(policy.InflationDecayBpsPerYear)) / basisPointsDenominator
		if inflation < uint64(policy.TerminalInflationBps) {
			inflation = uint64(policy.TerminalInflationBps)
		}
	}
	return uint16(inflation), nil
}

// InflationLamportsForEpoch 计算 epoch 通胀池 + 奖励按当前供应和 epoch slot 数线性释放。
func (policy MonetaryPolicy) InflationLamportsForEpoch(currentSupplyLamports uint64, epochID uint64, epochSlots uint64) (uint64, error) {
	policy = policy.Normalize()
	if err := policy.Validate(); err != nil {
		return 0, err
	}
	if currentSupplyLamports == 0 || epochSlots == 0 {
		return 0, nil
	}
	inflationBps, err := policy.InflationBpsForEpoch(epochID)
	if err != nil {
		return 0, err
	}
	rewardNumerator := new(big.Int).SetUint64(currentSupplyLamports)
	rewardNumerator.Mul(rewardNumerator, new(big.Int).SetUint64(uint64(inflationBps)))
	rewardNumerator.Mul(rewardNumerator, new(big.Int).SetUint64(epochSlots))

	rewardDenominator := new(big.Int).SetUint64(basisPointsDenominator)
	rewardDenominator.Mul(rewardDenominator, new(big.Int).SetUint64(policy.SlotsPerYear))
	rewardNumerator.Div(rewardNumerator, rewardDenominator)
	if !rewardNumerator.IsUint64() {
		return 0, fmt.Errorf("%w: epoch inflation exceeds uint64", ErrInvalidFee)
	}
	return rewardNumerator.Uint64(), nil
}
