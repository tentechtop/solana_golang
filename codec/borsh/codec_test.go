package borsh

import (
	"bytes"
	"errors"
	"testing"

	"solana_golang/schema"
)

func TestWriterReaderGoldenBytes(t *testing.T) {
	writer := NewWriter(32)
	writer.WriteUint8(7)
	writer.WriteUint16(0x1234)
	writer.WriteUint32(0x01020304)
	writer.WriteUint64(0x0102030405060708)
	writer.WriteInt64(-2)
	writer.WriteBool(true)
	writer.WriteFixedBytes([]byte{7, 6, 5})
	if err := writer.WriteBytes([]byte{9, 8}); err != nil {
		t.Fatalf("WriteBytes() error = %v", err)
	}
	if err := writer.WriteString("sol"); err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}

	expected := []byte{
		7,
		0x34, 0x12,
		0x04, 0x03, 0x02, 0x01,
		0x08, 0x07, 0x06, 0x05, 0x04, 0x03, 0x02, 0x01,
		0xfe, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
		1,
		7, 6, 5,
		2, 0, 0, 0, 9, 8,
		3, 0, 0, 0, 's', 'o', 'l',
	}
	if !bytes.Equal(writer.Bytes(), expected) {
		t.Fatalf("Bytes() = %v, want %v", writer.Bytes(), expected)
	}

	reader := NewReader(expected, 32)
	assertUint8(t, reader, 7)
	assertUint16(t, reader, 0x1234)
	assertUint32(t, reader, 0x01020304)
	assertUint64(t, reader, 0x0102030405060708)
	assertInt64(t, reader, -2)
	assertBool(t, reader, true)
	assertFixedBytes(t, reader, 3, []byte{7, 6, 5})
	assertBytes(t, reader, []byte{9, 8})
	assertString(t, reader, "sol")
	if err := reader.EnsureEOF(); err != nil {
		t.Fatalf("EnsureEOF() error = %v", err)
	}
}

func TestReaderRejectsInvalidBoundaries(t *testing.T) {
	if _, err := NewReader([]byte{5, 0, 0, 0, 1, 2}, 4).ReadBytes(); !errors.Is(err, ErrInvalidLength) {
		t.Fatalf("ReadBytes(length exceeds max) error = %v, want ErrInvalidLength", err)
	}
	if _, err := NewReader([]byte{4, 0, 0, 0, 1}, 8).ReadBytes(); !errors.Is(err, ErrInvalidLength) {
		t.Fatalf("ReadBytes(length exceeds remaining) error = %v, want ErrInvalidLength", err)
	}
	if _, err := NewReader([]byte{2}, 8).ReadBool(); !errors.Is(err, ErrInvalidBool) {
		t.Fatalf("ReadBool(invalid) error = %v, want ErrInvalidBool", err)
	}
	if _, err := NewReader([]byte{1}, 8).ReadFixedBytes(2); !errors.Is(err, ErrInvalidLength) {
		t.Fatalf("ReadFixedBytes(length exceeds remaining) error = %v, want ErrInvalidLength", err)
	}
	if _, err := NewReader([]byte{1, 0, 0, 0, 0xff}, 8).ReadString(); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("ReadString(invalid utf8) error = %v, want ErrInvalidData", err)
	}
}

func TestNewEnvelopeUsesBorshCodec(t *testing.T) {
	envelope, err := NewEnvelope("account.state", 1, "account.state.borsh.v1", []byte{1, 2, 3})
	if err != nil {
		t.Fatalf("NewEnvelope() error = %v", err)
	}
	if envelope.Codec != schema.CodecBorsh {
		t.Fatalf("Codec = %s, want borsh", envelope.Codec)
	}
}

func assertUint8(t *testing.T, reader *Reader, expected uint8) {
	t.Helper()
	value, err := reader.ReadUint8()
	if err != nil {
		t.Fatalf("ReadUint8() error = %v", err)
	}
	if value != expected {
		t.Fatalf("ReadUint8() = %d, want %d", value, expected)
	}
}

func assertUint16(t *testing.T, reader *Reader, expected uint16) {
	t.Helper()
	value, err := reader.ReadUint16()
	if err != nil {
		t.Fatalf("ReadUint16() error = %v", err)
	}
	if value != expected {
		t.Fatalf("ReadUint16() = %d, want %d", value, expected)
	}
}

func assertUint32(t *testing.T, reader *Reader, expected uint32) {
	t.Helper()
	value, err := reader.ReadUint32()
	if err != nil {
		t.Fatalf("ReadUint32() error = %v", err)
	}
	if value != expected {
		t.Fatalf("ReadUint32() = %d, want %d", value, expected)
	}
}

func assertUint64(t *testing.T, reader *Reader, expected uint64) {
	t.Helper()
	value, err := reader.ReadUint64()
	if err != nil {
		t.Fatalf("ReadUint64() error = %v", err)
	}
	if value != expected {
		t.Fatalf("ReadUint64() = %d, want %d", value, expected)
	}
}

func assertInt64(t *testing.T, reader *Reader, expected int64) {
	t.Helper()
	value, err := reader.ReadInt64()
	if err != nil {
		t.Fatalf("ReadInt64() error = %v", err)
	}
	if value != expected {
		t.Fatalf("ReadInt64() = %d, want %d", value, expected)
	}
}

func assertBool(t *testing.T, reader *Reader, expected bool) {
	t.Helper()
	value, err := reader.ReadBool()
	if err != nil {
		t.Fatalf("ReadBool() error = %v", err)
	}
	if value != expected {
		t.Fatalf("ReadBool() = %t, want %t", value, expected)
	}
}

func assertFixedBytes(t *testing.T, reader *Reader, length int, expected []byte) {
	t.Helper()
	value, err := reader.ReadFixedBytes(length)
	if err != nil {
		t.Fatalf("ReadFixedBytes() error = %v", err)
	}
	if !bytes.Equal(value, expected) {
		t.Fatalf("ReadFixedBytes() = %v, want %v", value, expected)
	}
}

func assertBytes(t *testing.T, reader *Reader, expected []byte) {
	t.Helper()
	value, err := reader.ReadBytes()
	if err != nil {
		t.Fatalf("ReadBytes() error = %v", err)
	}
	if !bytes.Equal(value, expected) {
		t.Fatalf("ReadBytes() = %v, want %v", value, expected)
	}
}

func assertString(t *testing.T, reader *Reader, expected string) {
	t.Helper()
	value, err := reader.ReadString()
	if err != nil {
		t.Fatalf("ReadString() error = %v", err)
	}
	if value != expected {
		t.Fatalf("ReadString() = %q, want %q", value, expected)
	}
}
