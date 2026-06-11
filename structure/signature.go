package structure

import (
	"fmt"

	"solana_golang/utils"
)

// SignatureVerification 描述签名校验结果 + 为交易池拒绝原因和日志提供明确上下文。
type SignatureVerification struct {
	PublicKey PublicKey
	Signature Signature
	Valid     bool
}

// SignMessage 签署消息字节 + 统一 Ed25519 私钥长度校验和错误上下文。
func SignMessage(privateKey []byte, message []byte) (Signature, error) {
	signatureBytes, err := utils.Ed25519Sign(privateKey, message)
	if err != nil {
		return Signature{}, fmt.Errorf("structure: sign message: %w", err)
	}
	signature, err := NewSignature(signatureBytes)
	if err != nil {
		return Signature{}, fmt.Errorf("structure: build signature: %w", err)
	}
	return signature, nil
}

// VerifyMessageSignature 校验消息签名 + 使用固定长度公钥和签名避免畸形输入。
func VerifyMessageSignature(publicKey PublicKey, message []byte, signature Signature) bool {
	if publicKey.IsZero() {
		return false
	}
	return utils.Ed25519Verify(publicKey[:], message, signature[:])
}

// VerifySignatures 校验交易签名 + 按消息账户顺序校验前 N 个签名账户。
func (transaction Transaction) VerifySignatures() ([]SignatureVerification, error) {
	message, err := transaction.SolanaMessage()
	if err != nil {
		return nil, err
	}
	if err := transaction.validateSignatureCount(message.Header.NumRequiredSignatures); err != nil {
		return nil, err
	}
	signData, err := message.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("structure: marshal message for signature verification: %w", err)
	}

	requiredSignatures := int(message.Header.NumRequiredSignatures)
	results := make([]SignatureVerification, requiredSignatures)
	for signatureIndex := 0; signatureIndex < requiredSignatures; signatureIndex++ {
		publicKey := message.AccountKeys[signatureIndex]
		signature := transaction.Signatures[signatureIndex]
		results[signatureIndex] = SignatureVerification{
			PublicKey: publicKey,
			Signature: signature,
			Valid:     VerifyMessageSignature(publicKey, signData, signature),
		}
	}
	return results, nil
}

// HasValidSignatures 判断交易签名是否全部有效 + 为交易池快速准入提供布尔结果。
func (transaction Transaction) HasValidSignatures() (bool, error) {
	results, err := transaction.VerifySignatures()
	if err != nil {
		return false, err
	}
	for _, result := range results {
		if !result.Valid {
			return false, nil
		}
	}
	return true, nil
}

// Sign 使用账户私钥签署交易 + 按消息签名账户顺序生成签名列表。
func (transaction Transaction) Sign(privateKeys map[PublicKey][]byte) (Transaction, error) {
	message, err := transaction.SolanaMessage()
	if err != nil {
		return Transaction{}, err
	}
	signData, err := message.MarshalBinary()
	if err != nil {
		return Transaction{}, fmt.Errorf("structure: marshal message for signing: %w", err)
	}

	signedTransaction := transaction.Clone()
	requiredSignatures := int(message.Header.NumRequiredSignatures)
	signedTransaction.Signatures = make([]Signature, requiredSignatures)
	for signatureIndex := 0; signatureIndex < requiredSignatures; signatureIndex++ {
		publicKey := message.AccountKeys[signatureIndex]
		privateKey, exists := privateKeys[publicKey]
		if !exists {
			return Transaction{}, fmt.Errorf("%w: missing private key for signer %d", ErrMissingRequiredSignature, signatureIndex)
		}
		signature, err := SignMessage(privateKey, signData)
		if err != nil {
			return Transaction{}, fmt.Errorf("structure: sign account %d: %w", signatureIndex, err)
		}
		signedTransaction.Signatures[signatureIndex] = signature
	}
	return signedTransaction, nil
}
