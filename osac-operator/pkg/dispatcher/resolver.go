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
	"errors"
	"fmt"

	privatev1 "github.com/osac-project/osac/osac-operator/internal/api/osac/private/v1"
	"github.com/osac-project/osac/osac-operator/pkg/networkmanager"
)

// ErrFabricManagerNotSet is returned by Resolve when the NetworkClass has no
// fabricManager set. It is wrapped in the returned error so callers can match it
// with errors.Is to distinguish "not configured yet" from other resolution failures.
var ErrFabricManagerNotSet = errors.New("fabricManager is required but not set")

// ResolvedManagers holds the validated fabric and k8s managers extracted from a NetworkClass.
type ResolvedManagers struct {
	// FabricManager is the validated fabric manager. Always populated.
	FabricManager networkmanager.Manager

	// K8sManager is the validated k8s manager, or nil when the NetworkClass
	// does not specify one (regions without VM workloads).
	K8sManager *networkmanager.Manager
}

// Resolver fetches a NetworkClass from the fulfillment-service and validates
// its manager references against registered ConfigMaps.
type Resolver struct {
	networkClassesClient privatev1.NetworkClassesClient
	discovery            *networkmanager.Discovery
}

// NewResolver creates a Resolver that uses the given gRPC client and ConfigMap discovery.
func NewResolver(
	ncClient privatev1.NetworkClassesClient,
	discovery *networkmanager.Discovery,
) *Resolver {
	return &Resolver{
		networkClassesClient: ncClient,
		discovery:            discovery,
	}
}

// Resolve fetches the NetworkClass by ID from the fulfillment-service, extracts the
// fabric and k8s manager names, and validates each against the registered ConfigMaps.
func (r *Resolver) Resolve(ctx context.Context, networkClassID string) (*ResolvedManagers, error) {
	resp, err := r.networkClassesClient.Get(ctx, &privatev1.NetworkClassesGetRequest{Id: networkClassID})
	if err != nil {
		return nil, fmt.Errorf("fetching NetworkClass %q: %w", networkClassID, err)
	}

	nc := resp.GetObject()
	if nc == nil {
		return nil, fmt.Errorf("NetworkClass %q: response contains no object", networkClassID)
	}

	fabricManagerName := nc.GetFabricManager()
	if fabricManagerName == "" {
		return nil, fmt.Errorf("NetworkClass %q: %w", networkClassID, ErrFabricManagerNotSet)
	}

	fabricMgr, err := r.discovery.GetFabricManager(ctx, fabricManagerName)
	if err != nil {
		return nil, fmt.Errorf("NetworkClass %q: resolving fabricManager %q: %w",
			networkClassID, fabricManagerName, err)
	}

	result := &ResolvedManagers{
		FabricManager: *fabricMgr,
	}

	k8sManagerName := nc.GetK8SManager()
	if k8sManagerName != "" {
		k8sMgr, err := r.discovery.GetK8sManager(ctx, k8sManagerName)
		if err != nil {
			return nil, fmt.Errorf("NetworkClass %q: resolving k8sManager %q: %w",
				networkClassID, k8sManagerName, err)
		}
		result.K8sManager = k8sMgr
	}

	return result, nil
}
