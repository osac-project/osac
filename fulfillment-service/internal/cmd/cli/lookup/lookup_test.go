/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package lookup

import (
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	"github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"
)

// fakeItem is a minimal stand-in for any resource type.
type fakeItem struct{ id string }

// makeListFn returns a ListFunc that ignores the filter and returns items.
func makeListFn(items []*fakeItem) ListFunc[*fakeItem] {
	return func(filter string, limit int32) ([]*fakeItem, error) {
		if int(limit) < len(items) {
			return items[:limit], nil
		}
		return items, nil
	}
}

// errorListFn returns a ListFunc that always returns an error.
func errorListFn(msg string) ListFunc[*fakeItem] {
	return func(filter string, limit int32) ([]*fakeItem, error) {
		return nil, fmt.Errorf("%s", msg)
	}
}

// captureListFn returns a ListFunc that records the last filter it was called with.
func captureListFn(captured *string) ListFunc[*fakeItem] {
	return func(filter string, limit int32) ([]*fakeItem, error) {
		*captured = filter
		return []*fakeItem{{id: "x"}}, nil
	}
}

var _ = Describe("Find", func() {
	table.DescribeTable("CEL filter construction",
		func(ref, expectedFilter string) {
			var got string
			_, _ = Find(ref, "compute instance", captureListFn(&got))
			Expect(got).To(Equal(expectedFilter))
		},
		table.Entry("plain name",
			"my-vm",
			`this.id == "my-vm" || this.metadata.name == "my-vm"`),
		table.Entry("UUID-style ID",
			"550e8400-e29b-41d4-a716-446655440000",
			`this.id == "550e8400-e29b-41d4-a716-446655440000" || this.metadata.name == "550e8400-e29b-41d4-a716-446655440000"`),
		table.Entry("double quotes are escaped",
			`my"vm`,
			`this.id == "my\"vm" || this.metadata.name == "my\"vm"`),
		table.Entry("backslashes are escaped",
			`my\vm`,
			`this.id == "my\\vm" || this.metadata.name == "my\\vm"`),
		table.Entry("single quotes pass through unescaped",
			"my'vm",
			`this.id == "my'vm" || this.metadata.name == "my'vm"`),
	)

	Describe("match count handling", func() {
		It("should return the single item on exactly one match", func() {
			item := &fakeItem{id: "abc"}
			result, err := Find("abc", "compute instance", makeListFn([]*fakeItem{item}))
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(item))
		})

		It("should return ErrNotFound on zero matches", func() {
			_, err := Find("missing", "virtual network", makeListFn(nil))
			Expect(err).To(HaveOccurred())
			var notFound *ErrNotFound
			Expect(errors.As(err, &notFound)).To(BeTrue())
			Expect(notFound.Ref).To(Equal("missing"))
			Expect(notFound.Kind).To(Equal("virtual network"))
			Expect(err.Error()).To(ContainSubstring("virtual network"))
			Expect(err.Error()).To(ContainSubstring("missing"))
			Expect(err.Error()).To(ContainSubstring("not found"))
		})

		It("should return ErrAmbiguous on two or more matches", func() {
			items := []*fakeItem{{id: "a"}, {id: "b"}}
			_, err := Find("shared-name", "subnet", makeListFn(items))
			Expect(err).To(HaveOccurred())
			var ambiguous *ErrAmbiguous
			Expect(errors.As(err, &ambiguous)).To(BeTrue())
			Expect(ambiguous.Ref).To(Equal("shared-name"))
			Expect(ambiguous.Kind).To(Equal("subnet"))
			Expect(err.Error()).To(ContainSubstring("subnets"))
			Expect(err.Error()).To(ContainSubstring("shared-name"))
			Expect(err.Error()).To(ContainSubstring("use the ID instead"))
		})
	})

	Describe("empty reference", func() {
		It("should return an error without calling the list function", func() {
			called := false
			listFn := func(filter string, limit int32) ([]*fakeItem, error) {
				called = true
				return nil, nil
			}
			_, err := Find("", "compute instance", listFn)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("compute instance"))
			Expect(err.Error()).To(ContainSubstring("must not be empty"))
			Expect(called).To(BeFalse())
		})
	})

	Describe("list function error propagation", func() {
		It("should propagate errors returned by the list function", func() {
			_, err := Find("ref", "cluster", errorListFn("connection refused"))
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("connection refused"))
			var notFound *ErrNotFound
			Expect(errors.As(err, &notFound)).To(BeFalse())
		})
	})
})
