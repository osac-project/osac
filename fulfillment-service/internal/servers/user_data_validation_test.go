/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package servers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("isPureXMLNoDTD", func() {
	// TC-1.1: Valid Unattend.xml accepted
	It("Accepts valid Unattend.xml with nested elements", func() {
		data := []byte(`<?xml version="1.0" encoding="utf-8"?>
<unattend xmlns="urn:schemas-microsoft-com:unattend">
  <settings pass="specialize">
    <component name="Microsoft-Windows-Shell-Setup">
      <ComputerName>MyPC</ComputerName>
    </component>
  </settings>
</unattend>`)
		Expect(isPureXMLNoDTD(data)).To(Succeed())
	})

	// TC-1.1 (variant): minimal valid XML
	It("Accepts minimal self-closing root element", func() {
		Expect(isPureXMLNoDTD([]byte(`<root/>`))).To(Succeed())
	})

	// Valid: XML with comments
	It("Accepts XML with comments", func() {
		data := []byte(`<!-- comment --><root><!-- inner --></root>`)
		Expect(isPureXMLNoDTD(data)).To(Succeed())
	})

	// Valid: XML with CDATA inside elements
	It("Accepts XML with CDATA sections inside elements", func() {
		data := []byte(`<root><![CDATA[some <data>]]></root>`)
		Expect(isPureXMLNoDTD(data)).To(Succeed())
	})

	// Valid: leading and trailing whitespace around root
	It("Accepts leading and trailing whitespace around root", func() {
		data := []byte("  \n\t<root/>\n  ")
		Expect(isPureXMLNoDTD(data)).To(Succeed())
	})

	// Valid: XML with processing instruction (<?xml ...?>)
	It("Accepts XML declaration processing instruction", func() {
		data := []byte(`<?xml version="1.0"?><root/>`)
		Expect(isPureXMLNoDTD(data)).To(Succeed())
	})

	// TC-1.3: Malformed XML rejected with parser error
	It("Rejects malformed XML", func() {
		err := isPureXMLNoDTD([]byte(`not xml at all`))
		Expect(err).To(HaveOccurred())
	})

	// TC-1.3 (variant): unclosed tag
	It("Rejects unclosed XML tags", func() {
		err := isPureXMLNoDTD([]byte(`<root><child></root>`))
		Expect(err).To(HaveOccurred())
	})

	// TC-1.4: Empty user_data rejected
	It("Rejects empty input", func() {
		err := isPureXMLNoDTD([]byte{})
		Expect(err).To(MatchError("no root element"))
	})

	// TC-1.5: Whitespace-only rejected
	It("Rejects whitespace-only input", func() {
		err := isPureXMLNoDTD([]byte("   \n\t  "))
		Expect(err).To(MatchError("no root element"))
	})

	// TC-1.7: Non-XML (cloud-config YAML) rejected for Windows
	It("Rejects cloud-config YAML", func() {
		err := isPureXMLNoDTD([]byte("#cloud-config\npackages:\n  - vim"))
		Expect(err).To(HaveOccurred())
	})

	// TC-1.9: DOCTYPE rejected
	It("Rejects DOCTYPE declaration", func() {
		err := isPureXMLNoDTD([]byte(`<!DOCTYPE html><html/>`))
		Expect(err).To(MatchError("DOCTYPE/DTD not allowed"))
	})

	// TC-1.10: Multiple root elements rejected
	It("Rejects multiple root elements", func() {
		err := isPureXMLNoDTD([]byte(`<a/><b/>`))
		Expect(err).To(MatchError("multiple root elements"))
	})

	// TC-1.11: Non-whitespace outside root rejected
	It("Rejects non-whitespace text outside root element", func() {
		err := isPureXMLNoDTD([]byte(`<root/>trailing text`))
		Expect(err).To(MatchError("non-whitespace text outside root element"))
	})

	// TC-1.11 (variant): text before root
	It("Rejects non-whitespace text before root element", func() {
		err := isPureXMLNoDTD([]byte(`leading text<root/>`))
		Expect(err).To(HaveOccurred())
	})

	// TC-1.12: DOCTYPE with internal subset rejected
	It("Rejects DOCTYPE with internal subset", func() {
		err := isPureXMLNoDTD([]byte(`<!DOCTYPE root [<!ELEMENT root EMPTY>]><root/>`))
		Expect(err).To(MatchError("DOCTYPE/DTD not allowed"))
	})

	// Edge case: plain text that looks like content
	It("Rejects plain text without any XML structure", func() {
		err := isPureXMLNoDTD([]byte(`Hello, World!`))
		Expect(err).To(HaveOccurred())
	})
})
