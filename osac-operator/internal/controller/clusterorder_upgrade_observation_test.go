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

	It("observes the control plane and all stamped node sets independently", func() {
		order := newOrder()
		hc := &hypershiftv1beta1.HostedCluster{ObjectMeta: metav1.ObjectMeta{
			Generation: 3, Annotations: map[string]string{v1alpha1.UpgradeAttemptIDAnnotation: "cp-id"},
		}}
		hc.Spec.Release.Image = image
		hc.Status.Version = &hypershiftv1beta1.ClusterVersionStatus{ObservedGeneration: 3}
		hc.Status.Version.Desired.Image = image
		hc.Status.Version.History = []configv1.UpdateHistory{{
			Image: image, Version: "4.18.0", State: configv1.CompletedUpdate,
		}}
		recordUpgradeObservations(order, hc, []hypershiftv1beta1.NodePool{
			newPool("compute", "compute-id"), newPool("gpu", "gpu-id"),
		})
		Expect(order.Status.UpgradeObservation.AttemptID).To(Equal("cp-id"))
		Expect(order.Status.UpgradeObservation.Version).To(Equal("4.18.0"))
		Expect(order.Status.NodeRequests[0].UpgradeObservation.AttemptID).To(Equal("compute-id"))
		Expect(order.Status.NodeRequests[1].UpgradeObservation.AttemptID).To(Equal("gpu-id"))
	})

	It("keeps the control-plane observed version unchanged while HyperShift reports a partial rollout", func() {
		order := newOrder()
		hc := &hypershiftv1beta1.HostedCluster{ObjectMeta: metav1.ObjectMeta{
			Generation: 3, Annotations: map[string]string{v1alpha1.UpgradeAttemptIDAnnotation: "cp-id"},
		}}
		hc.Spec.Release.Image = image
		hc.Status.Version = &hypershiftv1beta1.ClusterVersionStatus{ObservedGeneration: 3}
		hc.Status.Version.Desired.Image = image
		hc.Status.Version.History = []configv1.UpdateHistory{{
			Image: image, Version: "4.18.0", State: configv1.PartialUpdate,
		}}
		recordUpgradeObservations(order, hc, nil)
		Expect(order.Status.UpgradeObservation.AttemptID).To(Equal("cp-id"))
		Expect(order.Status.UpgradeObservation.Version).To(BeEmpty())
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

	It("updates only observed versions with a matching component history entry", func() {
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
		status.SetObservedCpVersion("4.17.0")
		status.SetNodeSets(map[string]*privatev1.ClusterNodeSet{
			"compute": {}, "gpu": {},
		})
		status.GetNodeSets()["gpu"].SetObservedVersion("4.16.0")
		status.SetUpgradeHistory([]*privatev1.ClusterUpgradeHistoryEntry{compute, cp})
		remote.SetStatus(status)
		syncClusterOrderUpgradeObservations(order, remote)
		Expect(remote.GetStatus().GetObservedCpVersion()).To(Equal("4.18.0"))
		Expect(remote.GetStatus().GetNodeSets()["compute"].GetObservedVersion()).To(Equal("4.17.0"))
		Expect(remote.GetStatus().GetNodeSets()["gpu"].GetObservedVersion()).To(Equal("4.16.0"))
	})

	It("observes a removed NodeSet while its stamped NodePool still exists", func() {
		order := newOrder()
		order.Spec.NodeRequests = order.Spec.NodeRequests[:1]
		order.Annotations[v1alpha1.UpgradeAttemptIDsAnnotation] = `{"nodeSets":{"compute":"compute-id"}}`
		pool := newPool("gpu", "gpu-id")
		recordUpgradeObservations(order, &hypershiftv1beta1.HostedCluster{}, []hypershiftv1beta1.NodePool{pool})
		Expect(order.Status.NodeRequests[1].UpgradeObservation).To(Equal(
			&v1alpha1.UpgradeObservation{AttemptID: "gpu-id", Version: "4.17.0"}))
	})

	It("retains a removed NodeSet until its NodePool is absent from a successful list", func() {
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
		entry.SetState(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING)
		entry.SetLastStateTime(timestamppb.New(time.Now().Add(-time.Minute)))
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
		Expect(entry.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING))
		order.Status.NodeRequests[1].UpgradeObservation = &v1alpha1.UpgradeObservation{
			AttemptID: "gpu-id", Version: "4.17.0",
		}
		syncClusterOrderUpgradeObservations(order, remote)
		Expect(remote.GetStatus().GetNodeSets()["gpu"].GetObservedVersion()).To(Equal("4.17.0"))
		order.Status.NodeRequests[1].UpgradeObservation.AttemptID = "stale-id"
		order.Status.NodeRequests[1].UpgradeObservation.Version = "4.18.0"
		syncClusterOrderUpgradeObservations(order, remote)
		Expect(remote.GetStatus().GetNodeSets()["gpu"].GetObservedVersion()).To(Equal("4.17.0"))

		Expect(client.Delete(context.Background(), &pool)).To(Succeed())
		Expect(reconciler.handleNodePools(context.Background(), order, &hypershiftv1beta1.NodePoolList{})).To(Succeed())
		Expect(order.Status.NodeRequests).To(HaveLen(1))
		Expect(order.Status.NodeRequests[0].NodeSet).To(Equal("compute"))
		Expect(syncRemovedNodeSetUpgrades(context.Background(), client, order, remote)).To(Succeed())
		Expect(remote.GetStatus().GetNodeSets()).NotTo(HaveKey("gpu"))
		Expect(entry.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_NODE_SET_REMOVED))
		Expect(entry.GetMessage()).To(ContainSubstring("gpu"))
		Expect(entry.GetLastStateTime().AsTime()).To(BeTemporally(">", time.Now().Add(-time.Minute)))
	})
})
