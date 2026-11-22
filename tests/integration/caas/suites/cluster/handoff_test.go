// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package cluster

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	metal3v1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	assistedv1 "github.com/openshift/assisted-service/api/v1beta1"
	bmfv1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	osacv1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/tests/integration/caas/internal/harness"
	"github.com/osac-project/osac/tests/integration/caas/suites/fixtures"
)

// awaitWorkerHandoff supplies each omitted host once and observes OSAC approval and native claims.
func (s *clusterFixture) awaitWorkerHandoff(ctx context.Context) {
	GinkgoHelper()
	s.awaitWorkerHandoffWithAllocationTimeout(ctx, 5*time.Minute)
}

// awaitWorkerHandoffWithAllocationTimeout gives allocation its own budget before host provisioning.
// Callers observe native worker inputs before starting this phase.
func (s *clusterFixture) awaitWorkerHandoffWithAllocationTimeout(ctx context.Context, allocationTimeout time.Duration) {
	GinkgoHelper()
	slog.DebugContext(ctx, "waiting for worker allocation", "order", s.order.Name, "budget", allocationTimeout)

	allocation, stopAllocation := context.WithTimeout(ctx, allocationTimeout)
	defer stopAllocation()

	By(fmt.Sprintf("waiting for OSAC to allocate %d workers", s.requestedWorkers))

	var order *osacv1.ClusterOrder

	Eventually(func(g Gomega) {
		var err error

		order, err = getClusterOrder(allocation, environment, s.order.Name)
		g.Expect(err).NotTo(HaveOccurred())
		slog.DebugContext(
			allocation,
			"observed worker allocation progress",
			"cluster",
			s.clusterID,
			"order",
			order.Name,
			"orderUID",
			order.UID,
			"phase",
			order.Status.Phase,
			"allocated",
			len(order.Status.Workers),
			"expected",
			s.requestedWorkers,
		)
		g.Expect(order.Status.Workers).To(HaveLen(int(s.requestedWorkers)))

		for _, worker := range order.Status.Workers {
			g.Expect(worker.BareMetalInstance.ID).NotTo(BeEmpty())
		}
	}).WithContext(allocation).Should(Succeed(), "OSAC must allocate the requested workers")
	By("observing real worker provisioning and registering the allocated hosts")
	provisioning, stopProvisioning := context.WithTimeout(ctx, 5*time.Minute)
	defer stopProvisioning()

	s.observeWorkers(provisioning, order)

	for _, worker := range s.workers {
		fixtures.ExpectSuccessfulJobs(
			provisioning,
			executions,
			worker.instance,
			"osac-aap/playbook_osac_create_bare_metal_instance.yml",
			s.jobPods,
		)
	}

	slog.DebugContext(ctx, "waiting for native consumer handoff", "order", s.order.Name, "budget", 4*time.Minute)
	By("waiting for OSAC approval and native consumer claims")
	handoff, stop := context.WithTimeout(ctx, 4*time.Minute)
	defer stop()

	for name, worker := range s.workers {
		var (
			current *assistedv1.Agent
			claim   client.ObjectKey
		)

		Eventually(func(g Gomega) {
			current = s.currentAgent(handoff, g, worker)
			claim = s.checkAgentHandoff(handoff, g, name, current)
		}).WithContext(handoff).Should(Succeed())
		worker.machineClaim = claim
		slog.DebugContext(
			handoff,
			"worker consumer handoff complete",
			"order",
			s.order.Name,
			"worker",
			name,
			"agent",
			current.GetName(),
			"agentUID",
			current.GetUID(),
			"claim",
			worker.machineClaim,
		)
	}

	s.verifyWorkerSources(handoff)
}

