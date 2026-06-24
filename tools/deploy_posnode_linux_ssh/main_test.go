package main

import "testing"

func TestParseFirewallPortsAcceptsTCPAndUDP(t *testing.T) {
	ports, err := parseFirewallPorts("8910/tcp,5210/tcp,5210/udp")
	if err != nil {
		t.Fatalf("parseFirewallPorts() error = %v", err)
	}
	expected := []string{"8910/tcp", "5210/tcp", "5210/udp"}
	if len(ports) != len(expected) {
		t.Fatalf("ports length = %d, want %d", len(ports), len(expected))
	}
	for index := range expected {
		if ports[index] != expected[index] {
			t.Fatalf("ports[%d] = %q, want %q", index, ports[index], expected[index])
		}
	}
}

func TestParseFirewallPortsRejectsShellInput(t *testing.T) {
	_, err := parseFirewallPorts("8910/tcp;rm -rf /")
	if err == nil {
		t.Fatal("parseFirewallPorts() error = nil, want rejection")
	}
}
