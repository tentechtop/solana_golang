package rpc

import (
	"context"
	"encoding/json"
	"fmt"
)

const (
	MethodGetBalance            = "getBalance"
	MethodGetAccountType        = "getAccountType"
	MethodSendTransaction       = "sendTransaction"
	MethodGetBlock              = "getBlock"
	MethodTreasuryTransfer      = "treasuryTransfer"
	MethodTransfer              = "transfer"
	MethodGetPrivacyState       = "getPrivacyState"
	MethodPrivacyDeposit        = "privacyDeposit"
	MethodPrivacyDepositToState = "privacyDepositToState"
	MethodPrivacyWithdraw       = "privacyWithdraw"
	MethodPrivacyTransfer       = "privacyTransfer"
	MethodPrivacyAuthorizeAudit = "privacyAuthorizeAudit"
	MethodRegisterValidator     = "registerValidator"
	MethodStake                 = "stake"
	MethodUnstake               = "unstake"
	MethodSlashValidator        = "slashValidator"
	MethodJailValidator         = "jailValidator"
	MethodGetValidatorSet       = "getValidatorSet"
	MethodGetNodeStatus         = "getNodeStatus"
	MethodGetConsensusStatus    = "getConsensusStatus"
	MethodGetMetrics            = "getMetrics"
	MethodGetHealth             = "getHealth"
)

// LedgerBackend 定义链业务后端 + 让 RPC 层只负责协议转换和参数校验。
type LedgerBackend interface {
	GetBalance(ctx context.Context, address string) (BalanceResult, error)
	SendTransaction(ctx context.Context, encodedTransaction string) (string, error)
	GetBlock(ctx context.Context, slot uint64) (BlockResult, error)
}

type TreasuryTransferBackend interface {
	TreasuryTransfer(ctx context.Context, destination string, lamports uint64) (string, error)
}

type TransferBackend interface {
	Transfer(ctx context.Context, sourceSeed string, destination string, lamports uint64) (string, error)
}

type AccountTypeBackend interface {
	GetAccountType(ctx context.Context, address string) (AccountTypeResult, error)
}

type PrivacyBackend interface {
	GetPrivacyState(ctx context.Context, stateAddress string) (PrivacyStateResult, error)
	PrivacyDeposit(ctx context.Context, sourceSeed string, stateSeed string, lamports uint64, auditor string, auditSecret string, expiresAtSlot uint64) (PrivacyTransactionResult, error)
	PrivacyDepositToState(ctx context.Context, sourceSeed string, stateAddress string, lamports uint64, auditor string, auditSecret string, expiresAtSlot uint64) (PrivacyTransactionResult, error)
	PrivacyWithdraw(ctx context.Context, authoritySeed string, stateAddress string, destination string, commitment string, nullifier string, lamports uint64, auditor string, auditSecret string, expiresAtSlot uint64) (PrivacyTransactionResult, error)
	PrivacyTransfer(ctx context.Context, authoritySeed string, stateAddress string, commitment string, nullifier string, recipient string, lamports uint64, auditor string, auditSecret string, expiresAtSlot uint64) (PrivacyTransactionResult, error)
	PrivacyAuthorizeAudit(ctx context.Context, authoritySeed string, stateAddress string, commitment string, auditor string, auditSecret string, scope uint8, expiresAtSlot uint64) (PrivacyTransactionResult, error)
}

type ValidatorJoinBackend interface {
	RegisterValidator(ctx context.Context, stakerSeed string, validatorSeed string, consensusSeed string, peerID string, stakeLamports uint64) (string, error)
	Stake(ctx context.Context, stakerSeed string, validatorAddress string, lamports uint64) (string, error)
	Unstake(ctx context.Context, stakerSeed string, validatorAddress string, lamports uint64, unlockEpoch uint64) (string, error)
}

type PunishmentBackend interface {
	SlashValidator(ctx context.Context, stakerSeed string, validatorAddress string, lamports uint64) (string, error)
	JailValidator(ctx context.Context, stakerSeed string, validatorAddress string, jailUntilEpoch uint64) (string, error)
}

type ValidatorSetBackend interface {
	GetValidatorSet(ctx context.Context) (ValidatorSetResult, error)
}

type NodeStatusBackend interface {
	GetNodeStatus(ctx context.Context) (any, error)
	GetHealth(ctx context.Context) (HealthResult, error)
}

type ConsensusStatusBackend interface {
	GetConsensusStatus(ctx context.Context) (any, error)
}

