package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/tentechtop/ordexus-core/internal/posnode"
)

var stagingPattern = regexp.MustCompile(`^/tmp/ordexus-deploy\.[a-zA-Z0-9]{6,}$`)

type deploymentResult struct {
	ChainID      string `json:"chain_id"`
	Service      string `json:"service"`
	Directory    string `json:"directory"`
	Architecture string `json:"architecture"`
	State        string `json:"state"`
	Head         uint64 `json:"head"`
	Peers        int    `json:"peers"`
	RPC          string `json:"rpc"`
	P2P          string `json:"p2p"`
	BinarySHA256 string `json:"binary_sha256"`
}

func (a *application) perform(request deployRequest, log func(string)) (result deploymentResult, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	log("1/6 连接 SSH，重新检查服务器、端口和已有网络…")
	r, err := a.connect(ctx, request)
	if err != nil {
		return result, err
	}
	defer r.client.Close()
	plan, err := a.inspectRemote(ctx, r, request)
	if err != nil {
		return result, err
	}
	if plan.ChainIdentity != request.ExpectedChainIdentity {
		return result, fmt.Errorf("目标网络身份已变化，请重新检查服务器")
	}
	config, err := posnode.PrepareFullNodeDeploymentConfig(plan.Manifest, posnode.FullNodeDeploymentOptions{
		Name: request.Name, DataPath: plan.Directory + "/data", AdvertisedIP: request.Host, PeerSeed: randomID(),
		P2PPort: request.P2PPort, RPCPort: request.RPCPort, MultiValidatorThreshold: request.Threshold, Network: plan.Network, PublicRPC: request.PublicRPC})
	if err != nil {
		return result, err
	}
	staging, err := r.run(ctx, "mktemp -d /tmp/ordexus-deploy.XXXXXXXXXX", false)
	if err != nil {
		return result, err
	}
	if !stagingPattern.MatchString(staging) {
		return result, fmt.Errorf("服务器临时目录无效")
	}
	defer func() {
		_, _ = r.run(context.Background(), "rm -f -- "+shellQuote(staging+"/node")+" "+shellQuote(staging+"/config.json")+" "+shellQuote(staging+"/unit")+"; rmdir -- "+shellQuote(staging), false)
	}()
	log("2/6 上传节点和配置，并核对 SHA-256…")
	file, err := os.Open(filepath.Join(a.bundle, "bin", "ordexus-node-linux-"+plan.Architecture))
	if err != nil {
		return result, err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return result, err
	}
	digest := hex.EncodeToString(hasher.Sum(nil))
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return result, err
	}
	if err := r.upload(ctx, staging+"/node", file); err != nil {
		return result, err
	}
	if err := r.upload(ctx, staging+"/config.json", bytes.NewReader(config)); err != nil {
		return result, err
	}
	unit := serviceUnit(plan, request)
	if err := r.upload(ctx, staging+"/unit", strings.NewReader(unit)); err != nil {
		return result, err
	}
	for name, expected := range map[string]string{"node": digest, "config.json": fmt.Sprintf("%x", sha256.Sum256(config)), "unit": fmt.Sprintf("%x", sha256.Sum256([]byte(unit)))} {
		actual, err := r.run(ctx, "sha256sum "+shellQuote(staging+"/"+name), false)
		if err != nil {
			return result, err
		}
		fields := strings.Fields(actual)
		if len(fields) == 0 || fields[0] != expected {
			return result, fmt.Errorf("上传文件 %s 的校验和不一致", name)
		}
	}
	log("3/6 安装独立节点目录和 systemd 服务…")
	install := installCommand(staging, plan)
	if _, err := r.run(ctx, install, true); err != nil {
		return result, err
	}
	started := false
	defer func() {
		if err != nil && started {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cleanupCancel()
			_, _ = r.run(cleanupCtx, "systemctl disable --now "+shellQuote(plan.Service), true)
			log("本次节点已停止；配置、账本和日志保留在 " + plan.Directory + "，其他服务未停止。")
		}
	}()
	if request.OpenP2PFirewall {
		log("4/6 为该节点放行所选 P2P 端口…")
		if _, err := r.run(ctx, firewallCommand(request, plan.Network), true); err != nil {
			return result, err
		}
	} else {
		log("4/6 保留服务器防火墙规则；请确保 P2P 端口可达。")
	}
	log("5/6 启用服务器开机启动，启动节点…")
	started = true
	if _, err := r.run(ctx, "systemctl daemon-reload && systemctl enable --now "+shellQuote(plan.Service), true); err != nil {
		return result, err
	}
	log("6/6 检查服务和链身份，等待 P2P 连接…")
	var status struct {
		ChainID  string `json:"chain_id"`
		Identity string `json:"chain_identity_hash"`
		Head     uint64 `json:"head_height"`
		Peers    int    `json:"known_peer_count"`
	}
	rpcReady := false
	for attempt := 0; attempt < 10; attempt++ {
		if _, err := r.run(ctx, "systemctl is-active --quiet "+shellQuote(plan.Service), true); err != nil {
			return result, fmt.Errorf("节点服务没有保持运行，请查看 journalctl -u %s", plan.Service)
		}
		command := "curl --silent --show-error --fail --max-time 3 --noproxy '*' -H 'Content-Type: application/json' --data " + shellQuote(`{"jsonrpc":"2.0","id":1,"method":"getNodeStatus","params":[]}`) + " " + shellQuote(fmt.Sprintf("http://127.0.0.1:%d/", request.RPCPort))
		data, readErr := r.run(ctx, command, false)
		if readErr == nil {
			var response struct {
				Result json.RawMessage `json:"result"`
			}
			if json.Unmarshal([]byte(data), &response) == nil && json.Unmarshal(response.Result, &status) == nil && status.ChainID != "" {
				if status.ChainID != plan.ChainID || status.Identity != plan.ChainIdentity {
					return result, fmt.Errorf("节点返回的链身份与已检查网络不一致")
				}
				rpcReady = true
				if status.Peers > 0 {
					break
				}
			}
		}
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	if !rpcReady {
		return result, fmt.Errorf("节点 RPC 未就绪，请查看服务器节点日志")
	}
	rpcAddress := fmt.Sprintf("http://127.0.0.1:%d/（通过 SSH 隧道访问）", request.RPCPort)
	if request.PublicRPC {
		rpcAddress = "http://" + netAddress(request.Host, request.RPCPort) + "/"
	}
	state := "已部署，正在同步"
	if status.Peers == 0 {
		state = "已部署，等待 P2P 连接"
		log("尚未连接其他节点，请检查 P2P 地址、防火墙和云安全组。")
	}
	result = deploymentResult{ChainID: plan.ChainID, Service: plan.Service, Directory: plan.Directory, Architecture: plan.Architecture, State: state, Head: status.Head, Peers: status.Peers, RPC: rpcAddress, P2P: netAddress(request.Host, request.P2PPort) + "/" + plan.Network, BinarySHA256: digest}
	log("部署完成；服务器已启用节点开机启动。")
	return result, nil
}

