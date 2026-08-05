/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package terminal

import (
	"context"
)

// contextKey is the type used to store the tool in the context.
type contextKey int

const (
	contextConsoleKey contextKey = iota
)

// ConsoleFromContext returns the console from the context. It panics if the given context doesn't contain a console.
func ConsoleFromContext(ctx context.Context) *Console {
	console := ctx.Value(contextConsoleKey).(*Console)
	if console == nil {
		panic("failed to get console from context")
	}
	return console
}

// ConsoleIntoContext creates a new context that contains the given console.
func ConsoleIntoContext(ctx context.Context, console *Console) context.Context {
	return context.WithValue(ctx, contextConsoleKey, console)
}
