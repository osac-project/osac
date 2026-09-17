/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package restgateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type captureComputeInstancesGatewayServer struct {
	publicv1.UnimplementedComputeInstancesServer
	createRequest *publicv1.ComputeInstancesCreateRequest
}

func (s *captureComputeInstancesGatewayServer) Create(_ context.Context, request *publicv1.ComputeInstancesCreateRequest) (*publicv1.ComputeInstancesCreateResponse, error) {
	s.createRequest = request
	return publicv1.ComputeInstancesCreateResponse_builder{Object: request.GetObject()}.Build(), nil
}

var _ = Describe("ComputeInstances REST gateway", func() {
	It("parses spec_fields from the query while keeping the request body as an object", func() {
		server := &captureComputeInstancesGatewayServer{}
		mux := runtime.NewServeMux()
		Expect(publicv1.RegisterComputeInstancesHandlerServer(context.Background(), mux, server)).To(Succeed())

		request := httptest.NewRequest(
			http.MethodPost,
			"/api/fulfillment/v1/compute_instances?spec_fields.paths=additional_disks",
			strings.NewReader(`{"metadata":{"name":"gateway-test"}}`),
		)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()

		mux.ServeHTTP(response, request)

		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(server.createRequest).NotTo(BeNil())
		Expect(server.createRequest.GetObject().GetMetadata().GetName()).To(Equal("gateway-test"))
		Expect(server.createRequest.GetSpecFields().GetPaths()).To(Equal([]string{"additional_disks"}))
	})
})
