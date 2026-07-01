/*
Copyright 2026 The KubeVela Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package upgrade

import (
	"context"
	"testing"
)

func entry(requiresUpgrade bool, upgraded string) compatEntry {
	return compatEntry{requiresUpgrade: requiresUpgrade, upgraded: upgraded}
}

func TestLRUCacheBasic(t *testing.T) {
	c := newLRUCache(3)

	c.put("a", entry(false, "1"))
	c.put("b", entry(false, "2"))
	c.put("c", entry(false, "3"))

	if v, ok := c.get("a"); !ok || v.upgraded != "1" {
		t.Errorf("expected a.upgraded=1, got %q %v", v.upgraded, ok)
	}

	// "a" was promoted; "b" is now LRU — adding "d" should evict "b"
	c.put("d", entry(false, "4"))
	if _, ok := c.get("b"); ok {
		t.Error("expected b to be evicted")
	}
	if v, ok := c.get("d"); !ok || v.upgraded != "4" {
		t.Errorf("expected d.upgraded=4, got %q %v", v.upgraded, ok)
	}
}

func TestLRUCacheUpdate(t *testing.T) {
	c := newLRUCache(2)
	c.put("a", entry(false, "1"))
	c.put("a", entry(true, "2"))
	if v, ok := c.get("a"); !ok || v.upgraded != "2" || !v.requiresUpgrade {
		t.Errorf("expected updated value a.upgraded=2 requiresUpgrade=true, got %q %v %v", v.upgraded, v.requiresUpgrade, ok)
	}
	if c.len() != 1 {
		t.Errorf("expected len 1, got %d", c.len())
	}
}

func TestLRUCacheCapacity(t *testing.T) {
	capacity := 10
	c := newLRUCache(capacity)
	for i := range capacity * 2 {
		c.put(string(rune('a'+i)), entry(false, ""))
	}
	if c.len() != capacity {
		t.Errorf("expected len %d, got %d", capacity, c.len())
	}
}

func TestLRUCacheDisabled(t *testing.T) {
	c := newLRUCache(0)
	c.put("a", entry(false, "1"))
	if _, ok := c.get("a"); ok {
		t.Error("expected disabled cache to return nothing")
	}
}

func TestEnsureCueVersionCompatibilityCacheHit(t *testing.T) {
	compatCache.Store(newLRUCache(512))

	input := `
list1: [1, 2, 3]
list2: [4, 5, 6]
combined: list1 + list2
`
	result1, _ := EnsureCueVersionCompatibility(input, "test-def", testKind, testArea)

	const sentinel = "CACHE_HIT_SENTINEL"
	key := templateHash(input)
	compatCache.Load().put(key, compatEntry{requiresUpgrade: true, upgraded: sentinel})

	result2, _ := EnsureCueVersionCompatibility(input, "test-def", testKind, testArea)
	if result2 != sentinel {
		t.Errorf("second call did not use cache: expected sentinel %q, got %q (first result was %q)", sentinel, result2, result1)
	}
	if compatCache.Load().len() != 1 {
		t.Errorf("expected 1 cache entry, got %d", compatCache.Load().len())
	}
}

func TestEnsureCueVersionCompatibilityAlreadyCompatible(t *testing.T) {
	compatCache.Store(newLRUCache(512))

	// Already in canonical CUE format — normalisation should be a no-op.
	input := `import "list"

list1: [1, 2, 3]
list2: [4, 5, 6]
combined: list.Concat([list1, list2])`
	result1, _ := EnsureCueVersionCompatibility(input, "test-def", testKind, testArea)
	if result1 != input {
		t.Errorf("already-compatible template should be returned unchanged on first call, got %q", result1)
	}

	const sentinel = "CACHE_HIT_SENTINEL"
	key := templateHash(input)
	compatCache.Load().put(key, compatEntry{requiresUpgrade: true, upgraded: sentinel})

	result2, _ := EnsureCueVersionCompatibility(input, "test-def", testKind, testArea)
	if result2 != sentinel {
		t.Errorf("second call did not use cache: expected sentinel %q, got %q", sentinel, result2)
	}
}

func TestEnsureCueVersionCompatibilityCacheDeterminism(t *testing.T) {
	// Cache-miss and cache-hit must return identical output for the same input.
	compatCache.Store(newLRUCache(512))

	input := `
list1: [1, 2, 3]
list2: [4, 5, 6]
combined: list1 + list2
`
	result1, _ := EnsureCueVersionCompatibility(input, "test-def", testKind, testArea)
	// Second call is a cache hit.
	result2, _ := EnsureCueVersionCompatibility(input, "test-def", testKind, testArea)
	if result1 != result2 {
		t.Errorf("cache-miss and cache-hit returned different results:\nmiss: %q\nhit:  %q", result1, result2)
	}
}

func TestInitCompatibilityCacheReinitialises(t *testing.T) {
	ctx := context.Background()

	// Populate the old cache.
	compatCache.Store(newLRUCache(10))
	compatCache.Load().put("old-key", compatEntry{upgraded: "old"})

	InitCompatibilityCache(ctx, 5)

	// New cache should be empty and have the new capacity.
	c := compatCache.Load()
	if c.len() != 0 {
		t.Errorf("expected empty cache after reinit, got len %d", c.len())
	}
	if c.capacity != 5 {
		t.Errorf("expected capacity 5, got %d", c.capacity)
	}
}

func TestInitCompatibilityCacheSizeZeroDisablesCache(t *testing.T) {
	ctx := context.Background()
	InitCompatibilityCache(ctx, 0)

	c := compatCache.Load()
	c.put("k", compatEntry{upgraded: "v"})
	if _, ok := c.get("k"); ok {
		t.Error("expected zero-size cache to store nothing")
	}
}

func TestEvictStaleRemovesExpiredEntries(t *testing.T) {
	c := newLRUCache(10)

	// Insert an entry then manually backdate its lastAccess.
	c.put("stale", compatEntry{upgraded: "old"})
	el := c.items["stale"]
	el.Value.(*lruEntry).lastAccess = el.Value.(*lruEntry).lastAccess.Add(-2 * CacheEntryTTL)

	c.put("fresh", compatEntry{upgraded: "new"})

	var evicted []string
	origFn := OnCacheEviction
	defer func() { OnCacheEviction = origFn }()
	OnCacheEviction = func(reason string) { evicted = append(evicted, reason) }

	c.evictStale()

	if _, ok := c.get("stale"); ok {
		t.Error("expected stale entry to be evicted")
	}
	if _, ok := c.get("fresh"); !ok {
		t.Error("expected fresh entry to survive eviction")
	}
	if len(evicted) != 1 || evicted[0] != "ttl" {
		t.Errorf("expected one ttl eviction event, got %v", evicted)
	}
}

func TestCapacityEvictionCallsCallback(t *testing.T) {
	c := newLRUCache(2)
	var reasons []string
	origFn := OnCacheEviction
	defer func() { OnCacheEviction = origFn }()
	OnCacheEviction = func(reason string) { reasons = append(reasons, reason) }

	c.put("a", entry(false, "1"))
	c.put("b", entry(false, "2"))
	c.put("c", entry(false, "3")) // evicts "a"

	if len(reasons) != 1 || reasons[0] != "capacity" {
		t.Errorf("expected one capacity eviction, got %v", reasons)
	}
}
