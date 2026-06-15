package zk

import (
	"bytes"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"math/big"
)

const (
	ConfidentialProofVersion = uint16(1)
	ConfidentialRangeBits    = uint8(64)
	MaxConfidentialAmount    = ^uint64(0)

	confidentialPointSize        = 33
	confidentialScalarSize       = 32
	rangeProofBitRecordSize      = confidentialPointSize*3 + confidentialScalarSize*4
	amountCiphertextProofSize    = 2 + confidentialPointSize*3 + confidentialScalarSize*3
	domainConfidentialH          = "solana_golang.zk.confidential.h.v1"
	domainConfidentialBitProof   = "solana_golang.zk.confidential.bit.v1"
	domainConfidentialBalance    = "solana_golang.zk.confidential.balance.v1"
	domainConfidentialRange      = "solana_golang.zk.confidential.range.v1"
	domainConfidentialElGamal    = "solana_golang.zk.confidential.elgamal.v1"
	domainConfidentialCommitment = "solana_golang.zk.confidential.commitment.v1"
	domainConfidentialEquality   = "solana_golang.zk.confidential.equality.v1"
)

type AmountOpening struct {
	Amount   uint64
	Blinding []byte
}

type ElGamalKeyPair struct {
	PrivateScalar []byte
	PublicKey     []byte
}

type ElGamalCiphertext struct {
	NonceCommitment []byte
	CiphertextPoint []byte
}

type RangeProof struct {
	Version        uint16
	Bits           uint8
	Commitment     []byte
	BitCommitments [][]byte
	BitProofs      []BitProof
}

type BitProof struct {
	Nonce0     []byte
	Nonce1     []byte
	Challenge0 []byte
	Challenge1 []byte
	Response0  []byte
	Response1  []byte
}

type BalanceProof struct {
	Version         uint16
	NonceCommitment []byte
	Response        []byte
}

type AmountCiphertextProof struct {
	Version            uint16
	CommitmentNonce    []byte
	RandomnessNonce    []byte
	CiphertextNonce    []byte
	AmountResponse     []byte
	BlindingResponse   []byte
	RandomnessResponse []byte
}

// NewAmountOpening 创建金额承诺开口 + 随机 blinding 隐藏金额。
func NewAmountOpening(amount uint64) (AmountOpening, error) {
	if amount > MaxConfidentialAmount {
		return AmountOpening{}, fmt.Errorf("%w: amount %d exceeds %d", ErrInvalidPublicInput, amount, MaxConfidentialAmount)
	}
	blinding, err := randomScalar(rand.Reader, elliptic.P256().Params().N)
	if err != nil {
		return AmountOpening{}, err
	}
	return AmountOpening{Amount: amount, Blinding: padScalar(blinding)}, nil
}

// CommitAmount 创建 Pedersen 金额承诺 + C = amount*G + blinding*H。
func CommitAmount(opening AmountOpening) ([]byte, error) {
	if opening.Amount > MaxConfidentialAmount {
		return nil, fmt.Errorf("%w: amount %d exceeds %d", ErrInvalidPublicInput, opening.Amount, MaxConfidentialAmount)
	}
	blinding, err := scalarFromBytes(opening.Blinding, true)
	if err != nil {
		return nil, err
	}
	point, err := pedersenPoint(opening.Amount, blinding)
	if err != nil {
		return nil, err
	}
	return pointBytes(point), nil
}

// SubtractScalars 计算标量差值 + 供余额守恒证明生成 blinding delta。
func SubtractScalars(left []byte, right []byte) ([]byte, error) {
	leftValue, err := scalarFromBytes(left, true)
	if err != nil {
		return nil, err
	}
	rightValue, err := scalarFromBytes(right, true)
	if err != nil {
		return nil, err
	}
	order := elliptic.P256().Params().N
	delta := new(big.Int).Sub(leftValue, rightValue)
	delta.Mod(delta, order)
	return padScalar(delta), nil
}

// GenerateElGamalKeyPair 生成 P-256 ElGamal 密钥对 + 监管密钥和审计密钥共用该格式。
func GenerateElGamalKeyPair() (ElGamalKeyPair, error) {
	keyPair, err := GenerateSchnorrKeyPair()
	if err != nil {
		return ElGamalKeyPair{}, err
	}
	return ElGamalKeyPair{PrivateScalar: keyPair.PrivateScalar, PublicKey: keyPair.PublicKey}, nil
}

// EncryptAmount 加密金额 + 使用 EC-ElGamal 让监管门限恢复后可解密。
func EncryptAmount(publicKey []byte, amount uint64) (ElGamalCiphertext, []byte, error) {
	if amount > MaxConfidentialAmount {
		return ElGamalCiphertext{}, nil, fmt.Errorf("%w: amount %d exceeds %d", ErrInvalidPublicInput, amount, MaxConfidentialAmount)
	}
	publicPoint, err := pointFromBytes(publicKey)
	if err != nil {
		return ElGamalCiphertext{}, nil, err
	}
	randomness, err := randomScalar(rand.Reader, elliptic.P256().Params().N)
	if err != nil {
		return ElGamalCiphertext{}, nil, err
	}
	noncePoint := scalarBasePoint(randomness)
	sharedPoint := scalarPoint(publicPoint, randomness)
	amountPoint := scalarBasePoint(new(big.Int).SetUint64(amount))
	ciphertextPoint := addPoints(amountPoint, sharedPoint)
	return ElGamalCiphertext{
		NonceCommitment: pointBytes(noncePoint),
		CiphertextPoint: pointBytes(ciphertextPoint),
	}, padScalar(randomness), nil
}

