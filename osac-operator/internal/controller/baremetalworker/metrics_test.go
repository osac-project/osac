/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package baremetalworker

import (
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

func histogramSampleCount(vec *prometheus.HistogramVec, tenant, instanceType string) uint64 {
	obs, err := vec.GetMetricWithLabelValues(tenant, workerTypeBareMetal, instanceType)
	Expect(err).NotTo(HaveOccurred())
	var m dto.Metric
	Expect(obs.(prometheus.Metric).Write(&m)).To(Succeed())
	return m.GetHistogram().GetSampleCount()
}

func coWithWorkers(name, tenant string, workers ...v1alpha1.WorkerStatus) v1alpha1.ClusterOrder {
	return v1alpha1.ClusterOrder{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Annotations: map[string]string{tenantAnnotationKey: tenant},
		},
		Status: v1alpha1.ClusterOrderStatus{Workers: workers},
	}
}

var _ = Describe("worker gauges", func() {
	It("aggregates desired/ready by tenant and instance_type, labeled worker_type=bare_metal", func() {
		updateWorkerGauges([]v1alpha1.ClusterOrder{
			coWithWorkers("cluster-a", "tenant-1",
				v1alpha1.WorkerStatus{InstanceType: "bm.large", Name: "a-0", Phase: workerPhaseReady},
				v1alpha1.WorkerStatus{InstanceType: "bm.large", Name: "a-1", Phase: workerPhaseWaitingForAgent},
				v1alpha1.WorkerStatus{InstanceType: "bm.gpu", Name: "a-2", Phase: workerPhaseFailed},
			),
			coWithWorkers("cluster-b", "tenant-2",
				v1alpha1.WorkerStatus{InstanceType: "bm.large", Name: "b-0", Phase: workerPhaseReady},
			),
		})

		expected := `
# HELP osac_caas_worker_desired Desired number of CaaS workers, by tenant, worker type, and instance type.
# TYPE osac_caas_worker_desired gauge
osac_caas_worker_desired{instance_type="bm.large",tenant="tenant-1",worker_type="bare_metal"} 2
osac_caas_worker_desired{instance_type="bm.gpu",tenant="tenant-1",worker_type="bare_metal"} 1
osac_caas_worker_desired{instance_type="bm.large",tenant="tenant-2",worker_type="bare_metal"} 1
# HELP osac_caas_worker_ready Ready CaaS workers, by tenant, worker type, and instance type.
# TYPE osac_caas_worker_ready gauge
osac_caas_worker_ready{instance_type="bm.large",tenant="tenant-1",worker_type="bare_metal"} 1
osac_caas_worker_ready{instance_type="bm.gpu",tenant="tenant-1",worker_type="bare_metal"} 0
osac_caas_worker_ready{instance_type="bm.large",tenant="tenant-2",worker_type="bare_metal"} 1
`
		Expect(testutil.CollectAndCompare(workerDesired, strings.NewReader(expected), "osac_caas_worker_desired")).To(Succeed())
		Expect(testutil.CollectAndCompare(workerReady, strings.NewReader(expected), "osac_caas_worker_ready")).To(Succeed())
	})

	It("clears stale series for clusters that no longer have workers", func() {
		updateWorkerGauges([]v1alpha1.ClusterOrder{
			coWithWorkers("cluster-stale", "tenant-gone",
				v1alpha1.WorkerStatus{InstanceType: "bm.large", Name: "s-0", Phase: workerPhaseReady},
			),
		})
		Expect(testutil.CollectAndCount(workerDesired)).To(BeNumerically(">", 0))

		updateWorkerGauges(nil)
		Expect(testutil.CollectAndCount(workerDesired)).To(Equal(0))
		Expect(testutil.CollectAndCount(workerReady)).To(Equal(0))
	})

	It("emits nothing when a ClusterOrder has no workers", func() {
		updateWorkerGauges([]v1alpha1.ClusterOrder{coWithWorkers("empty", "tenant-1")})
		Expect(testutil.CollectAndCount(workerDesired)).To(Equal(0))
	})
})

var _ = Describe("worker provisioning failures counter", func() {
	It("increments per failure, keyed by tenant and instance_type", func() {
		w := v1alpha1.WorkerStatus{InstanceType: "bm.fail", Name: "f-0", Phase: workerPhaseFailed}
		before := testutil.ToFloat64(workerProvisioningFailures.WithLabelValues("tenant-f", workerTypeBareMetal, "bm.fail"))

		observeProvisioningFailure("tenant-f", w)
		observeProvisioningFailure("tenant-f", w)

		after := testutil.ToFloat64(workerProvisioningFailures.WithLabelValues("tenant-f", workerTypeBareMetal, "bm.fail"))
		Expect(after - before).To(Equal(float64(2)))
	})
})

var _ = Describe("worker duration histograms", func() {
	It("observes provisioning duration keyed by tenant and instance_type", func() {
		w := v1alpha1.WorkerStatus{
			InstanceType:      "hist-prov",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-2 * time.Minute)),
		}
		before := histogramSampleCount(workerProvisioningDuration, "tenant-h", "hist-prov")
		observeProvisioningDuration("tenant-h", w)
		Expect(histogramSampleCount(workerProvisioningDuration, "tenant-h", "hist-prov")).To(Equal(before + 1))
	})

	It("observes correlation duration keyed by tenant and instance_type", func() {
		w := v1alpha1.WorkerStatus{
			InstanceType:      "hist-corr",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-90 * time.Second)),
		}
		before := histogramSampleCount(workerCorrelationDuration, "tenant-h", "hist-corr")
		observeCorrelationDuration("tenant-h", w)
		Expect(histogramSampleCount(workerCorrelationDuration, "tenant-h", "hist-corr")).To(Equal(before + 1))
	})
})
