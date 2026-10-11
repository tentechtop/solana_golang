package vmprogram

import (
	"bytes"
	"reflect"
	"strconv"
	"testing"

	"github.com/tentechtop/ordexus-core/runtime"
	"github.com/tentechtop/ordexus-core/structure"
	ovm "github.com/tentechtop/ordexus-core/vm"
	"github.com/tentechtop/ordexus-core/vm/assembler"
)

type fundFixture struct {
	t                                           *testing.T
	program, creator, beneficiary, donor, state structure.PublicKey
	accounts                                    map[structure.PublicKey]structure.Account
}

func newFundFixture(t *testing.T, mode uint8) *fundFixture {
	f := &fundFixture{t: t, program: StablecoinAddress("fund-test", "program"), creator: StablecoinAddress("fund-test", "creator"), beneficiary: StablecoinAddress("fund-test", "beneficiary"), donor: StablecoinAddress("fund-test", "donor")}
	f.state = FundraiserAddress(f.program)
	code, _ := FundraiserProgramData()
	f.accounts = map[structure.PublicKey]structure.Account{f.program: {Owner: structure.DefaultBuiltinProgramIDs.BPFLoader, Executable: true, Lamports: 100000000, Data: code}, f.state: {Owner: f.program, Lamports: 100000000}}
	for _, key := range []structure.PublicKey{f.creator, f.beneficiary, f.donor} {
		f.accounts[key] = structure.Account{Owner: structure.DefaultBuiltinProgramIDs.System, Lamports: 1000000000}
	}
	for _, key := range []structure.PublicKey{f.creator, f.donor} {
		f.accounts[FundraiserReceiptAddress(f.program, key)] = structure.Account{Owner: f.program, Lamports: 100000000}
	}
	deadline, target := uint64(20), uint64(1000)
	if mode == FundDonation {
		deadline = 0
		target = 0
	}
	f.call(true, f.creator, AssetInstruction{Type: FundInitialize, Decimals: mode, Name: "Community Fund", Symbol: strconv.FormatUint(deadline, 10), URI: f.beneficiary.String(), Amount: target}, 10)
	return f
}

func (f *fundFixture) call(ok bool, actor structure.PublicKey, i AssetInstruction, slot uint64, override ...structure.PublicKey) {
	f.t.Helper()
	keys := []structure.PublicKey{actor, f.state}
	if i.Type == FundContribute || i.Type == FundRefund {
		state, _ := UnmarshalFundraiserState(f.accounts[f.state].Data)
		extra := FundraiserReceiptAddress(f.program, actor)
		if state.Mode == FundDonation {
			extra = state.Beneficiary
		}
		if len(override) > 0 {
			extra = override[0]
		}
		keys = append(keys, extra)
	}
	indexes := make([]uint8, len(keys))
	for n := range indexes {
		indexes[n] = uint8(n)
	}
	keys = append(keys, f.program)
	wire, err := i.MarshalBinary()
	if err != nil {
		f.t.Fatal(err)
	}
	before := cloneStructureAccounts(f.accounts)
	c := runtime.InstructionContext{Instruction: structure.CompiledInstruction{ProgramIDIndex: uint8(len(keys) - 1), AccountIndexes: indexes, Data: wire}, Message: structure.ResolvedMessage{Header: structure.MessageHeader{NumRequiredSignatures: 1, NumReadonlyUnsignedAccounts: 1}, StaticAccountKeys: keys, AccountKeys: keys}, Accounts: f.accounts, CurrentSlot: slot, RentConfig: structure.DefaultRentConfig, BuiltinPrograms: structure.DefaultBuiltinProgramIDs}
	err = NewProgram(structure.DefaultBuiltinProgramIDs.BPFLoader, ovm.Runtime{}).Execute(c)
	if (err == nil) != ok {
		f.t.Fatalf("operation %d slot %d: %v", i.Type, slot, err)
	}
	if !ok && !reflect.DeepEqual(before, f.accounts) {
		f.t.Fatal("rejected operation changed accounts")
	}
}

func TestFundraiserDonationPaysOnlyFixedBeneficiary(t *testing.T) {
	f := newFundFixture(t, FundDonation)
	f.call(false, f.donor, AssetInstruction{Type: FundContribute, Amount: 100}, 11, f.creator)
	f.call(true, f.donor, AssetInstruction{Type: FundContribute, Amount: 100}, 11)
	if f.accounts[f.beneficiary].Lamports != 1000000100 || f.accounts[f.donor].Lamports != 999999900 || f.accounts[f.state].Lamports != 100000000 {
		t.Fatal("donation did not move directly to beneficiary")
	}
	f.call(false, f.donor, AssetInstruction{Type: FundCancel}, 12)
	f.call(false, f.creator, AssetInstruction{Type: FundWithdraw}, 12)
	f.call(true, f.creator, AssetInstruction{Type: FundCancel}, 12)
	f.call(false, f.donor, AssetInstruction{Type: FundContribute, Amount: 100}, 13)
}

