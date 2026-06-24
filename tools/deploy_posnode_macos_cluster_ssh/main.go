package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	remoteRoot   = "/Users/mac/solana_golang"
	remoteBinary = remoteRoot + "/bin/posnode"
)

type manifest struct {
	Validators []nodeManifest `json:"validators"`
}

type nodeManifest struct {
	Name         string `json:"name"`
	HostGroup    string `json:"host_group"`
	ConfigPath   string `json:"config_path"`
	RemoteConfig string `json:"remote_config"`
	DataPath     string `json:"data_path"`
}

func main() {
	host := envOrDefault("POSNODE_MAC_DEPLOY_HOST", "192.168.120.223")
	user := envOrDefault("POSNODE_MAC_DEPLOY_USER", "mac")
	password := os.Getenv("POSNODE_MAC_DEPLOY_PASSWORD")
	manifestPath := envOrDefault("POSNODE_CLUSTER_MANIFEST", "deploy/generated-4/manifest.json")
	resetData := envOrDefault("POSNODE_MAC_DEPLOY_RESET_DATA", "false") == "true"
	stopOnly := envOrDefault("POSNODE_MAC_STOP_ONLY", "false") == "true"
	if strings.TrimSpace(password) == "" {
		exitError("POSNODE_MAC_DEPLOY_PASSWORD is required")
	}
	plan, err := readManifest(manifestPath)
	if err != nil {
		exitError("read manifest: %v", err)
	}
	macNodes := filterMacNodes(plan.Validators)
	if len(macNodes) == 0 {
		exitError("manifest has no mac validators")
	}
	client, err := dialSSH(host, user, password)
	if err != nil {
		exitError("connect ssh: %v", err)
	}
	defer client.Close()

	if stopOnly {
		if err := stopExisting(client); err != nil {
			exitError("stop existing posnode: %v", err)
		}
		fmt.Println("stopped mac posnodes")
		return
	}

	if err := run(client, "mkdir -p "+shellQuote(remoteRoot+"/bin")+" "+shellQuote(remoteRoot+"/config")+" "+shellQuote(remoteRoot+"/data")+" "+shellQuote(remoteRoot+"/logs")); err != nil {
		exitError("create remote directories: %v", err)
	}
	if err := uploadFile(client, localPath("dist", "posnode-darwin-arm64"), remoteBinary, "0755"); err != nil {
		exitError("upload binary: %v", err)
	}
	if err := stopExisting(client); err != nil {
		exitError("stop existing posnode: %v", err)
	}
	for _, node := range macNodes {
		if resetData {
			if !validRemoteDataPath(node.DataPath) {
				exitError("unsafe remote data path: %s", node.DataPath)
			}
			if err := run(client, "rm -rf "+shellQuote(node.DataPath)); err != nil {
				exitError("reset %s: %v", node.Name, err)
			}
		}
		if err := uploadFile(client, node.ConfigPath, node.RemoteConfig, "0644"); err != nil {
			exitError("upload %s config: %v", node.Name, err)
		}
		if err := startNode(client, node); err != nil {
			exitError("start %s: %v", node.Name, err)
		}
	}
	if err := run(client, "sleep 3; ps -ax -o pid=,command= | grep /Users/mac/solana_golang/bin/posnode | grep -v grep || true"); err != nil {
		exitError("list started processes: %v", err)
	}
	fmt.Printf("deployed %d mac posnodes\n", len(macNodes))
}

func readManifest(path string) (manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return manifest{}, err
	}
	var value manifest
	if err := json.Unmarshal(data, &value); err != nil {
		return manifest{}, err
	}
	return value, nil
}

func filterMacNodes(nodes []nodeManifest) []nodeManifest {
	result := make([]nodeManifest, 0, len(nodes))
	for _, node := range nodes {
		if node.HostGroup == "mac" {
			result = append(result, node)
		}
	}
	return result
}

func stopExisting(client *ssh.Client) error {
	commands := []string{
		"launchctl bootout gui/$(id -u) /Users/mac/Library/LaunchAgents/com.solana_golang.posnode.plist >/dev/null 2>&1 || true",
		"pkill -TERM -f " + shellQuote(remoteBinary) + " >/dev/null 2>&1 || true",
		"sleep 1",
		"pkill -KILL -f " + shellQuote(remoteBinary) + " >/dev/null 2>&1 || true",
	}
	return run(client, strings.Join(commands, "; "))
}

func startNode(client *ssh.Client, node nodeManifest) error {
	logPath := remoteRoot + "/logs/" + node.Name + ".out.log"
	errPath := remoteRoot + "/logs/" + node.Name + ".err.log"
	command := "SG_LOG_FORMAT=json nohup " + shellQuote(remoteBinary) + " -config " + shellQuote(node.RemoteConfig) + " >> " + shellQuote(logPath) + " 2>> " + shellQuote(errPath) + " &"
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

func validRemoteDataPath(value string) bool {
	cleanValue := strings.TrimRight(strings.TrimSpace(value), "/")
	if cleanValue == "" || cleanValue == remoteRoot || cleanValue == remoteRoot+"/data" {
		return false
	}
	return strings.HasPrefix(cleanValue, remoteRoot+"/data/")
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
