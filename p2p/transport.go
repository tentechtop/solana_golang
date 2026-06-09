package p2p

import (
	"context"

	"solana_golang/utils"
)

// ConnectionHandler 处理入站连接 + 由传输层接收连接后交给上层协议。
type ConnectionHandler func(ctx context.Context, connection Connection)

// Connection 抽象单个 P2P 连接 + 屏蔽 TCP 和 QUIC 的读写差异。
type Connection interface {
	ID() string
	Protocol() utils.MultiAddressProtocol
	RemotePeerID() string
	LocalAddress() string
	RemoteAddress() string
	ReadMessage(ctx context.Context) (Message, error)
	WriteMessage(ctx context.Context, message Message) error
	Close() error
}

// Transport 抽象传输协议 + 让 Host 通过协议枚举选择具体实现。
type Transport interface {
	Protocol() utils.MultiAddressProtocol
	Listen(ctx context.Context, address utils.MultiAddress, handler ConnectionHandler) error
	Dial(ctx context.Context, address utils.MultiAddress) (Connection, error)
	Close() error
}
