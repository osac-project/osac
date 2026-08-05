/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package dao

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Errors", func() {
	Describe("ErrNotFound", func() {
		It("Implements the error interface", func() {
			var err error = &ErrNotFound{IDs: []string{"123"}}
			Expect(err).To(HaveOccurred())
		})

		It("Returns generic message when there are no identifiers", func() {
			err := &ErrNotFound{}
			Expect(err.Error()).To(Equal("object not found"))
		})

		It("Returns expected message for a single identifier", func() {
			err := &ErrNotFound{IDs: []string{"my-id"}}
			Expect(err.Error()).To(Equal("object with identifier 'my-id' not found"))
		})

		It("Returns expected message for two identifiers", func() {
			err := &ErrNotFound{IDs: []string{"a", "b"}}
			Expect(err.Error()).To(Equal("objects with identifiers 'a' and 'b' not found"))
		})

		It("Returns expected message for three identifiers", func() {
			err := &ErrNotFound{IDs: []string{"a", "b", "c"}}
			Expect(err.Error()).To(Equal("objects with identifiers 'a', 'b' and 'c' not found"))
		})
	})

	Describe("ErrAlreadyExists", func() {
		It("Implements the error interface", func() {
			var err error = &ErrAlreadyExists{}
			Expect(err).To(HaveOccurred())
		})

		It("Returns expected error message when identifier is set", func() {
			err := &ErrAlreadyExists{
				ID: "my-id",
			}
			Expect(err.Error()).To(Equal("object with identifier 'my-id' already exists"))
		})

		It("Returns expected message for a tenant with a name", func() {
			err := &ErrAlreadyExists{
				Table: "tenants",
				Name:  "my-tenant",
			}
			Expect(err.Error()).To(Equal("tenant 'my-tenant' already exists"))
		})

		It("Returns expected error message when name is set", func() {
			err := &ErrAlreadyExists{
				Name: "my-name",
			}
			Expect(err.Error()).To(Equal("object 'my-name' already exists"))
		})

		It("Returns expected error message when both identifier and name are set", func() {
			err := &ErrAlreadyExists{
				ID:   "my-id",
				Name: "my-name",
			}
			Expect(err.Error()).To(Equal("object with identifier 'my-id' and name 'my-name' already exists"))
		})

		It("Returns a generic error message when neither identifier nor name are set", func() {
			err := &ErrAlreadyExists{}
			Expect(err.Error()).To(Equal("object already exists"))
		})

		It("Returns custom message for projects", func() {
			err := &ErrAlreadyExists{
				Table: "projects",
				Name:  "my-name",
			}
			Expect(err.Error()).To(Equal("project 'my-name' already exists"))
		})
	})

	Describe("ErrImmutable", func() {
		It("Implements the error interface", func() {
			var err error = &ErrImmutable{
				Fields: []string{
					"metadata.name",
				},
			}
			Expect(err).To(HaveOccurred())
		})

		It("Returns expected message for a zero fields", func() {
			err := &ErrImmutable{
				Fields: nil,
			}
			Expect(err.Error()).To(Equal(
				"some fields are immutable",
			))
		})

		It("Returns expected message for a single column", func() {
			err := &ErrImmutable{
				Fields: []string{
					"metadata.name",
				},
			}
			Expect(err.Error()).To(Equal(
				"field 'metadata.name' is immutable",
			))
		})

		It("Returns expected message for two columns", func() {
			err := &ErrImmutable{
				Fields: []string{
					"metadata.name",
					"metadata.tenant",
				},
			}
			Expect(err.Error()).To(Equal(
				"fields 'metadata.name' and 'metadata.tenant' are immutable",
			))
		})

		It("Returns expected message for three columns", func() {
			err := &ErrImmutable{
				Fields: []string{
					"metadata.name",
					"metadata.tenant",
					"metadata.creator",
				},
			}
			Expect(err.Error()).To(Equal(
				"fields 'metadata.creator', 'metadata.name' and 'metadata.tenant' are immutable",
			))
		})
	})

	Describe("ErrDenied", func() {
		It("Implements the error interface", func() {
			var err error = &ErrDenied{Reason: "not allowed"}
			Expect(err).To(HaveOccurred())
		})

		It("Returns the Reason field as the error message", func() {
			err := &ErrDenied{Reason: "operation not permitted"}
			Expect(err.Error()).To(Equal("operation not permitted"))
		})

		It("Returns empty string when Reason is empty", func() {
			err := &ErrDenied{}
			Expect(err.Error()).To(BeEmpty())
		})
	})

	Describe("ErrReference", func() {
		It("Implements the error interface", func() {
			var err error = &ErrReference{
				Reason: "tenant 'my-tenant' doesn't exist",
			}
			Expect(err).To(HaveOccurred())
		})

		It("Returns the reason field as the error message", func() {
			err := &ErrReference{
				Reason: "tenant 'my-tenant' doesn't exist",
			}
			Expect(err.Error()).To(Equal("tenant 'my-tenant' doesn't exist"))
		})

		It("Returns general message when reason is empty", func() {
			err := &ErrReference{}
			Expect(err.Error()).To(Equal("some reference is invalid"))
		})
	})
})
