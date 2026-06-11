package structure

import "fmt"

const (
	MaxRecentBlockhashAgeSlots = uint64(150)
)

// RecentBlockhashEntry 描述 recent blockhash 记录 + 保存有效高度和费用参数。
type RecentBlockhashEntry struct {
	Blockhash            Blockhash
	Slot                 uint64
	LastValidBlockHeight uint64
	FeeCalculator        FeeCalculator
	TimestampUnix        int64
}

// BlockhashQueue 描述 recent blockhash 队列 + 为交易年龄检查提供有界窗口。
type BlockhashQueue struct {
	Entries     []RecentBlockhashEntry
	MaxAgeSlots uint64
}

// StatusCacheEntry 描述交易状态缓存项 + 用于交易去重和执行结果查询。
type StatusCacheEntry struct {
	TransactionID Signature
	MessageHash   Hash
	Slot          uint64
	Status        TransactionStatus
	Err           string
}

// NewBlockhashQueue 创建 recent blockhash 队列 + 统一默认年龄窗口。
func NewBlockhashQueue(maxAgeSlots uint64) BlockhashQueue {
	if maxAgeSlots == 0 {
		maxAgeSlots = MaxRecentBlockhashAgeSlots
	}
	return BlockhashQueue{MaxAgeSlots: maxAgeSlots}
}

// Validate 校验 recent blockhash 记录 + 确保哈希和费用参数可用。
func (entry RecentBlockhashEntry) Validate() error {
	if entry.Blockhash == (Blockhash{}) {
		return ErrEmptyBlockhash
	}
	if err := entry.FeeCalculator.Validate(); err != nil {
		return fmt.Errorf("structure: validate blockhash fee calculator: %w", err)
	}
	return nil
}

// IsRecent 判断 blockhash 是否仍在窗口内 + 使用 slot 差值做交易年龄检查。
func (entry RecentBlockhashEntry) IsRecent(currentSlot uint64, maxAgeSlots uint64) bool {
	if currentSlot < entry.Slot {
		return false
	}
	return currentSlot-entry.Slot <= maxAgeSlots
}

// Validate 校验 recent blockhash 队列 + 防止空窗口和非法记录进入交易检查。
func (queue BlockhashQueue) Validate() error {
	if queue.MaxAgeSlots == 0 {
		return fmt.Errorf("%w: max age slots cannot be zero", ErrInvalidBlockhashQueue)
	}
	if len(queue.Entries) > int(queue.MaxAgeSlots)+1 {
		return fmt.Errorf("%w: entries %d exceed window %d", ErrInvalidBlockhashQueue, len(queue.Entries), queue.MaxAgeSlots+1)
	}
	seen := make(map[Blockhash]struct{}, len(queue.Entries))
	for entryIndex, entry := range queue.Entries {
		if err := entry.Validate(); err != nil {
			return fmt.Errorf("structure: blockhash entry %d: %w", entryIndex, err)
		}
		if _, exists := seen[entry.Blockhash]; exists {
			return fmt.Errorf("%w: duplicate blockhash entry %d", ErrInvalidBlockhashQueue, entryIndex)
		}
		seen[entry.Blockhash] = struct{}{}
	}
	return nil
}

// Add 追加 recent blockhash + 自动裁剪超过窗口的旧记录。
func (queue *BlockhashQueue) Add(entry RecentBlockhashEntry) error {
	if queue == nil {
		return fmt.Errorf("%w: queue is nil", ErrInvalidBlockhashQueue)
	}
	if queue.MaxAgeSlots == 0 {
		queue.MaxAgeSlots = MaxRecentBlockhashAgeSlots
	}
	if err := entry.Validate(); err != nil {
		return err
	}
	queue.Entries = append(queue.Entries, entry)
	queue.trim(entry.Slot)
	return queue.Validate()
}

// Find 查找 blockhash 记录 + 返回副本避免调用方修改队列。
func (queue BlockhashQueue) Find(blockhash Blockhash) (RecentBlockhashEntry, bool) {
	for _, entry := range queue.Entries {
		if entry.Blockhash == blockhash {
			return entry, true
		}
	}
	return RecentBlockhashEntry{}, false
}

// IsRecent 判断 blockhash 是否可用 + 同时检查存在性和年龄窗口。
func (queue BlockhashQueue) IsRecent(blockhash Blockhash, currentSlot uint64) bool {
	entry, exists := queue.Find(blockhash)
	if !exists {
		return false
	}
	return entry.IsRecent(currentSlot, queue.MaxAgeSlots)
}

// Clone 深拷贝 blockhash 队列 + 避免交易检查修改共享窗口。
func (queue BlockhashQueue) Clone() BlockhashQueue {
	entries := make([]RecentBlockhashEntry, len(queue.Entries))
	copy(entries, queue.Entries)
	return BlockhashQueue{
		Entries:     entries,
		MaxAgeSlots: queue.MaxAgeSlots,
	}
}

// Validate 校验状态缓存项 + 确保至少有一个稳定交易标识。
func (entry StatusCacheEntry) Validate() error {
	if entry.TransactionID == (Signature{}) && entry.MessageHash == (Hash{}) {
		return fmt.Errorf("%w: missing transaction id and message hash", ErrInvalidStatusCache)
	}
	if entry.Status == TransactionStatusPending {
		return fmt.Errorf("%w: pending status should not enter status cache", ErrInvalidStatusCache)
	}
	return nil
}

func (queue *BlockhashQueue) trim(currentSlot uint64) {
	cutIndex := 0
	for cutIndex < len(queue.Entries) {
		if queue.Entries[cutIndex].IsRecent(currentSlot, queue.MaxAgeSlots) {
			break
		}
		cutIndex++
	}
	if cutIndex == 0 {
		return
	}
	queue.Entries = queue.Entries[cutIndex:]
}
