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

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
)

var _ = Describe("Bare metal instance templates server", func() {
	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewBareMetalInstanceTemplatesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewBareMetalInstanceTemplatesServer().
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})

		It("Fails if tenancy logic is not set", func() {
			server, err := NewBareMetalInstanceTemplatesServer().
				SetLogger(logger).
				Build()
			Expect(err).To(MatchError("tenancy logic is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		var (
			server        *BareMetalInstanceTemplatesServer
			privateServer *PrivateBareMetalInstanceTemplatesServer
		)

		BeforeEach(func() {
			var err error
			server, err = NewBareMetalInstanceTemplatesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())

			privateServer, err = NewPrivateBareMetalInstanceTemplatesServer().
				SetLogger(logger).
				SetAttributionLogic(attribution).
				SetTenancyLogic(tenancy).
				Build()
			Expect(err).ToNot(HaveOccurred())
		})

		It("Lists objects", func() {
			const count = 3
			for i := range count {
				_, err := privateServer.Create(ctx, privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
					Object: privatev1.BareMetalInstanceTemplate_builder{
						Title: fmt.Sprintf("Template %d", i),
					}.Build(),
				}.Build())
				Expect(err).ToNot(HaveOccurred())
			}

			response, err := server.List(ctx, publicv1.BareMetalInstanceTemplatesListRequest_builder{}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(count))
		})

		It("Gets object", func() {
			createResponse, err := privateServer.Create(ctx, privatev1.BareMetalInstanceTemplatesCreateRequest_builder{
				Object: privatev1.BareMetalInstanceTemplate_builder{
					Title:       "My template",
					Description: "A test template.",
				}.Build(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			id := createResponse.GetObject().GetId()

			response, err := server.Get(ctx, publicv1.BareMetalInstanceTemplatesGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetId()).To(Equal(id))
			Expect(response.GetObject().GetTitle()).To(Equal("My template"))
			Expect(response.GetObject().GetDescription()).To(Equal("A test template."))
		})
	})
})
