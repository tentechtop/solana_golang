package p2p

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"solana_golang/codec/borsh"
	"solana_golang/utils"
)

const (
	identifyProtocolVersion uint16 = 1
	maxPeerHintRecords             = 32
)

// IdentifyRequest 保存节点身份查询 + 让新连接主动获取对端签名地址记录。
type IdentifyRequest struct {
	Version            uint16
	CreatedAtUnixMilli int64
}

// IdentifyResponse 保存节点身份响应 + 承载本节点签名地址记录。
type IdentifyResponse struct {
	Version            uint16
	Record             []byte
	CreatedAtUnixMilli int64
}

// PeerHintsPayload 保存地址广播载荷 + 用于传播多个已签名节点记录。
type PeerHintsPayload struct {
	Version            uint16
	Records            [][]byte
	CreatedAtUnixMilli int64
}

func NewIdentifyRequest() IdentifyRequest {
	return IdentifyRequest{Version: identifyProtocolVersion, CreatedAtUnixMilli: time.Now().UnixMilli()}
}

func (request IdentifyRequest) MarshalBinary() ([]byte, error) {
	if err := request.Validate(); err != nil {
		return nil, err
	}
	writer := borsh.NewWriter(maxPeerRecordSize)
	writer.WriteUint16(request.Version)
	writer.WriteInt64(request.CreatedAtUnixMilli)
	return writer.BytesView(), nil
}

func UnmarshalIdentifyRequestBinary(data []byte) (IdentifyRequest, error) {
	reader := borsh.NewBorrowedReader(data, maxPeerRecordSize)
	version, err := reader.ReadUint16()
	if err != nil {
		return IdentifyRequest{}, fmt.Errorf("p2p: read identify version: %w", err)
	}
	createdAtUnixMilli, err := reader.ReadInt64()
	if err != nil {
		return IdentifyRequest{}, fmt.Errorf("p2p: read identify time: %w", err)
	}
	if err := reader.EnsureEOF(); err != nil {
		return IdentifyRequest{}, fmt.Errorf("p2p: read identify eof: %w", err)
	}
	request := IdentifyRequest{Version: version, CreatedAtUnixMilli: createdAtUnixMilli}
	return request, request.Validate()
}

func (request IdentifyRequest) Validate() error {
	if request.Version != identifyProtocolVersion {
		return fmt.Errorf("%w: unsupported identify version", ErrInvalidMessage)
	}
	if request.CreatedAtUnixMilli <= 0 {
		return fmt.Errorf("%w: invalid identify time", ErrInvalidMessage)
	}
	return nil
}

func NewIdentifyResponse(record []byte) (IdentifyResponse, error) {
	response := IdentifyResponse{
		Version:            identifyProtocolVersion,
		Record:             utils.CloneBytes(record),
		CreatedAtUnixMilli: time.Now().UnixMilli(),
	}
	return response, response.Validate()
}

func (response IdentifyResponse) MarshalBinary() ([]byte, error) {
	if err := response.Validate(); err != nil {
		return nil, err
	}
	writer := borsh.NewWriter(maxPeerRecordSize)
	writer.WriteUint16(response.Version)
	if err := writer.WriteBytes(response.Record); err != nil {
		return nil, fmt.Errorf("p2p: marshal identify record: %w", err)
	}
	writer.WriteInt64(response.CreatedAtUnixMilli)
	return writer.BytesView(), nil
}

func UnmarshalIdentifyResponseBinary(data []byte) (IdentifyResponse, error) {
	reader := borsh.NewBorrowedReader(data, maxPeerRecordSize)
	version, err := reader.ReadUint16()
	if err != nil {
		return IdentifyResponse{}, fmt.Errorf("p2p: read identify response version: %w", err)
	}
	record, err := reader.ReadBytes()
	if err != nil {
		return IdentifyResponse{}, fmt.Errorf("p2p: read identify record: %w", err)
	}
	createdAtUnixMilli, err := reader.ReadInt64()
	if err != nil {
		return IdentifyResponse{}, fmt.Errorf("p2p: read identify response time: %w", err)
	}
	if err := reader.EnsureEOF(); err != nil {
		return IdentifyResponse{}, fmt.Errorf("p2p: read identify response eof: %w", err)
	}
	response := IdentifyResponse{Version: version, Record: record, CreatedAtUnixMilli: createdAtUnixMilli}
	return response, response.Validate()
}

func (response IdentifyResponse) Validate() error {
	if response.Version != identifyProtocolVersion {
		return fmt.Errorf("%w: unsupported identify response version", ErrInvalidMessage)
	}
	if response.CreatedAtUnixMilli <= 0 {
		return fmt.Errorf("%w: invalid identify response time", ErrInvalidMessage)
	}
	if _, err := UnmarshalSignedPeerRecordBinary(response.Record); err != nil {
		return err
	}
	return nil
}

func NewPeerHintsPayload(records [][]byte) (PeerHintsPayload, error) {
	payload := PeerHintsPayload{
		Version:            identifyProtocolVersion,
		Records:            cloneRecordBytes(records),
		CreatedAtUnixMilli: time.Now().UnixMilli(),
	}
	return payload, payload.Validate()
}

