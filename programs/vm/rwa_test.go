package vmprogram

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"strconv"
	"testing"

	"github.com/tentechtop/ordexus-core/runtime"
	"github.com/tentechtop/ordexus-core/structure"
	ovm "github.com/tentechtop/ordexus-core/vm"
	"github.com/tentechtop/ordexus-core/vm/assembler"
)

type rwaFixture struct {
	t                                            *testing.T
	program, issuer, alice, bob, outsider, next  structure.PublicKey
	mint, aliceHolding, bobHolding, otherHolding structure.PublicKey
	proposal, aliceVote, bobVote                 structure.PublicKey
	dividend, aliceClaim, bobClaim               structure.PublicKey
	accounts                                     map[structure.PublicKey]structure.Account
}

func rwaTestHash(label string) string {
	sum := sha256.Sum256([]byte(label))
	return hex.EncodeToString(sum[:])
}

func newRWAFixture(t *testing.T, assetType uint8) *rwaFixture {
	t.Helper()
	f := &rwaFixture{t: t}
	f.program = rwaAddress("rwa-test", "program", strconv.Itoa(int(assetType)))
	f.issuer = rwaAddress("rwa-test", "issuer", strconv.Itoa(int(assetType)))
	f.alice = rwaAddress("rwa-test", "alice", strconv.Itoa(int(assetType)))
	f.bob = rwaAddress("rwa-test", "bob", strconv.Itoa(int(assetType)))
	f.outsider = rwaAddress("rwa-test", "outsider", strconv.Itoa(int(assetType)))
	f.next = rwaAddress("rwa-test", "next", strconv.Itoa(int(assetType)))
	symbol := "RSHARE"
	descriptor := "share:" + rwaTestHash("share-offering-documents")
	maxSupply := uint64(1000)
	if assetType == RWAAssetNFT {
		symbol = "RNFT"
		descriptor = "nft:" + rwaTestHash("nft-title-documents")
		maxSupply = 1
	}
	f.mint = RWAMintAddress(f.program, f.issuer, symbol)
	f.aliceHolding = RWAHoldingAddress(f.program, f.mint, f.alice)
	f.bobHolding = RWAHoldingAddress(f.program, f.mint, f.bob)
	f.otherHolding = RWAHoldingAddress(f.program, f.mint, f.outsider)
	f.proposal = RWAProposalAddress(f.program, f.mint, rwaTestHash("proposal-1"))
	f.aliceVote = RWAVoteAddress(f.program, f.proposal, f.alice)
	f.bobVote = RWAVoteAddress(f.program, f.proposal, f.bob)
	f.dividend = RWADividendAddress(f.program, f.mint, rwaTestHash("dividend-1"))
	f.aliceClaim = RWADividendClaimAddress(f.program, f.dividend, f.alice)
	f.bobClaim = RWADividendClaimAddress(f.program, f.dividend, f.bob)
	code, err := RWAProgramData()
	if err != nil {
		t.Fatal(err)
	}
	f.accounts = map[structure.PublicKey]structure.Account{
		f.program: {Owner: structure.DefaultBuiltinProgramIDs.BPFLoader, Executable: true, Lamports: 100_000_000, Data: code},
		f.mint:    {Owner: f.program, Lamports: 100_000_000},
	}
	for _, key := range []structure.PublicKey{f.issuer, f.alice, f.bob, f.outsider, f.next} {
		f.accounts[key] = structure.Account{Owner: structure.DefaultBuiltinProgramIDs.System, Lamports: 1_000_000_000}
	}
	for _, key := range []structure.PublicKey{f.aliceHolding, f.bobHolding, f.otherHolding, f.proposal, f.aliceVote, f.bobVote, f.dividend, f.aliceClaim, f.bobClaim} {
		f.accounts[key] = structure.Account{Owner: f.program, Lamports: 100_000_000}
	}
	f.call(true, f.issuer, AssetInstruction{Type: RWAInitialize, Amount: maxSupply, Name: "Regulated Real Asset", Symbol: symbol, URI: descriptor}, 10)
	return f
}

