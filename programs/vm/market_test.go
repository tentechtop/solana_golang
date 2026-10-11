package vmprogram

import (
	"math"
	"reflect"
	"testing"

	"github.com/tentechtop/ordexus-core/runtime"
	"github.com/tentechtop/ordexus-core/structure"
	ovm "github.com/tentechtop/ordexus-core/vm"
)

type marketFixture struct {
	program, token, seller, buyer, state, mint, vault, source, target structure.PublicKey
	accounts                                                          map[structure.PublicKey]structure.Account
}

func newMarketFixture(t *testing.T) *marketFixture {
	t.Helper()
	f := &marketFixture{program: StablecoinAddress("market-test", "program"), token: StablecoinAddress("market-test", "token"), seller: StablecoinAddress("market-test", "seller"), buyer: StablecoinAddress("market-test", "buyer")}
	f.state = MarketStateAddress(f.program)
	f.vault = MarketVaultAddress(f.program)
	f.mint = StablecoinAddress("nulla.asset.mint.v1", f.token.String())
	f.source = StablecoinAddress("nulla.asset.balance.v1", f.token.String(), f.seller.String())
	f.target = StablecoinAddress("nulla.asset.balance.v1", f.token.String(), f.buyer.String())
	code, _ := MarketProgramData()
	token, _ := ERC20LikeContractProgramData()
	mint, _ := (AssetMintState{Version: 1, Kind: AssetKindFungible, Decimals: 6, Authority: f.seller, Supply: 1000, Name: "Local Token", Symbol: "LOCAL"}).MarshalBinary()
	balance, _ := (AssetBalanceState{Version: 1, Mint: f.mint, Owner: f.seller, Amount: 1000}).MarshalBinary()
	f.accounts = map[structure.PublicKey]structure.Account{f.program: {Owner: structure.DefaultBuiltinProgramIDs.BPFLoader, Executable: true, Lamports: 100000000, Data: code}, f.token: {Owner: structure.DefaultBuiltinProgramIDs.BPFLoader, Executable: true, Lamports: 100000000, Data: token}, f.mint: {Owner: f.token, Lamports: 100000000, Data: mint}, f.state: {Owner: f.program, Lamports: 100000000}, f.vault: {Owner: f.token, Lamports: 100000000}, f.source: {Owner: f.token, Lamports: 100000000, Data: balance}, f.target: {Owner: f.token, Lamports: 100000000}, f.seller: {Owner: structure.DefaultBuiltinProgramIDs.System, Lamports: 1000000000}, f.buyer: {Owner: structure.DefaultBuiltinProgramIDs.System, Lamports: 1000000000}}
	return f
}
func (f *marketFixture) context(i AssetInstruction, buyer bool) runtime.InstructionContext {
	actor, balance := f.seller, f.source
	if buyer {
		actor, balance = f.buyer, f.target
	}
	keys := []structure.PublicKey{actor, f.state, balance, f.vault}
	if i.Type == MarketBuy {
		keys = append(keys, f.seller)
	}
	keys = append(keys, f.mint, f.token, f.program)
	index := func(k structure.PublicKey) uint8 {
		for n, v := range keys {
			if k == v {
				return uint8(n)
			}
		}
		panic("missing key")
	}
	order := []uint8{index(actor), index(f.state), index(f.mint), index(balance), index(f.vault), index(f.token)}
	if i.Type == MarketBuy {
		order = append(order, index(f.seller))
	}
	data, _ := i.MarshalBinary()
	return runtime.InstructionContext{Instruction: structure.CompiledInstruction{ProgramIDIndex: index(f.program), AccountIndexes: order, Data: data}, Message: structure.ResolvedMessage{Header: structure.MessageHeader{NumRequiredSignatures: 1, NumReadonlyUnsignedAccounts: 3}, AccountKeys: keys, StaticAccountKeys: keys}, Accounts: f.accounts, CurrentSlot: 10, RentConfig: structure.DefaultRentConfig, BuiltinPrograms: structure.DefaultBuiltinProgramIDs}
}
func (f *marketFixture) call(t *testing.T, i AssetInstruction, buyer, success bool, mutate func(*runtime.InstructionContext)) {
	t.Helper()
	c := f.context(i, buyer)
	if mutate != nil {
		mutate(&c)
	}
	before := cloneStructureAccounts(f.accounts)
	err := NewProgram(structure.DefaultBuiltinProgramIDs.BPFLoader, ovm.Runtime{}).Execute(c)
	if (err == nil) != success {
		t.Fatalf("operation %d: %v", i.Type, err)
	}
	if !success && !reflect.DeepEqual(before, f.accounts) {
		t.Fatal("failed market instruction changed accounts")
	}
}
func TestMarketAtomicPurchaseAndCancellation(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "buy", true: "cancel"}[cancel], func(t *testing.T) {
			f := newMarketFixture(t)
			f.call(t, AssetInstruction{Type: MarketCreate, Amount: 200, URI: "10000000"}, false, true, nil)
			s, _ := UnmarshalMarketOrder(f.accounts[f.state].Data)
			if s.Locked != 200 || len(f.accounts[f.state].Data) != MarketStateBytes {
				t.Fatal("incorrect locked state/size")
			}
			if cancel {
				f.call(t, AssetInstruction{Type: MarketCancel}, false, true, nil)
			} else {
				f.call(t, AssetInstruction{Type: MarketBuy, Amount: 200, URI: "10000000"}, true, true, nil)
			}
			f.call(t, AssetInstruction{Type: MarketBuy, Amount: 200, URI: "10000000"}, true, false, nil)
			f.call(t, AssetInstruction{Type: MarketCancel}, false, false, nil)
			source, _ := UnmarshalAssetBalanceStateBinary(f.accounts[f.source].Data)
			target, _ := UnmarshalAssetBalanceStateBinary(f.accounts[f.target].Data)
			vault, _ := UnmarshalAssetBalanceStateBinary(f.accounts[f.vault].Data)
			if source.Amount+target.Amount+vault.Amount != 1000 || vault.Amount != 0 {
				t.Fatal("token conservation failed")
			}
			if cancel {
				if source.Amount != 1000 || f.accounts[f.seller].Lamports != 1000000000 {
					t.Fatal("cancel did not restore seller")
				}
			} else {
				if source.Amount != 800 || target.Amount != 200 || f.accounts[f.seller].Lamports != 1010000000 || f.accounts[f.buyer].Lamports != 990000000 {
					t.Fatal("incorrect atomic settlement")
				}
			}
		})
	}
}
func TestMarketRejectionLeavesEscrowAndPaymentUnchanged(t *testing.T) {
	for _, variant := range []string{"unsigned", "readonly buyer", "readonly vault", "insufficient ORDU", "wrong seller", "wrong price", "wrong quantity", "wrong mint", "fake token program", "wrong balance owner", "overflow", "rent failure", "cancel by buyer", "seller steals escrow"} {
		t.Run(variant, func(t *testing.T) {
			f := newMarketFixture(t)
			f.call(t, AssetInstruction{Type: MarketCreate, Amount: 200, URI: "10000000"}, false, true, nil)
			i := AssetInstruction{Type: MarketBuy, Amount: 200, URI: "10000000"}
			if variant == "wrong price" {
				i.URI = "1"
			}
			if variant == "wrong quantity" {
				i.Amount = 1
			}
			if variant == "cancel by buyer" {
				i = AssetInstruction{Type: MarketCancel}
			}
			f.call(t, i, true, false, func(c *runtime.InstructionContext) {
				switch variant {
				case "unsigned":
					c.Message.Header.NumRequiredSignatures = 0
				case "readonly buyer":
					c.Message.Header.NumReadonlySignedAccounts = 1
				case "readonly vault":
					c.Message.Header.NumReadonlyUnsignedAccounts = 5
				case "insufficient ORDU":
					a := f.accounts[f.buyer]
					a.Lamports = 1
					f.accounts[f.buyer] = a
				case "wrong seller":
					c.Instruction.AccountIndexes[6] = c.Instruction.AccountIndexes[0]
				case "wrong mint":
					c.Instruction.AccountIndexes[2] = c.Instruction.AccountIndexes[5]
				case "fake token program":
					a := f.accounts[f.token]
					a.Data, _ = StablecoinProgramData()
					f.accounts[f.token] = a
				case "wrong balance owner":
					a := f.accounts[f.target]
					a.Owner = f.program
					f.accounts[f.target] = a
				case "overflow":
					a := f.accounts[f.target]
					a.Data, _ = (AssetBalanceState{Version: 1, Mint: f.mint, Owner: f.buyer, Amount: math.MaxUint64}).MarshalBinary()
					f.accounts[f.target] = a
				case "rent failure":
					a := f.accounts[f.target]
					a.Lamports = 1
					f.accounts[f.target] = a
				case "seller steals escrow":
					a := f.accounts[f.vault]
					b, _ := UnmarshalAssetBalanceStateBinary(a.Data)
					b.Owner = f.seller
					a.Data, _ = b.MarshalBinary()
					f.accounts[f.vault] = a
				}
			})
		})
	}
}
