package blockchain

import "errors"

var (
	ErrInvalidGenesis = errors.New("blockchain: invalid genesis")
	ErrInvalidCommit  = errors.New("blockchain: invalid commit")
	ErrLedgerClosed   = errors.New("blockchain: ledger closed")
)
