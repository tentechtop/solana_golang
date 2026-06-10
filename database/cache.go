package database

import "time"

type cacheEntry struct {
	value     []byte
	expiresAt time.Time
	accessed  time.Time
}

type tableCache struct {
	ttl     time.Duration
	maxSize int
	items   map[string]cacheEntry
}

func (p *databaseService) ensureCacheStateLocked() {
	if p.caches == nil {
		p.caches = make(map[Table]*tableCache)
	}
}
func (p *databaseService) cacheEnabled(table Table) bool {
	p.cacheMu.RLock()
	defer p.cacheMu.RUnlock()
	cache := p.caches[table]
	return cache != nil && cache.maxSize > 0
}
func (p *databaseService) cacheGet(table Table, key []byte) ([]byte, bool) {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	cache := p.caches[table]
	if cache == nil || cache.maxSize <= 0 {
		return nil, false
	}
	entry, ok := cache.items[string(key)]
	if !ok {
		return nil, false
	}
	now := time.Now()
	if !entry.expiresAt.IsZero() && now.After(entry.expiresAt) {
		delete(cache.items, string(key))
		return nil, false
	}
	entry.accessed = now
	cache.items[string(key)] = entry
	return cloneBytes(entry.value), true
}
func (p *databaseService) cacheSet(table Table, key []byte, value []byte) {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	p.ensureCacheStateLocked()
	cache := p.caches[table]
	if cache == nil || cache.maxSize <= 0 {
		return
	}
	now := time.Now()
	entry := cacheEntry{value: cloneBytes(value), accessed: now}
	if cache.ttl > 0 {
		entry.expiresAt = now.Add(cache.ttl)
	}
	cache.items[string(key)] = entry
	enforceCacheLimit(cache)
}
func (p *databaseService) cacheDelete(table Table, key []byte) {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	cache := p.caches[table]
	if cache == nil {
		return
	}
	delete(cache.items, string(key))
}
func (p *databaseService) clearCacheOnly(table Table) {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	if p.caches == nil {
		return
	}
	if table == TableAll {
		for _, cache := range p.caches {
			cache.items = make(map[string]cacheEntry)
		}
		return
	}
	cache := p.caches[table]
	if cache != nil {
		cache.items = make(map[string]cacheEntry)
	}
}
func (p *databaseService) applyCacheOperations(operations []DBOperation) {
	for _, op := range operations {
		switch op.Type {
		case OperationInsert, OperationUpdate:
			p.cacheSet(op.Table, op.Key, op.Value)
		case OperationDelete:
			p.cacheDelete(op.Table, op.Key)
		}
	}
}
func enforceCacheLimit(cache *tableCache) {
	for cache.maxSize > 0 && len(cache.items) > cache.maxSize {
		var oldestKey string
		var oldestTime time.Time
		for key, entry := range cache.items {
			if oldestKey == "" || entry.accessed.Before(oldestTime) {
				oldestKey = key
				oldestTime = entry.accessed
			}
		}
		delete(cache.items, oldestKey)
	}
}
