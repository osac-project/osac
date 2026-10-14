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
	"fmt"
	"os"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

const (
	ontapStorageProvider           = "ontap"
	preparationUsernameKey         = "username"
	preparationPasswordKey         = "password"
	storagePreparationTypeLabel    = "osac.openshift.io/secret-type"
	storagePreparationType         = "storage-tenant-preparation"
	storagePreparationBackendLabel = "osac.openshift.io/storage-backend-id"
)

func ontapBackendIDs(tiers []provisioning.TierDefinition) []string {
	seen := map[string]bool{}
	for _, tier := range tiers {
		if tier.Provider == ontapStorageProvider {
			seen[tier.BackendID] = true
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func storagePreparationNamespace() string {
	if ns := os.Getenv("OSAC_STORAGE_PREPARATION_NAMESPACE"); ns != "" {
		return ns
	}
	return "trident"
}

func setStorageWaiting(tenant *v1alpha1.Tenant, reason, message string) {
	for _, typ := range []v1alpha1.TenantConditionType{v1alpha1.TenantConditionStorageBackendReady, v1alpha1.TenantConditionClusterStorageReady} {
		tenant.SetStatusCondition(typ, metav1.ConditionFalse, reason, message)
		tenant.GetStatusCondition(typ).ObservedGeneration = tenant.Generation
	}
}

// Preparation Secrets are inputs, never backend readiness markers. An uncached
// hub read avoids consuming stale credentials or depending on workload caches.
func (r *StorageReconciler) resolveTenantPreparations(ctx context.Context, tenant *v1alpha1.Tenant, tiers []provisioning.TierDefinition) bool {
	ids := ontapBackendIDs(tiers)
	if len(ids) == 0 {
		return true
	}
	tenantID := tenant.Labels[osacTenantIDLabel]
	if tenantID == "" {
		setStorageWaiting(tenant, "TenantIdentityMissing", "Waiting for the Tenant's immutable Fulfillment ID")
		return false
	}
	connections := provisioning.StorageBackendConnectionsFromContext(ctx)
	for _, id := range ids {
		var secrets corev1.SecretList
		if err := r.APIReader.List(ctx, &secrets, client.InNamespace(storagePreparationNamespace()), client.MatchingLabels{
			storagePreparationTypeLabel: storagePreparationType, storagePreparationBackendLabel: id, osacTenantIDLabel: tenantID,
		}); err != nil {
			setStorageWaiting(tenant, "PreparationUnavailable", fmt.Sprintf("Cannot read preparation for backend %q; check operator access to the preparation namespace", id))
			return false
		}
		if len(secrets.Items) == 0 {
			setStorageWaiting(tenant, "PreparationMissing", fmt.Sprintf("Waiting for a tenant preparation Secret for backend %q in namespace %q", id, storagePreparationNamespace()))
			return false
		}
		if len(secrets.Items) != 1 {
			setStorageWaiting(tenant, "PreparationAmbiguous", fmt.Sprintf("Multiple preparation Secrets match this Tenant and backend %q; keep exactly one", id))
			return false
		}
		s := &secrets.Items[0]
		if !s.DeletionTimestamp.IsZero() || s.Type != corev1.SecretTypeOpaque ||
			(s.Labels[osacTenantKey] != "" && s.Labels[osacTenantKey] != tenant.Name) ||
			strings.TrimSpace(string(s.Data[preparationUsernameKey])) == "" || strings.TrimSpace(string(s.Data[preparationPasswordKey])) == "" || strings.TrimSpace(string(s.Data["svm"])) == "" {
			setStorageWaiting(tenant, "PreparationInvalid", fmt.Sprintf("Preparation for backend %q must be a non-deleting Opaque Secret with matching tenant identity and non-empty username, password and svm", id))
			return false
		}
		connection := connections[id]
		connection.TenantPreparation = &provisioning.TenantPreparation{
			TenantID: tenantID, Namespace: s.Namespace, Name: s.Name, UID: string(s.UID), ResourceVersion: s.ResourceVersion,
			ProviderConfig: map[string]any{preparationUsernameKey: string(s.Data[preparationUsernameKey]), preparationPasswordKey: string(s.Data[preparationPasswordKey]), "svm": string(s.Data["svm"])},
		}
		connections[id] = connection
	}
	return true
}

// External preparation credentials are deliberately excluded: rotation is not
// automatically applied. Immutable source identity and tier changes invalidate
// successful jobs without writing secrets (or credential hashes) into status.
func storageDesiredConfigVersion(ctx context.Context, tenant *v1alpha1.Tenant) string {
	tiers := provisioning.StorageTierDefinitionsFromContext(ctx)
	if len(ontapBackendIDs(tiers)) == 0 {
		return ""
	}
	type source struct{ BackendID, UID, SVM string }
	sources := make([]source, 0)
	connections := provisioning.StorageBackendConnectionsFromContext(ctx)
	for _, id := range ontapBackendIDs(tiers) {
		if p := connections[id].TenantPreparation; p != nil {
			svm, _ := p.ProviderConfig["svm"].(string)
			sources = append(sources, source{id, p.UID, svm})
		}
	}
	version, _ := provisioning.ComputeDesiredConfigVersion(struct {
		TenantID   string
		Generation int64
		Tiers      []provisioning.TierDefinition
		Sources    []source
	}{tenant.Labels[osacTenantIDLabel], tenant.Generation, tiers, sources})
	return version
}

func (r *StorageReconciler) handlePreparedClusterStorage(ctx context.Context, tenant *v1alpha1.Tenant) (ctrl.Result, bool, error) {
	if storageDesiredConfigVersion(ctx, tenant) == "" || preparedStorageApplied(tenant.Status.ClusterStorageJobs, storageDesiredConfigVersion(ctx, tenant)) {
		return ctrl.Result{}, false, nil
	}
	tenant.SetStatusCondition(v1alpha1.TenantConditionClusterStorageReady, metav1.ConditionFalse, "ClusterSetupPending", "Waiting for native storage configuration to complete")
	tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady).ObservedGeneration = tenant.Generation
	if r.ClusterStorageProvider == nil {
		return ctrl.Result{RequeueAfter: r.StatusPollInterval}, true, nil
	}
	result, err := r.handleClusterStorageProvisioning(ctx, tenant, true)
	if err == nil && result.RequeueAfter == 0 {
		result.RequeueAfter = r.StatusPollInterval
	}
	return result, true, err
}

func preparedStorageApplied(jobs []v1alpha1.JobStatus, version string) bool {
	latest := provisioning.FindLatestJobByType(jobs, v1alpha1.JobTypeProvision)
	return latest != nil && latest.State == v1alpha1.JobStateSucceeded && latest.ConfigVersion == version && version != ""
}

func preparedStorageStarted(tenant *v1alpha1.Tenant) bool {
	for _, job := range tenant.Status.StorageBackendJobs {
		if job.Type == v1alpha1.JobTypeProvision && job.ConfigVersion != "" {
			return true
		}
	}
	return false
}

func (r *StorageReconciler) preparedStorageRequeue(ctx context.Context, tenant *v1alpha1.Tenant) ctrl.Result {
	if len(ontapBackendIDs(provisioning.StorageTierDefinitionsFromContext(ctx))) == 0 {
		return ctrl.Result{}
	}
	tenant.GetStatusCondition(v1alpha1.TenantConditionClusterStorageReady).ObservedGeneration = tenant.Generation
	return ctrl.Result{RequeueAfter: r.StatusPollInterval}
}
