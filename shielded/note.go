package shielded

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"

	"golang.org/x/crypto/chacha20poly1305"
)

const (
	// NoteRandomnessSize 是票据承诺随机量的长度。
	NoteRandomnessSize = 32
	// MaxNoteMemoSize 限制单个加密票据 memo 的最大长度。
	MaxNoteMemoSize = 512
	// EncryptedNoteVersion 是当前票据密文的 wire 版本。
	EncryptedNoteVersion uint8 = 1
)

const (
	notePlaintextVersion     uint8 = 1
	noteEncryptionSaltDomain       = "solana_golang.shielded.note_encryption_salt.v2"
	noteEncryptionKeyDomain        = "solana_golang.shielded.note_encryption_key.v2"
	noteEncryptionAADDomain        = "solana_golang.shielded.note_encryption_aad.v2"
)

const (
	notePlaintextFixedSize  = 1 + 8 + NoteRandomnessSize + 2
	encryptedNoteHeaderSize = 1 + TransmissionKeySize + chacha20poly1305.NonceSizeX + 4
	minimumCiphertextSize   = notePlaintextFixedSize + chacha20poly1305.Overhead
	maximumCiphertextSize   = notePlaintextFixedSize + MaxNoteMemoSize + chacha20poly1305.Overhead
)

// Digest 是屏蔽电路使用的 BN254 标量域规范大端摘要。
type Digest [FieldElementSize]byte

// Commitment 是域分离的票据承诺。
type Commitment = Digest

// Nullifier 是域分离且由查看密钥控制的票据花费标识。
type Nullifier = Digest

// NoteRandomness 随机化相同收款方和金额产生的票据承诺。
type NoteRandomness [NoteRandomnessSize]byte

// Note 是仅在钱包内以明文存在的票据内容。
type Note struct {
	Value      uint64
	Randomness NoteRandomness
	Memo       []byte
}

// EncryptedNote 是节点可以转发和存储的 XChaCha20-Poly1305 票据密文。
type EncryptedNote struct {
	Version            uint8
	EphemeralPublicKey TransmissionKey
	Nonce              [chacha20poly1305.NonceSizeX]byte
	Ciphertext         []byte
}

// Bytes 返回摘要副本。
func (digest Digest) Bytes() []byte {
	result := make([]byte, len(digest))
	copy(result, digest[:])
	return result
}

// String 返回固定长度十六进制摘要。
func (digest Digest) String() string {
	return hex.EncodeToString(digest[:])
}

// NewDigest 从规范 BN254 标量域编码创建摘要。
func NewDigest(encoded []byte) (Digest, error) {
	if err := validateCanonicalField(encoded); err != nil {
		return Digest{}, fmt.Errorf("shielded: parse digest: %w", err)
	}
	var digest Digest
	copy(digest[:], encoded)
	return digest, nil
}

// IsZero 判断摘要是否为标量域零元素。
func (digest Digest) IsZero() bool {
	return isAllZero(digest[:])
}

// NewNote 使用调用方提供的随机量创建票据并复制 memo。
func NewNote(value uint64, randomness NoteRandomness, memo []byte) (Note, error) {
	if value == 0 {
		return Note{}, fmt.Errorf("shielded: note value must be positive")
	}
	if len(memo) > MaxNoteMemoSize {
		return Note{}, fmt.Errorf("shielded: note memo exceeds %d bytes", MaxNoteMemoSize)
	}
	if isAllZero(randomness[:]) {
		return Note{}, fmt.Errorf("shielded: note randomness cannot be zero")
	}
	if err := validateCanonicalField(randomness[:]); err != nil {
		return Note{}, fmt.Errorf("shielded: invalid note randomness: %w", err)
	}
	return Note{
		Value:      value,
		Randomness: randomness,
		Memo:       cloneBytes(memo),
	}, nil
}

// GenerateNote 使用系统安全随机源创建票据随机量。
func GenerateNote(value uint64, memo []byte) (Note, error) {
	return GenerateNoteWithReader(value, memo, rand.Reader)
}

