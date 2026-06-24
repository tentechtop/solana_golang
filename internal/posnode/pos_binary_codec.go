package posnode

import (
	"fmt"

	"solana_golang/codec/borsh"
	"solana_golang/consensus"
	"solana_golang/p2p"
	"solana_golang/structure"
)

const (
	posPayloadMagic        uint32 = 0x504f5332
	posPayloadVersion      uint16 = 1
	posMaxContainerLength         = p2p.DefaultMaxMessageSize
	posMaxListEntries             = 16 * 1024
	posMaxSnapshotAccounts        = 64 * 1024
	posMaxEvidenceDepth           = 2
)

func marshalTransactionEnvelopeBinary(envelope transactionEnvelope) ([]byte, error) {
	writer := newPOSPayloadWriter(p2p.ProtocolPoSTransactionV1)
	transactionBytes, err := envelope.Transaction.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("posnode: marshal transaction: %w", err)
	}
	if err := writer.WriteBytes(transactionBytes); err != nil {
		return nil, fmt.Errorf("posnode: marshal transaction bytes: %w", err)
	}
	if err := writeRouteFields(writer, envelope.OriginPeerID, envelope.HopCount, envelope.MaxHops); err != nil {
		return nil, err
	}
	return writer.Bytes(), nil
}

func unmarshalTransactionEnvelopeBinary(data []byte) (transactionEnvelope, error) {
	reader, err := newPOSPayloadReader(data, p2p.ProtocolPoSTransactionV1)
	if err != nil {
		return transactionEnvelope{}, err
	}
	transactionBytes, err := reader.ReadBytes()
	if err != nil {
		return transactionEnvelope{}, fmt.Errorf("posnode: unmarshal transaction bytes: %w", err)
	}
	transaction, err := structure.UnmarshalTransactionBinary(transactionBytes)
	if err != nil {
		return transactionEnvelope{}, fmt.Errorf("posnode: unmarshal transaction: %w", err)
	}
	originPeerID, hopCount, maxHops, err := readRouteFields(reader)
	if err != nil {
		return transactionEnvelope{}, err
	}
	if err := reader.EnsureEOF(); err != nil {
		return transactionEnvelope{}, fmt.Errorf("posnode: transaction envelope eof: %w", err)
	}
	return transactionEnvelope{
		Transaction:  transaction,
		OriginPeerID: originPeerID,
		HopCount:     hopCount,
		MaxHops:      maxHops,
	}, nil
}

func marshalProposalEnvelopeBinary(envelope proposalEnvelope) ([]byte, error) {
	writer := newPOSPayloadWriter(p2p.ProtocolPoSProposalV1)
	if err := writeBlockProposalBinary(writer, envelope.Proposal, 0); err != nil {
		return nil, err
	}
	return writer.Bytes(), nil
}

func unmarshalProposalEnvelopeBinary(data []byte) (proposalEnvelope, error) {
	reader, err := newPOSPayloadReader(data, p2p.ProtocolPoSProposalV1)
	if err != nil {
		return proposalEnvelope{}, err
	}
	proposal, err := readBlockProposalBinary(reader, 0)
	if err != nil {
		return proposalEnvelope{}, err
	}
	if err := reader.EnsureEOF(); err != nil {
		return proposalEnvelope{}, fmt.Errorf("posnode: proposal envelope eof: %w", err)
	}
	return proposalEnvelope{Proposal: proposal}, nil
}

func marshalVoteEnvelopeBinary(envelope voteEnvelope) ([]byte, error) {
	writer := newPOSPayloadWriter(p2p.ProtocolPoSVoteV1)
	voteBytes, err := envelope.Vote.MarshalBinary()
	if err != nil {
		return nil, err
	}
	if err := writer.WriteBytes(voteBytes); err != nil {
		return nil, fmt.Errorf("posnode: marshal vote bytes: %w", err)
	}
	writer.WriteFixedBytes(envelope.PublicKey[:])
	writer.WriteFixedBytes(envelope.Signature[:])
	if err := writer.WriteBytes(envelope.BLSPublicKey); err != nil {
		return nil, fmt.Errorf("posnode: marshal bls public key: %w", err)
	}
	if err := writer.WriteBytes(envelope.BLSSignature); err != nil {
		return nil, fmt.Errorf("posnode: marshal bls signature: %w", err)
	}
	if err := writeRouteFields(writer, envelope.OriginPeerID, envelope.HopCount, envelope.MaxHops); err != nil {
		return nil, err
	}
	return writer.Bytes(), nil
}

func unmarshalVoteEnvelopeBinary(data []byte) (voteEnvelope, error) {
	reader, err := newPOSPayloadReader(data, p2p.ProtocolPoSVoteV1)
	if err != nil {
		return voteEnvelope{}, err
	}
	envelope, err := readVoteEnvelopeFields(reader)
	if err != nil {
		return voteEnvelope{}, err
	}
	if err := reader.EnsureEOF(); err != nil {
		return voteEnvelope{}, fmt.Errorf("posnode: vote envelope eof: %w", err)
	}
	return envelope, nil
}

func marshalQCEnvelopeBinary(envelope qcEnvelope) ([]byte, error) {
	writer := newPOSPayloadWriter(p2p.ProtocolPoSQCV1)
	if err := writeQCEnvelopeFields(writer, envelope); err != nil {
		return nil, err
	}
	return writer.Bytes(), nil
}

func unmarshalQCEnvelopeBinary(data []byte) (qcEnvelope, error) {
	reader, err := newPOSPayloadReader(data, p2p.ProtocolPoSQCV1)
	if err != nil {
		return qcEnvelope{}, err
	}
	envelope, err := readQCEnvelopeFields(reader)
	if err != nil {
		return qcEnvelope{}, err
	}
	if err := reader.EnsureEOF(); err != nil {
		return qcEnvelope{}, fmt.Errorf("posnode: qc envelope eof: %w", err)
	}
	return envelope, nil
}

func marshalEvidenceEnvelopeBinary(envelope evidenceEnvelope) ([]byte, error) {
	writer := newPOSPayloadWriter(p2p.ProtocolPoSEvidenceV1)
	if err := writeSlashingEvidenceBinary(writer, envelope.Evidence, 0); err != nil {
		return nil, err
	}
	return writer.Bytes(), nil
}

func unmarshalEvidenceEnvelopeBinary(data []byte) (evidenceEnvelope, error) {
	reader, err := newPOSPayloadReader(data, p2p.ProtocolPoSEvidenceV1)
	if err != nil {
		return evidenceEnvelope{}, err
	}
	evidence, err := readSlashingEvidenceBinary(reader, 0)
	if err != nil {
		return evidenceEnvelope{}, err
	}
	if err := reader.EnsureEOF(); err != nil {
		return evidenceEnvelope{}, fmt.Errorf("posnode: evidence envelope eof: %w", err)
	}
	return evidenceEnvelope{Evidence: evidence}, nil
}

func marshalBlockHashRequestBinary(request blockHashRequestEnvelope) ([]byte, error) {
	writer := newPOSPayloadWriter(p2p.ProtocolPoSBlockByHashV1)
	if err := writeHashString(writer, request.Hash, "block hash request"); err != nil {
		return nil, err
	}
	return writer.Bytes(), nil
}

func unmarshalBlockHashRequestBinary(data []byte) (blockHashRequestEnvelope, error) {
	reader, err := newPOSPayloadReader(data, p2p.ProtocolPoSBlockByHashV1)
	if err != nil {
		return blockHashRequestEnvelope{}, err
	}
	hash, err := readHashString(reader, "block hash request")
	if err != nil {
		return blockHashRequestEnvelope{}, err
	}
	if err := reader.EnsureEOF(); err != nil {
		return blockHashRequestEnvelope{}, fmt.Errorf("posnode: block hash request eof: %w", err)
	}
	return blockHashRequestEnvelope{Hash: hash}, nil
}

func marshalBlockHeightRequestBinary(request blockHeightRequestEnvelope) ([]byte, error) {
	writer := newPOSPayloadWriter(p2p.ProtocolPoSBlockByHeightV1)
	writer.WriteUint64(request.Height)
	return writer.Bytes(), nil
}

func unmarshalBlockHeightRequestBinary(data []byte) (blockHeightRequestEnvelope, error) {
	reader, err := newPOSPayloadReader(data, p2p.ProtocolPoSBlockByHeightV1)
	if err != nil {
		return blockHeightRequestEnvelope{}, err
	}
	height, err := reader.ReadUint64()
	if err != nil {
		return blockHeightRequestEnvelope{}, fmt.Errorf("posnode: unmarshal block height: %w", err)
	}
	if err := reader.EnsureEOF(); err != nil {
		return blockHeightRequestEnvelope{}, fmt.Errorf("posnode: block height request eof: %w", err)
	}
	return blockHeightRequestEnvelope{Height: height}, nil
}

