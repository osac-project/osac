/*
Copyright (c) 2026 Red Hat, Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in
compliance with the License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0
*/

// Package main is the worked example from the metering event contract: a
// small reader that consumes every tenant's heartbeat topics and prints the
// billable quantity per tenant.
//
// It proves the reading rule and is not a production adapter. It keeps its
// deduplication state in memory, so a restart forgets the ids it has seen.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"

	cloudevents "github.com/cloudevents/sdk-go/v2"

	"github.com/osac-project/osac-metering/schema"
)

// UsageKey groups the events the contract bills together: one tenant, one
// resource, one meter, one component, and one set of billing dimensions.
// Dimensions holds the canonical encoding of the map, not the readable text,
// so two different maps cannot form one key.
type UsageKey struct {
	TenantID     string
	ResourceType string
	ResourceID   string
	Meter        string
	Component    string
	Dimensions   string
}

// Row is one billable total: the quantity summed for a UsageKey.
type Row struct {
	Key        UsageKey
	Dimensions string
	Quantity   string
	Unit       string
}

// Totals drops repeated CloudEvent ids and adds every remaining event's
// quantity per UsageKey. A correction is a negative quantity, so adding it
// subtracts the usage it reverses. Sarama calls ConsumeClaim once per claimed
// partition, so several goroutines share a Totals and every map and counter is
// guarded by mu.
type Totals struct {
	mu       sync.Mutex
	seen     map[string]struct{}
	sums     map[UsageKey]*big.Rat
	units    map[UsageKey]string
	displays map[UsageKey]string
	accepted int
	repeats  int
}

// NewTotals returns an empty set of totals.
func NewTotals() *Totals {
	return &Totals{
		seen:     make(map[string]struct{}),
		sums:     make(map[UsageKey]*big.Rat),
		units:    make(map[UsageKey]string),
		displays: make(map[UsageKey]string),
	}
}

// Add sums one usage event. It returns false when the event repeats an id it
// has already seen, so the caller can count the repeat without billing it.
func (t *Totals) Add(event cloudevents.Event) (bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	id := event.ID()
	if id == "" {
		return false, fmt.Errorf("usage event has no CloudEvent id")
	}
	if _, repeated := t.seen[id]; repeated {
		t.repeats++
		return false, nil
	}
	if event.Type() != schema.HeartbeatType {
		return false, fmt.Errorf("event %s has type %q, not %q: keep it and add support for the version", id, event.Type(), schema.HeartbeatType)
	}

	var data schema.HeartbeatData
	if err := event.DataAs(&data); err != nil {
		return false, fmt.Errorf("decoding event %s: %w", id, err)
	}
	quantity, ok := new(big.Rat).SetString(data.Usage.Quantity)
	if !ok {
		return false, fmt.Errorf("event %s has quantity %q, which is not a decimal number", id, data.Usage.Quantity)
	}

	dimensions, err := dimensionKey(data.BillingDimensions)
	if err != nil {
		return false, fmt.Errorf("event %s: %w", id, err)
	}

	key := UsageKey{
		TenantID:     data.TenantID,
		ResourceType: data.ResourceType,
		ResourceID:   data.ResourceID,
		Meter:        data.Meter,
		Component:    data.Component,
		Dimensions:   dimensions,
	}
	if t.sums[key] == nil {
		t.sums[key] = new(big.Rat)
		t.units[key] = data.Usage.Unit
		t.displays[key] = dimensionText(data.BillingDimensions)
	}
	t.sums[key].Add(t.sums[key], quantity)
	t.seen[id] = struct{}{}
	t.accepted++
	return true, nil
}

// Counts returns how many events were summed and how many repeats were
// dropped.
func (t *Totals) Counts() (accepted, repeats int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.accepted, t.repeats
}

// Rows returns the billable totals in a stable order.
func (t *Totals) Rows() []Row {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.rowsLocked()
}