type MetricsBackend interface {
	GetMetrics(ctx context.Context) (any, error)
}

type BalanceResult struct {
	Value uint64 `json:"value"`
}

type AccountTypeResult struct {
	Address string `json:"address"`
	Exists  bool   `json:"exists"`
	Owner   string `json:"owner,omitempty"`
	Type    string `json:"type"`
}

type BlockResult struct {
	Slot         uint64 `json:"slot"`
	Blockhash    string `json:"blockhash,omitempty"`
	ParentSlot   uint64 `json:"parentSlot,omitempty"`
	Transactions []any  `json:"transactions,omitempty"`
}

type TransactionSubmitResult struct {
	Signature string `json:"signature"`
}

type PrivacyTransactionResult struct {
	Signature        string `json:"signature"`
	PrivacyState     string `json:"privacy_state"`
	Commitment       string `json:"commitment,omitempty"`
	Nullifier        string `json:"nullifier,omitempty"`
	OutputCommitment string `json:"output_commitment,omitempty"`
}

type PrivacyAuditRecordResult struct {
	Auditor       string `json:"auditor"`
	Scope         uint8  `json:"scope"`
	ExpiresAtSlot uint64 `json:"expires_at_slot"`
	Ciphertext    string `json:"ciphertext"`
}

type PrivacyNoteResult struct {
	Commitment     string                     `json:"commitment"`
	SpendAuthority string                     `json:"spend_authority"`
	Amount         uint64                     `json:"amount"`
	Spent          bool                       `json:"spent"`
	SpentSlot      uint64                     `json:"spent_slot"`
	SpendNullifier string                     `json:"spend_nullifier,omitempty"`
	AuditRecords   []PrivacyAuditRecordResult `json:"audit_records"`
}

type PrivacyStateResult struct {
	Address         string              `json:"address"`
	Version         uint16              `json:"version"`
	Notes           []PrivacyNoteResult `json:"notes"`
	SpentNullifiers []string            `json:"spent_nullifiers"`
}

type ValidatorInfo struct {
	ValidatorID        string `json:"validator_id"`
	AccountAddress     string `json:"account_address"`
	ConsensusPublicKey string `json:"consensus_public_key"`
	P2PPeerID          string `json:"p2p_peer_id"`
	StakeLamports      uint64 `json:"stake_lamports"`
	Status             string `json:"status"`
	CommissionBps      uint16 `json:"commission_bps"`
}

type ValidatorSetResult struct {
	Validators []ValidatorInfo `json:"validators"`
}

type HealthResult struct {
	OK              bool   `json:"ok"`
	HeadHeight      uint64 `json:"head_height"`
	HeadSlot        uint64 `json:"head_slot"`
	FinalizedHeight uint64 `json:"finalized_height"`
	MempoolSize     int    `json:"mempool_size"`
}

func RegisterDefaultHandlers(router *Router, backend LedgerBackend) {
	_ = router.Register(MethodGetBalance, getBalanceHandler(backend))
	_ = router.Register(MethodGetAccountType, getAccountTypeHandler(backend))
	_ = router.Register(MethodSendTransaction, sendTransactionHandler(backend))
	_ = router.Register(MethodGetBlock, getBlockHandler(backend))
	_ = router.Register(MethodTreasuryTransfer, treasuryTransferHandler(backend))
	_ = router.Register(MethodTransfer, transferHandler(backend))
	_ = router.Register(MethodGetPrivacyState, getPrivacyStateHandler(backend))
	_ = router.Register(MethodPrivacyDeposit, privacyDepositHandler(backend))
	_ = router.Register(MethodPrivacyDepositToState, privacyDepositToStateHandler(backend))
	_ = router.Register(MethodPrivacyWithdraw, privacyWithdrawHandler(backend))
	_ = router.Register(MethodPrivacyTransfer, privacyTransferHandler(backend))
	_ = router.Register(MethodPrivacyAuthorizeAudit, privacyAuthorizeAuditHandler(backend))
	_ = router.Register(MethodRegisterValidator, registerValidatorHandler(backend))
	_ = router.Register(MethodStake, stakeHandler(backend))
	_ = router.Register(MethodUnstake, unstakeHandler(backend))
	_ = router.Register(MethodSlashValidator, slashValidatorHandler(backend))
	_ = router.Register(MethodJailValidator, jailValidatorHandler(backend))
	_ = router.Register(MethodGetValidatorSet, getValidatorSetHandler(backend))
	_ = router.Register(MethodGetNodeStatus, getNodeStatusHandler(backend))
	_ = router.Register(MethodGetConsensusStatus, getConsensusStatusHandler(backend))
	_ = router.Register(MethodGetMetrics, getMetricsHandler(backend))
	_ = router.Register(MethodGetHealth, getHealthHandler(backend))
}
func getBalanceHandler(backend LedgerBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		if backend == nil {
			return nil, ErrMethodUnavailable
		}
		requestParams, rpcError := parseGetBalanceParams(params)
		if rpcError != nil {
			return nil, rpcError
		}
		result, err := backend.GetBalance(ctx, requestParams.Address)
		if err != nil {
			return nil, internalError(fmt.Sprintf("get balance: %v", err))
		}
		return result, nil
	}
}

