/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"fmt"
	"math"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/references"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func seedClusterVersion(ctx context.Context, cv *privatev1.ClusterVersion) {
	GinkgoHelper()
	cv = proto.Clone(cv).(*privatev1.ClusterVersion)
	if refKey(cv.GetSpec().GetDiskImage()) == "" {
		cv.GetSpec().SetDiskImage(privatev1.DiskImageReference_builder{Id: "test-disk-image-id"}.Build())
	}
	seedClusterVersionObject(ctx, cv)
}

func seedClusterVersionWithoutDiskImage(ctx context.Context, cv *privatev1.ClusterVersion) {
	GinkgoHelper()
	cv = proto.Clone(cv).(*privatev1.ClusterVersion)
	cv.GetSpec().SetDiskImage(nil)
	seedClusterVersionObject(ctx, cv)
}

func seedClusterVersionObject(ctx context.Context, cv *privatev1.ClusterVersion) {
	GinkgoHelper()
	cvDao, err := dao.NewGenericDAO[*privatev1.ClusterVersion]().
		SetLogger(logger).
		SetTenancyLogic(tenancy).
		Build()
	Expect(err).ToNot(HaveOccurred())
	_, err = cvDao.Create().SetObject(cv).Do(ctx)
	Expect(err).ToNot(HaveOccurred())
}

func seedAddOnOperator(ctx context.Context, id, name string, published bool) {
	GinkgoHelper()
	seedAddOnOperatorObject(ctx, newTestAddOnOperator(id, name, published))
}

func newTestAddOnOperator(id, name string, published bool) *privatev1.AddOnOperator {
	return privatev1.AddOnOperator_builder{
		Id: id,
		Metadata: privatev1.Metadata_builder{
			Name:   name,
			Tenant: auth.SharedTenant,
		}.Build(),
		Title:     name,
		Published: proto.Bool(published),
	}.Build()
}

func seedAddOnOperatorObject(ctx context.Context, object *privatev1.AddOnOperator) {
	GinkgoHelper()
	operatorDao, err := dao.NewGenericDAO[*privatev1.AddOnOperator]().
		SetLogger(logger).
		SetTenancyLogic(tenancy).
		Build()
	Expect(err).ToNot(HaveOccurred())
	_, err = operatorDao.Create().SetObject(object).Do(ctx)
	Expect(err).ToNot(HaveOccurred())
}

type clusterCreator interface {
	Create(context.Context, *privatev1.ClustersCreateRequest) (*privatev1.ClustersCreateResponse, error)
}

func createClusterWithAddOnOperators(ctx context.Context, server clusterCreator,
	operators []*privatev1.AddOnOperatorReference) (*privatev1.Cluster, error) {
	response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
		Object: privatev1.Cluster_builder{
			Metadata: privatev1.Metadata_builder{Name: fmt.Sprintf("test-%s", uuid.New()[24:32])}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				Template:       privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
				AddOnOperators: operators,
			}.Build(),
		}.Build(),
	}.Build())
	if err != nil {
		return nil, err
	}
	return response.GetObject(), nil
}

func expectAddOnOperatorFieldViolation(err error, field string) {
	GinkgoHelper()
	status, ok := grpcstatus.FromError(err)
	Expect(ok).To(BeTrue())
	Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
	for _, detail := range status.Details() {
		badRequest, ok := detail.(*errdetails.BadRequest)
		if !ok {
			continue
		}
		for _, violation := range badRequest.GetFieldViolations() {
			if violation.GetField() == field {
				return
			}
		}
	}
	Fail(fmt.Sprintf("expected a field violation for %q, got %q", field, status.Message()))
}

func seedCaaSTestBareMetalInstanceType(
	ctx context.Context, id, name, tenant string, ports []*privatev1.BareMetalNetworkPortSpec,
) {
	GinkgoHelper()
	bmitDao, err := dao.NewGenericDAO[*privatev1.BareMetalInstanceType]().
		SetLogger(logger).
		SetTenancyLogic(tenancy).
		Build()
	Expect(err).ToNot(HaveOccurred())
	_, err = bmitDao.Create().SetObject(privatev1.BareMetalInstanceType_builder{
		Id: id,
		Metadata: privatev1.Metadata_builder{
			Name:   name,
			Tenant: tenant,
		}.Build(),
		Spec: privatev1.BareMetalInstanceTypeSpec_builder{
			Hardware: privatev1.BareMetalHardwareSpec_builder{NetworkPorts: ports}.Build(),
		}.Build(),
	}.Build()).Do(ctx)
	Expect(err).ToNot(HaveOccurred())
}

func newCaaSNodeSetCreateRequest(
	name string,
	nodeSets map[string]*privatev1.ClusterNodeSet,
	attachment *privatev1.ClusterNetworkAttachment,
) *privatev1.ClustersCreateRequest {
	return privatev1.ClustersCreateRequest_builder{
		Object: privatev1.Cluster_builder{
			Metadata: privatev1.Metadata_builder{Name: name}.Build(),
			Spec: privatev1.ClusterSpec_builder{
				Template:          privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
				NodeSets:          nodeSets,
				NetworkAttachment: attachment,
			}.Build(),
			Status: privatev1.ClusterStatus_builder{Hub: "my-hub-id"}.Build(),
		}.Build(),
	}.Build()
}

// clusterCreationFixture supplies explicit Cluster NodeSets for tests of unrelated
// fields that previously depended on template-supplied hardware. It also runs the
// reference interceptor for NodeSets, as a gRPC request would before reaching the handler.
type clusterCreationFixture struct{ *PrivateClustersServer }

func (s *clusterCreationFixture) Create(ctx context.Context, request *privatev1.ClustersCreateRequest) (*privatev1.ClustersCreateResponse, error) {
	request = proto.Clone(request).(*privatev1.ClustersCreateRequest)
	if spec := request.GetObject().GetSpec(); spec != nil && spec.GetTemplate() != nil &&
		spec.GetNodeSets() == nil && spec.GetTemplate().GetId() != "no-bmit-template-id" {
		spec = request.GetObject().GetSpec()
		nodeSets := map[string]*privatev1.ClusterNodeSet{
			"compute": privatev1.ClusterNodeSet_builder{
				Size: proto.Int32(3), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id", Shared: true}.Build(),
			}.Build(),
		}
		if spec.GetTemplate().GetId() == "my-template-id" || spec.GetTemplate().GetName() == "my-template-name" {
			nodeSets["gpu"] = privatev1.ClusterNodeSet_builder{
				Size: proto.Int32(1), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-gpu-bmit-id", Shared: true}.Build(),
			}.Build()
		}
		spec.SetNodeSets(nodeSets)
	}
	if err := s.resolveNodeSetReferences(ctx, request.GetObject().GetSpec().GetNodeSets()); err != nil {
		return nil, err
	}
	return s.PrivateClustersServer.Create(ctx, request)
}

func (s *clusterCreationFixture) Update(ctx context.Context, request *privatev1.ClustersUpdateRequest) (*privatev1.ClustersUpdateResponse, error) {
	request = proto.Clone(request).(*privatev1.ClustersUpdateRequest)
	if err := s.resolveNodeSetReferences(ctx, request.GetObject().GetSpec().GetNodeSets()); err != nil {
		return nil, err
	}
	return s.PrivateClustersServer.Update(ctx, request)
}

func (s *clusterCreationFixture) resolveNodeSetReferences(ctx context.Context, nodeSets map[string]*privatev1.ClusterNodeSet) error {
	request := privatev1.ClustersCreateRequest_builder{
		Object: privatev1.Cluster_builder{
			Spec: privatev1.ClusterSpec_builder{NodeSets: nodeSets}.Build(),
		}.Build(),
	}.Build()
	return resolveClusterNodeSetReferencesForTest(ctx, request, privatev1.Clusters_Create_FullMethodName)
}

// resolveClusterNodeSetReferencesForTest runs the real interceptor on a request
// containing only NodeSets. Other references remain under their existing handler
// tests, and Catalog Item defaults are applied later by the handler.
func resolveClusterNodeSetReferencesForTest(ctx context.Context, request proto.Message, method string) error {
	validator, err := references.NewReferenceValidator().SetLogger(logger).Build()
	if err != nil {
		return err
	}
	bmitDao, err := dao.NewGenericDAO[*privatev1.BareMetalInstanceType]().
		SetLogger(logger).SetTenancyLogic(tenancy).Build()
	if err != nil {
		return err
	}
	references.RegisterDAOLookup(validator, "osac.private.v1.BareMetalInstanceTypeReference", bmitDao)
	references.RegisterDAOLookup(validator, "osac.public.v1.BareMetalInstanceTypeReference", bmitDao)
	clusterVersionsDao, err := dao.NewGenericDAO[*privatev1.ClusterVersion]().
		SetLogger(logger).SetTenancyLogic(tenancy).Build()
	if err != nil {
		return err
	}
	references.RegisterDAOLookup(validator, "osac.private.v1.ClusterVersionReference", clusterVersionsDao)
	references.RegisterDAOLookup(validator, "osac.public.v1.ClusterVersionReference", clusterVersionsDao)
	_, err = validator.UnaryServer(ctx, request,
		&grpc.UnaryServerInfo{FullMethod: method},
		func(context.Context, any) (any, error) { return nil, nil })
	return err
}

