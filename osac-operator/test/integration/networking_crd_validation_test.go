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

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// CRD validation of the installed networking CRDs on the real API server
// (Unified design, Kubernetes resources; contract §4.8, §4.9). Every CR here
// is unmanaged, so the deployed controllers ignore it and only the API server
// decides. Envtest (internal/controller/networking_crd_contract_test.go)
// covers the same rules against the generated CRDs; this suite proves the
// CRDs the installer actually ships.

func validAddressPlan() map[string]any {
	return map[string]any{"overlay": int64(8), "fabric": int64(4), "lbVip": int64(4)}
}

func validNetworkClassSpec() map[string]any {
	return map[string]any{
		"fabricManager": "netris",
		"k8sManager":    "cudn_evpn",
		"defaults": map[string]any{
			"virtualNetworkIpv4Cidr": "10.0.0.0/16",
			"subnetIpv4Cidr":         "10.0.0.0/24",
			"ingressRules": []any{
				map[string]any{"protocol": "tcp", "portFrom": int64(22), "portTo": int64(22), "sourceCidr": "0.0.0.0/0"},
			},
			"egressRules": []any{
				map[string]any{"protocol": "all", "destinationCidr": "0.0.0.0/0"},
			},
		},
		"addressPlan": validAddressPlan(),
	}
}

func validNetworkAttachmentSpec(kind, interfaceType string) map[string]any {
	iface := map[string]any{"type": interfaceType}
	if interfaceType == "physical" {
		iface["name"] = "eth1"
		iface["macAddress"] = "52:54:00:aa:bb:cc"
	}
	return map[string]any{
		"target":         map[string]any{"kind": kind, "id": "workload-" + uniqueSuffix()},
		"subnet":         "subnet-" + uniqueSuffix(),
		"interface":      iface,
		"securityGroups": []any{"sg-" + uniqueSuffix()},
	}
}

// mutate returns a deep copy of spec changed by change.
func mutate(spec map[string]any, change func(map[string]any)) map[string]any {
	copied := (&unstructured.Unstructured{Object: map[string]any{"spec": spec}}).DeepCopy().Object["spec"].(map[string]any)
	change(copied)
	return copied
}

func crdName(prefix string) string {
	return fmt.Sprintf("crd-%s-%s", prefix, uniqueSuffix())
}

