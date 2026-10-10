/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"fmt"
	"net/http"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Service enablement", func() {
	BeforeEach(func() {
		if config.TestSuite == "" {
			Skip("focused service enablement scenarios require IT_TEST_SUITE")
		}
	})

	It("filters HostTypes and rejects disabled service endpoints", func(ctx context.Context) {
		var (
			expectedPublicServices  []publicv1.ServiceTier
			expectedPrivateServices []privatev1.ServiceTier
			disabledRestPath        string
			disabledGrpcError       error
		)

		switch config.TestSuite {
		case "bmaas-disabled":
			expectedPublicServices = []publicv1.ServiceTier{
				publicv1.ServiceTier_SERVICE_TIER_CAAS,
				publicv1.ServiceTier_SERVICE_TIER_VMAAS,
			}
			expectedPrivateServices = []privatev1.ServiceTier{
				privatev1.ServiceTier_SERVICE_TIER_CAAS,
				privatev1.ServiceTier_SERVICE_TIER_VMAAS,
			}
			disabledRestPath = "/api/fulfillment/v1/baremetal_instances"

			client := publicv1.NewBareMetalInstancesClient(tool.ExternalView().UserConn())
			_, disabledGrpcError = client.List(ctx, publicv1.BareMetalInstancesListRequest_builder{}.Build())
		case "vmaas-disabled":
			expectedPublicServices = []publicv1.ServiceTier{
				publicv1.ServiceTier_SERVICE_TIER_CAAS,
				publicv1.ServiceTier_SERVICE_TIER_BMAAS,
			}
			expectedPrivateServices = []privatev1.ServiceTier{
				privatev1.ServiceTier_SERVICE_TIER_CAAS,
				privatev1.ServiceTier_SERVICE_TIER_BMAAS,
			}
			disabledRestPath = "/api/fulfillment/v1/compute_instances"

			client := publicv1.NewComputeInstancesClient(tool.ExternalView().UserConn())
			_, disabledGrpcError = client.List(ctx, publicv1.ComputeInstancesListRequest_builder{}.Build())
		default:
			Fail(fmt.Sprintf("unsupported IT_TEST_SUITE value %q", config.TestSuite))
		}

		// Capabilities confirms that the running Kind deployment received the intended service flags.
		publicCapabilities := publicv1.NewCapabilitiesClient(tool.ExternalView().AnonymousConn())
		publicCapabilitiesResponse, err := publicCapabilities.Get(ctx, publicv1.CapabilitiesGetRequest_builder{}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(publicCapabilitiesResponse.GetEnabledServices()).To(Equal(expectedPublicServices))

		privateCapabilities := privatev1.NewCapabilitiesClient(tool.InternalView().AdminConn())
		privateCapabilitiesResponse, err := privateCapabilities.Get(ctx, privatev1.CapabilitiesGetRequest_builder{}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(privateCapabilitiesResponse.GetEnabledServices()).To(Equal(expectedPrivateServices))

		Expect(grpcstatus.Code(disabledGrpcError)).To(Equal(grpccodes.Unavailable))

		// The REST gateway registers all routes and translates an unavailable gRPC service to HTTP 503.
		restRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, disabledRestPath, nil)
		Expect(err).ToNot(HaveOccurred())
		restResponse, err := tool.ExternalView().UserClient().Do(restRequest)
		Expect(err).ToNot(HaveOccurred())
		defer restResponse.Body.Close()
		Expect(restResponse.StatusCode).To(Equal(http.StatusServiceUnavailable))

	})
})
