/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except
in compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

package contract

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/osac-project/osac/proto/metering"

	"github.com/osac-project/osac-metering/internal/rules"
	"github.com/osac-project/osac-metering/schema"
)

// dimensionView is one billing dimension and the tracked field its value comes from.
type dimensionView struct {
	Name string
	Path string
	Note string
}

// meterView is one meter, ready to render.
type meterView struct {
	Meter      rules.Meter
	Component  string
	Dimensions []dimensionView
	Conditions []string
}

// resourceView is one billable resource and its meters, ready to render.
type resourceView struct {
	Type    string
	Message string
	Fields  []metering.Field
	Meters  []meterView
}

// renderReference writes the readable contract. Every list is sorted, so the same inputs
// always produce the same bytes.
func renderReference(sources Sources) ([]byte, error) {
	views, err := buildResourceViews(sources)
	if err != nil {
		return nil, err
	}
	var out strings.Builder
	writeHeader(&out, sources, views)
	writeReadingRule(&out)
	writeLifecycle(&out)
	writeTransport(&out)
	writeDeduplication(&out)
	writeSchemaVersions(&out, sources)
	writeResources(&out, sources, views)
	writeBillingAccount(&out, sources)
	writeRegeneration(&out)
	return []byte(out.String()), nil
}

// buildResourceViews joins the field registry with the rule revision. A resource appears only
// when a meter bills it, because a resource without a meter emits no usage.
func buildResourceViews(sources Sources) ([]resourceView, error) {
	metersByResource := map[string][]rules.Meter{}
	for _, meter := range sources.Revision.Meters {
		metersByResource[meter.Resource] = append(metersByResource[meter.Resource], meter)
	}

	views := make([]resourceView, 0, len(sources.Resources))
	rendered := 0
	for _, resource := range sources.Resources {
		message := metering.ShortName(string(resource.Message))
		meters := metersByResource[message]
		if len(meters) == 0 {
			continue
		}
		rendered += len(meters)
		fields := append([]metering.Field{}, resource.Fields...)
		sort.Slice(fields, func(i, j int) bool { return fields[i].Path < fields[j].Path })
		view := resourceView{
			Type:    schema.ProtoResourceTypeToSchema(message),
			Message: message,
			Fields:  fields,
		}
		sort.Slice(meters, func(i, j int) bool { return meters[i].Meter < meters[j].Meter })
		for _, meter := range meters {
			meterView, err := newMeterView(meter)
			if err != nil {
				return nil, err
			}
			view.Meters = append(view.Meters, meterView)
		}
		views = append(views, view)
	}
	if rendered != len(sources.Revision.Meters) {
		return nil, fmt.Errorf(
			"the default rule revision defines %d meters but only %d name a resource the "+
				"field registry lists", len(sources.Revision.Meters), rendered,
		)
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Type < views[j].Type })
	return views, nil
}

// newMeterView resolves a meter's dimensions to the fields their values come from.
func newMeterView(meter rules.Meter) (meterView, error) {
	view := meterView{Meter: meter, Component: meter.EffectiveComponent()}
	dimensions := append([]string{}, meter.Dimensions...)
	sort.Strings(dimensions)
	for _, name := range dimensions {
		path, known := rules.DimensionPath(meter.Resource, name)
		if !known {
			return meterView{}, fmt.Errorf(
				"meter %s of %s dimensions by %q, which has no tracked field",
				meter.Meter, meter.Resource, name,
			)
		}
		view.Dimensions = append(view.Dimensions, dimensionView{
			Name: name, Path: path, Note: derivedDimensions[name],
		})
	}
	conditions := make([]string, 0, len(meter.Billable.Fields))
	for _, name := range sortedKeys(meter.Billable.Fields) {
		conditions = append(conditions, fmt.Sprintf("`%s` is `%s`", name, meter.Billable.Fields[name]))
	}
	view.Conditions = conditions
	return view, nil
}

func writeHeader(out *strings.Builder, sources Sources, views []resourceView) {
	meterCount := 0
	for _, view := range views {
		meterCount += len(view.Meters)
	}
	fmt.Fprintf(out, "# OSAC metering usage event contract\n\n")
	fmt.Fprintf(out,
		"This contract covers the usage events OSAC metering publishes: %d resource types "+
			"and %d meters, as rule revision %s defines them. The raw event log, the "+
			"failed-record topics, and the usage database stay internal. Only the per-tenant "+
			"usage topics and the events on them are part of this contract.\n\n",
		len(views), meterCount, sources.Revision.Number,
	)
	fmt.Fprintf(out,
		"It is generated from the metering field registry, the default rule revision the "+
			"chart ships, and the published schema files. Do not edit it by hand.\n\n",
	)
}

