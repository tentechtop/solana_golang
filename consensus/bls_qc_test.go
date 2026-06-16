package consensus

import (
	"testing"

	"solana_golang/utils"
)

func TestBLSAggregateQuorumCertificateVerifiesWithBitmap(t *testing.T) {
	blockHash := testHash(33)
	voters := []string{"validator-a", "validator-c"}
	validatorOrder := []string{"validator-a", "validator-b", "validator-c"}
	certificate := QuorumCertificate{
		Type:               VoteTypeConfirm,
		Slot:               42,
		BlockHeight:        7,
		BlockHash:          blockHash,
		ThresholdStake:     67,
		ConfirmedStake:     70,
		Voters:             voters,
		CreatedAtUnixMilli: 1710000000000,
	}
	signaturesByValidator := make(map[string][]byte)
	publicKeysByValidator := make(map[string][]byte)
	for _, validatorID := range validatorOrder {
		keyPair, err := BLSKeyPairFromSeed(utils.SHA256([]byte("bls-" + validatorID)))
		if err != nil {
			t.Fatalf("BLSKeyPairFromSeed() error = %v", err)
		}
		publicKeysByValidator[validatorID] = keyPair.PublicKey
		vote := Vote{
			Type:               certificate.Type,
			Slot:               certificate.Slot,
			BlockHeight:        certificate.BlockHeight,
			BlockHash:          certificate.BlockHash,
			VoterID:            validatorID,
			Stake:              35,
			CreatedAtUnixMilli: certificate.CreatedAtUnixMilli,
		}
		signature, err := SignBLSVote(keyPair.PrivateKey, vote)
		if err != nil {
			t.Fatalf("SignBLSVote() error = %v", err)
		}
		signaturesByValidator[validatorID] = signature
	}

	aggregated, err := AttachBLSAggregate(certificate, validatorOrder, signaturesByValidator)
	if err != nil {
		t.Fatalf("AttachBLSAggregate() error = %v", err)
	}
	if aggregated.SignatureScheme != BLSSignatureSchemeBasic {
		t.Fatalf("signature scheme = %s", aggregated.SignatureScheme)
	}
	if len(aggregated.AggregateSignature) == 0 {
		t.Fatal("aggregate signature is empty")
	}
	if len(aggregated.VoterBitmap) != 1 || aggregated.VoterBitmap[0] != 0b00000101 {
		t.Fatalf("voter bitmap = %08b, want 00000101", aggregated.VoterBitmap)
	}
	if err := VerifyBLSAggregate(aggregated, validatorOrder, publicKeysByValidator); err != nil {
		t.Fatalf("VerifyBLSAggregate() error = %v", err)
	}

	aggregated.BlockHash = testHash(34)
	if err := VerifyBLSAggregate(aggregated, validatorOrder, publicKeysByValidator); err == nil {
		t.Fatal("VerifyBLSAggregate() error = nil, want invalid signature")
	}
}

func TestBLSQuorumCertificateRoundTrip(t *testing.T) {
	blockHash := testHash(44)
	validatorOrder := []string{"validator-a"}
	keyPair, err := BLSKeyPairFromSeed(utils.SHA256([]byte("bls-round-trip")))
	if err != nil {
		t.Fatalf("BLSKeyPairFromSeed() error = %v", err)
	}
	certificate := QuorumCertificate{
		Type:               VoteTypeConfirm,
		Slot:               50,
		BlockHeight:        8,
		BlockHash:          blockHash,
		ThresholdStake:     1,
		ConfirmedStake:     1,
		Voters:             []string{"validator-a"},
		CreatedAtUnixMilli: 1710000000000,
	}
	signature, err := SignBLSVote(keyPair.PrivateKey, Vote{
		Type:               certificate.Type,
		Slot:               certificate.Slot,
		BlockHeight:        certificate.BlockHeight,
		BlockHash:          certificate.BlockHash,
		VoterID:            "validator-a",
		Stake:              1,
		CreatedAtUnixMilli: certificate.CreatedAtUnixMilli,
	})
	if err != nil {
		t.Fatalf("SignBLSVote() error = %v", err)
	}
	aggregated, err := AttachBLSAggregate(certificate, validatorOrder, map[string][]byte{"validator-a": signature})
	if err != nil {
		t.Fatalf("AttachBLSAggregate() error = %v", err)
	}
	encoded, err := aggregated.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	decoded, err := UnmarshalCertificateBinary(encoded)
	if err != nil {
		t.Fatalf("UnmarshalCertificateBinary() error = %v", err)
	}
	if err := VerifyBLSAggregate(decoded, validatorOrder, map[string][]byte{"validator-a": keyPair.PublicKey}); err != nil {
		t.Fatalf("VerifyBLSAggregate(decoded) error = %v", err)
	}
}

