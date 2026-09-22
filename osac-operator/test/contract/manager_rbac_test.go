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
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestManagerRoleGrantsLogicalVolumeLifecycle(t *testing.T) {
	rolePath := filepath.Join(repoRoot(), "osac-operator/config/rbac/role.yaml")
	role := loadClusterRoleFile(t, rolePath)
	got, found := clusterRoleRuleVerbs(role, "topolvm.io", []string{"logicalvolumes"})
	if !found {
		t.Fatalf("ClusterRole has no rule for apiGroup=%q resources=%v", "topolvm.io", []string{"logicalvolumes"})
	}
	want := []string{"create", "delete", "get", "list", "watch"}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ClusterRole rule for apiGroup=%q resources=%v has verbs %v, want exactly %v",
			"topolvm.io", []string{"logicalvolumes"}, got, want)
	}
}
