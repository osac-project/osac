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
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/fulfillment-service/internal/services"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Capabilities server", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	Describe("Creation", func() {
		It("Can be built if all the required parameters are set", func() {
			server, err := NewCapabilitiesServer().
				SetLogger(logger).
				AddAutnTrustedTokenIssuers("https://my-issuer.com").
				Build()
			Expect(err).ToNot(HaveOccurred())
			Expect(server).ToNot(BeNil())
		})

		It("Fails if logger is not set", func() {
			server, err := NewCapabilitiesServer().
				AddAutnTrustedTokenIssuers("https://my-issuer.com").
				Build()
			Expect(err).To(HaveOccurred())
			Expect(err).To(MatchError("logger is mandatory"))
			Expect(server).To(BeNil())
		})
	})

	Describe("Behaviour", func() {
		DescribeTable("returns enabled services for public and private APIs",
			func(flags services.Flags, expectedPublic []publicv1.ServiceTier, expectedPrivate []privatev1.ServiceTier) {
				publicServer, err := NewCapabilitiesServer().
					SetLogger(logger).
					SetServiceFlags(&flags).
					Build()
				Expect(err).ToNot(HaveOccurred())

				publicResponse, err := publicServer.Get(ctx, &publicv1.CapabilitiesGetRequest{})
				Expect(err).ToNot(HaveOccurred())
				Expect(publicResponse.GetEnabledServices()).To(Equal(expectedPublic))

				privateServer, err := NewPrivateCapabilitiesServer().
					SetLogger(logger).
					SetServiceFlags(&flags).
					Build()
				Expect(err).ToNot(HaveOccurred())

				privateResponse, err := privateServer.Get(ctx, &privatev1.CapabilitiesGetRequest{})
				Expect(err).ToNot(HaveOccurred())
				Expect(privateResponse.GetEnabledServices()).To(Equal(expectedPrivate))
			},
			Entry("selective services", services.Flags{CaaS: true, VMaaS: true},
				[]publicv1.ServiceTier{
					publicv1.ServiceTier_SERVICE_TIER_CAAS,
					publicv1.ServiceTier_SERVICE_TIER_VMAAS,
				},
				[]privatev1.ServiceTier{
					privatev1.ServiceTier_SERVICE_TIER_CAAS,
					privatev1.ServiceTier_SERVICE_TIER_VMAAS,
				},
			),
			Entry("all services", services.Flags{CaaS: true, VMaaS: true, BMaaS: true, MaaS: true},
				[]publicv1.ServiceTier{
					publicv1.ServiceTier_SERVICE_TIER_CAAS,
					publicv1.ServiceTier_SERVICE_TIER_VMAAS,
					publicv1.ServiceTier_SERVICE_TIER_BMAAS,
					publicv1.ServiceTier_SERVICE_TIER_MAAS,
				},
				[]privatev1.ServiceTier{
					privatev1.ServiceTier_SERVICE_TIER_CAAS,
					privatev1.ServiceTier_SERVICE_TIER_VMAAS,
					privatev1.ServiceTier_SERVICE_TIER_BMAAS,
					privatev1.ServiceTier_SERVICE_TIER_MAAS,
				},
			),
		)

		It("Returns no token issuer", func() {
			server, err := NewCapabilitiesServer().
				SetLogger(logger).
				Build()
			Expect(err).ToNot(HaveOccurred())
			response, err := server.Get(ctx, &publicv1.CapabilitiesGetRequest{})
			Expect(err).ToNot(HaveOccurred())
			issuers := response.GetAuthn().GetTrustedTokenIssuers()
			Expect(issuers).To(BeEmpty())
		})

		It("Returns one token issuer", func() {
			server, err := NewCapabilitiesServer().
				SetLogger(logger).
				AddAutnTrustedTokenIssuers("https://my-issuer.com").
				Build()
			Expect(err).ToNot(HaveOccurred())
			response, err := server.Get(ctx, &publicv1.CapabilitiesGetRequest{})
			Expect(err).ToNot(HaveOccurred())
			issuers := response.GetAuthn().GetTrustedTokenIssuers()
			Expect(issuers).To(Equal([]string{"https://my-issuer.com"}))
		})

		It("Returns two token issuers", func() {
			server, err := NewCapabilitiesServer().
				SetLogger(logger).
				AddAutnTrustedTokenIssuers(
					"https://my-issuer.com",
					"https://your-issuer.com",
				).
				Build()
			Expect(err).ToNot(HaveOccurred())
			response, err := server.Get(ctx, &publicv1.CapabilitiesGetRequest{})
			Expect(err).ToNot(HaveOccurred())
			issuers := response.GetAuthn().GetTrustedTokenIssuers()
			Expect(issuers).To(HaveExactElements(
				"https://my-issuer.com",
				"https://your-issuer.com",
			))
		})

		It("Sorts the token issuers", func() {
			server, err := NewCapabilitiesServer().
				SetLogger(logger).
				AddAutnTrustedTokenIssuers(
					"https://your-issuer.com",
					"https://my-issuer.com",
				).
				Build()
			Expect(err).ToNot(HaveOccurred())
			response, err := server.Get(ctx, &publicv1.CapabilitiesGetRequest{})
			Expect(err).ToNot(HaveOccurred())
			issuers := response.GetAuthn().GetTrustedTokenIssuers()
			Expect(issuers).To(HaveExactElements(
				"https://my-issuer.com",
				"https://your-issuer.com",
			))
		})

		It("Removes duplicates from the token issuers", func() {
			server, err := NewCapabilitiesServer().
				SetLogger(logger).
				AddAutnTrustedTokenIssuers(
					"https://my-issuer.com",
					"https://your-issuer.com",
					"https://my-issuer.com",
				).
				Build()
			Expect(err).ToNot(HaveOccurred())
			response, err := server.Get(ctx, &publicv1.CapabilitiesGetRequest{})
			Expect(err).ToNot(HaveOccurred())
			issuers := response.GetAuthn().GetTrustedTokenIssuers()
			Expect(issuers).To(HaveExactElements(
				"https://my-issuer.com",
				"https://your-issuer.com",
			))
		})
	})
})
