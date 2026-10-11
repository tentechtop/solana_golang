package posnode

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/tentechtop/ordexus-core/codec/borsh"
	"github.com/tentechtop/ordexus-core/consensus"
	"github.com/tentechtop/ordexus-core/p2p"
	"github.com/tentechtop/ordexus-core/structure"
)

const (
	posMaxConsensusTextLength = 128
	posMaxPeerIDLength        = 128
	posMaxProtocolErrorLength = 4 * 1024
)

func marshalEpochTransitionProofBinary(proof epochTransitionProof) ([]byte, error) {
	writer := newPOSPayloadWriter(p2p.ProtocolPoSEpochTransitionV1)
	if err := writeEpochTransitionProofBinary(writer, proof); err != nil {
		return nil, err
	}
	return writer.Bytes(), nil
}

func unmarshalEpochTransitionProofBinary(data []byte) (epochTransitionProof, error) {
	reader, err := newPOSPayloadReader(data, p2p.ProtocolPoSEpochTransitionV1)
	if err != nil {
		return epochTransitionProof{}, err
	}
	proof, err := readEpochTransitionProofBinary(reader)
	if err != nil {
		return epochTransitionProof{}, err
	}
	if err := reader.EnsureEOF(); err != nil {
		return epochTransitionProof{}, fmt.Errorf("posnode: epoch transition proof eof: %w", err)
	}
	return proof, nil
}

func marshalEpochProofRequestBinary(request epochProofRequest) ([]byte, error) {
	writer := newPOSPayloadWriter(p2p.ProtocolPoSEpochProofV1)
	writer.WriteUint64(request.FromEpochID)
	writer.WriteUint64(request.ToEpochID)
	return writer.Bytes(), nil
}

func unmarshalEpochProofRequestBinary(data []byte) (epochProofRequest, error) {
	reader, err := newPOSPayloadReader(data, p2p.ProtocolPoSEpochProofV1)
	if err != nil {
		return epochProofRequest{}, err
	}
	fromEpochID, err := reader.ReadUint64()
	if err != nil {
		return epochProofRequest{}, fmt.Errorf("posnode: unmarshal epoch proof source epoch: %w", err)
	}
	toEpochID, err := reader.ReadUint64()
	if err != nil {
		return epochProofRequest{}, fmt.Errorf("posnode: unmarshal epoch proof target epoch: %w", err)
	}
	if err := reader.EnsureEOF(); err != nil {
		return epochProofRequest{}, fmt.Errorf("posnode: epoch proof request eof: %w", err)
	}
	return epochProofRequest{FromEpochID: fromEpochID, ToEpochID: toEpochID}, nil
}

func marshalEpochProofResponseBinary(response epochProofResponse) ([]byte, error) {
	if len(response.Error) > posMaxProtocolErrorLength {
		return nil, fmt.Errorf("posnode: epoch proof response error exceeds limit")
	}
	if response.Error != "" && response.Found {
		return nil, fmt.Errorf("posnode: epoch proof error response cannot contain a proof")
	}
	writer := newPOSPayloadWriter(p2p.ProtocolPoSEpochProofV1)
	writer.WriteBool(response.Found)
	if err := writeBoundedPOSString(writer, response.Error, posMaxProtocolErrorLength, "epoch proof response error"); err != nil {
		return nil, err
	}
	if response.Found {
		if err := writeEpochTransitionProofBinary(writer, response.Proof); err != nil {
			return nil, err
		}
	}
	return writer.Bytes(), nil
}

func unmarshalEpochProofResponseBinary(data []byte) (epochProofResponse, error) {
	reader, err := newPOSPayloadReader(data, p2p.ProtocolPoSEpochProofV1)
	if err != nil {
		return epochProofResponse{}, err
	}
	found, err := reader.ReadBool()
	if err != nil {
		return epochProofResponse{}, fmt.Errorf("posnode: unmarshal epoch proof found flag: %w", err)
	}
	errorText, err := readBoundedPOSString(reader, posMaxProtocolErrorLength, "epoch proof response error")
	if err != nil {
		return epochProofResponse{}, err
	}
	if found && errorText != "" {
		return epochProofResponse{}, fmt.Errorf("posnode: epoch proof error response cannot contain a proof")
	}
	response := epochProofResponse{Found: found, Error: errorText}
	if found {
		response.Proof, err = readEpochTransitionProofBinary(reader)
		if err != nil {
			return epochProofResponse{}, err
		}
	}
	if err := reader.EnsureEOF(); err != nil {
		return epochProofResponse{}, fmt.Errorf("posnode: epoch proof response eof: %w", err)
	}
	return response, nil
}

func marshalEpochProofBatchRequestBinary(request epochProofBatchRequest) ([]byte, error) {
	if request.FromEpochID == ^uint64(0) || request.TargetEpochID <= request.FromEpochID {
		return nil, fmt.Errorf("posnode: epoch proof batch range is invalid")
	}
	if request.MaxProofs == 0 || request.MaxProofs > maxEpochProofBatchSize {
		return nil, fmt.Errorf("posnode: epoch proof batch count is invalid")
	}
	if request.MaxPayloadSize == 0 || request.MaxPayloadSize > maxEpochProofBatchPayloadBytes {
		return nil, fmt.Errorf("posnode: epoch proof batch payload limit is invalid")
	}
	writer := newPOSPayloadWriter(p2p.ProtocolPoSEpochProofBatchV1)
	writer.WriteUint64(request.FromEpochID)
	writer.WriteUint64(request.TargetEpochID)
	writer.WriteUint32(request.MaxProofs)
	writer.WriteUint32(request.MaxPayloadSize)
	return writer.Bytes(), nil
}

