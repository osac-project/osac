/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
specific language governing permissions and limitations under the License.
*/

package it

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/yaml"

	"github.com/osac-project/osac/fulfillment-service/internal/uuid"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
)

// The dev deployment configures no billing provider, so these specs exercise disabled mode end to end. Unlike
// BillingProviderStatuses.Get, which returns a NOT_CONFIGURED status object, the accounts List has no success
// shape for disabled mode: it fails on every path, so the CLI command fails too.
var _ = Describe("Billing accounts", Label("billing"), func() {
	It("fails from the CLI with the not-configured error", func(ctx context.Context) {
		homeDir, err := tool.NewCLIHomeDir()
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(homeDir)).To(Succeed()) })
		_, stderr, exitCode := tool.RunCLI(ctx, homeDir, "login", "https://"+internalServiceAddr,
			"--private", "--insecure", "--flow=password", "--user="+adminUsername, "--password="+adminsPassword)
		Expect(exitCode).To(Equal(0), "login: %s", stderr)

		stdout, stderr, exitCode := tool.RunCLI(ctx, homeDir, "get", "billingaccount", "-o", "json")
		Expect(exitCode).ToNot(Equal(0))
		Expect(stdout + stderr).To(ContainSubstring("billing is not configured"))
	})
})

// decodeBillingCLIItems decodes generic get's Any items, never the REST List envelope.
// Zero/many results are lists; a single result is an object.
func decodeBillingCLIItems(output, format, typeName string) ([]map[string]any, error) {
	body := []byte(output)
	if format == "yaml" {
		var err error
		body, err = yaml.YAMLToJSON(body)
		if err != nil {
			return nil, err
		}
	}
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, err
	}
	var items []map[string]any
	switch value := value.(type) {
	case map[string]any:
		items = []map[string]any{value}
	case []any:
		if len(value) == 1 {
			return nil, fmt.Errorf("single CLI result must be an object")
		}
		items = make([]map[string]any, 0, len(value))
		for _, item := range value {
			object, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("CLI item is not an object")
			}
			items = append(items, object)
		}
	default:
		return nil, fmt.Errorf("CLI result must be an object or list")
	}
	for _, item := range items {
		if item["@type"] != "type.googleapis.com/"+typeName {
			return nil, fmt.Errorf("unexpected CLI item type %v", item["@type"])
		}
		for _, field := range []string{"size", "total", "items"} {
			if _, envelope := item[field]; envelope {
				return nil, fmt.Errorf("CLI result must not contain a List envelope")
			}
		}
	}
	return items, nil
}

// newBillingCLI logs in for real and saves a customer tenant and an existing, nonempty
// project through the CLI. It does not replace Root's pre-run or write settings directly.
func newBillingCLI(ctx context.Context, tenant string, private bool) string {
	GinkgoHelper()
	Expect(tenant).ToNot(BeEmpty())
	Expect(tenant).ToNot(Equal("system"))
	projects := privatev1.NewProjectsClient(tool.InternalView().AdminConn())
	created, err := projects.Create(ctx, privatev1.ProjectsCreateRequest_builder{
		Object: privatev1.Project_builder{
			Metadata: privatev1.Metadata_builder{Name: "billing-cli-" + uuid.New(), Tenant: tenant}.Build(),
			Spec:     privatev1.ProjectSpec_builder{Title: "Billing CLI selection"}.Build(),
		}.Build(),
	}.Build())
	Expect(err).ToNot(HaveOccurred())
	project := created.GetObject()
	DeferCleanup(func(ctx context.Context) {
		deleteProject(ctx, projects, project.GetId())
	})
	home, err := tool.NewCLIHomeDir()
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(func() { Expect(os.RemoveAll(home)).To(Succeed()) })
	var stderr string
	var code int
	if private {
		_, stderr, code = tool.RunCLI(ctx, home, "login", "https://"+internalServiceAddr,
			"--private", "--insecure", "--flow=password", "--user="+adminUsername, "--password="+adminsPassword)
	} else {
		_, stderr, code = tool.LoginCLI(ctx, home, adminUsername, adminsPassword)
	}
	Expect(code).To(Equal(0), "login: %s", stderr)
	for _, selection := range []struct{ command, name string }{
		{"tenant", tenant}, {"project", project.GetMetadata().GetName()},
	} {
		Expect(selection.name).ToNot(BeEmpty())
		_, stderr, code = tool.RunCLI(ctx, home, selection.command, selection.name)
		Expect(code).To(Equal(0), "save %s: %s", selection.command, stderr)
		stdout, stderr, code := tool.RunCLI(ctx, home, selection.command)
		Expect(code).To(Equal(0), "read saved %s: %s", selection.command, stderr)
		Expect(stdout).To(ContainSubstring(selection.name))
	}
	AddReportEntry("billing CLI selection", map[string]any{
		"tenant": tenant, "project": project.GetMetadata().GetName(), "private": private,
	})
	return home
}

func billingCLIItems(ctx context.Context, home, resource, format, typeName string, args ...string) []map[string]any {
	GinkgoHelper()
	command := append([]string{"get", resource}, args...)
	command = append(command, "-o", format)
	stdout, stderr, code := tool.RunCLI(ctx, home, command...)
	Expect(code).To(Equal(0), "stdout: %s\nstderr: %s", stdout, stderr)
	items, err := decodeBillingCLIItems(stdout, format, typeName)
	Expect(err).ToNot(HaveOccurred(), "stdout: %s", stdout)
	return items
}

func expectBillingCLITable(ctx context.Context, home, resource string, headers []string, count int, args ...string) string {
	GinkgoHelper()
	command := append([]string{"get", resource}, args...)
	command = append(command, "-o", "table")
	stdout, stderr, code := tool.RunCLI(ctx, home, command...)
	Expect(code).To(Equal(0), "stdout: %s\nstderr: %s", stdout, stderr)
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if count == 0 {
		Expect(stdout).To(ContainSubstring("There are no objects matching the given criteria."))
	} else {
		Expect(lines).To(HaveLen(count + 1))
		Expect(strings.Fields(lines[0])).To(Equal(headers))
	}
	return stdout
}
