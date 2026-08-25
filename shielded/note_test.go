package shielded

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestNoteCommitmentAndNullifierDomainSeparation(t *testing.T) {
	spendingKey, viewKey, address, err := DeriveKeySet(bytes.Repeat([]byte{0x71}, SeedSize), testDiversifier(0x01))
	if err != nil {
		t.Fatalf("DeriveKeySet() error = %v", err)
	}
	memo := []byte("wallet-only memo")
	note, err := NewNote(125_000, testRandomness(0x11), memo)
	if err != nil {
		t.Fatalf("NewNote() error = %v", err)
	}
	memo[0] ^= 0xff
	if string(note.Memo) != "wallet-only memo" {
		t.Fatal("NewNote() 未复制调用方 memo")
	}

	commitment, err := note.Commitment(address)
	if err != nil {
		t.Fatalf("Note.Commitment() error = %v", err)
	}
	if commitment.IsZero() {
		t.Fatal("票据承诺为零")
	}
	if err := validateCanonicalField(commitment[:]); err != nil {
		t.Fatalf("commitment is not canonical field: %v", err)
	}
	repeatedCommitment, err := ComputeNoteCommitment(address, note)
	if err != nil || repeatedCommitment != commitment {
		t.Fatalf("ComputeNoteCommitment() = %v, %v; want deterministic %v", repeatedCommitment, err, commitment)
	}

	differentValue := note
	differentValue.Value++
	assertDifferentCommitment(t, address, commitment, differentValue, "value")
	differentRandomness := note
	differentRandomness.Randomness[NoteRandomnessSize-1]++
	assertDifferentCommitment(t, address, commitment, differentRandomness, "randomness")
	differentMemo := note
	differentMemo.Memo = []byte("different encrypted memo")
	memoCommitment, err := differentMemo.Commitment(address)
	if err != nil {
		t.Fatalf("memo commitment error = %v", err)
	}
	if memoCommitment != commitment {
		t.Fatal("memo 不属于电路 note commitment，修改 memo 不应改变 commitment")
	}
	_, _, differentAddress, err := DeriveKeySet(bytes.Repeat([]byte{0x72}, SeedSize), testDiversifier(0x01))
	if err != nil {
		t.Fatalf("DeriveKeySet() different address error = %v", err)
	}
	assertDifferentCommitment(t, differentAddress, commitment, note, "owner tag")

	nullifier, err := ComputeNullifier(viewKey, commitment, 7)
	if err != nil {
		t.Fatalf("ComputeNullifier() error = %v", err)
	}
	repeatedNullifier, err := spendingKey.Nullifier(address.Diversifier, commitment, 7)
	if err != nil || repeatedNullifier != nullifier {
		t.Fatalf("SpendingKey.Nullifier() = %v, %v; want %v", repeatedNullifier, err, nullifier)
	}
	if nullifier == commitment {
		t.Fatal("nullifier 未与 note commitment 做 MiMC 域分离")
	}
	differentPositionNullifier, err := ComputeNullifier(viewKey, commitment, 8)
	if err != nil {
		t.Fatalf("ComputeNullifier() different position error = %v", err)
	}
	if differentPositionNullifier == nullifier {
		t.Fatal("nullifier 未绑定叶子位置")
	}
	_, differentViewKey, _, err := DeriveKeySet(bytes.Repeat([]byte{0x73}, SeedSize), testDiversifier(0x01))
	if err != nil {
		t.Fatalf("DeriveKeySet() different view key error = %v", err)
	}
	differentKeyNullifier, err := ComputeNullifier(differentViewKey, commitment, 7)
	if err != nil {
		t.Fatalf("ComputeNullifier() different key error = %v", err)
	}
	if differentKeyNullifier == nullifier {
		t.Fatal("nullifier 未绑定查看密钥的 nullifier key")
	}
	computedNoteNullifier, err := ComputeNoteNullifier(viewKey, address, note, 7)
	if err != nil || computedNoteNullifier != nullifier {
		t.Fatalf("ComputeNoteNullifier() = %v, %v; want %v", computedNoteNullifier, err, nullifier)
	}
	if _, err := ComputeNoteNullifier(differentViewKey, address, note, 7); err == nil {
		t.Fatal("ComputeNoteNullifier() 接受不属于查看密钥的地址")
	}
	if _, err := ComputeNullifier(viewKey, commitment, uint64(1)<<CircuitMerkleDepth); err == nil {
		t.Fatal("ComputeNullifier() 接受超出电路深度的位置")
	}
}

