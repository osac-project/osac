// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package cluster

import (
	"context"
	"log/slog"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	assistedv1 "github.com/openshift/assisted-service/api/v1beta1"
	conditionsv1 "github.com/openshift/custom-resource-status/conditions/v1"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	osacv1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	scenario "github.com/osac-project/osac/tests/integration/caas/internal/simulator/scenario"
)

var _ = Describe("Worker installation and readiness", Label("caas-it", "lifecycle"), func() {
	var s *clusterFixture

	Context("With failed installation", Label("negative"), func() {
		BeforeEach(func(ctx SpecContext) {
			s = newClusterFixture(ctx, scenario.InstallationFailed, defaultGroup)
		}, NodeTimeout(5*time.Minute))
		It("keeps the cluster non-ready when its workers fail to install", func(ctx SpecContext) {
			By("requesting a cluster and waiting for its workers to be claimed")
			Expect(s.createCluster(ctx)).To(Succeed())
			s.awaitProvisioningJob(ctx)
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
			s = newClusterFixture(ctx, scenario.ControlPlaneStalled, defaultGroup)
		}, NodeTimeout(5*time.Minute))
		It("waits for control-plane availability after workers are installed", func(ctx SpecContext) {
			By("requesting a cluster and waiting for its provisioning job")
			Expect(s.createCluster(ctx)).To(Succeed())
			s.awaitProvisioningJob(ctx)
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
			s = newClusterFixture(ctx, scenario.InstallationStalled, defaultGroup)
		}, NodeTimeout(5*time.Minute))
		It("waits for worker installation", func(ctx SpecContext) {
			By("requesting a cluster and waiting for its workers to be claimed")
			Expect(s.createCluster(ctx)).To(Succeed())
			s.awaitProvisioningJob(ctx)
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

// awaitInstalledWorkers waits for Agent installation and reported capacity within the caller's budget.
func (s *clusterFixture) awaitInstalledWorkers(ctx context.Context) {
	GinkgoHelper()

	for _, worker := range s.workers {
		Eventually(
			func(g Gomega) { checkAgentInstalled(g, s.currentAgent(ctx, g, worker)) },
		).WithContext(ctx).
			Should(Succeed())
	}

	Eventually(
		func(g Gomega) { s.checkNodePoolCapacity(ctx, g, int32(len(s.workers))) },
	).WithContext(ctx).
		Should(Succeed())
}

// checkAgentInstalled checks installation conditions on an already observed Agent.
func checkAgentInstalled(g Gomega, current *assistedv1.Agent) {
	GinkgoHelper()

	for _, kind := range []conditionsv1.ConditionType{"Installed", "Bound"} {
		g.Expect(conditionsv1.IsStatusConditionTrue(current.Status.Conditions, kind)).
			To(BeTrue(), "Agent must report %s", kind)
	}
}

// checkNodePoolCapacity checks the original pool's independently published replica count.
func (s *clusterFixture) checkNodePoolCapacity(ctx context.Context, g Gomega, want int32) {
	GinkgoHelper()
	pool := &hypershiftv1.NodePool{}
	g.Expect(environment.Client.Get(ctx, client.ObjectKeyFromObject(s.nodePool), pool)).To(Succeed())
	g.Expect(pool.UID).To(Equal(s.nodePool.UID))
	g.Expect(pool.Status.Conditions).To(ContainElement(And(
		HaveField("Type", "Ready"), HaveField("ObservedGeneration", pool.Generation),
		Not(HaveField("Status", corev1.ConditionUnknown)),
	)), "NodePool must publish its capacity")
	replicas := pool.Status.Replicas
	slog.DebugContext(
		ctx,
		"observed native worker capacity",
		"order",
		s.order.Name,
		"nodePool",
		pool.GetName(),
		"uid",
		pool.GetUID(),
		"available",
		replicas,
		"expected",
		want,
	)
	g.Expect(replicas).To(Equal(want))
}

// awaitAgentState waits for installation progress within the caller's observation budget.
func (s *clusterFixture) awaitAgentState(ctx context.Context, want string) {
	GinkgoHelper()

	for _, worker := range s.workers {
		Eventually(
			func(g Gomega) { checkAgentState(g, s.currentAgent(ctx, g, worker), want) },
		).WithContext(ctx).
			Should(Succeed())
	}
}

// checkAgentState checks installation progress on an already observed Agent.
func checkAgentState(g Gomega, current *assistedv1.Agent, want string) {
	GinkgoHelper()
	state := current.Status.DebugInfo.State
	slog.Debug(
		"observed worker installation state",
		"agent",
		current.GetName(),
		"uid",
		current.GetUID(),
		"state",
		state,
		"expected",
		want,
	)
	g.Expect(state).To(Equal(want))
}

// checkWorkerFeedback checks independent OSAC and Fulfillment readiness observations.
func (s *clusterFixture) checkWorkerFeedback(
	ctx context.Context,
	g Gomega,
	ready int32,
	controlPlaneStatus metav1.ConditionStatus,
) {
	GinkgoHelper()
	order, err := getClusterOrder(ctx, environment, s.order.Name)
	g.Expect(err).NotTo(HaveOccurred())
	slog.DebugContext(
		ctx,
		"observed application worker feedback",
		"order",
		order.Name,
		"uid",
		order.UID,
		"phase",
		order.Status.Phase,
		"readyWorkers",
		order.Status.ReadyWorkers,
		"expectedReadyWorkers",
		ready,
		"expectedControlPlaneAvailable",
		controlPlaneStatus,
	)
	g.Expect(order.Status.Phase).NotTo(Equal(osacv1.ClusterOrderPhaseReady))
	g.Expect(order.Status.ReadyWorkers).NotTo(BeNil())
	g.Expect(*order.Status.ReadyWorkers).To(Equal(ready))

	condition := apimeta.FindStatusCondition(order.Status.Conditions, "ControlPlaneAvailable")
	g.Expect(condition).NotTo(BeNil())
	g.Expect(condition.Status).To(Equal(controlPlaneStatus))
	s.checkFulfillmentNotReady(ctx, g)
}

// checkFulfillmentNotReady checks API feedback independently of provider status.
func (s *clusterFixture) checkFulfillmentNotReady(ctx context.Context, g Gomega) {
	GinkgoHelper()
	response, err := connectionClusters.Get(ctx, publicv1.ClustersGetRequest_builder{Id: s.clusterID}.Build())
	g.Expect(err).NotTo(HaveOccurred())
	slog.DebugContext(
		ctx,
		"observed non-ready Fulfillment feedback",
		"cluster",
		s.clusterID,
		"state",
		response.GetObject().GetStatus().GetState().String(),
	)
	g.Expect(response.GetObject().GetStatus().GetState()).NotTo(Equal(publicv1.ClusterState_CLUSTER_STATE_READY))
}

// awaitFailedInstallation observes terminal host errors and non-ready feedback.
func (s *clusterFixture) awaitFailedInstallation(ctx context.Context) {
	GinkgoHelper()
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	s.awaitAgentState(ctx, "error")
	Eventually(
		func(g Gomega) { s.checkWorkerFeedback(ctx, g, 0, metav1.ConditionTrue) },
	).WithContext(ctx).
		Should(Succeed())
}

// checkFailedInstallation monitors the established installation error without promising a terminal API failure.
func (s *clusterFixture) checkFailedInstallation(ctx context.Context, g Gomega) {
	GinkgoHelper()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	s.checkWorkerFeedback(ctx, g, 0, metav1.ConditionTrue)

	for _, worker := range s.workers {
		checkAgentState(g, s.currentAgent(ctx, g, worker), "error")
	}
}

// awaitInstallationStalled observes an installing host before checking held feedback.
func (s *clusterFixture) awaitInstallationStalled(ctx context.Context) {
	GinkgoHelper()
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	s.awaitAgentState(ctx, "installing")
	Eventually(func(g Gomega) { s.checkInstallationStalled(ctx, g) }).WithContext(ctx).Should(Succeed())
}

// checkInstallationStalled requires zero installed capacity and non-ready API feedback.
func (s *clusterFixture) checkInstallationStalled(ctx context.Context, g Gomega) {
	GinkgoHelper()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	s.checkWorkerFeedback(ctx, g, 0, metav1.ConditionTrue)
	s.checkNodePoolCapacity(ctx, g, 0)

	for _, worker := range s.workers {
		checkAgentState(g, s.currentAgent(ctx, g, worker), "installing")
	}
}

// awaitControlPlaneStalled establishes worker installation before observing unavailable control-plane feedback.
func (s *clusterFixture) awaitControlPlaneStalled(ctx context.Context) {
	GinkgoHelper()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	s.awaitInstalledWorkers(ctx)
	Eventually(func(g Gomega) { s.checkControlPlaneStalled(ctx, g) }).WithContext(ctx).Should(Succeed())
}

// checkControlPlaneStalled requires installed capacity before control-plane availability is first reached.
func (s *clusterFixture) checkControlPlaneStalled(ctx context.Context, g Gomega) {
	GinkgoHelper()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	s.checkWorkerFeedback(ctx, g, int32(len(s.workers)), metav1.ConditionFalse)
	s.checkInstalledWorkersWithUnavailableControlPlane(ctx, g)
}

// checkControlPlaneRegression checks live readiness loss while preserving reached provisioning milestones.
func (s *clusterFixture) checkControlPlaneRegression(ctx context.Context, g Gomega) {
	GinkgoHelper()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	order, err := getClusterOrder(ctx, environment, s.order.Name)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(order.Status.Phase).To(Equal(osacv1.ClusterOrderPhaseProgressing))
	// ControlPlaneAvailable records a reached milestone; phase and Progressing describe current readiness.
	g.Expect(apimeta.IsStatusConditionTrue(order.Status.Conditions, osacv1.ConditionControlPlaneAvailable)).To(BeTrue())
	g.Expect(order.Status.Conditions).To(ContainElement(And(
		HaveField("Type", osacv1.ConditionProgressing),
		HaveField("Status", metav1.ConditionTrue),
		HaveField("Reason", osacv1.ReasonControlPlaneStarting),
	)))
	g.Expect(order.Status.ReadyWorkers).NotTo(BeNil())
	g.Expect(*order.Status.ReadyWorkers).To(Equal(int32(len(s.workers))))
	s.checkWorkerIdentities(ctx, g, order)
	s.checkInstalledWorkersWithUnavailableControlPlane(ctx, g)
	s.checkConsumerHandoff(ctx, g)

	response, err := connectionClusters.Get(ctx, publicv1.ClustersGetRequest_builder{Id: s.clusterID}.Build())
	g.Expect(err).NotTo(HaveOccurred())
	status := response.GetObject().GetStatus()
	g.Expect(status.GetState()).To(Equal(publicv1.ClusterState_CLUSTER_STATE_PROGRESSING))

	var ready *publicv1.ClusterCondition

	for _, condition := range status.GetConditions() {
		if condition.GetType() == publicv1.ClusterConditionType_CLUSTER_CONDITION_TYPE_READY {
			ready = condition
			break
		}
	}

	g.Expect(ready).NotTo(BeNil(), "Fulfillment must retain an explicit readiness condition after becoming ready")
	g.Expect(ready.GetStatus()).To(Equal(publicv1.ConditionStatus_CONDITION_STATUS_FALSE))
}

// checkInstalledWorkersWithUnavailableControlPlane checks original workers and
// reported capacity while native control-plane availability is false.
func (s *clusterFixture) checkInstalledWorkersWithUnavailableControlPlane(ctx context.Context, g Gomega) {
	GinkgoHelper()

	for _, worker := range s.workers {
		checkAgentInstalled(g, s.currentAgent(ctx, g, worker))
	}

	s.checkNodePoolCapacity(ctx, g, int32(len(s.workers)))
	cluster := &hypershiftv1.HostedCluster{}
	g.Expect(environment.Client.Get(ctx, client.ObjectKeyFromObject(s.hostedCluster), cluster)).To(Succeed())
	g.Expect(cluster.UID).To(Equal(s.hostedCluster.UID))
	g.Expect(cluster.Status.Conditions).To(ContainElement(And(
		HaveField("Type", "Available"), HaveField("Status", metav1.ConditionFalse),
		HaveField("ObservedGeneration", cluster.Generation),
	)))
}

// awaitReady waits for installed capacity and independent application/API feedback.
func (s *clusterFixture) awaitReady(ctx context.Context) {
	GinkgoHelper()
	ready, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	s.awaitInstalledWorkers(ready)
	By("waiting for ready OSAC feedback with the original worker identities")
	Eventually(func(g Gomega) {
		order, err := getClusterOrder(ready, environment, s.order.Name)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(order.Status.Phase).To(Equal(osacv1.ClusterOrderPhaseReady))
		g.Expect(apimeta.IsStatusConditionTrue(order.Status.Conditions, "ControlPlaneAvailable")).To(BeTrue())
		g.Expect(order.Status.ReadyWorkers).NotTo(BeNil())
		g.Expect(*order.Status.ReadyWorkers).To(Equal(int32(len(s.workers))))
		s.checkWorkerIdentities(ready, g, order)
		s.checkConsumerHandoff(ready, g)
	}).WithContext(ready).Should(Succeed())
	By("waiting for Fulfillment to publish the ready cluster state")
	Eventually(func() (publicv1.ClusterState, error) {
		response, err := connectionClusters.Get(ready, publicv1.ClustersGetRequest_builder{Id: s.clusterID}.Build())
		return response.GetObject().GetStatus().GetState(), err
	}).WithContext(ready).Should(Equal(publicv1.ClusterState_CLUSTER_STATE_READY))
	slog.InfoContext(
		ctx,
		"cluster ready with original worker identities",
		"cluster",
		s.clusterID,
		"order",
		s.order.Name,
		"orderUID",
		s.order.UID,
		"workers",
		len(s.workers),
	)
	AddReportEntry(
		"Provisioning identity",
		map[string]any{"cluster": s.clusterID, "order": s.order.Name, "orderUID": s.order.UID},
	)
}
