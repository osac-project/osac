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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/osac-project/osac-operator/pkg/networkmanager"
)

var _ = Describe("ParseConfigMap", func() {
	It("parses a valid fabric manager ConfigMap", func() {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "osac-network-fabric-manager-netris",
				Namespace: "osac",
				Labels:    map[string]string{networkmanager.LabelFabricManager: "true"},
			},
			Data: map[string]string{
				"name":         "netris",
				"description":  "Netris SDN controller",
				"capabilities": "ipv4",
			},
		}

		mgr, err := networkmanager.ParseConfigMap(cm, networkmanager.FabricManager)
		Expect(err).NotTo(HaveOccurred())
		Expect(mgr.Name).To(Equal("netris"))
		Expect(mgr.Description).To(Equal("Netris SDN controller"))
		Expect(mgr.Capabilities).To(ConsistOf(networkmanager.CapabilityIPv4))
		Expect(mgr.Type).To(Equal(networkmanager.FabricManager))
		Expect(mgr.ConfigMapRef).To(Equal(types.NamespacedName{Namespace: "osac", Name: "osac-network-fabric-manager-netris"}))
	})

	It("parses a valid k8s manager ConfigMap with multiple capabilities", func() {
		cm := &corev1.ConfigMap{
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

		mgr, err := networkmanager.ParseConfigMap(cm, networkmanager.K8sManager)
		Expect(err).NotTo(HaveOccurred())
		Expect(mgr.Name).To(Equal("cudn_localnet"))
		Expect(mgr.Type).To(Equal(networkmanager.K8sManager))
		Expect(mgr.Capabilities).To(ConsistOf(
			networkmanager.CapabilityIPv4,
			networkmanager.CapabilityIPv6,
			networkmanager.CapabilityDualStack,
		))
	})

	It("returns error for nil ConfigMap", func() {
		_, err := networkmanager.ParseConfigMap(nil, networkmanager.FabricManager)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("nil"))
	})

	It("returns error when data.name is missing", func() {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "osac"},
			Data: map[string]string{
				"description":  "Missing name",
				"capabilities": "ipv4",
			},
		}

		_, err := networkmanager.ParseConfigMap(cm, networkmanager.FabricManager)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("missing or empty required field data.name"))
	})

	It("returns error when data.name is empty", func() {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "osac"},
			Data: map[string]string{
				"name":         "   ",
				"capabilities": "ipv4",
			},
		}

		_, err := networkmanager.ParseConfigMap(cm, networkmanager.FabricManager)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("missing or empty required field data.name"))
	})

	It("returns error for invalid capability", func() {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "osac"},
			Data: map[string]string{
				"name":         "bad-caps",
				"capabilities": "ipv4,notACapability",
			},
		}

		_, err := networkmanager.ParseConfigMap(cm, networkmanager.FabricManager)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("invalid capability"))
		Expect(err.Error()).To(ContainSubstring("notACapability"))
	})

	It("returns error when capabilities field is missing", func() {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "osac"},
			Data: map[string]string{
				"name": "no-caps",
			},
		}

		_, err := networkmanager.ParseConfigMap(cm, networkmanager.FabricManager)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("capabilities field must not be empty"))
	})

	It("returns error when capabilities field is empty string", func() {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "osac"},
			Data: map[string]string{
				"name":         "empty-caps",
				"capabilities": "",
			},
		}

		_, err := networkmanager.ParseConfigMap(cm, networkmanager.FabricManager)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("capabilities field must not be empty"))
	})

	It("returns error when capabilities contain only commas and whitespace", func() {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "osac"},
			Data: map[string]string{
				"name":         "blank-caps",
				"capabilities": "  ,  , ",
			},
		}

		_, err := networkmanager.ParseConfigMap(cm, networkmanager.FabricManager)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("capabilities field must not be empty"))
	})

	It("expands dualStack to include ipv4 and ipv6", func() {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "osac"},
			Data: map[string]string{
				"name":         "ds-only",
				"capabilities": "dualStack",
			},
		}

		mgr, err := networkmanager.ParseConfigMap(cm, networkmanager.FabricManager)
		Expect(err).NotTo(HaveOccurred())
		Expect(mgr.Capabilities).To(ConsistOf(
			networkmanager.CapabilityDualStack,
			networkmanager.CapabilityIPv4,
			networkmanager.CapabilityIPv6,
		))
	})

	It("does not duplicate ipv4/ipv6 when dualStack is listed with them", func() {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "osac"},
			Data: map[string]string{
				"name":         "all-three",
				"capabilities": "ipv4,ipv6,dualStack",
			},
		}

		mgr, err := networkmanager.ParseConfigMap(cm, networkmanager.FabricManager)
		Expect(err).NotTo(HaveOccurred())
		Expect(mgr.Capabilities).To(ConsistOf(
			networkmanager.CapabilityIPv4,
			networkmanager.CapabilityIPv6,
			networkmanager.CapabilityDualStack,
		))
		Expect(mgr.Capabilities).To(HaveLen(3))
	})

	It("deduplicates repeated capabilities", func() {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "osac"},
			Data: map[string]string{
				"name":         "duped",
				"capabilities": "ipv4,ipv6,ipv4",
			},
		}

		mgr, err := networkmanager.ParseConfigMap(cm, networkmanager.FabricManager)
		Expect(err).NotTo(HaveOccurred())
		Expect(mgr.Capabilities).To(Equal([]networkmanager.Capability{
			networkmanager.CapabilityIPv4,
			networkmanager.CapabilityIPv6,
		}))
	})

	It("trims whitespace from capabilities", func() {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "osac"},
			Data: map[string]string{
				"name":         "spacey",
				"capabilities": " ipv4 , ipv6 ",
			},
		}

		mgr, err := networkmanager.ParseConfigMap(cm, networkmanager.FabricManager)
		Expect(err).NotTo(HaveOccurred())
		Expect(mgr.Capabilities).To(ConsistOf(networkmanager.CapabilityIPv4, networkmanager.CapabilityIPv6))
	})

	It("handles nil data map", func() {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "osac"},
		}

		_, err := networkmanager.ParseConfigMap(cm, networkmanager.FabricManager)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("missing or empty required field data.name"))
	})
})

var _ = Describe("Manager.HasCapability", func() {
	It("returns true for a supported capability", func() {
		mgr := &networkmanager.Manager{
			Capabilities: []networkmanager.Capability{networkmanager.CapabilityIPv4, networkmanager.CapabilityIPv6},
		}
		Expect(mgr.HasCapability(networkmanager.CapabilityIPv4)).To(BeTrue())
	})

	It("returns false for an unsupported capability", func() {
		mgr := &networkmanager.Manager{
			Capabilities: []networkmanager.Capability{networkmanager.CapabilityIPv4},
		}
		Expect(mgr.HasCapability(networkmanager.CapabilityDualStack)).To(BeFalse())
	})
})