func marshalBlockLocatorRequestBinary(request blockLocatorRequestEnvelope) ([]byte, error) {
	writer := newPOSPayloadWriter(p2p.ProtocolPoSBlockLocatorV1)
	if request.MaxEntries < 0 {
		return nil, fmt.Errorf("posnode: negative block locator entries")
	}
	if request.MaxEntries > posMaxListEntries {
		return nil, fmt.Errorf("posnode: block locator entries exceed limit")
	}
	writer.WriteUint32(uint32(request.MaxEntries))
	return writer.Bytes(), nil
}

func unmarshalBlockLocatorRequestBinary(data []byte) (blockLocatorRequestEnvelope, error) {
	reader, err := newPOSPayloadReader(data, p2p.ProtocolPoSBlockLocatorV1)
	if err != nil {
		return blockLocatorRequestEnvelope{}, err
	}
	maxEntries, err := reader.ReadUint32()
	if err != nil {
		return blockLocatorRequestEnvelope{}, fmt.Errorf("posnode: unmarshal block locator max entries: %w", err)
	}
	if err := reader.EnsureEOF(); err != nil {
		return blockLocatorRequestEnvelope{}, fmt.Errorf("posnode: block locator request eof: %w", err)
	}
	return blockLocatorRequestEnvelope{MaxEntries: int(maxEntries)}, nil
}

func marshalBlockResponseBinary(protocolID p2p.ProtocolID, response blockResponseEnvelope) ([]byte, error) {
	writer := newPOSPayloadWriter(protocolID)
	writer.WriteBool(response.Found)
	if err := writeOptionalHashString(writer, response.Hash, "block response hash"); err != nil {
		return nil, err
	}
	if err := writer.WriteString(response.Error); err != nil {
		return nil, fmt.Errorf("posnode: marshal block response error: %w", err)
	}
	if response.Found {
		if err := writeBlockProposalBinary(writer, response.Proposal, 0); err != nil {
			return nil, err
		}
	}
	return writer.Bytes(), nil
}

func unmarshalBlockResponseBinary(protocolID p2p.ProtocolID, data []byte) (blockResponseEnvelope, error) {
	reader, err := newPOSPayloadReader(data, protocolID)
	if err != nil {
		return blockResponseEnvelope{}, err
	}
	found, err := reader.ReadBool()
	if err != nil {
		return blockResponseEnvelope{}, fmt.Errorf("posnode: unmarshal block response found: %w", err)
	}
	hash, err := readOptionalHashString(reader, "block response hash")
	if err != nil {
		return blockResponseEnvelope{}, err
	}
	errorText, err := reader.ReadString()
	if err != nil {
		return blockResponseEnvelope{}, fmt.Errorf("posnode: unmarshal block response error: %w", err)
	}
	response := blockResponseEnvelope{Found: found, Hash: hash, Error: errorText}
	if found {
		proposal, err := readBlockProposalBinary(reader, 0)
		if err != nil {
			return blockResponseEnvelope{}, err
		}
		response.Proposal = proposal
	}
	if err := reader.EnsureEOF(); err != nil {
		return blockResponseEnvelope{}, fmt.Errorf("posnode: block response eof: %w", err)
	}
	return response, nil
}

func marshalBlockLocatorResponseBinary(response blockLocatorResponseEnvelope) ([]byte, error) {
	writer := newPOSPayloadWriter(p2p.ProtocolPoSBlockLocatorV1)
	if err := writeBlockLocatorEntriesBinary(writer, response.Entries); err != nil {
		return nil, err
	}
	if err := writer.WriteString(response.Error); err != nil {
		return nil, fmt.Errorf("posnode: marshal locator response error: %w", err)
	}
	return writer.Bytes(), nil
}

func unmarshalBlockLocatorResponseBinary(data []byte) (blockLocatorResponseEnvelope, error) {
	reader, err := newPOSPayloadReader(data, p2p.ProtocolPoSBlockLocatorV1)
	if err != nil {
		return blockLocatorResponseEnvelope{}, err
	}
	entries, err := readBlockLocatorEntriesBinary(reader)
	if err != nil {
		return blockLocatorResponseEnvelope{}, err
	}
	errorText, err := reader.ReadString()
	if err != nil {
		return blockLocatorResponseEnvelope{}, fmt.Errorf("posnode: unmarshal locator response error: %w", err)
	}
	if err := reader.EnsureEOF(); err != nil {
		return blockLocatorResponseEnvelope{}, fmt.Errorf("posnode: locator response eof: %w", err)
	}
	return blockLocatorResponseEnvelope{Entries: entries, Error: errorText}, nil
}

func marshalCommonAncestorRequestBinary(request commonAncestorRequestEnvelope) ([]byte, error) {
	writer := newPOSPayloadWriter(p2p.ProtocolPoSCommonAncestorV1)
	if err := writeBlockLocatorEntriesBinary(writer, request.Locator); err != nil {
		return nil, err
	}
	return writer.Bytes(), nil
}

func unmarshalCommonAncestorRequestBinary(data []byte) (commonAncestorRequestEnvelope, error) {
	reader, err := newPOSPayloadReader(data, p2p.ProtocolPoSCommonAncestorV1)
	if err != nil {
		return commonAncestorRequestEnvelope{}, err
	}
	entries, err := readBlockLocatorEntriesBinary(reader)
	if err != nil {
		return commonAncestorRequestEnvelope{}, err
	}
	if err := reader.EnsureEOF(); err != nil {
		return commonAncestorRequestEnvelope{}, fmt.Errorf("posnode: common ancestor request eof: %w", err)
	}
	return commonAncestorRequestEnvelope{Locator: entries}, nil
}

func marshalCommonAncestorResponseBinary(response commonAncestorResponseEnvelope) ([]byte, error) {
	writer := newPOSPayloadWriter(p2p.ProtocolPoSCommonAncestorV1)
	writer.WriteBool(response.Found)
	if err := writer.WriteString(response.Error); err != nil {
		return nil, fmt.Errorf("posnode: marshal common ancestor response error: %w", err)
	}
	if response.Found {
		if err := writeBlockLocatorEntryBinary(writer, response.Ancestor); err != nil {
			return nil, err
		}
	}
	return writer.Bytes(), nil
}

func unmarshalCommonAncestorResponseBinary(data []byte) (commonAncestorResponseEnvelope, error) {
	reader, err := newPOSPayloadReader(data, p2p.ProtocolPoSCommonAncestorV1)
	if err != nil {
		return commonAncestorResponseEnvelope{}, err
	}
	found, err := reader.ReadBool()
	if err != nil {
		return commonAncestorResponseEnvelope{}, fmt.Errorf("posnode: unmarshal common ancestor found: %w", err)
	}
	errorText, err := reader.ReadString()
	if err != nil {
		return commonAncestorResponseEnvelope{}, fmt.Errorf("posnode: unmarshal common ancestor error: %w", err)
	}
	response := commonAncestorResponseEnvelope{Found: found, Error: errorText}
	if found {
		response.Ancestor, err = readBlockLocatorEntryBinary(reader)
		if err != nil {
			return commonAncestorResponseEnvelope{}, err
		}
	}
	if err := reader.EnsureEOF(); err != nil {
		return commonAncestorResponseEnvelope{}, fmt.Errorf("posnode: common ancestor response eof: %w", err)
	}
	return response, nil
}

func marshalStateSnapshotRequestBinary(request stateSnapshotRequestEnvelope) ([]byte, error) {
	writer := newPOSPayloadWriter(p2p.ProtocolPoSStateSnapshotV1)
	if err := writeHashString(writer, request.BlockHash, "state snapshot block hash"); err != nil {
		return nil, err
	}
	return writer.Bytes(), nil
}

func unmarshalStateSnapshotRequestBinary(data []byte) (stateSnapshotRequestEnvelope, error) {
	reader, err := newPOSPayloadReader(data, p2p.ProtocolPoSStateSnapshotV1)
	if err != nil {
		return stateSnapshotRequestEnvelope{}, err
	}
	blockHash, err := readHashString(reader, "state snapshot block hash")
	if err != nil {
		return stateSnapshotRequestEnvelope{}, err
	}
	if err := reader.EnsureEOF(); err != nil {
		return stateSnapshotRequestEnvelope{}, fmt.Errorf("posnode: state snapshot request eof: %w", err)
	}
	return stateSnapshotRequestEnvelope{BlockHash: blockHash}, nil
}