func TestFundraiserRejectsUnsafeAccountContextsWithoutMutation(t *testing.T) {
	for _, variant := range []string{"unsigned", "readonly payer", "readonly state", "wrong state owner", "insufficient funds", "rent violation", "wrong template"} {
		t.Run(variant, func(t *testing.T) {
			f := newFundFixture(t, FundDonation)
			wire, err := (AssetInstruction{Type: FundContribute, Amount: 100}).MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}
			keys := []structure.PublicKey{f.donor, f.state, f.beneficiary, f.program}
			c := runtime.InstructionContext{Instruction: structure.CompiledInstruction{ProgramIDIndex: 3, AccountIndexes: []uint8{0, 1, 2}, Data: wire}, Message: structure.ResolvedMessage{Header: structure.MessageHeader{NumRequiredSignatures: 1, NumReadonlyUnsignedAccounts: 1}, StaticAccountKeys: keys, AccountKeys: keys}, Accounts: f.accounts, CurrentSlot: 11, RentConfig: structure.DefaultRentConfig, BuiltinPrograms: structure.DefaultBuiltinProgramIDs}
			switch variant {
			case "unsigned":
				c.Message.Header.NumRequiredSignatures = 0
			case "readonly payer":
				c.Message.Header.NumReadonlySignedAccounts = 1
			case "readonly state":
				c.Message.Header.NumReadonlyUnsignedAccounts = 3
			case "wrong state owner":
				a := f.accounts[f.state]
				a.Owner = f.creator
				f.accounts[f.state] = a
			case "insufficient funds":
				a := f.accounts[f.donor]
				a.Lamports = 99
				f.accounts[f.donor] = a
			case "rent violation":
				a := f.accounts[f.donor]
				a.Lamports, err = structure.DefaultRentConfig.MinimumBalance(0)
				if err != nil {
					t.Fatal(err)
				}
				f.accounts[f.donor] = a
			case "wrong template":
				a := f.accounts[f.program]
				a.Data, err = ERC20LikeContractProgramData()
				if err != nil {
					t.Fatal(err)
				}
				f.accounts[f.program] = a
			}
			before := cloneStructureAccounts(f.accounts)
			if err := NewProgram(structure.DefaultBuiltinProgramIDs.BPFLoader, ovm.Runtime{}).Execute(c); err == nil {
				t.Fatal("unsafe contribution succeeded")
			}
			if !reflect.DeepEqual(before, f.accounts) {
				t.Fatal("rejected contribution changed balances or state")
			}
		})
	}
}

func TestFundraiserCrowdfundingDeadlinePayoutAndNoDoubleSpend(t *testing.T) {
	f := newFundFixture(t, FundCrowdfunding)
	f.call(true, f.donor, AssetInstruction{Type: FundContribute, Amount: 1200}, 11)
	f.call(false, f.beneficiary, AssetInstruction{Type: FundWithdraw}, 19)
	f.call(false, f.donor, AssetInstruction{Type: FundRefund}, 19)
	f.call(false, f.creator, AssetInstruction{Type: FundWithdraw}, 20)
	f.call(false, f.creator, AssetInstruction{Type: FundCancel}, 20)
	f.call(false, f.donor, AssetInstruction{Type: FundContribute, Amount: 1}, 20)
	f.call(true, f.beneficiary, AssetInstruction{Type: FundWithdraw}, 20)
	f.call(false, f.beneficiary, AssetInstruction{Type: FundWithdraw}, 21)
	f.call(false, f.donor, AssetInstruction{Type: FundRefund}, 21)
	if f.accounts[f.beneficiary].Lamports != 1000001200 || f.accounts[f.state].Lamports != 100000000 {
		t.Fatal("payout accounting or rent wrong")
	}
}

func TestFundraiserFailedAndCancelledCampaignsRefundOnlyContributor(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(strconv.FormatBool(cancel), func(t *testing.T) {
			f := newFundFixture(t, FundCrowdfunding)
			f.call(true, f.donor, AssetInstruction{Type: FundContribute, Amount: 300}, 11)
			f.call(true, f.donor, AssetInstruction{Type: FundContribute, Amount: 200}, 12)
			f.call(true, f.creator, AssetInstruction{Type: FundContribute, Amount: 100}, 12)
			f.call(false, f.donor, AssetInstruction{Type: FundRefund}, 13)
			slot := uint64(20)
			if cancel {
				f.call(true, f.creator, AssetInstruction{Type: FundCancel}, 13)
				slot = 14
			}
			f.call(false, f.creator, AssetInstruction{Type: FundRefund}, slot, FundraiserReceiptAddress(f.program, f.donor))
			f.call(true, f.donor, AssetInstruction{Type: FundRefund}, slot)
			f.call(false, f.donor, AssetInstruction{Type: FundRefund}, slot)
			f.call(true, f.creator, AssetInstruction{Type: FundRefund}, slot)
			if f.accounts[f.donor].Lamports != 1000000000 || f.accounts[f.creator].Lamports != 1000000000 || f.accounts[f.state].Lamports != 100000000 {
				t.Fatal("refund accounting wrong")
			}
		})
	}
}

func TestFundraiserTemplateAndStateCodec(t *testing.T) {
	code, _ := FundraiserProgramData()
	built, err := assembler.AssembleWithManifest("mov r9, 0x46554e31\nsyscall asset_execute\nexit\n", ovm.ProgramManifest{ComputeUnitLimit: ovm.DefaultComputeUnitLimit, RequiredSyscalls: []ovm.SyscallID{ovm.SyscallAssetExecute}})
	if err != nil || !bytes.Equal(code, built) || IsStablecoinProgram(code) {
		t.Fatal("template mismatch")
	}
	f := newFundFixture(t, FundCrowdfunding)
	s, err := UnmarshalFundraiserState(f.accounts[f.state].Data)
	if err != nil || s.Target != 1000 {
		t.Fatal("state codec")
	}
	if _, err = UnmarshalFundraiserState(append(f.accounts[f.state].Data, 0)); err == nil {
		t.Fatal("trailing bytes accepted")
	}
	s.Withdrawn = 1001
	if _, err = s.MarshalBinary(); err == nil {
		t.Fatal("invalid accounting accepted")
	}
	f.call(false, f.creator, AssetInstruction{Type: FundInitialize, Decimals: FundCrowdfunding, Amount: 1000, Name: "Again", Symbol: "40", URI: f.beneficiary.String()}, 11)
}
