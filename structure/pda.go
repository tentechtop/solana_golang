package structure

import (
	"crypto/sha256"
	"fmt"
	"math/big"

	"solana_golang/utils"
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

// CreateProgramAddress 派生程序地址 + 使用 seeds 和 programID 计算 PDA。
func CreateProgramAddress(seeds [][]byte, programID PublicKey) (PublicKey, error) {
	if len(seeds) > maxSeeds {
		return PublicKey{}, fmt.Errorf("structure: too many PDA seeds: %d > %d", len(seeds), maxSeeds)
	}

	hasher := sha256.New()
	for seedIndex, seed := range seeds {
		if len(seed) > maxSeedLength {
			return PublicKey{}, fmt.Errorf("structure: PDA seed %d exceeds %d bytes", seedIndex, maxSeedLength)
		}
		hasher.Write(seed)
	}
	hasher.Write(programID[:])
	hasher.Write([]byte(pdaMarker))

	address, err := NewPublicKey(hasher.Sum(nil))
	if err != nil {
		return PublicKey{}, err
	}
	if IsOnEd25519Curve(address[:]) {
		return PublicKey{}, fmt.Errorf("structure: derived address must fall off the Ed25519 curve")
	}
	return address, nil
}

// FindProgramAddress 查找可用 PDA + 从 255 到 0 递减尝试 bump seed。
func FindProgramAddress(seeds [][]byte, programID PublicKey) (PublicKey, byte, error) {
	if len(seeds) >= maxSeeds {
		return PublicKey{}, 0, fmt.Errorf("structure: PDA seeds leave no room for bump seed")
	}
	for bump := 255; bump >= 0; bump-- {
		candidateSeeds := make([][]byte, 0, len(seeds)+1)
		candidateSeeds = append(candidateSeeds, seeds...)
		candidateSeeds = append(candidateSeeds, []byte{byte(bump)})
		address, err := CreateProgramAddress(candidateSeeds, programID)
		if err == nil {
			return address, byte(bump), nil
		}
	}
	return PublicKey{}, 0, fmt.Errorf("structure: unable to find a viable program address bump seed")
}

// CreateWithSeed 派生种子地址 + 匹配 Solana create_with_seed 规则。
func CreateWithSeed(base PublicKey, seed string, owner PublicKey) (PublicKey, error) {
	if len(seed) > maxSeedStringLength {
		return PublicKey{}, fmt.Errorf("structure: seed string exceeds %d bytes", maxSeedStringLength)
	}
	sum := sha256.Sum256(utils.ConcatBytes(base[:], []byte(seed), owner[:]))
	return NewPublicKey(sum[:])
}

// IsOnEd25519Curve 判断是否在 Ed25519 曲线上 + 用于排除有效 PDA 冲突。
func IsOnEd25519Curve(value []byte) bool {
	if len(value) != PublicKeySize {
		return false
	}

	encodedY := utils.CloneBytes(value)
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

// littleEndianBytesToBigInt 执行对应逻辑 + 保持函数职责清晰可维护。
func littleEndianBytesToBigInt(value []byte) *big.Int {
	reversed := utils.ReverseBytes(value)
	return new(big.Int).SetBytes(reversed)
}

// isQuadraticResidue 执行对应逻辑 + 保持函数职责清晰可维护。
func isQuadraticResidue(value *big.Int, prime *big.Int) bool {
	if value.Sign() == 0 {
		return true
	}
	exponent := new(big.Int).Sub(prime, big.NewInt(1))
	exponent.Rsh(exponent, 1)
	return new(big.Int).Exp(value, exponent, prime).Cmp(big.NewInt(1)) == 0
}
