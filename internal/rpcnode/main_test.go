package rpcnode

import (
	"testing"

	"solana_golang/internal/poswire"
	"solana_golang/p2p"
	"solana_golang/rpc"
	"solana_golang/structure"
	"solana_golang/utils"
)

func TestNewStaticPeerMarksValidatorCapability(t *testing.T) {
	peerID := testPeerID(1)
	peer, err := newStaticPeer(peerConfig{
		PeerID:       peerID,
		IP:           "127.0.0.1",
		Port:         5101,
		Role:         "validator",
		Roles:        []string{"full"},
		Capabilities: []string{"validator", "relay", "state_sync"},
	})
	if err != nil {
		t.Fatalf("newStaticPeer() error = %v", err)
	}
	if peer.Role != p2p.PeerRoleValidator {
		t.Fatalf("Role = %q, want validator", peer.Role)
	}
	if !peer.Validator {
		t.Fatal("Validator = false, want true")
	}
	if peer.Capabilities&p2p.PeerCapabilityValidator == 0 {
		t.Fatalf("Capabilities = %d, want validator bit", peer.Capabilities)
	}
	if peer.Capabilities&p2p.PeerCapabilityStateSync == 0 {
		t.Fatalf("Capabilities = %d, want state sync bit", peer.Capabilities)
	}
}

func TestNewStaticPeerUsesConfiguredQUICNetwork(t *testing.T) {
	peerID := testPeerID(11)
	peer, err := newStaticPeer(peerConfig{
		PeerID:  peerID,
		IP:      "127.0.0.1",
		Port:    5101,
		Network: "quic",
	})
	if err != nil {
		t.Fatalf("newStaticPeer() error = %v", err)
	}
	if len(peer.Addresses) != 1 {
		t.Fatalf("Addresses length = %d, want 1", len(peer.Addresses))
	}
	if peer.Addresses[0].Protocol != utils.ProtocolQUIC {
		t.Fatalf("Protocol = %q, want quic", peer.Addresses[0].Protocol)
	}
}

