/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package heartbeat

import "sync"

// BMaaSPresence is the last successfully loaded fulfillment-service BMaaS
// snapshot. It lets heartbeat generation distinguish a deleted resource from
// a projection that is not present in the current source snapshot.
type BMaaSPresence struct {
	mu     sync.RWMutex
	ids    map[string]struct{}
	loaded bool
}

func NewBMaaSPresence() *BMaaSPresence {
	return &BMaaSPresence{}
}

// Replace publishes a complete, successfully loaded source snapshot. Callers
// should not call Replace when loading the source fails, so the previous
// snapshot remains authoritative until a new one is available.
func (p *BMaaSPresence) Replace(ids []string) {
	current := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		current[id] = struct{}{}
	}
	p.mu.Lock()
	p.ids = current
	p.loaded = true
	p.mu.Unlock()
}

func (p *BMaaSPresence) Contains(id string) bool {
	if p == nil {
		return true
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if !p.loaded {
		return false
	}
	_, ok := p.ids[id]
	return ok
}
