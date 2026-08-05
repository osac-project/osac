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
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
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

		It("Creates empty object if no object is provided", func() {
			response, err := server.Create(ctx, privatev1.ComputeInstanceTemplatesCreateRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			Expect(response.GetObject()).ToNot(BeNil())
			Expect(response.GetObject().GetId()).ToNot(BeEmpty())
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
						Title:       "Template with deprecated default",
						Description: "Template referencing a deprecated instance type.",
						SpecDefaults: privatev1.ComputeInstanceTemplateSpecDefaults_builder{
							InstanceType: new("deprecated-type"),
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
							InstanceType: new("deprecated-for-update"),
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
						Title:       "Template with obsolete default",
						Description: "Template referencing an obsolete instance type.",
						SpecDefaults: privatev1.ComputeInstanceTemplateSpecDefaults_builder{
							InstanceType: new("obsolete-type"),
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
							InstanceType: new("obsolete-for-update"),
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
						Title:       "Template with active default",
						Description: "Template referencing an active instance type.",
						SpecDefaults: privatev1.ComputeInstanceTemplateSpecDefaults_builder{
							InstanceType: new("active-default"),
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
						Title:       "Template with missing default",
						Description: "Template referencing a non-existent instance type.",
						SpecDefaults: privatev1.ComputeInstanceTemplateSpecDefaults_builder{
							InstanceType: new("non-existent-type"),
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
	})
})
