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
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("REST gateway", func() {
	var (
		ctx             context.Context
		templatesClient privatev1.ClusterTemplatesClient
	)

	BeforeEach(func() {
		ctx = context.Background()
		templatesClient = privatev1.NewClusterTemplatesClient(tool.InternalView().AdminConn())
	})

	It("Should use protobuf field names in JSON representation", func() {
		// Create a cluster template:
		templateID := newTemplateID("my")
		_, err := templatesClient.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
			Object: privatev1.ClusterTemplate_builder{
				Id:          templateID,
				Title:       "My template",
				Description: "My template.",
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, err := templatesClient.Delete(ctx, privatev1.ClusterTemplatesDeleteRequest_builder{
				Id: templateID,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		// Retrieve the template via REST API:
		request, err := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			fmt.Sprintf("/api/fulfillment/v1/cluster_templates/%s", templateID),
			nil,
		)
		Expect(err).ToNot(HaveOccurred())
		response, err := tool.ExternalView().UserClient().Do(request)
		Expect(err).ToNot(HaveOccurred())
		defer response.Body.Close()
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		data, err := io.ReadAll(response.Body)
		Expect(err).ToNot(HaveOccurred())
		var body map[string]any
		err = json.Unmarshal(data, &body)
		Expect(err).ToNot(HaveOccurred())

		// Verify field names:
		Expect(body).NotTo(HaveKey("node_sets"))
	})

	It("Should be possible to fetch cluster templates via private REST API", func() {
		// Create a cluster template:
		templateID := newTemplateID("my")
		_, err := templatesClient.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
			Object: privatev1.ClusterTemplate_builder{
				Id:          templateID,
				Title:       "My private template",
				Description: "My private template.",
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_, err := templatesClient.Delete(ctx, privatev1.ClusterTemplatesDeleteRequest_builder{
				Id: templateID,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
		})

		// Retrieve the template via private REST API:
		request, err := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			fmt.Sprintf("/api/private/v1/cluster_templates/%s", templateID),
			nil,
		)
		Expect(err).ToNot(HaveOccurred())
		response, err := tool.InternalView().AdminClient().Do(request)
		Expect(err).ToNot(HaveOccurred())
		defer response.Body.Close()
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		data, err := io.ReadAll(response.Body)
		Expect(err).ToNot(HaveOccurred())
		var body map[string]any
		err = json.Unmarshal(data, &body)
		Expect(err).ToNot(HaveOccurred())

		// Verify the response contains the expected fields:
		Expect(body).To(HaveKey("id"))
		Expect(body).To(HaveKey("title"))
		Expect(body).To(HaveKey("description"))
		Expect(body).NotTo(HaveKey("node_sets"))
		Expect(body["id"]).To(Equal(templateID))
		Expect(body["title"]).To(Equal("My private template"))
		Expect(body["description"]).To(Equal("My private template."))

	})

	// The dev deployment configures no billing provider, so the billing REST routes carry the disabled-mode
	// error: FAILED_PRECONDITION over gRPC maps to HTTP 400 here. The loop covers the public routes through the
	// external view and the private routes through the internal view, so both surfaces' disabled-mode contract
	// is pinned at the route level.
	It("Should reject the billing routes with 400 when billing is not configured", Label("billing"), func() {
		public := map[string]func(*http.Request) (*http.Response, error){
			"/api/fulfillment/v1/billing/accounts":                   tool.ExternalView().AdminClient().Do,
			"/api/fulfillment/v1/billing/tenant_billing_assignments": tool.ExternalView().AdminClient().Do,
			"/api/private/v1/billing/accounts":                       tool.InternalView().AdminClient().Do,
			"/api/private/v1/billing/tenant_billing_assignments":     tool.InternalView().AdminClient().Do,
		}
		for path, do := range public {
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
			Expect(err).ToNot(HaveOccurred())
			response, err := do(request)
			Expect(err).ToNot(HaveOccurred())
			data, err := io.ReadAll(response.Body)
			response.Body.Close()
			Expect(err).ToNot(HaveOccurred())
			Expect(response.StatusCode).To(Equal(http.StatusBadRequest), path)
			Expect(string(data)).To(ContainSubstring("billing is not configured"), path)
		}
	})
})
