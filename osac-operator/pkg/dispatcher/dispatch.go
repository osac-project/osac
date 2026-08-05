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
	"github.com/osac-project/osac/osac-operator/pkg/networkmanager"
)

// ManagerRole identifies which manager type handles an operation.
type ManagerRole string

const (
	// ManagerRoleFabric targets the fabric manager for physical networking operations.
	ManagerRoleFabric ManagerRole = "fabric"

	// ManagerRoleK8s targets the k8s manager for overlay-to-fabric bridging.
	ManagerRoleK8s ManagerRole = "k8s"
)

// DispatchTarget pairs a manager role with its resolved manager details.
type DispatchTarget struct {
	// Role identifies why this manager is targeted (fabric vs k8s).
	Role ManagerRole

	// Manager is the validated manager registration from ConfigMap discovery.
	Manager networkmanager.Manager
}

// DispatchPlan describes which managers should handle provisioning for a resource.
type DispatchPlan struct {
	// Targets lists the managers to dispatch to, in order.
	Targets []DispatchTarget
}

// HasRole reports whether the plan includes a target with the given role.
func (p *DispatchPlan) HasRole(role ManagerRole) bool {
	if p == nil {
		return false
	}
	for i := range p.Targets {
		if p.Targets[i].Role == role {
			return true
		}
	}
	return false
}

// FabricTarget returns the fabric manager target, or nil if not present.
func (p *DispatchPlan) FabricTarget() *DispatchTarget {
	if p == nil {
		return nil
	}
	for i := range p.Targets {
		if p.Targets[i].Role == ManagerRoleFabric {
			return &p.Targets[i]
		}
	}
	return nil
}

// K8sTarget returns the k8s manager target, or nil if not present.
func (p *DispatchPlan) K8sTarget() *DispatchTarget {
	if p == nil {
		return nil
	}
	for i := range p.Targets {
		if p.Targets[i].Role == ManagerRoleK8s {
			return &p.Targets[i]
		}
	}
	return nil
}

// ResourceDispatchConfig defines which manager roles handle a given resource type.
type ResourceDispatchConfig struct {
	Roles []ManagerRole
}

// dispatchTable maps Kubernetes resource kinds to the manager roles that handle
// their provisioning operations.
var dispatchTable = map[string]ResourceDispatchConfig{
	"VirtualNetwork":       {Roles: []ManagerRole{ManagerRoleFabric}},
	"Subnet":               {Roles: []ManagerRole{ManagerRoleFabric, ManagerRoleK8s}},
	"SecurityGroup":        {Roles: []ManagerRole{ManagerRoleFabric}},
	"ExternalIP":           {Roles: []ManagerRole{ManagerRoleFabric}},
	"ExternalIPPool":       {Roles: []ManagerRole{ManagerRoleFabric}},
	"ExternalIPAttachment": {Roles: []ManagerRole{ManagerRoleFabric}},
	"NATGateway":           {Roles: []ManagerRole{ManagerRoleFabric}},
}

// LookupDispatchConfig returns the dispatch configuration for a resource kind.
// Returns nil if the kind is not in the dispatch table. The returned value is a
// defensive copy so callers cannot mutate the shared dispatchTable entry.
func LookupDispatchConfig(kind string) *ResourceDispatchConfig {
	cfg, ok := dispatchTable[kind]
	if !ok {
		return nil
	}
	rolesCopy := make([]ManagerRole, len(cfg.Roles))
	copy(rolesCopy, cfg.Roles)
	return &ResourceDispatchConfig{Roles: rolesCopy}
}

// KnownKinds returns a list of all resource kinds in the dispatch table.
func KnownKinds() []string {
	kinds := make([]string, 0, len(dispatchTable))
	for k := range dispatchTable {
		kinds = append(kinds, k)
	}
	return kinds
}
