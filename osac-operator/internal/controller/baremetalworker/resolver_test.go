/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package baremetalworker

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("nicMACs", func() {
	bmiWithNICs := func(macs ...string) *privatev1.BareMetalInstance {
		nics := make([]*privatev1.BareMetalNICStatus, 0, len(macs))
		for _, m := range macs {
			nics = append(nics, privatev1.BareMetalNICStatus_builder{Mac: m}.Build())
		}
		return privatev1.BareMetalInstance_builder{
			Status: privatev1.BareMetalInstanceStatus_builder{
				Hardware: privatev1.BareMetalHardware_builder{Nics: nics}.Build(),
			}.Build(),
		}.Build()
	}

	It("returns all NIC MACs from status.hardware.nics", func() {
		bmi := bmiWithNICs("aa:bb:cc:dd:ee:00", "aa:bb:cc:dd:ee:01")
		Expect(nicMACs(bmi)).To(Equal([]string{"aa:bb:cc:dd:ee:00", "aa:bb:cc:dd:ee:01"}))
	})

	It("skips empty MAC entries", func() {
		bmi := bmiWithNICs("aa:bb:cc:dd:ee:00", "")
		Expect(nicMACs(bmi)).To(Equal([]string{"aa:bb:cc:dd:ee:00"}))
	})

	It("returns nil when hardware is absent", func() {
		bmi := privatev1.BareMetalInstance_builder{
			Status: privatev1.BareMetalInstanceStatus_builder{}.Build(),
		}.Build()
		Expect(nicMACs(bmi)).To(BeNil())
	})

	It("returns nil for a nil BMI", func() {
		Expect(nicMACs(nil)).To(BeNil())
	})
})