func TestNormalizeConfigDefaultsNetworkToTCP(t *testing.T) {
	normalized, err := normalizeConfig(rpcNodeConfig{
		NodeName:         "rpc-101",
		ListenIP:         "0.0.0.0",
		ListenPort:       5101,
		PeerSeed:         "node-101",
		AllowInsecureP2P: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("normalizeConfig() error = %v", err)
	}
	if normalized.Network != string(utils.ProtocolTCP) {
		t.Fatalf("Network = %q, want tcp", normalized.Network)
	}
}

func TestNewStaticPeerKeepsPublicRPCOutOfValidatorSet(t *testing.T) {
	peerID := testPeerID(2)
	peer, err := newStaticPeer(peerConfig{
		PeerID:       peerID,
		IP:           "127.0.0.1",
		Port:         5101,
		Role:         "public_rpc",
		Capabilities: []string{"relay", "dht"},
	})
	if err != nil {
		t.Fatalf("newStaticPeer() error = %v", err)
	}
	if peer.Role != p2p.PeerRolePublicRPC {
		t.Fatalf("Role = %q, want public_rpc", peer.Role)
	}
	if peer.Validator {
		t.Fatal("Validator = true, want false")
	}
	if peer.Capabilities&p2p.PeerCapabilityValidator != 0 {
		t.Fatalf("Capabilities = %d, want no validator bit", peer.Capabilities)
	}
}

func TestNormalizeConfigRequiresNetworkIDForSecureRPCNode(t *testing.T) {
	_, err := normalizeConfig(rpcNodeConfig{
		NodeName:         "rpc-101",
		ListenIP:         "0.0.0.0",
		ListenPort:       5101,
		PeerSeed:         "node-101",
		AllowInsecureP2P: boolPtr(false),
	})
	if err == nil {
		t.Fatal("normalizeConfig() error = nil, want secure network id error")
	}
}

func TestNormalizeConfigRejectsValidatorRoleForRPCNode(t *testing.T) {
	_, err := normalizeConfig(rpcNodeConfig{
		NodeName:         "rpc-101",
		ListenIP:         "0.0.0.0",
		ListenPort:       5101,
		PeerSeed:         "node-101",
		AllowInsecureP2P: boolPtr(true),
		NodeRoles:        []string{"public_rpc", "validator"},
	})
	if err == nil {
		t.Fatal("normalizeConfig() error = nil, want rpc node role rejection")
	}
}

func TestNewStaticPeerRejectsInvalidCapability(t *testing.T) {
	_, err := newStaticPeer(peerConfig{
		PeerID:       testPeerID(3),
		IP:           "127.0.0.1",
		Port:         5101,
		Role:         "validator",
		Capabilities: []string{"validator", "bad-capability"},
	})
	if err == nil {
		t.Fatal("newStaticPeer() error = nil, want invalid capability rejection")
	}
}

func TestForwardedMethodsDoNotExposeLocalValidatorIdentity(t *testing.T) {
	for _, method := range forwardedMethods() {
		if method == rpc.MethodGetLocalValidatorIdentity {
			t.Fatal("forwarded methods must not expose local validator identity")
		}
		if method == rpc.MethodRegisterValidator || method == rpc.MethodStake {
			t.Fatalf("forwarded methods must not expose wallet-local signing method %s", method)
		}
	}
}

func TestForwardedMethodsExposeSignedTransactionAndReadOnlyContractQueries(t *testing.T) {
	methods := map[string]struct{}{}
	for _, method := range forwardedMethods() {
		methods[method] = struct{}{}
	}
	for _, method := range []string{rpc.MethodSendTransaction, rpc.MethodGetContractPrograms, rpc.MethodGetAssetState} {
		if _, exists := methods[method]; !exists {
			t.Fatalf("forwarded methods missing %s", method)
		}
	}
}

func TestRPCNodeIgnoredUnregisteredProtocolsKeepRPCForwardVisible(t *testing.T) {
	ignoredProtocols := map[p2p.ProtocolID]struct{}{}
	for _, protocolID := range rpcNodeIgnoredUnregisteredProtocols() {
		ignoredProtocols[protocolID] = struct{}{}
	}
	if _, exists := ignoredProtocols[p2p.ProtocolPoSStatusV1]; !exists {
		t.Fatal("rpcnode ignored protocols missing pos status")
	}
	if _, exists := ignoredProtocols[p2p.ProtocolPoSRPCForwardV1]; exists {
		t.Fatal("rpcnode must not ignore pos rpc forward protocol")
	}
}

func TestRotatePeerIDsStartsAtModuloIndex(t *testing.T) {
	result := rotatePeerIDs([]string{"a", "b", "c"}, 4)
	expected := []string{"b", "c", "a"}
	if len(result) != len(expected) {
		t.Fatalf("rotatePeerIDs length = %d, want %d", len(result), len(expected))
	}
	for index := range expected {
		if result[index] != expected[index] {
			t.Fatalf("rotatePeerIDs()[%d] = %q, want %q", index, result[index], expected[index])
		}
	}
}

func TestRetryableTransactionForwardErrorRecognizesBlockhashSkew(t *testing.T) {
	forwardError := &poswire.RPCForwardError{
		Message: "internal error",
		Data:    []byte(`"send transaction: posnode: preflight transaction failed: recent blockhash is not valid"`),
	}
	if !isRetryableTransactionForwardError(forwardError) {
		t.Fatal("isRetryableTransactionForwardError() = false, want true")
	}
}

func TestRetryableTransactionForwardErrorRejectsBusinessFailure(t *testing.T) {
	forwardError := &poswire.RPCForwardError{
		Message: "internal error",
		Data:    []byte(`"send transaction: posnode: preflight transaction failed: insufficient funds"`),
	}
	if isRetryableTransactionForwardError(forwardError) {
		t.Fatal("isRetryableTransactionForwardError() = true, want false")
	}
}

func TestTransactionFanoutTargetsWrapAfterAcceptedPeer(t *testing.T) {
	result := transactionFanoutTargets([]string{"a", "b", "c", "d"}, 2, 3)
	expected := []string{"d", "a", "b"}
	if len(result) != len(expected) {
		t.Fatalf("transactionFanoutTargets length = %d, want %d", len(result), len(expected))
	}
	for index := range expected {
		if result[index] != expected[index] {
			t.Fatalf("transactionFanoutTargets()[%d] = %q, want %q", index, result[index], expected[index])
		}
	}
}

func TestTransactionFanoutTargetsHonorsLimit(t *testing.T) {
	result := transactionFanoutTargets([]string{"a", "b", "c", "d"}, 0, 2)
	expected := []string{"b", "c"}
	if len(result) != len(expected) {
		t.Fatalf("transactionFanoutTargets length = %d, want %d", len(result), len(expected))
	}
	for index := range expected {
		if result[index] != expected[index] {
			t.Fatalf("transactionFanoutTargets()[%d] = %q, want %q", index, result[index], expected[index])
		}
	}
}

func TestRPCForwardResultTextDecodesJSONString(t *testing.T) {
	result := rpcForwardResultText([]byte(`"abc123"`))
	if result != "abc123" {
		t.Fatalf("rpcForwardResultText() = %q, want abc123", result)
	}
}

func TestStableLatestBlockhashFromStatusUsesFinalizedHash(t *testing.T) {
	result, ok := stableLatestBlockhashFromStatus(upstreamBlockhashStatus{
		FinalizedHash:   "finalized-hash",
		FinalizedHeight: 20,
		FinalizedSlot:   24,
		HeadSlot:        25,
	})
	if !ok {
		t.Fatal("stableLatestBlockhashFromStatus() ok = false, want true")
	}
	if result.Blockhash != "finalized-hash" {
		t.Fatalf("blockhash = %q, want finalized-hash", result.Blockhash)
	}
	if result.Height != 20 {
		t.Fatalf("height = %d, want 20", result.Height)
	}
	if result.Slot != 24 {
		t.Fatalf("slot = %d, want 24", result.Slot)
	}
	if result.LastValidSlot <= result.Slot {
		t.Fatalf("last valid slot = %d, want greater than slot %d", result.LastValidSlot, result.Slot)
	}
}

func TestStableLatestBlockhashFromStatusRejectsMissingFinalizedHash(t *testing.T) {
	if _, ok := stableLatestBlockhashFromStatus(upstreamBlockhashStatus{HeadHash: "head"}); ok {
		t.Fatal("stableLatestBlockhashFromStatus() ok = true, want false")
	}
}

func TestStableLatestBlockhashFromStatusRejectsMissingFinalizedSlot(t *testing.T) {
	_, ok := stableLatestBlockhashFromStatus(upstreamBlockhashStatus{
		FinalizedHash:   "finalized-hash",
		FinalizedHeight: 20,
		HeadSlot:        25,
	})
	if ok {
		t.Fatal("stableLatestBlockhashFromStatus() ok = true, want false")
	}
}

func TestStableLatestBlockhashFromStatusRejectsExpiringFinalizedHash(t *testing.T) {
	_, ok := stableLatestBlockhashFromStatus(upstreamBlockhashStatus{
		FinalizedHash:   "finalized-hash",
		FinalizedHeight: 20,
		FinalizedSlot:   100,
		HeadSlot:        100 + structure.MaxRecentBlockhashAgeSlots - stableBlockhashMinSlots + 1,
	})
	if ok {
		t.Fatal("stableLatestBlockhashFromStatus() ok = true, want false")
	}
}

func testPeerID(lastByte byte) string {
	seed := make([]byte, 32)
	seed[31] = lastByte
	return utils.Base58Encode(seed)
}

func boolPtr(value bool) *bool {
	return &value
}
