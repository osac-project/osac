/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package schema

import (
	"encoding/json"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// testNow is the clock the load tests use. It is explicit so a fixture may carry a future
// effective date without depending on the wall clock.
var testNow = time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)

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
	versions, err := loadVersions(versionFiles, testNow)
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
	_, err := loadVersions(files, testNow)
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
	_, err := loadVersions(files, testNow)
	if err == nil || !strings.Contains(err.Error(), "not after version v1") {
		t.Fatalf("want an effective-date order error, got %v", err)
	}
}

func TestLoadVersionsRejectsFileNameThatDoesNotMatchVersion(t *testing.T) {
	files := fstest.MapFS{
		versionDirectory + "/v2.json": {Data: []byte(publishedFile(t, "v1.json"))},
	}
	_, err := loadVersions(files, testNow)
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
	_, err := loadVersions(files, testNow)
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
	_, err := loadVersions(files, testNow)
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
	_, err := loadVersions(files, testNow)
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
	_, err := loadVersions(files, testNow)
	if err == nil || !strings.Contains(err.Error(), `missing field "tenant_id"`) {
		t.Fatalf("want a payload drift error, got %v", err)
	}
}

func TestLoadVersionsRequiresTheEnvelopeToMatchTheEvent(t *testing.T) {
	content := strings.Replace(
		publishedFile(t, "v1.json"), `"name": "osactenant"`, `"name": "osactenantx"`, 1,
	)
	files := fstest.MapFS{versionDirectory + "/v1.json": {Data: []byte(content)}}
	_, err := loadVersions(files, testNow)
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
	_, err := loadVersions(files, testNow)
	if err == nil || !strings.Contains(err.Error(), "want v<number>") {
		t.Fatalf("want a version-format error, got %v", err)
	}
}

func TestLoadVersionsHashFollowsTheFileContent(t *testing.T) {
	original, err := loadVersions(versionFiles, testNow)
	if err != nil {
		t.Fatalf("loading published schemas: %v", err)
	}
	changed := strings.Replace(
		publishedFile(t, "v1.json"), "Tenant the usage belongs to.", "Tenant the usage is billed to.", 1,
	)
	files := fstest.MapFS{versionDirectory + "/v1.json": {Data: []byte(changed)}}
	modified, err := loadVersions(files, testNow)
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
	versions, err := loadVersions(files, testNow)
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

func TestCheckSubsetRejectsADroppedField(t *testing.T) {
	older := []Field{{Name: "kept", Type: FieldTypeString}, {Name: "dropped", Type: FieldTypeString}}
	current := []Field{{Name: "kept", Type: FieldTypeString}}
	err := checkSubset("v1", "v2", "HeartbeatData", older, current)
	if err == nil {
		t.Fatal("dropping a field was accepted")
	}
	if !strings.Contains(err.Error(), `drops field "dropped"`) {
		t.Fatalf("error = %v, want a dropped-field error", err)
	}
}

func TestCheckSubsetRejectsARetypedField(t *testing.T) {
	older := []Field{{Name: "quantity", Type: FieldTypeString}}
	current := []Field{{Name: "quantity", Type: FieldTypeInteger}}
	err := checkSubset("v1", "v2", "HeartbeatData", older, current)
	if err == nil {
		t.Fatal("changing a field type was accepted")
	}
	if !strings.Contains(err.Error(), "changes field") {
		t.Fatalf("error = %v, want a type-change error", err)
	}
}

func TestCheckSubsetRejectsADroppedNestedField(t *testing.T) {
	older := []Field{{Name: "usage", Type: FieldTypeObject, Fields: []Field{
		{Name: "from", Type: FieldTypeString},
		{Name: "to", Type: FieldTypeString},
	}}}
	current := []Field{{Name: "usage", Type: FieldTypeObject, Fields: []Field{
		{Name: "from", Type: FieldTypeString},
	}}}
	err := checkSubset("v1", "v2", "HeartbeatData", older, current)
	if err == nil {
		t.Fatal("dropping a nested field was accepted")
	}
	if !strings.Contains(err.Error(), `drops field "to"`) {
		t.Fatalf("error = %v, want a nested dropped-field error", err)
	}
}

func TestCheckSubsetRejectsAMapValueTypeChange(t *testing.T) {
	older := []Field{{Name: "dimensions", Type: FieldTypeObject, Values: FieldTypeString}}
	current := []Field{{Name: "dimensions", Type: FieldTypeObject, Values: FieldTypeInteger}}
	err := checkSubset("v1", "v2", "HeartbeatData", older, current)
	if err == nil {
		t.Fatal("changing a map value type was accepted")
	}
	if !strings.Contains(err.Error(), "value type") {
		t.Fatalf("error = %v, want a map value-type error", err)
	}
}

func TestCheckSubsetAcceptsAnAddedField(t *testing.T) {
	older := []Field{{Name: "kept", Type: FieldTypeString}}
	current := []Field{{Name: "kept", Type: FieldTypeString}, {Name: "added", Type: FieldTypeString}}
	if err := checkSubset("v1", "v2", "HeartbeatData", older, current); err != nil {
		t.Fatalf("adding a field was rejected: %v", err)
	}
}

// versionDocument decodes a schema file so a test can change one field without matching
// formatting byte for byte.
func versionDocument(t *testing.T, content string) map[string]any {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal([]byte(content), &document); err != nil {
		t.Fatalf("decoding the test schema file: %v", err)
	}
	return document
}

// writeVersionDocument encodes a test schema file.
func writeVersionDocument(t *testing.T, document map[string]any) string {
	t.Helper()
	content, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encoding the test schema file: %v", err)
	}
	return string(content)
}

