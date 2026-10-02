// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package acceptance

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	api "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/controller/baremetalworker"
	"github.com/osac-project/osac/osac-operator/internal/controller/baremetalworker/fake"
	"github.com/osac-project/osac/osac-operator/internal/testing/envsim"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

type workerStatusFaultClient struct {
	client.Client
	fail func(*api.ClusterOrder) bool
	err  error
}

func (c *workerStatusFaultClient) Status() client.SubResourceWriter {
	return &workerStatusFaultWriter{SubResourceWriter: c.Client.Status(), c: c}
}

type workerStatusFaultWriter struct {
	client.SubResourceWriter
	c *workerStatusFaultClient
}

func (w *workerStatusFaultWriter) Patch(ctx context.Context, obj client.Object, p client.Patch, opts ...client.SubResourcePatchOption) error {
	if co, ok := obj.(*api.ClusterOrder); ok && w.c.fail(co) {
		return w.c.err
	}
	return w.SubResourceWriter.Patch(ctx, obj, p, opts...)
}

var _ = Describe("Unified worker reconciliation", func() {
	const tenant = "unified-tenant"
	var co *api.ClusterOrder
	var fc *fake.FulfillmentClient
	var provider baremetalworker.FulfillmentClient
	var ignition *fake.IgnitionServer
	var r *baremetalworker.Reconciler
	var sim *envsim.Simulator
	buildReconciler := func(c client.Client) *baremetalworker.Reconciler {
		return baremetalworker.NewReconciler(c, k8sClient, scheme.Scheme, provider, baremetalworker.NewIgnitionFetcher(nil), events.NewFakeRecorder(100), testNamespace)
	}
	BeforeEach(func() {
		name := "unified-" + uuid.NewString()
		co = &api.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace, Labels: map[string]string{"osac.openshift.io/clusterorder-uuid": "cluster"}, Annotations: map[string]string{"osac.openshift.io/tenant": tenant}}, Spec: api.ClusterOrderSpec{TemplateID: "test", PullSecret: `{"auths":{}}`, NodeRequests: []api.NodeRequest{{NodeSet: "standard", NumberOfNodes: 1, BareMetal: &api.BareMetalNodeSpec{InstanceType: "standard"}}}}}
		fc = fake.NewFulfillmentClient()
		provider = fc
		ignition = fake.NewIgnitionServer()
		sim = envsim.New(k8sClient)
		fc.AddCluster(privatev1.Cluster_builder{Id: "cluster", Metadata: privatev1.Metadata_builder{Tenant: tenant}.Build(), Spec: privatev1.ClusterSpec_builder{Version: privatev1.ClusterVersionReference_builder{Id: "version"}.Build()}.Build()}.Build())
		fc.AddClusterVersion(privatev1.ClusterVersion_builder{Id: "version", Spec: privatev1.ClusterVersionSpec_builder{DiskImage: privatev1.DiskImageReference_builder{Id: "image"}.Build()}.Build()}.Build())
		fc.AddDiskImage(privatev1.DiskImage_builder{Id: "image", Metadata: privatev1.Metadata_builder{Name: "image"}.Build()}.Build())
		fc.AddBareMetalInstanceType(newInstanceType("standard", "data-0"))
		r = buildReconciler(k8sClient)
		Expect(k8sClient.Create(ctx, co)).To(Succeed())
		DeferCleanup(func() {
			ignition.Close()
			latest := &api.ClusterOrder{}
			if k8sClient.Get(ctx, client.ObjectKeyFromObject(co), latest) == nil {
				latest.Finalizers = nil
				_ = k8sClient.Update(ctx, latest)
				if latest.DeletionTimestamp.IsZero() {
					_ = k8sClient.Delete(ctx, latest)
				}
			}
			_ = k8sClient.Delete(ctx, newInfraEnv(co.Name+"-infraenv"))
			_ = k8sClient.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: co.Name + "-pull-secret", Namespace: co.Namespace}})
		})
	})
	getOrder := func() *api.ClusterOrder {
		GinkgoHelper()
		latest := &api.ClusterOrder{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(co), latest)).To(Succeed())
		return latest
	}
	run := func() (reconcile.Result, error) {
		return r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(co)})
	}
	ready := func() {
		GinkgoHelper()
		for range 2 {
			_, err := run()
			Expect(err).NotTo(HaveOccurred())
		}
		Expect(sim.MarkInfraEnvReady(ctx, co.Name+"-infraenv", co.Namespace, ignition.URL())).To(Succeed())
	}
	provision := func() api.WorkerStatus {
		GinkgoHelper()
		ready()
		// Reserve and create are distinct persisted checkpoints.
		for range 2 {
			_, err := run()
			Expect(err).NotTo(HaveOccurred())
		}
		latest := getOrder()
		Expect(latest.Status.Workers[0].BareMetalInstance.ID).NotTo(BeEmpty())
		Expect(latest.Status.Workers).To(HaveLen(1))
		return latest.Status.Workers[0]
	}

	It("R01-E1 persists reserve -> one create -> observe checkpoints for NodeSets sharing a type", func() {
		latest := getOrder()
		latest.Spec.NodeRequests = []api.NodeRequest{
			{NodeSet: "compute", NumberOfNodes: 1, BareMetal: &api.BareMetalNodeSpec{InstanceType: "standard"}},
			{NodeSet: "batch", NumberOfNodes: 1, BareMetal: &api.BareMetalNodeSpec{InstanceType: "standard"}},
		}
		Expect(k8sClient.Update(ctx, latest)).To(Succeed())
		// Finalizer and InfraEnv preparation are separate from allocation.
		for range 2 {
			_, err := run()
			Expect(err).NotTo(HaveOccurred())
			Expect(fc.CreateCalls()).To(BeEmpty())
		}
		Expect(sim.MarkInfraEnvReady(ctx, co.Name+"-infraenv", co.Namespace, ignition.URL())).To(Succeed())
		result, err := run()
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		reserved := getOrder().Status.Workers
		Expect(reserved).To(HaveLen(2))
		Expect(fc.CreateCalls()).To(BeEmpty())
		for _, w := range reserved {
			Expect(w.BareMetalInstance.Name).NotTo(BeEmpty())
			Expect(w.BareMetalInstance.ID).To(BeEmpty())
		}
		for count := 1; count <= 2; count++ {
			_, err = run()
			Expect(err).NotTo(HaveOccurred())
			workers := getOrder().Status.Workers
			Expect(fc.CreateCalls()).To(HaveLen(count))
			Expect(workers).To(HaveLen(2))
			for i, w := range workers {
				Expect(w.Name).To(Equal(reserved[i].Name))
				Expect(w.NodeSet).To(Equal(reserved[i].NodeSet))
				Expect(w.InstanceType).To(Equal("standard"))
			}
			Expect(workers[count-1].BareMetalInstance.ID).NotTo(BeEmpty())
		}
		_, err = run()
		Expect(err).NotTo(HaveOccurred())
		Expect(fc.CreateCalls()).To(HaveLen(2))
	})

	It("R01-E2 recovers a lost Create acknowledgement after restart and delayed List visibility", func() {
		latest := getOrder()
		latest.Spec.NodeRequests[0].NumberOfNodes = 2
		Expect(k8sClient.Update(ctx, latest)).To(Succeed())
		ready()
		_, err := run()
		Expect(err).NotTo(HaveOccurred())
		reserved := getOrder().Status.Workers
		Expect(reserved).To(HaveLen(2))
		fault := &lostWorkerResponseClient{FulfillmentClient: fc}
		provider = fault
		r = buildReconciler(k8sClient)
		_, err = run()
		Expect(err).To(MatchError(ContainSubstring("lost successful Create acknowledgement")))
		Expect(getOrder().Status.Workers).To(Equal(reserved))
		Expect(fault.successful).To(Equal(1))
		stored, err := fc.ListBareMetalInstances(ctx, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(stored).To(HaveLen(1))
		Expect(stored[0].GetMetadata().GetName()).To(Equal(reserved[0].BareMetalInstance.Name))

		// Both the observation and AlreadyExists re-list omit the committed BMI.
		// A subsequent explicit call must recheck the same name, not reserve anew.
		r = buildReconciler(k8sClient)
		fc.SetListEmptyCalls(2)
		_, err = run()
		Expect(err).To(MatchError(ContainSubstring("not found in re-list")))
		Expect(getOrder().Status.Workers).To(Equal(reserved))
		Expect(fault.successful).To(Equal(1))
		fc.SetListEmptyOnce()
		_, err = run()
		Expect(err).NotTo(HaveOccurred())
		workers := getOrder().Status.Workers
		Expect(workers[0].BareMetalInstance.ID).To(Equal(stored[0].GetId()))
		Expect(workers[1]).To(Equal(reserved[1]), "recovery must not launch another independent create")
		Expect(fault.successful).To(Equal(1))
		for _, request := range fc.CreateCalls() {
			Expect(request.GetMetadata().GetName()).To(Equal(reserved[0].BareMetalInstance.Name))
		}
		_, err = run()
		Expect(err).NotTo(HaveOccurred())
		Expect(fault.successful).To(Equal(2))
		stored, err = fc.ListBareMetalInstances(ctx, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(stored).To(HaveLen(2))
		for i, worker := range getOrder().Status.Workers {
			Expect(worker.Name).To(Equal(reserved[i].Name))
			Expect(worker.BareMetalInstance.ID).NotTo(BeEmpty())
		}
	})

	DescribeTable("R01-E2 refuses unsafe recovery candidates without changing reservations", func(kind string, visibleInitially bool) {
		ready()
		_, err := run()
		Expect(err).NotTo(HaveOccurred())
		reserved := getOrder().Status.Workers
		candidate := privatev1.BareMetalInstance_builder{Id: "incarnation", Metadata: privatev1.Metadata_builder{
			Name: reserved[0].BareMetalInstance.Name, Tenant: tenant,
			Labels:      map[string]string{"osac.openshift.io/cluster-order": co.Name},
			Annotations: map[string]string{"osac.openshift.io/owner-reference": "ClusterOrder/" + co.Name},
		}.Build()}.Build()
		candidates := []*privatev1.BareMetalInstance{candidate}
		switch kind {
		case "foreign tenant":
			candidate.GetMetadata().SetTenant("foreign")
		case "foreign owner":
			candidate.GetMetadata().SetAnnotations(map[string]string{"osac.openshift.io/owner-reference": "ClusterOrder/foreign"})
		case "ambiguous":
			other := proto.Clone(candidate).(*privatev1.BareMetalInstance)
			other.SetId("other-incarnation")
			candidates = append(candidates, other)
		case "deletion timestamp":
			candidate.GetMetadata().SetDeletionTimestamp(timestamppb.Now())
		case "deleting state":
			candidate.SetStatus(privatev1.BareMetalInstanceStatus_builder{State: privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_DELETING}.Build())
		}
		fault := &workerRelistClient{FulfillmentClient: fc, candidates: candidates, visibleInitially: visibleInitially}
		provider = fault
		r = buildReconciler(k8sClient)
		_, err = run()
		Expect(err).To(HaveOccurred())
		if visibleInitially {
			Expect(fault.creates).To(Equal(0), "must reject deleting reference before creation")
			Expect(fault.lists).To(Equal(1))
		} else {
			Expect(fault.creates).To(Equal(1))
			Expect(fault.lists).To(Equal(2), "must exercise the AlreadyExists re-list")
		}
		Expect(getOrder().Status.Workers).To(Equal(reserved))
		Expect(fc.DeleteCalls()).To(BeEmpty())
	}, Entry("foreign tenant", "foreign tenant", false), Entry("foreign owner", "foreign owner", false),
		Entry("ambiguous", "ambiguous", false), Entry("deletion timestamp", "deletion timestamp", false), Entry("deleting state", "deleting state", false),
		Entry("initially visible deletion timestamp", "deletion timestamp", true), Entry("initially visible deleting state", "deleting state", true))

	It("W-E1 recovers a successful create after its ID status write fails without another Create", func() {
		ready()
		latest := getOrder()
		latest.Spec.NodeRequests[0].NumberOfNodes = 2
		Expect(k8sClient.Update(ctx, latest)).To(Succeed())
		reserveExistingWorker(co, "reserved")
		_, err := run() // Persist the other slot before injecting the ID-write fault.
		Expect(err).NotTo(HaveOccurred())
		other := getOrder().Status.Workers[1]
		interrupted := errors.New("interrupted ID persistence")
		r = buildReconciler(&workerStatusFaultClient{Client: k8sClient, err: interrupted, fail: func(latest *api.ClusterOrder) bool {
			return len(latest.Status.Workers) > 0 && latest.Status.Workers[0].BareMetalInstance.ID != ""
		}})
		_, err = run()
		Expect(errors.Is(err, interrupted)).To(BeTrue())
		Expect(fc.CreateCalls()).To(HaveLen(1))
		Expect(getOrder().Status.Workers[1]).To(Equal(other), "failed ID persistence must not launch another create")
		reserved := getOrder().Status.Workers[0]
		Expect(reserved.BareMetalInstance.Name).To(Equal("reserved"))
		Expect(reserved.BareMetalInstance.ID).To(BeEmpty())
		r = buildReconciler(k8sClient)
		beforeLists, beforeGets := len(fc.ListCalls()), len(fc.GetCalls())
		_, err = run()
		Expect(err).NotTo(HaveOccurred())
		recovered := getOrder().Status.Workers[0]
		Expect(recovered.Name).To(Equal(reserved.Name))
		Expect(recovered.BareMetalInstance.ID).To(Equal("reserved"))
		Expect(fc.CreateCalls()).To(HaveLen(1))
		Expect(fc.ListCalls()).To(HaveLen(beforeLists + 1))
		Expect(fc.GetCalls()).To(HaveLen(beforeGets))
	})
	It("W-E2 replaces an authoritative NotFound slot across prune, reserve and create checkpoints", func() {
		old := provision()
		Expect(fc.DeleteBareMetalInstance(ctx, old.BareMetalInstance.ID)).To(Succeed())
		createsBefore := len(fc.CreateCalls())
		for range 3 {
			_, err := run()
			Expect(err).NotTo(HaveOccurred())
		}
		workers := getOrder().Status.Workers
		Expect(workers).To(HaveLen(1))
		Expect(workers[0].Name).NotTo(Equal(old.Name))
		Expect(workers[0].BareMetalInstance.Name).NotTo(Equal(old.BareMetalInstance.Name))
		Expect(workers[0].BareMetalInstance.ID).NotTo(BeEmpty())
		Expect(fc.CreateCalls()).To(HaveLen(createsBefore + 1))
	})
	It("W-E2 keeps an omitted listed ID using exactly one authoritative fallback Get", func() {
		old := provision()
		fc.SetListEmptyOnce()
		getsBefore, listsBefore, createsBefore := len(fc.GetCalls()), len(fc.ListCalls()), len(fc.CreateCalls())
		_, err := run()
		Expect(err).NotTo(HaveOccurred())
		Expect(getOrder().Status.Workers[0].BareMetalInstance).To(Equal(old.BareMetalInstance))
		Expect(fc.GetCalls()).To(HaveLen(getsBefore + 1))
		Expect(fc.ListCalls()).To(HaveLen(listsBefore + 1))
		Expect(fc.CreateCalls()).To(HaveLen(createsBefore))
	})
	DescribeTable("W-E2 does not prune or replace workers from unknown provider evidence", func(failure string) {
		old := provision()
		createsBefore := len(fc.CreateCalls())
		switch failure {
		case "Get":
			fc.SetListEmptyOnce()
			fc.SetGetError(old.BareMetalInstance.ID, status.Error(codes.Internal, "Get outage"))
		case "List":
			fc.SetListError(status.Error(codes.Internal, "List outage"))
		case "Unavailable":
			fc.SetListError(baremetalworker.ErrFulfillmentServiceUnavailable)
		}
		result, err := run()
		if failure == "Unavailable" {
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(5 * time.Minute))
			Expect(apimeta.IsStatusConditionTrue(getOrder().Status.Conditions, api.ConditionFulfillmentServiceUnavailable)).To(BeTrue())
		} else {
			Expect(err).To(HaveOccurred())
		}
		Expect(getOrder().Status.Workers).To(ConsistOf(old))
		Expect(fc.CreateCalls()).To(HaveLen(createsBefore))
	}, Entry("Get error", "Get"), Entry("List error", "List"), Entry("service unavailable", "Unavailable"))
	It("W-E3 repairs identity and early Ready phase before the InfraEnv gate", func() {
		ready()
		reserveExistingWorker(co, "recorded")
		bmi, err := fc.CreateBareMetalInstance(ctx, privatev1.BareMetalInstance_builder{Id: "owned-id", Metadata: privatev1.Metadata_builder{Name: "recorded", Tenant: tenant, Labels: map[string]string{"osac.openshift.io/cluster-order": co.Name}, Annotations: map[string]string{"osac.openshift.io/owner-reference": "ClusterOrder/" + co.Name}}.Build()}.Build())
		Expect(err).NotTo(HaveOccurred())
		fc.SetHostMAC(bmi.GetId(), "aa:bb:cc:dd:ee:ff")
		agent := &unstructured.Unstructured{}
		agent.SetGroupVersionKind(agentGVK)
		agent.SetName(co.Name + "-agent")
		agent.SetNamespace(co.Namespace)
		agent.SetLabels(map[string]string{"infraenvs.agent-install.openshift.io": co.Name + "-infraenv", "osac.openshift.io/worker-name": "recorded"})
		Expect(unstructured.SetNestedField(agent.Object, true, "spec", "approved")).To(Succeed())
		Expect(unstructured.SetNestedSlice(agent.Object, []interface{}{map[string]interface{}{"macAddress": "aa:bb:cc:dd:ee:ff"}}, "status", "inventory", "interfaces")).To(Succeed())
		Expect(unstructured.SetNestedField(agent.Object, "installed", "status", "debugInfo", "state")).To(Succeed())
		Expect(k8sClient.Create(ctx, agent)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, agent) })
		infra := newInfraEnv(co.Name + "-infraenv")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(infra), infra)).To(Succeed())
		unstructured.RemoveNestedField(infra.Object, "status", "bootArtifacts", "discoveryIgnitionURL")
		Expect(k8sClient.Update(ctx, infra)).To(Succeed())
		getsBefore, createsBefore := len(fc.GetCalls()), len(fc.CreateCalls())
		result, err := run()
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(time.Second))
		repaired := getOrder().Status.Workers[0]
		Expect(repaired.BareMetalInstance.ID).To(Equal("owned-id"))
		Expect(repaired.Phase).To(Equal("Ready"))
		Expect(repaired.ReadySince).NotTo(BeNil())
		Expect(fc.GetCalls()).To(HaveLen(getsBefore))
		Expect(fc.CreateCalls()).To(HaveLen(createsBefore))
	})
	It("W-E3 does not record a recreated InfraEnv UID when stale-failure status persistence fails", func() {
		old := provision()
		latest := getOrder()
		storedUID := latest.Annotations["osac.openshift.io/infraenv-uid"]
		Expect(storedUID).NotTo(BeEmpty())
		infra := newInfraEnv(co.Name + "-infraenv")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(infra), infra)).To(Succeed())
		Expect(k8sClient.Delete(ctx, infra)).To(Succeed())
		replacement := newInfraEnv(infra.GetName())
		Expect(k8sClient.Create(ctx, replacement)).To(Succeed())
		Expect(sim.MarkInfraEnvReady(ctx, replacement.GetName(), co.Namespace, ignition.URL())).To(Succeed())
		interrupted := errors.New("stale failure persistence failed")
		r = buildReconciler(&workerStatusFaultClient{Client: k8sClient, err: interrupted, fail: func(latest *api.ClusterOrder) bool {
			return len(latest.Status.Workers) > 0 && latest.Status.Workers[0].Phase == "Failed"
		}})
		_, err := run()
		Expect(errors.Is(err, interrupted)).To(BeTrue())
		latest = getOrder()
		Expect(latest.Annotations["osac.openshift.io/infraenv-uid"]).To(Equal(storedUID))
		Expect(latest.Status.Workers).To(ConsistOf(old))
		Expect(fc.CreateCalls()).To(HaveLen(1))
		Expect(fc.DeleteCalls()).To(BeEmpty())
	})
	It("W-E4 merges phase evidence into real latest status after a conflict and derives aggregates from that merged slice", func() {
		old := provision()
		fc.SetHostMAC(old.BareMetalInstance.ID, "aa:bb:cc:dd:ee:44")
		agent := &unstructured.Unstructured{}
		agent.SetGroupVersionKind(agentGVK)
		agent.SetName(co.Name + "-agent")
		agent.SetNamespace(co.Namespace)
		agent.SetLabels(map[string]string{"infraenvs.agent-install.openshift.io": co.Name + "-infraenv", "osac.openshift.io/worker-name": old.Name})
		Expect(unstructured.SetNestedField(agent.Object, true, "spec", "approved")).To(Succeed())
		Expect(unstructured.SetNestedMap(agent.Object, map[string]interface{}{"name": co.Name, "namespace": co.Namespace}, "spec", "clusterDeploymentName")).To(Succeed())
		Expect(unstructured.SetNestedSlice(agent.Object, []interface{}{map[string]interface{}{"macAddress": "aa:bb:cc:dd:ee:44"}}, "status", "inventory", "interfaces")).To(Succeed())
		Expect(unstructured.SetNestedField(agent.Object, "installed", "status", "debugInfo", "state")).To(Succeed())
		Expect(k8sClient.Create(ctx, agent)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, agent) })
		appended := api.WorkerStatus{Name: "concurrent-vm", Kind: "VirtualMachine", NodeSet: "concurrent", Phase: "WaitingForAgent", CreationTimestamp: metav1.NewTime(time.Now().Truncate(time.Second))}
		injected := false
		r = buildReconciler(&workerStatusFaultClient{Client: k8sClient, fail: func(candidate *api.ClusterOrder) bool {
			if !injected && candidate.Status.ReadyWorkers != nil && *candidate.Status.ReadyWorkers == 1 {
				injected = true
				latest := getOrder()
				latest.Status.Workers = append(latest.Status.Workers, appended)
				apimeta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{Type: "ConcurrentOwner", Status: metav1.ConditionTrue, Reason: "Preserved", LastTransitionTime: metav1.Now()})
				Expect(k8sClient.Status().Update(ctx, latest)).To(Succeed())
			}
			// The underlying optimistic Patch now conflicts with the real apiserver.
			return false
		}})
		_, err := run()
		Expect(err).NotTo(HaveOccurred())
		// The first call persisted the phase repair; aggregation follows fresh observation.
		_, err = run()
		Expect(err).NotTo(HaveOccurred())
		Expect(injected).To(BeTrue())
		latest := getOrder()
		Expect(latest.Status.Workers).To(HaveLen(2))
		Expect(latest.Status.Workers[0].Phase).To(Equal("Ready"))
		Expect(latest.Status.Workers[1]).To(Equal(appended))
		Expect(apimeta.IsStatusConditionTrue(latest.Status.Conditions, "ConcurrentOwner")).To(BeTrue())
		Expect(latest.Status.CurrentWorkers).NotTo(BeNil())
		Expect(*latest.Status.CurrentWorkers).To(Equal(int32(2)))
		Expect(latest.Status.ReadyWorkers).NotTo(BeNil())
		Expect(*latest.Status.ReadyWorkers).To(Equal(int32(1)))
		Expect(latest.Status.DesiredWorkers).NotTo(BeNil())
		Expect(*latest.Status.DesiredWorkers).To(Equal(int32(2)))
		Expect(fc.CreateCalls()).To(HaveLen(1))
	})

	It("W-E5 recovers a Failed ID-less reference during deletion without history reset or allocation", func() {
		ready()
		reserveExistingWorker(co, "failed-reservation")
		latest := getOrder()
		w := &latest.Status.Workers[0]
		w.Phase = "Failed"
		w.AttemptCount = 4
		w.LastFailureReason = "previous"
		future := metav1.NewTime(time.Now().Add(time.Hour))
		w.NextRetryTime = &future
		Expect(k8sClient.Status().Update(ctx, latest)).To(Succeed())
		_, err := fc.CreateBareMetalInstance(ctx, privatev1.BareMetalInstance_builder{Id: "old-bmi", Metadata: privatev1.Metadata_builder{Name: "failed-reservation", Tenant: tenant, DeletionTimestamp: timestamppb.Now(), Labels: map[string]string{"osac.openshift.io/cluster-order": co.Name}, Annotations: map[string]string{"osac.openshift.io/owner-reference": "ClusterOrder/" + co.Name}}.Build(), Status: privatev1.BareMetalInstanceStatus_builder{State: privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_DELETING}.Build()}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Delete(ctx, latest)).To(Succeed())
		createsBefore := len(fc.CreateCalls())
		result, err := run()
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(30 * time.Second))
		recovered := getOrder().Status.Workers
		Expect(recovered).To(HaveLen(1))
		Expect(recovered[0].BareMetalInstance.ID).To(Equal("old-bmi"))
		Expect(recovered[0].AttemptCount).To(Equal(int32(4)))
		Expect(recovered[0].LastFailureReason).To(Equal("previous"))
		Expect(recovered[0].NextRetryTime).NotTo(BeNil())
		Expect(recovered[0].Phase).To(Equal("Deleting"))
		Expect(fc.CreateCalls()).To(HaveLen(createsBefore))
		Expect(fc.GetInstanceTypeCalls()).To(BeEmpty())
	})
})
