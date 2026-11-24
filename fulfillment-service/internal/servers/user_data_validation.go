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
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	grpccodes "google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

type xmlValidationReason string

const (
	xmlReasonInvalidUTF8         xmlValidationReason = "invalid_utf8"
	xmlReasonUnsupportedEncoding xmlValidationReason = "unsupported_encoding"
	xmlReasonMalformedSyntax     xmlValidationReason = "malformed_syntax"
	xmlReasonProhibitedDirective xmlValidationReason = "directive_prohibited"
	xmlReasonDuplicateAttribute  xmlValidationReason = "duplicate_attribute"
	xmlReasonNoRoot              xmlValidationReason = "no_root"
	xmlReasonMultipleRoots       xmlValidationReason = "multiple_roots"
	xmlReasonTextOutsideRoot     xmlValidationReason = "text_outside_root"
)

type xmlValidationError struct {
	reason xmlValidationReason
}

func (e *xmlValidationError) Error() string {
	return string(e.reason)
}

func newXMLValidationError(reason xmlValidationReason) error {
	return &xmlValidationError{reason: reason}
}

// mapWindowsXMLValidationError returns fixed wording without exposing XML content.
// field must describe the input field, never contain its submitted value.
func mapWindowsXMLValidationError(err error, field string) error {
	var xmlErr *xmlValidationError
	if errors.As(err, &xmlErr) {
		switch xmlErr.reason {
		case xmlReasonInvalidUTF8, xmlReasonUnsupportedEncoding:
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"%s has an unsupported encoding; UTF-8 is required", field)
		case xmlReasonProhibitedDirective:
			return grpcstatus.Errorf(grpccodes.InvalidArgument,
				"%s contains a prohibited XML directive", field)
		}
	}
	return grpcstatus.Errorf(grpccodes.InvalidArgument,
		"%s is not well-formed XML", field)
}

var utf8BOM = []byte{0xef, 0xbb, 0xbf}

// isPureXMLNoDTD validates a UTF-8 XML 1.0 document without normalizing its
// bytes. A UTF-8 BOM is ignored only for parsing and remains part of the input
// owned by the caller.
//
// This scanner follows the XML 1.0 (fifth edition) grammar, excluding DTDs,
// but requires a version declaration, when present, to be exactly "1.0";
// fifth-edition oddities such as "1.01" are rejected. encoding/xml cannot be
// the final validator: it rejects legal XML Names (including multiple colons
// and newer Unicode ranges) and accepts some forbidden characters. Names are
// compared lexically; namespace and Windows schema rules do not apply.
func isPureXMLNoDTD(data []byte) error {
	if !utf8.Valid(data) {
		return newXMLValidationError(xmlReasonInvalidUTF8)
	}

	for _, r := range string(data) {
		if !isXMLChar(r) {
			return newXMLValidationError(xmlReasonMalformedSyntax)
		}
	}
	parseData := bytes.TrimPrefix(data, utf8BOM)
	start, err := readXMLDeclaration(parseData)
	if err != nil {
		return err
	}
	return validateXMLDocument(parseData[start:])
}

// readXMLDeclaration consumes the optional declaration at the start of a
// document. The grammar is VersionInfo EncodingDecl? SDDecl? S?.
func readXMLDeclaration(data []byte) (int, error) {
	const declarationStart = "<?xml"
	if !bytes.HasPrefix(data, []byte(declarationStart)) ||
		len(data) == len(declarationStart) ||
		!isXMLWhitespace(data[len(declarationStart)]) {
		return 0, nil
	}

	end := bytes.Index(data, []byte("?>"))
	if end < 0 {
		return 0, newXMLValidationError(xmlReasonMalformedSyntax)
	}

	body := data[len(declarationStart):end]
	pos := skipXMLWhitespace(body, 0)

	name, value, next, ok := readXMLAttribute(body, pos)
	if !ok || string(name) != "version" || string(value) != "1.0" {
		return 0, newXMLValidationError(xmlReasonMalformedSyntax)
	}
	pos = next

	encodingSeen, standaloneSeen := false, false
	for pos < len(body) {
		if !isXMLWhitespace(body[pos]) {
			return 0, newXMLValidationError(xmlReasonMalformedSyntax)
		}
		pos = skipXMLWhitespace(body, pos)
		if pos == len(body) {
			break
		}

		name, value, next, ok = readXMLAttribute(body, pos)
		if !ok {
			return 0, newXMLValidationError(xmlReasonMalformedSyntax)
		}
		switch {
		case string(name) == "encoding" && !encodingSeen && !standaloneSeen:
			if !isXMLEncodingName(value) {
				return 0, newXMLValidationError(xmlReasonMalformedSyntax)
			}
			if !strings.EqualFold(string(value), "utf-8") {
				return 0, newXMLValidationError(xmlReasonUnsupportedEncoding)
			}
			encodingSeen = true
		case string(name) == "standalone" && !standaloneSeen:
			if string(value) != "yes" && string(value) != "no" {
				return 0, newXMLValidationError(xmlReasonMalformedSyntax)
			}
			standaloneSeen = true
		default:
			return 0, newXMLValidationError(xmlReasonMalformedSyntax)
		}
		pos = next
	}
	return end + 2, nil
}

