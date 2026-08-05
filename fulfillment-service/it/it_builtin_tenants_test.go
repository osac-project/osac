/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package it

import (
	"context"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/ginkgo/v2/dsl/table"
	. "github.com/onsi/gomega"

	privatev1 "github.com/osac-project/fulfillment-service/internal/api/osac/private/v1"
	"github.com/osac-project/fulfillment-service/internal/auth"
)

var _ = Describe("Builtin tenants", func() {
	var client privatev1.TenantsClient

	BeforeEach(func() {
		client = privatev1.NewTenantsClient(tool.InternalView().AdminConn())
	})

	DescribeTable(
		"Can be retrieved via the private API",
		func(ctx context.Context, id string) {
			response, err := client.Get(ctx, privatev1.TenantsGetRequest_builder{
				Id: id,
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			object := response.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).To(Equal(id))
			metadata := object.GetMetadata()
			Expect(metadata).ToNot(BeNil())
			Expect(metadata.GetName()).To(Equal(id))
			Expect(metadata.GetTenant()).To(Equal(id))
		},
		Entry("System", auth.SystemTenant),
		Entry("Shared", auth.SharedTenant),
	)

	It("Includes builtin tenants in the list", func(ctx context.Context) {
		response, err := client.List(ctx, privatev1.TenantsListRequest_builder{}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response).ToNot(BeNil())
		ids := make([]string, len(response.GetItems()))
		for i, item := range response.GetItems() {
			ids[i] = item.GetId()
		}
		Expect(ids).To(ContainElement(auth.SystemTenant))
		Expect(ids).To(ContainElement(auth.SharedTenant))
	})
})
