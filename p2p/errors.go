package p2p

import (
	"context"
	"errors"
	"net"
	"strings"

	"solana_golang/utils"
)

var (
	// ErrNilTransport 表示空传输实现 + 防止 Host 注册无效协议。
	ErrNilTransport = errors.New("p2p: nil transport")
	// ErrNilHandler 表示空连接处理器 + 防止监听后无法处理入站连接。
	ErrNilHandler = errors.New("p2p: nil connection handler")
	// ErrHostClosed 表示 Host 已关闭 + 防止关闭后继续写入连接池。
	ErrHostClosed = errors.New("p2p: host closed")
	// ErrPeerNotFound 表示节点不存在 + 为拨号和发送路径提供明确失败原因。
	ErrPeerNotFound = errors.New("p2p: peer not found")
	// ErrMaxPeersReached 表示节点表已满 + 防止发现层无限写入内存。
	ErrMaxPeersReached = errors.New("p2p: max peers reached")
	// ErrMaxConnectionsReached 表示连接池已满 + 防止恶意节点绕过 peer 表耗尽连接资源。
	ErrMaxConnectionsReached = errors.New("p2p: max connections reached")
	// ErrInboundLimitReached 表示入站连接并发已满 + 在握手前拒绝洪泛连接保护节点资源。
	ErrInboundLimitReached = errors.New("p2p: inbound connection limit reached")
	// ErrPeerIPLimitReached 表示单 IP 连接数已满 + 避免同一来源耗尽连接池。
	ErrPeerIPLimitReached = errors.New("p2p: peer ip connection limit reached")
	// ErrUnsupportedProtocol 表示协议不支持 + 限制传输层只处理声明的协议。
	ErrUnsupportedProtocol = errors.New("p2p: unsupported protocol")
	// ErrTransportUnavailable 表示传输不可用 + 允许 Host 降级尝试其他协议。
	ErrTransportUnavailable = errors.New("p2p: transport unavailable")
	// ErrInvalidMessage 表示消息无效 + 防止畸形数据进入上层业务。
	ErrInvalidMessage = errors.New("p2p: invalid message")
	// ErrConnectionClosed 表示连接已关闭 + 统一连接关闭后的错误语义。
	ErrConnectionClosed = errors.New("p2p: connection closed")
	// ErrDuplicateConnection 表示重复连接已被仲裁拒绝 + 防止同一节点对保留多条读写竞争连接。
	ErrDuplicateConnection = errors.New("p2p: duplicate connection")
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
	// ErrPeerRecordExpired 表示节点签名记录已过期 + 加载持久化节点时可降级为普通节点。
	ErrPeerRecordExpired = errors.New("p2p: peer record expired")
	// ErrPeerRecordNetworkMismatch 表示节点签名记录网络不匹配 + 防止跨网络地址污染路由表。
	ErrPeerRecordNetworkMismatch = errors.New("p2p: peer record network mismatch")
	// ErrPeerBlocked 表示节点已被临时封禁 + 阻止异常节点继续消耗连接和消息处理资源。
	ErrPeerBlocked = errors.New("p2p: peer blocked")
	// ErrPeerBackoff 表示节点处于拨号退避期 + 避免对持续失败节点进行高频重试。
	ErrPeerBackoff = errors.New("p2p: peer dial backoff")
	// ErrRateLimited 表示节点消息超过速率限制 + 保护读循环和协议处理器不被刷爆。
	ErrRateLimited = errors.New("p2p: rate limited")
	// ErrDuplicateMessage 表示消息 ID 重复 + 拒绝重放消息进入协议处理路径。
	ErrDuplicateMessage = errors.New("p2p: duplicate message")
	// ErrProtocolQueueFull 表示协议处理队列已满 + 防止业务处理堆积拖垮连接读循环。
	ErrProtocolQueueFull = errors.New("p2p: protocol queue full")
	// ErrWriteQueueFull 表示连接写队列已满 + 对慢连接施加背压避免内存无限堆积。
	ErrWriteQueueFull = errors.New("p2p: write queue full")
)

// ErrorInfo 保存结构化错误标签 + 让调用方在保留 errors.Is 的同时精确分流。
type ErrorInfo struct {
	Operation string
	PeerID    string
	Protocol  utils.MultiAddressProtocol
	Timeout   bool
	Temporary bool
	Retryable bool
}

// P2PError 包装底层错误 + 为上层调度提供可机器判断的错误上下文。
type P2PError struct {
	cause error
	info  ErrorInfo
}

func (err *P2PError) Error() string {
	if err == nil || err.cause == nil {
		return "p2p: nil error"
	}
	labels := make([]string, 0, 3)
	if err.info.Operation != "" {
		labels = append(labels, "operation="+err.info.Operation)
	}
	if err.info.PeerID != "" {
		labels = append(labels, "peer_id="+err.info.PeerID)
	}
	if err.info.Protocol != "" {
		labels = append(labels, "protocol="+string(err.info.Protocol))
	}
	if len(labels) == 0 {
		return err.cause.Error()
	}
	return "p2p: " + strings.Join(labels, " ") + ": " + err.cause.Error()
}

func (err *P2PError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.cause
}

// Info 返回错误分类信息 + 避免调用方解析错误字符串。
func (err *P2PError) Info() ErrorInfo {
	if err == nil {
		return ErrorInfo{}
	}
	return err.info
}

// WithErrorInfo 添加结构化错误信息 + 保持原始错误链可被 errors.Is/As 识别。
func WithErrorInfo(err error, info ErrorInfo) error {
	if err == nil {
		return nil
	}
	info.Timeout = info.Timeout || isTimeoutCause(err)
	info.Temporary = info.Temporary || isTemporaryCause(err)
	info.Retryable = info.Retryable || isRetryableCause(err)
	return &P2PError{cause: err, info: info}
}

// ErrorInfoOf 读取结构化错误信息 + 兼容未包装错误的基础分类。
func ErrorInfoOf(err error) (ErrorInfo, bool) {
	if err == nil {
		return ErrorInfo{}, false
	}
	var p2pError *P2PError
	if errors.As(err, &p2pError) {
		return p2pError.Info(), true
	}
	return ErrorInfo{
		Timeout:   isTimeoutCause(err),
		Temporary: isTemporaryCause(err),
		Retryable: isRetryableCause(err),
	}, false
}

// IsTimeoutError 判断超时错误 + 让调用方不依赖具体传输实现。
func IsTimeoutError(err error) bool {
	info, _ := ErrorInfoOf(err)
	return info.Timeout
}

// IsTemporaryError 判断临时错误 + 支持退避后重试策略。
func IsTemporaryError(err error) bool {
	info, _ := ErrorInfoOf(err)
	return info.Temporary
}

// IsRetryableError 判断可重试错误 + 统一拨号、请求和背压重试分支。
func IsRetryableError(err error) bool {
	info, _ := ErrorInfoOf(err)
	return info.Retryable
}

func isTimeoutCause(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netError net.Error
	return errors.As(err, &netError) && netError.Timeout()
}

func isTemporaryCause(err error) bool {
	if isTimeoutCause(err) {
		return true
	}
	return errors.Is(err, ErrPeerBackoff) ||
		errors.Is(err, ErrRateLimited) ||
		errors.Is(err, ErrWriteQueueFull) ||
		errors.Is(err, ErrProtocolQueueFull) ||
		errors.Is(err, ErrTransportUnavailable)
}

func isRetryableCause(err error) bool {
	return isTemporaryCause(err) ||
		errors.Is(err, ErrConnectionClosed) ||
		errors.Is(err, ErrDuplicateConnection)
}
