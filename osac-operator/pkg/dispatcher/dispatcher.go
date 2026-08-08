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
	"fmt"
)

// Dispatcher resolves a NetworkClass to its managers and builds a DispatchPlan
// that tells controllers which managers to target for a given resource kind.
type Dispatcher struct {
	resolver *cachedResolver
}

// NewDispatcher creates a Dispatcher with a fresh per-reconcile cache.
// The Resolver is stateless and safe to share across controllers; create it
// once at startup and pass it here at the top of each Reconcile call.
func NewDispatcher(resolver *Resolver) *Dispatcher {
	return &Dispatcher{
		resolver: newCachedResolver(resolver),
	}
}

// Dispatch resolves the NetworkClass and builds a DispatchPlan for the given
// resource kind.
// The returned plan contains one DispatchTarget per applicable manager role. For
// resources that require the k8s manager (e.g. Subnet), the k8s target is omitted
// when the NetworkClass does not specify a k8sManager (valid for non-VM regions).
func (d *Dispatcher) Dispatch(
	ctx context.Context,
	kind string,
	networkClassID string,
) (*DispatchPlan, error) {
	cfg := LookupDispatchConfig(kind)
	if cfg == nil {
		return nil, fmt.Errorf("no dispatch configuration for resource kind %q", kind)
	}

	resolved, err := d.resolver.Resolve(ctx, networkClassID)
	if err != nil {
		return nil, fmt.Errorf("resolving managers for %s: %w", kind, err)
	}

	plan := &DispatchPlan{}
	for _, role := range cfg.Roles {
		switch role {
		case ManagerRoleFabric:
			plan.Targets = append(plan.Targets, DispatchTarget{
				Role:    ManagerRoleFabric,
				Manager: resolved.FabricManager,
			})
		case ManagerRoleK8s:
			if resolved.K8sManager != nil {
				plan.Targets = append(plan.Targets, DispatchTarget{
					Role:    ManagerRoleK8s,
					Manager: *resolved.K8sManager,
				})
			}
		default:
			return nil, fmt.Errorf("no dispatch handling for manager role %q (kind %q)", role, kind)
		}
	}

	return plan, nil
}