func (payload PeerHintsPayload) MarshalBinary() ([]byte, error) {
	if err := payload.Validate(); err != nil {
		return nil, err
	}
	writer := borsh.NewWriter(DefaultMaxMessageSize)
	writer.WriteUint16(payload.Version)
	writer.WriteUint32(uint32(len(payload.Records)))
	for _, record := range payload.Records {
		if err := writer.WriteBytes(record); err != nil {
			return nil, fmt.Errorf("p2p: marshal peer hint record: %w", err)
		}
	}
	writer.WriteInt64(payload.CreatedAtUnixMilli)
	return writer.BytesView(), nil
}

func UnmarshalPeerHintsPayloadBinary(data []byte) (PeerHintsPayload, error) {
	reader := borsh.NewBorrowedReader(data, DefaultMaxMessageSize)
	version, err := reader.ReadUint16()
	if err != nil {
		return PeerHintsPayload{}, fmt.Errorf("p2p: read peer hints version: %w", err)
	}
	count, err := reader.ReadUint32()
	if err != nil {
		return PeerHintsPayload{}, fmt.Errorf("p2p: read peer hints count: %w", err)
	}
	if count > maxPeerHintRecords {
		return PeerHintsPayload{}, fmt.Errorf("%w: too many peer hint records", ErrInvalidMessage)
	}
	records := make([][]byte, 0, int(count))
	for index := 0; index < int(count); index++ {
		record, err := reader.ReadBytes()
		if err != nil {
			return PeerHintsPayload{}, fmt.Errorf("p2p: read peer hint record: %w", err)
		}
		records = append(records, record)
	}
	createdAtUnixMilli, err := reader.ReadInt64()
	if err != nil {
		return PeerHintsPayload{}, fmt.Errorf("p2p: read peer hints time: %w", err)
	}
	if err := reader.EnsureEOF(); err != nil {
		return PeerHintsPayload{}, fmt.Errorf("p2p: read peer hints eof: %w", err)
	}
	payload := PeerHintsPayload{Version: version, Records: records, CreatedAtUnixMilli: createdAtUnixMilli}
	return payload, payload.Validate()
}

func (payload PeerHintsPayload) Validate() error {
	if payload.Version != identifyProtocolVersion {
		return fmt.Errorf("%w: unsupported peer hints version", ErrInvalidMessage)
	}
	if payload.CreatedAtUnixMilli <= 0 {
		return fmt.Errorf("%w: invalid peer hints time", ErrInvalidMessage)
	}
	if len(payload.Records) > maxPeerHintRecords {
		return fmt.Errorf("%w: too many peer hint records", ErrInvalidMessage)
	}
	for _, record := range payload.Records {
		if _, err := UnmarshalSignedPeerRecordBinary(record); err != nil {
			return err
		}
	}
	return nil
}

func (host *Host) handleIdentifyRequest(ctx context.Context, message Message) (Message, error) {
	if _, err := UnmarshalIdentifyRequestBinary(message.Payload); err != nil {
		return Message{}, err
	}
	record, err := host.localSignedPeerRecord()
	if err != nil {
		return Message{}, err
	}
	encodedRecord, err := record.MarshalBinary()
	if err != nil {
		return Message{}, err
	}
	responsePayload, err := NewIdentifyResponse(encodedRecord)
	if err != nil {
		return Message{}, err
	}
	payload, err := responsePayload.MarshalBinary()
	if err != nil {
		return Message{}, err
	}
	return responseFor(message, host.peerID, ProtocolIdentifyResponseV1, payload)
}

func (host *Host) handlePeerHints(ctx context.Context, message Message) error {
	payload, err := UnmarshalPeerHintsPayloadBinary(message.Payload)
	if err != nil {
		host.metrics.peerRecordsRejected.Add(1)
		return err
	}
	for _, record := range payload.Records {
		if err := host.importSignedPeerRecord(record, "peer-hints"); err != nil {
			host.metrics.peerRecordsRejected.Add(1)
			host.logger.Warn("p2p peer hint rejected",
				slog.String("from_peer_id", message.FromPeerID),
				slog.Any("error", err),
			)
			continue
		}
	}
	return ctx.Err()
}

// IdentifyPeer 主动识别对端节点 + 获取并保存对端签名地址记录。
func (host *Host) IdentifyPeer(ctx context.Context, peerID string) error {
	connection, ok := host.Connection(peerID)
	if !ok {
		return fmt.Errorf("%w: %s", ErrPeerNotFound, peerID)
	}
	return host.identifyPeerOnConnection(ctx, connection, peerID)
}

// SendPeerHints 发送已签名节点提示 + 让上层发现流程复用统一 addr 协议。
func (host *Host) SendPeerHints(ctx context.Context, peerID string, peers []Peer) error {
	records := make([][]byte, 0, len(peers))
	for _, peer := range peers {
		record, ok, err := signedPeerRecordFromPeer(peer, host.expectedPeerRecordNetworkID())
		if err != nil || !ok {
			continue
		}
		encoded, err := record.MarshalBinary()
		if err != nil {
			continue
		}
		records = append(records, encoded)
	}
	payload, err := NewPeerHintsPayload(records)
	if err != nil {
		return err
	}
	encodedPayload, err := payload.MarshalBinary()
	if err != nil {
		return err
	}
	message, err := NewMessage(ProtocolPeerHintsV1, encodedPayload)
	if err != nil {
		return err
	}
	return host.Send(ctx, peerID, message)
}

