package p2p

import (
	"context"
	"fmt"
	"time"
)

const (
	defaultHandshakeTimeout = 5 * time.Second
)

func normalizeHandshakeTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return defaultHandshakeTimeout
	}
	return timeout
}

func normalizeMaxConnections(maxConnections int, maxPeers int) int {
	if maxConnections > 0 {
		return maxConnections
	}
	return normalizeMaxPeers(maxPeers)
}

func normalizeMaxPendingInbound(maxPendingInbound int, maxConnections int) int {
	if maxPendingInbound > 0 {
		return maxPendingInbound
	}
	if maxConnections <= 0 {
		return defaultMaxPeers
	}
	return maxConnections
}

func newInboundLimiter(limit int) chan struct{} {
	if limit <= 0 {
		limit = defaultMaxPeers
	}
	return make(chan struct{}, limit)
}

func (host *Host) acquireInboundSlot(ctx context.Context) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}

	select {
	case host.inboundSlots <- struct{}{}:
		return func() {
			<-host.inboundSlots
		}, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("p2p: acquire inbound slot: %w", ctx.Err())
	default:
		host.metrics.inboundRejected.Add(1)
		return nil, ErrInboundLimitReached
	}
}

func (host *Host) withHandshakeTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	handshakeDeadline := time.Now().Add(host.handshakeTimeout)
	if parentDeadline, ok := ctx.Deadline(); ok && parentDeadline.Before(handshakeDeadline) {
		return context.WithCancel(ctx)
	}
	return context.WithDeadline(ctx, handshakeDeadline)
}
