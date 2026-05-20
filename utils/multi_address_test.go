package utils

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseMultiAddressIPv4(t *testing.T) {
	peerID := Base58Encode(bytes.Repeat([]byte{0x01}, PublicKeySize))
	raw := "/ip4/101.35.87.31/tcp/5002/p2p/" + peerID

	address, err := ParseMultiAddress(raw)
	if err != nil {
		t.Fatalf("ParseMultiAddress() error = %v", err)
	}
	if address.IPType != MultiAddressIP4 {
		t.Fatalf("IPType = %q, want %q", address.IPType, MultiAddressIP4)
	}
	if address.IPAddress != "101.35.87.31" {
		t.Fatalf("IPAddress = %q, want %q", address.IPAddress, "101.35.87.31")
	}
	if address.Protocol != ProtocolTCP {
		t.Fatalf("Protocol = %q, want %q", address.Protocol, ProtocolTCP)
	}
	if address.Port != 5002 {
		t.Fatalf("Port = %d, want 5002", address.Port)
	}
	if address.PeerID != peerID {
		t.Fatalf("PeerID = %q, want %q", address.PeerID, peerID)
	}
	if address.RawAddress != raw {
		t.Fatalf("RawAddress = %q, want %q", address.RawAddress, raw)
	}
	if address.String() != raw {
		t.Fatalf("String() = %q, want %q", address.String(), raw)
	}
}

func TestParseMultiAddressNormalizesProtocol(t *testing.T) {
	peerID := Base58Encode(bytes.Repeat([]byte{0x02}, PublicKeySize))
	raw := "/ip6/2001:db8::1/QUIC/443/p2p/" + peerID

	address, err := NewMultiAddress(raw)
	if err != nil {
		t.Fatalf("NewMultiAddress() error = %v", err)
	}
	want := "/ip6/2001:db8::1/quic/443/p2p/" + peerID
	if address.ToRawAddress() != want {
		t.Fatalf("ToRawAddress() = %q, want %q", address.ToRawAddress(), want)
	}
	if address.RawAddress != raw {
		t.Fatalf("RawAddress = %q, want %q", address.RawAddress, raw)
	}
	if address.Protocol != ProtocolQUIC {
		t.Fatalf("Protocol = %q, want %q", address.Protocol, ProtocolQUIC)
	}
}

func TestBuildMultiAddress(t *testing.T) {
	peerID := Base58Encode(bytes.Repeat([]byte{0x03}, PublicKeySize))

	address, err := BuildMultiAddress(MultiAddressIP4, "10.0.0.1", ProtocolUDP, 8080, peerID)
	if err != nil {
		t.Fatalf("BuildMultiAddress() error = %v", err)
	}
	want := "/ip4/10.0.0.1/udp/8080/p2p/" + peerID
	if address.RawAddress != want {
		t.Fatalf("RawAddress = %q, want %q", address.RawAddress, want)
	}
}

func TestMultiAddressInvalidInputs(t *testing.T) {
	peerID := Base58Encode(bytes.Repeat([]byte{0x04}, PublicKeySize))
	shortPeerID := Base58Encode([]byte{1, 2, 3})

	tests := []struct {
		name string
		raw  string
	}{
		{name: "empty", raw: ""},
		{name: "format", raw: "ip4/127.0.0.1/tcp/80/p2p/" + peerID},
		{name: "ip type", raw: "/dns/example.com/tcp/80/p2p/" + peerID},
		{name: "ip address", raw: "/ip4/999.1.1.1/tcp/80/p2p/" + peerID},
		{name: "ip mismatch", raw: "/ip4/2001:db8::1/tcp/80/p2p/" + peerID},
		{name: "protocol", raw: "/ip4/127.0.0.1/http/80/p2p/" + peerID},
		{name: "port number", raw: "/ip4/127.0.0.1/tcp/eighty/p2p/" + peerID},
		{name: "port range", raw: "/ip4/127.0.0.1/tcp/70000/p2p/" + peerID},
		{name: "p2p segment", raw: "/ip4/127.0.0.1/tcp/80/peer/" + peerID},
		{name: "peer id base58", raw: "/ip4/127.0.0.1/tcp/80/p2p/0OIl"},
		{name: "peer id length", raw: "/ip4/127.0.0.1/tcp/80/p2p/" + shortPeerID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseMultiAddress(tt.raw); err == nil {
				t.Fatal("ParseMultiAddress() error = nil, want error")
			}
		})
	}
}

func TestBuildMultiAddressRejectsInvalidParts(t *testing.T) {
	peerID := Base58Encode(bytes.Repeat([]byte{0x05}, PublicKeySize))

	if _, err := BuildMultiAddress(MultiAddressIP6, "127.0.0.1", ProtocolTCP, 80, peerID); err == nil {
		t.Fatal("BuildMultiAddress(ip mismatch) error = nil, want error")
	}
	if _, err := BuildMultiAddress(MultiAddressIP4, "127.0.0.1", MultiAddressProtocol("http"), 80, peerID); err == nil {
		t.Fatal("BuildMultiAddress(protocol) error = nil, want error")
	}
	if _, err := BuildMultiAddress(MultiAddressIP4, "127.0.0.1", ProtocolTCP, 0, peerID); err == nil {
		t.Fatal("BuildMultiAddress(port) error = nil, want error")
	}
	if _, err := BuildMultiAddress(MultiAddressIP4, "127.0.0.1", ProtocolTCP, 80, strings.Repeat("1", 31)); err == nil {
		t.Fatal("BuildMultiAddress(peer id) error = nil, want error")
	}
}
