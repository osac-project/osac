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

// NetworkClassManagers holds the raw fabric/k8s manager name fields read from a
// NetworkClass, decoupled from any generated proto/gRPC type so this package
// carries no dependency on osac-operator's internal/ packages.
type NetworkClassManagers struct {
	// FabricManager is the configured fabric manager name. Always non-empty
	// for a valid NetworkClass (Resolve validates this).
	FabricManager string

	// K8sManager is the configured k8s manager name, or "" if the NetworkClass
	// does not specify one (regions without VM workloads).
	K8sManager string
}

// NetworkClassClient fetches the manager names configured on a NetworkClass by
// ID. Resolver depends only on this interface — never on a concrete generated
// gRPC client type — so pkg/dispatcher can be imported by other operators in
// this monorepo that implement this interface against their own
// fulfillment-service client.
type NetworkClassClient interface {
	// GetNetworkClass fetches the manager configuration for networkClassID.
	// Returns a nil *NetworkClassManagers (with a nil error) if the backend
	// has no such NetworkClass; Resolve turns that into a domain error.
	GetNetworkClass(ctx context.Context, networkClassID string) (*NetworkClassManagers, error)
}

// Resolver fetches a NetworkClass from the fulfillment-service and validates
// its manager references against registered ConfigMaps.
type Resolver struct {
	networkClassClient NetworkClassClient
	discovery          *networkmanager.Discovery
}

// NewResolver creates a Resolver that uses the given NetworkClassClient and ConfigMap discovery.
func NewResolver(
	networkClassClient NetworkClassClient,
	discovery *networkmanager.Discovery,
) *Resolver {
	return &Resolver{
		networkClassClient: networkClassClient,
		discovery:          discovery,
	}
}

// Resolve fetches the NetworkClass by ID from the fulfillment-service, extracts the
// fabric and k8s manager names, and validates each against the registered ConfigMaps.
func (r *Resolver) Resolve(ctx context.Context, networkClassID string) (*ResolvedManagers, error) {
	managers, err := r.networkClassClient.GetNetworkClass(ctx, networkClassID)
	if err != nil {
		return nil, fmt.Errorf("fetching NetworkClass %q: %w", networkClassID, err)
	}

	if managers == nil {
		return nil, fmt.Errorf("NetworkClass %q: response contains no object", networkClassID)
	}

	if managers.FabricManager == "" {
		return nil, fmt.Errorf("NetworkClass %q: %w", networkClassID, ErrFabricManagerNotSet)
	}

	fabricMgr, err := r.discovery.GetFabricManager(ctx, managers.FabricManager)
	if err != nil {
		return nil, fmt.Errorf("NetworkClass %q: resolving fabricManager %q: %w",
			networkClassID, managers.FabricManager, err)
	}

	result := &ResolvedManagers{
		FabricManager: *fabricMgr,
	}

	if managers.K8sManager != "" {
		k8sMgr, err := r.discovery.GetK8sManager(ctx, managers.K8sManager)
		if err != nil {
			return nil, fmt.Errorf("NetworkClass %q: resolving k8sManager %q: %w",
				networkClassID, managers.K8sManager, err)
		}
		result.K8sManager = k8sMgr
	}

	return result, nil
}
