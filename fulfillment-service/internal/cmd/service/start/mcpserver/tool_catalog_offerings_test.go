package mcpserver

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

const catalogOfferingTokenFixture = "catalog-offering-test-token"

type mockComputeInstanceCatalogOfferingsClient struct {
	publicv1.ComputeInstanceCatalogItemsClient
	createFunc func(context.Context, *publicv1.ComputeInstanceCatalogItemsCreateRequest, ...grpc.CallOption) (*publicv1.ComputeInstanceCatalogItemsCreateResponse, error)
	updateFunc func(context.Context, *publicv1.ComputeInstanceCatalogItemsUpdateRequest, ...grpc.CallOption) (*publicv1.ComputeInstanceCatalogItemsUpdateResponse, error)
	deleteFunc func(context.Context, *publicv1.ComputeInstanceCatalogItemsDeleteRequest, ...grpc.CallOption) (*publicv1.ComputeInstanceCatalogItemsDeleteResponse, error)
}

func (m *mockComputeInstanceCatalogOfferingsClient) Create(
	ctx context.Context, request *publicv1.ComputeInstanceCatalogItemsCreateRequest, options ...grpc.CallOption,
) (*publicv1.ComputeInstanceCatalogItemsCreateResponse, error) {
	return m.createFunc(ctx, request, options...)
}

func (m *mockComputeInstanceCatalogOfferingsClient) Update(
	ctx context.Context, request *publicv1.ComputeInstanceCatalogItemsUpdateRequest, options ...grpc.CallOption,
) (*publicv1.ComputeInstanceCatalogItemsUpdateResponse, error) {
	return m.updateFunc(ctx, request, options...)
}

func (m *mockComputeInstanceCatalogOfferingsClient) Delete(
	ctx context.Context, request *publicv1.ComputeInstanceCatalogItemsDeleteRequest, options ...grpc.CallOption,
) (*publicv1.ComputeInstanceCatalogItemsDeleteResponse, error) {
	return m.deleteFunc(ctx, request, options...)
}

type mockClusterCatalogOfferingsClient struct {
	publicv1.ClusterCatalogItemsClient
	createFunc func(context.Context, *publicv1.ClusterCatalogItemsCreateRequest, ...grpc.CallOption) (*publicv1.ClusterCatalogItemsCreateResponse, error)
	updateFunc func(context.Context, *publicv1.ClusterCatalogItemsUpdateRequest, ...grpc.CallOption) (*publicv1.ClusterCatalogItemsUpdateResponse, error)
	deleteFunc func(context.Context, *publicv1.ClusterCatalogItemsDeleteRequest, ...grpc.CallOption) (*publicv1.ClusterCatalogItemsDeleteResponse, error)
}

func (m *mockClusterCatalogOfferingsClient) Create(
	ctx context.Context, request *publicv1.ClusterCatalogItemsCreateRequest, options ...grpc.CallOption,
) (*publicv1.ClusterCatalogItemsCreateResponse, error) {
	return m.createFunc(ctx, request, options...)
}

func (m *mockClusterCatalogOfferingsClient) Update(
	ctx context.Context, request *publicv1.ClusterCatalogItemsUpdateRequest, options ...grpc.CallOption,
) (*publicv1.ClusterCatalogItemsUpdateResponse, error) {
	return m.updateFunc(ctx, request, options...)
}

func (m *mockClusterCatalogOfferingsClient) Delete(
	ctx context.Context, request *publicv1.ClusterCatalogItemsDeleteRequest, options ...grpc.CallOption,
) (*publicv1.ClusterCatalogItemsDeleteResponse, error) {
	return m.deleteFunc(ctx, request, options...)
}

type mockBareMetalCatalogOfferingsClient struct {
	publicv1.BareMetalInstanceCatalogItemsClient
	createFunc func(context.Context, *publicv1.BareMetalInstanceCatalogItemsCreateRequest, ...grpc.CallOption) (*publicv1.BareMetalInstanceCatalogItemsCreateResponse, error)
	updateFunc func(context.Context, *publicv1.BareMetalInstanceCatalogItemsUpdateRequest, ...grpc.CallOption) (*publicv1.BareMetalInstanceCatalogItemsUpdateResponse, error)
	deleteFunc func(context.Context, *publicv1.BareMetalInstanceCatalogItemsDeleteRequest, ...grpc.CallOption) (*publicv1.BareMetalInstanceCatalogItemsDeleteResponse, error)
}

