package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

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
	Vote      consensus.Vote      `json:"vote"`
	PublicKey structure.PublicKey `json:"public_key"`
	Signature structure.Signature `json:"signature"`
}

type qcEnvelope struct {
	QC consensus.QuorumCertificate `json:"qc"`
}

type blockHashRequestEnvelope struct {
	Hash string `json:"hash"`
}

type blockHeightRequestEnvelope struct {
	Height uint64 `json:"height"`
}

type blockResponseEnvelope struct {
	Found    bool            `json:"found"`
	Hash     string          `json:"hash,omitempty"`
	Proposal posProposalJSON `json:"proposal"`
	Error    string          `json:"error,omitempty"`
}

type stateSnapshotRequestEnvelope struct {
	BlockHash string `json:"block_hash"`
}

type stateSnapshotResponseEnvelope struct {
	Found     bool                  `json:"found"`
	BlockHash string                `json:"block_hash,omitempty"`
	StateRoot string                `json:"state_root,omitempty"`
	Accounts  []accountSnapshotJSON `json:"accounts,omitempty"`
	Error     string                `json:"error,omitempty"`
}

type accountSnapshotJSON struct {
	Address string `json:"address"`
	Account string `json:"account"`
}

type statusResponseEnvelope struct {
	NodeName        string              `json:"node_name"`
	PeerID          string              `json:"peer_id"`
	HeadHeight      uint64              `json:"head_height"`
	HeadSlot        uint64              `json:"head_slot"`
	HeadHash        string              `json:"head_hash"`
	FinalizedHeight uint64              `json:"finalized_height"`
	FinalizedHash   string              `json:"finalized_hash"`
	EpochID         uint64              `json:"epoch_id"`
	MempoolSize     int                 `json:"mempool_size"`
	ValidatorCount  int                 `json:"validator_count"`
	KnownPeerCount  int                 `json:"known_peer_count"`
	CurrentLeader   string              `json:"current_leader,omitempty"`
	UpcomingLeaders []leaderSlotJSON    `json:"upcoming_leaders,omitempty"`
	Metrics         nodeMetricsSnapshot `json:"metrics"`
}

type leaderSlotJSON struct {
	Slot        uint64 `json:"slot"`
	ValidatorID string `json:"validator_id"`
	PeerID      string `json:"peer_id"`
}

type posProposalJSON struct {
	Header          consensus.BlockHeader         `json:"header"`
	Transactions    []string                      `json:"transactions"`
	RewardQCs       []consensus.QuorumCertificate `json:"reward_qcs,omitempty"`
	Rewards         []consensus.BlockReward       `json:"rewards,omitempty"`
	LeaderSignature structure.Signature           `json:"leader_signature"`
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

func encodeVoteMessage(vote consensus.Vote, keyPair structure.SolanaKeyPair) (p2p.Message, error) {
	voteBytes, err := vote.MarshalBinary()
	if err != nil {
		return p2p.Message{}, err
	}
	signature, err := keyPair.Sign(voteBytes)
	if err != nil {
		return p2p.Message{}, fmt.Errorf("posnode: sign vote: %w", err)
	}
	payload, err := json.Marshal(voteEnvelope{Vote: vote, PublicKey: keyPair.PublicKey, Signature: signature})
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
