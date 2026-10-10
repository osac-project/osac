/*
Copyright (c) 2026 Red Hat Inc.

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

package it

import (
	"context"
	"fmt"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// This fixture gives the Hub cache two valid entries with distinct target
// namespaces on the component test cluster. It exercises canonical Hub entry
// and namespace routing; it does not model isolation between separate clusters.
var _ = Describe("Canonical networking Hub cache-entry routing", func() {
	It("places all networking CR families on the canonical Hub when an alternate Hub is available", func(ctx context.Context) {
		hubsClient := privatev1.NewHubsClient(tool.InternalView().AdminConn())
		hubAResponse, err := hubsClient.Get(ctx, privatev1.HubsGetRequest_builder{Id: hubId}.Build())
		Expect(err).ToNot(HaveOccurred())
		hubANamespace := hubAResponse.GetObject().GetSpec().GetNamespace()
		Expect(hubANamespace).ToNot(BeEmpty())

		hubBID, hubBNamespace := createValidRoutingHub(ctx, hubsClient, hubANamespace)
		tenantsClient := privatev1.NewTenantsClient(tool.InternalView().AdminConn())
		expectTenantSyncedToHubNamespace(ctx, tenantsClient, hubBNamespace)

		networkClassesClient := privatev1.NewNetworkClassesClient(tool.InternalView().AdminConn())
		virtualNetworksClient := privatev1.NewVirtualNetworksClient(tool.InternalView().AdminConn())
		subnetsClient := privatev1.NewSubnetsClient(tool.InternalView().AdminConn())
		securityGroupsClient := privatev1.NewSecurityGroupsClient(tool.InternalView().AdminConn())
		poolsClient := privatev1.NewExternalIPPoolsClient(tool.InternalView().AdminConn())
		privateExternalIPsClient := privatev1.NewExternalIPsClient(tool.InternalView().AdminConn())
		externalIPsClient := publicv1.NewExternalIPsClient(tool.ExternalView().UserConn())
		attachmentsClient := publicv1.NewExternalIPAttachmentsClient(tool.ExternalView().UserConn())
		privateAttachmentsClient := privatev1.NewExternalIPAttachmentsClient(tool.InternalView().AdminConn())
		natGatewaysClient := publicv1.NewNATGatewaysClient(tool.ExternalView().UserConn())
		clusterTemplatesClient := privatev1.NewClusterTemplatesClient(tool.InternalView().AdminConn())
		clustersClient := publicv1.NewClustersClient(tool.ExternalView().UserConn())

		networkClassName := fmt.Sprintf("test-hub-routing-nc-%s", uuid.New())
		networkClassResponse, err := networkClassesClient.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
			Object: privatev1.NetworkClass_builder{
				Metadata:      privatev1.Metadata_builder{Name: networkClassName, Tenant: usersGroup}.Build(),
				Title:         "Hub canonical placement test",
				FabricManager: new("netris"),
				Spec: privatev1.NetworkClassSpec_builder{
					Defaults: privatev1.NetworkDefaults_builder{
						VirtualNetworkIpv4Cidr: "10.230.0.0/16",
						SubnetIpv4Cidr:         "10.230.0.0/20",
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		networkClassID := networkClassResponse.GetObject().GetId()
		DeferCleanup(func(cleanupCtx context.Context) {
			deleteAndWaitForComputeInstanceFixtureResource(cleanupCtx,
				func(deleteCtx context.Context) error {
					_, deleteErr := networkClassesClient.Delete(deleteCtx, privatev1.NetworkClassesDeleteRequest_builder{Id: networkClassID}.Build())
					return deleteErr
				},
				func(getCtx context.Context) error {
					_, getErr := networkClassesClient.Get(getCtx, privatev1.NetworkClassesGetRequest_builder{Id: networkClassID}.Build())
					return getErr
				})
		})
		setNetworkClassCanonicalHub(ctx, networkClassesClient, networkClassID, hubId)
		expectNetworkClassStatus(
			ctx,
			networkClassesClient,
			networkClassID,
			privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
			hubId,
			"",
		)

		By("creating VirtualNetwork, Subnet, and SecurityGroup against the canonical NetworkClass")
		virtualNetworkID := fmt.Sprintf("test-hub-a-vn-%s", uuid.New())
		_, err = virtualNetworksClient.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
			Object: privatev1.VirtualNetwork_builder{
				Id:       virtualNetworkID,
				Metadata: privatev1.Metadata_builder{Name: virtualNetworkID, Tenant: usersGroup}.Build(),
				Spec: privatev1.VirtualNetworkSpec_builder{
					NetworkClass: privatev1.NetworkClassReference_builder{Id: networkClassID}.Build(),
					Region:       "us-east-1",
					Ipv4Cidr:     new("10.232.0.0/16"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(cleanupCtx context.Context) {
			_, _ = virtualNetworksClient.Delete(cleanupCtx, privatev1.VirtualNetworksDeleteRequest_builder{Id: virtualNetworkID}.Build())
		})
		expectNetworkingResourceHub(ctx, hubId, func(getCtx context.Context) (string, error) {
			response, getErr := virtualNetworksClient.Get(getCtx, privatev1.VirtualNetworksGetRequest_builder{Id: virtualNetworkID}.Build())
			if getErr != nil {
				return "", getErr
			}
			return response.GetObject().GetStatus().GetHub(), nil
		})
		expectNetworkingCRInHub(ctx, hubANamespace, hubBNamespace, labels.VirtualNetworkUuid, virtualNetworkID, func(namespace string) (int, error) {
			list := &osacv1alpha1.VirtualNetworkList{}
			listErr := tool.KubeClient().List(ctx, list, crclient.InNamespace(namespace), crclient.MatchingLabels{labels.VirtualNetworkUuid: virtualNetworkID})
			return len(list.Items), listErr
		})
		setRoutingVirtualNetworkReady(ctx, virtualNetworksClient, virtualNetworkID)

		subnetID := fmt.Sprintf("test-hub-a-subnet-%s", uuid.New())
		_, err = subnetsClient.Create(ctx, privatev1.SubnetsCreateRequest_builder{
			Object: privatev1.Subnet_builder{
				Id:       subnetID,
				Metadata: privatev1.Metadata_builder{Name: subnetID, Tenant: usersGroup}.Build(),
				Spec: privatev1.SubnetSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
					Ipv4Cidr:       new("10.232.1.0/24"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(cleanupCtx context.Context) {
			_, _ = subnetsClient.Delete(cleanupCtx, privatev1.SubnetsDeleteRequest_builder{Id: subnetID}.Build())
		})
		expectNetworkingResourceHub(ctx, hubId, func(getCtx context.Context) (string, error) {
			response, getErr := subnetsClient.Get(getCtx, privatev1.SubnetsGetRequest_builder{Id: subnetID}.Build())
			if getErr != nil {
				return "", getErr
			}
			return response.GetObject().GetStatus().GetHub(), nil
		})
		expectNetworkingCRInHub(ctx, hubANamespace, hubBNamespace, labels.SubnetUuid, subnetID, func(namespace string) (int, error) {
			list := &osacv1alpha1.SubnetList{}
			listErr := tool.KubeClient().List(ctx, list, crclient.InNamespace(namespace), crclient.MatchingLabels{labels.SubnetUuid: subnetID})
			return len(list.Items), listErr
		})
		setRoutingSubnetReady(ctx, subnetsClient, subnetID)

		securityGroupID := fmt.Sprintf("test-hub-a-sg-%s", uuid.New())
		_, err = securityGroupsClient.Create(ctx, privatev1.SecurityGroupsCreateRequest_builder{
			Object: privatev1.SecurityGroup_builder{
				Id:       securityGroupID,
				Metadata: privatev1.Metadata_builder{Name: securityGroupID, Tenant: usersGroup}.Build(),
				Spec: privatev1.SecurityGroupSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(cleanupCtx context.Context) {
			_, _ = securityGroupsClient.Delete(cleanupCtx, privatev1.SecurityGroupsDeleteRequest_builder{Id: securityGroupID}.Build())
		})
		expectNetworkingResourceHub(ctx, hubId, func(getCtx context.Context) (string, error) {
			response, getErr := securityGroupsClient.Get(getCtx, privatev1.SecurityGroupsGetRequest_builder{Id: securityGroupID}.Build())
			if getErr != nil {
				return "", getErr
			}
			return response.GetObject().GetStatus().GetHub(), nil
		})
		expectNetworkingCRInHub(ctx, hubANamespace, hubBNamespace, labels.SecurityGroupUuid, securityGroupID, func(namespace string) (int, error) {
			list := &osacv1alpha1.SecurityGroupList{}
			listErr := tool.KubeClient().List(ctx, list, crclient.InNamespace(namespace), crclient.MatchingLabels{labels.SecurityGroupUuid: securityGroupID})
			return len(list.Items), listErr
		})

		By("creating an ExternalIPPool and two ExternalIPs")
		poolID := fmt.Sprintf("test-hub-a-pool-%s", uuid.New())
		_, err = poolsClient.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
			Object: privatev1.ExternalIPPool_builder{
				Id:       poolID,
				Metadata: privatev1.Metadata_builder{Name: poolID, Tenant: usersGroup}.Build(),
				Spec: privatev1.ExternalIPPoolSpec_builder{
					Cidrs:    []string{uniqueCIDR()},
					IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(cleanupCtx context.Context) {
			_, _ = poolsClient.Delete(cleanupCtx, privatev1.ExternalIPPoolsDeleteRequest_builder{Id: poolID}.Build())
		})
		expectNetworkingResourceHub(ctx, hubId, func(getCtx context.Context) (string, error) {
			response, getErr := poolsClient.Get(getCtx, privatev1.ExternalIPPoolsGetRequest_builder{Id: poolID}.Build())
			if getErr != nil {
				return "", getErr
			}
			return response.GetObject().GetStatus().GetHub(), nil
		})
		expectNetworkingCRInHub(ctx, hubANamespace, hubBNamespace, labels.ExternalIPPoolUuid, poolID, func(namespace string) (int, error) {
			list := &osacv1alpha1.ExternalIPPoolList{}
			listErr := tool.KubeClient().List(ctx, list, crclient.InNamespace(namespace), crclient.MatchingLabels{labels.ExternalIPPoolUuid: poolID})
			return len(list.Items), listErr
		})
		setRoutingExternalIPPoolReady(ctx, poolsClient, poolID)

		createAllocatedExternalIP := func() string {
			ipID := fmt.Sprintf("test-hub-a-ip-%s", uuid.New())
			_, createErr := externalIPsClient.Create(ctx, publicv1.ExternalIPsCreateRequest_builder{
				Object: publicv1.ExternalIP_builder{
					Id:       ipID,
					Metadata: publicv1.Metadata_builder{Name: ipID}.Build(),
					Spec: publicv1.ExternalIPSpec_builder{
						Pool: publicv1.ExternalIPPoolReference_builder{Id: poolID}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(createErr).ToNot(HaveOccurred())
			DeferCleanup(func(cleanupCtx context.Context) {
				_, _ = externalIPsClient.Delete(cleanupCtx, publicv1.ExternalIPsDeleteRequest_builder{Id: ipID}.Build())
			})
			expectNetworkingResourceHub(ctx, hubId, func(getCtx context.Context) (string, error) {
				response, getErr := privateExternalIPsClient.Get(getCtx, privatev1.ExternalIPsGetRequest_builder{Id: ipID}.Build())
				if getErr != nil {
					return "", getErr
				}
				return response.GetObject().GetStatus().GetHub(), nil
			})
			expectNetworkingCRInHub(ctx, hubANamespace, hubBNamespace, labels.ExternalIPUuid, ipID, func(namespace string) (int, error) {
				list := &osacv1alpha1.ExternalIPList{}
				listErr := tool.KubeClient().List(ctx, list, crclient.InNamespace(namespace), crclient.MatchingLabels{labels.ExternalIPUuid: ipID})
				return len(list.Items), listErr
			})
			setRoutingExternalIPAllocated(ctx, privateExternalIPsClient, ipID)
			return ipID
		}
		attachmentIPID := createAllocatedExternalIP()
		natGatewayIPID := createAllocatedExternalIP()

		By("creating a Cluster target and an ExternalIPAttachment")
		instanceTypeID := createCatalogItemBareMetalInstanceTypeFixture(ctx, "")

		clusterTemplateID := fmt.Sprintf("test-hub-a-template-%s", uuid.New())
		_, err = clusterTemplatesClient.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
			Object: privatev1.ClusterTemplate_builder{
				Id:       clusterTemplateID,
				Title:    "Hub placement test template",
				Metadata: privatev1.Metadata_builder{Name: clusterTemplateID}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(cleanupCtx context.Context) {
			_, _ = clusterTemplatesClient.Delete(cleanupCtx, privatev1.ClusterTemplatesDeleteRequest_builder{Id: clusterTemplateID}.Build())
		})

		clusterResponse, err := clustersClient.Create(ctx, publicv1.ClustersCreateRequest_builder{
			Object: publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{Name: fmt.Sprintf("test-hub-a-cluster-%s", uuid.New()[24:])}.Build(),
				Spec: publicv1.ClusterSpec_builder{
					Template: publicv1.ClusterTemplateReference_builder{Id: clusterTemplateID}.Build(),
					NodeSets: testClusterNodeSets(instanceTypeID, 1),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		clusterID := clusterResponse.GetObject().GetId()
		DeferCleanup(func(cleanupCtx context.Context) {
			_, _ = clustersClient.Delete(cleanupCtx, publicv1.ClustersDeleteRequest_builder{Id: clusterID}.Build())
		})

		attachmentID := fmt.Sprintf("test-hub-a-attachment-%s", uuid.New())
		_, err = attachmentsClient.Create(ctx, publicv1.ExternalIPAttachmentsCreateRequest_builder{
			Object: publicv1.ExternalIPAttachment_builder{
				Id:       attachmentID,
				Metadata: publicv1.Metadata_builder{Name: attachmentID}.Build(),
				Spec: publicv1.ExternalIPAttachmentSpec_builder{
					ExternalIp:     publicv1.ExternalIPLocalReference_builder{Id: attachmentIPID}.Build(),
					Cluster:        publicv1.ClusterLocalReference_builder{Id: clusterID}.Build(),
					TargetEndpoint: publicv1.ExternalIPAttachmentEndpoint_EXTERNAL_IP_ATTACHMENT_ENDPOINT_API,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(cleanupCtx context.Context) {
			_, _ = attachmentsClient.Delete(cleanupCtx, publicv1.ExternalIPAttachmentsDeleteRequest_builder{Id: attachmentID}.Build())
		})
		expectNetworkingResourceHub(ctx, hubId, func(getCtx context.Context) (string, error) {
			response, getErr := privateAttachmentsClient.Get(getCtx, privatev1.ExternalIPAttachmentsGetRequest_builder{Id: attachmentID}.Build())
			if getErr != nil {
				return "", getErr
			}
			return response.GetObject().GetStatus().GetHub(), nil
		})
		expectNetworkingCRInHub(ctx, hubANamespace, hubBNamespace, labels.ExternalIPAttachmentUuid, attachmentID, func(namespace string) (int, error) {
			list := &osacv1alpha1.ExternalIPAttachmentList{}
			listErr := tool.KubeClient().List(ctx, list, crclient.InNamespace(namespace), crclient.MatchingLabels{labels.ExternalIPAttachmentUuid: attachmentID})
			return len(list.Items), listErr
		})

		By("creating a NATGateway from the second allocated ExternalIP")
		natGatewayID := fmt.Sprintf("test-hub-a-nat-gateway-%s", uuid.New())
		_, err = natGatewaysClient.Create(ctx, publicv1.NATGatewaysCreateRequest_builder{
			Object: publicv1.NATGateway_builder{
				Id:       natGatewayID,
				Metadata: publicv1.Metadata_builder{Name: natGatewayID}.Build(),
				Spec: publicv1.NATGatewaySpec_builder{
					VirtualNetwork: publicv1.VirtualNetworkLocalReference_builder{Id: virtualNetworkID}.Build(),
					ExternalIp:     publicv1.ExternalIPLocalReference_builder{Id: natGatewayIPID}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(cleanupCtx context.Context) {
			_, _ = natGatewaysClient.Delete(cleanupCtx, publicv1.NATGatewaysDeleteRequest_builder{Id: natGatewayID}.Build())
		})
		expectNetworkingCRInHub(ctx, hubANamespace, hubBNamespace, labels.NATGatewayUuid, natGatewayID, func(namespace string) (int, error) {
			list := &osacv1alpha1.NATGatewayList{}
			listErr := tool.KubeClient().List(ctx, list, crclient.InNamespace(namespace), crclient.MatchingLabels{labels.NATGatewayUuid: natGatewayID})
			return len(list.Items), listErr
		})

		By("preserving the SecurityGroup assignment if the canonical Hub changes")
		setNetworkClassCanonicalHub(ctx, networkClassesClient, networkClassID, hubBID)
		By("triggering SecurityGroup reconciliation against the changed canonical Hub")
		_, err = securityGroupsClient.Update(ctx, privatev1.SecurityGroupsUpdateRequest_builder{
			Object: privatev1.SecurityGroup_builder{
				Id: securityGroupID,
				Metadata: privatev1.Metadata_builder{
					Labels: map[string]string{"integration-test-trigger": "canonical-hub-changed"},
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.labels"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Eventually(func(g Gomega) {
			response, getErr := securityGroupsClient.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: securityGroupID}.Build())
			g.Expect(getErr).ToNot(HaveOccurred())
			g.Expect(response.GetObject().GetStatus().GetHub()).To(Equal(hubId))
			g.Expect(response.GetObject().GetStatus().GetState()).To(Equal(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_FAILED))
			g.Expect(response.GetObject().GetStatus().GetMessage()).To(ContainSubstring("assignment conflicts"))

			countSecurityGroups := func(namespace string) (int, error) {
				list := &osacv1alpha1.SecurityGroupList{}
				listErr := tool.KubeClient().List(ctx, list, crclient.InNamespace(namespace), crclient.MatchingLabels{labels.SecurityGroupUuid: securityGroupID})
				return len(list.Items), listErr
			}
			countA, listErr := countSecurityGroups(hubANamespace)
			g.Expect(listErr).ToNot(HaveOccurred())
			countB, listErr := countSecurityGroups(hubBNamespace)
			g.Expect(listErr).ToNot(HaveOccurred())
			g.Expect(countA).To(Equal(1))
			g.Expect(countB).To(BeZero())
		}, time.Minute, time.Second).Should(Succeed())
		setNetworkClassCanonicalHub(ctx, networkClassesClient, networkClassID, hubId)
	})

})

func createValidRoutingHub(ctx context.Context, hubsClient privatev1.HubsClient, canonicalHubNamespace string) (string, string) {
	GinkgoHelper()
	hubID := fmt.Sprintf("test-routing-hub-%s", uuid.New())
	namespace := fmt.Sprintf("test-hub-%s", uuid.New()[24:])
	serviceAccountName := "networking-routing-test"
	clusterRoleName := fmt.Sprintf("%s-tenant-sync", hubID)
	Expect(tool.KubeClient().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: namespace},
	})).To(Succeed())
	DeferCleanup(func(cleanupCtx context.Context) {
		_, _ = hubsClient.Delete(cleanupCtx, privatev1.HubsDeleteRequest_builder{Id: hubID}.Build())
		for _, object := range []crclient.Object{
			&rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: clusterRoleName}},
			&rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: clusterRoleName}},
		} {
			deleteErr := tool.KubeClient().Delete(cleanupCtx, object)
			if deleteErr != nil && !apierrors.IsNotFound(deleteErr) {
				Expect(deleteErr).ToNot(HaveOccurred())
			}
		}
		deleteErr := tool.KubeClient().Delete(cleanupCtx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}})
		if deleteErr != nil && !apierrors.IsNotFound(deleteErr) {
			Expect(deleteErr).ToNot(HaveOccurred())
		}
		expectTenantSyncedToHubNamespace(
			cleanupCtx,
			privatev1.NewTenantsClient(tool.InternalView().AdminConn()),
			canonicalHubNamespace,
		)
	})

	Expect(tool.KubeClient().Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: serviceAccountName, Namespace: namespace},
	})).To(Succeed())
	Expect(tool.KubeClient().Create(ctx, &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: serviceAccountName, Namespace: namespace},
		Rules: []rbacv1.PolicyRule{{
			APIGroups: []string{osacv1alpha1.GroupVersion.Group},
			Resources: []string{
				"virtualnetworks",
				"subnets",
				"securitygroups",
				"externalippools",
				"externalips",
				"externalipattachments",
				"natgateways",
			},
			Verbs: []string{"create", "delete", "get", "list", "patch", "update"},
		}},
	})).To(Succeed())
	Expect(tool.KubeClient().Create(ctx, &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: serviceAccountName, Namespace: namespace},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     serviceAccountName,
		},
		Subjects: []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      serviceAccountName,
			Namespace: namespace,
		}},
	})).To(Succeed())
	Expect(tool.KubeClient().Create(ctx, &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: clusterRoleName},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{osacv1alpha1.GroupVersion.Group},
				Resources: []string{"tenants"},
				Verbs:     []string{"create", "get", "patch"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"namespaces"},
				Verbs:     []string{"create", "get", "patch"},
			},
		},
	})).To(Succeed())
	Expect(tool.KubeClient().Create(ctx, &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: clusterRoleName},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     clusterRoleName,
		},
		Subjects: []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      serviceAccountName,
			Namespace: namespace,
		}},
	})).To(Succeed())

	expirationSeconds := int64(3600)
	tokenResponse, err := tool.kubeClientSet.CoreV1().ServiceAccounts(namespace).CreateToken(
		ctx,
		serviceAccountName,
		&authenticationv1.TokenRequest{Spec: authenticationv1.TokenRequestSpec{ExpirationSeconds: &expirationSeconds}},
		metav1.CreateOptions{},
	)
	Expect(err).ToNot(HaveOccurred())

	kubeconfig, err := os.ReadFile(tool.kcFile)
	Expect(err).ToNot(HaveOccurred())
	config, err := clientcmd.Load(kubeconfig)
	Expect(err).ToNot(HaveOccurred())
	currentContext := config.Contexts[config.CurrentContext]
	Expect(currentContext).ToNot(BeNil())
	cluster := config.Clusters[currentContext.Cluster]
	Expect(cluster).ToNot(BeNil())
	cluster.Server = "https://kubernetes.default.svc"
	config.Clusters = map[string]*clientcmdapi.Cluster{"hub-cluster": cluster}
	config.AuthInfos = map[string]*clientcmdapi.AuthInfo{"hub-user": {Token: tokenResponse.Status.Token}}
	config.Contexts = map[string]*clientcmdapi.Context{
		"hub-context": {Cluster: "hub-cluster", AuthInfo: "hub-user"},
	}
	config.CurrentContext = "hub-context"
	kubeconfig, err = clientcmd.Write(*config)
	Expect(err).ToNot(HaveOccurred())

	_, err = hubsClient.Create(ctx, privatev1.HubsCreateRequest_builder{
		Object: privatev1.Hub_builder{
			Id:       hubID,
			Metadata: privatev1.Metadata_builder{Name: hubID}.Build(),
			Spec: privatev1.HubSpec_builder{
				Kubeconfig: kubeconfig,
				Namespace:  namespace,
			}.Build(),
		}.Build(),
	}.Build())
	Expect(err).ToNot(HaveOccurred())
	return hubID, namespace
}

func expectTenantSyncedToHubNamespace(ctx context.Context, tenantsClient privatev1.TenantsClient, hubNamespace string) {
	GinkgoHelper()
	_, err := tenantsClient.Signal(ctx, privatev1.TenantsSignalRequest_builder{Id: usersGroup}.Build())
	Expect(err).ToNot(HaveOccurred())
	Eventually(func(g Gomega) {
		hubTenant := &osacv1alpha1.Tenant{}
		g.Expect(tool.KubeClient().Get(ctx, crclient.ObjectKey{
			Namespace: hubNamespace,
			Name:      usersGroup,
		}, hubTenant)).To(Succeed())

		tenantNamespace := &corev1.Namespace{}
		g.Expect(tool.KubeClient().Get(ctx, crclient.ObjectKey{Name: usersGroup}, tenantNamespace)).To(Succeed())
		g.Expect(tenantNamespace.Labels[labels.Project]).To(Equal(hubNamespace))

		response, getErr := tenantsClient.Get(ctx, privatev1.TenantsGetRequest_builder{Id: usersGroup}.Build())
		g.Expect(getErr).ToNot(HaveOccurred())
		g.Expect(response.GetObject().GetStatus().GetState()).To(Equal(privatev1.TenantState_TENANT_STATE_SYNCED),
			"tenant status message: %s", response.GetObject().GetStatus().GetMessage())
	}, time.Minute, time.Second).Should(Succeed())
}

func expectNetworkingResourceHub(ctx context.Context, expectedHubID string, getHub func(context.Context) (string, error)) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		hubID, err := getHub(ctx)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(hubID).To(Equal(expectedHubID))
	}, time.Minute, time.Second).Should(Succeed())
}

func expectNetworkingCRInHub(
	ctx context.Context,
	hubANamespace string,
	hubBNamespace string,
	label string,
	resourceID string,
	count func(namespace string) (int, error),
) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		countA, err := count(hubANamespace)
		g.Expect(err).ToNot(HaveOccurred())
		countB, err := count(hubBNamespace)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(countA).To(Equal(1), "%s %q should be created in canonical Hub A", label, resourceID)
		g.Expect(countB).To(BeZero(), "%s %q should be absent from alternate Hub B", label, resourceID)
	}, time.Minute, time.Second).Should(Succeed())
}

func setRoutingVirtualNetworkReady(ctx context.Context, client privatev1.VirtualNetworksClient, id string) {
	GinkgoHelper()
	response, err := client.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{Id: id}.Build())
	Expect(err).ToNot(HaveOccurred())
	object := response.GetObject()
	object.GetStatus().SetState(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY)
	_, err = client.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{
		Object: object, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
	}.Build())
	Expect(err).ToNot(HaveOccurred())
}

func setRoutingSubnetReady(ctx context.Context, client privatev1.SubnetsClient, id string) {
	GinkgoHelper()
	response, err := client.Get(ctx, privatev1.SubnetsGetRequest_builder{Id: id}.Build())
	Expect(err).ToNot(HaveOccurred())
	object := response.GetObject()
	object.GetStatus().SetState(privatev1.SubnetState_SUBNET_STATE_READY)
	_, err = client.Update(ctx, privatev1.SubnetsUpdateRequest_builder{
		Object: object, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
	}.Build())
	Expect(err).ToNot(HaveOccurred())
}

func setRoutingExternalIPPoolReady(ctx context.Context, client privatev1.ExternalIPPoolsClient, id string) {
	GinkgoHelper()
	response, err := client.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: id}.Build())
	Expect(err).ToNot(HaveOccurred())
	object := response.GetObject()
	object.GetStatus().SetState(privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY)
	_, err = client.Update(ctx, privatev1.ExternalIPPoolsUpdateRequest_builder{
		Object: object, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
	}.Build())
	Expect(err).ToNot(HaveOccurred())
}

func setRoutingExternalIPAllocated(ctx context.Context, client privatev1.ExternalIPsClient, id string) {
	GinkgoHelper()
	response, err := client.Get(ctx, privatev1.ExternalIPsGetRequest_builder{Id: id}.Build())
	Expect(err).ToNot(HaveOccurred())
	object := response.GetObject()
	object.GetStatus().SetState(privatev1.ExternalIPState_EXTERNAL_IP_STATE_ALLOCATED)
	_, err = client.Update(ctx, privatev1.ExternalIPsUpdateRequest_builder{
		Object: object, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
	}.Build())
	Expect(err).ToNot(HaveOccurred())
}
