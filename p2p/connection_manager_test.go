package p2p

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"solana_golang/utils"
)

func TestHostHandleConnectionRespondsPong(t *testing.T) {
	localPeerID := testPeerID(21)
	remotePeerID := testPeerID(22)
	host, err := NewHost(HostConfig{
		PeerID:        localPeerID,
		AllowInsecure: true,
	})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	request, err := NewRequestMessage(remotePeerID, MessageTypePing, nil)
	if err != nil {
		t.Fatalf("NewRequestMessage() error = %v", err)
	}
	request.ToPeerID = localPeerID
	connection := newScriptedConnection(utils.ProtocolTCP, remotePeerID, []Message{request})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go host.HandleConnection(ctx, connection)

	response := connection.waitWrite(t)
	if response.Type != MessageTypePong {
		t.Fatalf("response.Type = %d, want %d", response.Type, MessageTypePong)
	}
	if response.RequestID != request.ID {
		t.Fatalf("response.RequestID = %q, want %q", response.RequestID, request.ID)
	}
	if response.ToPeerID != remotePeerID {
		t.Fatalf("response.ToPeerID = %q, want %q", response.ToPeerID, remotePeerID)
	}
	if _, ok := host.ConnectionState(remotePeerID); !ok {
		t.Fatal("ConnectionState() ok = false, want true")
	}
}

func TestHostHeartbeatWritesPing(t *testing.T) {
	localPeerID := testPeerID(23)
	remotePeerID := testPeerID(24)
	host, err := NewHost(HostConfig{
		PeerID:        localPeerID,
		AllowInsecure: true,
	})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	connection := newScriptedConnection(utils.ProtocolTCP, remotePeerID, nil)
	if err := host.storeConnection(remotePeerID, connection); err != nil {
		t.Fatalf("storeConnection() error = %v", err)
	}
	host.heartbeatOnce(context.Background())

	message := connection.waitWrite(t)
	if message.Type != MessageTypePing {
		t.Fatalf("message.Type = %d, want %d", message.Type, MessageTypePing)
	}
	if message.FromPeerID != localPeerID {
		t.Fatalf("message.FromPeerID = %q, want %q", message.FromPeerID, localPeerID)
	}
	if message.ToPeerID != remotePeerID {
		t.Fatalf("message.ToPeerID = %q, want %q", message.ToPeerID, remotePeerID)
	}
	state, ok := host.ConnectionState(remotePeerID)
	if !ok {
		t.Fatal("ConnectionState() ok = false, want true")
	}
	if state.LastHeartbeatUnixMilli == 0 {
		t.Fatal("LastHeartbeatUnixMilli = 0, want heartbeat timestamp")
	}
}