// DecryptAmount 解密金额 + 在指定上限内解析离散对数。
func DecryptAmount(privateScalar []byte, ciphertext ElGamalCiphertext, maxAmount uint64) (uint64, error) {
	privateValue, err := scalarFromBytes(privateScalar, false)
	if err != nil {
		return 0, err
	}
	noncePoint, err := pointFromBytes(ciphertext.NonceCommitment)
	if err != nil {
		return 0, err
	}
	cipherPoint, err := pointFromBytes(ciphertext.CiphertextPoint)
	if err != nil {
		return 0, err
	}
	sharedPoint := scalarPoint(noncePoint, privateValue)
	amountPoint := subtractPoints(cipherPoint, sharedPoint)
	return decodeAmountPoint(amountPoint, maxAmount)
}

// ValidateElGamalCiphertext 校验 ElGamal 密文结构 + 拒绝无效曲线点进入业务状态。
func ValidateElGamalCiphertext(ciphertext ElGamalCiphertext) error {
	if _, err := pointFromBytes(ciphertext.NonceCommitment); err != nil {
		return err
	}
	if _, err := pointFromBytes(ciphertext.CiphertextPoint); err != nil {
		return err
	}
	return nil
}

// NewAmountCiphertextProof 创建金额密文一致性证明 + 证明审计密文和金额承诺包含同一个金额。
func NewAmountCiphertextProof(publicKey []byte, commitment []byte, ciphertext ElGamalCiphertext, opening AmountOpening, encryptionRandomness []byte) (AmountCiphertextProof, error) {
	if opening.Amount > MaxConfidentialAmount {
		return AmountCiphertextProof{}, fmt.Errorf("%w: amount %d exceeds %d", ErrInvalidPublicInput, opening.Amount, MaxConfidentialAmount)
	}
	publicPoint, commitmentPoint, noncePoint, cipherPoint, err := amountCiphertextPublicPoints(publicKey, commitment, ciphertext)
	if err != nil {
		return AmountCiphertextProof{}, err
	}
	amountValue := new(big.Int).SetUint64(opening.Amount)
	blindingValue, err := scalarFromBytes(opening.Blinding, true)
	if err != nil {
		return AmountCiphertextProof{}, err
	}
	randomnessValue, err := scalarFromBytes(encryptionRandomness, false)
	if err != nil {
		return AmountCiphertextProof{}, err
	}
	expectedCommitment, err := pedersenPoint(opening.Amount, blindingValue)
	if err != nil {
		return AmountCiphertextProof{}, err
	}
	if !samePoint(expectedCommitment, commitmentPoint) {
		return AmountCiphertextProof{}, fmt.Errorf("%w: opening does not match commitment", ErrVerificationFailed)
	}
	expectedNonce := scalarBasePoint(randomnessValue)
	expectedCipher := addPoints(scalarBasePoint(amountValue), scalarPoint(publicPoint, randomnessValue))
	if !samePoint(expectedNonce, noncePoint) || !samePoint(expectedCipher, cipherPoint) {
		return AmountCiphertextProof{}, fmt.Errorf("%w: opening does not match ciphertext", ErrVerificationFailed)
	}
	proof, err := newAmountCiphertextProof(publicPoint, commitmentPoint, noncePoint, cipherPoint, amountValue, blindingValue, randomnessValue)
	if err != nil {
		return AmountCiphertextProof{}, err
	}
	return proof, VerifyAmountCiphertextProof(publicKey, commitment, ciphertext, proof)
}

// VerifyAmountCiphertextProof 校验金额密文一致性证明 + 审计密文必须和 Pedersen 承诺同金额。
func VerifyAmountCiphertextProof(publicKey []byte, commitment []byte, ciphertext ElGamalCiphertext, proof AmountCiphertextProof) error {
	if proof.Version != ConfidentialProofVersion {
		return fmt.Errorf("%w: amount ciphertext proof version %d", ErrUnsupportedVersion, proof.Version)
	}
	publicPoint, commitmentPoint, noncePoint, cipherPoint, err := amountCiphertextPublicPoints(publicKey, commitment, ciphertext)
	if err != nil {
		return err
	}
	commitmentNonce, err := pointFromBytes(proof.CommitmentNonce)
	if err != nil {
		return err
	}
	randomnessNonce, err := pointFromBytes(proof.RandomnessNonce)
	if err != nil {
		return err
	}
	ciphertextNonce, err := pointFromBytes(proof.CiphertextNonce)
	if err != nil {
		return err
	}
	amountResponse, blindingResponse, randomnessResponse, err := amountCiphertextResponses(proof)
	if err != nil {
		return err
	}
	challenge := amountCiphertextChallenge(publicKey, commitment, ciphertext, proof)
	if !verifyCommitmentResponse(commitmentPoint, commitmentNonce, amountResponse, blindingResponse, challenge) {
		return fmt.Errorf("%w: amount commitment equality failed", ErrVerificationFailed)
	}
	if !samePoint(scalarBasePoint(randomnessResponse), addPoints(randomnessNonce, scalarPoint(noncePoint, challenge))) {
		return fmt.Errorf("%w: amount randomness equality failed", ErrVerificationFailed)
	}
	leftCipher := addPoints(scalarBasePoint(amountResponse), scalarPoint(publicPoint, randomnessResponse))
	rightCipher := addPoints(ciphertextNonce, scalarPoint(cipherPoint, challenge))
	if !samePoint(leftCipher, rightCipher) {
		return fmt.Errorf("%w: amount ciphertext equality failed", ErrVerificationFailed)
	}
	return nil
}

