package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	remoteRoot          = "/opt/solana_golang"
	remoteBinary        = remoteRoot + "/bin/posnode"
	defaultConfig       = "cmd/posnode/configs/bootstrap-public.json"
	defaultRemoteConfig = remoteRoot + "/config/bootstrap-public.json"
	defaultRemoteData   = remoteRoot + "/data/public-bootstrap"
	defaultServiceName  = "posnode-bootstrap.service"
)

type cliOptions struct {
	inspectOnly       bool
	stopPublicRPCOnly bool
	uploadBinaryOnly  bool
	resetData         bool
	noStopAll         bool
	noStopPublicRPC   bool
}

func main() {
	options, err := parseCLIOptions(os.Args[1:])
	if err != nil {
		exitError("parse args: %v", err)
	}

	host := envOrDefault("POSNODE_LINUX_DEPLOY_HOST", envOrDefault("POSNODE_DEPLOY_HOST", "101.35.87.31"))
	user := envOrDefault("POSNODE_LINUX_DEPLOY_USER", envOrDefault("POSNODE_DEPLOY_USER", "root"))
	password := envOrDefault("POSNODE_LINUX_DEPLOY_PASSWORD", os.Getenv("POSNODE_DEPLOY_PASSWORD"))
	if strings.TrimSpace(password) == "" {
		exitError("POSNODE_LINUX_DEPLOY_PASSWORD or POSNODE_DEPLOY_PASSWORD is required")
	}

	localConfigPath := envOrDefault("POSNODE_LINUX_DEPLOY_CONFIG", defaultConfig)
	remoteConfigPath := envOrDefault("POSNODE_LINUX_REMOTE_CONFIG", defaultRemoteConfig)
	remoteDataPath := envOrDefault("POSNODE_LINUX_REMOTE_DATA_PATH", defaultRemoteData)
	serviceName := envOrDefault("POSNODE_LINUX_SERVICE_NAME", defaultServiceName)
	resetData := envBoolOrDefault("POSNODE_LINUX_DEPLOY_RESET_DATA", false) || options.resetData
	stopAll := envBoolOrDefault("POSNODE_LINUX_STOP_ALL", true) && !options.noStopAll
	inspectOnly := envBoolOrDefault("POSNODE_LINUX_INSPECT_ONLY", false) || options.inspectOnly
	stopPublicRPC := envBoolOrDefault("POSNODE_LINUX_STOP_PUBLIC_RPC", true) && !options.noStopPublicRPC
	stopPublicRPCOnly := envBoolOrDefault("POSNODE_LINUX_STOP_PUBLIC_RPC_ONLY", false) || options.stopPublicRPCOnly
	uploadBinaryOnly := envBoolOrDefault("POSNODE_LINUX_UPLOAD_BINARY_ONLY", false) || options.uploadBinaryOnly
	restartServices, err := parseServiceNames(envOrDefault("POSNODE_LINUX_RESTART_SERVICES", "posnode-bootstrap.service,posnode-public-validator.service"))
	if err != nil {
		exitError("invalid restart services: %v", err)
	}
	firewallPorts, err := parseFirewallPorts(envOrDefault("POSNODE_LINUX_FIREWALL_PORTS", "8899/tcp,5101/tcp"))
	if err != nil {
		exitError("invalid firewall ports: %v", err)
	}
	healthRPCPort := envOrDefault("POSNODE_LINUX_HEALTH_RPC_PORT", "8899")
	if !validPortText(healthRPCPort) {
		exitError("invalid health rpc port: %s", healthRPCPort)
	}
	if !validServiceName(serviceName) {
		exitError("invalid service name: %s", serviceName)
	}
	if resetData && !validRemoteDataPath(remoteDataPath) {
		exitError("unsafe remote data path: %s", remoteDataPath)
	}

	client, err := dialSSH(host, user, password)
	if err != nil {
		exitError("connect ssh: %v", err)
	}
	defer client.Close()

	if inspectOnly {
		if err := inspectRemote(client); err != nil {
			exitError("inspect remote: %v", err)
		}
		return
	}
	if stopPublicRPC {
		if err := stopPublicRPCServices(client); err != nil {
			exitError("stop public rpc services: %v", err)
		}
	}
	if stopPublicRPCOnly {
		fmt.Println("public rpc services stopped")
		return
	}
	if uploadBinaryOnly {
		if err := uploadBinaryAndRestart(client, restartServices); err != nil {
			exitError("upload binary and restart: %v", err)
		}
		return
	}
	if err := stopExisting(client, serviceName, stopAll); err != nil {
		exitError("stop existing services: %v", err)
	}
	if resetData {
		if err := run(client, "rm -rf "+shellQuote(remoteDataPath)); err != nil {
			exitError("reset remote data: %v", err)
		}
	}
	if err := run(client, "install -d -m 0755 /opt/solana_golang/bin /opt/solana_golang/config /opt/solana_golang/data /opt/solana_golang/logs"); err != nil {
		exitError("create remote directories: %v", err)
	}
	if err := uploadFile(client, localPath("dist", "posnode-linux-amd64"), remoteBinary, "0755"); err != nil {
		exitError("upload binary: %v", err)
	}
	if err := uploadFile(client, localConfigPath, remoteConfigPath, "0644"); err != nil {
		exitError("upload config: %v", err)
	}
	if err := uploadBytes(client, []byte(buildServiceText(remoteConfigPath, serviceName)), "/etc/systemd/system/"+serviceName, "0644"); err != nil {
		exitError("upload service: %v", err)
	}

	commands := []string{
		"systemctl daemon-reload",
		buildFirewallCommand(firewallPorts),
		"systemctl enable " + shellQuote(serviceName),
		"systemctl restart " + shellQuote(serviceName),
		"sleep 3; systemctl --no-pager --full status " + shellQuote(serviceName) + " | head -n 40",
		"journalctl -u " + shellQuote(serviceName) + " -n 80 --no-pager",
		"systemctl is-active --quiet " + shellQuote(serviceName),
		"if command -v curl >/dev/null 2>&1; then curl -sS -X POST http://127.0.0.1:" + healthRPCPort + "/ -H 'Content-Type: application/json' --data '{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"getNodeStatus\",\"params\":[]}'; else echo 'curl not installed; skip status check'; fi",
	}
	for _, command := range commands {
		if err := run(client, command); err != nil {
			exitError("run %q: %v", command, err)
		}
	}
	fmt.Println("deploy linux posnode ok")
}

