package cache

import (
	"context"
	"sync"
	"time"
)

const (
	// DefaultSweepInterval Cache default sweep interval
	DefaultSweepInterval = time.Second * 3
)

// memoryCache memory cache, support time expired
type memoryCache struct {
	data          interface{}
	cacheDuration time.Duration
	startTime     time.Time
}

// NewMemoryCache new memory cache instance
func NewMemoryCache(data interface{}, cacheDuration time.Duration) *memoryCache {
	mc := &memoryCache{data: data, cacheDuration: cacheDuration, startTime: time.Now()}
	return mc
}

// IsExpired whether the cache data expires
func (m *memoryCache) IsExpired() bool {
	if m.cacheDuration <= 0 {
		return false
	}
	return time.Now().After(m.startTime.Add(m.cacheDuration))
}

// GetData get cache data
func (m *memoryCache) GetData() interface{} {
	return m.data
}

// MemoryCacheStore memory cache store
type MemoryCacheStore[K comparable] struct {
	store sync.Map
}

// NewMemoryCacheStore memory cache store
func NewMemoryCacheStore[K comparable](ctx context.Context) *MemoryCacheStore[K] {
	mcs := &MemoryCacheStore[K]{
		store: sync.Map{},
	}
	go mcs.run(ctx)
	return mcs
}

// run start a goroutine to clear expired cache data
func (m *MemoryCacheStore[K]) run(ctx context.Context) {
	ticker := time.NewTicker(DefaultSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.store.Range(func(key, value interface{}) bool {
				if value.(*memoryCache).IsExpired() {
					m.store.CompareAndDelete(key, value)
				}
				return true
			})
		}
	}
}

// Get cache data from store, if cache data is expired, return nil
func (m *MemoryCacheStore[K]) Get(key K) (value interface{}, found bool) {
	mc, ok := m.store.Load(key)
	if ok && !mc.(*memoryCache).IsExpired() {
		return mc.(*memoryCache).GetData(), true
	}
	return nil, false
}

// Put cache data, if cacheDuration>0, store will clear data after timeout.
func (m *MemoryCacheStore[K]) Put(key K, value interface{}, cacheDuration time.Duration) {
	mc := NewMemoryCache(value, cacheDuration)
	m.store.Store(key, mc)
}

// Delete cache data from store
func (m *MemoryCacheStore[K]) Delete(key K) {
	m.store.Delete(key)
}
