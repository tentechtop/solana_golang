package zk

import (
	"bytes"
	"crypto/elliptic"
	"crypto/rand"
	"fmt"
	"math/big"
)

const (
	MaxThresholdShareCount      = 255
	domainThresholdDecryptProof = "solana_golang.zk.threshold.decrypt.v1"
)

// ThresholdPublicKeySet 描述门限公钥集合 + 用多项式承诺验证份额和解密份额。
type ThresholdPublicKeySet struct {
	Threshold   int
	Total       int
	PublicKey   []byte
	Commitments [][]byte
}

// ThresholdShare 描述 Shamir 门限份额 + 用固定索引和值支持监管门限恢复。
type ThresholdShare struct {
	Index uint8
	Value []byte
}

// ThresholdDecryptionShare 描述门限解密份额 + 不暴露监管私钥份额。
type ThresholdDecryptionShare struct {
	Index           uint8
	DecryptionPoint []byte
	Proof           ThresholdDecryptionShareProof
}

// ThresholdDecryptionShareProof 描述解密份额正确性证明 + 证明同一份额作用于 G 和密文 R。
type ThresholdDecryptionShareProof struct {
	BaseNonce       []byte
	CiphertextNonce []byte
	Response        []byte
}

// SplitScalar 拆分私钥标量 + 使用 P-256 标量域生成 Shamir 多项式份额。
func SplitScalar(secret []byte, threshold int, total int) ([]ThresholdShare, error) {
	_, shares, err := SplitScalarWithPublicKeySet(secret, threshold, total)
	return shares, err
}

// SplitScalarWithPublicKeySet 拆分私钥标量并输出公钥承诺 + 支持不重构私钥的门限解密验证。
func SplitScalarWithPublicKeySet(secret []byte, threshold int, total int) (ThresholdPublicKeySet, []ThresholdShare, error) {
	if err := validateThresholdConfig(threshold, total); err != nil {
		return ThresholdPublicKeySet{}, nil, err
	}
	secretValue, err := scalarFromBytes(secret, false)
	if err != nil {
		return ThresholdPublicKeySet{}, nil, err
	}
	coefficients, err := newThresholdPolynomial(secretValue, threshold)
	if err != nil {
		return ThresholdPublicKeySet{}, nil, err
	}
	publicKeySet := thresholdPublicKeySetFromPolynomial(coefficients, threshold, total)
	return publicKeySet, evaluateThresholdShares(coefficients, total), nil
}

// RecoverScalar 恢复私钥标量 + 仅保留兼容用途，生产审计使用门限解密份额。
func RecoverScalar(shares []ThresholdShare, threshold int) ([]byte, error) {
	if err := validateSharesForRecovery(shares, threshold); err != nil {
		return nil, err
	}
	order := elliptic.P256().Params().N
	secret := big.NewInt(0)
	for shareIndex := 0; shareIndex < threshold; shareIndex++ {
		term, err := recoverShareTerm(shares[:threshold], shareIndex, order)
		if err != nil {
			return nil, err
		}
		secret.Add(secret, term)
		secret.Mod(secret, order)
	}
	if secret.Sign() == 0 {
		return nil, fmt.Errorf("%w: recovered scalar is zero", ErrInvalidProof)
	}
	return padScalar(secret), nil
}

// NewThresholdDecryptionShare 创建门限解密份额 + 单个监管节点只输出 share_i * R 和正确性证明。
func NewThresholdDecryptionShare(share ThresholdShare, ciphertext ElGamalCiphertext) (ThresholdDecryptionShare, error) {
	if share.Index == 0 {
		return ThresholdDecryptionShare{}, fmt.Errorf("%w: zero share index", ErrInvalidPublicInput)
	}
	shareValue, err := scalarFromBytes(share.Value, false)
	if err != nil {
		return ThresholdDecryptionShare{}, err
	}
	noncePoint, err := pointFromBytes(ciphertext.NonceCommitment)
	if err != nil {
		return ThresholdDecryptionShare{}, err
	}
	decryptionPoint := scalarPoint(noncePoint, shareValue)
	verificationPoint := scalarBasePoint(shareValue)
	proof, err := newThresholdDecryptionShareProof(share.Index, noncePoint, verificationPoint, decryptionPoint, shareValue)
	if err != nil {
		return ThresholdDecryptionShare{}, err
	}
	return ThresholdDecryptionShare{
		Index:           share.Index,
		DecryptionPoint: pointBytes(decryptionPoint),
		Proof:           proof,
	}, nil
}

