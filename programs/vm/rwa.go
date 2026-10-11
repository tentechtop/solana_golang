package vmprogram

import (
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"
	"math"
	"math/bits"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/tentechtop/ordexus-core/codec/borsh"
	"github.com/tentechtop/ordexus-core/runtime"
	"github.com/tentechtop/ordexus-core/structure"
	ovm "github.com/tentechtop/ordexus-core/vm"
)

// RWA uses a separate immutable template and instruction range. It deliberately
// does not reuse unrestricted fungible-token/NFT operations: every holder and
// every transfer remains subject to the on-chain compliance registry.
const (
	RWAInitialize AssetInstructionType = 300 + iota
	RWASetInvestor
	RWAIssue
	RWATransfer
	RWABurn
	RWAPause
	RWAFreezeInvestor
	RWACreateProposal
	RWAVote
	RWAFinalizeProposal
	RWACreateDividend
	RWAClaimDividend
	RWACloseDividend
	RWAProposeAuthority
	RWAAcceptAuthority
	RWAForceTransfer
)

const (
	RWAAssetShare uint8 = iota + 1
	RWAAssetNFT
)

const (
	RWAMintKind uint8 = 48 + iota
	RWAHoldingKind
	RWAProposalKind
	RWAVoteKind
	RWADividendKind
	RWADividendClaimKind
)

const (
	RWAVoteYes uint8 = iota + 1
	RWAVoteNo
	RWAVoteAbstain
)

type RWAMint struct {
	AssetType        uint8
	Decimals         uint8
	Creator          structure.PublicKey
	Authority        structure.PublicKey
	PendingAuthority structure.PublicKey
	Supply           uint64
	MaxSupply        uint64
	Paused           bool
	Name             string
	Symbol           string
	DocumentHash     string
	ActiveProposal   structure.PublicKey
	ProposalEndSlot  uint64
	ActiveDividend   structure.PublicKey
	DividendEndSlot  uint64
}

type RWAHolding struct {
	Mint         structure.PublicKey
	Owner        structure.PublicKey
	Amount       uint64
	Approved     bool
	Frozen       bool
	IdentityHash string
}

type RWAProposal struct {
	Mint           structure.PublicKey
	Creator        structure.PublicKey
	CreatedSlot    uint64
	EndSlot        uint64
	SnapshotSupply uint64
	Yes            uint64
	No             uint64
	Abstain        uint64
	QuorumPercent  uint8
	Finalized      bool
	Passed         bool
	Title          string
	DocumentHash   string
}

type RWAVoteReceipt struct {
	Proposal structure.PublicKey
	Voter    structure.PublicKey
	Weight   uint64
	Choice   uint8
}

type RWADividend struct {
	Mint           structure.PublicKey
	Creator        structure.PublicKey
	CreatedSlot    uint64
	EndSlot        uint64
	SnapshotSupply uint64
	Total          uint64
	Claimed        uint64
	Closed         bool
	DocumentHash   string
}

type RWADividendClaim struct {
	Dividend structure.PublicKey
	Owner    structure.PublicKey
	Amount   uint64
}

