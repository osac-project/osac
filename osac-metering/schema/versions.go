/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package schema

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	pathpkg "path"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// heartbeatTypePrefix is the CloudEvent type family of a usage event. A version's type appends
// its version, so a reader reads the schema version from the type alone.
const heartbeatTypePrefix = "osac.resource.heartbeat."

// versionDirectory holds the published schema files, one JSON file per version, named after
// the version. A file never changes after release; adding a field is a new file.
const versionDirectory = "versions"

//go:embed versions/*.json
var versionFiles embed.FS

// FieldType is the JSON type a schema field carries.
type FieldType string

const (
	FieldTypeString  FieldType = "string"
	FieldTypeInteger FieldType = "integer"
	FieldTypeBoolean FieldType = "boolean"
	FieldTypeObject  FieldType = "object"
)

// Field describes one field of a published usage event.
type Field struct {
	// Name is the JSON field name.
	Name string

	// Type is the field's JSON type.
	Type FieldType

	// Required says whether every event of the version carries the field.
	Required bool

	// Format is the optional JSON Schema format, for example date-time.
	Format string

	// Pattern is the optional JSON Schema regular expression the text must match.
	Pattern string

	// Const is the single value the field always carries, when it has one.
	Const string

	// Unit describes what the value is measured in, when it has a unit.
	Unit string

	// Description says what the field means.
	Description string

	// Values is the JSON type of a map's values when Type is object and the object is a map
	// rather than a fixed set of fields.
	Values FieldType

	// Fields are the nested fields when Type is object and Values is empty.
	Fields []Field
}

// Version is one published schema version: the event fields and the time the version took
// effect. A published version never changes; a new field is a new version.
type Version struct {
	// Version is the version name, for example v1.
	Version string

	// EffectiveAt is the earliest date a record may carry this version. The producer writes
	// SchemaVersion unconditionally and never selects a version by date, so a version must
	// not take effect after the release that ships it. Load enforces this for the version
	// the binary emits: a future-dated current version would write records that replay
	// refuses.
	EffectiveAt time.Time

	// CloudEventType is the event type of the version, for example
	// osac.resource.heartbeat.v1.
	CloudEventType string

	// Envelope are the CloudEvent attributes, outside the payload.
	Envelope []Field

	// Data are the payload fields.
	Data []Field

	// SourcePath is the repository path of the version's file, relative to the schema
	// module, for example versions/v1.json.
	SourcePath string

	// SourceSHA256 is the hex SHA-256 of the version's file, so a reader can verify the
	// published file it holds.
	SourceSHA256 string
}

// PublishedVersions returns every published schema version, sorted by version number. It
// fails when a file is invalid, when two files claim the same version or effective date, when
// an older version is not a subset of the newest (versions are add-only), when the version
// metering sends does not match the heartbeat payload in this package, or when that version
// takes effect in the future. Editing a published file is not how a field changes, so each
// mismatch must stop the build.
func PublishedVersions() ([]Version, error) {
	return loadVersions(versionFiles, time.Now().UTC())
}

// VersionInEffect returns the version in effect at a record's writing time: the latest
// version whose effective date is at or before it. This is reader API: a reader uses it to
// check that the version a record names had been published by then. Metering's rebuild does
// not call it; it decodes a record with the version its CloudEvent names and the current
// add-only payload shape.
func VersionInEffect(at time.Time) (Version, error) {
	if at.IsZero() {
		return Version{}, errors.New("no schema version is in effect at the zero time")
	}
	versions, err := PublishedVersions()
	if err != nil {
		return Version{}, err
	}
	return selectVersion(versions, at.UTC())
}

// selectVersion returns the latest version whose effective date is at or before the record
// time.
func selectVersion(versions []Version, at time.Time) (Version, error) {
	if len(versions) == 0 {
		return Version{}, fmt.Errorf("no schema version is in effect at %s", at.Format(time.RFC3339))
	}
	var selected Version
	found := false
	for _, version := range versions {
		if version.EffectiveAt.After(at) {
			break
		}
		selected = version
		found = true
	}
	if !found {
		return Version{}, fmt.Errorf(
			"no schema version is in effect at %s; the first version %s takes effect at %s",
			at.Format(time.RFC3339), versions[0].Version,
			versions[0].EffectiveAt.Format(time.RFC3339),
		)
	}
	return selected, nil
}