func marshalStateSnapshotResponseBinary(response stateSnapshotResponseEnvelope) ([]byte, error) {
	writer := newPOSPayloadWriter(p2p.ProtocolPoSStateSnapshotV1)
	writer.WriteBool(response.Found)
	if err := writer.WriteString(response.ChainID); err != nil {
		return nil, fmt.Errorf("posnode: marshal snapshot chain id: %w", err)
	}
	if err := writer.WriteString(response.ChainIdentityHash); err != nil {
		return nil, fmt.Errorf("posnode: marshal snapshot identity hash: %w", err)
	}
	if err := writer.WriteString(response.GenesisHash); err != nil {
		return nil, fmt.Errorf("posnode: marshal snapshot genesis hash: %w", err)
	}
	if err := writeOptionalHashString(writer, response.BlockHash, "state snapshot block hash"); err != nil {
		return nil, err
	}
	if err := writeOptionalHashString(writer, response.StateRoot, "state snapshot root"); err != nil {
		return nil, err
	}
	if err := writer.WriteString(response.Error); err != nil {
		return nil, fmt.Errorf("posnode: marshal snapshot error: %w", err)
	}
	if !response.Found {
		return writer.Bytes(), nil
	}
	if len(response.Accounts) > posMaxSnapshotAccounts {
		return nil, fmt.Errorf("posnode: snapshot accounts exceed limit")
	}
	writer.WriteUint32(uint32(len(response.Accounts)))
	for index, account := range response.Accounts {
		writer.WriteFixedBytes(account.Address[:])
		accountBytes, err := account.Account.MarshalBinary()
		if err != nil {
			return nil, fmt.Errorf("posnode: marshal snapshot account %d: %w", index, err)
		}
		if err := writer.WriteBytes(accountBytes); err != nil {
			return nil, fmt.Errorf("posnode: marshal snapshot account bytes %d: %w", index, err)
		}
	}
	return writer.Bytes(), nil
}

func unmarshalStateSnapshotResponseBinary(data []byte) (stateSnapshotResponseEnvelope, error) {
	reader, err := newPOSPayloadReader(data, p2p.ProtocolPoSStateSnapshotV1)
	if err != nil {
		return stateSnapshotResponseEnvelope{}, err
	}
	response, err := readStateSnapshotResponseFields(reader)
	if err != nil {
		return stateSnapshotResponseEnvelope{}, err
	}
	if err := reader.EnsureEOF(); err != nil {
		return stateSnapshotResponseEnvelope{}, fmt.Errorf("posnode: snapshot response eof: %w", err)
	}
	return response, nil
}

func marshalStatusRequestBinary() []byte {
	return newPOSPayloadWriter(p2p.ProtocolPoSStatusV1).Bytes()
}

func unmarshalStatusRequestBinary(data []byte) error {
	reader, err := newPOSPayloadReader(data, p2p.ProtocolPoSStatusV1)
	if err != nil {
		return err
	}
	return reader.EnsureEOF()
}

func marshalStatusResponseBinary(response statusResponseEnvelope) ([]byte, error) {
	writer := newPOSPayloadWriter(p2p.ProtocolPoSStatusV1)
	if err := writeStatusResponseFields(writer, response); err != nil {
		return nil, err
	}
	return writer.Bytes(), nil
}

func unmarshalStatusResponseBinary(data []byte) (statusResponseEnvelope, error) {
	reader, err := newPOSPayloadReader(data, p2p.ProtocolPoSStatusV1)
	if err != nil {
		return statusResponseEnvelope{}, err
	}
	response, err := readStatusResponseFields(reader)
	if err != nil {
		return statusResponseEnvelope{}, err
	}
	if err := reader.EnsureEOF(); err != nil {
		return statusResponseEnvelope{}, fmt.Errorf("posnode: status response eof: %w", err)
	}
	return response, nil
}

func newPOSPayloadWriter(protocolID p2p.ProtocolID) *borsh.Writer {
	writer := borsh.NewWriter(posMaxContainerLength)
	writer.WriteUint32(posPayloadMagic)
	writer.WriteUint16(posPayloadVersion)
	writer.WriteUint32(uint32(protocolID))
	return writer
}

func newPOSPayloadReader(data []byte, protocolID p2p.ProtocolID) (*borsh.Reader, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("posnode: empty binary payload")
	}
	reader := borsh.NewBorrowedReader(data, posMaxContainerLength)
	magic, err := reader.ReadUint32()
	if err != nil {
		return nil, fmt.Errorf("posnode: unmarshal payload magic: %w", err)
	}
	if magic != posPayloadMagic {
		return nil, fmt.Errorf("posnode: invalid payload magic")
	}
	version, err := reader.ReadUint16()
	if err != nil {
		return nil, fmt.Errorf("posnode: unmarshal payload version: %w", err)
	}
	if version != posPayloadVersion {
		return nil, fmt.Errorf("posnode: unsupported payload version %d", version)
	}
	encodedProtocolID, err := reader.ReadUint32()
	if err != nil {
		return nil, fmt.Errorf("posnode: unmarshal payload protocol: %w", err)
	}
	if p2p.ProtocolID(encodedProtocolID) != protocolID {
		return nil, fmt.Errorf("posnode: payload protocol mismatch")
	}
	return reader, nil
}

func writeRouteFields(writer *borsh.Writer, originPeerID string, hopCount uint8, maxHops uint8) error {
	if err := writer.WriteString(originPeerID); err != nil {
		return fmt.Errorf("posnode: marshal route origin peer: %w", err)
	}
	writer.WriteUint8(hopCount)
	writer.WriteUint8(maxHops)
	return nil
}

func readRouteFields(reader *borsh.Reader) (string, uint8, uint8, error) {
	originPeerID, err := reader.ReadString()
	if err != nil {
		return "", 0, 0, fmt.Errorf("posnode: unmarshal route origin peer: %w", err)
	}
	hopCount, err := reader.ReadUint8()
	if err != nil {
		return "", 0, 0, fmt.Errorf("posnode: unmarshal route hop count: %w", err)
	}
	maxHops, err := reader.ReadUint8()
	if err != nil {
		return "", 0, 0, fmt.Errorf("posnode: unmarshal route max hops: %w", err)
	}
	return originPeerID, hopCount, maxHops, nil
}

func writeBlockProposalBinary(writer *borsh.Writer, proposal consensus.BlockProposal, depth int) error {
	if depth > posMaxEvidenceDepth {
		return fmt.Errorf("posnode: proposal evidence nesting exceeds limit")
	}
	if err := writeBlockHeaderBinary(writer, proposal.Header); err != nil {
		return err
	}
	if len(proposal.Transactions) > consensus.MaxProposalTransactions {
		return fmt.Errorf("posnode: proposal transactions exceed limit")
	}
	writer.WriteUint32(uint32(len(proposal.Transactions)))
	for index, transaction := range proposal.Transactions {
		transactionBytes, err := transaction.MarshalBinary()
		if err != nil {
			return fmt.Errorf("posnode: marshal proposal transaction %d: %w", index, err)
		}
		if err := writer.WriteBytes(transactionBytes); err != nil {
			return fmt.Errorf("posnode: marshal proposal transaction bytes %d: %w", index, err)
		}
	}
	if err := writeQCListBinary(writer, proposal.RewardQCs, consensus.MaxRewardQCsPerBlock); err != nil {
		return err
	}
	if err := writeEvidenceListBinary(writer, proposal.Evidence, consensus.MaxSlashingEvidencePerBlock, depth); err != nil {
		return err
	}
	if err := writeRewardListBinary(writer, proposal.Rewards); err != nil {
		return err
	}
	writer.WriteFixedBytes(proposal.LeaderSignature[:])
	return nil
}

func readBlockProposalBinary(reader *borsh.Reader, depth int) (consensus.BlockProposal, error) {
	if depth > posMaxEvidenceDepth {
		return consensus.BlockProposal{}, fmt.Errorf("posnode: proposal evidence nesting exceeds limit")
	}
	header, err := readBlockHeaderBinary(reader)
	if err != nil {
		return consensus.BlockProposal{}, err
	}
	transactionCount, err := readCount(reader, consensus.MaxProposalTransactions, "proposal transactions")
	if err != nil {
		return consensus.BlockProposal{}, err
	}
	transactions := make([]structure.Transaction, transactionCount)
	for index := 0; index < transactionCount; index++ {
		transactionBytes, err := reader.ReadBytes()
		if err != nil {
			return consensus.BlockProposal{}, fmt.Errorf("posnode: unmarshal proposal transaction bytes %d: %w", index, err)
		}
		transactions[index], err = structure.UnmarshalTransactionBinary(transactionBytes)
		if err != nil {
			return consensus.BlockProposal{}, fmt.Errorf("posnode: unmarshal proposal transaction %d: %w", index, err)
		}
	}
	rewardQCs, err := readQCListBinary(reader, consensus.MaxRewardQCsPerBlock, "proposal reward qcs")
	if err != nil {
		return consensus.BlockProposal{}, err
	}
	evidence, err := readEvidenceListBinary(reader, consensus.MaxSlashingEvidencePerBlock, depth)
	if err != nil {
		return consensus.BlockProposal{}, err
	}
	rewards, err := readRewardListBinary(reader)
	if err != nil {
		return consensus.BlockProposal{}, err
	}
	signatureBytes, err := reader.ReadFixedBytes(structure.SignatureSize)
	if err != nil {
		return consensus.BlockProposal{}, fmt.Errorf("posnode: unmarshal proposal signature: %w", err)
	}
	signature, err := structure.SignatureFromBytes(signatureBytes)
	if err != nil {
		return consensus.BlockProposal{}, err
	}
	return consensus.BlockProposal{
		Header:          header,
		Transactions:    transactions,
		RewardQCs:       rewardQCs,
		Evidence:        evidence,
		Rewards:         rewards,
		LeaderSignature: signature,
	}, nil
}

