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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

var _ = Describe("CLI service-account lifecycle", Label("cli", "service-accounts"), Serial, func() {
	It("creates, reads, updates, grants and revokes a role, rotates, and deletes", func(ctx context.Context) {
		identity := createCatalogItemTenantAdminIdentityFixture(ctx)
		accounts := publicv1.NewServiceAccountsClient(identity.Conn)
		secrets := publicv1.NewSecretsClient(identity.Conn)
		roleBindings := publicv1.NewRoleBindingsClient(identity.Conn)
		rolesResponse, err := publicv1.NewRolesClient(identity.Conn).List(ctx, &publicv1.RolesListRequest{})
		Expect(err).ToNot(HaveOccurred())
		roles := make(map[string]string, len(rolesResponse.GetItems()))
		for _, role := range rolesResponse.GetItems() {
			roles[role.GetMetadata().GetName()] = role.GetId()
		}
		Expect(roles).To(HaveKey("tenant-automation"))

		homeDir, err := tool.NewCLIHomeDir()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(homeDir)).To(Succeed()) })
		_, stderr, code := tool.LoginCLI(ctx, homeDir, identity.Username, identity.Password)
		Expect(code).To(Equal(0), "CLI login failed: %s", stderr)

		accountName := "cli-sa-" + uuid.New()[24:]
		bindingName := "cli-sa-binding-" + uuid.New()[24:]
		var accountID, bindingID string
		accountDeleted, bindingDeleted := false, false
		DeferCleanup(func(ctx context.Context) {
			if bindingID != "" && !bindingDeleted {
				_, cleanupErr := roleBindings.Delete(ctx, publicv1.RoleBindingsDeleteRequest_builder{Id: bindingID}.Build())
				Expect(cleanupErr).To(Succeed())
			}
			if accountID != "" && !accountDeleted {
				deleteServiceAccount(ctx, accounts, accountID)
			}
		})

		createPath := writeServiceAccountCLIFile(homeDir, "create.json", map[string]any{
			"@type": "type.googleapis.com/osac.public.v1.ServiceAccount",
			"metadata": map[string]any{
				"name": accountName,
			},
		})
		createOutput, createStderr, code := tool.RunCLI(ctx, homeDir, "serviceaccount", "create", "-f", createPath)
		Expect(code).To(Equal(0), "service-account create failed: %s", createStderr)
		created := parseServiceAccountCredentialOutput(createOutput)
		Expect(created.ClientID).ToNot(BeEmpty())
		Expect(created.ClientSecret).ToNot(BeEmpty())
		accountID = created.ServiceAccount.ID
		Expect(accountID).ToNot(BeEmpty())
		Expect(created.ServiceAccount.Metadata.Name).To(Equal(accountName))
		Expect(created.ServiceAccount.Metadata.Tenant).To(Equal(identity.TenantID))

		accountResponse, err := accounts.Get(ctx, publicv1.ServiceAccountsGetRequest_builder{Id: accountID}.Build())
		Expect(err).ToNot(HaveOccurred())
		account := accountResponse.GetObject()
		credentialResponse, err := secrets.Get(ctx, publicv1.SecretsGetRequest_builder{Id: account.GetStatus().GetClientSecret().GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())
		if string(credentialResponse.GetObject().GetData()["client_id"]) != created.ClientID {
			Fail("CLI create output did not match the client ID from the referenced Secret")
		}
		if string(credentialResponse.GetObject().GetData()["client_secret"]) != created.ClientSecret {
			Fail("CLI create output did not match the credential from the referenced Secret")
		}

		getOutput, getStderr, code := tool.RunCLI(ctx, homeDir, "serviceaccount", "get", accountName)
		Expect(code).To(Equal(0), "service-account get failed: %s", getStderr)
		if strings.Contains(getOutput, created.ClientSecret) {
			Fail("service-account get exposed credential bytes")
		}
		var fetched serviceAccountCLIView
		Expect(json.Unmarshal([]byte(getOutput), &fetched)).To(Succeed())
		Expect(fetched.ID).To(Equal(accountID))
		Expect(fetched.Status.ClientSecret.ID).To(Equal(account.GetStatus().GetClientSecret().GetId()))

		listOutput, listStderr, code := tool.RunCLI(ctx, homeDir, "serviceaccount", "list", "--filter", fmt.Sprintf("this.metadata.name == %q", accountName))
		Expect(code).To(Equal(0), "service-account list failed: %s", listStderr)
		if strings.Contains(listOutput, created.ClientSecret) {
			Fail("service-account list exposed credential bytes")
		}
		var listed []serviceAccountCLIView
		Expect(json.Unmarshal([]byte(listOutput), &listed)).To(Succeed())
		Expect(listed).To(HaveLen(1))
		Expect(listed[0].ID).To(Equal(accountID))

		updatePath := writeServiceAccountCLIFile(homeDir, "update.json", map[string]any{
			"id": accountID,
			"metadata": map[string]any{
				"display_name": "Updated CLI worker", "labels": map[string]string{"source": "cli-test"},
			},
		})
		updateOutput, updateStderr, code := tool.RunCLI(ctx, homeDir, "serviceaccount", "update", "-f", updatePath)
		Expect(code).To(Equal(0), "service-account update failed: %s", updateStderr)
		if strings.Contains(updateOutput, created.ClientSecret) {
			Fail("service-account update exposed credential bytes")
		}
		var updated serviceAccountCLIView
		Expect(json.Unmarshal([]byte(updateOutput), &updated)).To(Succeed())
		Expect(updated.Metadata.DisplayName).To(Equal("Updated CLI worker"))
		Expect(updated.Metadata.Labels).To(HaveKeyWithValue("source", "cli-test"))

		bindingPath := writeServiceAccountCLIFile(homeDir, "binding.json", map[string]any{
			"@type":    "type.googleapis.com/osac.public.v1.RoleBinding",
			"metadata": map[string]any{"name": bindingName},
			"spec": map[string]any{
				"role":             map[string]string{"id": roles["tenant-automation"]},
				"service_accounts": []map[string]string{{"id": accountID}},
			},
		})
		_, createBindingStderr, code := tool.RunCLI(ctx, homeDir, "create", "-f", bindingPath)
		Expect(code).To(Equal(0), "RoleBinding create failed: %s", createBindingStderr)
		bindingLookup, err := roleBindings.List(ctx, publicv1.RoleBindingsListRequest_builder{
			Filter: new(fmt.Sprintf("this.metadata.name == %q", bindingName)),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(bindingLookup.GetItems()).To(HaveLen(1))
		bindingID = bindingLookup.GetItems()[0].GetId()
		Expect(bindingLookup.GetItems()[0].GetSpec().GetServiceAccounts()).To(HaveLen(1))
		Expect(bindingLookup.GetItems()[0].GetSpec().GetServiceAccounts()[0].GetId()).To(Equal(accountID))

		serviceAccountHome, err := tool.NewCLIHomeDir()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(serviceAccountHome)).To(Succeed()) })
		clientIDFile := filepath.Join(serviceAccountHome, "client-id")
		clientSecretFile := filepath.Join(serviceAccountHome, "client-secret")
		Expect(os.WriteFile(clientIDFile, []byte(created.ClientID), 0600)).To(Succeed())
		Expect(os.WriteFile(clientSecretFile, []byte(created.ClientSecret), 0600)).To(Succeed())
		_, clientLoginStderr, code := tool.RunCLI(ctx, serviceAccountHome,
			"login", fmt.Sprintf("https://%s", externalServiceAddr), "--flow=credentials",
			"--client-id-file", clientIDFile, "--client-secret-file", clientSecretFile, "--insecure")
		Expect(code).To(Equal(0), "client_credentials login failed: %s", clientLoginStderr)
		serviceTokenSource, err := serviceAccountTokenSource(ctx, created.ClientID, created.ClientSecret)
		Expect(err).ToNot(HaveOccurred())
		serviceAccountConn, err := tool.makeGrpcConn(externalServiceAddr, serviceTokenSource)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(serviceAccountConn.Close()).To(Succeed()) })
		_, secretErr := publicv1.NewSecretsClient(serviceAccountConn).Get(ctx,
			publicv1.SecretsGetRequest_builder{Id: account.GetStatus().GetClientSecret().GetId()}.Build())
		Expect(status.Code(secretErr)).To(Equal(codes.PermissionDenied),
			"ordinary service-account token must not read raw Secrets")

		_, authorizedStderr, code := tool.RunCLI(ctx, serviceAccountHome, "get", "clusters", "-o", "json")
		Expect(code).To(Equal(0), "RoleBinding should permit cluster listing: %s", authorizedStderr)
		secretOutput, deniedSecretStderr, code := tool.RunCLI(ctx, serviceAccountHome, "get", "secrets", account.GetStatus().GetClientSecret().GetId(), "-o", "json")
		if code == 0 && strings.Contains(secretOutput, created.ClientSecret) {
			Fail("ordinary service-account CLI output exposed raw Secret bytes")
		}
		if strings.Contains(deniedSecretStderr, created.ClientSecret) {
			Fail("CLI error output exposed the service-account credential")
		}

		_, deleteBindingStderr, code := tool.RunCLI(ctx, homeDir, "delete", "rolebindings", bindingName)
		Expect(code).To(Equal(0), "RoleBinding revoke failed: %s", deleteBindingStderr)
		bindingDeleted = true
		_, revokedStderr, code := tool.RunCLI(ctx, serviceAccountHome, "get", "clusters", "-o", "json")
		Expect(code).NotTo(Equal(0), "removing the RoleBinding should revoke API access")
		if strings.Contains(revokedStderr, created.ClientSecret) {
			Fail("CLI error output exposed the service-account credential")
		}

		rotateOutput, rotateStderr, code := tool.RunCLI(ctx, homeDir, "serviceaccount", "regenerate-secret", accountID)
		Expect(code).To(Equal(0), "service-account secret rotation failed: %s", rotateStderr)
		rotated := parseServiceAccountCredentialOutput(rotateOutput)
		Expect(rotated.ServiceAccount.ID).To(Equal(accountID))
		if rotated.ClientSecret == "" || rotated.ClientSecret == created.ClientSecret {
			Fail("secret rotation did not return a new credential")
		}
		rotationStatus, err := accounts.Get(ctx, publicv1.ServiceAccountsGetRequest_builder{Id: accountID}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(rotationStatus.GetObject().GetStatus().GetSecretRotationState()).To(Equal(
			publicv1.ServiceAccountSecretRotationState_SERVICE_ACCOUNT_SECRET_ROTATION_STATE_COMPLETED))
		rotatedSecret, err := secrets.Get(ctx, publicv1.SecretsGetRequest_builder{
			Id: rotationStatus.GetObject().GetStatus().GetClientSecret().GetId(),
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		if string(rotatedSecret.GetObject().GetData()["client_secret"]) != rotated.ClientSecret {
			Fail("CLI rotation output did not match the credential from the referenced Secret")
		}

		getOutput, getStderr, code = tool.RunCLI(ctx, homeDir, "serviceaccount", "get", accountName)
		Expect(code).To(Equal(0), "service-account get after rotation failed: %s", getStderr)
		if strings.Contains(getOutput, rotated.ClientSecret) {
			Fail("service-account get exposed rotated credential bytes")
		}

		deleteOutput, deleteStderr, code := tool.RunCLI(ctx, homeDir, "serviceaccount", "delete", accountName)
		Expect(code).To(Equal(0), "service-account delete failed: %s", deleteStderr)
		var deletion struct {
			ID                string `json:"id"`
			DeletionRequested bool   `json:"deletion_requested"`
		}
		Expect(json.Unmarshal([]byte(deleteOutput), &deletion)).To(Succeed())
		Expect(deletion.ID).To(Equal(accountID))
		Expect(deletion.DeletionRequested).To(BeTrue())
		accountDeleted = true
		Eventually(ctx, func(g Gomega) {
			_, getErr := accounts.Get(ctx, publicv1.ServiceAccountsGetRequest_builder{Id: accountID}.Build())
			g.Expect(status.Code(getErr)).To(Equal(codes.NotFound))
		}, 2*time.Minute, time.Second).Should(Succeed())
	})
})

