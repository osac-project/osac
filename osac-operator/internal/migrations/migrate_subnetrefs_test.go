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

package migrations

import (
	"os"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck
	. "github.com/onsi/gomega"    //nolint:revive,staticcheck

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newUnstructuredCI(name, namespace string, spec map[string]interface{}) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "osac.openshift.io/v1alpha1",
			"kind":       "ComputeInstance",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
			"spec": spec,
		},
	}
}

func ciSpec() map[string]interface{} {
	return map[string]interface{}{
		"templateID":  "test_template",
		"cores":       int64(4),
		"memoryGiB":   int64(8),
		"runStrategy": "Always",
		"image": map[string]interface{}{
			"sourceType": "registry",
			"sourceRef":  "quay.io/fedora/fedora-coreos:stable",
		},
		"bootDisk": map[string]interface{}{
			"sizeGiB": int64(30),
		},
	}
}

var ciGVK = schema.GroupVersionKind{
	Group: "osac.openshift.io", Version: "v1alpha1", Kind: "ComputeInstance",
}

func getSubnetRefFromNA(obj *unstructured.Unstructured, index int) string {
	na, found, _ := unstructured.NestedSlice(obj.Object, "spec", "networkAttachments")
	if !found || index >= len(na) {
		return ""
	}
	entry, ok := na[index].(map[string]interface{})
	if !ok {
		return ""
	}
	ref, _ := entry["subnetRef"].(string)
	return ref
}

var _ = Describe("migrateSubnetRefs", func() {
	var s *runtime.Scheme

	BeforeEach(func() {
		s = runtime.NewScheme()
		s.AddKnownTypeWithName(ciGVK, &unstructured.Unstructured{})
		s.AddKnownTypeWithName(
			schema.GroupVersionKind{Group: "osac.openshift.io", Version: "v1alpha1", Kind: "ComputeInstanceList"},
			&unstructured.UnstructuredList{},
		)
		os.Setenv(envComputeInstanceNamespace, "default")
	})

	AfterEach(func() {
		os.Unsetenv(envComputeInstanceNamespace)
	})

	It("should migrate a CR with subnetRef to networkAttachments", func() {
		spec := ciSpec()
		spec["subnetRef"] = "legacy-subnet"

		fc := fake.NewClientBuilder().
			WithScheme(s).
			WithObjects(newUnstructuredCI("migrate-legacy", "default", spec)).
			Build()

		Expect(migrateSubnetRefs(ctx, fc)).To(Succeed())

		result := &unstructured.Unstructured{}
		result.SetGroupVersionKind(ciGVK)
		Expect(fc.Get(ctx, types.NamespacedName{Name: "migrate-legacy", Namespace: "default"}, result)).To(Succeed())

		na, found, err := unstructured.NestedSlice(result.Object, "spec", "networkAttachments")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(na).To(HaveLen(1))
		Expect(getSubnetRefFromNA(result, 0)).To(Equal("legacy-subnet"))
	})

	It("should not modify a CR that already has networkAttachments", func() {
		spec := ciSpec()
		spec["networkAttachments"] = []interface{}{
			map[string]interface{}{"subnetRef": "existing-subnet"},
		}

		fc := fake.NewClientBuilder().
			WithScheme(s).
			WithObjects(newUnstructuredCI("already-migrated", "default", spec)).
			Build()

		Expect(migrateSubnetRefs(ctx, fc)).To(Succeed())

		result := &unstructured.Unstructured{}
		result.SetGroupVersionKind(ciGVK)
		Expect(fc.Get(ctx, types.NamespacedName{Name: "already-migrated", Namespace: "default"}, result)).To(Succeed())
		Expect(getSubnetRefFromNA(result, 0)).To(Equal("existing-subnet"))
	})

	It("should not modify a CR with neither subnetRef nor networkAttachments", func() {
		fc := fake.NewClientBuilder().
			WithScheme(s).
			WithObjects(newUnstructuredCI("no-subnet", "default", ciSpec())).
			Build()

		Expect(migrateSubnetRefs(ctx, fc)).To(Succeed())

		result := &unstructured.Unstructured{}
		result.SetGroupVersionKind(ciGVK)
		Expect(fc.Get(ctx, types.NamespacedName{Name: "no-subnet", Namespace: "default"}, result)).To(Succeed())

		_, found, _ := unstructured.NestedSlice(result.Object, "spec", "networkAttachments")
		Expect(found).To(BeFalse())
	})

	It("should migrate only legacy CRs in a mixed batch", func() {
		legacySpec := ciSpec()
		legacySpec["subnetRef"] = "legacy-subnet-1"

		modernSpec := ciSpec()
		modernSpec["networkAttachments"] = []interface{}{
			map[string]interface{}{"subnetRef": "modern-subnet"},
		}

		fc := fake.NewClientBuilder().
			WithScheme(s).
			WithObjects(
				newUnstructuredCI("batch-legacy", "default", legacySpec),
				newUnstructuredCI("batch-modern", "default", modernSpec),
				newUnstructuredCI("batch-none", "default", ciSpec()),
			).
			Build()

		Expect(migrateSubnetRefs(ctx, fc)).To(Succeed())

		legacy := &unstructured.Unstructured{}
		legacy.SetGroupVersionKind(ciGVK)
		Expect(fc.Get(ctx, types.NamespacedName{Name: "batch-legacy", Namespace: "default"}, legacy)).To(Succeed())
		Expect(getSubnetRefFromNA(legacy, 0)).To(Equal("legacy-subnet-1"))

		modern := &unstructured.Unstructured{}
		modern.SetGroupVersionKind(ciGVK)
		Expect(fc.Get(ctx, types.NamespacedName{Name: "batch-modern", Namespace: "default"}, modern)).To(Succeed())
		Expect(getSubnetRefFromNA(modern, 0)).To(Equal("modern-subnet"))

		none := &unstructured.Unstructured{}
		none.SetGroupVersionKind(ciGVK)
		Expect(fc.Get(ctx, types.NamespacedName{Name: "batch-none", Namespace: "default"}, none)).To(Succeed())
		_, found, _ := unstructured.NestedSlice(none.Object, "spec", "networkAttachments")
		Expect(found).To(BeFalse())
	})

	It("should be idempotent — second run migrates nothing", func() {
		spec := ciSpec()
		spec["subnetRef"] = "idem-subnet"

		fc := fake.NewClientBuilder().
			WithScheme(s).
			WithObjects(newUnstructuredCI("idem-test", "default", spec)).
			Build()

		Expect(migrateSubnetRefs(ctx, fc)).To(Succeed())
		Expect(migrateSubnetRefs(ctx, fc)).To(Succeed())

		result := &unstructured.Unstructured{}
		result.SetGroupVersionKind(ciGVK)
		Expect(fc.Get(ctx, types.NamespacedName{Name: "idem-test", Namespace: "default"}, result)).To(Succeed())

		na, found, _ := unstructured.NestedSlice(result.Object, "spec", "networkAttachments")
		Expect(found).To(BeTrue())
		Expect(na).To(HaveLen(1))
		Expect(getSubnetRefFromNA(result, 0)).To(Equal("idem-subnet"))
	})
})
