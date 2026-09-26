/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

func p32(i int32) *int32 { return &i }

var _ = Describe("ClusterOrder worker status contract (OSAC-4147)", func() {
	ctx := context.Background()

	It("DeepCopy round-trips a populated WorkerStatus and is independent of the original", func() {
		now := metav1.Now()
		orig := v1alpha1.ClusterOrderStatus{
			DesiredWorkers: p32(2),
			CurrentWorkers: p32(1),
			ReadyWorkers:   p32(1),
			Workers: []v1alpha1.WorkerStatus{
				{
					NodeSet:            "compute",
					Name:               "bm-cluster-a-worker-0",
					Kind:               "BareMetalInstance",
					ResourceID:         "uuid-0",
					Phase:              "Failed",
					AttemptCount:       2,
					CreationTimestamp:  now,
					LastFailureReason:  "AgentRegistrationTimeout",
					LastFailureMessage: "Agent did not register within 30m",
					LastFailureTime:    &now,
					NextRetryTime:      &now,
				},
			},
		}

		cp := orig.DeepCopy()
		Expect(cp.Workers).To(Equal(orig.Workers))
		Expect(cp.DesiredWorkers).To(Equal(orig.DesiredWorkers))

		// Mutating the copy must not affect the original (deep independence),
		// including the pointer fields (aggregates and *metav1.Time).
		cp.Workers[0].Name = "changed"
		*cp.DesiredWorkers = 99
		*cp.Workers[0].LastFailureTime = metav1.Time{}
		Expect(orig.Workers[0].Name).To(Equal("bm-cluster-a-worker-0"))
		Expect(*orig.DesiredWorkers).To(Equal(int32(2)))
		Expect(orig.Workers[0].LastFailureTime.Equal(&now)).To(BeTrue())
	})

	Context("in envtest", func() {
		var key types.NamespacedName

		newClusterOrder := func(name string) *v1alpha1.ClusterOrder {
			return &v1alpha1.ClusterOrder{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec:       v1alpha1.ClusterOrderSpec{TemplateID: "test"},
			}
		}

		AfterEach(func() {
			co := &v1alpha1.ClusterOrder{}
			if err := k8sClient.Get(ctx, key, co); err == nil {
				Expect(k8sClient.Delete(ctx, co)).To(Succeed())
			}
		})

		It("accepts a ClusterOrder with no Workers and nil aggregates (backward compatible)", func() {
			key = types.NamespacedName{Name: "ws-backcompat", Namespace: "default"}
			Expect(k8sClient.Create(ctx, newClusterOrder(key.Name))).To(Succeed())

			got := &v1alpha1.ClusterOrder{}
			Expect(k8sClient.Get(ctx, key, got)).To(Succeed())
			Expect(got.Status.Workers).To(BeEmpty())
			Expect(got.Status.DesiredWorkers).To(BeNil())
			Expect(got.Status.CurrentWorkers).To(BeNil())
			Expect(got.Status.ReadyWorkers).To(BeNil())
		})

		It("patchStatusWithRetry preserves live worker aggregates and Workers[]", func() {
			key = types.NamespacedName{Name: "ws-preserve", Namespace: "default"}
			Expect(k8sClient.Create(ctx, newClusterOrder(key.Name))).To(Succeed())

			// Simulate the BareMetalWorkerReconciler writing Workers[].
			co := &v1alpha1.ClusterOrder{}
			Expect(k8sClient.Get(ctx, key, co)).To(Succeed())
			now := metav1.Now()
			co.Status.Workers = []v1alpha1.WorkerStatus{{
				NodeSet:           "compute",
				Name:              "bm-cluster-a-worker-0",
				Kind:              "BareMetalInstance",
				Phase:             "Ready",
				AttemptCount:      1,
				CreationTimestamp: now,
			}}
			co.Status.DesiredWorkers = p32(1)
			co.Status.CurrentWorkers = p32(1)
			co.Status.ReadyWorkers = p32(1)
			Expect(k8sClient.Status().Update(ctx, co)).To(Succeed())

			// The ClusterOrder controller patches its own fields, not worker state.
			r := &ClusterOrderReconciler{Client: k8sClient, apiReader: k8sClient, Scheme: k8sClient.Scheme()}
			computed := v1alpha1.ClusterOrderStatus{
				Phase:          v1alpha1.ClusterOrderPhaseProgressing,
				DesiredWorkers: p32(1),
				CurrentWorkers: p32(1),
				ReadyWorkers:   p32(0),
			}
			Expect(r.patchStatusWithRetry(ctx, key, computed)).To(Succeed())

			got := &v1alpha1.ClusterOrder{}
			Expect(k8sClient.Get(ctx, key, got)).To(Succeed())
			// Reconciler-owned Workers[] survived the controller's patch.
			Expect(got.Status.Workers).To(HaveLen(1))
			Expect(got.Status.Workers[0].Name).To(Equal("bm-cluster-a-worker-0"))
			// Live worker aggregates and the phase were preserved/applied respectively.
			Expect(got.Status.DesiredWorkers).ToNot(BeNil())
			Expect(*got.Status.DesiredWorkers).To(Equal(int32(1)))
			Expect(*got.Status.CurrentWorkers).To(Equal(int32(1)))
			Expect(*got.Status.ReadyWorkers).To(Equal(int32(1)))
			Expect(got.Status.Phase).To(Equal(v1alpha1.ClusterOrderPhaseProgressing))
		})

		It("patchStatusWithRetry keeps a stale Ready phase progressing while workers are pending", func() {
			key = types.NamespacedName{Name: "ws-stale-ready", Namespace: "default"}
			Expect(k8sClient.Create(ctx, newClusterOrder(key.Name))).To(Succeed())

			co := &v1alpha1.ClusterOrder{}
			Expect(k8sClient.Get(ctx, key, co)).To(Succeed())
			co.Spec.NodeRequests = []v1alpha1.NodeRequest{{
				NumberOfNodes: 1,
				BareMetal:     &v1alpha1.BareMetalNodeSpec{InstanceType: "bm.large"},
			}}
			co.Status.Phase = v1alpha1.ClusterOrderPhaseProgressing
			co.Status.DesiredWorkers = p32(1)
			co.Status.CurrentWorkers = p32(1)
			co.Status.ReadyWorkers = p32(0)
			co.SetStatusCondition(v1alpha1.ConditionProgressing, metav1.ConditionTrue,
				"Workers Joining", v1alpha1.ReasonWorkersJoining)
			Expect(k8sClient.Update(ctx, co)).To(Succeed())
			Expect(k8sClient.Status().Update(ctx, co)).To(Succeed())

			r := &ClusterOrderReconciler{Client: k8sClient, apiReader: k8sClient, Scheme: k8sClient.Scheme()}
			computed := v1alpha1.ClusterOrderStatus{
				Phase: v1alpha1.ClusterOrderPhaseReady,
				Conditions: []metav1.Condition{{
					Type:   v1alpha1.ConditionProgressing,
					Status: metav1.ConditionFalse,
					Reason: v1alpha1.ReasonAsExpected,
				}},
			}
			Expect(r.patchStatusWithRetry(ctx, key, computed)).To(Succeed())

			got := &v1alpha1.ClusterOrder{}
			Expect(k8sClient.Get(ctx, key, got)).To(Succeed())
			Expect(got.Status.Phase).To(Equal(v1alpha1.ClusterOrderPhaseProgressing))
			progressing := apimeta.FindStatusCondition(got.Status.Conditions, v1alpha1.ConditionProgressing)
			Expect(progressing).NotTo(BeNil())
			Expect(progressing.Status).To(Equal(metav1.ConditionTrue))
			Expect(progressing.Reason).To(Equal(v1alpha1.ReasonWorkersJoining))
		})

		It("patchStatusWithRetry preserves the fresh worker failure condition", func() {
			key = types.NamespacedName{Name: "ws-worker-failure", Namespace: "default"}
			Expect(k8sClient.Create(ctx, newClusterOrder(key.Name))).To(Succeed())

			co := &v1alpha1.ClusterOrder{}
			Expect(k8sClient.Get(ctx, key, co)).To(Succeed())
			co.Status.Phase = v1alpha1.ClusterOrderPhaseProgressing
			co.SetStatusCondition(v1alpha1.ConditionWorkersFailed, metav1.ConditionTrue,
				"1 of 1 worker nodes failed to provision", "WorkersRetrying")
			Expect(k8sClient.Status().Update(ctx, co)).To(Succeed())

			r := &ClusterOrderReconciler{Client: k8sClient, apiReader: k8sClient, Scheme: k8sClient.Scheme()}
			computed := v1alpha1.ClusterOrderStatus{
				Phase: v1alpha1.ClusterOrderPhaseProgressing,
				Conditions: []metav1.Condition{{
					Type:    v1alpha1.ConditionWorkersFailed,
					Status:  metav1.ConditionFalse,
					Reason:  "WorkersHealthy",
					Message: "all workers healthy",
				}},
			}
			Expect(r.patchStatusWithRetry(ctx, key, computed)).To(Succeed())

			got := &v1alpha1.ClusterOrder{}
			Expect(k8sClient.Get(ctx, key, got)).To(Succeed())
			workersFailed := apimeta.FindStatusCondition(got.Status.Conditions, v1alpha1.ConditionWorkersFailed)
			Expect(workersFailed).NotTo(BeNil())
			Expect(workersFailed.Status).To(Equal(metav1.ConditionTrue))
			Expect(workersFailed.Reason).To(Equal("WorkersRetrying"))
			Expect(workersFailed.Message).To(Equal("1 of 1 worker nodes failed to provision"))
		})
	})
})
