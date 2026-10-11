// Package nodeapp owns one local node and the shared Android/Windows panel.
package nodeapp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	genesisprofile "github.com/tentechtop/ordexus-core/config/local"
	"github.com/tentechtop/ordexus-core/internal/posnode"
	"github.com/tentechtop/ordexus-core/rpc"
)

const maxRPCBytes = 8 << 20

type Settings struct {
	BootstrapRPC  string `json:"bootstrap_rpc"`
	Name          string `json:"name"`
	P2PPort       int    `json:"p2p_port"`
	RPCPort       int    `json:"rpc_port"`
	PairingIP     string `json:"pairing_ip"`
	WalletPairing bool   `json:"wallet_pairing"`
	ChainID       string `json:"chain_id,omitempty"`
	ChainIdentity string `json:"chain_identity,omitempty"`
	GenesisHash   string `json:"genesis_hash,omitempty"`
}

type Controller struct {
	mu                               sync.Mutex
	operation                        sync.Mutex
	root, platform, state, lastError string
	settings                         Settings
	started                          time.Time
	cancel                           context.CancelFunc
	done                             chan struct{}
	closed                           bool
	lock                             *os.File
	logs                             *rotatingLog
	logger                           *slog.Logger
	client                           *http.Client
	runNode                          func(context.Context, string, *slog.Logger) error
	relay                            *http.Server
	relayURL, relayToken             string
	relayExpires                     time.Time
	panel                            *http.Server
	panelURL, panelToken             string
	exit                             chan struct{}
	exitOnce                         sync.Once
}

