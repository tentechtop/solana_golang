package structure

import "errors"

var (
	ErrEmptyTransactionSignatures = errors.New("structure: transaction signatures cannot be empty")
	ErrInvalidMessageHeader       = errors.New("structure: invalid message header")
	ErrEmptyAccountKeys           = errors.New("structure: account keys cannot be empty")
	ErrEmptyRecentBlockhash       = errors.New("structure: recent blockhash cannot be empty")
	ErrEmptyInstructions          = errors.New("structure: instructions cannot be empty")
	ErrInvalidAccountMeta         = errors.New("structure: invalid account meta")
	ErrInvalidInstruction         = errors.New("structure: invalid instruction")
	ErrInvalidAddressTableLookup  = errors.New("structure: invalid address table lookup")
	ErrInvalidMessageVersion      = errors.New("structure: invalid message version")
	ErrMissingWritableSigner      = errors.New("structure: missing writable signer account")
	ErrMissingRequiredSignature   = errors.New("structure: missing required signature")
	ErrTooManyTransactions        = errors.New("structure: too many transactions")
	ErrInvalidBlockHeader         = errors.New("structure: invalid block header")
	ErrEmptyBlockhash             = errors.New("structure: blockhash cannot be empty")
)