func writeBlockHeaderBinary(writer *borsh.Writer, header consensus.BlockHeader) error {
	if err := writer.WriteString(header.ChainID); err != nil {
		return fmt.Errorf("posnode: marshal header chain id: %w", err)
	}
	writer.WriteUint64(header.Slot)
	writer.WriteUint64(header.Height)
	writer.WriteFixedBytes(header.ParentHash[:])
	writer.WriteFixedBytes(header.PreviousQCHash[:])
	if err := writer.WriteString(string(header.LeaderID)); err != nil {
		return fmt.Errorf("posnode: marshal header leader id: %w", err)
	}
	writer.WriteUint64(header.EpochID)
	writer.WriteFixedBytes(header.TxRoot[:])
	writer.WriteFixedBytes(header.ReceiptRoot[:])
	writer.WriteFixedBytes(header.RewardRoot[:])
	writer.WriteFixedBytes(header.StateRoot[:])
	writer.WriteFixedBytes(header.AccountRoot[:])
	writer.WriteInt64(header.TimestampUnixMilli)
	return nil
}

func readBlockHeaderBinary(reader *borsh.Reader) (consensus.BlockHeader, error) {
	chainID, err := reader.ReadString()
	if err != nil {
		return consensus.BlockHeader{}, fmt.Errorf("posnode: unmarshal header chain id: %w", err)
	}
	slot, err := reader.ReadUint64()
	if err != nil {
		return consensus.BlockHeader{}, fmt.Errorf("posnode: unmarshal header slot: %w", err)
	}
	height, err := reader.ReadUint64()
	if err != nil {
		return consensus.BlockHeader{}, fmt.Errorf("posnode: unmarshal header height: %w", err)
	}
	parentHash, err := readHash(reader, "header parent hash")
	if err != nil {
		return consensus.BlockHeader{}, err
	}
	previousQCHash, err := readHash(reader, "header previous qc hash")
	if err != nil {
		return consensus.BlockHeader{}, err
	}
	leaderID, err := reader.ReadString()
	if err != nil {
		return consensus.BlockHeader{}, fmt.Errorf("posnode: unmarshal header leader id: %w", err)
	}
	epochID, err := reader.ReadUint64()
	if err != nil {
		return consensus.BlockHeader{}, fmt.Errorf("posnode: unmarshal header epoch: %w", err)
	}
	txRoot, err := readHash(reader, "header tx root")
	if err != nil {
		return consensus.BlockHeader{}, err
	}
	receiptRoot, err := readHash(reader, "header receipt root")
	if err != nil {
		return consensus.BlockHeader{}, err
	}
	rewardRoot, err := readHash(reader, "header reward root")
	if err != nil {
		return consensus.BlockHeader{}, err
	}
	stateRoot, err := readHash(reader, "header state root")
	if err != nil {
		return consensus.BlockHeader{}, err
	}
	accountRoot, err := readHash(reader, "header account root")
	if err != nil {
		return consensus.BlockHeader{}, err
	}
	timestamp, err := reader.ReadInt64()
	if err != nil {
		return consensus.BlockHeader{}, fmt.Errorf("posnode: unmarshal header timestamp: %w", err)
	}
	return consensus.BlockHeader{
		ChainID:            chainID,
		Slot:               slot,
		Height:             height,
		ParentHash:         parentHash,
		PreviousQCHash:     previousQCHash,
		LeaderID:           consensus.ValidatorID(leaderID),
		EpochID:            epochID,
		TxRoot:             txRoot,
		ReceiptRoot:        receiptRoot,
		RewardRoot:         rewardRoot,
		StateRoot:          stateRoot,
		AccountRoot:        accountRoot,
		TimestampUnixMilli: timestamp,
	}, nil
}

func writeVoteEnvelopeFields(writer *borsh.Writer, envelope voteEnvelope) error {
	voteBytes, err := envelope.Vote.MarshalBinary()
	if err != nil {
		return err
	}
	if err := writer.WriteBytes(voteBytes); err != nil {
		return fmt.Errorf("posnode: marshal signed vote: %w", err)
	}
	writer.WriteFixedBytes(envelope.PublicKey[:])
	writer.WriteFixedBytes(envelope.Signature[:])
	if err := writer.WriteBytes(envelope.BLSPublicKey); err != nil {
		return fmt.Errorf("posnode: marshal signed vote bls public key: %w", err)
	}
	if err := writer.WriteBytes(envelope.BLSSignature); err != nil {
		return fmt.Errorf("posnode: marshal signed vote bls signature: %w", err)
	}
	return writeRouteFields(writer, envelope.OriginPeerID, envelope.HopCount, envelope.MaxHops)
}

func readVoteEnvelopeFields(reader *borsh.Reader) (voteEnvelope, error) {
	voteBytes, err := reader.ReadBytes()
	if err != nil {
		return voteEnvelope{}, fmt.Errorf("posnode: unmarshal signed vote bytes: %w", err)
	}
	vote, err := consensus.UnmarshalVoteBinary(voteBytes)
	if err != nil {
		return voteEnvelope{}, err
	}
	publicKey, err := readPublicKey(reader, "signed vote public key")
	if err != nil {
		return voteEnvelope{}, err
	}
	signature, err := readSignature(reader, "signed vote signature")
	if err != nil {
		return voteEnvelope{}, err
	}
	blsPublicKey, err := reader.ReadBytes()
	if err != nil {
		return voteEnvelope{}, fmt.Errorf("posnode: unmarshal signed vote bls public key: %w", err)
	}
	blsSignature, err := reader.ReadBytes()
	if err != nil {
		return voteEnvelope{}, fmt.Errorf("posnode: unmarshal signed vote bls signature: %w", err)
	}
	originPeerID, hopCount, maxHops, err := readRouteFields(reader)
	if err != nil {
		return voteEnvelope{}, err
	}
	return voteEnvelope{
		Vote:         vote,
		PublicKey:    publicKey,
		Signature:    signature,
		BLSPublicKey: blsPublicKey,
		BLSSignature: blsSignature,
		OriginPeerID: originPeerID,
		HopCount:     hopCount,
		MaxHops:      maxHops,
	}, nil
}

func writeQCEnvelopeFields(writer *borsh.Writer, envelope qcEnvelope) error {
	qcBytes, err := envelope.QC.MarshalBinary()
	if err != nil {
		return err
	}
	if err := writer.WriteBytes(qcBytes); err != nil {
		return fmt.Errorf("posnode: marshal qc bytes: %w", err)
	}
	if err := writer.WriteString(envelope.RootValidatorID); err != nil {
		return fmt.Errorf("posnode: marshal qc root validator: %w", err)
	}
	return writeRouteFields(writer, envelope.OriginPeerID, envelope.HopCount, envelope.MaxHops)
}

func readQCEnvelopeFields(reader *borsh.Reader) (qcEnvelope, error) {
	qcBytes, err := reader.ReadBytes()
	if err != nil {
		return qcEnvelope{}, fmt.Errorf("posnode: unmarshal qc bytes: %w", err)
	}
	qc, err := consensus.UnmarshalCertificateBinary(qcBytes)
	if err != nil {
		return qcEnvelope{}, err
	}
	rootValidatorID, err := reader.ReadString()
	if err != nil {
		return qcEnvelope{}, fmt.Errorf("posnode: unmarshal qc root validator: %w", err)
	}
	originPeerID, hopCount, maxHops, err := readRouteFields(reader)
	if err != nil {
		return qcEnvelope{}, err
	}
	return qcEnvelope{
		QC:              qc,
		RootValidatorID: rootValidatorID,
		OriginPeerID:    originPeerID,
		HopCount:        hopCount,
		MaxHops:         maxHops,
	}, nil
}

func writeSlashingEvidenceBinary(writer *borsh.Writer, evidence consensus.SlashingEvidence, depth int) error {
	if depth > posMaxEvidenceDepth {
		return fmt.Errorf("posnode: slashing evidence nesting exceeds limit")
	}
	writer.WriteUint8(uint8(evidence.Type))
	switch evidence.Type {
	case consensus.SlashingEvidenceTypeDoubleProposal:
		if evidence.DoubleProposal == nil {
			return fmt.Errorf("posnode: missing double proposal evidence")
		}
		if err := writeBlockProposalBinary(writer, evidence.DoubleProposal.FirstProposal, depth+1); err != nil {
			return err
		}
		return writeBlockProposalBinary(writer, evidence.DoubleProposal.SecondProposal, depth+1)
	case consensus.SlashingEvidenceTypeDoubleVote:
		if evidence.DoubleVote == nil {
			return fmt.Errorf("posnode: missing double vote evidence")
		}
		if err := writeSignedVoteBinary(writer, evidence.DoubleVote.FirstVote); err != nil {
			return err
		}
		return writeSignedVoteBinary(writer, evidence.DoubleVote.SecondVote)
	default:
		return fmt.Errorf("posnode: unsupported evidence type %d", evidence.Type)
	}
}

