package main

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

func main() {
	host := requiredEnv("SSH_UPLOAD_HOST")
	user := requiredEnv("SSH_UPLOAD_USER")
	password := requiredEnv("SSH_UPLOAD_PASSWORD")
	sourcePath := requiredEnv("SSH_UPLOAD_SOURCE")
	targetPath := requiredEnv("SSH_UPLOAD_TARGET")
	mode := envOrDefault("SSH_UPLOAD_MODE", "0644")
	if !validMode(mode) {
		exitError("invalid mode: %s", mode)
	}
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		exitError("read source: %v", err)
	}
	client, err := dialSSH(host, user, password)
	if err != nil {
		exitError("connect ssh: %v", err)
	}
	defer client.Close()
	if err := uploadBytes(client, data, targetPath, mode); err != nil {
		exitError("upload: %v", err)
	}
	fmt.Printf("uploaded %s to %s\n", sourcePath, targetPath)
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

func uploadBytes(client *ssh.Client, data []byte, targetPath string, mode string) error {
	targetPath = strings.TrimSpace(targetPath)
	if targetPath == "" || strings.ContainsAny(targetPath, "\x00\r\n") {
		return fmt.Errorf("invalid target path")
	}
	tempPath := targetPath + ".tmp"
	command := fmt.Sprintf(
		"install -d -m 0755 %s && cat > %s && chmod %s %s && mv %s %s",
		shellQuote(parentDir(targetPath)),
		shellQuote(tempPath),
		shellQuote(mode),
		shellQuote(tempPath),
		shellQuote(tempPath),
		shellQuote(targetPath),
	)
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

func parentDir(path string) string {
	index := strings.LastIndex(path, "/")
	if index <= 0 {
		return "."
	}
	return path[:index]
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func validMode(value string) bool {
	if len(value) != 4 {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '7' {
			return false
		}
	}
	return true
}

func envOrDefault(name string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func requiredEnv(name string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		exitError("%s is required", name)
	}
	return value
}

func exitError(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
