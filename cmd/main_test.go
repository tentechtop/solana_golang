package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	appconfig "solana_golang/config"
	"solana_golang/database"
	"solana_golang/p2p"
	"solana_golang/utils"
)

type closeRecorder struct {
	closed bool
}

func (recorder *closeRecorder) Close() error {
	recorder.closed = true
	return nil
}
func TestConfigPathFromFlagUsesFlag(t *testing.T) {
	restoreFlags := replaceCommandLine([]string{"cmd", "-config", "config/prod/config.yaml"})
	defer restoreFlags()

	if got := configPathFromFlag(); got != "config/prod/config.yaml" {
		t.Fatalf("configPathFromFlag() = %q, want flag path", got)
	}
}
func TestConfigPathFromFlagUsesEnvironment(t *testing.T) {
	restoreFlags := replaceCommandLine([]string{"cmd"})
	defer restoreFlags()
	t.Setenv("APP_CONFIG", "config/stage/config.yaml")

	if got := configPathFromFlag(); got != "config/stage/config.yaml" {
		t.Fatalf("configPathFromFlag() = %q, want env path", got)
	}
}
func TestConfigPathFromFlagUsesDefault(t *testing.T) {
	restoreFlags := replaceCommandLine([]string{"cmd"})
	defer restoreFlags()
	t.Setenv("APP_CONFIG", "")

	if got := configPathFromFlag(); got != appconfig.DefaultPath {
		t.Fatalf("configPathFromFlag() = %q, want default path", got)
	}
}
func TestNewConfiguredLoggerWritesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	logger, closer, err := newConfiguredLogger(appconfig.LogConfig{
		Level:    "info",
		Format:   utils.LogFormatJSON,
		Output:   utils.LogOutputFile,
		FilePath: path,
	})
	if err != nil {
		t.Fatalf("newConfiguredLogger() error = %v", err)
	}
	defer closer.Close()

	logger.Info("file logger ready")
	if closer == nil {
		t.Fatal("closer is nil for file logger")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(data), "file logger ready") {
		t.Fatalf("log file = %q, want message", string(data))
	}
}
func TestNewConfiguredLoggerRejectsBadConfig(t *testing.T) {
	_, _, err := newConfiguredLogger(appconfig.LogConfig{
		Format: "bad",
		Output: utils.LogOutputConsole,
	})
	if err == nil {
		t.Fatal("newConfiguredLogger() error = nil, want format error")
	}
}
func TestRunReturnsConfigError(t *testing.T) {
	restoreFlags := replaceCommandLine([]string{"cmd", "-config", filepath.Join(t.TempDir(), "missing.yaml")})
	defer restoreFlags()

	if err := run(); err == nil {
		t.Fatal("run() error = nil, want config error")
	}
}
func TestNodeModeFromConfigReadsJSONMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bootstrap.json")
	if err := os.WriteFile(path, []byte(`{"node_mode":"bootstrapnode"}`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	mode, err := nodeModeFromConfig(path)
	if err != nil {
		t.Fatalf("nodeModeFromConfig() error = %v", err)
	}
	if mode != "bootstrapnode" {
		t.Fatalf("nodeModeFromConfig() = %q, want bootstrapnode", mode)
	}
}
func TestNodeModeFromConfigReadsYAMLMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pos.yaml")
	if err := os.WriteFile(path, []byte("node_mode: POSNODE\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	mode, err := nodeModeFromConfig(path)
	if err != nil {
		t.Fatalf("nodeModeFromConfig() error = %v", err)
	}
	if mode != "posnode" {
		t.Fatalf("nodeModeFromConfig() = %q, want posnode", mode)
	}
}
func TestNodeModeFromConfigRejectsMissingMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.yaml")
	if err := os.WriteFile(path, []byte("rpc:\n  address: ':8899'\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := nodeModeFromConfig(path); err == nil {
		t.Fatal("nodeModeFromConfig() error = nil, want missing node_mode error")
	}
}
func TestRunReturnsServerError(t *testing.T) {
	configPath := writeRuntimeConfig(t, "bad address", freeTCPPort(t), filepath.Join(t.TempDir(), "db"))
	restoreFlags := replaceCommandLine([]string{"cmd", "-config", configPath})
	defer restoreFlags()

	if err := run(); err == nil {
		t.Fatal("run() error = nil, want server error")
	}
}
func TestStartRuntimeAndClose(t *testing.T) {
	config := appconfig.Default()
	config.Database.Path = filepath.Join(t.TempDir(), "db")
	config.RPC.Address = "127.0.0.1:0"
	config.P2P.ListenIP = "127.0.0.1"
	config.P2P.ListenPort = freeTCPPort(t)

	resources, err := startRuntime(config)
	if err != nil {
		t.Fatalf("startRuntime() error = %v", err)
	}
	if err := resources.close(); err != nil {
		t.Fatalf("close() error = %v", err)
	}
	resources.closeLog()
}
func TestEnsureNodeIdentityCreatesAndPersistsKeyPair(t *testing.T) {
	databaseInstance := openTestDatabase(t)
	defer databaseInstance.Close()
	resources := &runtimeResources{
		logger:   discardLogger(),
		database: databaseInstance,
	}

	identity, err := resources.ensureNodeIdentity()
	if err != nil {
		t.Fatalf("ensureNodeIdentity() error = %v", err)
	}
	if len(identity.PublicKey) != utils.Ed25519KeySize {
		t.Fatalf("PublicKey length = %d, want %d", len(identity.PublicKey), utils.Ed25519KeySize)
	}
	if len(identity.PrivateKey) != utils.Ed25519KeySize {
		t.Fatalf("PrivateKey length = %d, want %d", len(identity.PrivateKey), utils.Ed25519KeySize)
	}

	publicKey, err := databaseInstance.Get(database.TablePeer, nodeIdentityPublicKey)
	if err != nil {
		t.Fatalf("Get(public key) error = %v", err)
	}
	privateKey, err := databaseInstance.Get(database.TablePeer, nodeIdentityPrivateKey)
	if err != nil {
		t.Fatalf("Get(private key) error = %v", err)
	}
	if !utils.Ed25519Verify(publicKey, []byte("probe"), mustSign(t, privateKey, []byte("probe"))) {
		t.Fatal("persisted node key pair failed sign verification")
	}
}
func TestEnsureNodeIdentityReusesStoredKeyPair(t *testing.T) {
	databaseInstance := openTestDatabase(t)
	defer databaseInstance.Close()
	resources := &runtimeResources{
		logger:   discardLogger(),
		database: databaseInstance,
	}

	firstIdentity, err := resources.ensureNodeIdentity()
	if err != nil {
		t.Fatalf("ensureNodeIdentity(first) error = %v", err)
	}
	secondIdentity, err := resources.ensureNodeIdentity()
	if err != nil {
		t.Fatalf("ensureNodeIdentity(second) error = %v", err)
	}
	if secondIdentity.PeerID != firstIdentity.PeerID {
		t.Fatalf("PeerID = %q, want %q", secondIdentity.PeerID, firstIdentity.PeerID)
	}
}
func TestStartP2PRejectsInvalidConfig(t *testing.T) {
	resources := &runtimeResources{
		logger:       discardLogger(),
		serverErrors: make(chan error, 1),
	}
	config := appconfig.Default().P2P
	identity := testNodeIdentity(t)
	identity.PeerID = "bad"

	if err := resources.startP2P(config, identity); err == nil {
		t.Fatal("startP2P() error = nil, want peer id error")
	}
}
func TestStartP2PRejectsInvalidAddress(t *testing.T) {
	resources := &runtimeResources{
		logger:       discardLogger(),
		serverErrors: make(chan error, 1),
	}
	config := appconfig.Default().P2P
	config.ListenIP = "bad-ip"

	if err := resources.startP2P(config, testNodeIdentity(t)); err == nil {
		t.Fatal("startP2P() error = nil, want listen address error")
	}
}
func TestStartRPCReportsListenError(t *testing.T) {
	resources := &runtimeResources{
		logger:       discardLogger(),
		serverErrors: make(chan error, 1),
	}
	resources.startRPC(appconfig.RPCConfig{
		Address:      "bad address",
		MaxBodyBytes: 1024,
		MaxBatchSize: 1,
	})

	select {
	case err := <-resources.serverErrors:
		if err == nil {
			t.Fatal("server error is nil")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for rpc listen error")
	}
}
func TestStartRuntimeReturnsDatabaseError(t *testing.T) {
	config := appconfig.Default()
	config.Database.Path = ""

	if _, err := startRuntime(config); err == nil {
		t.Fatal("startRuntime() error = nil, want database error")
	}
}
func TestStartRuntimeReturnsP2PError(t *testing.T) {
	config := appconfig.Default()
	config.Database.Path = filepath.Join(t.TempDir(), "db")
	config.P2P.ListenIP = "bad-ip"

	if _, err := startRuntime(config); err == nil {
		t.Fatal("startRuntime() error = nil, want p2p error")
	}
}
func TestNewConfiguredLoggerReturnsFileOpenError(t *testing.T) {
	_, _, err := newConfiguredLogger(appconfig.LogConfig{
		Level:    "info",
		Format:   utils.LogFormatJSON,
		Output:   utils.LogOutputFile,
		FilePath: string([]byte{0}),
	})
	if err == nil {
		t.Fatal("newConfiguredLogger() error = nil, want file path error")
	}
}
func TestHandleP2PConnectionStopsOnReadError(t *testing.T) {
	resources := &runtimeResources{logger: discardLogger()}
	connection := &failingConnection{}

	resources.handleP2PConnection(context.Background(), connection)

	if !connection.closed {
		t.Fatal("connection was not closed")
	}
}
func TestHandleP2PConnectionRejectsUnknownProtocol(t *testing.T) {
	host, err := p2p.NewHost(p2p.HostConfig{
		PeerID:        testNodeIdentity(t).PeerID,
		AllowInsecure: true,
		Logger:        discardLogger(),
	})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	message, err := p2p.NewMessage(p2p.ProtocolPingV1, []byte("payload"))
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	message.Type = p2p.ProtocolID(9999)
	connection := &messageThenFailConnection{message: message}
	resources := &runtimeResources{
		logger:  discardLogger(),
		p2pHost: host,
	}

	resources.handleP2PConnection(context.Background(), connection)

	if !connection.closed {
		t.Fatal("connection was not closed")
	}
}
func TestWaitForStopReturnsServerError(t *testing.T) {
	want := errors.New("server failed")
	resources := &runtimeResources{
		logger:       discardLogger(),
		serverErrors: make(chan error, 1),
	}
	resources.serverErrors <- want

	if err := waitForStop(resources); !errors.Is(err, want) {
		t.Fatalf("waitForStop() error = %v, want %v", err, want)
	}
}
func TestWaitForStopReturnsOnSignal(t *testing.T) {
	resources := &runtimeResources{
		logger:       discardLogger(),
		serverErrors: make(chan error, 1),
	}
	done := make(chan error, 1)
	go func() {
		done <- waitForStop(resources)
	}()

	process, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("FindProcess() error = %v", err)
	}
	if err := process.Signal(os.Interrupt); err != nil {
		resources.serverErrors <- err
		<-done
		t.Skipf("process interrupt signal is not supported: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("waitForStop() error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for signal shutdown")
	}
}
func TestCloseLogClosesOnce(t *testing.T) {
	recorder := &closeRecorder{}
	resources := &runtimeResources{logCloser: recorder}

	resources.closeLog()
	resources.closeLog()

	if !recorder.closed {
		t.Fatal("log closer was not closed")
	}
	if resources.logCloser != nil {
		t.Fatal("log closer was not cleared")
	}
}
func TestErrorsJoin(t *testing.T) {
	first := errors.New("first")
	second := errors.New("second")

	err := errorsJoin([]error{nil, first, second})
	if !errors.Is(err, first) || !errors.Is(err, second) {
		t.Fatalf("errorsJoin() = %v, want wrapped errors", err)
	}
	if errorsJoin(nil) != nil {
		t.Fatal("errorsJoin(nil) != nil")
	}
}
func replaceCommandLine(args []string) func() {
	originalArgs := os.Args
	originalCommandLine := flag.CommandLine
	os.Args = args
	flag.CommandLine = flag.NewFlagSet(args[0], flag.ContinueOnError)
	flag.CommandLine.SetOutput(io.Discard)
	return func() {
		os.Args = originalArgs
		flag.CommandLine = originalCommandLine
	}
}
func freeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}
func openTestDatabase(t *testing.T) database.Database {
	t.Helper()
	databaseInstance, err := database.NewDatabase(database.DatabaseConfig{
		Engine: database.EnginePebble,
		Path:   filepath.Join(t.TempDir(), "db"),
		WAL:    true,
	})
	if err != nil {
		t.Fatalf("NewDatabase() error = %v", err)
	}
	return databaseInstance
}
func mustSign(t *testing.T, privateKey []byte, data []byte) []byte {
	t.Helper()
	signature, err := utils.Ed25519Sign(privateKey, data)
	if err != nil {
		t.Fatalf("Ed25519Sign() error = %v", err)
	}
	return signature
}
func writeRuntimeConfig(t *testing.T, rpcAddress string, p2pPort int, databasePath string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	content := strings.Join([]string{
		"node_mode: \"runtime\"",
		"rpc:",
		"  address: \"" + rpcAddress + "\"",
		"  max_body_bytes: 1048576",
		"  max_batch_size: 32",
		"log:",
		"  level: \"info\"",
		"  format: \"json\"",
		"  add_source: false",
		"  output: \"console\"",
		"database:",
		"  engine: \"pebble\"",
		"  path: \"" + filepath.ToSlash(databasePath) + "\"",
		"  wal: true",
		"p2p:",
		"  ip_type: \"ip4\"",
		"  listen_ip: \"127.0.0.1\"",
		"  listen_port: " + strconv.Itoa(p2pPort),
		"  default_protocol: \"tcp\"",
		"  max_peers: 64",
		"  network_id: \"solana_golang:test:00000000000000000000000000000000\"",
		"  software_version: \"solana_golang/test\"",
		"  min_outbound_peers: 0",
		"  bootstrap_timeout_millis: 1000",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}
func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func testNodeIdentity(t *testing.T) nodeIdentity {
	t.Helper()
	publicKey, privateKey, err := utils.GenerateEd25519KeyPairBytes()
	if err != nil {
		t.Fatalf("GenerateEd25519KeyPairBytes() error = %v", err)
	}
	return nodeIdentity{
		PeerID:     utils.Base58Encode(publicKey),
		PublicKey:  publicKey,
		PrivateKey: privateKey,
	}
}

type failingConnection struct {
	closed bool
}

func (connection *failingConnection) ID() string {
	return "connection-1"
}
func (connection *failingConnection) Protocol() utils.MultiAddressProtocol {
	return utils.ProtocolTCP
}
func (connection *failingConnection) RemotePeerID() string {
	return ""
}
func (connection *failingConnection) LocalAddress() string {
	return "127.0.0.1:1"
}
func (connection *failingConnection) RemoteAddress() string {
	return "127.0.0.1:2"
}
func (connection *failingConnection) ReadMessage(ctx context.Context) (p2p.Message, error) {
	return p2p.Message{}, errors.New("read failed")
}
func (connection *failingConnection) WriteMessage(ctx context.Context, message p2p.Message) error {
	return nil
}
func (connection *failingConnection) Close() error {
	connection.closed = true
	return nil
}

type messageThenFailConnection struct {
	message p2p.Message
	read    bool
	closed  bool
}

func (connection *messageThenFailConnection) ID() string {
	return "connection-2"
}
func (connection *messageThenFailConnection) Protocol() utils.MultiAddressProtocol {
	return utils.ProtocolTCP
}
func (connection *messageThenFailConnection) RemotePeerID() string {
	return ""
}
func (connection *messageThenFailConnection) LocalAddress() string {
	return "127.0.0.1:3"
}
func (connection *messageThenFailConnection) RemoteAddress() string {
	return "127.0.0.1:4"
}
func (connection *messageThenFailConnection) ReadMessage(ctx context.Context) (p2p.Message, error) {
	if connection.read {
		return p2p.Message{}, errors.New("done")
	}
	connection.read = true
	return connection.message, nil
}
func (connection *messageThenFailConnection) WriteMessage(ctx context.Context, message p2p.Message) error {
	return nil
}
func (connection *messageThenFailConnection) Close() error {
	connection.closed = true
	return nil
}
