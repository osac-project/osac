/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package schema

import (
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// publishedFile returns the bytes of a real published schema file, so a test changes one
// thing from a valid file rather than hand-writing the whole contract.
func publishedFile(t *testing.T, name string) string {
	t.Helper()
	content, err := fs.ReadFile(versionFiles, versionDirectory+"/"+name)
	if err != nil {
		t.Fatalf("reading published schema %s: %v", name, err)
	}
	return string(content)
}

// laterVersion returns version v2's file: the current file with its version, event type, and
// effective date moved forward.
func laterVersion(t *testing.T) string {
	t.Helper()
	content := strings.ReplaceAll(publishedFile(t, "v1.json"), "v1", "v2")
	content = strings.ReplaceAll(
		content, `"effective_at": "1970-01-01T00:00:00Z"`,
		`"effective_at": "2026-12-01T00:00:00Z"`,
	)
	return content
}

func TestLoadVersionsReadsPublishedFiles(t *testing.T) {
	versions, err := loadVersions(versionFiles)
	if err != nil {
		t.Fatalf("loading published schemas: %v", err)
	}
	if len(versions) != 1 || versions[0].Version != "v1" {
		t.Fatalf("want one version v1, got %v", versions)
	}
	if len(versions[0].SourceSHA256) != 64 {
		t.Fatalf("version v1 records hash %q", versions[0].SourceSHA256)
	}
}

func TestLoadVersionsRejectsReusedVersion(t *testing.T) {
	second := strings.ReplaceAll(
		publishedFile(t, "v1.json"),
		`"effective_at": "1970-01-01T00:00:00Z"`,
		`"effective_at": "2026-12-01T00:00:00Z"`,
	)
	files := fstest.MapFS{
		versionDirectory + "/v1.json": {Data: []byte(publishedFile(t, "v1.json"))},
		versionDirectory + "/v2.json": {Data: []byte(second)},
	}
	_, err := loadVersions(files)
	if err == nil || !strings.Contains(err.Error(), "reuses version v1") {
		t.Fatalf("want a reused-version error, got %v", err)
	}
}

func TestLoadVersionsRejectsChangedEffectiveDateOrder(t *testing.T) {
	earlier := strings.ReplaceAll(laterVersion(t), "2026-12-01T00:00:00Z", "1969-12-31T00:00:00Z")
	files := fstest.MapFS{
		versionDirectory + "/v1.json": {Data: []byte(publishedFile(t, "v1.json"))},
		versionDirectory + "/v2.json": {Data: []byte(earlier)},
	}
	_, err := loadVersions(files)
	if err == nil || !strings.Contains(err.Error(), "not after version v1") {
		t.Fatalf("want an effective-date order error, got %v", err)
	}
}

func TestLoadVersionsRejectsFileNameThatDoesNotMatchVersion(t *testing.T) {
	files := fstest.MapFS{
		versionDirectory + "/v2.json": {Data: []byte(publishedFile(t, "v1.json"))},
	}
	_, err := loadVersions(files)
	if err == nil || !strings.Contains(err.Error(), "the file must be named v1.json") {
		t.Fatalf("want a file-name error, got %v", err)
	}
}

func TestLoadVersionsRejectsEventTypeThatDoesNotNameTheVersion(t *testing.T) {
	content := strings.ReplaceAll(laterVersion(t), `"cloud_event_type": "osac.resource.heartbeat.v2"`,
		`"cloud_event_type": "osac.resource.heartbeat.v1"`)
	files := fstest.MapFS{
		versionDirectory + "/v2.json": {Data: []byte(content)},
	}
	_, err := loadVersions(files)
	if err == nil || !strings.Contains(err.Error(), "the type suffix is the schema version") {
		t.Fatalf("want an event-type error, got %v", err)
	}
}

func TestLoadVersionsRejectsUnknownFieldKey(t *testing.T) {
	content := strings.Replace(
		publishedFile(t, "v1.json"), `"name": "tenant_id",`,
		`"name": "tenant_id", "unexpected": true,`, 1,
	)
	files := fstest.MapFS{versionDirectory + "/v1.json": {Data: []byte(content)}}
	_, err := loadVersions(files)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("want an unknown-key error, got %v", err)
	}
}

func TestLoadVersionsRejectsMissingRequiredMarker(t *testing.T) {
	content := strings.Replace(
		publishedFile(t, "v1.json"),
		`      "type": "string",
      "required": true,
      "description": "Tenant the usage belongs to.",
      "unit": ""`, `      "type": "string",
      "description": "Tenant the usage belongs to.",
      "unit": ""`, 1,
	)
	files := fstest.MapFS{versionDirectory + "/v1.json": {Data: []byte(content)}}
	_, err := loadVersions(files)
	if err == nil || !strings.Contains(err.Error(), "no required marker") {
		t.Fatalf("want a required-marker error, got %v", err)
	}
}