func buildServiceText(remoteConfigPath string, serviceName string) string {
	logName := strings.TrimSuffix(serviceName, ".service")
	if logName == "" {
		logName = "posnode"
	}
	return fmt.Sprintf(`[Unit]
Description=solana_golang %s
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=/opt/solana_golang
ExecStart=/opt/solana_golang/bin/posnode -config %s
Restart=always
RestartSec=3
LimitNOFILE=1048576
Environment=SG_LOG_FORMAT=json
StandardOutput=append:/opt/solana_golang/logs/%s.out.log
StandardError=append:/opt/solana_golang/logs/%s.err.log

[Install]
WantedBy=multi-user.target
`, logName, remoteConfigPath, logName, logName)
}

func stopExisting(client *ssh.Client, serviceName string, stopAll bool) error {
	commands := []string{
		"systemctl stop " + shellQuote(serviceName) + " >/dev/null 2>&1 || true",
		"systemctl disable " + shellQuote(serviceName) + " >/dev/null 2>&1 || true",
	}
	if !stopAll {
		return run(client, strings.Join(commands, "; "))
	}
	commands = append(commands,
		"systemctl stop posnode.service >/dev/null 2>&1 || true",
		"systemctl disable posnode.service >/dev/null 2>&1 || true",
		"systemctl stop posnode-18v-boot.service >/dev/null 2>&1 || true",
		"systemctl disable posnode-18v-boot.service >/dev/null 2>&1 || true",
		"systemctl stop rpcnode.service >/dev/null 2>&1 || true",
		"systemctl disable rpcnode.service >/dev/null 2>&1 || true",
		"pkill -x posnode >/dev/null 2>&1 || true",
		"pkill -x rpcnode >/dev/null 2>&1 || true",
	)
	return run(client, strings.Join(commands, "; "))
}

func inspectRemote(client *ssh.Client) error {
	commands := []string{
		"echo '== systemd services =='; systemctl list-units --type=service --all --no-pager | grep -E 'posnode|rpcnode|public-rpc' || true",
		"echo '== processes =='; ps -eo pid,comm,args | grep -E 'posnode|rpcnode' | grep -v grep || true",
		"echo '== listening ports =='; ss -ltnp | grep -E ':(8899|8901|8910|8911|8912|8913|5101|5102|5103|5104|5105|5106|5107)\\b' || true",
		"echo '== config roles =='; for file in /opt/solana_golang/config/*.json; do [ -f \"$file\" ] || continue; echo \"--- $file\"; grep -E '\"chain_id\"|\"chain_identity_hash\"|\"genesis_hash\"|\"node_mode\"|\"node_name\"|\"data_path\"|\"listen_port\"|\"advertised_ip\"|\"advertised_port\"|\"rpc_enabled\"|\"rpc_listen_ip\"|\"rpc_port\"|\"node_role\"|\"validator_enabled\"|\"consensus_enabled\"|\"staker_address\"|\"bootstrap_join\"|\"rpc_url\"|\"registered_at_unix_milli\"|\"staker_signature\"|\"peer_key_path\"|\"validator_key_path\"|\"consensus_key_path\"|\"bls_key_path\"' \"$file\" || true; done",
		"echo '== recent validator logs =='; journalctl -u posnode-public-validator.service -n 80 --no-pager || true",
		"echo '== validator stdout tail =='; tail -n 80 /opt/solana_golang/logs/posnode-public-validator.out.log 2>/dev/null || true; echo '== validator stderr tail =='; tail -n 80 /opt/solana_golang/logs/posnode-public-validator.err.log 2>/dev/null || true",
	}
	for _, command := range commands {
		if err := run(client, command); err != nil {
			return err
		}
	}
	return nil
}