// VerifyThresholdDecryptionShare 校验门限解密份额 + 使用公钥承诺防止错误份额污染解密。
func VerifyThresholdDecryptionShare(publicKeySet ThresholdPublicKeySet, ciphertext ElGamalCiphertext, share ThresholdDecryptionShare) error {
	if err := publicKeySet.Validate(); err != nil {
		return err
	}
	if share.Index == 0 || int(share.Index) > publicKeySet.Total {
		return fmt.Errorf("%w: decryption share index out of range", ErrInvalidPublicInput)
	}
	noncePoint, err := pointFromBytes(ciphertext.NonceCommitment)
	if err != nil {
		return err
	}
	decryptionPoint, err := pointFromBytes(share.DecryptionPoint)
	if err != nil {
		return err
	}
	verificationPoint, err := publicKeySet.verificationPoint(share.Index)
	if err != nil {
		return err
	}
	return verifyThresholdDecryptionShareProof(share.Index, noncePoint, verificationPoint, decryptionPoint, share.Proof)
}

// DecryptAmountWithThresholdShares 聚合门限解密份额 + 不恢复完整监管私钥。
func DecryptAmountWithThresholdShares(publicKeySet ThresholdPublicKeySet, ciphertext ElGamalCiphertext, shares []ThresholdDecryptionShare, maxAmount uint64) (uint64, error) {
	selectedShares, err := validateThresholdDecryptionShares(publicKeySet, ciphertext, shares)
	if err != nil {
		return 0, err
	}
	sharedPoint, err := combineThresholdDecryptionPoints(selectedShares)
	if err != nil {
		return 0, err
	}
	cipherPoint, err := pointFromBytes(ciphertext.CiphertextPoint)
	if err != nil {
		return 0, err
	}
	amountPoint := subtractPoints(cipherPoint, sharedPoint)
	return decodeAmountPoint(amountPoint, maxAmount)
}

// DecryptAmountWithThresholdPrivateShares 模拟分布式门限解密 + 每个私钥份额只生成解密份额。
func DecryptAmountWithThresholdPrivateShares(publicKeySet ThresholdPublicKeySet, ciphertext ElGamalCiphertext, shares []ThresholdShare, maxAmount uint64) (uint64, error) {
	if err := validateSharesForRecovery(shares, publicKeySet.Threshold); err != nil {
		return 0, err
	}
	for _, share := range shares {
		if err := VerifyThresholdShare(publicKeySet, share); err != nil {
			return 0, err
		}
	}
	decryptionShares := make([]ThresholdDecryptionShare, 0, publicKeySet.Threshold)
	for _, share := range shares[:publicKeySet.Threshold] {
		decryptionShare, err := NewThresholdDecryptionShare(share, ciphertext)
		if err != nil {
			return 0, err
		}
		decryptionShares = append(decryptionShares, decryptionShare)
	}
	return DecryptAmountWithThresholdShares(publicKeySet, ciphertext, decryptionShares, maxAmount)
}

// VerifyThresholdShare 校验 Shamir 私钥份额 + 使用多项式公钥承诺验证份额归属。
func VerifyThresholdShare(publicKeySet ThresholdPublicKeySet, share ThresholdShare) error {
	if err := publicKeySet.Validate(); err != nil {
		return err
	}
	if share.Index == 0 || int(share.Index) > publicKeySet.Total {
		return fmt.Errorf("%w: share index out of range", ErrInvalidPublicInput)
	}
	shareValue, err := scalarFromBytes(share.Value, false)
	if err != nil {
		return err
	}
	expectedPoint, err := publicKeySet.verificationPoint(share.Index)
	if err != nil {
		return err
	}
	actualPoint := scalarBasePoint(shareValue)
	if !samePoint(actualPoint, expectedPoint) {
		return fmt.Errorf("%w: threshold share does not match public commitments", ErrVerificationFailed)
	}
	return nil
}

