/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package render

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"

	"github.com/osac-project/osac/osac-installer/pkg/install"
)

var _ = Describe("JSON", func() {
	It("renders every field, translating enums to their string form", func() {
		results := []install.Result{
			{Check: passingCheck, Status: install.Pass, Message: "CRD found"},
			{Check: warningCheck, Status: install.Failed, Message: "no default StorageClass found"},
		}

		out, err := JSON(results)

		Expect(err).NotTo(HaveOccurred())
		var report JSONReport
		Expect(json.Unmarshal([]byte(out), &report)).To(Succeed())
		Expect(report.Results).To(Equal([]JSONResult{
			{Check: "cert-manager-crds", Severity: "required", Status: "pass", Message: "CRD found"},
			{Check: "default-storageclass", Severity: "warning", Status: "fail", Message: "no default StorageClass found"},
		}))
	})

	It("sets ready to false when a Required check failed", func() {
		out, err := JSON([]install.Result{{Check: requiredCheck, Status: install.Failed}})

		Expect(err).NotTo(HaveOccurred())
		var report JSONReport
		Expect(json.Unmarshal([]byte(out), &report)).To(Succeed())
		Expect(report.Ready).To(BeFalse())
	})

	It("sets ready to true when only a Warning check failed", func() {
		out, err := JSON([]install.Result{{Check: warningCheck, Status: install.Failed}})

		Expect(err).NotTo(HaveOccurred())
		var report JSONReport
		Expect(json.Unmarshal([]byte(out), &report)).To(Succeed())
		Expect(report.Ready).To(BeTrue())
	})

	It("renders an empty results array, not null, for no results", func() {
		out, err := JSON(nil)

		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring(`"results": []`))
	})
})
