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
	"strings"
	"testing"
)

func TestOperatorAdminAggregateRolesUseExplicitVerbs(t *testing.T) {
	path := filepath.Join(repoRoot(), "osac-operator/charts/operator/templates/aggregate-roles.yaml")
	documents := loadHelmRBACDocuments(t, path)
	wantVerbs := []string{"create", "delete", "get", "list", "patch", "update", "watch"}
	sort.Strings(wantVerbs)

	for _, expectation := range []struct {
		roleSuffix string
		resource   string
	}{
		{roleSuffix: "-computeinstance-admin", resource: "computeinstances"},
		{roleSuffix: "-tenant-admin", resource: "tenants"},
	} {
		t.Run(expectation.roleSuffix, func(t *testing.T) {
			for _, document := range documents {
				if document.Kind != "ClusterRole" || !strings.HasSuffix(document.Metadata.Name, expectation.roleSuffix) {
					continue
				}
				got, found := clusterRoleRuleVerbs(clusterRole{Rules: document.Rules},
					"osac.openshift.io", expectation.resource)
				if !found {
					t.Fatalf("role %q has no rule for %s", document.Metadata.Name, expectation.resource)
				}
				sort.Strings(got)
				if !reflect.DeepEqual(got, wantVerbs) {
					t.Errorf("role %q grants %v on %s, want %v",
						document.Metadata.Name, got, expectation.resource, wantVerbs)
				}
				return
			}
			t.Fatalf("aggregate role ending in %q was not found", expectation.roleSuffix)
		})
	}
}

func TestOperatorAggregateRolesDoNotBindManagerToPredefinedAdmin(t *testing.T) {
	path := filepath.Join(repoRoot(), "osac-operator/charts/operator/templates/aggregate-roles.yaml")
	for _, document := range loadHelmRBACDocuments(t, path) {
		if document.Kind == "ClusterRoleBinding" && document.RoleRef.Name == "admin" {
			t.Fatalf("aggregate roles template binds a service account to predefined ClusterRole %q", document.RoleRef.Name)
		}
	}
}

func TestConsoleProxyUsesSAROnlyAndNamespaceScopedSecretReader(t *testing.T) {
	path := filepath.Join(repoRoot(), "osac-operator/charts/operator/templates/console-proxy/rbac.yaml")
	const roleSuffix = "-console-proxy-subjectaccessreview"
	var roleFound bool
	var bindingFound bool
	var secretReaderRoleFound bool
	var secretReaderBindingFound bool

	for _, document := range loadHelmRBACDocuments(t, path) {
		if document.Kind == "ClusterRole" && strings.HasSuffix(document.Metadata.Name, roleSuffix) {
			roleFound = true
			want := []policyRule{{
				APIGroups: []string{"authorization.k8s.io"},
				Resources: []string{"subjectaccessreviews"},
				Verbs:     []string{"create"},
			}}
			if !reflect.DeepEqual(document.Rules, want) {
				t.Errorf("console-proxy authorization role grants %v, want only SubjectAccessReview create", document.Rules)
			}
		}
		if document.Kind == "ClusterRoleBinding" && strings.HasSuffix(document.RoleRef.Name, roleSuffix) {
			bindingFound = true
			if len(document.Subjects) != 1 || document.Subjects[0].Kind != "ServiceAccount" ||
				!strings.HasSuffix(document.Subjects[0].Name, "-console-proxy") {
				t.Errorf("console-proxy auth binding has unexpected subjects: %+v", document.Subjects)
			}
		}
		if document.Kind == "ClusterRoleBinding" && document.RoleRef.Name == "system:auth-delegator" {
			t.Error("console-proxy must not use the predefined system:auth-delegator ClusterRole")
		}
		if document.Kind == "ClusterRole" && strings.HasSuffix(document.Metadata.Name, "-console-proxy-secret-reader") {
			secretReaderRoleFound = true
			want := []policyRule{{
				APIGroups: []string{""},
				Resources: []string{"secrets"},
				Verbs:     []string{"get", "list"},
			}}
			if !reflect.DeepEqual(document.Rules, want) {
				t.Errorf("console-proxy secret-reader ClusterRole grants %v, want Secret get/list", document.Rules)
			}
		}
		if document.Kind == "RoleBinding" && strings.HasSuffix(document.RoleRef.Name, "-console-proxy-secret-reader") {
			secretReaderBindingFound = true
			if document.RoleRef.Kind != "ClusterRole" {
				t.Errorf("console-proxy secret-reader binding references %q, want ClusterRole", document.RoleRef.Kind)
			}
			if document.Metadata.Namespace != "operator-release" {
				t.Errorf(
					"console-proxy secret-reader RoleBinding has namespace %q, want operator-release",
					document.Metadata.Namespace,
				)
			}
			if len(document.Subjects) != 1 || document.Subjects[0].Kind != "ServiceAccount" ||
				!strings.HasSuffix(document.Subjects[0].Name, "-console-proxy") ||
				document.Subjects[0].Namespace != "operator-release" {
				t.Errorf("console-proxy secret-reader binding has unexpected subjects: %+v", document.Subjects)
			}
		}
		if document.Kind == "ClusterRoleBinding" && strings.HasSuffix(document.RoleRef.Name, "-console-proxy-secret-reader") {
			t.Error("console-proxy secret-reader must not grant access across namespaces")
		}
	}
	if !roleFound {
		t.Errorf("console-proxy SubjectAccessReview ClusterRole ending in %q was not found", roleSuffix)
	}
	if !bindingFound {
		t.Errorf("console-proxy SubjectAccessReview ClusterRoleBinding ending in %q was not found", roleSuffix)
	}
	if !secretReaderRoleFound {
		t.Error("console-proxy secret-reader ClusterRole was not found")
	}
	if !secretReaderBindingFound {
		t.Error("console-proxy secret-reader RoleBinding was not found")
	}
}
