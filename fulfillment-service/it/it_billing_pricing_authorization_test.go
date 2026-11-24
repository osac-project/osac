/*
Copyright (c) 2026 Red Hat Inc.

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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// Use the harness's authenticated regular tenant user for all four method paths. Valid requests ensure these
// assertions prove authorization denial, not request validation or the dev deployment's disabled billing guard.
var _ = Describe("Billing pricing authorization", Label("billing"), func() {
	DescribeTable("denies regular users access to admin-only pricing Lists",
		func(ctx context.Context, list func(context.Context) error) {
			err := list(ctx)
			Expect(err).To(HaveOccurred())
			Expect(status.Code(err)).To(Equal(codes.PermissionDenied))
		},
		Entry("public CatalogPricingInputs.List", func(ctx context.Context) error {
			_, err := publicv1.NewCatalogPricingInputsClient(tool.ExternalView().UserConn()).List(ctx,
				publicv1.CatalogPricingInputsListRequest_builder{}.Build())
			return err
		}),
		Entry("private CatalogPricingInputs.List", func(ctx context.Context) error {
			_, err := privatev1.NewCatalogPricingInputsClient(tool.InternalView().UserConn()).List(ctx,
				privatev1.CatalogPricingInputsListRequest_builder{}.Build())
			return err
		}),
		Entry("public ProviderRateCardItems.List", func(ctx context.Context) error {
			_, err := publicv1.NewProviderRateCardItemsClient(tool.ExternalView().UserConn()).List(ctx,
				publicv1.ProviderRateCardItemsListRequest_builder{CurrencyCode: new("USD")}.Build())
			return err
		}),
		Entry("private ProviderRateCardItems.List", func(ctx context.Context) error {
			_, err := privatev1.NewProviderRateCardItemsClient(tool.InternalView().UserConn()).List(ctx,
				privatev1.ProviderRateCardItemsListRequest_builder{CurrencyCode: new("USD")}.Build())
			return err
		}),
	)
})
