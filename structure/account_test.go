package structure

import (
	"bytes"
	"errors"
	"testing"

	"solana_golang/codec/borsh"
)

func TestRentConfigMinimumBalance(t *testing.T) {
	zeroDataMinimum, err := MinimumBalanceForRentExemption(0)
	if err != nil {
		t.Fatalf("MinimumBalanceForRentExemption(0) error = %v", err)
	}
	expectedZeroDataMinimum := RentAccountStorageOverheadBytes * RentLamportsPerByteYear * RentExemptionThresholdYears
	if zeroDataMinimum != expectedZeroDataMinimum {
		t.Fatalf("minimum balance = %d, want %d", zeroDataMinimum, expectedZeroDataMinimum)
	}

	fiftyBytesMinimum, err := MinimumBalanceForRentExemption(50)
	if err != nil {
		t.Fatalf("MinimumBalanceForRentExemption(50) error = %v", err)
	}
	expectedFiftyBytesMinimum := (uint64(50) + RentAccountStorageOverheadBytes) * RentLamportsPerByteYear * RentExemptionThresholdYears
	if fiftyBytesMinimum != expectedFiftyBytesMinimum {
		t.Fatalf("minimum balance = %d, want %d", fiftyBytesMinimum, expectedFiftyBytesMinimum)
	}

	_, err = MinimumBalanceForRentExemption(MaxAccountDataSize + 1)
	if !errors.Is(err, ErrAccountDataTooLarge) {
		t.Fatalf("MinimumBalanceForRentExemption(too large) error = %v, want ErrAccountDataTooLarge", err)
	}
}

func TestNewAccountValidatesRentAndClonesData(t *testing.T) {
	data := []byte{1, 2, 3}
	minimumBalance := mustMinimumBalance(t, len(data))

	account, err := NewAccount(minimumBalance, data, newTestPublicKey(20), false, 7)
	if err != nil {
		t.Fatalf("NewAccount() error = %v", err)
	}
	data[0] = 9

	if account.Data[0] == data[0] {
		t.Fatal("NewAccount() did not clone data")
	}
	if account.DataLen() != 3 {
		t.Fatalf("DataLen() = %d, want 3", account.DataLen())
	}
	rentExempt, err := account.IsRentExempt(DefaultRentConfig)
	if err != nil {
		t.Fatalf("IsRentExempt() error = %v", err)
	}
	if !rentExempt {
		t.Fatal("IsRentExempt() = false, want true")
	}
}

func TestNewAccountRejectsInvalidRentState(t *testing.T) {
	minimumBalance := mustMinimumBalance(t, 1)

	_, err := NewAccount(minimumBalance-1, []byte{1}, newTestPublicKey(21), false, 0)
	if !errors.Is(err, ErrRentExemption) {
		t.Fatalf("NewAccount(low lamports) error = %v, want ErrRentExemption", err)
	}

	_, err = NewAccount(minimumBalance, []byte{1}, PublicKey{}, false, 0)
	if !errors.Is(err, ErrInvalidAccount) {
		t.Fatalf("NewAccount(empty owner) error = %v, want ErrInvalidAccount", err)
	}
}

func TestAccountMarshalRoundTrip(t *testing.T) {
	account := newRentExemptTestAccount(t, []byte{4, 5, 6})
	account.Executable = true
	account.RentEpoch = 9

	encoded, err := account.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	decoded, err := UnmarshalAccountBinary(encoded)
	if err != nil {
		t.Fatalf("UnmarshalAccountBinary() error = %v", err)
	}

	if decoded.Lamports != account.Lamports {
		t.Fatalf("Lamports = %d, want %d", decoded.Lamports, account.Lamports)
	}
	if !bytes.Equal(decoded.Data, account.Data) {
		t.Fatalf("Data = %v, want %v", decoded.Data, account.Data)
	}
	if decoded.Owner != account.Owner {
		t.Fatalf("Owner = %s, want %s", decoded.Owner.String(), account.Owner.String())
	}
	if decoded.Executable != account.Executable {
		t.Fatalf("Executable = %t, want %t", decoded.Executable, account.Executable)
	}
	if decoded.RentEpoch != account.RentEpoch {
		t.Fatalf("RentEpoch = %d, want %d", decoded.RentEpoch, account.RentEpoch)
	}
}

func TestAccountUnmarshalRejectsTrailingBytes(t *testing.T) {
	account := newRentExemptTestAccount(t, []byte{7})
	encoded, err := account.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}

	_, err = UnmarshalAccountBinary(append(encoded, 0))
	if !errors.Is(err, borsh.ErrInvalidData) {
		t.Fatalf("UnmarshalAccountBinary(trailing) error = %v, want borsh.ErrInvalidData", err)
	}
}

func TestAccountDebitPreservesRentExemption(t *testing.T) {
	minimumBalance := mustMinimumBalance(t, 2)
	account, err := NewAccount(minimumBalance+10, []byte{1, 2}, newTestPublicKey(22), false, 0)
	if err != nil {
		t.Fatalf("NewAccount() error = %v", err)
	}

	if err := account.DebitLamports(10, DefaultRentConfig); err != nil {
		t.Fatalf("DebitLamports(valid) error = %v", err)
	}
	if account.Lamports != minimumBalance {
		t.Fatalf("Lamports = %d, want %d", account.Lamports, minimumBalance)
	}

	err = account.DebitLamports(1, DefaultRentConfig)
	if !errors.Is(err, ErrRentExemption) {
		t.Fatalf("DebitLamports(below rent) error = %v, want ErrRentExemption", err)
	}
	if account.Lamports != minimumBalance {
		t.Fatalf("Lamports changed after failed debit = %d, want %d", account.Lamports, minimumBalance)
	}
}

func TestAccountSetDataRequiresRentReserveAndGrowthLimit(t *testing.T) {
	minimumBalance := mustMinimumBalance(t, 1)
	account, err := NewAccount(minimumBalance, []byte{1}, newTestPublicKey(23), false, 0)
	if err != nil {
		t.Fatalf("NewAccount() error = %v", err)
	}

	err = account.SetData([]byte{1, 2}, DefaultRentConfig)
	if !errors.Is(err, ErrRentExemption) {
		t.Fatalf("SetData(low rent) error = %v, want ErrRentExemption", err)
	}

	nextMinimumBalance := mustMinimumBalance(t, 2)
	if err := account.CreditLamports(nextMinimumBalance - account.Lamports); err != nil {
		t.Fatalf("CreditLamports() error = %v", err)
	}
	if err := account.SetData([]byte{1, 2}, DefaultRentConfig); err != nil {
		t.Fatalf("SetData(valid) error = %v", err)
	}

	largeData := make([]byte, account.DataLen()+MaxAccountDataIncreasePerInstruction+1)
	err = account.SetData(largeData, DefaultRentConfig)
	if !errors.Is(err, ErrAccountDataTooLarge) {
		t.Fatalf("SetData(large increase) error = %v, want ErrAccountDataTooLarge", err)
	}
}

func newRentExemptTestAccount(t *testing.T, data []byte) Account {
	t.Helper()

	account, err := NewAccount(mustMinimumBalance(t, len(data)), data, newTestPublicKey(24), false, 0)
	if err != nil {
		t.Fatalf("NewAccount() error = %v", err)
	}
	return account
}

func mustMinimumBalance(t *testing.T, dataLength int) uint64 {
	t.Helper()

	minimumBalance, err := MinimumBalanceForRentExemption(dataLength)
	if err != nil {
		t.Fatalf("MinimumBalanceForRentExemption() error = %v", err)
	}
	return minimumBalance
}
