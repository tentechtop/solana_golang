package p2p

import (
	"context"
	"fmt"

	"solana_golang/utils"
)

// QUICTransportConfig 保存 QUIC 配置 + 预留后续接入真实传输实现的入口。
type QUICTransportConfig struct {
	MaxMessageSize int
}

// QUICTransport 表示 QUIC 传输适配器 + 在未接入底层库前保持协议边界稳定。
type QUICTransport struct {
	maxMessageSize int
}

// NewQUICTransport 创建 QUIC 传输适配器 + 让 Host 可以注册 QUIC 协议。
func NewQUICTransport() *QUICTransport {
	return NewQUICTransportWithConfig(QUICTransportConfig{})
}

// NewQUICTransportWithConfig 创建 QUIC 传输适配器 + 保留消息上限配置。
func NewQUICTransportWithConfig(config QUICTransportConfig) *QUICTransport {
	return &QUICTransport{maxMessageSize: normalizeMaxMessageSize(config.MaxMessageSize)}
}

func (transport *QUICTransport) Protocol() utils.MultiAddressProtocol {
	return utils.ProtocolQUIC
}

// Listen 返回 QUIC 未启用错误 + 标明需要接入真实 QUIC 库后才能监听。
func (transport *QUICTransport) Listen(ctx context.Context, address utils.MultiAddress, handler ConnectionHandler) error {
	if err := validateListenInput(address, utils.ProtocolQUIC, handler); err != nil {
		return err
	}
	return fmt.Errorf("%w: quic listen adapter is not wired", ErrTransportUnavailable)
}

// Dial 返回 QUIC 未启用错误 + 允许 Host 自动降级尝试 TCP 地址。
func (transport *QUICTransport) Dial(ctx context.Context, address utils.MultiAddress) (Connection, error) {
	if err := validateDialInput(address, utils.ProtocolQUIC); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("%w: quic dial adapter is not wired", ErrTransportUnavailable)
}

// Close 关闭 QUIC 适配器 + 当前无底层资源需要释放。
func (transport *QUICTransport) Close() error {
	return nil
}
