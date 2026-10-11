package nodeapp

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/skip2/go-qrcode"
)

type WalletRequest struct {
	Payload string `json:"payload"`
	Image   string `json:"image"`
	Expires int64  `json:"expires_at_unix_millis"`
}

func walletQR(prefix string, payload map[string]any) (WalletRequest, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return WalletRequest{}, err
	}
	text := prefix + base64.RawStdEncoding.EncodeToString(data)
	image, err := qrcode.Encode(text, qrcode.Medium, 512)
	if err != nil {
		return WalletRequest{}, err
	}
	expires, _ := payload["expires_at_unix_millis"].(float64)
	return WalletRequest{Payload: text, Image: "data:image/png;base64," + base64.StdEncoding.EncodeToString(image), Expires: int64(expires)}, nil
}

func (c *Controller) Pairing(ctx context.Context) (WalletRequest, error) {
	c.operation.Lock()
	defer c.operation.Unlock()
	snapshot := c.Snapshot(ctx)
	if snapshot.State != "running" {
		return WalletRequest{}, fmt.Errorf("请先连接网络并完成同步，再申请验证者授权")
	}
	if !snapshot.Pairing.Enabled || snapshot.Pairing.State != "pending" {
		return WalletRequest{}, fmt.Errorf("请停止节点，在网络设置中开启钱包授权，然后重新启动；已绑定节点无需再次授权")
	}
	data, err := os.ReadFile(filepath.Join(c.root, "data", "validator-pairing", "pairing-qr.html"))
	if err != nil {
		return WalletRequest{}, err
	}
	match := regexp.MustCompile(`posvalpair:([A-Za-z0-9+/=_-]+)`).FindSubmatch(data)
	if len(match) != 2 {
		return WalletRequest{}, fmt.Errorf("节点尚未生成钱包授权二维码")
	}
	decoded, err := base64.RawStdEncoding.DecodeString(string(match[1]))
	if err != nil {
		return WalletRequest{}, err
	}
	var payload map[string]any
	if err = json.Unmarshal(decoded, &payload); err != nil {
		return WalletRequest{}, err
	}
	if expires, ok := payload["expires_at_unix_millis"].(float64); !ok || int64(expires) <= time.Now().UnixMilli() {
		return WalletRequest{}, fmt.Errorf("授权二维码已过期，请停止并重新启动节点刷新会话")
	}
	relay, err := c.ensureRelay(snapshot.Settings)
	if err != nil {
		return WalletRequest{}, err
	}
	payload["rpc_url"] = relay
	return walletQR("posvalpair:", payload)
}

// ExitRequest requests the existing InstructionExitValidator. It contains no
// private key and does not stop the validator before its exit is effective.
func (c *Controller) ExitRequest(ctx context.Context) (WalletRequest, error) {
	c.operation.Lock()
	defer c.operation.Unlock()
	snapshot := c.Snapshot(ctx)
	if snapshot.State != "running" {
		return WalletRequest{}, fmt.Errorf("请保持节点联网，确认链可提交交易后再申请退出")
	}
	var status struct {
		ValidatorEnabled bool `json:"validator_enabled"`
		Consensus        struct {
			LocalValidator struct {
				Address string `json:"account_address"`
				Status  string `json:"status"`
			} `json:"local_validator"`
		} `json:"consensus"`
	}
	if err := json.Unmarshal(snapshot.Node, &status); err != nil {
		return WalletRequest{}, err
	}
	if !status.ValidatorEnabled || status.Consensus.LocalValidator.Address == "" {
		return WalletRequest{}, fmt.Errorf("当前节点尚未成为链上验证者")
	}
	if status.Consensus.LocalValidator.Status == "exiting" || status.Consensus.LocalValidator.Status == "inactive" {
		return WalletRequest{}, fmt.Errorf("该验证者已在退出或停用状态，请等待协议生效")
	}
	relay, err := c.ensureRelay(snapshot.Settings)
	if err != nil {
		return WalletRequest{}, err
	}
	return walletQR("posvalexit:", map[string]any{"version": 1, "rpc_url": relay, "chain_id": snapshot.Settings.ChainID, "chain_identity_hash": snapshot.Settings.ChainIdentity, "genesis_hash": snapshot.Settings.GenesisHash, "validator_address": status.Consensus.LocalValidator.Address, "node_name": snapshot.Settings.Name, "expires_at_unix_millis": float64(time.Now().Add(15 * time.Minute).UnixMilli())})
}

func (c *Controller) ensureRelay(settings Settings) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancel == nil {
		return "", fmt.Errorf("节点已停止")
	}
	if c.relay != nil {
		if time.Now().Before(c.relayExpires) {
			return c.relayURL, nil
		}
		_ = c.relay.Close()
		c.relay = nil
	}
	if !localAddress(settings.PairingIP) {
		return "", fmt.Errorf("钱包连接 IP 已改变，请先检查网络设置")
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(settings.PairingIP, fmt.Sprint(settings.RPCPort+1)))
	if err != nil {
		return "", fmt.Errorf("钱包授权端口无法监听：%w", err)
	}
	token, err := randomToken()
	if err != nil {
		listener.Close()
		return "", err
	}
	c.relayToken = token
	c.relayURL = "http://" + listener.Addr().String() + "/" + token + "/"
	expires := time.Now().Add(15 * time.Minute)
	c.relayExpires = expires
	c.relay = &http.Server{ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 10 * time.Second, IdleTimeout: 10 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			if r.Method != "POST" || time.Now().After(expires) || subtle.ConstantTimeCompare([]byte(r.URL.Path), []byte("/"+token+"/")) != 1 {
				http.NotFound(w, r)
				return
			}
			if r.Header.Get("Origin") != "" {
				http.Error(w, "仅允许钱包应用", 403)
				return
			}
			body, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
			if err != nil || len(body) > 1<<20 {
				http.Error(w, "请求过大", 413)
				return
			}
			var message struct {
				Method string `json:"method"`
			}
			if json.Unmarshal(body, &message) != nil {
				http.Error(w, "JSON 无效", 400)
				return
			}
			allowed := map[string]bool{"getValidatorPairing": true, "completeValidatorPairing": true, "getNodeStatus": true, "getHealth": true, "getBalance": true, "getLatestBlockhash": true, "getAccountInfo": true, "getValidatorSet": true, "simulateTransaction": true, "sendTransaction": true, "getSignatureStatuses": true, "getTransaction": true, "getSlot": true, "getFeeForMessage": true}
			if !allowed[message.Method] {
				http.Error(w, "钱包会话不允许此方法", 403)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			request, _ := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("http://127.0.0.1:%d/", settings.RPCPort), bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			response, err := c.client.Do(request)
			if err != nil {
				http.Error(w, "本机节点 RPC 不可用", 502)
				return
			}
			defer response.Body.Close()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(response.StatusCode)
			_, _ = io.Copy(w, io.LimitReader(response.Body, maxRPCBytes))
		})}
	server := c.relay
	go func() { _ = server.Serve(listener) }()
	return c.relayURL, nil
}