// dropDataField removes a payload field from a test schema file.
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
	return writeVersionDocument(t, document)
}

// addDataField appends an optional payload field to a test schema file.
func addDataField(t *testing.T, content, name string) string {
	t.Helper()
	document := versionDocument(t, content)
	document["data"] = append(document["data"].([]any), map[string]any{
		"name": name, "type": "string", "required": false,
		"description": "Added by the next version.", "unit": "",
	})
	return writeVersionDocument(t, document)
}

// setDataField changes one attribute of a payload field in a test schema file, including a
// nested field.
func setDataField(t *testing.T, content, name, attribute, value string) string {
	t.Helper()
	document := versionDocument(t, content)
	if !setFieldAttribute(document["data"].([]any), name, attribute, value) {
		t.Fatalf("the test schema file has no field %q", name)
	}
	return writeVersionDocument(t, document)
}

func setFieldAttribute(fields []any, name, attribute, value string) bool {
	for _, raw := range fields {
		field := raw.(map[string]any)
		if field["name"] == name {
			field[attribute] = value
			return true
		}
		nested, ok := field["fields"].([]any)
		if ok && setFieldAttribute(nested, name, attribute, value) {
			return true
		}
	}
	return false
}

// twoVersions builds a v1 and a version-two file from the published file.
func twoVersions(t *testing.T, second string) fstest.MapFS {
	t.Helper()
	return fstest.MapFS{
		versionDirectory + "/v1.json": {Data: []byte(publishedFile(t, "v1.json"))},
		versionDirectory + "/v2.json": {Data: []byte(second)},
	}
}

// TestLoadVersionsRejectsAVersionThatDropsAField is the load-level add-only rule: replay
// decodes older records with the newest shape, so a v2 that drops a v1 field must not load.
func TestLoadVersionsRejectsAVersionThatDropsAField(t *testing.T) {
	_, err := loadVersions(twoVersions(t, dropDataField(t, laterVersion(t), "rules_revision")), testNow)
	if err == nil || !strings.Contains(err.Error(), `drops field "rules_revision"`) {
		t.Fatalf("want a dropped-field error, got %v", err)
	}
}