func getAccountTypeHandler(backend LedgerBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		accountTypeBackend, ok := backend.(AccountTypeBackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		values, rpcError := parseParamsArray(params)
		if rpcError != nil {
			return nil, rpcError
		}
		if len(values) < 1 {
			return nil, invalidParamsError("getAccountType requires address")
		}
		address, rpcError := parseStringParam(values[0], "getAccountType address")
		if rpcError != nil {
			return nil, rpcError
		}
		result, err := accountTypeBackend.GetAccountType(ctx, address)
		if err != nil {
			return nil, internalError(fmt.Sprintf("get account type: %v", err))
		}
		return result, nil
	}
}

func sendTransactionHandler(backend LedgerBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		if backend == nil {
			return nil, ErrMethodUnavailable
		}
		requestParams, rpcError := parseSendTransactionParams(params)
		if rpcError != nil {
			return nil, rpcError
		}
		signature, err := backend.SendTransaction(ctx, requestParams.EncodedTransaction)
		if err != nil {
			return nil, internalError(fmt.Sprintf("send transaction: %v", err))
		}
		return signature, nil
	}
}
func getBlockHandler(backend LedgerBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		if backend == nil {
			return nil, ErrMethodUnavailable
		}
		requestParams, rpcError := parseGetBlockParams(params)
		if rpcError != nil {
			return nil, rpcError
		}
		result, err := backend.GetBlock(ctx, requestParams.Slot)
		if err != nil {
			return nil, internalError(fmt.Sprintf("get block: %v", err))
		}
		return result, nil
	}
}

func treasuryTransferHandler(backend LedgerBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		transferBackend, ok := backend.(TreasuryTransferBackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		values, rpcError := parseParamsArray(params)
		if rpcError != nil {
			return nil, rpcError
		}
		destination, lamports, rpcError := parseAddressAmount(values, "treasuryTransfer")
		if rpcError != nil {
			return nil, rpcError
		}
		signature, err := transferBackend.TreasuryTransfer(ctx, destination, lamports)
		if err != nil {
			return nil, internalError(fmt.Sprintf("treasury transfer: %v", err))
		}
		return TransactionSubmitResult{Signature: signature}, nil
	}
}

func transferHandler(backend LedgerBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		transferBackend, ok := backend.(TransferBackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		values, rpcError := parseParamsArray(params)
		if rpcError != nil {
			return nil, rpcError
		}
		if len(values) < 3 {
			return nil, invalidParamsError("transfer requires source seed, destination, lamports")
		}
		sourceSeed, rpcError := parseStringParam(values[0], "transfer source seed")
		if rpcError != nil {
			return nil, rpcError
		}
		destination, rpcError := parseStringParam(values[1], "transfer destination")
		if rpcError != nil {
			return nil, rpcError
		}
		lamports, rpcError := parseUint64Param(values[2], "transfer lamports")
		if rpcError != nil {
			return nil, rpcError
		}
		signature, err := transferBackend.Transfer(ctx, sourceSeed, destination, lamports)
		if err != nil {
			return nil, internalError(fmt.Sprintf("transfer: %v", err))
		}
		return TransactionSubmitResult{Signature: signature}, nil
	}
}

func getPrivacyStateHandler(backend LedgerBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		privacyBackend, ok := backend.(PrivacyBackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		values, rpcError := parseParamsArray(params)
		if rpcError != nil {
			return nil, rpcError
		}
		if len(values) < 1 {
			return nil, invalidParamsError("getPrivacyState requires state address")
		}
		stateAddress, rpcError := parseStringParam(values[0], "getPrivacyState state address")
		if rpcError != nil {
			return nil, rpcError
		}
		result, err := privacyBackend.GetPrivacyState(ctx, stateAddress)
		if err != nil {
			return nil, internalError(fmt.Sprintf("get privacy state: %v", err))
		}
		return result, nil
	}
}

