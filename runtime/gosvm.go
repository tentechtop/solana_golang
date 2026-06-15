package runtime

import "context"

// GoSVM 表示 Solana 风格执行器 + 将未来 VM 和当前固定指令统一到 runtime 层。
type GoSVM struct {
	FixedExecutor FixedExecutor
}

// NewGoSVM 创建 GoSVM 执行器 + 显式传入程序避免 runtime 反向依赖业务包。
func NewGoSVM(programs ...Program) (GoSVM, error) {
	executor, err := NewFixedExecutor(programs...)
	if err != nil {
		return GoSVM{}, err
	}
	return GoSVM{FixedExecutor: executor}, nil
}

// ExecuteTransaction 执行交易 + 当前代理到固定指令执行器并保留未来 VM 替换点。
func (executor GoSVM) ExecuteTransaction(contextValue context.Context, request TransactionRequest) (TransactionResult, error) {
	if request.Mode == "" {
		request.Mode = ExecutionModeGoSVM
	}
	return executor.FixedExecutor.ExecuteTransaction(contextValue, request)
}
