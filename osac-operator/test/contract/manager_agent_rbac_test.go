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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"gopkg.in/yaml.v3"
)

var _ = It("grants the manager ClusterRole the permissions required for worker lifecycle", func() {
	path := filepath.Join(repoRoot(), "osac-operator/charts/operator/templates/clusterrole.yaml")
	raw, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred())
	var role clusterRole
	Expect(yaml.Unmarshal(templateDirectiveRe.ReplaceAll(raw, nil), &role)).To(Succeed())
	Expect(role.Kind).To(Equal("ClusterRole"), "expected manager ClusterRole, got %q", role.Kind)

	tests := []struct {
		group    string
		resource string
		verbs    []string
	}{
		{group: "agent-install.openshift.io", resource: "agents", verbs: []string{"delete", "get", "list", "patch", "watch"}},
		{
			group: "agent-install.openshift.io", resource: "infraenvs",
			verbs: []string{"create", "delete", "get", "list", "patch", "update", "watch"},
		},
		{group: "", resource: "secrets", verbs: []string{"create", "get"}},
	}
	for _, tt := range tests {
		By(tt.resource)
		func() {
			for _, verb := range tt.verbs {
				allowed := false
				for _, rule := range role.Rules {
					if slices.Contains(rule.APIGroups, tt.group) && slices.Contains(rule.Resources, tt.resource) &&
						slices.Contains(rule.Verbs, verb) {
						allowed = true
						break
					}
				}
				Expect(allowed).To(BeTrue(), "manager ClusterRole lacks %s on %s.%s", verb, tt.resource, tt.group)
			}
		}()
	}
})