func privacyDepositHandler(backend LedgerBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		privacyBackend, ok := backend.(PrivacyBackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		values, rpcError := parseParamsArray(params)
		if rpcError != nil {
			return nil, rpcError
		}
		sourceSeed, stateSeed, lamports, auditor, auditSecret, expiresAtSlot, rpcError := parsePrivacyDepositParams(values)
		if rpcError != nil {
			return nil, rpcError
		}
		result, err := privacyBackend.PrivacyDeposit(ctx, sourceSeed, stateSeed, lamports, auditor, auditSecret, expiresAtSlot)
		if err != nil {
			return nil, internalError(fmt.Sprintf("privacy deposit: %v", err))
		}
		return result, nil
	}
}

func privacyDepositToStateHandler(backend LedgerBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		privacyBackend, ok := backend.(PrivacyBackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		values, rpcError := parseParamsArray(params)
		if rpcError != nil {
			return nil, rpcError
		}
		sourceSeed, stateAddress, lamports, auditor, auditSecret, expiresAtSlot, rpcError := parsePrivacyDepositParams(values)
		if rpcError != nil {
			return nil, rpcError
		}
		result, err := privacyBackend.PrivacyDepositToState(ctx, sourceSeed, stateAddress, lamports, auditor, auditSecret, expiresAtSlot)
		if err != nil {
			return nil, internalError(fmt.Sprintf("privacy deposit to state: %v", err))
		}
		return result, nil
	}
}

func privacyWithdrawHandler(backend LedgerBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		privacyBackend, ok := backend.(PrivacyBackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		values, rpcError := parseParamsArray(params)
		if rpcError != nil {
			return nil, rpcError
		}
		parsed, rpcError := parsePrivacySpendParams(values, "privacyWithdraw")
		if rpcError != nil {
			return nil, rpcError
		}
		result, err := privacyBackend.PrivacyWithdraw(ctx, parsed.AuthoritySeed, parsed.StateAddress, parsed.DestinationOrRecipient, parsed.Commitment, parsed.Nullifier, parsed.Lamports, parsed.Auditor, parsed.AuditSecret, parsed.ExpiresAtSlot)
		if err != nil {
			return nil, internalError(fmt.Sprintf("privacy withdraw: %v", err))
		}
		return result, nil
	}
}

func privacyTransferHandler(backend LedgerBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		privacyBackend, ok := backend.(PrivacyBackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		values, rpcError := parseParamsArray(params)
		if rpcError != nil {
			return nil, rpcError
		}
		parsed, rpcError := parsePrivacySpendParams(values, "privacyTransfer")
		if rpcError != nil {
			return nil, rpcError
		}
		result, err := privacyBackend.PrivacyTransfer(ctx, parsed.AuthoritySeed, parsed.StateAddress, parsed.Commitment, parsed.Nullifier, parsed.DestinationOrRecipient, parsed.Lamports, parsed.Auditor, parsed.AuditSecret, parsed.ExpiresAtSlot)
		if err != nil {
			return nil, internalError(fmt.Sprintf("privacy transfer: %v", err))
		}
		return result, nil
	}
}

func privacyAuthorizeAuditHandler(backend LedgerBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		privacyBackend, ok := backend.(PrivacyBackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		values, rpcError := parseParamsArray(params)
		if rpcError != nil {
			return nil, rpcError
		}
		authoritySeed, stateAddress, commitment, auditor, auditSecret, scope, expiresAtSlot, rpcError := parsePrivacyAuthorizeAuditParams(values)
		if rpcError != nil {
			return nil, rpcError
		}
		result, err := privacyBackend.PrivacyAuthorizeAudit(ctx, authoritySeed, stateAddress, commitment, auditor, auditSecret, scope, expiresAtSlot)
		if err != nil {
			return nil, internalError(fmt.Sprintf("privacy authorize audit: %v", err))
		}
		return result, nil
	}
}

