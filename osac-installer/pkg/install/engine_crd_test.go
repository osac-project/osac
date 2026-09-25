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
	"k8s.io/apimachinery/pkg/runtime"
)

var _ = Describe("buildCRDExistsCheck", func() {
	entry := matrixEntry{
		ID:          "widgets-crd",
		Kind:        "crd-exists",
		Severity:    "required",
		Category:    "resource",
		CRD:         "widgets.example.com",
		Remediation: "install the Widget Operator",
	}

	It("rejects an entry with no crd", func() {
		_, err := buildCRDExistsCheck(matrixEntry{ID: "bad", Severity: "required"})
		Expect(err).To(HaveOccurred())
	})

	It("passes when the CRD is registered", func() {
		check, err := buildCRDExistsCheck(entry)
		Expect(err).NotTo(HaveOccurred())
		Expect(check.Severity).To(Equal(Required))

		clients := newFakeClients(nil, []runtime.Object{newUnstructuredCRD("widgets.example.com")})
		status, message := check.Run(context.Background(), clients)

		Expect(status).To(Equal(Pass))
		Expect(message).To(ContainSubstring("widgets.example.com"))
	})

	It("fails with the remediation text when the CRD is missing", func() {
		check, err := buildCRDExistsCheck(entry)
		Expect(err).NotTo(HaveOccurred())

		clients := newFakeClients(nil, nil)
		status, message := check.Run(context.Background(), clients)

		Expect(status).To(Equal(Failed))
		Expect(message).To(ContainSubstring("widgets.example.com"))
		Expect(message).To(ContainSubstring("install the Widget Operator"))
	})
})