func TestNoteValidationAndRandomGeneration(t *testing.T) {
	if _, err := NewNote(0, testRandomness(1), nil); err == nil {
		t.Fatal("NewNote() 接受零金额")
	}
	if _, err := NewNote(1, NoteRandomness{}, nil); err == nil {
		t.Fatal("NewNote() 接受零随机量")
	}
	var nonCanonical NoteRandomness
	for index := range nonCanonical {
		nonCanonical[index] = 0xff
	}
	if _, err := NewNote(1, nonCanonical, nil); err == nil {
		t.Fatal("NewNote() 接受非规范域随机量")
	}
	if _, err := NewNote(1, testRandomness(1), make([]byte, MaxNoteMemoSize+1)); err == nil {
		t.Fatal("NewNote() 接受过长 memo")
	}
	if _, err := GenerateNoteWithReader(1, nil, nil); err == nil {
		t.Fatal("GenerateNoteWithReader() 接受 nil 随机源")
	}
	if _, err := GenerateNoteWithReader(1, nil, bytes.NewReader(make([]byte, 3))); err == nil {
		t.Fatal("GenerateNoteWithReader() 接受不足随机数据")
	}
	randomInput := bytes.Repeat([]byte{0x2a}, NoteRandomnessSize)
	generated, err := GenerateNoteWithReader(9, []byte("memo"), bytes.NewReader(randomInput))
	if err != nil {
		t.Fatalf("GenerateNoteWithReader() error = %v", err)
	}
	if generated.Value != 9 || string(generated.Memo) != "memo" || isAllZero(generated.Randomness[:]) {
		t.Fatal("GenerateNoteWithReader() 生成错误票据")
	}
}

func TestNoteEncryptionRoundTripAndWireEncoding(t *testing.T) {
	_, viewKey, address, err := DeriveKeySet(bytes.Repeat([]byte{0x81}, SeedSize), testDiversifier(0x21))
	if err != nil {
		t.Fatalf("DeriveKeySet() error = %v", err)
	}
	note, err := NewNote(99_500, testRandomness(0x33), []byte("encrypted memo"))
	if err != nil {
		t.Fatalf("NewNote() error = %v", err)
	}
	randomness := bytes.Repeat([]byte{0x91}, TransmissionKeySize+24)
	encryptedNote, err := EncryptNoteWithReader(address, note, bytes.NewReader(randomness))
	if err != nil {
		t.Fatalf("EncryptNoteWithReader() error = %v", err)
	}
	decryptedNote, err := DecryptNote(viewKey, encryptedNote)
	if err != nil {
		t.Fatalf("DecryptNote() error = %v", err)
	}
	assertNotesEqual(t, decryptedNote, note)

	wireBytes, err := encryptedNote.MarshalBinary()
	if err != nil {
		t.Fatalf("EncryptedNote.MarshalBinary() error = %v", err)
	}
	parsedEncryptedNote, err := ParseEncryptedNote(wireBytes)
	if err != nil {
		t.Fatalf("ParseEncryptedNote() error = %v", err)
	}
	parsedPlaintext, err := DecryptNote(viewKey, parsedEncryptedNote)
	if err != nil {
		t.Fatalf("DecryptNote(parsed) error = %v", err)
	}
	assertNotesEqual(t, parsedPlaintext, note)
	parsedEncryptedNote.Ciphertext[0] ^= 0xff
	if bytes.Equal(parsedEncryptedNote.Ciphertext, encryptedNote.Ciphertext) {
		t.Fatal("ParseEncryptedNote() 未复制密文字节")
	}

	secondEncryptedNote, err := EncryptNoteWithReader(address, note, bytes.NewReader(bytes.Repeat([]byte{0x92}, TransmissionKeySize+24)))
	if err != nil {
		t.Fatalf("EncryptNoteWithReader() second error = %v", err)
	}
	if bytes.Equal(secondEncryptedNote.Ciphertext, encryptedNote.Ciphertext) || secondEncryptedNote.EphemeralPublicKey == encryptedNote.EphemeralPublicKey {
		t.Fatal("不同临时随机量未隔离票据密文")
	}
}

