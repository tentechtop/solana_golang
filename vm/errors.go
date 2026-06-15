package vm

import "errors"

var (
	ErrInvalidProgram   = errors.New("vm: invalid program")
	ErrInvalidAccount   = errors.New("vm: invalid account")
	ErrPermissionDenied = errors.New("vm: permission denied")
	ErrComputeExceeded  = errors.New("vm: compute budget exceeded")
	ErrExecutionFailed  = errors.New("vm: execution failed")
)
