/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package servers

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("isPureXMLNoDTD", func() {
	DescribeTable("validates the Windows XML profile",
		func(data []byte, expectedReason string) {
			original := bytes.Clone(data)
			err := isPureXMLNoDTD(data)
			Expect(data).To(Equal(original), "validation must preserve the input bytes")
			if expectedReason == "" {
				Expect(err).To(Succeed())
				return
			}
			Expect(err).To(MatchError(expectedReason))
		},
		Entry("accepts a UTF-8 declaration", []byte(`<?xml version="1.0" encoding="utf-8"?><root/>`), ""),
		Entry("accepts a mixed-case UTF-8 declaration", []byte(`<?xml version="1.0" encoding="UTF-8"?><root/>`), ""),
		Entry("accepts whitespace around declaration pseudo-attribute equals", []byte(`<?xml version = "1.0" encoding = "utf-8"?><root/>`), ""),
		Entry("accepts XML without a declaration", []byte(`<root/>`), ""),
		Entry("accepts a UTF-8 BOM", append([]byte{0xef, 0xbb, 0xbf}, []byte(`<?xml version="1.0"?><root/>`)...), ""),
		Entry("accepts unbound prefixes", []byte(`<prefix:root prefix:attr="value"/>`), ""),
		Entry("accepts distinct lexical names with the same expanded name", []byte(`<root xmlns:p="urn:test" xmlns:q="urn:test" p:attr="one" q:attr="two"/>`), ""),
		Entry("accepts an empty processing instruction", []byte(`<?processor?><root/>`), ""),
		Entry("accepts a processing instruction", []byte(`<?processor data?><root/>`), ""),
		Entry("accepts a leading xml-stylesheet processing instruction", []byte(`<?xml-stylesheet type="text/xsl" href="style.xsl"?><root/>`), ""),
		Entry("accepts comments and CDATA", []byte(`<!-- comment --><root><![CDATA[raw < data>]]></root>`), ""),
		Entry("accepts legal Unicode names", []byte(`<日本語 属性="値"/>`), ""),
		Entry("accepts whitespace around attribute equals", []byte(`<root attr = "value"/>`), ""),
		Entry("accepts trailing whitespace in a version-only declaration", []byte("<?xml version='1.0' \t\r\n?><root/>"), ""),
		Entry("accepts trailing whitespace after encoding", []byte(`<?xml version="1.0" encoding="utf-8" ?><root/>`), ""),
		Entry("accepts standalone without encoding", []byte(`<?xml version="1.0" standalone="yes" ?><root/>`), ""),
		Entry("accepts all declaration attributes", []byte(`<?xml version="1.0" encoding="uTf-8" standalone="no" ?><root/>`), ""),
		Entry("accepts lexical names with multiple colons", []byte(`<a:b:c a:b:c="value"></a:b:c>`), ""),
		Entry("accepts XML 1.0 fifth-edition names", []byte("<?\U00010000?><\u037f \u200c='value'><\U00010000/></\u037f>"), ""),
		Entry("accepts character and predefined entity references", []byte(`<root attr="&lt;&gt;&amp;&apos;&quot;&#9;&#xA;">&#13;&#x20;&#xD7FF;&#xE000;&#xFFFD;&#x10000;&#x10FFFF;</root>`), ""),
		Entry("accepts reference-like and directive-like literal text", []byte(`<?pi &unknown; <!DOCTYPE literal>?><!-- &unknown; <!DOCTYPE literal> --><root><![CDATA[&unknown; <!DOCTYPE literal>]]></root>`), ""),
		Entry("accepts miscellaneous markup around the root", []byte("<!--before--><?before?>\n<root/>\r\n<?after?><!--after-->"), ""),
		Entry("accepts nested Windows answer-file content", []byte(`<?xml version="1.0" encoding="utf-8" ?>
<unattend xmlns="urn:schemas-microsoft-com:unattend">
  <settings pass="specialize">
    <component name="Microsoft-Windows-Shell-Setup" processorArchitecture="amd64">
      <ComputerName>desktop</ComputerName>
      <RegisteredOwner>Research &amp; Development</RegisteredOwner>
    </component>
  </settings>
</unattend>`), ""),

		Entry("rejects raw invalid UTF-8", []byte{0xff, 0xfe, '<', 'r', 'o', 'o', 't', '/'}, "invalid_utf8"),
		Entry("rejects UTF-16 bytes", []byte{0xfe, 0xff, 0x00, 0x3c, 0x00, 0x72}, "invalid_utf8"),
		Entry("rejects UTF-8 declaring UTF-16", []byte(`<?xml version="1.0" encoding="UTF-16"?><root/>`), "unsupported_encoding"),
		Entry("rejects an empty encoding name", []byte(`<?xml version="1.0" encoding=""?><root/>`), "malformed_syntax"),
		Entry("rejects an encoding name with an invalid first character", []byte(`<?xml version="1.0" encoding="1bad"?><root/>`), "malformed_syntax"),
		Entry("rejects missing whitespace between declaration pseudo-attributes", []byte(`<?xml version="1.0"encoding="utf-8"?><root/>`), "malformed_syntax"),
		Entry("rejects a declaration without version", []byte(`<?xml encoding="utf-8"?><root/>`), "malformed_syntax"),
		Entry("rejects an unknown declaration pseudo-attribute", []byte(`<?xml version="1.0" encoding="utf-8" bogus="x"?><root/>`), "malformed_syntax"),
		Entry("rejects an XML declaration after the root", []byte(`<root/><?xml version="1.0" encoding="utf-8"?>`), "malformed_syntax"),
		Entry("rejects declaration attributes in the wrong order", []byte(`<?xml encoding="utf-8" version="1.0"?><root/>`), "malformed_syntax"),
		Entry("rejects a duplicate declaration pseudo-attribute", []byte(`<?xml version="1.0" version="1.0"?><root/>`), "malformed_syntax"),
		Entry("rejects an invalid declaration version", []byte(`<?xml version="1.1"?><root/>`), "malformed_syntax"),
		Entry("rejects an invalid declaration standalone value", []byte(`<?xml version="1.0" standalone="maybe"?><root/>`), "malformed_syntax"),
		Entry("rejects truncated XML", []byte(`<root><child></root>`), "malformed_syntax"),
		Entry("rejects duplicate lexical attributes", []byte(`<root attr="one" attr="two"/>`), "duplicate_attribute"),
		Entry("rejects adjacent attributes", []byte(`<root attr="one"attr="two"/>`), "malformed_syntax"),
		Entry("rejects a malformed processing-instruction target", []byte(`<?1invalid?><root/>`), "malformed_syntax"),
		Entry("rejects a processing instruction without a separator after its target", []byte(`<?foo!?><root/>`), "malformed_syntax"),
		Entry("rejects a reserved xml processing instruction", []byte(`<?xml?><root/>`), "malformed_syntax"),
		Entry("rejects a truncated processing instruction", []byte(`<?xml`), "malformed_syntax"),
		Entry("rejects an empty document", []byte{}, "no_root"),
		Entry("rejects whitespace-only input", []byte(" \n\t "), "no_root"),
		Entry("rejects multiple roots", []byte(`<a/><b/>`), "multiple_roots"),
		Entry("rejects text outside the root", []byte(`<root/>trailing`), "text_outside_root"),
		Entry("rejects a non-XML whitespace space outside the root", []byte("<root/>\u00a0"), "text_outside_root"),
		Entry("rejects a DOCTYPE", []byte(`<!DOCTYPE root><root/>`), "directive_prohibited"),
		Entry("rejects an internal DTD subset", []byte(`<!DOCTYPE root [<!ELEMENT root EMPTY>]><root/>`), "directive_prohibited"),
		Entry("rejects forbidden literal characters in comments", []byte("<!--\x00--><root/>"), "malformed_syntax"),
		Entry("rejects forbidden literal characters in processing instructions", []byte("<?pi \x01?><root/>"), "malformed_syntax"),
		Entry("rejects noncharacters in comments", []byte("<!--\ufffe--><root/>"), "malformed_syntax"),
		Entry("rejects surrogate references", []byte(`<root>&#xD800;</root>`), "malformed_syntax"),
		Entry("rejects character references before the root", []byte(`&#32;<root/>`), "text_outside_root"),
		Entry("rejects character references after the root", []byte(`<root/>&#x20;`), "text_outside_root"),
		Entry("rejects CDATA before the root", []byte(`<![CDATA[ ]]><root/>`), "malformed_syntax"),
		Entry("rejects empty CDATA after the root", []byte(`<root/><![CDATA[]]>`), "malformed_syntax"),
		Entry("rejects an unclosed root", []byte(`<root>`), "malformed_syntax"),
		Entry("rejects an unmatched closing tag", []byte(`<root/></root>`), "malformed_syntax"),
		Entry("rejects mismatched lexical prefixes", []byte(`<a:root></b:root>`), "malformed_syntax"),
		Entry("rejects attributes in closing tags", []byte(`<root></root attr='value'>`), "malformed_syntax"),
		Entry("rejects whitespace after an empty-element slash", []byte(`<root/ >`), "malformed_syntax"),
		Entry("rejects an invalid element name", []byte(`<1root/>`), "malformed_syntax"),
		Entry("rejects an invalid attribute name", []byte(`<root 1attr='value'/>`), "malformed_syntax"),
		Entry("rejects an unquoted attribute", []byte(`<root attr=value/>`), "malformed_syntax"),
		Entry("rejects raw less-than in attributes", []byte(`<root attr='<'/>`), "malformed_syntax"),
		Entry("rejects unknown entities in attributes", []byte(`<root attr='&unknown;'/>`), "malformed_syntax"),
		Entry("rejects unknown entities in text", []byte(`<root>&unknown;</root>`), "malformed_syntax"),
		Entry("rejects unterminated references", []byte(`<root>&amp</root>`), "malformed_syntax"),
		Entry("rejects empty references", []byte(`<root>&;</root>`), "malformed_syntax"),
		Entry("rejects zero character references", []byte(`<root>&#0;</root>`), "malformed_syntax"),
		Entry("rejects forbidden control references", []byte(`<root>&#x1;</root>`), "malformed_syntax"),
		Entry("rejects out-of-range references", []byte(`<root>&#x110000;</root>`), "malformed_syntax"),
		Entry("rejects overflowing references", []byte(`<root>&#999999999999999999999999999999999999999;</root>`), "malformed_syntax"),
		Entry("rejects signed numeric references", []byte(`<root>&#+32;</root>`), "malformed_syntax"),
		Entry("rejects uppercase X in references", []byte(`<root>&#X20;</root>`), "malformed_syntax"),
		Entry("rejects a bare CDATA terminator", []byte(`<root>]]></root>`), "malformed_syntax"),
		Entry("rejects a double hyphen in a comment", []byte(`<!-- a--b --><root/>`), "malformed_syntax"),
		Entry("rejects a comment ending in a hyphen", []byte(`<!-- a---><root/>`), "malformed_syntax"),
		Entry("rejects an unterminated comment", []byte(`<root><!--`), "malformed_syntax"),
		Entry("rejects an unterminated CDATA section", []byte(`<root><![CDATA[`), "malformed_syntax"),
		Entry("rejects an unterminated processing instruction", []byte(`<root><?pi`), "malformed_syntax"),
		Entry("rejects a declaration after whitespace", []byte(` <?xml version='1.0'?><root/>`), "malformed_syntax"),
		Entry("rejects a mixed-case reserved XML target", []byte(`<?XmL version='1.0'?><root/>`), "malformed_syntax"),
		Entry("rejects encoding after standalone", []byte(`<?xml version='1.0' standalone='yes' encoding='utf-8'?><root/>`), "malformed_syntax"),
	)

	It("rejects malformed XML without exposing its content", func() {
		sentinel := "credential-like-sentinel"
		err := isPureXMLNoDTD([]byte(`<root>` + sentinel + `</wrong>`))
		Expect(err).To(MatchError("malformed_syntax"))
		Expect(err.Error()).NotTo(ContainSubstring(sentinel))
	})

	It("rejects an external HTTP entity without resolving it", func(ctx SpecContext) {
		specCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		DeferCleanup(cancel)

		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			requests.Add(1)
		}))
		DeferCleanup(server.Close)

		data := []byte(`<!DOCTYPE root [<!ENTITY external SYSTEM "` + server.URL + `">]><root>&external;</root>`)
		err := isPureXMLNoDTD(data)
		Expect(err).To(MatchError("directive_prohibited"))
		Expect(requests.Load()).To(Equal(int32(0)))

		request, err := http.NewRequestWithContext(specCtx, http.MethodGet, server.URL, nil)
		Expect(err).NotTo(HaveOccurred())
		response, err := server.Client().Do(request)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(response.Body.Close)
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		Expect(requests.Load()).To(Equal(int32(1)))
	})
})