func unmarshalEpochProofBatchRequestBinary(data []byte) (epochProofBatchRequest, error) {
	reader, err := newPOSPayloadReader(data, p2p.ProtocolPoSEpochProofBatchV1)
	if err != nil {
		return epochProofBatchRequest{}, err
	}
	fromEpochID, err := reader.ReadUint64()
	if err != nil {
		return epochProofBatchRequest{}, fmt.Errorf("posnode: unmarshal epoch proof batch source epoch: %w", err)
	}
	targetEpochID, err := reader.ReadUint64()
	if err != nil {
		return epochProofBatchRequest{}, fmt.Errorf("posnode: unmarshal epoch proof batch target epoch: %w", err)
	}
	maxProofs, err := reader.ReadUint32()
	if err != nil {
		return epochProofBatchRequest{}, fmt.Errorf("posnode: unmarshal epoch proof batch count: %w", err)
	}
	maxPayloadSize, err := reader.ReadUint32()
	if err != nil {
		return epochProofBatchRequest{}, fmt.Errorf("posnode: unmarshal epoch proof batch payload limit: %w", err)
	}
	if err := reader.EnsureEOF(); err != nil {
		return epochProofBatchRequest{}, fmt.Errorf("posnode: epoch proof batch request eof: %w", err)
	}
	request := epochProofBatchRequest{
		FromEpochID:    fromEpochID,
		TargetEpochID:  targetEpochID,
		MaxProofs:      maxProofs,
		MaxPayloadSize: maxPayloadSize,
	}
	if _, err := marshalEpochProofBatchRequestBinary(request); err != nil {
		return epochProofBatchRequest{}, err
	}
	return request, nil
}

func marshalEpochProofBatchResponseBinary(response epochProofBatchResponse) ([]byte, error) {
	if err := validateEpochProofBatchResponse(response); err != nil {
		return nil, err
	}
	writer := newPOSPayloadWriter(p2p.ProtocolPoSEpochProofBatchV1)
	writer.WriteUint64(response.FromEpochID)
	writer.WriteUint64(response.TargetEpochID)
	writer.WriteBool(response.Complete)
	if err := writeBoundedPOSString(writer, response.Error, posMaxProtocolErrorLength, "epoch proof batch response error"); err != nil {
		return nil, err
	}
	writer.WriteUint32(uint32(len(response.Proofs)))
	for _, proof := range response.Proofs {
		if err := writeEpochTransitionProofBinary(writer, proof); err != nil {
			return nil, err
		}
	}
	payload := writer.Bytes()
	if len(payload) > maxEpochProofBatchPayloadBytes {
		return nil, fmt.Errorf("posnode: epoch proof batch response exceeds payload limit")
	}
	return payload, nil
}

func unmarshalEpochProofBatchResponseBinary(data []byte) (epochProofBatchResponse, error) {
	reader, err := newPOSPayloadReader(data, p2p.ProtocolPoSEpochProofBatchV1)
	if err != nil {
		return epochProofBatchResponse{}, err
	}
	fromEpochID, err := reader.ReadUint64()
	if err != nil {
		return epochProofBatchResponse{}, fmt.Errorf("posnode: unmarshal epoch proof batch source epoch: %w", err)
	}
	targetEpochID, err := reader.ReadUint64()
	if err != nil {
		return epochProofBatchResponse{}, fmt.Errorf("posnode: unmarshal epoch proof batch target epoch: %w", err)
	}
	complete, err := reader.ReadBool()
	if err != nil {
		return epochProofBatchResponse{}, fmt.Errorf("posnode: unmarshal epoch proof batch complete flag: %w", err)
	}
	errorText, err := readBoundedPOSString(reader, posMaxProtocolErrorLength, "epoch proof batch response error")
	if err != nil {
		return epochProofBatchResponse{}, err
	}
	proofCount, err := readCount(reader, maxEpochProofBatchSize, "epoch proof batch proofs")
	if err != nil {
		return epochProofBatchResponse{}, err
	}
	proofs := make([]epochTransitionProof, proofCount)
	for index := range proofs {
		proofs[index], err = readEpochTransitionProofBinary(reader)
		if err != nil {
			return epochProofBatchResponse{}, fmt.Errorf("posnode: unmarshal epoch proof batch proof %d: %w", index, err)
		}
	}
	if err := reader.EnsureEOF(); err != nil {
		return epochProofBatchResponse{}, fmt.Errorf("posnode: epoch proof batch response eof: %w", err)
	}
	response := epochProofBatchResponse{
		FromEpochID:   fromEpochID,
		TargetEpochID: targetEpochID,
		Complete:      complete,
		Proofs:        proofs,
		Error:         errorText,
	}
	if err := validateEpochProofBatchResponse(response); err != nil {
		return epochProofBatchResponse{}, err
	}
	return response, nil
}

func validateEpochProofBatchResponse(response epochProofBatchResponse) error {
	if len(response.Error) > posMaxProtocolErrorLength {
		return fmt.Errorf("posnode: epoch proof batch response error exceeds limit")
	}
	if response.FromEpochID == ^uint64(0) || response.TargetEpochID <= response.FromEpochID {
		return fmt.Errorf("posnode: epoch proof batch response range is invalid")
	}
	if len(response.Proofs) > maxEpochProofBatchSize {
		return fmt.Errorf("posnode: epoch proof batch response count exceeds limit")
	}
	if response.Error != "" {
		if response.Complete || len(response.Proofs) != 0 {
			return fmt.Errorf("posnode: epoch proof batch error response contains progress")
		}
		return nil
	}
	if len(response.Proofs) == 0 {
		return fmt.Errorf("posnode: successful epoch proof batch is empty")
	}
	expectedFromEpochID := response.FromEpochID
	var previousSnapshotRoot string
	for index, proof := range response.Proofs {
		transition := proof.Certificate.Transition
		if transition.FromEpochID != expectedFromEpochID || transition.ToEpochID != expectedFromEpochID+1 {
			return fmt.Errorf("posnode: epoch proof batch is not consecutive at index %d", index)
		}
		if transition.ToEpochID > response.TargetEpochID {
			return fmt.Errorf("posnode: epoch proof batch exceeds target epoch")
		}
		if index > 0 && transition.FromSnapshotRoot != previousSnapshotRoot {
			return fmt.Errorf("posnode: epoch proof batch snapshot roots are not linked at index %d", index)
		}
		previousSnapshotRoot = proof.Snapshot.SnapshotStateRoot.String()
		expectedFromEpochID = transition.ToEpochID
	}
	if response.Complete != (expectedFromEpochID == response.TargetEpochID) {
		return fmt.Errorf("posnode: epoch proof batch complete flag is inconsistent")
	}
	return nil
}

