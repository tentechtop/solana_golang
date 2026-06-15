package vm

import "fmt"

// ComputeMeter 记录计算资源 + 防止程序无限执行或资源滥用。
type ComputeMeter struct {
	limit    uint64
	consumed uint64
}

// NewComputeMeter 创建计算计量器 + 零值限制自动使用默认上限。
func NewComputeMeter(limit uint64) ComputeMeter {
	if limit == 0 {
		limit = DefaultComputeUnitLimit
	}
	return ComputeMeter{limit: limit}
}

// Consume 扣减计算单元 + 超限时立即失败。
func (meter *ComputeMeter) Consume(units uint64) error {
	if meter == nil {
		return fmt.Errorf("%w: meter is nil", ErrExecutionFailed)
	}
	if units > meter.limit-meter.consumed {
		return fmt.Errorf("%w: consumed %d, add %d, limit %d", ErrComputeExceeded, meter.consumed, units, meter.limit)
	}
	meter.consumed += units
	return nil
}

// Consumed 返回已消耗计算单元 + 供执行结果和日志记录。
func (meter ComputeMeter) Consumed() uint64 {
	return meter.consumed
}
