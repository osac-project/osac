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

package dispatcher_test

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	privatev1 "github.com/osac-project/osac/osac-operator/internal/api/osac/private/v1"
	"github.com/osac-project/osac/osac-operator/pkg/dispatcher"
	"github.com/osac-project/osac/osac-operator/pkg/networkmanager"
	"google.golang.org/grpc"
)

// stubNetworkClassesClient implements privatev1.NetworkClassesClient for testing.
type stubNetworkClassesClient struct {
	privatev1.NetworkClassesClient
	getFunc func(ctx context.Context, in *privatev1.NetworkClassesGetRequest, opts ...grpc.CallOption) (*privatev1.NetworkClassesGetResponse, error)
}

func (s *stubNetworkClassesClient) Get(ctx context.Context, in *privatev1.NetworkClassesGetRequest, opts ...grpc.CallOption) (*privatev1.NetworkClassesGetResponse, error) {
	return s.getFunc(ctx, in, opts...)
}

func newFabricManagerConfigMap(name, managerName, capabilities string) *corev1.ConfigMap {
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

func newK8sManagerConfigMap(name, managerName, capabilities string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "osac",
			Labels:    map[string]string{networkmanager.LabelK8sManager: "true"},
		},
		Data: map[string]string{
			"name":         managerName,
			"capabilities": capabilities,
		},
	}
}

var _ = Describe("Resolver", func() {
	var (
		ctx    context.Context
		scheme *runtime.Scheme
	)

	BeforeEach(func() {
		ctx = context.Background()
		scheme = runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
	})

	It("resolves a fabric-only NetworkClass", func() {
		k8sManagerStr := ""
		stub := &stubNetworkClassesClient{
			getFunc: func(_ context.Context, req *privatev1.NetworkClassesGetRequest, _ ...grpc.CallOption) (*privatev1.NetworkClassesGetResponse, error) {
				Expect(req.GetId()).To(Equal("nc-1"))
				return &privatev1.NetworkClassesGetResponse{
					Object: &privatev1.NetworkClass{
						Id:            "nc-1",
						FabricManager: "netris",
						K8SManager:    &k8sManagerStr,
					},
				}, nil
			},
		}

		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			newFabricManagerConfigMap("fm-netris", "netris", "ipv4"),
		).Build()
		disc, err := networkmanager.NewDiscovery(cl, "osac")
		Expect(err).NotTo(HaveOccurred())
		resolver := dispatcher.NewResolver(stub, disc)

		result, err := resolver.Resolve(ctx, "nc-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.FabricManager.Name).To(Equal("netris"))
		Expect(result.K8sManager).To(BeNil())
	})

	It("resolves a NetworkClass with both fabric and k8s managers", func() {
		k8sManagerName := "cudn_localnet"
		stub := &stubNetworkClassesClient{
			getFunc: func(_ context.Context, _ *privatev1.NetworkClassesGetRequest, _ ...grpc.CallOption) (*privatev1.NetworkClassesGetResponse, error) {
				return &privatev1.NetworkClassesGetResponse{
					Object: &privatev1.NetworkClass{
						Id:            "nc-2",
						FabricManager: "neutron",
						K8SManager:    &k8sManagerName,
					},
				}, nil
			},
		}

		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			newFabricManagerConfigMap("fm-neutron", "neutron", "ipv4,ipv6,dualStack"),
			newK8sManagerConfigMap("km-cudn", "cudn_localnet", "ipv4,ipv6,dualStack"),
		).Build()
		disc, err := networkmanager.NewDiscovery(cl, "osac")
		Expect(err).NotTo(HaveOccurred())
		resolver := dispatcher.NewResolver(stub, disc)

		result, err := resolver.Resolve(ctx, "nc-2")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.FabricManager.Name).To(Equal("neutron"))
		Expect(result.FabricManager.Type).To(Equal(networkmanager.FabricManager))
		Expect(result.K8sManager).NotTo(BeNil())
		Expect(result.K8sManager.Name).To(Equal("cudn_localnet"))
		Expect(result.K8sManager.Type).To(Equal(networkmanager.K8sManager))
	})

	It("returns error when NetworkClass is not found", func() {
		stub := &stubNetworkClassesClient{
			getFunc: func(_ context.Context, _ *privatev1.NetworkClassesGetRequest, _ ...grpc.CallOption) (*privatev1.NetworkClassesGetResponse, error) {
				return nil, fmt.Errorf("rpc error: code = NotFound")
			},
		}

		cl := fake.NewClientBuilder().WithScheme(scheme).Build()
		disc, err := networkmanager.NewDiscovery(cl, "osac")
		Expect(err).NotTo(HaveOccurred())
		resolver := dispatcher.NewResolver(stub, disc)

		_, err = resolver.Resolve(ctx, "nonexistent")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("fetching NetworkClass"))
	})

	It("returns error when fabricManager is empty", func() {
		stub := &stubNetworkClassesClient{
			getFunc: func(_ context.Context, _ *privatev1.NetworkClassesGetRequest, _ ...grpc.CallOption) (*privatev1.NetworkClassesGetResponse, error) {
				return &privatev1.NetworkClassesGetResponse{
					Object: &privatev1.NetworkClass{
						Id:            "nc-empty",
						FabricManager: "",
					},
				}, nil
			},
		}

		cl := fake.NewClientBuilder().WithScheme(scheme).Build()
		disc, err := networkmanager.NewDiscovery(cl, "osac")
		Expect(err).NotTo(HaveOccurred())
		resolver := dispatcher.NewResolver(stub, disc)

		_, err = resolver.Resolve(ctx, "nc-empty")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("fabricManager is required but not set"))
	})

	It("returns error when fabric manager is not registered", func() {
		stub := &stubNetworkClassesClient{
			getFunc: func(_ context.Context, _ *privatev1.NetworkClassesGetRequest, _ ...grpc.CallOption) (*privatev1.NetworkClassesGetResponse, error) {
				return &privatev1.NetworkClassesGetResponse{
					Object: &privatev1.NetworkClass{
						Id:            "nc-bad-fabric",
						FabricManager: "unknown-fabric",
					},
				}, nil
			},
		}

		cl := fake.NewClientBuilder().WithScheme(scheme).Build()
		disc, err := networkmanager.NewDiscovery(cl, "osac")
		Expect(err).NotTo(HaveOccurred())
		resolver := dispatcher.NewResolver(stub, disc)

		_, err = resolver.Resolve(ctx, "nc-bad-fabric")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("resolving fabricManager"))
		Expect(err.Error()).To(ContainSubstring("unknown-fabric"))
	})

	It("returns error when k8s manager is not registered", func() {
		k8sManagerName := "missing-k8s"
		stub := &stubNetworkClassesClient{
			getFunc: func(_ context.Context, _ *privatev1.NetworkClassesGetRequest, _ ...grpc.CallOption) (*privatev1.NetworkClassesGetResponse, error) {
				return &privatev1.NetworkClassesGetResponse{
					Object: &privatev1.NetworkClass{
						Id:            "nc-bad-k8s",
						FabricManager: "netris",
						K8SManager:    &k8sManagerName,
					},
				}, nil
			},
		}

		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			newFabricManagerConfigMap("fm-netris", "netris", "ipv4"),
		).Build()
		disc, err := networkmanager.NewDiscovery(cl, "osac")
		Expect(err).NotTo(HaveOccurred())
		resolver := dispatcher.NewResolver(stub, disc)

		_, err = resolver.Resolve(ctx, "nc-bad-k8s")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("resolving k8sManager"))
		Expect(err.Error()).To(ContainSubstring("missing-k8s"))
	})
})
