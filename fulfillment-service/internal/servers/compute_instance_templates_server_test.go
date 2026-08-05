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
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
)

var _ = Describe("Compute instance templates server", func() {
	Describe("Builder", func() {
		It("Creates server with logger and tenancy logic", func() {
			// Create the public server:
			server, err := NewComputeInstanceTemplatesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Doesn't create server without logger", func() {
			// Try to create the public server without logger:
			server, err := NewComputeInstanceTemplatesServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(server).To(BeNil())
		})

		It("Fails if attribution logic is not set", func() {
			server, err := NewComputeInstanceTemplatesServer().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("attribution logic is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewComputeInstanceTemplatesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var server *ComputeInstanceTemplatesServer

		BeforeEach(func() {
			var err error

			// Create the public server:
			server, err = NewComputeInstanceTemplatesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Creates object", func() {
			response, err := server.Create(ctx, publicv1.ComputeInstanceTemplatesCreateRequest_builder{
				Object: publicv1.ComputeInstanceTemplate_builder{
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
			response, err := server.Create(ctx, publicv1.ComputeInstanceTemplatesCreateRequest_builder{
				Object: publicv1.ComputeInstanceTemplate_builder{
					Title:       "My title",
					Description: "My description.",
					Parameters: []*publicv1.ComputeInstanceTemplateParameterDefinition{
						publicv1.ComputeInstanceTemplateParameterDefinition_builder{
							Name:        "cpu_count",
							Title:       "CPU Count",
							Description: "Number of CPUs",
							Required:    true,
							Type:        "type.googleapis.com/google.protobuf.Int32Value",
						}.Build(),
						publicv1.ComputeInstanceTemplateParameterDefinition_builder{
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
				_, err := server.Create(ctx, publicv1.ComputeInstanceTemplatesCreateRequest_builder{
					Object: publicv1.ComputeInstanceTemplate_builder{
						Title:       fmt.Sprintf("My title %d", i),
						Description: fmt.Sprintf("My description %d.", i),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects:
			response, err := server.List(ctx, publicv1.ComputeInstanceTemplatesListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			items := response.GetItems()
			Expect(items).To(HaveLen(count))
		})

		It("List objects with limit", func() {
			// Create a few objects:
			const count = 10
			for i := range count {
				_, err := server.Create(ctx, publicv1.ComputeInstanceTemplatesCreateRequest_builder{
					Object: publicv1.ComputeInstanceTemplate_builder{
						Title:       fmt.Sprintf("My title %d", i),
						Description: fmt.Sprintf("My description %d.", i),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects with limit:
			response, err := server.List(ctx, publicv1.ComputeInstanceTemplatesListRequest_builder{
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
				_, err := server.Create(ctx, publicv1.ComputeInstanceTemplatesCreateRequest_builder{
					Object: publicv1.ComputeInstanceTemplate_builder{
						Title:       fmt.Sprintf("My title %d", i),
						Description: fmt.Sprintf("My description %d.", i),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects with offset:
			response, err := server.List(ctx, publicv1.ComputeInstanceTemplatesListRequest_builder{
				Offset: new(int32(5)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			items := response.GetItems()
			Expect(items).To(HaveLen(5))
		})

		It("Gets object", func() {
			// Create an object:
			createResponse, err := server.Create(ctx, publicv1.ComputeInstanceTemplatesCreateRequest_builder{
				Object: publicv1.ComputeInstanceTemplate_builder{
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
			getResponse, err := server.Get(ctx, publicv1.ComputeInstanceTemplatesGetRequest_builder{
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
			createResponse, err := server.Create(ctx, publicv1.ComputeInstanceTemplatesCreateRequest_builder{
				Object: publicv1.ComputeInstanceTemplate_builder{
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
			updateResponse, err := server.Update(ctx, publicv1.ComputeInstanceTemplatesUpdateRequest_builder{
				Object: publicv1.ComputeInstanceTemplate_builder{
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
			createResponse, err := server.Create(ctx, publicv1.ComputeInstanceTemplatesCreateRequest_builder{
				Object: publicv1.ComputeInstanceTemplate_builder{
					Title:       "My title",
					Description: "My description.",
					Parameters: []*publicv1.ComputeInstanceTemplateParameterDefinition{
						publicv1.ComputeInstanceTemplateParameterDefinition_builder{
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
			updateResponse, err := server.Update(ctx, publicv1.ComputeInstanceTemplatesUpdateRequest_builder{
				Object: publicv1.ComputeInstanceTemplate_builder{
					Id:          id,
					Title:       "My title",
					Description: "My description.",
					Parameters: []*publicv1.ComputeInstanceTemplateParameterDefinition{
						publicv1.ComputeInstanceTemplateParameterDefinition_builder{
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
			createResponse, err := server.Create(ctx, publicv1.ComputeInstanceTemplatesCreateRequest_builder{
				Object: publicv1.ComputeInstanceTemplate_builder{
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
			deleteResponse, err := server.Delete(ctx, publicv1.ComputeInstanceTemplatesDeleteRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(deleteResponse).ToNot(BeNil())

			// Verify the object is deleted:
			getResponse, err := server.Get(ctx, publicv1.ComputeInstanceTemplatesGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(getResponse).To(BeNil())
		})

		It("Handles non-existent object", func() {
			// Try to get a non-existent object:
			getResponse, err := server.Get(ctx, publicv1.ComputeInstanceTemplatesGetRequest_builder{
				Id: "non-existent-id",
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(getResponse).To(BeNil())
		})

		It("Handles empty object in create request", func() {
			// Try to create with nil object:
			response, err := server.Create(ctx, publicv1.ComputeInstanceTemplatesCreateRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
		})

		It("Handles empty object in update request", func() {
			// Try to update with nil object:
			response, err := server.Update(ctx, publicv1.ComputeInstanceTemplatesUpdateRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
		})

		It("Handles empty ID in get request", func() {
			// Try to get with empty ID:
			response, err := server.Get(ctx, publicv1.ComputeInstanceTemplatesGetRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
		})

		It("Handles empty ID in delete request", func() {
			// Try to delete with empty ID:
			response, err := server.Delete(ctx, publicv1.ComputeInstanceTemplatesDeleteRequest_builder{}.Build())
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
		})

		It("Handles update without ID", func() {
			// Try to update without ID:
			response, err := server.Update(ctx, publicv1.ComputeInstanceTemplatesUpdateRequest_builder{
				Object: publicv1.ComputeInstanceTemplate_builder{
					Title:       "My title",
					Description: "My description.",
				}.Build(),
			}.Build())
			Expect(err).To(HaveOccurred())
			Expect(response).To(BeNil())
		})
	})
})
