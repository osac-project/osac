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
	. "github.com/onsi/gomega"

	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Roles", func() {
	var (
		ctx           context.Context
		privateClient privatev1.RolesClient
		publicClient  publicv1.RolesClient
	)

	BeforeEach(func() {
		ctx = context.Background()
		privateClient = privatev1.NewRolesClient(tool.InternalView().AdminConn())
		publicClient = publicv1.NewRolesClient(tool.ExternalView().UserConn())
	})

	Describe("Private API", func() {
		It("Lists only bindable built-in roles", func() {
			listResponse, err := privateClient.List(ctx, privatev1.RolesListRequest_builder{
				Filter: new(`
					this.id in [
						'019dd9f6-36cb-7824-ac1c-6455d8a7a9b5',
						'019dd9f6-36cc-7a35-bfc5-242833f11c2f',
						'019dd9f6-36cd-78bf-9fa2-1be4464108b0',
						'019dd9f6-36ce-78fc-96d8-b87cf58bf14c',
						'019dd9f6-36cf-77bc-8041-c6a6a415c185',
						'019dd9f6-d573-beaa-41c5-96965e594cc6',
					]
				`),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(listResponse).ToNot(BeNil())
			items := listResponse.GetItems()
			Expect(items).To(HaveLen(5))

			names := make([]string, len(items))
			for i, item := range items {
				names[i] = item.GetMetadata().GetName()
			}
			Expect(names).To(ContainElements(
				"cloud-provider-admin",
				"tenant-admin",
				"tenant-idp-manager",
				"tenant-reader",
				"cloud-provider-reader",
			))
		})

		It("Can get a specific built-in role", func() {
			listResponse, err := privateClient.List(ctx, privatev1.RolesListRequest_builder{
				Filter: new("this.metadata.name == 'tenant-admin'"),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(listResponse).ToNot(BeNil())
			items := listResponse.GetItems()
			Expect(items).To(HaveLen(1))

			role := items[0]
			getResponse, err := privateClient.Get(ctx, privatev1.RolesGetRequest_builder{
				Id: role.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse).ToNot(BeNil())
			object := getResponse.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).To(Equal(role.GetId()))
			Expect(object.GetMetadata().GetName()).To(Equal("tenant-admin"))
			Expect(object.GetSpec().GetTitle()).To(Equal("Tenant administrator"))
			Expect(object.GetSpec().GetDescription()).ToNot(BeEmpty())
			Expect(object.GetStatus().GetState()).To(Equal(privatev1.RoleState_ROLE_STATE_READY))
		})
	})

	Describe("Public API", func() {
		It("Lists only bindable built-in roles", func() {
			listResponse, err := publicClient.List(ctx, publicv1.RolesListRequest_builder{
				Filter: new(`
					this.metadata.name in [
						'cloud-provider-admin',
						'cloud-provider-reader',
						'tenant-admin',
						'tenant-reader',
						'tenant-idp-manager',
					]
				`),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(listResponse).ToNot(BeNil())
			items := listResponse.GetItems()
			Expect(items).To(HaveLen(3))

			names := make([]string, len(items))
			for i, item := range items {
				names[i] = item.GetMetadata().GetName()
			}
			Expect(names).To(ContainElements(
				"tenant-admin",
				"tenant-reader",
				"tenant-idp-manager",
			))
		})

		It("Can get a specific built-in role", func() {
			listResponse, err := publicClient.List(ctx, publicv1.RolesListRequest_builder{
				Filter: new("this.metadata.name == 'tenant-admin'"),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(listResponse).ToNot(BeNil())
			items := listResponse.GetItems()
			Expect(items).To(HaveLen(1))

			role := items[0]
			getResponse, err := publicClient.Get(ctx, publicv1.RolesGetRequest_builder{
				Id: role.GetId(),
			}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(getResponse).ToNot(BeNil())
			object := getResponse.GetObject()
			Expect(object).ToNot(BeNil())
			Expect(object.GetId()).To(Equal(role.GetId()))
			Expect(object.GetMetadata().GetName()).To(Equal("tenant-admin"))
			Expect(object.GetSpec().GetTitle()).To(Equal("Tenant administrator"))
			Expect(object.GetSpec().GetDescription()).ToNot(BeEmpty())
			Expect(object.GetStatus().GetState()).To(Equal(publicv1.RoleState_ROLE_STATE_READY))
		})
	})
})