func stopPublicRPCServices(client *ssh.Client) error {
	return run(client, buildStopPublicRPCCommand())
}

func uploadBinaryAndRestart(client *ssh.Client, serviceNames []string) error {
	if len(serviceNames) == 0 {
		return fmt.Errorf("restart services is empty")
	}
	if err := run(client, "install -d -m 0755 /opt/solana_golang/bin /opt/solana_golang/logs"); err != nil {
		return fmt.Errorf("create remote directories: %w", err)
	}
	if err := uploadFile(client, localPath("dist", "posnode-linux-amd64"), remoteBinary, "0755"); err != nil {
		return fmt.Errorf("upload binary: %w", err)
	}
	commands := []string{"systemctl daemon-reload"}
	for _, serviceName := range serviceNames {
		quotedName := shellQuote(serviceName)
		commands = append(commands,
			"systemctl restart "+quotedName,
			"sleep 2; systemctl --no-pager --full status "+quotedName+" | head -n 30",
			"systemctl is-active --quiet "+quotedName,
		)
	}
	commands = append(commands,
		"if command -v curl >/dev/null 2>&1; then curl -sS -X POST http://127.0.0.1:8899/ -H 'Content-Type: application/json' --data '{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"getHealth\",\"params\":[]}'; else echo 'curl not installed; skip bootstrap health check'; fi",
		"if command -v curl >/dev/null 2>&1; then curl -sS -X POST http://127.0.0.1:8901/ -H 'Content-Type: application/json' --data '{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"getHealth\",\"params\":[]}'; else echo 'curl not installed; skip validator health check'; fi",
	)
	for _, command := range commands {
		if err := run(client, command); err != nil {
			return err
		}
	}
	return nil
}

func buildStopPublicRPCCommand() string {
	serviceNames := []string{
		"rpcnode.service",
		"public-rpc.service",
		"posnode-public-rpc.service",
		"public-rpc-8910.service",
		"public-rpc-8911.service",
		"public-rpc-8912.service",
		"public-rpc-8913.service",
		"posnode-public-rpc-8910.service",
		"posnode-public-rpc-8911.service",
		"posnode-public-rpc-8912.service",
		"posnode-public-rpc-8913.service",
		"public-rpc-gateway-8910.service",
		"public-rpc-gateway-8911.service",
		"public-rpc-gateway-8912.service",
		"public-rpc-gateway-8913.service",
		"rpcnode-8910.service",
		"rpcnode-8911.service",
		"rpcnode-8912.service",
		"rpcnode-8913.service",
	}
	commands := make([]string, 0, len(serviceNames)*2+2)
	for _, serviceName := range serviceNames {
		quotedName := shellQuote(serviceName)
		commands = append(commands,
			"systemctl stop "+quotedName+" >/dev/null 2>&1 || true",
			"systemctl disable "+quotedName+" >/dev/null 2>&1 || true",
		)
	}
	commands = append(commands,
		"pkill -x rpcnode >/dev/null 2>&1 || true",
	)
	return strings.Join(commands, "; ")
}

