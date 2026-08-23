/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package contract contains contract tests that verify Helm chart templates
// match the RBAC permissions required by the operator at runtime.
package contract

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// helmTimeout is the maximum time a helm subprocess is allowed to run.
const helmTimeout = 30 * time.Second

// policyRule mirrors rbac.authorization.k8s.io/v1.PolicyRule for YAML parsing.
type policyRule struct {
	APIGroups []string `yaml:"apiGroups"`
	Resources []string `yaml:"resources"`
	Verbs     []string `yaml:"verbs"`
}

// clusterRole is a minimal representation of a ClusterRole for YAML parsing.
type clusterRole struct {
	APIVersion string       `yaml:"apiVersion"`
	Kind       string       `yaml:"kind"`
	Rules      []policyRule `yaml:"rules"`
}

// chartDir returns the absolute path to osac-operator/charts/operator relative
// to this test file's location, making the test independent of the working
// directory.
func chartDir() string {
	_, thisFile, _, _ := runtime.Caller(0) //nolint:dogsled
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "charts", "operator")
}

// renderHubAccessClusterRole shells out to `helm template` with
// hubAccess.enabled=true and returns the rendered YAML for the
// hub-access-clusterrole.yaml template.
//
// If helm is not on PATH the test is skipped so that `make test`
// (unit-test gate) never breaks on environments without helm.
func renderHubAccessClusterRole(t *testing.T) []byte {
	t.Helper()

	helmBin := findHelm(t)

	chart := chartDir()
	if _, err := os.Stat(filepath.Join(chart, "Chart.yaml")); err != nil {
		t.Fatalf("chart directory not found at %s: %v", chart, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), helmTimeout)
	defer cancel()

	cmd := exec.CommandContext(
		ctx,
		helmBin, "template", "contract-test", chart,
		"--set", "hubAccess.enabled=true",
		"-s", "templates/hub-access-clusterrole.yaml",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helm template failed: %v\n%s", err, out)
	}
	return out
}

// findHelm locates the helm binary on PATH or in ~/bin.
// It skips the test if helm cannot be found and handles
// os.UserHomeDir errors before constructing a fallback path.
func findHelm(t *testing.T) string {
	t.Helper()

	helmBin, err := exec.LookPath("helm")
	if err == nil {
		return helmBin
	}

	// Fall back to ~/bin/helm (CI containers may install there).
	// Use LookPath on the absolute path so directories and non-executable
	// files are rejected — os.Stat alone would accept them.
	home, homeErr := os.UserHomeDir()
	if homeErr != nil {
		t.Skipf("helm not on PATH and home directory unavailable (%v) — skipping contract test", homeErr)
	}

	helmBin = filepath.Join(home, "bin", "helm")
	if _, lookErr := exec.LookPath(helmBin); lookErr != nil {
		t.Skip("helm not found on PATH or in ~/bin — skipping contract test")
	}
	return helmBin
}

// parseClusterRole parses rendered YAML into a clusterRole struct.
func parseClusterRole(t *testing.T, rendered []byte) clusterRole {
	t.Helper()

	var cr clusterRole
	if err := yaml.Unmarshal(rendered, &cr); err != nil {
		t.Fatalf("failed to parse ClusterRole YAML: %v", err)
	}
	if cr.Kind != "ClusterRole" {
		t.Fatalf("expected Kind=ClusterRole, got %q", cr.Kind)
	}
	return cr
}

// ruleKey uniquely identifies a rule by its apiGroup + sorted resource list.
type ruleKey struct {
	apiGroup  string
	resources string // comma-joined sorted resources
}

// indexRules groups ClusterRole rules by (apiGroup, resources) so that
// assertions can look up expected verbs by resource group.
func indexRules(rules []policyRule) map[ruleKey][]string {
	idx := make(map[ruleKey][]string, len(rules))
	for _, r := range rules {
		for _, group := range r.APIGroups {
			res := make([]string, len(r.Resources))
			copy(res, r.Resources)
			sort.Strings(res)

			key := ruleKey{
				apiGroup:  group,
				resources: strings.Join(res, ","),
			}
			idx[key] = r.Verbs
		}
	}
	return idx
}

