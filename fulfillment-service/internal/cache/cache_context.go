/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package cache

import (
	"context"
)

// contextKey is the type used to store the tool in the context.
type contextKey int

const (
	contextDirKey contextKey = iota
)

// DirFromContext returns the cache directory from the context. It panics if the given context doesn't contain a cache
// directory.
func DirFromContext(ctx context.Context) string {
	dir := ctx.Value(contextDirKey).(string)
	if dir == "" {
		panic("failed to get cache directory from context")
	}
	return dir
}

// DirIntoContext creates a new context that contains the given cache directory.
func DirIntoContext(ctx context.Context, dir string) context.Context {
	return context.WithValue(ctx, contextDirKey, dir)
}
