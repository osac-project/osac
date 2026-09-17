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
	"reflect"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestManagerRoleGrantsLogicalVolumeLifecycle(t *testing.T) {
	rolePath := filepath.Join(repoRoot(), "osac-operator/config/rbac/role.yaml")
	raw, err := os.ReadFile(rolePath)
	if err != nil {
		t.Fatalf("failed to read manager ClusterRole at %s: %v", rolePath, err)
	}

	var role clusterRole
	if err := yaml.Unmarshal(raw, &role); err != nil {
		t.Fatalf("failed to parse manager ClusterRole at %s: %v", rolePath, err)
	}

	wantVerbs := []string{"create", "delete", "get", "list", "watch"}
	sort.Strings(wantVerbs)
	for _, rule := range role.Rules {
		if !reflect.DeepEqual(rule.APIGroups, []string{"topolvm.io"}) ||
			!reflect.DeepEqual(rule.Resources, []string{"logicalvolumes"}) {
			continue
		}

		gotVerbs := append([]string(nil), rule.Verbs...)
		sort.Strings(gotVerbs)
		if !reflect.DeepEqual(gotVerbs, wantVerbs) {
			t.Fatalf("topolvm.io/logicalvolumes verbs = %v, want exactly %v", gotVerbs, wantVerbs)
		}
		return
	}

	t.Fatal("manager ClusterRole has no topolvm.io/logicalvolumes rule")
}
