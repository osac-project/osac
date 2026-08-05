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
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/osac-project/osac/osac-operator/pkg/dispatcher"
	"github.com/osac-project/osac/osac-operator/pkg/networkmanager"
)

// stubNetworkClassClient implements dispatcher.NetworkClassClient for testing.
type stubNetworkClassClient struct {
	getFunc func(ctx context.Context, networkClassID string) (*dispatcher.NetworkClassManagers, error)
}

func (s *stubNetworkClassClient) GetNetworkClass(ctx context.Context, networkClassID string) (*dispatcher.NetworkClassManagers, error) {
	return s.getFunc(ctx, networkClassID)
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
		stub := &stubNetworkClassClient{
			getFunc: func(_ context.Context, networkClassID string) (*dispatcher.NetworkClassManagers, error) {
				Expect(networkClassID).To(Equal("nc-1"))
				return &dispatcher.NetworkClassManagers{
					FabricManager: "netris",
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
		Expect(result.FabricManager).NotTo(BeNil())
		Expect(result.FabricManager.Name).To(Equal("netris"))
		Expect(result.K8sManager).To(BeNil())
	})

	It("resolves a NetworkClass with both fabric and k8s managers", func() {
		stub := &stubNetworkClassClient{
			getFunc: func(_ context.Context, _ string) (*dispatcher.NetworkClassManagers, error) {
				return &dispatcher.NetworkClassManagers{
					FabricManager: "neutron",
					K8sManager:    "cudn_localnet",
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
		Expect(result.FabricManager).NotTo(BeNil())
		Expect(result.FabricManager.Name).To(Equal("neutron"))
		Expect(result.FabricManager.Type).To(Equal(networkmanager.FabricManager))
		Expect(result.K8sManager).NotTo(BeNil())
		Expect(result.K8sManager.Name).To(Equal("cudn_localnet"))
		Expect(result.K8sManager.Type).To(Equal(networkmanager.K8sManager))
	})

	It("returns error when NetworkClass is not found", func() {
		stub := &stubNetworkClassClient{
			getFunc: func(_ context.Context, _ string) (*dispatcher.NetworkClassManagers, error) {
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

	It("returns error when the client returns no object", func() {
		stub := &stubNetworkClassClient{
			getFunc: func(_ context.Context, _ string) (*dispatcher.NetworkClassManagers, error) {
				return nil, nil
			},
		}

		cl := fake.NewClientBuilder().WithScheme(scheme).Build()
		disc, err := networkmanager.NewDiscovery(cl, "osac")
		Expect(err).NotTo(HaveOccurred())
		resolver := dispatcher.NewResolver(stub, disc)

		_, err = resolver.Resolve(ctx, "nc-no-object")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("response contains no object"))
	})

	It("resolves a k8s-only NetworkClass (no fabricManager)", func() {
		stub := &stubNetworkClassClient{
			getFunc: func(_ context.Context, _ string) (*dispatcher.NetworkClassManagers, error) {
				return &dispatcher.NetworkClassManagers{
					K8sManager: "cudn_localnet",
				}, nil
			},
		}

		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			newK8sManagerConfigMap("km-cudn", "cudn_localnet", "ipv4,ipv6,dualStack"),
		).Build()
		disc, err := networkmanager.NewDiscovery(cl, "osac")
		Expect(err).NotTo(HaveOccurred())
		resolver := dispatcher.NewResolver(stub, disc)

		result, err := resolver.Resolve(ctx, "nc-k8s-only")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.FabricManager).To(BeNil())
		Expect(result.K8sManager).NotTo(BeNil())
		Expect(result.K8sManager.Name).To(Equal("cudn_localnet"))
	})

	It("returns error when neither fabricManager nor k8sManager is set", func() {
		stub := &stubNetworkClassClient{
			getFunc: func(_ context.Context, _ string) (*dispatcher.NetworkClassManagers, error) {
				return &dispatcher.NetworkClassManagers{}, nil
			},
		}

		cl := fake.NewClientBuilder().WithScheme(scheme).Build()
		disc, err := networkmanager.NewDiscovery(cl, "osac")
		Expect(err).NotTo(HaveOccurred())
		resolver := dispatcher.NewResolver(stub, disc)

		_, err = resolver.Resolve(ctx, "nc-no-manager")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("neither fabricManager nor k8sManager is set"))
		Expect(errors.Is(err, dispatcher.ErrNoManagerConfigured)).To(BeTrue())
	})

	It("returns error when fabric manager is not registered", func() {
		stub := &stubNetworkClassClient{
			getFunc: func(_ context.Context, _ string) (*dispatcher.NetworkClassManagers, error) {
				return &dispatcher.NetworkClassManagers{
					FabricManager: "unknown-fabric",
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
		stub := &stubNetworkClassClient{
			getFunc: func(_ context.Context, _ string) (*dispatcher.NetworkClassManagers, error) {
				return &dispatcher.NetworkClassManagers{
					FabricManager: "netris",
					K8sManager:    "missing-k8s",
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
