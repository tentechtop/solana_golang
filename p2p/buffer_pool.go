package p2p

import "sync"

const maxPooledMessageBufferSize = DefaultMaxMessageSize

type byteBufferPool struct {
	size int
	pool sync.Pool
}

var messageFrameHeaderPool = sync.Pool{
	New: func() any {
		return make([]byte, messageFrameHeaderSize)
	},
}

var messagePayloadPools = []*byteBufferPool{
	newByteBufferPool(1024),
	newByteBufferPool(4 * 1024),
	newByteBufferPool(16 * 1024),
	newByteBufferPool(64 * 1024),
	newByteBufferPool(256 * 1024),
	newByteBufferPool(1024 * 1024),
	newByteBufferPool(4 * 1024 * 1024),
}

func newByteBufferPool(size int) *byteBufferPool {
	bufferSize := size
	return &byteBufferPool{
		size: bufferSize,
		pool: sync.Pool{
			New: func() any {
				return make([]byte, bufferSize)
			},
		},
	}
}

func acquireMessageFrameHeader() []byte {
	buffer, ok := messageFrameHeaderPool.Get().([]byte)
	if !ok || cap(buffer) < messageFrameHeaderSize {
		return make([]byte, messageFrameHeaderSize)
	}
	return buffer[:messageFrameHeaderSize]
}

func releaseMessageFrameHeader(buffer []byte) {
	if cap(buffer) < messageFrameHeaderSize {
		return
	}
	messageFrameHeaderPool.Put(buffer[:messageFrameHeaderSize])
}

func acquireMessagePayloadBuffer(length int) []byte {
	if length <= 0 {
		return nil
	}
	pool := messagePayloadPool(length)
	if pool == nil {
		return make([]byte, length)
	}
	buffer, ok := pool.pool.Get().([]byte)
	if !ok || cap(buffer) < length {
		return make([]byte, length)
	}
	return buffer[:length]
}

func releaseMessagePayloadBuffer(buffer []byte) {
	if cap(buffer) == 0 || cap(buffer) > maxPooledMessageBufferSize {
		return
	}
	for _, pool := range messagePayloadPools {
		if cap(buffer) == pool.size {
			pool.pool.Put(buffer[:pool.size])
			return
		}
	}
}

func messagePayloadPool(length int) *byteBufferPool {
	if length <= 0 || length > maxPooledMessageBufferSize {
		return nil
	}
	for _, pool := range messagePayloadPools {
		if length <= pool.size {
			return pool
		}
	}
	return nil
}
