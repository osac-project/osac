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
	serviceWatchVerbs   = []string{"get", "list", "watch"}
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
		[]string{"delete", "get", "list", "patch", "update", "watch"},
	)
}

// TestManagerHelmClusterRoleWatchesServices verifies that the ClusterOrder
// Service informer has the list/watch verbs required by controller-runtime.
func TestManagerHelmClusterRoleWatchesServices(t *testing.T) {
	rolePath := filepath.Join(repoRoot(), "osac-operator/charts/operator/templates/clusterrole.yaml")
	role := loadHelmClusterRoleTemplate(t, rolePath)
	assertClusterRoleRuleVerbs(t, role, rolePath, "", []string{"services"}, serviceWatchVerbs)
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

func TestManagerDoesNotDeleteServiceAccountsOrRoleBindings(t *testing.T) {
	targets := []struct {
		path string
		helm bool
	}{
		{path: filepath.Join(repoRoot(), "osac-operator/config/rbac/role.yaml")},
		{path: filepath.Join(repoRoot(), "osac-operator/charts/operator/templates/clusterrole.yaml"), helm: true},
	}
	want := []string{"create", "get", "list", "patch", "update", "watch"}
	sort.Strings(want)

	for _, target := range targets {
		var role clusterRole
		if target.helm {
			role = loadHelmClusterRoleTemplate(t, target.path)
		} else {
			role = loadClusterRoleFile(t, target.path)
		}

		for _, rule := range []struct {
			apiGroup string
			resource string
		}{
			{apiGroup: "", resource: "serviceaccounts"},
			{apiGroup: "rbac.authorization.k8s.io", resource: "rolebindings"},
		} {
			got, found := clusterRoleRuleVerbs(role, rule.apiGroup, rule.resource)
			if !found {
				t.Fatalf("ClusterRole at %s has no rule for apiGroup=%q resource=%q",
					target.path, rule.apiGroup, rule.resource)
			}
			sort.Strings(got)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("ClusterRole at %s grants %v on %s.%s, want %v",
					target.path, got, rule.apiGroup, rule.resource, want)
			}
			if clusterRoleGrantsAnyName(role, rule.apiGroup, rule.resource, "delete") {
				t.Errorf("ClusterRole at %s grants delete on %s.%s", target.path, rule.apiGroup, rule.resource)
			}
		}
	}
}

func TestManagerCanBindOnlyTheAdminClusterRole(t *testing.T) {
	targets := []struct {
		path string
		helm bool
	}{
		{path: filepath.Join(repoRoot(), "osac-operator/config/rbac/role.yaml")},
		{path: filepath.Join(repoRoot(), "osac-operator/charts/operator/templates/clusterrole.yaml"), helm: true},
	}
	want := policyRule{
		APIGroups:     []string{"rbac.authorization.k8s.io"},
		Resources:     []string{"clusterroles"},
		ResourceNames: []string{"admin"},
		Verbs:         []string{"bind"},
	}

	for _, target := range targets {
		var role clusterRole
		if target.helm {
			role = loadHelmClusterRoleTemplate(t, target.path)
		} else {
			role = loadClusterRoleFile(t, target.path)
		}

		var bindRules []policyRule
		for _, rule := range role.Rules {
			if clusterRoleRuleGrantsAnyName(rule, "rbac.authorization.k8s.io", "clusterroles", "bind") {
				bindRules = append(bindRules, rule)
			}
		}
		if !reflect.DeepEqual(bindRules, []policyRule{want}) {
			t.Errorf("ClusterRole at %s grants bind via %v, want only %v", target.path, bindRules, want)
		}
	}
}

func TestManagerCannotCreateOrDeleteClusterOrders(t *testing.T) {
	targets := []struct {
		path string
		helm bool
	}{
		{path: filepath.Join(repoRoot(), "osac-operator/config/rbac/role.yaml")},
		{path: filepath.Join(repoRoot(), "osac-operator/charts/operator/templates/clusterrole.yaml"), helm: true},
	}
	want := []string{"get", "list", "patch", "update", "watch"}

	for _, target := range targets {
		var role clusterRole
		if target.helm {
			role = loadHelmClusterRoleTemplate(t, target.path)
		} else {
			role = loadClusterRoleFile(t, target.path)
		}
		verbs, found := clusterRoleRuleVerbs(role, "osac.openshift.io", "clusterorders")
		sort.Strings(verbs)
		if !found || !reflect.DeepEqual(verbs, want) {
			t.Errorf("ClusterRole at %s grants ClusterOrder verbs %v, found=%t; want %v",
				target.path, verbs, found, want)
		}
		if clusterRoleGrantsAnyName(role, "osac.openshift.io", "clusterorders", "create") ||
			clusterRoleGrantsAnyName(role, "osac.openshift.io", "clusterorders", "delete") {
			t.Errorf("ClusterRole at %s can create or delete ClusterOrders", target.path)
		}
	}
}

