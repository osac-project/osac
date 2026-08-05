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

// Package dispatcheradapter adapts osac-operator's generated fulfillment-service
// gRPC clients to the plain interfaces pkg/ packages depend on, keeping
// generated proto types out of those packages' public APIs.
package dispatcheradapter

import (
	"context"

	privatev1 "github.com/osac-project/osac/osac-operator/internal/api/osac/private/v1"
	"github.com/osac-project/osac/osac-operator/pkg/dispatcher"
)

var _ dispatcher.NetworkClassClient = (*NetworkClassAdapter)(nil)

// NetworkClassAdapter adapts a privatev1.NetworkClassesClient to
// dispatcher.NetworkClassClient.
type NetworkClassAdapter struct {
	client privatev1.NetworkClassesClient
}

// NewNetworkClassAdapter wraps the given gRPC client.
func NewNetworkClassAdapter(client privatev1.NetworkClassesClient) *NetworkClassAdapter {
	return &NetworkClassAdapter{client: client}
}

// GetNetworkClass implements dispatcher.NetworkClassClient.
func (a *NetworkClassAdapter) GetNetworkClass(ctx context.Context, networkClassID string) (*dispatcher.NetworkClassManagers, error) {
	resp, err := a.client.Get(ctx, &privatev1.NetworkClassesGetRequest{Id: networkClassID})
	if err != nil {
		return nil, err
	}

	nc := resp.GetObject()
	if nc == nil {
		return nil, nil
	}

	return &dispatcher.NetworkClassManagers{
		FabricManager: nc.GetFabricManager(),
		K8sManager:    nc.GetK8SManager(),
	}, nil
}