func validateThresholdConfig(threshold int, total int) error {
	if threshold < 2 {
		return fmt.Errorf("%w: threshold %d below 2", ErrInvalidPublicInput, threshold)
	}
	if total < threshold {
		return fmt.Errorf("%w: total %d below threshold %d", ErrInvalidPublicInput, total, threshold)
	}
	if total > MaxThresholdShareCount {
		return fmt.Errorf("%w: total %d exceeds %d", ErrInvalidPublicInput, total, MaxThresholdShareCount)
	}
	return nil
}

// Validate 校验门限公钥集合 + 防止无效承诺进入解密验证。
func (publicKeySet ThresholdPublicKeySet) Validate() error {
	if err := validateThresholdConfig(publicKeySet.Threshold, publicKeySet.Total); err != nil {
		return err
	}
	if len(publicKeySet.Commitments) != publicKeySet.Threshold {
		return fmt.Errorf("%w: commitment count mismatch", ErrInvalidPublicInput)
	}
	if len(publicKeySet.PublicKey) != confidentialPointSize {
		return fmt.Errorf("%w: threshold public key length mismatch", ErrInvalidPublicInput)
	}
	for _, commitment := range publicKeySet.Commitments {
		if _, err := pointFromBytes(commitment); err != nil {
			return err
		}
	}
	if !bytes.Equal(publicKeySet.PublicKey, publicKeySet.Commitments[0]) {
		return fmt.Errorf("%w: threshold public key does not match constant commitment", ErrInvalidPublicInput)
	}
	return nil
}

func newThresholdPolynomial(secret *big.Int, threshold int) ([]*big.Int, error) {
	order := elliptic.P256().Params().N
	coefficients := make([]*big.Int, threshold)
	coefficients[0] = new(big.Int).Set(secret)
	for index := 1; index < threshold; index++ {
		value, err := randomScalar(rand.Reader, order)
		if err != nil {
			return nil, err
		}
		coefficients[index] = value
	}
	return coefficients, nil
}

func thresholdPublicKeySetFromPolynomial(coefficients []*big.Int, threshold int, total int) ThresholdPublicKeySet {
	commitments := make([][]byte, threshold)
	for index := 0; index < threshold; index++ {
		commitments[index] = pointBytes(scalarBasePoint(coefficients[index]))
	}
	return ThresholdPublicKeySet{
		Threshold:   threshold,
		Total:       total,
		PublicKey:   cloneBytes(commitments[0]),
		Commitments: cloneByteSlices(commitments),
	}
}

func evaluateThresholdShares(coefficients []*big.Int, total int) []ThresholdShare {
	order := elliptic.P256().Params().N
	shares := make([]ThresholdShare, total)
	for index := 0; index < total; index++ {
		xValue := big.NewInt(int64(index + 1))
		yValue := evaluateThresholdPolynomial(coefficients, xValue, order)
		shares[index] = ThresholdShare{Index: uint8(index + 1), Value: padScalar(yValue)}
	}
	return shares
}

func (publicKeySet ThresholdPublicKeySet) verificationPoint(index uint8) (p256Point, error) {
	if err := publicKeySet.Validate(); err != nil {
		return p256Point{}, err
	}
	xValue := big.NewInt(int64(index))
	power := big.NewInt(1)
	result := infinityPoint()
	order := elliptic.P256().Params().N
	for _, commitment := range publicKeySet.Commitments {
		commitmentPoint, err := pointFromBytes(commitment)
		if err != nil {
			return p256Point{}, err
		}
		result = addPoints(result, scalarPoint(commitmentPoint, power))
		power.Mul(power, xValue)
		power.Mod(power, order)
	}
	return result, nil
}

func evaluateThresholdPolynomial(coefficients []*big.Int, xValue *big.Int, order *big.Int) *big.Int {
	result := big.NewInt(0)
	power := big.NewInt(1)
	for _, coefficient := range coefficients {
		term := new(big.Int).Mul(coefficient, power)
		result.Add(result, term)
		result.Mod(result, order)
		power.Mul(power, xValue)
		power.Mod(power, order)
	}
	return result
}

func validateSharesForRecovery(shares []ThresholdShare, threshold int) error {
	if threshold < 2 {
		return fmt.Errorf("%w: threshold %d below 2", ErrInvalidPublicInput, threshold)
	}
	if len(shares) < threshold {
		return fmt.Errorf("%w: share count %d below threshold %d", ErrInvalidPublicInput, len(shares), threshold)
	}
	if len(shares) > MaxThresholdShareCount {
		return fmt.Errorf("%w: share count %d exceeds %d", ErrInvalidPublicInput, len(shares), MaxThresholdShareCount)
	}
	return validateUniqueShares(shares)
}

