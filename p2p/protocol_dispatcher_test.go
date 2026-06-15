package p2p

import "testing"

func TestProtocolSchedulerUsesOneWorkerPerPartition(t *testing.T) {
	config := normalizeProtocolSchedulerConfig(ProtocolSchedulerConfig{
		WorkerCount:    8,
		PartitionCount: 2,
	})
	if config.WorkerCount != 2 || config.PartitionCount != 2 {
		t.Fatalf("config = workers %d partitions %d, want 2/2", config.WorkerCount, config.PartitionCount)
	}

	config = normalizeProtocolSchedulerConfig(ProtocolSchedulerConfig{WorkerCount: 8})
	if config.WorkerCount != 8 || config.PartitionCount != 8 {
		t.Fatalf("default partitions = workers %d partitions %d, want 8/8", config.WorkerCount, config.PartitionCount)
	}

	config = normalizeProtocolSchedulerConfig(ProtocolSchedulerConfig{PartitionCount: maxProtocolWorkerCount + 1})
	if config.WorkerCount != maxProtocolWorkerCount || config.PartitionCount != maxProtocolWorkerCount {
		t.Fatalf("capped config = workers %d partitions %d, want %d/%d",
			config.WorkerCount,
			config.PartitionCount,
			maxProtocolWorkerCount,
			maxProtocolWorkerCount,
		)
	}
}

func TestProtocolSchedulerCapsQueueSizes(t *testing.T) {
	config := normalizeProtocolSchedulerConfig(ProtocolSchedulerConfig{
		WorkerCount:     4,
		HighQueueSize:   maxProtocolQueueSize + 1,
		NormalQueueSize: maxProtocolQueueSize + 2,
		LowQueueSize:    maxProtocolQueueSize + 3,
	})
	if config.HighQueueSize != maxProtocolQueueSize ||
		config.NormalQueueSize != maxProtocolQueueSize ||
		config.LowQueueSize != maxProtocolQueueSize {
		t.Fatalf("queue sizes = %d/%d/%d, want cap %d",
			config.HighQueueSize,
			config.NormalQueueSize,
			config.LowQueueSize,
			maxProtocolQueueSize,
		)
	}

	config = normalizeProtocolSchedulerConfig(ProtocolSchedulerConfig{WorkerCount: 16})
	if config.HighQueueSize != 4096 || config.NormalQueueSize != 4096 || config.LowQueueSize != 4096 {
		t.Fatalf("auto queue sizes = %d/%d/%d, want 4096",
			config.HighQueueSize,
			config.NormalQueueSize,
			config.LowQueueSize,
		)
	}
}

func TestProtocolPartitionKeepsNonParallelProtocolOrdered(t *testing.T) {
	peerID := testPeerID(41)
	first, err := NewRequestMessage(peerID, ProtocolReceiveTransactionV1, []byte("first"))
	if err != nil {
		t.Fatalf("NewRequestMessage(first) error = %v", err)
	}
	second, err := NewRequestMessage(peerID, ProtocolReceiveTransactionV1, []byte("second"))
	if err != nil {
		t.Fatalf("NewRequestMessage(second) error = %v", err)
	}
	spec := ProtocolSpec{
		ID:       ProtocolReceiveTransactionV1,
		Name:     "/p2p/transaction/receive/1.0.0",
		Priority: MessagePriorityNormal,
	}

	firstIndex := protocolPartitionIndexForSpec(first, 32, spec)
	secondIndex := protocolPartitionIndexForSpec(second, 32, spec)
	if firstIndex != secondIndex {
		t.Fatalf("partition indexes = %d/%d, want same for non-parallel protocol", firstIndex, secondIndex)
	}
}

func TestProtocolPartitionShardsParallelProtocolByRequestID(t *testing.T) {
	peerID := testPeerID(42)
	spec := ProtocolSpec{
		ID:          ProtocolID(9100),
		Name:        "/p2p/test/parallel/1.0.0",
		HasResponse: true,
		Priority:    MessagePriorityNormal,
		Concurrency: ProtocolConcurrencyStateless,
	}
	seen := make(map[int]struct{})
	for index := 0; index < 128; index++ {
		message, err := NewRequestMessage(peerID, spec.ID, []byte("payload"))
		if err != nil {
			t.Fatalf("NewRequestMessage(%d) error = %v", index, err)
		}
		partitionIndex := protocolPartitionIndexForSpec(message, 32, spec)
		seen[partitionIndex] = struct{}{}
	}
	if len(seen) < 2 {
		t.Fatalf("parallel protocol used %d partition, want sharding across partitions", len(seen))
	}
}

func TestProtocolPartitionShardsStatefulProtocolByStateKey(t *testing.T) {
	peerID := testPeerID(43)
	spec := ProtocolSpec{
		ID:          ProtocolID(9200),
		Name:        "/p2p/test/state-key/1.0.0",
		HasResponse: true,
		Priority:    MessagePriorityNormal,
		Concurrency: ProtocolConcurrencyStateKey,
		PartitionKey: func(message Message) string {
			if len(message.Payload) == 0 {
				return ""
			}
			return string(message.Payload)
		},
	}
	first, err := NewRequestMessage(peerID, spec.ID, []byte("account-a"))
	if err != nil {
		t.Fatalf("NewRequestMessage(first) error = %v", err)
	}
	second, err := NewRequestMessage(peerID, spec.ID, []byte("account-a"))
	if err != nil {
		t.Fatalf("NewRequestMessage(second) error = %v", err)
	}
	if protocolPartitionIndexForSpec(first, 32, spec) != protocolPartitionIndexForSpec(second, 32, spec) {
		t.Fatal("same state key used different partitions, want ordered handling")
	}

	seen := make(map[int]struct{})
	for index := 0; index < 64; index++ {
		message, err := NewRequestMessage(peerID, spec.ID, []byte{byte(index)})
		if err != nil {
			t.Fatalf("NewRequestMessage(%d) error = %v", index, err)
		}
		seen[protocolPartitionIndexForSpec(message, 32, spec)] = struct{}{}
	}
	if len(seen) < 2 {
		t.Fatalf("state-key protocol used %d partition, want different keys in parallel", len(seen))
	}
}
