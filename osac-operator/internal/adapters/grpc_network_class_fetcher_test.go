/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package adapters_test

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"

	"github.com/osac-project/osac/osac-operator/internal/adapters"
	privatev1 "github.com/osac-project/osac/osac-operator/internal/api/osac/private/v1"
)

type stubNetworkClassesClient struct {
	privatev1.NetworkClassesClient
	getFunc func(ctx context.Context, in *privatev1.NetworkClassesGetRequest, opts ...grpc.CallOption) (*privatev1.NetworkClassesGetResponse, error)
}

func (s *stubNetworkClassesClient) Get(
	ctx context.Context,
	in *privatev1.NetworkClassesGetRequest,
	opts ...grpc.CallOption,
) (*privatev1.NetworkClassesGetResponse, error) {
	return s.getFunc(ctx, in, opts...)
}

var _ = Describe("GRPCNetworkClassFetcher", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("returns both managers when present", func() {
		k8sManager := "cudn_localnet"
		stub := &stubNetworkClassesClient{
			getFunc: func(_ context.Context, req *privatev1.NetworkClassesGetRequest, _ ...grpc.CallOption) (*privatev1.NetworkClassesGetResponse, error) {
				Expect(req.GetId()).To(Equal("nc-1"))
				return &privatev1.NetworkClassesGetResponse{
					Object: &privatev1.NetworkClass{
						Id:            "nc-1",
						FabricManager: "netris",
						K8SManager:    &k8sManager,
					},
				}, nil
			},
		}

		fetcher := adapters.NewGRPCNetworkClassFetcher(stub)
		info, err := fetcher.FetchNetworkClass(ctx, "nc-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(info.FabricManager).To(Equal("netris"))
		Expect(info.K8sManager).To(Equal("cudn_localnet"))
	})

	It("returns empty K8sManager when not specified", func() {
		stub := &stubNetworkClassesClient{
			getFunc: func(_ context.Context, _ *privatev1.NetworkClassesGetRequest, _ ...grpc.CallOption) (*privatev1.NetworkClassesGetResponse, error) {
				return &privatev1.NetworkClassesGetResponse{
					Object: &privatev1.NetworkClass{
						Id:            "nc-2",
						FabricManager: "netris",
					},
				}, nil
			},
		}

		fetcher := adapters.NewGRPCNetworkClassFetcher(stub)
		info, err := fetcher.FetchNetworkClass(ctx, "nc-2")
		Expect(err).NotTo(HaveOccurred())
		Expect(info.FabricManager).To(Equal("netris"))
		Expect(info.K8sManager).To(BeEmpty())
	})

	It("propagates gRPC errors", func() {
		stub := &stubNetworkClassesClient{
			getFunc: func(_ context.Context, _ *privatev1.NetworkClassesGetRequest, _ ...grpc.CallOption) (*privatev1.NetworkClassesGetResponse, error) {
				return nil, fmt.Errorf("rpc error: code = Unavailable")
			},
		}

		fetcher := adapters.NewGRPCNetworkClassFetcher(stub)
		_, err := fetcher.FetchNetworkClass(ctx, "nc-err")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("Unavailable"))
	})

	It("returns error when response object is nil", func() {
		stub := &stubNetworkClassesClient{
			getFunc: func(_ context.Context, _ *privatev1.NetworkClassesGetRequest, _ ...grpc.CallOption) (*privatev1.NetworkClassesGetResponse, error) {
				return &privatev1.NetworkClassesGetResponse{Object: nil}, nil
			},
		}

		fetcher := adapters.NewGRPCNetworkClassFetcher(stub)
		_, err := fetcher.FetchNetworkClass(ctx, "nc-nil")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("response contains no object"))
	})
})
