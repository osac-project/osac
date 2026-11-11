/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Cluster upgrade", func() {
	var (
		ctx                  context.Context
		bmitName             string
		clustersClient       publicv1.ClustersClient
		privateClusters      privatev1.ClustersClient
		clusterVersions      privatev1.ClusterVersionsClient
		templatesClient      privatev1.ClusterTemplatesClient
		templateID           string
		targetVersionName    string
		targetSemver         string
		targetImage          string
		defaultImage         string
		defaultVersionName   string
		defaultVersionSemver string
	)

	BeforeEach(func() {
		ctx = context.Background()
		ensureTenantDefaultNetworkingFixture(ctx, usersGroup, "")
		clustersClient = publicv1.NewClustersClient(tool.ExternalView().UserConn())
		privateClusters = privatev1.NewClustersClient(tool.InternalView().AdminConn())
		clusterVersions = privatev1.NewClusterVersionsClient(tool.InternalView().AdminConn())
		templatesClient = privatev1.NewClusterTemplatesClient(tool.InternalView().AdminConn())

		bmitName = fmt.Sprintf("test-bmit-%s", uuid.New()[24:32])
		instanceTypes := privatev1.NewBareMetalInstanceTypesClient(tool.InternalView().AdminConn())
		_, err := instanceTypes.Create(ctx, privatev1.BareMetalInstanceTypesCreateRequest_builder{
			Object: privatev1.BareMetalInstanceType_builder{
				Metadata: privatev1.Metadata_builder{Name: bmitName}.Build(),
				Spec: privatev1.BareMetalInstanceTypeSpec_builder{
					HostLabelSelector: privatev1.BareMetalLabelSelector_builder{
						MatchLabels: map[string]string{"hardware.profile": "compute"},
					}.Build(),
					Hardware: privatev1.BareMetalHardwareSpec_builder{
						Cpu:    privatev1.BareMetalCPUSpec_builder{Cores: 32, Architecture: "amd64", ThreadsPerCore: 2}.Build(),
						Memory: privatev1.BareMetalMemorySpec_builder{TotalGb: 128}.Build(),
						NetworkPorts: []*privatev1.BareMetalNetworkPortSpec{
							privatev1.BareMetalNetworkPortSpec_builder{
								Name: "eth0", Role: "fabric", Type: "Ethernet", Speed: "10Gbps",
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).NotTo(HaveOccurred())

		templateID = newTemplateID("upgrade_template")
		_, err = templatesClient.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
			Object: privatev1.ClusterTemplate_builder{
				Id:          templateID,
				Title:       "Upgrade template",
				Description: "Upgrade template.",
				Metadata:    privatev1.Metadata_builder{Name: fmt.Sprintf("upgrade-tmpl-%s", uuid.New()[24:32])}.Build(),
			}.Build(),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_, err := templatesClient.Delete(ctx, privatev1.ClusterTemplatesDeleteRequest_builder{Id: templateID}.Build())
			Expect(err).NotTo(HaveOccurred())
		})

		defaults, err := clusterVersions.List(ctx, privatev1.ClusterVersionsListRequest_builder{
			Filter: new(`this.spec.is_default == true`),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(defaults.GetItems()).To(HaveLen(1))
		defaultVersion := defaults.GetItems()[0]
		defaultVersionName = defaultVersion.GetMetadata().GetName()
		defaultVersionSemver = defaultVersion.GetSpec().GetVersion()
		defaultImage = defaultVersion.GetSpec().GetImage()

		targetSemver = nextCVVersion()
		Expect(targetSemver).NotTo(Equal(defaultVersionSemver))
		targetVersionName = fmt.Sprintf("upgrade-cv-%s", uuid.New()[24:32])
		targetImage = fmt.Sprintf("quay.io/openshift-release-dev/ocp-release:%s-multi", targetSemver)
		diskImage := createCatalogItemDiskImageFixture(ctx, "shared", catalogItemFixtureName())
		targetVersion, err := clusterVersions.Create(ctx, privatev1.ClusterVersionsCreateRequest_builder{
			Object: privatev1.ClusterVersion_builder{
				Metadata: privatev1.Metadata_builder{Name: targetVersionName}.Build(),
				Spec: privatev1.ClusterVersionSpec_builder{
					Version:   targetSemver,
					Image:     targetImage,
					DiskImage: privatev1.DiskImageReference_builder{Id: diskImage.GetId()}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_, err := clusterVersions.Delete(ctx, privatev1.ClusterVersionsDeleteRequest_builder{
				Id: targetVersion.GetObject().GetId(),
			}.Build())
			Expect(err).NotTo(HaveOccurred())
		})
	})

	createCluster := func(nodeSets map[string]*publicv1.ClusterNodeSet) *publicv1.Cluster {
		created, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("upgrade-cluster-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: templateID}.Build(),
					NodeSets: nodeSets,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		cluster := created.GetObject()
		DeferCleanup(func() {
			_, err := clustersClient.Delete(ctx, publicv1.ClustersDeleteRequest_builder{Id: cluster.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
		})
		return cluster
	}

	twoNodeSets := func() map[string]*publicv1.ClusterNodeSet {
		return map[string]*publicv1.ClusterNodeSet{
			"compute": publicv1.ClusterNodeSet_builder{
				Size: proto.Int32(1),
				BaremetalInstanceType: publicv1.BareMetalInstanceTypeReference_builder{
					Id: bmitName, Shared: true,
				}.Build(),
			}.Build(),
			"gpu": publicv1.ClusterNodeSet_builder{
				Size: proto.Int32(1),
				BaremetalInstanceType: publicv1.BareMetalInstanceTypeReference_builder{
					Id: bmitName, Shared: true,
				}.Build(),
			}.Build(),
		}
	}

	waitForClusterOrder := func(cluster *publicv1.Cluster) *osacv1alpha1.ClusterOrder {
		kubeClient := tool.KubeClient()
		orders := &osacv1alpha1.ClusterOrderList{}
		var order *osacv1alpha1.ClusterOrder
		Eventually(func(g Gomega) {
			err := kubeClient.List(ctx, orders, crclient.MatchingLabels{labels.ClusterOrderUuid: cluster.GetId()})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(orders.Items).To(HaveLen(1))
			order = &orders.Items[0]
		}, time.Minute, time.Second).Should(Succeed())
		return order
	}

	seedObservedVersions := func(cluster *publicv1.Cluster) {
		current, err := privateClusters.Get(ctx, privatev1.ClustersGetRequest_builder{Id: cluster.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
		object := current.GetObject()
		object.GetStatus().SetObservedVersion(defaultVersionSemver)
		for _, status := range object.GetStatus().GetNodeSets() {
			status.SetObservedVersion(defaultVersionSemver)
		}
		_, err = privateClusters.Update(ctx, privatev1.ClustersUpdateRequest_builder{
			Object: object,
			UpdateMask: &fieldmaskpb.FieldMask{
				Paths: []string{"status.observed_version", "status.node_sets"},
			},
		}.Build())
		Expect(err).NotTo(HaveOccurred())
	}

	updateTargetVersion := func(cluster *publicv1.Cluster) {
		_, err := clustersClient.Update(ctx, publicv1.ClustersUpdateRequest_builder{
			Object: publicv1.Cluster_builder{
				Id: cluster.GetId(),
				Spec: publicv1.ClusterSpec_builder{
					Version: publicv1.ClusterVersionReference_builder{Name: targetVersionName}.Build(),
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.version"}},
		}.Build())
		Expect(err).NotTo(HaveOccurred())
	}

	nodeRequestImage := func(order *osacv1alpha1.ClusterOrder, nodeSet string) string {
		for _, request := range order.Spec.NodeRequests {
			if request.NodeSet == nodeSet {
				return request.ReleaseImage
			}
		}
		return ""
	}

	waitForOrderReleaseImage := func(order *osacv1alpha1.ClusterOrder, image string) {
		kubeClient := tool.KubeClient()
		Eventually(func(g Gomega) {
			updated := &osacv1alpha1.ClusterOrder{}
			g.Expect(kubeClient.Get(ctx, crclient.ObjectKeyFromObject(order), updated)).To(Succeed())
			g.Expect(updated.Spec.ReleaseImage).To(Equal(image))
		}, time.Minute, time.Second).Should(Succeed())
	}

	waitForNodeRequestImage := func(order *osacv1alpha1.ClusterOrder, nodeSet, image string) {
		kubeClient := tool.KubeClient()
		Eventually(func(g Gomega) {
			updated := &osacv1alpha1.ClusterOrder{}
			g.Expect(kubeClient.Get(ctx, crclient.ObjectKeyFromObject(order), updated)).To(Succeed())
			g.Expect(nodeRequestImage(updated, nodeSet)).To(Equal(image), nodeSet)
		}, time.Minute, time.Second).Should(Succeed())
	}

	waitForNodeRequestNodeSets := func(order *osacv1alpha1.ClusterOrder, expected ...string) {
		kubeClient := tool.KubeClient()
		Eventually(func(g Gomega) {
			updated := &osacv1alpha1.ClusterOrder{}
			g.Expect(kubeClient.Get(ctx, crclient.ObjectKeyFromObject(order), updated)).To(Succeed())
			nodeSets := make([]string, 0, len(updated.Spec.NodeRequests))
			for _, request := range updated.Spec.NodeRequests {
				nodeSets = append(nodeSets, request.NodeSet)
			}
			g.Expect(nodeSets).To(ConsistOf(expected))
		}, time.Minute, time.Second).Should(Succeed())
	}

	markControlPlaneSucceeded := func(cluster *publicv1.Cluster) {
		privateCluster, err := privateClusters.Get(ctx, privatev1.ClustersGetRequest_builder{Id: cluster.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
		stored := privateCluster.GetObject()
		var controlPlane *privatev1.ClusterUpgradeHistoryEntry
		for _, entry := range stored.GetStatus().GetUpgradeHistory() {
			if entry.GetComponent().GetControlPlane() != nil {
				controlPlane = entry
				break
			}
		}
		Expect(controlPlane).NotTo(BeNil())
		controlPlane.SetState(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED)
		controlPlane.SetStateTransitionTime(timestamppb.New(time.Now()))
		_, err = privateClusters.Update(ctx, privatev1.ClustersUpdateRequest_builder{
			Object:     stored,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.upgrade_history"}},
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		_, err = privateClusters.Signal(ctx, privatev1.ClustersSignalRequest_builder{Id: cluster.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
	}

	It("projects node-set images only after control-plane success", func() {
		cluster := createCluster(testClusterNodeSets(bmitName, 1))
		order := waitForClusterOrder(cluster)
		Expect(order.Spec.NodeRequests[0].ReleaseImage).To(Equal(defaultImage))
		seedObservedVersions(cluster)
		updateTargetVersion(cluster)

		waitForOrderReleaseImage(order, targetImage)
		waitForNodeRequestImage(order, "my-node-set", defaultImage)

		privateCluster, err := privateClusters.Get(ctx, privatev1.ClustersGetRequest_builder{Id: cluster.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
		stored := privateCluster.GetObject()
		Expect(stored.GetStatus().GetUpgradeAttemptId()).NotTo(BeEmpty())
		Expect(stored.GetStatus().GetUpgradeHistory()).To(HaveLen(1))
		controlPlane := stored.GetStatus().GetUpgradeHistory()[0]
		Expect(controlPlane.GetComponent().GetControlPlane()).NotTo(BeNil())
		Expect(controlPlane.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING))
		Expect(stored.GetStatus().GetNodeSets()["my-node-set"].GetVersion().GetName()).To(Equal(defaultVersionName))

		controlPlane.SetState(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED)
		controlPlane.SetStateTransitionTime(timestamppb.New(time.Now()))
		_, err = privateClusters.Update(ctx, privatev1.ClustersUpdateRequest_builder{
			Object:     stored,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.upgrade_history"}},
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		_, err = privateClusters.Signal(ctx, privatev1.ClustersSignalRequest_builder{Id: cluster.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())

		Eventually(func(g Gomega) {
			reconciled, err := privateClusters.Get(ctx, privatev1.ClustersGetRequest_builder{Id: cluster.GetId()}.Build())
			g.Expect(err).NotTo(HaveOccurred())
			object := reconciled.GetObject()
			g.Expect(object.GetStatus().GetNodeSets()["my-node-set"].GetVersion().GetName()).To(Equal(targetVersionName))
			g.Expect(object.GetStatus().GetUpgradeHistory()).To(HaveLen(2))
			g.Expect(object.GetStatus().GetUpgradeHistory()[0].GetComponent().GetNodeSetId()).To(Equal("my-node-set"))
			g.Expect(object.GetStatus().GetUpgradeHistory()[0].GetAttemptId()).To(Equal(object.GetStatus().GetUpgradeAttemptId()))

		}, time.Minute, time.Second).Should(Succeed())

		waitForNodeRequestImage(order, "my-node-set", targetImage)
	})

	It("adds a node set at the control-plane version during an upgrade", func() {
		cluster := createCluster(twoNodeSets())
		order := waitForClusterOrder(cluster)
		seedObservedVersions(cluster)
		updateTargetVersion(cluster)

		waitForOrderReleaseImage(order, targetImage)
		waitForNodeRequestImage(order, "compute", defaultImage)
		waitForNodeRequestImage(order, "gpu", defaultImage)

		current, err := clustersClient.Get(ctx, publicv1.ClustersGetRequest_builder{Id: cluster.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
		nodeSets := current.GetObject().GetSpec().GetNodeSets()
		nodeSets["storage"] = publicv1.ClusterNodeSet_builder{
			Size: proto.Int32(1),
			BaremetalInstanceType: publicv1.BareMetalInstanceTypeReference_builder{
				Id: bmitName, Shared: true,
			}.Build(),
		}.Build()
		_, err = clustersClient.Update(ctx, publicv1.ClustersUpdateRequest_builder{
			Object: publicv1.Cluster_builder{
				Id:   cluster.GetId(),
				Spec: publicv1.ClusterSpec_builder{NodeSets: nodeSets}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.node_sets"}},
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		_, err = privateClusters.Signal(ctx, privatev1.ClustersSignalRequest_builder{Id: cluster.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())

		waitForNodeRequestImage(order, "compute", defaultImage)
		waitForNodeRequestImage(order, "gpu", defaultImage)
		waitForNodeRequestImage(order, "storage", targetImage)

		added, err := privateClusters.Get(ctx, privatev1.ClustersGetRequest_builder{Id: cluster.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
		addedObject := added.GetObject()
		Expect(addedObject.GetStatus().GetNodeSets()["storage"].GetVersion().GetName()).To(Equal(targetVersionName))
		Expect(addedObject.GetStatus().GetNodeSets()["storage"].GetObservedVersion()).To(BeEmpty())
		Expect(addedObject.GetStatus().GetNodeSets()["compute"].GetVersion().GetName()).To(Equal(defaultVersionName))
		Expect(addedObject.GetStatus().GetNodeSets()["gpu"].GetVersion().GetName()).To(Equal(defaultVersionName))

		markControlPlaneSucceeded(cluster)
		Eventually(func(g Gomega) {
			reconciled, err := privateClusters.Get(ctx, privatev1.ClustersGetRequest_builder{Id: cluster.GetId()}.Build())
			g.Expect(err).NotTo(HaveOccurred())
			object := reconciled.GetObject()
			for _, name := range []string{"compute", "gpu", "storage"} {
				g.Expect(object.GetStatus().GetNodeSets()[name].GetVersion().GetName()).To(Equal(targetVersionName), name)
			}
			history := object.GetStatus().GetUpgradeHistory()
			g.Expect(history).To(HaveLen(4))
			g.Expect(history[0].GetComponent().GetNodeSetId()).To(Equal("compute"))
			g.Expect(history[1].GetComponent().GetNodeSetId()).To(Equal("gpu"))
			g.Expect(history[2].GetComponent().GetNodeSetId()).To(Equal("storage"))
			g.Expect(history[2].GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED))
			g.Expect(history[3].GetComponent().GetControlPlane()).NotTo(BeNil())

		}, time.Minute, time.Second).Should(Succeed())

		waitForNodeRequestImage(order, "compute", targetImage)
		waitForNodeRequestImage(order, "gpu", targetImage)
		waitForNodeRequestImage(order, "storage", targetImage)
	})

	It("removes a node set during an upgrade without affecting the remaining node set", func() {
		cluster := createCluster(twoNodeSets())
		order := waitForClusterOrder(cluster)
		seedObservedVersions(cluster)
		updateTargetVersion(cluster)

		waitForOrderReleaseImage(order, targetImage)
		waitForNodeRequestImage(order, "compute", defaultImage)
		waitForNodeRequestImage(order, "gpu", defaultImage)

		current, err := clustersClient.Get(ctx, publicv1.ClustersGetRequest_builder{Id: cluster.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
		nodeSets := current.GetObject().GetSpec().GetNodeSets()
		delete(nodeSets, "gpu")
		_, err = clustersClient.Update(ctx, publicv1.ClustersUpdateRequest_builder{
			Object: publicv1.Cluster_builder{
				Id:   cluster.GetId(),
				Spec: publicv1.ClusterSpec_builder{NodeSets: nodeSets}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.node_sets"}},
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		_, err = privateClusters.Signal(ctx, privatev1.ClustersSignalRequest_builder{Id: cluster.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())

		waitForNodeRequestNodeSets(order, "compute")
		waitForNodeRequestImage(order, "compute", defaultImage)

		markControlPlaneSucceeded(cluster)
		Eventually(func(g Gomega) {
			reconciled, err := privateClusters.Get(ctx, privatev1.ClustersGetRequest_builder{Id: cluster.GetId()}.Build())
			g.Expect(err).NotTo(HaveOccurred())
			object := reconciled.GetObject()
			g.Expect(object.GetStatus().GetNodeSets()["compute"].GetVersion().GetName()).To(Equal(targetVersionName))
			history := object.GetStatus().GetUpgradeHistory()
			g.Expect(history).To(HaveLen(2))
			g.Expect(history[0].GetComponent().GetNodeSetId()).To(Equal("compute"))
			g.Expect(history[0].GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING))
			g.Expect(history[1].GetComponent().GetControlPlane()).NotTo(BeNil())

		}, time.Minute, time.Second).Should(Succeed())

		waitForNodeRequestNodeSets(order, "compute")
		waitForNodeRequestImage(order, "compute", targetImage)
	})
})
