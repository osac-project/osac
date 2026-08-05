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

// Package adapters provides internal implementations of public pkg/ interfaces
// for osac-operator's own use.
package adapters

import (
	"context"
	"fmt"

	privatev1 "github.com/osac-project/osac/osac-operator/internal/api/osac/private/v1"
	"github.com/osac-project/osac/osac-operator/pkg/dispatcher"
)

// GRPCNetworkClassFetcher adapts the generated privatev1.NetworkClassesClient
// to the dispatcher.NetworkClassFetcher interface.
type GRPCNetworkClassFetcher struct {
	client privatev1.NetworkClassesClient
}

// NewGRPCNetworkClassFetcher creates a fetcher that delegates to the given gRPC client.
func NewGRPCNetworkClassFetcher(client privatev1.NetworkClassesClient) *GRPCNetworkClassFetcher {
	return &GRPCNetworkClassFetcher{client: client}
}

// FetchNetworkClass calls the gRPC NetworkClasses.Get endpoint and returns the
// extracted manager names.
func (f *GRPCNetworkClassFetcher) FetchNetworkClass(
	ctx context.Context,
	id string,
) (*dispatcher.NetworkClassInfo, error) {
	resp, err := f.client.Get(ctx, &privatev1.NetworkClassesGetRequest{Id: id})
	if err != nil {
		return nil, err
	}

	nc := resp.GetObject()
	if nc == nil {
		return nil, fmt.Errorf("NetworkClass %q: response contains no object", id)
	}

	return &dispatcher.NetworkClassInfo{
		FabricManager: nc.GetFabricManager(),
		K8sManager:    nc.GetK8SManager(),
	}, nil
}
