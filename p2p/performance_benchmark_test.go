package p2p

import (
	"bytes"
	"testing"

	"solana_golang/utils"
)

func BenchmarkMessageFrameRoundTrip(b *testing.B) {
	payload := bytes.Repeat([]byte{1}, 1024)
	message, err := NewMessage(ProtocolReceiveTransactionV1, payload)
	if err != nil {
		b.Fatalf("NewMessage() error = %v", err)
	}
	message.FromPeerID = testPeerID(80)
	message.ToPeerID = testPeerID(81)
	if err := message.Validate(DefaultMaxMessageSize); err != nil {
		b.Fatalf("message.Validate() error = %v", err)
	}

	var buffer bytes.Buffer
	b.SetBytes(int64(len(payload)))
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		buffer.Reset()
		if err := writeMessageFrame(&buffer, message, DefaultMaxMessageSize); err != nil {
			b.Fatalf("writeMessageFrame() error = %v", err)
		}
		if _, err := readMessageFrame(&buffer, DefaultMaxMessageSize); err != nil {
			b.Fatalf("readMessageFrame() error = %v", err)
		}
	}
}

func BenchmarkSecureSessionSealOpen(b *testing.B) {
	initiatorSession, responderSession := benchmarkSecureSessionPair(b)
	plaintext := bytes.Repeat([]byte{2}, 1024)
	associatedData := []byte("benchmark-aad")

	b.SetBytes(int64(len(plaintext)))
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		payload, err := initiatorSession.Seal(plaintext, associatedData)
		if err != nil {
			b.Fatalf("Seal() error = %v", err)
		}
		if _, err := responderSession.Open(payload, associatedData); err != nil {
			b.Fatalf("Open() error = %v", err)
		}
	}
}

func benchmarkSecureSessionPair(b *testing.B) (*SecureSession, *SecureSession) {
	b.Helper()
	initiatorIdentity := benchmarkSecureSessionIdentity(b, "localnet", "node/1.0.0")
	responderIdentity := benchmarkSecureSessionIdentity(b, "localnet", "node/1.0.1")
	initiatorState, err := NewSecureSessionState(initiatorIdentity, SecureSessionRoleInitiator)
	if err != nil {
		b.Fatalf("NewSecureSessionState(initiator) error = %v", err)
	}
	responderState, err := NewSecureSessionState(responderIdentity, SecureSessionRoleResponder)
	if err != nil {
		b.Fatalf("NewSecureSessionState(responder) error = %v", err)
	}
	initiatorSession, err := initiatorState.Finalize(responderState.Handshake(), responderIdentity.PeerID)
	if err != nil {
		b.Fatalf("Finalize(initiator) error = %v", err)
	}
	responderSession, err := responderState.Finalize(initiatorState.Handshake(), initiatorIdentity.PeerID)
	if err != nil {
		b.Fatalf("Finalize(responder) error = %v", err)
	}
	return initiatorSession, responderSession
}

func benchmarkSecureSessionIdentity(b *testing.B, networkID string, softwareVersion string) SecureSessionIdentity {
	b.Helper()
	keyPair, err := utils.GenerateEd25519KeyPair()
	if err != nil {
		b.Fatalf("GenerateEd25519KeyPair() error = %v", err)
	}
	return SecureSessionIdentity{
		PeerID:             utils.Base58Encode(keyPair.PublicKey),
		PublicKey:          keyPair.PublicKey,
		PrivateKey:         keyPair.PrivateKey,
		NetworkID:          networkID,
		SoftwareVersion:    softwareVersion,
		MinProtocolVersion: MessageProtocolVersion,
		MaxProtocolVersion: MessageProtocolVersion,
	}
}
