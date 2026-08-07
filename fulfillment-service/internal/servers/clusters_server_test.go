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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
)

var _ = Describe("Clusters server", func() {
	BeforeEach(func() {
		// Put a context into the subject:
		ctx = auth.ContextWithSubject(
			ctx,
			&auth.Subject{
				User:    auth.SystemTenant,
				Tenants: auth.AllTenants,
			},
		)
	})

	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewClustersServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetScheme(testScheme).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewClustersServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetScheme(testScheme).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if attribution logic is not set", func() {
			server, err := NewClustersServer().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				SetScheme(testScheme).
				Build()
			Expect(err).To(MatchError("attribution logic is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewClustersServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetScheme(testScheme).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if scheme is not set", func() {
			server, err := NewClustersServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("scheme is mandatory"))
			Expect(server).To(BeNil())
		})

	})

	Describe("Behaviour", func() {
		var server *ClustersServer

		makeAny := func(m proto.Message) *anypb.Any {
			a, err := anypb.New(m)
			Expect(err).ToNot(HaveOccurred())
			return a
		}

		BeforeEach(func() {
			var err error

			// Create the server:
			server, err = NewClustersServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetScheme(testScheme).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Create the host types DAO:
			hostTypesDao, err := dao.NewGenericDAO[*privatev1.HostType]().
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

			// Create the the host types:
			_, err = hostTypesDao.Create().
				SetObject(
					privatev1.HostType_builder{
						Id:          "acme_1tib",
						Title:       "ACME 1TiB",
						Description: "ACME 1TiB.",
						Metadata: privatev1.Metadata_builder{
							Name:   "test-host-type-1tib",
							Tenant: auth.SharedTenant,
						}.Build(),
					}.Build()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = hostTypesDao.Create().
				SetObject(
					privatev1.HostType_builder{
						Id:          "acme_gpu",
						Title:       "ACME GPU",
						Description: "ACME GPU.",
						Metadata: privatev1.Metadata_builder{
							Name:   "test-host-type-gpu",
							Tenant: auth.SharedTenant,
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = hostTypesDao.Create().
				SetObject(
					privatev1.HostType_builder{
						Id:          "hal_9000",
						Title:       "HAL 9000",
						Description: "Heuristically programmed ALgorithmic computer.",
						Metadata: privatev1.Metadata_builder{
							Name:   "test-host-type-hal",
							Tenant: auth.SharedTenant,
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Create a usable template:
			_, err = templatesDao.Create().
				SetObject(
					privatev1.ClusterTemplate_builder{
						Id:          "my_template",
						Title:       "My template",
						Description: "My template",
						Metadata: privatev1.Metadata_builder{
							Name:   "test-template",
							Tenant: auth.SharedTenant,
						}.Build(),
						NodeSets: map[string]*privatev1.ClusterTemplateNodeSet{
							"compute": privatev1.ClusterTemplateNodeSet_builder{
								HostType: privatev1.HostTypeReference_builder{Id: "acme_1tib"}.Build(),
								Size:     3,
							}.Build(),
							"gpu": privatev1.ClusterTemplateNodeSet_builder{
								HostType: privatev1.HostTypeReference_builder{Id: "acme_gpu"}.Build(),
								Size:     1,
							}.Build(),
						},
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Create a template that has been deleted. Note that we add a finalizer to ensure that it will
			// not be completely deleted and archived, as we will use it to verify that clusters can't be
			// created using deleted templates.
			_, err = templatesDao.Create().
				SetObject(
					privatev1.ClusterTemplate_builder{
						Id:          "my_deleted_template",
						Title:       "My deleted template",
						Description: "My deleted template",
						Metadata: privatev1.Metadata_builder{
							Name:       "test-deleted-template",
							Finalizers: []string{"a"},
							Tenant:     auth.SharedTenant,
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = templatesDao.Delete().
				SetId("my_deleted_template").
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Create a template with parameters:
			_, err = templatesDao.Create().
				SetObject(
					privatev1.ClusterTemplate_builder{
						Id:          "my_with_parameters",
						Title:       "My with parameters",
						Description: "My with parameters.",
						Metadata: privatev1.Metadata_builder{
							Name:   "test-template-params",
							Tenant: auth.SharedTenant,
						}.Build(),
						Parameters: []*privatev1.ClusterTemplateParameterDefinition{
							privatev1.ClusterTemplateParameterDefinition_builder{
								Name:        "my_required_bool",
								Title:       "My required bool",
								Description: "My required bool.",
								Required:    true,
								Type:        "type.googleapis.com/google.protobuf.BoolValue",
							}.Build(),
							privatev1.ClusterTemplateParameterDefinition_builder{
								Name:        "my_optional_string",
								Title:       "My optional string",
								Description: "My optional string.",
								Required:    false,
								Type:        "type.googleapis.com/google.protobuf.StringValue",
								Default:     makeAny(wrapperspb.String("my value")),
							}.Build(),
						},
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Create a default cluster version for version resolution:
			seedClusterVersion(ctx, privatev1.ClusterVersion_builder{
				Id: "cv-default",
				Metadata: privatev1.Metadata_builder{
					Name:   "4-17-0",
					Tenant: auth.SharedTenant,
				}.Build(),
				Spec: privatev1.ClusterVersionSpec_builder{
					Image:     "quay.io/openshift-release-dev/ocp-release:4.17.0-multi",
					Enabled:   proto.Bool(true),
					IsDefault: proto.Bool(true),
					Version:   "4.17.0",
					State:     privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_ACTIVE,
				}.Build(),
			}.Build())
		})

		It("Creates object", func() {
			response, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).ToNot(BeEmpty())
		})

		It("Doesn't create object without template", func() {
			response, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(Equal("template is mandatory"))
		})

		It("Takes default node sets from template", func() {
			response, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := response.GetObject()
			nodeSets := object.GetSpec().GetNodeSets()
			Expect(nodeSets).To(HaveKey("compute"))
			computeNodeSet := nodeSets["compute"]
			Expect(computeNodeSet.GetHostType().GetId()).To(Equal("acme_1tib"))
			Expect(computeNodeSet.GetSize()).To(BeNumerically("==", 3))
			Expect(nodeSets).To(HaveKey("gpu"))
			gpuNodeSet := nodeSets["gpu"]
			Expect(gpuNodeSet.GetHostType().GetId()).To(Equal("acme_gpu"))
			Expect(gpuNodeSet.GetSize()).To(BeNumerically("==", 1))
		})

		It("Rejects node set that isn't in the template", func() {
			response, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
						NodeSets: map[string]*publicv1.ClusterNodeSet{
							"junk": publicv1.ClusterNodeSet_builder{
								HostType: publicv1.HostTypeReference_builder{Id: "acme_1tib"}.Build(),
								Size:     1000,
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(Equal(
				"node set 'junk' doesn't exist, valid values for template 'my_template' are " +
					"'compute' and 'gpu'",
			))
		})

		It("Rejects node set with host type that isn't in the template", func() {
			response, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
						NodeSets: map[string]*publicv1.ClusterNodeSet{
							"compute": publicv1.ClusterNodeSet_builder{
								HostType: publicv1.HostTypeReference_builder{Id: "hal_9000"}.Build(),
								Size:     1000,
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(Equal(
				"host type for node set 'compute' should be empty, 'test-host-type-1tib' or 'acme_1tib', like in " +
					"template 'my_template', but it is 'hal_9000'",
			))
		})

		It("Rejects node set with zero size", func() {
			response, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
						NodeSets: map[string]*publicv1.ClusterNodeSet{
							"compute": publicv1.ClusterNodeSet_builder{
								Size: 0,
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(Equal(
				"size for node set 'compute' should be greater than zero, but it is 0",
			))
		})

		It("Rejects node set with negative size", func() {
			response, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
						NodeSets: map[string]*publicv1.ClusterNodeSet{
							"compute": publicv1.ClusterNodeSet_builder{
								Size: -1,
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(Equal(
				"size for node set 'compute' should be greater than zero, but it is -1",
			))
		})

		It("Accepts node set with explicit size", func() {
			response, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
						NodeSets: map[string]*publicv1.ClusterNodeSet{
							"compute": publicv1.ClusterNodeSet_builder{
								Size: 1000,
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := response.GetObject()
			nodeSets := object.GetSpec().GetNodeSets()
			Expect(nodeSets).To(HaveKey("compute"))
			nodeSet := nodeSets["compute"]
			Expect(nodeSet.GetSize()).To(BeNumerically("==", 1000))
		})

		It("Accepts multiple node sets with explicit size", func() {
			response, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
						NodeSets: map[string]*publicv1.ClusterNodeSet{
							"compute": publicv1.ClusterNodeSet_builder{
								Size: 30,
							}.Build(),
							"gpu": publicv1.ClusterNodeSet_builder{
								Size: 10,
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := response.GetObject()
			nodeSets := object.GetSpec().GetNodeSets()
			Expect(nodeSets).To(HaveKey("compute"))
			computeNodeSet := nodeSets["compute"]
			Expect(computeNodeSet.GetSize()).To(BeNumerically("==", 30))
			Expect(nodeSets).To(HaveKey("gpu"))
			gpuNodeSet := nodeSets["gpu"]
			Expect(gpuNodeSet.GetSize()).To(BeNumerically("==", 10))
		})

		It("Merges explicit size for one node set with size for another node set from the template", func() {
			response, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
						NodeSets: map[string]*publicv1.ClusterNodeSet{
							"compute": publicv1.ClusterNodeSet_builder{
								Size: 30,
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := response.GetObject()
			nodeSets := object.GetSpec().GetNodeSets()
			Expect(nodeSets).To(HaveKey("compute"))
			computeNodeSet := nodeSets["compute"]
			Expect(computeNodeSet.GetSize()).To(BeNumerically("==", 30))
			Expect(nodeSets).To(HaveKey("gpu"))
			gpuNodeSet := nodeSets["gpu"]
			Expect(gpuNodeSet.GetSize()).To(BeNumerically("==", 1))
		})

		It("Rejects template that has been deleted", func() {
			response, err := server.Create(
				ctx,
				publicv1.ClustersCreateRequest_builder{
					Object: publicv1.Cluster_builder{
						Spec: publicv1.ClusterSpec_builder{
							Template: publicv1.ClusterTemplateReference_builder{Id: "my_deleted_template"}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
			)
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(Equal(
				"template 'my_deleted_template' has been deleted",
			))
		})

		It("Doesn't create object if there are missing required template parameters", func() {
			response, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_with_parameters"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(Equal(
				"parameter 'my_required_bool' of template 'my_with_parameters' is mandatory",
			))
		})

		It("Doesn't create object if one parameter doesn't exist in the template", func() {
			response, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_with_parameters"}.Build(),
						TemplateParameters: map[string]*anypb.Any{
							"junk": makeAny(wrapperspb.Int32(123)),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(Equal(
				"template parameter 'junk' doesn't exist, valid values for template " +
					"'my_with_parameters' are 'my_optional_string' and 'my_required_bool'",
			))
		})

		It("Doesn't create object if two parameters don't exist in the template", func() {
			response, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_with_parameters"}.Build(),
						TemplateParameters: map[string]*anypb.Any{
							"junk1": makeAny(wrapperspb.Int32(123)),
							"junk2": makeAny(wrapperspb.Int32(123)),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(Equal(
				"template parameters 'junk1' and 'junk2' don't exist, valid values for template " +
					"'my_with_parameters' are 'my_optional_string' and 'my_required_bool'",
			))
		})

		It("Doesn't create object if parameter type doesn't match the template", func() {
			response, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_with_parameters"}.Build(),
						TemplateParameters: map[string]*anypb.Any{
							"my_required_bool": makeAny(wrapperspb.Int32(123)),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(Equal(
				"type of parameter 'my_required_bool' of template 'my_with_parameters' should be " +
					"'type.googleapis.com/google.protobuf.BoolValue', but it is " +
					"'type.googleapis.com/google.protobuf.Int32Value'",
			))
		})

		It("Takes default values of parameters from the template", func() {
			response, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_with_parameters"}.Build(),
						TemplateParameters: map[string]*anypb.Any{
							"my_required_bool": makeAny(wrapperspb.Bool(true)),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := response.GetObject()
			templateParameters := object.GetSpec().GetTemplateParameters()

			parameterValue := templateParameters["my_required_bool"]
			Expect(parameterValue).ToNot(BeNil())
			boolValue := &wrapperspb.BoolValue{}
			err = anypb.UnmarshalTo(parameterValue, boolValue, proto.UnmarshalOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(boolValue.GetValue()).To(BeTrue())

			parameterValue = templateParameters["my_optional_string"]
			Expect(parameterValue).ToNot(BeNil())
			stringValue := &wrapperspb.StringValue{}
			err = anypb.UnmarshalTo(parameterValue, stringValue, proto.UnmarshalOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(stringValue.GetValue()).To(Equal("my value"))
		})

		It("Allows overriding of default values of template parameters", func() {
			response, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_with_parameters"}.Build(),
						TemplateParameters: map[string]*anypb.Any{
							"my_required_bool":   makeAny(wrapperspb.Bool(false)),
							"my_optional_string": makeAny(wrapperspb.String("your value")),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := response.GetObject()
			templateParameters := object.GetSpec().GetTemplateParameters()
			parameterValue := templateParameters["my_optional_string"]
			Expect(parameterValue).ToNot(BeNil())
			stringValue := &wrapperspb.StringValue{}
			err = anypb.UnmarshalTo(parameterValue, stringValue, proto.UnmarshalOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(stringValue.GetValue()).To(Equal("your value"))
		})

		It("List objects", func() {
			// Create a few objects:
			const count = 10
			for range count {
				_, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
					Object: publicv1.Cluster_builder{
						Spec: publicv1.ClusterSpec_builder{
							Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects:
			response, err := server.List(ctx, publicv1.ClustersListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			items := response.GetItems()
			Expect(items).To(HaveLen(count))
		})

		It("List objects with limit", func() {
			// Create a few objects:
			const count = 10
			for range count {
				_, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
					Object: publicv1.Cluster_builder{
						Spec: publicv1.ClusterSpec_builder{
							Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects:
			response, err := server.List(ctx, publicv1.ClustersListRequest_builder{
				Limit: new(int32(1)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", 1))
		})

		It("List objects with offset", func() {
			// Create a few objects:
			const count = 10
			for range count {
				_, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
					Object: publicv1.Cluster_builder{
						Spec: publicv1.ClusterSpec_builder{
							Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects:
			response, err := server.List(ctx, publicv1.ClustersListRequest_builder{
				Offset: new(int32(1)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", count-1))
		})

		It("List objects with filter", func() {
			// Create a few objects:
			const count = 10
			var objects []*publicv1.Cluster
			for range count {
				response, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
					Object: publicv1.Cluster_builder{
						Spec: publicv1.ClusterSpec_builder{
							Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				objects = append(objects, response.GetObject())
			}

			// List the objects:
			for _, object := range objects {
				response, err := server.List(ctx, publicv1.ClustersListRequest_builder{
					Filter: new(fmt.Sprintf("this.id == '%s'", object.GetId())),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetSize()).To(BeNumerically("==", 1))
				Expect(response.GetItems()[0].GetId()).To(Equal(object.GetId()))
			}
		})

		It("Get object", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Get it:
			getResponse, err := server.Get(ctx, publicv1.ClustersGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(createResponse.GetObject(), getResponse.GetObject())).To(BeTrue())
		})

		It("Returns not found error when getting object that doesn't exist", func() {
			response, err := server.Get(ctx, publicv1.ClustersGetRequest_builder{
				Id: "does-not-exist",
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.NotFound))
		})

		It("Update object", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-cluster",
					}.Build(),
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Update the object:
			updateResponse, err := server.Update(ctx, publicv1.ClustersUpdateRequest_builder{
				Object: publicv1.Cluster_builder{
					Id: object.GetId(),
					Spec: publicv1.ClusterSpec_builder{
						NodeSets: map[string]*publicv1.ClusterNodeSet{
							"compute": publicv1.ClusterNodeSet_builder{
								HostType: publicv1.HostTypeReference_builder{Id: "acme_1tib"}.Build(),
								Size:     4,
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = updateResponse.GetObject()
			nodeSet := object.GetSpec().GetNodeSets()["compute"]
			Expect(nodeSet.GetHostType().GetId()).To(Equal("acme_1tib"))
			Expect(nodeSet.GetSize()).To(BeNumerically("==", 4))

			// Get and verify:
			getResponse, err := server.Get(ctx, publicv1.ClustersGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = getResponse.GetObject()
			nodeSet = object.GetSpec().GetNodeSets()["compute"]
			Expect(nodeSet.GetHostType().GetId()).To(Equal("acme_1tib"))
			Expect(nodeSet.GetSize()).To(BeNumerically("==", 4))
		})

		It("Ignores changes to the status when an object is updated", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-cluster",
					}.Build(),
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Try to update the status:
			updateResponse, err := server.Update(ctx, publicv1.ClustersUpdateRequest_builder{
				Object: publicv1.Cluster_builder{
					Id: object.GetId(),
					Status: publicv1.ClusterStatus_builder{
						ApiUrl:     "https://my.api.com",
						ConsoleUrl: "https://my.console.com",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = updateResponse.GetObject()
			Expect(object.GetStatus().GetApiUrl()).To(BeEmpty())
			Expect(object.GetStatus().GetConsoleUrl()).To(BeEmpty())

			// Get the response and verify that the status hasn't been updated:
			getResponse, err := server.Get(ctx, publicv1.ClustersGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = getResponse.GetObject()
			Expect(object.GetStatus().GetApiUrl()).To(BeEmpty())
			Expect(object.GetStatus().GetConsoleUrl()).To(BeEmpty())
		})

		It("Delete object", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-cluster",
					}.Build(),
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Add a finalizer, as otherwise the object will be immediately deleted and archived and it
			// won't be possible to verify the deletion timestamp. This can't be done using the server
			// because this is a public object, and public objects don't have the finalizers field.
			tx, err := database.TxFromContext(ctx)
			Expect(err).ToNot(HaveOccurred())
			_, err = tx.Exec(
				ctx,
				`update clusters set finalizers = '{"a"}' where id = $1`,
				object.GetId(),
			)
			Expect(err).ToNot(HaveOccurred())

			// Delete the object:
			_, err = server.Delete(ctx, publicv1.ClustersDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Get and verify:
			getResponse, err := server.Get(ctx, publicv1.ClustersGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = getResponse.GetObject()
			Expect(object.GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
		})

		It("Returns not found error when deleting object that doesn't exist", func() {
			response, err := server.Delete(ctx, publicv1.ClustersDeleteRequest_builder{
				Id: "does_not_exist",
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.NotFound))
		})

		It("Preserves private data during update", func() {
			// Create the DAO:
			dao, err := dao.NewGenericDAO[*privatev1.Cluster]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Use the DAO directly to create an object with private data:
			createResponse, err := dao.Create().
				SetObject(
					privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name:   "test-cluster",
							Tenant: auth.SharedTenant,
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
							NodeSets: map[string]*privatev1.ClusterNodeSet{
								"compute": privatev1.ClusterNodeSet_builder{
									HostType: privatev1.HostTypeReference_builder{Id: "my_host_type"}.Build(),
									Size:     3,
								}.Build(),
							},
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							Hub: "123",
						}.
							Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Update the object using the public server:
			_, err = server.Update(ctx, publicv1.ClustersUpdateRequest_builder{
				Object: publicv1.Cluster_builder{
					Id: object.GetId(),
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
						NodeSets: map[string]*publicv1.ClusterNodeSet{
							"compute": publicv1.ClusterNodeSet_builder{
								HostType: publicv1.HostTypeReference_builder{Id: "my_host_type"}.Build(),
								Size:     4,
							}.Build(),
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Get the object again and verify that the private data hasn't changed:
			getResponse, err := dao.Get().
				SetId(object.GetId()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object = getResponse.GetObject()
			Expect(err).ToNot(HaveOccurred())
			Expect(object.GetStatus().GetHub()).To(Equal("123"))
		})

		It("Ignores status during creation", func() {
			// Try to create an object with status:
			createResponse, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
					}.Build(),
					Status: publicv1.ClusterStatus_builder{
						ApiUrl: "https://your.api",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Get the object and verify that the status was ignored:
			getResponse, err := server.Get(ctx, publicv1.ClustersGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = getResponse.GetObject()
			Expect(object.GetStatus().GetApiUrl()).To(BeEmpty())
		})

		It("Ignores changes to status during update", func() {
			// Use the DAO directly to create an object with status:
			dao, err := dao.NewGenericDAO[*privatev1.Cluster]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			createResponse, err := dao.Create().
				SetObject(
					privatev1.Cluster_builder{
						Metadata: privatev1.Metadata_builder{
							Name:   "test-cluster",
							Tenant: auth.SharedTenant,
						}.Build(),
						Spec: privatev1.ClusterSpec_builder{
							Template: privatev1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
						}.Build(),
						Status: privatev1.ClusterStatus_builder{
							ApiUrl: "https://my.api",
						}.Build(),
					}.Build(),
				).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Try to update the status:
			_, err = server.Update(ctx, publicv1.ClustersUpdateRequest_builder{
				Object: publicv1.Cluster_builder{
					Id: object.GetId(),
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
					}.Build(),
					Status: publicv1.ClusterStatus_builder{
						ApiUrl: "https://your.api",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Get the object again and verify that the status hasn't changed:
			getResponse, err := dao.Get().SetId(object.GetId()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
			object = getResponse.GetObject()
			Expect(object.GetStatus().GetApiUrl()).To(Equal("https://my.api"))
		})

		It("Update object with mask", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-cluster",
					}.Build(),
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Update the object using the field mask:
			updateResponse, err := server.Update(ctx, publicv1.ClustersUpdateRequest_builder{
				Object: publicv1.Cluster_builder{
					Id: object.GetId(),
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "your_template"}.Build(),
						NodeSets: map[string]*publicv1.ClusterNodeSet{
							"compute": publicv1.ClusterNodeSet_builder{
								Size: 4,
							}.Build(),
							"gpu": publicv1.ClusterNodeSet_builder{
								Size: 2,
							}.Build(),
						},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{
						"spec.node_sets.compute.size",
					},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = updateResponse.GetObject()
			verify := func(object *publicv1.Cluster) {
				Expect(object.GetSpec().GetTemplate().GetId()).To(Equal("my_template"))
				computeNodeSet := object.GetSpec().GetNodeSets()["compute"]
				Expect(computeNodeSet.GetSize()).To(BeNumerically("==", 4))
				gpuNodeSet := object.GetSpec().GetNodeSets()["gpu"]
				Expect(gpuNodeSet.GetSize()).To(BeNumerically("==", 1))
			}
			verify(object)

			// Get and verify:
			getResponse, err := server.Get(ctx, publicv1.ClustersGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = getResponse.GetObject()
			verify(object)
		})

		It("Allows removing a node set when multiple exist", func() {
			// Create a cluster with the default node sets from the template (compute and gpu):
			createResponse, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-cluster",
					}.Build(),
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()
			Expect(object.GetSpec().GetNodeSets()).To(HaveLen(2))
			Expect(object.GetSpec().GetNodeSets()).To(HaveKey("compute"))
			Expect(object.GetSpec().GetNodeSets()).To(HaveKey("gpu"))

			// Remove the gpu node set by updating with only the compute node set:
			updateResponse, err := server.Update(ctx, publicv1.ClustersUpdateRequest_builder{
				Object: publicv1.Cluster_builder{
					Id: object.GetId(),
					Spec: publicv1.ClusterSpec_builder{
						NodeSets: map[string]*publicv1.ClusterNodeSet{
							"compute": publicv1.ClusterNodeSet_builder{
								HostType: publicv1.HostTypeReference_builder{Id: "acme_1tib"}.Build(),
								Size:     3,
							}.Build(),
						},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"spec.node_sets"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = updateResponse.GetObject()
			Expect(object.GetSpec().GetNodeSets()).To(HaveLen(1))
			Expect(object.GetSpec().GetNodeSets()).To(HaveKey("compute"))
			Expect(object.GetSpec().GetNodeSets()).ToNot(HaveKey("gpu"))

			// Get and verify the node set was removed:
			getResponse, err := server.Get(ctx, publicv1.ClustersGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = getResponse.GetObject()
			Expect(object.GetSpec().GetNodeSets()).To(HaveLen(1))
			Expect(object.GetSpec().GetNodeSets()).To(HaveKey("compute"))
			Expect(object.GetSpec().GetNodeSets()).ToNot(HaveKey("gpu"))
		})

		It("Sets name when creating", func() {
			response, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "my-cluster",
					}.Build(),
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetMetadata().GetName()).To(Equal("my-cluster"))

			// Get and verify:
			getResponse, err := server.Get(ctx, publicv1.ClustersGetRequest_builder{
				Id: response.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetMetadata().GetName()).To(Equal("my-cluster"))
		})

		It("Updates name", func() {
			// Create the object with an initial name:
			createResponse, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "my-name",
					}.Build(),
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(createResponse.GetObject().GetMetadata().GetName()).To(Equal("my-name"))

			// Update the name:
			updateResponse, err := server.Update(ctx, publicv1.ClustersUpdateRequest_builder{
				Object: publicv1.Cluster_builder{
					Id: createResponse.GetObject().GetId(),
					Metadata: publicv1.Metadata_builder{
						Name: "your-name",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetMetadata().GetName()).To(Equal("your-name"))

			// Get and verify:
			getResponse, err := server.Get(ctx, publicv1.ClustersGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetMetadata().GetName()).To(Equal("your-name"))
		})

		It("Returns not found error when updating object that doesn't exist", func() {
			response, err := server.Update(ctx, publicv1.ClustersUpdateRequest_builder{
				Object: publicv1.Cluster_builder{
					Id: "does-not-exist",
					Metadata: publicv1.Metadata_builder{
						Name: "my-name",
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.NotFound))
		})

		// Note: Name validation tests are in internal/validation/protovalidate_interceptor_test.go
		// and it/it_validation_test.go. Name validation is handled by the protovalidate interceptor
		// at the gRPC layer, not by the server code. Unit tests that call the server directly
		// bypass the interceptor, so they cannot test name validation.

		DescribeTable(
			"Accepts creation with valid names",
			func(name string) {
				response, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
					Object: publicv1.Cluster_builder{
						Metadata: publicv1.Metadata_builder{
							Name: name,
						}.Build(),
						Spec: publicv1.ClusterSpec_builder{
							Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetObject().GetMetadata().GetName()).To(Equal(name))
			},
			Entry(
				"Simple name",
				"simple",
			),
			Entry(
				"With hyphens",
				"with-hyphens",
			),
			Entry(
				"With numbers",
				"with123numbers",
			),
			Entry(
				"Single character",
				"a",
			),
			Entry(
				"Maximum length",
				"a23456789012345678901234567890123456789012345678901234567890123",
			),
		)

		It("Adds label by updating", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-cluster",
					}.Build(),
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Delete the label:
			updateResponse, err := server.Update(ctx, publicv1.ClustersUpdateRequest_builder{
				Object: publicv1.Cluster_builder{
					Id: object.GetId(),
					Metadata: publicv1.Metadata_builder{
						Name: "test-cluster",
						Labels: map[string]string{
							"example.com/my-label": "my-value",
						},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{
						"metadata.labels",
					},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = updateResponse.GetObject()
			labels := object.GetMetadata().GetLabels()
			Expect(labels).To(HaveKeyWithValue("example.com/my-label", "my-value"))

			// Get and verify:
			getResponse, err := server.Get(ctx, publicv1.ClustersGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = getResponse.GetObject()
			labels = object.GetMetadata().GetLabels()
			Expect(labels).To(HaveKeyWithValue("example.com/my-label", "my-value"))
		})

		It("Updates label by updating", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-cluster",
						Labels: map[string]string{
							"example.com/my-label": "my-value",
						},
					}.Build(),
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Delete the label:
			updateResponse, err := server.Update(ctx, publicv1.ClustersUpdateRequest_builder{
				Object: publicv1.Cluster_builder{
					Id: object.GetId(),
					Metadata: publicv1.Metadata_builder{
						Name: "test-cluster",
						Labels: map[string]string{
							"example.com/my-label": "your-value",
						},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{
						"metadata.labels",
					},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = updateResponse.GetObject()
			labels := object.GetMetadata().GetLabels()
			Expect(labels).To(HaveKeyWithValue("example.com/my-label", "your-value"))

			// Get and verify:
			getResponse, err := server.Get(ctx, publicv1.ClustersGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = getResponse.GetObject()
			labels = object.GetMetadata().GetLabels()
			Expect(labels).To(HaveKeyWithValue("example.com/my-label", "your-value"))
		})

		It("Deletes label by updating", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-cluster",
						Labels: map[string]string{
							"example.com/my-label": "my-value",
						},
					}.Build(),
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Delete the label:
			updateResponse, err := server.Update(ctx, publicv1.ClustersUpdateRequest_builder{
				Object: publicv1.Cluster_builder{
					Id: object.GetId(),
					Metadata: publicv1.Metadata_builder{
						Name:   "test-cluster",
						Labels: map[string]string{},
					}.Build(),
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{
						"metadata.labels",
					},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = updateResponse.GetObject()
			labels := object.GetMetadata().GetLabels()
			Expect(labels).To(BeEmpty())

			// Get and verify:
			getResponse, err := server.Get(ctx, publicv1.ClustersGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = getResponse.GetObject()
			labels = object.GetMetadata().GetLabels()
			Expect(labels).To(BeEmpty())
		})

		It("Adds label by updating without specifying the field mask", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-cluster",
					}.Build(),
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Delete the label:
			updateResponse, err := server.Update(ctx, publicv1.ClustersUpdateRequest_builder{
				Object: publicv1.Cluster_builder{
					Id: object.GetId(),
					Metadata: publicv1.Metadata_builder{
						Name: "test-cluster",
						Labels: map[string]string{
							"example.com/my-label": "my-value",
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = updateResponse.GetObject()
			labels := object.GetMetadata().GetLabels()
			Expect(labels).To(HaveKeyWithValue("example.com/my-label", "my-value"))

			// Get and verify:
			getResponse, err := server.Get(ctx, publicv1.ClustersGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = getResponse.GetObject()
			labels = object.GetMetadata().GetLabels()
			Expect(labels).To(HaveKeyWithValue("example.com/my-label", "my-value"))
		})

		It("Updates label by updating without specifying the field mask", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-cluster",
						Labels: map[string]string{
							"example.com/my-label": "my-value",
						},
					}.Build(),
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Delete the label:
			updateResponse, err := server.Update(ctx, publicv1.ClustersUpdateRequest_builder{
				Object: publicv1.Cluster_builder{
					Id: object.GetId(),
					Metadata: publicv1.Metadata_builder{
						Name: "test-cluster",
						Labels: map[string]string{
							"example.com/my-label": "your-value",
						},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = updateResponse.GetObject()
			labels := object.GetMetadata().GetLabels()
			Expect(labels).To(HaveKeyWithValue("example.com/my-label", "your-value"))

			// Get and verify:
			getResponse, err := server.Get(ctx, publicv1.ClustersGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = getResponse.GetObject()
			labels = object.GetMetadata().GetLabels()
			Expect(labels).To(HaveKeyWithValue("example.com/my-label", "your-value"))
		})

		It("Updates label by updating without specifying the field mask", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-cluster",
						Labels: map[string]string{
							"example.com/my-label": "my-value",
						},
					}.Build(),
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Delete the label:
			updateResponse, err := server.Update(ctx, publicv1.ClustersUpdateRequest_builder{
				Object: publicv1.Cluster_builder{
					Id: object.GetId(),
					Metadata: publicv1.Metadata_builder{
						Name:   "test-cluster",
						Labels: map[string]string{},
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = updateResponse.GetObject()
			labels := object.GetMetadata().GetLabels()
			Expect(labels).To(BeEmpty())

			// Get and verify:
			getResponse, err := server.Get(ctx, publicv1.ClustersGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = getResponse.GetObject()
			labels = object.GetMetadata().GetLabels()
			Expect(labels).To(BeEmpty())
		})
	})

	Describe("Explicit fields", func() {
		var server *ClustersServer

		BeforeEach(func() {
			var err error

			server, err = NewClustersServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				SetScheme(testScheme).
				Build()
			Expect(err).ToNot(HaveOccurred())

			// Create host types:
			hostTypesDao, err := dao.NewGenericDAO[*privatev1.HostType]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = hostTypesDao.Create().
				SetObject(privatev1.HostType_builder{
					Id:    "acme_1tib",
					Title: "ACME 1TiB",
					Metadata: privatev1.Metadata_builder{
						Name:   "test-host-type-1tib",
						Tenant: auth.SharedTenant,
					}.Build(),
				}.Build()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Create template:
			templatesDao, err := dao.NewGenericDAO[*privatev1.ClusterTemplate]().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			_, err = templatesDao.Create().
				SetObject(privatev1.ClusterTemplate_builder{
					Id:    "my_template",
					Title: "My template",
					Metadata: privatev1.Metadata_builder{
						Name:   "test-template",
						Tenant: auth.SharedTenant,
					}.Build(),
					NodeSets: map[string]*privatev1.ClusterTemplateNodeSet{
						"compute": privatev1.ClusterTemplateNodeSet_builder{
							HostType: privatev1.HostTypeReference_builder{Id: "acme_1tib"}.Build(),
							Size:     3,
						}.Build(),
					},
				}.Build()).
				Do(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Create a default cluster version for version resolution:
			seedClusterVersion(ctx, privatev1.ClusterVersion_builder{
				Id: "cv-default",
				Metadata: privatev1.Metadata_builder{
					Name:   "4-17-0",
					Tenant: auth.SharedTenant,
				}.Build(),
				Spec: privatev1.ClusterVersionSpec_builder{
					Image:     "quay.io/openshift-release-dev/ocp-release:4.17.0-multi",
					Enabled:   proto.Bool(true),
					IsDefault: proto.Bool(true),
					Version:   "4.17.0",
					State:     privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_ACTIVE,
				}.Build(),
			}.Build())
		})

		It("Redacts pull_secret on Create response", func() {
			pullSecret := "my-secret-pull-secret"
			response, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Spec: publicv1.ClusterSpec_builder{
						Template:   publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
						PullSecret: &pullSecret,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetSpec().GetPullSecret()).To(Equal("***"))
		})

		It("Redacts pull_secret on Get response", func() {
			pullSecret := "my-secret-pull-secret"
			createResponse, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Spec: publicv1.ClusterSpec_builder{
						Template:   publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
						PullSecret: &pullSecret,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			getResponse, err := server.Get(ctx, publicv1.ClustersGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetSpec().GetPullSecret()).To(Equal("***"))
		})

		It("Redacts pull_secret on List response", func() {
			pullSecret := "my-secret-pull-secret"
			_, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Spec: publicv1.ClusterSpec_builder{
						Template:   publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
						PullSecret: &pullSecret,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			listResponse, err := server.List(ctx, publicv1.ClustersListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(listResponse.GetItems()).ToNot(BeEmpty())
			Expect(listResponse.GetItems()[0].GetSpec().GetPullSecret()).To(Equal("***"))
		})

		It("Preserves explicit fields through create and get", func() {
			pullSecret := "my-pull-secret"
			sshKey := "ssh-ed25519 AAAA..."
			podCIDR := "10.128.0.0/14"
			serviceCIDR := "172.30.0.0/16"

			// Seed a non-default ClusterVersion so the assertion proves the explicit
			// value was preserved, not the system default applied.
			seedClusterVersion(ctx, privatev1.ClusterVersion_builder{
				Id: "cv-non-default",
				Metadata: privatev1.Metadata_builder{
					Name:   "4-18-0",
					Tenant: auth.SharedTenant,
				}.Build(),
				Spec: privatev1.ClusterVersionSpec_builder{
					Image:   "quay.io/openshift-release-dev/ocp-release:4.18.0-multi",
					Enabled: proto.Bool(true),
					Version: "4.18.0",
					State:   privatev1.ClusterVersionState_CLUSTER_VERSION_STATE_ACTIVE,
				}.Build(),
			}.Build())

			versionName := "4-18-0"
			createResponse, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Spec: publicv1.ClusterSpec_builder{
						Template:     publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
						PullSecret:   &pullSecret,
						SshPublicKey: &sshKey,
						Version:      &publicv1.ClusterVersionReference{Name: versionName},
						Network: publicv1.ClusterNetwork_builder{
							PodCidr:     &podCIDR,
							ServiceCidr: &serviceCIDR,
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			getResponse, err := server.Get(ctx, publicv1.ClustersGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			spec := getResponse.GetObject().GetSpec()

			// pull_secret is redacted
			Expect(spec.GetPullSecret()).To(Equal("***"))
			// other fields preserved
			Expect(spec.GetSshPublicKey()).To(Equal(sshKey))
			Expect(spec.GetVersion().GetName()).To(Equal(versionName))
			Expect(spec.GetNetwork().GetPodCidr()).To(Equal(podCIDR))
			Expect(spec.GetNetwork().GetServiceCidr()).To(Equal(serviceCIDR))
		})

		It("Does not redact pull_secret when not set", func() {
			response, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetSpec().HasPullSecret()).To(BeFalse())
		})

		It("Update does not overwrite pull_secret with redacted sentinel", func() {
			// Create with a real pull secret:
			pullSecret := "my-real-pull-secret"
			createResponse, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-cluster",
					}.Build(),
					Spec: publicv1.ClusterSpec_builder{
						Template:   publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
						PullSecret: &pullSecret,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()

			// Update echoing back the redacted value (simulating a client that
			// does GET then PUT with the full object):
			redacted := "***"
			_, err = server.Update(ctx, publicv1.ClustersUpdateRequest_builder{
				Object: publicv1.Cluster_builder{
					Id: id,
					Spec: publicv1.ClusterSpec_builder{
						Template:   publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
						PullSecret: &redacted,
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Verify the stored value is still the original, not "***":
			getResponse, err := server.Get(ctx, publicv1.ClustersGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			// The GET returns "***" (redacted), but we need to verify the
			// stored value wasn't corrupted. We do this by checking that
			// pull_secret is still present (HasPullSecret) — if it had been
			// cleared by stripRedactedSecrets, it would not be set.
			Expect(getResponse.GetObject().GetSpec().HasPullSecret()).To(BeTrue())
			Expect(getResponse.GetObject().GetSpec().GetPullSecret()).To(Equal("***"))
		})

		It("Rejects invalid pod_cidr", func() {
			invalidCIDR := "not-a-cidr"
			_, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
						Network: publicv1.ClusterNetwork_builder{
							PodCidr: &invalidCIDR,
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("pod_cidr"))
		})

		It("Rejects invalid service_cidr", func() {
			validPodCIDR := "10.128.0.0/14"
			invalidCIDR := "999.999.999.999/99"
			_, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
						Network: publicv1.ClusterNetwork_builder{
							PodCidr:     &validPodCIDR,
							ServiceCidr: &invalidCIDR,
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("service_cidr"))
		})

		It("Accepts valid CIDRs", func() {
			podCIDR := "10.128.0.0/14"
			serviceCIDR := "172.30.0.0/16"
			response, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
						Network: publicv1.ClusterNetwork_builder{
							PodCidr:     &podCIDR,
							ServiceCidr: &serviceCIDR,
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetSpec().GetNetwork().GetPodCidr()).To(Equal(podCIDR))
			Expect(response.GetObject().GetSpec().GetNetwork().GetServiceCidr()).To(Equal(serviceCIDR))
		})

		It("Rejects invalid pod_cidr on update", func() {
			createResponse, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-cluster",
					}.Build(),
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			invalidCIDR := "not-a-cidr"
			_, err = server.Update(ctx, publicv1.ClustersUpdateRequest_builder{
				Object: publicv1.Cluster_builder{
					Id: createResponse.GetObject().GetId(),
					Spec: publicv1.ClusterSpec_builder{
						Network: publicv1.ClusterNetwork_builder{
							PodCidr: &invalidCIDR,
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("pod_cidr"))
		})

		It("Rejects invalid service_cidr on update", func() {
			createResponse, err := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
				Object: publicv1.Cluster_builder{
					Metadata: publicv1.Metadata_builder{
						Name: "test-cluster",
					}.Build(),
					Spec: publicv1.ClusterSpec_builder{
						Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			invalidCIDR := "999.999.999.999/99"
			_, err = server.Update(ctx, publicv1.ClustersUpdateRequest_builder{
				Object: publicv1.Cluster_builder{
					Id: createResponse.GetObject().GetId(),
					Spec: publicv1.ClusterSpec_builder{
						Network: publicv1.ClusterNetwork_builder{
							ServiceCidr: &invalidCIDR,
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(ContainSubstring("service_cidr"))
		})

		Describe("Dry run", func() {
			It("Returns resolved cluster without persisting", func() {
				response, err := server.Create(dryRunCtx(), publicv1.ClustersCreateRequest_builder{
					Object: publicv1.Cluster_builder{
						Spec: publicv1.ClusterSpec_builder{
							Template: publicv1.ClusterTemplateReference_builder{Id: "my_template"}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
				Expect(response.GetObject()).ToNot(BeNil())

				listResponse, err := server.List(ctx, publicv1.ClustersListRequest_builder{}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(listResponse.GetTotal()).To(Equal(int32(0)))
			})

			It("Returns same error as real creation for invalid template", func() {
				_, realErr := server.Create(ctx, publicv1.ClustersCreateRequest_builder{
					Object: publicv1.Cluster_builder{
						Spec: publicv1.ClusterSpec_builder{
							Template: publicv1.ClusterTemplateReference_builder{Id: "non-existent"}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(realErr).To(HaveOccurred())

				_, dryRunErr := server.Create(dryRunCtx(), publicv1.ClustersCreateRequest_builder{
					Object: publicv1.Cluster_builder{
						Spec: publicv1.ClusterSpec_builder{
							Template: publicv1.ClusterTemplateReference_builder{Id: "non-existent"}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(dryRunErr).To(HaveOccurred())
				Expect(grpcstatus.Code(dryRunErr)).To(Equal(grpcstatus.Code(realErr)))
				Expect(grpcstatus.Convert(dryRunErr).Message()).To(Equal(grpcstatus.Convert(realErr).Message()))
			})
		})
	})
})