func readSlashingEvidenceBinary(reader *borsh.Reader, depth int) (consensus.SlashingEvidence, error) {
	if depth > posMaxEvidenceDepth {
		return consensus.SlashingEvidence{}, fmt.Errorf("posnode: slashing evidence nesting exceeds limit")
	}
	evidenceType, err := reader.ReadUint8()
	if err != nil {
		return consensus.SlashingEvidence{}, fmt.Errorf("posnode: unmarshal evidence type: %w", err)
	}
	switch consensus.SlashingEvidenceType(evidenceType) {
	case consensus.SlashingEvidenceTypeDoubleProposal:
		firstProposal, err := readBlockProposalBinary(reader, depth+1)
		if err != nil {
			return consensus.SlashingEvidence{}, err
		}
		secondProposal, err := readBlockProposalBinary(reader, depth+1)
		if err != nil {
			return consensus.SlashingEvidence{}, err
		}
		return consensus.SlashingEvidence{
			Type: consensus.SlashingEvidenceTypeDoubleProposal,
			DoubleProposal: &consensus.DoubleProposalEvidence{
				FirstProposal:  firstProposal,
				SecondProposal: secondProposal,
			},
		}, nil
	case consensus.SlashingEvidenceTypeDoubleVote:
		firstVote, err := readSignedVoteBinary(reader)
		if err != nil {
			return consensus.SlashingEvidence{}, err
		}
		secondVote, err := readSignedVoteBinary(reader)
		if err != nil {
			return consensus.SlashingEvidence{}, err
		}
		return consensus.SlashingEvidence{
			Type: consensus.SlashingEvidenceTypeDoubleVote,
			DoubleVote: &consensus.SignedDoubleVoteEvidence{
				FirstVote:  firstVote,
				SecondVote: secondVote,
			},
		}, nil
	default:
		return consensus.SlashingEvidence{}, fmt.Errorf("posnode: unsupported evidence type %d", evidenceType)
	}
}

func writeSignedVoteBinary(writer *borsh.Writer, vote consensus.SignedVote) error {
	voteBytes, err := vote.Vote.MarshalBinary()
	if err != nil {
		return err
	}
	if err := writer.WriteBytes(voteBytes); err != nil {
		return fmt.Errorf("posnode: marshal evidence vote: %w", err)
	}
	writer.WriteFixedBytes(vote.PublicKey[:])
	writer.WriteFixedBytes(vote.Signature[:])
	return nil
}

func readSignedVoteBinary(reader *borsh.Reader) (consensus.SignedVote, error) {
	voteBytes, err := reader.ReadBytes()
	if err != nil {
		return consensus.SignedVote{}, fmt.Errorf("posnode: unmarshal evidence vote: %w", err)
	}
	vote, err := consensus.UnmarshalVoteBinary(voteBytes)
	if err != nil {
		return consensus.SignedVote{}, err
	}
	publicKey, err := readPublicKey(reader, "evidence vote public key")
	if err != nil {
		return consensus.SignedVote{}, err
	}
	signature, err := readSignature(reader, "evidence vote signature")
	if err != nil {
		return consensus.SignedVote{}, err
	}
	return consensus.SignedVote{Vote: vote, PublicKey: publicKey, Signature: signature}, nil
}

func writeQCListBinary(writer *borsh.Writer, qcs []consensus.QuorumCertificate, maxCount int) error {
	if len(qcs) > maxCount {
		return fmt.Errorf("posnode: qc list exceeds limit")
	}
	writer.WriteUint32(uint32(len(qcs)))
	for index, qc := range qcs {
		qcBytes, err := qc.MarshalBinary()
		if err != nil {
			return fmt.Errorf("posnode: marshal qc %d: %w", index, err)
		}
		if err := writer.WriteBytes(qcBytes); err != nil {
			return fmt.Errorf("posnode: marshal qc bytes %d: %w", index, err)
		}
	}
	return nil
}

func readQCListBinary(reader *borsh.Reader, maxCount int, label string) ([]consensus.QuorumCertificate, error) {
	count, err := readCount(reader, maxCount, label)
	if err != nil {
		return nil, err
	}
	qcs := make([]consensus.QuorumCertificate, count)
	for index := 0; index < count; index++ {
		qcBytes, err := reader.ReadBytes()
		if err != nil {
			return nil, fmt.Errorf("posnode: unmarshal qc bytes %d: %w", index, err)
		}
		qcs[index], err = consensus.UnmarshalCertificateBinary(qcBytes)
		if err != nil {
			return nil, fmt.Errorf("posnode: unmarshal qc %d: %w", index, err)
		}
	}
	return qcs, nil
}

func writeEvidenceListBinary(writer *borsh.Writer, evidence []consensus.SlashingEvidence, maxCount int, depth int) error {
	if len(evidence) > maxCount {
		return fmt.Errorf("posnode: evidence list exceeds limit")
	}
	writer.WriteUint32(uint32(len(evidence)))
	for index, item := range evidence {
		if err := writeSlashingEvidenceBinary(writer, item, depth+1); err != nil {
			return fmt.Errorf("posnode: marshal evidence %d: %w", index, err)
		}
	}
	return nil
}

func readEvidenceListBinary(reader *borsh.Reader, maxCount int, depth int) ([]consensus.SlashingEvidence, error) {
	count, err := readCount(reader, maxCount, "evidence list")
	if err != nil {
		return nil, err
	}
	evidence := make([]consensus.SlashingEvidence, count)
	for index := 0; index < count; index++ {
		evidence[index], err = readSlashingEvidenceBinary(reader, depth+1)
		if err != nil {
			return nil, fmt.Errorf("posnode: unmarshal evidence %d: %w", index, err)
		}
	}
	return evidence, nil
}

func writeRewardListBinary(writer *borsh.Writer, rewards []consensus.BlockReward) error {
	if len(rewards) > consensus.MaxBlockRewards {
		return fmt.Errorf("posnode: reward list exceeds limit")
	}
	writer.WriteUint32(uint32(len(rewards)))
	for _, reward := range rewards {
		writer.WriteUint8(uint8(reward.Type))
		if err := writer.WriteString(reward.ValidatorID); err != nil {
			return fmt.Errorf("posnode: marshal reward validator: %w", err)
		}
		writer.WriteFixedBytes(reward.AccountAddress[:])
		writer.WriteFixedBytes(reward.StakerAddress[:])
		writer.WriteUint64(reward.EpochID)
		writer.WriteUint64(reward.Slot)
		writer.WriteUint64(reward.Lamports)
		writer.WriteUint64(reward.Credits)
	}
	return nil
}

func readRewardListBinary(reader *borsh.Reader) ([]consensus.BlockReward, error) {
	count, err := readCount(reader, consensus.MaxBlockRewards, "reward list")
	if err != nil {
		return nil, err
	}
	rewards := make([]consensus.BlockReward, count)
	for index := 0; index < count; index++ {
		rewardType, err := reader.ReadUint8()
		if err != nil {
			return nil, fmt.Errorf("posnode: unmarshal reward type %d: %w", index, err)
		}
		validatorID, err := reader.ReadString()
		if err != nil {
			return nil, fmt.Errorf("posnode: unmarshal reward validator %d: %w", index, err)
		}
		accountAddress, err := readPublicKey(reader, "reward account")
		if err != nil {
			return nil, err
		}
		stakerAddress, err := readPublicKey(reader, "reward staker")
		if err != nil {
			return nil, err
		}
		epochID, err := reader.ReadUint64()
		if err != nil {
			return nil, fmt.Errorf("posnode: unmarshal reward epoch %d: %w", index, err)
		}
		slot, err := reader.ReadUint64()
		if err != nil {
			return nil, fmt.Errorf("posnode: unmarshal reward slot %d: %w", index, err)
		}
		lamports, err := reader.ReadUint64()
		if err != nil {
			return nil, fmt.Errorf("posnode: unmarshal reward lamports %d: %w", index, err)
		}
		credits, err := reader.ReadUint64()
		if err != nil {
			return nil, fmt.Errorf("posnode: unmarshal reward credits %d: %w", index, err)
		}
		rewards[index] = consensus.BlockReward{
			Type:           consensus.RewardType(rewardType),
			ValidatorID:    validatorID,
			AccountAddress: accountAddress,
			StakerAddress:  stakerAddress,
			EpochID:        epochID,
			Slot:           slot,
			Lamports:       lamports,
			Credits:        credits,
		}
	}
	return rewards, nil
}

