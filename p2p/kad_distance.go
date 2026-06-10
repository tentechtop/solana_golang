package p2p

import (
	"bytes"
	"crypto/rand"
	"fmt"

	"solana_golang/utils"
)

const kadIdentifierBits = peerIDByteSize * 8

// KADDistance 保存异或距离 + 固定 32 字节便于排序和桶定位。
type KADDistance [peerIDByteSize]byte

// KADPeerIDBytes 解码节点 ID + 统一 KAD 距离计算的输入格式。
func KADPeerIDBytes(peerID string) ([peerIDByteSize]byte, error) {
	decoded, err := utils.Base58Decode(peerID)
	if err != nil {
		return [peerIDByteSize]byte{}, fmt.Errorf("p2p: decode kad peer id: %w", err)
	}
	if len(decoded) != peerIDByteSize {
		return [peerIDByteSize]byte{}, fmt.Errorf("p2p: kad peer id requires %d bytes", peerIDByteSize)
	}
	var out [peerIDByteSize]byte
	copy(out[:], decoded)
	return out, nil
}

// KADPeerIDFromBytes 编码节点 ID + 保持路由表和 Peer.ID 格式一致。
func KADPeerIDFromBytes(peerID [peerIDByteSize]byte) string {
	return utils.Base58Encode(peerID[:])
}

// KADCalculateDistance 计算异或距离 + Kademlia 使用该距离排序邻近节点。
func KADCalculateDistance(first [peerIDByteSize]byte, second [peerIDByteSize]byte) KADDistance {
	var distance KADDistance
	for index := 0; index < peerIDByteSize; index++ {
		distance[index] = first[index] ^ second[index]
	}
	return distance
}

// KADCompareDistance 比较距离大小 + 返回值小于零表示 first 更近。
func KADCompareDistance(first KADDistance, second KADDistance) int {
	return bytes.Compare(first[:], second[:])
}

// KADBucketIndex 计算桶索引 + 0 表示最近桶，255 表示最远桶。
func KADBucketIndex(localID [peerIDByteSize]byte, targetID [peerIDByteSize]byte) (int, bool) {
	distance := KADCalculateDistance(localID, targetID)
	for byteIndex, value := range distance {
		if value == 0 {
			continue
		}
		for bitIndex := 7; bitIndex >= 0; bitIndex-- {
			if value&(1<<bitIndex) == 0 {
				continue
			}
			highestBit := byteIndex*8 + (7 - bitIndex)
			return kadIdentifierBits - 1 - highestBit, true
		}
	}
	return 0, false
}

// KADRandomTargetForBucket 生成目标桶随机 ID + 用于后续桶刷新查找。
func KADRandomTargetForBucket(localID [peerIDByteSize]byte, bucketIndex int) ([peerIDByteSize]byte, error) {
	if bucketIndex < 0 || bucketIndex >= kadIdentifierBits {
		return [peerIDByteSize]byte{}, fmt.Errorf("p2p: invalid kad bucket index %d", bucketIndex)
	}

	var distance [peerIDByteSize]byte
	if _, err := rand.Read(distance[:]); err != nil {
		return [peerIDByteSize]byte{}, fmt.Errorf("p2p: random kad target: %w", err)
	}

	targetBit := kadIdentifierBits - 1 - bucketIndex
	targetByte := targetBit / 8
	targetBitInByte := 7 - (targetBit % 8)
	for index := 0; index < targetByte; index++ {
		distance[index] = 0
	}
	distance[targetByte] &= byte(0xff >> (targetBit % 8))
	distance[targetByte] |= 1 << targetBitInByte

	var target [peerIDByteSize]byte
	for index := 0; index < peerIDByteSize; index++ {
		target[index] = localID[index] ^ distance[index]
	}
	return target, nil
}
