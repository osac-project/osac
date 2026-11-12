// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package cluster

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	assistedv1 "github.com/openshift/assisted-service/api/v1beta1"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	bmfv1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	osacv1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/tests/integration/caas/internal/awx"
	"github.com/osac-project/osac/tests/integration/caas/internal/harness"
	scenario "github.com/osac-project/osac/tests/integration/caas/internal/simulator/scenario"
	"github.com/osac-project/osac/tests/integration/caas/suites/fixtures"
)

const workersPerCluster = 2

// clusterWorker retains one allocation, its original Agent and the observed opaque claim.
// Worker names are the keys in clusterFixture.workers.
type clusterWorker struct {
	instance     *bmfv1.BareMetalInstance
	agent        *assistedv1.Agent
	machineClaim string
}

// clusterFixture retains the identities and execution evidence of one cluster request.
//
// The test fills in the cluster ID, ClusterOrder details and worker resources
// as they become available. Keeping this information lets cleanup
// find the resources even if the test fails before provisioning finishes.
//
// The deployed providers keep responding independently of this scenario. The
// AWX Pod observer stays active through deletion to retain worker-image evidence.
type clusterFixture struct {
	group           fixtures.InventoryGroup
	ignition        *harness.Ignition
	jobPods         *awx.JobPodObserver
	settings        *scenario.Scenario
	namespaces      map[string]types.UID
	instances       map[types.UID]*bmfv1.BareMetalInstance
	agents          map[types.UID]*assistedv1.Agent
	runID           string
	clusterID       string
	createAttempted bool
	capiClusterUID  types.UID
	order           *osacv1.ClusterOrder
	workers         map[string]*clusterWorker
	hostedCluster   *hypershiftv1.HostedCluster
	nodePool        *hypershiftv1.NodePool
	infraEnv        *assistedv1.InfraEnv
	userDataSecrets map[types.NamespacedName]struct{}
	cleanupFinished bool
}

// newClusterFixture creates case settings and keeps execution evidence through cleanup.
func newClusterFixture(ctx context.Context, profile scenario.Profile, group fixtures.InventoryGroup) *clusterFixture {
	GinkgoHelper()

	if err := fixtures.CheckInventoryReleased(ctx, environment, group.Inventory); err != nil {
		AbortSuite("inventory group is not reusable: " + err.Error())
	}

	bytes := make([]byte, 6)
	_, err := rand.Read(bytes)
	Expect(err).NotTo(HaveOccurred())
	s := &clusterFixture{
		group:           group,
		ignition:        &harness.Ignition{Kube: environment.Kube, Namespace: environment.Namespace, Group: group.Name},
		runID:           "caas-" + hex.EncodeToString(bytes),
		workers:         map[string]*clusterWorker{},
		instances:       map[types.UID]*bmfv1.BareMetalInstance{},
		agents:          map[types.UID]*assistedv1.Agent{},
		namespaces:      map[string]types.UID{},
		userDataSecrets: map[types.NamespacedName]struct{}{},
	}

	By("configuring provider responses for this cluster request")
	s.settings = &scenario.Scenario{Client: environment.Client, Namespace: environment.Namespace, Name: s.runID}
	Expect(s.settings.Create(ctx, profile, s.ignition.URL())).To(Succeed())
	AddReportEntry("Provider profile", profile)
	AddReportEntry("Inventory group", group.Name)
	DeferCleanup(func(ctx context.Context) {
		if s.jobPods != nil {
			defer s.jobPods.Close()
		}

		complete := false
		defer func() {
			if !complete {
				AbortSuite("case cleanup failed; stopping inventory group reuse")
			}
		}()

		if s.createAttempted && s.clusterID == "" {
			By("recovering the submitted request before allowing cleanup or reuse")
			Eventually(func() error {
				return s.recoverClusterID(ctx)
			}).WithContext(ctx).WithTimeout(time.Minute).Should(Succeed())
		}

		if s.clusterID != "" && !s.cleanupFinished {
			s.cleanupAfterFailure(ctx)
		}

		By("retiring response settings after case cleanup")
		Expect(s.settings.Delete(ctx)).To(Succeed())
		complete = true
	}, NodeTimeout(10*time.Minute))
	s.jobPods, err = awx.ObserveJobPods(ctx, environment)
	Expect(err).NotTo(HaveOccurred())
	slog.DebugContext(ctx, "opening cluster scenario", "run", s.runID, "group", group.Name)

	return s
}