func TestHostHeartbeatClosesExpiredConnection(t *testing.T) {
	localPeerID := testPeerID(25)
	remotePeerID := testPeerID(26)
	host, err := NewHost(HostConfig{
		PeerID:         localPeerID,
		AllowInsecure:  true,
		ConnectionIdle: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	connection := newScriptedConnection(utils.ProtocolTCP, remotePeerID, nil)
	if err := host.storeConnection(remotePeerID, connection); err != nil {
		t.Fatalf("storeConnection() error = %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	host.heartbeatOnce(context.Background())

	if _, ok := host.Connection(remotePeerID); ok {
		t.Fatal("Connection() ok = true, want expired connection removed")
	}
	if !connection.closed {
		t.Fatal("connection.closed = false, want true")
	}
}

func TestHostRejectsConnectionsOverLimit(t *testing.T) {
	host, err := NewHost(HostConfig{
		PeerID:         testPeerID(27),
		AllowInsecure:  true,
		MaxConnections: 1,
	})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	firstPeerID := testPeerID(28)
	secondPeerID := testPeerID(29)
	firstConnection := newScriptedConnection(utils.ProtocolTCP, firstPeerID, nil)
	secondConnection := newScriptedConnection(utils.ProtocolTCP, secondPeerID, nil)

	if err := host.storeConnection(firstPeerID, firstConnection); err != nil {
		t.Fatalf("storeConnection(first) error = %v", err)
	}
	if err := host.storeConnection(secondPeerID, secondConnection); !errors.Is(err, ErrMaxConnectionsReached) {
		t.Fatalf("storeConnection(second) error = %v, want ErrMaxConnectionsReached", err)
	}
}

func TestHostRejectsConnectionsOverIPLimit(t *testing.T) {
	host, err := NewHost(HostConfig{
		PeerID:              testPeerID(50),
		AllowInsecure:       true,
		MaxConnections:      4,
		MaxConnectionsPerIP: 1,
	})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	firstPeerID := testPeerID(51)
	secondPeerID := testPeerID(52)
	firstConnection := newScriptedConnection(utils.ProtocolTCP, firstPeerID, nil)
	secondConnection := newScriptedConnection(utils.ProtocolTCP, secondPeerID, nil)

	if err := host.storeConnection(firstPeerID, firstConnection); err != nil {
		t.Fatalf("storeConnection(first) error = %v", err)
	}
	if err := host.storeConnection(secondPeerID, secondConnection); !errors.Is(err, ErrPeerIPLimitReached) {
		t.Fatalf("storeConnection(second) error = %v, want ErrPeerIPLimitReached", err)
	}
	if host.Metrics().PerIPRejected != 1 {
		t.Fatalf("PerIPRejected = %d, want 1", host.Metrics().PerIPRejected)
	}
}

func TestHostRejectsSpoofedConnectionPeer(t *testing.T) {
	localPeerID := testPeerID(30)
	remotePeerID := testPeerID(31)
	spoofedPeerID := testPeerID(32)
	host, err := NewHost(HostConfig{PeerID: localPeerID, AllowInsecure: true})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	message, err := NewMessage(MessageTypeTransaction, nil)
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	message.FromPeerID = spoofedPeerID
	message.ToPeerID = localPeerID
	if err := message.Validate(DefaultMaxMessageSize); err != nil {
		t.Fatalf("message.Validate() error = %v", err)
	}
	connection := newScriptedConnection(utils.ProtocolTCP, remotePeerID, []Message{message})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	host.HandleConnection(ctx, connection)

	if _, ok := host.Connection(spoofedPeerID); ok {
		t.Fatal("spoofed peer connection stored, want rejection")
	}
}

func TestHostRejectsEmptyInboundSender(t *testing.T) {
	localPeerID := testPeerID(71)
	host, err := NewHost(HostConfig{PeerID: localPeerID, AllowInsecure: true})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	message, err := NewMessage(MessageTypeTransaction, nil)
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	message.ToPeerID = localPeerID
	connection := newScriptedConnection(utils.ProtocolTCP, "", []Message{message})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	host.HandleConnection(ctx, connection)

	if host.Metrics().MessagesRejected == 0 {
		t.Fatal("MessagesRejected = 0, want empty sender rejected")
	}
}

func TestHostClosesUnidentifiedIdleConnection(t *testing.T) {
	host, err := NewHost(HostConfig{
		PeerID:           testPeerID(100),
		AllowInsecure:    true,
		HandshakeTimeout: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	connection := newScriptedConnection(utils.ProtocolTCP, "", nil)
	done := make(chan struct{})
	go func() {
		host.HandleConnection(context.Background(), connection)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("HandleConnection() did not close unidentified idle connection")
	}
	if !connection.closed {
		t.Fatal("connection.closed = false, want idle connection closed")
	}
}

func TestHostRejectsEmptyOutboundPeer(t *testing.T) {
	host, err := NewHost(HostConfig{
		PeerID:        testPeerID(72),
		AllowInsecure: true,
	})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	message, err := NewMessage(MessageTypeTransaction, nil)
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	if err := host.Send(context.Background(), "", message); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("Send(empty peer) error = %v, want ErrInvalidMessage", err)
	}
}

func TestHostRequestIgnoresInterleavedPing(t *testing.T) {
	localPeerID := testPeerID(33)
	remotePeerID := testPeerID(34)
	host, err := NewHost(HostConfig{PeerID: localPeerID, AllowInsecure: true})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	connection := newResponsiveConnection(remotePeerID, localPeerID)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go host.HandleConnection(ctx, connection)

	requestPayload, err := NewKADFindNodeRequest(localPeerID, 1)
	if err != nil {
		t.Fatalf("NewKADFindNodeRequest() error = %v", err)
	}
	payload, err := requestPayload.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary() error = %v", err)
	}
	request, err := NewRequestMessage(localPeerID, ProtocolFindNodeRequestV1, payload)
	if err != nil {
		t.Fatalf("NewRequestMessage() error = %v", err)
	}
	request.ToPeerID = remotePeerID

	response, err := host.requestOnConnection(ctx, connection, remotePeerID, request)
	if err != nil {
		t.Fatalf("requestOnConnection() error = %v", err)
	}
	if response.Type != ProtocolFindNodeResponseV1 {
		t.Fatalf("response.Type = %d, want %d", response.Type, ProtocolFindNodeResponseV1)
	}
	if response.RequestID != request.ID {
		t.Fatalf("response.RequestID = %q, want %q", response.RequestID, request.ID)
	}

	pong := connection.waitWrite(t)
	if pong.Type != MessageTypePong {
		t.Fatalf("interleaved response Type = %d, want pong", pong.Type)
	}
}

func TestProtocolQueueDoesNotBlockHeartbeatResponse(t *testing.T) {
	localPeerID := testPeerID(53)
	remotePeerID := testPeerID(54)
	host, err := NewHost(HostConfig{
		PeerID:        localPeerID,
		AllowInsecure: true,
		ProtocolScheduler: ProtocolSchedulerConfig{
			WorkerCount: 1,
		},
	})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	started := make(chan struct{})
	release := make(chan struct{})
	err = host.RegisterVoidHandler(ProtocolSpec{
		ID:          ProtocolReceiveTransactionV1,
		Name:        "/test/slow-transaction/1.0.0",
		HasResponse: false,
		Priority:    MessagePriorityLow,
	}, func(ctx context.Context, message Message) error {
		close(started)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if err != nil {
		t.Fatalf("RegisterVoidHandler() error = %v", err)
	}

	slowMessage, err := NewMessage(ProtocolReceiveTransactionV1, nil)
	if err != nil {
		t.Fatalf("NewMessage(slow) error = %v", err)
	}
	slowMessage.FromPeerID = remotePeerID
	slowMessage.ToPeerID = localPeerID
	ping, err := NewRequestMessage(remotePeerID, MessageTypePing, nil)
	if err != nil {
		t.Fatalf("NewRequestMessage(ping) error = %v", err)
	}
	ping.ToPeerID = localPeerID
	connection := newScriptedConnection(utils.ProtocolTCP, remotePeerID, []Message{slowMessage, ping})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go host.HandleConnection(ctx, connection)

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for slow handler")
	}
	response := connection.waitWrite(t)
	if response.Type != MessageTypePong {
		t.Fatalf("response.Type = %d, want pong", response.Type)
	}
	close(release)
}

func TestProtocolQueueSchedulesHighPriorityBeforeLowPriority(t *testing.T) {
	localPeerID := testPeerID(55)
	remotePeerID := testPeerID(56)
	host, err := NewHost(HostConfig{
		PeerID:        localPeerID,
		AllowInsecure: true,
		ProtocolScheduler: ProtocolSchedulerConfig{
			WorkerCount: 1,
		},
	})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	order := make(chan string, 2)
	if err := host.RegisterVoidHandler(ProtocolSpec{
		ID:          ProtocolReceiveBlockV1,
		Name:        "/test/first-low/1.0.0",
		HasResponse: false,
		Priority:    MessagePriorityLow,
	}, func(ctx context.Context, message Message) error {
		close(firstStarted)
		select {
		case <-releaseFirst:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}); err != nil {
		t.Fatalf("RegisterVoidHandler(first) error = %v", err)
	}
	if err := host.RegisterVoidHandler(ProtocolSpec{
		ID:          ProtocolReceiveTransactionV1,
		Name:        "/test/queued-low/1.0.0",
		HasResponse: false,
		Priority:    MessagePriorityLow,
	}, func(ctx context.Context, message Message) error {
		order <- "low"
		return nil
	}); err != nil {
		t.Fatalf("RegisterVoidHandler(low) error = %v", err)
	}
	if err := host.RegisterVoidHandler(ProtocolSpec{
		ID:          ProtocolHotStuffVoteV1,
		Name:        "/test/queued-high/1.0.0",
		HasResponse: false,
		Priority:    MessagePriorityHigh,
	}, func(ctx context.Context, message Message) error {
		order <- "high"
		return nil
	}); err != nil {
		t.Fatalf("RegisterVoidHandler(high) error = %v", err)
	}

	first := testNetworkMessage(t, ProtocolReceiveBlockV1, remotePeerID, localPeerID)
	low := testNetworkMessage(t, ProtocolReceiveTransactionV1, remotePeerID, localPeerID)
	high := testNetworkMessage(t, ProtocolHotStuffVoteV1, remotePeerID, localPeerID)
	connection := newScriptedConnection(utils.ProtocolTCP, remotePeerID, []Message{first, low, high})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go host.HandleConnection(ctx, connection)

	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first low handler")
	}
	waitForProtocolJobsQueued(t, host, 3)
	close(releaseFirst)

	if got := waitProtocolOrder(t, order); got != "high" {
		t.Fatalf("first queued handler = %q, want high", got)
	}
	if got := waitProtocolOrder(t, order); got != "low" {
		t.Fatalf("second queued handler = %q, want low", got)
	}
}

func TestRequestManagerRequiresPeerAndResponseType(t *testing.T) {
	localPeerID := testPeerID(38)
	remotePeerID := testPeerID(39)
	otherPeerID := testPeerID(40)
	manager := newRequestManager()
	request, err := NewRequestMessage(localPeerID, ProtocolFindNodeRequestV1, nil)
	if err != nil {
		t.Fatalf("NewRequestMessage() error = %v", err)
	}

	waiter, unregister, err := manager.register(request.ID, remotePeerID, ProtocolFindNodeResponseV1, true)
	if err != nil {
		t.Fatalf("register() error = %v", err)
	}
	defer unregister()

	wrongPeerResponse, err := NewResponseMessage(otherPeerID, ProtocolFindNodeResponseV1, request.ID, nil)
	if err != nil {
		t.Fatalf("NewResponseMessage(wrong peer) error = %v", err)
	}
	if manager.fulfill(wrongPeerResponse) {
		t.Fatal("fulfill(wrong peer) = true, want false")
	}
	assertNoRequestResponse(t, waiter)

	wrongTypeResponse, err := NewResponseMessage(remotePeerID, ProtocolIdentifyResponseV1, request.ID, nil)
	if err != nil {
		t.Fatalf("NewResponseMessage(wrong type) error = %v", err)
	}
	if manager.fulfill(wrongTypeResponse) {
		t.Fatal("fulfill(wrong type) = true, want false")
	}
	assertNoRequestResponse(t, waiter)

	expectedResponse, err := NewResponseMessage(remotePeerID, ProtocolFindNodeResponseV1, request.ID, nil)
	if err != nil {
		t.Fatalf("NewResponseMessage(expected) error = %v", err)
	}
	if !manager.fulfill(expectedResponse) {
		t.Fatal("fulfill(expected) = false, want true")
	}
	select {
	case response := <-waiter:
		if response.ID != expectedResponse.ID {
			t.Fatalf("response.ID = %q, want %q", response.ID, expectedResponse.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for expected response")
	}
}

func TestHostRateLimitsInboundMessages(t *testing.T) {
	localPeerID := testPeerID(41)
	remotePeerID := testPeerID(42)
	host, err := NewHost(HostConfig{
		PeerID:        localPeerID,
		AllowInsecure: true,
		PeerProtection: PeerProtectionConfig{
			MaxInboundMessagesPerSecond: 1,
			MessageRateWindow:           time.Hour,
			BlockScore:                  -1000,
		},
	})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	first, err := NewRequestMessage(remotePeerID, MessageTypePing, nil)
	if err != nil {
		t.Fatalf("NewRequestMessage(first) error = %v", err)
	}
	first.ToPeerID = localPeerID
	second, err := NewRequestMessage(remotePeerID, MessageTypePing, nil)
	if err != nil {
		t.Fatalf("NewRequestMessage(second) error = %v", err)
	}
	second.ToPeerID = localPeerID
	connection := newScriptedConnection(utils.ProtocolTCP, remotePeerID, []Message{first, second})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	host.HandleConnection(ctx, connection)

	if !connection.closed {
		t.Fatal("connection.closed = false, want rate-limited connection closed")
	}
	if host.Metrics().MessagesRateLimited != 1 {
		t.Fatalf("MessagesRateLimited = %d, want 1", host.Metrics().MessagesRateLimited)
	}
	response := connection.waitWrite(t)
	if response.Type != MessageTypePong {
		t.Fatalf("response.Type = %d, want pong", response.Type)
	}
}

func TestPeerProtectionRejectsDuplicateMessage(t *testing.T) {
	protection := newPeerProtection(PeerProtectionConfig{})
	peerID := testPeerID(43)
	now := time.Now()
	if _, err := protection.acceptInboundMessage(peerID, "message-1", now); err != nil {
		t.Fatalf("acceptInboundMessage(first) error = %v", err)
	}
	snapshot, err := protection.acceptInboundMessage(peerID, "message-1", now.Add(time.Millisecond))
	if !errors.Is(err, ErrDuplicateMessage) {
		t.Fatalf("acceptInboundMessage(duplicate) error = %v, want ErrDuplicateMessage", err)
	}
	if snapshot.Score >= 0 {
		t.Fatalf("snapshot.Score = %d, want negative score", snapshot.Score)
	}
}

func TestHostBroadcastDeduplicatesAndLimitsPeers(t *testing.T) {
	localPeerID := testPeerID(44)
	firstPeerID := testPeerID(45)
	secondPeerID := testPeerID(46)
	thirdPeerID := testPeerID(47)
	host, err := NewHost(HostConfig{
		PeerID:        localPeerID,
		AllowInsecure: true,
		PeerProtection: PeerProtectionConfig{
			MaxBroadcastPeers: 2,
		},
	})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	firstConnection := newScriptedConnection(utils.ProtocolTCP, firstPeerID, nil)
	secondConnection := newScriptedConnection(utils.ProtocolTCP, secondPeerID, nil)
	thirdConnection := newScriptedConnection(utils.ProtocolTCP, thirdPeerID, nil)
	if err := host.storeConnection(firstPeerID, firstConnection); err != nil {
		t.Fatalf("storeConnection(first) error = %v", err)
	}
	if err := host.storeConnection(secondPeerID, secondConnection); err != nil {
		t.Fatalf("storeConnection(second) error = %v", err)
	}
	if err := host.storeConnection(thirdPeerID, thirdConnection); err != nil {
		t.Fatalf("storeConnection(third) error = %v", err)
	}

	message, err := NewMessage(MessageTypeTransaction, []byte("broadcast"))
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	peerIDs := []string{firstPeerID, firstPeerID, "", localPeerID, secondPeerID, thirdPeerID}
	if err := host.Broadcast(context.Background(), peerIDs, message); err != nil {
		t.Fatalf("Broadcast() error = %v", err)
	}

	if firstConnection.waitWrite(t).ToPeerID != firstPeerID {
		t.Fatal("first peer did not receive broadcast")
	}
	if secondConnection.waitWrite(t).ToPeerID != secondPeerID {
		t.Fatal("second peer did not receive broadcast")
	}
	assertNoConnectionWrite(t, thirdConnection)
	if host.Metrics().BroadcastPeersDropped != 4 {
		t.Fatalf("BroadcastPeersDropped = %d, want 4", host.Metrics().BroadcastPeersDropped)
	}
}

func TestHostDialPeerBacksOffAfterAllAttemptsFail(t *testing.T) {
	localPeerID := testPeerID(48)
	remotePeerID := testPeerID(49)
	host, err := NewHost(HostConfig{
		PeerID:        localPeerID,
		AllowInsecure: true,
		DialTimeout:   50 * time.Millisecond,
		PeerProtection: PeerProtectionConfig{
			DialBackoffBase: time.Second,
			DialBackoffMax:  time.Second,
		},
	})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	address := testAddress(t, utils.ProtocolTCP, freeTCPPort(t), remotePeerID)
	peer, err := NewPeer(remotePeerID, []utils.MultiAddress{address})
	if err != nil {
		t.Fatalf("NewPeer() error = %v", err)
	}
	if err := host.AddPeer(peer); err != nil {
		t.Fatalf("AddPeer() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := host.DialPeer(ctx, remotePeerID); err == nil {
		t.Fatal("DialPeer(first) error = nil, want dial failure")
	}
	if _, err := host.DialPeer(ctx, remotePeerID); !errors.Is(err, ErrPeerBackoff) {
		t.Fatalf("DialPeer(second) error = %v, want ErrPeerBackoff", err)
	}
	if host.Metrics().DialBackoffs == 0 {
		t.Fatal("DialBackoffs = 0, want backoff metric")
	}
}

func TestHostConnectionWriterForReusesStoredConnection(t *testing.T) {
	localPeerID := testPeerID(57)
	remotePeerID := testPeerID(58)
	host, err := NewHost(HostConfig{
		PeerID:        localPeerID,
		AllowInsecure: true,
	})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	connection := newScriptedConnection(utils.ProtocolTCP, remotePeerID, nil)
	if err := host.storeConnection(remotePeerID, connection); err != nil {
		t.Fatalf("storeConnection() error = %v", err)
	}
	storedConnection, ok := host.Connection(remotePeerID)
	if !ok {
		t.Fatal("Connection() ok = false, want true")
	}
	resolvedConnection := host.connectionWriterFor(connection)
	if resolvedConnection != storedConnection {
		t.Fatal("connectionWriterFor() did not reuse stored writer")
	}
}

func TestQueuedConnectionRejectsWriteAfterClose(t *testing.T) {
	base := newBlockingWriteConnection(testPeerID(59), testPeerID(60))
	connection := newQueuedConnection(base, queuedConnectionConfig{
		queueSize:    1,
		writeTimeout: time.Second,
		priority:     fixedMessagePriority(MessagePriorityNormal),
	})
	if err := connection.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	message := testNetworkMessage(t, ProtocolReceiveTransactionV1, base.localPeerID, base.remotePeerID)
	if err := connection.WriteMessage(context.Background(), message); !errors.Is(err, ErrConnectionClosed) {
		t.Fatalf("WriteMessage() error = %v, want ErrConnectionClosed", err)
	}
}

func TestHostAsyncWriteMetricsAfterFlush(t *testing.T) {
	localPeerID := testPeerID(65)
	remotePeerID := testPeerID(66)
	host, err := NewHost(HostConfig{
		PeerID:        localPeerID,
		AllowInsecure: true,
	})
	if err != nil {
		t.Fatalf("NewHost() error = %v", err)
	}
	defer host.Close()

	base := newBlockingWriteConnection(localPeerID, remotePeerID)
	if err := host.storeConnection(remotePeerID, base); err != nil {
		t.Fatalf("storeConnection() error = %v", err)
	}
	connection, ok := host.Connection(remotePeerID)
	if !ok {
		t.Fatal("Connection() ok = false, want true")
	}
	message := testNetworkMessage(t, ProtocolReceiveTransactionV1, localPeerID, remotePeerID)
	if err := host.writeConnectionMessage(context.Background(), connection, remotePeerID, message); err != nil {
		t.Fatalf("writeConnectionMessage() error = %v", err)
	}
	base.waitFirstWriteStarted(t)
	if host.Metrics().MessagesWritten != 0 {
		t.Fatalf("MessagesWritten = %d, want 0 before flush", host.Metrics().MessagesWritten)
	}

	base.releaseFirstWrite()
	base.waitWrite(t)
	waitForMessagesWritten(t, host, 1)
}

func TestQueuedConnectionAppliesBackpressure(t *testing.T) {
	base := newBlockingWriteConnection(testPeerID(67), testPeerID(68))
	connection := newQueuedConnection(base, queuedConnectionConfig{
		queueSize:    1,
		writeTimeout: time.Second,
		priority:     fixedMessagePriority(MessagePriorityNormal),
	})
	defer connection.Close()

	first := testNetworkMessage(t, ProtocolReceiveTransactionV1, base.localPeerID, base.remotePeerID)
	second := testNetworkMessage(t, ProtocolReceiveTransactionV1, base.localPeerID, base.remotePeerID)
	third := testNetworkMessage(t, ProtocolReceiveTransactionV1, base.localPeerID, base.remotePeerID)
	if err := connection.WriteMessage(context.Background(), first); err != nil {
		t.Fatalf("WriteMessage(first) error = %v", err)
	}
	base.waitFirstWriteStarted(t)
	if err := connection.WriteMessage(context.Background(), second); err != nil {
		t.Fatalf("WriteMessage(second) error = %v", err)
	}
	if err := connection.WriteMessage(context.Background(), third); !errors.Is(err, ErrWriteQueueFull) {
		t.Fatalf("WriteMessage(third) error = %v, want ErrWriteQueueFull", err)
	}
	base.releaseFirstWrite()
}

func TestQueuedConnectionPrioritizesHighPriorityWrites(t *testing.T) {
	base := newBlockingWriteConnection(testPeerID(69), testPeerID(70))
	connection := newQueuedConnection(base, queuedConnectionConfig{
		queueSize:    8,
		writeTimeout: time.Second,
		priority: func(message Message) MessagePriority {
			if message.Type == ProtocolHotStuffVoteV1 {
				return MessagePriorityHigh
			}
			return MessagePriorityLow
		},
	})
	defer connection.Close()

	firstLow := testNetworkMessage(t, ProtocolReceiveBlockV1, base.localPeerID, base.remotePeerID)
	queuedLow := testNetworkMessage(t, ProtocolReceiveBlockV1, base.localPeerID, base.remotePeerID)
	queuedHigh := testNetworkMessage(t, ProtocolHotStuffVoteV1, base.localPeerID, base.remotePeerID)
	if err := connection.WriteMessage(context.Background(), firstLow); err != nil {
		t.Fatalf("WriteMessage(first low) error = %v", err)
	}
	base.waitFirstWriteStarted(t)
	if err := connection.WriteMessage(context.Background(), queuedLow); err != nil {
		t.Fatalf("WriteMessage(queued low) error = %v", err)
	}
	if err := connection.WriteMessage(context.Background(), queuedHigh); err != nil {
		t.Fatalf("WriteMessage(queued high) error = %v", err)
	}
	base.releaseFirstWrite()

	if message := base.waitWrite(t); message.ID != firstLow.ID {
		t.Fatalf("first write = %s, want first low %s", message.ID, firstLow.ID)
	}
	if message := base.waitWrite(t); message.ID != queuedHigh.ID {
		t.Fatalf("second write = %s, want high %s", message.ID, queuedHigh.ID)
	}
	if message := base.waitWrite(t); message.ID != queuedLow.ID {
		t.Fatalf("third write = %s, want low %s", message.ID, queuedLow.ID)
	}
}

type scriptedConnection struct {
	protocol     utils.MultiAddressProtocol
	remotePeerID string
	reads        chan Message
	writes       chan Message
	closed       bool
}

type responsiveConnection struct {
	*scriptedConnection
	localPeerID string
}

type blockingWriteConnection struct {
	*scriptedConnection
	mutex        sync.Mutex
	localPeerID  string
	firstStarted chan struct{}
	releaseFirst chan struct{}
	blockedFirst bool
}

func newBlockingWriteConnection(localPeerID string, remotePeerID string) *blockingWriteConnection {
	return &blockingWriteConnection{
		scriptedConnection: newScriptedConnection(utils.ProtocolTCP, remotePeerID, nil),
		localPeerID:        localPeerID,
		firstStarted:       make(chan struct{}),
		releaseFirst:       make(chan struct{}),
	}
}

func (connection *blockingWriteConnection) WriteMessage(ctx context.Context, message Message) error {
	if connection.shouldBlockFirstWrite() {
		close(connection.firstStarted)
		select {
		case <-connection.releaseFirst:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return connection.scriptedConnection.WriteMessage(ctx, message)
}

func (connection *blockingWriteConnection) shouldBlockFirstWrite() bool {
	connection.mutex.Lock()
	defer connection.mutex.Unlock()
	if connection.blockedFirst {
		return false
	}
	connection.blockedFirst = true
	return true
}

func (connection *blockingWriteConnection) waitFirstWriteStarted(t *testing.T) {
	t.Helper()
	select {
	case <-connection.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first write")
	}
}

func (connection *blockingWriteConnection) releaseFirstWrite() {
	select {
	case <-connection.releaseFirst:
	default:
		close(connection.releaseFirst)
	}
}

func fixedMessagePriority(priority MessagePriority) func(Message) MessagePriority {
	return func(Message) MessagePriority {
		return priority
	}
}

func newResponsiveConnection(remotePeerID string, localPeerID string) *responsiveConnection {
	return &responsiveConnection{
		scriptedConnection: newScriptedConnection(utils.ProtocolTCP, remotePeerID, nil),
		localPeerID:        localPeerID,
	}
}

func (connection *responsiveConnection) WriteMessage(ctx context.Context, message Message) error {
	if message.Type != ProtocolFindNodeRequestV1 {
		return connection.scriptedConnection.WriteMessage(ctx, message)
	}

	ping, err := NewRequestMessage(connection.remotePeerID, MessageTypePing, nil)
	if err != nil {
		return err
	}
	ping.ToPeerID = connection.localPeerID
	connection.reads <- ping

	responsePayload, err := NewKADFindNodeResponse(connection.localPeerID, nil)
	if err != nil {
		return err
	}
	payload, err := responsePayload.MarshalBinary()
	if err != nil {
		return err
	}
	response, err := NewResponseMessage(connection.remotePeerID, ProtocolFindNodeResponseV1, message.ID, payload)
	if err != nil {
		return err
	}
	response.ToPeerID = connection.localPeerID
	connection.reads <- response
	return nil
}

func newScriptedConnection(protocol utils.MultiAddressProtocol, remotePeerID string, reads []Message) *scriptedConnection {
	connection := &scriptedConnection{
		protocol:     protocol,
		remotePeerID: remotePeerID,
		reads:        make(chan Message, len(reads)),
		writes:       make(chan Message, 8),
	}
	for _, message := range reads {
		connection.reads <- message
	}
	return connection
}

func (connection *scriptedConnection) ID() string {
	return "scripted-" + connection.remotePeerID
}

func (connection *scriptedConnection) Protocol() utils.MultiAddressProtocol {
	return connection.protocol
}

func (connection *scriptedConnection) RemotePeerID() string {
	return connection.remotePeerID
}

func (connection *scriptedConnection) LocalAddress() string {
	return "127.0.0.1:1000"
}

func (connection *scriptedConnection) RemoteAddress() string {
	return "127.0.0.1:1001"
}

func (connection *scriptedConnection) ReadMessage(ctx context.Context) (Message, error) {
	select {
	case message := <-connection.reads:
		return message, nil
	case <-ctx.Done():
		return Message{}, ctx.Err()
	case <-time.After(time.Second):
		return Message{}, errors.New("scripted read timeout")
	}
}

func (connection *scriptedConnection) WriteMessage(ctx context.Context, message Message) error {
	select {
	case connection.writes <- message:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (connection *scriptedConnection) Close() error {
	connection.closed = true
	return nil
}

func (connection *scriptedConnection) waitWrite(t *testing.T) Message {
	t.Helper()
	select {
	case message := <-connection.writes:
		return message
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for written message")
		return Message{}
	}
}

func assertNoRequestResponse(t *testing.T, waiter <-chan Message) {
	t.Helper()
	select {
	case response := <-waiter:
		t.Fatalf("unexpected response fulfilled: %+v", response)
	default:
	}
}

func assertNoConnectionWrite(t *testing.T, connection *scriptedConnection) {
	t.Helper()
	select {
	case message := <-connection.writes:
		t.Fatalf("unexpected written message: %+v", message)
	default:
	}
}

func testNetworkMessage(t *testing.T, messageType MessageType, fromPeerID string, toPeerID string) Message {
	t.Helper()
	message, err := NewMessage(messageType, nil)
	if err != nil {
		t.Fatalf("NewMessage(%d) error = %v", messageType, err)
	}
	message.FromPeerID = fromPeerID
	message.ToPeerID = toPeerID
	if err := message.Validate(DefaultMaxMessageSize); err != nil {
		t.Fatalf("message.Validate(%d) error = %v", messageType, err)
	}
	return message
}

func waitForProtocolJobsQueued(t *testing.T, host *Host, expected uint64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if host.Metrics().ProtocolJobsQueued >= expected {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("ProtocolJobsQueued = %d, want at least %d", host.Metrics().ProtocolJobsQueued, expected)
}

func waitForMessagesWritten(t *testing.T, host *Host, expected uint64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if host.Metrics().MessagesWritten >= expected {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("MessagesWritten = %d, want at least %d", host.Metrics().MessagesWritten, expected)
}

func waitProtocolOrder(t *testing.T, order <-chan string) string {
	t.Helper()
	select {
	case value := <-order:
		return value
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for protocol order")
		return ""
	}
}
