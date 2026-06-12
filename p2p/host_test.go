package p2p

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"solana_golang/utils"
)

func TestHostRequiresSecureSessionByDefault(t *testing.T) {
	_, err := NewHost(HostConfig{PeerID: testPeerID(35)})
	if !errors.Is(err, ErrSecureSession) {
		t.Fatalf("NewHost(insecure default) error = %v, want ErrSecureSession", err)
	}
}

func TestHostRejectsInsecureProduction(t *testing.T) {
	_, err := NewHost(HostConfig{
		PeerID:        testPeerID(72),
		AllowInsecure: true,
		Production:    true,
	})
	if !errors.Is(err, ErrSecureSession) {
		t.Fatalf("NewHost(production insecure) error = %v, want ErrSecureSession", err)
	}

	_, err = NewHost(HostConfig{
		PeerID:        testPeerID(73),
		AllowInsecure: true,
		Environment:   "production",
	})
	if !errors.Is(err, ErrSecureSession) {
		t.Fatalf("NewHost(environment production insecure) error = %v, want ErrSecureSession", err)
	}
}

func TestHostRejectsWildcardAdvertisedAddress(t *testing.T) {
	peerID := testPeerID(36)
	address, err := utils.BuildMultiAddress(utils.MultiAddressIP4, "0.0.0.0", utils.ProtocolTCP, 5031, peerID)
	if err != nil {
		t.Fatalf("BuildMultiAddress() error = %v", err)
	}

	_, err = NewHost(HostConfig{
		PeerID:              peerID,
		AllowInsecure:       true,
		AdvertisedAddresses: []utils.MultiAddress{address},
	})
	if !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("NewHost(wildcard advertised) error = %v, want ErrInvalidMessage", err)
	}
}

func TestHostDialAddressRejectsInvalidAddress(t *testing.T) {
	host, err := NewHost(HostConfig{
		PeerID:        testPeerID(74),
		AllowInsecure: true,
	})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	address := utils.MultiAddress{
		IPType:    utils.MultiAddressIP4,
		IPAddress: "127.0.0.1",
		Protocol:  utils.ProtocolTCP,
		Port:      70000,
		PeerID:    testPeerID(75),
	}
	_, err = host.DialAddress(context.Background(), address)
	if !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("DialAddress(invalid) error = %v, want ErrInvalidMessage", err)
	}
}

func TestHostSkipsWildcardListenAddressAdvertisement(t *testing.T) {
	peerID := testPeerID(37)
	host, err := NewHost(HostConfig{
		PeerID:        peerID,
		AllowInsecure: true,
	})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	address, err := utils.BuildMultiAddress(utils.MultiAddressIP4, "0.0.0.0", utils.ProtocolTCP, 5032, peerID)
	if err != nil {
		t.Fatalf("BuildMultiAddress() error = %v", err)
	}
	host.addAdvertisedAddress(address)

	if addresses := host.advertisedAddressSnapshots(); len(addresses) != 0 {
		t.Fatalf("advertised addresses = %+v, want none", addresses)
	}
}

