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
