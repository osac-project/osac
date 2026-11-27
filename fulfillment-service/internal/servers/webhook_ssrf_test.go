/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("validateWebhookURL", func() {
	DescribeTable("rejects unsafe URLs",
		func(raw string) {
			Expect(validateWebhookURL(raw)).To(HaveOccurred())
		},
		Entry("empty", ""),
		Entry("http", "http://hooks.example.com/osac"),
		Entry("loopback IP", "https://127.0.0.1/hook"),
		Entry("private IP", "https://10.0.0.5/hook"),
		Entry("metadata IP", "https://169.254.169.254/latest"),
		Entry("cgnat IP", "https://100.64.1.1/hook"),
		Entry("userinfo", "https://user:pass@hooks.example.com/osac"),
	)

	It("accepts a public HTTPS URL with a public IP host", func() {
		Expect(validateWebhookURL("https://8.8.8.8/osac")).To(Succeed())
	})
})
