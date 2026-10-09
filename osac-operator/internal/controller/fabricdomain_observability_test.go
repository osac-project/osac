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

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
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
		Client: c, APIReader: c, NetworkingNamespace: "networking", Recorder: recorder, observability: o,
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

func observabilityPersist(t *testing.T, r *FabricDomainReconciler, domain *v1alpha1.FabricDomain) {
	t.Helper()
	if err := r.persistFabricDomainStatusAndObserve(context.Background(), domain); err != nil {
		t.Fatal(err)
	}
}

func TestFabricDomainObservabilityInventory(t *testing.T) {
	a := observabilityDomain("a", "networking", "tenant-a")
	b := observabilityDomain("b", "networking", "tenant-a")
	b.Annotations[osacManagementStateAnnotation] = ManagementStateUnmanaged
	c := observabilityDomain("c", "networking", "tenant-b")
	c.Spec.Type = v1alpha1.FabricDomainTypeNVLink
	deleting := observabilityDomain("deleting", "networking", "tenant-a")
	now := metav1.Now()
	deleting.DeletionTimestamp = &now
	deleting.Finalizers = []string{osacFabricDomainFinalizer}
	other := observabilityDomain("other", "elsewhere", "tenant-a")
	noTenant := observabilityDomain("no-tenant", "networking", "")
	cached := observabilityClient(t, a, b, c, deleting, other, noTenant)
	_, registry, _ := observabilityReconciler(cached)
	labels := map[string]string{"type": "EthernetEW", "tenant": "tenant-a"}
	metric := observabilityMetric(t, registry, "osac_fabric_domains_total", labels)
	if metric.GetGauge().GetValue() != 2 {
		t.Fatalf("expected two existing domains without reconciling: %v", metric)
	}
	if metric := observabilityMetric(t, registry, "osac_fabric_domains_total",
		map[string]string{"type": "NVLink", "tenant": "tenant-b"}); metric.GetGauge().GetValue() != 1 {
		t.Fatalf("missing second type/tenant: %v", metric)
	}
	if metric := observabilityMetric(t, registry, "osac_fabric_domains_total",
		map[string]string{"type": "EthernetEW", "tenant": ""}); metric.GetGauge().GetValue() != 1 {
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

func TestFabricDomainObservabilityFirstReadyAndRestart(t *testing.T) {
	domain := observabilityDomain("domain", "networking", "tenant-a")
	c := observabilityClient(t, domain)
	r, registry, recorder := observabilityReconciler(c)
	domain.Status.Phase = v1alpha1.FabricDomainPhaseReady
	setReadyConditionTrue(&domain.Status.Conditions)
	stale := domain.DeepCopy()
	observabilityPersist(t, r, domain)
	observabilityEvent(t, recorder, corev1.EventTypeNormal, fabricDomainProvisionedEvent)
	if domain.Status.ProvisionedAt == nil {
		t.Fatal("first Ready timestamp was not persisted")
	}
	labels := map[string]string{"type": "EthernetEW"}
	metric := observabilityMetric(t, registry, "osac_fabric_domain_provisioning_duration_seconds", labels)
	wantSeconds := domain.Status.ProvisionedAt.Sub(domain.CreationTimestamp.Time).Seconds()
	if metric.GetHistogram().GetSampleCount() != 1 || math.Abs(metric.GetHistogram().GetSampleSum()-wantSeconds) > 1 {
		t.Fatalf("expected one creation-to-Ready sample (%f seconds): %v", wantSeconds, metric)
	}
	observabilityPersist(t, r, stale)
	observabilityNoEvent(t, recorder)
	if metric := observabilityMetric(t, registry, "osac_fabric_domain_provisioning_duration_seconds", labels); metric.GetHistogram().GetSampleCount() != 1 {
		t.Fatalf("stale reconcile duplicated duration: %v", metric)
	}
	r, restarted, recorder := observabilityReconciler(c)
	observabilityPersist(t, r, stale)
	observabilityNoEvent(t, recorder)
	// Resize/recovery has another Ready event but no second initial-duration sample.
	domain.Status.Phase = v1alpha1.FabricDomainPhaseProgressing
	setFabricDomainCondition(domain, metav1.ConditionFalse, "Provisioning", "membership update")
	observabilityPersist(t, r, domain)
	domain.Status.Phase = v1alpha1.FabricDomainPhaseReady
	setReadyConditionTrue(&domain.Status.Conditions)
	observabilityPersist(t, r, domain)
	observabilityEvent(t, recorder, corev1.EventTypeNormal, fabricDomainProvisionedEvent)
	if metric := observabilityMetric(t, restarted, "osac_fabric_domain_provisioning_duration_seconds", labels); metric != nil {
		t.Fatalf("restart/resize duplicated duration: %v", metric)
	}
}

func TestFabricDomainObservabilityFailures(t *testing.T) {
	domain := observabilityDomain("domain", "networking", "tenant-a")
	c := observabilityClient(t, domain)
	r, registry, recorder := observabilityReconciler(c)
	domain.Status.Phase = v1alpha1.FabricDomainPhaseFailed
	setFabricDomainCondition(domain, metav1.ConditionFalse, "InvalidHardwareBinding", "invalid binding")
	observabilityPersist(t, r, domain)
	observabilityEvent(t, recorder, corev1.EventTypeWarning, fabricDomainFailedEvent)
	setFabricDomainCondition(domain, metav1.ConditionFalse, "InvalidHardwareBinding", "retry message changed")
	observabilityPersist(t, r, domain)
	observabilityNoEvent(t, recorder)
	labels := map[string]string{"type": "EthernetEW", "reason": "InvalidHardwareBinding"}
	if metric := observabilityMetric(t, registry, "osac_fabric_domain_provisioning_failures_total", labels); metric.GetCounter().GetValue() != 1 {
		t.Fatalf("repeated failure counted more than once: %v", metric)
	}
	r, restarted, recorder := observabilityReconciler(c)
	observabilityPersist(t, r, domain)
	observabilityNoEvent(t, recorder)
	if metric := observabilityMetric(t, restarted, "osac_fabric_domain_provisioning_failures_total", labels); metric != nil {
		t.Fatalf("restart duplicated failure: %v", metric)
	}
	setFabricDomainCondition(domain, metav1.ConditionFalse, "UnsupportedType", "different failure")
	observabilityPersist(t, r, domain)
	observabilityEvent(t, recorder, corev1.EventTypeWarning, fabricDomainFailedEvent)
	labels["reason"] = "UnsupportedType"
	if metric := observabilityMetric(t, restarted, "osac_fabric_domain_provisioning_failures_total", labels); metric.GetCounter().GetValue() != 1 {
		t.Fatalf("reason transition was not counted: %v", metric)
	}
}

func TestFabricDomainObservabilityPersistenceFailureAndConflict(t *testing.T) {
	for _, ready := range []bool{false, true} {
		t.Run(map[bool]string{false: "Failed", true: "Ready"}[ready], func(t *testing.T) {
			domain := observabilityDomain("domain", "networking", "tenant-a")
			base := observabilityClient(t, domain)
			writeErr := errors.New("status update unavailable")
			attempts := 0
			c := interceptor.NewClient(base, interceptor.Funcs{
				SubResourceUpdate: func(ctx context.Context, c client.Client, subresource string,
					obj client.Object, opts ...client.SubResourceUpdateOption) error {
					attempts++
					if writeErr != nil {
						return writeErr
					}
					if attempts == 2 {
						return apierrors.NewConflict(schema.GroupResource{Resource: "fabricdomains"}, domain.Name,
							errors.New("concurrent update"))
					}
					return c.SubResource(subresource).Update(ctx, obj, opts...)
				},
			})
			r, registry, recorder := observabilityReconciler(c)
			domain.Status.Phase = v1alpha1.FabricDomainPhaseFailed
			setReadyConditionFailed(&domain.Status.Conditions, "failed")
			metricName := "osac_fabric_domain_provisioning_failures_total"
			labels := map[string]string{"type": "EthernetEW", "reason": v1alpha1.ReasonProvisioningFailed}
			if ready {
				domain.Status.Phase = v1alpha1.FabricDomainPhaseReady
				setReadyConditionTrue(&domain.Status.Conditions)
				metricName = "osac_fabric_domain_provisioning_duration_seconds"
				delete(labels, "reason")
			}
			if err := r.persistFabricDomainStatusAndObserve(context.Background(), domain); !errors.Is(err, writeErr) {
				t.Fatalf("expected write failure, got %v", err)
			}
			observabilityNoEvent(t, recorder)
			if metric := observabilityMetric(t, registry, metricName, labels); metric != nil {
				t.Fatalf("unsaved status emitted metric: %v", metric)
			}
			writeErr = nil
			observabilityPersist(t, r, domain)
			if attempts != 3 {
				t.Fatalf("expected retry after conflict, got %d attempts", attempts)
			}
			if ready {
				observabilityEvent(t, recorder, corev1.EventTypeNormal, fabricDomainProvisionedEvent)
			} else {
				observabilityEvent(t, recorder, corev1.EventTypeWarning, fabricDomainFailedEvent)
			}
			observabilityNoEvent(t, recorder)
			metric := observabilityMetric(t, registry, metricName, labels)
			if ready && metric.GetHistogram().GetSampleCount() != 1 || !ready && metric.GetCounter().GetValue() != 1 {
				t.Fatalf("successful retry must emit once: %v", metric)
			}
		})
	}
}

func TestFabricDomainObservabilityReadyBackfill(t *testing.T) {
	domain := observabilityDomain("legacy", "networking", "tenant-a")
	domain.Status.Phase = v1alpha1.FabricDomainPhaseReady
	setReadyConditionTrue(&domain.Status.Conditions)
	r, registry, recorder := observabilityReconciler(observabilityClient(t, domain))
	observabilityPersist(t, r, domain)
	if domain.Status.ProvisionedAt == nil {
		t.Fatal("missing backfilled marker")
	}
	observabilityNoEvent(t, recorder)
	if metric := observabilityMetric(t, registry, "osac_fabric_domain_provisioning_duration_seconds",
		map[string]string{"type": "EthernetEW"}); metric != nil {
		t.Fatalf("backfill invented an initial Ready observation: %v", metric)
	}
}

func TestFabricDomainObservabilityRequiresDomainReady(t *testing.T) {
	domain := observabilityDomain("domain", "networking", "tenant-a")
	r, registry, recorder := observabilityReconciler(observabilityClient(t, domain))
	domain.Status.Phase = v1alpha1.FabricDomainPhaseProgressing
	setFabricDomainCondition(domain, metav1.ConditionFalse, "ProvisioningDisabled", "enable provisioning")
	// A job-level success is not sufficient to declare the domain provisioned.
	domain.Status.ProvisioningJobs = []v1alpha1.JobStatus{{Type: v1alpha1.JobTypeProvision, State: v1alpha1.JobStateSucceeded}}
	observabilityPersist(t, r, domain)
	observabilityNoEvent(t, recorder)
	domain.Status.Phase = v1alpha1.FabricDomainPhaseReady
	observabilityPersist(t, r, domain)
	observabilityNoEvent(t, recorder)
	if domain.Status.ProvisionedAt != nil {
		t.Fatal("phase/job success without Ready=True must not set first-Ready timestamp")
	}
	if metric := observabilityMetric(t, registry, "osac_fabric_domain_provisioning_duration_seconds",
		map[string]string{"type": "EthernetEW"}); metric != nil {
		t.Fatalf("unready domain emitted duration: %v", metric)
	}
}

func TestFabricDomainObservabilityStaleIntermediateFlush(t *testing.T) {
	domain := observabilityDomain("domain", "networking", "tenant-a")
	stale := domain.DeepCopy()
	// metav1.Time is serialized at second precision by the status update.
	now := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
	domain.Status.ProvisionedAt = &now
	c := observabilityClient(t, domain)
	r, _, recorder := observabilityReconciler(c)
	if err := r.restoreFabricDomainProvisionedAt(context.Background(), stale); err != nil {
		t.Fatal(err)
	}
	if err := r.updateStatusWithRetry(context.Background(), client.ObjectKeyFromObject(stale), stale.Status); err != nil {
		t.Fatal(err)
	}
	persisted := &v1alpha1.FabricDomain{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(stale), persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Status.ProvisionedAt == nil || !persisted.Status.ProvisionedAt.Equal(&now) {
		t.Fatalf("intermediate status flush changed first-Ready marker: got %v, want %v", persisted.Status.ProvisionedAt, now)
	}
	observabilityNoEvent(t, recorder)
}

func TestFabricDomainObservabilityCleanup(t *testing.T) {
	for _, failUpdate := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "write-failure"}[failUpdate], func(t *testing.T) {
			domain := observabilityDomain("deleting", "networking", "tenant-a")
			now := metav1.Now()
			domain.DeletionTimestamp = &now
			domain.Finalizers = []string{osacFabricDomainFinalizer, "test/retain"}
			base := observabilityClient(t, domain)
			c := interceptor.NewClient(base, interceptor.Funcs{
				Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
					if failUpdate {
						return errors.New("finalizer update unavailable")
					}
					return c.Update(ctx, obj, opts...)
				},
			})
			r, _, recorder := observabilityReconciler(c)
			request := mcreconcile.Request{Request: reconcile.Request{NamespacedName: client.ObjectKeyFromObject(domain)}}
			_, err := r.Reconcile(context.Background(), request)
			if failUpdate {
				if err == nil {
					t.Fatal("expected finalizer update failure")
				}
				observabilityNoEvent(t, recorder)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			observabilityEvent(t, recorder, corev1.EventTypeNormal, fabricDomainDeletedEvent)
			if _, err := r.Reconcile(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			observabilityNoEvent(t, recorder)
		})
	}
	r, _, recorder := observabilityReconciler(observabilityClient(t))
	domain := observabilityDomain("unsuccessful-cleanup", "networking", "tenant-a")
	domain.Status.ProvisioningJobs = []v1alpha1.JobStatus{{Type: v1alpha1.JobTypeDeprovision, State: v1alpha1.JobStateFailed}}
	r.recordFabricDomainDeleted(domain)
	observabilityNoEvent(t, recorder)
}
