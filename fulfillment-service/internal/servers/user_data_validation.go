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
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
)

// isPureXMLNoDTD validates that data is well-formed XML with exactly one root element,
// no DOCTYPE/DTD directives, and no non-whitespace text outside the root element.
//
// It uses a streaming token loop via xml.NewDecoder (Go stdlib only, per design G-1)
// and enforces four invariants beyond basic well-formedness:
//
//  1. No DOCTYPE/DTD directives — the xml.Directive case rejects any <!DOCTYPE ...> token.
//  2. Exactly one root element — a second StartElement at depth 0 is rejected.
//  3. No non-whitespace text outside root — CharData at depth 0 must be whitespace-only.
//  4. At least one root element — after the loop, roots == 0 is rejected.
//
// Reference: design §4.3 (XML validation specification).
func isPureXMLNoDTD(data []byte) error {
	dec := xml.NewDecoder(bytes.NewReader(data))
	depth, roots := 0, 0
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err // catches all malformed XML
		}
		switch t := tok.(type) {
		case xml.Directive:
			return fmt.Errorf("DOCTYPE/DTD not allowed")
		case xml.StartElement:
			if depth == 0 {
				if roots++; roots > 1 {
					return fmt.Errorf("multiple root elements")
				}
			}
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 0 && len(bytes.TrimSpace(t)) > 0 {
				return fmt.Errorf("non-whitespace text outside root element")
			}
		}
	}
	if roots == 0 {
		return fmt.Errorf("no root element")
	}
	return nil
}
