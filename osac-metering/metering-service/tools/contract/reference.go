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
	Name     string
	Path     string
	Computed bool
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
	writeTerms(&out)
	writeReadingRule(&out)
	writeLifecycle(&out, sources)
	writeTransport(&out, sources)
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
			Name: name, Path: path, Computed: rules.Computed(name),
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

// writeTerms defines the words the rest of the contract uses.
func writeTerms(out *strings.Builder) {
	fmt.Fprintf(out, "## Terms\n\n")
	fmt.Fprintf(out, "| Term | Meaning |\n|---|---|\n")
	terms := [][2]string{
		{"Usage event (heartbeat)", "One measured quantity over one interval for one resource and meter, sent as a CloudEvent on the tenant's usage topic."},
		{"Resource", "The billable object the usage is about, named by `resource_type` and `resource_id`."},
		{"Metering field registry", "The fields metering tracks on each resource, declared by annotations on the private protos in `proto/metering` and kept in sync with fulfillment's database triggers by drift tests."},
		{"Meter", "What is measured on a resource, for example a running VM's seconds."},
		{"Component", "The billable line a meter feeds. It is the meter name unless a rule revision sets another. For example, the `compute` meter feeds the `compute` component, the line the provider prices. The running seconds are the quantity, in `usage.quantity`."},
		{"Billing dimensions", "Name and value pairs that identify the priced variant of the usage, such as an instance type. A change re-keys the usage."},
		{"Interval", "One span of time: `from` inclusive, `to` exclusive, in UTC with whole seconds."},
		{"Correction", "A usage event with a negative quantity for the same resource, meter, component, and billing dimensions; it takes back usage already sent."},
		{"Reader", "The provider's system that reads usage events."},
		{"Rule", "A meter definition in a rule revision: the states that bill, the dimensions the meter carries, and the multiplier."},
		{"Rule revision", "An immutable set of meter rules with an effective date. The chart ships the default revision."},
		{"Schema version", "The published shape of a usage event, named by the suffix of its CloudEvent type, for example `v1`."},
		{"External billing account id", "The tenant's id in the provider's billing system, carried by usage events when the tenant has one. The provider supplies the id and owns its correctness. M360 keeps its own tenant mapping, so this is the provider's identifier."},
	}
	for _, term := range terms {
		fmt.Fprintf(out, "| %s | %s |\n", term[0], term[1])
	}
	fmt.Fprintf(out, "\n")
}

func writeReadingRule(out *strings.Builder) {
	fmt.Fprintf(out, "## How to read usage\n\n")
	fmt.Fprintf(out,
		"Add every usage event for one resource, meter, component, and set of billing "+
			"dimensions. A correction is a usage event with a negative quantity for the same "+
			"resource, meter, component, and billing dimensions; adding it takes back the "+
			"usage it corrects. The sum is the billable quantity.\n\n",
	)
	fmt.Fprintf(out,
		"Match a correction to the usage it corrects by tenant, resource, meter, component, "+
			"and billing dimensions. Never match by quantity, event id, or interval. A "+
			"correction is sent in a later round, so it never shares the id of the event it "+
			"corrects.\n\n",
	)
}

func writeLifecycle(out *strings.Builder, sources Sources) {
	fmt.Fprintf(out, "## Lifecycle\n\n")
	fmt.Fprintf(out,
		"A resource starts billing when a rule revision says its state is billable. Each "+
			"round, by default every `%s`, metering sends the difference since the last "+
			"event for the same usage.\n\n", sources.HeartbeatInterval)
	fmt.Fprintf(out,
		"A state or dimension change re-keys the usage: later events for the resource carry "+
			"the new dimensions, and each key is summed on its own.\n\n",
	)
	fmt.Fprintf(out,
		"When a resource stops being billable, metering sends a final correction for the "+
			"span it billed past the stop.\n\n",
	)
	fmt.Fprintf(out,
		"To rebuild a billable quantity from retained records, add the records for the key. "+
			"See Schema versions for how replay reads a record. The total matches what the "+
			"reader saw.\n\n",
	)
}

