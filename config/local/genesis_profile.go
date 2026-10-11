package local

import _ "embed"

// GenesisAccountJSON is the single public genesis profile used by node apps.
//
//go:embed genesis-account.json
var GenesisAccountJSON []byte