func registerValidatorHandler(backend LedgerBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		joinBackend, ok := backend.(ValidatorJoinBackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		values, rpcError := parseParamsArray(params)
		if rpcError != nil {
			return nil, rpcError
		}
		if len(values) < 5 {
			return nil, invalidParamsError("registerValidator requires staker seed, validator seed, consensus seed, peer id, stake lamports")
		}
		stakerSeed, rpcError := parseStringParam(values[0], "registerValidator staker seed")
		if rpcError != nil {
			return nil, rpcError
		}
		validatorSeed, rpcError := parseStringParam(values[1], "registerValidator validator seed")
		if rpcError != nil {
			return nil, rpcError
		}
		consensusSeed, rpcError := parseStringParam(values[2], "registerValidator consensus seed")
		if rpcError != nil {
			return nil, rpcError
		}
		peerID, rpcError := parseStringParam(values[3], "registerValidator peer id")
		if rpcError != nil {
			return nil, rpcError
		}
		stakeLamports, rpcError := parseUint64Param(values[4], "registerValidator stake lamports")
		if rpcError != nil {
			return nil, rpcError
		}
		signature, err := joinBackend.RegisterValidator(ctx, stakerSeed, validatorSeed, consensusSeed, peerID, stakeLamports)
		if err != nil {
			return nil, internalError(fmt.Sprintf("register validator: %v", err))
		}
		return TransactionSubmitResult{Signature: signature}, nil
	}
}

func stakeHandler(backend LedgerBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		joinBackend, ok := backend.(ValidatorJoinBackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		values, rpcError := parseParamsArray(params)
		if rpcError != nil {
			return nil, rpcError
		}
		stakerSeed, validatorAddress, lamports, rpcError := parseSeedValidatorAmount(values, "stake")
		if rpcError != nil {
			return nil, rpcError
		}
		signature, err := joinBackend.Stake(ctx, stakerSeed, validatorAddress, lamports)
		if err != nil {
			return nil, internalError(fmt.Sprintf("stake: %v", err))
		}
		return TransactionSubmitResult{Signature: signature}, nil
	}
}

func unstakeHandler(backend LedgerBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		joinBackend, ok := backend.(ValidatorJoinBackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		values, rpcError := parseParamsArray(params)
		if rpcError != nil {
			return nil, rpcError
		}
		if len(values) < 4 {
			return nil, invalidParamsError("unstake requires staker seed, validator address, lamports, unlock epoch")
		}
		stakerSeed, validatorAddress, lamports, rpcError := parseSeedValidatorAmount(values[:3], "unstake")
		if rpcError != nil {
			return nil, rpcError
		}
		unlockEpoch, rpcError := parseUint64Param(values[3], "unstake unlock epoch")
		if rpcError != nil {
			return nil, rpcError
		}
		signature, err := joinBackend.Unstake(ctx, stakerSeed, validatorAddress, lamports, unlockEpoch)
		if err != nil {
			return nil, internalError(fmt.Sprintf("unstake: %v", err))
		}
		return TransactionSubmitResult{Signature: signature}, nil
	}
}

func slashValidatorHandler(backend LedgerBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		punishmentBackend, ok := backend.(PunishmentBackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		values, rpcError := parseParamsArray(params)
		if rpcError != nil {
			return nil, rpcError
		}
		stakerSeed, validatorAddress, lamports, rpcError := parseSeedValidatorAmount(values, "slashValidator")
		if rpcError != nil {
			return nil, rpcError
		}
		signature, err := punishmentBackend.SlashValidator(ctx, stakerSeed, validatorAddress, lamports)
		if err != nil {
			return nil, internalError(fmt.Sprintf("slash validator: %v", err))
		}
		return TransactionSubmitResult{Signature: signature}, nil
	}
}

func jailValidatorHandler(backend LedgerBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		punishmentBackend, ok := backend.(PunishmentBackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		values, rpcError := parseParamsArray(params)
		if rpcError != nil {
			return nil, rpcError
		}
		if len(values) < 3 {
			return nil, invalidParamsError("jailValidator requires staker seed, validator address, jail until epoch")
		}
		stakerSeed, rpcError := parseStringParam(values[0], "jailValidator staker seed")
		if rpcError != nil {
			return nil, rpcError
		}
		validatorAddress, rpcError := parseStringParam(values[1], "jailValidator validator address")
		if rpcError != nil {
			return nil, rpcError
		}
		jailUntilEpoch, rpcError := parseUint64Param(values[2], "jailValidator jail until epoch")
		if rpcError != nil {
			return nil, rpcError
		}
		signature, err := punishmentBackend.JailValidator(ctx, stakerSeed, validatorAddress, jailUntilEpoch)
		if err != nil {
			return nil, internalError(fmt.Sprintf("jail validator: %v", err))
		}
		return TransactionSubmitResult{Signature: signature}, nil
	}
}

