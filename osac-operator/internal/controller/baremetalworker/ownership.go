/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package baremetalworker

import (
	"context"
	"errors"
	"fmt"

	corev1 "k8s.io/api/core/v1"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const (
	// clusterOrderIDLabel is the label set by the fulfillment-service provisioning flow, carrying
	// the fulfillment-service Cluster UUID. Same key as controller.osacClusterOrderIDLabel (unexported).
	clusterOrderIDLabel      = "osac.openshift.io/clusterorder-uuid"
	clusterOrderLabel        = "osac.openshift.io/cluster-order"
	ownerReferenceAnnotation = "osac.openshift.io/owner-reference"
)

// authoritativeWorkerTenant uses order metadata only to locate/check the fulfillment Cluster.
// Neither the label nor the order annotation is a source of BMI ownership.
func (r *Reconciler) authoritativeWorkerTenant(ctx context.Context, co *v1alpha1.ClusterOrder) (string, error) {
	id := co.Labels[clusterOrderIDLabel]
	if id == "" {
		return "", r.rejectWorkerIdentity(co, "missing Cluster ID label")
	}
	cluster, err := r.fulfillment.GetCluster(ctx, id)
	if err != nil {
		// Transport unavailability is not an ownership mismatch. Fail closed with
		// the real blocker and let the reconciler persist the availability
		// condition; only semantic failures are reported as ownership rejections.
		if errors.Is(err, ErrFulfillmentServiceUnavailable) {
			return "", fmt.Errorf("getting authoritative Cluster %s: %w", id, err)
		}
		return "", r.rejectWorkerIdentity(co, fmt.Sprintf("getting authoritative Cluster %s: %v", id, err))
	}
	if cluster == nil || cluster.GetId() != id {
		return "", r.rejectWorkerIdentity(co, "authoritative Cluster ID does not match the order")
	}
	tenant := cluster.GetMetadata().GetTenant()
	if tenant == "" || co.Annotations["osac.openshift.io/tenant"] != tenant {
		return "", r.rejectWorkerIdentity(co, "missing or mismatched ClusterOrder/Cluster tenant")
	}
	return tenant, nil
}

func (r *Reconciler) rejectWorkerIdentity(co *v1alpha1.ClusterOrder, reason string) error {
	r.recorder.Eventf(co, nil, corev1.EventTypeWarning, "WorkerOwnershipMismatch", "CheckWorkerOwnership", "%s", reason)
	return fmt.Errorf("worker ownership: %s", reason)
}

func checkWorkerBMI(co *v1alpha1.ClusterOrder, tenant, name string, bmi *privatev1.BareMetalInstance) error {
	if bmi == nil || bmi.GetId() == "" || bmi.GetMetadata().GetTenant() != tenant ||
		bmi.GetMetadata().GetName() != name ||
		bmi.GetMetadata().GetAnnotations()["osac.openshift.io/owner-reference"] != "ClusterOrder/"+co.Name ||
		bmi.GetMetadata().GetLabels()[clusterOrderLabel] != co.Name {
		return fmt.Errorf("BMI %s is not owned by ClusterOrder %s in tenant %s", bmi.GetId(), co.Name, tenant)
	}
	return nil
}

// validateWorkerBMIReferences requires an explicit BMI name even before creation.
// Worker names identify status slots and are not a source of external identity.
func validateWorkerBMIReferences(co *v1alpha1.ClusterOrder) error {
	for _, w := range co.Status.Workers {
		if w.Kind == workerKindBMI && w.BareMetalInstance.Name == "" {
			return fmt.Errorf("worker %q: bareMetalInstance.name is required", w.Name)
		}
	}
	return nil
}

func checkRecordedWorkerBMI(co *v1alpha1.ClusterOrder, tenant string, w v1alpha1.WorkerStatus, bmi *privatev1.BareMetalInstance) error {
	if err := checkWorkerBMI(co, tenant, w.BareMetalInstance.Name, bmi); err != nil {
		return err
	}
	if bmi.GetId() != w.BareMetalInstance.ID {
		return fmt.Errorf("BMI ID does not match the recorded reference")
	}
	return nil
}
