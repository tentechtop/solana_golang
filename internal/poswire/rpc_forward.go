package poswire

import (
	"encoding/json"
	"fmt"
	"strings"

	"solana_golang/codec/borsh"
	"solana_golang/p2p"
	"solana_golang/rpc"
)

const (
	rpcForwardMagic       uint32 = 0x50575232
	rpcForwardVersion     uint16 = 1
	rpcForwardRequestKind uint8  = 1
	rpcForwardReplyKind   uint8  = 2
	maxRPCForwardMethod          = 128
)

type RPCForwardRequest struct {
	Method string
	Params []byte
}

type RPCForwardError struct {
	Code    int
	Message string
	Data    []byte
}

type RPCForwardResponse struct {
	Result []byte
	Error  *RPCForwardError
}

func MarshalRPCForwardRequest(request RPCForwardRequest, maxPayloadBytes int) ([]byte, error) {
	if strings.TrimSpace(request.Method) == "" || len(request.Method) > maxRPCForwardMethod {
		return nil, fmt.Errorf("poswire: invalid rpc forward method")
	}
	if len(request.Params) > 0 && !json.Valid(request.Params) {
		return nil, fmt.Errorf("poswire: invalid rpc forward params")
	}
	writer := newRPCForwardWriter(rpcForwardRequestKind, maxPayloadBytes)
	if err := writer.WriteString(request.Method); err != nil {
		return nil, fmt.Errorf("poswire: marshal rpc forward method: %w", err)
	}
	writer.WriteBool(len(request.Params) > 0)
	if len(request.Params) == 0 {
		return writer.Bytes(), nil
	}
	if err := writer.WriteBytes(request.Params); err != nil {
		return nil, fmt.Errorf("poswire: marshal rpc forward params: %w", err)
	}
	return writer.Bytes(), nil
}

func UnmarshalRPCForwardRequest(data []byte, maxPayloadBytes int) (RPCForwardRequest, error) {
	reader, err := newRPCForwardReader(data, rpcForwardRequestKind, maxPayloadBytes)
	if err != nil {
		return RPCForwardRequest{}, err
	}
	method, err := reader.ReadString()
	if err != nil {
		return RPCForwardRequest{}, fmt.Errorf("poswire: unmarshal rpc forward method: %w", err)
	}
	if strings.TrimSpace(method) == "" || len(method) > maxRPCForwardMethod {
		return RPCForwardRequest{}, fmt.Errorf("poswire: invalid rpc forward method")
	}
	hasParams, err := reader.ReadBool()
	if err != nil {
		return RPCForwardRequest{}, fmt.Errorf("poswire: unmarshal rpc forward params flag: %w", err)
	}
	var params []byte
	if hasParams {
		params, err = reader.ReadBytes()
		if err != nil {
			return RPCForwardRequest{}, fmt.Errorf("poswire: unmarshal rpc forward params: %w", err)
		}
		if !json.Valid(params) {
			return RPCForwardRequest{}, fmt.Errorf("poswire: invalid rpc forward params")
		}
	}
	if err := reader.EnsureEOF(); err != nil {
		return RPCForwardRequest{}, fmt.Errorf("poswire: rpc forward request eof: %w", err)
	}
	return RPCForwardRequest{Method: method, Params: params}, nil
}

func MarshalRPCForwardResponse(response RPCForwardResponse, maxPayloadBytes int) ([]byte, error) {
	if response.Error != nil && len(response.Result) > 0 {
		return nil, fmt.Errorf("poswire: rpc forward response cannot contain result and error")
	}
	if len(response.Result) > 0 && !json.Valid(response.Result) {
		return nil, fmt.Errorf("poswire: invalid rpc forward result")
	}
	if response.Error != nil && len(response.Error.Data) > 0 && !json.Valid(response.Error.Data) {
		return nil, fmt.Errorf("poswire: invalid rpc forward error data")
	}
	writer := newRPCForwardWriter(rpcForwardReplyKind, maxPayloadBytes)
	writer.WriteBool(len(response.Result) > 0)
	if len(response.Result) > 0 {
		if err := writer.WriteBytes(response.Result); err != nil {
			return nil, fmt.Errorf("poswire: marshal rpc forward result: %w", err)
		}
	}
	writer.WriteBool(response.Error != nil)
	if response.Error == nil {
		return writer.Bytes(), nil
	}
	writeInt64(writer, int64(response.Error.Code))
	if err := writer.WriteString(response.Error.Message); err != nil {
		return nil, fmt.Errorf("poswire: marshal rpc forward error message: %w", err)
	}
	writer.WriteBool(len(response.Error.Data) > 0)
	if len(response.Error.Data) == 0 {
		return writer.Bytes(), nil
	}
	if err := writer.WriteBytes(response.Error.Data); err != nil {
		return nil, fmt.Errorf("poswire: marshal rpc forward error data: %w", err)
	}
	return writer.Bytes(), nil
}