func writeTransport(out *strings.Builder, sources Sources) {
	fmt.Fprintf(out, "## Transport\n\n")
	fmt.Fprintf(out, "| Fact | The rule |\n|---|---|\n")
	fmt.Fprintf(out, "| Topic naming | One usage topic per tenant, named `%s<tenant>` |\n",
		schema.HeartbeatPrefix)
	fmt.Fprintf(out,
		"| Ordering | Each usage topic has one partition, so events arrive in the order "+
			"metering sent them |\n")
	fmt.Fprintf(out,
		"| Delivery | At least once. A repeat carries the same CloudEvent `id` |\n")
	retention := "bounds the recovery window"
	if sources.TopicRetention == "-1" {
		retention = "keeps every record"
	}
	fmt.Fprintf(out,
		"| Retention | Set per installation with `topics.retentionMs` (milliseconds); the "+
			"default `%s` %s |\n", sources.TopicRetention, retention)
	fmt.Fprintf(out,
		"| Compaction | None. Metering refuses a compacted usage topic and sets "+
			"`cleanup.policy` to `delete` |\n")
	fmt.Fprintf(out,
		"| Raw event log | Internal. Metering reads `%s<tenant>` and never publishes it |\n",
		schema.EventsPrefix)
	fmt.Fprintf(out,
		"| Failed records | Internal. OSAC's adapters write rejected events to "+
			"`%s<tenant>` |\n", schema.DeadLetterPrefix)
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
			"the billing account id (see Billing account id), so a resend carries the same "+
			"id.\n\n",
	)
	fmt.Fprintf(out,
		"A reader drops an event whose id it has already seen and never recomputes the id.\n\n",
	)
}

func writeSchemaVersions(out *strings.Builder, sources Sources) {
	fmt.Fprintf(out, "## Schema versions\n\n")
	fmt.Fprintf(out,
		"Every usage event names its schema version in the CloudEvent `type`: the suffix "+
			"after the last dot, for example `%s` in `%s`.\n\n",
		schema.SchemaVersion, schema.HeartbeatType)
	fmt.Fprintf(out,
		"A published schema never changes. A new field is a new version file with the next "+
			"version and the date it takes effect. The old file stays, so its records stay "+
			"readable.\n\n")
	fmt.Fprintf(out,
		"Versions are add-only: a new field is added, and no field is removed or given a "+
			"new type. Replay decodes every retained record with the current payload shape, "+
			"so the current shape must stay a superset of every published version; that is "+
			"what lets one reader read records written under older versions.\n\n")
	fmt.Fprintf(out,
		"A version's effective date is the earliest date its events may carry it, measured by "+
			"`heartbeat_time`, the round time that wrote the record. A reader that filters on "+
			"the CloudEvent `time` (the interval start) uses `heartbeat_time` for the version "+
			"boundary. Metering writes its current version unconditionally and never selects "+
			"a version by date, so the version must not take effect after the release that "+
			"ships it. Metering refuses to load a current version whose effective date is in "+
			"the future; an older, future-dated version may wait in the tree until its "+
			"release.\n\n")
	if sources.TopicRetention == "-1" {
		fmt.Fprintf(out,
			"A published version stays readable for as long as its records are retained. "+
				"Retention is indefinite by default, so no version has been retired.\n\n")
	} else {
		fmt.Fprintf(out,
			"A published version stays readable for as long as its records are retained. "+
				"Retention is bounded by `topics.retentionMs` (`%s`), so a version's "+
				"records stay readable for that window.\n\n", sources.TopicRetention)
	}
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
			"support before that version's effective date. OSAC's adapters forward the "+
			"CloudEvent type unchanged; they never relabel an event as another version.\n\n")

	for _, version := range sources.Versions {
		fmt.Fprintf(out, "### Event fields of version `%s`\n\n", version.Version)
		fmt.Fprintf(out, "#### CloudEvent attributes\n\n")
		writeFieldTable(out, version.Envelope)
		fmt.Fprintf(out, "\n#### Payload\n\n")
		writeFieldTable(out, version.Data)
		fmt.Fprintf(out, "\n")
	}
	fmt.Fprintf(out,
		"The billable line that `component` names is listed in the Component column of "+
			"each resource table below.\n\n")
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
		description := field.Description
		if field.Name == "billing_dimensions" {
			description += " The meter's dimensions are listed in the Dimensions table of " +
				"each resource section; the identity dimensions are listed under " +
				"Resources and meters."
		}
		fmt.Fprintf(out, "| `%s` | %s | %s | %s | %s |\n",
			name, fieldType, requiredLabel(field.Required), unit, description)
		if len(field.Fields) > 0 {
			writeFieldRows(out, name+".", field.Fields)
		}
	}
}

