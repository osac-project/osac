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
	"bytes"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestWritePageNotice(t *testing.T) {
	t.Run("complete page writes nothing", func(t *testing.T) {
		var stderr bytes.Buffer
		err := WritePageNotice(&stderr, "billing accounts", 0, 5, 5)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if stderr.Len() != 0 {
			t.Errorf("expected no output, got %q", stderr.String())
		}
	})

	t.Run("partial page writes the continuation hint", func(t *testing.T) {
		var stderr bytes.Buffer
		err := WritePageNotice(&stderr, "billing accounts", 0, 2, 5)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		expected := "Showing 1-2 of 5 billing accounts. Next page: --offset 2\n"
		if stderr.String() != expected {
			t.Errorf("expected %q, got %q", expected, stderr.String())
		}
	})

	t.Run("empty page beyond the end explains the offset", func(t *testing.T) {
		var stderr bytes.Buffer
		err := WritePageNotice(&stderr, "billing accounts", 5, 0, 5)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		expected := "No billing accounts on this page: offset 5 is at or beyond the total 5.\n"
		if stderr.String() != expected {
			t.Errorf("expected %q, got %q", expected, stderr.String())
		}
	})

	t.Run("empty page inside the collection reports both bounds", func(t *testing.T) {
		var stderr bytes.Buffer
		err := WritePageNotice(&stderr, "tenant billing assignments", 2, 0, 5)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		expected := "No tenant billing assignments on this page (offset 2, total 5).\n"
		if stderr.String() != expected {
			t.Errorf("expected %q, got %q", expected, stderr.String())
		}
	})

	t.Run("empty collection writes nothing", func(t *testing.T) {
		var stderr bytes.Buffer
		err := WritePageNotice(&stderr, "billing accounts", 0, 0, 0)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if stderr.Len() != 0 {
			t.Errorf("expected no output, got %q", stderr.String())
		}
	})

	t.Run("failing writer returns a wrapped error", func(t *testing.T) {
		sentinel := errors.New("broken stderr")
		err := WritePageNotice(&failingWriter{err: sentinel}, "billing accounts", 0, 2, 5)
		if err == nil {
			t.Fatal("expected an error, got nil")
		}
		if !errors.Is(err, sentinel) {
			t.Errorf("expected the error to wrap %v, got %v", sentinel, err)
		}
		if want := "failed to write the billing accounts page notice"; !strings.Contains(err.Error(), want) {
			t.Errorf("expected the error to mention %q, got %q", want, err.Error())
		}
	})

	t.Run("near-MaxInt32 offset does not overflow", func(t *testing.T) {
		var stderr bytes.Buffer
		// offset + returned would overflow int32; the int64 arithmetic must survive it and
		// recognize that the page already reaches the total.
		err := WritePageNotice(&stderr, "billing accounts", math.MaxInt32-1, 5, math.MaxInt32)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if stderr.Len() != 0 {
			t.Errorf("expected no output for a page that reaches the total, got %q", stderr.String())
		}
	})

	t.Run("near-MaxInt32 offset writes the hint without overflowing", func(t *testing.T) {
		var stderr bytes.Buffer
		err := WritePageNotice(&stderr, "billing accounts", math.MaxInt32-2, 1, math.MaxInt32)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		expected := "Showing 2147483646-2147483646 of 2147483647 billing accounts. " +
			"Next page: --offset 2147483646\n"
		if stderr.String() != expected {
			t.Errorf("expected %q, got %q", expected, stderr.String())
		}
	})
}
