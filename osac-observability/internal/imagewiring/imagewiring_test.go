// Copyright (c) 2026 Red Hat Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
// in compliance with the License. You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0

// Package imagewiring guards the two things that made the helper image
// undeployable before the release pipeline built it: the image had no build
// target, and the release stamping had no case for it. Either omission is
// invisible to a render assertion -- the chart renders a metricsAuth tag
// either way -- so these tests exercise the build and stamping behaviour
// itself.
package imagewiring

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const sampleValues = `global:
  observability:
    services:
      metricsAuth:
        # Builds the remote-write and query-allowlist helpers.
        repository: ghcr.io/osac-project/osac-observability
        # Placeholder; overwritten at release time.
        tag: ""
      oauth2Proxy:
        repository: quay.io/oauth2-proxy/oauth2-proxy
        # Placeholder; overwritten at release time.
        tag: ""
      openshiftOauthProxy:
        repository: registry.redhat.io/openshift4/ose-oauth-proxy
        tag: ""
`

// repoRoot walks up from this test file until it finds the go.work file the
// monorepo root carries, so the tests do not depend on the working directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test file")
	}
	dir := filepath.Dir(file)
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatalf("no go.work found above %s", filepath.Dir(file))
	return ""
}

func run(t *testing.T, dir string, name string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// flatten joins make's backslash-continued recipe lines into one line so the
// assertions can look at the actual command it would run.
func flatten(s string) string {
	s = strings.ReplaceAll(s, "\\\n", " ")
	return strings.Join(strings.Fields(s), " ")
}

func TestImageBuildUsesTheComponentContainerfileAndRepoRootContext(t *testing.T) {
	repo := repoRoot(t)
	img := "ghcr.io/osac-project/osac-observability:test"

	out, err := run(t, repo, "make", "-n", "-C", "osac-observability", "image-build", "IMG="+img, "VERSION=1.2.3")
	if err != nil {
		t.Fatalf("make image-build failed: %v\n%s", err, out)
	}
	cmd := flatten(out)
	for _, want := range []string{"-f Containerfile", "-t " + img, "--build-arg VERSION=1.2.3"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("image-build command %q is missing %q", cmd, want)
		}
	}
	// The Containerfile copies go.work and the sibling modules, so the build
	// context must be the monorepo root (osac-observability/..), not this
	// component directory.
	if !strings.HasSuffix(cmd, " ..") {
		t.Errorf("image-build command %q must build with the repo root as context", cmd)
	}
}

func TestImagePushPushesTheGivenImage(t *testing.T) {
	repo := repoRoot(t)
	img := "ghcr.io/osac-project/osac-observability:test"

	out, err := run(t, repo, "make", "-n", "-C", "osac-observability", "image-push", "IMG="+img)
	if err != nil {
		t.Fatalf("make image-push failed: %v\n%s", err, out)
	}
	if cmd := flatten(out); !strings.HasSuffix(cmd, "push "+img) {
		t.Errorf("image-push command %q does not push %s", cmd, img)
	}
}

// TestStampComponentImageRefsStampsOnlyMetricsAuth reproduces the release
// step that fills the tag into the umbrella values. Before this component had
// a stamping case the function hard-errored on it, so the release shipped a
// chart whose metricsAuth tag was still the empty placeholder.
func TestStampComponentImageRefsStampsOnlyMetricsAuth(t *testing.T) {
	repo := repoRoot(t)
	values := filepath.Join(t.TempDir(), "values.yaml")
	if err := os.WriteFile(values, []byte(sampleValues), 0o644); err != nil {
		t.Fatal(err)
	}

	script := filepath.Join(repo, "osac-installer", "scripts", "nightly-charts.sh")
	out, err := run(t, repo, "bash", "-c",
		`set -euo pipefail; source "$1"; stamp_component_image_refs osac-observability "$2" 1.2.3`,
		"_", script, values)
	if err != nil {
		t.Fatalf("stamp_component_image_refs osac-observability failed: %v\n%s", err, out)
	}

	raw, err := os.ReadFile(values)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Global struct {
			Observability struct {
				Services map[string]struct {
					Tag string `yaml:"tag"`
				} `yaml:"services"`
			} `yaml:"observability"`
		} `yaml:"global"`
	}
	if err := yaml.Unmarshal(raw, &got); err != nil {
		t.Fatalf("stamped values are not valid YAML: %v\n%s", err, raw)
	}
	services := got.Global.Observability.Services
	if tag := services["metricsAuth"].Tag; tag != "1.2.3" {
		t.Errorf("metricsAuth tag = %q, want 1.2.3", tag)
	}
	// The third-party proxies keep their pinned upstream tags; only OSAC's
	// own helper image is stamped.
	for _, name := range []string{"oauth2Proxy", "openshiftOauthProxy"} {
		if tag := services[name].Tag; tag != "" {
			t.Errorf("%s tag = %q, want it left untouched", name, tag)
		}
	}
	// awk is used instead of yq -i precisely to preserve formatting and
	// comments; yq would strip them and break ct-lint's yamllint.
	if !strings.Contains(string(raw), "# Placeholder; overwritten at release time.") {
		t.Error("stamping dropped comments from the umbrella values")
	}
}

// TestStampComponentImageRefsFailsWhenThePathIsMissing keeps a future chart
// rename from silently shipping an empty tag: the release must fail loudly.
func TestStampComponentImageRefsFailsWhenThePathIsMissing(t *testing.T) {
	repo := repoRoot(t)
	values := filepath.Join(t.TempDir(), "values.yaml")
	if err := os.WriteFile(values, []byte("global:\n  other: value\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	script := filepath.Join(repo, "osac-installer", "scripts", "nightly-charts.sh")
	out, err := run(t, repo, "bash", "-c",
		`set -euo pipefail; source "$1"; stamp_component_image_refs osac-observability "$2" 1.2.3`,
		"_", script, values)
	if err == nil {
		t.Fatalf("stamping succeeded with no metricsAuth path present; output: %s", out)
	}
	if !strings.Contains(out, "metricsAuth.tag not found") {
		t.Errorf("unexpected failure output: %s", out)
	}
}