// verifyWorkerSources checks original registrations, allocated hardware and the fixture's smallest root disk.
// Captured associations remain unchanged by verification.
func (s *clusterFixture) verifyWorkerSources(ctx context.Context) {
	GinkgoHelper()

	for name, worker := range s.workers {
		agent := s.currentAgent(ctx, Default, worker)
		Expect(agent.GetLabels()[harness.WorkerLabel]).To(Equal(name))
		bmi := &bmfv1.BareMetalInstance{}
		Expect(environment.Client.Get(ctx, client.ObjectKeyFromObject(worker.instance), bmi)).To(Succeed())
		Expect(bmi.UID).To(Equal(worker.instance.UID))
		Expect(bmi.Spec.ExternalHostID).To(Equal(worker.externalHostID))

		By("checking that provisioning selected the smallest inspected root disk")
		namespace, hostName, found := strings.Cut(bmi.Spec.ExternalHostID, "/")
		Expect(found).To(BeTrue())
		Expect(namespace).To(Equal(s.group.Inventory.Namespace))
		provisioned := &metal3v1.BareMetalHost{}
		Expect(environment.Client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: hostName}, provisioned)).To(Succeed())
		Expect(provisioned.UID).NotTo(BeEmpty())
		Expect(s.group.Inventory.Hosts).To(ContainElement(And(
			HaveField("Name", hostName), HaveField("Namespace", namespace), HaveField("UID", provisioned.UID),
		)))
		Expect(provisioned.Spec.RootDeviceHints).NotTo(BeNil())
		rootDevice := provisioned.Spec.RootDeviceHints.DeviceName
		Expect(rootDevice).To(Equal("/dev/sda"))
		Expect(provisioned.GetAnnotations()["osac.openshift.io/fallback-root-device"]).
			To(MatchJSON(`{"deviceName":"/dev/sda"}`))

		Expect(bmi.Status.Hardware).NotTo(BeNil())
		nics := bmi.Status.Hardware.NICs
		interfaces := agent.Status.Inventory.Interfaces
		matched := false

		for _, nic := range nics {
			mac := nic.MAC

			for _, iface := range interfaces {
				observed := iface.MacAddress
				matched = matched || (mac != "" && strings.EqualFold(mac, observed))
			}
		}

		Expect(matched).To(BeTrue(), "Agent inventory must match its allocated BMI hardware")
	}
}

// currentAgent reads an original Agent and stops immediately if it was replaced.
func (s *clusterFixture) currentAgent(ctx context.Context, g Gomega, worker *clusterWorker) *assistedv1.Agent {
	GinkgoHelper()
	current := &assistedv1.Agent{}
	err := environment.Client.Get(ctx, client.ObjectKeyFromObject(worker.agent), current)
	g.Expect(err).NotTo(HaveOccurred())

	if current.GetUID() != worker.agent.GetUID() {
		Fail("original worker Agent was replaced")
	}

	return current
}

// checkConsumerHandoff checks the retained consumer claim on every original Agent.
func (s *clusterFixture) checkConsumerHandoff(ctx context.Context, g Gomega) {
	GinkgoHelper()

	for name, worker := range s.workers {
		s.checkAgentHandoff(ctx, g, name, s.currentAgent(ctx, g, worker))
	}
}

// checkAgentHandoff checks OSAC approval and binding, then returns the validated consumer claim.
func (s *clusterFixture) checkAgentHandoff(
	ctx context.Context,
	g Gomega,
	name string,
	current *assistedv1.Agent,
) client.ObjectKey {
	GinkgoHelper()
	worker := s.workers[name]

	g.Expect(current.GetLabels()[harness.OrderLabel]).To(Equal(s.order.Name))
	g.Expect(current.GetLabels()[harness.WorkerLabel]).To(Equal(name))
	approved := current.Spec.Approved
	claim := client.ObjectKey{
		Name:      current.GetLabels()["agentMachineRef"],
		Namespace: current.GetAnnotations()["agentMachineRefNamespace"],
	}
	slog.DebugContext(
		ctx,
		"observed worker approval and consumer binding",
		"order",
		s.order.Name,
		"worker",
		name,
		"agent",
		current.GetName(),
		"agentUID",
		current.GetUID(),
		"approved",
		approved,
		"claim",
		claim,
	)
	g.Expect(approved).To(BeTrue())
	g.Expect(claim.Name).NotTo(BeEmpty())
	g.Expect(claim.Namespace).To(Equal(s.capiCluster.Namespace))

	if worker.machineClaim.Name != "" {
		g.Expect(claim).To(Equal(worker.machineClaim), "recovery must preserve the consumer claim")
	}

	deployment := current.Spec.ClusterDeploymentName
	g.Expect(deployment).NotTo(BeNil(), "consumer deployment binding must remain present")
	g.Expect(deployment.Name).NotTo(BeEmpty())
	g.Expect(deployment.Namespace).NotTo(BeEmpty())

	return claim
}
