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
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/pkg/provisioning"
)

const (
	fabricDomainProvisionedEvent  = "FabricDomainProvisioned"
	fabricDomainFailedEvent       = "FabricDomainProvisioningFailed"
	fabricDomainDeletedEvent      = "FabricDomainDeleted"
	fabricDomainTenantMetricLabel = "tenant"
	fabricDomainTypeMetricLabel   = "type"
)

// fabricDomainObservability has no process-global collectors: tests can register
// it in a private registry. The gauge counts all non-deleting CRs visible in the
// local manager's cache and configured networking namespace, including Unmanaged
// and unready domains. An empty namespace means all namespaces in that cache.
// Missing tenant annotations use tenant="". Empty label groups disappear.
// This is resource inventory, not a measure of jobs, members or dataplane health.
type fabricDomainObservability struct {
	reader    client.Reader
	namespace string
	total     *prometheus.Desc
	duration  *prometheus.HistogramVec
	failures  *prometheus.CounterVec
}

func newFabricDomainObservability(reader client.Reader, namespace string) *fabricDomainObservability {
	return &fabricDomainObservability{
		reader: reader, namespace: namespace,
		total: prometheus.NewDesc("osac_fabric_domains_total",
			"Non-deleting FabricDomain CRs in the operator networking namespace, regardless of readiness or management state.",
			[]string{fabricDomainTypeMetricLabel, fabricDomainTenantMetricLabel}, nil),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "osac_fabric_domain_provisioning_duration_seconds",
			Help:    "Seconds from CR creation to its first persisted Ready state; excludes later membership updates.",
			Buckets: []float64{1, 5, 15, 30, 60, 120, 300, 600, 1200, 1800, 3600},
		}, []string{fabricDomainTypeMetricLabel}),
		failures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "osac_fabric_domain_provisioning_failures_total",
			Help: "Persisted transitions into FabricDomain Failed or to a different failure reason; excludes repeated reconciles.",
		}, []string{fabricDomainTypeMetricLabel, "reason"}),
	}
}

func (o *fabricDomainObservability) Describe(ch chan<- *prometheus.Desc) {
	ch <- o.total
	o.duration.Describe(ch)
	o.failures.Describe(ch)
}

func (o *fabricDomainObservability) Collect(ch chan<- prometheus.Metric) {
	// List from the cache on every scrape, including after a restart. Never
	// increment/decrement inventory from reconcile events or retain stale series.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	domains := &v1alpha1.FabricDomainList{}
	if err := o.reader.List(ctx, domains, client.InNamespace(o.namespace)); err != nil {
		ch <- prometheus.NewInvalidMetric(o.total, fmt.Errorf("listing FabricDomains for metrics: %w", err))
	} else {
		type labels struct{ fabricType, tenant string }
		counts := map[labels]int{}
		for i := range domains.Items {
			domain := &domains.Items[i]
			if domain.DeletionTimestamp.IsZero() {
				counts[labels{string(domain.Spec.Type), domain.Annotations[osacTenantKey]}]++
			}
		}
		for label, count := range counts {
			ch <- prometheus.MustNewConstMetric(o.total, prometheus.GaugeValue, float64(count), label.fabricType, label.tenant)
		}
	}
	o.duration.Collect(ch)
	o.failures.Collect(ch)
}

func (r *FabricDomainReconciler) setupFabricDomainObservability() error {
	if r.observability == nil {
		r.observability = newFabricDomainObservability(r.Client, r.NetworkingNamespace)
	}
	if err := metrics.Registry.Register(r.observability); err != nil {
		return fmt.Errorf("registering FabricDomain metrics: %w", err)
	}
	return nil
}

func fabricDomainReady(status v1alpha1.FabricDomainStatus) bool {
	return status.Phase == v1alpha1.FabricDomainPhaseReady &&
		apimeta.IsStatusConditionTrue(status.Conditions, v1alpha1.ConditionReady)
}

// Restore the durable marker before the provisioning lifecycle's intermediate
// status flushes. A stale cache snapshot must not clear it before the final
// persistence/observation hook gets a chance to preserve it.
func (r *FabricDomainReconciler) restoreFabricDomainProvisionedAt(
	ctx context.Context, domain *v1alpha1.FabricDomain,
) error {
	if domain.Status.ProvisionedAt != nil || r.APIReader == nil {
		return nil
	}
	latest := &v1alpha1.FabricDomain{}
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(domain), latest); err != nil {
		return err
	}
	if latest.UID != domain.UID {
		return fmt.Errorf("FabricDomain %q was replaced while reconciling", domain.Name)
	}
	domain.Status.ProvisionedAt = latest.Status.ProvisionedAt.DeepCopy()
	return nil
}

