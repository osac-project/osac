/*
Copyright 2025.

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

	"github.com/osac-project/osac-operator/api/v1alpha1"
)

// ErrTenantBeingDeleted is returned by getTenant when the tenant exists but has DeletionTimestamp set.
var ErrTenantBeingDeleted = errors.New("tenant is being deleted")

// getTenant gets the tenant object from the local cluster.
// If the tenant is not found, returns nil and error.
// If the tenant has DeletionTimestamp, returns an error so the controller requeues (does not clear the instance's tenant reference).
func (r *ComputeInstanceReconciler) getTenant(ctx context.Context, instance *v1alpha1.ComputeInstance) (*v1alpha1.Tenant, error) {
	tenantName, exists := instance.GetAnnotations()[osacTenantKey]
	if !exists || tenantName == "" {
		return nil, fmt.Errorf("tenant information for compute instance %s not found", instance.GetName())
	}

	tenant := &v1alpha1.Tenant{}
	err := r.Get(ctx, client.ObjectKey{Namespace: r.TenantNamespace, Name: tenantName}, tenant)
	if err != nil {
		return nil, err
	}

	if !tenant.DeletionTimestamp.IsZero() {
		return nil, fmt.Errorf("%w: %s", ErrTenantBeingDeleted, tenant.GetName())
	}

	instance.SetTenantReferenceName(tenant.GetName())
	instance.SetTenantReferenceNamespace(tenant.GetNamespace())

	return tenant, nil
}

func labelSelectorFromComputeInstanceInstance(instance *v1alpha1.ComputeInstance) client.MatchingLabels {
	return client.MatchingLabels{
		osacComputeInstanceNameLabel: instance.GetName(),
	}
}
