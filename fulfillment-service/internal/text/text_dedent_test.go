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
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
)

var _ = DescribeTable(
	"Dedent",
	func(input, expected string) {
		actual := Dedent(input)
		Expect(actual).To(Equal(expected))
	},
	Entry(
		"Empty",
		"",
		"",
	),
	Entry(
		"Line break",
		"\n",
		"\n",
	),
	Entry(
		"One line without line break",
		"first line",
		"first line",
	),
	Entry(
		"One line with line break",
		"first line\n",
		"first line\n",
	),
	Entry(
		"Two lines with one line break",
		"first line\nsecond line",
		"first line\nsecond line",
	),
	Entry(
		"Two lines with one line break",
		"first line\nsecond line\n",
		"first line\nsecond line\n",
	),
	Entry(
		"Trailing blank line without line break",
		"first line\n ",
		"first line\n",
	),
	Entry(
		"Leading blank line",
		" \nfirst line\n",
		"\nfirst line\n",
	),
	Entry(
		"One leading space",
		" first line\n second line\n",
		"first line\nsecond line\n",
	),
	Entry(
		"Multiple leading spaces",
		"  first line\n  second line\n",
		"first line\nsecond line\n",
	),
	Entry(
		"One leading tab",
		"\tfirst line\n\tsecond line\n",
		"first line\nsecond line\n",
	),
	Entry(
		"Multiple leading tabs",
		"\t\tfirst line\n\t\tsecond line\n",
		"first line\nsecond line\n",
	),
	Entry(
		"Mix of leading spaces and tabs",
		"\t first line\n\t second line\n",
		"first line\nsecond line\n",
	),
	Entry(
		"One empty line in the middle",
		"  first line\n\n  second line\n",
		"first line\n\nsecond line\n",
	),
	Entry(
		"Multiple empty lines in the middle",
		"  first line\n\n\n  second line\n",
		"first line\n\n\nsecond line\n",
	),
	Entry(
		"Two prefixes of different lengths",
		"  first line\n second line\n",
		" first line\nsecond line\n",
	),
	Entry(
		"Two prefixes of different lengths (reversed)",
		" first line\n  second line\n",
		"first line\n second line\n",
	),
	Entry(
		"Line with one trailing space",
		"first line \n",
		"first line \n",
	),
	Entry(
		"Line with two trailing space",
		"first line  \n",
		"first line  \n",
	),
)
