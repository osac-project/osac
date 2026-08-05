/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package logging

import (
	"context"
	"errors"
	"log/slog"
	"strings"
)

// WriterBuilder contains the data and logic needed to create a writer that writes messages to a logger. Don't create
// instances of this type directly, use the NewWriterBuilder function instead.
type WriterBuilder struct {
	logger  *slog.Logger
	level   slog.Level
	context context.Context
	secrets []string
}

// Writer is an implementation of an io.Writer that writes messages to a slog.Logger. Don't create instances of this
// type directly, use the NewWriterBuilder function instead.
type Writer struct {
	logger  *slog.Logger
	level   slog.Level
	context context.Context
	secrets []string
}

// NewWriter creates a builder that can then be used to configure and create a new logging writer.
func NewWriter() *WriterBuilder {
	return &WriterBuilder{
		level: slog.LevelInfo,
	}
}

// SetLogger sets the logger. This is mandatory.
func (b *WriterBuilder) SetLogger(value *slog.Logger) *WriterBuilder {
	b.logger = value
	return b
}

// SetLevel sets the level. This is optional, if not set the 'info' level will be used.
func (b *WriterBuilder) SetLevel(value slog.Level) *WriterBuilder {
	b.level = value
	return b
}

// SetContext sets the context that will be passed to the logger from the Write method. This is optional, if not set no
// context will be passed to the logger.
func (b *WriterBuilder) SetContext(value context.Context) *WriterBuilder {
	b.context = value
	return b
}

// AddSecret adds a secret that will be redacted from the log messages.
func (b *WriterBuilder) AddSecret(value string) *WriterBuilder {
	b.secrets = append(b.secrets, value)
	return b
}

// AddSecrets adds a set of secrets that will be redacted from the log messages.
func (b *WriterBuilder) AddSecrets(values ...string) *WriterBuilder {
	b.secrets = append(b.secrets, values...)
	return b
}

// Build uses the configuration stored in the builder to create a new logging writer.
func (b *WriterBuilder) Build() (result *Writer, err error) {
	// Check arguments:
	if b.logger == nil {
		err = errors.New("logger is mandatory")
		return
	}

	// Remove empty secrets to prevent log amplification attacks:
	secrets := make([]string, 0, len(b.secrets))
	for _, secret := range b.secrets {
		if strings.TrimSpace(secret) != "" {
			secrets = append(secrets, secret)
		}
	}

	// Create and populate the object:
	result = &Writer{
		logger:  b.logger,
		level:   b.level,
		context: b.context,
		secrets: secrets,
	}
	return
}

// Write implements the io.Writer interface.
func (w *Writer) Write(p []byte) (n int, err error) {
	// Redact the message if needed:
	text := string(p)
	if len(w.secrets) > 0 {
		for _, secret := range w.secrets {
			text = strings.ReplaceAll(text, secret, redactMark)
		}
	}

	// Write the message to the logger:
	n = len(p)
	w.logger.LogAttrs(
		w.context,
		w.level,
		"Write",
		slog.Int("size", n),
		slog.String("data", text),
	)
	return
}
