package posnode

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"solana_golang/blockchain"
	"solana_golang/consensus"
	"solana_golang/p2p"
	"solana_golang/structure"
)

type transactionEnvelope struct {
	Transaction string `json:"transaction"`
}

type proposalEnvelope struct {
	Proposal posProposalJSON `json:"proposal"`
}

type voteEnvelope struct {
	Vote         consensus.Vote      `json:"vote"`
	PublicKey    structure.PublicKey `json:"public_key"`
	Signature    structure.Signature `json:"signature"`
	BLSPublicKey []byte              `json:"bls_public_key,omitempty"`
	BLSSignature []byte              `json:"bls_signature,omitempty"`
}

type qcEnvelope struct {
	QC consensus.QuorumCertificate `json:"qc"`
}

type evidenceEnvelope struct {
	Evidence consensus.SlashingEvidence `json:"evidence"`
}

type blockHashRequestEnvelope struct {
	Hash string `json:"hash"`
}

type blockHeightRequestEnvelope struct {
	Height uint64 `json:"height"`
}

type blockLocatorRequestEnvelope struct {
	MaxEntries int `json:"max_entries,omitempty"`
}

type blockResponseEnvelope struct {
	Found    bool            `json:"found"`
	Hash     string          `json:"hash,omitempty"`
	Proposal posProposalJSON `json:"proposal"`
	Error    string          `json:"error,omitempty"`
}

type blockLocatorEntryJSON struct {
	Height uint64 `json:"height"`
	Hash   string `json:"hash"`
}

type blockLocatorResponseEnvelope struct {
	Entries []blockLocatorEntryJSON `json:"entries,omitempty"`
	Error   string                  `json:"error,omitempty"`
}

type commonAncestorRequestEnvelope struct {
	Locator []blockLocatorEntryJSON `json:"locator,omitempty"`
}

type commonAncestorResponseEnvelope struct {
	Found    bool                  `json:"found"`
	Ancestor blockLocatorEntryJSON `json:"ancestor"`
	Error    string                `json:"error,omitempty"`
}

type stateSnapshotRequestEnvelope struct {
	BlockHash string `json:"block_hash"`
}

type stateSnapshotResponseEnvelope struct {
	Found             bool                  `json:"found"`
	ChainID           string                `json:"chain_id,omitempty"`
	ChainIdentityHash string                `json:"chain_identity_hash,omitempty"`
	GenesisHash       string                `json:"genesis_hash,omitempty"`
	BlockHash         string                `json:"block_hash,omitempty"`
	StateRoot         string                `json:"state_root,omitempty"`
	Accounts          []accountSnapshotJSON `json:"accounts,omitempty"`
	Error             string                `json:"error,omitempty"`
}

type accountSnapshotJSON struct {
	Address string `json:"address"`
	Account string `json:"account"`
}

type statusResponseEnvelope struct {
	ChainID           string              `json:"chain_id"`
	ChainIdentityHash string              `json:"chain_identity_hash"`
	GenesisHash       string              `json:"genesis_hash"`
	NodeName          string              `json:"node_name"`
	PeerID            string              `json:"peer_id"`
	HeadHeight        uint64              `json:"head_height"`
	HeadSlot          uint64              `json:"head_slot"`
	HeadHash          string              `json:"head_hash"`
	HeadQCHash        string              `json:"head_qc_hash,omitempty"`
	FinalizedHeight   uint64              `json:"finalized_height"`
	FinalizedHash     string              `json:"finalized_hash"`
	FinalityDepth     uint64              `json:"finality_depth"`
	EpochID           uint64              `json:"epoch_id"`
	MempoolSize       int                 `json:"mempool_size"`
	ValidatorCount    int                 `json:"validator_count"`
	KnownPeerCount    int                 `json:"known_peer_count"`
	P2PSecure         bool                `json:"p2p_secure_session"`
	P2PInsecure       bool                `json:"p2p_insecure_allowed"`
	StateRecovery     bool                `json:"state_recovery_enabled"`
	CurrentLeader     string              `json:"current_leader,omitempty"`
	UpcomingLeaders   []leaderSlotJSON    `json:"upcoming_leaders,omitempty"`
	Turbine           turbinePositionJSON `json:"turbine"`
	TransactionFast   transactionFastJSON `json:"transaction_fast_path"`
	Consensus         consensusStatusJSON `json:"consensus"`
	Metrics           nodeMetricsSnapshot `json:"metrics"`
}

