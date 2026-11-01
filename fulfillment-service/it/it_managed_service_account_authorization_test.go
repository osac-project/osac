/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0
*/
package it

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Managed service-account authorization", Serial, func() {
	It("uses current RoleBinding grants and revokes access immediately", func(ctx context.Context) {
		tenant, adminConn := createCatalogItemTenantAdminFixture(ctx)
		accounts := publicv1.NewServiceAccountsClient(adminConn)
		created, err := accounts.Create(ctx, serviceAccountRequest("", "authz-"+uuid.New()[24:]))
		Expect(err).ToNot(HaveOccurred())
		accountID := created.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) { deleteServiceAccount(ctx, accounts, accountID) })

		var account *publicv1.ServiceAccount
		Eventually(ctx, func(g Gomega) {
			current, getErr := accounts.Get(ctx, publicv1.ServiceAccountsGetRequest_builder{Id: accountID}.Build())
			g.Expect(getErr).ToNot(HaveOccurred())
			account = current.GetObject()
			g.Expect(account.GetStatus().GetClientSecret().GetId()).ToNot(BeEmpty())
		}, 2*time.Minute, time.Second).Should(Succeed())

		rolesResponse, err := publicv1.NewRolesClient(adminConn).List(ctx, &publicv1.RolesListRequest{})
		Expect(err).ToNot(HaveOccurred())
		roles := make(map[string]string, len(rolesResponse.GetItems()))
		for _, role := range rolesResponse.GetItems() {
			roles[role.GetMetadata().GetName()] = role.GetId()
		}
		Expect(roles).To(HaveKey("tenant-admin"))
		Expect(roles).To(HaveKey("tenant-automation"))

		secret, err := publicv1.NewSecretsClient(adminConn).Get(ctx,
			publicv1.SecretsGetRequest_builder{Id: account.GetStatus().GetClientSecret().GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		clientID := string(secret.GetObject().GetData()["client_id"])
		tokenSource, err := serviceAccountTokenSource(ctx, clientID, string(secret.GetObject().GetData()["client_secret"]))
		Expect(err).ToNot(HaveOccurred())
		serviceAccountConn, err := tool.makeGrpcConn(externalServiceAddr, tokenSource)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(serviceAccountConn.Close()).To(Succeed()) })

		clusters := publicv1.NewClustersClient(serviceAccountConn)
		_, err = clusters.List(ctx, &publicv1.ClustersListRequest{})
		Expect(status.Code(err)).To(Equal(codes.PermissionDenied), "a managed account without a RoleBinding must be denied")
		_, err = publicv1.NewUsersClient(serviceAccountConn).List(ctx, &publicv1.UsersListRequest{})
		Expect(status.Code(err)).To(Equal(codes.PermissionDenied), "a managed account without a RoleBinding must be denied")

		roleBindings := publicv1.NewRoleBindingsClient(adminConn)
		binding, err := roleBindings.Create(ctx, publicv1.RoleBindingsCreateRequest_builder{
			Object: publicv1.RoleBinding_builder{
				Metadata: publicv1.Metadata_builder{Name: "authz-" + uuid.New()[24:]}.Build(),
				Spec: publicv1.RoleBindingSpec_builder{
					Role: publicv1.RoleReference_builder{Id: roles["tenant-automation"]}.Build(),
					ServiceAccounts: []*publicv1.ServiceAccountReference{
						publicv1.ServiceAccountReference_builder{Id: accountID}.Build(),
					},
				}.Build(),
			}.Build(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		bindingID := binding.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) {
			_, deleteErr := roleBindings.Delete(ctx, publicv1.RoleBindingsDeleteRequest_builder{Id: bindingID}.Build())
			Expect(deleteErr).ToNot(HaveOccurred())
		})

		_, err = clusters.List(ctx, &publicv1.ClustersListRequest{})
		Expect(err).ToNot(HaveOccurred(), "tenant-automation grant should allow cluster listing in its tenant")
		_, err = publicv1.NewUsersClient(serviceAccountConn).List(ctx, &publicv1.UsersListRequest{})
		Expect(status.Code(err)).To(Equal(codes.PermissionDenied), "tenant-automation must not grant tenant-admin methods")
		Expect(account.GetMetadata().GetTenant()).To(Equal(tenant))

		_, err = roleBindings.Update(ctx, publicv1.RoleBindingsUpdateRequest_builder{
			Object: publicv1.RoleBinding_builder{Id: bindingID, Spec: publicv1.RoleBindingSpec_builder{
				Role: publicv1.RoleReference_builder{Id: roles["tenant-automation"]}.Build(),
			}.Build()}.Build(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.service_accounts"}},
		}.Build())
		Expect(err).ToNot(HaveOccurred())

		_, err = clusters.List(ctx, &publicv1.ClustersListRequest{})
		Expect(status.Code(err)).To(Equal(codes.PermissionDenied), "removing the current RoleBinding grant should take effect immediately")
	})
})