// readXMLAttributeValue reads the equals sign and quoted value of an attribute
// whose name ends at pos, returning the unquoted value and the position after
// the closing quote.
func readXMLAttributeValue(data []byte, pos int) (value []byte, next int, ok bool) {
	next = skipXMLWhitespace(data, pos)
	if next >= len(data) || data[next] != '=' {
		return nil, next, false
	}
	next++
	next = skipXMLWhitespace(data, next)
	if next >= len(data) || (data[next] != '\'' && data[next] != '"') {
		return nil, next, false
	}

	quote := data[next]
	valueEnd := bytes.IndexByte(data[next+1:], quote)
	if valueEnd < 0 {
		return nil, next, false
	}
	return data[next+1 : next+1+valueEnd], next + valueEnd + 2, true
}

func readXMLAttribute(data []byte, pos int) (name, value []byte, next int, ok bool) {
	name, next, ok = readXMLName(data, pos)
	if !ok {
		return nil, nil, next, false
	}

	value, next, ok = readXMLAttributeValue(data, next)
	if !ok {
		return nil, nil, next, false
	}
	return name, value, next, true
}

func isXMLEncodingName(value []byte) bool {
	if len(value) == 0 {
		return false
	}

	if !isASCIIAlpha(value[0]) {
		return false
	}
	for _, b := range value[1:] {
		if !isXMLEncodingNameByte(b) {
			return false
		}
	}
	return true
}

