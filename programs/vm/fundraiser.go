package vmprogram

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/tentechtop/ordexus-core/runtime"
	"github.com/tentechtop/ordexus-core/structure"
	ovm "github.com/tentechtop/ordexus-core/vm"
)

// New instruction range and immutable template; existing asset/stablecoin ABI is unchanged.
const (
	FundInitialize AssetInstructionType = 200 + iota
	FundContribute
	FundWithdraw
	FundRefund
	FundCancel
)

const (
	FundDonation     = 1
	FundCrowdfunding = 2
	FundStateKind    = 32
	FundReceiptKind  = 33
)

type FundraiserState struct {
	Mode                                              uint8
	Creator, Beneficiary                              structure.PublicKey
	Target, DeadlineSlot, Raised, Withdrawn, Refunded uint64
	Cancelled                                         bool
	Name                                              string
}

type FundraiserReceipt struct {
	Campaign, Contributor structure.PublicKey
	Contributed, Refunded uint64
}

func FundraiserProgramData() ([]byte, error) {
	tag, err := ovm.BuildRegisterInstruction(ovm.RegOpMovImm, 9, 0, 0, 0x46554e31)
	if err != nil {
		return nil, err
	}
	call, err := ovm.BuildRegisterInstruction(ovm.RegOpSyscall, 0, 0, 0, uint64(ovm.SyscallAssetExecute))
	if err != nil {
		return nil, err
	}
	code, err := ovm.BuildRegisterProgramCode(tag, call)
	if err != nil {
		return nil, err
	}
	return ovm.EncodeGovernedRegisterBytecode(code, nil, ovm.ProgramManifest{ComputeUnitLimit: ovm.DefaultComputeUnitLimit, RequiredSyscalls: []ovm.SyscallID{ovm.SyscallAssetExecute}})
}

func IsFundraiserProgram(data []byte) bool {
	expected, err := FundraiserProgramData()
	return err == nil && ovm.BytecodeProgramsEqual(data, expected)
}

func FundraiserAddress(program structure.PublicKey) structure.PublicKey {
	return StablecoinAddress("nulla.fundraiser.state.v1", program.String())
}

func FundraiserReceiptAddress(program, contributor structure.PublicKey) structure.PublicKey {
	return StablecoinAddress("nulla.fundraiser.receipt.v1", program.String(), contributor.String())
}

func validateFundraiserInstruction(i AssetInstruction) error {
	switch i.Type {
	case FundInitialize:
		if !utf8.ValidString(i.Name) || strings.TrimSpace(i.Name) == "" || len(i.Name) > 64 {
			return fmt.Errorf("fundraiser: name must contain 1-64 UTF-8 bytes")
		}
		beneficiary, err := structure.PublicKeyFromBase58(i.URI)
		if err != nil || beneficiary.IsZero() {
			return fmt.Errorf("fundraiser: invalid beneficiary")
		}
		deadline, err := strconv.ParseUint(i.Symbol, 10, 64)
		if err != nil {
			return fmt.Errorf("fundraiser: invalid deadline slot")
		}
		if i.Decimals == FundDonation {
			if i.Amount != 0 || deadline != 0 {
				return fmt.Errorf("fundraiser: donation has no target/deadline")
			}
		} else if i.Decimals != FundCrowdfunding || i.Amount == 0 || deadline == 0 {
			return fmt.Errorf("fundraiser: invalid crowdfunding target/deadline")
		}
	case FundContribute:
		if i.Amount == 0 {
			return fmt.Errorf("fundraiser: contribution must be positive")
		}
	case FundWithdraw, FundRefund, FundCancel:
		if i.Amount != 0 {
			return fmt.Errorf("fundraiser: operation does not accept an amount")
		}
	default:
		return fmt.Errorf("fundraiser: unknown operation")
	}
	if i.Type != FundInitialize && (i.Name != "" || i.Symbol != "" || i.URI != "" || i.Decimals != 0) {
		return fmt.Errorf("fundraiser: unexpected operation metadata")
	}
	return nil
}

func (s FundraiserState) MarshalBinary() ([]byte, error) {
	if s.Creator.IsZero() || s.Beneficiary.IsZero() || !utf8.ValidString(s.Name) || strings.TrimSpace(s.Name) == "" || len(s.Name) > 64 || s.Withdrawn > s.Raised || s.Refunded > s.Raised-s.Withdrawn {
		return nil, fmt.Errorf("fundraiser: invalid state/accounting")
	}
	if s.Mode == FundDonation {
		if s.Target != 0 || s.DeadlineSlot != 0 || s.Refunded != 0 || s.Withdrawn != s.Raised {
			return nil, fmt.Errorf("fundraiser: invalid donation state")
		}
	} else if s.Mode != FundCrowdfunding || s.Target == 0 || s.DeadlineSlot == 0 || (s.Withdrawn != 0 && (s.Withdrawn != s.Raised || s.Raised < s.Target || s.Cancelled)) {
		return nil, fmt.Errorf("fundraiser: invalid crowdfunding state")
	}
	w := stableWriter(FundStateKind)
	w.WriteUint8(s.Mode)
	w.WriteFixedBytes(s.Creator[:])
	w.WriteFixedBytes(s.Beneficiary[:])
	w.WriteUint64(s.Target)
	w.WriteUint64(s.DeadlineSlot)
	w.WriteUint64(s.Raised)
	w.WriteUint64(s.Withdrawn)
	w.WriteUint64(s.Refunded)
	if s.Cancelled {
		w.WriteUint8(1)
	} else {
		w.WriteUint8(0)
	}
	if err := w.WriteString(s.Name); err != nil {
		return nil, err
	}
	return w.Bytes(), nil
}

