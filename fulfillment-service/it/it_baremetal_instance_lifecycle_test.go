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
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	bmfov1alpha1 "github.com/osac-project/osac/bare-metal-fulfillment-operator/api/v1alpha1"
	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/kubernetes/labels"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
)

var _ = Describe("BareMetalInstance lifecycle", func() {
	var (
		bareMetalInstancesClient            publicv1.BareMetalInstancesClient
		privateBareMetalInstancesClient     privatev1.BareMetalInstancesClient
		bareMetalInstanceTemplatesClient    privatev1.BareMetalInstanceTemplatesClient
		bareMetalInstanceCatalogItemsClient privatev1.BareMetalInstanceCatalogItemsClient
		templateId                          string
		catalogItemId                       string
	)

	BeforeEach(func(ctx context.Context) {
		// Create clients
		bareMetalInstancesClient = publicv1.NewBareMetalInstancesClient(tool.ExternalView().UserConn())
		privateBareMetalInstancesClient = privatev1.NewBareMetalInstancesClient(tool.InternalView().AdminConn())
		bareMetalInstanceTemplatesClient = privatev1.NewBareMetalInstanceTemplatesClient(tool.InternalView().AdminConn())
		bareMetalInstanceCatalogItemsClient = privatev1.NewBareMetalInstanceCatalogItemsClient(tool.InternalView().AdminConn())

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

		// Create BareMetalInstanceCatalogItem (must be published for public API access)
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

	It("Creates a BareMetalInstance and verifies fields", func(ctx context.Context) {
		// Create BareMetalInstance via public API
		createResp, err := bareMetalInstancesClient.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{
			Object: publicv1.BareMetalInstance_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-bmi-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.BareMetalInstanceSpec_builder{
					CatalogItem: publicv1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemId}.Build(),
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

		// Set BareMetalInstance to RUNNING state via private Update API
		bmiGetResp, err := privateBareMetalInstancesClient.Get(ctx, privatev1.BareMetalInstancesGetRequest_builder{
			Id: bareMetalInstanceId,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		bmi := bmiGetResp.GetObject()
		bmi.SetStatus(privatev1.BareMetalInstanceStatus_builder{
			State: privatev1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_RUNNING,
		}.Build())
		_, err = privateBareMetalInstancesClient.Update(ctx, privatev1.BareMetalInstancesUpdateRequest_builder{
			Object:     bmi,
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

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
		Expect(object.GetStatus().GetState()).To(
			Equal(publicv1.BareMetalInstanceState_BARE_METAL_INSTANCE_STATE_RUNNING),
			"BareMetalInstance should be in RUNNING state after status override")
	})

	It("Rejects BareMetalInstance with non-existent catalog item", func(ctx context.Context) {
		_, err := bareMetalInstancesClient.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{
			Object: publicv1.BareMetalInstance_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-bmi-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.BareMetalInstanceSpec_builder{
					CatalogItem: publicv1.BareMetalInstanceCatalogItemReference_builder{Id: "non-existent-catalog-item"}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
	})

	It("Rejects Create with network_attachments when the Subnet's NetworkClass has no fabric_manager", func(ctx context.Context) {
		networkClassesClient := privatev1.NewNetworkClassesClient(tool.InternalView().AdminConn())
		virtualNetworksClient := privatev1.NewVirtualNetworksClient(tool.InternalView().AdminConn())
		subnetsClient := privatev1.NewSubnetsClient(tool.InternalView().AdminConn())
		// Create a k8s-only NetworkClass (no fabric_manager):
		ncResp, err := networkClassesClient.Create(ctx, privatev1.NetworkClassesCreateRequest_builder{
			Object: privatev1.NetworkClass_builder{
				Title:                  "Test k8s-only Network Class for BMI",
				ImplementationStrategy: "cudn",
				K8SManager:             new("cudn_localnet"),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		networkClassId := ncResp.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) {
			_, err := networkClassesClient.Delete(ctx, privatev1.NetworkClassesDeleteRequest_builder{
				Id: networkClassId,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

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
			_, err := virtualNetworksClient.Delete(ctx, privatev1.VirtualNetworksDeleteRequest_builder{
				Id: virtualNetworkId,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
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
			_, err := subnetsClient.Delete(ctx, privatev1.SubnetsDeleteRequest_builder{
				Id: subnetId,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		_, err = bareMetalInstancesClient.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{
			Object: publicv1.BareMetalInstance_builder{
				Spec: publicv1.BareMetalInstanceSpec_builder{
					CatalogItem: publicv1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemId}.Build(),
					NetworkAttachments: []*publicv1.BareMetalNetworkAttachment{
						publicv1.BareMetalNetworkAttachment_builder{
							Subnet: publicv1.SubnetLocalReference_builder{Id: subnetId}.Build(),
						}.Build(),
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
	})

	It("Creates BareMetalInstance with image and persists it", func(ctx context.Context) {
		createResp, err := bareMetalInstancesClient.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{
			Object: publicv1.BareMetalInstance_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-bmi-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.BareMetalInstanceSpec_builder{
					CatalogItem: publicv1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemId}.Build(),
					Image: publicv1.BareMetalInstanceImage_builder{
						SourceType: "registry",
						SourceRef:  "quay.io/test/rhel9:latest",
					}.Build(),
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
		image := getResp.GetObject().GetSpec().GetImage()
		Expect(image).ToNot(BeNil())
		Expect(image.GetSourceType()).To(Equal("registry"))
		Expect(image.GetSourceRef()).To(Equal("quay.io/test/rhel9:latest"))

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
	})

	It("Creates BareMetalInstance without image when no template default", func(ctx context.Context) {
		createResp, err := bareMetalInstancesClient.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{
			Object: publicv1.BareMetalInstance_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-bmi-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.BareMetalInstanceSpec_builder{
					CatalogItem: publicv1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemId}.Build(),
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
		Expect(getResp.GetObject().GetSpec().HasImage()).To(BeFalse(),
			"BareMetalInstance created without image should have no image set")
	})

	It("Applies template spec_defaults image when user omits image", func(ctx context.Context) {
		imageTemplateResp, err := bareMetalInstanceTemplatesClient.Create(ctx,
			privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
				Object: privatev1.BareMetalInstanceTemplate_builder{
					Id:          fmt.Sprintf("test_image_default_%s", strings.ReplaceAll(uuid.New(), "-", "_")),
					Title:       "Template with image default",
					Description: "Template that provides a default image via spec_defaults.",
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-template-%s", uuid.New()[24:32]),
					}.Build(),
					SpecDefaults: privatev1.BareMetalInstanceTemplateSpecDefaults_builder{
						Image: privatev1.BareMetalInstanceImage_builder{
							SourceType: "registry",
							SourceRef:  "quay.io/default/os:latest",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
		Expect(err).ToNot(HaveOccurred())
		imageTemplateId := imageTemplateResp.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) {
			_, err := bareMetalInstanceTemplatesClient.Delete(ctx, privatev1.BareMetalInstanceTemplatesDeleteRequest_builder{
				Id: imageTemplateId,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		imageCatalogResp, err := bareMetalInstanceCatalogItemsClient.Create(ctx,
			privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-catalog-item-%s", uuid.New()[24:32]),
					}.Build(),
					Title:     "Catalog item with image default template",
					Template:  privatev1.BareMetalInstanceTemplateReference_builder{Id: imageTemplateId}.Build(),
					Published: true,
				}.Build(),
			}.Build())
		Expect(err).ToNot(HaveOccurred())
		imageCatalogItemId := imageCatalogResp.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) {
			_, err := bareMetalInstanceCatalogItemsClient.Delete(ctx,
				privatev1.BareMetalInstanceCatalogItemsDeleteRequest_builder{
					Id: imageCatalogItemId,
				}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		createResp, err := bareMetalInstancesClient.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{
			Object: publicv1.BareMetalInstance_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-bmi-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.BareMetalInstanceSpec_builder{
					CatalogItem: publicv1.BareMetalInstanceCatalogItemReference_builder{Id: imageCatalogItemId}.Build(),
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
		image := getResp.GetObject().GetSpec().GetImage()
		Expect(image).ToNot(BeNil())
		Expect(image.GetSourceType()).To(Equal("registry"),
			"Template default source_type should be applied")
		Expect(image.GetSourceRef()).To(Equal("quay.io/default/os:latest"),
			"Template default source_ref should be applied")
	})

	It("User-provided image overrides template spec_defaults image", func(ctx context.Context) {
		imageTemplateResp, err := bareMetalInstanceTemplatesClient.Create(ctx,
			privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
				Object: privatev1.BareMetalInstanceTemplate_builder{
					Id:          fmt.Sprintf("test_image_override_%s", strings.ReplaceAll(uuid.New(), "-", "_")),
					Title:       "Template with overridable image default",
					Description: "Template whose image default should be overridden by user.",
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-template-%s", uuid.New()[24:32]),
					}.Build(),
					SpecDefaults: privatev1.BareMetalInstanceTemplateSpecDefaults_builder{
						Image: privatev1.BareMetalInstanceImage_builder{
							SourceType: "registry",
							SourceRef:  "quay.io/default/os:latest",
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
		Expect(err).ToNot(HaveOccurred())
		imageTemplateId := imageTemplateResp.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) {
			_, err := bareMetalInstanceTemplatesClient.Delete(ctx, privatev1.BareMetalInstanceTemplatesDeleteRequest_builder{
				Id: imageTemplateId,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		imageCatalogResp, err := bareMetalInstanceCatalogItemsClient.Create(ctx,
			privatev1.BareMetalInstanceCatalogItemsCreateRequest_builder{
				Object: privatev1.BareMetalInstanceCatalogItem_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-catalog-item-%s", uuid.New()[24:32]),
					}.Build(),
					Title:     "Catalog item for image override test",
					Template:  privatev1.BareMetalInstanceTemplateReference_builder{Id: imageTemplateId}.Build(),
					Published: true,
				}.Build(),
			}.Build())
		Expect(err).ToNot(HaveOccurred())
		imageCatalogItemId := imageCatalogResp.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) {
			_, err := bareMetalInstanceCatalogItemsClient.Delete(ctx,
				privatev1.BareMetalInstanceCatalogItemsDeleteRequest_builder{
					Id: imageCatalogItemId,
				}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		createResp, err := bareMetalInstancesClient.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{
			Object: publicv1.BareMetalInstance_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-bmi-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.BareMetalInstanceSpec_builder{
					CatalogItem: publicv1.BareMetalInstanceCatalogItemReference_builder{Id: imageCatalogItemId}.Build(),
					Image: publicv1.BareMetalInstanceImage_builder{
						SourceType: "registry",
						SourceRef:  "quay.io/user/custom:v2",
					}.Build(),
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
		image := getResp.GetObject().GetSpec().GetImage()
		Expect(image).ToNot(BeNil())
		Expect(image.GetSourceType()).To(Equal("registry"))
		Expect(image.GetSourceRef()).To(Equal("quay.io/user/custom:v2"),
			"User-provided image should override template default")
	})

	It("Rejects image with missing source_type", func(ctx context.Context) {
		_, err := bareMetalInstancesClient.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{
			Object: publicv1.BareMetalInstance_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-bmi-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.BareMetalInstanceSpec_builder{
					CatalogItem: publicv1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemId}.Build(),
					Image: publicv1.BareMetalInstanceImage_builder{
						SourceRef: "quay.io/test:latest",
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(ContainSubstring("image.source_type"))
	})

	It("Rejects image with missing source_ref", func(ctx context.Context) {
		_, err := bareMetalInstancesClient.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{
			Object: publicv1.BareMetalInstance_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-bmi-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.BareMetalInstanceSpec_builder{
					CatalogItem: publicv1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemId}.Build(),
					Image: publicv1.BareMetalInstanceImage_builder{
						SourceType: "registry",
					}.Build(),
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(ContainSubstring("image.source_ref"))
	})

	It("Rejects update that changes image", func(ctx context.Context) {
		createResp, err := bareMetalInstancesClient.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{
			Object: publicv1.BareMetalInstance_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-bmi-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.BareMetalInstanceSpec_builder{
					CatalogItem: publicv1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemId}.Build(),
					Image: publicv1.BareMetalInstanceImage_builder{
						SourceType: "registry",
						SourceRef:  "quay.io/test:latest",
					}.Build(),
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

		_, err = privateBareMetalInstancesClient.Update(ctx, privatev1.BareMetalInstancesUpdateRequest_builder{
			Object: privatev1.BareMetalInstance_builder{
				Id: bareMetalInstanceId,
				Metadata: privatev1.Metadata_builder{
					Name: name,
				}.Build(),
				Spec: privatev1.BareMetalInstanceSpec_builder{
					CatalogItem: privatev1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemId}.Build(),
					Image: privatev1.BareMetalInstanceImage_builder{
						SourceType: "registry",
						SourceRef:  "quay.io/other:latest",
					}.Build(),
				}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{
				Paths: []string{"spec.image"},
			},
		}.Build())
		Expect(err).To(HaveOccurred())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		Expect(status.Message()).To(ContainSubstring("image is immutable"))
	})

	It("Propagates restart_trigger to BMFO CR spec", func(ctx context.Context) {
		createResp, err := bareMetalInstancesClient.Create(ctx, publicv1.BareMetalInstancesCreateRequest_builder{
			Object: publicv1.BareMetalInstance_builder{
				Metadata: publicv1.Metadata_builder{
					Name: fmt.Sprintf("test-bmi-%s", uuid.New()[24:32]),
				}.Build(),
				Spec: publicv1.BareMetalInstanceSpec_builder{
					CatalogItem: publicv1.BareMetalInstanceCatalogItemReference_builder{Id: catalogItemId}.Build(),
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
})