func epochTransitionProofBinarySize(proof epochTransitionProof) (int, error) {
	writer := borsh.NewWriter(posMaxContainerLength)
	if err := writeEpochTransitionProofBinary(writer, proof); err != nil {
		return 0, err
	}
	return len(writer.BytesView()), nil
}

func writeEpochTransitionProofBinary(writer *borsh.Writer, proof epochTransitionProof) error {
	canonicalCertificate, err := proof.Certificate.Canonical()
	if err != nil {
		return fmt.Errorf("posnode: canonicalize epoch transition certificate: %w", err)
	}
	if err := proof.Snapshot.Validate(); err != nil {
		return fmt.Errorf("posnode: validate epoch transition snapshot: %w", err)
	}
	if canonicalCertificate.Transition.ToEpochID != proof.Snapshot.EpochID ||
		canonicalCertificate.Transition.ToSnapshotRoot != proof.Snapshot.SnapshotStateRoot.String() {
		return fmt.Errorf("posnode: epoch transition certificate does not bind the snapshot")
	}
	if err := writeEpochTransitionBinary(writer, canonicalCertificate.Transition); err != nil {
		return err
	}
	if err := writeEpochTransitionSignaturesBinary(writer, canonicalCertificate.Signatures); err != nil {
		return err
	}
	return writeEpochSnapshotBinary(writer, proof.Snapshot)
}

func readEpochTransitionProofBinary(reader *borsh.Reader) (epochTransitionProof, error) {
	transition, err := readEpochTransitionBinary(reader)
	if err != nil {
		return epochTransitionProof{}, err
	}
	signatures, err := readEpochTransitionSignaturesBinary(reader)
	if err != nil {
		return epochTransitionProof{}, err
	}
	certificate, err := (consensus.EpochTransitionCertificate{
		Transition: transition,
		Signatures: signatures,
	}).Canonical()
	if err != nil {
		return epochTransitionProof{}, fmt.Errorf("posnode: validate epoch transition certificate: %w", err)
	}
	snapshot, err := readEpochSnapshotBinary(reader)
	if err != nil {
		return epochTransitionProof{}, err
	}
	if transition.ToEpochID != snapshot.EpochID || transition.ToSnapshotRoot != snapshot.SnapshotStateRoot.String() {
		return epochTransitionProof{}, fmt.Errorf("posnode: epoch transition certificate does not bind the snapshot")
	}
	return epochTransitionProof{Certificate: certificate, Snapshot: snapshot}, nil
}

func writeEpochTransitionBinary(writer *borsh.Writer, transition consensus.EpochTransition) error {
	if err := transition.Validate(); err != nil {
		return err
	}
	if err := writeBoundedPOSString(writer, transition.ChainID, posMaxConsensusTextLength, "epoch transition chain id"); err != nil {
		return err
	}
	if err := writeHashString(writer, transition.ChainIdentityHash, "epoch transition chain identity hash"); err != nil {
		return err
	}
	if err := writeHashString(writer, transition.GenesisHash, "epoch transition genesis hash"); err != nil {
		return err
	}
	writer.WriteUint64(transition.FromEpochID)
	writer.WriteUint64(transition.ToEpochID)
	writer.WriteUint64(transition.SourceBlockHeight)
	if err := writeHashString(writer, transition.SourceBlockHash, "epoch transition source block hash"); err != nil {
		return err
	}
	if err := writeHashString(writer, transition.FromSnapshotRoot, "epoch transition source snapshot root"); err != nil {
		return err
	}
	return writeHashString(writer, transition.ToSnapshotRoot, "epoch transition target snapshot root")
}

func readEpochTransitionBinary(reader *borsh.Reader) (consensus.EpochTransition, error) {
	chainID, err := readBoundedPOSString(reader, posMaxConsensusTextLength, "epoch transition chain id")
	if err != nil {
		return consensus.EpochTransition{}, err
	}
	chainIdentityHash, err := readHashString(reader, "epoch transition chain identity hash")
	if err != nil {
		return consensus.EpochTransition{}, err
	}
	genesisHash, err := readHashString(reader, "epoch transition genesis hash")
	if err != nil {
		return consensus.EpochTransition{}, err
	}
	fromEpochID, err := reader.ReadUint64()
	if err != nil {
		return consensus.EpochTransition{}, fmt.Errorf("posnode: unmarshal epoch transition source epoch: %w", err)
	}
	toEpochID, err := reader.ReadUint64()
	if err != nil {
		return consensus.EpochTransition{}, fmt.Errorf("posnode: unmarshal epoch transition target epoch: %w", err)
	}
	sourceBlockHeight, err := reader.ReadUint64()
	if err != nil {
		return consensus.EpochTransition{}, fmt.Errorf("posnode: unmarshal epoch transition source height: %w", err)
	}
	sourceBlockHash, err := readHashString(reader, "epoch transition source block hash")
	if err != nil {
		return consensus.EpochTransition{}, err
	}
	fromSnapshotRoot, err := readHashString(reader, "epoch transition source snapshot root")
	if err != nil {
		return consensus.EpochTransition{}, err
	}
	toSnapshotRoot, err := readHashString(reader, "epoch transition target snapshot root")
	if err != nil {
		return consensus.EpochTransition{}, err
	}
	transition := consensus.EpochTransition{
		ChainID:           chainID,
		ChainIdentityHash: chainIdentityHash,
		GenesisHash:       genesisHash,
		FromEpochID:       fromEpochID,
		ToEpochID:         toEpochID,
		SourceBlockHeight: sourceBlockHeight,
		SourceBlockHash:   sourceBlockHash,
		FromSnapshotRoot:  fromSnapshotRoot,
		ToSnapshotRoot:    toSnapshotRoot,
	}
	if err := transition.Validate(); err != nil {
		return consensus.EpochTransition{}, err
	}
	return transition, nil
}

