package rpc

import "fmt"

const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603

	CodeMethodUnavailable = -32001
)

var (
	ErrParseError          = newError(CodeParseError, "parse error")
	ErrInvalidRequest      = newError(CodeInvalidRequest, "invalid request")
	ErrMethodNotFound      = newError(CodeMethodNotFound, "method not found")
	ErrInvalidParams       = newError(CodeInvalidParams, "invalid params")
	ErrInternalError       = newError(CodeInternalError, "internal error")
	ErrMethodUnavailable   = newError(CodeMethodUnavailable, "method unavailable")
	ErrBatchSizeExceeded   = newError(CodeInvalidRequest, "batch size exceeded")
	ErrRequestBodyTooLarge = newError(CodeInvalidRequest, "request body too large")
)

func newError(code int, message string) *Error {
	return &Error{Code: code, Message: message}
}

func errorWithData(base *Error, data any) *Error {
	if base == nil {
		return ErrInternalError
	}
	return &Error{Code: base.Code, Message: base.Message, Data: data}
}

func methodNotFoundError(method string) *Error {
	return errorWithData(ErrMethodNotFound, fmt.Sprintf("method %q is not supported", method))
}

func invalidParamsError(message string) *Error {
	return errorWithData(ErrInvalidParams, message)
}

func internalError(message string) *Error {
	return errorWithData(ErrInternalError, message)
}
