package p2p

import "errors"

var (
	// ErrNilTransport 表示空传输实现 + 防止 Host 注册无效协议。
	ErrNilTransport = errors.New("p2p: nil transport")
	// ErrNilHandler 表示空连接处理器 + 防止监听后无法处理入站连接。
	ErrNilHandler = errors.New("p2p: nil connection handler")
	// ErrHostClosed 表示 Host 已关闭 + 防止关闭后继续写入连接池。
	ErrHostClosed = errors.New("p2p: host closed")
	// ErrPeerNotFound 表示节点不存在 + 供拨号和发送路径明确失败原因。
	ErrPeerNotFound = errors.New("p2p: peer not found")
	// ErrUnsupportedProtocol 表示协议不支持 + 限制传输层只处理声明的协议。
	ErrUnsupportedProtocol = errors.New("p2p: unsupported protocol")
	// ErrTransportUnavailable 表示传输不可用 + 允许 Host 降级尝试其他协议。
	ErrTransportUnavailable = errors.New("p2p: transport unavailable")
	// ErrInvalidMessage 表示消息无效 + 防止畸形数据进入上层业务。
	ErrInvalidMessage = errors.New("p2p: invalid message")
	// ErrConnectionClosed 表示连接已关闭 + 统一连接关闭后的错误语义。
	ErrConnectionClosed = errors.New("p2p: connection closed")
	// ErrInvalidProtocol 表示协议定义无效 + 防止错误协议进入注册表。
	ErrInvalidProtocol = errors.New("p2p: invalid protocol")
	// ErrProtocolNotFound 表示协议未注册 + 防止消息进入空处理路径。
	ErrProtocolNotFound = errors.New("p2p: protocol not found")
	// ErrProtocolConflict 表示协议重复注册 + 防止处理器被意外覆盖。
	ErrProtocolConflict = errors.New("p2p: protocol conflict")
	// ErrNilProtocolHandler 表示协议处理器为空 + 防止运行时空调用。
	ErrNilProtocolHandler = errors.New("p2p: nil protocol handler")
	// ErrProtocolResponseMismatch 表示协议响应语义不匹配 + 防止请求响应关系混乱。
	ErrProtocolResponseMismatch = errors.New("p2p: protocol response mismatch")
	// ErrSecureSession 表示安全会话失败 + 防止未认证或未加密连接进入业务消息层。
	ErrSecureSession = errors.New("p2p: secure session")
)