func TestVoteCollectorFormsBLSQuorumCertificate(t *testing.T) {
	blockHash := testHash(55)
	validatorOrder := []string{"validator-a", "validator-b", "validator-c"}
	stakeByValidator := map[string]uint64{
		"validator-a": 40,
		"validator-b": 30,
		"validator-c": 30,
	}
	collector, err := NewVoteCollector(stakeByValidator, Quorum{Numerator: 2, Denominator: 3})
	if err != nil {
		t.Fatalf("NewVoteCollector() error = %v", err)
	}
	keyPairs := make(map[string]BLSKeyPair)
	publicKeysByValidator := make(map[string][]byte)
	for _, validatorID := range validatorOrder {
		keyPair, err := BLSKeyPairFromSeed(utils.SHA256([]byte("collector-" + validatorID)))
		if err != nil {
			t.Fatalf("BLSKeyPairFromSeed() error = %v", err)
		}
		keyPairs[validatorID] = keyPair
		publicKeysByValidator[validatorID] = keyPair.PublicKey
	}

	voters := []string{"validator-a", "validator-b"}
	var certificate QuorumCertificate
	var formed bool
	for _, voterID := range voters {
		vote := Vote{
			Type:               VoteTypeConfirm,
			Slot:               77,
			BlockHeight:        11,
			BlockHash:          blockHash,
			VoterID:            voterID,
			Stake:              stakeByValidator[voterID],
			CreatedAtUnixMilli: 1710000000000,
		}
		signature, err := SignBLSVote(keyPairs[voterID].PrivateKey, vote)
		if err != nil {
			t.Fatalf("SignBLSVote() error = %v", err)
		}
		certificate, formed, err = collector.AddVoteWithBLS(vote, signature, validatorOrder)
		if err != nil {
			t.Fatalf("AddVoteWithBLS() error = %v", err)
		}
	}
	if !formed {
		t.Fatal("formed = false, want true")
	}
	if certificate.SignatureScheme != BLSSignatureSchemeBasic {
		t.Fatalf("signature scheme = %s", certificate.SignatureScheme)
	}
	if err := VerifyBLSAggregate(certificate, validatorOrder, publicKeysByValidator); err != nil {
		t.Fatalf("VerifyBLSAggregate() error = %v", err)
	}
}

func TestVerifyBLSAggregateWithStakeRejectsInflatedStake(t *testing.T) {
	blockHash := testHash(66)
	validatorOrder := []string{"validator-a", "validator-b", "validator-c"}
	stakeByValidator := map[string]uint64{
		"validator-a": 10,
		"validator-b": 45,
		"validator-c": 45,
	}
	keyPair, err := BLSKeyPairFromSeed(utils.SHA256([]byte("inflated-stake-validator-a")))
	if err != nil {
		t.Fatalf("BLSKeyPairFromSeed() error = %v", err)
	}
	publicKeysByValidator := map[string][]byte{"validator-a": keyPair.PublicKey}
	certificate := QuorumCertificate{
		Type:               VoteTypeConfirm,
		Slot:               88,
		BlockHeight:        12,
		BlockHash:          blockHash,
		ThresholdStake:     67,
		ConfirmedStake:     67,
		Voters:             []string{"validator-a"},
		CreatedAtUnixMilli: 1710000000000,
	}
	signature, err := SignBLSVote(keyPair.PrivateKey, Vote{
		Type:               certificate.Type,
		Slot:               certificate.Slot,
		BlockHeight:        certificate.BlockHeight,
		BlockHash:          certificate.BlockHash,
		VoterID:            "validator-a",
		Stake:              stakeByValidator["validator-a"],
		CreatedAtUnixMilli: certificate.CreatedAtUnixMilli,
	})
	if err != nil {
		t.Fatalf("SignBLSVote() error = %v", err)
	}
	aggregated, err := AttachBLSAggregate(certificate, validatorOrder, map[string][]byte{"validator-a": signature})
	if err != nil {
		t.Fatalf("AttachBLSAggregate() error = %v", err)
	}
	err = VerifyBLSAggregateWithStake(aggregated, validatorOrder, publicKeysByValidator, stakeByValidator, Quorum{Numerator: 2, Denominator: 3})
	if err == nil {
		t.Fatal("VerifyBLSAggregateWithStake() error = nil, want inflated stake rejection")
	}
}
