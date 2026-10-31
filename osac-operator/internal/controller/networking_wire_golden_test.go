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

package controller

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
	"github.com/osac-project/osac/osac-operator/internal/networktask"
)

// The operator <-> AAP wire contract. tests/contract/network-task holds the
// task input the operator launches for each networking task family
// (inputs/<task>.<manager>.json, the osac_job_vars of the AAP job) and the
// results a manager returns (results/<task>.<manager>.json, the job
// artifacts). osac-aap/tests/unit/test_network_task_wire_contract.py reads the
// same files, so a changed field name or shape fails on both sides.
//
// The spec drives the real reconcilers on envtest and a fake AAP. Each result
// golden is fed to the engine, which runs the real parser, so an accepted
// golden moves the resource on. Each launch is compared with its input golden
// after the run-specific names, UIDs and namespace are replaced by fixed
// values. Run with UPDATE_GOLDEN=1 to rewrite the input goldens, then review
// the diff: it is the wire change.
const wireGoldenDir = "../../../tests/contract/network-task"

// Fixed API IDs. They match the IDs the osac-aap tests use for the same kinds.
const (
	wireVN     = "3f1c9a2e-7b4d-4e8a-9c6f-0d2e4b6a8c10"
	wireSubnet = "8a2b4c6d-1e3f-4a5b-9c7d-2e4f6a8b0c12"
	wirePool   = "5d6a0f1e-2b3c-4d5e-8f90-a1b2c3d4e5f6"
	wireEIP    = "7b6a5c4d-3e2f-4a1b-9c8d-7e6f5a4b3c2d"
	wireEIPA   = "9c8b7a6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d"
	wireNATGW  = "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d"
	wireVM     = "0f1e2d3c-4b5a-4968-8776-655443322110"
	wireSG     = "6a5b4c3d-2e1f-4a0b-9c8d-7e6f5a4b3c2e"
)

const wireNamespace = "osac-networking"

// wireGolden normalises launches and reads goldens.
type wireGolden struct {
	subs map[string]string
}

func newWireGolden(ns string) *wireGolden {
	return &wireGolden{subs: map[string]string{
		ns: wireNamespace,
		// The harness managers are test names; the goldens carry the real
		// manager names so the osac-aap side can run them unchanged.
		testFabricRef.Name:        "netris",
		testFabricRef.AnsibleRole: "osac.templates.netris",
	}}
}

// register replaces obj's name by name and its UID by a fixed canonical UUID.
func (g *wireGolden) register(obj client.Object, name string, uidSuffix int) {
	g.subs[obj.GetName()] = name
	g.subs[string(obj.GetUID())] = "00000000-0000-4000-8000-" + strings.Repeat("0", 11) + string(rune('0'+uidSuffix))
}

func (g *wireGolden) normalise(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, item := range v {
			out[k] = g.normalise(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = g.normalise(item)
		}
		return out
	case string:
		if replaced, ok := g.subs[v]; ok {
			return replaced
		}
	}
	return value
}

func wireJSON(value any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	Expect(enc.Encode(value)).To(Succeed())
	return buf.Bytes()
}

// expectLaunch compares the latest launch of task on manager with its input golden.
func (g *wireGolden) expectLaunch(h *netHarness, task, manager string) {
	GinkgoHelper()
	launch := h.aap.latest(task, manager)
	got := wireJSON(g.normalise(launch.vars))
	file := filepath.Join(wireGoldenDir, "inputs", task+"."+manager+".json")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		Expect(os.WriteFile(file, got, 0o644)).To(Succeed())
		return
	}
	want, err := os.ReadFile(file)
	Expect(err).NotTo(HaveOccurred())
	Expect(string(got)).To(Equal(string(want)), "launch of %s on %s differs from %s", task, manager, file)
}

// result returns the artifacts golden of task on manager.
func wireResult(task, manager string) string {
	GinkgoHelper()
	data, err := os.ReadFile(filepath.Join(wireGoldenDir, "results", task+"."+manager+".json"))
	Expect(err).NotTo(HaveOccurred())
	return string(data)
}

