package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tentechtop/ordexus-core/internal/posnode"
	"github.com/tentechtop/ordexus-core/rpc"
)

var nodeNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
var usernamePattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_.-]{0,31}$`)

func validateRequest(r *deployRequest) error {
	r.Host = strings.TrimSpace(r.Host)
	r.User = strings.TrimSpace(r.User)
	r.Name = strings.TrimSpace(r.Name)
	r.BootstrapRPC = strings.TrimSpace(r.BootstrapRPC)
	if net.ParseIP(r.Host) == nil {
		return fmt.Errorf("请输入有效的服务器 IPv4 或 IPv6 地址")
	}
	if !usernamePattern.MatchString(r.User) {
		return fmt.Errorf("SSH 用户名无效")
	}
	if r.Password == "" || len(r.Password) > 4096 || strings.ContainsAny(r.Password, "\x00\r\n") {
		return fmt.Errorf("请输入有效的单行 SSH 密码")
	}
	if !nodeNamePattern.MatchString(r.Name) {
		return fmt.Errorf("节点名称需使用小写字母、数字或连字符，并以字母开头")
	}
	for _, p := range []int{r.SSHPort, r.P2PPort, r.RPCPort} {
		if p < 1 || p > 65535 {
			return fmt.Errorf("端口必须在 1–65535 之间")
		}
	}
	if r.SSHPort == r.P2PPort || r.SSHPort == r.RPCPort || r.P2PPort == r.RPCPort {
		return fmt.Errorf("SSH、P2P 和 RPC 端口必须不同")
	}
	if r.Threshold < 1 || r.Threshold > 128 {
		return fmt.Errorf("共识门限必须在 1–128 之间")
	}
	return validateBootstrapRPC(r.BootstrapRPC)
}

func validateBootstrapRPC(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("请设置已有网络的 HTTP(S) 引导 RPC 地址")
	}
	return nil
}

type deploymentPlan struct {
	Architecture  string                      `json:"architecture"`
	ChainID       string                      `json:"chain_id"`
	ChainIdentity string                      `json:"chain_identity"`
	GenesisHash   string                      `json:"genesis_hash"`
	Network       string                      `json:"network"`
	Directory     string                      `json:"directory"`
	Service       string                      `json:"service"`
	PeerCount     int                         `json:"peer_count"`
	Manifest      rpc.BootstrapManifestResult `json:"-"`
}

func remoteRoot(r deployRequest) string       { return "/opt/ordexus-nodes/" + r.Name }
func serviceName(r deployRequest) string      { return "ordexus-node-" + r.Name + ".service" }
func netAddress(host string, port int) string { return net.JoinHostPort(host, strconv.Itoa(port)) }

func (a *application) inspect(ctx context.Context, request deployRequest) (deploymentPlan, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	connection, err := a.connect(ctx, request)
	if err != nil {
		return deploymentPlan{}, err
	}
	defer connection.client.Close()
	return a.inspectRemote(ctx, connection, request)
}

func (a *application) inspectRemote(ctx context.Context, r *remote, request deployRequest) (deploymentPlan, error) {
	var plan deploymentPlan
	system, err := r.run(ctx, "uname -s", false)
	if err != nil {
		return plan, err
	}
	if system != "Linux" {
		return plan, fmt.Errorf("当前部署器支持 Linux systemd 服务器")
	}
	arch, err := r.run(ctx, "uname -m", false)
	if err != nil {
		return plan, err
	}
	switch arch {
	case "x86_64", "amd64":
		plan.Architecture = "amd64"
	case "aarch64", "arm64":
		plan.Architecture = "arm64"
	default:
		return plan, fmt.Errorf("不支持的服务器架构：%s", arch)
	}
	if _, err := os.Stat(filepath.Join(a.bundle, "bin", "ordexus-node-linux-"+plan.Architecture)); err != nil {
		return plan, fmt.Errorf("部署包缺少 %s 节点二进制", plan.Architecture)
	}
	if _, err := r.run(ctx, "test -d /run/systemd/system && command -v systemctl >/dev/null && command -v curl >/dev/null && command -v sha256sum >/dev/null && command -v ss >/dev/null", false); err != nil {
		return plan, fmt.Errorf("服务器需要 systemd、curl、sha256sum 和 ss：%w", err)
	}
	if _, err := r.run(ctx, "test \"$(id -u)\" = 0", true); err != nil {
		return plan, fmt.Errorf("部署需要 root 或可使用 sudo 的 SSH 用户：%w", err)
	}
	if _, err := r.run(ctx, "test ! -e "+shellQuote(remoteRoot(request))+" && test ! -e "+shellQuote("/etc/systemd/system/"+serviceName(request)), true); err != nil {
		return plan, fmt.Errorf("同名节点目录或服务已经存在，请选择其他节点名称；现有账本不会被覆盖")
	}
	ports, err := r.run(ctx, "ss -H -lntu", false)
	if err != nil {
		return plan, err
	}
	for _, line := range strings.Split(ports, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		address := fields[4]
		for _, port := range []int{request.P2PPort, request.RPCPort} {
			if strings.HasSuffix(address, ":"+strconv.Itoa(port)) {
				return plan, fmt.Errorf("服务器端口 %d 已被占用", port)
			}
		}
	}
	manifest, err := fetchManifest(ctx, r, request.BootstrapRPC)
	if err != nil {
		return plan, err
	}
	supply, err := strconv.ParseUint(a.profile.Supply, 10, 64)
	if err != nil {
		return plan, fmt.Errorf("部署包创世发行量无效")
	}
	if manifest.Genesis.TreasuryAddress != a.profile.PublicKey || manifest.Genesis.InitialSupplyLamports != supply {
		return plan, fmt.Errorf("已有网络的创世账户或初始发行量与统一配置不一致，已停止部署")
	}
	if !manifest.Ready {
		return plan, fmt.Errorf("已有网络的创世清单尚未就绪")
	}
	peers := make([]rpc.BootstrapPeerConfigResult, 0, len(manifest.BootstrapPeers))
	for _, peer := range manifest.BootstrapPeers {
		ip := net.ParseIP(peer.IP)
		if peer.IP == "" || peer.IP == "localhost" || (ip != nil && (ip.IsLoopback() || ip.IsUnspecified())) {
			continue
		}
		if peer.Port < 1 || peer.Port > 65535 {
			continue
		}
		if peer.Network == "" {
			peer.Network = "tcp"
		}
		if peer.Network != "tcp" && peer.Network != "quic" {
			continue
		}
		peers = append(peers, peer)
	}
	if len(peers) == 0 {
		return plan, fmt.Errorf("清单中的 P2P 地址只有回环地址或无效地址，服务器无法加入；请设置服务器可达的引导节点")
	}
	manifest.BootstrapPeers = peers
	plan.Network = peers[0].Network
	for _, peer := range peers {
		if peer.Network == "tcp" {
			plan.Network = "tcp"
			break
		}
	}
	_, err = posnode.PrepareFullNodeDeploymentConfig(manifest, posnode.FullNodeDeploymentOptions{
		Name: request.Name, DataPath: remoteRoot(request) + "/data", AdvertisedIP: request.Host, PeerSeed: randomID(),
		P2PPort: request.P2PPort, RPCPort: request.RPCPort, MultiValidatorThreshold: request.Threshold, Network: plan.Network, PublicRPC: request.PublicRPC})
	if err != nil {
		return plan, fmt.Errorf("网络配置不兼容：%w", err)
	}
	plan.ChainID = manifest.ChainID
	plan.ChainIdentity = manifest.ChainIdentityHash
	plan.GenesisHash = manifest.GenesisHash
	plan.Directory = remoteRoot(request)
	plan.Service = serviceName(request)
	plan.PeerCount = len(peers)
	plan.Manifest = manifest
	return plan, nil
}

func fetchManifest(ctx context.Context, r *remote, endpoint string) (rpc.BootstrapManifestResult, error) {
	var response struct {
		Result rpc.BootstrapManifestResult `json:"result"`
		Error  *rpc.Error                  `json:"error"`
	}
	command := "curl --silent --show-error --fail --max-time 15 --noproxy '*' -H 'Content-Type: application/json' --data " + shellQuote(`{"jsonrpc":"2.0","id":1,"method":"getBootstrapManifest","params":[]}`) + " " + shellQuote(endpoint)
	data, err := r.run(ctx, command, false)
	if err != nil {
		return response.Result, fmt.Errorf("服务器无法访问引导 RPC：%w", err)
	}
	if err := json.Unmarshal([]byte(data), &response); err != nil {
		return response.Result, fmt.Errorf("引导 RPC 返回了无效的清单")
	}
	if response.Error != nil {
		return response.Result, fmt.Errorf("引导 RPC 不支持当前网络清单或返回错误：%s", response.Error.Message)
	}
	return response.Result, nil
}