var _ = Describe("Private clusters server", func() {
	Describe("node-set validation", func() {
		It("rejects a cluster without an effective node-set map", func() {
			cluster := privatev1.Cluster_builder{Spec: privatev1.ClusterSpec_builder{}.Build()}.Build()
			err := (&PrivateClustersServer{}).resolveClusterNodeSets(ctx, cluster)
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(err).To(MatchError(ContainSubstring("at least one node set")))
		})

		It("validates the resolved node-set map", func() {
			size := int32(2)
			valid := map[string]*privatev1.ClusterNodeSet{
				"workers": privatev1.ClusterNodeSet_builder{
					Size:                  &size,
					BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "worker"}.Build(),
				}.Build(),
			}
			Expect(validateClusterNodeSetMap(valid)).To(Succeed())
			Expect(validateClusterNodeSetMap(map[string]*privatev1.ClusterNodeSet{
				"workers": privatev1.ClusterNodeSet_builder{Size: &size}.Build(),
			})).To(MatchError("bare metal instance type for node set 'workers' is required"))
			Expect(validateClusterNodeSetMap(map[string]*privatev1.ClusterNodeSet{
				"workers": nil,
			})).To(MatchError("node set 'workers' must not be null"))

			zero := int32(0)
			Expect(validateClusterNodeSetMap(map[string]*privatev1.ClusterNodeSet{
				"workers": privatev1.ClusterNodeSet_builder{Size: &zero}.Build(),
			})).To(MatchError("size for node set 'workers' should be greater than zero, but it is 0"))
		})

		DescribeTable("accepts valid DNS label node-set names",
			func(name string) {
				size := int32(2)
				Expect(validateClusterNodeSetMap(map[string]*privatev1.ClusterNodeSet{
					name: privatev1.ClusterNodeSet_builder{
						Size:                  &size,
						BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "worker"}.Build(),
					}.Build(),
				})).To(Succeed())
			},
			Entry("simple name", "workers"),
			Entry("hyphenated name", "my-workers"),
			Entry("single character", "a"),
			Entry("name with digits", "worker1"),
		)

		DescribeTable("rejects invalid node-set names",
			func(name string) {
				size := int32(2)
				Expect(validateClusterNodeSetMap(map[string]*privatev1.ClusterNodeSet{
					name: privatev1.ClusterNodeSet_builder{
						Size:                  &size,
						BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "worker"}.Build(),
					}.Build(),
				})).To(MatchError(ContainSubstring("node set name '" + name + "' is not a valid DNS label")))
			},
			Entry("uppercase", "WORKERS"),
			Entry("leading hyphen", "-workers"),
			Entry("trailing hyphen", "workers-"),
			Entry("underscore", "my_workers"),
			Entry("empty string", ""),
			Entry("exceeds 63 characters", "a234567890123456789012345678901234567890123456789012345678901234"),
		)

		It("validates node-set names via validateNodeSetNames", func() {
			Expect(validateNodeSetNames(map[string]*privatev1.ClusterNodeSet{
				"workers": nil,
			})).To(Succeed())
			err := validateNodeSetNames(map[string]*privatev1.ClusterNodeSet{
				"INVALID": nil,
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("node set name 'INVALID' is not a valid DNS label"))
		})
	})

	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewPrivateClustersServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewPrivateClustersServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if attribution logic is not set", func() {
			server, err := NewPrivateClustersServer().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("attribution logic is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewPrivateClustersServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})

	})

	Describe("Behaviour", func() {
		var server *clusterCreationFixture

		BeforeEach(func() {
			var err error

			// Create the server:
			privateServer, buildErr := NewPrivateClustersServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(buildErr).ToNot(HaveOccurred())
			server = &clusterCreationFixture{PrivateClustersServer: privateServer}

			// Create a default cluster version for version resolution:
			seedClusterVersion(ctx, privatev1.ClusterVersion_builder{
				Id: "cv-default",
				Metadata: privatev1.Metadata_builder{
					Name:   "4-17-0",
					Tenant: testTenant,
				}.Build(),
				Spec: privatev1.ClusterVersionSpec_builder{
					Image:     "quay.io/openshift-release-dev/ocp-release:4.17.0-multi",
					Enabled:   proto.Bool(true),
					IsDefault: proto.Bool(true),
					Version:   "4.17.0",
					State:     privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_ACTIVE,
				}.Build(),
			}.Build())

			// Create the bare metal instance types DAO:
			bmitDao, err := dao.NewGenericDAO[*privatev1.BareMetalInstanceType]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Create the templates DAO:
			templatesDao, err := dao.NewGenericDAO[*privatev1.ClusterTemplate]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Create the bare metal instance types (platform-scoped, with a fabric network port
			// so network attachment specs can resolve a fabric interface):
			fabricPort := privatev1.BareMetalNetworkPortSpec_builder{
				Name: "data-0", Role: "fabric", Type: "Ethernet", Speed: "100Gbps",
			}.Build()
			_, err = bmitDao.Create().
				SetObject(
					privatev1.BareMetalInstanceType_builder{
						Id: "acme-bmit-id",
						Metadata: privatev1.Metadata_builder{
							Name:   "acme-bmit-name",
							Tenant: auth.SharedTenant,
						}.Build(),
						Spec: privatev1.BareMetalInstanceTypeSpec_builder{
							Hardware: privatev1.BareMetalHardwareSpec_builder{
								NetworkPorts: []*privatev1.BareMetalNetworkPortSpec{fabricPort},
							}.Build(),
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = bmitDao.Create().
				SetObject(
					privatev1.BareMetalInstanceType_builder{
						Id: "acme-gpu-bmit-id",
						Metadata: privatev1.Metadata_builder{
							Name:   "acme-gpu-name",
							Tenant: auth.SharedTenant,
						}.Build(),
						Spec: privatev1.BareMetalInstanceTypeSpec_builder{
							Hardware: privatev1.BareMetalHardwareSpec_builder{
								NetworkPorts: []*privatev1.BareMetalNetworkPortSpec{fabricPort},
							}.Build(),
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Create a usable template:
			_, err = templatesDao.Create().
				SetObject(
					privatev1.ClusterTemplate_builder{
						Id: "my-template-id",
						Metadata: privatev1.Metadata_builder{
							Name:   "my-template-name",
							Tenant: testTenant,
						}.Build(),
						Title:       "My template",
						Description: "My template",
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Create a second template for missing-node-set cases:
			_, err = templatesDao.Create().
				SetObject(
					privatev1.ClusterTemplate_builder{
						Id: "no-bmit-template-id",
						Metadata: privatev1.Metadata_builder{
							Name:   "no-bmit-template-name",
							Tenant: testTenant,
						}.Build(),
						Title:       "No BMI template",
						Description: "Template without hardware selections",
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Create ready networking resources for network attachment tests:
			networkClassesDao, err := dao.NewGenericDAO[*privatev1.NetworkClass]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = networkClassesDao.Create().SetObject(privatev1.NetworkClass_builder{
				Id: "test-network-class",
				Metadata: privatev1.Metadata_builder{
					Name:   "test-network-class",
					Tenant: testTenant,
				}.Build(),
				Status: privatev1.NetworkClassStatus_builder{
					State: privatev1.NetworkClassState_NETWORK_CLASS_STATE_READY,
					Hub:   "network-hub-a",
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			vnDao, err := dao.NewGenericDAO[*privatev1.VirtualNetwork]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = vnDao.Create().SetObject(privatev1.VirtualNetwork_builder{
				Id: "test-vnet",
				Metadata: privatev1.Metadata_builder{
					Name:   "test-vnet",
					Tenant: testTenant,
				}.Build(),
				Spec: privatev1.VirtualNetworkSpec_builder{
					NetworkClass: privatev1.NetworkClassReference_builder{Id: "test-network-class"}.Build(),
				}.Build(),
				Status: privatev1.VirtualNetworkStatus_builder{
					State: privatev1.VirtualNetworkState_VIRTUAL_NETWORK_STATE_READY,
					Hub:   "network-hub-a",
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			subnetsDao, err := dao.NewGenericDAO[*privatev1.Subnet]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			for _, subnetID := range []string{"subnet-1", "subnet-2"} {
				_, err = subnetsDao.Create().SetObject(privatev1.Subnet_builder{
					Id: subnetID,
					Metadata: privatev1.Metadata_builder{
						Name:   subnetID,
						Tenant: testTenant,
					}.Build(),
					Spec: privatev1.SubnetSpec_builder{
						VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "test-vnet"}.Build(),
						Ipv4Cidr:       new("10.0.0.0/24"),
					}.Build(),
					Status: privatev1.SubnetStatus_builder{
						State: privatev1.SubnetState_SUBNET_STATE_READY,
						Hub:   "network-hub-a",
					}.Build(),
				}.Build()).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			}

			sgDao, err := dao.NewGenericDAO[*privatev1.SecurityGroup]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = sgDao.Create().SetObject(privatev1.SecurityGroup_builder{
				Id: "default-sg",
				Metadata: privatev1.Metadata_builder{
					Name:   "default-sg",
					Tenant: testTenant,
				}.Build(),
				Spec: privatev1.SecurityGroupSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "test-vnet"}.Build(),
				}.Build(),
				Status: privatev1.SecurityGroupStatus_builder{
					State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY,
					Hub:   "network-hub-a",
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = sgDao.Create().SetObject(privatev1.SecurityGroup_builder{
				Id: "sg-2",
				Metadata: privatev1.Metadata_builder{
					Name:   "sg-2",
					Tenant: testTenant,
				}.Build(),
				Spec: privatev1.SecurityGroupSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "test-vnet"}.Build(),
				}.Build(),
				Status: privatev1.SecurityGroupStatus_builder{
					State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY,
					Hub:   "network-hub-a",
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = sgDao.Create().SetObject(privatev1.SecurityGroup_builder{
				Id: "sg-3",
				Metadata: privatev1.Metadata_builder{
					Name:   "sg-3",
					Tenant: testTenant,
				}.Build(),
				Spec: privatev1.SecurityGroupSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: "test-vnet"}.Build(),
				}.Build(),
				Status: privatev1.SecurityGroupStatus_builder{
					State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY,
					Hub:   "network-hub-a",
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Create numbered templates for list tests:
			for i := range 10 {
				_, err = templatesDao.Create().
					SetObject(
						privatev1.ClusterTemplate_builder{
							Id:          fmt.Sprintf("my-template-id-%d", i),
							Title:       fmt.Sprintf("My template %d", i),
							Description: fmt.Sprintf("My template %d", i),
							Metadata: privatev1.Metadata_builder{
								Name:   fmt.Sprintf("my-template-name-%d", i),
								Tenant: testTenant,
							}.Build(),
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			}

			seedAddOnOperator(ctx, "operator-1", "operator-one", true)
			seedAddOnOperator(ctx, "operator-2", "operator-two", true)
		})

		Describe("Tenant placement", func() {
			var tenantServer *PrivateClustersServer

			BeforeEach(func() {
				// Model an administrator explicitly: unlike the suite double, its
				// default tenant is shared, not testTenant.
				ctx = auth.ContextWithSubject(ctx, &auth.Subject{User: "admin", Tenants: auth.AllTenants})
				adminTenancy, err := auth.NewDefaultTenancyLogic().SetLogger(logger).Build()
				Expect(err).ToNot(HaveOccurred())
				tenantServer, err = NewPrivateClustersServer().
					SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(adminTenancy).Build()
				Expect(err).ToNot(HaveOccurred())

				_, err = tenantServer.templatesDao.Create().SetObject(privatev1.ClusterTemplate_builder{
					Metadata: privatev1.Metadata_builder{Name: "shared-template", Tenant: auth.SharedTenant}.Build(),
				}.Build()).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				seedClusterVersion(ctx, privatev1.ClusterVersion_builder{
					Id:       "shared-version-id",
					Metadata: privatev1.Metadata_builder{Name: "shared-version", Tenant: auth.SharedTenant}.Build(),
					Spec: privatev1.ClusterVersionSpec_builder{
						Version: "4.18.0", Image: "quay.io/openshift-release-dev/ocp-release:4.18.0-multi",
						Enabled: proto.Bool(true), State: privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_ACTIVE,
					}.Build(),
				}.Build())
				_, err = tenantServer.catalogItemsDao.Create().SetObject(privatev1.ClusterCatalogItem_builder{
					Metadata:  privatev1.Metadata_builder{Name: "shared-offering", Tenant: auth.SharedTenant}.Build(),
					Published: true,
					Template:  privatev1.ClusterTemplateReference_builder{Name: "shared-template", Shared: true}.Build(),
				}.Build()).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			})

			newRequest := func(tenant string, catalog bool) *privatev1.ClustersCreateRequest {
				request := newCaaSNodeSetCreateRequest(fmt.Sprintf("tenant-placement-%s", uuid.New()[24:32]),
					map[string]*privatev1.ClusterNodeSet{
						"workers": privatev1.ClusterNodeSet_builder{
							Size: proto.Int32(1),
							BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{
								Id: "acme-bmit-id", Name: "acme-bmit-name", Shared: true,
							}.Build(),
						}.Build(),
					}, nil)
				request.GetObject().GetMetadata().SetTenant(tenant)
				spec := request.GetObject().GetSpec()
				spec.SetVersion(privatev1.ClusterVersionReference_builder{Id: "shared-version-id", Shared: true}.Build())
				if catalog {
					spec.ClearTemplate()
					spec.SetCatalogItem(privatev1.ClusterCatalogItemReference_builder{Name: "shared-offering", Shared: true}.Build())
				} else {
					spec.SetTemplate(privatev1.ClusterTemplateReference_builder{Name: "shared-template", Shared: true}.Build())
				}
				return request
			}

			DescribeTable("rejects reserved tenant creation without persistence or request mutation",
				func(tenant string, catalog bool) {
					request := newRequest(tenant, catalog)
					original := proto.Clone(request)
					response, err := tenantServer.Create(ctx, request)
					Expect(grpcstatus.Code(err)).To(Equal(grpccodes.PermissionDenied))
					reserved := tenant
					if reserved == "" {
						reserved = auth.SharedTenant
					}
					Expect(grpcstatus.Convert(err).Message()).To(Equal(fmt.Sprintf("objects cannot be placed in the '%s' tenant", reserved)))
					Expect(response).To(BeNil())
					Expect(proto.Equal(request, original)).To(BeTrue())

					stored, err := tenantServer.generic.dao.List().
						SetFilter(fmt.Sprintf("this.metadata.name == %q", request.GetObject().GetMetadata().GetName())).Do(ctx)
					Expect(err).ToNot(HaveOccurred())
					Expect(stored.GetTotal()).To(BeZero())
					ips, err := tenantServer.externalIPDao.List().Do(ctx)
					Expect(err).ToNot(HaveOccurred())
					Expect(ips.GetTotal()).To(BeZero())
					attachments, err := tenantServer.externalIPAttachmentDao.List().Do(ctx)
					Expect(err).ToNot(HaveOccurred())
					Expect(attachments.GetTotal()).To(BeZero())
					workersDAO, err := dao.NewGenericDAO[*privatev1.BareMetalInstance]().
						SetLogger(logger).SetTenancyLogic(tenancy).Build()
					Expect(err).ToNot(HaveOccurred())
					workers, err := workersDAO.List().Do(ctx)
					Expect(err).ToNot(HaveOccurred())
					Expect(workers.GetTotal()).To(BeZero())
				},
				Entry("explicit shared tenant with a template", auth.SharedTenant, false),
				Entry("explicit shared tenant with a catalog item", auth.SharedTenant, true),
				Entry("admin default shared tenant with a template", "", false),
				Entry("admin default shared tenant with a catalog item", "", true),
				Entry("system tenant", auth.SystemTenant, false),
			)

			DescribeTable("creates a tenant-owned cluster from shared catalog resources",
				func(catalog bool) {
					request := newRequest(testTenant, catalog)
					original := proto.Clone(request)
					response, err := tenantServer.Create(ctx, request)
					Expect(err).ToNot(HaveOccurred())
					Expect(response.GetObject().GetMetadata().GetTenant()).To(Equal(testTenant))
					Expect(response.GetObject().GetSpec().GetTemplate().GetName()).To(Equal("shared-template"))
					Expect(response.GetObject().GetSpec().GetVersion().GetId()).To(Equal("shared-version-id"))
					Expect(proto.Equal(request, original)).To(BeTrue())
					stored, err := tenantServer.Get(ctx, privatev1.ClustersGetRequest_builder{Id: response.GetObject().GetId()}.Build())
					Expect(err).ToNot(HaveOccurred())
					Expect(stored.GetObject().GetMetadata().GetTenant()).To(Equal(testTenant))
				},
				Entry("direct template", false),
				Entry("catalog item", true),
			)

			It("rejects moving a cluster into shared without changing the stored tenant", func() {
				created, err := tenantServer.Create(ctx, newRequest(testTenant, false))
				Expect(err).ToNot(HaveOccurred())
				object := created.GetObject()
				request := privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: object.GetId(), Metadata: privatev1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.tenant"}},
				}.Build()
				original := proto.Clone(request)
				response, err := tenantServer.Update(ctx, request)
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.PermissionDenied))
				Expect(grpcstatus.Convert(err).Message()).To(Equal("objects cannot be placed in the 'shared' tenant"))
				Expect(response).To(BeNil())
				Expect(proto.Equal(request, original)).To(BeTrue())
				stored, err := tenantServer.Get(ctx, privatev1.ClustersGetRequest_builder{Id: object.GetId()}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(proto.Equal(stored.GetObject(), object)).To(BeTrue())
			})

			It("allows reading, status feedback and deletion of a legacy shared cluster", func() {
				// Bypass Create to model data persisted before shared placement was rejected.
				created, err := tenantServer.generic.dao.Create().SetObject(newRequest(auth.SharedTenant, false).GetObject()).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				id := created.GetObject().GetId()
				stored, err := tenantServer.Get(ctx, privatev1.ClustersGetRequest_builder{Id: id}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(stored.GetObject().GetMetadata().GetTenant()).To(Equal(auth.SharedTenant))
				updated, err := tenantServer.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: id,
						Status: privatev1.ClusterStatus_builder{
							State:               privatev1.ClusterState_CLUSTER_STATE_READY,
							StateTransitionTime: timestamppb.Now(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.state", "status.state_transition_time"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(updated.GetObject().GetMetadata().GetTenant()).To(Equal(auth.SharedTenant))
				stored, err = tenantServer.Get(ctx, privatev1.ClustersGetRequest_builder{Id: id}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(stored.GetObject().GetStatus().GetState()).To(Equal(privatev1.ClusterState_CLUSTER_STATE_READY))
				_, err = tenantServer.Delete(ctx, privatev1.ClustersDeleteRequest_builder{Id: id}.Build())
				Expect(err).ToNot(HaveOccurred())
				_, err = tenantServer.Get(ctx, privatev1.ClustersGetRequest_builder{Id: id}.Build())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.NotFound))
			})
		})

		It("Creates object", func() {
			response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
					}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).ToNot(BeEmpty())
			Expect(object.GetStatus().GetObservedCpVersion()).To(Equal("4.17.0"))
			Expect(object.GetStatus().GetNodeSets()["compute"].GetObservedVersion()).To(Equal("4.17.0"))
			Expect(object.GetStatus().GetNodeSets()["gpu"].GetObservedVersion()).To(Equal("4.17.0"))

			// Verify that the node sets inherited from the template carry the canonicalized
			// bare metal instance type references:
			nodeSets := object.GetSpec().GetNodeSets()
			Expect(nodeSets).To(HaveLen(2))
			Expect(nodeSets["compute"].GetBaremetalInstanceType().GetId()).To(Equal("acme-bmit-id"))
			Expect(nodeSets["compute"].GetBaremetalInstanceType().GetName()).To(Equal("acme-bmit-name"))
			Expect(nodeSets["compute"].GetSize()).To(BeNumerically("==", 3))
			Expect(nodeSets["gpu"].GetBaremetalInstanceType().GetId()).To(Equal("acme-gpu-bmit-id"))
			Expect(nodeSets["gpu"].GetBaremetalInstanceType().GetName()).To(Equal("acme-gpu-name"))
			Expect(nodeSets["gpu"].GetSize()).To(BeNumerically("==", 1))
		})

		It("rejects an IPv6-only pool for automatic ExternalIP allocation", func() {
			_, err := server.externalIPPoolDao.Create().SetObject(
				privatev1.ExternalIPPool_builder{
					Id: "ipv6-auto-pool",
					Metadata: privatev1.Metadata_builder{
						Tenant: testTenant,
					}.Build(),
					Spec: privatev1.ExternalIPPoolSpec_builder{
						IpFamily: privatev1.IPFamily_IP_FAMILY_IPV6,
					}.Build(),
					Status: privatev1.ExternalIPPoolStatus_builder{
						State:     privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY,
						Available: 2,
					}.Build(),
				}.Build(),
			).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			err = server.autoProvisionExternalIPs(ctx, privatev1.Cluster_builder{
				Id: "cluster-ipv6-only",
				Metadata: privatev1.Metadata_builder{
					Tenant: testTenant,
				}.Build(),
			}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("IP_FAMILY_IPV4"))
		})

		It("rejects a cluster whose subnet and security group are assigned to different Hubs", func() {
			virtualNetworksDao, err := dao.NewGenericDAO[*privatev1.VirtualNetwork]().
				SetLogger(logger).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			virtualNetwork, err := virtualNetworksDao.Create().SetObject(privatev1.VirtualNetwork_builder{
				Metadata: privatev1.Metadata_builder{Name: "hub-compat-vn", Tenant: testTenant}.Build(),
				Spec: privatev1.VirtualNetworkSpec_builder{
					NetworkClass: privatev1.NetworkClassReference_builder{Id: "test-network-class"}.Build(),
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			subnetsDao, err := dao.NewGenericDAO[*privatev1.Subnet]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			subnet, err := subnetsDao.Create().SetObject(privatev1.Subnet_builder{
				Metadata: privatev1.Metadata_builder{Name: "hub-compat-subnet", Tenant: testTenant}.Build(),
				Spec: privatev1.SubnetSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetwork.GetObject().GetId()}.Build(),
				}.Build(),
				Status: privatev1.SubnetStatus_builder{State: privatev1.SubnetState_SUBNET_STATE_READY, Hub: "network-hub-a"}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			securityGroupsDao, err := dao.NewGenericDAO[*privatev1.SecurityGroup]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			group, err := securityGroupsDao.Create().SetObject(privatev1.SecurityGroup_builder{
				Metadata: privatev1.Metadata_builder{Name: "hub-compat-sg", Tenant: testTenant}.Build(),
				Spec: privatev1.SecurityGroupSpec_builder{
					VirtualNetwork: privatev1.VirtualNetworkLocalReference_builder{Id: virtualNetwork.GetObject().GetId()}.Build(),
				}.Build(),
				Status: privatev1.SecurityGroupStatus_builder{State: privatev1.SecurityGroupState_SECURITY_GROUP_STATE_READY, Hub: "network-hub-b"}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			name := fmt.Sprintf("test-%s", uuid.New()[24:32])
			response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{Name: name}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
						NetworkAttachment: privatev1.ClusterNetworkAttachment_builder{
							Subnet:         privatev1.SubnetLocalReference_builder{Id: subnet.GetObject().GetId()}.Build(),
							SecurityGroups: []*privatev1.SecurityGroupLocalReference{privatev1.SecurityGroupLocalReference_builder{Id: group.GetObject().GetId()}.Build()},
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(response).To(BeNil())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
			Expect(err.Error()).To(ContainSubstring("different Hub than the canonical networking Hub"))
			Expect(err.Error()).ToNot(ContainSubstring("network-hub-a"))
			Expect(err.Error()).ToNot(ContainSubstring("network-hub-b"))

			clustersDao, err := dao.NewGenericDAO[*privatev1.Cluster]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			stored, err := clustersDao.List().SetFilter(fmt.Sprintf("this.metadata.name == %q", name)).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(stored.GetItems()).To(BeEmpty())
		})

		It("creates automatic ExternalIPs without attachments and cleans them up", func() {
			poolDao, err := dao.NewGenericDAO[*privatev1.ExternalIPPool]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = poolDao.Create().SetObject(privatev1.ExternalIPPool_builder{
				Metadata: privatev1.Metadata_builder{Tenant: auth.SharedTenant}.Build(),
				Spec: privatev1.ExternalIPPoolSpec_builder{
					Cidrs:    []string{"192.0.2.0/24"},
					IpFamily: privatev1.IPFamily_IP_FAMILY_IPV4,
				}.Build(),
				Status: privatev1.ExternalIPPoolStatus_builder{
					State:     privatev1.ExternalIPPoolState_EXTERNAL_IP_POOL_STATE_READY,
					Total:     5,
					Available: 5,
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{Name: fmt.Sprintf("deferred-%s", uuid.New()[24:32])}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template:                 privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
						AutoExternalIpAttachment: proto.Bool(true),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			clusterID := response.GetObject().GetId()

			eipDao, err := dao.NewGenericDAO[*privatev1.ExternalIP]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			filter := fmt.Sprintf("this.metadata.labels['%s'] == '%s'", autoCreatedForLabel, clusterID)
			eipList, err := eipDao.List().SetFilter(filter).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(eipList.GetItems()).To(HaveLen(2))
			endpoints := map[string]bool{}
			for _, eip := range eipList.GetItems() {
				Expect(eip.GetMetadata().GetLabels()[autoCreatedLabel]).To(Equal("true"))
				Expect(eip.GetMetadata().GetLabels()).ToNot(HaveKey(autoAttachmentDeferredLabel))
				Expect(eip.GetMetadata().GetLabels()[autoCreatedKindLabel]).To(Equal("cluster"))
				Expect(eip.GetMetadata().GetAnnotations()[ownerReferenceAnnotation]).To(Equal(clusterID))
				endpoints[eip.GetMetadata().GetLabels()[autoCreatedEndpointLabel]] = true
			}
			Expect(endpoints).To(HaveKey("api"))
			Expect(endpoints).To(HaveKey("ingress"))

			eiaDao, err := dao.NewGenericDAO[*privatev1.ExternalIPAttachment]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
			Expect(err).ToNot(HaveOccurred())
			eiaList, err := eiaDao.List().SetFilter(filter).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(eiaList.GetItems()).To(BeEmpty())

			_, err = server.Delete(ctx, privatev1.ClustersDeleteRequest_builder{Id: clusterID}.Build())
			Expect(err).ToNot(HaveOccurred())
			eipList, err = eipDao.List().SetFilter(filter).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(eipList.GetItems()).To(BeEmpty())
		})

		It("Preserves direct add-on operators through create and get", func() {
			operators := []*privatev1.AddOnOperatorReference{
				privatev1.AddOnOperatorReference_builder{Id: "operator-1", Name: "operator-one"}.Build(),
				privatev1.AddOnOperatorReference_builder{Id: "operator-2", Name: "operator-two"}.Build(),
			}

			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{Name: fmt.Sprintf("test-%s", uuid.New()[24:32])}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template:       privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
						AddOnOperators: operators,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			getResponse, err := server.Get(ctx, privatev1.ClustersGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			storedOperators := getResponse.GetObject().GetSpec().GetAddOnOperators()
			Expect(storedOperators).To(HaveLen(len(operators)))
			for i, operator := range operators {
				Expect(storedOperators[i].GetId()).To(Equal(operator.GetId()))
				Expect(storedOperators[i].GetName()).To(Equal(operator.GetName()))
				Expect(storedOperators[i].GetShared()).To(BeTrue())
				Expect(storedOperators[i].GetProject()).To(BeEmpty())
			}
		})

		It("resolves a name-only add-on operator reference during create", func() {
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{Name: fmt.Sprintf("test-%s", uuid.New()[24:32])}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
						AddOnOperators: []*privatev1.AddOnOperatorReference{
							privatev1.AddOnOperatorReference_builder{Name: "operator-one"}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			operator := createResponse.GetObject().GetSpec().GetAddOnOperators()[0]
			Expect(operator.GetId()).To(Equal("operator-1"))
			Expect(operator.GetName()).To(Equal("operator-one"))
		})

		It("resolves transitive dependencies and deduplicates shared dependencies", func() {
			dependency := newTestAddOnOperator("operator-dependency", "operator-dependency", true)
			seedAddOnOperatorObject(ctx, dependency)

			first := newTestAddOnOperator("operator-first", "operator-first", true)
			first.SetDependencies([]*privatev1.AddOnOperatorLocalReference{
				privatev1.AddOnOperatorLocalReference_builder{Id: dependency.GetId()}.Build(),
			})
			seedAddOnOperatorObject(ctx, first)

			second := newTestAddOnOperator("operator-second", "operator-second", true)
			second.SetDependencies([]*privatev1.AddOnOperatorLocalReference{
				privatev1.AddOnOperatorLocalReference_builder{Id: dependency.GetId()}.Build(),
			})
			seedAddOnOperatorObject(ctx, second)

			object, err := createClusterWithAddOnOperators(ctx, server, []*privatev1.AddOnOperatorReference{
				privatev1.AddOnOperatorReference_builder{Id: first.GetId()}.Build(),
				privatev1.AddOnOperatorReference_builder{Id: second.GetId()}.Build(),
			})
			Expect(err).ToNot(HaveOccurred())

			ids := make([]string, 0, len(object.GetSpec().GetAddOnOperators()))
			for _, operator := range object.GetSpec().GetAddOnOperators() {
				ids = append(ids, operator.GetId())
			}
			Expect(ids).To(ConsistOf(first.GetId(), second.GetId(), dependency.GetId()))
		})

		It("rejects cluster creation when the expanded dependency closure exceeds the operator limit", func() {
			dependency := newTestAddOnOperator("operator-expansion-limit-dependency", "operator-expansion-limit-dependency", true)
			dependencyIDs := make([]string, 0, maxClusterAddOnOperators-1)
			for i := range maxClusterAddOnOperators - 1 {
				dependencyID := fmt.Sprintf("operator-expansion-limit-leaf-%d", i)
				seedAddOnOperatorObject(ctx, newTestAddOnOperator(dependencyID, dependencyID, true))
				dependencyIDs = append(dependencyIDs, dependencyID)
			}
			seedAddOnOperatorObject(ctx, dependency)

			root := newTestAddOnOperator("operator-expansion-limit-root", "operator-expansion-limit-root", true)
			root.SetDependencies([]*privatev1.AddOnOperatorLocalReference{
				privatev1.AddOnOperatorLocalReference_builder{Id: dependency.GetId()}.Build(),
			})
			seedAddOnOperatorObject(ctx, root)

			operatorServer, err := NewPrivateAddOnOperatorsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			dependencies := make([]*privatev1.AddOnOperatorLocalReference, 0, len(dependencyIDs))
			for _, dependencyID := range dependencyIDs {
				dependencies = append(dependencies,
					privatev1.AddOnOperatorLocalReference_builder{Id: dependencyID}.Build())
			}
			_, err = operatorServer.Update(ctx, privatev1.AddOnOperatorsUpdateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Id:           dependency.GetId(),
					Dependencies: dependencies,
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"dependencies"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = createClusterWithAddOnOperators(ctx, server, []*privatev1.AddOnOperatorReference{
				privatev1.AddOnOperatorReference_builder{Id: root.GetId()}.Build(),
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("maximum of 32 operators"))
		})

		It("allows requested roots that overlap the dependency closure", func() {
			rootIDs := make([]string, 0, maxClusterAddOnOperators)
			for i := range maxClusterAddOnOperators {
				operatorID := fmt.Sprintf("operator-overlapping-root-%d", i)
				rootIDs = append(rootIDs, operatorID)
			}
			for i, operatorID := range rootIDs {
				operator := newTestAddOnOperator(operatorID, operatorID, true)
				if i == 0 {
					operator.SetDependencies([]*privatev1.AddOnOperatorLocalReference{
						privatev1.AddOnOperatorLocalReference_builder{Id: rootIDs[1]}.Build(),
					})
				}
				seedAddOnOperatorObject(ctx, operator)
			}

			object, err := createClusterWithAddOnOperators(ctx, server, func() []*privatev1.AddOnOperatorReference {
				refs := make([]*privatev1.AddOnOperatorReference, 0, len(rootIDs))
				for _, rootID := range rootIDs {
					refs = append(refs, privatev1.AddOnOperatorReference_builder{Id: rootID}.Build())
				}
				return refs
			}())
			Expect(err).ToNot(HaveOccurred())
			resolvedIDs := make([]string, 0, len(object.GetSpec().GetAddOnOperators()))
			for _, operator := range object.GetSpec().GetAddOnOperators() {
				resolvedIDs = append(resolvedIDs, operator.GetId())
			}
			Expect(resolvedIDs).To(ConsistOf(rootIDs))
		})

		It("keeps existing cluster snapshots when dependency edges change", func() {
			first := newTestAddOnOperator("operator-snapshot-first", "operator-snapshot-first", true)
			second := newTestAddOnOperator("operator-snapshot-second", "operator-snapshot-second", true)
			root := newTestAddOnOperator("operator-snapshot-root", "operator-snapshot-root", true)
			root.SetDependencies([]*privatev1.AddOnOperatorLocalReference{
				privatev1.AddOnOperatorLocalReference_builder{Id: first.GetId()}.Build(),
			})
			seedAddOnOperatorObject(ctx, first)
			seedAddOnOperatorObject(ctx, second)
			seedAddOnOperatorObject(ctx, root)

			firstCluster, err := createClusterWithAddOnOperators(ctx, server, []*privatev1.AddOnOperatorReference{
				privatev1.AddOnOperatorReference_builder{Id: root.GetId()}.Build(),
			})
			Expect(err).ToNot(HaveOccurred())

			operatorServer, err := NewPrivateAddOnOperatorsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = operatorServer.Update(ctx, privatev1.AddOnOperatorsUpdateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Id: root.GetId(),
					Dependencies: []*privatev1.AddOnOperatorLocalReference{
						privatev1.AddOnOperatorLocalReference_builder{Id: second.GetId()}.Build(),
					},
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"dependencies"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			secondCluster, err := createClusterWithAddOnOperators(ctx, server, []*privatev1.AddOnOperatorReference{
				privatev1.AddOnOperatorReference_builder{Id: root.GetId()}.Build(),
			})
			Expect(err).ToNot(HaveOccurred())

			firstIDs := make([]string, 0, len(firstCluster.GetSpec().GetAddOnOperators()))
			for _, operator := range firstCluster.GetSpec().GetAddOnOperators() {
				firstIDs = append(firstIDs, operator.GetId())
			}
			secondIDs := make([]string, 0, len(secondCluster.GetSpec().GetAddOnOperators()))
			for _, operator := range secondCluster.GetSpec().GetAddOnOperators() {
				secondIDs = append(secondIDs, operator.GetId())
			}
			Expect(firstIDs).To(ConsistOf(root.GetId(), first.GetId()))
			Expect(secondIDs).To(ConsistOf(root.GetId(), second.GetId()))
		})

		It("rejects circular dependencies", func() {
			first := newTestAddOnOperator("operator-cycle-a", "operator-cycle-a", true)
			first.SetDependencies([]*privatev1.AddOnOperatorLocalReference{
				privatev1.AddOnOperatorLocalReference_builder{Id: "operator-cycle-b"}.Build(),
			})
			second := newTestAddOnOperator("operator-cycle-b", "operator-cycle-b", true)
			second.SetDependencies([]*privatev1.AddOnOperatorLocalReference{
				privatev1.AddOnOperatorLocalReference_builder{Id: first.GetId()}.Build(),
			})
			seedAddOnOperatorObject(ctx, first)
			seedAddOnOperatorObject(ctx, second)

			_, err := createClusterWithAddOnOperators(ctx, server, []*privatev1.AddOnOperatorReference{
				privatev1.AddOnOperatorReference_builder{Id: first.GetId()}.Build(),
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("dependency cycle detected"))
		})

		It("rejects mutually exclusive operators", func() {
			first := newTestAddOnOperator("operator-exclusion-a", "operator-exclusion-a", true)
			first.SetExclusions([]*privatev1.AddOnOperatorLocalReference{
				privatev1.AddOnOperatorLocalReference_builder{Id: "operator-exclusion-b"}.Build(),
			})
			second := newTestAddOnOperator("operator-exclusion-b", "operator-exclusion-b", true)
			seedAddOnOperatorObject(ctx, first)
			seedAddOnOperatorObject(ctx, second)

			_, err := createClusterWithAddOnOperators(ctx, server, []*privatev1.AddOnOperatorReference{
				privatev1.AddOnOperatorReference_builder{Id: first.GetId()}.Build(),
				privatev1.AddOnOperatorReference_builder{Id: second.GetId()}.Build(),
			})
			Expect(err).To(HaveOccurred())
			expectAddOnOperatorFieldViolation(err, "spec.add_on_operators[0]")
			Expect(grpcstatus.Convert(err).Message()).To(ContainSubstring(first.GetId()))
			Expect(grpcstatus.Convert(err).Message()).To(ContainSubstring(second.GetId()))
		})

		It("rejects an exclusion declared by either selected operator", func() {
			first := newTestAddOnOperator("operator-reverse-exclusion-a", "operator-reverse-exclusion-a", true)
			second := newTestAddOnOperator("operator-reverse-exclusion-b", "operator-reverse-exclusion-b", true)
			second.SetExclusions([]*privatev1.AddOnOperatorLocalReference{
				privatev1.AddOnOperatorLocalReference_builder{Id: first.GetId()}.Build(),
			})
			seedAddOnOperatorObject(ctx, first)
			seedAddOnOperatorObject(ctx, second)

			_, err := createClusterWithAddOnOperators(ctx, server, []*privatev1.AddOnOperatorReference{
				privatev1.AddOnOperatorReference_builder{Id: first.GetId()}.Build(),
				privatev1.AddOnOperatorReference_builder{Id: second.GetId()}.Build(),
			})
			Expect(err).To(HaveOccurred())
			expectAddOnOperatorFieldViolation(err, "spec.add_on_operators[1]")
		})

		It("rejects an operator below its minimum cluster version", func() {
			operator := newTestAddOnOperator("operator-versioned", "operator-versioned", true)
			operator.SetMinOcpVersion("4.18.0")
			seedAddOnOperatorObject(ctx, operator)

			_, err := createClusterWithAddOnOperators(ctx, server, []*privatev1.AddOnOperatorReference{
				privatev1.AddOnOperatorReference_builder{Id: operator.GetId()}.Build(),
			})
			Expect(err).To(HaveOccurred())
			expectAddOnOperatorFieldViolation(err, "spec.add_on_operators[0]")
			Expect(grpcstatus.Convert(err).Message()).To(ContainSubstring("4.18.0"))
		})

		It("rejects an operator above its maximum cluster version", func() {
			operator := newTestAddOnOperator("operator-versioned-max", "operator-versioned-max", true)
			operator.SetMaxOcpVersion("4.16.0")
			seedAddOnOperatorObject(ctx, operator)

			_, err := createClusterWithAddOnOperators(ctx, server, []*privatev1.AddOnOperatorReference{
				privatev1.AddOnOperatorReference_builder{Id: operator.GetId()}.Build(),
			})
			Expect(err).To(HaveOccurred())
			expectAddOnOperatorFieldViolation(err, "spec.add_on_operators[0]")
			Expect(grpcstatus.Convert(err).Message()).To(ContainSubstring("4.16.0"))
		})

		It("accepts an operator at inclusive cluster version bounds", func() {
			operator := newTestAddOnOperator("operator-version-bound", "operator-version-bound", true)
			operator.SetMinOcpVersion("4.17.0")
			operator.SetMaxOcpVersion("4.17.0")
			seedAddOnOperatorObject(ctx, operator)

			_, err := createClusterWithAddOnOperators(ctx, server, []*privatev1.AddOnOperatorReference{
				privatev1.AddOnOperatorReference_builder{Id: operator.GetId()}.Build(),
			})
			Expect(err).ToNot(HaveOccurred())
		})

		It("rejects an unpublished operator", func() {
			operator := newTestAddOnOperator("operator-unpublished", "operator-unpublished", false)
			seedAddOnOperatorObject(ctx, operator)

			_, err := createClusterWithAddOnOperators(ctx, server, []*privatev1.AddOnOperatorReference{
				privatev1.AddOnOperatorReference_builder{Id: operator.GetId()}.Build(),
			})
			Expect(err).To(HaveOccurred())
			expectAddOnOperatorFieldViolation(err, "spec.add_on_operators[0]")
			Expect(grpcstatus.Convert(err).Message()).To(ContainSubstring("not published"))
		})

		It("accepts and canonicalizes an unpublished dependency", func() {
			dependency := newTestAddOnOperator("operator-unpublished-dependency", "operator-unpublished-dependency", false)
			seedAddOnOperatorObject(ctx, dependency)
			root := newTestAddOnOperator("operator-with-unpublished-dependency", "operator-with-unpublished-dependency", true)
			root.SetDependencies([]*privatev1.AddOnOperatorLocalReference{
				privatev1.AddOnOperatorLocalReference_builder{Id: dependency.GetId()}.Build(),
			})
			seedAddOnOperatorObject(ctx, root)

			object, err := createClusterWithAddOnOperators(ctx, server, []*privatev1.AddOnOperatorReference{
				privatev1.AddOnOperatorReference_builder{Id: root.GetId()}.Build(),
			})
			Expect(err).ToNot(HaveOccurred())
			operators := object.GetSpec().GetAddOnOperators()
			Expect(operators).To(HaveLen(2))
			Expect(operators[0].GetId()).To(Equal(dependency.GetId()))
			Expect(operators[0].GetName()).To(Equal(dependency.GetMetadata().GetName()))
			Expect(operators[1].GetId()).To(Equal(root.GetId()))
			Expect(operators[1].GetName()).To(Equal(root.GetMetadata().GetName()))
		})

		It("rejects an operator reference whose id and name disagree", func() {
			operator := newTestAddOnOperator("operator-id-name", "operator-id-name", true)
			seedAddOnOperatorObject(ctx, operator)

			_, err := createClusterWithAddOnOperators(ctx, server, []*privatev1.AddOnOperatorReference{
				privatev1.AddOnOperatorReference_builder{
					Id:   operator.GetId(),
					Name: "different-name",
				}.Build(),
			})
			Expect(err).To(HaveOccurred())
			expectAddOnOperatorFieldViolation(err, "spec.add_on_operators[0]")
			Expect(grpcstatus.Convert(err).Message()).To(ContainSubstring("id and name"))
		})

		It("honors an explicit shared scope for an operator name", func() {
			shared := newTestAddOnOperator("operator-shared", "same-operator-name", true)
			seedAddOnOperatorObject(ctx, shared)

			local := newTestAddOnOperator("operator-local", "same-operator-name", true)
			local.GetMetadata().SetTenant(testTenant)
			seedAddOnOperatorObject(ctx, local)

			object, err := createClusterWithAddOnOperators(ctx, server, []*privatev1.AddOnOperatorReference{
				privatev1.AddOnOperatorReference_builder{Name: "same-operator-name", Shared: true}.Build(),
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(object.GetSpec().GetAddOnOperators()).To(HaveLen(1))
			resolved := object.GetSpec().GetAddOnOperators()[0]
			Expect(resolved.GetId()).To(Equal(shared.GetId()))
			Expect(resolved.GetName()).To(Equal(shared.GetMetadata().GetName()))
			Expect(resolved.GetShared()).To(BeTrue())
			Expect(resolved.GetProject()).To(Equal(shared.GetMetadata().GetProject()))

			updateResponse, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: object.GetId(),
					Spec: privatev1.ClusterSpec_builder{
						AddOnOperators: []*privatev1.AddOnOperatorReference{
							privatev1.AddOnOperatorReference_builder{
								Name:   shared.GetMetadata().GetName(),
								Shared: true,
							}.Build(),
						},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.add_on_operators"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetSpec().GetAddOnOperators()).To(HaveLen(1))
			Expect(updateResponse.GetObject().GetSpec().GetAddOnOperators()[0].GetShared()).To(BeTrue())
		})

		It("resolves an unscoped shared operator name from a non-default project", func() {
			operator := newTestAddOnOperator("operator-project-scope", "operator-project-scope", true)
			seedAddOnOperatorObject(ctx, operator)
			projectsDAO, err := dao.NewGenericDAO[*privatev1.Project]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = projectsDAO.Create().SetObject(privatev1.Project_builder{
				Metadata: privatev1.Metadata_builder{Name: "workloads", Tenant: testTenant}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{
						Name:    fmt.Sprintf("test-%s", uuid.New()[24:32]),
						Project: "workloads",
					}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
						AddOnOperators: []*privatev1.AddOnOperatorReference{
							privatev1.AddOnOperatorReference_builder{Name: operator.GetMetadata().GetName()}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetSpec().GetAddOnOperators()).To(HaveLen(1))
			Expect(response.GetObject().GetSpec().GetAddOnOperators()[0].GetId()).To(Equal(operator.GetId()))
		})

		It("rejects a deleted operator", func() {
			operator := newTestAddOnOperator("operator-deleted", "operator-deleted", true)
			seedAddOnOperatorObject(ctx, operator)
			operatorDao, err := dao.NewGenericDAO[*privatev1.AddOnOperator]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = operatorDao.Delete().SetId(operator.GetId()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			_, err = createClusterWithAddOnOperators(ctx, server, []*privatev1.AddOnOperatorReference{
				privatev1.AddOnOperatorReference_builder{Id: operator.GetId()}.Build(),
			})
			Expect(err).To(HaveOccurred())
			expectAddOnOperatorFieldViolation(err, "spec.add_on_operators[0]")
			Expect(grpcstatus.Convert(err).Message()).To(ContainSubstring("deleted"))
		})

		It("rejects an expanded operator set over the maximum size", func() {
			operators := make([]*privatev1.AddOnOperatorReference, 0, 32)
			for i := range 32 {
				dependencyID := fmt.Sprintf("operator-limit-dependency-%d", i)
				seedAddOnOperatorObject(ctx, newTestAddOnOperator(dependencyID, dependencyID, true))

				operatorID := fmt.Sprintf("operator-limit-%d", i)
				operator := newTestAddOnOperator(operatorID, operatorID, true)
				operator.SetDependencies([]*privatev1.AddOnOperatorLocalReference{
					privatev1.AddOnOperatorLocalReference_builder{Id: dependencyID}.Build(),
				})
				seedAddOnOperatorObject(ctx, operator)
				operators = append(operators, privatev1.AddOnOperatorReference_builder{Id: operatorID}.Build())
			}

			_, err := createClusterWithAddOnOperators(ctx, server, operators)
			Expect(err).To(HaveOccurred())
			expectAddOnOperatorFieldViolation(err, "spec.add_on_operators")
			Expect(grpcstatus.Convert(err).Message()).To(ContainSubstring("32"))
		})

		It("Rejects changing add-on operators with a field mask", func() {
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{Name: fmt.Sprintf("test-%s", uuid.New()[24:32])}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template:       privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
						AddOnOperators: []*privatev1.AddOnOperatorReference{privatev1.AddOnOperatorReference_builder{Id: "operator-1", Name: "operator-one"}.Build()},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: createResponse.GetObject().GetId(),
					Spec: privatev1.ClusterSpec_builder{
						AddOnOperators: []*privatev1.AddOnOperatorReference{privatev1.AddOnOperatorReference_builder{Id: "operator-2", Name: "operator-two"}.Build()},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.add_on_operators"}},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("spec.add_on_operators"))
			Expect(status.Message()).To(ContainSubstring("immutable"))
		})

		It("Rejects changing add-on operators on a full-object update", func() {
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{Name: fmt.Sprintf("test-%s", uuid.New()[24:32])}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template:       privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
						AddOnOperators: []*privatev1.AddOnOperatorReference{privatev1.AddOnOperatorReference_builder{Id: "operator-1", Name: "operator-one"}.Build()},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			updated := proto.Clone(createResponse.GetObject()).(*privatev1.Cluster)
			updated.GetSpec().SetAddOnOperators([]*privatev1.AddOnOperatorReference{
				privatev1.AddOnOperatorReference_builder{Id: "operator-2", Name: "operator-two"}.Build(),
			})
			_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{Object: updated}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("spec.add_on_operators"))
			Expect(status.Message()).To(ContainSubstring("immutable"))
		})

		It("Rejects a spec update when the masked spec is omitted", func() {
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{Name: fmt.Sprintf("test-%s", uuid.New()[24:32])}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template:       privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
						AddOnOperators: []*privatev1.AddOnOperatorReference{privatev1.AddOnOperatorReference_builder{Id: "operator-1", Name: "operator-one"}.Build()},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			for _, path := range []string{"spec.add_on_operators", "spec.version"} {
				_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object:     privatev1.Cluster_builder{Id: createResponse.GetObject().GetId()}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{path}},
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(Equal("object and spec are required"))
			}
		})

		It("Preserves add-on operators on a full-object metadata update", func() {
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{Name: fmt.Sprintf("test-%s", uuid.New()[24:32])}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template:       privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
						AddOnOperators: []*privatev1.AddOnOperatorReference{privatev1.AddOnOperatorReference_builder{Id: "operator-1", Name: "operator-one"}.Build()},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			updateResponse, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: createResponse.GetObject().GetId(),
					Metadata: privatev1.Metadata_builder{
						Name:   createResponse.GetObject().GetMetadata().GetName(),
						Labels: map[string]string{"example.com/my-label": "my-value"},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetSpec().GetAddOnOperators()).To(HaveLen(1))
			Expect(updateResponse.GetObject().GetSpec().GetAddOnOperators()[0].GetName()).To(Equal("operator-one"))
			Expect(proto.Equal(updateResponse.GetObject().GetSpec(), createResponse.GetObject().GetSpec())).To(BeTrue())
		})

		It("preserves add-on operators when the referenced operator is deleted", func() {
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{Name: fmt.Sprintf("test-%s", uuid.New()[24:32])}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
						AddOnOperators: []*privatev1.AddOnOperatorReference{
							privatev1.AddOnOperatorReference_builder{Id: "operator-1", Name: "operator-one"}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			operatorDao, err := dao.NewGenericDAO[*privatev1.AddOnOperator]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = operatorDao.Delete().SetId("operator-1").Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			updateResponse, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: createResponse.GetObject().GetId(),
					Metadata: privatev1.Metadata_builder{
						Name:   createResponse.GetObject().GetMetadata().GetName(),
						Labels: map[string]string{"example.com/my-label": "my-value"},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetSpec().GetAddOnOperators()[0].GetId()).To(Equal("operator-1"))
		})

		It("Creates object with template specified by name", func() {
			// Create the object:
			response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
					}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Name: "my-template-name"}.Build(),
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).ToNot(BeEmpty())

			// Verify that the template name was replaced by the identifier and name is preserved:
			Expect(object.GetSpec().GetTemplate().GetId()).To(Equal("my-template-id"))
			Expect(object.GetSpec().GetTemplate().GetName()).To(Equal("my-template-name"))
		})

		It("Fails when creating object with non-existent template name", func() {
			_, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
					}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "does-not-exist"}.Build(),
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(Equal(
				"template 'does-not-exist' not found",
			))
		})

		DescribeTable("checks CaaS NodeSet hardware lifecycle without rewriting resolved references",
			func(path string, shared bool, project string) {
				ref := privatev1.BareMetalInstanceTypeReference_builder{
					Id: "acme-bmit-id", Name: "acme-bmit-name", Shared: shared, Project: project,
				}.Build()
				original := proto.Clone(ref)
				cluster := newCaaSNodeSetCreateRequest("reference-selectors", map[string]*privatev1.ClusterNodeSet{
					"workers": privatev1.ClusterNodeSet_builder{Size: proto.Int32(2), BaremetalInstanceType: ref}.Build(),
				}, nil).GetObject()
				cluster.GetMetadata().SetTenant(testTenant)
				var err error
				switch path {
				case "create":
					err = server.resolveClusterNodeSets(ctx, cluster)
				case "update":
					current := privatev1.Cluster_builder{Spec: privatev1.ClusterSpec_builder{}.Build()}.Build()
					err = server.prepareUpdatedClusterNodeSets(ctx, current, cluster,
						cluster.GetSpec().GetNodeSets(), &fieldmaskpb.FieldMask{Paths: []string{"spec.node_sets"}}, false)
				case "fabric":
					err = server.resolveFabricInterfaces(ctx, cluster)
				}
				Expect(err).ToNot(HaveOccurred())
				if path == "fabric" {
					Expect(cluster.GetSpec().GetNodeSets()["workers"].GetFabricInterface()).To(Equal("data-0"))
					return
				}
				Expect(proto.Equal(ref, original)).To(BeTrue())
			},
			Entry("create without shared=true", "create", false, ""),
			Entry("update without shared=true", "update", false, ""),
			Entry("fabric without shared=true", "fabric", false, ""),
			Entry("create with a project", "create", true, "other"),
			Entry("update with a project", "update", true, "other"),
			Entry("fabric with a project", "fabric", true, "other"),
		)

		It("resolves an explicitly shared name-only CaaS NodeSet reference without mutating the request", func() {
			request := newCaaSNodeSetCreateRequest("shared-name-only", map[string]*privatev1.ClusterNodeSet{
				"workers": privatev1.ClusterNodeSet_builder{
					Size: proto.Int32(2),
					BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{
						Name: "acme-bmit-name", Shared: true,
					}.Build(),
				}.Build(),
			}, nil)
			original := proto.Clone(request)
			response, err := server.Create(ctx, request)
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(request, original)).To(BeTrue())
			resolved := response.GetObject().GetSpec().GetNodeSets()["workers"].GetBaremetalInstanceType()
			Expect(resolved.GetId()).To(Equal("acme-bmit-id"))
			Expect(resolved.GetName()).To(Equal("acme-bmit-name"))
			Expect(resolved.GetShared()).To(BeTrue())
		})

		DescribeTable("rejects unavailable resolved CaaS NodeSet hardware", func(path, state string) {
			id := "acme-bmit-id"
			if state == "missing" {
				id = "missing-bmit-id"
			} else {
				object, err := getReferenceResource(ctx, server.bareMetalInstanceTypesDao, id)
				Expect(err).ToNot(HaveOccurred())
				object.GetMetadata().SetFinalizers([]string{"test-finalizer"})
				_, err = server.bareMetalInstanceTypesDao.Update().SetObject(object).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				_, err = server.bareMetalInstanceTypesDao.Delete().SetId(id).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			}
			cluster := newCaaSNodeSetCreateRequest("unavailable-hardware", map[string]*privatev1.ClusterNodeSet{
				"workers": privatev1.ClusterNodeSet_builder{
					Size: proto.Int32(2),
					BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{
						Id: id, Name: "acme-bmit-name", Shared: true,
					}.Build(),
				}.Build(),
			}, nil).GetObject()
			var err error
			if path == "create" {
				err = server.resolveClusterNodeSets(ctx, cluster)
			} else {
				current := privatev1.Cluster_builder{Spec: privatev1.ClusterSpec_builder{}.Build()}.Build()
				err = server.prepareUpdatedClusterNodeSets(ctx, current, cluster,
					cluster.GetSpec().GetNodeSets(), &fieldmaskpb.FieldMask{Paths: []string{"spec.node_sets"}}, false)
			}
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(err).To(MatchError(ContainSubstring("node_sets.workers.baremetal_instance_type")))
			if state == "missing" {
				Expect(err).To(MatchError(ContainSubstring("not found")))
			} else {
				Expect(err).To(MatchError(ContainSubstring("deleted")))
			}
		},
			Entry("deleted type on create", "create", "deleted"),
			Entry("deleted type on update", "update", "deleted"),
			Entry("missing type on create", "create", "missing"),
			Entry("missing type on update", "update", "missing"),
		)

		It("validates the fabric port using the exact shared BMIT resolved for the NodeSet", func() {
			seedCaaSTestBareMetalInstanceType(ctx, "shared-fabric-worker-id", "fabric-worker", auth.SharedTenant,
				[]*privatev1.BareMetalNetworkPortSpec{
					privatev1.BareMetalNetworkPortSpec_builder{Name: "data-0", Role: "fabric"}.Build(),
				})
			// This different tenant BMIT has a unique name, but its name equals the shared BMIT ID.
			// A second lookup using `id == key || name == key` must not replace/ambiguate the
			// already-resolved shared object used for fabric validation.
			seedCaaSTestBareMetalInstanceType(ctx, "tenant-other-worker-id", "shared-fabric-worker-id", testTenant, nil)
			response, err := server.Create(ctx, newCaaSNodeSetCreateRequest("shared-fabric", map[string]*privatev1.ClusterNodeSet{
				"workers": privatev1.ClusterNodeSet_builder{
					Size: proto.Int32(2),
					BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{
						Name: "fabric-worker", Shared: true,
					}.Build(),
				}.Build(),
			}, privatev1.ClusterNetworkAttachment_builder{
				Subnet: privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
			}.Build()))
			Expect(err).ToNot(HaveOccurred())
			resolved := response.GetObject().GetSpec().GetNodeSets()["workers"]
			Expect(resolved.GetBaremetalInstanceType().GetId()).To(Equal("shared-fabric-worker-id"))
			Expect(resolved.GetBaremetalInstanceType().GetShared()).To(BeTrue())
			Expect(resolved.GetFabricInterface()).To(Equal("data-0"))
		})

		DescribeTable("preserves hardware immutability without validating reference selectors on update",
			func(nodeSetName string, shared bool, project string) {
				created, err := server.Create(ctx, newCaaSNodeSetCreateRequest("invalid-scope-update", map[string]*privatev1.ClusterNodeSet{
					"compute": privatev1.ClusterNodeSet_builder{
						Size:                  proto.Int32(2),
						BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id", Shared: true}.Build(),
					}.Build(),
				}, nil))
				Expect(err).ToNot(HaveOccurred())
				request := privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: created.GetObject().GetId(),
						Spec: privatev1.ClusterSpec_builder{NodeSets: map[string]*privatev1.ClusterNodeSet{
							nodeSetName: privatev1.ClusterNodeSet_builder{
								Size: proto.Int32(1),
								BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{
									Name: "acme-gpu-name", Shared: shared, Project: project,
								}.Build(),
							}.Build(),
						}}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.node_sets"}},
				}.Build()
				original := proto.Clone(request)
				response, err := server.Update(ctx, request)
				expectedSpec := created.GetObject().GetSpec()
				if nodeSetName == "compute" {
					// Existing hardware is immutable, so this fails before reference resolution.
					Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
					Expect(err).To(MatchError(ContainSubstring("baremetal_instance_type is immutable")))
				} else {
					Expect(err).ToNot(HaveOccurred())
					expectedSpec = response.GetObject().GetSpec()
					ref := expectedSpec.GetNodeSets()[nodeSetName].GetBaremetalInstanceType()
					Expect(ref.GetId()).To(Equal("acme-gpu-bmit-id"))
					Expect(ref.GetName()).To(Equal("acme-gpu-name"))
					Expect(ref.GetShared()).To(Equal(shared))
					Expect(ref.GetProject()).To(Equal(project))
				}
				Expect(proto.Equal(request, original)).To(BeTrue())
				stored, err := server.Get(ctx, privatev1.ClustersGetRequest_builder{Id: created.GetObject().GetId()}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(proto.Equal(stored.GetObject().GetSpec(), expectedSpec)).To(BeTrue())
			},
			Entry("new NodeSet without shared=true", "gpu", false, ""),
			Entry("changed NodeSet without shared=true", "compute", false, ""),
			// The fixture resource lives in shared/""; after
			// OSAC-6381 the validator scopes name lookups to the
			// resolved project, so these entries must use the
			// resource's actual project.
			Entry("new NodeSet with shared=true", "gpu", true, ""),
			Entry("changed NodeSet with shared=true", "compute", true, ""),
		)

		It("canonicalizes newly added shared NodeSets on update and preserves them on size-only updates", func() {
			created, err := server.Create(ctx, newCaaSNodeSetCreateRequest("shared-update", map[string]*privatev1.ClusterNodeSet{
				"compute": privatev1.ClusterNodeSet_builder{
					Size: proto.Int32(2),
					BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{
						Id: "acme-bmit-id", Shared: true,
					}.Build(),
				}.Build(),
			}, nil))
			Expect(err).ToNot(HaveOccurred())

			updated, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: created.GetObject().GetId(),
					Spec: privatev1.ClusterSpec_builder{NodeSets: map[string]*privatev1.ClusterNodeSet{
						"compute": privatev1.ClusterNodeSet_builder{
							Size: proto.Int32(2),
							BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{
								Id: "acme-bmit-id", Shared: true,
							}.Build(),
						}.Build(),
						"gpu": privatev1.ClusterNodeSet_builder{
							Size: proto.Int32(1),
							BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{
								Name: "acme-gpu-name", Shared: true,
							}.Build(),
						}.Build(),
					}}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.node_sets"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			gpu := updated.GetObject().GetSpec().GetNodeSets()["gpu"]
			Expect(gpu.GetBaremetalInstanceType().GetId()).To(Equal("acme-gpu-bmit-id"))
			Expect(gpu.GetBaremetalInstanceType().GetName()).To(Equal("acme-gpu-name"))
			Expect(gpu.GetBaremetalInstanceType().GetShared()).To(BeTrue())

			sized, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: created.GetObject().GetId(),
					Spec: privatev1.ClusterSpec_builder{NodeSets: map[string]*privatev1.ClusterNodeSet{
						"gpu": privatev1.ClusterNodeSet_builder{Size: proto.Int32(4)}.Build(),
					}}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.node_sets.gpu.size"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			gpu = sized.GetObject().GetSpec().GetNodeSets()["gpu"]
			Expect(gpu.GetSize()).To(Equal(int32(4)))
			Expect(gpu.GetBaremetalInstanceType().GetId()).To(Equal("acme-gpu-bmit-id"))
			Expect(gpu.GetBaremetalInstanceType().GetShared()).To(BeTrue())
		})

		It("preserves legacy tenant BMIT and fabric interface when only scaling a NodeSet", func() {
			seedCaaSTestBareMetalInstanceType(ctx, "legacy-tenant-bmit-id", "legacy-tenant-bmit", testTenant,
				[]*privatev1.BareMetalNetworkPortSpec{
					privatev1.BareMetalNetworkPortSpec_builder{Name: "legacy-fabric", Role: "fabric"}.Build(),
				})
			clustersDao, err := dao.NewGenericDAO[*privatev1.Cluster]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			// Simulate an existing Cluster created before CaaS enforced shared-only BMITs.
			created, err := clustersDao.Create().SetObject(privatev1.Cluster_builder{
				Metadata: privatev1.Metadata_builder{Name: "legacy-bmit-scale", Tenant: testTenant}.Build(),
				Spec: privatev1.ClusterSpec_builder{
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					NodeSets: map[string]*privatev1.ClusterNodeSet{
						"workers": privatev1.ClusterNodeSet_builder{
							Size:                  proto.Int32(2),
							BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "legacy-tenant-bmit-id", Name: "legacy-tenant-bmit"}.Build(),
							FabricInterface:       "legacy-fabric",
						}.Build(),
					},
					NetworkAttachment: privatev1.ClusterNetworkAttachment_builder{
						Subnet: privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
					}.Build(),
				}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			updated, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: created.GetObject().GetId(),
					Spec: privatev1.ClusterSpec_builder{NodeSets: map[string]*privatev1.ClusterNodeSet{
						"workers": privatev1.ClusterNodeSet_builder{Size: proto.Int32(3)}.Build(),
					}}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.node_sets.workers.size"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			workers := updated.GetObject().GetSpec().GetNodeSets()["workers"]
			Expect(workers.GetSize()).To(Equal(int32(3)))
			Expect(workers.GetBaremetalInstanceType().GetId()).To(Equal("legacy-tenant-bmit-id"))
			Expect(workers.GetBaremetalInstanceType().GetName()).To(Equal("legacy-tenant-bmit"))
			Expect(workers.GetBaremetalInstanceType().GetShared()).To(BeFalse())
			Expect(workers.GetFabricInterface()).To(Equal("legacy-fabric"))
		})

		It("rejects a size-only mask that adds a NodeSet without a hardware type", func() {
			created, err := server.Create(ctx, newCaaSNodeSetCreateRequest("size-only-new-node-set", map[string]*privatev1.ClusterNodeSet{
				"workers": privatev1.ClusterNodeSet_builder{
					Size:                  proto.Int32(2),
					BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id", Shared: true}.Build(),
				}.Build(),
			}, nil))
			Expect(err).ToNot(HaveOccurred())

			_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: created.GetObject().GetId(),
					Spec: privatev1.ClusterSpec_builder{NodeSets: map[string]*privatev1.ClusterNodeSet{
						"gpu": privatev1.ClusterNodeSet_builder{Size: proto.Int32(1)}.Build(),
					}}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.node_sets.gpu.size"}},
			}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(err).To(MatchError(ContainSubstring("node_sets")))
		})

		It("Creates object with template specified by name and bare metal instance type in node set", func() {
			// Create a cluster specifying the template by name and the bare metal instance type by name:
			response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
					}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Name: "my-template-name"}.Build(),
						NodeSets: map[string]*privatev1.ClusterNodeSet{
							"compute": privatev1.ClusterNodeSet_builder{
								BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Name: "acme-bmit-name", Shared: true}.Build(),
								Size:                  proto.Int32(7),
							}.Build(),
						},
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).ToNot(BeEmpty())

			// Verify that the template and bare metal instance type names were replaced by the
			// identifiers and metadata names are preserved on the resolved references:
			Expect(object.GetSpec().GetTemplate().GetId()).To(Equal("my-template-id"))
			Expect(object.GetSpec().GetTemplate().GetName()).To(Equal("my-template-name"))
			nodeSets := object.GetSpec().GetNodeSets()
			Expect(nodeSets).To(HaveKey("compute"))
			nodeSet := nodeSets["compute"]
			Expect(nodeSet.GetBaremetalInstanceType().GetId()).To(Equal("acme-bmit-id"))
			Expect(nodeSet.GetBaremetalInstanceType().GetName()).To(Equal("acme-bmit-name"))
		})

		It("Fails when creating object with non-existent bare metal instance type name", func() {
			_, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
					}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
						NodeSets: map[string]*privatev1.ClusterNodeSet{
							"compute": privatev1.ClusterNodeSet_builder{
								BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Name: "does-not-exist", Shared: true}.Build(),
								Size:                  proto.Int32(5),
							}.Build(),
						},
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("does-not-exist"))
		})

		It("Accepts an additional node set with a valid bare metal instance type", func() {
			response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
					}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
						NodeSets: map[string]*privatev1.ClusterNodeSet{
							"does-not-exist": privatev1.ClusterNodeSet_builder{
								BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id", Shared: true}.Build(),
								Size:                  proto.Int32(5),
							}.Build(),
						},
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			nodes := response.GetObject().GetSpec().GetNodeSets()
			Expect(nodes).To(HaveLen(1))
			Expect(nodes["does-not-exist"].GetBaremetalInstanceType().GetId()).To(Equal("acme-bmit-id"))
			Expect(nodes["does-not-exist"].GetBaremetalInstanceType().GetName()).To(Equal("acme-bmit-name"))
		})

		It("Rejects an additional node set without bare metal instance type", func() {
			_, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
					}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
						NodeSets: map[string]*privatev1.ClusterNodeSet{
							"does-not-exist": privatev1.ClusterNodeSet_builder{
								Size: proto.Int32(5),
							}.Build(),
						},
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("bare metal instance type for node set 'does-not-exist' is required"))
		})

		It("rejects a cluster without request or catalog node sets", func() {
			// The template cannot supply node sets.
			_, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
					}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "no-bmit-template-id"}.Build(),
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("at least one node set"))
		})

		It("Returns 'already exists' when creating object with existing identifier", func() {
			// Create an object with a specific identifier:
			id := uuid.New()
			_, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: id,
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
					}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Try to create another object with the same identifier:
			name := fmt.Sprintf("test-%s", uuid.New()[24:32])
			_, err = server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: id,
					Metadata: privatev1.Metadata_builder{
						Name: name,
					}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.AlreadyExists))
			Expect(status.Message()).To(Equal(fmt.Sprintf("cluster with identifier '%s' and name '%s' already exists", id, name)))
		})

		It("List objects", func() {
			// Create a few objects:
			const count = 10
			for i := range count {
				_, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: fmt.Sprintf("my-template-id-%d", i)}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: fmt.Sprintf("my-hub-id-%d", i),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects:
			response, err := server.List(ctx, privatev1.ClustersListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			items := response.GetItems()
			Expect(items).To(HaveLen(count))
		})

		It("List objects with limit", func() {
			// Create a few objects:
			const count = 10
			for i := range count {
				_, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: fmt.Sprintf("my-template-id-%d", i)}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: fmt.Sprintf("my-hub-id-%d", i),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects:
			response, err := server.List(ctx, privatev1.ClustersListRequest_builder{
				Limit: new(int32(1)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", 1))
		})

		It("List objects with offset", func() {
			// Create a few objects:
			const count = 10
			for i := range count {
				_, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: fmt.Sprintf("my-template-id-%d", i)}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: fmt.Sprintf("my-hub-id-%d", i),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects:
			response, err := server.List(ctx, privatev1.ClustersListRequest_builder{
				Offset: new(int32(1)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", count-1))
		})

		It("List objects with filter", func() {
			// Create a few objects:
			const count = 10
			var objects []*privatev1.Cluster
			for i := range count {
				response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: fmt.Sprintf("my-template-id-%d", i)}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: fmt.Sprintf("my-hub-%d", i),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				objects = append(objects, response.GetObject())
			}

			// List the objects:
			for _, object := range objects {
				response, err := server.List(ctx, privatev1.ClustersListRequest_builder{
					Filter: new(fmt.Sprintf("this.id == '%s'", object.GetId())),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetSize()).To(BeNumerically("==", 1))
				Expect(response.GetItems()[0].GetId()).To(Equal(object.GetId()))
			}
		})

		It("Get object", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
					}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Get it:
			getResponse, err := server.Get(ctx, privatev1.ClustersGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(createResponse.GetObject(), getResponse.GetObject())).To(BeTrue())
		})

		It("Canonicalizes network CIDRs on Update", func() {
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{Name: "test-cluster"}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			updateResponse, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: object.GetId(),
					Spec: privatev1.ClusterSpec_builder{
						Template: object.GetSpec().GetTemplate(),
						Network: privatev1.ClusterNetwork_builder{
							PodCidr:     new("10.128.0.5/14"),
							ServiceCidr: new("172.30.1.0/16"),
						}.Build(),
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.network.pod_cidr", "spec.network.service_cidr"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			network := updateResponse.GetObject().GetSpec().GetNetwork()
			Expect(network.GetPodCidr()).To(Equal("10.128.0.0/14"))
			Expect(network.GetServiceCidr()).To(Equal("172.30.0.0/16"))

			getResponse, err := server.Get(ctx, privatev1.ClustersGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			network = getResponse.GetObject().GetSpec().GetNetwork()
			Expect(network.GetPodCidr()).To(Equal("10.128.0.0/14"))
			Expect(network.GetServiceCidr()).To(Equal("172.30.0.0/16"))
		})

		It("Update object", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{Name: "test-cluster"}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			name := object.GetMetadata().GetName()
			// Update the object (keeping template unchanged):
			updateResponse, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id:       object.GetId(),
					Metadata: privatev1.Metadata_builder{Name: name}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template:          object.GetSpec().GetTemplate(),
						NetworkAttachment: object.GetSpec().GetNetworkAttachment(),
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "your_hub",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetSpec().GetTemplate().GetId()).To(Equal("my-template-id"))
			Expect(updateResponse.GetObject().GetStatus().GetHub()).To(Equal("your_hub"))

			// Get and verify:
			getResponse, err := server.Get(ctx, privatev1.ClustersGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetSpec().GetTemplate().GetId()).To(Equal("my-template-id"))
			Expect(getResponse.GetObject().GetStatus().GetHub()).To(Equal("your_hub"))
		})

		It("Delete object", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{
						Name:       "test-cluster",
						Finalizers: []string{"a"},
					}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Delete the object:
			_, err = server.Delete(ctx, privatev1.ClustersDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Get and verify:
			getResponse, err := server.Get(ctx, privatev1.ClustersGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = getResponse.GetObject()
			Expect(object.GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
		})

		It("Rejects creation with duplicate condition", func() {
			_, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
					}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Conditions: []*privatev1.ClusterCondition{
							privatev1.ClusterCondition_builder{
								Type: privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_READY,
							}.Build(),
							privatev1.ClusterCondition_builder{
								Type: privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_READY,
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(Equal("condition 'CLUSTER_CONDITION_TYPE_READY' is duplicated"))
		})

		It("Rejects update with duplicate condition", func() {
			_, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{Name: "test-cluster"}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Conditions: []*privatev1.ClusterCondition{
							privatev1.ClusterCondition_builder{
								Type: privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_READY,
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Status: privatev1.ClusterStatus_builder{
						Conditions: []*privatev1.ClusterCondition{
							privatev1.ClusterCondition_builder{
								Type: privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_READY,
							}.Build(),
							privatev1.ClusterCondition_builder{
								Type: privatev1.ClusterConditionType_CLUSTER_CONDITION_TYPE_READY,
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(Equal("condition 'CLUSTER_CONDITION_TYPE_READY' is duplicated"))
		})

		It("Allows adding a new node set", func() {
			// Create a cluster with the default node sets from the template
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{Name: "test-cluster"}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			Expect(object.GetSpec().GetNodeSets()).To(HaveLen(2)) // compute and gpu

			// Add a new node set
			updated, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: object.GetId(),
					Spec: privatev1.ClusterSpec_builder{
						NodeSets: map[string]*privatev1.ClusterNodeSet{
							"compute": privatev1.ClusterNodeSet_builder{
								BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id", Shared: true}.Build(),
								Size:                  proto.Int32(3),
							}.Build(),
							"gpu": privatev1.ClusterNodeSet_builder{
								BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-gpu-bmit-id", Shared: true}.Build(),
								Size:                  proto.Int32(1),
							}.Build(),
							"storage": privatev1.ClusterNodeSet_builder{
								BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id", Shared: true}.Build(),
								Size:                  proto.Int32(2),
							}.Build(),
						},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.node_sets"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(updated.GetObject().GetSpec().GetNodeSets()["storage"].GetVersion(),
				object.GetSpec().GetVersion())).To(BeTrue())
		})

		It("Allows removing a node set when multiple exist", func() {
			// Create a cluster with the default node sets from the template
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{Name: "test-cluster"}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			Expect(object.GetSpec().GetNodeSets()).To(HaveLen(2)) // compute and gpu

			// Remove the gpu node set
			_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: object.GetId(),
					Spec: privatev1.ClusterSpec_builder{
						NodeSets: map[string]*privatev1.ClusterNodeSet{
							"compute": privatev1.ClusterNodeSet_builder{
								BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id", Shared: true}.Build(),
								Size:                  proto.Int32(3),
							}.Build(),
						},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.node_sets"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		It("Rejects removing the last node set", func() {
			// Create a cluster with a template that has only one node set
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{Name: "test-cluster"}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id-0"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			Expect(object.GetSpec().GetNodeSets()).To(HaveLen(1)) // only compute

			// Try to remove the last node set
			_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: object.GetId(),
					Spec: privatev1.ClusterSpec_builder{
						NodeSets: map[string]*privatev1.ClusterNodeSet{},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.node_sets"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(Equal("cannot remove the last node set: clusters must have at least one node set"))
		})

		It("Rejects changing baremetal_instance_type of an existing node set", func() {
			// Create a cluster with the default node sets from the template
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{Name: "test-cluster"}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Try to change the bare metal instance type of the compute node set
			_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: object.GetId(),
					Spec: privatev1.ClusterSpec_builder{
						NodeSets: map[string]*privatev1.ClusterNodeSet{
							"compute": privatev1.ClusterNodeSet_builder{
								BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-gpu-bmit-id", Shared: true}.Build(), // Changed from acme-bmit-id
								Size:                  proto.Int32(3),
							}.Build(),
							"gpu": privatev1.ClusterNodeSet_builder{
								BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-gpu-bmit-id", Shared: true}.Build(),
								Size:                  proto.Int32(1),
							}.Build(),
						},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.node_sets"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(Equal("cannot change baremetal_instance_type for node set 'compute' from 'acme-bmit-id' to 'acme-gpu-bmit-id': baremetal_instance_type is immutable"))
		})

		It("Rejects changing baremetal_instance_type in a full-object update", func() {
			created, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{Name: "immutable-hardware-full-update"}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			original := created.GetObject()
			updated := proto.Clone(original).(*privatev1.Cluster)
			updated.GetSpec().GetNodeSets()["compute"].SetBaremetalInstanceType(
				privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-gpu-bmit-id", Shared: true}.Build())

			_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{Object: updated}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(grpcstatus.Convert(err).Message()).To(ContainSubstring(
				"cannot change baremetal_instance_type for node set 'compute'"))

			stored, err := server.Get(ctx, privatev1.ClustersGetRequest_builder{Id: original.GetId()}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(stored.GetObject().GetSpec(), original.GetSpec())).To(BeTrue())
		})

		It("Allows changing size of an existing node set", func() {
			// Create a cluster with the default node sets from the template
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{Name: "test-cluster"}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Change the size of the compute node set
			updateResponse, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: object.GetId(),
					Spec: privatev1.ClusterSpec_builder{
						NodeSets: map[string]*privatev1.ClusterNodeSet{
							"compute": privatev1.ClusterNodeSet_builder{
								BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id", Shared: true}.Build(),
								Size:                  proto.Int32(5),
							}.Build(),
							"gpu": privatev1.ClusterNodeSet_builder{
								BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-gpu-bmit-id", Shared: true}.Build(),
								Size:                  proto.Int32(1),
							}.Build(),
						},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.node_sets"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			updatedObject := updateResponse.GetObject()
			Expect(updatedObject.GetSpec().GetNodeSets()["compute"].GetSize()).To(Equal(int32(5)))
		})

		It("Allows changing size with granular field mask", func() {
			// Create a cluster with the default node sets from the template
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{Name: "test-cluster"}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Get the initial size
			initialSize := object.GetSpec().GetNodeSets()["compute"].GetSize()
			newSize := initialSize + 2

			// Change only the size using a granular field mask
			updateResponse, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: object.GetId(),
					Spec: privatev1.ClusterSpec_builder{
						NodeSets: map[string]*privatev1.ClusterNodeSet{
							"compute": privatev1.ClusterNodeSet_builder{
								Size: proto.Int32(newSize),
							}.Build(),
						},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.node_sets.compute.size"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			updatedObject := updateResponse.GetObject()
			Expect(updatedObject.GetSpec().GetNodeSets()["compute"].GetSize()).To(Equal(newSize))
		})

		Describe("Cluster state validation for spec updates", func() {
			createClusterWithState := func(state privatev1.ClusterState) *privatev1.Cluster {
				createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				object := createResponse.GetObject()

				_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: object.GetId(),
						Status: privatev1.ClusterStatus_builder{
							State:               state,
							StateTransitionTime: timestamppb.Now(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"status.state", "status.state_transition_time"},
					},
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				getResponse, err := server.Get(ctx, privatev1.ClustersGetRequest_builder{
					Id: object.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				return getResponse.GetObject()
			}

			It("Rejects spec update when cluster state is failed", func() {
				object := createClusterWithState(privatev1.ClusterState_CLUSTER_STATE_FAILED)

				_, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: object.GetId(),
						Spec: privatev1.ClusterSpec_builder{
							NodeSets: map[string]*privatev1.ClusterNodeSet{
								"compute": privatev1.ClusterNodeSet_builder{
									Size: proto.Int32(5),
								}.Build(),
							},
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec.node_sets.compute.size"},
					},
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("cannot update cluster spec"))
				Expect(status.Message()).To(ContainSubstring("CLUSTER_STATE_FAILED"))
			})

			It("Rejects spec update when cluster state is delete_failed", func() {
				object := createClusterWithState(privatev1.ClusterState_CLUSTER_STATE_DELETE_FAILED)

				_, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: object.GetId(),
						Spec: privatev1.ClusterSpec_builder{
							NodeSets: map[string]*privatev1.ClusterNodeSet{
								"compute": privatev1.ClusterNodeSet_builder{
									Size: proto.Int32(5),
								}.Build(),
							},
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec.node_sets.compute.size"},
					},
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("cannot update cluster spec"))
				Expect(status.Message()).To(ContainSubstring("CLUSTER_STATE_DELETE_FAILED"))
			})

			It("Rejects spec update when cluster state is deleting", func() {
				object := createClusterWithState(privatev1.ClusterState_CLUSTER_STATE_DELETING)

				_, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: object.GetId(),
						Spec: privatev1.ClusterSpec_builder{
							NodeSets: map[string]*privatev1.ClusterNodeSet{
								"compute": privatev1.ClusterNodeSet_builder{
									Size: proto.Int32(5),
								}.Build(),
							},
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec.node_sets.compute.size"},
					},
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("cannot update cluster spec"))
				Expect(status.Message()).To(ContainSubstring("CLUSTER_STATE_DELETING"))
			})

			It("Allows spec update when cluster state is unspecified", func() {
				object := createClusterWithState(privatev1.ClusterState_CLUSTER_STATE_UNSPECIFIED)

				_, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: object.GetId(),
						Spec: privatev1.ClusterSpec_builder{
							NodeSets: map[string]*privatev1.ClusterNodeSet{
								"compute": privatev1.ClusterNodeSet_builder{
									Size: proto.Int32(5),
								}.Build(),
							},
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec.node_sets.compute.size"},
					},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})

			It("Allows status-only update when cluster state is failed", func() {
				object := createClusterWithState(privatev1.ClusterState_CLUSTER_STATE_FAILED)

				_, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: object.GetId(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "updated-hub",
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"status.hub"},
					},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})

			It("Allows spec update when cluster state is ready", func() {
				object := createClusterWithState(privatev1.ClusterState_CLUSTER_STATE_READY)

				_, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: object.GetId(),
						Spec: privatev1.ClusterSpec_builder{
							NodeSets: map[string]*privatev1.ClusterNodeSet{
								"compute": privatev1.ClusterNodeSet_builder{
									Size: proto.Int32(5),
								}.Build(),
							},
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec.node_sets.compute.size"},
					},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})

			It("Allows spec update when cluster state is progressing", func() {
				object := createClusterWithState(privatev1.ClusterState_CLUSTER_STATE_PROGRESSING)

				_, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: object.GetId(),
						Spec: privatev1.ClusterSpec_builder{
							NodeSets: map[string]*privatev1.ClusterNodeSet{
								"compute": privatev1.ClusterNodeSet_builder{
									Size: proto.Int32(5),
								}.Build(),
							},
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec.node_sets.compute.size"},
					},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})
		})

		It("Rejects changing template field", func() {
			oldTemplate := "my-template-id"
			newTemplate := "my-template-id-0"

			// Create a cluster
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{Name: "test-cluster"}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: oldTemplate}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Try to change the template
			_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: object.GetId(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: newTemplate}.Build(),
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
			Expect(status.Message()).To(Equal(fmt.Sprintf(
				"cannot change spec.template from '%s' to '%s': template is immutable",
				oldTemplate, newTemplate,
			)))
		})

		It("Rejects changing template_parameters field", func() {
			// Create a cluster with template parameters
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{Name: "test-cluster"}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Try to change the template parameters
			_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: object.GetId(),
					Spec: privatev1.ClusterSpec_builder{
						Template:           object.GetSpec().GetTemplate(),
						TemplateParameters: map[string]*anypb.Any{"key": nil},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.template_parameters"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(Equal("cannot change spec.template_parameters: template parameters are immutable"))
		})

		Describe("Network attachment immutability", func() {
			createClusterWithNetworkAttachment := func(subnet *privatev1.SubnetLocalReference, securityGroups []*privatev1.SecurityGroupLocalReference) *privatev1.Cluster {
				createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{Name: "test-cluster"}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
							NetworkAttachment: privatev1.ClusterNetworkAttachment_builder{
								Subnet:         subnet,
								SecurityGroups: securityGroups,
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				return createResponse.GetObject()
			}

			It("Rejects changing subnet via whole attachment replacement", func() {
				object := createClusterWithNetworkAttachment(privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(), []*privatev1.SecurityGroupLocalReference{privatev1.SecurityGroupLocalReference_builder{Id: "default-sg"}.Build()})

				_, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: object.GetId(),
						Spec: privatev1.ClusterSpec_builder{
							NetworkAttachment: privatev1.ClusterNetworkAttachment_builder{
								Subnet:         privatev1.SubnetLocalReference_builder{Id: "subnet-2"}.Build(),
								SecurityGroups: []*privatev1.SecurityGroupLocalReference{privatev1.SecurityGroupLocalReference_builder{Id: "default-sg"}.Build()},
							}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec.network_attachment"},
					},
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(Equal(
					"cannot change spec.network_attachment.subnet from 'subnet-1' to 'subnet-2': subnet is immutable",
				))
				stored, err := server.Get(ctx, privatev1.ClustersGetRequest_builder{Id: object.GetId()}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(proto.Equal(stored.GetObject().GetSpec().GetNetworkAttachment(), object.GetSpec().GetNetworkAttachment())).To(BeTrue())
			})

			It("Rejects removing network_attachment when one exists", func() {
				object := createClusterWithNetworkAttachment(privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(), []*privatev1.SecurityGroupLocalReference{privatev1.SecurityGroupLocalReference_builder{Id: "default-sg"}.Build()})

				_, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id:   object.GetId(),
						Spec: privatev1.ClusterSpec_builder{}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec.network_attachment"},
					},
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(Equal(
					"cannot change spec.network_attachment.subnet from 'subnet-1' to '': subnet is immutable",
				))
				stored, err := server.Get(ctx, privatev1.ClustersGetRequest_builder{Id: object.GetId()}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(proto.Equal(stored.GetObject().GetSpec().GetNetworkAttachment(), object.GetSpec().GetNetworkAttachment())).To(BeTrue())
			})

			It("Rejects adding network_attachment when none existed", func() {
				createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{Name: "test-cluster"}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				object := createResponse.GetObject()

				_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: object.GetId(),
						Spec: privatev1.ClusterSpec_builder{
							NetworkAttachment: privatev1.ClusterNetworkAttachment_builder{
								Subnet: privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
							}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec.network_attachment"},
					},
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(Equal(
					"cannot change spec.network_attachment.subnet from '' to 'subnet-1': subnet is immutable",
				))
				stored, err := server.Get(ctx, privatev1.ClustersGetRequest_builder{Id: object.GetId()}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(stored.GetObject().GetSpec().GetNetworkAttachment()).To(BeNil())
			})

			It("Rejects changing security_groups with same subnet", func() {
				object := createClusterWithNetworkAttachment(privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(), []*privatev1.SecurityGroupLocalReference{privatev1.SecurityGroupLocalReference_builder{Id: "default-sg"}.Build()})

				updateResponse, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: object.GetId(),
						Spec: privatev1.ClusterSpec_builder{
							NetworkAttachment: privatev1.ClusterNetworkAttachment_builder{
								Subnet:         privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
								SecurityGroups: []*privatev1.SecurityGroupLocalReference{privatev1.SecurityGroupLocalReference_builder{Id: "default-sg"}.Build(), privatev1.SecurityGroupLocalReference_builder{Id: "sg-2"}.Build()},
							}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec.network_attachment"},
					},
				}.Build())
				Expect(updateResponse).To(BeNil())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				stored, err := server.Get(ctx, privatev1.ClustersGetRequest_builder{Id: object.GetId()}.Build())
				Expect(err).ToNot(HaveOccurred())
				attachment := stored.GetObject().GetSpec().GetNetworkAttachment()
				Expect(attachment.GetSubnet().GetId()).To(Equal("subnet-1"))
				Expect(attachment.GetSecurityGroups()).To(HaveLen(1))
				Expect(attachment.GetSecurityGroups()[0].GetId()).To(Equal("default-sg"))
			})

			It("Rejects updating security_groups via sub-field mask", func() {
				object := createClusterWithNetworkAttachment(privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(), []*privatev1.SecurityGroupLocalReference{privatev1.SecurityGroupLocalReference_builder{Id: "default-sg"}.Build()})

				updateResponse, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: object.GetId(),
						Spec: privatev1.ClusterSpec_builder{
							NetworkAttachment: privatev1.ClusterNetworkAttachment_builder{
								SecurityGroups: []*privatev1.SecurityGroupLocalReference{privatev1.SecurityGroupLocalReference_builder{Id: "sg-2"}.Build(), privatev1.SecurityGroupLocalReference_builder{Id: "sg-3"}.Build()},
							}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec.network_attachment.security_groups"},
					},
				}.Build())
				Expect(updateResponse).To(BeNil())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				stored, err := server.Get(ctx, privatev1.ClustersGetRequest_builder{Id: object.GetId()}.Build())
				Expect(err).ToNot(HaveOccurred())
				attachment := stored.GetObject().GetSpec().GetNetworkAttachment()
				Expect(attachment.GetSubnet().GetId()).To(Equal("subnet-1"))
				Expect(attachment.GetSecurityGroups()).To(HaveLen(1))
				Expect(attachment.GetSecurityGroups()[0].GetId()).To(Equal("default-sg"))
			})

			It("Accepts an identical network_attachment", func() {
				object := createClusterWithNetworkAttachment(
					privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
					[]*privatev1.SecurityGroupLocalReference{privatev1.SecurityGroupLocalReference_builder{Id: "default-sg"}.Build()},
				)
				updated, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: object.GetId(),
						Spec: privatev1.ClusterSpec_builder{
							NetworkAttachment: privatev1.ClusterNetworkAttachment_builder{
								Subnet: privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
								SecurityGroups: []*privatev1.SecurityGroupLocalReference{
									privatev1.SecurityGroupLocalReference_builder{Id: "default-sg"}.Build(),
								},
							}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.network_attachment"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				attachment := updated.GetObject().GetSpec().GetNetworkAttachment()
				Expect(attachment.GetSubnet().GetId()).To(Equal("subnet-1"))
				Expect(attachment.GetSecurityGroups()[0].GetId()).To(Equal("default-sg"))
			})

			It("Passes through when mask does not include network_attachment", func() {
				object := createClusterWithNetworkAttachment(privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(), []*privatev1.SecurityGroupLocalReference{privatev1.SecurityGroupLocalReference_builder{Id: "default-sg"}.Build()})

				updateResponse, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: object.GetId(),
						Status: privatev1.ClusterStatus_builder{
							State:               privatev1.ClusterState_CLUSTER_STATE_READY,
							StateTransitionTime: timestamppb.Now(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"status.state", "status.state_transition_time"},
					},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				updated := updateResponse.GetObject()
				Expect(updated.GetSpec().GetNetworkAttachment().GetSubnet().GetId()).To(Equal("subnet-1"))
			})

			It("Rejects removing security_groups with same subnet", func() {
				object := createClusterWithNetworkAttachment(privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(), []*privatev1.SecurityGroupLocalReference{privatev1.SecurityGroupLocalReference_builder{Id: "default-sg"}.Build()})

				_, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: object.GetId(),
						Spec: privatev1.ClusterSpec_builder{
							NetworkAttachment: privatev1.ClusterNetworkAttachment_builder{
								Subnet:         privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
								SecurityGroups: []*privatev1.SecurityGroupLocalReference{},
							}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec.network_attachment"},
					},
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("security groups are immutable"))
				stored, err := server.Get(ctx, privatev1.ClustersGetRequest_builder{Id: object.GetId()}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(proto.Equal(stored.GetObject().GetSpec().GetNetworkAttachment(), object.GetSpec().GetNetworkAttachment())).To(BeTrue())
			})

			It("Rejects reordering equivalent security_groups", func() {
				object := createClusterWithNetworkAttachment(
					privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
					[]*privatev1.SecurityGroupLocalReference{
						privatev1.SecurityGroupLocalReference_builder{Id: "default-sg"}.Build(),
						privatev1.SecurityGroupLocalReference_builder{Id: "sg-2"}.Build(),
					},
				)

				_, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: object.GetId(),
						Spec: privatev1.ClusterSpec_builder{
							NetworkAttachment: privatev1.ClusterNetworkAttachment_builder{
								Subnet: privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
								SecurityGroups: []*privatev1.SecurityGroupLocalReference{
									privatev1.SecurityGroupLocalReference_builder{Id: "sg-2"}.Build(),
									privatev1.SecurityGroupLocalReference_builder{Id: "default-sg"}.Build(),
								},
							}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec.network_attachment"},
					},
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("security groups are immutable"))
				stored, err := server.Get(ctx, privatev1.ClustersGetRequest_builder{Id: object.GetId()}.Build())
				Expect(err).ToNot(HaveOccurred())
				attachment := stored.GetObject().GetSpec().GetNetworkAttachment()
				Expect(attachment.GetSecurityGroups()).To(HaveLen(2))
				Expect(attachment.GetSecurityGroups()[0].GetId()).To(Equal("default-sg"))
				Expect(attachment.GetSecurityGroups()[1].GetId()).To(Equal("sg-2"))
			})
		})

		Context("Auto external IP attachment immutability", func() {
			createClusterWithAttachment := func() *privatev1.Cluster {
				createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{Name: "test-cluster"}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
							NetworkAttachment: privatev1.ClusterNetworkAttachment_builder{
								Subnet:         privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
								SecurityGroups: []*privatev1.SecurityGroupLocalReference{privatev1.SecurityGroupLocalReference_builder{Id: "default-sg"}.Build()},
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				return createResponse.GetObject()
			}

			It("Rejects enabling auto_external_ip_attachment and provisions nothing", func() {
				object := createClusterWithAttachment()

				_, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: object.GetId(),
						Spec: privatev1.ClusterSpec_builder{
							AutoExternalIpAttachment: proto.Bool(true),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec.auto_external_ip_attachment"},
					},
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("auto_external_ip_attachment is immutable"))

				eipDao, err := dao.NewGenericDAO[*privatev1.ExternalIP]().
					SetLogger(logger).SetTenancyLogic(tenancy).Build()
				Expect(err).ToNot(HaveOccurred())
				eipList, err := eipDao.List().
					SetFilter(fmt.Sprintf("this.metadata.labels['%s'] == '%s'", autoCreatedForLabel, object.GetId())).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(eipList.GetItems()).To(BeEmpty())

				attDao, err := dao.NewGenericDAO[*privatev1.ExternalIPAttachment]().
					SetLogger(logger).SetTenancyLogic(tenancy).Build()
				Expect(err).ToNot(HaveOccurred())
				attList, err := attDao.List().
					SetFilter(fmt.Sprintf("this.metadata.labels['%s'] == '%s'", autoCreatedForLabel, object.GetId())).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				Expect(attList.GetItems()).To(BeEmpty())

				stored, err := server.Get(ctx, privatev1.ClustersGetRequest_builder{Id: object.GetId()}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(stored.GetObject().GetSpec().GetAutoExternalIpAttachment()).To(BeFalse())
			})
		})

		Describe("Catalog item", func() {
			var catalogItemsDao *dao.GenericDAO[*privatev1.ClusterCatalogItem]

			BeforeEach(func() {
				var err error
				catalogItemsDao, err = dao.NewGenericDAO[*privatev1.ClusterCatalogItem]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
			})

			createCatalogItem := func(id string, published bool, fields *privatev1.ClusterCatalogItemFields) {
				if fields == nil {
					fields = &privatev1.ClusterCatalogItemFields{}
				}
				if fields.GetNodeSets() == nil {
					fields = proto.Clone(fields).(*privatev1.ClusterCatalogItemFields)
					fields.SetNodeSets(privatev1.ClusterNodeSetMapPolicy_builder{
						Editable: privatev1.EditableClusterNodeSetMap_builder{
							DefaultValue: privatev1.ClusterNodeSetMap_builder{Items: map[string]*privatev1.ClusterCatalogNodeSet{
								"compute": privatev1.ClusterCatalogNodeSet_builder{Size: 3, BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id", Name: "acme-bmit-name", Shared: true}.Build()}.Build(),
								"gpu":     privatev1.ClusterCatalogNodeSet_builder{Size: 1, BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-gpu-bmit-id", Name: "acme-gpu-name", Shared: true}.Build()}.Build(),
							}}.Build(),
						}.Build(),
					}.Build())
				}
				_, err := catalogItemsDao.Create().SetObject(
					privatev1.ClusterCatalogItem_builder{
						Id: id,
						Metadata: privatev1.Metadata_builder{
							Name:   id + "-name",
							Tenant: testTenant,
						}.Build(),
						Title:     "Test Catalog Item",
						Published: published,
						Template:  privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
						Fields:    fields,
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			}

			It("Creates cluster with catalog item and populates node sets from its policy", func() {
				createCatalogItem("cat-happy", true, nil)

				response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							CatalogItem: privatev1.ClusterCatalogItemReference_builder{Id: "cat-happy"}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
				object := response.GetObject()
				Expect(object).ToNot(BeNil())
				Expect(object.GetId()).ToNot(BeEmpty())
				Expect(object.GetSpec().GetTemplate().GetId()).To(Equal("my-template-id"))
				Expect(object.GetSpec().GetTemplate().GetName()).To(Equal("my-template-name"))
				Expect(object.GetSpec().GetCatalogItem().GetId()).To(Equal("cat-happy"))

				// Verify node sets are populated from the catalog policy:
				nodeSets := object.GetSpec().GetNodeSets()
				Expect(nodeSets).To(HaveLen(2))
				Expect(nodeSets).To(HaveKey("compute"))
				Expect(nodeSets["compute"].GetBaremetalInstanceType().GetId()).To(Equal("acme-bmit-id"))
				Expect(nodeSets["compute"].GetBaremetalInstanceType().GetName()).To(Equal("acme-bmit-name"))
				Expect(nodeSets["compute"].GetSize()).To(Equal(int32(3)))
				Expect(nodeSets).To(HaveKey("gpu"))
				Expect(nodeSets["gpu"].GetBaremetalInstanceType().GetId()).To(Equal("acme-gpu-bmit-id"))
				Expect(nodeSets["gpu"].GetBaremetalInstanceType().GetName()).To(Equal("acme-gpu-name"))
				Expect(nodeSets["gpu"].GetSize()).To(Equal(int32(1)))
			})

			It("Creates cluster with catalog item specified by name", func() {
				createCatalogItem("cat-by-name", true, nil)

				response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							CatalogItem: privatev1.ClusterCatalogItemReference_builder{Name: "cat-by-name-name"}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
				object := response.GetObject()
				Expect(object).ToNot(BeNil())
				Expect(object.GetSpec().GetTemplate().GetId()).To(Equal("my-template-id"))
			})

			It("Fails when catalog item not found", func() {
				_, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							CatalogItem: privatev1.ClusterCatalogItemReference_builder{Id: "nonexistent"}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.NotFound))
				Expect(status.Message()).To(Equal(
					"catalog item 'nonexistent' not found",
				))
			})

			It("Fails when catalog item is not published", func() {
				createCatalogItem("cat-unpublished", false, nil)

				_, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							CatalogItem: privatev1.ClusterCatalogItemReference_builder{Id: "cat-unpublished"}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.NotFound))
				Expect(status.Message()).To(Equal(
					"catalog item 'cat-unpublished' is not published",
				))
			})

			It("Fails when both catalog_item and template are set", func() {
				_, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							CatalogItem: privatev1.ClusterCatalogItemReference_builder{Id: "any-catalog-item"}.Build(),
							Template:    privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(Equal("catalog_item and template are mutually exclusive"))
			})

			It("Rejects user value for non-editable field", func() {
				createCatalogItem("cat-noneditable", true, privatev1.ClusterCatalogItemFields_builder{SshPublicKey: privatev1.StringFieldPolicy_builder{Locked: proto.String("forced-key")}.Build()}.Build())

				_, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							CatalogItem:  privatev1.ClusterCatalogItemReference_builder{Id: "cat-noneditable"}.Build(),
							SshPublicKey: new("user-key"),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("not editable"))
			})

			DescribeTable("validates editable SSH public keys",
				func(catID string, value string, expectError bool) {
					createCatalogItem(catID, true, privatev1.ClusterCatalogItemFields_builder{SshPublicKey: privatev1.StringFieldPolicy_builder{Editable: privatev1.EditableStringField_builder{}.Build()}.Build()}.Build())

					response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
						Object: privatev1.Cluster_builder{
							Metadata: privatev1.Metadata_builder{
								Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
							}.Build(),
							Spec: privatev1.ClusterSpec_builder{
								CatalogItem:  privatev1.ClusterCatalogItemReference_builder{Id: catID}.Build(),
								SshPublicKey: new(value),
							}.Build(),
							Status: privatev1.ClusterStatus_builder{
								Hub: "my-hub-id",
							}.Build(),
						}.Build(),
					}.Build())
					if expectError {
						Expect(err).To(HaveOccurred())
						status, ok := grpcstatus.FromError(err)
						Expect(ok).To(BeTrue())
						Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
						Expect(status.Message()).To(ContainSubstring("spec.ssh_public_key"))
						Expect(status.Message()).To(ContainSubstring("invalid OpenSSH public key"))
					} else {
						Expect(err).ToNot(HaveOccurred())
						Expect(response.GetObject().GetSpec().GetSshPublicKey()).To(Equal(value))
					}
				},
				Entry("rejects malformed value", "cat-ssh-invalid", "short-val", true),
				Entry("accepts an OpenSSH public key", "cat-ssh-valid", testSSHPublicKey, false),
			)

			It("Applies default for editable field when not provided", func() {
				createCatalogItem("cat-default", true, privatev1.ClusterCatalogItemFields_builder{SshPublicKey: privatev1.StringFieldPolicy_builder{Editable: privatev1.EditableStringField_builder{DefaultValue: proto.String(testSSHPublicKey)}.Build()}.Build()}.Build())

				response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							CatalogItem: privatev1.ClusterCatalogItemReference_builder{Id: "cat-default"}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				object := response.GetObject()
				Expect(object.GetSpec().GetSshPublicKey()).To(Equal(testSSHPublicKey))
			})

			It("Applies spec defaults from template when created via catalog item", func() {
				// Create a template with spec defaults:
				templatesDao, err := dao.NewGenericDAO[*privatev1.ClusterTemplate]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
				_, err = templatesDao.Create().
					SetObject(
						privatev1.ClusterTemplate_builder{
							Id: "template-with-defaults",
							Metadata: privatev1.Metadata_builder{
								Name:   "template-with-defaults-name",
								Tenant: testTenant,
							}.Build(),
							Title:       "Template with defaults",
							Description: "Template with spec defaults",
							SpecDefaults: privatev1.ClusterTemplateSpecDefaults_builder{
								SshPublicKey: proto.String(testSSHPublicKey),
							}.Build(),
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				// Create a catalog item referencing the template with defaults:
				_, err = catalogItemsDao.Create().SetObject(
					privatev1.ClusterCatalogItem_builder{
						Id: "cat-with-defaults",
						Metadata: privatev1.Metadata_builder{
							Name:   "cat-with-defaults-name",
							Tenant: testTenant,
						}.Build(),
						Title:     "Catalog Item with Template Defaults",
						Published: true,
						Template:  privatev1.ClusterTemplateReference_builder{Id: "template-with-defaults"}.Build(),
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				// Create a cluster via catalog item without specifying ssh_public_key:
				response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							CatalogItem: privatev1.ClusterCatalogItemReference_builder{Id: "cat-with-defaults"}.Build(),
							NodeSets: map[string]*privatev1.ClusterNodeSet{
								"worker": privatev1.ClusterNodeSet_builder{Size: proto.Int32(2), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id", Shared: true}.Build()}.Build(),
							},
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				object := response.GetObject()

				// Verify spec defaults from template are applied:
				Expect(object.GetSpec().GetSshPublicKey()).To(Equal(testSSHPublicKey))

				// Verify the explicit Cluster node sets remain populated:
				nodeSets := object.GetSpec().GetNodeSets()
				Expect(nodeSets).To(HaveLen(1))
				Expect(nodeSets).To(HaveKey("worker"))
				Expect(nodeSets["worker"].GetBaremetalInstanceType().GetId()).To(Equal("acme-bmit-id"))
				Expect(nodeSets["worker"].GetSize()).To(Equal(int32(2)))
			})

			It("Applies version from template spec_defaults via catalog item", func() {
				// Seed a non-default ClusterVersion for the template to pin:
				seedClusterVersion(ctx, privatev1.ClusterVersion_builder{
					Id: uuid.New(),
					Metadata: privatev1.Metadata_builder{
						Name:   "4-18-0",
						Tenant: testTenant,
					}.Build(),
					Spec: privatev1.ClusterVersionSpec_builder{
						Image:   "quay.io/openshift-release-dev/ocp-release:4.18.0-multi",
						Enabled: proto.Bool(true),
						Version: "4.18.0",
					}.Build(),
				}.Build())

				// Create a template whose spec_defaults pins version:
				templatesDao, err := dao.NewGenericDAO[*privatev1.ClusterTemplate]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
				_, err = templatesDao.Create().
					SetObject(
						privatev1.ClusterTemplate_builder{
							Id: "template-version-pinned",
							Metadata: privatev1.Metadata_builder{
								Name:   "template-version-pinned-name",
								Tenant: testTenant,
							}.Build(),
							Title:       "Version-pinned template",
							Description: "Template that pins version via spec_defaults",
							SpecDefaults: privatev1.ClusterTemplateSpecDefaults_builder{
								Version: &privatev1.ClusterVersionReference{Name: "4-18-0"},
							}.Build(),
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				// Create a catalog item referencing the version-pinned template
				// (no version field_definition):
				_, err = catalogItemsDao.Create().SetObject(
					privatev1.ClusterCatalogItem_builder{
						Id: "cat-version-pinned",
						Metadata: privatev1.Metadata_builder{
							Name:   "cat-version-pinned-name",
							Tenant: testTenant,
						}.Build(),
						Title:     "Catalog Item with Version-Pinned Template",
						Published: true,
						Template:  &privatev1.ClusterTemplateReference{Id: "template-version-pinned"},
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				// Create a cluster via catalog item without specifying version:
				response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							CatalogItem: &privatev1.ClusterCatalogItemReference{Id: "cat-version-pinned"},
							NodeSets:    map[string]*privatev1.ClusterNodeSet{"worker": privatev1.ClusterNodeSet_builder{Size: proto.Int32(2), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id", Shared: true}.Build()}.Build()},
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				// Template spec_defaults.version should win over system default:
				Expect(response.GetObject().GetSpec().GetVersion().GetName()).To(Equal("4-18-0"))
			})

			It("Field definition version overrides template spec_defaults via catalog item", func() {
				// Seed a non-default ClusterVersion for the field_definition to set:
				seedClusterVersion(ctx, privatev1.ClusterVersion_builder{
					Id: uuid.New(),
					Metadata: privatev1.Metadata_builder{
						Name:   "4-19-0",
						Tenant: testTenant,
					}.Build(),
					Spec: privatev1.ClusterVersionSpec_builder{
						Image:   "quay.io/openshift-release-dev/ocp-release:4.19.0-multi",
						Enabled: proto.Bool(true),
						Version: "4.19.0",
					}.Build(),
				}.Build())

				// Create a template whose spec_defaults pins version to 4-17-0
				// (the system default, but specified explicitly as a template default):
				templatesDao, err := dao.NewGenericDAO[*privatev1.ClusterTemplate]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
				_, err = templatesDao.Create().
					SetObject(
						privatev1.ClusterTemplate_builder{
							Id: "template-fd-override",
							Metadata: privatev1.Metadata_builder{
								Name:   "template-fd-override-name",
								Tenant: testTenant,
							}.Build(),
							Title:       "Template for FD override test",
							Description: "Template whose spec_defaults are overridden by fields",
							SpecDefaults: privatev1.ClusterTemplateSpecDefaults_builder{
								Version: &privatev1.ClusterVersionReference{Name: "4-17-0"},
							}.Build(),
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				// Create a catalog item with a field_definition that overrides version:
				_, err = catalogItemsDao.Create().SetObject(
					privatev1.ClusterCatalogItem_builder{
						Id: "cat-fd-override",
						Metadata: privatev1.Metadata_builder{
							Name:   "cat-fd-override-name",
							Tenant: testTenant,
						}.Build(),
						Title:     "Catalog Item with FD version override",
						Published: true,
						Template:  &privatev1.ClusterTemplateReference{Id: "template-fd-override"},
						Fields:    privatev1.ClusterCatalogItemFields_builder{Version: privatev1.ClusterVersionReferenceFieldPolicy_builder{Locked: privatev1.ClusterVersionReference_builder{Name: "4-19-0"}.Build()}.Build()}.Build(),
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				// Create a cluster via catalog item without specifying version:
				response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							CatalogItem: &privatev1.ClusterCatalogItemReference{Id: "cat-fd-override"},
							NodeSets:    map[string]*privatev1.ClusterNodeSet{"worker": privatev1.ClusterNodeSet_builder{Size: proto.Int32(2), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id", Shared: true}.Build()}.Build()},
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				// Field definition default should win over template spec_defaults:
				Expect(response.GetObject().GetSpec().GetVersion().GetName()).To(Equal("4-19-0"))
			})

			It("Falls back to system default version via catalog item when nothing sets version", func() {
				// Create a template with no spec_defaults.version:
				templatesDao, err := dao.NewGenericDAO[*privatev1.ClusterTemplate]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
				_, err = templatesDao.Create().
					SetObject(
						privatev1.ClusterTemplate_builder{
							Id: "template-no-version",
							Metadata: privatev1.Metadata_builder{
								Name:   "template-no-version-name",
								Tenant: testTenant,
							}.Build(),
							Title:       "Template without version default",
							Description: "Template with no spec_defaults.version",
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				// Create a catalog item with no version field_definition:
				_, err = catalogItemsDao.Create().SetObject(
					privatev1.ClusterCatalogItem_builder{
						Id: "cat-no-version",
						Metadata: privatev1.Metadata_builder{
							Name:   "cat-no-version-name",
							Tenant: testTenant,
						}.Build(),
						Title:     "Catalog Item without version",
						Published: true,
						Template:  &privatev1.ClusterTemplateReference{Id: "template-no-version"},
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				// Create a cluster via catalog item without specifying version:
				response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							CatalogItem: &privatev1.ClusterCatalogItemReference{Id: "cat-no-version"},
							NodeSets:    map[string]*privatev1.ClusterNodeSet{"worker": privatev1.ClusterNodeSet_builder{Size: proto.Int32(2), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id", Shared: true}.Build()}.Build()},
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				// System default (4-17-0) should be used as last resort:
				Expect(response.GetObject().GetSpec().GetVersion().GetName()).To(Equal("4-17-0"))
			})

			It("Fails when catalog item has no template", func() {
				_, err := catalogItemsDao.Create().SetObject(
					privatev1.ClusterCatalogItem_builder{
						Id: "cat-no-template",
						Metadata: privatev1.Metadata_builder{
							Name:   "cat-no-template-name",
							Tenant: testTenant,
						}.Build(),
						Title:     "Catalog Item Without Template",
						Published: true,
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				_, err = server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							CatalogItem: privatev1.ClusterCatalogItemReference_builder{Id: "cat-no-template"}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("no template"))
			})

			It("Rejects changing catalog_item on update", func() {
				createCatalogItem("cat-immut", true, nil)

				createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{Name: "test-cluster"}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							CatalogItem: privatev1.ClusterCatalogItemReference_builder{Id: "cat-immut"}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				object := createResponse.GetObject()

				_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: object.GetId(),
						Spec: privatev1.ClusterSpec_builder{
							CatalogItem: privatev1.ClusterCatalogItemReference_builder{Id: "different-catalog-item"}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec.catalog_item"},
					},
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(Equal(
					"cannot change spec.catalog_item from 'cat-immut' to 'different-catalog-item': catalog item is immutable",
				))
			})
		})

		It("Allows changing version to a valid version on update", func() {
			versionName := "4-17-0"

			// Create a cluster with an explicit version:
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-cluster",
					}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
						Version:  &privatev1.ClusterVersionReference{Name: versionName},
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Seed a second usable ClusterVersion:
			seedClusterVersion(ctx, privatev1.ClusterVersion_builder{
				Id: uuid.New(),
				Metadata: privatev1.Metadata_builder{
					Name:   "4-18-0",
					Tenant: testTenant,
				}.Build(),
				Spec: privatev1.ClusterVersionSpec_builder{
					Image:   "quay.io/openshift-release-dev/ocp-release:4.18.0-multi",
					Enabled: proto.Bool(true),
					Version: "4.18.0",
				}.Build(),
			}.Build())

			// Change the version:
			newVersion := "4-18-0"
			updateResponse, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: object.GetId(),
					Spec: privatev1.ClusterSpec_builder{
						Version: &privatev1.ClusterVersionReference{Name: newVersion},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.version"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetSpec().GetVersion().GetName()).To(Equal("4-18-0"))
			for _, nodeSet := range updateResponse.GetObject().GetSpec().GetNodeSets() {
				Expect(nodeSet.GetVersion().GetName()).To(Equal("4-17-0"))
			}

			nodeSets := map[string]*privatev1.ClusterNodeSet{}
			for name, nodeSet := range updateResponse.GetObject().GetSpec().GetNodeSets() {
				nodeSets[name] = proto.Clone(nodeSet).(*privatev1.ClusterNodeSet)
				nodeSets[name].SetVersion(nil)
			}
			nodeSets["compute"].SetSize(nodeSets["compute"].GetSize() + 1)
			nodeSets["storage"] = privatev1.ClusterNodeSet_builder{
				Size: proto.Int32(2),
				BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{
					Id: "acme-bmit-id", Shared: true,
				}.Build(),
			}.Build()
			updated, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id:   object.GetId(),
					Spec: privatev1.ClusterSpec_builder{NodeSets: nodeSets}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.node_sets"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updated.GetObject().GetSpec().GetVersion().GetName()).To(Equal("4-18-0"))
			for _, name := range []string{"compute", "gpu"} {
				Expect(proto.Equal(updated.GetObject().GetSpec().GetNodeSets()[name].GetVersion(),
					object.GetSpec().GetVersion())).To(BeTrue())
			}
			Expect(proto.Equal(updated.GetObject().GetSpec().GetNodeSets()["storage"].GetVersion(),
				updated.GetObject().GetSpec().GetVersion())).To(BeTrue())
		})

		Describe("exclusive component version updates", func() {
			var cluster *privatev1.Cluster

			BeforeEach(func() {
				for _, target := range []struct{ id, name, version string }{
					{"cv-next", "4-18-0", "4.18.0"},
					{"cv-later", "4-19-0", "4.19.0"},
				} {
					seedClusterVersion(ctx, privatev1.ClusterVersion_builder{
						Id: target.id,
						Metadata: privatev1.Metadata_builder{
							Name: target.name, Tenant: testTenant,
						}.Build(),
						Spec: privatev1.ClusterVersionSpec_builder{
							Image:   "quay.io/openshift-release-dev/ocp-release:" + target.version + "-multi",
							Enabled: proto.Bool(true), Version: target.version,
						}.Build(),
					}.Build())
				}
				created, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{Name: "exclusive-versions"}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				cluster = created.GetObject()
			})

			updateVersions := func(controlPlane string, nodeVersions map[string]string) (*privatev1.Cluster, error) {
				nodeSets := make(map[string]*privatev1.ClusterNodeSet, len(cluster.GetSpec().GetNodeSets()))
				for name, nodeSet := range cluster.GetSpec().GetNodeSets() {
					nodeSets[name] = proto.Clone(nodeSet).(*privatev1.ClusterNodeSet)
				}
				for name, version := range nodeVersions {
					nodeSets[name].SetVersion(privatev1.ClusterVersionReference_builder{Name: version}.Build())
				}
				spec := privatev1.ClusterSpec_builder{NodeSets: nodeSets}.Build()
				paths := []string{"spec.node_sets"}
				if controlPlane != "" {
					spec.SetVersion(privatev1.ClusterVersionReference_builder{Name: controlPlane}.Build())
					paths = append(paths, "spec.version")
				}
				updated, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object:     privatev1.Cluster_builder{Id: cluster.GetId(), Spec: spec}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: paths},
				}.Build())
				if err != nil {
					return nil, err
				}
				return updated.GetObject(), nil
			}

			addStorageNodeSet := func(version *privatev1.ClusterVersionReference) map[string]*privatev1.ClusterNodeSet {
				nodeSets := make(map[string]*privatev1.ClusterNodeSet, len(cluster.GetSpec().GetNodeSets())+1)
				for name, nodeSet := range cluster.GetSpec().GetNodeSets() {
					nodeSets[name] = proto.Clone(nodeSet).(*privatev1.ClusterNodeSet)
				}
				nodeSets["storage"] = privatev1.ClusterNodeSet_builder{
					Size: proto.Int32(2),
					BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{
						Id: "acme-bmit-id", Shared: true,
					}.Build(),
					Version: version,
				}.Build()
				return nodeSets
			}

			It("rejects changing the control plane and a node set together", func() {
				_, err := updateVersions("4-18-0", map[string]string{"compute": "4-19-0"})
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(err).To(MatchError(ContainSubstring("only one component version")))
				stored, err := server.Get(ctx, privatev1.ClustersGetRequest_builder{Id: cluster.GetId()}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(proto.Equal(stored.GetObject().GetSpec(), cluster.GetSpec())).To(BeTrue())
			})

			It("rejects changing two node sets together", func() {
				_, err := updateVersions("", map[string]string{"compute": "4-18-0", "gpu": "4-19-0"})
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(err).To(MatchError(ContainSubstring("only one component version")))
			})

			It("accepts one node-set version change", func() {
				response, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: cluster.GetId(),
						Spec: privatev1.ClusterSpec_builder{NodeSets: map[string]*privatev1.ClusterNodeSet{
							"compute": privatev1.ClusterNodeSet_builder{
								Version: privatev1.ClusterVersionReference_builder{Name: "4-18-0"}.Build(),
							}.Build(),
						}}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.node_sets.compute.version"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				updated := response.GetObject()
				Expect(updated.GetSpec().GetNodeSets()["compute"].GetVersion().GetId()).To(Equal("cv-next"))
				Expect(updated.GetSpec().GetNodeSets()["gpu"].GetVersion().GetId()).To(Equal("cv-default"))
				Expect(updated.GetStatus().GetUpgradeHistory()).To(HaveLen(1))
				attempt := updated.GetStatus().GetUpgradeHistory()[0]
				Expect(attempt.GetAttemptId()).NotTo(BeEmpty())
				Expect(attempt.GetComponent().GetNodeSetId()).To(Equal("compute"))
				Expect(attempt.GetSourceVersion()).To(Equal("4.17.0"))
				Expect(attempt.GetTargetVersion()).To(Equal("4.18.0"))
				Expect(attempt.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING))
				Expect(updated.GetSpec().GetNodeSetAttemptIds()["compute"]).To(Equal(attempt.GetAttemptId()))
				Expect(updated.GetSpec().GetNodeSetAttemptIds()["gpu"]).To(BeEmpty())
				scaled, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: cluster.GetId(),
						Spec: privatev1.ClusterSpec_builder{NodeSets: map[string]*privatev1.ClusterNodeSet{
							"compute": privatev1.ClusterNodeSet_builder{Size: proto.Int32(4)}.Build(),
						}}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.node_sets.compute.size"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(scaled.GetObject().GetSpec().GetNodeSets()["compute"].GetVersion().GetId()).To(Equal("cv-next"))
				Expect(scaled.GetObject().GetSpec().GetNodeSetAttemptIds()["compute"]).To(Equal(attempt.GetAttemptId()))
			})

			It("rejects a node-set version change through a node-sets replacement", func() {
				_, err := updateVersions("", map[string]string{"compute": "4-18-0"})
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(err).To(MatchError(ContainSubstring("require a mask containing only")))
			})

			It("rejects changing a node-set size and version in the same masked request", func() {
				original := cluster.GetSpec().GetNodeSets()["compute"]
				newSize := original.GetSize() + 1
				_, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: cluster.GetId(),
						Spec: privatev1.ClusterSpec_builder{NodeSets: map[string]*privatev1.ClusterNodeSet{
							"compute": privatev1.ClusterNodeSet_builder{
								Size:    proto.Int32(newSize),
								Version: privatev1.ClusterVersionReference_builder{Name: "4-18-0"}.Build(),
							}.Build(),
						}}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{
						"spec.node_sets.compute.size", "spec.node_sets.compute.version",
					}},
				}.Build())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(err).To(MatchError(ContainSubstring("a node-set version update cannot include other fields")))
				stored, err := server.Get(ctx, privatev1.ClustersGetRequest_builder{Id: cluster.GetId()}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(proto.Equal(stored.GetObject().GetSpec(), cluster.GetSpec())).To(BeTrue())
			})

			It("rejects changing a node-set size and version in a full-object update", func() {
				full := proto.Clone(cluster).(*privatev1.Cluster)
				compute := full.GetSpec().GetNodeSets()["compute"]
				compute.SetSize(compute.GetSize() + 1)
				compute.SetVersion(privatev1.ClusterVersionReference_builder{Name: "4-18-0"}.Build())
				_, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{Object: full}.Build())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(err).To(MatchError(ContainSubstring("node-set size and node-set version")))
			})

			DescribeTable("allows scaling a node set with a control-plane version change",
				func(mask *fieldmaskpb.FieldMask) {
					full := proto.Clone(cluster).(*privatev1.Cluster)
					compute := full.GetSpec().GetNodeSets()["compute"]
					compute.SetSize(compute.GetSize() + 1)
					full.GetSpec().SetVersion(privatev1.ClusterVersionReference_builder{Name: "4-18-0"}.Build())
					updated, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
						Object: full, UpdateMask: mask,
					}.Build())
					Expect(err).ToNot(HaveOccurred())
					Expect(updated.GetObject().GetSpec().GetVersion().GetId()).To(Equal("cv-next"))
					Expect(updated.GetObject().GetSpec().GetNodeSets()["compute"].GetSize()).To(Equal(compute.GetSize()))
					Expect(updated.GetObject().GetSpec().GetNodeSets()["compute"].GetVersion().GetId()).To(Equal("cv-default"))
				},
				Entry("full object", (*fieldmaskpb.FieldMask)(nil)),
				Entry("full spec", &fieldmaskpb.FieldMask{Paths: []string{"spec"}}),
				Entry("control-plane version and node-set size", &fieldmaskpb.FieldMask{Paths: []string{
					"spec.version", "spec.node_sets.compute.size",
				}}),
			)

			DescribeTable("pins a legacy node set to the established control-plane version when scaling during its upgrade",
				func(mask *fieldmaskpb.FieldMask) {
					clustersDao, err := dao.NewGenericDAO[*privatev1.Cluster]().
						SetLogger(logger).
						SetTenancyLogic(tenancy).
						Build()
					Expect(err).ToNot(HaveOccurred())
					created, err := clustersDao.Create().SetObject(privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{Name: "legacy-scale-with-control-plane", Tenant: testTenant}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: cloneMessage(cluster.GetSpec().GetTemplate()),
							Version:  cloneMessage(cluster.GetSpec().GetVersion()),
							NodeSets: map[string]*privatev1.ClusterNodeSet{
								"compute": privatev1.ClusterNodeSet_builder{
									Size:                  proto.Int32(3),
									BaremetalInstanceType: cloneMessage(cluster.GetSpec().GetNodeSets()["compute"].GetBaremetalInstanceType()),
								}.Build(),
							},
						}.Build(),
						Status: privatev1.ClusterStatus_builder{Hub: "my-hub-id"}.Build(),
					}.Build()).Do(ctx)
					Expect(err).ToNot(HaveOccurred())
					full := proto.Clone(created.GetObject()).(*privatev1.Cluster)
					full.GetSpec().SetVersion(privatev1.ClusterVersionReference_builder{Name: "4-18-0"}.Build())
					full.GetSpec().GetNodeSets()["compute"].SetSize(4)
					_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
						Object: full, UpdateMask: mask,
					}.Build())
					Expect(err).ToNot(HaveOccurred())
					stored, err := server.Get(ctx, privatev1.ClustersGetRequest_builder{Id: full.GetId()}.Build())
					Expect(err).ToNot(HaveOccurred())
					Expect(stored.GetObject().GetSpec().GetVersion().GetId()).To(Equal("cv-next"))
					Expect(stored.GetObject().GetSpec().GetNodeSets()["compute"].GetSize()).To(Equal(int32(4)))
					Expect(stored.GetObject().GetSpec().GetNodeSets()["compute"].GetVersion().GetId()).To(Equal("cv-default"))
				},
				Entry("full object", (*fieldmaskpb.FieldMask)(nil)),
				Entry("control-plane version and node-set size", &fieldmaskpb.FieldMask{Paths: []string{
					"spec.version", "spec.node_sets.compute.size",
				}}),
			)

			It("accepts a control-plane change with unchanged node-set versions", func() {
				updated, err := updateVersions("4-18-0", nil)
				Expect(err).ToNot(HaveOccurred())
				Expect(updated.GetSpec().GetVersion().GetId()).To(Equal("cv-next"))
				for _, nodeSet := range updated.GetSpec().GetNodeSets() {
					Expect(nodeSet.GetVersion().GetId()).To(Equal("cv-default"))
				}
			})

			It("rejects a node-set version change in a full-object update", func() {
				full := proto.Clone(cluster).(*privatev1.Cluster)
				full.GetSpec().GetNodeSets()["compute"].SetVersion(privatev1.ClusterVersionReference_builder{Name: "4-18-0"}.Build())
				_, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{Object: full}.Build())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(err).To(MatchError(ContainSubstring("require a mask containing only")))
			})

			DescribeTable("adding a node set through a node-sets replacement",
				func(version string) {
					var selected *privatev1.ClusterVersionReference
					if version != "" {
						selected = privatev1.ClusterVersionReference_builder{Name: version}.Build()
					}
					updated, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
						Object: privatev1.Cluster_builder{
							Id:   cluster.GetId(),
							Spec: privatev1.ClusterSpec_builder{NodeSets: addStorageNodeSet(selected)}.Build(),
						}.Build(),
						UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.node_sets"}},
					}.Build())
					Expect(err).ToNot(HaveOccurred())
					Expect(updated.GetObject().GetSpec().GetNodeSets()["storage"].GetVersion().GetId()).To(Equal("cv-default"))
					Expect(updated.GetObject().GetSpec().GetNodeSets()["compute"].GetVersion().GetId()).To(Equal("cv-default"))
					Expect(updated.GetObject().GetStatus().GetNodeSets()["storage"].GetObservedVersion()).To(Equal("4.17.0"))
				},
				Entry("fills an omitted version", ""),
				Entry("preserves the explicit control-plane version", "4-17-0"),
			)

			It("rejects another catalog version when adding a node set", func() {
				_, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: cluster.GetId(),
						Spec: privatev1.ClusterSpec_builder{NodeSets: addStorageNodeSet(
							privatev1.ClusterVersionReference_builder{Name: "4-18-0"}.Build())}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.node_sets"}},
				}.Build())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(err).To(MatchError(ContainSubstring("must match the control-plane version")))
				stored, err := server.Get(ctx, privatev1.ClustersGetRequest_builder{Id: cluster.GetId()}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(stored.GetObject().GetSpec().GetNodeSets()).ToNot(HaveKey("storage"))
			})

			DescribeTable("adding a node set while changing the control plane in a full-spec edit",
				func(version string) {
					full := proto.Clone(cluster).(*privatev1.Cluster)
					full.GetSpec().SetVersion(privatev1.ClusterVersionReference_builder{Name: "4-18-0"}.Build())
					var selected *privatev1.ClusterVersionReference
					if version != "" {
						selected = privatev1.ClusterVersionReference_builder{Name: version}.Build()
					}
					full.GetSpec().SetNodeSets(addStorageNodeSet(selected))
					updated, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
						Object: full, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec"}},
					}.Build())
					Expect(err).ToNot(HaveOccurred())
					Expect(updated.GetObject().GetSpec().GetVersion().GetId()).To(Equal("cv-next"))
					Expect(updated.GetObject().GetSpec().GetNodeSets()["storage"].GetVersion().GetId()).To(Equal("cv-default"))
					Expect(updated.GetObject().GetSpec().GetNodeSets()["compute"].GetVersion().GetId()).To(Equal("cv-default"))
				},
				Entry("fills an omitted version from the stored control plane", ""),
				Entry("accepts an explicit version matching the stored control plane", "4-17-0"),
			)

			It("rejects the new control-plane version when adding a node set during its upgrade", func() {
				full := proto.Clone(cluster).(*privatev1.Cluster)
				full.GetSpec().SetVersion(privatev1.ClusterVersionReference_builder{Name: "4-18-0"}.Build())
				full.GetSpec().SetNodeSets(addStorageNodeSet(privatev1.ClusterVersionReference_builder{Name: "4-18-0"}.Build()))
				_, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: full, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec"}},
				}.Build())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(err).To(MatchError(ContainSubstring("must match the control-plane version")))
			})

			It("accepts the control-plane version on cluster creation", func() {
				created, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{Name: "matching-new-node-version"}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
							Version:  privatev1.ClusterVersionReference_builder{Name: "4-17-0"}.Build(),
							NodeSets: addStorageNodeSet(privatev1.ClusterVersionReference_builder{Name: "4-17-0"}.Build()),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(created.GetObject().GetSpec().GetNodeSets()["storage"].GetVersion().GetId()).To(Equal("cv-default"))
			})

			It("rejects another catalog version on cluster creation", func() {
				_, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{Name: "mismatched-new-node-version"}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
							Version:  privatev1.ClusterVersionReference_builder{Name: "4-17-0"}.Build(),
							NodeSets: addStorageNodeSet(privatev1.ClusterVersionReference_builder{Name: "4-18-0"}.Build()),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(err).To(MatchError(ContainSubstring("must match the control-plane version")))
			})

			It("rejects an uncataloged version on a new node set", func() {
				full := proto.Clone(cluster).(*privatev1.Cluster)
				full.GetSpec().SetNodeSets(addStorageNodeSet(privatev1.ClusterVersionReference_builder{
					Name: "missing-version"}.Build()))
				_, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{Object: full}.Build())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				stored, err := server.Get(ctx, privatev1.ClustersGetRequest_builder{Id: cluster.GetId()}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(stored.GetObject().GetSpec().GetNodeSets()).ToNot(HaveKey("storage"))
			})

			It("rejects a version selection for an unknown node set", func() {
				_, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: cluster.GetId(),
						Spec: privatev1.ClusterSpec_builder{NodeSets: map[string]*privatev1.ClusterNodeSet{
							"missing": privatev1.ClusterNodeSet_builder{
								Version: privatev1.ClusterVersionReference_builder{Name: "4-18-0"}.Build(),
							}.Build(),
						}}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.node_sets.missing.version"}},
				}.Build())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			})
		})

		It("pins new node sets to the resolved cluster version", func() {
			response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{Name: "new-versioned-node-sets"}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
					Status: privatev1.ClusterStatus_builder{Hub: "my-hub-id"}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			cluster := response.GetObject()
			Expect(cluster.GetSpec().GetVersion().GetId()).To(Equal("cv-default"))
			Expect(cluster.GetSpec().GetNodeSets()).To(HaveLen(2))
			for _, nodeSet := range cluster.GetSpec().GetNodeSets() {
				Expect(nodeSet.GetVersion()).To(Equal(cluster.GetSpec().GetVersion()))
			}
		})

		It("rejects mixed upgrades and pins legacy node sets on the first control-plane upgrade", func() {
			clustersDao, err := dao.NewGenericDAO[*privatev1.Cluster]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			oldVersion := privatev1.ClusterVersionReference_builder{Id: "cv-default", Name: "4-17-0"}.Build()
			created, err := clustersDao.Create().SetObject(privatev1.Cluster_builder{
				Metadata: privatev1.Metadata_builder{Name: "legacy-node-versions", Tenant: testTenant}.Build(),
				Spec: privatev1.ClusterSpec_builder{
					Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					Version:  oldVersion,
					NodeSets: map[string]*privatev1.ClusterNodeSet{
						"compute": privatev1.ClusterNodeSet_builder{
							Size: proto.Int32(3), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id", Shared: true}.Build(),
						}.Build(),
						"gpu": privatev1.ClusterNodeSet_builder{
							Size: proto.Int32(1), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-gpu-bmit-id", Shared: true}.Build(),
						}.Build(),
					},
				}.Build(),
				Status: privatev1.ClusterStatus_builder{Hub: "my-hub-id"}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			clusterID := created.GetObject().GetId()

			seedClusterVersion(ctx, privatev1.ClusterVersion_builder{
				Id:       "cv-next",
				Metadata: privatev1.Metadata_builder{Name: "4-18-0", Tenant: testTenant}.Build(),
				Spec: privatev1.ClusterVersionSpec_builder{
					Image: "quay.io/openshift-release-dev/ocp-release:4.18.0-multi", Enabled: proto.Bool(true), Version: "4.18.0",
				}.Build(),
			}.Build())

			// Both stored NodeSets predate independent versions. An explicit target on
			// compute still counts as a second change alongside the control plane.
			mixedNodeSets := make(map[string]*privatev1.ClusterNodeSet)
			for name, nodeSet := range created.GetObject().GetSpec().GetNodeSets() {
				Expect(nodeSet.GetVersion()).To(BeNil())
				mixedNodeSets[name] = proto.Clone(nodeSet).(*privatev1.ClusterNodeSet)
			}
			mixedNodeSets["compute"].SetVersion(privatev1.ClusterVersionReference_builder{Name: "4-18-0"}.Build())
			_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: clusterID,
					Spec: privatev1.ClusterSpec_builder{
						Version:  privatev1.ClusterVersionReference_builder{Name: "4-18-0"}.Build(),
						NodeSets: mixedNodeSets,
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.version", "spec.node_sets"}},
			}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
			Expect(err).To(MatchError(ContainSubstring("only one component version")))

			metadataOnly, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id:       clusterID,
					Metadata: privatev1.Metadata_builder{Description: "edited"}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.description"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(metadataOnly.GetObject().GetSpec(), created.GetObject().GetSpec())).To(BeTrue())
			sizeOnly, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: clusterID,
					Spec: privatev1.ClusterSpec_builder{NodeSets: map[string]*privatev1.ClusterNodeSet{
						"compute": privatev1.ClusterNodeSet_builder{Size: proto.Int32(4)}.Build(),
					}}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.node_sets.compute.size"}},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			for _, nodeSet := range sizeOnly.GetObject().GetSpec().GetNodeSets() {
				Expect(nodeSet.GetVersion().GetId()).To(Equal(oldVersion.GetId()))
			}

			updateVersion := func(version string) *privatev1.Cluster {
				response, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: clusterID,
						Spec: privatev1.ClusterSpec_builder{
							Version: privatev1.ClusterVersionReference_builder{Name: version}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.version"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				return response.GetObject()
			}

			unchanged := updateVersion("4-17-0")
			Expect(unchanged.GetSpec().GetVersion().GetId()).To(Equal(oldVersion.GetId()))
			for _, nodeSet := range unchanged.GetSpec().GetNodeSets() {
				Expect(nodeSet.GetVersion().GetId()).To(Equal(oldVersion.GetId()))
			}
			updateNodeSetVersion := func(version string) *privatev1.Cluster {
				response, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: clusterID,
						Spec: privatev1.ClusterSpec_builder{NodeSets: map[string]*privatev1.ClusterNodeSet{
							"compute": privatev1.ClusterNodeSet_builder{
								Version: privatev1.ClusterVersionReference_builder{Name: version}.Build(),
							}.Build(),
						}}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.node_sets.compute.version"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				return response.GetObject()
			}
			unchangedNodeSet := updateNodeSetVersion("4-17-0")
			Expect(unchangedNodeSet.GetSpec().GetNodeSets()["compute"].GetVersion().GetId()).To(Equal(oldVersion.GetId()))
			workerUpdated := updateNodeSetVersion("4-18-0")
			Expect(workerUpdated.GetSpec().GetNodeSets()["compute"].GetVersion().GetId()).To(Equal("cv-next"))
			Expect(workerUpdated.GetSpec().GetNodeSets()["gpu"].GetVersion().GetId()).To(Equal(oldVersion.GetId()))

			updated := updateVersion("4-18-0")
			Expect(updated.GetSpec().GetVersion().GetName()).To(Equal("4-18-0"))
			Expect(updated.GetSpec().GetNodeSets()["compute"].GetVersion().GetId()).To(Equal("cv-next"))
			Expect(updated.GetSpec().GetNodeSets()["gpu"].GetVersion().GetId()).To(Equal(oldVersion.GetId()))
			updated = updateVersion("4-17-0")
			Expect(updated.GetSpec().GetNodeSets()["compute"].GetVersion().GetId()).To(Equal("cv-next"))
			Expect(updated.GetSpec().GetNodeSets()["gpu"].GetVersion().GetId()).To(Equal(oldVersion.GetId()))
		})

		It("Preserves existing version when update sends empty name", func() {
			versionName := "4-17-0"

			// Create a cluster with an explicit version:
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-cluster",
					}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
						Version:  &privatev1.ClusterVersionReference{Name: versionName},
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Update with an explicitly empty version name:
			updateResponse, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: object.GetId(),
					Spec: privatev1.ClusterSpec_builder{
						Version: &privatev1.ClusterVersionReference{Name: ""},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.version"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetSpec().GetVersion().GetName()).To(Equal("4-17-0"))
		})

		It("Rejects changing version to a non-existent version", func() {
			versionName := "4-17-0"

			// Create a cluster with an explicit version:
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
					}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
						Version:  &privatev1.ClusterVersionReference{Name: versionName},
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Try to change to a non-existent version:
			nonExistent := "does-not-exist"
			_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: object.GetId(),
					Spec: privatev1.ClusterSpec_builder{
						Version: &privatev1.ClusterVersionReference{Name: nonExistent},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.version"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		})

		It("Rejects changing version to a disabled version", func() {
			versionName := "4-17-0"

			// Create a cluster with an explicit version:
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
					}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
						Version:  &privatev1.ClusterVersionReference{Name: versionName},
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Seed a disabled ClusterVersion:
			seedClusterVersion(ctx, privatev1.ClusterVersion_builder{
				Id: uuid.New(),
				Metadata: privatev1.Metadata_builder{
					Name:   "4-18-0-disabled",
					Tenant: testTenant,
				}.Build(),
				Spec: privatev1.ClusterVersionSpec_builder{
					Image:   "quay.io/openshift-release-dev/ocp-release:4.18.0-multi",
					Enabled: proto.Bool(false),
					Version: "4.18.0",
				}.Build(),
			}.Build())

			// Try to change to the disabled version:
			disabledName := "4-18-0-disabled"
			_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: object.GetId(),
					Spec: privatev1.ClusterSpec_builder{
						Version: &privatev1.ClusterVersionReference{Name: disabledName},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.version"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		})

		It("Rejects changing version to an obsolete version", func() {
			versionName := "4-17-0"

			// Create a cluster with an explicit version:
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
					}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
						Version:  &privatev1.ClusterVersionReference{Name: versionName},
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Seed an obsolete ClusterVersion:
			seedClusterVersion(ctx, privatev1.ClusterVersion_builder{
				Id: uuid.New(),
				Metadata: privatev1.Metadata_builder{
					Name:   "4-16-0-obsolete",
					Tenant: testTenant,
				}.Build(),
				Spec: privatev1.ClusterVersionSpec_builder{
					Image:   "quay.io/openshift-release-dev/ocp-release:4.16.0-multi",
					Enabled: proto.Bool(true),
					Version: "4.16.0",
					State:   privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_OBSOLETE,
				}.Build(),
			}.Build())

			// Try to change to the obsolete version:
			obsoleteName := "4-16-0-obsolete"
			_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: object.GetId(),
					Spec: privatev1.ClusterSpec_builder{
						Version: &privatev1.ClusterVersionReference{Name: obsoleteName},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.version"},
				},
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		})

		It("Rejects non-existent version on nil-mask full-object update", func() {
			versionName := "4-17-0"

			// Create a cluster with a valid version:
			createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
				Object: privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
					}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
						Version:  &privatev1.ClusterVersionReference{Name: versionName},
					}.Build(),
					Status: privatev1.ClusterStatus_builder{
						Hub: "my-hub-id",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Full-object update with nil mask and a non-existent version:
			object.GetSpec().SetVersion(&privatev1.ClusterVersionReference{Name: "does-not-exist"})
			_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: object,
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
		})

		Describe("ClusterVersion validation", func() {
			var validatedServer *clusterCreationFixture

			BeforeEach(func() {
				privateServer, buildErr := NewPrivateClustersServer().
					SetLogger(logger).
					SetAttributionLogic(attribution).
					SetTenancyLogic(tenancy).
					Build()
				Expect(buildErr).ToNot(HaveOccurred())
				validatedServer = &clusterCreationFixture{PrivateClustersServer: privateServer}
			})

			It("Rejects updating a cluster to a ClusterVersion without a disk image", func() {
				seedClusterVersionWithoutDiskImage(ctx, privatev1.ClusterVersion_builder{
					Metadata: privatev1.Metadata_builder{Name: "version-without-disk-image", Tenant: testTenant}.Build(),
					Spec:     privatev1.ClusterVersionSpec_builder{Version: "4.20.0", Image: "quay.io/ocp:4.20.0", Enabled: proto.Bool(true)}.Build(),
				}.Build())
				created, err := validatedServer.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{Name: "cluster-version-update"}.Build(),
						Spec:     privatev1.ClusterSpec_builder{Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"}}.Build(),
						Status:   privatev1.ClusterStatus_builder{Hub: "my-hub-id"}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				_, err = validatedServer.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id:   created.GetObject().GetId(),
						Spec: privatev1.ClusterSpec_builder{Version: &privatev1.ClusterVersionReference{Name: "version-without-disk-image"}}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.version"}},
				}.Build())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
				Expect(grpcstatus.Convert(err).Message()).To(ContainSubstring("disk image"))
			})

			It("Rejects a BM cluster when the explicitly selected ClusterVersion has no DiskImage", func() {
				seedClusterVersionWithoutDiskImage(ctx, privatev1.ClusterVersion_builder{
					Id: uuid.New(),
					Metadata: privatev1.Metadata_builder{
						Name:   "4-19-0-no-disk-image",
						Tenant: testTenant,
					}.Build(),
					Spec: privatev1.ClusterVersionSpec_builder{
						Image:   "quay.io/openshift-release-dev/ocp-release:4.19.0-multi",
						Enabled: proto.Bool(true),
						Version: "4.19.0",
					}.Build(),
				}.Build())

				_, err := validatedServer.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{Name: fmt.Sprintf("test-%s", uuid.New()[24:32])}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
							Version:  &privatev1.ClusterVersionReference{Name: "4-19-0-no-disk-image"},
						}.Build(),
						Status: privatev1.ClusterStatus_builder{Hub: "my-hub-id"}.Build(),
					}.Build(),
				}.Build())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
				Expect(grpcstatus.Convert(err).Message()).To(ContainSubstring("disk image"))
			})

			It("Does not auto-resolve a BM cluster to a default ClusterVersion without a DiskImage", func() {
				clusterVersionsDao, err := dao.NewGenericDAO[*privatev1.ClusterVersion]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
				_, err = clusterVersionsDao.Delete().SetId("cv-default").Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				seedClusterVersionWithoutDiskImage(ctx, privatev1.ClusterVersion_builder{
					Id:       "cv-default",
					Metadata: privatev1.Metadata_builder{Name: "4-17-0", Tenant: testTenant}.Build(),
					Spec: privatev1.ClusterVersionSpec_builder{
						Image:     "quay.io/openshift-release-dev/ocp-release:4.17.0-multi",
						Enabled:   proto.Bool(true),
						IsDefault: proto.Bool(true),
						Version:   "4.17.0",
						State:     privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_ACTIVE,
					}.Build(),
				}.Build())
				seedClusterVersion(ctx, privatev1.ClusterVersion_builder{
					Id:       "cv-non-default-with-disk-image",
					Metadata: privatev1.Metadata_builder{Name: "4-18-0", Tenant: testTenant}.Build(),
					Spec: privatev1.ClusterVersionSpec_builder{
						Image:     "quay.io/openshift-release-dev/ocp-release:4.18.0-multi",
						DiskImage: privatev1.DiskImageReference_builder{Id: "test-disk-image"}.Build(),
						Enabled:   proto.Bool(true),
						Version:   "4.18.0",
						State:     privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_ACTIVE,
					}.Build(),
				}.Build())

				_, err = validatedServer.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{Name: fmt.Sprintf("test-%s", uuid.New()[24:32])}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
						}.Build(),
						Status: privatev1.ClusterStatus_builder{Hub: "my-hub-id"}.Build(),
					}.Build(),
				}.Build())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
				Expect(grpcstatus.Convert(err).Message()).To(ContainSubstring("disk image"))
			})

			It("Rejects a ClusterTemplate-pinned BM ClusterVersion without a DiskImage", func() {
				seedClusterVersionWithoutDiskImage(ctx, privatev1.ClusterVersion_builder{
					Id: uuid.New(),
					Metadata: privatev1.Metadata_builder{
						Name:   "4-19-0-template-no-disk-image",
						Tenant: testTenant,
					}.Build(),
					Spec: privatev1.ClusterVersionSpec_builder{
						Image:   "quay.io/openshift-release-dev/ocp-release:4.19.0-multi",
						Enabled: proto.Bool(true),
						Version: "4.19.0",
					}.Build(),
				}.Build())

				templatesDao, err := dao.NewGenericDAO[*privatev1.ClusterTemplate]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
				_, err = templatesDao.Create().SetObject(privatev1.ClusterTemplate_builder{
					Id:       "template-pinned-no-disk-image",
					Metadata: privatev1.Metadata_builder{Name: "template-pinned-no-disk-image", Tenant: testTenant}.Build(),
					Title:    "Template with incompatible version",
					SpecDefaults: privatev1.ClusterTemplateSpecDefaults_builder{
						Version: &privatev1.ClusterVersionReference{Name: "4-19-0-template-no-disk-image"},
					}.Build(),
				}.Build()).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				_, err = validatedServer.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{Name: fmt.Sprintf("test-%s", uuid.New()[24:32])}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: &privatev1.ClusterTemplateReference{Id: "template-pinned-no-disk-image"},
						}.Build(),
						Status: privatev1.ClusterStatus_builder{Hub: "my-hub-id"}.Build(),
					}.Build(),
				}.Build())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.FailedPrecondition))
				Expect(grpcstatus.Convert(err).Message()).To(ContainSubstring("disk image"))
			})

			It("Rejects create with non-existent version", func() {
				_, err := validatedServer.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
							Version:  &privatev1.ClusterVersionReference{Name: "does-not-exist"},
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("version 'does-not-exist' not found"))
			})

			It("Rejects create with disabled version", func() {
				// Seed a disabled ClusterVersion:
				seedClusterVersion(ctx, privatev1.ClusterVersion_builder{
					Id: uuid.New(),
					Metadata: privatev1.Metadata_builder{
						Name:   "4-18-0-disabled",
						Tenant: testTenant,
					}.Build(),
					Spec: privatev1.ClusterVersionSpec_builder{
						Image:   "quay.io/openshift-release-dev/ocp-release:4.18.0-multi",
						Enabled: proto.Bool(false),
						Version: "4.18.0",
					}.Build(),
				}.Build())

				disabledName := "4-18-0-disabled"
				_, err := validatedServer.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
							Version:  &privatev1.ClusterVersionReference{Name: disabledName},
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("is disabled"))
			})

			It("Rejects create with obsolete version", func() {
				// Seed an obsolete ClusterVersion:
				seedClusterVersion(ctx, privatev1.ClusterVersion_builder{
					Id: uuid.New(),
					Metadata: privatev1.Metadata_builder{
						Name:   "4-16-0-obsolete",
						Tenant: testTenant,
					}.Build(),
					Spec: privatev1.ClusterVersionSpec_builder{
						Image:   "quay.io/openshift-release-dev/ocp-release:4.16.0-multi",
						Enabled: proto.Bool(true),
						Version: "4.16.0",
						State:   privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_OBSOLETE,
					}.Build(),
				}.Build())

				obsoleteName := "4-16-0-obsolete"
				_, err := validatedServer.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
							Version:  &privatev1.ClusterVersionReference{Name: obsoleteName},
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("is obsolete"))
			})

			It("Resolves system default version when none specified", func() {
				// The BeforeEach in the parent Behaviour block already seeds a default
				// ClusterVersion with name "4-17-0" and is_default=true.
				response, err := validatedServer.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetObject().GetSpec().GetVersion().GetName()).To(Equal("4-17-0"))
			})

			It("Rejects create when no system default version exists", func() {
				// Delete the default ClusterVersion:
				clusterVersionsDao, err := dao.NewGenericDAO[*privatev1.ClusterVersion]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
				_, err = clusterVersionsDao.Delete().
					SetId("cv-default").
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				_, err = validatedServer.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
				Expect(status.Message()).To(ContainSubstring("no version specified and no system default"))
			})

			It("Rejects create when system default version is disabled", func() {
				// Replace the existing default with a disabled one:
				clusterVersionsDao, err := dao.NewGenericDAO[*privatev1.ClusterVersion]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
				_, err = clusterVersionsDao.Delete().
					SetId("cv-default").
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				_, err = clusterVersionsDao.Create().
					SetObject(
						privatev1.ClusterVersion_builder{
							Id: uuid.New(),
							Metadata: privatev1.Metadata_builder{
								Name:   "4-18-0-disabled-default",
								Tenant: testTenant,
							}.Build(),
							Spec: privatev1.ClusterVersionSpec_builder{
								Image:     "quay.io/openshift-release-dev/ocp-release:4.18.0-multi",
								DiskImage: privatev1.DiskImageReference_builder{Id: "test-disk-image-id"}.Build(),
								Enabled:   proto.Bool(false),
								IsDefault: proto.Bool(true),
								Version:   "4.18.0",
								State:     privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_ACTIVE,
							}.Build(),
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				_, err = validatedServer.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("is disabled"))
			})

			It("Rejects create when system default version is obsolete", func() {
				// Replace the existing default with an obsolete one:
				clusterVersionsDao, err := dao.NewGenericDAO[*privatev1.ClusterVersion]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
				_, err = clusterVersionsDao.Delete().
					SetId("cv-default").
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				_, err = clusterVersionsDao.Create().
					SetObject(
						privatev1.ClusterVersion_builder{
							Id: uuid.New(),
							Metadata: privatev1.Metadata_builder{
								Name:   "4-16-0-obsolete-default",
								Tenant: testTenant,
							}.Build(),
							Spec: privatev1.ClusterVersionSpec_builder{
								Image:     "quay.io/openshift-release-dev/ocp-release:4.16.0-multi",
								DiskImage: privatev1.DiskImageReference_builder{Id: "test-disk-image-id"}.Build(),
								Enabled:   proto.Bool(true),
								IsDefault: proto.Bool(true),
								Version:   "4.16.0",
								State:     privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_OBSOLETE,
							}.Build(),
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				_, err = validatedServer.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: &privatev1.ClusterTemplateReference{Id: "my-template-id"},
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(ContainSubstring("is obsolete"))
			})
		})

		Describe("Version", func() {
			createCluster := func() *privatev1.Cluster {
				response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{Name: "test-cluster"}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				return response.GetObject()
			}

			It("Is zero on create", func() {
				object := createCluster()
				Expect(object.GetMetadata().GetVersion()).To(BeZero())
			})

			It("Is zero when retrieved after create", func() {
				object := createCluster()
				id := object.GetId()
				getResponse, err := server.Get(ctx, privatev1.ClustersGetRequest_builder{
					Id: id,
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				object = getResponse.GetObject()
				Expect(object.GetMetadata().GetVersion()).To(BeZero())
			})

			It("Is zero when listed after create", func() {
				object := createCluster()
				id := object.GetId()
				listResponse, err := server.List(ctx, privatev1.ClustersListRequest_builder{
					Filter: new(fmt.Sprintf("this.id == %q", id)),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				items := listResponse.GetItems()
				Expect(items).To(HaveLen(1))
				item := items[0]
				Expect(item.GetMetadata().GetVersion()).To(BeZero())
			})

			It("Increments on update", func() {
				// Create the object:
				object := createCluster()
				version := object.GetMetadata().GetVersion()

				// Update the object and verify that the version has been incremented:
				object.GetStatus().SetHub("hub-v1")
				updateResponse, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: object,
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				object = updateResponse.GetObject()
				Expect(object.GetMetadata().GetVersion()).To(BeNumerically(">", version))
			})

			It("Does not increment on no-op update", func() {
				// Create the object and get the initialversion:
				object := createCluster()
				version := object.GetMetadata().GetVersion()

				// Send an update request that doesn't really update anything, and then verify that the
				// version has not been incremented:
				updateResponse, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: object,
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				object = updateResponse.GetObject()
				Expect(object.GetMetadata().GetVersion()).To(Equal(version))
			})

			It("Lock succeeds when version matches", func() {
				// Create the object:
				object := createCluster()

				// Update with lock enabled and the right version:
				object.GetStatus().SetHub("your-hub-id")
				_, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: object,
					Lock:   true,
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			})

			It("Lock fails when version does not match", func() {
				// Create the object:
				object := createCluster()

				// Try to update with lock enabled but a wrong version:
				object.GetMetadata().SetVersion(math.MaxInt32)
				object.GetStatus().SetHub("your-hub-id")
				_, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: object,
					Lock:   true,
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.Aborted))

				// Verify that our changes were not applied:
				getResponse, err := server.Get(ctx, privatev1.ClustersGetRequest_builder{
					Id: object.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				object = getResponse.GetObject()
				Expect(object.GetStatus().GetHub()).To(Equal("my-hub-id"))
			})

			It("Lock is not enabled by default", func() {
				// Create the object:
				object := createCluster()

				// Send an update with a wrong version in the metadata but without enabling lock. The update
				// should succeed because optimistic locking is not enabled.
				object.GetMetadata().SetVersion(math.MaxInt32)
				object.GetStatus().SetHub("your-hub-id")
				_, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: object,
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				// Verify that our changes were applied:
				getResponse, err := server.Get(ctx, privatev1.ClustersGetRequest_builder{
					Id: object.GetId(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				object = getResponse.GetObject()
				Expect(object.GetStatus().GetHub()).To(Equal("your-hub-id"))

			})
		})

		Describe("Version transition time", func() {
			createCluster := func() *privatev1.Cluster {
				response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{Name: "test-cluster"}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				return response.GetObject()
			}

			seedNextVersion := func() {
				seedClusterVersion(ctx, privatev1.ClusterVersion_builder{
					Id: "cv-next",
					Metadata: privatev1.Metadata_builder{
						Name:   "4-18-0",
						Tenant: testTenant,
					}.Build(),
					Spec: privatev1.ClusterVersionSpec_builder{
						Image:   "quay.io/openshift-release-dev/ocp-release:4.18.0-multi",
						Enabled: proto.Bool(true),
						Version: "4.18.0",
					}.Build(),
				}.Build())
			}

			updateVersion := func(id, name string) *privatev1.Cluster {
				response, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: id,
						Spec: privatev1.ClusterSpec_builder{
							Version: privatev1.ClusterVersionReference_builder{Name: name}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.version"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				return response.GetObject()
			}
			updateNodeSetVersion := func(id, name string) *privatev1.Cluster {
				response, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{Id: id,
						Spec: privatev1.ClusterSpec_builder{NodeSets: map[string]*privatev1.ClusterNodeSet{
							name: privatev1.ClusterNodeSet_builder{Version: privatev1.ClusterVersionReference_builder{Name: "4-18-0"}.Build()}.Build(),
						}}.Build()}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.node_sets." + name + ".version"}},
				}.Build())
				Expect(err).NotTo(HaveOccurred())
				return response.GetObject()
			}

			It("Is unset until the version changes", func() {
				object := createCluster()
				Expect(object.GetStatus().GetVersionTransitionTime()).To(BeNil())
			})

			It("Is stamped when a version change is accepted", func() {
				object := createCluster()
				seedNextVersion()

				updated := updateVersion(object.GetId(), "4-18-0")

				Expect(updated.GetSpec().GetVersion().GetName()).To(Equal("4-18-0"))
				Expect(updated.GetStatus().GetVersionTransitionTime()).ToNot(BeNil())
				Expect(updated.GetStatus().GetUpgradeHistory()).To(HaveLen(1))
				attempt := updated.GetStatus().GetUpgradeHistory()[0]
				Expect(attempt.GetAttemptId()).NotTo(BeEmpty())
				Expect(attempt.GetComponent().GetControlPlane()).NotTo(BeNil())
				Expect(attempt.GetSourceVersion()).To(Equal("4.17.0"))
				Expect(attempt.GetTargetVersion()).To(Equal("4.18.0"))
				Expect(attempt.GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PENDING))
				Expect(attempt.GetAcceptedTime()).To(Equal(attempt.GetLastStateTime()))
				Expect(updated.GetSpec().GetControlPlaneAttemptId()).To(Equal(attempt.GetAttemptId()))
			})

			It("Is not re-stamped when the same version is set again", func() {
				object := createCluster()
				seedNextVersion()

				updated := updateVersion(object.GetId(), "4-18-0")
				stamp := updated.GetStatus().GetVersionTransitionTime()
				Expect(stamp).ToNot(BeNil())

				again := updateVersion(updated.GetId(), "4-18-0")

				Expect(again.GetStatus().GetVersionTransitionTime().AsTime()).To(Equal(stamp.AsTime()))
				Expect(again.GetStatus().GetUpgradeHistory()).To(HaveLen(1))
				Expect(again.GetSpec().GetControlPlaneAttemptId()).To(Equal(updated.GetSpec().GetControlPlaneAttemptId()))
			})

			It("keeps independent component IDs and newest-first history", func() {
				cluster := createCluster()
				seedNextVersion()
				controlPlane := updateVersion(cluster.GetId(), "4-18-0")
				cpID := controlPlane.GetSpec().GetControlPlaneAttemptId()
				compute := updateNodeSetVersion(cluster.GetId(), "compute")
				computeID := compute.GetSpec().GetNodeSetAttemptIds()["compute"]
				gpu := updateNodeSetVersion(cluster.GetId(), "gpu")
				gpuID := gpu.GetSpec().GetNodeSetAttemptIds()["gpu"]
				Expect(gpuID).NotTo(Equal(computeID))
				Expect(gpu.GetSpec().GetNodeSetAttemptIds()["compute"]).To(Equal(computeID))
				Expect(gpu.GetSpec().GetControlPlaneAttemptId()).To(Equal(cpID))
				history := gpu.GetStatus().GetUpgradeHistory()
				Expect(history).To(HaveLen(3))
				Expect(history[0].GetAttemptId()).To(Equal(gpuID))
				Expect(history[1].GetAttemptId()).To(Equal(computeID))
				Expect(history[2].GetAttemptId()).To(Equal(cpID))
			})

			It("updates an earlier component's history entry without reordering it", func() {
				cluster := createCluster()
				seedNextVersion()
				controlPlane := updateVersion(cluster.GetId(), "4-18-0")
				cpID := controlPlane.GetSpec().GetControlPlaneAttemptId()
				compute := updateNodeSetVersion(cluster.GetId(), "compute")
				history := compute.GetStatus().GetUpgradeHistory()
				Expect(history).To(HaveLen(2))
				cpTransition := &privatev1.ClusterUpgradeHistoryEntry{}
				cpTransition.SetAttemptId(cpID)
				cpTransition.SetState(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING)
				cpTransition.SetLastStateTime(timestamppb.New(history[1].GetLastStateTime().AsTime().Add(time.Second)))
				cpTransition.SetMessage("Control plane is updating")
				response, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{Id: cluster.GetId(),
						Status: privatev1.ClusterStatus_builder{UpgradeHistory: []*privatev1.ClusterUpgradeHistoryEntry{cpTransition}}.Build()}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.upgrade_history"}},
				}.Build())
				Expect(err).NotTo(HaveOccurred())
				merged := response.GetObject().GetStatus().GetUpgradeHistory()
				Expect(merged).To(HaveLen(2))
				Expect(merged[0].GetAttemptId()).To(Equal(compute.GetSpec().GetNodeSetAttemptIds()["compute"]))
				Expect(merged[1].GetAttemptId()).To(Equal(cpID))
				Expect(merged[1].GetState()).To(Equal(privatev1.ClusterUpgradeState_CLUSTER_UPGRADE_STATE_PROGRESSING))
				Expect(proto.Equal(merged[1].GetComponent(), history[1].GetComponent())).To(BeTrue())
				Expect(proto.Equal(merged[1].GetAcceptedTime(), history[1].GetAcceptedTime())).To(BeTrue())
			})

			It("preserves server-owned attempt IDs on full-object and parent-spec edits", func() {
				cluster := createCluster()
				seedNextVersion()
				updated := updateVersion(cluster.GetId(), "4-18-0")
				id := updated.GetSpec().GetControlPlaneAttemptId()
				full := proto.Clone(updated).(*privatev1.Cluster)
				full.GetSpec().SetControlPlaneAttemptId("caller-value")
				full.GetStatus().SetUpgradeHistory(nil)
				response, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{Object: full}.Build())
				Expect(err).NotTo(HaveOccurred())
				Expect(response.GetObject().GetSpec().GetControlPlaneAttemptId()).To(Equal(id))
				Expect(response.GetObject().GetStatus().GetUpgradeHistory()).To(HaveLen(1))
				partial := privatev1.Cluster_builder{Id: cluster.GetId(),
					Spec: proto.Clone(updated.GetSpec()).(*privatev1.ClusterSpec)}.Build()
				partial.GetSpec().SetControlPlaneAttemptId("other-caller-value")
				response, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: partial, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec"}},
				}.Build())
				Expect(err).NotTo(HaveOccurred())
				Expect(response.GetObject().GetSpec().GetControlPlaneAttemptId()).To(Equal(id))
			})

			// createClusterWithIdOnlyVersion inserts a cluster whose stored version reference
			// carries the id but not the name, the legacy shape that predates the enrichment
			// always writing {id, name}. Inserts bypass the metering source contract.
			createClusterWithIdOnlyVersion := func() *privatev1.Cluster {
				clustersDao, err := dao.NewGenericDAO[*privatev1.Cluster]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
				response, err := clustersDao.Create().SetObject(privatev1.Cluster_builder{
					Metadata: privatev1.Metadata_builder{Name: "test-cluster", Tenant: testTenant}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
						Version:  privatev1.ClusterVersionReference_builder{Id: "cv-default"}.Build(),
					}.Build(),
					Status: privatev1.ClusterStatus_builder{Hub: "my-hub-id"}.Build(),
				}.Build()).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
				return response.GetObject()
			}

			It("Is not stamped when enrichment only fills the name into an id-only stored reference", func() {
				object := createClusterWithIdOnlyVersion()

				updated := updateVersion(object.GetId(), "4-17-0")

				// The requested version resolves to the same id, so the stored reference is
				// kept as-is, no transition is stamped, and billing is not split.
				Expect(updated.GetSpec().GetVersion().GetId()).To(Equal("cv-default"))
				Expect(updated.GetStatus().GetVersionTransitionTime()).To(BeNil())
			})

			It("Is stamped when the version really changes after an enrichment-only update", func() {
				object := createClusterWithIdOnlyVersion()
				enriched := updateVersion(object.GetId(), "4-17-0")
				Expect(enriched.GetStatus().GetVersionTransitionTime()).To(BeNil())

				seedNextVersion()
				changed := updateVersion(enriched.GetId(), "4-18-0")

				Expect(changed.GetSpec().GetVersion().GetName()).To(Equal("4-18-0"))
				Expect(changed.GetSpec().GetVersion().GetId()).To(Equal("cv-next"))
				Expect(changed.GetStatus().GetVersionTransitionTime()).ToNot(BeNil())
			})

			It("Is preserved by updates that do not change the version", func() {
				object := createCluster()
				seedNextVersion()

				updated := updateVersion(object.GetId(), "4-18-0")
				stamp := updated.GetStatus().GetVersionTransitionTime()
				Expect(stamp).ToNot(BeNil())

				response, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: updated.GetId(),
						Spec: privatev1.ClusterSpec_builder{
							Version: privatev1.ClusterVersionReference_builder{Name: "4-18-0"}.Build(),
							NodeSets: map[string]*privatev1.ClusterNodeSet{
								"compute": privatev1.ClusterNodeSet_builder{
									Size:                  proto.Int32(3),
									BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id", Shared: true}.Build(),
								}.Build(),
							},
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.node_sets"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				Expect(response.GetObject().GetStatus().GetVersionTransitionTime().AsTime()).To(Equal(stamp.AsTime()))
			})
		})

		Describe("Dry run", func() {
			It("Returns resolved cluster with template path", func() {
				response, err := server.Create(dryRunCtx(), privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "test-cluster",
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Name: "my-template-name"}.Build(),
							NodeSets: map[string]*privatev1.ClusterNodeSet{
								"compute": privatev1.ClusterNodeSet_builder{
									BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Name: "acme-bmit-name", Shared: true}.Build(),
									Size:                  proto.Int32(7),
								}.Build(),
							},
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				object := response.GetObject()
				Expect(object).ToNot(BeNil())
				Expect(object.GetSpec().GetTemplate().GetId()).To(Equal("my-template-id"))
				nodeSets := object.GetSpec().GetNodeSets()
				Expect(nodeSets).To(HaveKey("compute"))
				Expect(nodeSets["compute"].GetBaremetalInstanceType().GetId()).To(Equal("acme-bmit-id"))
			})

			It("Returns resolved cluster with catalog item path", func() {
				catalogItemsDao, err := dao.NewGenericDAO[*privatev1.ClusterCatalogItem]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
				_, err = catalogItemsDao.Create().SetObject(
					privatev1.ClusterCatalogItem_builder{
						Id: "cat-dry-run",
						Metadata: privatev1.Metadata_builder{
							Name:   "cat-dry-run-name",
							Tenant: testTenant,
						}.Build(),
						Title:     "Dry Run Catalog Item",
						Published: true,
						Template:  privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				response, err := server.Create(dryRunCtx(), privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "test-cluster",
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							CatalogItem: privatev1.ClusterCatalogItemReference_builder{Id: "cat-dry-run"}.Build(),
							NodeSets:    map[string]*privatev1.ClusterNodeSet{"compute": privatev1.ClusterNodeSet_builder{Size: proto.Int32(3), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id", Shared: true}.Build()}.Build()},
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				object := response.GetObject()
				Expect(object).ToNot(BeNil())
				Expect(object.GetSpec().GetTemplate().GetId()).To(Equal("my-template-id"))
				Expect(object.GetSpec().GetCatalogItem().GetId()).To(Equal("cat-dry-run"))
			})

			It("Does not persist the object", func() {
				_, err := server.Create(dryRunCtx(), privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "test-cluster",
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Name: "my-template-name"}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				listResponse, err := server.List(ctx, privatev1.ClustersListRequest_builder{}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(listResponse.GetTotal()).To(Equal(int32(0)))
			})

			It("Returns same error as real creation for invalid template", func() {
				_, realErr := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "test-cluster",
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: "non-existent-template"}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(realErr).To(HaveOccurred())

				_, dryRunErr := server.Create(dryRunCtx(), privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "test-cluster",
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: "non-existent-template"}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(dryRunErr).To(HaveOccurred())
				Expect(grpcstatus.Code(dryRunErr)).To(Equal(grpcstatus.Code(realErr)))
				Expect(grpcstatus.Convert(dryRunErr).Message()).To(Equal(grpcstatus.Convert(realErr).Message()))
			})
		})

		Describe("Pull secret secret reference", func() {
			var secretsDao *dao.GenericDAO[*privatev1.Secret]

			BeforeEach(func() {
				var err error
				secretsDao, err = dao.NewGenericDAO[*privatev1.Secret]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())

				_, err = secretsDao.Create().SetObject(privatev1.Secret_builder{
					Id:   "my-secret-id",
					Type: privatev1.SecretType_SECRET_TYPE_PULL_SECRET,
					Metadata: privatev1.Metadata_builder{
						Name:   "my-secret-name",
						Tenant: testTenant,
					}.Build(),
				}.Build()).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				_, err = secretsDao.Create().SetObject(privatev1.Secret_builder{
					Id:   "override-secret-id",
					Type: privatev1.SecretType_SECRET_TYPE_PULL_SECRET,
					Metadata: privatev1.Metadata_builder{
						Name:   "override-secret-name",
						Tenant: testTenant,
					}.Build(),
				}.Build()).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			})

			It("Creates a cluster with pull_secret_secret reference by id", func() {
				response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
							NodeSets: map[string]*privatev1.ClusterNodeSet{
								"compute": privatev1.ClusterNodeSet_builder{Size: proto.Int32(3), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id", Shared: true}.Build()}.Build(),
								"gpu":     privatev1.ClusterNodeSet_builder{Size: proto.Int32(1), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-gpu-bmit-id", Shared: true}.Build()}.Build(),
							},
							PullSecretSecret: privatev1.SecretLocalReference_builder{Id: "my-secret-id"}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetObject().GetSpec().GetPullSecretSecret()).ToNot(BeNil())
				Expect(response.GetObject().GetSpec().GetPullSecretSecret().GetId()).To(Equal("my-secret-id"))
				Expect(response.GetObject().GetSpec().GetPullSecretSecret().GetName()).To(Equal("my-secret-name"))
			})

			It("Creates a cluster with pull_secret_secret reference by name", func() {
				response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
							NodeSets: map[string]*privatev1.ClusterNodeSet{
								"compute": privatev1.ClusterNodeSet_builder{Size: proto.Int32(3), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id", Shared: true}.Build()}.Build(),
								"gpu":     privatev1.ClusterNodeSet_builder{Size: proto.Int32(1), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-gpu-bmit-id", Shared: true}.Build()}.Build(),
							},
							PullSecretSecret: privatev1.SecretLocalReference_builder{Name: "my-secret-name"}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetObject().GetSpec().GetPullSecretSecret().GetId()).To(Equal("my-secret-id"))
				Expect(response.GetObject().GetSpec().GetPullSecretSecret().GetName()).To(Equal("my-secret-name"))
			})

			It("Rejects create when pull_secret_secret references a non-existent secret", func() {
				_, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
							NodeSets: map[string]*privatev1.ClusterNodeSet{
								"compute": privatev1.ClusterNodeSet_builder{Size: proto.Int32(3), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id", Shared: true}.Build()}.Build(),
								"gpu":     privatev1.ClusterNodeSet_builder{Size: proto.Int32(1), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-gpu-bmit-id", Shared: true}.Build()}.Build(),
							},
							PullSecretSecret: privatev1.SecretLocalReference_builder{Id: "nonexistent-secret"}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(grpcstatus.Convert(err).Message()).To(ContainSubstring("pull_secret_secret 'nonexistent-secret' not found"))
			})

			It("Rejects a directly supplied shared pull_secret_secret reference", func() {
				_, err := secretsDao.Create().SetObject(privatev1.Secret_builder{
					Id:   "shared-secret-direct-id",
					Type: privatev1.SecretType_SECRET_TYPE_PULL_SECRET,
					Metadata: privatev1.Metadata_builder{
						Name:   "shared-secret-direct",
						Tenant: auth.SharedTenant,
					}.Build(),
				}.Build()).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				_, err = server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
							NodeSets: map[string]*privatev1.ClusterNodeSet{
								"compute": privatev1.ClusterNodeSet_builder{Size: proto.Int32(3), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id", Shared: true}.Build()}.Build(),
								"gpu":     privatev1.ClusterNodeSet_builder{Size: proto.Int32(1), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-gpu-bmit-id", Shared: true}.Build()}.Build(),
							},
							PullSecretSecret: privatev1.SecretLocalReference_builder{Id: "shared-secret-direct-id"}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{Hub: "my-hub-id"}.Build(),
					}.Build(),
				}.Build())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(grpcstatus.Convert(err).Message()).To(ContainSubstring("pull_secret_secret 'shared-secret-direct-id' not found"))
			})

			It("Updates a cluster with pull_secret_secret reference", func() {
				createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
							NodeSets: map[string]*privatev1.ClusterNodeSet{
								"compute": privatev1.ClusterNodeSet_builder{Size: proto.Int32(3), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id", Shared: true}.Build()}.Build(),
								"gpu":     privatev1.ClusterNodeSet_builder{Size: proto.Int32(1), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-gpu-bmit-id", Shared: true}.Build()}.Build(),
							},
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				updateMask, err := fieldmaskpb.New(createResponse.GetObject(), "spec.pull_secret_secret")
				Expect(err).ToNot(HaveOccurred())

				updateResponse, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: createResponse.GetObject().GetId(),
						Spec: privatev1.ClusterSpec_builder{
							PullSecretSecret: privatev1.SecretLocalReference_builder{Id: "my-secret-id"}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: updateMask,
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(updateResponse.GetObject().GetSpec().GetPullSecretSecret().GetId()).To(Equal("my-secret-id"))
				Expect(updateResponse.GetObject().GetSpec().GetPullSecretSecret().GetName()).To(Equal("my-secret-name"))
			})

			It("Applies pull_secret_secret from template defaults", func() {
				templatesDao, err := dao.NewGenericDAO[*privatev1.ClusterTemplate]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
				_, err = templatesDao.Create().
					SetObject(
						privatev1.ClusterTemplate_builder{
							Id: "template-with-secret-ref",
							Metadata: privatev1.Metadata_builder{
								Name:   "template-with-secret-ref-name",
								Tenant: testTenant,
							}.Build(),
							Title:       "Template with secret ref default",
							Description: "Template with pull_secret_secret in spec defaults",
							SpecDefaults: privatev1.ClusterTemplateSpecDefaults_builder{
								PullSecretSecret: privatev1.SecretLocalReference_builder{Id: "my-secret-id"}.Build(),
							}.Build(),
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: "template-with-secret-ref"}.Build(),
							NodeSets: map[string]*privatev1.ClusterNodeSet{
								"compute": privatev1.ClusterNodeSet_builder{Size: proto.Int32(3), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id", Shared: true}.Build()}.Build(),
							},
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetObject().GetSpec().GetPullSecretSecret()).ToNot(BeNil())
				Expect(response.GetObject().GetSpec().GetPullSecretSecret().GetId()).To(Equal("my-secret-id"))
			})

			It("inherits a canonical shared pull Secret when a tenant Secret has the same name", func() {
				_, err := secretsDao.Create().SetObject(privatev1.Secret_builder{
					Id:   "shared-pull-secret-id",
					Type: privatev1.SecretType_SECRET_TYPE_PULL_SECRET,
					Metadata: privatev1.Metadata_builder{
						Name:   "my-secret-name",
						Tenant: auth.SharedTenant,
					}.Build(),
				}.Build()).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				templatesDao, err := dao.NewGenericDAO[*privatev1.ClusterTemplate]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
				_, err = templatesDao.Create().SetObject(privatev1.ClusterTemplate_builder{
					Id: "shared-template-with-secret-ref",
					Metadata: privatev1.Metadata_builder{
						Name:   "shared-template-with-secret-ref",
						Tenant: auth.SharedTenant,
					}.Build(),
					Title:       "Shared template with pull Secret",
					Description: "Shared template with a canonical pull Secret reference",
					SpecDefaults: privatev1.ClusterTemplateSpecDefaults_builder{
						PullSecretSecret: privatev1.SecretLocalReference_builder{
							Id:   "shared-pull-secret-id",
							Name: "my-secret-name",
						}.Build(),
					}.Build(),
				}.Build()).Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{
								Id: "shared-template-with-secret-ref",
							}.Build(),
							NodeSets: map[string]*privatev1.ClusterNodeSet{
								"compute": privatev1.ClusterNodeSet_builder{Size: proto.Int32(3), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id", Shared: true}.Build()}.Build(),
							},
						}.Build(),
						Status: privatev1.ClusterStatus_builder{Hub: "my-hub-id"}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetObject().GetSpec().GetPullSecretSecret().GetId()).
					To(Equal("shared-pull-secret-id"))

				updateResponse, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: response.GetObject().GetId(),
						Spec: privatev1.ClusterSpec_builder{
							PullSecretSecret: privatev1.SecretLocalReference_builder{
								Id:   "shared-pull-secret-id",
								Name: "my-secret-name",
							}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.pull_secret_secret"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(updateResponse.GetObject().GetSpec().GetPullSecretSecret().GetId()).
					To(Equal("shared-pull-secret-id"))
			})

			It("User-provided pull_secret_secret overrides the template default", func() {
				templatesDao, err := dao.NewGenericDAO[*privatev1.ClusterTemplate]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
				_, err = templatesDao.Create().
					SetObject(
						privatev1.ClusterTemplate_builder{
							Id: "template-override-secret",
							Metadata: privatev1.Metadata_builder{
								Name:   "template-override-secret-name",
								Tenant: testTenant,
							}.Build(),
							Title:       "Template override test",
							Description: "Template with pull_secret_secret default",
							SpecDefaults: privatev1.ClusterTemplateSpecDefaults_builder{
								PullSecretSecret: privatev1.SecretLocalReference_builder{Id: "my-secret-id"}.Build(),
							}.Build(),
						}.Build(),
					).
					Do(ctx)
				Expect(err).ToNot(HaveOccurred())

				response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.New()[24:32]),
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: "template-override-secret"}.Build(),
							NodeSets: map[string]*privatev1.ClusterNodeSet{
								"compute": privatev1.ClusterNodeSet_builder{Size: proto.Int32(3), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id", Shared: true}.Build()}.Build(),
							},
							PullSecretSecret: privatev1.SecretLocalReference_builder{
								Id: "override-secret-id",
							}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "my-hub-id",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetObject().GetSpec().GetPullSecretSecret().GetId()).To(Equal("override-secret-id"))
				Expect(response.GetObject().GetSpec().GetPullSecretSecret().GetName()).To(Equal("override-secret-name"))
			})
		})

		Describe("Fabric interface resolution from BareMetalInstanceType", func() {
			BeforeEach(func() {
				seedCaaSTestBareMetalInstanceType(ctx, "bmit-fabric-id", "bmit-fabric-name", auth.SharedTenant,
					[]*privatev1.BareMetalNetworkPortSpec{
						privatev1.BareMetalNetworkPortSpec_builder{Name: "mgmt-0", Role: "management"}.Build(),
						privatev1.BareMetalNetworkPortSpec_builder{Name: "data-0", Role: "fabric"}.Build(),
						privatev1.BareMetalNetworkPortSpec_builder{Name: "data-1", Role: "fabric"}.Build(),
					})
				seedCaaSTestBareMetalInstanceType(ctx, "bmit-no-fabric-id", "bmit-no-fabric-name", auth.SharedTenant,
					[]*privatev1.BareMetalNetworkPortSpec{
						privatev1.BareMetalNetworkPortSpec_builder{Name: "mgmt-0", Role: "management"}.Build(),
					})
			})

			It("Loads fabric hardware by ID without revalidating the reference name", func() {
				ref := privatev1.BareMetalInstanceTypeReference_builder{
					Id: "bmit-fabric-id", Name: "bmit-no-fabric-name", Shared: true,
				}.Build()
				cluster := newCaaSNodeSetCreateRequest("fabric-by-id", map[string]*privatev1.ClusterNodeSet{
					"compute": privatev1.ClusterNodeSet_builder{Size: proto.Int32(3), BaremetalInstanceType: ref}.Build(),
				}, nil).GetObject()

				err := server.resolveFabricInterfaces(ctx, cluster)
				Expect(err).ToNot(HaveOccurred())
				Expect(cluster.GetSpec().GetNodeSets()["compute"].GetFabricInterface()).To(Equal("data-0"))
				Expect(ref.GetName()).To(Equal("bmit-no-fabric-name"))
			})

			It("Does not resolve a name-only fabric hardware reference", func() {
				cluster := newCaaSNodeSetCreateRequest("fabric-name-only", map[string]*privatev1.ClusterNodeSet{
					"compute": privatev1.ClusterNodeSet_builder{
						Size: proto.Int32(3),
						BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{
							Name: "bmit-fabric-name", Shared: true,
						}.Build(),
					}.Build(),
				}, nil).GetObject()

				err := server.resolveFabricInterfaces(ctx, cluster)
				Expect(err).To(HaveOccurred())
				Expect(cluster.GetSpec().GetNodeSets()["compute"].GetFabricInterface()).To(BeEmpty())
			})

			It("Populates fabric_interface from the first fabric port", func() {
				response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{Name: "fabric-happy"}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
							NodeSets: map[string]*privatev1.ClusterNodeSet{
								"compute": privatev1.ClusterNodeSet_builder{
									BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "bmit-fabric-id", Shared: true}.
										Build(),
									Size: proto.Int32(3),
								}.Build(),
							},
							NetworkAttachment: privatev1.ClusterNetworkAttachment_builder{
								Subnet:         privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
								SecurityGroups: []*privatev1.SecurityGroupLocalReference{privatev1.SecurityGroupLocalReference_builder{Id: "default-sg"}.Build()},
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
				nodeSet := response.GetObject().GetSpec().GetNodeSets()["compute"]
				Expect(nodeSet).ToNot(BeNil())
				// The first port with role=fabric is "data-0"
				Expect(nodeSet.GetFabricInterface()).To(Equal("data-0"))
			})

			It("Returns FailedPrecondition when BMIT has no fabric port", func() {
				_, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{Name: "fabric-missing"}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
							NodeSets: map[string]*privatev1.ClusterNodeSet{
								"compute": privatev1.ClusterNodeSet_builder{
									BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "bmit-no-fabric-id", Shared: true}.
										Build(),
									Size: proto.Int32(3),
								}.Build(),
							},
							NetworkAttachment: privatev1.ClusterNetworkAttachment_builder{
								Subnet:         privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
								SecurityGroups: []*privatev1.SecurityGroupLocalReference{privatev1.SecurityGroupLocalReference_builder{Id: "default-sg"}.Build()},
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				st, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(st.Code()).To(Equal(grpccodes.FailedPrecondition))
				Expect(st.Message()).To(ContainSubstring("no network port with role 'fabric'"))
			})

			It("Skips fabric resolution when cluster has no network attachment", func() {
				response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{Name: "fabric-no-net"}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
							NodeSets: map[string]*privatev1.ClusterNodeSet{
								"compute": privatev1.ClusterNodeSet_builder{
									BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "bmit-no-fabric-id", Shared: true}.
										Build(),
									Size: proto.Int32(3),
								}.Build(),
							},
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
				nodeSet := response.GetObject().GetSpec().GetNodeSets()["compute"]
				Expect(nodeSet).ToNot(BeNil())
				// Without network_attachment, fabric_interface should be empty
				Expect(nodeSet.GetFabricInterface()).To(BeEmpty())
			})

			It("Selects the first fabric port when multiple exist", func() {
				response, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{
					Object: privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{Name: "fabric-multi"}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
							NodeSets: map[string]*privatev1.ClusterNodeSet{
								"compute": privatev1.ClusterNodeSet_builder{
									BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "bmit-fabric-id", Shared: true}.
										Build(),
									Size: proto.Int32(3),
								}.Build(),
							},
							NetworkAttachment: privatev1.ClusterNetworkAttachment_builder{
								Subnet:         privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(),
								SecurityGroups: []*privatev1.SecurityGroupLocalReference{privatev1.SecurityGroupLocalReference_builder{Id: "default-sg"}.Build()},
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
				nodeSet := response.GetObject().GetSpec().GetNodeSets()["compute"]
				Expect(nodeSet).ToNot(BeNil())
				// bmit-fabric-id has mgmt-0 (management), data-0 (fabric), data-1 (fabric)
				// The first fabric port should be selected: "data-0"
				Expect(nodeSet.GetFabricInterface()).To(Equal("data-0"))
			})

		})

		Describe("controller-reported endpoint validation", func() {
			type endpointField struct {
				path string
				set  func(*privatev1.ClusterStatus, string)
				get  func(*privatev1.ClusterStatus) string
			}

			fields := []endpointField{
				{
					path: "status.api_endpoint",
					set:  func(status *privatev1.ClusterStatus, value string) { status.SetApiEndpoint(value) },
					get:  func(status *privatev1.ClusterStatus) string { return status.GetApiEndpoint() },
				},
				{
					path: "status.ingress_endpoint",
					set:  func(status *privatev1.ClusterStatus, value string) { status.SetIngressEndpoint(value) },
					get:  func(status *privatev1.ClusterStatus) string { return status.GetIngressEndpoint() },
				},
			}

			invalidValues := []struct {
				name  string
				value string
			}{
				{name: "IPv6", value: "2001:db8::1"},
				{name: "IPv4-mapped IPv6", value: "::ffff:192.0.2.1"},
				{name: "malformed address", value: "not-an-ip"},
				{name: "non-canonical address", value: "192.000.2.1"},
				{name: "CIDR suffix", value: "192.0.2.1/32"},
			}

			newCluster := func(name string, status *privatev1.ClusterStatus) *privatev1.Cluster {
				return privatev1.Cluster_builder{
					Id:       uuid.New(),
					Metadata: privatev1.Metadata_builder{Name: name}.Build(),
					Spec: privatev1.ClusterSpec_builder{
						Template: privatev1.ClusterTemplateReference_builder{Id: "my-template-id"}.Build(),
					}.Build(),
					Status: status,
				}.Build()
			}

			It("accepts empty and canonical IPv4 endpoints on Create and Update", func() {
				status := privatev1.ClusterStatus_builder{
					ApiEndpoint:     "192.0.2.10",
					IngressEndpoint: "192.0.2.11",
				}.Build()
				object := newCluster("endpoint-valid-"+uuid.New()[24:32], status)
				createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{Object: object}.Build())
				Expect(err).ToNot(HaveOccurred())
				id := createResponse.GetObject().GetId()
				DeferCleanup(func() {
					_, deleteErr := server.Delete(ctx, privatev1.ClustersDeleteRequest_builder{Id: id}.Build())
					Expect(deleteErr).ToNot(HaveOccurred())
				})

				for _, field := range fields {
					By("accepting canonical IPv4 for " + field.path)
					updateStatus := privatev1.ClusterStatus_builder{}.Build()
					field.set(updateStatus, "198.51.100.9")
					updateResponse, updateErr := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
						Object:     privatev1.Cluster_builder{Id: id, Status: updateStatus}.Build(),
						UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{field.path}},
					}.Build())
					Expect(updateErr).ToNot(HaveOccurred())
					Expect(field.get(updateResponse.GetObject().GetStatus())).To(Equal("198.51.100.9"))
				}

				// An empty value is valid while the provisioning controller has not discovered a VIP.
				emptyCluster := newCluster("endpoint-empty-"+uuid.New()[24:32], privatev1.ClusterStatus_builder{}.Build())
				emptyResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{Object: emptyCluster}.Build())
				Expect(err).ToNot(HaveOccurred())
				DeferCleanup(func() {
					_, deleteErr := server.Delete(ctx, privatev1.ClustersDeleteRequest_builder{Id: emptyResponse.GetObject().GetId()}.Build())
					Expect(deleteErr).ToNot(HaveOccurred())
				})
			})

			It("rejects invalid endpoints before Create persistence", func() {
				for _, field := range fields {
					for _, invalid := range invalidValues {
						By("rejecting " + invalid.name + " for " + field.path)
						status := privatev1.ClusterStatus_builder{}.Build()
						field.set(status, invalid.value)
						object := newCluster("endpoint-invalid-"+uuid.New()[24:32], status)
						id := object.GetId()
						_, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{Object: object}.Build())
						if err == nil {
							_, deleteErr := server.Delete(ctx, privatev1.ClustersDeleteRequest_builder{Id: id}.Build())
							Expect(deleteErr).ToNot(HaveOccurred())
							Fail("Create accepted " + invalid.name + " for " + field.path)
						}

						errStatus, ok := grpcstatus.FromError(err)
						Expect(ok).To(BeTrue())
						Expect(errStatus.Code()).To(Equal(grpccodes.InvalidArgument))
						Expect(err.Error()).To(ContainSubstring(field.path))
						Expect(err.Error()).To(ContainSubstring("canonical IPv4"))

						_, getErr := server.Get(ctx, privatev1.ClustersGetRequest_builder{Id: id}.Build())
						getStatus, getOK := grpcstatus.FromError(getErr)
						Expect(getOK).To(BeTrue())
						Expect(getStatus.Code()).To(Equal(grpccodes.NotFound))
					}
				}
			})

			It("rejects invalid endpoint updates without changing stored status", func() {
				object := newCluster("endpoint-update-"+uuid.New()[24:32], privatev1.ClusterStatus_builder{
					ApiEndpoint:     "192.0.2.10",
					IngressEndpoint: "192.0.2.11",
				}.Build())
				createResponse, err := server.Create(ctx, privatev1.ClustersCreateRequest_builder{Object: object}.Build())
				Expect(err).ToNot(HaveOccurred())
				id := createResponse.GetObject().GetId()
				DeferCleanup(func() {
					_, deleteErr := server.Delete(ctx, privatev1.ClustersDeleteRequest_builder{Id: id}.Build())
					Expect(deleteErr).ToNot(HaveOccurred())
				})

				By("rejecting an invalid endpoint in a full-object update")
				fullObjectResponse, err := server.Get(ctx, privatev1.ClustersGetRequest_builder{Id: id}.Build())
				Expect(err).ToNot(HaveOccurred())
				fullObject := fullObjectResponse.GetObject()
				fullObject.GetStatus().SetApiEndpoint("2001:db8::9")
				_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{Object: fullObject}.Build())
				Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
				Expect(err.Error()).To(ContainSubstring("status.api_endpoint"))
				Expect(err.Error()).To(ContainSubstring("canonical IPv4"))
				storedResponse, err := server.Get(ctx, privatev1.ClustersGetRequest_builder{Id: id}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(storedResponse.GetObject().GetStatus().GetApiEndpoint()).To(Equal("192.0.2.10"))

				for _, field := range fields {
					for _, invalid := range invalidValues {
						By("rejecting " + invalid.name + " for " + field.path)
						updateStatus := privatev1.ClusterStatus_builder{}.Build()
						field.set(updateStatus, invalid.value)
						_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
							Object:     privatev1.Cluster_builder{Id: id, Status: updateStatus}.Build(),
							UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{field.path}},
						}.Build())
						errStatus, ok := grpcstatus.FromError(err)
						Expect(ok).To(BeTrue())
						Expect(errStatus.Code()).To(Equal(grpccodes.InvalidArgument))
						Expect(err.Error()).To(ContainSubstring(field.path))
						Expect(err.Error()).To(ContainSubstring("canonical IPv4"))

						getResponse, getErr := server.Get(ctx, privatev1.ClustersGetRequest_builder{Id: id}.Build())
						Expect(getErr).ToNot(HaveOccurred())
						Expect(field.get(getResponse.GetObject().GetStatus())).To(
							Equal(map[string]string{
								"status.api_endpoint":     "192.0.2.10",
								"status.ingress_endpoint": "192.0.2.11",
							}[field.path]))
					}

					By("accepting an empty endpoint for " + field.path)
					updateStatus := privatev1.ClusterStatus_builder{}.Build()
					field.set(updateStatus, "")
					_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
						Object:     privatev1.Cluster_builder{Id: id, Status: updateStatus}.Build(),
						UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{field.path}},
					}.Build())
					Expect(err).ToNot(HaveOccurred())
				}

			})
		})
	})
})