type leaderSlotJSON struct {
	Slot        uint64 `json:"slot"`
	ValidatorID string `json:"validator_id"`
	PeerID      string `json:"peer_id"`
}

type turbinePositionJSON struct {
	Slot             uint64   `json:"slot"`
	Fanout           int      `json:"fanout"`
	Layer            int      `json:"layer"`
	LeaderID         string   `json:"leader_id,omitempty"`
	LeaderPeerID     string   `json:"leader_peer_id,omitempty"`
	ParentValidator  string   `json:"parent_validator_id,omitempty"`
	ParentPeerID     string   `json:"parent_peer_id,omitempty"`
	ChildValidators  []string `json:"child_validator_ids,omitempty"`
	ChildPeerIDs     []string `json:"child_peer_ids,omitempty"`
	ValidatorInTree  bool     `json:"validator_in_tree"`
	TurbineAvailable bool     `json:"turbine_available"`
}

type transactionFastJSON struct {
	StartSlot         uint64           `json:"start_slot"`
	ForwardSlots      int              `json:"forward_slots"`
	LeaderSlots       []leaderSlotJSON `json:"leader_slots,omitempty"`
	ValidatorPeerIDs  []string         `json:"validator_peer_ids,omitempty"`
	PreferredPeerIDs  []string         `json:"preferred_peer_ids,omitempty"`
	ForwardValidators bool             `json:"forward_validators"`
	FastPathAvailable bool             `json:"fast_path_available"`
}

type consensusStatusJSON struct {
	Available        bool                           `json:"available"`
	Slot             uint64                         `json:"slot"`
	EpochID          uint64                         `json:"epoch_id"`
	EpochStartSlot   uint64                         `json:"epoch_start_slot"`
	EpochEndSlot     uint64                         `json:"epoch_end_slot"`
	TotalActiveStake uint64                         `json:"total_active_stake_lamports"`
	ValidatorCount   int                            `json:"validator_count"`
	LocalValidatorID string                         `json:"local_validator_id,omitempty"`
	LocalValidator   consensusValidatorStatusJSON   `json:"local_validator"`
	Validators       []consensusValidatorStatusJSON `json:"validators,omitempty"`
}

type consensusValidatorStatusJSON struct {
	ValidatorID                string   `json:"validator_id"`
	AccountAddress             string   `json:"account_address,omitempty"`
	ConsensusPublicKey         string   `json:"consensus_public_key,omitempty"`
	P2PPeerID                  string   `json:"p2p_peer_id,omitempty"`
	Status                     string   `json:"status,omitempty"`
	InCurrentEpoch             bool     `json:"in_current_epoch"`
	InTurbineTree              bool     `json:"in_turbine_tree"`
	TurbineLayer               int      `json:"turbine_layer"`
	TurbineParentValidatorID   string   `json:"turbine_parent_validator_id,omitempty"`
	TurbineParentPeerID        string   `json:"turbine_parent_peer_id,omitempty"`
	TurbineChildValidatorIDs   []string `json:"turbine_child_validator_ids,omitempty"`
	TurbineChildPeerIDs        []string `json:"turbine_child_peer_ids,omitempty"`
	EffectiveStakeLamports     uint64   `json:"effective_stake_lamports"`
	WeightBps                  uint64   `json:"weight_bps"`
	ActiveStakeLamports        uint64   `json:"active_stake_lamports"`
	PendingStakeLamports       uint64   `json:"pending_stake_lamports"`
	UnlockingStakeLamports     uint64   `json:"unlocking_stake_lamports"`
	ActivationEpoch            uint64   `json:"activation_epoch"`
	DeactivationEpoch          uint64   `json:"deactivation_epoch"`
	LastEffectiveStakeLamports uint64   `json:"last_effective_stake_lamports"`
	JailUntilEpoch             uint64   `json:"jail_until_epoch"`
	CommissionBps              uint16   `json:"commission_bps"`
}

