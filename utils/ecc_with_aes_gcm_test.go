package utils

import (
	"bytes"
	"testing"
)

func TestECCWithAESGCM(t *testing.T) {
	aliceKeys, err := GenerateCurve25519KeyPair()
	if err != nil {
		t.Fatalf("GenerateCurve25519KeyPair() alice error = %v", err)
	}
	bobKeys, err := GenerateCurve25519KeyPair()
	if err != nil {
		t.Fatalf("GenerateCurve25519KeyPair() bob error = %v", err)
	}

	if len(aliceKeys.PrivateKey) != Curve25519KeySize {
		t.Fatalf("alice private key length = %d, want %d", len(aliceKeys.PrivateKey), Curve25519KeySize)
	}
	if len(aliceKeys.PublicKey) != Curve25519KeySize {
		t.Fatalf("alice public key length = %d, want %d", len(aliceKeys.PublicKey), Curve25519KeySize)
	}

	aliceShared, err := GenerateSharedSecret(aliceKeys.PrivateKey, bobKeys.PublicKey)
	if err != nil {
		t.Fatalf("GenerateSharedSecret() alice error = %v", err)
	}
	bobShared, err := GenerateSharedSecret(bobKeys.PrivateKey, aliceKeys.PublicKey)
	if err != nil {
		t.Fatalf("GenerateSharedSecret() bob error = %v", err)
	}
	if !bytes.Equal(aliceShared, bobShared) {
		t.Fatal("shared secrets are not equal")
	}

	salt := bytes.Repeat([]byte{0x7a}, AESGCMHKDFSaltSize)
	aliceAESKey, err := DeriveAESKeyWithSalt(aliceShared, salt)
	if err != nil {
		t.Fatalf("DeriveAESKeyWithSalt() alice error = %v", err)
	}
	bobAESKey, err := DeriveAESKeyWithSalt(bobShared, salt)
	if err != nil {
		t.Fatalf("DeriveAESKeyWithSalt() bob error = %v", err)
	}
	if !bytes.Equal(aliceAESKey, bobAESKey) {
		t.Fatal("derived aes keys are not equal")
	}
	if len(aliceAESKey) != AES256KeySize {
		t.Fatalf("derived aes key length = %d, want %d", len(aliceAESKey), AES256KeySize)
	}

	message := []byte("curve25519 + aes-gcm message")
	encrypted, err := AESGCMEncrypt(aliceAESKey, message)
	if err != nil {
		t.Fatalf("AESGCMEncrypt() error = %v", err)
	}
	wantEncryptedLength := AESGCMNonceSize + len(message) + AESGCMTagSize
	if len(encrypted) != wantEncryptedLength {
		t.Fatalf("AESGCMEncrypt() length = %d, want %d", len(encrypted), wantEncryptedLength)
	}

	decrypted, err := AESGCMDecrypt(bobAESKey, encrypted)
	if err != nil {
		t.Fatalf("AESGCMDecrypt() error = %v", err)
	}
	if !bytes.Equal(decrypted, message) {
		t.Fatalf("AESGCMDecrypt() = %q, want %q", decrypted, message)
	}

	tampered := CloneBytes(encrypted)
	tampered[len(tampered)-1] ^= 0x01
	if _, err := AESGCMDecrypt(bobAESKey, tampered); err == nil {
		t.Fatal("AESGCMDecrypt() tampered error = nil, want error")
	}
}

func TestX25519RFC7748Vector(t *testing.T) {
	alicePrivate, err := HexToBytes("77076d0a7318a57d3c16c17251b26645df4c2f87ebc0992ab177fba51db92c2a")
	if err != nil {
		t.Fatalf("HexToBytes(alice private) error = %v", err)
	}
	alicePublic, err := HexToBytes("8520f0098930a754748b7ddcb43ef75a0dbf3a0d26381af4eba4a98eaa9b4e6a")
	if err != nil {
		t.Fatalf("HexToBytes(alice public) error = %v", err)
	}
	bobPrivate, err := HexToBytes("5dab087e624a8a4b79e17f8b83800ee66f3bb1292618b6fd1c2f8b27ff88e0eb")
	if err != nil {
		t.Fatalf("HexToBytes(bob private) error = %v", err)
	}
	bobPublic, err := HexToBytes("de9edb7d7b7dc1b4d35b61c2ece435373f8343c85b78674dadfc7e146f882b4f")
	if err != nil {
		t.Fatalf("HexToBytes(bob public) error = %v", err)
	}
	wantShared, err := HexToBytes("4a5d9d5ba4ce2de1728e3bf480350f25e07e21c947d19e3376f09b3c1e161742")
	if err != nil {
		t.Fatalf("HexToBytes(shared secret) error = %v", err)
	}

	aliceShared, err := GenerateSharedSecret(alicePrivate, bobPublic)
	if err != nil {
		t.Fatalf("GenerateSharedSecret(alice, bob) error = %v", err)
	}
	bobShared, err := GenerateSharedSecret(bobPrivate, alicePublic)
	if err != nil {
		t.Fatalf("GenerateSharedSecret(bob, alice) error = %v", err)
	}
	if !bytes.Equal(aliceShared, wantShared) {
		t.Fatalf("alice shared secret = %x, want %x", aliceShared, wantShared)
	}
	if !bytes.Equal(bobShared, wantShared) {
		t.Fatalf("bob shared secret = %x, want %x", bobShared, wantShared)
	}
}