func (f *rwaFixture) call(ok bool, actor structure.PublicKey, instruction AssetInstruction, slot uint64, extra ...structure.PublicKey) {
	f.t.Helper()
	keys := append([]structure.PublicKey{actor, f.mint}, extra...)
	indexes := make([]uint8, len(keys))
	for index := range indexes {
		indexes[index] = uint8(index)
	}
	keys = append(keys, f.program)
	wire, err := instruction.MarshalBinary()
	if err != nil {
		f.t.Fatalf("marshal operation %d: %v", instruction.Type, err)
	}
	before := cloneStructureAccounts(f.accounts)
	context := runtime.InstructionContext{
		Instruction:     structure.CompiledInstruction{ProgramIDIndex: uint8(len(keys) - 1), AccountIndexes: indexes, Data: wire},
		Message:         structure.ResolvedMessage{Header: structure.MessageHeader{NumRequiredSignatures: 1, NumReadonlyUnsignedAccounts: 1}, StaticAccountKeys: keys, AccountKeys: keys},
		Accounts:        f.accounts,
		CurrentSlot:     slot,
		RentConfig:      structure.DefaultRentConfig,
		BuiltinPrograms: structure.DefaultBuiltinProgramIDs,
	}
	err = NewProgram(structure.DefaultBuiltinProgramIDs.BPFLoader, ovm.Runtime{}).Execute(context)
	if (err == nil) != ok {
		f.t.Fatalf("operation %d at slot %d: %v", instruction.Type, slot, err)
	}
	if !ok && !reflect.DeepEqual(before, f.accounts) {
		f.t.Fatalf("rejected operation %d changed accounts", instruction.Type)
	}
}

func (f *rwaFixture) setInvestor(owner, holding structure.PublicKey, approved bool, label string, slot uint64) {
	amount := uint64(0)
	if approved {
		amount = 1
	}
	f.call(true, f.issuer, AssetInstruction{Type: RWASetInvestor, Amount: amount, URI: rwaTestHash(label)}, slot, owner, holding)
}

func TestRWAShareLifecycleVotingAndDividend(t *testing.T) {
	f := newRWAFixture(t, RWAAssetShare)
	f.setInvestor(f.alice, f.aliceHolding, true, "alice-kyc", 11)
	f.setInvestor(f.bob, f.bobHolding, true, "bob-kyc", 11)
	f.setInvestor(f.outsider, f.otherHolding, false, "outsider-review", 11)
	f.call(true, f.issuer, AssetInstruction{Type: RWAIssue, Amount: 100}, 12, f.aliceHolding)
	f.call(true, f.alice, AssetInstruction{Type: RWATransfer, Amount: 40}, 13, f.aliceHolding, f.bobHolding)
	f.call(false, f.alice, AssetInstruction{Type: RWATransfer, Amount: 1}, 14, f.aliceHolding, f.otherHolding)

	f.call(true, f.issuer, AssetInstruction{Type: RWACreateProposal, Amount: 30, Decimals: 50, Name: "Approve audited acquisition", URI: rwaTestHash("proposal-1")}, 20, f.proposal)
	f.call(false, f.alice, AssetInstruction{Type: RWATransfer, Amount: 1}, 21, f.aliceHolding, f.bobHolding)
	f.call(true, f.alice, AssetInstruction{Type: RWAVote, Amount: uint64(RWAVoteYes)}, 22, f.aliceHolding, f.proposal, f.aliceVote)
	f.call(true, f.bob, AssetInstruction{Type: RWAVote, Amount: uint64(RWAVoteNo)}, 23, f.bobHolding, f.proposal, f.bobVote)
	f.call(false, f.alice, AssetInstruction{Type: RWAVote, Amount: uint64(RWAVoteNo)}, 24, f.aliceHolding, f.proposal, f.aliceVote)
	f.call(false, f.outsider, AssetInstruction{Type: RWAFinalizeProposal}, 29, f.proposal)
	f.call(true, f.outsider, AssetInstruction{Type: RWAFinalizeProposal}, 30, f.proposal)

	proposal, err := UnmarshalRWAProposal(f.accounts[f.proposal].Data)
	if err != nil || !proposal.Finalized || !proposal.Passed || proposal.Yes != 60 || proposal.No != 40 || proposal.SnapshotSupply != 100 {
		t.Fatalf("unexpected proposal: %+v, %v", proposal, err)
	}
	aliceBefore := f.accounts[f.alice].Lamports
	bobBefore := f.accounts[f.bob].Lamports
	issuerBefore := f.accounts[f.issuer].Lamports
	f.call(true, f.issuer, AssetInstruction{Type: RWACreateDividend, Amount: 1001, Name: "40", URI: rwaTestHash("dividend-1")}, 31, f.dividend)
	f.call(false, f.alice, AssetInstruction{Type: RWATransfer, Amount: 1}, 32, f.aliceHolding, f.bobHolding)
	f.call(true, f.alice, AssetInstruction{Type: RWAClaimDividend}, 33, f.aliceHolding, f.dividend, f.aliceClaim)
	f.call(false, f.alice, AssetInstruction{Type: RWAClaimDividend}, 34, f.aliceHolding, f.dividend, f.aliceClaim)
	f.call(true, f.bob, AssetInstruction{Type: RWAClaimDividend}, 35, f.bobHolding, f.dividend, f.bobClaim)
	f.call(false, f.outsider, AssetInstruction{Type: RWACloseDividend}, 39, f.dividend, f.issuer)
	f.call(true, f.outsider, AssetInstruction{Type: RWACloseDividend}, 40, f.dividend, f.issuer)

	aliceClaim, err := UnmarshalRWADividendClaim(f.accounts[f.aliceClaim].Data)
	if err != nil || aliceClaim.Amount != 600 {
		t.Fatalf("alice claim: %+v, %v", aliceClaim, err)
	}
	bobClaim, err := UnmarshalRWADividendClaim(f.accounts[f.bobClaim].Data)
	if err != nil || bobClaim.Amount != 400 {
		t.Fatalf("bob claim: %+v, %v", bobClaim, err)
	}
	dividend, err := UnmarshalRWADividend(f.accounts[f.dividend].Data)
	if err != nil || !dividend.Closed || dividend.Claimed != 1000 || dividend.Total != 1001 {
		t.Fatalf("dividend: %+v, %v", dividend, err)
	}
	if f.accounts[f.alice].Lamports != aliceBefore+600 || f.accounts[f.bob].Lamports != bobBefore+400 || f.accounts[f.issuer].Lamports != issuerBefore-1000 {
		t.Fatal("dividend lamport distribution is incorrect")
	}
	mint, err := UnmarshalRWAMint(f.accounts[f.mint].Data)
	if err != nil || mint.Supply != 100 || !mint.ActiveProposal.IsZero() || !mint.ActiveDividend.IsZero() {
		t.Fatalf("unexpected mint: %+v, %v", mint, err)
	}
}