// NewRangeProof 创建范围证明 + 证明承诺金额在 [0, 2^bits) 且不泄露金额。
func NewRangeProof(opening AmountOpening, bits uint8) (RangeProof, error) {
	if bits == 0 || bits > ConfidentialRangeBits {
		return RangeProof{}, fmt.Errorf("%w: invalid range bits %d", ErrInvalidProof, bits)
	}
	if !amountFitsRangeBits(opening.Amount, bits) {
		return RangeProof{}, fmt.Errorf("%w: amount %d outside %d-bit range", ErrInvalidProof, opening.Amount, bits)
	}
	totalBlinding, err := scalarFromBytes(opening.Blinding, true)
	if err != nil {
		return RangeProof{}, err
	}
	commitment, err := CommitAmount(opening)
	if err != nil {
		return RangeProof{}, err
	}
	bitBlindings, err := splitBitBlindings(totalBlinding, bits)
	if err != nil {
		return RangeProof{}, err
	}
	bitCommitments := make([][]byte, int(bits))
	bitProofs := make([]BitProof, int(bits))
	for bitIndex := 0; bitIndex < int(bits); bitIndex++ {
		bitValue := (opening.Amount >> bitIndex) & 1
		bitPoint, err := pedersenPoint(bitValue, bitBlindings[bitIndex])
		if err != nil {
			return RangeProof{}, err
		}
		bitCommitments[bitIndex] = pointBytes(bitPoint)
		transcript := rangeBitTranscript(commitment, bits, bitIndex)
		bitProofs[bitIndex], err = newBitProof(uint8(bitValue), bitBlindings[bitIndex], bitPoint, transcript)
		if err != nil {
			return RangeProof{}, err
		}
	}
	proof := RangeProof{Version: ConfidentialProofVersion, Bits: bits, Commitment: commitment, BitCommitments: bitCommitments, BitProofs: bitProofs}
	return proof, proof.Verify()
}

// Verify 校验范围证明 + 同时校验位承诺加权和等于总承诺。
func (proof RangeProof) Verify() error {
	if proof.Version != ConfidentialProofVersion {
		return fmt.Errorf("%w: range version %d", ErrUnsupportedVersion, proof.Version)
	}
	if proof.Bits == 0 || proof.Bits > ConfidentialRangeBits {
		return fmt.Errorf("%w: invalid range bits %d", ErrInvalidProof, proof.Bits)
	}
	if len(proof.BitCommitments) != int(proof.Bits) || len(proof.BitProofs) != int(proof.Bits) {
		return fmt.Errorf("%w: range proof bit count mismatch", ErrInvalidProof)
	}
	commitmentPoint, err := pointFromBytes(proof.Commitment)
	if err != nil {
		return err
	}
	sumPoint := infinityPoint()
	for bitIndex := 0; bitIndex < int(proof.Bits); bitIndex++ {
		bitPoint, err := pointFromBytes(proof.BitCommitments[bitIndex])
		if err != nil {
			return err
		}
		transcript := rangeBitTranscript(proof.Commitment, proof.Bits, bitIndex)
		if err := verifyBitProof(bitPoint, proof.BitProofs[bitIndex], transcript); err != nil {
			return err
		}
		weightedPoint := scalarPoint(bitPoint, new(big.Int).Lsh(big.NewInt(1), uint(bitIndex)))
		sumPoint = addPoints(sumPoint, weightedPoint)
	}
	if !samePoint(sumPoint, commitmentPoint) {
		return fmt.Errorf("%w: range bit commitments do not sum to amount commitment", ErrVerificationFailed)
	}
	return nil
}

// NewBalanceProof 创建守恒证明 + 证明输入承诺和输出/公开金额之间只差 blinding。
func NewBalanceProof(inputCommitments [][]byte, outputCommitments [][]byte, publicAmount uint64, blindingDelta []byte) (BalanceProof, error) {
	targetPoint, transcript, err := balanceTarget(inputCommitments, outputCommitments, publicAmount)
	if err != nil {
		return BalanceProof{}, err
	}
	witness, err := scalarFromBytes(blindingDelta, true)
	if err != nil {
		return BalanceProof{}, err
	}
	proof, err := newDLogProof(hPoint(), targetPoint, witness, transcript)
	if err != nil {
		return BalanceProof{}, err
	}
	return proof, VerifyBalanceProof(inputCommitments, outputCommitments, publicAmount, proof)
}

// VerifyBalanceProof 校验守恒证明 + 不需要知道隐藏金额。
func VerifyBalanceProof(inputCommitments [][]byte, outputCommitments [][]byte, publicAmount uint64, proof BalanceProof) error {
	if proof.Version != ConfidentialProofVersion {
		return fmt.Errorf("%w: balance version %d", ErrUnsupportedVersion, proof.Version)
	}
	targetPoint, transcript, err := balanceTarget(inputCommitments, outputCommitments, publicAmount)
	if err != nil {
		return err
	}
	return verifyDLogProof(hPoint(), targetPoint, proof, transcript)
}

// NewCommitmentAmountProof 创建公开金额绑定证明 + 证明承诺金额等于透明侧扣款金额。
func NewCommitmentAmountProof(commitment []byte, publicAmount uint64, blinding []byte) (BalanceProof, error) {
	targetPoint, transcript, err := commitmentAmountTarget(commitment, publicAmount)
	if err != nil {
		return BalanceProof{}, err
	}
	witness, err := scalarFromBytes(blinding, true)
	if err != nil {
		return BalanceProof{}, err
	}
	proof, err := newDLogProof(hPoint(), targetPoint, witness, transcript)
	if err != nil {
		return BalanceProof{}, err
	}
	return proof, VerifyCommitmentAmountProof(commitment, publicAmount, proof)
}

// VerifyCommitmentAmountProof 校验公开金额绑定证明 + 防止透明入金和隐私承诺金额不一致。
func VerifyCommitmentAmountProof(commitment []byte, publicAmount uint64, proof BalanceProof) error {
	if proof.Version != ConfidentialProofVersion {
		return fmt.Errorf("%w: commitment amount version %d", ErrUnsupportedVersion, proof.Version)
	}
	targetPoint, transcript, err := commitmentAmountTarget(commitment, publicAmount)
	if err != nil {
		return err
	}
	return verifyDLogProof(hPoint(), targetPoint, proof, transcript)
}