var _ = Describe("Operator and AAP wire contract goldens", func() {
	It("launches the golden inputs and accepts the golden results of every networking task family", func() {
		h := newNetHarness(true)
		g := newWireGolden(h.ns)
		h.networkClass(true, false)
		ns := h.ns
		vns := &VirtualNetworkReconciler{Client: k8sClient, Engine: h.engine, NetworkingNamespace: ns}
		subnets := &SubnetReconciler{Client: k8sClient, Engine: h.engine, NetworkingNamespace: ns}
		pools := &ExternalIPPoolReconciler{Client: k8sClient, Engine: h.engine, NetworkingNamespace: ns}
		eips := &ExternalIPReconciler{Client: k8sClient, Engine: h.engine, NetworkingNamespace: ns}
		eipas := &ExternalIPAttachmentReconciler{Client: k8sClient, Engine: h.engine, NetworkingNamespace: ns}
		attachments := &NetworkAttachmentReconciler{Client: k8sClient, Engine: h.engine, NetworkingNamespace: ns}
		natgws := &NATGatewayReconciler{Client: k8sClient, Engine: h.engine, NetworkingNamespace: ns}

		// VirtualNetwork: the Fabric create returns backend_id and l3_vni, then
		// the Kubernetes create runs with the same input.
		vn := h.virtualNetwork(wireVN)
		g.register(vn, "vn-3f1c9a2e", 1)
		reconcileNet(vns, vn)
		g.expectLaunch(h, networktask.CreateVirtualNetwork, netFabric)
		h.aap.finish(networktask.CreateVirtualNetwork, netFabric, "successful", wireResult(networktask.CreateVirtualNetwork, netFabric))
		reconcileNet(vns, vn)
		Expect(vn.Status.BackendNetworkID).To(Equal("101"))
		Expect(vn.Status.L3VNI).To(Equal(int32(10001)))
		g.expectLaunch(h, networktask.CreateVirtualNetwork, netK8s)
		h.aap.succeed(networktask.CreateVirtualNetwork, netK8s)
		reconcileNet(vns, vn)
		expectReady(vn, metav1.ConditionTrue, v1alpha1.ReasonSucceeded)

		// Subnet: the Fabric create returns the network segment (l2_vni); the
		// Kubernetes create receives it as the network_segment launch key.
		subnet := h.subnet(wireSubnet, wireVN, "10.0.1.0/24", testPlan)
		g.register(subnet, "subnet-8a2b4c6d", 2)
		reconcileNet(subnets, subnet)
		g.expectLaunch(h, networktask.CreateSubnet, netFabric)
		h.aap.finish(networktask.CreateSubnet, netFabric, "successful", wireResult(networktask.CreateSubnet, netFabric))
		reconcileNet(subnets, subnet)
		Expect(subnet.Status.NetworkSegment).To(Equal(&v1alpha1.NetworkSegment{Type: v1alpha1.NetworkTypeEVPN, L2VNI: 10101}))
		g.expectLaunch(h, networktask.CreateSubnet, netK8s)
		h.aap.succeed(networktask.CreateSubnet, netK8s)
		reconcileNet(subnets, subnet)
		expectReady(subnet, metav1.ConditionTrue, v1alpha1.ReasonSucceeded)

		// ExternalIPPool and ExternalIP: the Fabric create returns the address.
		pool := &v1alpha1.ExternalIPPool{ObjectMeta: h.meta(kindExternalIPPool, wirePool),
			Spec: v1alpha1.ExternalIPPoolSpec{CIDRs: []string{"203.0.113.0/30"}, IPFamily: "IPv4"}}
		Expect(k8sClient.Create(ctx, pool)).To(Succeed())
		g.register(pool, "externalippool-5d6a0f1e", 3)
		reconcileNet(pools, pool)
		g.expectLaunch(h, networktask.CreateExternalIPPool, netFabric)
		h.aap.succeed(networktask.CreateExternalIPPool, netFabric)
		reconcileNet(pools, pool)
		expectReady(pool, metav1.ConditionTrue, v1alpha1.ReasonSucceeded)

		eip := &v1alpha1.ExternalIP{ObjectMeta: h.meta(kindExternalIP, wireEIP), Spec: v1alpha1.ExternalIPSpec{Pool: wirePool}}
		Expect(k8sClient.Create(ctx, eip)).To(Succeed())
		g.register(eip, "externalip-7b6a5c4d", 4)
		reconcileNet(eips, eip)
		g.expectLaunch(h, networktask.CreateExternalIP, netFabric)
		h.aap.finish(networktask.CreateExternalIP, netFabric, "successful", wireResult(networktask.CreateExternalIP, netFabric))
		reconcileNet(eips, eip)
		Expect(eip.Status.Address).To(Equal("203.0.113.1"))
		expectReady(eip, metav1.ConditionTrue, v1alpha1.ReasonSucceeded)

		// ExternalIPAttachment to a VM address on the Subnet.
		h.computeInstance(wireVM, wireSubnet, "10.0.1.17")
		eipa := &v1alpha1.ExternalIPAttachment{ObjectMeta: h.meta(kindExternalIPAttachment, wireEIPA),
			Spec: v1alpha1.ExternalIPAttachmentSpec{ExternalIP: wireEIP, ComputeInstance: ptr.To(wireVM)}}
		Expect(k8sClient.Create(ctx, eipa)).To(Succeed())
		g.register(eipa, "externalipattachment-9c8b7a6d", 5)
		reconcileNet(eipas, eipa)
		g.expectLaunch(h, networktask.AttachExternalIP, netFabric)
		h.aap.succeed(networktask.AttachExternalIP, netFabric)
		reconcileNet(eipas, eipa)
		expectReady(eipa, metav1.ConditionTrue, v1alpha1.ReasonSucceeded)

		// NetworkAttachment of the VM: Kubernetes apply returns the VM
		// namespace, then the Fabric apply carries the VM address.
		na := &v1alpha1.NetworkAttachment{
			ObjectMeta: metav1.ObjectMeta{Name: "computeinstance-" + wireVM, Namespace: ns,
				Annotations: map[string]string{networktask.TenantAnnotation: "tenant-a"}},
			Spec: v1alpha1.NetworkAttachmentSpec{
				Target:         v1alpha1.NetworkAttachmentTarget{Kind: v1alpha1.NetworkAttachmentTargetComputeInstance, ID: wireVM},
				Subnet:         wireSubnet,
				Interface:      v1alpha1.NetworkAttachmentInterface{Type: v1alpha1.NetworkInterfaceOverlay},
				SecurityGroups: []string{wireSG},
			},
		}
		Expect(k8sClient.Create(ctx, na)).To(Succeed())
		g.register(na, "computeinstance-"+wireVM, 6)
		vmNamespace := "osac-vn-" + wireVN
		err := k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: vmNamespace,
			Labels: map[string]string{networktask.TenantAnnotation: "tenant-a"}}})
		if err != nil {
			Expect(client.IgnoreAlreadyExists(err)).To(Succeed())
		}
		reconcileNet(attachments, na)
		g.expectLaunch(h, networktask.ApplyNetworkAttachment, netK8s)
		h.aap.finish(networktask.ApplyNetworkAttachment, netK8s, "successful", wireResult(networktask.ApplyNetworkAttachment, netK8s))
		reconcileNet(attachments, na)
		Expect(na.Status.VMAttachment).To(Equal(&v1alpha1.NetworkAttachmentVMAttachment{Namespace: vmNamespace}))
		g.expectLaunch(h, networktask.ApplyNetworkAttachment, netFabric)
		h.aap.succeed(networktask.ApplyNetworkAttachment, netFabric)
		reconcileNet(attachments, na)
		expectReady(na, metav1.ConditionTrue, v1alpha1.ReasonSucceeded)

		// NATGateway: the input carries the nat_gateway_context snapshot.
		natgw := &v1alpha1.NATGateway{ObjectMeta: h.meta(kindNATGateway, wireNATGW),
			Spec: v1alpha1.NATGatewaySpec{VirtualNetwork: wireVN, ExternalIP: wireEIP}}
		Expect(k8sClient.Create(ctx, natgw)).To(Succeed())
		g.register(natgw, "natgateway-1a2b3c4d", 7)
		reconcileNet(natgws, natgw)
		g.expectLaunch(h, networktask.CreateNATGateway, netFabric)
		h.aap.succeed(networktask.CreateNATGateway, netFabric)
		reconcileNet(natgws, natgw)
		expectReady(natgw, metav1.ConditionTrue, v1alpha1.ReasonSucceeded)

		// The stored task input is the launch input without the launch keys.
		for _, stored := range []struct {
			obj  v1alpha1.NetworkInputObject
			task string
			drop []string
		}{
			{vn, networktask.CreateVirtualNetwork, []string{"network_type"}},
			{subnet, networktask.CreateSubnet, []string{"network_type", "network_segment"}},
		} {
			input := taskInput(stored.obj)
			launch := h.aap.latest(stored.task, netFabric).vars
			for key, value := range input {
				Expect(launch).To(HaveKeyWithValue(key, value))
			}
			for key := range launch {
				if _, ok := input[key]; !ok {
					Expect(append([]string{"task", "manager"}, stored.drop...)).To(ContainElement(key))
				}
			}
		}
	})
})
