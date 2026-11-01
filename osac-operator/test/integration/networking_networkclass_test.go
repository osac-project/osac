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

package integration

import (
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// NetworkClass resolution by the deployed operator from manager registration
// ConfigMaps (contract §4.2, TC-REG-01). Registrations are synthetic and unique
// to each spec so these checks stay independent of provider-owned values.

const (
	// cudnEVPNRegistration is the ConfigMap the osac chart renders for the
	// cudn_evpn Kubernetes Manager (global.networking.k8sManager=cudn_evpn).
	cudnEVPNRegistration = "osac-network-k8s-manager-cudn-evpn"
	// netrisRegistration is the ConfigMap the osac chart renders for the
	// Netris Fabric Manager (global.networking.fabricManager=netris).
	netrisRegistration = "osac-network-fabric-manager-netris"
	// netrisAnsibleRole is the role the Netris registration must name.
	netrisAnsibleRole = "osac.templates.netris"
)

// registration describes a manager registration ConfigMap.
type registration struct {
	configMap string
	labels    []string
	immutable bool
	data      map[string]string
}

func fabricRegistration(configMap, name string, data map[string]string) registration {
	base := map[string]string{"name": name, "ansible_role": "acme.network.fabric_" + name, "network_types": "evpn"}
	for k, v := range data {
		base[k] = v
	}
	for k, v := range base {
		if v == "<delete>" {
			delete(base, k)
		}
	}
	return registration{configMap: configMap, labels: []string{fabricManagerLabel}, immutable: true, data: base}
}

func k8sRegistration(configMap, name string, data map[string]string) registration {
	base := map[string]string{"name": name, "ansible_role": "osac.templates.k8s_" + name, "network_type": "evpn"}
	for k, v := range data {
		base[k] = v
	}
	for k, v := range base {
		if v == "<delete>" {
			delete(base, k)
		}
	}
	return registration{configMap: configMap, labels: []string{k8sManagerLabel}, immutable: true, data: base}
}

// createRegistration creates the ConfigMap and registers its deletion.
func createRegistration(r registration) {
	labels := map[string]any{}
	for _, label := range r.labels {
		labels[label] = "true"
	}
	data := map[string]any{}
	for k, v := range r.data {
		data[k] = v
	}
	cm := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": r.configMap, "namespace": operatorNamespace, "labels": labels},
		"data":     data,
	}}
	if r.immutable {
		cm.Object["immutable"] = true
	}
	By(
		fmt.Sprintf(
			"creating registration ConfigMap %s labels=%v immutable=%v data=%v",
			r.configMap,
			r.labels,
			r.immutable,
			r.data,
		),
	)
	output, err := kubectl(toJSON(cm), "create", "-f", "-")
	Expect(err).NotTo(HaveOccurred(), output)
	DeferCleanup(func() { deleteRegistration(r.configMap) })
}

func deleteRegistration(configMap string) {
	_, err := kubectl("", "delete", "configmap", configMap, "-n", operatorNamespace, "--ignore-not-found", "--wait=true")
	Expect(err).NotTo(HaveOccurred())
}

// networkClassSpec returns a valid NetworkClass spec for the given managers.
func networkClassSpec(fabric, k8s string, eastWest bool) map[string]any {
	spec := validNetworkClassSpec()
	spec["fabricManager"] = fabric
	if k8s == "" {
		delete(spec, "k8sManager")
		spec["addressPlan"] = map[string]any{"overlay": int64(0), "fabric": int64(12), "lbVip": int64(4)}
	} else {
		spec["k8sManager"] = k8s
	}
	if eastWest {
		spec["eastWestCapabilities"] = map[string]any{"supportsEastWestEthernet": true}
	}
	return spec
}

// createNetworkClass creates a managed NetworkClass; the deployed operator
// resolves it. The NetworkClass is deleted (and waited for) at spec end.
func createNetworkClass(name, fabric, k8s string, eastWest bool) {
	obj := withAPIID(newObject("NetworkClass", name, networkClassSpec(fabric, k8s, eastWest)), name)
	mustCreate(obj)
}

