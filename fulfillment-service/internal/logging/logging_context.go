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
	"log/slog"
)

// contextKey is the type used to store the tool in the context.
type contextKey int

const (
	contextLoggerKey contextKey = iota
)

// LoggerFromContext returns the logger from the context. It panics if the given context doesn't contain a logger.
func LoggerFromContext(ctx context.Context) *slog.Logger {
	logger := ctx.Value(contextLoggerKey).(*slog.Logger)
	if logger == nil {
		panic("failed to get logger from context")
	}
	return logger
}

// LoggerIntoContext creates a new context that contains the given logger.
func LoggerIntoContext(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, contextLoggerKey, logger)
}
