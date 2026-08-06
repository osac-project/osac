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
		val, ok := ResolveFieldPath[string](obj, "id")
		Expect(ok).To(BeTrue())
		Expect(val).To(Equal("abc"))
	})

	It("Resolves a nested field path", func() {
		obj := publicv1.ClusterVersion_builder{
			Spec: publicv1.ClusterVersionSpec_builder{
				Version: "4.17.0",
			}.Build(),
		}.Build()
		val, ok := ResolveFieldPath[string](obj, "spec.version")
		Expect(ok).To(BeTrue())
		Expect(val).To(Equal("4.17.0"))
	})

	It("Resolves metadata.name", func() {
		obj := publicv1.ClusterVersion_builder{
			Metadata: publicv1.Metadata_builder{Name: "4-17-0"}.Build(),
		}.Build()
		val, ok := ResolveFieldPath[string](obj, "metadata.name")
		Expect(ok).To(BeTrue())
		Expect(val).To(Equal("4-17-0"))
	})

	It("Returns false for a nonexistent field", func() {
		obj := publicv1.Cluster_builder{Id: "abc"}.Build()
		val, ok := ResolveFieldPath[string](obj, "nonexistent")
		Expect(ok).To(BeFalse())
		Expect(val).To(BeEmpty())
	})

	It("Returns false for a nonexistent nested field", func() {
		obj := publicv1.Cluster_builder{Id: "abc"}.Build()
		val, ok := ResolveFieldPath[string](obj, "spec.nonexistent")
		Expect(ok).To(BeFalse())
		Expect(val).To(BeEmpty())
	})

	It("Returns false when an intermediate segment is not a message", func() {
		obj := publicv1.Cluster_builder{Id: "abc"}.Build()
		val, ok := ResolveFieldPath[string](obj, "id.nested")
		Expect(ok).To(BeFalse())
		Expect(val).To(BeEmpty())
	})

	It("Returns false when the type does not match", func() {
		obj := publicv1.Cluster_builder{Id: "abc"}.Build()
		val, ok := ResolveFieldPath[int64](obj, "id")
		Expect(ok).To(BeFalse())
		Expect(val).To(BeZero())
	})

	It("Returns false when an intermediate segment is a repeated field", func() {
		obj := publicv1.ClustersListResponse_builder{
			Items: []*publicv1.Cluster{
				publicv1.Cluster_builder{Id: "c1"}.Build(),
			},
		}.Build()
		val, ok := ResolveFieldPath[string](obj, "items.id")
		Expect(ok).To(BeFalse())
		Expect(val).To(BeEmpty())
	})

	It("Returns false when an intermediate segment is a map field", func() {
		obj := publicv1.Cluster_builder{
			Metadata: publicv1.Metadata_builder{
				Labels: map[string]string{"env": "prod"},
			}.Build(),
		}.Build()
		val, ok := ResolveFieldPath[string](obj, "metadata.labels.env")
		Expect(ok).To(BeFalse())
		Expect(val).To(BeEmpty())
	})
})

var _ = Describe("ResolveFieldPathOr", func() {
	It("Returns the field value when the path exists", func() {
		obj := publicv1.Cluster_builder{Id: "abc"}.Build()
		Expect(ResolveFieldPathOr(obj, "id", "fallback")).To(Equal("abc"))
	})

	It("Returns the fallback when the path does not exist", func() {
		obj := publicv1.Cluster_builder{Id: "abc"}.Build()
		Expect(ResolveFieldPathOr(obj, "nonexistent", "fallback")).To(Equal("fallback"))
	})

	It("Returns the fallback when the type does not match", func() {
		obj := publicv1.Cluster_builder{Id: "abc"}.Build()
		Expect(ResolveFieldPathOr(obj, "id", int64(42))).To(Equal(int64(42)))
	})
})
