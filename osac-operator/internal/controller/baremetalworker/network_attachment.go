/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package baremetalworker

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

const (
	subnetUUIDLabel        = "osac.openshift.io/subnet-uuid"
	securityGroupUUIDLabel = "osac.openshift.io/securitygroup-uuid"
)

// networkResourceID resolves a Kubernetes network CR name to the Fulfillment resource ID
// stored on that CR. Fulfillment references accept IDs or database resource names; the CR's
// generated Kubernetes name is not necessarily its Fulfillment resource name.
func (r *Reconciler) networkResourceID(
	ctx context.Context,
	co *v1alpha1.ClusterOrder,
	tenant, name, kind, idLabel string,
	resource client.Object,
) (string, error) {
	key := client.ObjectKey{Namespace: co.Namespace, Name: name}
	if err := r.Get(ctx, key, resource); err != nil {
		if apierrors.IsNotFound(err) {
			return "", fmt.Errorf("%s CR %q not found in namespace %q", kind, name, co.Namespace)
		}
		return "", fmt.Errorf("getting %s CR %q in namespace %q: %w", kind, name, co.Namespace, err)
	}

	resourceTenant := resource.GetAnnotations()[tenantAnnotationKey]
	if resourceTenant == "" || resourceTenant != tenant {
		return "", fmt.Errorf("%s CR %q tenant %q does not match ClusterOrder tenant %q", kind, name, resourceTenant, tenant)
	}

	id := resource.GetLabels()[idLabel]
	if id == "" {
		return "", fmt.Errorf("%s CR %q is missing required label %q", kind, name, idLabel)
	}
	return id, nil
}
