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
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ScriptTokenSourceBuilder contains the logic needed to create a token source that executes an external script to
// generate the token.
type ScriptTokenSourceBuilder struct {
	logger *slog.Logger
	script string
	store  TokenStore
}

type scriptTokenSource struct {
	logger      *slog.Logger
	script      string
	store       TokenStore
	tokenParser *jwt.Parser
}

// NewScriptTokenSource creates a builder that can then be used to configure and create a token source that executes a
// script to generate the token.
func NewScriptTokenSource() *ScriptTokenSourceBuilder {
	return &ScriptTokenSourceBuilder{}
}

// SetLogger sets the logger. This is mandatory.
func (b *ScriptTokenSourceBuilder) SetLogger(value *slog.Logger) *ScriptTokenSourceBuilder {
	b.logger = value
	return b
}

// SetScript sets script that will be used to generate new tokens. This is mandatory.
func (b *ScriptTokenSourceBuilder) SetScript(value string) *ScriptTokenSourceBuilder {
	b.script = value
	return b
}

// SetStore sets the token store that will be used to load and save tokens. This is mandatory.
func (b *ScriptTokenSourceBuilder) SetStore(value TokenStore) *ScriptTokenSourceBuilder {
	b.store = value
	return b
}

// Build uses the data stored in the builder to build a new script token source.
func (b *ScriptTokenSourceBuilder) Build() (result TokenSource, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.script == "" {
		err = errors.New("token generation script is mandatory")
		return
	}
	if b.store == nil {
		err = errors.New("token store is mandatory")
		return
	}

	// Create the token parser:
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{
			"RS256",
		}),
		jwt.WithIssuedAt(),
	)

	// Create and populate the object:
	result = &scriptTokenSource{
		logger:      b.logger,
		script:      b.script,
		store:       b.store,
		tokenParser: parser,
	}
	return
}

// Token is the implementation of the TokenSource interface.
func (s *scriptTokenSource) Token(ctx context.Context) (result *Token, err error) {
	// Try to load an existing token first:
	existingToken, err := s.store.Load(ctx)
	if err != nil {
		return
	}

	// If we have a token and it's still fresh, use it:
	if existingToken != nil && existingToken.Access != "" {
		// If the token has expired then we need to generate a new one. Note that if a token isn't a JWT we have no way
		// to check the expiry date, so we don't save it for future use.
		parsedToken, parseErr := s.parseToken(existingToken.Access)
		if parseErr == nil && !parsedToken.Expiry.Before(time.Now()) {
			result = parsedToken
			return
		}
	}

	// Generate a new token:
	rawToken, err := s.generateToken()
	if err != nil {
		return
	}

	// Try to parse the token to get expiry information:
	parsedToken, parseErr := s.parseToken(rawToken)
	if parseErr != nil {
		// If we can't parse it as a JWT, just return it as-is without saving:
		result = &Token{
			Access: rawToken,
		}
		return
	}

	// Save the parsed token to storage:
	err = s.store.Save(ctx, parsedToken)
	if err != nil {
		return
	}

	result = parsedToken
	return
}

func (s *scriptTokenSource) generateToken() (result string, err error) {
	shell, ok := os.LookupEnv("SHELL")
	if !ok {
		shell = "/usr/bin/sh"
	}
	out := &bytes.Buffer{}
	cmd := exec.Cmd{
		Path: shell,
		Args: []string{
			shell,
			"-c",
			s.script,
		},
		Stdout: out,
	}
	err = cmd.Run()
	if err != nil {
		err = fmt.Errorf("failed to execute token generation script '%s': %w", s.script, err)
		return
	}
	result = strings.TrimSpace(out.String())
	return
}

func (s *scriptTokenSource) parseToken(tokenText string) (result *Token, err error) {
	tokenClaims := jwt.MapClaims{}
	_, _, err = s.tokenParser.ParseUnverified(tokenText, tokenClaims)
	if err != nil {
		return
	}
	tokenEpirationTime, err := tokenClaims.GetExpirationTime()
	if err != nil {
		return
	}
	result = &Token{
		Access: tokenText,
		Expiry: tokenEpirationTime.Time,
	}
	return
}

// Invalidate clears the cached token, forcing a new token to be generated on the next Token() call.
func (s *scriptTokenSource) Invalidate(ctx context.Context) error {
	// Save an empty token to invalidate the cache
	return s.store.Save(ctx, &Token{})
}
