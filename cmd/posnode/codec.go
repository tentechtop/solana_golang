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

type posProposalJSON struct {
	Header          consensus.BlockHeader `json:"header"`
	Transactions    []string              `json:"transactions"`
	LeaderSignature structure.Signature   `json:"leader_signature"`
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
	transactions := make([]string, len(proposal.Transactions))
	for index, transaction := range proposal.Transactions {
		transactionBytes, err := transaction.MarshalBinary()
		if err != nil {
			return p2p.Message{}, fmt.Errorf("posnode: marshal proposal transaction %d: %w", index, err)
		}
		transactions[index] = base64.StdEncoding.EncodeToString(transactionBytes)
	}
	payload, err := json.Marshal(proposalEnvelope{Proposal: posProposalJSON{
		Header:          proposal.Header,
		Transactions:    transactions,
		LeaderSignature: proposal.LeaderSignature,
	}})
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
	transactions := make([]structure.Transaction, len(envelope.Proposal.Transactions))
	for index, encodedTransaction := range envelope.Proposal.Transactions {
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
		Header:          envelope.Proposal.Header,
		Transactions:    transactions,
		LeaderSignature: envelope.Proposal.LeaderSignature,
	}, nil
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