type posProposalJSON struct {
	Header          consensus.BlockHeader         `json:"header"`
	Transactions    []string                      `json:"transactions"`
	RewardQCs       []consensus.QuorumCertificate `json:"reward_qcs,omitempty"`
	Evidence        []consensus.SlashingEvidence  `json:"evidence,omitempty"`
	Rewards         []consensus.BlockReward       `json:"rewards,omitempty"`
	LeaderSignature structure.Signature           `json:"leader_signature"`
}

func encodeBlockLocatorEntries(entries []blockchain.BlockLocatorEntry) []blockLocatorEntryJSON {
	encoded := make([]blockLocatorEntryJSON, 0, len(entries))
	for _, entry := range entries {
		encoded = append(encoded, blockLocatorEntryJSON{
			Height: entry.Height,
			Hash:   entry.BlockHash.String(),
		})
	}
	return encoded
}

func decodeBlockLocatorEntries(entries []blockLocatorEntryJSON) ([]blockchain.BlockLocatorEntry, error) {
	decoded := make([]blockchain.BlockLocatorEntry, 0, len(entries))
	for index, entry := range entries {
		blockHash, err := structure.HashFromBase58(entry.Hash)
		if err != nil {
			return nil, fmt.Errorf("posnode: decode locator entry %d: %w", index, err)
		}
		decoded = append(decoded, blockchain.BlockLocatorEntry{
			Height:    entry.Height,
			BlockHash: blockHash,
		})
	}
	return decoded, nil
}

func encodeTransactionMessage(transaction structure.Transaction) (p2p.Message, error) {
	transactionBytes, err := transaction.MarshalBinary()
	if err != nil {
		return p2p.Message{}, err
	}
	payload, err := json.Marshal(transactionEnvelope{Transaction: base64.StdEncoding.EncodeToString(transactionBytes)})
	if err != nil {
		return p2p.Message{}, fmt.Errorf("posnode: marshal transaction envelope: %w", err)
	}
	return p2p.NewMessage(p2p.ProtocolPoSTransactionV1, payload)
}

func decodeTransactionMessage(message p2p.Message) (structure.Transaction, error) {
	envelope := transactionEnvelope{}
	if err := json.Unmarshal(message.Payload, &envelope); err != nil {
		return structure.Transaction{}, fmt.Errorf("posnode: decode transaction envelope: %w", err)
	}
	data, err := base64.StdEncoding.DecodeString(envelope.Transaction)
	if err != nil {
		return structure.Transaction{}, fmt.Errorf("posnode: decode transaction bytes: %w", err)
	}
	return structure.UnmarshalTransactionBinary(data)
}

func encodeProposalMessage(proposal consensus.BlockProposal) (p2p.Message, error) {
	proposalJSON, err := proposalToJSON(proposal)
	if err != nil {
		return p2p.Message{}, err
	}
	payload, err := json.Marshal(proposalEnvelope{Proposal: proposalJSON})
	if err != nil {
		return p2p.Message{}, fmt.Errorf("posnode: marshal proposal envelope: %w", err)
	}
	return p2p.NewMessage(p2p.ProtocolPoSProposalV1, payload)
}

