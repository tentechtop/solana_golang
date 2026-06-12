package main

import (
	"reflect"
	"testing"

	"solana_golang/utils"
)

func TestParsePreferredProtocolsUsesFallback(t *testing.T) {
	protocols, err := parsePreferredProtocols("", utils.ProtocolQUIC)
	if err != nil {
		t.Fatalf("parsePreferredProtocols() error = %v", err)
	}
	expected := []utils.MultiAddressProtocol{utils.ProtocolQUIC}
	if !reflect.DeepEqual(protocols, expected) {
		t.Fatalf("parsePreferredProtocols() = %v, want %v", protocols, expected)
	}
}

func TestParsePreferredProtocolsParsesMixedOrder(t *testing.T) {
	protocols, err := parsePreferredProtocols("quic,tcp", utils.ProtocolQUIC)
	if err != nil {
		t.Fatalf("parsePreferredProtocols() error = %v", err)
	}
	expected := []utils.MultiAddressProtocol{utils.ProtocolQUIC, utils.ProtocolTCP}
	if !reflect.DeepEqual(protocols, expected) {
		t.Fatalf("parsePreferredProtocols() = %v, want %v", protocols, expected)
	}
}

func TestParsePreferredProtocolsDeduplicates(t *testing.T) {
	protocols, err := parsePreferredProtocols("quic,tcp,quic", utils.ProtocolTCP)
	if err != nil {
		t.Fatalf("parsePreferredProtocols() error = %v", err)
	}
	expected := []utils.MultiAddressProtocol{utils.ProtocolQUIC, utils.ProtocolTCP}
	if !reflect.DeepEqual(protocols, expected) {
		t.Fatalf("parsePreferredProtocols() = %v, want %v", protocols, expected)
	}
}

func TestParsePreferredProtocolsRejectsInvalidProtocol(t *testing.T) {
	if _, err := parsePreferredProtocols("quic,udp", utils.ProtocolQUIC); err == nil {
		t.Fatal("parsePreferredProtocols() error = nil, want invalid protocol error")
	}
}