func writeReadingRule(out *strings.Builder) {
	fmt.Fprintf(out, "## How to read usage\n\n")
	fmt.Fprintf(out,
		"Add every usage event for one resource, meter, component, and set of billing "+
			"dimensions. A correction is a negative event, so adding it takes back the usage "+
			"it corrects. The sum is the billable quantity.\n\n",
	)
	fmt.Fprintf(out,
		"Match a correction to the usage it corrects by tenant, resource, meter, component, "+
			"and billing dimensions. Never match by quantity, event id, or interval. A "+
			"correction is sent in a later round, so it never shares the id of the event it "+
			"corrects.\n\n",
	)
}

func writeLifecycle(out *strings.Builder) {
	fmt.Fprintf(out, "## Lifecycle\n\n")
	fmt.Fprintf(out,
		"A resource starts billing when a rule revision says its state is billable. Each "+
			"minute metering sends the difference since the last event for the same usage.\n\n",
	)
	fmt.Fprintf(out,
		"A state or dimension change re-keys the usage: later events for the resource carry "+
			"the new dimensions, and each key is summed on its own.\n\n",
	)
	fmt.Fprintf(out,
		"When a resource stops being billable, metering sends a final correction for the "+
			"span it billed past the stop.\n\n",
	)
	fmt.Fprintf(out,
		"To rebuild a billable quantity from retained records, read each record with the "+
			"schema version in effect when it was written (see Schema versions) and add the "+
			"records for the key. The total matches what the reader saw.\n\n",
	)
}

func writeTransport(out *strings.Builder) {
	fmt.Fprintf(out, "## Transport\n\n")
	fmt.Fprintf(out, "| Fact | The rule |\n|---|---|\n")
	fmt.Fprintf(out, "| Topic naming | One usage topic per tenant, named `%s<tenant>` |\n",
		schema.HeartbeatPrefix)
	fmt.Fprintf(out,
		"| Ordering | Each usage topic has one partition, so events arrive in the order "+
			"metering sent them |\n")
	fmt.Fprintf(out,
		"| Delivery | At least once. A repeat carries the same CloudEvent `id` |\n")
	fmt.Fprintf(out,
		"| Retention | Set per installation with `topics.retentionMs`; the default `-1` "+
			"keeps every record |\n")
	fmt.Fprintf(out,
		"| Compaction | None. Metering refuses a compacted usage topic and sets "+
			"`cleanup.policy` to `delete` |\n")
	fmt.Fprintf(out,
		"| Raw event log | Internal. Metering reads `%s<tenant>` and never publishes it |\n",
		schema.EventsPrefix)
	fmt.Fprintf(out,
		"| Failed records | Internal. Rejected events land on `%s<tenant>` |\n",
		schema.DeadLetterPrefix)
	fmt.Fprintf(out,
		"| Retries | The reader owns its retries and its failed records. OSAC does not "+
			"retry for a reader |\n\n")
}

func writeDeduplication(out *strings.Builder) {
	fmt.Fprintf(out, "## Deduplication\n\n")
	fmt.Fprintf(out,
		"The CloudEvent `id` is the deduplication key. It is a lower-case hex SHA-256 of the "+
			"tenant, resource type, resource id, meter, component, billing dimensions, "+
			"interval start, interval end, and heartbeat time. It leaves out the quantity and "+
			"the external billing account id, so a resend carries the same id.\n\n",
	)
	fmt.Fprintf(out,
		"A reader drops an event whose id it has already seen and never recomputes the id.\n\n",
	)
}

func writeSchemaVersions(out *strings.Builder, sources Sources) {
	fmt.Fprintf(out, "## Schema versions\n\n")
	fmt.Fprintf(out,
		"Every usage event names its schema version in the CloudEvent `type`: the suffix "+
			"after the last dot, for example `v1` in `%s`.\n\n", schema.HeartbeatType)
	fmt.Fprintf(out,
		"A published schema never changes. A new field is a new version file with the next "+
			"version and the date it takes effect. The old file stays, so its records stay "+
			"readable.\n\n")
	fmt.Fprintf(out,
		"A published version stays readable for as long as its records are retained. "+
			"Retention is indefinite by default, so no version has been retired.\n\n")
	fmt.Fprintf(out, "| Version | CloudEvent type | Effective from | Source file | SHA-256 |\n")
	fmt.Fprintf(out, "|---|---|---|---|---|\n")
	for _, version := range sources.Versions {
		fmt.Fprintf(out, "| `%s` | `%s` | %s | `%s` | `%s` |\n",
			version.Version, version.CloudEventType,
			version.EffectiveAt.Format("2006-01-02T15:04:05Z"),
			schemaFilePath(version), version.SourceSHA256)
	}
	fmt.Fprintf(out, "\n")
	fmt.Fprintf(out,
		"A reader that meets a type it has not adopted alerts, keeps the event, and adds "+
			"support before that version's effective date. OSAC's own adapters dead-letter "+
			"such an event and increment a failure counter.\n\n")

	for _, version := range sources.Versions {
		fmt.Fprintf(out, "### Event fields of version `%s`\n\n", version.Version)
		fmt.Fprintf(out, "#### CloudEvent attributes\n\n")
		writeFieldTable(out, version.Envelope)
		fmt.Fprintf(out, "\n#### Payload\n\n")
		writeFieldTable(out, version.Data)
		fmt.Fprintf(out, "\n")
	}
}

