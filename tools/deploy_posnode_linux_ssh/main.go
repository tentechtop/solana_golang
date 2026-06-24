package main

import (
	"bytes"
	"fmt"
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
	defaultConfig       = "deploy/three-validator-101-linux.json"
	defaultRemoteConfig = remoteRoot + "/config/three-validator-101-linux.json"
	defaultRemoteData   = remoteRoot + "/data/posnode-three-101-stage"
	defaultServiceName  = "posnode-three-validator.service"
)

func main() {
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
	resetData := strings.EqualFold(envOrDefault("POSNODE_LINUX_DEPLOY_RESET_DATA", "false"), "true")
	stopAll := strings.EqualFold(envOrDefault("POSNODE_LINUX_STOP_ALL", "true"), "true")
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

func exitError(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
