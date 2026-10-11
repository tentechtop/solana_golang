package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type hostTrustError struct{ fingerprint string }

func (e *hostTrustError) Error() string {
	return "首次连接，请核对服务器 SSH 指纹：" + e.fingerprint
}

type remote struct {
	client   *ssh.Client
	password string
	sudo     bool
}

func (a *application) connect(ctx context.Context, request deployRequest) (*remote, error) {
	address := net.JoinHostPort(request.Host, fmt.Sprint(request.SSHPort))
	path := filepath.Join(a.stateDir, "known_hosts")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	_ = file.Close()
	verify, err := knownhosts.New(path)
	if err != nil {
		return nil, err
	}
	var trustError *hostTrustError
	config := &ssh.ClientConfig{User: request.User, Auth: []ssh.AuthMethod{ssh.Password(request.Password)}, Timeout: 20 * time.Second}
	config.HostKeyCallback = func(hostname string, address net.Addr, key ssh.PublicKey) error {
		if err := verify(hostname, address, key); err != nil {
			var keyError *knownhosts.KeyError
			if !errors.As(err, &keyError) || len(keyError.Want) > 0 {
				return fmt.Errorf("服务器 SSH 密钥与已信任记录不一致，连接已停止")
			}
			fingerprint := ssh.FingerprintSHA256(key)
			if request.HostFingerprint != fingerprint {
				trustError = &hostTrustError{fingerprint: fingerprint}
				return trustError
			}
			a.mu.Lock()
			defer a.mu.Unlock()
			file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			defer file.Close()
			_, err = file.WriteString(knownhosts.Line([]string{hostname}, key) + "\n")
			return err
		}
		return nil
	}
	connection, err := (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("SSH 连接失败：%w", err)
	}
	_ = connection.SetDeadline(time.Now().Add(20 * time.Second))
	clientConnection, channels, requests, err := ssh.NewClientConn(connection, address, config)
	if err != nil {
		_ = connection.Close()
		if trustError != nil {
			return nil, trustError
		}
		return nil, fmt.Errorf("SSH 登录失败：%w", err)
	}
	_ = connection.SetDeadline(time.Time{})
	client := ssh.NewClient(clientConnection, channels, requests)
	context.AfterFunc(ctx, func() { _ = client.Close() })
	result := &remote{client: client, password: request.Password}
	uid, err := result.run(ctx, "id -u", false)
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	result.sudo = strings.TrimSpace(uid) != "0"
	return result, nil
}

// Command arguments are never concatenated without POSIX shell quoting.
func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

type boundedOutput struct {
	mu    sync.Mutex
	bytes []byte
	limit int
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	remaining := b.limit - len(b.bytes)
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		b.bytes = append(b.bytes, p...)
	}
	return n, nil
}

func (r *remote) run(ctx context.Context, command string, elevated bool) (string, error) {
	session, err := r.client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	stop := context.AfterFunc(ctx, func() { _ = session.Close() })
	defer stop()
	out := &boundedOutput{limit: 8 << 20}
	session.Stdout = out
	session.Stderr = out
	if elevated && r.sudo {
		// Password goes only to sudo's stdin, never into a command or environment.
		command = "sudo -S -p '' -- sh -c " + shellQuote(command)
		session.Stdin = strings.NewReader(r.password + "\n")
	} else {
		command = "sh -c " + shellQuote(command)
	}
	err = session.Run(command)
	if err != nil {
		return "", fmt.Errorf("远端命令失败：%s (%w)", redact(strings.TrimSpace(string(out.bytes)), r.password), err)
	}
	return strings.TrimSpace(string(out.bytes)), nil
}

func (r *remote) upload(ctx context.Context, path string, data io.Reader) error {
	session, err := r.client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	stop := context.AfterFunc(ctx, func() { _ = session.Close() })
	defer stop()
	session.Stdin = data
	out := &boundedOutput{limit: 4096}
	session.Stdout = out
	session.Stderr = out
	if err := session.Run("umask 077; cat > " + shellQuote(path)); err != nil {
		return fmt.Errorf("上传失败：%s (%w)", redact(string(out.bytes), r.password), err)
	}
	return nil
}
