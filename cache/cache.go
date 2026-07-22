package cache

import "time"

// Cache interface defines the methods for a cache implementation.
type Cache[K comparable] interface {
	// Get retrieves a value from the cache by key.
	Get(key K) (value interface{}, found bool)

	// Put adds a value to the cache with the specified key and expiration time.
	Put(key K, value interface{}, expiration time.Duration)

	// Delete removes a value from the cache by key.
	Delete(key K)
}