// GenerateNoteWithReader 使用指定随机源创建票据，主要用于确定性测试和受控钱包环境。
func GenerateNoteWithReader(value uint64, memo []byte, randomnessSource io.Reader) (Note, error) {
	if randomnessSource == nil {
		return Note{}, fmt.Errorf("shielded: randomness source cannot be nil")
	}
	for attempt := 0; attempt < 256; attempt++ {
		var randomness NoteRandomness
		if _, err := io.ReadFull(randomnessSource, randomness[:]); err != nil {
			return Note{}, fmt.Errorf("shielded: generate note randomness: %w", err)
		}
		randomness[0] &= 0x3f
		if isAllZero(randomness[:]) || validateCanonicalField(randomness[:]) != nil {
			continue
		}
		return NewNote(value, randomness, memo)
	}
	return Note{}, fmt.Errorf("shielded: cannot generate canonical note randomness")
}

// Commitment 计算绑定所有者标签、金额和随机量的票据承诺。
func (note Note) Commitment(address ShieldedAddress) (Commitment, error) {
	return ComputeNoteCommitment(address, note)
}

// ComputeNoteCommitment 计算具有 V2 域分离的票据承诺。
func ComputeNoteCommitment(address ShieldedAddress, note Note) (Commitment, error) {
	if err := address.Validate(); err != nil {
		return Commitment{}, fmt.Errorf("shielded: commit note with invalid address: %w", err)
	}
	if _, err := marshalNotePlaintext(note); err != nil {
		return Commitment{}, err
	}
	ownerTag := Digest(address.OwnerTag)
	value := digestFromUint64(note.Value)
	randomness := Digest(note.Randomness)
	commitment, err := mimcFieldHash(noteDomainTag, ownerTag, value, randomness)
	if err != nil {
		return Commitment{}, fmt.Errorf("shielded: compute note commitment: %w", err)
	}
	if commitment.IsZero() {
		return Commitment{}, fmt.Errorf("shielded: note commitment cannot be zero")
	}
	return commitment, nil
}

// ComputeNullifier 使用查看密钥、承诺和叶子位置确定性计算 nullifier。
func ComputeNullifier(viewKey ViewKey, commitment Commitment, position uint64) (Nullifier, error) {
	if isAllZero(viewKey.nullifierKey[:]) {
		return Nullifier{}, fmt.Errorf("shielded: view key is not initialized")
	}
	if commitment.IsZero() {
		return Nullifier{}, fmt.Errorf("shielded: commitment cannot be zero")
	}
	if position >= uint64(1)<<CircuitMerkleDepth {
		return Nullifier{}, fmt.Errorf("shielded: nullifier position %d exceeds circuit capacity", position)
	}

	nullifierKey := Digest(viewKey.nullifierKey)
	positionField := digestFromUint64(position)
	nullifier, err := mimcFieldHash(nullifierDomainTag, commitment, positionField, nullifierKey)
	if err != nil {
		return Nullifier{}, fmt.Errorf("shielded: compute nullifier: %w", err)
	}
	if nullifier.IsZero() {
		return Nullifier{}, fmt.Errorf("shielded: nullifier cannot be zero")
	}
	return nullifier, nil
}

// ComputeNoteNullifier 验证地址归属后，从票据内容和位置计算 nullifier。
func ComputeNoteNullifier(viewKey ViewKey, address ShieldedAddress, note Note, position uint64) (Nullifier, error) {
	if !viewKey.OwnsAddress(address) {
		return Nullifier{}, fmt.Errorf("shielded: view key does not own address")
	}
	commitment, err := ComputeNoteCommitment(address, note)
	if err != nil {
		return Nullifier{}, err
	}
	return ComputeNullifier(viewKey, commitment, position)
}

// Nullifier 使用指定 diversifier 派生查看密钥并计算 nullifier。
func (spendingKey SpendingKey) Nullifier(diversifier Diversifier, commitment Commitment, position uint64) (Nullifier, error) {
	viewKey, err := spendingKey.DeriveViewKey(diversifier)
	if err != nil {
		return Nullifier{}, err
	}
	return ComputeNullifier(viewKey, commitment, position)
}