func isASCIIAlpha(b byte) bool {
	return (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

func isXMLEncodingNameByte(b byte) bool {
	return isASCIIAlpha(b) ||
		(b >= '0' && b <= '9') ||
		b == '.' || b == '_' || b == '-'
}

// xmlDocumentParser keeps only open element names, not a decoded document.
// Iteration avoids a recursion limit on otherwise valid, size-bounded input.
type xmlDocumentParser struct {
	data     []byte
	pos      int
	elements [][]byte
	rootSeen bool
}

func validateXMLDocument(data []byte) error {
	p := xmlDocumentParser{data: data}
	for p.pos < len(p.data) {
		if err := p.readToken(); err != nil {
			return err
		}
	}
	if len(p.elements) != 0 {
		return newXMLValidationError(xmlReasonMalformedSyntax)
	}
	if !p.rootSeen {
		return newXMLValidationError(xmlReasonNoRoot)
	}
	return nil
}

func (p *xmlDocumentParser) readToken() error {
	rest := p.data[p.pos:]
	var err error
	switch {
	case rest[0] != '<':
		return p.readText()
	case bytes.HasPrefix(rest, []byte("<!--")):
		p.pos, err = validateXMLComment(p.data, p.pos)
	case bytes.HasPrefix(rest, []byte("<![CDATA[")):
		if len(p.elements) == 0 {
			return newXMLValidationError(xmlReasonMalformedSyntax)
		}
		p.pos, err = validateXMLCDATA(p.data, p.pos)
	case bytes.HasPrefix(rest, []byte("<!")):
		return newXMLValidationError(xmlReasonProhibitedDirective)
	case bytes.HasPrefix(rest, []byte("<?")):
		p.pos, err = validateXMLProcessingInstruction(p.data, p.pos)
	case bytes.HasPrefix(rest, []byte("</")):
		return p.readClosingTag()
	default:
		return p.readElement()
	}
	return err
}

func (p *xmlDocumentParser) readText() error {
	end := bytes.IndexByte(p.data[p.pos:], '<')
	if end < 0 {
		end = len(p.data) - p.pos
	}
	text := p.data[p.pos : p.pos+end]
	p.pos += end
	if len(p.elements) == 0 {
		// Outside the root, only literal XML whitespace is allowed. A reference
		// or CDATA section that would decode to whitespace is still invalid.
		if skipXMLWhitespace(text, 0) != len(text) {
			return newXMLValidationError(xmlReasonTextOutsideRoot)
		}
		return nil
	}
	if bytes.Contains(text, []byte("]]>")) {
		return newXMLValidationError(xmlReasonMalformedSyntax)
	}
	return validateXMLReferences(text)
}

func validateXMLComment(data []byte, start int) (int, error) {
	end := bytes.Index(data[start+4:], []byte("-->"))
	if end < 0 {
		return 0, newXMLValidationError(xmlReasonMalformedSyntax)
	}
	body := data[start+4 : start+4+end]
	if bytes.Contains(body, []byte("--")) || bytes.HasSuffix(body, []byte("-")) {
		return 0, newXMLValidationError(xmlReasonMalformedSyntax)
	}
	return start + end + 7, nil
}

func validateXMLCDATA(data []byte, start int) (int, error) {
	end := bytes.Index(data[start+9:], []byte("]]>"))
	if end < 0 {
		return 0, newXMLValidationError(xmlReasonMalformedSyntax)
	}
	return start + end + 12, nil
}

func validateXMLProcessingInstruction(data []byte, start int) (int, error) {
	name, next, ok := readXMLName(data, start+2)
	if !ok || strings.EqualFold(string(name), "xml") {
		return 0, newXMLValidationError(xmlReasonMalformedSyntax)
	}
	if next >= len(data) ||
		(!isXMLWhitespace(data[next]) && !bytes.HasPrefix(data[next:], []byte("?>"))) {
		return 0, newXMLValidationError(xmlReasonMalformedSyntax)
	}
	end := bytes.Index(data[next:], []byte("?>"))
	if end < 0 {
		return 0, newXMLValidationError(xmlReasonMalformedSyntax)
	}
	return next + end + 2, nil
}

func (p *xmlDocumentParser) readClosingTag() error {
	name, next, ok := readXMLName(p.data, p.pos+2)
	if !ok || len(p.elements) == 0 || !bytes.Equal(name, p.elements[len(p.elements)-1]) {
		return newXMLValidationError(xmlReasonMalformedSyntax)
	}
	next = skipXMLWhitespace(p.data, next)
	if next >= len(p.data) || p.data[next] != '>' {
		return newXMLValidationError(xmlReasonMalformedSyntax)
	}
	p.elements = p.elements[:len(p.elements)-1]
	p.pos = next + 1
	return nil
}

func (p *xmlDocumentParser) readElement() error {
	name, next, ok := readXMLName(p.data, p.pos+1)
	if !ok {
		return newXMLValidationError(xmlReasonMalformedSyntax)
	}
	if len(p.elements) == 0 {
		if p.rootSeen {
			return newXMLValidationError(xmlReasonMultipleRoots)
		}
		p.rootSeen = true
	}
	p.pos = next

	attributes := make(map[string]struct{})
	for {
		attributeStart := p.pos
		p.pos = skipXMLWhitespace(p.data, p.pos)
		if p.pos >= len(p.data) {
			return newXMLValidationError(xmlReasonMalformedSyntax)
		}
		if p.data[p.pos] == '>' {
			p.pos++
			p.elements = append(p.elements, name)
			return nil
		}
		if bytes.HasPrefix(p.data[p.pos:], []byte("/>")) {
			p.pos += 2
			return nil
		}
		if attributeStart == p.pos {
			return newXMLValidationError(xmlReasonMalformedSyntax)
		}

		attribute, value, valueNext, ok := readXMLAttribute(p.data, p.pos)
		if !ok || bytes.ContainsRune(value, '<') {
			return newXMLValidationError(xmlReasonMalformedSyntax)
		}
		if err := validateXMLReferences(value); err != nil {
			return err
		}
		key := string(attribute)
		if _, exists := attributes[key]; exists {
			return newXMLValidationError(xmlReasonDuplicateAttribute)
		}
		attributes[key] = struct{}{}
		p.pos = valueNext
	}
}

// References occur only in character data and attribute values. They are never
// expanded: without DTDs, only the five predefined entities and character
// references are legal. Check code points before converting them to runes.
func validateXMLReferences(data []byte) error {
	for len(data) > 0 {
		start := bytes.IndexByte(data, '&')
		if start < 0 {
			return nil
		}
		data = data[start+1:]
		end := bytes.IndexByte(data, ';')
		if end < 0 || !isXMLReference(data[:end]) {
			return newXMLValidationError(xmlReasonMalformedSyntax)
		}
		data = data[end+1:]
	}
	return nil
}

func isXMLReference(ref []byte) bool {
	switch string(ref) {
	case "amp", "lt", "gt", "apos", "quot":
		return true
	}
	if len(ref) < 2 || ref[0] != '#' {
		return false
	}
	digits, base := ref[1:], 10
	if digits[0] == 'x' {
		digits, base = digits[1:], 16
	}
	for _, b := range digits {
		if b >= '0' && b <= '9' {
			continue
		}
		if base == 16 && ((b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')) {
			continue
		}
		return false
	}
	value, err := strconv.ParseUint(string(digits), base, 32)
	return err == nil && value <= utf8.MaxRune && isXMLChar(rune(value))
}

// XML 1.0 fifth edition, production [2] (Char).
func isXMLChar(r rune) bool {
	return r == '\t' || r == '\n' || r == '\r' ||
		(r >= 0x20 && r <= 0xd7ff) ||
		(r >= 0xe000 && r <= 0xfffd) ||
		(r >= 0x10000 && r <= 0x10ffff)
}

func readXMLName(data []byte, start int) (name []byte, next int, ok bool) {
	if start >= len(data) {
		return nil, start, false
	}

	r, size := utf8.DecodeRune(data[start:])
	if !isXMLNameStartRune(r) {
		return nil, start, false
	}

	next = start + size
	for next < len(data) {
		r, size = utf8.DecodeRune(data[next:])
		if !isXMLNameRune(r) {
			break
		}
		next += size
	}
	return data[start:next], next, true
}

func isXMLWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n'
}

func skipXMLWhitespace(data []byte, start int) int {
	for start < len(data) && isXMLWhitespace(data[start]) {
		start++
	}
	return start
}

// XML 1.0 fifth edition, production [4] (NameStartChar).
func isXMLNameStartRune(r rune) bool {
	switch {
	case r == ':' || r == '_':
		return true
	case isASCIIAlphaRune(r):
		return true
	case r >= 0xc0 && r <= 0xd6:
		return true
	case r >= 0xd8 && r <= 0xf6:
		return true
	case r >= 0xf8 && r <= 0x2ff:
		return true
	case r >= 0x370 && r <= 0x37d:
		return true
	case r >= 0x37f && r <= 0x1fff:
		return true
	default:
		return isExtendedXMLNameStartRune(r)
	}
}

func isASCIIAlphaRune(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
}

func isExtendedXMLNameStartRune(r rune) bool {
	switch {
	case r == 0x200c || r == 0x200d:
		return true
	case r >= 0x2070 && r <= 0x218f:
		return true
	case r >= 0x2c00 && r <= 0x2fef:
		return true
	case r >= 0x3001 && r <= 0xd7ff:
		return true
	case r >= 0xf900 && r <= 0xfdcf:
		return true
	case r >= 0xfdf0 && r <= 0xfffd:
		return true
	case r >= 0x10000 && r <= 0xeffff:
		return true
	default:
		return false
	}
}

// XML 1.0 fifth edition, production [4a] (NameChar).
func isXMLNameRune(r rune) bool {
	switch {
	case r == '-' || r == '.' || r == 0xb7:
		return true
	case r >= '0' && r <= '9':
		return true
	case r >= 0x300 && r <= 0x36f:
		return true
	case r >= 0x203f && r <= 0x2040:
		return true
	default:
		return isXMLNameStartRune(r)
	}
}
