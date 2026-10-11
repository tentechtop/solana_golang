package vmprogram

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/tentechtop/ordexus-core/runtime"
	"github.com/tentechtop/ordexus-core/structure"
	ovm "github.com/tentechtop/ordexus-core/vm"
)

const (
	RewardInitialize AssetInstructionType = 400 + iota
	RewardClaim
	RewardConfigKind  = 64
	RewardReceiptKind = 65
)

// UTC dates and content completion are attested by the configured oracle. Consensus
// has a slot clock, not a UTC clock. Slots bound voucher lifetime; receipts enforce
// one claim per wallet, attested UTC day and action. No oracle key can mint directly.
type RewardConfig struct {
	Issuer, Oracle, Mint                         structure.PublicKey
	Cap, SignInReward, VideoReward, TotalClaimed uint64
}
type RewardReceipt struct {
	Config, Owner structure.PublicKey
	Day           uint64
	Flags         uint8
}

func RewardProgramData() ([]byte, error) {
	tag, err := ovm.BuildRegisterInstruction(ovm.RegOpMovImm, 9, 0, 0, 0x52574431)
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
func IsRewardProgram(data []byte) bool {
	code, err := RewardProgramData()
	return err == nil && ovm.BytecodeProgramsEqual(code, data)
}
func RewardConfigAddress(program structure.PublicKey) structure.PublicKey {
	return StablecoinAddress("nulla.reward.config.v1", program.String())
}
func RewardReceiptAddress(program, owner structure.PublicKey) structure.PublicKey {
	return StablecoinAddress("nulla.reward.receipt.v1", program.String(), owner.String())
}
func RewardMintAddress(program structure.PublicKey) structure.PublicKey {
	return StablecoinAddress("nulla.asset.mint.v1", program.String())
}
func RewardBalanceAddress(program, owner structure.PublicKey) structure.PublicKey {
	return StablecoinAddress("nulla.asset.balance.v1", program.String(), owner.String())
}
func rewardNumber(s string) (uint64, error) {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil || strconv.FormatUint(n, 10) != s {
		return 0, fmt.Errorf("reward: invalid integer")
	}
	return n, nil
}
func rewardSettings(uri string) (structure.PublicKey, uint64, uint64, error) {
	p := strings.Split(uri, "|")
	if len(p) != 3 {
		return structure.PublicKey{}, 0, 0, fmt.Errorf("reward: invalid oracle settings")
	}
	key, err := structure.PublicKeyFromBase58(p[0])
	a, e1 := rewardNumber(p[1])
	b, e2 := rewardNumber(p[2])
	if err != nil || key.IsZero() || e1 != nil || e2 != nil || a == 0 || b == 0 {
		return key, 0, 0, fmt.Errorf("reward: invalid oracle or rewards")
	}
	return key, a, b, nil
}
func validateRewardInstruction(i AssetInstruction) error {
	switch i.Type {
	case RewardInitialize:
		_, a, b, err := rewardSettings(i.URI)
		if err != nil {
			return err
		}
		if i.Decimals != 6 || i.Amount < a || i.Amount < b || !utf8.ValidString(i.Name) || strings.TrimSpace(i.Name) == "" || strings.TrimSpace(i.Symbol) == "" {
			return fmt.Errorf("reward: invalid issuance settings")
		}
	case RewardClaim:
		if i.Amount == 0 || (i.Decimals != 1 && i.Decimals != 2) || i.Symbol != "" {
			return fmt.Errorf("reward: invalid day or action")
		}
		if _, err := rewardNumber(i.Name); err != nil {
			return err
		}
		sig, err := base64.StdEncoding.Strict().DecodeString(i.URI)
		if err != nil || len(sig) != ed25519.SignatureSize {
			return fmt.Errorf("reward: invalid voucher signature")
		}
	default:
		return fmt.Errorf("reward: unknown operation")
	}
	return nil
}
func (s RewardConfig) MarshalBinary() ([]byte, error) {
	if s.Issuer.IsZero() || s.Oracle.IsZero() || s.Mint.IsZero() || s.SignInReward == 0 || s.VideoReward == 0 || s.Cap < s.SignInReward || s.Cap < s.VideoReward || s.TotalClaimed > s.Cap {
		return nil, fmt.Errorf("reward: invalid configuration")
	}
	w := stableWriter(RewardConfigKind)
	for _, key := range []structure.PublicKey{s.Issuer, s.Oracle, s.Mint} {
		w.WriteFixedBytes(key[:])
	}
	for _, n := range []uint64{s.Cap, s.SignInReward, s.VideoReward, s.TotalClaimed} {
		w.WriteUint64(n)
	}
	return w.Bytes(), nil
}
func UnmarshalRewardConfig(data []byte) (RewardConfig, error) {
	r := newStableReader(data, RewardConfigKind)
	s := RewardConfig{Issuer: r.key(), Oracle: r.key(), Mint: r.key(), Cap: r.u64(), SignInReward: r.u64(), VideoReward: r.u64(), TotalClaimed: r.u64()}
	if err := r.done(); err != nil {
		return s, err
	}
	_, err := s.MarshalBinary()
	return s, err
}
func (s RewardReceipt) MarshalBinary() ([]byte, error) {
	if s.Config.IsZero() || s.Owner.IsZero() || s.Day == 0 || s.Flags == 0 || s.Flags > 3 {
		return nil, fmt.Errorf("reward: invalid receipt")
	}
	w := stableWriter(RewardReceiptKind)
	w.WriteFixedBytes(s.Config[:])
	w.WriteFixedBytes(s.Owner[:])
	w.WriteUint64(s.Day)
	w.WriteUint8(s.Flags)
	return w.Bytes(), nil
}
func UnmarshalRewardReceipt(data []byte) (RewardReceipt, error) {
	r := newStableReader(data, RewardReceiptKind)
	s := RewardReceipt{Config: r.key(), Owner: r.key(), Day: r.u64(), Flags: r.u8()}
	if err := r.done(); err != nil {
		return s, err
	}
	_, err := s.MarshalBinary()
	return s, err
}
func RewardVoucherMessage(program, owner structure.PublicKey, day uint64, action uint8, expirySlot uint64) []byte {
	out := append([]byte("nulla.reward.v1"), program[:]...)
	out = append(out, owner[:]...)
	out = binary.LittleEndian.AppendUint64(out, day)
	out = append(out, action)
	return binary.LittleEndian.AppendUint64(out, expirySlot)
}
func executeRewardInstruction(i AssetInstruction, c runtime.InstructionContext, accounts map[structure.PublicKey]structure.Account) error {
	program, err := assetVMProgramID(c)
	if err != nil {
		return err
	}
	if !IsRewardProgram(accounts[program].Data) {
		return fmt.Errorf("reward: immutable template required")
	}
	// Ordinary mint initialization and mint_to are deliberately unavailable.
	if i.Type == AssetInstructionInitializeAccount || i.Type == AssetInstructionTransfer || i.Type == AssetInstructionBurn || i.Type == AssetInstructionApprove || i.Type == AssetInstructionTransferFrom {
		return executeAssetInstruction(i, c, accounts)
	}
	if err := validateRewardInstruction(i); err != nil {
		return err
	}
	count := 5
	if i.Type == RewardInitialize {
		count = 3
	}
	if len(c.Instruction.AccountIndexes) != count {
		return fmt.Errorf("reward: incorrect account count")
	}
	keys := make([]structure.PublicKey, count)
	for index := range keys {
		keys[index], err = assetAccountAddressAt(c, index)
		if err != nil {
			return err
		}
		for j := 0; j < index; j++ {
			if keys[j] == keys[index] {
				return fmt.Errorf("reward: duplicate account")
			}
		}
		if err := requireAssetWritableAddress(c, keys[index], "reward"); err != nil {
			return err
		}
	}
	actor, configKey, mintKey := keys[0], keys[1], keys[2]
	if err := requireTransactionSigner(actor, c, "reward claimant"); err != nil {
		return err
	}
	if configKey != RewardConfigAddress(program) || mintKey != RewardMintAddress(program) {
		return fmt.Errorf("reward: noncanonical configuration or mint")
	}
	configAccount, err := assetOwnedWritableAccount("reward config", configKey, c, accounts)
	if err != nil {
		return err
	}
	mintAccount, err := assetOwnedWritableAccount("reward mint", mintKey, c, accounts)
	if err != nil {
		return err
	}
	if i.Type == RewardInitialize {
		if len(configAccount.Data) != 0 || len(mintAccount.Data) != 0 {
			return fmt.Errorf("reward: already initialized")
		}
		oracle, a, b, _ := rewardSettings(i.URI)
		s := RewardConfig{Issuer: actor, Oracle: oracle, Mint: mintKey, Cap: i.Amount, SignInReward: a, VideoReward: b}
		mint := AssetMintState{Version: assetStateVersion, Kind: AssetKindFungible, Decimals: 6, Authority: configKey, MaxSupply: i.Amount, Name: i.Name, Symbol: i.Symbol}
		if err := writeAssetAccount(configKey, configAccount, s.MarshalBinary, c, accounts); err != nil {
			return err
		}
		return writeAssetAccount(mintKey, mintAccount, mint.MarshalBinary, c, accounts)
	}
	s, err := UnmarshalRewardConfig(configAccount.Data)
	if err != nil {
		return err
	}
	mint, err := UnmarshalAssetMintStateBinary(mintAccount.Data)
	if err != nil {
		return err
	}
	if s.Mint != mintKey || mint.Authority != configKey || mint.Kind != AssetKindFungible || mint.Decimals != 6 || mint.MaxSupply != s.Cap {
		return fmt.Errorf("reward: mint binding mismatch")
	}
	expiry, _ := rewardNumber(i.Name)
	sig, _ := base64.StdEncoding.Strict().DecodeString(i.URI)
	if c.CurrentSlot > expiry || !ed25519.Verify(s.Oracle[:], RewardVoucherMessage(program, actor, i.Amount, i.Decimals, expiry), sig) {
		return fmt.Errorf("reward: invalid or expired voucher")
	}
	balanceKey, receiptKey := keys[3], keys[4]
	if balanceKey != RewardBalanceAddress(program, actor) || receiptKey != RewardReceiptAddress(program, actor) {
		return fmt.Errorf("reward: noncanonical claimant accounts")
	}
	ba, err := assetOwnedWritableAccount("reward balance", balanceKey, c, accounts)
	if err != nil {
		return err
	}
	ra, err := assetOwnedWritableAccount("reward receipt", receiptKey, c, accounts)
	if err != nil {
		return err
	}
	b := AssetBalanceState{Version: assetStateVersion, Mint: mintKey, Owner: actor}
	if len(ba.Data) > 0 {
		b, err = UnmarshalAssetBalanceStateBinary(ba.Data)
		if err != nil {
			return err
		}
	}
	if b.Mint != mintKey || b.Owner != actor {
		return fmt.Errorf("reward: balance binding mismatch")
	}
	r := RewardReceipt{Config: configKey, Owner: actor, Day: i.Amount}
	if len(ra.Data) > 0 {
		r, err = UnmarshalRewardReceipt(ra.Data)
		if err != nil {
			return err
		}
		if r.Config != configKey || r.Owner != actor || i.Amount < r.Day || (i.Amount == r.Day && r.Flags&i.Decimals != 0) {
			return fmt.Errorf("reward: already claimed or old day")
		}
		if i.Amount > r.Day {
			r.Day = i.Amount
			r.Flags = 0
		}
	}
	amount := s.SignInReward
	if i.Decimals == 2 {
		amount = s.VideoReward
	}
	if amount > s.Cap-s.TotalClaimed || mint.Supply > math.MaxUint64-amount || b.Amount > math.MaxUint64-amount {
		return fmt.Errorf("reward: campaign exhausted or balance overflow")
	}
	s.TotalClaimed += amount
	mint.Supply += amount
	b.Amount += amount
	r.Flags |= i.Decimals
	if err := writeAssetAccount(configKey, configAccount, s.MarshalBinary, c, accounts); err != nil {
		return err
	}
	if err := writeAssetAccount(mintKey, mintAccount, mint.MarshalBinary, c, accounts); err != nil {
		return err
	}
	if err := writeAssetAccount(balanceKey, ba, b.MarshalBinary, c, accounts); err != nil {
		return err
	}
	return writeAssetAccount(receiptKey, ra, r.MarshalBinary, c, accounts)
}
