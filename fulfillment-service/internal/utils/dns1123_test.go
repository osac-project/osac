/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with
the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package utils

import (
	"fmt"
	"testing"
)

func TestSanitizeDNS1123Name(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		maxBase int
		suffix  func(string) string
		want    string
	}{
		{
			name:  "dashes the runes a name refuses and trims the ends",
			input: "  Service_Account.v2! ", maxBase: 0, suffix: nil,
			want: "service-account-v2",
		},
		{
			name:  "returns an empty base for punctuation only",
			input: "???", maxBase: 0, suffix: nil,
			want: "",
		},
		{
			name:  "truncates a long base and trims the dash the cut leaves",
			input: "abcdefghi.klmno", maxBase: 10, suffix: nil,
			want: "abcdefghi",
		},
		{
			name:  "keeps a base that fits the max",
			input: "abcdefghij", maxBase: 10, suffix: nil,
			want: "abcdefghij",
		},
		{
			name:  "appends the suffix generated from the original input",
			input: "Compute.VCPUs", maxBase: 54,
			suffix: func(input string) string { return fmt.Sprintf("-%x", len(input)) },
			want:   "compute-vcpus-d",
		},
	}
	for _, c := range cases {
		if got := SanitizeDNS1123Name(c.input, c.maxBase, c.suffix); got != c.want {
			t.Errorf("%s: SanitizeDNS1123Name(%q, %d) = %q, want %q", c.name, c.input, c.maxBase, got, c.want)
		}
	}
}
