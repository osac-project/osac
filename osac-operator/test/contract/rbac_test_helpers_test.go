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
	"reflect"
	"regexp"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"
)

var helmTemplateDirectiveRE = regexp.MustCompile(`\{\{.*?\}\}`)

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

func clusterRoleRuleVerbs(role clusterRole, apiGroup string, resources []string) ([]string, bool) {
	sortedResources := append([]string(nil), resources...)
	sort.Strings(sortedResources)
	for _, rule := range role.Rules {
		if !reflect.DeepEqual(rule.APIGroups, []string{apiGroup}) {
			continue
		}
		actualResources := append([]string(nil), rule.Resources...)
		sort.Strings(actualResources)
		if reflect.DeepEqual(actualResources, sortedResources) {
			return rule.Verbs, true
		}
	}
	return nil, false
}
