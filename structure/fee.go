package structure

import "fmt"

const (
	LamportsPerSignature                     = uint64(5000)
	DefaultInstructionComputeUnits           = uint64(200000)
	MaxComputeUnitsPerTransaction            = uint64(1400000)
	DefaultLoadedAccountsDataSize            = uint64(64 * 1024 * 1024)
	MicroLamportsPerLamport                  = uint64(1000000)
	DefaultBaseFeeBurnPercent                = uint8(50)
	DefaultBuiltinInstructionCU              = uint64(3000)
	DefaultTransferComputeUnits              = uint64(3000)
	DefaultContractCallComputeUnits          = uint64(200000)
	DefaultContractDeployBaseCU              = uint64(200000)
	DefaultContractDeployByteCU              = uint64(10)
	DefaultBaseComputeUnitPriceMicroLamports = uint64(100)
	DefaultStorageWriteLamportsPerByte       = uint64(10)
)

// ComputeBudgetLimits 描述交易计算预算 + 限制执行成本和加载账户数据量。
type ComputeBudgetLimits struct {
	MaxComputeUnits               uint64
	ComputeUnitPriceMicroLamports uint64
	LoadedAccountsDataSizeLimit   uint64
	StorageWriteBytesLimit        uint64
	HeapFrameBytes                uint32
}

// PrioritizationFee 描述优先费输入 + 使用计算单元上限和单价计算额外费用。
type PrioritizationFee struct {
	ComputeUnitLimit              uint64
	ComputeUnitPriceMicroLamports uint64
}

// FeeDetails 描述费用明细 + 区分基础费、优先费、燃烧和验证者收入。
type FeeDetails struct {
	SignatureCount    uint64
	SignatureFee      uint64
	ComputeFee        uint64
	StorageWriteFee   uint64
	BaseFee           uint64
	PrioritizationFee uint64
	TotalFee          uint64
	BurnedFee         uint64
	ValidatorFee      uint64
}

// FeeCalculator 描述费用参数 + 让测试网和私有网络可配置基础费策略。
type FeeCalculator struct {
	LamportsPerSignature              uint64
	BaseComputeUnitPriceMicroLamports uint64
	StorageWriteLamportsPerByte       uint64
	BaseFeeBurnPercent                uint8
}

// DefaultComputeBudgetLimits 返回默认计算预算 + 为交易执行提供保守上限。
func DefaultComputeBudgetLimits() ComputeBudgetLimits {
	return ComputeBudgetLimits{
		MaxComputeUnits:             MaxComputeUnitsPerTransaction,
		LoadedAccountsDataSizeLimit: DefaultLoadedAccountsDataSize,
	}
}

// DefaultFeeCalculator 返回默认费用计算器 + 基础费按比例燃烧后把剩余费用和优先费分配给 leader。
func DefaultFeeCalculator() FeeCalculator {
	return FeeCalculator{
		LamportsPerSignature:              LamportsPerSignature,
		BaseComputeUnitPriceMicroLamports: DefaultBaseComputeUnitPriceMicroLamports,
		StorageWriteLamportsPerByte:       DefaultStorageWriteLamportsPerByte,
		BaseFeeBurnPercent:                DefaultBaseFeeBurnPercent,
	}
}

// Validate 校验计算预算 + 防止零预算和超上限预算进入调度器。
func (limits ComputeBudgetLimits) Validate() error {
	if limits.MaxComputeUnits == 0 {
		return fmt.Errorf("%w: max compute units cannot be zero", ErrInvalidFee)
	}
	if limits.MaxComputeUnits > MaxComputeUnitsPerTransaction {
		return fmt.Errorf("%w: max compute units %d exceeds %d", ErrInvalidFee, limits.MaxComputeUnits, MaxComputeUnitsPerTransaction)
	}
	if limits.LoadedAccountsDataSizeLimit == 0 {
		return fmt.Errorf("%w: loaded account data size limit cannot be zero", ErrInvalidFee)
	}
	return nil
}

// Fee 计算优先费 + 使用向上取整避免微单位费用被截断为零。
func (fee PrioritizationFee) Fee() (uint64, error) {
	product, err := safeMulUint64(fee.ComputeUnitLimit, fee.ComputeUnitPriceMicroLamports)
	if err != nil {
		return 0, fmt.Errorf("structure: calculate priority fee: %w", err)
	}
	if product == 0 {
		return 0, nil
	}
	return ((product - 1) / MicroLamportsPerLamport) + 1, nil
}

