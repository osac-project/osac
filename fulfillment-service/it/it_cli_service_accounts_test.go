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
	"encoding/base64"
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
		createOutput, createStderr, code := tool.RunCLI(ctx, homeDir, "create", "-f", createPath)
		Expect(code).To(Equal(0), "service-account create failed: %s", createStderr)
		Expect(createOutput).To(ContainSubstring("Created"))

		var account *publicv1.ServiceAccount
		Eventually(ctx, func(g Gomega) {
			response, listErr := accounts.List(ctx, publicv1.ServiceAccountsListRequest_builder{
				Filter: new(fmt.Sprintf("this.metadata.name == %q", accountName)),
			}.Build())
			g.Expect(listErr).ToNot(HaveOccurred())
			g.Expect(response.GetItems()).To(HaveLen(1))
			if len(response.GetItems()) == 1 {
				account = response.GetItems()[0]
				g.Expect(account.GetStatus().GetClientSecret().GetId()).ToNot(BeEmpty())
			}
		}, 5*time.Minute, time.Second).Should(Succeed())
		accountID = account.GetId()
		Expect(accountID).ToNot(BeEmpty())
		Expect(account.GetMetadata().GetName()).To(Equal(accountName))
		Expect(account.GetMetadata().GetTenant()).To(Equal(identity.TenantID))

		accountResponse, err := accounts.Get(ctx, publicv1.ServiceAccountsGetRequest_builder{Id: accountID}.Build())
		Expect(err).ToNot(HaveOccurred())
		account = accountResponse.GetObject()
		credentialResponse, err := secrets.Get(ctx, publicv1.SecretsGetRequest_builder{Id: account.GetStatus().GetClientSecret().GetId()}.Build())
		Expect(err).ToNot(HaveOccurred())

		secretOutput, secretStderr, code := tool.RunCLI(ctx, homeDir, "get", "secrets", account.GetStatus().GetClientSecret().GetId(), "-o", "json")
		Expect(code).To(Equal(0), "generic secret get failed: %s", secretStderr)
		var secretView struct {
			Data map[string]string `json:"data"`
		}
		Expect(json.Unmarshal([]byte(secretOutput), &secretView)).To(Succeed())
		clientIDBytes, err := base64.StdEncoding.DecodeString(secretView.Data["client_id"])
		Expect(err).ToNot(HaveOccurred())
		clientSecretBytes, err := base64.StdEncoding.DecodeString(secretView.Data["client_secret"])
		Expect(err).ToNot(HaveOccurred())
		clientID, clientSecret := string(clientIDBytes), string(clientSecretBytes)
		Expect(clientID).ToNot(BeEmpty())
		Expect(clientSecret).ToNot(BeEmpty())
		if string(credentialResponse.GetObject().GetData()["client_id"]) != clientID {
			Fail("generic secret get output did not match the client ID from the referenced Secret")
		}
		if string(credentialResponse.GetObject().GetData()["client_secret"]) != clientSecret {
			Fail("generic secret get output did not match the credential from the referenced Secret")
		}

		getOutput, getStderr, code := tool.RunCLI(ctx, homeDir, "get", "serviceaccounts", accountName, "-o", "json")
		Expect(code).To(Equal(0), "service-account get failed: %s", getStderr)
		if strings.Contains(getOutput, clientSecret) {
			Fail("service-account get exposed credential bytes")
		}
		var fetched serviceAccountCLIView
		Expect(json.Unmarshal([]byte(getOutput), &fetched)).To(Succeed())
		Expect(fetched.ID).To(Equal(accountID))
		Expect(fetched.Status.ClientSecret.ID).To(Equal(account.GetStatus().GetClientSecret().GetId()))

		listOutput, listStderr, code := tool.RunCLI(ctx, homeDir, "get", "serviceaccounts", "--filter", fmt.Sprintf("this.metadata.name == %q", accountName), "-o", "json")
		Expect(code).To(Equal(0), "service-account list failed: %s", listStderr)
		if strings.Contains(listOutput, clientSecret) {
			Fail("service-account list exposed credential bytes")
		}
		var listed serviceAccountCLIView
		Expect(json.Unmarshal([]byte(listOutput), &listed)).To(Succeed())
		Expect(listed.ID).To(Equal(accountID))

		editorPath := filepath.Join(homeDir, "service-account-editor.sh")
		editorScript := `#!/bin/sh
python3 - "$1" <<'PY'
import json
import sys

path = sys.argv[1]
with open(path, encoding="utf-8") as file:
    account = json.load(file)
account["metadata"]["labels"] = {"source": "cli-test"}
with open(path, "w", encoding="utf-8") as file:
    json.dump(account, file)
PY
`
		Expect(os.WriteFile(editorPath, []byte(editorScript), 0700)).To(Succeed())
		updateOutput, updateStderr, code := tool.RunCLIWithEnv(
			ctx,
			homeDir,
			[]string{"EDITOR=" + editorPath},
			"edit", "serviceaccounts", accountName, "-o", "json",
		)
		Expect(code).To(Equal(0), "service-account edit failed: %s", updateStderr)
		if strings.Contains(updateOutput, clientSecret) {
			Fail("service-account update exposed credential bytes")
		}
		updatedResponse, err := accounts.Get(ctx, publicv1.ServiceAccountsGetRequest_builder{Id: accountID}.Build())
		Expect(err).ToNot(HaveOccurred())
		Expect(updatedResponse.GetObject().GetMetadata().GetLabels()).To(HaveKeyWithValue("source", "cli-test"))

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
		Expect(os.WriteFile(clientIDFile, []byte(clientID), 0600)).To(Succeed())
		Expect(os.WriteFile(clientSecretFile, []byte(clientSecret), 0600)).To(Succeed())
		_, clientLoginStderr, code := tool.RunCLI(ctx, serviceAccountHome,
			"login", fmt.Sprintf("https://%s", externalServiceAddr), "--flow=credentials",
			"--client-id-file", clientIDFile, "--client-secret-file", clientSecretFile, "--insecure")
		Expect(code).To(Equal(0), "client_credentials login failed: %s", clientLoginStderr)
		serviceTokenSource, err := serviceAccountTokenSource(ctx, clientID, clientSecret)
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
		if code == 0 && strings.Contains(secretOutput, clientSecret) {
			Fail("ordinary service-account CLI output exposed raw Secret bytes")
		}
		if strings.Contains(deniedSecretStderr, clientSecret) {
			Fail("CLI error output exposed the service-account credential")
		}

		_, deleteBindingStderr, code := tool.RunCLI(ctx, homeDir, "delete", "rolebindings", bindingName)
		Expect(code).To(Equal(0), "RoleBinding revoke failed: %s", deleteBindingStderr)
		bindingDeleted = true
		_, revokedStderr, code := tool.RunCLI(ctx, serviceAccountHome, "get", "clusters", "-o", "json")
		Expect(code).NotTo(Equal(0), "removing the RoleBinding should revoke API access")
		if strings.Contains(revokedStderr, clientSecret) {
			Fail("CLI error output exposed the service-account credential")
		}

		rotateOutput, rotateStderr, code := tool.RunCLI(ctx, homeDir, "regenerate", "serviceaccount", accountID)
		Expect(code).To(Equal(0), "service-account secret rotation failed: %s", rotateStderr)
		rotated := parseServiceAccountCredentialOutput(rotateOutput)
		Expect(rotated.ServiceAccount.ID).To(Equal(accountID))
		Expect(rotated.ClientID).To(Equal(clientID))
		if rotated.ClientSecret == "" || rotated.ClientSecret == clientSecret {
			Fail("secret rotation did not return a new credential")
		}
		accountAfterRotation, err := accounts.Get(ctx, publicv1.ServiceAccountsGetRequest_builder{Id: accountID}.Build())
		Expect(err).ToNot(HaveOccurred())
		rotatedSecretID := accountAfterRotation.GetObject().GetStatus().GetClientSecret().GetId()
		Expect(rotatedSecretID).NotTo(BeEmpty())
		rotatedSecret, err := secrets.Get(ctx, publicv1.SecretsGetRequest_builder{
			Id: rotatedSecretID,
		}.Build())
		Expect(err).ToNot(HaveOccurred())
		if string(rotatedSecret.GetObject().GetData()["client_secret"]) != rotated.ClientSecret {
			Fail("CLI rotation output did not match the credential from the referenced Secret")
		}

		getOutput, getStderr, code = tool.RunCLI(ctx, homeDir, "get", "serviceaccounts", accountName, "-o", "json")
		Expect(code).To(Equal(0), "service-account get after rotation failed: %s", getStderr)
		if strings.Contains(getOutput, rotated.ClientSecret) {
			Fail("service-account get exposed rotated credential bytes")
		}

		_, deleteStderr, code := tool.RunCLI(ctx, homeDir, "delete", "serviceaccounts", accountName)
		Expect(code).To(Equal(0), "service-account delete failed: %s", deleteStderr)
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
