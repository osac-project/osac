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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/uuid"
)

var _ = Describe("Emergency access", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("Can create objects using gRPC and the emergency admin service account", func() {
		// Create the client:
		client := privatev1.NewClusterTemplatesClient(tool.InternalView().EmergencyConn())

		// Create the object:
		id := fmt.Sprintf("emergency_grpc_%s", uuid.New())
		_, err := client.Create(ctx, privatev1.ClusterTemplatesCreateRequest_builder{
			Object: privatev1.ClusterTemplate_builder{
				Id:          id,
				Title:       "Emergency gRPC template",
				Description: "Template created via gRPC using emergency admin service account.",
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		// Delete the object:
		_, err = client.Delete(ctx, privatev1.ClusterTemplatesDeleteRequest_builder{
			Id: id,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
	})

	It("Can create objects using REST and the emergency admin service account", func() {
		// Prepare the request body:
		id := fmt.Sprintf("emergency_rest_%s", uuid.New())
		requestBody := map[string]any{
			"id":          id,
			"title":       "Emergency REST template",
			"description": "Template created via REST using emergency admin service account.",
		}
		requestData, err := json.Marshal(requestBody)
		Expect(err).ToNot(HaveOccurred())

		// Create the object:
		createRequest, err := http.NewRequestWithContext(
			ctx,
			http.MethodPost,
			"/api/private/v1/cluster_templates",
			bytes.NewReader(requestData),
		)
		Expect(err).ToNot(HaveOccurred())
		createRequest.Header.Set("Content-Type", "application/json")
		createResponse, err := tool.InternalView().EmergencyClient().Do(createRequest)
		Expect(err).ToNot(HaveOccurred())
		defer createResponse.Body.Close()
		Expect(createResponse.StatusCode).To(Equal(http.StatusOK))

		// Delete the object:
		deleteRequest, err := http.NewRequestWithContext(
			ctx,
			http.MethodDelete,
			fmt.Sprintf("/api/private/v1/cluster_templates/%s", id),
			nil,
		)
		Expect(err).ToNot(HaveOccurred())
		deleteResponse, err := tool.InternalView().EmergencyClient().Do(deleteRequest)
		Expect(err).ToNot(HaveOccurred())
		defer deleteResponse.Body.Close()
		Expect(deleteResponse.StatusCode).To(Equal(http.StatusOK))
	})
})
