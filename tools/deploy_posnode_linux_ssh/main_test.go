package main

import (
	"strings"
	"testing"
)

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

func TestBuildStopPublicRPCCommandDoesNotStopPosnode(t *testing.T) {
	command := buildStopPublicRPCCommand()
	if !strings.Contains(command, "rpcnode.service") {
		t.Fatal("stop command should include rpcnode.service")
	}
	if !strings.Contains(command, "public-rpc-8911.service") {
		t.Fatal("stop command should include public-rpc services")
	}
	if !strings.Contains(command, "posnode-public-rpc-8911.service") {
		t.Fatal("stop command should include deployed posnode-public-rpc services")
	}
	if strings.Contains(command, "stop posnode") || strings.Contains(command, "pkill -x posnode") {
		t.Fatalf("stop command must not stop validators: %s", command)
	}
}

func TestParseCLIOptionsEnablesInspectOnly(t *testing.T) {
	options, err := parseCLIOptions([]string{"-inspect"})
	if err != nil {
		t.Fatalf("parseCLIOptions() error = %v", err)
	}
	if !options.inspectOnly {
		t.Fatal("inspectOnly = false, want true")
	}
}

func TestParseCLIOptionsSupportsOperationalSwitches(t *testing.T) {
	options, err := parseCLIOptions([]string{
		"-stop-public-rpc-only",
		"-upload-binary-only",
		"-reset-data",
		"-no-stop-all",
		"-no-stop-public-rpc",
	})
	if err != nil {
		t.Fatalf("parseCLIOptions() error = %v", err)
	}
	if !options.stopPublicRPCOnly || !options.uploadBinaryOnly || !options.resetData || !options.noStopAll || !options.noStopPublicRPC {
		t.Fatalf("options = %#v, want all operational switches enabled", options)
	}
}

func TestParseCLIOptionsRejectsUnknownArgs(t *testing.T) {
	_, err := parseCLIOptions([]string{"-unknown"})
	if err == nil {
		t.Fatal("parseCLIOptions() error = nil, want rejection")
	}
}

func TestParseServiceNamesRejectsShellInput(t *testing.T) {
	_, err := parseServiceNames("posnode.service;rm -rf /")
	if err == nil {
		t.Fatal("parseServiceNames() error = nil, want rejection")
	}
}

func TestParseServiceNamesTrimsEmptyItems(t *testing.T) {
	serviceNames, err := parseServiceNames(" posnode-bootstrap.service, ,posnode-public-validator.service ")
	if err != nil {
		t.Fatalf("parseServiceNames() error = %v", err)
	}
	if len(serviceNames) != 2 {
		t.Fatalf("service names length = %d, want 2", len(serviceNames))
	}
	if serviceNames[0] != "posnode-bootstrap.service" || serviceNames[1] != "posnode-public-validator.service" {
		t.Fatalf("service names = %#v", serviceNames)
	}
}
