/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package acceptance

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/controller/baremetalworker"
	"github.com/osac-project/osac/osac-operator/internal/controller/baremetalworker/fake"
	"github.com/osac-project/osac/osac-operator/internal/testing/envsim"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

const testNamespace = "default"

var (
	infraEnvGVK = schema.GroupVersionKind{Group: "agent-install.openshift.io", Version: "v1beta1", Kind: "InfraEnv"}
	agentGVK    = schema.GroupVersionKind{Group: "agent-install.openshift.io", Version: "v1beta1", Kind: "Agent"}
)

// bmiNamed builds a minimal BareMetalInstance the fake accepts (metadata.name required).
func bmiNamed(name string) *privatev1.BareMetalInstance {
	return privatev1.BareMetalInstance_builder{
		Metadata: privatev1.Metadata_builder{Name: name}.Build(),
	}.Build()
}

func newInfraEnv(name string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(infraEnvGVK)
	u.SetName(name)
	u.SetNamespace(testNamespace)
	return u
}

// The bare-metal worker acceptance suite. It wires the fake private API + ignition endpoint
// (OSAC-4149) and the environment simulator (OSAC-4150) into an envtest harness. The controller
// behavior each scenario asserts is implemented by later slices; those scenarios are pending
// (PIt) and carry the slice reference that will make them active.
var _ = Describe("Bare-metal worker provisioning", func() {
	var (
		fc  *fake.FulfillmentClient
		sim *envsim.Simulator
		ign *fake.IgnitionServer
	)

	BeforeEach(func() {
		fc = fake.NewFulfillmentClient()
		sim = envsim.New(k8sClient)
		ign = fake.NewIgnitionServer()
	})

	AfterEach(func() {
		ign.Close()
	})

	// Active spec: proves the harness (envtest + fake + simulator + ignition endpoint) stands up
	// and the pieces interoperate. This is AC-1 ("wires the fake and simulator into the harness").
	It("wires the fake, simulator, and ignition endpoint into an envtest harness", func() {
		co := &osacv1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{Name: "harness", Namespace: testNamespace},
			Spec:       osacv1alpha1.ClusterOrderSpec{TemplateID: "test"},
		}
		Expect(k8sClient.Create(ctx, co)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, co) })

		// Simulator: ClusterDeployment exists, then InfraEnv becomes ready with the fake ignition URL.
		Expect(sim.EnsureClusterDeployment(ctx, "harness-cd", testNamespace)).To(Succeed())
		ie := newInfraEnv("harness-infraenv")
		Expect(k8sClient.Create(ctx, ie)).To(Succeed())
		Expect(sim.MarkInfraEnvReady(ctx, "harness-infraenv", testNamespace, ign.URL())).To(Succeed())

		got := newInfraEnv("harness-infraenv")
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "harness-infraenv", Namespace: testNamespace}, got)).To(Succeed())
		url, _, _ := unstructured.NestedString(got.Object, "status", "bootArtifacts", "discoveryIgnitionURL")
		Expect(url).To(Equal(ign.URL()))

		// Simulator: an Agent registers with a MAC.
		Expect(sim.RegisterAgent(ctx, envsim.AgentOptions{
			Name: "harness-agent", Namespace: testNamespace, MAC: "aa:bb:cc:dd:ee:ff",
		})).To(Succeed())

		// Fake private API: a BMI create is recorded, and the real IgnitionFetcher reads the endpoint.
		_, err := fc.CreateBareMetalInstance(ctx, bmiNamed("harness-worker-0"))
		Expect(err).ToNot(HaveOccurred())
		Expect(fc.CreateCalls()).To(HaveLen(1))

		body, err := baremetalworker.NewIgnitionFetcher(nil).FetchIgnition(ctx, ign.URL())
		Expect(err).ToNot(HaveOccurred())
		Expect(body).ToNot(BeEmpty())
	})

	// --- Pending feature scenarios (green-with-pending until their slice lands) ---
	// Each body sketches arrange/act/assert with existing harness helpers; the "act = reconcile"
	// step is a comment because the BareMetalWorkerReconciler is OSAC-4152.

	// Tier-1 end-to-end: drives the whole provisioning-start arc through the real reconciler
	// (InfraEnv -> discovery ignition -> BMI creation -> WaitingForAgent -> agent registration ->
	// Binding) and then asserts the flow STALLS at Binding, which is exactly where a real cluster
	// blocks until the fabric/MetalLB network (OSAC-1436) lets the host install RHCOS and join the
	// HostedCluster. It reuses the seams the fake private API + environment simulator provide, so it
	// needs no hardware, no HyperShift, and no real network. The full path to Ready stays PIt below.
	It("starts worker provisioning and stalls at Binding without networking [OSAC-1436 seam]", func() {
		const (
			clusterUUID    = "provstart-cluster-uuid"
			cvID           = "4.18.0"
			diskImageID    = "rhcos-4.18"
			clusterIDLabel = "osac.openshift.io/clusterorder-uuid"
			coName         = "bmw-provstart"
		)

		rec := events.NewFakeRecorder(20)
		r := baremetalworker.NewReconciler(k8sClient, k8sClient, scheme.Scheme,
			fc, baremetalworker.NewIgnitionFetcher(nil), rec, testNamespace)

		// Preload the disk-image chain (Cluster -> ClusterVersion -> DiskImage) and the instance type
		// carrying a fabric-role port, so the reconciler can resolve everything a BMI create needs.
		fc.AddCluster(privatev1.Cluster_builder{
			Id: clusterUUID,
			Spec: privatev1.ClusterSpec_builder{
				Version: privatev1.ClusterVersionReference_builder{Id: cvID}.Build(),
			}.Build(),
		}.Build())
		fc.AddClusterVersion(privatev1.ClusterVersion_builder{
			Id: cvID,
			Spec: privatev1.ClusterVersionSpec_builder{
				DiskImage: privatev1.DiskImageReference_builder{Id: diskImageID}.Build(),
			}.Build(),
		}.Build())
		fc.AddDiskImage(privatev1.DiskImage_builder{
			Id: diskImageID,
			Spec: privatev1.DiskImageSpec_builder{
				SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
				SourceRef:  diskImageSourceRef,
			}.Build(),
		}.Build())
		fc.AddBareMetalInstanceType(newInstanceType("bm-standard", "data-0"))

		// A ClusterOrder with a 2-node bare-metal node set. Its NetworkAttachment names a Subnet and
		// SecurityGroup that are NEVER resolved against a real network — that unresolved reference is
		// precisely the OSAC-1436 seam; the reconciler passes the names through onto each BMI.
		co := &osacv1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{
				Name:      coName,
				Namespace: testNamespace,
				Labels:    map[string]string{clusterIDLabel: clusterUUID},
			},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID:   "test",
				PullSecret:   "{\"auths\":{}}",
				SSHPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5",
				NodeRequests: []osacv1alpha1.NodeRequest{{
					ResourceClass: "bm-standard",
					NumberOfNodes: 2,
					BareMetal:     &osacv1alpha1.BareMetalNodeSpec{InstanceType: "bm-standard"},
				}},
				NetworkAttachment: &osacv1alpha1.ClusterNetworkAttachment{
					SubnetRef:         "my-subnet",
					SecurityGroupRefs: []string{"sg-default"},
				},
			},
		}
		Expect(k8sClient.Create(ctx, co)).To(Succeed())
		DeferCleanup(func() {
			latest := &osacv1alpha1.ClusterOrder{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(co), latest); err != nil {
				return
			}
			if latest.DeletionTimestamp.IsZero() {
				_ = k8sClient.Delete(ctx, latest)
			}
			fc.SetDeleteError(nil)
			for range 3 {
				_, _ = r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(co)})
			}
			ie := newInfraEnv(coName + "-infraenv")
			_ = k8sClient.Delete(ctx, ie)
		})

		runReconcile := func() (reconcile.Result, error) {
			return r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: coName, Namespace: testNamespace},
			})
		}
		get := func() *osacv1alpha1.ClusterOrder {
			GinkgoHelper()
			latest := &osacv1alpha1.ClusterOrder{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: coName, Namespace: testNamespace}, latest)).To(Succeed())
			return latest
		}
		workerByName := func(latest *osacv1alpha1.ClusterOrder, name string) osacv1alpha1.WorkerStatus {
			GinkgoHelper()
			for _, w := range latest.Status.Workers {
				if w.Name == name {
					return w
				}
			}
			Fail("worker not found: " + name)
			return osacv1alpha1.WorkerStatus{}
		}

		// --- Phase A: provisioning starts ---

		// First reconcile: InfraEnv created (late binding), discovery ignition not ready yet -> requeue.
		res, err := runReconcile()
		Expect(err).ToNot(HaveOccurred())
		Expect(res.RequeueAfter).To(BeNumerically(">", 0))

		// Simulator: the discovery ignition becomes available at the fake endpoint.
		Expect(sim.MarkInfraEnvReady(ctx, coName+"-infraenv", testNamespace, ign.URL())).To(Succeed())

		// Second reconcile: ignition fetched, InfraEnvReady=True.
		_, err = runReconcile()
		Expect(err).ToNot(HaveOccurred())
		Expect(apimeta.IsStatusConditionTrue(
			get().Status.Conditions, osacv1alpha1.ConditionInfraEnvReady)).To(BeTrue())

		// Third reconcile: BMIs are created for both worker slots; workers enter WaitingForAgent.
		res, err = runReconcile()
		Expect(err).ToNot(HaveOccurred())
		Expect(res.RequeueAfter).To(BeNumerically(">", 0), "requeues for agent correlation")

		co = get()
		Expect(co.Status.Workers).To(HaveLen(2))
		for _, w := range co.Status.Workers {
			Expect(w.Phase).To(Equal("WaitingForAgent"))
			Expect(w.Kind).To(Equal("BareMetalInstance"))
			Expect(w.ResourceID).ToNot(BeEmpty())
		}

		// Two worker BMIs were created, tenant-invisible (system tenant) and carrying the
		// unresolved network attachment names — provisioning has genuinely started.
		calls := fc.CreateCalls()
		Expect(calls).To(HaveLen(2))
		for _, bmi := range calls {
			Expect(bmi.GetMetadata().GetTenant()).To(Equal("system"))
			na := bmi.GetSpec().GetNetworkAttachments()
			Expect(na).To(HaveLen(1))
			Expect(na[0].GetSubnet().GetName()).To(Equal("my-subnet"))
		}

		// --- Phase B: an agent registers and binds ---

		// One host boots the discovery ISO and registers as an Agent whose MAC matches worker-0's BMI.
		co = get()
		worker0 := workerByName(co, coName+"-worker-0")
		fc.SetHostMAC(worker0.ResourceID, "aa:bb:cc:00:00:00")
		Expect(sim.RegisterAgent(ctx, envsim.AgentOptions{
			Name: coName + "-agent-0", Namespace: testNamespace, MAC: "aa:bb:cc:00:00:00",
		})).To(Succeed())

		// Label the agent with the cluster-order label (simulates the controller's watch filter).
		agentObj := &unstructured.Unstructured{}
		agentObj.SetGroupVersionKind(agentGVK)
		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Name: coName + "-agent-0", Namespace: testNamespace,
		}, agentObj)).To(Succeed())
		agentLabels := agentObj.GetLabels()
		if agentLabels == nil {
			agentLabels = make(map[string]string)
		}
		agentLabels["osac.openshift.io/cluster-order"] = coName
		agentObj.SetLabels(agentLabels)
		Expect(k8sClient.Update(ctx, agentObj)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, agentObj) })

		// Reconcile: worker-0 correlates by MAC and advances to Binding; worker-1 (no agent) waits.
		_, err = runReconcile()
		Expect(err).ToNot(HaveOccurred())

		co = get()
		Expect(workerByName(co, coName+"-worker-0").Phase).To(Equal("Binding"))
		Expect(workerByName(co, coName+"-worker-1").Phase).To(Equal("WaitingForAgent"))

		// The controller completed late binding on the agent.
		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Name: coName + "-agent-0", Namespace: testNamespace,
		}, agentObj)).To(Succeed())
		Expect(agentObj.GetLabels()).To(HaveKeyWithValue("osac.openshift.io/worker-name", coName+"-worker-0"))

		// --- Phase C: the stall (assert the boundary; do NOT cross it) ---

		// We deliberately DO NOT set the bound agent's status.debugInfo.state="installed". That step
		// is the simulated stand-in for the OSAC-1436-dependent RHCOS install + node join over the
		// fabric/MetalLB network; setting it here would falsely advance past the real-world block.
		// So reconciling again must hold at Binding and never reach Ready.
		_, err = runReconcile()
		Expect(err).ToNot(HaveOccurred())

		co = get()
		Expect(workerByName(co, coName+"-worker-0").Phase).To(Equal("Binding"),
			"worker stalls at Binding until networking (OSAC-1436) lets the host install and join")
		Expect(workerByName(co, coName+"-worker-1").Phase).To(Equal("WaitingForAgent"))
		Expect(co.Status.ReadyWorkers).ToNot(BeNil())
		Expect(*co.Status.ReadyWorkers).To(Equal(int32(0)), "no worker reaches Ready without networking")
	})

	PIt("provisions a bare-metal cluster to Ready [OSAC-4152/OSAC-4159/OSAC-4160 + OSAC-1436]", func() {
		// The full happy path to Ready additionally requires the OSAC-1436 fabric/MetalLB network so
		// the host can install RHCOS and join the HostedCluster (Binding -> Ready gate). Stays pending
		// until OSAC-1436 lands in OSAC-2135; the acceptance analogue would drive the agent's
		// status.debugInfo.state="installed" step that the tier-1 test above intentionally omits.
	})

	PIt("creates BMIs with correct fields and keeps them tenant-invisible [OSAC-4159]", func() {
		// Given: a bare-metal node set + system-owned catalog item.
		// When:  the reconciler creates BMIs via the fake private API.
		// Then:  each recorded BMI carries instance_type, disk_image, user_data (ignition),
		//   network_attachments, and tenant="system"; they are invisible to tenant queries.
	})

	PIt("cleans up all BMIs on cluster delete [OSAC-4176]", func() {
		// Given: a Ready cluster with workers.
		// When:  the ClusterOrder is deleted and the finalizer runs.
		// Then:  every BMI in status.workers is deleted before the finalizer is removed.
	})

	It("rebuilds worker state after controller restart [OSAC-4167]", func() {
		const (
			clusterUUID    = "rebuild-cluster-uuid"
			cvID           = "4.18.0"
			diskImageID    = "rhcos-4.18"
			clusterIDLabel = "osac.openshift.io/clusterorder-uuid"
		)

		rec := events.NewFakeRecorder(10)
		r := baremetalworker.NewReconciler(k8sClient, k8sClient, scheme.Scheme,
			fc, baremetalworker.NewIgnitionFetcher(nil), rec, testNamespace)

		fc.AddCluster(privatev1.Cluster_builder{
			Id: clusterUUID,
			Spec: privatev1.ClusterSpec_builder{
				Version: privatev1.ClusterVersionReference_builder{Id: cvID}.Build(),
			}.Build(),
		}.Build())
		fc.AddClusterVersion(privatev1.ClusterVersion_builder{
			Id: cvID,
			Spec: privatev1.ClusterVersionSpec_builder{
				DiskImage: privatev1.DiskImageReference_builder{Id: diskImageID}.Build(),
			}.Build(),
		}.Build())
		fc.AddDiskImage(privatev1.DiskImage_builder{
			Id: diskImageID,
			Spec: privatev1.DiskImageSpec_builder{
				SourceType: privatev1.SourceType_SOURCE_TYPE_REGISTRY,
				SourceRef:  diskImageSourceRef,
			}.Build(),
		}.Build())
		fc.AddBareMetalInstanceType(newInstanceType("bm-standard", "data-0"))

		co := &osacv1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "bmw-rebuild",
				Namespace:  testNamespace,
				Labels:     map[string]string{clusterIDLabel: clusterUUID},
				Finalizers: []string{"baremetalworker.osac.openshift.io/finalizer"},
			},
			Spec: osacv1alpha1.ClusterOrderSpec{
				TemplateID:   "test",
				PullSecret:   "{\"auths\":{}}",
				SSHPublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5",
				NodeRequests: []osacv1alpha1.NodeRequest{{
					ResourceClass: "bm-standard",
					NumberOfNodes: 2,
					BareMetal: &osacv1alpha1.BareMetalNodeSpec{
						InstanceType: "bm-standard",
					},
				}},
				NetworkAttachment: &osacv1alpha1.ClusterNetworkAttachment{
					SubnetRef:         "my-subnet",
					SecurityGroupRefs: []string{"sg-default"},
				},
			},
		}
		Expect(k8sClient.Create(ctx, co)).To(Succeed())
		DeferCleanup(func() {
			latest := &osacv1alpha1.ClusterOrder{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(co), latest); err != nil {
				return
			}
			if latest.DeletionTimestamp.IsZero() {
				_ = k8sClient.Delete(ctx, latest)
			}
			fc.SetDeleteError(nil)
			for range 3 {
				_, _ = r.Reconcile(ctx, reconcile.Request{
					NamespacedName: client.ObjectKeyFromObject(co),
				})
			}
			ie := newInfraEnv(co.Name + "-infraenv")
			_ = k8sClient.Delete(ctx, ie)
		})

		// Create BMIs in the fake for two workers. The fake defaults resource ID to name.
		_, err := fc.CreateBareMetalInstance(ctx, privatev1.BareMetalInstance_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: "system",
				Name:   "bmw-rebuild-worker-0",
				Labels: map[string]string{"osac.openshift.io/cluster-order": "bmw-rebuild"},
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		_, err = fc.CreateBareMetalInstance(ctx, privatev1.BareMetalInstance_builder{
			Metadata: privatev1.Metadata_builder{
				Tenant: "system",
				Name:   "bmw-rebuild-worker-1",
				Labels: map[string]string{"osac.openshift.io/cluster-order": "bmw-rebuild"},
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		fc.SetHostMAC("bmw-rebuild-worker-0", "aa:bb:cc:00:00:00")
		fc.SetHostMAC("bmw-rebuild-worker-1", "aa:bb:cc:11:11:11")

		// Pre-seed status.workers as if the controller had previously written them.
		now := metav1.Now()
		co.Status.Workers = []osacv1alpha1.WorkerStatus{
			{
				Name:              "bmw-rebuild-worker-0",
				Kind:              "BareMetalInstance",
				ResourceID:        "bmw-rebuild-worker-0",
				NodeSet:           "bm-standard",
				Phase:             "Provisioning",
				CreationTimestamp: now,
			},
			{
				Name:               "bmw-rebuild-worker-1",
				Kind:               "BareMetalInstance",
				ResourceID:         "bmw-rebuild-worker-1",
				NodeSet:            "bm-standard",
				Phase:              "Binding",
				CreationTimestamp:  now,
				AttemptCount:       2,
				LastFailureReason:  "AgentRegistrationTimeout",
				LastFailureMessage: "no agent registered within 30m",
				LastFailureTime:    &now,
			},
		}
		Expect(k8sClient.Status().Update(ctx, co)).To(Succeed())

		// Register an agent for worker-0 that is bound and installed (simulating Ready state).
		Expect(sim.RegisterAgent(ctx, envsim.AgentOptions{
			Name: "bmw-rebuild-agent-0", Namespace: testNamespace, MAC: "aa:bb:cc:00:00:00",
		})).To(Succeed())
		agentObj := &unstructured.Unstructured{}
		agentObj.SetGroupVersionKind(agentGVK)
		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Name: "bmw-rebuild-agent-0", Namespace: testNamespace,
		}, agentObj)).To(Succeed())
		agentLabels := agentObj.GetLabels()
		if agentLabels == nil {
			agentLabels = make(map[string]string)
		}
		agentLabels["osac.openshift.io/cluster-order"] = "bmw-rebuild"
		agentLabels["osac.openshift.io/worker-name"] = "bmw-rebuild-worker-0"
		agentObj.SetLabels(agentLabels)
		Expect(unstructured.SetNestedField(agentObj.Object, "installed", "status", "debugInfo", "state")).To(Succeed())
		Expect(k8sClient.Update(ctx, agentObj)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, agentObj) })

		// No agent for worker-1 — should rebuild to WaitingForAgent.

		// Set up InfraEnv so the rest of the reconcile doesn't error.
		Expect(sim.EnsureClusterDeployment(ctx, "bmw-rebuild-cd", testNamespace)).To(Succeed())
		ie := newInfraEnv("bmw-rebuild-infraenv")
		Expect(k8sClient.Create(ctx, ie)).To(Succeed())
		Expect(sim.MarkInfraEnvReady(ctx, "bmw-rebuild-infraenv", testNamespace, ign.URL())).To(Succeed())

		// Run reconcile — rebuild should re-derive phases from live state.
		_, err = r.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: "bmw-rebuild", Namespace: testNamespace},
		})
		Expect(err).ToNot(HaveOccurred())

		// Verify rebuilt phases.
		co = &osacv1alpha1.ClusterOrder{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{
			Name: "bmw-rebuild", Namespace: testNamespace,
		}, co)).To(Succeed())

		Expect(co.Status.Workers).To(HaveLen(2))
		Expect(co.Status.Workers[0].Name).To(Equal("bmw-rebuild-worker-0"))
		Expect(co.Status.Workers[0].Phase).To(Equal("Ready"))
		Expect(co.Status.Workers[1].Name).To(Equal("bmw-rebuild-worker-1"))
		Expect(co.Status.Workers[1].Phase).To(Equal("WaitingForAgent"))

		// Failure history preserved.
		Expect(co.Status.Workers[1].AttemptCount).To(Equal(int32(2)))
		Expect(co.Status.Workers[1].LastFailureReason).To(Equal("AgentRegistrationTimeout"))
		Expect(co.Status.Workers[1].LastFailureMessage).To(Equal("no agent registered within 30m"))
		Expect(co.Status.Workers[1].LastFailureTime).ToNot(BeNil())

		// Zero additional BMI creates (the pre-seeded creates don't count).
		Expect(fc.CreateCalls()).To(HaveLen(2))
	})

	PIt("translates worker status to tenant-visible Cluster status [OSAC-4169]", func() {
		// Given: ClusterOrder worker conditions (WorkersFailed / InfraEnvReady / RHCOSImageNotFound).
		// When:  the feedback controller syncs to the public Cluster via Signal.
		// Then:  tenant-safe conditions (WORKER_PROVISIONING_FAILED/BLOCKED) appear without infra detail.
	})
})
