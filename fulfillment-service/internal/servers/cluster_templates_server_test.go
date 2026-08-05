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
	"google.golang.org/protobuf/proto"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/database"
)

var _ = Describe("Cluster templates server", func() {
	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewClusterTemplatesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewClusterTemplatesServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if attribution logic is not set", func() {
			server, err := NewClusterTemplatesServer().
				SetLogger(logger).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("attribution logic is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewClusterTemplatesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var server *ClusterTemplatesServer

		BeforeEach(func() {
			var err error

			// Create the server:
			server, err = NewClusterTemplatesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Creates object", func() {
			response, err := server.Create(ctx, publicv1.ClusterTemplatesCreateRequest_builder{
				Object: publicv1.ClusterTemplate_builder{
					Id:          "my_template",
					Title:       "My template",
					Description: "My template is *nice*",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).To(Equal("my_template"))
		})

		It("List objects", func() {
			// Create a few objects:
			const count = 10
			for i := range count {
				_, err := server.Create(ctx, publicv1.ClusterTemplatesCreateRequest_builder{
					Object: publicv1.ClusterTemplate_builder{
						Id:          fmt.Sprintf("my_template_%d", i),
						Title:       fmt.Sprintf("My template %d", i),
						Description: fmt.Sprintf("My template is *nice* %d", i),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects:
			response, err := server.List(ctx, publicv1.ClusterTemplatesListRequest_builder{
				Filter: new("this.id.startsWith('my_template_')"),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response).ToNot(BeNil())
			items := response.GetItems()
			Expect(items).To(HaveLen(count))
		})

		It("List objects with limit", func() {
			// Create a few objects:
			const count = 10
			for i := range count {
				_, err := server.Create(ctx, publicv1.ClusterTemplatesCreateRequest_builder{
					Object: publicv1.ClusterTemplate_builder{
						Id:          fmt.Sprintf("my_template_%d", i),
						Title:       fmt.Sprintf("My template %d", i),
						Description: fmt.Sprintf("My template is *nice* %d", i),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects:
			response, err := server.List(ctx, publicv1.ClusterTemplatesListRequest_builder{
				Limit: new(int32(1)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", 1))
		})

		It("List objects with offset", func() {
			// Create a few objects:
			const count = 10
			for i := range count {
				_, err := server.Create(ctx, publicv1.ClusterTemplatesCreateRequest_builder{
					Object: publicv1.ClusterTemplate_builder{
						Id:          fmt.Sprintf("my_template_%d", i),
						Title:       fmt.Sprintf("My template %d", i),
						Description: fmt.Sprintf("My template is *nice* %d", i),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			// List the objects:
			response, err := server.List(ctx, publicv1.ClusterTemplatesListRequest_builder{
				Filter: new("this.id.startsWith('my_template_')"),
				Offset: new(int32(1)),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetSize()).To(BeNumerically("==", count-1))
		})

		It("List objects with filter", func() {
			// Create a few objects:
			const count = 10
			var objects []*publicv1.ClusterTemplate
			for i := range count {
				response, err := server.Create(ctx, publicv1.ClusterTemplatesCreateRequest_builder{
					Object: publicv1.ClusterTemplate_builder{
						Id:          fmt.Sprintf("my_template_%d", i),
						Title:       fmt.Sprintf("My template %d", i),
						Description: fmt.Sprintf("My template is *nice* %d", i),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				objects = append(objects, response.GetObject())
			}

			// List the objects:
			for _, object := range objects {
				response, err := server.List(ctx, publicv1.ClusterTemplatesListRequest_builder{
					Filter: new(fmt.Sprintf("this.id == '%s'", object.GetId())),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
				Expect(response.GetSize()).To(BeNumerically("==", 1))
				Expect(response.GetItems()[0].GetId()).To(Equal(object.GetId()))
			}
		})

		It("Get object", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, publicv1.ClusterTemplatesCreateRequest_builder{
				Object: publicv1.ClusterTemplate_builder{
					Id:          "my_template",
					Title:       "My template",
					Description: "My template is *nice*",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Get it:
			getResponse, err := server.Get(ctx, publicv1.ClusterTemplatesGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(proto.Equal(createResponse.GetObject(), getResponse.GetObject())).To(BeTrue())
		})

		It("Update object", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, publicv1.ClusterTemplatesCreateRequest_builder{
				Object: publicv1.ClusterTemplate_builder{
					Id:          "my_template",
					Title:       "My template",
					Description: "My template is *nice*",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := createResponse.GetObject()

			// Update the object:
			updateResponse, err := server.Update(ctx, publicv1.ClusterTemplatesUpdateRequest_builder{
				Object: publicv1.ClusterTemplate_builder{
					Id:          object.GetId(),
					Title:       "Still my template",
					Description: "But now it is _ugly_",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(updateResponse.GetObject().GetTitle()).To(Equal("Still my template"))
			Expect(updateResponse.GetObject().GetDescription()).To(Equal("But now it is _ugly_"))

			// Get and verify:
			getResponse, err := server.Get(ctx, publicv1.ClusterTemplatesGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetTitle()).To(Equal("Still my template"))
			Expect(getResponse.GetObject().GetDescription()).To(Equal("But now it is _ugly_"))
		})

		It("Delete object", func() {
			// Create the object:
			createResponse, err := server.Create(ctx, publicv1.ClusterTemplatesCreateRequest_builder{
				Object: publicv1.ClusterTemplate_builder{
					Id:          "my_template",
					Title:       "My template",
					Description: "My template is *nice*",
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
				`update cluster_templates set finalizers = '{"a"}' where id = $1`,
				object.GetId(),
			)
			Expect(err).ToNot(HaveOccurred())

			// Delete the object:
			_, err = server.Delete(ctx, publicv1.ClusterTemplatesDeleteRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Get and verify:
			getResponse, err := server.Get(ctx, publicv1.ClusterTemplatesGetRequest_builder{
				Id: object.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object = getResponse.GetObject()
			Expect(object.GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
		})
	})
})