func RWAProgramData() ([]byte, error) {
	tag, err := ovm.BuildRegisterInstruction(ovm.RegOpMovImm, 9, 0, 0, 0x52574131)
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

func IsRWAProgram(data []byte) bool {
	expected, err := RWAProgramData()
	return err == nil && ovm.BytecodeProgramsEqual(expected, data)
}

func rwaAddress(domain string, parts ...string) structure.PublicKey {
	seed := sha256.Sum256([]byte(domain + "|" + strings.Join(parts, "|")))
	private := ed25519.NewKeyFromSeed(seed[:])
	var key structure.PublicKey
	copy(key[:], private[32:])
	return key
}

func RWAMintAddress(program, creator structure.PublicKey, symbol string) structure.PublicKey {
	return rwaAddress("nulla.rwa.mint.v1", program.String(), creator.String(), symbol)
}

func RWAHoldingAddress(program, mint, owner structure.PublicKey) structure.PublicKey {
	return rwaAddress("nulla.rwa.holding.v1", program.String(), mint.String(), owner.String())
}

func RWAProposalAddress(program, mint structure.PublicKey, documentHash string) structure.PublicKey {
	return rwaAddress("nulla.rwa.proposal.v1", program.String(), mint.String(), documentHash)
}

func RWAVoteAddress(program, proposal, voter structure.PublicKey) structure.PublicKey {
	return rwaAddress("nulla.rwa.vote.v1", program.String(), proposal.String(), voter.String())
}

func RWADividendAddress(program, mint structure.PublicKey, documentHash string) structure.PublicKey {
	return rwaAddress("nulla.rwa.dividend.v1", program.String(), mint.String(), documentHash)
}

func RWADividendClaimAddress(program, dividend, owner structure.PublicKey) structure.PublicKey {
	return rwaAddress("nulla.rwa.dividend-claim.v1", program.String(), dividend.String(), owner.String())
}

func parseRWAAssetDescriptor(value string) (uint8, string, error) {
	kind, hash, ok := strings.Cut(value, ":")
	if !ok || !stableReference(hash) {
		return 0, "", fmt.Errorf("rwa: descriptor must be share:<sha256> or nft:<sha256>")
	}
	switch kind {
	case "share":
		return RWAAssetShare, hash, nil
	case "nft":
		return RWAAssetNFT, hash, nil
	default:
		return 0, "", fmt.Errorf("rwa: unsupported asset type")
	}
}

func validateRWAInstruction(i AssetInstruction) error {
	clearMetadata := func() bool { return i.Decimals == 0 && i.Name == "" && i.Symbol == "" && i.URI == "" }
	switch i.Type {
	case RWAInitialize:
		kind, _, err := parseRWAAssetDescriptor(i.URI)
		if err != nil || i.Amount == 0 || i.Decimals != 0 || !utf8.ValidString(i.Name) || strings.TrimSpace(i.Name) == "" || !stableSymbolPattern.MatchString(i.Symbol) {
			return fmt.Errorf("rwa: invalid name, symbol, supply, decimals or document descriptor")
		}
		if kind == RWAAssetNFT && i.Amount != 1 {
			return fmt.Errorf("rwa: nft max supply must be one")
		}
	case RWASetInvestor:
		if i.Amount > 1 || i.Decimals != 0 || i.Name != "" || i.Symbol != "" || !stableReference(i.URI) {
			return fmt.Errorf("rwa: investor status and identity commitment required")
		}
	case RWAIssue, RWATransfer, RWABurn:
		if i.Amount == 0 || !clearMetadata() {
			return fmt.Errorf("rwa: positive amount required")
		}
	case RWAPause, RWAFreezeInvestor:
		if i.Amount > 1 || i.Decimals != 0 || i.Name != "" || i.Symbol != "" || i.URI != "" {
			return fmt.Errorf("rwa: flag must be zero or one")
		}
	case RWACreateProposal:
		if i.Amount == 0 || i.Decimals == 0 || i.Decimals > 100 || !utf8.ValidString(i.Name) || strings.TrimSpace(i.Name) == "" || i.Symbol != "" || !stableReference(i.URI) {
			return fmt.Errorf("rwa: proposal requires end slot, quorum, title and document hash")
		}
	case RWAVote:
		if (i.Amount < uint64(RWAVoteYes) || i.Amount > uint64(RWAVoteAbstain)) || !clearMetadata() {
			return fmt.Errorf("rwa: vote choice must be yes, no or abstain")
		}
	case RWACreateDividend:
		deadline, err := strconv.ParseUint(i.Name, 10, 64)
		if i.Amount == 0 || err != nil || deadline == 0 || i.Decimals != 0 || i.Symbol != "" || !stableReference(i.URI) {
			return fmt.Errorf("rwa: dividend requires amount, end slot and document hash")
		}
	case RWAForceTransfer:
		if i.Amount == 0 || i.Decimals != 0 || i.Name != "" || i.Symbol != "" || !stableReference(i.URI) {
			return fmt.Errorf("rwa: forced transfer requires amount and legal-basis hash")
		}
	case RWAFinalizeProposal, RWAClaimDividend, RWACloseDividend, RWAProposeAuthority, RWAAcceptAuthority:
		if i.Amount != 0 || !clearMetadata() {
			return fmt.Errorf("rwa: unexpected operation data")
		}
	default:
		return fmt.Errorf("rwa: unsupported instruction")
	}
	return nil
}

func (s RWAMint) Validate() error {
	if (s.AssetType != RWAAssetShare && s.AssetType != RWAAssetNFT) || s.Decimals != 0 || s.Creator.IsZero() || s.Authority.IsZero() || s.MaxSupply == 0 || s.Supply > s.MaxSupply || !utf8.ValidString(s.Name) || strings.TrimSpace(s.Name) == "" || !stableSymbolPattern.MatchString(s.Symbol) || !stableReference(s.DocumentHash) {
		return fmt.Errorf("rwa: invalid mint")
	}
	if s.AssetType == RWAAssetNFT && s.MaxSupply != 1 {
		return fmt.Errorf("rwa: invalid nft supply")
	}
	if s.ActiveProposal.IsZero() != (s.ProposalEndSlot == 0) || s.ActiveDividend.IsZero() != (s.DividendEndSlot == 0) || (!s.ActiveProposal.IsZero() && !s.ActiveDividend.IsZero()) {
		return fmt.Errorf("rwa: invalid active corporate action")
	}
	return nil
}

func (s RWAHolding) Validate() error {
	if s.Mint.IsZero() || s.Owner.IsZero() || !stableReference(s.IdentityHash) {
		return fmt.Errorf("rwa: invalid holding")
	}
	return nil
}

func (s RWAProposal) Validate() error {
	if s.Mint.IsZero() || s.Creator.IsZero() || s.EndSlot <= s.CreatedSlot || s.SnapshotSupply == 0 || s.QuorumPercent == 0 || s.QuorumPercent > 100 || !utf8.ValidString(s.Title) || strings.TrimSpace(s.Title) == "" || !stableReference(s.DocumentHash) {
		return fmt.Errorf("rwa: invalid proposal")
	}
	if s.Yes > s.SnapshotSupply || s.No > s.SnapshotSupply-s.Yes || s.Abstain > s.SnapshotSupply-s.Yes-s.No || (!s.Finalized && s.Passed) {
		return fmt.Errorf("rwa: invalid proposal tally")
	}
	return nil
}

func (s RWAVoteReceipt) Validate() error {
	if s.Proposal.IsZero() || s.Voter.IsZero() || s.Weight == 0 || s.Choice < RWAVoteYes || s.Choice > RWAVoteAbstain {
		return fmt.Errorf("rwa: invalid vote receipt")
	}
	return nil
}

func (s RWADividend) Validate() error {
	if s.Mint.IsZero() || s.Creator.IsZero() || s.EndSlot <= s.CreatedSlot || s.SnapshotSupply == 0 || s.Total == 0 || s.Claimed > s.Total || !stableReference(s.DocumentHash) {
		return fmt.Errorf("rwa: invalid dividend")
	}
	return nil
}

func (s RWADividendClaim) Validate() error {
	if s.Dividend.IsZero() || s.Owner.IsZero() {
		return fmt.Errorf("rwa: invalid dividend claim")
	}
	return nil
}

func rwaWriter(kind uint8) *borsh.Writer {
	w := borsh.NewWriter(MaxAssetStateBytes)
	w.WriteUint8(kind)
	w.WriteUint16(1)
	return w
}

func writeRWABool(w *borsh.Writer, value bool) {
	if value {
		w.WriteUint8(1)
	} else {
		w.WriteUint8(0)
	}
}

func (s RWAMint) MarshalBinary() ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	w := rwaWriter(RWAMintKind)
	w.WriteUint8(s.AssetType)
	w.WriteUint8(s.Decimals)
	for _, key := range []structure.PublicKey{s.Creator, s.Authority, s.PendingAuthority} {
		w.WriteFixedBytes(key[:])
	}
	w.WriteUint64(s.Supply)
	w.WriteUint64(s.MaxSupply)
	writeRWABool(w, s.Paused)
	for _, value := range []string{s.Name, s.Symbol, s.DocumentHash} {
		if err := w.WriteString(value); err != nil {
			return nil, err
		}
	}
	w.WriteFixedBytes(s.ActiveProposal[:])
	w.WriteUint64(s.ProposalEndSlot)
	w.WriteFixedBytes(s.ActiveDividend[:])
	w.WriteUint64(s.DividendEndSlot)
	return w.Bytes(), nil
}

