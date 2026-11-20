// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package cluster

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"

	scenario "github.com/osac-project/osac/tests/integration/caas/internal/simulator/scenario"
)

var _ = Describe("Worker installation and readiness", Label("caas-it", "lifecycle"), func() {
	var s *clusterFixture

	Context("With failed installation", Label("negative"), func() {
		BeforeEach(func(ctx SpecContext) {
			s = newClusterFixture(ctx, scenario.InstallationFailed, defaultGroup, 1)
		}, NodeTimeout(5*time.Minute))
		It("keeps the cluster non-ready when its workers fail to install", func(ctx SpecContext) {
			By("requesting a cluster and waiting for its workers to be claimed")
			Expect(s.createCluster(ctx)).To(Succeed())
			s.awaitProvisioningJob(ctx)
			s.awaitWorkerInputs(ctx)
			s.awaitWorkerHandoff(ctx)
			s.awaitProvisioningSuccess(ctx)

			By("waiting for each worker's installation error")
			s.awaitFailedInstallation(ctx)

			By("requiring non-ready feedback while the provider reports installation errors")
			Consistently(
				func(g Gomega) { s.checkFailedInstallation(ctx, g) },
			).WithContext(ctx).
				WithTimeout(8 * time.Second).
				Should(Succeed())

			By("deleting the failed installation through Fulfillment")
			s.deleteClusterAndVerify(ctx)
		}, SpecTimeout(30*time.Minute))
	})

	Context("With stalled control plane", Label("negative"), func() {
		BeforeEach(func(ctx SpecContext) {
			s = newClusterFixture(ctx, scenario.ControlPlaneStalled, defaultGroup, 2)
		}, NodeTimeout(5*time.Minute))
		It("waits for control-plane availability after workers are installed", func(ctx SpecContext) {
			By("requesting a cluster and waiting for its provisioning job")
			Expect(s.createCluster(ctx)).To(Succeed())
			s.awaitProvisioningJob(ctx)
			s.awaitWorkerInputs(ctx)
			s.awaitWorkerHandoff(ctx)
			s.awaitProvisioningSuccess(ctx)

			By("waiting for installed workers and their independently reported capacity")
			s.awaitControlPlaneStalled(ctx)

			By("requiring the cluster to stay non-ready despite its installed workers")
			Consistently(
				func(g Gomega) { s.checkControlPlaneStalled(ctx, g) },
			).WithContext(ctx).
				WithTimeout(8 * time.Second).
				Should(Succeed())

			By("allowing the control plane to become available without replacing workers")
			Expect(s.settings.SetProfile(ctx, scenario.Normal)).To(Succeed())
			s.awaitReady(ctx)

			By("holding availability again after the control plane has become ready")
			namespace := s.hostedCluster.GetNamespace() + "-" + s.hostedCluster.GetName()
			controlPlane := &hypershiftv1.HostedControlPlane{}
			Expect(environment.Client.Get(ctx,
				client.ObjectKey{Namespace: namespace, Name: s.hostedCluster.Name}, controlPlane)).To(Succeed())
			Expect(s.settings.SetProfile(ctx, scenario.ControlPlaneStalled)).To(Succeed())
			Eventually(func(g Gomega) {
				current := &hypershiftv1.HostedControlPlane{}
				g.Expect(environment.Client.Get(ctx, client.ObjectKeyFromObject(controlPlane), current)).To(Succeed())
				g.Expect(current.UID).To(Equal(controlPlane.UID))
				g.Expect(current.Status.Ready).To(BeFalse())
				g.Expect(apimeta.IsStatusConditionFalse(current.Status.Conditions, "Available")).To(BeTrue())
			}).WithContext(ctx).WithTimeout(time.Minute).Should(Succeed())

			By("requiring OSAC and Fulfillment to leave Ready while availability is held")
			Eventually(func(g Gomega) {
				s.checkControlPlaneRegression(ctx, g)
			}).WithContext(ctx).WithTimeout(5 * time.Minute).Should(Succeed())

			By("restoring availability on the original control plane and workers")
			Expect(s.settings.SetProfile(ctx, scenario.Normal)).To(Succeed())
			s.awaitReady(ctx)
			current := &hypershiftv1.HostedControlPlane{}
			Expect(environment.Client.Get(ctx, client.ObjectKeyFromObject(controlPlane), current)).To(Succeed())
			Expect(current.GetUID()).To(Equal(controlPlane.GetUID()))

			By("deleting the recovered cluster through Fulfillment")
			s.deleteClusterAndVerify(ctx)
		}, SpecTimeout(30*time.Minute))
	})

	Context("With stalled installation", Label("negative"), func() {
		BeforeEach(func(ctx SpecContext) {
			s = newClusterFixture(ctx, scenario.InstallationStalled, defaultGroup, 2)
		}, NodeTimeout(5*time.Minute))
		It("waits for worker installation", func(ctx SpecContext) {
			By("requesting a cluster and waiting for its workers to be claimed")
			Expect(s.createCluster(ctx)).To(Succeed())
			s.awaitProvisioningJob(ctx)
			s.awaitWorkerInputs(ctx)
			s.awaitWorkerHandoff(ctx)
			s.awaitProvisioningSuccess(ctx)

			By("waiting for workers to start installation while the control plane is available")
			s.awaitInstallationStalled(ctx)

			By("requiring installation, capacity and cluster readiness to remain blocked")
			Consistently(
				func(g Gomega) { s.checkInstallationStalled(ctx, g) },
			).WithContext(ctx).
				WithTimeout(8 * time.Second).
				Should(Succeed())

			By("allowing installation to finish on the original allocations")
			Expect(s.settings.SetProfile(ctx, scenario.Normal)).To(Succeed())
			s.awaitReady(ctx)

			By("deleting the recovered cluster through Fulfillment")
			s.deleteClusterAndVerify(ctx)
		}, SpecTimeout(30*time.Minute))
	})
})