func TestNoteEncryptionRejectsTamperingAndWrongViewKey(t *testing.T) {
	_, viewKey, address, err := DeriveKeySet(bytes.Repeat([]byte{0xa1}, SeedSize), testDiversifier(0x41))
	if err != nil {
		t.Fatalf("DeriveKeySet() error = %v", err)
	}
	note, err := NewNote(41, testRandomness(0x44), []byte("authenticated"))
	if err != nil {
		t.Fatalf("NewNote() error = %v", err)
	}
	encryptedNote, err := EncryptNoteWithReader(address, note, bytes.NewReader(bytes.Repeat([]byte{0xb1}, TransmissionKeySize+24)))
	if err != nil {
		t.Fatalf("EncryptNoteWithReader() error = %v", err)
	}

	testCases := []struct {
		name   string
		mutate func(*EncryptedNote)
	}{
		{name: "version", mutate: func(value *EncryptedNote) { value.Version++ }},
		{name: "ephemeral key", mutate: func(value *EncryptedNote) { value.EphemeralPublicKey[31] ^= 0x01 }},
		{name: "nonce", mutate: func(value *EncryptedNote) { value.Nonce[0] ^= 0x01 }},
		{name: "ciphertext", mutate: func(value *EncryptedNote) { value.Ciphertext[0] ^= 0x01 }},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			tampered := encryptedNote
			tampered.Ciphertext = cloneBytes(encryptedNote.Ciphertext)
			testCase.mutate(&tampered)
			if _, err := DecryptNote(viewKey, tampered); err == nil {
				t.Fatal("DecryptNote() 接受被篡改密文")
			}
		})
	}

	_, wrongViewKey, _, err := DeriveKeySet(bytes.Repeat([]byte{0xa2}, SeedSize), testDiversifier(0x41))
	if err != nil {
		t.Fatalf("DeriveKeySet() wrong key error = %v", err)
	}
	if _, err := DecryptNote(wrongViewKey, encryptedNote); err == nil {
		t.Fatal("DecryptNote() 接受错误查看密钥")
	}

	aadTamperedAddress := address
	aadTamperedAddress.OwnerTag[OwnerTagSize-1] ^= 0x01
	aadTamperedAddress.Checksum = computeAddressChecksum(aadTamperedAddress.payload())
	if err := aadTamperedAddress.Validate(); err != nil {
		t.Fatalf("AAD test address invalid: %v", err)
	}
	aadCiphertext, err := EncryptNoteWithReader(aadTamperedAddress, note, bytes.NewReader(bytes.Repeat([]byte{0xb2}, TransmissionKeySize+24)))
	if err != nil {
		t.Fatalf("EncryptNoteWithReader() AAD address error = %v", err)
	}
	if _, err := DecryptNote(viewKey, aadCiphertext); err == nil {
		t.Fatal("DecryptNote() 未将完整地址绑定到 AEAD associated data")
	}

	wireBytes, err := encryptedNote.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	lengthOffset := 1 + TransmissionKeySize + 24
	badLength := cloneBytes(wireBytes)
	binary.BigEndian.PutUint32(badLength[lengthOffset:lengthOffset+4], uint32(len(encryptedNote.Ciphertext)+1))
	if _, err := ParseEncryptedNote(badLength); err == nil {
		t.Fatal("ParseEncryptedNote() 接受错误密文长度")
	}
	if _, err := ParseEncryptedNote(append(cloneBytes(wireBytes), 0)); err == nil {
		t.Fatal("ParseEncryptedNote() 接受尾随字节")
	}
	if _, err := EncryptNoteWithReader(address, note, bytes.NewReader(make([]byte, 2))); err == nil {
		t.Fatal("EncryptNoteWithReader() 接受不足随机数据")
	}
}

func assertDifferentCommitment(t *testing.T, address ShieldedAddress, original Commitment, note Note, field string) {
	t.Helper()
	commitment, err := note.Commitment(address)
	if err != nil {
		t.Fatalf("commitment after changing %s error = %v", field, err)
	}
	if commitment == original {
		t.Fatalf("commitment did not bind %s", field)
	}
}

func assertNotesEqual(t *testing.T, actual Note, expected Note) {
	t.Helper()
	if actual.Value != expected.Value || actual.Randomness != expected.Randomness || !bytes.Equal(actual.Memo, expected.Memo) {
		t.Fatalf("note = %+v, want %+v", actual, expected)
	}
}

func testRandomness(lastByte byte) NoteRandomness {
	var randomness NoteRandomness
	randomness[NoteRandomnessSize-1] = lastByte
	return randomness
}