func TestHostSendFallsBackFromQUICToTCP(t *testing.T) {
	clientPeerID := testPeerID(5)
	serverPeerID := testPeerID(6)
	tcpAddress := testAddress(t, utils.ProtocolTCP, freeTCPPort(t), serverPeerID)
	quicAddress := testAddress(t, utils.ProtocolQUIC, tcpAddress.Port, serverPeerID)

	serverHost, err := NewHost(HostConfig{PeerID: serverPeerID, AllowInsecure: true}, NewTCPTransport())
	if err != nil {
		t.Fatalf("NewHost(server) error = %v", err)
	}
	clientHost, err := NewHost(HostConfig{PeerID: clientPeerID, AllowInsecure: true})
	if err != nil {
		t.Fatalf("NewHost(client) error = %v", err)
	}
	defer serverHost.Close()
	defer clientHost.Close()

	peer, err := NewPeer(serverPeerID, []utils.MultiAddress{quicAddress, tcpAddress})
	if err != nil {
		t.Fatalf("NewPeer() error = %v", err)
	}
	if err := clientHost.AddPeer(peer); err != nil {
		t.Fatalf("AddPeer() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	received := make(chan Message, 1)
	listenErrors := make(chan error, 1)
	go func() {
		listenErrors <- serverHost.Listen(ctx, tcpAddress, func(ctx context.Context, connection Connection) {
			defer connection.Close()
			readContext, readCancel := context.WithTimeout(context.Background(), time.Second)
			defer readCancel()
			message, err := connection.ReadMessage(readContext)
			if err == nil {
				received <- message
			}
		})
	}()
	waitForTCP(t, tcpAddress.Port)

	message := Message{Type: ProtocolPingV1, Payload: []byte("host")}
	sendContext, sendCancel := context.WithTimeout(context.Background(), time.Second)
	defer sendCancel()
	if err := clientHost.Send(sendContext, serverPeerID, message); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	select {
	case decoded := <-received:
		if decoded.FromPeerID != clientPeerID {
			t.Fatalf("FromPeerID = %q, want %q", decoded.FromPeerID, clientPeerID)
		}
		if decoded.ToPeerID != serverPeerID {
			t.Fatalf("ToPeerID = %q, want %q", decoded.ToPeerID, serverPeerID)
		}
		if !bytes.Equal(decoded.Payload, []byte("host")) {
			t.Fatalf("Payload = %q, want host", decoded.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for host message")
	}

	connection, ok := clientHost.Connection(serverPeerID)
	if !ok {
		t.Fatal("Connection() ok = false, want true")
	}
	if connection.Protocol() != utils.ProtocolTCP {
		t.Fatalf("Protocol = %q, want %q", connection.Protocol(), utils.ProtocolTCP)
	}

	cancel()
	if err := <-listenErrors; err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
}

func TestHostDialCandidatesUseRemoteProtocolPreference(t *testing.T) {
	localPeerID := testPeerID(88)
	remotePeerID := testPeerID(89)
	host, err := NewHost(HostConfig{
		PeerID:             localPeerID,
		AllowInsecure:      true,
		PreferredProtocols: []utils.MultiAddressProtocol{utils.ProtocolQUIC, utils.ProtocolTCP},
	})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	peer, err := NewPeer(remotePeerID, []utils.MultiAddress{
		testAddress(t, utils.ProtocolQUIC, 5033, remotePeerID),
		testAddress(t, utils.ProtocolTCP, 5034, remotePeerID),
	})
	if err != nil {
		t.Fatalf("NewPeer() error = %v", err)
	}
	peer.PreferredProtocols = []utils.MultiAddressProtocol{utils.ProtocolTCP, utils.ProtocolQUIC}

	addresses := host.dialCandidateAddresses(peer)
	if len(addresses) != 2 {
		t.Fatalf("dialCandidateAddresses() len = %d, want 2", len(addresses))
	}
	if addresses[0].Protocol != utils.ProtocolTCP {
		t.Fatalf("first protocol = %q, want tcp", addresses[0].Protocol)
	}
}

func TestHostDialAddressMarksVerifiedAndObservedAddress(t *testing.T) {
	localPeerID := testPeerID(90)
	remotePeerID := testPeerID(91)
	dialAddress := testAddress(t, utils.ProtocolTCP, 5035, remotePeerID)
	baseConnection := newScriptedConnection(utils.ProtocolTCP, remotePeerID, nil)
	host, err := NewHost(
		HostConfig{PeerID: localPeerID, AllowInsecure: true},
		dialOnlyTransport{protocol: utils.ProtocolTCP, connection: baseConnection},
	)
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	connection, err := host.DialAddress(context.Background(), dialAddress)
	if err != nil {
		t.Fatalf("DialAddress() error = %v", err)
	}
	defer connection.Close()

	peer, ok := host.Peer(remotePeerID)
	if !ok {
		t.Fatal("Peer() ok = false, want true")
	}
	if len(peer.AdvertisedAddresses) != 0 {
		t.Fatalf("AdvertisedAddresses = %+v, want empty for direct dial", peer.AdvertisedAddresses)
	}
	if len(peer.VerifiedAddresses) != 1 || peer.VerifiedAddresses[0].String() != dialAddress.String() {
		t.Fatalf("VerifiedAddresses = %+v, want dial address", peer.VerifiedAddresses)
	}

	state, ok := host.ConnectionState(remotePeerID)
	if !ok {
		t.Fatal("ConnectionState() ok = false, want true")
	}
	if state.ObservedRemoteAddress != baseConnection.RemoteAddress() {
		t.Fatalf("ObservedRemoteAddress = %q, want %q", state.ObservedRemoteAddress, baseConnection.RemoteAddress())
	}
	if state.RemoteAddress != state.ObservedRemoteAddress {
		t.Fatalf("RemoteAddress = %q, want compatibility with observed", state.RemoteAddress)
	}
}

type dialOnlyTransport struct {
	protocol   utils.MultiAddressProtocol
	connection Connection
}

func (transport dialOnlyTransport) Protocol() utils.MultiAddressProtocol {
	return transport.protocol
}

func (transport dialOnlyTransport) Listen(
	ctx context.Context,
	address utils.MultiAddress,
	handler ConnectionHandler,
) error {
	return nil
}

func (transport dialOnlyTransport) Dial(ctx context.Context, address utils.MultiAddress) (Connection, error) {
	if ctx != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return transport.connection, nil
}

func (transport dialOnlyTransport) Close() error {
	return nil
}
