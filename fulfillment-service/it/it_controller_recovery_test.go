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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// Stopping the shared controller must not overlap other integration specs.
var _ = Describe("Controller recovery", Serial, func() {
	It("reconciles a tenant created while the controller is stopped", func(ctx context.Context) {
		kubeClient := tool.KubeClient()
		deploymentKey := crclient.ObjectKey{Namespace: "osac", Name: "fulfillment-controller"}
		deployment := &appsv1.Deployment{}
		Expect(kubeClient.Get(ctx, deploymentKey, deployment)).To(Succeed())
		Expect(deployment.Spec.Template.Spec.Containers).To(ContainElement(
			HaveField("Command", ContainElement("--sync=false")),
		), "disable background sync so recovery exercises grouped event delivery")
		Expect(deployment.Spec.Replicas).ToNot(BeNil())
		originalReplicas := *deployment.Spec.Replicas
		Expect(originalReplicas).To(BeNumerically(">", 0))
		selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
		Expect(err).ToNot(HaveOccurred())

		expectNoControllerPods := func(ctx context.Context, g Gomega) {
			pods := &corev1.PodList{}
			g.Expect(kubeClient.List(ctx, pods,
				crclient.InNamespace(deploymentKey.Namespace), crclient.MatchingLabelsSelector{Selector: selector},
			)).To(Succeed())
			// Include terminating pods: they can still process events until they exit.
			g.Expect(pods.Items).To(BeEmpty())
		}

		scaleController := func(ctx context.Context, replicas int32) {
			GinkgoHelper()
			current := &appsv1.Deployment{}
			Expect(kubeClient.Get(ctx, deploymentKey, current)).To(Succeed())
			original := current.DeepCopy()
			current.Spec.Replicas = new(replicas)
			Expect(kubeClient.Patch(ctx, current, crclient.MergeFrom(original))).To(Succeed())
			Eventually(ctx, func(g Gomega) {
				updated := &appsv1.Deployment{}
				g.Expect(kubeClient.Get(ctx, deploymentKey, updated)).To(Succeed())
				g.Expect(updated.Status.ObservedGeneration).To(BeNumerically(">=", updated.Generation))
				g.Expect(updated.Status.Replicas).To(Equal(replicas))
				g.Expect(updated.Status.ReadyReplicas).To(Equal(replicas))
				g.Expect(updated.Status.AvailableReplicas).To(Equal(replicas))
				if replicas == 0 {
					expectNoControllerPods(ctx, g)
				}
			}, 3*time.Minute, time.Second).Should(Succeed())
		}

		tenantsClient := privatev1.NewTenantsClient(tool.InternalView().AdminConn())
		projectsClient := privatev1.NewProjectsClient(tool.InternalView().AdminConn())
		name := fmt.Sprintf("test-%s", uuid.New())
		var id string
		// Register before stopping the controller, and use a fresh cleanup context.
		// Restore it before deleting the tenant because deletion needs reconciliation.
		DeferCleanup(func(ctx context.Context) {
			scaleController(ctx, originalReplicas)
			if id != "" {
				deleteTenant(ctx, tenantsClient, projectsClient, id, name)
			}
		})

		By("Scaling the controller to zero and waiting for all its pods to disappear")
		scaleController(ctx, 0)

		By("Creating a tenant to generate an event while the controller is stopped")
		response, err := tenantsClient.Create(ctx, privatev1.TenantsCreateRequest_builder{
			Object: privatev1.Tenant_builder{
				Metadata: privatev1.Metadata_builder{Name: name}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		id = response.GetObject().GetId()
		Expect(id).ToNot(BeEmpty())

		By("Verifying the tenant stays unsynchronized while no controller pods exist")
		Consistently(ctx, func(g Gomega) {
			expectNoControllerPods(ctx, g)
			response, err := tenantsClient.Get(ctx, privatev1.TenantsGetRequest_builder{Id: id}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(response.GetObject().GetStatus().GetState()).ToNot(Equal(privatev1.TenantState_TENANT_STATE_SYNCED))
		}, 5*time.Second, time.Second).Should(Succeed())

		By("Scaling the controller back to one replica")
		scaleController(ctx, 1)

		By("Waiting for the tenant to reach SYNCED without any further writes or signals")
		// Background sync is disabled, so recovery relies on event-driven reconciliation.
		waitForTenantSynced(ctx, tenantsClient, id)
	})
})
