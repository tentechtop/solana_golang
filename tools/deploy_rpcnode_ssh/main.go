package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	remoteRoot    = "/opt/solana_golang"
	remoteBinary  = remoteRoot + "/bin/rpcnode"
	defaultConfig = "deploy/generated-4/rpcnode-101.json"
)

func buildServiceText(remoteConfig string) string {
	return fmt.Sprintf(`[Unit]
Description=solana_golang rpcnode
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=/opt/solana_golang
ExecStart=/opt/solana_golang/bin/rpcnode -config %s
Restart=always
RestartSec=3
LimitNOFILE=1048576
Environment=SG_LOG_FORMAT=json

[Install]
WantedBy=multi-user.target
`, remoteConfig)
}

func main() {
	host := envOrDefault("RPCNODE_DEPLOY_HOST", envOrDefault("POSNODE_DEPLOY_HOST", "101.35.87.31"))
	user := envOrDefault("RPCNODE_DEPLOY_USER", envOrDefault("POSNODE_DEPLOY_USER", "root"))
	password := envOrDefault("RPCNODE_DEPLOY_PASSWORD", os.Getenv("POSNODE_DEPLOY_PASSWORD"))
	if strings.TrimSpace(password) == "" {
		exitError("RPCNODE_DEPLOY_PASSWORD or POSNODE_DEPLOY_PASSWORD is required")
	}
	client, err := dialSSH(host, user, password)
	if err != nil {
		exitError("connect ssh: %v", err)
	}
	defer client.Close()

	localConfigPath := envOrDefault("RPCNODE_DEPLOY_CONFIG", defaultConfig)
	remoteConfig := envOrDefault("RPCNODE_REMOTE_CONFIG", remoteRoot+"/config/"+filepath.Base(localConfigPath))
	serviceName := envOrDefault("RPCNODE_SERVICE_NAME", "rpcnode.service")
	if !validServiceName(serviceName) {
		exitError("invalid service name: %s", serviceName)
	}
	remoteService := "/etc/systemd/system/" + serviceName

	commandsBeforeUpload := []string{
		"systemctl stop posnode.service >/dev/null 2>&1 || true",
		"systemctl disable posnode.service >/dev/null 2>&1 || true",
		"systemctl stop posnode-18v-boot.service >/dev/null 2>&1 || true",
		"systemctl disable posnode-18v-boot.service >/dev/null 2>&1 || true",
		"systemctl stop " + shellQuote(serviceName) + " >/dev/null 2>&1 || true",
		"pkill -x posnode >/dev/null 2>&1 || true",
		"pkill -x rpcnode >/dev/null 2>&1 || true",
		"install -d -m 0755 /opt/solana_golang/bin /opt/solana_golang/config /opt/solana_golang/data",
	}
	for _, command := range commandsBeforeUpload {
		if err := run(client, command); err != nil {
			exitError("run %q: %v", command, err)
		}
	}
	if err := uploadFile(client, localPath("dist", "rpcnode-linux-amd64"), remoteBinary, "0755"); err != nil {
		exitError("upload binary: %v", err)
	}
	if err := uploadFile(client, localConfigPath, remoteConfig, "0644"); err != nil {
		exitError("upload config: %v", err)
	}
	if err := uploadBytes(client, []byte(buildServiceText(remoteConfig)), remoteService, "0644"); err != nil {
		exitError("upload service: %v", err)
	}
	commands := []string{
		"systemctl daemon-reload",
		"if command -v firewall-cmd >/dev/null 2>&1 && systemctl is-active --quiet firewalld; then firewall-cmd --permanent --add-port=8899/tcp && firewall-cmd --permanent --add-port=5101/tcp && firewall-cmd --reload; else echo 'firewalld not active; skip port open'; fi",
		"systemctl enable " + shellQuote(serviceName),
		"systemctl restart " + shellQuote(serviceName),
		"sleep 2; systemctl --no-pager --full status " + shellQuote(serviceName) + " | head -n 40",
		"journalctl -u " + shellQuote(serviceName) + " -n 80 --no-pager",
		"systemctl is-active --quiet " + shellQuote(serviceName),
		"if command -v curl >/dev/null 2>&1; then curl -sS -X POST http://127.0.0.1:8899/ -H 'Content-Type: application/json' --data '{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"getNodeStatus\",\"params\":[]}'; else echo 'curl not installed; skip status check'; fi",
	}
	for _, command := range commands {
		if err := run(client, command); err != nil {
			exitError("run %q: %v", command, err)
		}
	}
	fmt.Println("deploy rpcnode ok")
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

func validServiceName(value string) bool {
	if strings.TrimSpace(value) == "" || strings.ContainsAny(value, "/\\") {
		return false
	}
	return strings.HasSuffix(value, ".service")
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
