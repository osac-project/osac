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

package contract

import (
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var helmTemplateDirectiveRE = regexp.MustCompile(`\{\{.*?\}\}`)

type rbacDocument struct {
	Kind     string       `yaml:"kind"`
	Metadata crMetadata   `yaml:"metadata"`
	Rules    []policyRule `yaml:"rules"`
	RoleRef  struct {
		Kind string `yaml:"kind"`
		Name string `yaml:"name"`
	} `yaml:"roleRef"`
	Subjects []struct {
		Kind      string `yaml:"kind"`
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"subjects"`
}

func loadClusterRoleFile(t *testing.T, path string) clusterRole {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read ClusterRole at %s: %v", path, err)
	}

	var role clusterRole
	if err := yaml.Unmarshal(raw, &role); err != nil {
		t.Fatalf("failed to parse ClusterRole at %s: %v", path, err)
	}
	return role
}

func loadHelmClusterRoleTemplate(t *testing.T, path string) clusterRole {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read ClusterRole template at %s: %v", path, err)
	}

	// The ClusterRole rules are static YAML; stripping the metadata template
	// directives lets this contract test inspect the rendered permission list
	// without requiring Helm or resolving chart dependencies.
	cleaned := helmTemplateDirectiveRE.ReplaceAllString(string(raw), "")
	var role clusterRole
	if err := yaml.Unmarshal([]byte(cleaned), &role); err != nil {
		t.Fatalf("failed to parse ClusterRole template at %s: %v", path, err)
	}
	if role.Kind != "ClusterRole" {
		t.Fatalf("resource at %s has kind %q, want ClusterRole", path, role.Kind)
	}
	return role
}

func loadHelmRBACDocuments(t *testing.T, path string) []rbacDocument {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read RBAC template at %s: %v", path, err)
	}
	withReleaseNamespace := strings.ReplaceAll(string(raw), "{{ .Release.Namespace }}", "operator-release")
	cleaned := helmTemplateDirectiveRE.ReplaceAllString(withReleaseNamespace, "")
	docs := strings.Split(cleaned, "\n---\n")
	manifests := make([]rbacDocument, 0, len(docs))
	for _, doc := range docs {
		if strings.TrimSpace(doc) == "" {
			continue
		}
		var manifest rbacDocument
		if err := yaml.Unmarshal([]byte(doc), &manifest); err != nil {
			t.Fatalf("failed to parse RBAC document in %s: %v", path, err)
		}
		if manifest.Kind != "" {
			manifests = append(manifests, manifest)
		}
	}
	return manifests
}

func clusterRoleRuleVerbs(role clusterRole, apiGroup, resource string) ([]string, bool) {
	verbSet := make(map[string]struct{})
	found := false
	for _, rule := range role.Rules {
		if !clusterRoleRuleMatches(rule, apiGroup, resource) {
			continue
		}
		found = true
		for _, verb := range rule.Verbs {
			verbSet[verb] = struct{}{}
		}
	}
	verbs := make([]string, 0, len(verbSet))
	for verb := range verbSet {
		verbs = append(verbs, verb)
	}
	sort.Strings(verbs)
	return verbs, found
}

func clusterRoleRuleMatches(rule policyRule, apiGroup, resource string) bool {
	return (slices.Contains(rule.APIGroups, apiGroup) || slices.Contains(rule.APIGroups, "*")) &&
		(slices.Contains(rule.Resources, resource) || slices.Contains(rule.Resources, "*"))
}

func clusterRoleRuleGrants(rule policyRule, apiGroup, resource, verb string) bool {
	return clusterRoleRuleMatches(rule, apiGroup, resource) &&
		(slices.Contains(rule.Verbs, verb) || slices.Contains(rule.Verbs, "*"))
}
