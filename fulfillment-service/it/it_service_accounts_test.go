/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0
*/
package it

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/osac-project/osac/fulfillment-service/internal/serviceaccounts"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("Service-account infrastructure", Serial, func() {
	It("manages tenant grants and credentials without changing an unrelated Keycloak client", func(ctx context.Context) {
		tenant, conn := createCatalogItemTenantAdminFixture(ctx)
		api := publicv1.NewServiceAccountsClient(conn)
		before := keycloakClient(ctx, "osac-controller")
		beforeSecret := keycloakClientSecret(ctx, "osac-controller")
		// OSAC-6366 owns this catalog entry; add a temporary fixture only when it is absent.
		privateRoles := privatev1.NewRolesClient(tool.InternalView().AdminConn())
		prerequisite, err := privateRoles.List(ctx, privatev1.RolesListRequest_builder{Filter: new(`this.metadata.name == "tenant-idp-manager"`)}.Build())
		Expect(err).ToNot(HaveOccurred())
		if len(prerequisite.GetItems()) == 0 {
			created, e := privateRoles.Create(ctx, privatev1.RolesCreateRequest_builder{Object: privatev1.Role_builder{
				Metadata: privatev1.Metadata_builder{Name: "tenant-idp-manager", Tenant: "shared"}.Build(),
				Spec:     privatev1.RoleSpec_builder{Title: "Tenant IdP manager"}.Build(),
			}.Build()}.Build())
			Expect(e).ToNot(HaveOccurred())
			DeferCleanup(func(ctx context.Context) {
				_, e := privateRoles.Delete(ctx, privatev1.RolesDeleteRequest_builder{Id: created.GetObject().GetId()}.Build())
				Expect(e).ToNot(HaveOccurred())
			})
		}
		request := serviceAccountRequest("", "robot")
		request.GetObject().SetStatus(publicv1.ServiceAccountStatus_builder{ClientId: "osac-controller"}.Build())
		response, err := api.Create(ctx, request)
		Expect(err).ToNot(HaveOccurred())
		account := response.GetObject()
		id := account.GetId()
		secret := response.GetClientSecret()
		DeferCleanup(func(ctx context.Context) { deleteServiceAccount(ctx, api, id) })
		secrets := publicv1.NewSecretsClient(conn)
		createdSecret, err := secrets.Create(ctx, publicv1.SecretsCreateRequest_builder{Object: publicv1.Secret_builder{
			Metadata: publicv1.Metadata_builder{Name: serviceaccounts.RegistrationSecretName(id)}.Build(),
			Data:     map[string][]byte{"value": []byte("must-not-overwrite-internal-credential")},
		}.Build()}.Build())
		if err == nil {
			DeferCleanup(func(ctx context.Context) {
				_, deleteErr := secrets.Delete(ctx, publicv1.SecretsDeleteRequest_builder{Id: createdSecret.GetObject().GetId()}.Build())
				Expect(deleteErr).ToNot(HaveOccurred())
			})
		}
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		Expect(secret).ToNot(BeEmpty())
		Expect(account.GetMetadata().GetTenant()).To(Equal(tenant))
		clientID := account.GetStatus().GetClientId()
		Expect(clientID).To(Equal("osac-sa-" + tenant + "-" + id))
		kc := keycloakClient(ctx, clientID)
		for _, field := range []string{"publicClient", "standardFlowEnabled", "implicitFlowEnabled", "directAccessGrantsEnabled", "fullScopeAllowed"} {
			Expect(kc[field]).To(BeFalse(), field)
		}
		Expect(kc["enabled"]).To(BeTrue())
		Expect(kc["serviceAccountsEnabled"]).To(BeTrue())
		Expect(kc["defaultClientScopes"]).To(ConsistOf("service_account"))
		Expect(kc["optionalClientScopes"]).To(BeEmpty())
		claims := serviceAccountClaims(ctx, clientID, secret)
		Expect(claims["osac_tenant"]).To(Equal(tenant))
		assertServiceAccountStoredWithoutSecret(ctx, id, secret)
		roles, err := publicv1.NewRolesClient(conn).List(ctx, &publicv1.RolesListRequest{})
		Expect(err).ToNot(HaveOccurred())
		catalog := map[string]string{}
		for _, role := range roles.GetItems() {
			catalog[role.GetMetadata().GetName()] = role.GetId()
		}
		for _, name := range []string{"tenant-admin", "tenant-idp-manager", "tenant-automation", "osac-csi-driver"} {
			Expect(catalog).To(HaveKey(name))
			updated, e := api.Update(ctx, publicv1.ServiceAccountsUpdateRequest_builder{Object: publicv1.ServiceAccount_builder{Id: id, Spec: publicv1.ServiceAccountSpec_builder{Roles: []*publicv1.RoleReference{publicv1.RoleReference_builder{Id: catalog[name]}.Build()}}.Build()}.Build(), UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.roles"}}}.Build())
			Expect(e).ToNot(HaveOccurred())
			Expect(updated.GetObject().GetSpec().GetRoles()[0].GetId()).To(Equal(catalog[name]))
			Expect(serviceAccountSQL(ctx, fmt.Sprintf(`select data->'spec'->'roles'->0->>'id' from service_accounts where data->'status'->>'client_id'='%s'`, clientID))).To(Equal(catalog[name]))
		}
		updated, err := api.Update(ctx, publicv1.ServiceAccountsUpdateRequest_builder{Object: publicv1.ServiceAccount_builder{Id: id, Metadata: publicv1.Metadata_builder{DisplayName: "Updated"}.Build(), Status: publicv1.ServiceAccountStatus_builder{ClientId: "forged"}.Build()}.Build()}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(updated.GetObject().GetSpec().GetRoles()).To(BeEmpty())
		Expect(updated.GetObject().GetStatus().GetClientId()).To(Equal(clientID))
		for _, object := range []proto.Message{updated.GetObject(), account} {
			data, e := protojson.Marshal(object)
			Expect(e).ToNot(HaveOccurred())
			Expect(bytes.Contains(data, []byte(secret))).To(BeFalse())
		}
		got, err := api.Get(ctx, publicv1.ServiceAccountsGetRequest_builder{Id: id}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(got.GetObject().GetMetadata().GetDisplayName()).To(Equal("Updated"))
		_, err = api.Update(ctx, publicv1.ServiceAccountsUpdateRequest_builder{Object: publicv1.ServiceAccount_builder{Id: id, Status: publicv1.ServiceAccountStatus_builder{ClientId: "forged"}.Build()}.Build(), UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"status.client_id"}}}.Build())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		for _, name := range []string{"tenant-automation", "osac-csi-driver"} {
			_, err = publicv1.NewRoleBindingsClient(conn).Create(ctx, publicv1.RoleBindingsCreateRequest_builder{Object: publicv1.RoleBinding_builder{Metadata: publicv1.Metadata_builder{Name: "machine-role-" + uuid.New()[24:]}.Build(), Spec: publicv1.RoleBindingSpec_builder{Role: publicv1.RoleReference_builder{Id: catalog[name]}.Build()}.Build()}.Build()}.Build())
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		}
		deleteServiceAccount(ctx, api, id)
		code, _, err := tool.KeycloakAdminRequest(ctx, http.MethodGet, "/clients/"+kc["id"].(string), nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(code).To(Equal(http.StatusNotFound))
		Expect(serviceAccountSQL(ctx, fmt.Sprintf(`select count(*) from archived_service_accounts where id='%s'`, id))).To(Equal("1"))
		Expect(keycloakClient(ctx, "osac-controller")).To(Equal(before))
		afterSecret := keycloakClientSecret(ctx, "osac-controller")
		Expect(bytes.Equal([]byte(afterSecret), []byte(beforeSecret))).To(BeTrue(), "unrelated Keycloak client secret changed")
	})

	It("filters lists and conceals foreign accounts from tenant administrators", func(ctx context.Context) {
		tenantA, connA := createCatalogItemTenantAdminFixture(ctx)
		tenantB, connB := createCatalogItemTenantAdminFixture(ctx)
		a, b := publicv1.NewServiceAccountsClient(connA), publicv1.NewServiceAccountsClient(connB)
		var ids []string
		for _, client := range []publicv1.ServiceAccountsClient{a, b} {
			response, err := client.Create(ctx, serviceAccountRequest("", "same-name"))
			Expect(err).ToNot(HaveOccurred())
			id := response.GetObject().GetId()
			ids = append(ids, id)
			DeferCleanup(func(ctx context.Context) { deleteServiceAccount(ctx, client, id) })
		}
		for i, client := range []publicv1.ServiceAccountsClient{a, b} {
			list, err := client.List(ctx, publicv1.ServiceAccountsListRequest_builder{Limit: new(int32(1)), Filter: new(`this.metadata.name == "same-name"`)}.Build())
			Expect(err).ToNot(HaveOccurred())
			Expect(list.GetTotal()).To(Equal(int32(1)))
			Expect(list.GetItems()).To(HaveLen(1))
			Expect(list.GetItems()[0].GetId()).To(Equal(ids[i]))
			foreign := ids[1-i]
			_, err = client.Get(ctx, publicv1.ServiceAccountsGetRequest_builder{Id: foreign}.Build())
			Expect(status.Code(err)).To(Equal(codes.NotFound))
			_, err = client.Update(ctx, publicv1.ServiceAccountsUpdateRequest_builder{Object: publicv1.ServiceAccount_builder{Id: foreign}.Build(), UpdateMask: &fieldmaskpb.FieldMask{}}.Build())
			Expect(status.Code(err)).To(Equal(codes.NotFound))
			_, err = client.Delete(ctx, publicv1.ServiceAccountsDeleteRequest_builder{Id: foreign}.Build())
			Expect(status.Code(err)).To(Equal(codes.NotFound))
		}
		Expect(tenantA).ToNot(Equal(tenantB))
		user := publicv1.NewServiceAccountsClient(tool.ExternalView().UserConn())
		for _, id := range []string{ids[0], uuid.New()} {
			_, err := user.Get(ctx, publicv1.ServiceAccountsGetRequest_builder{Id: id}.Build())
			Expect(status.Code(err)).To(Equal(codes.PermissionDenied))
		}
	})

	It("returns the REST envelope once and issues signed claims for a maximum-length tenant", func(ctx context.Context) {
		tenant := strings.Repeat("a", 50) + "-" + uuid.New()[24:]
		Expect(tenant).To(HaveLen(63))
		tenants := privatev1.NewTenantsClient(tool.InternalView().AdminConn())
		created, err := tenants.Create(ctx, privatev1.TenantsCreateRequest_builder{Object: privatev1.Tenant_builder{Metadata: privatev1.Metadata_builder{Name: tenant}.Build()}.Build()}.Build())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func(ctx context.Context) {
			deleteTenantUsers(ctx, tenant)
			deleteTenant(ctx, tenants, privatev1.NewProjectsClient(tool.InternalView().AdminConn()), created.GetObject().GetId(), tenant)
		})
		waitForTenantSynced(ctx, tenants, created.GetObject().GetId())
		api := publicv1.NewServiceAccountsClient(tool.ExternalView().AdminConn())
		request := serviceAccountRequest(tenant, strings.Repeat("r", 63))
		dry, err := api.Create(metadata.NewOutgoingContext(ctx, metadata.Pairs("x-dry-run", "true")), request)
		Expect(err).ToNot(HaveOccurred())
		Expect(dry.GetClientSecret()).To(BeEmpty())
		Expect(dry.GetObject().GetId()).To(BeEmpty())
		payload, err := protojson.Marshal(request.GetObject())
		Expect(err).ToNot(HaveOccurred())
		dryRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, "/api/fulfillment/v1/service_accounts", bytes.NewReader(payload))
		Expect(err).ToNot(HaveOccurred())
		dryRequest.Header.Set("Content-Type", "application/json")
		dryRequest.Header.Set("X-Dry-Run", "true")
		dryResult, err := tool.ExternalView().AdminClient().Do(dryRequest)
		Expect(err).ToNot(HaveOccurred())
		dryBody, err := io.ReadAll(dryResult.Body)
		Expect(dryResult.Body.Close()).To(Succeed())
		Expect(err).ToNot(HaveOccurred())
		Expect(dryResult.StatusCode).To(Equal(http.StatusOK))
		dryEnvelope := &publicv1.ServiceAccountsCreateResponse{}
		Expect(protojson.Unmarshal(dryBody, dryEnvelope)).To(Succeed())
		Expect(dryEnvelope.GetObject().GetId()).To(BeEmpty())
		Expect(dryEnvelope.GetClientSecret()).To(BeEmpty())
		Expect(keycloakServiceAccountClientIDs(ctx, tenant)).To(BeEmpty(), "dry-run must not create a Keycloak client")
		httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, "/api/fulfillment/v1/service_accounts", bytes.NewReader(payload))
		Expect(err).ToNot(HaveOccurred())
		httpRequest.Header.Set("Content-Type", "application/json")
		result, err := tool.ExternalView().AdminClient().Do(httpRequest)
		Expect(err).ToNot(HaveOccurred())
		defer result.Body.Close()
		body, err := io.ReadAll(result.Body)
		Expect(err).ToNot(HaveOccurred())
		Expect(result.StatusCode).To(Equal(http.StatusOK))
		response := &publicv1.ServiceAccountsCreateResponse{}
		Expect(protojson.Unmarshal(body, response)).To(Succeed())
		id := response.GetObject().GetId()
		DeferCleanup(func(ctx context.Context) { deleteServiceAccount(ctx, api, id) })
		secret := response.GetClientSecret()
		Expect(secret).ToNot(BeEmpty())
		clientID := response.GetObject().GetStatus().GetClientId()
		Expect(clientID).To(HaveLen(108))
		Expect(serviceAccountClaims(ctx, clientID, secret)["osac_tenant"]).To(Equal(tenant))
		Expect(bytes.Contains(body, []byte("keycloakClientUuid"))).To(BeFalse())
		read, err := tool.ExternalView().AdminClient().Get("/api/fulfillment/v1/service_accounts/" + id)
		Expect(err).ToNot(HaveOccurred())
		defer read.Body.Close()
		body, err = io.ReadAll(read.Body)
		Expect(err).ToNot(HaveOccurred())
		Expect(read.StatusCode).To(Equal(http.StatusOK))
		Expect(bytes.Contains(body, []byte(secret))).To(BeFalse())
		Expect(bytes.Contains(body, []byte("clientSecret"))).To(BeFalse())
		assertServiceAccountStoredWithoutSecret(ctx, id, secret)
	})

	It("reserves a deleting name until cleanup resumes after controller restart", func(ctx context.Context) {
		tenant, conn := createCatalogItemTenantAdminFixture(ctx)
		api := publicv1.NewServiceAccountsClient(conn)
		kube := tool.KubeClient()
		key := crclient.ObjectKey{Namespace: "osac", Name: "fulfillment-controller"}
		deployment := &appsv1.Deployment{}
		Expect(kube.Get(ctx, key, deployment)).To(Succeed())
		replicas := *deployment.Spec.Replicas
		scale := func(ctx context.Context, n int32) {
			current := &appsv1.Deployment{}
			Expect(kube.Get(ctx, key, current)).To(Succeed())
			old := current.DeepCopy()
			current.Spec.Replicas = new(n)
			Expect(kube.Patch(ctx, current, crclient.MergeFrom(old))).To(Succeed())
			Eventually(ctx, func(g Gomega) {
				d := &appsv1.Deployment{}
				g.Expect(kube.Get(ctx, key, d)).To(Succeed())
				g.Expect(d.Status.ReadyReplicas).To(Equal(n))
				if n == 0 {
					pods := &corev1.PodList{}
					g.Expect(kube.List(ctx, pods, crclient.InNamespace("osac"), crclient.MatchingLabels{"app": "fulfillment-controller"})).To(Succeed())
					g.Expect(pods.Items).To(BeEmpty())
				}
			}, 2*time.Minute, 2*time.Second).Should(Succeed())
		}
		var ids []string
		DeferCleanup(func(ctx context.Context) {
			scale(ctx, replicas)
			for _, id := range ids {
				deleteServiceAccount(ctx, api, id)
			}
		})
		scale(ctx, 0)
		response, err := api.Create(ctx, serviceAccountRequest("", "restart"))
		Expect(err).ToNot(HaveOccurred())
		id := response.GetObject().GetId()
		ids = append(ids, id)
		_, err = api.Delete(ctx, publicv1.ServiceAccountsDeleteRequest_builder{Id: id}.Build())
		Expect(err).ToNot(HaveOccurred())
		pending, err := privatev1.NewServiceAccountsClient(tool.InternalView().AdminConn()).Get(ctx, privatev1.ServiceAccountsGetRequest_builder{Id: id}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(pending.GetObject().GetMetadata().HasDeletionTimestamp()).To(BeTrue())
		Expect(pending.GetObject().GetMetadata().GetFinalizers()).To(ContainElement("fulfillment-controller"))
		_, err = api.Create(ctx, serviceAccountRequest("", "restart"))
		Expect(status.Code(err)).To(Equal(codes.AlreadyExists))
		Expect(keycloakClient(ctx, response.GetObject().GetStatus().GetClientId())["enabled"]).To(BeTrue())
		scale(ctx, replicas)
		deleteServiceAccount(ctx, api, id)
		next, err := api.Create(ctx, serviceAccountRequest(tenant, "restart"))
		Expect(err).ToNot(HaveOccurred())
		ids = append(ids, next.GetObject().GetId())
		Expect(next.GetObject().GetStatus().GetClientId()).ToNot(Equal(response.GetObject().GetStatus().GetClientId()))
	})
})
