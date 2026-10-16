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
	"time"

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
	storagePreparationBackendLabel = "osac.openshift.io/storage-backend"
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
	return "osac-storage"
}

// Stamp the generation evaluated by this reconcile, not a newer generation
// fetched while saving status. Fulfillment ignores stale storage observations.
func setTenantStorageCondition(tenant *v1alpha1.Tenant, typ v1alpha1.TenantConditionType, status metav1.ConditionStatus, reason, message string) {
	tenant.SetStatusCondition(typ, status, reason, message)
	tenant.GetStatusCondition(typ).ObservedGeneration = tenant.Generation
}

func setStorageWaiting(tenant *v1alpha1.Tenant, reason, message string) {
	for _, typ := range []v1alpha1.TenantConditionType{v1alpha1.TenantConditionStorageBackendReady, v1alpha1.TenantConditionClusterStorageReady} {
		setTenantStorageCondition(tenant, typ, metav1.ConditionFalse, reason, message)
	}
}

// Preparation Secrets are inputs, never backend readiness markers. An uncached
// hub read avoids consuming stale credentials or depending on workload caches.
func (r *StorageReconciler) resolveTenantPreparations(ctx context.Context, tenant *v1alpha1.Tenant, tiers []provisioning.TierDefinition) bool {
	ids := ontapBackendIDs(tiers)
	if len(ids) == 0 {
		return true
	}
	connections := provisioning.StorageBackendConnectionsFromContext(ctx)
	for _, id := range ids {
		connection := connections[id]
		if _, shared := connection.ProviderConfig["profiles"]; shared {
			continue
		}
		if connection.Name == "" {
			setStorageWaiting(tenant, "ConfigurationUnavailable", fmt.Sprintf("Cannot resolve the registered name of backend %q", id))
			return false
		}
		var secrets corev1.SecretList
		if err := r.APIReader.List(ctx, &secrets, client.InNamespace(storagePreparationNamespace()), client.MatchingLabels{
			storagePreparationBackendLabel: connection.Name, osacTenantKey: tenant.Name,
		}); err != nil {
			setStorageWaiting(tenant, "PreparationUnavailable", fmt.Sprintf("Cannot read preparation for backend %q; check operator access to the preparation namespace", connection.Name))
			return false
		}
		if len(secrets.Items) == 0 {
			setStorageWaiting(tenant, "PreparationMissing", fmt.Sprintf("Waiting for a tenant preparation Secret for backend %q in namespace %q", connection.Name, storagePreparationNamespace()))
			return false
		}
		if len(secrets.Items) != 1 {
			setStorageWaiting(tenant, "PreparationAmbiguous", fmt.Sprintf("Multiple preparation Secrets match this Tenant and backend %q; keep exactly one", connection.Name))
			return false
		}
		s := &secrets.Items[0]
		if !s.DeletionTimestamp.IsZero() || s.Type != corev1.SecretTypeOpaque ||
			strings.TrimSpace(string(s.Data[preparationUsernameKey])) == "" || strings.TrimSpace(string(s.Data[preparationPasswordKey])) == "" || strings.TrimSpace(string(s.Data["svm"])) == "" {
			setStorageWaiting(tenant, "PreparationInvalid", fmt.Sprintf("Preparation for backend %q must be a non-deleting Opaque Secret with non-empty username, password and svm", connection.Name))
			return false
		}
		connection.TenantPreparation = &provisioning.TenantPreparation{
			TenantID: tenant.Name, Namespace: s.Namespace, Name: s.Name, UID: string(s.UID), ResourceVersion: s.ResourceVersion,
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
	type source struct {
		BackendID, UID, SVM string
		ProviderConfig      map[string]any
	}
	sources := make([]source, 0)
	connections := provisioning.StorageBackendConnectionsFromContext(ctx)
	for _, id := range ontapBackendIDs(tiers) {
		if p := connections[id].TenantPreparation; p != nil {
			svm, _ := p.ProviderConfig["svm"].(string)
			sources = append(sources, source{BackendID: id, UID: p.UID, SVM: svm})
		} else if config := connections[id].ProviderConfig; len(config) > 0 {
			sources = append(sources, source{BackendID: id, ProviderConfig: config})
		}
	}
	version, _ := provisioning.ComputeDesiredConfigVersion(struct {
		TenantID   string
		Generation int64
		Tiers      []provisioning.TierDefinition
		Sources    []source
	}{tenant.Name, tenant.Generation, tiers, sources})
	return version
}

func (r *StorageReconciler) handlePreparedClusterStorage(ctx context.Context, tenant *v1alpha1.Tenant) (ctrl.Result, bool, error) {
	if storageDesiredConfigVersion(ctx, tenant) == "" || preparedStorageApplied(tenant.Status.ClusterStorageJobs, storageDesiredConfigVersion(ctx, tenant)) {
		return ctrl.Result{}, false, nil
	}
	setTenantStorageCondition(tenant, v1alpha1.TenantConditionClusterStorageReady, metav1.ConditionFalse, "ClusterSetupPending", "Waiting for native storage configuration to complete")
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

// Allow class discovery before reapplying a successful job. Use persisted launch
// times so restarts and frequent events cannot reset the cooldown. Like failed
// provisioning retries, repeated successes without outputs double the previous
// launch interval, capped at the existing maximum. A new config starts afresh.
func storageClassRepairDelay(jobs []v1alpha1.JobStatus, version string, now time.Time) time.Duration {
	var latest, previous *v1alpha1.JobStatus
	for i := len(jobs) - 1; i >= 0; i-- {
		job := &jobs[i]
		if job.Type != v1alpha1.JobTypeProvision || job.Target != "" || job.ConfigVersion != version || job.State != v1alpha1.JobStateSucceeded {
			continue
		}
		if latest == nil {
			latest = job
		} else {
			previous = job
			break
		}
	}
	if latest == nil {
		return 0
	}
	delay := provisioning.BackoffBaseDelay
	if previous != nil {
		gap := latest.Timestamp.Sub(previous.Timestamp.Time)
		if gap >= provisioning.BackoffMaxDelay/2 {
			delay = provisioning.BackoffMaxDelay
		} else if gap > delay/2 {
			delay = gap * 2
		}
	}
	elapsed := max(time.Duration(0), now.Sub(latest.Timestamp.Time))
	return max(time.Duration(0), delay-elapsed)
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
	return ctrl.Result{RequeueAfter: r.StatusPollInterval}
}
