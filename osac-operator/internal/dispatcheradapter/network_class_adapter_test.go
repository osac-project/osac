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

package dispatcheradapter_test

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"

	privatev1 "github.com/osac-project/osac/osac-operator/internal/api/osac/private/v1"
	"github.com/osac-project/osac/osac-operator/internal/dispatcheradapter"
)

// stubNetworkClassesClient implements privatev1.NetworkClassesClient for testing.
type stubNetworkClassesClient struct {
	privatev1.NetworkClassesClient
	getFunc func(ctx context.Context, in *privatev1.NetworkClassesGetRequest, opts ...grpc.CallOption) (*privatev1.NetworkClassesGetResponse, error)
}

func (s *stubNetworkClassesClient) Get(ctx context.Context, in *privatev1.NetworkClassesGetRequest, opts ...grpc.CallOption) (*privatev1.NetworkClassesGetResponse, error) {
	return s.getFunc(ctx, in, opts...)
}

var _ = Describe("NetworkClassAdapter", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("maps a response with both managers set", func() {
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

		adapter := dispatcheradapter.NewNetworkClassAdapter(stub)
		managers, err := adapter.GetNetworkClass(ctx, "nc-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(managers.FabricManager).To(Equal("netris"))
		Expect(managers.K8sManager).To(Equal("cudn_localnet"))
	})

	It("maps a response with no k8s manager to an empty string", func() {
		stub := &stubNetworkClassesClient{
			getFunc: func(_ context.Context, _ *privatev1.NetworkClassesGetRequest, _ ...grpc.CallOption) (*privatev1.NetworkClassesGetResponse, error) {
				return &privatev1.NetworkClassesGetResponse{
					Object: &privatev1.NetworkClass{
						Id:            "nc-2",
						FabricManager: "neutron",
					},
				}, nil
			},
		}

		adapter := dispatcheradapter.NewNetworkClassAdapter(stub)
		managers, err := adapter.GetNetworkClass(ctx, "nc-2")
		Expect(err).NotTo(HaveOccurred())
		Expect(managers.FabricManager).To(Equal("neutron"))
		Expect(managers.K8sManager).To(Equal(""))
	})

	It("returns nil managers and nil error when the response has no object", func() {
		stub := &stubNetworkClassesClient{
			getFunc: func(_ context.Context, _ *privatev1.NetworkClassesGetRequest, _ ...grpc.CallOption) (*privatev1.NetworkClassesGetResponse, error) {
				return &privatev1.NetworkClassesGetResponse{}, nil
			},
		}

		adapter := dispatcheradapter.NewNetworkClassAdapter(stub)
		managers, err := adapter.GetNetworkClass(ctx, "nc-missing")
		Expect(err).NotTo(HaveOccurred())
		Expect(managers).To(BeNil())
	})

	It("propagates gRPC errors unchanged", func() {
		stub := &stubNetworkClassesClient{
			getFunc: func(_ context.Context, _ *privatev1.NetworkClassesGetRequest, _ ...grpc.CallOption) (*privatev1.NetworkClassesGetResponse, error) {
				return nil, fmt.Errorf("rpc error: code = Unavailable")
			},
		}

		adapter := dispatcheradapter.NewNetworkClassAdapter(stub)
		managers, err := adapter.GetNetworkClass(ctx, "nc-3")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("Unavailable"))
		Expect(managers).To(BeNil())
	})
})
