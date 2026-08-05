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
)

// StaticTokenSourceBuilder contains the logic needed to create a token source that always returns the same token.
type StaticTokenSourceBuilder struct {
	logger *slog.Logger
	token  *Token
}

type staticTokenSource struct {
	logger *slog.Logger
	token  *Token
}

// NewStaticTokenSource creates a builder that can then be used to configure and create a token source that always
// returns the same static token.
func NewStaticTokenSource() *StaticTokenSourceBuilder {
	return &StaticTokenSourceBuilder{}
}

// SetLogger sets the logger. This is mandatory.
func (b *StaticTokenSourceBuilder) SetLogger(value *slog.Logger) *StaticTokenSourceBuilder {
	b.logger = value
	return b
}

// SetToken sets the token that will be returned by this token source. This is mandatory.
func (b *StaticTokenSourceBuilder) SetToken(value *Token) *StaticTokenSourceBuilder {
	b.token = value
	return b
}

// Build uses the data stored in the builder to build a new static token source.
func (b *StaticTokenSourceBuilder) Build() (result TokenSource, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.token == nil {
		err = errors.New("token is mandatory")
		return
	}

	// Create and populate the object:
	result = &staticTokenSource{
		logger: b.logger,
		token:  b.token,
	}
	return
}

// Token is the implementation of the TokenSource interface.
func (s *staticTokenSource) Token(ctx context.Context) (result *Token, err error) {
	result = s.token
	return
}

// Invalidate is a no-op for static tokens since they never change.
func (s *staticTokenSource) Invalidate(ctx context.Context) error {
	return nil
}
