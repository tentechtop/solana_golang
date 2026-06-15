package structure

// GoSVM 表示 Solana 风格执行器 + 统一 native program 和 VM program 的交易入口。
type GoSVM struct {
	Simulator TransactionSimulator
}

// ExecuteTransaction 执行交易 + 保持 Solana 交易格式和账户模型不变。
func (runtime GoSVM) ExecuteTransaction(input TransactionSimulationInput) (TransactionExecutionResult, error) {
	simulator := runtime.Simulator
	return simulator.Simulate(input)
}
