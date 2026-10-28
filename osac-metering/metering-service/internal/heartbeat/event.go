/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

// Package heartbeat builds and reads metering heartbeat CloudEvents. A heartbeat is one piece
// of usage: what resource and meter, over which interval, and how much, as a difference the
// round sends.
package heartbeat

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"

	"github.com/osac-project/osac-metering/internal/database"
	"github.com/osac-project/osac-metering/schema"
)

// Type is the CloudEvent type of a heartbeat.
const Type = schema.HeartbeatType

// FormatMicro renders a quantity in micro units as exact fixed-point text with six decimals.
func FormatMicro(quantityMicro int64) string {
	sign := ""
	if quantityMicro < 0 {
		sign = "-"
		quantityMicro = -quantityMicro
	}
	whole := quantityMicro / 1_000_000
	fraction := quantityMicro % 1_000_000
	return fmt.Sprintf("%s%d.%06d", sign, whole, fraction)
}

// ParseMicro parses fixed-point text into micro units. It fails on numbers that need more
// than six decimals or that do not fit in micro units.
func ParseMicro(text string) (int64, error) {
	rational, ok := new(big.Rat).SetString(text)
	if !ok {
		return 0, fmt.Errorf("%q is not a decimal number", text)
	}
	scaled := new(big.Rat).Mul(rational, big.NewRat(1_000_000, 1))
	if !scaled.IsInt() {
		return 0, fmt.Errorf("%q needs more than six decimals", text)
	}
	if !scaled.Num().IsInt64() {
		// Int64 is undefined when the numerator is out of range, and catch-up decodes
		// topic records with this function: a hostile or corrupt quantity must be
		// rejected, not wrapped into garbage usage.
		return 0, fmt.Errorf("%q does not fit in micro units", text)
	}
	return scaled.Num().Int64(), nil
}

// New builds the heartbeat CloudEvent for one difference piece. The ID names which piece of
// usage this is: tenant, resource, meter, component, dimensions, interval, and round time. It
// leaves out the quantity, so a heartbeat the relay resends after a crash keeps its ID and the
// billing provider drops the copy.
func New(
	tenant string,
	key database.LedgerKey,
	from, to time.Time,
	quantityMicro int64,
	unit string,
	heartbeatTime time.Time,
	lastAppliedOffset int64,
	rulesRevision string,
) (cloudevents.Event, error) {
	dimensions, err := decodeDimensions(key.Dimensions)
	if err != nil {
		return cloudevents.Event{}, err
	}
	data := schema.HeartbeatData{
		TenantID:          tenant,
		ResourceType:      schema.ProtoResourceTypeToSchema(key.ResourceType),
		ResourceID:        key.ResourceID,
		Meter:             key.Meter,
		Component:         key.Component,
		BillingDimensions: dimensions,
		Usage: schema.Usage{
			Semantics: schema.UsageSemanticsInterval,
			From:      formatTime(from),
			To:        formatTime(to),
			Quantity:  FormatMicro(quantityMicro),
			Unit:      unit,
			Precision: schema.UsagePrecisionSecond,
		},
		HeartbeatTime:     formatTime(heartbeatTime),
		LastAppliedOffset: lastAppliedOffset,
		RulesRevision:     rulesRevision,
	}
	id := ID(tenant, key, from, to, heartbeatTime)

	event := cloudevents.NewEvent()
	event.SetID(id)
	event.SetType(Type)
	event.SetSource("osac-metering")
	event.SetTime(from.UTC())
	// The extensions name the resource the way the adapters and the E2E tooling filter
	// events, like the lifecycle events of the previous pipeline did.
	event.SetExtension(schema.ExtResourceID, key.ResourceID)
	event.SetExtension(schema.ExtResourceType, schema.ProtoResourceTypeToSchema(key.ResourceType))
	event.SetExtension(schema.ExtTenant, tenant)
	if err := event.SetData(cloudevents.ApplicationJSON, data); err != nil {
		return cloudevents.Event{}, fmt.Errorf("encoding heartbeat: %w", err)
	}
	return event, nil
}

// ID computes the heartbeat's ID.
func ID(
	tenant string, key database.LedgerKey, from, to, heartbeatTime time.Time,
) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf(
		"%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s",
		tenant, key.ResourceType, key.ResourceID, key.Meter, key.Component, key.Dimensions,
		formatTime(from), formatTime(to), formatTime(heartbeatTime),
	)))
	return hex.EncodeToString(digest[:])
}

// Data reads the heartbeat's payload from a CloudEvent.
func Data(event cloudevents.Event) (schema.HeartbeatData, error) {
	var data schema.HeartbeatData
	if err := json.Unmarshal(event.Data(), &data); err != nil {
		return schema.HeartbeatData{}, fmt.Errorf("decoding heartbeat: %w", err)
	}
	if data.TenantID == "" || data.ResourceID == "" || data.Meter == "" {
		return schema.HeartbeatData{}, fmt.Errorf("heartbeat %s is missing identity fields", event.ID())
	}
	if data.Usage.From == "" || data.Usage.To == "" || data.Usage.Quantity == "" {
		return schema.HeartbeatData{}, fmt.Errorf("heartbeat %s is missing usage fields", event.ID())
	}
	return data, nil
}

// Interval parses the heartbeat's usage interval.
func Interval(data schema.HeartbeatData) (from, to time.Time, err error) {
	from, err = time.Parse(time.RFC3339Nano, data.Usage.From)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("parsing usage from: %w", err)
	}
	to, err = time.Parse(time.RFC3339Nano, data.Usage.To)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("parsing usage to: %w", err)
	}
	return from.UTC(), to.UTC(), nil
}

// HeartbeatTime parses the heartbeat's round time.
func HeartbeatTime(data schema.HeartbeatData) (time.Time, error) {
	result, err := time.Parse(time.RFC3339Nano, data.HeartbeatTime)
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing heartbeat time: %w", err)
	}
	return result.UTC(), nil
}

func formatTime(moment time.Time) string {
	// Whole seconds: the coarsest source clock in the pipeline is the Kubernetes CR
	// timestamp, and no boundary is known more finely than that.
	return moment.UTC().Truncate(time.Second).Format("2006-01-02T15:04:05Z07:00")
}

// decodeDimensions reads the key's dimension text. Corrupted text fails the heartbeat: the
// dimensions are the billing identity, and an empty set would re-key every piece.
func decodeDimensions(encoded string) (map[string]string, error) {
	dimensions := map[string]string{}
	if err := json.Unmarshal([]byte(encoded), &dimensions); err != nil {
		return nil, fmt.Errorf("decoding dimensions: %w", err)
	}
	return dimensions, nil
}
