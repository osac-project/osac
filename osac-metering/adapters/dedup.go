/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package adapters

import (
	"context"
	"sync"
	"time"
)

type dedupCache struct {
	mu      sync.Mutex
	entries map[string]time.Time // CloudEvent ID → insertion time
	ttl     time.Duration
}

func newDedupCache(ttl time.Duration) *dedupCache {
	return &dedupCache{
		entries: make(map[string]time.Time),
		ttl:     ttl,
	}
}

// contains returns true if the given ID is in the cache and not expired.
func (c *dedupCache) contains(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	ts, ok := c.entries[id]
	if !ok {
		return false
	}
	if time.Since(ts) > c.ttl {
		delete(c.entries, id)
		return false
	}
	return true
}

// add inserts an ID into the cache with the current timestamp.
func (c *dedupCache) add(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[id] = time.Now()
}

// evictExpired removes entries older than TTL.
func (c *dedupCache) evictExpired() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for id, ts := range c.entries {
		if now.Sub(ts) > c.ttl {
			delete(c.entries, id)
		}
	}
}

// startEviction runs evictExpired every 30 seconds until ctx is cancelled.
func (c *dedupCache) startEviction(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.evictExpired()
		}
	}
}

// len returns the number of entries in the cache.
func (c *dedupCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}