// createCluster submits the two-worker request and retains its ID for cleanup.
func (s *clusterFixture) createCluster(ctx context.Context) error {
	GinkgoHelper()
	s.createAttempted = true
	created, err := connectionClusters.Create(ctx, publicv1.ClustersCreateRequest_builder{Object: publicv1.Cluster_builder{
		Metadata: publicv1.Metadata_builder{Name: s.runID, Tenant: tenantID}.Build(),
		Spec: publicv1.ClusterSpec_builder{
			Template: publicv1.ClusterTemplateReference_builder{Id: "osac.templates.ocp_ci_small"}.Build(),
			Version:  publicv1.ClusterVersionReference_builder{Name: "caas-it-4-17-0", Shared: true}.Build(),
			NodeSets: map[string]*publicv1.ClusterNodeSet{
				"compute": publicv1.ClusterNodeSet_builder{
					Size: new(int32(workersPerCluster)),
					BaremetalInstanceType: publicv1.BareMetalInstanceTypeReference_builder{
						Name:   s.group.InstanceTypeName,
						Shared: true,
					}.Build(),
				}.Build(),
			},
		}.Build(),
	}.Build()}.Build())
	s.clusterID = created.GetObject().GetId()

	if err != nil {
		return err
	}

	if s.clusterID == "" {
		return fmt.Errorf("submitted cluster response has no identity")
	}

	slog.InfoContext(ctx, "cluster request accepted", "run", s.runID, "cluster", s.clusterID, "tenant", tenantID)
	AddReportEntry("Cluster identity", map[string]string{"run": s.runID, "id": s.clusterID})

	return nil
}

// recoverClusterID finds one original request after its Create acknowledgement is lost.
// Absence or ambiguous matches leave cleanup unresolved and retain response settings.
func (s *clusterFixture) recoverClusterID(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	response, err := connectionClusters.List(ctx, publicv1.ClustersListRequest_builder{
		Filter: new(fmt.Sprintf(`this.metadata.tenant == %q && this.metadata.name == %q`,
			tenantID, s.runID)),
		Limit: new(int32(2)),
	}.Build())
	if err != nil {
		return fmt.Errorf("recover submitted cluster: %w", err)
	}

	if response.GetTotal() != 1 || len(response.GetItems()) != 1 || response.GetItems()[0].GetId() == "" {
		return fmt.Errorf("submitted cluster identity remains unresolved")
	}

	s.clusterID = response.GetItems()[0].GetId()

	return nil
}

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

// awaitWorkerInputs follows real application references and correlation labels.
func (s *clusterFixture) awaitWorkerInputs(ctx context.Context) {
	GinkgoHelper()
	slog.DebugContext(ctx, "waiting for native worker inputs", "order", s.order.Name, "budget", 5*time.Minute)

	outputs, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	By("binding each native provider root as it appears and waiting for worker inputs")
	Eventually(func(g Gomega) {
		s.observeInputs(outputs, g)
		g.Expect(s.hostedCluster).NotTo(BeNil())
		g.Expect(s.capiClusterUID).NotTo(BeEmpty())
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
	Expect(s.hostedCluster.Spec.Platform.Type).To(Equal(hypershiftv1.AgentPlatform))
}

// awaitWorkerHandoff supplies each omitted host once and observes OSAC approval and native claims.
func (s *clusterFixture) awaitWorkerHandoff(ctx context.Context) {
	GinkgoHelper()
	s.awaitWorkerHandoffWithAllocationTimeout(ctx, 5*time.Minute)
}

// awaitWorkerHandoffWithAllocationTimeout gives allocation its own budget before host provisioning.
func (s *clusterFixture) awaitWorkerHandoffWithAllocationTimeout(ctx context.Context, allocationTimeout time.Duration) {
	GinkgoHelper()
	s.awaitWorkerInputs(ctx)
	slog.DebugContext(ctx, "waiting for worker allocation", "order", s.order.Name, "budget", allocationTimeout)

	allocation, stopAllocation := context.WithTimeout(ctx, allocationTimeout)
	defer stopAllocation()

	By("waiting for OSAC to allocate both workers")

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
			workersPerCluster,
		)
		g.Expect(order.Status.Workers).To(HaveLen(workersPerCluster))

		for _, worker := range order.Status.Workers {
			g.Expect(worker.BareMetalInstance.ID).NotTo(BeEmpty())
		}
	}).WithContext(allocation).Should(Succeed(), "OSAC must allocate both workers")
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
		var current *assistedv1.Agent

		Eventually(func(g Gomega) {
			current = s.currentAgent(handoff, g, worker)
			s.checkAgentHandoff(handoff, g, name, current)
		}).WithContext(handoff).Should(Succeed())
		worker.machineClaim = current.GetLabels()["agentMachineRef"]
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