func getValidatorSetHandler(backend LedgerBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		validatorBackend, ok := backend.(ValidatorSetBackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		if rpcError := parseNoParams(params); rpcError != nil {
			return nil, rpcError
		}
		result, err := validatorBackend.GetValidatorSet(ctx)
		if err != nil {
			return nil, internalError(fmt.Sprintf("get validator set: %v", err))
		}
		return result, nil
	}
}

func getNodeStatusHandler(backend LedgerBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		statusBackend, ok := backend.(NodeStatusBackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		if rpcError := parseNoParams(params); rpcError != nil {
			return nil, rpcError
		}
		result, err := statusBackend.GetNodeStatus(ctx)
		if err != nil {
			return nil, internalError(fmt.Sprintf("get node status: %v", err))
		}
		return result, nil
	}
}

func getConsensusStatusHandler(backend LedgerBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		statusBackend, ok := backend.(ConsensusStatusBackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		if rpcError := parseNoParams(params); rpcError != nil {
			return nil, rpcError
		}
		result, err := statusBackend.GetConsensusStatus(ctx)
		if err != nil {
			return nil, internalError(fmt.Sprintf("get consensus status: %v", err))
		}
		return result, nil
	}
}

func getMetricsHandler(backend LedgerBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		metricsBackend, ok := backend.(MetricsBackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		if rpcError := parseNoParams(params); rpcError != nil {
			return nil, rpcError
		}
		result, err := metricsBackend.GetMetrics(ctx)
		if err != nil {
			return nil, internalError(fmt.Sprintf("get metrics: %v", err))
		}
		return result, nil
	}
}

func getHealthHandler(backend LedgerBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		statusBackend, ok := backend.(NodeStatusBackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		if rpcError := parseNoParams(params); rpcError != nil {
			return nil, rpcError
		}
		result, err := statusBackend.GetHealth(ctx)
		if err != nil {
			return nil, internalError(fmt.Sprintf("get health: %v", err))
		}
		return result, nil
	}
}

type getBalanceParams struct {
	Address string `json:"address"`
}

type sendTransactionParams struct {
	EncodedTransaction string `json:"encodedTransaction"`
}

type getBlockParams struct {
	Slot uint64 `json:"slot"`
}

type privacySpendParams struct {
	AuthoritySeed          string
	StateAddress           string
	Commitment             string
	Nullifier              string
	DestinationOrRecipient string
	Lamports               uint64
	Auditor                string
	AuditSecret            string
	ExpiresAtSlot          uint64
}

func parseGetBalanceParams(params json.RawMessage) (getBalanceParams, *Error) {
	values, rpcError := parseParamsArray(params)
	if rpcError != nil {
		return getBalanceParams{}, rpcError
	}
	if len(values) < 1 {
		return getBalanceParams{}, invalidParamsError("getBalance requires address")
	}
	var address string
	if err := json.Unmarshal(values[0], &address); err != nil || address == "" {
		return getBalanceParams{}, invalidParamsError("getBalance address must be a non-empty string")
	}
	return getBalanceParams{Address: address}, nil
}
func parseSendTransactionParams(params json.RawMessage) (sendTransactionParams, *Error) {
	values, rpcError := parseParamsArray(params)
	if rpcError != nil {
		return sendTransactionParams{}, rpcError
	}
	if len(values) < 1 {
		return sendTransactionParams{}, invalidParamsError("sendTransaction requires encoded transaction")
	}
	var encodedTransaction string
	if err := json.Unmarshal(values[0], &encodedTransaction); err != nil || encodedTransaction == "" {
		return sendTransactionParams{}, invalidParamsError("sendTransaction transaction must be a non-empty string")
	}
	return sendTransactionParams{EncodedTransaction: encodedTransaction}, nil
}
func parseGetBlockParams(params json.RawMessage) (getBlockParams, *Error) {
	values, rpcError := parseParamsArray(params)
	if rpcError != nil {
		return getBlockParams{}, rpcError
	}
	if len(values) < 1 {
		return getBlockParams{}, invalidParamsError("getBlock requires slot")
	}
	var slot uint64
	if err := json.Unmarshal(values[0], &slot); err != nil {
		return getBlockParams{}, invalidParamsError("getBlock slot must be an unsigned integer")
	}
	return getBlockParams{Slot: slot}, nil
}

