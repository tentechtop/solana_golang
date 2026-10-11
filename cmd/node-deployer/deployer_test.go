package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func testRequest() deployRequest {
	return deployRequest{Host: "203.0.113.10", User: "root", Password: "test-only-password", SSHPort: 22, Name: "fullnode-01", P2PPort: 5102, RPCPort: 8902, BootstrapRPC: "https://bootstrap.example/", Threshold: 4}
}

func TestRequestRejectsShellInjectionAndCredentialURLs(t *testing.T) {
	for _, change := range []func(*deployRequest){
		func(r *deployRequest) { r.Host = "1.2.3.4; reboot" }, func(r *deployRequest) { r.User = "root; echo x" },
		func(r *deployRequest) { r.Name = "../existing-ledger" }, func(r *deployRequest) { r.BootstrapRPC = "https://user:secret@example.com/" },
		func(r *deployRequest) { r.BootstrapRPC = "file:///etc/passwd" }, func(r *deployRequest) { r.P2PPort = r.SSHPort },
	} {
		r := testRequest()
		change(&r)
		if validateRequest(&r) == nil {
			t.Fatalf("unsafe request accepted: %+v", r.Host)
		}
	}
	r := testRequest()
	r.Password = "quotes'$() & spaces"
	if err := validateRequest(&r); err != nil {
		t.Fatal(err)
	}
}

func TestLoopbackAPIDeniesCrossOriginAndMissingToken(t *testing.T) {
	a := &application{host: "127.0.0.1:19090", token: "token"}
	handler := a.protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for _, tc := range []struct {
		host, origin, token string
		want                int
	}{
		{"127.0.0.1:19090", "http://evil.example", "token", 403}, {"evil.example", "", "token", 403},
		{"127.0.0.1:19090", "", "", 403}, {"127.0.0.1:19090", "http://127.0.0.1:19090", "token", 204},
	} {
		request := httptest.NewRequest("POST", "http://"+tc.host+"/api/check", nil)
		request.Host = tc.host
		request.Header.Set("Origin", tc.origin)
		request.Header.Set("X-Ordexus-Token", tc.token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, request)
		if w.Code != tc.want {
			t.Fatalf("status %d, want %d", w.Code, tc.want)
		}
	}
}

