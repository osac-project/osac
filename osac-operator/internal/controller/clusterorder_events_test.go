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
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

var _ = Describe("ClusterOrder transition events", func() {
	newRecorder := func() *events.FakeRecorder {
		return events.NewFakeRecorder(10)
	}

	statusWithProgressingReason := func(reason string) v1alpha1.ClusterOrderStatus {
		return v1alpha1.ClusterOrderStatus{
			Phase: v1alpha1.ClusterOrderPhaseProgressing,
			Conditions: []metav1.Condition{
				{
					Type:               v1alpha1.ConditionProgressing,
					Status:             metav1.ConditionTrue,
					Reason:             reason,
					LastTransitionTime: metav1.NewTime(time.Now().UTC()),
				},
			},
		}
	}

	It("records a Normal event when the provisioning sub-stage changes", func() {
		recorder := newRecorder()
		reconciler := &ClusterOrderReconciler{Recorder: recorder}
		instance := &v1alpha1.ClusterOrder{}
		oldStatus := statusWithProgressingReason(v1alpha1.ReasonPreparingInfrastructure)
		instance.Status = statusWithProgressingReason(v1alpha1.ReasonControlPlaneStarting)

		reconciler.recordTransitionEvents(instance, &oldStatus)

		Eventually(recorder.Events).Should(Receive(And(
			ContainSubstring(corev1.EventTypeNormal),
			ContainSubstring(v1alpha1.ReasonControlPlaneStarting),
			ContainSubstring("entered provisioning stage"),
		)))
	})

	It("records a Normal event when a ClusterOrder is first created", func() {
		recorder := newRecorder()
		reconciler := &ClusterOrderReconciler{Recorder: recorder}
		instance := &v1alpha1.ClusterOrder{}
		instance.Status = statusWithProgressingReason(v1alpha1.ReasonPreparingInfrastructure)

		reconciler.recordTransitionEvents(instance, &v1alpha1.ClusterOrderStatus{})

		Eventually(recorder.Events).Should(Receive(And(
			ContainSubstring(corev1.EventTypeNormal),
			ContainSubstring(v1alpha1.ReasonCreated),
			ContainSubstring("ClusterOrder created"),
		)))
	})

	It("does not record an event when the provisioning sub-stage is unchanged", func() {
		recorder := newRecorder()
		reconciler := &ClusterOrderReconciler{Recorder: recorder}
		instance := &v1alpha1.ClusterOrder{}
		oldStatus := statusWithProgressingReason(v1alpha1.ReasonWorkersJoining)
		instance.Status = statusWithProgressingReason(v1alpha1.ReasonWorkersJoining)

		reconciler.recordTransitionEvents(instance, &oldStatus)

		Consistently(recorder.Events, 200*time.Millisecond).ShouldNot(Receive())
	})

	It("records a Warning event when provisioning becomes stalled", func() {
		recorder := newRecorder()
		reconciler := &ClusterOrderReconciler{Recorder: recorder}
		instance := &v1alpha1.ClusterOrder{}
		oldStatus := statusWithProgressingReason(v1alpha1.ReasonWorkersJoining)
		instance.Status = statusWithProgressingReason(v1alpha1.ReasonStalled)

		reconciler.recordTransitionEvents(instance, &oldStatus)

		Eventually(recorder.Events).Should(Receive(And(
			ContainSubstring(corev1.EventTypeWarning),
			ContainSubstring(v1alpha1.ReasonStalled),
		)))
	})

	It("records a Warning event when the provisioning stage becomes unknown", func() {
		recorder := newRecorder()
		reconciler := &ClusterOrderReconciler{Recorder: recorder}
		instance := &v1alpha1.ClusterOrder{}
		oldStatus := statusWithProgressingReason(v1alpha1.ReasonWorkersJoining)
		instance.Status = statusWithProgressingReason(v1alpha1.ReasonStageUnknown)

		reconciler.recordTransitionEvents(instance, &oldStatus)

		Eventually(recorder.Events).Should(Receive(And(
			ContainSubstring(corev1.EventTypeWarning),
			ContainSubstring(v1alpha1.ReasonStageUnknown),
			ContainSubstring("entered provisioning stage"),
		)))
	})

	It("records a Normal event when the ClusterOrder enters Deleting", func() {
		recorder := newRecorder()
		reconciler := &ClusterOrderReconciler{Recorder: recorder}
		instance := &v1alpha1.ClusterOrder{}
		oldStatus := statusWithProgressingReason(v1alpha1.ReasonWorkersJoining)
		instance.Status = oldStatus
		instance.Status.Phase = v1alpha1.ClusterOrderPhaseDeleting

		reconciler.recordTransitionEvents(instance, &oldStatus)

		Eventually(recorder.Events).Should(Receive(And(
			ContainSubstring(corev1.EventTypeNormal),
			ContainSubstring(v1alpha1.ReasonDeleting),
			ContainSubstring("ClusterOrder entered deleting phase"),
		)))
	})

	It("records a Warning event when a ClusterOrder enters Failed", func() {
		recorder := newRecorder()
		reconciler := &ClusterOrderReconciler{Recorder: recorder}
		instance := &v1alpha1.ClusterOrder{}
		oldStatus := statusWithProgressingReason(v1alpha1.ReasonWorkersJoining)
		instance.Status = statusWithProgressingReason(v1alpha1.ReasonProvisioningFailed)
		instance.Status.Phase = v1alpha1.ClusterOrderPhaseFailed
		instance.Status.Conditions[0].Message = "No agents available"

		reconciler.recordTransitionEvents(instance, &oldStatus)

		Eventually(recorder.Events).Should(Receive(And(
			ContainSubstring(corev1.EventTypeWarning),
			ContainSubstring(v1alpha1.ReasonProvisioningFailed),
			ContainSubstring("ClusterOrder provisioning failed"),
		)))
	})

	It("uses the fallback details when a Failed transition has no Progressing condition", func() {
		recorder := newRecorder()
		reconciler := &ClusterOrderReconciler{Recorder: recorder}
		instance := &v1alpha1.ClusterOrder{Status: v1alpha1.ClusterOrderStatus{
			Phase: v1alpha1.ClusterOrderPhaseFailed,
		}}
		oldStatus := statusWithProgressingReason(v1alpha1.ReasonWorkersJoining)

		reconciler.recordTransitionEvents(instance, &oldStatus)

		Eventually(recorder.Events).Should(Receive(And(
			ContainSubstring(corev1.EventTypeWarning),
			ContainSubstring(v1alpha1.ReasonFailed),
			ContainSubstring("ClusterOrder provisioning failed"),
		)))
	})

	It("records a Normal event when a ClusterOrder becomes Ready", func() {
		recorder := newRecorder()
		reconciler := &ClusterOrderReconciler{Recorder: recorder}
		instance := &v1alpha1.ClusterOrder{}
		oldStatus := statusWithProgressingReason(v1alpha1.ReasonWorkersJoining)
		instance.Status = v1alpha1.ClusterOrderStatus{
			Phase: v1alpha1.ClusterOrderPhaseReady,
			Conditions: []metav1.Condition{{
				Type:               v1alpha1.ConditionProgressing,
				Status:             metav1.ConditionFalse,
				Reason:             v1alpha1.ReasonAsExpected,
				LastTransitionTime: metav1.Now(),
			}},
		}

		reconciler.recordTransitionEvents(instance, &oldStatus)

		Eventually(recorder.Events).Should(Receive(And(
			ContainSubstring(corev1.EventTypeNormal),
			ContainSubstring(v1alpha1.ReasonReady),
			ContainSubstring("ClusterOrder is ready"),
		)))
	})

	It("does not record Ready when the phase is Ready but Progressing is not False", func() {
		recorder := newRecorder()
		reconciler := &ClusterOrderReconciler{Recorder: recorder}
		instance := &v1alpha1.ClusterOrder{Status: v1alpha1.ClusterOrderStatus{
			Phase: v1alpha1.ClusterOrderPhaseReady,
			Conditions: []metav1.Condition{{
				Type:   v1alpha1.ConditionProgressing,
				Status: metav1.ConditionTrue,
			}},
		}}

		oldStatus := statusWithProgressingReason(v1alpha1.ReasonWorkersJoining)
		reconciler.recordTransitionEvents(instance, &oldStatus)

		Consistently(recorder.Events, 200*time.Millisecond).ShouldNot(Receive())
	})

	It("does not record Ready when the phase is Ready without a Progressing condition", func() {
		recorder := newRecorder()
		reconciler := &ClusterOrderReconciler{Recorder: recorder}
		instance := &v1alpha1.ClusterOrder{Status: v1alpha1.ClusterOrderStatus{
			Phase: v1alpha1.ClusterOrderPhaseReady,
		}}

		oldStatus := statusWithProgressingReason(v1alpha1.ReasonWorkersJoining)
		reconciler.recordTransitionEvents(instance, &oldStatus)

		Consistently(recorder.Events, 200*time.Millisecond).ShouldNot(Receive())
	})

	It("does not duplicate the Ready event", func() {
		recorder := newRecorder()
		reconciler := &ClusterOrderReconciler{Recorder: recorder}
		status := v1alpha1.ClusterOrderStatus{
			Phase: v1alpha1.ClusterOrderPhaseReady,
			Conditions: []metav1.Condition{{
				Type:   v1alpha1.ConditionProgressing,
				Status: metav1.ConditionFalse,
			}},
		}
		instance := &v1alpha1.ClusterOrder{Status: status}

		reconciler.recordTransitionEvents(instance, &status)

		Consistently(recorder.Events, 200*time.Millisecond).ShouldNot(Receive())
	})

	It("does not emit an event when status persistence fails", func() {
		recorder := newRecorder()
		scheme := runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		instance := &v1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{Name: "order", Namespace: "default"},
		}
		reader := fake.NewClientBuilder().WithScheme(scheme).WithObjects(instance.DeepCopy()).Build()
		patchErr := errors.New("status patch failed")
		reconciler := &ClusterOrderReconciler{
			Client: interceptor.NewClient(reader, interceptor.Funcs{
				SubResourcePatch: func(_ context.Context, _ client.Client, subResourceName string,
					_ client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) error {
					Expect(subResourceName).To(Equal("status"))
					return patchErr
				},
			}),
			apiReader: reader,
			Recorder:  recorder,
		}
		instance.Status = statusWithProgressingReason(v1alpha1.ReasonControlPlaneStarting)

		err := reconciler.persistStatusAndRecordTransitionEvents(
			context.Background(), client.ObjectKeyFromObject(instance), instance,
			&v1alpha1.ClusterOrderStatus{},
		)

		Expect(err).To(MatchError(patchErr))
		Consistently(recorder.Events, 200*time.Millisecond).ShouldNot(Receive())
	})

	It("does not panic when no event recorder is configured", func() {
		reconciler := &ClusterOrderReconciler{}
		instance := &v1alpha1.ClusterOrder{}
		instance.Status = statusWithProgressingReason(v1alpha1.ReasonControlPlaneStarting)

		Expect(func() {
			reconciler.recordTransitionEvents(instance, &v1alpha1.ClusterOrderStatus{})
		}).NotTo(Panic())
	})
})
