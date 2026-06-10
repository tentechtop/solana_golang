package consensus

import "errors"

var (
	ErrInvalidClockConfig = errors.New("consensus: invalid clock config")
	ErrInvalidQuorum      = errors.New("consensus: invalid quorum")
	ErrInvalidVote        = errors.New("consensus: invalid vote")
	ErrInvalidCertificate = errors.New("consensus: invalid certificate")
	ErrDuplicateVote      = errors.New("consensus: duplicate vote")
	ErrConflictingVote    = errors.New("consensus: conflicting vote")
	ErrUnknownValidator   = errors.New("consensus: unknown validator")
)