func readStateSnapshotResponseFields(reader *borsh.Reader) (stateSnapshotResponseEnvelope, error) {
	found, err := reader.ReadBool()
	if err != nil {
		return stateSnapshotResponseEnvelope{}, fmt.Errorf("posnode: unmarshal snapshot found: %w", err)
	}
	chainID, err := reader.ReadString()
	if err != nil {
		return stateSnapshotResponseEnvelope{}, fmt.Errorf("posnode: unmarshal snapshot chain id: %w", err)
	}
	chainIdentityHash, err := reader.ReadString()
	if err != nil {
		return stateSnapshotResponseEnvelope{}, fmt.Errorf("posnode: unmarshal snapshot identity hash: %w", err)
	}
	genesisHash, err := reader.ReadString()
	if err != nil {
		return stateSnapshotResponseEnvelope{}, fmt.Errorf("posnode: unmarshal snapshot genesis hash: %w", err)
	}
	blockHash, err := readOptionalHashString(reader, "state snapshot block hash")
	if err != nil {
		return stateSnapshotResponseEnvelope{}, err
	}
	stateRoot, err := readOptionalHashString(reader, "state snapshot root")
	if err != nil {
		return stateSnapshotResponseEnvelope{}, err
	}
	errorText, err := reader.ReadString()
	if err != nil {
		return stateSnapshotResponseEnvelope{}, fmt.Errorf("posnode: unmarshal snapshot error: %w", err)
	}
	response := stateSnapshotResponseEnvelope{
		Found:             found,
		ChainID:           chainID,
		ChainIdentityHash: chainIdentityHash,
		GenesisHash:       genesisHash,
		BlockHash:         blockHash,
		StateRoot:         stateRoot,
		Error:             errorText,
	}
	if !found {
		return response, nil
	}
	accountCount, err := readCount(reader, posMaxSnapshotAccounts, "snapshot accounts")
	if err != nil {
		return stateSnapshotResponseEnvelope{}, err
	}
	response.Accounts = make([]accountSnapshotData, accountCount)
	for index := 0; index < accountCount; index++ {
		address, err := readPublicKey(reader, "snapshot account address")
		if err != nil {
			return stateSnapshotResponseEnvelope{}, err
		}
		accountBytes, err := reader.ReadBytes()
		if err != nil {
			return stateSnapshotResponseEnvelope{}, fmt.Errorf("posnode: unmarshal snapshot account bytes %d: %w", index, err)
		}
		account, err := structure.UnmarshalAccountBinary(accountBytes)
		if err != nil {
			return stateSnapshotResponseEnvelope{}, fmt.Errorf("posnode: unmarshal snapshot account %d: %w", index, err)
		}
		response.Accounts[index] = accountSnapshotData{
			Address: address,
			Account: account,
		}
	}
	return response, nil
}

func writeStatusResponseFields(writer *borsh.Writer, response statusResponseEnvelope) error {
	for _, value := range []string{
		response.ChainID,
		response.ChainIdentityHash,
		response.GenesisHash,
		response.NodeName,
		response.PeerID,
		response.NodeMode,
		response.NodeRole,
	} {
		if err := writer.WriteString(value); err != nil {
			return fmt.Errorf("posnode: marshal status string: %w", err)
		}
	}
	if err := writeStringSlice(writer, response.NodeRoles, posMaxListEntries, "node roles"); err != nil {
		return err
	}
	writer.WriteUint64(response.NodeCapabilities)
	if err := writeStringSlice(writer, response.CapabilityNames, posMaxListEntries, "capability names"); err != nil {
		return err
	}
	writer.WriteBool(response.ValidatorEnabled)
	writer.WriteBool(response.ConsensusEnabled)
	writer.WriteUint64(response.HeadHeight)
	writer.WriteUint64(response.HeadSlot)
	if err := writer.WriteString(response.HeadHash); err != nil {
		return fmt.Errorf("posnode: marshal status head hash: %w", err)
	}
	if err := writer.WriteString(response.HeadQCHash); err != nil {
		return fmt.Errorf("posnode: marshal status qc hash: %w", err)
	}
	writer.WriteUint64(response.FinalizedHeight)
	if err := writer.WriteString(response.FinalizedHash); err != nil {
		return fmt.Errorf("posnode: marshal status finalized hash: %w", err)
	}
	writer.WriteUint64(response.FinalityDepth)
	writer.WriteUint64(response.EpochID)
	writeInt(writer, response.MempoolSize)
	writeInt(writer, response.ValidatorCount)
	writeInt(writer, response.KnownPeerCount)
	writer.WriteBool(response.P2PSecure)
	writer.WriteBool(response.P2PInsecure)
	writer.WriteBool(response.StateRecovery)
	if err := writer.WriteString(response.CurrentLeader); err != nil {
		return fmt.Errorf("posnode: marshal status current leader: %w", err)
	}
	if err := writeLeaderSlots(writer, response.UpcomingLeaders); err != nil {
		return err
	}
	if err := writeTurbinePosition(writer, response.Turbine); err != nil {
		return err
	}
	if err := writeTransactionFast(writer, response.TransactionFast); err != nil {
		return err
	}
	if err := writeConsensusStatus(writer, response.Consensus); err != nil {
		return err
	}
	writeNodeMetrics(writer, response.Metrics)
	return nil
}

func readStatusResponseFields(reader *borsh.Reader) (statusResponseEnvelope, error) {
	var response statusResponseEnvelope
	var err error
	if response.ChainID, err = reader.ReadString(); err != nil {
		return response, err
	}
	if response.ChainIdentityHash, err = reader.ReadString(); err != nil {
		return response, err
	}
	if response.GenesisHash, err = reader.ReadString(); err != nil {
		return response, err
	}
	if response.NodeName, err = reader.ReadString(); err != nil {
		return response, err
	}
	if response.PeerID, err = reader.ReadString(); err != nil {
		return response, err
	}
	if response.NodeMode, err = reader.ReadString(); err != nil {
		return response, err
	}
	if response.NodeRole, err = reader.ReadString(); err != nil {
		return response, err
	}
	if response.NodeRoles, err = readStringSlice(reader, posMaxListEntries, "node roles"); err != nil {
		return response, err
	}
	if response.NodeCapabilities, err = reader.ReadUint64(); err != nil {
		return response, err
	}
	if response.CapabilityNames, err = readStringSlice(reader, posMaxListEntries, "capability names"); err != nil {
		return response, err
	}
	if response.ValidatorEnabled, err = reader.ReadBool(); err != nil {
		return response, err
	}
	if response.ConsensusEnabled, err = reader.ReadBool(); err != nil {
		return response, err
	}
	if response.HeadHeight, err = reader.ReadUint64(); err != nil {
		return response, err
	}
	if response.HeadSlot, err = reader.ReadUint64(); err != nil {
		return response, err
	}
	if response.HeadHash, err = reader.ReadString(); err != nil {
		return response, err
	}
	if response.HeadQCHash, err = reader.ReadString(); err != nil {
		return response, err
	}
	if response.FinalizedHeight, err = reader.ReadUint64(); err != nil {
		return response, err
	}
	if response.FinalizedHash, err = reader.ReadString(); err != nil {
		return response, err
	}
	if response.FinalityDepth, err = reader.ReadUint64(); err != nil {
		return response, err
	}
	if response.EpochID, err = reader.ReadUint64(); err != nil {
		return response, err
	}
	if response.MempoolSize, err = readInt(reader, "mempool size"); err != nil {
		return response, err
	}
	if response.ValidatorCount, err = readInt(reader, "validator count"); err != nil {
		return response, err
	}
	if response.KnownPeerCount, err = readInt(reader, "known peer count"); err != nil {
		return response, err
	}
	if response.P2PSecure, err = reader.ReadBool(); err != nil {
		return response, err
	}
	if response.P2PInsecure, err = reader.ReadBool(); err != nil {
		return response, err
	}
	if response.StateRecovery, err = reader.ReadBool(); err != nil {
		return response, err
	}
	if response.CurrentLeader, err = reader.ReadString(); err != nil {
		return response, err
	}
	if response.UpcomingLeaders, err = readLeaderSlots(reader); err != nil {
		return response, err
	}
	if response.Turbine, err = readTurbinePosition(reader); err != nil {
		return response, err
	}
	if response.TransactionFast, err = readTransactionFast(reader); err != nil {
		return response, err
	}
	if response.Consensus, err = readConsensusStatus(reader); err != nil {
		return response, err
	}
	response.Metrics, err = readNodeMetrics(reader)
	return response, err
}

func writeLeaderSlots(writer *borsh.Writer, values []leaderSlotJSON) error {
	if len(values) > posMaxListEntries {
		return fmt.Errorf("posnode: leader slot list exceeds limit")
	}
	writer.WriteUint32(uint32(len(values)))
	for _, value := range values {
		writer.WriteUint64(value.Slot)
		if err := writer.WriteString(value.ValidatorID); err != nil {
			return fmt.Errorf("posnode: marshal leader validator: %w", err)
		}
		if err := writer.WriteString(value.PeerID); err != nil {
			return fmt.Errorf("posnode: marshal leader peer: %w", err)
		}
	}
	return nil
}

