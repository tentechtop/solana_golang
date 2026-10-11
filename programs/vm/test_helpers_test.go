package vmprogram

import (
	"testing"

	"github.com/tentechtop/ordexus-core/structure"
)

func testAccount(t *testing.T, lamports uint64, data []byte, owner structure.PublicKey, executable bool) structure.Account {
	t.Helper()
	account, err := structure.NewAccount(lamports, data, owner, executable, 0)
	if err != nil {
		t.Fatalf("NewAccount() error = %v", err)
	}
	return account
}

func testPublicKey(seed byte) structure.PublicKey {
	var publicKey structure.PublicKey
	for index := range publicKey {
		publicKey[index] = seed + byte(index)
	}
	return publicKey
}
