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
	"io"
	"net/http"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
)

var _ = Describe("Ingress separation", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("Should be able to use the public gRPC public api via the external ingress", func() {
		client := publicv1.NewClusterTemplatesClient(tool.ExternalView().UserConn())
		response, err := client.List(ctx, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
	})

	It("Should be able to use the public gRPC API via the internal ingress", func() {
		client := publicv1.NewClusterTemplatesClient(tool.InternalView().AdminConn())
		response, err := client.List(ctx, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
	})

	It("Should not be able to use the private gRPC API via the external ingress", func() {
		client := privatev1.NewClusterTemplatesClient(tool.ExternalView().AdminConn())
		response, err := client.List(ctx, nil)
		Expect(err).To(HaveOccurred())
		Expect(response).To(BeNil())
		status, ok := grpcstatus.FromError(err)
		Expect(ok).To(BeTrue())
		Expect(status.Code()).To(Equal(grpccodes.Unimplemented))
	})

	It("Should be able to use the public REST API via the external ingress", func() {
		request, err := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			"/api/fulfillment/v1/cluster_templates",
			nil,
		)
		Expect(err).ToNot(HaveOccurred())
		response, err := tool.ExternalView().UserClient().Do(request)
		Expect(err).ToNot(HaveOccurred())
		defer response.Body.Close()
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		_, err = io.Copy(io.Discard, response.Body)
		Expect(err).ToNot(HaveOccurred())
	})

	It("Should be able to use the private REST API via the internal ingress", func() {
		request, err := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			"/api/private/v1/cluster_templates",
			nil,
		)
		Expect(err).ToNot(HaveOccurred())
		response, err := tool.InternalView().AdminClient().Do(request)
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		defer response.Body.Close()
		Expect(response.StatusCode).To(Equal(http.StatusOK))
	})

	It("Should not be able to use the private REST API via the external ingress", func() {
		request, err := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			"/api/private/v1/cluster_templates",
			nil,
		)
		Expect(err).ToNot(HaveOccurred())
		response, err := tool.ExternalView().AdminClient().Do(request)
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		defer response.Body.Close()
		Expect(response.StatusCode).To(Equal(http.StatusNotFound))
	})
})