func readLeaderSlots(reader *borsh.Reader) ([]leaderSlotJSON, error) {
	count, err := readCount(reader, posMaxListEntries, "leader slots")
	if err != nil {
		return nil, err
	}
	values := make([]leaderSlotJSON, count)
	for index := 0; index < count; index++ {
		slot, err := reader.ReadUint64()
		if err != nil {
			return nil, fmt.Errorf("posnode: unmarshal leader slot %d: %w", index, err)
		}
		validatorID, err := reader.ReadString()
		if err != nil {
			return nil, fmt.Errorf("posnode: unmarshal leader validator %d: %w", index, err)
		}
		peerID, err := reader.ReadString()
		if err != nil {
			return nil, fmt.Errorf("posnode: unmarshal leader peer %d: %w", index, err)
		}
		values[index] = leaderSlotJSON{Slot: slot, ValidatorID: validatorID, PeerID: peerID}
	}
	return values, nil
}

func writeTurbinePosition(writer *borsh.Writer, value turbinePositionJSON) error {
	writer.WriteUint64(value.Slot)
	writeInt(writer, value.Fanout)
	writeInt(writer, value.Layer)
	for _, text := range []string{value.LeaderID, value.LeaderPeerID, value.ParentValidator, value.ParentPeerID} {
		if err := writer.WriteString(text); err != nil {
			return fmt.Errorf("posnode: marshal turbine text: %w", err)
		}
	}
	if err := writeStringSlice(writer, value.ChildValidators, posMaxListEntries, "turbine child validators"); err != nil {
		return err
	}
	if err := writeStringSlice(writer, value.ChildPeerIDs, posMaxListEntries, "turbine child peers"); err != nil {
		return err
	}
	writer.WriteBool(value.ValidatorInTree)
	writer.WriteBool(value.TurbineAvailable)
	return nil
}

func readTurbinePosition(reader *borsh.Reader) (turbinePositionJSON, error) {
	var value turbinePositionJSON
	var err error
	if value.Slot, err = reader.ReadUint64(); err != nil {
		return value, err
	}
	if value.Fanout, err = readInt(reader, "turbine fanout"); err != nil {
		return value, err
	}
	if value.Layer, err = readInt(reader, "turbine layer"); err != nil {
		return value, err
	}
	if value.LeaderID, err = reader.ReadString(); err != nil {
		return value, err
	}
	if value.LeaderPeerID, err = reader.ReadString(); err != nil {
		return value, err
	}
	if value.ParentValidator, err = reader.ReadString(); err != nil {
		return value, err
	}
	if value.ParentPeerID, err = reader.ReadString(); err != nil {
		return value, err
	}
	if value.ChildValidators, err = readStringSlice(reader, posMaxListEntries, "turbine child validators"); err != nil {
		return value, err
	}
	if value.ChildPeerIDs, err = readStringSlice(reader, posMaxListEntries, "turbine child peers"); err != nil {
		return value, err
	}
	if value.ValidatorInTree, err = reader.ReadBool(); err != nil {
		return value, err
	}
	value.TurbineAvailable, err = reader.ReadBool()
	return value, err
}

func writeTransactionFast(writer *borsh.Writer, value transactionFastJSON) error {
	writer.WriteUint64(value.StartSlot)
	writeInt(writer, value.ForwardSlots)
	if err := writeLeaderSlots(writer, value.LeaderSlots); err != nil {
		return err
	}
	if err := writeStringSlice(writer, value.ValidatorPeerIDs, posMaxListEntries, "fast validator peers"); err != nil {
		return err
	}
	if err := writeStringSlice(writer, value.PreferredPeerIDs, posMaxListEntries, "fast preferred peers"); err != nil {
		return err
	}
	writer.WriteBool(value.ForwardValidators)
	writer.WriteBool(value.FastPathAvailable)
	return nil
}

func readTransactionFast(reader *borsh.Reader) (transactionFastJSON, error) {
	var value transactionFastJSON
	var err error
	if value.StartSlot, err = reader.ReadUint64(); err != nil {
		return value, err
	}
	if value.ForwardSlots, err = readInt(reader, "fast forward slots"); err != nil {
		return value, err
	}
	if value.LeaderSlots, err = readLeaderSlots(reader); err != nil {
		return value, err
	}
	if value.ValidatorPeerIDs, err = readStringSlice(reader, posMaxListEntries, "fast validator peers"); err != nil {
		return value, err
	}
	if value.PreferredPeerIDs, err = readStringSlice(reader, posMaxListEntries, "fast preferred peers"); err != nil {
		return value, err
	}
	if value.ForwardValidators, err = reader.ReadBool(); err != nil {
		return value, err
	}
	value.FastPathAvailable, err = reader.ReadBool()
	return value, err
}

func writeConsensusStatus(writer *borsh.Writer, value consensusStatusJSON) error {
	writer.WriteBool(value.Available)
	writer.WriteUint64(value.Slot)
	writer.WriteUint64(value.EpochID)
	writer.WriteUint64(value.EpochStartSlot)
	writer.WriteUint64(value.EpochEndSlot)
	writer.WriteUint64(value.TotalActiveStake)
	writeInt(writer, value.ValidatorCount)
	if err := writer.WriteString(value.LocalValidatorID); err != nil {
		return fmt.Errorf("posnode: marshal consensus local validator id: %w", err)
	}
	if err := writeConsensusValidatorStatus(writer, value.LocalValidator); err != nil {
		return err
	}
	if len(value.Validators) > posMaxListEntries {
		return fmt.Errorf("posnode: consensus validator list exceeds limit")
	}
	writer.WriteUint32(uint32(len(value.Validators)))
	for _, validator := range value.Validators {
		if err := writeConsensusValidatorStatus(writer, validator); err != nil {
			return err
		}
	}
	return nil
}

func readConsensusStatus(reader *borsh.Reader) (consensusStatusJSON, error) {
	var value consensusStatusJSON
	var err error
	if value.Available, err = reader.ReadBool(); err != nil {
		return value, err
	}
	if value.Slot, err = reader.ReadUint64(); err != nil {
		return value, err
	}
	if value.EpochID, err = reader.ReadUint64(); err != nil {
		return value, err
	}
	if value.EpochStartSlot, err = reader.ReadUint64(); err != nil {
		return value, err
	}
	if value.EpochEndSlot, err = reader.ReadUint64(); err != nil {
		return value, err
	}
	if value.TotalActiveStake, err = reader.ReadUint64(); err != nil {
		return value, err
	}
	if value.ValidatorCount, err = readInt(reader, "consensus validator count"); err != nil {
		return value, err
	}
	if value.LocalValidatorID, err = reader.ReadString(); err != nil {
		return value, err
	}
	if value.LocalValidator, err = readConsensusValidatorStatus(reader); err != nil {
		return value, err
	}
	count, err := readCount(reader, posMaxListEntries, "consensus validators")
	if err != nil {
		return value, err
	}
	value.Validators = make([]consensusValidatorStatusJSON, count)
	for index := 0; index < count; index++ {
		value.Validators[index], err = readConsensusValidatorStatus(reader)
		if err != nil {
			return value, err
		}
	}
	return value, nil
}

func writeConsensusValidatorStatus(writer *borsh.Writer, value consensusValidatorStatusJSON) error {
	for _, text := range []string{
		value.ValidatorID,
		value.AccountAddress,
		value.ConsensusPublicKey,
		value.P2PPeerID,
		value.Status,
	} {
		if err := writer.WriteString(text); err != nil {
			return fmt.Errorf("posnode: marshal consensus validator text: %w", err)
		}
	}
	writer.WriteBool(value.InCurrentEpoch)
	writer.WriteBool(value.InTurbineTree)
	writeInt(writer, value.TurbineLayer)
	if err := writer.WriteString(value.TurbineParentValidatorID); err != nil {
		return err
	}
	if err := writer.WriteString(value.TurbineParentPeerID); err != nil {
		return err
	}
	if err := writeStringSlice(writer, value.TurbineChildValidatorIDs, posMaxListEntries, "consensus child validators"); err != nil {
		return err
	}
	if err := writeStringSlice(writer, value.TurbineChildPeerIDs, posMaxListEntries, "consensus child peers"); err != nil {
		return err
	}
	writer.WriteUint64(value.EffectiveStakeLamports)
	writer.WriteUint64(value.WeightBps)
	writer.WriteUint64(value.ActiveStakeLamports)
	writer.WriteUint64(value.PendingStakeLamports)
	writer.WriteUint64(value.UnlockingStakeLamports)
	writer.WriteUint64(value.ActivationEpoch)
	writer.WriteUint64(value.DeactivationEpoch)
	writer.WriteUint64(value.LastEffectiveStakeLamports)
	writer.WriteUint64(value.JailUntilEpoch)
	writer.WriteUint16(value.CommissionBps)
	return nil
}

