/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package baremetalinstance

import (
	"bytes"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
)

func formatBareMetalInstance(bmi *publicv1.BareMetalInstance) string {
	var buf bytes.Buffer
	renderBareMetalInstance(&buf, bmi)
	return buf.String()
}

var _ = Describe("Describe Bare Metal Instance", func() {
	Describe("Network Interfaces section", func() {
		It("should show MACs when hardware is present", func() {
			bmi := &publicv1.BareMetalInstance{
				Id: "bmi-001",
				Status: publicv1.BareMetalInstanceStatus_builder{
					Hardware: publicv1.BareMetalHardware_builder{
						Nics: []*publicv1.BareMetalNICStatus{
							publicv1.BareMetalNICStatus_builder{Mac: "aa:bb:cc:dd:ee:01"}.Build(),
							publicv1.BareMetalNICStatus_builder{Mac: "ff:00:11:22:33:44"}.Build(),
						},
					}.Build(),
				}.Build(),
			}
			output := formatBareMetalInstance(bmi)
			Expect(output).To(ContainSubstring("Network Interfaces:"))
			Expect(output).To(ContainSubstring("aa:bb:cc:dd:ee:01"))
			Expect(output).To(ContainSubstring("ff:00:11:22:33:44"))
			Expect(output).NotTo(ContainSubstring("N/A"))
		})

		It("should show N/A when Status.Hardware is nil", func() {
			bmi := &publicv1.BareMetalInstance{
				Id:     "bmi-002",
				Status: publicv1.BareMetalInstanceStatus_builder{}.Build(),
			}
			output := formatBareMetalInstance(bmi)
			Expect(output).To(ContainSubstring("Network Interfaces:"))
			Expect(output).To(ContainSubstring("N/A"))
		})

		It("should show N/A when Status.Hardware.Nics is empty", func() {
			bmi := &publicv1.BareMetalInstance{
				Id: "bmi-003",
				Status: publicv1.BareMetalInstanceStatus_builder{
					Hardware: publicv1.BareMetalHardware_builder{
						Nics: []*publicv1.BareMetalNICStatus{},
					}.Build(),
				}.Build(),
			}
			output := formatBareMetalInstance(bmi)
			Expect(output).To(ContainSubstring("Network Interfaces:"))
			Expect(output).To(ContainSubstring("N/A"))
		})

		It("should show N/A when Status is nil", func() {
			bmi := &publicv1.BareMetalInstance{
				Id:     "bmi-004",
				Status: nil,
			}
			output := formatBareMetalInstance(bmi)
			Expect(output).To(ContainSubstring("Network Interfaces:"))
			Expect(output).To(ContainSubstring("N/A"))
		})
	})
})
