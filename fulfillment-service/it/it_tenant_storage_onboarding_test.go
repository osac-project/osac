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
	apiMeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("Tenant storage onboarding feedback", func() {
	It("projects and retains operator storage conditions through signaling and reads", func(ctx context.Context) {
		const conditionType = privatev1.TenantConditionType_TENANT_CONDITION_TYPE_STORAGE_ONBOARDING_COMPLETE
		tenants := privatev1.NewTenantsClient(tool.InternalView().AdminConn())
		projects := privatev1.NewProjectsClient(tool.InternalView().AdminConn())
		name := fmt.Sprintf("test-storage-%s", uuid.New())
		id := createTenant(ctx, tenants, name)
		DeferCleanup(func(cleanupCtx context.Context) { deleteTenant(cleanupCtx, tenants, projects, id, name) })
		waitForTenantSynced(ctx, tenants, id)
		key := crclient.ObjectKey{Namespace: hubNamespace, Name: name}

		// The fixture supplies operator status; provider workflows and automatic feedback are outside this boundary.
		expectCondition := func(status privatev1.ConditionStatus, reason, message string, transition time.Time, computeReason string) {
			Eventually(func(g Gomega) {
				response, err := tenants.Get(ctx, privatev1.TenantsGetRequest_builder{Id: id}.Build())
				g.Expect(err).NotTo(HaveOccurred())
				object := response.GetObject()
				g.Expect(object.GetStatus().GetState()).To(Equal(privatev1.TenantState_TENANT_STATE_SYNCED))
				c := findTenantCondition(object.GetStatus().GetConditions(), conditionType)
				g.Expect(c).NotTo(BeNil())
				g.Expect(c.GetStatus()).To(Equal(status))
				g.Expect(c.GetReason()).To(Equal(reason))
				if message != "" {
					g.Expect(c.GetMessage()).To(Equal(message))
				}
				if !transition.IsZero() {
					g.Expect(c.GetLastTransitionTime()).NotTo(BeNil())
					g.Expect(c.GetLastTransitionTime().AsTime()).To(Equal(transition))
				}
				vault := findTenantCondition(object.GetStatus().GetConditions(), privatev1.TenantConditionType_TENANT_CONDITION_TYPE_VAULT_READY)
				g.Expect(vault).NotTo(BeNil())
				g.Expect(vault.GetStatus()).To(Equal(privatev1.ConditionStatus_CONDITION_STATUS_TRUE))
				if computeReason != "" {
					compute := findTenantCondition(object.GetStatus().GetConditions(), privatev1.TenantConditionType_TENANT_CONDITION_TYPE_COMPUTE_INFRASTRUCTURE_READY)
					g.Expect(compute).NotTo(BeNil())
					g.Expect(compute.GetReason()).To(Equal(computeReason))
				}
			}, time.Minute, time.Second).Should(Succeed())
		}

		By("Keeping absent operator storage evidence incomplete")
		expectCondition(privatev1.ConditionStatus_CONDITION_STATUS_FALSE, "StorageOnboardingPending", "", time.Time{}, "")

		cases := []struct {
			source metav1.ConditionStatus
			want   privatev1.ConditionStatus
		}{
			{metav1.ConditionFalse, privatev1.ConditionStatus_CONDITION_STATUS_FALSE},
			{metav1.ConditionUnknown, privatev1.ConditionStatus_CONDITION_STATUS_UNSPECIFIED},
			{metav1.ConditionTrue, privatev1.ConditionStatus_CONDITION_STATUS_TRUE},
		}
		for i, test := range cases {
			By(fmt.Sprintf("Projecting operator condition %s", test.source))
			reason := fmt.Sprintf("Operator%s", test.source)
			message := fmt.Sprintf("Fixture reports storage onboarding %s", test.source)
			transition := time.Now().UTC().Truncate(time.Second).Add(time.Duration(i-3) * time.Hour)
			Eventually(func(g Gomega) {
				object := &osacv1alpha1.Tenant{}
				g.Expect(tool.KubeClient().Get(ctx, key, object)).To(Succeed())
				object.Status.Phase = osacv1alpha1.TenantPhaseReady
				apiMeta.SetStatusCondition(&object.Status.Conditions, metav1.Condition{
					Type: "StorageOnboardingComplete", Status: test.source,
					Reason: reason, Message: message, LastTransitionTime: metav1.NewTime(transition),
				})
				g.Expect(tool.KubeClient().Status().Update(ctx, object)).To(Succeed())
			}, time.Minute, time.Second).Should(Succeed())
			_, err := tenants.Signal(ctx, privatev1.TenantsSignalRequest_builder{Id: id}.Build())
			Expect(err).NotTo(HaveOccurred())
			expectCondition(test.want, reason, message, transition, "InfrastructureReady")

			By("Reading the persisted condition again through a fresh API request")
			expectCondition(test.want, reason, message, transition, "InfrastructureReady")

			By("Retaining the storage condition through another confirmed hub observation")
			Eventually(func(g Gomega) {
				object := &osacv1alpha1.Tenant{}
				g.Expect(tool.KubeClient().Get(ctx, key, object)).To(Succeed())
				object.Status.Phase = osacv1alpha1.TenantPhaseProgressing
				g.Expect(tool.KubeClient().Status().Update(ctx, object)).To(Succeed())
			}, time.Minute, time.Second).Should(Succeed())
			_, err = tenants.Signal(ctx, privatev1.TenantsSignalRequest_builder{Id: id}.Build())
			Expect(err).NotTo(HaveOccurred())
			// A changed compute reason proves the later reconciliation ran, rather than reading the previous result.
			expectCondition(test.want, reason, message, transition, "InfrastructurePending")
		}
	})
})