// VersionForType returns the version a CloudEvent type names among the given published
// versions. An unknown type is refused with this error, and a reader treats it as a version it
// has not adopted.
func VersionForType(versions []Version, cloudEventType string) (Version, error) {
	for _, version := range versions {
		if version.CloudEventType == cloudEventType {
			return version, nil
		}
	}
	return Version{}, fmt.Errorf(
		"no published schema version names event type %q; treat it as a version you have "+
			"not adopted", cloudEventType,
	)
}

// versionFile is the JSON shape of a published schema file.
type versionFile struct {
	Version        string      `json:"version"`
	EffectiveAt    string      `json:"effective_at"`
	CloudEventType string      `json:"cloud_event_type"`
	Envelope       []fieldFile `json:"envelope"`
	Data           []fieldFile `json:"data"`
}

// fieldFile is the JSON shape of one field in a published schema file. Required is a pointer
// so a missing marker is an error rather than an optional field.
type fieldFile struct {
	Name        string      `json:"name"`
	Type        string      `json:"type"`
	Required    *bool       `json:"required"`
	Format      string      `json:"format"`
	Pattern     string      `json:"pattern"`
	Const       string      `json:"const"`
	Unit        string      `json:"unit"`
	Description string      `json:"description"`
	Values      string      `json:"values"`
	Fields      []fieldFile `json:"fields"`
}

