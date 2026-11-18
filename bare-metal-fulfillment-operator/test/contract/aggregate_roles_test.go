package contract

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

var helmDirective = regexp.MustCompile(`\{\{.*?\}\}`)

type aggregateRoleDocument struct {
	Kind     string `json:"kind"`
	Metadata struct {
		Name   string            `json:"name"`
		Labels map[string]string `json:"labels"`
	} `json:"metadata"`
	Rules []struct {
		APIGroups []string `json:"apiGroups"`
		Resources []string `json:"resources"`
		Verbs     []string `json:"verbs"`
	} `json:"rules"`
}

func TestBareMetalPoolAdminAggregateRoleUsesExplicitVerbs(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve aggregate role test path")
	}
	path := filepath.Join(filepath.Dir(source), "..", "..", "charts", "operator", "templates", "aggregate-roles.yaml")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read aggregate roles template: %v", err)
	}
	cleaned := helmDirective.ReplaceAll(content, nil)
	wantVerbs := []string{"create", "delete", "get", "list", "patch", "update", "watch"}
	sort.Strings(wantVerbs)

	found := false
	for _, document := range bytes.Split(cleaned, []byte("\n---\n")) {
		var role aggregateRoleDocument
		if err := yaml.Unmarshal(document, &role); err != nil {
			t.Fatalf("parse aggregate role document: %v", err)
		}
		if role.Kind != "ClusterRole" || !strings.HasSuffix(role.Metadata.Name, "-baremetalpool-admin-role") {
			continue
		}
		found = true
		if role.Metadata.Labels["rbac.authorization.k8s.io/aggregate-to-admin"] != "true" {
			t.Fatal("BareMetalPool admin role must aggregate to the built-in admin role")
		}

		var gotVerbs []string
		for _, rule := range role.Rules {
			if contains(rule.APIGroups, "osac.openshift.io") && contains(rule.Resources, "baremetalpools") {
				gotVerbs = append(gotVerbs, rule.Verbs...)
			}
		}
		sort.Strings(gotVerbs)
		if !reflect.DeepEqual(gotVerbs, wantVerbs) {
			t.Fatalf("BareMetalPool admin verbs = %v, want %v", gotVerbs, wantVerbs)
		}
	}
	if !found {
		t.Fatal("BareMetalPool admin aggregate ClusterRole was not found")
	}
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
