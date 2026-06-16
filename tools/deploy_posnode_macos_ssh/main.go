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
	remoteRoot      = "/Users/mac/solana_golang"
	remoteBinary    = remoteRoot + "/bin/posnode"
	remoteConfig    = remoteRoot + "/config/posnode-223.json"
	remotePlist     = remoteRoot + "/Library/LaunchAgents/com.solana_golang.posnode.plist"
	remoteDataPath  = remoteRoot + "/data/posnode-223-stage"
	launchAgentPath = "/Users/mac/Library/LaunchAgents/com.solana_golang.posnode.plist"
	launchLabel     = "com.solana_golang.posnode"
)

const plistText = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.solana_golang.posnode</string>
  <key>ProgramArguments</key>
  <array>
    <string>/Users/mac/solana_golang/bin/posnode</string>
    <string>-config</string>
    <string>/Users/mac/solana_golang/config/posnode-223.json</string>
  </array>
  <key>WorkingDirectory</key>
  <string>/Users/mac/solana_golang</string>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>EnvironmentVariables</key>
  <dict>
    <key>SG_LOG_FORMAT</key>
    <string>json</string>
  </dict>
  <key>StandardOutPath</key>
  <string>/Users/mac/solana_golang/logs/posnode.out.log</string>
  <key>StandardErrorPath</key>
  <string>/Users/mac/solana_golang/logs/posnode.err.log</string>
</dict>
</plist>
`

func main() {
	host := envOrDefault("POSNODE_MAC_DEPLOY_HOST", "192.168.120.223")
	user := envOrDefault("POSNODE_MAC_DEPLOY_USER", "mac")
	password := os.Getenv("POSNODE_MAC_DEPLOY_PASSWORD")
	if strings.TrimSpace(password) == "" {
		exitError("POSNODE_MAC_DEPLOY_PASSWORD is required")
	}
	client, err := dialSSH(host, user, password)
	if err != nil {
		exitError("connect ssh: %v", err)
	}
	defer client.Close()

	if envOrDefault("POSNODE_MAC_DEPLOY_RESET_DATA", "false") == "true" {
		if err := run(client, "launchctl bootout gui/$(id -u) "+shellQuote(launchAgentPath)+" >/dev/null 2>&1 || true; pkill -x posnode >/dev/null 2>&1 || true; rm -rf "+shellQuote(remoteDataPath)); err != nil {
			exitError("reset remote data: %v", err)
		}
	}
	if err := run(client, "mkdir -p "+shellQuote(remoteRoot+"/bin")+" "+shellQuote(remoteRoot+"/config")+" "+shellQuote(remoteRoot+"/data")+" "+shellQuote(remoteRoot+"/logs")+" "+shellQuote("/Users/mac/Library/LaunchAgents")); err != nil {
		exitError("create remote directories: %v", err)
	}
	if err := uploadFile(client, localPath("dist", "posnode-darwin-arm64"), remoteBinary, "0755"); err != nil {
		exitError("upload binary: %v", err)
	}
	if err := uploadFile(client, localPath("deploy", "posnode-223-mac.json"), remoteConfig, "0644"); err != nil {
		exitError("upload config: %v", err)
	}
	if err := uploadBytes(client, []byte(plistText), launchAgentPath, "0644"); err != nil {
		exitError("upload launchd plist: %v", err)
	}

	if err := startWithLaunchd(client); err != nil {
		fmt.Printf("launchd start failed, fallback to nohup: %v\n", err)
		if err := startWithNohup(client); err != nil {
			exitError("start fallback: %v", err)
		}
	}
	commands := []string{
		"sleep 3",
		"pgrep -fl " + shellQuote(remoteBinary) + " || true",
		"tail -n 80 " + shellQuote(remoteRoot+"/logs/posnode.out.log") + " || true",
		"tail -n 80 " + shellQuote(remoteRoot+"/logs/posnode.err.log") + " || true",
		"if command -v curl >/dev/null 2>&1; then curl -sS -X POST http://127.0.0.1:8899/ -H 'Content-Type: application/json' --data '{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"getHealth\",\"params\":[]}'; else echo 'curl not installed; skip health check'; fi",
	}
	for _, command := range commands {
		if err := run(client, command); err != nil {
			exitError("run %q: %v", command, err)
		}
	}
	fmt.Println("deploy mac posnode ok")
}

func startWithLaunchd(client *ssh.Client) error {
	command := strings.Join([]string{
		"launchctl bootout gui/$(id -u) " + shellQuote(launchAgentPath) + " >/dev/null 2>&1 || true",
		"launchctl bootstrap gui/$(id -u) " + shellQuote(launchAgentPath),
		"launchctl enable gui/$(id -u)/" + shellQuote(launchLabel),
		"launchctl kickstart -k gui/$(id -u)/" + shellQuote(launchLabel),
	}, " && ")
	return run(client, command)
}

func startWithNohup(client *ssh.Client) error {
	command := strings.Join([]string{
		"pkill -x posnode >/dev/null 2>&1 || true",
		"SG_LOG_FORMAT=json nohup " + shellQuote(remoteBinary) + " -config " + shellQuote(remoteConfig) + " >> " + shellQuote(remoteRoot+"/logs/posnode.out.log") + " 2>> " + shellQuote(remoteRoot+"/logs/posnode.err.log") + " &",
	}, "; ")
	return run(client, command)
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
