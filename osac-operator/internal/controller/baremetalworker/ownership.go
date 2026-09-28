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
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func (r *Reconciler) handleVerifiedClusterDeletion(ctx context.Context, co *v1alpha1.ClusterOrder) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(co, bmWorkerFinalizer) {
		return ctrl.Result{}, nil
	}
	tenant, err := r.authoritativeWorkerTenant(ctx, co)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.recoverPendingBMIReferences(ctx, co, tenant); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.checkStatusWorkerOwnership(ctx, co, tenant); err != nil {
		return ctrl.Result{}, err
	}
	return r.handleClusterDeletion(ctx, co)
}

// recoverPendingBMIReferences closes the create/status-write crash window during
// finalization. A missing recorded ID must not cause an existing BMI to be orphaned.
func (r *Reconciler) recoverPendingBMIReferences(ctx context.Context, co *v1alpha1.ClusterOrder, tenant string) error {
	if err := validateWorkerBMIReferences(co); err != nil {
		return err
	}
	pending := false
	for _, w := range co.Status.Workers {
		if w.Kind == workerKindBMI && w.BareMetalInstance.ID == "" {
			pending = true
			break
		}
	}
	if !pending {
		return nil
	}
	filter := fmt.Sprintf(`this.metadata.labels["%s"] == "%s"`, clusterOrderLabel, co.Name)
	bmis, err := r.fulfillment.ListBareMetalInstances(ctx, filter)
	if err != nil {
		return fmt.Errorf("recovering pending BMI references: %w", err)
	}
	byName := bmisByName(bmis)
	recovered := make(map[string]v1alpha1.BareMetalInstanceReference)
	for _, w := range co.Status.Workers {
		if w.Kind != workerKindBMI || w.BareMetalInstance.ID != "" {
			continue
		}
		name := w.BareMetalInstance.Name
		if bmi, ok := byName[name]; ok {
			if err := checkWorkerBMI(co, tenant, name, bmi); err != nil {
				return r.rejectWorkerIdentity(co, err.Error())
			}
			recovered[w.Name] = v1alpha1.BareMetalInstanceReference{Name: name, ID: bmi.GetId()}
		}
	}
	if len(recovered) == 0 {
		return nil
	}
	if err := r.patchStatusWithRetry(ctx, co, func(latest *v1alpha1.ClusterOrder) {
		for i := range latest.Status.Workers {
			w := &latest.Status.Workers[i]
			if ref, ok := recovered[w.Name]; ok && w.BareMetalInstance.ID == "" &&
				w.BareMetalInstance.Name == ref.Name {
				w.BareMetalInstance = ref
			}
		}
	}); err != nil {
		return err
	}
	return r.apiReader.Get(ctx, client.ObjectKeyFromObject(co), co)
}

func (r *Reconciler) verifyClusterOrderOwnership(ctx context.Context, co *v1alpha1.ClusterOrder) (string, error) {
	tenant, err := r.authoritativeWorkerTenant(ctx, co)
	if err != nil {
		return "", err
	}
	if err := r.checkStatusWorkerOwnership(ctx, co, tenant); err != nil {
		return "", err
	}
	return tenant, nil
}

// authoritativeWorkerTenant uses order metadata only to locate/check the fulfillment Cluster.
// Neither the label nor the order annotation is a source of BMI ownership.
func (r *Reconciler) authoritativeWorkerTenant(ctx context.Context, co *v1alpha1.ClusterOrder) (string, error) {
	id := co.Labels[clusterOrderIDLabel]
	if id == "" {
		return "", r.rejectWorkerIdentity(co, "missing Cluster ID label")
	}
	cluster, err := r.fulfillment.GetCluster(ctx, id)
	if err != nil {
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

// checkStatusWorkerOwnership must run before rebuild, retry or deletion, including the
// deletion-first finalizer path. Retain unknown/foreign status for safe manual recovery.
func (r *Reconciler) checkStatusWorkerOwnership(ctx context.Context, co *v1alpha1.ClusterOrder, tenant string) error {
	if err := validateWorkerBMIReferences(co); err != nil {
		return err
	}
	for _, w := range co.Status.Workers {
		if w.Kind != workerKindBMI || w.BareMetalInstance.ID == "" {
			continue
		}
		bmi, err := r.fulfillment.GetBareMetalInstance(ctx, w.BareMetalInstance.ID)
		if err != nil {
			if status.Code(err) == codes.NotFound {
				continue
			}
			return fmt.Errorf("checking worker BMI %s: %w", w.BareMetalInstance.ID, err)
		}
		if err := checkWorkerBMI(co, tenant, w.BareMetalInstance.Name, bmi); err != nil {
			return r.rejectWorkerIdentity(co, err.Error())
		}
	}
	return nil
}

func (r *Reconciler) checkedDeleteBMI(ctx context.Context, co *v1alpha1.ClusterOrder, w v1alpha1.WorkerStatus) error {
	tenant, err := r.authoritativeWorkerTenant(ctx, co)
	if err != nil {
		return err
	}
	bmi, err := r.fulfillment.GetBareMetalInstance(ctx, w.BareMetalInstance.ID)
	if status.Code(err) == codes.NotFound {
		return nil // Already gone: allow retry/scale-down to clear the stale status ID.
	}
	if err != nil {
		return err
	}
	if err := checkWorkerBMI(co, tenant, w.BareMetalInstance.Name, bmi); err != nil {
		return r.rejectWorkerIdentity(co, err.Error())
	}
	return r.fulfillment.DeleteBareMetalInstance(ctx, w.BareMetalInstance.ID)
}
