/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package text

import (
	"sort"
	"strings"
	"unicode"
)

// Dedent removes from the given string all the whitespace that is common to all the lines.
func Dedent(s string) string {
	// Handle the special case of the empty string:
	if len(s) == 0 {
		return s
	}

	// Split the text into lines, and remember if the last line ended with the end of the string, as we will need to
	// know that in order to decide if we should add a trailing end of line when joining the modified lines.
	var lines []string
	buffer := &strings.Builder{}
	for _, r := range s {
		if r == '\n' {
			lines = append(lines, buffer.String())
			buffer.Reset()
		} else {
			buffer.WriteRune(r)
		}
	}
	eos := buffer.Len() > 0
	if eos {
		lines = append(lines, buffer.String())
	}

	// Calculate the set of blank space prefixes set for all the non empty lines, and replace blank lines with empty
	// strings:
	set := map[string]bool{}
	for i, line := range lines {
		index := strings.IndexFunc(line, func(r rune) bool {
			return !unicode.IsSpace(r)
		})
		if index != -1 {
			prefix := line[0:index]
			set[prefix] = true
		} else {
			lines[i] = ""
		}
	}

	// Sort the prefixes by length, from longest to shortest:
	list := make([]string, len(set))
	i := 0
	for prefix := range set {
		list[i] = prefix
		i++
	}
	sort.Slice(list, func(i, j int) bool {
		return len(list[i]) > len(list[j])
	})

	// Find the the length of the longest prefix (first in the sorted list) that is a prefix of all the non empty
	// lines:
	var length int
	for _, prefix := range list {
		i := 0
		for _, line := range lines {
			if len(line) > 0 && !strings.HasPrefix(line, prefix) {
				break
			}
			i++
		}
		if i == len(lines) {
			length = len(prefix)
			break
		}
	}

	// Remove the longest prefix from all the non empty lines:
	for i, line := range lines {
		if len(line) > 0 {
			lines[i] = line[length:]
		}
	}

	// Join the lines:
	buffer.Reset()
	for i, line := range lines {
		if i > 0 {
			buffer.WriteString("\n")
		}
		buffer.WriteString(line)
	}
	if !eos {
		buffer.WriteString("\n")
	}
	return buffer.String()
}
