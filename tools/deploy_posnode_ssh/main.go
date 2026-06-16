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
	remoteBinary  = remoteRoot + "/bin/posnode"
	remoteConfig  = remoteRoot + "/config/posnode-101.json"
	remoteService = "/etc/systemd/system/posnode.service"
	serviceText   = `[Unit]
Description=solana_golang posnode
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=/opt/solana_golang
ExecStart=/opt/solana_golang/bin/posnode -config /opt/solana_golang/config/posnode-101.json
Restart=always
RestartSec=3
LimitNOFILE=1048576
Environment=SG_LOG_FORMAT=json

[Install]
WantedBy=multi-user.target
`
)

func main() {
	host := envOrDefault("POSNODE_DEPLOY_HOST", "101.35.87.31")
	user := envOrDefault("POSNODE_DEPLOY_USER", "root")
	password := os.Getenv("POSNODE_DEPLOY_PASSWORD")
	if strings.TrimSpace(password) == "" {
		exitError("POSNODE_DEPLOY_PASSWORD is required")
	}
	client, err := dialSSH(host, user, password)
	if err != nil {
		exitError("connect ssh: %v", err)
	}
	defer client.Close()

	if envOrDefault("POSNODE_DEPLOY_RESET_DATA", "false") == "true" {
		if err := run(client, "systemctl stop posnode.service >/dev/null 2>&1 || true"); err != nil {
			exitError("stop service before reset: %v", err)
		}
		if err := run(client, "rm -rf /opt/solana_golang/data/posnode-101"); err != nil {
			exitError("reset remote data: %v", err)
		}
	}
	if err := run(client, "install -d -m 0755 /opt/solana_golang/bin /opt/solana_golang/config /opt/solana_golang/data"); err != nil {
		exitError("create remote directories: %v", err)
	}
	if err := uploadFile(client, localPath("dist", "posnode-linux-amd64"), remoteBinary, "0755"); err != nil {
		exitError("upload binary: %v", err)
	}
	if err := uploadFile(client, localPath("deploy", "posnode-101.json"), remoteConfig, "0644"); err != nil {
		exitError("upload config: %v", err)
	}
	if err := uploadBytes(client, []byte(serviceText), remoteService, "0644"); err != nil {
		exitError("upload service: %v", err)
	}
	commands := []string{
		"systemctl daemon-reload",
		"systemctl enable posnode.service",
		"systemctl restart posnode.service",
		"sleep 2; systemctl --no-pager --full status posnode.service | head -n 40",
		"journalctl -u posnode.service -n 80 --no-pager",
		"systemctl is-active --quiet posnode.service",
		"if command -v curl >/dev/null 2>&1; then curl -sS -X POST http://127.0.0.1:8899/ -H 'Content-Type: application/json' --data '{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"getHealth\",\"params\":[]}'; else echo 'curl not installed; skip health check'; fi",
	}
	for _, command := range commands {
		if err := run(client, command); err != nil {
			exitError("run %q: %v", command, err)
		}
	}
	fmt.Println("deploy ok")
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
