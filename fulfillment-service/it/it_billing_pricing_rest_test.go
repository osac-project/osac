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
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// The two pricing REST route groups this epic registers carry no route-level coverage elsewhere: the gateway
// package's unit suite asserts handler-list membership only. These specs drive the deployed gateway's HTTP boundary
// end to end - route resolution, method mapping, the interceptor chain, the handler - and pin that the REST result
// matches the gRPC path's.
var _ = Describe("Billing pricing REST routes", Label("billing"), func() {
	It("lists the catalog pricing inputs over REST identically to the gRPC path", func(ctx context.Context) {
		// Seed one published item offering any instance type, so the spec asserts a real aggregated row through the
		// deployed routes instead of passing on an empty catalog.
		template := createCatalogItemComputeInstanceTemplateFixture(ctx, nil, nil)
		createComputeInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(),
			publicv1.ComputeInstanceCatalogItem_builder{
				Metadata:  publicv1.Metadata_builder{Name: catalogItemFixtureName()}.Build(),
				Template:  publicv1.ComputeInstanceTemplateReference_builder{Id: template}.Build(),
				Published: true,
				Fields: publicv1.ComputeInstanceCatalogItemFields_builder{
					InstanceType: publicv1.InstanceTypeReferenceFieldPolicy_builder{
						Editable: publicv1.EditableInstanceTypeReferenceField_builder{}.Build(),
					}.Build(),
				}.Build(),
			}.Build())

		grpcList, err := publicv1.NewCatalogPricingInputsClient(tool.ExternalView().AdminConn()).List(ctx,
			publicv1.CatalogPricingInputsListRequest_builder{}.Build())
		Expect(err).NotTo(HaveOccurred())
		Expect(grpcList.GetTotal()).To(BeNumerically(">", 0), "the seeded item must be aggregated")

		By("reading the public REST route through the external ingress")
		body, statusCode, err := getBillingPricingREST(ctx, tool.ExternalView().AdminClient(),
			"/api/fulfillment/v1/billing/catalog_pricing_inputs")
		Expect(err).NotTo(HaveOccurred())
		Expect(statusCode).To(Equal(http.StatusOK))
		expectCatalogPricingInputsRESTParity(body, grpcList.GetTotal())

		By("reading the private REST route through the internal ingress")
		body, statusCode, err = getBillingPricingREST(ctx, tool.InternalView().AdminClient(),
			"/api/private/v1/billing/catalog_pricing_inputs")
		Expect(err).NotTo(HaveOccurred())
		Expect(statusCode).To(Equal(http.StatusOK))
		expectCatalogPricingInputsRESTParity(body, grpcList.GetTotal())
	})

	It("surfaces the billing-not-configured rejection over the provider rate card items REST routes", func(ctx context.Context) {
		// The dev deployment configures no billing provider, so the disabled mode exercises both routes end to end.
		_, err := publicv1.NewProviderRateCardItemsClient(tool.ExternalView().AdminConn()).List(ctx,
			publicv1.ProviderRateCardItemsListRequest_builder{}.Build())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("billing is not configured"), "the gRPC path sets the expectation")

		By("reading the public REST route through the external ingress")
		body, statusCode, err := getBillingPricingREST(ctx, tool.ExternalView().AdminClient(),
			"/api/fulfillment/v1/billing/provider_rate_card_items")
		Expect(err).NotTo(HaveOccurred())
		Expect(statusCode).To(Equal(http.StatusBadRequest), "grpc-gateway maps FAILED_PRECONDITION to 400")
		Expect(string(body)).To(ContainSubstring("billing is not configured"))

		By("reading the private REST route through the internal ingress")
		body, statusCode, err = getBillingPricingREST(ctx, tool.InternalView().AdminClient(),
			"/api/private/v1/billing/provider_rate_card_items")
		Expect(err).NotTo(HaveOccurred())
		Expect(statusCode).To(Equal(http.StatusBadRequest), "grpc-gateway maps FAILED_PRECONDITION to 400")
		Expect(string(body)).To(ContainSubstring("billing is not configured"))
	})
})

// getBillingPricingREST performs an authenticated GET against one of the deployed gateway's billing routes.
func getBillingPricingREST(ctx context.Context, client *http.Client, target string) ([]byte, int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build billing pricing REST request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, 0, fmt.Errorf("send billing pricing REST request: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, response.StatusCode, fmt.Errorf("read billing pricing REST response: %w", err)
	}
	return body, response.StatusCode, nil
}

// expectCatalogPricingInputsRESTParity asserts a catalog pricing inputs REST body carries the same inventory the
// gRPC path returned, including the seeded compute_instance/instance_type row.
func expectCatalogPricingInputsRESTParity(body []byte, grpcTotal int32) {
	var restList struct {
		Items []map[string]any `json:"items"`
		Total int32            `json:"total"`
	}
	Expect(json.Unmarshal(body, &restList)).To(Succeed())
	Expect(restList.Total).To(Equal(grpcTotal), "REST and gRPC must agree on the inventory")
	found := false
	for _, item := range restList.Items {
		Expect(item).To(HaveKey("service"))
		Expect(item).To(HaveKey("dimension"))
		if item["service"] == "compute_instance" && item["dimension"] == "instance_type" {
			found = true
		}
	}
	Expect(found).To(BeTrue(), "the seeded catalog item's instance_type row is missing")
}
