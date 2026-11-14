// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package cluster

import (
	"context"
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	installv1 "github.com/openshift/assisted-service/api/hiveextension/v1beta1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	scenario "github.com/osac-project/osac/tests/integration/caas/internal/simulator/scenario"
)

var _ = Describe("Cluster provisioning through installed providers", Label("caas-it", "lifecycle"), func() {
	var s *clusterFixture

	Context("With healthy providers", Label("positive"), func() {
		BeforeEach(func(ctx SpecContext) {
			s = newClusterFixture(ctx, scenario.Normal, defaultGroup)
		}, NodeTimeout(5*time.Minute))
		It("provisions and deletes", func(ctx SpecContext) {
			By("reading the discovery data that the workers should receive")
			ignition, err := s.ignition.Read(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(json.Valid(ignition)).To(BeTrue())

			By("requesting a cluster with two workers")
			Expect(s.createCluster(ctx)).To(Succeed())
			s.awaitProvisioningJob(ctx)
			s.awaitWorkerInputs(ctx)
			s.verifyInstallationRecovery(ctx)

			By("waiting for worker allocation, approval and consumer handoff")
			s.awaitWorkerHandoff(ctx)
			s.awaitProvisioningSuccess(ctx)

			By("waiting for the cluster to become ready through Fulfillment")
			s.awaitReady(ctx)

			By("checking the exact discovery data delivered to both allocated hosts")
			s.verifyUserData(ctx, ignition)

			By("deleting through Fulfillment and verifying jobs, removal and released inventory")
			s.deleteClusterAndVerify(ctx)
		}, SpecTimeout(30*time.Minute))
	})
})

// verifyInstallationRecovery recovers installation inputs before host registration.
// Assisted unbinds Agents when an active installation is deleted.
func (s *clusterFixture) verifyInstallationRecovery(ctx context.Context) {
	GinkgoHelper()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	By("observing the installed provider record and its owning deployment")
	namespace := s.hostedCluster.GetNamespace() + "-" + s.hostedCluster.GetName()

	var installations installv1.AgentClusterInstallList

	Eventually(func(g Gomega) {
		g.Expect(environment.Client.List(ctx, &installations, client.InNamespace(namespace))).To(Succeed())
		g.Expect(installations.Items).To(HaveLen(1))
		g.Expect(installations.Items[0].OwnerReferences).To(HaveLen(1))
	}).WithContext(ctx).Should(Succeed())
	installation := installations.Items[0]
	owner := installation.OwnerReferences[0]
	Expect(owner.Controller).NotTo(Equal(new(true)))
	Expect(owner.APIVersion).To(Equal("hive.openshift.io/v1"))
	Expect(owner.Kind).To(Equal("ClusterDeployment"))
	deployment := &hivev1.ClusterDeployment{}
	Expect(environment.Client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: owner.Name}, deployment)).To(Succeed())
	Expect(deployment.GetUID()).To(Equal(owner.UID))
	uid := installation.GetUID()
	Expect(uid).NotTo(BeEmpty())

	By("deleting only the original installation and awaiting controller-driven recovery")
	Expect(environment.Client.Delete(ctx, &installation, client.Preconditions{UID: &uid})).To(Succeed())
	Eventually(func(g Gomega) {
		current := &installv1.AgentClusterInstall{}
		g.Expect(environment.Client.Get(ctx, client.ObjectKeyFromObject(&installation), current)).To(Succeed())
		g.Expect(current.GetUID()).NotTo(BeEmpty())
		g.Expect(current.GetUID()).NotTo(Equal(uid))
		g.Expect(current.OwnerReferences).To(ConsistOf(owner))
	}).WithContext(ctx).Should(Succeed())

	By("retaining the original deployment through installation recovery")
	Expect(environment.Client.Get(ctx, client.ObjectKeyFromObject(deployment), deployment)).To(Succeed())
	Expect(deployment.UID).To(Equal(owner.UID))
}