// loadVersions parses and validates every version file in a filesystem. PublishedVersions
// reads the embedded files with the wall clock; tests feed a synthetic filesystem and an
// explicit now.
func loadVersions(fsys fs.FS, now time.Time) ([]Version, error) {
	paths, err := fs.Glob(fsys, versionDirectory+"/*.json")
	if err != nil {
		return nil, fmt.Errorf("listing schema versions: %w", err)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no published schema versions in %s", versionDirectory)
	}
	sort.Strings(paths)

	versions := make([]Version, 0, len(paths))
	seen := map[string]bool{}
	for _, path := range paths {
		content, err := fs.ReadFile(fsys, path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		version, err := parseVersion(path, content)
		if err != nil {
			return nil, err
		}
		if seen[version.Version] {
			return nil, fmt.Errorf(
				"%s reuses version %s; a published version is never reused, so a new "+
					"schema file needs the next version number",
				path, version.Version,
			)
		}
		seen[version.Version] = true
		if want := version.Version + ".json"; pathpkg.Base(path) != want {
			return nil, fmt.Errorf(
				"%s declares version %s; the file must be named %s", path, version.Version, want,
			)
		}
		versions = append(versions, version)
	}

	sort.Slice(versions, func(i, j int) bool {
		return versionNumber(versions[i].Version) < versionNumber(versions[j].Version)
	})
	for i := 1; i < len(versions); i++ {
		if !versions[i].EffectiveAt.After(versions[i-1].EffectiveAt) {
			return nil, fmt.Errorf(
				"schema version %s takes effect at %s, not after version %s at %s",
				versions[i].Version, versions[i].EffectiveAt.Format(time.RFC3339),
				versions[i-1].Version, versions[i-1].EffectiveAt.Format(time.RFC3339),
			)
		}
	}

	if err := checkCurrentPayload(versions, now); err != nil {
		return nil, err
	}
	if err := checkVersionsAddOnly(versions); err != nil {
		return nil, err
	}
	return versions, nil
}

// parseVersion parses and validates one version file.
func parseVersion(path string, content []byte) (Version, error) {
	var file versionFile
	decoder := json.NewDecoder(bytes.NewReader(content))
	// Unknown keys reject the file instead of silently dropping a field the author meant
	// the contract to carry.
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return Version{}, fmt.Errorf("%s is not a valid schema file: %w", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Version{}, fmt.Errorf(
			"%s has data after its version object; a schema file holds one JSON object", path,
		)
	}

	digest := sha256.Sum256(content)
	version := Version{
		Version:        file.Version,
		CloudEventType: file.CloudEventType,
		SourcePath:     path,
		SourceSHA256:   hex.EncodeToString(digest[:]),
	}
	if !versionPattern.MatchString(file.Version) {
		return Version{}, fmt.Errorf("%s names version %q, want v<number>", path, file.Version)
	}
	if want := heartbeatTypePrefix + file.Version; file.CloudEventType != want {
		return Version{}, fmt.Errorf(
			"%s names event type %q, want %q: the type suffix is the schema version",
			path, file.CloudEventType, want,
		)
	}
	effectiveAt, err := time.Parse(time.RFC3339, file.EffectiveAt)
	if err != nil {
		return Version{}, fmt.Errorf("%s has an invalid effective_at %q", path, file.EffectiveAt)
	}
	version.EffectiveAt = effectiveAt.UTC()
	if file.EffectiveAt != version.EffectiveAt.Format(time.RFC3339) {
		return Version{}, fmt.Errorf(
			"%s has effective_at %q; use UTC in RFC 3339 form, for example %s",
			path, file.EffectiveAt, version.EffectiveAt.Format(time.RFC3339),
		)
	}
	if version.Envelope, err = parseFields(file.Envelope, "envelope"); err != nil {
		return Version{}, fmt.Errorf("%s: %w", path, err)
	}
	if version.Data, err = parseFields(file.Data, "data"); err != nil {
		return Version{}, fmt.Errorf("%s: %w", path, err)
	}
	if len(version.Envelope) == 0 {
		return Version{}, fmt.Errorf("%s declares no envelope fields", path)
	}
	if len(version.Data) == 0 {
		return Version{}, fmt.Errorf("%s declares no payload fields", path)
	}
	return version, nil
}

// parseFields validates one level of fields.
func parseFields(files []fieldFile, where string) ([]Field, error) {
	fields := make([]Field, 0, len(files))
	seen := map[string]bool{}
	for _, file := range files {
		if !fieldNamePattern.MatchString(file.Name) {
			return nil, fmt.Errorf("%s field name %q is not lower_snake_case", where, file.Name)
		}
		if seen[file.Name] {
			return nil, fmt.Errorf("%s repeats field %q", where, file.Name)
		}
		seen[file.Name] = true
		if file.Required == nil {
			return nil, fmt.Errorf("%s field %q has no required marker", where, file.Name)
		}
		if file.Description == "" {
			return nil, fmt.Errorf("%s field %q has no description", where, file.Name)
		}
		fieldType := FieldType(file.Type)
		switch fieldType {
		case FieldTypeString, FieldTypeInteger, FieldTypeBoolean, FieldTypeObject:
		default:
			return nil, fmt.Errorf("%s field %q has unknown type %q", where, file.Name, file.Type)
		}
		field := Field{
			Name:        file.Name,
			Type:        fieldType,
			Required:    *file.Required,
			Format:      file.Format,
			Pattern:     file.Pattern,
			Const:       file.Const,
			Unit:        file.Unit,
			Description: file.Description,
		}
		if fieldType != FieldTypeString && (field.Format != "" || field.Pattern != "") {
			return nil, fmt.Errorf(
				"%s field %q has a format or pattern but is not a string", where, file.Name,
			)
		}
		if fieldType != FieldTypeString && field.Const != "" {
			return nil, fmt.Errorf(
				"%s field %q has a constant value but is not a string", where, file.Name,
			)
		}
		if fieldType != FieldTypeObject {
			if file.Values != "" || len(file.Fields) > 0 {
				return nil, fmt.Errorf(
					"%s field %q has nested fields or values but is not an object", where, file.Name,
				)
			}
			fields = append(fields, field)
			continue
		}
		switch {
		case file.Values != "" && len(file.Fields) > 0:
			return nil, fmt.Errorf(
				"%s field %q is both a map and a fixed object", where, file.Name,
			)
		case file.Values != "":
			switch FieldType(file.Values) {
			case FieldTypeString, FieldTypeInteger, FieldTypeBoolean:
				field.Values = FieldType(file.Values)
			default:
				return nil, fmt.Errorf(
					"%s field %q has unknown value type %q", where, file.Name, file.Values,
				)
			}
		case len(file.Fields) == 0:
			return nil, fmt.Errorf(
				"%s field %q is an object with neither fields nor a value type", where, file.Name,
			)
		default:
			nested, err := parseFields(file.Fields, where+" field "+file.Name)
			if err != nil {
				return nil, err
			}
			field.Fields = nested
		}
		fields = append(fields, field)
	}
	return fields, nil
}

// checkCurrentPayload requires the published version named by HeartbeatType to describe
// exactly the event this package sends: the CloudEvent attributes and the payload. A field
// added without a new version would change a published schema's meaning while its records
// still name the old version, so it fails here. It also refuses a current version whose
// effective date is in the future: the producer writes it unconditionally, so its records
// would be written before the date and replay would refuse them.
func checkCurrentPayload(versions []Version, now time.Time) error {
	var current *Version
	for i := range versions {
		if versions[i].CloudEventType == HeartbeatType {
			current = &versions[i]
			break
		}
	}
	if current == nil {
		return fmt.Errorf(
			"no published schema version for event type %s; add %s/%s.json",
			HeartbeatType, versionDirectory, SchemaVersion,
		)
	}
	envelope := currentEnvelope()
	if err := compareShape(current.Version, "the CloudEvent envelope", envelope, current.Envelope); err != nil {
		return err
	}
	expected, err := payloadShape(reflect.TypeOf(HeartbeatData{}))
	if err != nil {
		return err
	}
	if err := compareShape(current.Version, "HeartbeatData", expected, current.Data); err != nil {
		return err
	}
	if current.EffectiveAt.After(now) {
		return fmt.Errorf(
			"schema version %s takes effect at %s, after now (%s): metering writes its "+
				"current version unconditionally, so a release with a future effective date "+
				"would write records that replay refuses; set effective_at no later than the "+
				"release date",
			current.Version, current.EffectiveAt.Format(time.RFC3339),
			now.Format(time.RFC3339),
		)
	}
	return nil
}

// checkVersionsAddOnly requires every older version to remain a subset of the newest version.
// Versions are add-only: replay decodes older records with the current payload shape, so a
// dropped or retyped field would change the meaning of retained usage.
func checkVersionsAddOnly(versions []Version) error {
	return checkOlderVersions(versions, &versions[len(versions)-1])
}

// checkOlderVersions requires every field of an earlier version to still exist in the
// current version with the same type. Versions are add-only: a record written under an
// earlier version is decoded with the current payload shape, so dropping or retyping a
// field would change the meaning of retained usage.
func checkOlderVersions(versions []Version, current *Version) error {
	for i := range versions {
		older := &versions[i]
		if older.Version == current.Version || older.EffectiveAt.After(current.EffectiveAt) {
			continue
		}
		if err := checkSubset(older.Version, current.Version, "the CloudEvent envelope", older.Envelope, current.Envelope); err != nil {
			return err
		}
		if err := checkSubset(older.Version, current.Version, "HeartbeatData", older.Data, current.Data); err != nil {
			return err
		}
	}
	return nil
}

// checkSubset reports a field an earlier version published that the current version no
// longer publishes with the same type. Extra fields in the current version are additions.
func checkSubset(olderVersion, currentVersion, typeName string, older, current []Field) error {
	byName := map[string]Field{}
	for _, field := range current {
		byName[field.Name] = field
	}
	for _, old := range older {
		got, found := byName[old.Name]
		if !found {
			return fmt.Errorf(
				"schema version %s drops field %q of %s that version %s still carries; "+
					"versions are add-only, so add the next version file instead",
				currentVersion, old.Name, typeName, olderVersion,
			)
		}
		if got.Type != old.Type {
			return fmt.Errorf(
				"schema version %s changes field %q of %s from %s to %s; versions are add-only",
				currentVersion, old.Name, typeName, old.Type, got.Type,
			)
		}
		if got.Values != old.Values {
			return fmt.Errorf(
				"schema version %s changes field %q of %s from value type %q to %q; versions are add-only",
				currentVersion, old.Name, typeName, old.Values, got.Values,
			)
		}
		if got.Const != old.Const && !versionDiscriminator(typeName, old.Name) {
			// Every constant except the version discriminator is add-only.
			return fmt.Errorf(
				"schema version %s changes field %q of %s from value %q to %q; versions are add-only",
				currentVersion, old.Name, typeName, old.Const, got.Const,
			)
		}
		if got.Pattern != old.Pattern {
			return fmt.Errorf(
				"schema version %s changes the pattern of field %q of %s; versions are add-only",
				currentVersion, old.Name, typeName,
			)
		}
		if got.Format != old.Format {
			return fmt.Errorf(
				"schema version %s changes the format of field %q of %s; versions are add-only",
				currentVersion, old.Name, typeName,
			)
		}
		if got.Unit != old.Unit {
			return fmt.Errorf(
				"schema version %s changes the unit of field %q of %s from %q to %q; versions are add-only",
				currentVersion, old.Name, typeName, old.Unit, got.Unit,
			)
		}
		if len(old.Fields) > 0 || len(got.Fields) > 0 {
			if err := checkSubset(olderVersion, currentVersion, typeName+"."+old.Name, old.Fields, got.Fields); err != nil {
				return err
			}
		}
	}
	return nil
}

// versionDiscriminator reports whether a field is the CloudEvent type, whose constant changes
// with every version by design.
func versionDiscriminator(typeName, field string) bool {
	return typeName == "the CloudEvent envelope" && field == "type"
}

// currentEnvelope returns the CloudEvent attributes every heartbeat carries. The core
// attributes are the ones the CloudEvents SDK writes for an event with JSON data; the
// extensions are the ones heartbeat.New sets from the schema constants. The values are the
// constants the producer sets, so a published envelope cannot disagree with them.
func currentEnvelope() []Field {
	return []Field{
		{Name: "specversion", Type: FieldTypeString, Required: true, Const: "1.0"},
		{Name: "id", Type: FieldTypeString, Required: true, Pattern: "^[0-9a-f]{64}$"},
		{Name: "type", Type: FieldTypeString, Required: true, Const: HeartbeatType},
		{Name: "source", Type: FieldTypeString, Required: true, Const: "osac-metering"},
		{Name: "datacontenttype", Type: FieldTypeString, Required: true, Const: "application/json"},
		{Name: "time", Type: FieldTypeString, Required: true, Format: "date-time"},
		{Name: ExtResourceID, Type: FieldTypeString, Required: true},
		{Name: ExtResourceType, Type: FieldTypeString, Required: true},
		{Name: ExtTenant, Type: FieldTypeString, Required: true},
	}
}

// payloadShape derives the published fields of a Go payload type from its JSON tags. A field
// without omitempty is always marshalled, so it is required; a field with omitempty is absent
// when it is empty and may be published as optional.
func payloadShape(typ reflect.Type) ([]Field, error) {
	fields := make([]Field, 0, typ.NumField())
	for i := range typ.NumField() {
		structField := typ.Field(i)
		tag := structField.Tag.Get("json")
		name, options, _ := strings.Cut(tag, ",")
		if name == "" || name == "-" {
			continue
		}
		field, err := shapeOf(structField.Type)
		if err != nil {
			return nil, fmt.Errorf("payload field %q: %w", name, err)
		}
		field.Name = name
		field.Required = !slices.Contains(strings.Split(options, ","), "omitempty")
		fields = append(fields, field)
	}
	return fields, nil
}

// shapeOf maps one Go type to its published type.
func shapeOf(typ reflect.Type) (Field, error) {
	switch typ.Kind() {
	case reflect.String:
		return Field{Type: FieldTypeString}, nil
	case reflect.Bool:
		return Field{Type: FieldTypeBoolean}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return Field{Type: FieldTypeInteger}, nil
	case reflect.Map:
		if typ.Key().Kind() != reflect.String {
			return Field{}, fmt.Errorf("map key is %s, not a string", typ.Key().Kind())
		}
		values, err := shapeOf(typ.Elem())
		if err != nil {
			return Field{}, err
		}
		return Field{Type: FieldTypeObject, Values: values.Type}, nil
	case reflect.Pointer:
		return shapeOf(typ.Elem())
	case reflect.Struct:
		nested, err := payloadShape(typ)
		if err != nil {
			return Field{}, err
		}
		return Field{Type: FieldTypeObject, Fields: nested}, nil
	default:
		return Field{}, fmt.Errorf("go type %s has no published schema type", typ)
	}
}

// compareShape reports how a published field list differs from the event this package sends.
// The fix is a new version file, never an edit of a published one.
func compareShape(version, typeName string, expected, published []Field) error {
	publishedByName := map[string]Field{}
	for _, field := range published {
		publishedByName[field.Name] = field
	}
	for _, want := range expected {
		got, found := publishedByName[want.Name]
		if !found {
			return fmt.Errorf(
				"schema version %s is missing field %q of %s; "+
					"add the next version file instead of editing %s",
				version, want.Name, typeName, version,
			)
		}
		delete(publishedByName, want.Name)
		if got.Type != want.Type {
			return fmt.Errorf(
				"schema version %s declares field %q of %s as %s, want %s",
				version, want.Name, typeName, got.Type, want.Type,
			)
		}
		if got.Values != want.Values {
			return fmt.Errorf(
				"schema version %s declares field %q of %s with value type %q, want %q",
				version, want.Name, typeName, got.Values, want.Values,
			)
		}
		// The Go payload knows the envelope's fixed values but not the contract's
		// presentation choices for payload fields, so compare a value only when the code
		// defines one. The add-only check compares every version's choices.
		if want.Const != "" && got.Const != want.Const {
			return fmt.Errorf(
				"schema version %s declares field %q of %s as %q, want %q",
				version, want.Name, typeName, got.Const, want.Const,
			)
		}
		if want.Pattern != "" && got.Pattern != want.Pattern {
			return fmt.Errorf(
				"schema version %s declares pattern %q for field %q of %s, want %q",
				version, got.Pattern, want.Name, typeName, want.Pattern,
			)
		}
		if want.Format != "" && got.Format != want.Format {
			return fmt.Errorf(
				"schema version %s declares format %q for field %q of %s, want %q",
				version, got.Format, want.Name, typeName, want.Format,
			)
		}
		if want.Unit != "" && got.Unit != want.Unit {
			return fmt.Errorf(
				"schema version %s declares unit %q for field %q of %s, want %q",
				version, got.Unit, want.Name, typeName, want.Unit,
			)
		}
		if len(want.Fields) > 0 || len(got.Fields) > 0 {
			if err := compareShape(version, typeName+"."+want.Name, want.Fields, got.Fields); err != nil {
				return err
			}
		}
		if got.Required != want.Required {
			return fmt.Errorf(
				"schema version %s marks field %q of %s %s, want %s",
				version, want.Name, typeName, requiredWord(got.Required), requiredWord(want.Required),
			)
		}
	}
	extra := make([]string, 0, len(publishedByName))
	for name := range publishedByName {
		extra = append(extra, name)
	}
	sort.Strings(extra)
	if len(extra) > 0 {
		return fmt.Errorf(
			"schema version %s declares fields %s of %s that metering does not send",
			version, strings.Join(extra, ", "), typeName,
		)
	}
	return nil
}

// requiredWord renders a required marker for an error message.
func requiredWord(required bool) string {
	if required {
		return "required"
	}
	return "optional"
}

// CheckRequiredPayload reports the first field the version requires that a record's payload
// does not carry. Replay decodes every record with the current add-only payload shape, so it
// checks the record against the shape the record's own version published.
func (v Version) CheckRequiredPayload(data []byte) error {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("schema version %s: payload is not a JSON object: %w", v.Version, err)
	}
	return checkRequiredFields(v.Version, "", v.Data, payload)
}

// checkRequiredFields walks one level of a published payload and reports a required field the
// record omits.
func checkRequiredFields(
	version, prefix string, fields []Field, values map[string]json.RawMessage,
) error {
	for _, field := range fields {
		raw, found := values[field.Name]
		if !found || string(raw) == "null" {
			if field.Required {
				return fmt.Errorf(
					"record names schema version %s but is missing required field %q",
					version, prefix+field.Name,
				)
			}
			continue
		}
		if len(field.Fields) == 0 {
			continue
		}
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(raw, &nested); err != nil {
			return fmt.Errorf(
				"record names schema version %s but field %q is not an object",
				version, prefix+field.Name,
			)
		}
		if err := checkRequiredFields(version, prefix+field.Name+".", field.Fields, nested); err != nil {
			return err
		}
	}
	return nil
}

// versionNumber returns the numeric part of a version name that already matched
// versionPattern.
func versionNumber(version string) int {
	number, err := strconv.Atoi(strings.TrimPrefix(version, "v"))
	if err != nil {
		return 0
	}
	return number
}

var (
	versionPattern   = regexp.MustCompile(`^v[1-9][0-9]*$`)
	fieldNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)