// verbExpectation declares which verbs a resource group must contain.
type verbExpectation struct {
	name      string   // human-readable label for test output
	apiGroup  string   // RBAC apiGroup
	resources []string // resources list (sorted for matching)
	verbs     []string // required verbs
}

// expectedHubAccessVerbs returns the authoritative list of RBAC verb
// expectations for the hub-access ClusterRole.
//
// This is the single source of truth: if the fulfillment-service starts
// needing new verbs on hub-cluster resources, add them here and the test
// will fail until the Helm template is updated — preventing a recurrence
// of OSAC-4258.
func expectedHubAccessVerbs() []verbExpectation {
	return []verbExpectation{
		{
			name:     "osac.openshift.io main resources",
			apiGroup: "osac.openshift.io",
			resources: []string{
				"baremetalinstances",
				"clusterorders",
				"computeinstances",
				"externalipattachments",
				"externalippools",
				"externalips",
				"natgateways",
				"securitygroups",
				"subnets",
				"tenants",
				"virtualnetworks",
				"volumes",
			},
			verbs: []string{"create", "delete", "get", "list", "patch", "update", "watch"},
		},
		{
			name:     "osac.openshift.io /status subresources",
			apiGroup: "osac.openshift.io",
			resources: []string{
				"baremetalinstances/status",
				"clusterorders/status",
				"computeinstances/status",
				"externalipattachments/status",
				"externalippools/status",
				"externalips/status",
				"natgateways/status",
				"securitygroups/status",
				"subnets/status",
				"tenants/status",
				"virtualnetworks/status",
				"volumes/status",
			},
			verbs: []string{"get", "patch", "update"},
		},
		{
			name:     "console.osac.openshift.io console/vnc subresources",
			apiGroup: "console.osac.openshift.io",
			resources: []string{
				"computeinstances/console",
				"computeinstances/vnc",
			},
			verbs: []string{"get"},
		},
		{
			name:      "core API secrets",
			apiGroup:  "",
			resources: []string{"secrets"},
			verbs:     []string{"create"},
		},
	}
}

// TestHubAccessClusterRoleVerbs is the main contract test.  It renders the
// hub-access-clusterrole.yaml Helm template and verifies that every
// required verb is present for each resource group.
//
// This test prevents regressions like OSAC-4258, where the ClusterRole
// only granted "get" on /status subresources but the fulfillment-service
// code also required "update" and "patch".
func TestHubAccessClusterRoleVerbs(t *testing.T) {
	rendered := renderHubAccessClusterRole(t)
	cr := parseClusterRole(t, rendered)
	ruleIndex := indexRules(cr.Rules)

	for _, exp := range expectedHubAccessVerbs() {
		t.Run(exp.name, func(t *testing.T) {
			sorted := make([]string, len(exp.resources))
			copy(sorted, exp.resources)
			sort.Strings(sorted)

			key := ruleKey{
				apiGroup:  exp.apiGroup,
				resources: strings.Join(sorted, ","),
			}

			actualVerbs, ok := ruleIndex[key]
			if !ok {
				t.Fatalf("rule group not found in ClusterRole: apiGroup=%q resources=%v",
					exp.apiGroup, exp.resources)
			}

			actualSet := toSet(actualVerbs)
			var missing []string
			for _, v := range exp.verbs {
				if !actualSet[v] {
					missing = append(missing, v)
				}
			}
			if len(missing) > 0 {
				t.Errorf("missing required verbs for %s (apiGroup=%q resources=%v): %v\n"+
					"  have: %v\n"+
					"  want: %v",
					exp.name, exp.apiGroup, exp.resources, missing,
					actualVerbs, exp.verbs)
			}
		})
	}
}

// TestHubAccessClusterRoleDisabledByDefault verifies that the ClusterRole
// is NOT rendered when hubAccess.enabled is false (the default).
func TestHubAccessClusterRoleDisabledByDefault(t *testing.T) {
	helmBin := findHelm(t)
	chart := chartDir()

	ctx, cancel := context.WithTimeout(context.Background(), helmTimeout)
	defer cancel()

	cmd := exec.CommandContext(
		ctx,
		helmBin, "template", "contract-test", chart,
		"-s", "templates/hub-access-clusterrole.yaml",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// helm template exits non-zero when the selected template renders
		// to an empty document (because the if-guard evaluates false).
		// That is exactly the behaviour we want — but only that specific
		// error. Distinguish it from unexpected rendering failures.
		outStr := string(out)
		if strings.Contains(outStr, "could not find template") {
			return // expected: guarded template produced no output
		}
		t.Fatalf("unexpected helm template error (expected empty-template error): %v\n%s",
			err, outStr)
	}

	// If it somehow renders, the output should be empty or whitespace-only.
	trimmed := strings.TrimSpace(string(out))
	if trimmed != "" {
		t.Errorf("hub-access ClusterRole should not render when hubAccess.enabled=false,"+
			" but got:\n%s", trimmed)
	}
}