func (s RWAHolding) MarshalBinary() ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	w := rwaWriter(RWAHoldingKind)
	w.WriteFixedBytes(s.Mint[:])
	w.WriteFixedBytes(s.Owner[:])
	w.WriteUint64(s.Amount)
	writeRWABool(w, s.Approved)
	writeRWABool(w, s.Frozen)
	if err := w.WriteString(s.IdentityHash); err != nil {
		return nil, err
	}
	return w.Bytes(), nil
}

func (s RWAProposal) MarshalBinary() ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	w := rwaWriter(RWAProposalKind)
	w.WriteFixedBytes(s.Mint[:])
	w.WriteFixedBytes(s.Creator[:])
	for _, value := range []uint64{s.CreatedSlot, s.EndSlot, s.SnapshotSupply, s.Yes, s.No, s.Abstain} {
		w.WriteUint64(value)
	}
	w.WriteUint8(s.QuorumPercent)
	writeRWABool(w, s.Finalized)
	writeRWABool(w, s.Passed)
	if err := w.WriteString(s.Title); err != nil {
		return nil, err
	}
	if err := w.WriteString(s.DocumentHash); err != nil {
		return nil, err
	}
	return w.Bytes(), nil
}

func (s RWAVoteReceipt) MarshalBinary() ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	w := rwaWriter(RWAVoteKind)
	w.WriteFixedBytes(s.Proposal[:])
	w.WriteFixedBytes(s.Voter[:])
	w.WriteUint64(s.Weight)
	w.WriteUint8(s.Choice)
	return w.Bytes(), nil
}

func (s RWADividend) MarshalBinary() ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	w := rwaWriter(RWADividendKind)
	w.WriteFixedBytes(s.Mint[:])
	w.WriteFixedBytes(s.Creator[:])
	for _, value := range []uint64{s.CreatedSlot, s.EndSlot, s.SnapshotSupply, s.Total, s.Claimed} {
		w.WriteUint64(value)
	}
	writeRWABool(w, s.Closed)
	if err := w.WriteString(s.DocumentHash); err != nil {
		return nil, err
	}
	return w.Bytes(), nil
}

func (s RWADividendClaim) MarshalBinary() ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	w := rwaWriter(RWADividendClaimKind)
	w.WriteFixedBytes(s.Dividend[:])
	w.WriteFixedBytes(s.Owner[:])
	w.WriteUint64(s.Amount)
	return w.Bytes(), nil
}