// Validate 校验费用计算器 + 防止无效基础费参数进入执行流程。
func (calculator FeeCalculator) Validate() error {
	if calculator.LamportsPerSignature == 0 {
		return fmt.Errorf("%w: lamports per signature cannot be zero", ErrInvalidFee)
	}
	if calculator.BaseFeeBurnPercent > 100 {
		return fmt.Errorf("%w: burn percent %d exceeds 100", ErrInvalidFee, calculator.BaseFeeBurnPercent)
	}
	return nil
}

// Calculate 计算交易费用 + 结合签名数和计算预算得到完整费用明细。
func (calculator FeeCalculator) Calculate(signatureCount int, limits ComputeBudgetLimits) (FeeDetails, error) {
	if err := calculator.Validate(); err != nil {
		return FeeDetails{}, err
	}
	if err := limits.Validate(); err != nil {
		return FeeDetails{}, err
	}
	if signatureCount < 0 {
		return FeeDetails{}, fmt.Errorf("%w: negative signature count", ErrInvalidFee)
	}

	signatureFee, err := safeMulUint64(uint64(signatureCount), calculator.LamportsPerSignature)
	if err != nil {
		return FeeDetails{}, fmt.Errorf("structure: calculate signature fee: %w", err)
	}
	computeFee, err := unitPriceFeeFloor(limits.MaxComputeUnits, calculator.BaseComputeUnitPriceMicroLamports, "compute")
	if err != nil {
		return FeeDetails{}, err
	}
	storageWriteFee, err := safeMulUint64(limits.StorageWriteBytesLimit, calculator.StorageWriteLamportsPerByte)
	if err != nil {
		return FeeDetails{}, fmt.Errorf("structure: calculate storage write fee: %w", err)
	}
	baseFee, err := safeAddManyUint64(signatureFee, computeFee, storageWriteFee)
	if err != nil {
		return FeeDetails{}, fmt.Errorf("structure: calculate base fee: %w", err)
	}
	priorityFee, err := PrioritizationFee{
		ComputeUnitLimit:              limits.MaxComputeUnits,
		ComputeUnitPriceMicroLamports: limits.ComputeUnitPriceMicroLamports,
	}.Fee()
	if err != nil {
		return FeeDetails{}, err
	}
	totalFee, err := safeAddUint64(baseFee, priorityFee)
	if err != nil {
		return FeeDetails{}, fmt.Errorf("structure: calculate total fee: %w", err)
	}

	burnedFeeNumerator, err := safeMulUint64(baseFee, uint64(calculator.BaseFeeBurnPercent))
	if err != nil {
		return FeeDetails{}, fmt.Errorf("structure: calculate burned fee: %w", err)
	}
	burnedFee := burnedFeeNumerator / 100
	validatorBaseFee := baseFee - burnedFee
	validatorFee, err := safeAddUint64(validatorBaseFee, priorityFee)
	if err != nil {
		return FeeDetails{}, fmt.Errorf("structure: calculate validator fee: %w", err)
	}
	return FeeDetails{
		SignatureCount:    uint64(signatureCount),
		SignatureFee:      signatureFee,
		ComputeFee:        computeFee,
		StorageWriteFee:   storageWriteFee,
		BaseFee:           baseFee,
		PrioritizationFee: priorityFee,
		TotalFee:          totalFee,
		BurnedFee:         burnedFee,
		ValidatorFee:      validatorFee,
	}, nil
}

func unitPriceFeeFloor(units uint64, microLamportsPerUnit uint64, label string) (uint64, error) {
	product, err := safeMulUint64(units, microLamportsPerUnit)
	if err != nil {
		return 0, fmt.Errorf("structure: calculate %s fee: %w", label, err)
	}
	return product / MicroLamportsPerLamport, nil
}

func safeAddManyUint64(values ...uint64) (uint64, error) {
	total := uint64(0)
	for _, value := range values {
		nextTotal, err := safeAddUint64(total, value)
		if err != nil {
			return 0, err
		}
		total = nextTotal
	}
	return total, nil
}
