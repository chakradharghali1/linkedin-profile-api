package linkedin

import (
	"sync"
	"time"
)

/*
Cache stores fetched profiles for a short period.

LinkedIn allows very few automated requests before blocking a session, so the
cheapest request is the one never sent. Repeated lookups of the same profile
(a demo, a page refresh, a retry) are served from memory.

It is intentionally in-process: there is no external dependency to run, which
keeps deployment to a single container.
*/
type Cache struct {
	mu      sync.RWMutex
	ttl     time.Duration
	entries map[string]cacheEntry
}

type cacheEntry struct {
	profile   *Profile
	expiresAt time.Time
}

func NewCache(ttl time.Duration) *Cache {
	return &Cache{
		ttl:     ttl,
		entries: make(map[string]cacheEntry),
	}
}

// Get returns a cached profile if one is present and still fresh.
func (c *Cache) Get(key string) (*Profile, bool) {
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()

	if !ok || time.Now().After(entry.expiresAt) {
		return nil, false
	}

	return entry.profile, true
}

func (c *Cache) Set(key string, profile *Profile) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Drop expired entries opportunistically; the map is small and this
	// avoids needing a background sweeper.
	now := time.Now()

	for existing, entry := range c.entries {
		if now.After(entry.expiresAt) {
			delete(c.entries, existing)
		}
	}

	c.entries[key] = cacheEntry{
		profile:   profile,
		expiresAt: now.Add(c.ttl),
	}
}
