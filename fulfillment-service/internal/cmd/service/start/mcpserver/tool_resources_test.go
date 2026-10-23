/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package mcpserver

import (
	"context"
	"encoding/json"
	"time"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type mockComputeInstanceCatalogItemsClient struct {
	publicv1.ComputeInstanceCatalogItemsClient
	listFunc func(context.Context, *publicv1.ComputeInstanceCatalogItemsListRequest, ...grpc.CallOption) (*publicv1.ComputeInstanceCatalogItemsListResponse, error)
	getFunc  func(context.Context, *publicv1.ComputeInstanceCatalogItemsGetRequest, ...grpc.CallOption) (*publicv1.ComputeInstanceCatalogItemsGetResponse, error)
}

type mockProjectsClient struct {
	publicv1.ProjectsClient
	listFunc func(context.Context, *publicv1.ProjectsListRequest, ...grpc.CallOption) (*publicv1.ProjectsListResponse, error)
	getFunc  func(context.Context, *publicv1.ProjectsGetRequest, ...grpc.CallOption) (*publicv1.ProjectsGetResponse, error)
}

func (m *mockProjectsClient) List(
	ctx context.Context, request *publicv1.ProjectsListRequest, options ...grpc.CallOption,
) (*publicv1.ProjectsListResponse, error) {
	return m.listFunc(ctx, request, options...)
}

func (m *mockProjectsClient) Get(
	ctx context.Context, request *publicv1.ProjectsGetRequest, options ...grpc.CallOption,
) (*publicv1.ProjectsGetResponse, error) {
	return m.getFunc(ctx, request, options...)
}

type mockSecretsClient struct {
	publicv1.SecretsClient
	listFunc func(context.Context, *publicv1.SecretsListRequest, ...grpc.CallOption) (*publicv1.SecretsListResponse, error)
	getFunc  func(context.Context, *publicv1.SecretsGetRequest, ...grpc.CallOption) (*publicv1.SecretsGetResponse, error)
}

func (m *mockSecretsClient) List(
	ctx context.Context, request *publicv1.SecretsListRequest, options ...grpc.CallOption,
) (*publicv1.SecretsListResponse, error) {
	return m.listFunc(ctx, request, options...)
}

func (m *mockSecretsClient) Get(
	ctx context.Context, request *publicv1.SecretsGetRequest, options ...grpc.CallOption,
) (*publicv1.SecretsGetResponse, error) {
	return m.getFunc(ctx, request, options...)
}

func (m *mockComputeInstanceCatalogItemsClient) List(
	ctx context.Context, request *publicv1.ComputeInstanceCatalogItemsListRequest, options ...grpc.CallOption,
) (*publicv1.ComputeInstanceCatalogItemsListResponse, error) {
	return m.listFunc(ctx, request, options...)
}

func (m *mockComputeInstanceCatalogItemsClient) Get(
	ctx context.Context, request *publicv1.ComputeInstanceCatalogItemsGetRequest, options ...grpc.CallOption,
) (*publicv1.ComputeInstanceCatalogItemsGetResponse, error) {
	return m.getFunc(ctx, request, options...)
}

type mockComputeInstancesClient struct {
	publicv1.ComputeInstancesClient
	listFunc   func(context.Context, *publicv1.ComputeInstancesListRequest, ...grpc.CallOption) (*publicv1.ComputeInstancesListResponse, error)
	getFunc    func(context.Context, *publicv1.ComputeInstancesGetRequest, ...grpc.CallOption) (*publicv1.ComputeInstancesGetResponse, error)
	createFunc func(context.Context, *publicv1.ComputeInstancesCreateRequest, ...grpc.CallOption) (*publicv1.ComputeInstancesCreateResponse, error)
	deleteFunc func(context.Context, *publicv1.ComputeInstancesDeleteRequest, ...grpc.CallOption) (*publicv1.ComputeInstancesDeleteResponse, error)
}

type mockComputeInstanceTemplatesClient struct {
	publicv1.ComputeInstanceTemplatesClient
	listFunc func(context.Context, *publicv1.ComputeInstanceTemplatesListRequest, ...grpc.CallOption) (*publicv1.ComputeInstanceTemplatesListResponse, error)
	getFunc  func(context.Context, *publicv1.ComputeInstanceTemplatesGetRequest, ...grpc.CallOption) (*publicv1.ComputeInstanceTemplatesGetResponse, error)
}

func (m *mockComputeInstanceTemplatesClient) List(
	ctx context.Context, request *publicv1.ComputeInstanceTemplatesListRequest, options ...grpc.CallOption,
) (*publicv1.ComputeInstanceTemplatesListResponse, error) {
	return m.listFunc(ctx, request, options...)
}

func (m *mockComputeInstanceTemplatesClient) Get(
	ctx context.Context, request *publicv1.ComputeInstanceTemplatesGetRequest, options ...grpc.CallOption,
) (*publicv1.ComputeInstanceTemplatesGetResponse, error) {
	return m.getFunc(ctx, request, options...)
}

type mockStorageTiersClient struct {
	publicv1.StorageTiersClient
	listFunc func(context.Context, *publicv1.StorageTiersListRequest, ...grpc.CallOption) (*publicv1.StorageTiersListResponse, error)
	getFunc  func(context.Context, *publicv1.StorageTiersGetRequest, ...grpc.CallOption) (*publicv1.StorageTiersGetResponse, error)
}

func (m *mockStorageTiersClient) List(
	ctx context.Context, request *publicv1.StorageTiersListRequest, options ...grpc.CallOption,
) (*publicv1.StorageTiersListResponse, error) {
	return m.listFunc(ctx, request, options...)
}

func (m *mockStorageTiersClient) Get(
	ctx context.Context, request *publicv1.StorageTiersGetRequest, options ...grpc.CallOption,
) (*publicv1.StorageTiersGetResponse, error) {
	return m.getFunc(ctx, request, options...)
}

func (m *mockComputeInstancesClient) List(
	ctx context.Context, request *publicv1.ComputeInstancesListRequest, options ...grpc.CallOption,
) (*publicv1.ComputeInstancesListResponse, error) {
	return m.listFunc(ctx, request, options...)
}

func (m *mockComputeInstancesClient) Get(
	ctx context.Context, request *publicv1.ComputeInstancesGetRequest, options ...grpc.CallOption,
) (*publicv1.ComputeInstancesGetResponse, error) {
	return m.getFunc(ctx, request, options...)
}

func (m *mockComputeInstancesClient) Create(
	ctx context.Context, request *publicv1.ComputeInstancesCreateRequest, options ...grpc.CallOption,
) (*publicv1.ComputeInstancesCreateResponse, error) {
	return m.createFunc(ctx, request, options...)
}

func (m *mockComputeInstancesClient) Delete(
	ctx context.Context, request *publicv1.ComputeInstancesDeleteRequest, options ...grpc.CallOption,
) (*publicv1.ComputeInstancesDeleteResponse, error) {
	return m.deleteFunc(ctx, request, options...)
}

func requestWithToken(rawToken string) *mcp.CallToolRequest {
	return &mcp.CallToolRequest{
		Extra: &mcp.RequestExtra{
			TokenInfo: &sdkauth.TokenInfo{
				Extra: map[string]any{exchangeTokenExtraKey: apiTokenSource(func(context.Context) (string, error) {
					return "api-for-" + rawToken, nil
				})},
			},
		},
	}
}

func forwardedToken(ctx context.Context) string {
	metadata, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		return ""
	}
	values := metadata.Get("authorization")
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

var _ = Describe("handleListResources", func() {
	It("Lists caller-visible projects with bounded pagination and an explicit filter", func() {
		var capturedToken string
		var capturedFilter string
		var capturedOffset, capturedLimit int32
		projects := &mockProjectsClient{
			listFunc: func(
				ctx context.Context, request *publicv1.ProjectsListRequest, _ ...grpc.CallOption,
			) (*publicv1.ProjectsListResponse, error) {
				capturedToken = forwardedToken(ctx)
				capturedFilter = request.GetFilter()
				capturedOffset = request.GetOffset()
				capturedLimit = request.GetLimit()
				return publicv1.ProjectsListResponse_builder{
					Size:  1,
					Total: 3,
					Items: []*publicv1.Project{
						publicv1.Project_builder{
							Id:       "project-1",
							Metadata: publicv1.Metadata_builder{Name: "team.project"}.Build(),
							Spec: publicv1.ProjectSpec_builder{
								Title:       "Team project",
								Description: proto.String("A caller-visible project"),
							}.Build(),
							Status: publicv1.ProjectStatus_builder{
								State: publicv1.ProjectState_PROJECT_STATE_ACTIVE,
							}.Build(),
						}.Build(),
					},
				}.Build(), nil
			},
		}

		handler := handleListResources(newResourceRegistry(ServerDeps{ProjectsClient: projects}))
		_, output, err := handler(context.Background(), requestWithToken("raw-bearer-value"), ListResourcesInput{
			ResourceType: ResourceTypeProject,
			Filter:       `this.id == "project-1"`,
			Offset:       4,
			PageSize:     25,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(output).To(Equal(ListResourcesOutput{
			Offset: 4,
			Size:   1,
			Total:  3,
			Items: []ResourceSummary{{
				ID:          "project-1",
				Name:        "team.project",
				Title:       "Team project",
				Description: "A caller-visible project",
				State:       "PROJECT_STATE_ACTIVE",
			}},
		}))
		Expect(capturedToken).To(Equal("Bearer api-for-raw-bearer-value"))
		Expect(capturedFilter).To(Equal(`this.id == "project-1"`))
		Expect(capturedOffset).To(Equal(int32(4)))
		Expect(capturedLimit).To(Equal(int32(25)))
	})

	It("Lists Secret references only and never Secret bytes", func() {
		var capturedToken string
		var capturedFilter string
		var capturedOffset, capturedLimit int32
		secrets := &mockSecretsClient{
			listFunc: func(
				ctx context.Context, request *publicv1.SecretsListRequest, _ ...grpc.CallOption,
			) (*publicv1.SecretsListResponse, error) {
				capturedToken = forwardedToken(ctx)
				capturedFilter = request.GetFilter()
				capturedOffset = request.GetOffset()
				capturedLimit = request.GetLimit()
				return publicv1.SecretsListResponse_builder{
					Size:  1,
					Total: 2,
					Items: []*publicv1.Secret{
						publicv1.Secret_builder{
							Id:       "secret-1",
							Metadata: publicv1.Metadata_builder{Name: "pull-secret"}.Build(),
							Data:     map[string][]byte{".dockerconfigjson": []byte("sensitive-secret-bytes")},
							Type:     publicv1.SecretType_SECRET_TYPE_PULL_SECRET,
						}.Build(),
					},
				}.Build(), nil
			},
		}

		handler := handleListResources(newResourceRegistry(ServerDeps{SecretsClient: secrets}))
		_, output, err := handler(context.Background(), requestWithToken("raw-bearer-value"), ListResourcesInput{
			ResourceType: ResourceTypeSecretReference,
			Filter:       `this.type == SECRET_TYPE_PULL_SECRET`,
			Offset:       1,
			PageSize:     10,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(output).To(Equal(ListResourcesOutput{
			Offset: 1,
			Size:   1,
			Total:  2,
			Items: []ResourceSummary{{
				ID:   "secret-1",
				Name: "pull-secret",
				Type: "pull_secret",
			}},
		}))
		encoded, marshalErr := json.Marshal(output)
		Expect(marshalErr).To(Succeed())
		Expect(string(encoded)).ToNot(ContainSubstring("sensitive-secret-bytes"))
		Expect(string(encoded)).ToNot(ContainSubstring("data"))
		Expect(capturedToken).To(Equal("Bearer api-for-raw-bearer-value"))
		Expect(capturedFilter).To(Equal(`this.type == SECRET_TYPE_PULL_SECRET`))
		Expect(capturedOffset).To(Equal(int32(1)))
		Expect(capturedLimit).To(Equal(int32(10)))
	})

	It("Lists compute instance catalog items and uses the exchanged API token", func() {
		var capturedToken string
		var capturedOffset, capturedLimit int32
		catalogItems := &mockComputeInstanceCatalogItemsClient{
			listFunc: func(
				ctx context.Context, request *publicv1.ComputeInstanceCatalogItemsListRequest, options ...grpc.CallOption,
			) (*publicv1.ComputeInstanceCatalogItemsListResponse, error) {
				deadline, ok := ctx.Deadline()
				Expect(ok).To(BeTrue())
				Expect(time.Until(deadline)).To(BeNumerically(">", 0))
				Expect(time.Until(deadline)).To(BeNumerically("<=", toolCallTimeout))
				capturedToken = forwardedToken(ctx)
				capturedOffset = request.GetOffset()
				capturedLimit = request.GetLimit()
				return publicv1.ComputeInstanceCatalogItemsListResponse_builder{
					Size:  1,
					Total: 4,
					Items: []*publicv1.ComputeInstanceCatalogItem{
						publicv1.ComputeInstanceCatalogItem_builder{
							Id:          "catalog-item-1",
							Title:       "Small Fedora VM",
							Description: "A small virtual machine",
						}.Build(),
					},
				}.Build(), nil
			},
		}

		handler := handleListResources(newResourceRegistry(ServerDeps{ComputeInstanceCatalogItemsClient: catalogItems}))
		_, output, err := handler(context.Background(), requestWithToken("raw-bearer-value"), ListResourcesInput{
			ResourceType: ResourceTypeComputeInstanceCatalogItem,
			Offset:       2,
			PageSize:     20,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(output.Items).To(ConsistOf(ResourceSummary{
			ID:          "catalog-item-1",
			Title:       "Small Fedora VM",
			Description: "A small virtual machine",
		}))
		Expect(output.Offset).To(Equal(int32(2)))
		Expect(output.Size).To(Equal(int32(1)))
		Expect(output.Total).To(Equal(int32(4)))
		Expect(capturedToken).To(Equal("Bearer api-for-raw-bearer-value"))
		Expect(capturedOffset).To(Equal(int32(2)))
		Expect(capturedLimit).To(Equal(int32(20)))
	})

	It("Rejects invalid page bounds before calling a public List client", func() {
		var listCalls int
		registry := resourceRegistry{
			ResourceTypeComputeInstance: {
				list: func(context.Context, string, resourcePageRequest) (ListResourcesOutput, error) {
					listCalls++
					return ListResourcesOutput{}, nil
				},
			},
		}
		cases := []struct {
			name   string
			input  ListResourcesInput
			detail string
		}{
			{
				name: "negative offset",
				input: ListResourcesInput{
					ResourceType: ResourceTypeComputeInstance,
					Offset:       -1,
				},
				detail: "offset must not be negative",
			},
			{
				name: "page size above maximum",
				input: ListResourcesInput{
					ResourceType: ResourceTypeComputeInstance,
					PageSize:     maxResourcePageSize + 1,
				},
				detail: "page_size must be between 1 and 100",
			},
		}

		for _, testCase := range cases {
			By(testCase.name)
			_, _, err := handleListResources(registry)(
				context.Background(), requestWithToken("raw-bearer-value"), testCase.input,
			)
			envelope := decodeToolError(err)
			Expect(envelope.Category).To(Equal("invalid_request"))
			Expect(envelope.GRPCCode).To(Equal("InvalidArgument"))
			Expect(envelope.Retryable).To(BeFalse())
			Expect(envelope.Details).To(ContainElement(testCase.detail))
		}
		Expect(listCalls).To(BeZero())
	})

	It("Lists compute instances and forwards an optional filter", func() {
		var capturedToken string
		var capturedFilter string
		var capturedLimit int32
		instances := &mockComputeInstancesClient{
			listFunc: func(
				ctx context.Context, request *publicv1.ComputeInstancesListRequest, options ...grpc.CallOption,
			) (*publicv1.ComputeInstancesListResponse, error) {
				capturedToken = forwardedToken(ctx)
				capturedFilter = request.GetFilter()
				capturedLimit = request.GetLimit()
				return publicv1.ComputeInstancesListResponse_builder{
					Items: []*publicv1.ComputeInstance{
						publicv1.ComputeInstance_builder{
							Id:       "instance-1",
							Metadata: publicv1.Metadata_builder{Name: "demo-vm"}.Build(),
							Status: publicv1.ComputeInstanceStatus_builder{
								State: publicv1.ComputeInstanceState_COMPUTE_INSTANCE_STATE_RUNNING,
							}.Build(),
						}.Build(),
					},
				}.Build(), nil
			},
		}

		handler := handleListResources(newResourceRegistry(ServerDeps{ComputeInstancesClient: instances}))
		_, output, err := handler(context.Background(), requestWithToken("raw-bearer-value"), ListResourcesInput{
			ResourceType: ResourceTypeComputeInstance,
			Filter:       "this.metadata.name.startsWith(\"demo\")",
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(output.Items).To(ConsistOf(ResourceSummary{
			ID:    "instance-1",
			Name:  "demo-vm",
			State: "COMPUTE_INSTANCE_STATE_RUNNING",
		}))
		Expect(capturedFilter).To(Equal("this.metadata.name.startsWith(\"demo\")"))
		Expect(capturedToken).To(Equal("Bearer api-for-raw-bearer-value"))
		Expect(capturedLimit).To(Equal(int32(50)))
	})

	It("Lists compute instance templates and storage tiers needed to understand a catalog offering", func() {
		var capturedTemplateToken, capturedStorageToken string
		templates := &mockComputeInstanceTemplatesClient{
			listFunc: func(
				ctx context.Context, request *publicv1.ComputeInstanceTemplatesListRequest, options ...grpc.CallOption,
			) (*publicv1.ComputeInstanceTemplatesListResponse, error) {
				capturedTemplateToken = forwardedToken(ctx)
				Expect(request.GetFilter()).To(Equal(`this.id == "template-1"`))
				return publicv1.ComputeInstanceTemplatesListResponse_builder{
					Size:  1,
					Total: 1,
					Items: []*publicv1.ComputeInstanceTemplate{
						publicv1.ComputeInstanceTemplate_builder{
							Id:          "template-1",
							Metadata:    publicv1.Metadata_builder{Name: "small-fedora"}.Build(),
							Title:       "Small Fedora VM",
							Description: "A small virtual machine",
						}.Build(),
					},
				}.Build(), nil
			},
		}
		storageTiers := &mockStorageTiersClient{
			listFunc: func(
				ctx context.Context, request *publicv1.StorageTiersListRequest, options ...grpc.CallOption,
			) (*publicv1.StorageTiersListResponse, error) {
				capturedStorageToken = forwardedToken(ctx)
				Expect(request.GetLimit()).To(Equal(int32(50)))
				return publicv1.StorageTiersListResponse_builder{
					Size:  1,
					Total: 1,
					Items: []*publicv1.StorageTier{
						publicv1.StorageTier_builder{
							Id:       "storage-tier-1",
							Metadata: publicv1.Metadata_builder{Name: "local"}.Build(),
							Spec:     publicv1.StorageTierSpec_builder{Description: "Local block storage"}.Build(),
							Status: publicv1.StorageTierStatus_builder{
								State: publicv1.StorageTierState_STORAGE_TIER_STATE_ACTIVE,
							}.Build(),
						}.Build(),
					},
				}.Build(), nil
			},
		}

		handler := handleListResources(newResourceRegistry(ServerDeps{
			ComputeInstanceTemplatesClient: templates,
			StorageTiersClient:             storageTiers,
		}))
		_, templatesOutput, err := handler(context.Background(), requestWithToken("raw-bearer-value"), ListResourcesInput{
			ResourceType: ResourceTypeComputeInstanceTemplate,
			Filter:       `this.id == "template-1"`,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(templatesOutput.Items).To(ConsistOf(ResourceSummary{
			ID:          "template-1",
			Name:        "small-fedora",
			Title:       "Small Fedora VM",
			Description: "A small virtual machine",
		}))

		_, storageOutput, err := handler(context.Background(), requestWithToken("raw-bearer-value"), ListResourcesInput{
			ResourceType: ResourceTypeStorageTier,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(storageOutput.Items).To(ConsistOf(ResourceSummary{
			ID:          "storage-tier-1",
			Name:        "local",
			Description: "Local block storage",
			State:       "STORAGE_TIER_STATE_ACTIVE",
		}))
		Expect(capturedTemplateToken).To(Equal("Bearer api-for-raw-bearer-value"))
		Expect(capturedStorageToken).To(Equal("Bearer api-for-raw-bearer-value"))
	})

	It("Returns structured errors for invalid list arguments without calling a public client", func() {
		var listCalls int
		registry := resourceRegistry{
			ResourceTypeComputeInstance: {
				list: func(context.Context, string, resourcePageRequest) (ListResourcesOutput, error) {
					listCalls++
					return ListResourcesOutput{}, nil
				},
			},
		}
		cases := []struct {
			name           string
			input          ListResourcesInput
			expectedDetail string
		}{
			{
				name: "unsupported resource type",
				input: ListResourcesInput{
					ResourceType: "cluster",
				},
				expectedDetail: `unsupported resource type "cluster"`,
			},
			{
				name: "negative offset",
				input: ListResourcesInput{
					ResourceType: ResourceTypeComputeInstance,
					Offset:       -1,
				},
				expectedDetail: "offset must not be negative",
			},
			{
				name: "page size above maximum",
				input: ListResourcesInput{
					ResourceType: ResourceTypeComputeInstance,
					PageSize:     maxResourcePageSize + 1,
				},
				expectedDetail: "page_size must be between 1 and 100",
			},
		}

		for _, testCase := range cases {
			By(testCase.name)
			handler := handleListResources(registry)
			_, _, err := handler(context.Background(), requestWithToken("raw-bearer-value"), testCase.input)
			Expect(err).To(HaveOccurred())
			envelope := decodeToolError(err)
			Expect(envelope.Category).To(Equal("invalid_request"))
			Expect(envelope.GRPCCode).To(Equal("InvalidArgument"))
			Expect(envelope.Retryable).To(BeFalse())
			Expect(envelope.RequestID).ToNot(BeEmpty())
			Expect(envelope.Details).To(Equal([]string{testCase.expectedDetail}))
		}
		Expect(listCalls).To(BeZero())
	})

	It("Hides a catalog item list error description", func() {
		catalogItems := &mockComputeInstanceCatalogItemsClient{
			listFunc: func(
				ctx context.Context, request *publicv1.ComputeInstanceCatalogItemsListRequest, options ...grpc.CallOption,
			) (*publicv1.ComputeInstanceCatalogItemsListResponse, error) {
				return nil, status.Error(codes.Internal, "private SQL host and customer data")
			},
		}

		handler := handleListResources(newResourceRegistry(ServerDeps{ComputeInstanceCatalogItemsClient: catalogItems}))
		_, _, err := handler(context.Background(), requestWithToken("raw-bearer-value"), ListResourcesInput{
			ResourceType: ResourceTypeComputeInstanceCatalogItem,
		})
		envelope := decodeToolError(err)
		Expect(envelope.Category).To(Equal("upstream_failure"))
		Expect(envelope.GRPCCode).To(Equal("Unknown"))
		Expect(err.Error()).ToNot(ContainSubstring("private SQL host"))
	})
})

var _ = Describe("handleGetResource", func() {
	It("Gets a caller-visible project", func() {
		var capturedToken string
		projects := &mockProjectsClient{
			getFunc: func(
				ctx context.Context, request *publicv1.ProjectsGetRequest, _ ...grpc.CallOption,
			) (*publicv1.ProjectsGetResponse, error) {
				capturedToken = forwardedToken(ctx)
				Expect(request.GetId()).To(Equal("project-1"))
				return publicv1.ProjectsGetResponse_builder{
					Object: publicv1.Project_builder{
						Id:       "project-1",
						Metadata: publicv1.Metadata_builder{Name: "team.project"}.Build(),
						Spec:     publicv1.ProjectSpec_builder{Title: "Team project"}.Build(),
					}.Build(),
				}.Build(), nil
			},
		}

		handler := handleGetResource(newResourceRegistry(ServerDeps{ProjectsClient: projects}))
		_, output, err := handler(context.Background(), requestWithToken("raw-bearer-value"), GetResourceInput{
			ResourceType: ResourceTypeProject,
			ID:           "project-1",
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(output.Resource).To(HaveKeyWithValue("id", "project-1"))
		metadata, ok := output.Resource["metadata"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(metadata).To(HaveKeyWithValue("name", "team.project"))
		spec, ok := output.Resource["spec"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(spec).To(HaveKeyWithValue("title", "Team project"))
		Expect(capturedToken).To(Equal("Bearer api-for-raw-bearer-value"))
	})

	It("Rejects Secret-reference Get without calling the public Secrets API", func() {
		getCalled := false
		secrets := &mockSecretsClient{
			getFunc: func(
				context.Context, *publicv1.SecretsGetRequest, ...grpc.CallOption,
			) (*publicv1.SecretsGetResponse, error) {
				getCalled = true
				return nil, status.Error(codes.Unavailable, "must not be called")
			},
		}

		handler := handleGetResource(newResourceRegistry(ServerDeps{SecretsClient: secrets}))
		_, _, err := handler(context.Background(), requestWithToken("raw-bearer-value"), GetResourceInput{
			ResourceType: ResourceTypeSecretReference,
			ID:           "secret-1",
		})
		envelope := decodeToolError(err)
		Expect(envelope.Category).To(Equal("invalid_request"))
		Expect(envelope.GRPCCode).To(Equal("InvalidArgument"))
		Expect(envelope.Details).To(ContainElement("get_resource does not support secret_reference"))
		Expect(getCalled).To(BeFalse())
	})

	It("Gets a compute instance catalog item and returns its API representation", func() {
		var capturedToken string
		policy := &publicv1.UserDataSourceFieldPolicy{}
		catalogItems := &mockComputeInstanceCatalogItemsClient{
			getFunc: func(
				ctx context.Context, request *publicv1.ComputeInstanceCatalogItemsGetRequest, options ...grpc.CallOption,
			) (*publicv1.ComputeInstanceCatalogItemsGetResponse, error) {
				capturedToken = forwardedToken(ctx)
				Expect(request.GetId()).To(Equal("catalog-item-1"))
				return publicv1.ComputeInstanceCatalogItemsGetResponse_builder{
					Object: publicv1.ComputeInstanceCatalogItem_builder{
						Id:    "catalog-item-1",
						Title: "Small Fedora VM",
						Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
							UserDataSource: policy,
						}.Build(),
					}.Build(),
				}.Build(), nil
			},
		}

		handler := handleGetResource(newResourceRegistry(ServerDeps{ComputeInstanceCatalogItemsClient: catalogItems}))
		for _, editable := range []bool{false, true} {
			for _, inline := range []bool{true, false} {
				source := publicv1.UserDataSource_builder{Inline: proto.String("sensitive-cloud-init")}.Build()
				if !inline {
					source.SetSecret(publicv1.SecretLocalReference_builder{Id: "user-data-secret"}.Build())
				}
				policy.SetLocked(source)
				if editable {
					policy.SetEditable(publicv1.EditableUserDataSourceField_builder{DefaultValue: source}.Build())
				}
				_, output, err := handler(context.Background(), requestWithToken("raw-bearer-value"), GetResourceInput{
					ResourceType: ResourceTypeComputeInstanceCatalogItem,
					ID:           "catalog-item-1",
				})
				Expect(err).ToNot(HaveOccurred())
				Expect(output.Resource).To(HaveKeyWithValue("id", "catalog-item-1"))
				Expect(output.Resource).To(HaveKeyWithValue("title", "Small Fedora VM"))
				fields, ok := output.Resource["fields"].(map[string]any)
				Expect(ok).To(BeTrue())
				_, visible := fields["userDataSource"]
				Expect(visible).To(Equal(!inline))
				Expect(capturedToken).To(Equal("Bearer api-for-raw-bearer-value"))
			}
		}
	})

	It("Gets a compute instance and returns its API representation", func() {
		var capturedToken string
		source := publicv1.UserDataSource_builder{Inline: proto.String("sensitive-cloud-init")}.Build()
		instances := &mockComputeInstancesClient{
			getFunc: func(
				ctx context.Context, request *publicv1.ComputeInstancesGetRequest, options ...grpc.CallOption,
			) (*publicv1.ComputeInstancesGetResponse, error) {
				capturedToken = forwardedToken(ctx)
				Expect(request.GetId()).To(Equal("instance-1"))
				return publicv1.ComputeInstancesGetResponse_builder{
					Object: publicv1.ComputeInstance_builder{
						Id:       "instance-1",
						Metadata: publicv1.Metadata_builder{Name: "demo-vm"}.Build(),
						Spec: publicv1.ComputeInstanceSpec_builder{
							UserDataSource: source,
							InstanceType:   publicv1.InstanceTypeReference_builder{Id: "small"}.Build(),
						}.Build(),
					}.Build(),
				}.Build(), nil
			},
		}

		handler := handleGetResource(newResourceRegistry(ServerDeps{ComputeInstancesClient: instances}))
		_, output, err := handler(context.Background(), requestWithToken("raw-bearer-value"), GetResourceInput{
			ResourceType: ResourceTypeComputeInstance,
			ID:           "instance-1",
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(output.Resource).To(HaveKeyWithValue("id", "instance-1"))
		metadata, ok := output.Resource["metadata"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(metadata).To(HaveKeyWithValue("name", "demo-vm"))
		spec, ok := output.Resource["spec"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(spec).ToNot(HaveKey("userDataSource"))
		Expect(spec).To(HaveKey("instanceType"))
		Expect(capturedToken).To(Equal("Bearer api-for-raw-bearer-value"))

		source.SetSecret(publicv1.SecretLocalReference_builder{Id: "user-data-secret"}.Build())
		_, output, err = handler(context.Background(), requestWithToken("raw-bearer-value"), GetResourceInput{
			ResourceType: ResourceTypeComputeInstance,
			ID:           "instance-1",
		})
		Expect(err).ToNot(HaveOccurred())
		spec, ok = output.Resource["spec"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(spec["userDataSource"]).To(HaveKeyWithValue("secret", HaveKeyWithValue("id", "user-data-secret")))
	})

	It("Gets a compute instance template including the boot disk storage tier", func() {
		var capturedToken string
		templates := &mockComputeInstanceTemplatesClient{
			getFunc: func(
				ctx context.Context, request *publicv1.ComputeInstanceTemplatesGetRequest, options ...grpc.CallOption,
			) (*publicv1.ComputeInstanceTemplatesGetResponse, error) {
				capturedToken = forwardedToken(ctx)
				Expect(request.GetId()).To(Equal("template-1"))
				return publicv1.ComputeInstanceTemplatesGetResponse_builder{
					Object: publicv1.ComputeInstanceTemplate_builder{
						Id: "template-1",
						SpecDefaults: publicv1.ComputeInstanceTemplateSpecDefaults_builder{
							BootDisk: publicv1.ComputeInstanceDisk_builder{
								SizeGib: proto.Int32(20),
								StorageTier: publicv1.StorageTierReference_builder{
									Name: "local",
								}.Build(),
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build(), nil
			},
		}

		handler := handleGetResource(newResourceRegistry(ServerDeps{ComputeInstanceTemplatesClient: templates}))
		_, output, err := handler(context.Background(), requestWithToken("raw-bearer-value"), GetResourceInput{
			ResourceType: ResourceTypeComputeInstanceTemplate,
			ID:           "template-1",
		})
		Expect(err).ToNot(HaveOccurred())
		specDefaults, ok := output.Resource["specDefaults"].(map[string]any)
		Expect(ok).To(BeTrue())
		bootDisk, ok := specDefaults["bootDisk"].(map[string]any)
		Expect(ok).To(BeTrue())
		storageTier, ok := bootDisk["storageTier"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(storageTier).To(HaveKeyWithValue("name", "local"))
		Expect(capturedToken).To(Equal("Bearer api-for-raw-bearer-value"))
	})

	It("Returns structured errors for invalid get arguments without calling a public client", func() {
		var getCalls int
		registry := resourceRegistry{
			ResourceTypeComputeInstance: {
				get: func(context.Context, string) (proto.Message, error) {
					getCalls++
					return nil, nil
				},
			},
		}
		cases := []struct {
			name           string
			input          GetResourceInput
			expectedDetail string
		}{
			{
				name: "unsupported resource type",
				input: GetResourceInput{
					ResourceType: "cluster",
					ID:           "cluster-1",
				},
				expectedDetail: `unsupported resource type "cluster"`,
			},
			{
				name: "empty resource ID",
				input: GetResourceInput{
					ResourceType: ResourceTypeComputeInstance,
				},
				expectedDetail: "id must not be empty",
			},
		}

		for _, testCase := range cases {
			By(testCase.name)
			handler := handleGetResource(registry)
			_, _, err := handler(context.Background(), requestWithToken("raw-bearer-value"), testCase.input)
			Expect(err).To(HaveOccurred())
			envelope := decodeToolError(err)
			Expect(envelope.Category).To(Equal("invalid_request"))
			Expect(envelope.GRPCCode).To(Equal("InvalidArgument"))
			Expect(envelope.Retryable).To(BeFalse())
			Expect(envelope.RequestID).ToNot(BeEmpty())
			Expect(envelope.Details).To(Equal([]string{testCase.expectedDetail}))
		}
		Expect(getCalls).To(BeZero())
	})

	It("Preserves a safe Get error category without its description", func() {
		instances := &mockComputeInstancesClient{
			getFunc: func(
				ctx context.Context, request *publicv1.ComputeInstancesGetRequest, options ...grpc.CallOption,
			) (*publicv1.ComputeInstancesGetResponse, error) {
				return nil, status.Error(codes.NotFound, "private SQL host and customer data")
			},
		}

		handler := handleGetResource(newResourceRegistry(ServerDeps{ComputeInstancesClient: instances}))
		_, _, err := handler(context.Background(), requestWithToken("raw-bearer-value"), GetResourceInput{
			ResourceType: ResourceTypeComputeInstance,
			ID:           "instance-1",
		})
		envelope := decodeToolError(err)
		Expect(envelope.Category).To(Equal("not_found"))
		Expect(envelope.GRPCCode).To(Equal("NotFound"))
		Expect(err.Error()).ToNot(ContainSubstring("private SQL host"))
	})
})

var _ = Describe("messageToMap", func() {
	It("Returns an empty object for a nil message", func() {
		output, err := messageToMap(nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(output).To(Equal(map[string]any{}))
	})
})