type rwaReader struct {
	r   *borsh.Reader
	err error
}

func newRWAReader(data []byte, kind uint8) *rwaReader {
	r := &rwaReader{r: borsh.NewReader(data, MaxAssetStateBytes)}
	actual := r.u8()
	version := r.u16()
	if actual != kind || version != 1 {
		r.check(fmt.Errorf("rwa: unexpected state kind/version"))
	}
	return r
}

func (r *rwaReader) check(err error) {
	if r.err == nil {
		r.err = err
	}
}
func (r *rwaReader) u8() uint8   { value, err := r.r.ReadUint8(); r.check(err); return value }
func (r *rwaReader) u16() uint16 { value, err := r.r.ReadUint16(); r.check(err); return value }
func (r *rwaReader) u64() uint64 { value, err := r.r.ReadUint64(); r.check(err); return value }
func (r *rwaReader) str() string { value, err := r.r.ReadString(); r.check(err); return value }
func (r *rwaReader) key() structure.PublicKey {
	value, err := readAssetPublicKey(r.r, "rwa key")
	r.check(err)
	return value
}
func (r *rwaReader) boolean() bool {
	value := r.u8()
	if value > 1 {
		r.check(fmt.Errorf("rwa: invalid boolean"))
	}
	return value == 1
}
func (r *rwaReader) done() error { r.check(r.r.EnsureEOF()); return r.err }

func UnmarshalRWAMint(data []byte) (RWAMint, error) {
	r := newRWAReader(data, RWAMintKind)
	s := RWAMint{AssetType: r.u8(), Decimals: r.u8(), Creator: r.key(), Authority: r.key(), PendingAuthority: r.key(), Supply: r.u64(), MaxSupply: r.u64(), Paused: r.boolean(), Name: r.str(), Symbol: r.str(), DocumentHash: r.str(), ActiveProposal: r.key(), ProposalEndSlot: r.u64(), ActiveDividend: r.key(), DividendEndSlot: r.u64()}
	if err := r.done(); err != nil {
		return s, err
	}
	return s, s.Validate()
}

func UnmarshalRWAHolding(data []byte) (RWAHolding, error) {
	r := newRWAReader(data, RWAHoldingKind)
	s := RWAHolding{Mint: r.key(), Owner: r.key(), Amount: r.u64(), Approved: r.boolean(), Frozen: r.boolean(), IdentityHash: r.str()}
	if err := r.done(); err != nil {
		return s, err
	}
	return s, s.Validate()
}

func UnmarshalRWAProposal(data []byte) (RWAProposal, error) {
	r := newRWAReader(data, RWAProposalKind)
	s := RWAProposal{Mint: r.key(), Creator: r.key(), CreatedSlot: r.u64(), EndSlot: r.u64(), SnapshotSupply: r.u64(), Yes: r.u64(), No: r.u64(), Abstain: r.u64(), QuorumPercent: r.u8(), Finalized: r.boolean(), Passed: r.boolean(), Title: r.str(), DocumentHash: r.str()}
	if err := r.done(); err != nil {
		return s, err
	}
	return s, s.Validate()
}

func UnmarshalRWAVoteReceipt(data []byte) (RWAVoteReceipt, error) {
	r := newRWAReader(data, RWAVoteKind)
	s := RWAVoteReceipt{Proposal: r.key(), Voter: r.key(), Weight: r.u64(), Choice: r.u8()}
	if err := r.done(); err != nil {
		return s, err
	}
	return s, s.Validate()
}

func UnmarshalRWADividend(data []byte) (RWADividend, error) {
	r := newRWAReader(data, RWADividendKind)
	s := RWADividend{Mint: r.key(), Creator: r.key(), CreatedSlot: r.u64(), EndSlot: r.u64(), SnapshotSupply: r.u64(), Total: r.u64(), Claimed: r.u64(), Closed: r.boolean(), DocumentHash: r.str()}
	if err := r.done(); err != nil {
		return s, err
	}
	return s, s.Validate()
}

func UnmarshalRWADividendClaim(data []byte) (RWADividendClaim, error) {
	r := newRWAReader(data, RWADividendClaimKind)
	s := RWADividendClaim{Dividend: r.key(), Owner: r.key(), Amount: r.u64()}
	if err := r.done(); err != nil {
		return s, err
	}
	return s, s.Validate()
}

