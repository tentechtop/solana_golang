package consensus

import (
	"bytes"
	"testing"

	"github.com/tentechtop/ordexus-core/structure"
	"github.com/tentechtop/ordexus-core/utils"
)

func TestEpochTransitionSignBytesBindValidatorSetRoots(t *testing.T) {
	transition := testEpochTransition(t)
	first, err := transition.SignBytes()
	if err != nil {
		t.Fatalf("SignBytes() error = %v", err)
	}
	transition.ToSnapshotRoot = epochTransitionTestHash(t, "other-target").String()
	second, err := transition.SignBytes()
	if err != nil {
		t.Fatalf("SignBytes(changed) error = %v", err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("SignBytes() did not bind target snapshot root")
	}
}

func TestEpochTransitionRejectsSkippedEpoch(t *testing.T) {
	transition := testEpochTransition(t)
	transition.ToEpochID = transition.FromEpochID + 2
	if err := transition.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want skipped epoch rejection")
	}
}

func TestEpochTransitionCertificateRejectsDuplicateSigner(t *testing.T) {
	certificate := EpochTransitionCertificate{
		Transition: testEpochTransition(t),
		Signatures: []EpochTransitionSignature{
			{ValidatorID: "validator-a", PublicKey: "key-a", Signature: "signature-a"},
			{ValidatorID: "validator-a", PublicKey: "key-a", Signature: "signature-b"},
		},
	}
	if err := certificate.ValidateStructure(); err == nil {
		t.Fatal("ValidateStructure() error = nil, want duplicate signer rejection")
	}
}

func testEpochTransition(t testing.TB) EpochTransition {
	t.Helper()
	return EpochTransition{
		ChainID:           "transition-test",
		ChainIdentityHash: epochTransitionTestHash(t, "chain-identity").String(),
		GenesisHash:       epochTransitionTestHash(t, "genesis").String(),
		FromEpochID:       2,
		ToEpochID:         3,
		SourceBlockHeight: 19,
		SourceBlockHash:   epochTransitionTestHash(t, "source").String(),
		FromSnapshotRoot:  epochTransitionTestHash(t, "from").String(),
		ToSnapshotRoot:    epochTransitionTestHash(t, "to").String(),
	}
}

func epochTransitionTestHash(t testing.TB, value string) structure.Hash {
	t.Helper()
	hash, err := structure.NewHash(utils.SHA256([]byte(value)))
	if err != nil {
		t.Fatalf("NewHash() error = %v", err)
	}
	return hash
}
