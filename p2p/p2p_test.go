package p2p

import (
	"bytes"
	"net"
	"strconv"
	"testing"
	"time"

	"solana_golang/utils"
)

// testPeerID 验证目标行为 + 保证核心场景和边界条件稳定。
func testPeerID(seed byte) string {
	return utils.Base58Encode(bytes.Repeat([]byte{seed}, peerIDByteSize))
}

// testAddress 验证目标行为 + 保证核心场景和边界条件稳定。
func testAddress(t *testing.T, protocol utils.MultiAddressProtocol, port int, peerID string) utils.MultiAddress {
	t.Helper()
	address, err := utils.BuildMultiAddress(utils.MultiAddressIP4, "127.0.0.1", protocol, port, peerID)
	if err != nil {
		t.Fatalf("BuildMultiAddress() error = %v", err)
	}
	return address
}

// freeTCPPort 执行对应逻辑 + 保持函数职责清晰可维护。
func freeTCPPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen(:0) error = %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

// waitForTCP 执行对应逻辑 + 保持函数职责清晰可维护。
func waitForTCP(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 50*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("tcp port %d did not open", port)
}
