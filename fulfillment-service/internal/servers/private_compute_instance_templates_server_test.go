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
	"time"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
)

var _ = Describe("Private compute instance templates server", func() {
	Describe("Builder", func() {
		It("Creates server with logger", func() {
			server, err := NewPrivateComputeInstanceTemplatesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Doesn't create server without logger", func() {
			server, err := NewPrivateComputeInstanceTemplatesServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(server).To(BeNil())
		})

		It("Fails if attribution logic is not set", func() {
			server, err := NewPrivateComputeInstanceTemplatesServer().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("attribution logic is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewPrivateComputeInstanceTemplatesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var server *PrivateComputeInstanceTemplatesServer

		BeforeEach(func() {
			var err error

			// Create the server:
			server, err = NewPrivateComputeInstanceTemplatesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Creates object", func() {
			response, err := server.Create(ctx, privatev1.ComputeInstanceTemplatesCreateRequest_builder{
				Object: privatev1.ComputeInstanceTemplate_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:       "My title",
					Description: "My description.",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).ToNot(BeEmpty())
			Expect(object.GetTitle()).To(Equal("My title"))
			Expect(object.GetDescription()).To(Equal("My description."))
		})

		It("Creates object with parameters", func() {
			response, err := server.Create(ctx, privatev1.ComputeInstanceTemplatesCreateRequest_builder{
				Object: privatev1.ComputeInstanceTemplate_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:       "My title",
					Description: "My description.",
					Parameters: []*privatev1.ComputeInstanceTemplateParameterDefinition{
						privatev1.ComputeInstanceTemplateParameterDefinition_builder{
							Name:        "cpu_count",
							Title:       "CPU Count",
							Description: "Number of CPUs",
							Required:    true,
							Type:        "type.googleapis.com/google.protobuf.Int32Value",
						}.Build(),
						privatev1.ComputeInstanceTemplateParameterDefinition_builder{
							Name:        "memory_gb",
							Title:       "Memory (GB)",
							Description: "Amount of memory in GB",
							Required:    false,
							Type:        "type.googleapis.com/google.protobuf.Int32Value",
						}.Build(),
					},
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).ToNot(BeEmpty())
			Expect(object.GetTitle()).To(Equal("My title"))
			Expect(object.GetDescription()).To(Equal("My description."))
			parameters := object.GetParameters()
			Expect(parameters).To(HaveLen(2))
			Expect(parameters[0].GetName()).To(Equal("cpu_count"))
			Expect(parameters[0].GetRequired()).To(BeTrue())
			Expect(parameters[1].GetName()).To(Equal("memory_gb"))
			Expect(parameters[1].GetRequired()).To(BeFalse())
		})

		It("List objects", func() {
			// Create a few objects:
			const count = 10
			for i := range count {
				_, err := server.Create(ctx, privatev1.ComputeInstanceTemplatesCreateRequest_builder{
					Object: privatev1.ComputeInstanceTemplate_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Title:       fmt.Sprintf("My title %d", i),
						Description: fmt.Sprintf("My description %d.", i),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects:
			response, err := server.List(ctx, privatev1.ComputeInstanceTemplatesListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			items := response.GetItems()
			Expect(items).To(HaveLen(count))
		})

		It("List objects with limit", func() {
			// Create a few objects:
			const count = 10
			for i := range count {
				_, err := server.Create(ctx, privatev1.ComputeInstanceTemplatesCreateRequest_builder{
					Object: privatev1.ComputeInstanceTemplate_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Title:       fmt.Sprintf("My title %d", i),
						Description: fmt.Sprintf("My description %d.", i),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects with limit:
			response, err := server.List(ctx, privatev1.ComputeInstanceTemplatesListRequest_builder{
				Limit: new(int32(5)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			items := response.GetItems()
			Expect(items).To(HaveLen(5))
		})

		It("List objects with offset", func() {
			// Create a few objects:
			const count = 10
			for i := range count {
				_, err := server.Create(ctx, privatev1.ComputeInstanceTemplatesCreateRequest_builder{
					Object: privatev1.ComputeInstanceTemplate_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Title:       fmt.Sprintf("My title %d", i),
						Description: fmt.Sprintf("My description %d.", i),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects with offset:
			response, err := server.List(ctx, privatev1.ComputeInstanceTemplatesListRequest_builder{
				Offset: new(int32(5)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			items := response.GetItems()
			Expect(items).To(HaveLen(5))
		})

		It("Gets object", func() {
			// Create an object:
			createResponse, err := server.Create(ctx, privatev1.ComputeInstanceTemplatesCreateRequest_builder{
				Object: privatev1.ComputeInstanceTemplate_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:       "My title",
					Description: "My description.",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(createResponse).ToNot(BeNil())
			createdObject := createResponse.GetObject()
			Expect(createdObject).ToNot(BeNil())
			id := createdObject.GetId()
			Expect(id).ToNot(BeEmpty())

			// Get the object:
			getResponse, err := server.Get(ctx, privatev1.ComputeInstanceTemplatesGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse).ToNot(BeNil())
			object := getResponse.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).To(Equal(id))
			Expect(object.GetTitle()).To(Equal("My title"))
			Expect(object.GetDescription()).To(Equal("My description."))
		})

		It("Updates object", func() {
			// Create an object:
			createResponse, err := server.Create(ctx, privatev1.ComputeInstanceTemplatesCreateRequest_builder{
				Object: privatev1.ComputeInstanceTemplate_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-ci-template-update",
					}.Build(),
					Title:       "My title",
					Description: "My description.",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(createResponse).ToNot(BeNil())
			createdObject := createResponse.GetObject()
			Expect(createdObject).ToNot(BeNil())
			id := createdObject.GetId()
			Expect(id).ToNot(BeEmpty())

			// Update the object:
			updateResponse, err := server.Update(ctx, privatev1.ComputeInstanceTemplatesUpdateRequest_builder{
				Object: privatev1.ComputeInstanceTemplate_builder{
					Id:          id,
					Title:       "My updated title",
					Description: "My updated description.",
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"title", "description"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse).ToNot(BeNil())
			object := updateResponse.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).To(Equal(id))
			Expect(object.GetTitle()).To(Equal("My updated title"))
			Expect(object.GetDescription()).To(Equal("My updated description."))
		})

		It("Updates object parameters", func() {
			// Create an object with parameters:
			createResponse, err := server.Create(ctx, privatev1.ComputeInstanceTemplatesCreateRequest_builder{
				Object: privatev1.ComputeInstanceTemplate_builder{
					Metadata: privatev1.Metadata_builder{
						Name: "test-ci-template-params",
					}.Build(),
					Title:       "My title",
					Description: "My description.",
					Parameters: []*privatev1.ComputeInstanceTemplateParameterDefinition{
						privatev1.ComputeInstanceTemplateParameterDefinition_builder{
							Name:        "cpu_count",
							Title:       "CPU Count",
							Description: "Number of CPUs",
							Required:    true,
							Type:        "type.googleapis.com/google.protobuf.Int32Value",
						}.Build(),
					},
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(createResponse).ToNot(BeNil())
			createdObject := createResponse.GetObject()
			Expect(createdObject).ToNot(BeNil())
			id := createdObject.GetId()
			Expect(id).ToNot(BeEmpty())

			// Update the object with new parameters:
			updateResponse, err := server.Update(ctx, privatev1.ComputeInstanceTemplatesUpdateRequest_builder{
				Object: privatev1.ComputeInstanceTemplate_builder{
					Id:          id,
					Title:       "My title",
					Description: "My description.",
					Parameters: []*privatev1.ComputeInstanceTemplateParameterDefinition{
						privatev1.ComputeInstanceTemplateParameterDefinition_builder{
							Name:        "memory_gb",
							Title:       "Memory (GB)",
							Description: "Amount of memory in GB",
							Required:    false,
							Type:        "type.googleapis.com/google.protobuf.Int32Value",
						}.Build(),
					},
				}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"parameters"},
				},
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse).ToNot(BeNil())
			object := updateResponse.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).To(Equal(id))
			parameters := object.GetParameters()
			Expect(parameters).To(HaveLen(1))
			Expect(parameters[0].GetName()).To(Equal("memory_gb"))
			Expect(parameters[0].GetRequired()).To(BeFalse())
		})

		It("Deletes object", func() {
			// Create an object:
			createResponse, err := server.Create(ctx, privatev1.ComputeInstanceTemplatesCreateRequest_builder{
				Object: privatev1.ComputeInstanceTemplate_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
					}.Build(),
					Title:       "My title",
					Description: "My description.",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(createResponse).ToNot(BeNil())
			createdObject := createResponse.GetObject()
			Expect(createdObject).ToNot(BeNil())
			id := createdObject.GetId()
			Expect(id).ToNot(BeEmpty())

			// Delete the object:
			deleteResponse, err := server.Delete(ctx, privatev1.ComputeInstanceTemplatesDeleteRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(deleteResponse).ToNot(BeNil())

			// Verify the object is deleted:
			getResponse, err := server.Get(ctx, privatev1.ComputeInstanceTemplatesGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(getResponse).To(BeNil())
		})

		It("Handles non-existent object", func() {
			// Try to get a non-existent object:
			getResponse, err := server.Get(ctx, privatev1.ComputeInstanceTemplatesGetRequest_builder{
				Id: "non-existent-id",
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(getResponse).To(BeNil())
		})

		It("Rejects creation if no object is provided", func() {
			response, err := server.Create(ctx, privatev1.ComputeInstanceTemplatesCreateRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
			Expect(status.Message()).To(Equal("metadata is required"))
		})

		It("Handles empty object in update request", func() {
			// Try to update with nil object:
			response, err := server.Update(ctx, privatev1.ComputeInstanceTemplatesUpdateRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
		})

		It("Handles empty ID in get request", func() {
			// Try to get with empty ID:
			response, err := server.Get(ctx, privatev1.ComputeInstanceTemplatesGetRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
		})

		It("Handles empty ID in delete request", func() {
			// Try to delete with empty ID:
			response, err := server.Delete(ctx, privatev1.ComputeInstanceTemplatesDeleteRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
		})

		Describe("Instance type validation in spec_defaults", func() {
			var itServer *PrivateInstanceTypesServer

			// Helper to create an instance type and transition it to the given state.
			createInstanceTypeWithState := func(name string, state privatev1.InstanceTypeState) {
				_, err := itServer.Create(ctx, privatev1.InstanceTypesCreateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Metadata: privatev1.Metadata_builder{
							Name: name,
						}.Build(),
						Spec: privatev1.InstanceTypeSpec_builder{
							Cores:       4,
							MemoryGib:   16,
							Description: "Test instance type.",
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				if state == privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE {
					return
				}

				// Transition to the desired state:
				_, err = itServer.Update(ctx, privatev1.InstanceTypesUpdateRequest_builder{
					Object: privatev1.InstanceType_builder{
						Id: name,
						Spec: privatev1.InstanceTypeSpec_builder{
							State: state,
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.state"}},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			BeforeEach(func() {
				var err error
				itServer, err = NewPrivateInstanceTypesServer().
					SetLogger(logger).
					SetAttributionLogic(attribution).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())
			})

			It("Returns warning when spec_defaults references a DEPRECATED instance type on Create", func() {
				createInstanceTypeWithState("deprecated-type",
					privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED)

				response, err := server.Create(ctx, privatev1.ComputeInstanceTemplatesCreateRequest_builder{
					Object: privatev1.ComputeInstanceTemplate_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Title:       "Template with deprecated default",
						Description: "Template referencing a deprecated instance type.",
						SpecDefaults: privatev1.ComputeInstanceTemplateSpecDefaults_builder{
							InstanceType: privatev1.InstanceTypeReference_builder{Id: "deprecated-type"}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
				Expect(response.GetWarnings()).To(HaveLen(1))
				Expect(response.GetWarnings()[0]).To(ContainSubstring("deprecated"))
			})

			It("Returns warning when spec_defaults references a DEPRECATED instance type on Update", func() {
				// Create a template first (no spec_defaults):
				createResponse, err := server.Create(ctx, privatev1.ComputeInstanceTemplatesCreateRequest_builder{
					Object: privatev1.ComputeInstanceTemplate_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "test-ci-template-deprecated-update",
						}.Build(),
						Title:       "Template to update",
						Description: "Template without spec_defaults.",
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				id := createResponse.GetObject().GetId()

				// Create a DEPRECATED instance type:
				createInstanceTypeWithState("deprecated-for-update",
					privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED)

				// Update the template with spec_defaults referencing the deprecated type:
				updateResponse, err := server.Update(ctx, privatev1.ComputeInstanceTemplatesUpdateRequest_builder{
					Object: privatev1.ComputeInstanceTemplate_builder{
						Id: id,
						SpecDefaults: privatev1.ComputeInstanceTemplateSpecDefaults_builder{
							InstanceType: privatev1.InstanceTypeReference_builder{Id: "deprecated-for-update"}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec_defaults"},
					},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(updateResponse).ToNot(BeNil())
				Expect(updateResponse.GetWarnings()).To(HaveLen(1))
			})

			It("Rejects Create when spec_defaults references an OBSOLETE instance type", func() {
				createInstanceTypeWithState("obsolete-type",
					privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_OBSOLETE)

				response, err := server.Create(ctx, privatev1.ComputeInstanceTemplatesCreateRequest_builder{
					Object: privatev1.ComputeInstanceTemplate_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Title:       "Template with obsolete default",
						Description: "Template referencing an obsolete instance type.",
						SpecDefaults: privatev1.ComputeInstanceTemplateSpecDefaults_builder{
							InstanceType: privatev1.InstanceTypeReference_builder{Id: "obsolete-type"}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				Expect(response).To(BeNil())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
				Expect(status.Message()).To(ContainSubstring("obsolete"))
			})

			It("Rejects Update when spec_defaults references an OBSOLETE instance type", func() {
				// Create a template first:
				createResponse, err := server.Create(ctx, privatev1.ComputeInstanceTemplatesCreateRequest_builder{
					Object: privatev1.ComputeInstanceTemplate_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "test-ci-template-obsolete-update",
						}.Build(),
						Title:       "Template for obsolete update",
						Description: "Template to test obsolete rejection on update.",
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				id := createResponse.GetObject().GetId()

				// Create an OBSOLETE instance type:
				createInstanceTypeWithState("obsolete-for-update",
					privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_OBSOLETE)

				// Try to update with spec_defaults referencing the obsolete type:
				updateResponse, err := server.Update(ctx, privatev1.ComputeInstanceTemplatesUpdateRequest_builder{
					Object: privatev1.ComputeInstanceTemplate_builder{
						Id: id,
						SpecDefaults: privatev1.ComputeInstanceTemplateSpecDefaults_builder{
							InstanceType: privatev1.InstanceTypeReference_builder{Id: "obsolete-for-update"}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec_defaults"},
					},
				}.Build())
				Expect(err).To(HaveOccurred())
				Expect(updateResponse).To(BeNil())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
			})

			It("Returns no warnings when spec_defaults references an ACTIVE instance type", func() {
				createInstanceTypeWithState("active-default",
					privatev1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE)

				response, err := server.Create(ctx, privatev1.ComputeInstanceTemplatesCreateRequest_builder{
					Object: privatev1.ComputeInstanceTemplate_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Title:       "Template with active default",
						Description: "Template referencing an active instance type.",
						SpecDefaults: privatev1.ComputeInstanceTemplateSpecDefaults_builder{
							InstanceType: privatev1.InstanceTypeReference_builder{Id: "active-default"}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
				Expect(response.GetWarnings()).To(BeEmpty())
			})

			It("Rejects Create when spec_defaults references a non-existent instance type", func() {
				response, err := server.Create(ctx, privatev1.ComputeInstanceTemplatesCreateRequest_builder{
					Object: privatev1.ComputeInstanceTemplate_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Title:       "Template with missing default",
						Description: "Template referencing a non-existent instance type.",
						SpecDefaults: privatev1.ComputeInstanceTemplateSpecDefaults_builder{
							InstanceType: privatev1.InstanceTypeReference_builder{Id: "non-existent-type"}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				Expect(response).To(BeNil())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.NotFound))
			})
		})

		Describe("Disk image validation in spec_defaults", func() {
			// Helper to seed a DiskImage with a given lifecycle. Seeded directly through the
			// DAO (as a shared/global image) so the template server's tenancy-filtered DAO
			// resolves it. Note: unit tests call the server directly, so the gRPC
			// reference-validation interceptor is not in the chain — existence is exercised
			// here by the handler's own lookup.
			createDiskImageWithLifecycle := func(
				name string,
				lifecycle privatev1.DiskImageLifecycle,
				deprecation *privatev1.DiskImageDeprecation,
			) {
				diskImagesDao, err := dao.NewGenericDAO[*privatev1.DiskImage]().
					SetLogger(logger).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())

				_, err = diskImagesDao.Create().SetObject(
					privatev1.DiskImage_builder{
						Id: name,
						Metadata: privatev1.Metadata_builder{
							Name:   name,
							Tenant: auth.SharedTenant,
						}.Build(),
						Spec: privatev1.DiskImageSpec_builder{
							Lifecycle:   lifecycle,
							Deprecation: deprecation,
						}.Build(),
					}.Build(),
				).Do(ctx)
				Expect(err).ToNot(HaveOccurred())
			}

			It("Returns warning when spec_defaults references a DEPRECATED disk image on Create", func() {
				createDiskImageWithLifecycle("deprecated-di",
					privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_DEPRECATED,
					privatev1.DiskImageDeprecation_builder{
						ObsolescenceTimestamp: timestamppb.New(
							time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)),
					}.Build())

				response, err := server.Create(ctx, privatev1.ComputeInstanceTemplatesCreateRequest_builder{
					Object: privatev1.ComputeInstanceTemplate_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Title:       "Template with deprecated disk image default",
						Description: "Template referencing a deprecated disk image.",
						SpecDefaults: privatev1.ComputeInstanceTemplateSpecDefaults_builder{
							DiskImage: privatev1.DiskImageReference_builder{Id: "deprecated-di"}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
				Expect(response.GetWarnings()).To(HaveLen(1))
				Expect(response.GetWarnings()[0]).To(ContainSubstring("deprecated"))
				Expect(response.GetWarnings()[0]).To(ContainSubstring("2027"))
			})

			It("Returns warning when spec_defaults references a DEPRECATED disk image on Update", func() {
				// Create a template first (no spec_defaults):
				createResponse, err := server.Create(ctx, privatev1.ComputeInstanceTemplatesCreateRequest_builder{
					Object: privatev1.ComputeInstanceTemplate_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "test-ci-template-di-deprecated-update",
						}.Build(),
						Title:       "Template to update",
						Description: "Template without spec_defaults.",
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				id := createResponse.GetObject().GetId()

				createDiskImageWithLifecycle("deprecated-di-for-update",
					privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_DEPRECATED, nil)

				updateResponse, err := server.Update(ctx, privatev1.ComputeInstanceTemplatesUpdateRequest_builder{
					Object: privatev1.ComputeInstanceTemplate_builder{
						Id: id,
						SpecDefaults: privatev1.ComputeInstanceTemplateSpecDefaults_builder{
							DiskImage: privatev1.DiskImageReference_builder{Id: "deprecated-di-for-update"}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec_defaults"},
					},
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(updateResponse).ToNot(BeNil())
				Expect(updateResponse.GetWarnings()).To(HaveLen(1))
				Expect(updateResponse.GetWarnings()[0]).To(ContainSubstring("deprecated"))
			})

			It("Rejects Create when spec_defaults references an OBSOLETE disk image", func() {
				createDiskImageWithLifecycle("obsolete-di",
					privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_OBSOLETE, nil)

				response, err := server.Create(ctx, privatev1.ComputeInstanceTemplatesCreateRequest_builder{
					Object: privatev1.ComputeInstanceTemplate_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Title:       "Template with obsolete disk image default",
						Description: "Template referencing an obsolete disk image.",
						SpecDefaults: privatev1.ComputeInstanceTemplateSpecDefaults_builder{
							DiskImage: privatev1.DiskImageReference_builder{Id: "obsolete-di"}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				Expect(response).To(BeNil())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
				Expect(status.Message()).To(ContainSubstring("obsolete"))
			})

			It("Rejects Update when spec_defaults references an OBSOLETE disk image", func() {
				createResponse, err := server.Create(ctx, privatev1.ComputeInstanceTemplatesCreateRequest_builder{
					Object: privatev1.ComputeInstanceTemplate_builder{
						Metadata: privatev1.Metadata_builder{
							Name: "test-ci-template-di-obsolete-update",
						}.Build(),
						Title:       "Template for obsolete disk image update",
						Description: "Template to test obsolete rejection on update.",
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				id := createResponse.GetObject().GetId()

				createDiskImageWithLifecycle("obsolete-di-for-update",
					privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_OBSOLETE, nil)

				updateResponse, err := server.Update(ctx, privatev1.ComputeInstanceTemplatesUpdateRequest_builder{
					Object: privatev1.ComputeInstanceTemplate_builder{
						Id: id,
						SpecDefaults: privatev1.ComputeInstanceTemplateSpecDefaults_builder{
							DiskImage: privatev1.DiskImageReference_builder{Id: "obsolete-di-for-update"}.Build(),
						}.Build(),
					}.Build(),
					UpdateMask: &fieldmaskpb.FieldMask{
						Paths: []string{"spec_defaults"},
					},
				}.Build())
				Expect(err).To(HaveOccurred())
				Expect(updateResponse).To(BeNil())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
			})

			It("Returns no warnings when spec_defaults references an AVAILABLE disk image", func() {
				createDiskImageWithLifecycle("available-di",
					privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE, nil)

				response, err := server.Create(ctx, privatev1.ComputeInstanceTemplatesCreateRequest_builder{
					Object: privatev1.ComputeInstanceTemplate_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Title:       "Template with available disk image default",
						Description: "Template referencing an available disk image.",
						SpecDefaults: privatev1.ComputeInstanceTemplateSpecDefaults_builder{
							DiskImage: privatev1.DiskImageReference_builder{Id: "available-di"}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
				Expect(response.GetWarnings()).To(BeEmpty())
			})

			It("Resolves an AVAILABLE disk image referenced by name", func() {
				createDiskImageWithLifecycle("available-di-by-name",
					privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE, nil)

				response, err := server.Create(ctx, privatev1.ComputeInstanceTemplatesCreateRequest_builder{
					Object: privatev1.ComputeInstanceTemplate_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Title:       "Template referencing disk image by name",
						Description: "Template referencing an available disk image by name.",
						SpecDefaults: privatev1.ComputeInstanceTemplateSpecDefaults_builder{
							DiskImage: privatev1.DiskImageReference_builder{Name: "available-di-by-name"}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response).ToNot(BeNil())
				Expect(response.GetWarnings()).To(BeEmpty())
			})

			It("Rejects Create when spec_defaults references a non-existent disk image", func() {
				response, err := server.Create(ctx, privatev1.ComputeInstanceTemplatesCreateRequest_builder{
					Object: privatev1.ComputeInstanceTemplate_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Title:       "Template with missing disk image default",
						Description: "Template referencing a non-existent disk image.",
						SpecDefaults: privatev1.ComputeInstanceTemplateSpecDefaults_builder{
							DiskImage: privatev1.DiskImageReference_builder{Id: "nonexistent-di"}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				Expect(response).To(BeNil())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.NotFound))
			})

			// Deletion protection (OSAC-3727 AC-2 / TC-FR12-02). The Z0003 reverse-reference
			// trigger from migration 99 blocks soft-deleting a DiskImage that is still
			// referenced by an active ComputeInstanceTemplate's spec_defaults.disk_image
			// (matched via data->'spec_defaults'->'disk_image'->>'id'). This is a unit test,
			// not an it/ test: the servers suite runs a real Postgres with all migrations
			// applied, so the trigger is live, and the DiskImages server's generic Delete maps
			// the resulting dao.ErrInUse to gRPC FailedPrecondition — the full chain under test
			// without needing a kind cluster.
			It("Blocks deleting a DiskImage referenced by an active template's spec_defaults", func() {
				createDiskImageWithLifecycle("protected-di",
					privatev1.DiskImageLifecycle_DISK_IMAGE_LIFECYCLE_AVAILABLE, nil)

				// Create a template that references the disk image by id, so the Z0003
				// trigger's id-based JSONB match applies.
				_, err := server.Create(ctx, privatev1.ComputeInstanceTemplatesCreateRequest_builder{
					Object: privatev1.ComputeInstanceTemplate_builder{
						Metadata: privatev1.Metadata_builder{
							Name: fmt.Sprintf("test-%s", uuid.NewString()[:8]),
						}.Build(),
						Title:       "Template protecting a disk image",
						Description: "Template referencing a disk image to exercise deletion protection.",
						SpecDefaults: privatev1.ComputeInstanceTemplateSpecDefaults_builder{
							DiskImage: privatev1.DiskImageReference_builder{Id: "protected-di"}.Build(),
						}.Build(),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())

				// Attempt to delete the referenced disk image through the DiskImages server,
				// exercising the real DAO Delete -> Z0003 -> FailedPrecondition path.
				diskImagesServer, err := NewPrivateDiskImagesServer().
					SetLogger(logger).
					SetAttributionLogic(attribution).
					SetTenancyLogic(tenancy).
					Build()
				Expect(err).ToNot(HaveOccurred())

				_, err = diskImagesServer.Delete(ctx, privatev1.DiskImagesDeleteRequest_builder{
					Id: "protected-di",
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.FailedPrecondition))
			})
		})
	})
})