func TestLoadVersionsAcceptsAVersionThatOnlyAddsAField(t *testing.T) {
	versions, err := loadVersions(
		twoVersions(t, addDataField(t, laterVersion(t), "provider_region")), testNow,
	)
	if err != nil {
		t.Fatalf("a version that only adds an optional field must load: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("want two loaded versions, got %d", len(versions))
	}
}

func TestLoadVersionsRejectsAChangedPublishedPattern(t *testing.T) {
	second := setDataField(t, laterVersion(t), "quantity", "pattern", `^-?[0-9]+$`)
	_, err := loadVersions(twoVersions(t, second), testNow)
	if err == nil || !strings.Contains(err.Error(), "changes the pattern") {
		t.Fatalf("want a changed-pattern error, got %v", err)
	}
}

// TestLoadVersionsRejectsAFutureDatedCurrentVersion pins the enforcement: the producer writes
// its current version unconditionally, so a release may not ship a current version whose
// effective date has not arrived.
func TestLoadVersionsRejectsAFutureDatedCurrentVersion(t *testing.T) {
	content := strings.Replace(
		publishedFile(t, "v1.json"),
		`"effective_at": "1970-01-01T00:00:00Z"`,
		`"effective_at": "2028-01-01T00:00:00Z"`, 1,
	)
	files := fstest.MapFS{versionDirectory + "/v1.json": {Data: []byte(content)}}
	_, err := loadVersions(files, testNow)
	if err == nil || !strings.Contains(err.Error(), "after now") {
		t.Fatalf("want a future-effective-date error, got %v", err)
	}
}

func TestLoadVersionsRejectsDataAfterTheVersionObject(t *testing.T) {
	content := publishedFile(t, "v1.json") + "\n{}\n"
	files := fstest.MapFS{versionDirectory + "/v1.json": {Data: []byte(content)}}
	_, err := loadVersions(files, testNow)
	if err == nil || !strings.Contains(err.Error(), "data after its version object") {
		t.Fatalf("want a trailing-data error, got %v", err)
	}
}

func TestVersionCheckRequiredPayload(t *testing.T) {
	versions, err := loadVersions(versionFiles, testNow)
	if err != nil {
		t.Fatalf("loading published schemas: %v", err)
	}
	version := versions[0]
	payload := []byte(`{
		"tenant_id": "acme", "resource_type": "compute_instance", "resource_id": "vm-1",
		"meter": "compute", "component": "compute", "billing_dimensions": {"tenant": "acme"},
		"usage": {
			"semantics": "interval", "from": "2026-11-02T09:59:00Z",
			"to": "2026-11-02T10:00:00Z", "quantity": "60.000000",
			"unit": "second", "precision": "second"
		},
		"heartbeat_time": "2026-11-02T10:00:00Z", "last_applied_offset": 7,
		"rules_revision": "0001"
	}`)
	if err := version.CheckRequiredPayload(payload); err != nil {
		t.Fatalf("a complete payload was rejected: %v", err)
	}

	var complete map[string]any
	if err := json.Unmarshal(payload, &complete); err != nil {
		t.Fatalf("decoding the test payload: %v", err)
	}
	delete(complete["usage"].(map[string]any), "quantity")
	broken, err := json.Marshal(complete)
	if err != nil {
		t.Fatalf("encoding the test payload: %v", err)
	}
	if err := version.CheckRequiredPayload(broken); err == nil ||
		!strings.Contains(err.Error(), `missing required field "usage.quantity"`) {
		t.Fatalf("want a missing nested field error, got %v", err)
	}

	// An optional object that is present must still be complete; one that is absent may be
	// omitted.
	optional := versions[0]
	optional.Data = append([]Field{}, versions[0].Data...)
	for i := range optional.Data {
		if optional.Data[i].Name == "usage" {
			optional.Data[i].Required = false
		}
	}
	if err := optional.CheckRequiredPayload(broken); err == nil ||
		!strings.Contains(err.Error(), `missing required field "usage.quantity"`) {
		t.Fatalf("a present optional object must be complete, got %v", err)
	}
	var omitted map[string]any
	if err := json.Unmarshal(payload, &omitted); err != nil {
		t.Fatalf("decoding the test payload: %v", err)
	}
	delete(omitted, "usage")
	omittedPayload, err := json.Marshal(omitted)
	if err != nil {
		t.Fatalf("encoding the test payload: %v", err)
	}
	if err := optional.CheckRequiredPayload(omittedPayload); err != nil {
		t.Fatalf("an absent optional object must be accepted, got %v", err)
	}
}

func TestLoadVersionsRejectsAChangedEnvelopeConstant(t *testing.T) {
	content := strings.Replace(
		publishedFile(t, "v1.json"), `"const": "osac-metering"`, `"const": "someone-else"`, 1,
	)
	files := fstest.MapFS{versionDirectory + "/v1.json": {Data: []byte(content)}}
	_, err := loadVersions(files, testNow)
	if err == nil || !strings.Contains(err.Error(), `want "osac-metering"`) {
		t.Fatalf("want an envelope constant error, got %v", err)
	}
}
