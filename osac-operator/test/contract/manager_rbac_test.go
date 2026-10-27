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

var (
	logicalVolumeVerbs  = []string{"create", "delete", "get", "list", "watch"}
	storageClassROVerbs = []string{"get", "list", "watch"}
)

// TestManagerRoleGrantsLogicalVolumeLifecycle verifies that the kustomize
// ClusterRole grants LogicalVolume lifecycle verbs.
func TestManagerRoleGrantsLogicalVolumeLifecycle(t *testing.T) {
	rolePath := filepath.Join(repoRoot(), "osac-operator/config/rbac/role.yaml")
	role := loadClusterRoleFile(t, rolePath)
	assertClusterRoleRuleVerbs(t, role, rolePath, "topolvm.io", []string{"logicalvolumes"}, logicalVolumeVerbs)
}

// TestManagerHelmClusterRoleGrantsLogicalVolumeLifecycle verifies that the Helm
// ClusterRole grants LogicalVolume lifecycle verbs.
func TestManagerHelmClusterRoleGrantsLogicalVolumeLifecycle(t *testing.T) {
	rolePath := filepath.Join(repoRoot(), "osac-operator/charts/operator/templates/clusterrole.yaml")
	role := loadHelmClusterRoleTemplate(t, rolePath)
	assertClusterRoleRuleVerbs(t, role, rolePath, "topolvm.io", []string{"logicalvolumes"}, logicalVolumeVerbs)
}

// TestManagerHelmClusterRoleGrantsAgentCleanup verifies that the Helm
// ClusterRole grants Agent cleanup verbs.
func TestManagerHelmClusterRoleGrantsAgentCleanup(t *testing.T) {
	rolePath := filepath.Join(repoRoot(), "osac-operator/charts/operator/templates/clusterrole.yaml")
	role := loadHelmClusterRoleTemplate(t, rolePath)
	assertClusterRoleRuleVerbs(
		t, role, rolePath,
		"agent-install.openshift.io", []string{"agents"},
		[]string{"get", "list", "patch", "update", "watch"},
	)
}

// TestManagerRoleGrantsStorageClassReadOnly verifies that the kustomize
// ClusterRole grants read-only StorageClass verbs.
func TestManagerRoleGrantsStorageClassReadOnly(t *testing.T) {
	rolePath := filepath.Join(repoRoot(), "osac-operator/config/rbac/role.yaml")
	role := loadClusterRoleFile(t, rolePath)
	assertClusterRoleRuleVerbs(t, role, rolePath, "storage.k8s.io", []string{"storageclasses"}, storageClassROVerbs)
}

// TestManagerHelmClusterRoleGrantsStorageClassReadOnly verifies that the Helm
// ClusterRole grants read-only StorageClass verbs.
func TestManagerHelmClusterRoleGrantsStorageClassReadOnly(t *testing.T) {
	rolePath := filepath.Join(repoRoot(), "osac-operator/charts/operator/templates/clusterrole.yaml")
	role := loadHelmClusterRoleTemplate(t, rolePath)
	assertClusterRoleRuleVerbs(t, role, rolePath, "storage.k8s.io", []string{"storageclasses"}, storageClassROVerbs)
}

// assertClusterRoleRuleVerbs checks that role contains a rule for apiGroup and
// resources with the exact wantVerbs set.
func assertClusterRoleRuleVerbs(
	t *testing.T, role clusterRole, rolePath, apiGroup string, resources, wantVerbs []string,
) {
	t.Helper()
	got, found := clusterRoleRuleVerbs(role, apiGroup, resources)
	if !found {
		t.Fatalf(
			"ClusterRole at %s has no rule for apiGroup=%q resources=%v",
			rolePath, apiGroup, resources,
		)
	}
	want := append([]string(nil), wantVerbs...)
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ClusterRole at %s rule for apiGroup=%q resources=%v has verbs %v, want exactly %v",
			rolePath, apiGroup, resources, got, want)
	}
}
