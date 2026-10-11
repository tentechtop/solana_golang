package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	MethodGetBalance                 = "getBalance"
	MethodGetAccountType             = "getAccountType"
	MethodGetAccountInfo             = "getAccountInfo"
	MethodGetLatestBlockhash         = "getLatestBlockhash"
	MethodSendTransaction            = "sendTransaction"
	MethodSimulateTransaction        = "simulateTransaction"
	MethodGetBlock                   = "getBlock"
	MethodGetTransaction             = "getTransaction"
	MethodGetAddressTransactions     = "getAddressTransactions"
	MethodGetContractPrograms        = "getContractPrograms"
	MethodGetAssetState              = "getAssetState"
	MethodGetShieldedPool            = "getShieldedPool"
	MethodTreasuryTransfer           = "treasuryTransfer"
	MethodTransfer                   = "transfer"
	MethodRegisterValidator          = "registerValidator"
	MethodRegisterValidatorIdentity  = "registerValidatorIdentity"
	MethodStake                      = "stake"
	MethodUnstake                    = "unstake"
	MethodSlashValidator             = "slashValidator"
	MethodJailValidator              = "jailValidator"
	MethodGetLocalValidatorIdentity  = "getLocalValidatorIdentity"
	MethodGetValidatorSet            = "getValidatorSet"
	MethodGetNodeStatus              = "getNodeStatus"
	MethodGetPeerNetwork             = "getPeerNetwork"
	MethodGetConsensusStatus         = "getConsensusStatus"
	MethodGetMetrics                 = "getMetrics"
	MethodGetHealth                  = "getHealth"
	MethodGetValidatorPairing        = "getValidatorPairing"
	MethodCompleteValidatorPairing   = "completeValidatorPairing"
	MethodBootstrapRegisterValidator = "bootstrapRegisterValidator"
	MethodGetBootstrapManifest       = "getBootstrapManifest"
	MethodGetBootstrapStatus         = "getBootstrapStatus"
)

