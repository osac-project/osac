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
	"path/filepath"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestManagerClusterRoleAllowsAgentWatch(t *testing.T) {
	path := filepath.Join(repoRoot(), "osac-operator/charts/operator/templates/clusterrole.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var role clusterRole
	if err := yaml.Unmarshal(templateDirectiveRe.ReplaceAll(raw, nil), &role); err != nil {
		t.Fatal(err)
	}
	if role.Kind != "ClusterRole" {
		t.Fatalf("expected manager ClusterRole, got %q", role.Kind)
	}

	for _, rule := range role.Rules {
		if !slices.Contains(rule.APIGroups, "agent-install.openshift.io") || !slices.Contains(rule.Resources, "agents") {
			continue
		}
		for _, verb := range []string{"delete", "get", "list", "patch", "watch"} {
			if !slices.Contains(rule.Verbs, verb) {
				t.Errorf("manager ClusterRole lacks %s on agents.agent-install.openshift.io", verb)
			}
		}
		return
	}
	t.Fatal("manager ClusterRole lacks agents.agent-install.openshift.io rule")
}
