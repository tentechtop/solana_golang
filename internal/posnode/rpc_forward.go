package posnode

import (
	"context"
	"encoding/json"
	"fmt"

	"solana_golang/internal/poswire"
	"solana_golang/p2p"
	"solana_golang/rpc"
)

const (
	maxForwardedRPCBodyBytes = 1 << 20
	maxForwardedRPCBatchSize = 32
)

type rpcForwardDecodedResponse struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpc.Error      `json:"error,omitempty"`
}

// handleRPCForwardRequest 处理公网入口转发请求 + P2P 只承载二进制封装，JSON-RPC 仅在本地 RPC 边界还原。
func (node *posNode) handleRPCForwardRequest(ctx context.Context, message p2p.Message) (p2p.Message, error) {
	if len(message.Payload) == 0 {
		return p2p.Message{}, fmt.Errorf("posnode: forwarded rpc payload is empty")
	}
	if len(message.Payload) > maxForwardedRPCBodyBytes {
		return p2p.Message{}, fmt.Errorf("posnode: forwarded rpc payload too large")
	}
	forwardRequest, err := poswire.UnmarshalRPCForwardRequest(message.Payload, maxForwardedRPCBodyBytes)
	if err != nil {
		return p2p.Message{}, err
	}
	body, err := json.Marshal(rpc.Request{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  forwardRequest.Method,
		Params:  forwardRequest.Params,
	})
	if err != nil {
		return p2p.Message{}, fmt.Errorf("posnode: marshal forwarded rpc request: %w", err)
	}
	responseBody, err := rpc.HandleRawRequest(ctx, rpc.NewPublicRouter(node), body, maxForwardedRPCBatchSize)
	if err != nil {
		return p2p.Message{}, fmt.Errorf("posnode: handle forwarded rpc: %w", err)
	}
	forwardResponse, err := encodeRPCForwardResponse(responseBody)
	if err != nil {
		return p2p.Message{}, err
	}
	responsePayload, err := poswire.MarshalRPCForwardResponse(forwardResponse, maxForwardedRPCBodyBytes)
	if err != nil {
		return p2p.Message{}, fmt.Errorf("posnode: marshal forwarded rpc response: %w", err)
	}
	response, err := p2p.NewResponseMessage(node.peerKeyPair.peerID, p2p.ProtocolPoSRPCForwardV1, message.ID, responsePayload)
	if err != nil {
		return p2p.Message{}, err
	}
	response.ToPeerID = message.FromPeerID
	return response, nil
}

func encodeRPCForwardResponse(responseBody []byte) (poswire.RPCForwardResponse, error) {
	if len(responseBody) == 0 || string(responseBody) == "null" {
		return poswire.RPCForwardResponse{Result: []byte("null")}, nil
	}
	decoded := rpcForwardDecodedResponse{}
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return poswire.RPCForwardResponse{}, fmt.Errorf("posnode: decode forwarded rpc response: %w", err)
	}
	if decoded.Error != nil {
		forwardError, err := poswire.RPCForwardErrorFromRPCError(decoded.Error)
		if err != nil {
			return poswire.RPCForwardResponse{}, err
		}
		return poswire.RPCForwardResponse{Error: forwardError}, nil
	}
	if len(decoded.Result) == 0 {
		return poswire.RPCForwardResponse{Result: []byte("null")}, nil
	}
	return poswire.RPCForwardResponse{Result: decoded.Result}, nil
}
