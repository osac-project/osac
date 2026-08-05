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

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
)

var _ = Describe("Private host types server", func() {
	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewPrivateHostTypesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewPrivateHostTypesServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if attribution logic is not set", func() {
			server, err := NewPrivateHostTypesServer().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("attribution logic is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewPrivateHostTypesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var server *PrivateHostTypesServer

		BeforeEach(func() {
			var err error

			// Create the server:
			server, err = NewPrivateHostTypesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Creates object", func() {
			response, err := server.Create(ctx, privatev1.HostTypesCreateRequest_builder{
				Object: privatev1.HostType_builder{
					Title:       "My title",
					Description: "My description.",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).ToNot(BeEmpty())
		})

		It("List objects", func() {
			// Create a few objects:
			const count = 10
			for i := range count {
				_, err := server.Create(ctx, privatev1.HostTypesCreateRequest_builder{
					Object: privatev1.HostType_builder{
						Title:       fmt.Sprintf("My title %d", i),
						Description: fmt.Sprintf("My description %d.", i),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects:
			response, err := server.List(ctx, privatev1.HostTypesListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			items := response.GetItems()
			Expect(items).To(HaveLen(count))
		})

		It("List objects with limit", func() {
			// Create a few objects:
			const count = 10
			for i := range count {
				_, err := server.Create(ctx, privatev1.HostTypesCreateRequest_builder{
					Object: privatev1.HostType_builder{
						Title:       fmt.Sprintf("My title %d", i),
						Description: fmt.Sprintf("My description %d.", i),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects:
			response, err := server.List(ctx, privatev1.HostTypesListRequest_builder{
				Limit: new(int32(1)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", 1))
		})

		It("List objects with offset", func() {
			// Create a few objects:
			const count = 10
			for i := range count {
				_, err := server.Create(ctx, privatev1.HostTypesCreateRequest_builder{
					Object: privatev1.HostType_builder{
						Title:       fmt.Sprintf("My title %d", i),
						Description: fmt.Sprintf("My description %d.", i),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects:
			response, err := server.List(ctx, privatev1.HostTypesListRequest_builder{
				Offset: new(int32(1)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", count-1))
		})

		It("List objects with filter", func() {
			// Create a few objects:
			const count = 10
			var objects []*privatev1.HostType
			for i := range count {
				response, err := server.Create(ctx, privatev1.HostTypesCreateRequest_builder{
					Object: privatev1.HostType_builder{
						Title:       fmt.Sprintf("My title %d", i),
						Description: fmt.Sprintf("My description %d.", i),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				objects = append(objects, response.GetObject())
			}

			// List the objects:
			for _, object := range objects {
				response, err := server.List(ctx, privatev1.HostTypesListRequest_builder{
					Filter: new(fmt.Sprintf("this.id == '%s'", object.GetId())),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetSize()).To(BeNumerically("==", 1))
				Expect(response.GetItems()[0].GetId()).To(Equal(object.GetId()))
			}
		})

		It("Get object", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, privatev1.HostTypesCreateRequest_builder{
				Object: privatev1.HostType_builder{
					Title:       "My title",
					Description: "My description.",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Get it:
			getResponse, err := server.Get(ctx, privatev1.HostTypesGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(createResponse.GetObject(), getResponse.GetObject())).To(BeTrue())
		})

		It("Update object", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, privatev1.HostTypesCreateRequest_builder{
				Object: privatev1.HostType_builder{
					Title:       "My title",
					Description: "My description.",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Update the object:
			updateResponse, err := server.Update(ctx, privatev1.HostTypesUpdateRequest_builder{
				Object: privatev1.HostType_builder{
					Id:          object.GetId(),
					Title:       "Your title",
					Description: "Your description.",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetTitle()).To(Equal("Your title"))
			Expect(updateResponse.GetObject().GetDescription()).To(Equal("Your description."))

			// Get and verify:
			getResponse, err := server.Get(ctx, privatev1.HostTypesGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetTitle()).To(Equal("Your title"))
			Expect(getResponse.GetObject().GetDescription()).To(Equal("Your description."))
		})

		DescribeTable(
			"Rejects invalid labels on create and update",
			func(key string, value string, expected string) {
				_, err := server.Create(ctx, privatev1.HostTypesCreateRequest_builder{
					Object: privatev1.HostType_builder{
						Metadata: privatev1.Metadata_builder{
							Labels: map[string]string{
								key: value,
							},
						}.Build(),
						Title:       "My title",
						Description: "My description.",
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(Equal(expected))

				createResponse, err := server.Create(ctx, privatev1.HostTypesCreateRequest_builder{
					Object: privatev1.HostType_builder{
						Title:       "My title",
						Description: "My description.",
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				object := createResponse.GetObject()

				_, err = server.Update(ctx, privatev1.HostTypesUpdateRequest_builder{
					Object: privatev1.HostType_builder{
						Id: object.GetId(),
						Metadata: privatev1.Metadata_builder{
							Labels: map[string]string{
								key: value,
							},
						}.Build(),
						Title:       "My title",
						Description: "My description.",
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok = grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(Equal(expected))
			},
			Entry(
				"Invalid label name character",
				"bad^name",
				"value",
				"field 'metadata.labels' key 'bad^name' name must only contain lowercase letters (a-z), "+
					"digits (0-9), hyphens (-), underscores (_) or dots (.), but contains '^' at position 3",
			),
			Entry(
				"Invalid label prefix character",
				"bad_prefix/name",
				"value",
				"field 'metadata.labels' key 'bad_prefix/name' prefix segment must only contain lowercase "+
					"letters (a-z), digits (0-9) and hyphens (-), but contains '_' at position 3",
			),
			Entry(
				"Invalid label value character",
				"good",
				"bad@value",
				"field 'metadata.labels' key 'good' value must only contain lowercase letters (a-z), "+
					"digits (0-9), hyphens (-), underscores (_) or dots (.), but contains '@' at position 3",
			),
		)

		DescribeTable(
			"Rejects invalid annotations on create and update",
			func(key string, value string, expected string) {
				_, err := server.Create(ctx, privatev1.HostTypesCreateRequest_builder{
					Object: privatev1.HostType_builder{
						Metadata: privatev1.Metadata_builder{
							Annotations: map[string]string{
								key: value,
							},
						}.Build(),
						Title:       "My title",
						Description: "My description.",
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok := grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(Equal(expected))

				createResponse, err := server.Create(ctx, privatev1.HostTypesCreateRequest_builder{
					Object: privatev1.HostType_builder{
						Title:       "My title",
						Description: "My description.",
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				object := createResponse.GetObject()

				Expect(err).ToNot(HaveOccurred())
				_, err = server.Update(ctx, privatev1.HostTypesUpdateRequest_builder{
					Object: privatev1.HostType_builder{
						Id: object.GetId(),
						Metadata: privatev1.Metadata_builder{
							Annotations: map[string]string{
								key: value,
							},
						}.Build(),
						Title:       "My title",
						Description: "My description.",
					}.Build(),
				}.Build())
				Expect(err).To(HaveOccurred())
				status, ok = grpcstatus.FromError(err)
				Expect(ok).To(BeTrue())
				Expect(status.Code()).To(Equal(grpccodes.InvalidArgument))
				Expect(status.Message()).To(Equal(expected))
			},
			Entry(
				"Invalid annotation name character",
				"bad^annotation",
				"value",
				"field 'metadata.annotations' key 'bad^annotation' name must only contain lowercase letters "+
					"(a-z), digits (0-9), hyphens (-), underscores (_) or dots (.), but contains '^' at position 3",
			),
			Entry(
				"Invalid annotation prefix character",
				"bad_prefix/annotation",
				"value",
				"field 'metadata.annotations' key 'bad_prefix/annotation' prefix segment must only contain "+
					"lowercase letters (a-z), digits (0-9) and hyphens (-), but contains '_' at position 3",
			),
		)

		It("Delete object", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, privatev1.HostTypesCreateRequest_builder{
				Object: privatev1.HostType_builder{
					Metadata: privatev1.Metadata_builder{
						Finalizers: []string{"a"},
					}.Build(),
					Title:       "My title",
					Description: "My description.",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Delete the object:
			_, err = server.Delete(ctx, privatev1.HostTypesDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Get and verify:
			getResponse, err := server.Get(ctx, privatev1.HostTypesGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
		})
	})
})
