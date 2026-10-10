/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package validation

import (
	"buf.build/go/protovalidate"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("BareMetalHardwareSpec contract validation", func() {
	var validator protovalidate.Validator

	BeforeEach(func() {
		var err error
		validator, err = protovalidate.New()
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("Fabric port requirement", func() {
		DescribeTable("accepts hardware spec with at least one fabric port",
			func(spec *privatev1.BareMetalHardwareSpec) {
				Expect(validator.Validate(spec)).To(Succeed())
			},
			Entry("single fabric port", privatev1.BareMetalHardwareSpec_builder{
				Cpu: privatev1.BareMetalCPUSpec_builder{
					Cores:          32,
					Architecture:   "x86_64",
					ThreadsPerCore: 2,
				}.Build(),
				Memory: privatev1.BareMetalMemorySpec_builder{
					TotalGb: 128,
				}.Build(),
				NetworkPorts: []*privatev1.BareMetalNetworkPortSpec{
					privatev1.BareMetalNetworkPortSpec_builder{
						Name:  "data-0",
						Role:  "fabric",
						Type:  "Ethernet",
						Speed: "25Gbps",
					}.Build(),
				},
			}.Build()),
			Entry("fabric port among other roles", privatev1.BareMetalHardwareSpec_builder{
				Cpu: privatev1.BareMetalCPUSpec_builder{
					Cores:          16,
					Architecture:   "x86_64",
					ThreadsPerCore: 2,
				}.Build(),
				Memory: privatev1.BareMetalMemorySpec_builder{
					TotalGb: 64,
				}.Build(),
				NetworkPorts: []*privatev1.BareMetalNetworkPortSpec{
					privatev1.BareMetalNetworkPortSpec_builder{
						Name:  "mgmt-0",
						Role:  "management",
						Type:  "Ethernet",
						Speed: "1Gbps",
					}.Build(),
					privatev1.BareMetalNetworkPortSpec_builder{
						Name:  "data-0",
						Role:  "fabric",
						Type:  "Ethernet",
						Speed: "25Gbps",
					}.Build(),
					privatev1.BareMetalNetworkPortSpec_builder{
						Name:  "stor-0",
						Role:  "storage",
						Type:  "Ethernet",
						Speed: "100Gbps",
					}.Build(),
				},
			}.Build()),
			Entry("multiple fabric ports", privatev1.BareMetalHardwareSpec_builder{
				Cpu: privatev1.BareMetalCPUSpec_builder{
					Cores:          64,
					Architecture:   "aarch64",
					ThreadsPerCore: 1,
				}.Build(),
				Memory: privatev1.BareMetalMemorySpec_builder{
					TotalGb: 256,
				}.Build(),
				NetworkPorts: []*privatev1.BareMetalNetworkPortSpec{
					privatev1.BareMetalNetworkPortSpec_builder{
						Name:  "data-0",
						Role:  "fabric",
						Type:  "Ethernet",
						Speed: "25Gbps",
					}.Build(),
					privatev1.BareMetalNetworkPortSpec_builder{
						Name:  "data-1",
						Role:  "fabric",
						Type:  "Ethernet",
						Speed: "25Gbps",
					}.Build(),
				},
			}.Build()),
		)

		DescribeTable("rejects hardware spec without a fabric port",
			func(spec *privatev1.BareMetalHardwareSpec) {
				err := validator.Validate(spec)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("at least one network port with role 'fabric' is required"))
			},
			Entry("no network ports", privatev1.BareMetalHardwareSpec_builder{
				Cpu: privatev1.BareMetalCPUSpec_builder{
					Cores:          32,
					Architecture:   "x86_64",
					ThreadsPerCore: 2,
				}.Build(),
				Memory: privatev1.BareMetalMemorySpec_builder{
					TotalGb: 128,
				}.Build(),
			}.Build()),
			Entry("only management ports", privatev1.BareMetalHardwareSpec_builder{
				Cpu: privatev1.BareMetalCPUSpec_builder{
					Cores:          16,
					Architecture:   "x86_64",
					ThreadsPerCore: 2,
				}.Build(),
				Memory: privatev1.BareMetalMemorySpec_builder{
					TotalGb: 64,
				}.Build(),
				NetworkPorts: []*privatev1.BareMetalNetworkPortSpec{
					privatev1.BareMetalNetworkPortSpec_builder{
						Name:  "mgmt-0",
						Role:  "management",
						Type:  "Ethernet",
						Speed: "1Gbps",
					}.Build(),
				},
			}.Build()),
			Entry("storage and lifecycle ports but no fabric", privatev1.BareMetalHardwareSpec_builder{
				Cpu: privatev1.BareMetalCPUSpec_builder{
					Cores:          8,
					Architecture:   "x86_64",
					ThreadsPerCore: 2,
				}.Build(),
				Memory: privatev1.BareMetalMemorySpec_builder{
					TotalGb: 32,
				}.Build(),
				NetworkPorts: []*privatev1.BareMetalNetworkPortSpec{
					privatev1.BareMetalNetworkPortSpec_builder{
						Name:  "stor-0",
						Role:  "storage",
						Type:  "Ethernet",
						Speed: "100Gbps",
					}.Build(),
					privatev1.BareMetalNetworkPortSpec_builder{
						Name:  "bmc-0",
						Role:  "lifecycle",
						Type:  "Ethernet",
						Speed: "1Gbps",
					}.Build(),
				},
			}.Build()),
		)
	})

	Describe("Unique port names", func() {
		It("rejects duplicate port names", func() {
			spec := privatev1.BareMetalHardwareSpec_builder{
				Cpu: privatev1.BareMetalCPUSpec_builder{
					Cores:          16,
					Architecture:   "x86_64",
					ThreadsPerCore: 2,
				}.Build(),
				Memory: privatev1.BareMetalMemorySpec_builder{
					TotalGb: 64,
				}.Build(),
				NetworkPorts: []*privatev1.BareMetalNetworkPortSpec{
					privatev1.BareMetalNetworkPortSpec_builder{
						Name:  "data-0",
						Role:  "fabric",
						Type:  "Ethernet",
						Speed: "25Gbps",
					}.Build(),
					privatev1.BareMetalNetworkPortSpec_builder{
						Name:  "data-0",
						Role:  "fabric",
						Type:  "Ethernet",
						Speed: "25Gbps",
					}.Build(),
				},
			}.Build()
			err := validator.Validate(spec)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("network port names must be unique"))
		})
	})
})
