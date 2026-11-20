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

	ginkgo "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	kubeerrors "k8s.io/apimachinery/pkg/api/errors"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Cluster upgrade", func() {
	const pollRequestTimeout = 5 * time.Second

	var (
		ctx                  context.Context
		bmitName             string
		bmitID               string
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
		bmitResponse, err := instanceTypes.Create(ctx, privatev1.BareMetalInstanceTypesCreateRequest_builder{
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
		bmitID = bmitResponse.GetObject().GetId()
		deferCatalogItemFixtureDeletion(func(ctx context.Context) error {
			_, err := instanceTypes.Delete(ctx, privatev1.BareMetalInstanceTypesDeleteRequest_builder{
				Id: bmitID,
			}.Build())
			return err
		}, nil)

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
		deferClusterDeletion(cluster.GetId(), tool.ExternalView().UserConn())
		return cluster
	}

	twoNodeSets := func() map[string]*publicv1.ClusterNodeSet {
		return map[string]*publicv1.ClusterNodeSet{
			"compute": publicv1.ClusterNodeSet_builder{
				Size: proto.Int32(1),
				BaremetalInstanceType: publicv1.BareMetalInstanceTypeReference_builder{
					Id: bmitID, Shared: true,
				}.Build(),
			}.Build(),
			"gpu": publicv1.ClusterNodeSet_builder{
				Size: proto.Int32(1),
				BaremetalInstanceType: publicv1.BareMetalInstanceTypeReference_builder{
					Id: bmitID, Shared: true,
				}.Build(),
			}.Build(),
		}
	}

	waitForClusterOrder := func(cluster *publicv1.Cluster) *osacv1alpha1.ClusterOrder {
		kubeClient := tool.KubeClient()
		orders := &osacv1alpha1.ClusterOrderList{}
		var order *osacv1alpha1.ClusterOrder
		Eventually(func(g Gomega) {
			pollCtx, cancel := context.WithTimeout(ctx, pollRequestTimeout)
			defer cancel()
			err := kubeClient.List(pollCtx, orders, crclient.MatchingLabels{labels.ClusterOrderUuid: cluster.GetId()})
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
			pollCtx, cancel := context.WithTimeout(ctx, pollRequestTimeout)
			defer cancel()
			updated := &osacv1alpha1.ClusterOrder{}
			g.Expect(kubeClient.Get(pollCtx, crclient.ObjectKeyFromObject(order), updated)).To(Succeed())
			g.Expect(updated.Spec.ReleaseImage).To(Equal(image))
		}, time.Minute, time.Second).Should(Succeed())
	}

	waitForNodeRequestImage := func(order *osacv1alpha1.ClusterOrder, nodeSet, image string) {
		kubeClient := tool.KubeClient()
		Eventually(func(g Gomega) {
			pollCtx, cancel := context.WithTimeout(ctx, pollRequestTimeout)
			defer cancel()
			updated := &osacv1alpha1.ClusterOrder{}
			g.Expect(kubeClient.Get(pollCtx, crclient.ObjectKeyFromObject(order), updated)).To(Succeed())
			g.Expect(nodeRequestImage(updated, nodeSet)).To(Equal(image), nodeSet)
		}, time.Minute, time.Second).Should(Succeed())
	}

	applyControlPlaneState := func(cluster *publicv1.Cluster,
		componentState, aggregateState privatev1.ClusterUpgradeState) {
		privateCluster, err := privateClusters.Get(ctx, privatev1.ClustersGetRequest_builder{Id: cluster.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
		stored := privateCluster.GetObject()
		active := stored.GetStatus().GetActiveUpgradeAttempt()
		Expect(active).NotTo(BeNil())
		controlPlane := active.GetControlPlane()
		Expect(controlPlane).NotTo(BeNil())
		if aggregateState == privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_TIMED_OUT {
			controlPlane.SetTimeoutTime(timestamppb.New(
				controlPlane.GetStateTransitionTime().AsTime().Add(time.Second)))
			controlPlane.SetMessage("Control plane upgrade progress was not observed")
		} else {
			controlPlane.SetState(componentState)
			controlPlane.SetStateTransitionTime(timestamppb.New(
				controlPlane.GetStateTransitionTime().AsTime().Add(time.Second)))
		}
		Expect(stored.GetStatus().GetUpgradeHistory()).To(HaveLen(1))
		entry := stored.GetStatus().GetUpgradeHistory()[0]
		Expect(entry.GetAttemptId()).To(Equal(stored.GetStatus().GetUpgradeAttemptId()))
		entry.SetState(aggregateState)
		entry.SetStateTransitionTime(timestamppb.New(
			entry.GetStateTransitionTime().AsTime().Add(time.Second)))
		_, err = privateClusters.Update(ctx, privatev1.ClustersUpdateRequest_builder{
			Object: stored,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{
				"status.active_upgrade_attempt", "status.upgrade_history",
			}},
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		_, err = privateClusters.Signal(ctx, privatev1.ClustersSignalRequest_builder{Id: cluster.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
	}

	getPrivateCluster := func(cluster *publicv1.Cluster) *privatev1.Cluster {
		getCtx, cancel := context.WithTimeout(ctx, pollRequestTimeout)
		defer cancel()
		response, err := privateClusters.Get(getCtx, privatev1.ClustersGetRequest_builder{Id: cluster.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
		return response.GetObject()
	}

	waitForNodeSetImages := func(order *osacv1alpha1.ClusterOrder, image string, names ...string) {
		for _, name := range names {
			waitForNodeRequestImage(order, name, image)
		}
	}

	It("creates one aggregate attempt and suppresses node-set projection while the control plane is pending", func() {
		cluster := createCluster(twoNodeSets())
		order := waitForClusterOrder(cluster)
		seedObservedVersions(cluster)
		updateTargetVersion(cluster)

		waitForOrderReleaseImage(order, targetImage)
		waitForNodeSetImages(order, defaultImage, "compute", "gpu")
		kubeClient := tool.KubeClient()
		Consistently(func(g Gomega) {
			pollCtx, cancel := context.WithTimeout(ctx, pollRequestTimeout)
			defer cancel()
			updated := &osacv1alpha1.ClusterOrder{}
			g.Expect(kubeClient.Get(pollCtx, crclient.ObjectKeyFromObject(order), updated)).To(Succeed())
			g.Expect(nodeRequestImage(updated, "compute")).To(Equal(defaultImage))
			g.Expect(nodeRequestImage(updated, "gpu")).To(Equal(defaultImage))
		}, 5*time.Second, time.Second).Should(Succeed())

		stored := getPrivateCluster(cluster)
		Expect(stored.GetStatus().GetUpgradeAttemptId()).NotTo(BeEmpty())
		Expect(stored.GetStatus().GetUpgradeHistory()).To(HaveLen(1))
		entry := stored.GetStatus().GetUpgradeHistory()[0]
		Expect(entry.GetAttemptId()).To(Equal(stored.GetStatus().GetUpgradeAttemptId()))
		Expect(entry.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING))
		active := stored.GetStatus().GetActiveUpgradeAttempt()
		Expect(active).NotTo(BeNil())
		Expect(active.GetControlPlane().GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING))
		Expect(active.GetNodeSets()).To(HaveLen(2))
		for _, nodeSet := range active.GetNodeSets() {
			Expect(nodeSet.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING))
		}
		Expect(active.GetNodeSetsScheduledTime()).To(BeNil())
		for _, name := range []string{"compute", "gpu"} {
			Expect(stored.GetStatus().GetNodeSets()[name].GetVersion().GetName()).To(Equal(defaultVersionName), name)
		}
	})

	It("schedules node sets after attempt-attributed control-plane success", func() {
		cluster := createCluster(twoNodeSets())
		order := waitForClusterOrder(cluster)
		seedObservedVersions(cluster)
		updateTargetVersion(cluster)
		waitForOrderReleaseImage(order, targetImage)

		applyControlPlaneState(cluster,
			privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED,
			privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING)

		waitForNodeSetImages(order, targetImage, "compute", "gpu")
		stored := getPrivateCluster(cluster)
		Expect(stored.GetStatus().GetUpgradeHistory()).To(HaveLen(1))
		Expect(stored.GetStatus().GetUpgradeHistory()[0].GetState()).
			To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING))
		active := stored.GetStatus().GetActiveUpgradeAttempt()
		Expect(active.GetControlPlane().GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED))
		Expect(active.GetNodeSetsScheduledTime()).NotTo(BeNil())
		for _, name := range []string{"compute", "gpu"} {
			Expect(stored.GetStatus().GetNodeSets()[name].GetVersion().GetName()).To(Equal(targetVersionName), name)
		}
	})

	It("does not schedule node sets from an observed-version-only update", func() {
		cluster := createCluster(twoNodeSets())
		order := waitForClusterOrder(cluster)
		seedObservedVersions(cluster)
		updateTargetVersion(cluster)
		waitForOrderReleaseImage(order, targetImage)

		current, err := privateClusters.Get(ctx, privatev1.ClustersGetRequest_builder{Id: cluster.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())
		current.GetObject().GetStatus().SetObservedVersion(targetSemver)
		_, err = privateClusters.Update(ctx, privatev1.ClustersUpdateRequest_builder{
			Object:     current.GetObject(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.observed_version"}},
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		_, err = privateClusters.Signal(ctx, privatev1.ClustersSignalRequest_builder{Id: cluster.GetId()}.Build())
		Expect(err).NotTo(HaveOccurred())

		waitForNodeSetImages(order, defaultImage, "compute", "gpu")
		Consistently(func(g Gomega) {
			object := getPrivateCluster(cluster)
			active := object.GetStatus().GetActiveUpgradeAttempt()
			g.Expect(active.GetControlPlane().GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING))
			g.Expect(active.GetNodeSetsScheduledTime()).To(BeNil())
		}, 5*time.Second, time.Second).Should(Succeed())
		stored := getPrivateCluster(cluster)
		active := stored.GetStatus().GetActiveUpgradeAttempt()
		Expect(active.GetControlPlane().GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING))
		Expect(active.GetNodeSetsScheduledTime()).To(BeNil())
		for _, name := range []string{"compute", "gpu"} {
			Expect(stored.GetStatus().GetNodeSets()[name].GetVersion().GetName()).To(Equal(defaultVersionName), name)
		}
	})

	It("keeps timeout feedback active and recoverable through the aggregate history", func() {
		cluster := createCluster(twoNodeSets())
		order := waitForClusterOrder(cluster)
		seedObservedVersions(cluster)
		updateTargetVersion(cluster)
		waitForOrderReleaseImage(order, targetImage)

		applyControlPlaneState(cluster,
			privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING,
			privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_TIMED_OUT)

		timedOut := getPrivateCluster(cluster)
		Expect(timedOut.GetStatus().GetUpgradeHistory()[0].GetState()).
			To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_TIMED_OUT))
		active := timedOut.GetStatus().GetActiveUpgradeAttempt()
		Expect(active).NotTo(BeNil())
		Expect(active.GetControlPlane().GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING))
		Expect(active.GetControlPlane().GetTimeoutTime()).NotTo(BeNil())
		Expect(active.GetNodeSetsScheduledTime()).To(BeNil())

		applyControlPlaneState(cluster,
			privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED,
			privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING)

		waitForNodeSetImages(order, targetImage, "compute", "gpu")
		recovered := getPrivateCluster(cluster)
		Expect(recovered.GetStatus().GetUpgradeHistory()).To(HaveLen(1))
		Expect(recovered.GetStatus().GetUpgradeHistory()[0].GetState()).
			To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING))
		recoveredActive := recovered.GetStatus().GetActiveUpgradeAttempt()
		Expect(recoveredActive.GetControlPlane().GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED))
		Expect(recoveredActive.GetControlPlane().GetTimeoutTime()).To(BeNil())
		Expect(recoveredActive.GetNodeSetsScheduledTime()).NotTo(BeNil())
	})
	It("persists operator feedback through Fulfillment", ginkgo.Label(computeStorageLabel), func() {
		Expect(config.TestSuite).To(Equal(computeStorageLabel),
			"run with the installer compute-storage phase and its real operator")
		cluster := createCluster(twoNodeSets())
		order := waitForClusterOrder(cluster)
		kubeClient := tool.KubeClient()
		DeferCleanup(func() {
			// This contract supplies no HostedCluster or AAP job. Remove the
			// controlled order after requesting cluster deletion so the normal
			// fixture cleanup can observe archival without provider teardown.
			_, err := clustersClient.Delete(ctx, publicv1.ClustersDeleteRequest_builder{Id: cluster.GetId()}.Build())
			Expect(err).NotTo(HaveOccurred())
			Eventually(func(g Gomega) {
				current := &osacv1alpha1.ClusterOrder{}
				err := kubeClient.Get(ctx, crclient.ObjectKeyFromObject(order), current)
				if kubeerrors.IsNotFound(err) {
					return
				}
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(kubeClient.Delete(ctx, current)).To(Succeed())
				current.Finalizers = nil
				g.Expect(kubeClient.Update(ctx, current)).To(Succeed())
			}, time.Minute, time.Second).Should(Succeed())
		})
		Eventually(func(g Gomega) {
			updated := &osacv1alpha1.ClusterOrder{}
			g.Expect(kubeClient.Get(ctx, crclient.ObjectKeyFromObject(order), updated)).To(Succeed())
			if updated.Annotations == nil {
				updated.Annotations = map[string]string{}
			}
			// Keep the primary reconciler from replacing the controlled status.
			updated.Annotations["osac.openshift.io/management-state"] = "unmanaged"
			g.Expect(kubeClient.Update(ctx, updated)).To(Succeed())
		}, time.Minute, time.Second).Should(Succeed())

		seedObservedVersions(cluster)
		updateTargetVersion(cluster)
		waitForOrderReleaseImage(order, targetImage)
		stored := getPrivateCluster(cluster)
		attemptID := stored.GetStatus().GetUpgradeAttemptId()
		Expect(attemptID).NotTo(BeEmpty())

		Eventually(func(g Gomega) {
			updated := &osacv1alpha1.ClusterOrder{}
			g.Expect(kubeClient.Get(ctx, crclient.ObjectKeyFromObject(order), updated)).To(Succeed())
			updated.Status.UpgradeObservation = &osacv1alpha1.UpgradeObservation{
				AttemptID: attemptID,
				Version:   targetSemver,
				State:     osacv1alpha1.UpgradeObservationSucceeded,
			}
			g.Expect(kubeClient.Status().Update(ctx, updated)).To(Succeed())
		}, time.Minute, time.Second).Should(Succeed())

		Eventually(func(g Gomega) {
			response, err := privateClusters.Get(ctx, privatev1.ClustersGetRequest_builder{Id: cluster.GetId()}.Build())
			g.Expect(err).NotTo(HaveOccurred())
			current := response.GetObject()
			g.Expect(current.GetStatus().GetObservedVersion()).To(Equal(targetSemver))
			g.Expect(current.GetStatus().GetActiveUpgradeAttempt().GetControlPlane().GetState()).To(
				Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED))
			g.Expect(current.GetStatus().GetActiveUpgradeAttempt().GetNodeSetsScheduledTime()).NotTo(BeNil())
			g.Expect(current.GetStatus().GetUpgradeHistory()).To(HaveLen(1))
			g.Expect(current.GetStatus().GetUpgradeHistory()[0].GetState()).To(
				Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING))
		}, time.Minute, time.Second).Should(Succeed())

		Eventually(func(g Gomega) {
			updated := &osacv1alpha1.ClusterOrder{}
			g.Expect(kubeClient.Get(ctx, crclient.ObjectKeyFromObject(order), updated)).To(Succeed())
			updated.Status.NodeRequests = nil
			for _, request := range updated.Spec.NodeRequests {
				updated.Status.NodeRequests = append(updated.Status.NodeRequests, osacv1alpha1.NodeRequestStatus{
					NodeSet:       request.NodeSet,
					NumberOfNodes: request.NumberOfNodes,
					BareMetal:     request.BareMetal,
					UpgradeObservation: &osacv1alpha1.UpgradeObservation{
						AttemptID:      attemptID,
						Version:        targetSemver,
						State:          osacv1alpha1.UpgradeObservationSucceeded,
						ReadyAtVersion: true,
					},
				})
			}
			g.Expect(kubeClient.Status().Update(ctx, updated)).To(Succeed())
		}, time.Minute, time.Second).Should(Succeed())

		Eventually(func(g Gomega) {
			response, err := privateClusters.Get(ctx, privatev1.ClustersGetRequest_builder{Id: cluster.GetId()}.Build())
			g.Expect(err).NotTo(HaveOccurred())
			current := response.GetObject()
			g.Expect(current.GetStatus().GetUpgradeHistory()).To(HaveLen(1))
			g.Expect(current.GetStatus().GetUpgradeHistory()[0].GetState()).To(
				Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_SUCCEEDED))
			g.Expect(current.GetStatus().GetNodeSets()).To(HaveLen(2))
			for _, nodeSet := range current.GetStatus().GetNodeSets() {
				g.Expect(nodeSet.GetObservedVersion()).To(Equal(targetSemver))
			}
		}, time.Minute, time.Second).Should(Succeed())
	})
})
