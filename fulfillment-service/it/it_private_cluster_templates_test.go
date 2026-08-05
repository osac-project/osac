/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/uuid"
)

var _ = Describe("Private cluster templates", func() {
	var (
		ctx    context.Context
		client privatev1.ClusterTemplatesClient
	)

	BeforeEach(func() {
		ctx = context.Background()
		client = privatev1.NewClusterTemplatesClient(tool.InternalView().AdminConn())
	})

	It("Can get the list of templates", func() {
		// Create a template:
		id := fmt.Sprintf("my_template_%s", uuid.New())
		_, err := client.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
			Object: privatev1.ClusterTemplate_builder{
				Id:          id,
				Title:       "My title",
				Description: "My description.",
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		listResponse, err := client.List(ctx, privatev1.ClusterTemplatesListRequest_builder{}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(listResponse).ToNot(BeNil())
		items := listResponse.GetItems()
		Expect(items).ToNot(BeEmpty())
		ids := make([]string, len(items))
		for i, item := range items {
			ids[i] = item.GetId()
		}
		Expect(ids).To(ContainElement(id))
	})

	It("Can get a specific template", func() {
		// Create the template:
		id := fmt.Sprintf("my_template_%s", uuid.New())
		_, err := client.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
			Object: privatev1.ClusterTemplate_builder{
				Id:          id,
				Title:       "My title",
				Description: "My description.",
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Get the template and verify that the returned object is correct:
		response, err := client.Get(ctx, privatev1.ClusterTemplatesGetRequest_builder{
			Id: id,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		object := response.GetObject()
		Expect(object).ToNot(BeNil())
		Expect(object.GetId()).To(Equal(id))
		metadata := object.GetMetadata()
		Expect(metadata).ToNot(BeNil())
		Expect(metadata.HasCreationTimestamp()).To(BeTrue())
		Expect(metadata.HasDeletionTimestamp()).To(BeFalse())
		Expect(object.GetTitle()).To(Equal("My title"))
		Expect(object.GetDescription()).To(Equal("My description."))
	})

	It("Can create a template", func() {
		id := fmt.Sprintf("my_template_%s", uuid.New())
		response, err := client.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
			Object: privatev1.ClusterTemplate_builder{
				Id:          id,
				Title:       "My title",
				Description: "My description.",
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		object := response.GetObject()
		Expect(object).ToNot(BeNil())
		Expect(object.GetId()).To(Equal(id))
		metadata := object.GetMetadata()
		Expect(metadata).ToNot(BeNil())
		Expect(metadata.HasCreationTimestamp()).To(BeTrue())
		Expect(metadata.HasDeletionTimestamp()).To(BeFalse())
		Expect(object.GetTitle()).To(Equal("My title"))
		Expect(object.GetDescription()).To(Equal("My description."))
	})

	It("Can update a template", func() {
		// Create a template::
		id := fmt.Sprintf("my_template_%s", uuid.New())
		_, err := client.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
			Object: privatev1.ClusterTemplate_builder{
				Id:          id,
				Title:       "My title",
				Description: "My description.",
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Update it and verify that the returned object is correct:
		updateResponse, err := client.Update(ctx, privatev1.ClusterTemplatesUpdateRequest_builder{
			Object: privatev1.ClusterTemplate_builder{
				Id:          id,
				Title:       "My updated title",
				Description: "My updated description.",
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(updateResponse).ToNot(BeNil())
		object := updateResponse.GetObject()
		Expect(object).ToNot(BeNil())
		Expect(object.GetId()).To(Equal(id))
		metadata := object.GetMetadata()
		Expect(metadata).ToNot(BeNil())
		Expect(metadata.HasCreationTimestamp()).To(BeTrue())
		Expect(metadata.HasDeletionTimestamp()).To(BeFalse())
		Expect(object.GetTitle()).To(Equal("My updated title"))
		Expect(object.GetDescription()).To(Equal("My updated description."))

		// Get the template and verify that the returned object is correct:
		getResponse, err := client.Get(ctx, privatev1.ClusterTemplatesGetRequest_builder{
			Id: id,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(getResponse).ToNot(BeNil())
		object = getResponse.GetObject()
		Expect(object).ToNot(BeNil())
		Expect(object.GetId()).To(Equal(id))
		metadata = object.GetMetadata()
		Expect(metadata).ToNot(BeNil())
		Expect(metadata.HasCreationTimestamp()).To(BeTrue())
		Expect(metadata.HasDeletionTimestamp()).To(BeFalse())
		Expect(object.GetTitle()).To(Equal("My updated title"))
		Expect(object.GetDescription()).To(Equal("My updated description."))
	})

	It("Can delete a template", func() {
		// Create a template::
		id := fmt.Sprintf("my_template_%s", uuid.New())
		_, err := client.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
			Object: privatev1.ClusterTemplate_builder{
				Id:          id,
				Title:       "My title",
				Description: "My description.",
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Delete it:
		deleteResponse, err := client.Delete(ctx, privatev1.ClusterTemplatesDeleteRequest_builder{
			Id: id,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(deleteResponse).ToNot(BeNil())

		// Trying to get the deleted object should either fail if the object has been completely deleted and
		// archived, or return an object that has the deletion timestamp set.
		getResponse, err := client.Get(ctx, privatev1.ClusterTemplatesGetRequest_builder{
			Id: id,
		}.Build())
		if err != nil {
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status).ToNot(BeNil())
			Expect(status.Code()).To(Equal(grpccodes.NotFound))
			Expect(status.Message()).To(ContainSubstring(id))
		} else {
			Expect(getResponse).ToNot(BeNil())
			object := getResponse.GetObject()
			Expect(object).ToNot(BeNil())
			metadata := object.GetMetadata()
			Expect(metadata).ToNot(BeNil())
			Expect(metadata.HasDeletionTimestamp()).To(BeTrue())
		}
	})
})
