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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2/dsl/core"
	. "github.com/onsi/gomega"
	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

var _ = Describe("CLI tenant lifecycle", func() {
	It("creates, lists, updates, and deletes a tenant", func(ctx context.Context) {
		name := fmt.Sprintf("test-cli-%s", uuid.New())
		homeDir, err := tool.NewCLIHomeDir()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			Expect(os.RemoveAll(homeDir)).To(Succeed())
		})

		// Authenticate against the private API with an isolated home directory. Keep the address
		// and credentials out of the CLI arguments that the harness logs.
		userFile := filepath.Join(homeDir, "login-user")
		passwordFile := filepath.Join(homeDir, "login-password")
		Expect(os.WriteFile(userFile, []byte(adminUsername), 0600)).To(Succeed())
		Expect(os.WriteFile(passwordFile, []byte(adminsPassword), 0600)).To(Succeed())

		_, _, exitCode := tool.RunCLIWithEnv(ctx, homeDir,
			[]string{"OSAC_ADDRESS=https://" + internalServiceAddr},
			"login",
			"--private",
			"--flow=password",
			"--user-file="+userFile,
			"--password-file="+passwordFile,
			"--insecure",
		)
		Expect(exitCode).To(Equal(0), "login to the private API failed")

		tenantFile := filepath.Join(homeDir, "tenant.json")
		tenantObject := fmt.Sprintf(
			`{"@type":"type.googleapis.com/osac.private.v1.Tenant","metadata":{"name":%q}}`,
			name,
		)
		Expect(os.WriteFile(tenantFile, []byte(tenantObject), 0600)).To(Succeed())

		tenantsClient := privatev1.NewTenantsClient(tool.InternalView().AdminConn())
		projectsClient := privatev1.NewProjectsClient(tool.InternalView().AdminConn())
		created := false
		deleteRequested := false
		DeferCleanup(func(cleanupCtx context.Context) {
			if !created {
				return
			}
			if deleteRequested {
				completeTenantDeletion(cleanupCtx, tenantsClient, projectsClient, name, name)
			} else {
				deleteTenant(cleanupCtx, tenantsClient, projectsClient, name, name)
			}
			verifyTenantRemovedFromKeycloak(cleanupCtx, name)
		})

		// Suppress stdout because tenant Create may return one-time break-glass credentials.
		_, stderr, exitCode := tool.RunCLI(ctx, homeDir, "create", "-f", tenantFile)
		Expect(exitCode).To(Equal(0), "tenant Create failed: %s", stderr)
		created = true

		waitForTenantSynced(ctx, tenantsClient, name)
		verifyTenantInKeycloak(ctx, name)

		getOutput, stderr, exitCode := tool.RunCLI(ctx, homeDir,
			"get", "tenants", name, "-o", "json",
		)
		Expect(exitCode).To(Equal(0), "tenant Get failed: %s", stderr)
		var tenant struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		}
		Expect(json.Unmarshal([]byte(getOutput), &tenant)).To(Succeed())
		Expect(tenant.Metadata.Name).To(Equal(name))

		listOutput, stderr, exitCode := tool.RunCLI(ctx, homeDir,
			"get", "tenants", "-o", "json",
		)
		Expect(exitCode).To(Equal(0), "tenant List failed: %s", stderr)
		var listed []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
		}
		if unmarshalErr := json.Unmarshal([]byte(listOutput), &listed); unmarshalErr != nil {
			// The CLI renders one JSON result as an object and multiple results as an array.
			var single struct {
				Metadata struct {
					Name string `json:"name"`
				} `json:"metadata"`
			}
			Expect(json.Unmarshal([]byte(listOutput), &single)).To(Succeed())
			listed = append(listed, single)
		}
		names := make([]string, 0, len(listed))
		for _, item := range listed {
			names = append(names, item.Metadata.Name)
		}
		Expect(names).To(ContainElement(name))

		editorPath := filepath.Join(homeDir, "tenant-editor")
		editorScript := `#!/bin/sh
set -eu
file="$1"
tmp="${file}.tmp"
awk '
  {
    if (sub(/"metadata"[[:space:]]*:[[:space:]]*\{/, "\"metadata\":{\"display_name\":\"OSAC CLI display name\",\"description\":\"Updated by the OSAC CLI integration test\",")) {
      inserted = 1
    }
    print
  }
  END { if (!inserted) exit 1 }
' "$file" > "$tmp"
mv "$tmp" "$file"
`
		Expect(os.WriteFile(editorPath, []byte(editorScript), 0700)).To(Succeed())
		_, stderr, exitCode = tool.RunCLIWithEnv(ctx, homeDir, []string{"EDITOR=" + editorPath},
			"edit", "tenants", name, "-o", "json",
		)
		Expect(exitCode).To(Equal(0), "tenant edit failed: %s", stderr)

		getOutput, stderr, exitCode = tool.RunCLI(ctx, homeDir,
			"get", "tenants", name, "-o", "json",
		)
		Expect(exitCode).To(Equal(0), "tenant Get after edit failed: %s", stderr)
		var updated struct {
			Metadata struct {
				DisplayName string `json:"display_name"`
				Description string `json:"description"`
			} `json:"metadata"`
		}
		Expect(json.Unmarshal([]byte(getOutput), &updated)).To(Succeed())
		Expect(updated.Metadata.DisplayName).To(Equal("OSAC CLI display name"))
		Expect(updated.Metadata.Description).To(Equal("Updated by the OSAC CLI integration test"))

		_, stderr, exitCode = tool.RunCLI(ctx, homeDir, "delete", "tenants", name)
		Expect(exitCode).To(Equal(0), "tenant Delete failed: %s", stderr)
		deleteRequested = true
		completeTenantDeletion(ctx, tenantsClient, projectsClient, name, name)
		verifyTenantRemovedFromKeycloak(ctx, name)
		created = false
	})
})
