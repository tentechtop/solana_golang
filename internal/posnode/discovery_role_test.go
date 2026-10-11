package posnode

import (
	"testing"

	"github.com/tentechtop/ordexus-core/p2p"
)

func TestAdvertisedP2PRolePrefersBootnodeForFullNode(t *testing.T) {
	disabled := false
	config := nodeConfig{
		ResolvedNodeRole:  p2p.PeerRoleFull,
		ResolvedNodeRoles: []p2p.PeerRole{p2p.PeerRoleFull, p2p.PeerRoleBootnode},
		ValidatorEnabled:  &disabled,
		ConsensusEnabled:  &disabled,
	}

	if got := config.advertisedP2PRole(); got != p2p.PeerRoleBootnode {
		t.Fatalf("advertised p2p role = %q, want %q", got, p2p.PeerRoleBootnode)
	}
	if config.validatorEnabled() {
		t.Fatal("full node with bootnode role must not enable validator")
	}
	if config.consensusEnabled() {
		t.Fatal("full node with bootnode role must not enable consensus")
	}
}

func TestAdvertisedP2PRoleKeepsPrimaryRoleWithoutBootnode(t *testing.T) {
	config := nodeConfig{
		ResolvedNodeRole:  p2p.PeerRoleFull,
		ResolvedNodeRoles: []p2p.PeerRole{p2p.PeerRoleFull},
	}

	if got := config.advertisedP2PRole(); got != p2p.PeerRoleFull {
		t.Fatalf("advertised p2p role = %q, want %q", got, p2p.PeerRoleFull)
	}
}
