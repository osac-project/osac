/*
Copyright (c) 2025 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package auth

import (
	"context"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
)

var _ = Describe("Ticket credentials", func() {
	var ctx context.Context

	Describe("Creation", func() {
		It("Can be created with a ticket", func() {
			credentials := NewTicketCredentials("my-ticket")
			Expect(credentials).ToNot(BeNil())
		})

		It("Can be created with an empty ticket", func() {
			credentials := NewTicketCredentials("")
			Expect(credentials).ToNot(BeNil())
		})
	})

	Describe("Behavior", func() {
		It("Adds Authorization header to metadata", func() {
			credentials := NewTicketCredentials("my-ticket")

			metadata, err := credentials.GetRequestMetadata(ctx, "localhost:8000")
			Expect(err).ToNot(HaveOccurred())
			Expect(metadata).ToNot(BeNil())
			Expect(metadata).To(HaveKeyWithValue("Authorization", "Bearer my-ticket"))
		})

		It("Requires transport security", func() {
			credentials := NewTicketCredentials("my-ticket")

			security := credentials.RequireTransportSecurity()
			Expect(security).To(BeTrue())
		})
	})
})