func New(root, platform string) (*Controller, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	lock, err := acquireLock(filepath.Join(root, "app.lock"))
	if err != nil {
		return nil, fmt.Errorf("节点管理器已运行，或数据目录无法锁定：%w", err)
	}
	c := &Controller{root: root, platform: platform, state: "stopped", lock: lock,
		exit: make(chan struct{}), runNode: posnode.RunContext,
		client: &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil},
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }},
		settings: Settings{Name: "ordexus-" + platform, P2PPort: 19501, RPCPort: 29201, PairingIP: "127.0.0.1"}}
	if data, readErr := os.ReadFile(filepath.Join(root, "settings.json")); readErr == nil {
		if err = json.Unmarshal(data, &c.settings); err != nil {
			lock.Close()
			return nil, fmt.Errorf("设置文件损坏：%w", err)
		}
	} else if !os.IsNotExist(readErr) {
		lock.Close()
		return nil, readErr
	}
	c.logs, err = newRotatingLog(filepath.Join(root, "logs", "node.log"))
	if err != nil {
		lock.Close()
		return nil, err
	}
	c.logger = slog.New(slog.NewJSONHandler(c.logs, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if platform == "android" {
		runtime.GOMAXPROCS(2)
		debug.SetMemoryLimit(384 << 20)
		debug.SetGCPercent(75)
	}
	return c, nil
}

func randomToken() (string, error) {
	var data [32]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

func (c *Controller) Configure(ctx context.Context, settings Settings) error {
	c.operation.Lock()
	defer c.operation.Unlock()
	c.mu.Lock()
	busy, closed, previous := c.cancel != nil, c.closed, c.settings
	c.mu.Unlock()
	if busy || closed {
		return fmt.Errorf("请先停止节点，再修改网络设置")
	}
	settings.BootstrapRPC = strings.TrimSpace(settings.BootstrapRPC)
	settings.Name = strings.TrimSpace(settings.Name)
	u, err := url.Parse(settings.BootstrapRPC)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("请输入不含凭据的 HTTP(S) 引导 RPC 地址")
	}
	if !regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`).MatchString(settings.Name) {
		return fmt.Errorf("节点名需以小写字母开头，使用字母、数字或连字符，最多 40 字符")
	}
	if settings.RPCPort < 1024 || settings.RPCPort > 65534 || settings.P2PPort < 1024 || settings.P2PPort > 65535 || settings.P2PPort == settings.RPCPort || settings.P2PPort == settings.RPCPort+1 {
		return fmt.Errorf("端口需在 1024–65535 内，P2P 不能占用 RPC 或 RPC+1 配对端口")
	}
	if settings.PairingIP == "" {
		settings.PairingIP = "127.0.0.1"
	}
	if !localAddress(settings.PairingIP) {
		return fmt.Errorf("钱包连接 IP 必须是本机回环或局域网 IPv4 地址")
	}
	configPath := filepath.Join(c.root, "node.json")
	var existing map[string]json.RawMessage
	if data, readErr := os.ReadFile(configPath); readErr == nil {
		if err = json.Unmarshal(data, &existing); err != nil {
			return err
		}
		if string(existing["validator_enabled"]) == "true" {
			return fmt.Errorf("节点已完成验证者绑定；请保留现有身份和配置，当前版本不支持重新配置已绑定节点")
		}
	} else if !os.IsNotExist(readErr) {
		return readErr
	}
	var manifest rpc.BootstrapManifestResult
	if err = c.call(ctx, settings.BootstrapRPC, "getBootstrapManifest", []any{}, &manifest); err != nil {
		return fmt.Errorf("无法读取引导网络清单：%w", err)
	}
	var profile struct {
		PublicKey string `json:"raw_public_key"`
		Supply    string `json:"initial_supply_lamports"`
	}
	if err = json.Unmarshal(genesisprofile.GenesisAccountJSON, &profile); err != nil {
		return err
	}
	if manifest.Genesis.TreasuryAddress != profile.PublicKey || fmt.Sprint(manifest.Genesis.InitialSupplyLamports) != profile.Supply {
		return fmt.Errorf("引导网络的创世公钥或初始发行量与 Ordexus 统一配置不一致")
	}
	if manifest.NetworkParameters == nil || manifest.NetworkParameters.Version != 1 {
		return fmt.Errorf("引导节点尚未提供完整网络参数，请先升级引导节点；不能猜测奖励升级 epoch 和共识门限")
	}
	peers := make([]rpc.BootstrapPeerConfigResult, 0, len(manifest.BootstrapPeers))
	for _, peer := range manifest.BootstrapPeers {
		ip := net.ParseIP(peer.IP)
		if peer.IP == "" || strings.EqualFold(peer.IP, "localhost") || (ip != nil && (ip.IsLoopback() || ip.IsUnspecified())) || peer.Port < 1 || peer.Port > 65535 {
			continue
		}
		if peer.Network == "" {
			peer.Network = "tcp"
		}
		if peer.Network == "tcp" || peer.Network == "quic" {
			peers = append(peers, peer)
		}
	}
	if len(peers) == 0 {
		return fmt.Errorf("引导清单没有可达的 P2P 地址，其他手机或电脑无法加入只有回环 P2P 的网络")
	}
	manifest.BootstrapPeers = peers
	if previous.ChainIdentity != "" && previous.ChainIdentity != manifest.ChainIdentityHash {
		return fmt.Errorf("该数据目录已经绑定另一条链；请保留原账本，使用新的独立数据目录")
	}
	seed := ""
	if raw := existing["peer_seed"]; len(raw) > 0 {
		_ = json.Unmarshal(raw, &seed)
	}
	if seed == "" {
		seed, err = randomToken()
		if err != nil {
			return err
		}
	}
	network := peers[0].Network
	for _, peer := range peers {
		if peer.Network == "tcp" {
			network = "tcp"
			break
		}
	}
	data, err := posnode.PrepareFullNodeDeploymentConfig(manifest, posnode.FullNodeDeploymentOptions{
		Name: settings.Name, DataPath: filepath.Join(c.root, "data"), PeerSeed: seed,
		P2PPort: settings.P2PPort, RPCPort: settings.RPCPort, Network: network,
		MultiValidatorThreshold: manifest.NetworkParameters.MultiValidatorThreshold, WalletPairing: settings.WalletPairing})
	if err != nil {
		return fmt.Errorf("网络清单不兼容：%w", err)
	}
	settings.ChainID, settings.ChainIdentity, settings.GenesisHash = manifest.ChainID, manifest.ChainIdentityHash, manifest.GenesisHash
	oldConfig, oldErr := os.ReadFile(configPath)
	if err = atomicWrite(configPath, data); err != nil {
		return err
	}
	encoded, _ := json.MarshalIndent(settings, "", "  ")
	if err = atomicWrite(filepath.Join(c.root, "settings.json"), encoded); err != nil {
		if oldErr == nil {
			if restoreErr := atomicWrite(configPath, oldConfig); restoreErr != nil {
				return fmt.Errorf("保存设置失败，原配置恢复失败：%w", restoreErr)
			}
		} else {
			_ = os.Remove(configPath)
		}
		return err
	}
	c.mu.Lock()
	c.settings, c.lastError, c.state = settings, "", "stopped"
	c.mu.Unlock()
	c.logger.Info("node application network configured", "chain_id", settings.ChainID)
	return nil
}

func localAddress(address string) bool {
	ip := net.ParseIP(address)
	if ip == nil || ip.To4() == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	if !ip.IsPrivate() {
		return false
	}
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, local := range addresses {
		host, _, _ := net.ParseCIDR(local.String())
		if host.Equal(ip) {
			return true
		}
	}
	return false
}

func atomicWrite(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".ordexus-*.tmp")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temporary, path)
}

func (c *Controller) Start() error {
	c.operation.Lock()
	defer c.operation.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return fmt.Errorf("管理器已经关闭")
	}
	if c.cancel != nil {
		return fmt.Errorf("节点已经启动或正在停止")
	}
	if c.settings.ChainIdentity == "" {
		return fmt.Errorf("请先在网络设置中连接并校验已有网络")
	}
	if _, err := os.Stat(filepath.Join(c.root, "node.json")); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel, c.done, c.state, c.lastError, c.started = cancel, make(chan struct{}), "starting", "", time.Now()
	done := c.done
	go func() {
		err := c.runNode(ctx, filepath.Join(c.root, "node.json"), c.logger)
		c.mu.Lock()
		if err != nil && !errors.Is(err, context.Canceled) {
			c.state, c.lastError = "failed", err.Error()
		} else {
			c.state = "stopped"
		}
		c.cancel = nil
		close(done)
		c.mu.Unlock()
	}()
	return nil
}

func (c *Controller) Stop() error {
	c.operation.Lock()
	defer c.operation.Unlock()
	return c.stop()
}

// stop is called with operation held, so shutdown cannot race a new Start.
func (c *Controller) stop() error {
	c.mu.Lock()
	cancel, done := c.cancel, c.done
	if cancel != nil {
		c.state = "stopping"
		cancel()
	}
	relay := c.relay
	c.relay, c.relayURL, c.relayToken = nil, "", ""
	c.mu.Unlock()
	if relay != nil {
		_ = relay.Close()
	}
	if cancel == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-time.After(15 * time.Second):
		return fmt.Errorf("节点仍在正常关闭，请稍后查看状态；未强制终止或删除账本")
	}
}

func (c *Controller) Running() bool         { c.mu.Lock(); defer c.mu.Unlock(); return c.cancel != nil }
func (c *Controller) Done() <-chan struct{} { return c.exit }

func (c *Controller) Close() error {
	c.operation.Lock()
	defer c.operation.Unlock()
	if err := c.stop(); err != nil {
		return err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	panel := c.panel
	c.mu.Unlock()
	if panel != nil {
		_ = panel.Close()
	}
	c.client.CloseIdleConnections()
	c.logs.Close()
	c.lock.Close()
	c.exitOnce.Do(func() { close(c.exit) })
	return nil
}

func (c *Controller) call(ctx context.Context, endpoint, method string, params any, result any) error {
	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(request)
	if err != nil {
		return fmt.Errorf("RPC 连接失败")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("RPC HTTP 状态 %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxRPCBytes+1))
	if err != nil || len(data) > maxRPCBytes {
		return fmt.Errorf("RPC 响应过大或读取失败")
	}
	var envelope struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      int             `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   *rpc.Error      `json:"error"`
	}
	if err = json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("RPC 响应不是有效 JSON")
	}
	if envelope.Error != nil {
		return fmt.Errorf("RPC: %s", envelope.Error.Message)
	}
	if envelope.JSONRPC != "2.0" || envelope.ID != 1 || len(envelope.Result) == 0 {
		return fmt.Errorf("RPC 响应格式不兼容")
	}
	return json.Unmarshal(envelope.Result, result)
}