// awaitProvisioningSuccess checks automation after native worker handoff.
// External-access discovery waits for worker binding before provisioning completes.
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

	expected, err := s.ignition.Stored(ctx)
	Expect(err).NotTo(HaveOccurred())

	for _, worker := range s.workers {
		worker.agent = fixtures.RegisterHost(
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

// workerBMIs lists the complete set of this order's Fulfillment allocations.
func (s *clusterFixture) workerBMIs(ctx context.Context, g Gomega) []*publicv1.BareMetalInstance {
	GinkgoHelper()
	response, err := publicv1.NewBareMetalInstancesClient(connection).
		List(ctx, publicv1.BareMetalInstancesListRequest_builder{
			Filter: new(
				fmt.Sprintf(`this.metadata.labels["osac.openshift.io/cluster-order"] == %q`, s.order.Name),
			),
			Limit: new(int32(100)),
		}.Build())
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(response.GetTotal()).To(Equal(int32(len(response.GetItems()))))

	return response.GetItems()
}

// checkWorkerIdentities compares allocation identities at the readiness boundary.
func (s *clusterFixture) checkWorkerIdentities(ctx context.Context, g Gomega, order *osacv1.ClusterOrder) {
	GinkgoHelper()
	g.Expect(order.UID).To(Equal(s.order.UID))
	g.Expect(order.Status.Workers).To(HaveLen(len(s.workers)))

	var currentIDs, savedIDs []string

	for _, instance := range s.workerBMIs(ctx, g) {
		currentIDs = append(currentIDs, instance.GetId())
	}

	for _, worker := range order.Status.Workers {
		saved := s.workers[worker.Name]
		g.Expect(saved).NotTo(BeNil(), "original worker slot must remain present")
		g.Expect(worker.BareMetalInstance.ID).
			To(Equal(saved.instance.GetLabels()["osac.openshift.io/baremetalinstance-uuid"]))
	}

	for _, worker := range s.workers {
		saved := worker.instance
		savedIDs = append(savedIDs, saved.GetLabels()["osac.openshift.io/baremetalinstance-uuid"])
		current := &bmfv1.BareMetalInstance{}
		err := environment.Client.Get(ctx, client.ObjectKeyFromObject(saved), current)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(current.GetUID()).To(Equal(saved.GetUID()))
	}

	g.Expect(currentIDs).To(ConsistOf(savedIDs))
}

// verifyWorkerSources checks each original Agent against its allocated BMI hardware.
// Captured associations remain unchanged by verification.
func (s *clusterFixture) verifyWorkerSources(ctx context.Context) {
	GinkgoHelper()

	for name, worker := range s.workers {
		agent := s.currentAgent(ctx, Default, worker)
		Expect(agent.GetLabels()[harness.WorkerLabel]).To(Equal(name))
		bmi := &bmfv1.BareMetalInstance{}
		Expect(environment.Client.Get(ctx, client.ObjectKeyFromObject(worker.instance), bmi)).To(Succeed())
		Expect(bmi.UID).To(Equal(worker.instance.UID))
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

// checkAgentHandoff checks OSAC approval, correlation and the Agent binding signals used during release.
func (s *clusterFixture) checkAgentHandoff(
	ctx context.Context,
	g Gomega,
	name string,
	current *assistedv1.Agent,
) {
	GinkgoHelper()
	worker := s.workers[name]

	g.Expect(current.GetAnnotations()[harness.TenantAnnotation]).To(Equal(tenantID))
	g.Expect(current.GetLabels()[harness.OrderLabel]).To(Equal(s.order.Name))
	g.Expect(current.GetLabels()[harness.WorkerLabel]).To(Equal(name))
	approved := current.Spec.Approved
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
		current.GetLabels()["agentMachineRef"],
	)
	g.Expect(approved).To(BeTrue())
	claim := current.GetLabels()["agentMachineRef"]
	g.Expect(claim).NotTo(BeEmpty())

	if worker.machineClaim != "" {
		g.Expect(claim).To(Equal(worker.machineClaim), "recovery must preserve the consumer claim")
	}

	g.Expect(current.GetAnnotations()["agentMachineRefNamespace"]).NotTo(BeEmpty())
	deployment := current.Spec.ClusterDeploymentName
	g.Expect(deployment).NotTo(BeNil(), "consumer deployment binding must remain present")
	g.Expect(deployment.Name).NotTo(BeEmpty())
	g.Expect(deployment.Namespace).NotTo(BeEmpty())
}

// getClusterOrder observes the native application contract through the harness client.
func getClusterOrder(ctx context.Context, environment *harness.Environment, name string) (*osacv1.ClusterOrder, error) {
	order := &osacv1.ClusterOrder{}
	err := environment.Client.Get(ctx, client.ObjectKey{Namespace: environment.Namespace, Name: name}, order)
	return order, err
}
