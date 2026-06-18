package vm

import "testing"

func TestSetAccountRejectsOwnerChange(t *testing.T) {
	programID := testVMAddress(1)
	owner := testVMAddress(2)
	nextOwner := testVMAddress(3)
	account := Account{Address: testVMAddress(4), Lamports: 100, Owner: owner, IsWritable: true}
	accountSet, err := NewAccountSet(programID, []Account{account}, 0)
	if err != nil {
		t.Fatalf("NewAccountSet() error = %v", err)
	}

	updatedAccount := account
	updatedAccount.Owner = nextOwner
	if err := accountSet.SetAccount(0, updatedAccount); err == nil {
		t.Fatal("SetAccount() error = nil, want owner change rejection")
	}
}

func TestSetFixedProgramAccountAllowsWritableOwnerChange(t *testing.T) {
	programID := testVMAddress(11)
	owner := testVMAddress(12)
	nextOwner := testVMAddress(13)
	account := Account{Address: testVMAddress(14), Lamports: 100, Owner: owner, IsSigner: true, IsWritable: true}
	accountSet, err := NewAccountSet(programID, []Account{account}, 0)
	if err != nil {
		t.Fatalf("NewAccountSet() error = %v", err)
	}

	updatedAccount := account
	updatedAccount.Owner = nextOwner
	updatedAccount.Lamports = 120
	if err := accountSet.SetFixedProgramAccount(0, updatedAccount); err != nil {
		t.Fatalf("SetFixedProgramAccount() error = %v", err)
	}
	snapshot := accountSet.Snapshot()
	if snapshot[0].Owner != nextOwner || snapshot[0].Lamports != 120 {
		t.Fatalf("snapshot account = %+v, want owner and lamports updated", snapshot[0])
	}
	if !snapshot[0].IsSigner || !snapshot[0].IsWritable {
		t.Fatalf("snapshot permissions = signer %v writable %v, want preserved", snapshot[0].IsSigner, snapshot[0].IsWritable)
	}
}

func TestSetFixedProgramAccountRejectsReadonlyOwnerChange(t *testing.T) {
	programID := testVMAddress(21)
	owner := testVMAddress(22)
	nextOwner := testVMAddress(23)
	account := Account{Address: testVMAddress(24), Lamports: 100, Owner: owner, IsWritable: false}
	accountSet, err := NewAccountSet(programID, []Account{account}, 0)
	if err != nil {
		t.Fatalf("NewAccountSet() error = %v", err)
	}

	updatedAccount := account
	updatedAccount.Owner = nextOwner
	if err := accountSet.SetFixedProgramAccount(0, updatedAccount); err == nil {
		t.Fatal("SetFixedProgramAccount() error = nil, want readonly rejection")
	}
}

func testVMAddress(seed byte) Address {
	var address Address
	for index := range address {
		address[index] = seed + byte(index)
	}
	return address
}