func TestClusterRoleRuleResourceNamesAreMatchedPrecisely(t *testing.T) {
	rule := policyRule{
		APIGroups:     []string{""},
		Resources:     []string{"secrets"},
		ResourceNames: []string{"bootstrap"},
		Verbs:         []string{"create", "get"},
	}
	role := clusterRole{Rules: []policyRule{rule}}

	if verbs, found := clusterRoleRuleVerbs(role, "", "secrets"); found || len(verbs) != 0 {
		t.Fatalf("name-restricted rule granted broad verbs %v, found=%t", verbs, found)
	}
	if !clusterRoleRuleMatchesForName(rule, "", "secrets", "bootstrap") {
		t.Fatal("rule did not match its named Secret")
	}
	if clusterRoleRuleMatchesForName(rule, "", "secrets", "other") {
		t.Fatal("rule matched a different Secret name")
	}
	if clusterRoleRuleGrantsForName(rule, "", "secrets", "bootstrap", "create") {
		t.Fatal("resourceNames-restricted create was treated as a grant")
	}
	if !clusterRoleRuleGrantsForName(rule, "", "secrets", "bootstrap", "get") {
		t.Fatal("rule did not grant get for its named Secret")
	}
	if !clusterRoleRuleGrantsAnyName(rule, "", "secrets", "get") {
		t.Fatal("name-restricted get was not detected for an any-name permission check")
	}
	if clusterRoleRuleGrantsAnyName(rule, "", "secrets", "create") {
		t.Fatal("resourceNames-restricted create was treated as a grant")
	}
}

func TestClusterRoleRuleVerbsIncludesWildcardRules(t *testing.T) {
	role := clusterRole{Rules: []policyRule{
		{
			APIGroups: []string{"rbac.authorization.k8s.io"},
			Resources: []string{"rolebindings"},
			Verbs:     []string{"create"},
		},
		{
			APIGroups: []string{"*"},
			Resources: []string{"*"},
			Verbs:     []string{"delete"},
		},
		{
			APIGroups: []string{"rbac.authorization.k8s.io"},
			Resources: []string{"rolebindings"},
			Verbs:     []string{"*"},
		},
	}}

	got, found := clusterRoleRuleVerbs(role, "rbac.authorization.k8s.io", "rolebindings")
	want := []string{"*", "create", "delete"}
	sort.Strings(want)
	if !found || !reflect.DeepEqual(got, want) {
		t.Fatalf("wildcard role rules produced verbs %v, found=%t; want %v", got, found, want)
	}
}

func TestClusterRoleRuleVerbsDoesNotMatchAnotherResource(t *testing.T) {
	role := clusterRole{Rules: []policyRule{{
		APIGroups: []string{""},
		Resources: []string{"pods"},
		Verbs:     []string{"get"},
	}}}
	if verbs, found := clusterRoleRuleVerbs(role, "", "volumes"); found {
		t.Fatalf("pod rule granted verbs %v on volumes", verbs)
	}
}

// assertClusterRoleRuleVerbs checks the combined verbs for each requested
// resource independently.
func assertClusterRoleRuleVerbs(
	t *testing.T, role clusterRole, rolePath, apiGroup string, resources, wantVerbs []string,
) {
	t.Helper()
	want := append([]string(nil), wantVerbs...)
	sort.Strings(want)
	for _, resource := range resources {
		got, found := clusterRoleRuleVerbs(role, apiGroup, resource)
		if !found {
			t.Fatalf(
				"ClusterRole at %s has no rule for apiGroup=%q resource=%q",
				rolePath, apiGroup, resource,
			)
		}
		sort.Strings(got)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("ClusterRole at %s rule for apiGroup=%q resource=%q has verbs %v, want exactly %v",
				rolePath, apiGroup, resource, got, want)
		}
	}
}