// LedgerBackend 定义链业务后端 + 让 RPC 层只负责协议转换和参数校验。
type LedgerBackend interface {
	GetBalance(ctx context.Context, address string) (BalanceResult, error)
	GetLatestBlockhash(ctx context.Context) (LatestBlockhashResult, error)
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

type TransactionLookupBackend interface {
	GetTransaction(ctx context.Context, signature string) (TransactionDetailResult, error)
}

type AccountHistoryBackend interface {
	GetAddressTransactions(ctx context.Context, address string, cursor string, limit int) (AccountTransactionHistoryResult, error)
}

type ContractProgramBackend interface {
	GetContractPrograms(ctx context.Context, limit int) (ContractProgramListResult, error)
}

type AssetStateBackend interface {
	GetAssetState(ctx context.Context, program string, owner string) (AssetStateResult, error)
}

// ShieldedPoolBackend 定义屏蔽池只读查询 + 钱包无需向节点提交任何查看密钥或私钥。
type ShieldedPoolBackend interface {
	GetShieldedPool(ctx context.Context) (ShieldedPoolResult, error)
}

type ValidatorJoinBackend interface {
	RegisterValidator(ctx context.Context, stakerSeed string, validatorSeed string, consensusSeed string, peerID string, stakeLamports uint64) (string, error)
	RegisterValidatorIdentity(ctx context.Context, stakerSeed string, validatorAddress string, consensusPublicKey string, blsPublicKey string, peerID string, stakeLamports uint64) (string, error)
	Stake(ctx context.Context, stakerSeed string, validatorAddress string, lamports uint64) (string, error)
	Unstake(ctx context.Context, stakerSeed string, validatorAddress string, lamports uint64, unlockEpoch uint64) (string, error)
}

type LocalValidatorIdentityBackend interface {
	GetLocalValidatorIdentity(ctx context.Context) (LocalValidatorIdentityResult, error)
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

// PeerNetworkBackend 定义 peer 拓扑后端 + 让钱包区分已发现地址和当前连接。
type PeerNetworkBackend interface {
	GetPeerNetwork(ctx context.Context) (PeerNetworkResult, error)
}

type ConsensusStatusBackend interface {
	GetConsensusStatus(ctx context.Context) (any, error)
}

type MetricsBackend interface {
	GetMetrics(ctx context.Context) (any, error)
}

type ValidatorPairingBackend interface {
	GetValidatorPairing(ctx context.Context) (ValidatorPairingResult, error)
	CompleteValidatorPairing(ctx context.Context, request ValidatorPairingCompleteRequest) (ValidatorPairingCompleteResult, error)
}

type BootstrapBackend interface {
	BootstrapRegisterValidator(ctx context.Context, request BootstrapValidatorRegistrationRequest) (BootstrapRegisterValidatorResult, error)
	GetBootstrapManifest(ctx context.Context) (BootstrapManifestResult, error)
	GetBootstrapStatus(ctx context.Context) (BootstrapStatusResult, error)
}

type PublicRPCForwardBackend interface {
	ForwardPublicRPC(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, *Error, error)
}

type BootstrapNodeBackend interface {
	BootstrapBackend
	PublicRPCForwardBackend
	NodeStatusBackend
	PeerNetworkBackend
}

type BalanceResult struct {
	Value uint64 `json:"value"`
}

type LatestBlockhashResult struct {
	Blockhash            string `json:"blockhash"`
	Slot                 uint64 `json:"slot"`
	Height               uint64 `json:"height"`
	LastValidSlot        uint64 `json:"last_valid_slot"`
	LastValidBlockHeight uint64 `json:"last_valid_block_height"`
}

type AccountTypeResult struct {
	Address string `json:"address"`
	Exists  bool   `json:"exists"`
	Owner   string `json:"owner,omitempty"`
	Type    string `json:"type"`
}

type BlockResult struct {
	Slot                 uint64  `json:"slot"`
	Height               uint64  `json:"height,omitempty"`
	EpochID              uint64  `json:"epoch_id,omitempty"`
	Blockhash            string  `json:"blockhash,omitempty"`
	ParentSlot           uint64  `json:"parentSlot,omitempty"`
	BlockTimeUnixMilli   int64   `json:"block_time_unix_milli,omitempty"`
	StateRoot            string  `json:"state_root,omitempty"`
	EpochSnapshotRoot    string  `json:"epoch_snapshot_root,omitempty"`
	TxRoot               string  `json:"tx_root,omitempty"`
	ReceiptRoot          string  `json:"receipt_root,omitempty"`
	RewardQCRoot         string  `json:"reward_qc_root,omitempty"`
	EvidenceRoot         string  `json:"evidence_root,omitempty"`
	RewardRoot           string  `json:"reward_root,omitempty"`
	RewardQCCount        int     `json:"reward_qc_count"`
	EvidenceCount        int     `json:"evidence_count"`
	RewardCount          int     `json:"reward_count"`
	LeaderAddress        string  `json:"leader_address,omitempty"`
	LeaderAddressSource  string  `json:"leader_address_source,omitempty"`
	LeaderCommissionBps  *uint16 `json:"leader_commission_bps,omitempty"`
	LeaderStakeLamports  *uint64 `json:"leader_stake_lamports,omitempty"`
	LeaderVoteCredits    *uint64 `json:"leader_vote_credits,omitempty"`
	LeaderRewardLamports *uint64 `json:"leader_reward_lamports,omitempty"`
	Transactions         []any   `json:"transactions"`
}

// MarshalJSON 固定区块交易数组输出 + 空区块也要返回 [] 方便客户端区分无交易和 RPC 字段缺失。
func (result BlockResult) MarshalJSON() ([]byte, error) {
	type blockResultAlias BlockResult
	alias := blockResultAlias(result)
	if alias.Transactions == nil {
		alias.Transactions = []any{}
	}
	return json.Marshal(alias)
}

type TransactionDetailResult struct {
	Signature                 string   `json:"signature"`
	Found                     bool     `json:"found"`
	Location                  string   `json:"location"`
	Status                    string   `json:"status"`
	Error                     string   `json:"error,omitempty"`
	Sender                    string   `json:"sender,omitempty"`
	RecentBlockhash           string   `json:"recent_blockhash,omitempty"`
	FeeLamports               uint64   `json:"fee_lamports"`
	BaseFeeLamports           uint64   `json:"base_fee_lamports"`
	PrioritizationFeeLamports uint64   `json:"prioritization_fee_lamports"`
	BurnedFeeLamports         uint64   `json:"burned_fee_lamports"`
	LeaderFeeLamports         uint64   `json:"leader_fee_lamports"`
	LeaderAddress             string   `json:"leader_address,omitempty"`
	SubmitTimeUnixMilli       int64    `json:"submit_time_unix_milli"`
	AccountAddresses          []string `json:"account_addresses,omitempty"`
	WritableAddresses         []string `json:"writable_addresses,omitempty"`
	InstructionCount          int      `json:"instruction_count"`
	BlockHeight               uint64   `json:"block_height,omitempty"`
	Slot                      uint64   `json:"slot,omitempty"`
	Blockhash                 string   `json:"blockhash,omitempty"`
	Finalized                 bool     `json:"finalized"`
	TransactionBase64         string   `json:"transaction_base64,omitempty"`
}

type AccountTransactionRecordResult struct {
	Signature           string `json:"signature"`
	Direction           string `json:"direction"`
	Kind                string `json:"kind"`
	Counterparty        string `json:"counterparty,omitempty"`
	AmountLamports      string `json:"amount_lamports"`
	BlockHeight         uint64 `json:"block_height"`
	Slot                uint64 `json:"slot"`
	Blockhash           string `json:"blockhash"`
	SubmitTimeUnixMilli int64  `json:"submit_time_unix_milli"`
	Finalized           bool   `json:"finalized"`
	Status              string `json:"status"`
	Location            string `json:"location"`
}

type AccountTransactionHistoryResult struct {
	Address    string                           `json:"address"`
	Scope      string                           `json:"scope"`
	Records    []AccountTransactionRecordResult `json:"records"`
	NextCursor string                           `json:"next_cursor,omitempty"`
	HasMore    bool                             `json:"has_more"`
}

type ContractProgramResult struct {
	Address    string `json:"address"`
	Owner      string `json:"owner"`
	Executable bool   `json:"executable"`
	Lamports   string `json:"lamports"`
	DataLength int    `json:"data_length"`
	CodeHash   string `json:"code_hash"`
	RentEpoch  uint64 `json:"rent_epoch"`
}

type ContractProgramListResult struct {
	Scope    string                  `json:"scope"`
	Programs []ContractProgramResult `json:"programs"`
}

type AssetMintStateResult struct {
	Address   string `json:"address"`
	Exists    bool   `json:"exists"`
	Owner     string `json:"owner,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Decimals  uint8  `json:"decimals,omitempty"`
	Authority string `json:"authority,omitempty"`
	Supply    string `json:"supply,omitempty"`
	MaxSupply string `json:"max_supply,omitempty"`
	Name      string `json:"name,omitempty"`
	Symbol    string `json:"symbol,omitempty"`
	URI       string `json:"uri,omitempty"`
	Error     string `json:"error,omitempty"`
}

type AssetBalanceStateResult struct {
	Address string `json:"address"`
	Exists  bool   `json:"exists"`
	Owner   string `json:"owner,omitempty"`
	Mint    string `json:"mint,omitempty"`
	Amount  string `json:"amount,omitempty"`
	Error   string `json:"error,omitempty"`
}

type AssetStateResult struct {
	Program string                  `json:"program"`
	Owner   string                  `json:"owner"`
	Mint    AssetMintStateResult    `json:"mint"`
	Balance AssetBalanceStateResult `json:"balance"`
}

// ShieldedPoolResult 返回全局屏蔽池的公开共识状态 + data_base64 可供外部钱包按规范编码校验状态。
type ShieldedPoolResult struct {
	Address              string   `json:"address"`
	StateEncodingVersion uint16   `json:"state_encoding_version"`
	ProtocolVersion      uint16   `json:"protocol_version"`
	CircuitID            uint32   `json:"circuit_id"`
	VerifyingKeyHash     string   `json:"verifying_key_hash"`
	TreeDepth            uint8    `json:"tree_depth"`
	LeafCount            uint64   `json:"leaf_count"`
	CurrentRoot          string   `json:"current_root"`
	RecentAnchors        []string `json:"recent_anchors"`
	PoolLamports         uint64   `json:"pool_lamports"`
	PhysicalLamports     uint64   `json:"physical_lamports"`
	DataBase64           string   `json:"data_base64"`
}

type TransactionSubmitResult struct {
	Signature string `json:"signature"`
}

type ValidatorInfo struct {
	ValidatorID                string           `json:"validator_id"`
	AccountAddress             string           `json:"account_address"`
	StakerAddress              string           `json:"staker_address"`
	ConsensusPublicKey         string           `json:"consensus_public_key"`
	P2PPeerID                  string           `json:"p2p_peer_id"`
	StakeLamports              uint64           `json:"stake_lamports"`
	BondedStakeLamports        uint64           `json:"bonded_stake_lamports"`
	EffectiveStakeLamports     uint64           `json:"effective_stake_lamports"`
	SelfStakeLamports          uint64           `json:"self_stake_lamports"`
	SelfPendingStakeLamports   uint64           `json:"self_pending_stake_lamports"`
	SelfUnlockingStakeLamports uint64           `json:"self_unlocking_stake_lamports"`
	SelfRewardLamports         uint64           `json:"self_reward_lamports"`
	CommissionRewardLamports   uint64           `json:"commission_reward_lamports"`
	DelegatedLamports          uint64           `json:"delegated_lamports"`
	DelegatorCount             int              `json:"delegator_count"`
	Status                     string           `json:"status"`
	CommissionBps              uint16           `json:"commission_bps"`
	VoteCredits                uint64           `json:"vote_credits"`
	RewardLamports             uint64           `json:"reward_lamports"`
	LastRewardedSlot           uint64           `json:"last_rewarded_slot"`
	LastRewardEpoch            uint64           `json:"last_reward_epoch"`
	JailUntilEpoch             uint64           `json:"jail_until_epoch"`
	ActivationEpoch            uint64           `json:"activation_epoch"`
	DeactivationEpoch          uint64           `json:"deactivation_epoch"`
	LastEffectiveStakeLamports uint64           `json:"last_effective_stake_lamports"`
	LastSlashedSlot            uint64           `json:"last_slashed_slot"`
	Delegations                []DelegationInfo `json:"delegations,omitempty"`
}

type DelegationInfo struct {
	DelegatorAddress       string `json:"delegator_address"`
	ActiveStakeLamports    uint64 `json:"active_stake_lamports"`
	PendingStakeLamports   uint64 `json:"pending_stake_lamports"`
	UnlockingStakeLamports uint64 `json:"unlocking_stake_lamports"`
	TotalStakeLamports     uint64 `json:"total_stake_lamports"`
	RewardLamports         uint64 `json:"reward_lamports"`
	ActivationEpoch        uint64 `json:"activation_epoch"`
	DeactivationEpoch      uint64 `json:"deactivation_epoch"`
	UnlockEpoch            uint64 `json:"unlock_epoch"`
}

type ValidatorSetResult struct {
	Validators []ValidatorInfo `json:"validators"`
}

type LocalValidatorIdentityResult struct {
	NodeName                 string `json:"node_name"`
	StakerAddress            string `json:"staker_address"`
	ValidatorAddress         string `json:"validator_address"`
	ConsensusPublicKey       string `json:"consensus_public_key"`
	BLSPublicKey             string `json:"bls_public_key"`
	P2PPeerID                string `json:"p2p_peer_id"`
	RecommendedStakeLamports uint64 `json:"recommended_stake_lamports"`
	Registered               bool   `json:"registered"`
	Status                   string `json:"status"`
	ActiveStakeLamports      uint64 `json:"active_stake_lamports"`
	PendingStakeLamports     uint64 `json:"pending_stake_lamports"`
	UnlockingStakeLamports   uint64 `json:"unlocking_stake_lamports"`
	EffectiveStakeLamports   uint64 `json:"effective_stake_lamports"`
	ActivationEpoch          uint64 `json:"activation_epoch"`
	DeactivationEpoch        uint64 `json:"deactivation_epoch"`
	CurrentEpoch             uint64 `json:"current_epoch"`
	CommissionBps            uint16 `json:"commission_bps"`
	VoteCredits              uint64 `json:"vote_credits"`
	RewardLamports           uint64 `json:"reward_lamports"`
	SelfRewardLamports       uint64 `json:"self_reward_lamports"`
	CommissionRewardLamports uint64 `json:"commission_reward_lamports"`
	LastRewardedSlot         uint64 `json:"last_rewarded_slot"`
	LastRewardEpoch          uint64 `json:"last_reward_epoch"`
	LastSlashedSlot          uint64 `json:"last_slashed_slot"`
}

// PeerConnectionInfo 保存连接细节 + 让前端展示当前连通性和最近活跃时间。
type PeerConnectionInfo struct {
	Protocol               string `json:"protocol,omitempty"`
	RemoteAddress          string `json:"remote_address,omitempty"`
	ObservedRemoteAddress  string `json:"observed_remote_address,omitempty"`
	Encrypted              bool   `json:"encrypted"`
	ConnectedAtUnixMilli   int64  `json:"connected_at_unix_milli"`
	LastReadUnixMilli      int64  `json:"last_read_unix_milli"`
	LastWriteUnixMilli     int64  `json:"last_write_unix_milli"`
	LastHeartbeatUnixMilli int64  `json:"last_heartbeat_unix_milli"`
	FailureCount           uint32 `json:"failure_count"`
}

// PeerNetworkPeerResult 保存单个 peer 状态 + 让前端展示地址解析和连接结果。
type PeerNetworkPeerResult struct {
	PeerID                    string              `json:"peer_id"`
	Status                    string              `json:"status"`
	Role                      string              `json:"role"`
	Roles                     []string            `json:"roles,omitempty"`
	Capabilities              uint64              `json:"capabilities"`
	CapabilityNames           []string            `json:"capability_names,omitempty"`
	Validator                 bool                `json:"validator"`
	Connected                 bool                `json:"connected"`
	BestAddress               string              `json:"best_address,omitempty"`
	AdvertisedAddresses       []string            `json:"advertised_addresses,omitempty"`
	VerifiedAddresses         []string            `json:"verified_addresses,omitempty"`
	PreferredProtocols        []string            `json:"preferred_protocols,omitempty"`
	LatestSlot                uint64              `json:"latest_slot"`
	BlockHeight               uint64              `json:"block_height"`
	FailureCount              uint32              `json:"failure_count"`
	LastError                 string              `json:"last_error,omitempty"`
	LastSeenUnixMilli         int64               `json:"last_seen_unix_milli"`
	LastConnectedUnixMilli    int64               `json:"last_connected_unix_milli"`
	LastDisconnectedUnixMilli int64               `json:"last_disconnected_unix_milli"`
	Connection                *PeerConnectionInfo `json:"connection,omitempty"`
}

// PeerNetworkResult 保存 peer 拓扑结果 + 让前端按本地节点视角分析网络状态。
type PeerNetworkResult struct {
	LocalPeerID string                  `json:"local_peer_id"`
	Peers       []PeerNetworkPeerResult `json:"peers"`
}

type HealthResult struct {
	OK                                bool   `json:"ok"`
	HeadHeight                        uint64 `json:"head_height"`
	HeadSlot                          uint64 `json:"head_slot"`
	FinalizedHeight                   uint64 `json:"finalized_height"`
	HeadUpdatedUnixMilli              int64  `json:"head_updated_unix_milli"`
	HeadAgeMillis                     int64  `json:"head_age_millis"`
	HeadStaleThresholdMillis          int64  `json:"head_stale_threshold_millis"`
	ChainProgressing                  bool   `json:"chain_progressing"`
	TransactionSubmissionEnabled      bool   `json:"transaction_submission_enabled"`
	TransactionSubmissionReason       string `json:"transaction_submission_reason,omitempty"`
	MempoolSize                       int    `json:"mempool_size"`
	LivenessState                     string `json:"liveness_state,omitempty"`
	LivenessMode                      string `json:"liveness_mode,omitempty"`
	LivenessReason                    string `json:"liveness_reason,omitempty"`
	LivenessQuorumReady               bool   `json:"liveness_quorum_ready"`
	LivenessProductionEnabled         bool   `json:"liveness_production_enabled"`
	ReachableStakeLamports            uint64 `json:"reachable_stake_lamports"`
	RequiredStakeLamports             uint64 `json:"required_stake_lamports"`
	TotalActiveStakeLamports          uint64 `json:"total_active_stake_lamports"`
	RecentReachabilityWindowMillis    int64  `json:"recent_reachability_window_millis"`
	LastReachableStakeUpdateUnixMilli int64  `json:"last_reachable_stake_update_unix_milli"`
}

type BootstrapValidatorRegistrationRequest struct {
	ChainID               string `json:"chain_id"`
	NodeName              string `json:"node_name"`
	PeerID                string `json:"peer_id"`
	AdvertisedIP          string `json:"advertised_ip"`
	AdvertisedPort        int    `json:"advertised_port"`
	Network               string `json:"network"`
	StakerAddress         string `json:"staker_address"`
	ValidatorAddress      string `json:"validator_address"`
	ConsensusPublicKey    string `json:"consensus_public_key"`
	BLSPublicKeyBase64    string `json:"bls_public_key_base64"`
	StakeLamports         uint64 `json:"stake_lamports"`
	CommissionBps         uint16 `json:"commission_bps"`
	RegisteredAtUnixMilli int64  `json:"registered_at_unix_milli"`
	StakerSignature       string `json:"staker_signature"`
	Signature             string `json:"signature"`
}

type BootstrapRegisterValidatorResult struct {
	Accepted              bool   `json:"accepted"`
	Ready                 bool   `json:"ready"`
	ValidatorCount        int    `json:"validator_count"`
	MinValidators         int    `json:"min_validators"`
	GenesisStartUnixMilli int64  `json:"genesis_start_unix_millis,omitempty"`
	ChainIdentityHash     string `json:"chain_identity_hash,omitempty"`
	GenesisHash           string `json:"genesis_hash,omitempty"`
}

type BootstrapPeerConfigResult struct {
	PeerID       string   `json:"peer_id"`
	IP           string   `json:"ip"`
	Port         int      `json:"port"`
	Network      string   `json:"network"`
	Role         string   `json:"role,omitempty"`
	Roles        []string `json:"roles,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
}

type BootstrapGenesisAccountResult struct {
	Address  string `json:"address,omitempty"`
	Seed     string `json:"seed,omitempty"`
	Lamports uint64 `json:"lamports"`
}

type BootstrapGenesisValidatorResult struct {
	StakerAddress      string `json:"staker_address"`
	ValidatorAddress   string `json:"validator_address"`
	ConsensusPublicKey string `json:"consensus_public_key"`
	BLSPublicKeyBase64 string `json:"bls_public_key_base64"`
	PeerID             string `json:"peer_id"`
	StakeLamports      uint64 `json:"stake_lamports"`
	CommissionBps      uint16 `json:"commission_bps,omitempty"`
}

type BootstrapGenesisResult struct {
	InitialSupplyLamports uint64                            `json:"initial_supply_lamports"`
	TreasuryAddress       string                            `json:"treasury_address,omitempty"`
	FundedAccounts        []BootstrapGenesisAccountResult   `json:"funded_accounts"`
	InitialValidators     []BootstrapGenesisValidatorResult `json:"initial_validators"`
}

type BootstrapContractDeploymentPolicyResult struct {
	AllowedDeployers             []string `json:"allowed_deployers,omitempty"`
	MinDeploymentDepositLamports uint64   `json:"min_deployment_deposit_lamports,omitempty"`
	RequireManifest              bool     `json:"require_manifest"`
	AllowUpgradeableContracts    bool     `json:"allow_upgradeable_contracts"`
}

// NetworkParameters carries replay and recovery configuration without changing
// the existing chain identity or block encoding. Older clients ignore it.
type BootstrapNetworkParametersResult struct {
	Version                    int      `json:"version"`
	MultiValidatorThreshold    int      `json:"multi_validator_threshold"`
	StakeRewardExactRatioEpoch uint64   `json:"stake_reward_exact_ratio_epoch"`
	RecoveryEnabled            bool     `json:"recovery_enabled"`
	GovernancePublicKeys       []string `json:"governance_public_keys,omitempty"`
	GovernanceThreshold        int      `json:"governance_threshold,omitempty"`
}

type BootstrapManifestResult struct {
	NetworkParameters             *BootstrapNetworkParametersResult       `json:"network_parameters,omitempty"`
	Ready                         bool                                    `json:"ready"`
	ValidatorCount                int                                     `json:"validator_count"`
	MinValidators                 int                                     `json:"min_validators"`
	ChainID                       string                                  `json:"chain_id,omitempty"`
	ChainIdentityHash             string                                  `json:"chain_identity_hash,omitempty"`
	GenesisHash                   string                                  `json:"genesis_hash,omitempty"`
	GenesisStartUnixMilli         int64                                   `json:"genesis_start_unix_millis,omitempty"`
	SlotMillis                    int                                     `json:"slot_millis,omitempty"`
	EpochSlots                    uint64                                  `json:"epoch_slots,omitempty"`
	FinalityDepth                 uint64                                  `json:"finality_depth,omitempty"`
	TurbineFanout                 int                                     `json:"turbine_fanout,omitempty"`
	TransactionLeaderForwardSlots int                                     `json:"transaction_leader_forward_slots,omitempty"`
	TransactionForwardValidators  bool                                    `json:"transaction_forward_validators"`
	ContractDeploymentPolicy      BootstrapContractDeploymentPolicyResult `json:"contract_deployment_policy"`
	Genesis                       BootstrapGenesisResult                  `json:"genesis"`
	BootstrapPeers                []BootstrapPeerConfigResult             `json:"bootstrap_peers"`
}

type BootstrapStatusResult struct {
	Ready                 bool     `json:"ready"`
	ValidatorCount        int      `json:"validator_count"`
	MinValidators         int      `json:"min_validators"`
	RegisteredPeerIDs     []string `json:"registered_peer_ids"`
	GenesisStartUnixMilli int64    `json:"genesis_start_unix_millis,omitempty"`
	ChainIdentityHash     string   `json:"chain_identity_hash,omitempty"`
	GenesisHash           string   `json:"genesis_hash,omitempty"`
}

type ValidatorPairingResult struct {
	Enabled            bool                           `json:"enabled"`
	State              string                         `json:"state"`
	Mode               string                         `json:"mode,omitempty"`
	RPCURL             string                         `json:"rpc_url,omitempty"`
	BootstrapRPCURL    string                         `json:"bootstrap_rpc_url,omitempty"`
	ChainID            string                         `json:"chain_id,omitempty"`
	ChainIdentityHash  string                         `json:"chain_identity_hash,omitempty"`
	GenesisHash        string                         `json:"genesis_hash,omitempty"`
	NodeName           string                         `json:"node_name,omitempty"`
	NodePeerID         string                         `json:"node_peer_id,omitempty"`
	AdvertisedIP       string                         `json:"advertised_ip,omitempty"`
	AdvertisedPort     int                            `json:"advertised_port,omitempty"`
	Network            string                         `json:"network,omitempty"`
	ValidatorAddress   string                         `json:"validator_address,omitempty"`
	ConsensusAddress   string                         `json:"consensus_address,omitempty"`
	BLSPublicKey       string                         `json:"bls_public_key,omitempty"`
	RegisteredAtUnixMS int64                          `json:"registered_at_unix_millis,omitempty"`
	ExpiresAtUnixMS    int64                          `json:"expires_at_unix_millis,omitempty"`
	Completed          ValidatorPairingCompleteResult `json:"completed,omitempty"`
}

type ValidatorPairingCompleteRequest struct {
	Token                    string `json:"token"`
	StakerAddress            string `json:"staker_address"`
	ValidatorAddress         string `json:"validator_address"`
	ConsensusAddress         string `json:"consensus_address"`
	BLSPublicKey             string `json:"bls_public_key"`
	NodePeerID               string `json:"node_peer_id"`
	StakeLamports            uint64 `json:"stake_lamports"`
	Signature                string `json:"signature"`
	BootstrapStakerSignature string `json:"bootstrap_staker_signature,omitempty"`
}

type ValidatorPairingCompleteResult struct {
	State                    string `json:"state,omitempty"`
	StakerAddress            string `json:"staker_address,omitempty"`
	ValidatorAddress         string `json:"validator_address,omitempty"`
	ConsensusAddress         string `json:"consensus_address,omitempty"`
	BLSPublicKey             string `json:"bls_public_key,omitempty"`
	NodePeerID               string `json:"node_peer_id,omitempty"`
	StakeLamports            uint64 `json:"stake_lamports,omitempty"`
	Signature                string `json:"signature,omitempty"`
	BootstrapStakerSignature string `json:"bootstrap_staker_signature,omitempty"`
	ConfigUpdated            bool   `json:"config_updated"`
	RestartRequired          bool   `json:"restart_required"`
	ActivationStarted        bool   `json:"activation_started,omitempty"`
	ActivationError          string `json:"activation_error,omitempty"`
	ConfigPath               string `json:"config_path,omitempty"`
	ActivationNote           string `json:"activation_note,omitempty"`
}

func RegisterDefaultHandlers(router *Router, backend LedgerBackend) {
	registerStablecoinHandlers(router, backend)
	registerRWAHandlers(router, backend)
	registerRewardHandlers(router, backend)
	registerMarketHandlers(router, backend)
	_ = router.Register(MethodGetBalance, getBalanceHandler(backend))
	_ = router.Register(MethodGetAccountType, getAccountTypeHandler(backend))
	_ = router.Register(MethodGetAccountInfo, getAccountInfoHandler(backend))
	_ = router.Register(MethodGetLatestBlockhash, getLatestBlockhashHandler(backend))
	_ = router.Register(MethodSendTransaction, sendTransactionHandler(backend))
	_ = router.Register(MethodSimulateTransaction, simulateTransactionHandler(backend))
	_ = router.Register(MethodGetBlock, getBlockHandler(backend))
	_ = router.Register(MethodGetTransaction, getTransactionHandler(backend))
	_ = router.Register(MethodGetAddressTransactions, getAddressTransactionsHandler(backend))
	_ = router.Register(MethodGetContractPrograms, getContractProgramsHandler(backend))
	_ = router.Register(MethodGetAssetState, getAssetStateHandler(backend))
	_ = router.Register(MethodGetShieldedPool, getShieldedPoolHandler(backend))
	_ = router.Register(MethodTreasuryTransfer, treasuryTransferHandler(backend))
	_ = router.Register(MethodTransfer, transferHandler(backend))
	_ = router.Register(MethodRegisterValidator, signedTransactionRequiredHandler(MethodRegisterValidator))
	_ = router.Register(MethodRegisterValidatorIdentity, signedTransactionRequiredHandler(MethodRegisterValidatorIdentity))
	_ = router.Register(MethodStake, signedTransactionRequiredHandler(MethodStake))
	_ = router.Register(MethodUnstake, signedTransactionRequiredHandler(MethodUnstake))
	_ = router.Register(MethodGetLocalValidatorIdentity, getLocalValidatorIdentityHandler(backend))
	_ = router.Register(MethodGetValidatorSet, getValidatorSetHandler(backend))
	_ = router.Register(MethodGetNodeStatus, getNodeStatusHandler(backend))
	_ = router.Register(MethodGetPeerNetwork, getPeerNetworkHandler(backend))
	_ = router.Register(MethodGetConsensusStatus, getConsensusStatusHandler(backend))
	_ = router.Register(MethodGetMetrics, getMetricsHandler(backend))
	_ = router.Register(MethodGetHealth, getHealthHandler(backend))
	_ = router.Register(MethodGetValidatorPairing, getValidatorPairingHandler(backend))
	_ = router.Register(MethodCompleteValidatorPairing, completeValidatorPairingHandler(backend))
}

// RegisterPublicHandlers 注册公网 RPC 方法 + 防止管理和私钥代签接口暴露给 APP。
func RegisterPublicHandlers(router *Router, backend LedgerBackend) {
	registerStablecoinHandlers(router, backend)
	registerRWAHandlers(router, backend)
	registerRewardHandlers(router, backend)
	registerMarketHandlers(router, backend)
	_ = router.Register(MethodGetBalance, getBalanceHandler(backend))
	_ = router.Register(MethodGetAccountType, getAccountTypeHandler(backend))
	_ = router.Register(MethodGetAccountInfo, getAccountInfoHandler(backend))
	_ = router.Register(MethodGetLatestBlockhash, getLatestBlockhashHandler(backend))
	_ = router.Register(MethodSendTransaction, sendTransactionHandler(backend))
	_ = router.Register(MethodSimulateTransaction, simulateTransactionHandler(backend))
	_ = router.Register(MethodGetBlock, getBlockHandler(backend))
	_ = router.Register(MethodGetTransaction, getTransactionHandler(backend))
	_ = router.Register(MethodGetAddressTransactions, getAddressTransactionsHandler(backend))
	_ = router.Register(MethodGetContractPrograms, getContractProgramsHandler(backend))
	_ = router.Register(MethodGetAssetState, getAssetStateHandler(backend))
	_ = router.Register(MethodGetShieldedPool, getShieldedPoolHandler(backend))
	_ = router.Register(MethodGetValidatorSet, getValidatorSetHandler(backend))
	_ = router.Register(MethodGetNodeStatus, getNodeStatusHandler(backend))
	_ = router.Register(MethodGetPeerNetwork, getPeerNetworkHandler(backend))
	_ = router.Register(MethodGetHealth, getHealthHandler(backend))
}

func RegisterBootstrapHandlers(router *Router, backend BootstrapNodeBackend) {
	_ = router.Register(MethodBootstrapRegisterValidator, bootstrapRegisterValidatorHandler(backend))
	_ = router.Register(MethodGetBootstrapManifest, getBootstrapManifestHandler(backend))
	_ = router.Register(MethodGetBootstrapStatus, getBootstrapStatusHandler(backend))
	RegisterPublicForwardHandlers(router, backend)
	_ = router.Register(MethodGetNodeStatus, getNodeStatusHandler(backend))
	_ = router.Register(MethodGetPeerNetwork, getPeerNetworkHandler(backend))
	_ = router.Register(MethodGetHealth, getHealthHandler(backend))
}

func RegisterPublicForwardHandlers(router *Router, backend PublicRPCForwardBackend) {
	for _, method := range publicForwardMethods() {
		methodName := method
		_ = router.Register(methodName, publicForwardHandler(backend, methodName))
	}
}

func publicForwardMethods() []string {
	return []string{
		MethodGetBalance,
		MethodGetAccountType,
		MethodGetAccountInfo,
		MethodGetLatestBlockhash,
		MethodSendTransaction,
		MethodSimulateTransaction,
		MethodGetBlock,
		MethodGetTransaction,
		MethodGetAddressTransactions,
		MethodGetContractPrograms,
		MethodGetAssetState,
		MethodGetStablecoinCapabilities,
		MethodGetStablecoins,
		MethodGetStablecoinState,
		MethodGetStablecoinOrders,
		MethodGetRWACapabilities,
		MethodGetRewardCapabilities,
		MethodGetRewardState,
		MethodGetRWAAssets,
		MethodGetRWAState,
		MethodGetRWAProposal,
		MethodGetRWADividend,
		MethodGetShieldedPool,
		MethodGetValidatorSet,
	}
}

func publicForwardHandler(backend PublicRPCForwardBackend, method string) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		if backend == nil {
			return nil, ErrMethodUnavailable
		}
		result, rpcError, err := backend.ForwardPublicRPC(ctx, method, params)
		if err != nil {
			return nil, internalError(fmt.Sprintf("forward public rpc: %v", err))
		}
		if rpcError != nil {
			return nil, rpcError
		}
		if len(result) == 0 {
			return json.RawMessage("null"), nil
		}
		return result, nil
	}
}

