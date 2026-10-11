package runtime

import "context"

// GoOVM 表示 Ordexus 风格执行器 + 将未来 VM 和当前固定指令统一到 runtime 层。
type GoOVM struct {
	FixedExecutor FixedExecutor
}

// NewGoOVM 创建 GoOVM 执行器 + 显式传入程序避免 runtime 反向依赖业务包。
func NewGoOVM(programs ...Program) (GoOVM, error) {
	executor, err := NewFixedExecutor(programs...)
	if err != nil {
		return GoOVM{}, err
	}
	return GoOVM{FixedExecutor: executor}, nil
}

// NewGoOVMWithRegistry 创建 GoOVM 注册表执行器 + 支持程序处理器模型统一接入。
func NewGoOVMWithRegistry(registry ProgramRegistry) GoOVM {
	return GoOVM{FixedExecutor: NewFixedExecutorWithRegistry(registry)}
}

// ExecuteTransaction 执行交易 + 当前代理到固定指令执行器并保留未来 VM 替换点。
func (executor GoOVM) ExecuteTransaction(contextValue context.Context, request TransactionRequest) (TransactionResult, error) {
	if request.Mode == "" {
		request.Mode = ExecutionModeGoOVM
	}
	return executor.FixedExecutor.ExecuteTransaction(contextValue, request)
}
