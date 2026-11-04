/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package contract

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/osac-project/osac/proto/metering"

	"github.com/osac-project/osac-metering/schema"
)

// jsonSchema is the machine-checkable half of the contract: a JSON Schema (draft 2020-12)
// that validates one usage event against the version its type names.
type jsonSchema struct {
	Schema      string                       `json:"$schema"`
	ID          string                       `json:"$id"`
	Title       string                       `json:"title"`
	Description string                       `json:"description"`
	OneOf       []map[string]string          `json:"oneOf"`
	Definitions map[string]versionJSONSchema `json:"$defs"`
}

// versionJSONSchema is one published version. The x-osac- keys are annotations, so a JSON
// Schema validator ignores them while a reader can verify the source file it holds.
type versionJSONSchema struct {
	Title                string         `json:"title"`
	Version              string         `json:"x-osac-version"`
	EffectiveAt          string         `json:"x-osac-effective-at"`
	SourceFile           string         `json:"x-osac-source-file"`
	SourceSHA256         string         `json:"x-osac-source-sha256"`
	Type                 string         `json:"type"`
	Required             []string       `json:"required"`
	Properties           map[string]any `json:"properties"`
	AdditionalProperties bool           `json:"additionalProperties"`
}

// renderSchema builds the JSON Schema.
func renderSchema(sources Sources) ([]byte, error) {
	resourceTypes := resourceTypeValues(sources.Resources)
	doc := jsonSchema{
		Schema: "https://json-schema.org/draft/2020-12/schema",
		ID:     "https://osac-project.org/metering/usage-event.schema.json",
		Title:  "OSAC metering usage event",
		Description: "One usage event on a tenant's metering usage topic. The CloudEvent " +
			"type names the published schema version.",
		OneOf:       make([]map[string]string, 0, len(sources.Versions)),
		Definitions: make(map[string]versionJSONSchema, len(sources.Versions)),
	}

	for _, version := range sources.Versions {
		properties := map[string]any{}
		required := make([]string, 0, len(version.Envelope)+1)
		for _, field := range version.Envelope {
			properties[field.Name] = fieldJSONSchema(field)
			if field.Name == "osacresourcetype" {
				setEnum(properties, field.Name, resourceTypes)
			}
			if field.Required {
				required = append(required, field.Name)
			}
		}
		payload := objectJSONSchema(version.Data)
		if fields, ok := payload["properties"].(map[string]any); ok {
			setEnum(fields, "resource_type", resourceTypes)
		}
		properties["data"] = payload
		required = append(required, "data")
		sort.Strings(required)

		doc.OneOf = append(doc.OneOf, map[string]string{"$ref": "#/$defs/" + version.Version})
		doc.Definitions[version.Version] = versionJSONSchema{
			Title:                fmt.Sprintf("%s usage event", version.CloudEventType),
			Version:              version.Version,
			EffectiveAt:          version.EffectiveAt.Format("2006-01-02T15:04:05Z"),
			SourceFile:           schemaFilePath(version),
			SourceSHA256:         version.SourceSHA256,
			Type:                 "object",
			Required:             required,
			Properties:           properties,
			AdditionalProperties: false,
		}
	}

	content, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding the generated schema: %w", err)
	}
	return append(content, '\n'), nil
}

// resourceTypeValues returns the resource_type values events carry, derived from the field
// registry and sorted.
func resourceTypeValues(resources []metering.Resource) []string {
	seen := map[string]bool{}
	values := make([]string, 0, len(resources))
	for _, resource := range resources {
		value := schema.ProtoResourceTypeToSchema(metering.ShortName(string(resource.Message)))
		if seen[value] {
			continue
		}
		seen[value] = true
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

// setEnum adds an enum to one property when the property exists.
func setEnum(properties map[string]any, name string, values []string) {
	field, ok := properties[name].(map[string]any)
	if !ok {
		return
	}
	field["enum"] = values
}

// objectJSONSchema describes an object with a fixed set of fields.
func objectJSONSchema(fields []schema.Field) map[string]any {
	properties := make(map[string]any, len(fields))
	required := make([]string, 0, len(fields))
	for _, field := range fields {
		properties[field.Name] = fieldJSONSchema(field)
		if field.Required {
			required = append(required, field.Name)
		}
	}
	sort.Strings(required)
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
}

// fieldJSONSchema describes one published field.
func fieldJSONSchema(field schema.Field) map[string]any {
	result := map[string]any{"type": string(field.Type)}
	switch {
	case field.Type == schema.FieldTypeObject && field.Values != "":
		result["additionalProperties"] = map[string]any{"type": string(field.Values)}
	case field.Type == schema.FieldTypeObject:
		for key, value := range objectJSONSchema(field.Fields) {
			result[key] = value
		}
	}
	if field.Format != "" {
		result["format"] = field.Format
	}
	if field.Pattern != "" {
		result["pattern"] = field.Pattern
	}
	if field.Const != "" {
		result["const"] = field.Const
	}
	if field.Description != "" {
		result["description"] = field.Description
	}
	return result
}