func executeRWAInstruction(i AssetInstruction, c runtime.InstructionContext, accounts map[structure.PublicKey]structure.Account) error {
	if err := validateRWAInstruction(i); err != nil {
		return err
	}
	program, err := assetVMProgramID(c)
	if err != nil {
		return err
	}
	if !IsRWAProgram(accounts[program].Data) {
		return fmt.Errorf("rwa: immutable RWA template required")
	}
	expected := map[AssetInstructionType]int{
		RWAInitialize: 2, RWASetInvestor: 4, RWAIssue: 3, RWATransfer: 4,
		RWABurn: 3, RWAPause: 2, RWAFreezeInvestor: 3, RWACreateProposal: 3,
		RWAVote: 5, RWAFinalizeProposal: 3, RWACreateDividend: 3,
		RWAClaimDividend: 5, RWACloseDividend: 4, RWAProposeAuthority: 3,
		RWAAcceptAuthority: 2, RWAForceTransfer: 4,
	}[i.Type]
	if len(c.Instruction.AccountIndexes) != expected {
		return fmt.Errorf("rwa: expected %d accounts", expected)
	}
	keys := make([]structure.PublicKey, expected)
	for index := range keys {
		keys[index], err = assetAccountAddressAt(c, index)
		if err != nil {
			return err
		}
	}
	actor, mintKey := keys[0], keys[1]
	if actor.IsZero() || actor == mintKey {
		return fmt.Errorf("rwa: invalid actor")
	}
	if err := requireTransactionSigner(actor, c, "rwa actor"); err != nil {
		return err
	}
	mintAccount, err := assetOwnedAccount("rwa mint", mintKey, c, accounts)
	if err != nil {
		return err
	}
	writeMint := func(s RWAMint) error {
		if err := requireAssetWritableAddress(c, mintKey, "rwa mint"); err != nil {
			return err
		}
		return writeAssetAccount(mintKey, accounts[mintKey], s.MarshalBinary, c, accounts)
	}
	if i.Type == RWAInitialize {
		if len(mintAccount.Data) != 0 || mintKey != RWAMintAddress(program, actor, i.Symbol) {
			return fmt.Errorf("rwa: noncanonical or initialized mint")
		}
		assetType, documentHash, _ := parseRWAAssetDescriptor(i.URI)
		return writeMint(RWAMint{AssetType: assetType, Decimals: i.Decimals, Creator: actor, Authority: actor, MaxSupply: i.Amount, Name: i.Name, Symbol: i.Symbol, DocumentHash: documentHash})
	}
	m, err := UnmarshalRWAMint(mintAccount.Data)
	if err != nil {
		return err
	}
	if mintKey != RWAMintAddress(program, m.Creator, m.Symbol) {
		return fmt.Errorf("rwa: noncanonical mint")
	}
	authority := actor == m.Authority
	corporateActionActive := !m.ActiveProposal.IsZero() || !m.ActiveDividend.IsZero()
	readHolding := func(key structure.PublicKey, writable bool) (RWAHolding, structure.Account, error) {
		var account structure.Account
		var err error
		if writable {
			account, err = assetOwnedWritableAccount("rwa holding", key, c, accounts)
		} else {
			account, err = assetOwnedAccount("rwa holding", key, c, accounts)
		}
		if err != nil {
			return RWAHolding{}, account, err
		}
		holding, err := UnmarshalRWAHolding(account.Data)
		if err != nil {
			return holding, account, err
		}
		if holding.Mint != mintKey || key != RWAHoldingAddress(program, mintKey, holding.Owner) {
			return holding, account, fmt.Errorf("rwa: holding binding mismatch")
		}
		return holding, account, nil
	}
	writeHolding := func(key structure.PublicKey, account structure.Account, holding RWAHolding) error {
		return writeAssetAccount(key, account, holding.MarshalBinary, c, accounts)
	}
	if m.Paused && (i.Type == RWAIssue || i.Type == RWATransfer || i.Type == RWABurn || i.Type == RWAForceTransfer || i.Type == RWACreateDividend) {
		return fmt.Errorf("rwa: asset paused")
	}
	switch i.Type {
	case RWASetInvestor:
		if !authority || corporateActionActive {
			return fmt.Errorf("rwa: authority required and corporate action must be closed")
		}
		owner, holdingKey := keys[2], keys[3]
		if owner.IsZero() || owner == mintKey || owner == program || holdingKey != RWAHoldingAddress(program, mintKey, owner) {
			return fmt.Errorf("rwa: noncanonical investor holding")
		}
		account, err := assetOwnedWritableAccount("rwa holding", holdingKey, c, accounts)
		if err != nil {
			return err
		}
		holding := RWAHolding{Mint: mintKey, Owner: owner, Approved: i.Amount == 1, IdentityHash: i.URI}
		if len(account.Data) != 0 {
			holding, err = UnmarshalRWAHolding(account.Data)
			if err != nil || holding.Mint != mintKey || holding.Owner != owner {
				return fmt.Errorf("rwa: investor holding binding mismatch")
			}
			holding.Approved = i.Amount == 1
			holding.IdentityHash = i.URI
		}
		return writeHolding(holdingKey, account, holding)
	case RWAIssue:
		if !authority || corporateActionActive {
			return fmt.Errorf("rwa: authority required and corporate action must be closed")
		}
		holding, account, err := readHolding(keys[2], true)
		if err != nil {
			return err
		}
		if !holding.Approved || holding.Frozen {
			return fmt.Errorf("rwa: destination investor is not eligible")
		}
		if m.AssetType == RWAAssetNFT && i.Amount != 1 {
			return fmt.Errorf("rwa: nft amount must be one")
		}
		if i.Amount > m.MaxSupply-m.Supply || i.Amount > math.MaxUint64-holding.Amount {
			return fmt.Errorf("rwa: issuance limit or amount overflow")
		}
		m.Supply += i.Amount
		holding.Amount += i.Amount
		if err := writeHolding(keys[2], account, holding); err != nil {
			return err
		}
		return writeMint(m)
	case RWATransfer, RWAForceTransfer:
		if corporateActionActive {
			return fmt.Errorf("rwa: transfer locked by corporate action")
		}
		if i.Type == RWAForceTransfer && !authority {
			return fmt.Errorf("rwa: authority required for forced transfer")
		}
		source, sourceAccount, err := readHolding(keys[2], true)
		if err != nil {
			return err
		}
		destination, destinationAccount, err := readHolding(keys[3], true)
		if err != nil {
			return err
		}
		if keys[2] == keys[3] {
			return fmt.Errorf("rwa: source and destination must differ")
		}
		if i.Type == RWATransfer && (source.Owner != actor || !source.Approved || source.Frozen) {
			return fmt.Errorf("rwa: source investor is not eligible or did not sign")
		}
		if !destination.Approved || destination.Frozen {
			return fmt.Errorf("rwa: destination investor is not eligible")
		}
		if m.AssetType == RWAAssetNFT && i.Amount != 1 {
			return fmt.Errorf("rwa: nft amount must be one")
		}
		if source.Amount < i.Amount || i.Amount > math.MaxUint64-destination.Amount {
			return fmt.Errorf("rwa: insufficient balance or amount overflow")
		}
		source.Amount -= i.Amount
		destination.Amount += i.Amount
		if err := writeHolding(keys[2], sourceAccount, source); err != nil {
			return err
		}
		return writeHolding(keys[3], destinationAccount, destination)
	case RWABurn:
		if corporateActionActive {
			return fmt.Errorf("rwa: burn locked by corporate action")
		}
		holding, account, err := readHolding(keys[2], true)
		if err != nil {
			return err
		}
		if holding.Owner != actor || !holding.Approved || holding.Frozen || holding.Amount < i.Amount || m.Supply < i.Amount {
			return fmt.Errorf("rwa: burn authority or balance error")
		}
		if m.AssetType == RWAAssetNFT && i.Amount != 1 {
			return fmt.Errorf("rwa: nft amount must be one")
		}
		holding.Amount -= i.Amount
		m.Supply -= i.Amount
		if err := writeHolding(keys[2], account, holding); err != nil {
			return err
		}
		return writeMint(m)
	case RWAPause:
		if !authority {
			return fmt.Errorf("rwa: authority required")
		}
		m.Paused = i.Amount == 1
		return writeMint(m)
	case RWAFreezeInvestor:
		if !authority || corporateActionActive {
			return fmt.Errorf("rwa: authority required and corporate action must be closed")
		}
		holding, account, err := readHolding(keys[2], true)
		if err != nil {
			return err
		}
		holding.Frozen = i.Amount == 1
		return writeHolding(keys[2], account, holding)
	case RWACreateProposal:
		if !authority || m.AssetType != RWAAssetShare || corporateActionActive || m.Supply == 0 {
			return fmt.Errorf("rwa: share authority and nonzero unlocked supply required")
		}
		if i.Amount <= c.CurrentSlot {
			return fmt.Errorf("rwa: proposal end slot must be in the future")
		}
		proposalKey := keys[2]
		if proposalKey != RWAProposalAddress(program, mintKey, i.URI) {
			return fmt.Errorf("rwa: noncanonical proposal")
		}
		account, err := assetOwnedWritableAccount("rwa proposal", proposalKey, c, accounts)
		if err != nil {
			return err
		}
		if len(account.Data) != 0 {
			return fmt.Errorf("rwa: proposal already exists")
		}
		proposal := RWAProposal{Mint: mintKey, Creator: actor, CreatedSlot: c.CurrentSlot, EndSlot: i.Amount, SnapshotSupply: m.Supply, QuorumPercent: i.Decimals, Title: i.Name, DocumentHash: i.URI}
		if err := writeAssetAccount(proposalKey, account, proposal.MarshalBinary, c, accounts); err != nil {
			return err
		}
		m.ActiveProposal, m.ProposalEndSlot = proposalKey, i.Amount
		return writeMint(m)
	case RWAVote:
		proposalKey, voteKey := keys[3], keys[4]
		if m.ActiveProposal != proposalKey || c.CurrentSlot >= m.ProposalEndSlot {
			return fmt.Errorf("rwa: proposal is not open")
		}
		holding, _, err := readHolding(keys[2], false)
		if err != nil {
			return err
		}
		if holding.Owner != actor || !holding.Approved || holding.Frozen || holding.Amount == 0 {
			return fmt.Errorf("rwa: voter is not eligible")
		}
		proposalAccount, err := assetOwnedWritableAccount("rwa proposal", proposalKey, c, accounts)
		if err != nil {
			return err
		}
		proposal, err := UnmarshalRWAProposal(proposalAccount.Data)
		if err != nil || proposal.Mint != mintKey || proposal.EndSlot != m.ProposalEndSlot || proposal.Finalized {
			return fmt.Errorf("rwa: proposal binding mismatch")
		}
		if voteKey != RWAVoteAddress(program, proposalKey, actor) {
			return fmt.Errorf("rwa: noncanonical vote receipt")
		}
		voteAccount, err := assetOwnedWritableAccount("rwa vote", voteKey, c, accounts)
		if err != nil {
			return err
		}
		if len(voteAccount.Data) != 0 {
			return fmt.Errorf("rwa: investor already voted")
		}
		participation := proposal.Yes + proposal.No + proposal.Abstain
		if participation > proposal.SnapshotSupply || holding.Amount > proposal.SnapshotSupply-participation {
			return fmt.Errorf("rwa: vote weight exceeds snapshot supply")
		}
		switch uint8(i.Amount) {
		case RWAVoteYes:
			proposal.Yes += holding.Amount
		case RWAVoteNo:
			proposal.No += holding.Amount
		case RWAVoteAbstain:
			proposal.Abstain += holding.Amount
		}
		if err := writeAssetAccount(proposalKey, proposalAccount, proposal.MarshalBinary, c, accounts); err != nil {
			return err
		}
		receipt := RWAVoteReceipt{Proposal: proposalKey, Voter: actor, Weight: holding.Amount, Choice: uint8(i.Amount)}
		return writeAssetAccount(voteKey, voteAccount, receipt.MarshalBinary, c, accounts)
	case RWAFinalizeProposal:
		proposalKey := keys[2]
		if m.ActiveProposal != proposalKey || c.CurrentSlot < m.ProposalEndSlot {
			return fmt.Errorf("rwa: proposal cannot be finalized")
		}
		account, err := assetOwnedWritableAccount("rwa proposal", proposalKey, c, accounts)
		if err != nil {
			return err
		}
		proposal, err := UnmarshalRWAProposal(account.Data)
		if err != nil || proposal.Mint != mintKey || proposal.EndSlot != m.ProposalEndSlot || proposal.Finalized {
			return fmt.Errorf("rwa: proposal binding mismatch")
		}
		participation := proposal.Yes + proposal.No + proposal.Abstain
		required := proposal.SnapshotSupply / 100 * uint64(proposal.QuorumPercent)
		remainderProduct := (proposal.SnapshotSupply % 100) * uint64(proposal.QuorumPercent)
		required += remainderProduct / 100
		if remainderProduct%100 != 0 {
			required++
		}
		proposal.Finalized = true
		proposal.Passed = participation >= required && proposal.Yes > proposal.No
		if err := writeAssetAccount(proposalKey, account, proposal.MarshalBinary, c, accounts); err != nil {
			return err
		}
		m.ActiveProposal, m.ProposalEndSlot = structure.PublicKey{}, 0
		return writeMint(m)
	case RWACreateDividend:
		if !authority || m.AssetType != RWAAssetShare || corporateActionActive || m.Supply == 0 {
			return fmt.Errorf("rwa: share authority and nonzero unlocked supply required")
		}
		endSlot, _ := strconv.ParseUint(i.Name, 10, 64)
		if endSlot <= c.CurrentSlot {
			return fmt.Errorf("rwa: dividend end slot must be in the future")
		}
		dividendKey := keys[2]
		if dividendKey != RWADividendAddress(program, mintKey, i.URI) {
			return fmt.Errorf("rwa: noncanonical dividend")
		}
		account, err := assetOwnedWritableAccount("rwa dividend", dividendKey, c, accounts)
		if err != nil {
			return err
		}
		if len(account.Data) != 0 {
			return fmt.Errorf("rwa: dividend already exists")
		}
		if err := transferRWALamports(actor, dividendKey, i.Amount, true, program, c, accounts); err != nil {
			return err
		}
		dividend := RWADividend{Mint: mintKey, Creator: actor, CreatedSlot: c.CurrentSlot, EndSlot: endSlot, SnapshotSupply: m.Supply, Total: i.Amount, DocumentHash: i.URI}
		if err := writeAssetAccount(dividendKey, accounts[dividendKey], dividend.MarshalBinary, c, accounts); err != nil {
			return err
		}
		m.ActiveDividend, m.DividendEndSlot = dividendKey, endSlot
		return writeMint(m)
	case RWAClaimDividend:
		dividendKey, claimKey := keys[3], keys[4]
		if m.ActiveDividend != dividendKey || c.CurrentSlot >= m.DividendEndSlot {
			return fmt.Errorf("rwa: dividend is not open")
		}
		holding, _, err := readHolding(keys[2], false)
		if err != nil {
			return err
		}
		if holding.Owner != actor || !holding.Approved || holding.Frozen || holding.Amount == 0 {
			return fmt.Errorf("rwa: claimant is not eligible")
		}
		dividendAccount, err := assetOwnedWritableAccount("rwa dividend", dividendKey, c, accounts)
		if err != nil {
			return err
		}
		dividend, err := UnmarshalRWADividend(dividendAccount.Data)
		if err != nil || dividend.Mint != mintKey || dividend.EndSlot != m.DividendEndSlot || dividend.Closed {
			return fmt.Errorf("rwa: dividend binding mismatch")
		}
		if holding.Amount > dividend.SnapshotSupply {
			return fmt.Errorf("rwa: holding exceeds dividend snapshot")
		}
		if claimKey != RWADividendClaimAddress(program, dividendKey, actor) {
			return fmt.Errorf("rwa: noncanonical dividend claim")
		}
		claimAccount, err := assetOwnedWritableAccount("rwa dividend claim", claimKey, c, accounts)
		if err != nil {
			return err
		}
		if len(claimAccount.Data) != 0 {
			return fmt.Errorf("rwa: dividend already claimed")
		}
		high, low := bits.Mul64(dividend.Total, holding.Amount)
		payout, _ := bits.Div64(high, low, dividend.SnapshotSupply)
		if payout > dividend.Total-dividend.Claimed {
			return fmt.Errorf("rwa: dividend accounting overflow")
		}
		if payout > 0 {
			if err := transferRWALamports(dividendKey, actor, payout, false, program, c, accounts); err != nil {
				return err
			}
		}
		dividend.Claimed += payout
		if err := writeAssetAccount(dividendKey, accounts[dividendKey], dividend.MarshalBinary, c, accounts); err != nil {
			return err
		}
		claim := RWADividendClaim{Dividend: dividendKey, Owner: actor, Amount: payout}
		return writeAssetAccount(claimKey, claimAccount, claim.MarshalBinary, c, accounts)
	case RWACloseDividend:
		dividendKey, authorityKey := keys[2], keys[3]
		if m.ActiveDividend != dividendKey || authorityKey != m.Authority || c.CurrentSlot < m.DividendEndSlot {
			return fmt.Errorf("rwa: dividend cannot be closed")
		}
		account, err := assetOwnedWritableAccount("rwa dividend", dividendKey, c, accounts)
		if err != nil {
			return err
		}
		dividend, err := UnmarshalRWADividend(account.Data)
		if err != nil || dividend.Mint != mintKey || dividend.EndSlot != m.DividendEndSlot || dividend.Closed {
			return fmt.Errorf("rwa: dividend binding mismatch")
		}
		remainder := dividend.Total - dividend.Claimed
		if remainder > 0 {
			if err := transferRWALamports(dividendKey, authorityKey, remainder, false, program, c, accounts); err != nil {
				return err
			}
		}
		dividend.Closed = true
		if err := writeAssetAccount(dividendKey, accounts[dividendKey], dividend.MarshalBinary, c, accounts); err != nil {
			return err
		}
		m.ActiveDividend, m.DividendEndSlot = structure.PublicKey{}, 0
		return writeMint(m)
	case RWAProposeAuthority:
		if !authority || keys[2].IsZero() || keys[2] == actor || keys[2] == mintKey {
			return fmt.Errorf("rwa: valid successor and authority required")
		}
		m.PendingAuthority = keys[2]
		return writeMint(m)
	case RWAAcceptAuthority:
		if actor != m.PendingAuthority {
			return fmt.Errorf("rwa: proposed authority must sign")
		}
		m.Authority, m.PendingAuthority = actor, structure.PublicKey{}
		return writeMint(m)
	}
	return fmt.Errorf("rwa: unsupported operation")
}