func newAmountCiphertextProof(publicPoint p256Point, commitmentPoint p256Point, noncePoint p256Point, cipherPoint p256Point, amountValue *big.Int, blindingValue *big.Int, randomnessValue *big.Int) (AmountCiphertextProof, error) {
	order := elliptic.P256().Params().N
	amountNonce, err := randomScalar(rand.Reader, order)
	if err != nil {
		return AmountCiphertextProof{}, err
	}
	blindingNonce, err := randomScalar(rand.Reader, order)
	if err != nil {
		return AmountCiphertextProof{}, err
	}
	randomnessNonce, err := randomScalar(rand.Reader, order)
	if err != nil {
		return AmountCiphertextProof{}, err
	}

	commitmentNonce := addPoints(scalarBasePoint(amountNonce), scalarHPoint(blindingNonce))
	randomnessNoncePoint := scalarBasePoint(randomnessNonce)
	ciphertextNonce := addPoints(scalarBasePoint(amountNonce), scalarPoint(publicPoint, randomnessNonce))
	proof := AmountCiphertextProof{
		Version:         ConfidentialProofVersion,
		CommitmentNonce: pointBytes(commitmentNonce),
		RandomnessNonce: pointBytes(randomnessNoncePoint),
		CiphertextNonce: pointBytes(ciphertextNonce),
	}
	challenge := amountCiphertextChallengeFromPoints(publicPoint, commitmentPoint, noncePoint, cipherPoint, commitmentNonce, randomnessNoncePoint, ciphertextNonce)
	proof.AmountResponse = scalarChallengeResponse(amountNonce, challenge, amountValue, order)
	proof.BlindingResponse = scalarChallengeResponse(blindingNonce, challenge, blindingValue, order)
	proof.RandomnessResponse = scalarChallengeResponse(randomnessNonce, challenge, randomnessValue, order)
	return proof, nil
}

func amountCiphertextPublicPoints(publicKey []byte, commitment []byte, ciphertext ElGamalCiphertext) (p256Point, p256Point, p256Point, p256Point, error) {
	publicPoint, err := pointFromBytes(publicKey)
	if err != nil {
		return p256Point{}, p256Point{}, p256Point{}, p256Point{}, err
	}
	commitmentPoint, err := pointFromBytes(commitment)
	if err != nil {
		return p256Point{}, p256Point{}, p256Point{}, p256Point{}, err
	}
	noncePoint, err := pointFromBytes(ciphertext.NonceCommitment)
	if err != nil {
		return p256Point{}, p256Point{}, p256Point{}, p256Point{}, err
	}
	cipherPoint, err := pointFromBytes(ciphertext.CiphertextPoint)
	if err != nil {
		return p256Point{}, p256Point{}, p256Point{}, p256Point{}, err
	}
	return publicPoint, commitmentPoint, noncePoint, cipherPoint, nil
}

func amountCiphertextResponses(proof AmountCiphertextProof) (*big.Int, *big.Int, *big.Int, error) {
	amountResponse, err := scalarFromBytes(proof.AmountResponse, true)
	if err != nil {
		return nil, nil, nil, err
	}
	blindingResponse, err := scalarFromBytes(proof.BlindingResponse, true)
	if err != nil {
		return nil, nil, nil, err
	}
	randomnessResponse, err := scalarFromBytes(proof.RandomnessResponse, true)
	if err != nil {
		return nil, nil, nil, err
	}
	return amountResponse, blindingResponse, randomnessResponse, nil
}

func amountCiphertextChallenge(publicKey []byte, commitment []byte, ciphertext ElGamalCiphertext, proof AmountCiphertextProof) *big.Int {
	publicPoint, _ := pointFromBytes(publicKey)
	commitmentPoint, _ := pointFromBytes(commitment)
	noncePoint, _ := pointFromBytes(ciphertext.NonceCommitment)
	cipherPoint, _ := pointFromBytes(ciphertext.CiphertextPoint)
	commitmentNonce, _ := pointFromBytes(proof.CommitmentNonce)
	randomnessNonce, _ := pointFromBytes(proof.RandomnessNonce)
	ciphertextNonce, _ := pointFromBytes(proof.CiphertextNonce)
	return amountCiphertextChallengeFromPoints(publicPoint, commitmentPoint, noncePoint, cipherPoint, commitmentNonce, randomnessNonce, ciphertextNonce)
}

func amountCiphertextChallengeFromPoints(publicPoint p256Point, commitmentPoint p256Point, noncePoint p256Point, cipherPoint p256Point, commitmentNonce p256Point, randomnessNonce p256Point, ciphertextNonce p256Point) *big.Int {
	buffer := bytes.Buffer{}
	buffer.WriteString(domainConfidentialEquality)
	writeLengthPrefixedBytes(&buffer, pointBytes(publicPoint))
	writeLengthPrefixedBytes(&buffer, pointBytes(commitmentPoint))
	writeLengthPrefixedBytes(&buffer, pointBytes(noncePoint))
	writeLengthPrefixedBytes(&buffer, pointBytes(cipherPoint))
	writeLengthPrefixedBytes(&buffer, pointBytes(commitmentNonce))
	writeLengthPrefixedBytes(&buffer, pointBytes(randomnessNonce))
	writeLengthPrefixedBytes(&buffer, pointBytes(ciphertextNonce))
	return hashToScalar(buffer.Bytes())
}

func scalarChallengeResponse(nonce *big.Int, challenge *big.Int, witness *big.Int, order *big.Int) []byte {
	response := new(big.Int).Mul(challenge, witness)
	response.Add(response, nonce)
	response.Mod(response, order)
	return padScalar(response)
}

func verifyCommitmentResponse(commitmentPoint p256Point, commitmentNonce p256Point, amountResponse *big.Int, blindingResponse *big.Int, challenge *big.Int) bool {
	left := addPoints(scalarBasePoint(amountResponse), scalarHPoint(blindingResponse))
	right := addPoints(commitmentNonce, scalarPoint(commitmentPoint, challenge))
	return samePoint(left, right)
}

