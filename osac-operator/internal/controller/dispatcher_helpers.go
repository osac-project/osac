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

package controller

import (
	"context"
	"errors"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/dispatcher"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

// resolveDispatchPlan resolves the full DispatchPlan for a resource kind.
//
// Returns (nil, nil) when the dispatcher path is not active: resolver is nil,
// networkClassID is empty, or the NetworkClass has neither a fabricManager nor a
// k8sManager set (dispatcher.ErrNoManagerConfigured) — all cases where the caller
// should fall back to its own legacy behavior. Any other resolution error (e.g. a
// fabricManager or k8sManager referencing an unregistered manager ConfigMap) is
// returned to the caller as a real reconcile error, since that indicates a
// misconfiguration rather than an expected pre-migration state.
func resolveDispatchPlan(
	ctx context.Context,
	resolver *dispatcher.Resolver,
	kind string,
	networkClassID string,
) (*dispatcher.DispatchPlan, error) {
	if resolver == nil || networkClassID == "" {
		return nil, nil
	}

	plan, err := dispatcher.NewDispatcher(resolver).Dispatch(ctx, kind, networkClassID)
	switch {
	case err == nil:
		return plan, nil
	case errors.Is(err, dispatcher.ErrNoManagerConfigured):
		return nil, nil
	default:
		return nil, fmt.Errorf("resolving dispatch plan for %s (networkClass %q): %w", kind, networkClassID, err)
	}
}

// resolveImplementationStrategy determines the value a networking controller should
// write into osacImplementationStrategyAnnotation for AAP playbook selection.
//
// When the dispatcher path is active (see resolveDispatchPlan) it returns the resolved
// plan's fabric manager name. Otherwise it returns legacyStrategy unchanged — this is
// the behavior for deployments without the two-manager model configured, or resources
// using the platform-default NetworkClass (which has no ID to resolve against).
func resolveImplementationStrategy(
	ctx context.Context,
	resolver *dispatcher.Resolver,
	kind string,
	networkClassID string,
	legacyStrategy string,
) (string, error) {
	plan, err := resolveDispatchPlan(ctx, resolver, kind, networkClassID)
	if err != nil {
		return "", err
	}
	if plan == nil {
		return legacyStrategy, nil
	}
	target := plan.FabricTarget()
	if target == nil {
		// A K8sFallback kind (e.g. VirtualNetwork, SecurityGroup) with no fabricManager
		// resolves its fabric role to a k8s-role target instead (see Dispatch), so check
		// K8sTarget before giving up and falling back to legacyStrategy.
		target = plan.K8sTarget()
	}
	if target == nil {
		return legacyStrategy, nil
	}
	return target.Manager.Name, nil
}

// dispatchTargetProvider decorates a shared provisioning.ProvisioningProvider so that
// TriggerProvision/TriggerDeprovision see a resource clone with
// osacImplementationStrategyAnnotation overridden to managerName. AAP playbooks read
// this annotation from the serialized resource payload at trigger time, so overriding
// it per call is sufficient to route each dispatch target's job to the correct Ansible
// role through the one shared AAPProvider instance and template. The caller's original
// resource is never mutated. GetProvisionStatus/GetDeprovisionStatus poll by jobID and
// don't re-serialize the resource, so they delegate the resource through unmodified.
type dispatchTargetProvider struct {
	base        provisioning.ProvisioningProvider
	managerName string
}

// newDispatchTargetProvider creates a dispatchTargetProvider that routes jobs for
// managerName through base.
func newDispatchTargetProvider(base provisioning.ProvisioningProvider, managerName string) *dispatchTargetProvider {
	return &dispatchTargetProvider{base: base, managerName: managerName}
}

func (p *dispatchTargetProvider) TriggerProvision(ctx context.Context, resource client.Object) (*provisioning.ProvisionResult, error) {
	return p.base.TriggerProvision(ctx, p.withOverriddenStrategy(resource))
}

func (p *dispatchTargetProvider) GetProvisionStatus(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error) {
	return p.base.GetProvisionStatus(ctx, resource, jobID)
}

func (p *dispatchTargetProvider) TriggerDeprovision(
	ctx context.Context, resource client.Object, provisionJobs []v1alpha1.JobStatus,
) (*provisioning.DeprovisionResult, error) {
	return p.base.TriggerDeprovision(ctx, p.withOverriddenStrategy(resource), provisionJobs)
}

func (p *dispatchTargetProvider) GetDeprovisionStatus(ctx context.Context, resource client.Object, jobID string) (provisioning.ProvisionStatus, error) {
	return p.base.GetDeprovisionStatus(ctx, resource, jobID)
}

func (p *dispatchTargetProvider) Name() string {
	return p.base.Name()
}

// withOverriddenStrategy returns a deep copy of resource with
// osacImplementationStrategyAnnotation set to p.managerName, leaving resource itself
// untouched.
func (p *dispatchTargetProvider) withOverriddenStrategy(resource client.Object) client.Object {
	clone := resource.DeepCopyObject().(client.Object) //nolint:forcetypeassert // client.Object always implements DeepCopyObject returning itself
	annotations := clone.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string, 1)
	}
	annotations[osacImplementationStrategyAnnotation] = p.managerName
	clone.SetAnnotations(annotations)
	return clone
}
