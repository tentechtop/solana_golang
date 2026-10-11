package vmprogram

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"reflect"
	"github.com/tentechtop/ordexus-core/runtime"
	"github.com/tentechtop/ordexus-core/structure"
	ovm "github.com/tentechtop/ordexus-core/vm"
	"testing"
)

type rewardFixture struct {
	t                                              *testing.T
	program, owner, config, mint, balance, receipt structure.PublicKey
	oracle                                         ed25519.PrivateKey
	accounts                                       map[structure.PublicKey]structure.Account
}

func newRewardFixture(t *testing.T, cap uint64) *rewardFixture {
	f := &rewardFixture{t: t, program: StablecoinAddress("reward-test", "program"), owner: StablecoinAddress("reward-test", "owner")}
	seed := sha256.Sum256([]byte("public reward test oracle"))
	f.oracle = ed25519.NewKeyFromSeed(seed[:])
	var oracle structure.PublicKey
	copy(oracle[:], f.oracle.Public().(ed25519.PublicKey))
	f.config, f.mint, f.balance, f.receipt = RewardConfigAddress(f.program), RewardMintAddress(f.program), RewardBalanceAddress(f.program, f.owner), RewardReceiptAddress(f.program, f.owner)
	code, _ := RewardProgramData()
	f.accounts = map[structure.PublicKey]structure.Account{f.program: {Owner: structure.DefaultBuiltinProgramIDs.BPFLoader, Executable: true, Lamports: 100000000, Data: code}, f.owner: {Owner: structure.DefaultBuiltinProgramIDs.System, Lamports: 1000000000}}
	for _, k := range []structure.PublicKey{f.config, f.mint, f.balance, f.receipt} {
		f.accounts[k] = structure.Account{Owner: f.program, Lamports: 100000000}
	}
	f.call(true, AssetInstruction{Type: RewardInitialize, Amount: cap, Decimals: 6, Name: "Project PI", Symbol: "PI", URI: oracle.String() + "|1000000|2000000"}, 10, nil)
	return f
}
func (f *rewardFixture) voucher(day uint64, action uint8) AssetInstruction {
	return AssetInstruction{Type: RewardClaim, Amount: day, Decimals: action, Name: "100", URI: base64.StdEncoding.EncodeToString(ed25519.Sign(f.oracle, RewardVoucherMessage(f.program, f.owner, day, action, 100)))}
}
func (f *rewardFixture) call(ok bool, i AssetInstruction, slot uint64, change func(*runtime.InstructionContext)) {
	f.t.Helper()
	keys := []structure.PublicKey{f.owner, f.config, f.mint}
	if i.Type != RewardInitialize {
		keys = append(keys, f.balance, f.receipt)
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
	c := runtime.InstructionContext{Instruction: structure.CompiledInstruction{ProgramIDIndex: uint8(len(keys) - 1), AccountIndexes: indexes, Data: wire}, Message: structure.ResolvedMessage{Header: structure.MessageHeader{NumRequiredSignatures: 1, NumReadonlyUnsignedAccounts: 1}, StaticAccountKeys: keys, AccountKeys: keys}, Accounts: f.accounts, CurrentSlot: slot, RentConfig: structure.DefaultRentConfig, BuiltinPrograms: structure.DefaultBuiltinProgramIDs}
	if change != nil {
		change(&c)
	}
	before := cloneStructureAccounts(f.accounts)
	err = NewProgram(structure.DefaultBuiltinProgramIDs.BPFLoader, ovm.Runtime{}).Execute(c)
	if (err == nil) != ok {
		f.t.Fatalf("op %d: %v", i.Type, err)
	}
	if !ok && !reflect.DeepEqual(before, f.accounts) {
		f.t.Fatal("rejection changed state")
	}
}
func TestRewardClaimsAreDailyIndependentAndCapped(t *testing.T) {
	f := newRewardFixture(t, 4000000)
	f.call(true, f.voucher(20000, 1), 11, nil)
	f.call(false, f.voucher(20000, 1), 12, nil)
	f.call(true, f.voucher(20000, 2), 13, nil)
	f.call(false, f.voucher(20000, 2), 14, nil)
	f.call(true, f.voucher(20001, 1), 15, nil)
	f.call(false, f.voucher(20000, 2), 16, nil)
	f.call(false, f.voucher(20001, 2), 17, nil)
	b, _ := UnmarshalAssetBalanceStateBinary(f.accounts[f.balance].Data)
	s, _ := UnmarshalRewardConfig(f.accounts[f.config].Data)
	m, _ := UnmarshalAssetMintStateBinary(f.accounts[f.mint].Data)
	if b.Amount != 4000000 || m.Supply != 4000000 || s.TotalClaimed != 4000000 {
		t.Fatal("claims did not mint actual balance")
	}
}
func TestRewardRejectsForgedExpiredUnsignedAndAliasedClaims(t *testing.T) {
	for _, mode := range []string{"signature", "day", "action", "expired", "unsigned", "readonly", "alias", "foreign owner", "other template", "mint bypass"} {
		t.Run(mode, func(t *testing.T) {
			f := newRewardFixture(t, 10000000)
			i := f.voucher(20000, 1)
			slot := uint64(11)
			var change func(*runtime.InstructionContext)
			switch mode {
			case "signature":
				i.URI = base64.StdEncoding.EncodeToString(make([]byte, 64))
			case "day":
				i.Amount++
			case "action":
				i.Decimals = 2
			case "expired":
				slot = 101
			case "unsigned":
				change = func(c *runtime.InstructionContext) { c.Message.Header.NumRequiredSignatures = 0 }
			case "readonly":
				change = func(c *runtime.InstructionContext) { c.Message.Header.NumReadonlyUnsignedAccounts = 5 }
			case "alias":
				change = func(c *runtime.InstructionContext) { c.Instruction.AccountIndexes[4] = 3 }
			case "foreign owner":
				a := f.accounts[f.receipt]
				a.Owner = f.owner
				f.accounts[f.receipt] = a
			case "other template":
				a := f.accounts[f.program]
				a.Data, _ = ERC20LikeContractProgramData()
				f.accounts[f.program] = a
			case "mint bypass":
				i = AssetInstruction{Type: AssetInstructionMintTo, Amount: 1}
			}
			f.call(false, i, slot, change)
		})
	}
}
func TestRewardStateRoundTripAndStrictEOF(t *testing.T) {
	f := newRewardFixture(t, 10000000)
	f.call(true, f.voucher(20000, 1), 11, nil)
	if len(f.accounts[f.config].Data) != 131 || len(f.accounts[f.receipt].Data) != 76 {
		t.Fatal("unexpected rent size")
	}
	if _, e := UnmarshalRewardConfig(append(f.accounts[f.config].Data, 0)); e == nil {
		t.Fatal("config trailing data accepted")
	}
	if _, e := UnmarshalRewardReceipt(append(f.accounts[f.receipt].Data, 0)); e == nil {
		t.Fatal("receipt trailing data accepted")
	}
}
