/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/networktask"
)

func observabilityDomain(name, namespace, tenant string) *v1alpha1.FabricDomain {
	return &v1alpha1.FabricDomain{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: namespace, UID: types.UID(namespace + "/" + name),
			CreationTimestamp: metav1.NewTime(time.Now().Add(-2 * time.Minute)),
			Annotations:       map[string]string{osacTenantKey: tenant, "osac.openshift.io/owner-reference": "test-owner"},
		},
		Spec: v1alpha1.FabricDomainSpec{Type: v1alpha1.FabricDomainTypeEthernetEW, VirtualNetwork: "vnet-id"},
	}
}

func observabilityClient(t *testing.T, objects ...client.Object) client.WithWatch {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).
		WithStatusSubresource(&v1alpha1.FabricDomain{}).Build()
}

func observabilityReconciler(c client.Client) (*FabricDomainReconciler, *prometheus.Registry, *events.FakeRecorder) {
	o := newFabricDomainObservability(c, "networking")
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(o)
	recorder := events.NewFakeRecorder(20)
	return &FabricDomainReconciler{
		Client: c, NetworkingNamespace: "networking", Recorder: recorder, observability: o,
	}, registry, recorder
}

func observabilityMetric(t *testing.T, registry *prometheus.Registry, name string, labels map[string]string) *dto.Metric {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.Metric {
			if len(metric.Label) != len(labels) {
				continue
			}
			matches := true
			for _, label := range metric.Label {
				value, ok := labels[label.GetName()]
				matches = matches && ok && value == label.GetValue()
			}
			if matches {
				return metric
			}
		}
	}
	return nil
}

func observabilityEvent(t *testing.T, recorder *events.FakeRecorder, eventType, reason string) {
	t.Helper()
	select {
	case event := <-recorder.Events:
		if !strings.Contains(event, eventType+" "+reason+" ") {
			t.Fatalf("unexpected event: %s", event)
		}
	default:
		t.Fatalf("missing %s event", reason)
	}
}

func observabilityNoEvent(t *testing.T, recorder *events.FakeRecorder) {
	t.Helper()
	select {
	case event := <-recorder.Events:
		t.Fatalf("unexpected event: %s", event)
	default:
	}
}