func writeEpochTransitionSignaturesBinary(writer *borsh.Writer, signatures []consensus.EpochTransitionSignature) error {
	if len(signatures) == 0 || len(signatures) > consensus.MaxValidatorsPerSet {
		return fmt.Errorf("posnode: invalid epoch transition signature count")
	}
	writer.WriteUint32(uint32(len(signatures)))
	for index, item := range signatures {
		if err := writeValidatorSignatureBinary(writer, item.ValidatorID, item.PublicKey, item.Signature, "epoch transition", index); err != nil {
			return err
		}
	}
	return nil
}

func readEpochTransitionSignaturesBinary(reader *borsh.Reader) ([]consensus.EpochTransitionSignature, error) {
	count, err := readCount(reader, consensus.MaxValidatorsPerSet, "epoch transition signatures")
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, fmt.Errorf("posnode: epoch transition signatures are empty")
	}
	signatures := make([]consensus.EpochTransitionSignature, count)
	for index := 0; index < count; index++ {
		validatorID, publicKey, signature, err := readValidatorSignatureBinary(reader, "epoch transition", index)
		if err != nil {
			return nil, err
		}
		signatures[index] = consensus.EpochTransitionSignature{
			ValidatorID: validatorID,
			PublicKey:   publicKey,
			Signature:   signature,
		}
	}
	return signatures, nil
}

func writeEpochSnapshotBinary(writer *borsh.Writer, snapshot consensus.EpochSnapshot) error {
	if err := snapshot.Validate(); err != nil {
		return err
	}
	if len(snapshot.Validators) > consensus.MaxValidatorsPerSet {
		return fmt.Errorf("posnode: epoch snapshot validator count exceeds limit")
	}
	writer.WriteUint64(snapshot.EpochID)
	writer.WriteUint64(snapshot.StartSlot)
	writer.WriteUint64(snapshot.EndSlot)
	writer.WriteFixedBytes(snapshot.RandomSeed[:])
	writer.WriteUint64(snapshot.TotalActiveStake)
	writer.WriteUint32(uint32(len(snapshot.Validators)))
	for index, validator := range snapshot.Validators {
		if err := writeValidatorStateBinary(writer, validator, index); err != nil {
			return err
		}
	}
	writer.WriteFixedBytes(snapshot.SnapshotStateRoot[:])
	return nil
}

func readEpochSnapshotBinary(reader *borsh.Reader) (consensus.EpochSnapshot, error) {
	epochID, err := reader.ReadUint64()
	if err != nil {
		return consensus.EpochSnapshot{}, fmt.Errorf("posnode: unmarshal epoch snapshot id: %w", err)
	}
	startSlot, err := reader.ReadUint64()
	if err != nil {
		return consensus.EpochSnapshot{}, fmt.Errorf("posnode: unmarshal epoch snapshot start slot: %w", err)
	}
	endSlot, err := reader.ReadUint64()
	if err != nil {
		return consensus.EpochSnapshot{}, fmt.Errorf("posnode: unmarshal epoch snapshot end slot: %w", err)
	}
	randomSeed, err := readHash(reader, "epoch snapshot random seed")
	if err != nil {
		return consensus.EpochSnapshot{}, err
	}
	totalActiveStake, err := reader.ReadUint64()
	if err != nil {
		return consensus.EpochSnapshot{}, fmt.Errorf("posnode: unmarshal epoch snapshot total stake: %w", err)
	}
	validatorCount, err := readCount(reader, consensus.MaxValidatorsPerSet, "epoch snapshot validators")
	if err != nil {
		return consensus.EpochSnapshot{}, err
	}
	if validatorCount == 0 {
		return consensus.EpochSnapshot{}, fmt.Errorf("posnode: epoch snapshot validators are empty")
	}
	validators := make([]consensus.ValidatorState, validatorCount)
	for index := 0; index < validatorCount; index++ {
		validators[index], err = readValidatorStateBinary(reader, index)
		if err != nil {
			return consensus.EpochSnapshot{}, err
		}
	}
	snapshotRoot, err := readHash(reader, "epoch snapshot root")
	if err != nil {
		return consensus.EpochSnapshot{}, err
	}
	snapshot := consensus.EpochSnapshot{
		EpochID:           epochID,
		StartSlot:         startSlot,
		EndSlot:           endSlot,
		RandomSeed:        randomSeed,
		TotalActiveStake:  totalActiveStake,
		Validators:        validators,
		SnapshotStateRoot: snapshotRoot,
	}
	if err := snapshot.Validate(); err != nil {
		return consensus.EpochSnapshot{}, fmt.Errorf("posnode: validate epoch snapshot: %w", err)
	}
	return snapshot, nil
}

