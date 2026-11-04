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
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("upgrade attempt correlation", func() {
	const image = "quay.io/ocp/release:4.18.0"
	newOrder := func() *v1alpha1.ClusterOrder {
		return &v1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
				v1alpha1.UpgradeAttemptIDsAnnotation: `{"controlPlane":"cp-id","nodeSets":{"compute":"compute-id","gpu":"gpu-id"}}`,
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
			Generation: 3, Annotations: map[string]string{v1alpha1.UpgradeAttemptIDAnnotation: "cp-id"},
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

	It("observes the control plane and all stamped node sets independently", func() {
		order := newOrder()
		hc := newControlPlane()
		hc.Status.Version = versionStatus("4.18.0", configv1.CompletedUpdate)
		hc.Status.ControlPlaneVersion = controlPlaneVersionStatus("4.18.0", configv1.CompletedUpdate)
		hc.Status.Conditions = healthyConditions()
		recordUpgradeObservations(order, hc, []hypershiftv1beta1.NodePool{
			newPool("compute", "compute-id"), newPool("gpu", "gpu-id"),
		})
		Expect(order.Status.UpgradeObservation.AttemptID).To(Equal("cp-id"))
		Expect(order.Status.UpgradeObservation.State).To(Equal(v1alpha1.UpgradeObservationSucceeded))
		Expect(order.Status.UpgradeObservation.Version).To(Equal("4.18.0"))
		Expect(order.Status.NodeRequests[0].UpgradeObservation.AttemptID).To(Equal("compute-id"))
		Expect(order.Status.NodeRequests[1].UpgradeObservation.AttemptID).To(Equal("gpu-id"))
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
		Expect(order.Status.UpgradeObservation.AttemptID).To(Equal("cp-id"))
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

	It("does not start a new attempt from an older NodePool transition", func() {
		order := newOrder()
		pool := newPool("compute", "previous-id")
		baseline := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
		pool.Status.Conditions = []hypershiftv1beta1.NodePoolCondition{{
			Type: hypershiftv1beta1.NodePoolUpdatingVersionConditionType, Status: "True",
			LastTransitionTime: metav1.NewTime(baseline),
		}}
		recordUpgradeObservations(order, &hypershiftv1beta1.HostedCluster{}, []hypershiftv1beta1.NodePool{pool})
		Expect(order.Status.NodeRequests[0].UpgradeObservation.BaselineTransitionTime.Time).To(Equal(baseline))
		pool.Annotations[v1alpha1.UpgradeAttemptIDAnnotation] = "compute-id"
		recordUpgradeObservations(order, &hypershiftv1beta1.HostedCluster{}, []hypershiftv1beta1.NodePool{pool})
		Expect(order.Status.NodeRequests[0].UpgradeObservation.State).To(BeEmpty())
		pool.Status.Conditions[0].LastTransitionTime = metav1.NewTime(baseline.Add(time.Second))
		recordUpgradeObservations(order, &hypershiftv1beta1.HostedCluster{}, []hypershiftv1beta1.NodePool{pool})
		Expect(order.Status.NodeRequests[0].UpgradeObservation.State).To(Equal(v1alpha1.UpgradeObservationProgressing))
	})

	It("reports node-pool progress when the current UpdatingVersion condition is true", func() {
		order := newOrder()
		pool := newPool("compute", "compute-id")
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
		pool := newPool("compute", "compute-id")
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
		pool := newPool("compute", "compute-id")
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
		pool := newPool("compute", "compute-id")
		pool.Status.Conditions = []hypershiftv1beta1.NodePoolCondition{{
			Type:   hypershiftv1beta1.NodePoolValidReleaseImageConditionType,
			Status: corev1.ConditionFalse, ObservedGeneration: 2, Reason: "ValidationFailed",
		}}
		recordUpgradeObservations(order, &hypershiftv1beta1.HostedCluster{}, []hypershiftv1beta1.NodePool{pool})
		Expect(order.Status.NodeRequests[0].UpgradeObservation.State).To(BeEmpty())
	})

	It("does not promote failed feedback with a stale readiness bit", func() {
		entry := privatev1.ClusterUpgradeHistoryEntry_builder{
			TargetVersion: "4.18.0",
			State:         privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING,
		}.Build()
		observation := &v1alpha1.UpgradeObservation{
			State: v1alpha1.UpgradeObservationFailed, Version: "4.18.0",
			ReadyAtVersion: true, Message: "Release image was rejected",
		}
		syncUpgradeEntry(entry, observation, time.Now())
		Expect(entry.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_FAILED))
		Expect(entry.GetMessage()).To(Equal(observation.Message))
	})

	It("does not attribute an old ID or another release image to the current attempt", func() {
		order := newOrder()
		hc := &hypershiftv1beta1.HostedCluster{}
		old := newPool("compute", "previous-compute-id")
		stale := newPool("gpu", "gpu-id")
		stale.Spec.Release.Image = "quay.io/ocp/release:4.17.0"
		order.Status.UpgradeObservation = &v1alpha1.UpgradeObservation{AttemptID: "cp-id"}
		order.Status.NodeRequests[0].UpgradeObservation = &v1alpha1.UpgradeObservation{AttemptID: "compute-id"}
		order.Status.NodeRequests[1].UpgradeObservation = &v1alpha1.UpgradeObservation{AttemptID: "gpu-id"}
		recordUpgradeObservations(order, hc, []hypershiftv1beta1.NodePool{old, stale})
		Expect(order.Status.UpgradeObservation).To(BeNil())
		Expect(order.Status.NodeRequests[0].UpgradeObservation).To(BeNil())
		Expect(order.Status.NodeRequests[1].UpgradeObservation).To(BeNil())
	})

	It("does not copy attempt observations into independently observed versions", func() {
		order := newOrder()
		order.Status.UpgradeObservation = &v1alpha1.UpgradeObservation{AttemptID: "cp-id", Version: "4.18.0"}
		order.Status.NodeRequests[0].UpgradeObservation = &v1alpha1.UpgradeObservation{AttemptID: "compute-id", Version: "4.17.0"}
		order.Status.NodeRequests[1].UpgradeObservation = &v1alpha1.UpgradeObservation{AttemptID: "old-gpu-id", Version: "4.17.0"}
		cp := &privatev1.ClusterUpgradeHistoryEntry{}
		cp.SetAttemptId("cp-id")
		cpComponent := &privatev1.ClusterUpgradeComponent{}
		cpComponent.SetControlPlane(&emptypb.Empty{})
		cp.SetComponent(cpComponent)
		compute := &privatev1.ClusterUpgradeHistoryEntry{}
		compute.SetAttemptId("compute-id")
		computeComponent := &privatev1.ClusterUpgradeComponent{}
		computeComponent.SetNodeSetId("compute")
		compute.SetComponent(computeComponent)
		remote := &privatev1.Cluster{}
		spec := &privatev1.ClusterSpec{}
		spec.SetControlPlaneAttemptId("cp-id")
		spec.SetNodeSetAttemptIds(map[string]string{"compute": "compute-id", "gpu": "gpu-id"})
		remote.SetSpec(spec)
		status := &privatev1.ClusterStatus{}
		status.SetObservedVersion("4.17.0")
		status.SetNodeSets(map[string]*privatev1.ClusterNodeSet{
			"compute": {}, "gpu": {},
		})
		status.GetNodeSets()["gpu"].SetObservedVersion("4.16.0")
		status.SetUpgradeHistory([]*privatev1.ClusterUpgradeHistoryEntry{compute, cp})
		remote.SetStatus(status)
		syncClusterOrderUpgradeObservations(order, remote)
		Expect(remote.GetStatus().GetObservedVersion()).To(Equal("4.17.0"))
		Expect(remote.GetStatus().GetNodeSets()["compute"].GetObservedVersion()).To(BeEmpty())
		Expect(remote.GetStatus().GetNodeSets()["gpu"].GetObservedVersion()).To(Equal("4.16.0"))
	})

	It("requires the target version for control-plane progress and success attribution", func() {
		component := &privatev1.ClusterUpgradeComponent{}
		component.SetControlPlane(&emptypb.Empty{})
		entry := privatev1.ClusterUpgradeHistoryEntry_builder{
			Component: component, TargetVersion: "4.18.0",
			State: privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING,
		}.Build()
		observation := &v1alpha1.UpgradeObservation{
			State: v1alpha1.UpgradeObservationProgressing, Version: "4.19.0",
		}
		syncUpgradeEntry(entry, observation, time.Now())
		Expect(entry.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING))
		observation.Version = "4.18.0"
		syncUpgradeEntry(entry, observation, time.Now())
		Expect(entry.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING))
		observation.State = v1alpha1.UpgradeObservationSucceeded
		observation.Version = "4.19.0"
		syncUpgradeEntry(entry, observation, time.Now())
		Expect(entry.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING))
	})

	It("does not infer node-pool progress from a target status version alone", func() {
		component := &privatev1.ClusterUpgradeComponent{}
		component.SetNodeSetId("compute")
		entry := privatev1.ClusterUpgradeHistoryEntry_builder{
			Component: component, TargetVersion: "4.18.0",
			State: privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING,
		}.Build()
		observation := &v1alpha1.UpgradeObservation{Version: "4.18.0"}
		syncUpgradeEntry(entry, observation, time.Now())
		Expect(entry.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING))
		observation.State = v1alpha1.UpgradeObservationProgressing
		observation.Version = "4.17.0"
		syncUpgradeEntry(entry, observation, time.Now())
		Expect(entry.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING))
	})

	It("updates every component independently when attempt IDs are shared", func() {
		order := newOrder()
		order.Status.UpgradeObservation = &v1alpha1.UpgradeObservation{
			AttemptID: "shared-id", State: v1alpha1.UpgradeObservationSucceeded, Version: "4.18.0"}
		order.Status.NodeRequests[0].UpgradeObservation = &v1alpha1.UpgradeObservation{
			AttemptID: "shared-id", State: v1alpha1.UpgradeObservationFailed, Message: "Compute upgrade failed"}
		order.Status.NodeRequests[1].UpgradeObservation = &v1alpha1.UpgradeObservation{
			AttemptID: "shared-id", State: v1alpha1.UpgradeObservationProgressing}
		controlPlane := &privatev1.ClusterUpgradeComponent{}
		controlPlane.SetControlPlane(&emptypb.Empty{})
		compute := &privatev1.ClusterUpgradeComponent{}
		compute.SetNodeSetId("compute")
		gpu := &privatev1.ClusterUpgradeComponent{}
		gpu.SetNodeSetId("gpu")
		entry := func(component *privatev1.ClusterUpgradeComponent) *privatev1.ClusterUpgradeHistoryEntry {
			return privatev1.ClusterUpgradeHistoryEntry_builder{
				AttemptId: "shared-id", Component: component, TargetVersion: "4.18.0",
				State: privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING,
			}.Build()
		}
		cpEntry, computeEntry, gpuEntry := entry(controlPlane), entry(compute), entry(gpu)
		remote := privatev1.Cluster_builder{
			Spec: privatev1.ClusterSpec_builder{ControlPlaneAttemptId: "shared-id",
				NodeSetAttemptIds: map[string]string{"compute": "shared-id", "gpu": "shared-id"}}.Build(),
			Status: privatev1.ClusterStatus_builder{UpgradeHistory: []*privatev1.ClusterUpgradeHistoryEntry{
				gpuEntry, computeEntry, cpEntry,
			}}.Build(),
		}.Build()

		syncClusterOrderUpgradeObservationsAt(order, remote, time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC))
		Expect(cpEntry.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED))
		Expect(computeEntry.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_FAILED))
		Expect(computeEntry.GetMessage()).To(Equal("Compute upgrade failed"))
		Expect(gpuEntry.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING))
	})

	It("times out the control plane forty minutes after Progressing", func() {
		order := newOrder()
		now := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
		cp := &privatev1.ClusterUpgradeHistoryEntry{}
		cp.SetAttemptId("cp-id")
		cp.SetTargetVersion("4.18.0")
		cp.SetState(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING)
		cp.SetAcceptedTime(timestamppb.New(now))
		cp.SetStateTransitionTime(timestamppb.New(now))
		component := &privatev1.ClusterUpgradeComponent{}
		component.SetControlPlane(&emptypb.Empty{})
		cp.SetComponent(component)
		remote := &privatev1.Cluster{}
		remote.SetSpec(privatev1.ClusterSpec_builder{ControlPlaneAttemptId: "cp-id"}.Build())
		remote.SetStatus(privatev1.ClusterStatus_builder{ObservedVersion: "4.17.0",
			UpgradeHistory: []*privatev1.ClusterUpgradeHistoryEntry{cp}}.Build())
		order.Status.UpgradeObservation = &v1alpha1.UpgradeObservation{
			AttemptID: "cp-id", State: v1alpha1.UpgradeObservationProgressing, Version: "4.18.0",
		}
		syncClusterOrderUpgradeObservationsAt(order, remote, now.Add(5*time.Minute))
		Expect(cp.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING))
		Expect(cp.GetStateTransitionTime().AsTime()).To(Equal(now.Add(5 * time.Minute)))
		syncClusterOrderUpgradeObservationsAt(order, remote, now.Add(44*time.Minute))
		Expect(cp.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING))
		syncClusterOrderUpgradeObservationsAt(order, remote, now.Add(45*time.Minute))
		Expect(cp.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_TIMED_OUT))
		Expect(remote.GetStatus().GetObservedVersion()).To(Equal("4.17.0"))
		order.Status.UpgradeObservation.State = v1alpha1.UpgradeObservationSucceeded
		order.Status.UpgradeObservation.Version = "4.18.0"
		syncClusterOrderUpgradeObservationsAt(order, remote, now.Add(46*time.Minute))
		Expect(cp.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED))
		Expect(remote.GetStatus().GetObservedVersion()).To(Equal("4.17.0"))
	})

	It("times out a pending node pool ten minutes after acceptance", func() {
		order := newOrder()
		now := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
		entry := &privatev1.ClusterUpgradeHistoryEntry{}
		entry.SetAttemptId("compute-id")
		entry.SetTargetVersion("4.18.0")
		entry.SetState(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING)
		entry.SetAcceptedTime(timestamppb.New(now))
		entry.SetStateTransitionTime(timestamppb.New(now))
		entry.SetTimeoutNodeCount(2)
		component := &privatev1.ClusterUpgradeComponent{}
		component.SetNodeSetId("compute")
		entry.SetComponent(component)
		remote := &privatev1.Cluster{}
		remote.SetSpec(privatev1.ClusterSpec_builder{NodeSetAttemptIds: map[string]string{"compute": "compute-id"},
			NodeSets: map[string]*privatev1.ClusterNodeSet{"compute": {}}}.Build())
		remote.SetStatus(privatev1.ClusterStatus_builder{NodeSets: map[string]*privatev1.ClusterNodeSet{"compute": {}},
			UpgradeHistory: []*privatev1.ClusterUpgradeHistoryEntry{entry}}.Build())
		syncClusterOrderUpgradeObservationsAt(order, remote, now.Add(9*time.Minute))
		Expect(entry.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING))
		syncClusterOrderUpgradeObservationsAt(order, remote, now.Add(10*time.Minute))
		Expect(entry.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_TIMED_OUT))
		Expect(entry.GetMessage()).To(ContainSubstring("progress"))
		order.Status.NodeRequests[0].UpgradeObservation = &v1alpha1.UpgradeObservation{AttemptID: "compute-id",
			State: v1alpha1.UpgradeObservationSucceeded, Version: "4.17.0"}
		syncClusterOrderUpgradeObservationsAt(order, remote, now.Add(11*time.Minute))
		Expect(entry.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_TIMED_OUT))
		Expect(remote.GetStatus().GetNodeSets()["compute"].GetObservedVersion()).To(BeEmpty())
	})

	It("uses the configured per-node completion timeout", func() {
		now := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
		entry := &privatev1.ClusterUpgradeHistoryEntry{}
		entry.SetAttemptId("compute-id")
		entry.SetState(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING)
		entry.SetAcceptedTime(timestamppb.New(now.Add(-time.Minute)))
		entry.SetStateTransitionTime(timestamppb.New(now))
		entry.SetTimeoutNodeCount(2)
		component := &privatev1.ClusterUpgradeComponent{}
		component.SetNodeSetId("compute")
		entry.SetComponent(component)
		remote := privatev1.Cluster_builder{
			Spec: privatev1.ClusterSpec_builder{NodeSetAttemptIds: map[string]string{"compute": "compute-id"},
				NodeSets: map[string]*privatev1.ClusterNodeSet{"compute": {}}}.Build(),
			Status: privatev1.ClusterStatus_builder{UpgradeHistory: []*privatev1.ClusterUpgradeHistoryEntry{entry}}.Build(),
		}.Build()
		config := ClusterUpgradeTimeouts{Pending: 2 * time.Minute, ControlPlane: 5 * time.Minute, NodePoolPerNode: 3 * time.Minute}
		applyUpgradeTimeouts(remote, now.Add(5*time.Minute), config)
		Expect(entry.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING))
		applyUpgradeTimeouts(remote, now.Add(6*time.Minute), config)
		Expect(entry.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_TIMED_OUT))
		Expect(entry.GetMessage()).To(ContainSubstring("completion"))
	})

	It("uses the configured pending and control-plane completion timeouts", func() {
		now := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
		entry := &privatev1.ClusterUpgradeHistoryEntry{}
		entry.SetAttemptId("cp-id")
		entry.SetState(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING)
		entry.SetAcceptedTime(timestamppb.New(now))
		entry.SetStateTransitionTime(timestamppb.New(now))
		component := &privatev1.ClusterUpgradeComponent{}
		component.SetControlPlane(&emptypb.Empty{})
		entry.SetComponent(component)
		remote := privatev1.Cluster_builder{
			Spec:   privatev1.ClusterSpec_builder{ControlPlaneAttemptId: "cp-id"}.Build(),
			Status: privatev1.ClusterStatus_builder{UpgradeHistory: []*privatev1.ClusterUpgradeHistoryEntry{entry}}.Build(),
		}.Build()
		config := ClusterUpgradeTimeouts{Pending: 2 * time.Minute, ControlPlane: 5 * time.Minute, NodePoolPerNode: 3 * time.Minute}
		applyUpgradeTimeouts(remote, now.Add(2*time.Minute), config)
		Expect(entry.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_TIMED_OUT))
		Expect(entry.GetMessage()).To(ContainSubstring("progress"))
		entry.SetState(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING)
		entry.SetStateTransitionTime(timestamppb.New(now.Add(3 * time.Minute)))
		applyUpgradeTimeouts(remote, now.Add(8*time.Minute), config)
		Expect(entry.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_TIMED_OUT))
		Expect(entry.GetMessage()).To(ContainSubstring("completion"))
	})

	It("requeues only current attempts awaiting timeout", func() {
		entry := &privatev1.ClusterUpgradeHistoryEntry{}
		entry.SetAttemptId("cp-id")
		entry.SetState(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING)
		entry.SetAcceptedTime(timestamppb.Now())
		component := &privatev1.ClusterUpgradeComponent{}
		component.SetControlPlane(&emptypb.Empty{})
		entry.SetComponent(component)
		remote := privatev1.Cluster_builder{
			Spec:   privatev1.ClusterSpec_builder{ControlPlaneAttemptId: "cp-id"}.Build(),
			Status: privatev1.ClusterStatus_builder{UpgradeHistory: []*privatev1.ClusterUpgradeHistoryEntry{entry}}.Build(),
		}.Build()
		Expect(hasUpgradeAwaitingTimeout(remote)).To(BeTrue())
		entry.SetState(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING)
		Expect(hasUpgradeAwaitingTimeout(remote)).To(BeTrue())
		entry.SetState(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_TIMED_OUT)
		Expect(hasUpgradeAwaitingTimeout(remote)).To(BeFalse())
		entry.SetState(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED)
		Expect(hasUpgradeAwaitingTimeout(remote)).To(BeFalse())
		entry.SetState(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING)
		entry.SetAcceptedTime(nil)
		Expect(hasUpgradeAwaitingTimeout(remote)).To(BeFalse())
	})

	It("propagates a removed NodeSet's live NodePool progress", func() {
		order := newOrder()
		order.Spec.NodeRequests = order.Spec.NodeRequests[:1]
		order.Annotations[v1alpha1.UpgradeAttemptIDsAnnotation] = `{"nodeSets":{"compute":"compute-id"}}`
		pool := newPool("gpu", "gpu-id")
		pool.Status.Conditions = []hypershiftv1beta1.NodePoolCondition{{
			Type:   hypershiftv1beta1.NodePoolUpdatingVersionConditionType,
			Status: corev1.ConditionTrue, ObservedGeneration: pool.Generation,
		}}
		recordUpgradeObservations(order, &hypershiftv1beta1.HostedCluster{}, []hypershiftv1beta1.NodePool{pool})
		Expect(order.Status.NodeRequests[1].UpgradeObservation.State).To(Equal(v1alpha1.UpgradeObservationProgressing))
		Expect(order.Status.NodeRequests[1].UpgradeObservation.Version).To(Equal("4.17.0"))
		component := &privatev1.ClusterUpgradeComponent{}
		component.SetNodeSetId("gpu")
		entry := privatev1.ClusterUpgradeHistoryEntry_builder{
			AttemptId: "gpu-id", Component: component, TargetVersion: "4.18.0",
			State: privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING,
		}.Build()
		remote := privatev1.Cluster_builder{
			Spec: &privatev1.ClusterSpec{},
			Status: privatev1.ClusterStatus_builder{
				UpgradeHistory: []*privatev1.ClusterUpgradeHistoryEntry{entry},
			}.Build(),
		}.Build()
		syncClusterOrderUpgradeObservations(order, remote)
		Expect(entry.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING))
	})

	DescribeTable("retains a removed NodeSet until its NodePool is absent from a successful list", func(initialState, finalState privatev1.ClusterUpgradeState) {
		order := newOrder()
		order.Name = "order"
		order.Spec.NodeRequests = order.Spec.NodeRequests[:1]
		order.Status.ClusterReference = &v1alpha1.ClusterOrderClusterReferenceType{Namespace: "cluster-namespace"}
		pool := newPool("gpu", "gpu-id")
		pool.Name = "nodepool-order-gpu"
		pool.Namespace = "cluster-namespace"
		pool.Labels[osacClusterOrderNameLabel] = order.Name
		pool.Labels[agentInstanceTypeLabel] = "bm.gpu"
		pool.Status.Replicas = 1
		scheme := runtime.NewScheme()
		Expect(hypershiftv1beta1.AddToScheme(scheme)).To(Succeed())
		client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(&pool).Build()
		remote := &privatev1.Cluster{}
		remote.SetSpec(privatev1.ClusterSpec_builder{NodeSets: map[string]*privatev1.ClusterNodeSet{"compute": {}}}.Build())
		entry := &privatev1.ClusterUpgradeHistoryEntry{}
		entry.SetAttemptId("gpu-id")
		component := &privatev1.ClusterUpgradeComponent{}
		component.SetNodeSetId("gpu")
		entry.SetComponent(component)
		entry.SetState(initialState)
		entry.SetMessage("existing message")
		transitionTime := timestamppb.New(time.Now().Add(-time.Minute))
		entry.SetStateTransitionTime(transitionTime)
		status := &privatev1.ClusterStatus{}
		status.SetNodeSets(map[string]*privatev1.ClusterNodeSet{"compute": {}, "gpu": {}})
		status.GetNodeSets()["gpu"].SetObservedVersion("4.16.0")
		status.SetUpgradeHistory([]*privatev1.ClusterUpgradeHistoryEntry{entry})
		remote.SetStatus(status)
		reconciler := &ClusterOrderReconciler{}
		Expect(reconciler.handleNodePools(context.Background(), order,
			&hypershiftv1beta1.NodePoolList{Items: []hypershiftv1beta1.NodePool{pool}})).To(Succeed())
		Expect(order.Status.NodeRequests).To(HaveLen(2))
		Expect(syncRemovedNodeSetUpgrades(context.Background(), client, order, remote)).To(Succeed())
		Expect(remote.GetStatus().GetNodeSets()).To(HaveKey("gpu"))
		Expect(entry.GetState()).To(Equal(initialState))
		order.Status.NodeRequests[1].UpgradeObservation = &v1alpha1.UpgradeObservation{
			AttemptID: "gpu-id", Version: "4.17.0",
		}
		syncClusterOrderUpgradeObservations(order, remote)
		Expect(remote.GetStatus().GetNodeSets()["gpu"].GetObservedVersion()).To(Equal("4.16.0"))
		order.Status.NodeRequests[1].UpgradeObservation.AttemptID = "stale-id"
		order.Status.NodeRequests[1].UpgradeObservation.Version = "4.18.0"
		syncClusterOrderUpgradeObservations(order, remote)
		Expect(remote.GetStatus().GetNodeSets()["gpu"].GetObservedVersion()).To(Equal("4.16.0"))

		Expect(reconciler.handleNodePools(context.Background(), order, &hypershiftv1beta1.NodePoolList{})).To(Succeed())
		Expect(order.Status.NodeRequests).To(HaveLen(1))
		Expect(order.Status.NodeRequests[0].NodeSet).To(Equal("compute"))
		Expect(client.Delete(context.Background(), &pool)).To(Succeed())
		Expect(syncRemovedNodeSetUpgrades(context.Background(), client, order, remote)).To(Succeed())
		Expect(remote.GetStatus().GetNodeSets()).NotTo(HaveKey("gpu"))
		Expect(entry.GetState()).To(Equal(finalState))
		if finalState == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_NODE_SET_REMOVED {
			Expect(entry.GetMessage()).To(ContainSubstring("gpu"))
			Expect(entry.GetStateTransitionTime().AsTime()).To(BeTemporally(">", transitionTime.AsTime()))
		} else {
			Expect(entry.GetMessage()).To(Equal("existing message"))
			Expect(entry.GetStateTransitionTime()).To(Equal(transitionTime))
		}
	},
		Entry("pending attempt", privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING,
			privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_NODE_SET_REMOVED),
		Entry("progressing attempt", privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING,
			privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_NODE_SET_REMOVED),
		Entry("timed-out attempt", privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_TIMED_OUT,
			privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_TIMED_OUT),
	)
})
