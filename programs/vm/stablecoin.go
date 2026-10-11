package vmprogram

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/tentechtop/ordexus-core/codec/borsh"
	"github.com/tentechtop/ordexus-core/runtime"
	"github.com/tentechtop/ordexus-core/structure"
	ovm "github.com/tentechtop/ordexus-core/vm"
)

// Stablecoin instructions use the existing binary envelope, with a separate,
// immutable program template and account kinds. Legacy asset state is unchanged.
const (
	StableInitialize AssetInstructionType = 100 + iota
	StableOpenAccount
	StableIssue
	StableTransfer
	StableRequestRedemption
	StableSettleRedemption
	StableCancelRedemption
	StableClaimRedemption
	StablePause
	StableProposeAuthority
	StableAcceptAuthority
	StableSetLimit
	StableBurn
	StableRefundRedemption
)

const (
	StableMintKind        = 16
	StableBalanceKind     = 17
	StableOrderKind       = 18
	StableOrderIssue      = 1
	StableOrderRedemption = 2
	StableOrderPending    = 1
	StableOrderProcessing = 2
	StableOrderSettled    = 3
	StableOrderCancelled  = 4
)

var stableSymbolPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,11}$`)
var stableCurrencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

type StablecoinMint struct {
	Decimals          uint8
	Creator           structure.PublicKey
	Authority         structure.PublicKey
	PendingAuthority  structure.PublicKey
	Supply            uint64
	Reserve           uint64 // Issuer-declared backing, not an independently verified bank balance.
	PendingRedemption uint64
	Limit             uint64
	Paused            bool
	Name              string
	Symbol            string
	Currency          string
}

type StablecoinBalance struct {
	Mint   structure.PublicKey
	Owner  structure.PublicKey
	Amount uint64
}

type StablecoinOrder struct {
	Mint      structure.PublicKey
	Owner     structure.PublicKey
	Kind      uint8
	Status    uint8
	Amount    uint64
	Reference string // SHA-256 of an off-chain reference; no bank details on chain.
	Receipt   string
}

func StablecoinProgramData() ([]byte, error) {
	tag, err := ovm.BuildRegisterInstruction(ovm.RegOpMovImm, 9, 0, 0, 0x53544231)
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
	return ovm.EncodeGovernedRegisterBytecode(code, nil, ovm.ProgramManifest{
		ComputeUnitLimit: ovm.DefaultComputeUnitLimit,
		RequiredSyscalls: []ovm.SyscallID{ovm.SyscallAssetExecute},
	})
}

func IsStablecoinProgram(data []byte) bool {
	expected, err := StablecoinProgramData()
	return err == nil && ovm.BytecodeProgramsEqual(expected, data)
}

// Publicly derivable keys authorize only account allocation. Ownership checks,
// canonical address binding and actual wallet signatures authorize state writes.
func StablecoinAddress(domain string, parts ...string) structure.PublicKey {
	seed := sha256.Sum256([]byte(domain + "|" + strings.Join(parts, "|")))
	private := ed25519.NewKeyFromSeed(seed[:])
	var key structure.PublicKey
	copy(key[:], private[32:])
	return key
}

func StablecoinMintAddress(program, creator structure.PublicKey, symbol string) structure.PublicKey {
	return StablecoinAddress("nulla.stable.mint.v1", program.String(), creator.String(), symbol)
}

func StablecoinBalanceAddress(program, mint, owner structure.PublicKey) structure.PublicKey {
	return StablecoinAddress("nulla.stable.balance.v1", program.String(), mint.String(), owner.String())
}

func StablecoinOrderAddress(program, mint structure.PublicKey, kind uint8, owner structure.PublicKey, reference string) structure.PublicKey {
	if kind == StableOrderIssue {
		return StablecoinAddress("nulla.stable.issue.v1", program.String(), mint.String(), reference)
	}
	return StablecoinAddress("nulla.stable.redeem.v1", program.String(), mint.String(), owner.String(), reference)
}

func validateStablecoinInstruction(i AssetInstruction) error {
	if i.Type < StableInitialize || i.Type > StableRefundRedemption {
		return fmt.Errorf("stablecoin: unsupported instruction")
	}
	if i.Type == StableInitialize {
		if i.Amount == 0 || i.Decimals > 9 || !utf8.ValidString(i.Name) || strings.TrimSpace(i.Name) == "" || !stableSymbolPattern.MatchString(i.Symbol) || !stableCurrencyPattern.MatchString(i.URI) {
			return fmt.Errorf("stablecoin: invalid name, symbol, currency, decimals or issuance limit")
		}
		return nil
	}
	if i.Decimals != 0 || i.Name != "" || i.Symbol != "" {
		return fmt.Errorf("stablecoin: unexpected metadata")
	}
	switch i.Type {
	case StableOpenAccount:
		owner, err := structure.PublicKeyFromBase58(i.URI)
		if i.Amount != 0 || err != nil || owner.IsZero() {
			return fmt.Errorf("stablecoin: account owner public key required")
		}
	case StableIssue, StableRequestRedemption:
		if i.Amount == 0 || !stableReference(i.URI) {
			return fmt.Errorf("stablecoin: positive amount and 32-byte reference hash required")
		}
	case StableSettleRedemption, StableRefundRedemption:
		if i.Amount != 0 || !stableReference(i.URI) {
			return fmt.Errorf("stablecoin: payout receipt hash required")
		}
	case StableTransfer, StableSetLimit, StableBurn:
		if i.Amount == 0 || i.URI != "" {
			return fmt.Errorf("stablecoin: positive amount required")
		}
	case StablePause:
		if i.Amount > 1 || i.URI != "" {
			return fmt.Errorf("stablecoin: pause must be 0 or 1")
		}
	default:
		if i.Amount != 0 || i.URI != "" {
			return fmt.Errorf("stablecoin: unexpected amount or reference")
		}
	}
	return nil
}

func stableReference(s string) bool {
	decoded, err := hex.DecodeString(s)
	return err == nil && len(decoded) == 32 && strings.ToLower(s) == s && s != strings.Repeat("0", 64)
}

func (s StablecoinMint) Validate() error {
	if s.Creator.IsZero() || s.Authority.IsZero() || s.Decimals > 9 || s.Limit == 0 || len(s.Name) > maxAssetNameBytes || !utf8.ValidString(s.Name) || strings.TrimSpace(s.Name) == "" || !stableSymbolPattern.MatchString(s.Symbol) || !stableCurrencyPattern.MatchString(s.Currency) {
		return fmt.Errorf("stablecoin: invalid mint metadata")
	}
	if s.Supply > math.MaxUint64-s.PendingRedemption || s.Supply+s.PendingRedemption > s.Reserve || s.Supply+s.PendingRedemption > s.Limit {
		return fmt.Errorf("stablecoin: backing or issuance limit invariant violated")
	}
	return nil
}

func stableWriter(kind uint8) *borsh.Writer {
	w := borsh.NewWriter(MaxAssetStateBytes)
	w.WriteUint8(kind)
	w.WriteUint16(1)
	return w
}

func (s StablecoinMint) MarshalBinary() ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	w := stableWriter(StableMintKind)
	w.WriteUint8(s.Decimals)
	for _, key := range []structure.PublicKey{s.Creator, s.Authority, s.PendingAuthority} {
		w.WriteFixedBytes(key[:])
	}
	for _, n := range []uint64{s.Supply, s.Reserve, s.PendingRedemption, s.Limit} {
		w.WriteUint64(n)
	}
	if s.Paused {
		w.WriteUint8(1)
	} else {
		w.WriteUint8(0)
	}
	for _, value := range []string{s.Name, s.Symbol, s.Currency} {
		if err := w.WriteString(value); err != nil {
			return nil, err
		}
	}
	return w.Bytes(), nil
}

// A small checked reader keeps all codecs strict, bounded and trailing-byte safe.
type stableReader struct {
	r   *borsh.Reader
	err error
}

func newStableReader(data []byte, kind uint8) *stableReader {
	r := &stableReader{r: borsh.NewReader(data, MaxAssetStateBytes)}
	k := r.u8()
	v, e := r.r.ReadUint16()
	r.check(e)
	if k != kind || v != 1 {
		r.check(fmt.Errorf("stablecoin: unexpected state kind/version"))
	}
	return r
}
func (r *stableReader) check(e error) {
	if r.err == nil {
		r.err = e
	}
}
func (r *stableReader) u8() uint8   { v, e := r.r.ReadUint8(); r.check(e); return v }
func (r *stableReader) u64() uint64 { v, e := r.r.ReadUint64(); r.check(e); return v }
func (r *stableReader) str() string { v, e := r.r.ReadString(); r.check(e); return v }
func (r *stableReader) key() structure.PublicKey {
	v, e := readAssetPublicKey(r.r, "stablecoin key")
	r.check(e)
	return v
}
func (r *stableReader) done() error { r.check(r.r.EnsureEOF()); return r.err }

func UnmarshalStablecoinMint(data []byte) (StablecoinMint, error) {
	r := newStableReader(data, StableMintKind)
	s := StablecoinMint{Decimals: r.u8(), Creator: r.key(), Authority: r.key(), PendingAuthority: r.key(), Supply: r.u64(), Reserve: r.u64(), PendingRedemption: r.u64(), Limit: r.u64()}
	paused := r.u8()
	s.Paused = paused == 1
	s.Name, s.Symbol, s.Currency = r.str(), r.str(), r.str()
	if err := r.done(); err != nil {
		return s, err
	}
	if paused > 1 {
		return s, fmt.Errorf("stablecoin: invalid paused flag")
	}
	return s, s.Validate()
}

func (s StablecoinBalance) MarshalBinary() ([]byte, error) {
	if s.Mint.IsZero() || s.Owner.IsZero() {
		return nil, fmt.Errorf("stablecoin: empty balance binding")
	}
	w := stableWriter(StableBalanceKind)
	w.WriteFixedBytes(s.Mint[:])
	w.WriteFixedBytes(s.Owner[:])
	w.WriteUint64(s.Amount)
	return w.Bytes(), nil
}
func UnmarshalStablecoinBalance(data []byte) (StablecoinBalance, error) {
	r := newStableReader(data, StableBalanceKind)
	s := StablecoinBalance{Mint: r.key(), Owner: r.key(), Amount: r.u64()}
	if e := r.done(); e != nil {
		return s, e
	}
	_, e := s.MarshalBinary()
	return s, e
}
func (s StablecoinOrder) MarshalBinary() ([]byte, error) {
	if s.Mint.IsZero() || s.Owner.IsZero() || s.Amount == 0 || !stableReference(s.Reference) || (s.Kind != StableOrderIssue && s.Kind != StableOrderRedemption) || s.Status < 1 || s.Status > 4 {
		return nil, fmt.Errorf("stablecoin: invalid order")
	}
	if s.Kind == StableOrderIssue && s.Status != StableOrderSettled {
		return nil, fmt.Errorf("stablecoin: invalid issuance status")
	}
	if s.Kind == StableOrderRedemption && s.Status == StableOrderSettled && !stableReference(s.Receipt) {
		return nil, fmt.Errorf("stablecoin: missing payout receipt")
	}
	w := stableWriter(StableOrderKind)
	w.WriteFixedBytes(s.Mint[:])
	w.WriteFixedBytes(s.Owner[:])
	w.WriteUint8(s.Kind)
	w.WriteUint8(s.Status)
	w.WriteUint64(s.Amount)
	if e := w.WriteString(s.Reference); e != nil {
		return nil, e
	}
	if e := w.WriteString(s.Receipt); e != nil {
		return nil, e
	}
	return w.Bytes(), nil
}
func UnmarshalStablecoinOrder(data []byte) (StablecoinOrder, error) {
	r := newStableReader(data, StableOrderKind)
	s := StablecoinOrder{Mint: r.key(), Owner: r.key(), Kind: r.u8(), Status: r.u8(), Amount: r.u64(), Reference: r.str(), Receipt: r.str()}
	if e := r.done(); e != nil {
		return s, e
	}
	_, e := s.MarshalBinary()
	return s, e
}

func executeStablecoinInstruction(i AssetInstruction, c runtime.InstructionContext, accounts map[structure.PublicKey]structure.Account) error {
	if err := validateStablecoinInstruction(i); err != nil {
		return err
	}
	program, err := assetVMProgramID(c)
	if err != nil {
		return err
	}
	if !IsStablecoinProgram(accounts[program].Data) {
		return fmt.Errorf("stablecoin: immutable stablecoin program required")
	}
	expected := map[AssetInstructionType]int{StableInitialize: 2, StableOpenAccount: 3, StableIssue: 4, StableTransfer: 4, StableRequestRedemption: 4, StableSettleRedemption: 3, StableCancelRedemption: 4, StableClaimRedemption: 3, StablePause: 2, StableProposeAuthority: 3, StableAcceptAuthority: 2, StableSetLimit: 2, StableBurn: 3, StableRefundRedemption: 4}[i.Type]
	if len(c.Instruction.AccountIndexes) != expected {
		return fmt.Errorf("stablecoin: expected %d accounts", expected)
	}
	keys := make([]structure.PublicKey, expected)
	for n := range keys {
		keys[n], err = assetAccountAddressAt(c, n)
		if err != nil {
			return err
		}
	}
	actor, mintKey := keys[0], keys[1]
	if actor.IsZero() || actor == mintKey {
		return fmt.Errorf("stablecoin: invalid actor")
	}
	if err := requireTransactionSigner(actor, c, "stablecoin actor"); err != nil {
		return err
	}
	mintAccount, err := assetOwnedAccount("stablecoin mint", mintKey, c, accounts)
	if err != nil {
		return err
	}
	writeMint := func(s StablecoinMint) error {
		if e := requireAssetWritableAddress(c, mintKey, "stablecoin mint"); e != nil {
			return e
		}
		return writeAssetAccount(mintKey, mintAccount, s.MarshalBinary, c, accounts)
	}
	if i.Type == StableInitialize {
		if mintKey != StablecoinMintAddress(program, actor, i.Symbol) || len(mintAccount.Data) != 0 {
			return fmt.Errorf("stablecoin: mint address mismatch or already initialized")
		}
		return writeMint(StablecoinMint{Creator: actor, Authority: actor, Decimals: i.Decimals, Name: i.Name, Symbol: i.Symbol, Currency: i.URI, Limit: i.Amount})
	}
	m, err := UnmarshalStablecoinMint(mintAccount.Data)
	if err != nil {
		return err
	}
	if mintKey != StablecoinMintAddress(program, m.Creator, m.Symbol) {
		return fmt.Errorf("stablecoin: noncanonical mint")
	}
	issuer := actor == m.Authority
	if m.Paused && (i.Type == StableIssue || i.Type == StableTransfer || i.Type == StableRequestRedemption || i.Type == StableBurn) {
		return fmt.Errorf("stablecoin: asset paused")
	}
	readBalance := func(key structure.PublicKey) (StablecoinBalance, structure.Account, error) {
		a, e := assetOwnedWritableAccount("stablecoin balance", key, c, accounts)
		if e != nil {
			return StablecoinBalance{}, a, e
		}
		s, e := UnmarshalStablecoinBalance(a.Data)
		if e != nil {
			return s, a, e
		}
		if s.Mint != mintKey || key != StablecoinBalanceAddress(program, mintKey, s.Owner) {
			return s, a, fmt.Errorf("stablecoin: balance binding mismatch")
		}
		return s, a, nil
	}
	writeBalance := func(key structure.PublicKey, a structure.Account, s StablecoinBalance) error {
		return writeAssetAccount(key, a, s.MarshalBinary, c, accounts)
	}
	newOrder := func(key structure.PublicKey, s StablecoinOrder) error {
		if key != StablecoinOrderAddress(program, mintKey, s.Kind, s.Owner, s.Reference) {
			return fmt.Errorf("stablecoin: order address mismatch")
		}
		a, e := assetOwnedWritableAccount("stablecoin order", key, c, accounts)
		if e != nil {
			return e
		}
		if len(a.Data) != 0 {
			return fmt.Errorf("stablecoin: order already exists; do not issue or redeem twice")
		}
		return writeAssetAccount(key, a, s.MarshalBinary, c, accounts)
	}
	switch i.Type {
	case StableOpenAccount:
		key := keys[2]
		owner, e := structure.PublicKeyFromBase58(i.URI)
		if e != nil {
			return e
		}
		if owner.IsZero() || key != StablecoinBalanceAddress(program, mintKey, owner) {
			return fmt.Errorf("stablecoin: account owner/address mismatch")
		}
		a, e := assetOwnedWritableAccount("stablecoin balance", key, c, accounts)
		if e != nil {
			return e
		}
		if len(a.Data) > 0 {
			s, _, e := readBalance(key)
			if e != nil {
				return e
			}
			if s.Owner != owner {
				return fmt.Errorf("stablecoin: owner mismatch")
			}
			return nil
		}
		return writeBalance(key, a, StablecoinBalance{Mint: mintKey, Owner: owner})
	case StableIssue:
		if !issuer {
			return fmt.Errorf("stablecoin: issuer signature required")
		}
		s, a, e := readBalance(keys[2])
		if e != nil {
			return e
		}
		if i.Amount > math.MaxUint64-m.Reserve || i.Amount > math.MaxUint64-s.Amount || i.Amount > m.Limit-m.Supply-m.PendingRedemption {
			return fmt.Errorf("stablecoin: issuance exceeds limit or uint64")
		}
		if e := newOrder(keys[3], StablecoinOrder{Mint: mintKey, Owner: s.Owner, Kind: StableOrderIssue, Status: StableOrderSettled, Amount: i.Amount, Reference: i.URI}); e != nil {
			return e
		}
		m.Supply += i.Amount
		m.Reserve += i.Amount
		s.Amount += i.Amount
		if e := writeBalance(keys[2], a, s); e != nil {
			return e
		}
		return writeMint(m)
	case StableTransfer:
		if keys[2] == keys[3] {
			return fmt.Errorf("stablecoin: cannot transfer to same balance")
		}
		s, a, e := readBalance(keys[2])
		if e != nil {
			return e
		}
		d, b, e := readBalance(keys[3])
		if e != nil {
			return e
		}
		if s.Owner != actor || s.Amount < i.Amount || i.Amount > math.MaxUint64-d.Amount {
			return fmt.Errorf("stablecoin: transfer authority, balance or overflow error")
		}
		s.Amount -= i.Amount
		d.Amount += i.Amount
		if e := writeBalance(keys[2], a, s); e != nil {
			return e
		}
		return writeBalance(keys[3], b, d)
	case StableRequestRedemption, StableBurn:
		s, a, e := readBalance(keys[2])
		if e != nil {
			return e
		}
		if s.Owner != actor || s.Amount < i.Amount || m.Supply < i.Amount {
			return fmt.Errorf("stablecoin: burn authority or balance error")
		}
		if i.Type == StableRequestRedemption {
			if e := newOrder(keys[3], StablecoinOrder{Mint: mintKey, Owner: actor, Kind: StableOrderRedemption, Status: StableOrderPending, Amount: i.Amount, Reference: i.URI}); e != nil {
				return e
			}
			m.PendingRedemption += i.Amount
		}
		m.Supply -= i.Amount
		s.Amount -= i.Amount
		if e := writeBalance(keys[2], a, s); e != nil {
			return e
		}
		return writeMint(m)
	case StableClaimRedemption, StableSettleRedemption, StableCancelRedemption, StableRefundRedemption:
		orderIndex := 2
		if i.Type == StableCancelRedemption || i.Type == StableRefundRedemption {
			orderIndex = 3
		}
		key := keys[orderIndex]
		a, e := assetOwnedWritableAccount("stablecoin order", key, c, accounts)
		if e != nil {
			return e
		}
		o, e := UnmarshalStablecoinOrder(a.Data)
		if e != nil {
			return e
		}
		if o.Mint != mintKey || o.Kind != StableOrderRedemption || key != StablecoinOrderAddress(program, mintKey, o.Kind, o.Owner, o.Reference) {
			return fmt.Errorf("stablecoin: redemption order binding mismatch")
		}
		switch i.Type {
		case StableClaimRedemption:
			if !issuer || o.Status != StableOrderPending {
				return fmt.Errorf("stablecoin: only issuer can claim pending redemption")
			}
			o.Status = StableOrderProcessing
		case StableSettleRedemption:
			if !issuer || o.Status != StableOrderProcessing {
				return fmt.Errorf("stablecoin: issuer must claim redemption before confirming payout")
			}
			if m.PendingRedemption < o.Amount || m.Reserve < o.Amount {
				return fmt.Errorf("stablecoin: redemption accounting mismatch")
			}
			m.PendingRedemption -= o.Amount
			m.Reserve -= o.Amount
			o.Status = StableOrderSettled
			o.Receipt = i.URI
			if e := writeMint(m); e != nil {
				return e
			}
		case StableCancelRedemption, StableRefundRedemption:
			if i.Type == StableRefundRedemption {
				if !issuer || o.Status != StableOrderProcessing {
					return fmt.Errorf("stablecoin: only issuer can refund processing redemption")
				}
				o.Receipt = i.URI // Issuer attestation of definitive payout failure.
			} else if o.Status != StableOrderPending || (actor != o.Owner && !issuer) {
				return fmt.Errorf("stablecoin: only unclaimed redemption may be cancelled")
			}
			s, b, e := readBalance(keys[2])
			if e != nil {
				return e
			}
			if s.Owner != o.Owner || m.PendingRedemption < o.Amount || o.Amount > math.MaxUint64-s.Amount {
				return fmt.Errorf("stablecoin: cancellation balance mismatch")
			}
			m.PendingRedemption -= o.Amount
			m.Supply += o.Amount
			s.Amount += o.Amount
			o.Status = StableOrderCancelled
			if e := writeBalance(keys[2], b, s); e != nil {
				return e
			}
			if e := writeMint(m); e != nil {
				return e
			}
		}
		return writeAssetAccount(key, a, o.MarshalBinary, c, accounts)
	case StablePause, StableProposeAuthority, StableSetLimit:
		if !issuer {
			return fmt.Errorf("stablecoin: issuer signature required")
		}
		if i.Type == StablePause {
			m.Paused = i.Amount == 1
		}
		if i.Type == StableProposeAuthority {
			if keys[2].IsZero() || keys[2] == actor || keys[2] == mintKey {
				return fmt.Errorf("stablecoin: invalid successor")
			}
			m.PendingAuthority = keys[2]
		}
		if i.Type == StableSetLimit {
			m.Limit = i.Amount
		}
		return writeMint(m)
	case StableAcceptAuthority:
		if actor != m.PendingAuthority {
			return fmt.Errorf("stablecoin: proposed authority signature required")
		}
		m.Authority = actor
		m.PendingAuthority = structure.PublicKey{}
		return writeMint(m)
	}
	return fmt.Errorf("stablecoin: unsupported operation")
}
