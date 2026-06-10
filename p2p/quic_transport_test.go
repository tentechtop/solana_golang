package p2p

import (
	"context"
	"errors"
	"testing"

	"solana_golang/utils"
)

func TestQUICTransportUnavailable(t *testing.T) {
	peerID := testPeerID(7)
	address := testAddress(t, utils.ProtocolQUIC, 4001, peerID)
	transport := NewQUICTransport()

	_, err := transport.Dial(context.Background(), address)
	if !errors.Is(err, ErrTransportUnavailable) {
		t.Fatalf("Dial() error = %v, want ErrTransportUnavailable", err)
	}
}