// expectNetworkClassFailed waits for Ready=False Failed with the exact message
// and checks that nothing was resolved.
func expectNetworkClassFailed(name, message string) {
	nc := eventuallyReady("NetworkClass", name, "False", "Failed", message)
	status, _, _ := unstructured.NestedMap(nc.Object, "status")
	Expect(status).NotTo(HaveKey("networkManagers"), "a Failed NetworkClass resolves no manager: %v", status)
	Expect(status).NotTo(HaveKey("networkType"), "a Failed NetworkClass has no network type: %v", status)
	Expect(status).NotTo(HaveKey("securityGroupIcmp"), "a Failed NetworkClass records no capability: %v", status)
	expectNoJobs(nc)
}

// expectNetworkClassReady waits for Ready=True Succeeded and checks the
// resolved managers and network type (empty k8sName: no Kubernetes Manager).
func expectNetworkClassReady(
	name, fabricName, fabricRole, k8sName, k8sRole, networkType string,
) *unstructured.Unstructured {
	nc := eventuallyReady("NetworkClass", name, "True", "Succeeded", "")
	managers, _, _ := unstructured.NestedMap(nc.Object, "status", "networkManagers")
	Expect(managers).To(HaveKeyWithValue("fabric", map[string]any{"name": fabricName, "ansibleRole": fabricRole}),
		"status.networkManagers: %v", managers)
	if k8sName == "" {
		Expect(managers).NotTo(HaveKey("kubernetes"), "no Kubernetes Manager was selected: %v", managers)
	} else {
		Expect(managers).To(HaveKeyWithValue("kubernetes", map[string]any{"name": k8sName, "ansibleRole": k8sRole}),
			"status.networkManagers: %v", managers)
	}
	got, _, _ := unstructured.NestedString(nc.Object, "status", "networkType")
	Expect(got).To(Equal(networkType), "status.networkType")
	expectNoJobs(nc)
	return nc
}

// expectSecurityGroupICMP checks the derived status.securityGroupIcmp of a resolved NetworkClass.
func expectSecurityGroupICMP(nc *unstructured.Unstructured, want bool) {
	got, found, err := unstructured.NestedBool(nc.Object, "status", "securityGroupIcmp")
	Expect(err).NotTo(HaveOccurred())
	Expect(found).To(BeTrue(), "a resolved NetworkClass records status.securityGroupIcmp: %v", nc.Object["status"])
	Expect(got).To(Equal(want), "status.securityGroupIcmp")
}

// skipResolutionWhenDisabled skips a resolution spec on an operator deployed
// with provider networking disabled, which marks the NetworkClass Ready without
// reading the registrations (unified design, "Provider networking disabled").
// internal/controller/networkclass_controller_test.go covers resolution.
func skipResolutionWhenDisabled() {
	if operatorDeploymentEnv("OSAC_PROVISIONING_ENABLED") == "false" {
		Skip("provider networking is disabled: the operator does not resolve manager registrations")
	}
}