func (host *Host) identifyPeerOnConnection(ctx context.Context, connection Connection, peerID string) error {
	if ctx == nil {
		ctx = host.lifecycleContext
	}
	host.metrics.identifyStarted.Add(1)
	requestPayload, err := NewIdentifyRequest().MarshalBinary()
	if err != nil {
		host.metrics.identifyFailed.Add(1)
		return err
	}
	request, err := NewRequestMessage(host.peerID, ProtocolIdentifyRequestV1, requestPayload)
	if err != nil {
		host.metrics.identifyFailed.Add(1)
		return err
	}
	request.ToPeerID = peerID

	queryContext, cancel := context.WithTimeout(ctx, host.dialTimeout)
	defer cancel()
	response, err := host.requestOnConnection(queryContext, connection, peerID, request)
	if err != nil {
		host.metrics.identifyFailed.Add(1)
		return err
	}
	if response.Type != ProtocolIdentifyResponseV1 {
		host.metrics.identifyFailed.Add(1)
		return fmt.Errorf("%w: invalid identify response type", ErrInvalidMessage)
	}
	payload, err := UnmarshalIdentifyResponseBinary(response.Payload)
	if err != nil {
		host.metrics.identifyFailed.Add(1)
		return err
	}
	if err := host.importSignedPeerRecord(payload.Record, "identify"); err != nil {
		host.metrics.identifyFailed.Add(1)
		return err
	}
	host.metrics.identifySucceeded.Add(1)
	host.logger.Info("p2p identify completed", slog.String("peer_id", peerID))
	return nil
}

func (host *Host) identifyPeerAsync(connection Connection, peerID string) {
	if !host.secureSession || connection == nil || peerID == "" {
		return
	}
	go func() {
		if err := host.identifyPeerOnConnection(host.lifecycleContext, connection, peerID); err != nil {
			host.logger.Debug("p2p identify skipped",
				slog.String("peer_id", peerID),
				slog.Any("error", err),
			)
		}
	}()
}

func (host *Host) importSignedPeerRecord(recordBytes []byte, source string) error {
	record, err := UnmarshalSignedPeerRecordBinary(recordBytes)
	if err != nil {
		return err
	}
	if err := record.VerifyNetwork(host.expectedPeerRecordNetworkID()); err != nil {
		return err
	}
	peer, err := record.ToPeer()
	if err != nil {
		return err
	}
	if err := host.AddPeer(peer); err != nil {
		return err
	}
	host.metrics.peerRecordsAccepted.Add(1)
	host.logger.Debug("p2p peer record accepted",
		slog.String("peer_id", peer.ID),
		slog.String("source", source),
		slog.Int("advertised_addresses", len(peer.advertisedAddressList())),
	)
	return nil
}

func (host *Host) localSignedPeerRecord() (SignedPeerRecord, error) {
	if !host.secureSession {
		return SignedPeerRecord{}, fmt.Errorf("%w: secure identity required for peer record", ErrSecureSession)
	}
	addresses := host.advertisedAddressSnapshots()
	if len(addresses) == 0 {
		return SignedPeerRecord{}, fmt.Errorf("%w: no advertised address", ErrInvalidMessage)
	}
	peer, err := NewPeer(host.peerID, addresses)
	if err != nil {
		return SignedPeerRecord{}, err
	}
	peer.Role = PeerRoleFull
	peer.Capabilities = PeerCapabilityDHT
	peer.ProtocolVersion = fmt.Sprintf("%d", MessageProtocolVersion)
	peer.SoftwareVersion = host.secureIdentity.SoftwareVersion
	peer.PreferredProtocols = cloneProtocols(host.preferredProtocols)
	return NewSignedPeerRecord(peer, host.secureIdentity, host.peerRecordTTL)
}

func (host *Host) addAdvertisedAddress(address utils.MultiAddress) {
	if address.PeerID != host.peerID {
		return
	}
	if !isDialableAdvertisedAddress(address) {
		host.logger.Warn("p2p advertised address skipped",
			slog.String("address", address.String()),
		)
		return
	}
	rawAddress := address.String()
	host.mutex.Lock()
	defer host.mutex.Unlock()
	for _, existing := range host.advertisedAddresses {
		if existing.String() == rawAddress {
			return
		}
	}
	host.advertisedAddresses = append(host.advertisedAddresses, address)
}

func (host *Host) advertisedAddressSnapshots() []utils.MultiAddress {
	host.mutex.RLock()
	defer host.mutex.RUnlock()
	return cloneAddresses(host.advertisedAddresses)
}

func cloneRecordBytes(records [][]byte) [][]byte {
	if records == nil {
		return nil
	}
	cloned := make([][]byte, len(records))
	for index, record := range records {
		cloned[index] = utils.CloneBytes(record)
	}
	return cloned
}
