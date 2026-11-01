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

package v1alpha1_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

var _ = Describe("NetworkJobStatus", func() {
	It("round-trips the six contract fields with their JSON names", func() {
		in := v1alpha1.NetworkJobStatus{
			Task:    "apply_network_attachment",
			Manager: v1alpha1.NetworkJobManagerKubernetes,
			Attempt: 1,
			State:   v1alpha1.NetworkJobStateSucceeded,
			JobID:   "1234",
			Message: "",
		}
		data, err := json.Marshal(in)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(MatchJSON(`{"task":"apply_network_attachment","manager":"kubernetes","attempt":1,"state":"Succeeded","jobID":"1234"}`))
		var out v1alpha1.NetworkJobStatus
		Expect(json.Unmarshal(data, &out)).To(Succeed())
		Expect(out).To(Equal(in))
	})
})

var _ = Describe("Subnet status", func() {
	It("keeps the stored task input verbatim", func() {
		subnet := &v1alpha1.Subnet{}
		subnet.SetTaskInput(&runtime.RawExtension{Raw: []byte(`{"resource":{"kind":"Subnet","id":"a"}}`)})
		subnet.Status.IPAM = &v1alpha1.SubnetIPAM{GatewayIPv4: "10.0.1.1", OverlayIPv4CIDRs: []string{"10.0.1.0/25"}}
		subnet.Status.NetworkSegment = &v1alpha1.NetworkSegment{Type: v1alpha1.NetworkTypeEVPN, L2VNI: 10100}
		data, err := json.Marshal(subnet.Status)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(MatchJSON(`{
			"taskInput":{"resource":{"kind":"Subnet","id":"a"}},
			"ipam":{"gatewayIpv4":"10.0.1.1","overlayIpv4Cidrs":["10.0.1.0/25"]},
			"networkSegment":{"type":"evpn","l2Vni":10100}
		}`))
	})
})

var _ = Describe("Accessors", func() {
	It("expose jobs, task input and managers of the networking CRs", func() {
		objs := []v1alpha1.NetworkResourceObject{
			&v1alpha1.VirtualNetwork{}, &v1alpha1.Subnet{}, &v1alpha1.SecurityGroup{},
			&v1alpha1.ExternalIPPool{}, &v1alpha1.ExternalIP{}, &v1alpha1.ExternalIPAttachment{},
			&v1alpha1.NATGateway{}, &v1alpha1.FabricDomain{}, &v1alpha1.NetworkAttachment{},
		}
		for _, obj := range objs {
			obj.SetNetworkJobs([]v1alpha1.NetworkJobStatus{{Task: "t", Manager: v1alpha1.NetworkJobManagerFabric}})
			Expect(obj.GetNetworkJobs()).To(HaveLen(1))
			obj.SetNetworkManagers(&v1alpha1.NetworkManagers{Fabric: &v1alpha1.NetworkManagerRef{Name: "f", AnsibleRole: "a.b.c"}})
			Expect(obj.GetNetworkManagers().Fabric.Name).To(Equal("f"))
			obj.SetTaskInput(&runtime.RawExtension{Raw: []byte(`{}`)})
			Expect(obj.GetTaskInput()).NotTo(BeNil())
			Expect(obj.GetConditionsRef()).NotTo(BeNil())
		}
	})
})

var _ = Describe("NetworkAttachmentName", func() {
	It("is the lowercase target kind and the target API ID", func() {
		Expect(v1alpha1.NetworkAttachmentName(v1alpha1.NetworkAttachmentTargetComputeInstance, "abc")).To(Equal("computeinstance-abc"))
		Expect(v1alpha1.NetworkAttachmentName(v1alpha1.NetworkAttachmentTargetBareMetalInstance, "abc")).To(Equal("baremetalinstance-abc"))
	})
})
