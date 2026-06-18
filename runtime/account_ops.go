package runtime

import (
	"fmt"

	"solana_golang/structure"
)

// IsSignerAddress 判断地址是否为交易签名者 + 程序执行统一使用同一签名语义。
func IsSignerAddress(address structure.PublicKey, message structure.ResolvedMessage) bool {
	requiredSignatures := int(message.Header.NumRequiredSignatures)
	for accountIndex := 0; accountIndex < requiredSignatures && accountIndex < len(message.StaticAccountKeys); accountIndex++ {
		if message.StaticAccountKeys[accountIndex] == address {
			return true
		}
	}
	return false
}

// IsSignerContextAddress 判断上下文签名者 + 支持受控 CPI 注入池账户签名。
func IsSignerContextAddress(address structure.PublicKey, context InstructionContext) bool {
	if IsSignerAddress(address, context.Message) {
		return true
	}
	if len(context.SignerOverrides) == 0 {
		return false
	}
	_, exists := context.SignerOverrides[address]
	return exists
}

// IsWritableMessageAccount 判断消息账户是否可写 + VM 写回和固定程序共享同一权限判断。
func IsWritableMessageAccount(accountIndex int, message structure.ResolvedMessage) bool {
	staticMetas := message.StaticAccountMetas()
	if accountIndex < len(staticMetas) {
		return staticMetas[accountIndex].IsWritable
	}
	loadedIndex := accountIndex - len(message.StaticAccountKeys)
	return loadedIndex >= 0 && loadedIndex < len(message.LoadedAddresses.Writable)
}

// TransferLamports 转移账户余额 + 提供给固定程序复用并集中做租金校验。
func TransferLamports(
	sourceAddress structure.PublicKey,
	destinationAddress structure.PublicKey,
	lamports uint64,
	accounts map[structure.PublicKey]structure.Account,
	rentConfig structure.RentConfig,
) error {
	sourceAccount, exists := accounts[sourceAddress]
	if !exists {
		return fmt.Errorf("%w: source account not found", structure.ErrInvalidLoadedTransaction)
	}
	destinationAccount, exists := accounts[destinationAddress]
	if !exists {
		var err error
		destinationAccount, err = structure.NewAccount(0, nil, structure.DefaultBuiltinProgramIDs.System, false, 0)
		if err != nil {
			return fmt.Errorf("runtime: create destination account: %w", err)
		}
	}
	if err := sourceAccount.DebitLamports(lamports, rentConfig); err != nil {
		return err
	}
	if err := destinationAccount.CreditLamports(lamports); err != nil {
		return err
	}
	if err := destinationAccount.ValidateWithRent(rentConfig); err != nil {
		return err
	}
	accounts[sourceAddress] = sourceAccount
	accounts[destinationAddress] = destinationAccount
	return nil
}