func newBitProof(bit uint8, blinding *big.Int, commitment p256Point, transcript []byte) (BitProof, error) {
	if bit > 1 {
		return BitProof{}, fmt.Errorf("%w: invalid bit %d", ErrInvalidProof, bit)
	}
	order := elliptic.P256().Params().N
	y0 := commitment
	y1 := subtractPoints(commitment, scalarBasePoint(big.NewInt(1)))
	trueIndex := int(bit)
	falseIndex := 1 - trueIndex
	nonceTrue, err := randomScalar(rand.Reader, order)
	if err != nil {
		return BitProof{}, err
	}
	challengeFalse, err := randomScalar(rand.Reader, order)
	if err != nil {
		return BitProof{}, err
	}
	responseFalse, err := randomScalar(rand.Reader, order)
	if err != nil {
		return BitProof{}, err
	}
	noncePoints := make([]p256Point, 2)
	challenges := make([]*big.Int, 2)
	responses := make([]*big.Int, 2)
	noncePoints[trueIndex] = scalarHPoint(nonceTrue)
	falseTarget := []p256Point{y0, y1}[falseIndex]
	noncePoints[falseIndex] = subtractPoints(scalarHPoint(responseFalse), scalarPoint(falseTarget, challengeFalse))
	challenge := bitChallenge(y0, y1, noncePoints[0], noncePoints[1], transcript)
	challenges[falseIndex] = challengeFalse
	challenges[trueIndex] = new(big.Int).Sub(challenge, challengeFalse)
	challenges[trueIndex].Mod(challenges[trueIndex], order)
	responses[falseIndex] = responseFalse
	responses[trueIndex] = new(big.Int).Mul(challenges[trueIndex], blinding)
	responses[trueIndex].Add(responses[trueIndex], nonceTrue)
	responses[trueIndex].Mod(responses[trueIndex], order)
	return BitProof{
		Nonce0:     pointBytes(noncePoints[0]),
		Nonce1:     pointBytes(noncePoints[1]),
		Challenge0: padScalar(challenges[0]),
		Challenge1: padScalar(challenges[1]),
		Response0:  padScalar(responses[0]),
		Response1:  padScalar(responses[1]),
	}, nil
}

func verifyBitProof(commitment p256Point, proof BitProof, transcript []byte) error {
	y0 := commitment
	y1 := subtractPoints(commitment, scalarBasePoint(big.NewInt(1)))
	nonce0, err := pointFromBytes(proof.Nonce0)
	if err != nil {
		return err
	}
	nonce1, err := pointFromBytes(proof.Nonce1)
	if err != nil {
		return err
	}
	challenge0, err := scalarFromBytes(proof.Challenge0, true)
	if err != nil {
		return err
	}
	challenge1, err := scalarFromBytes(proof.Challenge1, true)
	if err != nil {
		return err
	}
	response0, err := scalarFromBytes(proof.Response0, true)
	if err != nil {
		return err
	}
	response1, err := scalarFromBytes(proof.Response1, true)
	if err != nil {
		return err
	}
	order := elliptic.P256().Params().N
	challengeSum := new(big.Int).Add(challenge0, challenge1)
	challengeSum.Mod(challengeSum, order)
	expectedChallenge := bitChallenge(y0, y1, nonce0, nonce1, transcript)
	if challengeSum.Cmp(expectedChallenge) != 0 {
		return fmt.Errorf("%w: bit challenge mismatch", ErrVerificationFailed)
	}
	if !samePoint(scalarHPoint(response0), addPoints(nonce0, scalarPoint(y0, challenge0))) {
		return fmt.Errorf("%w: bit branch zero failed", ErrVerificationFailed)
	}
	if !samePoint(scalarHPoint(response1), addPoints(nonce1, scalarPoint(y1, challenge1))) {
		return fmt.Errorf("%w: bit branch one failed", ErrVerificationFailed)
	}
	return nil
}

func splitBitBlindings(totalBlinding *big.Int, bits uint8) ([]*big.Int, error) {
	order := elliptic.P256().Params().N
	blindings := make([]*big.Int, int(bits))
	accumulated := big.NewInt(0)
	for bitIndex := 0; bitIndex < int(bits)-1; bitIndex++ {
		value, err := randomScalar(rand.Reader, order)
		if err != nil {
			return nil, err
		}
		blindings[bitIndex] = value
		weighted := new(big.Int).Mul(new(big.Int).Lsh(big.NewInt(1), uint(bitIndex)), value)
		accumulated.Add(accumulated, weighted)
		accumulated.Mod(accumulated, order)
	}
	lastWeight := new(big.Int).Lsh(big.NewInt(1), uint(bits-1))
	lastWeight.Mod(lastWeight, order)
	lastWeightInverse := new(big.Int).ModInverse(lastWeight, order)
	if lastWeightInverse == nil {
		return nil, fmt.Errorf("%w: cannot invert bit weight", ErrInvalidProof)
	}
	remaining := new(big.Int).Sub(totalBlinding, accumulated)
	remaining.Mod(remaining, order)
	blindings[int(bits)-1] = remaining.Mul(remaining, lastWeightInverse)
	blindings[int(bits)-1].Mod(blindings[int(bits)-1], order)
	return blindings, nil
}

func newDLogProof(base p256Point, target p256Point, witness *big.Int, transcript []byte) (BalanceProof, error) {
	order := elliptic.P256().Params().N
	nonce, err := randomScalar(rand.Reader, order)
	if err != nil {
		return BalanceProof{}, err
	}
	noncePoint := scalarPoint(base, nonce)
	challenge := dlogChallenge(base, target, noncePoint, transcript)
	response := new(big.Int).Mul(challenge, witness)
	response.Add(response, nonce)
	response.Mod(response, order)
	return BalanceProof{Version: ConfidentialProofVersion, NonceCommitment: pointBytes(noncePoint), Response: padScalar(response)}, nil
}