func TestFabricDomainObservabilityInventory(t *testing.T) {
	a := observabilityDomain("a", "networking", "tenant-a")
	b := observabilityDomain("b", "networking", "tenant-a")
	b.Annotations[osacManagementStateAnnotation] = ManagementStateUnmanaged
	c := observabilityDomain("c", "networking", "tenant-b")
	c.Spec.Type = v1alpha1.FabricDomainType("nvlink")
	deleting := observabilityDomain("deleting", "networking", "tenant-a")
	now := metav1.Now()
	deleting.DeletionTimestamp = &now
	deleting.Finalizers = []string{osacFabricDomainFinalizer}
	other := observabilityDomain("other", "elsewhere", "tenant-a")
	noTenant := observabilityDomain("no-tenant", "networking", "")
	cached := observabilityClient(t, a, b, c, deleting, other, noTenant)
	_, registry, _ := observabilityReconciler(cached)
	labels := map[string]string{"type": "ethernet_ew", "tenant": "tenant-a"}
	metric := observabilityMetric(t, registry, "osac_fabric_domains_total", labels)
	if metric.GetGauge().GetValue() != 2 {
		t.Fatalf("expected two existing domains without reconciling: %v", metric)
	}
	if metric := observabilityMetric(t, registry, "osac_fabric_domains_total",
		map[string]string{"type": "nvlink", "tenant": "tenant-b"}); metric.GetGauge().GetValue() != 1 {
		t.Fatalf("missing second type/tenant: %v", metric)
	}
	if metric := observabilityMetric(t, registry, "osac_fabric_domains_total",
		map[string]string{"type": "ethernet_ew", "tenant": ""}); metric.GetGauge().GetValue() != 1 {
		t.Fatalf("missing unannotated domain: %v", metric)
	}
	if err := cached.Delete(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if err := cached.Delete(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	if metric := observabilityMetric(t, registry, "osac_fabric_domains_total", labels); metric != nil {
		t.Fatalf("stale series after deletion: %v", metric)
	}
	// Restart after deletion: no per-object bookkeeping or prior observation needed.
	_, restarted, _ := observabilityReconciler(cached)
	if metric := observabilityMetric(t, restarted, "osac_fabric_domains_total", labels); metric != nil {
		t.Fatalf("deleted/unobserved objects changed restarted gauge: %v", metric)
	}
	all := prometheus.NewPedanticRegistry()
	all.MustRegister(newFabricDomainObservability(cached, ""))
	if metric := observabilityMetric(t, all, "osac_fabric_domains_total", labels); metric.GetGauge().GetValue() != 1 {
		t.Fatalf("empty namespace should count all cached namespaces: %v", metric)
	}
}

func TestFabricDomainObservabilityListError(t *testing.T) {
	c := interceptor.NewClient(observabilityClient(t), interceptor.Funcs{
		List: func(context.Context, client.WithWatch, client.ObjectList, ...client.ListOption) error {
			return errors.New("cache unavailable")
		},
	})
	_, registry, _ := observabilityReconciler(c)
	if _, err := registry.Gather(); err == nil || !strings.Contains(err.Error(), "cache unavailable") {
		t.Fatalf("list failure must fail collection, not report zero: %v", err)
	}
}

func observabilityConditioned(status metav1.ConditionStatus, reason string) *v1alpha1.FabricDomain {
	fd := observabilityDomain("domain", "networking", "tenant-a")
	meta.SetStatusCondition(&fd.Status.Conditions, metav1.Condition{
		Type: v1alpha1.NetworkConditionReady, Status: status, Reason: reason, Message: "m"})
	return fd
}

func TestFabricDomainObserveTransitions(t *testing.T) {
	durationLabels := map[string]string{"type": "ethernet_ew"}
	failureLabels := map[string]string{"type": "ethernet_ew", "reason": v1alpha1.ReasonFailed}
	r, registry, recorder := observabilityReconciler(observabilityClient(t))
	pending := observabilityConditioned(metav1.ConditionFalse, v1alpha1.ReasonProgressing)
	ready := observabilityConditioned(metav1.ConditionTrue, v1alpha1.ReasonSucceeded)
	failed := observabilityConditioned(metav1.ConditionFalse, v1alpha1.ReasonFailed)

	r.observeTransition(ready, pending)
	observabilityEvent(t, recorder, corev1.EventTypeNormal, fabricDomainProvisionedEvent)
	metric := observabilityMetric(t, registry, "osac_fabric_domain_provisioning_duration_seconds", durationLabels)
	if metric.GetHistogram().GetSampleCount() != 1 || math.Abs(metric.GetHistogram().GetSampleSum()-120) > 5 {
		t.Fatalf("expected one creation-to-Ready sample of about 120s: %v", metric)
	}
	r.observeTransition(ready, ready)
	observabilityNoEvent(t, recorder)
	if metric := observabilityMetric(t, registry, "osac_fabric_domain_provisioning_duration_seconds", durationLabels); metric.GetHistogram().GetSampleCount() != 1 {
		t.Fatalf("requeue in Ready added a sample: %v", metric)
	}

	r.observeTransition(failed, pending)
	observabilityEvent(t, recorder, corev1.EventTypeWarning, fabricDomainFailedEvent)
	r.observeTransition(failed, failed)
	observabilityNoEvent(t, recorder)
	if metric := observabilityMetric(t, registry, "osac_fabric_domain_provisioning_failures_total", failureLabels); metric.GetCounter().GetValue() != 1 {
		t.Fatalf("failure counted other than once: %v", metric)
	}
	// A different failure message in the same state is a repeat, not a transition.
	changed := observabilityConditioned(metav1.ConditionFalse, v1alpha1.ReasonFailed)
	changed.Status.Conditions[0].Message = "other"
	r.observeTransition(changed, failed)
	observabilityNoEvent(t, recorder)

	// Ready with ProvisioningDisabled provisioned nothing: no sample, no event.
	disabled := observabilityConditioned(metav1.ConditionTrue, v1alpha1.ReasonProvisioningDisabled)
	r.observeTransition(disabled, pending)
	observabilityNoEvent(t, recorder)
	if metric := observabilityMetric(t, registry, "osac_fabric_domain_provisioning_duration_seconds", durationLabels); metric.GetHistogram().GetSampleCount() != 1 {
		t.Fatalf("disabled provisioning counted as provisioned: %v", metric)
	}
}

func TestFabricDomainObserveDeleted(t *testing.T) {
	r, _, recorder := observabilityReconciler(observabilityClient(t))
	r.recordFabricDomainDeleted(observabilityDomain("gone", "networking", "tenant-a"))
	observabilityEvent(t, recorder, corev1.EventTypeNormal, fabricDomainDeletedEvent)
	r.Recorder = nil
	r.recordFabricDomainDeleted(observabilityDomain("gone", "networking", "tenant-a"))
}

var _ = Describe("FabricDomain observability through the reconciler", func() {
	It("observes Ready, Failed and Deleted once, after the write", func() {
		h := newNetHarness(true)
		h.readyVirtualNetwork("vn-1", true)
		h.networkClass(true, true)
		recorder := events.NewFakeRecorder(20)
		o := newFabricDomainObservability(k8sClient, h.ns)
		registry := prometheus.NewPedanticRegistry()
		registry.MustRegister(o)
		r := &FabricDomainReconciler{Client: k8sClient, Engine: h.engine, NetworkingNamespace: h.ns,
			Recorder: recorder, observability: o}
		newDomain := func(id string) *v1alpha1.FabricDomain {
			fd := &v1alpha1.FabricDomain{ObjectMeta: h.meta(kindFabricDomain, id),
				Spec: v1alpha1.FabricDomainSpec{Type: v1alpha1.FabricDomainTypeEthernetEW, Servers: []string{"host-a"}, VirtualNetwork: "vn-1"}}
			Expect(k8sClient.Create(ctx, fd)).To(Succeed())
			return fd
		}
		count := func(name string, labels map[string]string) float64 {
			families, err := registry.Gather()
			Expect(err).NotTo(HaveOccurred())
			for _, family := range families {
				for _, metric := range family.Metric {
					if family.GetName() != name || len(metric.Label) != len(labels) {
						continue
					}
					match := true
					for _, label := range metric.Label {
						match = match && labels[label.GetName()] == label.GetValue()
					}
					if match && metric.Histogram != nil {
						return float64(metric.Histogram.GetSampleCount())
					}
					if match {
						return metric.Counter.GetValue()
					}
				}
			}
			return 0
		}
		durationLabels := map[string]string{"type": "ethernet_ew"}
		failureLabels := map[string]string{"type": "ethernet_ew", "reason": v1alpha1.ReasonFailed}

		fd := newDomain("fd-ok")
		reconcileNet(r, fd)
		Expect(recorder.Events).To(BeEmpty())
		h.aap.succeed(networktask.CreateFabricDomain, netFabric)
		reconcileNet(r, fd)
		reconcileNet(r, fd)
		Expect(recorder.Events).To(Receive(ContainSubstring(fabricDomainProvisionedEvent)))
		Expect(recorder.Events).To(BeEmpty())
		Expect(count("osac_fabric_domain_provisioning_duration_seconds", durationLabels)).To(Equal(1.0))

		deleteNet(fd)
		reconcileNet(r, fd)
		Expect(recorder.Events).To(BeEmpty())
		h.aap.succeed(networktask.DeleteFabricDomain, netFabric)
		_, found := reconcileNet(r, fd)
		Expect(found).To(BeFalse())
		Expect(recorder.Events).To(Receive(ContainSubstring(fabricDomainDeletedEvent)))

		bad := newDomain("fd-bad")
		reconcileNet(r, bad)
		h.aap.finish(networktask.CreateFabricDomain, netFabric, "failed", `{"osac_error": {"message": "no", "retryable": false}}`)
		reconcileNet(r, bad)
		reconcileNet(r, bad)
		Expect(recorder.Events).To(Receive(And(ContainSubstring("Warning"), ContainSubstring(fabricDomainFailedEvent))))
		Expect(recorder.Events).To(BeEmpty())
		Expect(count("osac_fabric_domain_provisioning_failures_total", failureLabels)).To(Equal(1.0))
	})
})

func TestFabricDomainRegisterObservability(t *testing.T) {
	r := &FabricDomainReconciler{Client: observabilityClient(t, observabilityDomain("a", "networking", "tenant-a")),
		NetworkingNamespace: "networking"}
	registry := prometheus.NewPedanticRegistry()
	if err := r.registerObservability(registry); err != nil {
		t.Fatal(err)
	}
	if r.observability == nil {
		t.Fatal("registration must build the collector from the reconciler client")
	}
	if metric := observabilityMetric(t, registry, "osac_fabric_domains_total",
		map[string]string{"type": "ethernet_ew", "tenant": "tenant-a"}); metric.GetGauge().GetValue() != 1 {
		t.Fatalf("registered collector does not report the inventory: %v", metric)
	}
	if err := r.registerObservability(registry); err == nil {
		t.Fatal("a second registration must fail, not panic")
	}
}

var _ = Describe("FabricDomain observability registration", func() {
	It("SetupWithManager registers the collector on the manager metrics registry", func() {
		h := newNetHarness(true)
		r := &FabricDomainReconciler{Client: indexedClient, Engine: h.engine, NetworkingNamespace: h.ns}
		Expect(r.SetupWithManager(testMcManager)).To(Succeed())
		Expect(r.observability).NotTo(BeNil(), "SetupWithManager must register the collector")
		DeferCleanup(func() { metrics.Registry.Unregister(r.observability) })
		fd := &v1alpha1.FabricDomain{ObjectMeta: h.meta(kindFabricDomain, "fd-reg"),
			Spec: v1alpha1.FabricDomainSpec{Type: v1alpha1.FabricDomainTypeEthernetEW, Servers: []string{"h"}, VirtualNetwork: "vn-x"}}
		Expect(k8sClient.Create(ctx, fd)).To(Succeed())
		Eventually(func(g Gomega) {
			families, err := metrics.Registry.Gather()
			g.Expect(err).NotTo(HaveOccurred())
			found := false
			for _, family := range families {
				if family.GetName() == "osac_fabric_domains_total" {
					for _, m := range family.Metric {
						found = found || m.GetGauge().GetValue() >= 1
					}
				}
			}
			g.Expect(found).To(BeTrue())
		}).Should(Succeed())
	})
})

// fabricDomainConflicts wraps k8sClient so the next status update, or the next
// update of a deleting object, fails with a conflict. The engine keeps the
// plain client, so only the reconcile loop's own writes are hit.
type fabricDomainConflicts struct{ status, finalizer bool }

func (f *fabricDomainConflicts) client() client.Client {
	wc, err := client.NewWithWatch(cfg, client.Options{Scheme: k8sClient.Scheme()})
	Expect(err).NotTo(HaveOccurred())
	conflict := func(name string) error {
		return apierrors.NewConflict(schema.GroupResource{Resource: "fabricdomains"}, name, errors.New("injected"))
	}
	return interceptor.NewClient(wc, interceptor.Funcs{
		SubResourceUpdate: func(c context.Context, cl client.Client, sub string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			if f.status {
				f.status = false
				return conflict(obj.GetName())
			}
			return cl.SubResource(sub).Update(c, obj, opts...)
		},
		Update: func(c context.Context, cl client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if f.finalizer && obj.GetDeletionTimestamp() != nil {
				f.finalizer = false
				return conflict(obj.GetName())
			}
			return cl.Update(c, obj, opts...)
		},
	})
}

var _ = Describe("FabricDomain observability conflicts, restarts and toggles", func() {
	var (
		h        *netHarness
		r        *FabricDomainReconciler
		recorder *events.FakeRecorder
		registry *prometheus.Registry
		inject   *fabricDomainConflicts
	)
	samples := func(reg *prometheus.Registry) int {
		families, err := reg.Gather()
		Expect(err).NotTo(HaveOccurred())
		for _, family := range families {
			if family.GetName() == "osac_fabric_domain_provisioning_duration_seconds" {
				return int(family.Metric[0].Histogram.GetSampleCount())
			}
		}
		return 0
	}
	newReconciler := func(c client.Client) (*FabricDomainReconciler, *prometheus.Registry, *events.FakeRecorder) {
		o := newFabricDomainObservability(c, h.ns)
		reg := prometheus.NewPedanticRegistry()
		reg.MustRegister(o)
		rec := events.NewFakeRecorder(20)
		return &FabricDomainReconciler{Client: c, Engine: h.engine, NetworkingNamespace: h.ns,
			Recorder: rec, observability: o}, reg, rec
	}
	newDomain := func(id string) *v1alpha1.FabricDomain {
		fd := &v1alpha1.FabricDomain{ObjectMeta: h.meta(kindFabricDomain, id),
			Spec: v1alpha1.FabricDomainSpec{Type: v1alpha1.FabricDomainTypeEthernetEW, Servers: []string{"host-a"}, VirtualNetwork: "vn-1"}}
		Expect(k8sClient.Create(ctx, fd)).To(Succeed())
		return fd
	}
	BeforeEach(func() {
		h = newNetHarness(true)
		h.readyVirtualNetwork("vn-1", true)
		h.networkClass(true, true)
		inject = &fabricDomainConflicts{}
		r, registry, recorder = newReconciler(inject.client())
	})

	It("emits nothing for a conflicted final status write, then once on retry", func() {
		fd := newDomain("fd-c1")
		reconcileNet(r, fd)
		h.aap.succeed(networktask.CreateFabricDomain, netFabric)
		inject.status = true
		result, _ := reconcileNet(r, fd)
		Expect(result.RequeueAfter).NotTo(BeZero())
		Expect(recorder.Events).To(BeEmpty())
		Expect(samples(registry)).To(Equal(0))
		reconcileNet(r, fd)
		Expect(recorder.Events).To(Receive(ContainSubstring(fabricDomainProvisionedEvent)))
		Expect(recorder.Events).To(BeEmpty())
		Expect(samples(registry)).To(Equal(1))
	})

	It("emits Deleted only after the finalizer removal persisted", func() {
		fd := newDomain("fd-c2")
		reconcileNet(r, fd)
		h.aap.succeed(networktask.CreateFabricDomain, netFabric)
		reconcileNet(r, fd)
		<-recorder.Events
		deleteNet(fd)
		reconcileNet(r, fd)
		h.aap.succeed(networktask.DeleteFabricDomain, netFabric)
		inject.finalizer = true
		_, found := reconcileNet(r, fd)
		Expect(found).To(BeTrue())
		Expect(recorder.Events).To(BeEmpty())
		_, found = reconcileNet(r, fd)
		Expect(found).To(BeFalse())
		Expect(recorder.Events).To(Receive(ContainSubstring(fabricDomainDeletedEvent)))
		Expect(recorder.Events).To(BeEmpty())
	})

	It("emits no Deleted Event while the delete task fails", func() {
		fd := newDomain("fd-c3")
		reconcileNet(r, fd)
		h.aap.succeed(networktask.CreateFabricDomain, netFabric)
		reconcileNet(r, fd)
		<-recorder.Events
		deleteNet(fd)
		reconcileNet(r, fd)
		h.aap.finish(networktask.DeleteFabricDomain, netFabric, "failed", `{"osac_error": {"message": "x", "retryable": false}}`)
		_, found := reconcileNet(r, fd)
		Expect(found).To(BeTrue())
		reconcileNet(r, fd)
		Expect(recorder.Events).To(BeEmpty())
	})

	It("does not sample or emit again for a Ready domain after a restart", func() {
		fd := newDomain("fd-c4")
		reconcileNet(r, fd)
		h.aap.succeed(networktask.CreateFabricDomain, netFabric)
		reconcileNet(r, fd)
		Expect(samples(registry)).To(Equal(1))
		restarted, restartedRegistry, restartedRecorder := newReconciler(k8sClient)
		reconcileNet(restarted, fd)
		reconcileNet(restarted, fd)
		Expect(restartedRecorder.Events).To(BeEmpty())
		Expect(samples(restartedRegistry)).To(Equal(0))

	})

	It("counts a provisioning toggle on a provisioned domain as no new first Ready", func() {
		fd := newDomain("fd-c5")
		reconcileNet(r, fd)
		h.aap.succeed(networktask.CreateFabricDomain, netFabric)
		reconcileNet(r, fd)
		<-recorder.Events
		enabled := r.Engine
		r.Engine = networktask.New(k8sClient, k8sClient, h.aap, networktask.Config{
			TemplatePrefix: "osac", Organization: "osac", ProvisioningEnabled: false})
		reconcileNet(r, fd)
		expectReady(fd, metav1.ConditionTrue, v1alpha1.ReasonProvisioningDisabled)
		r.Engine = enabled
		reconcileNet(r, fd)
		expectReady(fd, metav1.ConditionTrue, v1alpha1.ReasonSucceeded)
		Expect(recorder.Events).To(BeEmpty())
		Expect(samples(registry)).To(Equal(1))
	})

	It("does not count Ready with provisioning disabled as provisioned", func() {
		r.Engine = networktask.New(k8sClient, k8sClient, h.aap, networktask.Config{
			TemplatePrefix: "osac", Organization: "osac", ProvisioningEnabled: false})
		fd := newDomain("fd-c6")
		reconcileNet(r, fd)
		expectReady(fd, metav1.ConditionTrue, v1alpha1.ReasonProvisioningDisabled)
		Expect(recorder.Events).To(BeEmpty())
		Expect(samples(registry)).To(Equal(0))
	})
})
