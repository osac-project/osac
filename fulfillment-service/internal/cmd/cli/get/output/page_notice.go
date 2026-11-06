/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package output

import (
	"fmt"
	"io"
)

// WritePageNotice writes the table-mode paging hint to w, which must be the error stream of the console so that it
// never mixes with the table on standard output. label names the entity, offset is the page offset the caller asked
// for, returned is the number of items the server actually returned, and total is the total number of items across
// all pages. Nothing is written when there is no more data (an empty collection, or a page that reaches the end);
// the caller prints the empty-collection message on standard output in the total == 0 case. All arithmetic uses
// int64 so that a near-MaxInt32 offset cannot overflow. Write failures are wrapped and returned.
func WritePageNotice(w io.Writer, label string, offset int32, returned int, total int32) error {
	if total <= 0 {
		return nil
	}
	if returned == 0 {
		if int64(offset) >= int64(total) {
			return writePageNotice(w, label, fmt.Sprintf(
				"No %s on this page: offset %d is at or beyond the total %d.\n", label, offset, total))
		}
		return writePageNotice(w, label, fmt.Sprintf(
			"No %s on this page (offset %d, total %d).\n", label, offset, total))
	}
	next := int64(offset) + int64(returned)
	if next >= int64(total) {
		return nil
	}
	return writePageNotice(w, label, fmt.Sprintf(
		"Showing %d-%d of %d %s. Next page: --offset %d\n", int64(offset)+1, next, total, label, next))
}

// writePageNotice writes one already-formatted notice line, wrapping any write failure with the entity label.
func writePageNotice(w io.Writer, label string, text string) error {
	if _, err := fmt.Fprint(w, text); err != nil {
		return fmt.Errorf("failed to write the %s page notice: %w", label, err)
	}
	return nil
}
