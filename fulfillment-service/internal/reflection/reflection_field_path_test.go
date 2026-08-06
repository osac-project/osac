/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package reflection

import (
	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	publicv1 "github.com/osac-project/osac/fulfillment-service/internal/api/osac/public/v1"
)

var _ = Describe("ResolveFieldPath", func() {
	It("Resolves a top-level field", func() {
		obj := publicv1.Cluster_builder{Id: "abc"}.Build()
		Expect(ResolveFieldPath(obj, "id")).To(Equal("abc"))
	})

	It("Resolves a nested field path", func() {
		obj := publicv1.ClusterVersion_builder{
			Spec: publicv1.ClusterVersionSpec_builder{
				Version: "4.17.0",
			}.Build(),
		}.Build()
		Expect(ResolveFieldPath(obj, "spec.version")).To(Equal("4.17.0"))
	})

	It("Resolves metadata.name", func() {
		obj := publicv1.ClusterVersion_builder{
			Metadata: publicv1.Metadata_builder{Name: "4-17-0"}.Build(),
		}.Build()
		Expect(ResolveFieldPath(obj, "metadata.name")).To(Equal("4-17-0"))
	})

	It("Returns empty string for a nonexistent field", func() {
		obj := publicv1.Cluster_builder{Id: "abc"}.Build()
		Expect(ResolveFieldPath(obj, "nonexistent")).To(BeEmpty())
	})

	It("Returns empty string for a nonexistent nested field", func() {
		obj := publicv1.Cluster_builder{Id: "abc"}.Build()
		Expect(ResolveFieldPath(obj, "spec.nonexistent")).To(BeEmpty())
	})

	It("Returns empty string when an intermediate segment is not a message", func() {
		obj := publicv1.Cluster_builder{Id: "abc"}.Build()
		Expect(ResolveFieldPath(obj, "id.nested")).To(BeEmpty())
	})
})