var _ = Describe("Networking CRD validation on the installed CRDs", Label("networking", "crd"), func() {
	Context("NetworkClass", func() {
		It("accepts a valid NetworkClass and keeps its whole spec immutable", func() {
			name := crdName("nc")
			mustCreate(unmanaged(newObject("NetworkClass", name, validNetworkClassSpec())))
			logObject("NetworkClass", name)

			expectPatchRejected("NetworkClass", name, `{"spec":{"fabricManager":"other"}}`, "fabricManager is immutable")
			expectPatchRejected("NetworkClass", name, `{"spec":{"k8sManager":"other"}}`, "k8sManager is immutable")
			expectPatchRejected("NetworkClass", name, `{"spec":{"k8sManager":null}}`, "k8sManager is immutable")
			expectPatchRejected("NetworkClass", name,
				`{"spec":{"defaults":{"subnetIpv4Cidr":"10.0.1.0/24"}}}`, "defaults is immutable")
			expectPatchRejected("NetworkClass", name,
				`{"spec":{"eastWestCapabilities":{"supportsEastWestEthernet":true}}}`, "eastWestCapabilities is immutable")

			By("rejecting an addressPlan change: the NetworkClass spec is immutable")
			expectPatchRejected("NetworkClass", name,
				`{"spec":{"addressPlan":{"overlay":12,"fabric":4,"lbVip":0}}}`, "addressPlan is immutable")
		})

		It("rejects an address plan that does not sum to 16 sixteenths or has a share above 16", func() {
			expectCreateRejected(
				unmanaged(newObject("NetworkClass", crdName("nc-sum"), mutate(validNetworkClassSpec(), func(s map[string]any) {
					s["addressPlan"] = map[string]any{"overlay": int64(8), "fabric": int64(4), "lbVip": int64(3)}
				}))),
				"addressPlan shares must sum to 16 sixteenths",
			)
			expectCreateRejected(
				unmanaged(newObject("NetworkClass", crdName("nc-max"), mutate(validNetworkClassSpec(), func(s map[string]any) {
					s["addressPlan"] = map[string]any{"overlay": int64(17), "fabric": int64(0), "lbVip": int64(0)}
				}))),
				"spec.addressPlan.overlay",
			)
			expectCreateRejected(
				unmanaged(newObject("NetworkClass", crdName("nc-plan"), mutate(validNetworkClassSpec(), func(s map[string]any) {
					delete(s, "addressPlan")
				}))),
				"spec.addressPlan: Required value",
			)
		})

		It("rejects non-canonical or IPv6 default CIDRs and invalid default rules", func() {
			expectCreateRejected(
				unmanaged(newObject("NetworkClass", crdName("nc-vn"), mutate(validNetworkClassSpec(), func(s map[string]any) {
					s["defaults"].(map[string]any)["virtualNetworkIpv4Cidr"] = "10.0.0.1/16"
				}))),
				"virtualNetworkIpv4Cidr must be a canonical IPv4 CIDR",
			)
			expectCreateRejected(
				unmanaged(newObject("NetworkClass", crdName("nc-v6"), mutate(validNetworkClassSpec(), func(s map[string]any) {
					s["defaults"].(map[string]any)["subnetIpv4Cidr"] = "fd00::/64"
				}))),
				"subnetIpv4Cidr must be a canonical IPv4 CIDR",
			)
			expectCreateRejected(
				unmanaged(newObject("NetworkClass", crdName("nc-rule"), mutate(validNetworkClassSpec(), func(s map[string]any) {
					s["defaults"].(map[string]any)["ingressRules"] = []any{
						map[string]any{"protocol": "tcp", "sourceCidr": "10.0.0.0/8"},
					}
				}))),
				"portFrom and portTo are set exactly for tcp and udp rules",
			)
			expectCreateRejected(
				unmanaged(newObject("NetworkClass", crdName("nc-fm"), mutate(validNetworkClassSpec(), func(s map[string]any) {
					s["fabricManager"] = ""
				}))),
				"spec.fabricManager",
			)
		})
	})

	Context("VirtualNetwork", func() {
		It("accepts a canonical IPv4 CIDR, keeps the spec immutable and no longer has the old fields", func() {
			name := crdName("vn")
			created := mustCreate(unmanaged(withAPIID(newObject("VirtualNetwork", name,
				map[string]any{"ipv4Cidr": "10.20.0.0/16"}), name)))
			Expect(created.Object).NotTo(HaveKey("status"), "an unmanaged VirtualNetwork must not be reconciled")
			expectPatchRejected("VirtualNetwork", name, `{"spec":{"ipv4Cidr":"10.21.0.0/16"}}`, "immutable")

			By("rejecting the removed networkClass and region fields (strict field validation)")
			expectCreateRejected(unmanaged(newObject("VirtualNetwork", crdName("vn-old"),
				map[string]any{"ipv4Cidr": "10.22.0.0/16", "networkClass": "cudn-net", "region": "us-west-1"})),
				`unknown field "spec.networkClass"`, `unknown field "spec.region"`)
		})

		It("validates the L3 VNI in status", func() {
			name := crdName("vn-l3")
			mustCreate(unmanaged(withAPIID(newObject("VirtualNetwork", name,
				map[string]any{"ipv4Cidr": "10.23.0.0/16"}), name)))

			By("writing an L3 VNI outside 1..16777215 through the status subresource")
			for _, l3Vni := range []string{"0", "-1", "16777216"} {
				output, err := patchAs("", "VirtualNetwork", name, "merge",
					`{"status":{"l3Vni":`+l3Vni+`}}`, "status")
				Expect(err).To(HaveOccurred(), "l3Vni %s must be rejected", l3Vni)
				Expect(output).To(ContainSubstring("l3Vni"))
			}

			By("writing a valid L3 VNI")
			_, err := patchAs("", "VirtualNetwork", name, "merge", `{"status":{"l3Vni":20001}}`, "status")
			Expect(err).NotTo(HaveOccurred())
			stored, err := getObject("VirtualNetwork", name)
			Expect(err).NotTo(HaveOccurred())
			l3Vni, _, _ := unstructured.NestedInt64(stored.Object, "status", "l3Vni")
			Expect(l3Vni).To(Equal(int64(20001)))
		})

		It("rejects a non-canonical, IPv6 or missing CIDR", func() {
			expectCreateRejected(unmanaged(newObject("VirtualNetwork", crdName("vn-nc"),
				map[string]any{"ipv4Cidr": "10.20.0.1/16"})), "ipv4Cidr must be a canonical IPv4 CIDR")
			expectCreateRejected(unmanaged(newObject("VirtualNetwork", crdName("vn-v6"),
				map[string]any{"ipv4Cidr": "fd00::/48"})), "ipv4Cidr must be a canonical IPv4 CIDR")
			expectCreateRejected(unmanaged(newObject("VirtualNetwork", crdName("vn-none"),
				map[string]any{})), "spec.ipv4Cidr")
		})
	})

	Context("Subnet", func() {
		subnetSpec := func() map[string]any {
			return map[string]any{
				"virtualNetwork": "vn-" + uniqueSuffix(),
				"ipv4Cidr":       "10.30.1.0/24",
				"addressPlan":    validAddressPlan(),
			}
		}

		It("requires a valid address plan and a canonical CIDR", func() {
			expectCreateRejected(unmanaged(newObject("Subnet", crdName("sn-plan"), mutate(subnetSpec(), func(s map[string]any) {
				delete(s, "addressPlan")
			}))), "spec.addressPlan: Required value")
			expectCreateRejected(unmanaged(newObject("Subnet", crdName("sn-sum"), mutate(subnetSpec(), func(s map[string]any) {
				s["addressPlan"] = map[string]any{"overlay": int64(16), "fabric": int64(1), "lbVip": int64(0)}
			}))), "addressPlan shares must sum to 16 sixteenths")
			expectCreateRejected(unmanaged(newObject("Subnet", crdName("sn-cidr"), mutate(subnetSpec(), func(s map[string]any) {
				s["ipv4Cidr"] = "10.30.1.7/24"
			}))), "ipv4Cidr must be a canonical IPv4 CIDR")
		})

		It("keeps the spec immutable and validates the network segment in status", func() {
			name := crdName("sn")
			mustCreate(unmanaged(withAPIID(newObject("Subnet", name, subnetSpec()), name)))
			expectPatchRejected("Subnet", name, `{"spec":{"virtualNetwork":"other"}}`, "immutable")
			expectPatchRejected(
				"Subnet",
				name,
				`{"spec":{"addressPlan":{"overlay":16,"fabric":0,"lbVip":0}}}`,
				"spec is immutable after creation",
			)

			By("writing a network segment whose L2 VNI is out of range through the status subresource")
			output, err := patchAs("", "Subnet", name, "merge",
				`{"status":{"networkSegment":{"type":"evpn","l2Vni":16777216}}}`, "status")
			Expect(err).To(HaveOccurred(), "an L2 VNI above 16777215 must be rejected")
			Expect(output).To(ContainSubstring("l2Vni"))

			By("rejecting a segment without a type")
			output, err = patchAs("", "Subnet", name, "merge",
				`{"status":{"networkSegment":{"l2Vni":100}}}`, "status")
			Expect(err).To(HaveOccurred(), "a segment needs its network type")
			Expect(output).To(ContainSubstring("type"))

			By("pruning the removed networkSegment.l3Vni field")
			_, err = patchAs("", "Subnet", name, "merge",
				`{"status":{"networkSegment":{"type":"evpn","l2Vni":100,"l3Vni":5}}}`, "status")
			Expect(err).NotTo(HaveOccurred())
			stored, err := getObject("Subnet", name)
			Expect(err).NotTo(HaveOccurred())
			segment, _, _ := unstructured.NestedMap(stored.Object, "status", "networkSegment")
			Expect(segment).To(Equal(map[string]any{"type": "evpn", "l2Vni": int64(100)}),
				"the installed CRD stores only {type, l2Vni}")

			By("writing two job entries with the same task and manager")
			output, err = patchAs("", "Subnet", name, "merge",
				`{"status":{"jobs":[{"task":"create_subnet","manager":"fabric","attempt":1,"state":"Running"},`+
					`{"task":"create_subnet","manager":"fabric","attempt":2,"state":"Running"}]}}`, "status")
			Expect(err).To(HaveOccurred(), "status.jobs keeps one entry per task and manager")
			Expect(output).To(ContainSubstring("Duplicate value"))
			logObject("Subnet", name)
		})
	})

	Context("SecurityGroup", func() {
		sgSpec := func(ingress, egress []any) map[string]any {
			spec := map[string]any{"virtualNetwork": "vn-" + uniqueSuffix()}
			if ingress != nil {
				spec["ingressRules"] = ingress
			}
			if egress != nil {
				spec["egressRules"] = egress
			}
			return spec
		}
		rule := func(protocol string, from, to any, source, destination string) map[string]any {
			r := map[string]any{"protocol": protocol}
			if from != nil {
				r["portFrom"] = from
			}
			if to != nil {
				r["portTo"] = to
			}
			if source != "" {
				r["sourceCidr"] = source
			}
			if destination != "" {
				r["destinationCidr"] = destination
			}
			return r
		}

		It("accepts ports exactly for tcp and udp rules and rejects every other combination", func() {
			name := crdName("sg")
			mustCreate(unmanaged(withAPIID(newObject("SecurityGroup", name, sgSpec(
				[]any{rule("tcp", int64(22), int64(22), "10.0.0.0/8", ""), rule("udp", int64(53), int64(54), "10.0.0.0/8", ""),
					rule("icmp", nil, nil, "10.0.0.0/8", ""), rule("all", nil, nil, "0.0.0.0/0", "")},
				[]any{rule("all", nil, nil, "", "0.0.0.0/0")})), name)))

			for i, invalid := range []struct {
				rule    map[string]any
				message string
			}{
				{rule("tcp", nil, nil, "10.0.0.0/8", ""), "portFrom and portTo are set exactly for tcp and udp rules"},
				{rule("udp", int64(53), nil, "10.0.0.0/8", ""), "portFrom and portTo are set exactly for tcp and udp rules"},
				{rule("icmp", int64(1), int64(1), "10.0.0.0/8", ""), "portFrom and portTo are set exactly for tcp and udp rules"},
				{
					rule("all", int64(1), int64(65535), "10.0.0.0/8", ""),
					"portFrom and portTo are set exactly for tcp and udp rules",
				},
				{rule("tcp", int64(443), int64(80), "10.0.0.0/8", ""), "portFrom must not exceed portTo"},
				{rule("tcp", int64(0), int64(80), "10.0.0.0/8", ""), "portFrom"},
				{rule("tcp", int64(1), int64(65536), "10.0.0.0/8", ""), "portTo"},
				{rule("sctp", nil, nil, "10.0.0.0/8", ""), "Unsupported value"},
				{rule("tcp", int64(22), int64(22), "10.0.0.1/8", ""), "sourceCidr must be a canonical IPv4 CIDR"},
				{rule("tcp", int64(22), int64(22), "10.0.0.0/8", "10.0.0.0/8"), "ingress rules must contain only a source CIDR"},
				{rule("tcp", int64(22), int64(22), "", ""), "ingress rules must contain only a source CIDR"},
			} {
				expectCreateRejected(unmanaged(newObject("SecurityGroup", fmt.Sprintf("%s-bad%d", name, i),
					sgSpec([]any{invalid.rule}, nil))), invalid.message)
			}
			expectCreateRejected(unmanaged(newObject("SecurityGroup", name+"-egress",
				sgSpec(nil, []any{rule("all", nil, nil, "10.0.0.0/8", "")}))), "egress rules must contain only a destination CIDR")
		})

		It("allows rule updates and keeps the VirtualNetwork immutable", func() {
			name := crdName("sg-upd")
			mustCreate(unmanaged(withAPIID(newObject("SecurityGroup", name,
				sgSpec([]any{rule("tcp", int64(22), int64(22), "10.0.0.0/8", "")}, nil)), name)))
			expectPatchAccepted("SecurityGroup", name,
				`{"spec":{"ingressRules":[{"protocol":"tcp","portFrom":443,"portTo":443,"sourceCidr":"0.0.0.0/0"}]}}`)
			expectPatchRejected("SecurityGroup", name, `{"spec":{"virtualNetwork":"other"}}`, "virtualNetwork is immutable")
			expectPatchRejected("SecurityGroup", name,
				`{"spec":{"ingressRules":[{"protocol":"tcp","sourceCidr":"0.0.0.0/0"}]}}`,
				"portFrom and portTo are set exactly for tcp and udp rules")
		})
	})

	Context("ExternalIPPool, ExternalIP, ExternalIPAttachment and NATGateway", func() {
		It("validates ExternalIPPool CIDRs and IP family and keeps the pool immutable", func() {
			name := crdName("pool")
			mustCreate(unmanaged(withAPIID(newObject("ExternalIPPool", name,
				map[string]any{"cidrs": []any{"192.0.2.0/24"}, "ipFamily": "IPv4"}), name)))
			expectPatchRejected("ExternalIPPool", name, `{"spec":{"cidrs":["198.51.100.0/24"]}}`, "immutable")
			expectCreateRejected(unmanaged(newObject("ExternalIPPool", name+"-v6",
				map[string]any{"cidrs": []any{"192.0.2.0/24"}, "ipFamily": "IPv6"})), "Unsupported value")
			expectCreateRejected(unmanaged(newObject("ExternalIPPool", name+"-two",
				map[string]any{"cidrs": []any{"192.0.2.0/24", "198.51.100.0/24"}, "ipFamily": "IPv4"})), "spec.cidrs")
			expectCreateRejected(unmanaged(newObject(
				"ExternalIPPool",
				name+"-nc",
				map[string]any{
					"cidrs":    []any{"192.0.2.1/24"},
					"ipFamily": "IPv4",
				},
			)), "cidrs must contain only canonical IPv4 CIDRs")
			expectCreateRejected(unmanaged(newObject("ExternalIPPool", name+"-none",
				map[string]any{"cidrs": []any{}, "ipFamily": "IPv4"})), "spec.cidrs")
		})

		It("keeps the ExternalIP pool and the NATGateway spec immutable", func() {
			eip := crdName("eip")
			mustCreate(unmanaged(withAPIID(newObject("ExternalIP", eip, map[string]any{"pool": "pool-a"}), eip)))
			expectPatchRejected("ExternalIP", eip, `{"spec":{"pool":"pool-b"}}`, "immutable")

			nat := crdName("nat")
			mustCreate(unmanaged(withAPIID(newObject("NATGateway", nat,
				map[string]any{"virtualNetwork": "vn-a", "externalIP": eip}), nat)))
			expectPatchRejected("NATGateway", nat, `{"spec":{"externalIP":"other"}}`, "spec is immutable after creation")
			expectCreateRejected(unmanaged(newObject("NATGateway", nat+"-bad",
				map[string]any{"virtualNetwork": "vn-a"})), "spec.externalIP")
		})

		It("requires exactly one ExternalIPAttachment target and a target endpoint exactly for a cluster", func() {
			base := crdName("eia")
			for i, invalid := range []struct {
				spec    map[string]any
				message string
			}{
				{map[string]any{"externalIP": "eip"}, "exactly one target field must be set"},
				{
					map[string]any{"externalIP": "eip", "computeInstance": "ci", "cluster": "co", "targetEndpoint": "API"},
					"exactly one target field must be set",
				},
				{map[string]any{"externalIP": "eip", "cluster": "co"}, "targetEndpoint is required when cluster is set"},
				{
					map[string]any{"externalIP": "eip", "computeInstance": "ci", "targetEndpoint": "API"},
					"targetEndpoint must not be set when computeInstance is set",
				},
				{
					map[string]any{"externalIP": "eip", "baremetalInstance": "bmi", "targetEndpoint": "Ingress"},
					"targetEndpoint must not be set when baremetalInstance is set",
				},
				{map[string]any{"externalIP": "eip", "cluster": "co", "targetEndpoint": "Console"}, "Unsupported value"},
			} {
				expectCreateRejected(
					unmanaged(newObject("ExternalIPAttachment", fmt.Sprintf("%s-bad%d", base, i), invalid.spec)),
					invalid.message,
				)
			}

			name := base + "-ok"
			mustCreate(unmanaged(withAPIID(newObject("ExternalIPAttachment", name,
				map[string]any{"externalIP": "eip", "cluster": "co", "targetEndpoint": "Ingress"}), name)))
			expectPatchRejected(
				"ExternalIPAttachment",
				name,
				`{"spec":{"targetEndpoint":"API"}}`,
				"spec is immutable after creation",
			)

			By("validating the routing domain of the destination in status")
			output, err := patchAs("", "ExternalIPAttachment", name, "merge",
				`{"status":{"destination":{"address":"10.0.0.5","routingDomain":{"type":"tenant_virtual_network"}}}}`, "status")
			Expect(err).To(HaveOccurred(), "a tenant routing domain needs its VirtualNetwork and Subnet IDs")
			Expect(output).To(ContainSubstring("virtualNetworkId and subnetId are set exactly for tenant_virtual_network"))
			_, err = patchAs(
				"",
				"ExternalIPAttachment",
				name,
				"merge",
				`{"status":{"destination":{"address":"10.0.0.5","routingDomain":`+
					`{"type":"tenant_virtual_network","virtualNetworkId":"vn","subnetId":"sn"}}}}`,
				"status",
			)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("FabricDomain", func() {
		It("accepts only ethernet_ew and keeps servers, type and VirtualNetwork fixed", func() {
			name := crdName("fd")
			mustCreate(unmanaged(withAPIID(newObject("FabricDomain", name, map[string]any{
				"type": "ethernet_ew", "servers": []any{"bmi-a", "bmi-b"}, "virtualNetwork": "vn-a",
			}), name)))
			expectPatchRejected("FabricDomain", name, `{"spec":{"servers":["bmi-a"]}}`,
				"FabricDomain servers cannot change in this release; delete and recreate the FabricDomain")
			expectPatchRejected("FabricDomain", name, `{"spec":{"servers":["bmi-a","bmi-b","bmi-c"]}}`,
				"FabricDomain servers cannot change in this release; delete and recreate the FabricDomain")
			expectPatchRejected("FabricDomain", name, `{"spec":{"virtualNetwork":"vn-b"}}`, "virtualNetwork is immutable")

			expectCreateRejected(unmanaged(newObject("FabricDomain", name+"-ib", map[string]any{
				"type": "infiniband", "servers": []any{"bmi-a"}, "virtualNetwork": "vn-a",
			})), "Unsupported value")
			expectCreateRejected(unmanaged(newObject("FabricDomain", name+"-none", map[string]any{
				"type": "ethernet_ew", "servers": []any{}, "virtualNetwork": "vn-a",
			})), "spec.servers")
		})
	})

	Context("NetworkAttachment", func() {
		It("accepts an overlay ComputeInstance and a physical BareMetalInstance attachment "+
			"and keeps the spec immutable", func() {
			overlay := crdName("na-ci")
			mustCreate(
				unmanaged(newObject("NetworkAttachment", overlay, validNetworkAttachmentSpec("ComputeInstance", "overlay"))),
			)
			physical := crdName("na-bmi")
			mustCreate(
				unmanaged(newObject("NetworkAttachment", physical, validNetworkAttachmentSpec("BareMetalInstance", "physical"))),
			)
			logObject("NetworkAttachment", physical)

			expectPatchRejected("NetworkAttachment", overlay, `{"spec":{"subnet":"other"}}`, "spec is immutable after creation")
			expectPatchRejected("NetworkAttachment", physical, `{"spec":{"interface":{"macAddress":"52:54:00:00:00:01"}}}`,
				"spec is immutable after creation")
			expectPatchRejected("NetworkAttachment", physical, `{"spec":{"securityGroups":["sg-other"]}}`,
				"spec is immutable after creation")
		})

		It("sets name and macAddress exactly for a physical interface and pairs ComputeInstance with overlay", func() {
			base := crdName("na")
			for i, invalid := range []struct {
				spec    map[string]any
				message string
			}{
				{mutate(validNetworkAttachmentSpec("BareMetalInstance", "physical"), func(s map[string]any) {
					delete(s["interface"].(map[string]any), "macAddress")
				}), "interface name and macAddress are set exactly for a physical interface"},
				{mutate(validNetworkAttachmentSpec("BareMetalInstance", "physical"), func(s map[string]any) {
					delete(s["interface"].(map[string]any), "name")
				}), "interface name and macAddress are set exactly for a physical interface"},
				{mutate(validNetworkAttachmentSpec("ComputeInstance", "overlay"), func(s map[string]any) {
					s["interface"].(map[string]any)["macAddress"] = "52:54:00:aa:bb:cc"
				}), "interface name and macAddress are set exactly for a physical interface"},
				{mutate(validNetworkAttachmentSpec("ComputeInstance", "physical"), keepSpec), wrongInterfaceKind},
				{mutate(validNetworkAttachmentSpec("BareMetalInstance", "overlay"), keepSpec), wrongInterfaceKind},
				{mutate(validNetworkAttachmentSpec("BareMetalInstance", "physical"), func(s map[string]any) {
					s["interface"].(map[string]any)["macAddress"] = "52:54:00:AA:BB:CC"
				}), "macAddress must be a lowercase colon-separated MAC address"},
				{mutate(validNetworkAttachmentSpec("BareMetalInstance", "physical"), func(s map[string]any) {
					s["interface"].(map[string]any)["macAddress"] = "52-54-00-aa-bb-cc"
				}), "macAddress must be a lowercase colon-separated MAC address"},
				{mutate(validNetworkAttachmentSpec("BareMetalInstance", "physical"), func(s map[string]any) {
					s["interface"].(map[string]any)["macAddress"] = "52:54:00:aa:bb"
				}), "macAddress must be a lowercase colon-separated MAC address"},
				{mutate(validNetworkAttachmentSpec("ComputeInstance", "overlay"), func(s map[string]any) {
					s["securityGroups"] = []any{}
				}), "spec.securityGroups"},
				{mutate(validNetworkAttachmentSpec("ComputeInstance", "overlay"), func(s map[string]any) {
					s["target"].(map[string]any)["kind"] = "ClusterOrder"
				}), "Unsupported value"},
				{mutate(validNetworkAttachmentSpec("ComputeInstance", "overlay"), func(s map[string]any) {
					s["securityGroups"] = []any{"sg-a", "sg-a"}
				}), "Duplicate value"},
			} {
				expectCreateRejected(
					unmanaged(newObject("NetworkAttachment", fmt.Sprintf("%s-bad%d", base, i), invalid.spec)),
					invalid.message,
				)
			}
		})
	})
})

// wrongInterfaceKind is the message of an attachment whose interface kind
// does not match its workload kind.
const wrongInterfaceKind = "a ComputeInstance has an overlay interface and a BareMetalInstance a physical one"

// keepSpec leaves a spec unchanged (for cases that only differ by the builder arguments).
func keepSpec(map[string]any) {}
