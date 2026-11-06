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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

var _ = Describe("ClusterOrder delete-failed phase", func() {
	DescribeTable("syncDeleteFailedPhase reports the deprovision outcome on the phase",
		func(
			jobs []v1alpha1.JobStatus,
			startingPhase v1alpha1.ClusterOrderPhaseType,
			expectedPhase v1alpha1.ClusterOrderPhaseType,
		) {
			instance := &v1alpha1.ClusterOrder{}
			instance.Status.Phase = startingPhase
			instance.Status.ProvisioningJobs = jobs

			syncDeleteFailedPhase(instance)

			Expect(instance.Status.Phase).To(Equal(expectedPhase))
		},
		Entry("a blocking failed deprovision job fails the phase",
			[]v1alpha1.JobStatus{{
				JobID: "job-1", Type: v1alpha1.JobTypeDeprovision,
				State: v1alpha1.JobStateFailed, Timestamp: metav1.NewTime(time.Now()),
				BlockDeletionOnFailure: true,
			}},
			v1alpha1.ClusterOrderPhaseDeleting,
			v1alpha1.ClusterOrderPhaseDeleteFailed,
		),
		Entry("a failed deprovision job that does not block deletion keeps Deleting",
			[]v1alpha1.JobStatus{{
				JobID: "job-1", Type: v1alpha1.JobTypeDeprovision,
				State: v1alpha1.JobStateFailed, Timestamp: metav1.NewTime(time.Now()),
			}},
			v1alpha1.ClusterOrderPhaseDeleting,
			v1alpha1.ClusterOrderPhaseDeleting,
		),
		Entry("a triggered retry returns a failed phase to Deleting",
			[]v1alpha1.JobStatus{{
				JobID: "job-1", Type: v1alpha1.JobTypeDeprovision,
				State: v1alpha1.JobStateFailed, Timestamp: metav1.NewTime(time.Now().Add(-time.Hour)),
				BlockDeletionOnFailure: true,
			}, {
				JobID: "job-2", Type: v1alpha1.JobTypeDeprovision,
				State: v1alpha1.JobStatePending, Timestamp: metav1.NewTime(time.Now()),
			}},
			v1alpha1.ClusterOrderPhaseDeleteFailed,
			v1alpha1.ClusterOrderPhaseDeleting,
		),
		Entry("a running deprovision job keeps Deleting",
			[]v1alpha1.JobStatus{{
				JobID: "job-1", Type: v1alpha1.JobTypeDeprovision,
				State: v1alpha1.JobStateRunning, Timestamp: metav1.NewTime(time.Now()),
				BlockDeletionOnFailure: true,
			}},
			v1alpha1.ClusterOrderPhaseDeleting,
			v1alpha1.ClusterOrderPhaseDeleting,
		),
		Entry("no deprovision job keeps Deleting",
			nil,
			v1alpha1.ClusterOrderPhaseDeleting,
			v1alpha1.ClusterOrderPhaseDeleting,
		),
		Entry("a provision job never fails the phase",
			[]v1alpha1.JobStatus{{
				JobID: "job-1", Type: v1alpha1.JobTypeProvision,
				State: v1alpha1.JobStateFailed, Timestamp: metav1.NewTime(time.Now()),
				BlockDeletionOnFailure: true,
			}},
			v1alpha1.ClusterOrderPhaseDeleting,
			v1alpha1.ClusterOrderPhaseDeleting,
		),
	)

	Context("patchStatusWithRetry delete phase", func() {
		const namespace = "default"

		// newDeletingOrder creates a ClusterOrder under deletion: a finalizer
		// keeps it present with a deletionTimestamp.
		newDeletingOrder := func(name string, phase v1alpha1.ClusterOrderPhaseType) types.NamespacedName {
			instance := newClusterOrder(name, namespace, nil)
			instance.Finalizers = []string{"osac.openshift.io/test"}
			Expect(k8sClient.Create(context.Background(), instance)).To(Succeed())
			Expect(k8sClient.Delete(context.Background(), instance)).To(Succeed())

			key := types.NamespacedName{Name: name, Namespace: namespace}
			got := &v1alpha1.ClusterOrder{}
			Expect(k8sClient.Get(context.Background(), key, got)).To(Succeed())
			Expect(got.DeletionTimestamp).NotTo(BeNil())
			got.Status.Phase = phase
			Expect(k8sClient.Status().Update(context.Background(), got)).To(Succeed())
			return key
		}

		It("keeps the computed DeleteFailed phase during deletion", func() {
			reconciler := &ClusterOrderReconciler{Client: k8sClient, apiReader: k8sClient, Scheme: k8sClient.Scheme()}
			key := newDeletingOrder("df-keep-phase", v1alpha1.ClusterOrderPhaseDeleting)

			computed := v1alpha1.ClusterOrderStatus{Phase: v1alpha1.ClusterOrderPhaseDeleteFailed}
			_, err := reconciler.patchStatusWithRetry(context.Background(), key, computed)
			Expect(err).NotTo(HaveOccurred())

			got := &v1alpha1.ClusterOrder{}
			Expect(k8sClient.Get(context.Background(), key, got)).To(Succeed())
			Expect(got.Status.Phase).To(Equal(v1alpha1.ClusterOrderPhaseDeleteFailed))
			deleting := apimeta.FindStatusCondition(got.Status.Conditions, v1alpha1.ConditionDeleting)
			Expect(deleting).NotTo(BeNil())
			Expect(deleting.Status).To(Equal(metav1.ConditionTrue))
			Expect(deleting.Reason).To(Equal(v1alpha1.ReasonDeprovisionFailed))
		})

		It("returns a persisted DeleteFailed phase to Deleting when the retry proceeds", func() {
			reconciler := &ClusterOrderReconciler{Client: k8sClient, apiReader: k8sClient, Scheme: k8sClient.Scheme()}
			key := newDeletingOrder("df-retry-phase", v1alpha1.ClusterOrderPhaseDeleteFailed)

			computed := v1alpha1.ClusterOrderStatus{Phase: v1alpha1.ClusterOrderPhaseDeleting}
			_, err := reconciler.patchStatusWithRetry(context.Background(), key, computed)
			Expect(err).NotTo(HaveOccurred())

			got := &v1alpha1.ClusterOrder{}
			Expect(k8sClient.Get(context.Background(), key, got)).To(Succeed())
			Expect(got.Status.Phase).To(Equal(v1alpha1.ClusterOrderPhaseDeleting))
			deleting := apimeta.FindStatusCondition(got.Status.Conditions, v1alpha1.ConditionDeleting)
			Expect(deleting).NotTo(BeNil())
			Expect(deleting.Reason).To(Equal(v1alpha1.ReasonDeleting))
		})

		It("forces Deleting when a stale computed phase arrives during deletion", func() {
			reconciler := &ClusterOrderReconciler{Client: k8sClient, apiReader: k8sClient, Scheme: k8sClient.Scheme()}
			key := newDeletingOrder("df-stale-phase", v1alpha1.ClusterOrderPhaseDeleting)

			computed := v1alpha1.ClusterOrderStatus{Phase: v1alpha1.ClusterOrderPhaseReady}
			_, err := reconciler.patchStatusWithRetry(context.Background(), key, computed)
			Expect(err).NotTo(HaveOccurred())

			got := &v1alpha1.ClusterOrder{}
			Expect(k8sClient.Get(context.Background(), key, got)).To(Succeed())
			Expect(got.Status.Phase).To(Equal(v1alpha1.ClusterOrderPhaseDeleting))
		})
	})
})