// TestHubAccessStatusSubresourceVerbs is a focused regression test for
// OSAC-4258: /status subresources must allow "get", "patch", AND "update".
func TestHubAccessStatusSubresourceVerbs(t *testing.T) {
	rendered := renderHubAccessClusterRole(t)
	cr := parseClusterRole(t, rendered)

	requiredStatusVerbs := []string{"get", "patch", "update"}

	for _, rule := range cr.Rules {
		for _, res := range rule.Resources {
			if !strings.HasSuffix(res, "/status") {
				continue
			}

			verbSet := toSet(rule.Verbs)
			for _, required := range requiredStatusVerbs {
				if !verbSet[required] {
					t.Errorf("OSAC-4258 regression: resource %q is missing verb %q "+
						"(have %v, need %v)",
						res, required, rule.Verbs, requiredStatusVerbs)
				}
			}
		}
	}
}

// TestHubAccessExpectedResources verifies that every expected OSAC resource
// appears in the ClusterRole's main resources rule. If a new CRD is added
// but not granted to the fulfillment-service, this test surfaces the gap.
func TestHubAccessExpectedResources(t *testing.T) {
	rendered := renderHubAccessClusterRole(t)
	cr := parseClusterRole(t, rendered)

	// Collect all main resources (not /status, /console, /vnc subresources).
	mainResources := make(map[string]bool)
	for _, rule := range cr.Rules {
		for _, group := range rule.APIGroups {
			if group != "osac.openshift.io" {
				continue
			}
			for _, res := range rule.Resources {
				if !strings.Contains(res, "/") {
					mainResources[res] = true
				}
			}
		}
	}

	expected := []string{
		"baremetalinstances",
		"clusterorders",
		"computeinstances",
		"externalipattachments",
		"externalippools",
		"externalips",
		"natgateways",
		"securitygroups",
		"subnets",
		"tenants",
		"virtualnetworks",
		"volumes",
	}

	for _, res := range expected {
		if !mainResources[res] {
			t.Errorf("expected resource %q not found in hub-access ClusterRole main resources rule", res)
		}
	}
}

// TestHubAccessStatusResourcesParity verifies that every main OSAC resource
// also has a corresponding /status subresource entry, ensuring status
// updates work for all resource types.
func TestHubAccessStatusResourcesParity(t *testing.T) {
	rendered := renderHubAccessClusterRole(t)
	cr := parseClusterRole(t, rendered)

	mainResources := make(map[string]bool)
	statusResources := make(map[string]bool)

	for _, rule := range cr.Rules {
		for _, group := range rule.APIGroups {
			if group != "osac.openshift.io" {
				continue
			}
			for _, res := range rule.Resources {
				if strings.HasSuffix(res, "/status") {
					statusResources[strings.TrimSuffix(res, "/status")] = true
				} else if !strings.Contains(res, "/") {
					mainResources[res] = true
				}
			}
		}
	}

	for res := range mainResources {
		if !statusResources[res] {
			t.Errorf("main resource %q has no corresponding /status subresource entry", res)
		}
	}
}

func toSet(items []string) map[string]bool {
	s := make(map[string]bool, len(items))
	for _, item := range items {
		s[item] = true
	}
	return s
}

// --- Diagnostic output helpers ---

// TestChartDirResolution is a trivial test that logs the resolved chart
// directory for debugging in CI.
func TestChartDirResolution(t *testing.T) {
	dir := chartDir()
	t.Logf("resolved chart directory: %s", dir)
	if _, err := os.Stat(filepath.Join(dir, "Chart.yaml")); err != nil {
		t.Fatalf("Chart.yaml not found in %s: %v", dir, err)
	}
}
