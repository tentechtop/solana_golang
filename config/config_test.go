package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"solana_golang/database"
	"solana_golang/utils"
)

func TestLoadLocalConfig(t *testing.T) {
	config, err := Load(filepath.Join("local", "config.yaml"))
	if err != nil {
		t.Fatalf("Load(local) error = %v", err)
	}
	if config.Database.Engine != string(database.EnginePebble) {
		t.Fatalf("Database.Engine = %q, want pebble", config.Database.Engine)
	}
	if config.Log.Output != utils.LogOutputConsole {
		t.Fatalf("Log.Output = %q, want console", config.Log.Output)
	}
	if config.P2P.Protocol() != utils.ProtocolTCP {
		t.Fatalf("P2P.Protocol() = %q, want tcp", config.P2P.Protocol())
	}
}
func TestLoadAbsoluteConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	source, err := os.ReadFile(filepath.Join("local", "config.yaml"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if err := os.WriteFile(path, source, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := Load(path); err != nil {
		t.Fatalf("Load(abs) error = %v", err)
	}
}
func TestLoadRejectsUnknownField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := []byte("rpc:\n  address: ':8899'\n  unknown: true\n")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("Load() error = nil, want unknown field error")
	}
}
func TestLoadUsesDefaultPathForEmptyInput(t *testing.T) {
	config, err := Load(" ")
	if err != nil {
		t.Fatalf("Load(empty) error = %v", err)
	}
	if config.RPC.Address == "" {
		t.Fatal("RPC.Address is empty")
	}
}
func TestLoadRejectsInvalidYaml(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("rpc:\n  address: ["), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("Load() error = nil, want yaml error")
	}
}
func TestLoadRejectsMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err == nil {
		t.Fatal("Load() error = nil, want missing file error")
	}
}
func TestLogFileOutputRequiresPath(t *testing.T) {
	config := Default()
	config.Log.Output = utils.LogOutputFile
	config.Log.FilePath = ""

	if err := config.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want file path error")
	}
}
func TestLogFileOutputWithPathIsValid(t *testing.T) {
	config := Default()
	config.Log.Output = utils.LogOutputFile
	config.Log.FilePath = "./logs/app.log"

	if err := config.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}
func TestLogEmptyOutputUsesConsoleDefault(t *testing.T) {
	config := Default()
	config.Log.Output = ""

	if err := config.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}
func TestP2PRejectsInvalidProtocol(t *testing.T) {
	config := Default()
	config.P2P.DefaultProtocol = "udp"

	if err := config.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want protocol error")
	}
}
func TestValidateRejectsInvalidRPC(t *testing.T) {
	config := Default()
	config.RPC.Address = ""

	if err := config.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want rpc address error")
	}
}
func TestValidateRejectsInvalidRPCLimits(t *testing.T) {
	config := Default()
	config.RPC.MaxBodyBytes = 0
	if err := config.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want max body error")
	}

	config = Default()
	config.RPC.MaxBatchSize = 0
	if err := config.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want max batch error")
	}
}
func TestValidateRejectsInvalidLogFormat(t *testing.T) {
	config := Default()
	config.Log.Format = "plain"

	if err := config.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want log format error")
	}
}
func TestValidateRejectsInvalidLogOutput(t *testing.T) {
	config := Default()
	config.Log.Output = "network"

	if err := config.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want log output error")
	}
}
func TestValidateRejectsInvalidDatabase(t *testing.T) {
	config := Default()
	config.Database.Engine = "badger"

	if err := config.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want database engine error")
	}
}
func TestValidateRejectsEmptyDatabasePath(t *testing.T) {
	config := Default()
	config.Database.Path = ""

	if err := config.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want database path error")
	}
}
func TestValidateRejectsInvalidP2PPort(t *testing.T) {
	config := Default()
	config.P2P.ListenPort = 70000

	if err := config.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want p2p port error")
	}
}
func TestValidateRejectsInvalidP2PMaxPeers(t *testing.T) {
	config := Default()
	config.P2P.MaxPeers = 0

	if err := config.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want max peers error")
	}
}
func TestValidateRejectsInvalidP2PAddress(t *testing.T) {
	config := Default()
	config.P2P.ListenIP = "bad-ip"

	if err := config.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want listen ip error")
	}
}
func TestProtocolFallsBackForInvalidValue(t *testing.T) {
	config := Default()
	config.P2P.DefaultProtocol = "udp"

	if protocol := config.P2P.Protocol(); protocol != utils.ProtocolTCP {
		t.Fatalf("Protocol() = %q, want tcp fallback", protocol)
	}
}
func TestDatabaseOptionsNormalizesEngine(t *testing.T) {
	config := DatabaseConfig{
		Engine: strings.ToUpper(string(database.EngineLevelDB)),
		Path:   "./data/leveldb",
		WAL:    true,
	}

	options := config.DatabaseOptions()
	if options.Engine != database.EngineLevelDB {
		t.Fatalf("Engine = %q, want leveldb", options.Engine)
	}
}