func TestLoadVersionsRequiresThePayloadShapeToMatchTheCode(t *testing.T) {
	content := strings.Replace(
		publishedFile(t, "v1.json"),
		`    {
      "name": "tenant_id",
      "type": "string",
      "required": true,
      "description": "Tenant the usage belongs to.",
      "unit": ""
    },
`, "", 1,
	)
	files := fstest.MapFS{versionDirectory + "/v1.json": {Data: []byte(content)}}
	_, err := loadVersions(files)
	if err == nil || !strings.Contains(err.Error(), `missing field "tenant_id"`) {
		t.Fatalf("want a payload drift error, got %v", err)
	}
}

func TestLoadVersionsRequiresTheEnvelopeToMatchTheEvent(t *testing.T) {
	content := strings.Replace(
		publishedFile(t, "v1.json"), `"name": "osactenant"`, `"name": "osactenantx"`, 1,
	)
	files := fstest.MapFS{versionDirectory + "/v1.json": {Data: []byte(content)}}
	_, err := loadVersions(files)
	if err == nil || !strings.Contains(err.Error(), `missing field "osactenant"`) {
		t.Fatalf("want an envelope drift error, got %v", err)
	}
}

func TestPayloadShapeHonoursOmitempty(t *testing.T) {
	type payload struct {
		Always string `json:"always"`
		Maybe  string `json:"maybe,omitempty"`
	}
	fields, err := payloadShape(reflect.TypeOf(payload{}))
	if err != nil {
		t.Fatalf("deriving the payload shape: %v", err)
	}
	if len(fields) != 2 {
		t.Fatalf("want two fields, got %v", fields)
	}
	if !fields[0].Required {
		t.Fatalf("a field without omitempty is required, got %v", fields[0])
	}
	if fields[1].Required {
		t.Fatalf("a field with omitempty is optional, got %v", fields[1])
	}
}

func TestCompareShapeAcceptsAPublishedOptionalField(t *testing.T) {
	expected := []Field{{Name: "maybe", Type: FieldTypeString, Required: false}}
	published := []Field{{Name: "maybe", Type: FieldTypeString, Required: false}}
	if err := compareShape("v2", "payload", expected, published); err != nil {
		t.Fatalf("an optional field may be published as optional, got %v", err)
	}
	if err := compareShape("v2", "payload", expected,
		[]Field{{Name: "maybe", Type: FieldTypeString, Required: true}}); err == nil {
		t.Fatal("publishing an omitted-when-empty field as required must fail")
	}
}

func TestSelectVersionRefusesAnEmptySet(t *testing.T) {
	_, err := selectVersion(nil, time.Unix(0, 0))
	if err == nil || !strings.Contains(err.Error(), "no schema version is in effect") {
		t.Fatalf("want an empty-set error, got %v", err)
	}
}

func TestLoadVersionsRejectsAnUnpaddedVersion(t *testing.T) {
	content := strings.ReplaceAll(publishedFile(t, "v1.json"), `"v1"`, `"v01"`)
	content = strings.ReplaceAll(content, "heartbeat.v1", "heartbeat.v01")
	files := fstest.MapFS{versionDirectory + "/v01.json": {Data: []byte(content)}}
	_, err := loadVersions(files)
	if err == nil || !strings.Contains(err.Error(), "want v<number>") {
		t.Fatalf("want a version-format error, got %v", err)
	}
}

func TestLoadVersionsHashFollowsTheFileContent(t *testing.T) {
	original, err := loadVersions(versionFiles)
	if err != nil {
		t.Fatalf("loading published schemas: %v", err)
	}
	changed := strings.Replace(
		publishedFile(t, "v1.json"), "Tenant the usage belongs to.", "Tenant the usage is billed to.", 1,
	)
	files := fstest.MapFS{versionDirectory + "/v1.json": {Data: []byte(changed)}}
	modified, err := loadVersions(files)
	if err != nil {
		t.Fatalf("loading changed schema: %v", err)
	}
	if original[0].SourceSHA256 == modified[0].SourceSHA256 {
		t.Fatal("changing a schema file must change its recorded hash")
	}
}

func TestVersionInEffectSelectsByRecordTime(t *testing.T) {
	files := fstest.MapFS{
		versionDirectory + "/v1.json": {Data: []byte(publishedFile(t, "v1.json"))},
		versionDirectory + "/v2.json": {Data: []byte(laterVersion(t))},
	}
	versions, err := loadVersions(files)
	if err != nil {
		t.Fatalf("loading synthetic schemas: %v", err)
	}
	before, err := time.Parse(time.RFC3339, "2026-11-30T23:59:59Z")
	if err != nil {
		t.Fatal(err)
	}
	after, err := time.Parse(time.RFC3339, "2026-12-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}

	selected, err := selectVersion(versions, before)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Version != "v1" {
		t.Fatalf("a record written before v2 must select v1, got %s", selected.Version)
	}
	selected, err = selectVersion(versions, after)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Version != "v2" {
		t.Fatalf("a record written at v2's effective time must select v2, got %s", selected.Version)
	}
}