func writeValidatorStateBinary(writer *borsh.Writer, validator consensus.ValidatorState, index int) error {
	if len(validator.ValidatorID) == 0 || len(validator.ValidatorID) > posMaxConsensusTextLength {
		return fmt.Errorf("posnode: invalid epoch snapshot validator id at index %d", index)
	}
	if len(validator.BLSPublicKey) > consensus.MaxValidatorBLSPublicKeyLen {
		return fmt.Errorf("posnode: validator bls public key exceeds limit at index %d", index)
	}
	if len(validator.P2PPeerID) > posMaxPeerIDLength {
		return fmt.Errorf("posnode: validator peer id exceeds limit at index %d", index)
	}
	if validator.Status > consensus.ValidatorStatusExiting {
		return fmt.Errorf("posnode: invalid validator status at index %d", index)
	}
	if err := writeBoundedPOSString(writer, string(validator.ValidatorID), posMaxConsensusTextLength, "epoch snapshot validator id"); err != nil {
		return err
	}
	writer.WriteFixedBytes(validator.AccountAddress[:])
	writer.WriteFixedBytes(validator.ConsensusPublicKey[:])
	if err := writer.WriteBytes(validator.BLSPublicKey); err != nil {
		return fmt.Errorf("posnode: marshal validator bls public key: %w", err)
	}
	if err := writeBoundedPOSString(writer, validator.P2PPeerID, posMaxPeerIDLength, "epoch snapshot validator peer id"); err != nil {
		return err
	}
	writer.WriteUint64(validator.StakeLamports)
	writer.WriteUint8(uint8(validator.Status))
	writer.WriteUint16(validator.CommissionBps)
	writer.WriteUint64(validator.LastVotedSlot)
	writer.WriteUint64(validator.MissedProposalCount)
	writer.WriteUint64(validator.MissedVoteCount)
	writer.WriteUint64(validator.JailUntilEpoch)
	return nil
}

func readValidatorStateBinary(reader *borsh.Reader, index int) (consensus.ValidatorState, error) {
	validatorID, err := readBoundedPOSString(reader, posMaxConsensusTextLength, "epoch snapshot validator id")
	if err != nil {
		return consensus.ValidatorState{}, err
	}
	accountAddress, err := readPublicKey(reader, "epoch snapshot validator account")
	if err != nil {
		return consensus.ValidatorState{}, err
	}
	consensusPublicKey, err := readPublicKey(reader, "epoch snapshot validator consensus public key")
	if err != nil {
		return consensus.ValidatorState{}, err
	}
	blsPublicKey, err := readBoundedPOSBytes(reader, consensus.MaxValidatorBLSPublicKeyLen, "validator bls public key")
	if err != nil {
		return consensus.ValidatorState{}, err
	}
	peerID, err := readBoundedPOSString(reader, posMaxPeerIDLength, "epoch snapshot validator peer id")
	if err != nil {
		return consensus.ValidatorState{}, err
	}
	stakeLamports, err := reader.ReadUint64()
	if err != nil {
		return consensus.ValidatorState{}, fmt.Errorf("posnode: unmarshal validator stake: %w", err)
	}
	status, err := reader.ReadUint8()
	if err != nil {
		return consensus.ValidatorState{}, fmt.Errorf("posnode: unmarshal validator status: %w", err)
	}
	if consensus.ValidatorStatus(status) > consensus.ValidatorStatusExiting {
		return consensus.ValidatorState{}, fmt.Errorf("posnode: invalid validator status at index %d", index)
	}
	commissionBps, err := reader.ReadUint16()
	if err != nil {
		return consensus.ValidatorState{}, fmt.Errorf("posnode: unmarshal validator commission: %w", err)
	}
	lastVotedSlot, err := reader.ReadUint64()
	if err != nil {
		return consensus.ValidatorState{}, fmt.Errorf("posnode: unmarshal validator last voted slot: %w", err)
	}
	missedProposalCount, err := reader.ReadUint64()
	if err != nil {
		return consensus.ValidatorState{}, fmt.Errorf("posnode: unmarshal validator missed proposal count: %w", err)
	}
	missedVoteCount, err := reader.ReadUint64()
	if err != nil {
		return consensus.ValidatorState{}, fmt.Errorf("posnode: unmarshal validator missed vote count: %w", err)
	}
	jailUntilEpoch, err := reader.ReadUint64()
	if err != nil {
		return consensus.ValidatorState{}, fmt.Errorf("posnode: unmarshal validator jail epoch: %w", err)
	}
	return consensus.ValidatorState{
		ValidatorID:         consensus.ValidatorID(validatorID),
		AccountAddress:      accountAddress,
		ConsensusPublicKey:  consensusPublicKey,
		BLSPublicKey:        blsPublicKey,
		P2PPeerID:           peerID,
		StakeLamports:       stakeLamports,
		Status:              consensus.ValidatorStatus(status),
		CommissionBps:       commissionBps,
		LastVotedSlot:       lastVotedSlot,
		MissedProposalCount: missedProposalCount,
		MissedVoteCount:     missedVoteCount,
		JailUntilEpoch:      jailUntilEpoch,
	}, nil
}

func marshalEmergencyRecoveryCertificateBinary(certificate consensus.EmergencyRecoveryCertificate) ([]byte, error) {
	writer := newPOSPayloadWriter(p2p.ProtocolPoSEmergencyRecoveryV1)
	if err := writeEmergencyRecoveryCertificateBinary(writer, certificate); err != nil {
		return nil, err
	}
	return writer.Bytes(), nil
}

func unmarshalEmergencyRecoveryCertificateBinary(data []byte) (consensus.EmergencyRecoveryCertificate, error) {
	reader, err := newPOSPayloadReader(data, p2p.ProtocolPoSEmergencyRecoveryV1)
	if err != nil {
		return consensus.EmergencyRecoveryCertificate{}, err
	}
	certificate, err := readEmergencyRecoveryCertificateBinary(reader)
	if err != nil {
		return consensus.EmergencyRecoveryCertificate{}, err
	}
	if err := reader.EnsureEOF(); err != nil {
		return consensus.EmergencyRecoveryCertificate{}, fmt.Errorf("posnode: emergency recovery certificate eof: %w", err)
	}
	return certificate, nil
}

func marshalMultiValidatorActivationCertificateBinary(certificate consensus.MultiValidatorActivationCertificate) ([]byte, error) {
	writer := newPOSPayloadWriter(p2p.ProtocolPoSMultiValidatorActivationV1)
	if err := writeMultiValidatorActivationCertificateBinary(writer, certificate); err != nil {
		return nil, err
	}
	return writer.Bytes(), nil
}