func (m *mockBareMetalCatalogOfferingsClient) Create(
	ctx context.Context, request *publicv1.BareMetalInstanceCatalogItemsCreateRequest, options ...grpc.CallOption,
) (*publicv1.BareMetalInstanceCatalogItemsCreateResponse, error) {
	return m.createFunc(ctx, request, options...)
}

func (m *mockBareMetalCatalogOfferingsClient) Update(
	ctx context.Context, request *publicv1.BareMetalInstanceCatalogItemsUpdateRequest, options ...grpc.CallOption,
) (*publicv1.BareMetalInstanceCatalogItemsUpdateResponse, error) {
	return m.updateFunc(ctx, request, options...)
}

func (m *mockBareMetalCatalogOfferingsClient) Delete(
	ctx context.Context, request *publicv1.BareMetalInstanceCatalogItemsDeleteRequest, options ...grpc.CallOption,
) (*publicv1.BareMetalInstanceCatalogItemsDeleteResponse, error) {
	return m.deleteFunc(ctx, request, options...)
}

type catalogOfferingDispatchRecorder struct {
	createCalled bool
	updateCalled bool
	deleteCalled bool
	token        string
}

func catalogOfferingDispatchClients(
	kind CatalogOfferingKind, publish bool,
) (catalogOfferingClients, *catalogOfferingDispatchRecorder) {
	recorder := &catalogOfferingDispatchRecorder{}
	switch kind {
	case CatalogOfferingKindComputeInstance:
		computeItems := &mockComputeInstanceCatalogOfferingsClient{
			createFunc: func(
				ctx context.Context,
				_ *publicv1.ComputeInstanceCatalogItemsCreateRequest,
				_ ...grpc.CallOption,
			) (*publicv1.ComputeInstanceCatalogItemsCreateResponse, error) {
				recorder.createCalled = true
				recorder.token = forwardedToken(ctx)
				return publicv1.ComputeInstanceCatalogItemsCreateResponse_builder{
					Object: publicv1.ComputeInstanceCatalogItem_builder{
						Id:       "compute-offering-1",
						Metadata: publicv1.Metadata_builder{Name: "offering", Version: 1}.Build(),
					}.Build(),
				}.Build(), nil
			},
			updateFunc: func(
				ctx context.Context,
				_ *publicv1.ComputeInstanceCatalogItemsUpdateRequest,
				_ ...grpc.CallOption,
			) (*publicv1.ComputeInstanceCatalogItemsUpdateResponse, error) {
				recorder.updateCalled = true
				recorder.token = forwardedToken(ctx)
				return publicv1.ComputeInstanceCatalogItemsUpdateResponse_builder{
					Object: publicv1.ComputeInstanceCatalogItem_builder{
						Id:        "compute-offering-1",
						Metadata:  publicv1.Metadata_builder{Name: "offering", Version: 8}.Build(),
						Published: publish,
					}.Build(),
				}.Build(), nil
			},
			deleteFunc: func(
				ctx context.Context,
				_ *publicv1.ComputeInstanceCatalogItemsDeleteRequest,
				_ ...grpc.CallOption,
			) (*publicv1.ComputeInstanceCatalogItemsDeleteResponse, error) {
				recorder.deleteCalled = true
				recorder.token = forwardedToken(ctx)
				return publicv1.ComputeInstanceCatalogItemsDeleteResponse_builder{}.Build(), nil
			},
		}
		return catalogOfferingClients{computeInstances: computeItems}, recorder
	case CatalogOfferingKindCluster:
		clusterItems := &mockClusterCatalogOfferingsClient{
			createFunc: func(
				ctx context.Context,
				_ *publicv1.ClusterCatalogItemsCreateRequest,
				_ ...grpc.CallOption,
			) (*publicv1.ClusterCatalogItemsCreateResponse, error) {
				recorder.createCalled = true
				recorder.token = forwardedToken(ctx)
				return publicv1.ClusterCatalogItemsCreateResponse_builder{
					Object: publicv1.ClusterCatalogItem_builder{
						Id:       "cluster-offering-1",
						Metadata: publicv1.Metadata_builder{Name: "offering", Version: 1}.Build(),
					}.Build(),
				}.Build(), nil
			},
			updateFunc: func(
				ctx context.Context,
				_ *publicv1.ClusterCatalogItemsUpdateRequest,
				_ ...grpc.CallOption,
			) (*publicv1.ClusterCatalogItemsUpdateResponse, error) {
				recorder.updateCalled = true
				recorder.token = forwardedToken(ctx)
				return publicv1.ClusterCatalogItemsUpdateResponse_builder{
					Object: publicv1.ClusterCatalogItem_builder{
						Id:        "cluster-offering-1",
						Metadata:  publicv1.Metadata_builder{Name: "offering", Version: 8}.Build(),
						Published: publish,
					}.Build(),
				}.Build(), nil
			},
			deleteFunc: func(
				ctx context.Context,
				_ *publicv1.ClusterCatalogItemsDeleteRequest,
				_ ...grpc.CallOption,
			) (*publicv1.ClusterCatalogItemsDeleteResponse, error) {
				recorder.deleteCalled = true
				recorder.token = forwardedToken(ctx)
				return publicv1.ClusterCatalogItemsDeleteResponse_builder{}.Build(), nil
			},
		}
		return catalogOfferingClients{clusters: clusterItems}, recorder
	case CatalogOfferingKindBareMetalInstance:
		bareMetalItems := &mockBareMetalCatalogOfferingsClient{
			createFunc: func(
				ctx context.Context,
				_ *publicv1.BareMetalInstanceCatalogItemsCreateRequest,
				_ ...grpc.CallOption,
			) (*publicv1.BareMetalInstanceCatalogItemsCreateResponse, error) {
				recorder.createCalled = true
				recorder.token = forwardedToken(ctx)
				return publicv1.BareMetalInstanceCatalogItemsCreateResponse_builder{
					Object: publicv1.BareMetalInstanceCatalogItem_builder{
						Id:       "bare-metal-offering-1",
						Metadata: publicv1.Metadata_builder{Name: "offering", Version: 1}.Build(),
					}.Build(),
				}.Build(), nil
			},
			updateFunc: func(
				ctx context.Context,
				_ *publicv1.BareMetalInstanceCatalogItemsUpdateRequest,
				_ ...grpc.CallOption,
			) (*publicv1.BareMetalInstanceCatalogItemsUpdateResponse, error) {
				recorder.updateCalled = true
				recorder.token = forwardedToken(ctx)
				return publicv1.BareMetalInstanceCatalogItemsUpdateResponse_builder{
					Object: publicv1.BareMetalInstanceCatalogItem_builder{
						Id:        "bare-metal-offering-1",
						Metadata:  publicv1.Metadata_builder{Name: "offering", Version: 8}.Build(),
						Published: publish,
					}.Build(),
				}.Build(), nil
			},
			deleteFunc: func(
				ctx context.Context,
				_ *publicv1.BareMetalInstanceCatalogItemsDeleteRequest,
				_ ...grpc.CallOption,
			) (*publicv1.BareMetalInstanceCatalogItemsDeleteResponse, error) {
				recorder.deleteCalled = true
				recorder.token = forwardedToken(ctx)
				return publicv1.BareMetalInstanceCatalogItemsDeleteResponse_builder{}.Build(), nil
			},
		}
		return catalogOfferingClients{bareMetal: bareMetalItems}, recorder
	default:
		Fail("unsupported catalog offering kind")
		return catalogOfferingClients{}, recorder
	}
}

