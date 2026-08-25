package shielded

import (
	"fmt"

	bn254fr "github.com/consensys/gnark-crypto/ecc/bn254/fr"
	bn254mimc "github.com/consensys/gnark-crypto/ecc/bn254/fr/mimc"
)

const (
	// FieldElementSize 是 BN254 标量域规范大端编码长度。
	FieldElementSize = bn254fr.Bytes
	// CircuitMerkleDepth 是 V2 屏蔽证明电路使用的固定承诺树深度。
	CircuitMerkleDepth = 32
)

const (
	ownerDomainTag     uint64 = 1
	noteDomainTag      uint64 = 2
	nullifierDomainTag uint64 = 3
	merkleDomainTag    uint64 = 4
	nullifierKeyTag    uint64 = 6
)

func mimcFieldHash(domain uint64, values ...Digest) (Digest, error) {
	hasher := bn254mimc.NewMiMC()
	domainElement := bn254fr.NewElement(domain)
	domainBytes := domainElement.Bytes()
	if _, err := hasher.Write(domainBytes[:]); err != nil {
		return Digest{}, fmt.Errorf("shielded: write MiMC domain %d: %w", domain, err)
	}
	for valueIndex, value := range values {
		if err := validateCanonicalField(value[:]); err != nil {
			return Digest{}, fmt.Errorf("shielded: MiMC input %d: %w", valueIndex, err)
		}
		if _, err := hasher.Write(value[:]); err != nil {
			return Digest{}, fmt.Errorf("shielded: write MiMC input %d: %w", valueIndex, err)
		}
	}
	var digest Digest
	copy(digest[:], hasher.Sum(nil))
	return digest, nil
}

func digestFromUint64(value uint64) Digest {
	element := bn254fr.NewElement(value)
	encoded := element.Bytes()
	return Digest(encoded)
}

func digestFromShortBytes(value []byte) (Digest, error) {
	if len(value) > len(Digest{}) {
		return Digest{}, fmt.Errorf("shielded: field input exceeds %d bytes", len(Digest{}))
	}
	var digest Digest
	copy(digest[len(digest)-len(value):], value)
	if err := validateCanonicalField(digest[:]); err != nil {
		return Digest{}, err
	}
	return digest, nil
}

func validateCanonicalField(encoded []byte) error {
	var element bn254fr.Element
	if err := element.SetBytesCanonical(encoded); err != nil {
		return fmt.Errorf("value is not a canonical BN254 field element: %w", err)
	}
	return nil
}