func UnmarshalFundraiserState(data []byte) (FundraiserState, error) {
	r := newStableReader(data, FundStateKind)
	s := FundraiserState{Mode: r.u8(), Creator: r.key(), Beneficiary: r.key(), Target: r.u64(), DeadlineSlot: r.u64(), Raised: r.u64(), Withdrawn: r.u64(), Refunded: r.u64()}
	flag := r.u8()
	s.Cancelled = flag == 1
	s.Name = r.str()
	if err := r.done(); err != nil {
		return s, err
	}
	if flag > 1 {
		return s, fmt.Errorf("fundraiser: invalid cancellation flag")
	}
	_, err := s.MarshalBinary()
	return s, err
}

func (r FundraiserReceipt) MarshalBinary() ([]byte, error) {
	if r.Campaign.IsZero() || r.Contributor.IsZero() || r.Contributed == 0 || r.Refunded > r.Contributed {
		return nil, fmt.Errorf("fundraiser: invalid receipt")
	}
	w := stableWriter(FundReceiptKind)
	w.WriteFixedBytes(r.Campaign[:])
	w.WriteFixedBytes(r.Contributor[:])
	w.WriteUint64(r.Contributed)
	w.WriteUint64(r.Refunded)
	return w.Bytes(), nil
}

func UnmarshalFundraiserReceipt(data []byte) (FundraiserReceipt, error) {
	r := newStableReader(data, FundReceiptKind)
	s := FundraiserReceipt{Campaign: r.key(), Contributor: r.key(), Contributed: r.u64(), Refunded: r.u64()}
	if err := r.done(); err != nil {
		return s, err
	}
	_, err := s.MarshalBinary()
	return s, err
}

