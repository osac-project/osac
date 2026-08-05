/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package auth

import (
	"context"
	"errors"
	"log/slog"
	"sync"
)

// MemoryTokenStoreBuilder contains the logic needed to create a token store that saves tokens in memory only.
type MemoryTokenStoreBuilder struct {
	logger *slog.Logger
}

type memoryTokenStore struct {
	logger *slog.Logger
	lock   sync.RWMutex
	token  *Token
}

// NewMemoryTokenStore creates a builder that can then be used to configure and create a memory token store.
func NewMemoryTokenStore() *MemoryTokenStoreBuilder {
	return &MemoryTokenStoreBuilder{}
}

// SetLogger sets the logger. This is mandatory.
func (b *MemoryTokenStoreBuilder) SetLogger(value *slog.Logger) *MemoryTokenStoreBuilder {
	b.logger = value
	return b
}

// Build uses the data stored in the builder to build a new memory token store.
func (b *MemoryTokenStoreBuilder) Build() (result TokenStore, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}

	// Create and populate the object:
	result = &memoryTokenStore{
		logger: b.logger,
	}
	return
}

// Load loads the tokens from memory. Returns a clone of the stored token to prevent side effects.
func (s *memoryTokenStore) Load(ctx context.Context) (result *Token, err error) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	if s.token == nil {
		return
	}
	result = &Token{
		Access:  s.token.Access,
		Refresh: s.token.Refresh,
		Expiry:  s.token.Expiry,
	}
	return
}

// Save saves the tokens to memory. Clones the provided token to prevent side effects.
func (s *memoryTokenStore) Save(ctx context.Context, token *Token) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	if token == nil {
		return errors.New("token cannot be nil")
	}
	s.token = &Token{
		Access:  token.Access,
		Refresh: token.Refresh,
		Expiry:  token.Expiry,
	}
	return nil
}
