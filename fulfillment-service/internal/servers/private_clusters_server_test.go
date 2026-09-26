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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

func seedClusterVersion(ctx context.Context, cv *privatev1.ClusterVersion) {
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
	operatorDao, err := dao.NewGenericDAO[*privatev1.AddOnOperator]().
		SetLogger(logger).
		SetTenancyLogic(tenancy).
		Build()
	Expect(err).ToNot(HaveOccurred())
	_, err = operatorDao.Create().SetObject(privatev1.AddOnOperator_builder{
		Id: id,
		Metadata: privatev1.Metadata_builder{
			Name:   name,
			Tenant: auth.SharedTenant,
		}.Build(),
		Title:     name,
		Published: proto.Bool(published),
	}.Build()).Do(ctx)
	Expect(err).ToNot(HaveOccurred())
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
// fields that previously depended on template-supplied hardware. It does not alter
// requests which already specify NodeSets or which have no direct Template.
type clusterCreationFixture struct{ *PrivateClustersServer }

func (s *clusterCreationFixture) Create(ctx context.Context, request *privatev1.ClustersCreateRequest) (*privatev1.ClustersCreateResponse, error) {
	if spec := request.GetObject().GetSpec(); spec != nil && spec.GetTemplate() != nil &&
		spec.GetNodeSets() == nil && spec.GetTemplate().GetId() != "no-bmit-template-id" {
		request = proto.Clone(request).(*privatev1.ClustersCreateRequest)
		spec = request.GetObject().GetSpec()
		nodeSets := map[string]*privatev1.ClusterNodeSet{
			"compute": privatev1.ClusterNodeSet_builder{
				Size: proto.Int32(3), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id"}.Build(),
			}.Build(),
		}
		if spec.GetTemplate().GetId() == "my-template-id" || spec.GetTemplate().GetName() == "my-template-name" {
			nodeSets["gpu"] = privatev1.ClusterNodeSet_builder{
				Size: proto.Int32(1), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-gpu-bmit-id"}.Build(),
			}.Build()
		}
		spec.SetNodeSets(nodeSets)
	}
	return s.PrivateClustersServer.Create(ctx, request)
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

			// Create a virtual network and subnets for network attachment tests:
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
				Expect(proto.Equal(storedOperators[i], operator)).To(BeTrue())
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

		It("resolves a name-only CaaS NodeSet reference from shared without caller scope", func() {
			response, err := server.Create(ctx, newCaaSNodeSetCreateRequest("shared-name-only", map[string]*privatev1.ClusterNodeSet{
				"workers": privatev1.ClusterNodeSet_builder{
					Size: proto.Int32(2),
					BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{
						Name: "acme-bmit-name", Shared: false,
					}.Build(),
				}.Build(),
			}, nil))
			Expect(err).ToNot(HaveOccurred())
			resolved := response.GetObject().GetSpec().GetNodeSets()["workers"].GetBaremetalInstanceType()
			Expect(resolved.GetId()).To(Equal("acme-bmit-id"))
			Expect(resolved.GetName()).To(Equal("acme-bmit-name"))
			Expect(resolved.GetShared()).To(BeTrue())
		})

		DescribeTable("rejects tenant-only BMIT references in CaaS NodeSets", func(ref *privatev1.BareMetalInstanceTypeReference) {
			seedCaaSTestBareMetalInstanceType(ctx, "tenant-only-caas-id", "tenant-only-caas", testTenant, nil)
			_, err := server.Create(ctx, newCaaSNodeSetCreateRequest("tenant-only-rejected", map[string]*privatev1.ClusterNodeSet{
				"workers": privatev1.ClusterNodeSet_builder{Size: proto.Int32(2), BaremetalInstanceType: ref}.Build(),
			}, nil))
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
		},
			Entry("by ID", privatev1.BareMetalInstanceTypeReference_builder{Id: "tenant-only-caas-id", Shared: false}.Build()),
			Entry("by name", privatev1.BareMetalInstanceTypeReference_builder{Name: "tenant-only-caas", Shared: false}.Build()),
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
						Name: "fabric-worker", Shared: false,
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
								Name: "acme-gpu-name", Shared: false,
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
					BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id"}.Build(),
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

		It("rejects a tenant-only BMIT when adding a NodeSet on update", func() {
			seedCaaSTestBareMetalInstanceType(ctx, "tenant-update-only-id", "tenant-update-only", testTenant, nil)
			created, err := server.Create(ctx, newCaaSNodeSetCreateRequest("tenant-update", map[string]*privatev1.ClusterNodeSet{
				"compute": privatev1.ClusterNodeSet_builder{
					Size: proto.Int32(2),
					BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{
						Id: "acme-bmit-id", Shared: true,
					}.Build(),
				}.Build(),
			}, nil))
			Expect(err).ToNot(HaveOccurred())
			_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
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
								Id: "tenant-update-only-id", Shared: false,
							}.Build(),
						}.Build(),
					}}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.node_sets"}},
			}.Build())
			Expect(grpcstatus.Code(err)).To(Equal(grpccodes.InvalidArgument))
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
								BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Name: "does-not-exist"}.Build(),
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
								BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id"}.Build(),
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
			_, err = server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
				Object: privatev1.Cluster_builder{
					Id: object.GetId(),
					Spec: privatev1.ClusterSpec_builder{
						NodeSets: map[string]*privatev1.ClusterNodeSet{
							"compute": privatev1.ClusterNodeSet_builder{
								BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id"}.Build(),
								Size:                  proto.Int32(3),
							}.Build(),
							"gpu": privatev1.ClusterNodeSet_builder{
								BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-gpu-bmit-id"}.Build(),
								Size:                  proto.Int32(1),
							}.Build(),
							"storage": privatev1.ClusterNodeSet_builder{
								BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id"}.Build(),
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
								BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id"}.Build(),
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
								BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-gpu-bmit-id"}.Build(), // Changed from acme-bmit-id
								Size:                  proto.Int32(3),
							}.Build(),
							"gpu": privatev1.ClusterNodeSet_builder{
								BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-gpu-bmit-id"}.Build(),
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
								BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id"}.Build(),
								Size:                  proto.Int32(5),
							}.Build(),
							"gpu": privatev1.ClusterNodeSet_builder{
								BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-gpu-bmit-id"}.Build(),
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
							State: state,
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"status.state"},
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
			})

			It("Allows changing security_groups with same subnet", func() {
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
				Expect(err).ToNot(HaveOccurred())
				updated := updateResponse.GetObject()
				Expect(updated.GetSpec().GetNetworkAttachment().GetSubnet().GetId()).To(Equal("subnet-1"))
				Expect(updated.GetSpec().GetNetworkAttachment().GetSecurityGroups()).To(HaveLen(2))
				Expect(updated.GetSpec().GetNetworkAttachment().GetSecurityGroups()[0].GetId()).To(Equal("default-sg"))
				Expect(updated.GetSpec().GetNetworkAttachment().GetSecurityGroups()[1].GetId()).To(Equal("sg-2"))
			})

			It("Allows updating security_groups via sub-field mask", func() {
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
				Expect(err).ToNot(HaveOccurred())
				updated := updateResponse.GetObject()
				Expect(updated.GetSpec().GetNetworkAttachment().GetSecurityGroups()).To(HaveLen(2))
				Expect(updated.GetSpec().GetNetworkAttachment().GetSecurityGroups()[0].GetId()).To(Equal("sg-2"))
				Expect(updated.GetSpec().GetNetworkAttachment().GetSecurityGroups()[1].GetId()).To(Equal("sg-3"))
			})

			It("Passes through when mask does not include network_attachment", func() {
				object := createClusterWithNetworkAttachment(privatev1.SubnetLocalReference_builder{Id: "subnet-1"}.Build(), []*privatev1.SecurityGroupLocalReference{privatev1.SecurityGroupLocalReference_builder{Id: "default-sg"}.Build()})

				updateResponse, err := server.Update(ctx, privatev1.ClustersUpdateRequest_builder{
					Object: privatev1.Cluster_builder{
						Id: object.GetId(),
						Status: privatev1.ClusterStatus_builder{
							State: privatev1.ClusterState_CLUSTER_STATE_READY,
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"status.state"},
					},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				updated := updateResponse.GetObject()
				Expect(updated.GetSpec().GetNetworkAttachment().GetSubnet().GetId()).To(Equal("subnet-1"))
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
								"compute": privatev1.ClusterCatalogNodeSet_builder{Size: 3, BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Name: "acme-bmit-name"}.Build()}.Build(),
								"gpu":     privatev1.ClusterCatalogNodeSet_builder{Size: 1, BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Name: "acme-gpu-name"}.Build()}.Build(),
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
								"worker": privatev1.ClusterNodeSet_builder{Size: proto.Int32(2), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id"}.Build()}.Build(),
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
							NodeSets:    map[string]*privatev1.ClusterNodeSet{"worker": privatev1.ClusterNodeSet_builder{Size: proto.Int32(2), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id"}.Build()}.Build()},
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
							NodeSets:    map[string]*privatev1.ClusterNodeSet{"worker": privatev1.ClusterNodeSet_builder{Size: proto.Int32(2), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id"}.Build()}.Build()},
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
							NodeSets:    map[string]*privatev1.ClusterNodeSet{"worker": privatev1.ClusterNodeSet_builder{Size: proto.Int32(2), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id"}.Build()}.Build()},
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
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
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
							NodeSets:    map[string]*privatev1.ClusterNodeSet{"compute": privatev1.ClusterNodeSet_builder{Size: proto.Int32(3), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id"}.Build()}.Build()},
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
								"compute": privatev1.ClusterNodeSet_builder{Size: proto.Int32(3), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id"}.Build()}.Build(),
								"gpu":     privatev1.ClusterNodeSet_builder{Size: proto.Int32(1), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-gpu-bmit-id"}.Build()}.Build(),
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
								"compute": privatev1.ClusterNodeSet_builder{Size: proto.Int32(3), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id"}.Build()}.Build(),
								"gpu":     privatev1.ClusterNodeSet_builder{Size: proto.Int32(1), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-gpu-bmit-id"}.Build()}.Build(),
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
								"compute": privatev1.ClusterNodeSet_builder{Size: proto.Int32(3), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id"}.Build()}.Build(),
								"gpu":     privatev1.ClusterNodeSet_builder{Size: proto.Int32(1), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-gpu-bmit-id"}.Build()}.Build(),
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
								"compute": privatev1.ClusterNodeSet_builder{Size: proto.Int32(3), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id"}.Build()}.Build(),
								"gpu":     privatev1.ClusterNodeSet_builder{Size: proto.Int32(1), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-gpu-bmit-id"}.Build()}.Build(),
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
								"compute": privatev1.ClusterNodeSet_builder{Size: proto.Int32(3), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id"}.Build()}.Build(),
								"gpu":     privatev1.ClusterNodeSet_builder{Size: proto.Int32(1), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-gpu-bmit-id"}.Build()}.Build(),
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
								"compute": privatev1.ClusterNodeSet_builder{Size: proto.Int32(3), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id"}.Build()}.Build(),
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
								"compute": privatev1.ClusterNodeSet_builder{Size: proto.Int32(3), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id"}.Build()}.Build(),
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
								"compute": privatev1.ClusterNodeSet_builder{Size: proto.Int32(3), BaremetalInstanceType: privatev1.BareMetalInstanceTypeReference_builder{Id: "acme-bmit-id"}.Build()}.Build(),
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
	})
})
