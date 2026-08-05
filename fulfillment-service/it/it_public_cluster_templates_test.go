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

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/fulfillment-service/internal/uuid"
)

var _ = Describe("Cluster templates", func() {
	var (
		ctx    context.Context
		client publicv1.ClusterTemplatesClient
	)

	BeforeEach(func() {
		ctx = context.Background()
		client = publicv1.NewClusterTemplatesClient(tool.ExternalView().UserConn())
	})

	It("Can get the list of templates", func() {
		// Create a template via the private API:
		adminClient := privatev1.NewClusterTemplatesClient(tool.InternalView().AdminConn())
		id := fmt.Sprintf("my_template_%s", uuid.New())
		_, err := adminClient.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
			Object: privatev1.ClusterTemplate_builder{
				Id:          id,
				Title:       "My title",
				Description: "My description.",
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		listResponse, err := client.List(ctx, publicv1.ClusterTemplatesListRequest_builder{}.Build())
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
		// Create a template via the private API:
		adminClient := privatev1.NewClusterTemplatesClient(tool.InternalView().AdminConn())
		id := fmt.Sprintf("my_template_%s", uuid.New())
		_, err := adminClient.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
			Object: privatev1.ClusterTemplate_builder{
				Id:          id,
				Title:       "My title",
				Description: "My description.",
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Get the template and verify that the returned object is correct:
		response, err := client.Get(ctx, publicv1.ClusterTemplatesGetRequest_builder{
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
	})
})
