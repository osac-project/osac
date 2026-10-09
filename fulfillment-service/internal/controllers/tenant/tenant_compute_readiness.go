/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package tenant

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clnt "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const (
	reasonInfrastructureReady          = "InfrastructureReady"
	reasonInfrastructurePending        = "InfrastructurePending"
	reasonInfrastructureFailed         = "InfrastructureFailed"
	reasonInfrastructureDeleting       = "InfrastructureDeleting"
	reasonInfrastructureStatusUnknown  = "InfrastructureStatusUnknown"
	reasonInfrastructureNotProvisioned = "InfrastructureNotProvisioned"
)

// checkInfrastructureReadiness observes compute and storage readiness on the hubs.
func (t *task) checkInfrastructureReadiness(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	const conditionType = privatev1.TenantConditionType_TENANT_CONDITION_TYPE_COMPUTE_INFRASTRUCTURE_READY
	if !t.tenant.HasStatus() {
		t.tenant.SetStatus(&privatev1.TenantStatus{})
	}
	tenantID := t.tenant.GetId()
	tenantName := t.tenant.GetMetadata().GetName()
	var observed, incomplete, pending, failed, deleting bool
	backendStorage := storageReadiness{sourceType: osacv1alpha1.TenantConditionStorageBackendReady}
	clusterStorage := storageReadiness{sourceType: osacv1alpha1.TenantConditionClusterStorageReady}
	reportError := func(_ error) {
		incomplete = true
		t.r.logger.ErrorContext(ctx, "Failed to observe tenant compute infrastructure")
	}
	if tenantID == "" || tenantName == "" {
		reportError(fmt.Errorf("tenant identity is incomplete"))
	} else {
		var offset int32
		for {
			response, err := t.r.hubsClient.List(ctx, privatev1.HubsListRequest_builder{Offset: &offset}.Build())
			if err != nil {
				reportError(err)
				break
			}
			for _, hub := range response.GetItems() {
				object, err := t.r.readTenantInfrastructure(ctx, hub.GetId(), tenantID, tenantName)
				if err != nil {
					reportError(err)
					continue
				}
				if object == nil {
					continue
				}
				observed = true
				backendStorage.observe(object)
				clusterStorage.observe(object)
				if !object.DeletionTimestamp.IsZero() {
					deleting = true
					continue
				}
				switch object.Status.Phase {
				case osacv1alpha1.TenantPhaseReady:
				case osacv1alpha1.TenantPhaseFailed:
					failed = true
				case osacv1alpha1.TenantPhaseDeleting:
					deleting = true
				default:
					pending = true
				}
			}
			count := response.GetSize()
			if count < 0 || int64(count) != int64(len(response.GetItems())) || count > response.GetTotal()-offset {
				reportError(fmt.Errorf("hub listing returned inconsistent pagination"))
				break
			}
			offset += count
			if offset >= response.GetTotal() {
				break
			}
			if count == 0 {
				reportError(fmt.Errorf("hub listing returned an empty incomplete page"))
				break
			}
		}
	}
	conditionStatus := privatev1.ConditionStatus_CONDITION_STATUS_FALSE
	var reason, message string
	switch {
	case failed:
		reason, message = reasonInfrastructureFailed, "Tenant compute infrastructure preparation failed"
	case deleting:
		reason, message = reasonInfrastructureDeleting, "Tenant compute infrastructure is being removed"
	case pending:
		reason, message = reasonInfrastructurePending, "Tenant compute infrastructure is still being prepared"
	case incomplete:
		conditionStatus = privatev1.ConditionStatus_CONDITION_STATUS_UNSPECIFIED
		reason, message = reasonInfrastructureStatusUnknown, "Tenant compute infrastructure readiness could not be determined"
	case observed:
		conditionStatus = privatev1.ConditionStatus_CONDITION_STATUS_TRUE
		reason, message = reasonInfrastructureReady, "Tenant compute infrastructure is ready on all participating hubs"
	default:
		reason, message = reasonInfrastructureNotProvisioned, "Tenant compute infrastructure has not been provisioned"
	}
	t.updateCondition(conditionType, conditionStatus, reason, message)
	backendStorage.project(t, privatev1.TenantConditionType_TENANT_CONDITION_TYPE_STORAGE_BACKEND_READY, incomplete)
	clusterStorage.project(t, privatev1.TenantConditionType_TENANT_CONDITION_TYPE_CLUSTER_STORAGE_READY, incomplete)
}

type storageReadiness struct {
	sourceType osacv1alpha1.TenantConditionType
	ready      *metav1.Condition
	failed     *metav1.Condition
	unknown    *metav1.Condition
	incomplete bool
}

func (s *storageReadiness) observe(object *osacv1alpha1.Tenant) {
	if !object.DeletionTimestamp.IsZero() || object.Status.Phase == osacv1alpha1.TenantPhaseDeleting {
		s.failed = &metav1.Condition{Reason: "StorageDeleting", Message: "Tenant storage configuration is being removed"}
		return
	}
	condition := object.GetStatusCondition(s.sourceType)
	if condition == nil || condition.ObservedGeneration != object.Generation {
		s.incomplete = true
		return
	}
	switch condition.Status {
	case metav1.ConditionTrue:
		if s.ready == nil {
			s.ready = condition
		}
	case metav1.ConditionFalse:
		if s.failed == nil {
			s.failed = condition
		}
	default:
		if s.unknown == nil {
			s.unknown = condition
		}
	}
}

func (s *storageReadiness) project(t *task, kind privatev1.TenantConditionType, observationIncomplete bool) {
	status := privatev1.ConditionStatus_CONDITION_STATUS_UNSPECIFIED
	reason, message := "StorageStatusUnknown", "Tenant storage readiness could not be determined"
	switch {
	case s.failed != nil:
		status = privatev1.ConditionStatus_CONDITION_STATUS_FALSE
		reason, message = s.failed.Reason, s.failed.Message
	case observationIncomplete || s.incomplete:
	case s.unknown != nil:
		reason, message = s.unknown.Reason, s.unknown.Message
	case s.ready != nil:
		status = privatev1.ConditionStatus_CONDITION_STATUS_TRUE
		reason, message = s.ready.Reason, s.ready.Message
	}
	t.updateCondition(kind, status, reason, message)
}

// readTenantInfrastructure finds the Tenant CR for a fulfillment tenant on one hub.
func (r *function) readTenantInfrastructure(ctx context.Context, hubID, tenantID, tenantName string) (*osacv1alpha1.Tenant, error) {
	entry, err := r.hubCache.Get(ctx, hubID)
	if err != nil {
		return nil, err
	}
	if entry == nil || entry.Client == nil || entry.Namespace == "" {
		return nil, fmt.Errorf("hub %q has no configured client or namespace", hubID)
	}
	objects := &osacv1alpha1.TenantList{}
	if err := entry.Client.List(ctx, objects, clnt.InNamespace(entry.Namespace), clnt.MatchingLabels{labels.TenantID: tenantID}); err != nil {
		return nil, err
	}
	if len(objects.Items) == 0 {
		return nil, nil
	}
	if len(objects.Items) != 1 || objects.Items[0].Name != tenantName {
		return nil, fmt.Errorf("hub %q has ambiguous or mismatched tenant identity", hubID)
	}
	return &objects.Items[0], nil
}
