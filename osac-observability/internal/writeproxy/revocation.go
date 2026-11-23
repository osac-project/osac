// Copyright (c) 2026 Red Hat Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
// in compliance with the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0

package writeproxy

import (
	"sort"
	"strings"
	"sync"
)

const (
	// RevocationConfigMapName names the ConfigMap shared by every proxy replica and by the
	// operator that revokes a deleted cluster's certificate.
	RevocationConfigMapName = "osac-metrics-write-revocation"
	// RevocationKey is the ConfigMap key that holds the newline-separated serial numbers.
	RevocationKey = "revoked"
)

// RevocationStore is the denylist shared through a ConfigMap. Until a replica has observed the
// ConfigMap once since process start it rejects every write: a revoked certificate must never
// be admitted because a watch has not caught up yet.
type RevocationStore struct {
	mu      sync.RWMutex
	synced  bool
	serials map[string]struct{}
}

// NewRevocationStore returns an unsynced store.
func NewRevocationStore() *RevocationStore {
	return &RevocationStore{serials: map[string]struct{}{}}
}

// Synced reports whether the store has a complete, current denylist.
func (s *RevocationStore) Synced() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.synced
}

// Revoked reports whether the serial must be refused. An unsynced store refuses everything.
func (s *RevocationStore) Revoked(serial string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.synced {
		return true
	}
	_, ok := s.serials[normalizeSerial(serial)]
	return ok
}

// Apply replaces the denylist with the contents of a successfully read ConfigMap.
func (s *RevocationStore) Apply(data map[string]string) {
	serials := map[string]struct{}{}
	for _, line := range strings.Split(data[RevocationKey], "\n") {
		serial := normalizeSerial(line)
		if serial != "" {
			serials[serial] = struct{}{}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.serials = serials
	s.synced = true
}

// Fail marks the denylist unknown after an unreadable watch, closing the gate.
func (s *RevocationStore) Fail() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.synced = false
}

func (s *RevocationStore) revokedSerials() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.serials))
	for serial := range s.serials {
		out = append(out, serial)
	}
	sort.Strings(out)
	return out
}

func normalizeSerial(serial string) string {
	return strings.ToLower(strings.TrimSpace(serial))
}