type Snapshot struct {
	State          string                     `json:"state"`
	Message        string                     `json:"message"`
	Platform       string                     `json:"platform"`
	Settings       Settings                   `json:"settings"`
	Node           json.RawMessage            `json:"node,omitempty"`
	Health         rpc.HealthResult           `json:"health"`
	Peers          rpc.PeerNetworkResult      `json:"peer_network"`
	Pairing        rpc.ValidatorPairingResult `json:"pairing"`
	UptimeSeconds  int64                      `json:"uptime_seconds"`
	GoHeapBytes    uint64                     `json:"go_heap_bytes"`
	ConnectedPeers int                        `json:"connected_peers"`
	RPCURL         string                     `json:"rpc_url"`
}

func (c *Controller) Snapshot(ctx context.Context) Snapshot {
	c.mu.Lock()
	result := Snapshot{State: c.state, Message: c.lastError, Platform: c.platform, Settings: c.settings,
		RPCURL: fmt.Sprintf("http://127.0.0.1:%d/", c.settings.RPCPort)}
	active, started := c.cancel != nil, c.started
	c.mu.Unlock()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	result.GoHeapBytes = mem.HeapAlloc
	if active {
		result.UptimeSeconds = int64(time.Since(started).Seconds())
	}
	if active && result.State != "stopping" {
		requestCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if err := c.call(requestCtx, result.RPCURL, "getNodeStatus", []any{}, &result.Node); err == nil {
			var identity struct {
				ChainIdentity string `json:"chain_identity_hash"`
			}
			_ = json.Unmarshal(result.Node, &identity)
			if identity.ChainIdentity != result.Settings.ChainIdentity {
				result.State, result.Message = "failed", "RPC 端口的节点链身份与本机配置不一致"
				result.Node = nil
				return result
			}
			_ = c.call(requestCtx, result.RPCURL, "getHealth", []any{}, &result.Health)
			_ = c.call(requestCtx, result.RPCURL, "getPeerNetwork", []any{}, &result.Peers)
			_ = c.call(requestCtx, result.RPCURL, "getValidatorPairing", []any{}, &result.Pairing)
			for _, peer := range result.Peers.Peers {
				if peer.Connected {
					result.ConnectedPeers++
				}
			}
			result.State = "waiting_peers"
			if result.ConnectedPeers > 0 {
				result.State = "syncing"
				if result.Health.OK && result.Health.ChainProgressing {
					result.State = "running"
				}
			}
		} else {
			result.Message = "正在启动本机 RPC，等待节点初始化"
		}
	}
	return result
}

func (c *Controller) Logs() string { return c.logs.Tail(48 << 10) }
