/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package install

import (
	"context"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
)

// These cases mirror the metal3-provisioning-watch-all-namespaces matrix
// entry, since that's the only current consumer of "resource-field-equals",
// but exercise the engine directly so its generality doesn't erode
// unnoticed.
var _ = Describe("buildResourceFieldEqualsCheck", func() {
	entry := matrixEntry{
		ID:              "provisioning-watch-all-namespaces",
		Kind:            "resource-field-equals",
		Severity:        "required",
		Category:        "resource",
		Group:           "metal3.io",
		Version:         "v1alpha1",
		Resource:        "provisionings",
		Field:           []string{"spec", "watchAllNamespaces"},
		Expected:        true,
		NotFoundMessage: "no Provisioning CR found; Metal3 backend requires a Provisioning CR with watchAllNamespaces: true",
		MismatchMessage: "Provisioning CR does not have spec.watchAllNamespaces: true",
	}

	It("rejects an entry missing group/version/resource/field", func() {
		_, err := buildResourceFieldEqualsCheck(matrixEntry{ID: "bad", Severity: "required"})
		Expect(err).To(HaveOccurred())
	})

	It("is a Required check", func() {
		check, err := buildResourceFieldEqualsCheck(entry)
		Expect(err).NotTo(HaveOccurred())
		Expect(check.Severity).To(Equal(Required))
	})

	It("passes when the field matches the expected value", func() {
		check, _ := buildResourceFieldEqualsCheck(entry)
		clients := newFakeClients(nil, []runtime.Object{newUnstructuredProvisioning("provisioning-configuration", true)})

		status, message := check.Run(context.Background(), clients)

		Expect(status).To(Equal(Pass))
		Expect(message).To(ContainSubstring("watchAllNamespaces"))
	})

	It("fails with notFoundMessage when no resource exists", func() {
		check, _ := buildResourceFieldEqualsCheck(entry)
		clients := newFakeClients(nil, nil)

		status, message := check.Run(context.Background(), clients)

		Expect(status).To(Equal(Failed))
		Expect(message).To(ContainSubstring("no Provisioning CR found"))
	})

	It("fails with mismatchMessage when the field is set to a different value", func() {
		check, _ := buildResourceFieldEqualsCheck(entry)
		clients := newFakeClients(nil, []runtime.Object{newUnstructuredProvisioning("provisioning-configuration", false)})

		status, message := check.Run(context.Background(), clients)

		Expect(status).To(Equal(Failed))
		Expect(message).To(ContainSubstring("watchAllNamespaces"))
	})

	It("fails when the field is unset", func() {
		check, _ := buildResourceFieldEqualsCheck(entry)
		clients := newFakeClients(nil, []runtime.Object{
			&unstructured.Unstructured{
				Object: map[string]any{
					"apiVersion": "metal3.io/v1alpha1",
					"kind":       "Provisioning",
					"metadata":   map[string]any{"name": "provisioning-configuration"},
				},
			},
		})

		status, message := check.Run(context.Background(), clients)

		Expect(status).To(Equal(Failed))
		Expect(message).To(ContainSubstring("watchAllNamespaces"))
	})
})
