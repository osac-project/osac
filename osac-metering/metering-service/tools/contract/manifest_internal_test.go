/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestCheckPublishedManifestInTheRepository verifies the committed manifest against the
// published files this checkout carries.
func TestCheckPublishedManifestInTheRepository(t *testing.T) {
	if err := checkPublishedManifest(repositoryRoot(t)); err != nil {
		t.Fatalf("the committed manifest does not match the published files: %v", err)
	}
}

// TestCheckPublishedManifestRejectsAnAddedField changes the bytes of a published file while
// the manifest keeps the old hash.
func TestCheckPublishedManifestRejectsAnAddedField(t *testing.T) {
	v1 := repositoryVersion(t, "v1.json")
	root := manifestFixture(t, map[string]string{"v1.json": v1})
	writeVersion(t, root, "v1.json", addDataField(t, v1, "probe_field"))
	if err := checkPublishedManifest(root); err == nil ||
		!strings.Contains(err.Error(), "changed") {
		t.Fatalf("adding a field to a published file must fail the manifest, got %v", err)
	}
}

// TestCheckPublishedManifestRejectsARemovedField is the other direction of the same rule.
func TestCheckPublishedManifestRejectsARemovedField(t *testing.T) {
	v1 := repositoryVersion(t, "v1.json")
	root := manifestFixture(t, map[string]string{"v1.json": v1})
	writeVersion(t, root, "v1.json", dropDataField(t, v1, "rules_revision"))
	if err := checkPublishedManifest(root); err == nil ||
		!strings.Contains(err.Error(), "changed") {
		t.Fatalf("removing a field from a published file must fail the manifest, got %v", err)
	}
}

// TestCheckPublishedManifestAcceptsANewVersionWithAnEntry is the allowed change: a new file
// plus its explicit manifest line. Without the line the new file fails.
func TestCheckPublishedManifestAcceptsANewVersionWithAnEntry(t *testing.T) {
	v1 := repositoryVersion(t, "v1.json")
	root := manifestFixture(t, map[string]string{"v1.json": v1, "v2.json": "{}\n"})
	if err := checkPublishedManifest(root); err != nil {
		t.Fatalf("a new version with its own manifest entry must pass: %v", err)
	}
	writeManifest(t, root, map[string]string{"v1.json": v1})
	if err := checkPublishedManifest(root); err == nil ||
		!strings.Contains(err.Error(), "has no entry") {
		t.Fatalf("a new version without an entry must fail, got %v", err)
	}
}

func TestCheckPublishedManifestRejectsAMalformedLine(t *testing.T) {
	v1 := repositoryVersion(t, "v1.json")
	root := manifestFixture(t, map[string]string{"v1.json": v1})
	writeManifestText(t, root, "not a manifest line\n")
	if err := checkPublishedManifest(root); err == nil ||
		!strings.Contains(err.Error(), "is not") {
		t.Fatalf("a malformed manifest line must fail, got %v", err)
	}
}

func TestCheckPublishedManifestRejectsADuplicateEntry(t *testing.T) {
	v1 := repositoryVersion(t, "v1.json")
	root := manifestFixture(t, map[string]string{"v1.json": v1})
	line := manifestLine(v1, "v1.json")
	writeManifestText(t, root, line+line)
	if err := checkPublishedManifest(root); err == nil ||
		!strings.Contains(err.Error(), "more than once") {
		t.Fatalf("a duplicate manifest entry must fail, got %v", err)
	}
}

func TestCheckPublishedManifestRejectsAnEntryForAMissingFile(t *testing.T) {
	v1 := repositoryVersion(t, "v1.json")
	root := manifestFixture(t, map[string]string{"v1.json": v1})
	manifest := manifestLine(v1, "v1.json") +
		"0000000000000000000000000000000000000000000000000000000000000000  v2.json\n"
	writeManifestText(t, root, manifest)
	if err := checkPublishedManifest(root); err == nil ||
		!strings.Contains(err.Error(), "which no published schema file provides") {
		t.Fatalf("an entry for a missing file must fail, got %v", err)
	}
}

// repoVersion reads one published version file from this checkout.
func repositoryVersion(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(repositoryRoot(t), versionsDirectory, name))
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(content)
}

// manifestFixture writes the versions and a matching manifest into a temporary repository
// root.
func manifestFixture(t *testing.T, versions map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range versions {
		writeVersion(t, root, name, content)
	}
	writeManifest(t, root, versions)
	return root
}

func writeVersion(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, versionsDirectory, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

func writeManifest(t *testing.T, root string, versions map[string]string) {
	t.Helper()
	names := make([]string, 0, len(versions))
	for name := range versions {
		names = append(names, name)
	}
	sort.Strings(names)
	var manifest strings.Builder
	for _, name := range names {
		manifest.WriteString(manifestLine(versions[name], name))
	}
	writeManifestText(t, root, manifest.String())
}

func writeManifestText(t *testing.T, root, content string) {
	t.Helper()
	path := filepath.Join(root, schemaDirectory, manifestName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// manifestLine renders one manifest entry.
func manifestLine(content, name string) string {
	digest := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%s  %s\n", hex.EncodeToString(digest[:]), name)
}

// addDataField appends an optional payload field to a version document.
func addDataField(t *testing.T, content, name string) string {
	t.Helper()
	document := versionDocument(t, content)
	document["data"] = append(document["data"].([]any), map[string]any{
		"name": name, "type": "string", "required": false,
		"description": "Added by the next version.", "unit": "",
	})
	return encodeVersionDocument(t, document)
}

// dropDataField removes a payload field from a version document.
func dropDataField(t *testing.T, content, name string) string {
	t.Helper()
	document := versionDocument(t, content)
	fields := document["data"].([]any)
	kept := make([]any, 0, len(fields))
	for _, field := range fields {
		if field.(map[string]any)["name"] != name {
			kept = append(kept, field)
		}
	}
	document["data"] = kept
	return encodeVersionDocument(t, document)
}

func versionDocument(t *testing.T, content string) map[string]any {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal([]byte(content), &document); err != nil {
		t.Fatalf("decoding the version document: %v", err)
	}
	return document
}

func encodeVersionDocument(t *testing.T, document map[string]any) string {
	t.Helper()
	content, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encoding the version document: %v", err)
	}
	return string(content)
}