func validateUniqueShares(shares []ThresholdShare) error {
	seen := make(map[uint8]struct{}, len(shares))
	for _, share := range shares {
		if share.Index == 0 {
			return fmt.Errorf("%w: zero share index", ErrInvalidPublicInput)
		}
		if _, exists := seen[share.Index]; exists {
			return fmt.Errorf("%w: duplicate share index %d", ErrInvalidPublicInput, share.Index)
		}
		if _, err := scalarFromBytes(share.Value, true); err != nil {
			return err
		}
		seen[share.Index] = struct{}{}
	}
	return nil
}

func recoverShareTerm(shares []ThresholdShare, targetIndex int, order *big.Int) (*big.Int, error) {
	targetShare := shares[targetIndex]
	targetX := big.NewInt(int64(targetShare.Index))
	targetY, err := scalarFromBytes(targetShare.Value, true)
	if err != nil {
		return nil, err
	}
	coefficient, err := lagrangeCoefficientAtZero(shares, targetIndex, targetX, order)
	if err != nil {
		return nil, err
	}
	term := new(big.Int).Mul(targetY, coefficient)
	term.Mod(term, order)
	return term, nil
}

func lagrangeCoefficientAtZero(shares []ThresholdShare, targetIndex int, targetX *big.Int, order *big.Int) (*big.Int, error) {
	numerator := big.NewInt(1)
	denominator := big.NewInt(1)
	for index, share := range shares {
		if index == targetIndex {
			continue
		}
		otherX := big.NewInt(int64(share.Index))
		numerator.Mul(numerator, new(big.Int).Neg(otherX))
		numerator.Mod(numerator, order)
		difference := new(big.Int).Sub(targetX, otherX)
		difference.Mod(difference, order)
		denominator.Mul(denominator, difference)
		denominator.Mod(denominator, order)
	}
	inverse := new(big.Int).ModInverse(denominator, order)
	if inverse == nil {
		return nil, fmt.Errorf("%w: denominator is not invertible", ErrInvalidProof)
	}
	coefficient := numerator.Mul(numerator, inverse)
	coefficient.Mod(coefficient, order)
	return coefficient, nil
}

func validateThresholdDecryptionShares(publicKeySet ThresholdPublicKeySet, ciphertext ElGamalCiphertext, shares []ThresholdDecryptionShare) ([]ThresholdDecryptionShare, error) {
	if err := publicKeySet.Validate(); err != nil {
		return nil, err
	}
	if len(shares) < publicKeySet.Threshold {
		return nil, fmt.Errorf("%w: decryption share count below threshold", ErrInvalidPublicInput)
	}
	if len(shares) > MaxThresholdShareCount {
		return nil, fmt.Errorf("%w: decryption share count exceeds %d", ErrInvalidPublicInput, MaxThresholdShareCount)
	}
	seen := make(map[uint8]struct{}, len(shares))
	for _, share := range shares {
		if _, exists := seen[share.Index]; exists {
			return nil, fmt.Errorf("%w: duplicate decryption share index %d", ErrInvalidPublicInput, share.Index)
		}
		if err := VerifyThresholdDecryptionShare(publicKeySet, ciphertext, share); err != nil {
			return nil, err
		}
		seen[share.Index] = struct{}{}
	}
	selectedShares := make([]ThresholdDecryptionShare, publicKeySet.Threshold)
	copy(selectedShares, shares[:publicKeySet.Threshold])
	return selectedShares, nil
}

func combineThresholdDecryptionPoints(shares []ThresholdDecryptionShare) (p256Point, error) {
	order := elliptic.P256().Params().N
	sharedPoint := infinityPoint()
	for shareIndex := range shares {
		decryptionPoint, err := pointFromBytes(shares[shareIndex].DecryptionPoint)
		if err != nil {
			return p256Point{}, err
		}
		coefficient, err := lagrangeCoefficientForDecryptionShares(shares, shareIndex, order)
		if err != nil {
			return p256Point{}, err
		}
		sharedPoint = addPoints(sharedPoint, scalarPoint(decryptionPoint, coefficient))
	}
	return sharedPoint, nil
}