func writeFieldTable(out *strings.Builder, fields []schema.Field) {
	fmt.Fprintf(out, "| Field | Type | Required | Unit | Meaning |\n|---|---|---|---|---|\n")
	writeFieldRows(out, "", fields)
}

func writeFieldRows(out *strings.Builder, prefix string, fields []schema.Field) {
	for _, field := range fields {
		name := prefix + field.Name
		fieldType := string(field.Type)
		if field.Values != "" {
			fieldType = fmt.Sprintf("object of %s", field.Values)
		}
		if field.Const != "" {
			fieldType = fmt.Sprintf("%s, always `%s`", fieldType, field.Const)
		}
		unit := field.Unit
		if unit == "" {
			unit = "-"
		}
		fmt.Fprintf(out, "| `%s` | %s | %s | %s | %s |\n",
			name, fieldType, requiredLabel(field.Required), unit, field.Description)
		if len(field.Fields) > 0 {
			writeFieldRows(out, name+".", field.Fields)
		}
	}
}

func writeResources(out *strings.Builder, sources Sources, views []resourceView) {
	fmt.Fprintf(out, "## Resources and meters\n\n")
	fmt.Fprintf(out,
		"The values below are the ones events carry in `resource_type`. A cluster appears "+
			"as `%s`.\n\n", schema.ResourceTypeClusterOrder)
	fmt.Fprintf(out,
		"The default rule revision is revision `%s`, effective from %s. It is immutable: a "+
			"rule change ships as the next revision.\n\n",
		sources.Revision.Number, sources.Revision.EffectiveAt.Format("2006-01-02T15:04:05Z"))
	writeIdentityDimensions(out)
	for _, view := range views {
		writeResource(out, view)
	}
}

func writeIdentityDimensions(out *strings.Builder) {
	fmt.Fprintf(out,
		"Every usage event also carries these identity dimensions in `billing_dimensions`, "+
			"besides the meter's own dimensions:\n\n")
	fmt.Fprintf(out, "| Dimension | Value |\n|---|---|\n")
	fmt.Fprintf(out, "| `deployment` | Id of the installation that wrote the event |\n")
	fmt.Fprintf(out, "| `tenant` | Tenant the usage belongs to |\n")
	fmt.Fprintf(out, "| `project` | Value of the resource's `metadata.project` |\n")
	fmt.Fprintf(out, "| `catalog` | Value of the resource's `spec.catalog_item` |\n")
	fmt.Fprintf(out, "| `template` | Value of the resource's `spec.template` |\n")
	fmt.Fprintf(out, "| `meter_type` | The meter's unit |\n\n")
}

