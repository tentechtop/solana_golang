package vmprogram

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"

	"github.com/tentechtop/ordexus-core/runtime"
	"github.com/tentechtop/ordexus-core/structure"
	ovm "github.com/tentechtop/ordexus-core/vm"
)

// V1 is an immutable, fully collateralized, whole-lot token/ORDU sell order.
// It deliberately accepts only the ordinary immutable token template, never
// stablecoins, restricted RWA, reward programs, or arbitrary executable code.
const (
	MarketCreate AssetInstructionType = 500 + iota
	MarketBuy
	MarketCancel
	MarketStateKind  = 80
	MarketStateBytes = 172
	MarketOpen       = 1
	MarketFilled     = 2
	MarketCancelled  = 3
)

type MarketOrder struct {
	Seller, Buyer, TokenProgram, Mint                      structure.PublicKey
	Amount, TotalLamports, CreatedSlot, ClosedSlot, Locked uint64
	Status                                                 uint8
}

func MarketProgramData() ([]byte, error) {
	tag, err := ovm.BuildRegisterInstruction(ovm.RegOpMovImm, 9, 0, 0, 0x4d4b5431)
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

func IsMarketProgram(data []byte) bool {
	code, err := MarketProgramData()
	return err == nil && ovm.BytecodeProgramsEqual(code, data)
}
func MarketStateAddress(program structure.PublicKey) structure.PublicKey {
	return StablecoinAddress("nulla.market.state.v1", program.String())
}
func MarketVaultAddress(program structure.PublicKey) structure.PublicKey {
	return StablecoinAddress("nulla.market.vault.v1", program.String())
}
func MarketVaultAuthority(program structure.PublicKey) structure.PublicKey {
	address, _ := ovm.CreateProgramAddress(vmAddressFromPublicKey(program), [][]byte{[]byte("market-vault-v1")})
	return publicKeyFromVMAddress(address)
}

// Read-only icon metadata is permitted; executable code and the entire manifest
// must match the immutable ordinary-token template. The legacy SVM1 and current
// OVM1 envelopes are equivalent only after both checksums validate.
func IsMarketTokenProgram(a structure.Account) bool {
	if !a.Executable || a.Owner != structure.DefaultBuiltinProgramIDs.BPFLoader {
		return false
	}
	expected, err := ERC20LikeContractProgramData()
	if err != nil {
		return false
	}
	loader := ovm.BytecodeLoader{}
	p, err := loader.Load(ovm.ProgramAccount{Owner: vmAddressFromPublicKey(a.Owner), Executable: true, Data: a.Data}, vmAddressFromPublicKey(a.Owner))
	if err != nil || p.Manifest == nil {
		return false
	}
	encoded, err := ovm.EncodeGovernedRegisterBytecode(p.Code, p.ReadOnlyData, *p.Manifest)
	if err != nil || !ovm.BytecodeProgramsEqual(encoded, a.Data) {
		return false
	}
	canonical, err := ovm.EncodeGovernedRegisterBytecode(p.Code, nil, *p.Manifest)
	if err != nil || !ovm.BytecodeProgramsEqual(canonical, expected) {
		return false
	}
	if len(p.ReadOnlyData) == 0 {
		return true
	}
	d := p.ReadOnlyData
	return len(d) >= 8 && len(d) <= 17200 && string(d[:4]) == "NIC1" && uint64(binary.LittleEndian.Uint32(d[4:8])) == uint64(len(d)-8)
}

func validateMarketInstruction(i AssetInstruction) error {
	if i.Decimals != 0 || i.Name != "" || i.Symbol != "" {
		return fmt.Errorf("market: unexpected instruction fields")
	}
	switch i.Type {
	case MarketCreate, MarketBuy:
		total, err := strconv.ParseUint(i.URI, 10, 64)
		if err != nil || total == 0 || strconv.FormatUint(total, 10) != i.URI || i.Amount == 0 {
			return fmt.Errorf("market: positive amount and canonical total required")
		}
	case MarketCancel:
		if i.Amount != 0 || i.URI != "" {
			return fmt.Errorf("market: cancel takes no price or quantity")
		}
	default:
		return fmt.Errorf("market: invalid operation")
	}
	return nil
}

func (s MarketOrder) MarshalBinary() ([]byte, error) {
	if s.Seller.IsZero() || s.TokenProgram.IsZero() || s.Mint.IsZero() || s.Amount == 0 || s.TotalLamports == 0 {
		return nil, fmt.Errorf("market: invalid order")
	}
	switch s.Status {
	case MarketOpen:
		if s.Locked != s.Amount || !s.Buyer.IsZero() || s.ClosedSlot != 0 {
			return nil, fmt.Errorf("market: invalid open order")
		}
	case MarketFilled:
		if s.Locked != 0 || s.Buyer.IsZero() || s.Buyer == s.Seller || s.ClosedSlot < s.CreatedSlot {
			return nil, fmt.Errorf("market: invalid filled order")
		}
	case MarketCancelled:
		if s.Locked != 0 || !s.Buyer.IsZero() || s.ClosedSlot < s.CreatedSlot {
			return nil, fmt.Errorf("market: invalid cancelled order")
		}
	default:
		return nil, fmt.Errorf("market: invalid status")
	}
	w := stableWriter(MarketStateKind)
	w.WriteFixedBytes(s.Seller[:])
	w.WriteFixedBytes(s.Buyer[:])
	w.WriteFixedBytes(s.TokenProgram[:])
	w.WriteFixedBytes(s.Mint[:])
	w.WriteUint64(s.Amount)
	w.WriteUint64(s.TotalLamports)
	w.WriteUint64(s.CreatedSlot)
	w.WriteUint64(s.ClosedSlot)
	w.WriteUint64(s.Locked)
	w.WriteUint8(s.Status)
	return w.Bytes(), nil
}

func UnmarshalMarketOrder(data []byte) (MarketOrder, error) {
	r := newStableReader(data, MarketStateKind)
	s := MarketOrder{Seller: r.key(), Buyer: r.key(), TokenProgram: r.key(), Mint: r.key(), Amount: r.u64(), TotalLamports: r.u64(), CreatedSlot: r.u64(), ClosedSlot: r.u64(), Locked: r.u64(), Status: r.u8()}
	if err := r.done(); err != nil {
		return s, err
	}
	_, err := s.MarshalBinary()
	return s, err
}

func executeMarketInstruction(i AssetInstruction, c runtime.InstructionContext, accounts map[structure.PublicKey]structure.Account) error {
	if err := validateMarketInstruction(i); err != nil {
		return err
	}
	program, err := assetVMProgramID(c)
	if err != nil || !IsMarketProgram(accounts[program].Data) {
		return fmt.Errorf("market: immutable template required")
	}
	count := 6
	if i.Type == MarketBuy {
		count = 7
	}
	if len(c.Instruction.AccountIndexes) != count {
		return fmt.Errorf("market: incorrect account count")
	}
	k := make([]structure.PublicKey, count)
	for n := range k {
		k[n], err = assetAccountAddressAt(c, n)
		if err != nil {
			return err
		}
		if k[n] == program {
			return fmt.Errorf("market: order program cannot be a settlement account")
		}
		for j := 0; j < n; j++ {
			if k[n] == k[j] {
				return fmt.Errorf("market: duplicate account")
			}
		}
		if n != 2 && n != 5 {
			if err := requireAssetWritableAddress(c, k[n], "market"); err != nil {
				return err
			}
		}
	}
	actor, stateKey, mintKey, balanceKey, vaultKey, tokenProgram := k[0], k[1], k[2], k[3], k[4], k[5]
	if !runtime.IsSignerContextAddress(actor, c) || accounts[actor].Owner != c.BuiltinPrograms.System || accounts[actor].Executable || len(accounts[actor].Data) != 0 {
		return fmt.Errorf("market: signed system wallet required")
	}
	if stateKey != MarketStateAddress(program) || vaultKey != MarketVaultAddress(program) {
		return fmt.Errorf("market: noncanonical order accounts")
	}
	if !IsMarketTokenProgram(accounts[tokenProgram]) {
		return fmt.Errorf("market: unsupported token program")
	}
	ma := accounts[mintKey]
	mint, err := UnmarshalAssetMintStateBinary(ma.Data)
	if err != nil || ma.Owner != tokenProgram || ma.Executable || mint.Kind != AssetKindFungible || mint.Decimals > 9 || mintKey != StablecoinAddress("nulla.asset.mint.v1", tokenProgram.String()) {
		return fmt.Errorf("market: invalid ordinary token mint")
	}
	if balanceKey != StablecoinAddress("nulla.asset.balance.v1", tokenProgram.String(), actor.String()) {
		return fmt.Errorf("market: noncanonical wallet balance")
	}
	sa, err := assetOwnedWritableAccount("order", stateKey, c, accounts)
	if err != nil {
		return err
	}
	ba, va := accounts[balanceKey], accounts[vaultKey]
	if ba.Owner != tokenProgram || va.Owner != tokenProgram || ba.Executable || va.Executable {
		return fmt.Errorf("market: token account ownership mismatch")
	}
	balance := AssetBalanceState{Version: assetStateVersion, Mint: mintKey, Owner: actor}
	if len(ba.Data) > 0 {
		balance, err = UnmarshalAssetBalanceStateBinary(ba.Data)
		if err != nil {
			return err
		}
	} else if i.Type != MarketBuy {
		return fmt.Errorf("market: seller balance missing")
	}
	if balance.Owner != actor || balance.Mint != mintKey {
		return fmt.Errorf("market: wallet balance binding mismatch")
	}
	vault := AssetBalanceState{Version: assetStateVersion, Mint: mintKey, Owner: MarketVaultAuthority(program)}
	var s MarketOrder
	if i.Type == MarketCreate {
		if len(sa.Data) != 0 || len(va.Data) != 0 {
			return fmt.Errorf("market: order already initialized")
		}
		if balance.Amount < i.Amount {
			return fmt.Errorf("market: insufficient tokens")
		}
		total, _ := strconv.ParseUint(i.URI, 10, 64)
		s = MarketOrder{Seller: actor, TokenProgram: tokenProgram, Mint: mintKey, Amount: i.Amount, TotalLamports: total, CreatedSlot: c.CurrentSlot, Locked: i.Amount, Status: MarketOpen}
		balance.Amount -= i.Amount
		vault.Amount = i.Amount
	} else {
		s, err = UnmarshalMarketOrder(sa.Data)
		if err != nil {
			return err
		}
		if s.Status != MarketOpen || s.Mint != mintKey || s.TokenProgram != tokenProgram {
			return fmt.Errorf("market: order closed or asset mismatch")
		}
		vault, err = UnmarshalAssetBalanceStateBinary(va.Data)
		if err != nil || vault.Owner != MarketVaultAuthority(program) || vault.Mint != mintKey || vault.Amount < s.Locked {
			return fmt.Errorf("market: invalid escrow")
		}
		if balance.Amount > math.MaxUint64-s.Amount {
			return fmt.Errorf("market: token balance overflow")
		}
		if i.Type == MarketBuy {
			total, _ := strconv.ParseUint(i.URI, 10, 64)
			if actor == s.Seller || k[6] != s.Seller || i.Amount != s.Amount || total != s.TotalLamports {
				return fmt.Errorf("market: seller, quantity or price mismatch")
			}
			source, target := accounts[actor], accounts[s.Seller]
			if target.Owner != c.BuiltinPrograms.System || target.Executable || len(target.Data) != 0 {
				return fmt.Errorf("market: invalid seller payment account")
			}
			if source.Lamports < total || target.Lamports > math.MaxUint64-total {
				return fmt.Errorf("market: insufficient ORDU or overflow")
			}
			source.Lamports -= total
			target.Lamports += total
			if err := source.ValidateWithRent(c.RentConfig); err != nil {
				return err
			}
			if err := target.ValidateWithRent(c.RentConfig); err != nil {
				return err
			}
			accounts[actor], accounts[s.Seller] = source, target
			s.Status, s.Buyer = MarketFilled, actor
		} else {
			if actor != s.Seller {
				return fmt.Errorf("market: only seller can cancel")
			}
			s.Status = MarketCancelled
		}
		balance.Amount += s.Amount
		vault.Amount -= s.Amount
		s.Locked = 0
		s.ClosedSlot = c.CurrentSlot
	}
	if err := writeAssetAccount(balanceKey, ba, balance.MarshalBinary, c, accounts); err != nil {
		return err
	}
	if err := writeAssetAccount(vaultKey, va, vault.MarshalBinary, c, accounts); err != nil {
		return err
	}
	return writeAssetAccount(stateKey, sa, s.MarshalBinary, c, accounts)
}