func lagrangeCoefficientForDecryptionShares(shares []ThresholdDecryptionShare, targetIndex int, order *big.Int) (*big.Int, error) {
	numerator := big.NewInt(1)
	denominator := big.NewInt(1)
	targetX := big.NewInt(int64(shares[targetIndex].Index))
	for index, share := range shares {
		if index == targetIndex {
			continue
		}
		otherX := big.NewInt(int64(share.Index))
		numerator.Mul(numerator, new(big.Int).Neg(otherX))
		numerator.Mod(numerator, order)
		difference := new(big.Int).Sub(targetX, otherX)
		difference.Mod(difference, order)
		denominator.Mul(denominator, difference)
		denominator.Mod(denominator, order)
	}
	inverse := new(big.Int).ModInverse(denominator, order)
	if inverse == nil {
		return nil, fmt.Errorf("%w: denominator is not invertible", ErrInvalidProof)
	}
	coefficient := numerator.Mul(numerator, inverse)
	coefficient.Mod(coefficient, order)
	return coefficient, nil
}

func newThresholdDecryptionShareProof(index uint8, noncePoint p256Point, verificationPoint p256Point, decryptionPoint p256Point, shareValue *big.Int) (ThresholdDecryptionShareProof, error) {
	order := elliptic.P256().Params().N
	nonce, err := randomScalar(rand.Reader, order)
	if err != nil {
		return ThresholdDecryptionShareProof{}, err
	}
	baseNonce := scalarBasePoint(nonce)
	ciphertextNonce := scalarPoint(noncePoint, nonce)
	challenge := thresholdDecryptionChallenge(index, noncePoint, verificationPoint, decryptionPoint, baseNonce, ciphertextNonce)
	response := new(big.Int).Mul(challenge, shareValue)
	response.Add(response, nonce)
	response.Mod(response, order)
	return ThresholdDecryptionShareProof{
		BaseNonce:       pointBytes(baseNonce),
		CiphertextNonce: pointBytes(ciphertextNonce),
		Response:        padScalar(response),
	}, nil
}

func verifyThresholdDecryptionShareProof(index uint8, noncePoint p256Point, verificationPoint p256Point, decryptionPoint p256Point, proof ThresholdDecryptionShareProof) error {
	baseNonce, err := pointFromBytes(proof.BaseNonce)
	if err != nil {
		return err
	}
	ciphertextNonce, err := pointFromBytes(proof.CiphertextNonce)
	if err != nil {
		return err
	}
	response, err := scalarFromBytes(proof.Response, true)
	if err != nil {
		return err
	}
	challenge := thresholdDecryptionChallenge(index, noncePoint, verificationPoint, decryptionPoint, baseNonce, ciphertextNonce)
	leftBase := scalarBasePoint(response)
	rightBase := addPoints(baseNonce, scalarPoint(verificationPoint, challenge))
	if !samePoint(leftBase, rightBase) {
		return fmt.Errorf("%w: threshold base proof failed", ErrVerificationFailed)
	}
	leftCiphertext := scalarPoint(noncePoint, response)
	rightCiphertext := addPoints(ciphertextNonce, scalarPoint(decryptionPoint, challenge))
	if !samePoint(leftCiphertext, rightCiphertext) {
		return fmt.Errorf("%w: threshold ciphertext proof failed", ErrVerificationFailed)
	}
	return nil
}

func thresholdDecryptionChallenge(index uint8, noncePoint p256Point, verificationPoint p256Point, decryptionPoint p256Point, baseNonce p256Point, ciphertextNonce p256Point) *big.Int {
	buffer := bytes.Buffer{}
	buffer.WriteString(domainThresholdDecryptProof)
	buffer.WriteByte(index)
	writeLengthPrefixedBytes(&buffer, pointBytes(noncePoint))
	writeLengthPrefixedBytes(&buffer, pointBytes(verificationPoint))
	writeLengthPrefixedBytes(&buffer, pointBytes(decryptionPoint))
	writeLengthPrefixedBytes(&buffer, pointBytes(baseNonce))
	writeLengthPrefixedBytes(&buffer, pointBytes(ciphertextNonce))
	return hashToScalar(buffer.Bytes())
}