func bootstrapRegisterValidatorHandler(backend BootstrapBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		if backend == nil {
			return nil, ErrMethodUnavailable
		}
		request, rpcError := parseBootstrapRegisterValidatorParams(params)
		if rpcError != nil {
			return nil, rpcError
		}
		result, err := backend.BootstrapRegisterValidator(ctx, request)
		if err != nil {
			return nil, internalError(fmt.Sprintf("bootstrap register validator: %v", err))
		}
		return result, nil
	}
}

func getBootstrapManifestHandler(backend BootstrapBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		if backend == nil {
			return nil, ErrMethodUnavailable
		}
		if rpcError := parseNoParams(params); rpcError != nil {
			return nil, rpcError
		}
		result, err := backend.GetBootstrapManifest(ctx)
		if err != nil {
			return nil, internalError(fmt.Sprintf("get bootstrap manifest: %v", err))
		}
		return result, nil
	}
}

func getBootstrapStatusHandler(backend BootstrapBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		if backend == nil {
			return nil, ErrMethodUnavailable
		}
		if rpcError := parseNoParams(params); rpcError != nil {
			return nil, rpcError
		}
		result, err := backend.GetBootstrapStatus(ctx)
		if err != nil {
			return nil, internalError(fmt.Sprintf("get bootstrap status: %v", err))
		}
		return result, nil
	}
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

