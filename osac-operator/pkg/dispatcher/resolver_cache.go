/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package dispatcher

import (
	"context"
)

// cachedResolver wraps a Resolver with a per-reconcile cache. It avoids repeated
// gRPC calls when multiple resources (e.g. a Subnet and its parent VirtualNetwork)
// reference the same NetworkClass within a single reconcile cycle.
type cachedResolver struct {
	inner *Resolver
	cache map[string]*ResolvedManagers
}

// newCachedResolver creates a cachedResolver that delegates to the given Resolver.
func newCachedResolver(inner *Resolver) *cachedResolver {
	return &cachedResolver{
		inner: inner,
		cache: make(map[string]*ResolvedManagers),
	}
}

// Resolve returns the resolved managers for the given NetworkClass ID, using a
// cached result if available. Only successful results are cached; errors always
// trigger a fresh resolve attempt.
func (c *cachedResolver) Resolve(ctx context.Context, networkClassID string) (*ResolvedManagers, error) {
	if cached, ok := c.cache[networkClassID]; ok {
		return cached, nil
	}

	managers, err := c.inner.Resolve(ctx, networkClassID)
	if err != nil {
		return nil, err
	}
	c.cache[networkClassID] = managers
	return managers, nil
}
