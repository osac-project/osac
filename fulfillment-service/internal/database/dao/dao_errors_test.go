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
				Kind: "tenant",
				Name: "my-tenant",
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

		It("Returns kind in error message for projects", func() {
			err := &ErrAlreadyExists{
				Kind: "project",
				Name: "my-name",
			}
			Expect(err.Error()).To(Equal("project 'my-name' already exists"))
		})

		It("Returns kind in error message for virtual networks", func() {
			err := &ErrAlreadyExists{
				Kind: "virtual network",
				Name: "my-vnet",
			}
			Expect(err.Error()).To(Equal("virtual network 'my-vnet' already exists"))
		})

		It("Returns kind in error message for cluster orders", func() {
			err := &ErrAlreadyExists{
				Kind: "cluster order",
				Name: "my-cluster",
			}
			Expect(err.Error()).To(Equal("cluster order 'my-cluster' already exists"))
		})

		It("Returns kind in error message for security groups", func() {
			err := &ErrAlreadyExists{
				Kind: "security group",
				ID:   "sg-123",
				Name: "my-sg",
			}
			Expect(err.Error()).To(Equal(
				"security group with identifier 'sg-123' and name 'my-sg' already exists",
			))
		})

		It("Returns kind in error message for NAT gateways", func() {
			err := &ErrAlreadyExists{
				Kind: "NAT gateway",
				Name: "my-natgw",
			}
			Expect(err.Error()).To(Equal("NAT gateway 'my-natgw' already exists"))
		})

		It("Returns kind in error message for external IP pools", func() {
			err := &ErrAlreadyExists{
				Kind: "external IP pool",
				Name: "my-pool",
			}
			Expect(err.Error()).To(Equal("external IP pool 'my-pool' already exists"))
		})

		It("Returns kind in error message for compute instances", func() {
			err := &ErrAlreadyExists{
				Kind: "compute instance",
				Name: "my-ci",
			}
			Expect(err.Error()).To(Equal("compute instance 'my-ci' already exists"))
		})

		It("Returns kind in error message for compute instance templates", func() {
			err := &ErrAlreadyExists{
				Kind: "compute instance template",
				Name: "my-template",
			}
			Expect(err.Error()).To(Equal("compute instance template 'my-template' already exists"))
		})

		It("Returns kind in error message for host types", func() {
			err := &ErrAlreadyExists{
				Kind: "host type",
				Name: "my-host-type",
			}
			Expect(err.Error()).To(Equal("host type 'my-host-type' already exists"))
		})

		It("Returns kind in error message for hubs", func() {
			err := &ErrAlreadyExists{
				Kind: "hub",
				Name: "my-hub",
			}
			Expect(err.Error()).To(Equal("hub 'my-hub' already exists"))
		})

		It("Returns custom reason verbatim when set, ignoring kind", func() {
			err := &ErrAlreadyExists{
				Kind:   "compute instance",
				ID:     "ci-123",
				Reason: "custom trigger message",
			}
			Expect(err.Error()).To(Equal("custom trigger message"))
		})

		It("Falls back to 'object' when kind is empty", func() {
			err := &ErrAlreadyExists{
				Name: "my-thing",
			}
			Expect(err.Error()).To(Equal("object 'my-thing' already exists"))
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

	Describe("ErrInvalidFilter", func() {
		It("Implements the error interface", func() {
			var err error = &ErrInvalidFilter{Reason: "field doesn't exist"}
			Expect(err).To(HaveOccurred())
		})

		It("Returns the Reason field as the error message", func() {
			err := &ErrInvalidFilter{Reason: "field 'my_field' doesn't exist"}
			Expect(err.Error()).To(Equal("field 'my_field' doesn't exist"))
		})

		It("Returns empty string when Reason is empty", func() {
			err := &ErrInvalidFilter{}
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
