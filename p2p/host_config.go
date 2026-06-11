package p2p

import (
	"fmt"
	"log/slog"
	"net"
	"time"

	"solana_golang/utils"
)

// normalizeDialTimeout 归一化拨号超时 + 防止零值导致拨号永久阻塞。
func normalizeDialTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return defaultDialTimeout
	}
	return timeout
}

// normalizeHeartbeatInterval 归一化心跳间隔 + 防止零值导致后台循环异常。
func normalizeHeartbeatInterval(interval time.Duration) time.Duration {
	if interval <= 0 {
		return defaultHeartbeatInterval
	}
	return interval
}

// normalizeConnectionIdle 归一化连接空闲时间 + 保证未配置时使用安全默认值。
func normalizeConnectionIdle(idle time.Duration) time.Duration {
	if idle <= 0 {
		return defaultConnectionIdle
	}
	return idle
}

// normalizeMaxPeerFailures 归一化失败阈值 + 防止零值导致首次错误即永久不可用。
func normalizeMaxPeerFailures(maxFailures uint32) uint32 {
	if maxFailures == 0 {
		return defaultMaxPeerFailures
	}
	return maxFailures
}

func normalizeMaxPeers(maxPeers int) int {
	if maxPeers <= 0 {
		return defaultMaxPeers
	}
	return maxPeers
}

func normalizeBroadcastConcurrency(concurrency int) int {
	if concurrency <= 0 {
		return 32
	}
	if concurrency > 1024 {
		return 1024
	}
	return concurrency
}

// normalizeLogger 归一化日志器 + 使用默认日志器避免空指针分支散落业务代码。
func normalizeLogger(logger *slog.Logger) *slog.Logger {
	return utils.EnsureLogger(logger)
}

// normalizeRegistry 归一化协议注册表 + 允许测试注入同时保证生产默认可用。
func normalizeRegistry(registry *ProtocolRegistry) *ProtocolRegistry {
	if registry != nil {
		return registry
	}
	return NewProtocolRegistry()
}

// normalizeRoutingTable 归一化 KAD 路由表 + 默认启用本节点路由表。
func normalizeRoutingTable(table *KADRoutingTable, peerID string) (*KADRoutingTable, error) {
	if table != nil {
		return table, nil
	}
	return NewKADRoutingTable(KADRoutingTableConfig{LocalPeerID: peerID})
}

func validateAdvertisedAddresses(addresses []utils.MultiAddress, peerID string) error {
	for _, address := range addresses {
		if address.PeerID != peerID {
			return fmt.Errorf("%w: advertised address owner mismatch", ErrInvalidMessage)
		}
		if !isDialableAdvertisedAddress(address) {
			return fmt.Errorf("%w: advertised address is not dialable", ErrInvalidMessage)
		}
	}
	return nil
}

func isDialableAdvertisedAddress(address utils.MultiAddress) bool {
	ip := net.ParseIP(address.IPAddress)
	return ip != nil && !ip.IsUnspecified()
}

// copyConnections 复制连接集合 + 缩短 Host 锁持有时间后再关闭连接。
func copyConnections(source map[string]Connection) []Connection {
	connections := make([]Connection, 0, len(source))
	for _, connection := range source {
		connections = append(connections, connection)
	}
	return connections
}

// copyTransports 复制传输集合 + 缩短 Host 锁持有时间后再关闭监听资源。
func copyTransports(source map[utils.MultiAddressProtocol]Transport) []Transport {
	transports := make([]Transport, 0, len(source))
	for _, transport := range source {
		transports = append(transports, transport)
	}
	return transports
}
