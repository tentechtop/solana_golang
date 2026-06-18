package posnode

import (
	"context"
	"fmt"

	"solana_golang/p2p"
	"solana_golang/rpc"
)

const (
	maxForwardedRPCBodyBytes = 1 << 20
	maxForwardedRPCBatchSize = 32
)

// handleRPCForwardRequest 处理公网入口转发请求 + 复用公网 RPC 白名单避免暴露管理和私钥接口。
func (node *posNode) handleRPCForwardRequest(ctx context.Context, message p2p.Message) (p2p.Message, error) {
	if len(message.Payload) == 0 {
		return p2p.Message{}, fmt.Errorf("posnode: forwarded rpc body is empty")
	}
	if len(message.Payload) > maxForwardedRPCBodyBytes {
		return p2p.Message{}, fmt.Errorf("posnode: forwarded rpc body too large")
	}
	responsePayload, err := rpc.HandleRawRequest(ctx, rpc.NewPublicRouter(node), message.Payload, maxForwardedRPCBatchSize)
	if err != nil {
		return p2p.Message{}, fmt.Errorf("posnode: handle forwarded rpc: %w", err)
	}
	response, err := p2p.NewResponseMessage(node.peerKeyPair.peerID, p2p.ProtocolPoSRPCForwardV1, message.ID, responsePayload)
	if err != nil {
		return p2p.Message{}, err
	}
	response.ToPeerID = message.FromPeerID
	return response, nil
}
