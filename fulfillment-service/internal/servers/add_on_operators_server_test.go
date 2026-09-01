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

	privatev1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
)

var _ = Describe("Add-on operators server", func() {
	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewAddOnOperatorsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewAddOnOperatorsServer().
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewAddOnOperatorsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Published filtering", func() {
		var publicServer *AddOnOperatorsServer
		var privateServer *PrivateAddOnOperatorsServer

		BeforeEach(func() {
			var err error

			privateServer, err = NewPrivateAddOnOperatorsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			publicServer, err = NewAddOnOperatorsServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("List returns only published operators", func() {
			// Create a published operator:
			_, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("published-%s", uuid.New()[24:32]),
					}.Build(),
					Title:     "Published Operator",
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// Create an unpublished operator:
			_, err = privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("unpublished-%s", uuid.New()[24:32]),
					}.Build(),
					Title: "Unpublished Operator",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// List via public server — should only see published:
			response, err := publicServer.List(ctx, publicv1.AddOnOperatorsListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			for _, item := range response.GetItems() {
				Expect(item.GetPublished()).To(BeTrue())
			}
		})

		It("Get returns published operator", func() {
			createResponse, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("published-%s", uuid.New()[24:32]),
					}.Build(),
					Title:     "Published Operator",
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			getResponse, err := publicServer.Get(ctx, publicv1.AddOnOperatorsGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse.GetObject().GetTitle()).To(Equal("Published Operator"))
		})

		It("Get returns NotFound for unpublished operator", func() {
			createResponse, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("unpublished-%s", uuid.New()[24:32]),
					}.Build(),
					Title: "Unpublished Operator",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			_, err = publicServer.Get(ctx, publicv1.AddOnOperatorsGetRequest_builder{
				Id: createResponse.GetObject().GetId(),
			}.Build())
			Expect(err).To(HaveOccurred())
			status, ok := grpcstatus.FromError(err)
			Expect(ok).To(BeTrue())
			Expect(status.Code()).To(Equal(grpccodes.NotFound))
		})

		It("List with filter composes correctly with published filter", func() {
			// Create a published operator with a specific title:
			_, err := privateServer.Create(ctx, privatev1.AddOnOperatorsCreateRequest_builder{
				Object: privatev1.AddOnOperator_builder{
					Metadata: privatev1.Metadata_builder{
						Name: fmt.Sprintf("gpu-%s", uuid.New()[24:32]),
					}.Build(),
					Title:     "GPU Operator",
					Published: true,
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())

			// List with a title filter:
			response, err := publicServer.List(ctx, publicv1.AddOnOperatorsListRequest_builder{
				Filter: new("this.title == 'GPU Operator'"),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).ToNot(BeEmpty())
			Expect(response.GetItems()[0].GetTitle()).To(Equal("GPU Operator"))
		})
	})
})
