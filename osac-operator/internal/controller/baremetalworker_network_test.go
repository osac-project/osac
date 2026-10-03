/*
Copyright 2025.

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
	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	"github.com/osac-project/osac/osac-operator/api/v1alpha1"
)

var _ = Describe("buildWorkerNetworkAttachments", func() {
	baseOrder := func() *v1alpha1.ClusterOrder {
		return &v1alpha1.ClusterOrder{
			Spec: v1alpha1.ClusterOrderSpec{
				NetworkAttachment: &v1alpha1.ClusterNetworkAttachment{
					SubnetRef:         "tenant-subnet",
					SecurityGroupRefs: []string{"sg-a", "sg-b"},
				},
				NodeRequests: []v1alpha1.NodeRequest{{
					ResourceClass:   "bm-standard",
					NumberOfNodes:   3,
					FabricInterface: "data-0",
				}},
			},
		}
	}

	It("builds a one-entry attachment from ClusterOrder subnet and stored FabricInterface", func() {
		order := baseOrder()
		attachments, err := buildWorkerNetworkAttachments(order, order.Spec.NodeRequests[0])
		Expect(err).NotTo(HaveOccurred())
		Expect(attachments).To(HaveLen(1))
		Expect(attachments[0].SubnetRef).To(Equal("tenant-subnet"))
		Expect(attachments[0].SecurityGroupRefs).To(Equal([]string{"sg-a", "sg-b"}))
		Expect(attachments[0].Interface).To(Equal("data-0"))
		Expect(attachments[0].Primary).To(BeTrue())
	})

	It("uses the NodeRequest FabricInterface even when a catalog would prefer another NIC", func() {
		// Simulate a later BMIT change that would resolve to data-1: the helper
		// never consults BMIT/HostType, so the stored Create-time value wins.
		order := baseOrder()
		nr := order.Spec.NodeRequests[0]
		nr.FabricInterface = "data-0"
		wouldBeResolvedFromUpdatedBMIT := "data-1"
		attachments, err := buildWorkerNetworkAttachments(order, nr)
		Expect(err).NotTo(HaveOccurred())
		Expect(attachments[0].Interface).To(Equal("data-0"))
		Expect(attachments[0].Interface).NotTo(Equal(wouldBeResolvedFromUpdatedBMIT))
	})

	It("returns an error when FabricInterface is empty", func() {
		order := baseOrder()
		nr := order.Spec.NodeRequests[0]
		nr.FabricInterface = ""
		_, err := buildWorkerNetworkAttachments(order, nr)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("fabricInterface"))
	})

	It("returns an error when networkAttachment is missing", func() {
		order := baseOrder()
		order.Spec.NetworkAttachment = nil
		_, err := buildWorkerNetworkAttachments(order, order.Spec.NodeRequests[0])
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("networkAttachment"))
	})

	It("returns an error when subnetRef is empty", func() {
		order := baseOrder()
		order.Spec.NetworkAttachment.SubnetRef = ""
		_, err := buildWorkerNetworkAttachments(order, order.Spec.NodeRequests[0])
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("subnetRef"))
	})

	It("returns an error when clusterOrder is nil", func() {
		_, err := buildWorkerNetworkAttachments(nil, v1alpha1.NodeRequest{FabricInterface: "data-0"})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("clusterOrder"))
	})

	It("copies security group refs without sharing the backing array", func() {
		order := baseOrder()
		attachments, err := buildWorkerNetworkAttachments(order, order.Spec.NodeRequests[0])
		Expect(err).NotTo(HaveOccurred())
		attachments[0].SecurityGroupRefs[0] = "mutated"
		Expect(order.Spec.NetworkAttachment.SecurityGroupRefs[0]).To(Equal("sg-a"))
	})
})
