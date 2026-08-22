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

package dispatcher

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	privatev1 "github.com/osac-project/osac/osac-operator/internal/api/osac/private/v1"
	"github.com/osac-project/osac/osac-operator/pkg/networkmanager"
)

// stubNetworkClassesClient implements privatev1.NetworkClassesClient for testing.
// Only Get is used by Resolver; the remaining methods panic if called.
type stubNetworkClassesClient struct {
	privatev1.NetworkClassesClient
	getFunc func(ctx context.Context, in *privatev1.NetworkClassesGetRequest, opts ...grpc.CallOption) (*privatev1.NetworkClassesGetResponse, error)
}

func (s *stubNetworkClassesClient) Get(ctx context.Context, in *privatev1.NetworkClassesGetRequest, opts ...grpc.CallOption) (*privatev1.NetworkClassesGetResponse, error) {
	return s.getFunc(ctx, in, opts...)
}

func newTestFabricManagerConfigMap(name, managerName, capabilities string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "osac",
			Labels:    map[string]string{networkmanager.LabelFabricManager: "true"},
		},
		Data: map[string]string{
			"name":         managerName,
			"capabilities": capabilities,
		},
	}
}

var _ = Describe("cachedResolver", func() {
	var (
		ctx       context.Context
		scheme    *runtime.Scheme
		callCount int
	)

	BeforeEach(func() {
		ctx = context.Background()
		scheme = runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
		callCount = 0
	})

	It("caches successful results", func() {
		fabricManager := "netris"
		stub := &stubNetworkClassesClient{
			getFunc: func(_ context.Context, _ *privatev1.NetworkClassesGetRequest, _ ...grpc.CallOption) (*privatev1.NetworkClassesGetResponse, error) {
				callCount++
				return &privatev1.NetworkClassesGetResponse{
					Object: &privatev1.NetworkClass{
						FabricManager: &fabricManager,
					},
				}, nil
			},
		}

		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			newTestFabricManagerConfigMap("fm-netris", "netris", "ipv4"),
		).Build()
		disc, err := networkmanager.NewDiscovery(cl, "osac")
		Expect(err).NotTo(HaveOccurred())
		resolver := NewResolver(stub, disc)
		cached := newCachedResolver(resolver)

		result1, err := cached.Resolve(ctx, "nc-cached")
		Expect(err).NotTo(HaveOccurred())
		Expect(result1.FabricManager.Name).To(Equal("netris"))
		Expect(callCount).To(Equal(1))

		result2, err := cached.Resolve(ctx, "nc-cached")
		Expect(err).NotTo(HaveOccurred())
		Expect(result2.FabricManager.Name).To(Equal("netris"))
		Expect(callCount).To(Equal(1))
	})

	It("does not cache errors", func() {
		stub := &stubNetworkClassesClient{
			getFunc: func(_ context.Context, _ *privatev1.NetworkClassesGetRequest, _ ...grpc.CallOption) (*privatev1.NetworkClassesGetResponse, error) {
				callCount++
				return nil, fmt.Errorf("unavailable")
			},
		}

		cl := fake.NewClientBuilder().WithScheme(scheme).Build()
		disc, err := networkmanager.NewDiscovery(cl, "osac")
		Expect(err).NotTo(HaveOccurred())
		resolver := NewResolver(stub, disc)
		cached := newCachedResolver(resolver)

		_, err = cached.Resolve(ctx, "nc-err")
		Expect(err).To(HaveOccurred())
		Expect(callCount).To(Equal(1))

		_, err = cached.Resolve(ctx, "nc-err")
		Expect(err).To(HaveOccurred())
		Expect(callCount).To(Equal(2))
	})

	It("caches different NetworkClass IDs independently", func() {
		fabricManager := "netris"
		stub := &stubNetworkClassesClient{
			getFunc: func(_ context.Context, _ *privatev1.NetworkClassesGetRequest, _ ...grpc.CallOption) (*privatev1.NetworkClassesGetResponse, error) {
				callCount++
				return &privatev1.NetworkClassesGetResponse{
					Object: &privatev1.NetworkClass{
						FabricManager: &fabricManager,
					},
				}, nil
			},
		}

		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			newTestFabricManagerConfigMap("fm-netris", "netris", "ipv4"),
		).Build()
		disc, err := networkmanager.NewDiscovery(cl, "osac")
		Expect(err).NotTo(HaveOccurred())
		resolver := NewResolver(stub, disc)
		cached := newCachedResolver(resolver)

		_, err = cached.Resolve(ctx, "nc-a")
		Expect(err).NotTo(HaveOccurred())
		_, err = cached.Resolve(ctx, "nc-b")
		Expect(err).NotTo(HaveOccurred())
		Expect(callCount).To(Equal(2))

		_, err = cached.Resolve(ctx, "nc-a")
		Expect(err).NotTo(HaveOccurred())
		_, err = cached.Resolve(ctx, "nc-b")
		Expect(err).NotTo(HaveOccurred())
		Expect(callCount).To(Equal(2))
	})
})