func unmarshalMultiValidatorActivationCertificateBinary(data []byte) (consensus.MultiValidatorActivationCertificate, error) {
	reader, err := newPOSPayloadReader(data, p2p.ProtocolPoSMultiValidatorActivationV1)
	if err != nil {
		return consensus.MultiValidatorActivationCertificate{}, err
	}
	certificate, err := readMultiValidatorActivationCertificateBinary(reader)
	if err != nil {
		return consensus.MultiValidatorActivationCertificate{}, err
	}
	if err := reader.EnsureEOF(); err != nil {
		return consensus.MultiValidatorActivationCertificate{}, fmt.Errorf("posnode: multi-validator activation certificate eof: %w", err)
	}
	return certificate, nil
}

func writeEmergencyRecoveryCertificateBinary(writer *borsh.Writer, certificate consensus.EmergencyRecoveryCertificate) error {
	if _, err := certificate.SignBytes(); err != nil {
		return err
	}
	excludedValidatorIDs := make([]string, len(certificate.ExcludedValidatorIDs))
	for index, validatorID := range certificate.ExcludedValidatorIDs {
		excludedValidatorIDs[index] = strings.TrimSpace(validatorID)
	}
	sort.Strings(excludedValidatorIDs)
	signatures, err := canonicalRecoverySignatures(certificate.Signatures)
	if err != nil {
		return err
	}
	writer.WriteUint16(certificate.Version)
	if err := writeBoundedPOSString(writer, certificate.ChainID, posMaxConsensusTextLength, "recovery chain id"); err != nil {
		return err
	}
	writer.WriteUint64(certificate.Sequence)
	writer.WriteUint64(certificate.FinalizedHeight)
	if err := writeHashString(writer, certificate.FinalizedHash, "recovery finalized hash"); err != nil {
		return err
	}
	writer.WriteUint64(certificate.SafetyEpochID)
	if err := writeHashString(writer, certificate.SafetyValidatorSetRoot, "recovery validator set root"); err != nil {
		return err
	}
	if err := writeBoundedPOSString(writer, certificate.ElderValidatorID, posMaxConsensusTextLength, "recovery elder validator id"); err != nil {
		return err
	}
	if err := writeBoundedPOSStringSlice(writer, excludedValidatorIDs, consensus.MaxValidatorsPerSet, "recovery excluded validators"); err != nil {
		return err
	}
	writer.WriteInt64(certificate.ExpiresAtUnixMilli)
	return writeRecoverySignaturesBinary(writer, signatures)
}

func readEmergencyRecoveryCertificateBinary(reader *borsh.Reader) (consensus.EmergencyRecoveryCertificate, error) {
	version, err := reader.ReadUint16()
	if err != nil {
		return consensus.EmergencyRecoveryCertificate{}, fmt.Errorf("posnode: unmarshal recovery version: %w", err)
	}
	chainID, err := readBoundedPOSString(reader, posMaxConsensusTextLength, "recovery chain id")
	if err != nil {
		return consensus.EmergencyRecoveryCertificate{}, err
	}
	sequence, err := reader.ReadUint64()
	if err != nil {
		return consensus.EmergencyRecoveryCertificate{}, fmt.Errorf("posnode: unmarshal recovery sequence: %w", err)
	}
	finalizedHeight, err := reader.ReadUint64()
	if err != nil {
		return consensus.EmergencyRecoveryCertificate{}, fmt.Errorf("posnode: unmarshal recovery finalized height: %w", err)
	}
	finalizedHash, err := readHashString(reader, "recovery finalized hash")
	if err != nil {
		return consensus.EmergencyRecoveryCertificate{}, err
	}
	safetyEpochID, err := reader.ReadUint64()
	if err != nil {
		return consensus.EmergencyRecoveryCertificate{}, fmt.Errorf("posnode: unmarshal recovery safety epoch: %w", err)
	}
	safetyValidatorSetRoot, err := readHashString(reader, "recovery validator set root")
	if err != nil {
		return consensus.EmergencyRecoveryCertificate{}, err
	}
	elderValidatorID, err := readBoundedPOSString(reader, posMaxConsensusTextLength, "recovery elder validator id")
	if err != nil {
		return consensus.EmergencyRecoveryCertificate{}, err
	}
	excludedValidatorIDs, err := readBoundedPOSStringSlice(reader, consensus.MaxValidatorsPerSet, "recovery excluded validators")
	if err != nil {
		return consensus.EmergencyRecoveryCertificate{}, err
	}
	expiresAtUnixMilli, err := reader.ReadInt64()
	if err != nil {
		return consensus.EmergencyRecoveryCertificate{}, fmt.Errorf("posnode: unmarshal recovery expiry: %w", err)
	}
	signatures, err := readRecoverySignaturesBinary(reader)
	if err != nil {
		return consensus.EmergencyRecoveryCertificate{}, err
	}
	certificate := consensus.EmergencyRecoveryCertificate{
		Version:                version,
		ChainID:                chainID,
		Sequence:               sequence,
		FinalizedHeight:        finalizedHeight,
		FinalizedHash:          finalizedHash,
		SafetyEpochID:          safetyEpochID,
		SafetyValidatorSetRoot: safetyValidatorSetRoot,
		ElderValidatorID:       elderValidatorID,
		ExcludedValidatorIDs:   excludedValidatorIDs,
		ExpiresAtUnixMilli:     expiresAtUnixMilli,
		Signatures:             signatures,
	}
	if _, err := certificate.SignBytes(); err != nil {
		return consensus.EmergencyRecoveryCertificate{}, err
	}
	return certificate, nil
}