func TestRWAComplianceFreezeForceTransferAndAuthorityRotation(t *testing.T) {
	f := newRWAFixture(t, RWAAssetShare)
	f.setInvestor(f.alice, f.aliceHolding, true, "alice-kyc", 11)
	f.setInvestor(f.bob, f.bobHolding, true, "bob-kyc", 11)
	f.setInvestor(f.outsider, f.otherHolding, false, "outsider-review", 11)
	f.call(true, f.issuer, AssetInstruction{Type: RWAIssue, Amount: 100}, 12, f.aliceHolding)
	f.call(true, f.issuer, AssetInstruction{Type: RWAFreezeInvestor, Amount: 1}, 13, f.aliceHolding)
	f.call(false, f.alice, AssetInstruction{Type: RWATransfer, Amount: 10}, 14, f.aliceHolding, f.bobHolding)
	f.call(false, f.issuer, AssetInstruction{Type: RWAForceTransfer, Amount: 10, URI: rwaTestHash("court-order")}, 15, f.aliceHolding, f.otherHolding)
	f.call(true, f.issuer, AssetInstruction{Type: RWAForceTransfer, Amount: 10, URI: rwaTestHash("court-order")}, 15, f.aliceHolding, f.bobHolding)
	f.call(true, f.issuer, AssetInstruction{Type: RWAPause, Amount: 1}, 16)
	f.call(false, f.issuer, AssetInstruction{Type: RWAForceTransfer, Amount: 1, URI: rwaTestHash("court-order-2")}, 17, f.aliceHolding, f.bobHolding)
	f.call(true, f.issuer, AssetInstruction{Type: RWAPause, Amount: 0}, 18)
	f.call(true, f.issuer, AssetInstruction{Type: RWAProposeAuthority}, 19, f.next)
	f.call(true, f.next, AssetInstruction{Type: RWAAcceptAuthority}, 20)
	f.call(false, f.issuer, AssetInstruction{Type: RWAPause, Amount: 1}, 21)
	f.call(true, f.next, AssetInstruction{Type: RWAPause, Amount: 1}, 21)

	alice, err := UnmarshalRWAHolding(f.accounts[f.aliceHolding].Data)
	if err != nil || alice.Amount != 90 || !alice.Frozen {
		t.Fatalf("alice holding: %+v, %v", alice, err)
	}
	bob, err := UnmarshalRWAHolding(f.accounts[f.bobHolding].Data)
	if err != nil || bob.Amount != 10 {
		t.Fatalf("bob holding: %+v, %v", bob, err)
	}
	mint, err := UnmarshalRWAMint(f.accounts[f.mint].Data)
	if err != nil || mint.Authority != f.next || !mint.Paused {
		t.Fatalf("mint authority: %+v, %v", mint, err)
	}
}