func TestDefaultNetworkStoresOnlyPublicRPC(t *testing.T) {
	bundle := t.TempDir()
	if err := os.Mkdir(filepath.Join(bundle, "config"), 0755); err != nil {
		t.Fatal(err)
	}
	a := &application{bundle: bundle}
	for _, tc := range []struct {
		body string
		want int
	}{
		{`{"bootstrap_rpc":"https://bootstrap.example/"}`, 200},
		{`{"bootstrap_rpc":"https://user:secret@bootstrap.example/"}`, 400},
		{`{"bootstrap_rpc":"https://bootstrap.example/","password":"test-only-password"}`, 400},
	} {
		request := httptest.NewRequest("POST", "/api/network", strings.NewReader(tc.body))
		w := httptest.NewRecorder()
		a.saveNetwork(w, request)
		if w.Code != tc.want {
			t.Fatalf("status %d, want %d", w.Code, tc.want)
		}
	}
	data, err := os.ReadFile(filepath.Join(bundle, "config", "network.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]string
	if json.Unmarshal(data, &saved) != nil || len(saved) != 1 || saved["bootstrap_rpc"] != "https://bootstrap.example/" {
		t.Fatalf("default network changed by rejected request: %s", data)
	}
}

type sshFixture struct {
	listener net.Listener
	signer   ssh.Signer
	auth     atomic.Int32
	commands chan string
	inputs   chan string
}

func newSSHFixture(t *testing.T, address string) *sshFixture {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	f := &sshFixture{listener: listener, signer: signer, commands: make(chan string, 16), inputs: make(chan string, 16)}
	config := &ssh.ServerConfig{PasswordCallback: func(meta ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
		f.auth.Add(1)
		if string(password) != "test-only-password" {
			return nil, io.EOF
		}
		return nil, nil
	}}
	config.AddHostKey(signer)
	go func() {
		for {
			socket, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				connection, channels, requests, err := ssh.NewServerConn(socket, config)
				if err != nil {
					_ = socket.Close()
					return
				}
				defer connection.Close()
				go ssh.DiscardRequests(requests)
				for incoming := range channels {
					channel, requests, err := incoming.Accept()
					if err != nil {
						continue
					}
					go func() {
						defer channel.Close()
						for request := range requests {
							if request.Type != "exec" {
								_ = request.Reply(false, nil)
								continue
							}
							var payload struct{ Command string }
							if ssh.Unmarshal(request.Payload, &payload) != nil {
								return
							}
							f.commands <- payload.Command
							_ = request.Reply(true, nil)
							input, _ := io.ReadAll(channel)
							f.inputs <- string(input)
							_, _ = channel.Write([]byte("0\n"))
							var exit [4]byte
							binary.BigEndian.PutUint32(exit[:], 0)
							_, _ = channel.SendRequest("exit-status", false, exit[:])
							return
						}
					}()
				}
			}()
		}
	}()
	t.Cleanup(func() { _ = listener.Close() })
	return f
}

func TestSSHTrustBeforePasswordAndRejectChangedKey(t *testing.T) {
	fixture := newSSHFixture(t, "127.0.0.1:0")
	host, port, _ := net.SplitHostPort(fixture.listener.Addr().String())
	number, _ := strconv.Atoi(port)
	r := testRequest()
	r.Host = host
	r.SSHPort = number
	a := &application{stateDir: t.TempDir()}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := a.connect(ctx, r); err == nil {
		t.Fatal("untrusted SSH key accepted")
	} else if _, ok := err.(*hostTrustError); !ok {
		t.Fatalf("unexpected error: %v", err)
	}
	if fixture.auth.Load() != 0 {
		t.Fatal("password sent before key approval")
	}
	r.HostFingerprint = ssh.FingerprintSHA256(fixture.signer.PublicKey())
	connection, err := a.connect(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if fixture.auth.Load() != 1 {
		t.Fatal("trusted authentication did not run")
	}
	if err := connection.upload(ctx, "/tmp/file with 'quote'", strings.NewReader("binary\x00\xffdata")); err != nil {
		t.Fatal(err)
	}
	_ = connection.client.Close()
	for i := 0; i < 2; i++ {
		if strings.Contains(<-fixture.commands, r.Password) {
			t.Fatal("password appeared in SSH command")
		}
	}
	<-fixture.inputs
	if got := <-fixture.inputs; got != "binary\x00\xffdata" {
		t.Fatalf("upload corrupted: %q", got)
	}
	address := fixture.listener.Addr().String()
	_ = fixture.listener.Close()
	replacement := newSSHFixture(t, address)
	r.HostFingerprint = ssh.FingerprintSHA256(replacement.signer.PublicKey())
	if _, err := a.connect(ctx, r); err == nil || !strings.Contains(err.Error(), "不一致") {
		t.Fatalf("changed key not rejected: %v", err)
	}
	if replacement.auth.Load() != 0 {
		t.Fatal("password sent to changed server")
	}
}

func TestInstallDoesNotDeleteOrStopExistingNodes(t *testing.T) {
	r := testRequest()
	p := deploymentPlan{Directory: remoteRoot(r), Service: serviceName(r)}
	command := installCommand("/tmp/ordexus-deploy.test123", p)
	if strings.Contains(command, "rm ") || strings.Contains(command, "pkill") || strings.Contains(command, "systemctl stop") {
		t.Fatal("installation affects existing processes or data")
	}
	if !strings.Contains(command, "mkdir -m 0750 '/opt/ordexus-nodes/fullnode-01'") {
		t.Fatal("node directory not exclusively created")
	}
	unit := serviceUnit(p, r)
	if strings.Contains(unit, r.Password) || !strings.Contains(unit, "User=ordexus-node") || !strings.Contains(unit, "WantedBy=multi-user.target") {
		t.Fatal("service credentials or startup behavior invalid")
	}
}