func writeResources(out *strings.Builder, sources Sources, views []resourceView) {
	fmt.Fprintf(out, "## Resources and meters\n\n")
	resourceTypes := make([]string, 0, len(views))
	for _, view := range views {
		resourceTypes = append(resourceTypes, "`"+view.Type+"`")
	}
	fmt.Fprintf(out,
		"The values below are the ones events carry in `resource_type`: %s.\n\n",
		strings.Join(resourceTypes, ", "))
	fmt.Fprintf(out,
		"The default rule revision is revision `%s`, effective from %s. It is the newest "+
			"revision the chart ships, and it is immutable: a rule change ships as the next "+
			"revision. This reference documents the newest shipped revision only; earlier "+
			"revisions stay in the chart and are immutable.\n\n",
		sources.Revision.Number, sources.Revision.EffectiveAt.Format("2006-01-02T15:04:05Z"))
	writeRuleExample(out, sources, views)
	fmt.Fprintf(out,
		"A provider that needs different meters ships the next immutable revision with a "+
			"later effective date; the chart ships revision `%s`. See [the metering chart "+
			"rules](../../charts/osac-metering/rules/).\n\n", sources.Revision.Number)
	multipliers := multiplierMeters(views)
	if len(multipliers) == 0 {
		fmt.Fprintf(out,
			"`Multiplier` names the tracked field metering has already multiplied into "+
				"`usage.quantity`, or `none`. A reader must not apply it again. No meter in "+
				"revision `%s` uses a multiplier.\n\n", sources.Revision.Number)
	} else {
		fmt.Fprintf(out,
			"`Multiplier` names the tracked field metering has already multiplied into "+
				"`usage.quantity`, or `none`. A reader must not apply it again. In revision "+
				"`%s`, these meters use a multiplier: %s.", sources.Revision.Number,
			strings.Join(multipliers, "; "))
		if volumeCapacityMultiplier(views) {
			fmt.Fprintf(out, " A 100 GiB volume's quantity is already multiplied by 100.")
		}
		fmt.Fprintf(out, "\n\n")
	}
	writeIdentityDimensions(out)
	for _, view := range views {
		writeResource(out, view)
	}
}

// writeRuleExample prints the default revision's YAML with a key legend. The rule file is
// immutable once released, so the reference explains its keys here instead of changing its
// bytes.
func writeRuleExample(out *strings.Builder, sources Sources, views []resourceView) {
	fmt.Fprintf(out, "### Rule example\n\n")
	fmt.Fprintf(out,
		"This is the default revision `%s` that the chart ships, copied from its rule file. "+
			"The table and key legend below explain the values.\n\n", sources.Revision.Number)
	yaml := sources.Revision.MetersYAML
	if !strings.HasSuffix(yaml, "\n") {
		yaml += "\n"
	}
	fmt.Fprintf(out, "```yaml\n%s```\n\n", yaml)
	fmt.Fprintf(out,
		"The rule file names each resource by its proto message; events carry the wire value:\n\n")
	fmt.Fprintf(out, "| Rule file `resource` | Event `resource_type` |\n|---|---|\n")
	for _, view := range views {
		fmt.Fprintf(out, "| `%s` | `%s` |\n", view.Message, view.Type)
	}
	fmt.Fprintf(out, "\n")
	fmt.Fprintf(out, "| Key | Meaning |\n|---|---|\n")
	keys := [][2]string{
		{"`resource`", "The resource type the rule bills, by proto message name. The table above maps it to the event value."},
		{"`meter`", "The meter name. Events carry it in `meter`."},
		{"`unit`", "What the meter measures. Events carry it in `usage.unit` and `billing_dimensions.meter_type`."},
		{"`billable_when.states`", "The resource states that bill."},
		{"`turns_billable_once`", "States that confirm a deferred billable span. A span that closes before one never bills."},
		{"`billable_when.fields`", "Conditions on fields fixed at creation: the expected text, or `set` for presence."},
		{"`multiplier`", "A tracked field the quantity is multiplied by. Omitted means 1."},
		{"`dimensions`", "The YAML entries are keys in `billing_dimensions`; each resource section lists the tracked field the value comes from."},
	}
	for _, key := range keys {
		fmt.Fprintf(out, "| %s | %s |\n", key[0], key[1])
	}
	fmt.Fprintf(out, "\n")
}