func executeFundraiserInstruction(i AssetInstruction, c runtime.InstructionContext, accounts map[structure.PublicKey]structure.Account) error {
	if err := validateFundraiserInstruction(i); err != nil {
		return err
	}
	program, err := assetVMProgramID(c)
	if err != nil {
		return err
	}
	if !IsFundraiserProgram(accounts[program].Data) {
		return fmt.Errorf("fundraiser: immutable fundraising template required")
	}
	expectedCount := 2
	if i.Type == FundContribute || i.Type == FundRefund {
		expectedCount = 3
	}
	if len(c.Instruction.AccountIndexes) != expectedCount {
		return fmt.Errorf("fundraiser: incorrect account count")
	}
	keys := make([]structure.PublicKey, expectedCount)
	for index := range keys {
		keys[index], err = assetAccountAddressAt(c, index)
		if err != nil {
			return err
		}
		for j := 0; j < index; j++ {
			if keys[j] == keys[index] {
				return fmt.Errorf("fundraiser: duplicate account")
			}
		}
		if err := requireAssetWritableAddress(c, keys[index], "fundraising"); err != nil {
			return err
		}
	}
	actor, stateKey := keys[0], keys[1]
	if !runtime.IsSignerContextAddress(actor, c) {
		return fmt.Errorf("fundraiser: actor must sign")
	}
	if stateKey != FundraiserAddress(program) {
		return fmt.Errorf("fundraiser: noncanonical campaign account")
	}
	stateAccount, err := assetOwnedWritableAccount("campaign", stateKey, c, accounts)
	if err != nil {
		return err
	}
	writeState := func(s FundraiserState) error {
		return writeAssetAccount(stateKey, accounts[stateKey], s.MarshalBinary, c, accounts)
	}
	if i.Type == FundInitialize {
		if len(stateAccount.Data) != 0 {
			return fmt.Errorf("fundraiser: already initialized")
		}
		beneficiary, _ := structure.PublicKeyFromBase58(i.URI)
		if beneficiary == stateKey || beneficiary == program {
			return fmt.Errorf("fundraiser: invalid beneficiary account")
		}
		deadline, _ := strconv.ParseUint(i.Symbol, 10, 64)
		if i.Decimals == FundCrowdfunding && deadline <= c.CurrentSlot {
			return fmt.Errorf("fundraiser: deadline must be in the future")
		}
		return writeState(FundraiserState{Mode: i.Decimals, Creator: actor, Beneficiary: beneficiary, Target: i.Amount, DeadlineSlot: deadline, Name: i.Name})
	}
	s, err := UnmarshalFundraiserState(stateAccount.Data)
	if err != nil {
		return err
	}
	transfer := func(from, to structure.PublicKey, amount uint64, signed bool) error {
		source, ok := accounts[from]
		if !ok {
			return fmt.Errorf("fundraiser: source missing")
		}
		target, ok := accounts[to]
		if !ok {
			return fmt.Errorf("fundraiser: destination missing")
		}
		if source.Executable || target.Executable {
			return fmt.Errorf("fundraiser: executable payment account")
		}
		if signed {
			if !runtime.IsSignerContextAddress(from, c) || source.Owner != c.BuiltinPrograms.System || len(source.Data) != 0 {
				return fmt.Errorf("fundraiser: contributor must be a signed system wallet")
			}
		} else if source.Owner != program {
			return fmt.Errorf("fundraiser: escrow owner mismatch")
		}
		if source.Lamports < amount || target.Lamports > math.MaxUint64-amount {
			return fmt.Errorf("fundraiser: insufficient balance or overflow")
		}
		source.Lamports -= amount
		target.Lamports += amount
		if err := source.ValidateWithRent(c.RentConfig); err != nil {
			return err
		}
		if err := target.ValidateWithRent(c.RentConfig); err != nil {
			return err
		}
		accounts[from] = source
		accounts[to] = target
		return nil
	}
	loadReceipt := func() (FundraiserReceipt, structure.Account, error) {
		if keys[2] != FundraiserReceiptAddress(program, actor) {
			return FundraiserReceipt{}, structure.Account{}, fmt.Errorf("fundraiser: noncanonical contributor receipt")
		}
		a, e := assetOwnedWritableAccount("receipt", keys[2], c, accounts)
		if e != nil {
			return FundraiserReceipt{}, a, e
		}
		if len(a.Data) == 0 {
			return FundraiserReceipt{Campaign: stateKey, Contributor: actor}, a, nil
		}
		r, e := UnmarshalFundraiserReceipt(a.Data)
		if e == nil && (r.Campaign != stateKey || r.Contributor != actor) {
			e = fmt.Errorf("fundraiser: receipt authority mismatch")
		}
		return r, a, e
	}
	switch i.Type {
	case FundContribute:
		if s.Cancelled || (s.Mode == FundCrowdfunding && c.CurrentSlot >= s.DeadlineSlot) {
			return fmt.Errorf("fundraiser: contributions closed")
		}
		if s.Raised > math.MaxUint64-i.Amount {
			return fmt.Errorf("fundraiser: total overflow")
		}
		if s.Mode == FundDonation {
			if keys[2] != s.Beneficiary || accounts[keys[2]].Owner != c.BuiltinPrograms.System {
				return fmt.Errorf("fundraiser: wrong beneficiary")
			}
			if err := transfer(actor, s.Beneficiary, i.Amount, true); err != nil {
				return err
			}
			s.Withdrawn += i.Amount
		} else {
			r, a, err := loadReceipt()
			if err != nil {
				return err
			}
			if r.Contributed > math.MaxUint64-i.Amount {
				return fmt.Errorf("fundraiser: receipt overflow")
			}
			if err := transfer(actor, stateKey, i.Amount, true); err != nil {
				return err
			}
			r.Contributed += i.Amount
			if err := writeAssetAccount(keys[2], a, r.MarshalBinary, c, accounts); err != nil {
				return err
			}
		}
		s.Raised += i.Amount
	case FundWithdraw:
		if s.Mode != FundCrowdfunding || actor != s.Beneficiary || s.Cancelled || c.CurrentSlot < s.DeadlineSlot || s.Raised < s.Target || s.Withdrawn != 0 || s.Refunded != 0 {
			return fmt.Errorf("fundraiser: payout is not available")
		}
		if err := transfer(stateKey, actor, s.Raised, false); err != nil {
			return err
		}
		s.Withdrawn = s.Raised
	case FundRefund:
		if s.Mode != FundCrowdfunding || s.Withdrawn != 0 || (!s.Cancelled && (c.CurrentSlot < s.DeadlineSlot || s.Raised >= s.Target)) {
			return fmt.Errorf("fundraiser: refund is not available")
		}
		r, a, err := loadReceipt()
		if err != nil {
			return err
		}
		amount := r.Contributed - r.Refunded
		if amount == 0 || amount > s.Raised-s.Refunded {
			return fmt.Errorf("fundraiser: nothing to refund")
		}
		if err := transfer(stateKey, actor, amount, false); err != nil {
			return err
		}
		r.Refunded += amount
		s.Refunded += amount
		if err := writeAssetAccount(keys[2], a, r.MarshalBinary, c, accounts); err != nil {
			return err
		}
	case FundCancel:
		if actor != s.Creator || s.Cancelled || s.Withdrawn > 0 && s.Mode == FundCrowdfunding {
			return fmt.Errorf("fundraiser: cancellation is not available")
		}
		if s.Mode == FundCrowdfunding && c.CurrentSlot >= s.DeadlineSlot && s.Raised >= s.Target {
			return fmt.Errorf("fundraiser: successful campaign cannot be cancelled")
		}
		s.Cancelled = true
	}
	return writeState(s)
}
