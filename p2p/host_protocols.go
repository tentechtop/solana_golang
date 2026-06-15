package p2p

import "context"

func (host *Host) registerDefaultProtocolHandlers() error {
	if _, ok := host.registry.Spec(ProtocolFindNodeRequestV1); ok {
		return host.registerDefaultDiscoveryHandlers()
	}
	spec := ProtocolSpec{
		ID:          ProtocolFindNodeRequestV1,
		Name:        "/p2p/find-node/request/1.0.0",
		HasResponse: true,
		Priority:    MessagePriorityNormal,
		Concurrency: defaultProtocolConcurrency(ProtocolFindNodeRequestV1),
	}
	if err := host.RegisterResultHandler(spec, host.handleFindNodeRequest); err != nil {
		return err
	}
	return host.registerDefaultDiscoveryHandlers()
}

func (host *Host) registerDefaultDiscoveryHandlers() error {
	if _, ok := host.registry.Spec(ProtocolIdentifyRequestV1); !ok {
		spec := ProtocolSpec{
			ID:          ProtocolIdentifyRequestV1,
			Name:        "/p2p/identify/request/1.0.0",
			HasResponse: true,
			Priority:    MessagePriorityHigh,
			Concurrency: defaultProtocolConcurrency(ProtocolIdentifyRequestV1),
		}
		if err := host.RegisterResultHandler(spec, host.handleIdentifyRequest); err != nil {
			return err
		}
	}
	if _, ok := host.registry.Spec(ProtocolPeerHintsV1); !ok {
		spec := ProtocolSpec{
			ID:          ProtocolPeerHintsV1,
			Name:        "/p2p/peer-hints/1.0.0",
			HasResponse: false,
			Priority:    MessagePriorityNormal,
			Concurrency: defaultProtocolConcurrency(ProtocolPeerHintsV1),
		}
		if err := host.RegisterVoidHandler(spec, host.handlePeerHints); err != nil {
			return err
		}
	}
	return nil
}

func (host *Host) handleFindNodeRequest(ctx context.Context, message Message) (Message, error) {
	request, err := UnmarshalKADFindNodeRequestBinary(message.Payload)
	if err != nil {
		return Message{}, err
	}
	peers, err := host.ClosestPeers(request.TargetPeerID, int(request.Limit))
	if err != nil {
		return Message{}, err
	}
	responsePayload, err := NewKADFindNodeResponse(request.TargetPeerID, peers)
	if err != nil {
		return Message{}, err
	}
	payload, err := responsePayload.MarshalBinary()
	if err != nil {
		return Message{}, err
	}
	return responseFor(message, host.peerID, ProtocolFindNodeResponseV1, payload)
}
