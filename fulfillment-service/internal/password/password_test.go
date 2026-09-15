/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package password

import (
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPassword(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Password package")
}

var _ = Describe("Generate", func() {
	It("returns a password of the expected length", func() {
		pw, err := Generate()
		Expect(err).ToNot(HaveOccurred())
		Expect(pw).To(HaveLen(Length))
	})

	It("uses only characters from the allowed charset", func() {
		// Generate several passwords to increase confidence.
		for range 20 {
			pw, err := Generate()
			Expect(err).ToNot(HaveOccurred())
			for _, ch := range pw {
				Expect(strings.ContainsRune(Charset, ch)).To(BeTrue(),
					"password contains disallowed character %q", string(ch))
			}
		}
	})

	It("does not contain URL/form-reserved characters", func() {
		const forbidden = "%#$"
		for range 20 {
			pw, err := Generate()
			Expect(err).ToNot(HaveOccurred())
			Expect(strings.ContainsAny(pw, forbidden)).To(BeFalse(),
				"password %q contains a forbidden character from %q", pw, forbidden)
		}
	})
})