func UnmarshalRPCForwardResponse(data []byte, maxPayloadBytes int) (RPCForwardResponse, error) {
	reader, err := newRPCForwardReader(data, rpcForwardReplyKind, maxPayloadBytes)
	if err != nil {
		return RPCForwardResponse{}, err
	}
	response := RPCForwardResponse{}
	hasResult, err := reader.ReadBool()
	if err != nil {
		return RPCForwardResponse{}, fmt.Errorf("poswire: unmarshal rpc forward result flag: %w", err)
	}
	if hasResult {
		response.Result, err = reader.ReadBytes()
		if err != nil {
			return RPCForwardResponse{}, fmt.Errorf("poswire: unmarshal rpc forward result: %w", err)
		}
		if !json.Valid(response.Result) {
			return RPCForwardResponse{}, fmt.Errorf("poswire: invalid rpc forward result")
		}
	}
	hasError, err := reader.ReadBool()
	if err != nil {
		return RPCForwardResponse{}, fmt.Errorf("poswire: unmarshal rpc forward error flag: %w", err)
	}
	if hasError {
		forwardError, err := readRPCForwardError(reader)
		if err != nil {
			return RPCForwardResponse{}, err
		}
		response.Error = &forwardError
	}
	if response.Error != nil && len(response.Result) > 0 {
		return RPCForwardResponse{}, fmt.Errorf("poswire: rpc forward response contains result and error")
	}
	if err := reader.EnsureEOF(); err != nil {
		return RPCForwardResponse{}, fmt.Errorf("poswire: rpc forward response eof: %w", err)
	}
	return response, nil
}

func RPCForwardErrorFromRPCError(rpcError *rpc.Error) (*RPCForwardError, error) {
	if rpcError == nil {
		return nil, nil
	}
	forwardError := &RPCForwardError{
		Code:    rpcError.Code,
		Message: rpcError.Message,
	}
	if rpcError.Data == nil {
		return forwardError, nil
	}
	data, err := json.Marshal(rpcError.Data)
	if err != nil {
		return nil, fmt.Errorf("poswire: marshal rpc error data: %w", err)
	}
	forwardError.Data = data
	return forwardError, nil
}

func (response RPCForwardResponse) RPCError() *rpc.Error {
	if response.Error == nil {
		return nil
	}
	var data any
	if len(response.Error.Data) > 0 {
		data = json.RawMessage(cloneBytes(response.Error.Data))
	}
	return &rpc.Error{
		Code:    response.Error.Code,
		Message: response.Error.Message,
		Data:    data,
	}
}

func newRPCForwardWriter(kind uint8, maxPayloadBytes int) *borsh.Writer {
	writer := borsh.NewWriter(normalizeRPCForwardMaxBytes(maxPayloadBytes))
	writer.WriteUint32(rpcForwardMagic)
	writer.WriteUint16(rpcForwardVersion)
	writer.WriteUint32(uint32(p2p.ProtocolPoSRPCForwardV1))
	writer.WriteUint8(kind)
	return writer
}

func newRPCForwardReader(data []byte, expectedKind uint8, maxPayloadBytes int) (*borsh.Reader, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("poswire: empty rpc forward payload")
	}
	reader := borsh.NewBorrowedReader(data, normalizeRPCForwardMaxBytes(maxPayloadBytes))
	magic, err := reader.ReadUint32()
	if err != nil {
		return nil, fmt.Errorf("poswire: unmarshal rpc forward magic: %w", err)
	}
	if magic != rpcForwardMagic {
		return nil, fmt.Errorf("poswire: invalid rpc forward magic")
	}
	version, err := reader.ReadUint16()
	if err != nil {
		return nil, fmt.Errorf("poswire: unmarshal rpc forward version: %w", err)
	}
	if version != rpcForwardVersion {
		return nil, fmt.Errorf("poswire: unsupported rpc forward version %d", version)
	}
	protocolID, err := reader.ReadUint32()
	if err != nil {
		return nil, fmt.Errorf("poswire: unmarshal rpc forward protocol: %w", err)
	}
	if p2p.ProtocolID(protocolID) != p2p.ProtocolPoSRPCForwardV1 {
		return nil, fmt.Errorf("poswire: rpc forward protocol mismatch")
	}
	kind, err := reader.ReadUint8()
	if err != nil {
		return nil, fmt.Errorf("poswire: unmarshal rpc forward kind: %w", err)
	}
	if kind != expectedKind {
		return nil, fmt.Errorf("poswire: rpc forward kind mismatch")
	}
	return reader, nil
}

func readRPCForwardError(reader *borsh.Reader) (RPCForwardError, error) {
	code, err := reader.ReadInt64()
	if err != nil {
		return RPCForwardError{}, fmt.Errorf("poswire: unmarshal rpc forward error code: %w", err)
	}
	message, err := reader.ReadString()
	if err != nil {
		return RPCForwardError{}, fmt.Errorf("poswire: unmarshal rpc forward error message: %w", err)
	}
	hasData, err := reader.ReadBool()
	if err != nil {
		return RPCForwardError{}, fmt.Errorf("poswire: unmarshal rpc forward error data flag: %w", err)
	}
	forwardError := RPCForwardError{Code: int(code), Message: message}
	if !hasData {
		return forwardError, nil
	}
	data, err := reader.ReadBytes()
	if err != nil {
		return RPCForwardError{}, fmt.Errorf("poswire: unmarshal rpc forward error data: %w", err)
	}
	if !json.Valid(data) {
		return RPCForwardError{}, fmt.Errorf("poswire: invalid rpc forward error data")
	}
	forwardError.Data = data
	return forwardError, nil
}

func normalizeRPCForwardMaxBytes(maxPayloadBytes int) int {
	if maxPayloadBytes <= 0 || maxPayloadBytes > p2p.DefaultMaxMessageSize {
		return p2p.DefaultMaxMessageSize
	}
	return maxPayloadBytes
}

func writeInt64(writer *borsh.Writer, value int64) {
	writer.WriteInt64(value)
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}