func verifyDLogProof(base p256Point, target p256Point, proof BalanceProof, transcript []byte) error {
	noncePoint, err := pointFromBytes(proof.NonceCommitment)
	if err != nil {
		return err
	}
	response, err := scalarFromBytes(proof.Response, true)
	if err != nil {
		return err
	}
	challenge := dlogChallenge(base, target, noncePoint, transcript)
	left := scalarPoint(base, response)
	right := addPoints(noncePoint, scalarPoint(target, challenge))
	if !samePoint(left, right) {
		return fmt.Errorf("%w: balance proof failed", ErrVerificationFailed)
	}
	return nil
}

func balanceTarget(inputCommitments [][]byte, outputCommitments [][]byte, publicAmount uint64) (p256Point, []byte, error) {
	if publicAmount > MaxConfidentialAmount {
		return p256Point{}, nil, fmt.Errorf("%w: public amount %d exceeds %d", ErrInvalidPublicInput, publicAmount, MaxConfidentialAmount)
	}
	target := infinityPoint()
	transcript := bytes.Buffer{}
	transcript.WriteString(domainConfidentialBalance)
	writeLengthPrefixedBytes(&transcript, uint64ToLE(publicAmount))
	for _, commitment := range inputCommitments {
		point, err := pointFromBytes(commitment)
		if err != nil {
			return p256Point{}, nil, err
		}
		target = addPoints(target, point)
		writeLengthPrefixedBytes(&transcript, commitment)
	}
	transcript.WriteByte(0xff)
	for _, commitment := range outputCommitments {
		point, err := pointFromBytes(commitment)
		if err != nil {
			return p256Point{}, nil, err
		}
		target = subtractPoints(target, point)
		writeLengthPrefixedBytes(&transcript, commitment)
	}
	if publicAmount > 0 {
		target = subtractPoints(target, scalarBasePoint(new(big.Int).SetUint64(publicAmount)))
	}
	return target, transcript.Bytes(), nil
}

func commitmentAmountTarget(commitment []byte, publicAmount uint64) (p256Point, []byte, error) {
	if publicAmount > MaxConfidentialAmount {
		return p256Point{}, nil, fmt.Errorf("%w: public amount %d exceeds %d", ErrInvalidPublicInput, publicAmount, MaxConfidentialAmount)
	}
	commitmentPoint, err := pointFromBytes(commitment)
	if err != nil {
		return p256Point{}, nil, err
	}
	target := subtractPoints(commitmentPoint, scalarBasePoint(new(big.Int).SetUint64(publicAmount)))
	transcript := bytes.Buffer{}
	transcript.WriteString(domainConfidentialCommitment)
	writeLengthPrefixedBytes(&transcript, uint64ToLE(publicAmount))
	writeLengthPrefixedBytes(&transcript, commitment)
	return target, transcript.Bytes(), nil
}

func rangeBitTranscript(commitment []byte, bits uint8, bitIndex int) []byte {
	buffer := bytes.Buffer{}
	buffer.WriteString(domainConfidentialRange)
	writeLengthPrefixedBytes(&buffer, commitment)
	buffer.WriteByte(bits)
	writeUint16(&buffer, uint16(bitIndex))
	return buffer.Bytes()
}

func bitChallenge(y0 p256Point, y1 p256Point, nonce0 p256Point, nonce1 p256Point, transcript []byte) *big.Int {
	buffer := bytes.Buffer{}
	buffer.WriteString(domainConfidentialBitProof)
	writeLengthPrefixedBytes(&buffer, pointBytes(y0))
	writeLengthPrefixedBytes(&buffer, pointBytes(y1))
	writeLengthPrefixedBytes(&buffer, pointBytes(nonce0))
	writeLengthPrefixedBytes(&buffer, pointBytes(nonce1))
	writeLengthPrefixedBytes(&buffer, transcript)
	return hashToScalar(buffer.Bytes())
}

func dlogChallenge(base p256Point, target p256Point, nonce p256Point, transcript []byte) *big.Int {
	buffer := bytes.Buffer{}
	buffer.WriteString(domainConfidentialBalance)
	writeLengthPrefixedBytes(&buffer, pointBytes(base))
	writeLengthPrefixedBytes(&buffer, pointBytes(target))
	writeLengthPrefixedBytes(&buffer, pointBytes(nonce))
	writeLengthPrefixedBytes(&buffer, transcript)
	return hashToScalar(buffer.Bytes())
}

func pedersenPoint(amount uint64, blinding *big.Int) (p256Point, error) {
	if amount > MaxConfidentialAmount {
		return p256Point{}, fmt.Errorf("%w: amount %d exceeds %d", ErrInvalidPublicInput, amount, MaxConfidentialAmount)
	}
	amountPoint := scalarBasePoint(new(big.Int).SetUint64(amount))
	blindPoint := scalarHPoint(blinding)
	return addPoints(amountPoint, blindPoint), nil
}

type p256Point struct {
	x        *big.Int
	y        *big.Int
	infinity bool
}

func infinityPoint() p256Point {
	return p256Point{infinity: true}
}

func scalarBasePoint(scalar *big.Int) p256Point {
	curve := elliptic.P256()
	x, y := curve.ScalarBaseMult(padScalar(new(big.Int).Mod(scalar, curve.Params().N)))
	return p256Point{x: x, y: y}
}

func hPoint() p256Point {
	return scalarBasePoint(hashToScalar([]byte(domainConfidentialH)))
}

func scalarHPoint(scalar *big.Int) p256Point {
	return scalarPoint(hPoint(), scalar)
}