func TestRWANFTIsUniqueAndComplianceRestricted(t *testing.T) {
	f := newRWAFixture(t, RWAAssetNFT)
	f.setInvestor(f.alice, f.aliceHolding, true, "alice-kyc-nft", 11)
	f.setInvestor(f.bob, f.bobHolding, true, "bob-kyc-nft", 11)
	f.call(true, f.issuer, AssetInstruction{Type: RWAIssue, Amount: 1}, 12, f.aliceHolding)
	f.call(false, f.issuer, AssetInstruction{Type: RWAIssue, Amount: 1}, 13, f.bobHolding)
	f.call(false, f.alice, AssetInstruction{Type: RWATransfer, Amount: 2}, 14, f.aliceHolding, f.bobHolding)
	f.call(true, f.alice, AssetInstruction{Type: RWATransfer, Amount: 1}, 15, f.aliceHolding, f.bobHolding)
	f.call(false, f.issuer, AssetInstruction{Type: RWACreateProposal, Amount: 30, Decimals: 50, Name: "Invalid NFT vote", URI: rwaTestHash("proposal-1")}, 20, f.proposal)
	f.call(false, f.issuer, AssetInstruction{Type: RWACreateDividend, Amount: 1000, Name: "30", URI: rwaTestHash("dividend-1")}, 20, f.dividend)
	f.call(true, f.bob, AssetInstruction{Type: RWABurn, Amount: 1}, 21, f.bobHolding)
	mint, err := UnmarshalRWAMint(f.accounts[f.mint].Data)
	if err != nil || mint.AssetType != RWAAssetNFT || mint.Supply != 0 || mint.MaxSupply != 1 {
		t.Fatalf("unexpected NFT mint: %+v, %v", mint, err)
	}
}

func TestRWATemplateAndStrictStateCodecs(t *testing.T) {
	code, err := RWAProgramData()
	if err != nil {
		t.Fatal(err)
	}
	built, err := assembler.AssembleWithManifest("mov r9, 0x52574131\nsyscall asset_execute\nexit\n", ovm.ProgramManifest{ComputeUnitLimit: ovm.DefaultComputeUnitLimit, RequiredSyscalls: []ovm.SyscallID{ovm.SyscallAssetExecute}})
	if err != nil || !bytes.Equal(code, built) || IsStablecoinProgram(code) || IsFundraiserProgram(code) {
		t.Fatalf("RWA template mismatch: %v", err)
	}
	f := newRWAFixture(t, RWAAssetShare)
	f.setInvestor(f.alice, f.aliceHolding, true, "alice-kyc-codec", 11)
	for label, data := range map[string][]byte{"mint": f.accounts[f.mint].Data, "holding": f.accounts[f.aliceHolding].Data} {
		mutated := append(append([]byte(nil), data...), 0)
		var err error
		if label == "mint" {
			_, err = UnmarshalRWAMint(mutated)
		} else {
			_, err = UnmarshalRWAHolding(mutated)
		}
		if err == nil {
			t.Fatalf("%s codec accepted trailing bytes", label)
		}
	}
	if (AssetInstruction{Type: RWAInitialize, Amount: 2, Name: "Bad NFT", Symbol: "BADNFT", URI: "nft:" + rwaTestHash("bad")}).Validate() == nil {
		t.Fatal("NFT max supply greater than one accepted")
	}
}