func transferRWALamports(from, to structure.PublicKey, amount uint64, signed bool, program structure.PublicKey, c runtime.InstructionContext, accounts map[structure.PublicKey]structure.Account) error {
	if err := requireAssetWritableAddress(c, from, "rwa payment source"); err != nil {
		return err
	}
	if err := requireAssetWritableAddress(c, to, "rwa payment destination"); err != nil {
		return err
	}
	source, sourceOK := accounts[from]
	target, targetOK := accounts[to]
	if !sourceOK || !targetOK || source.Executable || target.Executable {
		return fmt.Errorf("rwa: invalid payment account")
	}
	if signed {
		if !runtime.IsSignerContextAddress(from, c) || source.Owner != c.BuiltinPrograms.System || len(source.Data) != 0 {
			return fmt.Errorf("rwa: payer must be a signed system wallet")
		}
		if target.Owner != program {
			return fmt.Errorf("rwa: dividend escrow owner mismatch")
		}
	} else if source.Owner != program {
		return fmt.Errorf("rwa: dividend escrow owner mismatch")
	} else if target.Owner != c.BuiltinPrograms.System || len(target.Data) != 0 {
		return fmt.Errorf("rwa: dividend recipient must be a system wallet")
	}
	if source.Lamports < amount || target.Lamports > math.MaxUint64-amount {
		return fmt.Errorf("rwa: insufficient lamports or overflow")
	}
	source.Lamports -= amount
	target.Lamports += amount
	if err := source.ValidateWithRent(c.RentConfig); err != nil {
		return err
	}
	if err := target.ValidateWithRent(c.RentConfig); err != nil {
		return err
	}
	accounts[from], accounts[to] = source, target
	return nil
}
