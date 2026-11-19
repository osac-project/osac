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

// Package networkingmode resolves the installation-wide provider networking mode
// from the deployment's NetworkClass through the fulfillment private API.
package networkingmode

import (
	"context"
	"fmt"
	"sync"

	"google.golang.org/grpc"

	"github.com/osac-project/osac/bare-metal-fulfillment-operator/internal/controller"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type networkClassesClient interface {
	List(ctx context.Context, request *privatev1.NetworkClassesListRequest,
		opts ...grpc.CallOption) (*privatev1.NetworkClassesListResponse, error)
}

// Resolver reads the singleton NetworkClass and returns its provisioning mode. The mode is installation state and
// immutable, so a successful read is cached for the lifetime of the process.
type Resolver struct {
	client     networkClassesClient
	configured *bool

	mu       sync.Mutex
	resolved bool
	enabled  bool
}

// NewResolver creates a resolver. When configured is set, the accepted mode must match it; the NetworkClass is the
// authority.
func NewResolver(client networkClassesClient, configured *bool) *Resolver {
	return &Resolver{client: client, configured: configured}
}

// Mode returns the accepted provider networking mode. It returns controller.ErrProvisioningModeUnresolved while the
// NetworkClass, or its provisioning mode, isn't available.
func (r *Resolver) Mode(ctx context.Context) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.resolved {
		return r.enabled, nil
	}
	filter := "!has(this.metadata.deletion_timestamp)"
	response, err := r.client.List(ctx, privatev1.NetworkClassesListRequest_builder{
		Filter: &filter,
		Limit:  new(int32(2)),
	}.Build())
	if err != nil {
		return false, fmt.Errorf("%w: listing NetworkClasses: %v", controller.ErrProvisioningModeUnresolved, err)
	}
	switch {
	case len(response.GetItems()) == 0:
		return false, fmt.Errorf("%w: no NetworkClass exists yet", controller.ErrProvisioningModeUnresolved)
	case len(response.GetItems()) > 1:
		return false, fmt.Errorf("%w: more than one NetworkClass exists", controller.ErrProvisioningModeUnresolved)
	}
	class := response.GetItems()[0]
	if !class.GetSpec().HasProvisioningEnabled() {
		return false, fmt.Errorf("%w: NetworkClass %s has no provisioning mode", controller.ErrProvisioningModeUnresolved,
			class.GetId())
	}
	enabled := class.GetSpec().GetProvisioningEnabled()
	if r.configured != nil && *r.configured != enabled {
		return false, fmt.Errorf("global.networking.provisioningEnabled is %t but the NetworkClass records %t: "+
			"global.networking.provisioningEnabled cannot change after installation; reinstall to change it",
			*r.configured, enabled)
	}
	r.resolved = true
	r.enabled = enabled
	return enabled, nil
}

var _ controller.ProvisioningMode = (*Resolver)(nil)

// ensure errors stays referenced.
