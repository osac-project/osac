/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package servers

import (
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	"github.com/osac-project/osac/fulfillment-service/internal/collections"
	"github.com/osac-project/osac/fulfillment-service/internal/database/dao"
	"github.com/osac-project/osac/fulfillment-service/internal/idp"
	"github.com/osac-project/osac/fulfillment-service/internal/serviceaccounts"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Service account management", func() {
	var public *ServiceAccountsServer
	var private *PrivateServiceAccountsServer
	var stored *privatev1.ServiceAccount
	var tenantCtx context.Context
	var roles map[string]string
	BeforeEach(func() {
		tenantCtx = auth.ContextWithSubject(ctx, &auth.Subject{User: "admin", Tenants: collections.NewSet(testTenant)})
		tenancyLogic, err := auth.NewDefaultTenancyLogic().SetLogger(logger).Build()
		Expect(err).ToNot(HaveOccurred())
		idpClient := idp.NewMockClientInterface(ctrl)
		secretStore := newMemorySecretStore()
		public, err = NewServiceAccountsServer().SetLogger(logger).SetAttributionLogic(attribution).
			SetTenancyLogic(tenancyLogic).SetIdpClient(idpClient).SetSecretStore(secretStore).Build()
		Expect(err).ToNot(HaveOccurred())
		private, err = NewPrivateServiceAccountsServer().SetLogger(logger).SetAttributionLogic(attribution).
			SetTenancyLogic(tenancyLogic).SetIdpClient(idpClient).SetSecretStore(secretStore).Build()
		Expect(err).ToNot(HaveOccurred())
		roleDAO, err := dao.NewGenericDAO[*privatev1.Role]().SetLogger(logger).SetTenancyLogic(tenancy).Build()
		Expect(err).ToNot(HaveOccurred())
		// Model the IdP-manager catalog prerequisite while its migration is pending.
		prerequisite, err := roleDAO.List().SetFilter(`this.metadata.name == "tenant-idp-manager"`).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		if len(prerequisite.GetItems()) == 0 {
			_, err = roleDAO.Create().SetObject(privatev1.Role_builder{Id: "f7674e7a-8f50-437f-80fa-40fb8d9d327a",
				Metadata: privatev1.Metadata_builder{Name: "tenant-idp-manager", Tenant: auth.SharedTenant}.Build(),
				Status:   privatev1.RoleStatus_builder{State: privatev1.RoleState_ROLE_STATE_READY}.Build(),
			}.Build()).Do(ctx)
			Expect(err).ToNot(HaveOccurred())
		}
		catalog, err := roleDAO.List().Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		roles = map[string]string{}
		for _, role := range catalog.GetItems() {
			roles[role.GetMetadata().GetName()] = role.GetId()
		}
		accountDAO, err := dao.NewGenericDAO[*privatev1.ServiceAccount]().
			SetLogger(logger).
			SetTenancyLogic(tenancy).
			SetPersistNonColumnMetadataFields().
			Build()
		Expect(err).ToNot(HaveOccurred())
		created, err := accountDAO.Create().SetObject(privatev1.ServiceAccount_builder{
			Metadata: privatev1.Metadata_builder{Name: "robot", Tenant: testTenant, Creator: "admin",
				Finalizers:  []string{"fulfillment-controller", "other"},
				Annotations: map[string]string{"osac.openshift.io/tenant": testTenant},
				DisplayName: "Old", Description: "Old description", Labels: map[string]string{"old": "value"},
			}.Build(),
			Spec: privatev1.ServiceAccountSpec_builder{Roles: []*privatev1.RoleReference{
				privatev1.RoleReference_builder{Id: roles["tenant-admin"]}.Build(),
			}}.Build(),
		}.Build()).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		stored = created.GetObject()
		stored.SetStatus(privatev1.ServiceAccountStatus_builder{
			ClientId:           serviceaccounts.ClientID(testTenant, stored.GetId()),
			KeycloakClientUuid: "00000000-0000-0000-0000-000000000000",
		}.Build())
		updated, err := accountDAO.Update().SetObject(stored).Do(ctx)
		Expect(err).ToNot(HaveOccurred())
		stored = updated.GetObject()
	})
	It("returns public status and a tenant-filtered list", func() {
		got, err := public.Get(tenantCtx, publicv1.ServiceAccountsGetRequest_builder{Id: stored.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(got.GetObject().GetMetadata().GetDisplayName()).To(Equal("Old"))
		Expect(got.GetObject().GetMetadata().GetDescription()).To(Equal("Old description"))
		Expect(got.GetObject().GetStatus().GetClientId()).To(Equal(stored.GetStatus().GetClientId()))
		list, err := public.List(tenantCtx, publicv1.ServiceAccountsListRequest_builder{Limit: proto.Int32(1)}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(list.GetItems()).To(HaveLen(1))
		Expect(list.GetTotal()).To(Equal(int32(1)))
		foreign := auth.ContextWithSubject(ctx, &auth.Subject{User: "other", Tenants: collections.NewSet("other-tenant")})
		list, err = public.List(foreign, &publicv1.ServiceAccountsListRequest{})
		Expect(err).ToNot(HaveOccurred())
		Expect(list.GetTotal()).To(BeZero())
		Expect(list.GetItems()).To(BeEmpty())
	})
	DescribeTable("conceals foreign IDs", func(operation string) {
		foreign := auth.ContextWithSubject(ctx, &auth.Subject{User: "other", Tenants: collections.NewSet("other-tenant")})
		var err error
		switch operation {
		case "Get":
			_, err = public.Get(foreign, publicv1.ServiceAccountsGetRequest_builder{Id: stored.GetId()}.Build())
		case "Update":
			_, err = public.Update(foreign, publicv1.ServiceAccountsUpdateRequest_builder{
				Object: publicv1.ServiceAccount_builder{Id: stored.GetId()}.Build(), UpdateMask: &fieldmaskpb.FieldMask{},
			}.Build())
		case "Delete":
			_, err = public.Delete(foreign, publicv1.ServiceAccountsDeleteRequest_builder{Id: stored.GetId()}.Build())
		}
		Expect(status.Code(err)).To(Equal(codes.NotFound))
	}, Entry("Get", "Get"), Entry("Update", "Update"), Entry("Delete", "Delete"))
	It("merges only masked fields and preserves controller-owned state", func() {
		response, err := public.Update(tenantCtx, publicv1.ServiceAccountsUpdateRequest_builder{
			Object: publicv1.ServiceAccount_builder{Id: stored.GetId(),
				Metadata: publicv1.Metadata_builder{DisplayName: "Updated"}.Build(),
				Spec: publicv1.ServiceAccountSpec_builder{Roles: []*publicv1.RoleReference{
					publicv1.RoleReference_builder{Id: "unknown"}.Build(),
				}}.Build(), Status: publicv1.ServiceAccountStatus_builder{ClientId: "forged"}.Build(),
			}.Build(), UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.display_name"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetObject().GetMetadata().GetDisplayName()).To(Equal("Updated"))
		Expect(response.GetObject().GetSpec().GetRoles()[0].GetId()).To(Equal(roles["tenant-admin"]))
		got, err := private.Get(tenantCtx, privatev1.ServiceAccountsGetRequest_builder{Id: stored.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(proto.Equal(got.GetObject().GetStatus(), stored.GetStatus())).To(BeTrue())
		Expect(got.GetObject().GetMetadata().GetFinalizers()).To(Equal(stored.GetMetadata().GetFinalizers()))
	})
	DescribeTable("rejects server-owned update masks", func(path string) {
		_, err := public.Update(tenantCtx, publicv1.ServiceAccountsUpdateRequest_builder{
			Object:     publicv1.ServiceAccount_builder{Id: stored.GetId()}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{path}},
		}.Build())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	}, Entry("status", "status"), Entry("client ID", "status.client_id"), Entry("finalizers", "metadata.finalizers"),
		Entry("tenant", "metadata.tenant"), Entry("project", "metadata.project"), Entry("annotations", "metadata.annotations"))
	It("replaces mutable values with a nil mask but preserves identity and status", func() {
		response, err := public.Update(tenantCtx, publicv1.ServiceAccountsUpdateRequest_builder{
			Object: publicv1.ServiceAccount_builder{Id: stored.GetId(),
				Metadata: publicv1.Metadata_builder{DisplayName: "Replacement"}.Build(),
				Status:   publicv1.ServiceAccountStatus_builder{ClientId: "forged"}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		object := response.GetObject()
		Expect(object.GetMetadata().GetName()).To(Equal("robot"))
		Expect(object.GetMetadata().GetTenant()).To(Equal(testTenant))
		Expect(object.GetMetadata().GetDescription()).To(BeEmpty())
		Expect(object.GetMetadata().GetLabels()).To(BeEmpty())
		Expect(object.GetSpec().GetRoles()).To(BeEmpty())
		Expect(object.GetStatus().GetClientId()).To(Equal(stored.GetStatus().GetClientId()))
	})
	It("treats an explicit empty mask as a no-op", func() {
		response, err := public.Update(tenantCtx, publicv1.ServiceAccountsUpdateRequest_builder{
			Object: publicv1.ServiceAccount_builder{Id: stored.GetId()}.Build(), UpdateMask: &fieldmaskpb.FieldMask{},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetObject().GetMetadata().GetVersion()).To(Equal(stored.GetMetadata().GetVersion()))
	})
	It("rejects a stale version before changing stored grants", func() {
		_, err := public.Update(tenantCtx, publicv1.ServiceAccountsUpdateRequest_builder{
			Object:     publicv1.ServiceAccount_builder{Id: stored.GetId(), Metadata: publicv1.Metadata_builder{Version: -1}.Build()}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.roles"}}, Lock: true,
		}.Build())
		Expect(status.Code(err)).To(Equal(codes.Aborted))
	})
	DescribeTable("validates supported grants on the merged object", func(name string, allowed bool) {
		id := roles[name]
		if id == "" {
			id = "unknown"
		}
		response, err := public.Update(tenantCtx, publicv1.ServiceAccountsUpdateRequest_builder{
			Object: publicv1.ServiceAccount_builder{Id: stored.GetId(), Spec: publicv1.ServiceAccountSpec_builder{
				Roles: []*publicv1.RoleReference{publicv1.RoleReference_builder{Id: id}.Build()},
			}.Build()}.Build(), UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.roles"}},
		}.Build())
		if allowed {
			Expect(err).ToNot(HaveOccurred())
			Expect(response.GetObject().GetSpec().GetRoles()[0].GetId()).To(Equal(id))
		} else {
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		}
	}, Entry("automation", "tenant-automation", true), Entry("CSI", "osac-csi-driver", true),
		Entry("admin", "tenant-admin", true), Entry("IdP manager", "tenant-idp-manager", true),
		Entry("legacy user", "tenant-user", false), Entry("unknown", "unknown", false))
	It("bounds role validation to the assignable role count", func() {
		refs := make([]*privatev1.RoleReference, maxAssignableServiceAccountRoles+1)
		for i := range refs {
			refs[i] = &privatev1.RoleReference{}
		}
		private.roles = nil
		account := privatev1.ServiceAccount_builder{Spec: privatev1.ServiceAccountSpec_builder{Roles: refs}.Build()}.Build()
		Expect(status.Code(private.validateRoles(context.Background(), account))).To(Equal(codes.InvalidArgument))
	})
	It("rejects a name change while allowing the same name", func() {
		for _, name := range []string{"robot", "changed"} {
			_, err := public.Update(tenantCtx, publicv1.ServiceAccountsUpdateRequest_builder{
				Object:     publicv1.ServiceAccount_builder{Id: stored.GetId(), Metadata: publicv1.Metadata_builder{Name: name}.Build()}.Build(),
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.name"}},
			}.Build())
			if name == "robot" {
				Expect(err).ToNot(HaveOccurred())
			} else {
				Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
			}
		}
	})
	It("allows trusted finalizer cleanup after an assigned catalog role is removed", func() {
		_, err := private.Delete(tenantCtx, privatev1.ServiceAccountsDeleteRequest_builder{Id: stored.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		_, err = suiteTx.Exec(ctx, `delete from roles where id=$1`, roles["tenant-admin"])
		Expect(err).ToNot(HaveOccurred())
		got, err := private.Get(tenantCtx, privatev1.ServiceAccountsGetRequest_builder{Id: stored.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		got.GetObject().GetMetadata().SetFinalizers(nil)
		_, err = private.Update(tenantCtx, privatev1.ServiceAccountsUpdateRequest_builder{
			Object: got.GetObject(), Lock: true, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"metadata.finalizers"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		var live, archived int
		Expect(suiteTx.QueryRow(ctx, `select count(*) from service_accounts where id=$1`, stored.GetId()).Scan(&live)).To(Succeed())
		Expect(suiteTx.QueryRow(ctx, `select count(*) from archived_service_accounts where id=$1`, stored.GetId()).Scan(&archived)).To(Succeed())
		Expect(live).To(BeZero())
		Expect(archived).To(Equal(1))
	})

	It("marks deletion without synchronous Keycloak calls and retains cleanup finalizers", func() {
		_, err := public.Delete(tenantCtx, publicv1.ServiceAccountsDeleteRequest_builder{Id: stored.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		response, err := private.Get(tenantCtx, privatev1.ServiceAccountsGetRequest_builder{Id: stored.GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(response.GetObject().GetMetadata().GetDeletionTimestamp()).ToNot(BeNil())
		Expect(response.GetObject().GetMetadata().GetFinalizers()).To(ConsistOf("fulfillment-controller", "other"))
	})
	It("leaves the declared rotation action unimplemented without looking up the ID", func() {
		for _, id := range []string{stored.GetId(), "missing"} {
			_, err := public.RegenerateSecret(tenantCtx, publicv1.ServiceAccountsRegenerateSecretRequest_builder{Id: id}.Build())
			Expect(status.Code(err)).To(Equal(codes.Unimplemented), fmt.Sprintf("ID %s", id))
		}
	})
})
