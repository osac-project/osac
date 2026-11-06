/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package testing

import (
	"context"
	"log/slog"
	"sync"
)

// LogRecord is a record kept by LogRecorder, with its attributes by key.
type LogRecord struct {
	Level   slog.Level
	Message string
	Attrs   map[string]any
}

// LogRecorder is a log handler that keeps every record, at any level, so that tests can check what was logged. It is
// safe for concurrent use. Attributes added with WithAttrs and groups are not kept.
type LogRecorder struct {
	mu      sync.Mutex
	records []LogRecord
}

func (r *LogRecorder) Enabled(context.Context, slog.Level) bool {
	return true
}

func (r *LogRecorder) Handle(_ context.Context, record slog.Record) error {
	attrs := map[string]any{}
	record.Attrs(func(attr slog.Attr) bool {
		attrs[attr.Key] = attr.Value.Any()
		return true
	})
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, LogRecord{
		Level:   record.Level,
		Message: record.Message,
		Attrs:   attrs,
	})
	return nil
}

func (r *LogRecorder) WithAttrs([]slog.Attr) slog.Handler {
	return r
}

func (r *LogRecorder) WithGroup(string) slog.Handler {
	return r
}

// WithMessage returns the records that have the given message.
func (r *LogRecorder) WithMessage(message string) []LogRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []LogRecord
	for _, record := range r.records {
		if record.Message == message {
			result = append(result, record)
		}
	}
	return result
}

// Levels returns the level of each record that has the given message.
func (r *LogRecorder) Levels(message string) []slog.Level {
	var result []slog.Level
	for _, record := range r.WithMessage(message) {
		result = append(result, record.Level)
	}
	return result
}