func parseNoParams(params json.RawMessage) *Error {
	if len(params) == 0 || string(params) == "null" {
		return nil
	}
	values, rpcError := parseParamsArray(params)
	if rpcError != nil {
		return rpcError
	}
	if len(values) != 0 {
		return invalidParamsError("method does not accept params")
	}
	return nil
}

func parseAddressAmount(values []json.RawMessage, method string) (string, uint64, *Error) {
	if len(values) < 2 {
		return "", 0, invalidParamsError(method + " requires address and lamports")
	}
	address, rpcError := parseStringParam(values[0], method+" address")
	if rpcError != nil {
		return "", 0, rpcError
	}
	lamports, rpcError := parseUint64Param(values[1], method+" lamports")
	if rpcError != nil {
		return "", 0, rpcError
	}
	return address, lamports, nil
}

func parseSeedValidatorAmount(values []json.RawMessage, method string) (string, string, uint64, *Error) {
	if len(values) < 3 {
		return "", "", 0, invalidParamsError(method + " requires staker seed, validator address, lamports")
	}
	stakerSeed, rpcError := parseStringParam(values[0], method+" staker seed")
	if rpcError != nil {
		return "", "", 0, rpcError
	}
	validatorAddress, rpcError := parseStringParam(values[1], method+" validator address")
	if rpcError != nil {
		return "", "", 0, rpcError
	}
	lamports, rpcError := parseUint64Param(values[2], method+" lamports")
	if rpcError != nil {
		return "", "", 0, rpcError
	}
	return stakerSeed, validatorAddress, lamports, nil
}

func parseStringParam(value json.RawMessage, field string) (string, *Error) {
	var text string
	if err := json.Unmarshal(value, &text); err != nil || text == "" {
		return "", invalidParamsError(field + " must be a non-empty string")
	}
	return text, nil
}

func parseUint64Param(value json.RawMessage, field string) (uint64, *Error) {
	var number uint64
	if err := json.Unmarshal(value, &number); err != nil || number == 0 {
		return 0, invalidParamsError(field + " must be a positive unsigned integer")
	}
	return number, nil
}

func parseOptionalStringParam(value json.RawMessage, field string) (string, *Error) {
	var text string
	if err := json.Unmarshal(value, &text); err != nil {
		return "", invalidParamsError(field + " must be a string")
	}
	return text, nil
}

func parseUint64ParamAllowZero(value json.RawMessage, field string) (uint64, *Error) {
	var number uint64
	if err := json.Unmarshal(value, &number); err != nil {
		return 0, invalidParamsError(field + " must be an unsigned integer")
	}
	return number, nil
}

func parsePrivacyDepositParams(values []json.RawMessage) (string, string, uint64, string, string, uint64, *Error) {
	if len(values) < 6 {
		return "", "", 0, "", "", 0, invalidParamsError("privacyDeposit requires source seed, state seed, lamports, auditor, audit secret, expires slot")
	}
	sourceSeed, rpcError := parseStringParam(values[0], "privacyDeposit source seed")
	if rpcError != nil {
		return "", "", 0, "", "", 0, rpcError
	}
	stateSeed, rpcError := parseStringParam(values[1], "privacyDeposit state seed")
	if rpcError != nil {
		return "", "", 0, "", "", 0, rpcError
	}
	lamports, rpcError := parseUint64Param(values[2], "privacyDeposit lamports")
	if rpcError != nil {
		return "", "", 0, "", "", 0, rpcError
	}
	auditor, auditSecret, expiresAtSlot, rpcError := parsePrivacyAuditTail(values[3:6], "privacyDeposit")
	return sourceSeed, stateSeed, lamports, auditor, auditSecret, expiresAtSlot, rpcError
}

