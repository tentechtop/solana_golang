package utils

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

const (
	multiAddressSeparator = "/"

	// MultiAddressIP4 is the ip4 multi-address protocol segment.
	MultiAddressIP4 = "ip4"
	// MultiAddressIP6 is the ip6 multi-address protocol segment.
	MultiAddressIP6 = "ip6"
)

// MultiAddressProtocol is a supported P2P transport protocol.
type MultiAddressProtocol string

const (
	// ProtocolTCP represents a TCP transport segment.
	ProtocolTCP MultiAddressProtocol = "tcp"
	// ProtocolUDP represents a UDP transport segment.
	ProtocolUDP MultiAddressProtocol = "udp"
	// ProtocolQUIC represents a QUIC transport segment.
	ProtocolQUIC MultiAddressProtocol = "quic"
)

// MultiAddress stores a parsed P2P multi-address.
type MultiAddress struct {
	IPType     string
	IPAddress  string
	Protocol   MultiAddressProtocol
	Port       int
	PeerID     string
	RawAddress string
}

// NewMultiAddress parses rawAddress into a MultiAddress.
func NewMultiAddress(rawAddress string) (MultiAddress, error) {
	return ParseMultiAddress(rawAddress)
}

// ParseMultiAddress parses a standard address such as
// /ip4/101.35.87.31/tcp/5002/p2p/Base58Encoded32BytePeerID.
func ParseMultiAddress(rawAddress string) (MultiAddress, error) {
	rawAddress = strings.TrimSpace(rawAddress)
	if rawAddress == "" {
		return MultiAddress{}, fmt.Errorf("utils: multi-address cannot be empty")
	}

	segments := strings.Split(rawAddress, multiAddressSeparator)
	if len(segments) != 7 || segments[0] != "" {
		return MultiAddress{}, fmt.Errorf("utils: invalid multi-address format %q", rawAddress)
	}

	ipType, err := normalizeMultiAddressIPType(segments[1])
	if err != nil {
		return MultiAddress{}, err
	}
	ipAddress := segments[2]
	if !isValidMultiAddressIP(ipAddress, ipType) {
		return MultiAddress{}, fmt.Errorf("utils: invalid %s address %q", ipType, ipAddress)
	}

	protocol, err := ParseMultiAddressProtocol(segments[3])
	if err != nil {
		return MultiAddress{}, err
	}

	port, err := strconv.Atoi(segments[4])
	if err != nil {
		return MultiAddress{}, fmt.Errorf("utils: multi-address port must be numeric: %q", segments[4])
	}
	if err := validateMultiAddressPort(port); err != nil {
		return MultiAddress{}, err
	}

	if !strings.EqualFold(segments[5], "p2p") {
		return MultiAddress{}, fmt.Errorf("utils: multi-address missing p2p segment")
	}

	peerID := segments[6]
	if err := validateMultiAddressPeerID(peerID); err != nil {
		return MultiAddress{}, err
	}

	address := MultiAddress{
		IPType:     ipType,
		IPAddress:  ipAddress,
		Protocol:   protocol,
		Port:       port,
		PeerID:     peerID,
		RawAddress: rawAddress,
	}
	return address, nil
}

// BuildMultiAddress validates the parts and builds a canonical MultiAddress.
func BuildMultiAddress(ipType string, ipAddress string, protocol MultiAddressProtocol, port int, peerID string) (MultiAddress, error) {
	normalizedIPType, err := normalizeMultiAddressIPType(ipType)
	if err != nil {
		return MultiAddress{}, err
	}
	if !isValidMultiAddressIP(ipAddress, normalizedIPType) {
		return MultiAddress{}, fmt.Errorf("utils: invalid %s address %q", normalizedIPType, ipAddress)
	}

	normalizedProtocol, err := ParseMultiAddressProtocol(string(protocol))
	if err != nil {
		return MultiAddress{}, err
	}
	if err := validateMultiAddressPort(port); err != nil {
		return MultiAddress{}, err
	}
	if err := validateMultiAddressPeerID(peerID); err != nil {
		return MultiAddress{}, err
	}

	address := MultiAddress{
		IPType:    normalizedIPType,
		IPAddress: ipAddress,
		Protocol:  normalizedProtocol,
		Port:      port,
		PeerID:    peerID,
	}
	address.RawAddress = address.ToRawAddress()
	return address, nil
}

// ParseMultiAddressProtocol validates and normalizes a transport protocol.
func ParseMultiAddressProtocol(value string) (MultiAddressProtocol, error) {
	switch strings.ToLower(value) {
	case string(ProtocolTCP):
		return ProtocolTCP, nil
	case string(ProtocolUDP):
		return ProtocolUDP, nil
	case string(ProtocolQUIC):
		return ProtocolQUIC, nil
	default:
		return "", fmt.Errorf("utils: unsupported multi-address protocol %q", value)
	}
}

// ToRawAddress returns the canonical string representation.
func (m MultiAddress) ToRawAddress() string {
	return fmt.Sprintf("/%s/%s/%s/%d/p2p/%s", m.IPType, m.IPAddress, m.Protocol, m.Port, m.PeerID)
}

// String returns the canonical string representation.
func (m MultiAddress) String() string {
	return m.ToRawAddress()
}

func normalizeMultiAddressIPType(value string) (string, error) {
	switch strings.ToLower(value) {
	case MultiAddressIP4:
		return MultiAddressIP4, nil
	case MultiAddressIP6:
		return MultiAddressIP6, nil
	default:
		return "", fmt.Errorf("utils: unsupported multi-address IP type %q", value)
	}
}

func isValidMultiAddressIP(ipAddress string, ipType string) bool {
	ip := net.ParseIP(ipAddress)
	if ip == nil {
		return false
	}
	if ipType == MultiAddressIP4 {
		return ip.To4() != nil
	}
	return ip.To4() == nil && ip.To16() != nil
}

func validateMultiAddressPort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("utils: multi-address port out of range 1-65535: %d", port)
	}
	return nil
}

func validateMultiAddressPeerID(peerID string) error {
	decoded, err := Base58Decode(peerID)
	if err != nil {
		return fmt.Errorf("utils: invalid multi-address peer id: %w", err)
	}
	if len(decoded) != PublicKeySize {
		return fmt.Errorf("utils: multi-address peer id requires %d decoded bytes, got %d", PublicKeySize, len(decoded))
	}
	return nil
}
