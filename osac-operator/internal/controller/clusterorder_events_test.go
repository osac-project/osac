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
	apimeta "k8s.io/apimachinery/pkg/api/meta"
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

	withStages := func(status v1alpha1.ClusterOrderStatus, stages ...string) v1alpha1.ClusterOrderStatus {
		out := *status.DeepCopy()
		for _, stage := range stages {
			apimeta.SetStatusCondition(&out.Conditions, metav1.Condition{
				Type: stage, Status: metav1.ConditionTrue, Reason: v1alpha1.ReasonAsExpected,
			})
		}
		return out
	}

	drain := func(recorder *events.FakeRecorder) []string {
		var got []string
		for {
			select {
			case e := <-recorder.Events:
				got = append(got, e)
			default:
				return got
			}
		}
	}

	It("records a Normal event when a provisioning stage condition first becomes True", func() {
		recorder := newRecorder()
		reconciler := &ClusterOrderReconciler{Recorder: recorder}
		instance := &v1alpha1.ClusterOrder{}
		oldStatus := withStages(statusWithProgressingReason(v1alpha1.ConditionAccepted),
			v1alpha1.ConditionAccepted)
		instance.Status = withStages(statusWithProgressingReason(v1alpha1.ConditionControlPlaneCreated),
			v1alpha1.ConditionAccepted, v1alpha1.ConditionControlPlaneCreated)

		reconciler.recordTransitionEventsForStatus(instance, &oldStatus, &instance.Status)

		Eventually(recorder.Events).Should(Receive(And(
			ContainSubstring(corev1.EventTypeNormal),
			ContainSubstring(v1alpha1.ConditionControlPlaneCreated),
			ContainSubstring("ClusterOrder reached Control Plane Created"),
		)))
	})

	It("records exactly one event per provisioning milestone", func() {
		recorder := newRecorder()
		scheme := runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		instance := &v1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{Name: "order", Namespace: "default"},
			Status: withStages(statusWithProgressingReason(v1alpha1.ConditionAccepted),
				v1alpha1.ConditionAccepted),
		}
		reader := fake.NewClientBuilder().WithScheme(scheme).
			WithStatusSubresource(&v1alpha1.ClusterOrder{}).
			WithObjects(instance.DeepCopy()).Build()
		reconciler := &ClusterOrderReconciler{Client: reader, apiReader: reader, Recorder: recorder}

		oldStatus := *instance.Status.DeepCopy()
		// Mirror the handleHostedCluster milestone path: set the condition, then let the
		// Progressing reason follow the furthest condition reached.
		instance.SetStatusCondition(v1alpha1.ConditionControlPlaneCreated, metav1.ConditionTrue,
			"", v1alpha1.ReasonAsExpected)
		reconciler.advanceProgressingStage(instance)

		Expect(reconciler.persistStatusAndRecordTransitionEvents(
			context.Background(), client.ObjectKeyFromObject(instance), instance, &oldStatus,
		)).To(Succeed())

		Expect(drain(recorder)).To(ConsistOf(
			ContainSubstring("ClusterOrder reached Control Plane Created"),
		))
	})

	It("does not re-emit a milestone event once the condition is persisted", func() {
		recorder := newRecorder()
		scheme := runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		instance := &v1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{Name: "order", Namespace: "default"},
			Status: withStages(statusWithProgressingReason(v1alpha1.ConditionAccepted),
				v1alpha1.ConditionAccepted),
		}
		reader := fake.NewClientBuilder().WithScheme(scheme).
			WithStatusSubresource(&v1alpha1.ClusterOrder{}).
			WithObjects(instance.DeepCopy()).Build()
		reconciler := &ClusterOrderReconciler{Client: reader, apiReader: reader, Recorder: recorder}

		oldStatus := *instance.Status.DeepCopy()
		instance.SetStatusCondition(v1alpha1.ConditionControlPlaneCreated, metav1.ConditionTrue,
			"", v1alpha1.ReasonAsExpected)
		reconciler.advanceProgressingStage(instance)

		Expect(reconciler.persistStatusAndRecordTransitionEvents(
			context.Background(), client.ObjectKeyFromObject(instance), instance, &oldStatus,
		)).To(Succeed())
		Expect(recorder.Events).To(Receive(ContainSubstring("ClusterOrder reached Control Plane Created")))

		// A second reconcile recomputes the same milestone from a stale local copy of the
		// pre-milestone status. The persisted condition must suppress the duplicate.
		Expect(reconciler.persistStatusAndRecordTransitionEvents(
			context.Background(), client.ObjectKeyFromObject(instance), instance, &oldStatus,
		)).To(Succeed())
		Consistently(recorder.Events, 200*time.Millisecond).ShouldNot(Receive())
	})

	It("does not emit a milestone event when the status patch fails", func() {
		recorder := newRecorder()
		scheme := runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		instance := &v1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{Name: "order", Namespace: "default"},
			Status: withStages(statusWithProgressingReason(v1alpha1.ConditionAccepted),
				v1alpha1.ConditionAccepted),
		}
		reader := fake.NewClientBuilder().WithScheme(scheme).
			WithStatusSubresource(&v1alpha1.ClusterOrder{}).
			WithObjects(instance.DeepCopy()).Build()
		patchErr := errors.New("status patch failed")
		reconciler := &ClusterOrderReconciler{
			Client: interceptor.NewClient(reader, interceptor.Funcs{
				SubResourcePatch: func(_ context.Context, _ client.Client, _ string,
					_ client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) error {
					return patchErr
				},
			}),
			apiReader: reader,
			Recorder:  recorder,
		}

		oldStatus := *instance.Status.DeepCopy()
		instance.SetStatusCondition(v1alpha1.ConditionControlPlaneCreated, metav1.ConditionTrue,
			"", v1alpha1.ReasonAsExpected)
		reconciler.advanceProgressingStage(instance)

		Expect(reconciler.persistStatusAndRecordTransitionEvents(
			context.Background(), client.ObjectKeyFromObject(instance), instance, &oldStatus,
		)).To(MatchError(patchErr))
		Consistently(recorder.Events, 200*time.Millisecond).ShouldNot(Receive())
	})

	It("records a milestone event for each stage reached in a single patch", func() {
		recorder := newRecorder()
		reconciler := &ClusterOrderReconciler{Recorder: recorder}
		instance := &v1alpha1.ClusterOrder{}
		oldStatus := withStages(statusWithProgressingReason(v1alpha1.ConditionAccepted),
			v1alpha1.ConditionAccepted)
		instance.Status = withStages(statusWithProgressingReason(v1alpha1.ConditionClusterAvailable),
			v1alpha1.ConditionAccepted, v1alpha1.ConditionControlPlaneCreated,
			v1alpha1.ConditionControlPlaneAvailable, v1alpha1.ConditionClusterAvailable)

		reconciler.recordTransitionEventsForStatus(instance, &oldStatus, &instance.Status)

		Expect(drain(recorder)).To(ConsistOf(
			ContainSubstring("ClusterOrder reached Control Plane Created"),
			ContainSubstring("ClusterOrder reached Control Plane Available"),
			ContainSubstring("ClusterOrder reached Cluster Available"),
		))
	})

	It("records a Normal event when a ClusterOrder is first created", func() {
		recorder := newRecorder()
		reconciler := &ClusterOrderReconciler{Recorder: recorder}
		instance := &v1alpha1.ClusterOrder{}
		instance.Status = statusWithProgressingReason(v1alpha1.ConditionAccepted)

		reconciler.recordTransitionEventsForStatus(instance, &v1alpha1.ClusterOrderStatus{}, &instance.Status)

		Eventually(recorder.Events).Should(Receive(And(
			ContainSubstring(corev1.EventTypeNormal),
			ContainSubstring(v1alpha1.ReasonCreated),
			ContainSubstring("ClusterOrder created"),
		)))
	})

	It("does not record an event when no provisioning stage condition changed", func() {
		recorder := newRecorder()
		reconciler := &ClusterOrderReconciler{Recorder: recorder}
		instance := &v1alpha1.ClusterOrder{}
		stages := []string{v1alpha1.ConditionAccepted, v1alpha1.ConditionControlPlaneCreated,
			v1alpha1.ConditionControlPlaneAvailable}
		oldStatus := withStages(statusWithProgressingReason(v1alpha1.ConditionControlPlaneAvailable), stages...)
		instance.Status = withStages(statusWithProgressingReason(v1alpha1.ConditionControlPlaneAvailable), stages...)

		reconciler.recordTransitionEventsForStatus(instance, &oldStatus, &instance.Status)

		Consistently(recorder.Events, 200*time.Millisecond).ShouldNot(Receive())
	})

	It("records a Warning event when provisioning becomes stalled", func() {
		recorder := newRecorder()
		reconciler := &ClusterOrderReconciler{Recorder: recorder}
		instance := &v1alpha1.ClusterOrder{}
		oldStatus := statusWithProgressingReason(v1alpha1.ConditionControlPlaneAvailable)
		instance.Status = statusWithProgressingReason(v1alpha1.ReasonStalled)

		reconciler.recordTransitionEventsForStatus(instance, &oldStatus, &instance.Status)

		Eventually(recorder.Events).Should(Receive(And(
			ContainSubstring(corev1.EventTypeWarning),
			ContainSubstring(v1alpha1.ReasonStalled),
		)))
	})

	It("records a Normal event when the ClusterOrder enters Deleting", func() {
		recorder := newRecorder()
		reconciler := &ClusterOrderReconciler{Recorder: recorder}
		instance := &v1alpha1.ClusterOrder{}
		oldStatus := statusWithProgressingReason(v1alpha1.ConditionControlPlaneAvailable)
		instance.Status = oldStatus
		instance.Status.Phase = v1alpha1.ClusterOrderPhaseDeleting
		instance.SetStatusCondition(v1alpha1.ConditionDeleting, metav1.ConditionTrue,
			"ClusterOrder is being deleted", v1alpha1.ReasonDeleting)

		reconciler.recordTransitionEventsForStatus(instance, &oldStatus, &instance.Status)

		Eventually(recorder.Events).Should(Receive(And(
			ContainSubstring(corev1.EventTypeNormal),
			ContainSubstring(v1alpha1.ReasonDeleting),
			ContainSubstring("ClusterOrder entered deleting phase"),
		)))
	})

	It("does not duplicate the Deleting event on repeated reconciliation", func() {
		recorder := newRecorder()
		reconciler := &ClusterOrderReconciler{Recorder: recorder}
		oldStatus := statusWithProgressingReason(v1alpha1.ConditionControlPlaneAvailable)
		instance := &v1alpha1.ClusterOrder{Status: oldStatus}
		instance.Status.Phase = v1alpha1.ClusterOrderPhaseDeleting
		instance.SetStatusCondition(v1alpha1.ConditionDeleting, metav1.ConditionTrue,
			"ClusterOrder is being deleted", v1alpha1.ReasonDeleting)
		newStatus := instance.Status

		reconciler.recordTransitionEventsForStatus(instance, &oldStatus, &instance.Status)
		Expect(recorder.Events).To(Receive(ContainSubstring("ClusterOrder entered deleting phase")))

		reconciler.recordTransitionEventsForStatus(instance, &newStatus, &instance.Status)
		Consistently(recorder.Events, 200*time.Millisecond).ShouldNot(Receive())
	})

	It("emits Deleting only for the status patch that persists the transition", func() {
		recorder := newRecorder()
		scheme := runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		instance := &v1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{Name: "order", Namespace: "default"},
			Status:     statusWithProgressingReason(v1alpha1.ConditionControlPlaneAvailable),
		}
		reader := fake.NewClientBuilder().WithScheme(scheme).
			WithStatusSubresource(&v1alpha1.ClusterOrder{}).
			WithObjects(instance.DeepCopy()).Build()
		reconciler := &ClusterOrderReconciler{Client: reader, apiReader: reader, Recorder: recorder}
		instance.Status.Phase = v1alpha1.ClusterOrderPhaseDeleting
		instance.SetStatusCondition(v1alpha1.ConditionDeleting, metav1.ConditionTrue,
			"ClusterOrder is being deleted", v1alpha1.ReasonDeleting)
		oldStatus := statusWithProgressingReason(v1alpha1.ConditionControlPlaneAvailable)

		Expect(reconciler.persistStatusAndRecordTransitionEvents(
			context.Background(), client.ObjectKeyFromObject(instance), instance, &oldStatus,
		)).To(Succeed())
		Expect(recorder.Events).To(Receive(ContainSubstring("ClusterOrder entered deleting phase")))

		// A second reconcile still has the stale pre-delete status locally, but the
		// persisted status already contains Deleting. It must not emit a duplicate.
		Expect(reconciler.persistStatusAndRecordTransitionEvents(
			context.Background(), client.ObjectKeyFromObject(instance), instance, &oldStatus,
		)).To(Succeed())
		Consistently(recorder.Events, 200*time.Millisecond).ShouldNot(Receive())
	})

	It("records a Warning event when a ClusterOrder enters Failed", func() {
		recorder := newRecorder()
		reconciler := &ClusterOrderReconciler{Recorder: recorder}
		instance := &v1alpha1.ClusterOrder{}
		oldStatus := statusWithProgressingReason(v1alpha1.ConditionControlPlaneAvailable)
		instance.Status = statusWithProgressingReason(v1alpha1.ReasonProvisioningFailed)
		instance.Status.Phase = v1alpha1.ClusterOrderPhaseFailed
		instance.Status.Conditions[0].Message = "No agents available"

		reconciler.recordTransitionEventsForStatus(instance, &oldStatus, &instance.Status)

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
		oldStatus := statusWithProgressingReason(v1alpha1.ConditionControlPlaneAvailable)

		reconciler.recordTransitionEventsForStatus(instance, &oldStatus, &instance.Status)

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
		oldStatus := statusWithProgressingReason(v1alpha1.ConditionControlPlaneAvailable)
		instance.Status = v1alpha1.ClusterOrderStatus{
			Phase: v1alpha1.ClusterOrderPhaseReady,
			Conditions: []metav1.Condition{{
				Type:               v1alpha1.ConditionProgressing,
				Status:             metav1.ConditionFalse,
				Reason:             v1alpha1.ReasonAsExpected,
				LastTransitionTime: metav1.Now(),
			}},
		}

		reconciler.recordTransitionEventsForStatus(instance, &oldStatus, &instance.Status)

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

		oldStatus := statusWithProgressingReason(v1alpha1.ConditionControlPlaneAvailable)
		reconciler.recordTransitionEventsForStatus(instance, &oldStatus, &instance.Status)

		Consistently(recorder.Events, 200*time.Millisecond).ShouldNot(Receive())
	})

	It("does not record Ready when the phase is Ready without a Progressing condition", func() {
		recorder := newRecorder()
		reconciler := &ClusterOrderReconciler{Recorder: recorder}
		instance := &v1alpha1.ClusterOrder{Status: v1alpha1.ClusterOrderStatus{
			Phase: v1alpha1.ClusterOrderPhaseReady,
		}}

		oldStatus := statusWithProgressingReason(v1alpha1.ConditionControlPlaneAvailable)
		reconciler.recordTransitionEventsForStatus(instance, &oldStatus, &instance.Status)

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

		reconciler.recordTransitionEventsForStatus(instance, &status, &instance.Status)

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
		instance.Status = statusWithProgressingReason(v1alpha1.ConditionControlPlaneCreated)

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
		instance.Status = statusWithProgressingReason(v1alpha1.ConditionControlPlaneCreated)

		Expect(func() {
			reconciler.recordTransitionEventsForStatus(instance, &v1alpha1.ClusterOrderStatus{}, &instance.Status)
		}).NotTo(Panic())
	})
})
