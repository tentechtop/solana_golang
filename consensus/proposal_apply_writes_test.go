package consensus

import (
	"testing"

	"solana_golang/structure"
)

func TestChainStateApplyWritesSkipsMissingBuiltinProgramPlaceholder(t *testing.T) {
	state := ChainState{}
	nextState := state.applyWrites([]structure.AddressedAccount{
		{
			Address: structure.DefaultBuiltinProgramIDs.Privacy,
			Account: structure.Account{Owner: structure.DefaultBuiltinProgramIDs.NativeLoader},
		},
	})

	if len(nextState.Accounts) != 0 {
		t.Fatalf("accounts = %d, want no persisted builtin placeholder", len(nextState.Accounts))
	}
}