// multiplierMeters lists the meters of the shipped revision that scale their quantity.
func multiplierMeters(views []resourceView) []string {
	var result []string
	for _, view := range views {
		for _, meter := range view.Meters {
			if meter.Meter.Multiplier == "" {
				continue
			}
			result = append(result, fmt.Sprintf(
				"%s `%s` by `%s`", displayName(view.Type), meter.Meter.Meter, meter.Meter.Multiplier,
			))
		}
	}
	return result
}

// volumeCapacityMultiplier reports whether the revision scales Volume capacity by its
// provisioned size, which the reference's 100 GiB example explains.
func volumeCapacityMultiplier(views []resourceView) bool {
	for _, view := range views {
		if view.Type != schema.ResourceTypeVolume {
			continue
		}
		for _, meter := range view.Meters {
			if meter.Meter.Meter == "capacity" && meter.Meter.Multiplier == "provisioned_size_gib" {
				return true
			}
		}
	}
	return false
}

func writeIdentityDimensions(out *strings.Builder) {
	fmt.Fprintf(out,
		"Every usage event also carries these identity dimensions in `billing_dimensions`, "+
			"besides the meter's own dimensions:\n\n")
	fmt.Fprintf(out, "| Dimension | Value |\n|---|---|\n")
	for _, identity := range rules.IdentityDimensions {
		fmt.Fprintf(out, "| `%s` | %s |\n", identity.Name, identity.Description)
	}
	fmt.Fprintf(out,
		"\nA boolean dimension such as `attached` carries `true` or `false`. A dimension "+
			"with no value for the interval is omitted from `billing_dimensions`; do not read "+
			"a missing dimension as an empty string.\n\n")
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
		if dimension.Computed {
			if note := rules.ComputedDescription(name); note != "" {
				value += ", " + note
			}
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
	if first := firstVersionWithField(sources.Versions, "external_billing_account_id"); first != "" {
		earlier := ""
		if first != sources.Versions[0].Version {
			earlier = "; earlier versions do not carry it"
		}
		fmt.Fprintf(out,
			"Schema version `%s` and later carry `external_billing_account_id` when the "+
				"tenant has one%s. A tenant without one omits it. The field is optional. The "+
				"provider supplies it and ensures it matches the account in their billing "+
				"system; OSAC copies it as given. M360 keeps its own mapping.\n\n",
			first, earlier)
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
			"meter, or a schema file arrives with its regenerated reference.\n\n", SchemaName)
	fmt.Fprintf(out,
		"Every schema version file is pinned by SHA-256 in "+
			"`osac-metering/schema/published.sha256`; a new version adds its own "+
			"entry, and CI fails when a published file differs from it.\n")
}

// firstVersionWithField returns the first version that declares a payload field, or an empty
// string when no version does.
func firstVersionWithField(versions []schema.Version, name string) string {
	for _, version := range versions {
		for _, field := range version.Data {
			if field.Name == name {
				return version.Version
			}
		}
	}
	return ""
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