func parseCLIOptions(args []string) (cliOptions, error) {
	var options cliOptions
	flagSet := flag.NewFlagSet("deploy_posnode_linux_ssh", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	flagSet.BoolVar(&options.inspectOnly, "inspect", false, "inspect remote state and exit")
	flagSet.BoolVar(&options.stopPublicRPCOnly, "stop-public-rpc-only", false, "stop legacy public RPC services and exit")
	flagSet.BoolVar(&options.uploadBinaryOnly, "upload-binary-only", false, "upload binary and restart configured services")
	flagSet.BoolVar(&options.resetData, "reset-data", false, "reset remote data path before restart")
	flagSet.BoolVar(&options.noStopAll, "no-stop-all", false, "stop only the selected service")
	flagSet.BoolVar(&options.noStopPublicRPC, "no-stop-public-rpc", false, "skip stopping legacy public RPC services")
	if err := flagSet.Parse(args); err != nil {
		return cliOptions{}, err
	}
	if flagSet.NArg() > 0 {
		return cliOptions{}, fmt.Errorf("unsupported args: %s", strings.Join(flagSet.Args(), " "))
	}
	return options, nil
}

func parseServiceNames(value string) ([]string, error) {
	rawNames := strings.Split(value, ",")
	serviceNames := make([]string, 0, len(rawNames))
	for _, rawName := range rawNames {
		serviceName := strings.TrimSpace(rawName)
		if serviceName == "" {
			continue
		}
		if !validServiceName(serviceName) {
			return nil, fmt.Errorf("unsupported service name %q", serviceName)
		}
		serviceNames = append(serviceNames, serviceName)
	}
	return serviceNames, nil
}

func buildFirewallCommand(ports []string) string {
	if len(ports) == 0 {
		return "echo 'no firewall ports configured'"
	}
	commands := make([]string, 0, len(ports))
	for _, port := range ports {
		commands = append(commands, "firewall-cmd --permanent --add-port="+shellQuote(port))
	}
	return "if command -v firewall-cmd >/dev/null 2>&1 && systemctl is-active --quiet firewalld; then " + strings.Join(commands, " && ") + " && firewall-cmd --reload; else echo 'firewalld not active; skip port open'; fi"
}

func dialSSH(host string, user string, password string) (*ssh.Client, error) {
	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
			ssh.KeyboardInteractive(func(user string, instruction string, questions []string, echos []bool) ([]string, error) {
				_ = user
				_ = instruction
				answers := make([]string, len(questions))
				for index := range answers {
					answers[index] = password
				}
				return answers, nil
			}),
		},
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			_ = hostname
			_ = remote
			_ = key
			return nil
		},
		Timeout: 15 * time.Second,
	}
	return ssh.Dial("tcp", net.JoinHostPort(host, "22"), config)
}

func uploadFile(client *ssh.Client, sourcePath string, targetPath string, mode string) error {
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("read %s: %w", sourcePath, err)
	}
	return uploadBytes(client, data, targetPath, mode)
}

func uploadBytes(client *ssh.Client, data []byte, targetPath string, mode string) error {
	tempPath := targetPath + ".tmp"
	command := fmt.Sprintf("cat > %s && chmod %s %s && mv %s %s", shellQuote(tempPath), shellQuote(mode), shellQuote(tempPath), shellQuote(tempPath), shellQuote(targetPath))
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	session.Stdin = bytes.NewReader(data)
	output, err := session.CombinedOutput(command)
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func run(client *ssh.Client, command string) error {
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	output, err := session.CombinedOutput(command)
	text := strings.TrimSpace(string(output))
	if text != "" {
		fmt.Println(text)
	}
	if err != nil {
		return fmt.Errorf("%w: %s", err, text)
	}
	return nil
}

func localPath(parts ...string) string {
	items := append([]string{"."}, parts...)
	return filepath.Join(items...)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func validRemoteDataPath(value string) bool {
	cleanValue := strings.TrimRight(strings.TrimSpace(value), "/")
	if cleanValue == "" || cleanValue == remoteRoot || cleanValue == remoteRoot+"/data" {
		return false
	}
	return strings.HasPrefix(cleanValue, remoteRoot+"/data/")
}

func validServiceName(value string) bool {
	if strings.TrimSpace(value) == "" || strings.ContainsAny(value, "/\\") {
		return false
	}
	return strings.HasSuffix(value, ".service")
}

func parseFirewallPorts(value string) ([]string, error) {
	rawPorts := strings.Split(value, ",")
	ports := make([]string, 0, len(rawPorts))
	for _, rawPort := range rawPorts {
		port := strings.TrimSpace(rawPort)
		if port == "" {
			continue
		}
		if !validFirewallPortSpec(port) {
			return nil, fmt.Errorf("unsupported port spec %q", port)
		}
		ports = append(ports, port)
	}
	return ports, nil
}

func validFirewallPortSpec(value string) bool {
	parts := strings.Split(value, "/")
	if len(parts) != 2 {
		return false
	}
	protocol := strings.ToLower(strings.TrimSpace(parts[1]))
	if protocol != "tcp" && protocol != "udp" {
		return false
	}
	return validPortText(parts[0])
}

func validPortText(value string) bool {
	port, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return false
	}
	return port >= 1 && port <= 65535
}

func envOrDefault(name string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func envBoolOrDefault(name string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return strings.EqualFold(value, "true")
}

func exitError(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
