package utils

import (
	"crypto/sha256"
	"fmt"
	"math/big"
)

const (
	maxSeedLength       = 32
	maxSeeds            = 16
	pdaMarker           = "ProgramDerivedAddress"
	maxSeedStringLength = 32
)

var (
	ed25519Prime = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 255), big.NewInt(19))
	ed25519D     = func() *big.Int {
		numerator := big.NewInt(-121665)
		denominator := big.NewInt(121666)
		denominator.ModInverse(denominator, ed25519Prime)
		return numerator.Mul(numerator, denominator).Mod(numerator, ed25519Prime)
	}()
)

// CreateProgramAddress derives a Solana program address from seeds and programID.
func CreateProgramAddress(seeds [][]byte, programID PublicKey) (PublicKey, error) {
	if len(seeds) > maxSeeds {
		return PublicKey{}, fmt.Errorf("utils: too many PDA seeds: %d > %d", len(seeds), maxSeeds)
	}

	hasher := sha256.New()
	for i, seed := range seeds {
		if len(seed) > maxSeedLength {
			return PublicKey{}, fmt.Errorf("utils: PDA seed %d exceeds %d bytes", i, maxSeedLength)
		}
		hasher.Write(seed)
	}
	hasher.Write(programID[:])
	hasher.Write([]byte(pdaMarker))
	sum := hasher.Sum(nil)

	address, err := NewPublicKey(sum)
	if err != nil {
		return PublicKey{}, err
	}
	if IsOnEd25519Curve(address[:]) {
		return PublicKey{}, fmt.Errorf("utils: derived address must fall off the Ed25519 curve")
	}
	return address, nil
}

// FindProgramAddress finds the first valid PDA and bump seed, trying bumps from 255 down to 0.
func FindProgramAddress(seeds [][]byte, programID PublicKey) (PublicKey, byte, error) {
	if len(seeds) >= maxSeeds {
		return PublicKey{}, 0, fmt.Errorf("utils: PDA seeds leave no room for bump seed")
	}
	for bump := 255; bump >= 0; bump-- {
		bumpSeed := []byte{byte(bump)}
		candidateSeeds := make([][]byte, 0, len(seeds)+1)
		candidateSeeds = append(candidateSeeds, seeds...)
		candidateSeeds = append(candidateSeeds, bumpSeed)
		address, err := CreateProgramAddress(candidateSeeds, programID)
		if err == nil {
			return address, byte(bump), nil
		}
	}
	return PublicKey{}, 0, fmt.Errorf("utils: unable to find a viable program address bump seed")
}

// CreateWithSeed derives SHA256(base || seed || owner), matching Solana's create_with_seed.
func CreateWithSeed(base PublicKey, seed string, owner PublicKey) (PublicKey, error) {
	if len(seed) > maxSeedStringLength {
		return PublicKey{}, fmt.Errorf("utils: seed string exceeds %d bytes", maxSeedStringLength)
	}
	sum := sha256.Sum256(ConcatBytes(base[:], []byte(seed), owner[:]))
	return NewPublicKey(sum[:])
}

// IsOnEd25519Curve reports whether value is a valid compressed Ed25519 curve point.
func IsOnEd25519Curve(value []byte) bool {
	if len(value) != PublicKeySize {
		return false
	}

	encodedY := CloneBytes(value)
	encodedY[31] &= 0x7f
	y := littleEndianBytesToBigInt(encodedY)
	if y.Cmp(ed25519Prime) >= 0 {
		return false
	}

	y2 := new(big.Int).Mul(y, y)
	y2.Mod(y2, ed25519Prime)

	numerator := new(big.Int).Sub(y2, big.NewInt(1))
	numerator.Mod(numerator, ed25519Prime)

	denominator := new(big.Int).Mul(ed25519D, y2)
	denominator.Add(denominator, big.NewInt(1))
	denominator.Mod(denominator, ed25519Prime)
	if denominator.Sign() == 0 {
		return false
	}

	denominatorInv := new(big.Int).ModInverse(denominator, ed25519Prime)
	if denominatorInv == nil {
		return false
	}
	x2 := numerator.Mul(numerator, denominatorInv)
	x2.Mod(x2, ed25519Prime)
	if x2.Sign() == 0 && value[31]&0x80 != 0 {
		return false
	}
	return isQuadraticResidue(x2, ed25519Prime)
}

func littleEndianBytesToBigInt(value []byte) *big.Int {
	reversed := ReverseBytes(value)
	return new(big.Int).SetBytes(reversed)
}

func isQuadraticResidue(value *big.Int, prime *big.Int) bool {
	if value.Sign() == 0 {
		return true
	}
	exponent := new(big.Int).Sub(prime, big.NewInt(1))
	exponent.Rsh(exponent, 1)
	return new(big.Int).Exp(value, exponent, prime).Cmp(big.NewInt(1)) == 0
}
