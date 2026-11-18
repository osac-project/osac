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
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("ClusterOrder live version feedback", func() {
	const hcName = "selected-hosted-cluster"

	var (
		orderName      string
		orderNamespace string
		orderKey       types.NamespacedName
		mockClient     *mockClustersClient
		primary        *ClusterOrderReconciler
		feedback       *FeedbackReconciler
	)

	newHostedCluster := func(name, owner string) *unstructured.Unstructured {
		hc := &unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "hypershift.openshift.io/v1beta1",
			"kind":       "HostedCluster",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": orderNamespace,
				"labels": map[string]interface{}{
					osacClusterOrderNameLabel: owner,
				},
			},
		}}
		return hc
	}

	setHostedClusterHistory := func(name, field string, history []interface{}) {
		hc := newHostedCluster(name, orderName)
		Expect(k8sClient.Get(context.Background(), types.NamespacedName{
			Name: name, Namespace: orderNamespace,
		}, hc)).To(Succeed())
		Expect(unstructured.SetNestedMap(hc.Object, map[string]interface{}{
			"history": history,
		}, "status", field)).To(Succeed())
		Expect(k8sClient.Status().Update(context.Background(), hc)).To(Succeed())
	}

	newPool := func(name, owner, nodeSet, version string) *hypershiftv1beta1.NodePool {
		pool := &hypershiftv1beta1.NodePool{ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: orderNamespace,
			Labels: map[string]string{
				osacClusterOrderNameLabel: owner,
				agentNodeSetLabel:         nodeSet,
				agentInstanceTypeLabel:    "bm.large",
			},
		}}
		pool.Spec.ClusterName = hcName
		Expect(k8sClient.Create(context.Background(), pool)).To(Succeed())
		pool.Status.Version = version
		pool.Status.Replicas = 1
		Expect(k8sClient.Status().Update(context.Background(), pool)).To(Succeed())
		return pool
	}

	reconcilePrimary := func() {
		_, err := primary.Reconcile(context.Background(), reconcile.Request{NamespacedName: orderKey})
		Expect(err).NotTo(HaveOccurred())
	}

	reconcileFeedback := func() {
		_, err := feedback.Reconcile(context.Background(), reconcile.Request{NamespacedName: orderKey})
		Expect(err).NotTo(HaveOccurred())
	}

	reconcileOnce := func() {
		reconcilePrimary()
		reconcileFeedback()
	}

	BeforeEach(func() {
		namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			GenerateName: "observed-feedback-",
		}}
		Expect(k8sClient.Create(context.Background(), namespace)).To(Succeed())
		orderNamespace = namespace.Name
		orderName = namespace.Name
		orderKey = types.NamespacedName{Name: orderName, Namespace: orderNamespace}
		order := &v1alpha1.ClusterOrder{
			ObjectMeta: metav1.ObjectMeta{
				Name: orderName, Namespace: orderNamespace,
				Labels: map[string]string{osacClusterOrderIDLabel: "cluster-id"},
				Annotations: map[string]string{
					osacManagementStateAnnotation: ManagementStateManual,
				},
			},
			Spec: v1alpha1.ClusterOrderSpec{
				TemplateID: "test_template",
				NodeRequests: []v1alpha1.NodeRequest{{
					NodeSet: "workers", NumberOfNodes: 1,
					BareMetal: &v1alpha1.BareMetalNodeSpec{InstanceType: "bm.large"},
				}},
			},
		}
		Expect(k8sClient.Create(context.Background(), order)).To(Succeed())
		namespace.Labels = commonLabelsFromOrder(order)
		Expect(k8sClient.Update(context.Background(), namespace)).To(Succeed())
		order.Status.Phase = v1alpha1.ClusterOrderPhaseProgressing
		order.Status.ClusterReference = &v1alpha1.ClusterOrderClusterReferenceType{
			Namespace: orderNamespace, HostedClusterName: hcName,
		}
		order.Status.NodeRequests = []v1alpha1.NodeRequestStatus{{
			NodeSet: "workers", NumberOfNodes: 1,
			BareMetal: &v1alpha1.BareMetalNodeSpec{InstanceType: "bm.large"},
		}}
		Expect(k8sClient.Status().Update(context.Background(), order)).To(Succeed())

		status := &privatev1.ClusterStatus{}
		status.SetObservedVersion("4.22.0")
		workers := &privatev1.ClusterNodeSetStatus{}
		workers.SetObservedVersion("4.22.0")
		status.SetNodeSets(map[string]*privatev1.ClusterNodeSetStatus{"workers": workers})
		spec := &privatev1.ClusterSpec{}
		spec.SetNodeSets(map[string]*privatev1.ClusterNodeSet{
			"workers": privatev1.ClusterNodeSet_builder{
				BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{
					Name: "bm.large",
				}.Build(),
			}.Build(),
		})
		mockClient = &mockClustersClient{
			getResponse: &privatev1.ClustersGetResponse{Object: &privatev1.Cluster{
				Id: "cluster-id", Spec: spec, Status: status,
			}},
			updateResponse: &privatev1.ClustersUpdateResponse{},
		}
		primary = &ClusterOrderReconciler{
			Client: k8sClient, apiReader: k8sClient, Scheme: k8sClient.Scheme(),
			ClusterOrderNamespace: orderNamespace,
		}
		feedback = &FeedbackReconciler{
			bridge: newClusterOrderFeedbackBridge(k8sClient, mockClient, FeedbackReconcilerConfig{
				ClusterOrderNamespace: orderNamespace,
			}),
			clusterOrderNamespace: orderNamespace,
		}
	})

	AfterEach(func() {
		order := &v1alpha1.ClusterOrder{}
		if err := k8sClient.Get(context.Background(), orderKey, order); err == nil {
			order.Finalizers = nil
			Expect(k8sClient.Update(context.Background(), order)).To(Succeed())
			Expect(k8sClient.Delete(context.Background(), order)).To(Succeed())
		}
		pools := &hypershiftv1beta1.NodePoolList{}
		Expect(k8sClient.List(context.Background(), pools, client.InNamespace(orderNamespace))).To(Succeed())
		for i := range pools.Items {
			Expect(k8sClient.Delete(context.Background(), &pools.Items[i])).To(Succeed())
		}
		hcs := &unstructured.UnstructuredList{}
		hcs.SetGroupVersionKind(schema.GroupVersionKind{
			Group: "hypershift.openshift.io", Version: "v1beta1", Kind: "HostedClusterList",
		})
		Expect(k8sClient.List(context.Background(), hcs, client.InNamespace(orderNamespace))).To(Succeed())
		for i := range hcs.Items {
			Expect(k8sClient.Delete(context.Background(), &hcs.Items[i])).To(Succeed())
		}
		Expect(k8sClient.Delete(context.Background(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: orderNamespace},
		})).To(Succeed())
	})

	It("updates the selected live versions without an upgrade attempt", func() {
		Expect(k8sClient.Create(context.Background(), newHostedCluster(hcName, orderName))).To(Succeed())
		setHostedClusterHistory(hcName, "version", []interface{}{
			map[string]interface{}{"version": "4.22.2", "state": "Partial"},
			map[string]interface{}{"version": "4.22.1", "state": "Completed"},
			map[string]interface{}{"version": "4.22.0", "state": "Completed"},
		})
		setHostedClusterHistory(hcName, "controlPlaneVersion", []interface{}{
			map[string]interface{}{"version": "4.22.9", "state": "Completed"},
		})
		newPool("workers-pool", orderName, "workers", "4.22.1")

		reconcileOnce()
		storedOrder := &v1alpha1.ClusterOrder{}
		Expect(k8sClient.Get(context.Background(), orderKey, storedOrder)).To(Succeed())
		Expect(storedOrder.Status.ObservedVersion).To(Equal("4.22.1"))
		Expect(storedOrder.Status.NodeRequests[0].ObservedVersion).To(Equal("4.22.1"))
		remote := mockClient.lastUpdate
		Expect(remote.GetStatus().GetObservedVersion()).To(Equal("4.22.1"))
		Expect(remote.GetStatus().GetNodeSets()["workers"].GetObservedVersion()).To(Equal("4.22.1"))
		Expect(remote.GetStatus().GetUpgradeHistory()).To(BeEmpty())
		Expect(remote.GetStatus().GetUpgradeAttemptId()).To(BeEmpty())
		Expect(mockClient.lastUpdateMask.GetPaths()).To(ContainElements("status.observed_version", "status.node_sets"))

		updates := mockClient.updateCount
		reconcileOnce()
		Expect(mockClient.updateCount).To(Equal(updates))
	})

	It("feeds persisted ClusterOrder versions after the live resource disappears", func() {
		hc := newHostedCluster(hcName, orderName)
		Expect(k8sClient.Create(context.Background(), hc)).To(Succeed())
		setHostedClusterHistory(hcName, "version", []interface{}{
			map[string]interface{}{"version": "4.22.1", "state": "Completed"},
		})
		newPool("workers-pool", orderName, "workers", "4.22.1")
		reconcilePrimary()
		Expect(k8sClient.Delete(context.Background(), hc)).To(Succeed())

		reconcileFeedback()
		Expect(mockClient.getResponse.GetObject().GetStatus().GetObservedVersion()).To(Equal("4.22.1"))
		Expect(mockClient.getResponse.GetObject().GetStatus().GetNodeSets()["workers"].GetObservedVersion()).To(Equal("4.22.1"))
	})

	It("preserves baselines when live signals are absent or ambiguous", func() {
		reconcileOnce()
		Expect(mockClient.getResponse.GetObject().GetStatus().GetObservedVersion()).To(Equal("4.22.0"))
		Expect(mockClient.getResponse.GetObject().GetStatus().GetNodeSets()["workers"].GetObservedVersion()).To(Equal("4.22.0"))

		Expect(k8sClient.Create(context.Background(), newHostedCluster(hcName, orderName))).To(Succeed())
		setHostedClusterHistory(hcName, "version", []interface{}{
			map[string]interface{}{"version": "4.22.1", "state": "Partial"},
		})
		newPool("workers-empty", orderName, "workers", "")
		newPool("sibling", "another-order", "workers", "4.22.3")
		newPool("removed", orderName, "removed", "4.22.3")
		remote := mockClient.getResponse.GetObject()
		removed := &privatev1.ClusterNodeSetStatus{}
		removed.SetObservedVersion("4.21.0")
		remote.GetStatus().GetNodeSets()["removed"] = removed

		reconcileOnce()
		Expect(remote.GetStatus().GetObservedVersion()).To(Equal("4.22.0"))
		Expect(remote.GetStatus().GetNodeSets()["workers"].GetObservedVersion()).To(Equal("4.22.0"))
		Expect(remote.GetStatus().GetNodeSets()["removed"].GetObservedVersion()).To(Equal("4.21.0"))
		setHostedClusterHistory(hcName, "version", []interface{}{
			map[string]interface{}{"version": "4.22.1", "state": "Partial"},
			map[string]interface{}{"version": "", "state": "Completed"},
			map[string]interface{}{"version": "4.21.9", "state": "Completed"},
		})
		reconcileOnce()
		Expect(remote.GetStatus().GetObservedVersion()).To(Equal("4.22.0"))

		newPool("workers-duplicate", orderName, "workers", "4.22.3")
		reconcileOnce()
		Expect(remote.GetStatus().GetNodeSets()["workers"].GetObservedVersion()).To(Equal("4.22.0"))
	})

	It("refreshes live versions without advancing an active attempt", func() {
		Expect(k8sClient.Create(context.Background(), newHostedCluster(hcName, orderName))).To(Succeed())
		setHostedClusterHistory(hcName, "version", []interface{}{
			map[string]interface{}{"version": "4.22.1", "state": "Completed"},
		})
		newPool("workers-pool", orderName, "workers", "4.22.1")
		remote := mockClient.getResponse.GetObject()
		remote.GetStatus().SetUpgradeAttemptId("attempt-id")
		component := &privatev1.ClusterUpgradeComponent{}
		component.SetControlPlane(&emptypb.Empty{})
		entry := privatev1.ClusterUpgradeHistoryEntry_builder{
			AttemptId: "attempt-id", Component: component, TargetVersion: "4.22.2",
			State:        privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING,
			AcceptedTime: timestamppb.New(time.Now()),
		}.Build()
		remote.GetStatus().SetUpgradeHistory([]*privatev1.ClusterUpgradeHistoryEntry{entry})

		reconcileOnce()
		Expect(remote.GetStatus().GetObservedVersion()).To(Equal("4.22.1"))
		Expect(remote.GetStatus().GetNodeSets()["workers"].GetObservedVersion()).To(Equal("4.22.1"))
		Expect(entry.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING))
		Expect(entry.GetTargetVersion()).To(Equal("4.22.2"))
	})

	It("ignores a HostedCluster with the selected name but another order label", func() {
		Expect(k8sClient.Create(context.Background(), newHostedCluster(hcName, "another-order"))).To(Succeed())
		setHostedClusterHistory(hcName, "version", []interface{}{
			map[string]interface{}{"version": "4.22.3", "state": "Completed"},
		})
		newPool("sibling-cluster-pool", orderName, "workers", "4.22.3")
		reconcileOnce()
		Expect(mockClient.getResponse.GetObject().GetStatus().GetObservedVersion()).To(Equal("4.22.0"))
		Expect(mockClient.getResponse.GetObject().GetStatus().GetNodeSets()["workers"].GetObservedVersion()).To(Equal("4.22.0"))
	})

	It("waits for positive replicas before updating a node-set version", func() {
		order := &v1alpha1.ClusterOrder{}
		Expect(k8sClient.Get(context.Background(), orderKey, order)).To(Succeed())
		order.Status.NodeRequests = nil
		Expect(k8sClient.Status().Update(context.Background(), order)).To(Succeed())
		Expect(k8sClient.Create(context.Background(), newHostedCluster(hcName, orderName))).To(Succeed())
		setHostedClusterHistory(hcName, "version", []interface{}{
			map[string]interface{}{"version": "4.22.1", "state": "Completed"},
		})
		pool := newPool("workers-pool", orderName, "workers", "4.22.1")
		pool.Status.Replicas = 0
		Expect(k8sClient.Status().Update(context.Background(), pool)).To(Succeed())
		reconcileOnce()
		Expect(mockClient.getResponse.GetObject().GetStatus().GetNodeSets()["workers"].GetObservedVersion()).To(Equal("4.22.0"))

		Expect(k8sClient.Get(context.Background(), orderKey, order)).To(Succeed())
		order.Status.NodeRequests = []v1alpha1.NodeRequestStatus{{
			NodeSet: "workers", NumberOfNodes: 1,
			BareMetal: &v1alpha1.BareMetalNodeSpec{InstanceType: "bm.large"},
		}}
		Expect(k8sClient.Status().Update(context.Background(), order)).To(Succeed())
		pool.Status.Replicas = 1
		Expect(k8sClient.Status().Update(context.Background(), pool)).To(Succeed())
		reconcileOnce()
		Expect(mockClient.getResponse.GetObject().GetStatus().GetNodeSets()["workers"].GetObservedVersion()).To(Equal("4.22.1"))

		pool.Status.Replicas = 0
		pool.Status.Version = "4.22.2"
		Expect(k8sClient.Status().Update(context.Background(), pool)).To(Succeed())
		reconcileOnce()
		Expect(mockClient.getResponse.GetObject().GetStatus().GetNodeSets()["workers"].GetObservedVersion()).To(Equal("4.22.1"))
	})

	It("maps selected HyperShift resource events to the primary order reconcile", func() {
		pool := &hypershiftv1beta1.NodePool{ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{osacClusterOrderNameLabel: orderName},
		}}
		primary.ClusterOrderNamespace = orderNamespace
		Expect(primary.mapObjectToCluster(context.Background(), pool)).To(Equal(
			[]reconcile.Request{{NamespacedName: orderKey}},
		))
		pool.Labels[osacClusterOrderNameLabel] = "another-order"
		Expect(primary.mapObjectToCluster(context.Background(), pool)).To(BeEmpty())
	})
})
