// Ordexus Node Deployer is a loopback-only Windows deployment application.
package main

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

//go:embed ui.html
var uiFiles embed.FS

type profile struct {
	Wallet    string `json:"wallet_address"`
	PublicKey string `json:"raw_public_key"`
	Supply    string `json:"initial_supply_lamports"`
}

type application struct {
	bundle, stateDir, token, host string
	profile                       profile
	mu                            sync.Mutex
	job                           *deploymentJob
}

type deploymentJob struct {
	ID     string   `json:"id"`
	State  string   `json:"state"`
	Logs   []string `json:"logs"`
	Result any      `json:"result,omitempty"`
}

type deployRequest struct {
	Host                  string `json:"host"`
	User                  string `json:"user"`
	Password              string `json:"password"`
	SSHPort               int    `json:"ssh_port"`
	Name                  string `json:"name"`
	P2PPort               int    `json:"p2p_port"`
	RPCPort               int    `json:"rpc_port"`
	BootstrapRPC          string `json:"bootstrap_rpc"`
	PublicRPC             bool   `json:"public_rpc"`
	OpenP2PFirewall       bool   `json:"open_p2p_firewall"`
	Threshold             int    `json:"threshold"`
	HostFingerprint       string `json:"host_fingerprint"`
	ExpectedChainIdentity string `json:"expected_chain_identity"`
}

func main() {
	executable, err := os.Executable()
	if err != nil {
		panic(err)
	}
	bundle := flag.String("bundle", filepath.Dir(executable), "directory containing Linux binaries and public genesis profile")
	noBrowser := flag.Bool("no-browser", false, "print URL without opening browser")
	flag.Parse()
	stateDir, err := os.UserConfigDir()
	if err != nil {
		panic(err)
	}
	a := &application{bundle: *bundle, stateDir: filepath.Join(stateDir, "OrdexusNodeDeployer"), token: randomID()}
	if err := os.MkdirAll(a.stateDir, 0700); err != nil {
		panic(err)
	}
	data, err := os.ReadFile(filepath.Join(*bundle, "config", "genesis-account.json"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "部署包缺少公开创世配置，请使用完整部署包。", err)
		os.Exit(1)
	}
	if err := json.Unmarshal(data, &a.profile); err != nil || a.profile.PublicKey == "" || a.profile.Supply == "" {
		fmt.Fprintln(os.Stderr, "公开创世配置无效")
		os.Exit(1)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	a.host = listener.Addr().String()
	mux := http.NewServeMux()
	mux.HandleFunc("/", a.index)
	mux.HandleFunc("/api/check", a.check)
	mux.HandleFunc("/api/network", a.saveNetwork)
	mux.HandleFunc("/api/deploy", a.deploy)
	mux.HandleFunc("/api/job", a.jobStatus)
	server := &http.Server{Handler: a.protect(mux), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	url := "http://" + a.host + "/"
	fmt.Println("Ordexus 节点部署器:", url)
	if !*noBrowser {
		var command *exec.Cmd
		if runtime.GOOS == "windows" {
			command = exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url)
		} else {
			command = exec.Command("xdg-open", url)
		}
		if err := command.Start(); err != nil {
			fmt.Println("请在浏览器打开以上地址。")
		}
	}
	if err := server.Serve(listener); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func randomID() string {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(value[:])
}

func (a *application) protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'nonce-"+a.token+"'; style-src 'unsafe-inline'; frame-ancestors 'none'; object-src 'none'; base-uri 'none'")
		if r.Host != a.host || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != "http://"+a.host) {
			http.Error(w, "来源不允许", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") && r.Header.Get("X-Ordexus-Token") != a.token {
			http.Error(w, "访问凭证无效", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *application) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	data, _ := uiFiles.ReadFile("ui.html")
	page, err := template.New("ui").Parse(string(data))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	bootstrap := ""
	if data, err := os.ReadFile(filepath.Join(a.bundle, "config", "network.json")); err == nil {
		var network struct {
			BootstrapRPC string `json:"bootstrap_rpc"`
		}
		_ = json.Unmarshal(data, &network)
		bootstrap = network.BootstrapRPC
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = page.Execute(w, map[string]string{"Token": a.token, "Wallet": a.profile.Wallet, "Bootstrap": bootstrap})
}

func decodeRequest(w http.ResponseWriter, r *http.Request) (deployRequest, error) {
	var request deployRequest
	if r.Method != http.MethodPost {
		return request, fmt.Errorf("需要 POST 请求")
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return request, fmt.Errorf("请求格式无效")
	}
	return request, validateRequest(&request)
}

func reply(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (a *application) saveNetwork(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		reply(w, 405, map[string]string{"error": "需要 POST 请求"})
		return
	}
	var network struct {
		BootstrapRPC string `json:"bootstrap_rpc"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&network); err != nil {
		reply(w, 400, map[string]string{"error": "网络地址格式无效"})
		return
	}
	network.BootstrapRPC = strings.TrimSpace(network.BootstrapRPC)
	if err := validateBootstrapRPC(network.BootstrapRPC); err != nil {
		reply(w, 400, map[string]string{"error": err.Error()})
		return
	}
	data, _ := json.MarshalIndent(network, "", "  ")
	if err := os.WriteFile(filepath.Join(a.bundle, "config", "network.json"), data, 0644); err != nil {
		reply(w, 500, map[string]string{"error": "默认网络保存失败，请将部署包放到有写入权限的目录。"})
		return
	}
	reply(w, 200, map[string]string{"message": "已保存默认网络，下次打开部署器会自动填入。"})
}

func (a *application) check(w http.ResponseWriter, r *http.Request) {
	request, err := decodeRequest(w, r)
	if err != nil {
		reply(w, 400, map[string]string{"error": err.Error()})
		return
	}
	plan, err := a.inspect(r.Context(), request)
	if unknown, ok := err.(*hostTrustError); ok {
		reply(w, 200, map[string]any{"needs_trust": true, "fingerprint": unknown.fingerprint})
		return
	}
	if err != nil {
		reply(w, 400, map[string]string{"error": redact(err.Error(), request.Password)})
		return
	}
	reply(w, 200, plan)
}

func (a *application) deploy(w http.ResponseWriter, r *http.Request) {
	request, err := decodeRequest(w, r)
	if err != nil || request.ExpectedChainIdentity == "" {
		reply(w, 400, map[string]string{"error": "请先检查服务器和目标网络。"})
		return
	}
	a.mu.Lock()
	if a.job != nil && a.job.State == "running" {
		a.mu.Unlock()
		reply(w, 409, map[string]string{"error": "已有部署正在进行。"})
		return
	}
	job := &deploymentJob{ID: randomID(), State: "running", Logs: []string{}}
	a.job = job
	a.mu.Unlock()
	go func() {
		log := func(line string) {
			a.mu.Lock()
			defer a.mu.Unlock()
			job.Logs = append(job.Logs, redact(line, request.Password))
		}
		result, err := a.perform(request, log)
		a.mu.Lock()
		defer a.mu.Unlock()
		if err != nil {
			job.State = "failed"
			job.Logs = append(job.Logs, redact(err.Error(), request.Password))
		} else {
			job.State = "complete"
			job.Result = result
		}
		request.Password = ""
	}()
	reply(w, 202, map[string]string{"id": job.ID})
}

func (a *application) jobStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "需要 GET 请求", 405)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.job == nil || r.URL.Query().Get("id") != a.job.ID {
		http.NotFound(w, r)
		return
	}
	reply(w, 200, a.job)
}

func redact(value, password string) string {
	if password != "" {
		value = strings.ReplaceAll(value, password, "[已隐藏]")
	}
	return value
}