func scalarPoint(point p256Point, scalar *big.Int) p256Point {
	if point.infinity {
		return infinityPoint()
	}
	curve := elliptic.P256()
	x, y := curve.ScalarMult(point.x, point.y, padScalar(new(big.Int).Mod(scalar, curve.Params().N)))
	return p256Point{x: x, y: y}
}

func addPoints(left p256Point, right p256Point) p256Point {
	if left.infinity {
		return right
	}
	if right.infinity {
		return left
	}
	x, y := elliptic.P256().Add(left.x, left.y, right.x, right.y)
	if x == nil {
		return infinityPoint()
	}
	return p256Point{x: x, y: y}
}

func subtractPoints(left p256Point, right p256Point) p256Point {
	return addPoints(left, negatePoint(right))
}

func negatePoint(point p256Point) p256Point {
	if point.infinity {
		return point
	}
	p := elliptic.P256().Params().P
	y := new(big.Int).Neg(point.y)
	y.Mod(y, p)
	return p256Point{x: new(big.Int).Set(point.x), y: y}
}

func samePoint(left p256Point, right p256Point) bool {
	if left.infinity || right.infinity {
		return left.infinity == right.infinity
	}
	return left.x.Cmp(right.x) == 0 && left.y.Cmp(right.y) == 0
}

func pointBytes(point p256Point) []byte {
	if point.infinity {
		return nil
	}
	return elliptic.MarshalCompressed(elliptic.P256(), point.x, point.y)
}

func pointFromBytes(data []byte) (p256Point, error) {
	if len(data) != confidentialPointSize {
		return p256Point{}, fmt.Errorf("%w: point requires %d bytes, got %d", ErrInvalidProof, confidentialPointSize, len(data))
	}
	x, y := elliptic.UnmarshalCompressed(elliptic.P256(), data)
	if x == nil {
		return p256Point{}, fmt.Errorf("%w: invalid compressed point", ErrInvalidProof)
	}
	return p256Point{x: x, y: y}, nil
}

func scalarFromBytes(data []byte, allowZero bool) (*big.Int, error) {
	if len(data) != confidentialScalarSize {
		return nil, fmt.Errorf("%w: scalar requires %d bytes, got %d", ErrInvalidProof, confidentialScalarSize, len(data))
	}
	value := new(big.Int).SetBytes(data)
	order := elliptic.P256().Params().N
	if value.Cmp(order) >= 0 || (!allowZero && value.Sign() == 0) {
		return nil, fmt.Errorf("%w: scalar out of range", ErrInvalidProof)
	}
	return value, nil
}

func hashToScalar(data []byte) *big.Int {
	sum := sha256.Sum256(data)
	value := new(big.Int).SetBytes(sum[:])
	value.Mod(value, elliptic.P256().Params().N)
	if value.Sign() == 0 {
		value.SetInt64(1)
	}
	return value
}

func decodeAmountPoint(target p256Point, maxAmount uint64) (uint64, error) {
	current := infinityPoint()
	base := scalarBasePoint(big.NewInt(1))
	for amount := uint64(0); amount <= maxAmount; amount++ {
		if samePoint(current, target) {
			return amount, nil
		}
		current = addPoints(current, base)
	}
	return 0, fmt.Errorf("%w: amount point not found up to %d", ErrVerificationFailed, maxAmount)
}

func uint64ToLE(value uint64) []byte {
	var encoded [8]byte
	binary.LittleEndian.PutUint64(encoded[:], value)
	return encoded[:]
}

func amountFitsRangeBits(amount uint64, bits uint8) bool {
	if bits >= 64 {
		return true
	}
	return amount < (uint64(1) << bits)
}

// MarshalBinary 序列化范围证明 + 便于 VM syscall 或交易携带。
func (proof RangeProof) MarshalBinary() ([]byte, error) {
	if err := proof.Verify(); err != nil {
		return nil, err
	}
	buffer := bytes.Buffer{}
	writeUint16(&buffer, proof.Version)
	buffer.WriteByte(proof.Bits)
	buffer.Write(proof.Commitment)
	for index := range proof.BitCommitments {
		buffer.Write(proof.BitCommitments[index])
		bitProof := proof.BitProofs[index]
		buffer.Write(bitProof.Nonce0)
		buffer.Write(bitProof.Nonce1)
		buffer.Write(bitProof.Challenge0)
		buffer.Write(bitProof.Challenge1)
		buffer.Write(bitProof.Response0)
		buffer.Write(bitProof.Response1)
	}
	return buffer.Bytes(), nil
}

// UnmarshalRangeProofBinary 反序列化范围证明 + 拒绝畸形长度。
func UnmarshalRangeProofBinary(data []byte) (RangeProof, error) {
	reader := bytes.NewReader(data)
	version, err := readUint16(reader)
	if err != nil {
		return RangeProof{}, err
	}
	bitsByte, err := reader.ReadByte()
	if err != nil {
		return RangeProof{}, err
	}
	commitment, err := readFixedBytes(reader, confidentialPointSize)
	if err != nil {
		return RangeProof{}, err
	}
	bits := int(bitsByte)
	if bits == 0 || bits > int(ConfidentialRangeBits) {
		return RangeProof{}, fmt.Errorf("%w: invalid bits %d", ErrInvalidProof, bits)
	}
	wantRemaining := bits * rangeProofBitRecordSize
	if reader.Len() != wantRemaining {
		return RangeProof{}, fmt.Errorf("%w: range proof length mismatch", ErrInvalidProof)
	}
	proof := RangeProof{Version: version, Bits: bitsByte, Commitment: commitment, BitCommitments: make([][]byte, bits), BitProofs: make([]BitProof, bits)}
	for index := 0; index < bits; index++ {
		if proof.BitCommitments[index], err = readFixedBytes(reader, confidentialPointSize); err != nil {
			return RangeProof{}, err
		}
		if proof.BitProofs[index].Nonce0, err = readFixedBytes(reader, confidentialPointSize); err != nil {
			return RangeProof{}, err
		}
		if proof.BitProofs[index].Nonce1, err = readFixedBytes(reader, confidentialPointSize); err != nil {
			return RangeProof{}, err
		}
		if proof.BitProofs[index].Challenge0, err = readFixedBytes(reader, confidentialScalarSize); err != nil {
			return RangeProof{}, err
		}
		if proof.BitProofs[index].Challenge1, err = readFixedBytes(reader, confidentialScalarSize); err != nil {
			return RangeProof{}, err
		}
		if proof.BitProofs[index].Response0, err = readFixedBytes(reader, confidentialScalarSize); err != nil {
			return RangeProof{}, err
		}
		if proof.BitProofs[index].Response1, err = readFixedBytes(reader, confidentialScalarSize); err != nil {
			return RangeProof{}, err
		}
	}
	return proof, proof.Verify()
}