func writeMultiValidatorActivationCertificateBinary(writer *borsh.Writer, certificate consensus.MultiValidatorActivationCertificate) error {
	if _, err := certificate.SignBytes(); err != nil {
		return err
	}
	signatures, err := canonicalRecoverySignatures(certificate.Signatures)
	if err != nil {
		return err
	}
	writer.WriteUint16(certificate.Version)
	if err := writeBoundedPOSString(writer, certificate.ChainID, posMaxConsensusTextLength, "activation chain id"); err != nil {
		return err
	}
	writer.WriteUint64(certificate.Sequence)
	writer.WriteUint64(certificate.SafetyFinalizedHeight)
	if err := writeHashString(writer, certificate.SafetyFinalizedHash, "activation safety hash"); err != nil {
		return err
	}
	writer.WriteUint64(certificate.SafetyEpochID)
	if err := writeHashString(writer, certificate.SafetyValidatorSetRoot, "activation safety validator root"); err != nil {
		return err
	}
	writer.WriteUint64(certificate.TargetHeight)
	if err := writeHashString(writer, certificate.TargetHash, "activation target hash"); err != nil {
		return err
	}
	writer.WriteUint64(certificate.TargetEpochID)
	if err := writeHashString(writer, certificate.TargetValidatorSetRoot, "activation target validator root"); err != nil {
		return err
	}
	writer.WriteInt64(certificate.ExpiresAtUnixMilli)
	return writeRecoverySignaturesBinary(writer, signatures)
}

func readMultiValidatorActivationCertificateBinary(reader *borsh.Reader) (consensus.MultiValidatorActivationCertificate, error) {
	version, err := reader.ReadUint16()
	if err != nil {
		return consensus.MultiValidatorActivationCertificate{}, fmt.Errorf("posnode: unmarshal activation version: %w", err)
	}
	chainID, err := readBoundedPOSString(reader, posMaxConsensusTextLength, "activation chain id")
	if err != nil {
		return consensus.MultiValidatorActivationCertificate{}, err
	}
	sequence, err := reader.ReadUint64()
	if err != nil {
		return consensus.MultiValidatorActivationCertificate{}, fmt.Errorf("posnode: unmarshal activation sequence: %w", err)
	}
	safetyFinalizedHeight, err := reader.ReadUint64()
	if err != nil {
		return consensus.MultiValidatorActivationCertificate{}, fmt.Errorf("posnode: unmarshal activation safety height: %w", err)
	}
	safetyFinalizedHash, err := readHashString(reader, "activation safety hash")
	if err != nil {
		return consensus.MultiValidatorActivationCertificate{}, err
	}
	safetyEpochID, err := reader.ReadUint64()
	if err != nil {
		return consensus.MultiValidatorActivationCertificate{}, fmt.Errorf("posnode: unmarshal activation safety epoch: %w", err)
	}
	safetyValidatorSetRoot, err := readHashString(reader, "activation safety validator root")
	if err != nil {
		return consensus.MultiValidatorActivationCertificate{}, err
	}
	targetHeight, err := reader.ReadUint64()
	if err != nil {
		return consensus.MultiValidatorActivationCertificate{}, fmt.Errorf("posnode: unmarshal activation target height: %w", err)
	}
	targetHash, err := readHashString(reader, "activation target hash")
	if err != nil {
		return consensus.MultiValidatorActivationCertificate{}, err
	}
	targetEpochID, err := reader.ReadUint64()
	if err != nil {
		return consensus.MultiValidatorActivationCertificate{}, fmt.Errorf("posnode: unmarshal activation target epoch: %w", err)
	}
	targetValidatorSetRoot, err := readHashString(reader, "activation target validator root")
	if err != nil {
		return consensus.MultiValidatorActivationCertificate{}, err
	}
	expiresAtUnixMilli, err := reader.ReadInt64()
	if err != nil {
		return consensus.MultiValidatorActivationCertificate{}, fmt.Errorf("posnode: unmarshal activation expiry: %w", err)
	}
	signatures, err := readRecoverySignaturesBinary(reader)
	if err != nil {
		return consensus.MultiValidatorActivationCertificate{}, err
	}
	certificate := consensus.MultiValidatorActivationCertificate{
		Version:                version,
		ChainID:                chainID,
		Sequence:               sequence,
		SafetyFinalizedHeight:  safetyFinalizedHeight,
		SafetyFinalizedHash:    safetyFinalizedHash,
		SafetyEpochID:          safetyEpochID,
		SafetyValidatorSetRoot: safetyValidatorSetRoot,
		TargetHeight:           targetHeight,
		TargetHash:             targetHash,
		TargetEpochID:          targetEpochID,
		TargetValidatorSetRoot: targetValidatorSetRoot,
		ExpiresAtUnixMilli:     expiresAtUnixMilli,
		Signatures:             signatures,
	}
	if _, err := certificate.SignBytes(); err != nil {
		return consensus.MultiValidatorActivationCertificate{}, err
	}
	return certificate, nil
}

func canonicalRecoverySignatures(signatures []consensus.EmergencyRecoverySignature) ([]consensus.EmergencyRecoverySignature, error) {
	if len(signatures) == 0 || len(signatures) > consensus.MaxValidatorsPerSet {
		return nil, fmt.Errorf("posnode: invalid recovery signature count")
	}
	canonical := append([]consensus.EmergencyRecoverySignature(nil), signatures...)
	for index := range canonical {
		canonical[index].ValidatorID = strings.TrimSpace(canonical[index].ValidatorID)
		canonical[index].PublicKey = strings.TrimSpace(canonical[index].PublicKey)
		canonical[index].Signature = strings.TrimSpace(canonical[index].Signature)
	}
	sort.Slice(canonical, func(left int, right int) bool {
		return canonical[left].ValidatorID < canonical[right].ValidatorID
	})
	for index, item := range canonical {
		if index > 0 && item.ValidatorID == canonical[index-1].ValidatorID {
			return nil, fmt.Errorf("posnode: duplicate recovery validator signature")
		}
		if err := validateValidatorSignatureFields(item.ValidatorID, item.PublicKey, item.Signature, "recovery", index); err != nil {
			return nil, err
		}
	}
	return canonical, nil
}