func TestHKDFSHA256RFC5869Vector(t *testing.T) {
	ikm := bytes.Repeat([]byte{0x0b}, 22)
	salt, err := HexToBytes("000102030405060708090a0b0c")
	if err != nil {
		t.Fatalf("HexToBytes(salt) error = %v", err)
	}
	info, err := HexToBytes("f0f1f2f3f4f5f6f7f8f9")
	if err != nil {
		t.Fatalf("HexToBytes(info) error = %v", err)
	}
	wantOKM, err := HexToBytes("3cb25f25faacd57a90434f64d0362f2a2d2d0a90cf1a5a4c5db02d56ecc4c5bf34007208d5b887185865")
	if err != nil {
		t.Fatalf("HexToBytes(okm) error = %v", err)
	}

	okm, err := hkdfSHA256(ikm, salt, info, 42)
	if err != nil {
		t.Fatalf("hkdfSHA256() error = %v", err)
	}
	if !bytes.Equal(okm, wantOKM) {
		t.Fatalf("hkdfSHA256() = %x, want %x", okm, wantOKM)
	}
}

func TestECCWithAESGCMValidation(t *testing.T) {
	if _, err := GenerateSharedSecret([]byte{1}, make([]byte, Curve25519KeySize)); err == nil {
		t.Fatal("GenerateSharedSecret() short private key error = nil, want error")
	}
	if _, err := GenerateSharedSecret(make([]byte, Curve25519KeySize), []byte{1}); err == nil {
		t.Fatal("GenerateSharedSecret() short public key error = nil, want error")
	}
	if _, err := DeriveAESKeyWithSalt([]byte{1}, bytes.Repeat([]byte{1}, AESGCMHKDFSaltSize)); err == nil {
		t.Fatal("DeriveAESKeyWithSalt() short shared secret error = nil, want error")
	}
	if _, err := DeriveAESKeyWithSalt(make([]byte, Curve25519KeySize), nil); err == nil {
		t.Fatal("DeriveAESKeyWithSalt() empty salt error = nil, want error")
	}
	if _, err := AESGCMEncrypt([]byte{1}, []byte("hello")); err == nil {
		t.Fatal("AESGCMEncrypt() short key error = nil, want error")
	}
	if _, err := AESGCMDecrypt(make([]byte, AES256KeySize), []byte{1}); err == nil {
		t.Fatal("AESGCMDecrypt() short encrypted data error = nil, want error")
	}
	if _, err := DeriveAESKeyWithSalt(make([]byte, Curve25519KeySize), []byte{}); err == nil {
		t.Fatal("DeriveAESKeyWithSalt(empty salt) error = nil, want error")
	}
	if _, err := hkdfSHA256([]byte("ikm"), nil, nil, -1); err == nil {
		t.Fatal("hkdfSHA256(negative length) error = nil, want error")
	}
	if _, err := hkdfSHA256([]byte("ikm"), nil, nil, 255*32+1); err == nil {
		t.Fatal("hkdfSHA256(too long) error = nil, want error")
	}
	invalidPeer := make([]byte, Curve25519KeySize)
	if _, err := GenerateSharedSecret(make([]byte, Curve25519KeySize), invalidPeer); err == nil {
		t.Fatal("GenerateSharedSecret(low-order public key) error = nil, want error")
	}
}

func TestECCWithAESGCMAliases(t *testing.T) {
	alicePrivate, alicePublic, err := GenerateCurve25519KeyPairBytes()
	if err != nil {
		t.Fatalf("GenerateCurve25519KeyPairBytes() alice error = %v", err)
	}
	bobPrivate, bobPublic, err := GenerateCurve25519KeyPairBytes()
	if err != nil {
		t.Fatalf("GenerateCurve25519KeyPairBytes() bob error = %v", err)
	}

	aliceShared, err := GenerateSharedSecret(alicePrivate, bobPublic)
	if err != nil {
		t.Fatalf("GenerateSharedSecret() alice error = %v", err)
	}
	bobShared, err := GenerateSharedSecret(bobPrivate, alicePublic)
	if err != nil {
		t.Fatalf("GenerateSharedSecret() bob error = %v", err)
	}

	salt := bytes.Repeat([]byte{0x11}, AESGCMHKDFSaltSize)
	aliceKey, err := DeriveAesKeyWithSalt(aliceShared, salt)
	if err != nil {
		t.Fatalf("DeriveAesKeyWithSalt() alice error = %v", err)
	}
	bobKey, err := DeriveAesKeyWithSalt(bobShared, salt)
	if err != nil {
		t.Fatalf("DeriveAesKeyWithSalt() bob error = %v", err)
	}

	encrypted, err := AesGcmEncrypt(aliceKey, []byte("alias message"))
	if err != nil {
		t.Fatalf("AesGcmEncrypt() error = %v", err)
	}
	decrypted, err := AesGcmDecrypt(bobKey, encrypted)
	if err != nil {
		t.Fatalf("AesGcmDecrypt() error = %v", err)
	}
	if string(decrypted) != "alias message" {
		t.Fatalf("AesGcmDecrypt() = %q, want alias message", decrypted)
	}

	defaultKey, err := DeriveAesKey(aliceShared)
	if err != nil {
		t.Fatalf("DeriveAesKey() error = %v", err)
	}
	if len(defaultKey) != AES256KeySize {
		t.Fatalf("DeriveAesKey() length = %d, want %d", len(defaultKey), AES256KeySize)
	}
	wrongKey := CloneBytes(aliceKey)
	wrongKey[0] ^= 0xff
	if _, err := AesGcmDecrypt(wrongKey, encrypted); err == nil {
		t.Fatal("AesGcmDecrypt(wrong key) error = nil, want error")
	}
}
