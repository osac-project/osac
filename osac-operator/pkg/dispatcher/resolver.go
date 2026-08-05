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

	"github.com/osac-project/osac/osac-operator/pkg/networkmanager"
)

// ResolvedManagers holds the validated fabric and k8s managers extracted from a NetworkClass.
type ResolvedManagers struct {
	// FabricManager is the validated fabric manager. Always populated.
	FabricManager networkmanager.Manager

	// K8sManager is the validated k8s manager, or nil when the NetworkClass
	// does not specify one (regions without VM workloads).
	K8sManager *networkmanager.Manager
}

// Resolver fetches a NetworkClass and validates its manager references against
// registered ConfigMaps.
type Resolver struct {
	fetcher   NetworkClassFetcher
	discovery *networkmanager.Discovery
}

// NewResolver creates a Resolver that uses the given fetcher and ConfigMap discovery.
func NewResolver(
	fetcher NetworkClassFetcher,
	discovery *networkmanager.Discovery,
) *Resolver {
	return &Resolver{
		fetcher:   fetcher,
		discovery: discovery,
	}
}

// Resolve fetches the NetworkClass by ID, extracts the fabric and k8s manager
// names, and validates each against the registered ConfigMaps.
func (r *Resolver) Resolve(ctx context.Context, networkClassID string) (*ResolvedManagers, error) {
	ncInfo, err := r.fetcher.FetchNetworkClass(ctx, networkClassID)
	if err != nil {
		return nil, fmt.Errorf("fetching NetworkClass %q: %w", networkClassID, err)
	}

	if ncInfo.FabricManager == "" {
		return nil, fmt.Errorf("NetworkClass %q: fabricManager is required but not set", networkClassID)
	}

	fabricMgr, err := r.discovery.GetFabricManager(ctx, ncInfo.FabricManager)
	if err != nil {
		return nil, fmt.Errorf("NetworkClass %q: resolving fabricManager %q: %w",
			networkClassID, ncInfo.FabricManager, err)
	}

	result := &ResolvedManagers{
		FabricManager: *fabricMgr,
	}

	if ncInfo.K8sManager != "" {
		k8sMgr, err := r.discovery.GetK8sManager(ctx, ncInfo.K8sManager)
		if err != nil {
			return nil, fmt.Errorf("NetworkClass %q: resolving k8sManager %q: %w",
				networkClassID, ncInfo.K8sManager, err)
		}
		result.K8sManager = k8sMgr
	}

	return result, nil
}