func writeResource(out *strings.Builder, view resourceView) {
	fmt.Fprintf(out, "### %s (`%s`)\n\n", displayName(view.Type), view.Type)
	fmt.Fprintf(out, "Proto message `osac.private.v1.%s`.\n\n", view.Message)
	fmt.Fprintf(out, "| Meter | Component | Unit | Bills while | Turns billable once | Multiplier | Dimensions |\n")
	fmt.Fprintf(out, "|---|---|---|---|---|---|---|\n")
	dimensions := map[string]dimensionView{}
	for _, meter := range view.Meters {
		names := make([]string, 0, len(meter.Dimensions))
		for _, dimension := range meter.Dimensions {
			names = append(names, "`"+dimension.Name+"`")
			dimensions[dimension.Name] = dimension
		}
		fmt.Fprintf(out, "| `%s` | `%s` | `%s` | %s | %s | %s | %s |\n",
			meter.Meter.Meter, meter.Component, meter.Meter.Unit,
			joinOrNone(meter.Meter.Billable.States),
			joinOrNone(meter.Meter.Billable.TurnsBillableOnce),
			codeOrNone(meter.Meter.Multiplier), joinOrNone(names))
	}
	fmt.Fprintf(out, "\n")
	for _, meter := range view.Meters {
		if len(meter.Conditions) > 0 {
			fmt.Fprintf(out, "Meter `%s` also bills only when %s.\n\n",
				meter.Meter.Meter, strings.Join(meter.Conditions, " and "))
		}
		if meter.Meter.Billable.TurnsBillableOnceFrom == "self" {
			fmt.Fprintf(out,
				"Meter `%s` confirms on the resource's own states, not on its ownership "+
					"root's.\n\n", meter.Meter.Meter)
		}
	}
	names := make([]string, 0, len(dimensions))
	for name := range dimensions {
		names = append(names, name)
	}
	sort.Strings(names)
	fmt.Fprintf(out, "Dimensions:\n\n| Dimension | Value from |\n|---|---|\n")
	for _, name := range names {
		dimension := dimensions[name]
		value := fmt.Sprintf("`%s`", dimension.Path)
		if dimension.Note != "" {
			value = fmt.Sprintf("%s, %s", value, dimension.Note)
		}
		fmt.Fprintf(out, "| `%s` | %s |\n", name, value)
	}
	fmt.Fprintf(out, "\nTracked fields:\n\n")
	fmt.Fprintf(out, "| Field | Tracked as | Transition time | Owner |\n|---|---|---|---|\n")
	for _, field := range view.Fields {
		fmt.Fprintf(out, "| `%s` | %s | %s | %s |\n",
			field.Path, field.Kind, codeOrNone(field.TransitionTime),
			codeOrNone(field.OwnerResource))
	}
	fmt.Fprintf(out, "\n")
}

func writeBillingAccount(out *strings.Builder, sources Sources) {
	fmt.Fprintf(out, "## Billing account id\n\n")
	if versionHasField(sources.Versions, "external_billing_account_id") {
		fmt.Fprintf(out,
			"A usage event carries `external_billing_account_id` when the tenant has one. A "+
				"tenant without one omits it. The id labels where the event is reported in "+
				"the provider's billing system.\n\n")
		fmt.Fprintf(out,
			"The id never correlates usage. Match a correction to the event it corrects by "+
				"tenant, resource, meter, component, and billing dimensions. The id can change "+
				"while usage is in flight: metering stamps each event with the id current "+
				"when its round builds it and never rewrites published events.\n\n")
		return
	}
	fmt.Fprintf(out,
		"No published schema version carries an external billing account id yet. When a "+
			"version adds the field, it labels where each event is reported. It never "+
			"correlates usage, and a tenant without one omits it.\n\n")
}

func writeRegeneration(out *strings.Builder) {
	fmt.Fprintf(out, "## Regenerating this reference\n\n")
	fmt.Fprintf(out,
		"Do not edit this file or `%s` by hand. Run `make -C osac-metering contract`. CI "+
			"regenerates both and fails on any difference, so a change to a resource, a "+
			"meter, or a schema file arrives with its regenerated reference.\n", SchemaName)
}

// versionHasField reports whether any published version declares a payload field.
func versionHasField(versions []schema.Version, name string) bool {
	for _, version := range versions {
		for _, field := range version.Data {
			if field.Name == name {
				return true
			}
		}
	}
	return false
}

func requiredLabel(required bool) string {
	if required {
		return "required"
	}
	return "optional"
}

// displayName turns an event resource type into a heading, for example cluster_order into
// Cluster order.
func displayName(resourceType string) string {
	if name, known := displayNameOverrides[resourceType]; known {
		return name
	}
	words := strings.Split(strings.ReplaceAll(resourceType, "_", " "), " ")
	if len(words) > 0 && words[0] != "" {
		first := []rune(words[0])
		first[0] = unicode.ToUpper(first[0])
		words[0] = string(first)
	}
	return strings.Join(words, " ")
}

// displayNameOverrides names the resource types whose heading is not their event value with
// underscores replaced. A new resource type needs no entry.
var displayNameOverrides = map[string]string{
	schema.ResourceTypeExternalIP: "External IP",
	schema.ResourceTypeNATGateway: "NAT gateway",
}

// derivedDimensions explains a dimension whose value the calculator derives from another
// tracked field instead of copying it. It mirrors the computed dimensions in internal/rules.
var derivedDimensions = map[string]string{
	"ip_family": "derived from the address as `ipv4` or `ipv6`",
}

// joinOrNone renders a list for a table cell.
func joinOrNone(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}

// codeOrNone renders one value in backticks, or none when it is empty.
func codeOrNone(value string) string {
	if value == "" {
		return "none"
	}
	return "`" + value + "`"
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
