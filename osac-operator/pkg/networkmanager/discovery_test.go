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

package networkmanager_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/osac-project/osac/osac-operator/pkg/networkmanager"
)

var _ = Describe("Discovery", func() {
	var (
		ctx    context.Context
		scheme *runtime.Scheme
	)

	BeforeEach(func() {
		ctx = context.Background()
		scheme = runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())
	})

	Describe("NewDiscovery", func() {
		It("returns an error when namespace is empty", func() {
			cl := fake.NewClientBuilder().WithScheme(scheme).Build()
			_, err := networkmanager.NewDiscovery(cl, "")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("namespace must not be empty"))
		})
	})

	Describe("ListFabricManagers", func() {
		It("returns all fabric managers in the namespace", func() {
			netris := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "osac-network-fabric-manager-netris",
					Namespace: "osac",
					Labels:    map[string]string{networkmanager.LabelFabricManager: "true"},
				},
				Data: map[string]string{
					"name":         "netris",
					"description":  "Netris SDN",
					"capabilities": "ipv4",
				},
			}
			neutron := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "osac-network-fabric-manager-neutron",
					Namespace: "osac",
					Labels:    map[string]string{networkmanager.LabelFabricManager: "true"},
				},
				Data: map[string]string{
					"name":         "neutron",
					"description":  "OpenStack Neutron",
					"capabilities": "ipv4,ipv6,dualStack",
				},
			}

			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(netris, neutron).Build()
			disc, err := networkmanager.NewDiscovery(cl, "osac")
			Expect(err).NotTo(HaveOccurred())

			managers, err := disc.ListFabricManagers(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(managers).To(HaveLen(2))

			names := []string{managers[0].Name, managers[1].Name}
			Expect(names).To(ConsistOf("netris", "neutron"))
		})

		It("ignores ConfigMaps in other namespaces", func() {
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "osac-network-fabric-manager-netris",
					Namespace: "other-namespace",
					Labels:    map[string]string{networkmanager.LabelFabricManager: "true"},
				},
				Data: map[string]string{
					"name":         "netris",
					"capabilities": "ipv4",
				},
			}

			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build()
			disc, err := networkmanager.NewDiscovery(cl, "osac")
			Expect(err).NotTo(HaveOccurred())

			managers, err := disc.ListFabricManagers(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(managers).To(BeEmpty())
		})

		It("ignores ConfigMaps without the fabric-manager label", func() {
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "unrelated-configmap",
					Namespace: "osac",
					Labels:    map[string]string{"app": "something-else"},
				},
				Data: map[string]string{
					"name": "not-a-manager",
				},
			}

			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build()
			disc, err := networkmanager.NewDiscovery(cl, "osac")
			Expect(err).NotTo(HaveOccurred())

			managers, err := disc.ListFabricManagers(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(managers).To(BeEmpty())
		})

		It("returns empty list when no managers exist", func() {
			cl := fake.NewClientBuilder().WithScheme(scheme).Build()
			disc, err := networkmanager.NewDiscovery(cl, "osac")
			Expect(err).NotTo(HaveOccurred())

			managers, err := disc.ListFabricManagers(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(managers).To(BeEmpty())
		})

		It("returns error when two ConfigMaps register the same name", func() {
			cm1 := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "fm-netris-1",
					Namespace: "osac",
					Labels:    map[string]string{networkmanager.LabelFabricManager: "true"},
				},
				Data: map[string]string{
					"name":         "netris",
					"capabilities": "ipv4",
				},
			}
			cm2 := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "fm-netris-2",
					Namespace: "osac",
					Labels:    map[string]string{networkmanager.LabelFabricManager: "true"},
				},
				Data: map[string]string{
					"name":         "netris",
					"capabilities": "ipv4,ipv6",
				},
			}

			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm1, cm2).Build()
			disc, err := networkmanager.NewDiscovery(cl, "osac")
			Expect(err).NotTo(HaveOccurred())

			_, err = disc.ListFabricManagers(ctx)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("duplicate"))
			Expect(err.Error()).To(ContainSubstring("netris"))
		})

		It("does not return k8s managers", func() {
			k8sCM := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "osac-network-k8s-manager-cudn-localnet",
					Namespace: "osac",
					Labels:    map[string]string{networkmanager.LabelK8sManager: "true"},
				},
				Data: map[string]string{
					"name":         "cudn_localnet",
					"capabilities": "ipv4,ipv6,dualStack",
				},
			}

			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(k8sCM).Build()
			disc, err := networkmanager.NewDiscovery(cl, "osac")
			Expect(err).NotTo(HaveOccurred())

			managers, err := disc.ListFabricManagers(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(managers).To(BeEmpty())
		})

		It("returns error for ConfigMap with invalid data", func() {
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "bad-manager",
					Namespace: "osac",
					Labels:    map[string]string{networkmanager.LabelFabricManager: "true"},
				},
				Data: map[string]string{
					"description": "Missing name field",
				},
			}

			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build()
			disc, err := networkmanager.NewDiscovery(cl, "osac")
			Expect(err).NotTo(HaveOccurred())

			_, err = disc.ListFabricManagers(ctx)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("missing or empty required field data.name"))
		})
	})

	Describe("ListK8sManagers", func() {
		It("returns all k8s managers in the namespace", func() {
			cudnLocalnet := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "osac-network-k8s-manager-cudn-localnet",
					Namespace: "osac",
					Labels:    map[string]string{networkmanager.LabelK8sManager: "true"},
				},
				Data: map[string]string{
					"name":         "cudn_localnet",
					"description":  "CUDN LocalNet bridge",
					"capabilities": "ipv4,ipv6,dualStack",
				},
			}

			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cudnLocalnet).Build()
			disc, err := networkmanager.NewDiscovery(cl, "osac")
			Expect(err).NotTo(HaveOccurred())

			managers, err := disc.ListK8sManagers(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(managers).To(HaveLen(1))
			Expect(managers[0].Name).To(Equal("cudn_localnet"))
			Expect(managers[0].Type).To(Equal(networkmanager.K8sManager))
		})

		It("does not return fabric managers", func() {
			fabricCM := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "osac-network-fabric-manager-netris",
					Namespace: "osac",
					Labels:    map[string]string{networkmanager.LabelFabricManager: "true"},
				},
				Data: map[string]string{
					"name":         "netris",
					"capabilities": "ipv4",
				},
			}

			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(fabricCM).Build()
			disc, err := networkmanager.NewDiscovery(cl, "osac")
			Expect(err).NotTo(HaveOccurred())

			managers, err := disc.ListK8sManagers(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(managers).To(BeEmpty())
		})
	})

	Describe("GetFabricManager", func() {
		It("returns the manager matching the name", func() {
			netris := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "osac-network-fabric-manager-netris",
					Namespace: "osac",
					Labels:    map[string]string{networkmanager.LabelFabricManager: "true"},
				},
				Data: map[string]string{
					"name":         "netris",
					"description":  "Netris SDN",
					"capabilities": "ipv4",
				},
			}
			neutron := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "osac-network-fabric-manager-neutron",
					Namespace: "osac",
					Labels:    map[string]string{networkmanager.LabelFabricManager: "true"},
				},
				Data: map[string]string{
					"name":         "neutron",
					"capabilities": "ipv4,ipv6,dualStack",
				},
			}

			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(netris, neutron).Build()
			disc, err := networkmanager.NewDiscovery(cl, "osac")
			Expect(err).NotTo(HaveOccurred())

			mgr, err := disc.GetFabricManager(ctx, "neutron")
			Expect(err).NotTo(HaveOccurred())
			Expect(mgr.Name).To(Equal("neutron"))
			Expect(mgr.Capabilities).To(ConsistOf(
				networkmanager.CapabilityIPv4,
				networkmanager.CapabilityIPv6,
				networkmanager.CapabilityDualStack,
			))
		})

		It("returns ManagerNotFoundError when manager does not exist", func() {
			cl := fake.NewClientBuilder().WithScheme(scheme).Build()
			disc, err := networkmanager.NewDiscovery(cl, "osac")
			Expect(err).NotTo(HaveOccurred())

			_, err = disc.GetFabricManager(ctx, "nonexistent")
			Expect(err).To(HaveOccurred())
			Expect(networkmanager.IsManagerNotFound(err)).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring("fabric manager \"nonexistent\" not found"))
		})
	})

	Describe("GetK8sManager", func() {
		It("returns the manager matching the name", func() {
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "osac-network-k8s-manager-cudn-localnet",
					Namespace: "osac",
					Labels:    map[string]string{networkmanager.LabelK8sManager: "true"},
				},
				Data: map[string]string{
					"name":         "cudn_localnet",
					"capabilities": "ipv4,ipv6,dualStack",
				},
			}

			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).Build()
			disc, err := networkmanager.NewDiscovery(cl, "osac")
			Expect(err).NotTo(HaveOccurred())

			mgr, err := disc.GetK8sManager(ctx, "cudn_localnet")
			Expect(err).NotTo(HaveOccurred())
			Expect(mgr.Name).To(Equal("cudn_localnet"))
			Expect(mgr.Type).To(Equal(networkmanager.K8sManager))
		})

		It("returns ManagerNotFoundError when manager does not exist", func() {
			cl := fake.NewClientBuilder().WithScheme(scheme).Build()
			disc, err := networkmanager.NewDiscovery(cl, "osac")
			Expect(err).NotTo(HaveOccurred())

			_, err = disc.GetK8sManager(ctx, "nonexistent")
			Expect(err).To(HaveOccurred())
			Expect(networkmanager.IsManagerNotFound(err)).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring("k8s manager \"nonexistent\" not found"))
		})
	})
})
