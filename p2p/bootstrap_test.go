package p2p

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"solana_golang/utils"
)

func TestHostListenSkipsWildcardAdvertisementWithoutWarning(t *testing.T) {
	peerID := testPeerID(80)
	var logBuffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuffer, &slog.HandlerOptions{Level: slog.LevelDebug}))
	host, err := NewHost(HostConfig{
		PeerID:        peerID,
		AllowInsecure: true,
		Logger:        logger,
	}, listenOnlyTransport{protocol: utils.ProtocolTCP})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	address, err := utils.BuildMultiAddress(utils.MultiAddressIP4, "0.0.0.0", utils.ProtocolTCP, 5080, peerID)
	if err != nil {
		t.Fatalf("BuildMultiAddress() error = %v", err)
	}
	err = host.Listen(context.Background(), address, func(ctx context.Context, connection Connection) {})
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	if addresses := host.advertisedAddressSnapshots(); len(addresses) != 0 {
		t.Fatalf("advertised addresses = %+v, want none", addresses)
	}
	if strings.Contains(logBuffer.String(), "p2p advertised address skipped") {
		t.Fatalf("wildcard listen produced advertised warning: %s", logBuffer.String())
	}
}

func TestImportDiscoveredPeersCountsOnlyNewUniquePeers(t *testing.T) {
	localPeerID := testPeerID(81)
	host, err := NewHost(HostConfig{
		PeerID:        localPeerID,
		AllowInsecure: true,
	})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	existingPeer := testPeer(t, testPeerID(82), 5082)
	if err := host.AddPeer(existingPeer); err != nil {
		t.Fatalf("AddPeer(existing) error = %v", err)
	}
	newPeer := testPeer(t, testPeerID(83), 5083)

	discoveredPeers, err := host.importDiscoveredPeers([]Peer{
		existingPeer,
		newPeer,
		newPeer,
		{ID: localPeerID},
	})
	if err != nil {
		t.Fatalf("importDiscoveredPeers() error = %v", err)
	}
	if discoveredPeers != 1 {
		t.Fatalf("discovered peers = %d, want 1", discoveredPeers)
	}
	if _, ok := host.Peer(newPeer.ID); !ok {
		t.Fatal("new peer was not stored")
	}
}

func testPeer(t *testing.T, peerID string, port int) Peer {
	t.Helper()
	peer, err := NewPeer(peerID, []utils.MultiAddress{
		testAddress(t, utils.ProtocolTCP, port, peerID),
	})
	if err != nil {
		t.Fatalf("NewPeer() error = %v", err)
	}
	return peer
}

type listenOnlyTransport struct {
	protocol utils.MultiAddressProtocol
}

func (transport listenOnlyTransport) Protocol() utils.MultiAddressProtocol {
	return transport.protocol
}

func (transport listenOnlyTransport) Listen(
	ctx context.Context,
	address utils.MultiAddress,
	handler ConnectionHandler,
) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

func (transport listenOnlyTransport) Dial(ctx context.Context, address utils.MultiAddress) (Connection, error) {
	return nil, errors.New("dial not implemented")
}

func (transport listenOnlyTransport) Close() error {
	return nil
}
