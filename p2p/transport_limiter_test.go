package p2p

import (
	"errors"
	"testing"
)

func TestTransportInboundLimiterRejectsPerIPOverflow(t *testing.T) {
	limiter := newTransportInboundLimiter(4, 1)
	release, err := limiter.acquire("127.0.0.1:10001")
	if err != nil {
		t.Fatalf("acquire(first) error = %v", err)
	}
	defer release()

	_, err = limiter.acquire("127.0.0.1:10002")
	if !errors.Is(err, ErrPeerIPLimitReached) {
		t.Fatalf("acquire(second) error = %v, want ErrPeerIPLimitReached", err)
	}
}

func TestTransportInboundLimiterRejectsPendingOverflow(t *testing.T) {
	limiter := newTransportInboundLimiter(1, 16)
	release, err := limiter.acquire("127.0.0.1:10001")
	if err != nil {
		t.Fatalf("acquire(first) error = %v", err)
	}
	defer release()

	_, err = limiter.acquire("127.0.0.2:10002")
	if !errors.Is(err, ErrInboundLimitReached) {
		t.Fatalf("acquire(second) error = %v, want ErrInboundLimitReached", err)
	}
}
