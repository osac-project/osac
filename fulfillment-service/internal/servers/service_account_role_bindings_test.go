/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
You may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package servers

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/collections"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Role binding role scopes", func() {
	var public *RoleBindingsServer
	var roles *PrivateRolesServer
	var roleID func(string) string
	BeforeEach(func() {
		var err error
		public, err = NewRoleBindingsServer().SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		roles, err = NewPrivateRolesServer().SetLogger(logger).SetAttributionLogic(attribution).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		roleID = func(name string) string {
			response, err := roles.List(ctx, privatev1.RolesListRequest_builder{Filter: new(fmt.Sprintf("this.metadata.name == %q", name))}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetItems()).To(HaveLen(1))
			return response.GetItems()[0].GetId()
		}
	})

	It("allows a role to be bound to human users", func() {
		response, err := public.Create(ctx, publicv1.RoleBindingsCreateRequest_builder{Object: publicv1.RoleBinding_builder{
			Metadata: publicv1.Metadata_builder{Name: "automation-user"}.Build(),
			Spec: publicv1.RoleBindingSpec_builder{
				Role:  publicv1.RoleReference_builder{Id: roleID("tenant-automation")}.Build(),
				Users: []*publicv1.UserReference{publicv1.UserReference_builder{Id: "human-user"}.Build()},
			}.Build(),
		}.Build()}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetObject().GetSpec().GetRole().GetId()).To(Equal(roleID("tenant-automation")))
	})

	It("hides the system CSI role from tenant users", func() {
		systemTenancy, err := auth.NewDefaultTenancyLogic().SetLogger(logger).Build()
		Expect(err).ToNot(HaveOccurred())
		scopedRoles, err := NewPrivateRolesServer().SetLogger(logger).SetAttributionLogic(attribution).
			SetTenancyLogic(systemTenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		tenantCtx := auth.ContextWithSubject(ctx, &auth.Subject{
			User: "tenant-user", Tenants: collections.NewSet(testTenant),
		})
		response, err := scopedRoles.List(tenantCtx, privatev1.RolesListRequest_builder{
			Filter: new(`this.metadata.name == "osac-csi-driver"`),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetItems()).To(BeEmpty())
	})

	It("lets cloud administrators update and bind the system CSI role", func() {
		adminCtx := auth.ContextWithSubject(ctx, &auth.Subject{User: "cloud-admin", Tenants: auth.AllTenants})
		csiRoleID := roleID("osac-csi-driver")
		updated, err := roles.Update(adminCtx, privatev1.RolesUpdateRequest_builder{
			Object: privatev1.Role_builder{
				Id: csiRoleID, Spec: privatev1.RoleSpec_builder{Title: "Updated by cloud admin"}.Build(),
			}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.title"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(updated.GetObject().GetSpec().GetTitle()).To(Equal("Updated by cloud admin"))

		binding, err := public.Create(adminCtx, publicv1.RoleBindingsCreateRequest_builder{
			Object: publicv1.RoleBinding_builder{
				Metadata: publicv1.Metadata_builder{Name: "system-csi-binding", Tenant: auth.SystemTenant}.Build(),
				Spec: publicv1.RoleBindingSpec_builder{
					Role:  publicv1.RoleReference_builder{Id: csiRoleID}.Build(),
					Users: []*publicv1.UserReference{publicv1.UserReference_builder{Id: "csi-user"}.Build()},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(binding.GetObject().GetMetadata().GetTenant()).To(Equal(auth.SystemTenant))
	})
})
