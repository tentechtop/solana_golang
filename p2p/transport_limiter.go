package p2p

import (
	"errors"
	"net"
	"sync"
)

// transportInboundLimiter 限制传输层入站连接 + 在启动连接 goroutine 前拒绝过载来源。
type transportInboundLimiter struct {
	slots       chan struct{}
	maxPerIP    int
	mutex       sync.Mutex
	connections map[string]int
}

func newTransportInboundLimiter(maxPending int, maxPerIP int) *transportInboundLimiter {
	return &transportInboundLimiter{
		slots:       make(chan struct{}, normalizeMaxPendingInbound(maxPending, defaultMaxPeers)),
		maxPerIP:    normalizeMaxConnectionsPerIP(maxPerIP, defaultMaxPeers),
		connections: make(map[string]int),
	}
}

func (limiter *transportInboundLimiter) acquire(remoteAddress string) (func(), error) {
	if limiter == nil {
		return func() {}, nil
	}
	remoteIP := remoteIPFromConnectionAddress(remoteAddress)
	select {
	case limiter.slots <- struct{}{}:
	default:
		return nil, ErrInboundLimitReached
	}
	if err := limiter.acquireIP(remoteIP); err != nil {
		<-limiter.slots
		return nil, err
	}
	return func() {
		limiter.releaseIP(remoteIP)
		<-limiter.slots
	}, nil
}

func (limiter *transportInboundLimiter) acquireIP(remoteIP string) error {
	if remoteIP == "" || limiter.maxPerIP <= 0 {
		return nil
	}
	limiter.mutex.Lock()
	defer limiter.mutex.Unlock()
	if limiter.connections[remoteIP] >= limiter.maxPerIP {
		return ErrPeerIPLimitReached
	}
	limiter.connections[remoteIP]++
	return nil
}

func (limiter *transportInboundLimiter) releaseIP(remoteIP string) {
	if remoteIP == "" || limiter.maxPerIP <= 0 {
		return
	}
	limiter.mutex.Lock()
	defer limiter.mutex.Unlock()
	count := limiter.connections[remoteIP]
	if count <= 1 {
		delete(limiter.connections, remoteIP)
		return
	}
	limiter.connections[remoteIP] = count - 1
}

func closeRejectedNetConnection(connection net.Conn, err error) error {
	if connection != nil {
		_ = connection.Close()
	}
	if errors.Is(err, ErrPeerIPLimitReached) {
		return ErrPeerIPLimitReached
	}
	return ErrInboundLimitReached
}