// MarshalBinary 序列化守恒证明 + 使用固定长度字段。
func (proof BalanceProof) MarshalBinary() ([]byte, error) {
	if proof.Version != ConfidentialProofVersion {
		return nil, fmt.Errorf("%w: balance version %d", ErrUnsupportedVersion, proof.Version)
	}
	if len(proof.NonceCommitment) != confidentialPointSize || len(proof.Response) != confidentialScalarSize {
		return nil, fmt.Errorf("%w: invalid balance proof length", ErrInvalidProof)
	}
	buffer := bytes.Buffer{}
	writeUint16(&buffer, proof.Version)
	buffer.Write(proof.NonceCommitment)
	buffer.Write(proof.Response)
	return buffer.Bytes(), nil
}

// UnmarshalBalanceProofBinary 反序列化守恒证明 + 拒绝尾部污染。
func UnmarshalBalanceProofBinary(data []byte) (BalanceProof, error) {
	reader := bytes.NewReader(data)
	version, err := readUint16(reader)
	if err != nil {
		return BalanceProof{}, err
	}
	nonce, err := readFixedBytes(reader, confidentialPointSize)
	if err != nil {
		return BalanceProof{}, err
	}
	response, err := readFixedBytes(reader, confidentialScalarSize)
	if err != nil {
		return BalanceProof{}, err
	}
	if reader.Len() != 0 {
		return BalanceProof{}, fmt.Errorf("%w: trailing balance proof bytes", ErrInvalidProof)
	}
	return BalanceProof{Version: version, NonceCommitment: nonce, Response: response}, nil
}

// MarshalBinary 序列化金额密文一致性证明 + 使用固定字段便于链上携带。
func (proof AmountCiphertextProof) MarshalBinary() ([]byte, error) {
	if err := proof.Validate(); err != nil {
		return nil, err
	}
	buffer := bytes.Buffer{}
	writeUint16(&buffer, proof.Version)
	buffer.Write(proof.CommitmentNonce)
	buffer.Write(proof.RandomnessNonce)
	buffer.Write(proof.CiphertextNonce)
	buffer.Write(proof.AmountResponse)
	buffer.Write(proof.BlindingResponse)
	buffer.Write(proof.RandomnessResponse)
	return buffer.Bytes(), nil
}

// UnmarshalAmountCiphertextProofBinary 反序列化金额密文一致性证明 + 拒绝长度畸形输入。
func UnmarshalAmountCiphertextProofBinary(data []byte) (AmountCiphertextProof, error) {
	if len(data) != amountCiphertextProofSize {
		return AmountCiphertextProof{}, fmt.Errorf("%w: amount ciphertext proof requires %d bytes, got %d", ErrInvalidProof, amountCiphertextProofSize, len(data))
	}
	reader := bytes.NewReader(data)
	version, err := readUint16(reader)
	if err != nil {
		return AmountCiphertextProof{}, err
	}
	proof := AmountCiphertextProof{Version: version}
	if proof.CommitmentNonce, err = readFixedBytes(reader, confidentialPointSize); err != nil {
		return AmountCiphertextProof{}, err
	}
	if proof.RandomnessNonce, err = readFixedBytes(reader, confidentialPointSize); err != nil {
		return AmountCiphertextProof{}, err
	}
	if proof.CiphertextNonce, err = readFixedBytes(reader, confidentialPointSize); err != nil {
		return AmountCiphertextProof{}, err
	}
	if proof.AmountResponse, err = readFixedBytes(reader, confidentialScalarSize); err != nil {
		return AmountCiphertextProof{}, err
	}
	if proof.BlindingResponse, err = readFixedBytes(reader, confidentialScalarSize); err != nil {
		return AmountCiphertextProof{}, err
	}
	if proof.RandomnessResponse, err = readFixedBytes(reader, confidentialScalarSize); err != nil {
		return AmountCiphertextProof{}, err
	}
	if reader.Len() != 0 {
		return AmountCiphertextProof{}, fmt.Errorf("%w: trailing amount ciphertext proof bytes", ErrInvalidProof)
	}
	return proof, proof.Validate()
}

// Validate 校验金额密文一致性证明结构 + 防止畸形点和越界标量进入验证。
func (proof AmountCiphertextProof) Validate() error {
	if proof.Version != ConfidentialProofVersion {
		return fmt.Errorf("%w: amount ciphertext proof version %d", ErrUnsupportedVersion, proof.Version)
	}
	if _, err := pointFromBytes(proof.CommitmentNonce); err != nil {
		return err
	}
	if _, err := pointFromBytes(proof.RandomnessNonce); err != nil {
		return err
	}
	if _, err := pointFromBytes(proof.CiphertextNonce); err != nil {
		return err
	}
	if _, _, _, err := amountCiphertextResponses(proof); err != nil {
		return err
	}
	return nil
}

func readAll(reader io.Reader, data []byte) error {
	_, err := io.ReadFull(reader, data)
	return err
}
