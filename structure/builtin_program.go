package structure

import "fmt"

var DefaultBuiltinProgramIDs = BuiltinProgramIDs{
	System:             MustPublicKeyFromBase58("11111111111111111111111111111111"),
	ComputeBudget:      MustPublicKeyFromBase58("ComputeBudget111111111111111111111111111111"),
	AddressLookupTable: MustPublicKeyFromBase58("AddressLookupTab1e1111111111111111111111111"),
	NativeLoader:       MustPublicKeyFromBase58("NativeLoader1111111111111111111111111111111"),
	BPFLoader:          MustPublicKeyFromBase58("BPFLoader2111111111111111111111111111111111"),
	Vote:               MustPublicKeyFromBase58("Vote111111111111111111111111111111111111111"),
	Token:              MustPublicKeyFromBase58("TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"),
	Stake:              MustPublicKeyFromBase58("Stake11111111111111111111111111111111111111"),
	Config:             MustPublicKeyFromBase58("Config1111111111111111111111111111111111111"),
	Shielded:           PublicKey{0x53, 0x68, 0x69, 0x65, 0x6c, 0x64, 0x65, 0x64, 0x50, 0x6f, 0x6f, 0x6c, 0x50, 0x72, 0x6f, 0x67, 0x72, 0x61, 0x6d, 0x56, 0x32, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2},
}

// BuiltinProgramIDs 描述内置程序地址集合 + 统一执行层和指令构造使用的程序 ID。
type BuiltinProgramIDs struct {
	System             PublicKey
	ComputeBudget      PublicKey
	AddressLookupTable PublicKey
	NativeLoader       PublicKey
	BPFLoader          PublicKey
	Vote               PublicKey
	Token              PublicKey
	Stake              PublicKey
	Config             PublicKey
	Shielded           PublicKey
}

// Validate 校验内置程序集合 + 防止启动时使用缺失的程序 ID。
func (programIDs BuiltinProgramIDs) Validate() error {
	if programIDs.ComputeBudget.IsZero() {
		return fmt.Errorf("%w: compute budget program is empty", ErrInvalidAccount)
	}
	if programIDs.AddressLookupTable.IsZero() {
		return fmt.Errorf("%w: address lookup table program is empty", ErrInvalidAccount)
	}
	if programIDs.NativeLoader.IsZero() {
		return fmt.Errorf("%w: native loader program is empty", ErrInvalidAccount)
	}
	if programIDs.BPFLoader.IsZero() {
		return fmt.Errorf("%w: bpf loader program is empty", ErrInvalidAccount)
	}
	if programIDs.Vote.IsZero() {
		return fmt.Errorf("%w: vote program is empty", ErrInvalidAccount)
	}
	if programIDs.Token.IsZero() {
		return fmt.Errorf("%w: token program is empty", ErrInvalidAccount)
	}
	if programIDs.Stake.IsZero() {
		return fmt.Errorf("%w: stake program is empty", ErrInvalidAccount)
	}
	if programIDs.Config.IsZero() {
		return fmt.Errorf("%w: config program is empty", ErrInvalidAccount)
	}
	if programIDs.Shielded.IsZero() {
		return fmt.Errorf("%w: shielded program is empty", ErrInvalidAccount)
	}
	return nil
}

// IsBuiltinProgram 判断是否内置程序 + 为加载账户和执行分发提供快速判断。
func (programIDs BuiltinProgramIDs) IsBuiltinProgram(programID PublicKey) bool {
	return programID == programIDs.System ||
		programID == programIDs.ComputeBudget ||
		programID == programIDs.AddressLookupTable ||
		programID == programIDs.NativeLoader ||
		programID == programIDs.BPFLoader ||
		programID == programIDs.Vote ||
		programID == programIDs.Token ||
		programID == programIDs.Stake ||
		programID == programIDs.Config ||
		programID == programIDs.Shielded
}