// EncryptNote 使用临时 X25519 密钥和 XChaCha20-Poly1305 加密票据。
func EncryptNote(address ShieldedAddress, note Note) (EncryptedNote, error) {
	return EncryptNoteWithReader(address, note, rand.Reader)
}

// EncryptNoteWithReader 使用指定安全随机源生成临时密钥和 24 字节 nonce。
func EncryptNoteWithReader(address ShieldedAddress, note Note, randomnessSource io.Reader) (EncryptedNote, error) {
	if randomnessSource == nil {
		return EncryptedNote{}, fmt.Errorf("shielded: randomness source cannot be nil")
	}
	addressBytes, err := address.MarshalBinary()
	if err != nil {
		return EncryptedNote{}, fmt.Errorf("shielded: encrypt note for invalid address: %w", err)
	}
	plaintext, err := marshalNotePlaintext(note)
	if err != nil {
		return EncryptedNote{}, err
	}

	recipientPublicKey, err := ecdh.X25519().NewPublicKey(address.TransmissionKey[:])
	if err != nil {
		return EncryptedNote{}, fmt.Errorf("shielded: parse recipient transmission key: %w", err)
	}
	var ephemeralPrivateKeyBytes [TransmissionKeySize]byte
	if _, err := io.ReadFull(randomnessSource, ephemeralPrivateKeyBytes[:]); err != nil {
		return EncryptedNote{}, fmt.Errorf("shielded: generate ephemeral transmission key: %w", err)
	}
	ephemeralPrivateKey, err := ecdh.X25519().NewPrivateKey(ephemeralPrivateKeyBytes[:])
	if err != nil {
		return EncryptedNote{}, fmt.Errorf("shielded: parse ephemeral transmission key: %w", err)
	}
	sharedSecret, err := ephemeralPrivateKey.ECDH(recipientPublicKey)
	if err != nil {
		return EncryptedNote{}, fmt.Errorf("shielded: derive note shared secret: %w", err)
	}

	var encryptedNote EncryptedNote
	encryptedNote.Version = EncryptedNoteVersion
	copy(encryptedNote.EphemeralPublicKey[:], ephemeralPrivateKey.PublicKey().Bytes())
	if _, err := io.ReadFull(randomnessSource, encryptedNote.Nonce[:]); err != nil {
		return EncryptedNote{}, fmt.Errorf("shielded: generate note nonce: %w", err)
	}
	encryptionKey, err := deriveNoteEncryptionKey(sharedSecret, encryptedNote.EphemeralPublicKey, address.TransmissionKey)
	if err != nil {
		return EncryptedNote{}, err
	}
	aead, err := chacha20poly1305.NewX(encryptionKey)
	if err != nil {
		return EncryptedNote{}, fmt.Errorf("shielded: create note cipher: %w", err)
	}
	aad := noteEncryptionAAD(encryptedNote.Version, addressBytes, encryptedNote.EphemeralPublicKey)
	encryptedNote.Ciphertext = aead.Seal(nil, encryptedNote.Nonce[:], plaintext, aad)
	return encryptedNote, nil
}

