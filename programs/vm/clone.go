package vmprogram

import "solana_golang/structure"

func cloneStructureAccounts(accounts map[structure.PublicKey]structure.Account) map[structure.PublicKey]structure.Account {
	clonedAccounts := make(map[structure.PublicKey]structure.Account, len(accounts))
	for address, account := range accounts {
		clonedAccounts[address] = account.Clone()
	}
	return clonedAccounts
}

func clonePublicKeys(publicKeys []structure.PublicKey) []structure.PublicKey {
	if publicKeys == nil {
		return nil
	}
	clonedKeys := make([]structure.PublicKey, len(publicKeys))
	copy(clonedKeys, publicKeys)
	return clonedKeys
}
