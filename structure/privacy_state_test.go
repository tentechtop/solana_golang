package structure

import "testing"

func TestUnmarshalPrivacyStateAcceptsPreallocatedZeroData(t *testing.T) {
	preallocatedData := make([]byte, 4096)

	state, err := UnmarshalPrivacyStateBinary(preallocatedData)
	if err != nil {
		t.Fatalf("UnmarshalPrivacyStateBinary(preallocated zero data) error = %v", err)
	}
	if state.Version != PrivacyStateVersion {
		t.Fatalf("state version = %d, want %d", state.Version, PrivacyStateVersion)
	}
	if len(state.Notes) != 0 {
		t.Fatalf("notes length = %d, want 0", len(state.Notes))
	}
	if len(state.SpentNullifiers) != 0 {
		t.Fatalf("spent nullifiers length = %d, want 0", len(state.SpentNullifiers))
	}
}

func TestUnmarshalPrivacyStateRejectsCorruptNonZeroData(t *testing.T) {
	corruptData := make([]byte, 4096)
	corruptData[0] = 2

	_, err := UnmarshalPrivacyStateBinary(corruptData)
	if err == nil {
		t.Fatal("UnmarshalPrivacyStateBinary(corrupt non-zero data) error = nil, want error")
	}
}
