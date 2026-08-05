/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package instancetype

import (
	"bytes"
	"time"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/timestamppb"

	publicv1 "github.com/osac-project/fulfillment-service/internal/api/osac/public/v1"
)

func formatInstanceType(it *publicv1.InstanceType) string {
	var buf bytes.Buffer
	renderInstanceType(&buf, it)
	return buf.String()
}

var _ = Describe("Rendering tests", func() {
	It("should display all base fields for ACTIVE type", func() {
		it := publicv1.InstanceType_builder{
			Id: "standard-4-16",
			Metadata: publicv1.Metadata_builder{
				Name: "standard-4-16",
			}.Build(),
			Spec: publicv1.InstanceTypeSpec_builder{
				Cores:       4,
				MemoryGib:   16,
				Description: "Balanced compute",
				State:       publicv1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE,
			}.Build(),
		}.Build()
		output := formatInstanceType(it)
		Expect(output).To(ContainSubstring("standard-4-16"))
		Expect(output).To(ContainSubstring("4"))
		Expect(output).To(ContainSubstring("16"))
		Expect(output).To(ContainSubstring("ACTIVE"))
		Expect(output).To(ContainSubstring("Balanced compute"))
		Expect(output).NotTo(ContainSubstring("INSTANCE_TYPE_STATE_"))
		Expect(output).NotTo(ContainSubstring("Replacement:"))
		Expect(output).NotTo(ContainSubstring("Deprecated At:"))
		Expect(output).NotTo(ContainSubstring("Obsolete At:"))
	})

	It("should strip INSTANCE_TYPE_STATE_ prefix from state", func() {
		it := publicv1.InstanceType_builder{
			Id: "deprecated-2-8",
			Metadata: publicv1.Metadata_builder{
				Name: "deprecated-2-8",
			}.Build(),
			Spec: publicv1.InstanceTypeSpec_builder{
				Cores:     2,
				MemoryGib: 8,
				State:     publicv1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED,
			}.Build(),
		}.Build()
		output := formatInstanceType(it)
		Expect(output).To(ContainSubstring("DEPRECATED"))
		Expect(output).NotTo(ContainSubstring("INSTANCE_TYPE_STATE_"))
	})

	It("should show deprecation section when deprecation data exists", func() {
		depTime := time.Date(2026, 6, 10, 17, 21, 0, 0, time.UTC)
		obsTime := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
		it := publicv1.InstanceType_builder{
			Id: "old-4-16",
			Metadata: publicv1.Metadata_builder{
				Name: "old-4-16",
			}.Build(),
			Spec: publicv1.InstanceTypeSpec_builder{
				Cores:     4,
				MemoryGib: 16,
				State:     publicv1.InstanceTypeState_INSTANCE_TYPE_STATE_DEPRECATED,
				Deprecation: publicv1.InstanceTypeDeprecation_builder{
					Replacement:           "standard-8-32",
					DeprecationTimestamp:  timestamppb.New(depTime),
					ObsolescenceTimestamp: timestamppb.New(obsTime),
				}.Build(),
			}.Build(),
		}.Build()
		output := formatInstanceType(it)
		Expect(output).To(MatchRegexp(`Replacement:\s+standard-8-32`))
		Expect(output).To(MatchRegexp(`Deprecated At:\s+2026-06-10T17:21:00Z`))
		Expect(output).To(MatchRegexp(`Obsolete At:\s+2026-12-31T00:00:00Z`))
	})

	It("should omit deprecation section for ACTIVE type without deprecation data", func() {
		it := publicv1.InstanceType_builder{
			Id: "active-8-32",
			Metadata: publicv1.Metadata_builder{
				Name: "active-8-32",
			}.Build(),
			Spec: publicv1.InstanceTypeSpec_builder{
				Cores:     8,
				MemoryGib: 32,
				State:     publicv1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE,
			}.Build(),
		}.Build()
		output := formatInstanceType(it)
		Expect(output).NotTo(ContainSubstring("Replacement:"))
		Expect(output).NotTo(ContainSubstring("Deprecated At:"))
		Expect(output).NotTo(ContainSubstring("Obsolete At:"))
	})

	It("should omit description when empty", func() {
		it := publicv1.InstanceType_builder{
			Id: "no-desc-2-4",
			Metadata: publicv1.Metadata_builder{
				Name: "no-desc-2-4",
			}.Build(),
			Spec: publicv1.InstanceTypeSpec_builder{
				Cores:     2,
				MemoryGib: 4,
				State:     publicv1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE,
			}.Build(),
		}.Build()
		output := formatInstanceType(it)
		Expect(output).NotTo(ContainSubstring("Description:"))
	})

	It("should show description when present", func() {
		it := publicv1.InstanceType_builder{
			Id: "highmem-4-64",
			Metadata: publicv1.Metadata_builder{
				Name: "highmem-4-64",
			}.Build(),
			Spec: publicv1.InstanceTypeSpec_builder{
				Cores:       4,
				MemoryGib:   64,
				Description: "High memory",
				State:       publicv1.InstanceTypeState_INSTANCE_TYPE_STATE_ACTIVE,
			}.Build(),
		}.Build()
		output := formatInstanceType(it)
		Expect(output).To(MatchRegexp(`Description:\s+High memory`))
	})
})
