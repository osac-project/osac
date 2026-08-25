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
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

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
	Metadata   crMetadata   `yaml:"metadata"`
	Rules      []policyRule `yaml:"rules"`
}

// crMetadata captures the name field for identifying the right ClusterRole.
type crMetadata struct {
	Name string `yaml:"name"`
}

// hubAccessTemplatePath is the path to the hub-access template relative to
// the mono-repo root. After the OSAC-3752 restructuring this lives in the
// osac-installer umbrella chart, not in osac-operator's own chart.
const hubAccessTemplatePath = "osac-installer/charts/osac/templates/hub-access.yaml"

// hubAccessValuesPath is the umbrella chart's values.yaml which sets
// the default for hubAccess.enabled.
const hubAccessValuesPath = "osac-installer/charts/osac/values.yaml"

// templateDirectiveRe matches Helm/Go-template directives ({{ ... }}).
var templateDirectiveRe = regexp.MustCompile(`\{\{.*?\}\}`)

// repoRoot returns the mono-repo root by walking up from this test file's
// location until it finds the go.work file that anchors the workspace.
func repoRoot() string {
	_, thisFile, _, _ := runtime.Caller(0) //nolint:dogsled
	dir := filepath.Dir(thisFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root without finding go.work.
			// Fall back to relative path from the test file location:
			// test/contract/ -> osac-operator/ -> mono-repo root
			_, f, _, _ := runtime.Caller(0) //nolint:dogsled
			return filepath.Join(filepath.Dir(f), "..", "..", "..")
		}
		dir = parent
	}
}

// loadHubAccessClusterRoles reads the hub-access Helm template, strips
// Go-template directives so the remaining YAML is parseable, splits the
// multi-document file, and returns only the ClusterRole documents.
//
// This approach avoids shelling out to helm, which requires chart
// dependency resolution for the umbrella chart and may not be installed
// in every CI environment.
func loadHubAccessClusterRoles(t *testing.T) []clusterRole {
	t.Helper()

	templateFile := filepath.Join(repoRoot(), hubAccessTemplatePath)
	raw, err := os.ReadFile(templateFile)
	if err != nil {
		t.Fatalf("failed to read hub-access template at %s: %v\n"+
			"(repo root resolved to %s)", templateFile, err, repoRoot())
	}

	// Strip template directives so pure-YAML fields remain.
	// Lines that are ONLY a template directive (e.g. {{- if ... }}) become
	// empty and are harmless. Lines with mixed content (e.g. "name: {{ .Release.Namespace }}-hub-access")
	// become "name: -hub-access" which still parses as valid YAML.
	cleaned := templateDirectiveRe.ReplaceAllString(string(raw), "")

	// Split multi-document YAML on "---" separators.
	docs := strings.Split(cleaned, "\n---\n")

	var roles []clusterRole
	for _, doc := range docs {
		trimmed := strings.TrimSpace(doc)
		if trimmed == "" {
			continue
		}
		var cr clusterRole
		if err := yaml.Unmarshal([]byte(trimmed), &cr); err != nil {
			// Some documents may not parse cleanly after directive stripping
			// (e.g. ServiceAccount with only template-derived fields). Skip those.
			continue
		}
		if cr.Kind == "ClusterRole" {
			roles = append(roles, cr)
		}
	}

	if len(roles) == 0 {
		t.Fatal("no ClusterRole documents found in hub-access template")
	}
	return roles
}

// findMainHubAccessRole returns the primary hub-access ClusterRole (the one
// whose name ends with "-hub-access", not "-hub-access-hosted-clusters").
func findMainHubAccessRole(t *testing.T, roles []clusterRole) clusterRole {
	t.Helper()
	for _, cr := range roles {
		name := cr.Metadata.Name
		if strings.HasSuffix(name, "-hub-access") && !strings.Contains(name, "hosted-clusters") {
			return cr
		}
	}
	t.Fatal("main hub-access ClusterRole (name ending in '-hub-access') not found")
	return clusterRole{} // unreachable
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

// TestHubAccessClusterRoleVerbs is the main contract test.  It parses the
// hub-access Helm template and verifies that every required verb is present
// for each resource group.
//
// This test prevents regressions like OSAC-4258, where the ClusterRole
// only granted "get" on /status subresources but the fulfillment-service
// code also required "update" and "patch".
func TestHubAccessClusterRoleVerbs(t *testing.T) {
	roles := loadHubAccessClusterRoles(t)
	cr := findMainHubAccessRole(t, roles)
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

// hubAccessValues is a minimal struct for parsing the hubAccess section
// from the umbrella chart's values.yaml.
type hubAccessValues struct {
	HubAccess struct {
		Enabled bool `yaml:"enabled"`
	} `yaml:"hubAccess"`
}

// TestHubAccessClusterRoleGuarded verifies two things:
//  1. The hub-access template is wrapped in a {{- if .Values.hubAccess.enabled }}
//     guard, ensuring the resources are not deployed unless explicitly enabled.
//  2. The chart's values.yaml defaults hubAccess.enabled to false, so the
//     ClusterRole is opt-in by default. This catches accidental default flips.
func TestHubAccessClusterRoleGuarded(t *testing.T) {
	t.Run("template has if-guard", func(t *testing.T) {
		templateFile := filepath.Join(repoRoot(), hubAccessTemplatePath)
		raw, err := os.ReadFile(templateFile)
		if err != nil {
			t.Fatalf("failed to read hub-access template: %v", err)
		}

		content := string(raw)
		if !strings.Contains(content, "{{- if .Values.hubAccess.enabled }}") {
			t.Error("hub-access template is not guarded by {{- if .Values.hubAccess.enabled }}")
		}
		if !strings.Contains(content, "{{- end }}") {
			t.Error("hub-access template is missing closing {{- end }}")
		}
	})

	t.Run("values.yaml defaults hubAccess.enabled to false", func(t *testing.T) {
		valuesFile := filepath.Join(repoRoot(), hubAccessValuesPath)
		raw, err := os.ReadFile(valuesFile)
		if err != nil {
			t.Fatalf("failed to read values.yaml: %v", err)
		}

		var vals hubAccessValues
		if err := yaml.Unmarshal(raw, &vals); err != nil {
			t.Fatalf("failed to parse values.yaml: %v", err)
		}

		if vals.HubAccess.Enabled {
			t.Error("hubAccess.enabled defaults to true in values.yaml — " +
				"it must default to false so hub-access resources are opt-in")
		}
	})
}

// TestHubAccessStatusSubresourceVerbs is a focused regression test for
// OSAC-4258: /status subresources must allow "get", "patch", AND "update".
func TestHubAccessStatusSubresourceVerbs(t *testing.T) {
	roles := loadHubAccessClusterRoles(t)
	cr := findMainHubAccessRole(t, roles)

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
	roles := loadHubAccessClusterRoles(t)
	cr := findMainHubAccessRole(t, roles)

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
	roles := loadHubAccessClusterRoles(t)
	cr := findMainHubAccessRole(t, roles)

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

	for res := range statusResources {
		if !mainResources[res] {
			t.Errorf("/status subresource %q/status has no corresponding main resource entry", res)
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

// TestTemplateFileResolution logs the resolved template path for CI debugging.
func TestTemplateFileResolution(t *testing.T) {
	root := repoRoot()
	path := filepath.Join(root, hubAccessTemplatePath)
	t.Logf("resolved repo root: %s", root)
	t.Logf("resolved template path: %s", path)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("hub-access template not found at %s: %v", path, err)
	}
}
