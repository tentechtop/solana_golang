package p2p

import (
	"bytes"
	"context"
	"testing"
	"time"

	"solana_golang/utils"
)

func TestQUICTransportSendMessage(t *testing.T) {
	peerID := testPeerID(7)
	address := testAddress(t, utils.ProtocolQUIC, freeUDPPort(t), peerID)
	serverTransport := newInsecureQUICTransportForTest(t)
	clientTransport := newInsecureQUICTransportForTest(t)
	defer serverTransport.Close()
	defer clientTransport.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	received := make(chan Message, 1)
	handlerErrors := make(chan error, 1)
	listenErrors := make(chan error, 1)
	go func() {
		listenErrors <- serverTransport.Listen(ctx, address, func(ctx context.Context, connection Connection) {
			defer connection.Close()
			readContext, readCancel := context.WithTimeout(context.Background(), time.Second)
			defer readCancel()
			message, err := connection.ReadMessage(readContext)
			if err != nil {
				handlerErrors <- err
				return
			}
			received <- message
		})
	}()

	connection := dialQUICEventually(t, clientTransport, address)
	defer connection.Close()

	dialContext, dialCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer dialCancel()
	message, err := NewMessage(ProtocolPingV1, []byte("quic"))
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	if err := connection.WriteMessage(dialContext, message); err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}

	select {
	case err := <-handlerErrors:
		t.Fatalf("handler error = %v", err)
	case decoded := <-received:
		if !bytes.Equal(decoded.Payload, []byte("quic")) {
			t.Fatalf("Payload = %q, want quic", decoded.Payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for message")
	}

	cancel()
	if err := <-listenErrors; err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
}

func TestQUICTransportUsesSeparatePriorityStreams(t *testing.T) {
	peerID := testPeerID(8)
	address := testAddress(t, utils.ProtocolQUIC, freeUDPPort(t), peerID)
	serverTransport := newInsecureQUICTransportForTest(t)
	clientTransport := newInsecureQUICTransportForTest(t)
	defer serverTransport.Close()
	defer clientTransport.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	received := make(chan ProtocolID, 2)
	listenErrors := make(chan error, 1)
	go func() {
		listenErrors <- serverTransport.Listen(ctx, address, func(ctx context.Context, connection Connection) {
			defer connection.Close()
			for index := 0; index < 2; index++ {
				readContext, readCancel := context.WithTimeout(context.Background(), 2*time.Second)
				message, err := connection.ReadMessage(readContext)
				readCancel()
				if err != nil {
					return
				}
				received <- message.Type
			}
		})
	}()

	connection := dialQUICEventually(t, clientTransport, address)
	defer connection.Close()

	lowMessage, err := NewMessage(ProtocolBlockV1, []byte("low"))
	if err != nil {
		t.Fatalf("NewMessage(low) error = %v", err)
	}
	highMessage, err := NewMessage(ProtocolHotStuffVoteV1, []byte("high"))
	if err != nil {
		t.Fatalf("NewMessage(high) error = %v", err)
	}
	writeContext, writeCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer writeCancel()
	if err := connection.WriteMessage(writeContext, lowMessage); err != nil {
		t.Fatalf("WriteMessage(low) error = %v", err)
	}
	if err := connection.WriteMessage(writeContext, highMessage); err != nil {
		t.Fatalf("WriteMessage(high) error = %v", err)
	}

	quicConnection, ok := connection.(*QUICConnection)
	if !ok {
		t.Fatalf("connection type = %T, want *QUICConnection", connection)
	}
	quicConnection.streamMutex.Lock()
	highPool := quicConnection.writeStreams[MessagePriorityHigh]
	lowPool := quicConnection.writeStreams[MessagePriorityLow]
	streamCount := len(quicConnection.writeStreams)
	quicConnection.streamMutex.Unlock()
	hasHighStream := highPool != nil && len(highPool.streams) > 0
	hasLowStream := lowPool != nil && len(lowPool.streams) > 0
	if !hasHighStream || !hasLowStream || streamCount < 2 {
		t.Fatalf("write stream priorities high=%v low=%v count=%d, want separated high and low", hasHighStream, hasLowStream, streamCount)
	}

	for index := 0; index < 2; index++ {
		select {
		case <-received:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for priority stream message")
		}
	}
	cancel()
	if err := <-listenErrors; err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
}

func TestQUICTransportGrowsSamePriorityStreamPool(t *testing.T) {
	peerID := testPeerID(18)
	address := testAddress(t, utils.ProtocolQUIC, freeUDPPort(t), peerID)
	serverTransport := newInsecureQUICTransportForTest(t)
	clientTransport := newQUICTransportForTest(t, QUICTransportConfig{StreamPoolSize: 4})
	defer serverTransport.Close()
	defer clientTransport.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	received := make(chan struct{}, 4)
	listenErrors := make(chan error, 1)
	go func() {
		listenErrors <- serverTransport.Listen(ctx, address, func(ctx context.Context, connection Connection) {
			defer connection.Close()
			for index := 0; index < 4; index++ {
				readContext, readCancel := context.WithTimeout(context.Background(), 2*time.Second)
				_, err := connection.ReadMessage(readContext)
				readCancel()
				if err != nil {
					return
				}
				received <- struct{}{}
			}
		})
	}()

	connection := dialQUICEventually(t, clientTransport, address)
	defer connection.Close()

	writeContext, writeCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer writeCancel()
	for index := 0; index < 4; index++ {
		message, err := NewMessage(ProtocolQueryBlockByHashV1, []byte("readonly"))
		if err != nil {
			t.Fatalf("NewMessage(%d) error = %v", index, err)
		}
		if err := connection.WriteMessage(writeContext, message); err != nil {
			t.Fatalf("WriteMessage(%d) error = %v", index, err)
		}
	}

	quicConnection, ok := connection.(*QUICConnection)
	if !ok {
		t.Fatalf("connection type = %T, want *QUICConnection", connection)
	}
	quicConnection.streamMutex.Lock()
	lowPool := quicConnection.writeStreams[MessagePriorityLow]
	lowStreamCount := 0
	if lowPool != nil {
		lowStreamCount = len(lowPool.streams)
	}
	quicConnection.streamMutex.Unlock()
	if lowStreamCount < 2 {
		t.Fatalf("readonly stream pool size = %d, want at least 2", lowStreamCount)
	}

	for index := 0; index < 4; index++ {
		select {
		case <-received:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for same-priority stream message")
		}
	}
	cancel()
	if err := <-listenErrors; err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
}

func TestQUICTransportIgnoresCanceledPeerStream(t *testing.T) {
	peerID := testPeerID(20)
	address := testAddress(t, utils.ProtocolQUIC, freeUDPPort(t), peerID)
	serverTransport := newInsecureQUICTransportForTest(t)
	clientTransport := newInsecureQUICTransportForTest(t)
	defer serverTransport.Close()
	defer clientTransport.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	received := make(chan Message, 1)
	handlerErrors := make(chan error, 1)
	listenErrors := make(chan error, 1)
	go func() {
		listenErrors <- serverTransport.Listen(ctx, address, func(ctx context.Context, connection Connection) {
			defer connection.Close()
			readContext, readCancel := context.WithTimeout(ctx, 2*time.Second)
			defer readCancel()
			message, err := connection.ReadMessage(readContext)
			if err != nil {
				handlerErrors <- err
				return
			}
			received <- message
		})
	}()

	connection := dialQUICEventually(t, clientTransport, address)
	defer connection.Close()

	quicConnection, ok := connection.(*QUICConnection)
	if !ok {
		t.Fatalf("connection type = %T, want *QUICConnection", connection)
	}
	streamContext, streamCancel := context.WithTimeout(context.Background(), 2*time.Second)
	extraStream, err := quicConnection.connection.OpenStreamSync(streamContext)
	streamCancel()
	if err != nil {
		t.Fatalf("OpenStreamSync() error = %v", err)
	}
	closeUnusedQUICStream(extraStream)
	time.Sleep(100 * time.Millisecond)

	message, err := NewMessage(ProtocolPingV1, []byte("after-stream-cancel"))
	if err != nil {
		t.Fatalf("NewMessage() error = %v", err)
	}
	writeContext, writeCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer writeCancel()
	if err := connection.WriteMessage(writeContext, message); err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}

	select {
	case err := <-handlerErrors:
		t.Fatalf("handler error = %v", err)
	case decoded := <-received:
		if !bytes.Equal(decoded.Payload, message.Payload) {
			t.Fatalf("Payload = %q, want %q", decoded.Payload, message.Payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for message")
	}

	cancel()
	if err := <-listenErrors; err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
}

func TestQUICTransportKeepsOrderedProtocolOnSinglePriorityStream(t *testing.T) {
	peerID := testPeerID(19)
	address := testAddress(t, utils.ProtocolQUIC, freeUDPPort(t), peerID)
	serverTransport := newInsecureQUICTransportForTest(t)
	clientTransport := newQUICTransportForTest(t, QUICTransportConfig{StreamPoolSize: 4})
	defer serverTransport.Close()
	defer clientTransport.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	received := make(chan struct{}, 4)
	listenErrors := make(chan error, 1)
	go func() {
		listenErrors <- serverTransport.Listen(ctx, address, func(ctx context.Context, connection Connection) {
			defer connection.Close()
			for index := 0; index < 4; index++ {
				readContext, readCancel := context.WithTimeout(context.Background(), 2*time.Second)
				_, err := connection.ReadMessage(readContext)
				readCancel()
				if err != nil {
					return
				}
				received <- struct{}{}
			}
		})
	}()

	connection := dialQUICEventually(t, clientTransport, address)
	defer connection.Close()

	writeContext, writeCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer writeCancel()
	for index := 0; index < 4; index++ {
		message, err := NewMessage(ProtocolReceiveTransactionV1, []byte("ordered"))
		if err != nil {
			t.Fatalf("NewMessage(%d) error = %v", index, err)
		}
		if err := connection.WriteMessage(writeContext, message); err != nil {
			t.Fatalf("WriteMessage(%d) error = %v", index, err)
		}
	}

	quicConnection, ok := connection.(*QUICConnection)
	if !ok {
		t.Fatalf("connection type = %T, want *QUICConnection", connection)
	}
	quicConnection.streamMutex.Lock()
	normalPool := quicConnection.writeStreams[MessagePriorityNormal]
	normalStreamCount := 0
	if normalPool != nil {
		normalStreamCount = len(normalPool.streams)
	}
	quicConnection.streamMutex.Unlock()
	if normalStreamCount != 1 {
		t.Fatalf("ordered stream pool size = %d, want 1", normalStreamCount)
	}

	for index := 0; index < 4; index++ {
		select {
		case <-received:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for ordered stream message")
		}
	}
	cancel()
	if err := <-listenErrors; err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
}

func TestQUICTransportUsesTemporaryTLSAndSkipsCertificateChain(t *testing.T) {
	transport, err := NewQUICTransport()
	if err != nil {
		t.Fatalf("NewQUICTransport() error = %v", err)
	}
	defer transport.Close()

	tlsConfig, err := transport.clientTLSConfig()
	if err != nil {
		t.Fatalf("clientTLSConfig() error = %v", err)
	}
	if !tlsConfig.InsecureSkipVerify {
		t.Fatal("clientTLSConfig().InsecureSkipVerify = false, want true")
	}
}

func TestNormalizeQUICConfigUsesLargePayloadWindows(t *testing.T) {
	config := normalizeQUICConfig(nil, QUICTransportConfig{}, DefaultMaxMessageSize)
	if config.InitialStreamReceiveWindow != defaultQUICInitialStreamReceiveWindow {
		t.Fatalf("InitialStreamReceiveWindow = %d, want %d",
			config.InitialStreamReceiveWindow,
			defaultQUICInitialStreamReceiveWindow,
		)
	}
	if config.InitialConnectionReceiveWindow != defaultQUICInitialConnectionReceiveWindow {
		t.Fatalf("InitialConnectionReceiveWindow = %d, want %d",
			config.InitialConnectionReceiveWindow,
			defaultQUICInitialConnectionReceiveWindow,
		)
	}

	custom := normalizeQUICConfig(nil, QUICTransportConfig{
		InitialStreamReceiveWindow:     2 * 1024 * 1024,
		InitialConnectionReceiveWindow: 6 * 1024 * 1024,
	}, DefaultMaxMessageSize)
	if custom.InitialStreamReceiveWindow != 2*1024*1024 {
		t.Fatalf("custom InitialStreamReceiveWindow = %d", custom.InitialStreamReceiveWindow)
	}
	if custom.InitialConnectionReceiveWindow != 6*1024*1024 {
		t.Fatalf("custom InitialConnectionReceiveWindow = %d", custom.InitialConnectionReceiveWindow)
	}
}

func TestQUICReadBufferSizeCapsMemoryByMessageLimit(t *testing.T) {
	if got := quicReadBufferSize(1 * 1024 * 1024); got != 16 {
		t.Fatalf("quicReadBufferSize(1MB) = %d, want 16", got)
	}
	if got := quicReadBufferSize(MaxConfigurableMessageSize); got != 1 {
		t.Fatalf("quicReadBufferSize(max) = %d, want 1", got)
	}
	if got := quicReadBufferSize(1); got != maxQUICReadBufferSize {
		t.Fatalf("quicReadBufferSize(default) = %d, want cap %d", got, maxQUICReadBufferSize)
	}
}

func newInsecureQUICTransportForTest(t *testing.T) *QUICTransport {
	t.Helper()
	return newQUICTransportForTest(t, QUICTransportConfig{})
}

func newQUICTransportForTest(t *testing.T, config QUICTransportConfig) *QUICTransport {
	t.Helper()
	transport, err := NewQUICTransportWithConfig(config)
	if err != nil {
		t.Fatalf("NewQUICTransportWithConfig() error = %v", err)
	}
	return transport
}

func dialQUICEventually(t *testing.T, transport *QUICTransport, address utils.MultiAddress) Connection {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		connection, err := transport.Dial(ctx, address)
		cancel()
		if err == nil {
			return connection
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("Dial() error = %v", lastErr)
	return nil
}