// DecryptNote 使用查看密钥解密并认证票据，不需要暴露 SpendingKey。
func DecryptNote(viewKey ViewKey, encryptedNote EncryptedNote) (Note, error) {
	if err := encryptedNote.Validate(); err != nil {
		return Note{}, err
	}
	address, err := viewKey.Address()
	if err != nil {
		return Note{}, err
	}
	addressBytes, err := address.MarshalBinary()
	if err != nil {
		return Note{}, err
	}

	privateKey, err := ecdh.X25519().NewPrivateKey(viewKey.transmissionPrivateKey[:])
	if err != nil {
		return Note{}, fmt.Errorf("shielded: parse viewing transmission key: %w", err)
	}
	ephemeralPublicKey, err := ecdh.X25519().NewPublicKey(encryptedNote.EphemeralPublicKey[:])
	if err != nil {
		return Note{}, fmt.Errorf("shielded: parse ephemeral transmission key: %w", err)
	}
	sharedSecret, err := privateKey.ECDH(ephemeralPublicKey)
	if err != nil {
		return Note{}, fmt.Errorf("shielded: derive note shared secret: %w", err)
	}
	encryptionKey, err := deriveNoteEncryptionKey(sharedSecret, encryptedNote.EphemeralPublicKey, address.TransmissionKey)
	if err != nil {
		return Note{}, err
	}
	aead, err := chacha20poly1305.NewX(encryptionKey)
	if err != nil {
		return Note{}, fmt.Errorf("shielded: create note cipher: %w", err)
	}
	aad := noteEncryptionAAD(encryptedNote.Version, addressBytes, encryptedNote.EphemeralPublicKey)
	plaintext, err := aead.Open(nil, encryptedNote.Nonce[:], encryptedNote.Ciphertext, aad)
	if err != nil {
		return Note{}, fmt.Errorf("shielded: decrypt note: authentication failed")
	}
	return parseNotePlaintext(plaintext)
}

// Validate 验证密文版本、公钥和长度边界。
func (encryptedNote EncryptedNote) Validate() error {
	if encryptedNote.Version != EncryptedNoteVersion {
		return fmt.Errorf("shielded: unsupported encrypted note version %d", encryptedNote.Version)
	}
	if err := validateTransmissionKey(encryptedNote.EphemeralPublicKey); err != nil {
		return fmt.Errorf("shielded: invalid ephemeral transmission key: %w", err)
	}
	if len(encryptedNote.Ciphertext) < minimumCiphertextSize || len(encryptedNote.Ciphertext) > maximumCiphertextSize {
		return fmt.Errorf("shielded: encrypted note ciphertext length %d is outside [%d, %d]", len(encryptedNote.Ciphertext), minimumCiphertextSize, maximumCiphertextSize)
	}
	return nil
}

// MarshalBinary 返回密文的规范 wire 编码。
func (encryptedNote EncryptedNote) MarshalBinary() ([]byte, error) {
	if err := encryptedNote.Validate(); err != nil {
		return nil, err
	}
	encoded := make([]byte, 0, encryptedNoteHeaderSize+len(encryptedNote.Ciphertext))
	encoded = append(encoded, encryptedNote.Version)
	encoded = append(encoded, encryptedNote.EphemeralPublicKey[:]...)
	encoded = append(encoded, encryptedNote.Nonce[:]...)
	lengthBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(lengthBytes, uint32(len(encryptedNote.Ciphertext)))
	encoded = append(encoded, lengthBytes...)
	encoded = append(encoded, encryptedNote.Ciphertext...)
	return encoded, nil
}

// ParseEncryptedNote 解析并验证票据密文的规范 wire 编码。
func ParseEncryptedNote(encoded []byte) (EncryptedNote, error) {
	if len(encoded) < encryptedNoteHeaderSize {
		return EncryptedNote{}, fmt.Errorf("shielded: encrypted note requires at least %d bytes, got %d", encryptedNoteHeaderSize, len(encoded))
	}
	encryptedNote := EncryptedNote{Version: encoded[0]}
	offset := 1
	copy(encryptedNote.EphemeralPublicKey[:], encoded[offset:offset+TransmissionKeySize])
	offset += TransmissionKeySize
	copy(encryptedNote.Nonce[:], encoded[offset:offset+chacha20poly1305.NonceSizeX])
	offset += chacha20poly1305.NonceSizeX
	ciphertextLength := int(binary.BigEndian.Uint32(encoded[offset : offset+4]))
	offset += 4
	if ciphertextLength != len(encoded)-offset {
		return EncryptedNote{}, fmt.Errorf("shielded: encrypted note ciphertext length mismatch")
	}
	encryptedNote.Ciphertext = cloneBytes(encoded[offset:])
	if err := encryptedNote.Validate(); err != nil {
		return EncryptedNote{}, err
	}
	return encryptedNote, nil
}