type serviceAccountCLIView struct {
	ID       string `json:"id"`
	Metadata struct {
		Name        string            `json:"name"`
		Tenant      string            `json:"tenant"`
		DisplayName string            `json:"display_name"`
		Labels      map[string]string `json:"labels"`
	} `json:"metadata"`
	Status struct {
		ClientSecret struct {
			ID string `json:"id"`
		} `json:"client_secret"`
	} `json:"status"`
}

func parseServiceAccountCredentialOutput(output string) struct {
	ServiceAccount serviceAccountCLIView `json:"service_account"`
	ClientID       string                `json:"client_id"`
	ClientSecret   string                `json:"client_secret"`
} {
	var result struct {
		ServiceAccount serviceAccountCLIView `json:"service_account"`
		ClientID       string                `json:"client_id"`
		ClientSecret   string                `json:"client_secret"`
	}
	Expect(json.Unmarshal([]byte(output), &result)).To(Succeed())
	return result
}

func writeServiceAccountCLIFile(homeDir, name string, value any) string {
	GinkgoHelper()
	path := filepath.Join(homeDir, name)
	data, err := json.Marshal(value)
	Expect(err).ToNot(HaveOccurred())
	Expect(os.WriteFile(path, data, 0600)).To(Succeed())
	return path
}
