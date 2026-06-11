package structure

import "fmt"

var DefaultBuiltinProgramIDs = BuiltinProgramIDs{
	System:             MustPublicKeyFromBase58("11111111111111111111111111111111"),
	ComputeBudget:      MustPublicKeyFromBase58("ComputeBudget111111111111111111111111111111"),
	AddressLookupTable: MustPublicKeyFromBase58("AddressLookupTab1e1111111111111111111111111"),
	NativeLoader:       MustPublicKeyFromBase58("NativeLoader1111111111111111111111111111111"),
	Vote:               MustPublicKeyFromBase58("Vote111111111111111111111111111111111111111"),
	Stake:              MustPublicKeyFromBase58("Stake11111111111111111111111111111111111111"),
	Config:             MustPublicKeyFromBase58("Config1111111111111111111111111111111111111"),
}

// BuiltinProgramIDs 描述内置程序地址集合 + 统一执行层和指令构造使用的程序 ID。
type BuiltinProgramIDs struct {
	System             PublicKey
	ComputeBudget      PublicKey
	AddressLookupTable PublicKey
	NativeLoader       PublicKey
	Vote               PublicKey
	Stake              PublicKey
	Config             PublicKey
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
	if programIDs.Vote.IsZero() {
		return fmt.Errorf("%w: vote program is empty", ErrInvalidAccount)
	}
	if programIDs.Stake.IsZero() {
		return fmt.Errorf("%w: stake program is empty", ErrInvalidAccount)
	}
	if programIDs.Config.IsZero() {
		return fmt.Errorf("%w: config program is empty", ErrInvalidAccount)
	}
	return nil
}

// IsBuiltinProgram 判断是否内置程序 + 为加载账户和执行分发提供快速判断。
func (programIDs BuiltinProgramIDs) IsBuiltinProgram(programID PublicKey) bool {
	return programID == programIDs.System ||
		programID == programIDs.ComputeBudget ||
		programID == programIDs.AddressLookupTable ||
		programID == programIDs.NativeLoader ||
		programID == programIDs.Vote ||
		programID == programIDs.Stake ||
		programID == programIDs.Config
}