func marshalNotePlaintext(note Note) ([]byte, error) {
	if note.Value == 0 {
		return nil, fmt.Errorf("shielded: note value must be positive")
	}
	if isAllZero(note.Randomness[:]) {
		return nil, fmt.Errorf("shielded: note randomness cannot be zero")
	}
	if err := validateCanonicalField(note.Randomness[:]); err != nil {
		return nil, fmt.Errorf("shielded: invalid note randomness: %w", err)
	}
	if len(note.Memo) > MaxNoteMemoSize {
		return nil, fmt.Errorf("shielded: note memo exceeds %d bytes", MaxNoteMemoSize)
	}
	encoded := make([]byte, 0, notePlaintextFixedSize+len(note.Memo))
	encoded = append(encoded, notePlaintextVersion)
	valueBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(valueBytes, note.Value)
	encoded = append(encoded, valueBytes...)
	encoded = append(encoded, note.Randomness[:]...)
	memoLengthBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(memoLengthBytes, uint16(len(note.Memo)))
	encoded = append(encoded, memoLengthBytes...)
	encoded = append(encoded, note.Memo...)
	return encoded, nil
}

func parseNotePlaintext(encoded []byte) (Note, error) {
	if len(encoded) < notePlaintextFixedSize {
		return Note{}, fmt.Errorf("shielded: note plaintext requires at least %d bytes, got %d", notePlaintextFixedSize, len(encoded))
	}
	if encoded[0] != notePlaintextVersion {
		return Note{}, fmt.Errorf("shielded: unsupported note plaintext version %d", encoded[0])
	}
	note := Note{Value: binary.BigEndian.Uint64(encoded[1:9])}
	offset := 9
	copy(note.Randomness[:], encoded[offset:offset+NoteRandomnessSize])
	offset += NoteRandomnessSize
	memoLength := int(binary.BigEndian.Uint16(encoded[offset : offset+2]))
	offset += 2
	if memoLength > MaxNoteMemoSize || memoLength != len(encoded)-offset {
		return Note{}, fmt.Errorf("shielded: note memo length mismatch")
	}
	note.Memo = cloneBytes(encoded[offset:])
	return note, nil
}

func deriveNoteEncryptionKey(sharedSecret []byte, ephemeralPublicKey TransmissionKey, recipientPublicKey TransmissionKey) ([]byte, error) {
	saltHasher := sha256.New()
	writeDomain(saltHasher, noteEncryptionSaltDomain)
	saltHasher.Write(ephemeralPublicKey[:])
	saltHasher.Write(recipientPublicKey[:])
	key, err := deriveHKDFSHA256(sharedSecret, saltHasher.Sum(nil), noteEncryptionKeyDomain, chacha20poly1305.KeySize)
	if err != nil {
		return nil, fmt.Errorf("shielded: derive note encryption key: %w", err)
	}
	return key, nil
}

func noteEncryptionAAD(version uint8, addressBytes []byte, ephemeralPublicKey TransmissionKey) []byte {
	aad := make([]byte, 0, 2+len(noteEncryptionAADDomain)+1+len(addressBytes)+len(ephemeralPublicKey))
	aad = appendDomain(aad, noteEncryptionAADDomain)
	aad = append(aad, version)
	aad = append(aad, addressBytes...)
	aad = append(aad, ephemeralPublicKey[:]...)
	return aad
}

func writeDomain(writer io.Writer, domain string) {
	length := []byte{byte(len(domain) >> 8), byte(len(domain))}
	_, _ = writer.Write(length)
	_, _ = writer.Write([]byte(domain))
}

func appendDomain(target []byte, domain string) []byte {
	target = append(target, byte(len(domain)>>8), byte(len(domain)))
	return append(target, domain...)
}

func cloneBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	result := make([]byte, len(value))
	copy(result, value)
	return result
}
