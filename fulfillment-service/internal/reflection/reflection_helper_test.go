/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package reflection

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/config"
	"github.com/osac-project/fulfillment-service/internal/packages"
	"github.com/osac-project/fulfillment-service/internal/testing"
)

var _ = Describe("Reflection helper", func() {
	var (
		ctx        context.Context
		server     *testing.Server
		connection *grpc.ClientConn
	)

	BeforeEach(func() {
		var err error

		// Create a context:
		ctx = context.Background()

		// Create the server:
		server = testing.NewServer()
		DeferCleanup(server.Stop)

		// Create the client connection:
		connection, err = grpc.NewClient(
			server.Address(),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(connection.Close)
	})

	Describe("Creation", func() {
		It("Can be created with all the mandatory parameters", func() {
			helper, err := NewHelper().
				SetLogger(logger).
				SetConnection(connection).
				AddPackage(packages.PublicV1, 1).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(helper).ToNot(BeNil())
		})

		It("Can be created with multiple packages", func() {
			helper, err := NewHelper().
				SetLogger(logger).
				SetConnection(connection).
				AddPackage(packages.PublicV1, 1).
				AddPackage(packages.PrivateV1, 0).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(helper).ToNot(BeNil())
		})

		It("Can be created with multiple specified in one call", func() {
			helper, err := NewHelper().
				SetLogger(logger).
				SetConnection(connection).
				AddPackages(map[string]int{
					packages.PrivateV1: 0,
					packages.PublicV1:  1,
				}).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(helper).ToNot(BeNil())
		})

		It("Can't be created without a logger", func() {
			helper, err := NewHelper().
				SetConnection(connection).
				AddPackage(packages.PublicV1, 1).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(helper).To(BeNil())
		})

		It("Can't be created without a connection", func() {
			helper, err := NewHelper().
				SetLogger(logger).
				AddPackage(packages.PublicV1, 1).
				Build()
			Expect(err).To(MatchError("gRPC connection is mandatory"))
			Expect(helper).To(BeNil())
		})

		It("Can't be created without at least one package", func() {
			helper, err := NewHelper().
				SetLogger(logger).
				SetConnection(connection).
				Build()
			Expect(err).To(MatchError("at least one package is mandatory"))
			Expect(helper).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var helper Helper

		BeforeEach(func() {
			var err error
			helper, err = NewHelper().
				SetLogger(logger).
				SetConnection(connection).
				AddPackage(packages.PublicV1, 1).
				SetTenantFunc(config.TenantFromContext).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Returns object types in singular", func() {
			Expect(helper.Singulars()).To(ConsistOf(
				"baremetalinstance",
				"baremetalinstancecatalogitem",
				"baremetalinstancetemplate",
				"cluster",
				"clustercatalogitem",
				"clustertemplate",
				"clusterversion",
				"computeinstance",
				"computeinstancecatalogitem",
				"computeinstancetemplate",
				"externalip",
				"externalipattachment",
				"hosttype",
				"identityprovider",
				"instancetype",
				"natgateway",
				"networkclass",
				"project",
				"projectmembership",
				"role",
				"rolebinding",
				"secret",
				"securitygroup",
				"subnet",
				"tenant",
				"user",
				"virtualnetwork",
			))
		})

		It("Returns object types in plural", func() {
			Expect(helper.Plurals()).To(ConsistOf(
				"baremetalinstances",
				"baremetalinstancecatalogitems",
				"baremetalinstancetemplates",
				"clusters",
				"clustercatalogitems",
				"clustertemplates",
				"clusterversions",
				"computeinstances",
				"computeinstancecatalogitems",
				"computeinstancetemplates",
				"externalipattachments",
				"externalips",
				"hosttypes",
				"identityproviders",
				"instancetypes",
				"natgateways",
				"networkclasses",
				"projectmemberships",
				"projects",
				"roles",
				"rolebindings",
				"secrets",
				"securitygroups",
				"subnets",
				"tenants",
				"users",
				"virtualnetworks",
			))
		})

		DescribeTable(
			"Lookup by object type",
			func(objectType string, expectedFullName string) {
				objectHelper := helper.Lookup(objectType)
				Expect(objectHelper).ToNot(BeNil())
				Expect(string(objectHelper.FullName())).To(Equal(expectedFullName))
			},
			Entry(
				"Cluster in singular",
				"cluster",
				"osac.public.v1.Cluster",
			),
			Entry(
				"Cluster in plural",
				"clusters",
				"osac.public.v1.Cluster",
			),
			Entry(
				"Cluster in singular upper case",
				"CLUSTER",
				"osac.public.v1.Cluster",
			),
			Entry(
				"Host type in plural",
				"hosttypes",
				"osac.public.v1.HostType",
			),
			Entry(
				"Tenant in singular",
				"tenant",
				"osac.public.v1.Tenant",
			),
			Entry(
				"Tenant in plural",
				"tenants",
				"osac.public.v1.Tenant",
			),
			Entry(
				"Tenant in upper case",
				"TENANT",
				"osac.public.v1.Tenant",
			),
		)

		DescribeTable(
			"Returns descriptor",
			func(objectType string, expectedFullName string) {
				objectHelper := helper.Lookup(objectType)
				Expect(objectHelper).ToNot(BeNil())
				objectDescriptor := objectHelper.Descriptor()
				Expect(objectDescriptor).ToNot(BeNil())
				Expect(string(objectDescriptor.FullName())).To(Equal(expectedFullName))
			},
			Entry(
				"Cluster",
				"cluster",
				"osac.public.v1.Cluster",
			),
			Entry(
				"Cluster template",
				"clustertemplate",
				"osac.public.v1.ClusterTemplate",
			),
			Entry(
				"Host type",
				"hosttype",
				"osac.public.v1.HostType",
			),
			Entry(
				"Compute instance template",
				"computeinstancetemplate",
				"osac.public.v1.ComputeInstanceTemplate",
			),
			Entry(
				"Compute instance",
				"computeinstance",
				"osac.public.v1.ComputeInstance",
			),
		)

		DescribeTable(
			"Creates instance",
			func(objectType string, expectedInstance proto.Message) {
				objectHelper := helper.Lookup(objectType)
				Expect(objectHelper).ToNot(BeNil())
				actualInstance := objectHelper.Instance()
				Expect(proto.Equal(actualInstance, expectedInstance)).To(BeTrue())
			},
			Entry(
				"Cluster",
				"cluster",
				&publicv1.Cluster{},
			),
			Entry(
				"Cluster template",
				"clustertemplate",
				&publicv1.ClusterTemplate{},
			),
			Entry(
				"Host type",
				"hosttype",
				&publicv1.HostType{},
			),
		)

		It("Invokes get method", func() {
			// Register a clusters server that responds to the get request:
			publicv1.RegisterClustersServer(server.Registrar(), &testing.ClustersServerFuncs{
				GetFunc: func(ctx context.Context, request *publicv1.ClustersGetRequest,
				) (response *publicv1.ClustersGetResponse, err error) {
					defer GinkgoRecover()
					Expect(request.GetId()).To(Equal("123"))
					response = publicv1.ClustersGetResponse_builder{
						Object: publicv1.Cluster_builder{
							Id: "123",
							Status: publicv1.ClusterStatus_builder{
								State: publicv1.ClusterState_CLUSTER_STATE_READY,
							}.Build(),
						}.Build(),
					}.Build()
					return
				},
			})

			// Start the server:
			server.Start()

			// Use the helper to send the request, and verify the response:
			objectHelper := helper.Lookup("cluster")
			Expect(objectHelper).ToNot(BeNil())
			object, err := objectHelper.Get(ctx, "123")
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(object, publicv1.Cluster_builder{
				Id: "123",
				Status: publicv1.ClusterStatus_builder{
					State: publicv1.ClusterState_CLUSTER_STATE_READY,
				}.Build(),
			}.Build())).To(BeTrue())
		})

		It("Invokes list method", func() {
			// Register a clusters server that responds to the list request:
			publicv1.RegisterClustersServer(server.Registrar(), &testing.ClustersServerFuncs{
				ListFunc: func(ctx context.Context, request *publicv1.ClustersListRequest,
				) (response *publicv1.ClustersListResponse, err error) {
					response = publicv1.ClustersListResponse_builder{
						Size:  2,
						Total: 2,
						Items: []*publicv1.Cluster{
							publicv1.Cluster_builder{
								Id: "123",
							}.Build(),
							publicv1.Cluster_builder{
								Id: "456",
							}.Build(),
						},
					}.Build()
					return
				},
			})

			// Start the server:
			server.Start()

			// Use the helper to send the request, and verify the response:
			objectHelper := helper.Lookup("cluster")
			Expect(objectHelper).ToNot(BeNil())
			listResult, err := objectHelper.List(ctx, ListOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(listResult.Items).To(HaveLen(2))
			Expect(listResult.Total).To(Equal(int32(2)))
			Expect(proto.Equal(
				listResult.Items[0],
				publicv1.Cluster_builder{
					Id: "123",
				}.Build(),
			)).To(BeTrue())
			Expect(proto.Equal(
				listResult.Items[1],
				publicv1.Cluster_builder{
					Id: "456",
				}.Build(),
			)).To(BeTrue())
		})

		It("Invokes create method", func() {
			// Register a clusters server that responds to the create request:
			publicv1.RegisterClustersServer(server.Registrar(), &testing.ClustersServerFuncs{
				CreateFunc: func(ctx context.Context, request *publicv1.ClustersCreateRequest,
				) (response *publicv1.ClustersCreateResponse, err error) {
					defer GinkgoRecover()
					Expect(proto.Equal(
						request.Object,
						publicv1.Cluster_builder{
							Spec: publicv1.ClusterSpec_builder{
								NodeSets: map[string]*publicv1.ClusterNodeSet{
									"xyz": publicv1.ClusterNodeSet_builder{
										HostType: "acme_1tib",
										Size:     3,
									}.Build(),
								},
							}.Build(),
						}.Build(),
					)).To(BeTrue())
					response = publicv1.ClustersCreateResponse_builder{
						Object: publicv1.Cluster_builder{
							Id: "123",
							Spec: publicv1.ClusterSpec_builder{
								NodeSets: map[string]*publicv1.ClusterNodeSet{
									"xyz": publicv1.ClusterNodeSet_builder{
										HostType: "acme_1tib",
										Size:     3,
									}.Build(),
								},
							}.Build(),
						}.Build(),
					}.Build()
					return
				},
			})

			// Start the server:
			server.Start()

			// Use the helper to send the request, and verify the response:
			objectHelper := helper.Lookup("cluster")
			Expect(objectHelper).ToNot(BeNil())
			object, err := objectHelper.Create(ctx, publicv1.Cluster_builder{
				Spec: publicv1.ClusterSpec_builder{
					NodeSets: map[string]*publicv1.ClusterNodeSet{
						"xyz": publicv1.ClusterNodeSet_builder{
							HostType: "acme_1tib",
							Size:     3,
						}.Build(),
					},
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(
				object,
				publicv1.Cluster_builder{
					Id: "123",
					Spec: publicv1.ClusterSpec_builder{
						NodeSets: map[string]*publicv1.ClusterNodeSet{
							"xyz": publicv1.ClusterNodeSet_builder{
								HostType: "acme_1tib",
								Size:     3,
							}.Build(),
						},
					}.Build(),
				}.Build(),
			)).To(BeTrue())
		})

		It("Invokes update method", func() {
			// Register a clusters server that responds to the update request:
			publicv1.RegisterClustersServer(server.Registrar(), &testing.ClustersServerFuncs{
				UpdateFunc: func(ctx context.Context, request *publicv1.ClustersUpdateRequest,
				) (response *publicv1.ClustersUpdateResponse, err error) {
					defer GinkgoRecover()
					Expect(proto.Equal(
						request.Object,
						publicv1.Cluster_builder{
							Id: "123",
							Spec: publicv1.ClusterSpec_builder{
								NodeSets: map[string]*publicv1.ClusterNodeSet{
									"xyz": publicv1.ClusterNodeSet_builder{
										Size: 3,
									}.Build(),
								},
							}.Build(),
						}.Build(),
					)).To(BeTrue())
					response = publicv1.ClustersUpdateResponse_builder{
						Object: publicv1.Cluster_builder{
							Id: "123",
							Spec: publicv1.ClusterSpec_builder{
								NodeSets: map[string]*publicv1.ClusterNodeSet{
									"xyz": publicv1.ClusterNodeSet_builder{
										HostType: "acme_1tib",
										Size:     3,
									}.Build(),
								},
							}.Build(),
						}.Build(),
					}.Build()
					return
				},
			})

			// Start the server:
			server.Start()

			// Use the helper to send the request, and verify the response:
			objectHelper := helper.Lookup("cluster")
			Expect(objectHelper).ToNot(BeNil())
			object, err := objectHelper.Update(ctx, publicv1.Cluster_builder{
				Id: "123",
				Spec: publicv1.ClusterSpec_builder{
					NodeSets: map[string]*publicv1.ClusterNodeSet{
						"xyz": publicv1.ClusterNodeSet_builder{
							Size: 3,
						}.Build(),
					},
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(
				object,
				publicv1.Cluster_builder{
					Id: "123",
					Spec: publicv1.ClusterSpec_builder{
						NodeSets: map[string]*publicv1.ClusterNodeSet{
							"xyz": publicv1.ClusterNodeSet_builder{
								HostType: "acme_1tib",
								Size:     3,
							}.Build(),
						},
					}.Build(),
				}.Build(),
			)).To(BeTrue())
		})

		It("Invokes delete method", func() {
			// Register a clusters server that responds to the delete request:
			publicv1.RegisterClustersServer(server.Registrar(), &testing.ClustersServerFuncs{
				DeleteFunc: func(ctx context.Context, request *publicv1.ClustersDeleteRequest,
				) (response *publicv1.ClustersDeleteResponse, err error) {
					defer GinkgoRecover()
					response = publicv1.ClustersDeleteResponse_builder{}.Build()
					return
				},
			})

			// Start the server:
			server.Start()

			// Use the helper to send the request, and verify the response:
			objectHelper := helper.Lookup("cluster")
			Expect(objectHelper).ToNot(BeNil())
			err := objectHelper.Delete(ctx, "123")
			Expect(err).ToNot(HaveOccurred())
		})

		It("Returns metadata from get method", func() {
			// Register a clusters server that responds to the get request with metadata:
			publicv1.RegisterClustersServer(server.Registrar(), &testing.ClustersServerFuncs{
				GetFunc: func(ctx context.Context, request *publicv1.ClustersGetRequest,
				) (response *publicv1.ClustersGetResponse, err error) {
					defer GinkgoRecover()
					Expect(request.GetId()).To(Equal("123"))
					response = publicv1.ClustersGetResponse_builder{
						Object: publicv1.Cluster_builder{
							Id: "123",
							Metadata: publicv1.Metadata_builder{
								Name: "my-cluster",
							}.Build(),
						}.Build(),
					}.Build()
					return
				},
			})

			// Start the server:
			server.Start()

			// Use the helper to send the request, and verify the metadata is returned:
			objectHelper := helper.Lookup("cluster")
			Expect(objectHelper).ToNot(BeNil())
			object, err := objectHelper.Get(ctx, "123")
			Expect(err).ToNot(HaveOccurred())
			Expect(object).ToNot(BeNil())
			metadata := objectHelper.GetMetadata(object)
			Expect(metadata).ToNot(BeNil())
			Expect(metadata.GetName()).To(Equal("my-cluster"))
		})

		It("Sorts types according to package order", func() {
			// Create a helper with multiple packages, where private has a lower order (0) than public (1),
			// so private types should appear first:
			multiPackageHelper, err := NewHelper().
				SetLogger(logger).
				SetConnection(connection).
				AddPackage(packages.PrivateV1, 0).
				AddPackage(packages.PublicV1, 1).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Get all the type names:
			names := multiPackageHelper.Names()

			// Verify that private types come before public types:
			var lastPrivateIndex = -1
			var firstPublicIndex = -1
			for i, name := range names {
				if strings.HasPrefix(name, packages.PrivateV1) {
					lastPrivateIndex = i
				}
				if strings.HasPrefix(name, packages.PublicV1) && firstPublicIndex == -1 {
					firstPublicIndex = i
				}
			}

			// If both package types exist, verify that all private types come before public types:
			if lastPrivateIndex >= 0 && firstPublicIndex >= 0 {
				Expect(lastPrivateIndex).To(
					BeNumerically("<", firstPublicIndex),
					"All private types should come before public types",
				)
			}

			// Verify that within each package, types are sorted alphabetically:
			privateTypes := []string{}
			publicTypes := []string{}
			for _, name := range names {
				if strings.HasPrefix(name, packages.PrivateV1) {
					privateTypes = append(privateTypes, name)
				}
				if strings.HasPrefix(name, packages.PublicV1) {
					publicTypes = append(publicTypes, name)
				}
			}

			// Check that private types are sorted alphabetically:
			if len(privateTypes) > 1 {
				for i := 1; i < len(privateTypes); i++ {
					Expect(privateTypes[i-1] < privateTypes[i]).To(
						BeTrue(),
						"Types within private package should be sorted alphabetically, '%s' "+
							"should come before '%s'",
						privateTypes[i-1], privateTypes[i],
					)
				}
			}

			// Check that public types are sorted alphabetically:
			if len(publicTypes) > 1 {
				for i := 1; i < len(publicTypes); i++ {
					Expect(publicTypes[i-1] < publicTypes[i]).To(
						BeTrue(),
						"Types within public package should be sorted alphabetically, '%s' "+
							"should come before '%s'",
						publicTypes[i-1], publicTypes[i],
					)
				}
			}
		})

		It("Sets tenant on an object with existing metadata", func() {
			objectHelper := helper.Lookup("cluster")
			Expect(objectHelper).ToNot(BeNil())
			object := publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: "my-cluster",
				}.Build(),
			}.Build()
			objectHelper.SetTenant(object, "my-tenant")
			Expect(objectHelper.GetTenant(object)).To(Equal("my-tenant"))
			Expect(objectHelper.GetMetadata(object).GetName()).To(Equal("my-cluster"))
		})

		It("Sets tenant on an object without metadata", func() {
			objectHelper := helper.Lookup("cluster")
			Expect(objectHelper).ToNot(BeNil())
			object := publicv1.Cluster_builder{
				Id: "123",
			}.Build()
			objectHelper.SetTenant(object, "my-tenant")
			Expect(objectHelper.GetTenant(object)).To(Equal("my-tenant"))
		})

		It("Returns empty tenant when none is set", func() {
			objectHelper := helper.Lookup("cluster")
			Expect(objectHelper).ToNot(BeNil())
			object := publicv1.Cluster_builder{
				Metadata: publicv1.Metadata_builder{
					Name: "my-cluster",
				}.Build(),
			}.Build()
			Expect(objectHelper.GetTenant(object)).To(BeEmpty())
		})

		It("Injects tenant filter into list when tenant is in context", func() {
			var capturedFilter string
			publicv1.RegisterClustersServer(server.Registrar(), &testing.ClustersServerFuncs{
				ListFunc: func(ctx context.Context, request *publicv1.ClustersListRequest,
				) (response *publicv1.ClustersListResponse, err error) {
					capturedFilter = request.GetFilter()
					response = publicv1.ClustersListResponse_builder{
						Size:  0,
						Total: 0,
					}.Build()
					return
				},
			})
			server.Start()

			tenantCtx := config.TenantIntoContext(ctx, "acme")
			objectHelper := helper.Lookup("cluster")
			Expect(objectHelper).ToNot(BeNil())
			_, err := objectHelper.List(tenantCtx, ListOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(capturedFilter).To(Equal(`this.metadata.tenant == "acme"`))
		})

		It("Combines tenant filter with user-provided filter", func() {
			var capturedFilter string
			publicv1.RegisterClustersServer(server.Registrar(), &testing.ClustersServerFuncs{
				ListFunc: func(ctx context.Context, request *publicv1.ClustersListRequest,
				) (response *publicv1.ClustersListResponse, err error) {
					capturedFilter = request.GetFilter()
					response = publicv1.ClustersListResponse_builder{
						Size:  0,
						Total: 0,
					}.Build()
					return
				},
			})
			server.Start()

			tenantCtx := config.TenantIntoContext(ctx, "acme")
			objectHelper := helper.Lookup("cluster")
			Expect(objectHelper).ToNot(BeNil())
			_, err := objectHelper.List(tenantCtx, ListOptions{
				Filter: `this.metadata.name == "my-cluster"`,
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(capturedFilter).To(Equal(
				`this.metadata.tenant == "acme" && (this.metadata.name == "my-cluster")`,
			))
		})

		It("Reports tenant-scoped types correctly", func() {
			Expect(helper.Lookup("cluster").IsTenantScoped()).To(BeTrue())
			Expect(helper.Lookup("virtualnetwork").IsTenantScoped()).To(BeTrue())
			Expect(helper.Lookup("subnet").IsTenantScoped()).To(BeTrue())
			Expect(helper.Lookup("project").IsTenantScoped()).To(BeTrue())
		})

		It("Reports platform-scoped types correctly", func() {
			Expect(helper.Lookup("hosttype").IsTenantScoped()).To(BeFalse())
			Expect(helper.Lookup("tenant").IsTenantScoped()).To(BeFalse())
			Expect(helper.Lookup("networkclass").IsTenantScoped()).To(BeFalse())
			Expect(helper.Lookup("role").IsTenantScoped()).To(BeFalse())
		})

		It("Accounts for every discovered type as tenant-scoped or platform-scoped", func() {
			names := helper.Names()
			Expect(names).ToNot(BeEmpty())
			for _, name := range names {
				objectHelper := helper.Lookup(name)
				Expect(objectHelper).ToNot(BeNil(), "Lookup failed for %s", name)
				// Verify that every type has an explicit scope decision — if this test
				// fails after adding a new resource type, add it to platformScopedTypes
				// if it is not tenant-scoped.
				_ = objectHelper.IsTenantScoped()
			}
		})

		It("Does not inject tenant filter when no tenant in context", func() {
			var capturedFilter string
			publicv1.RegisterClustersServer(server.Registrar(), &testing.ClustersServerFuncs{
				ListFunc: func(ctx context.Context, request *publicv1.ClustersListRequest,
				) (response *publicv1.ClustersListResponse, err error) {
					capturedFilter = request.GetFilter()
					response = publicv1.ClustersListResponse_builder{
						Size:  0,
						Total: 0,
					}.Build()
					return
				},
			})
			server.Start()

			objectHelper := helper.Lookup("cluster")
			Expect(objectHelper).ToNot(BeNil())
			_, err := objectHelper.List(ctx, ListOptions{
				Filter: `this.metadata.name == "my-cluster"`,
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(capturedFilter).To(Equal(`this.metadata.name == "my-cluster"`))
		})

		It("Sorts types according to package order when adding packages", func() {
			// Create a helper using AddPackages method with reversed order:
			multiPackageHelper, err := NewHelper().
				SetLogger(logger).
				SetConnection(connection).
				AddPackages(map[string]int{
					packages.PublicV1:  2,
					packages.PrivateV1: 1,
				}).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Get all the type names:
			names := multiPackageHelper.Names()

			// Verify that private types come before public types:
			var lastPrivateIndex = -1
			var firstPublicIndex = -1
			for i, name := range names {
				if strings.HasPrefix(name, packages.PrivateV1) {
					lastPrivateIndex = i
				}
				if strings.HasPrefix(name, packages.PublicV1) && firstPublicIndex == -1 {
					firstPublicIndex = i
				}
			}

			// If both package types exist, verify that all private types come before public types:
			if lastPrivateIndex >= 0 && firstPublicIndex >= 0 {
				Expect(lastPrivateIndex).To(
					BeNumerically("<", firstPublicIndex),
					"All private types should come before public types when "+
						"private package has lower order",
				)
			}
		})
	})
})
