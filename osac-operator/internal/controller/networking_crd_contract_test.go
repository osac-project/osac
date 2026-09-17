/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package controller

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

func networkingContractName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func expectCRDCreateRejected(object client.Object) {
	Expect(k8sClient.Create(ctx, object)).To(HaveOccurred())
}

var _ = Describe("IPv4-only networking CRD contracts", func() {
	It("accepts canonical IPv4 VirtualNetworks and rejects missing, host-bit, IPv6, and dual-stack CIDRs", func() {
		valid := &v1alpha1.VirtualNetwork{
			ObjectMeta: metav1.ObjectMeta{Name: networkingContractName("valid-vn"), Namespace: "default"},
			Spec:       v1alpha1.VirtualNetworkSpec{Region: "us-east-1", IPv4CIDR: "10.251.0.0/16"},
		}
		Expect(k8sClient.Create(ctx, valid)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, valid) })

		cases := []struct {
			name string
			spec v1alpha1.VirtualNetworkSpec
		}{
			{name: "missing IPv4", spec: v1alpha1.VirtualNetworkSpec{Region: "us-east-1"}},
			{name: "empty IPv4", spec: v1alpha1.VirtualNetworkSpec{Region: "us-east-1", IPv4CIDR: ""}},
			{name: "host-bit IPv4", spec: v1alpha1.VirtualNetworkSpec{Region: "us-east-1", IPv4CIDR: "10.251.0.1/16"}},
			{name: "IPv6-only", spec: v1alpha1.VirtualNetworkSpec{Region: "us-east-1", IPv6CIDR: "2001:db8::/32"}},
			{name: "dual-stack", spec: v1alpha1.VirtualNetworkSpec{Region: "us-east-1", IPv4CIDR: "10.251.1.0/24", IPv6CIDR: "2001:db8::/32"}},
		}
		for _, testCase := range cases {
			By("rejecting " + testCase.name)
			expectCRDCreateRejected(&v1alpha1.VirtualNetwork{
				ObjectMeta: metav1.ObjectMeta{Name: networkingContractName("invalid-vn"), Namespace: "default"},
				Spec:       testCase.spec,
			})
		}
	})

	It("rejects VirtualNetwork updates that introduce a non-canonical IPv4 or any IPv6 CIDR", func() {
		object := &v1alpha1.VirtualNetwork{
			ObjectMeta: metav1.ObjectMeta{Name: networkingContractName("update-vn"), Namespace: "default"},
			Spec:       v1alpha1.VirtualNetworkSpec{Region: "us-east-1", IPv4CIDR: "10.252.0.0/16"},
		}
		Expect(k8sClient.Create(ctx, object)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, object) })

		object.Spec.IPv4CIDR = "10.252.0.1/16"
		Expect(k8sClient.Update(ctx, object)).To(HaveOccurred())
		object.Spec.IPv4CIDR = "10.252.0.0/16"
		object.Spec.IPv6CIDR = "2001:db8::/32"
		Expect(k8sClient.Update(ctx, object)).To(HaveOccurred())
	})

	It("accepts canonical IPv4 Subnets and rejects missing, host-bit, IPv6, and dual-stack CIDRs", func() {
		valid := &v1alpha1.Subnet{
			ObjectMeta: metav1.ObjectMeta{Name: networkingContractName("valid-subnet"), Namespace: "default"},
			Spec:       v1alpha1.SubnetSpec{VirtualNetwork: "parent-vn", IPv4CIDR: "10.253.1.0/24"},
		}
		Expect(k8sClient.Create(ctx, valid)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, valid) })

		cases := []struct {
			name string
			spec v1alpha1.SubnetSpec
		}{
			{name: "missing IPv4", spec: v1alpha1.SubnetSpec{VirtualNetwork: "parent-vn"}},
			{name: "empty IPv4", spec: v1alpha1.SubnetSpec{VirtualNetwork: "parent-vn", IPv4CIDR: ""}},
			{name: "host-bit IPv4", spec: v1alpha1.SubnetSpec{VirtualNetwork: "parent-vn", IPv4CIDR: "10.253.1.1/24"}},
			{name: "IPv6-only", spec: v1alpha1.SubnetSpec{VirtualNetwork: "parent-vn", IPv6CIDR: "2001:db8::/64"}},
			{name: "dual-stack", spec: v1alpha1.SubnetSpec{VirtualNetwork: "parent-vn", IPv4CIDR: "10.253.2.0/24", IPv6CIDR: "2001:db8::/64"}},
		}
		for _, testCase := range cases {
			By("rejecting " + testCase.name)
			expectCRDCreateRejected(&v1alpha1.Subnet{
				ObjectMeta: metav1.ObjectMeta{Name: networkingContractName("invalid-subnet"), Namespace: "default"},
				Spec:       testCase.spec,
			})
		}
	})

	It("rejects Subnet updates that introduce non-canonical IPv4 or IPv6", func() {
		object := &v1alpha1.Subnet{
			ObjectMeta: metav1.ObjectMeta{Name: networkingContractName("update-subnet"), Namespace: "default"},
			Spec:       v1alpha1.SubnetSpec{VirtualNetwork: "parent-vn", IPv4CIDR: "10.254.1.0/24"},
		}
		Expect(k8sClient.Create(ctx, object)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, object) })

		object.Spec.IPv4CIDR = "10.254.1.1/24"
		Expect(k8sClient.Update(ctx, object)).To(HaveOccurred())
		object.Spec.IPv4CIDR = "10.254.1.0/24"
		object.Spec.IPv6CIDR = "2001:db8::/64"
		Expect(k8sClient.Update(ctx, object)).To(HaveOccurred())
	})

	It("accepts canonical IPv4 SecurityGroup rules and rejects non-canonical and IPv6 rule CIDRs", func() {
		valid := &v1alpha1.SecurityGroup{
			ObjectMeta: metav1.ObjectMeta{Name: networkingContractName("valid-sg"), Namespace: "default"},
			Spec: v1alpha1.SecurityGroupSpec{
				VirtualNetwork: "parent-vn",
				IngressRules: []v1alpha1.SecurityRule{{
					Protocol:   v1alpha1.SecurityGroupProtocolAll,
					SourceCIDR: "10.255.0.0/16",
				}},
				EgressRules: []v1alpha1.SecurityRule{{
					Protocol:        v1alpha1.SecurityGroupProtocolAll,
					DestinationCIDR: "10.255.0.0/16",
				}},
			},
		}
		Expect(k8sClient.Create(ctx, valid)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, valid) })

		cases := []struct {
			name string
			rule v1alpha1.SecurityRule
		}{
			{name: "missing ingress CIDR", rule: v1alpha1.SecurityRule{Protocol: v1alpha1.SecurityGroupProtocolAll}},
			{name: "host-bit source", rule: v1alpha1.SecurityRule{Protocol: v1alpha1.SecurityGroupProtocolAll, SourceCIDR: "10.255.0.1/16"}},
			{name: "ingress destination-only CIDR", rule: v1alpha1.SecurityRule{Protocol: v1alpha1.SecurityGroupProtocolAll, DestinationCIDR: "10.255.0.0/16"}},
			{name: "IPv6 source", rule: v1alpha1.SecurityRule{Protocol: v1alpha1.SecurityGroupProtocolAll, SourceCIDR: "2001:db8::/64"}},
			{name: "host-bit destination", rule: v1alpha1.SecurityRule{Protocol: v1alpha1.SecurityGroupProtocolAll, DestinationCIDR: "10.255.1.1/24"}},
			{name: "IPv6 destination", rule: v1alpha1.SecurityRule{Protocol: v1alpha1.SecurityGroupProtocolAll, DestinationCIDR: "2001:db8::/64"}},
		}
		for _, testCase := range cases {
			By("rejecting " + testCase.name)
			expectCRDCreateRejected(&v1alpha1.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{Name: networkingContractName("invalid-sg"), Namespace: "default"},
				Spec:       v1alpha1.SecurityGroupSpec{VirtualNetwork: "parent-vn", IngressRules: []v1alpha1.SecurityRule{testCase.rule}},
			})
		}

		egressCases := []struct {
			name string
			rule v1alpha1.SecurityRule
		}{
			{name: "missing egress CIDR", rule: v1alpha1.SecurityRule{Protocol: v1alpha1.SecurityGroupProtocolAll}},
			{name: "egress source-only CIDR", rule: v1alpha1.SecurityRule{Protocol: v1alpha1.SecurityGroupProtocolAll, SourceCIDR: "10.255.0.0/16"}},
		}
		for _, testCase := range egressCases {
			By("rejecting " + testCase.name)
			expectCRDCreateRejected(&v1alpha1.SecurityGroup{
				ObjectMeta: metav1.ObjectMeta{Name: networkingContractName("invalid-egress-sg"), Namespace: "default"},
				Spec:       v1alpha1.SecurityGroupSpec{VirtualNetwork: "parent-vn", EgressRules: []v1alpha1.SecurityRule{testCase.rule}},
			})
		}
	})

	It("accepts one canonical IPv4 ExternalIPPool CIDR and rejects every other family or CIDR shape", func() {
		valid := &v1alpha1.ExternalIPPool{
			ObjectMeta: metav1.ObjectMeta{Name: networkingContractName("valid-pool"), Namespace: "default"},
			Spec:       v1alpha1.ExternalIPPoolSpec{CIDRs: []string{"10.0.0.0/28"}, IPFamily: "IPv4"},
		}
		Expect(k8sClient.Create(ctx, valid)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, valid) })

		cases := []struct {
			name string
			spec v1alpha1.ExternalIPPoolSpec
		}{
			{name: "missing CIDR", spec: v1alpha1.ExternalIPPoolSpec{IPFamily: "IPv4"}},
			{name: "multiple CIDRs", spec: v1alpha1.ExternalIPPoolSpec{CIDRs: []string{"10.0.1.0/28", "10.0.2.0/28"}, IPFamily: "IPv4"}},
			{name: "host-bit CIDR", spec: v1alpha1.ExternalIPPoolSpec{CIDRs: []string{"10.0.3.1/28"}, IPFamily: "IPv4"}},
			{name: "IPv6 CIDR", spec: v1alpha1.ExternalIPPoolSpec{CIDRs: []string{"2001:db8::/64"}, IPFamily: "IPv4"}},
			{name: "IPv6 family", spec: v1alpha1.ExternalIPPoolSpec{CIDRs: []string{"2001:db8::/64"}, IPFamily: "IPv6"}},
			{name: "unspecified family", spec: v1alpha1.ExternalIPPoolSpec{CIDRs: []string{"10.0.4.0/28"}}},
		}
		for _, testCase := range cases {
			By("rejecting " + testCase.name)
			expectCRDCreateRejected(&v1alpha1.ExternalIPPool{
				ObjectMeta: metav1.ObjectMeta{Name: networkingContractName("invalid-pool"), Namespace: "default"},
				Spec:       testCase.spec,
			})
		}
	})

	It("rejects ExternalIPPool updates that change family, cardinality, or canonical CIDR", func() {
		object := &v1alpha1.ExternalIPPool{
			ObjectMeta: metav1.ObjectMeta{Name: networkingContractName("update-pool"), Namespace: "default"},
			Spec:       v1alpha1.ExternalIPPoolSpec{CIDRs: []string{"10.1.0.0/28"}, IPFamily: "IPv4"},
		}
		Expect(k8sClient.Create(ctx, object)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, object) })

		object.Spec.CIDRs = []string{"10.1.0.1/28"}
		Expect(k8sClient.Update(ctx, object)).To(HaveOccurred())
		object.Spec.CIDRs = []string{"10.1.0.0/28", "10.1.1.0/28"}
		Expect(k8sClient.Update(ctx, object)).To(HaveOccurred())
		object.Spec.CIDRs = []string{"10.1.0.0/28"}
		object.Spec.IPFamily = "IPv6"
		Expect(k8sClient.Update(ctx, object)).To(HaveOccurred())
	})
})
