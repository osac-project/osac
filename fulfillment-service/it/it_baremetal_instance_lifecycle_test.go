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
	"encoding/json"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	bmfov1alpha1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	osacv1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// bmiTestSSHPublicKey is a valid OpenSSH public key used to satisfy the
// requirement that a BareMetalInstance provide at least one authentication
// method (ssh_public_key or user_data) at create time.
const bmiTestSSHPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIG8K1ZuSC7tmzxD5LJJXwkCfStVEjzXWYCFhJaLBxWAn test@example.com"

var _ = Describe("BareMetalInstance lifecycle", func() {
	var (
		bareMetalInstancesClient            publicv1.BareMetalInstancesClient
		privateBareMetalInstancesClient     privatev1.BareMetalInstancesClient
		bareMetalInstanceTemplatesClient    privatev1.BareMetalInstanceTemplatesClient
		bareMetalInstanceCatalogItemsClient privatev1.BareMetalInstanceCatalogItemsClient
		bareMetalInstanceTypesClient        privatev1.BareMetalInstanceTypesClient
		diskImagesClient                    privatev1.DiskImagesClient
		templateId                          string
		catalogItemId                       string
		instanceTypeId                      string
		defaultDiskImageId                  string
		defaultNetworkingFixture            catalogItemNetworkFixture
	)

	BeforeEach(func(ctx context.Context) {
		// Create clients
		bareMetalInstancesClient = publicv1.NewBareMetalInstancesClient(tool.ExternalView().UserConn())
		privateBareMetalInstancesClient = privatev1.NewBareMetalInstancesClient(tool.InternalView().AdminConn())
		bareMetalInstanceTemplatesClient = privatev1.NewBareMetalInstanceTemplatesClient(tool.InternalView().AdminConn())
		bareMetalInstanceCatalogItemsClient = privatev1.NewBareMetalInstanceCatalogItemsClient(tool.InternalView().AdminConn())
		bareMetalInstanceTypesClient = privatev1.NewBareMetalInstanceTypesClient(tool.InternalView().AdminConn())
		diskImagesClient = privatev1.NewDiskImagesClient(tool.InternalView().AdminConn())

		// Create BareMetalInstanceType before the template so the template can
		// provide the effective type for catalog-based instance creation.
		instanceTypeResp, err := bareMetalInstanceTypesClient.Create(ctx, privatev1.BareMetalInstanceTypesCreateRequest_builder{
			Object: privatev1.BareMetalInstanceType_builder{
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("test-instance-type-%s", uuid.New()[24:32]),
					Tenant: auth.SharedTenant,
				}.Build(),
				Spec: privatev1.BareMetalInstanceTypeSpec_builder{
					Hardware: privatev1.BareMetalHardwareSpec_builder{
						Cpu: privatev1.BareMetalCPUSpec_builder{
							Cores:          4,
							Architecture:   "amd64",
							ThreadsPerCore: 2,
						}.Build(),
						Memory: privatev1.BareMetalMemorySpec_builder{
							TotalGb: 16,
						}.Build(),
						NetworkPorts: []*privatev1.BareMetalNetworkPortSpec{
							privatev1.BareMetalNetworkPortSpec_builder{
								Name:  "data-0",
								Role:  "fabric",
								Type:  "Ethernet",
								Speed: "25Gbps",
							}.Build(),
						},
					}.Build(),
					HostLabelSelector: privatev1.BareMetalLabelSelector_builder{
						MatchLabels: map[string]string{
							"osac.openshift.io/host-type": "compute",
						},
					}.Build(),
					Description: "Test bare metal instance type for integration tests.",
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		instanceTypeId = instanceTypeResp.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) {
			_, err := bareMetalInstanceTypesClient.Delete(ctx, privatev1.BareMetalInstanceTypesDeleteRequest_builder{
				Id: instanceTypeId,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		// Lifecycle Creates omit network_attachments and use shared catalog items without
		// network field policies; hard-fail defaulting needs a tenant-default Subnet/SG.
		defaultNetworkingFixture = ensureTenantDefaultNetworkingFixture(ctx, usersGroup, "")

		// Create BareMetalInstanceTemplate with an explicit ID that matches the BMFO CRD
		// validation pattern (^[a-zA-Z_][a-zA-Z0-9._]*$). Auto-generated UUIDs start with
		// a digit and are rejected by the CRD when the controller creates the CR.
		templateResp, err := bareMetalInstanceTemplatesClient.Create(ctx, privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
			Object: privatev1.BareMetalInstanceTemplate_builder{
				Id:          fmt.Sprintf("test_template_%s", strings.ReplaceAll(uuid.New(), "-", "_")),
				Title:       "Test BMI Template",
				Description: "Template for bare metal instance lifecycle test.",
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("test-template-%s", uuid.New()[24:32]),
				}.Build(),
				InstanceType: privatev1.BareMetalInstanceTypeReference_builder{
					Id:     instanceTypeId,
					Shared: true,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		templateId = templateResp.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) {
			_, err := bareMetalInstanceTemplatesClient.Delete(ctx, privatev1.BareMetalInstanceTemplatesDeleteRequest_builder{
				Id: templateId,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		diskImageResp, err := diskImagesClient.Create(ctx, privatev1.DiskImagesCreateRequest_builder{
			Object: privatev1.DiskImage_builder{
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("test-default-di-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: privatev1.DiskImageSpec_builder{
					SourceType:    privatev1.SourceType_SOURCE_TYPE_REGISTRY,
					SourceRef:     "quay.io/test/rhel9:latest",
					GuestOsFamily: privatev1.GuestOSFamily_GUEST_OS_FAMILY_LINUX,
					Architecture: []privatev1.Architecture{
						privatev1.Architecture_ARCHITECTURE_AMD64,
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		defaultDiskImageId = diskImageResp.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) {
			_, err := diskImagesClient.Delete(ctx, privatev1.DiskImagesDeleteRequest_builder{
				Id: defaultDiskImageId,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		// Create a published Catalog Item for provisioning; drafts remain readable.
		catalogResp, err := bareMetalInstanceCatalogItemsClient.Create(ctx, privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
			Object: privatev1.BareMetalInstanceCatalogItem_builder{
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("test-catalog-item-%s", uuid.New()[24:32]),
				}.Build(),
				Title:     "Test BMI Catalog Item",
				Template:  privatev1.BareMetalInstanceTemplateReference_builder{Id: templateId}.Build(),
				Published: true,
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		catalogItemId = catalogResp.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) {
			_, err := bareMetalInstanceCatalogItemsClient.Delete(ctx, privatev1.BareMetalInstanceCatalogItemsDeleteRequest_builder{
				Id: catalogItemId,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

	})

	It("Creates a BareMetalInstance with omitted networking and verifies fields", func(ctx context.Context) {
		// Omitted network_attachments request the tenant default Subnet and
		// SecurityGroup; the BeforeEach ensured a ready default fixture.
		network := getTenantDefaultNetworkFixture(ctx, usersGroup, "")

		// Create BareMetalInstance via public API
		createResp, err := bareMetalInstancesClient.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{
			Object: publicv1.BareMetalInstance_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-bmi-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.BareMetalInstanceSpec_builder{
					CatalogItem:  publicv1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemId}.Build(),
					InstanceType: publicv1.BareMetalInstanceTypeReference_builder{Id: instanceTypeId, Shared: true}.Build(),
					SshPublicKey: new(bmiTestSSHPublicKey),
					DiskImage:    publicv1.DiskImageReference_builder{Id: defaultDiskImageId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		object := createResp.GetObject()
		Expect(object).ToNot(BeNil())
		bareMetalInstanceId := object.GetId()
		DeferCleanup(func(ctx context.Context) {
			_, err := privateBareMetalInstancesClient.Delete(ctx, privatev1.BareMetalInstancesDeleteRequest_builder{
				Id: bareMetalInstanceId,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Eventually(func(g Gomega) {
				_, err := privateBareMetalInstancesClient.Get(ctx, privatev1.BareMetalInstancesGetRequest_builder{
					Id: bareMetalInstanceId,
				}.Build())
				g.Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				g.Expect(ok).To(BeTrue())
				g.Expect(status.Code()).To(Equal(grpccodes.NotFound))
			}, 2*time.Minute, time.Second).Should(Succeed())
		})

		// Omitted network_attachments should resolve to one attachment with the
		// tenant default Subnet/SecurityGroup and the first fabric interface.
		Expect(object.GetSpec().GetNetworkAttachments()).To(HaveLen(1),
			"omitted network_attachments should resolve to one default attachment")
		createAttachment := object.GetSpec().GetNetworkAttachments()[0]
		Expect(createAttachment.GetSubnet().GetId()).To(Equal(network.subnetID),
			"default attachment should reference the tenant default Subnet")
		Expect(createAttachment.GetSecurityGroups()).To(HaveLen(1),
			"default attachment should apply exactly one default SecurityGroup")
		Expect(createAttachment.GetSecurityGroups()[0].GetId()).To(Equal(network.securityGroupID),
			"default attachment should reference the tenant default SecurityGroup")
		Expect(createAttachment.GetInterface()).To(Equal("data-0"),
			"default attachment should select the first fabric-role interface")

		// Verify the BareMetalInstance fields via public Get
		getResp, err := bareMetalInstancesClient.Get(ctx, publicv1.BareMetalInstancesGetRequest_builder{
			Id: bareMetalInstanceId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		object = getResp.GetObject()
		metadata := object.GetMetadata()
		Expect(metadata).ToNot(BeNil())
		Expect(metadata.HasCreationTimestamp()).To(BeTrue())
		Expect(metadata.HasDeletionTimestamp()).To(BeFalse())
		Expect(object.GetSpec().GetCatalogItem().GetId()).To(Equal(catalogItemId),
			"BareMetalInstance should persist catalog item reference")
		Expect(object.GetSpec().GetTemplate()).ToNot(BeNil(),
			"spec.template should be materialized from catalog item")
		Expect(object.GetSpec().GetTemplate().GetId()).To(Equal(templateId),
			"materialized template should reference the template from the catalog item")
		Expect(object.GetSpec().GetDiskImage().GetId()).To(Equal(defaultDiskImageId),
			"BareMetalInstance should persist the requested disk image")
		// Resolved default networking should survive public persistence.
		Expect(object.GetSpec().GetNetworkAttachments()).To(HaveLen(1),
			"public Get should expose one default network attachment")
		persistedAttachment := object.GetSpec().GetNetworkAttachments()[0]
		Expect(persistedAttachment.GetSubnet().GetId()).To(Equal(network.subnetID),
			"persisted attachment should reference the tenant default Subnet")
		Expect(persistedAttachment.GetSecurityGroups()).To(HaveLen(1),
			"persisted attachment should apply exactly one default SecurityGroup")
		Expect(persistedAttachment.GetSecurityGroups()[0].GetId()).To(Equal(network.securityGroupID),
			"persisted attachment should reference the tenant default SecurityGroup")
		Expect(persistedAttachment.GetInterface()).To(Equal("data-0"),
			"persisted attachment should select the first fabric-role interface")
	})

	It("waits for automatic ExternalIP cleanup before deleting the BMFO CR", func(ctx context.Context) {
		// Keep an automatically created ExternalIP terminating so this test can
		// observe whether the workload CR is deleted before its child is gone.
		Expect(defaultNetworkingFixture.networkClassID).NotTo(BeEmpty(),
			"the BMI lifecycle fixture should provide a ready NetworkClass on its Hub")

		poolsClient := privatev1.NewExternalIPPoolsClient(tool.InternalView().AdminConn())
		externalIPsClient := privatev1.NewExternalIPsClient(tool.InternalView().AdminConn())
		poolID := fmt.Sprintf("test-pool-%s", uuid.New())
		_, err := poolsClient.Create(ctx, privatev1.ExternalIPPoolsCreateRequest_builder{
			Object: privatev1.ExternalIPPool_builder{
				Id:       poolID,
				Metadata: privatev1.Metadata_builder{Name: poolID}.Build(),
				Spec: privatev1.ExternalIPPoolSpec_builder{
					Cidrs:    []string{uniqueCIDR()},
					IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func(cleanupCtx context.Context) {
			Expect(cleanupExternalIPPoolFixture(cleanupCtx, poolsClient, poolID)).To(Succeed())
		})

		Eventually(func(g Gomega) {
			response, getErr := poolsClient.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: poolID}.Build())
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(response.GetObject().GetStatus().GetState()).To(
				Equal(privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_PENDING))
		}).WithTimeout(time.Minute).WithPolling(time.Second).Should(Succeed())
		poolResponse, err := poolsClient.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: poolID}.Build())
		Expect(err).NotTo(HaveOccurred())
		pool := poolResponse.GetObject()
		pool.GetStatus().SetState(privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY)
		_, err = poolsClient.Update(ctx, privatev1.ExternalIPPoolsUpdateRequest_builder{
			Object:     pool,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		Eventually(func(g Gomega) {
			response, getErr := poolsClient.Get(ctx, privatev1.ExternalIPPoolsGetRequest_builder{Id: poolID}.Build())
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(response.GetObject().GetStatus().GetState()).To(
				Equal(privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY))
		}, time.Minute, time.Second).Should(Succeed())

		createResponse, err := bareMetalInstancesClient.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{
			Object: publicv1.BareMetalInstance_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-bmi-auto-eip-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.BareMetalInstanceSpec_builder{
					CatalogItem:              publicv1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemId}.Build(),
					InstanceType:             publicv1.BareMetalInstanceTypeReference_builder{Id: instanceTypeId, Shared: true}.Build(),
					SshPublicKey:             new(bmiTestSSHPublicKey),
					DiskImage:                publicv1.DiskImageReference_builder{Id: defaultDiskImageId}.Build(),
					AutoExternalIpAttachment: new(true),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		bmiID := createResponse.GetObject().GetId()
		externalIPHeld := false
		externalIPID := ""
		DeferCleanup(func(cleanupCtx context.Context) {
			if bmiID != "" {
				_, deleteErr := privateBareMetalInstancesClient.Delete(cleanupCtx,
					privatev1.BareMetalInstancesDeleteRequest_builder{Id: bmiID}.Build())
				if grpcstatus.Code(deleteErr) != grpccodes.NotFound {
					Expect(deleteErr).NotTo(HaveOccurred())
				}
			}
			if externalIPHeld && externalIPID != "" {
				setExternalIPTestFinalizer(cleanupCtx, externalIPsClient, externalIPID, false)
			}
			if bmiID != "" {
				Eventually(func(g Gomega) {
					_, getErr := privateBareMetalInstancesClient.Get(cleanupCtx,
						privatev1.BareMetalInstancesGetRequest_builder{Id: bmiID}.Build())
					g.Expect(grpcstatus.Code(getErr)).To(Equal(grpccodes.NotFound))
				}, 2*time.Minute, time.Second).Should(Succeed())
			}
		})

		filter := fmt.Sprintf(
			"this.metadata.labels['osac.openshift.io/auto-created-for'] == %q && this.metadata.labels['osac.openshift.io/auto-created'] == 'true'",
			bmiID,
		)
		limit := int32(2)
		var externalIP *privatev1.ExternalIP
		Eventually(func(g Gomega) {
			response, listErr := externalIPsClient.List(ctx, privatev1.ExternalIPsListRequest_builder{
				Filter: &filter,
				Limit:  &limit,
			}.Build())
			g.Expect(listErr).NotTo(HaveOccurred())
			g.Expect(response.GetItems()).To(HaveLen(1))
			externalIP = response.GetItems()[0]
		}, time.Minute, time.Second).Should(Succeed())
		externalIPID = externalIP.GetId()
		setExternalIPTestFinalizer(ctx, externalIPsClient, externalIPID, true)
		externalIPHeld = true

		kubeClient := tool.KubeClient()
		bmiList := &bmfov1alpha1.BareMetalInstanceList{}
		eipList := &osacv1alpha1.ExternalIPList{}
		Eventually(func(g Gomega) {
			g.Expect(kubeClient.List(ctx, eipList, crclient.InNamespace(hubNamespace),
				crclient.MatchingLabels{labels.ExternalIPUuid: externalIPID})).To(Succeed())
			g.Expect(eipList.Items).To(HaveLen(1))
		}, time.Minute, time.Second).Should(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(kubeClient.List(ctx, bmiList, crclient.InNamespace(hubNamespace),
				crclient.MatchingLabels{labels.BareMetalInstanceUuid: bmiID})).To(Succeed())
			g.Expect(bmiList.Items).To(HaveLen(1))
		}, time.Minute, time.Second).Should(Succeed())

		_, err = privateBareMetalInstancesClient.Delete(ctx,
			privatev1.BareMetalInstancesDeleteRequest_builder{Id: bmiID}.Build())
		Expect(err).NotTo(HaveOccurred())
		Eventually(func(g Gomega) {
			response, getErr := privateBareMetalInstancesClient.Get(ctx,
				privatev1.BareMetalInstancesGetRequest_builder{Id: bmiID}.Build())
			g.Expect(getErr).NotTo(HaveOccurred())
			g.Expect(response.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeTrue())
			ipResponse, ipErr := externalIPsClient.Get(ctx,
				privatev1.ExternalIPsGetRequest_builder{Id: externalIPID}.Build())
			g.Expect(ipErr).NotTo(HaveOccurred())
			g.Expect(ipResponse.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeTrue())
			g.Expect(ipResponse.GetObject().GetMetadata().GetFinalizers()).To(ContainElement(externalIPTestFinalizer))
		}, time.Minute, time.Second).Should(Succeed())

		// Wait until the child Kubernetes object reflects deletion, then verify
		// the BMFO CR remains active while its Fulfillment IP is held by a finalizer.
		Eventually(func(g Gomega) {
			g.Expect(kubeClient.List(ctx, eipList, crclient.InNamespace(hubNamespace),
				crclient.MatchingLabels{labels.ExternalIPUuid: externalIPID})).To(Succeed())
			g.Expect(eipList.Items).To(BeEmpty())
		}, time.Minute, time.Second).Should(Succeed())
		Eventually(func(g Gomega) {
			ipResponse, ipErr := externalIPsClient.Get(ctx,
				privatev1.ExternalIPsGetRequest_builder{Id: externalIPID}.Build())
			g.Expect(ipErr).NotTo(HaveOccurred())
			g.Expect(ipResponse.GetObject().GetMetadata().GetFinalizers()).To(ContainElement(externalIPTestFinalizer))
		}, time.Minute, time.Second).Should(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(kubeClient.List(ctx, bmiList, crclient.InNamespace(hubNamespace),
				crclient.MatchingLabels{labels.BareMetalInstanceUuid: bmiID})).To(Succeed())
			g.Expect(bmiList.Items).To(HaveLen(1))
			g.Expect(bmiList.Items[0].GetDeletionTimestamp()).To(BeNil())
		}, time.Minute, time.Second).Should(Succeed())

		setExternalIPTestFinalizer(ctx, externalIPsClient, externalIPID, false)
		externalIPHeld = false
		Eventually(func(g Gomega) {
			_, getErr := externalIPsClient.Get(ctx, privatev1.ExternalIPsGetRequest_builder{Id: externalIPID}.Build())
			g.Expect(grpcstatus.Code(getErr)).To(Equal(grpccodes.NotFound))
		}, 2*time.Minute, time.Second).Should(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(kubeClient.List(ctx, bmiList, crclient.InNamespace(hubNamespace),
				crclient.MatchingLabels{labels.BareMetalInstanceUuid: bmiID})).To(Succeed())
			g.Expect(bmiList.Items).To(BeEmpty())
		}, time.Minute, time.Second).Should(Succeed())
		Eventually(func(g Gomega) {
			_, getErr := privateBareMetalInstancesClient.Get(ctx,
				privatev1.BareMetalInstancesGetRequest_builder{Id: bmiID}.Build())
			g.Expect(grpcstatus.Code(getErr)).To(Equal(grpccodes.NotFound))
		}, 2*time.Minute, time.Second).Should(Succeed())
	})

	It("Rejects BareMetalInstance with non-existent catalog item", func(ctx context.Context) {
		_, err := bareMetalInstancesClient.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{
			Object: publicv1.BareMetalInstance_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-bmi-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.BareMetalInstanceSpec_builder{
					CatalogItem:  publicv1.BareMetalInstanceCatalogItemReference_builder{Id: "non-existent-catalog-item"}.Build(),
					InstanceType: publicv1.BareMetalInstanceTypeReference_builder{Id: instanceTypeId, Shared: true}.Build(),
					SshPublicKey: new(bmiTestSSHPublicKey),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.NotFound))
	})

	It("rejects public updates that change network attachments", func(ctx context.Context) {
		// NetworkClass is a deployment singleton. Reuse the tenant-default subnet
		// and security group instead of creating a second NetworkClass.
		network := getTenantDefaultNetworkFixture(ctx, usersGroup, "")
		createResp, err := bareMetalInstancesClient.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{
			Object: publicv1.BareMetalInstance_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-bmi-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.BareMetalInstanceSpec_builder{
					CatalogItem:        publicv1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemId}.Build(),
					InstanceType:       publicv1.BareMetalInstanceTypeReference_builder{Id: instanceTypeId, Shared: true}.Build(),
					SshPublicKey:       new(bmiTestSSHPublicKey),
					DiskImage:          publicv1.DiskImageReference_builder{Id: defaultDiskImageId}.Build(),
					NetworkAttachments: []*publicv1.BareMetalNetworkAttachment{network.bareMetalInstanceAttachment()},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).NotTo(HaveOccurred())
		object := createResp.GetObject()
		bareMetalInstanceId := object.GetId()
		DeferCleanup(func(ctx context.Context) {
			_, err := privateBareMetalInstancesClient.Delete(ctx, privatev1.BareMetalInstancesDeleteRequest_builder{
				Id: bareMetalInstanceId,
			}.Build())
			Expect(err).NotTo(HaveOccurred())
			Eventually(func(g Gomega) {
				_, err := privateBareMetalInstancesClient.Get(ctx, privatev1.BareMetalInstancesGetRequest_builder{
					Id: bareMetalInstanceId,
				}.Build())
				g.Expect(err).To(HaveOccurred())
				g.Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound))
			}, 2*time.Minute, time.Second).Should(Succeed())
		})

		originalAttachment := proto.Clone(object.GetSpec().GetNetworkAttachments()[0]).(*publicv1.BareMetalNetworkAttachment)
		changed := proto.Clone(originalAttachment).(*publicv1.BareMetalNetworkAttachment)
		changed.SetSecurityGroups(nil)
		candidate := proto.Clone(object).(*publicv1.BareMetalInstance)
		candidate.GetSpec().SetNetworkAttachments([]*publicv1.BareMetalNetworkAttachment{changed})
		mask := catalogItemUpdateMask("spec.network_attachments")
		_, err = bareMetalInstancesClient.Update(ctx, publicv1.BareMetalInstancesUpdateRequest_builder{
			Object: candidate, UpdateMask: mask,
		}.Build())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))

		persisted, err := bareMetalInstancesClient.Get(ctx, publicv1.BareMetalInstancesGetRequest_builder{Id: bareMetalInstanceId}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(proto.Equal(persisted.GetObject().GetSpec().GetNetworkAttachments()[0], originalAttachment)).To(BeTrue())

		_, err = bareMetalInstancesClient.Update(ctx, publicv1.BareMetalInstancesUpdateRequest_builder{
			Object:     persisted.GetObject(),
			UpdateMask: mask,
		}.Build())
		Expect(err).NotTo(HaveOccurred())
	})

	It("Rejects Create with network_attachments when the Subnet's NetworkClass has no fabric_manager", func(ctx context.Context) {
		networkClassesClient := privatev1.NewNetworkClassesClient(tool.InternalView().AdminConn())
		virtualNetworksClient := privatev1.NewVirtualNetworksClient(tool.InternalView().AdminConn())
		subnetsClient := privatev1.NewSubnetsClient(tool.InternalView().AdminConn())
		securityGroupsClient := privatev1.NewSecurityGroupsClient(tool.InternalView().AdminConn())
		// The default fixture is system-managed. Remove it through the controller's
		// default-networking lifecycle before replacing its singleton NetworkClass.
		defaultNetworkingManager, err := newTestDefaultNetworkingManager()
		Expect(err).NotTo(HaveOccurred())
		Expect(deleteTenantDefaultNetworkingFixture(ctx, defaultNetworkingManager, usersGroup)).To(Succeed())
		Expect(defaultNetworkingFixture.networkClassCreated).To(BeTrue(),
			"the k8s-only validation fixture must own the deployment's singleton NetworkClass")
		Expect(cleanupNetworkClassFixture(ctx, networkClassesClient, defaultNetworkingFixture.networkClassID)).To(Succeed())
		listedClasses, err := networkClassesClient.List(ctx, privatev1.NetworkClassesListRequest_builder{
			Filter: new("!has(this.metadata.deletion_timestamp)"),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(listedClasses.GetItems()).To(BeEmpty(), "default fixture cleanup must remove its NetworkClass before replacement")

		// Create a k8s-only NetworkClass (no fabric_manager):
		ncResp, err := networkClassesClient.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
			Object: privatev1.NetworkClass_builder{
				Title:      "Test k8s-only Network Class for BMI",
				K8SManager: new("cudn_localnet"),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		networkClassId := ncResp.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) {
			Expect(cleanupNetworkClassFixture(ctx, networkClassesClient, networkClassId)).To(Succeed())
		})
		waitForNetworkClassReady(ctx, networkClassesClient, networkClassId)

		virtualNetworkId := fmt.Sprintf("test-vnet-%s", uuid.New())
		_, err = virtualNetworksClient.Create(ctx, privatev1.VirtualNetworksCreateRequest_builder{
			Object: privatev1.VirtualNetwork_builder{
				Id: virtualNetworkId,
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("test-vnet-%s", uuid.New()[24:32]),
					Tenant: usersGroup,
				}.Build(),
				Spec: privatev1.VirtualNetworkSpec_builder{
					NetworkClass: privatev1.NetworkClassReference_builder{Id: networkClassId}.Build(),
					Region:       "us-east-1",
					Ipv4Cidr:     new("10.101.0.0/16"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(ctx context.Context) {
			Expect(cleanupVirtualNetworkFixture(ctx, virtualNetworksClient, virtualNetworkId)).To(Succeed())
		})

		// Wait for the VN reconciler to finish initial processing before
		// overriding state, same as other tests that create Subnets.
		Eventually(func(g Gomega) {
			resp, err := virtualNetworksClient.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{
				Id: virtualNetworkId,
			}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetObject().GetStatus().GetState()).To(
				Equal(privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_PENDING))
		}, time.Minute, time.Second).Should(Succeed())

		// Set VirtualNetwork to READY state via private Update API.
		// In IT environment there is no osac-operator/feedback controller to reconcile state.
		vnGetResp, err := virtualNetworksClient.Get(ctx, privatev1.VirtualNetworksGetRequest_builder{
			Id: virtualNetworkId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		vn := vnGetResp.GetObject()
		vn.SetStatus(privatev1.VirtualNetworkStatus_builder{
			State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
		}.Build())
		_, err = virtualNetworksClient.Update(ctx, privatev1.VirtualNetworksUpdateRequest_builder{
			Object:     vn,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		subnetId := fmt.Sprintf("test-subnet-%s", uuid.New())
		_, err = subnetsClient.Create(ctx, privatev1.SubnetsCreateRequest_builder{
			Object: privatev1.Subnet_builder{
				Id: subnetId,
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("test-subnet-%s", uuid.New()[24:32]),
					Tenant: usersGroup,
				}.Build(),
				Spec: privatev1.SubnetSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkId}.Build(),
					Ipv4Cidr:       new("10.101.1.0/24"),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(ctx context.Context) {
			Expect(cleanupSubnetFixture(ctx, subnetsClient, subnetId)).To(Succeed())
		})

		securityGroupID := fmt.Sprintf("test-security-group-%s", uuid.New())
		_, err = securityGroupsClient.Create(ctx, privatev1.SecurityGroupsCreateRequest_builder{
			Object: privatev1.SecurityGroup_builder{
				Id: securityGroupID,
				Metadata: privatev1.Metadata_builder{
					Name:   fmt.Sprintf("test-security-group-%s", uuid.New()[24:32]),
					Tenant: usersGroup,
				}.Build(),
				Spec: privatev1.SecurityGroupSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetworkId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(ctx context.Context) {
			Expect(cleanupSecurityGroupFixture(ctx, securityGroupsClient, securityGroupID)).To(Succeed())
		})
		Eventually(func(g Gomega) {
			resp, err := securityGroupsClient.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: securityGroupID}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetObject().GetStatus().GetState()).To(Equal(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_PENDING))
		}, time.Minute, time.Second).Should(Succeed())
		groupResp, err := securityGroupsClient.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: securityGroupID}.Build())
		Expect(err).ToNot(HaveOccurred())
		group := groupResp.GetObject()
		group.SetStatus(privatev1.SecurityGroupStatus_builder{State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY}.Build())
		_, err = securityGroupsClient.Update(ctx, privatev1.SecurityGroupsUpdateRequest_builder{
			Object:     group,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Eventually(func(g Gomega) {
			resp, err := securityGroupsClient.Get(ctx, privatev1.SecurityGroupsGetRequest_builder{Id: securityGroupID}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(resp.GetObject().GetStatus().GetState()).To(Equal(privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY))
		}, time.Minute, time.Second).Should(Succeed())

		_, err = bareMetalInstancesClient.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{
			Object: publicv1.BareMetalInstance_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-bmi-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.BareMetalInstanceSpec_builder{
					CatalogItem:  publicv1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemId}.Build(),
					InstanceType: publicv1.BareMetalInstanceTypeReference_builder{Id: instanceTypeId, Shared: true}.Build(),
					SshPublicKey: new(bmiTestSSHPublicKey),
					NetworkAttachments: []*publicv1.BareMetalNetworkAttachment{
						publicv1.BareMetalNetworkAttachment_builder{
							Subnet: publicv1.SubnetLocalReference_builder{Id: subnetId}.Build(),
							SecurityGroups: []*publicv1.SecurityGroupLocalReference{
								publicv1.SecurityGroupLocalReference_builder{Id: securityGroupID}.Build(),
							},
						}.Build(),
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
	})

	It("Creates BareMetalInstance with disk_image and persists it", func(ctx context.Context) {
		diResp, err := diskImagesClient.Create(ctx, privatev1.DiskImagesCreateRequest_builder{
			Object: privatev1.DiskImage_builder{
				Metadata: privatev1.Metadata_builder{
					Name: fmt.Sprintf("test-di-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: privatev1.DiskImageSpec_builder{
					SourceType:    privatev1.SourceType_SOURCE_TYPE_REGISTRY,
					SourceRef:     "quay.io/test/rhel9:latest",
					GuestOsFamily: privatev1.GuestOSFamily_GUEST_OS_FAMILY_LINUX,
					Architecture: []privatev1.Architecture{
						privatev1.Architecture_ARCHITECTURE_AMD64,
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		diskImageId := diResp.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) {
			_, err := diskImagesClient.Delete(ctx, privatev1.DiskImagesDeleteRequest_builder{
				Id: diskImageId,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		createResp, err := bareMetalInstancesClient.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{
			Object: publicv1.BareMetalInstance_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-bmi-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.BareMetalInstanceSpec_builder{
					CatalogItem:  publicv1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemId}.Build(),
					InstanceType: publicv1.BareMetalInstanceTypeReference_builder{Id: instanceTypeId, Shared: true}.Build(),
					SshPublicKey: new(bmiTestSSHPublicKey),
					DiskImage:    publicv1.DiskImageReference_builder{Id: diskImageId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		bareMetalInstanceId := createResp.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) {
			_, err := privateBareMetalInstancesClient.Delete(ctx, privatev1.BareMetalInstancesDeleteRequest_builder{
				Id: bareMetalInstanceId,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Eventually(func(g Gomega) {
				_, err := privateBareMetalInstancesClient.Get(ctx, privatev1.BareMetalInstancesGetRequest_builder{
					Id: bareMetalInstanceId,
				}.Build())
				g.Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				g.Expect(ok).To(BeTrue())
				g.Expect(status.Code()).To(Equal(grpccodes.NotFound))
			}, 2*time.Minute, time.Second).Should(Succeed())
		})

		getResp, err := bareMetalInstancesClient.Get(ctx, publicv1.BareMetalInstancesGetRequest_builder{
			Id: bareMetalInstanceId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResp.GetObject().GetSpec().GetDiskImage().GetId()).To(Equal(diskImageId))

		// Wait for the controller to reconcile (state moves from UNSPECIFIED)
		kubeClient := tool.KubeClient()
		Eventually(func(g Gomega) {
			resp, err := privateBareMetalInstancesClient.Get(ctx, privatev1.BareMetalInstancesGetRequest_builder{
				Id: bareMetalInstanceId,
			}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			bmi := resp.GetObject()
			state := bmi.GetStatus().GetState()
			hub := bmi.GetStatus().GetHub()
			finalizers := bmi.GetMetadata().GetFinalizers()
			// Debug: print BMI state on each poll so we can see controller progress
			fmt.Fprintf(GinkgoWriter, "[DEBUG] BMI id=%s state=%s hub=%q finalizers=%v\n",
				bareMetalInstanceId, state, hub, finalizers)
			g.Expect(state).ToNot(
				Equal(privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_UNSPECIFIED),
				"controller should reconcile the BMI and set state")
		}, time.Minute, time.Second).Should(Succeed())

		// Verify the controller creates a BMFO BareMetalInstance CR on the cluster
		bmiList := &bmfov1alpha1.BareMetalInstanceList{}
		var kubeObject *bmfov1alpha1.BareMetalInstance
		Eventually(
			func(g Gomega) {
				err := kubeClient.List(ctx, bmiList, crclient.MatchingLabels{
					labels.BareMetalInstanceUuid: bareMetalInstanceId,
				})
				g.Expect(err).ToNot(HaveOccurred())
				fmt.Fprintf(GinkgoWriter, "[DEBUG] BMFO CR count=%d\n", len(bmiList.Items))
				g.Expect(bmiList.Items).To(HaveLen(1))
				kubeObject = &bmiList.Items[0]
			},
			time.Minute,
			time.Second,
		).Should(Succeed())

		Expect(kubeObject.GetNamespace()).To(Equal(hubNamespace))

		var params map[string]string
		Expect(json.Unmarshal([]byte(kubeObject.Spec.TemplateParameters), &params)).To(Succeed())
		Expect(params).To(HaveKeyWithValue("imageURL", "quay.io/test/rhel9:latest"))

		Expect(kubeObject.Spec.TemplateID).To(Equal(templateId),
			"BMFO CR TemplateID should match the materialized template")
	})

	It("Rejects BareMetalInstance without disk_image when the catalog item provides no default", func(ctx context.Context) {
		_, err := bareMetalInstancesClient.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{
			Object: publicv1.BareMetalInstance_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-bmi-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.BareMetalInstanceSpec_builder{
					CatalogItem:  publicv1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemId}.Build(),
					InstanceType: publicv1.BareMetalInstanceTypeReference_builder{Id: instanceTypeId, Shared: true}.Build(),
					SshPublicKey: new(bmiTestSSHPublicKey),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
	})

	It("Propagates restart_trigger to BMFO CR spec", func(ctx context.Context) {
		createResp, err := bareMetalInstancesClient.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{
			Object: publicv1.BareMetalInstance_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-bmi-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.BareMetalInstanceSpec_builder{
					CatalogItem:  publicv1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemId}.Build(),
					InstanceType: publicv1.BareMetalInstanceTypeReference_builder{Id: instanceTypeId, Shared: true}.Build(),
					SshPublicKey: new(bmiTestSSHPublicKey),
					DiskImage:    publicv1.DiskImageReference_builder{Id: defaultDiskImageId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		bareMetalInstanceId := createResp.GetObject().GetId()
		name := createResp.GetObject().GetMetadata().GetName()
		DeferCleanup(func(ctx context.Context) {
			_, err := privateBareMetalInstancesClient.Delete(ctx, privatev1.BareMetalInstancesDeleteRequest_builder{
				Id: bareMetalInstanceId,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Eventually(func(g Gomega) {
				_, err := privateBareMetalInstancesClient.Get(ctx, privatev1.BareMetalInstancesGetRequest_builder{
					Id: bareMetalInstanceId,
				}.Build())
				g.Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				g.Expect(ok).To(BeTrue())
				g.Expect(status.Code()).To(Equal(grpccodes.NotFound))
			}, 2*time.Minute, time.Second).Should(Succeed())
		})

		// Wait for the controller to reconcile (state moves from UNSPECIFIED)
		Eventually(func(g Gomega) {
			resp, err := privateBareMetalInstancesClient.Get(ctx, privatev1.BareMetalInstancesGetRequest_builder{
				Id: bareMetalInstanceId,
			}.Build())
			g.Expect(err).ToNot(HaveOccurred())
			state := resp.GetObject().GetStatus().GetState()
			fmt.Fprintf(GinkgoWriter, "[DEBUG] BMI id=%s state=%s\n", bareMetalInstanceId, state)
			g.Expect(state).ToNot(
				Equal(privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_UNSPECIFIED),
				"controller should reconcile the BMI and set state")
		}, time.Minute, time.Second).Should(Succeed())

		// Verify the BMFO CR was created with restart_trigger=0
		kubeClient := tool.KubeClient()
		var kubeObject *bmfov1alpha1.BareMetalInstance
		Eventually(func(g Gomega) {
			bmiList := &bmfov1alpha1.BareMetalInstanceList{}
			err := kubeClient.List(ctx, bmiList, crclient.MatchingLabels{
				labels.BareMetalInstanceUuid: bareMetalInstanceId,
			})
			g.Expect(err).ToNot(HaveOccurred())
			fmt.Fprintf(GinkgoWriter, "[DEBUG] BMFO CR count=%d\n", len(bmiList.Items))
			g.Expect(bmiList.Items).To(HaveLen(1))
			kubeObject = &bmiList.Items[0]
		}, time.Minute, time.Second).Should(Succeed())

		Expect(kubeObject.Spec.RestartTrigger).To(Equal(int64(0)),
			"newly created BareMetalInstance CR should have RestartTrigger=0")

		// Update restart_trigger via public API
		_, err = bareMetalInstancesClient.Update(ctx, publicv1.BareMetalInstancesUpdateRequest_builder{
			Object: publicv1.BareMetalInstance_builder{
				Id: bareMetalInstanceId,
				Metadata: publicv1.Metadata_builder{
					Name: name,
				}.Build(),
				Spec: publicv1.BareMetalInstanceSpec_builder{
					CatalogItem:    publicv1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemId}.Build(),
					InstanceType:   publicv1.BareMetalInstanceTypeReference_builder{Id: instanceTypeId, Shared: true}.Build(),
					RestartTrigger: 1,
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{
				Paths: []string{"spec.restart_trigger"},
			},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Wait for the controller to propagate restart_trigger=1 onto the CR
		Eventually(func(g Gomega) {
			bmiList := &bmfov1alpha1.BareMetalInstanceList{}
			err := kubeClient.List(ctx, bmiList, crclient.MatchingLabels{
				labels.BareMetalInstanceUuid: bareMetalInstanceId,
			})
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(bmiList.Items).To(HaveLen(1))
			trigger := bmiList.Items[0].Spec.RestartTrigger
			fmt.Fprintf(GinkgoWriter, "[DEBUG] BMFO CR RestartTrigger=%d\n", trigger)
			g.Expect(trigger).To(Equal(int64(1)),
				"controller should propagate restart_trigger=1 to CR spec")
		}, time.Minute, time.Second).Should(Succeed())
	})

	It("Rejects Create with both catalog_item and template", func(ctx context.Context) {
		_, err := bareMetalInstancesClient.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{
			Object: publicv1.BareMetalInstance_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-bmi-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.BareMetalInstanceSpec_builder{
					CatalogItem: publicv1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemId}.Build(),
					Template:    publicv1.BareMetalInstanceTemplateReference_builder{Id: templateId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(ContainSubstring("mutually exclusive"))
	})

	It("Materializes spec.template from catalog item and rejects template mutation", func(ctx context.Context) {
		createResp, err := bareMetalInstancesClient.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{
			Object: publicv1.BareMetalInstance_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-bmi-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.BareMetalInstanceSpec_builder{
					CatalogItem:  publicv1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemId}.Build(),
					SshPublicKey: new(bmiTestSSHPublicKey),
					DiskImage:    publicv1.DiskImageReference_builder{Id: defaultDiskImageId}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		bareMetalInstanceId := createResp.GetObject().GetId()
		name := createResp.GetObject().GetMetadata().GetName()
		DeferCleanup(func(ctx context.Context) {
			_, err := privateBareMetalInstancesClient.Delete(ctx, privatev1.BareMetalInstancesDeleteRequest_builder{
				Id: bareMetalInstanceId,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Eventually(func(g Gomega) {
				_, err := privateBareMetalInstancesClient.Get(ctx, privatev1.BareMetalInstancesGetRequest_builder{
					Id: bareMetalInstanceId,
				}.Build())
				g.Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				g.Expect(ok).To(BeTrue())
				g.Expect(status.Code()).To(Equal(grpccodes.NotFound))
			}, 2*time.Minute, time.Second).Should(Succeed())
		})

		// Verify spec.template is materialized from the catalog item at write time
		getResp, err := bareMetalInstancesClient.Get(ctx, publicv1.BareMetalInstancesGetRequest_builder{
			Id: bareMetalInstanceId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		spec := getResp.GetObject().GetSpec()
		Expect(spec.GetCatalogItem().GetId()).To(Equal(catalogItemId),
			"catalog item reference should be persisted")
		Expect(spec.GetTemplate()).ToNot(BeNil(),
			"spec.template should be materialized from catalog item at write time")
		Expect(spec.GetTemplate().GetId()).To(Equal(templateId),
			"materialized template should reference the template from the catalog item")

		// Create a second template so the reference validation interceptor passes,
		// then verify the server's immutability check catches the attempted change.
		otherTemplateResp, err := bareMetalInstanceTemplatesClient.Create(ctx,
			privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
				Object: privatev1.BareMetalInstanceTemplate_builder{
					Id:    fmt.Sprintf("test_other_%s", strings.ReplaceAll(uuid.New(), "-", "_")),
					Title: "Other template for immutability test",
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-other-%s", uuid.New()[24:32]),
					}.Build(),
				}.Build(),
			}.Build())
		Expect(err).ToNot(HaveOccurred())
		otherTemplateId := otherTemplateResp.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) {
			_, err := bareMetalInstanceTemplatesClient.Delete(ctx,
				privatev1.BareMetalInstanceTemplatesDeleteRequest_builder{Id: otherTemplateId}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		// Verify spec.template is immutable: update with a different (real) template ID must be rejected
		_, err = privateBareMetalInstancesClient.Update(ctx, privatev1.BareMetalInstancesUpdateRequest_builder{
			Object: privatev1.BareMetalInstance_builder{
				Id: bareMetalInstanceId,
				Metadata: privatev1.Metadata_builder{
					Name: name,
				}.Build(),
				Spec: privatev1.BareMetalInstanceSpec_builder{
					Template: privatev1.BareMetalInstanceTemplateReference_builder{Id: otherTemplateId}.Build(),
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{
				Paths: []string{"spec.template"},
			},
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(ContainSubstring("template is immutable"))
	})

	It("Resolves a shared catalog item by name and persists its materialized Template", func(ctx context.Context) {
		// Provider-created catalog items default to shared; name lookup must select that scope.
		catName := fmt.Sprintf("test-named-cat-%s", uuid.New()[24:32])
		catResp, err := bareMetalInstanceCatalogItemsClient.Create(ctx,
			privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Metadata:  privatev1.Metadata_builder{Name: catName}.Build(),
					Template:  privatev1.BareMetalInstanceTemplateReference_builder{Id: templateId}.Build(),
					Published: true,
				}.Build(),
			}.Build())
		Expect(err).ToNot(HaveOccurred())
		namedCatId := catResp.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) {
			_, err := bareMetalInstanceCatalogItemsClient.Delete(ctx,
				privatev1.BareMetalInstanceCatalogItemsDeleteRequest_builder{Id: namedCatId}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		// The handler resolves the shared name and persists the canonical reference.
		createResp, err := bareMetalInstancesClient.Create(ctx,
			publicv1.BareMetalInstancesCreateRequest_builder{
				Object: publicv1.BareMetalInstance_builder{
					Metadata: publicv1.Metadata_builder{
						Name: fmt.Sprintf("test-bmi-%s", uuid.New()[24:32]),
					}.Build(),
					Spec: publicv1.BareMetalInstanceSpec_builder{
						CatalogItem: publicv1.BareMetalInstanceCatalogItemReference_builder{
							Name:   catName,
							Shared: true,
						}.Build(),
						SshPublicKey: new(bmiTestSSHPublicKey),
						DiskImage:    publicv1.DiskImageReference_builder{Id: defaultDiskImageId}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
		Expect(err).ToNot(HaveOccurred())
		bareMetalInstanceId := createResp.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) {
			_, err := privateBareMetalInstancesClient.Delete(ctx,
				privatev1.BareMetalInstancesDeleteRequest_builder{Id: bareMetalInstanceId}.Build())
			Expect(err).ToNot(HaveOccurred())
			Eventually(func(g Gomega) {
				_, err := privateBareMetalInstancesClient.Get(ctx,
					privatev1.BareMetalInstancesGetRequest_builder{Id: bareMetalInstanceId}.Build())
				g.Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				g.Expect(ok).To(BeTrue())
				g.Expect(status.Code()).To(Equal(grpccodes.NotFound))
			}, 2*time.Minute, time.Second).Should(Succeed())
		})

		getResp, err := bareMetalInstancesClient.Get(ctx,
			publicv1.BareMetalInstancesGetRequest_builder{Id: bareMetalInstanceId}.Build())
		Expect(err).ToNot(HaveOccurred())
		object := getResp.GetObject()
		Expect(object.GetSpec().GetCatalogItem().GetId()).To(Equal(namedCatId),
			"persisted catalog item reference should contain the resolved ID")
		Expect(object.GetSpec().GetCatalogItem().GetName()).To(Equal(catName),
			"persisted catalog item reference should preserve the name")
		Expect(object.GetSpec().GetCatalogItem().GetShared()).To(BeTrue(),
			"persisted catalog item reference should preserve shared scope")
		Expect(object.GetSpec().GetTemplate()).ToNot(BeNil(),
			"spec.template should be materialized when catalog item is referenced by name")
		Expect(object.GetSpec().GetTemplate().GetId()).To(Equal(templateId),
			"materialized template should reference the template from the named catalog item")
	})

	Context("Direct template path", func() {
		var directTemplateId string

		BeforeEach(func(ctx context.Context) {
			// Create a template with a BMFO-CRD-compatible ID (must match ^[a-zA-Z_][a-zA-Z0-9._]*$)
			templateResp, err := bareMetalInstanceTemplatesClient.Create(ctx,
				privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
					Object: privatev1.BareMetalInstanceTemplate_builder{
						Id:          fmt.Sprintf("test_direct_%s", strings.ReplaceAll(uuid.New(), "-", "_")),
						Title:       "Direct Template Test",
						Description: "Template for direct-template provisioning tests.",
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-direct-%s", uuid.New()[24:32]),
						}.Build(),
					}.Build(),
				}.Build())
			Expect(err).ToNot(HaveOccurred())
			directTemplateId = templateResp.GetObject().GetId()
			DeferCleanup(func(ctx context.Context) {
				_, err := bareMetalInstanceTemplatesClient.Delete(ctx,
					privatev1.BareMetalInstanceTemplatesDeleteRequest_builder{
						Id: directTemplateId,
					}.Build())
				Expect(err).ToNot(HaveOccurred())
			})
		})

		It("Creates BareMetalInstance with direct template and verifies BMFO CR", func(ctx context.Context) {
			diResp, err := diskImagesClient.Create(ctx, privatev1.DiskImagesCreateRequest_builder{
				Object: privatev1.DiskImage_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-di-%s", uuid.New()[24:32]),
					}.Build(),
					Spec: privatev1.DiskImageSpec_builder{
						SourceType:    privatev1.SourceType_SOURCE_TYPE_REGISTRY,
						SourceRef:     "quay.io/test/rhel9:latest",
						GuestOsFamily: privatev1.GuestOSFamily_GUEST_OS_FAMILY_LINUX,
						Architecture: []privatev1.Architecture{
							privatev1.Architecture_ARCHITECTURE_AMD64,
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			diskImageId := diResp.GetObject().GetId()
			DeferCleanup(func(ctx context.Context) {
				_, err := diskImagesClient.Delete(ctx, privatev1.DiskImagesDeleteRequest_builder{
					Id: diskImageId,
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})

			createResp, err := bareMetalInstancesClient.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{
				Object: publicv1.BareMetalInstance_builder{
					Metadata: publicv1.Metadata_builder{
						Name: fmt.Sprintf("test-bmi-%s", uuid.New()[24:32]),
					}.Build(),
					Spec: publicv1.BareMetalInstanceSpec_builder{
						Template:     publicv1.BareMetalInstanceTemplateReference_builder{Id: directTemplateId}.Build(),
						InstanceType: publicv1.BareMetalInstanceTypeReference_builder{Id: instanceTypeId, Shared: true}.Build(),
						SshPublicKey: new(bmiTestSSHPublicKey),
						DiskImage:    publicv1.DiskImageReference_builder{Id: diskImageId}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			bareMetalInstanceId := createResp.GetObject().GetId()
			DeferCleanup(func(ctx context.Context) {
				_, err := privateBareMetalInstancesClient.Delete(ctx, privatev1.BareMetalInstancesDeleteRequest_builder{
					Id: bareMetalInstanceId,
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Eventually(func(g Gomega) {
					_, err := privateBareMetalInstancesClient.Get(ctx, privatev1.BareMetalInstancesGetRequest_builder{
						Id: bareMetalInstanceId,
					}.Build())
					g.Expect(err).To(HaveOccurred())
					status, ok := grpcstatus.FromError(err)
					g.Expect(ok).To(BeTrue())
					g.Expect(status.Code()).To(Equal(grpccodes.NotFound))
				}, 2*time.Minute, time.Second).Should(Succeed())
			})

			// Verify API response fields
			getResp, err := bareMetalInstancesClient.Get(ctx, publicv1.BareMetalInstancesGetRequest_builder{
				Id: bareMetalInstanceId,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			spec := getResp.GetObject().GetSpec()
			Expect(spec.GetTemplate().GetId()).To(Equal(directTemplateId),
				"spec.template should be persisted from the direct-template path")
			Expect(spec.HasCatalogItem()).To(BeFalse(),
				"spec.catalog_item should be absent when created via direct template")

			// Wait for the controller to reconcile (state moves from UNSPECIFIED)
			kubeClient := tool.KubeClient()
			Eventually(func(g Gomega) {
				resp, err := privateBareMetalInstancesClient.Get(ctx, privatev1.BareMetalInstancesGetRequest_builder{
					Id: bareMetalInstanceId,
				}.Build())
				g.Expect(err).ToNot(HaveOccurred())
				state := resp.GetObject().GetStatus().GetState()
				fmt.Fprintf(GinkgoWriter, "[DEBUG] BMI id=%s state=%s\n", bareMetalInstanceId, state)
				g.Expect(state).ToNot(
					Equal(privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_UNSPECIFIED),
					"controller should reconcile the BMI and set state")
			}, time.Minute, time.Second).Should(Succeed())

			// Verify the controller creates a BMFO BareMetalInstance CR with the correct TemplateID
			var kubeObject *bmfov1alpha1.BareMetalInstance
			Eventually(func(g Gomega) {
				bmiList := &bmfov1alpha1.BareMetalInstanceList{}
				err := kubeClient.List(ctx, bmiList, crclient.MatchingLabels{
					labels.BareMetalInstanceUuid: bareMetalInstanceId,
				})
				g.Expect(err).ToNot(HaveOccurred())
				fmt.Fprintf(GinkgoWriter, "[DEBUG] BMFO CR count=%d\n", len(bmiList.Items))
				g.Expect(bmiList.Items).To(HaveLen(1))
				kubeObject = &bmiList.Items[0]
			}, time.Minute, time.Second).Should(Succeed())

			Expect(kubeObject.Spec.TemplateID).To(Equal(directTemplateId),
				"BMFO CR TemplateID should match spec.template from the direct-template path")
			Expect(kubeObject.GetNamespace()).To(Equal(hubNamespace))

			var params map[string]string
			Expect(json.Unmarshal([]byte(kubeObject.Spec.TemplateParameters), &params)).To(Succeed())
			Expect(params).To(HaveKeyWithValue("imageURL", "quay.io/test/rhel9:latest"))
		})

		It("Rejects direct template that does not exist", func(ctx context.Context) {
			// Creation-source lookup belongs to the handler and reports missing Templates as NotFound.
			_, err := bareMetalInstancesClient.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{
				Object: publicv1.BareMetalInstance_builder{
					Metadata: publicv1.Metadata_builder{
						Name: fmt.Sprintf("test-bmi-%s", uuid.New()[24:32]),
					}.Build(),
					Spec: publicv1.BareMetalInstanceSpec_builder{
						Template: publicv1.BareMetalInstanceTemplateReference_builder{Id: "nonexistent_template"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.NotFound))
			Expect(status.Message()).To(ContainSubstring("not found"))
		})
	})
})