var _ = Describe("NetworkClass resolution "+
	"by the deployed operator (TC-REG-01)", Label("networking", "networkclass"), func() {
	var sfx, fabricName, k8sName, fabricCM, k8sCM, ncName string

	BeforeEach(func() {
		sfx = uniqueSuffix()
		fabricName, k8sName = "itf_"+sfx, "itk_"+sfx
		fabricCM, k8sCM = "it-fabric-"+sfx, "it-k8s-"+sfx
		ncName = "nc-it-" + sfx
		By("checking that no other NetworkClass exists (a deployment has exactly one)")
		Expect(listNames("NetworkClass")).To(BeEmpty(), "leftover NetworkClasses would make the networking controllers wait")
	})

	It("resolves Fabric and Kubernetes Manager registrations and records evpn", func() {
		skipResolutionWhenDisabled()
		createRegistration(fabricRegistration(fabricCM, fabricName, nil))
		createRegistration(k8sRegistration(k8sCM, k8sName, nil))
		createNetworkClass(ncName, fabricName, k8sName, false)
		expectNetworkClassReady(
			ncName,
			fabricName,
			"acme.network.fabric_"+fabricName,
			k8sName,
			"osac.templates.k8s_"+k8sName,
			"evpn",
		)
	})

	It("resolves the chart-rendered Netris and cudn_evpn registrations", func() {
		skipResolutionWhenDisabled()
		By("checking that the chart rendered the cudn_evpn registration")
		output, err := kubectl("", "get", "configmap", cudnEVPNRegistration, "-n", operatorNamespace,
			"-o", "jsonpath={.metadata.labels}")
		Expect(err).NotTo(HaveOccurred(), "the installer must render the cudn_evpn registration "+
			"(global.networking.k8sManager=cudn_evpn): %s", output)
		Expect(output).To(ContainSubstring(k8sManagerLabel))

		By("checking that the chart rendered the Netris registration")
		output, err = kubectl("", "get", "configmap", netrisRegistration, "-n", operatorNamespace,
			"-o", "jsonpath={.metadata.labels}")
		Expect(err).NotTo(HaveOccurred(), "the installer must render the Netris registration "+
			"(global.networking.fabricManager=netris): %s", output)
		labelValue, err := kubectl("", "get", "configmap", netrisRegistration, "-n", operatorNamespace,
			"-o", `jsonpath={.metadata.labels.osac\.openshift\.io/network-fabric-manager}`)
		Expect(err).NotTo(HaveOccurred(), labelValue)
		Expect(labelValue).To(Equal("true"))
		netrisData, err := kubectl("", "get", "configmap", netrisRegistration, "-n", operatorNamespace,
			"-o", "jsonpath={.data.ansible_role} {.data.network_types}")
		Expect(err).NotTo(HaveOccurred(), netrisData)
		Expect(strings.Fields(netrisData)).To(Equal([]string{netrisAnsibleRole, "evpn"}),
			"the Netris registration must name its ansible_role and the evpn network type")

		createNetworkClass(ncName, "netris", "cudn_evpn", false)
		nc := expectNetworkClassReady(
			ncName,
			"netris",
			netrisAnsibleRole,
			"cudn_evpn",
			"osac.templates.cudn_evpn",
			"evpn",
		)
		Expect(nc.Object["status"]).NotTo(HaveKey("jobs"), "resolution runs no job")
		expectSecurityGroupICMP(nc, false)
	})

	It("resolves the chart-rendered Netris registration alone with security_group_icmp", func() {
		skipResolutionWhenDisabled()
		createNetworkClass(ncName, "netris", "", false)
		nc := expectNetworkClassReady(ncName, "netris", netrisAnsibleRole, "", "", "")
		expectSecurityGroupICMP(nc, true)
	})

	It("records security_group_icmp when both registrations declare it", func() {
		skipResolutionWhenDisabled()
		createRegistration(fabricRegistration(fabricCM, fabricName, map[string]string{"capabilities": "security_group_icmp"}))
		createRegistration(k8sRegistration(k8sCM, k8sName, map[string]string{"capabilities": "security_group_icmp"}))
		createNetworkClass(ncName, fabricName, k8sName, false)
		nc := expectNetworkClassReady(
			ncName,
			fabricName,
			"acme.network.fabric_"+fabricName,
			k8sName,
			"osac.templates.k8s_"+k8sName,
			"evpn",
		)
		expectSecurityGroupICMP(nc, true)
	})

	It("resolves a Fabric Manager alone, accepts an empty key as absent and leaves the network type empty", func() {
		skipResolutionWhenDisabled()
		createRegistration(fabricRegistration(fabricCM, fabricName, map[string]string{"capabilities": "", "description": ""}))
		createNetworkClass(ncName, fabricName, "", false)
		expectNetworkClassReady(ncName, fabricName, "acme.network.fabric_"+fabricName, "", "", "")
	})

	It("resolves east-west Ethernet when the Fabric Manager declares eastWestEthernet", func() {
		skipResolutionWhenDisabled()
		createRegistration(fabricRegistration(fabricCM, fabricName, map[string]string{"capabilities": "eastWestEthernet"}))
		createRegistration(k8sRegistration(k8sCM, k8sName, nil))
		createNetworkClass(ncName, fabricName, k8sName, true)
		expectNetworkClassReady(
			ncName,
			fabricName,
			"acme.network.fabric_"+fabricName,
			k8sName,
			"osac.templates.k8s_"+k8sName,
			"evpn",
		)
	})

	It("fails while the Fabric Manager is missing and resolves once its registration is created", func() {
		skipResolutionWhenDisabled()
		createNetworkClass(ncName, fabricName, "", false)
		expectNetworkClassFailed(ncName, fmt.Sprintf(
			"fabric manager %q matches no ConfigMap labelled %s", fabricName, fabricManagerLabel))

		By("creating the registration: the operator re-resolves the Failed NetworkClass")
		createRegistration(fabricRegistration(fabricCM, fabricName, nil))
		expectNetworkClassReady(ncName, fabricName, "acme.network.fabric_"+fabricName, "", "", "")
	})

	It("fails on a duplicate registration naming both ConfigMaps and resolves once one is deleted", func() {
		skipResolutionWhenDisabled()
		first, second := fabricCM+"-a", fabricCM+"-b"
		createRegistration(fabricRegistration(first, fabricName, nil))
		createRegistration(fabricRegistration(second, fabricName, nil))
		createNetworkClass(ncName, fabricName, "", false)
		expectNetworkClassFailed(ncName, fmt.Sprintf(
			"fabric manager %q matches more than one ConfigMap: %s, %s", fabricName, first, second))

		By("deleting one of the duplicates: the operator re-resolves the Failed NetworkClass")
		deleteRegistration(second)
		expectNetworkClassReady(ncName, fabricName, "acme.network.fabric_"+fabricName, "", "", "")
	})

	It("keeps a Ready NetworkClass Ready when its registration is deleted", func() {
		skipResolutionWhenDisabled()
		createRegistration(fabricRegistration(fabricCM, fabricName, nil))
		createNetworkClass(ncName, fabricName, "", false)
		before := expectNetworkClassReady(ncName, fabricName, "acme.network.fabric_"+fabricName, "", "", "")

		By("deleting the registration of the resolved Fabric Manager")
		deleteRegistration(fabricCM)
		Consistently(func(g Gomega) {
			nc, err := getObject("NetworkClass", ncName)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(readyCondition(nc)).To(HaveKeyWithValue("status", "True"))
			g.Expect(nc.Object["status"]).
				To(HaveKeyWithValue("networkManagers", before.Object["status"].(map[string]any)["networkManagers"]))
		}, 20*time.Second, 2*time.Second).Should(Succeed(), "a Ready NetworkClass is never resolved again")
	})

	type invalidCase struct {
		fabric   map[string]string
		k8s      map[string]string // nil: no Kubernetes Manager registration
		k8sName  bool              // the NetworkClass selects the Kubernetes Manager
		eastWest bool
		both     bool // the Fabric registration also carries the Kubernetes label
		mutable  bool // the Fabric registration is not immutable
		message  func(fabricName, k8sName, fabricCM, k8sCM string) string
	}
	DescribeTable(
		"sets Failed with the exact reason for an invalid registration or selection",
		func(c invalidCase) {
			skipResolutionWhenDisabled()
			fabric := fabricRegistration(fabricCM, fabricName, c.fabric)
			fabric.immutable = !c.mutable
			if c.both {
				fabric.labels = append(fabric.labels, k8sManagerLabel)
			}
			if c.fabric["name"] != "" {
				fabricName = c.fabric["name"]
			}
			createRegistration(fabric)
			selected := ""
			if c.k8sName {
				selected = k8sName
			}
			if c.k8s != nil {
				createRegistration(k8sRegistration(k8sCM, k8sName, c.k8s))
			}
			createNetworkClass(ncName, fabricName, selected, c.eastWest)
			expectNetworkClassFailed(ncName, c.message(fabricName, k8sName, fabricCM, k8sCM))
		},
		Entry("a registration that is not immutable", invalidCase{mutable: true,
			message: func(_, _, cm, _ string) string {
				return fmt.Sprintf("ConfigMap %s/%s must set immutable: true", operatorNamespace, cm)
			}}),
		Entry("network_type on a Fabric Manager", invalidCase{fabric: map[string]string{"network_type": "evpn"},
			message: func(_, _, cm, _ string) string {
				return fmt.Sprintf(
					"ConfigMap %s/%s: unknown data key %q for a fabric manager registration",
					operatorNamespace,
					cm,
					"network_type",
				)
			}}),
		Entry("the typo networkTypes on a Fabric Manager", invalidCase{fabric: map[string]string{"networkTypes": "evpn"},
			message: func(_, _, cm, _ string) string {
				return fmt.Sprintf(
					"ConfigMap %s/%s: unknown data key %q for a fabric manager registration",
					operatorNamespace,
					cm,
					"networkTypes",
				)
			}}),
		Entry(
			"capabilities on a Kubernetes Manager",
			invalidCase{k8s: map[string]string{"capabilities": "eastWestEthernet"}, k8sName: true,
				message: func(_, _, _, cm string) string {
					return fmt.Sprintf(
						"ConfigMap %s/%s: unknown capability %q",
						operatorNamespace,
						cm,
						"eastWestEthernet",
					)
				}},
		),
		Entry("an invalid manager name", invalidCase{fabric: map[string]string{"name": "Bad-Fabric"},
			message: func(name, _, cm, _ string) string {
				return fmt.Sprintf(
					"ConfigMap %s/%s: name %q must start with a lowercase letter "+
						"and use only lowercase letters, digits, and underscores",
					operatorNamespace,
					cm,
					name,
				)
			}}),
		Entry("an ansible_role that is not fully qualified", invalidCase{fabric: map[string]string{"ansible_role": "netris"},
			message: func(_, _, cm, _ string) string {
				return fmt.Sprintf(
					"ConfigMap %s/%s: ansible_role %q is not a fully qualified role name <namespace>.<collection>.<role>",
					operatorNamespace,
					cm,
					"netris",
				)
			}}),
		Entry("an unknown capability", invalidCase{fabric: map[string]string{"capabilities": "infiniband"},
			message: func(_, _, cm, _ string) string {
				return fmt.Sprintf("ConfigMap %s/%s: unknown capability %q", operatorNamespace, cm, "infiniband")
			}}),
		Entry("a repeated network type", invalidCase{fabric: map[string]string{"network_types": "evpn,evpn"},
			message: func(_, _, cm, _ string) string {
				return fmt.Sprintf(
					"ConfigMap %s/%s: network_types has an empty or repeated value %q",
					operatorNamespace,
					cm,
					"evpn",
				)
			}}),
		Entry(
			"a Kubernetes Manager without network_type",
			invalidCase{k8s: map[string]string{"network_type": "<delete>"}, k8sName: true,
				message: func(_, k8s, _, _ string) string {
					return fmt.Sprintf("Kubernetes Manager %q has no network_type", k8s)
				}},
		),
		Entry(
			"a Kubernetes Manager role outside osac.*",
			invalidCase{k8s: map[string]string{"ansible_role": "acme.net.k8s"}, k8sName: true,
				message: func(_, k8s, _, _ string) string {
					return fmt.Sprintf("Kubernetes Manager %q: ansible_role %q must be in an osac.* collection", k8s, "acme.net.k8s")
				}},
		),
		Entry("a Kubernetes Manager network type the Fabric Manager does not declare (names both)",
			invalidCase{fabric: map[string]string{"network_types": "<delete>"}, k8s: map[string]string{}, k8sName: true,
				message: func(fabric, k8s, _, _ string) string {
					return fmt.Sprintf(
						"Kubernetes Manager %q network type %q is not in the network_types of Fabric Manager %q",
						k8s,
						"evpn",
						fabric,
					)
				}}),
		Entry("a missing Kubernetes Manager", invalidCase{k8sName: true,
			message: func(_, k8s, _, _ string) string {
				return fmt.Sprintf("kubernetes manager %q matches no ConfigMap labelled %s", k8s, k8sManagerLabel)
			}}),
		Entry("east-west Ethernet without eastWestEthernet", invalidCase{eastWest: true,
			message: func(fabric, _, _, _ string) string {
				return fmt.Sprintf("east-west Ethernet is enabled but Fabric Manager %q does not declare eastWestEthernet", fabric)
			}}),
		Entry("a registration carrying both manager-type labels", invalidCase{both: true,
			message: func(_, _, cm, _ string) string {
				return fmt.Sprintf("ConfigMap %s/%s carries both manager-type labels", operatorNamespace, cm)
			}}),
	)

	It("rejects data changes to manager registrations because they are immutable ConfigMaps", func() {
		createRegistration(fabricRegistration(fabricCM, fabricName, nil))
		createRegistration(k8sRegistration(k8sCM, k8sName, nil))
		// The installer-rendered registrations must be immutable too.
		for _, configMap := range []string{fabricCM, k8sCM, netrisRegistration, cudnEVPNRegistration} {
			By(fmt.Sprintf("changing the ansible_role of %s", configMap))
			output, err := kubectl("", "patch", "configmap", configMap, "-n", operatorNamespace,
				"--type=merge", "-p", `{"data":{"ansible_role":"acme.other.role"}}`)
			Expect(err).To(HaveOccurred(), "an immutable registration must not change")
			Expect(strings.ToLower(output)).To(ContainSubstring("immutable"))
			after, err := kubectl(
				"",
				"get",
				"configmap",
				configMap,
				"-n",
				operatorNamespace,
				"-o",
				"jsonpath={.data.ansible_role}",
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(after).NotTo(Equal("acme.other.role"))
		}
	})
})
