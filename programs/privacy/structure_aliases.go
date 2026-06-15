package privacy

import "solana_golang/structure"

type Hash = structure.Hash
type PublicKey = structure.PublicKey
type PrivacyAuditScope = structure.PrivacyAuditScope
type PrivacyInstructionType = structure.PrivacyInstructionType

const (
	MaxPrivacyAuditRecordsPerNote = structure.MaxPrivacyAuditRecordsPerNote
	PrivacyAuditScopeOwner        = structure.PrivacyAuditScopeOwner
	PrivacyAuditScopeBusiness     = structure.PrivacyAuditScopeBusiness
	PrivacyAuditScopeRegulatory   = structure.PrivacyAuditScopeRegulatory
	PrivacyInstructionDeposit     = structure.PrivacyInstructionDeposit
	PrivacyInstructionWithdraw    = structure.PrivacyInstructionWithdraw
	PrivacyInstructionTransfer    = structure.PrivacyInstructionTransfer
)

var (
	ErrInsufficientLamports      = structure.ErrInsufficientLamports
	ErrInvalidPrivacyInstruction = structure.ErrInvalidPrivacyInstruction
	NewHash                      = structure.NewHash
	NewPublicKey                 = structure.NewPublicKey
)
