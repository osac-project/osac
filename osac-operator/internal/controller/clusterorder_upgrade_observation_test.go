/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package controller

import (
	"math"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"google.golang.org/protobuf/types/known/timestamppb"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("upgrade attempt correlation", func() {
	const image = "quay.io/ocp/release:4.18.0"
	newOrder := func() *v1alpha1.ClusterOrder {
		return &v1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
				v1alpha1.UpgradeAttemptIDAnnotation: "cluster-id",
			}},
			Spec: v1alpha1.ClusterOrderSpec{ReleaseImage: image,
				NodeRequests: []v1alpha1.NodeRequest{
					{NodeSet: "compute", ReleaseImage: image},
					{NodeSet: "gpu", ReleaseImage: image},
				}},
			Status: v1alpha1.ClusterOrderStatus{NodeRequests: []v1alpha1.NodeRequestStatus{
				{NodeSet: "compute"}, {NodeSet: "gpu"},
			}},
		}
	}
	newPool := func(name, id string) hypershiftv1beta1.NodePool {
		pool := hypershiftv1beta1.NodePool{ObjectMeta: metav1.ObjectMeta{
			Generation:  2,
			Labels:      map[string]string{agentNodeSetLabel: name},
			Annotations: map[string]string{v1alpha1.UpgradeAttemptIDAnnotation: id},
		}}
		pool.Spec.Release.Image = image
		pool.Status.Version = "4.17.0"
		return pool
	}
	newControlPlane := func() *hypershiftv1beta1.HostedCluster {
		hc := &hypershiftv1beta1.HostedCluster{ObjectMeta: metav1.ObjectMeta{
			Generation: 3, Annotations: map[string]string{v1alpha1.UpgradeAttemptIDAnnotation: "cluster-id"},
		}}
		hc.Spec.Release.Image = image
		return hc
	}
	versionStatus := func(version string, state configv1.UpdateState) *hypershiftv1beta1.ClusterVersionStatus {
		return &hypershiftv1beta1.ClusterVersionStatus{
			ObservedGeneration: 1,
			Desired:            configv1.Release{Version: version, Image: "quay.io/ocp/release@sha256:digest"},
			History: []configv1.UpdateHistory{{
				Version: version, Image: "quay.io/ocp/release@sha256:digest", State: state,
			}},
		}
	}
	controlPlaneVersionStatus := func(version string, state configv1.UpdateState) hypershiftv1beta1.ControlPlaneVersionStatus {
		return hypershiftv1beta1.ControlPlaneVersionStatus{
			ObservedGeneration: 1,
			Desired:            configv1.Release{Version: version, Image: "quay.io/ocp/release@sha256:digest"},
			History: []hypershiftv1beta1.ControlPlaneUpdateHistory{{
				Version: version, Image: "quay.io/ocp/release@sha256:digest", State: state,
			}},
		}
	}
	healthyConditions := func() []metav1.Condition {
		return []metav1.Condition{
			{Type: string(hypershiftv1beta1.HostedClusterAvailable), Status: metav1.ConditionTrue, ObservedGeneration: 3},
			{Type: string(hypershiftv1beta1.HostedClusterDegraded), Status: metav1.ConditionFalse, ObservedGeneration: 3},
			{Type: string(hypershiftv1beta1.ClusterVersionSucceeding), Status: metav1.ConditionTrue, ObservedGeneration: 3},
			{Type: string(hypershiftv1beta1.ClusterVersionProgressing), Status: metav1.ConditionFalse, ObservedGeneration: 3},
		}
	}
	newActiveAttempt := func(state privatev1.ClusterUpgradeState) *privatev1.Cluster {
		accepted := time.Date(2026, 11, 18, 11, 0, 0, 0, time.UTC)
		controlPlane := privatev1.ClusterUpgradeControlPlaneState_builder{
			State: state, AcceptedTime: timestamppb.New(accepted), StateTransitionTime: timestamppb.New(accepted),
		}.Build()
		nodeSets := []*privatev1.ClusterUpgradeNodeSetState{
			privatev1.ClusterUpgradeNodeSetState_builder{
				NodeSetId: "compute", TimeoutNodeCount: 2, State: privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING,
			}.Build(),
			privatev1.ClusterUpgradeNodeSetState_builder{
				NodeSetId: "gpu", TimeoutNodeCount: 1, State: privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING,
			}.Build(),
		}
		active := privatev1.ClusterUpgradeAttemptState_builder{
			ControlPlane: controlPlane, NodeSets: nodeSets,
		}.Build()
		if state == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED {
			active.SetNodeSetsScheduledTime(timestamppb.New(accepted))
		}
		entry := privatev1.ClusterUpgradeHistoryEntry_builder{
			AttemptId: "cluster-id", SourceVersion: "4.17.0", TargetVersion: "4.18.0",
			AcceptedTime: timestamppb.New(accepted), StateTransitionTime: timestamppb.New(accepted),
			State: privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING,
		}.Build()
		return privatev1.Cluster_builder{
			Spec: privatev1.ClusterSpec_builder{NodeSets: map[string]*privatev1.ClusterNodeSet{
				"compute": {}, "gpu": {},
			}}.Build(),
			Status: privatev1.ClusterStatus_builder{
				UpgradeAttemptId: "cluster-id", ObservedVersion: "4.17.0",
				NodeSets:             map[string]*privatev1.ClusterNodeSetStatus{"compute": {}, "gpu": {}},
				ActiveUpgradeAttempt: active, UpgradeHistory: []*privatev1.ClusterUpgradeHistoryEntry{entry},
			}.Build(),
		}.Build()
	}

	It("observes the control plane and all stamped node sets independently", func() {
		order := newOrder()
		hc := newControlPlane()
		hc.Status.Version = versionStatus("4.18.0", configv1.CompletedUpdate)
		hc.Status.ControlPlaneVersion = controlPlaneVersionStatus("4.18.0", configv1.CompletedUpdate)
		hc.Status.Conditions = healthyConditions()
		recordUpgradeObservations(order, hc, []hypershiftv1beta1.NodePool{
			newPool("compute", "cluster-id"), newPool("gpu", "cluster-id"),
		})
		Expect(order.Status.UpgradeObservation.AttemptID).To(Equal("cluster-id"))
		Expect(order.Status.UpgradeObservation.State).To(Equal(v1alpha1.UpgradeObservationSucceeded))
		Expect(order.Status.UpgradeObservation.Version).To(Equal("4.18.0"))
		Expect(order.Status.NodeRequests[0].UpgradeObservation.AttemptID).To(Equal("cluster-id"))
		Expect(order.Status.NodeRequests[1].UpgradeObservation.AttemptID).To(Equal("cluster-id"))
	})

	It("stops recording upgrade observations for a removed NodeRequest", func() {
		order := newOrder()
		order.Spec.NodeRequests = order.Spec.NodeRequests[:1]
		order.Status.NodeRequests[1].UpgradeObservation = &v1alpha1.UpgradeObservation{AttemptID: "cluster-id"}

		recordUpgradeObservations(order, newControlPlane(), []hypershiftv1beta1.NodePool{
			newPool("gpu", "cluster-id"),
		})

		Expect(order.Status.NodeRequests[1].UpgradeObservation).To(BeNil())
	})

	DescribeTable("reports control-plane progress from either partial history", func(controlPlanePartial bool) {
		order := newOrder()
		hc := newControlPlane()
		hc.Status.Version = versionStatus("4.17.0", configv1.CompletedUpdate)
		hc.Status.ControlPlaneVersion = controlPlaneVersionStatus("4.17.0", configv1.CompletedUpdate)
		if controlPlanePartial {
			hc.Status.ControlPlaneVersion = controlPlaneVersionStatus("4.18.0", configv1.PartialUpdate)
		} else {
			hc.Status.Version = versionStatus("4.18.0", configv1.PartialUpdate)
		}
		recordUpgradeObservations(order, hc, nil)
		Expect(order.Status.UpgradeObservation.AttemptID).To(Equal("cluster-id"))
		Expect(order.Status.UpgradeObservation.Version).To(Equal("4.18.0"))
		Expect(order.Status.UpgradeObservation.State).To(Equal(v1alpha1.UpgradeObservationProgressing))
	},
		Entry("controlPlaneVersion is partial", true),
		Entry("version is partial", false),
	)

	It("holds progress when the two partial histories disagree on version", func() {
		order := newOrder()
		hc := newControlPlane()
		hc.Status.Version = versionStatus("4.18.0", configv1.PartialUpdate)
		hc.Status.ControlPlaneVersion = controlPlaneVersionStatus("4.19.0", configv1.PartialUpdate)
		recordUpgradeObservations(order, hc, nil)
		Expect(order.Status.UpgradeObservation.State).To(BeEmpty())
		Expect(order.Status.UpgradeObservation.Version).To(BeEmpty())
	})

	It("requires both target histories and all current health conditions for control-plane success", func() {
		order := newOrder()
		hc := newControlPlane()
		hc.Status.Version = versionStatus("4.18.0", configv1.CompletedUpdate)
		hc.Status.ControlPlaneVersion = controlPlaneVersionStatus("4.18.0", configv1.CompletedUpdate)
		recordUpgradeObservations(order, hc, nil)
		Expect(order.Status.UpgradeObservation.State).NotTo(Equal(v1alpha1.UpgradeObservationSucceeded))
		hc.Status.Conditions = healthyConditions()
		recordUpgradeObservations(order, hc, nil)
		Expect(order.Status.UpgradeObservation.State).To(Equal(v1alpha1.UpgradeObservationSucceeded))
		Expect(order.Status.UpgradeObservation.Version).To(Equal("4.18.0"))
	})

	DescribeTable("does not report control-plane success with a missing or mismatched signal",
		func(change func(*hypershiftv1beta1.HostedCluster)) {
			order := newOrder()
			hc := newControlPlane()
			hc.Status.Version = versionStatus("4.18.0", configv1.CompletedUpdate)
			hc.Status.Conditions = healthyConditions()
			hc.Status.ControlPlaneVersion = controlPlaneVersionStatus("4.18.0", configv1.CompletedUpdate)
			change(hc)
			recordUpgradeObservations(order, hc, nil)
			Expect(order.Status.UpgradeObservation.State).NotTo(Equal(v1alpha1.UpgradeObservationSucceeded))
		},
		Entry("control-plane desired differs", func(hc *hypershiftv1beta1.HostedCluster) {
			hc.Status.ControlPlaneVersion.Desired.Version = "4.19.0"
		}),
		Entry("control-plane history differs", func(hc *hypershiftv1beta1.HostedCluster) {
			hc.Status.ControlPlaneVersion.History[0].Version = "4.19.0"
		}),
		Entry("control-plane history is partial", func(hc *hypershiftv1beta1.HostedCluster) {
			hc.Status.ControlPlaneVersion.History[0].State = configv1.PartialUpdate
		}),
		Entry("guest desired differs", func(hc *hypershiftv1beta1.HostedCluster) {
			hc.Status.Version.Desired.Version = "4.19.0"
		}),
		Entry("guest history differs", func(hc *hypershiftv1beta1.HostedCluster) {
			hc.Status.Version.History[0].Version = "4.19.0"
		}),
		Entry("guest history is partial", func(hc *hypershiftv1beta1.HostedCluster) {
			hc.Status.Version.History[0].State = configv1.PartialUpdate
		}),
		Entry("available is absent", func(hc *hypershiftv1beta1.HostedCluster) {
			hc.Status.Conditions = hc.Status.Conditions[1:]
		}),
		Entry("degraded is true", func(hc *hypershiftv1beta1.HostedCluster) {
			hc.Status.Conditions[1].Status = metav1.ConditionTrue
		}),
		Entry("succeeding is false", func(hc *hypershiftv1beta1.HostedCluster) {
			hc.Status.Conditions[2].Status = metav1.ConditionFalse
		}),
		Entry("progressing is true", func(hc *hypershiftv1beta1.HostedCluster) {
			hc.Status.Conditions[3].Status = metav1.ConditionTrue
		}),
		Entry("health is stale", func(hc *hypershiftv1beta1.HostedCluster) {
			hc.Status.Conditions[0].ObservedGeneration = 2
		}),
	)

	It("does not report a release-image condition as terminal failure", func() {
		order := newOrder()
		hc := newControlPlane()
		hc.Status.Conditions = []metav1.Condition{{Type: string(hypershiftv1beta1.ValidReleaseImage), Status: metav1.ConditionFalse,
			ObservedGeneration: 3, Reason: "UnsupportedReleaseImage"}}
		recordUpgradeObservations(order, hc, nil)
		Expect(order.Status.UpgradeObservation.State).To(BeEmpty())
	})

	It("ignores zero-generation NodePool conditions regardless of transition time", func() {
		order := newOrder()
		pool := newPool("compute", "cluster-id")
		baseline := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
		pool.Status.Conditions = []hypershiftv1beta1.NodePoolCondition{{
			Type: hypershiftv1beta1.NodePoolUpdatingVersionConditionType, Status: corev1.ConditionTrue,
			ObservedGeneration: 0, LastTransitionTime: metav1.NewTime(baseline.Add(time.Second)),
		}}
		recordUpgradeObservations(order, &hypershiftv1beta1.HostedCluster{}, []hypershiftv1beta1.NodePool{pool})
		Expect(order.Status.NodeRequests[0].UpgradeObservation.State).To(BeEmpty())
	})

	It("reports node-pool progress when the current UpdatingVersion condition is true", func() {
		order := newOrder()
		pool := newPool("compute", "cluster-id")
		pool.Status.Conditions = []hypershiftv1beta1.NodePoolCondition{{
			Type:   hypershiftv1beta1.NodePoolUpdatingVersionConditionType,
			Status: corev1.ConditionTrue, ObservedGeneration: 2,
		}}
		recordUpgradeObservations(order, &hypershiftv1beta1.HostedCluster{}, []hypershiftv1beta1.NodePool{pool})
		Expect(order.Status.NodeRequests[0].UpgradeObservation.State).To(Equal(v1alpha1.UpgradeObservationProgressing))
		Expect(order.Status.NodeRequests[0].UpgradeObservation.Version).To(Equal("4.17.0"))
		pool.Status.Conditions[0].ObservedGeneration = 1
		recordUpgradeObservations(order, &hypershiftv1beta1.HostedCluster{}, []hypershiftv1beta1.NodePool{pool})
		Expect(order.Status.NodeRequests[0].UpgradeObservation.State).To(BeEmpty())
	})

	It("requires the current UpdatingVersion, AllMachinesReady, and Ready conditions for node-pool success", func() {
		order := newOrder()
		pool := newPool("compute", "cluster-id")
		pool.Status.Version = "4.18.0"
		pool.Status.Conditions = []hypershiftv1beta1.NodePoolCondition{
			{Type: hypershiftv1beta1.NodePoolUpdatingVersionConditionType, Status: corev1.ConditionFalse, ObservedGeneration: 2},
			{Type: hypershiftv1beta1.NodePoolReadyConditionType, Status: corev1.ConditionTrue, ObservedGeneration: 2},
			{Type: hypershiftv1beta1.NodePoolAllMachinesReadyConditionType, Status: corev1.ConditionTrue, ObservedGeneration: 2},
		}
		recordUpgradeObservations(order, &hypershiftv1beta1.HostedCluster{}, []hypershiftv1beta1.NodePool{pool})
		Expect(order.Status.NodeRequests[0].UpgradeObservation.State).To(Equal(v1alpha1.UpgradeObservationSucceeded))
		Expect(order.Status.NodeRequests[0].UpgradeObservation.ReadyAtVersion).To(BeTrue())
	})

	DescribeTable("does not report node-pool success without each current signal", func(change func(*hypershiftv1beta1.NodePool)) {
		order := newOrder()
		pool := newPool("compute", "cluster-id")
		pool.Status.Version = "4.18.0"
		pool.Status.Conditions = []hypershiftv1beta1.NodePoolCondition{
			{Type: hypershiftv1beta1.NodePoolUpdatingVersionConditionType, Status: corev1.ConditionFalse, ObservedGeneration: 2},
			{Type: hypershiftv1beta1.NodePoolAllMachinesReadyConditionType, Status: corev1.ConditionTrue, ObservedGeneration: 2},
			{Type: hypershiftv1beta1.NodePoolReadyConditionType, Status: corev1.ConditionTrue, ObservedGeneration: 2},
		}
		change(&pool)
		recordUpgradeObservations(order, &hypershiftv1beta1.HostedCluster{}, []hypershiftv1beta1.NodePool{pool})
		Expect(order.Status.NodeRequests[0].UpgradeObservation.State).NotTo(Equal(v1alpha1.UpgradeObservationSucceeded))
	},
		Entry("status version is absent", func(pool *hypershiftv1beta1.NodePool) { pool.Status.Version = "" }),
		Entry("UpdatingVersion is absent", func(pool *hypershiftv1beta1.NodePool) {
			pool.Status.Conditions = pool.Status.Conditions[1:]
		}),
		Entry("UpdatingVersion is true", func(pool *hypershiftv1beta1.NodePool) {
			pool.Status.Conditions[0].Status = corev1.ConditionTrue
		}),
		Entry("AllMachinesReady is false", func(pool *hypershiftv1beta1.NodePool) {
			pool.Status.Conditions[1].Status = corev1.ConditionFalse
		}),
		Entry("Ready is false", func(pool *hypershiftv1beta1.NodePool) {
			pool.Status.Conditions[2].Status = corev1.ConditionFalse
		}),
		Entry("Ready is stale", func(pool *hypershiftv1beta1.NodePool) {
			pool.Status.Conditions[2].ObservedGeneration = 1
		}),
	)

	It("does not report a NodePool release condition as terminal failure", func() {
		order := newOrder()
		pool := newPool("compute", "cluster-id")
		pool.Status.Conditions = []hypershiftv1beta1.NodePoolCondition{{
			Type:   hypershiftv1beta1.NodePoolValidReleaseImageConditionType,
			Status: corev1.ConditionFalse, ObservedGeneration: 2, Reason: "ValidationFailed",
		}}
		recordUpgradeObservations(order, &hypershiftv1beta1.HostedCluster{}, []hypershiftv1beta1.NodePool{pool})
		Expect(order.Status.NodeRequests[0].UpgradeObservation.State).To(BeEmpty())
	})

	It("applies current NodePool progress to the frozen participant", func() {
		order := newOrder()
		remote := newActiveAttempt(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED)
		order.Status.NodeRequests[0].UpgradeObservation = &v1alpha1.UpgradeObservation{
			AttemptID: "cluster-id", State: v1alpha1.UpgradeObservationProgressing, Version: "4.17.0",
		}
		remote.GetStatus().GetActiveUpgradeAttempt().GetNodeSets()[0].SetStateTransitionTime(
			remote.GetStatus().GetActiveUpgradeAttempt().GetControlPlane().GetAcceptedTime())
		now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

		syncClusterOrderUpgradeObservationsAt(order, remote, now)

		nodeSet := remote.GetStatus().GetActiveUpgradeAttempt().GetNodeSets()[0]
		Expect(nodeSet.GetNodeSetId()).To(Equal("compute"))
		Expect(nodeSet.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING))
		Expect(nodeSet.GetStateTransitionTime().AsTime()).To(Equal(now))
	})

	It("advances NodePool progress beyond an equal pending timestamp", func() {
		order := newOrder()
		remote := newActiveAttempt(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED)
		order.Status.NodeRequests[0].UpgradeObservation = &v1alpha1.UpgradeObservation{
			AttemptID: "cluster-id", State: v1alpha1.UpgradeObservationProgressing, Version: "4.17.0",
		}
		nodeSet := remote.GetStatus().GetActiveUpgradeAttempt().GetNodeSets()[0]
		accepted := remote.GetStatus().GetActiveUpgradeAttempt().GetControlPlane().GetAcceptedTime()
		nodeSet.SetStateTransitionTime(accepted)

		syncClusterOrderUpgradeObservationsAt(order, remote, accepted.AsTime())

		Expect(nodeSet.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING))
		Expect(nodeSet.GetStateTransitionTime().AsTime()).To(Equal(accepted.AsTime().Add(time.Nanosecond)))
	})

	It("sends NodePool success without writing live observations", func() {
		order := newOrder()
		remote := newActiveAttempt(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED)
		remote.GetStatus().GetNodeSets()["compute"].SetObservedVersion("4.17.0")
		order.Status.NodeRequests[0].UpgradeObservation = &v1alpha1.UpgradeObservation{
			AttemptID: "cluster-id", State: v1alpha1.UpgradeObservationSucceeded,
			Version: "4.18.0", ReadyAtVersion: true,
		}
		now := time.Date(2026, 11, 18, 12, 10, 0, 0, time.UTC)

		syncClusterOrderUpgradeObservationsAt(order, remote, now)

		nodeSet := remote.GetStatus().GetActiveUpgradeAttempt().GetNodeSets()[0]
		Expect(nodeSet.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED))
		Expect(nodeSet.GetStateTransitionTime().AsTime()).To(Equal(now))
		Expect(remote.GetStatus().GetNodeSets()["compute"].GetObservedVersion()).To(Equal("4.17.0"))
	})

	It("accepts NodePool failure after progress and clears its timeout warning", func() {
		order := newOrder()
		remote := newActiveAttempt(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED)
		nodeSet := remote.GetStatus().GetActiveUpgradeAttempt().GetNodeSets()[0]
		nodeSet.SetState(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING)
		nodeSet.SetTimeoutTime(timestamppb.New(time.Date(2026, 11, 18, 11, 30, 0, 0, time.UTC)))
		nodeSet.SetMessage("NodePool upgrade is taking longer to finish")
		order.Status.NodeRequests[0].UpgradeObservation = &v1alpha1.UpgradeObservation{
			AttemptID: "cluster-id", State: v1alpha1.UpgradeObservationFailed,
			Message: "NodePool upgrade failed",
		}
		now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

		syncClusterOrderUpgradeObservationsAt(order, remote, now)

		Expect(nodeSet.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_FAILED))
		Expect(nodeSet.GetMessage()).To(Equal("NodePool upgrade failed"))
		Expect(nodeSet.GetStateTransitionTime().AsTime()).To(Equal(now))
		Expect(nodeSet.GetTimeoutTime()).To(BeNil())

		order.Status.NodeRequests[0].UpgradeObservation = &v1alpha1.UpgradeObservation{
			AttemptID: "cluster-id", State: v1alpha1.UpgradeObservationSucceeded,
			Version: "4.18.0", ReadyAtVersion: true,
		}
		syncClusterOrderUpgradeObservationsAt(order, remote, now.Add(time.Minute))
		Expect(nodeSet.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_FAILED))
		Expect(nodeSet.GetMessage()).To(Equal("NodePool upgrade failed"))
		Expect(nodeSet.GetStateTransitionTime().AsTime()).To(Equal(now))
	})

	It("retains NodePool success after later stale and failed observations", func() {
		order := newOrder()
		remote := newActiveAttempt(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED)
		nodeSet := remote.GetStatus().GetActiveUpgradeAttempt().GetNodeSets()[0]
		nodeSet.SetState(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED)
		transitionTime := time.Date(2026, 11, 18, 11, 0, 30, 0, time.UTC)
		nodeSet.SetStateTransitionTime(timestamppb.New(transitionTime))
		compute := func() *privatev1.ClusterUpgradeNodeSetState {
			for _, participant := range remote.GetStatus().GetActiveUpgradeAttempt().GetNodeSets() {
				if participant.GetNodeSetId() == "compute" {
					return participant
				}
			}
			return nil
		}
		order.Status.NodeRequests[0].UpgradeObservation = &v1alpha1.UpgradeObservation{
			AttemptID: "cluster-id", State: v1alpha1.UpgradeObservationProgressing, Version: "4.17.0",
		}

		syncClusterOrderUpgradeObservationsAt(order, remote, time.Date(2026, 11, 18, 11, 1, 0, 0, time.UTC))

		current := compute()
		Expect(current).NotTo(BeNil())
		Expect(current.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED))
		Expect(current.GetStateTransitionTime().AsTime()).To(Equal(transitionTime))

		order.Status.NodeRequests[0].UpgradeObservation = &v1alpha1.UpgradeObservation{
			AttemptID: "cluster-id", State: v1alpha1.UpgradeObservationFailed, Message: "NodePool upgrade failed",
		}
		syncClusterOrderUpgradeObservationsAt(order, remote, time.Date(2026, 11, 18, 11, 2, 0, 0, time.UTC))

		current = compute()
		Expect(current).NotTo(BeNil())
		Expect(current.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED))
		Expect(current.GetStateTransitionTime().AsTime()).To(Equal(transitionTime))
	})

	It("ignores feedback for a node set not frozen into the attempt", func() {
		order := newOrder()
		order.Spec.NodeRequests = append(order.Spec.NodeRequests, v1alpha1.NodeRequest{NodeSet: "storage"})
		order.Status.NodeRequests = append(order.Status.NodeRequests, v1alpha1.NodeRequestStatus{
			NodeSet: "storage", NumberOfNodes: 1, ObservedVersion: "4.18.1",
			UpgradeObservation: &v1alpha1.UpgradeObservation{
				AttemptID: "cluster-id", State: v1alpha1.UpgradeObservationSucceeded,
				Version: "4.18.0", ReadyAtVersion: true,
			},
		})
		remote := newActiveAttempt(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED)
		remote.GetSpec().GetNodeSets()["storage"] = &privatev1.ClusterNodeSet{}
		remote.GetStatus().GetNodeSets()["storage"] = &privatev1.ClusterNodeSetStatus{}
		remote.GetStatus().GetNodeSets()["storage"].SetObservedVersion("4.17.0")

		syncClusterOrderUpgradeObservationsAt(order, remote, time.Date(2026, 11, 18, 11, 1, 0, 0, time.UTC))

		Expect(remote.GetStatus().GetActiveUpgradeAttempt().GetNodeSets()).To(HaveLen(2))
		for _, nodeSet := range remote.GetStatus().GetActiveUpgradeAttempt().GetNodeSets() {
			Expect(nodeSet.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING))
		}
		Expect(remote.GetStatus().GetNodeSets()["storage"].GetObservedVersion()).To(Equal("4.17.0"))

		syncClusterOrderObservedVersions(order, remote)
		Expect(remote.GetStatus().GetNodeSets()["storage"].GetObservedVersion()).To(Equal("4.18.1"))
		Expect(remote.GetStatus().GetActiveUpgradeAttempt().GetNodeSets()).To(HaveLen(2))
	})

	It("ignores live progress for a removed node set", func() {
		order := newOrder()
		remote := newActiveAttempt(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED)
		delete(remote.GetSpec().GetNodeSets(), "gpu")
		order.Status.NodeRequests[1].UpgradeObservation = &v1alpha1.UpgradeObservation{
			AttemptID: "cluster-id", State: v1alpha1.UpgradeObservationProgressing, Version: "4.17.0",
		}

		syncClusterOrderUpgradeObservationsAt(order, remote, time.Date(2026, 11, 18, 11, 1, 0, 0, time.UTC))

		Expect(remote.GetStatus().GetActiveUpgradeAttempt().GetNodeSets()[1].GetState()).
			To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING))
	})

	Describe("phase timeout warnings", func() {
		now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
		config := ClusterUpgradeTimeouts{
			Pending: 2 * time.Minute, ControlPlane: 5 * time.Minute, NodePoolPerNode: 3 * time.Minute,
		}
		newObservation := func(state v1alpha1.UpgradeObservationState, version string) *v1alpha1.UpgradeObservation {
			return &v1alpha1.UpgradeObservation{
				AttemptID: "cluster-id", State: state, Version: version,
			}
		}

		It("clears a control-plane start warning on progress and retains a completion warning until success", func() {
			order := newOrder()
			remote := newActiveAttempt(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING)
			controlPlane := remote.GetStatus().GetActiveUpgradeAttempt().GetControlPlane()
			accepted := controlPlane.GetAcceptedTime().AsTime()

			syncClusterOrderUpgradeObservationsAt(order, remote, accepted.Add(config.Pending), config)
			Expect(controlPlane.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING))
			Expect(controlPlane.GetTimeoutTime()).NotTo(BeNil())
			Expect(controlPlane.GetMessage()).To(ContainSubstring("progress was not observed"))
			Expect(remote.GetStatus().GetUpgradeHistory()[0].GetState()).
				To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING))

			order.Status.UpgradeObservation = newObservation(v1alpha1.UpgradeObservationProgressing, "4.18.0")
			syncClusterOrderUpgradeObservationsAt(order, remote, now, config)
			Expect(controlPlane.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING))
			Expect(controlPlane.GetTimeoutTime()).To(BeNil())
			Expect(controlPlane.GetMessage()).To(BeEmpty())

			syncClusterOrderUpgradeObservationsAt(order, remote, now.Add(config.ControlPlane), config)
			Expect(controlPlane.GetTimeoutTime()).NotTo(BeNil())
			Expect(controlPlane.GetTimeoutTime().AsTime()).To(Equal(now.Add(config.ControlPlane)))
			warningTime := controlPlane.GetTimeoutTime().AsTime()
			progressTime := controlPlane.GetStateTransitionTime().AsTime()
			Expect(controlPlane.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING))
			Expect(controlPlane.GetMessage()).To(ContainSubstring("completion was not observed"))

			syncClusterOrderUpgradeObservationsAt(order, remote, now.Add(config.ControlPlane+time.Minute), config)
			Expect(controlPlane.GetTimeoutTime().AsTime()).To(Equal(warningTime))
			Expect(controlPlane.GetStateTransitionTime().AsTime()).To(Equal(progressTime))

			order.Status.UpgradeObservation = newObservation(v1alpha1.UpgradeObservationSucceeded, "4.18.0")
			syncClusterOrderUpgradeObservationsAt(order, remote, now.Add(config.ControlPlane+2*time.Minute), config)
			Expect(controlPlane.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED))
			Expect(controlPlane.GetTimeoutTime()).To(BeNil())
		})

		It("uses the frozen NodePool size and keeps a completion warning through repeated progress", func() {
			order := newOrder()
			remote := newActiveAttempt(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED)
			participant := remote.GetStatus().GetActiveUpgradeAttempt().GetNodeSets()[0]
			remote.GetStatus().GetActiveUpgradeAttempt().SetNodeSetsScheduledTime(timestamppb.New(now))
			remote.GetSpec().GetNodeSets()["compute"].SetSize(10)

			syncClusterOrderUpgradeObservationsAt(order, remote, now.Add(config.Pending), config)
			Expect(participant.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING))
			Expect(participant.GetTimeoutTime()).NotTo(BeNil())
			Expect(participant.GetMessage()).To(ContainSubstring("NodePool \"compute\""))

			order.Status.NodeRequests[0].UpgradeObservation =
				newObservation(v1alpha1.UpgradeObservationProgressing, "4.17.0")
			progressTime := now.Add(config.Pending + time.Minute)
			syncClusterOrderUpgradeObservationsAt(order, remote, progressTime, config)
			Expect(participant.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING))
			Expect(participant.GetTimeoutTime()).To(BeNil())
			Expect(participant.GetTimeoutNodeCount()).To(Equal(int32(2)))

			syncClusterOrderUpgradeObservationsAt(order, remote, progressTime.Add(5*time.Minute), config)
			Expect(participant.GetTimeoutTime()).To(BeNil())
			syncClusterOrderUpgradeObservationsAt(order, remote, progressTime.Add(6*time.Minute), config)
			Expect(participant.GetTimeoutTime()).NotTo(BeNil())
			Expect(participant.GetTimeoutTime().AsTime()).To(Equal(progressTime.Add(2 * config.NodePoolPerNode)))
			warningTime := participant.GetTimeoutTime().AsTime()
			Expect(participant.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING))
			Expect(participant.GetMessage()).To(ContainSubstring("completion was not observed"))

			syncClusterOrderUpgradeObservationsAt(order, remote, progressTime.Add(7*time.Minute), config)
			Expect(participant.GetTimeoutTime().AsTime()).To(Equal(warningTime))
			Expect(participant.GetStateTransitionTime().AsTime()).To(Equal(progressTime))

			order.Status.NodeRequests[0].UpgradeObservation =
				newObservation(v1alpha1.UpgradeObservationSucceeded, "4.18.0")
			order.Status.NodeRequests[0].UpgradeObservation.ReadyAtVersion = true
			syncClusterOrderUpgradeObservationsAt(order, remote, progressTime.Add(8*time.Minute), config)
			Expect(participant.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED))
			Expect(participant.GetTimeoutTime()).To(BeNil())
		})

		It("does not time out a removed NodePool", func() {
			remote := newActiveAttempt(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED)
			delete(remote.GetSpec().GetNodeSets(), "compute")
			syncClusterOrderUpgradeObservationsAt(newOrder(), remote, now.Add(config.Pending), config)
			Expect(remote.GetStatus().GetActiveUpgradeAttempt().GetNodeSets()[0].GetTimeoutTime()).To(BeNil())
		})

		It("caps NodePool completion duration overflow", func() {
			Expect(nodeSetCompletionTimeout(math.MaxInt32, time.Hour)).
				To(Equal(time.Duration(math.MaxInt64)))
		})
	})

	It("applies control-plane progress at the target version", func() {
		order := newOrder()
		remote := newActiveAttempt(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING)
		order.Status.UpgradeObservation = &v1alpha1.UpgradeObservation{
			AttemptID: "cluster-id", State: v1alpha1.UpgradeObservationProgressing, Version: "4.18.0",
		}
		now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

		syncClusterOrderUpgradeObservationsAt(order, remote, now)

		controlPlane := remote.GetStatus().GetActiveUpgradeAttempt().GetControlPlane()
		Expect(controlPlane.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING))
		Expect(controlPlane.GetStateTransitionTime().AsTime()).To(Equal(now))
	})

	It("starts node-set progress only after scheduling", func() {
		order := newOrder()
		remote := newActiveAttempt(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED)
		nodeSet := remote.GetStatus().GetActiveUpgradeAttempt().GetNodeSets()[0]
		remote.GetStatus().GetActiveUpgradeAttempt().ClearNodeSetsScheduledTime()
		order.Status.NodeRequests[0].UpgradeObservation = &v1alpha1.UpgradeObservation{
			AttemptID: "cluster-id", State: v1alpha1.UpgradeObservationProgressing, Version: "4.18.0",
		}
		now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

		syncClusterOrderUpgradeObservationsAt(order, remote, now)
		Expect(nodeSet.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING))

		remote.GetStatus().GetActiveUpgradeAttempt().SetNodeSetsScheduledTime(timestamppb.New(now))
		syncClusterOrderUpgradeObservationsAt(order, remote, now.Add(time.Second))
		Expect(nodeSet.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING))
	})

	It("does not apply control-plane progress at another version", func() {
		order := newOrder()
		remote := newActiveAttempt(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING)
		order.Status.UpgradeObservation = &v1alpha1.UpgradeObservation{
			AttemptID: "cluster-id", State: v1alpha1.UpgradeObservationProgressing, Version: "4.19.0",
		}

		syncClusterOrderUpgradeObservationsAt(order, remote, time.Date(2026, 11, 18, 11, 1, 0, 0, time.UTC))

		Expect(remote.GetStatus().GetActiveUpgradeAttempt().GetControlPlane().GetState()).
			To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING))
	})

	It("accepts control-plane success without writing live observations", func() {
		order := newOrder()
		remote := newActiveAttempt(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING)
		order.Status.UpgradeObservation = &v1alpha1.UpgradeObservation{
			AttemptID: "cluster-id", State: v1alpha1.UpgradeObservationSucceeded, Version: "4.18.0",
		}
		now := time.Date(2026, 11, 18, 12, 5, 0, 0, time.UTC)

		syncClusterOrderUpgradeObservationsAt(order, remote, now)

		controlPlane := remote.GetStatus().GetActiveUpgradeAttempt().GetControlPlane()
		Expect(controlPlane.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED))
		Expect(controlPlane.GetStateTransitionTime().AsTime()).To(Equal(now))
		Expect(remote.GetStatus().GetObservedVersion()).To(Equal("4.17.0"))
	})

	It("accepts control-plane failure from pending and clears its timeout warning", func() {
		order := newOrder()
		remote := newActiveAttempt(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING)
		controlPlane := remote.GetStatus().GetActiveUpgradeAttempt().GetControlPlane()
		controlPlane.SetTimeoutTime(timestamppb.New(time.Date(2026, 11, 18, 11, 30, 0, 0, time.UTC)))
		controlPlane.SetMessage("Control plane upgrade is taking longer to start")
		order.Status.UpgradeObservation = &v1alpha1.UpgradeObservation{
			AttemptID: "cluster-id", State: v1alpha1.UpgradeObservationFailed,
			Message: "Control plane upgrade failed",
		}
		now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

		syncClusterOrderUpgradeObservationsAt(order, remote, now)

		Expect(controlPlane.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_FAILED))
		Expect(controlPlane.GetMessage()).To(Equal("Control plane upgrade failed"))
		Expect(controlPlane.GetStateTransitionTime().AsTime()).To(Equal(now))
		Expect(controlPlane.GetTimeoutTime()).To(BeNil())

		order.Status.UpgradeObservation = &v1alpha1.UpgradeObservation{
			AttemptID: "cluster-id", State: v1alpha1.UpgradeObservationSucceeded, Version: "4.18.0",
		}
		syncClusterOrderUpgradeObservationsAt(order, remote, now.Add(time.Minute))
		Expect(controlPlane.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_FAILED))
		Expect(controlPlane.GetMessage()).To(Equal("Control plane upgrade failed"))
		Expect(controlPlane.GetStateTransitionTime().AsTime()).To(Equal(now))
	})

	It("retains control-plane success after degradation and failed feedback", func() {
		order := newOrder()
		remote := newActiveAttempt(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING)
		order.Status.UpgradeObservation = &v1alpha1.UpgradeObservation{
			AttemptID: "cluster-id", State: v1alpha1.UpgradeObservationSucceeded, Version: "4.18.0",
		}
		now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
		syncClusterOrderUpgradeObservationsAt(order, remote, now)

		order.Status.UpgradeObservation = &v1alpha1.UpgradeObservation{
			AttemptID: "cluster-id", Version: "4.17.0",
		}
		syncClusterOrderUpgradeObservationsAt(order, remote, now.Add(time.Minute))
		order.Status.UpgradeObservation = &v1alpha1.UpgradeObservation{
			AttemptID: "cluster-id", State: v1alpha1.UpgradeObservationFailed,
			Message: "Control plane upgrade failed",
		}
		syncClusterOrderUpgradeObservationsAt(order, remote, now.Add(2*time.Minute))

		controlPlane := remote.GetStatus().GetActiveUpgradeAttempt().GetControlPlane()
		Expect(controlPlane.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED))
		Expect(controlPlane.GetStateTransitionTime().AsTime()).To(Equal(now))
		Expect(remote.GetStatus().GetObservedVersion()).To(Equal("4.17.0"))
	})

	It("ignores control-plane feedback for another attempt", func() {
		order := newOrder()
		remote := newActiveAttempt(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING)
		order.Status.UpgradeObservation = &v1alpha1.UpgradeObservation{
			AttemptID: "old-cluster-id", State: v1alpha1.UpgradeObservationSucceeded, Version: "4.18.0",
		}

		syncClusterOrderUpgradeObservationsAt(order, remote, time.Date(2026, 11, 18, 11, 1, 0, 0, time.UTC))

		Expect(remote.GetStatus().GetActiveUpgradeAttempt().GetControlPlane().GetState()).
			To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING))
		Expect(remote.GetStatus().GetObservedVersion()).To(Equal("4.17.0"))
	})

	It("keeps continuous observed-version feedback independent of attempt state", func() {
		order := newOrder()
		order.Status.ObservedVersion = "4.18.1"
		order.Status.NodeRequests[0].NumberOfNodes = 1
		order.Status.NodeRequests[0].ObservedVersion = "4.18.1"
		remote := newActiveAttempt(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING)

		syncClusterOrderObservedVersions(order, remote)

		Expect(remote.GetStatus().GetObservedVersion()).To(Equal("4.18.1"))
		Expect(remote.GetStatus().GetNodeSets()["compute"].GetObservedVersion()).To(Equal("4.18.1"))
		Expect(remote.GetStatus().GetActiveUpgradeAttempt().GetControlPlane().GetState()).
			To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING))

		remote.GetStatus().ClearActiveUpgradeAttempt()
		order.Status.ObservedVersion = "4.18.2"
		order.Status.NodeRequests[0].ObservedVersion = "4.18.2"
		syncClusterOrderObservedVersions(order, remote)
		Expect(remote.GetStatus().GetObservedVersion()).To(Equal("4.18.2"))
		Expect(remote.GetStatus().GetNodeSets()["compute"].GetObservedVersion()).To(Equal("4.18.2"))
	})
})