func writeRecoverySignaturesBinary(writer *borsh.Writer, signatures []consensus.EmergencyRecoverySignature) error {
	writer.WriteUint32(uint32(len(signatures)))
	for index, item := range signatures {
		if err := writeValidatorSignatureBinary(writer, item.ValidatorID, item.PublicKey, item.Signature, "recovery", index); err != nil {
			return err
		}
	}
	return nil
}

func readRecoverySignaturesBinary(reader *borsh.Reader) ([]consensus.EmergencyRecoverySignature, error) {
	count, err := readCount(reader, consensus.MaxValidatorsPerSet, "recovery signatures")
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, fmt.Errorf("posnode: recovery signatures are empty")
	}
	signatures := make([]consensus.EmergencyRecoverySignature, count)
	for index := 0; index < count; index++ {
		validatorID, publicKey, signature, err := readValidatorSignatureBinary(reader, "recovery", index)
		if err != nil {
			return nil, err
		}
		signatures[index] = consensus.EmergencyRecoverySignature{
			ValidatorID: validatorID,
			PublicKey:   publicKey,
			Signature:   signature,
		}
	}
	return canonicalRecoverySignatures(signatures)
}

func writeValidatorSignatureBinary(writer *borsh.Writer, validatorID string, publicKeyText string, signatureText string, label string, index int) error {
	if err := validateValidatorSignatureFields(validatorID, publicKeyText, signatureText, label, index); err != nil {
		return err
	}
	publicKey, _ := structure.PublicKeyFromBase58(strings.TrimSpace(publicKeyText))
	signature, _ := structure.SignatureFromBase58(strings.TrimSpace(signatureText))
	if err := writeBoundedPOSString(writer, validatorID, posMaxConsensusTextLength, label+" validator id"); err != nil {
		return err
	}
	writer.WriteFixedBytes(publicKey[:])
	writer.WriteFixedBytes(signature[:])
	return nil
}

func readValidatorSignatureBinary(reader *borsh.Reader, label string, index int) (string, string, string, error) {
	validatorID, err := readBoundedPOSString(reader, posMaxConsensusTextLength, label+" validator id")
	if err != nil {
		return "", "", "", err
	}
	publicKey, err := readPublicKey(reader, label+" public key")
	if err != nil {
		return "", "", "", err
	}
	signature, err := readSignature(reader, label+" signature")
	if err != nil {
		return "", "", "", err
	}
	if err := validateValidatorSignatureFields(validatorID, publicKey.String(), signature.String(), label, index); err != nil {
		return "", "", "", err
	}
	return validatorID, publicKey.String(), signature.String(), nil
}

func validateValidatorSignatureFields(validatorID string, publicKeyText string, signatureText string, label string, index int) error {
	if strings.TrimSpace(validatorID) == "" || len(validatorID) > posMaxConsensusTextLength {
		return fmt.Errorf("posnode: invalid %s validator id at index %d", label, index)
	}
	if _, err := structure.PublicKeyFromBase58(strings.TrimSpace(publicKeyText)); err != nil {
		return fmt.Errorf("posnode: invalid %s public key at index %d: %w", label, index, err)
	}
	if _, err := structure.SignatureFromBase58(strings.TrimSpace(signatureText)); err != nil {
		return fmt.Errorf("posnode: invalid %s signature at index %d: %w", label, index, err)
	}
	return nil
}

func writeBoundedPOSString(writer *borsh.Writer, value string, maxLength int, label string) error {
	if len(value) > maxLength {
		return fmt.Errorf("posnode: %s length %d exceeds %d", label, len(value), maxLength)
	}
	if err := writer.WriteString(value); err != nil {
		return fmt.Errorf("posnode: marshal %s: %w", label, err)
	}
	return nil
}

func readBoundedPOSString(reader *borsh.Reader, maxLength int, label string) (string, error) {
	valueBytes, err := readBoundedPOSBytes(reader, maxLength, label)
	if err != nil {
		return "", err
	}
	if !utf8.Valid(valueBytes) {
		return "", fmt.Errorf("posnode: %s is not valid utf8", label)
	}
	return string(valueBytes), nil
}

func readBoundedPOSBytes(reader *borsh.Reader, maxLength int, label string) ([]byte, error) {
	length, err := reader.ReadUint32()
	if err != nil {
		return nil, fmt.Errorf("posnode: unmarshal %s length: %w", label, err)
	}
	if length > uint32(maxLength) {
		return nil, fmt.Errorf("posnode: %s length %d exceeds %d", label, length, maxLength)
	}
	value, err := reader.ReadFixedBytes(int(length))
	if err != nil {
		return nil, fmt.Errorf("posnode: unmarshal %s: %w", label, err)
	}
	return value, nil
}

func writeBoundedPOSStringSlice(writer *borsh.Writer, values []string, maxCount int, label string) error {
	if len(values) > maxCount {
		return fmt.Errorf("posnode: %s count exceeds limit", label)
	}
	writer.WriteUint32(uint32(len(values)))
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		if _, exists := seen[value]; exists {
			return fmt.Errorf("posnode: duplicate %s entry", label)
		}
		seen[value] = struct{}{}
		if err := writeBoundedPOSString(writer, value, posMaxConsensusTextLength, fmt.Sprintf("%s %d", label, index)); err != nil {
			return err
		}
	}
	return nil
}

func readBoundedPOSStringSlice(reader *borsh.Reader, maxCount int, label string) ([]string, error) {
	count, err := readCount(reader, maxCount, label)
	if err != nil {
		return nil, err
	}
	values := make([]string, count)
	seen := make(map[string]struct{}, count)
	for index := 0; index < count; index++ {
		value, err := readBoundedPOSString(reader, posMaxConsensusTextLength, fmt.Sprintf("%s %d", label, index))
		if err != nil {
			return nil, err
		}
		if _, exists := seen[value]; exists {
			return nil, fmt.Errorf("posnode: duplicate %s entry", label)
		}
		seen[value] = struct{}{}
		values[index] = value
	}
	return values, nil
}
