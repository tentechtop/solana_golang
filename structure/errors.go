package structure

import "errors"

var (
	ErrEmptyTransactionSignatures = errors.New("structure: transaction signatures cannot be empty")
	ErrInvalidMessageHeader       = errors.New("structure: invalid message header")
	ErrEmptyAccountKeys           = errors.New("structure: account keys cannot be empty")
	ErrEmptyRecentBlockhash       = errors.New("structure: recent blockhash cannot be empty")
	ErrInvalidInstruction         = errors.New("structure: invalid instruction")
	ErrTooManyTransactions        = errors.New("structure: too many transactions")
	ErrInvalidBlockHeader         = errors.New("structure: invalid block header")
	ErrEmptyBlockhash             = errors.New("structure: blockhash cannot be empty")
)