func decodeProposalMessage(message p2p.Message) (consensus.BlockProposal, error) {
	envelope := proposalEnvelope{}
	if err := json.Unmarshal(message.Payload, &envelope); err != nil {
		return consensus.BlockProposal{}, fmt.Errorf("posnode: decode proposal envelope: %w", err)
	}
	return proposalFromJSON(envelope.Proposal)
}

func encodeVoteMessage(vote consensus.Vote, keyPair structure.SolanaKeyPair, blsKeyPair consensus.BLSKeyPair) (p2p.Message, error) {
	voteBytes, err := vote.MarshalBinary()
	if err != nil {
		return p2p.Message{}, err
	}
	signature, err := keyPair.Sign(voteBytes)
	if err != nil {
		return p2p.Message{}, fmt.Errorf("posnode: sign vote: %w", err)
	}
	envelope := voteEnvelope{Vote: vote, PublicKey: keyPair.PublicKey, Signature: signature}
	if len(blsKeyPair.PrivateKey) > 0 {
		blsSignature, err := consensus.SignBLSVote(blsKeyPair.PrivateKey, vote)
		if err != nil {
			return p2p.Message{}, fmt.Errorf("posnode: sign bls vote: %w", err)
		}
		envelope.BLSPublicKey = append([]byte(nil), blsKeyPair.PublicKey...)
		envelope.BLSSignature = blsSignature
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return p2p.Message{}, fmt.Errorf("posnode: marshal vote envelope: %w", err)
	}
	return p2p.NewMessage(p2p.ProtocolPoSVoteV1, payload)
}

func decodeVoteMessage(message p2p.Message) (voteEnvelope, error) {
	envelope := voteEnvelope{}
	if err := json.Unmarshal(message.Payload, &envelope); err != nil {
		return voteEnvelope{}, fmt.Errorf("posnode: decode vote envelope: %w", err)
	}
	return envelope, envelope.Vote.Validate()
}

func encodeQCMessage(qc consensus.QuorumCertificate) (p2p.Message, error) {
	payload, err := json.Marshal(qcEnvelope{QC: qc})
	if err != nil {
		return p2p.Message{}, fmt.Errorf("posnode: marshal qc envelope: %w", err)
	}
	return p2p.NewMessage(p2p.ProtocolPoSQCV1, payload)
}

func encodeEvidenceMessage(evidence consensus.SlashingEvidence) (p2p.Message, error) {
	payload, err := json.Marshal(evidenceEnvelope{Evidence: evidence})
	if err != nil {
		return p2p.Message{}, fmt.Errorf("posnode: marshal evidence envelope: %w", err)
	}
	return p2p.NewMessage(p2p.ProtocolPoSEvidenceV1, payload)
}

func decodeEvidenceMessage(message p2p.Message) (consensus.SlashingEvidence, error) {
	envelope := evidenceEnvelope{}
	if err := json.Unmarshal(message.Payload, &envelope); err != nil {
		return consensus.SlashingEvidence{}, fmt.Errorf("posnode: decode evidence envelope: %w", err)
	}
	return envelope.Evidence, nil
}

func proposalToJSON(proposal consensus.BlockProposal) (posProposalJSON, error) {
	transactions := make([]string, len(proposal.Transactions))
	for index, transaction := range proposal.Transactions {
		transactionBytes, err := transaction.MarshalBinary()
		if err != nil {
			return posProposalJSON{}, fmt.Errorf("posnode: marshal proposal transaction %d: %w", index, err)
		}
		transactions[index] = base64.StdEncoding.EncodeToString(transactionBytes)
	}
	return posProposalJSON{
		Header:          proposal.Header,
		Transactions:    transactions,
		RewardQCs:       append([]consensus.QuorumCertificate(nil), proposal.RewardQCs...),
		Evidence:        append([]consensus.SlashingEvidence(nil), proposal.Evidence...),
		Rewards:         append([]consensus.BlockReward(nil), proposal.Rewards...),
		LeaderSignature: proposal.LeaderSignature,
	}, nil
}

