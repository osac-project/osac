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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