func readConsensusValidatorStatus(reader *borsh.Reader) (consensusValidatorStatusJSON, error) {
	var value consensusValidatorStatusJSON
	var err error
	if value.ValidatorID, err = reader.ReadString(); err != nil {
		return value, err
	}
	if value.AccountAddress, err = reader.ReadString(); err != nil {
		return value, err
	}
	if value.ConsensusPublicKey, err = reader.ReadString(); err != nil {
		return value, err
	}
	if value.P2PPeerID, err = reader.ReadString(); err != nil {
		return value, err
	}
	if value.Status, err = reader.ReadString(); err != nil {
		return value, err
	}
	if value.InCurrentEpoch, err = reader.ReadBool(); err != nil {
		return value, err
	}
	if value.InTurbineTree, err = reader.ReadBool(); err != nil {
		return value, err
	}
	if value.TurbineLayer, err = readInt(reader, "consensus turbine layer"); err != nil {
		return value, err
	}
	if value.TurbineParentValidatorID, err = reader.ReadString(); err != nil {
		return value, err
	}
	if value.TurbineParentPeerID, err = reader.ReadString(); err != nil {
		return value, err
	}
	if value.TurbineChildValidatorIDs, err = readStringSlice(reader, posMaxListEntries, "consensus child validators"); err != nil {
		return value, err
	}
	if value.TurbineChildPeerIDs, err = readStringSlice(reader, posMaxListEntries, "consensus child peers"); err != nil {
		return value, err
	}
	if value.EffectiveStakeLamports, err = reader.ReadUint64(); err != nil {
		return value, err
	}
	if value.WeightBps, err = reader.ReadUint64(); err != nil {
		return value, err
	}
	if value.ActiveStakeLamports, err = reader.ReadUint64(); err != nil {
		return value, err
	}
	if value.PendingStakeLamports, err = reader.ReadUint64(); err != nil {
		return value, err
	}
	if value.UnlockingStakeLamports, err = reader.ReadUint64(); err != nil {
		return value, err
	}
	if value.ActivationEpoch, err = reader.ReadUint64(); err != nil {
		return value, err
	}
	if value.DeactivationEpoch, err = reader.ReadUint64(); err != nil {
		return value, err
	}
	if value.LastEffectiveStakeLamports, err = reader.ReadUint64(); err != nil {
		return value, err
	}
	if value.JailUntilEpoch, err = reader.ReadUint64(); err != nil {
		return value, err
	}
	if value.CommissionBps, err = reader.ReadUint16(); err != nil {
		return value, err
	}
	return value, nil
}

func writeNodeMetrics(writer *borsh.Writer, value nodeMetricsSnapshot) {
	for _, counter := range []uint64{
		value.BlocksProduced,
		value.ProposalsAccepted,
		value.ProposalsRejected,
		value.VotesSent,
		value.QCFormed,
		value.QCReceived,
		value.ForkDecisions,
		value.Reorgs,
		value.OrphanStored,
		value.SyncRequests,
		value.SyncFailures,
		value.TransactionsIn,
		value.TransactionsDrop,
		value.EvidenceReceived,
	} {
		writer.WriteUint64(counter)
	}
}

func readNodeMetrics(reader *borsh.Reader) (nodeMetricsSnapshot, error) {
	values := make([]uint64, 14)
	for index := range values {
		value, err := reader.ReadUint64()
		if err != nil {
			return nodeMetricsSnapshot{}, fmt.Errorf("posnode: unmarshal metric %d: %w", index, err)
		}
		values[index] = value
	}
	return nodeMetricsSnapshot{
		BlocksProduced:    values[0],
		ProposalsAccepted: values[1],
		ProposalsRejected: values[2],
		VotesSent:         values[3],
		QCFormed:          values[4],
		QCReceived:        values[5],
		ForkDecisions:     values[6],
		Reorgs:            values[7],
		OrphanStored:      values[8],
		SyncRequests:      values[9],
		SyncFailures:      values[10],
		TransactionsIn:    values[11],
		TransactionsDrop:  values[12],
		EvidenceReceived:  values[13],
	}, nil
}

func writeBlockLocatorEntriesBinary(writer *borsh.Writer, entries []blockLocatorEntryJSON) error {
	if len(entries) > posMaxListEntries {
		return fmt.Errorf("posnode: block locator entries exceed limit")
	}
	writer.WriteUint32(uint32(len(entries)))
	for _, entry := range entries {
		if err := writeBlockLocatorEntryBinary(writer, entry); err != nil {
			return err
		}
	}
	return nil
}

func readBlockLocatorEntriesBinary(reader *borsh.Reader) ([]blockLocatorEntryJSON, error) {
	count, err := readCount(reader, posMaxListEntries, "block locator entries")
	if err != nil {
		return nil, err
	}
	entries := make([]blockLocatorEntryJSON, count)
	for index := 0; index < count; index++ {
		entries[index], err = readBlockLocatorEntryBinary(reader)
		if err != nil {
			return nil, err
		}
	}
	return entries, nil
}

func writeBlockLocatorEntryBinary(writer *borsh.Writer, entry blockLocatorEntryJSON) error {
	writer.WriteUint64(entry.Height)
	return writeHashString(writer, entry.Hash, "block locator hash")
}

func readBlockLocatorEntryBinary(reader *borsh.Reader) (blockLocatorEntryJSON, error) {
	height, err := reader.ReadUint64()
	if err != nil {
		return blockLocatorEntryJSON{}, fmt.Errorf("posnode: unmarshal locator height: %w", err)
	}
	hash, err := readHashString(reader, "block locator hash")
	if err != nil {
		return blockLocatorEntryJSON{}, err
	}
	return blockLocatorEntryJSON{Height: height, Hash: hash}, nil
}

func writeStringSlice(writer *borsh.Writer, values []string, maxCount int, label string) error {
	if len(values) > maxCount {
		return fmt.Errorf("posnode: %s exceeds limit", label)
	}
	writer.WriteUint32(uint32(len(values)))
	for _, value := range values {
		if err := writer.WriteString(value); err != nil {
			return fmt.Errorf("posnode: marshal %s: %w", label, err)
		}
	}
	return nil
}

func readStringSlice(reader *borsh.Reader, maxCount int, label string) ([]string, error) {
	count, err := readCount(reader, maxCount, label)
	if err != nil {
		return nil, err
	}
	values := make([]string, count)
	for index := 0; index < count; index++ {
		values[index], err = reader.ReadString()
		if err != nil {
			return nil, fmt.Errorf("posnode: unmarshal %s %d: %w", label, index, err)
		}
	}
	return values, nil
}

func writeInt(writer *borsh.Writer, value int) {
	writer.WriteInt64(int64(value))
}

func readInt(reader *borsh.Reader, label string) (int, error) {
	value, err := reader.ReadInt64()
	if err != nil {
		return 0, fmt.Errorf("posnode: unmarshal %s: %w", label, err)
	}
	maxInt := int64(^uint(0) >> 1)
	minInt := -maxInt - 1
	if value < minInt || value > maxInt {
		return 0, fmt.Errorf("posnode: %s outside int range", label)
	}
	return int(value), nil
}

func readCount(reader *borsh.Reader, maxCount int, label string) (int, error) {
	count, err := reader.ReadUint32()
	if err != nil {
		return 0, fmt.Errorf("posnode: unmarshal %s count: %w", label, err)
	}
	if count > uint32(maxCount) {
		return 0, fmt.Errorf("posnode: %s count %d exceeds %d", label, count, maxCount)
	}
	return int(count), nil
}

func writeHashString(writer *borsh.Writer, value string, label string) error {
	hash, err := structure.HashFromBase58(value)
	if err != nil {
		return fmt.Errorf("posnode: decode %s: %w", label, err)
	}
	writer.WriteFixedBytes(hash[:])
	return nil
}

func readHashString(reader *borsh.Reader, label string) (string, error) {
	hash, err := readHash(reader, label)
	if err != nil {
		return "", err
	}
	return hash.String(), nil
}

func writeOptionalHashString(writer *borsh.Writer, value string, label string) error {
	writer.WriteBool(value != "")
	if value == "" {
		return nil
	}
	return writeHashString(writer, value, label)
}

func readOptionalHashString(reader *borsh.Reader, label string) (string, error) {
	hasValue, err := reader.ReadBool()
	if err != nil {
		return "", fmt.Errorf("posnode: unmarshal optional %s flag: %w", label, err)
	}
	if !hasValue {
		return "", nil
	}
	return readHashString(reader, label)
}

func readHash(reader *borsh.Reader, label string) (structure.Hash, error) {
	hashBytes, err := reader.ReadFixedBytes(structure.HashSize)
	if err != nil {
		return structure.Hash{}, fmt.Errorf("posnode: unmarshal %s: %w", label, err)
	}
	hash, err := structure.NewHash(hashBytes)
	if err != nil {
		return structure.Hash{}, err
	}
	return hash, nil
}

func readPublicKey(reader *borsh.Reader, label string) (structure.PublicKey, error) {
	keyBytes, err := reader.ReadFixedBytes(structure.PublicKeySize)
	if err != nil {
		return structure.PublicKey{}, fmt.Errorf("posnode: unmarshal %s: %w", label, err)
	}
	return structure.PublicKeyFromBytes(keyBytes)
}

func readSignature(reader *borsh.Reader, label string) (structure.Signature, error) {
	signatureBytes, err := reader.ReadFixedBytes(structure.SignatureSize)
	if err != nil {
		return structure.Signature{}, fmt.Errorf("posnode: unmarshal %s: %w", label, err)
	}
	return structure.SignatureFromBytes(signatureBytes)
}
