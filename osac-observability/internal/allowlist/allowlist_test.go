// Copyright (c) 2026 Red Hat Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
// in compliance with the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0

package allowlist

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "endpoints.txt")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write allowlist: %v", err)
	}
	return path
}

func TestLoadMissingFileUsesTheDefaultEndpoints(t *testing.T) {
	matcher, err := Load(filepath.Join(t.TempDir(), "does-not-exist.txt"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, path := range []string{
		"/api/v1/query",
		"/api/v1/query_range",
		"/api/v1/series",
		"/api/v1/labels",
		"/api/v1/label/__name__/values",
	} {
		if !matcher.Allowed(http.MethodGet, path) {
			t.Errorf("GET %s should be allowed by the default list", path)
		}
	}
	if matcher.Allowed(http.MethodGet, "/api/v1/targets") {
		t.Error("the default list must not allow an endpoint outside the five")
	}
}

func TestLoadRejectsAnEmptyFile(t *testing.T) {
	path := writeFile(t, "\n# only comments\n\n")
	if _, err := Load(path); err == nil {
		t.Fatal("Load should fail closed on a file with no endpoint")
	}
}

func TestLoadReadsSpecifiersAndIgnoresCommentsAndBlanks(t *testing.T) {
	path := writeFile(t, "# platform endpoints\n/api/v1/query\n\n/api/v1/labels\n")
	matcher, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !matcher.Allowed(http.MethodGet, "/api/v1/query") {
		t.Error("GET /api/v1/query should be allowed")
	}
	if !matcher.Allowed(http.MethodGet, "/api/v1/labels") {
		t.Error("GET /api/v1/labels should be allowed")
	}
	if matcher.Allowed(http.MethodGet, "/api/v1/series") {
		t.Error("a file replaces the default list, so /api/v1/series must be denied")
	}
}

func TestLoadRejectsACombinedMethodAndPath(t *testing.T) {
	path := writeFile(t, "GET /api/v1/query\n")
	if _, err := Load(path); err == nil {
		t.Fatal("Load should reject a specifier that is not a bare path")
	}
}

func TestLoadRejectsAnUnsupportedPlaceholder(t *testing.T) {
	path := writeFile(t, "/api/v1/label/<id>/values\n")
	if _, err := Load(path); err == nil {
		t.Fatal("Load should reject an unknown placeholder")
	}
}

func TestLoadReplacesTheNamePlaceholder(t *testing.T) {
	path := writeFile(t, "/api/v1/label/<name>/values\n")
	matcher, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, candidate := range []string{
		"/api/v1/label/__name__/values",
		"/api/v1/label/instance/values",
	} {
		if !matcher.Allowed(http.MethodGet, candidate) {
			t.Errorf("GET %s should be allowed", candidate)
		}
	}
	for _, candidate := range []string{
		"/api/v1/label/__name__/values/extra",
		"/api/v1/label//values",
		"/api/v1/label/a/b/values",
	} {
		if matcher.Allowed(http.MethodGet, candidate) {
			t.Errorf("GET %s should be denied", candidate)
		}
	}
}

func TestMatcherAllowsOnlyTheDocumentedEndpoints(t *testing.T) {
	matcher := Default()
	cases := []struct {
		path  string
		allow bool
	}{
		{"/api/v1/query", true},
		{"/api/v1/query_range", true},
		{"/api/v1/series", true},
		{"/api/v1/labels", true},
		{"/api/v1/label/__name__/values", true},
		{"/api/v1/label/instance/values", true},
		{"/api/v1/targets", false},
		{"/api/v1/rules", false},
		{"/api/v1/alerts", false},
		{"/api/v1/status/buildinfo", false},
		{"/api/v1/admin/tsdb/delete_series", false},
		{"/api/v1/stores", false},
		{"/api/v1/metadata", false},
		{"/", false},
		{"/-/ready", false},
		{"/-/healthy", false},
		{"/api/v1/label/__name__/values/extra", false},
		{"/api/v1/query/extra", false},
		{"/api/v1/queryseries", false},
		{"/API/v1/query", false},
	}
	for _, tc := range cases {
		if got := matcher.Allowed(http.MethodGet, tc.path); got != tc.allow {
			t.Errorf("GET %s allowed = %t, want %t", tc.path, got, tc.allow)
		}
	}
}

func TestMatcherRejectsNonGetAndPostMethods(t *testing.T) {
	matcher := Default()
	for _, method := range []string{
		http.MethodGet,
		http.MethodPost,
	} {
		if !matcher.Allowed(method, "/api/v1/query") {
			t.Errorf("%s /api/v1/query should be allowed", method)
		}
	}
	for _, method := range []string{
		http.MethodPut,
		http.MethodDelete,
		http.MethodPatch,
		http.MethodHead,
		http.MethodOptions,
		strings.ToLower(http.MethodGet),
	} {
		if matcher.Allowed(method, "/api/v1/query") {
			t.Errorf("%s /api/v1/query should be denied", method)
		}
	}
}

func TestDefaultListsExactlyFiveEndpoints(t *testing.T) {
	if got := len(Default().Specifiers()); got != 5 {
		t.Fatalf("Default() has %d endpoints, want 5: %v", got, Default().Specifiers())
	}
	path := writeFile(t, DefaultSpecifiers)
	matcher, err := Load(path)
	if err != nil {
		t.Fatalf("Load(DefaultSpecifiers): %v", err)
	}
	if len(matcher.Specifiers()) != 5 {
		t.Errorf("the ConfigMap content should carry the same five endpoints")
	}
}
