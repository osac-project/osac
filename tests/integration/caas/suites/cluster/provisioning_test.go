// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package cluster

import (
	"context"
	"log/slog"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	bmfv1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	osacv1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/tests/integration/caas/suites/fixtures"
)

// awaitProvisioningJob captures the generated order after OSAC submits its job.
func (s *clusterFixture) awaitProvisioningJob(ctx context.Context) {
	GinkgoHelper()
	slog.DebugContext(ctx, "waiting for provisioning submission", "cluster", s.clusterID, "budget", 4*time.Minute)

	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()

	Eventually(func(g Gomega) {
		s.observeClusterOrder(ctx, g)
		g.Expect(s.order).NotTo(BeNil())
		g.Expect(s.order.Status.ProvisioningJobs).NotTo(BeEmpty())
		slog.DebugContext(
			ctx,
			"observed provisioning submission",
			"cluster",
			s.clusterID,
			"order",
			s.order.Name,
			"orderUID",
			s.order.UID,
			"jobs",
			len(s.order.Status.ProvisioningJobs),
		)
	}).WithContext(ctx).Should(Succeed(), "Fulfillment and OSAC must create the order and submit provisioning")
}

// awaitWorkerInputs follows real application references after scoped automation starts.
func (s *clusterFixture) awaitWorkerInputs(ctx context.Context) {
	GinkgoHelper()
	start, cancelStart := context.WithTimeout(ctx, clusterJobStartTimeout)
	defer cancelStart()

	fixtures.ExpectStartedJob(start, executions, s.order,
		"osac-aap/playbook_osac_create_hosted_cluster.yml", s.jobPods)
	cancelStart()
	slog.DebugContext(ctx, "waiting for native worker inputs", "order", s.order.Name, "budget", 5*time.Minute)

	outputs, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	By("binding each native provider root as it appears and waiting for worker inputs")
	Eventually(func(g Gomega) {
		foundCluster := s.observeInputs(outputs, g)
		g.Expect(s.hostedCluster).NotTo(BeNil())
		g.Expect(foundCluster).To(BeTrue(), "the bound CAPI Cluster must be present")
		g.Expect(s.infraEnv).NotTo(BeNil())
		g.Expect(s.nodePool).NotTo(BeNil())
		slog.DebugContext(
			ctx,
			"correlated native worker inputs",
			"order",
			s.order.Name,
			"orderUID",
			s.order.UID,
			"hostedCluster",
			s.hostedCluster.GetName(),
			"nodePool",
			s.nodePool.GetName(),
			"infraEnv",
			s.infraEnv.GetName(),
		)
	}).WithContext(outputs).Should(Succeed(), "real playbooks and OSAC must publish native inputs")
	AddReportEntry(
		"Native provider roots",
		map[string]string{
			"hostedCluster": string(s.hostedCluster.GetUID()),
			"infraEnv":      string(s.infraEnv.GetUID()),
			"settings":      s.settings.Name,
		},
	)
	Expect(s.hostedCluster.Spec.Release.Image).To(Equal(fixtures.ReleaseImage))
	Expect(s.nodePool.Spec.Release.Image).To(Equal(fixtures.ReleaseImage))
	Expect(s.hostedCluster.Spec.Platform.Type).To(Equal(hypershiftv1.AgentPlatform))
}

// awaitProvisioningSuccess checks automation after native worker handoff.
func (s *clusterFixture) awaitProvisioningSuccess(ctx context.Context) {
	GinkgoHelper()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	fixtures.ExpectSuccessfulJobs(
		ctx,
		executions,
		s.order,
		"osac-aap/playbook_osac_create_hosted_cluster.yml",
		s.jobPods,
	)
}

// observeWorkers captures each real allocation and its associated Agent once.
// Allocation correlation and host registration share a five-minute provisioning budget.
func (s *clusterFixture) observeWorkers(ctx context.Context, order *osacv1.ClusterOrder) {
	GinkgoHelper()

	for _, worker := range order.Status.Workers {
		var instance *bmfv1.BareMetalInstance

		Eventually(func(g Gomega) {
			var objects bmfv1.BareMetalInstanceList

			err := environment.Client.List(ctx, &objects, client.InNamespace(environment.Namespace),
				client.MatchingLabels{"osac.openshift.io/baremetalinstance-uuid": worker.BareMetalInstance.ID})
			g.Expect(err).NotTo(HaveOccurred())
			// Count mismatches expose no allocation payloads.
			matchingInstances := len(objects.Items)
			g.Expect(matchingInstances).To(Equal(1))
			instance = objects.Items[0].DeepCopy()
		}).WithContext(ctx).WithTimeout(2 * time.Minute).Should(Succeed())
		s.workers[worker.Name] = &clusterWorker{instance: instance}
		s.instances[instance.GetUID()] = instance.DeepCopy()
		slog.DebugContext(
			ctx,
			"correlated worker allocation",
			"order",
			s.order.Name,
			"worker",
			worker.Name,
			"instance",
			instance.GetName(),
			"instanceUID",
			instance.GetUID(),
			"allocation",
			worker.BareMetalInstance.ID,
		)
	}

	Expect(s.workers).To(HaveLen(int(s.requestedWorkers)))

	expected, err := s.ignition.Stored(ctx)
	Expect(err).NotTo(HaveOccurred())

	for _, worker := range s.workers {
		worker.agent, worker.externalHostID = fixtures.RegisterHost(
			ctx,
			environment,
			s.group.Inventory,
			worker.instance,
			s.infraEnv,
			tenantID,
			expected,
		)
		s.agents[worker.agent.GetUID()] = worker.agent
	}
}
