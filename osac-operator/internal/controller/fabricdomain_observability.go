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
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/networktask"
)

const (
	fabricDomainProvisionedEvent  = "FabricDomainProvisioned"
	fabricDomainFailedEvent       = "FabricDomainProvisioningFailed"
	fabricDomainDeletedEvent      = "FabricDomainDeleted"
	fabricDomainTenantMetricLabel = "tenant"

	maxFabricDomainEventMessage = 200
	fabricDomainTypeMetricLabel = "type"
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
			Help:    "Seconds from CR creation to its first persisted Ready state after a task ran.",
			Buckets: []float64{1, 5, 15, 30, 60, 120, 300, 600, 1200, 1800, 3600},
		}, []string{fabricDomainTypeMetricLabel}),
		failures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "osac_fabric_domain_provisioning_failures_total",
			Help: "Persisted transitions into FabricDomain Failed; excludes repeated reconciles.",
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
	return r.registerObservability(metrics.Registry)
}

func (r *FabricDomainReconciler) registerObservability(reg prometheus.Registerer) error {
	if r.observability == nil {
		r.observability = newFabricDomainObservability(r.Client, r.NetworkingNamespace)
	}
	if err := reg.Register(r.observability); err != nil {
		return fmt.Errorf("registering FabricDomain metrics: %w", err)
	}
	return nil
}

// fabricDomainProvisioned reports Ready after a task ran. Ready with
// ProvisioningDisabled counts only if the create task already succeeded,
// so toggling provisioning off and on is not a new first Ready.
func fabricDomainProvisioned(fd *v1alpha1.FabricDomain) bool {
	state, _ := networkFeedback(fd, fd.Status.Conditions)
	ready := apimeta.FindStatusCondition(fd.Status.Conditions, v1alpha1.NetworkConditionReady)
	return state == networkFeedbackReady && ready != nil &&
		(ready.Reason != v1alpha1.ReasonProvisioningDisabled ||
			networktask.Succeeded(fd, networktask.CreateFabricDomain, v1alpha1.NetworkJobManagerFabric))
}

// observeTransition runs after the status write succeeded. A requeue that
// leaves the state unchanged counts and emits nothing.
func (r *FabricDomainReconciler) observeTransition(fd, before *v1alpha1.FabricDomain) {
	stateBefore, _ := networkFeedback(before, before.Status.Conditions)
	stateAfter, _ := networkFeedback(fd, fd.Status.Conditions)
	if fabricDomainProvisioned(fd) && !fabricDomainProvisioned(before) {
		if r.observability != nil && !fd.CreationTimestamp.IsZero() {
			if seconds := time.Since(fd.CreationTimestamp.Time).Seconds(); seconds >= 0 {
				r.observability.duration.WithLabelValues(string(fd.Spec.Type)).Observe(seconds)
			}
		}
		if r.Recorder != nil {
			r.Recorder.Eventf(fd, nil, corev1.EventTypeNormal, fabricDomainProvisionedEvent,
				"Provision", "FabricDomain reached Ready")
		}
	}
	if stateAfter == networkFeedbackFailed && stateBefore != stateAfter {
		reason := fabricDomainFailureReason(fd)
		if r.observability != nil {
			r.observability.failures.WithLabelValues(string(fd.Spec.Type), reason).Inc()
		}
		if r.Recorder != nil {
			r.Recorder.Eventf(fd, nil, corev1.EventTypeWarning, fabricDomainFailedEvent,
				"Provision", "FabricDomain entered Failed (reason: %s): %s", reason, fabricDomainFailureMessage(fd))
		}
	}
}

func fabricDomainFailureReason(fd *v1alpha1.FabricDomain) string {
	condition := apimeta.FindStatusCondition(fd.Status.Conditions, v1alpha1.NetworkConditionReady)
	if condition != nil && condition.Status == metav1.ConditionFalse && condition.Reason != "" {
		return condition.Reason
	}
	return v1alpha1.ReasonFailed
}

// fabricDomainFailureMessage is the Ready condition message, valid UTF-8 and
// short enough for an Event.
func fabricDomainFailureMessage(fd *v1alpha1.FabricDomain) string {
	condition := apimeta.FindStatusCondition(fd.Status.Conditions, v1alpha1.NetworkConditionReady)
	if condition == nil {
		return ""
	}
	message := sanitizeFeedbackText(condition.Message)
	if runes := []rune(message); len(runes) > maxFabricDomainEventMessage {
		message = string(runes[:maxFabricDomainEventMessage]) + "..."
	}
	return message
}

// recordFabricDomainDeleted runs only after the finalizer removal persisted,
// which the shared loop allows only once the delete task succeeded.
func (r *FabricDomainReconciler) recordFabricDomainDeleted(fd *v1alpha1.FabricDomain) {
	if r.Recorder == nil {
		return
	}
	r.Recorder.Eventf(fd, nil, corev1.EventTypeNormal, fabricDomainDeletedEvent,
		"Delete", "FabricDomain cleanup completed")
}
