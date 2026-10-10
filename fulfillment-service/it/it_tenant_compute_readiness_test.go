/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Tenant compute readiness feedback", func() {
	It("reports ready after Tenant status feedback", func(ctx context.Context) {
		tenants := privatev1.NewTenantsClient(tool.InternalView().AdminConn())
		projects := privatev1.NewProjectsClient(tool.InternalView().AdminConn())
		name := fmt.Sprintf("test-compute-%s", uuid.New())
		id := createTenant(ctx, tenants, name)
		DeferCleanup(func(cleanupCtx context.Context) { deleteTenant(cleanupCtx, tenants, projects, id, name) })

		waitForTenantSynced(ctx, tenants, id)

		key := crclient.ObjectKey{Namespace: hubNamespace, Name: name}
		Eventually(func(g Gomega) {
			object := &osacv1alpha1.Tenant{}
			g.Expect(tool.KubeClient().Get(ctx, key, object)).To(Succeed())
			g.Expect(object.Labels).To(HaveKeyWithValue(labels.TenantUuid, name))
			g.Expect(object.Labels).To(HaveKeyWithValue(labels.TenantID, id))
		}, time.Minute, time.Second).Should(Succeed())

		By("Marking the Tenant CR ready and signaling the status feedback")
		Eventually(func(g Gomega) {
			object := &osacv1alpha1.Tenant{}
			g.Expect(tool.KubeClient().Get(ctx, key, object)).To(Succeed())
			object.Status.Phase = osacv1alpha1.TenantPhaseReady
			g.Expect(tool.KubeClient().Status().Update(ctx, object)).To(Succeed())
		}, time.Minute, time.Second).Should(Succeed())
		_, err := tenants.Signal(ctx, privatev1.TenantsSignalRequest_builder{Id: id}.Build())
		Expect(err).NotTo(HaveOccurred())

		expectComputeCondition := func(want privatev1.ConditionStatus, reason string, timeout time.Duration) {
			Eventually(func(g Gomega) {
				response, err := tenants.Get(ctx, privatev1.TenantsGetRequest_builder{Id: id}.Build())
				g.Expect(err).NotTo(HaveOccurred())
				object := response.GetObject()
				g.Expect(object.GetStatus().GetState()).To(Equal(privatev1.TenantState_TENANT_STATE_SYNCED))

				compute := findTenantCondition(object.GetStatus().GetConditions(), privatev1.TenantConditionType_TENANT_CONDITION_TYPE_COMPUTE_INFRASTRUCTURE_READY)
				g.Expect(compute).NotTo(BeNil())
				g.Expect(compute.GetStatus()).To(Equal(want))
				g.Expect(compute.GetReason()).To(Equal(reason))

				vault := findTenantCondition(object.GetStatus().GetConditions(), privatev1.TenantConditionType_TENANT_CONDITION_TYPE_VAULT_READY)
				g.Expect(vault).NotTo(BeNil())
				g.Expect(vault.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))

				network := findTenantCondition(object.GetStatus().GetConditions(), privatev1.TenantConditionType_TENANT_CONDITION_TYPE_DEFAULT_NETWORKING_READY)
				g.Expect(network).NotTo(BeNil())
			}, timeout, time.Second).Should(Succeed())
		}

		expectComputeCondition(privatev1.ConditionStatus_CONDITION_STATUS_TRUE, "InfrastructureReady", 25*time.Second)

		By("Creating an ambiguous observation to establish a non-ready baseline")
		duplicate := &osacv1alpha1.Tenant{ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-duplicate",
			Namespace: hubNamespace,
			Labels: map[string]string{
				labels.TenantUuid: name,
				labels.TenantID:   id,
			},
			Annotations: map[string]string{
				"osac.openshift.io/tenant":          name,
				"osac.openshift.io/owner-reference": id,
			},
		}}
		Expect(tool.KubeClient().Create(ctx, duplicate)).To(Succeed())
		DeferCleanup(func(cleanupCtx context.Context) {
			_ = tool.KubeClient().Delete(cleanupCtx, duplicate)
		})
		_, err = tenants.Signal(ctx, privatev1.TenantsSignalRequest_builder{Id: id}.Build())
		Expect(err).NotTo(HaveOccurred())
		expectComputeCondition(privatev1.ConditionStatus_CONDITION_STATUS_UNSPECIFIED, "InfrastructureStatusUnknown", 15*time.Second)

		By("Removing the ambiguity and changing Tenant status to trigger feedback")
		Eventually(func(g Gomega) {
			g.Expect(tool.KubeClient().Get(ctx, crclient.ObjectKeyFromObject(duplicate), duplicate)).To(Succeed())
			duplicate.Labels[labels.TenantID] = "unrelated-tenant-id"
			g.Expect(tool.KubeClient().Update(ctx, duplicate)).To(Succeed())
		}, time.Minute, time.Second).Should(Succeed())

		Eventually(func(g Gomega) {
			object := &osacv1alpha1.Tenant{}
			g.Expect(tool.KubeClient().Get(ctx, key, object)).To(Succeed())
			object.Status.Conditions = append(object.Status.Conditions, metav1.Condition{
				Type:               "FeedbackTest",
				Status:             metav1.ConditionTrue,
				Reason:             "ControlledTransition",
				Message:            "Trigger tenant readiness feedback",
				LastTransitionTime: metav1.Now(),
			})
			g.Expect(tool.KubeClient().Status().Update(ctx, object)).To(Succeed())
		}, time.Minute, time.Second).Should(Succeed())
		_, err = tenants.Signal(ctx, privatev1.TenantsSignalRequest_builder{Id: id}.Build())
		Expect(err).NotTo(HaveOccurred())

		expectComputeCondition(privatev1.ConditionStatus_CONDITION_STATUS_TRUE, "InfrastructureReady", 15*time.Second)
	})
})
