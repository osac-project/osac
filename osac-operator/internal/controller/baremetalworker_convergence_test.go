// Copyright (c) 2026 Red Hat Inc.
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	api "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/controller/baremetalworker"
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

// agentPatchFaultClient runs beforeAgentPatch immediately before an Agent patch
// reaches the real apiserver. It lets a test inject a competing writer after the
// reconciler's fresh Agent read but before its optimistic Patch, without any
// production fault-injection hook.
type agentPatchFaultClient struct {
	client.Client
	beforeAgentPatch func(ctx context.Context, agent *unstructured.Unstructured) error
}

func (c *agentPatchFaultClient) Patch(ctx context.Context, obj client.Object, p client.Patch, opts ...client.PatchOption) error {
	if agent, ok := obj.(*unstructured.Unstructured); ok && agent.GroupVersionKind() == agentGVK && c.beforeAgentPatch != nil {
		if err := c.beforeAgentPatch(ctx, agent); err != nil {
			return err
		}
	}
	return c.Client.Patch(ctx, obj, p, opts...)
}

var _ = Describe("Unified worker reconciliation", Label("baremetalworker"), func() {
	const tenant = "unified-tenant"
	var co *api.ClusterOrder
	var fc *workerFulfillmentStub
	var provider baremetalworker.FulfillmentClient
	var ignition *workerIgnitionEndpoint
	var r *baremetalworker.Reconciler

	var recorder *events.FakeRecorder
	buildReconciler := func(c client.Client) *baremetalworker.Reconciler {
		return baremetalworker.NewReconciler(c, k8sClient, scheme.Scheme, provider, baremetalworker.NewIgnitionFetcher(nil), recorder, testNamespace)
	}
	BeforeEach(func() {
		name := "unified-" + uuid.NewString()
		co = &api.ClusterOrder{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace, Labels: map[string]string{"osac.openshift.io/clusterorder-uuid": "cluster"}, Annotations: map[string]string{"osac.openshift.io/tenant": tenant}}, Spec: api.ClusterOrderSpec{TemplateID: "test", PullSecret: `{"auths":{}}`, NodeRequests: []api.NodeRequest{{NodeSet: "standard", NumberOfNodes: 1, BareMetal: &api.BareMetalNodeSpec{InstanceType: "standard"}}}}}
		fc = newWorkerFulfillmentStub()
		recorder = events.NewFakeRecorder(100)
		provider = fc
		ignition = newWorkerIgnitionEndpoint()

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
	getAgent := func(name string) *unstructured.Unstructured {
		GinkgoHelper()
		agent := &unstructured.Unstructured{}
		agent.SetGroupVersionKind(agentGVK)
		Expect(k8sClient.Get(ctx, client.ObjectKey{Name: name, Namespace: co.Namespace}, agent)).To(Succeed())
		return agent
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
		Expect(setWorkerIgnitionURL(ctx, co.Name+"-infraenv", co.Namespace, ignition.URL())).To(Succeed())
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

	markFailed := func() api.WorkerStatus {
		GinkgoHelper()
		latest := getOrder()
		w := &latest.Status.Workers[0]
		w.Phase = "Failed"
		w.LastFailureReason = "AgentRegistrationTimeout"
		past := metav1.NewTime(time.Now().Add(-time.Hour))
		now := metav1.Now()
		w.LastFailureTime = &now
		w.ReadySince = &past
		Expect(k8sClient.Status().Update(ctx, latest)).To(Succeed())
		return getOrder().Status.Workers[0]
	}
	step := func() reconcile.Result {
		GinkgoHelper()
		result, err := run()
		Expect(err).NotTo(HaveOccurred())
		return result
	}
	makeRetryDue := func() {
		GinkgoHelper()
		latest := getOrder()
		Expect(latest.Status.Workers[0].NextRetryTime).NotTo(BeNil())
		// Explicit fixture clock transition; do not sleep through production backoff.
		past := metav1.NewTime(time.Now().Add(-time.Minute))
		latest.Status.Workers[0].NextRetryTime = &past
		Expect(k8sClient.Status().Update(ctx, latest)).To(Succeed())
	}
	cleanupAgent := func(w api.WorkerStatus, state string) *unstructured.Unstructured {
		GinkgoHelper()
		agent := &unstructured.Unstructured{Object: map[string]interface{}{
			"spec": map[string]interface{}{}, "status": map[string]interface{}{
				"debugInfo": map[string]interface{}{"state": state},
			},
		}}
		agent.SetGroupVersionKind(agentGVK)
		agent.SetName(co.Name + "-cleanup-agent")
		agent.SetNamespace(co.Namespace)
		agent.SetLabels(map[string]string{"osac.openshift.io/worker-name": w.Name})
		Expect(k8sClient.Create(ctx, agent)).To(Succeed())
		DeferCleanup(func() {
			latest := agent.DeepCopy()
			if k8sClient.Get(ctx, client.ObjectKeyFromObject(agent), latest) == nil {
				latest.SetFinalizers(nil)
				Expect(k8sClient.Update(ctx, latest)).To(Succeed())
				_ = k8sClient.Delete(ctx, latest)
			}
		})
		return agent
	}

	It("R03-E1 retains a failed incarnation until absence and schedules one distinct replacement", func() {
		old := provision()
		fc.SetPendingDeletion(true)
		markFailed()
		step() // Request Delete, not completion.
		for range 3 {
			Expect(step().RequeueAfter).To(BeNumerically(">", 0))
			w := getOrder().Status.Workers[0]
			Expect(w.BareMetalInstance).To(Equal(old.BareMetalInstance))
			Expect(w.Phase).To(Equal("Failed"))
			Expect(w.AttemptCount).To(BeZero())
			Expect(w.NextRetryTime).To(BeNil())
		}
		Expect(fc.DeleteCalls()).To(Equal([]string{old.BareMetalInstance.ID}))
		Expect(fc.CreateCalls()).To(HaveLen(1))
		fc.CompleteDeletion(old.BareMetalInstance.ID)
		step()
		checkpoint := getOrder().Status.Workers[0]
		Expect(checkpoint.BareMetalInstance.ID).To(BeEmpty())
		Expect(checkpoint.AttemptCount).To(Equal(int32(1)))
		Expect(checkpoint.NextRetryTime).NotTo(BeNil())
		Expect(checkpoint.ReadySince).To(BeNil())
		step()
		Expect(getOrder().Status.Workers[0]).To(Equal(checkpoint))
		makeRetryDue()
		// R09: the replacement attempt persists a fresh origin before its Create.
		step()
		awaiting := getOrder().Status.Workers[0]
		Expect(awaiting.AttemptStartedAt).NotTo(BeNil())
		Expect(awaiting.BareMetalInstance.ID).To(BeEmpty())
		step()
		replacement := getOrder().Status.Workers[0]
		Expect(replacement.BareMetalInstance.Name).To(Equal(old.BareMetalInstance.Name))
		Expect(replacement.BareMetalInstance.ID).NotTo(BeEmpty())
		Expect(replacement.BareMetalInstance.ID).NotTo(Equal(old.BareMetalInstance.ID))
		Expect(replacement.AttemptCount).To(Equal(int32(1)))
		Expect(replacement.ReadySince).To(BeNil())
		Expect(fc.CreateCalls()).To(HaveLen(2))
	})

	DescribeTable("R03-E2 retains retirement references and the finalizer through delay and outages", func(parentDeleting bool) {
		latest := getOrder()
		latest.Spec.NodeRequests[0].NumberOfNodes = 2
		Expect(k8sClient.Update(ctx, latest)).To(Succeed())
		ready()
		step() // Reserve both slots.
		step()
		step() // One Create per invocation.
		step() // Persist normal phase repair before exercising retirement.
		old := getOrder().Status.Workers[0]
		retiring := func() api.WorkerStatus {
			GinkgoHelper()
			for _, w := range getOrder().Status.Workers {
				if w.Name == old.Name {
					return w
				}
			}
			Fail("retiring worker disappeared before completion")
			return api.WorkerStatus{}
		}
		fc.SetPendingDeletion(true)
		markFailed()
		step() // R09: persist the failed demotion before retirement.
		latest = getOrder()
		if parentDeleting {
			Expect(k8sClient.Delete(ctx, latest)).To(Succeed())
		} else {
			latest.Spec.NodeRequests[0].NumberOfNodes = 1
			Expect(k8sClient.Update(ctx, latest)).To(Succeed())
		}
		step() // Durable retirement before external cleanup.
		Expect(retiring().Phase).To(Equal("Unbinding"))
		Expect(fc.DeleteCalls()).To(BeEmpty())
		step()
		for range 2 {
			step()
			Expect(retiring().BareMetalInstance).To(Equal(old.BareMetalInstance))
			Expect(getOrder().Finalizers).To(ContainElement("osac.openshift.io/baremetalworker-finalizer"))
		}
		fc.SetListEmptyOnce()
		fc.SetGetError(old.BareMetalInstance.ID, status.Error(codes.PermissionDenied, "cleanup Get denied"))
		_, err := run()
		Expect(err).To(HaveOccurred())
		Expect(getOrder().Status.Workers).To(HaveLen(2))
		Expect(getOrder().Finalizers).NotTo(BeEmpty())
		fc.SetGetError(old.BareMetalInstance.ID, nil)
		fc.SetListError(status.Error(codes.Unavailable, "cleanup List outage"))
		_, err = run()
		Expect(err).To(MatchError(ContainSubstring("cleanup List outage")))
		Expect(retiring().BareMetalInstance).To(Equal(old.BareMetalInstance))
		fc.SetListError(nil)
		fc.CompleteDeletion(old.BareMetalInstance.ID)
		if parentDeleting {
			for _, w := range getOrder().Status.Workers[1:] {
				fc.CompleteDeletion(w.BareMetalInstance.ID)
			}
		}
		step()
		if parentDeleting {
			Expect(apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(co), &api.ClusterOrder{}))).To(BeTrue())
		} else {
			Expect(getOrder().Status.Workers).To(HaveLen(1))
			Expect(getOrder().Status.Workers[0].BareMetalInstance.ID).NotTo(Equal(old.BareMetalInstance.ID))
		}
		Expect(fc.CreateCalls()).To(HaveLen(2))
	}, Entry("failed scale-down", false), Entry("parent deletion", true))

	It("R03-E3 recovers an interrupted provisioning ID during deletion without creating", func() {
		ready()
		step() // Reserve.
		interrupted := errors.New("lost ID persistence")
		r = buildReconciler(&workerStatusFaultClient{Client: k8sClient, err: interrupted, fail: func(order *api.ClusterOrder) bool {
			return order.Status.Workers[0].BareMetalInstance.ID != ""
		}})
		_, err := run()
		Expect(err).To(MatchError(interrupted))
		reserved := getOrder().Status.Workers[0]
		Expect(reserved.BareMetalInstance.ID).To(BeEmpty())
		stored, err := fc.ListBareMetalInstances(ctx, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(stored).To(HaveLen(1))
		fc.SetPendingDeletion(true)
		Expect(k8sClient.Delete(ctx, getOrder())).To(Succeed())
		// Finalization must not need ignition or image/instance-type prerequisites.
		Expect(k8sClient.Delete(ctx, newInfraEnv(co.Name+"-infraenv"))).To(Succeed())
		r = buildReconciler(k8sClient)
		step()
		Expect(getOrder().Status.Workers[0].BareMetalInstance.ID).To(Equal(stored[0].GetId()))
		Expect(fc.DeleteCalls()).To(BeEmpty())
		step() // Retirement intent.
		step() // Delete request.
		Expect(getOrder().Finalizers).NotTo(BeEmpty())
		Expect(fc.DeleteCalls()).To(Equal([]string{stored[0].GetId()}))
		fc.CompleteDeletion(stored[0].GetId())
		step()
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(co), &api.ClusterOrder{}))).To(BeTrue())
		Expect(fc.CreateCalls()).To(HaveLen(1))
	})

	It("R03-E4 waits for authoritative old Agent removal despite cached omission", func() {
		old := provision()
		markFailed()
		agent := cleanupAgent(old, "known-unbound")
		agent.SetFinalizers([]string{"test.osac.openshift.io/agent-cleanup"})
		Expect(k8sClient.Update(ctx, agent)).To(Succeed())
		cache := &cleanupAgentFaultClient{Client: k8sClient, omitAgents: true}
		r = buildReconciler(cache)
		for range 3 {
			step()
			Expect(getOrder().Status.Workers[0].BareMetalInstance.ID).To(Equal(old.BareMetalInstance.ID))
			Expect(fc.DeleteCalls()).To(BeEmpty())
			Expect(fc.CreateCalls()).To(HaveLen(1))
		}
		Expect(cache.omittedLists).To(BeNumerically(">", 0))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
		Expect(agent.GetDeletionTimestamp().IsZero()).To(BeFalse())
		// Only the simulated test-owned Agent lifecycle finalizer is advanced.
		agent.SetFinalizers(nil)
		Expect(k8sClient.Update(ctx, agent)).To(Succeed())
		step()
		Expect(fc.DeleteCalls()).To(Equal([]string{old.BareMetalInstance.ID}))
		step()
		Expect(getOrder().Status.Workers[0].NextRetryTime).NotTo(BeNil())
	})

	It("R03-E4 real API UID preconditions reject deletion of a recreated Agent", func() {
		old := provision()
		markFailed()
		step() // R09: persist the failed demotion before Agent deletion preconditions.
		agent := cleanupAgent(old, "known-unbound")
		oldUID := agent.GetUID()
		fault := &cleanupAgentFaultClient{Client: k8sClient, beforeDelete: func() {
			Expect(k8sClient.Delete(ctx, agent)).To(Succeed())
			replacement := agent.DeepCopy()
			replacement.SetUID("")
			replacement.SetResourceVersion("")
			Expect(k8sClient.Create(ctx, replacement)).To(Succeed())
			Expect(replacement.GetUID()).NotTo(Equal(oldUID))
		}}
		r = buildReconciler(fault)
		_, err := run()
		Expect(apierrors.IsConflict(err)).To(BeTrue())
		Expect(apierrors.IsConflict(fault.deleteErr)).To(BeTrue(), "real apiserver must reject stale preconditions")
		Expect(fault.deleteErr).To(MatchError(ContainSubstring("UID")))
		Expect(fault.deleteOptions.Preconditions.UID).To(HaveValue(Equal(oldUID)))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
		Expect(agent.GetUID()).NotTo(Equal(oldUID))
		Expect(agent.GetDeletionTimestamp().IsZero()).To(BeTrue())
		Expect(fc.DeleteCalls()).To(BeEmpty())
		Expect(fc.CreateCalls()).To(HaveLen(1))
		Expect(getOrder().Status.Workers[0].BareMetalInstance.ID).To(Equal(old.BareMetalInstance.ID))
	})

	It("R03-E5 blocks bound Failed cleanup and excludes old readiness from its replacement", func() {
		old := provision()
		markFailed()
		step() // R09: persist the failed demotion, clearing the old healthy interval.
		failed := getOrder().Status.Workers[0]
		Expect(failed.ReadySince).To(BeNil())
		agent := cleanupAgent(old, "installed")
		Expect(unstructured.SetNestedMap(agent.Object, map[string]interface{}{"name": co.Name, "namespace": co.Namespace}, "spec", "clusterDeploymentName")).To(Succeed())
		Expect(k8sClient.Update(ctx, agent)).To(Succeed())
		for range 3 {
			step()
			Expect(getOrder().Status.Workers[0]).To(Equal(failed))
			Expect(getOrder().Status.ReadyWorkers).To(HaveValue(Equal(int32(0))))
		}
		Expect(fc.DeleteCalls()).To(BeEmpty())
		Expect(fc.CreateCalls()).To(HaveLen(1))
		found := false
		for len(recorder.Events) > 0 {
			if strings.Contains(<-recorder.Events, "WorkerCleanupBlocked") {
				found = true
			}
		}
		Expect(found).To(BeTrue(), "bound remediation must have an observable blocker")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(agent), agent)).To(Succeed())
		Expect(agent.GetDeletionTimestamp().IsZero()).To(BeTrue())
		// Simulate the owner completing detach; never clear a production hook.
		unstructured.RemoveNestedField(agent.Object, "spec", "clusterDeploymentName")
		Expect(unstructured.SetNestedField(agent.Object, "known-unbound", "status", "debugInfo", "state")).To(Succeed())
		Expect(k8sClient.Update(ctx, agent)).To(Succeed())
		step() // Agent removal.
		step() // BMI request.
		step() // Confirm absence and schedule.
		makeRetryDue()
		step() // R09: persist the fresh attempt origin.
		step() // Fresh replacement observation, not old installed evidence.
		w := getOrder().Status.Workers[0]
		Expect(w.BareMetalInstance.ID).NotTo(Equal(old.BareMetalInstance.ID))
		Expect(w.Phase).To(Equal("WaitingForAgent"))
		Expect(w.ReadySince).To(BeNil())
		Expect(getOrder().Status.ReadyWorkers).To(HaveValue(Equal(int32(0))))
		Expect(fc.CreateCalls()).To(HaveLen(2))
	})

	It("R09-E1 measures the registration timeout from the persisted attempt origin", func() {
		provision()
		// The order is fresh; only the persisted attempt origin may drive the clock.
		latest := getOrder()
		recent := metav1.NewTime(time.Now().Add(-time.Minute))
		latest.Status.Workers[0].AttemptStartedAt = &recent
		latest.Status.Workers[0].CreationTimestamp = metav1.NewTime(time.Now().Add(-24 * time.Hour))
		Expect(k8sClient.Status().Update(ctx, latest)).To(Succeed())
		step()
		Expect(getOrder().Status.Workers[0].Phase).To(Equal("WaitingForAgent"))

		// An expired origin fails without waiting for a real 30-minute timeout.
		latest = getOrder()
		stale := metav1.NewTime(time.Now().Add(-time.Hour))
		latest.Status.Workers[0].AttemptStartedAt = &stale
		Expect(k8sClient.Status().Update(ctx, latest)).To(Succeed())
		step()
		failed := getOrder().Status.Workers[0]
		Expect(failed.Phase).To(Equal("Failed"))
		Expect(failed.LastFailureReason).To(Equal("AgentRegistrationTimeout"))
	})

	It("R09-E2 keeps one attempt origin across a lost Create acknowledgement and restart", func() {
		ready()
		step() // Reserve only; the origin is part of the reservation write.
		reserved := getOrder().Status.Workers[0]
		Expect(reserved.AttemptStartedAt).NotTo(BeNil())
		Expect(reserved.BareMetalInstance.ID).To(BeEmpty())

		// The Create succeeds but its ID status write is lost.
		interrupted := errors.New("interrupted ID persistence")
		r = buildReconciler(&workerStatusFaultClient{Client: k8sClient, err: interrupted, fail: func(latest *api.ClusterOrder) bool {
			return len(latest.Status.Workers) > 0 && latest.Status.Workers[0].BareMetalInstance.ID != ""
		}})
		_, err := run()
		Expect(errors.Is(err, interrupted)).To(BeTrue())
		lost := getOrder().Status.Workers[0]
		Expect(lost.BareMetalInstance.ID).To(BeEmpty())
		Expect(lost.AttemptStartedAt).To(Equal(reserved.AttemptStartedAt))

		// A restarted reconciler recovers the same identity and origin.
		r = buildReconciler(k8sClient)
		_, err = run()
		Expect(err).NotTo(HaveOccurred())
		recovered := getOrder().Status.Workers[0]
		Expect(recovered.BareMetalInstance.ID).NotTo(BeEmpty())
		Expect(recovered.AttemptStartedAt).To(Equal(reserved.AttemptStartedAt))
	})

	It("R09-E3 backfills a legacy attempt once without extending its deadline", func() {
		provision()
		latest := getOrder()
		latest.Status.Workers[0].AttemptStartedAt = nil
		Expect(k8sClient.Status().Update(ctx, latest)).To(Succeed())
		step()
		backfilled := getOrder().Status.Workers[0].AttemptStartedAt
		Expect(backfilled).NotTo(BeNil())
		step()
		Expect(getOrder().Status.Workers[0].AttemptStartedAt).To(Equal(backfilled), "backfill must be one-time")

		// The persisted origin, not the parent order age, schedules the timeout.
		latest = getOrder()
		stale := metav1.NewTime(time.Now().Add(-time.Hour))
		latest.Status.Workers[0].AttemptStartedAt = &stale
		Expect(k8sClient.Status().Update(ctx, latest)).To(Succeed())
		step()
		Expect(getOrder().Status.Workers[0].Phase).To(Equal("Failed"))
	})

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
		Expect(setWorkerIgnitionURL(ctx, co.Name+"-infraenv", co.Namespace, ignition.URL())).To(Succeed())
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
		stored, err := fc.ListBareMetalInstances(ctx, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(stored).To(HaveLen(1))
		Expect(recovered.BareMetalInstance.ID).To(Equal(stored[0].GetId()))
		Expect(recovered.BareMetalInstance.ID).NotTo(Equal("reserved"))
		Expect(fc.CreateCalls()).To(HaveLen(1))
		Expect(fc.ListCalls()).To(HaveLen(beforeLists + 2)) // Includes the assertion's List.
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
	It("R08-E1 keeps unavailability and semantic outcomes independent across orders", func() {
		// Order A (the shared fixture) owns one provisioned worker.
		workerA := provision()

		// Order B (same tenant) records a BMI the provider no longer has, so its
		// next fallback Get is an authoritative NotFound: a semantic absence.
		other := &api.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "r08-" + uuid.NewString(),
				Namespace:   testNamespace,
				Labels:      map[string]string{"osac.openshift.io/clusterorder-uuid": "cluster"},
				Annotations: map[string]string{"osac.openshift.io/tenant": tenant},
			},
			Spec: *co.Spec.DeepCopy(),
		}
		Expect(k8sClient.Create(ctx, other)).To(Succeed())
		DeferCleanup(func() {
			latest := &api.ClusterOrder{}
			if k8sClient.Get(ctx, client.ObjectKeyFromObject(other), latest) == nil {
				latest.Finalizers = nil
				_ = k8sClient.Update(ctx, latest)
				if latest.DeletionTimestamp.IsZero() {
					_ = k8sClient.Delete(ctx, latest)
				}
			}
			_ = k8sClient.Delete(ctx, newInfraEnv(other.Name+"-infraenv"))
			_ = k8sClient.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: other.Name + "-pull-secret", Namespace: other.Namespace}})
		})
		other.Status.Workers = []api.WorkerStatus{{
			Name: "r08-b", Kind: "BareMetalInstance", NodeSet: "standard", InstanceType: "standard",
			Phase: "WaitingForAgent", CreationTimestamp: metav1.Now(),
			BareMetalInstance: api.BareMetalInstanceReference{Name: "r08-b", ID: "gone-bmi"},
		}}
		Expect(k8sClient.Status().Update(ctx, other)).To(Succeed())
		reconcileOrder := func(order *api.ClusterOrder) (reconcile.Result, error) {
			return r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(order)})
		}
		readOrder := func(order *api.ClusterOrder) *api.ClusterOrder {
			GinkgoHelper()
			latest := &api.ClusterOrder{}
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(order), latest)).To(Succeed())
			return latest
		}

		// Order A: a transport outage persists an order-scoped condition with the
		// bounded unavailable delay and retains the recorded incarnation.
		fc.SetListEmptyOnce()
		fc.SetGetError(workerA.BareMetalInstance.ID, fmt.Errorf("%w: %w",
			baremetalworker.ErrFulfillmentServiceUnavailable, status.Error(codes.Unavailable, "R08 outage")))
		result, err := reconcileOrder(co)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(5 * time.Minute))
		expectingOutage := readOrder(co)
		Expect(apimeta.IsStatusConditionTrue(expectingOutage.Status.Conditions, api.ConditionFulfillmentServiceUnavailable)).To(BeTrue())
		Expect(expectingOutage.Status.Workers).To(ConsistOf(workerA))

		// Order B: an authoritative NotFound prunes the stale slot even while A is
		// unavailable, and neither order's condition affects the other. The first
		// invocation only records the finalizer; the second observes and prunes.
		for range 2 {
			result, err = reconcileOrder(other)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(time.Second))
		}
		pruned := readOrder(other)
		Expect(pruned.Status.Workers).To(BeEmpty())
		Expect(apimeta.IsStatusConditionTrue(pruned.Status.Conditions, api.ConditionFulfillmentServiceUnavailable)).To(BeFalse())
		Expect(apimeta.IsStatusConditionTrue(readOrder(co).Status.Conditions, api.ConditionFulfillmentServiceUnavailable)).To(BeTrue(), "order B's success must not clear order A's outage")

		// Recovery is explicit: the next successful invocation for A clears the
		// condition and keeps the recorded incarnation.
		fc.SetGetError(workerA.BareMetalInstance.ID, nil)
		_, err = reconcileOrder(co)
		Expect(err).NotTo(HaveOccurred())
		recovered := readOrder(co)
		Expect(apimeta.IsStatusConditionTrue(recovered.Status.Conditions, api.ConditionFulfillmentServiceUnavailable)).To(BeFalse())
		Expect(recovered.Status.Workers).To(ConsistOf(workerA))
	})
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
		// The replacement models the object the reconciler recreates for an
		// authoritatively absent InfraEnv: same name, same owner, new UID.
		replacement := newOwnedInfraEnv(co)
		Expect(k8sClient.Create(ctx, replacement)).To(Succeed())
		Expect(setWorkerIgnitionURL(ctx, replacement.GetName(), co.Namespace, ignition.URL())).To(Succeed())
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
	It("R02-E1 restarts after a real status conflict without overwriting another writer", func() {
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
		injected := false
		r = buildReconciler(&workerStatusFaultClient{Client: k8sClient, fail: func(candidate *api.ClusterOrder) bool {
			if !injected && candidate.Status.ReadyWorkers != nil && *candidate.Status.ReadyWorkers == 1 {
				injected = true
				latest := getOrder()
				apimeta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{Type: "ConcurrentOwner", Status: metav1.ConditionTrue, Reason: "Preserved", LastTransitionTime: metav1.Now()})
				Expect(k8sClient.Status().Update(ctx, latest)).To(Succeed())
			}
			// The underlying optimistic Patch now conflicts with the real apiserver.
			return false
		}})
		// The first invocation persists the phase repair, aggregates and conditions
		// in one write. Another status writer wins the optimistic race first, so the
		// write is rejected and the invocation restarts.
		_, err := run()
		Expect(err).To(HaveOccurred())
		Expect(injected).To(BeTrue())
		latest := getOrder()
		Expect(latest.Status.Workers).To(HaveLen(1))
		Expect(latest.Status.Workers[0].Phase).To(Equal("WaitingForAgent"),
			"the rejected write must not overwrite the concurrent writer")
		Expect(apimeta.IsStatusConditionTrue(latest.Status.Conditions, "ConcurrentOwner")).To(BeTrue())
		// A fresh invocation observes the persisted phase and converges the aggregate
		// fields without rebasing the stale calculation over the concurrent
		// worker/condition update.
		_, err = run()
		Expect(err).NotTo(HaveOccurred())
		latest = getOrder()
		Expect(latest.Status.Workers).To(HaveLen(1))
		Expect(latest.Status.Workers[0].Phase).To(Equal("Ready"))
		Expect(apimeta.IsStatusConditionTrue(latest.Status.Conditions, "ConcurrentOwner")).To(BeTrue())
		Expect(latest.Status.CurrentWorkers).NotTo(BeNil())
		Expect(*latest.Status.CurrentWorkers).To(Equal(int32(1)))
		Expect(latest.Status.ReadyWorkers).NotTo(BeNil())
		Expect(*latest.Status.ReadyWorkers).To(Equal(int32(1)))
		Expect(latest.Status.DesiredWorkers).NotTo(BeNil())
		Expect(*latest.Status.DesiredWorkers).To(Equal(int32(1)))
		Expect(fc.CreateCalls()).To(HaveLen(1))
	})

	It("R02-E2 propagates a real Agent binding conflict without takeover", func() {
		old := provision()
		const mac = "aa:bb:cc:dd:ee:55"
		fc.SetHostMAC(old.BareMetalInstance.ID, mac)
		agentName := co.Name + "-agent"
		Expect(createWorkerAgent(ctx, workerAgentFixture{
			Name: agentName, Namespace: co.Namespace, MAC: mac,
		})).To(Succeed())
		// The cluster-order label models the controller's watch filter; the Agent is
		// deliberately left unbound so the next reconcile attempts late binding.
		agent := getAgent(agentName)
		agentLabels := agent.GetLabels()
		if agentLabels == nil {
			agentLabels = map[string]string{}
		}
		agentLabels["osac.openshift.io/cluster-order"] = co.Name
		agent.SetLabels(agentLabels)
		Expect(k8sClient.Update(ctx, agent)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, agent) })

		createsBefore := len(fc.CreateCalls())
		patches := 0
		competingRV := ""
		r = buildReconciler(&agentPatchFaultClient{Client: k8sClient, beforeAgentPatch: func(ctx context.Context, patched *unstructured.Unstructured) error {
			patches++
			// A competing writer rebinds the Agent after the reconciler's fresh read but
			// before its optimistic Patch, advancing the live resource version. The
			// worker-name label is left unset so a fresh invocation must refuse adoption
			// through the binding guard rather than skipping an already-labelled Agent.
			live := &unstructured.Unstructured{}
			live.SetGroupVersionKind(agentGVK)
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(patched), live); err != nil {
				return err
			}
			if err := unstructured.SetNestedMap(live.Object, map[string]interface{}{
				"name": "other-cluster", "namespace": co.Namespace,
			}, "spec", "clusterDeploymentName"); err != nil {
				return err
			}
			if err := k8sClient.Update(ctx, live); err != nil {
				return err
			}
			competingRV = live.GetResourceVersion()
			return nil
		}})

		// The optimistic Patch carries the stale base resource version and is rejected.
		_, err := run()
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsConflict(err)).To(BeTrue(), "expected a propagated optimistic-lock conflict, got %v", err)
		Expect(patches).To(Equal(1), "exactly one Agent patch attempt, no in-reconcile retry")

		// No takeover: the competing binding and its resource version survive untouched.
		live := getAgent(agentName)
		Expect(live.GetResourceVersion()).To(Equal(competingRV))
		Expect(live.GetLabels()).NotTo(HaveKey("osac.openshift.io/worker-name"))
		competingName, _, _ := unstructured.NestedString(live.Object, "spec", "clusterDeploymentName", "name")
		Expect(competingName).To(Equal("other-cluster"))

		// The interrupted invocation persisted no worker progress and caused no external effect.
		interrupted := getOrder()
		Expect(interrupted.Status.Workers).To(HaveLen(1))
		Expect(interrupted.Status.Workers[0].Name).To(Equal(old.Name))
		Expect(interrupted.Status.Workers[0].Phase).To(Equal("WaitingForAgent"))
		Expect(interrupted.Status.Workers[0].BareMetalInstance).To(Equal(old.BareMetalInstance))
		Expect(fc.CreateCalls()).To(HaveLen(createsBefore))
		Expect(fc.DeleteCalls()).To(BeEmpty())

		// A fresh invocation respects the competing binding and refuses to adopt it.
		r = buildReconciler(k8sClient)
		_, err = run()
		Expect(err).To(MatchError(ContainSubstring("bound to another cluster deployment")))
		Expect(getAgent(agentName).GetLabels()).NotTo(HaveKey("osac.openshift.io/worker-name"))
		Expect(fc.CreateCalls()).To(HaveLen(createsBefore))

		// Once the test owner releases the competing binding, an explicit invocation
		// may bind safely to this cluster deployment.
		released := getAgent(agentName)
		unstructured.RemoveNestedField(released.Object, "spec", "clusterDeploymentName")
		Expect(k8sClient.Update(ctx, released)).To(Succeed())
		_, err = run()
		Expect(err).NotTo(HaveOccurred())
		bound := getAgent(agentName)
		Expect(bound.GetLabels()).To(HaveKeyWithValue("osac.openshift.io/worker-name", old.Name))
		boundName, _, _ := unstructured.NestedString(bound.Object, "spec", "clusterDeploymentName", "name")
		Expect(boundName).To(Equal(co.Name))
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
		Expect(result.RequeueAfter).To(Equal(time.Second))
		// ID recovery is a durable observation boundary. Deletion proceeds only
		// on the next explicit invocation from the fresh persisted reference.
		result, err = run()
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(time.Second)) // Persist retirement before mutation.
		Expect(getOrder().Status.Workers[0].Phase).To(Equal("Unbinding"))
		result, err = run()
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(30 * time.Second))
		Expect(fc.DeleteCalls()).To(BeEmpty(), "deletion metadata proves the request is already persisted")
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

	// registerUnboundAgent places a MAC-correlated but not-yet-bound Agent in the
	// cluster-order watch filter, so the next reconcile may late-bind it.
	registerUnboundAgent := func(name, mac string) *unstructured.Unstructured {
		GinkgoHelper()
		Expect(createWorkerAgent(ctx, workerAgentFixture{
			Name: name, Namespace: co.Namespace, MAC: mac,
		})).To(Succeed())
		agent := getAgent(name)
		labels := agent.GetLabels()
		if labels == nil {
			labels = map[string]string{}
		}
		labels["osac.openshift.io/cluster-order"] = co.Name
		agent.SetLabels(labels)
		Expect(k8sClient.Update(ctx, agent)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, agent) })
		return getAgent(name)
	}

	It("R05-E1 recovers interrupted binding from the Agent without a second patch or Create", func() {
		old := provision()
		const mac = "aa:bb:cc:dd:ee:71"
		fc.SetHostMAC(old.BareMetalInstance.ID, mac)
		agent := registerUnboundAgent(co.Name+"-r05-e1-agent", mac)
		createsBefore := len(fc.CreateCalls())

		// The Agent patch succeeds, then the worker status write is lost.
		statusErr := errors.New("worker status write interrupted")
		r = buildReconciler(&workerStatusFaultClient{Client: k8sClient, err: statusErr, fail: func(candidate *api.ClusterOrder) bool {
			return len(candidate.Status.Workers) > 0 && candidate.Status.Workers[0].Phase == "Binding"
		}})
		_, err := run()
		Expect(err).To(MatchError(statusErr))
		Expect(getAgent(agent.GetName()).GetLabels()).To(HaveKeyWithValue("osac.openshift.io/worker-name", old.Name))
		Expect(getOrder().Status.Workers[0].Phase).To(Equal("WaitingForAgent"))
		Expect(fc.CreateCalls()).To(HaveLen(createsBefore))

		// A fresh invocation derives Binding from the labeled Agent without repatching it.
		r = buildReconciler(k8sClient)
		version := getAgent(agent.GetName()).GetResourceVersion()
		_, err = run()
		Expect(err).NotTo(HaveOccurred())
		Expect(getOrder().Status.Workers[0].Phase).To(Equal("Binding"))
		Expect(getOrder().Status.Workers[0].BareMetalInstance).To(Equal(old.BareMetalInstance))
		Expect(fc.CreateCalls()).To(HaveLen(createsBefore))
		Expect(getAgent(agent.GetName()).GetResourceVersion()).To(Equal(version), "status repair must not rebind the Agent")

		// Installed progression is observed by another fresh invocation, still without a rebind.
		installed := getAgent(agent.GetName())
		Expect(unstructured.SetNestedField(installed.Object, "installed", "status", "debugInfo", "state")).To(Succeed())
		Expect(k8sClient.Update(ctx, installed)).To(Succeed())
		version = getAgent(agent.GetName()).GetResourceVersion()
		_, err = run()
		Expect(err).NotTo(HaveOccurred())
		Expect(getOrder().Status.Workers[0].Phase).To(Equal("Ready"))
		Expect(getOrder().Status.Workers[0].ReadySince).NotTo(BeNil())
		Expect(fc.CreateCalls()).To(HaveLen(createsBefore))
		Expect(getAgent(agent.GetName()).GetResourceVersion()).To(Equal(version))
	})

	It("R05-E2 keeps demotion and protected history independent of blocked prerequisites", func() {
		old := provision()
		const mac = "aa:bb:cc:dd:ee:72"
		fc.SetHostMAC(old.BareMetalInstance.ID, mac)
		agent := registerUnboundAgent(co.Name+"-r05-e2-agent", mac)
		// Bind, then report installed so the worker reaches Ready.
		step()
		installed := getAgent(agent.GetName())
		Expect(unstructured.SetNestedField(installed.Object, "installed", "status", "debugInfo", "state")).To(Succeed())
		Expect(k8sClient.Update(ctx, installed)).To(Succeed())
		step()
		ready := getOrder().Status.Workers[0]
		Expect(ready.Phase).To(Equal("Ready"))
		Expect(ready.ReadySince).NotTo(BeNil())
		createsBefore := len(fc.CreateCalls())

		// Block provisioning prerequisites. Ready demotion still happens on the next
		// explicit observation and retains identity/history.
		blocked := getOrder()
		blocked.Spec.PullSecret = ""
		Expect(k8sClient.Update(ctx, blocked)).To(Succeed())
		uninstalled := getAgent(agent.GetName())
		Expect(unstructured.SetNestedSlice(uninstalled.Object, []interface{}{
			map[string]interface{}{"type": "Installed", "status": "False"},
		}, "status", "conditions")).To(Succeed())
		Expect(k8sClient.Update(ctx, uninstalled)).To(Succeed())
		step()
		demoted := getOrder().Status.Workers[0]
		Expect(demoted.Phase).To(Equal("Binding"))
		Expect(demoted.BareMetalInstance).To(Equal(old.BareMetalInstance))
		// R09: demotion ends the continuous healthy interval.
		Expect(demoted.ReadySince).To(BeNil())
		Expect(fc.CreateCalls()).To(HaveLen(createsBefore))

		// A protected phase is not resurrected from an installed Agent while provider
		// cleanup is blocked, and its identity/history survive.
		protected := getOrder()
		protected.Status.Workers[0].Phase = "Unbinding"
		protected.Status.Workers[0].AttemptCount = 3
		protected.Status.Workers[0].LastFailureReason = "previous"
		Expect(k8sClient.Status().Update(ctx, protected)).To(Succeed())
		before := getOrder().Status.Workers[0]
		fc.SetDeleteError(errors.New("provider deletion pending"))
		step()
		got := getOrder().Status.Workers[0]
		Expect(got.Phase).To(Equal("Unbinding"))
		Expect(got.BareMetalInstance).To(Equal(before.BareMetalInstance))
		Expect(got.AttemptCount).To(Equal(int32(3)))
		Expect(got.LastFailureReason).To(Equal("previous"))
		Expect(fc.CreateCalls()).To(HaveLen(createsBefore))
	})

	It("R07-E2 fails only the stale waiting worker and records the replacement UID afterwards", func() {
		old := provision()
		const mac = "aa:bb:cc:dd:ee:81"
		fc.SetHostMAC(old.BareMetalInstance.ID, mac)
		agent := registerUnboundAgent(co.Name+"-r07-e2-agent", mac)
		// Bind, then report installed so the first worker reaches Ready.
		step()
		installed := getAgent(agent.GetName())
		Expect(unstructured.SetNestedField(installed.Object, "installed", "status", "debugInfo", "state")).To(Succeed())
		Expect(k8sClient.Update(ctx, installed)).To(Succeed())
		step()
		Expect(getOrder().Status.Workers[0].Phase).To(Equal("Ready"))
		storedUID := getOrder().Annotations["osac.openshift.io/infraenv-uid"]
		Expect(storedUID).NotTo(BeEmpty())

		// A second worker is still waiting for its Agent when the InfraEnv is replaced.
		latest := getOrder()
		latest.Spec.NodeRequests[0].NumberOfNodes = 2
		Expect(k8sClient.Update(ctx, latest)).To(Succeed())
		for i := 0; i < 6; i++ {
			workers := getOrder().Status.Workers
			if len(workers) == 2 && workers[1].BareMetalInstance.ID != "" {
				break
			}
			step()
		}
		workers := getOrder().Status.Workers
		Expect(workers).To(HaveLen(2))
		Expect(workers[1].Phase).To(Equal("WaitingForAgent"))
		Expect(workers[1].BareMetalInstance.ID).NotTo(BeEmpty())

		// Block a creation prerequisite: stale classification is InfraEnv evidence,
		// not a creation input, so it must not be gated on the disk image.
		fc.AddClusterVersion(privatev1.ClusterVersion_builder{
			Id: "version", Spec: privatev1.ClusterVersionSpec_builder{
				DiskImage: privatev1.DiskImageReference_builder{Id: "missing-image"}.Build(),
			}.Build(),
		}.Build())
		creates := len(fc.CreateCalls())

		// An authoritatively absent InfraEnv is recreated with a new UID; the deleted
		// object's Ready claim must not be reused as evidence.
		infra := newInfraEnv(co.Name + "-infraenv")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(infra), infra)).To(Succeed())
		oldInfraUID := infra.GetUID()
		Expect(k8sClient.Delete(ctx, infra)).To(Succeed())
		recreated := false
		for i := 0; i < 10 && !recreated; i++ {
			step()
			got := newInfraEnv(co.Name + "-infraenv")
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(got), got); err == nil && got.GetUID() != oldInfraUID {
				recreated = true
			}
		}
		Expect(recreated).To(BeTrue(), "the reconciler must recreate an authoritatively absent InfraEnv")
		Expect(setWorkerIgnitionURL(ctx, co.Name+"-infraenv", co.Namespace, ignition.URL())).To(Succeed())

		// The replacement is a durable boundary: only the waiting worker fails, and the
		// replacement UID is not acknowledged in the same invocation as the repair.
		result, err := run()
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(time.Second), "stale-worker classification is a durable boundary")
		workers = getOrder().Status.Workers
		Expect(workers[0].Phase).To(Equal("Ready"), "bound and installed Agent evidence is not stale")
		Expect(workers[0].BareMetalInstance).To(Equal(old.BareMetalInstance))
		Expect(workers[1].Phase).To(Equal("Failed"))
		Expect(workers[1].LastFailureReason).To(Equal("AgentRegistrationTimeout"))
		Expect(getOrder().Annotations["osac.openshift.io/infraenv-uid"]).To(Equal(storedUID),
			"the replacement UID must not be recorded before the stale worker keeps its evidence")
		Expect(fc.CreateCalls()).To(HaveLen(creates))

		// The next invocation records the replacement UID. The healthy worker keeps its
		// incarnation and the failed worker waits for its own retry.
		step()
		latest = getOrder()
		Expect(latest.Annotations["osac.openshift.io/infraenv-uid"]).NotTo(Equal(storedUID))
		Expect(latest.Status.Workers).To(HaveLen(2))
		Expect(latest.Status.Workers[0].Phase).To(Equal("Ready"))
		Expect(latest.Status.Workers[0].BareMetalInstance).To(Equal(old.BareMetalInstance))
		Expect(latest.Status.Workers[1].Phase).To(Equal("Failed"))
		Expect(fc.CreateCalls()).To(HaveLen(creates))
	})
	It("R05-E3 makes no Ready decision from the pre-bind snapshot", func() {
		old := provision()
		const mac = "aa:bb:cc:dd:ee:73"
		fc.SetHostMAC(old.BareMetalInstance.ID, mac)
		agent := registerUnboundAgent(co.Name+"-r05-e3-agent", mac)
		// The Agent already reports installed before this invocation, but binding is
		// a separate action and the pre-bind snapshot must not report Ready.
		Expect(unstructured.SetNestedField(agent.Object, "installed", "status", "debugInfo", "state")).To(Succeed())
		Expect(k8sClient.Update(ctx, agent)).To(Succeed())
		createsBefore := len(fc.CreateCalls())

		step()
		Expect(getOrder().Status.Workers[0].Phase).To(Equal("Binding"))
		Expect(fc.CreateCalls()).To(HaveLen(createsBefore))

		// A fresh invocation observes the labeled installed Agent and reports Ready
		// without repatching the Agent or allocating again.
		version := getAgent(agent.GetName()).GetResourceVersion()
		_, err := run()
		Expect(err).NotTo(HaveOccurred())
		Expect(getOrder().Status.Workers[0].Phase).To(Equal("Ready"))
		Expect(fc.CreateCalls()).To(HaveLen(createsBefore))
		Expect(getAgent(agent.GetName()).GetResourceVersion()).To(Equal(version))
	})

	It("R06-E3 does not take over a same-name Agent recreated under stale evidence", func() {
		old := provision()
		const mac = "aa:bb:cc:dd:ee:76"
		fc.SetHostMAC(old.BareMetalInstance.ID, mac)
		agent := registerUnboundAgent(co.Name+"-r06-e3-agent", mac)
		createsBefore := len(fc.CreateCalls())

		// Between the reconciler's observation and its optimistic patch, the Agent is
		// deleted and replaced by a same-name object with a new UID.
		recreated := false
		var replacement *unstructured.Unstructured
		r = buildReconciler(&agentPatchFaultClient{Client: k8sClient, beforeAgentPatch: func(callbackCtx context.Context, observed *unstructured.Unstructured) error {
			if recreated {
				return nil
			}
			recreated = true
			latest := getAgent(observed.GetName())
			replacement = latest.DeepCopy()
			Expect(k8sClient.Delete(callbackCtx, latest)).To(Succeed())
			replacement.SetUID("")
			replacement.SetResourceVersion("")
			replacement.SetCreationTimestamp(metav1.Time{})
			replacement.SetManagedFields(nil)
			replacement.SetLabels(map[string]string{"osac.openshift.io/cluster-order": co.Name})
			return k8sClient.Create(callbackCtx, replacement)
		}})
		_, err := run()
		Expect(err).To(HaveOccurred(), "a recreated Agent must not be patched under stale evidence")
		Expect(recreated).To(BeTrue())
		Expect(replacement.GetUID()).ToNot(Equal(agent.GetUID()))
		Expect(getAgent(agent.GetName()).GetLabels()).ToNot(HaveKey("osac.openshift.io/worker-name"))
		Expect(getOrder().Status.Workers[0].Phase).To(Equal("WaitingForAgent"))
		Expect(fc.CreateCalls()).To(HaveLen(createsBefore))

		// A fresh reconciler observes the replacement as a new incarnation and binds it.
		r = buildReconciler(k8sClient)
		_, err = run()
		Expect(err).ToNot(HaveOccurred())
		Expect(getOrder().Status.Workers[0].Phase).To(Equal("Binding"))
		Expect(getAgent(agent.GetName()).GetLabels()).To(HaveKeyWithValue("osac.openshift.io/worker-name", old.Name))
	})

	It("R06-E4 reconstructs binding from durable Agent evidence after a restart", func() {
		old := provision()
		const mac = "aa:bb:cc:dd:ee:77"
		fc.SetHostMAC(old.BareMetalInstance.ID, mac)
		agent := registerUnboundAgent(co.Name+"-r06-e4-agent", mac)
		createsBefore := len(fc.CreateCalls())

		// The Agent patch succeeds but the worker status write is lost.
		statusErr := errors.New("worker status write interrupted")
		r = buildReconciler(&workerStatusFaultClient{Client: k8sClient, err: statusErr, fail: func(candidate *api.ClusterOrder) bool {
			return len(candidate.Status.Workers) > 0 && candidate.Status.Workers[0].Phase == "Binding"
		}})
		_, err := run()
		Expect(err).To(MatchError(statusErr))
		Expect(getAgent(agent.GetName()).GetLabels()).To(HaveKeyWithValue("osac.openshift.io/worker-name", old.Name))
		Expect(getOrder().Status.Workers[0].Phase).To(Equal("WaitingForAgent"))
		patchedVersion := getAgent(agent.GetName()).GetResourceVersion()

		// A brand-new reconciler with no in-memory association state reconstructs the
		// Binding from the durable Agent label alone, with no second Agent or BMI patch.
		r = buildReconciler(k8sClient)
		_, err = run()
		Expect(err).ToNot(HaveOccurred())
		Expect(getOrder().Status.Workers[0].Phase).To(Equal("Binding"))
		Expect(getOrder().Status.Workers[0].BareMetalInstance).To(Equal(old.BareMetalInstance))
		Expect(fc.CreateCalls()).To(HaveLen(createsBefore))
		Expect(getAgent(agent.GetName()).GetResourceVersion()).To(Equal(patchedVersion), "restart must not rebind the Agent")
	})

	It("R04-E1a retires and cleans up while InfraEnv, image and pull-secret prerequisites are unavailable", func() {
		provision()
		latest := getOrder()
		latest.Spec.NodeRequests[0].NumberOfNodes = 2
		Expect(k8sClient.Update(ctx, latest)).To(Succeed())
		step() // Reserve the second slot.
		step() // Create it.
		workers := getOrder().Status.Workers
		Expect(workers).To(HaveLen(2))
		Expect(workers[1].BareMetalInstance.ID).NotTo(BeEmpty())
		// Scale back down: one slot retires, and every creation prerequisite is
		// then removed (pull secret, InfraEnv and resolvable disk image).
		latest = getOrder()
		latest.Spec.NodeRequests[0].NumberOfNodes = 1
		Expect(k8sClient.Update(ctx, latest)).To(Succeed())
		blocked := getOrder()
		blocked.Spec.PullSecret = ""
		Expect(k8sClient.Update(ctx, blocked)).To(Succeed())
		infra := newInfraEnv(co.Name + "-infraenv")
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(infra), infra)).To(Succeed())
		Expect(k8sClient.Delete(ctx, infra)).To(Succeed())
		Expect(k8sClient.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: co.Name + "-pull-secret", Namespace: co.Namespace}})).To(Succeed())
		fc.AddClusterVersion(privatev1.ClusterVersion_builder{
			Id: "version",
			Spec: privatev1.ClusterVersionSpec_builder{
				DiskImage: privatev1.DiskImageReference_builder{Id: "missing-image"}.Build(),
			}.Build(),
		}.Build())
		fetches, creates := ignition.Calls(), len(fc.CreateCalls())

		result, err := run()
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		var retiring api.WorkerStatus
		marked := 0
		for _, w := range getOrder().Status.Workers {
			if w.Phase == "Unbinding" {
				retiring = w
				marked++
			}
		}
		Expect(marked).To(Equal(1), "exactly one excess slot retires")
		Expect(retiring.BareMetalInstance.ID).NotTo(BeEmpty())
		Expect(ignition.Calls()).To(Equal(fetches))
		Expect(fc.CreateCalls()).To(HaveLen(creates))

		// Cleanup proceeds; the unavailable prerequisite is still reported instead of
		// silently completing the invocation.
		for i := 0; i < 4 && len(fc.DeleteCalls()) == 0; i++ {
			_, err = run()
		}
		Expect(err).To(HaveOccurred())
		Expect(fc.DeleteCalls()).To(ContainElement(retiring.BareMetalInstance.ID))
		Expect(ignition.Calls()).To(Equal(fetches), "cleanup must not fetch discovery ignition")
		Expect(fc.CreateCalls()).To(HaveLen(creates))
	})

	It("R04-E1b converges Agent binding while another worker waits for its retry", func() {
		first := provision()
		latest := getOrder()
		latest.Spec.NodeRequests[0].NumberOfNodes = 2
		Expect(k8sClient.Update(ctx, latest)).To(Succeed())
		step() // Reserve the independent second slot.
		step() // Create it.
		workers := getOrder().Status.Workers
		Expect(workers).To(HaveLen(2))
		second := workers[1]
		Expect(second.BareMetalInstance.ID).NotTo(BeEmpty())
		// The second worker fails and is not due for its retry yet.
		failing := getOrder()
		failing.Status.Workers[1].Phase = "Failed"
		failing.Status.Workers[1].LastFailureReason = "InfrastructureError"
		failure := metav1.NewTime(time.Now().Add(-time.Hour))
		future := metav1.NewTime(time.Now().Add(time.Hour))
		failing.Status.Workers[1].LastFailureTime = &failure
		failing.Status.Workers[1].NextRetryTime = &future
		Expect(k8sClient.Status().Update(ctx, failing)).To(Succeed())
		// A MAC-correlated Agent makes the first worker bindable in this invocation.
		const mac = "aa:bb:cc:dd:ee:74"
		fc.SetHostMAC(first.BareMetalInstance.ID, mac)
		agent := registerUnboundAgent(co.Name+"-r04-e1b-agent", mac)
		creates := len(fc.CreateCalls())

		result, err := run()
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		Expect(fc.DeleteCalls()).To(ContainElement(second.BareMetalInstance.ID))
		bound, ok := workerStatusByName(getOrder().Status.Workers, first.Name)
		Expect(ok).To(BeTrue())
		Expect(bound.Phase).To(Equal("Binding"))
		pending, ok := workerStatusByName(getOrder().Status.Workers, second.Name)
		Expect(ok).To(BeTrue())
		Expect(pending.Phase).To(Equal("Failed"))
		Expect(pending.BareMetalInstance.ID).To(Equal(second.BareMetalInstance.ID))
		Expect(fc.CreateCalls()).To(HaveLen(creates))

		// The binding completes once the Agent reports installed.
		installed := getAgent(agent.GetName())
		Expect(unstructured.SetNestedField(installed.Object, "installed", "status", "debugInfo", "state")).To(Succeed())
		Expect(k8sClient.Update(ctx, installed)).To(Succeed())
		step()
		reached, ok := workerStatusByName(getOrder().Status.Workers, first.Name)
		Expect(ok).To(BeTrue())
		Expect(reached.Phase).To(Equal("Ready"))
	})

	It("R04-E3 rechecks pending states without Agent or NodePool watch events", func() {
		old := provision()
		const mac = "aa:bb:cc:dd:ee:75"
		fc.SetHostMAC(old.BareMetalInstance.ID, mac)
		// Waiting for its Agent: the invocation schedules its own bounded recheck.
		res, err := run()
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(Equal(30 * time.Second))
		// A registered but not yet installed Agent binds, and the pending state keeps
		// a bounded recheck even though no Agent or NodePool event is delivered.
		agent := registerUnboundAgent(co.Name+"-r04-e3-agent", mac)
		res, err = run()
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(BeNumerically(">", 0))
		Expect(getOrder().Status.Workers[0].Phase).To(Equal("Binding"))
		installed := getAgent(agent.GetName())
		Expect(unstructured.SetNestedField(installed.Object, "installed", "status", "debugInfo", "state")).To(Succeed())
		Expect(k8sClient.Update(ctx, installed)).To(Succeed())
		res, err = run()
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(BeNumerically(">", 0))
		Expect(getOrder().Status.Workers[0].Phase).To(Equal("Ready"))
		// A stable Ready order holds no polling timer and relies on watches.
		res, err = run()
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(BeZero())
	})

	It("R04-E1c persists the worker summary while the image lookup is blocked", func() {
		old := provision()
		// A stale summary: the worker waits for its Agent while Ready is still 1.
		stale := getOrder()
		stale.Spec.NodeRequests[0].NumberOfNodes = 2
		Expect(k8sClient.Update(ctx, stale)).To(Succeed())
		one := int32(1)
		stale.Status.ReadyWorkers = &one
		Expect(k8sClient.Status().Update(ctx, stale)).To(Succeed())
		// The resolved ClusterVersion now references an unknown disk image.
		fc.AddClusterVersion(privatev1.ClusterVersion_builder{
			Id: "version",
			Spec: privatev1.ClusterVersionSpec_builder{
				DiskImage: privatev1.DiskImageReference_builder{Id: "missing-image"}.Build(),
			}.Build(),
		}.Build())
		creates, deletes := len(fc.CreateCalls()), len(fc.DeleteCalls())

		_, err := run()
		Expect(err).To(HaveOccurred())
		latest := getOrder()
		Expect(latest.Status.ReadyWorkers).To(HaveValue(Equal(int32(0))))
		Expect(latest.Status.DesiredWorkers).To(HaveValue(Equal(int32(2))))
		Expect(latest.Status.CurrentWorkers).To(HaveValue(Equal(int32(1))))
		Expect(latest.Status.Workers[0].BareMetalInstance.ID).To(Equal(old.BareMetalInstance.ID))
		Expect(fc.CreateCalls()).To(HaveLen(creates))
		Expect(fc.DeleteCalls()).To(HaveLen(deletes))
	})

	It("R10-E1 reports intent before reservation and advances current/ready with evidence", func() {
		latest := getOrder()
		latest.Spec.NodeRequests[0].NumberOfNodes = 2
		Expect(k8sClient.Update(ctx, latest)).To(Succeed())
		// Finalizer and InfraEnv creation are separate boundaries; while the
		// ignition artifact is unpublished the summary already reports intent and
		// no allocation.
		for range 3 {
			step()
		}
		before := getOrder()
		Expect(before.Status.Workers).To(BeEmpty())
		Expect(before.Status.DesiredWorkers).To(HaveValue(Equal(int32(2))))
		Expect(before.Status.CurrentWorkers).To(HaveValue(Equal(int32(0))))
		Expect(before.Status.ReadyWorkers).To(HaveValue(Equal(int32(0))))
		Expect(setWorkerIgnitionURL(ctx, co.Name+"-infraenv", co.Namespace, ignition.URL())).To(Succeed())

		// Reservations hold names but no verified identity, so they stay out of current.
		_, err := run()
		Expect(err).NotTo(HaveOccurred())
		reserved := getOrder()
		Expect(reserved.Status.Workers).To(HaveLen(2))
		Expect(reserved.Status.CurrentWorkers).To(HaveValue(Equal(int32(0))))
		for _, w := range reserved.Status.Workers {
			Expect(w.BareMetalInstance.Name).NotTo(BeEmpty())
			Expect(w.BareMetalInstance.ID).To(BeEmpty())
		}

		// Each successful create persists an identity; the next observation counts it.
		for count := 1; count <= 2; count++ {
			_, err = run()
			Expect(err).NotTo(HaveOccurred())
			Expect(getOrder().Status.Workers[count-1].BareMetalInstance.ID).NotTo(BeEmpty())
		}
		_, err = run()
		Expect(err).NotTo(HaveOccurred())
		observed := getOrder()
		Expect(observed.Status.CurrentWorkers).To(HaveValue(Equal(int32(2))))
		Expect(observed.Status.ReadyWorkers).To(HaveValue(Equal(int32(0))))

		// A unique compatible, installed Agent advances exactly one worker to Ready.
		first := observed.Status.Workers[0]
		const mac = "aa:bb:cc:dd:ee:91"
		fc.SetHostMAC(first.BareMetalInstance.ID, mac)
		agent := &unstructured.Unstructured{}
		agent.SetGroupVersionKind(agentGVK)
		agent.SetName(co.Name + "-r10-e1-agent")
		agent.SetNamespace(co.Namespace)
		agent.SetLabels(map[string]string{
			"infraenvs.agent-install.openshift.io": co.Name + "-infraenv",
			"osac.openshift.io/worker-name":        first.Name,
		})
		Expect(unstructured.SetNestedField(agent.Object, true, "spec", "approved")).To(Succeed())
		Expect(unstructured.SetNestedMap(agent.Object, map[string]interface{}{"name": co.Name, "namespace": co.Namespace}, "spec", "clusterDeploymentName")).To(Succeed())
		Expect(unstructured.SetNestedSlice(agent.Object, []interface{}{map[string]interface{}{"macAddress": mac}}, "status", "inventory", "interfaces")).To(Succeed())
		Expect(unstructured.SetNestedField(agent.Object, "installed", "status", "debugInfo", "state")).To(Succeed())
		Expect(k8sClient.Create(ctx, agent)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, agent) })
		for range 2 {
			_, err = run()
			Expect(err).NotTo(HaveOccurred())
		}
		converged := getOrder()
		Expect(converged.Status.CurrentWorkers).To(HaveValue(Equal(int32(2))))
		Expect(converged.Status.ReadyWorkers).To(HaveValue(Equal(int32(1))))
	})

	It("R10-E2 drops desired immediately on scale-down and keeps retiring references until cleanup", func() {
		latest := getOrder()
		latest.Spec.NodeRequests[0].NumberOfNodes = 2
		Expect(k8sClient.Update(ctx, latest)).To(Succeed())
		ready()
		for range 4 {
			step()
		}
		scaled := getOrder()
		Expect(scaled.Status.Workers).To(HaveLen(2))
		Expect(scaled.Status.DesiredWorkers).To(HaveValue(Equal(int32(2))))
		Expect(scaled.Status.CurrentWorkers).To(HaveValue(Equal(int32(2))))

		// The intent drops in the same write that records the retirement intent.
		latest = getOrder()
		latest.Spec.NodeRequests[0].NumberOfNodes = 1
		Expect(k8sClient.Update(ctx, latest)).To(Succeed())
		_, err := run()
		Expect(err).NotTo(HaveOccurred())
		retiring := getOrder()
		Expect(retiring.Status.DesiredWorkers).To(HaveValue(Equal(int32(1))))
		Expect(retiring.Status.CurrentWorkers).To(HaveValue(Equal(int32(1))))
		Expect(retiring.Status.ReadyWorkers).To(HaveValue(Equal(int32(0))))
		Expect(retiring.Status.Workers).To(HaveLen(2), "the journal reference stays until cleanup confirms absence")
		unbinding := 0
		for _, w := range retiring.Status.Workers {
			if w.Phase == "Unbinding" {
				unbinding++
				Expect(w.BareMetalInstance.ID).NotTo(BeEmpty())
			}
		}
		Expect(unbinding).To(Equal(1))
	})

	It("R10-E3 partitions capacity per NodeSet so surplus cannot mask a missing set", func() {
		latest := getOrder()
		latest.Spec.NodeRequests = []api.NodeRequest{
			{NodeSet: "compute", NumberOfNodes: 1, BareMetal: &api.BareMetalNodeSpec{InstanceType: "standard"}},
			{NodeSet: "batch", NumberOfNodes: 1, BareMetal: &api.BareMetalNodeSpec{InstanceType: "standard"}},
		}
		Expect(k8sClient.Update(ctx, latest)).To(Succeed())
		ready()
		for range 4 {
			step()
		}
		Expect(getOrder().Status.Workers).To(HaveLen(2))

		// Relabel the batch reservation as a second compute worker: compute now has
		// surplus while batch is missing, both sharing one instance type.
		latest = getOrder()
		for i := range latest.Status.Workers {
			if latest.Status.Workers[i].NodeSet == "batch" {
				latest.Status.Workers[i].NodeSet = "compute"
			}
		}
		Expect(k8sClient.Status().Update(ctx, latest)).To(Succeed())
		_, err := run()
		Expect(err).NotTo(HaveOccurred())
		got := getOrder()
		Expect(got.Status.DesiredWorkers).To(HaveValue(Equal(int32(2))), "intent still covers both NodeSets")
		Expect(got.Status.CurrentWorkers).To(HaveValue(Equal(int32(1))), "surplus compute cannot satisfy the missing batch set")
		Expect(got.Status.ReadyWorkers).To(HaveValue(Equal(int32(0))))
	})

	It("R10-E4 retains the last summary across a provider outage and recovers explicitly", func() {
		provision()
		step()
		before := getOrder()
		Expect(before.Status.DesiredWorkers).To(HaveValue(Equal(int32(1))))
		Expect(before.Status.CurrentWorkers).To(HaveValue(Equal(int32(1))))
		Expect(before.Status.Workers).To(HaveLen(1))

		// A List outage is unknown, not absence: no fabricated zero summary.
		fc.SetListError(fmt.Errorf("%w: %w", baremetalworker.ErrFulfillmentServiceUnavailable,
			status.Error(codes.Unavailable, "R10 provider outage")))
		result, err := run()
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(5 * time.Minute))
		blocked := getOrder()
		Expect(apimeta.IsStatusConditionTrue(blocked.Status.Conditions, api.ConditionFulfillmentServiceUnavailable)).To(BeTrue())
		Expect(blocked.Status.DesiredWorkers).To(HaveValue(Equal(int32(1))))
		Expect(blocked.Status.CurrentWorkers).To(HaveValue(Equal(int32(1))))
		Expect(blocked.Status.Workers).To(ConsistOf(before.Status.Workers))

		// Recovery is explicit on the next successful observation.
		fc.SetListError(nil)
		_, err = run()
		Expect(err).NotTo(HaveOccurred())
		recovered := getOrder()
		Expect(apimeta.IsStatusConditionTrue(recovered.Status.Conditions, api.ConditionFulfillmentServiceUnavailable)).To(BeFalse())
		Expect(recovered.Status.DesiredWorkers).To(HaveValue(Equal(int32(1))))
		Expect(recovered.Status.CurrentWorkers).To(HaveValue(Equal(int32(1))))
	})
})

// workerStatusByName returns the recorded worker with the given slot name.
func workerStatusByName(workers []api.WorkerStatus, name string) (api.WorkerStatus, bool) {
	for _, w := range workers {
		if w.Name == name {
			return w, true
		}
	}
	return api.WorkerStatus{}, false
}