var _ = Describe("catalog offering family dispatch", func() {
	DescribeTable("Creates each supported family through its public API",
		func(kind CatalogOfferingKind, expectedKind string) {
			clients, recorder := catalogOfferingDispatchClients(kind, false)
			handler := handleCreateCatalogOffering(clients)
			_, output, err := handler(
				context.Background(), requestWithToken(catalogOfferingTokenFixture),
				CreateCatalogOfferingInput{Kind: kind, Name: "offering", TemplateID: "template-1"},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(recorder.createCalled).To(BeTrue())
			Expect(recorder.updateCalled).To(BeFalse())
			Expect(recorder.deleteCalled).To(BeFalse())
			Expect(recorder.token).To(Equal("Bearer api-for-" + catalogOfferingTokenFixture))
			Expect(output.Kind).To(Equal(expectedKind))
			Expect(output.ID).ToNot(BeEmpty())
		},
		Entry("compute instance", CatalogOfferingKindComputeInstance, "compute_instance"),
		Entry("cluster", CatalogOfferingKindCluster, "cluster"),
		Entry("bare-metal instance", CatalogOfferingKindBareMetalInstance, "bare_metal_instance"),
	)

	DescribeTable("Updates each supported family through its public API",
		func(kind CatalogOfferingKind, expectedKind string) {
			clients, recorder := catalogOfferingDispatchClients(kind, false)
			handler := handleUpdateCatalogOffering(clients)
			_, output, err := handler(
				context.Background(), requestWithToken(catalogOfferingTokenFixture),
				UpdateCatalogOfferingInput{
					Kind: kind, ID: "offering-1", Version: 7, Title: stringPointer("Updated"),
				},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(recorder.updateCalled).To(BeTrue())
			Expect(recorder.createCalled).To(BeFalse())
			Expect(recorder.deleteCalled).To(BeFalse())
			Expect(recorder.token).To(Equal("Bearer api-for-" + catalogOfferingTokenFixture))
			Expect(output.Kind).To(Equal(expectedKind))
			Expect(output.ID).ToNot(BeEmpty())
		},
		Entry("compute instance", CatalogOfferingKindComputeInstance, "compute_instance"),
		Entry("cluster", CatalogOfferingKindCluster, "cluster"),
		Entry("bare-metal instance", CatalogOfferingKindBareMetalInstance, "bare_metal_instance"),
	)

	DescribeTable("Publishes each supported family through its public API",
		func(kind CatalogOfferingKind, expectedKind string) {
			clients, recorder := catalogOfferingDispatchClients(kind, true)
			handler := handlePublishCatalogOffering(clients)
			_, output, err := handler(
				context.Background(), requestWithToken(catalogOfferingTokenFixture),
				PublishCatalogOfferingInput{Kind: kind, ID: "offering-1", Version: 7},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(recorder.updateCalled).To(BeTrue())
			Expect(recorder.createCalled).To(BeFalse())
			Expect(recorder.deleteCalled).To(BeFalse())
			Expect(recorder.token).To(Equal("Bearer api-for-" + catalogOfferingTokenFixture))
			Expect(output.Kind).To(Equal(expectedKind))
			Expect(output.Published).To(BeTrue())
		},
		Entry("compute instance", CatalogOfferingKindComputeInstance, "compute_instance"),
		Entry("cluster", CatalogOfferingKindCluster, "cluster"),
		Entry("bare-metal instance", CatalogOfferingKindBareMetalInstance, "bare_metal_instance"),
	)

	DescribeTable("Deletes each supported family through its public API",
		func(kind CatalogOfferingKind, expectedKind string) {
			clients, recorder := catalogOfferingDispatchClients(kind, false)
			handler := handleDeleteCatalogOffering(clients)
			_, output, err := handler(
				context.Background(), requestWithToken(catalogOfferingTokenFixture),
				DeleteCatalogOfferingInput{Kind: kind, ID: "offering-1"},
			)
			Expect(err).ToNot(HaveOccurred())
			Expect(recorder.deleteCalled).To(BeTrue())
			Expect(recorder.createCalled).To(BeFalse())
			Expect(recorder.updateCalled).To(BeFalse())
			Expect(recorder.token).To(Equal("Bearer api-for-" + catalogOfferingTokenFixture))
			Expect(output).To(Equal(DeleteCatalogOfferingOutput{Kind: expectedKind, ID: "offering-1"}))
		},
		Entry("compute instance", CatalogOfferingKindComputeInstance, "compute_instance"),
		Entry("cluster", CatalogOfferingKindCluster, "cluster"),
		Entry("bare-metal instance", CatalogOfferingKindBareMetalInstance, "bare_metal_instance"),
	)
})
var _ = Describe("handleCreateCatalogOffering", func() {
	It("Creates an unpublished offering with the caller's token", func() {
		var capturedToken string
		var capturedObject *publicv1.ComputeInstanceCatalogItem
		computeItems := &mockComputeInstanceCatalogOfferingsClient{
			createFunc: func(
				ctx context.Context,
				request *publicv1.ComputeInstanceCatalogItemsCreateRequest,
				_ ...grpc.CallOption,
			) (*publicv1.ComputeInstanceCatalogItemsCreateResponse, error) {
				capturedToken = forwardedToken(ctx)
				capturedObject = request.GetObject()
				return publicv1.ComputeInstanceCatalogItemsCreateResponse_builder{
					Object: publicv1.ComputeInstanceCatalogItem_builder{
						Id:       "compute-offering-1",
						Metadata: publicv1.Metadata_builder{Name: "offering", Version: 1}.Build(),
					}.Build(),
					Warnings: []string{"template parameter is deprecated"},
				}.Build(), nil
			},
		}

		handler := handleCreateCatalogOffering(catalogOfferingClients{computeInstances: computeItems})
		_, output, err := handler(
			context.Background(), requestWithToken(catalogOfferingTokenFixture), CreateCatalogOfferingInput{
				Kind:        CatalogOfferingKindComputeInstance,
				Name:        " offering ",
				Title:       "Compute offering",
				Description: "A governed offering",
				TemplateID:  " template-1 ",
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(capturedToken).To(Equal("Bearer api-for-" + catalogOfferingTokenFixture))
		Expect(capturedObject.GetMetadata().GetName()).To(Equal("offering"))
		Expect(capturedObject.GetTitle()).To(Equal("Compute offering"))
		Expect(capturedObject.GetDescription()).To(Equal("A governed offering"))
		Expect(capturedObject.GetTemplate().GetId()).To(Equal("template-1"))
		Expect(capturedObject.GetPublished()).To(BeFalse())
		Expect(output).To(Equal(CatalogOfferingOutput{
			Kind:     "compute_instance",
			ID:       "compute-offering-1",
			Name:     "offering",
			Version:  1,
			Warnings: []string{"template parameter is deprecated"},
		}))
	})

	It("Rejects unsupported kinds and missing required fields before the public API", func() {
		computeItems := &mockComputeInstanceCatalogOfferingsClient{
			createFunc: func(
				_ context.Context,
				_ *publicv1.ComputeInstanceCatalogItemsCreateRequest,
				_ ...grpc.CallOption,
			) (*publicv1.ComputeInstanceCatalogItemsCreateResponse, error) {
				return nil, errors.New("Create should not be called")
			},
		}
		handler := handleCreateCatalogOffering(catalogOfferingClients{computeInstances: computeItems})

		_, _, err := handler(context.Background(), requestWithToken(catalogOfferingTokenFixture), CreateCatalogOfferingInput{
			Kind: "volume", Name: "offering", TemplateID: "template-1",
		})
		Expect(decodeToolError(err).Details).To(ConsistOf(
			"kind must be compute_instance, cluster, or bare_metal_instance",
		))

		_, _, err = handler(context.Background(), requestWithToken(catalogOfferingTokenFixture), CreateCatalogOfferingInput{
			Kind: CatalogOfferingKindComputeInstance, TemplateID: "template-1",
		})
		Expect(decodeToolError(err).Details).To(ConsistOf("name is required"))

		_, _, err = handler(context.Background(), requestWithToken(catalogOfferingTokenFixture), CreateCatalogOfferingInput{
			Kind: CatalogOfferingKindComputeInstance, Name: "offering",
		})
		Expect(decodeToolError(err).Details).To(ConsistOf("template_id is required"))
	})

	It("Rejects names outside the public Metadata constraint before the public API", func() {
		var createCalled bool
		computeItems := &mockComputeInstanceCatalogOfferingsClient{
			createFunc: func(
				context.Context,
				*publicv1.ComputeInstanceCatalogItemsCreateRequest,
				...grpc.CallOption,
			) (*publicv1.ComputeInstanceCatalogItemsCreateResponse, error) {
				createCalled = true
				return nil, nil
			},
		}
		handler := handleCreateCatalogOffering(catalogOfferingClients{computeInstances: computeItems})

		_, _, err := handler(context.Background(), requestWithToken(catalogOfferingTokenFixture), CreateCatalogOfferingInput{
			Kind: CatalogOfferingKindComputeInstance, Name: "Bad_Name", TemplateID: "template-1",
		})

		Expect(err).To(HaveOccurred())
		Expect(decodeToolError(err).Details).To(ConsistOf(
			"name \"Bad_Name\" must be a valid RFC 1123 DNS label (1-63 lowercase letters, digits, or hyphens; must start and end with a letter or digit)",
		))
		Expect(createCalled).To(BeFalse())
	})
})

var _ = Describe("handleUpdateCatalogOffering", func() {
	It("Applies only display fields with an exact mask and lock", func() {
		var capturedRequest *publicv1.ClusterCatalogItemsUpdateRequest
		clusterItems := &mockClusterCatalogOfferingsClient{
			updateFunc: func(
				_ context.Context,
				request *publicv1.ClusterCatalogItemsUpdateRequest,
				_ ...grpc.CallOption,
			) (*publicv1.ClusterCatalogItemsUpdateResponse, error) {
				capturedRequest = request
				return publicv1.ClusterCatalogItemsUpdateResponse_builder{
					Object: publicv1.ClusterCatalogItem_builder{
						Id:       "cluster-offering-1",
						Metadata: publicv1.Metadata_builder{Name: "offering", Version: 8}.Build(),
					}.Build(),
				}.Build(), nil
			},
		}

		handler := handleUpdateCatalogOffering(catalogOfferingClients{clusters: clusterItems})
		_, output, err := handler(
			context.Background(), requestWithToken(catalogOfferingTokenFixture), UpdateCatalogOfferingInput{
				Kind:    CatalogOfferingKindCluster,
				ID:      " cluster-offering-1 ",
				Version: 7,
				Title:   stringPointer("Updated title"),
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(capturedRequest.GetUpdateMask().GetPaths()).To(ConsistOf("title"))
		Expect(capturedRequest.GetLock()).To(BeTrue())
		Expect(capturedRequest.GetObject().GetId()).To(Equal("cluster-offering-1"))
		Expect(capturedRequest.GetObject().GetMetadata().GetVersion()).To(Equal(int32(7)))
		Expect(capturedRequest.GetObject().GetTitle()).To(Equal("Updated title"))
		Expect(capturedRequest.GetObject().GetTemplate()).To(BeNil())
		Expect(output).To(Equal(CatalogOfferingOutput{
			Kind: "cluster", ID: "cluster-offering-1", Name: "offering", Version: 8,
		}))
	})

	It("Requires a target, current version, and a supported field", func() {
		clusterItems := &mockClusterCatalogOfferingsClient{
			updateFunc: func(
				_ context.Context,
				_ *publicv1.ClusterCatalogItemsUpdateRequest,
				_ ...grpc.CallOption,
			) (*publicv1.ClusterCatalogItemsUpdateResponse, error) {
				return nil, errors.New("Update should not be called")
			},
		}
		handler := handleUpdateCatalogOffering(catalogOfferingClients{clusters: clusterItems})

		_, _, err := handler(context.Background(), requestWithToken(catalogOfferingTokenFixture), UpdateCatalogOfferingInput{
			Kind: CatalogOfferingKindCluster, ID: "offering-1", Version: 1,
		})
		Expect(decodeToolError(err).Details).To(ConsistOf(
			"at least one supported update field is required",
		))

		_, _, err = handler(context.Background(), requestWithToken(catalogOfferingTokenFixture), UpdateCatalogOfferingInput{
			Kind: CatalogOfferingKindCluster, ID: "offering-1", Version: -1,
			Title: stringPointer("Updated"),
		})
		Expect(decodeToolError(err).Details).To(ConsistOf("version must not be negative"))
	})
})

var _ = Describe("handlePublishCatalogOffering", func() {
	It("Publishes through an exact locked update", func() {
		var capturedRequest *publicv1.BareMetalInstanceCatalogItemsUpdateRequest
		bareMetalItems := &mockBareMetalCatalogOfferingsClient{
			updateFunc: func(
				_ context.Context,
				request *publicv1.BareMetalInstanceCatalogItemsUpdateRequest,
				_ ...grpc.CallOption,
			) (*publicv1.BareMetalInstanceCatalogItemsUpdateResponse, error) {
				capturedRequest = request
				return publicv1.BareMetalInstanceCatalogItemsUpdateResponse_builder{
					Object: publicv1.BareMetalInstanceCatalogItem_builder{
						Id:        "bare-metal-offering-1",
						Metadata:  publicv1.Metadata_builder{Name: "offering", Version: 8}.Build(),
						Published: true,
					}.Build(),
				}.Build(), nil
			},
		}

		handler := handlePublishCatalogOffering(catalogOfferingClients{bareMetal: bareMetalItems})
		_, output, err := handler(
			context.Background(), requestWithToken(catalogOfferingTokenFixture), PublishCatalogOfferingInput{
				Kind: CatalogOfferingKindBareMetalInstance, ID: "bare-metal-offering-1", Version: 7,
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(capturedRequest.GetUpdateMask().GetPaths()).To(ConsistOf("published"))
		Expect(capturedRequest.GetLock()).To(BeTrue())
		Expect(capturedRequest.GetObject().GetId()).To(Equal("bare-metal-offering-1"))
		Expect(capturedRequest.GetObject().GetMetadata().GetVersion()).To(Equal(int32(7)))
		Expect(capturedRequest.GetObject().GetPublished()).To(BeTrue())
		Expect(output.Published).To(BeTrue())
	})

	DescribeTable("Maps publication failures to safe errors",
		func(
			publicErr error, category, grpcCode string,
		) {
			bareMetalItems := &mockBareMetalCatalogOfferingsClient{
				updateFunc: func(
					_ context.Context,
					_ *publicv1.BareMetalInstanceCatalogItemsUpdateRequest,
					_ ...grpc.CallOption,
				) (*publicv1.BareMetalInstanceCatalogItemsUpdateResponse, error) {
					return nil, publicErr
				},
			}
			handler := handlePublishCatalogOffering(catalogOfferingClients{bareMetal: bareMetalItems})
			_, output, err := handler(
				context.Background(), requestWithToken(catalogOfferingTokenFixture), PublishCatalogOfferingInput{
					Kind: CatalogOfferingKindBareMetalInstance, ID: "offering-1", Version: 7,
				},
			)
			envelope := decodeToolError(err)
			Expect(output).To(BeZero())
			Expect(envelope.Category).To(Equal(category))
			Expect(envelope.GRPCCode).To(Equal(grpcCode))
			Expect(err.Error()).ToNot(ContainSubstring(publicErr.Error()))
		},
		Entry("stale version", status.Error(codes.Aborted, "version changed privately"),
			"conflict", "Aborted"),
		Entry("policy dependency", status.Error(codes.FailedPrecondition, "private dependency detail"),
			"conflict", "FailedPrecondition"),
		Entry("authorization", status.Error(codes.PermissionDenied, "private tenant detail"),
			"authorization", "PermissionDenied"),
	)
})

var _ = Describe("handleDeleteCatalogOffering", func() {
	It("Deletes the exact target as the caller", func() {
		var capturedToken string
		var capturedID string
		computeItems := &mockComputeInstanceCatalogOfferingsClient{
			deleteFunc: func(
				ctx context.Context,
				request *publicv1.ComputeInstanceCatalogItemsDeleteRequest,
				_ ...grpc.CallOption,
			) (*publicv1.ComputeInstanceCatalogItemsDeleteResponse, error) {
				capturedToken = forwardedToken(ctx)
				capturedID = request.GetId()
				return publicv1.ComputeInstanceCatalogItemsDeleteResponse_builder{}.Build(), nil
			},
		}

		handler := handleDeleteCatalogOffering(catalogOfferingClients{computeInstances: computeItems})
		_, output, err := handler(
			context.Background(), requestWithToken(catalogOfferingTokenFixture), DeleteCatalogOfferingInput{
				Kind: CatalogOfferingKindComputeInstance, ID: " offering-1 ",
			},
		)
		Expect(err).ToNot(HaveOccurred())
		Expect(capturedToken).To(Equal("Bearer api-for-" + catalogOfferingTokenFixture))
		Expect(capturedID).To(Equal("offering-1"))
		Expect(output).To(Equal(DeleteCatalogOfferingOutput{
			Kind: "compute_instance", ID: "offering-1",
		}))
	})
})