func proposalFromJSON(proposal posProposalJSON) (consensus.BlockProposal, error) {
	transactions := make([]structure.Transaction, len(proposal.Transactions))
	for index, encodedTransaction := range proposal.Transactions {
		transactionBytes, err := base64.StdEncoding.DecodeString(encodedTransaction)
		if err != nil {
			return consensus.BlockProposal{}, fmt.Errorf("posnode: decode proposal transaction %d: %w", index, err)
		}
		transaction, err := structure.UnmarshalTransactionBinary(transactionBytes)
		if err != nil {
			return consensus.BlockProposal{}, fmt.Errorf("posnode: unmarshal proposal transaction %d: %w", index, err)
		}
		transactions[index] = transaction
	}
	return consensus.BlockProposal{
		Header:          proposal.Header,
		Transactions:    transactions,
		RewardQCs:       append([]consensus.QuorumCertificate(nil), proposal.RewardQCs...),
		Evidence:        append([]consensus.SlashingEvidence(nil), proposal.Evidence...),
		Rewards:         append([]consensus.BlockReward(nil), proposal.Rewards...),
		LeaderSignature: proposal.LeaderSignature,
	}, nil
}

func encodeStateSnapshotResponse(blockHash structure.Hash, state consensus.ChainState) (stateSnapshotResponseEnvelope, error) {
	stateRoot, err := state.RootHash()
	if err != nil {
		return stateSnapshotResponseEnvelope{}, err
	}
	accounts := make([]accountSnapshotJSON, len(state.Accounts))
	for index, addressedAccount := range state.Accounts {
		accountBytes, err := addressedAccount.Account.MarshalBinary()
		if err != nil {
			return stateSnapshotResponseEnvelope{}, fmt.Errorf("posnode: marshal snapshot account %d: %w", index, err)
		}
		accounts[index] = accountSnapshotJSON{
			Address: addressedAccount.Address.String(),
			Account: base64.StdEncoding.EncodeToString(accountBytes),
		}
	}
	return stateSnapshotResponseEnvelope{
		Found:     true,
		BlockHash: blockHash.String(),
		StateRoot: stateRoot.String(),
		Accounts:  accounts,
	}, nil
}

func decodeStateSnapshotResponse(response stateSnapshotResponseEnvelope) (structure.Hash, consensus.ChainState, error) {
	blockHash, err := structure.HashFromBase58(response.BlockHash)
	if err != nil {
		return structure.Hash{}, consensus.ChainState{}, fmt.Errorf("posnode: decode snapshot block hash: %w", err)
	}
	accounts := make([]structure.AddressedAccount, len(response.Accounts))
	for index, accountJSON := range response.Accounts {
		address, err := structure.PublicKeyFromBase58(accountJSON.Address)
		if err != nil {
			return structure.Hash{}, consensus.ChainState{}, fmt.Errorf("posnode: decode snapshot account address %d: %w", index, err)
		}
		accountBytes, err := base64.StdEncoding.DecodeString(accountJSON.Account)
		if err != nil {
			return structure.Hash{}, consensus.ChainState{}, fmt.Errorf("posnode: decode snapshot account bytes %d: %w", index, err)
		}
		account, err := structure.UnmarshalAccountBinary(accountBytes)
		if err != nil {
			return structure.Hash{}, consensus.ChainState{}, fmt.Errorf("posnode: unmarshal snapshot account %d: %w", index, err)
		}
		accounts[index] = structure.AddressedAccount{Address: address, Account: account}
	}
	state := consensus.ChainState{Accounts: accounts}
	stateRoot, err := state.RootHash()
	if err != nil {
		return structure.Hash{}, consensus.ChainState{}, err
	}
	if stateRoot.String() != response.StateRoot {
		return structure.Hash{}, consensus.ChainState{}, fmt.Errorf("posnode: snapshot state root mismatch")
	}
	return blockHash, state, nil
}
