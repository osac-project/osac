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
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/osac-project/osac/osac-operator/pkg/networkmanager"
)

const ns = "osac-operator"

func registration(cmName string, label string, data map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: cmName, Namespace: ns, Labels: map[string]string{label: "true"}},
		Immutable:  ptr.To(true),
		Data:       data,
	}
}

func fabric(data map[string]string) *corev1.ConfigMap {
	base := map[string]string{"name": "acme_fabric", "ansible_role": "acme.network.fabric", "network_types": "evpn"}
	for k, v := range data {
		base[k] = v
	}
	for k, v := range base {
		if v == "<delete>" {
			delete(base, k)
		}
	}
	return registration("network-manager-acme-fabric", networkmanager.LabelFabricManager, base)
}

func k8s(data map[string]string) *corev1.ConfigMap {
	base := map[string]string{"name": "cudn_evpn", "ansible_role": "osac.service.cudn_evpn", "network_type": "evpn"}
	for k, v := range data {
		base[k] = v
	}
	for k, v := range base {
		if v == "<delete>" {
			delete(base, k)
		}
	}
	return registration("network-manager-cudn-evpn", networkmanager.LabelK8sManager, base)
}

func resolve(eastWest bool, k8sName string, objs ...client.Object) (*networkmanager.Resolution, error) {
	scheme := runtime.NewScheme()
	Expect(corev1.AddToScheme(scheme)).To(Succeed())
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	d, err := networkmanager.NewDiscovery(c, ns)
	Expect(err).NotTo(HaveOccurred())
	return d.Resolve(context.Background(), "acme_fabric", k8sName, eastWest)
}

