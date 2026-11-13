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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

// The two pricing REST route groups this epic registers carry no route-level coverage elsewhere: the gateway
// package's unit suite asserts handler-list membership only. These specs drive the deployed gateway's HTTP boundary
// end to end - route resolution, method mapping, the interceptor chain, the handler - and pin that the REST result
// matches the gRPC path's.
var _ = Describe("Billing pricing REST routes", Label("billing"), Serial, func() {
	It("lists the catalog pricing inputs over REST identically to the gRPC path", func(ctx context.Context) {
		// The deployed server reuses one catalog inventory sweep until the cache TTL elapses, so a read right after
		// seeding can observe a snapshot from before the item existed. Shorten that TTL on the deployed Deployment
		// first, as the CLI spec does, so the bounded poll below spans several refreshes.
		catalogPricingShortenInventoryTTL(ctx)

		// Seed one published item offering any instance type, so the spec asserts a real aggregated row through the
		// deployed routes instead of passing on an empty catalog.
		template := createCatalogItemComputeInstanceTemplateFixture(ctx, nil, nil)
		item := createComputeInstanceCatalogItemFixture(ctx, tool.ExternalView().AdminConn(),
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
		itemID := item.GetId()

		// The patched cache expires after four seconds, so the 20s poll spans several refreshes with margin for a
		// slow one. The gRPC read, both REST reads and the parity comparison all sit inside the poll: a stale
		// snapshot retries instead of failing, and both paths must show the seeded item before the spec passes.
		By("polling the gRPC path and both billing pricing REST routes for the seeded item")
		Eventually(ctx, func(g Gomega) {
			// Bound all three reads together to 3s so a hung request cannot stall polling; with the 2s interval,
			// several attempts still fit in the 20s budget even when each attempt exhausts its deadline.
			attemptCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()

			grpcList, err := publicv1.NewCatalogPricingInputsClient(tool.ExternalView().AdminConn()).List(attemptCtx,
				publicv1.CatalogPricingInputsListRequest_builder{}.Build())
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(grpcList.GetTotal()).To(BeNumerically(">", 0), "the seeded item must be aggregated")
			g.Expect(catalogPricingGRPCInputRowListsItem(grpcList, itemID)).To(BeTrue(),
				"the gRPC instance_type row does not list the seeded catalog item")

			body, statusCode, err := getBillingPricingREST(attemptCtx, tool.ExternalView().AdminClient(),
				"/api/fulfillment/v1/billing/catalog_pricing_inputs")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(statusCode).To(Equal(http.StatusOK))
			expectCatalogPricingInputsRESTParity(g, body, grpcList.GetTotal(), itemID)

			body, statusCode, err = getBillingPricingREST(attemptCtx, tool.InternalView().AdminClient(),
				"/api/private/v1/billing/catalog_pricing_inputs")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(statusCode).To(Equal(http.StatusOK))
			expectCatalogPricingInputsRESTParity(g, body, grpcList.GetTotal(), itemID)
		}, 20*time.Second, 2*time.Second).Should(Succeed())
	})

	It("surfaces the billing-not-configured rejection over the provider rate card items REST routes", func(ctx context.Context) {
		// The dev deployment configures no billing provider, so the disabled mode exercises both routes end to end.
		_, err := publicv1.NewProviderRateCardItemsClient(tool.ExternalView().AdminConn()).List(ctx,
			publicv1.ProviderRateCardItemsListRequest_builder{CurrencyCode: new("USD")}.Build())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("billing is not configured"), "the gRPC path sets the expectation")

		By("reading the public REST route through the external ingress")
		body, statusCode, err := getBillingPricingREST(ctx, tool.ExternalView().AdminClient(),
			"/api/fulfillment/v1/billing/provider_rate_card_items?currency_code=USD")
		Expect(err).NotTo(HaveOccurred())
		Expect(statusCode).To(Equal(http.StatusBadRequest), "grpc-gateway maps FAILED_PRECONDITION to 400")
		Expect(string(body)).To(ContainSubstring("billing is not configured"))

		By("reading the private REST route through the internal ingress")
		body, statusCode, err = getBillingPricingREST(ctx, tool.InternalView().AdminClient(),
			"/api/private/v1/billing/provider_rate_card_items?currency_code=USD")
		Expect(err).NotTo(HaveOccurred())
		Expect(statusCode).To(Equal(http.StatusBadRequest), "grpc-gateway maps FAILED_PRECONDITION to 400")
		Expect(string(body)).To(ContainSubstring("billing is not configured"))
	})

	It("rejects a provider rate card items REST request without a currency code", func(ctx context.Context) {
		_, err := publicv1.NewProviderRateCardItemsClient(tool.ExternalView().AdminConn()).List(ctx,
			publicv1.ProviderRateCardItemsListRequest_builder{}.Build())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument), "the gRPC path sets the expectation")
		Expect(err.Error()).To(ContainSubstring("currency_code"))

		By("reading the public REST route through the external ingress")
		body, statusCode, err := getBillingPricingREST(ctx, tool.ExternalView().AdminClient(),
			"/api/fulfillment/v1/billing/provider_rate_card_items")
		Expect(err).NotTo(HaveOccurred())
		Expect(statusCode).To(Equal(http.StatusBadRequest), "grpc-gateway maps INVALID_ARGUMENT to 400")
		Expect(string(body)).To(ContainSubstring("currency_code"))

		By("reading the private REST route through the internal ingress")
		body, statusCode, err = getBillingPricingREST(ctx, tool.InternalView().AdminClient(),
			"/api/private/v1/billing/provider_rate_card_items")
		Expect(err).NotTo(HaveOccurred())
		Expect(statusCode).To(Equal(http.StatusBadRequest), "grpc-gateway maps INVALID_ARGUMENT to 400")
		Expect(string(body)).To(ContainSubstring("currency_code"))
	})

	It("rejects a tenant-scoped provider rate card items REST request as not implemented", func(ctx context.Context) {
		_, err := publicv1.NewProviderRateCardItemsClient(tool.ExternalView().AdminConn()).List(ctx,
			publicv1.ProviderRateCardItemsListRequest_builder{
				CurrencyCode: new("USD"),
				TenantId:     new("tenant-a"),
			}.Build())
		Expect(status.Code(err)).To(Equal(codes.Unimplemented), "the gRPC path sets the expectation")
		Expect(err.Error()).To(ContainSubstring("tenant_id is not supported"))

		By("reading the public REST route through the external ingress")
		body, statusCode, err := getBillingPricingREST(ctx, tool.ExternalView().AdminClient(),
			"/api/fulfillment/v1/billing/provider_rate_card_items?currency_code=USD&tenant_id=tenant-a")
		Expect(err).NotTo(HaveOccurred())
		Expect(statusCode).To(Equal(http.StatusNotImplemented), "grpc-gateway maps UNIMPLEMENTED to 501")
		Expect(string(body)).To(ContainSubstring("tenant_id is not supported"))

		By("reading the private REST route through the internal ingress")
		body, statusCode, err = getBillingPricingREST(ctx, tool.InternalView().AdminClient(),
			"/api/private/v1/billing/provider_rate_card_items?currency_code=USD&tenant_id=tenant-a")
		Expect(err).NotTo(HaveOccurred())
		Expect(statusCode).To(Equal(http.StatusNotImplemented), "grpc-gateway maps UNIMPLEMENTED to 501")
		Expect(string(body)).To(ContainSubstring("tenant_id is not supported"))
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
// gRPC path returned, including a compute_instance/instance_type row that lists the seeded catalog item.
func expectCatalogPricingInputsRESTParity(g Gomega, body []byte, grpcTotal int32, itemID string) {
	var restList struct {
		Items []map[string]any `json:"items"`
		Total int32            `json:"total"`
	}
	g.Expect(json.Unmarshal(body, &restList)).To(Succeed())
	g.Expect(restList.Total).To(Equal(grpcTotal), "REST and gRPC must agree on the inventory")
	rowFound := false
	itemFound := false
	for _, item := range restList.Items {
		g.Expect(item).To(HaveKey("service"))
		g.Expect(item).To(HaveKey("dimension"))
		if item["service"] == "compute_instance" && item["dimension"] == "instance_type" {
			rowFound = true
			if catalogPricingRowListsItem(item, itemID) {
				itemFound = true
			}
		}
	}
	g.Expect(rowFound).To(BeTrue(), "the seeded catalog item's instance_type row is missing")
	g.Expect(itemFound).To(BeTrue(), "the REST instance_type row does not list the seeded catalog item")
}

// catalogPricingGRPCInputRowListsItem reports whether a gRPC catalog pricing inputs listing's
// compute_instance/instance_type row references the given catalog item id.
func catalogPricingGRPCInputRowListsItem(list *publicv1.CatalogPricingInputsListResponse, id string) bool {
	for _, row := range list.GetItems() {
		if row.GetService() != "compute_instance" || row.GetDimension() != "instance_type" {
			continue
		}
		for _, entry := range row.GetCatalogItems() {
			if entry.GetId() == id {
				return true
			}
		}
	}
	return false
}