// persistFabricDomainStatusAndObserve compares against the status actually
// replaced by the successful write, not a potentially stale reconcile/cache
// snapshot. Conflicts and failed writes emit nothing. The durable first-Ready
// marker prevents resize/recovery/restart from adding a second duration sample.
// Like normal Kubernetes events/Prometheus counters, observations are best effort:
// a process crash between persistence and emission can lose an observation.
func (r *FabricDomainReconciler) persistFabricDomainStatusAndObserve(
	ctx context.Context, domain *v1alpha1.FabricDomain,
) error {
	reader := r.APIReader
	if reader == nil {
		reader = r.Client
	}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &v1alpha1.FabricDomain{}
		if err := reader.Get(ctx, client.ObjectKeyFromObject(domain), latest); err != nil {
			return err
		}
		if latest.UID != domain.UID {
			return fmt.Errorf("FabricDomain %q was replaced while reconciling", domain.Name)
		}
		before := latest.Status
		latest.Status = domain.Status
		// Never replace a previously persisted timestamp with a stale snapshot.
		latest.Status.ProvisionedAt = before.ProvisionedAt
		if latest.DeletionTimestamp.IsZero() && before.ProvisionedAt == nil {
			if fabricDomainReady(before) {
				// Backfill pre-observability Ready objects without a new sample/event.
				condition := apimeta.FindStatusCondition(before.Conditions, v1alpha1.ConditionReady)
				latest.Status.ProvisionedAt = condition.LastTransitionTime.DeepCopy()
			} else if fabricDomainReady(latest.Status) {
				now := metav1.Now()
				latest.Status.ProvisionedAt = &now
			}
		}
		if equality.Semantic.DeepEqual(before, latest.Status) {
			domain.Status = latest.Status
			return nil
		}
		if err := r.Status().Update(ctx, latest); err != nil {
			return err
		}
		domain.Status = latest.Status
		r.observeFabricDomainTransition(latest, before)
		return nil
	})
}

func (r *FabricDomainReconciler) observeFabricDomainTransition(
	domain *v1alpha1.FabricDomain, before v1alpha1.FabricDomainStatus,
) {
	if domain.DeletionTimestamp.IsZero() && fabricDomainReady(domain.Status) && !fabricDomainReady(before) {
		if r.observability != nil && before.ProvisionedAt == nil && domain.Status.ProvisionedAt != nil &&
			!domain.CreationTimestamp.IsZero() {
			seconds := domain.Status.ProvisionedAt.Sub(domain.CreationTimestamp.Time).Seconds()
			if seconds >= 0 {
				r.observability.duration.WithLabelValues(string(domain.Spec.Type)).Observe(seconds)
			}
		}
		if r.Recorder != nil {
			r.Recorder.Eventf(domain, nil, corev1.EventTypeNormal, fabricDomainProvisionedEvent,
				"Provision", "FabricDomain reached Ready")
		}
	}
	reason := fabricDomainFailureReason(domain.Status)
	if domain.Status.Phase == v1alpha1.FabricDomainPhaseFailed &&
		(before.Phase != v1alpha1.FabricDomainPhaseFailed || fabricDomainFailureReason(before) != reason) {
		if r.observability != nil {
			r.observability.failures.WithLabelValues(string(domain.Spec.Type), reason).Inc()
		}
		if r.Recorder != nil {
			r.Recorder.Eventf(domain, nil, corev1.EventTypeWarning, fabricDomainFailedEvent,
				"Provision", "FabricDomain entered Failed (reason: %s)", reason)
		}
	}
}

func fabricDomainFailureReason(status v1alpha1.FabricDomainStatus) string {
	condition := apimeta.FindStatusCondition(status.Conditions, v1alpha1.ConditionReady)
	if condition != nil && condition.Status == metav1.ConditionFalse && condition.Reason != "" {
		return condition.Reason
	}
	return v1alpha1.ReasonProvisioningFailed
}

// Called only after this reconcile successfully persisted finalizer removal.
// A terminal but unsuccessful non-blocking cleanup job is not successful cleanup.
func (r *FabricDomainReconciler) recordFabricDomainDeleted(domain *v1alpha1.FabricDomain) {
	if r.Recorder == nil {
		return
	}
	job := provisioning.FindLatestJobByType(domain.Status.ProvisioningJobs, v1alpha1.JobTypeDeprovision)
	if job != nil && !job.State.IsSuccessful() {
		return
	}
	r.Recorder.Eventf(domain, nil, corev1.EventTypeNormal, fabricDomainDeletedEvent,
		"Delete", "FabricDomain cleanup completed")
}