var _ = Describe("Resolve (TC-REG-01)", func() {
	It("resolves a valid Fabric and Kubernetes Manager and records the network type", func() {
		res, err := resolve(false, "cudn_evpn", fabric(nil), k8s(nil))
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Fabric.Name).To(Equal("acme_fabric"))
		Expect(res.Fabric.AnsibleRole).To(Equal("acme.network.fabric"))
		Expect(res.Kubernetes.AnsibleRole).To(Equal("osac.service.cudn_evpn"))
		Expect(res.NetworkType).To(Equal("evpn"))
	})

	It("leaves the network type empty without a Kubernetes Manager", func() {
		res, err := resolve(false, "", fabric(map[string]string{"network_types": "<delete>"}))
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Kubernetes).To(BeNil())
		Expect(res.NetworkType).To(BeEmpty())
	})

	It("accepts a key with an empty value as absent", func() {
		res, err := resolve(false, "", fabric(map[string]string{"capabilities": "", "description": ""}))
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Fabric.Capabilities).To(BeEmpty())
	})

	It("accepts eastWestEthernet when east-west is enabled", func() {
		_, err := resolve(true, "", fabric(map[string]string{"capabilities": "eastWestEthernet"}))
		Expect(err).NotTo(HaveOccurred())
	})

	It("accepts security_group_icmp on a Fabric and a Kubernetes Manager", func() {
		res, err := resolve(false, "cudn_evpn",
			fabric(map[string]string{"capabilities": "eastWestEthernet,security_group_icmp"}),
			k8s(map[string]string{"capabilities": "security_group_icmp"}))
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Fabric.HasCapability(networkmanager.CapabilitySecurityGroupICMP)).To(BeTrue())
		Expect(res.Kubernetes.HasCapability(networkmanager.CapabilitySecurityGroupICMP)).To(BeTrue())
	})

	DescribeTable("SecurityGroupICMP",
		func(k8sName string, want bool, objs ...client.Object) {
			res, err := resolve(false, k8sName, objs...)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.SecurityGroupICMP()).To(Equal(want))
		},
		Entry("Fabric alone declares it", "", true,
			fabric(map[string]string{"capabilities": "security_group_icmp"})),
		Entry("Fabric alone without it", "", false, fabric(nil)),
		Entry("both declare it", "cudn_evpn", true,
			fabric(map[string]string{"capabilities": "security_group_icmp"}),
			k8s(map[string]string{"capabilities": "security_group_icmp"})),
		Entry("only the Fabric Manager declares it", "cudn_evpn", false,
			fabric(map[string]string{"capabilities": "security_group_icmp"}), k8s(nil)),
		Entry("only the Kubernetes Manager declares it", "cudn_evpn", false,
			fabric(nil), k8s(map[string]string{"capabilities": "security_group_icmp"})),
	)

	It("rejects a malformed name", func() {
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "n", Namespace: ns},
			Immutable: ptr.To(true), Data: map[string]string{"name": "Acme-Fabric", "ansible_role": "a.b.c"}}
		_, err := networkmanager.ParseConfigMap(cm, networkmanager.FabricManager)
		Expect(err).To(MatchError(`ConfigMap osac-operator/n: name "Acme-Fabric" must start with a lowercase letter and use only lowercase letters, digits, and underscores`))
	})

	It("ignores a bad unrelated registration", func() {
		other := registration("other", networkmanager.LabelFabricManager, map[string]string{"name": "Bad Name", "typo": "x"})
		_, err := resolve(false, "", fabric(nil), other)
		Expect(err).NotTo(HaveOccurred())
	})

	DescribeTable("fails with the reason",
		func(k8sName string, eastWest bool, message string, objs ...client.Object) {
			_, err := resolve(eastWest, k8sName, objs...)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal(message))
		},
		Entry("no Fabric registration", "", false,
			`fabric manager "acme_fabric" matches no ConfigMap labelled osac.openshift.io/network-fabric-manager`),
		Entry("a Fabric name registered only as a Kubernetes Manager", "", false,
			`fabric manager "acme_fabric" matches no ConfigMap labelled osac.openshift.io/network-fabric-manager`,
			registration("x", networkmanager.LabelK8sManager, map[string]string{"name": "acme_fabric"})),
		Entry("two matching registrations", "", false,
			`fabric manager "acme_fabric" matches more than one ConfigMap: a, b`,
			registration("a", networkmanager.LabelFabricManager, map[string]string{"name": "acme_fabric"}),
			registration("b", networkmanager.LabelFabricManager, map[string]string{"name": "acme_fabric"})),
		Entry("both manager-type labels", "", false,
			`ConfigMap osac-operator/both carries both manager-type labels`,
			&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "both", Namespace: ns, Labels: map[string]string{
				networkmanager.LabelFabricManager: "true", networkmanager.LabelK8sManager: "true"}},
				Immutable: ptr.To(true), Data: map[string]string{"name": "acme_fabric", "ansible_role": "a.b.c"}}),
		Entry("not immutable", "", false,
			`ConfigMap osac-operator/network-manager-acme-fabric must set immutable: true`,
			func() client.Object { cm := fabric(nil); cm.Immutable = nil; return cm }()),
		Entry("immutable false", "", false,
			`ConfigMap osac-operator/network-manager-acme-fabric must set immutable: true`,
			func() client.Object { cm := fabric(nil); cm.Immutable = ptr.To(false); return cm }()),
		Entry("missing ansible_role", "", false,
			`ConfigMap osac-operator/network-manager-acme-fabric: ansible_role "" is not a fully qualified role name <namespace>.<collection>.<role>`,
			fabric(map[string]string{"ansible_role": "<delete>"})),
		Entry("malformed ansible_role", "", false,
			`ConfigMap osac-operator/network-manager-acme-fabric: ansible_role "acme.fabric" is not a fully qualified role name <namespace>.<collection>.<role>`,
			fabric(map[string]string{"ansible_role": "acme.fabric"})),
		Entry("unknown capability", "", false,
			`ConfigMap osac-operator/network-manager-acme-fabric: unknown capability "ipv4"`,
			fabric(map[string]string{"capabilities": "ipv4"})),
		Entry("repeated capability", "", false,
			`ConfigMap osac-operator/network-manager-acme-fabric: capabilities has an empty or repeated value "eastWestEthernet"`,
			fabric(map[string]string{"capabilities": "eastWestEthernet,eastWestEthernet"})),
		Entry("empty list entry", "", false,
			`ConfigMap osac-operator/network-manager-acme-fabric: network_types has an empty or repeated value ""`,
			fabric(map[string]string{"network_types": "evpn,"})),
		Entry("space in a list", "", false,
			`ConfigMap osac-operator/network-manager-acme-fabric: unknown network type " evpn"`,
			fabric(map[string]string{"network_types": "evpn, evpn"})),
		Entry("unknown network type", "", false,
			`ConfigMap osac-operator/network-manager-acme-fabric: unknown network type "vlan"`,
			fabric(map[string]string{"network_types": "vlan"})),
		Entry("typo networkTypes", "", false,
			`ConfigMap osac-operator/network-manager-acme-fabric: unknown data key "networkTypes" for a fabric manager registration`,
			fabric(map[string]string{"networkTypes": "evpn"})),
		Entry("network_type on a Fabric Manager", "", false,
			`ConfigMap osac-operator/network-manager-acme-fabric: unknown data key "network_type" for a fabric manager registration`,
			fabric(map[string]string{"network_type": "evpn"})),
		Entry("capabilities on a Kubernetes Manager", "cudn_evpn", false,
			`ConfigMap osac-operator/network-manager-cudn-evpn: unknown capability "eastWestEthernet"`,
			fabric(nil), k8s(map[string]string{"capabilities": "eastWestEthernet"})),
		Entry("no Kubernetes registration", "cudn_evpn", false,
			`kubernetes manager "cudn_evpn" matches no ConfigMap labelled osac.openshift.io/network-k8s-manager`,
			fabric(nil)),
		Entry("Kubernetes Manager without network_type", "cudn_evpn", false,
			`Kubernetes Manager "cudn_evpn" has no network_type`,
			fabric(nil), k8s(map[string]string{"network_type": ""})),
		Entry("Kubernetes Manager with an unknown network_type", "cudn_evpn", false,
			`ConfigMap osac-operator/network-manager-cudn-evpn: unknown network type "vlan"`,
			fabric(nil), k8s(map[string]string{"network_type": "vlan"})),
		Entry("Kubernetes Manager outside osac.*", "cudn_evpn", false,
			`Kubernetes Manager "cudn_evpn": ansible_role "acme.network.k8s" must be in an osac.* collection`,
			fabric(nil), k8s(map[string]string{"ansible_role": "acme.network.k8s"})),
		Entry("network type the Fabric Manager does not declare", "cudn_evpn", false,
			`Kubernetes Manager "cudn_evpn" network type "evpn" is not in the network_types of Fabric Manager "acme_fabric"`,
			fabric(map[string]string{"network_types": ""}), k8s(nil)),
		Entry("east-west without the capability", "", true,
			`east-west Ethernet is enabled but Fabric Manager "acme_fabric" does not declare eastWestEthernet`,
			fabric(nil)),
	)
})