func parsePrivacySpendParams(values []json.RawMessage, method string) (privacySpendParams, *Error) {
	if len(values) < 9 {
		return privacySpendParams{}, invalidParamsError(method + " requires authority seed, state address, commitment, nullifier, destination/recipient, lamports, auditor, audit secret, expires slot")
	}
	authoritySeed, rpcError := parseStringParam(values[0], method+" authority seed")
	if rpcError != nil {
		return privacySpendParams{}, rpcError
	}
	stateAddress, rpcError := parseStringParam(values[1], method+" state address")
	if rpcError != nil {
		return privacySpendParams{}, rpcError
	}
	commitment, rpcError := parseStringParam(values[2], method+" commitment")
	if rpcError != nil {
		return privacySpendParams{}, rpcError
	}
	nullifier, rpcError := parseStringParam(values[3], method+" nullifier")
	if rpcError != nil {
		return privacySpendParams{}, rpcError
	}
	destinationOrRecipient, rpcError := parseStringParam(values[4], method+" destination or recipient")
	if rpcError != nil {
		return privacySpendParams{}, rpcError
	}
	lamports, rpcError := parseUint64Param(values[5], method+" lamports")
	if rpcError != nil {
		return privacySpendParams{}, rpcError
	}
	auditor, auditSecret, expiresAtSlot, rpcError := parsePrivacyAuditTail(values[6:9], method)
	if rpcError != nil {
		return privacySpendParams{}, rpcError
	}
	return privacySpendParams{
		AuthoritySeed:          authoritySeed,
		StateAddress:           stateAddress,
		Commitment:             commitment,
		Nullifier:              nullifier,
		DestinationOrRecipient: destinationOrRecipient,
		Lamports:               lamports,
		Auditor:                auditor,
		AuditSecret:            auditSecret,
		ExpiresAtSlot:          expiresAtSlot,
	}, nil
}

func parsePrivacyAuthorizeAuditParams(values []json.RawMessage) (string, string, string, string, string, uint8, uint64, *Error) {
	if len(values) < 7 {
		return "", "", "", "", "", 0, 0, invalidParamsError("privacyAuthorizeAudit requires authority seed, state address, commitment, auditor, audit secret, scope, expires slot")
	}
	authoritySeed, rpcError := parseStringParam(values[0], "privacyAuthorizeAudit authority seed")
	if rpcError != nil {
		return "", "", "", "", "", 0, 0, rpcError
	}
	stateAddress, rpcError := parseStringParam(values[1], "privacyAuthorizeAudit state address")
	if rpcError != nil {
		return "", "", "", "", "", 0, 0, rpcError
	}
	commitment, rpcError := parseStringParam(values[2], "privacyAuthorizeAudit commitment")
	if rpcError != nil {
		return "", "", "", "", "", 0, 0, rpcError
	}
	auditor, rpcError := parseStringParam(values[3], "privacyAuthorizeAudit auditor")
	if rpcError != nil {
		return "", "", "", "", "", 0, 0, rpcError
	}
	auditSecret, rpcError := parseStringParam(values[4], "privacyAuthorizeAudit audit secret")
	if rpcError != nil {
		return "", "", "", "", "", 0, 0, rpcError
	}
	scopeValue, rpcError := parseUint64Param(values[5], "privacyAuthorizeAudit scope")
	if rpcError != nil || scopeValue > 255 {
		return "", "", "", "", "", 0, 0, invalidParamsError("privacyAuthorizeAudit scope must be 1, 2, or 3")
	}
	expiresAtSlot, rpcError := parseUint64ParamAllowZero(values[6], "privacyAuthorizeAudit expires slot")
	if rpcError != nil {
		return "", "", "", "", "", 0, 0, rpcError
	}
	return authoritySeed, stateAddress, commitment, auditor, auditSecret, uint8(scopeValue), expiresAtSlot, nil
}

func parsePrivacyAuditTail(values []json.RawMessage, method string) (string, string, uint64, *Error) {
	auditor, rpcError := parseOptionalStringParam(values[0], method+" auditor")
	if rpcError != nil {
		return "", "", 0, rpcError
	}
	auditSecret, rpcError := parseOptionalStringParam(values[1], method+" audit secret")
	if rpcError != nil {
		return "", "", 0, rpcError
	}
	expiresAtSlot, rpcError := parseUint64ParamAllowZero(values[2], method+" expires slot")
	if rpcError != nil {
		return "", "", 0, rpcError
	}
	if (auditor == "") != (auditSecret == "") {
		return "", "", 0, invalidParamsError(method + " auditor and audit secret must be provided together")
	}
	return auditor, auditSecret, expiresAtSlot, nil
}

func parseParamsArray(params json.RawMessage) ([]json.RawMessage, *Error) {
	if len(params) == 0 || string(params) == "null" {
		return nil, invalidParamsError("params must be an array")
	}
	var values []json.RawMessage
	if err := json.Unmarshal(params, &values); err != nil {
		return nil, invalidParamsError("params must be an array")
	}
	return values, nil
}
