package schema

import (
	"bytes"
	"errors"
	"testing"
)

func TestRegistryDecodeEnvelope(t *testing.T) {
	registry := NewRegistry()
	record := Schema{
		Key: SchemaKey{
			Type:     "example.message",
			Version:  1,
			Codec:    CodecBorsh,
			SchemaID: "example.message.borsh.v1",
		},
		Decode: func(payload []byte) (any, error) {
			return string(payload), nil
		},
		Validate: func(value any) error {
			if value != "hello" {
				t.Fatalf("Validate() value = %v, want hello", value)
			}
			return nil
		},
	}
	if err := registry.Register(record); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	envelope, err := NewEnvelope(record.Key.Type, record.Key.Version, record.Key.Codec, record.Key.SchemaID, []byte("hello"))
	if err != nil {
		t.Fatalf("NewEnvelope() error = %v", err)
	}
	encoded, err := envelope.MarshalBinary(DefaultMaxPayloadSize)
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	decodedEnvelope, err := UnmarshalEnvelope(encoded, DefaultMaxPayloadSize)
	if err != nil {
		t.Fatalf("UnmarshalEnvelope() error = %v", err)
	}

	decoded, err := registry.DecodeEnvelope(decodedEnvelope)
	if err != nil {
		t.Fatalf("DecodeEnvelope() error = %v", err)
	}
	if decoded != "hello" {
		t.Fatalf("DecodeEnvelope() = %v, want hello", decoded)
	}
}

func TestRegistryRejectsDuplicateAndUnregistered(t *testing.T) {
	registry := NewRegistry()
	record := Schema{
		Key: SchemaKey{
			Type:     "example.message",
			Version:  1,
			Codec:    CodecBorsh,
			SchemaID: "example.message.borsh.v1",
		},
		Decode: func(payload []byte) (any, error) {
			return payload, nil
		},
	}
	if err := registry.Register(record); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := registry.Register(record); !errors.Is(err, ErrSchemaRegistered) {
		t.Fatalf("Register(duplicate) error = %v, want ErrSchemaRegistered", err)
	}
	conflictingRecord := record
	conflictingRecord.Key.Type = "other.message"
	if err := registry.Register(conflictingRecord); !errors.Is(err, ErrSchemaRegistered) {
		t.Fatalf("Register(conflicting schema id) error = %v, want ErrSchemaRegistered", err)
	}

	envelope, err := NewEnvelope("missing.message", 1, CodecBorsh, "missing.borsh.v1", []byte("x"))
	if err != nil {
		t.Fatalf("NewEnvelope() error = %v", err)
	}
	if _, err := registry.DecodeEnvelope(envelope); !errors.Is(err, ErrSchemaNotFound) {
		t.Fatalf("DecodeEnvelope(unregistered) error = %v, want ErrSchemaNotFound", err)
	}
}

func TestEnvelopeRejectsHashMismatch(t *testing.T) {
	envelope, err := NewEnvelope("example.message", 1, CodecBorsh, "example.message.borsh.v1", []byte("hello"))
	if err != nil {
		t.Fatalf("NewEnvelope() error = %v", err)
	}
	envelope.PayloadBytes[0] = 'j'
	if err := envelope.Validate(DefaultMaxPayloadSize); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("Validate(hash mismatch) error = %v, want ErrHashMismatch", err)
	}
}

func TestCanonicalHashIncludesMetadata(t *testing.T) {
	hash, err := CanonicalHash("solana_golang:test", "transfer", 1, []byte("payload"))
	if err != nil {
		t.Fatalf("CanonicalHash() error = %v", err)
	}
	otherDomainHash, err := CanonicalHash("solana_golang:other", "transfer", 1, []byte("payload"))
	if err != nil {
		t.Fatalf("CanonicalHash(other domain) error = %v", err)
	}
	otherVersionHash, err := CanonicalHash("solana_golang:test", "transfer", 2, []byte("payload"))
	if err != nil {
		t.Fatalf("CanonicalHash(other version) error = %v", err)
	}
	if bytes.Equal(hash[:], otherDomainHash[:]) {
		t.Fatal("CanonicalHash() ignored domain separator")
	}
	if bytes.Equal(hash[:], otherVersionHash[:]) {
		t.Fatal("CanonicalHash() ignored version")
	}
	if _, err := CanonicalHash("", "transfer", 1, []byte("payload")); !errors.Is(err, ErrInvalidCanonical) {
		t.Fatalf("CanonicalHash(empty domain) error = %v, want ErrInvalidCanonical", err)
	}
}