func getLatestBlockhashHandler(backend LedgerBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		if backend == nil {
			return nil, ErrMethodUnavailable
		}
		if rpcError := parseNoParams(params); rpcError != nil {
			return nil, rpcError
		}
		result, err := backend.GetLatestBlockhash(ctx)
		if err != nil {
			return nil, internalError(fmt.Sprintf("get latest blockhash: %v", err))
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

func signedTransactionRequiredHandler(method string) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		_ = ctx
		_ = params
		return nil, invalidParamsError(method + " requires wallet-local signing; submit the signed transaction with sendTransaction")
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

func getTransactionHandler(backend LedgerBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		transactionBackend, ok := backend.(TransactionLookupBackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		values, rpcError := parseParamsArray(params)
		if rpcError != nil {
			return nil, rpcError
		}
		if len(values) < 1 {
			return nil, invalidParamsError("getTransaction requires signature")
		}
		signature, rpcError := parseStringParam(values[0], "getTransaction signature")
		if rpcError != nil {
			return nil, rpcError
		}
		result, err := transactionBackend.GetTransaction(ctx, signature)
		if err != nil {
			return nil, internalError(fmt.Sprintf("get transaction: %v", err))
		}
		return result, nil
	}
}

func getAddressTransactionsHandler(backend LedgerBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		accountHistoryBackend, ok := backend.(AccountHistoryBackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		values, rpcError := parseParamsArray(params)
		if rpcError != nil {
			return nil, rpcError
		}
		if len(values) < 1 {
			return nil, invalidParamsError("getAddressTransactions requires address")
		}
		address, rpcError := parseStringParam(values[0], "getAddressTransactions address")
		if rpcError != nil {
			return nil, rpcError
		}

		limit := uint64(20)
		if len(values) >= 2 {
			limit, rpcError = parseUint64Param(values[1], "getAddressTransactions limit")
			if rpcError != nil {
				return nil, rpcError
			}
		}

		cursor := ""
		if len(values) >= 3 {
			cursor, rpcError = parseOptionalStringParam(values[2], "getAddressTransactions cursor")
			if rpcError != nil {
				return nil, rpcError
			}
		}

		result, err := accountHistoryBackend.GetAddressTransactions(ctx, address, cursor, int(limit))
		if err != nil {
			return nil, internalError(fmt.Sprintf("get address transactions: %v", err))
		}
		return result, nil
	}
}

func getContractProgramsHandler(backend LedgerBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		contractProgramBackend, ok := backend.(ContractProgramBackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		limit, rpcError := parseContractProgramsParams(params)
		if rpcError != nil {
			return nil, rpcError
		}
		result, err := contractProgramBackend.GetContractPrograms(ctx, limit)
		if err != nil {
			return nil, internalError(fmt.Sprintf("get contract programs: %v", err))
		}
		return result, nil
	}
}

func getAssetStateHandler(backend LedgerBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		assetStateBackend, ok := backend.(AssetStateBackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		program, owner, rpcError := parseAssetStateParams(params)
		if rpcError != nil {
			return nil, rpcError
		}
		result, err := assetStateBackend.GetAssetState(ctx, program, owner)
		if err != nil {
			return nil, internalError(fmt.Sprintf("get asset state: %v", err))
		}
		return result, nil
	}
}

func getShieldedPoolHandler(backend LedgerBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		shieldedPoolBackend, ok := backend.(ShieldedPoolBackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		if rpcError := parseNoParams(params); rpcError != nil {
			return nil, rpcError
		}
		result, err := shieldedPoolBackend.GetShieldedPool(ctx)
		if err != nil {
			return nil, internalError(fmt.Sprintf("get shielded pool: %v", err))
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

func registerValidatorIdentityHandler(backend LedgerBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		joinBackend, ok := backend.(ValidatorJoinBackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		values, rpcError := parseParamsArray(params)
		if rpcError != nil {
			return nil, rpcError
		}
		parsedParams, rpcError := parseRegisterValidatorIdentityParams(values)
		if rpcError != nil {
			return nil, rpcError
		}
		signature, err := joinBackend.RegisterValidatorIdentity(ctx,
			parsedParams.StakerSeed,
			parsedParams.ValidatorAddress,
			parsedParams.ConsensusPublicKey,
			parsedParams.BLSPublicKey,
			parsedParams.PeerID,
			parsedParams.StakeLamports,
		)
		if err != nil {
			return nil, internalError(fmt.Sprintf("register validator identity: %v", err))
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

func getLocalValidatorIdentityHandler(backend LedgerBackend) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		identityBackend, ok := backend.(LocalValidatorIdentityBackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		if rpcError := parseNoParams(params); rpcError != nil {
			return nil, rpcError
		}
		result, err := identityBackend.GetLocalValidatorIdentity(ctx)
		if err != nil {
			return nil, internalError(fmt.Sprintf("get local validator identity: %v", err))
		}
		return result, nil
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

func getNodeStatusHandler(backend any) HandlerFunc {
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

func getPeerNetworkHandler(backend any) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		peerNetworkBackend, ok := backend.(PeerNetworkBackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		if rpcError := parseNoParams(params); rpcError != nil {
			return nil, rpcError
		}
		result, err := peerNetworkBackend.GetPeerNetwork(ctx)
		if err != nil {
			return nil, internalError(fmt.Sprintf("get peer network: %v", err))
		}
		return result, nil
	}
}

func getConsensusStatusHandler(backend any) HandlerFunc {
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

func getMetricsHandler(backend any) HandlerFunc {
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

func getHealthHandler(backend any) HandlerFunc {
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

func getValidatorPairingHandler(backend any) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		pairingBackend, ok := backend.(ValidatorPairingBackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		if rpcError := parseNoParams(params); rpcError != nil {
			return nil, rpcError
		}
		result, err := pairingBackend.GetValidatorPairing(ctx)
		if err != nil {
			return nil, internalError(fmt.Sprintf("get validator pairing: %v", err))
		}
		return result, nil
	}
}

func completeValidatorPairingHandler(backend any) HandlerFunc {
	return func(ctx context.Context, params json.RawMessage) (any, *Error) {
		pairingBackend, ok := backend.(ValidatorPairingBackend)
		if !ok {
			return nil, ErrMethodUnavailable
		}
		request, rpcError := parseCompleteValidatorPairingParams(params)
		if rpcError != nil {
			return nil, rpcError
		}
		result, err := pairingBackend.CompleteValidatorPairing(ctx, request)
		if err != nil {
			return nil, validatorPairingError(err)
		}
		return result, nil
	}
}

func validatorPairingError(err error) *Error {
	message := fmt.Sprintf("complete validator pairing: %v", err)
	if isValidatorPairingInternalError(message) {
		return internalError(message)
	}
	return invalidParamsError(message)
}

func isValidatorPairingInternalError(message string) bool {
	normalizedMessage := strings.ToLower(strings.TrimSpace(message))
	internalMarkers := []string{
		"config path is empty",
		"read config for validator pairing",
		"decode config for validator pairing",
		"encode paired validator config",
		"write paired validator config",
		"replace paired validator config",
	}
	for _, marker := range internalMarkers {
		if strings.Contains(normalizedMessage, marker) {
			return true
		}
	}
	return false
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

type registerValidatorIdentityParams struct {
	StakerSeed         string
	ValidatorAddress   string
	ConsensusPublicKey string
	BLSPublicKey       string
	PeerID             string
	StakeLamports      uint64
}

func parseCompleteValidatorPairingParams(params json.RawMessage) (ValidatorPairingCompleteRequest, *Error) {
	values, rpcError := parseParamsArray(params)
	if rpcError != nil {
		return ValidatorPairingCompleteRequest{}, rpcError
	}
	if len(values) != 1 {
		return ValidatorPairingCompleteRequest{}, invalidParamsError("completeValidatorPairing requires one request object")
	}
	request := ValidatorPairingCompleteRequest{}
	if err := json.Unmarshal(values[0], &request); err != nil {
		return ValidatorPairingCompleteRequest{}, invalidParamsError("completeValidatorPairing request must be an object")
	}
	if request.Token == "" ||
		request.StakerAddress == "" ||
		request.ValidatorAddress == "" ||
		request.ConsensusAddress == "" ||
		request.BLSPublicKey == "" ||
		request.NodePeerID == "" ||
		(request.Signature == "" && request.BootstrapStakerSignature == "") ||
		request.StakeLamports == 0 {
		return ValidatorPairingCompleteRequest{}, invalidParamsError("completeValidatorPairing has empty required fields")
	}
	return request, nil
}

func parseBootstrapRegisterValidatorParams(params json.RawMessage) (BootstrapValidatorRegistrationRequest, *Error) {
	values, rpcError := parseParamsArray(params)
	if rpcError != nil {
		return BootstrapValidatorRegistrationRequest{}, rpcError
	}
	if len(values) != 1 {
		return BootstrapValidatorRegistrationRequest{}, invalidParamsError("bootstrapRegisterValidator requires one registration object")
	}
	request := BootstrapValidatorRegistrationRequest{}
	if err := json.Unmarshal(values[0], &request); err != nil {
		return BootstrapValidatorRegistrationRequest{}, invalidParamsError("bootstrapRegisterValidator registration must be an object")
	}
	// 功能目的：允许空链 ID 发现模式；实现原因：引导节点负责绑定权威链，身份字段仍必须完整。
	if request.NodeName == "" ||
		request.PeerID == "" ||
		request.AdvertisedIP == "" ||
		request.AdvertisedPort == 0 ||
		request.StakerAddress == "" ||
		request.ValidatorAddress == "" ||
		request.ConsensusPublicKey == "" ||
		request.BLSPublicKeyBase64 == "" ||
		request.StakeLamports == 0 ||
		request.RegisteredAtUnixMilli == 0 ||
		request.StakerSignature == "" ||
		request.Signature == "" {
		return BootstrapValidatorRegistrationRequest{}, invalidParamsError("bootstrapRegisterValidator registration has empty required fields")
	}
	return request, nil
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

func parseContractProgramsParams(params json.RawMessage) (int, *Error) {
	if len(params) == 0 || string(params) == "null" {
		return 0, nil
	}
	values, rpcError := parseParamsArray(params)
	if rpcError != nil {
		return 0, rpcError
	}
	if len(values) == 0 {
		return 0, nil
	}
	if len(values) > 1 {
		return 0, invalidParamsError("getContractPrograms accepts at most one limit parameter")
	}
	limit, rpcError := parseUint64Param(values[0], "getContractPrograms limit")
	if rpcError != nil {
		return 0, rpcError
	}
	if limit > uint64(^uint(0)>>1) {
		return 0, invalidParamsError("getContractPrograms limit is too large")
	}
	return int(limit), nil
}

func parseAssetStateParams(params json.RawMessage) (string, string, *Error) {
	values, rpcError := parseParamsArray(params)
	if rpcError != nil {
		return "", "", rpcError
	}
	if len(values) < 2 {
		return "", "", invalidParamsError("getAssetState requires program and owner")
	}
	if len(values) > 2 {
		return "", "", invalidParamsError("getAssetState accepts exactly program and owner")
	}
	program, rpcError := parseStringParam(values[0], "getAssetState program")
	if rpcError != nil {
		return "", "", rpcError
	}
	owner, rpcError := parseStringParam(values[1], "getAssetState owner")
	if rpcError != nil {
		return "", "", rpcError
	}
	return program, owner, nil
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

func parseRegisterValidatorIdentityParams(values []json.RawMessage) (registerValidatorIdentityParams, *Error) {
	if len(values) < 6 {
		return registerValidatorIdentityParams{}, invalidParamsError("registerValidatorIdentity requires staker seed, validator address, consensus public key, bls public key, peer id, stake lamports")
	}
	stakerSeed, rpcError := parseStringParam(values[0], "registerValidatorIdentity staker seed")
	if rpcError != nil {
		return registerValidatorIdentityParams{}, rpcError
	}
	validatorAddress, rpcError := parseStringParam(values[1], "registerValidatorIdentity validator address")
	if rpcError != nil {
		return registerValidatorIdentityParams{}, rpcError
	}
	consensusPublicKey, rpcError := parseStringParam(values[2], "registerValidatorIdentity consensus public key")
	if rpcError != nil {
		return registerValidatorIdentityParams{}, rpcError
	}
	blsPublicKey, rpcError := parseStringParam(values[3], "registerValidatorIdentity bls public key")
	if rpcError != nil {
		return registerValidatorIdentityParams{}, rpcError
	}
	peerID, rpcError := parseStringParam(values[4], "registerValidatorIdentity peer id")
	if rpcError != nil {
		return registerValidatorIdentityParams{}, rpcError
	}
	stakeLamports, rpcError := parseUint64Param(values[5], "registerValidatorIdentity stake lamports")
	if rpcError != nil {
		return registerValidatorIdentityParams{}, rpcError
	}
	return registerValidatorIdentityParams{
		StakerSeed:         stakerSeed,
		ValidatorAddress:   validatorAddress,
		ConsensusPublicKey: consensusPublicKey,
		BLSPublicKey:       blsPublicKey,
		PeerID:             peerID,
		StakeLamports:      stakeLamports,
	}, nil
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
