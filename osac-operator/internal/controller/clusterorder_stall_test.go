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
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

var _ = Describe("ClusterOrder stall detection", func() {
	const (
		preparingInfrastructureThreshold = 15 * time.Minute
		controlPlaneStartingThreshold    = 30 * time.Minute
		workersJoiningThreshold          = 20 * time.Minute
	)

	baseTime := time.Date(2026, time.September, 7, 12, 0, 0, 0, time.UTC)

	newReconciler := func(now time.Time) *ClusterOrderReconciler {
		return &ClusterOrderReconciler{
			StatusPollInterval: time.Minute,
			StallThresholds: ClusterOrderStallThresholds{
				PreparingInfrastructure: preparingInfrastructureThreshold,
				ControlPlaneStarting:    controlPlaneStartingThreshold,
				WorkersJoining:          workersJoiningThreshold,
			},
			now: func() time.Time { return now },
		}
	}

	newOrder := func(stage string, stageStartedAt time.Time) *v1alpha1.ClusterOrder {
		order := &v1alpha1.ClusterOrder{
			Status: v1alpha1.ClusterOrderStatus{
				Phase: v1alpha1.ClusterOrderPhaseProgressing,
			},
		}
		order.SetStatusCondition(v1alpha1.ConditionProgressing, metav1.ConditionTrue, humanizeConditionName(stage), stage)
		order.SetStatusCondition(v1alpha1.ConditionAccepted, metav1.ConditionTrue, "", v1alpha1.ReasonInitialized)
		if stage == v1alpha1.ReasonControlPlaneStarting || stage == v1alpha1.ReasonWorkersJoining {
			order.SetStatusCondition(v1alpha1.ConditionControlPlaneCreated, metav1.ConditionTrue, "", v1alpha1.ReasonAsExpected)
		}
		if stage == v1alpha1.ReasonWorkersJoining {
			order.SetStatusCondition(v1alpha1.ConditionControlPlaneAvailable, metav1.ConditionTrue, "", v1alpha1.ReasonAsExpected)
		}

		for index := range order.Status.Conditions {
			condition := &order.Status.Conditions[index]
			switch condition.Type {
			case v1alpha1.ConditionAccepted:
				if stage == v1alpha1.ReasonPreparingInfrastructure {
					condition.LastTransitionTime = metav1.NewTime(stageStartedAt)
				}
			case v1alpha1.ConditionControlPlaneCreated:
				if stage == v1alpha1.ReasonControlPlaneStarting {
					condition.LastTransitionTime = metav1.NewTime(stageStartedAt)
				}
			case v1alpha1.ConditionControlPlaneAvailable:
				if stage == v1alpha1.ReasonWorkersJoining {
					condition.LastTransitionTime = metav1.NewTime(stageStartedAt)
				}
			}
		}
		return order
	}

	It("requeues for the remaining current-stage duration", func() {
		order := newOrder(v1alpha1.ReasonPreparingInfrastructure, baseTime)
		reconciler := newReconciler(baseTime.Add(10 * time.Minute))

		result := reconciler.detectProvisioningStall(order)

		Expect(result.RequeueAfter).To(Equal(5 * time.Minute))
		progressing := findCondition(order, v1alpha1.ConditionProgressing)
		Expect(progressing.Reason).To(Equal(v1alpha1.ReasonPreparingInfrastructure))
	})

	It("marks the current stage Stalled at its threshold", func() {
		order := newOrder(v1alpha1.ReasonControlPlaneStarting, baseTime)
		reconciler := newReconciler(baseTime.Add(controlPlaneStartingThreshold))

		result := reconciler.detectProvisioningStall(order)

		Expect(result.RequeueAfter).To(BeNumerically(">", 0))
		progressing := findCondition(order, v1alpha1.ConditionProgressing)
		Expect(progressing.Reason).To(Equal(v1alpha1.ReasonStalled))
		Expect(progressing.Message).To(ContainSubstring("Control Plane Starting"))
	})

	It("preserves Stalled across a subsequent reconcile without stage advancement", func() {
		order := newOrder(v1alpha1.ReasonPreparingInfrastructure, baseTime)
		reconciler := newReconciler(baseTime.Add(preparingInfrastructureThreshold))

		reconciler.detectProvisioningStall(order)
		reconciler.initializeProgressingStage(order)

		progressing := apimeta.FindStatusCondition(order.Status.Conditions, v1alpha1.ConditionProgressing)
		Expect(progressing.Reason).To(Equal(v1alpha1.ReasonStalled))
		Expect(progressing.Message).To(Equal("Stalled at Preparing Infrastructure"))
	})

	It("measures from the later stage rather than cumulative provisioning time", func() {
		order := newOrder(v1alpha1.ReasonControlPlaneStarting, baseTime.Add(25*time.Minute))
		reconciler := newReconciler(baseTime.Add(30 * time.Minute))

		result := reconciler.detectProvisioningStall(order)

		Expect(result.RequeueAfter).To(Equal(25 * time.Minute))
		progressing := findCondition(order, v1alpha1.ConditionProgressing)
		Expect(progressing.Reason).To(Equal(v1alpha1.ReasonControlPlaneStarting))
	})

	It("does not stall when the current stage is unknown", func() {
		order := newOrder(v1alpha1.ReasonStageUnknown, baseTime)
		reconciler := newReconciler(baseTime.Add(24 * time.Hour))

		result := reconciler.detectProvisioningStall(order)

		Expect(result.RequeueAfter).To(BeZero())
		Expect(findCondition(order, v1alpha1.ConditionProgressing).Reason).To(Equal(v1alpha1.ReasonStageUnknown))
	})

	It("uses default thresholds when no threshold configuration is supplied", func() {
		order := newOrder(v1alpha1.ReasonPreparingInfrastructure, baseTime)
		reconciler := newReconciler(baseTime.Add(10 * time.Minute))
		reconciler.StallThresholds = ClusterOrderStallThresholds{}

		result := reconciler.detectProvisioningStall(order)

		Expect(result.RequeueAfter).To(Equal(5 * time.Minute))
	})

	It("does not start a timer until the current stage has a transition timestamp", func() {
		order := newOrder(v1alpha1.ReasonPreparingInfrastructure, baseTime)
		findCondition(order, v1alpha1.ConditionAccepted).LastTransitionTime = metav1.Time{}
		reconciler := newReconciler(baseTime.Add(24 * time.Hour))

		result := reconciler.detectProvisioningStall(order)

		Expect(result.RequeueAfter).To(BeZero())
		Expect(findCondition(order, v1alpha1.ConditionProgressing).Reason).
			To(Equal(v1alpha1.ReasonPreparingInfrastructure))
	})

	It("does not run outside an active progressing state", func() {
		order := newOrder(v1alpha1.ReasonPreparingInfrastructure, baseTime)
		order.Status.Phase = v1alpha1.ClusterOrderPhaseReady
		reconciler := newReconciler(baseTime.Add(24 * time.Hour))

		result := reconciler.detectProvisioningStall(order)

		Expect(result.RequeueAfter).To(BeZero())
		Expect(findCondition(order, v1alpha1.ConditionProgressing).Reason).
			To(Equal(v1alpha1.ReasonPreparingInfrastructure))
	})

	It("does not run when Progressing is absent or false", func() {
		reconciler := newReconciler(baseTime.Add(24 * time.Hour))
		absentOrder := newOrder(v1alpha1.ReasonPreparingInfrastructure, baseTime)
		absentOrder.Status.Conditions = absentOrder.Status.Conditions[1:]
		falseOrder := newOrder(v1alpha1.ReasonPreparingInfrastructure, baseTime)
		findCondition(falseOrder, v1alpha1.ConditionProgressing).Status = metav1.ConditionFalse

		Expect(reconciler.detectProvisioningStall(absentOrder).RequeueAfter).To(BeZero())
		Expect(reconciler.detectProvisioningStall(falseOrder).RequeueAfter).To(BeZero())
	})

	It("self-clears Stalled when the control plane advances to workers joining", func() {
		order := newOrder(v1alpha1.ReasonControlPlaneStarting, baseTime)
		reconciler := newReconciler(baseTime.Add(controlPlaneStartingThreshold))

		reconciler.detectProvisioningStall(order)
		Expect(findCondition(order, v1alpha1.ConditionProgressing).Reason).To(Equal(v1alpha1.ReasonStalled))

		order.SetStatusCondition(v1alpha1.ConditionControlPlaneAvailable, metav1.ConditionTrue, "", v1alpha1.ReasonAsExpected)
		for index := range order.Status.Conditions {
			if order.Status.Conditions[index].Type == v1alpha1.ConditionControlPlaneAvailable {
				order.Status.Conditions[index].LastTransitionTime = metav1.NewTime(baseTime.Add(controlPlaneStartingThreshold))
			}
		}
		reconciler.setProgressingStage(order, v1alpha1.ReasonWorkersJoining)

		result := reconciler.detectProvisioningStall(order)

		Expect(result.RequeueAfter).To(Equal(workersJoiningThreshold))
		Expect(findCondition(order, v1alpha1.ConditionProgressing).Reason).To(Equal(v1alpha1.ReasonWorkersJoining))
	})

	It("uses the longest applicable host-type override while workers join", func() {
		order := newOrder(v1alpha1.ReasonWorkersJoining, baseTime)
		order.Spec.NodeRequests = []v1alpha1.NodeRequest{
			{ResourceClass: "fast", NumberOfNodes: 1},
			{ResourceClass: "slow", NumberOfNodes: 1},
		}
		reconciler := newReconciler(baseTime.Add(25 * time.Minute))
		reconciler.StallThresholds.WorkersJoiningByHostType = map[string]time.Duration{
			"fast": 10 * time.Minute,
			"slow": 30 * time.Minute,
		}

		result := reconciler.detectProvisioningStall(order)

		Expect(result.RequeueAfter).To(Equal(5 * time.Minute))
		Expect(findCondition(order, v1alpha1.ConditionProgressing).Reason).To(Equal(v1alpha1.ReasonWorkersJoining))
	})

	It("honors a shorter worker-join override for a single host type", func() {
		order := newOrder(v1alpha1.ReasonWorkersJoining, baseTime)
		order.Spec.NodeRequests = []v1alpha1.NodeRequest{{ResourceClass: "fast", NumberOfNodes: 1}}
		reconciler := newReconciler(baseTime.Add(10 * time.Minute))
		reconciler.StallThresholds.WorkersJoiningByHostType = map[string]time.Duration{
			"fast": 5 * time.Minute,
		}

		reconciler.detectProvisioningStall(order)

		Expect(findCondition(order, v1alpha1.ConditionProgressing).Reason).To(Equal(v1alpha1.ReasonStalled))
	})

	It("keeps an earlier provisioning requeue over the stall timer", func() {
		order := newOrder(v1alpha1.ReasonPreparingInfrastructure, baseTime)
		reconciler := newReconciler(baseTime.Add(10 * time.Minute))

		result := reconciler.withStallRequeue(order, ctrl.Result{RequeueAfter: time.Minute})

		Expect(result.RequeueAfter).To(Equal(time.Minute))
	})

	It("uses the stall timer when it is earlier than the provisioning requeue", func() {
		order := newOrder(v1alpha1.ReasonPreparingInfrastructure, baseTime)
		reconciler := newReconciler(baseTime.Add(10 * time.Minute))

		result := reconciler.withStallRequeue(order, ctrl.Result{RequeueAfter: 10 * time.Minute})

		Expect(result.RequeueAfter).To(Equal(5 * time.Minute))
	})

	It("does not start a control-plane stall timer without its stage marker", func() {
		order := newOrder(v1alpha1.ReasonPreparingInfrastructure, baseTime)
		progressing := findCondition(order, v1alpha1.ConditionProgressing)
		progressing.Reason = v1alpha1.ReasonControlPlaneStarting
		progressing.Message = humanizeConditionName(v1alpha1.ReasonControlPlaneStarting)
		reconciler := newReconciler(baseTime.Add(24 * time.Hour))

		result := reconciler.detectProvisioningStall(order)

		Expect(result.RequeueAfter).To(BeZero())
		Expect(findCondition(order, v1alpha1.ConditionProgressing).Reason).
			To(Equal(v1alpha1.ReasonControlPlaneStarting))
	})

	It("uses the base worker-join threshold when there are no node requests", func() {
		thresholds := ClusterOrderStallThresholds{WorkersJoining: workersJoiningThreshold}

		Expect(thresholds.workersJoiningThreshold(nil)).To(Equal(workersJoiningThreshold))
	})

	It("uses the base worker-join threshold without an override for the host type", func() {
		thresholds := ClusterOrderStallThresholds{WorkersJoining: workersJoiningThreshold}

		Expect(thresholds.workersJoiningThreshold([]v1alpha1.NodeRequest{{ResourceClass: "standard"}})).
			To(Equal(workersJoiningThreshold))
	})

	It("uses the default worker-join threshold when no base value is configured", func() {
		thresholds := ClusterOrderStallThresholds{}

		Expect(thresholds.workersJoiningThreshold([]v1alpha1.NodeRequest{{ResourceClass: "standard"}})).
			To(Equal(defaultWorkersJoiningStallThreshold))
	})
})

func findCondition(order *v1alpha1.ClusterOrder, conditionType string) *metav1.Condition {
	for index := range order.Status.Conditions {
		condition := &order.Status.Conditions[index]
		if condition.Type == conditionType {
			return condition
		}
	}
	return nil
}
