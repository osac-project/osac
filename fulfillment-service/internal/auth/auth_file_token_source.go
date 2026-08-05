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
	"os"
	"strings"
	"time"
)

// FileTokenSourceBuilder contains the logic needed to create a token source that reads tokens from a file.
type FileTokenSourceBuilder struct {
	logger *slog.Logger
	file   string
}

type fileTokenSource struct {
	logger    *slog.Logger
	file      string
	timestamp time.Time
	token     *Token
}

// NewFileTokenSource creates a builder that can then be used to configure and create a token source that reads
// tokens from a file. The token source will monitor the file's modification time and only reload the token
// when the file has changed.
func NewFileTokenSource() *FileTokenSourceBuilder {
	return &FileTokenSourceBuilder{}
}

// SetLogger sets the logger. This is mandatory.
func (b *FileTokenSourceBuilder) SetLogger(value *slog.Logger) *FileTokenSourceBuilder {
	b.logger = value
	return b
}

// SetFile sets the path to the file containing the access token. This is mandatory.
func (b *FileTokenSourceBuilder) SetFile(value string) *FileTokenSourceBuilder {
	b.file = value
	return b
}

// Build uses the data stored in the builder to build a new file token source.
func (b *FileTokenSourceBuilder) Build() (result TokenSource, err error) {
	// Check parameters:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}
	if b.file == "" {
		err = errors.New("file is mandatory")
		return
	}

	// Check that the file exists:
	_, err = os.Stat(b.file)
	if err != nil {
		return
	}

	// Add the file name to the logger:
	logger := b.logger.With(
		slog.String("file", b.file),
	)

	// Create and populate the object:
	result = &fileTokenSource{
		logger: logger,
		file:   b.file,
	}
	return
}

// Token is the implementation of the TokenSource interface.
func (s *fileTokenSource) Token(ctx context.Context) (result *Token, err error) {
	// Get modification time:
	info, err := os.Stat(s.file)
	if err != nil {
		s.logger.ErrorContext(
			ctx,
			"Failed to get file info for token file",
			slog.Any("error", err),
		)
		return
	}
	timestamp := info.ModTime()

	// Check if we need to reload the token:
	reload := s.token == nil || timestamp.After(s.timestamp)
	if reload {
		s.logger.DebugContext(
			ctx,
			"Token file has changed, reloading token",
			slog.Time("timestamp", timestamp),
		)
		var data []byte
		data, err = os.ReadFile(s.file)
		if err != nil {
			s.logger.ErrorContext(
				ctx,
				"Failed to read token file",
				slog.Any("error", err),
			)
			return nil, err
		}
		token := strings.TrimSpace(string(data))
		if token == "" {
			err = errors.New("token file is empty")
			return
		}
		s.token = &Token{
			Access: token,
		}
		s.timestamp = timestamp
	}

	// Return the token:
	result = s.token
	return
}

// Invalidate clears the cached token, forcing the file to be re-read on the next Token() call.
func (s *fileTokenSource) Invalidate(ctx context.Context) error {
	s.token = nil
	s.timestamp = time.Time{}
	return nil
}