func installCommand(staging string, plan deploymentPlan) string {
	root := shellQuote(plan.Directory)
	return "set -eu\n" +
		"getent passwd ordexus-node >/dev/null || useradd --system --no-create-home --shell /usr/sbin/nologin ordexus-node\n" +
		"install -d -m 0755 /opt/ordexus-nodes\n" +
		"mkdir -m 0750 " + root + "\n" +
		"install -d -m 0750 " + shellQuote(plan.Directory+"/bin") + " " + shellQuote(plan.Directory+"/config") + " " + shellQuote(plan.Directory+"/data") + " " + shellQuote(plan.Directory+"/logs") + "\n" +
		"install -m 0755 " + shellQuote(staging+"/node") + " " + shellQuote(plan.Directory+"/bin/ordexus-node") + "\n" +
		"install -m 0600 " + shellQuote(staging+"/config.json") + " " + shellQuote(plan.Directory+"/config/node.json") + "\n" +
		"chown -R ordexus-node " + root + "\n" +
		"install -m 0644 " + shellQuote(staging+"/unit") + " " + shellQuote("/etc/systemd/system/"+plan.Service)
}

func serviceUnit(plan deploymentPlan, r deployRequest) string {
	return fmt.Sprintf(`[Unit]
Description=Ordexus full node %s
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=ordexus-node
WorkingDirectory=%s
ExecStart=%s/bin/ordexus-node -config %s/config/node.json
Restart=on-failure
RestartSec=5
TimeoutStopSec=45
LimitNOFILE=65536
UMask=0077
Environment=GOMAXPROCS=2 GOMEMLIMIT=512MiB GOGC=75
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=%s/data %s/logs
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
`, r.Name, plan.Directory, plan.Directory, plan.Directory, plan.Directory, plan.Directory)
}

func firewallCommand(r deployRequest, network string) string {
	proto := "tcp"
	if network == "quic" {
		proto = "udp"
	}
	ports := []string{fmt.Sprintf("%d/%s", r.P2PPort, proto)}
	if r.PublicRPC {
		ports = append(ports, fmt.Sprintf("%d/tcp", r.RPCPort))
	}
	var firewalld, ufw []string
	for _, port := range ports {
		firewalld = append(firewalld, "firewall-cmd --permanent --add-port="+shellQuote(port))
		ufw = append(ufw, "ufw allow "+shellQuote(port))
	}
	return "set -e; if command -v firewall-cmd >/dev/null && systemctl is-active --quiet firewalld; then " + strings.Join(firewalld, " && ") + " && firewall-cmd --reload; elif command -v ufw >/dev/null && ufw status | grep -q '^Status: active'; then " + strings.Join(ufw, " && ") + "; else echo '未检测到启用的 firewalld/ufw，请检查云安全组及现有防火墙'; fi"
}