// rowsLocked returns the rows from the maps. The caller holds t.mu.
func (t *Totals) rowsLocked() []Row {
	rows := make([]Row, 0, len(t.sums))
	for key, sum := range t.sums {
		rows = append(rows, Row{
			Key:        key,
			Dimensions: t.displays[key],
			Quantity:   sum.FloatString(6),
			Unit:       t.units[key],
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i].Key, rows[j].Key
		if a.TenantID != b.TenantID {
			return a.TenantID < b.TenantID
		}
		if a.ResourceType != b.ResourceType {
			return a.ResourceType < b.ResourceType
		}
		if a.ResourceID != b.ResourceID {
			return a.ResourceID < b.ResourceID
		}
		if a.Meter != b.Meter {
			return a.Meter < b.Meter
		}
		if a.Component != b.Component {
			return a.Component < b.Component
		}
		return a.Dimensions < b.Dimensions
	})
	return rows
}

// tenantUnit is the key of a billable total in the printed output.
type tenantUnit struct {
	tenant string
	unit   string
}

// tenantTotal is one printed billable total.
type tenantTotal struct {
	tenantUnit
	quantity *big.Rat
}

// snapshot returns the rows and the per-tenant totals under one lock, so the
// printed output comes from a single consistent state. Print must not call a
// locked method while holding the lock.
func (t *Totals) snapshot() ([]Row, []tenantTotal) {
	t.mu.Lock()
	defer t.mu.Unlock()

	rows := t.rowsLocked()
	totals := make(map[tenantUnit]*big.Rat)
	for _, row := range rows {
		key := tenantUnit{tenant: row.Key.TenantID, unit: row.Unit}
		if totals[key] == nil {
			totals[key] = new(big.Rat)
		}
		totals[key].Add(totals[key], t.sums[row.Key])
	}
	keys := make([]tenantUnit, 0, len(totals))
	for key := range totals {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].tenant != keys[j].tenant {
			return keys[i].tenant < keys[j].tenant
		}
		return keys[i].unit < keys[j].unit
	})
	sorted := make([]tenantTotal, 0, len(keys))
	for _, key := range keys {
		sorted = append(sorted, tenantTotal{tenantUnit: key, quantity: totals[key]})
	}
	return rows, sorted
}

// Print writes one line per billable total and one total per tenant and unit.
func (t *Totals) Print(w io.Writer) error {
	rows, totals := t.snapshot()
	writer := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "TENANT\tRESOURCE TYPE\tRESOURCE\tMETER\tCOMPONENT\tDIMENSIONS\tQUANTITY\tUNIT"); err != nil {
		return fmt.Errorf("writing totals header: %w", err)
	}
	for _, row := range rows {
		dimensions := row.Dimensions
		if dimensions == "" {
			dimensions = "-"
		}
		if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			row.Key.TenantID, row.Key.ResourceType, row.Key.ResourceID, row.Key.Meter,
			row.Key.Component, dimensions, row.Quantity, row.Unit); err != nil {
			return fmt.Errorf("writing totals row: %w", err)
		}
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("writing totals: %w", err)
	}
	for _, total := range totals {
		if _, err := fmt.Fprintf(w, "BILLABLE TOTAL %s: %s %s\n", total.tenant, total.quantity.FloatString(6), total.unit); err != nil {
			return fmt.Errorf("writing billable total: %w", err)
		}
	}
	return nil
}

// dimensionKey renders a dimension map as its canonical grouping key.
// encoding/json sorts map keys, so equal maps encode identically, and its
// quoting keeps a value containing a comma or equals sign from merging with a
// different map.
func dimensionKey(dimensions map[string]string) (string, error) {
	if len(dimensions) == 0 {
		return "", nil
	}
	encoded, err := json.Marshal(dimensions)
	if err != nil {
		return "", fmt.Errorf("encoding billing dimensions: %w", err)
	}
	return string(encoded), nil
}

// dimensionText renders a dimension map for display as sorted name=value
// pairs. Only the display uses this form; grouping uses dimensionKey.
func dimensionText(dimensions map[string]string) string {
	if len(dimensions) == 0 {
		return ""
	}
	pairs := make([]string, 0, len(dimensions))
	for name, value := range dimensions {
		pairs = append(pairs, name+"="+value)
	}
	sort.Strings(pairs)
	return strings.Join(pairs, ",")
}
